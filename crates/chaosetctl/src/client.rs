//! `client` / `cli` 子命令：daemon 的终端交互测试台（不复制任何组词逻辑）。
//!
//! 输出/按键/退出码与旧 Go 测试台逐字兼容；连接失败明确报错，不回退本地引擎。

use std::io::{BufRead, Write};

use chaoset_ipc::dto::Composition;

use crate::buildinfo;

/// REPL 依赖的最小控制接口（真实实现是 `chaoset_ipc::client::Client`，测试用 Fake）。
pub trait Controller {
    fn input(&mut self, text: &str, half: bool) -> Result<Composition, String>;
    fn backspace(&mut self) -> Result<Composition, String>;
    fn backspace_word(&mut self) -> Result<Composition, String>;
    fn reset(&mut self) -> Result<Composition, String>;
    fn commit_raw(&mut self) -> Result<String, String>;
    fn next_page(&mut self) -> Result<Composition, String>;
    fn prev_page(&mut self) -> Result<Composition, String>;
    fn select_prefix(&mut self, index: usize) -> Result<(Composition, String), String>;
    #[allow(clippy::wrong_self_convention)]
    fn punct(&mut self, key: &str, half: bool) -> Result<String, String>;
}

pub fn run_cli(
    args: &[String],
    stdin: &mut dyn BufRead,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let mut socket: Option<String> = None;
    let mut use_pipe = false;
    let mut show_version = false;
    let mut i = 0;
    while i < args.len() {
        let arg = &args[i];
        let normalized = arg.trim_start_matches('-');
        match normalized {
            "socket" => {
                i += 1;
                if i >= args.len() {
                    let _ = writeln!(stderr, "flag 需要 -socket 参数值");
                    return 2;
                }
                socket = Some(args[i].clone());
            }
            "pipe" => use_pipe = true,
            "version" => show_version = true,
            "h" | "help" => {
                print_flag_usage(stderr);
                return 2;
            }
            other if !arg.starts_with('-') => {
                // Go flag 在首个非 flag 参数处停止解析并忽略后续参数。
                let _ = other;
                break;
            }
            other => {
                let _ = writeln!(stderr, "未知 flag: -{other}");
                print_flag_usage(stderr);
                return 2;
            }
        }
        i += 1;
    }
    if show_version {
        let _ = writeln!(stdout, "{}", buildinfo::info());
        return 0;
    }
    if use_pipe && socket.is_some() {
        let _ = writeln!(stderr, "-socket / -pipe 只能二选一");
        return 2;
    }
    let endpoint = if use_pipe {
        "pipe".to_string()
    } else if let Some(s) = &socket {
        format!("unix:{s}")
    } else {
        match chaoset_config::paths::socket_path() {
            Ok(p) => format!("unix:{}", p.display()),
            Err(e) => {
                let _ = writeln!(stderr, "无法确定默认 daemon socket： {e}");
                return 1;
            }
        }
    };
    let mut controller = match connect_controller(socket.as_deref(), use_pipe) {
        Ok(c) => c,
        Err(e) => {
            let _ = writeln!(stderr, "连接输入法 daemon 失败（{endpoint}）: {e}");
            return 1;
        }
    };
    let _ = writeln!(stdout, "chaoset 输入法测试台 —— 输入拼音回车看候选；单独数字 1-9 上屏对应候选；= 下一页 / - 上一页；标点键 , . ; : ? ! 等输出中文标点；:b 退格；:r 重置；:q 退出");
    run_repl(controller.as_mut(), stdin, stdout)
}

fn print_flag_usage(stderr: &mut dyn Write) {
    let _ = writeln!(stderr, "用法: chaosetctl client [-socket PATH | -pipe] [-version]");
    let _ = writeln!(stderr, "  -socket PATH  连接该 Unix 套接字上的 chaosetd；与 -pipe 二选一，缺省用平台默认 daemon socket");
    let _ = writeln!(stderr, "  -pipe         经 Windows 命名管道连接 chaosetd（按当前用户 SID 自动命名；仅 Windows）");
    let _ = writeln!(stderr, "  -version      打印构建信息（提交号/构建时间）后退出");
}

/// 真实连接（待 IPC 客户端接线）。
fn connect_controller(socket: Option<&str>, use_pipe: bool) -> Result<Box<dyn Controller>, String> {
    let client = if use_pipe {
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
        let path = match socket {
            Some(s) => std::path::PathBuf::from(s),
            None => chaoset_config::paths::socket_path()?,
        };
        chaoset_ipc::client::Client::connect_unix(&path)?
    };
    Ok(Box::new(ClientController { client }))
}

struct ClientController {
    client: chaoset_ipc::client::Client,
}

impl Controller for ClientController {
    fn input(&mut self, text: &str, half: bool) -> Result<Composition, String> { self.client.input(text, half) }
    fn backspace(&mut self) -> Result<Composition, String> { self.client.backspace() }
    fn backspace_word(&mut self) -> Result<Composition, String> { self.client.backspace_word() }
    fn reset(&mut self) -> Result<Composition, String> { self.client.reset() }
    fn commit_raw(&mut self) -> Result<String, String> { self.client.commit_raw() }
    fn next_page(&mut self) -> Result<Composition, String> { self.client.next_page() }
    fn prev_page(&mut self) -> Result<Composition, String> { self.client.prev_page() }
    fn select_prefix(&mut self, index: usize) -> Result<(Composition, String), String> { self.client.select_prefix(index) }
    fn punct(&mut self, key: &str, half: bool) -> Result<String, String> { self.client.punct(key, half) }
}

/// 交互循环（注入 controller，单测零 socket）。
pub fn run_repl(ctrl: &mut dyn Controller, stdin: &mut dyn BufRead, stdout: &mut dyn Write) -> i32 {
    let mut line = String::new();
    loop {
        let _ = write!(stdout, "> ");
        let _ = stdout.flush();
        line.clear();
        match stdin.read_line(&mut line) {
            Ok(0) | Err(_) => break,
            Ok(_) => {}
        }
        let cmd = line.trim();
        if cmd == ":q" {
            return 0;
        }
        if cmd.is_empty() {
            continue;
        }
        let result = match cmd {
            ":b" => ctrl.backspace().map(|c| CompositionPrint::Composition(c)),
            ":bw" => ctrl.backspace_word().map(CompositionPrint::Composition),
            ":r" | ":c" => ctrl.reset().map(CompositionPrint::Composition),
            ":enter" => ctrl.commit_raw().map(CompositionPrint::Commit),
            "=" | "]" => ctrl.next_page().map(CompositionPrint::Composition),
            "-" | "[" => ctrl.prev_page().map(CompositionPrint::Composition),
            _ if cmd.len() == 1 && cmd.as_bytes()[0].is_ascii_digit() && cmd != "0" => {
                let idx = (cmd.as_bytes()[0] - b'1') as usize;
                ctrl.select_prefix(idx).map(|(comp, committed)| CompositionPrint::Selection(comp, committed))
            }
            _ if cmd.len() == 1 && is_punct_key(cmd.chars().next().unwrap()) => {
                ctrl.punct(cmd, false).map(CompositionPrint::Commit)
            }
            _ => {
                match ctrl.input("", false) {
                    Ok(cur) if !cur.input.is_empty() => {
                        if let Err(e) = ctrl.reset() {
                            let _ = writeln!(stdout, "错误: {e}");
                            continue;
                        }
                    }
                    Ok(_) => {}
                    Err(e) => {
                        let _ = writeln!(stdout, "错误: {e}");
                        continue;
                    }
                }
                ctrl.input(cmd, false).map(CompositionPrint::Composition)
            }
        };
        match result {
            Ok(CompositionPrint::Composition(comp)) => print_composition(&comp, stdout),
            Ok(CompositionPrint::Commit(text)) => { let _ = writeln!(stdout, "上屏: {text}"); }
            Ok(CompositionPrint::Selection(comp, committed)) => {
                let _ = writeln!(stdout, "上屏: {committed}");
                if !comp.input.is_empty() {
                    print_composition(&comp, stdout);
                }
            }
            Err(e) => { let _ = writeln!(stdout, "错误: {e}"); }
        }
    }
    0
}

enum CompositionPrint {
    Composition(Composition),
    Commit(String),
    Selection(Composition, String),
}

fn is_punct_key(c: char) -> bool {
    ",.;:?!()[]<>\\~^'\"".contains(c)
}

pub fn print_composition(comp: &Composition, stdout: &mut dyn Write) {
    if comp.input.is_empty() {
        let _ = writeln!(stdout, "(空)");
        return;
    }
    let page_info = if comp.page_count > 1 {
        format!("   [{}/{} 页]", comp.page + 1, comp.page_count)
    } else {
        String::new()
    };
    let _ = writeln!(stdout, "输入: {}   预编辑: {}{}", comp.input, comp.preedit, page_info);
    if comp.candidates.is_empty() {
        let _ = writeln!(stdout, "  (无候选)");
        return;
    }
    let parts: Vec<String> = comp
        .candidates
        .iter()
        .enumerate()
        .map(|(i, c)| format!("{}.{}{}", i + 1, c.text, if c.from_user { "*" } else { "" }))
        .collect();
    let _ = writeln!(stdout, "  {}", parts.join("  "));
}

#[cfg(test)]
mod tests {
    use super::*;
    use chaoset_ipc::dto::{Candidate, Composition};

    struct Fake {
        comp: Composition,
        fail: bool,
        calls: Vec<String>,
    }

    impl Fake {
        fn new() -> Self {
            Self {
                comp: Composition { input: "ni'hao".into(), preedit: "ni'hao".into(), candidates: vec![Candidate { text: "你好".into(), syllables: "ni'hao".into(), score: 1, from_user: true }], page: 0, page_count: 1, committed: String::new(), shuangpin: String::new() },
                fail: false,
                calls: Vec::new(),
            }
        }
    }

    impl Controller for Fake {
        fn input(&mut self, text: &str, _half: bool) -> Result<Composition, String> { self.calls.push(format!("input:{text}")); if self.fail { Err("boom".into()) } else { Ok(self.comp.clone()) } }
        fn backspace(&mut self) -> Result<Composition, String> { self.calls.push("backspace".into()); Ok(self.comp.clone()) }
        fn backspace_word(&mut self) -> Result<Composition, String> { Ok(self.comp.clone()) }
        fn reset(&mut self) -> Result<Composition, String> { self.calls.push("reset".into()); Ok(Composition::default()) }
        fn commit_raw(&mut self) -> Result<String, String> { Ok("上屏文本".into()) }
        fn next_page(&mut self) -> Result<Composition, String> { Ok(self.comp.clone()) }
        fn prev_page(&mut self) -> Result<Composition, String> { Ok(self.comp.clone()) }
        fn select_prefix(&mut self, _index: usize) -> Result<(Composition, String), String> { Ok((Composition::default(), "你好".into())) }
        fn punct(&mut self, _key: &str, _half: bool) -> Result<String, String> { Ok("，".into()) }
    }

    fn run_inputs(input: &str) -> (i32, String) {
        let mut fake = Fake::new();
        let mut stdin = input.as_bytes();
        let mut out = Vec::new();
        let code = run_repl(&mut fake, &mut stdin, &mut out);
        (code, String::from_utf8(out).unwrap())
    }

    #[test]
    fn prints_banner_style_and_composition() {
        let (code, out) = run_inputs("nihao\n:q\n");
        assert_eq!(code, 0);
        assert!(out.contains("输入: ni'hao   预编辑: ni'hao"));
        assert!(out.contains("  1.你好*"));
    }

    #[test]
    fn selection_prints_committed() {
        let (_, out) = run_inputs("1\n:q\n");
        assert!(out.contains("上屏: 你好"));
    }

    #[test]
    fn errors_go_stdout_and_loop_continues() {
        let mut fake = Fake::new();
        fake.fail = true;
        let mut stdin = ":b\n:q\n".as_bytes();
        let mut out = Vec::new();
        assert_eq!(run_repl(&mut fake, &mut stdin, &mut out), 0);
    }

    #[test]
    fn empty_composition_prints_placeholder() {
        let mut fake = Fake::new();
        fake.comp = Composition::default();
        let mut stdin = "x\n:q\n".as_bytes();
        let mut out = Vec::new();
        run_repl(&mut fake, &mut stdin, &mut out);
        assert!(String::from_utf8(out).unwrap().contains("(空)"));
    }

    #[test]
    fn pipe_on_unix_fails_cleanly() {
        let mut stdin: &[u8] = b"";
        let mut out = Vec::new();
        let mut err = Vec::new();
        let code = run_cli(&["-pipe".into()], &mut stdin, &mut out, &mut err);
        if cfg!(windows) {
            return;
        }
        assert_eq!(code, 1);
        assert!(String::from_utf8(err).unwrap().contains("连接输入法 daemon 失败"));
    }
}
