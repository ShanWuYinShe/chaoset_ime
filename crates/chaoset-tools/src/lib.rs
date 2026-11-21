//! 词库导入（TSV/Rime YAML/CC-CEDICT）+ 用户词典本地文件工具。

pub mod dictimport;
pub mod dictparse;
pub mod fsutil;
pub mod userdict;

/// 单行构建信息（-version 用）：版本来自构建期注入的 CHAOSET_BUILD_VERSION
/// （scripts/version.sh 单一真源），裸 cargo 构建回退 Cargo.toml 包版本。
pub fn version_info() -> String {
    let version = option_env!("CHAOSET_TOOLS_BUILD_VERSION").unwrap_or(env!("CARGO_PKG_VERSION"));
    format!(
        "chaoset {version} {}/{} no-vcs",
        std::env::consts::OS,
        std::env::consts::ARCH
    )
}
