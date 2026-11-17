#ifndef CHAOSET_SETUP_PAGES_H
#define CHAOSET_SETUP_PAGES_H

// Linux 设置窗页面构建接口（2026-11-06 按页面拆分，模块地图见
// docs/architecture.md §3.3）。
//
// 窗口骨架（GtkApplication、窗口、主题、自绘导航、页脚、入口）在
// chaoset-setup.c；三个内容页各一个 TU，构建函数返回尚未挂到 GtkStack 的
// 页面容器，由骨架负责挂载并设置导航标题。控件信号回调留在各自页面文件。
// 三页共用的卡片/行/说明构造器也在骨架文件，页面只描述内容与顺序。

#include <gtk/gtk.h>

// 设置窗上下文：骨架创建，页面读取。app/window/stack 由骨架维护，
// stats_label 由词库页在构建时填写。
typedef struct {
    GtkApplication *app;
    GtkWidget *window;
    GtkWidget *stack;
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

/* 外观主题应用（冷路径）：system 跟随系统 / light 浅色 / dark 深色。
 * 只设置 GtkSettings:gtk-application-prefer-dark-theme，作用于 GTK3 设置窗；
 * ibus 候选/预编辑颜色由面板决定，本函数不碰。 */
void setup_apply_theme(const char *theme);

// 卡片骨架：底色/描边/圆角与标题样式由 GTK CSS 主题按令牌定义，页面只描述
// 内容。卡片间距固定 16，卡片内标题与内容由骨架排版（标题 13pt 半粗）。
GtkWidget *setup_card_new(const gchar *title_markup);
void setup_card_add(GtkWidget *card, GtkWidget *child);
GtkWidget *setup_row_new(const gchar *label_text);
GtkWidget *setup_hint_new(const gchar *text);
GtkWidget *setup_label_new(const gchar *text, const gchar *css_class);

#endif // CHAOSET_SETUP_PAGES_H
