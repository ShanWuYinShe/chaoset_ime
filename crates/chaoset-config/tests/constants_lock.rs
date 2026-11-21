//! 跨进程契约常量锁定：键名/域/子键/CLI 名/黄金 JSON 一个字符都不能漂移。

use chaoset_config::constants::*;
use chaoset_config::{Settings, Shuangpin, ModeScope, Theme};

#[test]
fn windows_subkey_is_two_levels() {
    assert_eq!(WINDOWS_REG_SUBKEY, r"Software\Chaoset");
    assert_eq!(WINDOWS_REG_SUBKEY.split('\\').count(), 2);
}

#[test]
fn mac_domain_and_keys() {
    assert_eq!(MAC_DEFAULTS_DOMAIN, "ime.chaoset.inputmethod.ChaosetIME");
    assert_eq!(MAC_KEY_ASSOCIATE, "AssociateEnabled");
    assert_eq!(MAC_KEY_FUZZY, "ChaosetFuzzy");
    assert_eq!(MAC_KEY_SHUANGPIN, "ChaosetShuangpin");
    assert_eq!(MAC_KEY_MODE_SCOPE, "ChaosetModeScope");
    assert_eq!(MAC_KEY_THEME, "ChaosetTheme");
}

#[test]
fn linux_json_keys() {
    let mut keys = vec![LINUX_KEY_ASSOCIATE, LINUX_KEY_FUZZY, LINUX_KEY_SHUANGPIN, LINUX_KEY_MODE_SCOPE, LINUX_KEY_THEME];
    keys.sort();
    assert_eq!(keys, vec!["associate", "fuzzy", "mode_scope", "shuangpin", "theme"]);
}

#[test]
fn cli_mode_scope_is_hyphen() {
    assert_eq!(CLI_ITEM_MODE_SCOPE, "mode-scope");
    assert!(!CLI_ITEM_MODE_SCOPE.contains('_'));
}

#[test]
fn golden_json_strings() {
    assert_eq!(Settings::default().to_json().unwrap(), DEFAULTS_JSON);
    let all = Settings {
        associate: false,
        fuzzy: true,
        shuangpin: Shuangpin::Zrm,
        mode_scope: ModeScope::Process,
        ui_english: true,
        theme: Theme::Dark,
    };
    assert_eq!(all.to_json().unwrap(), ALL_VALUES_JSON);
}
