//! chaoset-userdict 本地文件模式契约（移植 Go cmd/chaoset-userdict/main_test.go +
//! internal/tools/userdict/local.go 语义）：stats/clear/delete/export 文案逐字、
//! 导出 4 列/3 列金标、原子写、旧格式迁移、损坏改名与退出码。

use std::fs;
use std::path::{Path, PathBuf};

use chaoset_tools::userdict::{run_local, run_local_cli, run_local_with_stderr, split_syllables};
use tempfile::TempDir;

const FIXED_TS: i64 = 1_758_700_000;
const FIXED_RFC3339: &str = "2025-09-24T07:46:40Z";

fn ts_fixture(raw: &str) -> String {
    raw.replace("$TS", &FIXED_TS.to_string())
}

fn write_fixture(dir: &TempDir, raw: &str) -> PathBuf {
    let path = dir.path().join("userdict.json");
    fs::write(&path, ts_fixture(raw)).unwrap();
    path
}

/// 标准夹具：你好×3、世界×1、bigram(你好→世界)×2、词组「键盘鼠标」×2。
const CANONICAL_FIXTURE: &str = r#"{"words":{"你好":{"count":3,"last_used":$TS,"sylls":"ni hao"},"世界":{"count":1,"last_used":$TS,"sylls":"shi jie"}},"phrases":{"键盘鼠标":{"word":"键盘鼠标","sylls":"jian pan shu biao","count":2,"last_used":$TS}},"bigrams":{"你好->世界":{"count":2,"last_used":$TS}}}"#;

fn sylls(items: &[&str]) -> Vec<String> {
    items.iter().map(|s| s.to_string()).collect()
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

fn temp_userdict_files(dir: &Path) -> Vec<PathBuf> {
    fs::read_dir(dir)
        .unwrap()
        .filter_map(|entry| entry.ok().map(|e| e.path()))
        .filter(|p| {
            p.file_name()
                .map(|name| name.to_string_lossy().starts_with(".chaoset-userdict-"))
                .unwrap_or(false)
        })
        .collect()
}

#[test]
fn stats_prints_exact_counts() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "stats", false, None, "", &[], &mut stdout).unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "词 2 条 / 词组 1 条 / 上下文 1 条\n"
    );
}

#[test]
fn clear_prints_before_counts_and_empties_file() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "clear", false, None, "", &[], &mut stdout).unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "已清空：词 2 条、词组 1 条、上下文 1 条\n"
    );

    // 清空必须立即落盘：重新读文件，三张表都为空
    let data = fs::read_to_string(&path).unwrap();
    let value: serde_json::Value = serde_json::from_str(&data).unwrap();
    for table in ["words", "phrases", "bigrams"] {
        let map = value.get(table).unwrap().as_object().unwrap();
        assert!(map.is_empty(), "{table} 清空后应为空: {map:?}");
    }

    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "stats", false, None, "", &[], &mut stdout).unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "词 0 条 / 词组 0 条 / 上下文 0 条\n"
    );
}

#[test]
fn delete_with_syllables_is_exact_and_persists() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let mut stdout: Vec<u8> = Vec::new();
    run_local(
        &path,
        "delete",
        false,
        None,
        "你好",
        &sylls(&["ni", "hao"]),
        &mut stdout,
    )
    .unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "已删除「你好」的学习记录\n"
    );

    // 立即落盘：重载后词与 bigram 语义（bigram 刻意保留）
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "stats", false, None, "", &[], &mut stdout).unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "词 1 条 / 词组 1 条 / 上下文 1 条\n",
        "bigram 刻意保留"
    );
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "export", false, None, "", &[], &mut stdout).unwrap();
    let exported = String::from_utf8(stdout).unwrap();
    assert!(!exported.contains("你好\tni hao"), "删除后导出不应再有该词");
    assert!(exported.contains("世界\tshi jie"));
    assert!(exported.contains("你好\t世界\t2"), "bigram 保留");
}

#[test]
fn delete_missing_reports_not_found() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "delete", false, None, "没学过", &[], &mut stdout).unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "未找到「没学过」的学习记录（可能未学习过或已删除）\n"
    );
}

#[test]
fn delete_without_syllables_falls_back_to_word() {
    let dir = TempDir::new().unwrap();
    // 词表只学过 zhang 读音；按 chang（词典读音）请求删除时应兜底删同词记录。
    let path = write_fixture(
        &dir,
        r#"{"words":{"长":{"count":1,"last_used":$TS,"sylls":"zhang"}},"phrases":{"长":{"word":"长","sylls":"chang du","count":2,"last_used":$TS}},"bigrams":{}}"#,
    );
    let mut stdout: Vec<u8> = Vec::new();
    run_local(
        &path,
        "delete",
        false,
        None,
        "长",
        &sylls(&["chang"]),
        &mut stdout,
    )
    .unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "已删除「长」的学习记录\n"
    );
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "stats", false, None, "", &[], &mut stdout).unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "词 0 条 / 词组 0 条 / 上下文 0 条\n"
    );
}

#[test]
fn delete_empty_word_is_error() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let mut stdout: Vec<u8> = Vec::new();
    let err = run_local(&path, "delete", false, None, "", &[], &mut stdout).unwrap_err();
    assert_eq!(err, "待删除的词不能为空");
    assert!(stdout.is_empty());
}

#[test]
fn export_full_is_byte_exact() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "export", false, None, "", &[], &mut stdout).unwrap();
    let expected = format!(
        "# 词\t拼音\t次数\t最近使用\n\
         你好\tni hao\t3\t{FIXED_RFC3339}\n\
         世界\tshi jie\t1\t{FIXED_RFC3339}\n\
         # 词组\t拼音\t次数\t最近使用\n\
         键盘鼠标\tjian pan shu biao\t2\t{FIXED_RFC3339}\n\
         # 上下文\t前词\t后词\t次数\n\
         你好\t世界\t2\n"
    );
    assert_eq!(String::from_utf8(stdout).unwrap(), expected);
}

#[test]
fn export_phrases_only_three_columns_without_header() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "export", true, None, "", &[], &mut stdout).unwrap();
    let out = String::from_utf8(stdout).unwrap();
    assert_eq!(out, "键盘鼠标\tjian pan shu biao\t2\n");
    assert!(!out.contains('#'));
}

#[test]
fn export_to_file_is_atomic_and_0644() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let target = dir.path().join("export.tsv");
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "export", true, Some(&target), "", &[], &mut stdout).unwrap();
    assert!(stdout.is_empty(), "-o 写文件时不应再打 stdout");
    assert_eq!(
        fs::read_to_string(&target).unwrap(),
        "键盘鼠标\tjian pan shu biao\t2\n"
    );
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mode = fs::metadata(&target).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode, 0o644, "导出文件权限应为 0644");
    }
    assert!(
        temp_userdict_files(dir.path()).is_empty(),
        "不应残留导出临时文件"
    );
}

#[test]
fn export_to_missing_parent_fails_and_does_not_create_it() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let target = dir.path().join("no-such-dir").join("export.tsv");
    let mut stdout: Vec<u8> = Vec::new();
    let err = run_local(&path, "export", true, Some(&target), "", &[], &mut stdout).unwrap_err();
    assert!(err.starts_with("创建临时文件失败"), "err={err}");
    assert!(!dir.path().join("no-such-dir").exists(), "不创建父目录");
}

#[test]
fn export_failure_keeps_old_target_untouched() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    // 目标是一个目录：临时文件能建、rename 必失败 → 错误文案 + 目录保持原样。
    let target = dir.path().join("target-dir");
    fs::create_dir(&target).unwrap();
    fs::write(target.join("keep.txt"), "keep").unwrap();
    let mut stdout: Vec<u8> = Vec::new();
    let err = run_local(&path, "export", true, Some(&target), "", &[], &mut stdout).unwrap_err();
    assert!(
        err.contains("替换导出文件失败（旧文件保持不变）"),
        "err={err}"
    );
    assert!(target.is_dir(), "旧目标应保持原样");
    assert_eq!(fs::read_to_string(target.join("keep.txt")).unwrap(), "keep");
}

#[test]
fn legacy_flat_format_is_migrated_on_write() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("userdict.json");
    // 旧扁平格式：顶层就是「词\u0000音节」→ 记录。
    fs::write(
        &path,
        r#"{"你好\u0000ni hao": {"count": 2, "last_used": 100}}"#,
    )
    .unwrap();

    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "stats", false, None, "", &[], &mut stdout).unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "词 1 条 / 词组 0 条 / 上下文 0 条\n"
    );

    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "export", false, None, "", &[], &mut stdout).unwrap();
    assert!(
        String::from_utf8(stdout)
            .unwrap()
            .contains("你好\tni hao\t2\t1970-01-01T00:01:40Z"),
        "旧格式迁移后拼音列应保留"
    );

    // 触发一次落盘（删除）后文件应升级为 canonical（含 phrases 键、words 键无 NUL）
    let mut stdout: Vec<u8> = Vec::new();
    run_local(&path, "delete", false, None, "你好", &[], &mut stdout).unwrap();
    let data = fs::read_to_string(&path).unwrap();
    let value: serde_json::Value = serde_json::from_str(&data).unwrap();
    assert!(value.get("phrases").is_some(), "落盘后应为 canonical 格式");
    let words = value.get("words").unwrap().as_object().unwrap();
    assert!(
        !words.keys().any(|k| k.contains('\u{0}')),
        "words 键不应残留 NUL"
    );
}

#[test]
fn corrupt_file_is_renamed_and_warned() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("userdict.json");
    let corrupt = "{ 这不是 JSON 正文 ";
    fs::write(&path, corrupt).unwrap();

    let mut stdout: Vec<u8> = Vec::new();
    let mut stderr: Vec<u8> = Vec::new();
    run_local_with_stderr(
        &path,
        "stats",
        false,
        None,
        "",
        &[],
        &mut stdout,
        &mut stderr,
    )
    .unwrap();
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "词 0 条 / 词组 0 条 / 上下文 0 条\n",
        "损坏后以空词典继续"
    );
    let err_text = String::from_utf8(stderr).unwrap();
    assert!(err_text.contains("警告:"), "应打印警告: {err_text}");
    assert!(
        err_text.contains("解析失败"),
        "警告应说明解析失败: {err_text}"
    );
    assert!(
        err_text.contains(".corrupt-"),
        "警告应给出备份路径: {err_text}"
    );
    assert!(!path.exists(), "损坏文件应已改名");
    let backups = corrupt_backups(&path);
    assert_eq!(backups.len(), 1);
    assert_eq!(fs::read_to_string(&backups[0]).unwrap(), corrupt);
}

#[test]
fn io_error_still_fails() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("userdict.json");
    fs::create_dir(&path).unwrap();
    let mut stdout: Vec<u8> = Vec::new();
    let mut stderr: Vec<u8> = Vec::new();
    let err = run_local_with_stderr(
        &path,
        "stats",
        false,
        None,
        "",
        &[],
        &mut stdout,
        &mut stderr,
    )
    .unwrap_err();
    assert!(!err.is_empty());
}

#[test]
fn cli_exit_codes_and_usage() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let run_cli = |args: &[&str]| {
        let args: Vec<String> = args.iter().map(|s| s.to_string()).collect();
        let mut stdout: Vec<u8> = Vec::new();
        let mut stderr: Vec<u8> = Vec::new();
        let code = run_local_cli(&path, &args, &mut stdout, &mut stderr);
        (
            code,
            String::from_utf8(stdout).unwrap(),
            String::from_utf8(stderr).unwrap(),
        )
    };

    let (code, stdout, stderr) = run_cli(&[]);
    assert_eq!(code, 2);
    assert!(stdout.is_empty());
    assert!(stderr.contains("stats|clear|delete"));

    let (code, stdout, _) = run_cli(&["-h"]);
    assert_eq!(code, 0);
    assert!(stdout.contains("用法: chaoset userdict"));

    let (code, _, stderr) = run_cli(&["bogus"]);
    assert_eq!(code, 2);
    assert!(stderr.contains("用法: chaoset userdict"));

    let (code, _, stderr) = run_cli(&["delete"]);
    assert_eq!(code, 2);
    assert!(stderr.contains("delete 词 [音节...]"));

    let (code, _, stderr) = run_cli(&["export", "-bogus"]);
    assert_eq!(code, 2);
    assert!(stderr.contains("flag provided but not defined: -bogus"));

    let (code, stdout, _) = run_cli(&["export", "-h"]);
    assert_eq!(code, 0);
    assert!(stdout.contains("用法: chaoset userdict"));

    let (code, stdout, stderr) = run_cli(&["stats"]);
    assert_eq!(code, 0);
    assert_eq!(stdout, "词 2 条 / 词组 1 条 / 上下文 1 条\n");
    assert!(stderr.is_empty());

    // 运行期错误 → 退出 1，错误写 stderr
    fs::remove_file(&path).unwrap();
    fs::create_dir(&path).unwrap();
    let (code, _, stderr) = run_cli(&["stats"]);
    assert_eq!(code, 1);
    assert!(!stderr.is_empty());
}

#[test]
fn cli_delete_and_export_flags() {
    let dir = TempDir::new().unwrap();
    let path = write_fixture(&dir, CANONICAL_FIXTURE);
    let target = dir.path().join("learned.tsv");

    let args: Vec<String> = vec![
        "export".into(),
        "-phrases".into(),
        "-o".into(),
        target.to_string_lossy().to_string(),
    ];
    let mut stdout: Vec<u8> = Vec::new();
    let mut stderr: Vec<u8> = Vec::new();
    let code = run_local_cli(&path, &args, &mut stdout, &mut stderr);
    assert_eq!(code, 0, "stderr={:?}", String::from_utf8_lossy(&stderr));
    assert_eq!(
        fs::read_to_string(&target).unwrap(),
        "键盘鼠标\tjian pan shu biao\t2\n"
    );

    // delete 的音节接受空格与单引号混写
    let args: Vec<String> = vec!["delete".into(), "你好".into(), "ni'".into(), "hao".into()];
    let mut stdout: Vec<u8> = Vec::new();
    let mut stderr: Vec<u8> = Vec::new();
    let code = run_local_cli(&path, &args, &mut stdout, &mut stderr);
    assert_eq!(code, 0, "stderr={:?}", String::from_utf8_lossy(&stderr));
    assert_eq!(
        String::from_utf8(stdout).unwrap(),
        "已删除「你好」的学习记录\n"
    );
}

#[test]
fn split_syllables_accepts_space_and_apostrophe() {
    assert_eq!(split_syllables(""), Vec::<String>::new());
    assert_eq!(split_syllables("jin tian"), vec!["jin", "tian"]);
    assert_eq!(split_syllables("jin'tian"), vec!["jin", "tian"]);
    assert_eq!(split_syllables(" jin  'tian "), vec!["jin", "tian"]);
}
