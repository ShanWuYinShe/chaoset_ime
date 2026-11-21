fn env_or(key: &str, fallback: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| fallback.to_string())
}

/// Windows：复用 shims/windows/resources/chaoset.rc 模板生成图标 + VS_VERSION_INFO
/// （rc.exe 优先，windres 兜底）。此前这里只嵌 ico，设置中心 exe 的属性页没有
/// 产品名/公司/版本——三端 GUI app 必须带完整 app 信息的契约要求补齐。
fn emit_windows_resource(
    stem: &str,
    file_description: &str,
    internal_name: &str,
    original_filename: &str,
) {
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
        .replace("@FILE_DESCRIPTION@", file_description)
        .replace("@INTERNAL_NAME@", internal_name)
        .replace("@ORIGINAL_FILENAME@", original_filename);
    let rc_path = format!("{out_dir}/{stem}.rc");
    if std::fs::write(&rc_path, rc).is_err() {
        return;
    }

    // GNU（mingw）目标由 windres 产出 COFF object 交给 ld；MSVC 目标优先 rc.exe
    // 产出 .res 交给 link.exe。中文说明必须按 UTF-8（65001）解释，否则属性页乱码。
    // 工作目录设为资源目录以解析模板里的 chaoset.ico 相对路径。
    let target_env = std::env::var("CARGO_CFG_TARGET_ENV").unwrap_or_default();
    if target_env != "gnu" {
        let res_path = format!("{out_dir}/{stem}.res");
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
    let obj_path = format!("{out_dir}/{stem}_res.o");
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

fn main() {
    slint_build::compile("ui/app.slint").expect("编译 Slint UI 模板失败");
    // 版本号由构建方注入（Makefile/build.sh 传 CHAOSET_BUILD_VERSION），
    // 变化时必须重编 main.rs，避免设置中心显示旧版本。显式转发成 rustc-env：
    // 只靠 ambient 环境变量时 cargo 无法据 build script 输出判断是否重建。
    println!("cargo:rerun-if-env-changed=CHAOSET_BUILD_VERSION");
    println!("cargo:rerun-if-env-changed=CHAOSET_VERSION_QUAD");
    if let Ok(version) = std::env::var("CHAOSET_BUILD_VERSION") {
        println!("cargo:rustc-env=CHAOSET_BUILD_VERSION={version}");
    }

    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() != Ok("windows") {
        return;
    }
    emit_windows_resource(
        "chaoset_settings",
        "Chaoset 拼音输入法跨平台设置中心",
        "chaoset-settings",
        "chaoset-settings.exe",
    );
}
