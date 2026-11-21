//! Windows：由 shims/windows/resources/chaoset.rc 模板生成并链接图标 + VS_VERSION_INFO；
//! 所有平台：把构建版本注入二进制（单一真源仍是 scripts/version.sh，由 Makefile export）。

fn main() {
    println!("cargo:rerun-if-env-changed=CHAOSET_BUILD_VERSION");
    println!("cargo:rerun-if-env-changed=CHAOSET_VERSION_QUAD");
    let version = std::env::var("CHAOSET_BUILD_VERSION").unwrap_or_else(|_| "dev".to_string());
    println!("cargo:rustc-env=CHAOSET_BUILD_VERSION={version}");

    let target_os = std::env::var("CARGO_CFG_TARGET_OS").unwrap_or_default();
    if target_os != "windows" {
        return;
    }
    let manifest_dir = std::env::var("CARGO_MANIFEST_DIR").unwrap();
    let out_dir = std::env::var("OUT_DIR").unwrap();
    let root = std::path::Path::new(&manifest_dir).join("../..");
    let template_path = root.join("shims/windows/resources/chaoset.rc");
    let Ok(template) = std::fs::read_to_string(&template_path) else {
        println!("cargo:warning=未找到 {}，跳过 Windows 资源", template_path.display());
        return;
    };
    let quad = std::env::var("CHAOSET_VERSION_QUAD").unwrap_or_else(|_| "0.0.0.0".to_string());
    let nums: Vec<&str> = quad.split('.').collect();
    let part = |i: usize| nums.get(i).copied().unwrap_or("0");
    let rc = template
        .replace("@EXTRA_RESOURCE@", "")
        .replace("@VERSION@", &version)
        .replace("@VERSION_QUAD@", &quad)
        .replace("@VER_MAJOR@", part(0))
        .replace("@VER_MINOR@", part(1))
        .replace("@VER_PATCH@", part(2))
        .replace("@VER_BUILD@", part(3))
        .replace("@FILE_TYPE@", "1")
        .replace("@FILE_DESCRIPTION@", "Chaoset 拼音输入法全能命令行控制工具")
        .replace("@INTERNAL_NAME@", "chaosetctl")
        .replace("@ORIGINAL_FILENAME@", "chaosetctl.exe");
    let rc_path = format!("{out_dir}/chaosetctl.rc");
    let res_path = format!("{out_dir}/chaosetctl.res");
    if std::fs::write(&rc_path, rc).is_err() {
        return;
    }
    let ico = root.join("shims/windows/resources/chaoset.ico");
    let _ = ico;
    let ok = std::process::Command::new("rc.exe")
        .args(["/c", "65001", "/fo", &res_path, &rc_path])
        .current_dir(root.join("shims/windows/resources"))
        .status()
        .map(|s| s.success())
        .unwrap_or(false);
    if ok {
        println!("cargo:rustc-link-arg={res_path}");
        return;
    }
    let windres = if std::process::Command::new("x86_64-w64-mingw32-windres")
        .arg("--version")
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
    {
        "x86_64-w64-mingw32-windres"
    } else {
        "windres"
    };
    let ok = std::process::Command::new(windres)
        .args(["--codepage=65001", "-I", ".", "-O", "coff", "-i", &rc_path, "-o", &res_path])
        .current_dir(root.join("shims/windows/resources"))
        .status()
        .map(|s| s.success())
        .unwrap_or(false);
    if ok {
        println!("cargo:rustc-link-arg={res_path}");
    } else {
        println!("cargo:warning=Windows 资源编译失败（rc.exe/windres 均不可用），发布前必须由 CI 校验");
    }
}
