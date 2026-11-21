// Linux 设置窗「关于」页：产品简介、许可与仓库地址（2026-11-06 按页面
// 拆分）。版本号统一在窗口底部页脚（AGENTS.md 版本可见性），本页不再重复。

#include <gtk/gtk.h>

#include "setup_pages.h"

GtkWidget *setup_page_about_build(AppCtx *ctx) {
    (void)ctx;
    GtkWidget *page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 16);
    GtkWidget *card = setup_card_new("<b>关于</b>");

    /* 头部横排：图标在左、名称与平台副标题在右（与另两端关于页一致）。
     * 优先用 hicolor 里的 "chaoset"；源码直跑/未装图标包时退化为 .brand-icon 品牌标，
     * 避免关于页缺图比其他两端更空。 */
    GtkWidget *header = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 14);
    GtkWidget *icon;
    if (gtk_icon_theme_has_icon(gtk_icon_theme_get_default(), "chaoset")) {
        icon = gtk_image_new_from_icon_name("chaoset", GTK_ICON_SIZE_DIALOG);
        gtk_image_set_pixel_size(GTK_IMAGE(icon), 44);
    } else {
        icon = setup_label_new("C", "brand-icon");
        gtk_label_set_line_wrap(GTK_LABEL(icon), FALSE);
    }
    gtk_widget_set_valign(icon, GTK_ALIGN_START);
    gtk_box_pack_start(GTK_BOX(header), icon, FALSE, FALSE, 0);
    GtkWidget *titles = gtk_box_new(GTK_ORIENTATION_VERTICAL, 2);
    GtkWidget *app_name = setup_label_new("Chaoset 输入法", "title");
    gtk_label_set_line_wrap(GTK_LABEL(app_name), FALSE);
    gtk_box_pack_start(GTK_BOX(titles), app_name, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(titles),
                       setup_hint_new("跨平台中文输入法（Linux ibus 前端）"), FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(header), titles, TRUE, TRUE, 0);
    setup_card_add(card, header);

    setup_card_add(card, setup_hint_new("代码 MIT · 词库 CC BY-SA 4.0"));
    setup_card_add(card, setup_label_new("设置项会随版本持续补充", "tertiary"));
    setup_card_add(card, setup_label_new(
                             "https://github.com/ShanWuYinShe/chaoset_ime", "link"));
    gtk_box_pack_start(GTK_BOX(page), card, FALSE, FALSE, 0);
    return page;
}
