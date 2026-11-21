//! 平台存储抽象：strict（CLI 用，损坏/失败要报错）与默认实现。

use crate::model::{ModeScope, Settings, Shuangpin, Theme};

pub trait SettingsStore {
    fn load(&self) -> Result<Settings, String>;
    fn set_associate(&self, on: bool) -> Result<(), String>;
    fn set_fuzzy(&self, on: bool) -> Result<(), String>;
    fn set_shuangpin(&self, value: Shuangpin) -> Result<(), String>;
    fn set_mode_scope(&self, value: ModeScope) -> Result<(), String>;
    fn set_theme(&self, value: Theme) -> Result<(), String>;
    fn set_ui_english(&self, on: bool) -> Result<(), String>;
}

#[cfg(target_os = "macos")]
pub mod darwin;
#[cfg(unix)]
pub mod linux;
#[cfg(target_os = "windows")]
pub mod windows;

#[cfg(target_os = "macos")]
pub fn default_store() -> Box<dyn SettingsStore> {
    Box::new(darwin::DarwinStore::default())
}

#[cfg(target_os = "linux")]
pub fn default_store() -> Box<dyn SettingsStore> {
    Box::new(linux::LinuxStore::default())
}

#[cfg(target_os = "windows")]
pub fn default_store() -> Box<dyn SettingsStore> {
    Box::new(windows::WindowsStore::default())
}

// 便捷自由函数（与旧 Go `settings.Load/SetXxx` 一一对应）。

pub fn load() -> Result<Settings, String> { default_store().load() }
pub fn set_associate(on: bool) -> Result<(), String> { default_store().set_associate(on) }
pub fn set_fuzzy(on: bool) -> Result<(), String> { default_store().set_fuzzy(on) }
pub fn set_shuangpin(value: Shuangpin) -> Result<(), String> { default_store().set_shuangpin(value) }
pub fn set_mode_scope(value: ModeScope) -> Result<(), String> { default_store().set_mode_scope(value) }
pub fn set_theme(value: Theme) -> Result<(), String> { default_store().set_theme(value) }
pub fn set_ui_english(on: bool) -> Result<(), String> { default_store().set_ui_english(on) }
