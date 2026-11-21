//! 用户词典加载契约（与 Go engine/userdict_recover_test.go、userdict_test.go 同语义）：
//! 旧扁平格式迁移到 canonical 且不丢数据；损坏 JSON 改名 <path>.corrupt-* 后以
//! 空词典继续；读取失败（权限/路径是目录）仍返回 Err；正常文件无警告。

use std::fs;
use std::path::{Path, PathBuf};

use chaoset_engine::userdict::UserDict;
use tempfile::TempDir;

fn userdict_path(dir: &TempDir) -> PathBuf {
    dir.path().join("userdict.json")
}

fn corrupt_backups(path: &Path) -> Vec<PathBuf> {
    let parent = path.parent().unwrap();
    let prefix = format!("{}.corrupt-", path.file_name().unwrap().to_string_lossy());
    let mut found: Vec<PathBuf> = fs::read_dir(parent)
        .unwrap()
        .filter_map(|entry| entry.ok().map(|e| e.path()))
        .filter(|p| {
            p.file_name()
                .map(|name| name.to_string_lossy().starts_with(&prefix))
                .unwrap_or(false)
        })
        .collect();
    found.sort();
    found
}

#[test]
fn legacy_flat_format_is_migrated_without_data_loss() {
    let dir = TempDir::new().unwrap();
    let path = userdict_path(&dir);
    // 阶段 0 扁平格式：顶层就是「词\u0000音节」→ 记录。
    fs::write(
        &path,
        r#"{"你好\u0000ni hao": {"count": 2, "last_used": 100}}"#,
    )
    .unwrap();

    let (dict, warnings) = UserDict::open(Some(&path)).unwrap();
    assert!(warnings.is_empty());
    assert_eq!(dict.stats().words, 1);
    assert!(dict.is_dirty(), "迁移后应标脏，等下一次落盘升级格式");
    // last_used=100（1970 年）→ 衰减 0.1：2×500×0.1 = 100
    let out = dict.export_tsv(false);
    assert!(
        out.contains("你好\tni hao\t2\t1970-01-01T00:01:40Z"),
        "迁移后音节应保留: {out}"
    );

    // 再次落盘应升级为新格式（含 phrases 键、words 以词为键且无 NUL）
    dict.save().unwrap();
    let data = fs::read_to_string(&path).unwrap();
    let value: serde_json::Value = serde_json::from_str(&data).unwrap();
    assert!(value.get("phrases").is_some(), "保存后应含 phrases 键");
    let words = value.get("words").unwrap().as_object().unwrap();
    assert!(words.contains_key("你好"), "words 应以词为键: {words:?}");
    assert!(
        !words.keys().any(|k| k.contains('\u{0}')),
        "words 键不应残留 NUL: {words:?}"
    );
}

#[test]
fn go_canonical_keys_are_migrated() {
    let dir = TempDir::new().unwrap();
    let path = userdict_path(&dir);
    // Go daemon 的「新格式」：words/phrases 键是「词\0音节」、bigrams 键是「前词\0后词」。
    let json = r#"{
      "words": {"你好\u0000ni hao": {"count": 2, "last_used": 1700000000}},
      "phrases": {"单位\u0000dan wei": {"word": "单位", "sylls": "dan wei", "count": 1, "last_used": 1700000000}},
      "bigrams": {"你好\u0000世界": {"count": 1, "last_used": 1700000000}}
    }"#;
    fs::write(&path, json).unwrap();

    let (dict, warnings) = UserDict::open(Some(&path)).unwrap();
    assert!(warnings.is_empty());
    let stats = dict.stats();
    assert_eq!((stats.words, stats.phrases, stats.bigrams), (1, 1, 1));
    assert!(dict.is_dirty());
    let out = dict.export_tsv(false);
    assert!(
        out.contains("你好\tni hao\t2\t"),
        "words 音节应从键迁移: {out}"
    );
    assert!(
        out.contains("单位\tdan wei\t1\t"),
        "phrases 应迁移到以词为键: {out}"
    );
    assert!(
        out.contains("你好\t世界\t1"),
        "bigrams 键应迁移为 前词->后词: {out}"
    );
}

#[test]
fn corrupt_file_is_renamed_and_dict_is_empty() {
    let dir = TempDir::new().unwrap();
    let path = userdict_path(&dir);
    let corrupt = "{ 这不是 JSON 正文，模拟断电写坏的文件 ".as_bytes();
    fs::write(&path, corrupt).unwrap();

    let (dict, warnings) = UserDict::open(Some(&path)).unwrap();
    assert_eq!(warnings.len(), 1, "应返回一条警告供调用方记日志");
    assert!(
        warnings[0].contains("解析失败"),
        "警告应说明解析失败: {warnings:?}"
    );
    assert!(
        warnings[0].contains(".corrupt-"),
        "警告应给出备份路径: {warnings:?}"
    );
    let stats = dict.stats();
    assert_eq!(
        (stats.words, stats.phrases, stats.bigrams),
        (0, 0, 0),
        "损坏恢复后应为空词典"
    );

    // 损坏文件必须改名保留现场、内容原样，且原路径已不存在
    let backups = corrupt_backups(&path);
    assert_eq!(
        backups.len(),
        1,
        "应恰好生成一个 .corrupt-* 备份: {backups:?}"
    );
    assert_eq!(fs::read(&backups[0]).unwrap(), corrupt.to_vec());
    assert!(!path.exists(), "原路径应已被移走");

    // 恢复出的词典必须能正常写入学习数据并落盘
    let mut restored = dict;
    restored.bump_word("你好", Some("ni hao"), None);
    restored.save().unwrap();
    let (reloaded, warnings) = UserDict::open(Some(&path)).unwrap();
    assert!(warnings.is_empty());
    assert_eq!(reloaded.stats().words, 1);
}

#[test]
fn legacy_constructor_new_recovers_corrupt_file_without_panicking() {
    let dir = TempDir::new().unwrap();
    let path = userdict_path(&dir);
    fs::write(&path, b"not json").unwrap();
    // new 是兼容入口（daemon 用）：损坏文件降级为空词典，不 panic/不退出。
    let dict = UserDict::new(Some(&path));
    assert_eq!(dict.stats().words, 0);
    assert!(!path.exists(), "损坏文件应已改名");
    assert_eq!(corrupt_backups(&path).len(), 1);
}

#[test]
fn io_errors_still_fail() {
    let dir = TempDir::new().unwrap();
    let path = userdict_path(&dir);
    // 路径是目录：读取失败（不是 JSON 解析问题），改名解决不了，必须报错。
    fs::create_dir(&path).unwrap();
    let err = UserDict::open(Some(&path)).err();
    assert!(err.is_some(), "读取失败必须返回 Err");
}

#[test]
fn valid_file_loads_without_warning() {
    let dir = TempDir::new().unwrap();
    let path = userdict_path(&dir);
    fs::write(
        &path,
        r#"{"words": {"世界": {"count": 1, "last_used": 1700000000, "sylls": "shi jie"}},"phrases": {},"bigrams": {}}"#,
    )
    .unwrap();
    let (dict, warnings) = UserDict::open(Some(&path)).unwrap();
    assert!(warnings.is_empty(), "正常文件不应产生警告: {warnings:?}");
    assert_eq!(dict.stats().words, 1);
    assert!(!dict.is_dirty(), "canonical 文件不应标脏");
}

#[test]
fn missing_file_is_empty_dict_without_warning() {
    let dir = TempDir::new().unwrap();
    let path = userdict_path(&dir);
    let (dict, warnings) = UserDict::open(Some(&path)).unwrap();
    assert!(warnings.is_empty());
    assert_eq!(dict.stats().words, 0);
    assert!(!dict.is_dirty());
}

#[test]
fn non_object_json_is_treated_as_corrupt() {
    let dir = TempDir::new().unwrap();
    let path = userdict_path(&dir);
    fs::write(&path, b"[1, 2, 3]").unwrap();
    let (dict, warnings) = UserDict::open(Some(&path)).unwrap();
    assert_eq!(warnings.len(), 1);
    assert_eq!(dict.stats().words, 0);
    assert_eq!(corrupt_backups(&path).len(), 1);
}
