// 统一设置中心启动器（模块实现；声明与调用约束见 settings_launcher.hpp）。
//
// Windows 壳不再有原生设置窗：唯一设置 UI 是跨平台 Slint 设置中心
//（shims/settings，产物 chaoset-settings.exe）。本模块只做两件事：
//   1) 在 chaoset-ui.exe / ChaosetIME.dll 同目录找 chaoset-settings.exe；
//   2) CreateProcessW 脱钩启动（CREATE_NO_WINDOW、不等待退出）。
// 单窗口语义由设置中心自己的单实例逻辑（Local\ChaosetSettingsCenter 互斥体）
// 保证：重复启动的实例把已有窗口前置后立即退出，本模块不重复实现。
// 绝不在 TSF/TIP 进程内执行——那会在宿主 UI 线程上同步建进程（卡顿源），
// 且 AppContainer 宿主（开始菜单/搜索）里 CreateProcessW 会继承 AC 身份。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <string>
#include <vector>

#include "diagnostics.hpp"
#include "settings_launcher.hpp"
#include "winutil.hpp"

namespace chaoset::win {
namespace {

constexpr wchar_t kSettingsExeName[] = L"chaoset-settings.exe";

// 模块所在目录（返回值以反斜杠结尾）；取不到返回空串。
std::wstring moduleDir(HMODULE module) {
    wchar_t path[MAX_PATH] = {0};
    if (GetModuleFileNameW(module, path, MAX_PATH) == 0) return std::wstring();
    std::wstring dir(path);
    const size_t slash = dir.find_last_of(L"\\/");
    if (slash == std::wstring::npos) return std::wstring();
    dir.resize(slash + 1);
    return dir;
}

// 宽字符 → UTF-8：只用于把路径写进单行 tsf.log（vsnprintf 是窄字符格式化）。
std::string utf8Of(const std::wstring& text) {
    if (text.empty()) return std::string();
    const int bytes = WideCharToMultiByte(CP_UTF8, 0, text.c_str(), -1, nullptr, 0,
                                          nullptr, nullptr);
    if (bytes <= 1) return std::string();
    std::string out(static_cast<size_t>(bytes - 1), '\0');
    WideCharToMultiByte(CP_UTF8, 0, text.c_str(), -1, out.data(), bytes, nullptr, nullptr);
    return out;
}

// 先找 chaoset-ui.exe（当前进程 exe）同目录，再找本 DLL 同目录：安装包/便携包
// 把 DLL、UI 宿主与设置中心放在同一目录，分离部署时两种位置都认。
std::wstring findSettingsExe() {
    const HMODULE probes[] = {nullptr, g_hInst};  // nullptr = 当前进程 exe 自身
    for (HMODULE probe : probes) {
        const std::wstring dir = moduleDir(probe);
        if (dir.empty()) continue;
        const std::wstring candidate = dir + kSettingsExeName;
        if (GetFileAttributesW(candidate.c_str()) != INVALID_FILE_ATTRIBUTES) return candidate;
    }
    return std::wstring();
}

// --page 只接受 Slint 设置中心认识的三种取值（见 shims/settings/src/main.rs），
// 其它值一律不传、按默认页启动。
bool isKnownPage(const wchar_t* page) {
    return page != nullptr &&
           (wcscmp(page, L"input") == 0 || wcscmp(page, L"dict") == 0 ||
            wcscmp(page, L"about") == 0);
}

bool launchAtPage(const wchar_t* page) {
    const std::wstring exe = findSettingsExe();
    if (exe.empty()) {
        // 安装包/便携包必须带 chaoset-settings.exe（package_msvc.sh 与 NSIS 都
        // 强制校验）；走到这里说明安装不完整，或只把 DLL/UI 宿主单独拷了出来。
        diagPrintf(L"tsf.log",
                   "settings center missing: %s not found next to chaoset-ui.exe or "
                   "ChaosetIME.dll (incomplete install)\n",
                   "chaoset-settings.exe");
        return false;
    }

    std::wstring command = L"\"" + exe + L"\"";
    if (isKnownPage(page)) {
        command += L" --page ";
        command += page;
    }
    // CreateProcessW 可能临时改写命令行缓冲：给独立的可写缓冲并补终止符。
    std::vector<wchar_t> commandBuf(command.begin(), command.end());
    commandBuf.push_back(L'\0');
    // 工作目录设为设置中心所在目录：文件对话框/相对资源解析不依赖 UI 宿主的 cwd。
    std::wstring workDir = exe;
    const size_t slash = workDir.find_last_of(L"\\/");
    if (slash != std::wstring::npos) {
        workDir.resize(slash + 1);
    } else {
        workDir.clear();
    }

    STARTUPINFOW si{};
    si.cb = sizeof(si);
    PROCESS_INFORMATION pi{};
    // CREATE_NO_WINDOW：GUI 子系统程序本无控制台，显式声明避免偶发黑框；
    // 不等待：设置中心生命周期独立于 UI 宿主（关掉 UI 宿主/输入法不影响设置窗）。
    const BOOL ok = CreateProcessW(exe.c_str(), commandBuf.data(), nullptr, nullptr, FALSE,
                                   CREATE_NO_WINDOW, nullptr,
                                   workDir.empty() ? nullptr : workDir.c_str(), &si, &pi);
    if (!ok) {
        const std::string utf8 = utf8Of(exe);
        diagPrintf(L"tsf.log", "settings center launch failed: %s err=%lu\n", utf8.c_str(),
                   static_cast<unsigned long>(GetLastError()));
        return false;
    }
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    // 不等待退出：设置中心生命周期独立于 UI 宿主。重复启动由设置中心自己的
    // 单实例逻辑收口（已有窗口前置后退出），本模块不轮询、不等待。
    return true;
}

}  // namespace

bool LaunchSettingsCenter() { return launchAtPage(nullptr); }

bool LaunchSettingsCenterAtPage(const wchar_t* page) { return launchAtPage(page); }

}  // namespace chaoset::win
