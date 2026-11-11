#define _POSIX_C_SOURCE 200809L

// Linux 设置窗 C 侧核心：路径解析、Go 工具执行、config 读写与输入法进程
// 生命周期（2026-11-06 按职责拆分，模块地图见 docs/architecture.md §3.3）。
//
// 只调用 Go 工具（chaoset-userdict / chaoset-import）与 ibus，不实现词库
// 合并/用户词典读写；学习数据流程在 setup_learn.c，词库导入流程在
// setup_import.c。拆分后跨 TU 共享的内部声明见 setup_internal.h。

#include "setup_ctl.h"
#include "setup_internal.h"

#include <dirent.h>
#include <errno.h>
#include <signal.h>
#include <string.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include <glib/gstdio.h>

SetupResult result_ok(gchar *message) {
    SetupResult r = { TRUE, message ? message : g_strdup("完成") };
    return r;
}

SetupResult result_error(const gchar *message) {
    SetupResult r = { FALSE, g_strdup(message ? message : "操作失败") };
    return r;
}

SetupResult result_error_take(gchar *message) {
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
gchar *find_tool(const gchar *name) {
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
gboolean run_tool(gchar **argv, gchar **out, gchar **err, GError **error) {
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

gchar *trimmed(gchar *text) {
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

gchar *userdict_path(void) {
    return tool_path("userdict", fallback_userdict_path);
}

gchar *imported_dict_path(void) {
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

/* replace_json_value 在简单键值 JSON 里原地替换 "key": <旧 token> 为 raw_value
 * （raw_value 由调用方拼好，含引号的字符串值也支持）。只改这一个键、保留其它键：
 * fuzzy/traditional/shuangpin 等键由 Go 工具（chaoset-settingsctl）写入，C 设置窗
 * 只做冷路径的单项更新，**不得**重建最小 JSON 把其它键清掉（2026-11-08 审计
 * GO-03：切一次中英作用域会丢用户已保存的设置）。
 * key 不存在时插到最后一个 '}' 前；空/损坏文件（无 '}'）写最小 JSON。 */
static gchar *replace_json_value(const gchar *text, const char *key,
                                 const char *raw_value) {
    gchar *needle = g_strdup_printf("\"%s\"", key);
    const gchar *p = strstr(text, needle);
    if (p == NULL) {
        const gchar *close = strrchr(text, '}');
        if (close == NULL) {
            gchar *result = g_strdup_printf("{\n  \"%s\": %s\n}\n", key, raw_value);
            g_free(needle);
            return result;
        }
        gchar *prefix = g_strndup(text, (gsize)(close - text));
        gchar *trimmed_prefix = g_strdup(prefix);
        g_strchomp(trimmed_prefix);
        gboolean has_content = trimmed_prefix[0] != '\0' &&
                               strchr(trimmed_prefix, ':') != NULL;
        gchar *result = g_strdup_printf("%s%s\"%s\": %s\n}%s",
                                        prefix,
                                        has_content ? ",\n  " : "\n  ",
                                        key, raw_value, close + 1);
        g_free(trimmed_prefix);
        g_free(prefix);
        g_free(needle);
        return result;
    }
    p += strlen(needle);
    p = strchr(p, ':');
    if (p == NULL) {
        g_free(needle);
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
                                    (gint)(p - text), text, raw_value, token_end);
    g_free(needle);
    return result;
}

/* 作用域合法值（新写入契约）：只允许 system|process，与 Go internal/settings /
 * Windows 注册表 / macOS UserDefaults 同口径。旧三档值只在读取/CLI 解析时归一化。 */
static gboolean valid_mode_scope(const char *scope) {
    return scope != NULL &&
           (g_strcmp0(scope, "system") == 0 ||
            g_strcmp0(scope, "process") == 0);
}

/* 存储读取的宽容归一化（三端一致）：system/process 保持原值；旧三档
 * window→process（「每窗口」并入「每进程」）；未知/空→system。
 * 大小写与首尾空白容忍（与 Go parseStoredModeScope 同语义）。 */
static const char *normalized_mode_scope(const char *raw) {
    if (raw == NULL) {
        return "system";
    }
    char lower[16];
    g_strlcpy(lower, raw, sizeof(lower));
    g_strstrip(lower);
    if (g_ascii_strcasecmp(lower, "process") == 0 ||
        g_ascii_strcasecmp(lower, "window") == 0) {
        return "process";
    }
    return "system";
}

/* 中英状态作用域：读 JSON 里的 "mode_scope" 字符串；缺省 system。
 * 旧三档值 window 读取时归一化为 process；未知/损坏值→system。
 * 与 Windows HKCU\Software\Chaoset\ModeScope / macOS UserDefaults 同语义。 */
const char *setup_config_load_mode_scope(void) {
    static char cached[16] = "system";
    gchar *path = config_path();
    GError *error = NULL;
    gchar *text = read_text(path, NULL, &error);
    g_free(path);
    if (error != NULL) {
        g_clear_error(&error);
        g_strlcpy(cached, "system", sizeof(cached)); // 读取失败：回到缺省，不沿用旧缓存
        return cached;
    }
    if (text == NULL) {
        g_strlcpy(cached, "system", sizeof(cached)); // 文件不存在 = 未配置
        return cached;
    }
    // 未知/损坏值一律回退缺省 system（与 Go store_linux 的宽容归一化一致）：
    // 先把缓存置缺省，只在解析出取值时按两档归一化覆盖，避免旧值在文件被改坏后继续生效。
    g_strlcpy(cached, "system", sizeof(cached));
    const gchar *p = strstr(text, "\"mode_scope\"");
    if (p != NULL) {
        const gchar *colon = strchr(p, ':');
        if (colon != NULL) {
            const gchar *q = strchr(colon, '"');
            if (q != NULL) {
                q++;
                const gchar *end = strchr(q, '"');
                if (end != NULL && (gsize)(end - q) < sizeof(cached)) {
                    char tmp[16];
                    g_strlcpy(tmp, q, (gsize)(end - q) + 1);
                    g_strlcpy(cached, normalized_mode_scope(tmp), sizeof(cached));
                }
            }
        }
    }
    g_free(text);
    return cached;
}

/* 写作用域：只接受新值 system|process（旧三档值由读取/CLI 归一化，不写入）。
 * 读-改-写，只替换 mode_scope 一个键、保留文件里其它键。
 * 不能重建最小 JSON：fuzzy/traditional/shuangpin 等键由 Go 工具写入，
 * 重建会把用户已保存的设置静默清掉（2026-11-08 审计 GO-03）。 */
gboolean setup_config_save_mode_scope(const char *scope) {
    if (!valid_mode_scope(scope)) {
        return FALSE;
    }
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
    gchar *quoted = g_strdup_printf("\"%s\"", scope);
    gchar *updated = replace_json_value(old != NULL ? old : "{}", "mode_scope", quoted);
    g_free(quoted);
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


// 联想开关的单项写入复用通用 JSON 编辑（保留 fuzzy/traditional/shuangpin 等键）。
static gchar *replace_associate_value(const gchar *text, gboolean associate) {
    return replace_json_value(text, "associate", associate ? "true" : "false");
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

GArray *engine_pids(void) { return process_pids("chaoset-engine"); }
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

gboolean terminate_engines(GArray *pids, gint timeout_ms) {
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

gboolean terminate_daemon(void) {
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

gboolean run_ibus(const gchar *subcommand) {
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
