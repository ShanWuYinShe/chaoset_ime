//! Windows：复用 shims/windows/resources/chaoset.rc 模板生成图标 + VS_VERSION_INFO
//! （rc.exe 优先，windres 兜底）；版本由构建方注入：CHAOSET_BUILD_VERSION（显示版本）
//! 与 CHAOSET_VERSION_QUAD（4 段数字版本，PE VS_FIXEDFILEINFO 只接受数字）。

fn env_or(key: &str, fallback: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| fallback.to_string())
}

/// 生成并链接 Windows 资源（图标 + 版本信息）；编译失败只告警，由 CI 的
/// scripts/check_windows_version_info.py 兜底断言（不允许缺资源的包发布）。
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
    // 模板在 crate 目录之外：显式声明依赖，改占位符/图标时能触发重建。
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
    // 产出 .res 交给 link.exe。两者的中文说明都必须按 UTF-8（65001）解释，
    // 否则属性页是 mojibake。工作目录设为资源目录以解析模板里的 chaoset.ico。
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

fn run_git(root: &std::path::Path, args: &[&str]) -> Option<String> {
    let out = std::process::Command::new("git")
        .arg("-C")
        .arg(root)
        .args(args)
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    let value = String::from_utf8_lossy(&out.stdout).trim().to_string();
    if value.is_empty() { None } else { Some(value) }
}

/// 注入 git 元数据，供 `version` 显示 rev/构建时间/脏工作区；无 git 时静默跳过。
fn emit_git_meta(root: &std::path::Path) {
    if let Some(rev) = run_git(root, &["rev-parse", "--short=12", "HEAD"]) {
        println!("cargo:rustc-env=CHAOSET_BUILD_REV={rev}");
    }
    if let Some(built) = run_git(root, &["show", "-s", "--format=%cI", "HEAD"]) {
        println!("cargo:rustc-env=CHAOSET_BUILD_TIME={built}");
    }
    if let Some(status) = run_git(root, &["status", "--porcelain", "--untracked-files=no"]) {
        if !status.is_empty() {
            println!("cargo:rustc-env=CHAOSET_BUILD_DIRTY=1");
        }
    }
}

fn main() {
    println!("cargo:rerun-if-env-changed=CHAOSET_BUILD_VERSION");
    println!("cargo:rerun-if-env-changed=CHAOSET_VERSION_QUAD");
    if let Ok(version) = std::env::var("CHAOSET_BUILD_VERSION") {
        println!("cargo:rustc-env=CHAOSET_BUILD_VERSION={version}");
    }
    if let Ok(manifest_dir) = std::env::var("CARGO_MANIFEST_DIR") {
        emit_git_meta(&std::path::Path::new(&manifest_dir).join("../.."));
    }

    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() != Ok("windows") {
        return;
    }
    emit_windows_resource(
        "chaosetd",
        "Chaoset 拼音输入法核心守护进程",
        "chaosetd",
        "chaosetd.exe",
    );
}
