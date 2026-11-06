// Chaoset 输入法设置窗口（Linux 原生 GTK3，C）。
//
// 入口：ibus-setup 选中 Chaoset 后的「首选项」按钮（component XML 的
// <setup> 命令）；也可直接运行。与 macOS SettingsWindow.swift / Windows
// 设置窗三端同构：输入行为、学习数据、词库导入。
//
// 架构边界：本文件是 shims 平台 UI；词库合并/用户词典读写全部委托 Go 工具
// （chaoset-userdict / chaoset-import），C 侧只做 GTK 编排与 ibus 重启。
//
// 单例：Gtk.Application(application_id=org.chaoset.Setup)，重复启动只激活
// 已打开窗口。

#include <gtk/gtk.h>

#include "setup_ctl.h"

#ifndef CHAOSET_VERSION
#define CHAOSET_VERSION "dev"
#endif

typedef struct {
    GtkApplication *app;
    GtkWidget *window;
    GtkWidget *stats_label;
} AppCtx;

typedef SetupResult (*WorkFunc)(gpointer data);
typedef void (*DoneFunc)(gpointer data, SetupResult result);

typedef struct {
    GtkWidget *window;
    WorkFunc work;
    gpointer data;
    GDestroyNotify data_free;
    DoneFunc done;
    gpointer done_data;
} AsyncJob;

typedef struct {
    AsyncJob *job;
    SetupResult result;
} AsyncDone;

static void show_message(GtkWindow *parent, GtkMessageType type, const gchar *text) {
    GtkWidget *dialog = gtk_message_dialog_new(parent,
                                               GTK_DIALOG_MODAL,
                                               type,
                                               GTK_BUTTONS_OK,
                                               "%s",
                                               text != NULL ? text : "");
    gtk_dialog_run(GTK_DIALOG(dialog));
    gtk_widget_destroy(dialog);
}

static gboolean async_finish(gpointer user_data) {
    AsyncDone *done = user_data;
    AsyncJob *job = done->job;
    gtk_widget_set_sensitive(job->window, TRUE);
    if (job->done != NULL) {
        job->done(job->done_data, done->result);
    } else {
        setup_result_free(&done->result);
    }
    if (job->data_free != NULL) {
        job->data_free(job->data);
    }
    g_free(job);
    g_free(done);
    return G_SOURCE_REMOVE;
}

static gpointer async_worker(gpointer user_data) {
    AsyncJob *job = user_data;
    SetupResult result = job->work(job->data);
    AsyncDone *done = g_new0(AsyncDone, 1);
    done->job = job;
    done->result = result;
    g_idle_add(async_finish, done);
    return NULL;
}

// run_async 把耗时操作放后台线程；完成后回主线程恢复窗口并回调。
// GTK 非线程安全：工作线程只跑子进程/文件 IO，绝不碰控件。
static void run_async(GtkWidget *window, WorkFunc work, gpointer data,
                      GDestroyNotify data_free, DoneFunc done, gpointer done_data) {
    AsyncJob *job = g_new0(AsyncJob, 1);
    job->window = window;
    job->work = work;
    job->data = data;
    job->data_free = data_free;
    job->done = done;
    job->done_data = done_data;
    gtk_widget_set_sensitive(window, FALSE);
    GThread *thread = g_thread_new("chaoset-setup-op", async_worker, job);
    g_thread_unref(thread);
}

static void refresh_stats(AppCtx *ctx) {
    SetupResult result = setup_userdict_stats();
    if (result.ok) {
        gtk_label_set_text(GTK_LABEL(ctx->stats_label), result.message);
    } else {
        gchar *text = g_strdup_printf("无法读取学习数据：%s", result.message);
        gtk_label_set_text(GTK_LABEL(ctx->stats_label), text);
        g_free(text);
    }
    setup_result_free(&result);
}

static void on_clear_done(gpointer data, SetupResult result) {
    AppCtx *ctx = data;
    show_message(GTK_WINDOW(ctx->window),
                 result.ok ? GTK_MESSAGE_INFO : GTK_MESSAGE_ERROR,
                 result.message);
    setup_result_free(&result);
    refresh_stats(ctx);
}

static SetupResult work_clear(gpointer data) {
    (void)data;
    return setup_userdict_clear();
}

static void on_clear_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    AppCtx *ctx = user_data;
    GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(ctx->window),
                                               GTK_DIALOG_MODAL,
                                               GTK_MESSAGE_WARNING,
                                               GTK_BUTTONS_OK_CANCEL,
                                               "%s",
                                               "清空学习数据？");
    gtk_message_dialog_format_secondary_text(GTK_MESSAGE_DIALOG(dialog),
        "将删除全部调频词、用户词组与上下文共现记录（仅本机），操作不可恢复。\n"
        "输入法正在运行时，需要先重启 ibus（期间输入法短暂不可用）。");
    gint response = gtk_dialog_run(GTK_DIALOG(dialog));
    gtk_widget_destroy(dialog);
    if (response != GTK_RESPONSE_OK) {
        return;
    }
    run_async(ctx->window, work_clear, NULL, NULL, on_clear_done, ctx);
}

static SetupResult work_export(gpointer data) {
    return setup_userdict_export(data);
}

static void on_export_done(gpointer data, SetupResult result) {
    AppCtx *ctx = data;
    show_message(GTK_WINDOW(ctx->window),
                 result.ok ? GTK_MESSAGE_INFO : GTK_MESSAGE_ERROR,
                 result.message);
    setup_result_free(&result);
}

static void on_export_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    AppCtx *ctx = user_data;
    GtkWidget *chooser = gtk_file_chooser_dialog_new("导出学习数据",
                                                     GTK_WINDOW(ctx->window),
                                                     GTK_FILE_CHOOSER_ACTION_SAVE,
                                                     "取消", GTK_RESPONSE_CANCEL,
                                                     "保存", GTK_RESPONSE_ACCEPT,
                                                     NULL);
    gtk_file_chooser_set_do_overwrite_confirmation(GTK_FILE_CHOOSER(chooser), TRUE);
    gtk_file_chooser_set_current_name(GTK_FILE_CHOOSER(chooser), "chaoset-userdict.tsv");
    GtkFileFilter *filter = gtk_file_filter_new();
    gtk_file_filter_set_name(filter, "TSV 文件 (*.tsv)");
    gtk_file_filter_add_pattern(filter, "*.tsv");
    gtk_file_chooser_add_filter(GTK_FILE_CHOOSER(chooser), filter);
    gint response = gtk_dialog_run(GTK_DIALOG(chooser));
    gchar *path = NULL;
    if (response == GTK_RESPONSE_ACCEPT) {
        path = gtk_file_chooser_get_filename(GTK_FILE_CHOOSER(chooser));
    }
    gtk_widget_destroy(chooser);
    if (path == NULL) {
        return;
    }
    run_async(ctx->window, work_export, path, g_free, on_export_done, ctx);
}

static SetupResult work_import(gpointer data) {
    GPtrArray *paths = data;
    return setup_import_dicts((gchar **)paths->pdata, paths->len);
}

static void on_import_done(gpointer data, SetupResult result) {
    AppCtx *ctx = data;
    show_message(GTK_WINDOW(ctx->window),
                 result.ok ? GTK_MESSAGE_INFO : GTK_MESSAGE_ERROR,
                 result.message);
    setup_result_free(&result);
    refresh_stats(ctx);
}

static void on_import_clicked(GtkButton *button, gpointer user_data) {
    (void)button;
    AppCtx *ctx = user_data;
    GtkWidget *chooser = gtk_file_chooser_dialog_new("导入词库",
                                                     GTK_WINDOW(ctx->window),
                                                     GTK_FILE_CHOOSER_ACTION_OPEN,
                                                     "取消", GTK_RESPONSE_CANCEL,
                                                     "打开", GTK_RESPONSE_ACCEPT,
                                                     NULL);
    gtk_file_chooser_set_select_multiple(GTK_FILE_CHOOSER(chooser), TRUE);
    const gchar *filters[][2] = {
        { "Rime 词库 (*.yaml)", "*.yaml" },
        { "CC-CEDICT / 文本 (*.txt)", "*.txt" },
        { "TSV (*.tsv)", "*.tsv" },
        { "全部文件", "*" },
    };
    for (gsize i = 0; i < G_N_ELEMENTS(filters); i++) {
        GtkFileFilter *filter = gtk_file_filter_new();
        gtk_file_filter_set_name(filter, filters[i][0]);
        gtk_file_filter_add_pattern(filter, filters[i][1]);
        gtk_file_chooser_add_filter(GTK_FILE_CHOOSER(chooser), filter);
    }
    gint response = gtk_dialog_run(GTK_DIALOG(chooser));
    GSList *files = NULL;
    if (response == GTK_RESPONSE_ACCEPT) {
        files = gtk_file_chooser_get_filenames(GTK_FILE_CHOOSER(chooser));
    }
    gtk_widget_destroy(chooser);
    if (files == NULL) {
        return;
    }
    GPtrArray *paths = g_ptr_array_new_with_free_func(g_free);
    for (GSList *item = files; item != NULL; item = item->next) {
        g_ptr_array_add(paths, item->data);
    }
    g_slist_free(files);
    if (paths->len == 0) {
        g_ptr_array_free(paths, TRUE);
        return;
    }
    run_async(ctx->window, work_import, paths,
              (GDestroyNotify)g_ptr_array_unref, on_import_done, ctx);
}

static gboolean on_associate_toggled(GtkSwitch *sw, gboolean state, gpointer user_data) {
    (void)user_data;
    if (!setup_config_save(state)) {
        show_message(GTK_WINDOW(gtk_widget_get_toplevel(GTK_WIDGET(sw))),
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
        show_message(GTK_WINDOW(gtk_widget_get_toplevel(GTK_WIDGET(btn))),
                     GTK_MESSAGE_ERROR, "保存设置失败，请检查配置目录权限。");
    }
}

static GtkWidget *build_window(AppCtx *ctx) {
    gboolean associate = TRUE;
    (void)setup_config_load(&associate);

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
    GtkWidget *input_page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    GtkWidget *dict_page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    GtkWidget *about_page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);
    gtk_widget_set_margin_start(input_page, 12);
    gtk_widget_set_margin_end(input_page, 12);
    gtk_widget_set_margin_start(dict_page, 12);
    gtk_widget_set_margin_end(dict_page, 12);
    gtk_widget_set_margin_start(about_page, 12);
    gtk_widget_set_margin_end(about_page, 12);
    gtk_box_pack_start(GTK_BOX(stack), input_page, TRUE, TRUE, 0);
    gtk_box_pack_start(GTK_BOX(stack), dict_page, TRUE, TRUE, 0);
    gtk_box_pack_start(GTK_BOX(stack), about_page, TRUE, TRUE, 0);
    /* GTK3 没有 gtk_stack_child_set_title（那是 GTK4 的 gtk_stack_page_set_title）；
     * GTK3 通过 GtkStack 的子属性 "title" 设置导航标题（2026-11-06 修复编译错误）。 */
    gtk_container_child_set(GTK_CONTAINER(stack), input_page, "title", "输入", NULL);
    gtk_container_child_set(GTK_CONTAINER(stack), dict_page, "title", "词库", NULL);
    gtk_container_child_set(GTK_CONTAINER(stack), about_page, "title", "关于", NULL);
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
    GSList *mode_group_radio = NULL;
    const struct {
        const char *label;
        const char *scope;
    } kScopes[] = {
        {"系统共享", "system"},
        {"每程序", "process"},
        {"每窗口", "window"},
    };
    for (gsize i = 0; i < G_N_ELEMENTS(kScopes); i++) {
        GtkWidget *rb = gtk_radio_button_new_with_label_from_widget(
            GTK_RADIO_BUTTON(mode_group_radio), kScopes[i].label);
        if (mode_group_radio == NULL) {
            mode_group_radio = gtk_radio_button_get_group(GTK_RADIO_BUTTON(rb));
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

    GtkWidget *data_group = gtk_label_new(NULL);
    gtk_label_set_markup(GTK_LABEL(data_group), "<b>学习数据</b>");
    gtk_label_set_xalign(GTK_LABEL(data_group), 0.0);
    gtk_box_pack_start(GTK_BOX(dict_page), data_group, FALSE, FALSE, 0);

    ctx->stats_label = gtk_label_new("读取中…");
    gtk_label_set_xalign(GTK_LABEL(ctx->stats_label), 0.0);
    gtk_label_set_line_wrap(GTK_LABEL(ctx->stats_label), TRUE);
    gtk_label_set_max_width_chars(GTK_LABEL(ctx->stats_label), 40);
    gtk_box_pack_start(GTK_BOX(dict_page), ctx->stats_label, FALSE, FALSE, 0);

    GtkWidget *data_row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 12);
    GtkWidget *clear_btn = gtk_button_new_with_label("清空学习数据…");
    GtkWidget *export_btn = gtk_button_new_with_label("导出学习数据…");
    gtk_box_pack_start(GTK_BOX(data_row), clear_btn, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(data_row), export_btn, FALSE, FALSE, 0);
    gtk_box_pack_start(GTK_BOX(dict_page), data_row, FALSE, FALSE, 0);

    GtkWidget *data_hint = gtk_label_new("学习数据仅保存在本机，用于调频与词组联想；清空后不可恢复。");
    gtk_label_set_xalign(GTK_LABEL(data_hint), 0.0);
    gtk_label_set_line_wrap(GTK_LABEL(data_hint), TRUE);
    gtk_label_set_max_width_chars(GTK_LABEL(data_hint), 40);
    gtk_style_context_add_class(gtk_widget_get_style_context(data_hint), "dim-label");
    gtk_box_pack_start(GTK_BOX(dict_page), data_hint, FALSE, FALSE, 0);

    GtkWidget *dict_group = gtk_label_new(NULL);
    gtk_label_set_markup(GTK_LABEL(dict_group), "<b>词库</b>");
    gtk_label_set_xalign(GTK_LABEL(dict_group), 0.0);
    gtk_box_pack_start(GTK_BOX(dict_page), dict_group, FALSE, FALSE, 0);

    GtkWidget *import_btn = gtk_button_new_with_label("导入词库…");
    gtk_box_pack_start(GTK_BOX(dict_page), import_btn, FALSE, FALSE, 0);

    /* 词库导入说明：2026-11-06 阶段2 三端统一句（Windows/macOS 同句）。 */
    GtkWidget *dict_hint = gtk_label_new("支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。");
    gtk_label_set_xalign(GTK_LABEL(dict_hint), 0.0);
    gtk_label_set_line_wrap(GTK_LABEL(dict_hint), TRUE);
    gtk_label_set_max_width_chars(GTK_LABEL(dict_hint), 40);
    gtk_style_context_add_class(gtk_widget_get_style_context(dict_hint), "dim-label");
    gtk_box_pack_start(GTK_BOX(dict_page), dict_hint, FALSE, FALSE, 0);

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

    g_signal_connect(sw, "state-set", G_CALLBACK(on_associate_toggled), ctx);
    g_signal_connect(clear_btn, "clicked", G_CALLBACK(on_clear_clicked), ctx);
    g_signal_connect(export_btn, "clicked", G_CALLBACK(on_export_clicked), ctx);
    g_signal_connect(import_btn, "clicked", G_CALLBACK(on_import_clicked), ctx);

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
    refresh_stats(ctx);
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