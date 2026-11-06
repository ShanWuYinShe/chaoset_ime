// Linux 设置窗「词库」页：学习数据统计/清空/导出与词库导入入口
// （2026-11-06 按页面拆分）。页面构建函数返回 GtkBox，控件回调留在本文件。
//
// 耗时操作（子进程/ibus 重启/文件 IO）经 run_async 放后台线程，完成后回主
// 线程恢复窗口并弹结果；GTK 非线程安全，工作线程只跑子进程/文件 IO，绝不碰
// 控件。统计刷新由窗口骨架在 show_all 后调用（保持拆分前时序）。

#include <gtk/gtk.h>

#include "setup_ctl.h"
#include "setup_pages.h"

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

void setup_page_dict_refresh(AppCtx *ctx) {
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
    setup_show_message(GTK_WINDOW(ctx->window),
                       result.ok ? GTK_MESSAGE_INFO : GTK_MESSAGE_ERROR,
                       result.message);
    setup_result_free(&result);
    setup_page_dict_refresh(ctx);
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
    setup_show_message(GTK_WINDOW(ctx->window),
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
    setup_show_message(GTK_WINDOW(ctx->window),
                       result.ok ? GTK_MESSAGE_INFO : GTK_MESSAGE_ERROR,
                       result.message);
    setup_result_free(&result);
    setup_page_dict_refresh(ctx);
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

GtkWidget *setup_page_dict_build(AppCtx *ctx) {
    GtkWidget *dict_page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 12);

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

    g_signal_connect(clear_btn, "clicked", G_CALLBACK(on_clear_clicked), ctx);
    g_signal_connect(export_btn, "clicked", G_CALLBACK(on_export_clicked), ctx);
    g_signal_connect(import_btn, "clicked", G_CALLBACK(on_import_clicked), ctx);
    return dict_page;
}
