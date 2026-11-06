// ChaosetIME TSF 壳（Windows 专用胶水层）。
//
// 职责：COM TIP 注册与激活、按键拦截（ITfKeyEventSink + ITfKeystrokeMgr）、
// 预编辑合成（ITfContextComposition）、候选上屏；引擎逻辑全部经
// core::Session 与 chaosetd 通信，传输只有 Windows 命名管道一种
// （\\.\pipe\chaoset-<SID>：无端口、ACL 限当前用户，无任何回退通道）。
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
#include <windows.h>
#include <windowsx.h>  // GET_X_LPARAM / GET_Y_LPARAM（设置窗导航命中）
#include <wingdi.h>
#include <appmodel.h>
#include <msctf.h>
#include <objbase.h>
#include <shellapi.h>  // SHQueryUserNotificationState（全屏免打扰，2026-10-31 A7）
#include <commdlg.h>  // GetSaveFileNameW（设置窗口导出学习数据，2026-10-31）
#include <shlobj.h>   // SHGetFolderPathW（设置窗口导入词库定位 %LOCALAPPDATA%，2026-10-31）
#include <sddl.h>     // ConvertSidToStringSidW（命名管道名按当前用户 SID）
#include <cderr.h>    // FNERR_BUFFERTOOSMALL（导入词库多选缓冲区不足）
#include <uiautomationcore.h>     // UIA 类型（无障碍宿主 provider，2026-10-31）
#include <uiautomationcoreapi.h>  // UiaHostProviderFromHwnd / UiaReturnRawElementProvider

#include <algorithm>
#include <cmath>
#include <cstdarg>
#include <cstdio>
#include <cstring>
#include <cwchar>
#include <map>
#include <string>
#include <vector>

#include "candidate_layout.hpp"
#include "core.hpp"

#ifndef CHAOSET_VERSION
#define CHAOSET_VERSION "dev"
#endif

static HINSTANCE g_hInst = nullptr;

// 无障碍（2026-10-31，基础能力）：候选窗处理 WM_GETOBJECT，向屏幕阅读器返回
// **UIA 宿主 provider**（窗口级：名称/控件类型/位置）。逐条候选的 UIA 子项
// 需要自定义 IRawElementProviderSimple/Fragment，留待 Windows 真机验收后再做。
//
// 动态加载 uiautomationcore.dll：不给 DLL 增加静态导入依赖（精简/旧系统没有
// 该 DLL 时输入法仍可正常使用；只有屏幕阅读器查询时才走这里）。
using UiaHostProviderFromHwndFn = HRESULT(WINAPI*)(HWND, IRawElementProviderSimple**);
using UiaReturnRawElementProviderFn = LRESULT(WINAPI*)(HWND, WPARAM, LPARAM, IRawElementProviderSimple*);
static UiaHostProviderFromHwndFn g_uiaHostProviderFromHwnd = nullptr;
static UiaReturnRawElementProviderFn g_uiaReturnRawElementProvider = nullptr;

static void ensureUiaLoaded() {
    static bool tried = false;
    if (tried) return;
    tried = true;
    HMODULE mod = LoadLibraryW(L"uiautomationcore.dll");
    if (mod == nullptr) return;
    g_uiaHostProviderFromHwnd = reinterpret_cast<UiaHostProviderFromHwndFn>(
        GetProcAddress(mod, "UiaHostProviderFromHwnd"));
    g_uiaReturnRawElementProvider = reinterpret_cast<UiaReturnRawElementProviderFn>(
        GetProcAddress(mod, "UiaReturnRawElementProvider"));
}

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
// 现代 Windows (Windows 8/10/11) 沉浸式与托盘支持能力分类，确保在现代系统设置与 UWP 应用中可见。
//
// **刻意不注册 TFCAT_TIPCAP_UIELEMENTENABLED**（2026-10-30 修）：
// 该类别声明「本 TIP 以 UILess 模式提供候选 UI」——TSF 在沉浸式宿主
// （Windows 11 开始菜单 / 搜索框等）里据此期望 TIP 通过
// ITfUIElementMgr::BeginUIElement 提交 ITfUIElement，由宿主自行渲染。
// 本壳从未实现这些接口（全文件 ITfUIElementMgr / BeginUIElement 出现 0 次），
// 候选窗是进程内自建的 GDI 弹窗——声明与实现矛盾。
// 实机症状：记事本（普通 Win32 宿主）候选窗正常，开始菜单里完全看不到
// （2026-10-30 用户反馈）。撤掉声明让 TSF 按「TIP 自绘候选」的普通路径
// 对待，与实现保持一致。
//
// 保留 IMMERSIVESUPPORT / SYSTRAYSUPPORT：它们只影响「在现代设置与任务栏
// 中可见」，与候选窗渲染路径无关。
// 线程级隔间：键盘开/关（中文输入 = 打开）。Windows 自带的输入指示器
//（任务栏右下的「中/英」）就是读这个值来显示当前模式的——微软拼音、搜狗
// 等输入法都靠它，不需要自建托盘图标（2026-11-05 用户要求补模式提示）。
// 数值取自 Windows SDK 的 msctf.lib（此处按 ReactOS 的 msctf_g.c 抄录，一致）。
const GUID kGUID_COMPARTMENT_KEYBOARD_OPENCLOSE = {
    0x58273aad, 0x01bb, 0x4164, {0x95, 0xc6, 0x75, 0x5b, 0xa0, 0xb5, 0x16, 0x2d}};

const GUID kGUID_TFCAT_TIPCAP_IMMERSIVESUPPORT = {
    0x13a016df, 0x560b, 0x46cd, {0x94, 0x7a, 0x4c, 0x3a, 0xf1, 0xe0, 0xe3, 0x5d}};
const GUID kGUID_TFCAT_TIPCAP_SYSTRAYSUPPORT = {
    0x25504fb4, 0x7bab, 0x4bc1, {0x9c, 0x69, 0xcf, 0x81, 0x89, 0x0f, 0x0e, 0xf5}};

constexpr DWORD kVkShift = 0x10;
constexpr DWORD kVkBackspace = 0x08;
constexpr DWORD kVkDelete = 0x2E;  // VK_DELETE：组词中与 Backspace 同语义（对齐 mac）
constexpr DWORD kVkEscape = 0x1B;
constexpr DWORD kVkReturn = 0x0D;
constexpr DWORD kVkSpace = 0x20;
constexpr DWORD kVkPrior = 0x21;           // Page Up
constexpr DWORD kVkNext = 0x22;            // Page Down
constexpr DWORD kVkFirstLetter = 0x41;  // 'A'
constexpr DWORD kVkLastLetter = 0x5A;   // 'Z'
constexpr DWORD kVkFirstDigit = 0x31;   // '1'
constexpr DWORD kVkLastDigit = 0x39;    // '9'
// 小键盘数字（VK_NUMPAD0..9）。Windows 的大小键盘虚键完全独立，此前只认
// VK_1-9：按小键盘数字落到「不可处理键」清空组词后放行，三端不一致
//（macOS 按字符、Linux 认 KP_1-9，2026-10-30 审计 A7）。
constexpr DWORD kVkNumpad1 = 0x61;      // VK_NUMPAD1
constexpr DWORD kVkNumpad9 = 0x69;      // VK_NUMPAD9
// 每页候选数：与 engine.PageSize() 一致（数字键 1-6 指位）。此前联想分页
// 处散落 6 个字面量，引擎改页容量时会静默漂移（2026-10-30 审计 R20）。
constexpr int kPageSize = 6;
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

// 存活中的模式提示窗数量。提示窗窗口过程同样指向本 DLL 代码；此前没有
// 计数（漏了防卸载保护），且 owner 为空时不会被宿主连带销毁。2026-10-31
// 审计 A4 补计数 + owner 后统一纳入 DllCanUnloadNow。
 volatile LONG g_liveToastWindows = 0;
// 任务栏模式指示器窗口：窗口过程同样指向本 DLL，必须纳入防卸载保护。
volatile LONG g_liveTrayWindows = 0;

// 全屏免打扰（2026-10-31 审计 A7）：D3D 全屏游戏/演示模式下不弹候选窗——
// 置顶候选窗盖在全屏游戏上会闪屏、干扰焦点，用户明确要求游戏场景不能被
// 输入法打扰。只抑制候选窗显示，不改变按键/组词逻辑（WASD 仍会被组词，
// 这是任何中文输入法激活时的固有行为，README 已说明游戏前切英文模式）。
// 注册表 HKCU\Software\Chaoset\FullscreenSuppress（DWORD，缺省 1）可关闭：
// 部分全屏浏览器/视频也会被判定为 D3D 全屏，此时设 0 即可恢复候选窗。
static bool isFullscreenSuppressEnabled() {
    DWORD v = 1;
    DWORD sz = sizeof(v);
    if (RegGetValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"FullscreenSuppress",
                     RRF_RT_REG_DWORD, nullptr, &v, &sz) != ERROR_SUCCESS) {
        v = 1;  // 未配置 = 开启（用户 2026-10-30 明确要求游戏场景优先）
    }
    return v != 0;
}

// 只认「全屏独占 D3D / 演示模式」两种状态；普通最大化、无边框全屏返回
// QUNS_ACCEPTS_NOTIFICATIONS，不在抑制范围（边界见 README 已知限制）。
static bool isFullscreenAppActive() {
    QUERY_USER_NOTIFICATION_STATE state = QUNS_ACCEPTS_NOTIFICATIONS;
    if (FAILED(SHQueryUserNotificationState(&state))) return false;
    return state == QUNS_RUNNING_D3D_FULL_SCREEN || state == QUNS_PRESENTATION_MODE;
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
    COLORREF userMark;  // 学习标记 *（用户数据加权候选）

    // 半透明色在 GDI 里必须预先混合成不透明 RGB（GDI 没有逐字形 alpha）。
    // macOS CandidateTheme 用带 alpha 的 NSColor 实时混合，Windows 侧把
    // 「有效色」固化成常量——公式与最终值必须与 mac 一致，否则同一候选窗
    // 在两端深浅不同（2026-10-30 三端一致性审计 R5/A5）：
    //   有效色 = round(bg*(1-a) + fg*a)，bg 为该色实际叠加的面板/键帽底色。
    // 审计实测的偏差：深色 page/chevron mac #7A7A80 vs 旧值 #98989F（+30）、
    // 深色 chipBorder #838386 vs #78787C、浅色 page #A1A1A6 vs #8E8E93。
    static CandidateColors get(bool dark) {
        if (dark) {
            return {
                RGB(44, 44, 46),       // panelBg #2C2C2E（mac 为 F5 alpha，GDI 侧不透明）
                RGB(74, 74, 76),       // panelBorder：白 #24 alpha 叠加 panelBg → #4A4A4C
                RGB(255, 255, 255),    // text #FFFFFF
                RGB(0, 102, 235),      // highlightBg #0066EB
                RGB(255, 255, 255),    // highlightText #FFFFFF
                RGB(90, 90, 94),       // chipBg #5A5A5E
                RGB(131, 131, 134),    // chipBorder：白 #40 alpha 叠加 chipBg → #838386
                RGB(255, 255, 255),    // chipDigit #FFFFFF
                RGB(69, 143, 240),     // highlightChipBg（0x45 alpha 白叠加在 #0066EB）
                RGB(122, 122, 128),    // pageColor：mac #98989F alpha B8 叠加 panelBg → #7A7A80
                RGB(69, 69, 71),       // divider：白 #1E alpha 叠加 panelBg → #454547
                RGB(122, 122, 128),    // chevron 同 pageColor 有效值 #7A7A80
                RGB(48, 209, 88)       // userMark #30D158（与 skin.UserMarkColor 深色一致）
            };
        }
        return {
            RGB(246, 246, 248),        // panelBg #F6F6F8（mac 为 F6 alpha）
            RGB(221, 221, 223),        // panelBorder：黑 #1A alpha 叠加 panelBg → #DDDDDF
            RGB(29, 29, 31),           // text #1D1D1F（Apple 原生主文本色）
            RGB(0, 122, 255),          // highlightBg #007AFF（Apple 原生蓝胶囊）
            RGB(255, 255, 255),        // highlightText #FFFFFF
            RGB(255, 255, 255),        // chipBg #FFFFFF
            RGB(195, 195, 195),        // chipBorder：黑 #3C alpha 叠加白键帽 → #C3C3C3
            RGB(29, 29, 31),           // chipDigit #1D1D1F
            RGB(69, 158, 255),         // highlightChipBg（0x45 alpha 白叠加在 #007AFF）
            RGB(161, 161, 166),        // pageColor：mac #8E8E93 alpha D0 叠加 panelBg → #A1A1A6
            RGB(226, 226, 228),        // divider：黑 #14 alpha 叠加 panelBg → #E2E2E4（沿用既有值）
            RGB(161, 161, 166),        // chevron 同 pageColor 有效值 #A1A1A6
            RGB(52, 199, 89)           // userMark #34C759（与 skin.UserMarkColor 浅色一致）
        };
    }

    // 把「已按 alpha 混合在 bg 上的有效色」的透明度再按 num/256 缩放：
    // 结果 = bg + (有效色-bg)*num/256。用于单页翻页箭头置灰——macOS
    // 用 chevron.withAlphaComponent(alpha/3)，GDI 没有 alpha，只能按同一
    // 混合几何折算出等效不透明色（2026-10-30 审计 R16）。
    static COLORREF fadeToward(COLORREF fg, COLORREF bg, int num) {
        const int r = GetRValue(bg) + (GetRValue(fg) - GetRValue(bg)) * num / 256;
        const int g = GetGValue(bg) + (GetGValue(fg) - GetGValue(bg)) * num / 256;
        const int b = GetBValue(bg) + (GetBValue(fg) - GetBValue(bg)) * num / 256;
        return RGB(r, g, b);
    }
};

// 描边线宽 pt 化：1pt 在 96dpi 下 MulDiv 四舍五入仍是 1px，125%/150%/200%
// 下为 2/2/3px——与 macOS 的 1pt 描边同口径（2026-10-30 三端一致性审计
// A2：面板/键帽/分隔线/Toast 此前固定 1 设备像素，缩放下系统性偏细
// 25%-50%）。下限钳 1：任何 DPI 下描边都不能消失。
//
// 实现注意：这些 Pen 都是 CreatePen 的 PS_SOLID（非几何笔），没有几何
// 笔的圆头线帽与抗锯齿，高 DPI 下边缘是硬像素；而 mac 是 AppKit 矢量
// 描边（亚像素抗锯齿）。线宽/几何语义正确即可，观感差异属已知平台差异
// （不引入 GDI+ 只为四道描边）。
static int strokePx(int dpi) { return (std::max)(1, MulDiv(1, dpi, 72)); }

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

// 「联想开关变化」跨进程广播消息（2026-10-31）。
//
// 设置窗与候选窗可能在不同宿主进程（用户从 A 应用打开设置窗后切到 B 应用打字），
// 且设置窗**不能**持有 ChaosetTIP*：TSF 随时可能 Release TIP，而设置窗还开着，
// 裸指针就是宿主进程内 UAF（2026-10-30 三端一致性审计 P1-2）。
// 因此用 RegisterWindowMessageW：同一字符串在整个会话内返回同一消息 id，
// HWND_BROADCAST 可送达所有顶层窗口（含各宿主进程里的候选窗），候选窗再转发给
// 同进程的 TIP 收起联想。与 macOS 设置窗 post 通知后立即收起对齐。
UINT associateChangedMessage() {
    static const UINT id = RegisterWindowMessageW(L"Chaoset.AssociateChanged");
    return id;
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

// 浮窗（候选窗/模式提示条）的 owner：**必须是当前前台窗口，且必须是本进程的**。
//
// 2026-11-05 实机问题：只按 hostWindowOf(ctx) 设 owner 时，若 TSF 给的上下文
// 还停留在**另一个窗口**（用户先在别的窗口打过字、或窗口内部焦点抖动），
// owner 就指向了那个窗口。带 owner 的 TOPMOST 窗口显示时会把 owner 所在的
// 窗口组抬到前面，表现为「按 Shift 后弹到另一个窗口，但键盘焦点仍在原输入框」。
// 跨进程 owner 更糟（会把别人进程的窗口带上来），因此一并排除。
//
// 保留 owner 本身是必需的：Windows 11 开始菜单/搜索是沉浸式 z 序 band，
// 无主 topmost 窗口会被压在整个 band 之下（2026-10-30 实机取证）。
// 当前宿主进程是否属于「沉浸式 shell」（开始菜单/搜索/任务栏/文本服务）。
//
// 只有这一类宿主需要 owner：它们自带独立 z 序 band，无主 topmost 窗口会被压在
// 整个 band 之下（2026-10-30 实机取证：SearchApp 里候选窗根本不存在）。
// 普通桌面宿主**不需要** owner，而带 owner 的 TOPMOST 窗口显示时会调整 owner
// 所在窗口组的 z 序——同一进程里有多个窗口时（浏览器多窗口/PWA），用户会看到
// 「按 Shift 后另一个窗口弹到前面」（2026-11-05 用户实机复现：Firefox 两个窗口
// 之间来回切，ownpid==fgpid==selfpid 仍然弹窗）。
static bool shellHostNeedsOwner() {
    static int cached = -1;  // -1 未判定，0 不需要，1 需要
    if (cached >= 0) return cached != 0;
    wchar_t path[MAX_PATH]{};
    bool need = false;
    if (GetModuleFileNameW(nullptr, path, MAX_PATH) > 0) {
        const wchar_t* base = wcsrchr(path, L'\\');
        const wchar_t* name = base != nullptr ? base + 1 : path;
        static const wchar_t* const kShellHosts[] = {
            L"explorer.exe", L"searchapp.exe", L"startmenuexperiencehost.exe",
            L"shellexperiencehost.exe", L"textinputhost.exe", L"applicationframehost.exe",
        };
        for (const wchar_t* host : kShellHosts) {
            if (_wcsicmp(name, host) == 0) {
                need = true;
                break;
            }
        }
    }
    cached = need ? 1 : 0;
    return need;
}

static HWND popupOwner() {
    if (!shellHostNeedsOwner()) return nullptr;
    const HWND fg = GetForegroundWindow();
    if (fg == nullptr) return nullptr;
    DWORD pid = 0;
    GetWindowThreadProcessId(fg, &pid);
    if (pid != GetCurrentProcessId()) return nullptr;
    return fg;
}

class CandidateWindow {
public:
    CandidateWindow() = default;
    ~CandidateWindow() { destroy(); }

    CandidateWindow(const CandidateWindow&) = delete;
    CandidateWindow& operator=(const CandidateWindow&) = delete;

    HWND hwnd() const { return hwnd_; }

    // 设置候选窗 owner（宿主窗口）。窗口已创建时用 GWLP_HWNDPARENT 改 owner
    // （SetWindowLongPtr(hwnd, GWLP_HWNDPARENT) 的语义就是 SetWindowLong 的
    // owner 设置，不是父窗口——WS_POPUP 窗口没有父窗口）。
    // 必须在 show() 之前设置才能作用于 CreateWindowExW；已创建时改 owner
    // 会重建 z 序关系，同样有效。
    void setOwner(HWND owner) {
        if (owner == ownerHwnd_) return;
        ownerHwnd_ = owner;
        if (hwnd_ != nullptr && IsWindow(hwnd_)) {
            SetLastError(0);
            const LONG_PTR prev = SetWindowLongPtrW(hwnd_, GWLP_HWNDPARENT,
                                                    reinterpret_cast<LONG_PTR>(owner));
            // SetWindowLongPtr 返回旧值；失败时返回 0 且 GetLastError 非 0
            //（跨线程/跨完整性级别 owner 可能被拒）。真机排障需要这条日志：
            // owner 没设上时开始菜单/搜索仍会看不到候选窗。
            if (prev == 0 && GetLastError() != 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "candidate setOwner FAILED err=%lu owner=%p\n",
                           static_cast<unsigned long>(GetLastError()),
                           reinterpret_cast<void*>(owner));
            }
        }
    }

    // 外部（TIP）设置：当前输入法是否真的处于激活状态。非激活时**任何**显示
    // 请求都必须被拒绝——候选窗是 WS_EX_TOPMOST 且要接收鼠标点选，绝不能
    // 在后台留着挡住别的应用。2026-11-06 用户反馈「鼠标点系统设置/开始菜单
    // 无效」：TOPMOST 窗口没及时隐藏就会吃掉点击。
    void setActiveGate(bool active) { activeGate_ = active; if (!active) hide(); }

    void show(HINSTANCE hinst, POINT pt, int lineH, const chaoset::Comp& comp, ChaosetTIP* tip) {
        if (!activeGate_) {
            // 非激活：一律不显示（也不建窗），避免任何意义上抢屏幕/挡点击。
            hide();
            return;
        }
        if (comp.candidates.empty()) {
            hide();
            return;
        }
        // 全屏免打扰：D3D 全屏游戏/演示模式下不显示候选窗（注册表可关，
        // 见 isFullscreenSuppressEnabled）。只抑制显示，不改按键/组词。
        if (isFullscreenSuppressEnabled() && isFullscreenAppActive()) {
            hide();
            if (diagBudget()) {
                diagPrintf(L"tsf.log", "candidate suppressed (fullscreen) anchor=%ld,%ld\n",
                           static_cast<long>(pt.x), static_cast<long>(pt.y));
            }
            return;
        }
        comp_ = comp;
        tip_ = tip;
        targetPos_ = pt;
        targetLineHeight_ = lineH;
        ensureWindow(hinst);
        if (hwnd_ == nullptr) {
            // 创建失败此前完全静默：开始菜单「看不到候选窗」时无法区分
            // 「没建出来」与「建了但被盖住/跑到屏幕外」（2026-10-30 排障）。
            if (diagBudget()) {
                diagPrintf(L"tsf.log", "candidate window create FAILED err=%lu anchor=%ld,%ld\n",
                           static_cast<unsigned long>(GetLastError()),
                           static_cast<long>(pt.x), static_cast<long>(pt.y));
            }
            return;
        }

        layoutAndPosition();
        InvalidateRect(hwnd_, nullptr, TRUE);
        ShowWindow(hwnd_, SW_SHOWNOACTIVATE);
        // 记录最终落点与可见性：ShowWindow 成功但 IsWindowVisible 为假，
        // 或落点在所有显示器工作区之外，都能在这里看出来。
        if (diagBudget()) {
            RECT wr{};
            GetWindowRect(hwnd_, &wr);
            HMONITOR hMon = MonitorFromWindow(hwnd_, MONITOR_DEFAULTTONULL);
            // ownerpid / fgpid：定位「打字窗口与 owner 窗口不是同一个」时
            // 带 owner 的 TOPMOST 窗口会把 owner 所在窗口组抬到前面
            //（用户反馈 2026-11-05：按 Shift 后弹到另一个窗口而焦点仍在输入框）。
            const HWND own = GetWindow(hwnd_, GW_OWNER);
            DWORD ownPid = 0;
            if (own != nullptr) GetWindowThreadProcessId(own, &ownPid);
            const HWND fg = GetForegroundWindow();
            DWORD fgPid = 0;
            if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
            diagPrintf(L"tsf.log",
                       "candidate shown pos=%ld,%ld size=%ldx%ld visible=%d monitor=%d "
                       "anchor=%ld,%ld ownpid=%lu fgpid=%lu selfpid=%lu\n",
                       static_cast<long>(wr.left), static_cast<long>(wr.top),
                       static_cast<long>(wr.right - wr.left), static_cast<long>(wr.bottom - wr.top),
                       IsWindowVisible(hwnd_) ? 1 : 0, hMon != nullptr ? 1 : 0,
                       static_cast<long>(pt.x), static_cast<long>(pt.y),
                       static_cast<unsigned long>(ownPid), static_cast<unsigned long>(fgPid),
                       static_cast<unsigned long>(GetCurrentProcessId()));
        }
    }

    void hide() {
        if (hwnd_ != nullptr && IsWindow(hwnd_)) {
            ShowWindow(hwnd_, SW_HIDE);
        }
    }

    void destroy() {
        if (hwnd_ != nullptr) {
            // 主动销毁走 DestroyWindow：WM_NCDESTROY 里统一回收（见
            // onWindowDestroyed），避免这里和窗口过程各减一次计数。
            // DestroyWindow 失败（跨线程等）时兜底回收，防止计数泄漏。
            if (!IsWindow(hwnd_) || !DestroyWindow(hwnd_)) onWindowDestroyed();
        }
        tip_ = nullptr;
    }

    // 窗口被销毁时统一回收：无论是我们主动 DestroyWindow，还是 owner 宿主
    // 窗口销毁时系统连带销毁 owned 候选窗（2026-10-31 审计 A1）。此前
    // WM_DESTROY 是空 case，owner 连带销毁路径不会清 hwnd_、也不递减
    // g_liveCandidateWindows——同一 TIP 线程内关掉一个窗口再到另一个窗口
    // 打字时，ensureWindow 会重建窗口并再次 ++，计数永久 >0，
    // DllCanUnloadNow 恒 S_FALSE（升级时旧 DLL 无法卸载）。
    void onWindowDestroyed() {
        if (hwnd_ != nullptr) {
            hwnd_ = nullptr;
            // 窗口过程已失效，此时才允许 DLL 被卸载（见 DllCanUnloadNow）
            InterlockedDecrement(&g_liveCandidateWindows);
        }
        // 清掉裸指针，避免窗口已销毁却仍被引用（审查指出的悬垂风险）
        tip_ = nullptr;
    }

    void onLButtonDown(int x, int y);
    // 右键候选行：弹出上下文菜单「删除该词」（先记录命中项，菜单返回后
    // 由 TIP 删除学习记录并刷新候选；详见文件后部定义与 docs/ui-parity.md）。
    void onRButtonUp(int x, int y);

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
        // 学习标记：daemon Candidate.FromUser → 文本后绿色「*」。
        // fromUser 来自 core::Candidate（IPC "from_user"），markW 是布局宽度
        //（"*" 文本宽 + 2pt，含绘制起点 +1pt），与 mac markW 同口径。
        bool fromUser = false;
        int markW = 0;
    };

    // 候选窗按**锚点（光标）所在显示器**的 DPI 布局，而不是主屏 DPI：
    // 混合 DPI 多屏下候选窗落到副屏时尺寸/字号才正确（2026-10-31）。
    int getDpi() const { return layoutDpi_ > 0 ? layoutDpi_ : systemDpi(); }
    int layoutDpi_ = 0;  // layoutAndPosition 时按 targetPos_ 计算并缓存，onPaint 复用

    // 候选窗统一以 **pt** 为布局单位：MulDiv(pt, dpi, 72)，与 macOS
    // CandidatePanel.Layout 的 pt 值 1:1（2026-10-30 三端一致性整改 A1）。
    //
    // 历史：字号一直用 MulDiv(v,dpi,72)（pt→px），而间距/键帽/圆角/最大宽
    // 用 MulDiv(v,dpi,96)（px@96）——同一面板两套换算基准，Windows 上「字
    // 一样大、留白和键帽小 25%」，最大宽只有 macOS 的 0.75（候选更容易被
    // 丢弃/截断）。现在全部改成 pt，各 DPI 下的实际像素值（MulDiv 四舍五入）：
    //
    //   参数             pt    96dpi  120dpi  144dpi  192dpi
    //   padding           7      9      12      14      19
    //   firstPadLeft      9     12      15      18      24
    //   itemPadX          7      9      12      14      19
    //   gap              11     15      18      22      29
    //   chipPadX          4      5       7       8      11
    //   chipPadY          2      3       3       4       5
    //   pillMargin        2      3       3       4       5
    //   chevronBoxW      36     48      60      72      96
    //   gearBoxW         26     35      43      52      69
    //   panelRadius       8     11      13      16      21（RoundRect 传直径 16pt）
    //   textH            21     28      35      42      56
    //   maxWidth        720    960    1200    1440    1920
    //   minWidth         84    112     140     168     224
    //   caret gap         6      8      10      12      16
    //
    // 均为正整数像素且远小于 720pt 上限；窗口定位仍按显示器 rcWork 钳制，
    // 面板不会溢出屏幕。截断判定统一用「内容宽度上限 638pt」的 pt 换算值
    // （见 layoutAndPosition 的 maxContent），与 mac 同口径。
    static int pt(int v, int dpi) { return MulDiv(v, dpi, 72); }

    bool isInteractiveArea() const {
        POINT pt;
        if (GetCursorPos(&pt) && hwnd_ != nullptr && IsWindow(hwnd_)) {
            ScreenToClient(hwnd_, &pt);
            for (const auto& it : items_) {
                if (PtInRect(&it.itemRect, pt)) return true;
            }
            if (comp_.pageCount > 1 && PtInRect(&chevronRect_, pt)) return true;
        }
        return false;
    }

    // 设置窗关闭联想后的跨进程广播处理（见 associateChangedMessage）。
    // 实现在文件后部（需要 ChaosetTIP 完整定义才能转发 clearAssociate）。
    void onAssociateChanged();

    void ensureWindow(HINSTANCE hinst) {
        if (hwnd_ != nullptr) {
            if (IsWindow(hwnd_)) return;
            // 句柄失效：owner 宿主窗口销毁时 owned 候选窗被系统连带销毁。
            // 先归还计数再重建，否则每重建一次计数 +1（2026-10-31 审计 A1）。
            onWindowDestroyed();
        }

        const std::wstring cls = windowClassName(L"ChaosetCandidateWndClass", hinst);
        WNDCLASSEXW wc{};
        wc.cbSize = sizeof(wc);
        wc.style = CS_DROPSHADOW | CS_HREDRAW | CS_VREDRAW;
        wc.lpfnWndProc = CandidateWndProc;
        wc.hInstance = hinst;
        wc.hCursor = LoadCursorW(nullptr, MAKEINTRESOURCEW(32512));
        wc.lpszClassName = cls.c_str();
        // 注册失败原因此前被丢弃。ERROR_CLASS_ALREADY_EXISTS 属正常复用
        // （同名类已由本模块注册），其它错误码则意味着窗口注定创建失败。
        if (RegisterClassExW(&wc) == 0 && diagBudget()) {
            const DWORD err = GetLastError();
            if (err != ERROR_CLASS_ALREADY_EXISTS) {
                diagPrintf(L"tsf.log", "RegisterClassEx failed err=%lu\n",
                           static_cast<unsigned long>(err));
            }
        }

        // 层级设计（三端对照，2026-10-28 复核）：候选窗保持 TOPMOST——
        // 微软自带拼音与搜狗等输入法的候选窗同样是置顶 popup。Windows 的
        // 下拉菜单是系统模态的 topmost popup，弹出时位于 Z 序最顶端且
        // 独占鼠标（模态菜单循环），候选窗 frame 即使重叠也不会拦截菜单
        // 点击——与 macOS 不同（NSPanel.popUpMenu 层级会真的压住菜单栏
        // 拦截点击，macOS 端因此降级为 floating）。去掉 TOPMOST 会让
        // 候选窗被宿主文档窗口盖住，不可行。
        // 全链路防拦截：WS_EX_NOACTIVATE 不抢焦点 + 失焦即收候选窗
        // （OnSetFocus(FALSE)），点击其它应用时候选窗先收起再切换。
        //
        // **owner 窗口**（2026-10-30 开始菜单排障）：owner 此前恒为 nullptr，
        // 候选窗是无主顶层窗口，仅靠 WS_EX_TOPMOST 维持可见性。普通桌面宿主
        // （记事本/浏览器/终端）够用，但 Windows 11 开始菜单/搜索这类**沉浸式
        // band** 窗口自带独立 z 序层级，无主 topmost 窗口会被压在整个 band 之下。
        // 实机取证（2026-10-30 用户机器枚举）：SearchApp 里候选窗根本不存在，
        // 而 firefox/WindowsTerminal 里存在且 Owner=0x0——说明 owner 缺失与
        // 「只有沉浸式宿主看不到」高度相关。
        // owner 设为宿主窗口后，owned window 恒在其 owner 之上，可跨 band 显示
        //（微软拼音与搜狗候选窗同样以宿主窗口为 owner）。
        // **不要**在这里兜底取 GetForegroundWindow()（2026-11-05 实机根因）：
        // 对普通宿主 popupOwner() 返回 nullptr，旧代码这里又回退到前台窗口，
        // 等于绕过了「只给沉浸式 shell 宿主设 owner」的规则。带 owner 的
        // TOPMOST 窗口一显示就会把 owner 所在窗口组抬到前面——Chrome/Firefox
        // 多窗口时表现为「按 Shift 后另一个窗口弹上来，焦点却不变」。
        // 实机证据：chrome pid=5372 的 candidate shown ownpid=5372（本进程窗口），
        // 而同进程的 toast shown ownpid=0（提示条没有这条兜底，所以不弹）。
        HWND owner = ownerHwnd_;
        if (owner == nullptr || !IsWindow(owner)) owner = popupOwner();
        hwnd_ = CreateWindowExW(
            WS_EX_TOOLWINDOW | WS_EX_TOPMOST | WS_EX_NOACTIVATE,
            cls.c_str(),
            L"Chaoset 候选",
            WS_POPUP,
            0, 0, 0, 0,
            owner, nullptr, hinst, nullptr
        );
        if (hwnd_ != nullptr) {
            SetWindowLongPtrW(hwnd_, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(this));
            // 窗口过程现在指向本 DLL 的代码：只要它还活着，DLL 就不能被卸载
            InterlockedIncrement(&g_liveCandidateWindows);
        }
    }

    static LRESULT CALLBACK CandidateWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
        CandidateWindow* self = reinterpret_cast<CandidateWindow*>(GetWindowLongPtrW(hwnd, GWLP_USERDATA));
        if (msg == associateChangedMessage()) {
            if (self != nullptr) self->onAssociateChanged();
            return 0;
        }
        switch (msg) {
            case WM_GETOBJECT:
                // 屏幕阅读器（Narrator/NVDA）查询窗口无障碍对象：返回 UIA 宿主
                // provider（窗口级）。只处理根对象 id；其它对象交给 DefWindowProc。
                if (static_cast<LONG>(lParam) == UiaRootObjectId) {
                    ensureUiaLoaded();
                    IRawElementProviderSimple* provider = nullptr;
                    if (g_uiaHostProviderFromHwnd != nullptr &&
                        g_uiaReturnRawElementProvider != nullptr &&
                        SUCCEEDED(g_uiaHostProviderFromHwnd(hwnd, &provider)) &&
                        provider != nullptr) {
                        LRESULT result = g_uiaReturnRawElementProvider(hwnd, wParam, lParam, provider);
                        provider->Release();
                        return result;
                    }
                }
                break;
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
            case WM_RBUTTONUP:
                // 右键候选行 → 弹出「删除该词」菜单（macOS CandidatePanel 的
                // menu(for:) 同语义）。右键只走菜单，不触发左键点选/上屏。
                if (self != nullptr) {
                    self->onRButtonUp(static_cast<short>(LOWORD(lParam)), static_cast<short>(HIWORD(lParam)));
                    return 0;
                }
                break;
            case WM_SETCURSOR:
                if (self != nullptr && self->isInteractiveArea()) {
                    SetCursor(LoadCursorW(nullptr, MAKEINTRESOURCEW(32649)));  // IDC_HAND
                    return TRUE;
                }
                break;
            case WM_SETTINGCHANGE:
            case WM_THEMECHANGED:
                // 系统深浅色/主题切换：onPaint 每帧按 isSystemDarkTheme() 取色，
                // 但窗口不会自动重绘——候选窗会保持旧配色直到下次输入
                // （2026-10-31 审计：Windows 主题切换不重绘）。立即失效重绘；
                // 布局不依赖主题，无需重新 layoutAndPosition。
                if (self != nullptr) InvalidateRect(hwnd, nullptr, TRUE);
                break;
            case WM_DPICHANGED:
                // 窗口跨屏（不同缩放）后重新布局：DPI 仍以锚点所在显示器为准。
                if (self != nullptr) {
                    self->layoutAndPosition();
                    InvalidateRect(hwnd, nullptr, TRUE);
                }
                break;
            case WM_NCDESTROY:
                // 主动销毁与 owner 连带销毁的统一回收点（见 onWindowDestroyed）。
                // 必须在 DefWindowProc 之前取 self（WM_NCDESTROY 之后 USERDATA 失效）。
                if (self != nullptr) self->onWindowDestroyed();
                break;
        }
        return DefWindowProcW(hwnd, msg, wParam, lParam);
    }

    void layoutAndPosition() {
        const int dpi = dpiForPoint(targetPos_);
        layoutDpi_ = dpi;
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

        // 排版参数（与 macOS Layout 1:1；统一 pt 口径，见 pt() 注释）
        const int pad = pt(7, dpi);
        const int firstPadLeft = pt(9, dpi);
        const int itemPadX = pt(7, dpi);
        const int gap = pt(11, dpi);
        const int chipPadX = pt(4, dpi);
        const int chipPadY = pt(2, dpi);
        const int pillMargin = pt(2, dpi);
        const int chevronBoxW = pt(36, dpi);
        const int gearBoxW = pt(26, dpi);

        TEXTMETRICW tmText{}, tmIdx{};
        SelectObject(memDC, fontNormal);
        GetTextMetricsW(memDC, &tmText);
        SelectObject(memDC, fontIndex);
        GetTextMetricsW(memDC, &tmIdx);

        // 行高：macOS Layout.textH = ceil(fontSize*1.4) = 21pt（CJK 安全值，
        // 见 CandidatePanel.swift 注释：GDI/AppKit 西文 metrics 不含中文字形
        // 上下延展，按它算会把第二行裁半行）。取 tmHeight 与 21pt 的较大者。
        int textH = (std::max)(static_cast<int>(tmText.tmHeight), pt(21, dpi));
        int idxH = tmIdx.tmHeight;
        int chipH = idxH + chipPadY * 2;
        int contentH = (std::max)(textH, chipH);
        // 多行折行布局（2026-10-31 与 macOS CandidatePanel.layoutRows 对齐）：
        // 逐项测量 → candidate_layout.hpp 纯布局函数折行 → 回填各 rect。
        // 折行上限 3 行（mac Layout.maxRows）；第 3 行之后的候选不参与布局
        //（与 mac 一致：数字键仍可能选中不可见项，文档记录为已知边角）。
        items_.clear();

        // 面板最大内容宽度：与 macOS CandidatePanel.layoutRows 的公式逐项对齐
        // （2026-10-30 审计 A11）：窗宽上限 720pt，扣内边距*2、翻页区 36pt、
        // 齿轮区 26pt、首选胶囊外扩 pillMargin*2、2pt 安全余量，得 638pt。
        constexpr int kMaxWidthPt = 720;
        constexpr int kMaxContentPt = kMaxWidthPt - 7 * 2 - 36 - 26 - 2 * 2 - 2;  // 638pt
        constexpr int kMaxRows = 3;  // mac Layout.maxRows
        const int maxContent = pt(kMaxContentPt, dpi);
        const int lineSpacing = pt(4, dpi);  // mac Layout.lineSpacing
        const wchar_t* kEllipsis = L"…";

        std::vector<chaoset::layout::Box> boxes;
        std::vector<ItemPos> measured;
        boxes.reserve(comp_.candidates.size());
        measured.reserve(comp_.candidates.size());
        for (std::size_t i = 0; i < comp_.candidates.size(); ++i) {
            ItemPos item;
            item.index = static_cast<int>(i);
            item.isFirst = (i == 0);
            item.fromUser = comp_.candidates[i].fromUser;
            item.indexStr = std::to_wstring(i + 1);
            item.textStr = toWide(comp_.candidates[i].text);

            int padX = item.isFirst ? firstPadLeft : itemPadX;

            SelectObject(memDC, fontIndex);
            SIZE szIdx{};
            GetTextExtentPoint32W(memDC, item.indexStr.c_str(), static_cast<int>(item.indexStr.size()), &szIdx);

            SelectObject(memDC, fontNormal);
            SIZE szTxt{};
            GetTextExtentPoint32W(memDC, item.textStr.c_str(), static_cast<int>(item.textStr.size()), &szTxt);

            // 学习标记宽度：与 mac 同口径——"*"（正文字体）宽度 + 2pt
            //（CandidatePanel.layoutRows 的 markW = ceil("*"宽度)+2；
            // draw 时在文本后 +1pt 起画，advance 加上该 markW）。
            int markW = 0;
            if (item.fromUser) {
                SIZE szMark{};
                GetTextExtentPoint32W(memDC, L"*", 1, &szMark);
                markW = szMark.cx + pt(2, dpi);
            }

            int chipW = szIdx.cx + chipPadX * 2;
            int chipTextGap = (std::max)(pt(3, dpi), static_cast<int>(szIdx.cx * 45 / 100));

            // 任意单条候选自身超宽都要截断（不只首项）：折行只解决「多项组合
            // 超宽」，单条 > maxContent 时换行也放不下，必须逐字截断加…。
            // 与 mac layoutRows 的「rowFrames.isEmpty && boxW > maxContent」
            // 等价——能超过 maxContent 的项必然独占一行。
            const int itemW = padX * 2 + chipW + chipTextGap + szTxt.cx + markW;
            if (itemW > maxContent) {
                const int avail = (std::max)(
                    maxContent - padX * 2 - chipW - chipTextGap - markW, pt(20, dpi));
                if (szTxt.cx > avail) {
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
                }
            }

            chaoset::layout::Box box;
            box.padX = padX;
            box.chipW = chipW;
            box.chipTextGap = chipTextGap;
            box.textW = szTxt.cx;
            box.markW = markW;
            boxes.push_back(box);
            measured.push_back(std::move(item));
        }

        const auto rowsLayout = chaoset::layout::wrapRows(boxes, maxContent, gap, contentH, kMaxRows);
        int rowCount = static_cast<int>(rowsLayout.rows.size());
        if (rowCount == 0) rowCount = 1;  // 无候选时保留一行高度（同 mac minHeight）
        const int rowH = contentH + lineSpacing;
        int contentW = 0;
        for (const auto& row : rowsLayout.rows) contentW = (std::max)(contentW, row.width);
        int winH = pad * 2 + rowCount * rowH - lineSpacing;
        winH = (std::max)(winH, pt(20, dpi));

        std::size_t flat = 0;
        for (int r = 0; r < static_cast<int>(rowsLayout.rows.size()); ++r) {
            const auto& row = rowsLayout.rows[static_cast<std::size_t>(r)];
            const int rowTop = pad + r * rowH;
            for (const auto& boxRect : row.items) {
                if (flat >= measured.size() || flat >= boxes.size()) break;
                ItemPos item = std::move(measured[flat]);
                const chaoset::layout::Box& box = boxes[flat];
                const int x = pad + boxRect.x;
                ++flat;

                item.itemRect.left = x;
                item.itemRect.top = rowTop;
                item.itemRect.right = x + boxRect.w;
                item.itemRect.bottom = rowTop + contentH;

                if (item.isFirst) {
                    item.pillRect.left = x - pillMargin;
                    item.pillRect.top = rowTop - pillMargin;
                    item.pillRect.right = x + boxRect.w + pillMargin;
                    item.pillRect.bottom = rowTop + contentH + pillMargin;
                }

                const int chipLeft = x + box.padX;
                const int chipTop = rowTop + (contentH - chipH) / 2;
                item.chipRect.left = chipLeft;
                item.chipRect.top = chipTop;
                item.chipRect.right = chipLeft + box.chipW;
                item.chipRect.bottom = chipTop + chipH;

                const int textLeft = item.chipRect.right + box.chipTextGap;
                const int textTop = rowTop + (contentH - tmText.tmHeight) / 2;
                item.textRect.left = textLeft;
                item.textRect.top = textTop;
                item.textRect.right = textLeft + box.textW;
                item.textRect.bottom = textTop + tmText.tmHeight;

                item.markW = box.markW;  // onPaint 画学习标记用（0 = 未加权）
                items_.push_back(std::move(item));
            }
        }

        // 设置齿轮区（参考微信输入法候选栏右侧的设置入口）：折叠区左侧齿轮，
        // 点击打开设置窗口（词组联想开关）。宽度与 mac SettingsGear.boxW=26pt 一致。
        int totalW = pad * 2 + contentW + chevronBoxW + gearBoxW;
        // 最小窗宽与 mac contentSize 的 minW 同公式：
        // padding*2 + chevronBoxW + gearBoxW + 8pt（2026-10-30 审计 R10：此前
        // Windows 用 120px@96=90pt，比 mac 的 84pt 宽，不一致）。
        int minW = pad * 2 + chevronBoxW + gearBoxW + pt(8, dpi);
        int winW = (std::max)(totalW, minW);

        chevronRect_.left = winW - chevronBoxW;
        chevronRect_.top = 0;
        chevronRect_.right = winW;
        chevronRect_.bottom = winH;

        settingsRect_.left = winW - chevronBoxW - gearBoxW;
        settingsRect_.top = 0;
        settingsRect_.right = winW - chevronBoxW;
        settingsRect_.bottom = winH;

        dividerX_ = settingsRect_.left + pt(2, dpi);

        SelectObject(memDC, oldFont);
        DeleteDC(memDC);
        ReleaseDC(nullptr, screenDC);
        DeleteObject(fontNormal);
        DeleteObject(fontIndex);

        // 定位：与 mac candidateOrigin 同语义——光标行下方 gap 6pt，
        // 下方放不下且上方空间更大时改放上方，最后用工作区 rcWork 钳制。
        // 2026-10-30 审计 R21：此前下方间距 4px@96（3pt）、无「上方更大」
        // 判据，与 mac 的 6pt 不一致。
        POINT anchor = targetPos_;
        int lineH = targetLineHeight_ > 0 ? targetLineHeight_ : pt(20, dpi);
        const int caretGap = pt(6, dpi);
        const int edgeGap = pt(4, dpi);  // 屏幕边缘安全余量（mac 用 visibleFrame 钳制）
        int winX = anchor.x;
        int winY = anchor.y + caretGap;

        HMONITOR hMon = MonitorFromPoint(anchor, MONITOR_DEFAULTTONEAREST);
        MONITORINFO mi{};
        mi.cbSize = sizeof(mi);
        if (GetMonitorInfoW(hMon, &mi)) {
            RECT work = mi.rcWork;
            if (winX + winW > work.right) {
                winX = work.right - winW - edgeGap;
            }
            if (winX < work.left) {
                winX = work.left + edgeGap;
            }
            const int spaceBelow = work.bottom - (anchor.y + caretGap);
            const int spaceAbove = anchor.y - work.top;
            if (winH > spaceBelow && spaceAbove > spaceBelow) {
                winY = anchor.y - lineH - winH - caretGap;
            }
            if (winY + winH > work.bottom) {
                winY = work.bottom - winH - edgeGap;
            }
            if (winY < work.top) {
                winY = work.top + edgeGap;
            }
        }

        SetWindowRgn(hwnd_, CreateRoundRectRgn(0, 0, winW + 1, winH + 1, pt(16, dpi), pt(16, dpi)), TRUE);
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

        // 面板背景圆角与 1pt 细边框（pt 化，见 strokePx）
        HBRUSH bgBrush = CreateSolidBrush(theme.panelBg);
        HPEN borderPen = CreatePen(PS_SOLID, strokePx(dpi), theme.panelBorder);
        HGDIOBJ oldBrush = SelectObject(memDC, bgBrush);
        HGDIOBJ oldPen = SelectObject(memDC, borderPen);
        RoundRect(memDC, 0, 0, w, h, pt(16, dpi), pt(16, dpi));

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
                          pt(12, dpi), pt(12, dpi));
                SelectObject(memDC, prevBrush);
                SelectObject(memDC, prevPen);
                DeleteObject(pillBrush);

                // 蓝胶囊内键帽：半透白单层填充，无边框
                HBRUSH chipBrush = CreateSolidBrush(theme.highlightChipBg);
                prevBrush = SelectObject(memDC, chipBrush);
                prevPen = SelectObject(memDC, GetStockObject(NULL_PEN));
                RoundRect(memDC, item.chipRect.left, item.chipRect.top,
                          item.chipRect.right, item.chipRect.bottom,
                          pt(6, dpi), pt(6, dpi));
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

                // 学习标记 *：用户数据加权候选（与 mac theme.userMark 同色，
                // 画在文本后 +1pt、宽 markW，与 CandidatePanel.draw 同口径）
                if (item.fromUser) {
                    SetTextColor(memDC, theme.userMark);
                    RECT markRc = item.textRect;
                    markRc.left = item.textRect.right + pt(1, dpi);
                    markRc.right = markRc.left + item.markW;
                    DrawTextW(memDC, L"*", 1, &markRc, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
                }
            } else {
                // 次选键帽：外边框 + 内部底色
                HBRUSH chipBrush = CreateSolidBrush(theme.chipBg);
                HPEN chipPen = CreatePen(PS_SOLID, strokePx(dpi), theme.chipBorder);
                HGDIOBJ prevBrush = SelectObject(memDC, chipBrush);
                HGDIOBJ prevPen = chipPen != nullptr ? SelectObject(memDC, chipPen) : nullptr;
                RoundRect(memDC, item.chipRect.left, item.chipRect.top,
                          item.chipRect.right, item.chipRect.bottom,
                          pt(6, dpi), pt(6, dpi));
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

                // 学习标记 *（见首选分支注释）
                if (item.fromUser) {
                    SetTextColor(memDC, theme.userMark);
                    RECT markRc = item.textRect;
                    markRc.left = item.textRect.right + pt(1, dpi);
                    markRc.right = markRc.left + item.markW;
                    DrawTextW(memDC, L"*", 1, &markRc, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
                }
            }
        }

        // 绘制右侧折叠区：垂直分割线 + 设置齿轮 + 翻页箭头 chevron + 页码。
        // 全部按 pt 口径（与 macOS CandidatePanel.draw 同几何）。
        //
        // 分割线高度：mac Layout.textH*55% = 21pt*0.55 = 11.55pt（此前用整个
        // 窗高的 55%，高约 21-22px≈16pt，明显偏长——2026-10-30 审计 R18/A6）。
        const int textH = pt(21, dpi);  // 与 CandidatePanel Layout.textH 一致
        const int divH = textH * 55 / 100;
        const int divY = (h - divH) / 2;
        HPEN divPen = CreatePen(PS_SOLID, strokePx(dpi), theme.divider);
        HGDIOBJ prevPen = divPen != nullptr ? SelectObject(memDC, divPen) : nullptr;
        MoveToEx(memDC, dividerX_, divY, nullptr);
        LineTo(memDC, dividerX_, divY + divH);
        if (prevPen != nullptr) SelectObject(memDC, prevPen);
        if (divPen != nullptr) DeleteObject(divPen);

        // 设置齿轮（分割线右侧、翻页箭头左侧）：**与 macOS 端完全相同的
        // 矢量算法**（中心圆环 r3.4pt/线宽1.8pt + 8 齿辐条 4.6pt→7.2pt/
        // 线宽2pt 圆头线帽——几何参数与色值逐一对应，mac/Windows 两端视觉
        // 一致，2026-10-28 用户拍板；Linux 用 ibus 原生面板无齿轮，见
        // shims/linux/README.md）。颜色直接用 theme.chipDigit（与 mac
        // theme.chipDigit 相同：暗色纯白/浅色 #1D1D1F），不再硬编码两份。
        // GDI 几何笔（ExtCreatePen PS_GEOMETRIC）才有圆头线帽与真实线宽。
        {
            const COLORREF gearColor = theme.chipDigit;
            const double u = dpi / 72.0; // mac 端 pt 值 → 设备像素
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

        // 折叠子区（齿轮右侧的翻页/页码区）：chevron 与页码的水平中心都取
        // 该子区中点——与 mac CandidatePanel.draw 的 flipZoneX/chevX 公式一致
        // （flipZoneX = dividerX + gearBoxW，chevX = flipZoneX + (w-flipZoneX)/2）。
        // 2026-10-30 审计 R17：此前页码 RECT 从 dividerX_ 到窗口右缘居中，
        // 中心落在 w-30，比 mac 的 chevX 左移约 13px，且与代码自身注释矛盾。
        const int flipZoneX = dividerX_ + (settingsRect_.right - settingsRect_.left);
        const int chevX = flipZoneX + (w - flipZoneX) / 2;
        bool hasNext = comp_.page + 1 < comp_.pageCount;
        bool hasPrev = comp_.page > 0;
        bool dirDown = hasNext || !hasPrev;
        std::wstring pageStr;
        int pageW = 0;
        int pageH = 0;
        if (comp_.pageCount > 1) {
            pageStr = std::to_wstring(comp_.page + 1) + L"/" + std::to_wstring(comp_.pageCount);
            SelectObject(memDC, fontIndex);
            SIZE szPage{};
            GetTextExtentPoint32W(memDC, pageStr.c_str(), static_cast<int>(pageStr.size()), &szPage);
            pageW = szPage.cx;
            pageH = szPage.cy;
        }
        // blockH = 箭头块 8pt + 3pt 间隔 + 页码高；整体垂直居中（mac blockTop）。
        const int blockH = pt(8, dpi) + (comp_.pageCount > 1 ? pt(3, dpi) + pageH : 0);
        const int blockTop = (h - blockH) / 2;
        const int arm = pt(3, dpi);
        const int chevY = blockTop + pt(4, dpi);
        const int chevDy = pt(2, dpi);  // mac arm*0.7 ≈ 2.1pt
        // 单页置灰：mac chevron.withAlphaComponent(alpha/3)；GDI 无 alpha，按
        // 同一混合几何折算为向面板底色回退 2/3 的等效不透明色（保留 1/3）。
        const COLORREF chevronColor = (comp_.pageCount <= 1)
            ? CandidateColors::fadeToward(theme.chevron, theme.panelBg, 85)
            : theme.chevron;
        // mac 线宽 1.5pt：pt(3)/2 四舍五入，保证任意 DPI 下 ≥1px。
        int chevPenW = (pt(3, dpi) + 1) / 2;
        if (chevPenW < 1) chevPenW = 1;
        HPEN chevPen = CreatePen(PS_SOLID, chevPenW, chevronColor);
        prevPen = chevPen != nullptr ? SelectObject(memDC, chevPen) : nullptr;
        if (dirDown) {
            MoveToEx(memDC, chevX - arm, chevY - chevDy, nullptr);
            LineTo(memDC, chevX, chevY + chevDy);
            LineTo(memDC, chevX + arm, chevY - chevDy);
        } else {
            MoveToEx(memDC, chevX - arm, chevY + chevDy, nullptr);
            LineTo(memDC, chevX, chevY - chevDy);
            LineTo(memDC, chevX + arm, chevY + chevDy);
        }
        if (prevPen != nullptr) SelectObject(memDC, prevPen);
        if (chevPen != nullptr) DeleteObject(chevPen);

        // 页码：水平居中于 chevron（mac x = chevX - pageW/2），
        // 垂直位置 = blockTop + 8pt + 3pt。
        if (comp_.pageCount > 1) {
            RECT pageRc{ chevX - pageW / 2, blockTop + pt(11, dpi),
                         chevX - pageW / 2 + pageW, blockTop + pt(11, dpi) + pageH + 2 };
            SetTextColor(memDC, theme.pageColor);
            SelectObject(memDC, fontIndex);
            DrawTextW(memDC, pageStr.c_str(), -1, &pageRc,
                      DT_LEFT | DT_TOP | DT_SINGLELINE);
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
    // 只在输入法真正激活时才允许显示（见 setActiveGate）。默认 false：
    // 建窗/Activate 之前不存在任何窗口，从根上杜绝「后台 app 里留一个
    // TOPMOST 窗口挡鼠标」。
    bool activeGate_ = false;
    // 候选窗的 owner 窗口（宿主窗口）。非空时候选窗是其 owned window，
    // 恒在 owner 之上，可跨越 Windows 11 开始菜单/搜索的沉浸式 z 序 band。
    // 由 show() 的调用方经 setOwner() 设置（来源见 ChaosetTIP::render 里
    // 的 ITfContextView::GetWnd 探测）。
    HWND ownerHwnd_ = nullptr;
    chaoset::Comp comp_;
    ChaosetTIP* tip_ = nullptr;
    POINT targetPos_{0, 0};
    int targetLineHeight_ = 20;

    std::vector<ItemPos> items_;
    RECT chevronRect_{};
    int dividerX_ = 0;
    RECT settingsRect_{}; // 设置齿轮命中区
};

// ---- 传输：Windows 命名管道 ----
//
// 壳与 daemon 之间唯一的通道：\\.\pipe\chaoset-<SID>（daemon 用 `-pipe` 启动，
// 管道名按当前用户 SID 生成）。命名管道无端口、ACL 限当前用户 + AppContainer
// 包；没有第二传输、没有回退——管道连不上即 daemon 不可达，按键原样放行。
class PipeTransport : public chaoset::Transport {
public:
    PipeTransport() : pipeName_(currentUserPipeName()) {}
    ~PipeTransport() override { closeConn(); }

    bool isConnected() const { return pipe_ != INVALID_HANDLE_VALUE; }

    bool ensureConnected() {
        if (pipe_ != INVALID_HANDLE_VALUE) return true;
        DWORD now = GetTickCount();
        if (now - lastFail_ < 2000) return false;  // 失败冷却 2 秒，避免频繁重试阻塞
        if (pipeName_.empty()) {
            lastFail_ = now;
            return false;
        }
        HANDLE h = INVALID_HANDLE_VALUE;
        // daemon 可能尚未创建管道实例：最多等 3×20ms，绝不在 UI 线程长等。
        for (int i = 0; i < 3; ++i) {
            h = ::CreateFileW(pipeName_.c_str(), GENERIC_READ | GENERIC_WRITE,
                              0, nullptr, OPEN_EXISTING, FILE_FLAG_OVERLAPPED, nullptr);
            if (h != INVALID_HANDLE_VALUE) break;
            if (GetLastError() != ERROR_PIPE_BUSY) break;
            if (!::WaitNamedPipeW(pipeName_.c_str(), 20)) break;
        }
        if (h == INVALID_HANDLE_VALUE) {
            lastFail_ = now;
            // 每进程只记一次：区分「daemon 未起」与「管道 ACL 拒绝」。
            static volatile LONG pipeFailLogged = 0;
            if (InterlockedCompareExchange(&pipeFailLogged, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log",
                           "pipe connect failed err=%lu appcontainer=%d\n",
                           GetLastError(), isAppContainer() ? 1 : 0);
            }
            return false;
        }
        pipe_ = h;
        return true;
    }

    bool writeAll(const std::uint8_t* data, std::size_t len) override {
        if (pipe_ == INVALID_HANDLE_VALUE && !ensureConnected()) return false;
        return pipeIo(true, data, len);
    }

    bool readAll(std::uint8_t* buf, std::size_t len) override {
        if (pipe_ == INVALID_HANDLE_VALUE) return false;
        return pipeIo(false, buf, len);
    }

    void closeConn() override {
        if (pipe_ != INVALID_HANDLE_VALUE) {
            ::CloseHandle(pipe_);
            pipe_ = INVALID_HANDLE_VALUE;
        }
        lastFail_ = 0;
    }

private:
    // 当前用户命名管道名：\\.\pipe\chaoset-<SID>，与 Go daemon 的
    // ipc.DefaultPipeName() 同规则（SID 而非用户名，多用户/非 ASCII 安全）。
    static std::wstring currentUserPipeName() {
        HANDLE token = nullptr;
        if (!::OpenProcessToken(::GetCurrentProcess(), TOKEN_QUERY, &token)) return L"";
        DWORD len = 0;
        ::GetTokenInformation(token, TokenUser, nullptr, 0, &len);
        if (len == 0) {
            ::CloseHandle(token);
            return L"";
        }
        std::vector<BYTE> buf(len);
        if (!::GetTokenInformation(token, TokenUser, buf.data(), len, &len)) {
            ::CloseHandle(token);
            return L"";
        }
        auto* user = reinterpret_cast<TOKEN_USER*>(buf.data());
        LPWSTR sid = nullptr;
        if (!::ConvertSidToStringSidW(user->User.Sid, &sid)) {
            ::CloseHandle(token);
            return L"";
        }
        std::wstring name = L"\\\\.\\pipe\\chaoset-";
        name += sid;
        ::LocalFree(sid);
        ::CloseHandle(token);
        return name;
    }

    // 同步读写 + 200ms 超时：单次等待上限 200ms，
    // daemon 半死时不会永久阻塞宿主 UI 线程。
    bool pipeIo(bool isWrite, const std::uint8_t* data, std::size_t len) {
        std::size_t off = 0;
        while (off < len) {
            DWORD chunk = static_cast<DWORD>(
                std::min<std::size_t>(len - off, 1u << 20));
            OVERLAPPED ov{};
            ov.hEvent = ::CreateEventW(nullptr, TRUE, FALSE, nullptr);
            if (ov.hEvent == nullptr) {
                failConn();
                return false;
            }
            DWORD done = 0;
            BOOL ok = isWrite
                          ? ::WriteFile(pipe_, data + off, chunk, &done, &ov)
                          : ::ReadFile(pipe_, const_cast<std::uint8_t*>(data) + off,
                                       chunk, &done, &ov);
            if (!ok) {
                DWORD err = ::GetLastError();
                if (err == ERROR_IO_PENDING) {
                    DWORD wait = ::WaitForSingleObject(ov.hEvent, 200);
                    if (wait == WAIT_OBJECT_0) {
                        ok = ::GetOverlappedResult(pipe_, &ov, &done, FALSE);
                    } else {
                        ::CancelIoEx(pipe_, &ov);
                        ::WaitForSingleObject(ov.hEvent, INFINITE);
                        ok = FALSE;
                    }
                }
            }
            ::CloseHandle(ov.hEvent);
            if (!ok || done == 0) {
                failConn();
                return false;
            }
            off += done;
        }
        return true;
    }

    void failConn() {
        if (pipe_ != INVALID_HANDLE_VALUE) {
            ::CloseHandle(pipe_);
            pipe_ = INVALID_HANDLE_VALUE;
        }
        lastFail_ = GetTickCount();
    }

    HANDLE pipe_ = INVALID_HANDLE_VALUE;
    DWORD lastFail_ = 0;
    std::wstring pipeName_;
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

// ---- daemon 懒启动（当前用户上下文） ----
//
// 安装器以管理员权限运行，不能可靠地替交互用户启动 daemon：LOCALAPPDATA、
// 命名管道 ACL、用户词库都可能属于提升后的管理员账户；反之亦然。
// 因此安装器只写开机 Run 项，不启动 daemon；TSF 在首次激活/首键时以当前
// 用户身份启动（-pipe，用户数据写 %LOCALAPPDATA%\Chaoset）。daemon 的
// -seeddict 负责首次从安装目录复制种子词库，不覆盖已有用户词库。
// 启动失败只记诊断日志并按英文透传降级，绝不阻塞/关闭宿主程序。

static std::wstring daemonQuote(const std::wstring& s) { return L"\"" + s + L"\""; }

// 最近一次成功 CreateProcess 的时间（5s 内用于让首个按键短暂等待 daemon
// 完成词库加载，之后立即复用连接）。放在文件作用域便于 Activate 的预热与
// OnTestKeyDown 的实际等待共享同一状态。
static volatile DWORD g_daemonStartOkAt = 0;

// 返回 true 表示本次确实发起了 daemon 进程（CreateProcess 成功）；被 3s 节流
// 或找不到 exe 时返回 false，调用方据此避免每键都长时间等待。
static bool ensureDaemonStarted() {
    // AppContainer 宿主（Windows 11 开始菜单、搜索框等）里不发起进程。
    //
    // CreateProcessW 让子进程运行在**调用者的安全上下文**里（CreateProcessAsUser
    // 才能换 token）。从 AppContainer 进程拉起的 chaosetd 会继承 AppContainer
    // 身份：%LOCALAPPDATA% 被重定向到包私有目录（词库/学习数据/日志写进用户
    // 正常程序看不到的地方），更糟时读不到旧 userdict 会走 log.Fatal，在监听
    // 管道之前就退出，还会跟正常会话的 daemon 抢同一个按用户 SID 命名的管道。
    //
    // 这不影响 AppContainer 宿主使用输入法：daemon 由登录 Run 项常驻拉起、
    // 崩溃后由任意普通程序里的首次输入懒启动恢复；AppContainer 宿主只需连
    // 已在运行的管道（DACL 含 ALL APPLICATION PACKAGES，且带低完整性标签，
    // 见 ipc/pipe_sddl.go）。因此这里不做无用的 spawn，只如实记录日志。
    if (isAppContainer()) return false;

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
    // lpCurrentDirectory 传 nullptr：daemon 所有路径都是绝对路径，工作目录
    // 无关紧要；AppContainer 宿主对 LOCALAPPDATA 可能无写权限，若该目录不存在
    // 再用它当 cwd 会让 CreateProcess 直接失败。
    //
    // 优先 BREAKAWAY：TSF 宿主（浏览器等）可能位于带 kill-on-close 的 Job
    // 对象里，普通子进程会随宿主退出被杀死；Job 不允许 breakaway 时回退普通
    // 创建，行为与旧版一致。
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
        g_daemonStartOkAt = now;
    }
    if (diagBudget()) {
        diagPrintf(L"tsf.log", "daemon lazy start ok=%d err=%lu appcontainer=%d\n",
                   ok ? 1 : 0, ok ? 0UL : logErr, isAppContainer() ? 1 : 0);
    }
    return ok;
}

// 首次输入时启动并短暂等待管道就绪；之后连接常驻复用，不再产生等待。
// daemon 启动后首次加载 6.5 万词条约需 100ms；只在确实刚发起进程或 5s 内
// 刚成功发起过时等待，避免 daemon 缺失时每个按键都阻塞上千毫秒。
static bool ensureDaemonConnected(PipeTransport& transport) {
    if (transport.isConnected()) return true;
    const bool started = ensureDaemonStarted();
    const DWORD now = GetTickCount();
    const DWORD startedAt = g_daemonStartOkAt;
    const bool recentStart = startedAt != 0 && now - startedAt < 5000;
    // 每次发起启动后只完整等待一次；若 8 次都失败，2s 内后续按键不再逐键
    // 阻塞（否则 daemon 起不来时打字会持续卡顿），2s 后仍允许再等一轮。
    static DWORD lastWaitAt = 0;
    const bool canWait = started || (recentStart && (lastWaitAt == 0 || now - lastWaitAt > 2000));
    if (canWait) lastWaitAt = now;
    const int attempts = canWait ? 8 : 1;
    for (int i = 0; i < attempts; ++i) {
        transport.closeConn();  // 清掉失败冷却，允许本轮立即重试
        if (transport.ensureConnected()) return true;
        if (i + 1 < attempts) Sleep(25);
    }
    return false;
}

// daemon 请求失败（连接还在但 session 方法返回 !ok）：每条 2s 最多记一次，
// 不随每个按键写日志。配合管道连接失败日志，可区分「根本没连上」与
// 「连上了但请求失败（超时/daemon 业务错误）」。
static void diagDaemonRequestFailure(PipeTransport& transport, const char* op) {
    static DWORD last = 0;
    const DWORD now = GetTickCount();
    if (last != 0 && now - last < 2000) return;
    last = now;
    if (diagBudget()) {
        diagPrintf(L"tsf.log", "daemon request failed op=%s connected=%d\n",
                   op, transport.isConnected() ? 1 : 0);
    }
}

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
// （chaoset-ui.exe，登录 Run 项常驻 + TIP 按需拉起）。TIP 只通过命名管道上报
// 模式、请求打开设置，不持有任何 UI。
//
// 管道安全描述符与 daemon 完全同规则：DACL 当前用户 + SYSTEM +
// ALL APPLICATION PACKAGES(S-1-15-2-1)，SACL 低完整性标签 ML;;NW;;;LW。
// 这是 AppContainer 宿主（开始菜单/搜索）能「连上并写入」的前提；设置 SACL
// 失败时降级为仅 DACL（与 ipc/pipe_sddl.go 的降级策略一致）。
static std::wstring currentUserSid() {
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
static DWORD currentSessionId() {
    DWORD id = 0;
    ProcessIdToSessionId(GetCurrentProcessId(), &id);
    return id;
}

// UI 宿主命令管道名。SID 拿不到时返回空串：调用方按「UI 不可用」降级。
static const std::wstring& uiPipeName() {
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
static std::wstring uiMutexName() {
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
static bool uiSendCommandOnce(const char* command) {
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
static bool uiSendCommand(const char* command, int attempts = 4) {
    for (int i = 0; i < attempts; ++i) {
        if (uiSendCommandOnce(command)) return true;
        Sleep(10);
    }
    return false;
}

// 宿主 UI 线程专用版本：**完全不重试、不 Sleep**（单次非阻塞尝试）。
// Activate/OnSetFocus 在系统设置、开始菜单这类宿主里同步执行，任何 Sleep
// 都会直接变成用户可感知的卡顿（2026-11-06 用户反馈）。
static bool uiSendCommandFast(const char* command) {
    return uiSendCommandOnce(command);
}

// 向 UI 宿主查权威中英状态（system 作用域）。**非阻塞、可失败**：
// 拿不到就返回 false，调用方继续用本地缓存——绝不能为了同步状态卡住宿主。
// 请求/应答同一条短连接（管道是双向的，见 uiPipeThreadProc）。
static bool uiQueryMode(bool* english) {
    if (english == nullptr) return false;
    const std::wstring& name = uiPipeName();
    if (name.empty()) return false;
    HANDLE pipe = CreateFileW(name.c_str(), GENERIC_READ | GENERIC_WRITE, 0, nullptr,
                              OPEN_EXISTING, 0, nullptr);
    if (pipe == INVALID_HANDLE_VALUE) return false;
    const char req[] = "query\n";
    DWORD written = 0;
    bool ok = WriteFile(pipe, req, sizeof(req) - 1, &written, nullptr) != FALSE;
    if (ok) {
        char buf[32] = {0};
        DWORD read = 0;
        // 后台读：服务端同一时刻只有一个实例在监听，正常情况下应答 <1ms 就到；
        // 200ms 上限避免半死进程把我们挂住。
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
            CloseHandle(ov.hEvent);
        }
        std::string reply(buf, read);
        if (reply.rfind("mode ", 0) == 0) {
            *english = reply.size() >= 6 && reply[5] == '1';
            ok = true;
        } else {
            ok = false;
        }
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

// 按需拉起 UI 宿主（5s 节流）：托盘指示器 / 设置窗都靠它。只在管道连不上时调用。
static void ensureUiHostStarted() {
    static DWORD lastAttempt = 0;
    const DWORD now = GetTickCount();
    if (lastAttempt != 0 && now - lastAttempt < 5000) return;
    lastAttempt = now;
    if (!uiSpawnHost(nullptr)) {
        static volatile LONG logged = 0;
        if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
            diagPrintf(L"tsf.log", "ui host spawn failed appcontainer=%d elevated=%d\n",
                       isAppContainer() ? 1 : 0, isProcessElevated() ? 1 : 0);
        }
    }
}

// 打开设置窗：优先请求已在运行的 UI 宿主；没有就让 UI 宿主带 --settings 启动
// （首实例会直接弹出设置窗，第二实例把请求转给首实例后退出）。AppContainer
// 宿主不能发起进程，只能走已运行实例（登录 Run 项负责常驻），失败时由调用方
// 通过 daemon 代拉起（见 ChaosetTIP::showSettingsWindow）。
static bool openSettingsViaUi() {
    // TIP 所在宿主进程通常就是前台进程：显式授权 UI 宿主把设置窗切到前台，
    // 否则从开始菜单点齿轮时设置窗可能被 Start 的 z 序 band 挡在后面。
    // 返回 FALSE 不一定致命（UI 宿主自己的 SetForegroundWindow 仍可能被系统
    // 允许），记录下来供 AC 前台问题排障。
    const BOOL allowFg = AllowSetForegroundWindow(ASFW_ANY);
    static volatile LONG loggedAllowFg = 0;
    if (InterlockedCompareExchange(&loggedAllowFg, 1, 0) == 0) {
        diagPrintf(L"tsf.log", "allow set foreground ok=%d appcontainer=%d\n",
                   allowFg ? 1 : 0, isAppContainer() ? 1 : 0);
    }
    if (uiSendCommand("settings", 20)) return true;  // ≈200ms，覆盖 UI 刚启动的竞态
    if (isAppContainer()) return false;              // AC：调用方走 daemon 代拉起
    return uiSpawnHost(L"--settings");
}

// 把当前中英模式上报给 UI 宿主（0=中文 1=英文）。每次事件一次短连接，
// 正常路径 <1ms。
//
// allowSpawn：只有「本进程就是前台、确实在接收用户输入」时才允许懒启动 UI
// 宿主。后台 TIP 实例（explorer 单进程 4 个、被 TSF 提前激活的进程）不能
// 因为一次无关的 Activate 就拉起进程或改写托盘模式（2026-11-05 评审 P0）。
// 上报失败不重发本次模式：下一轮焦点/切换事件会再上报，UI 宿主启动后还有
// HKCU 记忆的上次模式兜底。
// 只有**持有焦点的进程**能更新托盘（2026-11-06 用户规则：
// 「谁有焦点谁就能更新托盘」）。
//
// 为什么必须在这里收口：TSF 会把 TIP 装进每个宿主进程（explorer 单进程实测
// 4 个实例），后台实例的 Activate/焦点回调同样会走到上报路径。若不加闸门，
// 后台实例用自己陈旧的 enMode_ 覆盖托盘 → 用户看到「托盘状态和当前窗口对不上」。
//
// 判定放在函数入口而不是各调用点：调用点都散落在 Activate/OnSetFocus/
// storeEnglish 里，逐个判断容易漏（2026-11-06 审计：4 个调用点原先只有 1 个
// 判断了前台）。**所有**托盘更新都必须经过这里。
static bool reportModeToUi(bool english, bool allowSpawn) {
    // 本进程不是前台 → 一律不许改托盘。
    {
        const HWND fg = GetForegroundWindow();
        DWORD fgPid = 0;
        if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
        if (fgPid != GetCurrentProcessId()) return false;
    }
    const char* command = english ? "mode 1" : "mode 0";
    // 单次非阻塞尝试：本函数跑在宿主 UI 线程（Activate/OnSetFocus）上。
    if (uiSendCommandFast(command)) return true;
    if (allowSpawn) ensureUiHostStarted();
    return false;
}

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

    // 位置语义与 mac ModeToastController.show 一致：调用方传光标行下方
    // 的锚点，本函数测量出胶囊尺寸后按工作区钳制（mac 用 candidateOrigin
    // 的 clampTopLeft；Windows 此前完全不钳制，贴近屏幕边缘时提示会被截掉）。
    void show(HINSTANCE hinst, const std::wstring& text, int x, int y) {
        if (!ensureWindow(hinst)) return;
        // owner 跟随当前前台窗口刷新：窗口在 TIP 生命周期内复用，只在首次
        // 创建时取 owner 会让后续（尤其 shell 宿主）提示条永远无 owner，
        // 被沉浸式 z 序 band 压住（2026-11-05 回归审计 P2-10）。
        const HWND wantOwner = popupOwner();
        if (wantOwner != nullptr && GetWindow(hwnd_, GW_OWNER) != wantOwner) {
            SetWindowLongPtrW(hwnd_, GWLP_HWNDPARENT,
                              reinterpret_cast<LONG_PTR>(wantOwner));
        }
        text_ = text;
        // 提示条跟随所在显示器的 DPI（混合 DPI 多屏）
        dpi_ = dpiForPoint(POINT{x, y});
        measure();
        HMONITOR hMon = MonitorFromPoint(POINT{x, y}, MONITOR_DEFAULTTONEAREST);
        MONITORINFO mi{};
        mi.cbSize = sizeof(mi);
        if (GetMonitorInfoW(hMon, &mi)) {
            const int edge = pt(4);
            if (x + w_ > mi.rcWork.right) x = mi.rcWork.right - w_ - edge;
            if (x < mi.rcWork.left) x = mi.rcWork.left + edge;
            if (y + h_ > mi.rcWork.bottom) y = mi.rcWork.bottom - h_ - edge;
            if (y < mi.rcWork.top) y = mi.rcWork.top + edge;
        }
        SetWindowPos(hwnd_, HWND_TOPMOST, x, y, w_, h_, SWP_NOACTIVATE | SWP_SHOWWINDOW);
        SetWindowRgn(hwnd_, CreateRoundRectRgn(0, 0, w_ + 1, h_ + 1, pt(10) * 2, pt(10) * 2), TRUE);
        if (diagBudget()) {
            // 同候选窗：owner / 前台窗口 pid 用于定位「窗口被抬到前面」。
            const HWND own = GetWindow(hwnd_, GW_OWNER);
            DWORD ownPid = 0;
            if (own != nullptr) GetWindowThreadProcessId(own, &ownPid);
            const HWND fg = GetForegroundWindow();
            DWORD fgPid = 0;
            if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
            diagPrintf(L"tsf.log", "toast shown ownpid=%lu fgpid=%lu selfpid=%lu\n",
                       static_cast<unsigned long>(ownPid), static_cast<unsigned long>(fgPid),
                       static_cast<unsigned long>(GetCurrentProcessId()));
        }
        InvalidateRect(hwnd_, nullptr, TRUE);
        SetTimer(hwnd_, kTimerId, kDurationMs, nullptr);
    }

    void hide() {
        if (hwnd_ != nullptr && IsWindow(hwnd_)) {
            KillTimer(hwnd_, kTimerId);
            ShowWindow(hwnd_, SW_HIDE);
        }
    }

private:
    static constexpr UINT kTimerId = 1;
    static constexpr UINT kDurationMs = 1200;

    // 提示条与候选窗同用 pt 口径（mac ModeToastView.size：文本 +28/+14，
    // 圆角 10pt；此前 px@96 口径只有 0.75 倍，见 2026-10-30 审计 A1/R28）。
    int pt(int v) const { return MulDiv(v, dpi_, 72); }

    bool ensureWindow(HINSTANCE hinst) {
        if (hwnd_ != nullptr) {
            if (IsWindow(hwnd_)) return true;
            // owner 宿主窗口销毁会连带销毁提示窗：先归还计数再重建（A1/A4）。
            onWindowDestroyed();
        }
        const std::wstring cls = windowClassName(L"ChaosetModeToastWndClass", hinst);
        WNDCLASSEXW wc{};
        wc.cbSize = sizeof(wc);
        wc.style = CS_DROPSHADOW;
        wc.lpfnWndProc = ModeToast::wndProc;
        wc.hInstance = hinst;
        wc.lpszClassName = cls.c_str();
        RegisterClassExW(&wc);
        // ModeToast 是**纯提示**：不接收鼠标（带 WS_EX_TRANSPARENT 穿透到
        // 宿主应用，对应 macOS 端 ModeToastController 的 ignoresMouseEvents=true；
        // 2026-10-30 审计 A12 修正：此前注释写「纯提示 ignoresMouseEvents」但
        // 窗口实际没有穿透，提示显示 1.2s 内其区域会吃掉点击）。层级同候选窗
        // TOPMOST 保持（系统模态菜单弹出时位于最上，不会挡菜单）。
        // owner 设为前台窗口：提示条与候选窗一样，无主 TOPMOST 窗口会被
        // Windows 11 开始菜单/搜索的沉浸式 z 序 band 压住，模式切换就看不到
        // 反馈（2026-10-31 审计 A4）。前台窗口拿不到时退回 nullptr（旧行为）。
        HWND owner = popupOwner();
        hwnd_ = CreateWindowExW(
            WS_EX_TOOLWINDOW | WS_EX_TOPMOST | WS_EX_NOACTIVATE | WS_EX_TRANSPARENT,
            cls.c_str(), L"ChaosetModeToast",
            WS_POPUP, 0, 0, 0, 0,
            owner, nullptr, hinst, nullptr);
        if (hwnd_ != nullptr) {
            SetWindowLongPtrW(hwnd_, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(this));
            // 窗口过程指向本 DLL 代码：活着就禁止宿主 FreeLibrary（同候选窗）
            InterlockedIncrement(&g_liveToastWindows);
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
        // mac ModeToastView.size：宽 = ceil(文本宽)+28，高 = ceil(文本高)+14
        w_ = sz.cx + pt(28);
        h_ = sz.cy + pt(14);
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
        // 描边 1pt（mac Toast 同为 1pt；用 show() 缓存的 dpi_，见 strokePx）
        HPEN pen = CreatePen(PS_SOLID, strokePx(dpi_), dark ? RGB(0x45, 0x45, 0x48) : RGB(0xC8, 0xC8, 0xCC));
        HGDIOBJ oldBrush = SelectObject(dc, bg);
        HGDIOBJ oldPen = SelectObject(dc, pen);
        // mac 圆角 r10pt（NSBezierPath xRadius/yRadius=10）→ GDI 直径 20pt
        RoundRect(dc, 0, 0, w_, h_, pt(10) * 2, pt(10) * 2);
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
            case WM_SETTINGCHANGE:
            case WM_THEMECHANGED:
                // 同候选窗：主题切换立即重绘（提示条仅 1.2s，仍可能在显示期间
                // 遇到切换，保持配色一致）。
                if (self != nullptr) InvalidateRect(hwnd, nullptr, TRUE);
                break;
            case WM_DPICHANGED:
                if (self != nullptr) {
                    self->dpi_ = static_cast<int>(HIWORD(wParam));
                    self->measure();
                    RECT wr{};
                    GetWindowRect(hwnd, &wr);
                    SetWindowPos(hwnd, HWND_TOPMOST, wr.left, wr.top, self->w_, self->h_,
                                 SWP_NOACTIVATE | SWP_SHOWWINDOW);
                    InvalidateRect(hwnd, nullptr, TRUE);
                }
                break;
            case WM_NCDESTROY:
                // 主动销毁与 owner 连带销毁的统一回收点（同候选窗 A1/A4）。
                if (self != nullptr) self->onWindowDestroyed();
                break;
        }
        return DefWindowProcW(hwnd, msg, wParam, lParam);
    }

public:
    // 彻底销毁（非激活退场用）：只 hide() 的话窗口对象还活着、定时器也可能
    // 残留；用户要求「感觉不到输入法存在」，所以退场走销毁。
    void destroy() {
        hide();
        if (hwnd_ != nullptr) {
            if (!IsWindow(hwnd_) || !DestroyWindow(hwnd_)) onWindowDestroyed();
        }
    }

private:
    // 窗口销毁回收：清句柄并归还 DLL 防卸载计数（owner 连带销毁也会走到）。
    void onWindowDestroyed() {
        if (hwnd_ != nullptr) {
            hwnd_ = nullptr;
            InterlockedDecrement(&g_liveToastWindows);
        }
    }

    HWND hwnd_ = nullptr;
    std::wstring text_;
    int dpi_ = 96;
    int w_ = 0;
    int h_ = 0;
};

// mingw-w64 的 msctf.h 只声明了 ITfTextInputProcessor，没有微软 SDK 里的
// ITfTextInputProcessorEx（UILess 模式所需）。这里按 Windows SDK 10 的
// msctf.idl 原样补声明，IID 与虚表顺序都取自 SDK：
//   [
//     object, local,
//     uuid(6e4e2102-f9cd-433d-b496-303ce03a6507),
//   ]
//   interface ITfTextInputProcessorEx : ITfTextInputProcessor
//   {
//       HRESULT ActivateEx([in] ITfThreadMgr *ptim, [in] TfClientId tid, [in] DWORD dwFlags);
//   };
//
// 为什么需要（2026-11-05，微软《UILess Mode Overview》原文）：
//   "To be UI less aware, TIP needs to implement ITfTextInputProcessorEx.
//    If this interface is not implemented, TIP will not be activated under
//    the UI less mode thread."
// 不实现它就等于对 UILess 宿主（游戏/全屏/部分 WinUI 应用）完全不可见，
// 而且连日志都不会产生——我们此前看到的 "uiless=0" 正是「被排除」而非
// 「不存在 UILess 宿主」。
static const GUID kIID_ITfTextInputProcessorEx = {
    0x6e4e2102, 0xf9cd, 0x433d, {0xb4, 0x96, 0x30, 0x3c, 0xe0, 0x3a, 0x65, 0x07}};

struct ITfTextInputProcessorEx : public ITfTextInputProcessor {
    virtual HRESULT STDMETHODCALLTYPE ActivateEx(ITfThreadMgr* ptim, TfClientId tid,
                                                 DWORD dwFlags) = 0;
};


// 托盘左键单击 → 打开设置窗（实现在 UI 宿主段；这里只需前向声明）。
static void onUiTrayIconClicked();
// 托盘右键菜单（实现在 UI 宿主段；需要 g_uiHostWnd/openSettings 等）。
static void showTrayMenu(HWND owner);

// 中英状态作用域（2026-11-06 用户要求可配置）：
//   system  —— 全系统共享（默认）：权威在 UI 宿主 + HKCU，所有 TIP 跟随。
//   process —— 每个宿主进程各自独立：TIP 进程内静态量保存。
//   window  —— 每个窗口/上下文独立：TIP 按宿主 HWND 保存。
// 定义在 UI 宿主段，这里前置声明供 TIP 侧使用。
enum class ModeScope { System, Process, Window };
static ModeScope uiLoadModeScope();
static void uiSaveModeScope(ModeScope scope);
static bool uiQueryMode(bool* english);

// UI 宿主的隐藏消息窗口（定义见文件后部；托盘点击要往里投消息）。
extern HWND g_uiHostWnd;
static constexpr UINT kUiToggleModeMsg = WM_APP + 73;

// ---- 任务栏「中/英」模式指示器（只有 chaoset-ui.exe 使用）----
//
// 为什么是独立进程的自绘托盘图标：中英模式虽已写进 TSF 线程隔间
// GUID_COMPARTMENT_KEYBOARD_OPENCLOSE（回读成功），但 Windows 不会为第三方
// TIP 绘制任务栏「中/英」（任务栏输入指示器只显示 profile 图标，实测是蓝色 C）。
// 搜狗等输入法同样是自建托盘图标。
//
// **绝不能在 TIP 进程里创建**：TSF 把 TIP 加载进每个宿主进程，explorer.exe 单进程
// 实测就有 4 个 TIP 实例；每个实例各画一个图标就是用户看到的任务栏
// 「中 英 中 英」。因此 UiTray 只由单例 UI 进程（chaoset-ui.exe）持有，
// TIP 经命名管道上报模式。UI 进程是普通用户会话进程（中等完整性），
// 托盘图标在 AppContainer 宿主里也能正常显示。
class UiTray {
public:
    UiTray() = default;
    UiTray(const UiTray&) = delete;
    UiTray& operator=(const UiTray&) = delete;
    ~UiTray() { destroy(); }

    void show(bool english) {
        if (!ensureWindow()) {
            logFailure("window", GetLastError());
            return;
        }
        if (icon_ == nullptr || english_ != english) {
            if (icon_ != nullptr) DestroyIcon(icon_);
            icon_ = makeGlyphIcon(english);
            english_ = english;
        }
        if (icon_ == nullptr) {
            logFailure("icon", GetLastError());
            return;
        }
        NOTIFYICONDATAW nid{};
        nid.cbSize = sizeof(nid);
        nid.hWnd = hwnd_;
        nid.uID = kIconId;
        nid.uFlags = NIF_ICON | NIF_TIP | NIF_MESSAGE;
        nid.uCallbackMessage = kCallbackMsg;
        nid.hIcon = icon_;
        wcscpy_s(nid.szTip, english ? L"Chaoset 输入法：英文" : L"Chaoset 输入法：中文");
        if (!Shell_NotifyIconW(shown_ ? NIM_MODIFY : NIM_ADD, &nid)) {
            shown_ = false;
            logFailure("Shell_NotifyIcon", GetLastError());
            // explorer/taskbar 未就绪时（登录早期实测 1460/1008）1.5s 后重试，
            // 否则本次会话可能整场没有托盘图标；成功后由 show() 取消定时器。
            if (hwnd_ != nullptr) SetTimer(hwnd_, kRetryTimerId, 1500, nullptr);
            return;
        }
        shown_ = true;
        english_ = english;
        KillTimer(hwnd_, kRetryTimerId);
    }

    // 托盘图标当前是否真的在显示。调用方据此决定要不要再用浮动提示兜底
    //（两个地方同时提示中英文模式既重复又干扰，2026-11-05 用户反馈）。
    bool isShown() const { return shown_; }

    void hide() {
        if (hwnd_ != nullptr) KillTimer(hwnd_, kRetryTimerId);
        if (!shown_) return;
        NOTIFYICONDATAW nid{};
        nid.cbSize = sizeof(nid);
        nid.hWnd = hwnd_;
        nid.uID = kIconId;
        Shell_NotifyIconW(NIM_DELETE, &nid);
        shown_ = false;
    }

private:
    static constexpr UINT kIconId = 1;
    static constexpr UINT kCallbackMsg = WM_APP + 19;
    static constexpr UINT kRetryTimerId = 2;
    HWND hwnd_ = nullptr;
    HICON icon_ = nullptr;
    bool shown_ = false;
    bool english_ = false;
    // 是否已把本窗口计入 g_liveTrayWindows（WM_NCDESTROY 里按此判断是否归还，
    // 避免窗口创建失败路径把计数减成负数→ DllCanUnloadNow 恒 S_FALSE）。
    bool counted_ = false;
    volatile LONG loggedFail_ = 0;

    // 托盘失败必须**绕开 diagBudget**：预算只有 200 次，会被每键日志烧光，
    // 正好把失败原因吞掉（2026-11-05 实机：0 条失败日志但图标没出现）。
    static void logFailure(const char* what, unsigned long err) {
        static volatile LONG logged = 0;
        if (InterlockedCompareExchange(&logged, 1, 0) != 0) return;
        diagPrintf(L"tsf.log", "tray icon FAILED: %s err=%lu\n", what, err);
    }

    static UINT taskbarCreatedMsg() {
        static const UINT msg = RegisterWindowMessageW(L"TaskbarCreated");
        return msg;
    }

    static LRESULT CALLBACK wndProc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
        UiTray* self = reinterpret_cast<UiTray*>(GetWindowLongPtrW(hwnd, GWLP_USERDATA));
        if (msg == taskbarCreatedMsg()) {
            // explorer 重启后托盘被清空，无条件重新添加：旧实现只在
            // icon_ != nullptr 时才重加，若首次 NIM_ADD 就失败（登录早期）
            // 会整场没有图标（2026-11-05 评审 P1）。
            if (self != nullptr) {
                self->shown_ = false;
                self->show(self->english_);
            }
            return 0;
        }
        if (msg == WM_TIMER) {
            if (self != nullptr && wp == kRetryTimerId) {
                self->show(self->english_);
                return 0;
            }
        }
        if (msg == kCallbackMsg) {
            // 托盘点击语义（2026-11-06 用户要求，与微软拼音/搜狗一致）：
            //   左键单击 = 切换中英文；
            //   右键单击 = 弹菜单（打开设置… / 关于）。
            // 旧实现左键 = 打开设置窗、右键完全没处理，用户点了没反应。
            const UINT ev = LOWORD(lp);
            if (ev == WM_LBUTTONUP) {
                if (g_uiHostWnd != nullptr) {
                    PostMessageW(g_uiHostWnd, kUiToggleModeMsg, 0, 0);
                }
                return 0;
            }
            if (ev == WM_RBUTTONUP) {
                showTrayMenu(hwnd);
                return 0;
            }
            return 0;
        }
        if (msg == WM_NCDESTROY) {
            SetWindowLongPtrW(hwnd, GWLP_USERDATA, 0);
            if (self != nullptr) {
                self->hwnd_ = nullptr;
                if (self->counted_) {
                    self->counted_ = false;
                    InterlockedDecrement(&g_liveTrayWindows);
                }
            }
            return 0;
        }
        return DefWindowProcW(hwnd, msg, wp, lp);
    }

    bool ensureWindow() {
        if (hwnd_ != nullptr && IsWindow(hwnd_)) return true;
        hwnd_ = nullptr;
        const std::wstring cls = windowClassName(L"ChaosetTrayWndClass", g_hInst);
        WNDCLASSEXW wc{};
        wc.cbSize = sizeof(wc);
        wc.lpfnWndProc = UiTray::wndProc;
        wc.hInstance = g_hInst;
        wc.lpszClassName = cls.c_str();
        RegisterClassExW(&wc);
        hwnd_ = CreateWindowExW(WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE, cls.c_str(),
                                L"ChaosetTray", WS_POPUP, 0, 0, 0, 0, nullptr, nullptr,
                                g_hInst, nullptr);
        if (hwnd_ == nullptr) return false;
        SetWindowLongPtrW(hwnd_, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(this));
        counted_ = true;
        InterlockedIncrement(&g_liveTrayWindows);
        return true;
    }

    // 画一个 16×16 的小图标：深色圆角底 + 白色「中」/「英」。
    static HICON makeGlyphIcon(bool english) {
        const int sz = GetSystemMetrics(SM_CXSMICON) > 0 ? GetSystemMetrics(SM_CXSMICON) : 16;
        BITMAPV5HEADER bi{};
        bi.bV5Size = sizeof(bi);
        bi.bV5Width = sz;
        bi.bV5Height = -sz;  // 自上而下
        bi.bV5Planes = 1;
        bi.bV5BitCount = 32;
        bi.bV5Compression = BI_BITFIELDS;
        bi.bV5RedMask = 0x00FF0000;
        bi.bV5GreenMask = 0x0000FF00;
        bi.bV5BlueMask = 0x000000FF;
        bi.bV5AlphaMask = 0xFF000000;
        HDC screen = GetDC(nullptr);
        if (screen == nullptr) return nullptr;
        HDC dc = CreateCompatibleDC(screen);
        ReleaseDC(nullptr, screen);
        if (dc == nullptr) return nullptr;
        void* bits = nullptr;
        HBITMAP color = CreateDIBSection(dc, reinterpret_cast<BITMAPINFO*>(&bi),
                                         DIB_RGB_COLORS, &bits, nullptr, 0);
        if (color == nullptr || bits == nullptr) {
            if (color != nullptr) DeleteObject(color);
            DeleteDC(dc);
            return nullptr;
        }
        HGDIOBJ old = SelectObject(dc, color);
        ZeroMemory(bits, static_cast<size_t>(sz) * sz * 4);
        HBRUSH bg = CreateSolidBrush(RGB(24, 24, 24));
        HBRUSH oldBrush = static_cast<HBRUSH>(SelectObject(dc, bg));
        HPEN pen = CreatePen(PS_SOLID, 1, RGB(24, 24, 24));
        HPEN oldPen = static_cast<HPEN>(SelectObject(dc, pen));
        RoundRect(dc, 0, 0, sz, sz, sz / 3, sz / 3);
        SelectObject(dc, oldBrush);
        SelectObject(dc, oldPen);
        DeleteObject(bg);
        DeleteObject(pen);
        SetBkMode(dc, TRANSPARENT);
        SetTextColor(dc, RGB(255, 255, 255));
        LOGFONTW lf{};
        lf.lfHeight = -MulDiv(sz, 72, 96) * 9 / 10;
        lf.lfWeight = FW_SEMIBOLD;
        lf.lfCharSet = DEFAULT_CHARSET;
        wcscpy_s(lf.lfFaceName, L"Microsoft YaHei");
        HFONT font = CreateFontIndirectW(&lf);
        HGDIOBJ oldFont = SelectObject(dc, font);
        RECT rc{0, 0, sz, sz};
        DrawTextW(dc, english ? L"英" : L"中", -1, &rc, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, oldFont);
        DeleteObject(font);
        SelectObject(dc, old);
        // 把「近黑 = 背景透明」转成 alpha：GDI 绘制不写 alpha 通道。
        auto* px = static_cast<unsigned char*>(bits);
        for (int i = 0; i < sz * sz; ++i) {
            unsigned char* p = px + i * 4;
            const bool transparent = (p[0] == 0 && p[1] == 0 && p[2] == 0);
            p[3] = transparent ? 0 : 255;
        }
        DeleteDC(dc);
        // ICONINFO 的 hbmMask **不能是 NULL**：CreateIconIndirect 在部分系统上会
        // 直接失败，导致图标永远建不出来、只能一直用浮动提示兜底
        //（2026-11-05 实机：0 条失败日志、任务栏始终没有图标）。
        // 32bpp 图标实际由 alpha 通道决定透明，掩码全 0（不透明）即可。
        //
        // 掩码按 1bpp WORD 对齐动态分配：SM_CXSMICON 随系统 DPI 变大
        //（250% → 40、300% → 48），旧实现固定 256 字节，sz≥44 时直接
        // 返回 nullptr，高 DPI 机器托盘永远没有图标（2026-11-05 回归审计 P2-2）。
        const int maskStride = ((sz + 15) / 16) * 2;
        std::vector<unsigned char> maskBits(static_cast<size_t>(maskStride) * sz, 0);
        HBITMAP mask = CreateBitmap(sz, sz, 1, 1, maskBits.data());
        ICONINFO ii{};
        ii.fIcon = TRUE;
        ii.hbmColor = color;
        ii.hbmMask = mask;
        HICON icon = CreateIconIndirect(&ii);
        if (mask != nullptr) DeleteObject(mask);
        DeleteObject(color);
        return icon;
    }

    void destroy() {
        hide();
        if (icon_ != nullptr) {
            DestroyIcon(icon_);
            icon_ = nullptr;
        }
        if (hwnd_ != nullptr && IsWindow(hwnd_)) DestroyWindow(hwnd_);
        hwnd_ = nullptr;
    }
};

class ChaosetTIP : public ITfTextInputProcessorEx,
                  public ITfKeyEventSink,
                  public ITfCompositionSink {
public:
    ChaosetTIP() { InterlockedIncrement(&g_liveObjects); }
    virtual ~ChaosetTIP() {
        // 防御性收尾：正常路径由 Deactivate 清理，但宿主也可能在未调用
        // Deactivate 的情况下直接释放 TIP（TSF 不保证成对）。窗口过程与
        // SetWinEventHook 钩子都指向本 DLL 内的代码，不撤会让下次
        // WM_PAINT / 前台事件跳到已卸载内存（宿主进程崩溃）。
        if (fgHook_ != nullptr) {
            UnhookWinEvent(fgHook_);
            removeHookEntry(fgHook_);
            fgHook_ = nullptr;
        }
        candidateWnd_.destroy();
        if (tm_ != nullptr) { tm_->Release(); tm_ = nullptr; }
        InterlockedDecrement(&g_liveObjects);
    }

    // IUnknown
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown) {
            *ppv = static_cast<IUnknown*>(static_cast<ITfTextInputProcessor*>(this));
        } else if (riid == IID_ITfTextInputProcessor) {
            *ppv = static_cast<ITfTextInputProcessor*>(this);
        } else if (riid == kIID_ITfTextInputProcessorEx) {
            *ppv = static_cast<ITfTextInputProcessorEx*>(this);
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
        // AppContainer/沉浸式宿主（开始菜单、搜索）不投递 OnSetFocus(FALSE)，
        // 但会重复调用 Activate——这是把上一轮残留清干净的最早可靠时机。
        applyPendingStaleReset();
        g_lastOp = kOpActivate;
        ensureCrashHandler();
        // 每个宿主进程只记一次 Activate：字段统一不变，同时避免反复聚焦耗尽
        // diagBudget（预算要留给真正的 daemon 失败/渲染失败日志）。
        static volatile LONG activateLogged = 0;
        if (InterlockedCompareExchange(&activateLogged, 1, 0) == 0 && diagBudget()) {
            char host[260];
            diagHostName(host, static_cast<int>(sizeof(host)));
            // fgpid：前台窗口所属进程。与 pid 不同即说明本 TIP 不在前台进程
            // 里（PWA 独立窗口/多进程宿主的排查关键，2026-11-05）。
            const HWND fg = GetForegroundWindow();
            DWORD fgPid = 0;
            if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
            diagPrintf(L"tsf.log", "activate pid=%lu fgpid=%lu host=%s appcontainer=%d build=%s %s\n",
                       static_cast<unsigned long>(GetCurrentProcessId()),
                       static_cast<unsigned long>(fgPid), host,
                       isAppContainer() ? 1 : 0, __DATE__, __TIME__);
        }
        // UILess 模式探测（2026-10-30 开始菜单候选窗排障）：
        // ITfThreadMgrEx::GetActiveFlags 返回 TF_TMF_UIELEMENTENABLEDONLY(0x4)
        // 表示**当前宿主**以 UI-less 模式运行 TIP——此时 TSF 期望 TIP 用
        // ITfUIElementMgr 提交候选、由宿主渲染；本壳不实现这些接口，候选窗
        // 是自绘 GDI 弹窗，在 UILess 宿主里必然看不到。这是判断「撤掉
        // UIELEMENTENABLED 类别是否奏效」的直接证据：修复生效后该位应为 0。
        static volatile LONG activeFlagsLogged = 0;
        if (ptim != nullptr && InterlockedCompareExchange(&activeFlagsLogged, 1, 0) == 0 &&
            diagBudget()) {
            ITfThreadMgrEx* tme = nullptr;
            DWORD flags = 0;
            HRESULT hrEx = ptim->QueryInterface(IID_ITfThreadMgrEx,
                                                reinterpret_cast<void**>(&tme));
            HRESULT hrFlags = (SUCCEEDED(hrEx) && tme != nullptr)
                                  ? tme->GetActiveFlags(&flags)
                                  : E_NOINTERFACE;
            if (tme != nullptr) tme->Release();
            diagPrintf(L"tsf.log", "activeflags hr_qi=0x%08lX hr=0x%08lX flags=0x%08lX uiless=%d\n",
                       static_cast<unsigned long>(hrEx),
                       static_cast<unsigned long>(hrFlags),
                       static_cast<unsigned long>(flags),
                       (SUCCEEDED(hrFlags) && (flags & 0x4)) ? 1 : 0);
        }
        // 部分宿主会重复调用 Activate（实机 tsf.log 同一 pid 多次 activate）。
        // 重复初始化必须幂等：旧的 thread manager 引用先释放，否则每次重复
        // Activate 泄漏一个 ITfThreadMgr；按键下沉也不能重复 Advise（否则
        // 每个按键会被投递两次，组词/上屏双份执行）。
        if (tm_ != nullptr && tm_ != ptim) {
            tm_->Release();
            tm_ = nullptr;
        }
        if (tm_ == nullptr) {
            tm_ = ptim;
            if (tm_ != nullptr) tm_->AddRef();
        }
        clientId_ = tid;
        // 激活档案查询器（判断「当前激活的输入法是不是我们」）。创建失败
        // 不能让「非激活整键放行」静默退化成旧行为：留一条绕开每键预算的日志，
        // 排障时能区分「闸门没生效」与「真的没有切走」（2026-11-05 审计 P2-5）。
        if (profileMgr_ == nullptr) {
            const HRESULT hrProfile = CoCreateInstance(
                kCLSID_TF_InputProcessorProfiles, nullptr, CLSCTX_INPROC_SERVER,
                IID_ITfInputProcessorProfileMgr, reinterpret_cast<void**>(&profileMgr_));
            static volatile LONG loggedProfileMgr = 0;
            if (InterlockedCompareExchange(&loggedProfileMgr, 1, 0) == 0) {
                diagPrintf(L"tsf.log", "activetip profile mgr hr=0x%08lX\n",
                           static_cast<unsigned long>(hrProfile));
            }
        }
        // 线程级隔间：把中英模式交给 Windows 输入指示器（任务栏「中/英」）。
        if (tm_ != nullptr && modeCompartment_ == nullptr) {
            ITfCompartmentMgr* compMgr = nullptr;
            if (SUCCEEDED(tm_->QueryInterface(IID_ITfCompartmentMgr,
                                              reinterpret_cast<void**>(&compMgr))) &&
                compMgr != nullptr) {
                compMgr->GetCompartment(kGUID_COMPARTMENT_KEYBOARD_OPENCLOSE,
                                        &modeCompartment_);
                compMgr->Release();
            }
        }
        syncModeCompartment();
        // 模式指示器归单例 UI 进程所有（chaoset-ui.exe 托盘图标唯一）；
        // TIP 只上报状态，绝不自己画托盘（每进程一个 = 任务栏多个图标）。
        //
        // 只有**本进程就是前台**时才上报/允许懒启动：TSF 会提前 Activate 后台
        // 进程的 TIP（实机 tsf.log 有 activate pid=2872 fgpid=12156、fgpid=0），
        // 后台实例上报会把用户当前的托盘模式改回自己的状态（2026-11-05
        // 评审 P0）。
        // 只在「本线程激活档案确实是我们 + 本进程就是前台」时才上报/懒启动。
        // 2026-11-06 用户要求「不激活就不要有任何动作」：旧实现无条件上报，
        // 连非激活的后台实例也会去连 UI 宿主管道（失败还会触发懒启动进程），
        // 属于用户可感知的副作用。
        if (isActiveTip()) {
            // 激活时同步一次权威状态（同样只在「拿到焦点/被激活」时做）。
            // 上报的前台判定在 reportModeToUi 入口统一收口；后台实例在这里
            // 就被拦下（不连管道、不拉起 UI 宿主）。
            syncEnglishFromAuthority();
            reportModeToUi(enMode_, true);
        } else {
            // 非激活：撤掉可能残留的全局钩子，确保「零钩子」。
            unhookForeground();
        }
        caretFound_ = false;
        caretFromMouse_ = false;
        // 前台切换兜底钩子**按需注册**（2026-11-06）：SetWinEventHook 是全局钩子
        // （idThread/idProcess 都为 0 时监视全系统前台切换），explorer 单进程就
        // 有 4 个 TIP 实例。在 Activate 里无条件注册 = 每个加载了 TIP 的进程
        // 都挂一个全局钩子，系统设置/开始菜单这类 XAML 宿主对额外钩子尤其敏感。
        // 因此只在「本进程就是前台 + 本线程激活档案是我们」时才挂；真正离开
        // 时由 Deactivate / OnSetFocus(FALSE) 撤掉（见 unhookForeground）。
        if (shouldWatchForeground() && fgHook_ == nullptr) ensureForegroundHook();

        if (!keySinkAdvised_) {
            ITfKeystrokeMgr* mgr = nullptr;
            if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
                keySinkAdvised_ = SUCCEEDED(mgr->AdviseKeyEventSink(clientId_, keySink(), TRUE));
                mgr->Release();
            }
        }
        // daemon 预热**绝不阻塞宿主 UI 线程**（2026-11-06 用户反馈「卡住系统
        // 设置」）：CreateProcessW + 3×20ms 的管道重试加起来可达数百毫秒，
        // 而 Activate 是在宿主 UI 线程上同步调用的（TSF 把 TIP 装进每个进程）。
        //
        // 而且**只有激活时才预热**：拉起 chaosetd 进程本身就是用户可感知的
        // 副作用（任务管理器里多一个进程），非激活的后台 TIP 实例不该触发。
        // 真正的连接等待仍由首次按键路径负责（那里用户确实在用输入法）。
        if (isActiveTip()) warmDaemonInBackgroundOnce();
        // 激活门：Activate 只说明 TSF 把 TIP 装进了本宿主，**不代表用户在用
        // 我们的输入法**（explorer 里 4 个实例、后台进程也会被提前 Activate），
        // 所以这里不无条件开门——只按「本线程激活档案是不是我们」决定。
        candidateWnd_.setActiveGate(isActiveTip());
        return S_OK;
    }

    // ITfTextInputProcessorEx（2026-11-05）：UILess 线程下 TSF 调这个而不是 Activate。
    //
    // 当前只做两件事：记录 dwFlags（拿到「是否真的存在 UILess 宿主」的第一手
    // 数据），然后复用 Activate 的初始化。**渲染路径暂不变**——是否改用
    // ITfUIElementMgr 提交候选，等日志出现 uielementonly=1 再决定。
    //
    // 注意：在 UILess 线程里按微软文档「categorized by GUID_TFCAT_TIPCAP_
    // UIELEMENTENABLED 的 TIP 必须用 ITfUIElementMgr 显示所有 UI」。我们尚未
    // 注册该类别（注册会全局生效，声明与实现不一致时正是 2026-10-30
    // 「开始菜单里候选窗完全消失」的原因），所以这里不会主动走 UI 元素路径。
    HRESULT STDMETHODCALLTYPE ActivateEx(ITfThreadMgr* ptim, TfClientId tid,
                                         DWORD dwFlags) override {
        if (diagBudget()) {
            diagPrintf(L"tsf.log", "activateEx flags=0x%08lX uielementonly=%d selfpid=%lu\n",
                       static_cast<unsigned long>(dwFlags),
                       (dwFlags & TF_TMAE_UIELEMENTENABLEDONLY) ? 1 : 0,
                       static_cast<unsigned long>(GetCurrentProcessId()));
        }
        if ((dwFlags & TF_TMAE_UIELEMENTENABLEDONLY) != 0) {
            // UILess 线程：本壳尚未实现 ITfUIElementMgr，若在这里激活，只能
            // 用自绘 GDI 窗口（宿主不会渲染、且可能盖住游戏/吃掉按键）。
            // 选择**不激活**，等价于旧行为（不实现该接口的 TIP 在此类线程
            // 根本不会被激活），避免 2026-10-30「开始菜单候选窗消失」的
            // 同类事故。真正支持 UILess 需要 ITfUIElementMgr + 候选元素实现。
            diagPrintf(L"tsf.log", "activateEx refused: UIElementOnly thread, not activating\n");
            return S_OK;
        }
        return Activate(ptim, tid);
    }

    HRESULT STDMETHODCALLTYPE Deactivate() override {
        // 停用即「非激活」：彻底退场（销毁 UI + 撤掉所有钩子）。
        // 绝不能留任何 TOPMOST 窗口/全局钩子在别人的应用上面。
        retireUiCompletely();
        pendingStaleReset_ = false;
        if (profileMgr_ != nullptr) {
            profileMgr_->Release();
            profileMgr_ = nullptr;
        }
        if (modeCompartment_ != nullptr) {
            modeCompartment_->Release();
            modeCompartment_ = nullptr;
        }
        // 候选窗已在 retireUiCompletely() 里销毁，这里不再重复。
        // 已确认段先落盘（与 macOS deactivateServer → flushConfirmed 同语义）：
        // TSF 停用本 TIP 时会销毁合成，若直接 endComposition 会连用户确认过的
        // 字一起丢掉。正常失焦路径 OnSetFocus(FALSE) 已先行落盘，这里是
        // 「未经过焦点事件直接停用」的兜底（2026-10-30 审计 P1-3 收尾路径）。
        if (!confirmedPreedit_.empty()) {
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(compCtx_, all);
        }
        endComposition();
        resetSession();
        comp_ = chaoset::Comp();
        // 停用期间发生的按键收不到任何 Test 回调，Shift 记账必须归零，
        // 否则重新激活后第一次 Shift 空按会被「上一轮的孤儿按住」吃掉。
        resetShiftTracking();
        // TIP 重新激活时可能连的是另一套 daemon 配置：方案名丢弃重新缓存，
        // 否则旧方案会让 ';' 错路由（见 syncDpScheme）。
        dpScheme_.clear();
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
            // 失焦 = 非激活：**彻底退场**。
            //
            // 2026-11-06 用户要求：「一个进程或者窗口失去焦点，候选窗之类的东西
            // 也应该全部隐藏……取消任何钩子、关闭任何候选窗，让用户感觉不到这个
            // 输入法的存在」。这里不再只 hide()：直接销毁候选窗/提示条、撤掉
            // 全局钩子、作废光标锚点与联想态（retireUiCompletely 统一收口）。
            retireUiCompletely();
            // 焦点离开：**挂起**而不是直接丢弃按住状态。
            //
            // 浏览器（Chrome/Firefox，尤其「安装为应用」的独立窗口）会在
            // 窗口内部把焦点在地址栏 / 页面元素之间搬动，一样触发
            // OnSetFocus(FALSE)。旧实现无脑 resetShiftTracking()，于是
            // 「按下 Shift → 焦点抖一下 → 松开」永远结算不了，表现为
            // 「能打字但 Shift 切不了中英」。现在：若 Shift 仍物理按下则
            // 只挂起（松开前焦点回来就照常切换），真的切走应用则松开时
            // 只清状态不切换。2026-11-05。
            suspendShiftHoldOnFocusLost();
            // 失去焦点：已确认段先落盘（用户确认过的字不能丢；旧实现里这些
            // 段早已在文档，净效果一致），纯拼音预编辑维持「清空」旧语义——
            // 防止跨窗口残留组词状态导致后续窗口吃键。落盘必须对**合成所属
            // 上下文**（compCtx_）做：此刻 GetFocus 可能已指向新窗口的文档，
            // 经 focusContext() 兜底会把字写进错误的窗口（同 endComposition
            // 的 2026-10-29 审查结论）。
            if (isComposing() || !confirmedPreedit_.empty()) {
                resetSession();
                comp_ = chaoset::Comp();
                if (!confirmedPreedit_.empty()) {
                    const std::string all = confirmedPreedit_;
                    confirmedPreedit_.clear();
                    commit(compCtx_, all);
                } else {
                    endComposition();
                }
            }
        } else {
            // 焦点回来：只有「本线程激活档案确实是我们」才开门/挂钩子。
            if (isActiveTip()) {
                // 获得焦点时的中英状态处理（按作用域二分，2026-11-06 用户确认的语义）：
                //   system          —— 用**全局权威状态覆盖本地**（跨进程查询，整个
                //                      输入过程中唯一的查询点）；
                //   process/window —— 保持**自己本地的**状态（本来就存在进程静态量/
                //                      窗口映射里，无需覆盖），只把 TSF 隔间与托盘
                //                      同步成本窗口的值。
                syncEnglishFromAuthority();
                syncModeCompartment();
                // 上报托盘：前台判定由 reportModeToUi 入口统一收口（拿到焦点的
                // 进程必然是前台；谁有焦点谁就能更新托盘）。
                reportModeToUi(currentEnglish(), false);
                candidateWnd_.setActiveGate(true);
                ensureForegroundHook();
            } else {
                candidateWnd_.setActiveGate(false);
                unhookForeground();
            }
            // 焦点回到本 TIP：清掉「没有活合成、但本地还挂着组词」的陈旧状态。
            //
            // 典型场景（2026-11-05 用户反馈）：Windows 11 开始菜单宿主关闭时
            // 可能不投递 OnSetFocus(FALSE)，本地 comp_ 与合成文本就留了下来；
            // 重新打开开始菜单后旧的拼音又冒出来。这里以「有没有活合成」为准：
            // composition_ 非空说明合成仍在（正常聚焦），保持不动；为空则说明
            // 上一轮合成已随宿主销毁，本地残留必须丢弃。
            if (composition_ == nullptr && (isComposing() || !confirmedPreedit_.empty())) {
                resetComposition();
                comp_ = chaoset::Comp();
                confirmedPreedit_.clear();
                candidateWnd_.hide();
            }
            if (!transport_.isConnected()) {
                // 同样不阻塞宿主 UI 线程：OnSetFocus 也在宿主线程上同步调用。
                warmDaemonInBackgroundOnce();
            }
        }
        // 焦点回来：解除挂起，按住的 Shift 松开时仍可正常切换。
        //
        // **只在获得焦点时清** shiftHoldSuspended_：失焦分支刚由
        // suspendShiftHoldOnFocusLost() 置位，若无条件清，就会让
        // 「按住 Shift 切窗口 / Shift+点击开新窗口，再松开」误切换中英
        //（2026-11-05 回归审计 P1-1，与 docs/ui-parity 的「挂起」语义矛盾）。
        if (fForeground != FALSE) {
            shiftHoldSuspended_ = false;
            // 托盘上报已在本分支开头做过（reportModeToUi 入口有前台闸门），
            // 这里不再重复发一次管道命令。
        }
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                            BOOL* pfEaten) override {
        g_lastOp = kOpTestKeyDown;
        if (bailOutIfInactive()) {
            *pfEaten = FALSE;
            return S_OK;
        }
        applyPendingStaleReset();  // 前台已离开过：先丢掉上一轮的残留组词
        // Shift 记账自愈必须覆盖所有按键（含修饰键）：若上一次按住的 KeyUp
        // 丢了，这里先复位，随后的 Shift 按下才能重新开一次干净的会话。
        reconcileShiftState();
        if (isModifierKey(wParam)) {
            // Shift 空按切换中英的跟踪点（2026-10-31 修复）。
            //
            // 为什么从 OnKeyDown/OnKeyUp 移到这里：TSF 两阶段模型下，
            // OnTestKeyDown 返回 FALSE 的按键（本壳对所有修饰键都返回 FALSE）
            // **不会**再调用 OnKeyDown/OnKeyUp——原实现把跟踪写在 Key 阶段，
            // 修饰键可能永远进不了跟踪函数（中英切换失效）。Test 阶段对每个
            // 按键都会调用，是唯一可靠的位置（OnTestKeyUp 同理）。
            static volatile LONG loggedTestModifier = 0;
            if (InterlockedCompareExchange(&loggedTestModifier, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "modifier in OnTestKeyDown vk=0x%02lX\n",
                           static_cast<unsigned long>(wParam));
            }
            // 只有 Shift 参与「空按切换中英」；Ctrl/Alt/Win 等其它修饰键
            // 不能进 onShiftEvent（否则松 Ctrl 会被误判成 Shift 空按），但
            // 它们属于「Shift 按住期间按过其它键」——必须关闭空按窗口。
            // mac（flagsChanged 清 shiftArmed）/Linux（任何非 Shift 事件清
            // sinceShift）都是这个语义；此前 Windows 漏了这一步，
            // Shift→Ctrl→松 Shift 会误切换中英（2026-10-31 三端复核发现）。
            if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
                onShiftEvent(false, wParam, pic);
            } else if (shiftHeld_ != 0) {
                sinceShift_ = true;
            }
            *pfEaten = FALSE;  // 修饰键始终放行
            return S_OK;
        }
        // 任何非 Shift 键按下都取消「Shift 空按」窗口。必须放在 Test 阶段：
        // 原实现放在 OnKeyDown，Shift+方向键/功能键等未组词的按键不会进
        // OnKeyDown → sinceShift_ 仍为 false → 松 Shift 时误切换中英。
        sinceShift_ = true;
        if (currentEnglish()) {
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
        if (bailOutIfInactive()) {
            *pfEaten = FALSE;
            return S_OK;
        }
        applyPendingStaleReset();
        // Shift 记账自愈也必须覆盖 Key 阶段：Chrome/conhost/Windows Terminal
        // 等宿主的内容键只进 Key 阶段（Test 阶段收不到修饰键），只挂 Test
        // 会让这些宿主丢 KeyUp 后没有收口路径（2026-11-05 回归审计 P2-11）。
        reconcileShiftState();
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            // **第二跟踪点**（2026-11-05 实机日志实证，必须有）。
            //
            // 背景：2026-10-31 曾把跟踪从 Key 阶段整体移到 Test 阶段，理由是
            // 「Test 返回 FALSE 的按键不会再进 Key 阶段」。实机 tsf.log 证明这个
            // 前提只在部分宿主成立，Chrome / conhost / Windows Terminal 恰好相反：
            //   Chrome pid=1376  ：modifier in OnKeyDown vk=0x10（有）
            //                      modifier in OnTestKeyDown（无）→ 永远不切换
            //   Firefox          ：modifier in OnTestKeyDown vk=0x10（有）→ 正常
            // 也就是这些宿主把修饰键**只**送进 Key 阶段。只挂 Test 阶段等于对它们
            // 彻底失效，这正是「能打字但 Shift 切不了」的直接原因。
            //
            // 两个阶段都挂是安全的：shiftHeld_ 位掩码与 begin/end 都是幂等的，
            // 同一按键被两阶段各投递一次不会双计，重复释放会被「孤立释放」拦住。
            static volatile LONG loggedDownModifier = 0;
            if (InterlockedCompareExchange(&loggedDownModifier, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "modifier in OnKeyDown vk=0x%02lX\n",
                           static_cast<unsigned long>(wParam));
            }
            onShiftEvent(false, wParam, pic);
            *pfEaten = FALSE;  // 只透传不消费，Shift 组合键与按键事件完整交给宿主
            return S_OK;
        }
        sinceShift_ = true;  // 幂等兜底（正常已在 OnTestKeyDown 置位）
        *pfEaten = handleKey(wParam, pic) ? TRUE : FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyUp(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                          BOOL* pfEaten) override {
        if (bailOutIfInactive()) {
            *pfEaten = FALSE;
            return S_OK;
        }
        if (isModifierKey(wParam)) {
            static volatile LONG loggedTestUpModifier = 0;
            if (InterlockedCompareExchange(&loggedTestUpModifier, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "modifier in OnTestKeyUp vk=0x%02lX\n",
                           static_cast<unsigned long>(wParam));
            }
            if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
                // 与 OnTestKeyDown 对称：Key 阶段收不到修饰键时，这里也收不到；
                // 跟踪必须放在 Test 阶段（见 OnTestKeyDown 注释）。
                onShiftEvent(true, wParam, pic);
            } else if (shiftHeld_ != 0) {
                // 其它修饰键的释放同样关闭空按窗口：mac 在 flagsChanged（按下
                // 与释放都触发）清 shiftArmed，Linux 在任何非 Shift 事件清
                // sinceShift。典型场景：Ctrl 先按住 → 按 Shift → 松 Ctrl →
                // 松 Shift，Windows 此前会误切换中英。
                sinceShift_ = true;
            }
        }
        *pfEaten = FALSE;  // KeyUp 一律不消费，保证宿主应用状态机完整，绝不导致宿主丢键或键盘卡死
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnKeyUp(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                      BOOL* pfEaten) override {
        if (bailOutIfInactive()) {
            *pfEaten = FALSE;
            return S_OK;
        }
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            // **第二跟踪点**（与 OnKeyDown 对称，见那里的日志实证说明）。
            static volatile LONG loggedUpModifier = 0;
            if (InterlockedCompareExchange(&loggedUpModifier, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "modifier in OnKeyUp vk=0x%02lX\n",
                           static_cast<unsigned long>(wParam));
            }
            onShiftEvent(true, wParam, pic);
            *pfEaten = FALSE;  // 只透传不消费
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
        resetSession();
        comp_ = chaoset::Comp();
        // 合成被宿主终止时 TSF 会把合成内文本（含已确认段）留在文档里，
        // 缓冲使命已结束，必须清空——绝不能串进下一轮组词的预编辑。
        confirmedPreedit_.clear();
        return S_OK;
    }

    void selectCandidateByIndex(int index) {
        g_lastOp = kOpCandidateClick;
        // 联想窗点击：联想候选直接上屏并继续联想（与数字键同路径）
        if (!assoc_.candidates.empty()) {
            const int global = assocPage_ * kPageSize + index;
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
        if (index < 0 || index >= static_cast<int>(comp_.candidates.size())) return;
        ContextRef ctx(focusContext());
        // 鼠标点选走与数字键/空格相同的**渐进上屏**路径（2026-10-30 三端
        // 一致性整改 A4）：此前直接 session_.select(index)，长串拼音下点一个
        // 候选会把整串拼音全部清空/上屏，而键盘点选只消耗该候选对应音节、
        // 剩余拼音回灌继续组词——同一个候选窗两种选词语义。macOS
        // ChaosetController.commit 与下方键盘路径都先 selectPrefix，失败
        //（daemon 不可达/越界）才回退整句上屏。
        const std::string pickedSyllables = comp_.candidates[index].syllables;
        chaoset::Comp rest;
        if (session_.selectPrefix(index, &rest) && !rest.committed.empty()) {
            syncDpScheme(rest);  // 契约：每次 Composition 响应都带双拼方案名
            // 单合成渐进（见 confirmedPreedit_ 注释）：确认段并入当前合成，
            // 不再中途结束/重建合成。剩余耗尽才整串一次落盘并进入联想。
            confirmedPreedit_ += rest.committed;
            comp_ = rest;
            if (!comp_.input.empty()) {
                render(ctx.get());
            } else {
                comp_ = chaoset::Comp();
                const std::string all = confirmedPreedit_;
                confirmedPreedit_.clear();
                commit(ctx.get(), all);
                enterAssociate(pickedSyllables);
            }
            return;
        }
        std::string committed;
        if (session_.select(index, &committed)) {
            const std::string all = confirmedPreedit_ + committed;
            confirmedPreedit_.clear();
            commit(ctx.get(), all);
        }
    }

    // 候选窗右键「删除该词」：按候选的「词 + 音节」删除本机学习数据
    // （daemon 的 Words 调频 + Phrases 词组；bigram 上下文共现保留，见
    // engine.UserDict.DeleteWord 注释）。约束与 macOS 壳一致：
    //   - 只删学习记录：不上屏、不改文档、不结束组词；
    //   - 写类 IPC 不重试（重试会把「已删除」变成「未找到」，也不应重放写请求）；
    //   - 删除成功后刷新候选：组词态 reset + 重放当前剩余拼音（daemon 不缓存
    //     候选列表）；联想态按同一联想来源音节重新查询，删空则收起面板。
    void deleteCandidateByIndex(int index) {
        g_lastOp = kOpCandidateClick;
        if (!assoc_.candidates.empty()) {
            const int global = assocPage_ * kPageSize + index;
            if (index < 0 || global >= static_cast<int>(assoc_.candidates.size())) return;
            const chaoset::Candidate pick = assoc_.candidates[global];
            bool deleted = false;
            if (!session_.userDelete(pick.text, splitSyllables(pick.syllables), &deleted)) {
                showToast(L"删除失败：daemon 不可达", true);
                return;
            }
            if (!deleted) {
                showToast(L"未找到该词的学习记录", true);
                return;
            }
            const std::string src = assocSylls_;
            assoc_ = chaoset::Comp();
            assocPage_ = 0;
            std::vector<std::string> sylls = splitSyllables(src);
            chaoset::Comp refreshed;
            if (!sylls.empty() && session_.associate(sylls, &refreshed) &&
                !refreshed.candidates.empty()) {
                syncDpScheme(refreshed);
                assoc_ = std::move(refreshed);
                assocPage_ = 0;
                showAssociatePage();
            } else {
                candidateWnd_.hide();
            }
            showToast(L"已删除该词", true);
            return;
        }
        if (!hasCandidates()) return;
        if (index < 0 || index >= static_cast<int>(comp_.candidates.size())) return;
        const chaoset::Candidate pick = comp_.candidates[index];
        bool deleted = false;
        if (!session_.userDelete(pick.text, splitSyllables(pick.syllables), &deleted)) {
            showToast(L"删除失败：daemon 不可达", true);
            return;
        }
        if (!deleted) {
            showToast(L"未找到该词的学习记录", true);
            return;
        }
        // 重新组词刷新候选。已确认段 confirmedPreedit_ 是壳侧状态，不影响。
        ContextRef ctx(focusContext());
        const std::string raw = comp_.input;
        resetComposition();
        if (raw.empty()) {
            comp_ = chaoset::Comp();
            render(ctx.get());
        } else {
            chaoset::Comp rest = session_.input(raw, halfWidth_);
            if (!rest.ok) {
                // 极端故障（daemon 半死）：已确认段先落盘，避免合成悬空丢字。
                diagDaemonRequestFailure(transport_, "input(refresh)");
                comp_ = chaoset::Comp();
                flushOrCancel(ctx.get());
                showToast(L"删除后刷新候选失败", true);
                return;
            }
            syncDpScheme(rest);
            comp_ = std::move(rest);
            render(ctx.get());
        }
        showToast(L"已删除该词", true);
    }

    void flipPage(bool next) {
        if (!hasCandidates() || comp_.pageCount <= 1) return;
        g_lastOp = kOpCandidateClick;
        ContextRef ctx(focusContext());
        comp_ = next ? session_.nextPage() : session_.prevPage();
        if (!comp_.ok) {
            // daemon 故障：已确认段先落盘收尾，不能随翻页失败被 endComposition 丢弃
            diagDaemonRequestFailure(transport_, "page(mouse)");
            flushOrCancel(ctx.get());
            return;
        }
        syncDpScheme(comp_);
        render(ctx.get());
    }

private:
    ITfKeyEventSink* keySink() { return static_cast<ITfKeyEventSink*>(this); }
    ITfCompositionSink* compositionSink() { return static_cast<ITfCompositionSink*>(this); }

    bool isComposing() const { return !comp_.input.empty(); }
    bool hasCandidates() const { return !comp_.candidates.empty(); }

    // ---- 中英切换：Shift 空按（按下后未按其他键即释放）----
    // 语义与 ibus 壳的 shiftTracker（shims/linux/engine/main.cpp:50-72）一致；
    // 事件路径见 OnTestKeyDown 等分流。
    //
    // **记账方式 2026-11-05 重写：按「一次按住会话」记账，不再按事件累加。**
    // 旧实现用 shiftDown_++/--：每收到一次 Shift 按下就 +1、每收到一次释放才 -1，
    // 只有计数回到 0 才结算。只要丢失**任意一次** KeyUp，计数就只增不减、永远
    // 回不到 0 —— 该进程内 Shift 空按**永久失效**，只能重启应用恢复。
    // 丢失 KeyUp 的典型场景（都不是宿主 bug，而是 Windows 的既定行为）：
    //   1. 按住 Shift 时切换窗口（Alt+Tab / 点别的窗口）：KeyUp 交给**新前台
    //      窗口**，本线程永远收不到；
    //   2. TIP 被 TSF 停用期间发生的按键（Deactivate → 再 Activate）；
    //   3. 宿主重复投递同一次按下（auto-repeat / TSF 与宿主各投递一次）。
    // 这正好解释实机症状：Terminal / 开始菜单 / Chrome（长驻进程 + 频繁切走
    // 窗口）切不了，Firefox（同一会话里连续空按）可以。
    // 本地中英模式缓存。真正语义由 ModeScope 决定（见 currentEnglish）：
    //   system  —— 权威在 UI 宿主，这里只是「上次查到的值」，按键时会刷新。
    //   process —— 本进程所有 TIP 实例共享（下面 g_procEnglish）。
    //   window  —— 按宿主窗口分开存（g_winEnglish）。
    bool enMode_ = false;

    // 进程级/窗口级状态（process / window 作用域用）。故意放在进程静态区：
    // TSF 会在同一宿主进程里创建多个 TIP 实例，process 档要求它们共享。
    static std::map<HWND, bool>& windowModes() {
        static std::map<HWND, bool> m;
        return m;
    }
    static bool& processEnglish() {
        static bool english = false;
        return english;
    }

    // 当前宿主窗口（window 作用域的分桶键）。拿不到就退化成进程级，
    // 绝不能因为取不到窗口而让状态随机跳。
    HWND modeBucketKey() const {
        const HWND fg = GetForegroundWindow();
        if (fg != nullptr) return fg;
        return GetActiveWindow();
    }

    // 读当前中英模式（**所有判断都应该走这里**，不要直接读 enMode_）。
    //
    // 关键性能约束（2026-11-06 用户要求）：「窗口在获取焦点以后才有必要去获取、
    // 更新这个状态，用不到的时候没必要浪费性能」。所以这里**只读本地缓存**，
    // 不做任何 IPC —— 它在每个按键的 OnTestKeyDown 里都会被调用。
    // system 作用域的跨进程同步只在 OnSetFocus / Activate 时做一次（见
    // syncEnglishFromAuthority）。
    bool currentEnglish() {
        switch (uiLoadModeScope()) {
            case ModeScope::System:
                return enMode_;
            case ModeScope::Process:
                return processEnglish();
            case ModeScope::Window: {
                const HWND key = modeBucketKey();
                auto it = windowModes().find(key);
                if (it != windowModes().end()) return it->second;
                windowModes()[key] = enMode_;
                return enMode_;
            }
        }
        return enMode_;
    }

    // 从权威源同步一次（**只在获得焦点/激活时调用**）。
    //
    // 用户明确要求不要在打字路径上做跨进程查询。窗口拿到焦点时同步一次即可：
    // 此时用户刚切过来，正是最需要「显示正确中英状态」的时刻；之后整个输入
    // 过程都只读本地缓存，零 IPC 开销。
    void syncEnglishFromAuthority() {
        if (uiLoadModeScope() != ModeScope::System) return;
        bool english = enMode_;
        if (uiQueryMode(&english)) {
            enMode_ = english;
            syncModeCompartment();  // 让系统输入指示器/隔间也跟上
        }
    }

    // 写当前中英模式（所有切换都必须走这里）。
    void storeEnglish(bool english) {
        enMode_ = english;
        switch (uiLoadModeScope()) {
            case ModeScope::System:
                // 权威在 UI 宿主：上报即写入（system 档下 wndproc 会更新托盘+HKCU）。
                break;
            case ModeScope::Process:
                processEnglish() = english;
                break;
            case ModeScope::Window:
                windowModes()[modeBucketKey()] = english;
                break;
        }
        syncModeCompartment();
        // 上报托盘：reportModeToUi 入口统一要求「本进程持有焦点」才放行
        // （2026-11-06 用户规则：谁有焦点谁就能更新托盘）。后台窗口切中英
        // 只改本地状态，不会污染托盘显示。
        reportModeToUi(english, true);
    }
    // 当前按住的 Shift 物理键位掩码（bit0=左、bit1=右、bit2=泛化 VK_SHIFT）。
    // 用**掩码**而不是计数器：置位/清位天然幂等，键盘 auto-repeat 或宿主重复
    // 投递同一次按下都不会把它推到「永远回不到 0」——这正是旧实现 ++/-- 的
    // 致命缺陷。同时掩码能正确区分左右 Shift 同时按住的场景。
    unsigned shiftHeld_ = 0;
    bool sinceShift_ = true;    // 按住期间是否出现过其它键（true = 不是空按）
    // 按住期间焦点离开过。浏览器会在**窗口内部**把焦点在地址栏/页面元素之间
    // 搬来搬去，一样触发 OnSetFocus(FALSE)——此时不能直接丢弃按住状态，
    // 否则「按下 Shift → 焦点抖一下 → 松开」就永远切换不了（2026-11-05）。
    // 改为挂起：松开前焦点回来就照常切换，否则松开时只清状态不切换
    // （真·切走应用时不该切模式）。
    bool shiftHoldSuspended_ = false;

    // ---- 前台切换兜底（2026-11-05）----
    //
    // 为什么需要：Windows 11 开始菜单/搜索这类宿主**不投递 OnSetFocus(FALSE)**
    //（实机：关闭开始菜单后重开，上一轮的拼音与候选窗都还在）。TSF 焦点事件
    // 在这类宿主上不可靠，改用系统级的前台窗口事件（SetWinEventHook，
    // 事件驱动、非轮询）作为「用户已经离开本窗口」的可靠信号。
    //
    // 回调里**不碰 TSF**（可能不在本 TIP 的焦点上下文中，贸然 RequestEditSession
    // 会失败甚至干扰宿主）：只收起自己的浮窗并打标记，真正的状态清理由下一次
    // 按键在 TIP 线程上完成。
    HWINEVENTHOOK fgHook_ = nullptr;
    bool pendingStaleReset_ = false;

    // 线程级隔间（GUID_COMPARTMENT_KEYBOARD_OPENCLOSE）：把中英模式写进 TSF
    // 线程隔间（其他 TSF 客户端可读，语义与微软拼音一致）。任务栏「中/英」
    // 指示器由单例 UI 进程绘制，TIP 不再持有任何托盘状态。
    ITfCompartment* modeCompartment_ = nullptr;

    // **当前激活的输入法是不是我们**（2026-11-05 用户反馈：选定的输入法不是
    // chaoset 时按 Shift 仍会出中英提示）。
    //
    // 实测：切到其它输入法/英文键盘布局后，TSF 仍会把按键投给本 TIP 的按键
    // 下沉（我们的 Deactivate 未被调用或不及时），于是 Shift 照样触发切换。
    // 正确判据是查询本线程的**激活输入处理器档案**：
    //   ITfInputProcessorProfileMgr::GetActiveProfile(GUID_TFCAT_TIP_KEYBOARD)
    // 返回的 clsid 不是我们 → 直接放行所有按键，绝不参与输入。
    ITfInputProcessorProfileMgr* profileMgr_ = nullptr;

    bool isActiveTip() {
        // 查询器不可用 → **fail-closed**（按「不是我们」处理）。
        //
        // 2026-11-06 用户要求：「输入法不激活就不该有任何动作，让用户感觉不到
        // 它的存在」。旧实现这里 return true（怕把输入搞坏），结果是：只要
        // 档案查询器建不出来，后台 TIP 实例就会被当成激活着，继续建候选窗、
        // 挂全局钩子、处理按键——正是用户看到的「鼠标点了没反应 / 系统设置卡」。
        // 反过来 fail-closed 的最坏后果只是「输入法暂时不出候选」，比干扰系统小得多。
        if (profileMgr_ == nullptr) return false;
        TF_INPUTPROCESSORPROFILE prof{};
        const HRESULT hr = profileMgr_->GetActiveProfile(kGUID_TFCAT_TIP_KEYBOARD, &prof);
        // 首次查询结果留一条日志：否则「非激活整键放行」是否生效只能靠猜
        //（2026-11-05 回归审计 P2-5：全量日志 0 条 inactive 无法自证）。
        static volatile LONG loggedQuery = 0;
        if (InterlockedCompareExchange(&loggedQuery, 1, 0) == 0) {
            diagPrintf(L"tsf.log", "activetip query hr=0x%08lX type=%lu match=%d\n",
                       static_cast<unsigned long>(hr),
                       static_cast<unsigned long>(prof.dwProfileType),
                       prof.clsid == CLSID_ChaosetTIP ? 1 : 0);
        }
        // 查询失败同样 fail-closed：宁可放行按键，也不在不确定时干预用户。
        if (FAILED(hr)) return false;
        return prof.dwProfileType == TF_PROFILETYPE_INPUTPROCESSOR && prof.clsid == CLSID_ChaosetTIP;
    }
    // 前台钩子回调 → TIP 实例的映射。SetWinEventHook 没有 user data，但回调
    // 第一个参数就是 hook 句柄，可以据此找回实例。
    //
    // **不能用单个 thread_local 指针**：同一线程理论上可能有多个 TIP 实例
    // （重复激活、宿主自建线程管理器），后注册的会覆盖前者，回调就派发给
    // 错误实例（隐藏别人的浮窗、清错状态）。WINEVENT_OUTOFCONTEXT 的回调在
    // 注册线程上派发，所以 thread_local 容器是正确的线程安全边界。
    struct HookEntry {
        HWINEVENTHOOK hook;
        ChaosetTIP* tip;
    };
    static thread_local std::vector<HookEntry> hookRegistry_;

    static void removeHookEntry(HWINEVENTHOOK hook) {
        for (auto it = hookRegistry_.begin(); it != hookRegistry_.end(); ++it) {
            if (it->hook == hook) {
                hookRegistry_.erase(it);
                return;
            }
        }
    }

    static void CALLBACK fgHookProc(HWINEVENTHOOK hook, DWORD event, HWND hwnd, LONG, LONG,
                                    DWORD, DWORD) {
        if (event != EVENT_SYSTEM_FOREGROUND) return;
        for (const HookEntry& entry : hookRegistry_) {
            if (entry.hook == hook && entry.tip != nullptr) {
                entry.tip->onForegroundChanged(hwnd);
                return;
            }
        }
    }

    void onForegroundChanged(HWND fg) {
        if (fg == nullptr) return;
        // 前台仍停在我们自己正组词的那个窗口上：不是「离开」，不动状态。
        //
        // **回调里绝不碰 TSF**（旧实现这里调 hostWindowOf(compCtx_) → COM，
        // 与「前台回调只 hide + 置位」的契约矛盾，且旧上下文可能已失效）。
        // 合成窗口 HWND 在 render() 里用 TSF 线程缓存好，这里只读。
        if (composeHostWnd_ != nullptr && fg == composeHostWnd_) return;
        candidateWnd_.hide();
        toast_.hide();
        if (isComposing() || !confirmedPreedit_.empty() || !assoc_.candidates.empty()) {
            pendingStaleReset_ = true;
            if (diagBudget()) {
                diagPrintf(L"tsf.log", "stale state dropped (foreground changed) selfpid=%lu\n",
                           static_cast<unsigned long>(GetCurrentProcessId()));
            }
        }
    }

    // 下一次按键时在 TIP 线程上清掉残留（不使用编辑会话：旧上下文可能已销毁）。
    // 当前激活的输入法不是我们 → 所有按键立即放行，收掉一切 UI。
    // 返回 true 表示调用方应直接返回 S_OK（pfEaten=FALSE）。
    // ---- 前台钩子的按需注册（2026-11-06 用户反馈「卡住系统设置」）----
    //
    // SetWinEventHook(EVENT_SYSTEM_FOREGROUND, ..., idThread=0, idProcess=0) 是
    // **全系统**钩子：每个加载了 TIP 的宿主进程都会挂一个（explorer 单进程实测
    // 4 个 TIP 实例）。这会增加系统前台切换的派发开销，而系统设置/开始菜单
    // 这类 XAML/AppContainer 宿主对额外钩子尤其敏感。
    //
    // 结论：只有当「本线程确实在用我们的输入法」时才挂；退到后台/切走输入法时
    // 立刻撤掉。钩子的作用只是兜住「宿主不投递 OnSetFocus(FALSE)」，非激活时
    // 本就用不上。
    bool shouldWatchForeground() const {
        // 必须先看激活档案：后台 TIP 实例（TSF 会提前 Activate）不该挂全局钩子。
        if (!const_cast<ChaosetTIP*>(this)->isActiveTip()) return false;
        // 本进程还必须是前台：完全在后台的宿主不需要兜底。
        const HWND fg = GetForegroundWindow();
        if (fg == nullptr) return false;
        DWORD fgPid = 0;
        GetWindowThreadProcessId(fg, &fgPid);
        return fgPid == GetCurrentProcessId();
    }

    void ensureForegroundHook() {
        if (fgHook_ != nullptr || !shouldWatchForeground()) return;
        fgHook_ = SetWinEventHook(EVENT_SYSTEM_FOREGROUND, EVENT_SYSTEM_FOREGROUND,
                                  nullptr, fgHookProc, 0, 0, WINEVENT_OUTOFCONTEXT);
        if (fgHook_ != nullptr) {
            hookRegistry_.push_back(HookEntry{fgHook_, this});
        } else if (diagBudget()) {
            diagPrintf(L"tsf.log", "foreground hook FAILED err=%lu\n", GetLastError());
        }
    }

    void unhookForeground() {
        if (fgHook_ == nullptr) return;
        UnhookWinEvent(fgHook_);
        removeHookEntry(fgHook_);
        fgHook_ = nullptr;
    }

    // daemon 预热（一次性，后台线程）：Activate 里绝不阻塞宿主 UI 线程。
    // 真正的「连不上就等/降级」仍由首次按键路径（OnTestKeyDown →
    // ensureDaemonConnected）负责，那里的等待有上限且用户可感知。
    // 线程入口必须是 __stdcall 普通函数：32 位目标下 lambda 不能隐式转成
    // LPTHREAD_START_ROUTINE（-fpermissive 才放过，不能依赖）。
    static DWORD WINAPI daemonWarmThread(LPVOID) {
        ensureDaemonStarted();
        return 0;
    }

    static void warmDaemonInBackgroundOnce() {
        static volatile LONG started = 0;
        if (InterlockedCompareExchange(&started, 1, 0) != 0) return;
        HANDLE th = CreateThread(nullptr, 0, daemonWarmThread, nullptr, 0, nullptr);
        if (th != nullptr) CloseHandle(th);
    }

    // ---- 「完全退场」：让用户感觉不到这个输入法的存在 ----
    //
    // 2026-11-06 用户要求：非激活 / 窗口失去焦点时，候选窗、提示条、钩子、定时器
    // 都必须消失，不留任何会在屏幕上或系统派发里冒头的东西。
    //
    // 与「只是 hide()」的区别：
    //   * hide()  只是 ShowWindow(SW_HIDE)，窗口对象还在、类还注册着，一旦哪条
    //     路径漏判又 ShowWindow 就会重新冒出来；TOPMOST 窗口还活着本身就是
    //     用户能感知的存在。
    //   * retire() 直接 destroy() 窗口 + 撤掉所有钩子 + 停掉定时器，下次真正
    //     激活时按需重建（建窗成本远小于「挡住用户鼠标」的代价）。
    void retireUiCompletely() {
        // 1) 候选窗/提示条：销毁而不是隐藏（destroy 内部会把 g_live* 计数归还）。
        candidateWnd_.setActiveGate(false);
        candidateWnd_.destroy();
        toast_.destroy();
        // 2) 所有钩子（全局前台钩子只在激活时挂，这里撤销即回到「零钩子」）。
        unhookForeground();
        // 3) 焦点锚点与联想态作废：它们只对「刚才那个窗口」有意义。
        caretFound_ = false;
        caretFromMouse_ = false;
        clearAssociate();
    }

    bool bailOutIfInactive() {
        if (isActiveTip()) {
            // 激活中：允许候选窗显示（bailOut 是每键都会走的便宜闸门，
            // 顺手把门打开，保证从「非激活」恢复后候选窗能正常出现）。
            candidateWnd_.setActiveGate(true);
            return false;
        }
        // 非激活：**彻底退场**（销毁窗口、撤钩子），而不是只 hide。
        retireUiCompletely();
        static volatile LONG loggedInactive = 0;
        if (InterlockedCompareExchange(&loggedInactive, 1, 0) == 0) {
            diagPrintf(L"tsf.log", "inactive profile: keys passed through\n");
        }
        candidateWnd_.hide();
        toast_.hide();
        // 联想态也要清：否则切到别的输入法再切回来，数字/空格会继续用
        // 上一次的联想列表选词（2026-11-05 回归审计 P2-6）。
        clearAssociate();
        pendingStaleReset_ = false;
        resetShiftTracking();
        return true;
    }

    void applyPendingStaleReset() {
        if (!pendingStaleReset_) return;
        pendingStaleReset_ = false;
        // 用户用数字键确认过的段不能丢：与 OnSetFocus(FALSE) 同语义，先落盘
        //（挂起清理只该丢未确认的拼音，不该丢已确认文本）。
        if (!confirmedPreedit_.empty() && composition_ != nullptr) {
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(compCtx_, all);
        }
        // **必须先结束 TSF 合成本身**（2026-11-05 实机）：只清本地状态的话，
        // 宿主文档里的预编辑文本会原样留着——开始菜单失焦重开后旧拼音还在，
        // 原因就在这里。endComposition 会对合成所属上下文发编辑会话，
        // 把预编辑文本从文档里删掉。
        endComposition();
        resetSession();
        comp_ = chaoset::Comp();
        confirmedPreedit_.clear();
        clearAssociate();
        candidateWnd_.hide();
    }

    // AppContainer 宿主不能 CreateProcess：请求 daemon（普通用户完整性、运行在
    // 同一用户会话）代拉起同目录的 chaoset-ui.exe（IPC 方法 "ui"）。
    // daemon 不可达时返回 false，调用方按失败降级。
    // settings=true 时让 daemon 以 --settings 拉起 UI 宿主：首实例直接弹出
    // 设置窗，因此**调用方不需要再轮询等管道就绪**（2026-11-06 清理）。
    bool ensureUiHostStartedByDaemon(bool settings = false) {
        if (!transport_.isConnected() && !ensureDaemonConnected(transport_)) return false;
        return session_.ensureUiHost(settings);
    }


    static unsigned shiftBit(WPARAM vk) {
        if (vk == VK_LSHIFT) return 1u;
        if (vk == VK_RSHIFT) return 2u;
        return 4u;  // 泛化 VK_SHIFT：多数宿主只投递这个
    }

    // 系统层面是否仍有任一 Shift 物理按下。只在持有按住会话时才查询
    // （唯一调用点 reconcileShiftState 已先判 shiftHeld_），正常打字路径零额外开销。
    // 用来兜住上下文切换/焦点变化造成的 KeyUp 丢失。
    static bool shiftPhysicallyDown() {
        return (GetAsyncKeyState(VK_LSHIFT) & 0x8000) != 0 ||
               (GetAsyncKeyState(VK_RSHIFT) & 0x8000) != 0;
    }

    // 焦点/停用复位。Linux 壳在 focusIn/focusOut 调 shift_.reset()
    // （shims/linux/engine/main.cpp:385-389），Windows 此前完全没有这一步：
    // 按住 Shift 切走窗口时 KeyUp 不会回来，复位是唯一能保证不卡死的时机。
    void resetShiftTracking() {
        shiftHeld_ = 0;
        sinceShift_ = true;  // 复位后首个释放不得触发切换
        shiftHoldSuspended_ = false;
    }

    // 焦点暂时离开（仍在同一进程/同一窗口族）：不丢按住状态，只挂起。
    void suspendShiftHoldOnFocusLost() {
        if (shiftHeld_ != 0 && shiftPhysicallyDown()) {
            shiftHoldSuspended_ = true;
        } else {
            resetShiftTracking();
        }
    }

    // 开始一次「按住会话」：位运算幂等（auto-repeat / 重复投递都不累积）。
    void beginShiftHold(unsigned bit) {
        if ((shiftHeld_ & bit) != 0) return;
        if (shiftHeld_ == 0) {
            sinceShift_ = false;
            shiftHoldSuspended_ = false;
        }
        shiftHeld_ |= bit;
    }

    // 空按判定 + 切换。pic 为事件路径带来的上下文；自愈收口时传 nullptr，
    // 内部再取 focusContext()（必须用 ContextRef 释放）。
    void finishShiftHold(ITfContext* pic) {
        const bool suspended = shiftHoldSuspended_;
        shiftHoldSuspended_ = false;
        // 纯事件驱动：按键只会在本 TIP 有焦点时到达，不需要额外的前台门控。
        if (!sinceShift_ && !suspended) {
            if (pic != nullptr) {
                toggleEnMode(pic);
            } else {
                ContextRef owned(nullptr);
                owned.ctx = focusContext();
                toggleEnMode(owned.ctx);
            }
        }
        sinceShift_ = true;
    }

    // 结束一次「按住会话」：只有最后一次抬起才结算。
    void endShiftHold(unsigned bit, ITfContext* pic = nullptr) {
        if ((shiftHeld_ & bit) == 0) return;
        shiftHeld_ &= ~bit;
        if (shiftHeld_ != 0) return;  // 另一侧还按着，等它抬起
        finishShiftHold(pic);
    }

    // 无条件收口：事件路径留下的按住位（左右/泛化位混用）在物理松开后
    // 由下一次按键统一结算，避免任何一位永远挂着。
    void settleAllShiftHolds() {
        if (shiftHeld_ == 0) return;
        shiftHeld_ = 0;
        finishShiftHold(nullptr);
    }

    // 把当前中英模式写进线程级隔间，供 Windows 输入指示器显示。
    // 语义（微软《predefined compartments》）：非零 = 键盘打开（中文输入）。
    void syncModeCompartment() {
        if (modeCompartment_ == nullptr) return;
        // 手工零初始化（VariantInit 在 oleaut32，本壳不链接该库）。
        VARIANT var;
        ZeroMemory(&var, sizeof(var));
        var.vt = VT_I4;
        var.lVal = currentEnglish() ? 0 : 1;  // 非零 = 键盘打开（中文）
        const HRESULT hr = modeCompartment_->SetValue(clientId_, &var);
        // 回读验证：SetValue 返回 S_OK 也可能没生效（跨线程/宿主覆盖），
        // 没有回读就无法判断「任务栏中/英不显示」到底是没写进去还是系统没画。
        VARIANT back;
        ZeroMemory(&back, sizeof(back));
        const HRESULT hrGet = modeCompartment_->GetValue(&back);
        // 日志节制：成功后只记一次，失败每次都记（不受 diagBudget 限制——
        // 每键日志会把 200 次预算烧光，正好吞掉失败原因）。
        static volatile LONG loggedOnce = 0;
        const bool failed = FAILED(hr) || FAILED(hrGet);
        if (failed || InterlockedCompareExchange(&loggedOnce, 1, 0) == 0) {
            diagPrintf(L"tsf.log",
                       "mode compartment en=%d set_hr=0x%08lX get_hr=0x%08lX read=%ld\n",
                       currentEnglish() ? 1 : 0, static_cast<unsigned long>(hr),
                       static_cast<unsigned long>(hrGet),
                       static_cast<long>(SUCCEEDED(hrGet) ? back.lVal : -1));
        }
    }

    void toggleEnMode(ITfContext* pic) {
        // 翻转：读当前（可能来自 UI 宿主的权威值）→ 取反 → 写回。
        storeEnglish(!currentEnglish());
        if (diagBudget()) {
            // 诊断补前台信息：Firefox/Chrome 的「安装应用」(PWA) 窗口是独立
            // 进程/窗口，若 fgpid != selfpid 说明本 TIP 并不在拥有焦点的那个
            // 进程里——这是判断「PWA 里切不了」到底卡在哪一层的直接证据。
            const HWND fg = GetForegroundWindow();
            DWORD fgPid = 0;
            if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
            // 合成上下文所属窗口：**只在真有合成时**才有意义（没合成时
            // compCtx_ 为空，之前据此算出的 samewnd 恒为 0，是无效字段）。
            // Firefox 多窗口场景下若合成窗口 != 前台窗口，切换时对旧窗口上下文
            // 做编辑会话就可能把那个窗口顶到前面（2026-11-05 用户反馈）。
            DWORD compPid = 0;
            HWND composeWnd = nullptr;
            if (compCtx_ != nullptr) {
                composeWnd = hostWindowOf(compCtx_);
                if (composeWnd != nullptr) GetWindowThreadProcessId(composeWnd, &compPid);
            }
            const bool pending = isComposing() || !confirmedPreedit_.empty();
            diagPrintf(L"tsf.log",
                       "shift toggle enMode=%d fgpid=%lu selfpid=%lu pending=%d "
                       "comppid=%lu samewnd=%d\n",
                       currentEnglish() ? 1 : 0, static_cast<unsigned long>(fgPid),
                       static_cast<unsigned long>(GetCurrentProcessId()), pending ? 1 : 0,
                       static_cast<unsigned long>(compPid),
                       (!pending || (composeWnd != nullptr && composeWnd == fg)) ? 1 : 0);
        }
        // 模式切换收尾：已确认段先落盘（用户确认过的字不能丢），纯拼音仍按
        // 旧语义丢弃；此前直接 endComposition 会清空 confirmedPreedit_
        // （审计 P1-3）。
        //
        // 收尾按「有没有未上屏内容」分流（2026-11-05 三审）：
        //
        // 切中英本身是纯壳本地状态，唯一需要通知 daemon 的理由是丢弃当前组词。
        // 没有未上屏内容时这次 IPC 纯属空转（同步往返，daemon 忙时可达 250ms），
        // 而且它发生在按键回调里——PWA/独立窗口这类宿主对 TSF 编辑会话更敏感，
        // 少做一次无意义往返就少一类干扰源。
        //
        // 保留 resetComposition 语义：只丢组词、**保留** prev/history，
        // 所以「切模式把刚上屏的词也忘掉」的问题不会回来。
        const bool pending = isComposing() || !confirmedPreedit_.empty();
        if (pending) {
            flushOrCancel(pic);
        } else if (!dpScheme_.empty()) {
            // 双拼例外：引擎可能持有「暂存首键」(dpPending)，壳侧 comp_ 为空
            // 也看不见它，必须让 daemon 清一次，否则下一轮会拿旧首键解码。
            resetComposition();
            comp_ = chaoset::Comp();
        } else {
            comp_ = chaoset::Comp();  // 全拼且无未上屏内容：零 IPC
        }
        candidateWnd_.hide();
        // 模式提示只有一个出口：单例 UI 进程的任务栏指示器。不再有浮动 toast
        // 兜底——那正是用户看到的「输入光标附近 / 鼠标附近也在提示」的来源
        //（2026-11-05 用户反馈：三处同时提示 + 任务栏多个图标）。
        // storeEnglish() 已经上报过，这里不再重复（每次按键一次管道往返太贵）。
    }

    // vk 必须是原始 WPARAM（VK_SHIFT / VK_LSHIFT / VK_RSHIFT），
    // 据此决定置/清哪一位——左右 Shift 同时按住时各自记一位。
    void onShiftEvent(bool release, WPARAM vk, ITfContext* pic) {
        const unsigned bit = shiftBit(vk);
        if (!release) {
            beginShiftHold(bit);
            return;
        }
        if ((shiftHeld_ & bit) != 0) {
            endShiftHold(bit, pic);
            return;
        }
        // 无按下的孤立释放。宿主混用键码时（按下 VK_LSHIFT、释放 VK_SHIFT 或
        // 反之）位置对不上——若 Shift 确已不在物理按下状态就整体结算，
        // 否则会留下一位永远挂着。
        if (shiftHeld_ == 0 || shiftPhysicallyDown()) return;
        settleAllShiftHolds();
    }

    // 结算一次已经松开的 Shift 按住会话。
    //
    // 事件路径（OnTestKeyUp）与按键自愈共用这一个出口，保证两条来源的
    // 判定规则完全一致：只有「期间没按过任何别的键/鼠标」才切换中英。
    void reconcileShiftState() {
        // 只做「物理已松开但状态还挂着」的收口；按住中一律不干预。
        // 旧实现会按焦点门控直接清状态——窗口内焦点抖动时正好把正在按的
        // Shift 吃掉，这是「能打字但 Shift 切不了」的直接原因之一
        //（2026-11-05 修）。
        //
        // 纯事件驱动：不靠定时器，只在**下一次按键**到达时顺手自愈。
        // 若这里真的收口了，说明宿主丢了 Shift 的释放事件——留一条一次性
        // 证据，排障时能直接看出「哪个宿主不投递 KeyUp」，零常驻开销。
        if (shiftHeld_ != 0 && !shiftPhysicallyDown()) {
            static volatile LONG loggedLostKeyUp = 0;
            if (!sinceShift_ && InterlockedCompareExchange(&loggedLostKeyUp, 1, 0) == 0 &&
                diagBudget()) {
                diagPrintf(L"tsf.log", "shift: 宿主丢弃了 Shift 释放事件，已按物理状态收口\n");
            }
            settleAllShiftHolds();
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

    // 双拼模式的 ';' 键：US 布局非 Shift 的 ;（VK_OEM_1）。双拼时它不是
    // 标点键而是引擎的组词/解码键（引擎契约：input(";")，dpPending 非 0
    // 时与字母一样成对解码，为 0 时按 punct(";") 语义上屏首选+分号）。
    // Shift+; 产生的 ':' 不是它，仍走标点分支——与 mac 的字符级判定一致。
    static bool isDpSemicolonKey(WPARAM vk, bool shift) {
        return !shift && vk == kVkOemSemicolon;
    }

    // 数字键 → 页内序号（0 起）：主键盘 1-9 与小键盘 1-9 都支持。
    // 返回 -1 表示不是数字选词键。主键盘带 Shift 时产生 !@# 等字符，由调用方
    // 另行判定（小键盘不受 Shift 影响）。
    static int digitIndex(WPARAM vk) {
        if (vk >= kVkFirstDigit && vk <= kVkLastDigit) {
            return static_cast<int>(vk - kVkFirstDigit);
        }
        if (vk >= kVkNumpad1 && vk <= kVkNumpad9) {
            return static_cast<int>(vk - kVkNumpad1);
        }
        return -1;
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

        // 组词中 Backspace/Delete：修饰键映射与 macOS 对齐（2026-10-31 用户要求：
        // Control↔Ctrl、Option↔Alt、Command↔Win）。两者都消费，具体行为在
        // handleKey 里按「Command/Ctrl/Shift → 全部删除；Alt → 按音节回退；
        // 无修饰 → 逐字符回退」处理。非组词态一律放行给宿主。
        if (isComposing() && (vk == kVkBackspace || vk == kVkDelete)) return true;
        // Ctrl+U 整行清空；Ctrl+Shift+U 是宿主快捷键（mac/Linux 都放行），
        // 所以这里必须要求不带 Shift。
        if (ctrl) return isComposing() && vk == 'U' && !shiftActive();
        if (alt || win) return false;

        // 联想模式：数字（有对应项）/空格（选第一个）/回车/Esc/Backspace
        //（收起）由联想分支消费；- / = 收起联想放行；其余按键退出联想后
        // 按正常流程判定（继续往下走）。
        if (!assoc_.candidates.empty()) {
            if (vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
                vk == kVkBackspace) return true;
            // 联想翻页键：PageUp/PageDown 不判 Shift（与 mac/Linux 一致：
            // 联想态 Shift+PageUp/PageDown 同样翻联想页，2026-10-30 审计 A7）。
            if (vk == kVkPrior || vk == kVkNext) return true;
            if (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus)) return true;
            const int digit = digitIndex(vk);
            if (digit >= 0 && (vk >= kVkNumpad1 || !shiftActive())) {
                return assocPage_ * kPageSize + digit <
                       static_cast<int>(assoc_.candidates.size());
            }
        }

        // Shift+空格只在输入法介入时（组词/联想）才预告吞键。
        // 2026-10-29 修复（输入事件审计 F1）：此前无条件 return true，
        // 任何应用里 Shift+Space 都被拦——游戏里 Shift=冲刺/潜行、Space=跳。
        // 注意：联想态的 Shift+空格由上方联想分支先行消费——实际行为是
        // 「选当前页第一个联想词上屏」，**不**切换全半角（三端实测一致；
        // 2026-10-30 按实际行为修正文档与本节注释）；只有组词态才会走到
        // handleKey 的全半角切换分支。
        if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
            return isComposing() || !assoc_.candidates.empty();
        }

        // 只在需要打字时介入（2026-10-29 用户要求）：未组词状态下，先判
        // 该键是否与组词相关——字母（不带 Shift）或标点才有「开始组词/出
        // 中文标点」的可能，其余键（数字/导航/F键/空格等）立即放行，且绝不
        // 触碰连接状态；内容键才允许尝试 daemon 连接（服务不可达仍放行）。
        if (!isComposing()) {
            const bool relevant = (vk >= kVkFirstLetter && vk <= kVkLastLetter && !shiftActive()) ||
                                  keyPunctChar(vk, shiftActive()) != nullptr;
            if (!relevant) return false;
            if (!ensureDaemonConnected(const_cast<PipeTransport&>(transport_))) {
                return false;
            }
        }

        // Shift+字母（大写英文，含 CapsLock）：放行给宿主，不进组词
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) return !shiftActive();
        // 翻页键：- / = / [ / ] 不带 Shift（Shift 变体 _ + { } 是字符，
        // 与 mac 字符级判定一致放行）；PageUp/PageDown 无论 Shift 都翻页
        //（mac keyCode/Linux keysym 都不看 Shift，2026-10-31 三端对齐）。
        const bool isPageKey =
            (vk == kVkPrior || vk == kVkNext) ||
            (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus ||
                                vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
        if (isPageKey && isComposing()) return hasCandidates();
        // 双拼模式下 ';' 是引擎的组词/解码键（见 handleKey 的 DP 路由），
        // 同样要在这里预告吞键——TSF 两阶段模型里 OnTestKeyDown 预告 FALSE
        // 时 OnKeyDown 可能根本收不到该键（参照上方字母/标点判定）。
        if (!dpScheme_.empty() && isDpSemicolonKey(vk, shiftActive())) return true;
        if (keyPunctChar(vk, shiftActive()) != nullptr) return true;
        if (!isComposing()) return false;
        // 数字选词只在不带 Shift 且本页确有该序号候选时；Shift+数字产生的
        // !@# 等字符与本页没有的序号都放行（与 handleKey 的判定保持一致）。
        // 小键盘 1-9（VK_NUMPAD1-9）同样可选词，且不受 Shift 影响。
        const int digit = digitIndex(vk);
        return vk == kVkBackspace || vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
               (digit >= 0 && (vk >= kVkNumpad1 || !shiftActive()) &&
                digit < static_cast<int>(comp_.candidates.size()));
    }

    bool handleKey(WPARAM vk, ITfContext* pic) {
        if (isModifierKey(vk)) return false;

        // 英文模式：内容键一律放行。**必须在实际处理点判断**，不能只靠
        // OnTestKeyDown 的预告分支——实测 Chrome / conhost / Windows Terminal
        // 连内容键也走 Key 阶段（Test 阶段收不到修饰键，同理收不到这些键），
        // 预告分支根本不执行，于是「提示切到英文、打字仍出拼音候选」。
        // 实机日志：16:13:42 shift toggle enMode=1 之后 16:13:43 仍在
        // candidate shown（2026-11-05）。
        if (currentEnglish()) {
            clearAssociate();
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);  // 切换瞬间没收干净的残留，这里兜底清理
            }
            return false;  // 放行给宿主
        }

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
                    const chaoset::Candidate pick = assoc_.candidates[assocPage_ * kPageSize];
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
            // 联想翻页键：- / =、[ / ]、PageUp / PageDown——与组词态翻页
            // 键集合一致（2026-10-29 三端一致性审查）。此前只认 - / =，
            // 其余键落到「其余按键退出联想」：面板收起而按键透传给宿主
            // （文档跟着滚动），与 macOS/Linux 行为各不相同。
            const bool assocPageKey =
                vk == kVkPrior || vk == kVkNext ||
                (!shift && (vk == kVkOemMinus || vk == kVkOemPlus ||
                            vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
            if (assocPageKey) {
                const bool next = vk == kVkOemPlus || vk == kVkOemCloseBrackets ||
                                  vk == kVkNext;
                flipAssociate(next);
                return true;
            }
            const int assocDigit = digitIndex(vk);
            if (assocDigit >= 0 && (vk >= kVkNumpad1 || !shift)) {
                // 全局序号 = 当前页 × 每页条数 + 页内序号（候选窗展示的是切片）
                const int index = assocPage_ * kPageSize + assocDigit;
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

        // 组词中 Backspace/Delete：修饰键映射（2026-10-31 用户要求：
        // Control↔Ctrl、Option↔Alt、Command↔Win）。优先级与 macOS 实现一致：
        // Command/Ctrl/Shift → 全部删除（含未落盘确认段，与 Esc 同语义）；
        // Alt → 按音节回退（engine.backspaceWord）；无修饰 → 下方逐字符回退。
        // 非组词态一律放行给宿主。
        //
        // 注意：Win/Super 组合可能先被系统外壳拦截（TSF 未必收得到），
        // 输入法只在收到按键时按 Command 语义处理——真机以实际到达为准。
        if (isComposing() && (vk == kVkBackspace || vk == kVkDelete)) {
            const bool shift = (GetKeyState(VK_SHIFT) & 0x8000) != 0;
            if (win || ctrl || shift) {
                resetComposition();
                comp_ = chaoset::Comp();
                confirmedPreedit_.clear();
                endComposition();
                return true;
            }
            if (alt) {
                comp_ = session_.backspaceWord();
                if (!comp_.ok) {
                    diagDaemonRequestFailure(transport_, "backspaceWord");
                    flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                    return true;
                }
                syncDpScheme(comp_);
                if (comp_.input.empty()) {
                    if (!confirmedPreedit_.empty()) {
                        // 剩余拼音删空：已确认段立即落盘收尾（与普通 Backspace 同路径）
                        comp_ = chaoset::Comp();
                        const std::string all = confirmedPreedit_;
                        confirmedPreedit_.clear();
                        commit(pic, all);
                    } else {
                        endComposition();
                    }
                } else {
                    render(pic);
                }
                return true;
            }
        }

        // Alt 或 Win 快捷键：先收尾未决组词，然后放行原键给宿主。
        // 收尾走 flushOrCancel 而不是 endComposition：用户已确认的段不能
        // 随快捷键透传消失（与 macOS passThrough → flushConfirmed 同语义，
        // 2026-10-30 审计 P1-3）。
        if (alt || win) {
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            return false;
        }

        // Ctrl 组合键：
        if (ctrl) {
            // Ctrl+U：类终端整行清空（对齐 macOS 壳，组词中消费并全部删除）；
            // Ctrl+Shift+U 是宿主快捷键（mac/Linux 都放行），不在此列。
            if (vk == 'U' && isComposing() && !shiftActive()) {
                resetComposition();
                endComposition();
                comp_ = chaoset::Comp();
                return true;
            }
            // 其余 Ctrl 组合键（如 Ctrl+A/C/V/Z）：若处于组词中，先收尾再放行
            // 原键给宿主——已确认段随透传落盘（用户确认过的字不能丢）
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            return false;
        }

        // 字母：追加组词。Shift+字母（大写英文，含 CapsLock）不进组词：
        // 放行原字符，组词中先清空——此前无条件小写化，Shift+A 会变成 a
        // 混进拼音（mac 壳 isCompositionChar 只认小写，此处对齐）。
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) {
            if (shiftActive()) {
                // 大写英文不进组词：放行原字符，组词先收尾（已确认段随透传落盘）
                if (isComposing() || !confirmedPreedit_.empty()) {
                    flushOrCancel(pic);
                }
                return false;
            }
            std::string ch(1, static_cast<char>('a' + (vk - kVkFirstLetter)));
            comp_ = session_.input(ch, halfWidth_);
            if (!comp_.ok) {
                // 偶发断开（连接已不在且不在失败冷却）：重连后重试一次，绝不
                // 轻易泄露首字母到宿主程序。此前这里无条件 closeConn 清零
                // 失败冷却——daemon 半死（响应超时）时每键都会重新全量重试，
                // 宿主 UI 线程每键被阻塞 ~200ms；现在尊重 failConn 冷却，
                // 冷却期内直接降级放行（2026-10-27 审查发现）。
                if (!transport_.isConnected() && ensureDaemonConnected(transport_)) {
                    comp_ = session_.input(ch, halfWidth_);
                }
            }
            if (!comp_.ok) {
                // daemon 确实不可达：收尾（已确认段落盘，不因 daemon 故障丢字）
                // 并放行原键，保证宿主应用不卡死
                diagDaemonRequestFailure(transport_, "input");
                flushOrCancel(pic);
                return false;
            }
            syncDpScheme(comp_);  // 双拼方案名（';' 路由依据，见 syncDpScheme）
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
        // 空组词放行原键/出标点）。- / = / [ / ] 的 Shift 变体是字符不翻页；
        // PageUp/PageDown 无论 Shift 都翻页（对齐 mac/Linux）。
        const bool isPageKey =
            (vk == kVkPrior || vk == kVkNext) ||
            (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus ||
                                vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
        if (isPageKey && isComposing() && hasCandidates()) {
            bool isPrev = (vk == kVkOemMinus || vk == kVkOemOpenBrackets || vk == kVkPrior);
            comp_ = isPrev ? session_.prevPage() : session_.nextPage();
            if (!comp_.ok) {
                diagDaemonRequestFailure(transport_, "page");
                flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                return true;
            }
            syncDpScheme(comp_);
            render(pic);
            return true;
        }

        // 双拼模式的 ';'（配合引擎契约）：双拼时 ';' 不是标点键，而是引擎的
        // 组词/解码键——dpPending 非 0 时与字母一样组成 pair 解码，为 0 时
        // 按 punct(";") 语义（组词中先上屏首选+标点）。必须路由到 input 而
        // 不是 punct，引擎才能在双拼键位状态机里看到它。Shift+; 产生的
        // ':' 不在此列（仍走下方标点分支），与 mac 的字符级判定一致。
        if (!dpScheme_.empty() && isDpSemicolonKey(vk, shiftActive())) {
            comp_ = session_.input(";", halfWidth_);
            if (!comp_.ok) {
                // 与字母/撇号分支同一重连策略：尊重连接失败冷却
                if (!transport_.isConnected() && ensureDaemonConnected(transport_)) {
                    comp_ = session_.input(";", halfWidth_);
                }
            }
            if (!comp_.ok) {
                diagDaemonRequestFailure(transport_, "input;");
                flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                return false;       // 绝不吞键：daemon 不可达时让 ; 原样进文档
            }
            syncDpScheme(comp_);
            if (!comp_.committed.empty()) {
                // 引擎已把「上屏文本」（组词中首选 + 分号 / 空组词的分号）
                // 放进 committed：壳侧已确认段必须先拼接再一次性落盘——
                // 用户确认过的字不能丢（与 mac 的 confirmedText + committed
                // 同语义，2026-10-30 三端接口契约）。
                const std::string all = confirmedPreedit_ + comp_.committed;
                confirmedPreedit_.clear();
                commit(pic, all);
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
                comp_ = session_.input("'", halfWidth_);
                if (!comp_.ok) {
                    // 只在偶发断开时重连重试（尊重失败冷却，见字母分支注释）
                    if (!transport_.isConnected() && ensureDaemonConnected(transport_)) {
                        comp_ = session_.input("'", halfWidth_);
                    }
                }
                if (!comp_.ok) {
                    diagDaemonRequestFailure(transport_, "input'");
                    flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                    return false;
                }
                syncDpScheme(comp_);
                render(pic);
                return true;
            }
            const bool composing = isComposing();
            std::string committed;
            if (session_.punct(key, halfWidth_, &committed)) {
                // 标点收尾：已确认段 + 首选候选 + 标点一次落盘
                //（上屏失败（上下文不可插入文本）且没有未决拼音时放行原键，
                // 避免中文标点凭空消失；有未决拼音时仍需消费，保持组词状态机完整）
                const std::string all = confirmedPreedit_ + committed;
                confirmedPreedit_.clear();
                if (commit(pic, all) || composing) return true;
                return false;
            }
            // daemon 不可达：收尾（已确认段落盘）后放行原键，不卡宿主应用
            if (composing || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            return false;
        }

        // Shift+空格：切换全角/半角标点模式。放在 isComposing 守卫**之前**：
        // 用户常在开始打字前先切模式，此前放在守卫之后导致空组词时永远
        // 切不动（mac 壳在组词判断之前处理，此处对齐；wouldHandle 已对
        // Shift+空格无条件返回 TRUE，两条路径此前互相矛盾）。
        // 联想态到不了这里：上方联想分支已把空格当「选第一个联想词」消费
        //（三端实测一致，2026-10-30 文档修正），因此本分支实际只处理组词态。
        if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
            // 无组词/无联想时放行给宿主：输入法没有理由拦截应用自己的
            // Shift+Space（2026-10-29 输入事件审计 F1）。
            if (!isComposing() && assoc_.candidates.empty()) return false;
            // 全/半角开关归壳本地持有（2026-10-29 起）：快捷键切换不再与
            // daemon 往返，状态随 punct 请求下发（daemon 只按状态产出文本）
            halfWidth_ = !halfWidth_;
            showToast(halfWidth_ ? L"半角标点" : L"全角标点");
            return true;
        }

        if (!isComposing()) return false;

        // 普通 Backspace / ForwardDelete（无修饰）：逐字符回退。
        // Delete 与 Backspace 同语义（对齐 macOS 组词中两者都删拼音）；
        // 带修饰的组合已在上面按映射处理。
        if (vk == kVkBackspace || vk == kVkDelete) {
            comp_ = session_.backspace();
            if (!comp_.ok) {
                diagDaemonRequestFailure(transport_, "backspace");
                flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                return true;
            }
            syncDpScheme(comp_);
            if (comp_.input.empty()) {
                if (!confirmedPreedit_.empty()) {
                    // 剩余拼音删空：已确认段立即落盘收尾——不能继续挂在
                    // 合成里变成「无拼音的孤合成」（对齐 macOS 壳）
                    comp_ = chaoset::Comp();
                    const std::string all = confirmedPreedit_;
                    confirmedPreedit_.clear();
                    commit(pic, all);
                } else {
                    endComposition();
                }
            }
            else render(pic);
            return true;
        }

        if (vk == kVkEscape) {
            // Esc：直接全部删除（清空全部拼音）。含未落盘的已确认段一并取消
            //——与 macOS 壳/系统拼音语义一致：合成内已转换但未上屏的内容
            // 随整条组词一起丢弃。
            resetComposition();
            comp_ = chaoset::Comp();
            confirmedPreedit_.clear();
            endComposition();
            return true;
        }

        // 数字选词：只认不带 Shift 的 1-9，且按**本页实际候选数**判定——
        // 页面容量是 6，越界数字不消费、落到末尾兜底（清组词后放行原键）。
        // 此前 select 失败仍 return true：数字凭空消失，既不选词也不进文档
        // （mac 壳 2026-10-26 已修，此处对齐）。带 Shift 的数字（!@# 等字符）
        // 不属于选词，同样落到末尾放行。
        const int composeDigit = digitIndex(vk);
        if (composeDigit >= 0 && (vk >= kVkNumpad1 || !shiftActive())) {
            int index = composeDigit;
            if (index < static_cast<int>(comp_.candidates.size())) {
                // 上屏候选的音节串必须在 selectPrefix 之前取：成功后
                // comp_ 会被替换成剩余组词态（或清空），候选表丢了。
                const std::string pickedSyllables = comp_.candidates[index].syllables;
                // 渐进上屏：只消耗候选音节对应的拼音前缀，剩余回灌继续组词
                //（2026-10-29 用户反馈「选一个候选清空整个拼音」不合适）。
                // 单合成模型（见 confirmedPreedit_ 注释）：确认段并入当前
                // 合成，不再中途结束/重建合成——部分 App 对合成重建的选区
                // 记账有缺陷，会把刚上屏的段覆盖丢（用户实测选「今」丢字）。
                chaoset::Comp rest;
                if (session_.selectPrefix(index, &rest) && !rest.committed.empty()) {
                    syncDpScheme(rest);
                    confirmedPreedit_ += rest.committed;
                    comp_ = rest;
                    if (!comp_.input.empty()) {
                        render(pic);
                    } else {
                        // 剩余耗尽：整串一次落盘，与整句上屏同收尾（进入联想）。
                        // 音节基准传候选自身的撇号音节串（2026-10-29 修复：
                        // 不能把上屏词文本当音节传，单字词会切错位）。
                        comp_ = chaoset::Comp();
                        const std::string all = confirmedPreedit_;
                        confirmedPreedit_.clear();
                        commit(pic, all);
                        enterAssociate(pickedSyllables);
                    }
                } else {
                    // daemon 不可达/越界：回退整句上屏（现状语义）
                    std::string committed;
                    if (session_.select(index, &committed)) {
                        const std::string all = confirmedPreedit_ + committed;
                        confirmedPreedit_.clear();
                        commit(pic, all);
                    }
                }
                return true;
            }
        }

        if (vk == kVkReturn && isComposing()) {
            // 回车语义：已确认段按确认文本、剩余拼音按原始串一起上屏
            //（与 macOS 壳一致：已转换部分保留转换结果）
            std::string raw = confirmedPreedit_ + comp_.input;
            confirmedPreedit_.clear();
            resetComposition();
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
                    syncDpScheme(rest);
                    // 单合成渐进（见 confirmedPreedit_ 注释，与数字选词一致）
                    confirmedPreedit_ += rest.committed;
                    comp_ = rest;
                    if (!comp_.input.empty()) {
                        render(pic);
                    } else {
                        comp_ = chaoset::Comp();
                        const std::string all = confirmedPreedit_;
                        confirmedPreedit_.clear();
                        commit(pic, all);
                        enterAssociate(pickedSyllables);
                    }
                    return true;
                }
                std::string committed;
                if (session_.select(0, &committed)) {
                    const std::string all = confirmedPreedit_ + committed;
                    confirmedPreedit_.clear();
                    commit(pic, all);
                }
                return true;
            }
            // 残缺组词顶屏：已确认段按确认文本、剩余拼音按原始串一起上屏
            //（与上方回车分支一致：用户确认过的字不能丢，2026-10-30
            // 审计 P1-3）。先取出拼接再清缓冲，顺序与 flush 型收尾一致。
            std::string raw = confirmedPreedit_ + comp_.input;
            confirmedPreedit_.clear();
            resetComposition();
            commit(pic, raw);
            return true;
        }

        // 不可处理的按键（方向键 / F 键 / 数字 0 / Shift+数字的 @# 等字符）：
        // 组词中先收尾再放行，避免预编辑与实文脱节（对齐 macOS 壳 passThrough；
        // 已确认段随透传落盘，用户确认过的字不能丢）
        if (isComposing() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
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
        resetComposition();
        comp_ = chaoset::Comp();
        confirmedPreedit_.clear(); // 上下文已不可写：缓冲一并放弃，绝不串进下轮组词
        candidateWnd_.hide();
        // 走到这里时合成对象必然已不存在（未建出或 SetText 失败已当场结束），
        // 上下文记录一并清掉，避免过期引用跨越焦点切换存活。
        releaseCompositionContext();
        return false;
    }

    // 把引擎候选状态渲染为预编辑合成与候选悬浮窗
    void render(ITfContext* pic) {
        g_lastOp = kOpRender;
        // 防御：非激活时绝不渲染任何 UI（候选窗/预编辑都不该出现在别人的应用里）。
        // 按键入口已有 bailOutIfInactive 闸门，这里再兜一层——render 还有别的
        // 调用点（联想、翻页、候选删除后的刷新），不能指望每个都先过闸门。
        if (!isActiveTip()) {
            candidateWnd_.hide();
            toast_.hide();
            return;
        }
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
        caretFromMouse_ = false;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyPreedit(c, ec);
        });
        const HRESULT hr = es.run(tm_, clientId_);
        es.Release();
        if (FAILED(hr)) diagTsfFail("RequestEditSession(render)", hr);

        // 缓存承载本合成的宿主窗口（供前台事件回调做无 COM 的比较）。
        composeHostWnd_ = contextHostWindow(ctx);

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
                caretFromMouse_ = false;
            } else {
                // 鼠标兜底：**锚点要下移一行**。
                //
                // 鼠标通常正停在用户正在输入的那一行上，而候选窗是从锚点
                // 再往下 6pt 起画的——直接用鼠标坐标会把候选窗画在输入行
                // 上面，正好挡住正在打的字（Firefox 装到任务栏后的独立窗口
                // 实测「候选框遮挡输入」，2026-11-05）。下移一行后候选窗
                // 落在输入行下方，与有真实光标时的观感一致。
                GetCursorPos(&caretPos_);
                caretLineHeight_ = 20;
                caretPos_.y += caretLineHeight_;
                caretFromMouse_ = true;
            }
        }
        // 候选窗 owner = 宿主窗口（2026-10-30 开始菜单排障）。
        // ITfContextView::GetWnd 返回承载本上下文的宿主 HWND；拿不到时退回
        // 前台窗口。owner 关系让候选窗恒在其宿主之上，可跨 Windows 11
        // 开始菜单/搜索的沉浸式 z 序 band——无主 topmost 窗口会被压住。
        // owner 用**当前前台窗口**（且限定本进程），不要用 hostWindowOf(ctx)——
        // 后者可能是另一个窗口的上下文，带 owner 显示时会把它抬到前面，
        // 表现为「候选窗一出现就把别的窗口顶上来」（2026-11-05 用户反馈）。
        candidateWnd_.setOwner(popupOwner());
        candidateWnd_.show(g_hInst, caretPos_, caretLineHeight_, comp_, this);
    }

    // 取上下文宿主窗口（**只在 TSF 线程调用**）：与 hostWindowOf 的区别是
    // 不回退到前台窗口，供前台事件回调缓存后做纯 Win32 比较。
    static HWND contextHostWindow(ITfContext* ctx) {
        if (ctx == nullptr) return nullptr;
        ITfContextView* view = nullptr;
        if (FAILED(ctx->GetActiveView(&view)) || view == nullptr) return nullptr;
        HWND wnd = nullptr;
        const HRESULT hr = view->GetWnd(&wnd);
        view->Release();
        if (FAILED(hr) || wnd == nullptr || !IsWindow(wnd)) return nullptr;
        return wnd;
    }

    // 取上下文宿主窗口：优先 ITfContextView::GetWnd（TSF 标准途径，
    // 直接给出承载该文档的 HWND），失败退回前台窗口。
    static HWND hostWindowOf(ITfContext* ctx) {
        if (ctx != nullptr) {
            ITfContextView* view = nullptr;
            if (SUCCEEDED(ctx->GetActiveView(&view)) && view != nullptr) {
                HWND wnd = nullptr;
                const HRESULT hr = view->GetWnd(&wnd);
                view->Release();
                if (SUCCEEDED(hr) && wnd != nullptr && IsWindow(wnd)) return wnd;
            }
        }
        return GetForegroundWindow();
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
            caretFromMouse_ = false;
        }
        view->Release();
    }

    void applyPreedit(ITfContext* ctx, TfEditCookie ec) {
        g_lastOp = kOpPreedit;
        // 预编辑 = 已确认段（渐进上屏的已选字）+ 剩余拼音：二者同属**同一条
        // 合成**，整条组词结束才落盘（见 confirmedPreedit_ 注释）。壳对文档
        // 只做两类操作——同一合成内 SetText 更新（与逐键输入同原语）与
        // 落盘提交（见 applyCommit），不存在「中途结束再重建合成」的易碎操作。
        const std::string combinedPreedit = confirmedPreedit_ + comp_.preedit;
        // 「预编辑是否真的写进了文档」：字符串为空视作已写（无需合成）；
        // SetText 失败时保持 false，字母分支据此判定该上下文不可用并放行。
        preeditWritten_ = combinedPreedit.empty();
        const std::wstring wide = toWide(combinedPreedit);
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
        // 提交即整条组词结束：确认段缓冲必须清空（调用方已先行取出拼接，
        // 这里幂等兜底，防止异常路径把旧段串进下一轮组词的预编辑）。
        confirmedPreedit_.clear();
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

    // 缓存引擎的双拼方案名（IPC Composition.shuangpin）。引擎契约：启用
    // 双拼时**每次 Compose 都回填**方案名；方案在 daemon 生命周期内固定，
    // 壳只需要任意一次响应就能判定 ';' 路由（见 handleKey），因此只在
    // 非空时覆盖，空值不清除（兼容未实现该字段的旧 daemon）。
    void syncDpScheme(const chaoset::Comp& comp) {
        if (!comp.shuangpin.empty()) dpScheme_ = comp.shuangpin;
    }

    // 结束组词并重置引擎会话。reset 响应同样是 Composition（也带 shuangpin），
    // 这里统一缓存方案名——所有调用点替换 session_.reset() 以免漏掉。
    void resetSession() { syncDpScheme(session_.reset()); }

    // 只丢弃当前组词、保留学习上下文（prev/history）的收尾。
    //
    // 用途判定（2026-11-05）：**用户还会继续打字**的场景一律走这里——
    // 切中英模式、退格删空、Esc 取消、回车/空格顶屏、宿主拒建合成兜底、
    // 传给宿主后清本地状态。这些情况下用户只是「打断了这一轮组词」，
    // 上一轮上屏的 prev/bigram 依然有效；用 reset 会让引擎忘记刚打的字，
    // 紧接着的候选失去上下文加成（实测：学过「我们→时」后输入 shi，
    // 带 prev 首候选是「时」，清掉后变成「是」）。
    //
    // reset 只保留给**会话真的结束**的场景：失焦（OnSetFocus(FALSE)）、
    // TIP 停用（Deactivate）、合成被宿主终止（OnCompositionTerminated）。
    void resetComposition() { syncDpScheme(session_.resetComposition()); }

    // 透传/故障型收尾（对齐 macOS 壳 passThrough）：已确认段先落盘——用户
    // 确认过的字不能随透传（Ctrl+C 等）或 daemon 故障消失；纯拼音维持
    // 「清空」旧语义。显式取消（Esc / 全部删除）不走这里，直接 endComposition。
    //
    // 用 resetComposition（不是 reset）：这条路径的全部调用方（切中英、
    // 方向键/功能键透传、daemon 故障降级、翻页失败）都意味着**用户还会
    // 继续输入**，不该把刚上屏的 prev/bigram 一起忘掉（2026-11-05）。
    void flushOrCancel(ITfContext* pic) {
        resetComposition();
        comp_ = chaoset::Comp();
        if (!confirmedPreedit_.empty()) {
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(pic, all);
        } else {
            endComposition();
        }
    }

    // 取消/清理当前合成：必须经真实编辑会话拿到有效 cookie。旧实现用
    // cookie 0 在编辑会话外调用 SetText/EndComposition，TSF 只会返回失败：
    // 预编辑文本留在文档里、合成对象也泄漏（2026-10-29 审查）。
    void endComposition() {
        candidateWnd_.hide();
        // 取消整条组词：未落盘的已确认段一并丢弃（与 macOS 壳 Esc 语义一致；
        // 幂等兜底——落盘型收尾走 commit() 且已先行取出拼接）。
        confirmedPreedit_.clear();
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

    // 短暂提示（半/全角、删除候选结果）：放光标行底边下方，与候选窗同锚点。
    //
    // **真实光标未知时不显示**，除非调用方明确允许鼠标锚点（删除候选是鼠标
    // 触发的操作，提示落在鼠标附近符合预期）。2026-11-05 用户反馈「中英文
    // 提示在光标和鼠标两处都出现」——模式提示已改由单例托盘承担；这里也
    // 不能再把鼠标兜底坐标当成光标，否则提示又会跟着鼠标跑。
    void showToast(const wchar_t* text, bool allowMouseAnchor = false) {
        // 非激活一律不弹任何浮窗（2026-11-06）：提示条虽带 WS_EX_TRANSPARENT
        // 不吃点击，但 TOPMOST 浮窗在别人的应用上突然出现同样是干扰。
        if (!isActiveTip()) return;
        if (!allowMouseAnchor) {
            if (!caretFound_ || caretFromMouse_) return;
        }
        POINT anchor = caretPos_;
        if (anchor.x == 0 && anchor.y == 0) return;
        // 与候选窗同锚点：光标行底边下方 6pt（mac showToast 复用
        // candidateOrigin，候选窗与提示条位置一致）。2026-10-30 审计 A12：
        // 此前是「光标行底 + 整行高 + 6px」，比候选窗低约一行且随 DPI 偏
        // 0.75 倍；工作区钳制在 ModeToast::show 内做。
        const int gap = MulDiv(6, systemDpi(), 72);
        toast_.show(g_hInst, text, anchor.x, anchor.y + gap);
    }
    POINT caretPos_{0, 0};
    int caretLineHeight_ = 20;
    // 本次渲染是否拿到了**真实** GDI 光标矩形（caretPos_ 可信）。
    bool caretFound_ = false;
    // caretPos_ 是否来自鼠标兜底（宿主没有真实光标时）。提示条据此拒绝
    // 在鼠标位置显示，避免「提示跟着鼠标跑」（2026-11-05 用户反馈）。
    bool caretFromMouse_ = false;

    PipeTransport transport_;
    chaoset::Session session_{transport_};
    chaoset::Comp comp_;
    // 渐进上屏的「已确认段」缓冲（2026-10-30 兼容性整改，与 macOS 壳
    // confirmedText 同语义）：SelectPrefix 返回的已确认文本**不再立即
    // 结束合成写盘**，而是并入当前合成一起展示（applyPreedit 写
    // confirmedPreedit_ + comp_.preedit），直到整条组词结束才一次落盘。
    //
    // 为什么（用户实测：长串组词中选 1（今），部分 App 里「今」丢失、
    // 剩余拼音卡死）：旧实现每次渐进选择都「EndComposition + 立刻
    // StartComposition」——对正确跟踪选区的 App 没问题，但对合成结束/
    // 重建时选区记账有缺陷的 App（部分 Chromium/Java/旧 Win32 宿主），
    // 新合成的锚点可能落回旧文本处，随后的 SetText 直接覆盖掉刚上屏的
    // 「今」，只剩剩余拼音。改单合成模型后，渐进上屏与逐键输入使用完全
    // 相同的原语（同一条合成内 SetText），不再中途结束/重建合成；落盘只剩
    // 「整条组词结束」一个时机，与微软拼音「合成内已转换段」的行为一致。
    // 清空时机：commit（落盘）、endComposition（取消）、OnCompositionTerminated
    // （宿主终止，文本已随合成留在文档）、activateServer 之外的一切
    // 会话结束路径——绝不能串进下一轮组词。
    std::string confirmedPreedit_;
    // 引擎双拼方案名（zrm/mspy/flypy；空 = 未启用）。只用于按键路由：
    // 双拼时 ';' 是引擎的组词/解码键，必须走 input 而不是 punct。
    // 来源见 syncDpScheme（IPC Composition.shuangpin）。
    std::string dpScheme_;
    bool halfWidth_ = false; // 全/半角标点开关（壳本地持有，随 punct 请求下发）
    // 词组联想（对齐 macOS 壳）：candidates 非空即联想中。选词上屏后按
    // 上屏词音节查续接词组继续展示，数字键可直接连选。
    chaoset::Comp assoc_;
    int assocPage_ = 0; // 联想当前页（0 起，本地分页每页 6 条）
    // 联想来源音节（撇号分隔）：右键删除联想候选后按同一来源重新查询续接
    // 词组，保证删除刷新不改变联想链（与 macOS associateSylls 同语义）。
    std::string assocSylls_;
    ITfComposition* composition_ = nullptr;
    // 合成所属宿主窗口（render 时缓存；前台事件回调只读，避免回调里碰 TSF）。
    HWND composeHostWnd_ = nullptr;
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
        composeHostWnd_ = nullptr;
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

    // 面板当前是否在展示联想候选（候选窗箭头点击据此选择翻哪一组页）。
    // 2026-10-29：联想与组词共用同一个候选窗，但分页数据源不同
    // （assoc_ vs comp_），此前箭头点击无条件翻组词页。
    bool isAssociatePanel() const { return !assoc_.candidates.empty(); }

    // 联想翻页入口（供候选窗箭头调用；实现见下方 flipAssociate）。
    void flipAssociateFromPanel(bool next);

    // 设置窗关闭联想后立即收起（候选窗收到跨进程广播后转发；macOS 同语义）。
    void clearAssociateFromSettings();

private:

    void enterAssociate(const std::string& syllables) {
        if (!RegGetAssociate()) return; // 设置关闭词组联想
        assoc_ = chaoset::Comp();
        assocSylls_.clear();
        std::vector<std::string> sylls = splitSyllables(syllables);
        if (sylls.empty()) return;
        chaoset::Comp assoc;
        if (!session_.associate(sylls, &assoc) || assoc.candidates.empty()) return;
        syncDpScheme(assoc);
        assoc_ = std::move(assoc);
        assocSylls_ = syllables;
        assocPage_ = 0;
        showAssociatePage();
    }

    // 展示联想当前页（本地分页：全量候选在 assoc_，每页 pageSize 条）。
    void showAssociatePage() {
        const int per = kPageSize; // 与 engine.PageSize 一致（数字键 1-6 指位）
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
        const int pageCount = (total + kPageSize - 1) / kPageSize;
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
        assocSylls_.clear();
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
            // 面板当前展示的可能是**联想**分页（showAssociatePage 传进来的是
            // 联想页切片），此时必须翻联想页。2026-10-29 修复：此前无条件走
            // flipPage（翻组词页）——而联想时组词态 comp_ 是空的，flipPage
            // 首行 hasCandidates() 直接 return，**点箭头完全没反应**
            // （macOS 面板箭头在联想态走 associateFlip、Linux 同理）。
            if (tip_->isAssociatePanel()) {
                tip_->flipAssociateFromPanel(dirDown);
            } else {
                tip_->flipPage(dirDown);
            }
        }
    }
}

void CandidateWindow::onRButtonUp(int x, int y) {
    if (tip_ == nullptr) return;
    POINT pt{ x, y };
    const int index = [&]() {
        for (const auto& it : items_) {
            if (PtInRect(&it.itemRect, pt)) return it.index;
        }
        return -1;
    }();
    if (index < 0) return;
    POINT screen{ pt.x, pt.y };
    ClientToScreen(hwnd_, &screen);
    HMENU menu = CreatePopupMenu();
    if (menu == nullptr) return;
    AppendMenuW(menu, MF_STRING, 1, L"删除该词");
    // MSDN KB135788：TrackPopupMenu 前 SetForegroundWindow，否则点菜单外
    // 菜单不消失。候选窗是 WS_EX_NOACTIVATE（不抢焦点），SetForegroundWindow
    // 可能被系统忽略，返回后补发 WM_NULL 让菜单循环正常退出。
    SetForegroundWindow(hwnd_);
    const UINT cmd = TrackPopupMenu(
        menu, TPM_RETURNCMD | TPM_NONOTIFY | TPM_LEFTALIGN | TPM_TOPALIGN,
        screen.x, screen.y, 0, hwnd_, nullptr);
    DestroyMenu(menu);
    PostMessageW(hwnd_, WM_NULL, 0, 0);
    // TPM_RETURNCMD：命令在返回后同步执行；候选在菜单跟踪期间不刷新，
    // index 仍是当前页切片内的序号（与 onLButtonDown 的点击语义一致）。
    if (cmd == 1) tip_->deleteCandidateByIndex(index);
}

// 联想翻页的候选窗入口（public 转发，实现在 ChaosetTIP 私有区）。
void ChaosetTIP::flipAssociateFromPanel(bool next) { flipAssociate(next); }

// 设置窗关闭联想：立即收起当前联想面板（候选窗收到跨进程广播后转发；
// macOS 设置窗 post 通知后 Controller 也是立即收起）。
void ChaosetTIP::clearAssociateFromSettings() { clearAssociate(); }

// 候选窗收到「联想开关变化」广播：关闭时立即收起联想；只查注册表，
// 不信任消息参数（消息 id 由字符串注册，跨进程一致）。
void CandidateWindow::onAssociateChanged() {
    if (RegGetAssociate()) return;
    if (tip_ != nullptr) tip_->clearAssociateFromSettings();
}

// ---- 设置窗口（三端一致：macOS SettingsWindow.swift 同构）----
//
// 候选栏齿轮 → 独立设置窗口。此前这里是 TrackPopupMenu 的单个勾选项，
// 用户要求与 macOS 一致的专门设置窗口。布局与 macOS 逐项对应：标题
// 「Chaoset 设置」/ 分组「输入行为」/「词组联想」开关 / 说明文字 /
// 底部「更多设置将在后续版本提供」。开关即时写 HKCU\Software\Chaoset，
// **不访问 TIP**（设置窗可活过 TIP，见 SettingsWndProc 的 UAF 注释）。
// 窗口单例：已打开则前置。
// 设置窗口视觉与 macOS SettingsWindow 对齐（2026-10-30 三端一致性整改 A9）：
// 分组标题 13pt Bold / 标签 13pt / 说明文字 11pt（此前全部 9pt——明显小于
// mac 的 13/11pt）。窗口客户区 380×190pt（mac contentRect 同尺寸），控件
// 坐标统一 pt 口径。
//
// 2026-10-31：新增「学习数据」区（统计 / 清空 / 导出）——普通用户不再需要
// 命令行（`chaoset-userdict` 降级为高级/排障兜底）。设置窗口不持有 TIP 指针，
// 数据管理直接用独立的 PipeTransport（命名管道）+ core::Session 连 daemon，与输入法
// 主连接互不影响（daemon 支持多连接，每个连接独立组词会话）。
//
// 2026-10-31 视觉对齐（编译级，需 Windows 真机验收）：
// - 分组标题改为加粗 Static（与 macOS SettingsWindow 的 bold label 同构）；
//   不再用 BS_GROUPBOX——Win32 分组框不跟随系统深色，深色下会留下浅色边框。
// - 深浅色跟随系统：DWM 深色标题栏 + 自绘背景/文字/复选框/按钮；
//   WM_SETTINGCHANGE / WM_THEMECHANGED 立即重绘。
// - per-monitor DPI：按设置窗口所在显示器 DPI 布局，WM_DPICHANGED 重排。
static HWND g_settingsWnd = nullptr;        // 单例句柄
static int g_settingsDpi = 96;              // 设置窗所在显示器 DPI（per-monitor）
static HFONT g_settingsFont = nullptr;      // 正文/标签 13pt
static HFONT g_settingsFontBold = nullptr;  // 分组标题 13pt Bold
static HFONT g_settingsFontHint = nullptr;  // 说明文字 11pt
static HBRUSH g_settingsBgBrush = nullptr;  // 背景刷（主题切换时重建）
static bool g_settingsDark = false;         // 当前系统是否深色
static bool g_settingsAssociate = true;     // 词组联想开关状态（自绘复选框）

// ---- 设置窗布局：左侧导航 + 右侧分组卡片 + 滚动（2026-11-06 重构）----
//
// 参照微信输入法设置窗的形态：左边是固定宽度的导航（输入/词库/关于），
// 右边是按「卡片」分组的内容区，内容超出一页时可滚动。原来的实现是
// 一张固定 pt 坐标表（380×490pt，12 个控件一个页面），加第三组控件就得
// 整体挪坐标，且没有卡片/分组视觉。
//
// 关键取舍：**导航与卡片背景是自绘的**（Win32 标准控件不跟随系统深色，
// 深色下会露浅色边框），内容行里的交互控件继续用 BS_OWNERDRAW 子窗口，
// 这样既保留无障碍/键盘 Tab，又有统一外观。
enum class SettingsPage { Input = 0, Dictionary = 1, About = 2 };

static constexpr int kNavWidth = 132;      // 左侧导航宽度（pt）
static constexpr int kContentPad = 20;     // 右侧内容区外边距（pt）
static constexpr int kCardPad = 16;        // 卡片内边距（pt）
static constexpr int kCardGap = 14;        // 卡片间距（pt）
static constexpr int kCardRadius = 10;     // 卡片圆角（pt）
static constexpr int kRowHeight = 34;      // 卡片内单行高度（pt）
static constexpr int kWinW = 640;          // 客户区宽度（pt）
static constexpr int kWinH = 560;          // 客户区高度（pt）
static constexpr int kFooterH = 34;        // 页脚（版本号）高度（pt）

static SettingsPage g_settingsPage = SettingsPage::Input;
static int g_settingsScroll = 0;           // 内容区滚动偏移（pt）

// 当前页可用的内容区尺寸（已扣除导航/边距/页脚）。
static RECT settingsContentRect() {
    RECT r{};
    r.left = kNavWidth + kContentPad;
    r.top = kContentPad;
    r.right = kWinW - kContentPad;
    r.bottom = kWinH - kFooterH;
    return r;
}

// 本页内容总高度（用于决定滚动条范围）。按页手工声明——页内容固定，
// 不需要通用布局树；保持「一眼能算出高度」比引入布局框架更可控。
static int settingsPageContentHeight(SettingsPage page) {
    switch (page) {
        case SettingsPage::Input:
            return 300;
        case SettingsPage::Dictionary:
            return 340;
        case SettingsPage::About:
            return 220;
    }
    return 200;
}

static int settingsMaxScroll() {
    const RECT c = settingsContentRect();
    const int visible = c.bottom - c.top;
    const int total = settingsPageContentHeight(g_settingsPage);
    const int maxScroll = total - visible;
    return maxScroll > 0 ? maxScroll : 0;
}

static void settingsClampScroll() {
    const int maxScroll = settingsMaxScroll();
    if (g_settingsScroll > maxScroll) g_settingsScroll = maxScroll;
    if (g_settingsScroll < 0) g_settingsScroll = 0;
}

struct SettingsColors {
    COLORREF bg;
    COLORREF text;
    COLORREF secondary;
    COLORREF accent;
    COLORREF border;
};

// 深浅色与 macOS SettingsWindow 的语义色对应（label / secondaryLabel / accent）。
static SettingsColors settingsColors(bool dark) {
    if (dark) {
        return {RGB(0x1E, 0x1E, 0x20), RGB(0xF2, 0xF2, 0xF4), RGB(0x9E, 0x9E, 0xA6),
                RGB(0x0A, 0x84, 0xFF), RGB(0x4A, 0x4A, 0x4F)};
    }
    return {RGB(0xFF, 0xFF, 0xFF), RGB(0x1D, 0x1D, 0x1F), RGB(0x6E, 0x6E, 0x73),
            RGB(0x0A, 0x84, 0xFF), RGB(0xC8, 0xC8, 0xCC)};
}

static int settingsPt(int v) { return MulDiv(v, g_settingsDpi, 72); }

static void settingsDeleteFonts() {
    if (g_settingsFont != nullptr) { DeleteObject(g_settingsFont); g_settingsFont = nullptr; }
    if (g_settingsFontBold != nullptr) { DeleteObject(g_settingsFontBold); g_settingsFontBold = nullptr; }
    if (g_settingsFontHint != nullptr) { DeleteObject(g_settingsFontHint); g_settingsFontHint = nullptr; }
}

static void settingsCreateFonts() {
    g_settingsFont = CreateFontW(-settingsPt(13), 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
                                 DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                 CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
    g_settingsFontBold = CreateFontW(-settingsPt(13), 0, 0, 0, FW_BOLD, FALSE, FALSE, FALSE,
                                     DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                     CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
    g_settingsFontHint = CreateFontW(-settingsPt(11), 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
                                     DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                                     CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
}

// DWM 深色标题栏（Win10 1903+ 属性 20；1809 用 19）。动态加载 dwmapi：旧系统
// 或服务器核心没有该 API，失败静默忽略（标题栏保持系统默认）。
static void settingsApplyDarkTitleBar(HWND wnd, bool dark) {
    typedef HRESULT(WINAPI* DwmSetWindowAttributeFn)(HWND, DWORD, LPCVOID, DWORD);
    static DwmSetWindowAttributeFn fn = []() -> DwmSetWindowAttributeFn {
        HMODULE mod = LoadLibraryW(L"dwmapi.dll");
        if (mod == nullptr) return nullptr;
        return reinterpret_cast<DwmSetWindowAttributeFn>(
            reinterpret_cast<void*>(GetProcAddress(mod, "DwmSetWindowAttribute")));
    }();
    if (fn == nullptr) return;
    BOOL v = dark ? TRUE : FALSE;
    if (FAILED(fn(wnd, 20, &v, sizeof(v)))) fn(wnd, 19, &v, sizeof(v));
}

// 主题切换：重建背景刷、标题栏、重绘所有自绘控件（候选窗/Toast 另有实现）。
static void settingsUpdateTheme(HWND wnd) {
    g_settingsDark = isSystemDarkTheme();
    if (g_settingsBgBrush != nullptr) DeleteObject(g_settingsBgBrush);
    g_settingsBgBrush = CreateSolidBrush(settingsColors(g_settingsDark).bg);
    settingsApplyDarkTitleBar(wnd, g_settingsDark);
    for (int id : {101, 106, 107, 110, 121, 122, 123}) {
        HWND c = GetDlgItem(wnd, id);
        if (c != nullptr) InvalidateRect(c, nullptr, TRUE);
    }
    InvalidateRect(wnd, nullptr, TRUE);
}

// 控件布局（pt 坐标；创建 / 切页 / 滚动 / WM_DPICHANGED 共用，单一口径）。
//
// 每个控件声明「所属页 + 在卡片内的相对位置」，这里统一加上内容区偏移与
// 滚动量。未在当前页的控件直接 SWP_HIDEWINDOW，避免跨页串味。
struct SettingsCtlItem {
    int id;
    SettingsPage page;
    int x, y, w, h;   // 相对内容区左上角（pt）
    int fontKind;     // 0=正文 1=加粗 2=提示
};

static const SettingsCtlItem kSettingsCtl[] = {
    // ---- 输入页 ----
    {101, SettingsPage::Input, 20, 44, 200, 26, 0},   // 词组联想
    {102, SettingsPage::Input, 20, 74, 380, 18, 2},   // 说明
    {121, SettingsPage::Input, 20, 130, 110, 28, 0},  // 系统共享
    {122, SettingsPage::Input, 138, 130, 110, 28, 0}, // 每程序
    {123, SettingsPage::Input, 256, 130, 110, 28, 0}, // 每窗口
    {124, SettingsPage::Input, 20, 166, 400, 18, 2},  // 说明
    // ---- 词库页 ----
    {105, SettingsPage::Dictionary, 20, 44, 400, 18, 0},   // 学习数据统计
    {106, SettingsPage::Dictionary, 20, 76, 130, 28, 0},   // 清空
    {107, SettingsPage::Dictionary, 158, 76, 130, 28, 0},  // 导出
    {108, SettingsPage::Dictionary, 20, 112, 400, 18, 2},  // 说明
    {110, SettingsPage::Dictionary, 20, 160, 150, 28, 0},  // 导入词库
    {112, SettingsPage::Dictionary, 20, 196, 400, 18, 2},  // 说明
};

static void settingsLayoutControls(HWND wnd) {
    settingsClampScroll();
    const RECT content = settingsContentRect();
    for (const auto& it : kSettingsCtl) {
        HWND c = GetDlgItem(wnd, it.id);
        if (c == nullptr) continue;
        const bool visible = (it.page == g_settingsPage);
        const int x = content.left + it.x;
        const int y = content.top + it.y - g_settingsScroll;
        SetWindowPos(c, nullptr, settingsPt(x), settingsPt(y), settingsPt(it.w), settingsPt(it.h),
                     SWP_NOZORDER | SWP_NOACTIVATE | (visible ? SWP_SHOWWINDOW : SWP_HIDEWINDOW));
        HFONT font = g_settingsFont;
        if (it.fontKind == 1) font = g_settingsFontBold;
        else if (it.fontKind == 2) font = g_settingsFontHint;
        SendMessageW(c, WM_SETFONT, reinterpret_cast<WPARAM>(font), TRUE);
    }
    // 关于页没有子控件（版本号等信息直接自绘）；隐藏所有控件。
    if (g_settingsPage == SettingsPage::About) {
        for (const auto& it : kSettingsCtl) {
            HWND c = GetDlgItem(wnd, it.id);
            if (c != nullptr) ShowWindow(c, SW_HIDE);
        }
    }
    // 客户区尺寸（导航 + 内容 + 页脚），保持窗口左上角不变。
    RECT rc = {0, 0, settingsPt(kWinW), settingsPt(kWinH)};
    const DWORD style = static_cast<DWORD>(GetWindowLongPtrW(wnd, GWL_STYLE));
    const DWORD exStyle = static_cast<DWORD>(GetWindowLongPtrW(wnd, GWL_EXSTYLE));
    AdjustWindowRectEx(&rc, style, FALSE, exStyle);
    RECT wr{};
    GetWindowRect(wnd, &wr);
    SetWindowPos(wnd, nullptr, wr.left, wr.top, rc.right - rc.left, rc.bottom - rc.top,
                 SWP_NOZORDER | SWP_NOACTIVATE);
}

static void settingsApplyDpi(HWND wnd, int dpi) {
    if (dpi <= 0) dpi = systemDpi();
    g_settingsDpi = dpi;
    settingsDeleteFonts();
    settingsCreateFonts();
    settingsLayoutControls(wnd);
    InvalidateRect(wnd, nullptr, TRUE);
}

// ---- 设置窗自绘：左侧导航 / 卡片背景 / 页脚（2026-11-06）----
//
// 在 WM_PAINT 里一次性绘制「非交互」部分：导航栏、导航项（选中态高亮）、
// 每张卡片的圆角底、卡片内标题、页脚版本号。交互控件是子窗口，自己重绘。

// 圆角填充矩形（卡片/导航选中项共用）。
static void settingsFillRoundRect(HDC dc, const RECT& r, int radius, COLORREF fill) {
    HBRUSH brush = CreateSolidBrush(fill);
    HGDIOBJ oldBrush = SelectObject(dc, brush);
    HGDIOBJ oldPen = SelectObject(dc, GetStockObject(NULL_PEN));
    RoundRect(dc, r.left, r.top, r.right, r.bottom, radius, radius);
    SelectObject(dc, oldPen);
    SelectObject(dc, oldBrush);
    DeleteObject(brush);
}

// 导航项文案（顺序与 SettingsPage 一致）。
static const wchar_t* settingsNavLabel(int index) {
    switch (index) {
        case 0: return L"输入";
        case 1: return L"词库";
        case 2: return L"关于";
        default: return L"";
    }
}

// 导航项命中矩形（自绘 + 点击命中都用它，避免两套坐标）。
static RECT settingsNavItemRect(int index) {
    RECT r{};
    r.left = 10;
    r.top = 64 + index * 44;
    r.right = kNavWidth - 10;
    r.bottom = r.top + 36;
    return r;
}

static void settingsPaint(HDC dc, HWND wnd) {
    const SettingsColors c = settingsColors(g_settingsDark);
    RECT client{};
    GetClientRect(wnd, &client);
    const int W = client.right - client.left;
    const int H = client.bottom - client.top;
    if (W <= 0 || H <= 0) return;

    // 1) 整窗背景
    FillRect(dc, &client, g_settingsBgBrush);

    // 2) 左侧导航栏底色（比背景略深/略浅一档，形成分栏）
    RECT nav{0, 0, settingsPt(kNavWidth), H};
    const COLORREF navBg = g_settingsDark ? RGB(0x24, 0x24, 0x27) : RGB(0xF5, 0xF5, 0xF7);
    FillRect(dc, &nav, CreateSolidBrush(navBg));

    // 3) 应用标题
    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, c.text);
    HGDIOBJ oldFont = SelectObject(dc, g_settingsFontBold);
    RECT title{settingsPt(16), settingsPt(18), settingsPt(kNavWidth - 8), settingsPt(44)};
    DrawTextW(dc, L"Chaoset 设置", -1, &title, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, oldFont);

    // 4) 导航项（选中项圆角高亮 + 强调色文字）
    for (int i = 0; i < 3; ++i) {
        RECT r = settingsNavItemRect(i);
        RECT pr{settingsPt(r.left), settingsPt(r.top), settingsPt(r.right), settingsPt(r.bottom)};
        const bool selected = (static_cast<int>(g_settingsPage) == i);
        if (selected) {
            settingsFillRoundRect(dc, pr, settingsPt(8), c.accent);
        }
        SetTextColor(dc, selected ? RGB(0xFF, 0xFF, 0xFF) : c.text);
        HGDIOBJ of = SelectObject(dc, g_settingsFont);
        RECT tr = pr;
        tr.left += settingsPt(14);
        DrawTextW(dc, settingsNavLabel(i), -1, &tr, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, of);
    }

    // 5) 右侧内容卡片
    const RECT content = settingsContentRect();
    const int cardX = settingsPt(content.left);
    const int cardW = settingsPt(content.right - content.left);
    auto drawCard = [&](int y, int h, const wchar_t* caption) {
        RECT card{cardX, settingsPt(y), cardX + cardW, settingsPt(y + h)};
        const COLORREF cardBg = g_settingsDark ? RGB(0x2A, 0x2A, 0x2E) : RGB(0xFA, 0xFA, 0xFC);
        settingsFillRoundRect(dc, card, settingsPt(kCardRadius), cardBg);
        SetTextColor(dc, c.secondary);
        HGDIOBJ of = SelectObject(dc, g_settingsFontBold);
        RECT cap{card.left + settingsPt(kCardPad), card.top + settingsPt(12),
                 card.right - settingsPt(kCardPad), card.top + settingsPt(34)};
        DrawTextW(dc, caption, -1, &cap, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, of);
    };
    const int baseY = content.top - g_settingsScroll;
    if (g_settingsPage == SettingsPage::Input) {
        drawCard(baseY, 100, L"词组联想");
        drawCard(baseY + 114, 106, L"中英状态");
    } else if (g_settingsPage == SettingsPage::Dictionary) {
        drawCard(baseY, 132, L"学习数据");
        drawCard(baseY + 146, 166, L"词库");
    } else {
        drawCard(baseY, 150, L"关于");
        // 关于页信息直接画（没有交互控件）
        SetTextColor(dc, c.text);
        HGDIOBJ of = SelectObject(dc, g_settingsFont);
        wchar_t ver[128];
        swprintf_s(ver, L"Chaoset 版本 %hs", CHAOSET_VERSION);
        RECT vr{settingsPt(content.left + kCardPad), settingsPt(baseY + 44),
                settingsPt(content.right - kCardPad), settingsPt(baseY + 68)};
        DrawTextW(dc, ver, -1, &vr, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SetTextColor(dc, c.secondary);
        HGDIOBJ of2 = SelectObject(dc, g_settingsFontHint);
        RECT dr{settingsPt(content.left + kCardPad), settingsPt(baseY + 72),
                settingsPt(content.right - kCardPad), settingsPt(baseY + 130)};
        DrawTextW(dc, L"跨平台中文输入法（Windows TSF 壳）\n代码 MIT · 词库 CC BY-SA 4.0\n"
                      L"https://github.com/ShanWuYinShe/chaoset_ime",
                  -1, &dr, DT_LEFT | DT_WORDBREAK);
        SelectObject(dc, of2);
        SelectObject(dc, of);
    }

    // 6) 页脚版本号（固定在底部，不随滚动）
    RECT footer{settingsPt(kNavWidth + kContentPad), settingsPt(kWinH - kFooterH + 8),
                settingsPt(kWinW - kContentPad), settingsPt(kWinH - 6)};
    SetTextColor(dc, c.secondary);
    HGDIOBJ ff = SelectObject(dc, g_settingsFontHint);
    wchar_t footerVer[128];
    swprintf_s(footerVer, L"Chaoset 版本 %hs", CHAOSET_VERSION);
    DrawTextW(dc, footerVer, -1, &footer, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, ff);

    // 7) 滚动条（内容超出一页时，右侧细条）
    const int maxScroll = settingsMaxScroll();
    if (maxScroll > 0) {
        const int trackTop = settingsPt(content.top);
        const int trackH = settingsPt(content.bottom - content.top);
        const int thumbH = trackH * (content.bottom - content.top) /
                           (settingsPageContentHeight(g_settingsPage));
        const int thumbTop = trackTop + (trackH - thumbH) * g_settingsScroll / maxScroll;
        RECT thumb{settingsPt(kWinW - 8), thumbTop, settingsPt(kWinW - 4), thumbTop + thumbH};
        const COLORREF thumbColor = g_settingsDark ? RGB(0x5A, 0x5A, 0x60) : RGB(0xC0, 0xC0, 0xC6);
        settingsFillRoundRect(dc, thumb, settingsPt(2), thumbColor);
    }
}

// 自绘「三选一」按钮（中英状态作用域）。选中项用主题强调色描边 + 浅底，
// 与自绘按钮同一套配色，深浅色都跟随主题（Win32 标准按钮不跟随）。
static void settingsDrawChoice(const DRAWITEMSTRUCT* dis) {
    const SettingsColors c = settingsColors(g_settingsDark);
    HDC dc = dis->hDC;
    FillRect(dc, &dis->rcItem, g_settingsBgBrush);
    const bool focused = (dis->itemState & ODS_FOCUS) != 0;
    ModeScope current = uiLoadModeScope();
    bool selected = false;
    if (dis->CtlID == 121) selected = current == ModeScope::System;
    else if (dis->CtlID == 122) selected = current == ModeScope::Process;
    else if (dis->CtlID == 123) selected = current == ModeScope::Window;
    RECT r = dis->rcItem;
    const COLORREF fillColor = selected
                                   ? (g_settingsDark ? RGB(0x2A, 0x3A, 0x4A) : RGB(0xE3, 0xEF, 0xFB))
                                   : (g_settingsDark ? RGB(0x2C, 0x2C, 0x30) : RGB(0xF2, 0xF2, 0xF5));
    HBRUSH fill = CreateSolidBrush(fillColor);
    FillRect(dc, &r, fill);
    DeleteObject(fill);
    HPEN pen = CreatePen(PS_SOLID, (std::max)(1, settingsPt(selected ? 2 : 1)),
                         selected ? c.accent : c.border);
    HGDIOBJ oldPen = SelectObject(dc, pen);
    HGDIOBJ oldBrush = SelectObject(dc, GetStockObject(NULL_BRUSH));
    const int rad = settingsPt(6);
    RoundRect(dc, r.left, r.top, r.right, r.bottom, rad, rad);
    SelectObject(dc, oldBrush);
    SelectObject(dc, oldPen);
    DeleteObject(pen);
    wchar_t text[64] = {0};
    GetWindowTextW(dis->hwndItem, text, _countof(text));
    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, selected ? c.accent : c.text);
    HGDIOBJ oldFont = SelectObject(dc, g_settingsFont);
    DrawTextW(dc, text, -1, &r, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, oldFont);
    if (focused) {
        RECT fr = r;
        InflateRect(&fr, -settingsPt(2), -settingsPt(2));
        DrawFocusRect(dc, &fr);
    }
}

// 自绘按钮（深色模式下 Win32 标准按钮不跟随系统主题，故自绘）。
static void settingsDrawButton(const DRAWITEMSTRUCT* dis) {
    const SettingsColors c = settingsColors(g_settingsDark);
    HDC dc = dis->hDC;
    FillRect(dc, &dis->rcItem, g_settingsBgBrush);
    const bool pressed = (dis->itemState & ODS_SELECTED) != 0;
    RECT r = dis->rcItem;
    const COLORREF fillColor = pressed ? c.border
                                      : (g_settingsDark ? RGB(0x2C, 0x2C, 0x30) : RGB(0xF2, 0xF2, 0xF5));
    HBRUSH fill = CreateSolidBrush(fillColor);
    HPEN pen = CreatePen(PS_SOLID, (std::max)(1, settingsPt(1)), c.border);
    HGDIOBJ ob = SelectObject(dc, fill);
    HGDIOBJ op = SelectObject(dc, pen);
    RoundRect(dc, r.left, r.top, r.right, r.bottom, settingsPt(6), settingsPt(6));
    SelectObject(dc, ob);
    SelectObject(dc, op);
    DeleteObject(fill);
    DeleteObject(pen);
    wchar_t text[64] = {0};
    GetWindowTextW(dis->hwndItem, text, 64);
    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, c.text);
    HFONT oldFont = static_cast<HFONT>(SelectObject(dc, g_settingsFont));
    DrawTextW(dc, text, -1, &r, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, oldFont);
    if ((dis->itemState & ODS_FOCUS) != 0) DrawFocusRect(dc, &r);
}

// 自绘复选框：14pt 圆角方框 + 勾 + 文本（深色/浅色都自绘，避免主题控件发白）。
static void settingsDrawCheckbox(const DRAWITEMSTRUCT* dis) {
    const SettingsColors c = settingsColors(g_settingsDark);
    HDC dc = dis->hDC;
    FillRect(dc, &dis->rcItem, g_settingsBgBrush);
    const int box = settingsPt(14);
    const int top = dis->rcItem.top + ((dis->rcItem.bottom - dis->rcItem.top) - box) / 2;
    RECT br{dis->rcItem.left, top, dis->rcItem.left + box, top + box};
    HBRUSH fill = CreateSolidBrush(g_settingsAssociate ? c.accent : c.bg);
    HPEN pen = CreatePen(PS_SOLID, (std::max)(1, settingsPt(1)),
                         g_settingsAssociate ? c.accent : c.border);
    HGDIOBJ ob = SelectObject(dc, fill);
    HGDIOBJ op = SelectObject(dc, pen);
    RoundRect(dc, br.left, br.top, br.right, br.bottom, settingsPt(4), settingsPt(4));
    SelectObject(dc, ob);
    SelectObject(dc, op);
    DeleteObject(fill);
    DeleteObject(pen);
    if (g_settingsAssociate) {
        HPEN checkPen = CreatePen(PS_SOLID, (std::max)(1, settingsPt(2)), RGB(255, 255, 255));
        HGDIOBJ old = SelectObject(dc, checkPen);
        MoveToEx(dc, br.left + box * 28 / 100, br.top + box * 52 / 100, nullptr);
        LineTo(dc, br.left + box * 44 / 100, br.top + box * 70 / 100);
        LineTo(dc, br.left + box * 74 / 100, br.top + box * 32 / 100);
        SelectObject(dc, old);
        DeleteObject(checkPen);
    }
    RECT tr = dis->rcItem;
    tr.left = br.right + settingsPt(8);
    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, c.text);
    HFONT oldFont = static_cast<HFONT>(SelectObject(dc, g_settingsFont));
    DrawTextW(dc, L"词组联想", -1, &tr, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, oldFont);
    if ((dis->itemState & ODS_FOCUS) != 0) DrawFocusRect(dc, &tr);
}

// 学习数据区：统计刷新 / 清空 / 导出。全部经 IPC 连 daemon，不依赖 TIP。
static void SettingsRefreshStats(HWND wnd) {
    HWND label = GetDlgItem(wnd, 105);
    if (label == nullptr) return;
    PipeTransport transport;
    chaoset::Session session(transport);
    chaoset::UserStats st;
    if (!session.userStats(&st)) {
        SetWindowTextW(label, L"daemon 未运行，无法读取学习数据");
        return;
    }
    wchar_t buf[160];
    wsprintfW(buf, L"调频词 %d 条 · 用户词组 %d 条 · 上下文 %d 条", st.words, st.phrases, st.bigrams);
    SetWindowTextW(label, buf);
}

static void SettingsClearLearningData(HWND wnd) {
    if (MessageBoxW(wnd,
                    L"将删除全部调频词、用户词组与上下文共现记录（仅本机），操作不可恢复。\n\n确定清空学习数据？",
                    L"清空学习数据", MB_YESNO | MB_ICONWARNING | MB_DEFBUTTON2) != IDYES) {
        return;
    }
    PipeTransport transport;
    chaoset::Session session(transport);
    if (!session.userClear()) {
        MessageBoxW(wnd, L"清空失败：daemon 未运行，或未启用用户词典。", L"学习数据",
                    MB_OK | MB_ICONERROR);
        return;
    }
    SettingsRefreshStats(wnd);
}

static void SettingsExportLearningData(HWND wnd) {
    wchar_t path[MAX_PATH] = L"chaoset-userdict.tsv";
    OPENFILENAMEW ofn{};
    ofn.lStructSize = sizeof(ofn);
    ofn.hwndOwner = wnd;
    ofn.lpstrFilter = L"TSV 文件 (*.tsv)\0*.tsv\0所有文件 (*.*)\0*.*\0";
    ofn.lpstrFile = path;
    ofn.nMaxFile = MAX_PATH;
    ofn.lpstrTitle = L"导出学习数据";
    ofn.Flags = OFN_OVERWRITEPROMPT | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR;
    ofn.lpstrDefExt = L"tsv";
    if (!GetSaveFileNameW(&ofn)) return;
    PipeTransport transport;
    chaoset::Session session(transport);
    std::string tsv;
    if (!session.userExportAll(false, &tsv)) {
        MessageBoxW(wnd, L"导出失败：daemon 未运行，或未启用用户词典。", L"学习数据",
                    MB_OK | MB_ICONERROR);
        return;
    }
    HANDLE f = CreateFileW(path, GENERIC_WRITE, 0, nullptr, CREATE_ALWAYS,
                           FILE_ATTRIBUTE_NORMAL, nullptr);
    if (f == INVALID_HANDLE_VALUE) {
        MessageBoxW(wnd, L"无法写入所选文件。", L"学习数据", MB_OK | MB_ICONERROR);
        return;
    }
    DWORD written = 0;
    const BOOL ok = tsv.empty() ? TRUE : WriteFile(f, tsv.data(),
                                                  static_cast<DWORD>(tsv.size()), &written, nullptr);
    CloseHandle(f);
    if (!ok) {
        MessageBoxW(wnd, L"写入文件失败。", L"学习数据", MB_OK | MB_ICONERROR);
        return;
    }
    MessageBoxW(wnd, L"学习数据已导出。", L"学习数据", MB_OK | MB_ICONINFORMATION);
}

// ---- 词库导入（GUI，2026-10-31）----
//
// 普通用户不再需要命令行：设置窗口「词库」区选择 Rime dict.yaml /
// CC-CEDICT txt / TSV，调用随包 chaoset-import.exe 合并到
// %LOCALAPPDATA%\Chaoset\dict.tsv（旧文件先备份、同卷 staging +
// MoveFileEx 原子替换），然后重启 daemon（读安装器写入的 Run 项命令行）。
// 与 macOS Setup.app 的导入流程同语义（合并而非覆盖、备份、原子替换、
// 检查导入器退出码——只看产物文件会把部分解析失败当成成功）。

static std::wstring settingsQuote(const std::wstring& s) { return L"\"" + s + L"\""; }

// 运行子进程并等待退出；logPath 非空时把 stdout/stderr 重定向到该文件。
// 返回 true 表示进程正常退出（*exitCode 为退出码）；超时/启动失败返回 false。
static bool settingsRunProcess(const std::wstring& exe, const std::wstring& cmdLine,
                               const std::wstring& logPath, DWORD* exitCode) {
    if (exitCode != nullptr) *exitCode = 1;
    HANDLE logFile = INVALID_HANDLE_VALUE;
    HANDLE nul = INVALID_HANDLE_VALUE;
    STARTUPINFOW si{};
    si.cb = sizeof(si);
    if (!logPath.empty()) {
        logFile = CreateFileW(logPath.c_str(), GENERIC_WRITE, FILE_SHARE_READ, nullptr,
                              CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
        nul = CreateFileW(L"NUL", GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, nullptr,
                          OPEN_EXISTING, 0, nullptr);
        if (logFile == INVALID_HANDLE_VALUE || nul == INVALID_HANDLE_VALUE) {
            if (logFile != INVALID_HANDLE_VALUE) CloseHandle(logFile);
            if (nul != INVALID_HANDLE_VALUE) CloseHandle(nul);
            return false;
        }
        si.dwFlags = STARTF_USESTDHANDLES;
        si.hStdInput = nul;
        si.hStdOutput = logFile;
        si.hStdError = logFile;
    }
    std::wstring mutableCmd = cmdLine;  // CreateProcessW 需要可写缓冲
    PROCESS_INFORMATION pi{};
    const BOOL ok = CreateProcessW(exe.empty() ? nullptr : exe.c_str(), mutableCmd.data(),
                                   nullptr, nullptr, logPath.empty() ? FALSE : TRUE,
                                   CREATE_NO_WINDOW, nullptr, nullptr, &si, &pi);
    if (logFile != INVALID_HANDLE_VALUE) CloseHandle(logFile);
    if (nul != INVALID_HANDLE_VALUE) CloseHandle(nul);
    if (!ok) return false;
    const DWORD wait = WaitForSingleObject(pi.hProcess, 120000);
    DWORD code = 1;
    if (wait == WAIT_OBJECT_0) {
        GetExitCodeProcess(pi.hProcess, &code);
    } else {
        TerminateProcess(pi.hProcess, 1);
    }
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    if (exitCode != nullptr) *exitCode = code;
    return wait == WAIT_OBJECT_0;
}

// 启动常驻子进程（daemon）用：settingsRunProcess 会等待进程退出并超时
// TerminateProcess，不能拿来启动 daemon，否则设置窗会卡 120s 再把服务杀掉。
static bool settingsStartProcessDetached(const std::wstring& exe, const std::wstring& cmdLine) {
    std::wstring mutableCmd = cmdLine;
    STARTUPINFOW si{};
    si.cb = sizeof(si);
    PROCESS_INFORMATION pi{};
    BOOL ok = CreateProcessW(exe.empty() ? nullptr : exe.c_str(), mutableCmd.data(),
                             nullptr, nullptr, FALSE,
                             CREATE_NO_WINDOW | CREATE_BREAKAWAY_FROM_JOB,
                             nullptr, nullptr, &si, &pi);
    if (!ok && GetLastError() == ERROR_ACCESS_DENIED) {
        // 设置窗宿主在禁止 breakaway 的 Job 中时回退普通创建（短命设置窗
        // 通常无所谓；这里主要保证 daemon 不在可被宿主关闭杀死的 Job 内）。
        ok = CreateProcessW(exe.empty() ? nullptr : exe.c_str(), mutableCmd.data(),
                            nullptr, nullptr, FALSE, CREATE_NO_WINDOW,
                            nullptr, nullptr, &si, &pi);
    }
    if (ok) {
        CloseHandle(pi.hThread);
        CloseHandle(pi.hProcess);
    }
    return ok != FALSE;
}

// 便携安装/被手工删过 Run 项时的兜底：按 DLL 所在目录 + 当前用户数据目录
// 拼出与 TSF 懒启动相同的 -pipe 命令行，避免设置窗导入词库后无法重启 daemon。
static bool settingsStartDaemonFromInstall() {
    wchar_t module[MAX_PATH] = {0};
    if (GetModuleFileNameW(g_hInst, module, MAX_PATH) == 0) return false;
    std::wstring dir(module);
    const size_t slash = dir.find_last_of(L"\\/");
    if (slash == std::wstring::npos) return false;
    dir.resize(slash);
    const std::wstring exe = dir + L"\\chaosetd.exe";
    if (GetFileAttributesW(exe.c_str()) == INVALID_FILE_ATTRIBUTES) return false;

    wchar_t appData[MAX_PATH] = {0};
    if (FAILED(SHGetFolderPathW(nullptr, CSIDL_LOCAL_APPDATA, nullptr, SHGFP_TYPE_CURRENT, appData))) return false;
    const std::wstring dataDir = std::wstring(appData) + L"\\Chaoset";
    CreateDirectoryW(dataDir.c_str(), nullptr);
    std::wstring cmd = settingsQuote(exe) + L" -pipe" +
                       L" -dict " + settingsQuote(dataDir + L"\\dict.tsv") +
                       L" -seeddict " + settingsQuote(dir + L"\\dict.tsv") +
                       L" -userdict " + settingsQuote(dataDir + L"\\userdict.json") +
                       L" -log " + settingsQuote(dataDir + L"\\chaosetd.log");
    return settingsStartProcessDetached(exe, cmd);
}

// 重启 daemon：从安装器写入的 Run 项（HKLM 优先、HKCU 兜底）取完整命令行。
// 先 taskkill 旧进程再按原命令行启动；没有自启动项时用安装目录兜底启动
// （便携版没有 Run 项），仍失败才返回 false 让调用方提示重启。
static bool settingsRestartDaemon() {
    const wchar_t* kRunKey = L"Software\\Microsoft\\Windows\\CurrentVersion\\Run";
    std::wstring run;
    for (HKEY root : {HKEY_LOCAL_MACHINE, HKEY_CURRENT_USER}) {
        HKEY h = nullptr;
        if (RegOpenKeyExW(root, kRunKey, 0, KEY_READ, &h) != ERROR_SUCCESS) continue;
        DWORD type = 0;
        DWORD size = 0;
        if (RegQueryValueExW(h, L"ChaosetDaemon", nullptr, &type, nullptr, &size) == ERROR_SUCCESS &&
            (type == REG_SZ || type == REG_EXPAND_SZ) && size > sizeof(wchar_t)) {
            std::vector<wchar_t> val(size / sizeof(wchar_t) + 1, 0);
            if (RegQueryValueExW(h, L"ChaosetDaemon", nullptr, nullptr,
                                 reinterpret_cast<LPBYTE>(val.data()), &size) == ERROR_SUCCESS) {
                run = val.data();
                if (type == REG_EXPAND_SZ) {
                    // 安装器写的是 REG_EXPAND_SZ（保留 %LOCALAPPDATA%）；CreateProcess
                    // 不会展开环境变量，必须用设置窗当前用户的环境展开后再启动。
                    std::vector<wchar_t> expanded(32768, 0);
                    const DWORD n = ExpandEnvironmentStringsW(run.c_str(), expanded.data(),
                                                              static_cast<DWORD>(expanded.size()));
                    if (n > 0 && n <= expanded.size()) run = expanded.data();
                }
            }
        }
        RegCloseKey(h);
        if (!run.empty()) break;
    }
    wchar_t sysDir[MAX_PATH] = {0};
    if (GetSystemDirectoryW(sysDir, MAX_PATH) == 0) return false;
    const std::wstring taskkill = std::wstring(sysDir) + L"\\taskkill.exe";
    settingsRunProcess(taskkill, settingsQuote(taskkill) + L" /IM chaosetd.exe /F", L"", nullptr);
    // taskkill /F 立即生效，但进程退出与监听资源释放可能略滞后；给 1s 再启动，
    // 避免新 daemon 因单实例互斥体/管道占位启动失败（导入是显式操作，1s 可接受）。
    Sleep(1000);
    if (!run.empty()) {
        return settingsStartProcessDetached(L"", run);
    }
    return settingsStartDaemonFromInstall();
}

static void SettingsImportDictionary(HWND wnd) {
    // 1) 用户数据目录 + 随包 DLL/种子目录
    wchar_t appData[MAX_PATH] = {0};
    if (FAILED(SHGetFolderPathW(nullptr, CSIDL_LOCAL_APPDATA, nullptr, SHGFP_TYPE_CURRENT, appData))) {
        MessageBoxW(wnd, L"无法定位用户数据目录。", L"导入词库", MB_OK | MB_ICONERROR);
        return;
    }
    const std::wstring dataDir = std::wstring(appData) + L"\\Chaoset";
    wchar_t module[MAX_PATH] = {0};
    if (GetModuleFileNameW(g_hInst, module, MAX_PATH) == 0) return;
    std::wstring dir(module);
    const size_t slash = dir.find_last_of(L"\\/");
    if (slash == std::wstring::npos) return;
    dir.resize(slash);

    // 2) 词库文件位置；安装器不再以管理员身份复制用户词库，首次导入前
    // 用随包种子初始化（与 daemon -seeddict 语义一致，不覆盖已有文件）。
    const std::wstring dictPath = dataDir + L"\\dict.tsv";
    if (GetFileAttributesW(dictPath.c_str()) == INVALID_FILE_ATTRIBUTES) {
        const std::wstring seed = dir + L"\\dict.tsv";
        CreateDirectoryW(dataDir.c_str(), nullptr);
        if (GetFileAttributesW(seed.c_str()) != INVALID_FILE_ATTRIBUTES) {
            CopyFileW(seed.c_str(), dictPath.c_str(), FALSE);
        }
    }
    if (GetFileAttributesW(dictPath.c_str()) == INVALID_FILE_ATTRIBUTES) {
        MessageBoxW(wnd, L"找不到词库文件 dict.tsv。\n请先通过安装程序安装 Chaoset 后再导入。",
                    L"导入词库", MB_OK | MB_ICONERROR);
        return;
    }
    // 3) 随包导入器（与 DLL 同目录；便携 zip 里也在同目录）
    const std::wstring importer = dir + L"\\chaoset-import.exe";
    if (GetFileAttributesW(importer.c_str()) == INVALID_FILE_ATTRIBUTES) {
        MessageBoxW(wnd, L"找不到 chaoset-import.exe（请使用完整安装包）。", L"导入词库",
                    MB_OK | MB_ICONERROR);
        return;
    }
    // 3) 选择词库文件（可多选）
    std::vector<wchar_t> buf(4096, 0);
    OPENFILENAMEW ofn{};
    ofn.lStructSize = sizeof(ofn);
    ofn.hwndOwner = wnd;
    ofn.lpstrFilter = L"词库文件 (*.tsv;*.txt;*.yaml)\0*.tsv;*.txt;*.yaml\0所有文件 (*.*)\0*.*\0";
    ofn.lpstrFile = buf.data();
    ofn.nMaxFile = static_cast<DWORD>(buf.size());
    ofn.lpstrTitle = L"选择要导入的词库";
    ofn.Flags = OFN_ALLOWMULTISELECT | OFN_EXPLORER | OFN_FILEMUSTEXIST |
                OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR;
    if (!GetOpenFileNameW(&ofn)) {
        if (CommDlgExtendedError() == FNERR_BUFFERTOOSMALL) {
            MessageBoxW(wnd, L"选择的文件过多，请分批导入。", L"导入词库", MB_OK | MB_ICONERROR);
        }
        return;
    }
    std::vector<std::wstring> files;
    {
        const wchar_t* p = buf.data();
        const std::wstring first = p;
        p += first.size() + 1;
        if (*p == L'\0') {
            files.push_back(first);
        } else {
            while (*p != L'\0') {
                const std::wstring name = p;
                files.push_back(first + L"\\" + name);
                p += name.size() + 1;
            }
        }
    }
    if (files.empty()) return;
    // 4) 合并/备份/原子替换全部交给 Go 工具：壳只传 -o/-backup 和文件列表。
    //    （2026-11-01 用户要求工具逻辑用 Go，不在 shims 里重复实现。）
    const std::wstring logPath = dataDir + L"\\import.log";
    std::wstring cmd = settingsQuote(importer) + L" -o " + settingsQuote(dictPath) +
                      L" -backup";
    for (const auto& f : files) cmd += L" " + settingsQuote(f);
    DWORD code = 1;
    HCURSOR oldCursor = SetCursor(LoadCursorW(nullptr, MAKEINTRESOURCEW(32514)));  // IDC_WAIT
    const bool ran = settingsRunProcess(importer, cmd, logPath, &code);
    SetCursor(oldCursor);
    if (!ran || code != 0 || GetFileAttributesW(dictPath.c_str()) == INVALID_FILE_ATTRIBUTES) {
        std::wstring msg = L"导入失败";
        if (ran) msg += L"（导入器退出码 " + std::to_wstring(code) + L"）";
        msg += L"。\n\n详情见：" + logPath;
        MessageBoxW(wnd, msg.c_str(), L"导入词库", MB_OK | MB_ICONERROR);
        return;
    }
    // 5) 重启 daemon（新词库在启动时加载）
    const bool restarted = settingsRestartDaemon();
    std::wstring msg = L"词库已导入。\n\n旧词库已由导入器备份为 dict.tsv.bak.<时间戳>"
                      L"（首次导入会跳过备份）。";
    msg += restarted ? L"\n\ndaemon 已重启，新词库立即生效。"
                     : L"\n\n未找到 daemon 自启动项，请重启电脑或注销后重新登录使新词库生效。";
    MessageBoxW(wnd, msg.c_str(), L"导入词库", MB_OK | MB_ICONINFORMATION);
}

// 切换设置页：重排控件 + 重绘（导航高亮与卡片都随页变化）。
static void settingsSwitchPage(HWND wnd, SettingsPage page) {
    if (g_settingsPage == page) return;
    g_settingsPage = page;
    g_settingsScroll = 0;  // 换页回到顶部，避免出现「新页莫名从中间开始」
    settingsLayoutControls(wnd);
    InvalidateRect(wnd, nullptr, TRUE);
}

static LRESULT CALLBACK SettingsWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
        // 整窗自绘：导航栏、卡片底、页脚版本号、滚动条（2026-11-06 卡片式重构）。
        case WM_PAINT: {
            PAINTSTRUCT ps{};
            HDC dc = BeginPaint(hwnd, &ps);
            settingsPaint(dc, hwnd);
            EndPaint(hwnd, &ps);
            return 0;
        }
        case WM_ERASEBKGND:
            return 1;  // 背景由 WM_PAINT 统一画，避免闪烁
        // 左键点导航切换页（导航是自绘的，不是子窗口，只能在这里命中）。
        case WM_LBUTTONDOWN: {
            const POINT pt{GET_X_LPARAM(lParam), GET_Y_LPARAM(lParam)};
            for (int i = 0; i < 3; ++i) {
                RECT r = settingsNavItemRect(i);
                RECT pr{settingsPt(r.left), settingsPt(r.top), settingsPt(r.right),
                        settingsPt(r.bottom)};
                if (PtInRect(&pr, pt)) {
                    settingsSwitchPage(hwnd, static_cast<SettingsPage>(i));
                    return 0;
                }
            }
            return 0;
        }
        // 滚轮滚动内容区（只有内容超出一页时才真正移动）。
        case WM_MOUSEWHEEL: {
            if (settingsMaxScroll() <= 0) return 0;
            const int delta = GET_WHEEL_DELTA_WPARAM(wParam);
            g_settingsScroll -= delta > 0 ? 24 : -24;
            settingsClampScroll();
            settingsLayoutControls(hwnd);
            InvalidateRect(hwnd, nullptr, TRUE);
            return 0;
        }
        case WM_COMMAND:
            if (HIWORD(wParam) == BN_CLICKED && LOWORD(wParam) == 106) {
                SettingsClearLearningData(hwnd);
                return 0;
            }
            if (HIWORD(wParam) == BN_CLICKED && LOWORD(wParam) == 107) {
                SettingsExportLearningData(hwnd);
                return 0;
            }
            if (HIWORD(wParam) == BN_CLICKED && LOWORD(wParam) == 110) {
                SettingsImportDictionary(hwnd);
                return 0;
            }
            if (HIWORD(wParam) == BN_CLICKED &&
                (LOWORD(wParam) == 121 || LOWORD(wParam) == 122 || LOWORD(wParam) == 123)) {
                // 中英状态作用域切换：写 HKCU，立即生效（TIP 下次获得焦点时读取）。
                ModeScope scope = ModeScope::System;
                if (LOWORD(wParam) == 122) scope = ModeScope::Process;
                else if (LOWORD(wParam) == 123) scope = ModeScope::Window;
                uiSaveModeScope(scope);
                for (int id : {121, 122, 123}) {
                    HWND b = GetDlgItem(hwnd, id);
                    if (b != nullptr) InvalidateRect(b, nullptr, TRUE);
                }
                return 0;
            }
            if (LOWORD(wParam) == 101 && HIWORD(wParam) == BN_CLICKED) {
                // 自绘复选框：手动翻转状态（BS_OWNERDRAW 不自动切换）。
                // 只写注册表，**不解引用 TIP**：设置窗的生命周期与 TIP 无关
                // （TSF 随时可能 Release TIP，而窗口还开着——此前把裸
                // ChaosetTIP* 存在 GWLP_USERDATA，TIP 释放后点复选框就是
                // 宿主进程内 UAF，2026-10-30 三端一致性审计 P1-2）。
                //
                // 关闭联想时不再只能等下一次按键：用跨进程广播通知所有宿主
                // 进程的候选窗立即收起（候选窗自己转发给同进程 TIP），与 macOS
                // 设置窗通知即时收起对齐；设置窗仍不持有 TIP 指针，不引入 UAF。
                g_settingsAssociate = !g_settingsAssociate;
                RegSetAssociate(g_settingsAssociate);
                if (!g_settingsAssociate) {
                    PostMessageW(HWND_BROADCAST, associateChangedMessage(), 0, 0);
                }
                HWND chk = GetDlgItem(hwnd, 101);
                if (chk != nullptr) InvalidateRect(chk, nullptr, TRUE);
            }
            return 0;
        case WM_DRAWITEM: {
            const DRAWITEMSTRUCT* dis = reinterpret_cast<const DRAWITEMSTRUCT*>(lParam);
            if (dis != nullptr) {
                if (dis->CtlID == 101) {
                    settingsDrawCheckbox(dis);
                    return TRUE;
                }
                if (dis->CtlID == 106 || dis->CtlID == 107 || dis->CtlID == 110) {
                    settingsDrawButton(dis);
                    return TRUE;
                }
                // 中英状态三选一（121/122/123）：选中项高亮。
                if (dis->CtlID == 121 || dis->CtlID == 122 || dis->CtlID == 123) {
                    settingsDrawChoice(dis);
                    return TRUE;
                }
            }
            break;
        }
        case WM_CTLCOLORSTATIC: {
            // 说明文字/分组标题用次级文本色（macOS secondaryLabel 对应），
            // 正文用主文本色；背景统一返回主题背景刷（深色模式必需）。
            if (g_settingsBgBrush == nullptr) {
                g_settingsBgBrush = CreateSolidBrush(settingsColors(g_settingsDark).bg);
            }
            const int id = GetDlgCtrlID(reinterpret_cast<HWND>(lParam));
            const SettingsColors c = settingsColors(g_settingsDark);
            const bool secondary = (id == 100 || id == 109 || id == 111 || id == 120 ||
                                    id == 102 || id == 103 || id == 104 || id == 108 ||
                                    id == 112 || id == 124);
            HDC dc = reinterpret_cast<HDC>(wParam);
            SetTextColor(dc, secondary ? c.secondary : c.text);
            SetBkMode(dc, TRANSPARENT);
            return reinterpret_cast<LRESULT>(g_settingsBgBrush);
        }
        case WM_SETTINGCHANGE:
        case WM_THEMECHANGED:
            // 系统深浅色/主题切换：重建背景刷 + DWM 标题栏 + 重绘自绘控件。
            settingsUpdateTheme(hwnd);
            return 0;
        case WM_DPICHANGED:
            // 窗口跨到不同缩放的显示器：按新 DPI 重排控件与窗口尺寸。
            settingsApplyDpi(hwnd, static_cast<int>(HIWORD(wParam)));
            return 0;
        case WM_CLOSE:
            DestroyWindow(hwnd);
            return 0;
        case WM_DESTROY:
            settingsDeleteFonts();
            if (g_settingsBgBrush != nullptr) { DeleteObject(g_settingsBgBrush); g_settingsBgBrush = nullptr; }
            g_settingsWnd = nullptr;
            InterlockedDecrement(&g_liveSettingsWindows);
            return 0;
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

// 设置窗的真正创建入口：只在单例 UI 宿主进程（chaoset-ui.exe）里调用。
// 从「TIP 宿主进程内创建」改为外部 UI 进程后：
//   - 开始菜单/搜索（Low IL AppContainer）里点齿轮也能弹出：不再被沉浸式
//     z 序 band 压住，也不再受 AC 的文件对话框/子进程限制；
//   - 宿主应用退出不会带走设置窗，宿主也不会因为设置窗存活而无法卸载 DLL。
// 打开设置窗。page 缺省为「输入」页；托盘菜单「关于」用 About 页。
// 单例窗口已存在时只切页并前置，不另开新窗。
static void openSettingsWindowStandalone(SettingsPage page = SettingsPage::Input) {
    if (g_settingsWnd != nullptr && IsWindow(g_settingsWnd)) {
        settingsSwitchPage(g_settingsWnd, page);
        SetForegroundWindow(g_settingsWnd);
        return;
    }
    g_settingsPage = page;

    const std::wstring cls = windowClassName(L"ChaosetSettingsWndClass", g_hInst);
    WNDCLASSEXW wc{};
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = SettingsWndProc;
    wc.hInstance = g_hInst;
    wc.hCursor = LoadCursorW(nullptr, MAKEINTRESOURCEW(32512));
    wc.hbrBackground = nullptr;  // 自绘背景（深色模式跟随系统）
    wc.lpszClassName = cls.c_str();
    RegisterClassExW(&wc);

    // per-monitor DPI：按鼠标所在显示器取 DPI（UI 进程没有 TIP 的 caretPos_），
    // 副屏/混合 DPI 下也能开在正确显示器。
    POINT anchor{};
    GetCursorPos(&anchor);
    g_settingsDpi = dpiForPoint(anchor);
    g_settingsDark = isSystemDarkTheme();
    g_settingsAssociate = RegGetAssociate();
    settingsCreateFonts();

    // 居中于光标所在显示器的工作区（macOS center 同语义）
    const DWORD style = WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU;
    const DWORD exStyle = WS_EX_DLGMODALFRAME;
    RECT fr = {0, 0, settingsPt(kWinW), settingsPt(kWinH)};
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
    if (wnd == nullptr) {
        settingsDeleteFonts();
        return;
    }
    // 注意：这里**不再写 GWLP_USERDATA**（此前存的是裸 this）。设置窗的
    // 窗口过程只做 RegSetAssociate，不需要访问 TIP；不存指针就没有「TIP
    // 已释放、窗口还活着」的悬垂解引用（审计 P1-2）。
    g_settingsWnd = wnd;
    InterlockedIncrement(&g_liveSettingsWindows);

    // 分组标题用加粗 Static（与 macOS 同构；深色下不会出现浅色 GroupBox 边框）；
    // 复选框与按钮自绘（BS_OWNERDRAW），深色/浅色都跟随主题。
    auto makeCtl = [&](const wchar_t* ctlClass, DWORD ctlStyle, const wchar_t* text, int id) {
        return CreateWindowExW(0, ctlClass, text, WS_CHILD | WS_VISIBLE | ctlStyle,
                               0, 0, 0, 0, wnd,
                               reinterpret_cast<HMENU>(static_cast<INT_PTR>(id)), g_hInst, nullptr);
    };
    makeCtl(L"Static", SS_LEFT, L"输入行为", 100);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"词组联想", 101);
    makeCtl(L"Static", SS_LEFT, L"选词上屏后展示可续接的词组候选；", 102);
    makeCtl(L"Static", SS_LEFT, L"关闭后不再弹出联想候选。", 103);
    // 中英状态作用域（2026-11-06 用户要求可配置）：三选一按钮组。
    // 说明写清楚差异，避免用户不知道「系统共享」和「每窗口」到底差在哪。
    makeCtl(L"Static", SS_LEFT, L"中英状态", 120);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"系统共享", 121);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"每程序", 122);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"每窗口", 123);
    makeCtl(L"Static", SS_LEFT, L"切一次所有程序都生效（推荐）；后两者仅当前程序/窗口独立。", 124);
    makeCtl(L"Static", SS_LEFT, L"学习数据", 109);
    makeCtl(L"Static", SS_LEFT, L"读取中…", 105);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"清空学习数据…", 106);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"导出学习数据…", 107);
    makeCtl(L"Static", SS_LEFT, L"学习数据仅保存在本机，用于调频与词组联想；清空后不可恢复。", 108);
    makeCtl(L"Static", SS_LEFT, L"词库", 111);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"导入词库…", 110);
    makeCtl(L"Static", SS_LEFT, L"支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启 daemon。", 112);
    makeCtl(L"Static", SS_LEFT, L"更多设置将在后续版本提供", 104);
    std::wstring versionText = L"Chaoset 版本 " + toWide(CHAOSET_VERSION);
    makeCtl(L"Static", SS_LEFT, versionText.c_str(), 113);

    settingsLayoutControls(wnd);
    settingsUpdateTheme(wnd);

    HANDLE icoBig = LoadImageW(g_hInst, MAKEINTRESOURCEW(1), IMAGE_ICON,
                               GetSystemMetrics(SM_CXICON), GetSystemMetrics(SM_CYICON), LR_DEFAULTCOLOR);
    if (icoBig != nullptr) SendMessageW(wnd, WM_SETICON, ICON_BIG, reinterpret_cast<LPARAM>(icoBig));
    HANDLE icoSmall = LoadImageW(g_hInst, MAKEINTRESOURCEW(1), IMAGE_ICON,
                                 GetSystemMetrics(SM_CXSMICON), GetSystemMetrics(SM_CYSMICON), LR_DEFAULTCOLOR);
    if (icoSmall != nullptr) SendMessageW(wnd, WM_SETICON, ICON_SMALL, reinterpret_cast<LPARAM>(icoSmall));

    ShowWindow(wnd, SW_SHOWNORMAL);
    // 窗口先显示再读统计：daemon 不可达时连接最多 20ms 超时+冷却，不能让
    // 设置窗的首次出现被它拖慢（标签先显示「读取中…」，随后刷新）。
    SettingsRefreshStats(wnd);
    // 前台激活：从开始菜单/搜索（AC）里点齿轮时，UI 宿主所在的普通进程要
    // 把窗口切到前台，否则可能被 Start 的 z 序 band 压住看不到。TIP 侧已调用
    // AllowSetForegroundWindow；仍失败时用一次 TOPMOST 切换强制置顶（随后恢复
    // 非 topmost，避免长期遮挡），再前台化并闪烁任务栏按钮。
    SetForegroundWindow(wnd);
    if (GetForegroundWindow() != wnd) {
        SetWindowPos(wnd, HWND_TOPMOST, 0, 0, 0, 0,
                     SWP_NOMOVE | SWP_NOSIZE | SWP_SHOWWINDOW);
        SetWindowPos(wnd, HWND_NOTOPMOST, 0, 0, 0, 0,
                     SWP_NOMOVE | SWP_NOSIZE | SWP_SHOWWINDOW);
        SetForegroundWindow(wnd);
        FLASHWINFO flash{};
        flash.cbSize = sizeof(flash);
        flash.hwnd = wnd;
        flash.dwFlags = FLASHW_ALL;
        flash.uCount = 3;
        FlashWindowEx(&flash);
    }
    if (diagBudget()) {
        diagPrintf(L"tsf.log", "settings shown foreground=%d selfpid=%lu\n",
                   GetForegroundWindow() == wnd ? 1 : 0,
                   static_cast<unsigned long>(GetCurrentProcessId()));
    }
}

// 候选栏齿轮：设置窗由单例 UI 进程（chaoset-ui.exe）创建，TIP 只发管道请求。
// 优先请求已在运行的 UI 宿主；没有宿主时由 UI 宿主带 --settings 启动自己
// （首实例直接弹设置窗，第二个实例把请求转给首实例后退出）。AppContainer
// 宿主不能发起进程，只能连接登录 Run 项常驻的 UI 宿主。
void ChaosetTIP::showSettingsWindow() {
    if (openSettingsViaUi()) return;
    // 兜底：AppContainer 宿主（开始菜单/搜索）里 TIP 不能 CreateProcess，请
    // daemon（同一用户的普通完整性进程）代拉起 UI 宿主。
    //
    // **不轮询**（2026-11-06 用户质疑「为什么 5×20ms 轮询」）：那次轮询是为了
    // 等刚拉起的 UI 宿主开始监听管道——但这是自找的。只要让 daemon 用
    // --settings 拉起：首实例启动后自己弹设置窗；已有实例时第二实例把请求
    // 转发给首实例（转发重试在 runUiHost 的互斥体分支里，跑在 UI 宿主自己
    // 的进程里，不占 TIP 线程）。TIP 发完 IPC 立刻返回，零等待。
    if (isAppContainer() && ensureUiHostStartedByDaemon(/*settings=*/true)) {
        return;
    }
    static volatile LONG logged = 0;
    if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
        diagPrintf(L"tsf.log", "open settings failed: ui host unavailable appcontainer=%d\n",
                   isAppContainer() ? 1 : 0);
    }
    // 点击齿轮是最明确的「用户想要设置」信号：失败不能静默（用户会点两次、
    // 三次都没有反应）。候选窗提示条用鼠标锚点（当前操作就是鼠标点击）。
    showToast(L"设置窗口打不开：chaoset-ui.exe 未运行", true);
}

// ---- UI 宿主进程（chaoset-ui.exe）----
//
// 进程模型：chaoset-ui.exe 只有几十行（LoadLibrary 本 DLL + 调用导出
// ChaosetRunUi），托盘绘制/设置窗/命名管道全部复用本文件代码，不存在第二套
// UI 实现。这样做的原因见文件开头「单例 UI 宿主进程通信」的说明。
//
// 单实例：Local\ChaosetUi-<SID>-<session> 互斥体。第二个实例若带 --settings，
// 就把 "settings" 命令转发给首实例后退出（连点齿轮不会开多个设置窗）。
//
// 生命周期：登录 Run 项常驻；TIP 在托盘/设置窗需要时按需拉起
// （ensureUiHostStarted / openSettingsViaUi / daemon 代拉起）。退出只由本输入法
// 自己的维护动作触发（安装器 taskkill，或向隐藏窗口 PostMessage(WM_CLOSE) 做
// 测试）——命令管道**不接受 quit**：DACL 对 ALL APPLICATION PACKAGES 开放，
// 任意低完整性进程都能连接，不能让它关掉会话级 UI。
//
// 退出顺序（2026-11-05 评审 P0，必须保持）：置 stopping → SetEvent → 打断并
// **join 管道线程**（CancelSynchronousIo + WaitForSingleObject）→ 再销毁托盘/
// 隐藏窗口 → 返回（ui_main.cpp 之后才 FreeLibrary）。否则管道线程可能正阻塞在
// ConnectNamedPipe/ReadFile，DLL 卸载后它会在已卸载代码里恢复并解引用已释放的
// g_uiTray。
static constexpr UINT kUiOpenSettingsMsg = WM_APP + 71;
// 打开设置窗并切到「关于」页（托盘右键菜单用，2026-11-06）。
static constexpr UINT kUiOpenSettingsAboutMsg = WM_APP + 74;
static constexpr UINT kUiSetModeMsg = WM_APP + 72;
// kUiToggleModeMsg 定义见文件前部（TIP 侧托盘点击也要用）。
HWND g_uiHostWnd = nullptr;
static UiTray* g_uiTray = nullptr;  // 进程生命周期内唯一托盘实例（仅 UI 主线程使用）
static HANDLE g_uiStopEvent = nullptr;
static volatile LONG g_uiStopping = 0;

// UiTray 左键单击入口（UiTray::wndProc 里调用；只投递消息，不直接建窗）。
static void onUiTrayIconClicked() {
    if (g_uiHostWnd != nullptr) PostMessageW(g_uiHostWnd, kUiOpenSettingsMsg, 0, 0);
}

// 托盘右键菜单（2026-11-06 用户要求）。
//
// 只放「确有必要」的项：中英切换给了左键，所以菜单里不再重复；设置与关于是
// 托盘唯一能触达的入口。
//
// TrackPopupMenu 的调用约定（容易踩）：先 SetForegroundWindow(owner) 再弹，
// 否则点菜单外部不会消失（菜单卡住）；弹完必须 PostMessage(WM_NULL) 让菜单
// 消息循环收尾，这是微软文档给的标准做法。
static void showTrayMenu(HWND owner) {
    HMENU menu = CreatePopupMenu();
    if (menu == nullptr) return;
    AppendMenuW(menu, MF_STRING, 1, L"打开设置…");
    AppendMenuW(menu, MF_SEPARATOR, 0, nullptr);
    AppendMenuW(menu, MF_STRING, 2, L"关于 Chaoset");
    POINT pt{};
    GetCursorPos(&pt);
    SetForegroundWindow(owner);
    const UINT cmd = TrackPopupMenu(menu, TPM_RETURNCMD | TPM_RIGHTBUTTON,
                                    pt.x, pt.y, 0, owner, nullptr);
    DestroyMenu(menu);
    PostMessageW(owner, WM_NULL, 0, 0);
    if (cmd == 1) {
        // 打开设置窗（输入页）。
        if (g_uiHostWnd != nullptr) PostMessageW(g_uiHostWnd, kUiOpenSettingsMsg, 0, 0);
    } else if (cmd == 2) {
        // 「关于」不另开对话框：直接打开设置窗的「关于」页（2026-11-06 用户
        // 要求「关于其实就在设置窗口中已经有了」）。版本号与项目链接都在那一页。
        if (g_uiHostWnd != nullptr) {
            PostMessageW(g_uiHostWnd, kUiOpenSettingsAboutMsg, 0, 0);
        }
    }
}

// 上次中英模式的持久化：UI 宿主是中等完整性进程，可写 HKCU；TIP 在
// AppContainer 里写不了。宿主被杀/升级重启后靠它恢复「英」而不是跳回「中」。
static bool uiLoadSavedEnglish() {
    DWORD value = 0;
    DWORD size = sizeof(value);
    if (RegGetValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"UiEnglish",
                     RRF_RT_REG_DWORD, nullptr, &value, &size) == ERROR_SUCCESS) {
        return value != 0;
    }
    return false;
}

static void uiSaveEnglish(bool english) {
    const DWORD value = english ? 1 : 0;
    RegSetKeyValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"UiEnglish",
                    REG_DWORD, &value, sizeof(value));
}

// 权威中英状态（system 作用域下的唯一真相）。
//
// 为什么放在 UI 宿主：它每用户会话唯一、常驻、中等完整性（能写 HKCU），
// 而 TIP 会被装进任意宿主进程（含 AppContainer，写不了 HKCU）。
// g_uiEnglish 只在 UI 主线程读写（托盘/菜单/管道消息都由主线程处理）。
static bool g_uiEnglish = false;

static bool uiCurrentEnglish() { return g_uiEnglish; }

// ModeScope 的定义见文件前部（TIP 侧要先用到）。读取失败一律按 system
// （最符合用户直觉：切一次全系统生效）。
static ModeScope uiLoadModeScope() {
    wchar_t buf[32] = {0};
    DWORD size = sizeof(buf) - sizeof(wchar_t);
    if (RegGetValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"ModeScope",
                     RRF_RT_REG_SZ, nullptr, buf, &size) == ERROR_SUCCESS) {
        if (_wcsicmp(buf, L"process") == 0) return ModeScope::Process;
        if (_wcsicmp(buf, L"window") == 0) return ModeScope::Window;
    }
    return ModeScope::System;
}

static void uiSaveModeScope(ModeScope scope) {
    const wchar_t* text = L"system";
    if (scope == ModeScope::Process) text = L"process";
    else if (scope == ModeScope::Window) text = L"window";
    RegSetKeyValueW(HKEY_CURRENT_USER, L"Software\\Chaoset", L"ModeScope",
                    REG_SZ, text, static_cast<DWORD>((wcslen(text) + 1) * sizeof(wchar_t)));
}

// 管道线程只解析命令并 PostMessage 到 UI 主线程：托盘图标（icon_/shown_/窗口）
// 只能由拥有消息循环的主线程操作，否则与 TaskbarCreated/初始 show 竞争。
// 处理一条命令，返回要回给调用方的应答（空串 = 不回应答）。
//
// 应答是双向管道的核心用途（2026-11-06）：托盘左键切中英后，TIP 需要立刻知道
// 新状态；system 作用域下任何 TIP 也都可能来查权威状态。
static std::string uiHandleCommand(const char* command, DWORD len) {
    if (len >= 6 && strncmp(command, "mode ", 5) == 0) {
        if (g_uiHostWnd != nullptr) {
            PostMessageW(g_uiHostWnd, kUiSetModeMsg, command[5] == '1' ? 1 : 0, 0);
        }
        return std::string();
    }
    // 查询/切换权威中英状态（system 作用域）。应答格式 "mode 0\n"/"mode 1\n"。
    if (len >= 6 && strncmp(command, "query", 5) == 0) {
        return std::string(uiCurrentEnglish() ? "mode 1\n" : "mode 0\n");
    }
    if (len >= 6 && strncmp(command, "toggle", 6) == 0) {
        // 在 UI 主线程外翻转状态不安全（托盘图标与 HKCU 都归主线程）：
        // 投递消息让主线程翻转，应答返回**翻转前**的状态，调用方据此自己
        // 取反或用下一次 query 确认（幂等，重复 toggle 不会抖）。
        if (g_uiHostWnd != nullptr) PostMessageW(g_uiHostWnd, kUiToggleModeMsg, 0, 0);
        return std::string();
    }
    if (len >= 8 && strncmp(command, "settings", 8) == 0) {
        if (g_uiHostWnd != nullptr) PostMessageW(g_uiHostWnd, kUiOpenSettingsMsg, 0, 0);
        return std::string();
    }
    // 其它命令（含 quit）一律忽略：管道对低完整性进程开放，不能提供关闭 UI 的入口。
    return std::string();
}

// 设置权威中英状态（只在 UI 主线程调用）：托盘图标 + HKCU 一起更新。
static void uiApplyEnglish(bool english) {
    g_uiEnglish = english;
    if (g_uiTray != nullptr) {
        g_uiTray->show(english);
    }
    uiSaveEnglish(english);
}

static LRESULT CALLBACK uiHostWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
        case kUiSetModeMsg:
            // TIP 上报它当前的中英模式。system 作用域下以「最后一次上报」为
            // 权威（用户刚在某个窗口里切过）；process/window 作用域下各窗口
            // 独立，托盘只反映最近活动窗口，不写回权威状态。
            if (g_uiTray != nullptr) {
                const bool english = wParam != 0;
                if (uiLoadModeScope() == ModeScope::System) {
                    uiApplyEnglish(english);
                } else {
                    g_uiTray->show(english);  // 只在 UI 主线程碰托盘
                }
            }
            return 0;
        case kUiToggleModeMsg:
            // 托盘左键 / 管道 "toggle"：翻转权威状态。
            uiApplyEnglish(!g_uiEnglish);
            return 0;
        case kUiOpenSettingsMsg:
            openSettingsWindowStandalone(SettingsPage::Input);
            return 0;
        case kUiOpenSettingsAboutMsg:
            openSettingsWindowStandalone(SettingsPage::About);
            return 0;
        case WM_CLOSE:
            DestroyWindow(hwnd);
            return 0;
        case WM_DESTROY:
            g_uiHostWnd = nullptr;
            PostQuitMessage(0);
            return 0;
        default:
            break;
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

// 管道安全描述符：与 daemon 的 ipc/pipe_sddl.go 完全同规则。
//   D:P(A;;GA;;;SY)                         SYSTEM 完全控制
//       (A;;GA;;;<当前用户 SID>)             当前用户完全控制
//       (A;;GRGW;;;S-1-15-2-1)              ALL APPLICATION PACKAGES 读写
//   S:(ML;;NW;;;LW)                         低完整性 NO_WRITE_UP
// 后两者缺一不可：AppContainer 宿主（开始菜单/搜索）是低完整性，缺标签会
// 「能连不能写」。设置 SACL 失败时降级为仅 DACL（daemon 同样降级并记录）。
static PSECURITY_DESCRIPTOR uiPipeSecurityDescriptor() {
    const std::wstring sid = currentUserSid();
    if (sid.empty()) return nullptr;
    PSECURITY_DESCRIPTOR sd = nullptr;
    const std::wstring withLabel = L"D:P(A;;GA;;;SY)(A;;GA;;;" + sid +
                                   L")(A;;GRGW;;;S-1-15-2-1)S:(ML;;NW;;;LW)";
    if (ConvertStringSecurityDescriptorToSecurityDescriptorW(withLabel.c_str(), SDDL_REVISION_1,
                                                            &sd, nullptr)) {
        return sd;
    }
    const std::wstring daclOnly =
        L"D:P(A;;GA;;;SY)(A;;GA;;;" + sid + L")(A;;GRGW;;;S-1-15-2-1)";
    if (ConvertStringSecurityDescriptorToSecurityDescriptorW(daclOnly.c_str(), SDDL_REVISION_1,
                                                            &sd, nullptr)) {
        if (diagBudget()) {
            diagPrintf(L"tsf.log",
                       "ui pipe: integrity label failed, AppContainer hosts may be unable to write\\n");
        }
        return sd;
    }
    return nullptr;
}

// 命令管道服务线程：命令都是几十字节的短连接（TIP → "mode 0/1"、"settings"）。
static DWORD WINAPI uiPipeThreadProc(LPVOID) {
    const std::wstring& name = uiPipeName();
    if (name.empty()) return 1;
    PSECURITY_DESCRIPTOR sd = uiPipeSecurityDescriptor();
    SECURITY_ATTRIBUTES sa{};
    sa.nLength = sizeof(sa);
    sa.lpSecurityDescriptor = sd;
    for (;;) {
        if (WaitForSingleObject(g_uiStopEvent, 0) == WAIT_OBJECT_0) break;
        // PIPE_ACCESS_DUPLEX（2026-11-06）：UI 宿主不只是被动接收命令，还要能
        // 应答——托盘左键切中英后，调用方（TIP）需要知道切换后的状态；
        // system 作用域下也要把权威状态回给查询方。仍用「短连接 + 请求/应答」
        // 而非长连接：单线程 accept 循环不能被一条连接占死。
        HANDLE pipe = CreateNamedPipeW(
            name.c_str(), PIPE_ACCESS_DUPLEX,
            PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS,
            PIPE_UNLIMITED_INSTANCES, 1024, 1024, 0, sd != nullptr ? &sa : nullptr);
        if (pipe == INVALID_HANDLE_VALUE) {
            Sleep(500);
            continue;
        }
        // 阻塞等待连接；退出时主线程用 CancelSynchronousIo 打断本线程的同步 I/O。
        const BOOL connected = ConnectNamedPipe(pipe, nullptr);
        if (WaitForSingleObject(g_uiStopEvent, 0) == WAIT_OBJECT_0) {
            CloseHandle(pipe);
            break;
        }
        if (!connected && GetLastError() != ERROR_PIPE_CONNECTED) {
            CloseHandle(pipe);
            Sleep(50);
            continue;
        }
        // 读命令：客户端必须在 ~1.5s 内发完，防止「连上不发数据」长期占住
        // 单线程服务（管道名可猜，任意本机进程都能连）。
        char buffer[512];
        DWORD read = 0;
        const DWORD deadline = GetTickCount() + 1500;
        for (;;) {
            if (WaitForSingleObject(g_uiStopEvent, 0) == WAIT_OBJECT_0) break;
            DWORD available = 0;
            if (!PeekNamedPipe(pipe, nullptr, 0, nullptr, &available, nullptr)) break;
            if (available > 0) {
                if (ReadFile(pipe, buffer, sizeof(buffer) - 1, &read, nullptr) && read > 0) {
                    buffer[read] = '\0';
                    const std::string reply = uiHandleCommand(buffer, read);
                    // 有应答就回写（双向管道）。调用方按需读；不读也不会卡住
                    // 服务端：写失败/管道已关都只当次失败。
                    if (!reply.empty()) {
                        DWORD written = 0;
                        WriteFile(pipe, reply.data(), static_cast<DWORD>(reply.size()),
                                  &written, nullptr);
                    }
                }
                break;
            }
            if (GetTickCount() > deadline) break;
            Sleep(10);
        }
        DisconnectNamedPipe(pipe);
        CloseHandle(pipe);
    }
    if (sd != nullptr) LocalFree(sd);
    return 0;
}

// UI 宿主主函数（由导出 ChaosetRunUi 调用，运行在 chaoset-ui.exe 进程里）。
static int runUiHost(LPCWSTR cmdLine) {
    const std::wstring sid = currentUserSid();
    // 文件对话框（词库导入/学习数据导出）与主题查询需要 Apartment；
    // 失败不影响托盘指示器与设置窗主体。
    const HRESULT hrCo = CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
    // UI 宿主也是进程：崩溃同样要留下 crash.log（此前只有 TIP 的 Activate 装 VEH）。
    ensureCrashHandler();
    g_uiStopEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);

    // 单实例互斥体（名字带会话 ID，见 uiPipeName 注释）。
    HANDLE mutex = nullptr;
    const std::wstring mutexName = uiMutexName();
    if (!mutexName.empty()) {
        mutex = CreateMutexW(nullptr, FALSE, mutexName.c_str());
        if (mutex != nullptr && GetLastError() == ERROR_ALREADY_EXISTS) {
            // 已有实例：带 --settings 时把请求转发给首实例。首实例可能还没开始
            // 监听管道（互斥体在管道线程之前创建），这里做 ~2s 的短重试。
            const bool wantSettings =
                cmdLine != nullptr && wcsstr(cmdLine, L"--settings") != nullptr;
            if (wantSettings) {
                bool sent = false;
                for (int i = 0; i < 100 && !sent; ++i) {
                    sent = uiSendCommand("settings", 1);
                    if (!sent) Sleep(20);
                }
                if (!sent) {
                    diagPrintf(L"tsf.log", "ui host: forward settings failed after retry\\n");
                }
            }
            CloseHandle(mutex);
            if (g_uiStopEvent != nullptr) CloseHandle(g_uiStopEvent);
            if (SUCCEEDED(hrCo)) CoUninitialize();
            return 0;
        }
    }

    // 隐藏消息窗口：托盘回调、模式更新、打开设置请求、退出都走这里。
    const std::wstring cls = windowClassName(L"ChaosetUiHostWndClass", g_hInst);
    WNDCLASSEXW wc{};
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = uiHostWndProc;
    wc.hInstance = g_hInst;
    wc.lpszClassName = cls.c_str();
    RegisterClassExW(&wc);
    g_uiHostWnd = CreateWindowExW(0, cls.c_str(), L"ChaosetUiHost", WS_POPUP, 0, 0, 0, 0,
                                  nullptr, nullptr, g_hInst, nullptr);
    if (g_uiHostWnd == nullptr) {
        if (mutex != nullptr) CloseHandle(mutex);
        if (g_uiStopEvent != nullptr) CloseHandle(g_uiStopEvent);
        if (SUCCEEDED(hrCo)) CoUninitialize();
        return 1;
    }

    // 托盘中英指示器：整个用户会话唯一。恢复上次模式；TIP 焦点/切换时上报。
    g_uiTray = new UiTray();
    // 权威状态初始化：优先 HKCU 记忆（上次托盘显示的状态），否则中文。
    g_uiEnglish = uiLoadSavedEnglish();
    g_uiTray->show(g_uiEnglish);

    // 先启动管道线程，再处理 --settings：否则二次实例的转发请求会赶在监听前
    // 到达（虽然它自身有重试，这里尽量避免）。
    HANDLE pipeThread = CreateThread(nullptr, 0, uiPipeThreadProc, nullptr, 0, nullptr);
    if (pipeThread == nullptr) {
        diagPrintf(L"tsf.log", "ui host: create pipe thread FAILED\\n");
        delete g_uiTray;
        g_uiTray = nullptr;
        DestroyWindow(g_uiHostWnd);
        if (mutex != nullptr) CloseHandle(mutex);
        if (g_uiStopEvent != nullptr) CloseHandle(g_uiStopEvent);
        if (SUCCEEDED(hrCo)) CoUninitialize();
        return 1;
    }

    // 首次带 --settings 启动（TIP 侧连不上管道时的拉起路径）：直接弹设置窗。
    if (cmdLine != nullptr && wcsstr(cmdLine, L"--settings") != nullptr) {
        Sleep(30);  // 给管道线程一个创建实例的机会
        openSettingsWindowStandalone();
    }

    MSG msg;
    while (GetMessageW(&msg, nullptr, 0, 0) > 0) {
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }

    // 退出顺序（评审 P0）：先停管道线程并 join，再销毁托盘/窗口。
    InterlockedExchange(&g_uiStopping, 1);
    if (g_uiStopEvent != nullptr) SetEvent(g_uiStopEvent);
    CancelSynchronousIo(pipeThread);
    WaitForSingleObject(pipeThread, 3000);  // 阻塞在 ConnectNamedPipe 时由 Cancel 打断
    CloseHandle(pipeThread);
    g_uiHostWnd = nullptr;
    if (g_uiTray != nullptr) {
        delete g_uiTray;
        g_uiTray = nullptr;
    }
    if (g_uiStopEvent != nullptr) {
        CloseHandle(g_uiStopEvent);
        g_uiStopEvent = nullptr;
    }
    if (mutex != nullptr) CloseHandle(mutex);
    if (SUCCEEDED(hrCo)) CoUninitialize();
    return 0;
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

// 前台钩子回调需要拿到「发起注册的 TIP 实例」：OUTOFCONTEXT 钩子的回调在
// 注册它的线程上派发，所以用 thread_local 传递即可，无需加锁或全局容器。
thread_local std::vector<ChaosetTIP::HookEntry> ChaosetTIP::hookRegistry_;

}  // namespace

// ---- 导出 ----

// chaoset-ui.exe 的唯一入口：宿主托盘中英指示器 + 设置窗 + 命名管道服务。
// 实现全部在本 DLL 内（设置窗代码/托盘绘制/管道），exe 只是 LoadLibrary 壳。
extern "C" __declspec(dllexport) int WINAPI ChaosetRunUi(HINSTANCE /*exeInst*/,
                                                         LPCWSTR cmdLine) {
    return runUiHost(cmdLine);
}

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
        UnregisterClassW(windowClassName(L"ChaosetTrayWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetUiHostWndClass", hinstDLL).c_str(), hinstDLL);
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
    if (g_liveToastWindows != 0) return S_FALSE;
    if (g_liveTrayWindows != 0) return S_FALSE;
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

// 旧版（≤v0.2.16）注册过 UILess/UIELEMENTENABLED 类别；v0.2.17 起本壳不再
// 声明该类别（不实现 ITfUIElementMgr）。NSIS 安装器有 DeleteRegKey 清理，
// 但便携 ZIP 走 regsvr32 → DllRegisterServer 的路径没有清理步骤，升级用户
// 会残留该类别，开始菜单/搜索继续按 UILess 模式期待宿主渲染候选窗
//（2026-10-31 审计 A2）。这里在每个 DLL 注册时幂等清掉自己注册表视图下的
// 残留：64 位 DLL 清 64 位视图，32 位 DLL（WOW64）清 32 位视图。
static void removeLegacyUILessCategory() {
    const GUID kLegacyUIElementEnabled = {
        0x49d2f9cf, 0x1f5e, 0x11d7, {0xa6, 0xd3, 0x00, 0x06, 0x5b, 0x84, 0x43, 0x5c}};
    // 1) 经类别管理器注销（若 COM 已初始化；失败不影响后续注册）
    ITfCategoryMgr* catMgr = nullptr;
    if (SUCCEEDED(CoCreateInstance(kCLSID_TF_CategoryMgr, nullptr, CLSCTX_INPROC_SERVER,
                                   kIID_ITfCategoryMgr, reinterpret_cast<void**>(&catMgr)))) {
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kLegacyUIElementEnabled, CLSID_ChaosetTIP);
        catMgr->Release();
    }
    // 2) 直接删注册表残留（类别管理器注销在部分环境可能不落盘；幂等兜底）
    std::string base = "Software\\Microsoft\\CTF\\TIPs\\" + guidToString(CLSID_ChaosetTIP) +
                       "\\Category\\";
    const std::string cat = guidToString(kLegacyUIElementEnabled);
    RegDeleteTreeA(HKEY_LOCAL_MACHINE, (base + "Category\\" + cat).c_str());
    RegDeleteTreeA(HKEY_LOCAL_MACHINE, (base + "Item\\" + cat).c_str());
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

    // 升级清理：便携 ZIP（install.bat → regsvr32）路径没有 NSIS 的
    // DeleteRegKey 步骤，必须在注册时自己清掉旧版残留的 UILess 类别（A2）。
    removeLegacyUILessCategory();

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
        // 不注册 UIELEMENTENABLED：本壳不实现 ITfUIElementMgr（见常量处注释）
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
        // 不含 UIELEMENTENABLED：本壳不实现 ITfUIElementMgr（见常量处注释）
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
        // 注销旧版注册过的 UILess 类别（本版不再注册，但升级用户的注册表里
        // 仍残留，卸载必须清干净；GUID 直接内联，常量已随修复删除）。
        const GUID kLegacyUIElementEnabled = {
            0x49d2f9cf, 0x1f5e, 0x11d7, {0xa6, 0xd3, 0x00, 0x06, 0x5b, 0x84, 0x43, 0x5c}};
        catMgr->UnregisterCategory(CLSID_ChaosetTIP, kLegacyUIElementEnabled, CLSID_ChaosetTIP);
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
