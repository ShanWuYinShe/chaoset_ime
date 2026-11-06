// Linux 设置窗「输入」页：词组联想开关、中英状态作用域三选一
// （2026-11-06 按页面拆分）。页面构建函数返回 GtkBox；控件信号回调留在
// 本文件。共享 AppCtx/提示框见 setup_pages.h，设置项读写委托 setup_ctl.h。

#include <gtk/gtk.h>

#include "setup_ctl.h"
#include "setup_pages.h"

static gboolean on_associate_toggled(GtkSwitch *sw, gboolean state, gpointer user_data) {
    (void)user_data;
    if (!setup_config_save(state)) {
        setup_show_message(GTK_WINDOW(gtk_widget_get_toplevel(GTK_WIDGET(sw))),
                           GTK_MESSAGE_ERROR,
                           "保存设置失败，请检查配置目录权限。");
    }
    return FALSE;
}

/* 中英状态作用域单选：只在被选中时保存（GTK 单选会同时发 toggled，
 * 其中一个是取消选中，必须过滤掉）。 */
static void on_mode_scope_toggled(GtkToggleButton *btn, gpointer user_data) {
    (void)user_data;
    if (!gtk_toggle_button_get_active(btn)) {
        return;
    }
    const char *scope = g_object_get_data(G_OBJECT(btn), "chaoset-mode-scope");
    if (scope != NULL && !setup_config_save_mode_scope(scope)) {
        setup_show_message(GTK_WINDOW(gtk_widget_get_toplevel(GTK_WIDGET(btn))),
                           GTK_MESSAGE_ERROR, "保存设置失败，请检查配置目录权限。");
    }
}

GtkWidget *setup_page_input_build(AppCtx *ctx) {
    gboolean associate = TRUE;
    (void)setup_config_load(&associate);

    GtkWidget *input_page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    GtkWidget *box = input_page;

    GtkWidget *group = gtk_label_new(NULL);
    gtk_label_set_markup(GTK_LABEL(group), "<b>输入行为</b>");
    gtk_label_set_xalign(GTK_LABEL(group), 0.0);
    gtk_box_pack_start(GTK_BOX(box), group, FALSE, FALSE, 0);

    GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    GtkWidget *row_label = gtk_label_new("词组联想");
    gtk_label_set_xalign(GTK_LABEL(row_label), 0.0);
    GtkWidget *sw = gtk_switch_new();
    gtk_switch_set_active(GTK_SWITCH(sw), associate);
    gtk_box_pack_start(GTK_BOX(row), row_label, FALSE, FALSE, 0);
    gtk_box_pack_end(GTK_BOX(row), sw, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(box), row, FALSE, FALSE, 0);

    GtkWidget *hint = gtk_label_new("选词上屏后展示可续接的词组候选；关闭后不再弹出联想候选。");
    gtk_label_set_xalign(GTK_LABEL(hint), 0.0);
    gtk_label_set_line_wrap(GTK_LABEL(hint), TRUE);
    gtk_label_set_max_width_chars(GTK_LABEL(hint), 40);
    gtk_style_context_add_class(gtk_widget_get_style_context(hint), "dim-label");
    gtk_box_pack_start(GTK_BOX(box), hint, FALSE, FALSE, 0);

    /* 中英状态作用域（2026-11-06 与 Windows/macOS 设置窗对齐）：
     * 三选一，写入配置文件，引擎下次启动读取。 */
    GtkWidget *mode_group = gtk_label_new(NULL);
    gtk_label_set_markup(GTK_LABEL(mode_group), "<b>中英状态</b>");
    gtk_label_set_xalign(GTK_LABEL(mode_group), 0.0);
    gtk_box_pack_start(GTK_BOX(input_page), mode_group, FALSE, FALSE, 0);

    GtkWidget *mode_row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    const char *current_scope = setup_config_load_mode_scope();
    /* 单选组首成员：GTK3 用「组内某个 radio」作为后续成员的创建参数。
     * 2026-11-06 修复回归：此前把组列表指针当成 radio 成员做类型转换后传构造器，
     * 每个 radio 各自成组、三个可同时选中（中英状态三选一失效）。 */
    GtkRadioButton *mode_group_first = NULL;
    const struct {
        const char *label;
        const char *scope;
    } kScopes[] = {
        {"系统共享", "system"},
        {"每程序", "process"},
        {"每窗口", "window"},
    };
    for (gsize i = 0; i < G_N_ELEMENTS(kScopes); i++) {
        GtkWidget *rb;
        if (mode_group_first == NULL) {
            rb = gtk_radio_button_new_with_label(NULL, kScopes[i].label);
            mode_group_first = GTK_RADIO_BUTTON(rb);
        } else {
            rb = gtk_radio_button_new_with_label_from_widget(
                mode_group_first, kScopes[i].label);
        }
        gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(rb),
                                     g_strcmp0(current_scope, kScopes[i].scope) == 0);
        g_object_set_data_full(G_OBJECT(rb), "chaoset-mode-scope",
                               g_strdup(kScopes[i].scope), g_free);
        g_signal_connect(rb, "toggled", G_CALLBACK(on_mode_scope_toggled), ctx);
        gtk_box_pack_start(GTK_BOX(mode_row), rb, FALSE, FALSE, 0);
    }
    gtk_box_pack_start(GTK_BOX(input_page), mode_row, FALSE, FALSE, 0);

    GtkWidget *mode_hint = gtk_label_new("切一次所有程序都生效（推荐）；后两者仅当前程序/窗口独立。");
    gtk_label_set_xalign(GTK_LABEL(mode_hint), 0.0);
    gtk_label_set_line_wrap(GTK_LABEL(mode_hint), TRUE);
    gtk_label_set_max_width_chars(GTK_LABEL(mode_hint), 40);
    gtk_style_context_add_class(gtk_widget_get_style_context(mode_hint), "dim-label");
    gtk_box_pack_start(GTK_BOX(input_page), mode_hint, FALSE, FALSE, 0);

    g_signal_connect(sw, "state-set", G_CALLBACK(on_associate_toggled), ctx);
    return input_page;
}
