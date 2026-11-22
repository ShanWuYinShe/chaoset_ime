pub mod bridge;
pub mod events;
pub mod open_url;

slint::include_modules!();

pub fn detect_system_dark() -> bool {
    #[cfg(target_os = "macos")]
    {
        if let Ok(output) = std::process::Command::new("defaults")
            .args(["read", "-g", "AppleInterfaceStyle"])
            .output()
        {
            if output.status.success() {
                let s = String::from_utf8_lossy(&output.stdout);
                return s.trim() == "Dark";
            }
        }
    }
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        let mut cmd = std::process::Command::new("reg");
        cmd.args(["query", r"HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize", "/v", "AppsUseLightTheme"]);
        cmd.creation_flags(CREATE_NO_WINDOW);
        if let Ok(output) = cmd.output() {
            if output.status.success() {
                let s = String::from_utf8_lossy(&output.stdout);
                if s.contains("0x0") {
                    return true;
                }
            }
        }
    }
    #[cfg(target_os = "linux")]
    {
        if let Ok(output) = std::process::Command::new("gsettings")
            .args(["get", "org.gnome.desktop.interface", "color-scheme"])
            .output()
        {
            if output.status.success() {
                let s = String::from_utf8_lossy(&output.stdout).to_lowercase();
                if s.contains("dark") {
                    return true;
                }
            }
        }
    }
    false
}

pub fn update_theme(window: &MainWindow, theme_str: &str) {
    let is_dark = match theme_str {
        "dark" => true,
        "light" => false,
        _ => detect_system_dark(),
    };
    Theme::get(window).set_is_dark(is_dark);
}
