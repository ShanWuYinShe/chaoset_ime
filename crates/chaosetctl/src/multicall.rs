//! argv[0] 多调用与子命令分发（与旧 Go `cmd/chaosetctl/main.go` 行为一致）。

use std::ffi::OsString;
use std::io::{BufRead, Write};
use std::path::{Path, PathBuf};

use crate::{buildinfo, client, importcmd, settingsctl, userdictcmd};

pub const USAGE_TEXT: &str = r#"Chaoset 拼音输入法全能命令行控制工具 (chaosetctl)

用法:
  chaosetctl <command> [flags...] [args...]

可用命令:
  config | settings    读取或写入输入法设置项（开关/模式/主题）
  import               导入与合并词库文件（TSV 或 Rime dict.yaml）
  userdict             管理用户自学习词典（查看统计/导出/清空/按词删除）
  client | cli         终端交互测试台与 IPC 链路测试
  version              显示版本信息
  help                 显示帮助说明

多调用模式 (Multicall):
  当程序名（argv[0]）为 chaoset-import、chaoset-userdict、chaoset-settingsctl、
  chaoset-cli 时，将自动分发至对应的子命令执行。

输入 'chaosetctl <command> -h' 查看各子命令的详细用法。"#;

/// 规范化 argv[0]：取文件名、转小写、去掉 `.exe`。
pub fn normalize_argv0(prog: &OsString) -> String {
    let base = Path::new(prog)
        .file_name()
        .map(|s| s.to_string_lossy().to_ascii_lowercase())
        .unwrap_or_default();
    base.strip_suffix(".exe").unwrap_or(&base).to_string()
}

pub fn run(
    argv: Vec<OsString>,
    stdin: &mut dyn BufRead,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let prog = argv.first().cloned().unwrap_or_default();
    let args: Vec<String> = argv.iter().skip(1).map(|s| s.to_string_lossy().to_string()).collect();
    let base = normalize_argv0(&prog);

    // 1) argv[0] 多调用
    match base.as_str() {
        "chaoset-import" => return importcmd::run(&args, stdout, stderr),
        "chaoset-userdict" => return userdictcmd::run(&args, stdout, stderr, None),
        "chaoset-settingsctl" => return settingsctl::run(&args, stdout, stderr),
        "chaoset-settings" => {
            let first = args.first().map(String::as_str);
            if let Some(first) = first {
                if first != "gui" && first != "--gui" && !first.starts_with('-') {
                    return settingsctl::run(&args, stdout, stderr);
                }
            }
            return launch_settings_gui(&args, stdout, stderr);
        }
        "chaoset-cli" => return client::run_cli(&args, stdin, stdout, stderr),
        _ => {}
    }

    // 2) 子命令分发
    if args.is_empty() {
        let _ = writeln!(stdout, "{USAGE_TEXT}");
        return 0;
    }
    let subcmd = args[0].as_str();
    let subargs = &args[1..];
    match subcmd {
        "help" | "-h" | "--help" => {
            let _ = writeln!(stdout, "{USAGE_TEXT}");
            0
        }
        "version" | "-version" | "--version" | "-v" => {
            let _ = writeln!(stdout, "{}", buildinfo::info());
            0
        }
        "import" | "dict-import" => importcmd::run(subargs, stdout, stderr),
        "userdict" => userdictcmd::run(subargs, stdout, stderr, None),
        "settingsctl" | "settings" | "config" => {
            if subargs.first().map(String::as_str) == Some("gui") || subargs.first().map(String::as_str) == Some("--gui") {
                return launch_settings_gui(&subargs[1..], stdout, stderr);
            }
            settingsctl::run(subargs, stdout, stderr)
        }
        "get" | "set" => settingsctl::run(&args, stdout, stderr),
        "cli" | "client" | "test" => client::run_cli(subargs, stdin, stdout, stderr),
        other => {
            let _ = writeln!(stderr, "未知子命令 {other:?}");
            let _ = writeln!(stderr);
            let _ = writeln!(stderr, "{USAGE_TEXT}");
            2
        }
    }
}

/// 拉起同目录/ PATH 中的设置中心；子进程退出码透传。
fn launch_settings_gui(args: &[String], stdout: &mut dyn Write, stderr: &mut dyn Write) -> i32 {
    let exe_name = if cfg!(windows) { "chaoset-settings.exe" } else { "chaoset-settings" };
    let mut candidates: Vec<PathBuf> = Vec::new();
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            candidates.push(dir.join(exe_name));
        }
    }
    if let Ok(path_var) = std::env::var("PATH") {
        for dir in std::env::split_paths(&path_var) {
            candidates.push(dir.join(exe_name));
        }
    }
    let Some(bin) = candidates.into_iter().find(|p| p.is_file()) else {
        let _ = writeln!(stderr, "未找到 {exe_name} 可执行文件");
        return 1;
    };
    match std::process::Command::new(&bin).args(args).status() {
        Ok(status) => status.code().unwrap_or(1),
        Err(e) => {
            let _ = writeln!(stderr, "启动设置窗口失败: {e}");
            1
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn argv(items: &[&str]) -> Vec<OsString> { items.iter().map(OsString::from).collect() }

    fn run_argv(items: &[&str]) -> (i32, String, String) {
        let mut stdin: &[u8] = b"";
        let mut out = Vec::new();
        let mut err = Vec::new();
        let code = run(argv(items), &mut stdin, &mut out, &mut err);
        (code, String::from_utf8(out).unwrap(), String::from_utf8(err).unwrap())
    }

    #[test]
    fn argv0_normalization() {
        assert_eq!(normalize_argv0(&OsString::from("/usr/bin/CHAOSET-IMPORT.EXE")), "chaoset-import");
        assert_eq!(normalize_argv0(&OsString::from("chaosetctl")), "chaosetctl");
    }

    #[test]
    fn no_args_and_help_exit_zero() {
        let (code, out, err) = run_argv(&["chaosetctl"]);
        assert_eq!(code, 0);
        assert!(out.contains("Chaoset 拼音输入法全能命令行控制工具"));
        assert!(err.is_empty());
        let (code, out, _) = run_argv(&["chaosetctl", "help"]);
        assert_eq!(code, 0);
        assert!(out.contains("可用命令"));
    }

    #[test]
    fn version_is_single_line() {
        let (code, out, _) = run_argv(&["chaosetctl", "version"]);
        assert_eq!(code, 0);
        assert!(out.starts_with("chaoset "));
        assert_eq!(out.lines().count(), 1);
    }

    #[test]
    fn unknown_subcommand_is_two() {
        let (code, out, err) = run_argv(&["chaosetctl", "bogus"]);
        assert_eq!(code, 2);
        assert!(out.is_empty());
        assert!(err.contains("未知子命令"));
    }

    #[test]
    fn multicall_settingsctl_help() {
        let (code, out, _) = run_argv(&["chaoset-settingsctl", "help"]);
        assert_eq!(code, 0);
        assert!(out.contains("chaoset-settingsctl get"));
    }

    #[test]
    fn multicall_import_without_args_is_two() {
        let (code, _, err) = run_argv(&["chaoset-import"]);
        assert_eq!(code, 2);
        assert!(!err.is_empty());
    }
}
