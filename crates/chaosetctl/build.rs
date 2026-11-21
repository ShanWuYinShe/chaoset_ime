//! Windows：由 shims/windows/resources/chaoset.rc 模板生成并链接图标 + VS_VERSION_INFO
//! （rc.exe /c 65001 优先，windres -c 65001 兜底；工作目录 = 资源目录，模板里的
//! `1 ICON "chaoset.ico"` 才能解析）。
//! 所有平台：把构建版本注入二进制（单一真源仍是 scripts/version.sh，由 Makefile export）。

fn env_or(key: &str, fallback: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| fallback.to_string())
}

fn main() {
    println!("cargo:rerun-if-env-changed=CHAOSET_BUILD_VERSION");
    println!("cargo:rerun-if-env-changed=CHAOSET_VERSION_QUAD");
    if let Ok(version) = std::env::var("CHAOSET_BUILD_VERSION") {
        println!("cargo:rustc-env=CHAOSET_BUILD_VERSION={version}");
    }

    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() != Ok("windows") {
        return;
    }

    let manifest_dir = std::env::var("CARGO_MANIFEST_DIR").unwrap();
    let out_dir = std::env::var("OUT_DIR").unwrap();
    let root = std::path::Path::new(&manifest_dir).join("../..");
    let resources = root.join("shims/windows/resources");
    let template_path = resources.join("chaoset.rc");
    println!("cargo:rerun-if-changed={}", template_path.display());
    println!(
        "cargo:rerun-if-changed={}",
        resources.join("chaoset.ico").display()
    );

    let Ok(template) = std::fs::read_to_string(&template_path) else {
        println!(
            "cargo:warning=未找到 {}，跳过 Windows 资源",
            template_path.display()
        );
        return;
    };
    let version = env_or("CHAOSET_BUILD_VERSION", "dev");
    let quad = env_or("CHAOSET_VERSION_QUAD", "0.0.0.0");
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
    if std::fs::write(&rc_path, rc).is_err() {
        return;
    }

    // GNU（mingw）目标：windres 产出 COFF object 交给 ld。
    let target_env = std::env::var("CARGO_CFG_TARGET_ENV").unwrap_or_default();
    if target_env != "gnu" {
        // MSVC 目标：rc.exe 产出 .res 交给 link.exe（/c 65001 = 按 UTF-8 读源码，
        // 否则模板里的中文说明会变成 mojibake）。
        let res_path = format!("{out_dir}/chaosetctl.res");
        let rc_ok = std::process::Command::new("rc.exe")
            .args(["/c", "65001", "/fo", &res_path, &rc_path])
            .current_dir(&resources)
            .status()
            .map(|s| s.success())
            .unwrap_or(false);
        if rc_ok {
            println!("cargo:rustc-link-arg={res_path}");
            return;
        }
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
    let obj_path = format!("{out_dir}/chaosetctl_res.o");
    let windres_ok = std::process::Command::new(windres)
        .args([
            "-c", "65001", "-I", ".", "-O", "coff", "-i", &rc_path, "-o", &obj_path,
        ])
        .current_dir(&resources)
        .status()
        .map(|s| s.success())
        .unwrap_or(false);
    if windres_ok {
        println!("cargo:rustc-link-arg={obj_path}");
    } else {
        println!(
            "cargo:warning=Windows 资源编译失败（rc.exe/windres 均不可用），发布前必须由 CI 校验"
        );
    }
}
