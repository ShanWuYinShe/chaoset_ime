// Linux 设置窗词库导入流程（2026-11-06 按职责拆分）。
//
// 安全流程与 macOS/Windows 一致：只调 Go 工具 chaoset-import（-o 目标 +
// -backup），合并/备份/原子替换全部由 Go 侧负责；导入成功后重启 ibus 让新
// 词库生效。

#include "setup_ctl.h"
#include "setup_internal.h"

static SetupResult restart_engines(void) {
    // daemon 内存里有词库/用户词典；不先停掉，重启引擎后连上的还是旧 daemon，
    // 导入/清空不会生效。
    if (!terminate_daemon()) {
        return result_error("无法停止 chaosetd，未能重启输入法");
    }
    GArray *pids = engine_pids();
    gboolean has_engines = pids->len > 0;
    gchar *ibus = find_tool("ibus");
    if (has_engines && ibus == NULL) {
        g_array_free(pids, TRUE);
        return result_error("输入法引擎正在运行，但找不到 ibus 命令，未能重启");
    }
    g_free(ibus);
    if (has_engines) {
        (void)run_ibus("exit");
        if (!terminate_engines(pids, 8000)) {
            g_array_free(pids, TRUE);
            return result_error("无法停止输入法引擎，未能重启");
        }
    }
    g_array_free(pids, TRUE);
    if (find_tool("ibus") == NULL) {
        return result_ok(g_strdup("ibus 未运行（下次启动输入法时生效）"));
    }
    if (run_ibus("restart") || run_ibus("start")) {
        return result_ok(g_strdup("ibus 已重启，新词库已生效"));
    }
    return result_error("ibus 未能自动重启；请运行：ibus restart");
}

SetupResult setup_import_dicts(gchar **paths, gsize n_paths) {
    if (paths == NULL || n_paths == 0) {
        return result_error("未选择词库文件");
    }
    gchar *tool = find_tool("chaoset-import");
    if (tool == NULL) {
        return result_error("未找到 chaoset-import（请重新安装）");
    }
    gchar *target = imported_dict_path();
    gchar **argv = g_new0(gchar *, n_paths + 6);
    gsize i = 0;
    argv[i++] = tool;
    argv[i++] = "-o";
    argv[i++] = target;
    argv[i++] = "-backup";
    for (gsize j = 0; j < n_paths; j++) {
        argv[i++] = paths[j];
    }
    gchar *out = NULL;
    gchar *err = NULL;
    GError *error = NULL;
    gboolean ok = run_tool(argv, &out, &err, &error);
    g_free(argv);
    g_free(tool);
    SetupResult result;
    if (!ok) {
        gchar *detail = (err != NULL && *err != '\0') ? g_strdup(err) :
                        (error != NULL ? g_strdup(error->message) : g_strdup("导入失败"));
        result = result_error_take(g_strstrip(detail));
    } else if (!g_file_test(target, G_FILE_TEST_IS_REGULAR)) {
        result = result_error("导入失败：未生成词库文件");
    } else {
        gchar *detail = trimmed(out != NULL ? g_strdup(out) : g_strdup(""));
        SetupResult restart = restart_engines();
        if (!restart.ok) {
            gchar *message = g_strdup_printf("词库已导入，但 %s", restart.message);
            result = result_error_take(message);
        } else {
            gchar *message = g_strdup_printf("已导入到 %s%s；%s",
                                             target,
                                             *detail != '\0' ? "；" : "",
                                             restart.message);
            if (*detail != '\0') {
                gchar *with_detail = g_strdup_printf("已导入到 %s；%s；%s",
                                                     target, detail, restart.message);
                g_free(message);
                message = with_detail;
            }
            result = result_ok(message);
        }
        setup_result_free(&restart);
        g_free(detail);
    }
    g_clear_error(&error);
    g_free(out);
    g_free(err);
    g_free(target);
    return result;
}
