// daemon 懒启动/连接/熔断原语（模块实现；只有 worker 线程/设置窗调用）。
// 叶子模块：不得 include/引用 TIP TU（tip.hpp / tip_*.cpp）的类型。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <shlobj.h>   // SHGetFolderPathW（%LOCALAPPDATA%）
#include <string>

#include "daemon.hpp"
#include "diagnostics.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// ---- daemon 懒启动（当前用户上下文） ----
//
// 安装器以管理员权限运行，不能可靠地替交互用户启动 daemon：LOCALAPPDATA、
// 命名管道 ACL、用户词库都可能属于提升后的管理员账户；反之亦然。
// 因此安装器只写开机 Run 项，不启动 daemon；TSF 在首次激活/首键时以当前
// 用户身份启动（-pipe，用户数据写 %LOCALAPPDATA%\Chaoset）。daemon 的
// -seeddict 负责首次从安装目录复制种子词库，不覆盖已有用户词库。
// 启动失败只记诊断日志并按英文透传降级，绝不阻塞/关闭宿主程序。

static std::wstring daemonQuote(const std::wstring& s) { return L"\"" + s + L"\""; }

// ---- daemon 懒启动（当前用户上下文） ----
//
// 安装器以管理员权限运行，不能可靠地替交互用户启动 daemon：LOCALAPPDATA、
// 命名管道 ACL、用户词库都可能属于提升后的管理员账户；反之亦然。
// 因此安装器只写开机 Run 项，不启动 daemon；TSF 在首次激活/首键时以当前
// 用户身份启动（-pipe，用户数据写 %LOCALAPPDATA%\Chaoset）。daemon 的
// -seeddict 负责首次从安装目录复制种子词库，不覆盖已有用户词库。
// 启动失败只记诊断日志并按英文透传降级，绝不阻塞/关闭宿主程序。
//
// 2026-11-06 零阻塞：本函数只允许在 ShellWorker 后台线程调用；
// TSF 线程用 ensureDaemonFast（单次非阻塞探测 + fail-open）。


// 返回 true 表示本次确实发起了 daemon 进程（CreateProcess 成功）。
bool ensureDaemonStarted() {
    // AppContainer 宿主（Windows 11 开始菜单、搜索框等）里不发起进程。
    // CreateProcessW 让子进程运行在调用者的安全上下文里；AC 拉起的 daemon
    // 会继承 AppContainer 身份（词库/日志写进包私有目录、抢同名管道）。
    if (isAppContainer()) return false;

    // 已在运行就不要 CreateProcess（审计 T8）：旧实现每次宿主激活都 spawn
    // 一个 chaosetd，子进程要先完整加载 65k 词库才发现单实例互斥体被占用
    // （实测 115/125 次全是空转）。先做一次非阻塞管道探测。
    if (daemonPipeExists()) return false;

    static volatile LONG lastAttempt = 0;
    const DWORD now = GetTickCount();
    const LONG prev = lastAttempt;
    if (prev != 0 && static_cast<DWORD>(now - static_cast<DWORD>(prev)) < 3000) return false;
    lastAttempt = static_cast<LONG>(now);

    wchar_t module[MAX_PATH] = {0};
    if (GetModuleFileNameW(g_hInst, module, MAX_PATH) == 0) return false;
    std::wstring dllDir(module);
    const size_t slash = dllDir.find_last_of(L"\\/");
    if (slash == std::wstring::npos) return false;
    dllDir.resize(slash);
    const std::wstring exe = dllDir + L"\\chaosetd.exe";
    if (GetFileAttributesW(exe.c_str()) == INVALID_FILE_ATTRIBUTES) return false;

    wchar_t appData[MAX_PATH] = {0};
    if (FAILED(SHGetFolderPathW(nullptr, CSIDL_LOCAL_APPDATA, nullptr, SHGFP_TYPE_CURRENT, appData))) return false;
    const std::wstring dataDir = std::wstring(appData) + L"\\Chaoset";
    CreateDirectoryW(dataDir.c_str(), nullptr);

    std::wstring cmd = daemonQuote(exe) + L" -pipe" +
                       L" -dict " + daemonQuote(dataDir + L"\\dict.tsv") +
                       L" -seeddict " + daemonQuote(dllDir + L"\\dict.tsv") +
                       L" -userdict " + daemonQuote(dataDir + L"\\userdict.json") +
                       L" -log " + daemonQuote(dataDir + L"\\chaosetd.log");
    std::wstring mutableCmd = cmd;
    STARTUPINFOW si{};
    si.cb = sizeof(si);
    PROCESS_INFORMATION pi{};
    // lpCurrentDirectory 传 nullptr：daemon 所有路径都是绝对路径。
    // 优先 BREAKAWAY：TSF 宿主可能位于带 kill-on-close 的 Job 对象里。
    DWORD logErr = 0;
    BOOL ok = CreateProcessW(exe.c_str(), mutableCmd.data(), nullptr, nullptr, FALSE,
                             CREATE_NO_WINDOW | CREATE_BREAKAWAY_FROM_JOB,
                             nullptr, nullptr, &si, &pi);
    if (!ok) {
        logErr = GetLastError();
        if (logErr == ERROR_ACCESS_DENIED) {
            ok = CreateProcessW(exe.c_str(), mutableCmd.data(), nullptr, nullptr, FALSE,
                                CREATE_NO_WINDOW, nullptr, nullptr, &si, &pi);
            if (!ok) logErr = GetLastError();
        }
    }
    if (ok) {
        CloseHandle(pi.hThread);
        CloseHandle(pi.hProcess);
    }
    if (diagBudget()) {
        diagPrintf(L"tsf.log", "daemon lazy start ok=%d err=%lu appcontainer=%d\n",
                   ok ? 1 : 0, ok ? 0UL : logErr, isAppContainer() ? 1 : 0);
    }
    return ok;
}

// worker 线程专用：建立/等待 daemon 连接（有界，总共约 <=1s）。
// TSF 线程绝不调用——那里走 ensureDaemonFast（零等待 + fail-open）。
bool ensureDaemonConnected(PipeTransport& transport) {
    if (transport.isConnected()) return true;
    if (transport.circuitOpen()) return false;
    ensureDaemonStarted();
    // daemon 首次加载 6.5 万词条约 100ms；刚发起时短等一轮，之后由下一次
    // 焦点/按键事件自然重试（worker 不会被每键调用）。
    for (int i = 0; i < 40; ++i) {
        transport.closeConn();
        if (transport.ensureConnected()) return true;
        if (!daemonPipeExists() && i > 5) break;
        Sleep(20);
    }
    return false;
}

// daemon 请求失败（连接还在但 session 方法返回 !ok）：每条 2s 最多记一次，
// 不随每个按键写日志。配合管道连接失败日志，可区分「根本没连上」与
// 「连上了但请求失败（超时/daemon 业务错误）」。
void diagDaemonRequestFailure(PipeTransport& transport, const char* op) {
    static DWORD last = 0;
    const DWORD now = GetTickCount();
    if (last != 0 && now - last < 2000) return;
    last = now;
    if (diagBudget()) {
        diagPrintf(L"tsf.log", "daemon request failed op=%s connected=%d\n",
                   op, transport.isConnected() ? 1 : 0);
    }
}

}  // namespace chaoset::win
