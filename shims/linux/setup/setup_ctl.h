#ifndef CHAOSET_SETUP_CTL_H
#define CHAOSET_SETUP_CTL_H

// Linux 设置窗的 C 侧编排：只调用 Go 工具（chaoset-userdict / chaoset-import）
// 与 ibus，不实现词库合并/用户词典读写；GTK UI 在 chaoset-setup.c（窗口
// 骨架）与 setup_page_*.c（页面），拆分见 docs/architecture.md §3.3。
//
// 返回值统一为 SetupResult（ok + 可显示的中文消息），调用方负责 free。

#include <glib.h>

typedef struct {
    gboolean ok;
    gchar *message;
} SetupResult;

void setup_result_free(SetupResult *result);

// 学习数据：stats / clear / export。clear 会先停 ibus 引擎并确认退出，
// 清空后恢复 ibus；export 只读，引擎运行中也可安全执行。
SetupResult setup_userdict_stats(void);
SetupResult setup_userdict_clear(void);
SetupResult setup_userdict_export(const gchar *out_path);

// 词库导入：chaoset-import 负责合并/备份/原子替换，成功后重启 ibus。
SetupResult setup_import_dicts(gchar **paths, gsize n_paths);

// 设置项：config.json 的 associate（未写=开启）。保存时保留其它键。
gboolean setup_config_load(gboolean *associate);
gboolean setup_config_save(gboolean associate);

/* 中英状态作用域（2026-11-06 与 Windows/macOS 设置窗对齐）：
 * "system"（默认，全系统共享）/ "process"（每程序）/ "window"（每窗口）。 */
const char *setup_config_load_mode_scope(void);
gboolean setup_config_save_mode_scope(const char *scope);

#endif