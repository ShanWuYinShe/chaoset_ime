// 单例 UI 宿主进程（chaoset-ui.exe）：UiTray、托盘菜单、runUiHost、命令管道线程。
// 叶子模块：不得 include/引用 TIP TU（tip.hpp / tip_*.cpp）的类型；
// UI 宿主进程里的 ShellWorker 生命周期由导出入口注入（见 ui_host.hpp）。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <shellapi.h>  // Shell_NotifyIconW（托盘图标）
#include <objbase.h>   // CoInitializeEx / CoUninitialize（文件对话框、主题查询）
#include <sddl.h>      // ConvertStringSecurityDescriptorToSecurityDescriptorW（管道 SDDL）
#include <cwchar>
#include <string>
#include <vector>

#include "ui_host.hpp"
#include "ui_client.hpp"
#include "settings_window.hpp"
#include "config.hpp"
#include "diagnostics.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// 托盘右键菜单（实现在 UI 宿主段；需要 g_uiHostWnd/openSettings 等）。
static void showTrayMenu(HWND owner);

// UI 宿主的隐藏消息窗口（定义见文件后部；托盘点击要往里投消息）。
extern HWND g_uiHostWnd;
static constexpr UINT kUiToggleModeMsg = WM_APP + 73;

// UI 宿主进程里的 ShellWorker 生命周期原语：由导出入口 ChaosetRunUi 注入
// （worker 调度属于 shell_worker.cpp，叶子模块不反向引用）。
static UiHostWorkerFn g_workerStart = nullptr;
static UiHostWorkerFn g_workerStop = nullptr;
static UiHostWorkerWaitFn g_workerWaitStopped = nullptr;

void uiHostSetWorkerHooks(UiHostWorkerFn start, UiHostWorkerFn stop,
                          UiHostWorkerWaitFn waitStopped) {
    g_workerStart = start;
    g_workerStop = stop;
    g_workerWaitStopped = waitStopped;
}

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

    // 显示并刷新图标（由 TIP 的 active=1 命令驱动，2026-11-07 用户要求：
    // 输入法未激活时托盘必须隐藏、激活时才出现）。visible_ 表示「用户期望
    // 可见」：active=0 隐藏后置 false，explorer 重启（TaskbarCreated）不会
    // 把用户已经不期望的图标提前点亮。
    void show(bool english) {
        visible_ = true;
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
        if (!notifyIcon(shown_ ? NIM_MODIFY : NIM_ADD)) {
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

    // 只更新图标/提示，**绝不创建或显示图标**：托盘可见性只由 TIP 的 active
    // 命令控制（mode 命令只负责「当前 app 实际中英状态」）。2026-11-07 用户
    // 反馈 bug1/bug3：旧实现 mode 上报即 show()，托盘一旦出现永不休眠，且
    // process/window scope 下托盘状态会和当前 app 的实际状态脱节。
    // 隐藏时什么都不做——下一次 show(g_uiEnglish) 会自然带上最新模式。
    void setMode(bool english) {
        if (!shown_) return;
        if (icon_ != nullptr && english_ == english) return;
        HICON fresh = makeGlyphIcon(english);
        if (fresh == nullptr) {
            logFailure("icon", GetLastError());
            return;
        }
        if (icon_ != nullptr) DestroyIcon(icon_);
        icon_ = fresh;
        english_ = english;
        if (!notifyIcon(NIM_MODIFY)) logFailure("Shell_NotifyIcon", GetLastError());
    }

    // 托盘图标当前是否真的在显示。调用方据此决定要不要再用浮动提示兜底
    //（两个地方同时提示中英文模式既重复又干扰，2026-11-05 用户反馈）。
    bool isShown() const { return shown_; }

    // 隐藏：NIM_DELETE 把图标从任务栏移除并清 shown_；之后 active=1 的 show()
    // 会重新 NIM_ADD（用户要求：未激活必须消失，切回来必须能再出现）。
    // 由 UI 主线程的 active=0 防抖定时器调用。
    void hide() {
        visible_ = false;
        if (hwnd_ != nullptr) KillTimer(hwnd_, kRetryTimerId);
        if (!shown_) return;
        if (!notifyIcon(NIM_DELETE)) logFailure("Shell_NotifyIcon", GetLastError());
        shown_ = false;
    }

private:
    static constexpr UINT kIconId = 1;
    static constexpr UINT kCallbackMsg = WM_APP + 19;
    static constexpr UINT kRetryTimerId = 2;
    HWND hwnd_ = nullptr;
    HICON icon_ = nullptr;
    bool shown_ = false;
    bool visible_ = false;  // 用户期望托盘可见（TIP active=1 显示 / active=0 隐藏）
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

    // 统一填 NOTIFYICONDATAW：add/show、setMode（NIM_MODIFY）、hide（NIM_DELETE）
    // 用同一份字段，避免三处 tooltip/回调消息漂移。
    bool notifyIcon(DWORD action) {
        NOTIFYICONDATAW nid{};
        nid.cbSize = sizeof(nid);
        nid.hWnd = hwnd_;
        nid.uID = kIconId;
        nid.uFlags = NIF_ICON | NIF_TIP | NIF_MESSAGE;
        nid.uCallbackMessage = kCallbackMsg;
        nid.hIcon = icon_;
        wcscpy_s(nid.szTip, english_ ? L"Chaoset 输入法：英文" : L"Chaoset 输入法：中文");
        return Shell_NotifyIconW(action, &nid) != FALSE;
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
                // 只在 active 生命周期期望可见（visible_）时恢复：启动阶段从未
                // 显示过、或已被 active=0 隐藏的会话，不能被 TaskbarCreated 复活。
                self->shown_ = false;
                if (self->visible_) self->show(self->english_);
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

// 任务栏模式指示器窗口：窗口过程同样指向本 DLL，必须纳入防卸载保护。
volatile LONG g_liveTrayWindows = 0;

// ---- UI 宿主进程（chaoset-ui.exe）----
//
// 进程模型：chaoset-ui.exe 只有几十行（LoadLibrary 本 DLL + 调用导出
// ChaosetRunUi），托盘绘制/设置窗/命名管道全部复用本文件代码，不存在第二套
// UI 实现。这样做的原因见文件开头「单例 UI 宿主进程通信」的说明。
//
// 单实例：Local\ChaosetUi-<SID>-<session> 互斥体。第二个实例若带 --settings，
// 就把 "settings" 命令转发给首实例后退出（连点齿轮不会开多个设置窗）。
//
// 生命周期：不写登录 Run 项（2026-11-06 用户要求：开机不显示托盘，切到输入法
// 才显示）；TIP 首次激活时用 --mode 懒启动，设置窗入口用 --settings 拉起
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
static constexpr UINT kUiSetModeMsg = WM_APP + 72;
// TIP→UI 的 active 上报（2026-11-07 协议定稿）：托盘可见性的唯一开关。
// 命令管道收到纯文本 "active 1"/"active 0" 后 PostMessage 到这里；
// active=1 取消待隐藏定时器并 show(g_uiEnglish)，active=0 防抖 300ms 后 hide()。
// 占 WM_APP+74（原「打开设置→关于」消息顺延到 +75，避免撞号）。
static constexpr UINT kUiActiveMsg = WM_APP + 74;
// 打开设置窗并切到「关于」页（托盘右键菜单用，2026-11-06）。
static constexpr UINT kUiOpenSettingsAboutMsg = WM_APP + 75;
// kUiToggleModeMsg 定义见文件前部（TIP 侧托盘点击也要用）。
// 托盘隐藏防抖（active=0 后 300ms 才真正隐藏，期间 active=1 取消）：焦点在同一
// 进程多个 TIP 实例/多个窗口间抖动时不会闪图标。定时器挂在 UI 宿主隐藏窗口上。
static constexpr UINT_PTR kUiHideTimerId = 1;
static constexpr UINT kUiHideDelayMs = 300;
HWND g_uiHostWnd = nullptr;
static UiTray* g_uiTray = nullptr;  // 进程生命周期内唯一托盘实例（仅 UI 主线程使用）
static HANDLE g_uiStopEvent = nullptr;
static volatile LONG g_uiStopping = 0;

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

// 权威中英状态（system 作用域下的唯一真相）。
//
// 为什么放在 UI 宿主：它每用户会话唯一、常驻、中等完整性（能写 HKCU），
// 而 TIP 会被装进任意宿主进程（含 AppContainer，写不了 HKCU）。
// g_uiEnglish 只在 UI 主线程读写（托盘/菜单/管道消息都由主线程处理）。
//
// process/window 作用域下它不是全局权威：只表示「最近一次激活 app 的模式」，
// 供托盘图标显示（托盘跟 app），不写 HKCU（2026-11-07 bug3 修复）。
static bool g_uiEnglish = false;

static bool uiCurrentEnglish() { return g_uiEnglish; }


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
    // active 1/0：托盘可见性的唯一开关（2026-11-07 用户反馈「托盘永不休眠」）。
    // mode 只更新图标/状态，active 才决定显示还是隐藏；解析后投递 UI 主线程
    //（Shell_NotifyIcon/窗口操作只能由拥有消息循环的主线程做）。
    if (len >= 8 && strncmp(command, "active ", 7) == 0) {
        if (g_uiHostWnd != nullptr) {
            PostMessageW(g_uiHostWnd, kUiActiveMsg, command[7] == '1' ? 1 : 0, 0);
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

// 设置权威中英状态（只在 UI 主线程调用）：写 HKCU + 同步托盘图标。
// **不改变托盘可见性**：可见性只由 active 命令控制（2026-11-07）。system
// scope 下托盘是全局权威（app 跟托盘）；process/window scope 见 kUiSetModeMsg。
static void uiApplyEnglish(bool english) {
    g_uiEnglish = english;
    if (g_uiTray != nullptr) {
        g_uiTray->setMode(english);
    }
    uiSaveEnglish(english);
}

static LRESULT CALLBACK uiHostWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
        case kUiSetModeMsg: {
            // TIP 上报它当前 app 的中英模式（激活/切换时）。**不改变托盘可见性**：
            //   - system scope：托盘是全局权威，写 HKCU（app 跟托盘）；
            //   - process/window scope：托盘只是当前 app 的指示器（托盘跟 app），
            //     只更新内存与图标，不写 HKCU（后果由 docs/ui-parity.md 记录）。
            const bool english = wParam != 0;
            if (uiLoadModeScope() == ModeScope::System) {
                uiApplyEnglish(english);
            } else {
                g_uiEnglish = english;
                if (g_uiTray != nullptr) g_uiTray->setMode(english);  // 只在 UI 主线程碰托盘
            }
            return 0;
        }
        case kUiToggleModeMsg:
            // 托盘左键 / 管道 "toggle"：翻转权威状态。
            // 只在 system scope 生效：process/window scope 下托盘只是「当前 app
            // 实际状态」的指示器，不能反向改全局（2026-11-07 用户反馈 bug3：
            // 托盘与 app 中英不一致；scope 语义见 docs/ui-parity.md）。
            if (uiLoadModeScope() == ModeScope::System) uiApplyEnglish(!g_uiEnglish);
            return 0;
        case kUiActiveMsg:
            // TIP→UI active 上报：托盘可见性的唯一开关（2026-11-07 用户反馈
            // bug1「托盘永不休眠」）。所有托盘操作只在 UI 主线程。
            if (wParam != 0) {
                // 前台有激活的 Chaoset TIP：取消待隐藏并显示当前模式。
                KillTimer(hwnd, kUiHideTimerId);
                if (g_uiTray != nullptr) g_uiTray->show(g_uiEnglish);
            } else {
                // 激活上下文失焦/停用/前台离开：防抖 300ms 后隐藏；期间若收到
                // active=1（焦点在同一进程多 TIP 实例间抖动）会被 KillTimer 取消。
                if (SetTimer(hwnd, kUiHideTimerId, kUiHideDelayMs, nullptr) == 0) {
                    // 定时器创建失败（资源耗尽等）：立刻隐藏——绝不能留下一个
                    // 「未激活却永不消失」的托盘图标（用户反馈的 bug1）。
                    if (g_uiTray != nullptr) g_uiTray->hide();
                }
            }
            return 0;
        case WM_TIMER:
            if (wParam == kUiHideTimerId) {
                KillTimer(hwnd, kUiHideTimerId);
                if (g_uiTray != nullptr) g_uiTray->hide();
                return 0;
            }
            break;
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
            KillTimer(hwnd, kUiHideTimerId);
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
// UI 宿主主函数（由导出 ChaosetRunUi 调用，运行在 chaoset-ui.exe 进程里）。
static int finishUiHost(int code) {
    // 先停后台 worker 并等它退出（它跑本 DLL 的代码，FreeLibrary 前必须结束）。
    // worker 原语由导出入口 ChaosetRunUi 注入（shell_worker.cpp 的 ShellWorker）。
    if (g_workerStop != nullptr) g_workerStop();
    if (g_workerWaitStopped != nullptr) g_workerWaitStopped(2000);
    return code;
}

int runUiHost(LPCWSTR cmdLine) {
    const std::wstring sid = currentUserSid();
    // 文件对话框（词库导入/学习数据导出）与主题查询需要 Apartment；
    // 失败不影响托盘指示器与设置窗主体。
    const HRESULT hrCo = CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
    // UI 宿主也是进程：崩溃同样要留下 crash.log（此前只有 TIP 的 Activate 装 VEH）。
    ensureCrashHandler();
    // 后台 worker：日志异步落地（设置窗/托盘路径也会写 tsf.log）。
    if (g_workerStart != nullptr) g_workerStart();
    g_uiStopEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);

    // 单实例互斥体（名字带会话 ID，见 uiPipeName 注释）。
    HANDLE mutex = nullptr;
    const std::wstring mutexName = uiMutexName();
    if (!mutexName.empty()) {
        mutex = CreateMutexW(nullptr, FALSE, mutexName.c_str());
        if (mutex != nullptr && GetLastError() == ERROR_ALREADY_EXISTS) {
            // 已有实例：把 --settings / --mode 请求转发给首实例。首实例可能还没
            // 开始监听管道（互斥体在管道线程之前创建），这里做 ~2s 的短重试。
            bool wantSettings = false;
            bool wantMode = false;
            bool modeEnglish = false;
            if (cmdLine != nullptr) {
                wantSettings = wcsstr(cmdLine, L"--settings") != nullptr;
                if (wcsstr(cmdLine, L"--mode 1") != nullptr) {
                    wantMode = true;
                    modeEnglish = true;
                } else if (wcsstr(cmdLine, L"--mode 0") != nullptr) {
                    wantMode = true;
                }
            }
            if (wantSettings || wantMode) {
                bool sentSettings = !wantSettings;
                bool sentMode = !wantMode;
                for (int i = 0; i < 100 && (!sentSettings || !sentMode); ++i) {
                    if (!sentMode) sentMode = uiSendCommand(modeEnglish ? "mode 1" : "mode 0", 1);
                    if (!sentSettings) sentSettings = uiSendCommand("settings", 1);
                    if (!sentSettings || !sentMode) Sleep(20);
                }
                bool sent = sentSettings && sentMode;
                if (!sent) {
                    diagPrintf(L"tsf.log", "ui host: forward settings failed after retry\n");
                }
            }
            CloseHandle(mutex);
            if (g_uiStopEvent != nullptr) CloseHandle(g_uiStopEvent);
            if (SUCCEEDED(hrCo)) CoUninitialize();
            return finishUiHost(0);
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
        return finishUiHost(1);
    }

    // 托盘中英指示器：整个用户会话唯一。**启动时一律不显示**（2026-11-07 用户
    // 要求：输入法未激活时托盘必须隐藏）。可见性只由 TIP 的 active 上报控制：
    // 收到 active=1 才 show()，active=0 防抖后 hide()；UI 宿主不写登录 Run 项，
    // 由 TIP 首次激活时带 --mode 懒启动（见 ensureUiHostStarted）。
    g_uiTray = new UiTray();
    // 权威状态初始化：优先 HKCU 记忆（上次托盘显示的状态），否则中文。
    g_uiEnglish = uiLoadSavedEnglish();

    // 先启动管道线程，再处理 --settings：否则二次实例的转发请求会赶在监听前
    // 到达（虽然它自身有重试，这里尽量避免）。
    HANDLE pipeThread = CreateThread(nullptr, 0, uiPipeThreadProc, nullptr, 0, nullptr);
    if (pipeThread == nullptr) {
        diagPrintf(L"tsf.log", "ui host: create pipe thread FAILED\n");
        delete g_uiTray;
        g_uiTray = nullptr;
        DestroyWindow(g_uiHostWnd);
        if (mutex != nullptr) CloseHandle(mutex);
        if (g_uiStopEvent != nullptr) CloseHandle(g_uiStopEvent);
        if (SUCCEEDED(hrCo)) CoUninitialize();
        return finishUiHost(1);
    }

    // 命令行语义：
    //   --settings   TIP 点齿轮但 UI 宿主没在跑时的拉起路径，首实例直接弹设置窗；
    //   --mode 0/1   TIP 首次激活时带上的当前中英状态——**只记录，不显示托盘**
    //                （可见性由随后到达的 active 上报控制；旧实现在这里直接
    //                 show()，是「托盘一旦出现永不休眠」的根因之一）。
    bool wantSettings = false;
    bool haveMode = false;
    bool modeEnglish = false;
    if (cmdLine != nullptr) {
        wantSettings = wcsstr(cmdLine, L"--settings") != nullptr;
        if (wcsstr(cmdLine, L"--mode 1") != nullptr) {
            haveMode = true;
            modeEnglish = true;
        } else if (wcsstr(cmdLine, L"--mode 0") != nullptr) {
            haveMode = true;
        }
    }
    if (haveMode) {
        // 只同步模式（system scope 写 HKCU），**不显示托盘**：此后 TIP 的
        // active=1 上报才会真正 show()，模式上报会再校准图标。
        g_uiEnglish = modeEnglish;
        if (uiLoadModeScope() == ModeScope::System) uiSaveEnglish(modeEnglish);
    }
    if (wantSettings) {
        Sleep(30);  // 给管道线程一个创建实例的机会
        openSettingsWindowStandalone();
    }

    MSG msg;
    while (GetMessageW(&msg, nullptr, 0, 0) > 0) {
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }

    // 退出顺序（评审 P0）：先停管道线程并 join，再销毁托盘/窗口；最后停 worker。
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
    return finishUiHost(0);
}

}  // namespace chaoset::win
