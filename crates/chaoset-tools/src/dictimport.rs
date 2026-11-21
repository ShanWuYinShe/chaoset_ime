//! chaoset-import 语义：把热词文件（TSV / Rime dict.yaml / CC-CEDICT）归一化，
//! 与种子词库、已有输出词库合并，原子写出一份可直接给 chaosetd -dict 加载的 TSV。
//!
//! 合并规则与 Go internal/tools/dictimport 一致：seed 与已有 -o 文件先并入底表
//! （replace=false，取较大词频）；位置词库按 -replace 策略并入。写出全程用
//! 「同目录临时文件 + 0644 + rename」原子替换，任何失败都不会改动旧词库。

use std::collections::HashMap;
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};

use crate::dictparse::{self, Entry};
use crate::fsutil;

/// 用法说明（-h/--help 输出到 stdout；参数错误时输出到 stderr）。
pub const IMPORT_USAGE: &str =
    "用法: chaoset-import -o <输出TSV> [-seed <种子词库>] [-backup] [-replace] <输入词库...>

把热词文件（TSV / Rime dict.yaml / CC-CEDICT）归一化，与已有词库合并输出可直接
给 chaosetd -dict 加载的 TSV。重复词条默认取较大词频；-replace 时以导入词频覆盖。
写出用同目录临时文件 + rename 原子替换，成功/失败都不会截断旧词库。

flags:
  -o string        输出 TSV 路径（必填）
  -seed string     种子词库路径（先并入底表；壳用它保留随包基础词库）
  -backup          写目标前把旧词库备份为 <target>.bak.<时间戳>；备份失败则中止
  -replace         与已有词库重复的词条：true=以导入词频覆盖，false=取较大词频
  -version         打印构建信息（提交号/构建时间）后退出
";

/// 解析后的命令行参数。
#[derive(Debug, Default, PartialEq, Eq)]
struct ImportArgs {
    out: String,
    seed: String,
    backup: bool,
    replace: bool,
    version: bool,
    inputs: Vec<String>,
}

/// flag 解析失败：Help 走 stdout + 退出 0；Message 走 stderr + 退出 2。
#[derive(Debug)]
enum FlagError {
    Help,
    Message(String),
}

/// 按 Go flag 包的语义解析：首个非 flag 参数之后全部是位置参数；支持
/// -flag / --flag / -flag=value / --flag=value；-- 之后全部是位置参数。
fn parse_args(args: &[String]) -> Result<ImportArgs, FlagError> {
    let mut parsed = ImportArgs::default();
    let mut idx = 0;
    while idx < args.len() {
        let arg = &args[idx];
        if arg == "-" || !arg.starts_with('-') {
            break;
        }
        if arg == "--" {
            idx += 1;
            break;
        }
        let body = arg.trim_start_matches('-');
        let (name, inline) = match body.split_once('=') {
            Some((name, value)) => (name, Some(value)),
            None => (body, None),
        };
        match name {
            "h" | "help" => return Err(FlagError::Help),
            "o" | "seed" => {
                let value = match inline {
                    Some(value) => value.to_string(),
                    None => {
                        idx += 1;
                        match args.get(idx) {
                            Some(value) => value.clone(),
                            None => {
                                return Err(FlagError::Message(format!(
                                    "flag needs an argument: -{name}"
                                )))
                            }
                        }
                    }
                };
                if name == "o" {
                    parsed.out = value;
                } else {
                    parsed.seed = value;
                }
            }
            "backup" | "replace" | "version" => {
                let value = match inline {
                    Some(value) => parse_bool(value).ok_or_else(|| {
                        FlagError::Message(format!(
                            "invalid boolean value {value:?} for -{name}: parse error"
                        ))
                    })?,
                    None => true,
                };
                match name {
                    "backup" => parsed.backup = value,
                    "replace" => parsed.replace = value,
                    _ => parsed.version = value,
                }
            }
            _ => {
                return Err(FlagError::Message(format!(
                    "flag provided but not defined: -{name}"
                )))
            }
        }
        idx += 1;
    }
    parsed.inputs = args[idx..].to_vec();
    Ok(parsed)
}

/// Go strconv.ParseBool 兼容的布尔值解析。
fn parse_bool(value: &str) -> Option<bool> {
    match value {
        "1" | "t" | "T" | "true" | "TRUE" | "True" => Some(true),
        "0" | "f" | "F" | "false" | "FALSE" | "False" => Some(false),
        _ => None,
    }
}

/// CLI 入口（chaoset-import / chaosetctl import）：退出码 0/1/2。
pub fn run(args: &[String], stdout: &mut dyn Write, stderr: &mut dyn Write) -> i32 {
    let parsed = match parse_args(args) {
        Ok(parsed) => parsed,
        Err(FlagError::Help) => {
            let _ = write!(stdout, "{IMPORT_USAGE}");
            return 0;
        }
        Err(FlagError::Message(message)) => {
            let _ = writeln!(stderr, "{message}");
            let _ = write!(stderr, "{IMPORT_USAGE}");
            return 2;
        }
    };

    if parsed.version {
        let _ = writeln!(stdout, "{}", crate::version_info());
        return 0;
    }
    if parsed.out.is_empty() || parsed.inputs.is_empty() {
        let _ = write!(stderr, "{IMPORT_USAGE}");
        return 2;
    }

    match merge_files(
        Path::new(&parsed.out),
        &parsed.seed,
        parsed.backup,
        parsed.replace,
        &parsed.inputs,
        stdout,
        stderr,
    ) {
        Ok(()) => 0,
        Err(err) => {
            let _ = writeln!(stderr, "chaoset-import: {err}");
            1
        }
    }
}

/// 合并词库并原子写出（Go dictimport.Run 的 Rust 版）。
///
/// 成功提示写 stdout（已写入 <path>（共 N 条））；每个输入文件跳过的无效行数与
/// 旧词库备份路径写 stderr，与 Go 实现逐字一致。
pub fn merge_files(
    out_path: &Path,
    seed: &str,
    backup: bool,
    replace: bool,
    inputs: &[String],
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> Result<(), String> {
    let mut merged: HashMap<String, Acc> = HashMap::new();

    if !seed.is_empty() {
        absorb_file(&mut merged, seed, false, stderr).map_err(|e| format!("种子词库: {e}"))?;
    }
    match fs::metadata(out_path) {
        Ok(_) => absorb_file(&mut merged, &out_path.to_string_lossy(), false, stderr)?,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => {}
        Err(err) => return Err(format!("检查已有词库 {} 失败: {err}", out_path.display())),
    }
    for input in inputs {
        absorb_file(&mut merged, input, replace, stderr)?;
    }

    let entries: Vec<Entry> = merged
        .into_values()
        .map(|acc| Entry {
            word: acc.word,
            syllables: acc.sylls,
            freq: acc.freq,
        })
        .collect();

    let dir = fsutil::parent_dir(out_path).to_path_buf();
    fs::create_dir_all(&dir).map_err(|e| format!("创建输出目录失败: {e}"))?;

    if backup {
        match fs::metadata(out_path) {
            Ok(_) => backup_file(out_path, stderr)?,
            Err(err) if err.kind() == std::io::ErrorKind::NotFound => {}
            Err(err) => return Err(format!("检查已有词库 {} 失败: {err}", out_path.display())),
        }
    }

    let (mut tmp_file, tmp_path) = fsutil::create_temp_file(&dir, ".chaoset-import-", ".tmp")
        .map_err(|e| format!("创建临时文件失败: {e}"))?;
    let write_result = {
        let mut writer = std::io::BufWriter::new(&mut tmp_file);
        let result = dictparse::write_tsv(&mut writer, &entries)
            .and_then(|()| writer.flush())
            .map_err(|e| format!("写出词库失败: {e}"));
        result
    };
    drop(tmp_file);
    if let Err(err) = write_result {
        let _ = fs::remove_file(&tmp_path);
        return Err(err);
    }
    if let Err(err) = fsutil::set_mode_0644(&tmp_path) {
        let _ = fs::remove_file(&tmp_path);
        return Err(format!("设置词库权限失败: {err}"));
    }
    if let Err(err) = fsutil::replace_file(&tmp_path, out_path) {
        let _ = fs::remove_file(&tmp_path);
        return Err(format!("替换词库失败（旧词库保持不变）: {err}"));
    }

    let _ = writeln!(
        stdout,
        "已写入 {}（共 {} 条）",
        out_path.display(),
        entries.len()
    );
    Ok(())
}

/// 合并中的一条累积记录。
struct Acc {
    word: String,
    sylls: Vec<String>,
    freq: i32,
}

/// 读取并宽松解析一个词库文件，按 replace 策略并入 merged；跳过行写 stderr 警告。
fn absorb_file(
    merged: &mut HashMap<String, Acc>,
    path: &str,
    replace: bool,
    stderr: &mut dyn Write,
) -> Result<(), String> {
    let data = fs::read(path).map_err(|e| format!("读取 {path} 失败: {e}"))?;
    let content = String::from_utf8_lossy(&data);
    let (entries, stats) = dictparse::parse_auto(&content);
    absorb(merged, entries, replace);
    if stats.skipped > 0 {
        let _ = writeln!(stderr, "{path}: 跳过 {} 行无效词条", stats.skipped);
    }
    Ok(())
}

/// 并入词条：重复键（词 + 音节）在 replace 时覆盖词频，否则取较大词频。
fn absorb(merged: &mut HashMap<String, Acc>, entries: Vec<Entry>, replace: bool) {
    for entry in entries {
        let key = format!("{}\u{0}{}", entry.word, entry.syllables_joined());
        match merged.get_mut(&key) {
            Some(old) => {
                if replace || entry.freq > old.freq {
                    old.freq = entry.freq;
                }
            }
            None => {
                merged.insert(
                    key,
                    Acc {
                        word: entry.word,
                        sylls: entry.syllables,
                        freq: entry.freq,
                    },
                );
            }
        }
    }
}

/// 把旧词库备份为 <path>.bak.<本地时间戳>（冲突时追加 -1/-2...），打印备份路径。
pub fn backup_file(path: &Path, stderr: &mut dyn Write) -> Result<(), String> {
    backup_file_with_stamp(path, &fsutil::local_stamp(), stderr)
}

/// backup_file 的可注入时间戳版本（单测固定时间戳用）。
fn backup_file_with_stamp(path: &Path, stamp: &str, stderr: &mut dyn Write) -> Result<(), String> {
    let info = fs::metadata(path).map_err(|e| format!("备份前检查旧词库失败: {e}"))?;
    let path_text = path.to_string_lossy();
    let mut bak = PathBuf::from(format!("{path_text}.bak.{stamp}"));
    let mut seq = 1usize;
    while bak.exists() {
        bak = PathBuf::from(format!("{path_text}.bak.{stamp}-{seq}"));
        seq += 1;
    }

    let mut src = File::open(path).map_err(|e| format!("打开旧词库备份失败: {e}"))?;
    let mut opts = OpenOptions::new();
    opts.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
        let mode = info.permissions().mode() & 0o777;
        opts.mode(mode);
    }
    #[cfg(not(unix))]
    {
        let _ = &info;
    }
    let mut dst = opts
        .open(&bak)
        .map_err(|e| format!("创建旧词库备份失败: {e}"))?;
    if let Err(err) = std::io::copy(&mut src, &mut dst) {
        drop(dst);
        let _ = fs::remove_file(&bak);
        return Err(format!("写入旧词库备份失败: {err}"));
    }
    if let Err(err) = dst.flush() {
        drop(dst);
        let _ = fs::remove_file(&bak);
        return Err(format!("关闭旧词库备份失败: {err}"));
    }
    drop(dst);
    let _ = writeln!(stderr, "旧词库已备份：{}", bak.display());
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    fn args(items: &[&str]) -> Vec<String> {
        items.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn backup_name_collision_increments_suffix() {
        let dir = tempdir().unwrap();
        let target = dir.path().join("dict.tsv");
        fs::write(&target, "旧词\tjiu ci\t10\n").unwrap();
        let stamp = "20260101-010203";

        let mut err1: Vec<u8> = Vec::new();
        backup_file_with_stamp(&target, stamp, &mut err1).unwrap();
        assert_eq!(
            String::from_utf8(err1).unwrap(),
            format!(
                "旧词库已备份：{}\n",
                dir.path().join("dict.tsv.bak.20260101-010203").display()
            )
        );

        let mut err2: Vec<u8> = Vec::new();
        backup_file_with_stamp(&target, stamp, &mut err2).unwrap();
        let mut err3: Vec<u8> = Vec::new();
        backup_file_with_stamp(&target, stamp, &mut err3).unwrap();

        assert!(dir.path().join("dict.tsv.bak.20260101-010203").is_file());
        assert!(dir.path().join("dict.tsv.bak.20260101-010203-1").is_file());
        assert!(dir.path().join("dict.tsv.bak.20260101-010203-2").is_file());
        for name in [
            "dict.tsv.bak.20260101-010203",
            "dict.tsv.bak.20260101-010203-1",
        ] {
            assert_eq!(
                fs::read_to_string(dir.path().join(name)).unwrap(),
                "旧词\tjiu ci\t10\n"
            );
        }
    }

    #[test]
    fn backup_uses_old_file_permissions() {
        let dir = tempdir().unwrap();
        let target = dir.path().join("dict.tsv");
        fs::write(&target, "旧词\tjiu ci\t10\n").unwrap();
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&target, fs::Permissions::from_mode(0o600)).unwrap();
            let mut err: Vec<u8> = Vec::new();
            backup_file_with_stamp(&target, "20260101-010203", &mut err).unwrap();
            let bak = dir.path().join("dict.tsv.bak.20260101-010203");
            let mode = fs::metadata(&bak).unwrap().permissions().mode() & 0o777;
            assert_eq!(mode, 0o600, "备份权限应与旧文件一致");
        }
    }

    #[test]
    fn parse_args_forms() {
        let parsed = parse_args(&args(&[
            "-o=x.tsv",
            "--seed",
            "s.tsv",
            "-backup",
            "--replace=false",
            "-version=false",
            "a.tsv",
            "-b.tsv",
        ]))
        .unwrap();
        assert_eq!(parsed.out, "x.tsv");
        assert_eq!(parsed.seed, "s.tsv");
        assert!(parsed.backup);
        assert!(!parsed.replace);
        assert!(!parsed.version);
        // 首个位置参数之后全部是位置参数（-b.tsv 也当输入文件）
        assert_eq!(
            parsed.inputs,
            vec!["a.tsv".to_string(), "-b.tsv".to_string()]
        );
    }

    #[test]
    fn parse_args_double_dash_terminates_flags() {
        let parsed = parse_args(&args(&["-o", "x.tsv", "--", "-weird.tsv"])).unwrap();
        assert_eq!(parsed.out, "x.tsv");
        assert_eq!(parsed.inputs, vec!["-weird.tsv".to_string()]);
    }

    #[test]
    fn parse_args_help_and_errors() {
        assert!(matches!(parse_args(&args(&["-h"])), Err(FlagError::Help)));
        assert!(matches!(
            parse_args(&args(&["--help"])),
            Err(FlagError::Help)
        ));
        match parse_args(&args(&["-o"])) {
            Err(FlagError::Message(m)) => assert_eq!(m, "flag needs an argument: -o"),
            _ => panic!("缺参数应报错"),
        }
        match parse_args(&args(&["-nope"])) {
            Err(FlagError::Message(m)) => assert_eq!(m, "flag provided but not defined: -nope"),
            _ => panic!("未知 flag 应报错"),
        }
        match parse_args(&args(&["-backup=maybe"])) {
            Err(FlagError::Message(m)) => assert!(m.contains("invalid boolean value")),
            _ => panic!("非法布尔值应报错"),
        }
    }
}
