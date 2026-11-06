#define _POSIX_C_SOURCE 200809L

#include "setup_ctl.h"

#include <dirent.h>
#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include <glib/gstdio.h>

static SetupResult result_ok(gchar *message) {
    SetupResult r = { TRUE, message ? message : g_strdup("完成") };
    return r;
}

static SetupResult result_error(const gchar *message) {
    SetupResult r = { FALSE, g_strdup(message ? message : "操作失败") };
    return r;
}

static SetupResult result_error_take(gchar *message) {
    SetupResult r = { FALSE, message ? message : g_strdup("操作失败") };
    return r;
}

void setup_result_free(SetupResult *result) {
    if (result == NULL) {
        return;
    }
    g_free(result->message);
    result->message = NULL;
}

// find_tool 先查 PATH（安装后 /usr/bin），再查源码树 bin/（CI/开发环境）。
static gchar *find_tool(const gchar *name) {
    gchar *exe = g_find_program_in_path(name);
    if (exe != NULL) {
        return exe;
    }
    gchar *cwd = g_get_current_dir();
    gchar *cand = g_build_filename(cwd, "bin", name, NULL);
    g_free(cwd);
    if (g_file_test(cand, G_FILE_TEST_IS_EXECUTABLE)) {
        return cand;
    }
    g_free(cand);
    return NULL;
}

// run_tool 同步执行子进程；out/err 可为 NULL。成功 = 正常退出且退出码 0。
static gboolean run_tool(gchar **argv, gchar **out, gchar **err, GError **error) {
    gint status = 0;
    gchar *stdout_text = NULL;
    gchar *stderr_text = NULL;
    if (!g_spawn_sync(NULL, argv, NULL, G_SPAWN_SEARCH_PATH, NULL, NULL,
                      &stdout_text, &stderr_text, &status, error)) {
        g_free(stdout_text);
        g_free(stderr_text);
        return FALSE;
    }
    if (out != NULL) {
        *out = stdout_text;
    } else {
        g_free(stdout_text);
    }
    if (err != NULL) {
        *err = stderr_text;
    } else {
        g_free(stderr_text);
    }
    return WIFEXITED(status) && WEXITSTATUS(status) == 0;
}

static gchar *trimmed(gchar *text) {
    if (text == NULL) {
        return g_strdup("");
    }
    g_strstrip(text);
    return text;
}

// json_get_string 从 -print-paths 的扁平 JSON 中取字符串字段。输出由本项目
// Go 工具生成（值里不含转义控制字符），这里做最小解析，避免引入 JSON 库。
static gchar *json_get_string(const gchar *json, const gchar *key) {
    if (json == NULL || key == NULL) {
        return NULL;
    }
    gchar *needle = g_strdup_printf("\"%s\"", key);
    const gchar *p = strstr(json, needle);
    g_free(needle);
    if (p == NULL) {
        return NULL;
    }
    p = strchr(p, ':');
    if (p == NULL) {
        return NULL;
    }
    p++;
    while (*p == ' ' || *p == '\t') {
        p++;
    }
    if (*p != '"') {
        return NULL;
    }
    p++;
    const gchar *start = p;
    while (*p != '\0' && *p != '"') {
        if (*p == '\\' && p[1] != '\0') {
            p++;
        }
        p++;
    }
    return g_strndup(start, (gsize)(p - start));
}

static gchar *xdg_data_dir(void) {
    const gchar *base = g_getenv("XDG_DATA_HOME");
    if (base != NULL && *base != '\0') {
        return g_build_filename(base, "chaoset", NULL);
    }
    return g_build_filename(g_get_home_dir(), ".local", "share", "chaoset", NULL);
}

static gchar *xdg_config_dir(void) {
    const gchar *base = g_getenv("XDG_CONFIG_HOME");
    if (base != NULL && *base != '\0') {
        return g_build_filename(base, "chaoset", NULL);
    }
    return g_build_filename(g_get_home_dir(), ".config", "chaoset", NULL);
}

// tool_path 调 Go 工具拿路径；工具缺失时回退 XDG（源码树未构建时设置窗仍可用）。
static gchar *tool_path(const gchar *key, gchar *(*fallback)(void)) {
    gchar *tool = find_tool("chaoset-userdict");
    if (tool != NULL) {
        gchar *argv[] = { tool, "-print-paths", NULL };
        gchar *out = NULL;
        GError *error = NULL;
        if (run_tool(argv, &out, NULL, &error)) {
            gchar *value = json_get_string(out, key);
            g_free(out);
            g_free(tool);
            if (value != NULL && *value != '\0') {
                return value;
            }
            g_free(value);
        } else {
            g_clear_error(&error);
        }
        g_free(out);
        g_free(tool);
    }
    return fallback();
}

static gchar *fallback_userdict_path(void) {
    gchar *dir = xdg_data_dir();
    gchar *path = g_build_filename(dir, "userdict.json", NULL);
    g_free(dir);
    return path;
}

static gchar *fallback_imported_dict_path(void) {
    gchar *dir = xdg_data_dir();
    gchar *path = g_build_filename(dir, "dict.tsv", NULL);
    g_free(dir);
    return path;
}

static gchar *fallback_config_path(void) {
    gchar *dir = xdg_config_dir();
    gchar *path = g_build_filename(dir, "config.json", NULL);
    g_free(dir);
    return path;
}

static gchar *config_path(void) {
    return tool_path("config", fallback_config_path);
}

static gchar *userdict_path(void) {
    return tool_path("userdict", fallback_userdict_path);
}

static gchar *imported_dict_path(void) {
    return tool_path("imported_dict", fallback_imported_dict_path);
}

// read_text 读文件；不存在返回 NULL 且 *missing=TRUE，其它错误设置 error。
static gchar *read_text(const gchar *path, gboolean *missing, GError **error) {
    gchar *data = NULL;
    gsize len = 0;
    if (missing != NULL) {
        *missing = FALSE;
    }
    if (!g_file_get_contents(path, &data, &len, error)) {
        if (error != NULL && *error != NULL && (*error)->code == G_FILE_ERROR_NOENT) {
            g_clear_error(error);
            if (missing != NULL) {
                *missing = TRUE;
            }
            return NULL;
        }
        return NULL;
    }
    return data;
}

gboolean setup_config_load(gboolean *associate) {
    if (associate == NULL) {
        return FALSE;
    }
    *associate = TRUE;
    gchar *path = config_path();
    GError *error = NULL;
    gchar *text = read_text(path, NULL, &error);
    g_free(path);
    if (error != NULL) {
        g_clear_error(&error);
        return FALSE;
    }
    if (text == NULL) {
        return TRUE;
    }
    const gchar *p = strstr(text, "\"associate\"");
    if (p != NULL) {
        p = strchr(p, ':');
        if (p != NULL) {
            p++;
            while (*p == ' ' || *p == '\t' || *p == '\n' || *p == '\r') {
                p++;
            }
            *associate = g_str_has_prefix(p, "true");
        }
    }
    g_free(text);
    return TRUE;
}

/* 中英状态作用域：读 JSON 里的 "mode_scope" 字符串；缺省 system。
 * 与 Windows HKCU\Software\Chaoset\ModeScope / macOS UserDefaults 同语义。 */
const char *setup_config_load_mode_scope(void) {
    static char cached[16] = "system";
    gchar *path = config_path();
    GError *error = NULL;
    gchar *text = read_text(path, NULL, &error);
    g_free(path);
    if (error != NULL) {
        g_clear_error(&error);
        return cached;
    }
    if (text == NULL) {
        return cached;
    }
    const gchar *p = strstr(text, "\"mode_scope\"");
    if (p != NULL) {
        const gchar *colon = strchr(p, ':');
        if (colon != NULL) {
            const gchar *q = strchr(colon, '"');
            if (q != NULL) {
                q++;
                const gchar *end = strchr(q, '"');
                if (end != NULL && (gsize)(end - q) < sizeof(cached)) {
                    g_strlcpy(cached, q, (gsize)(end - q) + 1);
                }
            }
        }
    }
    g_free(text);
    return cached;
}

/* 写作用域：重新拼一份最小 JSON（只保留 associate + mode_scope）。
 * 配置文件是本项目独占的简单键值 JSON，重建比原地编辑更不容易出错，
 * 也避免上面的字符串手术在损坏文件上产生非法 JSON。 */
gboolean setup_config_save_mode_scope(const char *scope) {
    if (scope == NULL || *scope == '\0') {
        return FALSE;
    }
    gboolean associate = TRUE;
    (void)setup_config_load(&associate);
    gchar *path = config_path();
    gchar *dir = g_path_get_dirname(path);
    if (g_mkdir_with_parents(dir, 0700) != 0) {
        g_free(dir);
        g_free(path);
        return FALSE;
    }
    g_free(dir);
    gchar *json = g_strdup_printf(
        "{\n  \"associate\": %s,\n  \"mode_scope\": \"%s\"\n}\n",
        associate ? "true" : "false", scope);
    GError *error = NULL;
    gboolean ok = g_file_set_contents(path, json, -1, &error);
    g_free(json);
    if (ok) {
        (void)g_chmod(path, 0644);
    } else {
        g_clear_error(&error);
    }
    g_free(path);
    return ok;
}


static gchar *replace_associate_value(const gchar *text, gboolean associate) {
    const gchar *needle = "\"associate\"";
    const gchar *p = strstr(text, needle);
    const gchar *value = associate ? "true" : "false";
    if (p == NULL) {
        // 没有该键：插到最后一个 '}' 前；空/损坏文件直接写最小 JSON。
        const gchar *close = strrchr(text, '}');
        if (close == NULL) {
            return g_strdup_printf("{\n  \"associate\": %s\n}\n", value);
        }
        gchar *prefix = g_strndup(text, (gsize)(close - text));
        gchar *trimmed_prefix = g_strdup(prefix);
        g_strchomp(trimmed_prefix);
        gboolean has_content = trimmed_prefix[0] != '\0' &&
                               strchr(trimmed_prefix, ':') != NULL;
        gchar *result = g_strdup_printf("%s%s\"associate\": %s\n}%s",
                                        prefix,
                                        has_content ? ",\n  " : "\n  ",
                                        value, close + 1);
        g_free(trimmed_prefix);
        g_free(prefix);
        return result;
    }
    p = strchr(p, ':');
    if (p == NULL) {
        return g_strdup(text);
    }
    p++;
    while (*p == ' ' || *p == '\t' || *p == '\n' || *p == '\r') {
        p++;
    }
    const gchar *token_end = p;
    while (*token_end != '\0' && *token_end != ',' && *token_end != '}' &&
           *token_end != '\n' && *token_end != '\r' && *token_end != ' ' &&
           *token_end != '\t') {
        token_end++;
    }
    gchar *result = g_strdup_printf("%.*s%s%s",
                                    (gint)(p - text), text, value, token_end);
    return result;
}

gboolean setup_config_save(gboolean associate) {
    gchar *path = config_path();
    gchar *dir = g_path_get_dirname(path);
    if (g_mkdir_with_parents(dir, 0700) != 0) {
        g_free(dir);
        g_free(path);
        return FALSE;
    }
    g_free(dir);

    GError *error = NULL;
    gboolean missing = FALSE;
    gchar *old = read_text(path, &missing, &error);
    if (error != NULL) {
        g_clear_error(&error);
        g_free(path);
        return FALSE;
    }
    gchar *updated = replace_associate_value(old != NULL ? old : "{}", associate);
    g_free(old);
    gboolean ok = g_file_set_contents(path, updated, -1, &error);
    g_free(updated);
    if (ok) {
        (void)g_chmod(path, 0644);
    } else {
        g_clear_error(&error);
    }
    g_free(path);
    return ok;
}

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

// process_pids 扫描 /proc 找指定 comm 的进程（不依赖 pgrep）。
static GArray *process_pids(const gchar *name) {
    GArray *pids = g_array_new(FALSE, FALSE, sizeof(pid_t));
    DIR *proc = opendir("/proc");
    if (proc == NULL) {
        return pids;
    }
    struct dirent *entry;
    while ((entry = readdir(proc)) != NULL) {
        gboolean numeric = TRUE;
        for (const gchar *p = entry->d_name; *p != '\0'; p++) {
            if (!g_ascii_isdigit(*p)) {
                numeric = FALSE;
                break;
            }
        }
        if (!numeric) {
            continue;
        }
        gchar *comm_path = g_strdup_printf("/proc/%s/comm", entry->d_name);
        gchar *comm = NULL;
        if (g_file_get_contents(comm_path, &comm, NULL, NULL)) {
            g_strstrip(comm);
            if (g_strcmp0(comm, name) == 0) {
                pid_t pid = (pid_t)g_ascii_strtoll(entry->d_name, NULL, 10);
                g_array_append_val(pids, pid);
            }
            g_free(comm);
        }
        g_free(comm_path);
    }
    closedir(proc);
    return pids;
}

static GArray *engine_pids(void) { return process_pids("chaoset-engine"); }
static GArray *daemon_pids(void) { return process_pids("chaosetd"); }

static gboolean wait_engines_gone(gint timeout_ms) {
    gint64 deadline = g_get_monotonic_time() + (gint64)timeout_ms * 1000;
    for (;;) {
        GArray *pids = engine_pids();
        gboolean gone = pids->len == 0;
        g_array_free(pids, TRUE);
        if (gone) {
            return TRUE;
        }
        if (g_get_monotonic_time() >= deadline) {
            return FALSE;
        }
        g_usleep(100 * 1000);
    }
}

static gboolean terminate_engines(GArray *pids, gint timeout_ms) {
    for (guint i = 0; i < pids->len; i++) {
        pid_t pid = g_array_index(pids, pid_t, i);
        if (kill(pid, SIGTERM) != 0 && errno != ESRCH) {
            return FALSE;
        }
    }
    if (wait_engines_gone(timeout_ms)) {
        return TRUE;
    }
    GArray *left = engine_pids();
    for (guint i = 0; i < left->len; i++) {
        pid_t pid = g_array_index(left, pid_t, i);
        if (kill(pid, SIGKILL) != 0 && errno != ESRCH) {
            g_array_free(left, TRUE);
            return FALSE;
        }
    }
    g_array_free(left, TRUE);
    return wait_engines_gone(3000);
}

static gboolean wait_daemon_gone(gint timeout_ms) {
    gint64 deadline = g_get_monotonic_time() + (gint64)timeout_ms * 1000;
    for (;;) {
        GArray *pids = daemon_pids();
        gboolean gone = pids->len == 0;
        g_array_free(pids, TRUE);
        if (gone) {
            return TRUE;
        }
        if (g_get_monotonic_time() >= deadline) {
            return FALSE;
        }
        g_usleep(100 * 1000);
    }
}

static gboolean terminate_daemon(void) {
    GArray *pids = daemon_pids();
    for (guint i = 0; i < pids->len; i++) {
        pid_t pid = g_array_index(pids, pid_t, i);
        if (kill(pid, SIGTERM) != 0 && errno != ESRCH) {
            g_array_free(pids, TRUE);
            return FALSE;
        }
    }
    g_array_free(pids, TRUE);
    if (wait_daemon_gone(5000)) {
        return TRUE;
    }
    GArray *left = daemon_pids();
    for (guint i = 0; i < left->len; i++) {
        pid_t pid = g_array_index(left, pid_t, i);
        (void)kill(pid, SIGKILL);
    }
    g_array_free(left, TRUE);
    return wait_daemon_gone(3000);
}

static gboolean run_ibus(const gchar *subcommand) {
    gchar *ibus = find_tool("ibus");
    if (ibus == NULL) {
        return FALSE;
    }
    gchar *argv[] = { ibus, (gchar *)subcommand, NULL };
    GError *error = NULL;
    gboolean ok = run_tool(argv, NULL, NULL, &error);
    g_clear_error(&error);
    g_free(ibus);
    return ok;
}

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
