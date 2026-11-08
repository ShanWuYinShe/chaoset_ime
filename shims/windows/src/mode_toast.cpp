// 模式切换提示（短暂 toast）实现；声明与职责说明见 mode_toast.hpp。
//
// 叶子模块（docs/architecture.md §3.1）：本 TU 不得 include/引用 win32.cpp，
// 只使用 winutil（owner/类名/DPI）、theme（配色）、diagnostics（预算日志）。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <string>

#include "diagnostics.hpp"
#include "mode_toast.hpp"
#include "theme.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// 存活中的模式提示窗数量：窗口过程指向本 DLL 代码，此前没有计数（漏了防卸载
// 保护），且 owner 为空时不会被宿主连带销毁。2026-10-31 审计 A4 补计数 + owner
// 后统一纳入 DllCanUnloadNow。
volatile LONG g_liveToastWindows = 0;

ModeToast::~ModeToast() { destroy(); }

void ModeToast::show(HINSTANCE hinst, const std::wstring& text, int x, int y) {
    if (!ensureWindow(hinst)) return;
    // owner 跟随当前前台窗口刷新：窗口在 TIP 生命周期内复用，只在首次创建时取
    // owner 会让后续（尤其 shell 宿主）提示条永远无 owner，被沉浸式 z 序 band
    // 压住（2026-11-05 回归审计 P2-10）。
    const HWND wantOwner = popupOwner();
    if (wantOwner != nullptr && GetWindow(hwnd_, GW_OWNER) != wantOwner) {
        SetWindowLongPtrW(hwnd_, GWLP_HWNDPARENT, reinterpret_cast<LONG_PTR>(wantOwner));
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

void ModeToast::hide() {
    if (hwnd_ != nullptr && IsWindow(hwnd_)) {
        KillTimer(hwnd_, kTimerId);
        ShowWindow(hwnd_, SW_HIDE);
    }
}

int ModeToast::pt(int v) const { return MulDiv(v, dpi_, 72); }

bool ModeToast::ensureWindow(HINSTANCE hinst) {
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
    // ModeToast 是**纯提示**：不接收鼠标（带 WS_EX_TRANSPARENT 穿透到宿主应用，
    // 对应 macOS 端 ModeToastController 的 ignoresMouseEvents=true；2026-10-30
    // 审计 A12 修正：此前注释写「纯提示 ignoresMouseEvents」但窗口实际没有穿透，
    // 提示显示 1.2s 内其区域会吃掉点击）。层级同候选窗 TOPMOST 保持（系统模态
    // 菜单弹出时位于最上，不会挡菜单）。owner 设为前台窗口：提示条与候选窗一样，
    // 无主 TOPMOST 窗口会被 Windows 11 开始菜单/搜索的沉浸式 z 序 band 压住，
    // 模式切换就看不到反馈（2026-10-31 审计 A4）。前台窗口拿不到时退回
    // nullptr（旧行为）。
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

void ModeToast::measure() {
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

HFONT ModeToast::makeFont() const {
    return CreateFontW(-MulDiv(13, dpi_, 72), 0, 0, 0, FW_MEDIUM, FALSE, FALSE, FALSE,
                       DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                       CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
}

void ModeToast::onPaint(HWND hwnd) {
    PAINTSTRUCT ps{};
    HDC dc = BeginPaint(hwnd, &ps);
    const bool dark = isSystemDarkTheme();
    HBRUSH bg = CreateSolidBrush(dark ? RGB(0x2C, 0x2C, 0x2E) : RGB(0xF6, 0xF6, 0xF8));
    // 描边 1pt（mac Toast 同为 1pt；用 show() 缓存的 dpi_，见 strokePx）
    HPEN pen = CreatePen(PS_SOLID, strokePx(dpi_),
                         dark ? RGB(0x45, 0x45, 0x48) : RGB(0xC8, 0xC8, 0xCC));
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

LRESULT CALLBACK ModeToast::wndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
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
            // 同候选窗：主题切换立即重绘（提示条仅 1.2s，仍可能在显示期间遇到
            // 切换，保持配色一致）。
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

void ModeToast::destroy() {
    hide();
    if (hwnd_ != nullptr) {
        if (!IsWindow(hwnd_) || !DestroyWindow(hwnd_)) onWindowDestroyed();
    }
}

void ModeToast::onWindowDestroyed() {
    if (hwnd_ != nullptr) {
        hwnd_ = nullptr;
        InterlockedDecrement(&g_liveToastWindows);
    }
}

}  // namespace chaoset::win
