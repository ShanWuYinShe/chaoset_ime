// TIP 线程 message-only 窗口的消息常量、跨 TU 注册表原语与 TipThreadSink 接口
// （Windows 壳拆分 TU，wave2）。
//
// worker 线程（shell_worker.cpp）只能经 postTipThread/postTipAll 把结果回投到
// TIP 线程的 message-only 窗口；窗口过程、按线程 TIP 注册表、窗口创建/销毁与
// 前台钩子表全部在 tip_thread.cpp。TIP 侧（tip_core.cpp）通过 TipThreadSink
// 抽象接口接收分发（onWorkerForeground/onWorkerModeAuthority/onWorkerShowToast），
// 因此本 TU 不 include/引用具体 TIP 类型。
//
// 模块边界（docs/architecture.md §3.1）：只依赖 <windows.h>/<vector>；
// 不 include tip.hpp 或 win32.cpp。

#ifndef CHAOSET_WIN_TIP_THREAD_HPP_
#define CHAOSET_WIN_TIP_THREAD_HPP_

#include <windows.h>

#include <vector>

namespace chaoset::win {

constexpr UINT kTipForegroundMsg    = WM_APP + 101;  // 前台变化（钩子回调 → TIP 线程）
constexpr UINT kTipModeAuthorityMsg = WM_APP + 103;  // 后台查到的权威中英状态
constexpr UINT kTipShowToastMsg     = WM_APP + 104;  // 后台失败提示（wParam=文案 id）
// worker 线程 → TIP 线程回投（按线程 id 找该线程的 message-only 窗口）。
void postTipThread(DWORD tid, UINT msg, WPARAM wp, LPARAM lp);
// 广播到所有存活的 TIP 线程窗口（权威中英状态变化等）。
void postTipAll(UINT msg, WPARAM wp, LPARAM lp);

// message-only 窗口注册表（tip_thread.cpp 的窗口过程/创建/销毁路径调用）。
void regTipWnd(DWORD tid, HWND wnd);
void unregTipWnd(HWND wnd);

// TIP 侧对 message-only 窗口的分发接口（wave2）：窗口过程只认这个抽象接口，
// 不依赖具体 TIP 类，保持「叶子模块不得反向 include TIP」的单向依赖。
class TipThreadSink {
public:
    virtual ~TipThreadSink() = default;
    virtual void onWorkerForeground(HWND fg) = 0;
    virtual void onWorkerModeAuthority() = 0;
    virtual void onWorkerShowToast(int id) = 0;
};

// 按线程的 TIP 注册表与消息窗口（Thread_local；窗口过程见 tip_thread.cpp）。
extern thread_local HWINEVENTHOOK g_tlFgHook;                  // 本线程唯一前台钩子
extern thread_local std::vector<TipThreadSink*> g_tlFgTips;    // 本线程全部 TIP（消息派发）
extern thread_local std::vector<TipThreadSink*> g_tlHookTips;  // 已挂前台钩子的 TIP
extern thread_local HWND g_tlTipMsgWnd;                        // 本线程 message-only 窗口
// 存活中的 TIP message-only 窗口数量：窗口过程指向本 DLL 代码，存活期间禁止
// 宿主 FreeLibrary（DllCanUnloadNow 复核）。
extern volatile LONG g_liveTipMsgWindows;

HWND ensureTipMsgWnd();
void registerTipOnThread(TipThreadSink* tip);
void unregisterTipFromThread(TipThreadSink* tip);
void addTipToThreadHookList(TipThreadSink* tip);
void removeTipFromThreadHookList(TipThreadSink* tip);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_TIP_THREAD_HPP_
