//! 输入行为设置（模糊音 / 双拼方案）的读取。
//!
//! 为什么 daemon 自己读：macOS 的 LaunchAgent 与 Windows 安装器 Run 项用
//! **固定参数**拉起 daemon，设置窗改完后无法改这些静态参数；让 daemon 启动时
//! 读同一份平台设置（与 Go `internal/settings` 同键），重启 daemon 即生效，
//! 不必让每个壳在 spawn 时拼参数、也不必维护两套配置来源。
//!
//! 优先级：显式命令行参数 > 平台设置 > 缺省（模糊音关、全拼）。
//! 显式参数优先是为了排障/一次性覆盖（`chaosetd -fuzzy` 仍可强开）。

/// 输入行为设置。字段与 `internal/settings` 的 `fuzzy` / `shuangpin` 同语义。
#[derive(Default, Debug, PartialEq, Eq)]
pub struct InputSettings {
    pub fuzzy: bool,
    /// "" = 全拼；zrm / mspy / flypy = 三种双拼方案。
    pub shuangpin: String,
}

/// 归一化双拼方案名：大小写/首尾空白容忍，未知值回退全拼。
/// 与 Go `settings.parseStoredShuangpin` 同口径：存储嗅探到脏值不应让输入法起不来。
pub fn normalize_shuangpin(raw: &str) -> String {
    match raw.trim().to_ascii_lowercase().as_str() {
        "zrm" => "zrm".to_string(),
        "mspy" => "mspy".to_string(),
        "flypy" => "flypy".to_string(),
        _ => String::new(),
    }
}

/// 解析布尔设置值（注册表 DWORD / defaults 输出 / JSON bool 的统一口径）：
/// 1/true/yes 为真，0/false/no 为假，其它视为未设置（None）。
pub fn parse_bool(raw: &str) -> Option<bool> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "1" | "true" | "yes" => Some(true),
        "0" | "false" | "no" => Some(false),
        _ => None,
    }
}

/// 从 Linux `config.json` 文本解析设置：只关心 fuzzy/shuangpin 两个键，
/// 其它键与未知结构一律忽略（文件由 Go 设置核心/设置窗维护）。
pub fn parse_linux_config(text: &str) -> InputSettings {
    let mut out = InputSettings::default();
    let value: serde_json::Value = match serde_json::from_str(text) {
        Ok(v) => v,
        Err(_) => return out,
    };
    if let Some(raw) = value.get("fuzzy") {
        // 只接受 JSON bool；类型坏了按未设置处理（宽容，不让 daemon 起不来）。
        if let Some(on) = raw.as_bool() {
            out.fuzzy = on;
        }
    }
    if let Some(raw) = value.get("shuangpin").and_then(|v| v.as_str()) {
        out.shuangpin = normalize_shuangpin(raw);
    }
    out
}

/// 读取当前平台的输入行为设置。读取失败/缺省一律返回 `InputSettings::default()`，
/// 绝不因为设置读取失败让 daemon 起不来（用户侧表现是打不出字）。
pub fn load() -> InputSettings {
    #[cfg(target_os = "macos")]
    {
        return load_macos();
    }
    #[cfg(target_os = "windows")]
    {
        return load_windows();
    }
    #[cfg(all(not(target_os = "macos"), not(target_os = "windows")))]
    {
        return load_linux();
    }
}

// ── Linux：$XDG_CONFIG_HOME/chaoset/config.json ──────────────────────

#[cfg(not(any(target_os = "macos", target_os = "windows")))]
fn load_linux() -> InputSettings {
    let path = match linux_config_path() {
        Some(p) => p,
        None => return InputSettings::default(),
    };
    match std::fs::read_to_string(&path) {
        Ok(text) => parse_linux_config(&text),
        Err(_) => InputSettings::default(),
    }
}

#[cfg(not(any(target_os = "macos", target_os = "windows")))]
fn linux_config_path() -> Option<std::path::PathBuf> {
    if let Ok(base) = std::env::var("XDG_CONFIG_HOME") {
        if !base.is_empty() {
            return Some(
                std::path::Path::new(&base)
                    .join("chaoset")
                    .join("config.json"),
            );
        }
    }
    let home = std::env::var("HOME").ok()?;
    Some(
        std::path::Path::new(&home)
            .join(".config")
            .join("chaoset")
            .join("config.json"),
    )
}

// ── macOS：UserDefaults 域 ime.chaoset.inputmethod.ChaosetIME ─────────

#[cfg(target_os = "macos")]
fn load_macos() -> InputSettings {
    const DOMAIN: &str = "ime.chaoset.inputmethod.ChaosetIME";
    let fuzzy = defaults_read(DOMAIN, "ChaosetFuzzy")
        .and_then(|raw| parse_bool(&raw))
        .unwrap_or(false);
    let shuangpin = defaults_read(DOMAIN, "ChaosetShuangpin")
        .map(|raw| normalize_shuangpin(&raw))
        .unwrap_or_default();
    InputSettings { fuzzy, shuangpin }
}

#[cfg(target_os = "macos")]
fn defaults_read(domain: &str, key: &str) -> Option<String> {
    // 标准库没有 plist API：/usr/bin/defaults 与 SettingsWindow.swift 的
    // UserDefaults.standard 读同一域（设置核心 store_darwin.go 同口径）。
    let out = std::process::Command::new("/usr/bin/defaults")
        .args(["read", domain, key])
        .output()
        .ok()?;
    if !out.status.success() {
        return None; // 键/域不存在（defaults 退出码 1）= 未设置
    }
    let text = String::from_utf8_lossy(&out.stdout).trim().to_string();
    // 字符串值可能带引号（随系统版本），与 Go parseDefaultsString 同处理。
    let text = text.trim_matches('"').to_string();
    Some(text)
}

// ── Windows：HKCU\\Software\\Chaoset ─────────────────────────────────

#[cfg(target_os = "windows")]
fn load_windows() -> InputSettings {
    let subkey = wide("Software\\Chaoset");
    let mut fuzzy = false;
    let mut shuangpin = String::new();
    unsafe {
        let mut hkey: windows_sys::Win32::System::Registry::HKEY = 0;
        let status = windows_sys::Win32::System::Registry::RegOpenKeyExW(
            windows_sys::Win32::System::Registry::HKEY_CURRENT_USER,
            subkey.as_ptr(),
            0,
            windows_sys::Win32::System::Registry::KEY_READ,
            &mut hkey,
        );
        if status != 0 {
            return InputSettings::default();
        }
        if let Some(v) = reg_read_dword(hkey, "Fuzzy") {
            fuzzy = v != 0;
        }
        if let Some(v) = reg_read_string(hkey, "Shuangpin") {
            shuangpin = normalize_shuangpin(&v);
        }
        windows_sys::Win32::System::Registry::RegCloseKey(hkey);
    }
    InputSettings { fuzzy, shuangpin }
}

#[cfg(target_os = "windows")]
fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(std::iter::once(0)).collect()
}

#[cfg(target_os = "windows")]
unsafe fn reg_read_dword(
    hkey: windows_sys::Win32::System::Registry::HKEY,
    name: &str,
) -> Option<u32> {
    use windows_sys::Win32::System::Registry::{RegQueryValueExW, REG_DWORD};
    let name = wide(name);
    let mut kind = 0u32;
    let mut value = 0u32;
    let mut size = std::mem::size_of::<u32>() as u32;
    let status = RegQueryValueExW(
        hkey,
        name.as_ptr(),
        std::ptr::null_mut(),
        &mut kind,
        &mut value as *mut u32 as *mut u8,
        &mut size,
    );
    if status == 0 && kind == REG_DWORD {
        Some(value)
    } else {
        None
    }
}

#[cfg(target_os = "windows")]
unsafe fn reg_read_string(
    hkey: windows_sys::Win32::System::Registry::HKEY,
    name: &str,
) -> Option<String> {
    use windows_sys::Win32::System::Registry::{RegQueryValueExW, REG_SZ};
    let name = wide(name);
    let mut kind = 0u32;
    let mut size = 0u32;
    let probe = RegQueryValueExW(
        hkey,
        name.as_ptr(),
        std::ptr::null_mut(),
        &mut kind,
        std::ptr::null_mut(),
        &mut size,
    );
    if probe != 0 || kind != REG_SZ || size == 0 {
        return None;
    }
    let mut buf = vec![0u16; (size as usize + 1) / 2];
    let mut size2 = size;
    let status = RegQueryValueExW(
        hkey,
        name.as_ptr(),
        std::ptr::null_mut(),
        &mut kind,
        buf.as_mut_ptr() as *mut u8,
        &mut size2,
    );
    if status != 0 {
        return None;
    }
    let len = buf.iter().position(|&c| c == 0).unwrap_or(buf.len());
    Some(String::from_utf16_lossy(&buf[..len]))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_shuangpin_tolerates_case_and_space() {
        assert_eq!(normalize_shuangpin(" zrm "), "zrm");
        assert_eq!(normalize_shuangpin("MSPY"), "mspy");
        assert_eq!(normalize_shuangpin("Flypy"), "flypy");
        assert_eq!(normalize_shuangpin(""), "");
        assert_eq!(normalize_shuangpin("bogus"), "");
    }

    #[test]
    fn parse_bool_matches_platform_outputs() {
        assert_eq!(parse_bool("1"), Some(true));
        assert_eq!(parse_bool("true"), Some(true));
        assert_eq!(parse_bool("0"), Some(false));
        assert_eq!(parse_bool("banana"), None);
    }

    #[test]
    fn parse_linux_config_reads_fuzzy_and_shuangpin() {
        let s = parse_linux_config(r#"{"associate": true, "fuzzy": true, "shuangpin": "zrm"}"#);
        assert!(s.fuzzy);
        assert_eq!(s.shuangpin, "zrm");
    }

    #[test]
    fn parse_linux_config_tolerant_of_missing_and_bad_values() {
        let s = parse_linux_config(r#"{"fuzzy": "yes", "shuangpin": 3}"#);
        assert!(!s.fuzzy);
        assert_eq!(s.shuangpin, "");
        assert_eq!(parse_linux_config("not json"), InputSettings::default());
    }
}
