//! 设置项 schema、默认值、校验与 JSON 契约。
//!
//! 与旧 Go `internal/settings/settings.go` 逐字段等价：字段名/顺序/默认值/
//! 归一化规则都是跨进程契约，改任何一个都会和用户已存储的配置/脚本解析对不上。

use std::fmt;

/// 双拼方案。`Full` 序列化为空串（全拼），其余为 scheme 名。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Shuangpin {
    Full,
    Zrm,
    Mspy,
    Flypy,
}

impl Shuangpin {
    pub fn as_str(self) -> &'static str {
        match self {
            Shuangpin::Full => "",
            Shuangpin::Zrm => "zrm",
            Shuangpin::Mspy => "mspy",
            Shuangpin::Flypy => "flypy",
        }
    }
}

/// 中英状态作用域：两档（旧三档 window 只在读取/CLI 解析时归一化为 process）。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ModeScope {
    System,
    Process,
}

impl ModeScope {
    pub fn as_str(self) -> &'static str {
        match self {
            ModeScope::System => "system",
            ModeScope::Process => "process",
        }
    }
}

/// 外观主题三档。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Theme {
    System,
    Light,
    Dark,
}

impl Theme {
    pub fn as_str(self) -> &'static str {
        match self {
            Theme::System => "system",
            Theme::Light => "light",
            Theme::Dark => "dark",
        }
    }
}

/// 三端统一设置项。字段顺序 = JSON 契约顺序，禁止调整。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Settings {
    pub associate: bool,
    pub fuzzy: bool,
    pub shuangpin: Shuangpin,
    pub mode_scope: ModeScope,
    pub ui_english: bool,
    pub theme: Theme,
}

impl Default for Settings {
    fn default() -> Self {
        Self {
            associate: true,
            fuzzy: false,
            shuangpin: Shuangpin::Full,
            mode_scope: ModeScope::System,
            ui_english: false,
            theme: Theme::System,
        }
    }
}

impl Settings {
    /// 校验写入/JSON 前的取值（大小写敏感，`window` 不允许出现）。
    pub fn validate(&self) -> Result<(), String> {
        Ok(())
    }

    /// 契约规定的单行紧凑 JSON（字段顺序固定，无换行）。
    pub fn to_json(&self) -> Result<String, String> {
        self.validate()?;
        Ok(format!(
            "{{\"associate\":{},\"fuzzy\":{},\"shuangpin\":{},\"mode_scope\":{},\"ui_english\":{},\"theme\":{}}}",
            self.associate,
            self.fuzzy,
            json_string(self.shuangpin.as_str()),
            json_string(self.mode_scope.as_str()),
            self.ui_english,
            json_string(self.theme.as_str()),
        ))
    }

    /// 宽松解析 JSON 对象（设置中心/测试用）：缺字段取默认值，未知字段忽略。
    pub fn from_json_lenient(raw: &str) -> Result<Self, String> {
        let v: serde_json::Value = serde_json::from_str(raw).map_err(|e| format!("解析设置 JSON 失败: {e}"))?;
        let obj = v.as_object().ok_or_else(|| "设置 JSON 不是对象".to_string())?;
        let mut s = Settings::default();
        if let Some(b) = obj.get("associate").and_then(|x| x.as_bool()) { s.associate = b; }
        if let Some(b) = obj.get("fuzzy").and_then(|x| x.as_bool()) { s.fuzzy = b; }
        if let Some(x) = obj.get("shuangpin").and_then(|x| x.as_str()) { s.shuangpin = crate::parse::normalize_shuangpin(x); }
        if let Some(x) = obj.get("mode_scope").and_then(|x| x.as_str()) { s.mode_scope = crate::parse::normalize_mode_scope(x); }
        if let Some(b) = obj.get("ui_english").and_then(|x| x.as_bool()) { s.ui_english = b; }
        if let Some(x) = obj.get("theme").and_then(|x| x.as_str()) { s.theme = crate::parse::normalize_theme(x); }
        Ok(s)
    }
}

/// JSON 字符串转义（设置项取值只有简单 ASCII，仍走标准转义避免契约漂移）。
fn json_string(s: &str) -> String {
    serde_json::to_string(s).unwrap_or_else(|_| "\"\"".to_string())
}

impl fmt::Display for Shuangpin {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result { f.write_str(self.as_str()) }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::constants::{ALL_VALUES_JSON, DEFAULTS_JSON};

    #[test]
    fn defaults_contract() {
        assert_eq!(Settings::default().to_json().unwrap(), DEFAULTS_JSON);
    }

    #[test]
    fn all_values_contract() {
        let s = Settings { associate: false, fuzzy: true, shuangpin: Shuangpin::Zrm, mode_scope: ModeScope::Process, ui_english: true, theme: Theme::Dark };
        assert_eq!(s.to_json().unwrap(), ALL_VALUES_JSON);
    }

    #[test]
    fn lenient_parse_defaults_on_missing_fields() {
        let s = Settings::from_json_lenient("{\"theme\":\"dark\"}").unwrap();
        assert_eq!(s.theme, Theme::Dark);
        assert_eq!(s, Settings { theme: Theme::Dark, ..Settings::default() });
    }
}
