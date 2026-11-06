// Chaoset 输入法设置窗口（Linux 原生 GTK3，C）—— 窗口骨架与入口。
//
// 入口：ibus-setup 选中 Chaoset 后的「首选项」按钮（component XML 的
// <setup> 命令）；也可直接运行。与 macOS SettingsWindow.swift / Windows
// 设置窗三端同构：输入行为、学习数据、词库导入。
//
// 架构边界：本文件是 shims 平台 UI；词库合并/用户词典读写全部委托 Go 工具
// （chaoset-userdict / chaoset-import），C 侧只做 GTK 编排与 ibus 重启。
// 2026-11-06 按页面拆分（模块地图见 docs/architecture.md §3.3）：
//   setup_page_input.c / setup_page_dict.c / setup_page_about.c = 三个内容页；
//   setup_pages.h = 页面接口与共享 AppCtx；
//   本文件 = GtkApplication / 窗口 / 左导航 / 页脚 / 入口。
//
// 单例：GtkApplication(application_id=org.chaoset.Setup)，重复启动只激活
// 已打开窗口。

#include <gtk/gtk.h>

#include "setup_pages.h"

#ifndef CHAOSET_VERSION
#define CHAOSET_VERSION "dev"
#endif

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

static GtkWidget *build_window(AppCtx *ctx) {
    GtkWidget *window = gtk_application_window_new(ctx->app);
    ctx->window = window;
    gtk_window_set_title(GTK_WINDOW(window), "Chaoset 设置");
    gtk_window_set_resizable(GTK_WINDOW(window), FALSE);
    gtk_window_set_position(GTK_WINDOW(window), GTK_WIN_POS_CENTER);
    /* 客户区 640x560：与 Windows/macOS 设置窗对齐（2026-11-06 阶段2）。 */
    gtk_window_set_default_size(GTK_WINDOW(window), 640, 560);

    /* 布局：左侧导航（GtkStackSidebar：输入 / 词库 / 关于）+ 右侧内容页
     * （GtkStack）+ 底部固定页脚版本号；2026-11-06 阶段2 与 Windows/macOS
     * 设置窗对齐。卡片外观只在 Windows 上实现（AppKit/GTK 不做像素级卡片），
     * Linux 用原生分组：GtkStackSidebar + 分组标题。 */
    GtkWidget *vbox = gtk_box_new(GTK_ORIENTATION_VERTICAL, 6);
    gtk_container_add(GTK_CONTAINER(window), vbox);
    GtkWidget *hbox = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    gtk_widget_set_margin_start(hbox, 12);
    gtk_widget_set_margin_end(hbox, 12);
    gtk_widget_set_margin_top(hbox, 12);
    gtk_box_pack_start(GTK_BOX(vbox), hbox, TRUE, TRUE, 0);
    GtkWidget *stack = gtk_stack_new();
    gtk_stack_set_transition_type(GTK_STACK(stack), GTK_STACK_TRANSITION_TYPE_CROSSFADE);
    gtk_stack_set_hhomogeneous(GTK_STACK(stack), TRUE);
    gtk_stack_set_vhomogeneous(GTK_STACK(stack), FALSE);
    GtkWidget *sidebar = gtk_stack_sidebar_new();
    gtk_stack_sidebar_set_stack(GTK_STACK_SIDEBAR(sidebar), GTK_STACK(stack));
    gtk_box_pack_start(GTK_BOX(hbox), sidebar, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(hbox), stack, TRUE, TRUE, 0);
    GtkWidget *input_page = setup_page_input_build(ctx);
    GtkWidget *dict_page = setup_page_dict_build(ctx);
    GtkWidget *about_page = setup_page_about_build(ctx);
    gtk_widget_set_margin_start(input_page, 12);
    gtk_widget_set_margin_end(input_page, 12);
    gtk_widget_set_margin_start(dict_page, 12);
    gtk_widget_set_margin_end(dict_page, 12);
    gtk_widget_set_margin_start(about_page, 12);
    gtk_widget_set_margin_end(about_page, 12);
    /* GtkStack 是 GtkContainer 而不是 GtkBox：页面必须用 gtk_stack_add_titled
     * 挂载（2026-11-06 修复回归：此前误把 stack 当 box 调用 box 的 pack API 挂页，
     * GTK 断言失败导致三个内容页从未挂到 GtkStack、设置窗内容区为空）。
     * 该 API 同时设置子属性 name 与 title，GtkStackSidebar 的导航项取自 title。 */
    gtk_stack_add_titled(GTK_STACK(stack), input_page, "input", "输入");
    gtk_stack_add_titled(GTK_STACK(stack), dict_page, "dict", "词库");
    gtk_stack_add_titled(GTK_STACK(stack), about_page, "about", "关于");

    /* 页脚版本号：所有页固定显示（AGENTS.md 版本可见性；与 Windows/macOS 页脚
     * 同语义），取代「版本号只放关于页」的旧形态（2026-11-06 阶段2）。 */
    gchar *footer_text = g_strdup_printf("Chaoset 版本 %s", CHAOSET_VERSION);
    GtkWidget *footer = gtk_label_new(footer_text);
    g_free(footer_text);
    gtk_label_set_xalign(GTK_LABEL(footer), 0.0);
    gtk_style_context_add_class(gtk_widget_get_style_context(footer), "dim-label");
    gtk_widget_set_margin_start(footer, 12);
    gtk_widget_set_margin_end(footer, 12);
    gtk_widget_set_margin_bottom(footer, 8);
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
