// setup_ctl 的无 GTK 单测：配置读写/保留未知键、路径回退。
// CI 在原生 GTK3 编译后用 pkg-config glib-2.0 单独编译运行。

#include <glib.h>
#include <glib/gstdio.h>

#include "setup_ctl.h"

static void test_config_roundtrip(void) {
    gchar *dir = g_dir_make_tmp("chaoset-setup-test-XXXXXX", NULL);
    g_assert_nonnull(dir);
    g_setenv("XDG_CONFIG_HOME", dir, TRUE);
    g_setenv("PATH", "/nonexistent", TRUE);

    gboolean associate = FALSE;
    g_assert_true(setup_config_load(&associate));
    g_assert_true(associate);
    g_assert_true(setup_config_save(FALSE));
    g_assert_true(setup_config_load(&associate));
    g_assert_false(associate);

    // 保存 associate 不能丢掉其它设置键（fuzzy/traditional/shuangpin）。
    gchar *path = g_build_filename(dir, "chaoset", "config.json", NULL);
    gchar *dir_path = g_path_get_dirname(path);
    g_mkdir_with_parents(dir_path, 0700);
    g_assert_true(g_file_set_contents(path,
        "{\n  \"fuzzy\": true,\n  \"associate\": true\n}\n", -1, NULL));
    g_assert_true(setup_config_save(FALSE));
    gchar *text = NULL;
    g_assert_true(g_file_get_contents(path, &text, NULL, NULL));
    g_assert_nonnull(g_strstr_len(text, -1, "\"fuzzy\": true"));
    g_assert_nonnull(g_strstr_len(text, -1, "\"associate\": false"));
    g_free(text);
    g_free(dir_path);
    g_free(path);
    g_free(dir);
}

static void test_config_missing_defaults_on(void) {
    gchar *dir = g_dir_make_tmp("chaoset-setup-test-XXXXXX", NULL);
    g_assert_nonnull(dir);
    g_setenv("XDG_CONFIG_HOME", dir, TRUE);
    g_setenv("PATH", "/nonexistent", TRUE);
    gboolean associate = FALSE;
    g_assert_true(setup_config_load(&associate));
    g_assert_true(associate);
    g_free(dir);
}


// 切一次「中英状态作用域」必须保留 Go 侧（chaoset-settingsctl）写入的其它键，
// 不能把 fuzzy/traditional/shuangpin 清掉（2026-11-08 审计 GO-03）。
static void test_config_mode_scope_preserves_unknown_keys(void) {
    gchar *dir = g_dir_make_tmp("chaoset-setup-test-XXXXXX", NULL);
    g_assert_nonnull(dir);
    g_setenv("XDG_CONFIG_HOME", dir, TRUE);
    g_setenv("PATH", "/nonexistent", TRUE);

    gchar *path = g_build_filename(dir, "chaoset", "config.json", NULL);
    gchar *dir_path = g_path_get_dirname(path);
    g_mkdir_with_parents(dir_path, 0700);
    g_assert_true(g_file_set_contents(path,
        "{\n  \"fuzzy\": true,\n  \"traditional\": false,\n"
        "  \"shuangpin\": \"zrm\",\n  \"mode_scope\": \"system\"\n}\n",
        -1, NULL));

    g_assert_true(setup_config_save_mode_scope("process"));
    gchar *text = NULL;
    g_assert_true(g_file_get_contents(path, &text, NULL, NULL));
    g_assert_nonnull(g_strstr_len(text, -1, "\"fuzzy\": true"));
    g_assert_nonnull(g_strstr_len(text, -1, "\"traditional\": false"));
    g_assert_nonnull(g_strstr_len(text, -1, "\"shuangpin\": \"zrm\""));
    g_assert_nonnull(g_strstr_len(text, -1, "\"mode_scope\": \"process\""));
    g_free(text);
    g_assert_cmpstr(setup_config_load_mode_scope(), ==, "process");

    // 非法/旧三档值：新写入只接受 system|process，旧值 window 只允许读取归一化；
    // 拒绝写入且不改动文件。
    g_assert_false(setup_config_save_mode_scope("window"));
    g_assert_false(setup_config_save_mode_scope("Bogus"));
    g_assert_false(setup_config_save_mode_scope(NULL));
    g_assert_false(setup_config_save_mode_scope(""));
    g_assert_true(g_file_get_contents(path, &text, NULL, NULL));
    g_assert_nonnull(g_strstr_len(text, -1, "\"mode_scope\": \"process\""));
    g_free(text);

    g_free(dir_path);
    g_free(path);
    g_free(dir);
}

// 配置文件不存在 = 缺省 system；写入会创建文件；文件里出现未知值时回退 system。
static void test_config_mode_scope_defaults_create_and_corrupt(void) {
    gchar *dir = g_dir_make_tmp("chaoset-setup-test-XXXXXX", NULL);
    g_assert_nonnull(dir);
    g_setenv("XDG_CONFIG_HOME", dir, TRUE);
    g_setenv("PATH", "/nonexistent", TRUE);

    g_assert_cmpstr(setup_config_load_mode_scope(), ==, "system");
    g_assert_true(setup_config_save_mode_scope("process"));

    gchar *path = g_build_filename(dir, "chaoset", "config.json", NULL);
    g_assert_true(g_file_test(path, G_FILE_TEST_EXISTS));
    g_assert_cmpstr(setup_config_load_mode_scope(), ==, "process");

    // 未知值/损坏文件一律回退缺省 system（与 Go store 的宽容归一化一致）。
    g_assert_true(g_file_set_contents(path, "{\n  \"mode_scope\": \"bogus\"\n}\n",
                                      -1, NULL));
    g_assert_cmpstr(setup_config_load_mode_scope(), ==, "system");
    g_assert_true(g_file_set_contents(path, "not json at all", -1, NULL));
    g_assert_cmpstr(setup_config_load_mode_scope(), ==, "system");

    g_free(path);
    g_free(dir);
}

// 存量值读取时归一化：system/process 保持原值、旧三档 window→process；
// 大小写/首尾空白容忍；未知/空→system（与 Go parseStoredModeScope 一致）。
static void test_config_mode_scope_legacy_normalized(void) {
    gchar *dir = g_dir_make_tmp("chaoset-setup-test-XXXXXX", NULL);
    g_assert_nonnull(dir);
    g_setenv("XDG_CONFIG_HOME", dir, TRUE);
    g_setenv("PATH", "/nonexistent", TRUE);

    gchar *path = g_build_filename(dir, "chaoset", "config.json", NULL);
    gchar *dir_path = g_path_get_dirname(path);
    g_mkdir_with_parents(dir_path, 0700);
    const struct {
        const char *raw;
        const char *want;
    } kCases[] = {
        {"system", "system"},
        {"SYSTEM", "system"},
        {"process", "process"},
        {"PROCESS", "process"},
        {"window", "process"}, // 旧值迁移：window→process
        {" WINDOW ", "process"},
        {"bogus", "system"},
        {"", "system"},
    };
    for (gsize i = 0; i < G_N_ELEMENTS(kCases); i++) {
        gchar *json = g_strdup_printf("{\n  \"mode_scope\": \"%s\"\n}\n", kCases[i].raw);
        g_assert_true(g_file_set_contents(path, json, -1, NULL));
        g_free(json);
        g_assert_cmpstr(setup_config_load_mode_scope(), ==, kCases[i].want);
    }

    g_free(dir_path);
    g_free(path);
    g_free(dir);
}

int main(int argc, char **argv) {
    g_test_init(&argc, &argv, NULL);
    g_test_add_func("/setupctl/config_roundtrip", test_config_roundtrip);
    g_test_add_func("/setupctl/config_missing_defaults_on", test_config_missing_defaults_on);
    g_test_add_func("/setupctl/config_mode_scope_preserves_unknown_keys",
                    test_config_mode_scope_preserves_unknown_keys);
    g_test_add_func("/setupctl/config_mode_scope_defaults_create_and_corrupt",
                    test_config_mode_scope_defaults_create_and_corrupt);
    g_test_add_func("/setupctl/config_mode_scope_legacy_normalized",
                    test_config_mode_scope_legacy_normalized);
    return g_test_run();
}