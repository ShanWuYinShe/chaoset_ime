// TIP 线程 message-only 窗口的消息常量与跨 TU 注册表原语（Windows 壳拆分 TU）。
//
// 从 win32.cpp 拆出（wave1 续）：worker 线程（shell_worker.cpp）只能经
// postTipThread/postTipAll 把结果回投到 TIP 线程的 message-only 窗口；窗口
// 过程与按线程的 TIP 注册表仍留在 win32.cpp（需要 ChaosetTIP 完整类型做方法
// 分发，wave2 随 tip.hpp 一起迁移到 tip_thread.cpp）。
//
// 叶子模块（docs/architecture.md §3.1）：不 include/引用 TIP 类型；只依赖
// <windows.h>。

#ifndef CHAOSET_WIN_TIP_THREAD_HPP_
#define CHAOSET_WIN_TIP_THREAD_HPP_

#include <windows.h>

namespace chaoset::win {

constexpr UINT kTipForegroundMsg    = WM_APP + 101;  // 前台变化（钩子回调 → TIP 线程）
constexpr UINT kTipModeAuthorityMsg = WM_APP + 103;  // 后台查到的权威中英状态
constexpr UINT kTipShowToastMsg     = WM_APP + 104;  // 后台失败提示（wParam=文案 id）
// TIP message-only 窗口定时器：只在「本线程有激活 TIP」时运行，500ms 复核一次
// 激活档案；用户切换输入法但 TSF 未调用 Deactivate 时，托盘要能及时隐藏。
constexpr UINT_PTR kActiveWatchTimerId = 3;

// worker 线程 → TIP 线程回投（按线程 id 找该线程的 message-only 窗口）。
void postTipThread(DWORD tid, UINT msg, WPARAM wp, LPARAM lp);
// 广播到所有存活的 TIP 线程窗口（权威中英状态变化等）。
void postTipAll(UINT msg, WPARAM wp, LPARAM lp);

// message-only 窗口注册表（win32.cpp 的窗口过程/创建/销毁路径调用）。
void regTipWnd(DWORD tid, HWND wnd);
void unregTipWnd(HWND wnd);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_TIP_THREAD_HPP_
