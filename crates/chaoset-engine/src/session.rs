//! 输入法会话状态机：按键组词、候选词排序、分页、渐进选择与联想。

use std::sync::Arc;

use crate::dict::Dictionary;
use crate::punct::PunctState;
use crate::s2t::to_traditional;
use crate::shuangpin::{ShuangpinDecoder, ShuangpinScheme};
use crate::syllable::{tokenize_pinyin, Token, TypoCorrection};
use crate::userdict::UserDict;

// 上屏学习（调频/共现/历史/词组进化、渐进整句固化、分段推断）按职责独立成文件。
mod learn;

pub const PAGE_SIZE: usize = 6;

// ── 匹配层级常量（对齐 Go compose_rank.go，数值相对关系不能变）────────
// 层级是第一排序维度：跨层的先后由 rank 决定，词频打分只在同层内起作用。
const RANK_EXACT: i32 = 0; // 全拼单词完全匹配（jingchang → 经常）
const RANK_INITIAL_EXACT: i32 = 1; // 简拼/混拼单词完全匹配（zg → 中国）
// 末段残缺续接（zhonggu → 中国，daxu → 大学）：存在干净解析时落在该层，
// 严格排在完整消费输入的精确匹配之后；无法干净切分时改用
// RANK_CONTINUATION_LEAD 领跑（见 compose 里的 continuation_rank）。
const RANK_CONTINUATION: i32 = 2;
const RANK_INITIAL_PREFIX: i32 = 3; // 简拼偏长词（zg → 中国人民）
const RANK_PREFIX: i32 = 4; // 全拼偏长词续接（jing → 经济）
const RANK_BACKOFF: i32 = 5; // 逐级回退前缀（nihaox → 你好）
const RANK_CONTINUATION_LEAD: i32 = 6; // 无法干净切分时的续接领跑（quanmia → 全面）
const RANK_TYPO_FIX: i32 = 7; // 错拼修正候选初始层级（最低，仅保证列表末尾可见）

/// 按切分边界重建预编辑，并**保留用户输入的大小写**（对齐 Go preeditWithCase）。
///
/// 预编辑是「按回车会原样上屏的内容」的预览：用户敲 NIHAO 时看到 NI'HAO。
/// 做法是把输入去掉显式分隔符后与音节文本逐个字符对齐——长度不匹配（别名
/// 归一 lue→lve、双拼解码等）或字符不同（同上）时退回音节文本本身。
fn preedit_with_case(input: &str, tokens: &[Token]) -> String {
    let src: Vec<char> = input.chars().filter(|c| *c != '\'').collect();
    let canon: Vec<char> = tokens.iter().flat_map(|t| t.text.chars()).collect();
    if src.len() != canon.len() {
        return tokens
            .iter()
            .map(|t| t.text.as_str())
            .collect::<Vec<_>>()
            .join("'");
    }
    let mut out = String::new();
    let mut idx = 0;
    for (i, tok) in tokens.iter().enumerate() {
        if i > 0 {
            out.push('\'');
        }
        for c in tok.text.chars() {
            // 音节文本是规范化小写；原字符只可能在大小写上不同。
            if src[idx].to_ascii_lowercase() == c {
                out.push(src[idx]);
            } else {
                out.push(c);
            }
            idx += 1;
        }
    }
    out
}

/// 输入形态判定结果（Go composeShape 的 Rust 版，纯函数产出）。
/// 决定续接层级、常规路径是否走昂贵的 prefix 收集、超段仲裁口径与错拼
/// 走不走纠错路径。
#[derive(Debug, Clone, Default)]
struct ComposeShape {
    /// 最短切分的段数（0 = 没有任何合法切分）。
    min_pattern_len: usize,
    /// 存在一条「所有音节都是完整全拼、没有声母简拼段」的最干净解析。
    all_exact_shape: bool,
    /// 错拼形态（互斥的细分层级）。
    typo_suspicious: bool,
    /// 最优切分里非末位有单字母声母碎片（zhengchagn 的 g）。
    mid_suspicious: bool,
    /// 末位是孤立声母碎片（chifanl 的 l / nihap 的 p）。
    tail_suspicious: bool,
    /// 含反序双字母且不能按正常切分解释：只走归位修正。
    mistake_input: bool,
    /// 超段仲裁口径：首选切分段数、所有切分最多段数、是否有简拼段。
    input_segs: i32,
    max_input_segs: i32,
    input_has_initials: bool,
}

/// 计算输入形态（Go analyzeShape 的逐项镜像）。
fn analyze_shape(input: &str, patterns: &[Vec<Token>]) -> ComposeShape {
    let mut sh = ComposeShape {
        all_exact_shape: true,
        ..Default::default()
    };
    if let Some(first) = patterns.first() {
        sh.min_pattern_len = first.len();
    }
    for pat in patterns {
        if pat.len() < sh.min_pattern_len {
            sh.min_pattern_len = pat.len();
        }
    }
    // 输入的「最干净解析」：所有音节都是完整全拼、且没有任何一段被拆成
    // 声母简拼。只有存在这样一条干净解析时，续接才退居次要位置。
    for pat in patterns {
        if pat.len() != sh.min_pattern_len {
            continue;
        }
        if !sh.all_exact_shape {
            break;
        }
        for tok in pat {
            if !tok.is_exact {
                sh.all_exact_shape = false;
                break;
            }
        }
    }
    sh.typo_suspicious = crate::syllable::misspelled_from_patterns(input, patterns);
    sh.mid_suspicious = !sh.typo_suspicious && crate::syllable::has_mid_isolated_initial(patterns);
    sh.tail_suspicious = !sh.typo_suspicious
        && !sh.mid_suspicious
        && crate::syllable::has_tail_isolated_initial(patterns);
    // 含「反序双字母」的输入：拼音里不合法，属最高置信度手误。整串能切成
    // 完整音节或纯声母简拼时，反序组合可能跨音节/声母边界（gongneng 的
    // gn = gong+neng；gn 简拼 = gong+neng），不能当手误。
    sh.mistake_input = !crate::syllable::has_clean_full_split(patterns)
        && !crate::syllable::all_initial_patterns(patterns)
        && crate::syllable::has_common_mistake_cluster(input);
    if let Some(first) = patterns.first() {
        sh.input_segs = first.len() as i32;
        sh.input_has_initials = first.iter().any(|t| !t.is_exact);
        for pat in patterns {
            if pat.len() as i32 > sh.max_input_segs {
                sh.max_input_segs = pat.len() as i32;
            }
        }
    }
    sh
}

/// 错拼路径的共享状态（Go typoState）。
#[derive(Default)]
struct TypoState {
    /// 已加入候选池的纠错词条（词 + 音节串）。
    added: std::collections::HashSet<String>,
    /// 来自纠错后「按已有词切分」DP 的候选。
    phrase_added: std::collections::HashSet<String>,
    /// 该纠错来自物理邻键替换（保留原词频）。
    is_near: std::collections::HashMap<String, bool>,
    /// 来自反序双字母归位修正（mistake_input 时无条件提升）。
    mistake_keys: std::collections::HashSet<String>,
    /// 修正串长度（tail_suspicious 时的完整性门槛）。
    lens: std::collections::HashMap<String, usize>,
    /// 是否命中过「整词级」结果：有整词级结果时组合候选不得抢到精确层。
    word_found: bool,
    /// 反序归位修正后的首个音节（只有词组候选时补首音节单字）。
    mistake_syllable: String,
    /// 归位修正在 fixes 里的条目数。
    cluster_fix_count: usize,
    /// 已做的纠错分词 DP 次数。
    phrase_queries: usize,
}

/// 候选池 key：词 + \u0000 + 音节串（Go userKey/add 的 key 口径）。
fn pool_key(word: &str, syllables: &str) -> String {
    format!("{}\u{0}{}", word, syllables)
}

/// 候选池（Go composeState.items + seen 的 Rust 版）：保插入序 + 按
/// 「词\u0000音节串」去重合并。插入序是稳定排序的次键（同层同分候选的
/// 先后），必须保留——HashMap 遍历序会破坏确定性。
#[derive(Default)]
struct Pool {
    map: std::collections::HashMap<String, (Candidate, i32, i32)>,
    order: Vec<String>,
}

impl Pool {
    fn new() -> Self {
        Self::default()
    }

    fn is_empty(&self) -> bool {
        self.map.is_empty()
    }

    /// 插入或合并候选（Go composeState.add 的合并规则）：更高分覆盖 Score/
    /// FromUser；FromUser 只升不降；rank 只降不升；typo_fix 只升不降。
    #[allow(clippy::too_many_arguments)]
    fn add(
        &mut self,
        word: &str,
        display_text: String,
        syllables: String,
        score: i32,
        from_user: bool,
        rank: i32,
        typo_fix: bool,
    ) {
        let key = pool_key(word, &syllables);
        if let Some(slot) = self.map.get_mut(&key) {
            if score > slot.1 {
                slot.1 = score;
                slot.0.from_user = from_user;
            } else if from_user {
                slot.0.from_user = true;
            }
            if rank < slot.2 {
                slot.2 = rank;
            }
            if typo_fix {
                slot.0.typo_fix = true;
            }
            return;
        }
        self.order.push(key.clone());
        self.map.insert(
            key,
            (
                Candidate {
                    text: display_text,
                    syllables,
                    score: 0,
                    from_user,
                    typo_fix,
                },
                score,
                rank,
            ),
        );
    }

    /// 按插入序取出（排序前的稳定基准）。
    fn into_ordered_vec(self) -> Vec<(Candidate, i32, i32)> {
        let mut out = Vec::with_capacity(self.order.len());
        for key in &self.order {
            if let Some(v) = self.map.get(key) {
                out.push(v.clone());
            }
        }
        out
    }

    /// 按插入序的只读快照（判定用）。
    fn snapshot(&self) -> Vec<(Candidate, i32, i32)> {
        self.order
            .iter()
            .filter_map(|k| self.map.get(k).cloned())
            .collect()
    }

    fn retain<F: Fn(&Candidate, i32, i32) -> bool>(&mut self, f: F) {
        self.order.retain(|k| {
            self.map
                .get(k)
                .map(|(c, s, r)| f(c, *s, *r))
                .unwrap_or(false)
        });
        self.map.retain(|_, (c, s, r)| f(c, *s, *r));
    }

    /// 按 key 设置 rank（阶段提升用）。
    fn set_rank(&mut self, key: &str, rank: i32) {
        if let Some(slot) = self.map.get_mut(key) {
            slot.2 = rank;
        }
    }
}

/// 错拼修正的阶段一（Go addTypoCorrections）：生成修正串 → 查词库 → 以最低
/// 层 rankTypoFix 加入候选池并打 typo_fix 标记；对可组合的修正做分词 DP。
/// 只有输入形态被判定为可疑（typo/mid/tail）时才执行。
#[allow(clippy::too_many_arguments)]
/// 单条切分路径的模糊变体笛卡尔积（Go expandTokenVariants）：完整音节按
/// fuzzyVariants 展开、碎片原样保留，首条必为原路径，封顶 maxFuzzyPaths=8。
fn expand_token_variants(tokens: &[Token], fuzzy: bool) -> Vec<Vec<Token>> {
    const MAX_FUZZY_PATHS: usize = 8;
    if !fuzzy || tokens.is_empty() {
        return vec![tokens.to_vec()];
    }
    let sets: Vec<Vec<Token>> = tokens
        .iter()
        .map(|tok| {
            if tok.is_exact {
                crate::fuzzy::fuzzy_syllable_variants(&tok.text)
                    .into_iter()
                    .map(|fz| Token {
                        text: fz,
                        is_exact: true,
                    })
                    .collect()
            } else {
                vec![tok.clone()]
            }
        })
        .collect();
    let mut out: Vec<Vec<Token>> = Vec::new();
    let mut cur: Vec<Token> = Vec::new();
    fn build(
        i: usize,
        sets: &[Vec<Token>],
        cur: &mut Vec<Token>,
        out: &mut Vec<Vec<Token>>,
        max: usize,
    ) {
        if out.len() >= max {
            return;
        }
        if i == sets.len() {
            out.push(cur.clone());
            return;
        }
        for v in &sets[i] {
            cur.push(v.clone());
            build(i + 1, sets, cur, out, max);
            cur.pop();
        }
    }
    build(0, &sets, &mut cur, &mut out, MAX_FUZZY_PATHS);
    out
}

fn add_typo_corrections(
    dict: &Dictionary,
    user: Option<&UserDict>,
    input: &str,
    shape: &ComposeShape,
    prev: Option<&str>,
    traditional: bool,
    pool: &mut Pool,
    typo: &mut TypoState,
) {
    const TYPO_MAX_CORRECTIONS: usize = 32;
    const TYPO_MAX_QUERIES: usize = 32;
    const NON_ADJACENT_TYPO_PENALTY: i32 = 15;
    const TYPO_MAX_PHRASE_QUERIES: usize = 16;

    let tail_suspicious = shape.tail_suspicious;
    if shape.typo_suspicious || shape.mid_suspicious || tail_suspicious {
        // 含「反序双字母」时只使用最高置信度的归位修正；归位之外仍保留通用
        // 纠错作为兜底（单字候选来自这里），归位候选会无条件提升到精确层。
        let mistake_only = shape.mistake_input;
        let mut fixes: Vec<TypoCorrection> = Vec::new();
        if mistake_only {
            let cluster_fixes = crate::syllable::common_mistake_fixes(input);
            typo.cluster_fix_count = cluster_fixes.len();
            for f in cluster_fixes {
                fixes.push(TypoCorrection {
                    text: f,
                    is_near: true,
                });
            }
            fixes.extend(crate::syllable::typo_corrections(
                input,
                TYPO_MAX_CORRECTIONS,
            ));
        }
        if fixes.is_empty() {
            fixes = crate::syllable::typo_corrections(input, TYPO_MAX_CORRECTIONS);
        }
        if mistake_only {
            if let Some(first) = fixes.first() {
                let pats = crate::syllable::tokenize_pinyin(&first.text);
                if let Some(t0) = pats.first().and_then(|p| p.first()) {
                    typo.mistake_syllable = t0.text.clone();
                }
            }
        }
        // 只查询靠前的若干条：生成很便宜，每条一次的 Trie 查询才是主要成本。
        if fixes.len() > TYPO_MAX_QUERIES {
            fixes.truncate(TYPO_MAX_QUERIES);
        }

        for (fix_idx, fix) in fixes.iter().enumerate() {
            let is_cluster_fix = mistake_only && fix_idx < typo.cluster_fix_count;
            let patterns = crate::syllable::tokenize_pinyin(&fix.text);
            let Some(qp) = patterns.first() else { continue };
            if qp.is_empty() {
                continue;
            }
            let penalty = if fix.is_near {
                0
            } else {
                NON_ADJACENT_TYPO_PENALTY
            };

            let syll_refs: Vec<&str> = qp.iter().map(|t| t.text.as_str()).collect();
            let exacts: Vec<(String, Vec<String>, u32)> = dict
                .exact_words(&syll_refs)
                .iter()
                .take(8)
                .map(|e| (e.word.clone(), e.syllables.clone(), e.freq))
                .collect();
            for (word, syllables_vec, freq) in exacts {
                let syllables = syllables_vec.join("'");
                let key = pool_key(&word, &syllables);
                if typo.added.contains(&key) {
                    continue;
                }
                typo.added.insert(key.clone());
                typo.is_near.insert(key.clone(), fix.is_near);
                typo.lens.insert(key.clone(), fix.text.len());
                let mut boost = 0i32;
                if let Some(u) = user {
                    if !mistake_only && !shape.mistake_input {
                        let (b, _) = u.score(&word, prev);
                        boost = b as i32;
                    }
                }
                let mut score = freq as i32 + boost - penalty;
                if score < 1 {
                    score = 1;
                }
                let display_text = if traditional {
                    crate::s2t::to_traditional(&word)
                } else {
                    word.clone()
                };
                pool.add(
                    &word,
                    display_text,
                    syllables,
                    score,
                    boost > 0,
                    RANK_TYPO_FIX,
                    true,
                );
                if is_cluster_fix {
                    typo.mistake_keys.insert(key);
                }
                typo.word_found = true;
            }

            // 用户词库：自学习词与主词典同层参与纠错召回（Go 同口径：仅在非
            // mistake-only 且非反序输入时；用户词条本身即用户分，不再加 Score）。
            if let Some(user) = user {
                if !mistake_only && !shape.mistake_input {
                    let qp_pairs: Vec<(&str, bool)> =
                        qp.iter().map(|t| (t.text.as_str(), t.is_exact)).collect();
                    let (u_exacts, _) = user.query_tokens(&qp_pairs, 4);
                    for hit in u_exacts {
                        let syllables = hit.syllables.join("'");
                        let key = pool_key(&hit.word, &syllables);
                        if typo.added.contains(&key) {
                            continue;
                        }
                        typo.added.insert(key.clone());
                        typo.is_near.insert(key.clone(), fix.is_near);
                        typo.lens.insert(key.clone(), fix.text.len());
                        let mut score = hit.freq - penalty;
                        if score < 1 {
                            score = 1;
                        }
                        let display_text = if traditional {
                            crate::s2t::to_traditional(&hit.word)
                        } else {
                            hit.word.clone()
                        };
                        pool.add(
                            &hit.word.clone(),
                            display_text,
                            syllables,
                            score,
                            true,
                            RANK_TYPO_FIX,
                            true,
                        );
                        if is_cluster_fix {
                            typo.mistake_keys.insert(key);
                        }
                        typo.word_found = true;
                    }
                }
            }

            // 纠错后的「按已有词切分」DP：修正串在词库里没有单条整词时
            // （changshuru 没有「长输入」词条，但「长」+「输入」都存在），
            // 把修正后的音节切成已有词序列给出词组候选。只有没有任何整词级
            // 纠错命中时才提升到精确层（见 promote_typo_compositions）。
            // tail_suspicious（合法混拼的末位声母）不生成；替换/插入类修正
            // 也不生成（单词语义已能覆盖，组合只会带来噪声）。
            if let Some(phrase_bonus) = typo_fix_phrase_bonus(input, &fix.text) {
                if !tail_suspicious && typo.phrase_queries < TYPO_MAX_PHRASE_QUERIES {
                    typo.phrase_queries += 1;
                    for e in crate::typo::composed_phrase_entries(dict, qp, 6) {
                        let syllables = e.syllables.join("'");
                        let key = pool_key(&e.word, &syllables);
                        if typo.added.contains(&key) {
                            continue;
                        }
                        typo.added.insert(key.clone());
                        typo.phrase_added.insert(key.clone());
                        typo.is_near.insert(key.clone(), fix.is_near);
                        typo.lens.insert(key.clone(), fix.text.len());
                        let mut score = e.freq - penalty + phrase_bonus;
                        if score < 1 {
                            score = 1;
                        }
                        let display_text = if traditional {
                            crate::s2t::to_traditional(&e.word)
                        } else {
                            e.word.clone()
                        };
                        pool.add(
                            &e.word,
                            display_text,
                            syllables,
                            score,
                            false,
                            RANK_TYPO_FIX,
                            true,
                        );
                    }
                }
            }
        }
    }

    // 反序归位输入如果只有词组候选（chagnshuru → 常输入/长输入），补上修正后
    // 首音节的单字候选，保证「单字始终保留」、用户仍可逐字选择。
    if shape.mistake_input && !typo.mistake_syllable.is_empty() {
        let has_single = pool
            .snapshot()
            .iter()
            .any(|(c, _, _)| c.text.chars().count() == 1);
        if !has_single {
            let syll = typo.mistake_syllable.clone();
            let refs = [syll.as_str()];
            let singles: Vec<(String, Vec<String>, u32)> = dict
                .exact_words(&refs)
                .iter()
                .take(8)
                .map(|e| (e.word.clone(), e.syllables.clone(), e.freq))
                .collect();
            for (word, syllables_vec, freq) in singles {
                let syllables = syllables_vec.join("'");
                let key = pool_key(&word, &syllables);
                if typo.added.contains(&key) {
                    continue;
                }
                typo.added.insert(key.clone());
                typo.is_near.insert(key.clone(), true);
                typo.lens.insert(key.clone(), input.len());
                let display_text = if traditional {
                    crate::s2t::to_traditional(&word)
                } else {
                    word.clone()
                };
                pool.add(
                    &word,
                    display_text,
                    syllables,
                    freq as i32,
                    false,
                    RANK_TYPO_FIX,
                    true,
                );
            }
        }
    }
}

/// 判断某个修正串是否允许做「分词 DP」组合，并返回修正类型的置信度加权。
/// 只允许两类修正：相邻互换（+50）与删除/粘键折叠（长度变少，+30）。
fn typo_fix_phrase_bonus(src: &str, fixed: &str) -> Option<i32> {
    if fixed.is_empty() || src.is_empty() || fixed == src {
        return None;
    }
    if fixed.len() < src.len() {
        return Some(30); // 删除或连击折叠
    }
    if fixed.len() != src.len() {
        return None; // 插入：交给单词语义
    }
    let sb = src.as_bytes();
    let fb = fixed.as_bytes();
    let mut diff = 0;
    let mut first: i32 = -1;
    for i in 0..sb.len() {
        if sb[i] != fb[i] {
            diff += 1;
            if first < 0 {
                first = i as i32;
            }
        }
    }
    if diff != 2 || first < 0 || first as usize + 1 >= sb.len() {
        return None;
    }
    let f = first as usize;
    if sb[f] == fb[f + 1] && sb[f + 1] == fb[f] {
        return Some(50);
    }
    None
}

/// 错拼修正的阶段二（Go promoteTypoCompositions）：只有该输入没有任何
/// 「整词级」纠错命中时，才把纠错组合 DP 候选提升到精确层。
fn promote_typo_compositions(pool: &mut Pool, typo: &TypoState) {
    const TYPO_PHRASE_PROMOTE_MAX: usize = 8;
    if typo.phrase_added.is_empty() || typo.word_found {
        return;
    }
    let mut entries: Vec<(String, i32)> = Vec::new();
    for (cand, score, _) in pool.snapshot() {
        if !cand.typo_fix {
            continue;
        }
        let key = pool_key(&cand.text, &cand.syllables);
        if typo.phrase_added.contains(&key) {
            entries.push((key, score));
        }
    }
    entries.sort_by(|a, b| b.1.cmp(&a.1));
    entries.truncate(TYPO_PHRASE_PROMOTE_MAX);
    for (key, _) in entries {
        pool.set_rank(&key, RANK_EXACT);
    }
}

/// 已有整词级纠错命中时，丢弃错拼修正的**组合候选**。
///
/// 组合候选（`phrase_added`）的设计意图是「修正串在词库里没有单条整词、但能切成
/// 已有词序列」（`changshuru` → 长+输入）。一旦本次输入已经命中整词级纠错
///（`word_found`，如 `nihaox` → 修正为 `nihao` 命中「你好」），同一串的拼合产物
/// 就只是把已命中的词拆成两段的噪声：`nihaox` 首页出现「你号/你毫/你薅」、
/// `womend`（打「我们的」的必经中间态）首页 6 条里 4 条是「我门/握们/我闷/沃们」、
/// `yiyii` 甚至 6 条全是拼合噪声。固定口径扫描（291 个常用词 × 26 尾字母 = 7566
/// 形态）里 50.7% 的形态首页含这类噪声——「完整前缀 + 末音音节首字母」是每个多音
/// 节输入的必经帧，不是罕见手误。整词命中已给出用户真正想打的词，拼合候选只剩
/// 干扰，故整词命中时整批丢弃（不提升、不保留到回退层）。
fn drop_typo_compositions_when_word_found(pool: &mut Pool, typo: &TypoState) {
    if !typo.word_found || typo.phrase_added.is_empty() {
        return;
    }
    pool.retain(|cand, _, _| {
        let key = pool_key(&cand.text, &cand.syllables);
        !typo.phrase_added.contains(&key)
    });
}

/// 错拼修正的阶段三（Go promoteTypoCorrections）：排序前按词典事实把高置信度
/// 纠错提升到精确层，并取消「常规路径也能命中」的候选的 typo_fix 标记。
fn promote_typo_corrections(pool: &mut Pool, typo: &TypoState, shape: &ComposeShape, input: &str) {
    const TYPO_PROMOTE_RATIO: i32 = 2;
    if typo.added.is_empty() {
        return;
    }
    // 只有**纯错拼来源**的候选才保留整串消费标记：常规切分路径也命中的候选
    // 说明输入本身就能按音节消费到该词，渐进上屏必须保持既有语义。
    // rank 在常规路径命中去重时会降到 < rankTypoFix（add 只降不升），所以
    // 「rank 仍是 rankTypoFix」= 只有错拼路径找到过它。
    let mut clear_keys: Vec<String> = Vec::new();
    for (cand, _, rank) in pool.snapshot() {
        if !cand.typo_fix || rank == RANK_TYPO_FIX {
            continue;
        }
        let key = pool_key(&cand.text, &cand.syllables);
        if typo.phrase_added.contains(&key) {
            continue; // 组合 DP 候选的整串消费语义必须保留
        }
        clear_keys.push(key);
    }
    for key in clear_keys {
        if let Some(slot) = pool.map.get_mut(&key) {
            slot.0.typo_fix = false;
        }
    }

    let snapshot = pool.snapshot();
    let mut best_typo_freq: i32 = 0;
    for (cand, score, _) in &snapshot {
        let key = pool_key(&cand.text, &cand.syllables);
        if typo.phrase_added.contains(&key) {
            continue;
        }
        if shape.mistake_input && !typo.mistake_keys.contains(&key) {
            continue;
        }
        if typo.added.contains(&key)
            && *typo.is_near.get(&key).unwrap_or(&false)
            && (!shape.tail_suspicious || *typo.lens.get(&key).unwrap_or(&0) >= input.len())
            && *score > best_typo_freq
        {
            best_typo_freq = *score;
        }
    }

    // 「常规候选有多强」只能采信单词级精确匹配，必须排除一切拼接产物：
    // 只认 rankExact / rankInitialExact 两级（直接命中词典词条）。
    let mut best_regular_freq: i32 = 0;
    let mut has_regular_word = false;
    for (cand, score, rank) in &snapshot {
        let key = pool_key(&cand.text, &cand.syllables);
        if typo.added.contains(&key) {
            continue;
        }
        if *rank != RANK_EXACT && *rank != RANK_INITIAL_EXACT {
            continue;
        }
        // transpose 触发的手误场景：只有「音节数 == 输入段数」的全消耗精确词
        // 才算常规候选（中间夹声母的组合产物与单字碎片都是强行切分的产物）。
        if (shape.typo_suspicious || shape.mid_suspicious || shape.tail_suspicious)
            && shape.input_segs > 0
            && (cand.syllables.matches('\'').count() as i32 + 1) != shape.input_segs
        {
            continue;
        }
        has_regular_word = true;
        if *score > best_regular_freq {
            best_regular_freq = *score;
        }
    }

    if best_typo_freq > 0
        && (shape.mistake_input
            || !has_regular_word
            || best_typo_freq >= TYPO_PROMOTE_RATIO * best_regular_freq)
    {
        let mut promote_keys: Vec<String> = Vec::new();
        for (cand, _, _) in &snapshot {
            let key = pool_key(&cand.text, &cand.syllables);
            if typo.phrase_added.contains(&key) {
                continue; // 组合候选只由阶段二提升
            }
            if shape.mistake_input && !typo.mistake_keys.contains(&key) {
                continue; // 反序归位输入只提升归位修正
            }
            if typo.added.contains(&key)
                && *typo.is_near.get(&key).unwrap_or(&false)
                && (!shape.tail_suspicious || *typo.lens.get(&key).unwrap_or(&0) >= input.len())
            {
                promote_keys.push(key);
            }
        }
        for key in promote_keys {
            pool.set_rank(&key, RANK_EXACT);
        }
    }
}

/// 错拼修正候选固定展示位（Go placeTypoFixedPosition，第 3 位起）。
/// 只搬 rank < rankBackoff 的高置信度纠错——低置信度纠错仍留在列表末尾。
fn place_typo_fixed_position(items: Vec<(Candidate, i32, i32)>) -> Vec<(Candidate, i32, i32)> {
    const TYPO_CANDIDATE_FIXED_POSITION: usize = 3;
    if items.is_empty() {
        return items;
    }
    let mut normal: Vec<(Candidate, i32, i32)> = Vec::new();
    let mut fixed: Vec<(Candidate, i32, i32)> = Vec::new();
    for it in items {
        if it.0.typo_fix && it.2 < RANK_BACKOFF {
            fixed.push(it);
        } else {
            normal.push(it);
        }
    }
    if fixed.is_empty() {
        return normal;
    }
    let insert_at = (TYPO_CANDIDATE_FIXED_POSITION - 1).min(normal.len());
    let mut out = Vec::with_capacity(normal.len() + fixed.len());
    out.extend(normal.drain(..insert_at));
    out.extend(fixed);
    out.extend(normal);
    out
}

/// 正常输入的「已有词组合」候选（Go addCompositionCandidates）：
/// 正确拼写的多音节输入如果整串在词库里没有单条词条、但能切成已有词的序列
/// （changshuru → 长+输入、tuzhong → 图+中），也作为候选给出。约束：只允许
/// 词典中真实存在的词段（不造词）；放在 RANK_COMPOSITION 层（真实词条与逐级
/// 回退之后），不会挤掉正常首选；最多 4 条。
fn add_composition_candidates(
    dict: &Dictionary,
    user: Option<&UserDict>,
    shape: &ComposeShape,
    patterns: &[Vec<Token>],
    prev: Option<&str>,
    traditional: bool,
    pool: &mut Pool,
) {
    if shape.typo_suspicious || shape.mid_suspicious || shape.tail_suspicious || patterns.is_empty()
    {
        return;
    }
    let clean = &patterns[0];
    if clean.len() < 2 || clean.iter().any(|t| !t.is_exact) {
        return;
    }
    for e in crate::typo::composed_phrase_entries(dict, clean, 4) {
        let (boost, from_user) = if let Some(u) = user {
            let (b, is_user) = u.score(&e.word, prev);
            (b as i32, is_user)
        } else {
            (0, false)
        };
        let display_text = if traditional {
            to_traditional(&e.word)
        } else {
            e.word.clone()
        };
        pool.add(
            &e.word,
            display_text,
            e.syllables.join("'"),
            e.freq + boost,
            from_user,
            RANK_COMPOSITION,
            false,
        );
    }
}

/// 单段输入的单字过滤（Go filterSingleSegment）：单音节全拼（diao/yi/jie）与
/// 单段声母（d/y）不再出任何词语——既不是前缀扩展词，也不是连写恰好等于
/// 输入的全匹配词（xian 的「西安」、jie 的「饥饿」）。词语只能通过显式分段
/// （xi'an/ji'e）或完整多音节输入匹配。
fn filter_single_segment(pool: &mut Pool, patterns: &[Vec<Token>]) {
    let Some(first_pat) = patterns.first() else {
        return;
    };
    if first_pat.len() != 1 {
        return;
    }
    let tok = &first_pat[0];
    // 单字母声母（d/y）才是「单段输入」；sh/ch/zh 是多字母简拼，继续按词候选
    // 处理，不做单字过滤。
    if !tok.is_exact && tok.text.chars().count() != 1 {
        return;
    }
    // 单音节全拼只保留「整个输入就是该音节」的单字；回退/另一切分产生的其他
    // 音节单字（diao 的 di 声母字「第」）也不应混进来。模糊音/别名输入的候选
    // 音节按 canonical + fuzzyVariants 归一后比较。
    let canon = crate::syllable::canonical_syllable(&tok.text).to_string();
    let mut allowed: std::collections::HashSet<String> = std::collections::HashSet::new();
    allowed.insert(canon.clone());
    for v in crate::fuzzy::fuzzy_syllable_variants(&canon) {
        allowed.insert(v);
    }
    let tok_text = tok.text.clone();
    let tok_is_exact = tok.is_exact;
    pool.retain(|cand, _, _| {
        let text = &cand.text;
        if text.chars().count() > 1 {
            return false; // 单段输入的拼音匹配层只允许单字
        }
        let syll = cand.syllables.replace('\'', "");
        if !tok_is_exact {
            // 单字母声母：只保留该声母能匹配的单字（matchSyllable 语义：
            // 前缀匹配 + z/c/s 兼容 zh/ch/sh）。
            let canon_syll = crate::syllable::canonical_syllable(&syll).to_string();
            return canon_syll.starts_with(&tok_text)
                || (tok_text == "z" && canon_syll.starts_with("zh"))
                || (tok_text == "c" && canon_syll.starts_with("ch"))
                || (tok_text == "s" && canon_syll.starts_with("sh"));
        }
        allowed.contains(&syll)
    });
}

/// 词典多字词组封顶（MAX_DICT_PHRASES = 2 页）：按排好的顺序数过去，超出上限的
/// 非用户多字候选直接丢弃；用户词组与单字不计数、不截断。只数不剔位：逐级回退的
/// 真实词组排在用户学过的单字之后是既有语义（Linux E2E：`n'ce'shi'ci` 下学过的
/// 「你」在前、导入词「测试词」紧随其后必须在首页），不能按「单字之后无词组」剔掉。
fn cap_dict_phrases(sorted: Vec<(Candidate, i32, i32)>) -> Vec<(Candidate, i32, i32)> {
    let mut kept_phrases = 0usize;
    sorted
        .into_iter()
        .filter(|(c, _, _)| {
            if c.from_user || c.text.chars().count() < 2 {
                return true;
            }
            if kept_phrases >= MAX_DICT_PHRASES {
                return false;
            }
            kept_phrases += 1;
            true
        })
        .collect()
}

// 组合候选层（Go rankComposition = rankBackoff+64）：把多音节输入切成「词库里
// 真实存在的词」序列（changshuru → 长+输入）。放在所有真实词条与逐级回退碎片
// 之后，只作为列表末尾的额外选项——组合没有语言模型，不得改变原有候选先后。
const RANK_COMPOSITION: i32 = RANK_BACKOFF + 64;
// 单字收尾（Go rankSingleBackfill = rankComposition+16）：纯单字收尾、列表最末；
// 单字块之后不得再出现词组。
const RANK_SINGLE_BACKFILL: i32 = RANK_COMPOSITION + 16;

/// 续接候选每多补一个字符的得分折价。Go 数据扫描定型值：30 是唯一同时保住
/// 「全拼完整输入首选率」与「末音节残缺首选率」的点（中国 85−30=55 > 中古 36）。
const CONTINUATION_PENALTY: i32 = 30;

/// 单音节全拼/单段声母/单字兜底的 exact 上限：同音字全量召回（yi 141 等都放得下）。
const SINGLE_SYLLABLE_EXACT_LIMIT: usize = 300;
/// 多段简拼/混拼的词候选上限：放大到 300 会让 zg/dz 候选膨胀到 ~80 页，稀释单字页位。
const INITIALS_EXACT_LIMIT: usize = 120;
/// 用户学习词组（完整消费输入的那部分）的查询上限：**不设上限**——用户明确要求
/// 自己输入过的词组全部展示、数目不限。实际条数受用户词典规模约束（几百条量级），
/// 续接/回退层的用户词仍各用自己的小上限（它们不消费完整输入，属预测而非匹配）。
const USER_PHRASES_UNLIMITED: usize = usize::MAX;
/// 预制（词典）多字词组在候选列表里的封顶 = 2 页。超出部分直接截掉，其后紧接首段
/// 全量单字：用户要的词不在前 2 页时，第 3 页起就能逐字/部分选取拼词，不必翻过
/// 几十页词典词组（实测 shiji 46 条 8 页、zg 140 条 24 页才到单字）；拼出的词经
/// 学习成为用户词组，下次置顶且不计入本上限。单字与用户词组不截断。
const MAX_DICT_PHRASES: usize = 2 * PAGE_SIZE;
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, Default)]
pub struct Candidate {
    pub text: String,
    pub syllables: String,
    #[serde(default)]
    pub score: i32,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub from_user: bool,
    /// 错拼修正标记（Go cand.typoFix）：该候选来自「错拼修正」。内部字段，
    /// 不进 IPC JSON——三端壳按序号选择，不需要也不应感知该字段。服务
    /// SelectPrefix 的整串消费语义与「纠错固定第 3 位」排序契约。
    #[serde(skip, default)]
    pub typo_fix: bool,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, Default)]
pub struct Composition {
    pub input: String,
    pub preedit: String,
    pub candidates: Vec<Candidate>,
    pub page: usize,
    pub page_count: usize,
    #[serde(skip_serializing_if = "String::is_empty", default)]
    pub committed: String,
    #[serde(skip_serializing_if = "String::is_empty", default)]
    pub shuangpin: String,
}

pub struct Session {
    dict: Arc<Dictionary>,
    user_dict: Option<Arc<UserDict>>,
    pub fuzzy: bool,
    pub traditional: bool,
    pub shuangpin: Option<ShuangpinScheme>,
    shuangpin_decoder: Option<ShuangpinDecoder>,
    /// 双拼首键（等待第二键成对解码；None = 无）。
    dp_pending: Option<char>,
    /// 双拼退格栈：每次「按键事件」实际追加到输入串的字符数（退格回滚用）。
    dp_lens: Vec<usize>,
    punct_state: PunctState,

    input_raw: String,
    tokens: Vec<Token>,
    all_candidates: Vec<(Candidate, u32)>, // (Candidate, Score)
    page: usize,
    page_size: usize,

    last_committed: Option<String>,
    /// 刚上屏词的音节（Go s.prevSylls）：联想剥前缀的切分基准，壳传的不可信。
    prev_sylls: Vec<String>,
    /// 最近几次上屏（词, 音节），词组进化观察「相邻共现」用（Go s.history）。
    history: Vec<(String, Vec<String>)>,
    /// 本次组词中已「渐进上屏」的各段（词, 音节）。整串耗尽时拼成整句习惯写入
    /// 用户词库（见 `learn_progressive_sequence`）；从空组词重新开始时作废。
    seq_segs: Vec<(String, Vec<String>)>,
}

/// 从输入串开头按音节消费候选（Go consumeInputPrefix 的 Rust 版）。
///
/// 输入保留用户原始敲法：可能是省写/别名（xve→xue、niou→niu）、模糊形态
/// （zheng 命中 zen）、声母（nh）或半截拼音（zhongg），候选音节是规范拼写。
/// 因此不能按「token 字母数」切字节串——`ni'hao` 会从 hao 中间截断；必须
/// 逐音节块匹配，并在块之间跳过显式分隔符。返回 (剩余输入, 已匹配音节数)；
/// matched==0 表示一个音节都对不上，调用方回退整串上屏语义。
fn consume_input_prefix(input: &str, sylls: &[String], fuzzy: bool) -> (String, usize) {
    let bytes = input.as_bytes();
    let mut i = 0usize;
    let mut matched = 0usize;
    for syl in sylls {
        if syl.is_empty() {
            continue;
        }
        while i < bytes.len() && bytes[i] == b'\'' {
            i += 1;
        }
        if i >= bytes.len() {
            break;
        }
        // 当前音节块（到下一个 ' 或输入结尾）：匹配不跨块。
        let end = bytes[i..]
            .iter()
            .position(|&b| b == b'\'')
            .map(|p| i + p)
            .unwrap_or(bytes.len());
        let n = match_syllable_chunk(&input[i..end], syl, fuzzy);
        if n == 0 {
            break;
        }
        i += n;
        matched += 1;
    }
    while i < bytes.len() && bytes[i] == b'\'' {
        i += 1;
    }
    (input[i..].to_string(), matched)
}

/// 输入块开头与候选音节匹配的字母数（Go matchSyllableChunk）：
/// 1) 块开头是完整音节/省写别名/模糊等价 → 最长匹配；
/// 2) 块开头是完整音节且是候选音节前缀（gu→guo）→ 匹配该完整音节；
/// 3) 块开头没有完整音节：整块是候选前缀（zho→zhong）或块首声母是候选
///    前缀（k→kan、zh→zhong）→ 匹配整块/声母；否则 0。
///
/// 完整音节不匹配时**不再退回声母**：用户敲的是另一个音节（zheng ≠ zen），
/// 不能把 zheng 拆成 z + heng 混进后续音节；zzz 对 jin 必须整体不匹配。
fn match_syllable_chunk(chunk: &str, syl: &str, fuzzy: bool) -> usize {
    let max_l = chunk.len().min(crate::syllable::MAX_SYLLABLE_LEN);
    // 1) 完整音节 / 省写别名 / 模糊形态：块开头最长子串优先。
    for l in (1..=max_l).rev() {
        let canon = crate::syllable::canonical_syllable(&chunk[..l]);
        if canon.as_ref() == syl {
            return l;
        }
        if fuzzy
            && crate::fuzzy::fuzzy_syllable_variants(syl)
                .iter()
                .any(|v| v == canon.as_ref())
        {
            return l;
        }
    }
    // 2) 块开头是完整音节、且是候选音节的前缀（gu → guo，用户只打了一半）。
    let mut longest = 0usize;
    for l in (1..=max_l).rev() {
        if crate::syllable::is_syllable(&chunk[..l]) {
            longest = l;
            break;
        }
    }
    if longest > 0 {
        return if syl.starts_with(&chunk[..longest]) {
            longest
        } else {
            0
        };
    }
    // 3) 块开头没有完整音节：整块是候选音节前缀，或块首合法声母是前缀。
    if syl.starts_with(chunk) {
        return chunk.len();
    }
    let init = ["zh", "ch", "sh"]
        .iter()
        .find(|c| chunk.starts_with(**c))
        .copied()
        .unwrap_or(&chunk[..chunk.len().min(1)]);
    if !init.is_empty() && syl.starts_with(init) {
        return init.len();
    }
    0
}

impl Session {
    pub fn new(dict: Arc<Dictionary>, user_dict: Option<Arc<UserDict>>) -> Self {
        Self {
            dict,
            user_dict,
            fuzzy: false,
            traditional: false,
            shuangpin: None,
            shuangpin_decoder: None,
            dp_pending: None,
            dp_lens: Vec::new(),
            punct_state: PunctState::default(),
            input_raw: String::new(),
            tokens: Vec::new(),
            all_candidates: Vec::new(),
            page: 0,
            page_size: PAGE_SIZE,
            last_committed: None,
            prev_sylls: Vec::new(),
            history: Vec::new(),
            seq_segs: Vec::new(),
        }
    }

    pub fn set_fuzzy(&mut self, fuzzy: bool) {
        self.fuzzy = fuzzy;
    }

    pub fn set_traditional(&mut self, traditional: bool) {
        self.traditional = traditional;
    }

    pub fn set_shuangpin(&mut self, scheme: Option<ShuangpinScheme>) {
        if scheme != self.shuangpin {
            self.dp_pending = None;
            self.dp_lens.clear();
        }
        self.shuangpin = scheme;
        self.shuangpin_decoder = scheme.map(ShuangpinDecoder::new);
    }

    pub fn page_size(&self) -> usize {
        self.page_size
    }

    /// 核心组词函数
    pub fn compose(&mut self) -> Composition {
        if self.input_raw.is_empty() {
            self.tokens.clear();
            self.all_candidates.clear();
            self.page = 0;
            return Composition {
                shuangpin: self
                    .shuangpin
                    .map(|s| s.name().to_string())
                    .unwrap_or_default(),
                ..Default::default()
            };
        }

        // 1. 匹配串：全拼保留用户大小写，双拼的输入串在 input() 里已解码成
        //    音节（与 Go 一致：input 存解码结果，不是键码），这里统一取小写视图。
        let pinyin_query = self.match_input();

        // 2. 音节切分
        let patterns = tokenize_pinyin(&pinyin_query);
        let best_pattern = patterns.first().cloned().unwrap_or_default();
        self.tokens = best_pattern.clone();

        // 4. 召回与打分（分层召回，对齐 Go compose.go 的编排顺序）：
        // 层级是第一排序维度，词频打分只在同层内决定先后——这是与 Go 一致的
        // 关键语义（旧的「词频 × 5000/1000 加成」方案用打分模拟分层，跨层会被
        // 高词频挤压，正是 tail 形态命中率 32% vs 89% 的根源）。
        //
        // 调用顺序即语义，不能交换（见 Go compose.go 注释）：
        //   续接（D）→ 错拼（E）→ 组合提升（F）→ 常规 token（H）→ 单字收尾（I）
        //   → 纠错提升（K）→ 单段过滤（L）→ 排序（M）→ 纠错固定位（N）。
        let match_input = self.match_input();
        let shape = analyze_shape(&match_input, &patterns);
        let mut typo = TypoState::default();
        let mut pool = Pool::new();

        // 两条续接路径共用层级：存在干净解析时落在 RANK_CONTINUATION——严格排在
        // 「完整消费输入」的精确匹配之后（用户要求：输入 shiji 先给 shi'ji 的词，
        // 再给 shi'jie/shi'jian 这类还要补字母的续接；此前与精确层同层按「词频 −
        // 补全折价」竞争，高频续接 世界 会插到 事迹 前面）。无法干净切分时常规
        // 候选全是碎片假词，续接必须领跑。
        let continuation_rank = if shape.all_exact_shape {
            RANK_CONTINUATION
        } else {
            RANK_CONTINUATION_LEAD
        };
        let traditional = self.traditional;
        let prev = self.last_committed.as_deref();

        // 4.1 末段残缺续接（Go addContinuations）：
        // 路径一：末音节本身合法但还能延长（gu→guo、jin→jing、xu→xue），把
        // 末音节当「待补全」续接，才能让 zhonggu 出「中国」、daxu 出「大学」；
        // 路径二：末段整体连音节都不是（quanmia 的 mia），把「前面已完整 + 末段
        // 整体待补全」再查一次，否则常规切分只会命中「全米啊」这类碎片假词。
        for pat in &patterns {
            if pat.len() < 2 {
                continue;
            }
            let last = pat.last().unwrap();
            if !last.is_exact || crate::syllable::syllable_continuations(&last.text).is_empty() {
                continue;
            }
            let head: Vec<&str> = pat[..pat.len() - 1]
                .iter()
                .map(|t| t.text.as_str())
                .collect();
            let mut hits: Vec<(String, Vec<String>, u32, String)> = self
                .dict
                .query_continuation(&head, &last.text, 24)
                .into_iter()
                .map(|(idx, remain)| {
                    let e = &self.dict.entries[idx];
                    (e.word.clone(), e.syllables.clone(), e.freq, remain)
                })
                .collect();
            if let Some(ref u) = self.user_dict {
                let head_tokens: Vec<(&str, bool)> = pat[..pat.len() - 1]
                    .iter()
                    .map(|t| (t.text.as_str(), t.is_exact))
                    .collect();
                for (hit, remain) in u.query_continuation(&head_tokens, &last.text, 8) {
                    hits.push((hit.word, hit.syllables, hit.freq.max(0) as u32, remain));
                }
            }
            for (word, syllables_vec, freq, remain) in hits {
                if remain.is_empty() {
                    continue; // 末音节本身已是完整音节：等同精确匹配，由常规路径覆盖
                }
                let (boost, from_user) = if let Some(ref u) = self.user_dict {
                    let (b, f) = u.score(&word, prev);
                    (b as i32, f)
                } else {
                    (0, false)
                };
                // 补全越长越不确定（gu→guo 远强于 gu→guang），按字符数折价
                let score = freq as i32 + boost - CONTINUATION_PENALTY * remain.len() as i32;
                let display_text = if traditional {
                    to_traditional(&word)
                } else {
                    word.clone()
                };
                pool.add(
                    &word,
                    display_text,
                    syllables_vec.join("'"),
                    score,
                    from_user,
                    continuation_rank,
                    false,
                );
            }
        }
        for (head_syls, frag) in crate::syllable::partial_tail_splits(&match_input) {
            let head: Vec<&str> = head_syls.iter().map(|s| s.as_str()).collect();
            let mut hits: Vec<(String, Vec<String>, u32, String)> = self
                .dict
                .query_continuation(&head, &frag, 24)
                .into_iter()
                .map(|(idx, remain)| {
                    let e = &self.dict.entries[idx];
                    (e.word.clone(), e.syllables.clone(), e.freq, remain)
                })
                .collect();
            if let Some(ref u) = self.user_dict {
                let head_tokens: Vec<(&str, bool)> = head.iter().map(|s| (*s, true)).collect();
                for (hit, remain) in u.query_continuation(&head_tokens, &frag, 8) {
                    hits.push((hit.word, hit.syllables, hit.freq.max(0) as u32, remain));
                }
            }
            for (word, syllables_vec, freq, remain) in hits {
                let (boost, from_user) = if let Some(ref u) = self.user_dict {
                    let (b, f) = u.score(&word, prev);
                    (b as i32, f)
                } else {
                    (0, false)
                };
                let score = freq as i32 + boost - CONTINUATION_PENALTY * remain.len() as i32;
                let display_text = if traditional {
                    to_traditional(&word)
                } else {
                    word.clone()
                };
                pool.add(
                    &word,
                    display_text,
                    syllables_vec.join("'"),
                    score,
                    from_user,
                    continuation_rank,
                    false,
                );
            }
        }

        // 4.1.5 连写匹配（Go addJoinedMatches）：拼音连写恰好等于输入的词条，
        // 按精确层推出——不依赖切分枚举，兜住枚举上限导致的漏匹配。错拼形态
        // 不做（产物全是假词），输入含显式分隔符时也不做（用户已划分音节，
        // 去掉分隔符按连写匹配会把 b'a 误当成 ba）。
        if !shape.typo_suspicious && !self.input_raw.contains('\'') {
            for e in self.dict.query_joined_pinyin(&match_input, 20) {
                let (boost, from_user) = if let Some(ref u) = self.user_dict {
                    let (b, f) = u.score(&e.word, prev);
                    (b as i32, f)
                } else {
                    (0, false)
                };
                let display_text = if traditional {
                    to_traditional(&e.word)
                } else {
                    e.word.clone()
                };
                pool.add(
                    &e.word.clone(),
                    display_text,
                    e.syllables.join("'"),
                    e.freq as i32 + boost,
                    from_user,
                    RANK_EXACT,
                    false,
                );
            }
            if let Some(ref u) = self.user_dict {
                for h in u.query_joined_pinyin(&match_input, USER_PHRASES_UNLIMITED) {
                    let display_text = if traditional {
                        to_traditional(&h.word)
                    } else {
                        h.word.clone()
                    };
                    pool.add(
                        &h.word,
                        display_text,
                        h.syllables.join("'"),
                        h.freq,
                        true,
                        RANK_EXACT,
                        false,
                    );
                }
            }
        }

        // 4.2 错拼修正（Go addTypoCorrections）+ 组合候选提升（F）
        add_typo_corrections(
            &self.dict,
            self.user_dict.as_deref(),
            &match_input,
            &shape,
            prev,
            traditional,
            &mut pool,
            &mut typo,
        );
        promote_typo_compositions(&mut pool, &typo);
        // 整词级纠错已命中时，同批组合候选是重复噪声（见函数注释）：整批丢弃。
        drop_typo_compositions_when_word_found(&mut pool, &typo);

        // 4.3 常规 token 匹配（Go addTokenMatches）：纯音节 pattern 优先，
        // 全部空手而归才回退含碎片的 pattern（简拼兜底）。上限口径（取值依据
        // 见 Go compose_query.go addTokenMatches 注释的 2026-10-28 实测）：
        //   - 单音节全拼/单段声母 exact=300：同音字全量（yi 141 / ji 130）；
        //   - 多段简拼/混拼 exact=120：放大到 300 会让候选膨胀 ~80 页；
        //   - 常规多音节 20/20（多条歧义切分 × 条数是乘法放大）；
        //   - prefix 一律 20：低频衍生词组洪水会稀释单字页位。
        let pattern_has_fragment = |pat: &[Token]| pat.iter().any(|t| !t.is_exact);
        let has_pure_pattern = patterns.iter().any(|p| !pattern_has_fragment(p));

        for pass in 0..2 {
            // 有纯音节 pattern：第一遍跑纯音节、全部空手而归第二遍放开碎片；
            // 全是碎片 pattern（无任何纯音节解，如 nh/zzz）：第一遍直接跑碎片。
            let want_fragment = if has_pure_pattern {
                pass == 1
            } else {
                pass == 0
            };
            if pass == 1 && (!has_pure_pattern || !pool.is_empty()) {
                break;
            }
            for pat in patterns
                .iter()
                .filter(|p| pattern_has_fragment(p) == want_fragment)
            {
                for qp in expand_token_variants(pat, self.fuzzy) {
                    let has_fragment_in_pat = pattern_has_fragment(&qp);
                    let tokens: Vec<&str> = qp.iter().map(|t| t.text.as_str()).collect();

                    // 上限分配与 Go 同序（顺序有意义）：单段（含单段声母）放开到
                    // 单字上限 300——逐字挑选时才能选到 top 之外的同声母单字；
                    // 多段简拼/混拼 120；常规多音节 20（歧义切分是乘法放大）。
                    let exact_limit = if tokens.len() == 1 {
                        SINGLE_SYLLABLE_EXACT_LIMIT
                    } else if has_fragment_in_pat {
                        INITIALS_EXACT_LIMIT
                    } else {
                        20
                    };
                    let single_seg_single_char =
                        tokens.len() == 1 && (qp[0].is_exact || qp[0].text.chars().count() == 1);

                    // Go drop 段：回退上限 6 层；回退到单个完整音节且原输入 ≥4 段
                    // 时放开同音字全量（对齐单音节口径，SelectPrefix 逐字挑选才选
                    // 得到 top 之外的字），其余回退层 10。
                    let backoff_floor = tokens.len().saturating_sub(6).max(1);
                    // Go drop 段的提升特例：整串没有任何精确命中、去掉 1 段后剩余
                    // 全部是完整音节、且被去掉的段里有声母碎片（nihaox → [ni,hao] +
                    // 碎片 x）时，把「你好」这类回退命中提升到精确层置顶推出。
                    let mut full_exact_len: usize = 0;
                    // 错拼形态（Go addTokenMatches）：主查询换 QueryTokensExact（不展开
                    // prefix 子树——碎片前缀的 BFS 展开量大且产物全是假词），且不做逐级
                    // 回退（回退层产物同样会被纠错候选压掉，只徒增页数）。
                    let typo_shape = shape.typo_suspicious;
                    for l in (backoff_floor..=tokens.len()).rev() {
                        if typo_shape && l != tokens.len() {
                            continue;
                        }
                        let sub = &tokens[..l];
                        let is_full = l == tokens.len();
                        let (exacts, prefixes) = if typo_shape {
                            (self.dict.query_exact(sub, 20), Vec::new())
                        } else if is_full {
                            let (e, p) = self.dict.query_exact_and_prefix(sub, exact_limit, 20);
                            (e, p)
                        } else {
                            let first_exact = qp.first().map(|t| t.is_exact).unwrap_or(false);
                            let backoff_limit =
                                if sub.len() == 1 && first_exact && tokens.len() >= 4 {
                                    SINGLE_SYLLABLE_EXACT_LIMIT
                                } else {
                                    10
                                };
                            (self.dict.query_exact(sub, backoff_limit), Vec::new())
                        };
                        if is_full {
                            full_exact_len = exacts.len();
                        }

                        let rank_exact = if has_fragment_in_pat {
                            RANK_INITIAL_EXACT
                        } else {
                            RANK_EXACT
                        };
                        let rank_prefix = if has_fragment_in_pat {
                            RANK_INITIAL_PREFIX
                        } else {
                            RANK_PREFIX
                        };
                        // Go rankBackoff + drop：回退越深（l 越小）排越后
                        let backoff_rank = RANK_BACKOFF + (tokens.len() - l) as i32;
                        // 提升特例（见上）：drop=1、剩余全整音节、整串无精确命中、
                        // 被去掉的末段含碎片
                        let promote_to_exact = !is_full
                            && full_exact_len == 0
                            && (tokens.len() - l) == 1
                            && qp[..l].iter().all(|t| t.is_exact)
                            && qp[l..].iter().any(|t| !t.is_exact);
                        let effective_rank = if promote_to_exact {
                            RANK_EXACT
                        } else {
                            backoff_rank
                        };

                        for e in exacts {
                            let (boost, from_user) = if let Some(ref u) = self.user_dict {
                                let (b, f) = u.score(&e.word, prev);
                                (b as i32, f)
                            } else {
                                (0, false)
                            };
                            let rank = if is_full { rank_exact } else { effective_rank };
                            let display_text = if traditional {
                                to_traditional(&e.word)
                            } else {
                                e.word.clone()
                            };
                            pool.add(
                                &e.word.clone(),
                                display_text,
                                e.syllables.join("'"),
                                e.freq as i32 + boost,
                                from_user,
                                rank,
                                false,
                            );
                        }
                        // 单段输入只出单字：前缀扩展词不进池（exact 池天然只有单字）
                        for e in prefixes {
                            if single_seg_single_char {
                                continue;
                            }
                            let (boost, from_user) = if let Some(ref u) = self.user_dict {
                                let (b, f) = u.score(&e.word, prev);
                                (b as i32, f)
                            } else {
                                (0, false)
                            };
                            let display_text = if traditional {
                                to_traditional(&e.word)
                            } else {
                                e.word.clone()
                            };
                            pool.add(
                                &e.word.clone(),
                                display_text,
                                e.syllables.join("'"),
                                e.freq as i32 + boost,
                                from_user,
                                rank_prefix,
                                false,
                            );
                        }
                        // 用户词库：自学习词组/调频词与主词典同层参与召回（Go 同
                        // 口径）：完整 token 查询层查 queryTokens（exact+prefix），
                        // 回退层只补 QueryTokensExact(shorter, 5)。前缀层的整句式
                        // 学习记录必须跳过（候选硬约束：部分输入不得出现整词候选）。
                        if let Some(ref u) = self.user_dict {
                            if is_full {
                                let qp_pairs: Vec<(&str, bool)> =
                                    qp.iter().map(|t| (t.text.as_str(), t.is_exact)).collect();
                                // 用户词组不设上限（用户明确要求：自己输入过的词组
                                // 数目不限、全部展示；它们经排序置顶且不受词典封顶约束）。
                                let (u_exacts, u_prefixes) =
                                    u.query_tokens(&qp_pairs, USER_PHRASES_UNLIMITED);
                                for hit in u_exacts {
                                    let display_text = if traditional {
                                        to_traditional(&hit.word)
                                    } else {
                                        hit.word.clone()
                                    };
                                    pool.add(
                                        &hit.word.clone(),
                                        display_text,
                                        hit.syllables.join("'"),
                                        hit.freq,
                                        true,
                                        rank_exact,
                                        false,
                                    );
                                }
                                for hit in u_prefixes {
                                    if self.is_sentence_entry(&hit.word, &hit.syllables, &hit.segs)
                                    {
                                        continue;
                                    }
                                    let display_text = if traditional {
                                        to_traditional(&hit.word)
                                    } else {
                                        hit.word.clone()
                                    };
                                    pool.add(
                                        &hit.word.clone(),
                                        display_text,
                                        hit.syllables.join("'"),
                                        hit.freq,
                                        true,
                                        rank_prefix,
                                        false,
                                    );
                                }
                            } else {
                                let sub_tokens: Vec<(&str, bool)> = qp[..l]
                                    .iter()
                                    .map(|t| (t.text.as_str(), t.is_exact))
                                    .collect();
                                for hit in u.query_tokens_exact(&sub_tokens, 5) {
                                    let display_text = if traditional {
                                        to_traditional(&hit.word)
                                    } else {
                                        hit.word.clone()
                                    };
                                    pool.add(
                                        &hit.word.clone(),
                                        display_text,
                                        hit.syllables.join("'"),
                                        hit.freq,
                                        true,
                                        effective_rank,
                                        false,
                                    );
                                }
                            }
                        }
                    }
                }
            }
        }

        // 4.5 首段全量单字收尾（Go addSingleCharBackfill）：任何多段输入都在
        // 列表最末追加首段全量单字（rankSingleBackfill），让 SelectPrefix 能
        // 选到 top 之外的字；单段输入本身已是全量单字，不重复追加。
        // **错拼输入不追加单字兜底**（项目硬约束）：纠错候选固定第 3 位，其余
        // 位置只保留纠错与真实词条，否则列表会被几百个同音单字淹没。
        if !shape.typo_suspicious {
            if let Some(first_pat) = patterns.first() {
                if first_pat.len() >= 2 {
                    let first = [first_pat[0].text.as_str()];
                    let singles: Vec<(String, Vec<String>, u32)> = self
                        .dict
                        .query_exact(&first, SINGLE_SYLLABLE_EXACT_LIMIT)
                        .iter()
                        .filter(|e| e.word.chars().count() == 1)
                        .map(|e| (e.word.clone(), e.syllables.clone(), e.freq))
                        .collect();
                    for (word, syllables_vec, freq) in singles {
                        let display_text = if traditional {
                            to_traditional(&word)
                        } else {
                            word.clone()
                        };
                        pool.add(
                            &word,
                            display_text,
                            syllables_vec.join("'"),
                            freq as i32,
                            false,
                            RANK_SINGLE_BACKFILL,
                            false,
                        );
                    }
                    // 用户词库的首音节单字同样补齐（Go 同口径）
                    if let Some(ref u) = self.user_dict {
                        let first_pairs = [(first_pat[0].text.as_str(), first_pat[0].is_exact)];
                        for hit in u.query_tokens_exact(&first_pairs, SINGLE_SYLLABLE_EXACT_LIMIT) {
                            if hit.word.chars().count() != 1 {
                                continue;
                            }
                            let display_text = if traditional {
                                to_traditional(&hit.word)
                            } else {
                                hit.word.clone()
                            };
                            pool.add(
                                &hit.word.clone(),
                                display_text,
                                hit.syllables.join("'"),
                                hit.freq,
                                true,
                                RANK_SINGLE_BACKFILL,
                                false,
                            );
                        }
                    }
                }
            }
        }

        // 4.6 已有词组合候选（Go addCompositionCandidates，J 段）
        add_composition_candidates(
            &self.dict,
            self.user_dict.as_deref(),
            &shape,
            &patterns,
            prev,
            traditional,
            &mut pool,
        );

        // 4.7 纠错层级提升（Go promoteTypoCorrections）：必须在常规/单字收集
        // 完之后、单段过滤之前——它按 rank 反推「纯错拼来源」。
        promote_typo_corrections(&mut pool, &typo, &shape, &match_input);

        // 4.7 单段输入只出单字（Go filterSingleSegment）
        filter_single_segment(&mut pool, &patterns);

        // 5. 排序（Go sortCandidates/lessCand 镜像；Pool 保插入序 + Rust
        // sort_by 稳定排序 = Go sort.SliceStable）：① 回退层后置；② 超段仲裁
        // （仅全拼输入，候选音节数多于输入段数的后置——用户候选按 maxInputSegs
        // 判，非用户按首选切分判；简拼/混拼 token 数不是音节数，不参与）；
        // ③ 完整消费输入的候选（RANK_EXACT / RANK_INITIAL_EXACT，含提升到精确层
        // 的纠错）优先于还要补字母的续接/偏长词——用户词组只在同一消费层级内置顶，
        // 学过的续接词（shiji 下的 世界）仍排在 shi'ji 的词典词之后；
        // ④ 用户加权候选优先；⑤ 层级；⑥ 得分降序、词长、字典序。
        let input_segs = shape.input_segs;
        let max_input_segs = shape.max_input_segs;
        let input_has_initials = shape.input_has_initials;
        let mut sorted: Vec<(Candidate, i32, i32)> = pool.into_ordered_vec();
        sorted.sort_by(|a, b| {
            use std::cmp::Ordering;
            let a_backoff = a.2 >= RANK_BACKOFF;
            let b_backoff = b.2 >= RANK_BACKOFF;
            if a_backoff != b_backoff {
                return if a_backoff {
                    Ordering::Greater
                } else {
                    Ordering::Less
                };
            }
            if max_input_segs > 0 && !input_has_initials {
                let a_n = a.0.syllables.split('\'').count() as i32;
                let b_n = b.0.syllables.split('\'').count() as i32;
                let a_over = a_n > max_input_segs || (!a.0.from_user && a_n > input_segs);
                let b_over = b_n > max_input_segs || (!b.0.from_user && b_n > input_segs);
                if a_over != b_over {
                    return if a_over {
                        Ordering::Greater
                    } else {
                        Ordering::Less
                    };
                }
            }
            let a_full = a.2 <= RANK_INITIAL_EXACT;
            let b_full = b.2 <= RANK_INITIAL_EXACT;
            if a_full != b_full {
                return if a_full {
                    Ordering::Less
                } else {
                    Ordering::Greater
                };
            }
            if a.0.from_user != b.0.from_user {
                return if a.0.from_user {
                    Ordering::Less
                } else {
                    Ordering::Greater
                };
            }
            if a.2 != b.2 {
                return a.2.cmp(&b.2);
            }
            if a.1 != b.1 {
                return b.1.cmp(&a.1);
            }
            // Go len(Text) 是字节数；中文等宽 3 字节，与字符数比较等价
            if a.0.text.len() != b.0.text.len() {
                return a.0.text.len().cmp(&b.0.text.len());
            }
            a.0.text.cmp(&b.0.text)
        });
        // 纠错固定第 3 位（Go placeTypoFixedPosition）；之后再做词典词组封顶——
        // 固定位在索引 2，必在前 2 页之内，不会被封顶截掉。
        let sorted = place_typo_fixed_position(sorted);
        let sorted = cap_dict_phrases(sorted);        self.all_candidates = sorted.into_iter().map(|(c, s, _)| (c, s as u32)).collect();

        // 预编辑（Go buildComposition 口径）：默认原样输入；有候选且有首选切分时
        // 按切分边界重建并保留大小写（如 NI'HAO）——无候选（打错字/无切分）时
        // 保持原串，用户能看到「回车会原样上屏什么」。
        let preedit =
            if !self.all_candidates.is_empty() && !patterns.is_empty() && !patterns[0].is_empty() {
                preedit_with_case(&self.input_raw, &patterns[0])
            } else {
                self.input_raw.clone()
            };

        // 6. 构造分页 Composition
        let total = self.all_candidates.len();
        let page_count = if total == 0 {
            0
        } else {
            (total + self.page_size - 1) / self.page_size
        };

        if self.page >= page_count {
            self.page = 0;
        }

        let start = self.page * self.page_size;
        let end = (start + self.page_size).min(total);
        let page_cands = if start < total {
            self.all_candidates[start..end]
                .iter()
                .map(|(c, score)| {
                    let mut cand = c.clone();
                    cand.score = *score as i32;
                    cand
                })
                .collect()
        } else {
            Vec::new()
        };

        Composition {
            input: self.input_raw.clone(),
            preedit,
            candidates: page_cands,
            page: self.page,
            page_count,
            committed: String::new(),
            shuangpin: self
                .shuangpin
                .map(|s| s.name().to_string())
                .unwrap_or_default(),
        }
    }

    /// 用户键盘输入
    ///
    /// 与 Go engine.Session.Input 同契约：壳是标点唯一真源，input 只收拼音
    /// 字母，其余字符静默丢弃（标点走 punct()）。**大小写都收且保留原样**：
    /// 用户回车（commit_raw）应原样上屏自己敲的字母，不必为大写切输入法；
    /// 拼音匹配另走 match_input() 小写视图。双拼按键与大小写无关，统一小写
    /// 后成对解码。例外：双拼模式 ';' 是韵母键（mspy 的 ing），必须放行。
    pub fn input(&mut self, text: &str, half_width: bool) -> Result<Composition, String> {
        // 从空组词重新开始：上一轮渐进上屏的未完成分段不能带入本轮组词（否则
        // 渐进选「今天」→ 退格到空 → 打 wo 选「我」会把 [今天 我] 拼成伪整句）。
        if self.input_raw.is_empty() {
            self.seq_segs.clear();
        }
        // 与 Go engine.Session.Input 同契约：壳是标点唯一真源，input 只收拼音
        // 字母，其余字符静默丢弃（标点走 punct()）。**大小写都收且保留原样**：
        // 用户回车（commit_raw）应原样上屏自己敲的字母，不必为大写切输入法；
        // 拼音匹配另走 match_input() 小写视图。
        //
        // 双拼模式不同：按键大小写不敏感，两键成对解码后把**音节**写进输入串
        // （Go 同口径：input 是解码结果，不是键码）；dp_lens 记录每次按键事件
        // 实际追加的字符数，退格按「按键事件」整段回滚。
        for c in text.chars() {
            if self.shuangpin_decoder.is_some() {
                let lower = c.to_ascii_lowercase();
                match lower {
                    '\'' => {
                        self.input_raw.push('\'');
                        self.dp_lens.push(1);
                        self.after_append();
                    }
                    ';' if self.dp_pending.is_none() => {
                        // 没有暂存首键时 ';' 只能是标点：走完整 punct 语义（含
                        // 半角、同键循环与「组词中先上屏首选」），上屏文本放进
                        // committed。不处理同一 text 中 ';' 之后的字符：punct 已
                        // 结束本轮组词，壳按单键转发。
                        let out = self.punct(";", half_width)?;
                        let mut comp = self.compose();
                        comp.committed = out;
                        return Ok(comp);
                    }
                    'a'..='z' | ';' => match self.dp_pending {
                        None => self.dp_pending = Some(lower),
                        Some(pending) => {
                            self.dp_pending = None;
                            let pair = format!("{}{}", pending, lower);
                            let syl = self
                                .shuangpin_decoder
                                .as_ref()
                                .and_then(|d| d.decode(&pair));
                            match syl {
                                Some(syl) => {
                                    self.input_raw.push_str(&syl);
                                    self.dp_lens.push(syl.chars().count());
                                    self.after_append();
                                }
                                // 无效键码对：整体丢弃（与 Go 一致）
                                None => return Ok(self.compose()),
                            }
                        }
                    },
                    _ => {}
                }
                continue;
            }
            if c.is_ascii_alphabetic() || c == '\'' {
                self.input_raw.push(c);
            }
        }

        Ok(self.compose())
    }

    /// 壳重连后重建组词状态：把输入串整体置为 `text` 并重新组词，等价于用户
    /// 逐键敲出这一串。`text` 在全拼下是原始拼音、在双拼下是**已解码的音节串**
    /// （即上一次 Composition 的 input）。
    ///
    /// 用途（issue #122）：壳侧按键请求超时后管道断开，daemon 会话随连接消失，
    /// 而壳本地仍持有用户已输入的内容。重连后壳把「上次确认的输入串 + 此后
    /// 按键」一次送回（IPC `input` 的 `base` 参数），不必拆掉组词让用户重打。
    pub fn restore_input(&mut self, text: &str) -> Composition {
        let mut cleaned = String::with_capacity(text.len());
        for c in text.chars() {
            if c.is_ascii_alphabetic() || c == '\'' {
                cleaned.push(c);
            }
        }
        self.input_raw = cleaned;
        self.dp_pending = None;
        self.dp_lens.clear();
        self.page = 0;
        self.punct_state.reset();
        if self.shuangpin_decoder.is_some() {
            // 双拼退格按「按键事件」整段回滚：重建后没有按键栈，回滚粒度取
            // 首选切分的各音节长度（与逐键解码时的 dp_lens 同构）。
            if let Some(first) = tokenize_pinyin(&self.match_input()).first() {
                self.dp_lens = first
                    .iter()
                    .map(|t| t.text.chars().count())
                    .collect();
            }
        }
        self.compose()
    }

    /// `input` 的带基底版本：先把输入串整体重建为 `base`（见 `restore_input`），
    /// 再按 `input` 的语义追加 `text`。`base` 为 None 时与 `input` 完全等价。
    pub fn input_with_base(
        &mut self,
        text: &str,
        half_width: bool,
        base: Option<&str>,
    ) -> Result<Composition, String> {
        if let Some(base) = base {
            self.restore_input(base);
        }
        self.input(text, half_width)
    }

    /// 追加音节后的公共状态（Go afterAppend）：截断超长输入、同步双拼退格栈、
    /// 回第一页、打断标点循环。
    fn after_append(&mut self) {
        const MAX_INPUT_RUNES: usize = 64;
        let chars: Vec<char> = self.input_raw.chars().collect();
        if chars.len() > MAX_INPUT_RUNES {
            self.input_raw = chars[chars.len() - MAX_INPUT_RUNES..].iter().collect();
        }
        // 截断后栈的总长度可能超出输入长度：从头弹出多余项（Go reconcileDpLens）
        let mut total: usize = self.dp_lens.iter().sum();
        let len = self.input_raw.chars().count();
        while total > len && !self.dp_lens.is_empty() {
            total -= self.dp_lens.remove(0);
        }
        self.page = 0;
        self.punct_state.reset();
    }

    /// 用于拼音**匹配**的小写视图（对齐 Go matchInput）。
    ///
    /// 大写字母保留在 input_raw 里供预编辑显示与回车原样上屏；切分、查词、
    /// 纠错、联想一律按小写口径，保证「NIHAO」与「nihao」候选一致。
    fn match_input(&self) -> String {
        self.input_raw.to_ascii_lowercase()
    }

    /// 退格删除最后一个按键字符
    pub fn backspace(&mut self) -> Composition {
        // 双拼：删除粒度是「按键事件」——暂存中的首键直接清除，已解码音节
        // 整段回滚（对应两键），与 Go Backspace 同语义。
        if self.shuangpin_decoder.is_some() {
            if self.dp_pending.is_some() {
                self.dp_pending = None;
                return self.compose();
            }
            if let Some(cnt) = self.dp_lens.pop() {
                let chars: Vec<char> = self.input_raw.chars().collect();
                let cnt = cnt.min(chars.len());
                let keep = chars.len() - cnt;
                self.input_raw = chars[..keep].iter().collect();
            }
            self.page = 0;
            self.punct_state.reset();
            return self.compose();
        }
        self.input_raw.pop();
        self.compose()
    }

    /// 删除最后一个整词音节（按词退格）
    pub fn backspace_word(&mut self) -> Composition {
        // 双拼模式下单次 Backspace 已是完整音节回滚，直接复用（Go 同口径）。
        if self.shuangpin_decoder.is_some() {
            return self.backspace();
        }
        if let Some(last_tok) = self.tokens.pop() {
            let remove_len = last_tok.text.len();
            for _ in 0..remove_len {
                self.input_raw.pop();
            }
        } else {
            self.input_raw.pop();
        }
        self.compose()
    }

    /// 选择指定候选词（0~page_size-1）直接全部上屏
    pub fn select(&mut self, index: usize) -> Result<String, String> {
        let global_idx = self.page * self.page_size + index;
        if global_idx >= self.all_candidates.len() {
            // 与 Go 同文案：数量是**当前页**候选数（Composition.Candidates 长度）
            let page_count =
                (self.all_candidates.len() - self.page * self.page_size).min(self.page_size);
            return Err(format!(
                "候选序号 {} 越界（当前共 {} 个候选）",
                index + 1,
                page_count
            ));
        }

        let cand = self.all_candidates[global_idx].0.clone();
        let sylls: Vec<String> = cand.syllables.split('\'').map(|s| s.to_string()).collect();
        // 记录用户调频、共现、历史与词组进化（整句式记录按分段逐段学）
        let segs = self.candidate_segs(&cand, &sylls);
        self.learn_selection(&cand.text, &sylls, &segs);
        self.seq_segs.clear(); // 一次整词上屏：本次组词的渐进分段作废
        self.input_raw.clear();
        self.compose();

        Ok(cand.text)
    }

    /// 渐进上屏：只消耗匹配的前缀音节，剩余部分继续组词
    pub fn select_prefix(&mut self, index: usize) -> Result<Composition, String> {
        let global_idx = self.page * self.page_size + index;
        if global_idx >= self.all_candidates.len() {
            let page_count =
                (self.all_candidates.len() - self.page * self.page_size).min(self.page_size);
            return Err(format!(
                "候选序号 {} 越界（当前共 {} 个候选）",
                index + 1,
                page_count
            ));
        }

        let cand = self.all_candidates[global_idx].0.clone();
        let sylls: Vec<String> = cand.syllables.split('\'').map(|s| s.to_string()).collect();

        // 按音节消费输入前缀（Go consumeInputPrefix）：显式分隔符、省写/别名
        // （niou→niu）、模糊形态与半截拼音都按音节块匹配，不能按 token 字母数
        // 切字节串——旧实现选完「你好」后 `ni'hao'shi'jie` 会剩 `o'shi'jie`。
        // 一个音节都对不上（整句/简拼展开形态差异）或候选是纯错拼修正时回退
        // 整串上屏语义（Go 同口径：错拼修正候选把整串都解释掉了，残留字母不
        // 是待续组词的拼音）。
        let lower = self.input_raw.to_ascii_lowercase();
        let (rest, matched) = consume_input_prefix(&lower, &sylls, self.fuzzy);
        let rest = if matched == 0 || cand.typo_fix {
            String::new()
        } else {
            rest
        };
        let rest_empty = rest.is_empty();
        if rest_empty {
            self.input_raw.clear();
        } else {
            // rest 来自小写视图，ASCII 长度与原输入一致：按长度切回原输入，
            // 保留剩余字母的大小写。
            let keep = self.input_raw.len() - rest.len();
            self.input_raw = self.input_raw[keep..].to_string();
        }

        // 记录调频、共现、历史与词组进化
        let segs = self.candidate_segs(&cand, &sylls);
        self.learn_selection(&cand.text, &sylls, &segs);
        // 组词内容变了回到第一页（Go 同口径）：否则在第 N 页选段后，剩余拼音
        // 会停在原页码，首屏候选被跳过。
        self.page = 0;
        if rest_empty {
            // 本次组词由若干渐进上屏段拼完：把整串固化成整句习惯（分段 = 各段
            // 上屏词）。单段选完整个输入不触发（不是整句）。
            if !self.seq_segs.is_empty() {
                let mut all = std::mem::take(&mut self.seq_segs);
                all.push((cand.text.clone(), sylls.clone()));
                self.learn_progressive_sequence(&all);
            }
        } else {
            self.seq_segs.push((cand.text.clone(), sylls.clone()));
        }

        let mut comp = self.compose();
        comp.committed = cand.text;
        Ok(comp)
    }

    /// 下一页
    pub fn next_page(&mut self) -> Composition {
        let total = self.all_candidates.len();
        let page_count = if total == 0 {
            0
        } else {
            (total + self.page_size - 1) / self.page_size
        };

        if page_count > 0 && self.page + 1 < page_count {
            self.page += 1;
        }
        self.compose()
    }

    /// 上一页
    pub fn prev_page(&mut self) -> Composition {
        if self.page > 0 {
            self.page -= 1;
        }
        self.compose()
    }

    /// 重置清空当前输入会话与上下文
    pub fn reset(&mut self) -> Composition {
        self.input_raw.clear();
        self.last_committed = None;
        self.prev_sylls.clear();
        self.history.clear();
        self.seq_segs.clear();
        self.punct_state.reset();
        self.compose()
    }

    /// 仅重置当前组词，保留历史上下文
    pub fn reset_composition(&mut self) -> Composition {
        self.input_raw.clear();
        self.dp_pending = None;
        self.dp_lens.clear();
        self.compose()
    }

    /// 直接将未转化的原始拼音按键作为文本上屏
    pub fn commit_raw(&mut self) -> Result<String, String> {
        let text = self.input_raw.clone();
        self.input_raw.clear();
        self.compose();
        Ok(text)
    }

    /// 联想候选查询
    pub fn associate(&self, sylls: &[String]) -> Vec<Candidate> {
        const MAX_ASSOCIATE: usize = 30;
        // 必须刚上屏过词（s.prev）：联想的语义是「词组续接」，不是按拼音猜词。
        let prev = match self.last_committed.as_deref() {
            Some(p) if !p.is_empty() => p,
            _ => return Vec::new(),
        };
        // 剥前缀的切分基准一律取引擎自己记录的上屏音节（Go s.prevSylls），
        // 调用方传的 sylls 只作兜底：壳传来的数组长度不可信（Linux/Windows 壳
        // 的渐进上屏收尾把「上屏词文本」当音节传，单字「题」会被当成 1 个音节）。
        let prev_sylls: Vec<String> = if !self.prev_sylls.is_empty() {
            self.prev_sylls.clone()
        } else {
            sylls.to_vec()
        };

        // 1. 字前缀层命中：主词典 + 用户词组 + 用户调频词。
        // 严格按**字**（不回退音节前缀）：回退层会混入同音其它字的续接词
        // （选「测试」也弹「策士团」）。字前缀没有续接词时不弹联想。
        struct RawHit {
            word: String,
            syllables: Vec<String>,
            freq: i32,
            segs: Vec<String>,
            learned: bool,
        }
        let mut raw: Vec<RawHit> = Vec::new();
        for e in self.dict.query_word_prefix(prev, 200) {
            raw.push(RawHit {
                word: e.word.clone(),
                syllables: e.syllables.clone(),
                freq: e.freq as i32,
                segs: Vec::new(),
                learned: false,
            });
        }
        if let Some(u) = &self.user_dict {
            for h in u.query_phrases_by_word_prefix(prev) {
                raw.push(RawHit {
                    word: h.word,
                    syllables: h.syllables,
                    freq: h.freq,
                    segs: h.segs,
                    learned: true,
                });
            }
            for h in u.query_words_by_word_prefix(prev) {
                raw.push(RawHit {
                    word: h.word,
                    syllables: h.syllables,
                    freq: h.freq,
                    segs: h.segs,
                    learned: true,
                });
            }
        }

        // 2. 剥掉已上屏前缀：整句式学习记录只在用户确认过的分段边界剥
        // （打「今」绝不给「天星期一」）。
        let prev_chars = prev.chars().count();
        let mut out: Vec<(Candidate, bool)> = Vec::new();
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        for h in raw {
            let key = format!("{}|{}", h.word, h.syllables.join("'"));
            if !seen.insert(key) {
                continue;
            }
            let mut text = h.word.clone();
            let mut syl_str = h.syllables.join("'");
            if h.word.starts_with(prev) && h.syllables.len() > prev_sylls.len() {
                if h.learned {
                    let segs = self.entry_segs(&h.word, &h.syllables, &h.segs);
                    if segs.len() >= 2 {
                        match self.strip_at_segment_boundary(
                            &h.word,
                            &h.syllables,
                            &segs,
                            prev,
                            &prev_sylls,
                        ) {
                            Some((t, s)) => {
                                text = t;
                                syl_str = s;
                            }
                            None => continue, // 不在确认过的分段边界：不提供整词尾巴
                        }
                    } else {
                        text = h.word.chars().skip(prev_chars).collect();
                        syl_str = h.syllables[prev_sylls.len()..].join("'");
                    }
                } else {
                    text = h.word.chars().skip(prev_chars).collect();
                    syl_str = h.syllables[prev_sylls.len()..].join("'");
                }
                if text.is_empty() {
                    continue;
                }
            }
            out.push((
                Candidate {
                    text,
                    syllables: syl_str,
                    score: h.freq,
                    from_user: false,
                    typo_fix: false,
                },
                true, // 全部来自字前缀层
            ));
        }

        // 3. 上下文加成：调频 + 「上屏词 → 联想词」共现（Bigram）。
        if let Some(u) = &self.user_dict {
            for item in out.iter_mut() {
                item.0.score += u.score_full(&item.0.text, Some(prev));
            }
        }

        // 4. 字前缀层优先，层内按加成后得分降序、同分按文本（与 Go 稳定排序一致）
        out.sort_by(|a, b| {
            b.1.cmp(&a.1)
                .then(b.0.score.cmp(&a.0.score))
                .then(a.0.text.cmp(&b.0.text))
        });
        out.truncate(MAX_ASSOCIATE);
        out.into_iter().map(|(c, _)| c).collect()
    }

    /// 联想词直接上屏
    pub fn associate_select(&mut self, word: &str, sylls: &[String]) -> Result<String, String> {
        // 联想上屏只回传 word+sylls：分段用主词典推断（Go 同口径），走与选词
        // 相同的学习入口（调频 / 共现 / 历史 / 词组进化）。
        let segs = self.infer_segs(word, sylls);
        self.learn_selection(word, sylls, &segs);
        self.seq_segs.clear();
        Ok(word.to_string())
    }

    /// 处理标点输入（组词中先上屏首选词再上屏标点）
    pub fn punct(&mut self, key: &str, half_width: bool) -> Result<String, String> {
        let c = key.chars().next().ok_or("标点键为空")?;
        if !crate::punct::is_punct_key(c) {
            return Err(format!("未知标点键 {:?}", key));
        }

        if !self.input_raw.is_empty() {
            if c == '\'' {
                return Err("组词中的 ' 是音节分隔符，不作为标点提交".to_string());
            }

            let committed = if !self.all_candidates.is_empty() {
                self.select(0)?
            } else {
                self.input_raw.clear();
                self.tokens.clear();
                self.all_candidates.clear();
                String::new()
            };

            let punct_str = self.punct_state.process(c, half_width);
            Ok(format!("{}{}", committed, punct_str))
        } else {
            let punct_str = self.punct_state.process(c, half_width);
            Ok(punct_str)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn create_test_session() -> Session {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "你好".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string()],
            freq: 9000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "你".to_string(),
            syllables: vec!["ni".to_string()],
            freq: 5000,
        });
        dict.sort_nodes();

        Session::new(Arc::new(dict), None)
    }

    #[test]
    fn test_session_input_and_select() {
        let mut sess = create_test_session();
        let comp = sess.input("nihao", false).unwrap();
        assert_eq!(comp.preedit, "ni'hao");
        assert!(!comp.candidates.is_empty());
        assert_eq!(comp.candidates[0].text, "你好");

        let committed = sess.select(0).unwrap();
        assert_eq!(committed, "你好");

        let comp_after = sess.compose();
        assert!(comp_after.input.is_empty());
    }

    #[test]
    fn test_session_select_prefix() {
        let mut sess = create_test_session();
        sess.input("nihao", false).unwrap();

        // 假设选择第 2 项「你」（单音节），应消耗 "ni"，剩余 "hao"
        let comp = sess.select_prefix(1).unwrap();
        assert_eq!(comp.committed, "你");
        assert_eq!(comp.input, "hao");
    }

    #[test]
    fn test_session_backspace() {
        let mut sess = create_test_session();
        sess.input("ni", false).unwrap();
        let comp = sess.backspace();
        assert_eq!(comp.input, "n");
    }

    /// 输入过滤契约（对齐 Go Input）：整体小写、只收 [a-z']，其余字符
    /// 静默丢弃（标点由壳路由到 punct()，不在 input 里顶字上屏）。
    #[test]
    fn test_input_filters_non_pinyin_chars() {
        let mut sess = create_test_session();
        let comp = sess.input("NI1hao!?", false).unwrap();
        // 大小写保留（回车原样上屏），非字母丢弃；预编辑同样保留大小写。
        assert_eq!(comp.input, "NIhao");
        assert_eq!(comp.preedit, "NI'hao");
        assert!(comp.committed.is_empty(), "input 不得代行标点上屏");
        assert_eq!(comp.candidates[0].text, "你好");

        // 全部为非法字符：组词保持为空（新会话，input 累积语义与 Go 一致——
        // 非法字符不产生新组词，也不清掉既有组词）
        let mut sess2 = create_test_session();
        let comp2 = sess2.input("123", false).unwrap();
        assert!(comp2.input.is_empty());
        assert!(comp2.candidates.is_empty());
    }

    /// 大写输入：匹配与全小写等价，输入/预编辑保留大小写，退格保留剩余大小写，
    /// commit_raw 原样返回 —— 中文模式下直接敲大写 + 回车即可上屏英文。
    #[test]
    fn test_uppercase_input_preserved() {
        let mut sess = create_test_session();
        let comp = sess.input("NIHAO", false).unwrap();
        assert_eq!(comp.input, "NIHAO");
        assert_eq!(comp.preedit, "NI'HAO");
        assert_eq!(
            comp.candidates[0].text, "你好",
            "大写应按小写匹配: {:?}",
            comp.candidates
        );

        let comp = sess.backspace();
        assert_eq!(comp.input, "NIHA");

        let raw = sess.commit_raw().unwrap();
        assert_eq!(raw, "NIHA", "commit_raw 应原样返回用户敲的字符");
    }

    /// CommitRaw 全串原样（含大小写）——Enter 直接上屏英文。
    #[test]
    fn test_commit_raw_keeps_case() {
        let mut sess = create_test_session();
        sess.input("HelloWorld", false).unwrap();
        assert_eq!(sess.commit_raw().unwrap(), "HelloWorld");
    }

    /// 纯音节切分优先于声母碎片：xian 是合法音节，不得混入 xia'n
    /// 这类碎片切分展开的候选（对齐 Go：碎片只在整段无音节解时兜底）。
    #[test]
    fn test_fragment_patterns_lose_to_pure_syllables() {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "先".to_string(),
            syllables: vec!["xian".to_string()],
            freq: 5000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "瞎闹".to_string(),
            syllables: vec!["xia".to_string(), "nao".to_string()],
            freq: 9000, // 词频更高也不得靠碎片切分胜出
        });
        dict.sort_nodes();
        let mut sess = Session::new(Arc::new(dict), None);

        let comp = sess.input("xian", false).unwrap();
        for cd in &comp.candidates {
            let first = cd.syllables.split('\'').next().unwrap_or("");
            assert_ne!(
                first, "xia",
                "碎片切分不得污染纯音节输入: {:?}",
                comp.candidates
            );
        }
        assert_eq!(comp.candidates[0].text, "先");
    }

    /// 简拼与无候选兜底：整段没有合法音节切分时，碎片导航才参与召回
    /// （nh → n/h 展开、zzz → z'z'z 展开）。
    #[test]
    fn test_fragment_fallback_recall() {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "你好".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string()],
            freq: 9000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "自治州".to_string(),
            syllables: vec!["zi".to_string(), "zhi".to_string(), "zhou".to_string()],
            freq: 800,
        });
        dict.sort_nodes();
        let mut sess = Session::new(Arc::new(dict), None);

        // 简拼 nh：n/h 碎片展开召回 你好
        let comp = sess.input("nh", false).unwrap();
        assert!(
            comp.candidates.iter().any(|c| c.text == "你好"),
            "简拼应召回 你好: {:?}",
            comp.candidates
        );

        // zzz：无任何合法音节，碎片 z'z'z 兜底召回 自治州（zi'zhi'zhou 每个
        // 音节都以 z 开头）
        sess.reset();
        let comp2 = sess.input("zzz", false).unwrap();
        assert!(
            comp2.candidates.iter().any(|c| c.text == "自治州"),
            "碎片兜底应召回 自治州: {:?}",
            comp2.candidates
        );
    }

    /// 末段残缺续接（RANK_CONTINUATION）严格排在完整消费输入的精确匹配之后：
    /// zhonggu 先给 中古（zhong'gu），续接 中国（gu→guo，词频再高也在精确层之后）
    /// 留在首页可达；daxu 没有精确竞争者，续接 大学 仍是首选。
    #[test]
    fn test_exact_consumption_beats_continuation() {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "中国".to_string(),
            syllables: vec!["zhong".to_string(), "guo".to_string()],
            freq: 85,
        });
        dict.insert(crate::dict::WordEntry {
            word: "中古".to_string(),
            syllables: vec!["zhong".to_string(), "gu".to_string()],
            freq: 36,
        });
        dict.insert(crate::dict::WordEntry {
            word: "大学".to_string(),
            syllables: vec!["da".to_string(), "xue".to_string()],
            freq: 80,
        });
        dict.sort_nodes();
        let mut sess = Session::new(Arc::new(dict), None);

        let comp = sess.input("zhonggu", false).unwrap();
        assert_eq!(
            comp.candidates[0].text, "中古",
            "完整消费输入的 中古 应先于续接 中国: {:?}",
            comp.candidates
        );
        assert_eq!(
            comp.candidates[1].text, "中国",
            "续接 中国 须紧随其后、首页可达: {:?}",
            comp.candidates
        );

        sess.reset();
        let comp2 = sess.input("daxu", false).unwrap();
        assert_eq!(
            comp2.candidates[0].text, "大学",
            "xu→xue 续接: {:?}",
            comp2.candidates
        );
    }

    /// 末段整体非音节的续接领跑（rankContinuationLead）：quanmia 常规切分
    /// 只能出碎片假词，续接必须领跑出「全面」。
    #[test]
    fn test_continuation_lead_for_broken_tail() {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "全面".to_string(),
            syllables: vec!["quan".to_string(), "mian".to_string()],
            freq: 80,
        });
        dict.sort_nodes();
        let mut sess = Session::new(Arc::new(dict), None);

        let comp = sess.input("quanmia", false).unwrap();
        assert!(
            comp.candidates.iter().any(|c| c.text == "全面"),
            "残缺末段应续接出 全面: {:?}",
            comp.candidates
        );
    }

    /// 单段输入只出单字（Go 硬约束）：xian 只出先/线等同音单字，西安必须
    /// 显式 xi'an；zh/ch/sh 双字母简拼仍出词候选。
    #[test]
    fn test_single_segment_only_single_chars() {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "先".to_string(),
            syllables: vec!["xian".to_string()],
            freq: 5000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "西安".to_string(),
            syllables: vec!["xi".to_string(), "an".to_string()],
            freq: 9000, // 词频更高也不得出现在 xian 单段输入
        });
        dict.insert(crate::dict::WordEntry {
            word: "中国".to_string(),
            syllables: vec!["zhong".to_string(), "guo".to_string()],
            freq: 9000,
        });
        dict.sort_nodes();
        let mut sess = Session::new(Arc::new(dict), None);

        let comp = sess.input("xian", false).unwrap();
        assert!(
            comp.candidates.iter().all(|c| c.text.chars().count() == 1),
            "单段全拼只出单字: {:?}",
            comp.candidates
        );

        // zh 是双字母简拼，仍出词候选
        sess.reset();
        let comp2 = sess.input("zh", false).unwrap();
        assert!(
            comp2.candidates.iter().any(|c| c.text == "中国"),
            "双字母简拼 zh 应保留词候选: {:?}",
            comp2.candidates
        );
    }

    /// 回退提升特例（Go drop 段）：nihaox 整串无精确命中、去掉末位碎片 x 后
    /// 剩余全整音节，你好 提升到精确层置顶；且多段输入末尾是纯单字收尾
    /// （rankSingleBackfill）。
    #[test]
    fn test_backoff_promotion_and_single_backfill() {
        let mut sess = create_test_session();
        sess.input("nihao", false).unwrap();
        // 构造 nihaox：在 create_test_session 的词库下，nihaox 首选 你好
        sess.reset();
        let comp = sess.input("nihaox", false).unwrap();
        assert_eq!(
            comp.candidates[0].text, "你好",
            "回退提升应让 你好 置顶: {:?}",
            comp.candidates
        );
        // 多段输入：列表最末是单字（rankSingleBackfill 收尾）
        let last = comp.candidates.last().unwrap();
        assert_eq!(
            last.text.chars().count(),
            1,
            "多段输入应以单字收尾: {:?}",
            comp.candidates
        );
    }

    /// 超段仲裁（仅全拼输入）：候选音节数多于输入段数的后置——你好吗
    /// （3 音节）词频再高也不得排到 你好（2 音节=完全消耗）之前。
    #[test]
    fn test_over_segment_arbitration() {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "你好".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string()],
            freq: 100, // 词频低
        });
        dict.insert(crate::dict::WordEntry {
            word: "你好吗".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string(), "ma".to_string()],
            freq: 9000, // 词频极高但音节数超过输入段数
        });
        dict.sort_nodes();
        let mut sess = Session::new(Arc::new(dict), None);

        let comp = sess.input("nihao", false).unwrap();
        assert_eq!(
            comp.candidates[0].text, "你好",
            "完全消耗优先于超段: {:?}",
            comp.candidates
        );
        assert!(
            comp.candidates
                .iter()
                .position(|c| c.text == "你好吗")
                .unwrap()
                > comp
                    .candidates
                    .iter()
                    .position(|c| c.text == "你好")
                    .unwrap()
        );
    }
    // ── 会话级错拼回归（Go typo_test.go / typo_common_mistakes_test.go 镜像）──

    /// 加载内置词库（与 Go mustCommonDict 同源：仓库根 data/dict_common.tsv）。
    fn common_dict() -> Arc<Dictionary> {
        let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../../data/dict_common.tsv");
        let mut dict = Dictionary::new();
        dict.load_tsv(path)
            .unwrap_or_else(|e| panic!("加载内置词库失败 {path}: {e}"));
        Arc::new(dict)
    }

    /// Go assertTypoCorrectionPosition 镜像：纠错结果必须在候选里；位置恰为
    /// 第 3 位（下标 2）或每个候选都是纠错候选（全部 typoFix）时算通过。
    fn assert_typo_position(cands: &[Candidate], input: &str, want: &str) {
        let idx = cands.iter().position(|c| c.text == want);
        let Some(idx) = idx else {
            panic!("{input}: 候选里没有纠错结果「{want}」: {cands:?}");
        };
        if idx == 2 {
            return; // 固定第 3 位
        }
        if !cands.is_empty() && cands.iter().all(|c| c.typo_fix) {
            return;
        }
        panic!("{input}: 纠错「{want}」位置 {idx}（期望第 3 位或全纠错列表）: {cands:?}");
    }

/// 错拼组合候选的「同批噪声」必须随整词命中一起丢弃（2026-11-24 用户要求：
    /// 错拼形态在日常输入中高频出现——「完整前缀 + 末音节首字母」是每个多音节输入
    /// 的必经帧）。
    ///
    /// 正向：`word_found`（已有整词级纠错命中）时，同一串的拼合产物（把已命中的词
    /// 拆成两段：你+号、我+门）不得留在候选里——固定口径扫描 291 常用词 × 26 尾字母
    /// = 7566 形态，修复前 50.7% 的首页含这类噪声，修复后 0.0%。
    /// 反向：无整词命中的错拼（chagnshuru → 长输入）必须仍出组合候选；常规拼写与
    /// 整词纠错候选（一周目）不受影响；错拼固定第 3 位契约不变。
    #[test]
    fn test_typo_compositions_dropped_when_word_found() {
        let dict = common_dict();
        let comp_of = |input: &str| {
            let mut sess = Session::new(Arc::clone(&dict), None);
            sess.input(input, false).unwrap()
        };

        // 正向：整词命中「你好」后，拼合噪声（你+号/你+毫/你+薅）必须消失。
        let comp = comp_of("nihaox");
        assert_eq!(comp.candidates[0].text, "你好", "整词纠错仍须首选: {:?}", comp.candidates);
        for noise in ["你号", "你毫", "你薅"] {
            assert!(
                !comp.candidates.iter().any(|c| c.text == noise),
                "nihaox: 整词命中后不得出现拼合噪声「{noise}」: {:?}",
                comp.candidates
            );
        }

        // 正向：womend 是打「我们的」的必经中间态——「我门/握们/我闷/沃们」不得占位。
        let comp = comp_of("womend");
        assert_eq!(comp.candidates[0].text, "我们");
        for noise in ["我门", "握们", "我闷", "沃们"] {
            assert!(
                !comp.candidates.iter().any(|c| c.text == noise),
                "womend: 不得出现拼合噪声「{noise}」: {:?}",
                comp.candidates
            );
        }

        // 正向：jintianx 的「进天」（进+天）同样不得出现。
        let comp = comp_of("jintianx");
        assert_eq!(comp.candidates[0].text, "今天");
        assert!(
            !comp.candidates.iter().any(|c| c.text == "进天"),
            "jintianx: 不得出现拼合噪声「进天」: {:?}",
            comp.candidates
        );

        // 反向：无整词级命中的错拼仍须给组合候选（chagnshuru 没有整词纠错命中，
        // 「长输入」是把修正串切成已有词的唯一线索，promote_typo_compositions 会提升它）。
        let comp = comp_of("chagnshuru");
        assert!(
            comp.candidates.iter().any(|c| c.text == "长输入" && c.typo_fix),
            "chagnshuru: 无整词命中时组合候选必须保留: {:?}",
            comp.candidates
        );

        // 反向：整词纠错候选（一周目）是正确修复结果，不能被当组合噪声删除。
        let comp = comp_of("yizhoumui");
        assert!(
            comp.candidates.iter().any(|c| c.text == "一周目" && c.typo_fix),
            "yizhoumui: 整词纠错「一周目」必须保留: {:?}",
            comp.candidates
        );

        // 反向：常规拼写与既有首选完全不受影响。
        assert_eq!(comp_of("nihao").candidates[0].text, "你好");
        assert_eq!(comp_of("zhongguo").candidates[0].text, "中国");
    }

    /// 单次编辑能修正的常见手误（相邻颠倒 / 多敲 / 连击 / 漏敲）。
    #[test]
    fn test_typo_correction_fixes_common_mistakes() {
        let dict = common_dict();
        let cases = [
            ("zhogn", "中"),
            ("zhonng", "中"),
            ("zhoonng", "中"),
            ("zhonguo", "中国"),
            ("jisuanjii", "计算机"),
            ("niihao", "你好"),
            ("niihaoo", "你好"),
        ];
        for (input, want) in cases {
            let mut sess = Session::new(Arc::clone(&dict), None);
            let comp = sess.input(input, false).unwrap();
            assert!(
                !comp.candidates.is_empty(),
                "{input}: 应有候选（期望「{want}」）"
            );
            assert_typo_position(&comp.candidates, input, want);
        }
    }

    /// 反序双字母归位（gn/hz/hc/hs）：dign→定、hzong→中、hcang→常、hsang→上。
    #[test]
    fn test_common_mistake_bigrams() {
        let dict = common_dict();
        for (input, want) in [
            ("dign", "定"),
            ("dagn", "当"),
            ("zhogn", "中"),
            ("chagn", "常"),
            ("shagn", "上"),
            ("hzong", "中"),
            ("hcang", "常"),
            ("hsang", "上"),
            ("hsuo", "说"),
        ] {
            let mut sess = Session::new(Arc::clone(&dict), None);
            let comp = sess.input(input, false).unwrap();
            assert!(
                !comp.candidates.is_empty(),
                "{input}: 应有候选（期望「{want}」）"
            );
            assert_typo_position(&comp.candidates, input, want);
        }
    }

    /// 反向约束（最关键）：错拼修正绝不能污染正常输入的首选。
    /// 合法简拼 / 混拼与手误在串形态上高度相似，判据一放宽就会挤掉正常结果。
    #[test]
    fn test_typo_does_not_disturb_normal_inputs() {
        let dict = common_dict();
        for (input, want) in [
            ("zg", "中国"),
            ("bj", "北京"),
            ("jsj", "计算机"),
            ("zhongg", "中国"),
            ("chifanl", "吃饭"),
            ("womd", "我们"),
            ("nihao", "你好"),
            ("jingchang", "经常"),
            ("jisuanji", "计算机"),
            ("zhongguo", "中国"),
            ("shanghai", "上海"),
            ("diannao", "电脑"),
        ] {
            let mut sess = Session::new(Arc::clone(&dict), None);
            let comp = sess.input(input, false).unwrap();
            assert!(!comp.candidates.is_empty(), "{input}: 应有候选");
            assert_eq!(
                comp.candidates[0].text, want,
                "{input}: 第一候选 = {:?}, 期望「{want}」（纠错候选不得挤掉正常结果）",
                comp.candidates[0].text
            );
        }
    }

    /// 反序归位在多音节词里也要能救回来（dignxiang→丁香、zhognguo→中国…）。
    #[test]
    fn test_common_mistake_bigrams_in_words() {
        let dict = common_dict();
        for (input, want) in [
            ("zhognguo", "中国"),
            ("beijign", "北京"),
            ("shagnhai", "上海"),
            ("xignfu", "幸福"),
            ("degndai", "等待"),
            ("pegnyou", "朋友"),
            ("jignchang", "经常"),
            ("zhognwen", "中文"),
            ("hzongguo", "中国"),
        ] {
            let mut sess = Session::new(Arc::clone(&dict), None);
            let comp = sess.input(input, false).unwrap();
            assert!(
                comp.candidates.iter().any(|c| c.text == want),
                "{input}: 纠错后应能出「{want}」，实际前 8 条 {:?}",
                comp.candidates
                    .iter()
                    .take(8)
                    .map(|c| &c.text)
                    .collect::<Vec<_>>()
            );
        }
    }

    /// 软纠错（韵母级反序 ao/ua/ue/ve）：hoa→好、hau→化、xeu→学、lev→略；
    /// 且正确拼写（hao/hua/xue/lve/que）首选不能被软纠错改变。
    #[test]
    fn test_common_mistake_soft_bigrams() {
        let dict = common_dict();
        for (input, want) in [
            ("hoa", "好"),
            ("doa", "大"),
            ("hau", "化"),
            ("xeu", "学"),
            ("lev", "略"),
        ] {
            let mut sess = Session::new(Arc::clone(&dict), None);
            let comp = sess.input(input, false).unwrap();
            assert!(
                !comp.candidates.is_empty(),
                "{input}: 应有候选（期望「{want}」）"
            );
            assert_typo_position(&comp.candidates, input, want);
        }
        for (input, want) in [
            ("hao", "好"),
            ("hua", "化"),
            ("xue", "学"),
            ("lve", "略"),
            ("que", "却"),
        ] {
            let mut sess = Session::new(Arc::clone(&dict), None);
            let comp = sess.input(input, false).unwrap();
            assert_eq!(
                comp.candidates[0].text, want,
                "{input}: 第一候选 = {:?}, 期望「{want}」（正常输入不应被软纠错影响）",
                comp.candidates[0].text
            );
        }
    }
    /// 联想按**上屏词的字**续接（Go Associate）：选「测试」→ 联想「测试题/测试员」，
    /// 不混入同音其它字的续接词（策士团），层内按词频降序。
    #[test]
    fn test_associate_word_prefix() {
        let mut dict = Dictionary::new();
        for (word, pinyin, freq) in [
            ("测试", "ce'shi", 100u32),
            ("测试题", "ce'shi'ti", 40),
            ("测试员", "ce'shi'yuan", 30),
            ("策士团", "ce'shi'tuan", 90),
        ] {
            dict.insert(crate::dict::WordEntry {
                word: word.to_string(),
                syllables: pinyin.split('\'').map(|s| s.to_string()).collect(),
                freq,
            });
        }
        dict.sort_nodes();
        let mut sess = Session::new(std::sync::Arc::new(dict), None);
        sess.input("ceshi", false).unwrap();
        let idx = sess
            .all_candidates
            .iter()
            .position(|(c, _)| c.text == "测试")
            .expect("应有「测试」候选");
        sess.select(idx).unwrap();
        let assoc = sess.associate(&["ce".to_string(), "shi".to_string()]);
        let texts: Vec<&str> = assoc.iter().map(|c| c.text.as_str()).collect();
        assert_eq!(texts, ["题", "员"], "只续接同字前缀、按词频降序: {texts:?}");
    }

    /// 整句安全约束（项目红线）：整句式学习记录只在用户确认过的**分段边界**
    /// 剥前缀——确认过「今天 / 星期一」时，上屏「今天」给出「星期一」，
    /// 上屏「今」绝不给「天星期一」。
    #[test]
    fn test_associate_sentence_only_at_segment_boundary() {
        let dict = std::sync::Arc::new(Dictionary::new());
        let user = std::sync::Arc::new(UserDict::new::<&str>(None));
        user.bump_word(
            "今天星期一",
            Some("jin'tian'xing'qi'yi"),
            Some(vec!["今天".to_string(), "星期一".to_string()]),
        );
        let mut sess = Session::new(dict, Some(user));

        // 边界：上屏「今天」→ 只给「星期一」
        sess.last_committed = Some("今天".to_string());
        sess.prev_sylls = vec!["jin".to_string(), "tian".to_string()];
        let assoc = sess.associate(&[]);
        let texts: Vec<&str> = assoc.iter().map(|c| c.text.as_str()).collect();
        assert_eq!(texts, ["星期一"], "分段边界上应给出续接段: {texts:?}");
        assert_eq!(assoc[0].syllables, "xing'qi'yi");

        // 非边界：上屏「今」→ 不得给出整词尾巴「天星期一」
        sess.last_committed = Some("今".to_string());
        sess.prev_sylls = vec!["jin".to_string()];
        let assoc = sess.associate(&[]);
        assert!(
            assoc.is_empty(),
            "未确认的分段边界不得提供整词尾巴: {:?}",
            assoc.iter().map(|c| c.text.as_str()).collect::<Vec<_>>()
        );
    }
    /// 模糊变体展开（Go expandTokenVariants）：关模糊时原样一条；开模糊时
    /// 完整音节展开、碎片保留，首条必为原路径，路径数封顶 8。
    #[test]
    fn test_expand_token_variants() {
        let tokens = vec![Token {
            text: "shi".to_string(),
            is_exact: true,
        }];
        let off = expand_token_variants(&tokens, false);
        assert_eq!(off.len(), 1);
        assert_eq!(off[0][0].text, "shi");

        let on = expand_token_variants(&tokens, true);
        assert_eq!(on[0][0].text, "shi", "首条必须是原路径");
        assert!(on.len() > 1, "模糊变体应展开出多条路径: {}", on.len());
        assert!(on.len() <= 8, "路径数封顶 8: {}", on.len());
        assert!(
            on.iter().flatten().any(|t| t.text == "si"),
            "shi 的模糊变体应含 si"
        );

        // 多音节：各音节变体做笛卡尔积（受 8 条上限约束）
        let multi = vec![
            Token {
                text: "shi".to_string(),
                is_exact: true,
            },
            Token {
                text: "shi".to_string(),
                is_exact: true,
            },
        ];
        let paths = expand_token_variants(&multi, true);
        assert!(paths.len() > 1);
        assert!(paths.len() <= 8);
        for p in &paths {
            assert_eq!(p.len(), 2, "每条路径音节数不变");
        }
    }
    /// 双拼流式集成（与 Go TestDoublePinyinSession 同口径）：逐键输入成对解码、
    /// 退格按「按键事件」整段回滚、暂存首键的退格只清暂存。
    #[test]
    fn test_shuangpin_session_stream() {
        let mut sess = create_test_session();
        sess.set_shuangpin(Some(ShuangpinScheme::Zrm));

        // 首键暂存：组成状态为空（不显示未解码的键码）
        let comp = sess.input("n", false).unwrap();
        assert!(
            comp.candidates.is_empty() && comp.input.is_empty(),
            "首键暂存应为空组成: {:?}",
            comp
        );

        let comp = sess.input("i", false).unwrap();
        assert_eq!(comp.input, "ni", "成对后输入应为解码音节");

        sess.input("h", false).unwrap();
        let comp = sess.input("k", false).unwrap();
        assert_eq!(comp.input, "nihao");
        assert_eq!(comp.preedit, "ni'hao");
        assert!(
            comp.candidates.iter().any(|c| c.text == "你好"),
            "nihao 应命中「你好」: {:?}",
            comp.candidates
        );

        // 退格：整段回滚 hao → 只剩 ni
        let comp = sess.backspace();
        assert_eq!(comp.input, "ni", "退格应整段回滚 hao");

        // 暂存首键的退格：清除暂存，输入不变
        sess.input("h", false).unwrap();
        let comp = sess.backspace();
        assert_eq!(comp.input, "ni", "暂存首键的退格只清暂存");
    }
    /// 错拼输入不追加单字兜底（项目硬约束，Go 同口径）：lianxiagn 的候选里
    /// 不得出现只靠 rankSingleBackfill 层才会召回的冷僻同音字。
    #[test]
    fn test_typo_input_has_no_single_char_backfill() {
        let mut sess = Session::new(common_dict(), None);
        let comp = sess.input("lianxiagn", false).unwrap();
        assert!(!comp.candidates.is_empty(), "错拼输入应有纠错候选");
        assert!(
            !comp.candidates.iter().any(|c| c.text == "莲"),
            "错拼输入不得追加单字兜底层（莲 只靠兜底才会出现）: {:?}",
            comp.candidates
                .iter()
                .map(|c| c.text.as_str())
                .collect::<Vec<_>>()
        );
    }
    /// 用户词库参与组词召回（Go 同口径）：主词典没有的词，学过之后能打出来，
    /// 且候选带 from_user 标记（壳据此显示学习标记）。
    #[test]
    fn test_user_word_recall() {
        let user = std::sync::Arc::new(UserDict::new::<&str>(None));
        user.bump_word("幂等性", Some("mi deng xing"), None);
        let mut sess = Session::new(common_dict(), Some(user));
        let comp = sess.input("midengxing", false).unwrap();
        let hit = comp.candidates.iter().find(|c| c.text == "幂等性");
        assert!(
            hit.is_some(),
            "用户学过的词应出现在候选: {:?}",
            comp.candidates
                .iter()
                .map(|c| c.text.as_str())
                .collect::<Vec<_>>()
        );
        assert!(hit.unwrap().from_user, "用户词条应带 from_user 标记");
    }

    /// 整句安全（项目红线）：整句式学习记录不得作为部分输入的前缀候选——
    /// 打「今」不能出「今天星期一」；完整输入时才作为整句候选出现。
    #[test]
    fn test_user_sentence_not_prefix_candidate() {
        let user = std::sync::Arc::new(UserDict::new::<&str>(None));
        user.bump_word(
            "今天星期一",
            Some("jin tian xing qi yi"),
            Some(vec!["今天".to_string(), "星期一".to_string()]),
        );
        let mut sess = Session::new(common_dict(), Some(user));
        let comp = sess.input("jin", false).unwrap();
        assert!(
            !comp.candidates.iter().any(|c| c.text == "今天星期一"),
            "部分输入不得出现整句记录: {:?}",
            comp.candidates
                .iter()
                .map(|c| c.text.as_str())
                .collect::<Vec<_>>()
        );
        let comp = sess.input("tianxingqiyi", false).unwrap();
        assert!(
            comp.candidates.iter().any(|c| c.text == "今天星期一"),
            "完整输入应给出整句候选: {:?}",
            comp.candidates
                .iter()
                .map(|c| c.text.as_str())
                .collect::<Vec<_>>()
        );
    }
}
