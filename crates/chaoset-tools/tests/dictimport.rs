//! chaoset-import 契约（移植 Go cmd/chaoset-import/main_test.go 全清单）：
//! 合并取较大词频 / -replace 覆盖 / seed 与 -backup / 失败不改旧文件 /
//! 原子写 + 进度与错误文案 / flag 解析与退出码。

use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};

use chaoset_tools::dictimport::run;
use chaoset_tools::dictparse::parse_auto;
use tempfile::TempDir;

fn write_file(dir: &TempDir, name: &str, content: &str) -> PathBuf {
    let path = dir.path().join(name);
    fs::write(&path, content).unwrap();
    path
}

fn path_str(path: &Path) -> String {
    path.to_string_lossy().to_string()
}

fn run_cli(args: &[String]) -> (i32, String, String) {
    let mut stdout: Vec<u8> = Vec::new();
    let mut stderr: Vec<u8> = Vec::new();
    let code = run(args, &mut stdout, &mut stderr);
    (
        code,
        String::from_utf8(stdout).unwrap(),
        String::from_utf8(stderr).unwrap(),
    )
}

/// 解析输出 TSV，按「词 + 音节」索引词频。
fn read_entries(path: &Path) -> HashMap<String, i32> {
    let data = fs::read_to_string(path).unwrap();
    let (entries, _) = parse_auto(&data);
    entries
        .into_iter()
        .map(|e| (format!("{}\u{0}{}", e.word, e.syllables_joined()), e.freq))
        .collect()
}

fn backup_paths(target: &Path) -> Vec<PathBuf> {
    let parent = target.parent().unwrap();
    let prefix = format!("{}.bak.", target.file_name().unwrap().to_string_lossy());
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

fn temp_import_files(dir: &Path) -> Vec<PathBuf> {
    fs::read_dir(dir)
        .unwrap()
        .filter_map(|entry| entry.ok().map(|e| e.path()))
        .filter(|p| {
            p.file_name()
                .map(|name| name.to_string_lossy().starts_with(".chaoset-import-"))
                .unwrap_or(false)
        })
        .collect()
}

#[test]
fn merge_keeps_larger_freq() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    fs::write(&out, "你好\tni hao\t10\n").unwrap();
    let hot = write_file(&dir, "hot.tsv", "你好\tni hao\t20\n你\tni\t30\n");

    let (code, stdout, stderr) = run_cli(&["-o".into(), path_str(&out), path_str(&hot)]);
    assert_eq!(code, 0, "stderr={stderr}");
    assert!(stderr.is_empty(), "无跳过/备份时 stderr 应为空: {stderr:?}");
    let got = read_entries(&out);
    assert_eq!(got["你好\u{0}ni hao"], 20, "重复词应取较大词频");
    assert_eq!(got["你\u{0}ni"], 30);
    assert_eq!(
        stdout,
        format!("已写入 {}（共 2 条）\n", out.display()),
        "成功文案逐字一致"
    );
    assert!(temp_import_files(dir.path()).is_empty(), "不应残留临时文件");
}

#[test]
fn replace_overrides_lower_freq() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    fs::write(&out, "你好\tni hao\t20\n").unwrap();
    let hot = write_file(&dir, "hot.tsv", "你好\tni hao\t5\n");

    let (code, _, stderr) = run_cli(&[
        "-o".into(),
        path_str(&out),
        "-replace".into(),
        path_str(&hot),
    ]);
    assert_eq!(code, 0, "stderr={stderr}");
    assert_eq!(read_entries(&out)["你好\u{0}ni hao"], 5);
}

#[test]
fn missing_input_keeps_old_output_bytes() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    let old = "旧词\tjiu ci\t42\n";
    fs::write(&out, old).unwrap();
    let missing = dir.path().join("missing.yaml");

    let (code, stdout, stderr) = run_cli(&["-o".into(), path_str(&out), path_str(&missing)]);
    assert_eq!(code, 1, "输入不存在应报错退出 1");
    assert!(stdout.is_empty(), "失败时不应有成功文案: {stdout:?}");
    assert!(
        stderr.contains("chaoset-import: 读取") && stderr.contains("失败"),
        "错误文案不符: {stderr}"
    );
    assert_eq!(
        fs::read_to_string(&out).unwrap(),
        old,
        "失败时旧词库必须逐字节不变"
    );
}

#[test]
fn seed_and_backup() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    let seed = write_file(&dir, "seed.tsv", "基础\tji chu\t50\n");
    fs::write(&out, "旧词\tjiu ci\t10\n").unwrap();
    let hot = write_file(&dir, "hot.tsv", "新词\txin ci\t20\n");

    let (code, stdout, stderr) = run_cli(&[
        "-o".into(),
        path_str(&out),
        "-seed".into(),
        path_str(&seed),
        "-backup".into(),
        path_str(&hot),
    ]);
    assert_eq!(code, 0, "stderr={stderr}");
    let got = read_entries(&out);
    assert_eq!(got["基础\u{0}ji chu"], 50, "seed 应并入底表");
    assert_eq!(got["旧词\u{0}jiu ci"], 10, "已有词库应保留");
    assert_eq!(got["新词\u{0}xin ci"], 20);
    assert_eq!(stdout, format!("已写入 {}（共 3 条）\n", out.display()));

    let baks = backup_paths(&out);
    assert_eq!(baks.len(), 1, "应生成 1 个旧词库备份: {baks:?}");
    let name = baks[0].file_name().unwrap().to_string_lossy().to_string();
    let suffix = name
        .strip_prefix("merged.tsv.bak.")
        .expect("备份名格式应为 <target>.bak.<stamp>");
    assert_eq!(suffix.len(), 15, "时间戳应为 YYYYMMDD-HHMMSS: {name}");
    assert_eq!(suffix.as_bytes()[8], b'-');
    assert!(fs::read_to_string(&baks[0]).unwrap().contains("旧词"));
    assert!(stderr.contains(&format!("旧词库已备份：{}", baks[0].display())));
}

#[test]
fn backup_only_when_target_exists() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    let hot = write_file(&dir, "hot.tsv", "新词\txin ci\t20\n");
    let (code, _, stderr) = run_cli(&[
        "-o".into(),
        path_str(&out),
        "-backup".into(),
        path_str(&hot),
    ]);
    assert_eq!(code, 0, "stderr={stderr}");
    assert!(backup_paths(&out).is_empty(), "首次导入不应产生备份");
}

#[test]
fn seed_missing_fails() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    let hot = write_file(&dir, "hot.tsv", "新词\txin ci\t20\n");
    let missing_seed = dir.path().join("missing-seed.tsv");
    let (code, _, stderr) = run_cli(&[
        "-o".into(),
        path_str(&out),
        "-seed".into(),
        path_str(&missing_seed),
        path_str(&hot),
    ]);
    assert_eq!(code, 1);
    assert!(
        stderr.starts_with("chaoset-import: 种子词库:"),
        "stderr={stderr}"
    );
}

#[test]
fn creates_output_dir() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("nested").join("merged.tsv");
    let hot = write_file(&dir, "hot.tsv", "你好\tni hao\t10\n");
    let (code, _, stderr) = run_cli(&["-o".into(), path_str(&out), path_str(&hot)]);
    assert_eq!(code, 0, "stderr={stderr}");
    assert!(out.is_file(), "输出文件应存在");
}

#[test]
fn output_is_byte_exact_sorted_tsv() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    let hot = write_file(&dir, "hot.tsv", "你好\tni hao\t20\n你\tni\t30\n");
    let (code, _, _) = run_cli(&["-o".into(), path_str(&out), path_str(&hot)]);
    assert_eq!(code, 0);
    let expected = "\
# chaoset 词库（由 chaoset-import 生成，词\\t拼音\\t词频）
你\tni\t30
你好\tni hao\t20
";
    assert_eq!(fs::read(&out).unwrap(), expected.as_bytes());
}

#[test]
fn skipped_lines_go_to_stderr() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    let hot = write_file(&dir, "hot.tsv", "你好\tni hao\t10\n坏词\tzzz\t5\n坏行2\n");
    let (code, _, stderr) = run_cli(&["-o".into(), path_str(&out), path_str(&hot)]);
    assert_eq!(code, 0);
    assert_eq!(
        stderr,
        format!("{}: 跳过 2 行无效词条\n", hot.display()),
        "跳过行文案逐字一致"
    );
}

#[test]
fn help_exits_zero_with_usage_on_stdout() {
    for flag in ["-h", "--help"] {
        let (code, stdout, stderr) = run_cli(&[flag.to_string()]);
        assert_eq!(code, 0, "{flag} 应退出 0");
        assert!(
            stdout.contains("用法: chaoset-import"),
            "用法应在 stdout: {stdout:?}"
        );
        assert!(stderr.is_empty(), "{flag} 不应写 stderr: {stderr:?}");
    }
}

#[test]
fn usage_errors_exit_two() {
    // 缺 -o
    let hot = "x.tsv";
    let (code, stdout, stderr) = run_cli(&[hot.to_string()]);
    assert_eq!(code, 2);
    assert!(stdout.is_empty());
    assert!(stderr.contains("用法: chaoset-import"));

    // 有 -o 但没有输入
    let (code, _, stderr) = run_cli(&["-o".into(), "out.tsv".into()]);
    assert_eq!(code, 2);
    assert!(stderr.contains("用法: chaoset-import"));

    // 未知 flag
    let (code, _, stderr) = run_cli(&[
        "-bogus".to_string(),
        "-o".to_string(),
        "out.tsv".to_string(),
        "in.tsv".to_string(),
    ]);
    assert_eq!(code, 2);
    assert!(
        stderr.contains("flag provided but not defined: -bogus"),
        "stderr={stderr}"
    );

    // 缺 flag 参数
    let (code, _, stderr) = run_cli(&["-o".to_string()]);
    assert_eq!(code, 2);
    assert!(
        stderr.contains("flag needs an argument: -o"),
        "stderr={stderr}"
    );
}

#[test]
fn version_exits_zero_single_line() {
    let (code, stdout, stderr) = run_cli(&["-version".to_string()]);
    assert_eq!(code, 0, "stderr={stderr}");
    assert_eq!(stdout.lines().count(), 1, "版本应为单行: {stdout:?}");
    assert!(stdout.starts_with("chaoset "), "版本行文案: {stdout:?}");
}

#[test]
fn bool_flag_forms_are_accepted() {
    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    fs::write(&out, "你好\tni hao\t20\n").unwrap();
    let hot = write_file(&dir, "hot.tsv", "你好\tni hao\t5\n");
    // -replace=false 等价于不带（取较大词频）
    let (code, _, stderr) = run_cli(&[
        "-o".into(),
        path_str(&out),
        "--replace=false".into(),
        path_str(&hot),
    ]);
    assert_eq!(code, 0, "stderr={stderr}");
    assert_eq!(read_entries(&out)["你好\u{0}ni hao"], 20);
    // --replace（覆盖）
    let (code, _, stderr) = run_cli(&[
        "--o".into(),
        path_str(&out),
        "--replace".into(),
        path_str(&hot),
    ]);
    assert_eq!(code, 0, "stderr={stderr}");
    assert_eq!(read_entries(&out)["你好\u{0}ni hao"], 5);
}

#[cfg(unix)]
#[test]
fn backup_failure_aborts_and_keeps_old_target() {
    use std::os::unix::fs::PermissionsExt;

    let dir = TempDir::new().unwrap();
    let out = dir.path().join("merged.tsv");
    let old = "旧词\tjiu ci\t42\n";
    fs::write(&out, old).unwrap();
    let hot = write_file(&dir, "hot.tsv", "新词\txin ci\t20\n");

    // 目录改为只读：备份文件创建失败（EACCES），导入必须中止且旧词库不变。
    fs::set_permissions(dir.path(), fs::Permissions::from_mode(0o500)).unwrap();
    let readonly = fs::metadata(dir.path()).unwrap().permissions().mode() & 0o200 == 0;
    let result = if readonly {
        let args = vec![
            "-o".to_string(),
            path_str(&out),
            "-backup".to_string(),
            path_str(&hot),
        ];
        let mut stdout: Vec<u8> = Vec::new();
        let mut stderr: Vec<u8> = Vec::new();
        let code = run(&args, &mut stdout, &mut stderr);
        Some((code, String::from_utf8(stderr).unwrap()))
    } else {
        None
    };
    fs::set_permissions(dir.path(), fs::Permissions::from_mode(0o700)).unwrap();

    let (code, stderr) = result.expect("无法让目录只读（可能以 root 运行），跳过");
    assert_eq!(code, 1);
    assert!(
        stderr.contains("chaoset-import: 创建旧词库备份失败"),
        "stderr={stderr}"
    );
    assert_eq!(
        fs::read_to_string(&out).unwrap(),
        old,
        "备份失败时旧词库必须不变"
    );
    assert!(backup_paths(&out).is_empty(), "不应留下半截备份");
}

#[test]
fn unusable_output_path_fails_without_creating_target() {
    let dir = TempDir::new().unwrap();
    // 输出路径的父级是普通文件：目录不可用，必须报错且不创建目标文件。
    let blocker = write_file(&dir, "blocker", "x");
    let out = blocker.join("merged.tsv");
    let hot = write_file(&dir, "hot.tsv", "你好\tni hao\t10\n");
    let (code, _, stderr) = run_cli(&["-o".into(), path_str(&out), path_str(&hot)]);
    assert_eq!(code, 1);
    assert!(stderr.starts_with("chaoset-import:"), "stderr={stderr}");
    assert!(!out.exists());
}
