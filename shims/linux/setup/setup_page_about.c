// Linux 设置窗「关于」页：产品简介、许可与仓库地址（2026-11-06 按页面
// 拆分）。版本号统一在窗口底部页脚（AGENTS.md 版本可见性），本页不再重复。

#include <gtk/gtk.h>

#include "setup_pages.h"

GtkWidget *setup_page_about_build(AppCtx *ctx) {
    (void)ctx;
    GtkWidget *about_page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);

    /* 关于页分组标题与正文（2026-11-06 阶段2：与 Windows 卡片标题「关于」、
     * macOS「关于」分组统一）；版本号统一放在窗口底部页脚，本页不再重复。 */
    GtkWidget *about_group = gtk_label_new(NULL);
    gtk_label_set_markup(GTK_LABEL(about_group), "<b>关于</b>");
    gtk_label_set_xalign(GTK_LABEL(about_group), 0.0);
    gtk_box_pack_start(GTK_BOX(about_page), about_group, FALSE, FALSE, 0);

    GtkWidget *about_desc = gtk_label_new("跨平台中文输入法（Linux IBus 壳）");
    gtk_label_set_xalign(GTK_LABEL(about_desc), 0.0);
    gtk_style_context_add_class(gtk_widget_get_style_context(about_desc), "dim-label");
    gtk_box_pack_start(GTK_BOX(about_page), about_desc, FALSE, FALSE, 0);

    GtkWidget *about_license = gtk_label_new("代码 MIT · 词库 CC BY-SA 4.0");
    gtk_label_set_xalign(GTK_LABEL(about_license), 0.0);
    gtk_style_context_add_class(gtk_widget_get_style_context(about_license), "dim-label");
    gtk_box_pack_start(GTK_BOX(about_page), about_license, FALSE, FALSE, 0);

    GtkWidget *more = gtk_label_new("更多设置将在后续版本提供");
    gtk_label_set_xalign(GTK_LABEL(more), 0.0);
    gtk_style_context_add_class(gtk_widget_get_style_context(more), "dim-label");
    gtk_box_pack_start(GTK_BOX(about_page), more, FALSE, FALSE, 0);

    GtkWidget *repo_label = gtk_label_new("https://github.com/ShanWuYinShe/chaoset_ime");
    gtk_label_set_xalign(GTK_LABEL(repo_label), 0.0);
    gtk_label_set_line_wrap(GTK_LABEL(repo_label), TRUE);
    gtk_style_context_add_class(gtk_widget_get_style_context(repo_label), "dim-label");
    gtk_box_pack_start(GTK_BOX(about_page), repo_label, FALSE, FALSE, 0);

    return about_page;
}
