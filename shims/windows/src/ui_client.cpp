// 单例 UI 宿主（chaoset-ui.exe）管道低层：命令/查询/拉起原语（模块实现）。
// 叶子模块：不得 include/引用 TIP TU（tip.hpp / tip_*.cpp）的类型；
// reportModeToUi / openSettingsViaUi 等 worker 编排在 shell_worker.cpp。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <string>

#include "ui_client.hpp"
#include "diagnostics.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// ---- 单例 UI 宿主进程通信（chaoset-ui.exe）----
//
// 为什么托盘指示器与设置窗必须放进独立进程（2026-11-05 用户反馈的根因）：
//   1) TSF TIP 由系统加载进**每个宿主进程**——实测 explorer.exe 一个进程就有 4 个
//      TIP 实例（4 个按钮窗口）。进程内自绘托盘 = 每个实例一个图标，而且各实例
//      enMode_ 互相独立，任务栏会同时出现「中 英 中 英」多个图标；
//   2) 设置窗若由宿主进程创建：开始菜单/搜索（SearchApp.exe 等 Low IL
//      AppContainer）里会被沉浸式 z 序 band 压住，且文件对话框、导入器进程、
//      重启 daemon 在 AC 里都不可用；宿主退出还会带走设置窗。
// 因此任务栏中英指示器与设置窗统一交给「每用户会话唯一」的后台 UI 进程
// （chaoset-ui.exe，**不随登录启动**：TIP 首次激活时带 --mode 懒启动；托盘
// 可见性只由 TIP 的 active 上报控制，见 ui_host.cpp 的 kUiActiveMsg）。
// TIP 只通过命名管道上报模式与激活状态、请求打开设置，不持有任何 UI。
//
// 管道安全描述符与 daemon 完全同规则：DACL 当前用户 + SYSTEM +
// ALL APPLICATION PACKAGES(S-1-15-2-1)，SACL 低完整性标签 ML;;NW;;;LW。
// 这是 AppContainer 宿主（开始菜单/搜索）能「连上并写入」的前提；设置 SACL
// 失败时降级为仅 DACL（与 ipc/pipe_sddl.go 的降级策略一致）。

// UI 宿主命令管道名。SID 拿不到时返回空串：调用方按「UI 不可用」降级。
const std::wstring& uiPipeName() {
    static const std::wstring name = []() {
        const std::wstring sid = currentUserSid();
        if (sid.empty()) return std::wstring();
        wchar_t buf[256] = {0};
        wsprintfW(buf, L"\\\\.\\pipe\\chaoset-ui-%s-%lu", sid.c_str(),
                  static_cast<unsigned long>(currentSessionId()));
        return std::wstring(buf);
    }();
    return name;
}

// UI 宿主单实例互斥体名（同样带会话 ID，见 uiPipeName 注释）。
std::wstring uiMutexName() {
    const std::wstring sid = currentUserSid();
    if (sid.empty()) return std::wstring();
    wchar_t buf[256] = {0};
    wsprintfW(buf, L"Local\\ChaosetUi-%s-%lu", sid.c_str(),
              static_cast<unsigned long>(currentSessionId()));
    return std::wstring(buf);
}

// 发一条命令（每次短连接，几十字节）。服务端同一时刻只有一个实例在监听，
// 多个 TIP 实例并发上报可能拿到 ERROR_PIPE_BUSY，因此短重试；写失败也重试
// （服务端 DisconnectNamedPipe/CloseHandle 之间有极短窗口）。
bool uiSendCommandOnce(const char* command) {
    const std::wstring& name = uiPipeName();
    if (name.empty() || command == nullptr) return false;
    HANDLE pipe = CreateFileW(name.c_str(), GENERIC_WRITE, 0, nullptr, OPEN_EXISTING, 0, nullptr);
    if (pipe == INVALID_HANDLE_VALUE) {
        if (GetLastError() == ERROR_PIPE_BUSY) WaitNamedPipeW(name.c_str(), 50);
        return false;
    }
    std::string payload(command);
    payload.push_back('\n');
    DWORD written = 0;
    const BOOL ok = WriteFile(pipe, payload.data(), static_cast<DWORD>(payload.size()),
                              &written, nullptr);
    CloseHandle(pipe);
    return ok != FALSE && written == payload.size();
}

// attempts=4（≈40ms）用于按键路径；设置窗点击路径用更多次（≈200ms）。
//
// 2026-11-06：这个函数会在宿主 UI 线程上被调用（Activate / OnSetFocus），
// 所以「重试次数」必须很小。UI 宿主没跑时由 ensureUiHostStarted 节流拉起，
// 本次上报失败无害（下次焦点/切换事件会重报，UI 宿主启动后还有 HKCU 记忆兜底）。
bool uiSendCommand(const char* command, int attempts) {
    for (int i = 0; i < attempts; ++i) {
        if (uiSendCommandOnce(command)) return true;
        Sleep(10);
    }
    return false;
}

// 宿主 UI 线程专用版本：**完全不重试、不 Sleep**（单次非阻塞尝试）。
// Activate/OnSetFocus 在系统设置、开始菜单这类宿主里同步执行，任何 Sleep
// 都会直接变成用户可感知的卡顿（2026-11-06 用户反馈）。
bool uiSendCommandFast(const char* command) {
    return uiSendCommandOnce(command);
}

// 向 UI 宿主查权威中英状态（System 作用域）。只在 ShellWorker 后台线程调用。
//
// 2026-11-06 零阻塞审计（真机实测）：旧实现用同步句柄却传 OVERLAPPED，ReadFile
// 会同步阻塞到底、所谓的 200ms 上限是死代码；UI 宿主挂起时宿主 UI 线程可无限
// 阻塞。现在句柄带 FILE_FLAG_OVERLAPPED，200ms 等待真实生效；TSF 线程改读
// HKCU UiEnglish 镜像（见 syncEnglishFromAuthority），本函数只做异步校准。
bool uiQueryMode(bool* english) {
    if (english == nullptr) return false;
    const std::wstring& name = uiPipeName();
    if (name.empty()) return false;
    HANDLE pipe = CreateFileW(name.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr,
                              OPEN_EXISTING, FILE_FLAG_OVERLAPPED, nullptr);
    if (pipe == INVALID_HANDLE_VALUE) return false;
    bool ok = false;
    OVERLAPPED wov{};
    wov.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
    if (wov.hEvent != nullptr) {
        const char req[] = "query\n";
        DWORD written = 0;
        BOOL w = WriteFile(pipe, req, sizeof(req) - 1, &written, &wov);
        if (!w && GetLastError() == ERROR_IO_PENDING) {
            if (WaitForSingleObject(wov.hEvent, 200) == WAIT_OBJECT_0) {
                w = GetOverlappedResult(pipe, &wov, &written, FALSE);
            }
        }
        if (w && written == sizeof(req) - 1) {
            char buf[32] = {0};
            DWORD read = 0;
            OVERLAPPED ov{};
            ov.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
            if (ov.hEvent != nullptr) {
                BOOL r = ReadFile(pipe, buf, sizeof(buf) - 1, &read, &ov);
                if (!r && GetLastError() == ERROR_IO_PENDING) {
                    if (WaitForSingleObject(ov.hEvent, 200) == WAIT_OBJECT_0) {
                        if (!GetOverlappedResult(pipe, &ov, &read, FALSE)) read = 0;
                    } else {
                        CancelIoEx(pipe, &ov);
                        read = 0;
                    }
                }
                std::string reply(buf, read);
                if (reply.rfind("mode ", 0) == 0) {
                    *english = reply.size() >= 6 && reply[5] == '1';
                    ok = true;
                }
                CloseHandle(ov.hEvent);
            }
        }
        CloseHandle(wov.hEvent);
    }
    CloseHandle(pipe);
    return ok;
}

// 与 DLL 同目录的 chaoset-ui.exe（安装包/便携包都把它放在 DLL 旁边）。
static std::wstring uiHostExePath() {
    wchar_t module[MAX_PATH] = {0};
    if (g_hInst == nullptr || GetModuleFileNameW(g_hInst, module, MAX_PATH) == 0) return std::wstring();
    std::wstring dir(module);
    const size_t slash = dir.find_last_of(L"\\/");
    if (slash == std::wstring::npos) return std::wstring();
    dir.resize(slash);
    return dir + L"\\chaoset-ui.exe";
}

// 当前进程是否提升后的管理员。提升进程懒启动 UI 宿主会得到一个 elevated 的
// UI 进程（托盘/设置窗落在错误完整性级别，且它与普通 daemon 管道/用户数据可能
// 不一致）；因此只允许非提升进程懒启动，提升宿主依赖登录 Run 项的实例。
static bool isProcessElevated() {
    static int cached = -1;
    if (cached >= 0) return cached == 1;
    cached = 0;
    HANDLE token = nullptr;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)) return false;
    TOKEN_ELEVATION elevation{};
    DWORD len = 0;
    if (GetTokenInformation(token, TokenElevation, &elevation, sizeof(elevation), &len)) {
        cached = elevation.TokenIsElevated ? 1 : 0;
    }
    CloseHandle(token);
    return cached == 1;
}

// 拉起 UI 宿主（detached，不等待退出）。AppContainer 宿主里 CreateProcessW 会让
// 子进程继承 AC 身份（写错用户目录、连不上同名管道），一律不发起进程。
static bool uiSpawnHost(const wchar_t* args) {
    if (isAppContainer()) return false;
    if (isProcessElevated()) {
        static volatile LONG loggedElevated = 0;
        if (InterlockedCompareExchange(&loggedElevated, 1, 0) == 0) {
            diagPrintf(L"tsf.log", "ui host spawn skipped: caller elevated\n");
        }
        return false;
    }
    const std::wstring exe = uiHostExePath();
    if (exe.empty() || GetFileAttributesW(exe.c_str()) == INVALID_FILE_ATTRIBUTES) return false;
    std::wstring cmd = L"\"" + exe + L"\"";
    if (args != nullptr && *args != L'\0') {
        cmd += L" ";
        cmd += args;
    }
    std::wstring mutableCmd = cmd;
    STARTUPINFOW si{};
    si.cb = sizeof(si);
    PROCESS_INFORMATION pi{};
    BOOL ok = CreateProcessW(exe.c_str(), mutableCmd.data(), nullptr, nullptr, FALSE,
                             CREATE_NO_WINDOW | CREATE_BREAKAWAY_FROM_JOB, nullptr, nullptr,
                             &si, &pi);
    if (!ok && GetLastError() == ERROR_ACCESS_DENIED) {
        ok = CreateProcessW(exe.c_str(), mutableCmd.data(), nullptr, nullptr, FALSE,
                            CREATE_NO_WINDOW, nullptr, nullptr, &si, &pi);
    }
    if (ok) {
        CloseHandle(pi.hThread);
        CloseHandle(pi.hProcess);
    }
    return ok != FALSE;
}

// UI 宿主命令管道是否已存在（非阻塞探测）：已在运行就绝不再 CreateProcess。
static bool uiHostPipeExists() {
    const std::wstring& name = uiPipeName();
    if (name.empty()) return false;
    HANDLE h = CreateFileW(name.c_str(), GENERIC_WRITE, 0, nullptr, OPEN_EXISTING, 0, nullptr);
    if (h == INVALID_HANDLE_VALUE) return false;
    CloseHandle(h);
    return true;
}

// 按需拉起 UI 宿主（5s 节流）：托盘指示器 / 设置窗都靠它。只在 ShellWorker
// 后台线程调用——CreateProcessW 出现在 TSF 宿主 UI 线程上就是卡顿源（审计 T7）。
// args 可为 "--mode 0/1"（TIP 首次激活时带上当前中英状态，UI 宿主起来后
// 直接显示正确模式）或 "--settings"。
void ensureUiHostStarted(const wchar_t* args) {
    if (uiHostPipeExists()) return;  // 已在运行：绝不重复建进程
    static DWORD lastAttempt = 0;
    const DWORD now = GetTickCount();
    if (lastAttempt != 0 && now - lastAttempt < 5000) return;
    lastAttempt = now;
    if (!uiSpawnHost(args)) {
        static volatile LONG logged = 0;
        if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
            diagPrintf(L"tsf.log", "ui host spawn failed appcontainer=%d elevated=%d\n",
                       isAppContainer() ? 1 : 0, isProcessElevated() ? 1 : 0);
        }
    }
}

}  // namespace chaoset::win
