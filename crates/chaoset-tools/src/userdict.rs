//! 用户词典本地文件模式（不依赖运行中的 daemon，直接读写 userdict.json）。
//!
//! 文件格式以引擎（crates/chaoset-engine 的 UserDict）canonical JSON 为准：
//! words/phrases/bigrams 三张表；旧扁平格式（键为「词\0音节」）加载时自动迁移。
//! 文件损坏时改名 <path>.corrupt-<时间戳> 后以空词典继续（权限错误仍报错）。

use std::io::Write;
use std::path::{Path, PathBuf};

use crate::fsutil;

/// 主命令用法（未知子命令/缺子命令时输出到 stderr）。
pub const LOCAL_USAGE: &str = "用法: chaoset userdict [-socket PATH | -pipe | -userdict FILE] stats|clear|delete 词 [音节...]|export [-phrases] [-o FILE]";

/// 本地文件模式入口（chaosetctl 已解析好全局 flag 后调用）。
///
/// cmd = stats / clear / delete / export；export 写 stdout 或原子写 out；
/// 删除/清空会立即原子落盘。损坏文件的警告经 eprintln 打到进程 stderr，
/// 需要捕获时用 [run_local_with_stderr]。
pub fn run_local(
    path: &Path,
    cmd: &str,
    phrases_only: bool,
    out: Option<&Path>,
    word: &str,
    sylls: &[String],
    stdout: &mut dyn Write,
) -> Result<(), String> {
    let mut stderr = std::io::stderr();
    run_local_with_stderr(
        path,
        cmd,
        phrases_only,
        out,
        word,
        sylls,
        stdout,
        &mut stderr,
    )
}

/// 与 run_local 相同，但损坏文件警告写入显式 stderr（测试/调用方捕获用）。
#[allow(clippy::too_many_arguments)]
pub fn run_local_with_stderr(
    path: &Path,
    cmd: &str,
    phrases_only: bool,
    out: Option<&Path>,
    word: &str,
    sylls: &[String],
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> Result<(), String> {
    let (dict, warnings) =
        chaoset_engine::UserDict::open(Some(path)).map_err(|err| err.to_string())?;
    for warning in warnings {
        let _ = writeln!(stderr, "警告: {warning}");
    }

    match cmd {
        "stats" => {
            let stats = dict.stats();
            let _ = writeln!(
                stdout,
                "词 {} 条 / 词组 {} 条 / 上下文 {} 条",
                stats.words, stats.phrases, stats.bigrams
            );
            Ok(())
        }
        "clear" => {
            let before = dict.stats();
            dict.clear();
            dict.save().map_err(|err| err.to_string())?;
            let _ = writeln!(
                stdout,
                "已清空：词 {} 条、词组 {} 条、上下文 {} 条",
                before.words, before.phrases, before.bigrams
            );
            Ok(())
        }
        "delete" => {
            if word.is_empty() {
                return Err("待删除的词不能为空".to_string());
            }
            let deleted = dict.delete_word(word, sylls);
            if deleted {
                dict.save().map_err(|err| err.to_string())?;
                let _ = writeln!(stdout, "已删除「{word}」的学习记录");
            } else {
                let _ = writeln!(stdout, "未找到「{word}」的学习记录（可能未学习过或已删除）");
            }
            Ok(())
        }
        "export" => {
            let tsv = dict.export_tsv(phrases_only);
            write_out(out, tsv.as_bytes(), stdout)
        }
        other => Err(format!("未知 userdict 子命令: {other}")),
    }
}

/// 完整本地 CLI 入口（chaoset-userdict 本地模式）：自行解析 stats/clear/
/// delete/export 的位置参数与 flag；成功 0、运行错误 1、用法错误 2。
pub fn run_local_cli(
    path: &Path,
    args: &[String],
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let Some(cmd) = args.first() else {
        let _ = writeln!(stderr, "{LOCAL_USAGE}");
        return 2;
    };
    let rest = &args[1..];

    let mut phrases_only = false;
    let mut out: Option<PathBuf> = None;
    let mut word = String::new();
    let mut sylls: Vec<String> = Vec::new();

    let cmd: &str = match cmd.as_str() {
        "stats" => "stats",
        "clear" => "clear",
        "delete" => {
            let Some(first) = rest.first() else {
                let _ = writeln!(stderr, "用法: chaoset userdict ... delete 词 [音节...]");
                return 2;
            };
            if first.is_empty() {
                let _ = writeln!(stderr, "用法: chaoset userdict ... delete 词 [音节...]");
                return 2;
            }
            word = first.clone();
            sylls = split_syllables(&rest[1..].join(" "));
            "delete"
        }
        "export" => match parse_export_args(rest) {
            Ok((parsed_phrases, parsed_out)) => {
                phrases_only = parsed_phrases;
                out = parsed_out;
                "export"
            }
            Err(FlagError::Help) => {
                let _ = write!(stdout, "{LOCAL_USAGE}");
                return 0;
            }
            Err(FlagError::Message(message)) => {
                let _ = writeln!(stderr, "{message}");
                let _ = write!(stderr, "{LOCAL_USAGE}");
                return 2;
            }
        },
        "help" | "-h" | "--help" => {
            let _ = write!(stdout, "{LOCAL_USAGE}");
            return 0;
        }
        _ => {
            let _ = writeln!(stderr, "{LOCAL_USAGE}");
            return 2;
        }
    };

    match run_local_with_stderr(
        path,
        cmd,
        phrases_only,
        out.as_deref(),
        &word,
        &sylls,
        stdout,
        stderr,
    ) {
        Ok(()) => 0,
        Err(err) => {
            let _ = writeln!(stderr, "{err}");
            1
        }
    }
}

/// delete 的音节参数：空格与单引号都是分隔符（jin'tian 与 jin tian 等价）。
pub fn split_syllables(value: &str) -> Vec<String> {
    value
        .replace('\'', " ")
        .split_whitespace()
        .map(String::from)
        .collect()
}

enum FlagError {
    Help,
    Message(String),
}

/// export 子命令 flag：-phrases（只导出词组三列）与 -o（输出文件）。
fn parse_export_args(args: &[String]) -> Result<(bool, Option<PathBuf>), FlagError> {
    let mut phrases_only = false;
    let mut out: Option<PathBuf> = None;
    let mut idx = 0;
    while idx < args.len() {
        let arg = &args[idx];
        if arg == "-" || !arg.starts_with('-') {
            break; // Go flag：首个非 flag 参数之后不再解析
        }
        if arg == "--" {
            break;
        }
        let body = arg.trim_start_matches('-');
        let (name, inline) = match body.split_once('=') {
            Some((name, value)) => (name, Some(value)),
            None => (body, None),
        };
        match name {
            "h" | "help" => return Err(FlagError::Help),
            "phrases" => {
                phrases_only = match inline {
                    Some(value) => parse_bool(value).ok_or_else(|| {
                        FlagError::Message(format!(
                            "invalid boolean value {value:?} for -phrases: parse error"
                        ))
                    })?,
                    None => true,
                };
            }
            "o" => {
                let value = match inline {
                    Some(value) => value.to_string(),
                    None => {
                        idx += 1;
                        match args.get(idx) {
                            Some(value) => value.clone(),
                            None => {
                                return Err(FlagError::Message(
                                    "flag needs an argument: -o".to_string(),
                                ))
                            }
                        }
                    }
                };
                out = Some(PathBuf::from(value));
            }
            _ => {
                return Err(FlagError::Message(format!(
                    "flag provided but not defined: -{name}"
                )))
            }
        }
        idx += 1;
    }
    Ok((phrases_only, out))
}

fn parse_bool(value: &str) -> Option<bool> {
    match value {
        "1" | "t" | "T" | "true" | "TRUE" | "True" => Some(true),
        "0" | "f" | "F" | "false" | "FALSE" | "False" => Some(false),
        _ => None,
    }
}

/// 导出输出：无 -o 原样写 stdout；有 -o 用同目录临时文件 + 0644 + rename 原子写
/// （不创建父目录，失败时旧文件保持不变）。
fn write_out(out: Option<&Path>, content: &[u8], stdout: &mut dyn Write) -> Result<(), String> {
    let Some(path) = out else {
        stdout
            .write_all(content)
            .map_err(|err| format!("写出导出内容失败: {err}"))?;
        return Ok(());
    };

    let dir = fsutil::parent_dir(path).to_path_buf();
    let (mut file, tmp_path) = fsutil::create_temp_file(&dir, ".chaoset-userdict-", ".tmp")
        .map_err(|err| format!("创建临时文件失败: {err}"))?;
    let cleanup = |tmp: &Path| {
        let _ = std::fs::remove_file(tmp);
    };
    if let Err(err) = file.write_all(content) {
        drop(file);
        cleanup(&tmp_path);
        return Err(format!("写出导出文件失败: {err}"));
    }
    if let Err(err) = file.flush() {
        drop(file);
        cleanup(&tmp_path);
        return Err(format!("关闭临时文件失败: {err}"));
    }
    drop(file);
    if let Err(err) = fsutil::set_mode_0644(&tmp_path) {
        cleanup(&tmp_path);
        return Err(format!("设置导出文件权限失败: {err}"));
    }
    if let Err(err) = fsutil::replace_file(&tmp_path, path) {
        cleanup(&tmp_path);
        return Err(format!("替换导出文件失败（旧文件保持不变）: {err}"));
    }
    Ok(())
}
