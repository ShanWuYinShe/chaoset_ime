// chaoset-ui.exe：单例 UI 宿主进程（托盘中英指示器 + 设置窗）。
//
// 只有几十行：找到与自身同目录的 ChaosetIME.dll，调用导出 ChaosetRunUi。
// 托盘绘制 / 设置窗 / 命名管道命令全部复用本 DLL 内的同一份实现，
// 不存在第二套 UI 代码；也不把设置窗搬进 Go daemon（项目职责边界：UI 归平台 shim）。
//
// 为什么需要独立进程见 ui_client.hpp / ui_host.hpp 的说明：
//   - TSF 把 TIP 加载进每个宿主进程，进程内自绘托盘会变成任务栏多个图标；
//   - 设置窗由宿主进程托管时，开始菜单/搜索（Low IL AppContainer）里打不开，
//     且宿主退出会带走设置窗。
//
// 构建：x86_64-w64-mingw32-g++ -municode -mwindows（见 build.sh）。
#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

namespace {
using RunUiFn = int(WINAPI*)(HINSTANCE, LPCWSTR);

void showLoadError(const wchar_t* text) {
    MessageBoxW(nullptr, text, L"Chaoset", MB_OK | MB_ICONERROR);
}

// 动态启用 Per-Monitor V2 DPI 意识：根治高分屏缩放下 DWM 位图缩放导致的模糊问题。
void enablePerMonitorDpiAwareness() {
    typedef BOOL(WINAPI* SetProcessDpiAwarenessContextFn)(HANDLE);
    HMODULE user32 = GetModuleHandleW(L"user32.dll");
    if (user32 != nullptr) {
        auto setContext = reinterpret_cast<SetProcessDpiAwarenessContextFn>(
            reinterpret_cast<void*>(GetProcAddress(user32, "SetProcessDpiAwarenessContext")));
        if (setContext != nullptr) {
            // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
            // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE = -3
            if (setContext(reinterpret_cast<HANDLE>(static_cast<INT_PTR>(-4))) ||
                setContext(reinterpret_cast<HANDLE>(static_cast<INT_PTR>(-3)))) {
                return;
            }
        }
    }
    HMODULE shcore = LoadLibraryW(L"shcore.dll");
    if (shcore != nullptr) {
        typedef HRESULT(WINAPI* SetProcessDpiAwarenessFn)(int);
        auto setDpiAwareness = reinterpret_cast<SetProcessDpiAwarenessFn>(
            reinterpret_cast<void*>(GetProcAddress(shcore, "SetProcessDpiAwareness")));
        if (setDpiAwareness != nullptr && SUCCEEDED(setDpiAwareness(2 /* PROCESS_PER_MONITOR_DPI_AWARE */))) {
            return;
        }
    }
    if (user32 != nullptr) {
        typedef BOOL(WINAPI* SetProcessDPIAwareFn)();
        auto setDpiAware = reinterpret_cast<SetProcessDPIAwareFn>(
            reinterpret_cast<void*>(GetProcAddress(user32, "SetProcessDPIAware")));
        if (setDpiAware != nullptr) setDpiAware();
    }
}
}  // namespace

int WINAPI wWinMain(HINSTANCE hInst, HINSTANCE, LPWSTR /*cmdLine*/, int) {
    enablePerMonitorDpiAwareness();
    wchar_t path[MAX_PATH] = {0};
    if (GetModuleFileNameW(hInst, path, MAX_PATH) == 0) return 1;
    wchar_t* slash = wcsrchr(path, L'\\');
    if (slash == nullptr) return 1;
    lstrcpyW(slash + 1, L"ChaosetIME.dll");

    HMODULE dll = LoadLibraryW(path);
    if (dll == nullptr) {
        showLoadError(L"找不到 ChaosetIME.dll（请使用完整安装包）。");
        return 1;
    }
    // GetProcAddress 返回签名未知的 FARPROC：先经 void* 中转再转目标签名，
    // 避免 mingw -Wall 的 -Wcast-function-type（与 candidate_window.cpp 的 loadProcAddress 同语义）。
    auto runUi = reinterpret_cast<RunUiFn>(reinterpret_cast<void*>(GetProcAddress(dll, "ChaosetRunUi")));
    if (runUi == nullptr) {
        FreeLibrary(dll);
        showLoadError(L"ChaosetIME.dll 缺少 ChaosetRunUi 导出（DLL 与 UI 宿主版本不匹配）。");
        return 1;
    }
    // cmdLine 由 DLL 解析（--settings 表示首实例启动后直接打开设置窗）。
    // runUi（ChaosetRunUi）返回前已 join 命令管道线程并销毁本 DLL 创建的窗口，
    // 因此这里 FreeLibrary 是安全的（2026-11-05 评审 P0：否则线程可能仍在
    // 已卸载代码里运行）。
    const int rc = runUi(hInst, GetCommandLineW());
    FreeLibrary(dll);
    return rc;
}
