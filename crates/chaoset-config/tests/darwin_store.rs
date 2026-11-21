//! macOS 存储：用注入 runner 断言实际执行的 defaults 参数（不碰真实用户配置）。
#![cfg(target_os = "macos")]

use std::sync::Mutex;

use chaoset_config::constants::MAC_DEFAULTS_DOMAIN;
use chaoset_config::model::{ModeScope, Shuangpin, Theme};
use chaoset_config::store::darwin::{CommandRunner, DarwinStore, RunOutcome};
use chaoset_config::store::SettingsStore;

struct FakeRunner {
    calls: Mutex<Vec<Vec<String>>>,
    replies: Mutex<Vec<RunOutcome>>,
}

impl FakeRunner {
    fn new() -> Self {
        Self { calls: Mutex::new(Vec::new()), replies: Mutex::new(Vec::new()) }
    }
    fn push(&self, outcome: RunOutcome) {
        self.replies.lock().unwrap().push(outcome);
    }
    fn calls(&self) -> Vec<Vec<String>> { self.calls.lock().unwrap().clone() }
}

impl CommandRunner for FakeRunner {
    fn run(&self, program: &str, args: &[&str]) -> RunOutcome {
        let mut call = vec![program.to_string()];
        call.extend(args.iter().map(|s| s.to_string()));
        self.calls.lock().unwrap().push(call);
        if self.replies.lock().unwrap().is_empty() {
            return RunOutcome::Done { code: 1, stdout: String::new(), stderr: String::new() };
        }
        self.replies.lock().unwrap().remove(0)
    }
}

fn done(code: i32, stdout: &str) -> RunOutcome {
    RunOutcome::Done { code, stdout: stdout.to_string(), stderr: String::new() }
}

#[test]
fn load_reads_five_keys_in_order() {
    let runner = std::sync::Arc::new(FakeRunner::new());
    runner.push(done(0, "1"));
    runner.push(done(0, "0"));
    runner.push(done(0, "\"zrm\""));
    runner.push(done(0, "window"));
    runner.push(done(0, "dark"));
    struct Shared(std::sync::Arc<FakeRunner>);
    impl CommandRunner for Shared {
        fn run(&self, p: &str, a: &[&str]) -> RunOutcome { self.0.run(p, a) }
    }
    let store = DarwinStore::with_runner(MAC_DEFAULTS_DOMAIN, Box::new(Shared(runner.clone())));
    let s = store.load().unwrap();
    assert!(s.associate);
    assert!(!s.fuzzy);
    assert_eq!(s.shuangpin, Shuangpin::Zrm);
    assert_eq!(s.mode_scope, ModeScope::Process);
    assert_eq!(s.theme, Theme::Dark);
    let calls = runner.calls();
    assert_eq!(calls.len(), 5);
    let keys = ["AssociateEnabled", "ChaosetFuzzy", "ChaosetShuangpin", "ChaosetModeScope", "ChaosetTheme"];
    for (i, key) in keys.iter().enumerate() {
        assert_eq!(calls[i], vec!["/usr/bin/defaults".to_string(), "read".to_string(), MAC_DEFAULTS_DOMAIN.to_string(), key.to_string()]);
    }
}

#[test]
fn unset_exit_1_defaults_other_codes_error() {
    struct Once(RunOutcome);
    impl CommandRunner for Once {
        fn run(&self, _p: &str, _a: &[&str]) -> RunOutcome {
            RunOutcome::Done { code: 1, stdout: String::new(), stderr: String::new() }
        }
    }
    let store = DarwinStore::with_runner(MAC_DEFAULTS_DOMAIN, Box::new(Once(RunOutcome::SpawnError(String::new()))));
    assert_eq!(store.load().unwrap(), chaoset_config::Settings::default());

    struct Err255;
    impl CommandRunner for Err255 {
        fn run(&self, _p: &str, _a: &[&str]) -> RunOutcome { done_with(255) }
    }
    let store = DarwinStore::with_runner(MAC_DEFAULTS_DOMAIN, Box::new(Err255));
    assert!(store.load().is_err());
}

fn done_with(code: i32) -> RunOutcome {
    RunOutcome::Done { code, stdout: String::new(), stderr: String::new() }
}

#[test]
fn writes_are_exact() {
    let runner = std::sync::Arc::new(FakeRunner::new());
    for _ in 0..5 { runner.push(done(0, "")); }
    struct Shared(std::sync::Arc<FakeRunner>);
    impl CommandRunner for Shared {
        fn run(&self, p: &str, a: &[&str]) -> RunOutcome { self.0.run(p, a) }
    }
    let store = DarwinStore::with_runner(MAC_DEFAULTS_DOMAIN, Box::new(Shared(runner.clone())));
    store.set_associate(false).unwrap();
    store.set_fuzzy(true).unwrap();
    store.set_shuangpin(Shuangpin::Flypy).unwrap();
    store.set_mode_scope(ModeScope::Process).unwrap();
    store.set_theme(Theme::Light).unwrap();
    let calls = runner.calls();
    assert_eq!(calls[0], vec!["/usr/bin/defaults", "write", MAC_DEFAULTS_DOMAIN, "AssociateEnabled", "-bool", "false"]);
    assert_eq!(calls[1], vec!["/usr/bin/defaults", "write", MAC_DEFAULTS_DOMAIN, "ChaosetFuzzy", "-bool", "true"]);
    assert_eq!(calls[2], vec!["/usr/bin/defaults", "write", MAC_DEFAULTS_DOMAIN, "ChaosetShuangpin", "-string", "flypy"]);
    assert_eq!(calls[3], vec!["/usr/bin/defaults", "write", MAC_DEFAULTS_DOMAIN, "ChaosetModeScope", "-string", "process"]);
    assert_eq!(calls[4], vec!["/usr/bin/defaults", "write", MAC_DEFAULTS_DOMAIN, "ChaosetTheme", "-string", "light"]);
}

#[test]
fn ui_english_unsupported_without_spawn() {
    let runner = std::sync::Arc::new(FakeRunner::new());
    struct Shared(std::sync::Arc<FakeRunner>);
    impl CommandRunner for Shared {
        fn run(&self, p: &str, a: &[&str]) -> RunOutcome { self.0.run(p, a) }
    }
    let store = DarwinStore::with_runner(MAC_DEFAULTS_DOMAIN, Box::new(Shared(runner.clone())));
    assert!(store.set_ui_english(true).is_err());
    assert!(runner.calls().is_empty());
}
