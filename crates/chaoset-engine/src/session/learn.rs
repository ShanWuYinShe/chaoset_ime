//! 上屏学习（Go sequence_learn.go 与 learnSelection/learnPhrase 的 Rust 移植）：
//! 调频、共现、上屏历史与词组进化、渐进选段拼出的整句固化、学习记录的分段推断
//! 与整句式记录的安全约束。从 session.rs 按职责拆出；只依赖 Session 的字段与
//! 用户词典/主词典的查询接口。
//!
//! 候选硬约束（见 AGENTS.md「候选质量」）：整句式学习记录必须带分段，只在**完整
//! 输入**时作为整句候选、只在用户确认过的**分段边界**剥前缀；部分输入不得出现整词
//! 候选（打「今」绝不得给出「天星期一」）。

use super::*;

/// 词组进化：相邻两词共现达到该次数后固化为用户词组（Go learnThreshold）。
const LEARN_THRESHOLD: i32 = 2;
/// 可固化词组的最大音节数，避免学出无意义长串（Go maxLearnSylls）。
const MAX_LEARN_SYLLS: usize = 4;
/// 参与词组进化观察的上屏历史长度（Go maxHistory）。
const MAX_HISTORY: usize = 8;
/// 「整句式输入习惯」可学习的最长音节数（Go maxSentenceSylls）：超过它的输入是连打/
/// 误触而非习惯词，不记。
const MAX_SENTENCE_SYLLS: usize = 12;

/// 按分段词的字数切分音节序列（「词长 == 音节数」，Go splitSyllsBySegs）。
/// 任一分段字数为 0 或总字数对不上时返回 None，调用方退化为普通词学习。
fn split_sylls_by_segs<'a>(sylls: &'a [String], segs: &[String]) -> Option<Vec<&'a [String]>> {
    if segs.len() < 2 || sylls.is_empty() {
        return None;
    }
    let mut out = Vec::with_capacity(segs.len());
    let mut pos = 0usize;
    for seg in segs {
        let n = seg.chars().count();
        if n == 0 || pos + n > sylls.len() {
            return None;
        }
        out.push(&sylls[pos..pos + n]);
        pos += n;
    }
    if pos != sylls.len() {
        return None;
    }
    Some(out)
}

impl Session {
    /// 候选的学习分段：用户学习记录自带的 Segs 优先，旧数据/普通词用主词典
    /// 推断（推断不出为空 = 普通词学习）。Rust 的 Candidate 不带 Segs 字段，
    /// 这里按词回查用户词典（Go 由候选直接携带 cand.Segments）。
    pub(super) fn candidate_segs(&self, cand: &Candidate, sylls: &[String]) -> Vec<String> {
        let rec_segs = self
            .user_dict
            .as_ref()
            .map(|u| u.word_segs(&cand.text))
            .unwrap_or_default();
        self.entry_segs(&cand.text, sylls, &rec_segs)
    }

    /// 记录一次上屏的学习数据：词频、与上一上屏词的 Bigram、history 与词组进化
    /// （Go learnSelection）。`select` / `select_prefix` / `associate_select` 共用。
    ///
    /// `segs` 是「整句式上屏」时用户确认过的分段词文本（逐段渐进拼出的整串，或
    /// 一次选中的整句候选自带的分段），普通单词为空。整句式记录带分段：完整输入
    /// 时作为整句候选参与调频加权；部分输入不给整词候选、联想也只在分段边界剥
    /// 前缀（见 `is_sentence_entry`）。整句一键上屏按「用户逐段确认」的等价路径
    /// 再学一遍分段词与相邻共现，两条路径的学习不分叉。
    pub(super) fn learn_selection(&mut self, text: &str, sylls: &[String], segs: &[String]) {
        let sentence = self.is_sentence_selection(text, sylls, segs);
        let prev = self.last_committed.clone();
        if let Some(ref u) = self.user_dict {
            if sentence {
                u.bump_word(text, Some(&sylls.join("'")), Some(segs.to_vec()));
            } else {
                u.bump_word(text, Some(&sylls.join("'")), None);
                if let Some(ref p) = prev {
                    u.bump_bigram(p, text);
                }
            }
        }
        if sentence {
            let parts = split_sylls_by_segs(sylls, segs).unwrap_or_default();
            let mut prev_word = prev;
            for (i, seg) in segs.iter().enumerate() {
                let seg_sylls = parts.get(i).copied().unwrap_or(&[]);
                if let Some(ref u) = self.user_dict {
                    if !seg.is_empty() && !seg_sylls.is_empty() {
                        u.bump_word(seg, Some(&seg_sylls.join("'")), None);
                        if let Some(ref p) = prev_word {
                            u.bump_bigram(p, seg);
                        }
                    }
                }
                self.push_history(seg, seg_sylls);
                self.learn_phrase();
                prev_word = Some(seg.clone());
            }
        } else {
            self.push_history(text, sylls);
            self.learn_phrase();
        }
        self.last_committed = Some(text.to_string());
        self.prev_sylls = sylls.to_vec();
    }

    fn push_history(&mut self, word: &str, sylls: &[String]) {
        self.history
            .push((word.to_string(), sylls.to_vec()));
        if self.history.len() > MAX_HISTORY {
            let drop = self.history.len() - MAX_HISTORY;
            self.history.drain(..drop);
        }
    }

    /// 判断一次上屏是否属于「整句式」（Go isSentenceSelection）：候选自带 ≥2 个
    /// 分段、分段与音节序列严格对齐、整词不是词典里的普通词条、音节数不超过
    /// MAX_SENTENCE_SYLLS。
    fn is_sentence_selection(&self, text: &str, sylls: &[String], segs: &[String]) -> bool {
        if segs.len() < 2 || sylls.len() < 2 || sylls.len() > MAX_SENTENCE_SYLLS {
            return false;
        }
        if split_sylls_by_segs(sylls, segs).is_none() || segs.concat() != text {
            return false;
        }
        let syl_refs: Vec<&str> = sylls.iter().map(|s| s.as_str()).collect();
        !self.dict.contains_word(text, &syl_refs)
    }

    /// 观察「最后两次上屏」（Go learnPhrase）：相邻共现达到 LEARN_THRESHOLD 且主
    /// 词典还没有这个组合时，固化为用户词组。链式扩展（A+B→AB 之后 AB+又一词）
    /// 由后续真实上屏自然发生。
    fn learn_phrase(&self) {
        let Some(u) = self.user_dict.as_ref() else {
            return;
        };
        if self.history.len() < 2 {
            return;
        }
        let n = self.history.len();
        let (a, b) = (&self.history[n - 2], &self.history[n - 1]);
        let mut sylls = a.1.clone();
        sylls.extend(b.1.iter().cloned());
        if sylls.len() > MAX_LEARN_SYLLS {
            return;
        }
        let word = format!("{}{}", a.0, b.0);
        let syl_refs: Vec<&str> = sylls.iter().map(|s| s.as_str()).collect();
        if self.dict.contains_word(&word, &syl_refs) {
            return;
        }
        if u.pair_count(&a.0, &b.0) < LEARN_THRESHOLD {
            return;
        }
        // 记录分段（进化时的前后两个上屏词）：联想剥前缀只允许在这些边界发生。
        u.bump_phrase(
            &word,
            &sylls.join("'"),
            Some(vec![a.0.clone(), b.0.clone()]),
        );
    }

    /// 「本次组词全部由渐进上屏段拼完」时，把整串记成带分段的整句习惯（Go
    /// learnProgressiveSequence）：下一轮完整输入同一串时整句候选被用户调频
    /// 加权。只记整句习惯、不记可被部分输入命中的「整词」候选；分段与音节对
    /// 不上、太长或本身是词典词时不记（宁缺勿错）。
    pub(super) fn learn_progressive_sequence(&self, segs: &[(String, Vec<String>)]) {
        let Some(u) = self.user_dict.as_ref() else {
            return;
        };
        if segs.len() < 2 {
            return;
        }
        let mut word = String::new();
        let mut sylls: Vec<String> = Vec::new();
        let mut seg_words: Vec<String> = Vec::with_capacity(segs.len());
        for (w, s) in segs {
            if w.is_empty() || w.chars().count() != s.len() {
                return;
            }
            word.push_str(w);
            sylls.extend(s.iter().cloned());
            seg_words.push(w.clone());
        }
        if sylls.len() > MAX_SENTENCE_SYLLS {
            return;
        }
        let syl_refs: Vec<&str> = sylls.iter().map(|s| s.as_str()).collect();
        if self.dict.contains_word(&word, &syl_refs) {
            return;
        }
        u.bump_word(&word, Some(&sylls.join("'")), Some(seg_words));
    }

    /// 推断学习词条的分段（Go inferSegs）：整句记录没有 Segs 时的兼容路径。
    /// 要求逐字对应音节、**每个分段都是词典词**、且能切成 ≥2 段；切不出或
    /// 本身已是词典词（普通词）时返回空（宁可当普通词，也不臆造边界）。
    pub(super) fn infer_segs(&self, word: &str, syllables: &[String]) -> Vec<String> {
        if syllables.is_empty() {
            return Vec::new();
        }
        let runes: Vec<char> = word.chars().collect();
        if runes.len() != syllables.len() {
            return Vec::new();
        }
        let syl_refs: Vec<&str> = syllables.iter().map(|s| s.as_str()).collect();
        if self.dict.contains_word(word, &syl_refs) {
            return Vec::new();
        }
        let n = runes.len();
        let mut best = vec![i32::MAX; n + 1];
        let mut prev = vec![0usize; n + 1];
        best[0] = 0;
        for pos in 0..n {
            if best[pos] == i32::MAX {
                continue;
            }
            for k in 1..=(n - pos) {
                let seg: String = runes[pos..pos + k].iter().collect();
                if !self.dict.contains_word(&seg, &syl_refs[pos..pos + k]) {
                    continue;
                }
                if best[pos] + 1 < best[pos + k] {
                    best[pos + k] = best[pos] + 1;
                    prev[pos + k] = pos;
                }
            }
        }
        if best[n] < 2 {
            return Vec::new();
        }
        let mut segs: Vec<String> = Vec::new();
        let mut pos = n;
        while pos > 0 {
            let p = prev[pos];
            segs.push(runes[p..pos].iter().collect());
            pos = p;
        }
        segs.reverse();
        segs
    }

    /// 学习词条的分段（Go entrySegs）：优先取记录里的 Segs（新数据），
    /// 旧数据没有 Segs 时用词典最长词切分推断，推断不出则视为普通词。
    pub(super) fn entry_segs(&self, word: &str, syllables: &[String], segs: &[String]) -> Vec<String> {
        if segs.len() >= 2 {
            return segs.to_vec();
        }
        self.infer_segs(word, syllables)
    }

    /// 整句式学习记录判定（Go isSentenceEntry）：新数据看 Segs（≥2 段即整句），
    /// 旧数据看「>4 音节且不在词典」或能推断出 ≥2 段。整句式记录**不参与部分
    /// 输入的前缀候选**——否则打「今」就能看到「今天星期一」，正是用户反对的
    /// 整词贪婪匹配（项目候选硬约束）。
    pub(super) fn is_sentence_entry(&self, word: &str, syllables: &[String], segs: &[String]) -> bool {
        if syllables.len() < 2 {
            return false;
        }
        if segs.len() >= 2 {
            return true;
        }
        let syl_refs: Vec<&str> = syllables.iter().map(|s| s.as_str()).collect();
        if self.dict.contains_word(word, &syl_refs) {
            return false;
        }
        if syllables.len() > 4 {
            return true; // 旧数据：>4 音节且不在词典，只可能是整句
        }
        self.infer_segs(word, syllables).len() >= 2
    }

    /// 在整句式学习记录的分段边界上剥掉已上屏前缀（Go stripAtSegmentBoundary）：
    /// prev_word 必须恰好等于前 k 段（k≥1 且 k<总段数）的拼接且音节数一致；
    /// 不在边界上返回 None——调用方不应提供该「尾巴」候选。
    pub(super) fn strip_at_segment_boundary(
        &self,
        word: &str,
        syllables: &[String],
        segs: &[String],
        prev_word: &str,
        prev_sylls: &[String],
    ) -> Option<(String, String)> {
        if prev_word.is_empty() || prev_sylls.is_empty() || segs.len() < 2 {
            return None;
        }
        let word_runes: Vec<char> = word.chars().collect();
        let total_syls = syllables.len();
        let mut rune_count = 0usize;
        let mut syl_count = 0usize;
        for (k, seg) in segs.iter().enumerate() {
            let n = seg.chars().count();
            rune_count += n;
            syl_count += n;
            if rune_count > word_runes.len() || syl_count > total_syls {
                return None;
            }
            let prefix: String = word_runes[..rune_count].iter().collect();
            if prefix == prev_word && syl_count == prev_sylls.len() {
                if rune_count >= word_runes.len() || syl_count >= total_syls {
                    return None; // 恰好等于整词：没有续接部分
                }
                let text: String = word_runes[rune_count..].iter().collect();
                return Some((text, syllables[syl_count..].join("'")));
            }
            if k == segs.len() - 1 {
                break;
            }
        }
        None
    }

}


#[cfg(test)]
mod tests {
    use super::*;

    fn common_dict() -> Arc<Dictionary> {
        let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../../data/dict_common.tsv");
        let mut dict = Dictionary::new();
        dict.load_tsv(path)
            .unwrap_or_else(|e| panic!("加载内置词库失败 {path}: {e}"));
        Arc::new(dict)
    }

    /// 全部候选（翻到末页），返回 (文本, 音节, 是否用户词)。
    fn all_candidates(sess: &mut Session, input: &str) -> Vec<(String, String, bool)> {
        let mut comp = sess.input(input, false).unwrap();
        let mut all: Vec<(String, String, bool)> = comp
            .candidates
            .iter()
            .map(|c| (c.text.clone(), c.syllables.clone(), c.from_user))
            .collect();
        while ((comp.page + 1) as usize) < comp.page_count as usize {
            comp = sess.next_page();
            all.extend(
                comp.candidates
                    .iter()
                    .map(|c| (c.text.clone(), c.syllables.clone(), c.from_user)),
            );
        }
        all
    }

    fn fresh(user: Option<Arc<UserDict>>) -> Session {
        Session::new(common_dict(), user)
    }

    fn pick(comp: &Composition, text: &str, syllables: &str) -> usize {
        comp.candidates
            .iter()
            .position(|c| c.text == text && c.syllables == syllables)
            .unwrap_or_else(|| panic!("首页应有 {text}[{syllables}]: {:?}", comp.candidates))
    }

    /// 正向：渐进选段「你好」+「世界」拼完 nihaoshijie → 整句固化为带分段的用户词，
    /// 再完整输入时置顶且带学习标记（修复：Rust 移植丢失 seqSegs 学习，用户拼过的
    /// 词组从不进词库）。
    /// 反向：部分输入 nihao 不得出现整句尾巴「你好世界」，且「你好」首选不变。
    #[test]
    fn progressive_selection_learns_sentence_and_ranks_first() {
        let user = Arc::new(UserDict::new::<&str>(None));
        let mut sess = fresh(Some(Arc::clone(&user)));
        for key in "nihaoshijie".chars() {
            sess.input(&key.to_string(), false).unwrap();
        }
        let comp = sess.compose();
        let rest = sess.select_prefix(pick(&comp, "你好", "ni'hao")).unwrap();
        assert_eq!(rest.input, "shijie");
        let rest = sess
            .select_prefix(pick(&rest, "世界", "shi'jie"))
            .unwrap();
        assert!(rest.input.is_empty(), "整串应由两段拼完");
        assert_eq!(
            user.word_segs("你好世界"),
            vec!["你好".to_string(), "世界".to_string()],
            "整句习惯必须带分段写入用户词典"
        );

        let mut again = fresh(Some(Arc::clone(&user)));
        let comp = again.input("nihaoshijie", false).unwrap();
        assert_eq!(comp.candidates[0].text, "你好世界", "学过的整句应置顶");
        assert!(comp.candidates[0].from_user);

        let mut part = fresh(Some(Arc::clone(&user)));
        let comp = part.input("nihao", false).unwrap();
        assert_eq!(comp.candidates[0].text, "你好", "部分输入首选不受影响");
        let all = all_candidates(&mut part, "nihao");
        assert!(
            !all.iter().any(|(t, ..)| t == "你好世界"),
            "部分输入不得出现整句候选: {all:?}"
        );
    }

    /// 显式音节分隔符：渐进上屏按音节消费输入，不能从字母中间截断剩余串，
    /// 也不能因此丢掉整句学习（`ni'hao'shi'jie` 与 `nihaoshijie` 等价）。
    #[test]
    fn progressive_selection_with_separators_learns_sentence() {
        let user = Arc::new(UserDict::new::<&str>(None));
        let mut sess = fresh(Some(Arc::clone(&user)));
        for key in "ni'hao'shi'jie".chars() {
            sess.input(&key.to_string(), false).unwrap();
        }
        let comp = sess.compose();
        let rest = sess.select_prefix(pick(&comp, "你好", "ni'hao")).unwrap();
        assert_eq!(rest.input, "shi'jie", "剩余输入不得从音节中间截断");
        let rest = sess.select_prefix(pick(&rest, "世界", "shi'jie")).unwrap();
        assert!(rest.input.is_empty(), "整串应已消费完");
        assert_eq!(
            user.word_segs("你好世界"),
            vec!["你好".to_string(), "世界".to_string()],
            "带分隔符的渐进拼句同样要固化整句"
        );
    }

    /// 省写韵母（niou→niu）与纯错拼修正（lianxiagn→联想）都要按整串消费：
    /// 前者不能按 token 规范拼写的字母数切原输入，后者残留的字母不是待续拼音。
    #[test]
    fn select_prefix_consumes_aliases_and_typo_fixes() {
        let mut sess = fresh(None);
        let comp = sess.input("niou", false).unwrap();
        let rest = sess.select_prefix(pick(&comp, "牛", "niu")).unwrap();
        assert!(rest.input.is_empty(), "niou 选「牛」应消费整串: {rest:?}");

        let mut sess = fresh(None);
        let comp = sess.input("lianxiagn", false).unwrap();
        let rest = sess
            .select_prefix(pick(&comp, "联想", "lian'xiang"))
            .unwrap();
        assert!(
            rest.input.is_empty(),
            "错拼修正候选应消费整串输入: {rest:?}"
        );
    }

    /// 正向：相邻两次上屏「今天」「天气」共现 2 次 → 固化为用户词组「今天天气」（带分段），
    /// 下次完整输入 jintiantianqi 置顶。
    /// 反向：只共现 1 次不固化（阈值 LEARN_THRESHOLD=2）；词典已有的词不重复学。
    #[test]
    fn adjacent_commits_evolve_into_user_phrase() {
        let user = Arc::new(UserDict::new::<&str>(None));
        let mut sess = fresh(Some(Arc::clone(&user)));
        let round = |sess: &mut Session| {
            sess.input("jintian", false).unwrap();
            assert_eq!(sess.select(0).unwrap(), "今天");
            sess.input("tianqi", false).unwrap();
            assert_eq!(sess.select(0).unwrap(), "天气");
        };
        round(&mut sess);
        assert_eq!(user.stats().phrases, 0, "共现 1 次不得固化");
        round(&mut sess);
        assert_eq!(user.stats().phrases, 1, "共现 2 次应固化为词组");

        let mut again = fresh(Some(Arc::clone(&user)));
        let comp = again.input("jintiantianqi", false).unwrap();
        assert_eq!(comp.candidates[0].text, "今天天气");
        assert!(comp.candidates[0].from_user);
    }

    /// 反向：渐进段累积随组词重启作废——选「今天」后把剩余拼音退格到空，再打 wo
    /// 选「我」，不得拼出伪整句「今天我」。
    #[test]
    fn progressive_segments_reset_when_composition_restarts() {
        let user = Arc::new(UserDict::new::<&str>(None));
        let mut sess = fresh(Some(Arc::clone(&user)));
        sess.input("jintianwo", false).unwrap();
        let comp = sess.compose();
        let rest = sess
            .select_prefix(pick(&comp, "今天", "jin'tian"))
            .unwrap();
        assert_eq!(rest.input, "wo");
        for _ in 0..2 {
            sess.backspace();
        }
        assert!(sess.compose().input.is_empty());
        sess.input("wo", false).unwrap();
        assert_eq!(sess.select(0).unwrap(), "我");
        assert!(
            user.word_segs("今天我").is_empty(),
            "组词重启后不得把已上屏段拼成伪整句"
        );
    }

    /// 词典多字词组封顶 2 页（12 条）：第 13 条起是首段单字且以单字收尾；首页仍全是
    /// 完整消费输入的词。简拼（zg）同样封顶。
    #[test]
    fn dict_phrases_capped_at_two_pages() {
        for input in ["shiji", "zg", "nh"] {
            let mut sess = fresh(None);
            let all = all_candidates(&mut sess, input);
            let phrases = all.iter().filter(|(t, ..)| t.chars().count() >= 2).count();
            assert_eq!(phrases, MAX_DICT_PHRASES, "{input}: 词典词组应恰好 2 页");
            assert!(
                all[MAX_DICT_PHRASES..]
                    .iter()
                    .all(|(t, ..)| t.chars().count() == 1),
                "{input}: 第 3 页起应为纯单字收尾: {:?}",
                &all[MAX_DICT_PHRASES..]
            );
        }
        let mut sess = fresh(None);
        let comp = sess.input("shiji", false).unwrap();
        assert!(
            comp.candidates.iter().all(|c| c.syllables == "shi'ji"),
            "shiji 首页应全是完整消费输入的词: {:?}",
            comp.candidates
        );
    }

    /// 用户词组不设上限、不计入封顶：20 个 shi'ji 的用户词全部出现且都在词典词之前；
    /// 学过的精确词（事迹）置顶于词典精确词（世纪）之上。
    #[test]
    fn user_phrases_unlimited_and_rank_first() {
        let user = Arc::new(UserDict::new::<&str>(None));
        for i in 0..20 {
            user.bump_word(&format!("组词{i}"), Some("shi'ji"), None);
        }
        for _ in 0..3 {
            user.bump_word("事迹", Some("shi'ji"), None);
        }
        let mut sess = fresh(Some(Arc::clone(&user)));
        let all = all_candidates(&mut sess, "shiji");
        assert_eq!(
            all.iter().filter(|(t, ..)| t.starts_with("组词")).count(),
            20,
            "用户词组不得被上限截断"
        );
        let first_dict = all.iter().position(|(.., u)| !u).unwrap();
        assert!(
            all[..first_dict].iter().filter(|(t, ..)| t.starts_with("组词")).count() == 20,
            "全部用户词组应排在词典词之前"
        );
        assert_eq!(all[0].0, "事迹", "学过的精确词应置顶");
        assert_eq!(
            all.iter().filter(|(t, ..)| t.chars().count() >= 2 && !t.starts_with("组词") && *t != "事迹").count(),
            MAX_DICT_PHRASES,
            "词典词组仍只保留 2 页"
        );
    }

    /// 完整消费输入的候选严格排在还要补字母的续接之前（用户要求：shiji 先给 shi'ji）；
    /// 反向：续接仍可达——zhonggu 的「中国」留在首页，学过的续接词也不越过精确层，
    /// 无法干净切分（quanmia）时续接仍领跑。次序断言限定在常规层（单字块之前）：
    /// 组合候选（rankComposition）夹在回退单字与兜底单字之间是既有行为，不在此约束内。
    #[test]
    fn exact_consumption_ranks_before_continuation() {
        let check = |all: Vec<(String, String, bool)>, want_user: bool| {
            let singles = all
                .iter()
                .position(|(t, ..)| t.chars().count() == 1)
                .unwrap();
            let page = &all[..singles];
            let last_exact = page
                .iter()
                .rposition(|(_, s, _)| s == "zhong'gu")
                .unwrap();
            let guo = page.iter().position(|(t, ..)| t == "中国").unwrap();
            assert!(last_exact < guo, "zhong'gu 的词应全部排在续接「中国」之前: {page:?}");
            assert!(guo < PAGE_SIZE, "续接「中国」仍须在首页可达: {page:?}");
            assert_eq!(
                page[guo].2, want_user,
                "「中国」的用户标记应符合预期: {page:?}"
            );
        };
        check(all_candidates(&mut fresh(None), "zhonggu"), false);

        let user = Arc::new(UserDict::new::<&str>(None));
        user.bump_word("中国", Some("zhong'guo"), None);
        // 学过的续接词（from_user）仍不越过完整消费输入的词典词
        check(all_candidates(&mut fresh(Some(user)), "zhonggu"), true);

        let mut sess = fresh(None);
        assert_eq!(
            all_candidates(&mut sess, "quanmia")[0].0, "全面",
            "无法干净切分时续接仍领跑"
        );
    }

    /// Linux E2E 回归（test-ci.sh「用户导入词库真的参与匹配」）：学过的单字「你」按
    /// 用户优先排在前面时，逐级回退层命中的导入词「测试词」（ce'shi'ci）必须仍在首页，
    /// 不得被「单字之后无词组」之类的封顶规则误杀。
    #[test]
    fn backoff_phrase_stays_on_first_page_after_user_single() {
        let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../../data/dict_common.tsv");
        let mut dict = Dictionary::new();
        dict.load_tsv(path).unwrap();
        dict.insert(crate::dict::WordEntry {
            word: "测试词".to_string(),
            syllables: vec!["ce".into(), "shi".into(), "ci".into()],
            freq: 99,
        });
        dict.sort_nodes();
        let user = Arc::new(UserDict::new::<&str>(None));
        user.bump_word("你", Some("ni"), None);
        let mut sess = Session::new(Arc::new(dict), Some(user));
        let comp = sess.input("nceshici", false).unwrap();
        let page: Vec<&str> = comp.candidates.iter().map(|c| c.text.as_str()).collect();
        assert_eq!(page[0], "你", "学过的单字按用户优先居首: {page:?}");
        assert!(
            page.contains(&"测试词"),
            "逐级回退命中的导入词必须留在首页: {page:?}"
        );
    }}
