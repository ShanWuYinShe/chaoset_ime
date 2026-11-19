#![windows_subsystem = "windows"]

mod bridge;

use slint::ComponentHandle;

slint::include_modules!();

fn update_theme(window: &MainWindow, theme_str: &str) {
    let is_dark = match theme_str {
        "dark" => true,
        "light" => false,
        _ => {
            // "system": 默认使用浅色，后续可根据操作系统偏好同步
            false
        }
    };
    Theme::get(window).set_is_dark(is_dark);
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let window = MainWindow::new()?;

    // 1. 初始化设置状态
    if let Ok(cfg) = bridge::get_config() {
        window.set_associate_enabled(cfg.associate);
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

    // 4. 事件绑定：联想开关
    window.on_associate_toggled(move |enabled| {
        let _ = bridge::set_associate(enabled);
    });

    // 5. 事件绑定：中英状态作用域
    let w_handle = window.as_weak();
    window.on_mode_scope_changed(move |scope| {
        let _ = bridge::set_mode_scope(scope.as_str());
        if let Some(w) = w_handle.upgrade() {
            w.set_mode_scope(scope);
        }
    });

    // 6. 事件绑定：主题切换
    let w_handle = window.as_weak();
    window.on_theme_changed(move |theme| {
        let _ = bridge::set_theme(theme.as_str());
        if let Some(w) = w_handle.upgrade() {
            w.set_current_theme(theme.clone());
            update_theme(&w, theme.as_str());
        }
    });

    // 7. 事件绑定：清空学习数据
    let w_handle = window.as_weak();
    window.on_clear_userdict_clicked(move || {
        let res = bridge::clear_userdict();
        if let Some(w) = w_handle.upgrade() {
            match res {
                Ok(msg) => {
                    let text = if msg.is_empty() {
                        "已清空学习数据".to_string()
                    } else {
                        msg
                    };
                    w.set_user_stats_text(text.into());
                }
                Err(err) => w.set_user_stats_text(format!("清空失败: {}", err).into()),
            }
        }
    });

    // 8. 事件绑定：导出词组
    let w_handle = window.as_weak();
    window.on_export_userdict_clicked(move || {
        let file = rfd::FileDialog::new()
            .set_file_name("userdict_phrases.tsv")
            .add_filter("TSV 词组文件", &["tsv", "txt"])
            .save_file();
        if let Some(path) = file {
            let res = bridge::export_userdict(&path);
            if let Some(w) = w_handle.upgrade() {
                match res {
                    Ok(msg) => {
                        let text = if msg.is_empty() {
                            format!("已导出到 {}", path.display())
                        } else {
                            msg
                        };
                        w.set_user_stats_text(text.into());
                    }
                    Err(err) => w.set_user_stats_text(format!("导出失败: {}", err).into()),
                }
            }
        }
    });

    // 9. 事件绑定：导入外部词库
    let w_handle = window.as_weak();
    window.on_import_dict_clicked(move || {
        let files = rfd::FileDialog::new()
            .add_filter("词库文件", &["tsv", "txt", "yaml"])
            .pick_files();
        if let Some(files) = files {
            if !files.is_empty() {
                let res = bridge::import_dict(&files);
                if let Some(w) = w_handle.upgrade() {
                    match res {
                        Ok(msg) => {
                            let text = if msg.is_empty() {
                                "词库导入成功".to_string()
                            } else {
                                msg
                            };
                            w.set_user_stats_text(text.into());
                        }
                        Err(err) => w.set_user_stats_text(format!("导入失败: {}", err).into()),
                    }
                }
            }
        }
    });

    window.run()?;
    Ok(())
}
