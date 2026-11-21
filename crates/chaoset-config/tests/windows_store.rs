//! Windows 注册表设置存储的真实往返测试（只在 Windows 上编译/运行）。
//!
//! CI 的 settings-windows job 用
//! `cargo test --manifest-path crates/Cargo.toml -p chaoset-config --test windows_store`
//! 在一次性 Windows runner 上真跑 HKCU：DWORD/REG_SZ 读写、mode_scope 归一化、
//! 类型损坏与超长字符串回退、scratch 子键清理幂等。
//!
//! 安全边界：**只碰 `Software\\Chaoset-test-<pid>-<tag>`**，绝不读写真实
//! `Software\\Chaoset`（用户正在使用的设置）；每个测试用自己的 tag，互不干扰，
//! 结束时删除 scratch 子键并断言第二次删除幂等。
#![cfg(windows)]

use chaoset_config::constants::{
    WINDOWS_REG_SUBKEY, WIN_VALUE_ASSOCIATE, WIN_VALUE_FUZZY, WIN_VALUE_MODE_SCOPE,
    WIN_VALUE_SHUANGPIN, WIN_VALUE_THEME, WIN_VALUE_UI_ENGLISH,
};
use chaoset_config::model::{ModeScope, Settings, Shuangpin, Theme};
use chaoset_config::store::windows::WindowsStore;
use chaoset_config::store::SettingsStore;

use windows_sys::Win32::Foundation::{ERROR_FILE_NOT_FOUND, ERROR_SUCCESS};
use windows_sys::Win32::System::Registry::{
    RegCloseKey, RegCreateKeyExW, RegDeleteKeyW, RegDeleteTreeW, RegQueryValueExW, RegSetValueExW,
    HKEY, HKEY_CURRENT_USER, KEY_ALL_ACCESS, REG_DWORD, REG_SZ,
};

/// scratch 子键：带 PID + tag，多个测试进程/并发 CI 之间互不干扰。
fn scratch_subkey(tag: &str) -> String {
    format!("Software\\Chaoset-test-{}-{tag}", std::process::id())
}

fn wide(text: &str) -> Vec<u16> {
    text.encode_utf16().chain(std::iter::once(0)).collect()
}

/// 打开（必要时创建）scratch 键执行 f，结束后关句柄。
fn with_hkey<T>(subkey: &str, f: impl FnOnce(HKEY) -> T) -> T {
    let path = wide(subkey);
    let mut hkey: HKEY = 0;
    let mut disposition: u32 = 0;
    let status = unsafe {
        RegCreateKeyExW(
            HKEY_CURRENT_USER,
            path.as_ptr(),
            0,
            std::ptr::null(),
            0,
            KEY_ALL_ACCESS,
            std::ptr::null(),
            &mut hkey,
            &mut disposition,
        )
    };
    assert_eq!(status, ERROR_SUCCESS, "创建 scratch 注册表键失败: {subkey}");
    let out = f(hkey);
    unsafe { RegCloseKey(hkey) };
    out
}

/// 删除 scratch 键：返回 Win32 错误码（0 = 成功，2 = 键不存在）。
///
/// RegDeleteTreeW 只递归删值/子键、**保留键本身**，因此还要 RegDeleteKeyW 把键删掉；
/// 幂等断言依赖第二次删除返回 ERROR_FILE_NOT_FOUND。
fn delete_tree(subkey: &str) -> u32 {
    let path = wide(subkey);
    let tree = unsafe { RegDeleteTreeW(HKEY_CURRENT_USER, path.as_ptr()) };
    if tree != ERROR_SUCCESS && tree != ERROR_FILE_NOT_FOUND {
        return tree;
    }
    unsafe { RegDeleteKeyW(HKEY_CURRENT_USER, path.as_ptr()) }
}

/// 清理 scratch 键并断言幂等：第一次成功（或本就不存在），第二次必定「文件不存在」。
fn cleanup_scratch(subkey: &str) {
    let first = delete_tree(subkey);
    assert!(
        first == ERROR_SUCCESS || first == ERROR_FILE_NOT_FOUND,
        "删除 scratch 注册表键失败: {subkey}（错误码 {first}）"
    );
    let second = delete_tree(subkey);
    assert_eq!(
        second, ERROR_FILE_NOT_FOUND,
        "scratch 注册表键删除必须幂等（第二次应报文件不存在，实际 {second}）"
    );
    // 清理后 store 必须回到缺省值（键已不存在）。
    let store = WindowsStore::with_subkey(subkey);
    assert_eq!(
        store.load().unwrap(),
        Settings::default(),
        "清理后应回到缺省设置"
    );
}

fn write_dword_raw(subkey: &str, name: &str, value: u32) {
    with_hkey(subkey, |hkey| {
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
        assert_eq!(status, ERROR_SUCCESS, "写 REG_DWORD {name} 失败");
    });
}

fn write_sz_raw(subkey: &str, name: &str, value: &str) {
    with_hkey(subkey, |hkey| {
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
        assert_eq!(status, ERROR_SUCCESS, "写 REG_SZ {name} 失败");
    });
}

/// 读原始值（类型 + 字节数 + 值），用于断言存储格式而不是只看解析结果。
fn read_raw(subkey: &str, name: &str) -> Option<(u32, u32, Vec<u8>)> {
    with_hkey(subkey, |hkey| {
        let wname = wide(name);
        let mut kind: u32 = 0;
        let mut data = [0u8; 1024];
        let mut size = data.len() as u32;
        let status = unsafe {
            RegQueryValueExW(
                hkey,
                wname.as_ptr(),
                std::ptr::null(),
                &mut kind,
                data.as_mut_ptr(),
                &mut size,
            )
        };
        if status != ERROR_SUCCESS {
            return None;
        }
        Some((kind, size, data[..size as usize].to_vec()))
    })
}

#[test]
fn dword_and_sz_roundtrip() {
    let subkey = scratch_subkey("roundtrip");
    cleanup_scratch(&subkey);
    // 键不存在时 strict load 必须给缺省值，而不是报错。
    let store = WindowsStore::with_subkey(&subkey);
    assert_eq!(
        store.load().unwrap(),
        Settings::default(),
        "键不存在应返回缺省设置"
    );

    store.set_associate(false).unwrap();
    store.set_fuzzy(true).unwrap();
    store.set_shuangpin(Shuangpin::Zrm).unwrap();
    store.set_mode_scope(ModeScope::Process).unwrap();
    store.set_theme(Theme::Dark).unwrap();
    store.set_ui_english(true).unwrap();

    let expected = Settings {
        associate: false,
        fuzzy: true,
        shuangpin: Shuangpin::Zrm,
        mode_scope: ModeScope::Process,
        ui_english: true,
        theme: Theme::Dark,
    };
    assert_eq!(store.load().unwrap(), expected);

    // 存储格式契约：布尔写 DWORD（0/1），枚举写 REG_SZ（字符串）。
    let (kind, size, bytes) = read_raw(&subkey, WIN_VALUE_ASSOCIATE).expect("Associate 值应存在");
    assert_eq!(kind, REG_DWORD, "Associate 必须是 REG_DWORD");
    assert_eq!(size, 4);
    assert_eq!(u32::from_le_bytes(bytes[..4].try_into().unwrap()), 0);
    let (kind, _size, bytes) = read_raw(&subkey, WIN_VALUE_FUZZY).expect("Fuzzy 值应存在");
    assert_eq!(kind, REG_DWORD, "Fuzzy 必须是 REG_DWORD");
    assert_eq!(u32::from_le_bytes(bytes[..4].try_into().unwrap()), 1);
    for (name, want) in [
        (WIN_VALUE_SHUANGPIN, "zrm"),
        (WIN_VALUE_MODE_SCOPE, "process"),
        (WIN_VALUE_THEME, "dark"),
    ] {
        let (kind, _size, bytes) = read_raw(&subkey, name).expect("REG_SZ 值应存在");
        assert_eq!(kind, REG_SZ, "{name} 必须是 REG_SZ");
        let text = String::from_utf16_lossy(
            &bytes
                .chunks_exact(2)
                .map(|pair| u16::from_le_bytes([pair[0], pair[1]]))
                .collect::<Vec<u16>>(),
        );
        assert_eq!(text.trim_end_matches('\u{0}'), want, "{name} 存储值不符");
    }
    let (kind, _size, bytes) = read_raw(&subkey, WIN_VALUE_UI_ENGLISH).expect("UiEnglish 应存在");
    assert_eq!(kind, REG_DWORD);
    assert_eq!(u32::from_le_bytes(bytes[..4].try_into().unwrap()), 1);

    cleanup_scratch(&subkey);
}

#[test]
fn mode_scope_normalization_eight_cases() {
    let subkey = scratch_subkey("modescope");
    cleanup_scratch(&subkey);
    let store = WindowsStore::with_subkey(&subkey);
    // 存储读取归一化：旧三档 window→process、未知/空→system（大小写与空白容忍）。
    let cases: [(&str, ModeScope); 8] = [
        ("system", ModeScope::System),
        ("process", ModeScope::Process),
        ("window", ModeScope::Process),
        ("", ModeScope::System),
        (" SYSTEM ", ModeScope::System),
        ("PROCESS", ModeScope::Process),
        ("WINDOW", ModeScope::Process),
        ("bogus", ModeScope::System),
    ];
    for (raw, want) in cases {
        write_sz_raw(&subkey, WIN_VALUE_MODE_SCOPE, raw);
        assert_eq!(
            store.load().unwrap().mode_scope,
            want,
            "mode_scope 归一化失败：原始值 {raw:?}"
        );
    }
    cleanup_scratch(&subkey);
}

#[test]
fn type_corruption_falls_back() {
    let subkey = scratch_subkey("corrupt");
    cleanup_scratch(&subkey);
    let store = WindowsStore::with_subkey(&subkey);
    // DWORD 写到字符串键上：读取方发现类型不符，回退缺省而不是报错。
    write_dword_raw(&subkey, WIN_VALUE_MODE_SCOPE, 1);
    write_dword_raw(&subkey, WIN_VALUE_THEME, 1);
    write_dword_raw(&subkey, WIN_VALUE_SHUANGPIN, 1);
    // REG_SZ 写到布尔键上：同样回退缺省。
    write_sz_raw(&subkey, WIN_VALUE_ASSOCIATE, "off");
    write_sz_raw(&subkey, WIN_VALUE_FUZZY, "on");
    write_sz_raw(&subkey, WIN_VALUE_UI_ENGLISH, "on");

    let loaded = store
        .load()
        .expect("类型损坏不应让 strict load 失败（回退缺省）");
    assert_eq!(
        loaded.associate,
        Settings::default().associate,
        "关联词回退缺省"
    );
    assert_eq!(loaded.fuzzy, Settings::default().fuzzy, "模糊音回退缺省");
    assert_eq!(loaded.mode_scope, ModeScope::System, "作用域回退 system");
    assert_eq!(loaded.theme, Theme::System, "主题回退 system");
    assert_eq!(loaded.shuangpin, Shuangpin::Full, "双拼回退全拼");
    assert_eq!(
        loaded.ui_english,
        Settings::default().ui_english,
        "ui_english 回退缺省"
    );
    cleanup_scratch(&subkey);
}

#[test]
fn overlong_string_falls_back() {
    let subkey = scratch_subkey("overlong");
    cleanup_scratch(&subkey);
    let store = WindowsStore::with_subkey(&subkey);
    // 存储读取缓冲固定 129 个 UTF-16 单元：超长值读取会 ERROR_MORE_DATA，
    // 必须回退缺省（不能 panic，也不能截断成脏值）。
    let long = "x".repeat(400);
    write_sz_raw(&subkey, WIN_VALUE_MODE_SCOPE, &long);
    write_sz_raw(&subkey, WIN_VALUE_THEME, &long);
    write_sz_raw(&subkey, WIN_VALUE_SHUANGPIN, &long);
    let loaded = store.load().expect("超长字符串不应让 strict load 失败");
    assert_eq!(loaded.mode_scope, ModeScope::System);
    assert_eq!(loaded.theme, Theme::System);
    assert_eq!(loaded.shuangpin, Shuangpin::Full);
    cleanup_scratch(&subkey);
}

#[test]
fn scratch_subkey_never_touches_real_settings() {
    let real_before = WindowsStore::default().load().unwrap();
    let subkey = scratch_subkey("isolation");
    assert_ne!(
        subkey, WINDOWS_REG_SUBKEY,
        "scratch 子键绝不能等于真实设置子键"
    );
    assert!(
        subkey.starts_with("Software\\Chaoset-test-"),
        "scratch 子键命名必须带 test 前缀"
    );
    cleanup_scratch(&subkey);
    let store = WindowsStore::with_subkey(&subkey);
    store.set_associate(false).unwrap();
    store.set_theme(Theme::Dark).unwrap();
    cleanup_scratch(&subkey);
    // 真实 Software\Chaoset 在测试前后必须完全不变。
    let real_after = WindowsStore::default().load().unwrap();
    assert_eq!(
        real_before, real_after,
        "测试不得改动真实 Software\\Chaoset 设置"
    );
}
