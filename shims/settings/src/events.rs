//! 设置窗事件绑定（从 main.rs 抽出：main 只保留初始化与事件循环，控制文件预算）。

use crate::{bridge, update_theme, MainWindow};
use slint::ComponentHandle;

pub fn wire(window: &MainWindow) {

    // 4. 事件绑定：联想开关
    window.on_associate_toggled(move |enabled| {
        let _ = bridge::set_associate(enabled);
    });

    // 4.1 事件绑定：模糊音开关（daemon 启动时读取，改完重启 daemon 生效）
    window.on_fuzzy_toggled(move |enabled| {
        if bridge::set_fuzzy(enabled).is_ok() {
            bridge::restart_daemon();
        }
    });

    // 4.2 事件绑定：双拼方案（同样需要重启 daemon；重启失败也不改 UI 选中态）
    let w_shuangpin = window.as_weak();
    window.on_shuangpin_changed(move |scheme| {
        if bridge::set_shuangpin(scheme.as_str()).is_ok() {
            bridge::restart_daemon();
            if let Some(w) = w_shuangpin.upgrade() {
                w.set_shuangpin(scheme);
            }
        }
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

    // 7. 事件绑定：清空学习数据（不可恢复，先二次确认；删除学习数据前先停 daemon，
    // 防止内存态旧数据在退出时回写，由 bridge::clear_userdict 负责停/启）。
    let w_handle = window.as_weak();
    window.on_clear_userdict_clicked(move || {
        let confirmed = rfd::MessageDialog::new()
            .set_title("清空学习数据")
            .set_description("将删除本机全部学习数据（调频、词组联想与整句记录），清空后不可恢复。确定继续？")
            .set_buttons(rfd::MessageButtons::YesNo)
            .show()
            == rfd::MessageDialogResult::Yes;
        if !confirmed {
            return;
        }
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
            .set_title("导出学习数据")
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

    // 9. 事件绑定：打开外部链接（检查更新 / 项目主页）。
    window.on_open_url(|url| {
        let _ = crate::open_url::open_url(url.as_str());
    });

    // 9. 事件绑定：导入外部词库
    let w_handle = window.as_weak();
    window.on_import_dict_clicked(move || {
        let files = rfd::FileDialog::new()
            .set_title("导入外部词库")
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
                            // 导入结果在 daemon 启动时加载：与旧设置窗一致，成功
                            // 后立即重启 daemon，避免用户以为没生效。
                            bridge::restart_daemon();
                        }
                        Err(err) => w.set_user_stats_text(format!("导入失败: {}", err).into()),
                    }
                }
            }
        }
    });

}
