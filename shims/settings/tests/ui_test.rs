use chaoset_settings::{update_theme, MainWindow, Theme};
use slint::Global;

fn assert_color(c: slint::Color, r: u8, g: u8, b: u8, name: &str) {
    assert_eq!(
        (c.red(), c.green(), c.blue()),
        (r, g, b),
        "颜色令牌 {} 不匹配: 实际 ({}, {}, {}) vs 期望 ({}, {}, {})",
        name,
        c.red(),
        c.green(),
        c.blue(),
        r,
        g,
        b
    );
}

fn init_test_backend() {
    let _ = std::panic::catch_unwind(|| {
        i_slint_backend_testing::init_no_event_loop();
    });
}

#[test]
fn test_main_window_init_state() {
    init_test_backend();
    let window = MainWindow::new().unwrap();
    assert_eq!(window.get_current_page(), 0);
    assert!(window.get_associate_enabled());
    assert_eq!(window.get_mode_scope(), "system");
    assert_eq!(window.get_current_theme(), "system");
}

#[test]
fn test_ui_parity_visual_tokens_light() {
    init_test_backend();
    let window = MainWindow::new().unwrap();
    let theme = Theme::get(&window);
    theme.set_is_dark(false);

    // 21 项视觉令牌（Light 模式）
    assert_color(theme.get_window_bg(), 0xF4, 0xF7, 0xFB, "window_bg");
    assert_color(theme.get_nav_bg(), 0xEA, 0xF1, 0xF9, "nav_bg");
    assert_color(theme.get_card_bg(), 0xFF, 0xFF, 0xFF, "card_bg");
    assert_color(theme.get_card_border(), 0xD9, 0xE4, 0xF0, "card_border");
    assert_color(theme.get_divider(), 0xE4, 0xED, 0xF6, "divider");
    assert_color(theme.get_text_primary(), 0x14, 0x21, 0x3D, "text_primary");
    assert_color(theme.get_text_secondary(), 0x4A, 0x5A, 0x70, "text_secondary");
    assert_color(theme.get_text_tertiary(), 0x7A, 0x88, 0x99, "text_tertiary");
    assert_color(theme.get_accent(), 0x00, 0x7A, 0xFF, "accent");
    assert_color(theme.get_accent_strong(), 0x00, 0x66, 0xEB, "accent_strong");
    assert_color(theme.get_accent_pressed(), 0x00, 0x52, 0xBE, "accent_pressed");
    assert_color(theme.get_accent_soft(), 0xE5, 0xF1, 0xFF, "accent_soft");
    assert_color(theme.get_accent_text(), 0x00, 0x66, 0xEB, "accent_text");
    assert_color(theme.get_control_bg(), 0xF0, 0xF5, 0xFB, "control_bg");
    assert_color(theme.get_control_hover(), 0xE3, 0xED, 0xF8, "control_hover");
    assert_color(theme.get_control_border(), 0xC9, 0xD9, 0xEB, "control_border");
    assert_color(theme.get_control_text(), 0x14, 0x21, 0x3D, "control_text");
    assert_color(theme.get_knob(), 0xFF, 0xFF, 0xFF, "knob");
    assert_color(theme.get_danger(), 0xD3, 0x2F, 0x2F, "danger");
    assert_color(theme.get_on_accent(), 0xFF, 0xFF, 0xFF, "on_accent");
}

#[test]
fn test_ui_parity_visual_tokens_dark() {
    init_test_backend();
    let window = MainWindow::new().unwrap();
    let theme = Theme::get(&window);
    theme.set_is_dark(true);

    // 21 项视觉令牌（Dark 模式）
    assert_color(theme.get_window_bg(), 0x10, 0x1A, 0x2C, "window_bg");
    assert_color(theme.get_nav_bg(), 0x0C, 0x16, 0x26, "nav_bg");
    assert_color(theme.get_card_bg(), 0x1A, 0x27, 0x40, "card_bg");
    assert_color(theme.get_card_border(), 0x2D, 0x3E, 0x5C, "card_border");
    assert_color(theme.get_divider(), 0x24, 0x35, 0x52, "divider");
    assert_color(theme.get_text_primary(), 0xF2, 0xF6, 0xFC, "text_primary");
    assert_color(theme.get_text_secondary(), 0xAF, 0xBF, 0xD6, "text_secondary");
    assert_color(theme.get_text_tertiary(), 0x7E, 0x90, 0xAC, "text_tertiary");
    assert_color(theme.get_accent(), 0x00, 0x66, 0xEB, "accent");
    assert_color(theme.get_accent_strong(), 0x00, 0x7A, 0xFF, "accent_strong");
    assert_color(theme.get_accent_pressed(), 0x4D, 0xA3, 0xFF, "accent_pressed");
    assert_color(theme.get_accent_soft(), 0x16, 0x34, 0x5C, "accent_soft");
    assert_color(theme.get_accent_text(), 0x7F, 0xB4, 0xFF, "accent_text");
    assert_color(theme.get_control_bg(), 0x22, 0x33, 0x53, "control_bg");
    assert_color(theme.get_control_hover(), 0x2C, 0x41, 0x65, "control_hover");
    assert_color(theme.get_control_border(), 0x3A, 0x50, 0x74, "control_border");
    assert_color(theme.get_control_text(), 0xF2, 0xF6, 0xFC, "control_text");
    assert_color(theme.get_knob(), 0xF2, 0xF6, 0xFC, "knob");
    assert_color(theme.get_danger(), 0xFF, 0x7B, 0x72, "danger");
    assert_color(theme.get_on_accent(), 0xFF, 0xFF, 0xFF, "on_accent");
}

#[test]
fn test_ui_parity_geometric_tokens() {
    init_test_backend();
    let window = MainWindow::new().unwrap();
    let theme = Theme::get(&window);
    assert_eq!(theme.get_card_radius(), 14.0);
    assert_eq!(theme.get_card_pad(), 18.0);
    assert_eq!(theme.get_card_gap(), 16.0);
    assert_eq!(theme.get_row_height(), 36.0);
    assert_eq!(theme.get_nav_width(), 148.0);
    assert_eq!(theme.get_switch_width(), 38.0);
    assert_eq!(theme.get_switch_height(), 22.0);
    assert_eq!(theme.get_button_height(), 32.0);
    assert_eq!(theme.get_control_radius(), 9.0);
    assert_eq!(theme.get_nav_item_radius(), 10.0);
}

#[test]
fn test_update_theme_logic() {
    init_test_backend();
    let window = MainWindow::new().unwrap();
    update_theme(&window, "dark");
    assert!(Theme::get(&window).get_is_dark());
    update_theme(&window, "light");
    assert!(!Theme::get(&window).get_is_dark());
}

#[test]
fn test_callbacks_and_pages() {
    init_test_backend();
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::Arc;

    let window = MainWindow::new().unwrap();

    let toggled = Arc::new(AtomicBool::new(false));
    let t_clone = toggled.clone();
    window.on_associate_toggled(move |v| {
        t_clone.store(v, Ordering::SeqCst);
    });
    window.invoke_associate_toggled(true);
    assert!(toggled.load(Ordering::SeqCst));

    window.set_current_page(1);
    assert_eq!(window.get_current_page(), 1);
    window.set_current_page(2);
    assert_eq!(window.get_current_page(), 2);
}
