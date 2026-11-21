//! 错拼修正的「按已有词切分」分词 DP（对齐 Go typo_dp.go）。
//!
//! 用户输入长拼音时经常打错一两个字母（chagnshuru → changshuru、
//! tuzhogng → tuzhong）；正确拼写时也可能整串在词库里没有单条词条，但能切成
//! 已有词（changshuru → 长输入、tuzhong → 图中）。这里对音节序列做 DP，把
//! 整串切成「已存在的词」序列：
//!   - 错拼修正路径：作为纠错组合候选（typo_fix 标记，选中即整串消费）；
//!   - 正常输入路径：作为低层组合候选（rankComposition 层，本轮未接）。
//!
//! 硬边界：每一段都必须在词典里真实存在，不造词；整词本身存在的组合
//! （如「你好」= 你+好 恰好是词条）直接跳过，避免把整词候选加上分段、改变
//! 学习语义。

use crate::dict::Dictionary;
use crate::syllable::Token;

// ── DP 常量（Go typo_dp.go 逐值移植）────────────────────────────
/// 多字词每多一个音节的奖励：鼓励把长段当作一个已有的多字词，而不是拆成
/// 更碎的组合。刻意小于「一个普通词的词量级」——纠错组合候选只能与其他
/// 候选同层竞争，不能靠奖励虚高把正常候选挤掉。
const TYPO_DP_WORD_BONUS: i32 = 40;
/// 每多一个切分段的小惩罚：同等词频下分得越少越可信。
const TYPO_DP_SEGMENT_PENALTY: i32 = 25;
/// 单字分段的额外惩罚：单字拼接的置信度低于成词。
const TYPO_DP_SINGLE_PENALTY: i32 = 15;
/// 「末段是多字词」的奖励：中文词组通常是右重心的。
const TYPO_DP_TAIL_WORD_BONUS: i32 = 40;
/// 对应地惩罚以单字收尾的切分。
const TYPO_DP_TAIL_SINGLE_PENALTY: i32 = 20;
/// 限制参与 DP 的音节数：错拼场景输入不会太长，超长串组合数爆炸且价值低。
const TYPO_DP_MAX_SYLLABLES: usize = 12;
/// 每个位置保留的最优路径数（按累计得分截断）。
const TYPO_DP_MAX_STATES: usize = 24;
/// 一个词最多占用的音节数。
const TYPO_DP_MAX_SPAN: usize = 6;

/// DP 中的一个「词段」：词典里精确存在的一条词。
#[derive(Debug, Clone)]
pub struct PhraseSpan {
    pub word: String,
    pub syllables: Vec<String>,
    pub freq: i32,
}

/// DP 的部分切分结果。
#[derive(Debug, Clone, Default)]
struct PhrasePath {
    words: Vec<String>,
    syllables: Vec<String>,
    score: i32,
}

/// 把完整音节序列切成已有词序列，返回组合候选（Go composedPhraseEntries）。
/// tokens 必须是纯完整音节；任一段不是完整音节时返回空（不造词）。
pub fn composed_phrase_entries(
    dict: &Dictionary,
    tokens: &[Token],
    max_results: usize,
) -> Vec<PhraseSpan> {
    let n = tokens.len();
    if n < 2 || n > TYPO_DP_MAX_SYLLABLES {
        return Vec::new();
    }
    if tokens.iter().any(|t| !t.is_exact) {
        return Vec::new();
    }
    let max_results = if max_results == 0 { 4 } else { max_results };
    let syll_text: Vec<String> = tokens.iter().map(|t| t.text.clone()).collect();

    // span(i, j) 缓存：把 syll_text[i..j] 当作一个词查询
    let mut span_cache: std::collections::HashMap<(usize, usize), Vec<PhraseSpan>> =
        std::collections::HashMap::new();
    let mut span = |i: usize, j: usize| -> Vec<PhraseSpan> {
        if let Some(got) = span_cache.get(&(i, j)) {
            return got.clone();
        }
        let limit = if j - i == 1 { 8 } else { 4 };
        let mut out: Vec<PhraseSpan> = Vec::new();
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        let sylls: Vec<&str> = syll_text[i..j].iter().map(|s| s.as_str()).collect();
        for e in dict.exact_words(&sylls) {
            if e.word.is_empty() || !seen.insert(e.word.clone()) {
                continue;
            }
            out.push(PhraseSpan {
                word: e.word.clone(),
                syllables: e.syllables.clone(),
                freq: e.freq as i32,
            });
        }
        // 词频降序、同频字典序（Go 同款稳定排序口径）
        out.sort_by(|a, b| b.freq.cmp(&a.freq).then(a.word.cmp(&b.word)));
        out.truncate(limit);
        span_cache.insert((i, j), out.clone());
        out
    };

    // states[j] = 到达位置 j 的若干最优路径
    let mut states: Vec<Vec<PhrasePath>> = vec![Vec::new(); n + 1];
    states[0] = vec![PhrasePath::default()];
    for i in 0..n {
        if states[i].is_empty() {
            continue;
        }
        let max_j = (i + TYPO_DP_MAX_SPAN).min(n);
        for j in (i + 1)..=max_j {
            let spans = span(i, j);
            if spans.is_empty() {
                continue;
            }
            let base_states = states[i].clone();
            for st in &base_states {
                for sp in &spans {
                    let mut np = PhrasePath {
                        words: st.words.clone(),
                        syllables: st.syllables.clone(),
                        score: st.score
                            + sp.freq
                            + TYPO_DP_WORD_BONUS * (sp.syllables.len() as i32 - 1),
                    };
                    np.words.push(sp.word.clone());
                    np.syllables.extend(sp.syllables.iter().cloned());
                    if sp.syllables.len() == 1 {
                        np.score -= TYPO_DP_SINGLE_PENALTY;
                    }
                    if !st.words.is_empty() {
                        np.score -= TYPO_DP_SEGMENT_PENALTY;
                    }
                    if j == n {
                        if sp.syllables.len() > 1 {
                            np.score += TYPO_DP_TAIL_WORD_BONUS;
                        } else {
                            np.score -= TYPO_DP_TAIL_SINGLE_PENALTY;
                        }
                    }
                    states[j].push(np);
                }
            }
            if states[j].len() > TYPO_DP_MAX_STATES * 4 {
                states[j].sort_by(|a, b| b.score.cmp(&a.score));
                states[j].truncate(TYPO_DP_MAX_STATES);
            }
        }
    }

    let mut filtered: Vec<PhrasePath> = Vec::new();
    let mut seen_text: std::collections::HashSet<String> = std::collections::HashSet::new();
    for p in states[n].iter() {
        if p.words.len() < 2 {
            continue; // 单段 = 整词，常规 typo 整词查询已覆盖
        }
        let word = p.words.join("");
        if !seen_text.insert(word.clone()) {
            continue;
        }
        // 整词本身在词库里存在时跳过：常规 typo 查询会以整词候选给出，
        // 绝不能在这里给它补分段（会改变上屏学习语义）。
        let syll_refs: Vec<&str> = p.syllables.iter().map(|s| s.as_str()).collect();
        if dict.contains_word(&word, &syll_refs) {
            continue;
        }
        filtered.push(p.clone());
    }
    filtered.sort_by(|a, b| {
        b.score
            .cmp(&a.score)
            .then(a.words.len().cmp(&b.words.len()))
            .then(a.words.join("").cmp(&b.words.join("")))
    });
    filtered.truncate(max_results);
    filtered
        .into_iter()
        .map(|p| PhraseSpan {
            word: p.words.join(""),
            syllables: p.syllables.clone(),
            freq: p.score,
        })
        .collect()
}
