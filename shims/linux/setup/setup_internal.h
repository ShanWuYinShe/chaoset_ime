#ifndef CHAOSET_SETUP_INTERNAL_H
#define CHAOSET_SETUP_INTERNAL_H

// Linux 设置窗 C 侧内部接口：拆分后跨 TU 共享的非公开符号
// （2026-11-06 按职责拆分，模块地图见 docs/architecture.md §3.3）。
//
// 依赖方向（单向）：setup_learn.c / setup_import.c → setup_ctl.c。
//   setup_ctl.c    路径解析、Go 工具执行、config 读写、输入法进程生命周期；
//   setup_learn.c  学习数据统计/导出/清空；
//   setup_import.c 词库导入流程（成功后重启输入法）。
// 公开 API 仍只在 setup_ctl.h；本文件的声明仅供设置窗内部 TU 使用。

#include <glib.h>

#include "setup_ctl.h"

// SetupResult 构造器：message 所有权转移给结果（ok 版本为空时填「完成」）。
SetupResult result_ok(gchar *message);
SetupResult result_error(const gchar *message);
SetupResult result_error_take(gchar *message);

// Go 工具查找（PATH → 源码树 bin/）与同步执行；run_tool 成功 = 正常退出且
// 退出码 0。
gchar *find_tool(const gchar *name);
gboolean run_tool(gchar **argv, gchar **out, gchar **err, GError **error);

// 去掉子进程输出的首尾空白（接管所有权）；NULL 视为空串。
gchar *trimmed(gchar *text);

// 用户数据路径：优先 Go 工具 -print-paths，缺失时回退 XDG。
gchar *userdict_path(void);
gchar *imported_dict_path(void);

// 输入法进程生命周期：清空学习数据与导入词库前都必须先停 daemon（防止
// 内存旧状态回写）；操作完成后由调用方用 run_ibus("restart"/"start") 恢复。
GArray *engine_pids(void);
gboolean terminate_engines(GArray *pids, gint timeout_ms);
gboolean terminate_daemon(void);
gboolean run_ibus(const gchar *subcommand);

#endif // CHAOSET_SETUP_INTERNAL_H
