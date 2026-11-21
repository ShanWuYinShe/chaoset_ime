//! 取值解析与存储归一化。两套语义必须分开：
//!   - CLI 入参解析：非法 → 参数错误（exit 2）；
//!   - 存储读取归一化：脏值回缺省，永不报错（旧三档 window→process 等）。

use crate::model::{ModeScope, Shuangpin, Theme};

fn lower_trim(raw: &str) -> String {
    raw.trim().to_lowercase()
}

/// CLI：on/off（大小写/空白容忍）→ bool。
pub fn parse_on_off(raw: &str) -> Result<bool, String> {
    match lower_trim(raw).as_str() {
        "on" => Ok(true),
        "off" => Ok(false),
        _ => Err(format!("只接受 on|off，收到 {raw:?}")),
    }
}

/// CLI：全拼/双拼方案（full/""/none/quanpin → 全拼；zrm/mspy/flypy）。
pub fn parse_shuangpin(raw: &str) -> Result<Shuangpin, String> {
    match lower_trim(raw).as_str() {
        "" | "full" | "none" | "quanpin" => Ok(Shuangpin::Full),
        "zrm" => Ok(Shuangpin::Zrm),
        "mspy" => Ok(Shuangpin::Mspy),
        "flypy" => Ok(Shuangpin::Flypy),
        _ => Err(format!("shuangpin 只接受 full|zrm|mspy|flypy，收到 {raw:?}")),
    }
}

/// CLI：中英作用域（兼容旧值 window → process）。
pub fn parse_mode_scope(raw: &str) -> Result<ModeScope, String> {
    match lower_trim(raw).as_str() {
        "system" => Ok(ModeScope::System),
        "process" | "window" => Ok(ModeScope::Process),
        _ => Err(format!("mode-scope 只接受 system|process（兼容旧值 window），收到 {raw:?}")),
    }
}

/// CLI：外观主题三档。
pub fn parse_theme(raw: &str) -> Result<Theme, String> {
    match lower_trim(raw).as_str() {
        "system" => Ok(Theme::System),
        "light" => Ok(Theme::Light),
        "dark" => Ok(Theme::Dark),
        _ => Err(format!("theme 只接受 system|light|dark，收到 {raw:?}")),
    }
}

/// 存储读取：双拼归一化（未知/脏值 → 全拼）。
pub fn normalize_shuangpin(raw: &str) -> Shuangpin {
    match lower_trim(raw).as_str() {
        "zrm" => Shuangpin::Zrm,
        "mspy" => Shuangpin::Mspy,
        "flypy" => Shuangpin::Flypy,
        _ => Shuangpin::Full,
    }
}

/// 存储读取：作用域归一化（window→process，未知/空 → system）。
pub fn normalize_mode_scope(raw: &str) -> ModeScope {
    match lower_trim(raw).as_str() {
        "process" | "window" => ModeScope::Process,
        _ => ModeScope::System,
    }
}

/// 存储读取：主题归一化（未知/空 → system）。
pub fn normalize_theme(raw: &str) -> Theme {
    match lower_trim(raw).as_str() {
        "light" => Theme::Light,
        "dark" => Theme::Dark,
        _ => Theme::System,
    }
}

/// macOS `defaults read` 的 bool 输出：1/0/true/false/yes/no，以及任意整数
/// （非 0 = true，与 Swift `as? Bool` 对 NSNumber 的行为一致）。
pub fn parse_defaults_bool(out: &str) -> Result<bool, String> {
    let v = out.trim();
    match v.to_lowercase().as_str() {
        "1" | "true" | "yes" => return Ok(true),
        "0" | "false" | "no" => return Ok(false),
        _ => {}
    }
    if let Ok(n) = v.parse::<i64>() {
        return Ok(n != 0);
    }
    Err(format!("不是布尔值: {out:?}"))
}

/// macOS `defaults read` 的字符串输出：去一层首尾引号（不同系统版本行为）。
pub fn parse_defaults_string(out: &str) -> String {
    let s = out.trim();
    if s.len() >= 2 && s.starts_with('"') && s.ends_with('"') {
        return s[1..s.len() - 1].trim().to_string();
    }
    s.to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cli_parsers() {
        assert_eq!(parse_on_off("ON").unwrap(), true);
        assert!(parse_on_off("yes").is_err());
        assert_eq!(parse_shuangpin("").unwrap(), Shuangpin::Full);
        assert_eq!(parse_shuangpin(" full ").unwrap(), Shuangpin::Full);
        assert_eq!(parse_shuangpin("ZRM").unwrap(), Shuangpin::Zrm);
        assert!(parse_shuangpin("bogus").is_err());
        assert_eq!(parse_mode_scope("WINDOW").unwrap(), ModeScope::Process);
        assert!(parse_mode_scope("auto").is_err());
        assert_eq!(parse_theme(" Light ").unwrap(), Theme::Light);
        assert!(parse_theme("auto").is_err());
    }

    #[test]
    fn stored_normalizers() {
        assert_eq!(normalize_shuangpin(" zrm "), Shuangpin::Zrm);
        assert_eq!(normalize_shuangpin("bogus"), Shuangpin::Full);
        assert_eq!(normalize_mode_scope("window"), ModeScope::Process);
        assert_eq!(normalize_mode_scope("auto"), ModeScope::System);
        assert_eq!(normalize_theme("DARK"), Theme::Dark);
        assert_eq!(normalize_theme(""), Theme::System);
    }

    #[test]
    fn defaults_output_parsers() {
        for v in ["1", "true", "YES", "2", "-3"] { assert_eq!(parse_defaults_bool(v).unwrap(), true); }
        for v in ["0", "false", "no"] { assert_eq!(parse_defaults_bool(v).unwrap(), false); }
        assert!(parse_defaults_bool("banana").is_err());
        assert_eq!(parse_defaults_string("  process  "), "process");
        assert_eq!(parse_defaults_string("\"process\""), "process");
        assert_eq!(parse_defaults_string("\"\""), "");
    }
}
