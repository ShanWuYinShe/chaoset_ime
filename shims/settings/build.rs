fn main() {
    slint_build::compile("ui/app.slint").expect("编译 Slint UI 模板失败");
    // 版本号由构建方注入（Makefile/build.sh 传 CHAOSET_BUILD_VERSION），
    // 变化时必须重编 main.rs，避免设置中心显示旧版本。
    println!("cargo:rerun-if-env-changed=CHAOSET_BUILD_VERSION");

    let target_os = std::env::var("CARGO_CFG_TARGET_OS").unwrap_or_default();
    if target_os == "windows" {
        let out_dir = std::env::var("OUT_DIR").unwrap();
        let manifest_dir = std::env::var("CARGO_MANIFEST_DIR").unwrap();
        let ico_path = std::path::Path::new(&manifest_dir).join("../windows/resources/chaoset.ico");
        let rc_path = format!("{}/chaoset_settings.rc", out_dir);
        let res_path = format!("{}/chaoset_settings.res", out_dir);
        let rc_content = format!("1 ICON \"{}\"\n", ico_path.to_str().unwrap().replace('\\', "/"));
        if std::fs::write(&rc_path, rc_content).is_ok() {
            // 优先尝试使用 rc.exe (MSVC 原生)
            let rc_ok = std::process::Command::new("rc.exe")
                .args(&["/fo", &res_path, &rc_path])
                .status()
                .map(|s| s.success())
                .unwrap_or(false);
            if rc_ok {
                println!("cargo:rustc-link-arg={}", res_path);
            } else {
                // 降级尝试使用 MinGW windres
                let windres_cmd = if std::process::Command::new("x86_64-w64-mingw32-windres").arg("--version").status().map(|s| s.success()).unwrap_or(false) {
                    "x86_64-w64-mingw32-windres"
                } else {
                    "windres"
                };
                let windres_ok = std::process::Command::new(windres_cmd)
                    .args(&["-i", &rc_path, "-O", "coff", "-o", &res_path])
                    .status()
                    .map(|s| s.success())
                    .unwrap_or(false);
                if windres_ok {
                    println!("cargo:rustc-link-arg={}", res_path);
                }
            }
        }
    }
}
