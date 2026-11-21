//! 把构建版本注入本 crate（-version 输出用）：单一真源仍是 scripts/version.sh，
//! 裸 cargo 构建读不到 CHAOSET_BUILD_VERSION 时回退 Cargo.toml 的包版本。

fn main() {
    println!("cargo:rerun-if-env-changed=CHAOSET_BUILD_VERSION");
    if let Ok(version) = std::env::var("CHAOSET_BUILD_VERSION") {
        println!("cargo:rustc-env=CHAOSET_TOOLS_BUILD_VERSION={version}");
    }
}
