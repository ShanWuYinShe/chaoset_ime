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
            for child in node.children.values_mut() {
                sort_recursive(child, entries);
            }
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

    /// 精确匹配音节序列的词条（声母碎片按碎片导航展开）
    pub fn query_exact(&self, syllables: &[&str]) -> Vec<&WordEntry> {
        let mut indices = Vec::new();
        Self::walk(&self.trie, syllables, false, &mut indices);
        indices.iter().map(|&idx| &self.entries[idx]).collect()
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
}

#[cfg(test)]
mod tests {
    use super::*;

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

        let exact = dict.query_exact(&["ni", "hao"]);
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
        let exact_xian = dict.query_exact(&["xian"]);
        assert_eq!(exact_xian.len(), 1);
        assert_eq!(exact_xian[0].word, "先");
    }
}
