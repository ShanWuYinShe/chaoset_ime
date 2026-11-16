// Chaoset 输入法设置窗口（Linux 原生 GTK3，C）—— 窗口骨架、主题与入口。
// 入口：ibus-setup 选中 Chaoset 后的「首选项」按钮（component XML <setup> 命令）。
// 架构边界：词库合并/用户词典读写委托 Go 工具，C 侧只做 GTK 编排与 ibus 重启（模块地图见 architecture.md §3.3）。
// 单例：GtkApplication(application_id=org.chaoset.Setup)，重复启动只激活已开窗口。

#include <gtk/gtk.h>
#include <string.h>

#include "setup_pages.h"

#ifndef CHAOSET_VERSION
#define CHAOSET_VERSION "dev"
#endif

/* 主题令牌：语义字段与另两端同构（docs/ui-parity.md「设置窗视觉令牌」为唯一事实源）。 */
typedef struct {
    const gchar *window_bg, *nav_bg, *card_bg, *card_border, *divider;
    const gchar *text_primary, *text_secondary, *text_tertiary;
    const gchar *accent, *accent_strong, *accent_pressed, *accent_soft, *accent_text;
    const gchar *control_bg, *control_hover, *control_border, *control_text;
    const gchar *knob, *danger, *on_accent, *shadow, *shadow_alpha;
} SetupTheme;

static const SetupTheme kThemeLight = {
    "#F5EFE6", "#EFE7DB", "#FFFCF7", "#E8DCCC", "#EDE3D6",
    "#2E2924", "#6E655A", "#8A7E72",
    "#C9703F", "#A85427", "#96491F", "#F6E6D9", "#9C4A25",
    "#F1E9DD", "#F7F0E6", "#E0D4C4", "#2E2924",
    "#FFFCF7", "#A8482F", "#FFFDF9", "#5A3C2A", "0.12",
};
static const SetupTheme kThemeDark = {
    "#1C1916", "#171412", "#26221E", "#3A332C", "#332D27",
    "#F3ECE3", "#B4A79A", "#8B7F73",
    "#E0915F", "#E0915F", "#EBA57A", "#3B2A20", "#E9A87C",
    "#221E1A", "#2B2621", "#403830", "#F3ECE3",
    "#F6EFE6", "#D97757", "#2A1B12", "#000000", "0.38",
};

/* 生成整套设置窗样式：只声明需要覆盖的属性，字体 family/行高交给桌面主题；颜色全部来自令牌表。 */
static gchar *theme_css(const SetupTheme *t) {
    GString *css = g_string_new(NULL);
    g_string_append_printf(css,
        "window, window.background { background-color: %s; color: %s; }\n"
        "headerbar, headerbar:backdrop { background-image: none; background-color: %s;"
        " color: %s; border: none; box-shadow: none; min-height: 30px; padding: 0 6px; }\nheaderbar .titlebutton { background: none; border: none; box-shadow: none; color: %s; }\n",
        t->window_bg, t->text_primary, t->window_bg, t->text_primary, t->text_secondary);

    /* 字号用 px 不用 pt：macOS 的 pt 即逻辑像素，GTK 的 pt 按 96dpi 会大 33%，px 才能对齐视觉字号。 */
    g_string_append_printf(css,
        ".title { font-size: 15px; font-weight: bold; color: %s; }\n"
        ".app-title { font-size: 13px; font-weight: bold; color: %s; }\n"
        ".card-title { font-size: 13px; font-weight: bold; color: %s; }\n"
        ".row-label { font-size: 13px; color: %s; }\n"
        ".hint { font-size: 11px; color: %s; }\n"
        ".footer { font-size: 11px; color: %s; }\n"
        ".tertiary { font-size: 11px; color: %s; }\n"
        ".link { font-size: 11px; color: %s; }\n"
        ".brand-icon { background-color: %s; color: %s; border-radius: 10px; font-size: 20px; font-weight: bold; padding: 12px 14px; }\n",
        t->text_primary, t->text_primary, t->text_secondary, t->text_primary,
        t->text_secondary, t->text_secondary, t->text_tertiary, t->accent_text,
        t->accent_strong, t->on_accent);

    g_string_append_printf(css,
        ".nav-column { background-color: %s; }\n"
        ".nav { background-color: transparent; background-image: none; border: none;"
        " border-radius: 10px; color: %s; font-size: 13px; padding: 0 12px; padding-left: 14px; box-shadow: none; }\n"
        ".nav:hover { background-color: %s; }\n"
        ".nav:active { background-color: %s; }\n"
        ".nav:checked, .nav:checked:hover { background-color: %s; color: %s;"
        " font-weight: bold; }\n",
        t->nav_bg, t->text_primary, t->control_hover, t->control_hover,
        t->accent_soft, t->accent_text);

    /* 卡片：圆角 14 / 内边距 18 / 1pt 描边 / 柔和阴影；行高 36 由行容器保证 */
    g_string_append_printf(css,
        ".card { background-color: %s; background-image: none; border: 1px solid %s;"
        " border-radius: 14px; padding: 18px; box-shadow: 0 2px 12px alpha(%s, %s); }\n"
        ".row { min-height: 36px; }\n",
        t->card_bg, t->card_border, t->shadow, t->shadow_alpha);

    g_string_append_printf(css,
        ".choice { background-color: %s; background-image: none; border: 1px solid %s;"
        " border-radius: 9px; color: %s; font-size: 13px; min-height: 30px;"
        " padding: 0 12px; box-shadow: none; }\n"
        ".choice:hover { background-color: %s; }\n"
        ".choice:active { background-color: %s; }\n"
        ".choice:checked, .choice:checked:hover { background-color: %s;"
        " border-color: %s; color: %s; font-weight: bold; }\n",
        t->control_bg, t->control_border, t->control_text, t->control_hover,
        t->control_hover, t->accent_soft, t->accent, t->accent_text);

    g_string_append_printf(css,
        "button { font-size: 13px; min-height: 32px; padding: 0 14px;"
        " border-radius: 9px; box-shadow: none; }\n"
        ".primary, .primary:hover, .primary:active, .primary:backdrop {"
        " background-image: none; border: 1px solid %s; color: %s; }\n"
        ".primary { background-color: %s; }\n"
        ".primary:hover, .primary:active { background-color: %s; border-color: %s; }\n"
        ".secondary, .secondary:active, .secondary:backdrop { background-image: none;"
        " border: 1px solid %s; color: %s; }\n"
        ".secondary { background-color: %s; }\n"
        ".secondary:hover { background-color: %s; }\n"
        ".danger, .danger:backdrop { color: %s; }\n"
        "button:focus, .nav:focus, .choice:focus { box-shadow: inset 0 0 0 2px %s; }\n",
        t->accent_strong, t->on_accent, t->accent_strong, t->accent_pressed,
        t->accent_pressed, t->control_border, t->control_text, t->control_bg,
        t->control_hover, t->danger, t->accent);

    g_string_append_printf(css,
        "switch { min-width: 38px; min-height: 22px; border-radius: 11px;"
        " background-image: none; background-color: %s; border: 1px solid %s; }\n"
        "switch:hover { background-color: %s; }\n"
        "switch:checked, switch:checked:backdrop { background-color: %s;"
        " border-color: %s; }\n"
        "switch:checked:hover { background-color: %s; border-color: %s; }\n"
        "switch slider { min-width: 16px; min-height: 16px; border-radius: 8px;"
        " background-image: none; background-color: %s; border: 1px solid %s; box-shadow: none; }\n"
        "switch:focus { box-shadow: inset 0 0 0 2px %s; }\n",
        t->control_bg, t->control_border, t->control_hover, t->accent, t->accent,
        t->accent_pressed, t->accent_pressed, t->knob, t->control_border, t->accent);

    /* 对话框/文件选择器/禁用态：系统对话框也会继承暖色，避免冷蓝强调渗入 */
    g_string_append_printf(css,
        "dialog, messagedialog, filechooser { background-color: %s; color: %s; }\n"
        "separator { background-color: %s; min-height: 1px; }\n"
        "*:disabled { color: %s; }\n",
        t->window_bg, t->text_primary, t->divider, t->text_tertiary);

    return g_string_free(css, FALSE);
}

static gboolean name_looks_dark(const gchar *name) {
    gchar *lower = name != NULL ? g_ascii_strdown(name, -1) : NULL;
    gboolean dark = lower != NULL && strstr(lower, "dark") != NULL;
    g_free(lower);
    return dark;
}

/* 深浅色判定：优先读窗口背景色相对亮度（跟随实际 GTK 主题）；窗口未实现化时为透明，
 * 退回 GtkSettings prefer-dark 与主题名。 */
static gboolean window_prefers_dark(GtkWidget *window) {
    GtkStyleContext *style = gtk_widget_get_style_context(window);
    GValue value = G_VALUE_INIT; /* GTK 自己初始化 GValue，重复初始化会报 critical */
    gtk_style_context_get_property(style, "background-color", GTK_STATE_FLAG_NORMAL, &value);
    const GdkRGBA *bg = G_VALUE_HOLDS(&value, GDK_TYPE_RGBA) ? g_value_get_boxed(&value) : NULL;
    gboolean dark = FALSE;
    if (bg != NULL && bg->alpha >= 0.5) {
        gdouble luminance = 0.2126 * bg->red + 0.7152 * bg->green + 0.0722 * bg->blue;
        g_value_unset(&value);
        return luminance < 0.5;
    }
    g_value_unset(&value);

    GtkSettings *settings = gtk_settings_get_default();
    if (settings != NULL) {
        gboolean prefer_dark = FALSE;
        gchar *theme_name = NULL;
        g_object_get(settings, "gtk-application-prefer-dark-theme", &prefer_dark, NULL);
        g_object_get(settings, "gtk-theme-name", &theme_name, NULL);
        dark = prefer_dark || name_looks_dark(theme_name);
        g_free(theme_name);
        if (dark) {
            return TRUE;
        }
    }
    return name_looks_dark(g_getenv("GTK_THEME"));
}

static void install_theme(GtkWidget *window) {
    const SetupTheme *theme = window_prefers_dark(window) ? &kThemeDark : &kThemeLight;
    gchar *css = theme_css(theme);
    GtkCssProvider *provider = gtk_css_provider_new();
    GError *error = NULL;
    gtk_css_provider_load_from_data(provider, css, -1, &error);
    g_free(css);
    if (error != NULL) {
        g_warning("Chaoset 设置窗样式加载失败：%s", error->message);
        g_clear_error(&error);
    }
    GdkScreen *screen = gdk_screen_get_default();
    if (screen != NULL) {
        gtk_style_context_add_provider_for_screen(
            screen, GTK_STYLE_PROVIDER(provider), GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    }
    g_object_unref(provider);
}

void setup_show_message(GtkWindow *parent, GtkMessageType type, const gchar *text) {
    GtkWidget *dialog = gtk_message_dialog_new(parent,
                                               GTK_DIALOG_MODAL,
                                               type,
                                               GTK_BUTTONS_OK,
                                               "%s",
                                               text != NULL ? text : "");
    gtk_dialog_run(GTK_DIALOG(dialog));
    gtk_widget_destroy(dialog);
}

/* 面板：需要整块底色的容器。GtkBox 按 CSS 盒模型绘制背景/圆角/描边；EventBox 关 visible_window 会丢底色。 */
static GtkWidget *panel_new(const gchar *css_class) {
    GtkWidget *panel = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_style_context_add_class(gtk_widget_get_style_context(panel), css_class);
    return panel;
}

GtkWidget *setup_label_new(const gchar *text, const gchar *css_class) {
    GtkWidget *label = gtk_label_new(text);
    gtk_label_set_xalign(GTK_LABEL(label), 0.0);
    gtk_label_set_line_wrap(GTK_LABEL(label), TRUE);
    gtk_label_set_max_width_chars(GTK_LABEL(label), 40);
    if (css_class != NULL) {
        gtk_style_context_add_class(gtk_widget_get_style_context(label), css_class);
    }
    return label;
}

GtkWidget *setup_hint_new(const gchar *text) {
    GtkWidget *label = setup_label_new(text, "hint");
    gtk_label_set_max_width_chars(GTK_LABEL(label), 34); /* 只压 preferred 宽，换行仍按卡片内容宽 */
    return label;
}

GtkWidget *setup_card_new(const gchar *title_markup) {
    GtkWidget *card = panel_new("card");
    GtkWidget *body = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    GtkWidget *title = gtk_label_new(NULL);
    gtk_label_set_markup(GTK_LABEL(title), title_markup);
    gtk_label_set_xalign(GTK_LABEL(title), 0.0);
    gtk_style_context_add_class(gtk_widget_get_style_context(title), "card-title");
    gtk_box_pack_start(GTK_BOX(body), title, FALSE, FALSE, 0);
    GtkWidget *content = gtk_box_new(GTK_ORIENTATION_VERTICAL, 8);
    gtk_box_pack_start(GTK_BOX(body), content, FALSE, FALSE, 0);
    g_object_set_data(G_OBJECT(card), "chaoset-card-content", content);
    gtk_container_add(GTK_CONTAINER(card), body);
    return card;
}

void setup_card_add(GtkWidget *card, GtkWidget *child) {
    GtkWidget *content = g_object_get_data(G_OBJECT(card), "chaoset-card-content");
    if (content != NULL) {
        gtk_box_pack_start(GTK_BOX(content), child, FALSE, FALSE, 0);
    }
}

GtkWidget *setup_row_new(const gchar *label_text) {
    GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    gtk_style_context_add_class(gtk_widget_get_style_context(row), "row");
    gtk_widget_set_size_request(row, -1, 36);
    GtkWidget *label = setup_label_new(label_text, "row-label");
    gtk_label_set_line_wrap(GTK_LABEL(label), FALSE);
    gtk_widget_set_hexpand(label, TRUE);
    gtk_box_pack_start(GTK_BOX(row), label, TRUE, TRUE, 0);
    return row;
}

/* 导航互斥单选用 toggled + 重入保护：程序化 set_active 也会同步再发 toggled，无保护会栈溢出。 */
static gboolean nav_syncing = FALSE;
static void on_nav_toggled(GtkToggleButton *button, gpointer user_data) {
    AppCtx *ctx = user_data;
    if (nav_syncing) { return; }
    nav_syncing = TRUE;
    if (!gtk_toggle_button_get_active(button)) {
        /* 再点当前页不应变成"无选中" */
        gtk_toggle_button_set_active(button, TRUE);
    } else {
        GtkWidget *navbox = gtk_widget_get_parent(GTK_WIDGET(button));
        GList *children = gtk_container_get_children(GTK_CONTAINER(navbox));
        for (GList *item = children; item != NULL; item = item->next) {
            if (item->data != (gpointer)button && GTK_IS_TOGGLE_BUTTON(item->data)) {
                gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(item->data), FALSE);
            }
        }
        g_list_free(children);
        const gchar *page = g_object_get_data(G_OBJECT(button), "chaoset-page");
        if (page != NULL && ctx->stack != NULL) { gtk_stack_set_visible_child_name(GTK_STACK(ctx->stack), page); }
    }
    nav_syncing = FALSE;
}
/* 左导航自绘（class nav 胶囊）：GtkStackSidebar 的选中态由主题绘制，做不出三端同构的圆角胶囊。 */
static GtkWidget *build_nav_column(AppCtx *ctx) {
    GtkWidget *column = panel_new("nav-column");
    gtk_widget_set_size_request(column, 148, -1);

    GtkWidget *navbox = gtk_box_new(GTK_ORIENTATION_VERTICAL, 4);
    gtk_widget_set_margin_start(navbox, 12);
    gtk_widget_set_margin_end(navbox, 12);
    gtk_widget_set_margin_top(navbox, 8);
    gtk_container_add(GTK_CONTAINER(column), navbox);

    GtkWidget *app_title = setup_label_new("Chaoset 设置", "app-title");
    gtk_label_set_line_wrap(GTK_LABEL(app_title), FALSE);
    gtk_widget_set_margin_bottom(app_title, 14);
    gtk_box_pack_start(GTK_BOX(navbox), app_title, FALSE, FALSE, 0);

    const struct {
        const gchar *label;
        const gchar *page;
    } kNavItems[] = {
        {"输入", "input"},
        {"词库", "dict"},
        {"关于", "about"},
    };
    for (gsize i = 0; i < G_N_ELEMENTS(kNavItems); i++) {
        GtkWidget *button = gtk_toggle_button_new();
        GtkWidget *button_label = gtk_label_new(kNavItems[i].label);
        gtk_label_set_xalign(GTK_LABEL(button_label), 0.0);
        gtk_widget_set_halign(button_label, GTK_ALIGN_FILL);
        gtk_container_add(GTK_CONTAINER(button), button_label);
        gtk_style_context_add_class(gtk_widget_get_style_context(button), "nav");
        gtk_widget_set_size_request(button, 112, 34);
        gtk_widget_set_halign(button, GTK_ALIGN_START);
        g_object_set_data(G_OBJECT(button), "chaoset-page", (gpointer)kNavItems[i].page);
        g_signal_connect(button, "toggled", G_CALLBACK(on_nav_toggled), ctx);
        gtk_box_pack_start(GTK_BOX(navbox), button, FALSE, FALSE, 0);
        if (i == 0) {
            gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(button), TRUE);
        }
    }
    return column;
}

static GtkWidget *build_window(AppCtx *ctx) {
    GtkWidget *window = gtk_application_window_new(ctx->app);
    ctx->window = window;
    gtk_window_set_resizable(GTK_WINDOW(window), FALSE); gtk_window_set_position(GTK_WINDOW(window), GTK_WIN_POS_CENTER); /* 与 mac/Windows 同：固定 640×560、居中 */
    gtk_window_set_default_size(GTK_WINDOW(window), 640, 560); /* 客户区 640×560，三端同骨架 */
    install_theme(window);

    /* 标题栏自绘（暖色 headerbar，避免系统灰标题栏割裂）。必须 set_titlebar 之后再
     * set_title：GTK3 在 set_titlebar 时清空标题，WM_NAME 会退化成进程名。 */
    GtkWidget *header = gtk_header_bar_new();
    gtk_header_bar_set_show_close_button(GTK_HEADER_BAR(header), TRUE);
    gtk_window_set_titlebar(GTK_WINDOW(window), header);
    gtk_header_bar_set_custom_title(GTK_HEADER_BAR(header), gtk_label_new("")); /* 标题已在左侧导航顶部，避免重复 */
    gtk_window_set_title(GTK_WINDOW(window), "Chaoset 设置");

    /* 用 GtkGrid：横向 GtkBox 会把多余宽度分给导航列（148→192），Grid 里导航固定宽、右侧列吃满。 */
    GtkWidget *root = gtk_grid_new();
    gtk_container_add(GTK_CONTAINER(window), root);
    GtkWidget *vbox = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    GtkWidget *nav_column = build_nav_column(ctx);
    gtk_widget_set_vexpand(nav_column, TRUE);
    gtk_widget_set_hexpand(vbox, TRUE); gtk_widget_set_vexpand(vbox, TRUE);
    gtk_grid_attach(GTK_GRID(root), nav_column, 0, 0, 1, 1); gtk_grid_attach(GTK_GRID(root), vbox, 1, 0, 1, 1);

    GtkWidget *stack = gtk_stack_new();
    ctx->stack = stack;
    gtk_stack_set_transition_type(GTK_STACK(stack), GTK_STACK_TRANSITION_TYPE_CROSSFADE);
    gtk_stack_set_hhomogeneous(GTK_STACK(stack), TRUE);
    gtk_stack_set_vhomogeneous(GTK_STACK(stack), FALSE);
    GtkWidget *input_page = setup_page_input_build(ctx);
    GtkWidget *dict_page = setup_page_dict_build(ctx);
    GtkWidget *about_page = setup_page_about_build(ctx);
    gtk_stack_add_titled(GTK_STACK(stack), input_page, "input", "输入");
    gtk_stack_add_titled(GTK_STACK(stack), dict_page, "dict", "词库");
    gtk_stack_add_titled(GTK_STACK(stack), about_page, "about", "关于");

    gtk_widget_set_margin_start(stack, 24);
    gtk_widget_set_margin_end(stack, 24);
    gtk_widget_set_margin_top(stack, 10); /* +headerbar 34 = 44，与 mac/Windows 内容起点一致 */
    gtk_box_pack_start(GTK_BOX(vbox), stack, TRUE, TRUE, 0);

    /* 页脚版本号：固定在窗口最底、与内容列左对齐（checker 断言装配行） */
    gchar *footer_text = g_strdup_printf("Chaoset 版本 %s", CHAOSET_VERSION);
    GtkWidget *footer = gtk_label_new(footer_text);
    g_free(footer_text);
    gtk_label_set_xalign(GTK_LABEL(footer), 0.0);
    gtk_style_context_add_class(gtk_widget_get_style_context(footer), "footer");
    gtk_widget_set_margin_start(footer, 24);
    gtk_widget_set_margin_end(footer, 24);
    gtk_widget_set_margin_top(footer, 12);
    gtk_widget_set_margin_bottom(footer, 16);
    gtk_box_pack_start(GTK_BOX(vbox), footer, FALSE, FALSE, 0);

    gtk_widget_show_all(window);
    setup_page_dict_refresh(ctx);
    return window;
}

static void on_activate(GtkApplication *app, gpointer user_data) {
    (void)user_data;
    GtkWindow *existing = gtk_application_get_active_window(app);
    if (existing != NULL) {
        gtk_window_present(existing);
        return;
    }
    AppCtx *ctx = g_new0(AppCtx, 1);
    ctx->app = app;
    build_window(ctx);
    gtk_window_present(GTK_WINDOW(ctx->window));
}

int main(int argc, char **argv) {
    GtkApplication *app = gtk_application_new("org.chaoset.Setup", G_APPLICATION_DEFAULT_FLAGS);
    g_signal_connect(app, "activate", G_CALLBACK(on_activate), NULL);
    int status = g_application_run(G_APPLICATION(app), argc, argv);
    g_object_unref(app);
    return status;
}
