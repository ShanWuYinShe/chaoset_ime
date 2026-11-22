//! 设置窗打开外部链接：只在用户点击「检查更新 / 项目主页」时调用（冷路径）。

use std::process::Command;

/// 用系统默认浏览器打开链接；失败返回可展示给用户的中文错误。
pub fn open_url(url: &str) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    let status = Command::new("open").arg(url).status();
    #[cfg(target_os = "linux")]
    let status = Command::new("xdg-open").arg(url).status();
    #[cfg(target_os = "windows")]
    let status = {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        Command::new("cmd")
            .args(["/C", "start", "", url])
            .creation_flags(CREATE_NO_WINDOW)
            .status()
    };
    status.map(|_| ()).map_err(|e| format!("打开链接失败: {e}"))
}
