//! 用户词典导出契约（与 Go engine/userdict_export_test.go 同语义）：
//! 全量导出 4 列（词 / 拼音 / 次数 / RFC3339 UTC）、phrases_only 无段头 3 列、
//! 排序次数降序 → 词升序、空段省略、拼音列取记录 syllables 而不是整句分段。

use std::fs;
use std::path::PathBuf;

use chaoset_engine::userdict::{format_rfc3339_utc, UserDict};
use tempfile::TempDir;

/// 2025-09-24T07:46:40Z：固定时间让导出逐字节可断言。
const FIXED_LAST_USED: i64 = 1_758_700_000;
const FIXED_RFC3339: &str = "2025-09-24T07:46:40Z";

/// JSON 夹具里用 $TS 占位 last_used（避免 format! 与 JSON 花括号互相转义）。
fn ts_fixture(raw: &str) -> String {
    raw.replace("$TS", &FIXED_LAST_USED.to_string())
}

fn open_fixture(dir: &TempDir, raw: &str) -> UserDict {
    let path: PathBuf = dir.path().join("userdict.json");
    fs::write(&path, ts_fixture(raw)).unwrap();
    let (dict, warnings) = UserDict::open(Some(&path)).unwrap();
    assert!(warnings.is_empty(), "正常文件不应有警告: {warnings:?}");
    dict
}

/// 标准夹具：你好×3、世界×1、bigram(你好→世界)×2、词组「键盘鼠标」×2。
const CANONICAL_FIXTURE: &str = r#"{"words":{"你好":{"count":3,"last_used":$TS,"sylls":"ni hao"},"世界":{"count":1,"last_used":$TS,"sylls":"shi jie"}},"phrases":{"键盘鼠标":{"word":"键盘鼠标","sylls":"jian pan shu biao","count":2,"last_used":$TS}},"bigrams":{"你好->世界":{"count":2,"last_used":$TS}}}"#;

#[test]
fn rfc3339_utc_formatting() {
    assert_eq!(format_rfc3339_utc(0), "1970-01-01T00:00:00Z");
    assert_eq!(format_rfc3339_utc(1_000_000_000), "2001-09-09T01:46:40Z");
    assert_eq!(format_rfc3339_utc(FIXED_LAST_USED), FIXED_RFC3339);
    // 1970 前（负时间戳）按 UTC 正常换算，不 panic 也不回退成 0。
    assert_eq!(format_rfc3339_utc(-1), "1969-12-31T23:59:59Z");
    assert_eq!(format_rfc3339_utc(-86_400), "1969-12-31T00:00:00Z");
}

#[test]
fn full_export_is_byte_exact() {
    let dir = TempDir::new().unwrap();
    let dict = open_fixture(&dir, CANONICAL_FIXTURE);
    let expected = format!(
        "# 词\t拼音\t次数\t最近使用\n\
         你好\tni hao\t3\t{FIXED_RFC3339}\n\
         世界\tshi jie\t1\t{FIXED_RFC3339}\n\
         # 词组\t拼音\t次数\t最近使用\n\
         键盘鼠标\tjian pan shu biao\t2\t{FIXED_RFC3339}\n\
         # 上下文\t前词\t后词\t次数\n\
         你好\t世界\t2\n"
    );
    assert_eq!(dict.export_tsv(false), expected);
}

#[test]
fn phrases_only_export_has_no_header_and_three_columns() {
    let dir = TempDir::new().unwrap();
    let dict = open_fixture(&dir, CANONICAL_FIXTURE);
    let out = dict.export_tsv(true);
    assert_eq!(out, "键盘鼠标\tjian pan shu biao\t2\n");
    assert!(!out.contains('#'), "phrases_only 不应有段头: {out}");
    assert!(!out.contains("你好"), "phrases_only 不应含词段: {out}");
    assert_eq!(out.lines().count(), 1);
    for line in out.lines() {
        assert_eq!(line.split('\t').count(), 3, "应为三列: {line}");
    }
}

#[test]
fn empty_dict_exports_nothing() {
    let dict = UserDict::new::<&str>(None);
    assert_eq!(dict.export_tsv(false), "");
    assert_eq!(dict.export_tsv(true), "");
}

#[test]
fn sections_are_omitted_when_empty() {
    let dir = TempDir::new().unwrap();
    let dict = open_fixture(
        &dir,
        r#"{"words":{"只有词":{"count":1,"last_used":$TS,"sylls":"zhi you ci"}},"phrases":{},"bigrams":{}}"#,
    );
    let out = dict.export_tsv(false);
    assert_eq!(
        out,
        format!("# 词\t拼音\t次数\t最近使用\n只有词\tzhi you ci\t1\t{FIXED_RFC3339}\n")
    );
}

#[test]
fn words_sorted_by_count_desc_then_word_bytes() {
    let dir = TempDir::new().unwrap();
    let dict = open_fixture(
        &dir,
        r#"{"words":{"b":{"count":2,"last_used":$TS,"sylls":"bi"},"a":{"count":2,"last_used":$TS,"sylls":"a"},"ab":{"count":2,"last_used":$TS,"sylls":"a b"},"z":{"count":2,"last_used":$TS,"sylls":"zi"},"c":{"count":1,"last_used":$TS,"sylls":"ci"}},"phrases":{},"bigrams":{}}"#,
    );
    let out = dict.export_tsv(false);
    let rows: Vec<&str> = out.lines().skip(1).collect();
    assert_eq!(rows.len(), 5);
    assert!(rows[0].starts_with("a\t"), "同次数按词字节序: {rows:?}");
    assert!(rows[1].starts_with("ab\t"));
    assert!(rows[2].starts_with("b\t"));
    assert!(rows[3].starts_with("z\t"));
    assert!(
        rows[4].starts_with("c\t"),
        "次数降序最后才是 count=1: {rows:?}"
    );
}

#[test]
fn phrases_sorted_by_count_desc_then_word() {
    let dir = TempDir::new().unwrap();
    let dict = open_fixture(
        &dir,
        r#"{"words":{},"phrases":{"乙组":{"word":"乙组","sylls":"yi zu","count":1,"last_used":$TS},"甲组":{"word":"甲组","sylls":"jia zu","count":2,"last_used":$TS},"丙组":{"word":"丙组","sylls":"bing zu","count":1,"last_used":$TS}},"bigrams":{}}"#,
    );
    let out = dict.export_tsv(false);
    let rows: Vec<&str> = out.lines().skip(1).collect(); // 跳过词组段头（词段为空不输出）
    assert!(rows[0].starts_with("甲组\t"), "次数降序: {rows:?}");
    // count=1 的两条按词 UTF-8 字节序：丙组 < 乙组
    assert!(rows[1].starts_with("丙组\t"), "同次数按词字节序: {rows:?}");
    assert!(rows[2].starts_with("乙组\t"));
}

#[test]
fn bigram_sorted_by_count_desc_then_pair() {
    let dir = TempDir::new().unwrap();
    let dict = open_fixture(
        &dir,
        r#"{"words":{},"phrases":{},"bigrams":{"b->c":{"count":1,"last_used":$TS},"a->z":{"count":1,"last_used":$TS},"丙->丁":{"count":2,"last_used":$TS}}}"#,
    );
    let out = dict.export_tsv(false);
    let rows: Vec<&str> = out.lines().skip(1).collect(); // 跳过上下文段头（词/词组段为空）
    assert_eq!(rows[0], "丙\t丁\t2");
    assert_eq!(rows[1], "a\tz\t1");
    assert_eq!(rows[2], "b\tc\t1");
}

#[test]
fn pinyin_column_uses_sylls_not_segs() {
    let dir = TempDir::new().unwrap();
    // 整句记录同时带 segs（分段词文本）与 sylls；导出拼音列必须取 sylls。
    let dict = open_fixture(
        &dir,
        r#"{"words":{"今天星期一":{"count":1,"last_used":$TS,"segs":["今天","星期一"],"sylls":"jin tian xing qi yi"}},"phrases":{},"bigrams":{}}"#,
    );
    let out = dict.export_tsv(false);
    assert!(
        out.contains("今天星期一\tjin tian xing qi yi\t1\t"),
        "拼音列必须取 sylls: {out}"
    );
    assert!(!out.contains("今天 星期一"), "不能把 segs 当拼音列: {out}");
}

#[test]
fn pinyin_separator_is_normalized_to_spaces() {
    let dir = TempDir::new().unwrap();
    // daemon 学习路径可能存 ' 连接；导出统一成空格，方便再导入。
    let dict = open_fixture(
        &dir,
        r#"{"words":{"测试":{"count":1,"last_used":$TS,"sylls":"ce'shi"}},"phrases":{},"bigrams":{}}"#,
    );
    assert!(dict.export_tsv(false).contains("测试\tce shi\t1\t"));
}

#[test]
fn stats_counts_three_tables() {
    let dir = TempDir::new().unwrap();
    let dict = open_fixture(&dir, CANONICAL_FIXTURE);
    let stats = dict.stats();
    assert_eq!((stats.words, stats.phrases, stats.bigrams), (2, 1, 1));
}
