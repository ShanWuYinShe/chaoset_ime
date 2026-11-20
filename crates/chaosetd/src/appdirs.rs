use std::path::PathBuf;

pub fn data_dir() -> std::io::Result<PathBuf> {
    #[cfg(target_os = "macos")]
    {
        let home = std::env::var("HOME").map_err(|e| std::io::Error::new(std::io::ErrorKind::NotFound, e))?;
        Ok(PathBuf::from(home).join("Library").join("Application Support").join("chaoset"))
    }
    #[cfg(target_os = "windows")]
    {
        let base = std::env::var("LOCALAPPDATA").or_else(|_| {
            let userprofile = std::env::var("USERPROFILE")
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::NotFound, e))?;
            Ok(format!("{}\\AppData\\Local", userprofile))
        })?;
        Ok(PathBuf::from(base).join("Chaoset"))
    }
    #[cfg(not(any(target_os = "macos", target_os = "windows")))]
    {
        if let Ok(base) = std::env::var("XDG_DATA_HOME") {
            if !base.is_empty() {
                return Ok(PathBuf::from(base).join("chaoset"));
            }
        }
        let home = std::env::var("HOME").map_err(|e| std::io::Error::new(std::io::ErrorKind::NotFound, e))?;
        Ok(PathBuf::from(home).join(".local").join("share").join("chaoset"))
    }
}

pub fn default_socket_path() -> std::io::Result<PathBuf> {
    #[cfg(target_os = "windows")]
    {
        Err(std::io::Error::new(
            std::io::ErrorKind::Unsupported,
            "Windows 使用命名管道（-pipe），没有 Unix socket 路径",
        ))
    }
    #[cfg(not(target_os = "windows"))]
    {
        #[cfg(target_os = "linux")]
        {
            if let Ok(runtime_dir) = std::env::var("XDG_RUNTIME_DIR") {
                if !runtime_dir.is_empty() {
                    return Ok(PathBuf::from(runtime_dir).join("chaoset").join("chaoset.sock"));
                }
            }
        }
        let dir = data_dir()?;
        Ok(dir.join("chaoset.sock"))
    }
}

pub fn default_user_dict_path() -> std::io::Result<PathBuf> {
    let dir = data_dir()?;
    Ok(dir.join("userdict.json"))
}

#[cfg(target_os = "windows")]
pub fn default_pipe_name() -> std::io::Result<String> {
    // 获取当前进程用户 SID
    use std::ptr::null_mut;
    use windows_sys::Win32::Foundation::{CloseHandle, GetLastError};
    use windows_sys::Win32::Security::{
        GetTokenInformation, TokenUser, TOKEN_QUERY, TOKEN_USER,
    };
    use windows_sys::Win32::Security::Authorization::ConvertSidToStringSidW;
    use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};

    unsafe {
        let mut token = 0;
        if OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) == 0 {
            return Err(std::io::Error::from_raw_os_error(GetLastError() as i32));
        }

        let mut len = 0;
        GetTokenInformation(token, TokenUser, null_mut(), 0, &mut len);
        let mut buf = vec![0u8; len as usize];
        if GetTokenInformation(token, TokenUser, buf.as_mut_ptr() as _, len, &mut len) == 0 {
            CloseHandle(token);
            return Err(std::io::Error::from_raw_os_error(GetLastError() as i32));
        }
        CloseHandle(token);

        let token_user = buf.as_ptr() as *const TOKEN_USER;
        let sid = (*token_user).User.Sid;
        let mut str_ptr = null_mut();
        if ConvertSidToStringSidW(sid, &mut str_ptr) == 0 {
            return Err(std::io::Error::from_raw_os_error(GetLastError() as i32));
        }

        // 读取 UTF-16 字符串
        let mut len = 0;
        while *str_ptr.add(len) != 0 {
            len += 1;
        }
        let slice = std::slice::from_raw_parts(str_ptr, len);
        let sid_str = String::from_utf16_lossy(slice);
        windows_sys::Win32::Foundation::LocalFree(str_ptr as _);

        Ok(format!(r"\\.\pipe\chaoset-{}", sid_str))
    }
}
