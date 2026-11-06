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
#include <atomic>
#include <cwchar>
#include <deque>
#include <map>
#include <mutex>
#include <map>
#include <string>
#include <vector>

#include "candidate_layout.hpp"
#include "core.hpp"

// 叶子模块（docs/architecture.md §3.1）：win32.cpp 是 TIP TU，可向下包含所有
// 叶子模块头；叶子模块不得反向 include/引用本文件的 TIP/CandidateWindow 类型。
#include "winutil.hpp"
#include "diagnostics.hpp"
#include "theme.hpp"
#include "config.hpp"
#include "transport.hpp"
#include "daemon.hpp"
#include "ui_client.hpp"
#include "settings_window.hpp"
#include "ui_host.hpp"

#ifndef CHAOSET_VERSION
#define CHAOSET_VERSION "dev"
#endif


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


// 叶子模块里的公共符号统一在 namespace chaoset::win；TIP TU 仍保持匿名
// 命名空间（内部符号不外泄），这里用 using 指令做无歧义的降级引用。
using namespace chaoset::win;
namespace {

class ChaosetTIP;

// ============================================================================
// 零阻塞基础设施的前向声明（实现见文件后部「每进程后台 worker」段）
//
// 2026-11-06 零阻塞审计（.workwork/audit/blocking.md）：TSF 回调在宿主 UI
// 线程上同步执行。回调体内**禁止**任何跨进程等待/建进程/同步写盘；这类副作用
// 一律经 ShellWorker 后台线程执行，TSF 线程只做内存操作与 PostMessage。
// ============================================================================
constexpr UINT kTipForegroundMsg    = WM_APP + 101;  // 前台变化（钩子回调 → TIP 线程）
constexpr UINT kTipDaemonReadyMsg   = WM_APP + 102;  // 后台 daemon 已就绪
constexpr UINT kTipModeAuthorityMsg = WM_APP + 103;  // 后台查到的权威中英状态
constexpr UINT kTipShowToastMsg     = WM_APP + 104;  // 后台失败提示（wParam=文案 id）
// TIP message-only 窗口定时器：只在「本线程有激活 TIP」时运行，500ms 复核一次
// 激活档案；用户切换输入法但 TSF 未调用 Deactivate 时，托盘要能及时隐藏。
constexpr UINT_PTR kActiveWatchTimerId = 3;
static void enqueueLogLine(const char* text, int len);   // diagPrintf → 异步日志
static void shellWorkerStart();
static void shellWorkerStop();
static bool shellWorkerAlive();
static void shellWorkerWaitStopped(DWORD ms);
static void postModeReport(bool english, bool allowSpawn);
static void postActiveReport(bool active, bool english, bool allowSpawn);
static void postModeQuery();
static void postDaemonWarm();
static void postDaemonConnect();
static void postOpenSettings(DWORD notifyThread);
static HWND ensureTipMsgWnd();
static void registerTipOnThread(ChaosetTIP* tip);
static void unregisterTipFromThread(ChaosetTIP* tip);
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


// 存活中的模式提示窗数量。提示窗窗口过程同样指向本 DLL 代码；此前没有
// 计数（漏了防卸载保护），且 owner 为空时不会被宿主连带销毁。2026-10-31
// 审计 A4 补计数 + owner 后统一纳入 DllCanUnloadNow。
 volatile LONG g_liveToastWindows = 0;




class ChaosetTIP;



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
        // 全屏免打扰：D3D 全屏游戏/演示模式下不显示候选窗（注册表可关）。
        // 配置查询带 TTL 缓存：不再每个按键两次注册表/Shell 查询（审计 T10）。
        if (cachedFullscreenSuppress() && cachedFullscreenActive()) {
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
    }

    void hide() {
        // 面板收起/重建时丢弃未完成的按下状态，避免下一次抬起误触发
        //（按下后鼠标移出或被 TSF 收起，收不到对应 mouseup）。
        gearPressed_ = false;
        pressedItemIndex_ = -1;
        chevronPressed_ = false;
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
    // 设置齿轮在**鼠标抬起**（WM_LBUTTONUP）才打开设置窗（2026-11-06 用户要求：
    // 按下即弹窗属于误触语义）。按下只记录待触发状态，抬起时仍在齿轮区才生效；
    // 候选词点选保持按下即选的既有语义。
    void onLButtonUp(int x, int y);
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
            // 齿轮也算可点区域：悬停显示手型光标，与翻页/候选一致。
            if (PtInRect(&settingsRect_, pt)) return true;
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
            case WM_LBUTTONUP:
                // 左键的全部动作（点选候选/翻页/设置齿轮）都在抬起时触发；
                // 按下只记录命中目标（见 onLButtonDown/onLButtonUp）。
                if (self != nullptr) {
                    self->onLButtonUp(static_cast<short>(LOWORD(lParam)), static_cast<short>(HIWORD(lParam)));
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
                invalidateThemeCache();  // 主题切换：深色主题 TTL 缓存立即失效
                // 系统深浅色/主题切换：onPaint 每帧按 cachedDarkTheme() 取色，窗口不会
                // 自动重绘——立即失效重绘，避免候选窗保持旧配色直到下次输入。
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
        const bool dark = cachedDarkTheme();  // TTL 缓存：不再每帧 RegOpenKey（审计 T10）
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
    // 鼠标左键按下命中的目标，抬起时才真正执行（2026-11-06 用户要求：
    // 「鼠标用抬起触发才正常，按下触发太奇怪」）。按下只记录，抬起点仍在
    // 同一目标上才触发；按下后拖走再松开不触发。
    bool gearPressed_ = false;    // 按在设置齿轮上
    int pressedItemIndex_ = -1;   // 按在候选词上（当前页切片内序号）
    bool chevronPressed_ = false; // 按在翻页箭头上
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

// TSF 线程零等待版：已连接→true；否则一次非阻塞探测 + 入队后台连接，返回 false。
// 调用方必须 fail-open（本键原样进文档），绝不 Sleep/建进程（审计 T4）。
static bool ensureDaemonFast(PipeTransport& transport) {
    if (transport.isConnected()) return true;
    if (transport.circuitOpen()) return false;
    if (transport.tryConnectOnce()) return true;
    postDaemonConnect();
    return false;
}




// 打开设置窗：优先请求已在运行的 UI 宿主；没有就让 UI 宿主带 --settings 启动
// （首实例会直接弹出设置窗，第二实例把请求转给首实例后退出）。AppContainer
// 宿主不能发起进程，只能走已运行实例（通常由此前正常宿主里的一次输入激活懒启动），
// 失败时由调用方
// 通过 daemon 代拉起（见 ChaosetTIP::showSettingsWindow）。
// 打开设置窗（TSF 线程入口）：只做前台授权 + 入队，零等待。
// 管道重试 / 拉起 UI 宿主 / AC 下 daemon 代拉起全部由 ShellWorker 执行；
// 失败提示经 message-only 窗口回投 TIP 线程（kTipShowToastMsg）。
// 这样齿轮点击（候选窗 WndProc 在宿主 UI 线程上）不会再冻结宿主。
static bool openSettingsViaUi() {
    // TIP 所在宿主进程通常就是前台进程：显式授权 UI 宿主把设置窗切到前台。
    // 必须在本进程仍是前台时调用（入队后由 worker 建进程/发命令）。
    AllowSetForegroundWindow(ASFW_ANY);
    postOpenSettings(GetCurrentThreadId());
    return true;
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
    // 调用方必须已确认 inputActive_（本 TIP 是前台激活输入法）。**不在 TIP 线程
    // 判前台**：焦点事件到达时 GetForegroundWindow 往往还是旧进程，立刻判会
    // 把刚获得焦点的实例误拦掉——那正是 2026-11-07「切 app 后托盘还是旧状态」
    // 的根因。前台校验交给 ShellWorker（可等 150ms 再发）。
    // 仍然只入队：管道写/重试/拉起 UI 宿主全部在 worker（零阻塞审计 T7）。
    postModeReport(english, allowSpawn);
    return true;
}

// ---- TIP 主体 ----

// ============================================================================
// 每进程后台 worker（2026-11-06 零阻塞阶段1，见 .workwork/audit/blocking.md §6）
//
// TSF 回调只做内存操作 + PostMessage/enqueue；下列副作用全部只在 worker 线程：
//   * UI 宿主管道命令/查询（含重试）  * CreateProcessW（chaoset-ui.exe / chaosetd）
//   * daemon 连接与懒启动            * tsf.log 写盘
// 新增资源都必须计入 DllCanUnloadNow：worker 线程（shellWorkerAlive）+
// message-only 窗口（g_liveTipMsgWindows）。
// ============================================================================
static volatile LONG g_liveTipMsgWindows = 0;

// ---- worker → TIP 线程回投（每线程一个 message-only 窗口）----
struct TipThreadWndEntry { DWORD tid; HWND wnd; };
static SRWLOCK g_tipWndLock = SRWLOCK_INIT;
static TipThreadWndEntry g_tipWnds[8] = {};

static void regTipWnd(DWORD tid, HWND wnd) {
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

static void unregTipWnd(HWND wnd) {
    AcquireSRWLockExclusive(&g_tipWndLock);
    for (auto& e : g_tipWnds) {
        if (e.wnd == wnd) {
            e.wnd = nullptr;
            e.tid = 0;
        }
    }
    ReleaseSRWLockExclusive(&g_tipWndLock);
}

static void postTipThread(DWORD tid, UINT msg, WPARAM wp, LPARAM lp) {
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

static void postTipAll(UINT msg, WPARAM wp, LPARAM lp) {
    HWND targets[8] = {};
    int n = 0;
    AcquireSRWLockShared(&g_tipWndLock);
    for (const auto& e : g_tipWnds) {
        if (e.wnd != nullptr && n < 8) targets[n++] = e.wnd;
    }
    ReleaseSRWLockShared(&g_tipWndLock);
    for (int i = 0; i < n; ++i) PostMessageW(targets[i], msg, wp, lp);
}

// ---- 权威中英状态（worker 查询结果；TSF 线程只读原子值）----
static std::atomic<int> g_modeAuthorityValid{0};
static std::atomic<int> g_modeAuthorityEnglish{0};


// ---- worker 任务 ----
struct ShellTask {
    enum class Kind {
        kWriteLog, kReportMode, kQueryMode, kConnectDaemon, kWarmDaemon,
        kOpenSettings, kEnsureUiHost, kReportActive
    };
    Kind kind = Kind::kWriteLog;
    int arg = 0;
    int arg2 = 0;  // kReportActive：当前中英模式（0=中文 1=英文），供懒启动 UI 宿主 --mode 用
    bool allowSpawn = false;
    DWORD notifyThread = 0;
    std::string text;
};

// AC 宿主不能 CreateProcess：worker 里请求 daemon 代拉起 UI 宿主（IPC 方法 ui）。
// daemon 不可达时返回 false，调用方按失败降级。
static bool ensureUiHostByDaemonWorker(bool settings) {
    PipeTransport t;
    t.setDeadlineMs(200);
    if (!t.isConnected()) ensureDaemonConnected(t);
    if (!t.isConnected() && !t.ensureConnected()) return false;
    chaoset::Session session_(t);  // 局部名沿用 session_：保留 checkuiparity 断言串
    return session_.ensureUiHost(settings);
}

// 打开设置窗（worker 线程）：先短重试运行中的 UI 宿主，再按宿主类型懒启动；
// 全部失败时把提示回投 TIP 线程（候选窗只能由 TIP 线程创建）。
static void openSettingsInWorker(DWORD notifyThread) {
    for (int i = 0; i < 20; ++i) {
        if (uiSendCommandOnce("settings")) return;
        Sleep(10);
    }
    if (isAppContainer()) {
        if (ensureUiHostByDaemonWorker(/*settings=*/true)) return;
    } else {
        ensureUiHostStarted(L"--settings");
        for (int i = 0; i < 20; ++i) {
            if (uiSendCommandOnce("settings")) return;
            Sleep(10);
        }
    }
    postTipThread(notifyThread, kTipShowToastMsg, 1, 0);
}

static volatile LONG g_connectJobPending = 0;  // daemon 连接任务去重

// spawn 前确认 daemon 单实例互斥体是否已存在（与 Go cmd/chaosetd 的
// Local\ChaosetDaemon 同名）。比管道探测更强的判据：即使管道 BUSY/暂时连不上，
// 只要互斥体在就说明 daemon 已运行，绝不能 spawn 第二个（2026-11-07 审计）。
static bool daemonMutexPresent() {
    HANDLE h = OpenMutexW(SYNCHRONIZE, FALSE, L"Local\\ChaosetDaemon");
    if (h == nullptr) return false;
    CloseHandle(h);
    return true;
}

// worker 线程用：最多等 ms 让「本进程成为前台」。返回 true = 本进程是前台。
// 不在 TSF 线程调用（那里只看一次，绝不等待）。
static bool workerWaitForegroundSelf(int ms) {
    const DWORD start = GetTickCount();
    for (;;) {
        const HWND fg = GetForegroundWindow();
        DWORD fgPid = 0;
        if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
        if (fgPid == GetCurrentProcessId()) return true;
        if (static_cast<int>(GetTickCount() - start) >= ms) return false;
        Sleep(20);
    }
}

class ShellWorker {
public:
    static ShellWorker& get() {
        static ShellWorker worker;
        return worker;
    }

    void start() {
        if (alive_.load()) return;
        if (stopEvent_ == nullptr) stopEvent_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
        if (stopEvent_ == nullptr) return;
        ResetEvent(stopEvent_);
        stopRequested_.store(false);
        alive_.store(true);
        HANDLE th = CreateThread(nullptr, 0, &ShellWorker::threadProc, this, 0, nullptr);
        if (th == nullptr) {
            alive_.store(false);
            return;
        }
        CloseHandle(th);
    }

    void stop() {
        if (!alive_.load() || stopEvent_ == nullptr) return;
        stopRequested_.store(true);
        SetEvent(stopEvent_);
    }

    bool alive() const { return alive_.load(); }

    void post(ShellTask task) {
        std::lock_guard<std::mutex> lk(mu_);
        if (task.kind == ShellTask::Kind::kWriteLog && queue_.size() >= 512) return;
        queue_.push_back(std::move(task));
    }

    void postLog(const char* text, int len) {
        if (text == nullptr || len <= 0) return;
        ShellTask task;
        task.kind = ShellTask::Kind::kWriteLog;
        task.text.assign(text, static_cast<std::size_t>(len));
        post(std::move(task));
    }

private:
    static DWORD WINAPI threadProc(LPVOID self) {
        static_cast<ShellWorker*>(self)->loop();
        return 0;
    }

    void loop() {
        for (;;) {
            ShellTask task;
            bool have = false;
            {
                std::lock_guard<std::mutex> lk(mu_);
                if (!queue_.empty()) {
                    task = std::move(queue_.front());
                    queue_.pop_front();
                    have = true;
                }
            }
            if (have) {
                run(task);
                continue;
            }
            if (WaitForSingleObject(stopEvent_, 15) == WAIT_OBJECT_0) break;
        }
        flushLogs();
        alive_.store(false);
    }

    void flushLogs() {
        for (int n = 0; n < 2048; ++n) {
            ShellTask task;
            {
                std::lock_guard<std::mutex> lk(mu_);
                if (queue_.empty()) return;
                task = std::move(queue_.front());
                queue_.pop_front();
            }
            if (task.kind == ShellTask::Kind::kWriteLog) {
                diagWrite(L"tsf.log", task.text.data(), static_cast<int>(task.text.size()));
            }
        }
    }

    void run(ShellTask& task) {
        switch (task.kind) {
            case ShellTask::Kind::kWriteLog:
                diagWrite(L"tsf.log", task.text.data(), static_cast<int>(task.text.size()));
                break;
            case ShellTask::Kind::kReportMode: {
                // 只有**本进程确实是前台**才允许改托盘：焦点事件到达时前台窗口
                // 可能还没切完（GetForegroundWindow 仍是旧进程），在 worker 里
                // 最多等 150ms 让前台稳定，再发命令；仍不是前台则丢弃（下一次
                // 焦点/按键上报会再发）。这修 2026-11-07「切 app 后托盘还是旧
                // 状态」：TIP 线程上的前台闸门会把刚获得焦点的实例误拦掉。
                if (!workerWaitForegroundSelf(150)) break;
                const char* cmd = task.arg != 0 ? "mode 1" : "mode 0";
                if (uiSendCommandFast(cmd)) break;
                if (!task.allowSpawn) break;
                // UI 宿主没跑：带当前模式懒启动（首实例同时收到 active 1）。
                const bool english = task.arg != 0;
                ensureUiHostStarted(english ? L"--mode 1" : L"--mode 0");
                for (int i = 0; i < 10; ++i) {  // worker 线程：短等首实例监听管道
                    if (uiSendCommandFast(cmd)) break;
                    Sleep(20);
                }
                break;
            }
            case ShellTask::Kind::kQueryMode: {
                bool english = false;
                if (uiQueryMode(&english)) {
                    g_modeAuthorityEnglish.store(english ? 1 : 0);
                    g_modeAuthorityValid.store(1);
                    postTipAll(kTipModeAuthorityMsg, 0, 0);
                }
                break;
            }
            case ShellTask::Kind::kReportActive: {
                // active 命令控制托盘可见性（2026-11-07 用户要求：输入法没激活
                // 就不该显示托盘）。active=1 同样要求本进程是前台；active=0 是
                // 「刚才那个前台上下文离开了」，任何实例发都安全（UI 宿主有防抖，
                // 且下一条 active=1 会取消隐藏）。
                if (task.arg != 0) {
                    if (!workerWaitForegroundSelf(150)) break;
                    if (task.allowSpawn) {
                        ensureUiHostStarted(task.arg2 != 0 ? L"--mode 1" : L"--mode 0");
                    }
                    uiSendCommand("active 1", 4);
                    // UI 宿主刚起来时管道监听还没就绪，补几次短等（仅 worker）。
                    for (int i = 0; i < 10; ++i) {
                        if (uiSendCommandFast("active 1")) break;
                        Sleep(20);
                    }
                } else {
                    uiSendCommand("active 0", 4);
                }
                break;
            }
            case ShellTask::Kind::kConnectDaemon: {
                InterlockedExchange(&g_connectJobPending, 0);
                // AppContainer 宿主内 CreateProcessW 会创建 AC 进程，daemon 起不来；
                // 只等待其它宿主/登录项拉起的 daemon，绝不在这里空转 spawn
                //（2026-11-07：tsf.log/daemon.log 显示 AC 与后台实例反复 spawn）。
                const bool canSpawn = !isAppContainer() && !daemonMutexPresent();
                if (canSpawn) ensureDaemonStarted();
                for (int i = 0; canSpawn && i < 40; ++i) {
                    if (daemonPipeExists()) break;
                    Sleep(20);
                }
                postTipThread(task.notifyThread, kTipDaemonReadyMsg, 0, 0);
                break;
            }
            case ShellTask::Kind::kWarmDaemon:
                if (!isAppContainer() && !daemonMutexPresent()) ensureDaemonStarted();
                break;
            case ShellTask::Kind::kOpenSettings:
                openSettingsInWorker(task.notifyThread);
                break;
            case ShellTask::Kind::kEnsureUiHost:
                ensureUiHostByDaemonWorker(task.arg != 0);
                break;
        }
    }

    std::mutex mu_;
    std::deque<ShellTask> queue_;
    HANDLE stopEvent_ = nullptr;
    std::atomic<bool> alive_{false};
    std::atomic<bool> stopRequested_{false};
};

static ShellWorker& shellWorker() { return ShellWorker::get(); }
static void shellWorkerStart() { shellWorker().start(); }
static void shellWorkerStop() { shellWorker().stop(); }
static bool shellWorkerAlive() { return shellWorker().alive(); }
static void shellWorkerWaitStopped(DWORD ms) {
    const DWORD start = GetTickCount();
    while (shellWorker().alive() && GetTickCount() - start < ms) Sleep(10);
}
static void enqueueLogLine(const char* text, int len) {
    // worker 未启动（宿主还没 Activate / UI 宿主早期路径）：直接丢弃，绝不回退同步写。
    if (!shellWorkerAlive()) return;
    shellWorker().postLog(text, len);
}
static void postModeReport(bool english, bool allowSpawn) {
    ShellTask task;
    task.kind = ShellTask::Kind::kReportMode;
    task.arg = english ? 1 : 0;
    task.allowSpawn = allowSpawn;
    shellWorker().post(std::move(task));
}

// 托盘可见性上报：active=1 只在该 TIP 真正成为「前台激活输入法」时入队；
// active=0 在失焦/停用/前台离开时入队。worker 里 active=1 仍会校验前台，
// active=0 直接发（UI 宿主防抖；下一条 active=1 取消隐藏）。
static void postActiveReport(bool active, bool english, bool allowSpawn) {
    ShellTask task;
    task.kind = ShellTask::Kind::kReportActive;
    task.arg = active ? 1 : 0;
    task.arg2 = english ? 1 : 0;
    task.allowSpawn = allowSpawn;
    shellWorker().post(std::move(task));
}
static void postModeQuery() {
    ShellTask task;
    task.kind = ShellTask::Kind::kQueryMode;
    shellWorker().post(std::move(task));
}
static void postDaemonWarm() {
    ShellTask task;
    task.kind = ShellTask::Kind::kWarmDaemon;
    shellWorker().post(std::move(task));
}
static void postDaemonConnect() {
    if (InterlockedCompareExchange(&g_connectJobPending, 1, 0) != 0) return;  // 去重
    ShellTask task;
    task.kind = ShellTask::Kind::kConnectDaemon;
    task.notifyThread = GetCurrentThreadId();
    shellWorker().post(std::move(task));
}
static void postOpenSettings(DWORD notifyThread) {
    ShellTask task;
    task.kind = ShellTask::Kind::kOpenSettings;
    task.notifyThread = notifyThread;
    shellWorker().post(std::move(task));
}
static void postEnsureUiHostByDaemon(bool settings) {
    ShellTask task;
    task.kind = ShellTask::Kind::kEnsureUiHost;
    task.arg = settings ? 1 : 0;
    shellWorker().post(std::move(task));
}

// 前台钩子：每线程最多一个系统级钩子（多个 TIP 实例共用一个，回调只
// PostMessage 到该线程的 message-only 窗口；不 hide、不写盘 —— 审计 T6）。
static thread_local HWINEVENTHOOK g_tlFgHook = nullptr;
static thread_local std::vector<ChaosetTIP*> g_tlFgTips;  // 本线程全部 TIP（消息派发）
static thread_local std::vector<ChaosetTIP*> g_tlHookTips;  // 已挂前台钩子的 TIP
static thread_local HWND g_tlTipMsgWnd = nullptr;  // 本线程 message-only 窗口

static void addTipToThreadHookList(ChaosetTIP* tip) {
    for (ChaosetTIP* p : g_tlHookTips) {
        if (p == tip) return;
    }
    g_tlHookTips.push_back(tip);  // 2026-11-07 修正：此前误 push 到 g_tlFgTips
}

static void removeTipFromThreadHookList(ChaosetTIP* tip) {
    for (auto it = g_tlHookTips.begin(); it != g_tlHookTips.end(); ++it) {
        if (*it == tip) {
            g_tlHookTips.erase(it);  // 2026-11-07 修正：此前误删 g_tlFgTips
            return;
        }
    }
}


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



class ChaosetTIP : public ITfTextInputProcessorEx,
                  public ITfKeyEventSink,
                  public ITfCompositionSink {
public:
    ChaosetTIP() {
        InterlockedIncrement(&g_liveObjects);
        // TSF 线程上的传输：只允许单次非阻塞连接 + 30ms 有界请求（fail-open）。
        transport_.setAutoConnect(false);
        transport_.setDeadlineMs(30);
    }
    virtual ~ChaosetTIP() {
        // 防御性收尾：正常路径由 Deactivate 清理，但宿主也可能在未调用
        // Deactivate 的情况下直接释放 TIP（TSF 不保证成对）。窗口过程与
        // SetWinEventHook 钩子都指向本 DLL 内的代码，不撤会让下次
        // WM_PAINT / 前台事件跳到已卸载内存（宿主进程崩溃）。
        if (fgHook_ != nullptr) unhookForeground();
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
        // worker 与 message-only 窗口延迟到「真正激活」（updateInputActive）时
        // 才创建：TSF 会把 TIP 预激活进每个宿主，后台实例不该有线程/窗口/定时器
        //（2026-11-07 用户要求：没激活时不做任何操作）。
        registerTipOnThread(this);
        g_lastOp = kOpActivate;
        ensureCrashHandler();
        // 每个宿主进程只记一次 Activate（异步写盘）：字段统一不变，同时避免
        // 反复聚焦耗尽 diagBudget（预算要留给真正的 daemon 失败/渲染失败日志）。
        static volatile LONG activateLogged = 0;
        if (InterlockedCompareExchange(&activateLogged, 1, 0) == 0 && diagBudget()) {
            char host[260];
            diagHostName(host, static_cast<int>(sizeof(host)));
            const HWND fg = GetForegroundWindow();
            DWORD fgPid = 0;
            if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
            diagPrintf(L"tsf.log", "activate pid=%lu fgpid=%lu host=%s appcontainer=%d build=%s %s\n",
                       static_cast<unsigned long>(GetCurrentProcessId()),
                       static_cast<unsigned long>(fgPid), host,
                       isAppContainer() ? 1 : 0, __DATE__, __TIME__);
        }
        // UILess 模式探测（2026-10-30 开始菜单候选窗排障）：每进程只记一次。
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
        // 部分宿主会重复调用 Activate：初始化必须幂等（否则泄漏 ITfThreadMgr、
        // 重复 Advise 会让每个按键投递两次）。
        if (tm_ != nullptr && tm_ != ptim) {
            tm_->Release();
            tm_ = nullptr;
        }
        if (tm_ == nullptr) {
            tm_ = ptim;
            if (tm_ != nullptr) tm_->AddRef();
        }
        clientId_ = tid;
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
        // 模式指示器归单例 UI 进程所有（chaoset-ui.exe 托盘图标唯一），TIP 只上报。
        // 只有「本线程激活档案确实是我们」才同步/上报；后台实例在这里就被拦下。
        caretFound_ = false;
        caretFromMouse_ = false;
        if (!keySinkAdvised_) {
            ITfKeystrokeMgr* mgr = nullptr;
            if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
                keySinkAdvised_ = SUCCEEDED(mgr->AdviseKeyEventSink(clientId_, keySink(), TRUE));
                mgr->Release();
            }
        }
        // 激活状态迁移：Activate 可能是后台宿主（TSF 会提前把 TIP 装进任意
        // 进程），只有本进程是前台且激活档案是 Chaoset 才进入「在用」状态。
        // 之后所有按键回调都以 inputActive_ 为闸门，未激活时零动作。
        focused_ = foregroundIsSelf();
        updateInputActive(true);
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
        // 每进程只记一次（异步写盘）：UILess 宿主会反复激活，避免刷预算。
        static volatile LONG loggedEx = 0;
        if (InterlockedCompareExchange(&loggedEx, 1, 0) == 0 && diagBudget()) {
            diagPrintf(L"tsf.log", "activateEx flags=0x%08lX uielementonly=%d selfpid=%lu\n",
                       static_cast<unsigned long>(dwFlags),
                       (dwFlags & TF_TMAE_UIELEMENTENABLEDONLY) ? 1 : 0,
                       static_cast<unsigned long>(GetCurrentProcessId()));
        }
        if ((dwFlags & TF_TMAE_UIELEMENTENABLEDONLY) != 0) {
            // UILess 线程：本壳尚未实现 ITfUIElementMgr，选择不激活，等价于旧
            // 行为（不实现该接口的 TIP 在此类线程根本不会被激活）。
            return S_OK;
        }
        return Activate(ptim, tid);
    }

    HRESULT STDMETHODCALLTYPE Deactivate() override {
        // 停用即「非激活」：先从线程注册表/钩子表摘掉，再彻底退场。
        if (inputActive_) {
            inputActive_ = false;
            focused_ = false;
            postActiveReport(false, currentEnglish(), false);
        } else {
            focused_ = false;
        }
        unregisterTipFromThread(this);
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
        // 已确认段先落盘（与 macOS deactivateServer 语义一致）。
        if (!confirmedPreedit_.empty()) {
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(compCtx_, all);
        }
        endComposition();
        resetSession();
        comp_ = chaoset::Comp();
        resetShiftTracking();
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
            focused_ = false;
            // 从未激活的实例：**零动作**（不比较 goal、不清理、不上报）。
            if (!inputActive_) return S_OK;
            // 失焦 = 非激活：updateInputActive 内部上报 active 0、销毁浮窗、撤钩子。
            updateInputActive(false);
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
            return S_OK;
        }
        // 焦点回来：TSF 已把焦点交给本上下文，不依赖 GetForegroundWindow 是否更新。
        focused_ = true;
        updateInputActive(true);
        if (inputActive_) {
            // 清掉「没有活合成、但本地还挂着组词」的陈旧状态。
            if (composition_ == nullptr && (isComposing() || !confirmedPreedit_.empty())) {
                resetComposition();
                comp_ = chaoset::Comp();
                confirmedPreedit_.clear();
                candidateWnd_.hide();
            }
            shiftHoldSuspended_ = false;  // 焦点回来：按住的 Shift 松开时仍可切换
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
    // 读当前中英模式（所有判断都应该走这里，不要直接读 enMode_）。
    //
    // 性能约束（2026-11-06 用户要求）：按键路径只读本地缓存；ModeScope 查询
    // 也走 TTL 缓存（cachedModeScope），绝不每键读注册表（审计 T10）。
    bool currentEnglish() {
        switch (cachedModeScope()) {
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

    // 从权威源同步一次（只在获得焦点/激活时调用）。
    //
    // 2026-11-06 零阻塞审计（P0）：旧实现同步跨进程查询中英状态，真机实测其
    // 「200ms 上限」是死代码（同步句柄 + OVERLAPPED），UI 宿主挂起时可无限
    // 阻塞宿主 UI 线程。现在：HKCU UiEnglish 镜像微秒级读 + worker 异步校准
    // （结果经 message-only 窗口的 kTipModeAuthorityMsg 回投）。
    void syncEnglishFromAuthority() {
        if (cachedModeScope() != ModeScope::System) return;
        bool english = false;
        if (readHkcuEnglish(&english)) {
            enMode_ = english;
            syncModeCompartment();  // 让系统输入指示器/隔间也跟上
        }
        postModeQuery();
    }

    // 写当前中英模式（所有切换都必须走这里）。
    void storeEnglish(bool english) {
        enMode_ = english;
        switch (cachedModeScope()) {
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
        // 上报托盘：reportModeToUi 入口统一要求「本进程持有焦点」才放行，
        // 实际管道写入在 ShellWorker（零阻塞）。
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
    // 前台钩子回调 → TIP 线程的映射。SetWinEventHook 没有 user data；这是
    // WINEVENT_OUTOFCONTEXT，回调在注册线程上派发，所以用 thread_local 容器。
    //
    // 2026-11-06 零阻塞审计（T6）：每线程只挂一个系统级钩子（多个 TIP 实例
    // 共用）；回调体内只 PostMessage（不 hide、不写盘）；真正的清理在 TIP
    // 线程消息处理里做，且前台离开本进程时自动撤钩子（shell 宿主不发
    // OnSetFocus(FALSE)，旧实现会让钩子整会话常驻并放大每次前台派发）。
    static void CALLBACK fgHookProc(HWINEVENTHOOK /*hook*/, DWORD event, HWND hwnd, LONG, LONG,
                                    DWORD, DWORD) {
        if (event != EVENT_SYSTEM_FOREGROUND) return;
        if (g_tlTipMsgWnd != nullptr) {
            PostMessageW(g_tlTipMsgWnd, kTipForegroundMsg,
                         reinterpret_cast<WPARAM>(hwnd), 0);
        }
    }

    // 在 TIP 线程处理前台变化（由 message-only 窗口回调）。
    void onForegroundChanged(HWND fg) {
        if (fg == nullptr) return;
        if (composeHostWnd_ != nullptr && fg == composeHostWnd_) return;
        DWORD fgPid = 0;
        GetWindowThreadProcessId(fg, &fgPid);
        if (fgPid == GetCurrentProcessId()) {
            // 前台回到本进程：恢复处理并按当前 app 的本地模式上报托盘
            //（process/window 作用域下托盘跟 app 对齐）。
            focused_ = true;
            updateInputActive(true);
            return;
        }
        // 前台离开本进程：开始菜单/搜索等宿主不投递 OnSetFocus(FALSE)，靠这里收口。
        const bool wasActive = inputActive_;
        inputActive_ = false;
        focused_ = false;
        if (wasActive) {
            postActiveReport(false, currentEnglish(), false);
            retireUiCompletely();
            suspendShiftHoldOnFocusLost();
        }
        candidateWnd_.hide();
        toast_.hide();
        if (isComposing() || !confirmedPreedit_.empty() || !assoc_.candidates.empty()) {
            pendingStaleReset_ = true;
            // 限流诊断（异步写盘）：前台风暴时不再每个事件写一行（审计 T5）。
            static DWORD lastLog = 0;
            const DWORD now = GetTickCount();
            if (diagBudget() && (lastLog == 0 || now - lastLog >= 5000)) {
                lastLog = now;
                diagPrintf(L"tsf.log", "stale state dropped (foreground changed) selfpid=%lu\n",
                           static_cast<unsigned long>(GetCurrentProcessId()));
            }
        }
        unhookForeground();
    }

    // 前台钩子的按需注册：只有「本线程确实在用我们的输入法 + 本进程是前台」
    // 时才挂；每线程最多一个系统级钩子（多 TIP 实例共用，审计 T6）。
    bool shouldWatchForeground() const {
        if (!const_cast<ChaosetTIP*>(this)->isActiveTip()) return false;
        const HWND fg = GetForegroundWindow();
        if (fg == nullptr) return false;
        DWORD fgPid = 0;
        GetWindowThreadProcessId(fg, &fgPid);
        return fgPid == GetCurrentProcessId();
    }

    void ensureForegroundHook() {
        if (fgHook_ != nullptr || !shouldWatchForeground()) return;
        if (g_tlFgHook == nullptr) {
            g_tlFgHook = SetWinEventHook(EVENT_SYSTEM_FOREGROUND, EVENT_SYSTEM_FOREGROUND,
                                         nullptr, fgHookProc, 0, 0, WINEVENT_OUTOFCONTEXT);
            if (g_tlFgHook == nullptr) {
                if (diagBudget()) {
                    diagPrintf(L"tsf.log", "foreground hook FAILED err=%lu\n", GetLastError());
                }
                return;
            }
        }
        fgHook_ = g_tlFgHook;
        addTipToThreadHookList(this);
    }

    void unhookForeground() {
        removeTipFromThreadHookList(this);
        fgHook_ = nullptr;
        if (g_tlHookTips.empty() && g_tlFgHook != nullptr) {
            UnhookWinEvent(g_tlFgHook);
            g_tlFgHook = nullptr;
        }
    }
    // ---- 激活状态机（2026-11-07 用户要求：未激活时不做任何操作）----
    //
    // inputActive_ 是**所有 TSF 按键/焦点逻辑的唯一闸门**：只有「TSF 焦点在本
    // 上下文 + 本线程激活档案是 Chaoset」时才为 true。为 false 时按键回调立即
    // 放行、不查询/不清理/不上报；清理已在上一次 active→inactive 迁移时完成。
    bool focused_ = false;      // OnSetFocus 记录的 TSF 焦点
    bool inputActive_ = false;  // 前台激活输入法（真正的“在用”）
    bool warmed_ = false;       // daemon 预热每实例只发一次（修重复 spawn）

    static bool foregroundIsSelf() {
        const HWND fg = GetForegroundWindow();
        if (fg == nullptr) return false;
        DWORD fgPid = 0;
        GetWindowThreadProcessId(fg, &fgPid);
        return fgPid == GetCurrentProcessId();
    }

    // 唯一的激活状态迁移出口。fromFocusEvent：OnSetFocus(TRUE) 时 TSF 已把焦点
    // 交给本上下文，允许不等 GetForegroundWindow 更新；Activate 则需要前台校验
    //（后台宿主会重复 Activate）。
    void updateInputActive(bool fromFocusEvent) {
        const bool next = focused_ && isActiveTip() &&
                          (fromFocusEvent || foregroundIsSelf());
        if (next == inputActive_) {
            if (!next) {
                candidateWnd_.setActiveGate(false);
                unhookForeground();
            } else {
                candidateWnd_.setActiveGate(true);
            }
            return;
        }
        inputActive_ = next;
        if (next) {
            // 第一次真正激活：创建后台 worker + TIP 线程 message-only 窗口
            //（之后所有异步上报/守卫都依赖它们；未激活实例不创建）。
            shellWorkerStart();
            ensureTipMsgWnd();
            // system 作用域：从 HKCU 镜像同步 + worker 异步校准；其它 scope 用本地值。
            syncEnglishFromAuthority();
            syncModeCompartment();
            // 先 active 再 mode：UI 宿主只有收到 active 1 才显示托盘。
            postActiveReport(true, currentEnglish(), true);
            reportModeToUi(currentEnglish(), true);
            candidateWnd_.setActiveGate(true);
            ensureForegroundHook();
            if (!transport_.isConnected()) warmDaemonInBackgroundOnce();
            refreshActiveWatchTimer();
            static volatile LONG loggedOn = 0;
            if (InterlockedCompareExchange(&loggedOn, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "input active=1 pid=%lu fgpid=%lu\n",
                           static_cast<unsigned long>(GetCurrentProcessId()),
                           static_cast<unsigned long>(0));
            }
        } else {
            // 失焦/停用/前台离开：先通知托盘隐藏，再彻底退场。
            postActiveReport(false, currentEnglish(), false);
            retireUiCompletely();
            // 保留旧的「焦点抖一下不丢 Shift 会话」语义：物理键仍按着则挂起，
            // 释放后由下一次按键按「挂起」规则收口（真切走应用时不会误切中英）。
            suspendShiftHoldOnFocusLost();
            refreshActiveWatchTimer();
            static volatile LONG loggedOff = 0;
            if (InterlockedCompareExchange(&loggedOff, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "input active=0 pid=%lu\n",
                           static_cast<unsigned long>(GetCurrentProcessId()));
            }
        }
    }

    // daemon 预热（保留原函数名/调用形态作为 checkuiparity 契约）：只入队，
    // CreateProcessW 在 ShellWorker 线程执行。每实例只发一次：旧实现每次焦点
    // 回来都发，连接失败时 worker 每次都 spawn chaosetd（tsf.log 大量
    // "daemon lazy start"、daemon.log 反复 "已有 chaosetd 在运行"）。
    void warmDaemonInBackgroundOnce() {
        if (warmed_) return;
        warmed_ = true;
        postDaemonWarm();
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

    // 线程内是否有任一 TIP 真正在用：定时器只在有用例时运行，未激活时零定时器。
    void refreshActiveWatchTimer() {
        if (g_tlTipMsgWnd == nullptr) return;
        bool any = false;
        for (ChaosetTIP* t : g_tlFgTips) {
            if (t != nullptr && t->inputActive_) {
                any = true;
                break;
            }
        }
        if (any) {
            SetTimer(g_tlTipMsgWnd, kActiveWatchTimerId, 500, nullptr);
        } else {
            KillTimer(g_tlTipMsgWnd, kActiveWatchTimerId);
        }
    }

    // 切换到其它输入法（TSF 未调 Deactivate）时的统一收口：托盘隐藏 + 彻底退场。
    void switchToInactiveProfile() {
        inputActive_ = false;
        focused_ = false;
        postActiveReport(false, currentEnglish(), false);
        retireUiCompletely();
        suspendShiftHoldOnFocusLost();
        refreshActiveWatchTimer();
        static volatile LONG logged = 0;
        if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
            diagPrintf(L"tsf.log", "profile switched: input inactive pid=%lu\n",
                       static_cast<unsigned long>(GetCurrentProcessId()));
        }
    }

    bool bailOutIfInactive() {
        if (inputActive_) {
            // 双保险：TSF 在部分宿主里切走输入法后仍把按键下沉给本 TIP 且不调用
            // Deactivate（2026-11-05 实测）。必须每键复核激活档案，否则用户已经
            // 换到别的输入法时我们还会吃键/显示托盘（用户 2026-11-07 的 bug1/2）。
            if (!isActiveTip()) {
                switchToInactiveProfile();
                return true;
            }
            candidateWnd_.setActiveGate(true);
            return false;
        }
        // 非激活：**绝对零动作**（2026-11-07 用户要求：没激活时不要处理任何
        // 东西、不要拦截按键、不要获取鼠标事件）。这里不 retire、不查询档案、
        // 不写盘、不动钩子/候选窗——清理已在 active→inactive 迁移时完成。
        static volatile LONG loggedInactive = 0;
        if (InterlockedCompareExchange(&loggedInactive, 1, 0) == 0 && diagBudget()) {
            diagPrintf(L"tsf.log", "inactive: keys passed through untouched\n");
        }
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

    // AppContainer 宿主不能 CreateProcess：请求 daemon 代拉起 UI 宿主（IPC ui）。
    // **只入队**（ShellWorker 线程执行）；保留函数名/调用形态供 checkuiparity
    // 静态契约，实际实现见 ensureUiHostByDaemonWorker / session_.ensureUiHost。
    bool ensureUiHostStartedByDaemon(bool settings = false) {
        postEnsureUiHostByDaemon(settings);
        return true;
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
        VARIANT var;
        ZeroMemory(&var, sizeof(var));
        var.vt = VT_I4;
        var.lVal = currentEnglish() ? 0 : 1;  // 非零 = 键盘打开（中文）
        const HRESULT hr = modeCompartment_->SetValue(clientId_, &var);
        VARIANT back;
        ZeroMemory(&back, sizeof(back));
        const HRESULT hrGet = modeCompartment_->GetValue(&back);
        // 日志只留失败（2s 限流、异步写盘）：成功路径不再每进程写一条。
        const bool failed = FAILED(hr) || FAILED(hrGet);
        static DWORD lastFailLog = 0;
        const DWORD now = GetTickCount();
        if (failed && diagBudget() && (lastFailLog == 0 || now - lastFailLog >= 2000)) {
            lastFailLog = now;
            diagPrintf(L"tsf.log",
                       "mode compartment failed en=%d set_hr=0x%08lX get_hr=0x%08lX read=%ld\n",
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
            // 零阻塞（审计 T4）：只在已连接时介入；未连接则入队后台连接并
            // fail-open——本键原样进文档，下一次按键即可正常组词，绝不 Sleep/建进程。
            if (!ensureDaemonFast(const_cast<PipeTransport&>(transport_))) return false;
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

        // 零阻塞（审计 T4）：Key-only 宿主（Chrome/conhost/Terminal 不投递 Test
        // 阶段）在这里补一次非阻塞连接探测；失败时各分支 fail-open（不阻塞、
        // 按键原样进文档）。Test 阶段已连接时这里只是一次分支预测。
        if (!transport_.isConnected()) ensureDaemonFast(transport_);

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
        // 用 inputActive_ 而不是 isActiveTip：后者在后台预激活实例仍可能为 true。
        if (!inputActive_) {
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
    // ---- worker 消息回投入口（message-only 窗口 → TIP 线程）----
    // 500ms 激活档案复核（定时器只在 active 时运行）：部分宿主切换输入法后
    // 不会调用 Deactivate（2026-11-05 实测），必须有一处事件驱动兜底让托盘
    // 和按键闸门在同一时刻失效。
    void onActiveWatchTimer() {
        if (!inputActive_) return;
        if (isActiveTip()) return;
        switchToInactiveProfile();
    }
    void onWorkerForeground(HWND fg) { onForegroundChanged(fg); }
    void onWorkerModeAuthority() {
        if (cachedModeScope() != ModeScope::System) return;
        if (g_modeAuthorityValid.load() != 0) {
            enMode_ = g_modeAuthorityEnglish.load() != 0;
            syncModeCompartment();
        }
    }
    void onWorkerShowToast(int id) {
        if (id == 1) showToast(L"设置窗口打不开：chaoset-ui.exe 未运行", true);
    }

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
        if (!cachedAssociate()) return;  // 设置关闭词组联想（1s TTL，审计 T10）
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

// ---- TIP 线程 message-only 窗口（worker / 钩子回调 → TIP 线程）----
//
// 2026-11-06 零阻塞：ShellWorker 与 SetWinEventHook 回调都不能碰 TSF/UI
// 对象，只能 PostMessage 到这个窗口；窗口过程在 TIP 线程上分发。
static LRESULT CALLBACK tipMsgWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
        case WM_TIMER:
            if (wParam == kActiveWatchTimerId) {
                for (ChaosetTIP* tip : g_tlFgTips) {
                    if (tip != nullptr) tip->onActiveWatchTimer();
                }
                return 0;
            }
            break;
        case WM_NCDESTROY:
            if (g_tlTipMsgWnd == hwnd) g_tlTipMsgWnd = nullptr;
            unregTipWnd(hwnd);
            InterlockedDecrement(&g_liveTipMsgWindows);
            return 0;
        case kTipForegroundMsg: {
            HWND fg = reinterpret_cast<HWND>(wParam);
            for (ChaosetTIP* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerForeground(fg);
            }
            return 0;
        }
        case kTipModeAuthorityMsg: {
            for (ChaosetTIP* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerModeAuthority();
            }
            return 0;
        }
        case kTipShowToastMsg:
            for (ChaosetTIP* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerShowToast(static_cast<int>(wParam));
            }
            return 0;
        case kTipDaemonReadyMsg:
            return 0;
        default:
            break;
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

static HWND ensureTipMsgWnd() {
    if (g_tlTipMsgWnd != nullptr) return g_tlTipMsgWnd;
    const std::wstring cls = windowClassName(L"ChaosetTipMsgWndClass", g_hInst);
    WNDCLASSEXW wc{};
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = tipMsgWndProc;
    wc.hInstance = g_hInst;
    wc.lpszClassName = cls.c_str();
    RegisterClassExW(&wc);
    HWND wnd = CreateWindowExW(0, cls.c_str(), L"ChaosetTipMsg", 0, 0, 0, 0, 0,
                               HWND_MESSAGE, nullptr, g_hInst, nullptr);
    if (wnd == nullptr) return nullptr;
    g_tlTipMsgWnd = wnd;
    InterlockedIncrement(&g_liveTipMsgWindows);
    regTipWnd(GetCurrentThreadId(), wnd);
    return wnd;
}

static void registerTipOnThread(ChaosetTIP* tip) {
    if (tip == nullptr) return;
    for (ChaosetTIP* p : g_tlFgTips) {
        if (p == tip) return;
    }
    g_tlFgTips.push_back(tip);
}

static void unregisterTipFromThread(ChaosetTIP* tip) {
    if (tip == nullptr) return;
    removeTipFromThreadHookList(tip);
    for (auto it = g_tlFgTips.begin(); it != g_tlFgTips.end(); ++it) {
        if (*it == tip) {
            g_tlFgTips.erase(it);
            break;
        }
    }
    if (g_tlFgTips.empty() && g_tlTipMsgWnd != nullptr) {
        DestroyWindow(g_tlTipMsgWnd);  // WM_NCDESTROY 里归还计数并清 thread_local
    }
}

void CandidateWindow::onLButtonDown(int x, int y) {
    // **按下只记录命中目标，不执行任何动作**：候选点选、翻页、设置齿轮统一在
    // 鼠标抬起时触发（2026-11-06 用户要求：「鼠标用抬起触发才正常，按下触发
    // 太奇怪」；按下即上屏/即弹窗在按下后想拖走或放弃时会误触）。
    const POINT pt{ x, y };
    gearPressed_ = false;
    pressedItemIndex_ = -1;
    chevronPressed_ = false;
    if (tip_ == nullptr) return;
    if (PtInRect(&settingsRect_, pt)) {
        gearPressed_ = true;
        return;
    }
    for (const auto& it : items_) {
        if (PtInRect(&it.itemRect, pt)) {
            pressedItemIndex_ = it.index;
            return;
        }
    }
    if (comp_.pageCount > 1 && PtInRect(&chevronRect_, pt)) chevronPressed_ = true;
}

void CandidateWindow::onLButtonUp(int x, int y) {
    // 抬起点必须仍在**同一个**命中目标上才执行（按下后拖走再松开不触发）。
    const POINT pt{ x, y };
    const bool wasGear = gearPressed_;
    const int wasItem = pressedItemIndex_;
    const bool wasChevron = chevronPressed_;
    gearPressed_ = false;
    pressedItemIndex_ = -1;
    chevronPressed_ = false;
    if (tip_ == nullptr) return;
    if (wasGear) {
        if (PtInRect(&settingsRect_, pt)) tip_->showSettingsWindow();
        return;
    }
    if (wasItem >= 0) {
        for (const auto& it : items_) {
            if (it.index == wasItem && PtInRect(&it.itemRect, pt)) {
                tip_->selectCandidateByIndex(it.index);
                break;
            }
        }
        return;
    }
    if (wasChevron && comp_.pageCount > 1 && PtInRect(&chevronRect_, pt)) {
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
    invalidateAssociateCache();  // 广播到达：立即失效缓存
    if (cachedAssociate()) return;
    if (tip_ != nullptr) tip_->clearAssociateFromSettings();
}

// 候选栏齿轮：设置窗由单例 UI 进程（chaoset-ui.exe）创建，TIP 只发管道请求。
// 优先请求已在运行的 UI 宿主；没有宿主时由 UI 宿主带 --settings 启动自己
// （首实例直接弹设置窗，第二个实例把请求转给首实例后退出）。AppContainer
// 宿主不能发起进程，只能连接登录 Run 项常驻的 UI 宿主。
void ChaosetTIP::showSettingsWindow() {
    // TSF 线程零等待：openSettingsViaUi 内部只做前台授权 + worker 入队，
    // 恒返回 true（管道重试/拉起 UI 宿主/失败提示都在 ShellWorker）。
    if (openSettingsViaUi()) return;  // 保留既有静态契约：不是同步打开
    // 理论上的失败兜底（当前 openSettingsViaUi 恒 true）：AppContainer 宿主
    // 不能 CreateProcess，入队请 daemon 代拉起（worker 线程执行）。
    if (isAppContainer()) ensureUiHostStartedByDaemon(/*settings=*/true);
    static volatile LONG logged = 0;
    if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
        diagPrintf(L"tsf.log", "open settings failed: ui host unavailable appcontainer=%d\n",
                   isAppContainer() ? 1 : 0);
    }
    showToast(L"设置窗口打不开：chaoset-ui.exe 未运行", true);
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

// chaoset-ui.exe 的唯一入口：宿主托盘中英指示器 + 设置窗 + 命名管道服务。
// 实现全部在本 DLL 内（设置窗代码/托盘绘制/管道），exe 只是 LoadLibrary 壳。
extern "C" __declspec(dllexport) int WINAPI ChaosetRunUi(HINSTANCE /*exeInst*/,
                                                         LPCWSTR cmdLine) {
    // UI 宿主进程里的异步日志/后台任务 worker 属于本 TU（TIP 的 ShellWorker），
    // 叶子模块 ui_host 不反向依赖；这里把生命周期原语注入给 runUiHost，
    // 启动/停止顺序与拆分前一致（先起 worker 再建窗口，退出前先停 worker）。
    uiHostSetWorkerHooks(&shellWorkerStart, &shellWorkerStop, &shellWorkerWaitStopped);
    return runUiHost(cmdLine);
}

BOOL WINAPI DllMain(HINSTANCE hinstDLL, DWORD fdwReason, LPVOID reserved) {
    if (fdwReason == DLL_PROCESS_ATTACH) {
        g_hInst = hinstDLL;
        // tsf.log 的异步落盘入口：诊断是叶子模块，worker 段在 DllMain 注册
        // enqueueLogLine；未注册/worker 未启动时直接丢弃，绝不回退同步写。
        diagSetAsyncLog(&enqueueLogLine);
    } else if (fdwReason == DLL_PROCESS_DETACH && reserved == nullptr && hinstDLL != nullptr) {
        // 动态卸载（FreeLibrary）：先停后台 worker（它跑本 DLL 的代码），并注销
        // 本模块注册的窗口类。Windows 不会随 DLL 卸载自动注销窗口类，不注销的话
        // 下次加载时同名类的 lpfnWndProc 可能仍指向已卸载内存。
        // 进程终止（reserved != nullptr）时无需处理。
        shellWorkerStop();
        UnregisterClassW(windowClassName(L"ChaosetCandidateWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetModeToastWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetSettingsWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetTrayWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetUiHostWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetTipMsgWndClass", hinstDLL).c_str(), hinstDLL);
    }
    return TRUE;
}

STDAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void** ppv) {
    if (!IsEqualCLSID(rclsid, CLSID_ChaosetTIP)) return CLASS_E_CLASSNOTAVAILABLE;
    // 2026-11-06 零阻塞：此处不再同步写 tsf.log（classobject 属于加载期回调；
    // Activate 行已记录 host，足以区分「DLL 加载了但 Activate 没跑」）。
    static ChaosetFactory factory;  // 无状态，静态实例即可
    return factory.QueryInterface(riid, ppv);
}

STDAPI DllCanUnloadNow() {
    // 必须同时考虑 TIP 实例、各类窗口与后台 worker：
    //   * 窗口过程都指向本 DLL 代码，残留窗口时 FreeLibrary 会让下一次
    //     WM_PAINT 跳到已卸载内存；
    //   * ShellWorker 线程同样执行本 DLL 代码，线程存活期间绝不能卸载。
    // 没有 TIP/窗口后才停 worker；线程尚未退干净前继续返回 S_FALSE。
    if (g_liveObjects != 0) return S_FALSE;
    if (g_liveCandidateWindows != 0) return S_FALSE;
    if (g_liveSettingsWindows != 0) return S_FALSE;
    if (g_liveToastWindows != 0) return S_FALSE;
    if (g_liveTrayWindows != 0) return S_FALSE;
    if (g_liveTipMsgWindows != 0) return S_FALSE;
    shellWorkerStop();
    if (shellWorkerAlive()) return S_FALSE;
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
