//! 单行构建信息：`chaoset <版本> <os>/<arch>[ rev=<12>][ built=<RFC3339>][ dirty]`。
//!
//! 版本来自构建期注入的 `CHAOSET_BUILD_VERSION`（scripts/version.sh 单一真源），
//! 本地裸 cargo 构建回退 `dev`；rev/built 由 build.rs 从 git 读取（无 git 时 no-vcs）。

pub fn info() -> String {
    let version = option_env!("CHAOSET_BUILD_VERSION").unwrap_or("dev");
    let os = std::env::consts::OS;
    let arch = std::env::consts::ARCH;
    let rev = option_env!("CHAOSET_BUILD_REV").unwrap_or("");
    let built = option_env!("CHAOSET_BUILD_TIME").unwrap_or("");
    let dirty = option_env!("CHAOSET_BUILD_DIRTY").unwrap_or("");
    let mut parts: Vec<String> = vec![format!("chaoset {version} {os}/{arch}")];
    if !rev.is_empty() {
        parts.push(format!("rev={rev}"));
    }
    if !built.is_empty() {
        parts.push(format!("built={built}"));
    }
    if dirty == "1" {
        parts.push("dirty".to_string());
    }
    if rev.is_empty() && built.is_empty() {
        parts.push("no-vcs".to_string());
    }
    parts.join(" ")
}
