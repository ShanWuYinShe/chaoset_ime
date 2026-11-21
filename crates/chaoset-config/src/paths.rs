//! 平台用户路径（与 daemon 原 `appdirs.rs`、Go `internal/appdirs` 同口径）。

use std::path::PathBuf;

pub const APP_NAME: &str = crate::constants::APP_NAME;

fn home_dir() -> Result<PathBuf, String> {
    match std::env::var("HOME") {
        Ok(v) if !v.is_empty() => Ok(PathBuf::from(v)),
        _ => Err("定位用户主目录失败".to_string()),
    }
}

/// 用户数据目录：macOS Application Support / Linux XDG_DATA_HOME / Windows LOCALAPPDATA。
pub fn data_dir() -> Result<PathBuf, String> {
    #[cfg(target_os = "macos")]
    {
        Ok(home_dir()?.join("Library").join("Application Support").join(APP_NAME))
    }
    #[cfg(target_os = "windows")]
    {
        let base = match std::env::var("LOCALAPPDATA") {
            Ok(v) if !v.is_empty() => v,
            _ => format!("{}\\AppData\\Local", home_dir()?.display()),
        };
        Ok(PathBuf::from(base).join("Chaoset"))
    }
    #[cfg(not(any(target_os = "macos", target_os = "windows")))]
    {
        let base = match std::env::var("XDG_DATA_HOME") {
            Ok(v) if !v.is_empty() => PathBuf::from(v),
            _ => home_dir()?.join(".local").join("share"),
        };
        Ok(base.join(APP_NAME))
    }
}

/// Linux 设置文件：$XDG_CONFIG_HOME/chaoset/config.json（HOME 兜底）。
pub fn linux_config_path() -> Result<PathBuf, String> {
    let base = match std::env::var("XDG_CONFIG_HOME") {
        Ok(v) if !v.is_empty() => PathBuf::from(v),
        _ => home_dir()?.join(".config"),
    };
    Ok(base.join(APP_NAME).join("config.json"))
}

/// 当前平台设置文件路径（Linux 用 XDG; macOS/Windows 仅供排障查询）。
pub fn config_path() -> Result<PathBuf, String> {
    #[cfg(target_os = "linux")]
    {
        linux_config_path()
    }
    #[cfg(not(target_os = "linux"))]
    {
        Ok(data_dir()?.join("config.json"))
    }
}

/// Unix socket 路径：Linux 优先 $XDG_RUNTIME_DIR/chaoset，macOS Application Support。
pub fn socket_path() -> Result<PathBuf, String> {
    #[cfg(target_os = "windows")]
    {
        Err("当前平台使用命名管道，没有 Unix socket 路径".to_string())
    }
    #[cfg(not(target_os = "windows"))]
    {
        #[cfg(target_os = "linux")]
        {
            if let Ok(dir) = std::env::var("XDG_RUNTIME_DIR") {
                if !dir.is_empty() {
                    return Ok(PathBuf::from(dir).join(APP_NAME).join("chaoset.sock"));
                }
            }
        }
        Ok(data_dir()?.join("chaoset.sock"))
    }
}

pub fn user_dict_path() -> Result<PathBuf, String> {
    Ok(data_dir()?.join("userdict.json"))
}

pub fn imported_dict_path() -> Result<PathBuf, String> {
    Ok(data_dir()?.join("dict.tsv"))
}

#[cfg(target_os = "windows")]
pub fn default_pipe_name() -> Result<String, String> {
    use std::ptr::null_mut;
    use windows_sys::Win32::Foundation::{CloseHandle, GetLastError, LocalFree};
    use windows_sys::Win32::Security::Authorization::ConvertSidToStringSidW;
    use windows_sys::Win32::Security::{GetTokenInformation, TokenUser, TOKEN_QUERY, TOKEN_USER};
    use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};

    unsafe {
        let mut token = 0;
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) == 0 {
            return Err(format!("OpenProcessToken 失败: {}", GetLastError()));
        }
        let mut len = 0;
        GetTokenInformation(token, TokenUser, null_mut(), 0, &mut len);
        let mut buf = vec![0u8; len as usize];
        if GetTokenInformation(token, TokenUser, buf.as_mut_ptr() as _, len, &mut len) == 0 {
            CloseHandle(token);
            return Err(format!("GetTokenInformation 失败: {}", GetLastError()));
        }
        CloseHandle(token);
        let token_user = buf.as_ptr() as *const TOKEN_USER;
        let sid = (*token_user).User.Sid;
        let mut str_ptr = null_mut();
        if ConvertSidToStringSidW(sid, &mut str_ptr) == 0 {
            return Err(format!("ConvertSidToStringSidW 失败: {}", GetLastError()));
        }
        let mut n = 0;
        while *str_ptr.add(n) != 0 { n += 1; }
        let sid_str = String::from_utf16_lossy(std::slice::from_raw_parts(str_ptr, n));
        LocalFree(str_ptr as _);
        Ok(format!(r"\\.\pipe\chaoset-{sid_str}"))
    }
}
