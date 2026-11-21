#![windows_subsystem = "windows"]

use chaoset_settings::{bridge, events, update_theme, MainWindow};
use slint::ComponentHandle;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let window = MainWindow::new()?;

    // 0. 解析命令行参数以决定初始展示页
    let mut initial_page = 0;
    let args: Vec<String> = std::env::args().collect();
    let mut i = 1;
    while i < args.len() {
        if args[i] == "--page" && i + 1 < args.len() {
            match args[i + 1].as_str() {
                "input" | "0" => initial_page = 0,
                "dict" | "1" => initial_page = 1,
                "about" | "2" => initial_page = 2,
                _ => {}
            }
            i += 2;
        } else {
            i += 1;
        }
    }
    window.set_current_page(initial_page);

    // 1. 初始化设置状态
    if let Ok(cfg) = bridge::get_config() {
        window.set_associate_enabled(cfg.associate);
        window.set_fuzzy_enabled(cfg.fuzzy);
        window.set_shuangpin(cfg.shuangpin.clone().into());
        window.set_mode_scope(cfg.mode_scope.clone().into());
        window.set_current_theme(cfg.theme.clone().into());
        update_theme(&window, &cfg.theme);
    }

    // 2. 加载词典统计信息
    if let Ok(stats) = bridge::get_userdict_stats() {
        window.set_user_stats_text(stats.into());
    } else {
        window.set_user_stats_text("暂无学习数据".into());
    }

    // 3. 版本号信息
    let version = env!("CARGO_PKG_VERSION");
    window.set_version_text(format!("Chaoset 版本 {}", version).into());

    // 4. 事件绑定（联想/模糊音/双拼/中英作用域/主题/学习数据/词库导入）见 events.rs。
    events::wire(&window);

    window.run()?;
    Ok(())
}
