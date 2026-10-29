// ChaosetIME TSF 壳（Windows 专用胶水层）。
//
// 职责：COM TIP 注册与激活、按键拦截（ITfKeyEventSink + ITfKeystrokeMgr）、
// 预编辑合成（ITfContextComposition）、候选上屏；引擎逻辑全部经
// core::Session 走 TCP 回环（127.0.0.1，默认 7949）与 chaosetd 通信。
// daemon 不可达时所有按键原样透传（降级策略，见 README）。
//
// 验证等级：mingw-w64 编译级 + 核心层行为级（macOS 上对真实 daemon，
// 见 tests/core_test.cpp）。COM 注册表细节与 TSF 运行时行为需在
// Windows 真机上完成联调（已知限制，README 有说明）。
//
// 构建见 build.sh（x86_64-w64-mingw32-g++）。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <wingdi.h>
#include <appmodel.h>
#include <msctf.h>
#include <objbase.h>

#include <algorithm>
#include <cmath>
#include <cstdarg>
#include <cstdio>
#include <cstring>
#include <cwchar>
#include <string>
#include <vector>

#include "core.hpp"

static HINSTANCE g_hInst = nullptr;

namespace {

// 本壳 TIP 的 COM 类标识（固定值；注册表以它为键）
const GUID CLSID_ChaosetTIP = {
    0x8a5c7b31, 0x4e2d, 0x4c6a, {0x9f, 0x41, 0x77, 0xd0, 0xb3, 0xe5, 0xc1, 0xa8}};

// 本壳语言配置标识（固定值；用于 Windows TSF 0x0804 简体中文配置）
const GUID GUID_ChaosetProfile = {
    0x5a1b3c7d, 0x8e9f, 0x4a1b, {0xa2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18, 0x29}};

// Windows TSF 核心接口与类别标识（避免不同 SDK/MinGW 版本的外部符号缺失）
const GUID kCLSID_TF_InputProcessorProfiles = {
    0x33c53a50, 0xf456, 0x4884, {0xb0, 0x49, 0x85, 0xfd, 0x64, 0x3e, 0xcf, 0xed}};
const GUID kIID_ITfInputProcessorProfiles = {
    0x1f02b6c5, 0x7842, 0x4ee6, {0x8a, 0x0b, 0x9a, 0x24, 0x18, 0x3a, 0x95, 0xca}};
const GUID kCLSID_TF_CategoryMgr = {
    0xa4b544a1, 0x438d, 0x4b41, {0x93, 0x25, 0x86, 0x95, 0x23, 0xe2, 0xd6, 0xc7}};
const GUID kIID_ITfCategoryMgr = {
    0xc3acefb5, 0xf69d, 0x4905, {0x93, 0x8f, 0xfc, 0xad, 0xcf, 0x4b, 0xe8, 0x30}};

// TSF 键盘与显示类别标识
const GUID kGUID_TFCAT_TIP_KEYBOARD = {
    0x34745c63, 0xb2f0, 0x4784, {0x8b, 0x67, 0x5e, 0x12, 0xc8, 0x70, 0x1a, 0x31}};
const GUID kGUID_TFCAT_DISPLAYATTRIBUTEPROVIDER = {
    0x046602e4, 0xc36a, 0x4f66, {0x82, 0xf7, 0xb4, 0x4d, 0x5f, 0xd8, 0x1a, 0xc4}};
// 现代 Windows (Windows 8/10/11) 沉浸式与托盘支持能力分类，确保在现代系统设置与 UWP 应用中可见
const GUID kGUID_TFCAT_TIPCAP_UIELEMENTENABLED = {
    0x49d2f9cf, 0x1f5e, 0x11d7, {0xa6, 0xd3, 0x00, 0x06, 0x5b, 0x84, 0x43, 0x5c}};
const GUID kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT = {
    0x13a016df, 0x560b, 0x46cd, {0x94, 0x7a, 0x4c, 0x3a, 0xf1, 0xe0, 0xe3, 0x5d}};
const GUID kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT = {
    0x25504fb4, 0x7bab, 0x4bc1, {0x9c, 0x69, 0xcf, 0x81, 0x89, 0x0f, 0x0e, 0xf5}};

constexpr char kDefaultTcpAddr[] = "127.0.0.1:7949";
constexpr DWORD kVkShift = 0x10;
constexpr DWORD kVkBackspace = 0x08;
constexpr DWORD kVkEscape = 0x1B;
constexpr DWORD kVkReturn = 0x0D;
constexpr DWORD kVkSpace = 0x20;
constexpr DWORD kVkPrior = 0x21;           // Page Up
constexpr DWORD kVkNext = 0x22;            // Page Down
constexpr DWORD kVkFirstLetter = 0x41;  // 'A'
constexpr DWORD kVkLastLetter = 0x5A;   // 'Z'
constexpr DWORD kVkFirstDigit = 0x31;   // '1'
constexpr DWORD kVkLastDigit = 0x39;    // '9'
// OEM 标点虚键（US 键盘布局假设；真机联调时按实际布局核验）
constexpr DWORD kVkOemSemicolon = 0xBA;      // ';'
constexpr DWORD kVkOemComma = 0xBC;          // ','
constexpr DWORD kVkOemPeriod = 0xBE;         // '.'
constexpr DWORD kVkOemOpenBrackets = 0xDB;   // '['
constexpr DWORD kVkOemBackslash = 0xDC;      // '\\'
constexpr DWORD kVkOemCloseBrackets = 0xDD;  // ']'
constexpr DWORD kVkOemQuotes = 0xDE;         // '\''
constexpr DWORD kVkOemPlus = 0xBB;           // '='（US 布局；shift 为 '+'）
constexpr DWORD kVkOemMinus = 0xBD;          // '-'
constexpr DWORD kVkOem2 = 0xBF;              // '/'（US 布局；shift 为 '?'）
constexpr DWORD kVkOem3 = 0xC0;              // '`'（US 布局；shift 为 '~'）

 volatile LONG g_liveObjects = 0;

// 当前存活的候选窗口数量。用于 DllCanUnloadNow —— 候选窗的窗口过程指向
// 本 DLL 内的代码，只要还开着窗口就不能让宿主 FreeLibrary（否则下一次
// WM_PAINT 会跳到已卸载内存，崩掉 explorer / TextInputHost）。
 volatile LONG g_liveCandidateWindows = 0;

// 存活中的设置窗口数量。设置窗口过程同样指向本 DLL 代码，开着时禁止
// 宿主 FreeLibrary（与候选窗同一防卸载机制，见 DllCanUnloadNow）。
 volatile LONG g_liveSettingsWindows = 0;

std::string envOr(const char* name, const std::string& fallback) {
    char buf[256] = {0};
    DWORD n = GetEnvironmentVariableA(name, buf, sizeof(buf));
    if (n > 0 && n < sizeof(buf)) return std::string(buf);
    return fallback;
}

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

enum DiagOp : LONG {
    kOpNone = 0,
    kOpActivate,
    kOpDeactivate,
    kOpTestKeyDown,
    kOpKeyDown,
    kOpRender,
    kOpPreedit,
    kOpCommit,
    kOpEndComposition,
    kOpCompositionTerminated,
    kOpCandidateClick,
};

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

// 本进程所属包的日志目录（仅打包进程有包身份）：
// %LOCALAPPDATA%\Packages\<Family>\LocalState\Chaoset
// AppContainer 宿主里这是唯一保证可写的位置（2026-10-29 开始菜单排障：
// 此前 tsf.log 对这些宿主完全静默，无法区分「没激活」和「写了但被拒」）。
static bool diagPackageLogDir(wchar_t* out, int cap) {
    UINT32 len = 0;
    const LONG rc = GetCurrentPackageFullName(&len, nullptr);
    if (rc != ERROR_INSUFFICIENT_BUFFER) return false;  // 非打包进程 / 无包身份
    std::wstring full(len, L'\0');
    if (GetCurrentPackageFullName(&len, full.data()) != ERROR_SUCCESS) return false;
    full.resize(len);
    // FullName: Name_Version_Arch_ResourceId_PublisherHash → Family: Name_PublisherHash
    const size_t first = full.find(L'_');
    const size_t last = full.rfind(L'_');
    if (first == std::wstring::npos || last == std::wstring::npos || last <= first) return false;
    wchar_t base[MAX_PATH];
    const DWORD n = GetEnvironmentVariableW(L"LOCALAPPDATA", base, MAX_PATH);
    if (n == 0 || n >= MAX_PATH) return false;
    const std::wstring family = full.substr(0, first) + L"_" + full.substr(last + 1);
    const int wr = swprintf(out, cap, L"%s\\Packages\\%s\\LocalState\\Chaoset", base, family.c_str());
    return wr > 0 && wr < cap;
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
        // AppContainer 宿主（开始菜单/搜索框）对 %LOCALAPPDATA% 无写权限，
        // 回退到本进程所属包的 LocalState——打包应用唯一保证可写的目录。
        // 日志会分散在各宿主的包目录下：\Packages\<Family>\LocalState\Chaoset
        wchar_t pkg[MAX_PATH];
        if (!diagPackageLogDir(pkg, MAX_PATH)) return;
        CreateDirectoryW(pkg, nullptr);
        wchar_t p2[MAX_PATH];
        if (swprintf(p2, MAX_PATH, L"%s\\%s", pkg, name) <= 0) return;
        h = CreateFileW(p2, FILE_APPEND_DATA, FILE_SHARE_READ | FILE_SHARE_WRITE,
                        nullptr, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
        if (h == INVALID_HANDLE_VALUE) return;
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

class ChaosetTIP;

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
// 类名带上模块基址：基址相同 → 旧类 WndProc 地址与新映射完全一致，复用
// 安全；基址不同 → 注册出新类，绝不复用旧映射里的函数指针。
std::wstring windowClassName(const wchar_t* base, HINSTANCE inst) {
    wchar_t buf[96] = {0};
    wsprintfW(buf, L"%s-%p", base, static_cast<void*>(inst));
    return std::wstring(buf);
}

// ---- 候选窗主题配色：与 macOS CandidatePanel.swift / skin.DefaultSkinFor 像素级一致 ----

struct CandidateColors {
    COLORREF panelBg;
    COLORREF panelBorder;
    COLORREF text;
    COLORREF highlightBg;
    COLORREF highlightText;
    COLORREF chipBg;
    COLORREF chipBorder;
    COLORREF chipDigit;
    COLORREF highlightChipBg;
    COLORREF pageColor;
    COLORREF divider;
    COLORREF chevron;

    static CandidateColors get(bool dark) {
        if (dark) {
            return {
                RGB(44, 44, 46),       // panelBg #2C2C2E
                RGB(72, 72, 74),       // panelBorder #48484A
                RGB(255, 255, 255),    // text #FFFFFF
                RGB(0, 102, 235),      // highlightBg #0066EB
                RGB(255, 255, 255),    // highlightText #FFFFFF
                RGB(90, 90, 94),       // chipBg #5A5A5E
                RGB(120, 120, 124),    // chipBorder
                RGB(255, 255, 255),    // chipDigit #FFFFFF
                RGB(69, 143, 240),     // highlightChipBg（0x45 alpha 白叠加在 #0066EB）
                RGB(152, 152, 159),    // pageColor #98989F
                RGB(63, 63, 66),       // divider
                RGB(152, 152, 159)     // chevron
            };
        }
        return {
            RGB(246, 246, 248),        // panelBg #F6F6F8
            RGB(212, 212, 216),        // panelBorder #D4D4D8
            RGB(29, 29, 31),           // text #1D1D1F（Apple 原生主文本色）
            RGB(0, 122, 255),          // highlightBg #007AFF（Apple 原生蓝胶囊）
            RGB(255, 255, 255),        // highlightText #FFFFFF
            RGB(255, 255, 255),        // chipBg #FFFFFF
            RGB(208, 208, 212),        // chipBorder
            RGB(29, 29, 31),           // chipDigit #1D1D1F
            RGB(69, 158, 255),         // highlightChipBg（0x45 alpha 白叠加在 #007AFF）
            RGB(142, 142, 147),        // pageColor #8E8E93
            RGB(226, 226, 228),        // divider
            RGB(142, 142, 147)         // chevron
        };
    }
};

// ---- 设置：词组联想开关（HKCU\Software\Chaoset，DWORD Associate）----
// 未写入时默认开启。候选栏齿轮菜单切换（参考微信输入法候选栏设置入口）；
// Linux 用 config.json、macOS 用 UserDefaults，三端语义一致。

bool RegGetAssociate() {
    HKEY hKey = nullptr;
    DWORD val = 1, sz = sizeof(val);
    if (RegOpenKeyExW(HKEY_CURRENT_USER, L"Software\\Chaoset",
                      0, KEY_READ, &hKey) == ERROR_SUCCESS) {
        RegQueryValueExW(hKey, L"Associate", nullptr, nullptr,
                         reinterpret_cast<LPBYTE>(&val), &sz);
        RegCloseKey(hKey);
    }
    return val != 0;
}

void RegSetAssociate(bool on) {
    HKEY hKey = nullptr;
    if (RegCreateKeyExW(HKEY_CURRENT_USER, L"Software\\Chaoset", 0, nullptr,
                        REG_OPTION_NON_VOLATILE, KEY_WRITE, nullptr,
                        &hKey, nullptr) == ERROR_SUCCESS) {
        DWORD v = on ? 1 : 0;
        RegSetValueExW(hKey, L"Associate", 0, REG_DWORD,
                       reinterpret_cast<const BYTE*>(&v), sizeof(v));
        RegCloseKey(hKey);
    }
}

bool isSystemDarkTheme() {
    HKEY hKey = nullptr;
    DWORD val = 1;
    DWORD sz = sizeof(val);
    if (RegOpenKeyExW(HKEY_CURRENT_USER,
                      L"Software\\Microsoft\\Windows\\CurrentVersion\\Themes\\Personalize",
                      0, KEY_READ, &hKey) == ERROR_SUCCESS) {
        RegQueryValueExW(hKey, L"AppsUseLightTheme", nullptr, nullptr, reinterpret_cast<LPBYTE>(&val), &sz);
        RegCloseKey(hKey);
    }
    return val == 0;
}

// ---- 候选词悬浮窗：双缓冲原生 GDI 渲染、DPI 感知、光标跟随、圆角胶囊与 macOS 视觉一致 ----

// 圆头几何笔（齿轮辐条用）。ExtCreatePen 的 plbrush 是必填的
// LOGBRUSH*：gdi32 在用户态先读 lbStyle/lbColor 再进内核
// （ReactOS win32ss/gdi/gdi32/objects/brush.c 同构），传 nullptr 就是
// 空指针解引用。TSF DLL 在宿主进程内，这里崩 = 宿主程序直接退出。
// 2026-10-29 Windows 候选窗每次打字只画出空框后进程崩溃的根因。
// 笔创建失败时退回普通实心笔：样式可以差一点，空句柄绝不能 SelectObject。
HPEN createRoundCapPen(int width, COLORREF color) {
    LOGBRUSH lb{};
    lb.lbStyle = BS_SOLID;
    lb.lbColor = color;
    lb.lbHatch = 0;
    const DWORD w = static_cast<DWORD>(width > 0 ? width : 1);
    HPEN pen = ExtCreatePen(PS_GEOMETRIC | PS_ENDCAP_ROUND, w, &lb, 0, nullptr);
    if (pen == nullptr) {
        pen = CreatePen(PS_SOLID, static_cast<int>(w), color);
    }
    return pen;
}

class CandidateWindow {
public:
    CandidateWindow() = default;
    ~CandidateWindow() { destroy(); }

    CandidateWindow(const CandidateWindow&) = delete;
    CandidateWindow& operator=(const CandidateWindow&) = delete;

    HWND hwnd() const { return hwnd_; }

    void show(HINSTANCE hinst, POINT pt, int lineH, const chaoset::Comp& comp, ChaosetTIP* tip) {
        if (comp.candidates.empty()) {
            hide();
            return;
        }
        comp_ = comp;
        tip_ = tip;
        targetPos_ = pt;
        targetLineHeight_ = lineH;
        ensureWindow(hinst);
        if (hwnd_ == nullptr) return;

        layoutAndPosition();
        InvalidateRect(hwnd_, nullptr, TRUE);
        ShowWindow(hwnd_, SW_SHOWNOACTIVATE);
    }

    void hide() {
        if (hwnd_ != nullptr && IsWindow(hwnd_)) {
            ShowWindow(hwnd_, SW_HIDE);
        }
    }

    void destroy() {
        // 即使窗口句柄已失效（IsWindow == FALSE）也要清空成员并归还计数：
        // 此前只在 IsWindow 为真时才处理，句柄一旦失效 g_liveCandidateWindows
        // 就永远不归零，DllCanUnloadNow 永远返回 S_FALSE。
        if (hwnd_ != nullptr) {
            if (IsWindow(hwnd_)) DestroyWindow(hwnd_);
            hwnd_ = nullptr;
            // 窗口过程已失效，此时才允许 DLL 被卸载（见 DllCanUnloadNow）
            InterlockedDecrement(&g_liveCandidateWindows);
        }
        // 清掉裸指针，避免窗口已销毁却仍被引用（审查指出的悬垂风险）
        tip_ = nullptr;
    }

    void onLButtonDown(int x, int y);

private:
    struct ItemPos {
        int index = 0;
        std::wstring indexStr;
        std::wstring textStr;
        RECT itemRect{};
        RECT pillRect{};
        RECT chipRect{};
        RECT textRect{};
        bool isFirst = false;
    };

    static int getDpi() {
        HDC hdc = GetDC(nullptr);
        int dpi = 96;
        if (hdc != nullptr) {
            dpi = GetDeviceCaps(hdc, LOGPIXELSY);
            ReleaseDC(nullptr, hdc);
        }
        return dpi;
    }

    static int scale(int px, int dpi) {
        return MulDiv(px, dpi, 96);
    }

    bool isInteractiveArea() const {
        POINT pt;
        if (GetCursorPos(&pt) && hwnd_ != nullptr) {
            ScreenToClient(hwnd_, &pt);
            for (const auto& it : items_) {
                if (PtInRect(&it.itemRect, pt)) return true;
            }
            if (comp_.pageCount > 1 && PtInRect(&chevronRect_, pt)) return true;
        }
        return false;
    }

    void ensureWindow(HINSTANCE hinst) {
        if (hwnd_ != nullptr && IsWindow(hwnd_)) return;

        const std::wstring cls = windowClassName(L"ChaosetCandidateWndClass", hinst);
        WNDCLASSEXW wc{};
        wc.cbSize = sizeof(wc);
        wc.style = CS_DROPSHADOW | CS_HREDRAW | CS_VREDRAW;
        wc.lpfnWndProc = CandidateWndProc;
        wc.hInstance = hinst;
        wc.hCursor = LoadCursorW(nullptr, MAKEINTRESOURCEW(32512));
        wc.lpszClassName = cls.c_str();
        RegisterClassExW(&wc);

        // 层级设计（三端对照，2026-10-28 复核）：候选窗保持 TOPMOST——
        // 微软自带拼音与搜狗等输入法的候选窗同样是置顶 popup。Windows 的
        // 下拉菜单是系统模态的 topmost popup，弹出时位于 Z 序最顶端且
        // 独占鼠标（模态菜单循环），候选窗 frame 即使重叠也不会拦截菜单
        // 点击——与 macOS 不同（NSPanel.popUpMenu 层级会真的压住菜单栏
        // 拦截点击，macOS 端因此降级为 floating）。去掉 TOPMOST 会让
        // 候选窗被宿主文档窗口盖住，不可行。
        // 全链路防拦截：WS_EX_NOACTIVATE 不抢焦点 + 失焦即收候选窗
        // （OnSetFocus(FALSE)），点击其它应用时候选窗先收起再切换。
        hwnd_ = CreateWindowExW(
            WS_EX_TOOLWINDOW | WS_EX_TOPMOST | WS_EX_NOACTIVATE,
            cls.c_str(),
            L"ChaosetCandidate",
            WS_POPUP,
            0, 0, 0, 0,
            nullptr, nullptr, hinst, nullptr
        );
        if (hwnd_ != nullptr) {
            SetWindowLongPtrW(hwnd_, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(this));
            // 窗口过程现在指向本 DLL 的代码：只要它还活着，DLL 就不能被卸载
            InterlockedIncrement(&g_liveCandidateWindows);
        }
    }

    static LRESULT CALLBACK CandidateWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
        CandidateWindow* self = reinterpret_cast<CandidateWindow*>(GetWindowLongPtrW(hwnd, GWLP_USERDATA));
        switch (msg) {
            case WM_MOUSEACTIVATE:
                return MA_NOACTIVATE;
            case WM_ERASEBKGND:
                return 1;
            case WM_PAINT:
                if (self != nullptr) {
                    self->onPaint(hwnd);
                    return 0;
                }
                break;
            case WM_LBUTTONDOWN:
                if (self != nullptr) {
                    self->onLButtonDown(static_cast<short>(LOWORD(lParam)), static_cast<short>(HIWORD(lParam)));
                    return 0;
                }
                break;
            case WM_SETCURSOR:
                if (self != nullptr && self->isInteractiveArea()) {
                    SetCursor(LoadCursorW(nullptr, MAKEINTRESOURCEW(32649)));  // IDC_HAND
                    return TRUE;
                }
                break;
            case WM_DESTROY:
                break;
        }
        return DefWindowProcW(hwnd, msg, wParam, lParam);
    }

    void layoutAndPosition() {
        int dpi = getDpi();
        int fontH = -MulDiv(15, dpi, 72);
        int idxFontH = -MulDiv(12, dpi, 72);
        HFONT fontNormal = CreateFontW(fontH, 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
                                       DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                       CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
        HFONT fontIndex = CreateFontW(idxFontH, 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
                                      DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                      CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");

        HDC screenDC = GetDC(nullptr);
        HDC memDC = CreateCompatibleDC(screenDC);
        HGDIOBJ oldFont = SelectObject(memDC, fontNormal);

        // 排版参数（与 macOS Layout 保持 1:1 像素级对齐）
        int pad = scale(7, dpi);
        int firstPadLeft = scale(9, dpi);
        int itemPadX = scale(7, dpi);
        int gap = scale(11, dpi);
        int chipPadX = scale(4, dpi);
        int chipPadY = scale(2, dpi);
        int pillMargin = scale(2, dpi);
        int chevronBoxW = scale(36, dpi);

        TEXTMETRICW tmText{}, tmIdx{};
        SelectObject(memDC, fontNormal);
        GetTextMetricsW(memDC, &tmText);
        SelectObject(memDC, fontIndex);
        GetTextMetricsW(memDC, &tmIdx);

        int textH = (std::max)(static_cast<int>(tmText.tmHeight), scale(22, dpi));
        int idxH = tmIdx.tmHeight;
        int chipH = idxH + chipPadY * 2;
        int contentH = (std::max)(textH, chipH);
        int winH = pad * 2 + contentH;
        int contentTop = pad;

        items_.clear();
        int curX = pad;

        // 面板最大内容宽度（对齐 macOS Layout.maxWidth=720pt）：单条长候选
        // 此前可把面板撑出屏幕，右侧内容被裁掉且点不到（2026-10-27 审查）。
        // 超宽的候选项不再排列（仍在候选列表里，可翻页查看）；首项独超宽时
        // 逐字截断加省略号，保证至少能显示一条。
        const int maxContent = scale(720, dpi) - pad * 2 - chevronBoxW;
        const wchar_t* kEllipsis = L"…";

        for (std::size_t i = 0; i < comp_.candidates.size(); ++i) {
            ItemPos item;
            item.index = static_cast<int>(i);
            item.isFirst = (i == 0);
            item.indexStr = std::to_wstring(i + 1);
            item.textStr = toWide(comp_.candidates[i].text);

            int padX = item.isFirst ? firstPadLeft : itemPadX;

            SelectObject(memDC, fontIndex);
            SIZE szIdx{};
            GetTextExtentPoint32W(memDC, item.indexStr.c_str(), static_cast<int>(item.indexStr.size()), &szIdx);

            SelectObject(memDC, fontNormal);
            SIZE szTxt{};
            GetTextExtentPoint32W(memDC, item.textStr.c_str(), static_cast<int>(item.textStr.size()), &szTxt);

            int chipW = szIdx.cx + chipPadX * 2;
            int chipTextGap = (std::max)(scale(3, dpi), static_cast<int>(szIdx.cx * 45 / 100));
            int itemW = padX * 2 + chipW + chipTextGap + szTxt.cx;

            if (items_.empty()) {
                // 首项独超宽：逐字截断加省略号（保证面板不出屏）
                if (curX + itemW + chevronBoxW - gap > maxContent + pad) {
                    const int avail = (std::max)(maxContent - padX * 2 - chipW - chipTextGap - chevronBoxW - scale(8, dpi), scale(20, dpi));
                    SIZE szEll{};
                    SelectObject(memDC, fontNormal);
                    GetTextExtentPoint32W(memDC, kEllipsis, 1, &szEll);
                    while (item.textStr.size() > 1) {
                        item.textStr.resize(item.textStr.size() - 1);
                        GetTextExtentPoint32W(memDC, item.textStr.c_str(), static_cast<int>(item.textStr.size()), &szTxt);
                        if (szTxt.cx + szEll.cx <= avail) break;
                    }
                    item.textStr += kEllipsis;
                    GetTextExtentPoint32W(memDC, item.textStr.c_str(), static_cast<int>(item.textStr.size()), &szTxt);
                    itemW = padX * 2 + chipW + chipTextGap + szTxt.cx;
                }
            } else if (curX - pad + itemW > maxContent) {
                // 后续项放不下：停止排列（候选仍在列表中，数字键可选）
                break;
            }

            item.itemRect.left = curX;
            item.itemRect.top = contentTop;
            item.itemRect.right = curX + itemW;
            item.itemRect.bottom = contentTop + contentH;

            if (item.isFirst) {
                item.pillRect.left = curX - pillMargin;
                item.pillRect.top = contentTop - pillMargin;
                item.pillRect.right = curX + itemW + pillMargin;
                item.pillRect.bottom = contentTop + contentH + pillMargin;
            }

            int chipLeft = curX + padX;
            int chipTop = contentTop + (contentH - chipH) / 2;
            item.chipRect.left = chipLeft;
            item.chipRect.top = chipTop;
            item.chipRect.right = chipLeft + chipW;
            item.chipRect.bottom = chipTop + chipH;

            int textLeft = item.chipRect.right + chipTextGap;
            int textTop = contentTop + (contentH - tmText.tmHeight) / 2;
            item.textRect.left = textLeft;
            item.textRect.top = textTop;
            item.textRect.right = textLeft + szTxt.cx;
            item.textRect.bottom = textTop + tmText.tmHeight;

            items_.push_back(item);
            curX += itemW + gap;
        }

        // 设置齿轮区（参考微信输入法候选栏右侧的设置入口）：折叠区左侧
        // 20px 齿轮，点击弹设置菜单（词组联想开关）。
        int gearBoxW = scale(26, dpi);
        int totalW = (curX > pad ? curX - gap : curX) + pad + chevronBoxW + gearBoxW;
        int winW = (std::max)(totalW, scale(120, dpi));

        chevronRect_.left = winW - chevronBoxW;
        chevronRect_.top = 0;
        chevronRect_.right = winW;
        chevronRect_.bottom = winH;

        settingsRect_.left = winW - chevronBoxW - gearBoxW;
        settingsRect_.top = 0;
        settingsRect_.right = winW - chevronBoxW;
        settingsRect_.bottom = winH;

        dividerX_ = settingsRect_.left + scale(2, dpi);

        SelectObject(memDC, oldFont);
        DeleteDC(memDC);
        ReleaseDC(nullptr, screenDC);
        DeleteObject(fontNormal);
        DeleteObject(fontIndex);

        POINT pt = targetPos_;
        int lineH = targetLineHeight_ > 0 ? targetLineHeight_ : scale(20, dpi);
        int winX = pt.x;
        int winY = pt.y + scale(4, dpi);

        HMONITOR hMon = MonitorFromPoint(pt, MONITOR_DEFAULTTONEAREST);
        MONITORINFO mi{};
        mi.cbSize = sizeof(mi);
        if (GetMonitorInfoW(hMon, &mi)) {
            RECT work = mi.rcWork;
            if (winX + winW > work.right) {
                winX = work.right - winW - scale(4, dpi);
            }
            if (winX < work.left) {
                winX = work.left + scale(4, dpi);
            }
            if (winY + winH > work.bottom) {
                winY = pt.y - lineH - winH - scale(4, dpi);
                if (winY < work.top) {
                    winY = work.top + scale(4, dpi);
                }
            }
        }

        SetWindowRgn(hwnd_, CreateRoundRectRgn(0, 0, winW + 1, winH + 1, scale(16, dpi), scale(16, dpi)), TRUE);
        SetWindowPos(hwnd_, HWND_TOPMOST, winX, winY, winW, winH,
                     SWP_NOACTIVATE | SWP_SHOWWINDOW);
    }

    void onPaint(HWND hwnd) {
        PAINTSTRUCT ps;
        HDC hdc = BeginPaint(hwnd, &ps);
        RECT clientRc;
        GetClientRect(hwnd, &clientRc);
        int w = clientRc.right - clientRc.left;
        int h = clientRc.bottom - clientRc.top;
        if (w <= 0 || h <= 0) {
            EndPaint(hwnd, &ps);
            return;
        }

        int dpi = getDpi();
        bool dark = isSystemDarkTheme();
        CandidateColors theme = CandidateColors::get(dark);

        HDC memDC = CreateCompatibleDC(hdc);
        HBITMAP memBmp = CreateCompatibleBitmap(hdc, w, h);
        HGDIOBJ oldBmp = SelectObject(memDC, memBmp);

        // 面板背景圆角与 1px 细边框
        HBRUSH bgBrush = CreateSolidBrush(theme.panelBg);
        HPEN borderPen = CreatePen(PS_SOLID, 1, theme.panelBorder);
        HGDIOBJ oldBrush = SelectObject(memDC, bgBrush);
        HGDIOBJ oldPen = SelectObject(memDC, borderPen);
        RoundRect(memDC, 0, 0, w, h, scale(16, dpi), scale(16, dpi));

        SetBkMode(memDC, TRANSPARENT);

        int fontH = -MulDiv(15, dpi, 72);
        int idxFontH = -MulDiv(12, dpi, 72);
        HFONT fontNormal = CreateFontW(fontH, 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
                                       DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                       CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
        HFONT fontIndex = CreateFontW(idxFontH, 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
                                      DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                      CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
        HGDIOBJ oldFont = SelectObject(memDC, fontNormal);

        // 绘制候选列表
        for (const auto& item : items_) {
            if (item.isFirst) {
                // 首选：Apple 风格高亮蓝胶囊
                // GDI 对象必须先摘出 DC 再 DeleteObject：仍被 DC 持有期间
                // DeleteObject 会失败并泄漏句柄，候选窗每次重绘都漏一个。
                HBRUSH pillBrush = CreateSolidBrush(theme.highlightBg);
                HGDIOBJ prevBrush = SelectObject(memDC, pillBrush);
                HGDIOBJ prevPen = SelectObject(memDC, GetStockObject(NULL_PEN));
                RoundRect(memDC, item.pillRect.left, item.pillRect.top,
                          item.pillRect.right, item.pillRect.bottom,
                          scale(12, dpi), scale(12, dpi));
                SelectObject(memDC, prevBrush);
                SelectObject(memDC, prevPen);
                DeleteObject(pillBrush);

                // 蓝胶囊内键帽：半透白单层填充，无边框
                HBRUSH chipBrush = CreateSolidBrush(theme.highlightChipBg);
                prevBrush = SelectObject(memDC, chipBrush);
                prevPen = SelectObject(memDC, GetStockObject(NULL_PEN));
                RoundRect(memDC, item.chipRect.left, item.chipRect.top,
                          item.chipRect.right, item.chipRect.bottom,
                          scale(6, dpi), scale(6, dpi));
                SelectObject(memDC, prevBrush);
                SelectObject(memDC, prevPen);
                DeleteObject(chipBrush);

                // 数字：白色
                SetTextColor(memDC, theme.highlightText);
                SelectObject(memDC, fontIndex);
                DrawTextW(memDC, item.indexStr.c_str(), -1, const_cast<RECT*>(&item.chipRect),
                          DT_CENTER | DT_VCENTER | DT_SINGLELINE);

                // 首选文本：白色
                SetTextColor(memDC, theme.highlightText);
                SelectObject(memDC, fontNormal);
                DrawTextW(memDC, item.textStr.c_str(), -1, const_cast<RECT*>(&item.textRect),
                          DT_LEFT | DT_VCENTER | DT_SINGLELINE);
            } else {
                // 次选键帽：外边框 + 内部底色
                HBRUSH chipBrush = CreateSolidBrush(theme.chipBg);
                HPEN chipPen = CreatePen(PS_SOLID, 1, theme.chipBorder);
                HGDIOBJ prevBrush = SelectObject(memDC, chipBrush);
                HGDIOBJ prevPen = chipPen != nullptr ? SelectObject(memDC, chipPen) : nullptr;
                RoundRect(memDC, item.chipRect.left, item.chipRect.top,
                          item.chipRect.right, item.chipRect.bottom,
                          scale(6, dpi), scale(6, dpi));
                SelectObject(memDC, prevBrush);
                if (prevPen != nullptr) SelectObject(memDC, prevPen);
                DeleteObject(chipBrush);
                if (chipPen != nullptr) DeleteObject(chipPen);

                // 数字：深色 (深色模式下为白色)
                SetTextColor(memDC, theme.chipDigit);
                SelectObject(memDC, fontIndex);
                DrawTextW(memDC, item.indexStr.c_str(), -1, const_cast<RECT*>(&item.chipRect),
                          DT_CENTER | DT_VCENTER | DT_SINGLELINE);

                // 候选文本：深色 (深色模式下为白色)
                SetTextColor(memDC, theme.text);
                SelectObject(memDC, fontNormal);
                DrawTextW(memDC, item.textStr.c_str(), -1, const_cast<RECT*>(&item.textRect),
                          DT_LEFT | DT_VCENTER | DT_SINGLELINE);
            }
        }

        // 绘制右侧折叠区：垂直分割线 + 翻页箭头 chevron + 页码
        int divH = h * 55 / 100;
        int divY = (h - divH) / 2;
        HPEN divPen = CreatePen(PS_SOLID, 1, theme.divider);
        HGDIOBJ prevPen = divPen != nullptr ? SelectObject(memDC, divPen) : nullptr;
        MoveToEx(memDC, dividerX_, divY, nullptr);
        LineTo(memDC, dividerX_, divY + divH);
        if (prevPen != nullptr) SelectObject(memDC, prevPen);
        if (divPen != nullptr) DeleteObject(divPen);

        // 设置齿轮（分割线右侧、翻页箭头左侧）：**与 macOS 端完全相同的
        // 矢量算法**（中心圆环 r3.4/线宽1.8 + 8 齿辐条 4.6→7.2/线宽2
        // 圆头线帽——几何参数与色值逐一对应，三端 UI 一模一样，
        // 2026-10-28 用户拍板）。颜色同 mac chipDigit：暗色纯白/
        // 浅色 #1D1D1F，固定不变。GDI 几何笔（ExtCreatePen
        // PS_GEOMETRIC）才有圆头线帽与真实线宽。
        {
            const bool dark = isSystemDarkTheme();
            const COLORREF gearColor = dark ? RGB(0xFF, 0xFF, 0xFF) : RGB(0x1D, 0x1D, 0x1F);
            const double u = dpi / 96.0; // mac 端 pt 值按 1x 换算
            const double cx = (settingsRect_.left + settingsRect_.right) / 2.0;
            const double cy = h / 2.0;
            // 中心圆环：NULL_BRUSH 只描边不填充
            HBRUSH ringBrush = static_cast<HBRUSH>(GetStockObject(NULL_BRUSH));
            HPEN ringPen = CreatePen(PS_SOLID, static_cast<int>(1.8 * u + 0.5), gearColor);
            HGDIOBJ oldBrush = SelectObject(memDC, ringBrush);
            HGDIOBJ oldPen = ringPen != nullptr ? SelectObject(memDC, ringPen) : nullptr;
            const double rr = 3.4 * u;
            Ellipse(memDC,
                    static_cast<int>(cx - rr + 0.5), static_cast<int>(cy - rr + 0.5),
                    static_cast<int>(cx + rr + 0.5), static_cast<int>(cy + rr + 0.5));
            // 8 齿辐条：PS_GEOMETRIC 圆头线帽
            HPEN spokePen = createRoundCapPen(static_cast<int>(2 * u + 0.5), gearColor);
            if (spokePen != nullptr) {
                SelectObject(memDC, spokePen);
                for (int i = 0; i < 8; ++i) {
                    const double a = i * 3.14159265358979 / 4.0;
                    const double dx = cos(a), dy = sin(a);
                    MoveToEx(memDC,
                             static_cast<int>(cx + dx * 4.6 * u + 0.5),
                             static_cast<int>(cy + dy * 4.6 * u + 0.5), nullptr);
                    LineTo(memDC,
                           static_cast<int>(cx + dx * 7.2 * u + 0.5),
                           static_cast<int>(cy + dy * 7.2 * u + 0.5));
                }
            }
            SelectObject(memDC, oldBrush);
            if (oldPen != nullptr) SelectObject(memDC, oldPen);
            if (ringPen != nullptr) DeleteObject(ringPen);
            if (spokePen != nullptr) DeleteObject(spokePen);
        }

        // 翻页箭头
        int chevX = settingsRect_.right + (w - settingsRect_.right) / 2;
        bool hasNext = comp_.page + 1 < comp_.pageCount;
        bool hasPrev = comp_.page > 0;
        bool dirDown = hasNext || !hasPrev;
        int arm = scale(3, dpi);
        int chevY = (comp_.pageCount > 1) ? (h / 2 - scale(4, dpi)) : (h / 2);

        HPEN chevPen = CreatePen(PS_SOLID, scale(1, dpi) < 2 ? 1 : 2, theme.chevron);
        prevPen = chevPen != nullptr ? SelectObject(memDC, chevPen) : nullptr;
        if (dirDown) {
            MoveToEx(memDC, chevX - arm, chevY - scale(2, dpi), nullptr);
            LineTo(memDC, chevX, chevY + scale(2, dpi));
            LineTo(memDC, chevX + arm + 1, chevY - scale(2, dpi) - 1);
        } else {
            MoveToEx(memDC, chevX - arm, chevY + scale(2, dpi), nullptr);
            LineTo(memDC, chevX, chevY - scale(2, dpi));
            LineTo(memDC, chevX + arm + 1, chevY + scale(2, dpi) + 1);
        }
        if (prevPen != nullptr) SelectObject(memDC, prevPen);
        if (chevPen != nullptr) DeleteObject(chevPen);

        // 页码指示（多页时居中显示于 chevron 下方）
        if (comp_.pageCount > 1) {
            std::wstring pageStr = std::to_wstring(comp_.page + 1) + L"/" + std::to_wstring(comp_.pageCount);
            RECT pageRc{ dividerX_, chevY + scale(3, dpi), w, h };
            SetTextColor(memDC, theme.pageColor);
            SelectObject(memDC, fontIndex);
            DrawTextW(memDC, pageStr.c_str(), -1, &pageRc, DT_CENTER | DT_TOP | DT_SINGLELINE);
        }

        BitBlt(hdc, 0, 0, w, h, memDC, 0, 0, SRCCOPY);

        SelectObject(memDC, oldFont);
        DeleteObject(fontNormal);
        DeleteObject(fontIndex);
        SelectObject(memDC, oldBrush);
        DeleteObject(bgBrush);
        SelectObject(memDC, oldPen);
        DeleteObject(borderPen);
        SelectObject(memDC, oldBmp);
        DeleteObject(memBmp);
        DeleteDC(memDC);

        EndPaint(hwnd, &ps);
    }

    HWND hwnd_ = nullptr;
    chaoset::Comp comp_;
    ChaosetTIP* tip_ = nullptr;
    POINT targetPos_{0, 0};
    int targetLineHeight_ = 20;

    std::vector<ItemPos> items_;
    RECT chevronRect_{};
    int dividerX_ = 0;
    RECT settingsRect_{}; // 设置齿轮命中区
};

// ---- 传输：TCP 回环（与 daemon -tcp 127.0.0.1:7949 配对） ----

class TcpTransport : public chaoset::Transport {
public:
    TcpTransport() {
        WSADATA data{};
        WSAStartup(MAKEWORD(2, 2), &data);
        parseAddr();
    }
    ~TcpTransport() override {
        closeConn();
        WSACleanup();
    }

    bool isConnected() const { return sock_ != INVALID_SOCKET; }

    bool ensureConnected() {
        if (sock_ != INVALID_SOCKET) return true;
        // AppContainer 宿主（开始菜单 / 搜索框等）默认禁止回环连接，连接会
        // 被立即拒绝 → 走下方失败冷却 2 秒后放行降级，绝不阻塞系统 UI 线程；
        // 安装器已对这些宿主授予 CheckNetIsolation 回环豁免，豁免后这里就是
        // 普通连接路径，拼音在开始菜单里即可正常转换（2026-10-29 实机反馈）。
        DWORD now = GetTickCount();
        if (now - lastFail_ < 2000) return false;  // 失败冷却 2 秒，避免频繁重试阻塞

        SOCKET s = INVALID_SOCKET;
        unsigned long ip = inet_addr(host_.c_str());
        if (ip != INADDR_NONE) {
            s = ::socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
            if (s != INVALID_SOCKET) {
                // 设置为非阻塞模式，确保 connect 绝不在 UI 线程上死等挂起
                u_long nonblocking = 1;
                ::ioctlsocket(s, FIONBIO, &nonblocking);

                int nodelay = 1;
                ::setsockopt(s, IPPROTO_TCP, TCP_NODELAY, reinterpret_cast<const char*>(&nodelay), sizeof(nodelay));

                sockaddr_in sa{};
                sa.sin_family = AF_INET;
                sa.sin_port = htons(static_cast<u_short>(port_));
                sa.sin_addr.s_addr = ip;

                int res = ::connect(s, reinterpret_cast<sockaddr*>(&sa), sizeof(sa));
                if (res == SOCKET_ERROR) {
                    int err = WSAGetLastError();
                    if (err == WSAEWOULDBLOCK) {
                        fd_set writefds, exceptfds;
                        FD_ZERO(&writefds);
                        FD_ZERO(&exceptfds);
                        FD_SET(s, &writefds);
                        FD_SET(s, &exceptfds);

                        // 本地回环连接通常 < 0.1ms；设置 20ms 超短超时，绝不卡顿系统输入法切换或窗口事件
                        timeval tv{0, 20000};
                        int sel = ::select(0, nullptr, &writefds, &exceptfds, &tv);
                        if (sel > 0 && FD_ISSET(s, &writefds) && !FD_ISSET(s, &exceptfds)) {
                            int sockErr = 0;
                            int optLen = sizeof(sockErr);
                            if (::getsockopt(s, SOL_SOCKET, SO_ERROR, reinterpret_cast<char*>(&sockErr), &optLen) == 0 && sockErr == 0) {
                                // 连上：恢复阻塞模式，设置 200ms 的收发超时
                                u_long blocking = 0;
                                ::ioctlsocket(s, FIONBIO, &blocking);
                                DWORD timeout = 200;
                                ::setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                                ::setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                                sock_ = s;
                                return true;
                            }
                        }
                    }
                    ::closesocket(s);
                    s = INVALID_SOCKET;
                } else if (res == 0) {
                    u_long blocking = 0;
                    ::ioctlsocket(s, FIONBIO, &blocking);
                    DWORD timeout = 200;
                    ::setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                    ::setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                    sock_ = s;
                    return true;
                }
            }
        } else {
            // 域名回退路径（如 localhost 等）
            addrinfoW hints{};
            hints.ai_family = AF_INET;
            hints.ai_socktype = SOCK_STREAM;
            addrinfoW* res = nullptr;
            std::wstring hostW = toWide(host_);
            std::wstring portW = toWide(std::to_string(port_));
            if (GetAddrInfoW(hostW.c_str(), portW.c_str(), &hints, &res) == 0) {
                for (addrinfoW* p = res; p != nullptr; p = p->ai_next) {
                    s = ::socket(p->ai_family, p->ai_socktype, p->ai_protocol);
                    if (s == INVALID_SOCKET) continue;

                    u_long nonblocking = 1;
                    ::ioctlsocket(s, FIONBIO, &nonblocking);

                    int nodelay = 1;
                    ::setsockopt(s, IPPROTO_TCP, TCP_NODELAY, reinterpret_cast<const char*>(&nodelay), sizeof(nodelay));

                    int cRes = ::connect(s, p->ai_addr, static_cast<int>(p->ai_addrlen));
                    if (cRes == SOCKET_ERROR) {
                        int err = WSAGetLastError();
                        if (err == WSAEWOULDBLOCK) {
                            fd_set writefds, exceptfds;
                            FD_ZERO(&writefds);
                            FD_ZERO(&exceptfds);
                            FD_SET(s, &writefds);
                            FD_SET(s, &exceptfds);

                            timeval tv{0, 20000};
                            int sel = ::select(0, nullptr, &writefds, &exceptfds, &tv);
                            if (sel > 0 && FD_ISSET(s, &writefds) && !FD_ISSET(s, &exceptfds)) {
                                int sockErr = 0;
                                int optLen = sizeof(sockErr);
                                if (::getsockopt(s, SOL_SOCKET, SO_ERROR, reinterpret_cast<char*>(&sockErr), &optLen) == 0 && sockErr == 0) {
                                    u_long blocking = 0;
                                    ::ioctlsocket(s, FIONBIO, &blocking);
                                    DWORD timeout = 200;
                                    ::setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                                    ::setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                                    break;
                                }
                            }
                        }
                        ::closesocket(s);
                        s = INVALID_SOCKET;
                    } else if (cRes == 0) {
                        u_long blocking = 0;
                        ::ioctlsocket(s, FIONBIO, &blocking);
                        DWORD timeout = 200;
                        ::setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                        ::setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                        break;
                    }
                }
                FreeAddrInfoW(res);
            }
        }

        if (s == INVALID_SOCKET) {
            lastFail_ = now;
            // 每进程只记一次：区分「宿主没激活 TIP」（无任何日志）与
            // 「激活了但连不上 daemon」（回环豁免未生效 / LPAC 拒绝）
            static volatile LONG failLogged = 0;
            if (InterlockedCompareExchange(&failLogged, 1, 0) == 0 && diagBudget()) {
                char host[260];
                diagHostName(host, static_cast<int>(sizeof(host)));
                diagPrintf(L"tsf.log", "connect failed host=%s addr=%s:%d appcontainer=%d\n",
                           host, host_.c_str(), port_, isAppContainer() ? 1 : 0);
            }
            return false;
        }
        sock_ = s;
        return true;
    }

    bool writeAll(const std::uint8_t* data, std::size_t len) override {
        if (!ensureConnected()) return false;
        std::size_t off = 0;
        while (off < len) {
            int n = ::send(sock_, reinterpret_cast<const char*>(data + off),
                           static_cast<int>(len - off), 0);
            if (n <= 0) {
                failConn();
                return false;
            }
            off += static_cast<std::size_t>(n);
        }
        return true;
    }

    bool readAll(std::uint8_t* buf, std::size_t len) override {
        std::size_t off = 0;
        while (off < len) {
            int n = ::recv(sock_, reinterpret_cast<char*>(buf + off),
                           static_cast<int>(len - off), 0);
            if (n <= 0) {
                failConn();
                return false;
            }
            off += static_cast<std::size_t>(n);
        }
        return true;
    }

    void closeConn() override {
        if (sock_ != INVALID_SOCKET) {
            ::closesocket(sock_);
            sock_ = INVALID_SOCKET;
        }
        lastFail_ = 0;  // 主动断开（非失败）不进入冷却
    }

private:
    // 收发失败断开：与主动断开不同，进入 2s 冷却——daemon 半死（连接可建
    // 但响应超时）时，此前每次失败都清零冷却，下一键又全量重连+超时，宿主
    // UI 线程每键被阻塞最多 ~400ms；进入冷却后后续按键直接降级放行，
    // 只有下一轮冷却过期才重试（2026-10-27 审查发现）。
    void failConn() {
        if (sock_ != INVALID_SOCKET) {
            ::closesocket(sock_);
            sock_ = INVALID_SOCKET;
        }
        lastFail_ = GetTickCount();
    }

    void parseAddr() {
        std::string a = envOr("CHAOSET_TCP", kDefaultTcpAddr);
        auto pos = a.rfind(':');
        if (pos != std::string::npos) {
            host_ = a.substr(0, pos);
            try {
                port_ = std::stoi(a.substr(pos + 1));
            } catch (...) {
                port_ = 7949;
            }
        } else {
            host_ = a;
            port_ = 7949;
        }
    }

    SOCKET sock_ = INVALID_SOCKET;
    DWORD lastFail_ = 0;
    std::string host_ = "127.0.0.1";
    int port_ = 7949;
};

// ---- 同步编辑会话：TSF 规定文本操作必须经 RequestEditSession ----

class EditSession : public ITfEditSession {
public:
    using Fn = void (*)(void* user, ITfContext* ctx, TfEditCookie ec);
    EditSession(ITfContext* ctx, void* user, Fn fn)
        : ctx_(ctx), user_(user), fn_(fn) {}

    HRESULT run(ITfThreadMgr* tm, TfClientId tid) {
        (void)tm;
        HRESULT hrSession = E_FAIL;
        HRESULT hr = ctx_->RequestEditSession(tid, this, TF_ES_SYNC | TF_ES_READWRITE, &hrSession);
        if (FAILED(hr)) return hr;
        return hrSession;
    }

    // IUnknown（栈上使用：外层在 RequestEditSession 返回后 Release）
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown || riid == IID_ITfEditSession) {
            *ppv = static_cast<ITfEditSession*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    // 栈上生命周期：TSF 在同步调用内 AddRef/Release 配对，
    // 由外层作用域析构，Release 绝不 delete this。
    ULONG STDMETHODCALLTYPE AddRef() override { return InterlockedIncrement(&ref_); }
    ULONG STDMETHODCALLTYPE Release() override { return InterlockedDecrement(&ref_); }
    HRESULT STDMETHODCALLTYPE DoEditSession(TfEditCookie ec) override {
        fn_(user_, ctx_, ec);
        return S_OK;
    }

private:
    ITfContext* ctx_;
    void* user_;
    Fn fn_;
    LONG ref_ = 1;
};

// ---- TIP 主体 ----

// ---- 模式切换提示（短暂 toast）----
// 中英/全半角切换此前零反馈，用户只能靠打字试探（2026-10-27 审查，与
// macOS/ibus 壳统一补齐）。独立小窗 + 1.2s 自动隐藏；连续切换时重置
// 定时器，不会提前收起新提示。深浅色跟随系统（与候选窗同源）。

class ModeToast {
public:
    ~ModeToast() { destroy(); }
    ModeToast() = default;
    ModeToast(const ModeToast&) = delete;
    ModeToast& operator=(const ModeToast&) = delete;

    void show(HINSTANCE hinst, const std::wstring& text, int x, int y) {
        if (!ensureWindow(hinst)) return;
        text_ = text;
        dpi_ = systemDpi();
        measure();
        int r = scale(10);
        SetWindowPos(hwnd_, HWND_TOPMOST, x, y, w_, h_, SWP_NOACTIVATE | SWP_SHOWWINDOW);
        SetWindowRgn(hwnd_, CreateRoundRectRgn(0, 0, w_ + 1, h_ + 1, r * 2, r * 2), TRUE);
        InvalidateRect(hwnd_, nullptr, TRUE);
        SetTimer(hwnd_, kTimerId, kDurationMs, nullptr);
    }

    void hide() {
        if (hwnd_ != nullptr) {
            KillTimer(hwnd_, kTimerId);
            ShowWindow(hwnd_, SW_HIDE);
        }
    }

private:
    static constexpr UINT kTimerId = 1;
    static constexpr UINT kDurationMs = 1200;

    static int systemDpi() {
        HDC hdc = GetDC(nullptr);
        int dpi = 96;
        if (hdc != nullptr) {
            dpi = GetDeviceCaps(hdc, LOGPIXELSY);
            ReleaseDC(nullptr, hdc);
        }
        return dpi;
    }

    static int scale(int px) { return MulDiv(px, systemDpi(), 96); }

    bool ensureWindow(HINSTANCE hinst) {
        if (hwnd_ != nullptr) return true;
        const std::wstring cls = windowClassName(L"ChaosetModeToastWndClass", hinst);
        WNDCLASSEXW wc{};
        wc.cbSize = sizeof(wc);
        wc.style = CS_DROPSHADOW;
        wc.lpfnWndProc = ModeToast::wndProc;
        wc.hInstance = hinst;
        wc.lpszClassName = cls.c_str();
        RegisterClassExW(&wc);
        // ModeToast 同候选窗：TOPMOST 保持（纯提示 ignoresMouseEvents
        // 本就不拦截点击；系统模态菜单弹出时位于最上），层级决策见候选窗
        // 鼠标零特例（2026-10-29 用户要求）：toast 就是一个普通提示窗，
        // 不做穿透/置顶之外的特殊处理
        hwnd_ = CreateWindowExW(
            WS_EX_TOOLWINDOW | WS_EX_TOPMOST | WS_EX_NOACTIVATE,
            cls.c_str(), L"ChaosetModeToast",
            WS_POPUP, 0, 0, 0, 0,
            nullptr, nullptr, hinst, nullptr);
        if (hwnd_ != nullptr) {
            SetWindowLongPtrW(hwnd_, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(this));
        }
        return hwnd_ != nullptr;
    }

    void measure() {
        HDC dc = GetDC(hwnd_);
        HFONT font = makeFont();
        HGDIOBJ old = SelectObject(dc, font);
        SIZE sz{};
        GetTextExtentPoint32W(dc, text_.c_str(), static_cast<int>(text_.size()), &sz);
        SelectObject(dc, old);
        DeleteObject(font);
        ReleaseDC(hwnd_, dc);
        w_ = sz.cx + scale(28);
        h_ = sz.cy + scale(12);
    }

    HFONT makeFont() const {
        return CreateFontW(-MulDiv(13, dpi_, 72), 0, 0, 0, FW_MEDIUM, FALSE, FALSE, FALSE,
                           DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                           CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
    }

    void onPaint(HWND hwnd) {
        PAINTSTRUCT ps{};
        HDC dc = BeginPaint(hwnd, &ps);
        const bool dark = isSystemDarkTheme();
        HBRUSH bg = CreateSolidBrush(dark ? RGB(0x2C, 0x2C, 0x2E) : RGB(0xF6, 0xF6, 0xF8));
        HPEN pen = CreatePen(PS_SOLID, 1, dark ? RGB(0x45, 0x45, 0x48) : RGB(0xC8, 0xC8, 0xCC));
        HGDIOBJ oldBrush = SelectObject(dc, bg);
        HGDIOBJ oldPen = SelectObject(dc, pen);
        int r = scale(10);
        RoundRect(dc, 0, 0, w_, h_, r * 2, r * 2);
        SelectObject(dc, oldBrush);
        SelectObject(dc, oldPen);
        DeleteObject(bg);
        DeleteObject(pen);
        SetBkMode(dc, TRANSPARENT);
        SetTextColor(dc, dark ? RGB(0xFF, 0xFF, 0xFF) : RGB(0x26, 0x26, 0x28));
        HFONT font = makeFont();
        HGDIOBJ oldFont = SelectObject(dc, font);
        RECT rc{0, 0, w_, h_};
        DrawTextW(dc, text_.c_str(), static_cast<int>(text_.size()), &rc,
                  DT_CENTER | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, oldFont);
        DeleteObject(font);
        EndPaint(hwnd, &ps);
    }

    static LRESULT CALLBACK wndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
        ModeToast* self = reinterpret_cast<ModeToast*>(GetWindowLongPtrW(hwnd, GWLP_USERDATA));
        switch (msg) {
            case WM_MOUSEACTIVATE:
                return MA_NOACTIVATE;
            case WM_ERASEBKGND:
                return 1;
            case WM_PAINT:
                if (self != nullptr) {
                    self->onPaint(hwnd);
                    return 0;
                }
                break;
            case WM_TIMER:
                if (self != nullptr && wParam == kTimerId) {
                    self->hide();
                    return 0;
                }
                break;
        }
        return DefWindowProcW(hwnd, msg, wParam, lParam);
    }

    void destroy() {
        if (hwnd_ != nullptr) {
            DestroyWindow(hwnd_);
            hwnd_ = nullptr;
        }
    }

    HWND hwnd_ = nullptr;
    std::wstring text_;
    int dpi_ = 96;
    int w_ = 0;
    int h_ = 0;
};

class ChaosetTIP : public ITfTextInputProcessor, public ITfKeyEventSink, public ITfCompositionSink {
public:
    ChaosetTIP() { InterlockedIncrement(&g_liveObjects); }
    virtual ~ChaosetTIP() {
        if (tm_ != nullptr) tm_->Release();
        InterlockedDecrement(&g_liveObjects);
    }

    // IUnknown
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown) {
            *ppv = static_cast<IUnknown*>(static_cast<ITfTextInputProcessor*>(this));
        } else if (riid == IID_ITfTextInputProcessor) {
            *ppv = static_cast<ITfTextInputProcessor*>(this);
        } else if (riid == IID_ITfKeyEventSink) {
            *ppv = static_cast<ITfKeyEventSink*>(this);
        } else if (riid == IID_ITfCompositionSink) {
            *ppv = static_cast<ITfCompositionSink*>(this);
        } else {
            *ppv = nullptr;
            return E_NOINTERFACE;
        }
        AddRef();
        return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return InterlockedIncrement(&ref_); }
    ULONG STDMETHODCALLTYPE Release() override {
        ULONG r = InterlockedDecrement(&ref_);
        if (r == 0) delete this;
        return r;
    }

    // ITfTextInputProcessor
    HRESULT STDMETHODCALLTYPE Activate(ITfThreadMgr* ptim, TfClientId tid) override {
        g_lastOp = kOpActivate;
        ensureCrashHandler();
        if (diagBudget()) {
            char host[260];
            diagHostName(host, static_cast<int>(sizeof(host)));
            diagPrintf(L"tsf.log", "activate pid=%lu host=%s appcontainer=%d build=%s %s\n",
                       static_cast<unsigned long>(GetCurrentProcessId()), host,
                       isAppContainer() ? 1 : 0, __DATE__, __TIME__);
        }
        tm_ = ptim;
        if (tm_ != nullptr) tm_->AddRef();
        clientId_ = tid;

        ITfKeystrokeMgr* mgr = nullptr;
        if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
            keySinkAdvised_ = SUCCEEDED(mgr->AdviseKeyEventSink(clientId_, keySink(), TRUE));
            mgr->Release();
        }
        // 非阻塞预热连接（< 0.1ms；AppContainer 未获回环豁免时是即时拒绝，
        // 同样不阻塞，见 TcpTransport::ensureConnected 注释）
        transport_.ensureConnected();
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE Deactivate() override {
        candidateWnd_.destroy();
        endComposition();
        session_.reset();
        comp_ = chaoset::Comp();
        if (keySinkAdvised_ && tm_ != nullptr) {
            ITfKeystrokeMgr* mgr = nullptr;
            if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
                mgr->UnadviseKeyEventSink(clientId_);
                mgr->Release();
            }
            keySinkAdvised_ = false;
        }
        if (tm_ != nullptr) {
            tm_->Release();
            tm_ = nullptr;
        }
        if (probeCtx_ != nullptr) {
            probeCtx_->Release();
            probeCtx_ = nullptr;
            probeCan_ = false;
        }
        return S_OK;
    }

    // ITfKeyEventSink
    HRESULT STDMETHODCALLTYPE OnSetFocus(BOOL fForeground) override {
        if (!fForeground) {
            candidateWnd_.hide();
            toast_.hide();
            clearAssociate();
            // 失去焦点时清空未决预编辑与候选，防止跨窗口残留组词状态导致后续窗口吃键
            if (isComposing()) {
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
            }
        } else {
            transport_.ensureConnected();
        }
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                            BOOL* pfEaten) override {
        g_lastOp = kOpTestKeyDown;
        if (isModifierKey(wParam)) {
            *pfEaten = FALSE;  // 修饰键始终放行
            return S_OK;
        }
        if (enMode_) {
            *pfEaten = FALSE;  // 英文模式全放行
            return S_OK;
        }
        if (!wouldHandle(wParam)) {
            *pfEaten = FALSE;
            return S_OK;
        }
        // 需要新建合成（字母）或插入文本（标点）的键，在「不支持该操作的
        // 上下文」里必须放行：2026-10-29 实机反馈，Windows 11 开始菜单里
        // 按字母没有任何反应，搜索框也不出现——键在 OnTestKeyDown 被吃掉，
        // 而 applyPreedit 在这个上下文里建不出合成，字符既不进文档也不进
        // 宿主自己的搜索框。这里先做一次无副作用的接口探测（只读文档、
        // 缺 ITfInsertAtSelection / ITfContextComposition 的上下文直接放行）；
        // handleKey 里还有一道「渲染后仍无合成则放行」的兜底（OnKeyDown 的
        // pfEaten 才是最终裁决，见 ITfKeyEventSink 两阶段设计）。
        if (composition_ == nullptr) {
            const bool shift = shiftActive();
            const bool letter = !shift && wParam >= kVkFirstLetter && wParam <= kVkLastLetter;
            const bool punct = keyPunctChar(wParam, shift) != nullptr;
            if (letter || punct) {
                ITfContext* ctx = pic;
                ContextRef owned(nullptr);
                if (ctx == nullptr) {
                    owned.ctx = focusContext();
                    ctx = owned.ctx;
                }
                bool ok;
                if (letter && ctx == probeCtx_) {
                    ok = probeCan_;  // 缓存命中：零 COM 调用
                } else {
                    ok = letter ? canStartComposition(ctx) : canInsert(ctx);
                    if (letter && ctx != nullptr) {
                        if (probeCtx_ != nullptr) probeCtx_->Release();
                        probeCtx_ = ctx;
                        probeCtx_->AddRef();
                        probeCan_ = ok;
                    }
                }
                if (!ok) {
                    *pfEaten = FALSE;
                    return S_OK;
                }
            }
        }
        *pfEaten = TRUE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                        BOOL* pfEaten) override {
        g_lastOp = kOpKeyDown;
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            onShiftEvent(false);
            *pfEaten = FALSE;  // 只跟踪不消费，Shift 组合键与按键事件完整透传给宿主
            return S_OK;
        }
        sinceShift_ = true;  // 其他键按下：取消 Shift 空按窗口
        *pfEaten = handleKey(wParam, pic) ? TRUE : FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyUp(ITfContext* /*pic*/, WPARAM wParam, LPARAM /*lParam*/,
                                          BOOL* pfEaten) override {
        (void)wParam;
        *pfEaten = FALSE;  // KeyUp 一律不消费，保证宿主应用状态机完整，绝不导致宿主丢键或键盘卡死
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnKeyUp(ITfContext* /*pic*/, WPARAM wParam, LPARAM /*lParam*/,
                                      BOOL* pfEaten) override {
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            onShiftEvent(true);
            *pfEaten = FALSE;  // 只跟踪不消费
            return S_OK;
        }
        *pfEaten = FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnPreservedKey(ITfContext* /*pic*/, REFGUID /*rguid*/, BOOL* pfEaten) override {
        *pfEaten = FALSE;
        return S_OK;
    }

    // ITfCompositionSink：合成被外部终止（焦点切换等）
    HRESULT STDMETHODCALLTYPE OnCompositionTerminated(TfEditCookie /*ec*/,
                                                      ITfComposition* /*pComposition*/) override {
        g_lastOp = kOpCompositionTerminated;
        candidateWnd_.hide();
        // 引用所有权：TSF 只在「本服务以外的人」结束合成时回调（Microsoft
        // SampleIME Composition.cpp 注释原文：whenever someone other than
        // this service ends a composition）。这时成员仍持有引用，必须在此
        // 释放；自己 EndComposition 的路径已先把成员摘成局部引用并自行
        // Release，看到 nullptr 就不能再释放（否则二次释放）。
        if (composition_ != nullptr) {
            composition_->Release();
            composition_ = nullptr;
        }
        releaseCompositionContext();
        session_.reset();
        comp_ = chaoset::Comp();
        return S_OK;
    }

    void selectCandidateByIndex(int index) {
        g_lastOp = kOpCandidateClick;
        // 联想窗点击：联想候选直接上屏并继续联想（与数字键同路径）
        if (!assoc_.candidates.empty()) {
            const int global = assocPage_ * 6 + index;
            if (index < 0 || global >= static_cast<int>(assoc_.candidates.size())) return;
            const chaoset::Candidate pick = assoc_.candidates[global];
            ContextRef ctx(focusContext());
            std::string committed;
            if (session_.associateSelect(pick.text, splitSyllables(pick.syllables), &committed)) {
                const std::string sylls = pick.syllables;
                if (commit(ctx.get(), committed)) enterAssociate(sylls);
            } else {
                clearAssociate();
            }
            return;
        }
        if (!hasCandidates()) return;
        ContextRef ctx(focusContext());
        std::string committed;
        if (session_.select(index, &committed)) {
            commit(ctx.get(), committed);
        }
    }

    void flipPage(bool next) {
        if (!hasCandidates() || comp_.pageCount <= 1) return;
        g_lastOp = kOpCandidateClick;
        ContextRef ctx(focusContext());
        comp_ = next ? session_.nextPage() : session_.prevPage();
        if (!comp_.ok) {
            endComposition();
            return;
        }
        render(ctx.get());
    }

private:
    ITfKeyEventSink* keySink() { return static_cast<ITfKeyEventSink*>(this); }
    ITfCompositionSink* compositionSink() { return static_cast<ITfCompositionSink*>(this); }

    bool isComposing() const { return !comp_.input.empty(); }
    bool hasCandidates() const { return !comp_.candidates.empty(); }

    // ---- 中英切换：Shift 空按（按下后未按其他键即释放）----
    // 语义与 ibus 壳的 shiftTracker 一致；事件路径见 OnTestKeyDown 等分流。
    bool enMode_ = false;
    int shiftDown_ = 0;      // 仍按着的 Shift 键数（左右）
    bool sinceShift_ = true; // 窗口外初始值：防止首个 Shift 释放误触发

    void onShiftEvent(bool release) {
        if (!release) {
            ++shiftDown_;
            sinceShift_ = false;
            return;
        }
        if (shiftDown_ == 0) return;  // 无按下的孤立释放
        --shiftDown_;
        if (shiftDown_ == 0) {
            if (!sinceShift_) {
                enMode_ = !enMode_;
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
                candidateWnd_.hide();
                showModeToast(enMode_ ? L"英文模式" : L"中文模式");
            }
            sinceShift_ = false;
        }
    }

    // 按键 → 产生的 ASCII 字符（US 布局直映，含 Shift 变体），只返回
    // engine.IsPunctKey 字符集内的成员，nullptr 表示「不是标点，放行」。
    //
    // 判定必须是**字符级**而非 VK 级：Shift+; 产生的字符是 ':'、Shift+/
    // 是 '?'——此前纯 VK 直映不看 Shift，导致 Shift+; 出「；」而不是「：」、
    // Shift+, 出「，」而不是「《」，且 ？！：《》"" 等十个中文标点完全不可达
    // （2026-10-27 审查发现；macOS/ibus 壳按字符判定，此处对齐）。
    // Shift 变体不在字符集的键（如 Shift+2 → @、Shift+[ → {）返回 nullptr，
    // 原字符透传给宿主，与 mac 的 passThrough 行为一致。
    static const char* keyPunctChar(WPARAM vk, bool shift) {
        if (shift) {
            switch (vk) {
                case kVkFirstDigit: return "!";          // Shift+1
                case '6': return "^";
                case '9': return "(";
                case '0': return ")";
                case kVkOemSemicolon: return ":";
                case kVkOemComma: return "<";
                case kVkOemPeriod: return ">";
                case kVkOem2: return "?";
                case kVkOem3: return "~";
                case kVkOemQuotes: return "\"";          // 开闭引号配对在 daemon
            }
            return nullptr;
        }
        switch (vk) {
            case kVkOemComma: return ",";
            case kVkOemPeriod: return ".";
            case kVkOemSemicolon: return ";";
            case kVkOemOpenBrackets: return "[";
            case kVkOemBackslash: return "\\";
            case kVkOemCloseBrackets: return "]";
            case kVkOemQuotes: return "'";
        }
        return nullptr;
    }

    // Shift/CapsLock 有效：字母按大写、数字/OEM 键按 Shift 变体处理
    static bool shiftActive() {
        return (GetKeyState(VK_SHIFT) & 0x8000) != 0 || (GetKeyState(VK_CAPITAL) & 0x0001) != 0;
    }

    static bool isModifierKey(WPARAM vk) {
        return vk == VK_SHIFT || vk == VK_LSHIFT || vk == VK_RSHIFT ||
               vk == VK_CONTROL || vk == VK_LCONTROL || vk == VK_RCONTROL ||
               vk == VK_MENU || vk == VK_LMENU || vk == VK_RMENU ||
               vk == VK_LWIN || vk == VK_RWIN;
    }

    // 是否会吃掉该键：修饰键组合（Ctrl/Alt/Win 快捷键）一律放行给宿主系统与应用，
    // 仅在组词中支持 Ctrl+Backspace 快速清空拼音；字母与标点键正常输入；
    // 组词中再吃数字/空格/回车/退格/Esc/翻页。
    bool wouldHandle(WPARAM vk) const {
        if (isModifierKey(vk)) return false;

        const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
        const bool alt  = (GetKeyState(VK_MENU) & 0x8000) != 0;
        const bool win  = ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);

        if (alt || win) return false;
        if (ctrl) {
            // 组词中支持 Ctrl+Backspace 快速清空拼音与 Ctrl+U 整行清空；
            // 其余 Ctrl 组合键（如 Ctrl+A/C/V/Z）全部放行
            return isComposing() && (vk == kVkBackspace || vk == 'U');
        }

        // 联想模式：数字（有对应项）/空格（选第一个）/回车/Esc/Backspace
        //（收起）由联想分支消费；- / = 收起联想放行；其余按键退出联想后
        // 按正常流程判定（继续往下走）。
        if (!assoc_.candidates.empty()) {
            if (vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
                vk == kVkBackspace) return true;
            if (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus)) return true;
            if (!shiftActive() && vk >= kVkFirstDigit && vk <= kVkLastDigit) {
                return assocPage_ * 6 + static_cast<int>(vk - kVkFirstDigit) <
                       static_cast<int>(assoc_.candidates.size());
            }
        }

        if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) return true;

        // 只在需要打字时介入（2026-10-29 用户要求）：未组词状态下，先判
        // 该键是否与组词相关——字母（不带 Shift）或标点才有「开始组词/出
        // 中文标点」的可能，其余键（数字/导航/F键/空格等）立即放行，且绝不
        // 触碰连接状态；内容键才允许尝试 daemon 连接（服务不可达仍放行）。
        if (!isComposing()) {
            const bool relevant = (vk >= kVkFirstLetter && vk <= kVkLastLetter && !shiftActive()) ||
                                  keyPunctChar(vk, shiftActive()) != nullptr;
            if (!relevant) return false;
            if (!const_cast<TcpTransport&>(transport_).isConnected() &&
                !const_cast<TcpTransport&>(transport_).ensureConnected()) {
                return false;
            }
        }

        // Shift+字母（大写英文，含 CapsLock）：放行给宿主，不进组词
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) return !shiftActive();
        // 翻页键不带 Shift（Shift 变体 _ + { } 与 mac 一致放行原字符）
        const bool isPageKey = !shiftActive() &&
                               (vk == kVkOemMinus || vk == kVkOemPlus ||
                                vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets ||
                                vk == kVkPrior || vk == kVkNext);
        if (isPageKey && isComposing()) return hasCandidates();
        if (keyPunctChar(vk, shiftActive()) != nullptr) return true;
        if (!isComposing()) return false;
        // 数字选词只在不带 Shift 且本页确有该序号候选时；Shift+数字产生的
        // !@# 等字符与本页没有的序号都放行（与 handleKey 的判定保持一致）
        return vk == kVkBackspace || vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
               ((vk >= kVkFirstDigit && vk <= kVkLastDigit) && !shiftActive() &&
                static_cast<int>(vk - kVkFirstDigit) < static_cast<int>(comp_.candidates.size()));
    }

    bool handleKey(WPARAM vk, ITfContext* pic) {
        if (isModifierKey(vk)) return false;

        // 词组联想分支（对齐 macOS 壳，2026-10-27 接入 Windows）：
        //   数字（有对应项）→ 联想词直接上屏并继续联想；
        //   空格 → 结束联想，空格透传；回车 → 结束联想并消费；
        //   Esc → 收起联想；- / = → 单页无翻页语义，收起放行；
        //   其余按键 → 退出联想走正常流程（字母进组词 / 标点出中文标点 /
        //   Ctrl 等修饰键照旧放行）。
        if (!assoc_.candidates.empty()) {
            const bool shift = shiftActive();
            if (vk == kVkEscape || vk == kVkReturn) {
                clearAssociate();
                return true;
            }
            // 空格：**默认选当前页第一个联想词上屏**（渐进组句闭环，
            // 与 macOS 端一致），不再是透传
            if (vk == kVkSpace) {
                if (!assoc_.candidates.empty()) {
                    const chaoset::Candidate pick = assoc_.candidates[assocPage_ * 6];
                    std::string committed;
                    if (session_.associateSelect(pick.text, splitSyllables(pick.syllables), &committed)) {
                        const std::string sylls = pick.syllables;
                        // 上屏确实写进了文档才继续联想：否则联想窗悬在
                        // 「词没上屏却推下一批」的假状态（2026-10-29 审查）。
                        if (commit(pic, committed)) enterAssociate(sylls);
                        return true;
                    }
                }
                clearAssociate();
                return true;
            }
            // - / =：联想翻页（边界页保持当前页）
            // Backspace：收起联想并消费——透传会误删刚上屏的文字
            if (vk == kVkBackspace) {
                clearAssociate();
                return true;
            }
            if (!shift && (vk == kVkOemMinus || vk == kVkOemPlus)) {
                flipAssociate(vk == kVkOemPlus);
                return true;
            }
            if (!shift && vk >= kVkFirstDigit && vk <= kVkLastDigit) {
                // 全局序号 = 当前页 × 每页条数 + 页内序号（候选窗展示的是切片）
                const int index = assocPage_ * 6 + static_cast<int>(vk - kVkFirstDigit);
                if (index < static_cast<int>(assoc_.candidates.size())) {
                    const chaoset::Candidate pick = assoc_.candidates[index];
                    std::string committed;
                    if (session_.associateSelect(pick.text, splitSyllables(pick.syllables), &committed)) {
                        const std::string sylls = pick.syllables;
                        // 上屏确实写进了文档才继续联想：否则联想窗悬在
                        // 「词没上屏却推下一批」的假状态（2026-10-29 审查）。
                        if (commit(pic, committed)) enterAssociate(sylls);
                    } else {
                        clearAssociate();
                    }
                    return true;
                }
                clearAssociate(); // 越界序号：收起联想放行数字
                return false;
            }
            clearAssociate(); // 其余按键退出联想，落到下方正常流程
        }

        const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
        const bool alt  = (GetKeyState(VK_MENU) & 0x8000) != 0;
        const bool win  = ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);

        // Alt 或 Win 快捷键：若处于组词中，先清空未决预编辑与候选窗，然后放行原键给宿主
        if (alt || win) {
            if (isComposing()) {
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
            }
            return false;
        }

        // Ctrl 组合键：
        if (ctrl) {
            if (vk == kVkBackspace && isComposing()) {
                // Ctrl+Backspace：直接全部删除（清空全部拼音）
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
                return true;
            }
            // Ctrl+U：类终端整行清空（对齐 macOS 壳，组词中消费并全部删除）
            if (vk == 'U' && isComposing()) {
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
                return true;
            }
            // 其余 Ctrl 组合键（如 Ctrl+A/C/V/Z）：若处于组词中，先清空未决预编辑与候选窗，放行原键给宿主
            if (isComposing()) {
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
            }
            return false;
        }

        // 字母：追加组词。Shift+字母（大写英文，含 CapsLock）不进组词：
        // 放行原字符，组词中先清空——此前无条件小写化，Shift+A 会变成 a
        // 混进拼音（mac 壳 isCompositionChar 只认小写，此处对齐）。
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) {
            if (shiftActive()) {
                if (isComposing()) {
                    session_.reset();
                    endComposition();
                    comp_ = chaoset::Comp();
                }
                return false;
            }
            std::string ch(1, static_cast<char>('a' + (vk - kVkFirstLetter)));
            comp_ = session_.input(ch);
            if (!comp_.ok) {
                // 偶发断开（连接已不在且不在失败冷却）：重连后重试一次，绝不
                // 轻易泄露首字母到宿主程序。此前这里无条件 closeConn 清零
                // 失败冷却——daemon 半死（响应超时）时每键都会重新全量重试，
                // 宿主 UI 线程每键被阻塞 ~200ms；现在尊重 failConn 冷却，
                // 冷却期内直接降级放行（2026-10-27 审查发现）。
                if (!transport_.isConnected() && transport_.ensureConnected()) {
                    comp_ = session_.input(ch);
                }
            }
            if (!comp_.ok) {
                // daemon 确实不可达：清状态放行原键，保证宿主应用不卡死
                endComposition();
                return false;
            }
            render(pic);
            // 渲染后仍没有合成对象：当前上下文不接受 TSF 合成（Windows 11
            // 开始菜单搜索框等）。此时必须撤掉本地未决状态并放行原键，
            // 否则就是「键被吃掉、字也没出来」——2026-10-29 实机反馈。
            // OnKeyDown 返回 false 是两阶段按键模型的最终裁决，TSF 会把
            // 原键交还宿主（OnTestKeyDown 的 eaten 只是预告）。
            //
            // 兜底条件必须同时覆盖两类拒绝：合成根本没建出来
            // （composition_ == nullptr），以及 StartComposition「成功」但
            // 预编辑 SetText 被拒（只读上下文在编辑会话里仍会拒绝写入，
            // 此时有合成对象却没有合成文本——TS_ES_READONLY）。两者都
            // 意味着键进不了文档，必须放行。
            if (isComposing() && (composition_ == nullptr || !preeditWritten_)) {
                return bailOutNoComposition();
            }
            return true;
        }

        // 候选翻页：- / =、[ / ]、PageUp / PageDown（组词中且有候选时消费；
        // 空组词放行原键/出标点）。翻页键不带 Shift：Shift 变体 _ + { }
        // 不是翻页也不是标点，落到末尾清组词放行（对齐 mac 的字符级判定）。
        const bool isPageKey = !shiftActive() &&
                               (vk == kVkOemMinus || vk == kVkOemPlus ||
                                vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets ||
                                vk == kVkPrior || vk == kVkNext);
        if (isPageKey && isComposing() && hasCandidates()) {
            bool isPrev = (vk == kVkOemMinus || vk == kVkOemOpenBrackets || vk == kVkPrior);
            comp_ = isPrev ? session_.prevPage() : session_.nextPage();
            if (!comp_.ok) {
                endComposition();
                return true;
            }
            render(pic);
            return true;
        }

        // 标点键（US 布局字符级映射，含 Shift 变体）：组词中先上屏首选再出
        // 中文标点，空组词直接出中文标点；映射规则在 daemon 侧统一完成。
        if (const char* key = keyPunctChar(vk, shiftActive())) {
            if (vk == kVkOemQuotes && isComposing()) {
                // 组词中的撇号是音节分隔符，走组词流程
                comp_ = session_.input("'");
                if (!comp_.ok) {
                    // 只在偶发断开时重连重试（尊重失败冷却，见字母分支注释）
                    if (!transport_.isConnected() && transport_.ensureConnected()) {
                        comp_ = session_.input("'");
                    }
                }
                if (!comp_.ok) {
                    endComposition();
                    return false;
                }
                render(pic);
                return true;
            }
            const bool composing = isComposing();
            std::string committed;
            if (session_.punct(key, halfWidth_, &committed)) {
                // 上屏失败（上下文不可插入文本）且没有未决拼音时放行原键，
                // 避免中文标点凭空消失；有未决拼音时仍需消费，保持组词状态机完整
                if (commit(pic, committed) || composing) return true;
                return false;
            }
            // daemon 不可达：清状态放行原键，不卡宿主应用
            if (composing) {
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
            }
            return false;
        }

        // Shift+空格：切换全角/半角标点模式。放在 isComposing 守卫**之前**：
        // 用户常在开始打字前先切模式，此前放在守卫之后导致空组词时永远
        // 切不动（mac 壳在组词判断之前处理，此处对齐；wouldHandle 已对
        // Shift+空格无条件返回 TRUE，两条路径此前互相矛盾）。
        if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
            // 全/半角开关归壳本地持有（2026-10-29 起）：快捷键切换不再与
            // daemon 往返，状态随 punct 请求下发（daemon 只按状态产出文本）
            halfWidth_ = !halfWidth_;
            showModeToast(halfWidth_ ? L"半角标点" : L"全角标点");
            return true;
        }

        if (!isComposing()) return false;

        if (vk == kVkBackspace) {
            if ((GetKeyState(VK_CONTROL) | GetKeyState(VK_SHIFT)) & 0x8000) {
                // Ctrl/Shift+Backspace：直接全部删除（对齐 macOS 壳把
                // Shift+Delete 归入全部删除；此前 Shift 走逐字符回退，三端不一致）
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
                return true;
            }
            // 普通 Backspace：慢慢部分删除（逐字符回退）
            comp_ = session_.backspace();
            if (!comp_.ok) {
                endComposition();
                return true;
            }
            if (comp_.input.empty()) endComposition();
            else render(pic);
            return true;
        }

        if (vk == kVkEscape) {
            // Esc：直接全部删除（清空全部拼音）
            session_.reset();
            endComposition();
            comp_ = chaoset::Comp();
            return true;
        }

        // 数字选词：只认不带 Shift 的 1-9，且按**本页实际候选数**判定——
        // 页面容量是 6，越界数字不消费、落到末尾兜底（清组词后放行原键）。
        // 此前 select 失败仍 return true：数字凭空消失，既不选词也不进文档
        // （mac 壳 2026-10-26 已修，此处对齐）。带 Shift 的数字（!@# 等字符）
        // 不属于选词，同样落到末尾放行。
        if (vk >= kVkFirstDigit && vk <= kVkLastDigit && !shiftActive()) {
            int index = static_cast<int>(vk - kVkFirstDigit);
            if (index < static_cast<int>(comp_.candidates.size())) {
                // 上屏候选的音节串必须在 selectPrefix 之前取：成功后
                // comp_ 会被替换成剩余组词态（或清空），候选表丢了。
                const std::string pickedSyllables = comp_.candidates[index].syllables;
                // 渐进上屏：只消耗候选音节对应的拼音前缀，剩余回灌继续组词
                //（2026-10-29 用户反馈「选一个候选清空整个拼音」不合适）
                chaoset::Comp rest;
                if (session_.selectPrefix(index, &rest) && !rest.committed.empty()) {
                    commit(pic, rest.committed);
                    if (!rest.input.empty()) {
                        // 剩余拼音回灌：更新 comp_ 为剩余组词态并重渲染
                        comp_ = rest;
                        render(pic);
                    } else {
                        // 剩余耗尽：与整句上屏同收尾（进入联想）。
                        // 2026-10-29 修复：此前传 rest.committed（上屏词
                        // **文本**，如「题」），被当作音节数组传入引擎，
                        // 单字词 → 1 个「音节」，多字词剥前缀时音节切错位
                        // （「测试」→「题」记成 shí'tí）。改传候选自身的
                        // 撇号音节串，长度与剥前缀基准一致。
                        comp_ = chaoset::Comp();
                        endComposition();
                        enterAssociate(pickedSyllables);
                    }
                } else {
                    // daemon 不可达/越界：回退整句上屏（现状语义）
                    std::string committed;
                    if (session_.select(index, &committed)) {
                        commit(pic, committed);
                    }
                }
                return true;
            }
        }

        if (vk == kVkReturn && isComposing()) {
            std::string raw = comp_.input;
            session_.reset();
            commit(pic, raw);
            return true;
        }

        // 空格：组词中一律消费（用户意图永远是「结束组词」，不应有裸空格
        // 漏进文档）——有候选选首选；残缺组词顶屏原始拼音，与回车等效
        // （此前要求 hasCandidates，无候选时空格透传进文档，mac 2026-10-25
        // 实机反馈的同族问题）。Shift+空格已在前面的全半角分支处理。
        if (vk == kVkSpace) {
            if (hasCandidates()) {
                // 同数字选词：音节奏必须在 selectPrefix 之前取（见上）
                const std::string pickedSyllables = comp_.candidates[0].syllables;
                chaoset::Comp rest;
                if (session_.selectPrefix(0, &rest) && !rest.committed.empty()) {
                    commit(pic, rest.committed);
                    if (!rest.input.empty()) {
                        comp_ = rest;
                        render(pic);
                    } else {
                        comp_ = chaoset::Comp();
                        endComposition();
                        enterAssociate(pickedSyllables);
                    }
                    return true;
                }
                std::string committed;
                if (session_.select(0, &committed)) {
                    commit(pic, committed);
                }
                return true;
            }
            std::string raw = comp_.input;
            session_.reset();
            commit(pic, raw);
            return true;
        }

        // 不可处理的按键（方向键 / F 键 / 数字 0 / Shift+数字的 @# 等字符）：
        // 组词中先清空再放行，避免预编辑与实文脱节（对齐 macOS 壳 passThrough）
        if (isComposing()) {
            session_.reset();
            endComposition();
            comp_ = chaoset::Comp();
        }
        return false;
    }

    ITfContext* focusContext() {
        if (tm_ == nullptr) return nullptr;
        ITfDocumentMgr* doc = nullptr;
        if (FAILED(tm_->GetFocus(&doc)) || doc == nullptr) return nullptr;
        ITfContext* ctx = nullptr;
        doc->GetBase(&ctx);
        doc->Release();
        return ctx;  // 调用方必须用 ContextRef 释放
    }

    // focusContext() 返回的引用计数上下文：析构时 Release。此前大多数调用点
    // 直接丢弃返回值，每次鼠标选词/翻页都泄漏一个 ITfContext（2026-10-27
    // 审查发现，本次一并修正）。
    struct ContextRef {
        explicit ContextRef(ITfContext* c) : ctx(c) {}
        ~ContextRef() {
            if (ctx != nullptr) ctx->Release();
        }
        ContextRef(const ContextRef&) = delete;
        ContextRef& operator=(const ContextRef&) = delete;
        ITfContext* get() const { return ctx; }
        ITfContext* ctx;
    };

    // 上下文能力探测：只做 QI，不改文档、不产生副作用，可安全用于
    // OnTestKeyDown。Windows 11 开始菜单等 XAML 宿主可能不提供合成接口。
    static bool canInsert(ITfContext* ctx) {
        if (ctx == nullptr) return false;
        ITfInsertAtSelection* insert = nullptr;
        if (FAILED(ctx->QueryInterface(IID_ITfInsertAtSelection,
                                       reinterpret_cast<void**>(&insert))) ||
            insert == nullptr) {
            return false;
        }
        insert->Release();
        return true;
    }

    static bool canCompose(ITfContext* ctx) {
        if (!canInsert(ctx)) return false;
        ITfContextComposition* cc = nullptr;
        if (FAILED(ctx->QueryInterface(IID_ITfContextComposition,
                                       reinterpret_cast<void**>(&cc))) ||
            cc == nullptr) {
            return false;
        }
        cc->Release();
        return true;
    }

    // 合成所属上下文（render/applyPreedit 成功建立 composition_ 时记录）。
    // 生命周期与 composition_ 一致：建立时记下，OnCompositionTerminated /
    // commit / endComposition 清空。此后对合成的一切清理都必须对它做，
    // 不能再用 focusContext() 现查——焦点可能已经切到别的文档。
    ITfContext* compCtx_ = nullptr;

    // composition_ 所属上下文的清理 cookie：同步编辑会话成功后写入
    // （RequestEditSession 返回前 DoEditSession 已执行完）。endComposition
    // 在会话外被调用时无法拿 cookie，TF_ES_SYNC 失败（TS_E_SYNCHRONOUS）
    // 时保留上一次成功值比用 0 强——0 一定失败，旧值多半仍有效。
    TfEditCookie compEditCookie_ = 0;

    // canStartComposition 按上下文缓存：同一上下文反复按键时避免每键
    // GetStatus + 双 QueryInterface（不可合成宿主此前每键三次 COM 调用）。
    // 持有引用保证指针不悬垂；合成建立/上下文切换由指针比较自然失效。
    ITfContext* probeCtx_ = nullptr;
    bool probeCan_ = false;

    // StartComposition/InsertTextAtSelection 语义上要求可写文档：只读上下文
    // 上调用属于 TS_E_READONLY，且部分宿主对只读文档的回调和出参处理不可靠。
    // 探测顺序（与 SampleIME 一致）：GetStatus 查 TF_SD_READONLY → QueryInterface
    // ITfInsertAtSelection → ITfContextComposition。全程零文档副作用，可安全
    // 用于 OnTestKeyDown 预检（2026-10-29 实机反馈：Windows 11 开始菜单
    // 搜索框等宿主的上下文需要放行）。
    static bool canStartComposition(ITfContext* ctx) {
        if (ctx == nullptr) return false;
        TF_STATUS st{};
        if (FAILED(ctx->GetStatus(&st))) return false;
        if (st.dwStaticFlags & TF_SD_READONLY) return false;
        return canCompose(ctx);  // canInsert + ITfContextComposition
    }

    // 「该键现在确实处理不了」→ 撤掉引擎未决状态并放行原键。渲染后仍无
    // 合成对象（接口探测放过了但宿主仍拒绝建合成）即属此类：键必须还给
    // 宿主，否则就是「键被吃掉、字也没出来」（2026-10-29 实机反馈）。
    // OnKeyDown 返回 false 是两阶段按键模型的最终裁决，TSF 会把原键交还。
    bool bailOutNoComposition() {
        if (diagBudget()) {
            char host[260];
            diagHostName(host, static_cast<int>(sizeof(host)));
            diagPrintf(L"tsf.log", "no composition after render host=%s preedit_len=%lu\n",
                       host, static_cast<unsigned long>(comp_.preedit.size()));
        }
        session_.reset();
        comp_ = chaoset::Comp();
        candidateWnd_.hide();
        // 走到这里时合成对象必然已不存在（未建出或 SetText 失败已当场结束），
        // 上下文记录一并清掉，避免过期引用跨越焦点切换存活。
        releaseCompositionContext();
        return false;
    }

    // 把引擎候选状态渲染为预编辑合成与候选悬浮窗
    void render(ITfContext* pic) {
        g_lastOp = kOpRender;
        ContextRef owned(nullptr);
        ITfContext* ctx = pic;
        if (ctx == nullptr) {
            owned.ctx = focusContext();
            ctx = owned.ctx;
        }
        if (ctx == nullptr) return;
        // 记录合成所属上下文：此后所有对 composition_ 的清理必须对它做——
        // 此前一律 focusContext()，焦点已切走时拿到的是新文档的上下文，
        // 用它的 cookie 清旧合成必然失败，拼音文本永久残留（2026-10-29 审查）。
        if (compCtx_ != ctx) {
            if (compCtx_ != nullptr) compCtx_->Release();
            compCtx_ = ctx;
            ctx->AddRef();
        }
        caretFound_ = false;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyPreedit(c, ec);
        });
        const HRESULT hr = es.run(tm_, clientId_);
        es.Release();
        if (FAILED(hr)) diagTsfFail("RequestEditSession(render)", hr);

        if (comp_.candidates.empty()) {
            candidateWnd_.hide();
            return;
        }

        if (!caretFound_ || (caretPos_.x == 0 && caretPos_.y == 0)) {
            // 锚点兜底链：真实 GDI 光标 → 鼠标位置。rcCaret 只在 hwndCaret
            // 非空时有意义；浏览器等自绘应用没有 GDI 光标，借 hwndFocus 的
            // rcCaret 只会拿到 (0,0)，候选窗飞到屏幕左上角（2026-10-29 实机
            // 反馈）——该分支已删，直接退回鼠标位置（用户点哪儿鼠标在哪儿）。
            GUITHREADINFO gti{};
            gti.cbSize = sizeof(gti);
            if (GetGUIThreadInfo(0, &gti) && gti.hwndCaret != nullptr &&
                (gti.rcCaret.right > gti.rcCaret.left || gti.rcCaret.bottom > gti.rcCaret.top)) {
                POINT pt{gti.rcCaret.left, gti.rcCaret.bottom};
                ClientToScreen(gti.hwndCaret, &pt);
                caretPos_ = pt;
                caretLineHeight_ = (gti.rcCaret.bottom > gti.rcCaret.top) ? (gti.rcCaret.bottom - gti.rcCaret.top) : 20;
                caretFound_ = true;
            } else {
                GetCursorPos(&caretPos_);
                caretLineHeight_ = 20;
            }
        }
        candidateWnd_.show(g_hInst, caretPos_, caretLineHeight_, comp_, this);
    }

    // 用合成范围更新候选窗锚点；View 不可用或拿到零矩形时保留旧位置——
    // Chromium 系浏览器对刚建立的合成文本还没完成布局，GetTextExt 会返回
    // S_OK 但矩形全零（2026-10-29 实机反馈：候选窗因此飞到屏幕左上角），
    // 必须按失败处理，等下一次渲染布局就绪后自然拿到真实位置。
    void updateCaretFromRange(ITfContext* ctx, TfEditCookie ec, ITfRange* range) {
        ITfContextView* view = nullptr;
        if (FAILED(ctx->GetActiveView(&view)) || view == nullptr) return;
        RECT rc{};
        BOOL fClipped = FALSE;
        if (SUCCEEDED(view->GetTextExt(ec, range, &rc, &fClipped)) &&
            (rc.right > rc.left || rc.bottom > rc.top)) {
            caretPos_.x = rc.left;
            caretPos_.y = rc.bottom;
            caretLineHeight_ = (rc.bottom > rc.top) ? (rc.bottom - rc.top) : 20;
            caretFound_ = true;
        }
        view->Release();
    }

    void applyPreedit(ITfContext* ctx, TfEditCookie ec) {
        g_lastOp = kOpPreedit;
        // 「预编辑是否真的写进了文档」：字符串为空视作已写（无需合成）；
        // SetText 失败时保持 false，字母分支据此判定该上下文不可用并放行。
        preeditWritten_ = comp_.preedit.empty();
        const std::wstring wide = toWide(comp_.preedit);
        if (composition_ != nullptr) {
            ITfRange* range = nullptr;
            const HRESULT hrRange = composition_->GetRange(&range);
            if (FAILED(hrRange) || range == nullptr) {
                diagTsfFail("ITfComposition::GetRange(preedit)",
                            FAILED(hrRange) ? hrRange : E_POINTER);
                composition_->Release();
                composition_ = nullptr;
                return;
            }
            // 先按上一轮布局查光标再改文本：Firefox(Gecko) 对「本次会话刚
            // 修改过的范围」布局未刷新，SetText 之后再查会失败或拿到旧值；
            // 改文本前合成起点不变、文档布局有效，这一查才是可靠锚点
            // （Chrome 同步布局，两次都能成功，后一次覆盖前一次）。
            updateCaretFromRange(ctx, ec, range);
            const HRESULT hrText =
                range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
            if (FAILED(hrText)) {
                diagTsfFail("ITfRange::SetText(preedit)", hrText);
                // 写不进（只读上下文 / 会话已被终止）：合成已无意义。
                // EndComposition 交还引用，本地所有权一并清空——保留一个
                // 写不进字的合成只会让后续兜底永远认为「合成还在」。
                preeditWritten_ = false;
                composition_->EndComposition(ec);
                composition_->Release();
                composition_ = nullptr;
                updateCaretFromRange(ctx, ec, range);
                range->Release();
                return;
            }
            preeditWritten_ = true;
            updateCaretFromRange(ctx, ec, range);
            range->Release();
            return;
        }
        ITfInsertAtSelection* insert = nullptr;
        HRESULT hr = ctx->QueryInterface(IID_ITfInsertAtSelection, reinterpret_cast<void**>(&insert));
        if (FAILED(hr) || insert == nullptr) {
            diagTsfFail("ITfInsertAtSelection(preedit)", FAILED(hr) ? hr : E_NOINTERFACE);
            return;
        }
        ITfRange* range = nullptr;
        // QUERYONLY：只取插入点位置的范围，不插文本
        hr = insert->InsertTextAtSelection(ec, TF_IAS_QUERYONLY, nullptr, 0, &range);
        if (FAILED(hr) || range == nullptr) {
            diagTsfFail("InsertTextAtSelection(QUERYONLY)", FAILED(hr) ? hr : E_POINTER);
            insert->Release();
            return;
        }
        // 首次建合成前先锚定插入点：此处文档已布局完成（Firefox 对新合成
        // 文本的布局要等会话结束后才刷新，合成后再查会失败），即便首键
        // 也能贴住光标；合成建立后再查一次，成功则覆盖为更精确的值。
        updateCaretFromRange(ctx, ec, range);
        ITfContextComposition* cc = nullptr;
        hr = ctx->QueryInterface(IID_ITfContextComposition, reinterpret_cast<void**>(&cc));
        if (FAILED(hr) || cc == nullptr) {
            diagTsfFail("ITfContextComposition", FAILED(hr) ? hr : E_NOINTERFACE);
        } else {
            ITfComposition* comp = nullptr;
            hr = cc->StartComposition(ec, range, compositionSink(), &comp);
            if (FAILED(hr) || comp == nullptr) {
                diagTsfFail("StartComposition", FAILED(hr) ? hr : E_POINTER);
                if (comp != nullptr) comp->Release();
            } else {
                composition_ = comp;
                const HRESULT hrText =
                    range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                if (FAILED(hrText)) {
                    diagTsfFail("ITfRange::SetText(preedit)", hrText);
                    // StartComposition 成功但文本写不进：与上面同理，
                    // 必须立刻结束合成并清空所有权，否则字母分支的
                    // 「渲染后仍无合成」兜底永远不触发，键照样被吃。
                    preeditWritten_ = false;
                    composition_->EndComposition(ec);
                    composition_->Release();
                    composition_ = nullptr;
                } else {
                    preeditWritten_ = true;
                }
            }
            cc->Release();
        }
        // 合成建立失败也尝试取一次光标位置：候选窗仍能贴住插入点显示
        updateCaretFromRange(ctx, ec, range);
        range->Release();
        insert->Release();
    }

    // 上屏；返回是否确实写进了文档（调用方据此决定失败时是否放行原键）
    bool commit(ITfContext* pic, const std::string& text) {
        g_lastOp = kOpCommit;
        candidateWnd_.hide();
        ContextRef owned(nullptr);
        ITfContext* ctx = pic;
        if (ctx == nullptr) {
            owned.ctx = focusContext();
            ctx = owned.ctx;
        }
        if (ctx == nullptr) return false;
        committed_ = text;
        commitWritten_ = false;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyCommit(c, ec);
        });
        const HRESULT hr = es.run(tm_, clientId_);
        es.Release();
        if (FAILED(hr)) diagTsfFail("RequestEditSession(commit)", hr);
        comp_ = chaoset::Comp();
        return SUCCEEDED(hr) && commitWritten_;
    }

    void applyCommit(ITfContext* ctx, TfEditCookie ec) {
        g_lastOp = kOpCommit;
        commitWritten_ = false;
        if (committed_.empty()) return;
        const std::wstring wide = toWide(committed_);

        // 先摘成员再用局部指针：EndComposition 之后成员值不再可信，且
        // 「我方结束」与「外部终止」两条路径的引用所有权必须唯一
        // （见 OnCompositionTerminated 的注释）。
        ITfComposition* comp = composition_;
        composition_ = nullptr;
        if (comp != nullptr) {
            ITfRange* range = nullptr;
            if (SUCCEEDED(comp->GetRange(&range)) && range != nullptr) {
                const HRESULT hrText =
                    range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                if (FAILED(hrText)) diagTsfFail("ITfRange::SetText(commit)", hrText);
                else commitWritten_ = true;
                range->Release();
            }
            const HRESULT hrEnd = comp->EndComposition(ec);
            if (FAILED(hrEnd)) diagTsfFail("ITfComposition::EndComposition(commit)", hrEnd);
            comp->Release();
            releaseCompositionContext();
        } else {
            // 无合成（直接打标点 / 合成被外部终止）：必须自己插入文本。
            // 此前这里什么都不做，标点会凭空消失。
            ITfInsertAtSelection* insert = nullptr;
            HRESULT hr = ctx->QueryInterface(IID_ITfInsertAtSelection,
                                             reinterpret_cast<void**>(&insert));
            if (FAILED(hr) || insert == nullptr) {
                diagTsfFail("ITfInsertAtSelection(commit)", FAILED(hr) ? hr : E_NOINTERFACE);
            } else {
                ITfRange* range = nullptr;
                hr = insert->InsertTextAtSelection(ec, 0, wide.c_str(),
                                                   static_cast<LONG>(wide.size()), &range);
                if (FAILED(hr)) {
                    diagTsfFail("InsertTextAtSelection(commit)", hr);
                } else {
                    commitWritten_ = true;
                    if (range != nullptr) range->Release();
                }
                insert->Release();
            }
        }
        committed_.clear();
    }

    // 取消/清理当前合成：必须经真实编辑会话拿到有效 cookie。旧实现用
    // cookie 0 在编辑会话外调用 SetText/EndComposition，TSF 只会返回失败：
    // 预编辑文本留在文档里、合成对象也泄漏（2026-10-29 审查）。
    void endComposition() {
        candidateWnd_.hide();
        ITfComposition* comp = composition_;
        if (comp == nullptr) return;
        g_lastOp = kOpEndComposition;
        composition_ = nullptr;  // 先摘成员：结束路径的引用所有权唯一（见 sink）
        // 必须对合成所属的上下文做（render 时记录），不能用 focusContext()：
        // 焦点切走后现查到的是新文档的上下文，RequestEditSession 会失败或
        // 拿到无效 cookie，拼音文本永久残留（2026-10-29 审查）。
        // 注意所有权：compCtx_ 的引用归 releaseCompositionContext() 释放，
        // 这里绝不能把它包进 owning 的 ContextRef（会双重 Release）。
        ITfContext* target = compCtx_;
        ContextRef owned(nullptr);
        if (target == nullptr) {
            owned.ctx = focusContext();
            target = owned.ctx;
        }
        if (target != nullptr) {
            endTarget_ = comp;
            EditSession es(target, this, [](void* user, ITfContext* c, TfEditCookie ec) {
                static_cast<ChaosetTIP*>(user)->applyEndComposition(c, ec);
            });
            HRESULT hrSession = S_OK;
            const HRESULT hr = target->RequestEditSession(
                clientId_, &es, TF_ES_SYNC | TF_ES_READWRITE, &hrSession);
            if (FAILED(hr)) {
                diagTsfFail("RequestEditSession(endComposition)", hr);
            } else if (FAILED(hrSession)) {
                // TF_ES_SYNC 在后台线程等场景会失败；保留最后一次成功的
                // cookie 收尾——比换 cookie 0 强（0 必失败，旧值多半仍有效）。
                diagTsfFail("RequestEditSession(endComposition).session", hrSession);
                if (compEditCookie_ > 0) applyEndComposition(nullptr, compEditCookie_);
            }
            es.Release();
            endTarget_ = nullptr;
        }
        comp->Release();
        releaseCompositionContext();
    }

    void applyEndComposition(ITfContext* /*ctx*/, TfEditCookie ec) {
        ITfComposition* comp = endTarget_;
        if (comp == nullptr) return;
        if (ec > 0) compEditCookie_ = ec;  // 记录成功 cookie（endComposition 兜底用）
        ITfRange* range = nullptr;
        if (SUCCEEDED(comp->GetRange(&range)) && range != nullptr) {
            const HRESULT hrText = range->SetText(ec, 0, L"", 0);
            if (FAILED(hrText)) diagTsfFail("ITfRange::SetText(clear)", hrText);
            range->Release();
        }
        const HRESULT hrEnd = comp->EndComposition(ec);
        if (FAILED(hrEnd)) diagTsfFail("ITfComposition::EndComposition(clear)", hrEnd);
    }

    LONG ref_ = 1;
    ITfThreadMgr* tm_ = nullptr;
    TfClientId clientId_ = 0;
    bool keySinkAdvised_ = false;

    CandidateWindow candidateWnd_;
    ModeToast toast_;

    // 模式提示：放光标行下方（caretPos_ 是最后已知光标行底边的屏幕坐标）；
    // 没有历史位置时退回鼠标。
    void showModeToast(const wchar_t* text) {
        POINT pt = caretPos_;
        if (pt.x == 0 && pt.y == 0) GetCursorPos(&pt);
        int lineH = caretLineHeight_ > 0 ? caretLineHeight_ : 20;
        toast_.show(g_hInst, text, pt.x, pt.y + lineH + 6);
    }
    POINT caretPos_{0, 0};
    int caretLineHeight_ = 20;
    bool caretFound_ = false;

    TcpTransport transport_;
    chaoset::Session session_{transport_};
    chaoset::Comp comp_;
    bool halfWidth_ = false; // 全/半角标点开关（壳本地持有，随 punct 请求下发）
    // 词组联想（对齐 macOS 壳）：candidates 非空即联想中。选词上屏后按
    // 上屏词音节查续接词组继续展示，数字键可直接连选。
    chaoset::Comp assoc_;
    int assocPage_ = 0; // 联想当前页（0 起，本地分页每页 6 条）
    ITfComposition* composition_ = nullptr;
    ITfComposition* endTarget_ = nullptr;  // endComposition 编辑会话内的目标
    std::string committed_;
    bool commitWritten_ = false;
    bool preeditWritten_ = false;  // 预编辑确实写进了文档（渲染兜底判定用）

    // 合成结束（commit / endComposition / 外部终止）后调用：清空合成所属
    // 上下文记录。composition_ 为空时 compCtx_ 必须为空，避免持有过期引用。
    void releaseCompositionContext() {
        if (compCtx_ != nullptr) {
            compCtx_->Release();
            compCtx_ = nullptr;
        }
        preeditWritten_ = false;
    }

    // 撇号分隔的音节串 → 音节数组（"xian'zai" → ["xian","zai"]）。
    static std::vector<std::string> splitSyllables(const std::string& syllables) {
        std::vector<std::string> out;
        std::size_t start = 0;
        while (start <= syllables.size()) {
            std::size_t pos = syllables.find('\'', start);
            if (pos == std::string::npos) {
                if (start < syllables.size()) out.push_back(syllables.substr(start));
                break;
            }
            if (pos > start) out.push_back(syllables.substr(start, pos - start));
            start = pos + 1;
        }
        return out;
    }

    // 选词上屏后进入联想：查续接词组并复用候选窗展示（单页，页码 1/1）。
    // 候选栏齿轮 → 设置菜单（微信输入法交互参考）：菜单项带勾选态，
    // 点击即切换并立即生效（关闭时收起联想窗，开启后下次选词生效）。
    // TrackPopupMenu 前需 SetForegroundWindow（否则菜单点外部不消失，
    // MSDN KB135788 的经典要求），返回后补发 WM_NULL 复位。
public:
    // 设置窗口入口（三端一致的独立设置窗口，macOS SettingsWindow 同构；
    // 定义在文件后部 CandidateWindow::onLButtonDown 附近）。
    void showSettingsWindow();

    // 设置窗口开关回调：写注册表并即时生效——关闭时立即收起联想窗
    // （与 macOS SettingsWindow 的 chaosetAssociateDisabled 通知语义一致）
    void applyAssociateSetting(bool on) {
        RegSetAssociate(on);
        if (!on) clearAssociate();
    }

private:

    void enterAssociate(const std::string& syllables) {
        if (!RegGetAssociate()) return; // 设置关闭词组联想
        assoc_ = chaoset::Comp();
        std::vector<std::string> sylls = splitSyllables(syllables);
        if (sylls.empty()) return;
        chaoset::Comp assoc;
        if (!session_.associate(sylls, &assoc) || assoc.candidates.empty()) return;
        assoc_ = std::move(assoc);
        assocPage_ = 0;
        showAssociatePage();
    }

    // 展示联想当前页（本地分页：全量候选在 assoc_，每页 pageSize 条）。
    void showAssociatePage() {
        const int per = 6; // 与 engine.PageSize 一致（数字键 1-6 指位）
        const int total = static_cast<int>(assoc_.candidates.size());
        const int pageCount = (total + per - 1) / per;
        if (assocPage_ >= pageCount) assocPage_ = pageCount - 1;
        const int start = assocPage_ * per;
        const int end = std::min(start + per, total);
        chaoset::Comp page;
        page.ok = true;
        page.page = assocPage_;
        page.pageCount = pageCount;
        page.candidates.assign(assoc_.candidates.begin() + start,
                               assoc_.candidates.begin() + end);
        candidateWnd_.show(g_hInst, caretPos_, caretLineHeight_, page, this);
    }

    // 联想翻页（- / = 键）：边界页保持当前页。返回是否发生了翻页。
    bool flipAssociate(bool next) {
        const int total = static_cast<int>(assoc_.candidates.size());
        const int pageCount = (total + 5) / 6;
        if (next && assocPage_ + 1 < pageCount) {
            ++assocPage_;
        } else if (!next && assocPage_ > 0) {
            --assocPage_;
        } else {
            return false;
        }
        showAssociatePage();
        return true;
    }

    // 退出联想并收起联想窗。
    void clearAssociate() {
        if (assoc_.candidates.empty()) return;
        assoc_ = chaoset::Comp();
        assocPage_ = 0;
        candidateWnd_.hide();
    }
};

void CandidateWindow::onLButtonDown(int x, int y) {
    POINT pt{ x, y };
    if (tip_ != nullptr && PtInRect(&settingsRect_, pt)) {
        tip_->showSettingsWindow();
        return;
    }
    for (const auto& it : items_) {
        if (PtInRect(&it.itemRect, pt)) {
            if (tip_ != nullptr) {
                tip_->selectCandidateByIndex(it.index);
            }
            return;
        }
    }
    if (comp_.pageCount > 1 && PtInRect(&chevronRect_, pt)) {
        if (tip_ != nullptr) {
            bool hasNext = comp_.page + 1 < comp_.pageCount;
            bool hasPrev = comp_.page > 0;
            bool dirDown = hasNext || !hasPrev;
            tip_->flipPage(dirDown);
        }
    }
}

// ---- 设置窗口（三端一致：macOS SettingsWindow.swift 同构）----
//
// 候选栏齿轮 → 独立设置窗口。此前这里是 TrackPopupMenu 的单个勾选项，
// 用户要求与 macOS 一致的专门设置窗口。布局与 macOS 逐项对应：标题
// 「Chaoset 设置」/ 分组「输入行为」/「词组联想」开关 / 说明文字 /
// 底部「更多设置将在后续版本提供」。开关即时写 HKCU\Software\Chaoset
// 并收起联想窗（applyAssociateSetting）。窗口单例：已打开则前置。
static HWND g_settingsWnd = nullptr;       // 单例句柄
static HFONT g_settingsFont = nullptr;     // 正文 9pt
static HFONT g_settingsFontBold = nullptr; // 分组标题 9pt Bold

static LRESULT CALLBACK SettingsWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    ChaosetTIP* tip = reinterpret_cast<ChaosetTIP*>(GetWindowLongPtrW(hwnd, GWLP_USERDATA));
    switch (msg) {
        case WM_COMMAND:
            if (LOWORD(wParam) == 101 && HIWORD(wParam) == BN_CLICKED) {
                const bool on = SendMessageW(reinterpret_cast<HWND>(lParam),
                                             BM_GETCHECK, 0, 0) == BST_CHECKED;
                if (tip != nullptr) tip->applyAssociateSetting(on);
            }
            return 0;
        case WM_CTLCOLORSTATIC: {
            // 说明文字 / 底部提示用次级文本灰（macOS secondaryLabel 对应）
            const int id = GetDlgCtrlID(reinterpret_cast<HWND>(lParam));
            if (id == 102 || id == 103 || id == 104) {
                HDC dc = reinterpret_cast<HDC>(wParam);
                SetTextColor(dc, RGB(110, 110, 115));
                SetBkMode(dc, TRANSPARENT);
                return reinterpret_cast<LRESULT>(GetSysColorBrush(COLOR_WINDOW));
            }
            break;
        }
        case WM_CLOSE:
            DestroyWindow(hwnd);
            return 0;
        case WM_DESTROY:
            if (g_settingsFont != nullptr) { DeleteObject(g_settingsFont); g_settingsFont = nullptr; }
            if (g_settingsFontBold != nullptr) { DeleteObject(g_settingsFontBold); g_settingsFontBold = nullptr; }
            g_settingsWnd = nullptr;
            InterlockedDecrement(&g_liveSettingsWindows);
            return 0;
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

void ChaosetTIP::showSettingsWindow() {
    // 单例：已打开则前置（macOS makeKeyAndOrderFront 同语义）
    if (g_settingsWnd != nullptr && IsWindow(g_settingsWnd)) {
        SetForegroundWindow(g_settingsWnd);
        return;
    }

    const std::wstring cls = windowClassName(L"ChaosetSettingsWndClass", g_hInst);
    WNDCLASSEXW wc{};
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = SettingsWndProc;
    wc.hInstance = g_hInst;
    wc.hCursor = LoadCursorW(nullptr, MAKEINTRESOURCEW(32512));
    wc.hbrBackground = reinterpret_cast<HBRUSH>(static_cast<INT_PTR>(COLOR_WINDOW + 1));
    wc.lpszClassName = cls.c_str();
    RegisterClassExW(&wc);

    int dpi = 96;
    if (HDC dc = GetDC(nullptr)) {
        dpi = GetDeviceCaps(dc, LOGPIXELSY);
        ReleaseDC(nullptr, dc);
    }
    auto scalePx = [dpi](int px) { return MulDiv(px, dpi, 96); };

    // 居中于光标所在显示器的工作区（macOS center 同语义）
    POINT anchor = caretPos_;
    if (anchor.x == 0 && anchor.y == 0) GetCursorPos(&anchor);
    const DWORD style = WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU;
    const DWORD exStyle = WS_EX_DLGMODALFRAME;
    RECT fr = {0, 0, scalePx(400), scalePx(220)};
    AdjustWindowRectEx(&fr, style, FALSE, exStyle);
    int x = CW_USEDEFAULT, y = CW_USEDEFAULT;
    MONITORINFO mi{};
    mi.cbSize = sizeof(mi);
    if (GetMonitorInfoW(MonitorFromPoint(anchor, MONITOR_DEFAULTTONEAREST), &mi)) {
        const int w = fr.right - fr.left, h = fr.bottom - fr.top;
        x = mi.rcWork.left + ((mi.rcWork.right - mi.rcWork.left) - w) / 2;
        y = mi.rcWork.top + ((mi.rcWork.bottom - mi.rcWork.top) - h) / 2;
    }

    HWND wnd = CreateWindowExW(exStyle, cls.c_str(), L"Chaoset 设置", style,
                               x, y, fr.right - fr.left, fr.bottom - fr.top,
                               nullptr, nullptr, g_hInst, nullptr);
    if (wnd == nullptr) return;
    SetWindowLongPtrW(wnd, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(this));
    g_settingsWnd = wnd;
    InterlockedIncrement(&g_liveSettingsWindows);

    g_settingsFont = CreateFontW(-MulDiv(9, dpi, 72), 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
                                 DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                 CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
    g_settingsFontBold = CreateFontW(-MulDiv(9, dpi, 72), 0, 0, 0, FW_BOLD, FALSE, FALSE, FALSE,
                                     DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                     CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
    auto makeCtl = [&](const wchar_t* ctlClass, DWORD ctlStyle, const wchar_t* text,
                       int cx, int cy, int cw, int ch, int id, HFONT font) {
        HWND c = CreateWindowExW(0, ctlClass, text, WS_CHILD | WS_VISIBLE | ctlStyle,
                                 scalePx(cx), scalePx(cy), scalePx(cw), scalePx(ch), wnd,
                                 reinterpret_cast<HMENU>(static_cast<INT_PTR>(id)), g_hInst, nullptr);
        if (c != nullptr && font != nullptr) SendMessageW(c, WM_SETFONT, reinterpret_cast<WPARAM>(font), TRUE);
        return c;
    };
    // 与 macOS SettingsWindow 逐项对应：分组「输入行为」→「词组联想」开关
    // → 说明文字 → 底部「更多设置将在后续版本提供」
    makeCtl(L"Button", BS_GROUPBOX, L"输入行为", 16, 14, 368, 92, 100, g_settingsFontBold);
    HWND chk = makeCtl(L"Button", BS_AUTOCHECKBOX | WS_TABSTOP, L"词组联想",
                       34, 40, 140, 20, 101, g_settingsFont);
    SendMessageW(chk, BM_SETCHECK, RegGetAssociate() ? BST_CHECKED : BST_UNCHECKED, 0);
    makeCtl(L"Static", 0, L"选词上屏后展示可续接的词组候选；", 34, 68, 340, 18, 102, g_settingsFont);
    makeCtl(L"Static", 0, L"关闭后不再弹出联想候选。", 34, 86, 340, 18, 103, g_settingsFont);
    makeCtl(L"Static", 0, L"更多设置将在后续版本提供", 34, 124, 340, 18, 104, g_settingsFont);

    HANDLE icoBig = LoadImageW(g_hInst, MAKEINTRESOURCEW(1), IMAGE_ICON,
                               GetSystemMetrics(SM_CXICON), GetSystemMetrics(SM_CYICON), LR_DEFAULTCOLOR);
    if (icoBig != nullptr) SendMessageW(wnd, WM_SETICON, ICON_BIG, reinterpret_cast<LPARAM>(icoBig));
    HANDLE icoSmall = LoadImageW(g_hInst, MAKEINTRESOURCEW(1), IMAGE_ICON,
                                 GetSystemMetrics(SM_CXSMICON), GetSystemMetrics(SM_CYSMICON), LR_DEFAULTCOLOR);
    if (icoSmall != nullptr) SendMessageW(wnd, WM_SETICON, ICON_SMALL, reinterpret_cast<LPARAM>(icoSmall));

    ShowWindow(wnd, SW_SHOWNORMAL);
    SetForegroundWindow(wnd);
}

// ---- 类厂 ----

class ChaosetFactory : public IClassFactory {
public:
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown || riid == IID_IClassFactory) {
            *ppv = static_cast<IClassFactory*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return 2; }
    ULONG STDMETHODCALLTYPE Release() override { return 1; }
    HRESULT STDMETHODCALLTYPE CreateInstance(IUnknown* outer, REFIID riid, void** ppv) override {
        if (outer != nullptr) return CLASS_E_NOAGGREGATION;
        ChaosetTIP* tip = new ChaosetTIP();
        HRESULT hr = tip->QueryInterface(riid, ppv);
        tip->Release();
        return hr;
    }
    HRESULT STDMETHODCALLTYPE LockServer(BOOL /*fLock*/) override { return S_OK; }
};

}  // namespace

// ---- 导出 ----

BOOL WINAPI DllMain(HINSTANCE hinstDLL, DWORD fdwReason, LPVOID reserved) {
    if (fdwReason == DLL_PROCESS_ATTACH) {
        g_hInst = hinstDLL;
    } else if (fdwReason == DLL_PROCESS_DETACH && reserved == nullptr && hinstDLL != nullptr) {
        // 动态卸载（FreeLibrary）：注销本模块注册的窗口类。Windows 不会随
        // DLL 卸载自动注销（RegisterClassExW 文档明示），不注销的话下次
        // 加载时同名类的 lpfnWndProc 可能仍指向已卸载内存。进程终止
        // （reserved != nullptr）时无需处理。
        UnregisterClassW(windowClassName(L"ChaosetCandidateWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetModeToastWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetSettingsWndClass", hinstDLL).c_str(), hinstDLL);
    }
    return TRUE;
}

STDAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void** ppv) {
    if (!IsEqualCLSID(rclsid, CLSID_ChaosetTIP)) return CLASS_E_CLASSNOTAVAILABLE;
    // 一次性记录宿主实例化 TIP：与 activate 行对读可以区分「宿主加载了
    // DLL 但 Activate 没跑」和「宿主根本没加载」（2026-10-29 开始菜单
    // 排障：tsf.log 里没有任何开始菜单相关宿主的记录，无法定位加载层）。
    static volatile LONG logged = 0;
    if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
        char host[260];
        diagHostName(host, static_cast<int>(sizeof(host)));
        diagPrintf(L"tsf.log", "classobject host=%s build=%s %s\n", host,
                   __DATE__, __TIME__);
    }
    static ChaosetFactory factory;  // 无状态，静态实例即可
    return factory.QueryInterface(riid, ppv);
}

STDAPI DllCanUnloadNow() {
    // 必须同时考虑 TIP 实例与候选窗口：
    //
    // 2026-10-26 审查指出的崩溃隐患——g_liveObjects 只统计 ChaosetTIP，
    // 但候选窗是懒创建的 HWND，其窗口过程（CandidateWndProc）指向本 DLL 里的
    // 代码。若窗口还开着而 TIP 已全部释放，计数归零 → 宿主 FreeLibrary →
    // 窗口过程变成已卸载内存的函数指针，下一次 WM_PAINT 直接崩 explorer /
    // TextInputHost。因此有残留窗口时也返回 S_FALSE（不可卸载）。
    if (g_liveObjects != 0) return S_FALSE;
    if (g_liveCandidateWindows != 0) return S_FALSE;
    if (g_liveSettingsWindows != 0) return S_FALSE;
    return S_OK;
}

static std::string dllPath() {
    char path[MAX_PATH] = {0};
    if (g_hInst != nullptr) {
        GetModuleFileNameA(g_hInst, path, MAX_PATH);
    }
    return path;
}

static std::wstring dllPathW() {
    wchar_t path[MAX_PATH] = {0};
    if (g_hInst != nullptr) {
        GetModuleFileNameW(g_hInst, path, MAX_PATH);
    }
    return path;
}

static HRESULT regSet(HKEY root, const std::string& key, const std::string& name,
                      const std::string& data) {
    HKEY k = nullptr;
    LONG r = RegCreateKeyExA(root, key.c_str(), 0, nullptr, REG_OPTION_NON_VOLATILE, KEY_WRITE,
                             nullptr, &k, nullptr);
    if (r != ERROR_SUCCESS) return E_FAIL;
    r = RegSetValueExA(k, name.c_str(), 0, REG_SZ, reinterpret_cast<const BYTE*>(data.c_str()),
                       static_cast<DWORD>(data.size() + 1));
    RegCloseKey(k);
    return r == ERROR_SUCCESS ? S_OK : E_FAIL;
}

static HRESULT regSetDword(HKEY root, const std::string& key, const std::string& name,
                           DWORD data) {
    HKEY k = nullptr;
    LONG r = RegCreateKeyExA(root, key.c_str(), 0, nullptr, REG_OPTION_NON_VOLATILE, KEY_WRITE,
                             nullptr, &k, nullptr);
    if (r != ERROR_SUCCESS) return E_FAIL;
    r = RegSetValueExA(k, name.c_str(), 0, REG_DWORD, reinterpret_cast<const BYTE*>(&data),
                       sizeof(DWORD));
    RegCloseKey(k);
    return r == ERROR_SUCCESS ? S_OK : E_FAIL;
}

static std::string guidToString(const GUID& g) {
    wchar_t wide[64] = {0};
    StringFromGUID2(g, wide, 64);
    char narrow[64] = {0};
    WideCharToMultiByte(CP_ACP, 0, wide, -1, narrow, sizeof(narrow), nullptr, nullptr);
    return narrow;
}

STDAPI DllRegisterServer() {
    std::string clsid = guidToString(CLSID_ChaosetTIP);
    std::string profid = guidToString(GUID_ChaosetProfile);
    std::string pathA = dllPath();
    std::wstring pathW = dllPathW();

    // 1. COM 基础服务注册 (HKLM\Software\Classes\CLSID\{CLSID})
    std::string base = "Software\\Classes\\CLSID\\" + clsid;
    HRESULT hr = regSet(HKEY_LOCAL_MACHINE, base, "", "Chaoset 拼音输入法");
    if (SUCCEEDED(hr)) hr = regSet(HKEY_LOCAL_MACHINE, base + "\\InprocServer32", "", pathA);
    if (SUCCEEDED(hr)) hr = regSet(HKEY_LOCAL_MACHINE, base + "\\InprocServer32", "ThreadingModel", "Apartment");

    // 2. 通过 Windows TSF 标准 COM 接口注册配置文件与类别
    HRESULT hrCo = CoInitialize(nullptr);

    ITfInputProcessorProfiles* profiles = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_InputProcessorProfiles, nullptr,
                                   CLSCTX_INPROC_SERVER, kIID_ITfInputProcessorProfiles,
                                   reinterpret_cast<void**>(&profiles)))) {
        profiles->Register(CLSID_ChaosetTIP);
        const wchar_t* desc = L"Chaoset 拼音输入法";
        profiles->AddLanguageProfile(CLSID_ChaosetTIP, 0x0804, GUID_ChaosetProfile,
                                     desc, static_cast<ULONG>(wcslen(desc)),
                                     pathW.c_str(), static_cast<ULONG>(pathW.size()), 0);
        profiles->EnableLanguageProfile(CLSID_ChaosetTIP, 0x0804, GUID_ChaosetProfile, TRUE);
        profiles->EnableLanguageProfileByDefault(CLSID_ChaosetTIP, 0x0804, GUID_ChaosetProfile, TRUE);
        profiles->Release();
    }

    ITfCategoryMgr* catMgr = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_CategoryMgr, nullptr,
                                   CLSCTX_INPROC_SERVER, kIID_ITfCategoryMgr,
                                   reinterpret_cast<void**>(&catMgr)))) {
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIP_KEYBOARD, CLSID_ChaosetTIP);
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_DISPLAYATTRIBUTEPROVIDER, CLSID_ChaosetTIP);
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_UIELEMENTENABLED, CLSID_ChaosetTIP);
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT, CLSID_ChaosetTIP);
        catMgr->RegisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT, CLSID_ChaosetTIP);
        catMgr->Release();
    }

    if (SUCCEEDED(hrCo)) {
        CoUninitialize();
    }

    // 3. 注册表双重保障：直接写入 CTF 结构（应对特殊环境与控制面板直接扫描）
    std::string tips = "Software\\Microsoft\\CTF\\TIPs\\" + clsid;
    regSet(HKEY_LOCAL_MACHINE, tips, "", "Chaoset 拼音输入法");

    const GUID* catGuids[] = {
        &kGUID_TFCAT_TIP_KEYBOARD,
        &kGUID_TFCAT_DISPLAYATTRIBUTEPROVIDER,
        &kGUID_TFCAT_TIPCAP_UIELEMENTENABLED,
        &kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT,
        &kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT,
    };
    for (const GUID* g : catGuids) {
        std::string gStr = guidToString(*g);
        regSet(HKEY_LOCAL_MACHINE, tips + "\\Category\\Category\\" + gStr + "\\" + clsid, "", "");
        regSet(HKEY_LOCAL_MACHINE, tips + "\\Category\\Item\\" + gStr + "\\" + clsid, "", "");
    }

    std::string langProfile = tips + "\\LanguageProfile\\0x00000804\\" + profid;
    regSet(HKEY_LOCAL_MACHINE, langProfile, "Description", "Chaoset 拼音输入法");
    regSet(HKEY_LOCAL_MACHINE, langProfile, "IconFile", pathA);
    regSetDword(HKEY_LOCAL_MACHINE, langProfile, "IconIndex", 0);
    regSetDword(HKEY_LOCAL_MACHINE, langProfile, "Enable", 1);

    return hr;
}

STDAPI DllUnregisterServer() {
    std::string clsid = guidToString(CLSID_ChaosetTIP);

    HRESULT hrCo = CoInitialize(nullptr);

    ITfCategoryMgr* catMgr = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_CategoryMgr, nullptr,
                                   CLSCTX_INPROC_SERVER, kIID_ITfCategoryMgr,
                                   reinterpret_cast<void**>(&catMgr)))) {
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIP_KEYBOARD, CLSID_ChaosetTIP);
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_DISPLAYATTRIBUTEPROVIDER, CLSID_ChaosetTIP);
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_UIELEMENTENABLED, CLSID_ChaosetTIP);
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT, CLSID_ChaosetTIP);
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT, CLSID_ChaosetTIP);
        catMgr->Release();
    }

    ITfInputProcessorProfiles* profiles = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_InputProcessorProfiles, nullptr,
                                   CLSCTX_INPROC_SERVER, kIID_ITfInputProcessorProfiles,
                                   reinterpret_cast<void**>(&profiles)))) {
        profiles->RemoveLanguageProfile(CLSID_ChaosetTIP, 0x0804, GUID_ChaosetProfile);
        profiles->Unregister(CLSID_ChaosetTIP);
        profiles->Release();
    }

    if (SUCCEEDED(hrCo)) {
        CoUninitialize();
    }

    RegDeleteTreeA(HKEY_LOCAL_MACHINE, ("Software\\Microsoft\\CTF\\TIPs\\" + clsid).c_str());
    RegDeleteTreeA(HKEY_LOCAL_MACHINE, ("Software\\Classes\\CLSID\\" + clsid).c_str());
    return S_OK;
}

// 保持历史兼容
STDAPI DllUninstallServer() {
    return DllUnregisterServer();
}
