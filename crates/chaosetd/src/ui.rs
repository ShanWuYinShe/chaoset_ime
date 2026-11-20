pub fn ensure_ui_host(settings: bool) -> std::io::Result<()> {
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        let exe_path = std::env::current_exe()?;
        let ui_path = exe_path.with_file_name("chaoset-ui.exe");
        if !ui_path.exists() {
            return Err(std::io::Error::new(
                std::io::ErrorKind::NotFound,
                format!("找不到 chaoset-ui.exe: {}", ui_path.display()),
            ));
        }

        let mut cmd = std::process::Command::new(ui_path);
        if settings {
            cmd.arg("--settings");
        }
        // DETACHED_PROCESS: 0x00000008
        cmd.creation_flags(0x00000008);
        cmd.spawn()?;
        Ok(())
    }
    #[cfg(not(target_os = "windows"))]
    {
        let _ = settings;
        Err(std::io::Error::new(
            std::io::ErrorKind::Unsupported,
            "单例 UI 宿主仅 Windows 支持",
        ))
    }
}
