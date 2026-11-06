// Windows 公共小工具（模块实现；声明见 winutil.hpp）。
// 叶子模块：无内部依赖；不得 include/引用 win32.cpp（TIP TU）的类型。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <sddl.h>  // ConvertSidToStringSidW（命名管道名按当前用户 SID）
#include <string>
#include <vector>

#include "winutil.hpp"

namespace chaoset::win {

// 本 DLL 模块句柄（原 win32.cpp 的 static 全局；DllMain 赋值）。
HINSTANCE g_hInst = nullptr;

std::wstring toWide(const std::string& utf8) {
    int n = MultiByteToWideChar(CP_UTF8, 0, utf8.c_str(), static_cast<int>(utf8.size()), nullptr, 0);
    std::wstring w;
    if (n > 0) {
        w.resize(static_cast<std::size_t>(n));
        MultiByteToWideChar(CP_UTF8, 0, utf8.c_str(), static_cast<int>(utf8.size()), &w[0], n);
    }
    return w;
}

bool isAppContainer() {
    static int cached = -1;
    if (cached != -1) return cached == 1;

    HANDLE hToken = nullptr;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &hToken)) {
        cached = 0;
        return false;
    }
    BOOL isAppCont = FALSE;
    DWORD len = 0;
    // TokenIsAppContainer (29) 判断当前进程是否运行在 UWP / AppContainer 沙盒中
    if (GetTokenInformation(hToken, static_cast<TOKEN_INFORMATION_CLASS>(29), &isAppCont, sizeof(isAppCont), &len)) {
        cached = isAppCont ? 1 : 0;
    } else {
        cached = 0;
    }
    CloseHandle(hToken);
    return cached == 1;
}

// ---- 窗口类名：带模块基址（防止 DLL 重载后沿用旧类的悬垂 WndProc）----
//
// TSF 会在宿主空闲时调用 DllCanUnloadNow 卸载本 DLL。Windows 不会随 DLL
// 卸载注销窗口类（RegisterClassExW 文档原文：No window classes registered
// by a DLL are unregistered when the DLL is unloaded）。重新加载后若模块
// 基址变化，同名旧类的 lpfnWndProc 仍指向已卸载内存；RegisterClassExW
// 失败后 CreateWindowExW 会沿用旧类，第一次 WM_PAINT 就跳到野指针，
// 宿主进程直接退出（2026-10-29 审查发现，与「输入拼音后进程消失」的
// 候选窗首帧时序一致）。
//
// 类名带上模块基址只是**第二道防线**，不能单独作为安全依据：跨版本重载时
// 同一基址下函数偏移可能不同（「基址相同 = 同地址」不成立）。真正的保护是
// DllMain（DLL_PROCESS_DETACH）里主动 UnregisterClassW 所有本 DLL 注册的
// 窗口类；新增窗口类必须同时加到 DllMain 的注销列表（2026-11-05 回归审计
// P1-4/P3-4：ChaosetTrayWndClass 曾漏注销）。
std::wstring windowClassName(const wchar_t* base, HINSTANCE inst) {
    wchar_t buf[96] = {0};
    wsprintfW(buf, L"%s-%p", base, static_cast<void*>(inst));
    return std::wstring(buf);
}

// ---- 系统 DPI ----
//
// 候选窗 / 模式提示 / 设置窗口共用。MulDiv(pt, dpi, 72) 即 pt→设备像素。
//
// 2026-10-31：per-monitor DPI 已接入（候选窗 / Toast / 设置窗口）：
//   - dpiForPoint：动态加载 shcore!GetDpiForMonitor，按锚点所在显示器取 DPI；
//   - 三处窗口都处理 WM_DPICHANGED 重新布局/重绘（候选窗以锚点显示器为准）。
// systemDpi() 保留为主屏/旧系统（Win8.1 之前无 shcore）兜底。
// 仍需 Windows 真机验收混合 DPI 多屏（本机无 Windows 运行时）。
int systemDpi() {
    HDC hdc = GetDC(nullptr);
    int dpi = 96;
    if (hdc != nullptr) {
        dpi = GetDeviceCaps(hdc, LOGPIXELSY);
        ReleaseDC(nullptr, hdc);
    }
    return dpi;
}

// 取指定屏幕坐标所在显示器的 DPI（Win8.1+ 的 shcore!GetDpiForMonitor）。
// 动态加载：Win7 没有 shcore.dll，缺失时退回主屏 DPI，不影响启动。
int dpiForPoint(POINT pt) {
    typedef HRESULT(WINAPI* GetDpiForMonitorFn)(HMONITOR, int, UINT*, UINT*);
    static GetDpiForMonitorFn fn = []() -> GetDpiForMonitorFn {
        HMODULE mod = LoadLibraryW(L"shcore.dll");
        if (mod == nullptr) return nullptr;
        // 经 void* 中转：FARPROC 与具体函数指针类型不同，直接 reinterpret_cast
        // 会触发 -Wcast-function-type（不改编译选项）。
        return reinterpret_cast<GetDpiForMonitorFn>(
            reinterpret_cast<void*>(GetProcAddress(mod, "GetDpiForMonitor")));
    }();
    HMONITOR mon = MonitorFromPoint(pt, MONITOR_DEFAULTTONEAREST);
    if (fn != nullptr && mon != nullptr) {
        UINT x = 96, y = 96;
        // MDT_EFFECTIVE_DPI = 0
        if (SUCCEEDED(fn(mon, 0, &x, &y)) && x > 0) return static_cast<int>(x);
    }
    return systemDpi();
}

std::wstring currentUserSid() {
    static std::wstring cached;
    static bool tried = false;
    if (tried) return cached;
    tried = true;
    HANDLE token = nullptr;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) return cached;
    DWORD len = 0;
    GetTokenInformation(token, TokenUser, nullptr, 0, &len);
    if (len > 0) {
        std::vector<BYTE> buf(len);
        if (GetTokenInformation(token, TokenUser, buf.data(), len, &len)) {
            auto* user = reinterpret_cast<TOKEN_USER*>(buf.data());
            LPWSTR sid = nullptr;
            if (ConvertSidToStringSidW(user->User.Sid, &sid) && sid != nullptr) {
                cached = sid;
                LocalFree(sid);
            }
        }
    }
    CloseHandle(token);
    return cached;
}

// UI 宿主命名空间：管道与互斥体都带「当前用户 SID + 会话 ID」。
// 只用 SID 不够：RDP/快速用户切换时同一用户的两个会话各有自己的 explorer 与
// UI 宿主，共用一个管道名会让 mode/settings 落到另一个会话（2026-11-05 评审 P1）。
DWORD currentSessionId() {
    DWORD id = 0;
    ProcessIdToSessionId(GetCurrentProcessId(), &id);
    return id;
}

}  // namespace chaoset::win
