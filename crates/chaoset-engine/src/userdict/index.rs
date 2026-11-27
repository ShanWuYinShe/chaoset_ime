//! 用户自学习词典内存索引：把 O(N) 全表扫描收敛到音节 Trie + 连写哈希表。
//!
//! 索引只存 `(表, 键, 预切分音节, 连写串, 词字数)` 等结构化定位信息；次数与时间
//! 衰减仍实时从 `UserFile` 读取，避免 `bump_word`/`bump_phrase` 只累加次数时做索引
//! 变更，也避免 `save()` 快照克隆整棵索引树。

use std::collections::{HashMap, HashSet};

use super::{
    decay, split_sylls, token_matches, TokenHit, UserFile, WordPrefixHit, USER_BOOST_PER_COUNT,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub(super) enum EntryTable {
    Word,
    Phrase,
}

#[derive(Debug, Clone)]
struct IndexedEntry {
    table: EntryTable,
    key: String,
    word_chars: usize,
    sylls: Vec<String>,
    joined: String,
}

#[derive(Debug, Default)]
struct TrieNode {
    children: HashMap<String, TrieNode>,
    exact: Vec<u32>,
}

/// 音节 Trie + 拼音连写哈希索引。与 `UserFile` 同受 `UserDict::data` 读写锁保护。
#[derive(Debug, Default)]
pub(super) struct UserIndex {
    entries: Vec<Option<IndexedEntry>>,
    free_ids: Vec<u32>,
    lookup: HashMap<(EntryTable, String), u32>,
    joined: HashMap<String, Vec<u32>>,
    trie: TrieNode,
}

impl UserIndex {
    pub(super) fn rebuild(file: &UserFile) -> Self {
        let mut idx = Self::default();
        let total = file.words.len() + file.phrases.len();
        idx.entries.reserve(total);
        idx.lookup.reserve(total);
        for (word, rec) in &file.words {
            idx.upsert(EntryTable::Word, word.clone(), word.chars().count(), &rec.sylls);
        }
        for (key, rec) in &file.phrases {
            idx.upsert(
                EntryTable::Phrase,
                key.clone(),
                rec.word.chars().count(),
                &rec.sylls,
            );
        }
        idx
    }

    pub(super) fn clear(&mut self) {
        self.entries.clear();
        self.free_ids.clear();
        self.lookup.clear();
        self.joined.clear();
        self.trie = TrieNode::default();
    }

    pub(super) fn upsert(
        &mut self,
        table: EntryTable,
        key: String,
        word_chars: usize,
        sylls_str: &str,
    ) {
        let sylls = split_sylls(sylls_str);
        let joined = if sylls.is_empty() {
            String::new()
        } else {
            sylls.join("")
        };
        if let Some(&id) = self.lookup.get(&(table, key.clone())) {
            let slot = self.entries[id as usize]
                .as_ref()
                .expect("lookup points to live indexed entry");
            if slot.sylls == sylls && slot.word_chars == word_chars {
                return;
            }
            let old_sylls = slot.sylls.clone();
            let old_joined = slot.joined.clone();
            Self::remove_links(&mut self.joined, &mut self.trie, id, &old_joined, &old_sylls);
            let slot_mut = self.entries[id as usize]
                .as_mut()
                .expect("lookup points to live indexed entry");
            slot_mut.word_chars = word_chars;
            slot_mut.sylls = sylls;
            slot_mut.joined = joined;
            let slot_ref = self.entries[id as usize].as_ref().unwrap();
            Self::add_links(
                &mut self.joined,
                &mut self.trie,
                id,
                &slot_ref.joined,
                &slot_ref.sylls,
            );
            return;
        }
        let id = if let Some(reused) = self.free_ids.pop() {
            reused
        } else {
            self.entries.push(None);
            (self.entries.len() - 1) as u32
        };
        Self::add_links(&mut self.joined, &mut self.trie, id, &joined, &sylls);
        self.entries[id as usize] = Some(IndexedEntry {
            table,
            key: key.clone(),
            word_chars,
            sylls,
            joined,
        });
        self.lookup.insert((table, key), id);
    }

    pub(super) fn remove(&mut self, table: EntryTable, key: &str) {
        let Some(id) = self.lookup.remove(&(table, key.to_string())) else {
            return;
        };
        if let Some(old) = self.entries[id as usize].take() {
            Self::remove_links(&mut self.joined, &mut self.trie, id, &old.joined, &old.sylls);
        }
        self.free_ids.push(id);
    }

    fn add_links(
        joined_map: &mut HashMap<String, Vec<u32>>,
        trie: &mut TrieNode,
        id: u32,
        joined: &str,
        sylls: &[String],
    ) {
        if !joined.is_empty() {
            joined_map.entry(joined.to_string()).or_default().push(id);
        }
        let mut node = trie;
        for syl in sylls {
            node = node.children.entry(syl.clone()).or_default();
        }
        node.exact.push(id);
    }

    fn remove_links(
        joined_map: &mut HashMap<String, Vec<u32>>,
        trie: &mut TrieNode,
        id: u32,
        joined: &str,
        sylls: &[String],
    ) {
        if !joined.is_empty() {
            if let Some(ids) = joined_map.get_mut(joined) {
                if let Some(pos) = ids.iter().position(|&x| x == id) {
                    ids.swap_remove(pos);
                }
                if ids.is_empty() {
                    joined_map.remove(joined);
                }
            }
        }
        remove_from_trie(trie, sylls, 0, id);
    }
}

fn remove_from_trie(node: &mut TrieNode, sylls: &[String], depth: usize, id: u32) -> bool {
    if depth == sylls.len() {
        if let Some(pos) = node.exact.iter().position(|&x| x == id) {
            node.exact.swap_remove(pos);
        }
        return node.exact.is_empty() && node.children.is_empty();
    }
    let syl = &sylls[depth];
    let remove_child = match node.children.get_mut(syl) {
        Some(child) => remove_from_trie(child, sylls, depth + 1, id),
        None => false,
    };
    if remove_child {
        node.children.remove(syl);
    }
    node.exact.is_empty() && node.children.is_empty()
}

fn calc_freq(count: i32, last_used: i64, now: i64) -> i32 {
    ((count as f64 * USER_BOOST_PER_COUNT) * decay(last_used, now)) as i32
}

struct LiveEntry<'a> {
    table: EntryTable,
    word: &'a str,
    word_chars: usize,
    sylls: &'a [String],
    segs: &'a [String],
    freq: i32,
}

fn resolve_entry<'a>(
    index: &'a UserIndex,
    file: &'a UserFile,
    id: u32,
    now: i64,
) -> Option<LiveEntry<'a>> {
    let slot = index.entries.get(id as usize)?.as_ref()?;
    match slot.table {
        EntryTable::Word => {
            let rec = file.words.get(&slot.key)?;
            Some(LiveEntry {
                table: EntryTable::Word,
                word: &slot.key,
                word_chars: slot.word_chars,
                sylls: &slot.sylls,
                segs: &rec.segs,
                freq: calc_freq(rec.count, rec.last_used, now),
            })
        }
        EntryTable::Phrase => {
            let rec = file.phrases.get(&slot.key)?;
            Some(LiveEntry {
                table: EntryTable::Phrase,
                word: &rec.word,
                word_chars: slot.word_chars,
                sylls: &slot.sylls,
                segs: &rec.segs,
                freq: calc_freq(rec.count, rec.last_used, now),
            })
        }
    }
}

fn collect_matching_nodes<'a>(
    node: &'a TrieNode,
    tokens: &[(&str, bool)],
    depth: usize,
    out: &mut Vec<&'a TrieNode>,
) {
    if depth == tokens.len() {
        out.push(node);
        return;
    }
    let (tok, exact) = tokens[depth];
    if exact {
        if let Some(child) = node.children.get(tok) {
            collect_matching_nodes(child, tokens, depth + 1, out);
        }
    } else {
        for (syl, child) in &node.children {
            if token_matches(syl, tok, false) {
                collect_matching_nodes(child, tokens, depth + 1, out);
            }
        }
    }
}

fn collect_subtree_ids(node: &TrieNode, out: &mut Vec<u32>) {
    out.extend_from_slice(&node.exact);
    for child in node.children.values() {
        collect_subtree_ids(child, out);
    }
}

fn collect_descendant_ids(node: &TrieNode, out: &mut Vec<u32>) {
    for child in node.children.values() {
        collect_subtree_ids(child, out);
    }
}

/// 解析 ID 列表并对同 `(word, sylls)` 按优先表顺序去重：
/// token/续接查询 Phrases 先于 Words；连写查询 Words 先于 Phrases。
fn materialize_unique<'a>(
    index: &'a UserIndex,
    file: &'a UserFile,
    ids: &[u32],
    now: i64,
    phrases_first: bool,
) -> Vec<LiveEntry<'a>> {
    let mut primary = Vec::new();
    let mut secondary = Vec::new();
    for &id in ids {
        let Some(entry) = resolve_entry(index, file, id, now) else {
            continue;
        };
        let is_primary = if phrases_first {
            entry.table == EntryTable::Phrase
        } else {
            entry.table == EntryTable::Word
        };
        if is_primary {
            primary.push(entry);
        } else {
            secondary.push(entry);
        }
    }
    if primary.is_empty() {
        return secondary;
    }
    if secondary.is_empty() {
        return primary;
    }
    let mut seen: HashSet<(&'a str, &'a [String])> =
        HashSet::with_capacity(primary.len() + secondary.len());
    let mut out = Vec::with_capacity(primary.len() + secondary.len());
    for entry in primary.into_iter().chain(secondary.into_iter()) {
        if seen.insert((entry.word, entry.sylls)) {
            out.push(entry);
        }
    }
    out
}

fn rank_and_build_token_hits(mut entries: Vec<LiveEntry<'_>>, limit: usize) -> Vec<TokenHit> {
    entries.sort_by(|a, b| {
        b.freq
            .cmp(&a.freq)
            .then(a.word_chars.cmp(&b.word_chars))
            .then(a.word.cmp(b.word))
            .then_with(|| a.sylls.join("'").cmp(&b.sylls.join("'")))
    });
    let mut seen_words: HashSet<&str> = HashSet::with_capacity(entries.len().min(limit));
    let mut out = Vec::with_capacity(entries.len().min(limit));
    for e in entries {
        if !seen_words.insert(e.word) {
            continue;
        }
        out.push(TokenHit {
            word: e.word.to_string(),
            syllables: e.sylls.to_vec(),
            freq: e.freq,
            segs: e.segs.to_vec(),
        });
        if out.len() >= limit {
            break;
        }
    }
    out
}

pub(super) fn query_joined_pinyin(
    index: &UserIndex,
    file: &UserFile,
    input: &str,
    max_results: usize,
    now: i64,
) -> Vec<WordPrefixHit> {
    if input.is_empty() {
        return Vec::new();
    }
    let Some(ids) = index.joined.get(input) else {
        return Vec::new();
    };
    let limit = if max_results == 0 { 20 } else { max_results };
    let mut entries = materialize_unique(index, file, ids, now, false);
    entries.sort_by(|a, b| {
        b.freq
            .cmp(&a.freq)
            .then(a.word.cmp(b.word))
            .then_with(|| a.sylls.join("'").cmp(&b.sylls.join("'")))
    });
    entries.truncate(limit);
    entries
        .into_iter()
        .map(|e| WordPrefixHit {
            word: e.word.to_string(),
            syllables: e.sylls.to_vec(),
            freq: e.freq,
            segs: e.segs.to_vec(),
        })
        .collect()
}

pub(super) fn query_tokens_exact(
    index: &UserIndex,
    file: &UserFile,
    tokens: &[(&str, bool)],
    max_results: usize,
    now: i64,
) -> Vec<TokenHit> {
    let limit = if max_results == 0 { 20 } else { max_results };
    let mut nodes = Vec::new();
    collect_matching_nodes(&index.trie, tokens, 0, &mut nodes);
    if nodes.is_empty() {
        return Vec::new();
    }
    let mut ids = Vec::new();
    for node in nodes {
        ids.extend_from_slice(&node.exact);
    }
    if ids.is_empty() {
        return Vec::new();
    }
    let entries = materialize_unique(index, file, &ids, now, true);
    rank_and_build_token_hits(entries, limit)
}

pub(super) fn query_tokens(
    index: &UserIndex,
    file: &UserFile,
    tokens: &[(&str, bool)],
    max_results: usize,
    now: i64,
) -> (Vec<TokenHit>, Vec<TokenHit>) {
    let limit = if max_results == 0 { 20 } else { max_results };
    let mut nodes = Vec::new();
    collect_matching_nodes(&index.trie, tokens, 0, &mut nodes);
    if nodes.is_empty() {
        return (Vec::new(), Vec::new());
    }
    let mut exact_ids = Vec::new();
    let mut prefix_ids = Vec::new();
    for node in nodes {
        exact_ids.extend_from_slice(&node.exact);
        collect_descendant_ids(node, &mut prefix_ids);
    }
    let exacts = if exact_ids.is_empty() {
        Vec::new()
    } else {
        rank_and_build_token_hits(materialize_unique(index, file, &exact_ids, now, true), limit)
    };
    let prefixes = if prefix_ids.is_empty() {
        Vec::new()
    } else {
        rank_and_build_token_hits(
            materialize_unique(index, file, &prefix_ids, now, true),
            limit,
        )
    };
    (exacts, prefixes)
}

pub(super) fn query_continuation(
    index: &UserIndex,
    file: &UserFile,
    tokens: &[(&str, bool)],
    frag: &str,
    max_results: usize,
    now: i64,
) -> Vec<(TokenHit, String)> {
    if frag.is_empty() || tokens.is_empty() {
        return Vec::new();
    }
    let limit = if max_results == 0 { 8 } else { max_results };
    let mut nodes = Vec::new();
    collect_matching_nodes(&index.trie, tokens, 0, &mut nodes);
    if nodes.is_empty() {
        return Vec::new();
    }

    let mut primary: Vec<(LiveEntry<'_>, &str)> = Vec::new();
    let mut secondary: Vec<(LiveEntry<'_>, &str)> = Vec::new();
    let mut sub_ids = Vec::new();

    for node in nodes {
        for (next_syl, child) in &node.children {
            let Some(remain) = next_syl.strip_prefix(frag) else {
                continue;
            };
            sub_ids.clear();
            collect_subtree_ids(child, &mut sub_ids);
            for &id in &sub_ids {
                let Some(entry) = resolve_entry(index, file, id, now) else {
                    continue;
                };
                if entry.table == EntryTable::Phrase {
                    primary.push((entry, remain));
                } else {
                    secondary.push((entry, remain));
                }
            }
        }
    }

    let mut combined: Vec<(LiveEntry<'_>, &str)> = if primary.is_empty() {
        secondary
    } else if secondary.is_empty() {
        primary
    } else {
        let mut seen: HashSet<(&str, &[String])> =
            HashSet::with_capacity(primary.len() + secondary.len());
        let mut out = Vec::with_capacity(primary.len() + secondary.len());
        for item in primary.into_iter().chain(secondary.into_iter()) {
            if seen.insert((item.0.word, item.0.sylls)) {
                out.push(item);
            }
        }
        out
    };

    combined.sort_by(|a, b| {
        a.1.len()
            .cmp(&b.1.len())
            .then(b.0.freq.cmp(&a.0.freq))
            .then(a.0.word.cmp(b.0.word))
            .then_with(|| a.0.sylls.join("'").cmp(&b.0.sylls.join("'")))
    });
    combined.truncate(limit);
    combined
        .into_iter()
        .map(|(e, remain)| {
            (
                TokenHit {
                    word: e.word.to_string(),
                    syllables: e.sylls.to_vec(),
                    freq: e.freq,
                    segs: e.segs.to_vec(),
                },
                remain.to_string(),
            )
        })
        .collect()
}
