//! 用户词典删除语义（与 Go engine/userdict_delete_test.go 同语义）：
//! 有音节先精确删 Words+Phrases；未命中再按词文本兜底删所有读音；Bigrams 刻意
//! 保留；只有真的删到记录才标脏；立即模式/延迟落盘都能在重载后生效。

use std::fs;
use std::path::PathBuf;

use chaoset_engine::userdict::UserDict;
use tempfile::TempDir;

fn write_fixture(dir: &TempDir, json: &str) -> PathBuf {
    let path = dir.path().join("userdict.json");
    fs::write(&path, json).unwrap();
    path
}

fn open_fixture(dir: &TempDir, json: &str) -> (UserDict, PathBuf) {
    let path = write_fixture(dir, json);
    let (dict, warnings) = UserDict::open(Some(&path)).unwrap();
    assert!(warnings.is_empty(), "正常文件不应有警告: {warnings:?}");
    (dict, path)
}

/// 复查用：重新打开文件（验证落盘结果，而不是只看内存态）。
fn reload(path: &PathBuf) -> UserDict {
    let (dict, warnings) = UserDict::open(Some(path)).unwrap();
    assert!(warnings.is_empty());
    dict
}

const BASE_JSON: &str = r#"{
  "words": {
    "今天": {"count": 2, "last_used": 1700000000, "sylls": "jin tian"},
    "你好": {"count": 1, "last_used": 1700000000, "sylls": "ni hao"}
  },
  "phrases": {
    "单位": {"word": "单位", "sylls": "dan wei", "count": 2, "last_used": 1700000000},
    "今天天气": {"word": "今天天气", "sylls": "jin tian tian qi", "count": 1, "last_used": 1700000000}
  },
  "bigrams": {
    "你好->今天": {"count": 2, "last_used": 1700000000}
  }
}"#;

#[test]
fn delete_word_removes_words_and_phrases() {
    let dir = TempDir::new().unwrap();
    let (dict, path) = open_fixture(&dir, BASE_JSON);
    assert!(!dict.is_dirty());

    assert!(dict.delete_word("今天", &["jin".to_string(), "tian".to_string()]));
    assert!(dict.is_dirty(), "真删到记录必须标脏");
    let after = dict.stats();
    assert_eq!(after.words, 1);
    assert_eq!(after.phrases, 2);

    dict.save().unwrap();
    let reloaded = reload(&path);
    assert_eq!(reloaded.stats().words, 1);
    assert!(
        !reloaded.export_tsv(false).contains("今天\tjin tian"),
        "重载后「今天」仍存在"
    );
    assert!(reloaded.export_tsv(false).contains("你好\tni hao"));
}

#[test]
fn delete_exact_phrase() {
    let dir = TempDir::new().unwrap();
    let (dict, _path) = open_fixture(&dir, BASE_JSON);
    assert!(dict.delete_word("单位", &["dan".to_string(), "wei".to_string()]));
    assert_eq!(dict.stats().phrases, 1);
    assert!(!dict.export_tsv(false).contains("单位\tdan wei"));
}

#[test]
fn delete_missing_is_idempotent_and_does_not_mark_dirty() {
    let dir = TempDir::new().unwrap();
    let (dict, _path) = open_fixture(&dir, BASE_JSON);
    assert!(!dict.is_dirty());

    assert!(!dict.delete_word("没学过", &[]));
    assert!(!dict.is_dirty(), "未命中不能标脏");

    assert!(!dict.delete_word("没学过", &["mei".to_string()]));
    assert!(!dict.is_dirty(), "精确未命中且兜底未命中也不能标脏");

    // 重复删除已删过的词：返回 false 且不标脏
    assert!(dict.delete_word("你好", &[]));
    assert!(dict.is_dirty());
    dict.save().unwrap();
    assert!(!dict.is_dirty());
    assert!(!dict.delete_word("你好", &[]));
    assert!(!dict.is_dirty(), "重复删除不应再次标脏");
}

#[test]
fn delete_falls_back_to_word_when_syllables_miss() {
    let dir = TempDir::new().unwrap();
    // 「长」只学过 zhang 读音；调用方按 chang（词典读音）请求删除。
    let json = r#"{
      "words": {"长": {"count": 1, "last_used": 1700000000, "sylls": "zhang"}},
      "phrases": {"长": {"word": "长", "sylls": "chang du", "count": 2, "last_used": 1700000000}},
      "bigrams": {}
    }"#;
    let (dict, _path) = open_fixture(&dir, json);
    assert!(dict.delete_word("长", &["chang".to_string()]));
    let stats = dict.stats();
    assert_eq!(stats.words, 0, "兜底应删掉同词的其它读音记录");
    assert_eq!(stats.phrases, 0, "兜底应删掉同词的词组记录");
}

#[test]
fn exact_hit_on_words_does_not_fall_back_to_phrases() {
    let dir = TempDir::new().unwrap();
    // 词表精确命中（长/chang）后不再按词兜底，词组里的「长」保留——与 Go 同语义。
    let json = r#"{
      "words": {"长": {"count": 1, "last_used": 1700000000, "sylls": "chang"}},
      "phrases": {"长": {"word": "长", "sylls": "zhang", "count": 2, "last_used": 1700000000}},
      "bigrams": {}
    }"#;
    let (dict, _path) = open_fixture(&dir, json);
    assert!(dict.delete_word("长", &["chang".to_string()]));
    let stats = dict.stats();
    assert_eq!(stats.words, 0);
    assert_eq!(stats.phrases, 1, "精确命中后不应触发按词兜底");
}

#[test]
fn delete_keeps_bigrams_on_purpose() {
    let dir = TempDir::new().unwrap();
    let (dict, path) = open_fixture(&dir, BASE_JSON);
    assert!(dict.delete_word("今天", &["jin".to_string(), "tian".to_string()]));
    dict.save().unwrap();

    let reloaded = reload(&path);
    assert_eq!(reloaded.stats().bigrams, 1, "Bigrams 刻意保留");
    let out = reloaded.export_tsv(false);
    assert!(
        out.contains("你好\t今天\t2"),
        "删除词不应删上下文成对统计: {out}"
    );
}

#[test]
fn delete_sentence_entry_with_segs() {
    let dir = TempDir::new().unwrap();
    let json = r#"{
      "words": {"今天星期一": {"count": 2, "last_used": 1700000000, "segs": ["今天", "星期一"], "sylls": "jin tian xing qi yi"}},
      "phrases": {},
      "bigrams": {}
    }"#;
    let (dict, _path) = open_fixture(&dir, json);
    let sylls: Vec<String> = ["jin", "tian", "xing", "qi", "yi"]
        .iter()
        .map(|s| s.to_string())
        .collect();
    assert!(dict.delete_word("今天星期一", &sylls));
    assert_eq!(dict.stats().words, 0);
}

#[test]
fn delete_empty_word_is_rejected_without_changes() {
    let dir = TempDir::new().unwrap();
    let (dict, _path) = open_fixture(&dir, BASE_JSON);
    assert!(!dict.delete_word("", &[]));
    assert!(!dict.delete_word("", &["ni".to_string()]));
    assert!(!dict.is_dirty());
    assert_eq!(dict.stats().words, 2);
}

#[test]
fn deferred_mode_delete_only_dirty_then_flush_persists() {
    let dir = TempDir::new().unwrap();
    let (dict, path) = open_fixture(&dir, BASE_JSON);
    // 无落盘触发时删除只标脏（引擎不自动写盘，由 daemon ticker / CLI 显式 save）。
    assert!(dict.delete_word("今天天气", &[]));
    let before = fs::read(&path).unwrap();
    assert!(dict.is_dirty(), "删除后应标脏等待 flush");
    assert!(std::str::from_utf8(&before).unwrap().contains("今天天气"));
    assert!(dict.flush_if_dirty().unwrap());
    let reloaded = reload(&path);
    assert!(!reloaded.export_tsv(false).contains("今天天气"));
    assert!(!dict.is_dirty());
}
