//! `config` / `settings` / `settingsctl` 子命令：读写三端统一设置项。
//!
//! 输出/退出码/错误前缀与旧 Go `chaoset-settingsctl` 逐字兼容：
//!   0 成功；1 运行错误（存储不可用/平台不支持）；2 参数非法。

use std::io::Write;

use chaoset_config::parse;
use chaoset_config::store::{self, SettingsStore};

pub const USAGE_TEXT: &str = r#"用法：
  chaoset-settingsctl get
  chaoset-settingsctl set associate on|off
  chaoset-settingsctl set fuzzy on|off
  chaoset-settingsctl set shuangpin full|zrm|mspy|flypy
  chaoset-settingsctl set mode-scope system|process
  chaoset-settingsctl set theme system|light|dark
  chaoset-settingsctl set ui-english on|off

get 输出单行 JSON：{\"associate\":true,\"fuzzy\":false,\"shuangpin\":\"\",\"mode_scope\":\"system\",\"ui_english\":false,\"theme\":\"system\"}
fuzzy 是模糊音开关（zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing）。
shuangpin 取值：full 全拼（默认，等同空串）/ zrm 自然码 / mspy 微软 / flypy 小鹤。
mode-scope 同时接受旧三档值 window 并归一化（window→process）。
theme 取值：system 跟随系统 / light 浅色 / dark 深色。
退出码：0 成功；1 运行错误；2 参数非法。错误写 stderr。"#;

pub fn run(args: &[String], stdout: &mut dyn Write, stderr: &mut dyn Write) -> i32 {
    let store = store::default_store();
    run_with(store.as_ref(), args, stdout, stderr)
}

/// 可注入 store 的入口（单测不碰真实用户配置）。
pub fn run_with(
    store: &dyn SettingsStore,
    args: &[String],
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> i32 {
    let Some(subcmd) = args.first() else {
        return usage_error(stderr, "缺少子命令");
    };
    match subcmd.as_str() {
        "help" | "-h" | "--help" => {
            if args.len() != 1 {
                return usage_error(stderr, &format!("{subcmd} 不接受额外参数"));
            }
            let _ = writeln!(stdout, "{USAGE_TEXT}");
            0
        }
        "get" => {
            if args.len() != 1 {
                return usage_error(stderr, "get 不接受参数");
            }
            let settings = match store.load() {
                Ok(s) => s,
                Err(e) => return fail(stderr, 1, &format!("读取设置失败: {e}")),
            };
            let line = match settings.to_json() {
                Ok(l) => l,
                Err(e) => return fail(stderr, 1, &format!("生成设置 JSON 失败: {e}")),
            };
            let _ = writeln!(stdout, "{line}");
            0
        }
        "set" => run_set(store, &args[1..], stderr),
        other => usage_error(stderr, &format!("未知子命令 {other:?}")),
    }
}

fn run_set(store: &dyn SettingsStore, args: &[String], stderr: &mut dyn Write) -> i32 {
    let Some(name) = args.first() else {
        return usage_error(stderr, "set 缺少设置项");
    };
    if args.len() > 2 {
        return usage_error(stderr, &format!("set {name} 只接受一个取值"));
    }
    let value = args.get(1).map(String::as_str).unwrap_or("");
    match name.as_str() {
        "associate" => {
            if args.len() != 2 { return usage_error(stderr, "set associate 需要 on|off"); }
            match parse::parse_on_off(value) {
                Ok(on) => match store.set_associate(on) {
                    Ok(()) => 0,
                    Err(e) => fail(stderr, 1, &format!("写入 associate 失败: {e}")),
                },
                Err(e) => fail(stderr, 2, &format!("set associate: {e}")),
            }
        }
        "fuzzy" => {
            if args.len() != 2 { return usage_error(stderr, "set fuzzy 需要 on|off"); }
            match parse::parse_on_off(value) {
                Ok(on) => match store.set_fuzzy(on) {
                    Ok(()) => 0,
                    Err(e) => fail(stderr, 1, &format!("写入 fuzzy 失败: {e}")),
                },
                Err(e) => fail(stderr, 2, &format!("set fuzzy: {e}")),
            }
        }
        "shuangpin" => {
            if args.len() != 2 { return usage_error(stderr, "set shuangpin 需要 full|zrm|mspy|flypy"); }
            match parse::parse_shuangpin(value) {
                Ok(scheme) => match store.set_shuangpin(scheme) {
                    Ok(()) => 0,
                    Err(e) => fail(stderr, 1, &format!("写入 shuangpin 失败: {e}")),
                },
                Err(e) => fail(stderr, 2, &format!("set shuangpin: {e}")),
            }
        }
        "mode-scope" => {
            if args.len() != 2 { return usage_error(stderr, "set mode-scope 需要 system|process"); }
            match parse::parse_mode_scope(value) {
                Ok(scope) => match store.set_mode_scope(scope) {
                    Ok(()) => 0,
                    Err(e) => fail(stderr, 1, &format!("写入 mode-scope 失败: {e}")),
                },
                Err(e) => fail(stderr, 2, &format!("set mode-scope: {e}")),
            }
        }
        "theme" => {
            if args.len() != 2 { return usage_error(stderr, "set theme 需要 system|light|dark"); }
            match parse::parse_theme(value) {
                Ok(theme) => match store.set_theme(theme) {
                    Ok(()) => 0,
                    Err(e) => fail(stderr, 1, &format!("写入 theme 失败: {e}")),
                },
                Err(e) => fail(stderr, 2, &format!("set theme: {e}")),
            }
        }
        "ui-english" => {
            if args.len() != 2 { return usage_error(stderr, "set ui-english 需要 on|off"); }
            match parse::parse_on_off(value) {
                Ok(on) => match store.set_ui_english(on) {
                    Ok(()) => 0,
                    Err(e) => fail(stderr, 1, &format!("写入 ui-english 失败: {e}")),
                },
                Err(e) => fail(stderr, 2, &format!("set ui-english: {e}")),
            }
        }
        other => usage_error(stderr, &format!("未知设置项 {other:?}")),
    }
}

fn fail(stderr: &mut dyn Write, code: i32, message: &str) -> i32 {
    let _ = writeln!(stderr, "chaoset-settingsctl: {message}");
    code
}

fn usage_error(stderr: &mut dyn Write, message: &str) -> i32 {
    let _ = writeln!(stderr, "chaoset-settingsctl: {message}");
    let _ = writeln!(stderr, "{USAGE_TEXT}");
    2
}

#[cfg(test)]
mod tests {
    use super::*;
    use chaoset_config::model::{ModeScope, Settings, Shuangpin, Theme};
    use std::cell::RefCell;

    #[derive(Default)]
    struct MemoryStore {
        settings: RefCell<Settings>,
        writes: RefCell<Vec<String>>,
    }

    impl SettingsStore for MemoryStore {
        fn load(&self) -> Result<Settings, String> { Ok(self.settings.borrow().clone()) }
        fn set_associate(&self, on: bool) -> Result<(), String> { self.settings.borrow_mut().associate = on; self.writes.borrow_mut().push("associate".into()); Ok(()) }
        fn set_fuzzy(&self, on: bool) -> Result<(), String> { self.settings.borrow_mut().fuzzy = on; self.writes.borrow_mut().push("fuzzy".into()); Ok(()) }
        fn set_shuangpin(&self, value: Shuangpin) -> Result<(), String> { self.settings.borrow_mut().shuangpin = value; self.writes.borrow_mut().push("shuangpin".into()); Ok(()) }
        fn set_mode_scope(&self, value: ModeScope) -> Result<(), String> { self.settings.borrow_mut().mode_scope = value; self.writes.borrow_mut().push("mode-scope".into()); Ok(()) }
        fn set_theme(&self, value: Theme) -> Result<(), String> { self.settings.borrow_mut().theme = value; self.writes.borrow_mut().push("theme".into()); Ok(()) }
        fn set_ui_english(&self, on: bool) -> Result<(), String> { self.settings.borrow_mut().ui_english = on; self.writes.borrow_mut().push("ui-english".into()); Ok(()) }
    }

    fn run_args(store: &MemoryStore, args: &[&str]) -> (i32, String, String) {
        let args: Vec<String> = args.iter().map(|s| s.to_string()).collect();
        let mut out = Vec::new();
        let mut err = Vec::new();
        let code = run_with(store, &args, &mut out, &mut err);
        (code, String::from_utf8(out).unwrap(), String::from_utf8(err).unwrap())
    }

    #[test]
    fn get_outputs_golden_json() {
        let store = MemoryStore::default();
        let (code, out, err) = run_args(&store, &["get"]);
        assert_eq!(code, 0);
        assert_eq!(out, "{\"associate\":true,\"fuzzy\":false,\"shuangpin\":\"\",\"mode_scope\":\"system\",\"ui_english\":false,\"theme\":\"system\"}\n");
        assert!(err.is_empty());
    }

    #[test]
    fn set_writes_parsed_values() {
        let store = MemoryStore::default();
        assert_eq!(run_args(&store, &["set", "associate", "OFF"]).0, 0);
        assert_eq!(run_args(&store, &["set", "fuzzy", "on"]).0, 0);
        assert_eq!(run_args(&store, &["set", "shuangpin", "full"]).0, 0);
        assert_eq!(run_args(&store, &["set", "mode-scope", "WINDOW"]).0, 0);
        assert_eq!(run_args(&store, &["set", "theme", "Light"]).0, 0);
        assert_eq!(run_args(&store, &["set", "ui-english", "on"]).0, 0);
        let s = store.settings.borrow().clone();
        assert!(!s.associate);
        assert!(s.fuzzy);
        assert_eq!(s.shuangpin, Shuangpin::Full);
        assert_eq!(s.mode_scope, ModeScope::Process);
        assert_eq!(s.theme, Theme::Light);
        assert!(s.ui_english);
    }

    #[test]
    fn usage_errors_exit_2_with_empty_stdout() {
        let store = MemoryStore::default();
        for args in [&[][..], &["get", "x"][..], &["set"][..], &["set", "associate"][..], &["set", "bogus", "on"][..], &["set", "theme", "bogus"][..], &["set", "mode_scope", "system"][..], &["unknown"][..]] {
            let (code, out, err) = run_args(&store, args);
            assert_eq!(code, 2, "args={args:?} err={err}");
            assert!(out.is_empty(), "args={args:?}");
            assert!(err.starts_with("chaoset-settingsctl: "), "args={args:?}");
        }
    }

    #[test]
    fn help_is_zero_and_contains_contract() {
        let store = MemoryStore::default();
        let (code, out, err) = run_args(&store, &["help"]);
        assert_eq!(code, 0);
        assert!(out.contains("chaoset-settingsctl get"));
        assert!(err.is_empty());
    }
}
