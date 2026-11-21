//! `userdict` 子命令：daemon 模式（IPC）与本地文件模式（chaoset-tools）。

use std::io::Write;
use std::path::{Path, PathBuf};

use crate::buildinfo;

const USAGE: &str = "用法: chaoset userdict [-socket PATH | -pipe | -userdict FILE] stats|clear|delete 词 [音节...]|export [-phrases] [-o FILE]";

pub fn run(args: &[String], stdout: &mut dyn Write, stderr: &mut dyn Write, _store: Option<()>) -> i32 {
    let parsed = match parse_flags(args, stderr) {
        Ok(p) => p,
        Err(()) => return 2,
    };
    if parsed.version {
        let _ = writeln!(stdout, "{}", buildinfo::info());
        return 0;
    }
    if parsed.print_paths {
        match paths_json() {
            Ok(text) => { let _ = writeln!(stdout, "{text}"); return 0; }
            Err(e) => { let _ = writeln!(stderr, "{e}"); return 1; }
        }
    }
    if parsed.pipe && parsed.socket.is_some() {
        let _ = writeln!(stderr, "-socket / -pipe 只能二选一");
        return 2;
    }
    let Some(cmd) = parsed.rest.first().map(String::as_str) else {
        let _ = writeln!(stderr, "{USAGE}");
        return 2;
    };
    let rest = &parsed.rest[1..];
    let (phrases_only, out_file, word, sylls) = match cmd {
        "stats" | "clear" => (false, None, String::new(), Vec::new()),
        "export" => match parse_export_flags(rest, stderr) {
            Ok(v) => v,
            Err(()) => return 2,
        },
        "delete" => {
            if rest.is_empty() || rest[0].is_empty() {
                let _ = writeln!(stderr, "用法: chaoset userdict ... delete 词 [音节...]");
                return 2;
            }
            let word = rest[0].clone();
            let sylls = split_syllables(&rest[1..].join(" "));
            (false, None, word, sylls)
        }
        _ => {
            let _ = writeln!(stderr, "{USAGE}");
            return 2;
        }
    };

    if let Some(path) = parsed.userdict.as_deref() {
        return match chaoset_tools::userdict::run_local(
            Path::new(path),
            cmd,
            phrases_only,
            out_file.as_deref(),
            &word,
            &sylls,
            stdout,
        ) {
            Ok(()) => 0,
            Err(e) => { let _ = writeln!(stderr, "{e}"); 1 }
        };
    }

    match run_daemon(&parsed, cmd, phrases_only, out_file.as_deref(), &word, &sylls, stdout) {
        Ok(()) => 0,
        Err(e) => { let _ = writeln!(stderr, "{e}"); 1 }
    }
}

struct Parsed {
    socket: Option<String>,
    pipe: bool,
    userdict: Option<String>,
    version: bool,
    print_paths: bool,
    rest: Vec<String>,
}

fn parse_flags(args: &[String], stderr: &mut dyn Write) -> Result<Parsed, ()> {
    let mut p = Parsed { socket: None, pipe: false, userdict: None, version: false, print_paths: false, rest: Vec::new() };
    let mut i = 0;
    while i < args.len() {
        let arg = &args[i];
        if !arg.starts_with('-') || arg == "-" {
            p.rest.extend_from_slice(&args[i..]);
            return Ok(p);
        }
        let raw = arg.trim_start_matches('-');
        let (name, inline) = match raw.split_once('=') {
            Some((n, v)) => (n, Some(v.to_string())),
            None => (raw, None),
        };
        let mut need_value = |i: &mut usize| -> Result<String, ()> {
            if let Some(v) = &inline { return Ok(v.clone()); }
            *i += 1;
            match args.get(*i) {
                Some(v) => Ok(v.clone()),
                None => { let _ = writeln!(stderr, "flag 需要 -{name} 参数值"); Err(()) }
            }
        };
        match name {
            "socket" => p.socket = Some(need_value(&mut i)?),
            "pipe" => p.pipe = true,
            "userdict" => p.userdict = Some(need_value(&mut i)?),
            "version" => p.version = true,
            "print-paths" => p.print_paths = true,
            "h" | "help" => {
                let _ = writeln!(stderr, "{USAGE}");
                return Err(());
            }
            other => {
                let _ = writeln!(stderr, "未知 flag: -{other}");
                let _ = writeln!(stderr, "{USAGE}");
                return Err(());
            }
        }
        i += 1;
    }
    Ok(p)
}

fn parse_export_flags(args: &[String], stderr: &mut dyn Write) -> Result<(bool, Option<PathBuf>, String, Vec<String>), ()> {
    let mut phrases_only = false;
    let mut out: Option<PathBuf> = None;
    let mut i = 0;
    while i < args.len() {
        let arg = &args[i];
        let raw = arg.trim_start_matches('-');
        let (name, inline) = match raw.split_once('=') {
            Some((n, v)) => (n, Some(v.to_string())),
            None => (raw, None),
        };
        match name {
            "phrases" => phrases_only = true,
            "o" => {
                let value = if let Some(v) = inline { v } else {
                    i += 1;
                    match args.get(i) { Some(v) => v.clone(), None => { let _ = writeln!(stderr, "flag 需要 -o 参数值"); return Err(()); } }
                };
                out = Some(PathBuf::from(value));
            }
            "h" | "help" => { let _ = writeln!(stderr, "{USAGE}"); return Err(()); }
            other => { let _ = writeln!(stderr, "未知 flag: -{other}"); return Err(()); }
        }
        i += 1;
    }
    Ok((phrases_only, out, String::new(), Vec::new()))
}

fn split_syllables(s: &str) -> Vec<String> {
    s.replace('\'', " ").split_whitespace().map(|s| s.to_string()).collect()
}

fn paths_json() -> Result<String, String> {
    let mut map = std::collections::BTreeMap::<String, String>::new();
    if let Ok(p) = chaoset_config::paths::data_dir() { map.insert("data".into(), p.display().to_string()); }
    if let Ok(p) = chaoset_config::paths::user_dict_path() { map.insert("userdict".into(), p.display().to_string()); }
    if let Ok(p) = chaoset_config::paths::imported_dict_path() { map.insert("imported_dict".into(), p.display().to_string()); }
    if let Ok(p) = chaoset_config::paths::config_path() { map.insert("config".into(), p.display().to_string()); }
    if let Ok(p) = chaoset_config::paths::socket_path() { map.insert("socket".into(), p.display().to_string()); }
    serde_json::to_string(&map).map_err(|e| format!("序列化路径失败: {e}"))
}

fn run_daemon(
    parsed: &Parsed,
    cmd: &str,
    phrases_only: bool,
    out_file: Option<&Path>,
    word: &str,
    sylls: &[String],
    stdout: &mut dyn Write,
) -> Result<(), String> {
    let mut client = if parsed.pipe {
        #[cfg(windows)]
        {
            let name = chaoset_config::paths::default_pipe_name()?;
            chaoset_ipc::client::Client::connect_pipe(&name)?
        }
        #[cfg(not(windows))]
        {
            return Err("命名管道仅 Windows 支持".to_string());
        }
    } else {
        let path = match parsed.socket.as_deref() {
            Some(s) => PathBuf::from(s),
            None => chaoset_config::paths::socket_path().map_err(|e| {
                #[cfg(windows)]
                { format!("{e}（Windows 请显式加 -pipe，或加 -userdict FILE 走本地文件模式）") }
                #[cfg(not(windows))]
                { e }
            })?,
        };
        chaoset_ipc::client::Client::connect_unix(&path)?
    };

    let map_err = |e: String| format!("连接 daemon 失败（daemon 未运行可改用 -userdict 本地模式）: {e}");
    let _ = &map_err;
    match cmd {
        "stats" => {
            let st = client.user_stats()?;
            print_stats(st.words, st.phrases, st.bigrams, stdout);
        }
        "clear" => {
            let before = client.user_stats()?;
            client.user_clear()?;
            let _ = writeln!(stdout, "已清空：词 {} 条、词组 {} 条、上下文 {} 条", before.words, before.phrases, before.bigrams);
        }
        "delete" => {
            let deleted = client.user_delete(word, sylls)?;
            print_delete_result(word, deleted, stdout);
        }
        "export" => {
            let tsv = client.user_export(phrases_only)?;
            write_out(out_file, &tsv, stdout)?;
        }
        _ => unreachable!(),
    }
    Ok(())
}

fn print_stats(words: u64, phrases: u64, bigrams: u64, stdout: &mut dyn Write) {
    let _ = writeln!(stdout, "词 {words} 条 / 词组 {phrases} 条 / 上下文 {bigrams} 条");
}

fn print_delete_result(word: &str, deleted: bool, stdout: &mut dyn Write) {
    if deleted {
        let _ = writeln!(stdout, "已删除「{word}」的学习记录");
    } else {
        let _ = writeln!(stdout, "未找到「{word}」的学习记录（可能未学习过或已删除）");
    }
}

pub fn write_out(out_file: Option<&Path>, content: &str, stdout: &mut dyn Write) -> Result<(), String> {
    let Some(path) = out_file else {
        write!(stdout, "{content}").map_err(|e| format!("写出导出文件失败: {e}"))?;
        return Ok(());
    };
    let dir = path.parent().unwrap_or_else(|| Path::new("."));
    let tmp = dir.join(format!(".chaoset-userdict-{}-{}.tmp", std::process::id(), nanos()));
    let result = (|| -> Result<(), String> {
        std::fs::write(&tmp, content).map_err(|e| format!("写出导出文件失败: {e}"))?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&tmp, std::fs::Permissions::from_mode(0o644))
                .map_err(|e| format!("设置导出文件权限失败: {e}"))?;
        }
        match std::fs::rename(&tmp, path) {
            Ok(()) => Ok(()),
            Err(_) if cfg!(windows) && path.exists() => {
                std::fs::remove_file(path).map_err(|e| format!("替换导出文件失败（旧文件保持不变）: {e}"))?;
                std::fs::rename(&tmp, path).map_err(|e| format!("替换导出文件失败（旧文件保持不变）: {e}"))
            }
            Err(e) => Err(format!("替换导出文件失败（旧文件保持不变）: {e}")),
        }
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
    result
}

fn nanos() -> u128 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0)
}
