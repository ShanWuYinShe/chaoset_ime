//! Dictionary 单测：Trie 碎片导航、prefix 池逐层 BFS，以及 query_exact_and_prefix
//! 与「全量排序 + 扫词条求最高词频」旧算法参考实现的逐条等价性（issue #122）。

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
        syllables: vec!["ni".to_string()],
        freq: 1000,
    });
    dict.insert(WordEntry {
        word: "你好".to_string(),
        syllables: vec!["ni".to_string(), "hao".to_string()],
        freq: 5000,
    });
    dict.insert(WordEntry {
        word: "你好吗".to_string(),
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
    for (word, sylls, freq) in [
        ("自治州", vec!["zi", "zhi", "zhou"], 800u32),
        ("赞", vec!["zan"], 600),
        ("先", vec!["xian"], 5000),
    ] {
        dict.insert(WordEntry {
            word: word.to_string(),
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
/// prefix 池是 Go queryTokens 的逐层 BFS（跨起点累积排名、每层最多 12 节点、
/// 逐节点收集）：简拼 nh 的 prefix 池应有 20 条且含 3 音节扩展词——「全局只
/// 排一次取 12」会只剩 13 条并漏掉这些词。
#[test]
fn test_query_exact_and_prefix_fragment_bfs() {
    let mut dict = Dictionary::new();
    dict.load_tsv(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../data/dict_common.tsv"
    ))
    .unwrap();
    dict.sort_nodes();
    let (exact, prefix) = dict.query_exact_and_prefix(&["n", "h"], 120, 20);
    assert_eq!(exact.len(), 109, "简拼 nh 的精确池条数应与 Go 相同");
    assert_eq!(prefix.len(), 20, "prefix 池应与 Go 同为 20 条");
    let words: Vec<&str> = prefix.iter().map(|e| e.word.as_str()).collect();
    for want in ["女孩子", "您还未", "拿回去", "牛磺酸"] {
        assert!(words.contains(&want), "prefix 池应含 {want}: {words:?}");
    }
}

/// 旧算法的完整参考实现，保留在这里当 query_exact_and_prefix 的对照物：
/// exact 池收齐碎片导航命中的**全部**词条再整体排序截断；prefix 池的逐层
/// BFS 每次扫子节点全部词条求最高词频。返回 (exact_ids, prefix_ids)。
fn query_reference(
    dict: &Dictionary,
    syllables: &[&str],
    exact_limit: usize,
    prefix_limit: usize,
) -> (Vec<usize>, Vec<usize>) {
    let entries = &dict.entries;
    let mut terminals = Dictionary::walk_tokens_nodes(dict, syllables);
    if terminals.is_empty() {
        return (Vec::new(), Vec::new());
    }
    let mut exact_ids: Vec<usize> = Vec::new();
    let mut seen: HashMap<usize, ()> = HashMap::new();
    for node in &terminals {
        for &id in &node.exact_words {
            if seen.insert(id, ()).is_none() {
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

    terminals.sort_by_key(|n| n.subtree_min);
    let mut level_nodes = terminals;
    let max_prefix_scan = prefix_limit.saturating_mul(4);
    let mut prefix_ids: Vec<usize> = Vec::new();
    let mut prefix_seen: HashMap<usize, ()> = HashMap::new();
    for _depth in 0..2 {
        if level_nodes.is_empty() || prefix_ids.len() >= max_prefix_scan {
            break;
        }
        let mut ranked: Vec<(u32, usize, &TrieNode)> = Vec::new();
        let mut next_level: Vec<&TrieNode> = Vec::new();
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
            ranked.sort_by(|a, b| b.0.cmp(&a.0).then(a.1.cmp(&b.1)));
            ranked.truncate(12);
            next_level.clear();
            for (_, _, child) in &ranked {
                next_level.push(*child);
                for &id in &child.exact_words {
                    if prefix_seen.insert(id, ()).is_none() {
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
    (exact_ids, prefix_ids)
}

/// 逐条比较 query_exact_and_prefix 与旧算法参考实现（两个池都比）。
fn assert_query_matches_reference(
    dict: &Dictionary,
    cases: &[Vec<&str>],
    limits: &[usize],
    label: &str,
) -> usize {
    let entry_key =
        |e: &WordEntry| (e.freq, e.word.clone(), e.syllables.join("'"), e.word.chars().count());
    let mut checked = 0usize;
    for limit in limits {
        for toks in cases {
            let (want_exact, want_prefix) = query_reference(dict, toks, *limit, 20);
            let (exact, prefix) = dict.query_exact_and_prefix(toks, *limit, 20);
            let got: Vec<_> = exact.iter().map(|e| entry_key(e)).collect();
            let want: Vec<_> = want_exact.iter().map(|&i| entry_key(&dict.entries[i])).collect();
            assert_eq!(got, want, "{label}: exact 池与全量排序不一致: toks={toks:?} limit={limit}");
            let got: Vec<_> = prefix.iter().map(|e| entry_key(e)).collect();
            let want: Vec<_> =
                want_prefix.iter().map(|&i| entry_key(&dict.entries[i])).collect();
            assert_eq!(
                got, want,
                "{label}: prefix 池与「扫词条求最高词频」的旧 BFS 不一致: toks={toks:?} limit={limit}"
            );
            checked += 1;
        }
    }
    checked
}

/// 内置词库：覆盖全部单声母、zh/ch/sh、单音节、两段简拼与多段输入，以及含 0
/// 在内的各种截断上限（issue #122：有界 exact 池必须与全量排序逐条等价）。
#[test]
fn test_exact_pool_bounded_equals_full_sort() {
    let mut dict = Dictionary::new();
    dict.load_tsv(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../data/dict_common.tsv"
    ))
    .unwrap();
    let initials = [
        "b", "p", "m", "f", "d", "t", "n", "l", "g", "k", "h", "j", "q", "x", "zh", "ch",
        "sh", "r", "z", "c", "s", "y", "w",
    ];
    let mut cases: Vec<Vec<&str>> = initials.iter().map(|i| vec![*i]).collect();
    for s in ["yi", "ji", "shi", "a", "e", "wo", "ni", "de"] {
        cases.push(vec![s]);
    }
    for (a, b) in [
        ("n", "h"),
        ("z", "g"),
        ("j", "j"),
        ("d", "k"),
        ("b", "j"),
        ("zh", "g"),
        ("s", "h"),
        ("c", "h"),
        ("x", "i"),
    ] {
        cases.push(vec![a, b]);
    }
    let multi: [&[&str]; 7] = [
        &["xi", "an"],
        &["duan", "k"],
        &["duan", "kou"],
        &["ying", "gai", "s"],
        &["x", "i", "a", "n"],
        &["z", "h", "o", "n", "g"],
        &["b", "ei", "b", "ian"],
    ];
    for toks in multi {
        cases.push(toks.to_vec());
    }
    let checked = assert_query_matches_reference(
        &dict,
        &cases,
        &[0, 1, 7, 20, 120, 300, 5000],
        "内置词库",
    );
    assert!(checked >= 329, "等价性检查样本数不足: {checked}");
}

/// 同频密集的词库（用户导入的第三方词库常见）：截断边界落在并列组里时，
/// 全量排序按字典序取词，有界收集必须靠「与第 limit 名同频的条目全部收
/// 进来」才等价；prefix 池的子节点最高词频也大量并列。
#[test]
fn test_exact_pool_bounded_equals_full_sort_tie_heavy() {
    let mut dict = Dictionary::new();
    let mut put = |word: &str, syllables: &[&str], freq: u32| {
        dict.insert(WordEntry {
            word: word.to_string(),
            syllables: syllables.iter().map(|s| s.to_string()).collect(),
            freq,
        });
    };
    // 节点内并列序 = 插入序，这里刻意与字典序相反。
    for i in 0..40 {
        put(&format!("词{:02}", 39 - i), &["da"], 50);
    }
    for i in 0..3 {
        put(&format!("甲{i}"), &["dai"], 60);
    }
    for i in 0..12 {
        put(&format!("乙{:02}", i), &["dan"], 50);
    }
    for i in 0..30u32 {
        put(&format!("丙{:02}", i), &["da", "i"], 40 + (i % 2) * 5);
        put(&format!("丁{:02}", i), &["da", "o"], 40 + (i % 3) * 5);
        put(&format!("戊{:02}", i), &["dai", "z"], 45);
    }
    for i in 0..6 {
        put(&format!("己{i}"), &["da", "i", "a"], 30);
    }
    dict.sort_nodes();

    let cases: Vec<Vec<&str>> = vec![
        vec!["d"],
        vec!["da"],
        vec!["d", "i"],
        vec!["d", "a"],
        vec!["da", "i"],
        vec!["da", "a"],
        vec!["dai", "z"],
        vec!["da", "i", "a"],
        vec!["d", "i", "a"],
    ];
    let limits: Vec<usize> = (0..=12).chain([20, 45, 100, 5000]).collect();
    assert_query_matches_reference(&dict, &cases, &limits, "同频密集词库");
}

/// 节点缓存的最高词频必须等于「节点全部词条词频的最大值」，且两条建树路径
/// （insert 增量 / rebuild_trie 重建）结果一致——prefix 池排名直接读它。
fn assert_max_freq_consistent(dict: &Dictionary) {
    let entries = &dict.entries;
    fn walk(node: &TrieNode, entries: &[WordEntry]) {
        let real = node
            .exact_words
            .iter()
            .map(|&id| entries[id].freq)
            .max()
            .unwrap_or(0);
        assert_eq!(node.max_freq, real, "节点 max_freq 与词条最高词频不一致");
        for child in node.children.values() {
            walk(child, entries);
        }
    }
    walk(&dict.trie, entries);
}

#[test]
fn test_node_max_freq_matches_words_on_both_build_paths() {
    let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../../data/dict_common.tsv");
    let mut dict = Dictionary::new();
    dict.load_tsv(path).unwrap();
    assert_max_freq_consistent(&dict);
    let before = dict.query_exact_and_prefix(&["zh"], 300, 20);
    let before_words: Vec<_> = before
        .0
        .iter()
        .map(|e| (e.word.clone(), e.syllables.join("'")))
        .collect();

    // 二进制缓存加载路径：先整体解析再 rebuild_trie + sort_nodes。
    dict.rebuild_trie();
    dict.sort_nodes();
    assert_max_freq_consistent(&dict);
    let after = dict.query_exact_and_prefix(&["zh"], 300, 20);
    let after_words: Vec<_> = after
        .0
        .iter()
        .map(|e| (e.word.clone(), e.syllables.join("'")))
        .collect();
    assert_eq!(before_words, after_words, "两条建树路径的查询结果必须一致");
}
