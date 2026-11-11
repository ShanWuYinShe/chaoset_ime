// TIP 线程 message-only 窗口的跨线程注册表与回投原语；声明与职责说明见
// tip_thread.hpp。窗口过程/按线程 TIP 注册表仍在 win32.cpp（wave2 迁移）。
//
// 叶子模块（docs/architecture.md §3.1）：不 include/引用 win32.cpp 的 TIP 类型。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include "tip_thread.hpp"

namespace chaoset::win {

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

}  // namespace chaoset::win
