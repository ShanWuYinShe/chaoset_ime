#ifndef CHAOSET_SETUP_PAGES_H
#define CHAOSET_SETUP_PAGES_H

// Linux 设置窗页面构建接口（2026-11-06 按页面拆分，模块地图见
// docs/architecture.md §3.3）。
//
// 窗口骨架（GtkApplication、窗口、左导航 GtkStackSidebar、页脚、入口）在
// chaoset-setup.c；三个内容页各一个 TU，构建函数返回尚未挂到 GtkStack 的
// 页面容器，由骨架负责 pack 并设置导航标题。控件信号回调留在各自页面文件。

#include <gtk/gtk.h>

// 设置窗上下文：骨架创建，页面读取。app/window 由骨架维护，
// stats_label 由词库页在构建时填写。
typedef struct {
    GtkApplication *app;
    GtkWidget *window;
    GtkWidget *stats_label;
} AppCtx;

// 页面构建：返回一个新的垂直 GtkBox（导航标题由骨架设置）。
GtkWidget *setup_page_input_build(AppCtx *ctx);
GtkWidget *setup_page_dict_build(AppCtx *ctx);
GtkWidget *setup_page_about_build(AppCtx *ctx);

// 词库页「学习数据」统计刷新：窗口骨架在 gtk_widget_show_all 之后调用，
// 保持拆分前的时序（先映射窗口再读统计）。
void setup_page_dict_refresh(AppCtx *ctx);

// 共享模态提示框：各页面回调展示结果/错误用。
void setup_show_message(GtkWindow *parent, GtkMessageType type, const gchar *text);

#endif // CHAOSET_SETUP_PAGES_H
