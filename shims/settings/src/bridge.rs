use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::process::Command;
use serde::Deserialize;

#[derive(Debug, Clone, Deserialize)]
#[allow(dead_code)]
pub struct ConfigSettings {
    #[serde(default = "default_associate")]
    pub associate: bool,
    #[serde(default = "default_mode_scope")]
    pub mode_scope: String,
    #[serde(default = "default_theme")]
    pub theme: String,
    #[serde(default)]
    pub ui_english: bool,
}

fn default_associate() -> bool {
    true
}

fn default_mode_scope() -> String {
    "system".to_string()
}

fn default_theme() -> String {
    "system".to_string()
}

impl Default for ConfigSettings {
    fn default() -> Self {
        Self {
            associate: true,
            mode_scope: "system".to_string(),
            theme: "system".to_string(),
            ui_english: false,
        }
    }
}

pub fn find_chaoset_bin() -> PathBuf {
    if let Ok(env_path) = std::env::var("CHAOS_BIN") {
        let p = PathBuf::from(env_path);
        if p.exists() {
            return p;
        }
    }

    let bin_names = if cfg!(windows) {
        ["chaosetctl.exe", "chaoset.exe"]
    } else {
        ["chaosetctl", "chaoset"]
    };

    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            for bin in bin_names {
                let candidate = dir.join(bin);
                if candidate.exists() {
                    return candidate;
                }
                let candidate_bin = dir.join("bin").join(bin);
                if candidate_bin.exists() {
                    return candidate_bin;
                }
            }
        }
    }

    if let Ok(cwd) = std::env::current_dir() {
        for bin in bin_names {
            let candidate = cwd.join("bin").join(bin);
            if candidate.exists() {
                return candidate;
            }
            let candidate_root = cwd.join(bin);
            if candidate_root.exists() {
                return candidate_root;
            }
        }
    }

    PathBuf::from(bin_names[0])
}

fn run_cmd(args: &[&str]) -> Result<String, String> {
    let bin = find_chaoset_bin();
    let mut cmd = Command::new(&bin);
    cmd.args(args);
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }
    let output = cmd
        .output()
        .map_err(|e| format!("执行 {:?} 失败: {}", bin, e))?;

    if output.status.success() {
        Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
    } else {
        let err = String::from_utf8_lossy(&output.stderr).trim().to_string();
        if err.is_empty() {
            let out = String::from_utf8_lossy(&output.stdout).trim().to_string();
            if out.is_empty() {
                Err(format!("命令退出码: {:?}", output.status.code()))
            } else {
                Err(out)
            }
        } else {
            Err(err)
        }
    }
}

pub fn get_config() -> Result<ConfigSettings, String> {
    let raw = run_cmd(&["config", "get"])?;
    serde_json::from_str(&raw).map_err(|e| format!("解析设置 JSON 失败: {} (原始输出: {})", e, raw))
}

pub fn set_associate(enabled: bool) -> Result<(), String> {
    let val = if enabled { "on" } else { "off" };
    run_cmd(&["config", "set", "associate", val]).map(|_| ())
}

pub fn set_mode_scope(scope: &str) -> Result<(), String> {
    run_cmd(&["config", "set", "mode_scope", scope]).map(|_| ())
}

pub fn set_theme(theme: &str) -> Result<(), String> {
    run_cmd(&["config", "set", "theme", theme]).map(|_| ())
}

pub fn get_paths() -> Result<HashMap<String, String>, String> {
    let raw = run_cmd(&["userdict", "-print-paths"])?;
    serde_json::from_str(&raw).map_err(|e| format!("解析数据路径失败: {}", e))
}

pub fn get_userdict_stats() -> Result<String, String> {
    match run_cmd(&["userdict", "stats"]) {
        Ok(s) => Ok(s),
        Err(_) => {
            if let Ok(paths) = get_paths() {
                if let Some(p) = paths.get("userdict") {
                    return run_cmd(&["userdict", "-userdict", p, "stats"]);
                }
            }
            Err("无法获取词典统计".to_string())
        }
    }
}

pub fn clear_userdict() -> Result<String, String> {
    match run_cmd(&["userdict", "clear"]) {
        Ok(s) => Ok(s),
        Err(_) => {
            if let Ok(paths) = get_paths() {
                if let Some(p) = paths.get("userdict") {
                    return run_cmd(&["userdict", "-userdict", p, "clear"]);
                }
            }
            Err("清空学习数据失败".to_string())
        }
    }
}

pub fn export_userdict(out_path: &Path) -> Result<String, String> {
    let path_str = out_path.to_string_lossy().to_string();
    match run_cmd(&["userdict", "export", "-phrases", "-o", &path_str]) {
        Ok(s) => Ok(s),
        Err(_) => {
            if let Ok(paths) = get_paths() {
                if let Some(p) = paths.get("userdict") {
                    return run_cmd(&["userdict", "-userdict", p, "export", "-phrases", "-o", &path_str]);
                }
            }
            Err("导出用户词典失败".to_string())
        }
    }
}

pub fn import_dict(files: &[PathBuf]) -> Result<String, String> {
    if files.is_empty() {
        return Err("未选择导入文件".to_string());
    }
    let paths = get_paths()?;
    let target = paths.get("imported_dict").ok_or("找不到导入词库目标路径")?;

    let mut args = vec!["import", "-o", target, "-backup"];
    let file_strs: Vec<String> = files.iter().map(|f| f.to_string_lossy().to_string()).collect();
    for f in &file_strs {
        args.push(f.as_str());
    }

    run_cmd(&args)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_default_config() {
        let cfg = ConfigSettings::default();
        assert!(cfg.associate);
        assert_eq!(cfg.mode_scope, "system");
        assert_eq!(cfg.theme, "system");
        assert!(!cfg.ui_english);
    }

    #[test]
    fn test_config_json_deserialization() {
        let json = r#"{"associate":false,"mode_scope":"process","theme":"dark","ui_english":true}"#;
        let cfg: ConfigSettings = serde_json::from_str(json).unwrap();
        assert!(!cfg.associate);
        assert_eq!(cfg.mode_scope, "process");
        assert_eq!(cfg.theme, "dark");
        assert!(cfg.ui_english);
    }

    #[test]
    fn test_config_json_with_missing_fields() {
        let json = r#"{}"#;
        let cfg: ConfigSettings = serde_json::from_str(json).unwrap();
        assert!(cfg.associate);
        assert_eq!(cfg.mode_scope, "system");
        assert_eq!(cfg.theme, "system");
        assert!(!cfg.ui_english);
    }

    #[test]
    fn test_userdict_paths_json() {
        let json = r#"{"userdict":"/path/to/dict.db","imported_dict":"/path/to/import.tsv"}"#;
        let paths: HashMap<String, String> = serde_json::from_str(json).unwrap();
        assert_eq!(paths.get("userdict").unwrap(), "/path/to/dict.db");
        assert_eq!(paths.get("imported_dict").unwrap(), "/path/to/import.tsv");
    }

    #[test]
    fn test_import_dict_empty_files() {
        let res = import_dict(&[]);
        assert!(res.is_err());
        assert_eq!(res.unwrap_err(), "未选择导入文件");
    }
}
