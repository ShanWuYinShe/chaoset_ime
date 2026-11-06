#define _POSIX_C_SOURCE 200809L

// Linux 设置窗学习数据流程：统计 / 导出 / 清空（2026-11-06 按职责拆分）。
//
// 全部操作委托 Go 工具 chaoset-userdict（-userdict 指向用户词典路径）；
// 清空按「停 daemon → 停引擎 → 清空 → 恢复 ibus」顺序编排，防止 daemon
// 内存里的旧状态回写。C 侧不读写词库/用户词典内容。

#include "setup_ctl.h"
#include "setup_internal.h"

static SetupResult run_userdict(gchar *const extra[], const gchar *fallback_message) {
    gchar *tool = find_tool("chaoset-userdict");
    if (tool == NULL) {
        return result_error("未找到 chaoset-userdict（请重新安装）");
    }
    gchar *path = userdict_path();
    gchar **argv = g_new0(gchar *, 8);
    gsize i = 0;
    argv[i++] = tool;
    argv[i++] = "-userdict";
    argv[i++] = path;
    for (gsize j = 0; extra != NULL && extra[j] != NULL; j++) {
        argv[i++] = extra[j];
    }
    gchar *out = NULL;
    gchar *err = NULL;
    GError *error = NULL;
    gboolean ok = run_tool(argv, &out, &err, &error);
    g_free(argv);
    g_free(path);
    g_free(tool);
    SetupResult result;
    if (!ok) {
        gchar *detail = (err != NULL && *err != '\0') ? g_strdup(err) :
                        (error != NULL ? g_strdup(error->message) : g_strdup(fallback_message));
        if (out != NULL && *out != '\0' && (err == NULL || *err == '\0')) {
            g_free(detail);
            detail = g_strdup(out);
        }
        result = result_error_take(g_strstrip(detail));
    } else {
        gchar *detail = trimmed(out != NULL ? g_strdup(out) : g_strdup(""));
        result = result_ok(*detail != '\0' ? detail : g_strdup(fallback_message));
        if (*detail == '\0') {
            g_free(detail);
        }
    }
    g_clear_error(&error);
    g_free(out);
    g_free(err);
    return result;
}

SetupResult setup_userdict_stats(void) {
    gchar *extra[] = { "stats", NULL };
    return run_userdict(extra, "读取学习数据失败");
}

SetupResult setup_userdict_export(const gchar *out_path) {
    if (out_path == NULL || *out_path == '\0') {
        return result_error("未选择导出路径");
    }
    gchar *extra[] = { "export", "-o", (gchar *)out_path, NULL };
    SetupResult result = run_userdict(extra, "导出失败");
    if (result.ok) {
        g_free(result.message);
        result.message = g_strdup_printf("已导出到 %s", out_path);
    }
    return result;
}

SetupResult setup_userdict_clear(void) {
    if (!terminate_daemon()) {
        return result_error("无法停止 chaosetd，未清空学习数据");
    }
    GArray *pids = engine_pids();
    gboolean had_engines = pids->len > 0;
    gchar *ibus = find_tool("ibus");
    if (had_engines && ibus == NULL) {
        g_array_free(pids, TRUE);
        return result_error("输入法引擎正在运行，但找不到 ibus 命令，未清空学习数据");
    }
    g_free(ibus);
    if (had_engines) {
        (void)run_ibus("exit");
        if (!terminate_engines(pids, 8000)) {
            g_array_free(pids, TRUE);
            return result_error("无法停止输入法引擎，未清空学习数据。请注销后重新登录再试");
        }
    }
    g_array_free(pids, TRUE);

    gchar *extra[] = { "clear", NULL };
    SetupResult result = run_userdict(extra, "已清空学习数据");
    if (result.ok) {
        g_free(result.message);
        result.message = g_strdup("已清空学习数据");
    }
    if (had_engines && find_tool("ibus") != NULL) {
        gboolean restored = run_ibus("restart") || run_ibus("start");
        if (!restored && result.ok) {
            g_free(result.message);
            result.message = g_strdup("学习数据已清空，但 ibus 未能自动重启；请运行：ibus restart");
        }
    }
    return result;
}
