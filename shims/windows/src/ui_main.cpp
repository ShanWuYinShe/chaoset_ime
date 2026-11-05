// chaoset-ui.exe：单例 UI 宿主进程（托盘中英指示器 + 设置窗）。
//
// 只有几十行：找到与自身同目录的 ChaosetIME.dll，调用导出 ChaosetRunUi。
// 托盘绘制 / 设置窗 / 命名管道命令全部复用 TSF 壳（win32.cpp）里的同一份实现，
// 不存在第二套 UI 代码；也不把设置窗搬进 Go daemon（项目职责边界：UI 归平台 shim）。
//
// 为什么需要独立进程见 win32.cpp「单例 UI 宿主进程通信」注释：
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
}  // namespace

int WINAPI wWinMain(HINSTANCE hInst, HINSTANCE, LPWSTR /*cmdLine*/, int) {
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
    auto runUi = reinterpret_cast<RunUiFn>(GetProcAddress(dll, "ChaosetRunUi"));
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
