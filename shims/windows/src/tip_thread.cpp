// TIP 线程 message-only 窗口：注册表、窗口过程、创建/销毁与 TipThreadSink 分发
// （Windows 壳拆分 TU，wave2）。
//
// 从 win32.cpp 迁入（wave2）：窗口过程 tipMsgWndProc（worker/钩子回调 → TIP
// 线程的分发，经 TipThreadSink 接口）、ensureTipMsgWnd、按线程 TIP 注册表
// g_tlFgTips/g_tlHookTips/g_tlFgHook/g_tlTipMsgWnd 与 g_liveTipMsgWindows、
// registerTipOnThread/unregisterTipFromThread、前台钩子表 helper。
// worker → TIP 线程回投原语（postTipThread/postTipAll/regTipWnd/unregTipWnd）
// 自 wave1 起就在本 TU。
//
// 模块边界（docs/architecture.md §3.1）：不 include/引用具体 TIP 类型；
// 对 TIP 的唯一接触是 TipThreadSink 抽象接口。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <vector>

#include "tip_thread.hpp"
#include "winutil.hpp"   // windowClassName / g_hInst

namespace chaoset::win {

// 存活中的 TIP message-only 窗口数量（DllCanUnloadNow 复核）。
volatile LONG g_liveTipMsgWindows = 0;

// ---- worker → TIP 线程回投（每线程一个 message-only 窗口）----
struct TipThreadWndEntry { DWORD tid; HWND wnd; };
static SRWLOCK g_tipWndLock = SRWLOCK_INIT;
static TipThreadWndEntry g_tipWnds[8] = {};

void regTipWnd(DWORD tid, HWND wnd) {
    AcquireSRWLockExclusive(&g_tipWndLock);
    for (auto& e : g_tipWnds) {
        if (e.wnd == nullptr || e.tid == tid) {
            e.tid = tid;
            e.wnd = wnd;
            break;
        }
    }
    ReleaseSRWLockExclusive(&g_tipWndLock);
}

void unregTipWnd(HWND wnd) {
    AcquireSRWLockExclusive(&g_tipWndLock);
    for (auto& e : g_tipWnds) {
        if (e.wnd == wnd) {
            e.wnd = nullptr;
            e.tid = 0;
        }
    }
    ReleaseSRWLockExclusive(&g_tipWndLock);
}

void postTipThread(DWORD tid, UINT msg, WPARAM wp, LPARAM lp) {
    HWND target = nullptr;
    AcquireSRWLockShared(&g_tipWndLock);
    for (const auto& e : g_tipWnds) {
        if (e.wnd != nullptr && e.tid == tid) {
            target = e.wnd;
            break;
        }
    }
    ReleaseSRWLockShared(&g_tipWndLock);
    if (target != nullptr) PostMessageW(target, msg, wp, lp);
}

void postTipAll(UINT msg, WPARAM wp, LPARAM lp) {
    HWND targets[8] = {};
    int n = 0;
    AcquireSRWLockShared(&g_tipWndLock);
    for (const auto& e : g_tipWnds) {
        if (e.wnd != nullptr && n < 8) targets[n++] = e.wnd;
    }
    ReleaseSRWLockShared(&g_tipWndLock);
    for (int i = 0; i < n; ++i) PostMessageW(targets[i], msg, wp, lp);
}

// ---- 按线程的 TIP 注册表（原 win32.cpp 匿名命名空间；thread_local）----
// 前台钩子：每线程最多一个系统级钩子（多个 TIP 实例共用一个，回调只
// PostMessage 到该线程的 message-only 窗口；不 hide、不写盘 —— 审计 T6）。
thread_local HWINEVENTHOOK g_tlFgHook = nullptr;
thread_local std::vector<TipThreadSink*> g_tlFgTips;  // 本线程全部 TIP（消息派发）
thread_local std::vector<TipThreadSink*> g_tlHookTips;  // 已挂前台钩子的 TIP
thread_local HWND g_tlTipMsgWnd = nullptr;  // 本线程 message-only 窗口

void addTipToThreadHookList(TipThreadSink* tip) {
    for (TipThreadSink* p : g_tlHookTips) {
        if (p == tip) return;
    }
    g_tlHookTips.push_back(tip);  // 2026-11-07 修正：此前误 push 到 g_tlFgTips
}

void removeTipFromThreadHookList(TipThreadSink* tip) {
    for (auto it = g_tlHookTips.begin(); it != g_tlHookTips.end(); ++it) {
        if (*it == tip) {
            g_tlHookTips.erase(it);  // 2026-11-07 修正：此前误删 g_tlFgTips
            return;
        }
    }
}

// ---- TIP 线程 message-only 窗口（worker / 钩子回调 → TIP 线程）----
//
// 2026-11-06 零阻塞：ShellWorker 与 SetWinEventHook 回调都不能碰 TSF/UI
// 对象，只能 PostMessage 到这个窗口；窗口过程在 TIP 线程上分发。
static LRESULT CALLBACK tipMsgWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
        case WM_NCDESTROY:
            if (g_tlTipMsgWnd == hwnd) g_tlTipMsgWnd = nullptr;
            unregTipWnd(hwnd);
            InterlockedDecrement(&g_liveTipMsgWindows);
            return 0;
        case kTipForegroundMsg: {
            HWND fg = reinterpret_cast<HWND>(wParam);
            for (TipThreadSink* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerForeground(fg);
            }
            return 0;
        }
        case kTipModeAuthorityMsg: {
            for (TipThreadSink* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerModeAuthority();
            }
            return 0;
        }
        case kTipShowToastMsg:
            for (TipThreadSink* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerShowToast(static_cast<int>(wParam));
            }
            return 0;
        default:
            break;
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

HWND ensureTipMsgWnd() {
    if (g_tlTipMsgWnd != nullptr) return g_tlTipMsgWnd;
    const std::wstring cls = windowClassName(L"ChaosetTipMsgWndClass", g_hInst);
    WNDCLASSEXW wc{};
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = tipMsgWndProc;
    wc.hInstance = g_hInst;
    wc.lpszClassName = cls.c_str();
    RegisterClassExW(&wc);
    HWND wnd = CreateWindowExW(0, cls.c_str(), L"ChaosetTipMsg", 0, 0, 0, 0, 0,
                               HWND_MESSAGE, nullptr, g_hInst, nullptr);
    if (wnd == nullptr) return nullptr;
    g_tlTipMsgWnd = wnd;
    InterlockedIncrement(&g_liveTipMsgWindows);
    regTipWnd(GetCurrentThreadId(), wnd);
    return wnd;
}

void registerTipOnThread(TipThreadSink* tip) {
    if (tip == nullptr) return;
    for (TipThreadSink* p : g_tlFgTips) {
        if (p == tip) return;
    }
    g_tlFgTips.push_back(tip);
}

void unregisterTipFromThread(TipThreadSink* tip) {
    if (tip == nullptr) return;
    removeTipFromThreadHookList(tip);
    for (auto it = g_tlFgTips.begin(); it != g_tlFgTips.end(); ++it) {
        if (*it == tip) {
            g_tlFgTips.erase(it);
            break;
        }
    }
    if (g_tlFgTips.empty() && g_tlTipMsgWnd != nullptr) {
        DestroyWindow(g_tlTipMsgWnd);  // WM_NCDESTROY 里归还计数并清 thread_local
    }
}

}  // namespace chaoset::win
