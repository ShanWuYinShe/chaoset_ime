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

    /// 精确匹配音节序列的词条
    pub fn query_exact(&self, syllables: &[&str]) -> Vec<&WordEntry> {
        let mut node = &self.trie;
        for &syll in syllables {
            match node.children.get(syll) {
                Some(next) => node = next,
                None => return Vec::new(),
            }
        }
        node.exact_words
            .iter()
            .map(|&idx| &self.entries[idx])
            .collect()
    }

    /// 前缀匹配音节序列的所有词条（包含以其为前缀的多字词）
    pub fn query_prefix(&self, syllables: &[&str], limit: usize) -> Vec<&WordEntry> {
        let mut node = &self.trie;
        for &syll in syllables {
            match node.children.get(syll) {
                Some(next) => node = next,
                None => return Vec::new(),
            }
        }

        let mut indices = Vec::new();
        fn collect_all(node: &TrieNode, out: &mut Vec<usize>) {
            out.extend_from_slice(&node.exact_words);
            for child in node.children.values() {
                collect_all(child, out);
            }
        }

        collect_all(node, &mut indices);
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
}
