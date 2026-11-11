// 每进程一个后台 worker（零阻塞契约的副作用执行器）——Windows 壳独立 TU。
//
// 从 win32.cpp 拆出（wave1 续）。TSF 回调在宿主 UI 线程同步执行，函数体内
// 禁止跨进程等待/建进程/同步写盘；这类副作用一律经本模块入队到 worker 线程：
//   * UI 宿主管道命令/查询（含重试）  * CreateProcessW（chaoset-ui.exe / chaosetd）
//   * daemon 连接与懒启动            * tsf.log 写盘
// worker 任务的结果经 tip_thread.hpp 的 postTipThread/postTipAll 回投 TIP 线程
// 的 message-only 窗口（本 TU 不 include/引用 TIP 类型）。
//
// 模块边界（docs/architecture.md §3.1）：不得 include win32.cpp；依赖
// transport/daemon/ui_client/diagnostics 叶子模块与 tip_thread 的消息常量。
// 新增资源都必须计入 DllCanUnloadNow：worker 线程（shellWorkerAlive）。

#ifndef CHAOSET_WIN_SHELL_WORKER_HPP_
#define CHAOSET_WIN_SHELL_WORKER_HPP_

#include <windows.h>

#include <atomic>

#include "transport.hpp"

namespace chaoset::win {

// ---- 权威中英状态（worker 查询结果；TSF 线程只读原子值）----
extern std::atomic<int> g_modeAuthorityValid;
extern std::atomic<int> g_modeAuthorityEnglish;

// ---- worker 生命周期（DllMain / ChaosetRunUi / DllCanUnloadNow 使用）----
void shellWorkerStart();
void shellWorkerStop();
bool shellWorkerAlive();
// 有界等待线程退出（UI 宿主退出路径用；调用方给上界）。
void shellWorkerWaitStopped(DWORD ms);

// tsf.log 异步落盘入口（DllMain 注册给 diagnostics；worker 未启动直接丢弃）。
void enqueueLogLine(const char* text, int len);

// ---- TIP 线程 → worker 的投递入口（零等待：只入队）----
//
// 命令协议（UI 宿主 ui_host.cpp 服务端解析，pid 由 worker 附 GetCurrentProcessId()）：
//   mode <0|1> <pid>         模式变化即时上报
//   active 1 <0|1> <pid>     激活 + 当前中英 + pid（激活迁移/焦点事件时立即发）
//   active 0 <pid>           失活/失焦/停用
//   query / toggle / settings  不变
// english：当前中英模式（0=中文 1=英文）；allowSpawn：UI 宿主不可用时是否允许
// 在 worker 线程懒启动（调用方必须已确认 TSF 焦点 + 激活档案为真）。
void postModeReport(bool english, bool allowSpawn);
void postActiveReport(bool active, bool english, bool allowSpawn);
void postModeQuery();
void postDaemonWarm();
void postDaemonConnect();
void postOpenSettings(DWORD notifyThread);
void postEnsureUiHostByDaemon(bool settings);

// ---- TSF 线程零等待的 daemon/设置入口（TIP 调用）----
// 已连接→true；否则一次非阻塞探测 + 入队后台连接，返回 false（调用方 fail-open）。
bool ensureDaemonFast(PipeTransport& transport);
// 打开设置窗：只做前台授权 + 入队，恒返回 true（零等待）。
bool openSettingsViaUi();
// 把当前中英模式上报给 UI 宿主（只入队；前台校验在 worker 里做）。
bool reportModeToUi(bool english, bool allowSpawn);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_SHELL_WORKER_HPP_
