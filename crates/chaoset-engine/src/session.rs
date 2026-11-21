//! 输入法会话状态机：按键组词、候选词排序、分页、渐进选择与联想。

use std::sync::Arc;

use crate::dict::Dictionary;
use crate::fuzzy::fuzzy_syllable_variants;
use crate::punct::PunctState;
use crate::s2t::to_traditional;
use crate::shuangpin::{ShuangpinDecoder, ShuangpinScheme};
use crate::syllable::{tokenize_chunk, tokenize_pinyin, typo_corrections, Token};
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

        // 4. 召回与打分（分层召回，对齐 Go compose_query.go / compose_rank.go）：
        // 层级是第一排序维度，词频打分只在同层内决定先后——这是与 Go 一致的
        // 关键语义（旧的「词频 × 5000/1000 加成」方案用打分模拟分层，跨层会被
        // 高词频挤压，正是 tail 形态命中率 32% vs 89% 的根源）。
        //
        // 候选池 key = 词 + \u0000 + 音节串；同词同音节保留更优层级、其次更高得分。
        let mut pool: std::collections::HashMap<String, (Candidate, i32, i32)> =
            std::collections::HashMap::new();

        // —— 形态分析（Go analyzeShape 的排序/续接子集）——
        let min_pattern_len = patterns.iter().map(|p| p.len()).min().unwrap_or(0);
        let mut all_exact_shape = true;
        for pat in patterns.iter().filter(|p| p.len() == min_pattern_len) {
            if pat.iter().any(|t| !t.is_exact) {
                all_exact_shape = false;
                break;
            }
        }
        // 两条续接路径共用层级：存在干净解析时与 rankExact 同层竞争（词频 −
        // 补全折价决定）；否则常规候选全是碎片假词，续接必须领跑。
        let continuation_rank = if all_exact_shape {
            RANK_EXACT
        } else {
            RANK_CONTINUATION_LEAD
        };
        let input_segs = patterns.first().map(|p| p.len()).unwrap_or(0) as i32;
        let max_input_segs = patterns.iter().map(|p| p.len()).max().unwrap_or(0) as i32;
        let input_has_initials = patterns
            .first()
            .map(|p| p.iter().any(|t| !t.is_exact))
            .unwrap_or(false);

        // 插入候选：同词同音节保留更优层级，其次更高得分
        fn push_entry(
            pool: &mut std::collections::HashMap<String, (Candidate, i32, i32)>,
            raw_word: &str,
            display_text: String,
            syllables: String,
            score: i32,
            from_user: bool,
            rank: i32,
        ) {
            let key = format!("{}\u{0}{}", raw_word, syllables);
            let cand = Candidate {
                text: display_text,
                syllables,
                score: 0,
                from_user,
            };
            match pool.get_mut(&key) {
                Some(slot) => {
                    if rank < slot.2 || (rank == slot.2 && score > slot.1) {
                        *slot = (cand, score, rank);
                    }
                }
                None => {
                    pool.insert(key, (cand, score, rank));
                }
            }
        }

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
            for (idx, remain) in self.dict.query_continuation(&head, &last.text, 24) {
                let e = &self.dict.entries[idx];
                let (boost, from_user) = if let Some(ref u) = self.user_dict {
                    let (b, f) = u.score(&e.word, self.last_committed.as_deref());
                    (b as i32, f)
                } else {
                    (0, false)
                };
                // 补全越长越不确定（gu→guo 远强于 gu→guang），按字符数折价
                let score = e.freq as i32 + boost - CONTINUATION_PENALTY * remain.len() as i32;
                let display_text = if self.traditional {
                    to_traditional(&e.word)
                } else {
                    e.word.clone()
                };
                push_entry(
                    &mut pool,
                    &e.word,
                    display_text,
                    e.syllables.join("'"),
                    score,
                    from_user,
                    continuation_rank,
                );
            }
        }
        for (head_syls, frag) in crate::syllable::partial_tail_splits(&self.input_raw.clone()) {
            let head: Vec<&str> = head_syls.iter().map(|s| s.as_str()).collect();
            for (idx, remain) in self.dict.query_continuation(&head, &frag, 24) {
                let e = &self.dict.entries[idx];
                let (boost, from_user) = if let Some(ref u) = self.user_dict {
                    let (b, f) = u.score(&e.word, self.last_committed.as_deref());
                    (b as i32, f)
                } else {
                    (0, false)
                };
                let score = e.freq as i32 + boost - CONTINUATION_PENALTY * remain.len() as i32;
                let display_text = if self.traditional {
                    to_traditional(&e.word)
                } else {
                    e.word.clone()
                };
                push_entry(
                    &mut pool,
                    &e.word,
                    display_text,
                    e.syllables.join("'"),
                    score,
                    from_user,
                    continuation_rank,
                );
            }
        }

        // 4.2 常规 token 匹配（Go addTokenMatches）：纯音节 pattern 优先，
        // 全部空手而归才回退含碎片的 pattern（简拼兜底）。上限口径（取值依据
        // 见 Go compose_query.go addTokenMatches 注释的 2026-10-28 实测）：
        //   - 单音节全拼/单段声母 exact=300：同音字全量（yi 141 / ji 130）；
        //   - 多段简拼/混拼 exact=120：放大到 300 会让候选膨胀 ~80 页；
        //   - 常规多音节 20/20（多条歧义切分 × 条数是乘法放大）；
        //   - prefix 一律 20：低频衍生词组洪水会稀释单字页位；
        //   - 单段输入只出单字：单音节全拼与单字母声母不进前缀扩展词
        //    （diao 的「调用」、xian 的「西安」都不出）；zh/ch/sh 双字母
        //     简拼仍出词候选。
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

                    for e in &exacts {
                        let (boost, from_user) = if let Some(ref u) = self.user_dict {
                            let (b, f) = u.score(&e.word, self.last_committed.as_deref());
                            (b as i32, f)
                        } else {
                            (0, false)
                        };
                        let rank = if is_full { rank_exact } else { effective_rank };
                        let display_text = if self.traditional {
                            to_traditional(&e.word)
                        } else {
                            e.word.clone()
                        };
                        push_entry(
                            &mut pool,
                            &e.word,
                            display_text,
                            e.syllables.join("'"),
                            e.freq as i32 + boost,
                            from_user,
                            rank,
                        );
                    }
                    // 单段输入只出单字：前缀扩展词不进池（exact 池天然只有单字）
                    for e in &prefixes {
                        if single_seg_single_char {
                            continue;
                        }
                        let (boost, from_user) = if let Some(ref u) = self.user_dict {
                            let (b, f) = u.score(&e.word, self.last_committed.as_deref());
                            (b as i32, f)
                        } else {
                            (0, false)
                        };
                        let display_text = if self.traditional {
                            to_traditional(&e.word)
                        } else {
                            e.word.clone()
                        };
                        push_entry(
                            &mut pool,
                            &e.word,
                            display_text,
                            e.syllables.join("'"),
                            e.freq as i32 + boost,
                            from_user,
                            rank_prefix,
                        );
                    }
                }
            }
        }

        // 4.3 模糊音召回（保持既有行为：首选音节的模糊变体，稍降权）
        let best_syll_slices: Vec<&str> = best_pattern.iter().map(|t| t.text.as_str()).collect();
        if self.fuzzy && !best_syll_slices.is_empty() {
            let first_syll = best_syll_slices[0];
            let fuzzy_vars = fuzzy_syllable_variants(first_syll);
            for var in fuzzy_vars {
                if var != first_syll {
                    let mut alt_sylls = best_syll_slices.clone();
                    alt_sylls[0] = &var;
                    for entry in self.dict.query_prefix(&alt_sylls, 16) {
                        let display_text = if self.traditional {
                            to_traditional(&entry.word)
                        } else {
                            entry.word.clone()
                        };
                        push_entry(
                            &mut pool,
                            &entry.word,
                            display_text,
                            entry.syllables.join("'"),
                            entry.freq.saturating_sub(200) as i32, // 模糊候选稍降权
                            false,
                            RANK_EXACT,
                        );
                    }
                }
            }
        }

        // 4.4 纠错召回（保持既有行为：无精确候选时按单次编辑纠错查询）
        if pool.is_empty() {
            let typos = typo_corrections(&self.input_raw, 3);
            for typo in typos {
                let typo_tokens = tokenize_chunk(&typo.text);
                if let Some(first_pat) = typo_tokens.first() {
                    let t_sylls: Vec<&str> = first_pat.iter().map(|t| t.text.as_str()).collect();
                    for entry in self.dict.query_prefix(&t_sylls, 8) {
                        let display_text = if self.traditional {
                            to_traditional(&entry.word)
                        } else {
                            entry.word.clone()
                        };
                        push_entry(
                            &mut pool,
                            &entry.word,
                            display_text,
                            entry.syllables.join("'"),
                            entry.freq.saturating_sub(500) as i32,
                            false,
                            RANK_EXACT,
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
                for entry in self.dict.query_exact(&first, SINGLE_SYLLABLE_EXACT_LIMIT) {
                    if entry.word.chars().count() != 1 {
                        continue;
                    }
                    let display_text = if self.traditional {
                        to_traditional(&entry.word)
                    } else {
                        entry.word.clone()
                    };
                    push_entry(
                        &mut pool,
                        &entry.word,
                        display_text,
                        entry.syllables.join("'"),
                        entry.freq as i32,
                        false,
                        RANK_SINGLE_BACKFILL,
                    );
                }
            }
        }

        // 单段输入只出单字（Go 硬约束，TestSingleSegmentNoWords 契约）：无
        // 撇号且最短切分为 1 段、且该段是完整音节或单字母声母的输入，全部
        // 候选只允许单字——任何切分路径（xian 的 [xi,an]）泄漏进来的词语候
        // 选一律过滤；zh/ch/sh 双字母简拼仍出词候选；显式分段 xi'an 不受限。
        let single_segment_input = !self.input_raw.contains('\'')
            && patterns
                .first()
                .map(|p| p.len() == 1 && (p[0].is_exact || p[0].text.chars().count() == 1))
                .unwrap_or(false);
        if single_segment_input {
            pool.retain(|_, (c, _, _)| c.text.chars().count() == 1);
        }

        // 5. 排序（Go sortCandidates/lessCand 镜像；Rust sort_by 是稳定排序，
        // 对应 Go sort.SliceStable）：① 回退层后置；② 超段仲裁（仅全拼输入，
        // 候选音节数多于输入段数的后置——用户候选按 maxInputSegs 判，非用户
        // 按首选切分判；简拼/混拼 token 数不是音节数，不参与）；③ 用户加权
        // 候选优先；④ 层级；⑤ 得分降序、词长、字典序。
        let mut sorted: Vec<(Candidate, i32, i32)> = pool.into_values().collect();
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
}
