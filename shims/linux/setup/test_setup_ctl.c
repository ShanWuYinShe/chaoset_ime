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

int main(int argc, char **argv) {
    g_test_init(&argc, &argv, NULL);
    g_test_add_func("/setupctl/config_roundtrip", test_config_roundtrip);
    g_test_add_func("/setupctl/config_missing_defaults_on", test_config_missing_defaults_on);
    return g_test_run();
}