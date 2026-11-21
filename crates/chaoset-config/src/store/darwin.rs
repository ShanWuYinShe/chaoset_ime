//! macOS 存储：`/usr/bin/defaults` 读写 UserDefaults 域。
//!
//! 域 = 输入法 app 的 CFBundleIdentifier（不是安装器）；键名与 Swift 壳逐字兼容。
//! 未设置 = `defaults read` 退出码 1；其它非零退出码/启动失败按错误上报（strict）。

use std::process::Command;

use crate::constants::{
    MAC_DEFAULTS_DOMAIN, MAC_KEY_ASSOCIATE, MAC_KEY_FUZZY, MAC_KEY_MODE_SCOPE, MAC_KEY_SHUANGPIN,
    MAC_KEY_THEME,
};
use crate::model::{ModeScope, Settings, Shuangpin, Theme};
use crate::parse;
use crate::store::SettingsStore;

pub const DEFAULTS_BIN: &str = "/usr/bin/defaults";

#[derive(Debug)]
pub enum RunOutcome {
    Done { code: i32, stdout: String, stderr: String },
    SpawnError(String),
}

pub trait CommandRunner: Send + Sync {
    fn run(&self, program: &str, args: &[&str]) -> RunOutcome;
}

pub struct StdCommandRunner;

impl CommandRunner for StdCommandRunner {
    fn run(&self, program: &str, args: &[&str]) -> RunOutcome {
        match Command::new(program).args(args).output() {
            Ok(out) => RunOutcome::Done {
                code: out.status.code().unwrap_or(-1),
                stdout: String::from_utf8_lossy(&out.stdout).trim().to_string(),
                stderr: String::from_utf8_lossy(&out.stderr).trim().to_string(),
            },
            Err(e) => RunOutcome::SpawnError(e.to_string()),
        }
    }
}

pub struct DarwinStore {
    pub domain: String,
    pub runner: Box<dyn CommandRunner>,
}

impl Default for DarwinStore {
    fn default() -> Self {
        Self { domain: MAC_DEFAULTS_DOMAIN.to_string(), runner: Box::new(StdCommandRunner) }
    }
}

impl DarwinStore {
    pub fn with_runner(domain: &str, runner: Box<dyn CommandRunner>) -> Self {
        Self { domain: domain.to_string(), runner }
    }

    fn read_raw(&self, key: &str) -> Result<Option<String>, String> {
        match self.runner.run(DEFAULTS_BIN, &["read", &self.domain, key]) {
            RunOutcome::Done { code: 1, .. } => Ok(None),
            RunOutcome::Done { code: 0, stdout, .. } => Ok(Some(stdout)),
            RunOutcome::Done { code, stderr, .. } => Err(format!(
                "读取 macOS 设置 {key} 失败: {DEFAULTS_BIN} 退出码 {code}（{stderr}）"
            )),
            RunOutcome::SpawnError(e) => Err(format!("读取 macOS 设置 {key} 失败: {e}")),
        }
    }

    fn write(&self, key: &str, type_flag: &str, value: &str) -> Result<(), String> {
        match self.runner.run(DEFAULTS_BIN, &["write", &self.domain, key, type_flag, value]) {
            RunOutcome::Done { code: 0, .. } => Ok(()),
            RunOutcome::Done { code, stderr, .. } => {
                Err(format!("写 macOS 设置 {key} 失败: {DEFAULTS_BIN} 退出码 {code}（{stderr}）"))
            }
            RunOutcome::SpawnError(e) => Err(format!("写 macOS 设置 {key} 失败: {e}")),
        }
    }
}

impl SettingsStore for DarwinStore {
    fn load(&self) -> Result<Settings, String> {
        let mut s = Settings::default();
        if let Some(raw) = self.read_raw(MAC_KEY_ASSOCIATE)? {
            s.associate = parse::parse_defaults_bool(&raw).map_err(|e| format!("解析 macOS 设置 {MAC_KEY_ASSOCIATE} 失败: {e}"))?;
        }
        if let Some(raw) = self.read_raw(MAC_KEY_FUZZY)? {
            s.fuzzy = parse::parse_defaults_bool(&raw).map_err(|e| format!("解析 macOS 设置 {MAC_KEY_FUZZY} 失败: {e}"))?;
        }
        if let Some(raw) = self.read_raw(MAC_KEY_SHUANGPIN)? {
            s.shuangpin = parse::normalize_shuangpin(&parse::parse_defaults_string(&raw));
        }
        if let Some(raw) = self.read_raw(MAC_KEY_MODE_SCOPE)? {
            s.mode_scope = parse::normalize_mode_scope(&parse::parse_defaults_string(&raw));
        }
        if let Some(raw) = self.read_raw(MAC_KEY_THEME)? {
            s.theme = parse::normalize_theme(&parse::parse_defaults_string(&raw));
        }
        Ok(s)
    }

    fn set_associate(&self, on: bool) -> Result<(), String> {
        self.write(MAC_KEY_ASSOCIATE, "-bool", if on { "true" } else { "false" })
    }

    fn set_fuzzy(&self, on: bool) -> Result<(), String> {
        self.write(MAC_KEY_FUZZY, "-bool", if on { "true" } else { "false" })
    }

    fn set_shuangpin(&self, value: Shuangpin) -> Result<(), String> {
        self.write(MAC_KEY_SHUANGPIN, "-string", value.as_str())
    }

    fn set_mode_scope(&self, value: ModeScope) -> Result<(), String> {
        self.write(MAC_KEY_MODE_SCOPE, "-string", value.as_str())
    }

    fn set_theme(&self, value: Theme) -> Result<(), String> {
        self.write(MAC_KEY_THEME, "-string", value.as_str())
    }

    fn set_ui_english(&self, _on: bool) -> Result<(), String> {
        Err("当前平台不支持该设置项: ui_english 仅 Windows 有持久化（macOS 壳不保存中英状态）".to_string())
    }
}
