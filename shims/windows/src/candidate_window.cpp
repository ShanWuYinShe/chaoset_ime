// 候选词悬浮窗（Windows GDI 自绘）实现；声明与职责说明见 candidate_window.hpp。
//
// 从 win32.cpp 拆出（wave1 续）：本 TU 不 include/引用 TIP 类型，对 TIP 的唯一
// 接触是 CandidateWindowHost 抽象接口。叶子依赖见头文件注释与
// docs/architecture.md §3.1。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <wingdi.h>
#include <uiautomationcore.h>     // UIA 类型（无障碍宿主 provider，2026-10-31）
#include <uiautomationcoreapi.h>  // UiaHostProviderFromHwnd / UiaReturnRawElementProvider

#include <algorithm>
#include <cmath>
#include <string>
#include <vector>

#include "candidate_layout.hpp"
#include "candidate_window.hpp"
#include "config.hpp"
#include "core.hpp"
#include "diagnostics.hpp"
#include "theme.hpp"
#include "winutil.hpp"

namespace chaoset::win {

namespace {

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

// GetProcAddress 返回签名未知的 FARPROC，直接 reinterpret_cast 到具体导出
// 函数指针会触发 mingw -Wall 的 -Wcast-function-type（构建基线要求零警告）。
// 先经 void* 中转再转目标签名与直接转换语义等价（Win32 ABI 下函数指针与
// 数据指针同宽，实际签名由调用方按导出文档保证），不屏蔽/降级任何告警。
template <typename Fn>
static Fn loadProcAddress(HMODULE mod, const char* name) {
    void* proc = reinterpret_cast<void*>(GetProcAddress(mod, name));
    return reinterpret_cast<Fn>(proc);
}

static void ensureUiaLoaded() {
    static bool tried = false;
    if (tried) return;
    tried = true;
    HMODULE mod = LoadLibraryW(L"uiautomationcore.dll");
    if (mod == nullptr) return;
    g_uiaHostProviderFromHwnd = loadProcAddress<UiaHostProviderFromHwndFn>(
        mod, "UiaHostProviderFromHwnd");
    g_uiaReturnRawElementProvider = loadProcAddress<UiaReturnRawElementProviderFn>(
        mod, "UiaReturnRawElementProvider");
}

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

}  // namespace

// 当前存活的候选窗口数量。用于 DllCanUnloadNow —— 候选窗的窗口过程指向
// 本 DLL 内的代码，只要还开着窗口就不能让宿主 FreeLibrary（否则下一次
// WM_PAINT 会跳到已卸载内存，崩掉 explorer / TextInputHost）。
 volatile LONG g_liveCandidateWindows = 0;

CandidateWindow::CandidateWindow() = default;

CandidateWindow::~CandidateWindow() { destroy(); }


HWND CandidateWindow::hwnd() const { return hwnd_; }


// 设置候选窗 owner（宿主窗口）。窗口已创建时用 GWLP_HWNDPARENT 改 owner
// （SetWindowLongPtr(hwnd, GWLP_HWNDPARENT) 的语义就是 SetWindowLong 的
// owner 设置，不是父窗口——WS_POPUP 窗口没有父窗口）。
// 必须在 show() 之前设置才能作用于 CreateWindowExW；已创建时改 owner
// 会重建 z 序关系，同样有效。
void CandidateWindow::setOwner(HWND owner) {
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
void CandidateWindow::setActiveGate(bool active) { activeGate_ = active; if (!active) hide(); }


void CandidateWindow::show(HINSTANCE hinst, POINT pt, int lineH, const chaoset::Comp& comp, CandidateWindowHost* host) {
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
    tip_ = host;
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


void CandidateWindow::hide() {
    // 面板收起/重建时丢弃未完成的按下状态，避免下一次抬起误触发
    //（按下后鼠标移出或被 TSF 收起，收不到对应 mouseup）。
    gearPressed_ = false;
    pressedItemIndex_ = -1;
    chevronPressed_ = false;
    if (hwnd_ != nullptr && IsWindow(hwnd_)) {
        ShowWindow(hwnd_, SW_HIDE);
    }
}


void CandidateWindow::destroy() {
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
void CandidateWindow::onWindowDestroyed() {
    if (hwnd_ != nullptr) {
        hwnd_ = nullptr;
        // 窗口过程已失效，此时才允许 DLL 被卸载（见 DllCanUnloadNow）
        InterlockedDecrement(&g_liveCandidateWindows);
    }
    // 清掉裸指针，避免窗口已销毁却仍被引用（审查指出的悬垂风险）
    tip_ = nullptr;
}


// 候选窗按**锚点（光标）所在显示器**的 DPI 布局，而不是主屏 DPI：
// 混合 DPI 多屏下候选窗落到副屏时尺寸/字号才正确（2026-10-31）。
int CandidateWindow::getDpi() const { return layoutDpi_ > 0 ? layoutDpi_ : systemDpi(); }


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
int CandidateWindow::pt(int v, int dpi) { return MulDiv(v, dpi, 72); }


bool CandidateWindow::isInteractiveArea() const {
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


void CandidateWindow::ensureWindow(HINSTANCE hinst) {
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


LRESULT CALLBACK CandidateWindow::CandidateWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
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


void CandidateWindow::layoutAndPosition() {
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


void CandidateWindow::onPaint(HWND hwnd) {
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

// 候选窗收到「联想开关变化」广播：关闭时立即收起联想；只查注册表，
// 不信任消息参数（消息 id 由字符串注册，跨进程一致）。
void CandidateWindow::onAssociateChanged() {
    invalidateAssociateCache();  // 广播到达：立即失效缓存
    if (cachedAssociate()) return;
    if (tip_ != nullptr) tip_->clearAssociateFromSettings();
}

}  // namespace chaoset::win
