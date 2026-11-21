//! Chaoset 拼音输入法全能命令行工具（Rust multicall）。
//!
//! 支持 argv[0] 多调用（chaoset / chaosetctl / chaoset-cli / chaoset-import /
//! chaoset-userdict / chaoset-settingsctl），Linux 发行包用软链共享同一二进制。

mod buildinfo;
mod importcmd;
mod multicall;
mod settingsctl;
mod userdictcmd;
mod client;

fn main() {
    let argv: Vec<std::ffi::OsString> = std::env::args_os().collect();
    let mut stdout = std::io::stdout();
    let mut stderr = std::io::stderr();
    let stdin_handle = std::io::stdin();
    let mut stdin = stdin_handle.lock();
    let code = multicall::run(argv, &mut stdin, &mut stdout, &mut stderr);
    std::process::exit(code);
}
