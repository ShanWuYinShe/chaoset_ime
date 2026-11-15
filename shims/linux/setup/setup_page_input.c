// Linux 设置窗「输入」页：词组联想开关、中英状态二选一（2026-11-06 按页面
// 拆分）。页面构建函数返回 GtkBox；控件信号回调留在本文件。
// 共享 AppCtx/卡片骨架见 setup_pages.h，设置项读写委托 setup_ctl.h。

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

/* 中英状态二选一是互斥单选：toggled + 重入保护（程序化 set_active 会同步再发
 * toggled，没有保护会互相触发到栈溢出）；点击已选项不应变成"无选中"。 */
static gboolean mode_syncing = FALSE;

static void on_mode_scope_toggled(GtkToggleButton *button, gpointer user_data) {
    (void)user_data;
    if (mode_syncing) { return; }
    mode_syncing = TRUE;
    if (!gtk_toggle_button_get_active(button)) {
        gtk_toggle_button_set_active(button, TRUE);
    } else {
        GtkWidget *row = gtk_widget_get_parent(GTK_WIDGET(button));
        GList *children = gtk_container_get_children(GTK_CONTAINER(row));
        for (GList *item = children; item != NULL; item = item->next) {
            if (item->data != (gpointer)button && GTK_IS_TOGGLE_BUTTON(item->data)) {
                gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(item->data), FALSE);
            }
        }
        g_list_free(children);
        const char *scope = g_object_get_data(G_OBJECT(button), "chaoset-mode-scope");
        if (scope != NULL && !setup_config_save_mode_scope(scope)) {
            setup_show_message(GTK_WINDOW(gtk_widget_get_toplevel(GTK_WIDGET(button))),
                               GTK_MESSAGE_ERROR, "保存设置失败，请检查配置目录权限。");
        }
    }
    mode_syncing = FALSE;
}

GtkWidget *setup_page_input_build(AppCtx *ctx) {
    gboolean associate = TRUE;
    (void)setup_config_load(&associate);

    GtkWidget *page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 16);

    GtkWidget *behavior_card = setup_card_new("<b>输入行为</b>");
    GtkWidget *row = setup_row_new("词组联想");
    GtkWidget *sw = gtk_switch_new();
    gtk_switch_set_active(GTK_SWITCH(sw), associate);
    gtk_widget_set_valign(sw, GTK_ALIGN_CENTER);
    gtk_box_pack_end(GTK_BOX(row), sw, FALSE, FALSE, 0);
    setup_card_add(behavior_card, row);
    setup_card_add(behavior_card,
                   setup_hint_new("选词上屏后展示可续接的词组候选；关闭后不再弹出联想候选。"));
    gtk_box_pack_start(GTK_BOX(page), behavior_card, FALSE, FALSE, 0);

    /* 中英状态作用域（与 Windows/macOS 设置窗对齐）：二选一
     * （全局共享 / 每进程独立），写入配置文件，引擎下次启动读取。 */
    GtkWidget *mode_card = setup_card_new("<b>中英状态</b>");
    GtkWidget *mode_row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 8);
    const char *current_scope = setup_config_load_mode_scope();
    const struct {
        const char *label;
        const char *scope;
    } kScopes[] = {
        {"全局共享", "system"},
        {"每进程独立", "process"},
    };
    for (gsize i = 0; i < G_N_ELEMENTS(kScopes); i++) {
        GtkWidget *choice = gtk_toggle_button_new_with_label(kScopes[i].label);
        gtk_style_context_add_class(gtk_widget_get_style_context(choice), "choice");
        gtk_widget_set_size_request(choice, -1, 30);
        gtk_toggle_button_set_active(GTK_TOGGLE_BUTTON(choice),
                                     g_strcmp0(current_scope, kScopes[i].scope) == 0);
        g_object_set_data_full(G_OBJECT(choice), "chaoset-mode-scope",
                               g_strdup(kScopes[i].scope), g_free);
        g_signal_connect(choice, "toggled", G_CALLBACK(on_mode_scope_toggled), ctx);
        gtk_box_pack_start(GTK_BOX(mode_row), choice, FALSE, FALSE, 0);
    }
    setup_card_add(mode_card, mode_row);
    setup_card_add(mode_card,
                   setup_hint_new("切一次所有程序都生效（推荐）；每进程则各自独立。"));
    gtk_box_pack_start(GTK_BOX(page), mode_card, FALSE, FALSE, 0);

    g_signal_connect(sw, "state-set", G_CALLBACK(on_associate_toggled), ctx);
    return page;
}
