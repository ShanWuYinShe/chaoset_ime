//! Linux config.json 存储：字节格式/权限/保留未知键/损坏拒绝。
#![cfg(unix)]

use std::io::Write;
use std::os::unix::fs::PermissionsExt;

use chaoset_config::model::{ModeScope, Shuangpin, Theme};
use chaoset_config::store::linux::LinuxStore;
use chaoset_config::store::SettingsStore;
use tempfile::TempDir;

fn store(dir: &TempDir) -> LinuxStore {
    LinuxStore::new(dir.path().join("config.json"))
}

#[test]
fn missing_file_defaults_and_creates_nothing() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("config.json");
    let s = store(&dir).load().unwrap();
    assert_eq!(s, chaoset_config::Settings::default());
    assert!(!path.exists());
}

#[test]
fn canonical_bytes_match_c_format() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("config.json");
    let st = store(&dir);
    std::fs::create_dir_all(dir.path()).unwrap();
    st.set_associate(true).unwrap();
    let bytes = std::fs::read(&path).unwrap();
    let text = String::from_utf8(bytes).unwrap();
    assert_eq!(text, "{\n  \"associate\": true\n}\n");
}

#[test]
fn roundtrip_and_preserve_unknown_keys_raw() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("config.json");
    std::fs::write(
        &path,
        "{\n  \"traditional\": false,\n  \"extra\": {\n    \"n\": 1.50\n  },\n  \"associate\": true\n}\n",
    )
    .unwrap();
    let st = store(&dir);
    st.set_mode_scope(ModeScope::Process).unwrap();
    st.set_theme(Theme::Dark).unwrap();
    st.set_shuangpin(Shuangpin::Zrm).unwrap();
    st.set_fuzzy(true).unwrap();
    let text = std::fs::read_to_string(&path).unwrap();
    assert!(text.contains("\"traditional\": false"), "{text}");
    assert!(text.contains("1.50"), "未知数字字面量被改写: {text}");
    assert!(text.contains("\"mode_scope\": \"process\""), "{text}");
    assert!(text.contains("\"theme\": \"dark\""), "{text}");
    let s = st.load().unwrap();
    assert_eq!(s.mode_scope, ModeScope::Process);
    assert_eq!(s.theme, Theme::Dark);
    assert_eq!(s.shuangpin, Shuangpin::Zrm);
    assert!(s.fuzzy);
    assert!(s.associate, "未写入的键应保持原值");
}

#[test]
fn corrupt_json_is_rejected_and_file_unchanged() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("config.json");
    let raw = "{not json";
    std::fs::write(&path, raw).unwrap();
    let st = store(&dir);
    assert!(st.load().is_err());
    assert!(st.set_theme(Theme::Light).is_err());
    assert_eq!(std::fs::read_to_string(&path).unwrap(), raw);
}

#[test]
fn invalid_field_types_are_rejected() {
    for (body, key) in [
        (r#"{"associate": "yes"}"#, "associate"),
        (r#"{"fuzzy": 1}"#, "fuzzy"),
        (r#"{"mode_scope": 3}"#, "mode_scope"),
        (r#"{"theme": []}"#, "theme"),
    ] {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("config.json");
        std::fs::write(&path, body).unwrap();
        let err = store(&dir).load().unwrap_err();
        assert!(err.contains(key), "错误应包含键名 {key}: {err}");
    }
}

#[test]
fn ui_english_unsupported_does_not_touch_file() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("config.json");
    assert!(store(&dir).set_ui_english(true).is_err());
    assert!(!path.exists());
}

#[test]
fn file_and_dir_permissions() {
    let dir = TempDir::new().unwrap();
    let nested = dir.path().join("a/b");
    let st = LinuxStore::new(nested.join("config.json"));
    st.set_associate(false).unwrap();
    let meta = std::fs::metadata(nested.join("config.json")).unwrap();
    assert_eq!(meta.permissions().mode() & 0o777, 0o644);
    let dir_meta = std::fs::metadata(nested.parent().unwrap()).unwrap();
    assert_eq!(dir_meta.permissions().mode() & 0o777, 0o700);
}

#[test]
fn no_leftover_tmp_files() {
    let dir = TempDir::new().unwrap();
    let st = store(&dir);
    st.set_associate(true).unwrap();
    st.set_fuzzy(true).unwrap();
    let names: Vec<String> = std::fs::read_dir(dir.path())
        .unwrap()
        .filter_map(|e| e.ok())
        .map(|e| e.file_name().to_string_lossy().to_string())
        .collect();
    assert!(!names.iter().any(|n| n.contains(".config.json.tmp-")), "{names:?}");
}

#[test]
fn empty_file_defaults() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("config.json");
    let mut f = std::fs::File::create(&path).unwrap();
    f.write_all(b"   \n\t").unwrap();
    assert_eq!(store(&dir).load().unwrap(), chaoset_config::Settings::default());
}
