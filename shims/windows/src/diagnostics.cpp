// 诊断日志（tsf.log / crash.log）与 VEH 崩溃取证（模块实现）。
// 叶子模块：不得 include/引用 win32.cpp（TIP TU）的类型；
// tsf.log 的异步落盘入口由 TIP TU 的 DllMain 注入（见 diagnostics.hpp）。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <cstdarg>
#include <cstdio>
#include <cstring>
#include <cwchar>

#include "diagnostics.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// ---- 诊断日志（崩溃取证 + TSF 失败记录）----
//
// 2026-10-29 用户实机反馈：Windows Terminal 里输入拼音后整个进程消失，
// 开始菜单里按字母既不出搜索框也没有任何反应。前者没有故障模块名无法
// 定位，后者只能看到「键被吃掉」却看不到被哪一步吃掉。这里统一落盘：
//
//   %LOCALAPPDATA%\Chaoset\crash.log  未处理异常（VEH：异常码/地址/
//                                      模块+偏移/调用栈/最后一次 TSF 操作）
//   %LOCALAPPDATA%\Chaoset\tsf.log    TSF 调用失败与 TIP 生命周期
//                                      （宿主进程名 + AppContainer 标志）
//
// 正常输入路径不写盘；tsf.log 每进程最多 200 条，避免刷爆用户磁盘。
// 崩溃处理器只用栈缓冲与同步文件写入，不分配堆、不调用 TSF；记录完
// 返回 EXCEPTION_CONTINUE_SEARCH，WER 与宿主自己的崩溃处理不受影响。


// tsf.log 的异步落盘入口：DllMain 把 TIP TU 的 enqueueLogLine 注册进来。
// 未注册时直接丢弃，绝不回退同步写（零阻塞契约）。
static AsyncLogFn enqueueLogLine = nullptr;

void diagSetAsyncLog(AsyncLogFn fn) { enqueueLogLine = fn; }

// ---- 诊断日志（崩溃取证 + TSF 失败记录）----
//
// 2026-10-29 用户实机反馈：Windows Terminal 里输入拼音后整个进程消失，
// 开始菜单里按字母既不出搜索框也没有任何反应。前者没有故障模块名无法
// 定位，后者只能看到「键被吃掉」却看不到被哪一步吃掉。这里统一落盘：
//
//   %LOCALAPPDATA%\Chaoset\crash.log  未处理异常（VEH：异常码/地址/
//                                      模块+偏移/调用栈/最后一次 TSF 操作）
//   %LOCALAPPDATA%\Chaoset\tsf.log    TSF 调用失败与 TIP 生命周期
//                                      （宿主进程名 + AppContainer 标志）
//
// 正常输入路径不写盘；tsf.log 每进程最多 200 条，避免刷爆用户磁盘。
// 崩溃处理器只用栈缓冲与同步文件写入，不分配堆、不调用 TSF；记录完
// 返回 EXCEPTION_CONTINUE_SEARCH，WER 与宿主自己的崩溃处理不受影响。



volatile LONG g_lastOp = kOpNone;

const char* diagOpName(LONG op) {
    switch (op) {
        case kOpActivate: return "Activate";
        case kOpDeactivate: return "Deactivate";
        case kOpTestKeyDown: return "OnTestKeyDown";
        case kOpKeyDown: return "OnKeyDown";
        case kOpRender: return "render";
        case kOpPreedit: return "applyPreedit";
        case kOpCommit: return "applyCommit";
        case kOpEndComposition: return "endComposition";
        case kOpCompositionTerminated: return "OnCompositionTerminated";
        case kOpCandidateClick: return "candidateClick";
        default: return "None";
    }
}

// 16 位大写十六进制（不依赖 CRT 的 %llX，崩溃处理器里也能用）
void diagHex64(char* out, unsigned long long v) {
    static const char kDigits[] = "0123456789ABCDEF";
    for (int i = 15; i >= 0; --i) {
        out[i] = kDigits[v & 0xF];
        v >>= 4;
    }
    out[16] = '\0';
}

void diagStamp(char* out, int cap) {
    SYSTEMTIME st{};
    GetLocalTime(&st);
    snprintf(out, static_cast<size_t>(cap), "%04u-%02u-%02u %02u:%02u:%02u.%03u",
             st.wYear, st.wMonth, st.wDay, st.wHour, st.wMinute, st.wSecond, st.wMilliseconds);
}

void diagWrite(const wchar_t* name, const char* text, int len) {
    if (text == nullptr || len <= 0) return;
    wchar_t path[MAX_PATH];
    DWORD n = GetEnvironmentVariableW(L"LOCALAPPDATA", path, MAX_PATH);
    if (n == 0 || n >= MAX_PATH) return;

    const wchar_t kSub[] = L"\\Chaoset";
    const DWORD kSubLen = static_cast<DWORD>(sizeof(kSub) / sizeof(kSub[0]) - 1);
    const DWORD kNameLen = static_cast<DWORD>(wcslen(name));
    if (n + kSubLen + 1 + kNameLen + 1 > MAX_PATH) return;

    wchar_t dir[MAX_PATH];
    memcpy(dir, path, n * sizeof(wchar_t));
    memcpy(dir + n, kSub, (kSubLen + 1) * sizeof(wchar_t));
    CreateDirectoryW(dir, nullptr);

    memcpy(path + n, kSub, kSubLen * sizeof(wchar_t));
    path[n + kSubLen] = L'\\';
    memcpy(path + n + kSubLen + 1, name, (kNameLen + 1) * sizeof(wchar_t));

    // FILE_APPEND_DATA：WriteFile 始终写到文件末尾，多进程/多线程不会覆盖旧记录
    HANDLE h = CreateFileW(path, FILE_APPEND_DATA, FILE_SHARE_READ | FILE_SHARE_WRITE,
                           nullptr, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (h == INVALID_HANDLE_VALUE) {
        // AppContainer 宿主（开始菜单/搜索）里 %LOCALAPPDATA% 被系统重定向到
        // 包私有目录 \Packages\<Family>\AC，所以**第一条路径本来就落在包内**，
        // 实测 SearchApp 写的就是
        //   ...\Packages\Microsoft.Windows.Search_cw5n1h2txyewy\AC\Chaoset\tsf.log
        // 旧实现在这里再拼 \Packages\<Family>\LocalState 是错的（AC 下会得到
        // ...\AC\Packages\...），已删除；直接回退 TEMP（打包进程同样保证可写）。
        {
            // 第三兜底：TEMP。AppContainer 进程的 TEMP 被系统重定向到
            // \Packages\<Family>\LocalCache\Local\Temp，是**任何**打包进程
            // 都保证可写的少数位置之一（2026-10-30 开始菜单排障：前两条
            // 都失败时完全静默，无法区分「没激活」与「写不进」）。
            wchar_t tmp[MAX_PATH];
            const DWORD tn = GetEnvironmentVariableW(L"TEMP", tmp, MAX_PATH);
            if (tn > 0 && tn < MAX_PATH) {
                wchar_t tdir[MAX_PATH];
                if (swprintf(tdir, MAX_PATH, L"%s\\Chaoset", tmp) > 0) {
                    CreateDirectoryW(tdir, nullptr);
                    wchar_t p3[MAX_PATH];
                    if (swprintf(p3, MAX_PATH, L"%s\\%s", tdir, name) > 0) {
                        h = CreateFileW(p3, FILE_APPEND_DATA,
                                        FILE_SHARE_READ | FILE_SHARE_WRITE, nullptr,
                                        OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
                    }
                }
            }
            if (h == INVALID_HANDLE_VALUE) return;
        }
    }
    DWORD written = 0;
    WriteFile(h, text, static_cast<DWORD>(len), &written, nullptr);
    CloseHandle(h);
}

void diagPrintf(const wchar_t* name, const char* fmt, ...) {
    char stamp[40];
    diagStamp(stamp, static_cast<int>(sizeof(stamp)));
    char line[1200];
    int n = snprintf(line, sizeof(line), "[%s] ", stamp);
    if (n < 0 || n >= static_cast<int>(sizeof(line))) return;
    va_list ap;
    va_start(ap, fmt);
    int m = vsnprintf(line + n, sizeof(line) - static_cast<size_t>(n), fmt, ap);
    va_end(ap);
    if (m < 0) return;
    if (n + m >= static_cast<int>(sizeof(line))) m = static_cast<int>(sizeof(line)) - n - 1;
    // 2026-11-06 零阻塞：tsf.log 的写入统一交给 ShellWorker 后台线程。
    // 按键/焦点/钩子回调路径绝不写盘（AGENTS 契约）；worker 未启动时直接丢弃，
    // 绝不回退成同步写。crash.log 由 VEH 直接调 diagWrite（崩溃路径，保持同步）。
    if (wcscmp(name, L"tsf.log") == 0) {
        // worker 段注册的异步入口；未注册/worker 未启动时直接丢弃。
        if (enqueueLogLine != nullptr) enqueueLogLine(line, n + m);
        return;
    }
    diagWrite(name, line, n + m);
}

// tsf.log 写入预算：每进程最多 200 条，失败路径集中爆发时不会写满磁盘
bool diagBudget() {
    static volatile LONG remaining = 200;
    return InterlockedDecrement(&remaining) >= 0;
}

void diagTsfFail(const char* op, HRESULT hr) {
    if (!diagBudget()) return;
    diagPrintf(L"tsf.log", "%s failed hr=0x%08lX appcontainer=%d\n", op,
               static_cast<unsigned long>(hr), isAppContainer() ? 1 : 0);
}

void diagHostName(char* out, int cap) {
    wchar_t path[MAX_PATH] = {0};
    DWORD n = GetModuleFileNameW(nullptr, path, MAX_PATH);
    const wchar_t* base = (n > 0) ? wcsrchr(path, L'\\') : nullptr;
    if (base != nullptr) ++base;
    if (n == 0 || WideCharToMultiByte(CP_UTF8, 0, base, -1, out, cap, nullptr, nullptr) <= 0) {
        snprintf(out, static_cast<size_t>(cap), "?");
    }
}

// 地址 → 模块名 + 模块内偏移；地址不在任何模块（堆/栈/已卸载内存）时返回 false
bool diagModuleOf(const void* addr, char* module, int cap, char* offsetHex) {
    MEMORY_BASIC_INFORMATION mbi{};
    if (VirtualQuery(addr, &mbi, sizeof(mbi)) == 0 || mbi.AllocationBase == nullptr) return false;
    wchar_t full[MAX_PATH] = {0};
    if (GetModuleFileNameW(static_cast<HMODULE>(mbi.AllocationBase), full, MAX_PATH) == 0) {
        return false;
    }
    const wchar_t* base = wcsrchr(full, L'\\');
    base = (base != nullptr) ? base + 1 : full;
    if (WideCharToMultiByte(CP_UTF8, 0, base, -1, module, cap, nullptr, nullptr) <= 0) {
        return false;
    }
    const unsigned long long offset = static_cast<unsigned long long>(
        reinterpret_cast<const char*>(addr) - reinterpret_cast<const char*>(mbi.AllocationBase));
    diagHex64(offsetHex, offset);
    return true;
}

LONG CALLBACK diagCrashHandler(EXCEPTION_POINTERS* info) {
    if (info == nullptr || info->ExceptionRecord == nullptr) return EXCEPTION_CONTINUE_SEARCH;
    const DWORD code = info->ExceptionRecord->ExceptionCode;
    switch (code) {
        case EXCEPTION_BREAKPOINT:
        case EXCEPTION_SINGLE_STEP:
        case 0x40010006:  // OutputDebugStringA
        case 0x4001000A:  // OutputDebugStringW
        case 0xE06D7363:  // MSVC C++ 异常
        case 0xE0434352:  // CLR 异常
            return EXCEPTION_CONTINUE_SEARCH;
    }

    // **只记真正的错误级异常**（2026-11-05）。
    //
    // 以前这里什么都记，结果在开始菜单/搜索（AppContainer 宿主）里堆了 197 条
    // 「崩溃」，实际全是宿主自己抛出又接住的 COM/RPC 首次异常：
    //   EPT_S_NOT_REGISTERED(0x6D9)、RPC_S_SERVER_UNAVAILABLE(0x800706BA) …
    // 决定性证据：那些条目的 pid 从头到尾没变，进程一直活着在正常出候选。
    //
    // NTSTATUS 的严重性位只有 0xC0000000 档是错误（访问违例 0xC0000005、
    // 栈溢出 0xC00000FD、fail-fast 0xC0000409、页内错误 0xC0000006 …）。
    // 0x8007xxxx 是 RPC 警告级、0x00000xxx 是 Win32 状态码，都属正常控制流。
    if ((code & 0xF0000000u) != 0xC0000000u) return EXCEPTION_CONTINUE_SEARCH;

    // 同一线程重入（日志写入本身触发异常）直接放弃，避免无限递归
    static volatile LONG busy = 0;
    if (InterlockedCompareExchange(&busy, 1, 0) != 0) return EXCEPTION_CONTINUE_SEARCH;

    char stamp[40];
    diagStamp(stamp, static_cast<int>(sizeof(stamp)));
    char addrHex[17];
    diagHex64(addrHex, reinterpret_cast<unsigned long long>(info->ExceptionRecord->ExceptionAddress));
    char line[512];
    int n = snprintf(line, sizeof(line),
                     "[%s] CRASH pid=%lu tid=%lu code=0x%08lX addr=0x%s lastop=%s\n", stamp,
                     static_cast<unsigned long>(GetCurrentProcessId()),
                     static_cast<unsigned long>(GetCurrentThreadId()),
                     static_cast<unsigned long>(code), addrHex, diagOpName(g_lastOp));
    diagWrite(L"crash.log", line, n);

    char module[260];
    char offHex[17];
    if (diagModuleOf(info->ExceptionRecord->ExceptionAddress, module, static_cast<int>(sizeof(module)),
                     offHex)) {
        n = snprintf(line, sizeof(line), "  fault %s+0x%s\n", module, offHex);
        diagWrite(L"crash.log", line, n);
    }

    if (code == 0xC0000005 && info->ExceptionRecord->NumberParameters >= 2) {
        const ULONG_PTR access = info->ExceptionRecord->ExceptionInformation[0];
        const ULONG_PTR target = info->ExceptionRecord->ExceptionInformation[1];
        char targetHex[17];
        diagHex64(targetHex, static_cast<unsigned long long>(target));
        const char* kind = access == 0 ? "read" : (access == 1 ? "write"
                                                 : access == 8   ? "execute" : "other");
        n = snprintf(line, sizeof(line), "  access %s at 0x%s\n", kind, targetHex);
        diagWrite(L"crash.log", line, n);
    }

    void* frames[24];
    USHORT count = CaptureStackBackTrace(1, 24, frames, nullptr);
    for (USHORT i = 0; i < count; ++i) {
        if (!diagModuleOf(frames[i], module, static_cast<int>(sizeof(module)), offHex)) continue;
        n = snprintf(line, sizeof(line), "  #%02u %s+0x%s\n", i, module, offHex);
        diagWrite(L"crash.log", line, n);
    }
    diagWrite(L"crash.log", "----\n", 5);

    InterlockedExchange(&busy, 0);
    return EXCEPTION_CONTINUE_SEARCH;
}

// 在 Activate 里惰性注册：DllMain/DllGetClassObject 处于加载器锁内，
// 官方文档不建议在那里调用 AddVectoredExceptionHandler。
void ensureCrashHandler() {
    static volatile LONG installed = 0;
    if (InterlockedCompareExchange(&installed, 1, 0) == 0) {
        AddVectoredExceptionHandler(1, diagCrashHandler);
    }
}

}  // namespace chaoset::win
