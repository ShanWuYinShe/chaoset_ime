//! chaoset-config：Chaoset IME 三端统一设置与路径的 **Rust 唯一实现**。
//!
//! 冷路径（Rust CLI `chaosetctl config …`、设置中心经 CLI）用 `store::load` /
//! `store::set_*`（strict：存储损坏 → 报错，避免覆盖用户配置）；
//! 热路径（daemon 启动读 fuzzy/shuangpin）用 `behavior::load_input_behavior`
//! （lenient：永不失败，读不到就用缺省，绝不让输入法起不来）。
//!
//! 三端存储键名/默认值/JSON 契约与历史 Go `internal/settings` 逐字兼容：
//!   macOS   UserDefaults 域 ime.chaoset.inputmethod.ChaosetIME（/usr/bin/defaults）
//!   Windows HKCU\\Software\\Chaoset
//!   Linux   $XDG_CONFIG_HOME/chaoset/config.json（2 空格缩进 + 末尾换行，保留未知键）

pub mod behavior;
pub mod constants;
pub mod model;
pub mod parse;
pub mod paths;
pub mod store;

pub use model::{ModeScope, Settings, Shuangpin, Theme};
