//! Linux 存储：`$XDG_CONFIG_HOME/chaoset/config.json`。
//!
//! 与 C 设置窗/ibus 引擎逐字节兼容：2 空格缩进 + 末尾换行；**只改目标键、
//! 未知键原样保留**（`traditional` 等由引擎读取；`1.50` 这类字面量不能被改写）；
//! 损坏 JSON/字段类型错误 → 报错且绝不覆盖原文件；写入用同目录临时文件 + rename。

use std::collections::BTreeMap;
use std::io::Write;
use std::path::{Path, PathBuf};

use serde_json::value::RawValue;

use crate::constants::{LINUX_KEY_ASSOCIATE, LINUX_KEY_FUZZY, LINUX_KEY_MODE_SCOPE, LINUX_KEY_SHUANGPIN, LINUX_KEY_THEME};
use crate::model::{ModeScope, Settings, Shuangpin, Theme};
use crate::parse;
use crate::paths;
use crate::store::SettingsStore;

pub struct LinuxStore {
    pub path: PathBuf,
}

impl Default for LinuxStore {
    fn default() -> Self {
        Self { path: paths::linux_config_path().unwrap_or_else(|_| PathBuf::from("config.json")) }
    }
}

impl LinuxStore {
    pub fn new(path: impl Into<PathBuf>) -> Self {
        Self { path: path.into() }
    }

    fn require_bool(&self, m: &serde_json::Map<String, serde_json::Value>, key: &str) -> Result<Option<bool>, String> {
        match m.get(key) {
            None => Ok(None),
            Some(v) => v.as_bool().map(Some).ok_or_else(|| {
                format!("设置文件 {} 的 \"{key}\" 不是 bool", self.path.display())
            }),
        }
    }

    fn require_string(&self, m: &serde_json::Map<String, serde_json::Value>, key: &str) -> Result<Option<String>, String> {
        match m.get(key) {
            None => Ok(None),
            Some(v) => v.as_str().map(|s| Some(s.to_string())).ok_or_else(|| {
                format!("设置文件 {} 的 \"{key}\" 不是字符串", self.path.display())
            }),
        }
    }

    fn update(&self, mutate: impl FnOnce(&mut BTreeMap<String, Box<RawValue>>) -> Result<(), String>) -> Result<(), String> {
        let mut map = read_raw_map(&self.path)?;
        mutate(&mut map)?;
        write_raw_map(&self.path, &map)
    }
}

fn read_raw_map(path: &Path) -> Result<BTreeMap<String, Box<RawValue>>, String> {
    let data = match std::fs::read_to_string(path) {
        Ok(d) => d,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(BTreeMap::new()),
        Err(e) => return Err(format!("读取设置文件 {} 失败: {e}", path.display())),
    };
    if data.trim().is_empty() {
        return Ok(BTreeMap::new());
    }
    serde_json::from_str::<BTreeMap<String, Box<RawValue>>>(&data)
        .map_err(|e| format!("解析设置文件 {} 失败（不是合法 JSON 对象）: {e}", path.display()))
}

fn write_raw_map(path: &Path, map: &BTreeMap<String, Box<RawValue>>) -> Result<(), String> {
    let mut pretty = serde_json::to_string_pretty(map)
        .map_err(|e| format!("生成设置 JSON 失败: {e}"))?;
    pretty.push('\n');
    let dir = path.parent().unwrap_or_else(|| Path::new("."));
    std::fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(dir)
        .map_err(|e| format!("创建设置目录 {} 失败: {e}", dir.display()))?;
    let tmp = dir.join(format!(".config.json.tmp-{}-{}", std::process::id(), now_nanos()));
    let result = (|| -> Result<(), String> {
        let mut f = std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&tmp)
            .map_err(|e| format!("创建临时设置文件失败: {e}"))?;
        f.write_all(pretty.as_bytes()).map_err(|e| format!("写临时设置文件失败: {e}"))?;
        f.flush().map_err(|e| format!("写临时设置文件失败: {e}"))?;
        drop(f);
        std::fs::set_permissions(&tmp, std::os::unix::fs::PermissionsExt::from_mode(0o644))
            .map_err(|e| format!("设置文件权限失败: {e}"))?;
        std::fs::rename(&tmp, path).map_err(|e| format!("替换设置文件 {} 失败: {e}", path.display()))?;
        Ok(())
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
    result
}

fn now_nanos() -> u128 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0)
}

use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};

impl SettingsStore for LinuxStore {
    fn load(&self) -> Result<Settings, String> {
        let mut s = Settings::default();
        let data = match std::fs::read_to_string(&self.path) {
            Ok(d) => d,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(s),
            Err(e) => return Err(format!("读取设置文件 {} 失败: {e}", self.path.display())),
        };
        if data.trim().is_empty() {
            return Ok(s);
        }
        let map: serde_json::Map<String, serde_json::Value> = serde_json::from_str(&data)
            .map_err(|e| format!("解析设置文件 {} 失败（不是合法 JSON 对象）: {e}", self.path.display()))?;
        if let Some(v) = self.require_bool(&map, LINUX_KEY_ASSOCIATE)? { s.associate = v; }
        if let Some(v) = self.require_bool(&map, LINUX_KEY_FUZZY)? { s.fuzzy = v; }
        if let Some(v) = self.require_string(&map, LINUX_KEY_SHUANGPIN)? { s.shuangpin = parse::normalize_shuangpin(&v); }
        if let Some(v) = self.require_string(&map, LINUX_KEY_MODE_SCOPE)? { s.mode_scope = parse::normalize_mode_scope(&v); }
        if let Some(v) = self.require_string(&map, LINUX_KEY_THEME)? { s.theme = parse::normalize_theme(&v); }
        Ok(s)
    }

    fn set_associate(&self, on: bool) -> Result<(), String> {
        self.update(|m| set_raw(m, LINUX_KEY_ASSOCIATE, serde_json::json!(on)))
    }

    fn set_fuzzy(&self, on: bool) -> Result<(), String> {
        self.update(|m| set_raw(m, LINUX_KEY_FUZZY, serde_json::json!(on)))
    }

    fn set_shuangpin(&self, value: Shuangpin) -> Result<(), String> {
        self.update(|m| set_raw(m, LINUX_KEY_SHUANGPIN, serde_json::json!(value.as_str())))
    }

    fn set_mode_scope(&self, value: ModeScope) -> Result<(), String> {
        self.update(|m| set_raw(m, LINUX_KEY_MODE_SCOPE, serde_json::json!(value.as_str())))
    }

    fn set_theme(&self, value: Theme) -> Result<(), String> {
        self.update(|m| set_raw(m, LINUX_KEY_THEME, serde_json::json!(value.as_str())))
    }

    fn set_ui_english(&self, _on: bool) -> Result<(), String> {
        Err("当前平台不支持该设置项: ui_english 仅 Windows 有持久化（Linux 壳不保存中英状态）".to_string())
    }
}

fn set_raw(m: &mut BTreeMap<String, Box<RawValue>>, key: &str, value: serde_json::Value) -> Result<(), String> {
    let raw = serde_json::value::to_raw_value(&value).map_err(|e| format!("生成设置 JSON 失败: {e}"))?;
    m.insert(key.to_string(), raw);
    Ok(())
}
