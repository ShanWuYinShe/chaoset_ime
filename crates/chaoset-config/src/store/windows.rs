//! Windows 存储：`HKCU\Software\Chaoset` 注册表。
//!
//! 键名/类型/缺省值与 C 壳 config.cpp 逐字兼容；只写目标值，绝不删键/碰其它值。
//! 读句柄必须 `KEY_READ`、写句柄必须 `KEY_WRITE`（写句柄查询会 Access denied，历史踩坑）。

use crate::constants::{
    WINDOWS_REG_SUBKEY, WIN_VALUE_ASSOCIATE, WIN_VALUE_FUZZY, WIN_VALUE_MODE_SCOPE,
    WIN_VALUE_SHUANGPIN, WIN_VALUE_THEME, WIN_VALUE_UI_ENGLISH,
};
use crate::model::{ModeScope, Settings, Shuangpin, Theme};
use crate::parse;
use crate::store::SettingsStore;

use windows_sys::Win32::Foundation::{ERROR_FILE_NOT_FOUND, ERROR_PATH_NOT_FOUND};
use windows_sys::Win32::System::Registry::{
    RegCloseKey, RegCreateKeyExW, RegOpenKeyExW, RegQueryValueExW, RegSetValueExW,
    HKEY, HKEY_CURRENT_USER, KEY_READ, KEY_WRITE, REG_DWORD, REG_OPTION_NON_VOLATILE, REG_SZ,
};

pub struct WindowsStore {
    pub subkey: String,
}

impl Default for WindowsStore {
    fn default() -> Self {
        Self { subkey: WINDOWS_REG_SUBKEY.to_string() }
    }
}

impl WindowsStore {
    pub fn with_subkey(subkey: &str) -> Self {
        Self { subkey: subkey.to_string() }
    }

    fn open_read(&self) -> Result<HKEY, String> {
        let subkey = wide(&self.subkey);
        let mut hkey: HKEY = 0;
        let status = unsafe { RegOpenKeyExW(HKEY_CURRENT_USER, subkey.as_ptr(), 0, KEY_READ, &mut hkey) };
        if status == ERROR_FILE_NOT_FOUND || status == ERROR_PATH_NOT_FOUND {
            return Err("__missing__".to_string());
        }
        if status != 0 {
            return Err(format!("打开注册表 HKCU\\{} 失败: {status}", self.subkey));
        }
        Ok(hkey)
    }

    fn create_write(&self) -> Result<HKEY, String> {
        let subkey = wide(&self.subkey);
        let mut hkey: HKEY = 0;
        let mut disposition = 0u32;
        let status = unsafe {
            RegCreateKeyExW(
                HKEY_CURRENT_USER,
                subkey.as_ptr(),
                0,
                std::ptr::null(),
                REG_OPTION_NON_VOLATILE,
                KEY_WRITE,
                std::ptr::null(),
                &mut hkey,
                &mut disposition,
            )
        };
        if status != 0 {
            return Err(format!("创建注册表 HKCU\\{} 失败: {status}", self.subkey));
        }
        Ok(hkey)
    }

    fn load_impl(&self) -> Result<Settings, String> {
        let hkey = match self.open_read() {
            Ok(h) => h,
            Err(e) if e == "__missing__" => return Ok(Settings::default()),
            Err(e) => return Err(e),
        };
        let mut s = Settings::default();
        if let Some(v) = unsafe { read_dword(hkey, WIN_VALUE_ASSOCIATE) } { s.associate = v != 0; }
        if let Some(v) = unsafe { read_dword(hkey, WIN_VALUE_FUZZY) } { s.fuzzy = v != 0; }
        if let Some(v) = unsafe { read_string(hkey, WIN_VALUE_SHUANGPIN) } { s.shuangpin = parse::normalize_shuangpin(&v); }
        if let Some(v) = unsafe { read_string(hkey, WIN_VALUE_MODE_SCOPE) } { s.mode_scope = parse::normalize_mode_scope(&v); }
        if let Some(v) = unsafe { read_dword(hkey, WIN_VALUE_UI_ENGLISH) } { s.ui_english = v != 0; }
        if let Some(v) = unsafe { read_string(hkey, WIN_VALUE_THEME) } { s.theme = parse::normalize_theme(&v); }
        unsafe { RegCloseKey(hkey) };
        Ok(s)
    }

    fn write_dword(&self, name: &str, value: u32) -> Result<(), String> {
        let hkey = self.create_write()?;
        let wname = wide(name);
        let status = unsafe {
            RegSetValueExW(
                hkey,
                wname.as_ptr(),
                0,
                REG_DWORD,
                &value as *const u32 as *const u8,
                4,
            )
        };
        unsafe { RegCloseKey(hkey) };
        if status != 0 {
            return Err(format!("写注册表值 {name} 失败: {status}"));
        }
        Ok(())
    }

    fn write_string(&self, name: &str, value: &str) -> Result<(), String> {
        let hkey = self.create_write()?;
        let wname = wide(name);
        let wvalue: Vec<u16> = value.encode_utf16().chain(std::iter::once(0)).collect();
        let bytes = (wvalue.len() * 2) as u32;
        let status = unsafe {
            RegSetValueExW(
                hkey,
                wname.as_ptr(),
                0,
                REG_SZ,
                wvalue.as_ptr() as *const u8,
                bytes,
            )
        };
        unsafe { RegCloseKey(hkey) };
        if status != 0 {
            return Err(format!("写注册表值 {name} 失败: {status}"));
        }
        Ok(())
    }
}

impl SettingsStore for WindowsStore {
    fn load(&self) -> Result<Settings, String> { self.load_impl() }

    fn set_associate(&self, on: bool) -> Result<(), String> {
        self.write_dword(WIN_VALUE_ASSOCIATE, on as u32)
    }

    fn set_fuzzy(&self, on: bool) -> Result<(), String> {
        self.write_dword(WIN_VALUE_FUZZY, on as u32)
    }

    fn set_shuangpin(&self, value: Shuangpin) -> Result<(), String> {
        self.write_string(WIN_VALUE_SHUANGPIN, value.as_str())
    }

    fn set_mode_scope(&self, value: ModeScope) -> Result<(), String> {
        self.write_string(WIN_VALUE_MODE_SCOPE, value.as_str())
    }

    fn set_theme(&self, value: Theme) -> Result<(), String> {
        self.write_string(WIN_VALUE_THEME, value.as_str())
    }

    fn set_ui_english(&self, on: bool) -> Result<(), String> {
        self.write_dword(WIN_VALUE_UI_ENGLISH, on as u32)
    }
}

fn wide(s: &str) -> Vec<u16> {
    s.encode_utf16().chain(std::iter::once(0)).collect()
}

unsafe fn read_dword(hkey: HKEY, name: &str) -> Option<u32> {
    let wname = wide(name);
    let mut kind = 0u32;
    let mut value = 0u32;
    let mut size = std::mem::size_of::<u32>() as u32;
    let status = unsafe {
        RegQueryValueExW(
            hkey,
            wname.as_ptr(),
            std::ptr::null_mut(),
            &mut kind,
            &mut value as *mut u32 as *mut u8,
            &mut size,
        )
    };
    if status == 0 && kind == REG_DWORD && size == 4 { Some(value) } else { None }
}

unsafe fn read_string(hkey: HKEY, name: &str) -> Option<String> {
    let wname = wide(name);
    let mut kind = 0u32;
    let mut buf = [0u16; 129];
    let mut size = (buf.len() * 2) as u32;
    let status = unsafe {
        RegQueryValueExW(
            hkey,
            wname.as_ptr(),
            std::ptr::null_mut(),
            &mut kind,
            buf.as_mut_ptr() as *mut u8,
            &mut size,
        )
    };
    if status != 0 || kind != REG_SZ || size < 2 {
        return None;
    }
    let len = buf.iter().position(|&c| c == 0).unwrap_or(buf.len());
    Some(String::from_utf16_lossy(&buf[..len]))
}
