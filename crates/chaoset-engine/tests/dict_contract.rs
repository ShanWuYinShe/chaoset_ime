//! 内置词库的数据契约（构建期严格校验，CI 门禁）。
//!
//! 内置词库是 `cmd/chaoset-dictgen` 的生成产物，正确性必须由 CI 保证：
//! 这里按严格规则独立解析 `data/dict_common.tsv`（3 列、音节合法、
//! 音节数==字数、词频 1..99），并断言规模与手工词表「已合并进产物」；
//! 任何一行脏数据或忘记重新生成都让 CI 失败，而不是留到用户机器上被
//! 运行时宽松加载吞掉。
//!
//! 为什么放在 Rust 侧：数据契约必须由真正消费这份数据的引擎实现对其实施
//! 断言。运行时除 TSV 源外，还会加载 dictc 编译出的二进制词库（CSDB），但
//! 二进制只是 TSV 的等价编码（见 tests/dict_binary.rs），契约仍严格校验 TSV
//! 源本身；Go 引擎删除后这一层不能跟着消失。

use std::collections::HashSet;
use std::fs;
use std::path::PathBuf;

fn data_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../data")
}

struct Row {
    word: String,
    sylls: Vec<String>,
    freq: i64,
}

fn key(word: &str, sylls: &[String]) -> String {
    format!("{}\u{0}{}", word, sylls.join(" "))
}

/// 严格解析「词\t拼音\t词频」TSV：坏行直接 panic（测试失败）。
fn parse_strict(rel: &str, min_freq: i64) -> Vec<Row> {
    let path = data_dir().join(rel);
    let text =
        fs::read_to_string(&path).unwrap_or_else(|e| panic!("读取 {} 失败: {e}", path.display()));
    let mut rows = Vec::new();
    for (idx, raw) in text.lines().enumerate() {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let line_no = idx + 1;
        let parts: Vec<&str> = line.split('\t').collect();
        assert_eq!(
            parts.len(),
            3,
            "{} 第 {} 行应为 3 列: {line:?}",
            rel,
            line_no
        );
        let word = parts[0].trim();
        let sylls: Vec<String> = parts[1].split_whitespace().map(str::to_string).collect();
        let freq: i64 = parts[2]
            .trim()
            .parse()
            .unwrap_or_else(|e| panic!("{} 第 {} 行词频不是整数: {e}", rel, line_no));
        assert!(
            !word.is_empty() && !sylls.is_empty(),
            "{} 第 {} 行词或拼音为空",
            rel,
            line_no
        );
        assert!(
            freq >= min_freq,
            "{} 第 {} 行词频 {freq} 应 ≥{min_freq}",
            rel,
            line_no
        );
        for s in &sylls {
            assert!(
                chaoset_engine::is_syllable(s),
                "{} 第 {} 行存在非法音节 {s:?}",
                rel,
                line_no
            );
        }
        rows.push(Row {
            word: word.to_string(),
            sylls,
            freq,
        });
    }
    rows
}

#[test]
fn builtin_dict_is_strictly_valid() {
    let rows = parse_strict("dict_common.tsv", 1);
    let mut bad_syll = Vec::new();
    let mut bad_freq = Vec::new();
    let mut multi = 0usize;
    let mut single = 0usize;
    let mut words = HashSet::new();
    for r in &rows {
        if r.sylls.len() != r.word.chars().count() {
            bad_syll.push(format!("{}/{}", r.word, r.sylls.join(" ")));
        }
        if !(1..=99).contains(&r.freq) {
            bad_freq.push(format!("{}={}", r.word, r.freq));
        }
        if r.sylls.len() > 1 {
            multi += 1;
        } else {
            single += 1;
        }
        words.insert(r.word.clone());
    }
    assert!(
        bad_syll.is_empty(),
        "{} 条音节数与字数不等，例 {:?}",
        bad_syll.len(),
        &bad_syll[..bad_syll.len().min(3)]
    );
    assert!(
        bad_freq.is_empty(),
        "{} 条词频超出 1..99，例 {:?}",
        bad_freq.len(),
        &bad_freq[..bad_freq.len().min(3)]
    );
    assert!(
        rows.len() >= 60_000,
        "内置词库词条数异常偏少: {}（期望 ≥60000）",
        rows.len()
    );
    assert!(
        multi * 100 >= rows.len() * 80,
        "多字词比例异常: {}/{}（期望 ≥80%）",
        multi,
        rows.len()
    );
    assert!(single >= 8_000, "单字条目异常偏少: {single}（期望 ≥8000）");
    for w in ["的", "我们", "你好", "数据", "微信"] {
        assert!(
            words.contains(w),
            "内置词库缺少常用词 {w:?}（词表门槛或拼音链路被改坏）"
        );
    }
}

/// `data/dict_extra.tsv` / `data/dict_hot.tsv` / `data/categories/*.tsv` 是
/// **允许手工编辑** 的词表：逐行校验格式，并断言每条都已合并进生成产物
///（改完词表必须重新运行 chaoset-dictgen 并提交产物）。
fn check_supplement(rel: &str) {
    let rows = parse_strict(rel, 1);
    assert!(
        !rows.is_empty(),
        "{rel} 词表为空（至少应保留历史维护的词条）"
    );
    let common: HashSet<String> = parse_strict("dict_common.tsv", 1)
        .iter()
        .map(|r| key(&r.word, &r.sylls))
        .collect();
    for r in &rows {
        assert!(
            common.contains(&key(&r.word, &r.sylls)),
            "{rel} 词条 {:?}（{}）未合并进 dict_common.tsv —— 改完必须重新运行 cmd/chaoset-dictgen 并提交生成产物",
            r.word,
            r.sylls.join(" ")
        );
    }
}

#[test]
fn supplement_tables_are_valid_and_merged() {
    check_supplement("dict_extra.tsv");
    check_supplement("dict_hot.tsv");
    let cat_dir = data_dir().join("categories");
    let mut count = 0;
    for entry in fs::read_dir(&cat_dir).expect("读取分类词库目录失败") {
        let entry = entry.expect("读取分类词库目录项失败");
        let path = entry.path();
        if path.extension().and_then(|e| e.to_str()) != Some("tsv") {
            continue;
        }
        let rel = format!("categories/{}", path.file_name().unwrap().to_string_lossy());
        check_supplement(&rel);
        count += 1;
    }
    assert!(
        count >= 5,
        "分类词库文件数偏少: {count}（期望至少 5 组分类）"
    );
}

/// 运行时加载路径（宽松/去重）必须能载入内置词库，且分页常量与壳契约一致。
#[test]
fn runtime_loads_builtin_dict_and_page_size_matches() {
    let path = data_dir().join("dict_common.tsv");
    let mut dict = chaoset_engine::Dictionary::new();
    let loaded = dict.load_tsv(&path).expect("Rust 引擎必须能加载内置词库");
    assert!(loaded >= 60_000, "运行时加载词条数异常偏少: {loaded}");
    assert_eq!(
        chaoset_engine::PAGE_SIZE,
        6,
        "每页候选数契约（三端壳数字键 1-6）"
    );
}
