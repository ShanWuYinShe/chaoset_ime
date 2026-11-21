//! 词典解析、Trie 紧凑存储与高效前缀召回。

use std::collections::HashMap;
use std::fs::File;
use std::io::{BufRead, BufReader};
use std::path::Path;

/// 词条对象
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct WordEntry {
    pub word: String,
    pub pinyin: String,
    pub syllables: Vec<String>,
    pub freq: u32,
}

/// 紧凑前缀 Trie 树节点
#[derive(Debug, Default)]
struct TrieNode {
    children: HashMap<String, TrieNode>,
    /// 完整匹配当前音节路径的词条列表索引（按词频降序排列）
    exact_words: Vec<usize>,
    /// 子树内最小词条下标（Go dictNode.subtreeMin）：prefix 逐层展开的确定性
    /// 次序键——同层节点子树互不相交，该键唯一，构成全序。
    subtree_min: usize,
}

/// 主词典
pub struct Dictionary {
    pub entries: Vec<WordEntry>,
    trie: TrieNode,
}

impl Dictionary {
    pub fn new() -> Self {
        Self {
            entries: Vec::new(),
            trie: TrieNode::default(),
        }
    }

    /// 从 TSV 文件加载词典
    pub fn load_tsv<P: AsRef<Path>>(&mut self, path: P) -> std::io::Result<usize> {
        let file = File::open(path)?;
        let reader = BufReader::new(file);
        let mut count = 0;

        for line_res in reader.lines() {
            let line = line_res?;
            let trimmed = line.trim();
            if trimmed.is_empty() || trimmed.starts_with('#') {
                continue;
            }

            let parts: Vec<&str> = trimmed.split('\t').collect();
            if parts.len() < 2 {
                continue;
            }

            let word = parts[0].trim().to_string();
            let pinyin = parts[1].trim().to_string();
            let freq = if parts.len() >= 3 {
                parts[2].trim().parse::<u32>().unwrap_or(100)
            } else {
                100
            };

            let syllables: Vec<String> = pinyin
                .replace('\'', " ")
                .split_whitespace()
                .map(|s| s.to_string())
                .collect();

            if syllables.is_empty() || word.is_empty() {
                continue;
            }

            self.insert(WordEntry {
                word,
                pinyin,
                syllables,
                freq,
            });
            count += 1;
        }

        self.sort_nodes();
        Ok(count)
    }

    /// 从 TSV 字符串加载词典
    pub fn load_tsv_str(&mut self, content: &str) -> usize {
        let mut count = 0;
        for line in content.lines() {
            let trimmed = line.trim();
            if trimmed.is_empty() || trimmed.starts_with('#') {
                continue;
            }

            let parts: Vec<&str> = trimmed.split('\t').collect();
            if parts.len() < 2 {
                continue;
            }

            let word = parts[0].trim().to_string();
            let pinyin = parts[1].trim().to_string();
            let freq = if parts.len() >= 3 {
                parts[2].trim().parse::<u32>().unwrap_or(100)
            } else {
                100
            };

            let syllables: Vec<String> = pinyin
                .replace('\'', " ")
                .split_whitespace()
                .map(|s| s.to_string())
                .collect();

            if syllables.is_empty() || word.is_empty() {
                continue;
            }

            self.insert(WordEntry {
                word,
                pinyin,
                syllables,
                freq,
            });
            count += 1;
        }

        self.sort_nodes();
        count
    }

    /// 插入一条新词条
    pub fn insert(&mut self, entry: WordEntry) {
        let idx = self.entries.len();
        let syllables = entry.syllables.clone();
        self.entries.push(entry);

        let mut node = &mut self.trie;
        for syll in &syllables {
            node = node.children.entry(syll.clone()).or_default();
        }
        node.exact_words.push(idx);
    }

    /// 构建完后对各节点按词频降序排序
    pub fn sort_nodes(&mut self) {
        let entries = &self.entries;
        fn sort_recursive(node: &mut TrieNode, entries: &[WordEntry]) {
            node.exact_words
                .sort_by(|&a, &b| entries[b].freq.cmp(&entries[a].freq));
            let mut min_id = node.exact_words.first().copied().unwrap_or(usize::MAX);
            for child in node.children.values_mut() {
                sort_recursive(child, entries);
                if child.subtree_min < min_id {
                    min_id = child.subtree_min;
                }
            }
            node.subtree_min = min_id;
        }
        sort_recursive(&mut self.trie, entries);
    }

    /// 声母碎片判定：单/双字母声母（zh/ch/sh）且本身不是完整音节。
    /// 简拼与无候选兜底（如 zzz → z'z'z）都靠它在 Trie 上做碎片导航。
    fn is_initial_fragment(syll: &str) -> bool {
        crate::syllable::INITIALS.contains(&syll) && !crate::syllable::is_syllable(syll)
    }

    /// 沿音节序列下钻：整音节精确匹配子节点；声母碎片则展开所有以该
    /// 声母开头的音节子树（ze/zan/zi… 都算「z」的命中）。prefix=true
    /// 时叶子收集整棵子树（词级前缀），否则只收叶子节点的精确词条。
    fn walk<'a>(node: &'a TrieNode, sylls: &[&str], prefix: bool, out: &mut Vec<usize>) {
        if sylls.is_empty() {
            if prefix {
                fn collect_all(n: &TrieNode, o: &mut Vec<usize>) {
                    o.extend_from_slice(&n.exact_words);
                    for child in n.children.values() {
                        collect_all(child, o);
                    }
                }
                collect_all(node, out);
            } else {
                out.extend_from_slice(&node.exact_words);
            }
            return;
        }
        let syll = sylls[0];
        match node.children.get(syll) {
            Some(next) => Self::walk(next, &sylls[1..], prefix, out),
            None => {
                if Self::is_initial_fragment(syll) {
                    for (k, child) in node.children.iter() {
                        if k.starts_with(syll) {
                            Self::walk(child, &sylls[1..], prefix, out);
                        }
                    }
                }
            }
        }
    }

    /// 精确音节路径上的全部词条（按节点词频序排列，不截断、**不做碎片
    /// 导航**）。对齐 Go Dictionary.QueryExact：错拼 DP 的分段查询要求
    /// 「每一段都是词库里真实存在的词」，用碎片导航会造出假词段。
    pub fn exact_words(&self, syllables: &[&str]) -> Vec<&WordEntry> {
        let mut node = &self.trie;
        for syll in syllables {
            match node.children.get(*syll) {
                Some(next) => node = next,
                None => return Vec::new(),
            }
        }
        node.exact_words
            .iter()
            .map(|&idx| &self.entries[idx])
            .collect()
    }

    /// 词库中是否存在「词 + 音节序列」精确词条（Go Dictionary.Contains）。
    /// 错拼 DP 用它排除「整词本身已是词条」的组合（如 你好 = 你+好），
    /// 否则会给整词候选补上分段、改变上屏学习语义。
    pub fn contains_word(&self, word: &str, syllables: &[&str]) -> bool {
        self.exact_words(syllables).iter().any(|e| e.word == word)
    }

    /// 精确匹配音节序列的词条（声母碎片按碎片导航展开；碎片导航会展开
    /// 多个子树，按词频降序截断到 limit——对齐 Go QueryTokensExact 的上限
    /// 口径：单音节全拼/单段声母/单字兜底 300、多段简拼 120、回退层 10）
    pub fn query_exact(&self, syllables: &[&str], limit: usize) -> Vec<&WordEntry> {
        let mut indices = Vec::new();
        Self::walk(&self.trie, syllables, false, &mut indices);
        let entries = &self.entries;
        indices.sort_by(|&a, &b| entries[b].freq.cmp(&entries[a].freq));
        indices.dedup();
        indices
            .into_iter()
            .take(limit)
            .map(|idx| &entries[idx])
            .collect()
    }

    /// 前缀匹配音节序列的所有词条（包含以其为前缀的多字词；碎片导航
    /// 展开的多个子树按词频降序合并去重）
    pub fn query_prefix(&self, syllables: &[&str], limit: usize) -> Vec<&WordEntry> {
        let mut indices = Vec::new();
        Self::walk(&self.trie, syllables, true, &mut indices);
        let entries = &self.entries;
        indices.sort_by(|&a, &b| entries[b].freq.cmp(&entries[a].freq));
        indices.dedup();

        indices
            .into_iter()
            .take(limit)
            .map(|idx| &self.entries[idx])
            .collect()
    }

    /// 沿音节序列精确下钻，返回全部终点节点（不走碎片导航——续接查询的
    /// tokens 都是完整音节）。
    fn walk_nodes<'a>(node: &'a TrieNode, sylls: &[&str], out: &mut Vec<&'a TrieNode>) {
        if sylls.is_empty() {
            out.push(node);
            return;
        }
        if let Some(next) = node.children.get(sylls[0]) {
            Self::walk_nodes(next, &sylls[1..], out);
        }
    }

    /// 沿 token 序列下钻并返回**全部终点节点**（Go walkTokens 镜像）：
    /// 完整音节按子节点精确匹配；声母碎片按「音节以该声母开头」展开
    /// （z 兼容 zh、c 兼容 ch、s 兼容 sh，与 Go matchSyllable 同口径）。
    /// 简拼/混拼（n+h、zzz）能命中多个终点节点，后续双池各自合并。
    fn walk_tokens_nodes<'a>(&'a self, tokens: &[&str]) -> Vec<&'a TrieNode> {
        if tokens.is_empty() {
            return Vec::new();
        }
        let mut curr: Vec<&TrieNode> = vec![&self.trie];
        for tok in tokens {
            let mut next: Vec<&TrieNode> = Vec::new();
            let exact = crate::syllable::is_syllable(tok);
            for node in &curr {
                if exact {
                    if let Some(child) = node.children.get(*tok) {
                        next.push(child);
                    }
                } else {
                    for (syl, child) in node.children.iter() {
                        if Self::match_syllable(syl, tok) {
                            next.push(child);
                        }
                    }
                }
            }
            if next.is_empty() {
                return Vec::new();
            }
            curr = next;
        }
        curr
    }

    /// 音节与 token 的匹配（Go matchSyllable）：完整音节等值；碎片前缀匹配，
    /// 且 z/c/s 兼容 zh/ch/sh。
    fn match_syllable(syl: &str, tok: &str) -> bool {
        if syl == tok || syl.starts_with(tok) {
            return true;
        }
        matches!((tok, syl), ("z", "zh") | ("c", "ch") | ("s", "sh"))
            || (tok == "z" && syl.starts_with("zh"))
            || (tok == "c" && syl.starts_with("ch"))
            || (tok == "s" && syl.starts_with("sh"))
    }

    /// 末段残缺续接查询（对齐 Go Dictionary.QueryContinuation）：head 音节
    /// 全部精确消耗后，把 frag 视为**一个尚未打完的音节**，沿以其为前缀的
    /// 下探两层（覆盖 中国 / 中国人 这类衍生词），返回（词条索引, 需补全的
    /// 尾巴）。补全越短越可能：中国(remain "o") 优先于 中关村(remain "an")。
    pub fn query_continuation(
        &self,
        tokens: &[&str],
        frag: &str,
        max_results: usize,
    ) -> Vec<(usize, String)> {
        if frag.is_empty() || tokens.is_empty() {
            return Vec::new();
        }
        let mut terminals: Vec<&TrieNode> = Vec::new();
        Self::walk_nodes(&self.trie, tokens, &mut terminals);

        let entries = &self.entries;
        let mut out: Vec<(usize, String)> = Vec::new();
        let mut seen: std::collections::HashSet<usize> = std::collections::HashSet::new();
        fn walk_layers<'a>(
            node: &'a TrieNode,
            depth: usize,
            remain: &str,
            frag: &str,
            out: &mut Vec<(usize, String)>,
            seen: &mut std::collections::HashSet<usize>,
            entries: &'a [WordEntry],
        ) {
            if depth > 2 {
                return;
            }
            for (syl, child) in node.children.iter() {
                if !syl.starts_with(frag) {
                    continue;
                }
                let r = if depth == 0 {
                    syl[frag.len()..].to_string()
                } else {
                    remain.to_string()
                };
                for &id in &child.exact_words {
                    if seen.insert(id) {
                        out.push((id, r.clone()));
                    }
                }
                walk_layers(child, depth + 1, &r, frag, out, seen, entries);
            }
        }
        for node in terminals {
            walk_layers(node, 0, "", frag, &mut out, &mut seen, entries);
        }
        // 补全越短越可能：remain 长度升序 → 词频降序 → 字典序
        out.sort_by(|a, b| {
            let (ia, ra) = a;
            let (ib, rb) = b;
            ra.len()
                .cmp(&rb.len())
                .then(entries[*ib].freq.cmp(&entries[*ia].freq))
                .then(entries[*ia].word.cmp(&entries[*ib].word))
        });
        out.truncate(max_results);
        out
    }

    /// Go queryTokens 的双池语义（错拼/常规召回共用）：
    ///   - exact 池 = 该音节路径节点上的词条（同音字全量），按词频降序、
    ///     同频按词字典序，截断 exact_limit；
    ///   - prefix 池 = 「衍生偏长词」，**逐层探索（最多向下 2 层、每层最多展开
    ///     12 个节点）**：节点按「节点内最高词频、子树最小词条下标」排序后展开，
    ///     避免宽泛节点下全树递归与巨型排序（Go 注释实测：z/zh 单键 1.2–1.8ms
    ///     的主因）。收集后按词频降序、词长升序、字典序排序截断。
    ///
    /// 不能简单地「取整棵子树再按词频截断」——那会把 2 个音节以外的长词
    /// （xi'an 输入带出 西安交通大学）混进候选，与 Go 行为不一致。
    pub fn query_exact_and_prefix(
        &self,
        syllables: &[&str],
        exact_limit: usize,
        prefix_limit: usize,
    ) -> (Vec<&WordEntry>, Vec<&WordEntry>) {
        let entries = &self.entries;
        let mut terminals: Vec<&TrieNode> = self.walk_tokens_nodes(syllables);
        if terminals.is_empty() {
            return (Vec::new(), Vec::new());
        }

        // exact 池
        let mut exact_ids: Vec<usize> = Vec::new();
        let mut seen: std::collections::HashSet<usize> = std::collections::HashSet::new();
        for node in &terminals {
            for &id in &node.exact_words {
                if seen.insert(id) {
                    exact_ids.push(id);
                }
            }
        }
        exact_ids.sort_by(|&a, &b| {
            entries[b]
                .freq
                .cmp(&entries[a].freq)
                .then(entries[a].word.cmp(&entries[b].word))
                .then(
                    entries[a]
                        .syllables
                        .join("'")
                        .cmp(&entries[b].syllables.join("'")),
                )
        });
        exact_ids.truncate(exact_limit);

        // prefix 池：Go queryTokens 的逐层 BFS
        terminals.sort_by_key(|n| n.subtree_min);
        let mut level_nodes: Vec<&TrieNode> = terminals;
        let max_prefix_scan = prefix_limit.saturating_mul(4);
        let mut prefix_ids: Vec<usize> = Vec::new();
        let mut prefix_seen: std::collections::HashSet<usize> = std::collections::HashSet::new();
        const MAX_EXPAND_PER_LEVEL: usize = 12;
        let mut depth = 0;
        while depth < 2 && !level_nodes.is_empty() && prefix_ids.len() < max_prefix_scan {
            let mut ranked: Vec<(u32, usize, &TrieNode)> = Vec::new();
            for node in &level_nodes {
                for child in node.children.values() {
                    let top = child
                        .exact_words
                        .iter()
                        .map(|&id| entries[id].freq)
                        .max()
                        .unwrap_or(0);
                    ranked.push((top, child.subtree_min, child));
                }
            }
            ranked.sort_by(|a, b| b.0.cmp(&a.0).then(a.1.cmp(&b.1)));
            ranked.truncate(MAX_EXPAND_PER_LEVEL);
            let mut next_level: Vec<&TrieNode> = Vec::new();
            for (_, _, child) in ranked {
                next_level.push(child);
                for &id in &child.exact_words {
                    if prefix_seen.insert(id) {
                        prefix_ids.push(id);
                    }
                }
            }
            level_nodes = next_level;
            depth += 1;
        }
        prefix_ids.sort_by(|&a, &b| {
            entries[b]
                .freq
                .cmp(&entries[a].freq)
                .then(
                    entries[a]
                        .word
                        .chars()
                        .count()
                        .cmp(&entries[b].word.chars().count()),
                )
                .then(entries[a].word.cmp(&entries[b].word))
                .then(
                    entries[a]
                        .syllables
                        .join("'")
                        .cmp(&entries[b].syllables.join("'")),
                )
        });
        prefix_ids.truncate(prefix_limit);

        (
            exact_ids.iter().map(|&i| &entries[i]).collect(),
            prefix_ids.iter().map(|&i| &entries[i]).collect(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// prefix 池必须是 Go queryTokens 的逐层探索语义（≤2 层）：只收「多 1~2
    /// 个音节」的衍生词，更长的词不得混入（否则 xi'an 会带出 西安交通大学）。
    #[test]
    fn test_prefix_bfs_depth_limit() {
        let mut dict = Dictionary::new();
        for (word, pinyin, freq) in [
            ("西安", "xi'an", 70u32),
            ("西安事变", "xi'an'shi'bian", 40),
            ("西安交通大学", "xi'an'jiao'tong'da'xue", 50),
        ] {
            dict.insert(WordEntry {
                word: word.to_string(),
                pinyin: pinyin.to_string(),
                syllables: pinyin.split('\'').map(|s| s.to_string()).collect(),
                freq,
            });
        }
        dict.sort_nodes();

        let (exact, prefix) = dict.query_exact_and_prefix(&["xi", "an"], 20, 20);
        assert_eq!(exact.len(), 1);
        assert_eq!(exact[0].word, "西安");
        let words: Vec<&str> = prefix.iter().map(|e| e.word.as_str()).collect();
        assert!(
            words.contains(&"西安事变"),
            "+2 音节内的衍生词应在 prefix 池: {words:?}"
        );
        assert!(
            !words.contains(&"西安交通大学"),
            "超过 2 层的长词不得进入 prefix 池: {words:?}"
        );
    }

    #[test]
    fn test_dict_exact_and_prefix() {
        let mut dict = Dictionary::new();
        dict.insert(WordEntry {
            word: "你".to_string(),
            pinyin: "ni".to_string(),
            syllables: vec!["ni".to_string()],
            freq: 1000,
        });
        dict.insert(WordEntry {
            word: "你好".to_string(),
            pinyin: "ni'hao".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string()],
            freq: 5000,
        });
        dict.insert(WordEntry {
            word: "你好吗".to_string(),
            pinyin: "ni'hao'ma".to_string(),
            syllables: vec!["ni".to_string(), "hao".to_string(), "ma".to_string()],
            freq: 800,
        });
        dict.sort_nodes();

        let exact = dict.query_exact(&["ni", "hao"], 20);
        assert_eq!(exact.len(), 1);
        assert_eq!(exact[0].word, "你好");

        let prefix = dict.query_prefix(&["ni"], 10);
        assert_eq!(prefix.len(), 3);
        assert_eq!(prefix[0].word, "你好"); // 词频 5000 居首
        assert_eq!(prefix[1].word, "你"); // 词频 1000 居次
        assert_eq!(prefix[2].word, "你好吗"); // 词频 800
    }

    /// 声母碎片导航：单声母 token（z/x/n…）按「音节以该声母开头」展开，
    /// 支撑简拼与无候选兜底（zzz → z'z'z 召回自治州/啧啧啧等）。
    #[test]
    fn test_fragment_navigation() {
        let mut dict = Dictionary::new();
        for (word, pinyin, sylls, freq) in [
            ("自治州", "zi'zhi'zhou", vec!["zi", "zhi", "zhou"], 800u32),
            ("赞", "zan", vec!["zan"], 600),
            ("先", "xian", vec!["xian"], 5000),
        ] {
            dict.insert(WordEntry {
                word: word.to_string(),
                pinyin: pinyin.to_string(),
                syllables: sylls.iter().map(|s| s.to_string()).collect(),
                freq,
            });
        }
        dict.sort_nodes();

        // 碎片 z 展开 z* 子树：自治州(z* 前缀)与赞都命中，按词频降序。
        let z_hits = dict.query_prefix(&["z"], 10);
        let z_words: Vec<&str> = z_hits.iter().map(|e| e.word.as_str()).collect();
        assert!(
            z_words.contains(&"自治州"),
            "碎片 z 应召回 zi'zhi'zhou: {:?}",
            z_words
        );
        assert!(z_words.contains(&"赞"), "碎片 z 应召回 zan: {:?}", z_words);
        assert_eq!(z_hits[0].word, "自治州"); // 词频 800 居首、赞 600 居次

        // 碎片 x 只命中 x*：先(xian) 命中，z* 词条不出现。
        let x_hits = dict.query_prefix(&["x"], 10);
        assert_eq!(x_hits.len(), 1);
        assert_eq!(x_hits[0].word, "先");

        // 碎片精确匹配（词级）：query_exact(["xian"]) 只有整音节路径。
        let exact_xian = dict.query_exact(&["xian"], 20);
        assert_eq!(exact_xian.len(), 1);
        assert_eq!(exact_xian[0].word, "先");
    }
}
