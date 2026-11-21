//! 三端存储键名/路径/CLI 名字的唯一常量表。
//!
//! 这些字符串是**跨进程契约**：Go CLI、Slint 设置中心、daemon、三端原生壳
//! 都按同一份键名读写；任何一处少一个反斜杠/下划线都会静默写到另一份数据。
//! 平台无关单测锁定这里的值（见 tests/constants_lock.rs）。

pub const APP_NAME: &str = "chaoset";

/// Windows 注册表子键（HKCU 下）。必须与 C 壳 config.cpp 的 L"Software\\Chaoset" 一致。
pub const WINDOWS_REG_SUBKEY: &str = r"Software\Chaoset";
pub const WIN_VALUE_ASSOCIATE: &str = "Associate";
pub const WIN_VALUE_FUZZY: &str = "Fuzzy";
pub const WIN_VALUE_SHUANGPIN: &str = "Shuangpin";
pub const WIN_VALUE_MODE_SCOPE: &str = "ModeScope";
pub const WIN_VALUE_UI_ENGLISH: &str = "UiEnglish";
pub const WIN_VALUE_THEME: &str = "Theme";

/// macOS UserDefaults 域 = 输入法 app 的 CFBundleIdentifier（不是安装器）。
pub const MAC_DEFAULTS_DOMAIN: &str = "ime.chaoset.inputmethod.ChaosetIME";
pub const MAC_KEY_ASSOCIATE: &str = "AssociateEnabled";
pub const MAC_KEY_FUZZY: &str = "ChaosetFuzzy";
pub const MAC_KEY_SHUANGPIN: &str = "ChaosetShuangpin";
pub const MAC_KEY_MODE_SCOPE: &str = "ChaosetModeScope";
pub const MAC_KEY_THEME: &str = "ChaosetTheme";

/// Linux config.json 的 JSON 键（XDG_CONFIG_HOME/chaoset/config.json）。
pub const LINUX_KEY_ASSOCIATE: &str = "associate";
pub const LINUX_KEY_FUZZY: &str = "fuzzy";
pub const LINUX_KEY_SHUANGPIN: &str = "shuangpin";
pub const LINUX_KEY_MODE_SCOPE: &str = "mode_scope";
pub const LINUX_KEY_THEME: &str = "theme";

/// CLI 设置项命令名：**连字符**（与历史 bug 的 `mode_scope` 相对）。
pub const CLI_ITEM_MODE_SCOPE: &str = "mode-scope";

/// 默认 JSON 黄金串（设置中心 / 脚本解析契约，字段顺序固定）。
pub const DEFAULTS_JSON: &str = r#"{"associate":true,"fuzzy":false,"shuangpin":"","mode_scope":"system","ui_english":false,"theme":"system"}"#;
pub const ALL_VALUES_JSON: &str = r#"{"associate":false,"fuzzy":true,"shuangpin":"zrm","mode_scope":"process","ui_english":true,"theme":"dark"}"#;
