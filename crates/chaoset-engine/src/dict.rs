//! 词典解析、Trie 紧凑存储与高效前缀召回。

use std::collections::HashMap;
use std::fs::File;
use std::io::{BufRead, BufReader};
use std::path::Path;

/// 词条对象
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct WordEntry {
    pub word: String,
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
    /// 本节点 exact_words 的最高词频（节点无词条为 0）。insert / rebuild_trie
    /// 增量维护、sort_nodes 复核；prefix 池排名直接读它，不必每次扫全节点词条。
    max_freq: u32,
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
            let freq = if parts.len() >= 3 {
                parts[2].trim().parse::<u32>().unwrap_or(100)
            } else {
                100
            };

            let syllables: Vec<String> = parts[1]
                .trim()
                .replace('\'', " ")
                .split_whitespace()
                .map(|s| s.to_string())
                .collect();

            if syllables.is_empty() || word.is_empty() {
                continue;
            }

            self.insert(WordEntry {
                word,
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
            let freq = if parts.len() >= 3 {
                parts[2].trim().parse::<u32>().unwrap_or(100)
            } else {
                100
            };

            let syllables: Vec<String> = parts[1]
                .trim()
                .replace('\'', " ")
                .split_whitespace()
                .map(|s| s.to_string())
                .collect();

            if syllables.is_empty() || word.is_empty() {
                continue;
            }

            self.insert(WordEntry {
                word,
                syllables,
                freq,
            });
            count += 1;
        }

        self.sort_nodes();
        count
    }

    /// 插入一条新词条
    ///
    /// 先沿 trie 建路径、再推入 entries：不要预先 `entry.syllables.clone()`，
    /// 那是「每条词条深拷贝一份 Vec<String>」——20 万条规模下多出几十万次
    /// String 堆分配，是 TSV/二进制两条加载路径共同的启动开销大头。trie 边
    /// 的 key 仍需 clone（HashMap<String, _> 要持有 key），但 word 与 syllables
    /// 本身直接 move 进 entries。
    pub fn insert(&mut self, entry: WordEntry) {
        let idx = self.entries.len();

        let mut node = &mut self.trie;
        for syll in &entry.syllables {
            node = node.children.entry(syll.clone()).or_default();
        }
        node.exact_words.push(idx);
        if entry.freq > node.max_freq {
            node.max_freq = entry.freq;
        }

        self.entries.push(entry);
    }

    /// 从 `entries` 全量重建 Trie。
    ///
    /// 供「先解析完、全部校验通过后再一次性提交」的加载路径使用：二进制缓存
    /// 解析失败时 `self` 必须保持原状，否则 `load_with_cache` 回退 TSV 时会
    /// 拿到半加载的词典状态。调用方需保证 `entries` 已按最终顺序就位。
    pub(crate) fn rebuild_trie(&mut self) {
        self.trie = TrieNode::default();
        let entries = &self.entries;
        for (idx, entry) in entries.iter().enumerate() {
            let mut node = &mut self.trie;
            for syll in &entry.syllables {
                node = node.children.entry(syll.clone()).or_default();
            }
            node.exact_words.push(idx);
            if entry.freq > node.max_freq {
                node.max_freq = entry.freq;
            }
        }
    }

    /// 构建完后对各节点按词频降序排序
    pub fn sort_nodes(&mut self) {
        let entries = &self.entries;
        fn sort_recursive(node: &mut TrieNode, entries: &[WordEntry]) {
            node.exact_words
                .sort_by(|&a, &b| entries[b].freq.cmp(&entries[a].freq));
            // 排序后首条即最高词频：复核增量维护的 max_freq（任何建树路径都以
            // sort_nodes 收尾，这里兜住绕过 insert/rebuild_trie 的写法）。
            node.max_freq = node
                .exact_words
                .first()
                .map(|&id| entries[id].freq)
                .unwrap_or(0);
            // 注意是**子树内最小词条下标**（Go dictNode.subtreeMin），不是排序后
            // 第一个——exact_words 按词频降序，first() 是最高频词的索引，会让
            // prefix BFS 的确定性次序键与 Go 不一致（简拼 nh 的 12 节点展开集合
            // 因此偏差，少 7 条三音节扩展词）。
            let mut min_id = node.exact_words.iter().copied().min().unwrap_or(usize::MAX);
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
    /// 按「拼音连写 == 输入」匹配词库里已有的词条（Go Dictionary.QueryJoinedPinyin）。
    ///
    /// 不依赖切分枚举、也不造词：只沿 Trie 上已有词条的路径边走，用输入串直接
    /// 对齐音节边界。用于兜住切分枚举上限导致的漏匹配（用户要求：词库里有的
    /// 词仍要能用 DP 匹配到）。空输入返回空。
    pub fn query_joined_pinyin(&self, input: &str, max_results: usize) -> Vec<&WordEntry> {
        if input.is_empty() {
            return Vec::new();
        }
        let limit = if max_results == 0 { 20 } else { max_results };
        let bytes = input.as_bytes();
        let mut out: Vec<usize> = Vec::new();
        let mut seen: std::collections::HashSet<usize> = std::collections::HashSet::new();
        fn dfs(
            node: &TrieNode,
            bytes: &[u8],
            pos: usize,
            out: &mut Vec<usize>,
            seen: &mut std::collections::HashSet<usize>,
        ) {
            if pos == bytes.len() {
                for &id in &node.exact_words {
                    if seen.insert(id) {
                        out.push(id);
                    }
                }
                return;
            }
            for l in 1..=crate::syllable::MAX_SYLLABLE_LEN {
                if pos + l > bytes.len() {
                    break;
                }
                let key = std::str::from_utf8(&bytes[pos..pos + l]).unwrap_or_default();
                if let Some(child) = node.children.get(key) {
                    dfs(child, bytes, pos + l, out, seen);
                }
            }
        }
        dfs(&self.trie, bytes, 0, &mut out, &mut seen);
        if out.is_empty() {
            return Vec::new();
        }
        let entries = &self.entries;
        out.sort_by(|&a, &b| {
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
        out.truncate(limit);
        out.iter().map(|&i| &entries[i]).collect()
    }

    /// 按**词字面**前缀查词条（Go Dictionary.QueryWordPrefix）：联想续接按
    /// 上屏词的字走（测试→测试题/测试员），只要词库里以该词开头、且比它更长
    /// 的词（同词本身不算续接）。排序：词频降序 → 词字典序 → 音节串，保证
    /// 多读音同词时的确定性；最多 max_results 条。
    pub fn query_word_prefix(&self, prefix: &str, max_results: usize) -> Vec<&WordEntry> {
        if prefix.is_empty() || max_results == 0 {
            return Vec::new();
        }
        let prefix_len = prefix.chars().count();
        let mut out: Vec<&WordEntry> = self
            .entries
            .iter()
            .filter(|e| e.word.chars().count() > prefix_len && e.word.starts_with(prefix))
            .collect();
        out.sort_by(|a, b| {
            b.freq
                .cmp(&a.freq)
                .then(a.word.cmp(&b.word))
                .then(a.syllables.join("'").cmp(&b.syllables.join("'")))
        });
        out.truncate(max_results);
        out
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

        // exact 池。节点内词条已按词频降序（sort_nodes），所以全局前 exact_limit
        // 名只可能落在「各终点节点自己的前 exact_limit 名 + 与第 exact_limit 名
        // 同频的条目」之内——若某条在自己节点里排到第 exact_limit 名之后且频率
        // 更低，同节点至少有 exact_limit 条词频不低于它，全局排序必把它挤出前
        // exact_limit 名。旧实现把碎片导航命中的**全部**词条收齐再整体排序：
        // 单声母（j/zh/sh…）一次要过万条，是每个词首字母键 2–7ms 的来源
        //（issue #122：单键延迟过高会让候选明显滞后；Windows 壳已改为异步管线）。截断边界上
        // 的并列仍由下方同一个完整比较器裁决，结果与全量排序逐条一致。
        let mut exact: Vec<(u32, usize)> = Vec::new();
        let mut seen: std::collections::HashSet<usize> = std::collections::HashSet::new();
        for node in &terminals {
            let words = &node.exact_words;
            let mut end = words.len().min(exact_limit);
            if exact_limit > 0 && end < words.len() {
                let boundary = entries[words[exact_limit - 1]].freq;
                while end < words.len() && entries[words[end]].freq == boundary {
                    end += 1;
                }
            }
            for &id in &words[..end] {
                if seen.insert(id) {
                    exact.push((entries[id].freq, id));
                }
            }
        }
        exact.sort_by(|a, b| {
            b.0.cmp(&a.0)
                .then_with(|| entries[a.1].word.cmp(&entries[b.1].word))
                .then_with(|| {
                    entries[a.1]
                        .syllables
                        .join("'")
                        .cmp(&entries[b.1].syllables.join("'"))
                })
        });
        exact.truncate(exact_limit);

        // prefix 池：Go queryTokens 的逐层 BFS。注意 Go 的结构是「ranked 跨起点
        // 节点累积，每处理一个起点就重排一次并截断到 12，随后收集该次快照的节点
        // 词条，下一层取最后一次快照」——逐节点收集会让靠前的起点先贡献词条，
        // 结果条目明显多于「全局只排一次」（简拼 nh 的 20 条 vs 13 条）。
        terminals.sort_by_key(|n| n.subtree_min);
        let mut level_nodes: Vec<&TrieNode> = terminals;
        let max_prefix_scan = prefix_limit.saturating_mul(4);
        let mut prefix_ids: Vec<usize> = Vec::new();
        let mut prefix_seen: std::collections::HashSet<usize> = std::collections::HashSet::new();
        const MAX_EXPAND_PER_LEVEL: usize = 12;
        for _depth in 0..2 {
            if level_nodes.is_empty() || prefix_ids.len() >= max_prefix_scan {
                break;
            }
            let mut ranked: Vec<(u32, usize, &TrieNode)> = Vec::new();
            let mut next_level: Vec<&TrieNode> = Vec::new();
            for node in &level_nodes {
                for child in node.children.values() {
                    // 节点最高词频读缓存（sort_nodes 复核）：旧实现每次对子节点
                    // 全部词条求 max，碎片导航下一个声母就要扫上万个词条。
                    let top = child.max_freq;
                    ranked.push((top, child.subtree_min, child));
                }
                ranked.sort_by(|a, b| b.0.cmp(&a.0).then(a.1.cmp(&b.1)));
                ranked.truncate(MAX_EXPAND_PER_LEVEL);
                next_level.clear();
                for (_, _, child) in &ranked {
                    next_level.push(*child);
                    for &id in &child.exact_words {
                        if prefix_seen.insert(id) {
                            prefix_ids.push(id);
                        }
                    }
                }
            }
            level_nodes = next_level;
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
            exact.iter().map(|&(_, i)| &entries[i]).collect(),
            prefix_ids.iter().map(|&i| &entries[i]).collect(),
        )
    }
}

#[cfg(test)]
mod tests;
