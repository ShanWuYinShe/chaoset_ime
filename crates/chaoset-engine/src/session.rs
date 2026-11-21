//! 输入法会话状态机：按键组词、候选词排序、分页、渐进选择与联想。

use std::sync::Arc;

use crate::dict::Dictionary;
use crate::fuzzy::fuzzy_syllable_variants;
use crate::punct::PunctState;
use crate::s2t::to_traditional;
use crate::shuangpin::{ShuangpinDecoder, ShuangpinScheme};
use crate::syllable::{tokenize_pinyin, Token, TypoCorrection};
use crate::userdict::UserDict;

pub const PAGE_SIZE: usize = 6;

// ── 匹配层级常量（对齐 Go compose_rank.go，数值相对关系不能变）────────
// 层级是第一排序维度：跨层的先后由 rank 决定，词频打分只在同层内起作用。
const RANK_EXACT: i32 = 0; // 全拼单词完全匹配（jingchang → 经常）
const RANK_INITIAL_EXACT: i32 = 1; // 简拼/混拼单词完全匹配（zg → 中国）
                                   // 注：Go 侧该常量同样仅在层级表中定义——续接候选的实际层级由 shape 决定
                                   // （存在干净解析取 rankExact，否则取 rankContinuationLead 领跑）。保留
                                   // 常量以维持与 Go 层级表的逐值对应。
#[allow(dead_code)]
const RANK_CONTINUATION: i32 = 2; // 末段残缺续接（zhonggu → 中国，daxu → 大学）
const RANK_INITIAL_PREFIX: i32 = 3; // 简拼偏长词（zg → 中国人民）
const RANK_PREFIX: i32 = 4; // 全拼偏长词续接（jing → 经济）
const RANK_BACKOFF: i32 = 5; // 逐级回退前缀（nihaox → 你好）
const RANK_CONTINUATION_LEAD: i32 = 6; // 无法干净切分时的续接领跑（quanmia → 全面）
const RANK_TYPO_FIX: i32 = 7; // 错拼修正候选初始层级（最低，仅保证列表末尾可见）

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

// Go：rankComposition = rankBackoff+64、rankSingleBackfill = rankComposition+16。
// 组合候选（rankComposition 层）本轮未迁移；单字收尾保持在其后（列表最末纯单字）。
const RANK_SINGLE_BACKFILL: i32 = RANK_BACKOFF + 64 + 16;

/// 续接候选每多补一个字符的得分折价。Go 数据扫描定型值：30 是唯一同时保住
/// 「全拼完整输入首选率」与「末音节残缺首选率」的点（中国 85−30=55 > 中古 36）。
const CONTINUATION_PENALTY: i32 = 30;

/// 单音节全拼/单段声母/单字兜底的 exact 上限：同音字全量召回（yi 141 等都放得下）。
const SINGLE_SYLLABLE_EXACT_LIMIT: usize = 300;
/// 多段简拼/混拼的词候选上限：放大到 300 会让 zg/dz 候选膨胀到 ~80 页，稀释单字页位。
const INITIALS_EXACT_LIMIT: usize = 120;

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
    punct_state: PunctState,

    input_raw: String,
    tokens: Vec<Token>,
    all_candidates: Vec<(Candidate, u32)>, // (Candidate, Score)
    page: usize,
    page_size: usize,

    last_committed: Option<String>,
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
            punct_state: PunctState::default(),
            input_raw: String::new(),
            tokens: Vec::new(),
            all_candidates: Vec::new(),
            page: 0,
            page_size: PAGE_SIZE,
            last_committed: None,
        }
    }

    pub fn set_fuzzy(&mut self, fuzzy: bool) {
        self.fuzzy = fuzzy;
    }

    pub fn set_traditional(&mut self, traditional: bool) {
        self.traditional = traditional;
    }

    pub fn set_shuangpin(&mut self, scheme: Option<ShuangpinScheme>) {
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

        // 1. 双拼解码
        let pinyin_query = if let Some(ref decoder) = self.shuangpin_decoder {
            let mut decoded = String::new();
            let mut chars = self.input_raw.chars().peekable();
            while let Some(c1) = chars.next() {
                if let Some(&c2) = chars.peek() {
                    let pair = format!("{}{}", c1, c2);
                    if let Some(syll) = decoder.decode(&pair) {
                        chars.next();
                        if !decoded.is_empty() {
                            decoded.push('\'');
                        }
                        decoded.push_str(syll);
                        continue;
                    }
                }
                decoded.push(c1);
            }
            decoded
        } else {
            self.input_raw.clone()
        };

        // 2. 音节切分
        let patterns = tokenize_pinyin(&pinyin_query);
        let best_pattern = patterns.first().cloned().unwrap_or_default();
        self.tokens = best_pattern.clone();

        // 3. 构建音节序列与 preedit 显示
        let mut preedit_parts: Vec<String> = Vec::new();
        for tok in &best_pattern {
            preedit_parts.push(tok.text.clone());
        }

        let preedit = preedit_parts.join("'");

        // 4. 召回与打分（分层召回，对齐 Go compose.go 的编排顺序）：
        // 层级是第一排序维度，词频打分只在同层内决定先后——这是与 Go 一致的
        // 关键语义（旧的「词频 × 5000/1000 加成」方案用打分模拟分层，跨层会被
        // 高词频挤压，正是 tail 形态命中率 32% vs 89% 的根源）。
        //
        // 调用顺序即语义，不能交换（见 Go compose.go 注释）：
        //   续接（D）→ 错拼（E）→ 组合提升（F）→ 常规 token（H）→ 单字收尾（I）
        //   → 纠错提升（K）→ 单段过滤（L）→ 排序（M）→ 纠错固定位（N）。
        let shape = analyze_shape(&self.input_raw, &patterns);
        let mut typo = TypoState::default();
        let mut pool = Pool::new();

        // 两条续接路径共用层级：存在干净解析时与 rankExact 同层竞争（词频 −
        // 补全折价决定）；否则常规候选全是碎片假词，续接必须领跑。
        let continuation_rank = if shape.all_exact_shape {
            RANK_EXACT
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
            let hits: Vec<(String, Vec<String>, u32, String)> = self
                .dict
                .query_continuation(&head, &last.text, 24)
                .into_iter()
                .map(|(idx, remain)| {
                    let e = &self.dict.entries[idx];
                    (e.word.clone(), e.syllables.clone(), e.freq, remain)
                })
                .collect();
            for (word, syllables_vec, freq, remain) in hits {
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
        for (head_syls, frag) in crate::syllable::partial_tail_splits(&self.input_raw.clone()) {
            let head: Vec<&str> = head_syls.iter().map(|s| s.as_str()).collect();
            let hits: Vec<(String, Vec<String>, u32, String)> = self
                .dict
                .query_continuation(&head, &frag, 24)
                .into_iter()
                .map(|(idx, remain)| {
                    let e = &self.dict.entries[idx];
                    (e.word.clone(), e.syllables.clone(), e.freq, remain)
                })
                .collect();
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

        // 4.2 错拼修正（Go addTypoCorrections）+ 组合候选提升（F）
        add_typo_corrections(
            &self.dict,
            self.user_dict.as_deref(),
            &self.input_raw,
            &shape,
            prev,
            traditional,
            &mut pool,
            &mut typo,
        );
        promote_typo_compositions(&mut pool, &typo);

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
                let has_fragment_in_pat = pattern_has_fragment(pat);
                let tokens: Vec<&str> = pat.iter().map(|t| t.text.as_str()).collect();

                let exact_limit = if has_fragment_in_pat {
                    INITIALS_EXACT_LIMIT
                } else if tokens.len() == 1 {
                    SINGLE_SYLLABLE_EXACT_LIMIT
                } else {
                    20
                };
                let single_seg_single_char =
                    tokens.len() == 1 && (pat[0].is_exact || pat[0].text.chars().count() == 1);

                // Go drop 段：回退上限 6 层；回退到单个完整音节且原输入 ≥4 段
                // 时放开同音字全量（对齐单音节口径，SelectPrefix 逐字挑选才选
                // 得到 top 之外的字），其余回退层 10。
                let backoff_floor = tokens.len().saturating_sub(6).max(1);
                // Go drop 段的提升特例：整串没有任何精确命中、去掉 1 段后剩余
                // 全部是完整音节、且被去掉的段里有声母碎片（nihaox → [ni,hao] +
                // 碎片 x）时，把「你好」这类回退命中提升到精确层置顶推出。
                let mut full_exact_len: usize = 0;
                for l in (backoff_floor..=tokens.len()).rev() {
                    let sub = &tokens[..l];
                    let is_full = l == tokens.len();
                    let (exacts, prefixes) = if is_full {
                        let (e, p) = self.dict.query_exact_and_prefix(sub, exact_limit, 20);
                        (e, p)
                    } else {
                        let first_exact = pat.first().map(|t| t.is_exact).unwrap_or(false);
                        let backoff_limit = if sub.len() == 1 && first_exact && tokens.len() >= 4 {
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
                        && pat[..l].iter().all(|t| t.is_exact)
                        && pat[l..].iter().any(|t| !t.is_exact);
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
                }
            }
        }

        // 4.4 模糊音召回（保持既有行为：首选音节的模糊变体，稍降权）
        let best_syll_slices: Vec<&str> = best_pattern.iter().map(|t| t.text.as_str()).collect();
        if self.fuzzy && !best_syll_slices.is_empty() {
            let first_syll = best_syll_slices[0];
            let fuzzy_vars = fuzzy_syllable_variants(first_syll);
            for var in fuzzy_vars {
                if var != first_syll {
                    let mut alt_sylls = best_syll_slices.clone();
                    alt_sylls[0] = &var;
                    let hits: Vec<(String, Vec<String>, u32)> = self
                        .dict
                        .query_prefix(&alt_sylls, 16)
                        .iter()
                        .map(|e| (e.word.clone(), e.syllables.clone(), e.freq))
                        .collect();
                    for (word, syllables_vec, freq) in hits {
                        let display_text = if traditional {
                            to_traditional(&word)
                        } else {
                            word.clone()
                        };
                        pool.add(
                            &word,
                            display_text,
                            syllables_vec.join("'"),
                            freq.saturating_sub(200) as i32, // 模糊候选稍降权
                            false,
                            RANK_EXACT,
                            false,
                        );
                    }
                }
            }
        }

        // 4.5 首段全量单字收尾（Go addSingleCharBackfill）：任何多段输入都在
        // 列表最末追加首段全量单字（rankSingleBackfill），让 SelectPrefix 能
        // 选到 top 之外的字；单段输入本身已是全量单字，不重复追加。
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
            }
        }

        // 4.6 纠错层级提升（Go promoteTypoCorrections）：必须在常规/单字收集
        // 完之后、单段过滤之前——它按 rank 反推「纯错拼来源」。
        promote_typo_corrections(&mut pool, &typo, &shape, &self.input_raw);

        // 4.7 单段输入只出单字（Go filterSingleSegment）
        filter_single_segment(&mut pool, &patterns);

        // 5. 排序（Go sortCandidates/lessCand 镜像；Pool 保插入序 + Rust
        // sort_by 稳定排序 = Go sort.SliceStable）：① 回退层后置；② 超段仲裁
        // （仅全拼输入，候选音节数多于输入段数的后置——用户候选按 maxInputSegs
        // 判，非用户按首选切分判；简拼/混拼 token 数不是音节数，不参与）；
        // ③ 用户加权候选优先；④ 层级；⑤ 得分降序、词长、字典序。
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
        // 纠错固定第 3 位（Go placeTypoFixedPosition）
        let sorted = place_typo_fixed_position(sorted);
        self.all_candidates = sorted.into_iter().map(|(c, s, _)| (c, s as u32)).collect();

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
    pub fn input(&mut self, text: &str, _half_width: bool) -> Result<Composition, String> {
        // 与 Go engine.Session.Input 同契约：壳是标点唯一真源，input 只收
        // 拼音字母（先整体小写，非 [a-z'] 一律静默丢弃，标点走 punct()）。
        // 例外：双拼模式 ';' 是韵母键（mspy 的 ing），必须放行参与成对解码。
        for c in text.to_lowercase().chars() {
            if c.is_ascii_lowercase() || c == '\'' || (c == ';' && self.shuangpin_decoder.is_some())
            {
                self.input_raw.push(c);
            }
        }

        Ok(self.compose())
    }

    /// 退格删除最后一个按键字符
    pub fn backspace(&mut self) -> Composition {
        self.input_raw.pop();
        self.compose()
    }

    /// 删除最后一个整词音节（按词退格）
    pub fn backspace_word(&mut self) -> Composition {
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
            return Err("候选词索引越界".to_string());
        }

        let cand = self.all_candidates[global_idx].0.clone();

        // 记录用户调频与共现
        if let Some(ref u) = self.user_dict {
            u.bump_word(&cand.text, None);
            if let Some(ref prev) = self.last_committed {
                u.bump_bigram(prev, &cand.text);
            }
        }

        self.last_committed = Some(cand.text.clone());
        self.input_raw.clear();
        self.compose();

        Ok(cand.text)
    }

    /// 渐进上屏：只消耗匹配的前缀音节，剩余部分继续组词
    pub fn select_prefix(&mut self, index: usize) -> Result<Composition, String> {
        let global_idx = self.page * self.page_size + index;
        if global_idx >= self.all_candidates.len() {
            return Err("候选词索引越界".to_string());
        }

        let cand = self.all_candidates[global_idx].0.clone();
        let syll_count = cand.syllables.split('\'').count();

        // 消耗前面 syll_count 个 token 对应的字符
        let mut consumed_chars = 0;
        for tok in self.tokens.iter().take(syll_count) {
            consumed_chars += tok.text.len();
        }

        if consumed_chars >= self.input_raw.len() {
            self.input_raw.clear();
        } else {
            self.input_raw = self.input_raw[consumed_chars..].to_string();
        }

        // 记录调频
        if let Some(ref u) = self.user_dict {
            u.bump_word(&cand.text, None);
            if let Some(ref prev) = self.last_committed {
                u.bump_bigram(prev, &cand.text);
            }
        }
        self.last_committed = Some(cand.text.clone());

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
        self.punct_state.reset();
        self.compose()
    }

    /// 仅重置当前组词，保留历史上下文
    pub fn reset_composition(&mut self) -> Composition {
        self.input_raw.clear();
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
        let syll_slices: Vec<&str> = sylls.iter().map(|s| s.as_str()).collect();
        let matched = self.dict.query_prefix(&syll_slices, 16);

        matched
            .into_iter()
            .map(|e| {
                let text = if self.traditional {
                    to_traditional(&e.word)
                } else {
                    e.word.clone()
                };
                Candidate {
                    text,
                    syllables: e.syllables.join("'"),
                    score: 0,
                    from_user: false,
                    typo_fix: false,
                }
            })
            .collect()
    }

    /// 联想词直接上屏
    pub fn associate_select(&mut self, word: &str, _sylls: &[String]) -> Result<String, String> {
        if let Some(ref u) = self.user_dict {
            u.bump_word(word, None);
            if let Some(ref prev) = self.last_committed {
                u.bump_bigram(prev, word);
            }
        }
        self.last_committed = Some(word.to_string());
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
            pinyin: "ni'hao".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string()],
            freq: 9000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "你".to_string(),
            pinyin: "ni".to_string(),
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
        assert_eq!(comp.input, "nihao");
        assert_eq!(comp.preedit, "ni'hao");
        assert!(comp.committed.is_empty(), "input 不得代行标点上屏");
        assert_eq!(comp.candidates[0].text, "你好");

        // 全部为非法字符：组词保持为空（新会话，input 累积语义与 Go 一致——
        // 非法字符不产生新组词，也不清掉既有组词）
        let mut sess2 = create_test_session();
        let comp2 = sess2.input("123", false).unwrap();
        assert!(comp2.input.is_empty());
        assert!(comp2.candidates.is_empty());
    }

    /// 纯音节切分优先于声母碎片：xian 是合法音节，不得混入 xia'n
    /// 这类碎片切分展开的候选（对齐 Go：碎片只在整段无音节解时兜底）。
    #[test]
    fn test_fragment_patterns_lose_to_pure_syllables() {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "先".to_string(),
            pinyin: "xian".to_string(),
            syllables: vec!["xian".to_string()],
            freq: 5000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "瞎闹".to_string(),
            pinyin: "xia'nao".to_string(),
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
            pinyin: "ni'hao".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string()],
            freq: 9000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "自治州".to_string(),
            pinyin: "zi'zhi'zhou".to_string(),
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

    /// 末段残缺续接（rankContinuation）：zhonggu 的末音节 gu 是 guo 的前缀，
    /// 中国（词频 85，补 o 后 55）应压过 中古（36）成为首选；daxu → 大学。
    #[test]
    fn test_continuation_beats_partial_word() {
        let mut dict = Dictionary::new();
        dict.insert(crate::dict::WordEntry {
            word: "中国".to_string(),
            pinyin: "zhong'guo".to_string(),
            syllables: vec!["zhong".to_string(), "guo".to_string()],
            freq: 85,
        });
        dict.insert(crate::dict::WordEntry {
            word: "中古".to_string(),
            pinyin: "zhong'gu".to_string(),
            syllables: vec!["zhong".to_string(), "gu".to_string()],
            freq: 36,
        });
        dict.insert(crate::dict::WordEntry {
            word: "大学".to_string(),
            pinyin: "da'xue".to_string(),
            syllables: vec!["da".to_string(), "xue".to_string()],
            freq: 80,
        });
        dict.sort_nodes();
        let mut sess = Session::new(Arc::new(dict), None);

        let comp = sess.input("zhonggu", false).unwrap();
        assert_eq!(
            comp.candidates[0].text, "中国",
            "续接应让 中国 压过 中古: {:?}",
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
            pinyin: "quan'mian".to_string(),
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
            pinyin: "xian".to_string(),
            syllables: vec!["xian".to_string()],
            freq: 5000,
        });
        dict.insert(crate::dict::WordEntry {
            word: "西安".to_string(),
            pinyin: "xi'an".to_string(),
            syllables: vec!["xi".to_string(), "an".to_string()],
            freq: 9000, // 词频更高也不得出现在 xian 单段输入
        });
        dict.insert(crate::dict::WordEntry {
            word: "中国".to_string(),
            pinyin: "zhong'guo".to_string(),
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
            pinyin: "ni'hao".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string()],
            freq: 100, // 词频低
        });
        dict.insert(crate::dict::WordEntry {
            word: "你好吗".to_string(),
            pinyin: "ni'hao'ma".to_string(),
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
}
