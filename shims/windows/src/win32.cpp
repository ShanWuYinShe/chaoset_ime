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
#include <msctf.h>
#include <objbase.h>

#include <algorithm>
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

 volatile LONG g_liveObjects = 0;

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

class ChaosetTIP;

// ---- 候选窗主题配色：与 macOS CandidatePanel.swift / renderer.DefaultSkinFor 像素级一致 ----

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

class CandidateWindow {
public:
    CandidateWindow() = default;
    ~CandidateWindow() { destroy(); }

    CandidateWindow(const CandidateWindow&) = delete;
    CandidateWindow& operator=(const CandidateWindow&) = delete;

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
        if (hwnd_ != nullptr && IsWindow(hwnd_)) {
            DestroyWindow(hwnd_);
            hwnd_ = nullptr;
        }
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

        WNDCLASSEXW wc{};
        wc.cbSize = sizeof(wc);
        wc.style = CS_DROPSHADOW | CS_HREDRAW | CS_VREDRAW;
        wc.lpfnWndProc = CandidateWndProc;
        wc.hInstance = hinst;
        wc.hCursor = LoadCursorW(nullptr, MAKEINTRESOURCEW(32512));
        wc.lpszClassName = L"ChaosetCandidateWndClass";
        RegisterClassExW(&wc);

        hwnd_ = CreateWindowExW(
            WS_EX_TOOLWINDOW | WS_EX_TOPMOST | WS_EX_NOACTIVATE,
            L"ChaosetCandidateWndClass",
            L"ChaosetCandidate",
            WS_POPUP,
            0, 0, 0, 0,
            nullptr, nullptr, hinst, nullptr
        );
        if (hwnd_ != nullptr) {
            SetWindowLongPtrW(hwnd_, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(this));
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

        int totalW = (curX > pad ? curX - gap : curX) + pad + chevronBoxW;
        int winW = (std::max)(totalW, scale(120, dpi));

        chevronRect_.left = winW - chevronBoxW;
        chevronRect_.top = 0;
        chevronRect_.right = winW;
        chevronRect_.bottom = winH;

        dividerX_ = winW - chevronBoxW + scale(2, dpi);

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
                HBRUSH pillBrush = CreateSolidBrush(theme.highlightBg);
                SelectObject(memDC, pillBrush);
                HPEN nullPen = CreatePen(PS_NULL, 0, 0);
                SelectObject(memDC, nullPen);
                RoundRect(memDC, item.pillRect.left, item.pillRect.top,
                          item.pillRect.right, item.pillRect.bottom,
                          scale(12, dpi), scale(12, dpi));
                DeleteObject(pillBrush);
                DeleteObject(nullPen);

                // 蓝胶囊内键帽：半透白单层填充，无边框
                HBRUSH chipBrush = CreateSolidBrush(theme.highlightChipBg);
                SelectObject(memDC, chipBrush);
                HPEN chipPen = CreatePen(PS_NULL, 0, 0);
                SelectObject(memDC, chipPen);
                RoundRect(memDC, item.chipRect.left, item.chipRect.top,
                          item.chipRect.right, item.chipRect.bottom,
                          scale(6, dpi), scale(6, dpi));
                DeleteObject(chipBrush);
                DeleteObject(chipPen);

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
                SelectObject(memDC, chipBrush);
                SelectObject(memDC, chipPen);
                RoundRect(memDC, item.chipRect.left, item.chipRect.top,
                          item.chipRect.right, item.chipRect.bottom,
                          scale(6, dpi), scale(6, dpi));
                DeleteObject(chipBrush);
                DeleteObject(chipPen);

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
        SelectObject(memDC, divPen);
        MoveToEx(memDC, dividerX_, divY, nullptr);
        LineTo(memDC, dividerX_, divY + divH);
        DeleteObject(divPen);

        // 翻页箭头
        int chevX = dividerX_ + (w - dividerX_) / 2;
        bool hasNext = comp_.page + 1 < comp_.pageCount;
        bool hasPrev = comp_.page > 0;
        bool dirDown = hasNext || !hasPrev;
        int arm = scale(3, dpi);
        int chevY = (comp_.pageCount > 1) ? (h / 2 - scale(4, dpi)) : (h / 2);

        HPEN chevPen = CreatePen(PS_SOLID, scale(1, dpi) < 2 ? 1 : 2, theme.chevron);
        SelectObject(memDC, chevPen);
        if (dirDown) {
            MoveToEx(memDC, chevX - arm, chevY - scale(2, dpi), nullptr);
            LineTo(memDC, chevX, chevY + scale(2, dpi));
            LineTo(memDC, chevX + arm + 1, chevY - scale(2, dpi) - 1);
        } else {
            MoveToEx(memDC, chevX - arm, chevY + scale(2, dpi), nullptr);
            LineTo(memDC, chevX, chevY - scale(2, dpi));
            LineTo(memDC, chevX + arm + 1, chevY + scale(2, dpi) + 1);
        }
        DeleteObject(chevPen);

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

    bool ensureConnected() {
        if (sock_ != INVALID_SOCKET) return true;
        DWORD now = GetTickCount();
        if (now - lastFail_ < 200) return false;  // 快速重试冷却 200ms

        SOCKET s = INVALID_SOCKET;

        // 极速直连路径：IPv4 纯数字地址（如 127.0.0.1）直接使用 sockaddr_in，彻底绕过 GetAddrInfoW 造成的 50-500ms 耗时
        unsigned long ip = inet_addr(host_.c_str());
        if (ip != INADDR_NONE) {
            s = ::socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
            if (s != INVALID_SOCKET) {
                int nodelay = 1;
                ::setsockopt(s, IPPROTO_TCP, TCP_NODELAY, reinterpret_cast<const char*>(&nodelay), sizeof(nodelay));
                DWORD timeout = 500;
                ::setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                ::setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));

                sockaddr_in sa{};
                sa.sin_family = AF_INET;
                sa.sin_port = htons(static_cast<u_short>(port_));
                sa.sin_addr.s_addr = ip;
                if (::connect(s, reinterpret_cast<sockaddr*>(&sa), sizeof(sa)) == 0) {
                    sock_ = s;
                    return true;
                }
                ::closesocket(s);
                s = INVALID_SOCKET;
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
                    int nodelay = 1;
                    ::setsockopt(s, IPPROTO_TCP, TCP_NODELAY, reinterpret_cast<const char*>(&nodelay), sizeof(nodelay));
                    DWORD timeout = 500;
                    ::setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                    ::setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, reinterpret_cast<const char*>(&timeout), sizeof(timeout));
                    if (::connect(s, p->ai_addr, static_cast<int>(p->ai_addrlen)) == 0) break;
                    ::closesocket(s);
                    s = INVALID_SOCKET;
                }
                FreeAddrInfoW(res);
            }
        }

        if (s == INVALID_SOCKET) {
            lastFail_ = now;
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
                closeConn();
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
                closeConn();
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
        lastFail_ = 0;  // 主动断开（非连接失败）不进入冷却
    }

private:
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
        tm_ = ptim;
        if (tm_ != nullptr) tm_->AddRef();
        clientId_ = tid;

        ITfKeystrokeMgr* mgr = nullptr;
        if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
            keySinkAdvised_ = SUCCEEDED(mgr->AdviseKeyEventSink(clientId_, keySink(), TRUE));
            mgr->Release();
        }
        // 激活时预热建立 TCP 回环连接，消除首个字母按键冷启动建立连接耗时，彻底杜绝漏首字母
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
        return S_OK;
    }

    // ITfKeyEventSink
    HRESULT STDMETHODCALLTYPE OnSetFocus(BOOL fForeground) override {
        if (!fForeground) {
            candidateWnd_.hide();
        } else {
            // 获得焦点时确保后台连接处于活跃状态
            transport_.ensureConnected();
        }
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyDown(ITfContext* /*pic*/, WPARAM wParam, LPARAM /*lParam*/,
                                            BOOL* pfEaten) override {
        // Shift 事件始终接住用于空按跟踪（最终不消费，见 OnKeyDown）
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) *pfEaten = TRUE;
        else if (enMode_) *pfEaten = FALSE;           // 英文模式全放行
        else *pfEaten = wouldHandle(wParam) ? TRUE : FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                        BOOL* pfEaten) override {
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            onShiftEvent(false);
            *pfEaten = FALSE;  // 只跟踪不消费，Shift 组合键不受影响
            return S_OK;
        }
        sinceShift_ = true;  // 其他键按下：取消 Shift 空按窗口
        *pfEaten = handleKey(wParam, pic) ? TRUE : FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyUp(ITfContext* /*pic*/, WPARAM wParam, LPARAM /*lParam*/,
                                          BOOL* pfEaten) override {
        *pfEaten = (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) ? TRUE : FALSE;
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
    HRESULT STDMETHODCALLTYPE OnCompositionTerminated(TfEditCookie /*ec*/, ITfComposition* /*pComposition*/) override {
        candidateWnd_.hide();
        composition_ = nullptr;
        session_.reset();
        comp_ = chaoset::Comp();
        return S_OK;
    }

    void selectCandidateByIndex(int index) {
        if (!hasCandidates()) return;
        ITfContext* ctx = focusContext();
        std::string committed;
        if (session_.select(index, &committed)) {
            commit(ctx, committed);
        }
    }

    void flipPage(bool next) {
        if (!hasCandidates() || comp_.pageCount <= 1) return;
        ITfContext* ctx = focusContext();
        comp_ = next ? session_.nextPage() : session_.prevPage();
        if (!comp_.ok) {
            endComposition();
            return;
        }
        render(ctx);
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
            }
            sinceShift_ = false;
        }
    }

    // OEM 虚键 → 标点字符（与 engine.IsPunctKey 的直映子集对应；
    // shift+键产生的 ？!: 等需字符级识别，待真机联调时接 ToAscii）
    static const char* oemPunctChar(WPARAM vk) {
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
            // 组词中支持 Ctrl+Backspace 快速清空拼音；其余 Ctrl 组合键（如 Ctrl+A/C/V/Z）全部放行
            return isComposing() && (vk == kVkBackspace);
        }

        if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) return true;

        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) return true;
        const bool isPageKey = (vk == kVkOemMinus || vk == kVkOemPlus ||
                                vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets ||
                                vk == kVkPrior || vk == kVkNext);
        if (isPageKey && isComposing()) return hasCandidates();
        if (oemPunctChar(vk) != nullptr) return true;
        if (!isComposing()) return false;
        return vk == kVkBackspace || vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
               (vk >= kVkFirstDigit && vk <= kVkLastDigit);
    }

    bool handleKey(WPARAM vk, ITfContext* pic) {
        if (isModifierKey(vk)) return false;

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
            // 其余 Ctrl 组合键（如 Ctrl+A/C/V/Z）：若处于组词中，先清空未决预编辑与候选窗，放行原键给宿主
            if (isComposing()) {
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
            }
            return false;
        }

        // 字母：追加组词
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) {
            std::string ch(1, static_cast<char>('a' + (vk - kVkFirstLetter)));
            comp_ = session_.input(ch);
            if (!comp_.ok) {
                // 若偶发连接断开或冷启动未连上，立即关闭重连并重试一次，绝不轻易泄露首字母到宿主程序
                transport_.closeConn();
                if (transport_.ensureConnected()) {
                    comp_ = session_.input(ch);
                }
            }
            if (!comp_.ok) {
                // daemon 确实不可达：清状态放行原键，保证宿主应用不卡死
                endComposition();
                return false;
            }
            render(pic);
            return true;
        }

        // 候选翻页：- / =、[ / ]、PageUp / PageDown（组词中且有候选时消费；空组词放行原键/出标点）
        const bool isPageKey = (vk == kVkOemMinus || vk == kVkOemPlus ||
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

        // 标点键（US 布局 OEM 映射）：组词中先上屏首选再出中文标点，
        // 空组词直接出中文标点；映射规则在 daemon 侧统一完成。
        if (const char* key = oemPunctChar(vk)) {
            if (vk == kVkOemQuotes && isComposing()) {
                // 组词中的撇号是音节分隔符，走组词流程
                comp_ = session_.input("'");
                if (!comp_.ok) {
                    transport_.closeConn();
                    if (transport_.ensureConnected()) {
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
            if (session_.punct(key, &committed)) {
                commit(pic, committed);
                return true;
            }
            // daemon 不可达：清状态放行原键，不卡宿主应用
            if (composing) {
                session_.reset();
                endComposition();
                comp_ = chaoset::Comp();
            }
            return false;
        }

        if (!isComposing()) return false;

        // Shift+空格：切换全角/半角标点模式（优先于首选上屏；
        // GetKeyState 查物理 Shift 状态，通信失败时 toggle 返回 false
        // 仅表示未切到半角，不影响后续按键）
        if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
            session_.toggleHalfWidth();
            return true;
        }

        if (vk == kVkBackspace) {
            if (GetKeyState(VK_CONTROL) & 0x8000) {
                // Ctrl+Backspace：直接全部删除（清空全部拼音）
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

        if (vk >= kVkFirstDigit && vk <= kVkLastDigit && hasCandidates()) {
            int index = static_cast<int>(vk - kVkFirstDigit);
            std::string committed;
            if (session_.select(index, &committed)) {
                commit(pic, committed);
            }
            return true;
        }

        if (vk == kVkReturn && isComposing()) {
            std::string raw = comp_.input;
            session_.reset();
            commit(pic, raw);
            return true;
        }

        if (vk == kVkSpace && hasCandidates()) {
            std::string committed;
            if (session_.select(0, &committed)) {
                commit(pic, committed);
            }
            return true;
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
        return ctx;
    }

    // 把引擎候选状态渲染为预编辑合成与候选悬浮窗
    void render(ITfContext* pic) {
        ITfContext* ctx = pic != nullptr ? pic : focusContext();
        if (ctx == nullptr) return;
        caretFound_ = false;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyPreedit(c, ec);
        });
        es.run(tm_, clientId_);
        es.Release();

        if (comp_.candidates.empty()) {
            candidateWnd_.hide();
            return;
        }

        if (!caretFound_ || (caretPos_.x == 0 && caretPos_.y == 0)) {
            GUITHREADINFO gti{};
            gti.cbSize = sizeof(gti);
            if (GetGUIThreadInfo(0, &gti) && gti.hwndCaret != nullptr) {
                POINT pt{gti.rcCaret.left, gti.rcCaret.bottom};
                ClientToScreen(gti.hwndCaret, &pt);
                caretPos_ = pt;
                caretLineHeight_ = (gti.rcCaret.bottom > gti.rcCaret.top) ? (gti.rcCaret.bottom - gti.rcCaret.top) : 20;
                caretFound_ = true;
            } else if (GetGUIThreadInfo(0, &gti) && gti.hwndFocus != nullptr) {
                POINT pt{gti.rcCaret.left, gti.rcCaret.bottom};
                ClientToScreen(gti.hwndFocus, &pt);
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

    void applyPreedit(ITfContext* ctx, TfEditCookie ec) {
        std::wstring wide = toWide(comp_.preedit);
        if (composition_ != nullptr) {
            ITfRange* range = nullptr;
            if (SUCCEEDED(composition_->GetRange(&range)) && range != nullptr) {
                range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                ITfContextView* view = nullptr;
                if (SUCCEEDED(ctx->GetActiveView(&view)) && view != nullptr) {
                    RECT rc{};
                    BOOL fClipped = FALSE;
                    if (SUCCEEDED(view->GetTextExt(ec, range, &rc, &fClipped))) {
                        caretPos_.x = rc.left;
                        caretPos_.y = rc.bottom;
                        caretLineHeight_ = (rc.bottom > rc.top) ? (rc.bottom - rc.top) : 20;
                        caretFound_ = true;
                    }
                    view->Release();
                }
                range->Release();
            }
            return;
        }
        ITfInsertAtSelection* insert = nullptr;
        if (FAILED(ctx->QueryInterface(IID_ITfInsertAtSelection, reinterpret_cast<void**>(&insert)))) return;
        ITfRange* range = nullptr;
        // QUERYONLY：只取插入点位置的范围，不插文本
        if (SUCCEEDED(insert->InsertTextAtSelection(ec, TF_IAS_QUERYONLY, nullptr, 0, &range)) &&
            range != nullptr) {
            ITfContextComposition* cc = nullptr;
            if (SUCCEEDED(ctx->QueryInterface(IID_ITfContextComposition, reinterpret_cast<void**>(&cc)))) {
                if (SUCCEEDED(cc->StartComposition(ec, range, compositionSink(), &composition_))) {
                    if (composition_ != nullptr) {
                        range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                    }
                }
                cc->Release();
            }
            ITfContextView* view = nullptr;
            if (SUCCEEDED(ctx->GetActiveView(&view)) && view != nullptr) {
                RECT rc{};
                BOOL fClipped = FALSE;
                if (SUCCEEDED(view->GetTextExt(ec, range, &rc, &fClipped))) {
                    caretPos_.x = rc.left;
                    caretPos_.y = rc.bottom;
                    caretLineHeight_ = (rc.bottom > rc.top) ? (rc.bottom - rc.top) : 20;
                    caretFound_ = true;
                }
                view->Release();
            }
            range->Release();
        }
        insert->Release();
    }

    void commit(ITfContext* pic, const std::string& text) {
        candidateWnd_.hide();
        ITfContext* ctx = pic != nullptr ? pic : focusContext();
        if (ctx == nullptr) return;
        committed_ = text;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyCommit(c, ec);
        });
        es.run(tm_, clientId_);
        es.Release();
        comp_ = chaoset::Comp();
    }

    void applyCommit(ITfContext* /*ctx*/, TfEditCookie ec) {
        std::wstring wide = toWide(committed_);
        if (composition_ != nullptr) {
            ITfRange* range = nullptr;
            if (SUCCEEDED(composition_->GetRange(&range)) && range != nullptr) {
                range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                range->Release();
            }
            composition_->EndComposition(ec);
            composition_->Release();
            composition_ = nullptr;
        }
        committed_.clear();
    }

    void endComposition() {
        candidateWnd_.hide();
        if (composition_ == nullptr) return;
        ITfContext* ctx = focusContext();
        if (ctx != nullptr) {
            ITfRange* range = nullptr;
            if (SUCCEEDED(composition_->GetRange(&range)) && range != nullptr) {
                range->SetText(ecScratch(ctx), 0, L"", 0);
                range->Release();
            }
            composition_->EndComposition(ecScratch(ctx));
        }
        composition_->Release();
        composition_ = nullptr;
    }

    // endComposition 无 pic 可用时的占位 cookie：
    // 清空预编辑属于残留清理，失败也不影响宿主（合成由 TSF 回收）。
    static TfEditCookie ecScratch(ITfContext* /*ctx*/) { return 0; }

    LONG ref_ = 1;
    ITfThreadMgr* tm_ = nullptr;
    TfClientId clientId_ = 0;
    bool keySinkAdvised_ = false;

    CandidateWindow candidateWnd_;
    POINT caretPos_{0, 0};
    int caretLineHeight_ = 20;
    bool caretFound_ = false;

    TcpTransport transport_;
    chaoset::Session session_{transport_};
    chaoset::Comp comp_;
    ITfComposition* composition_ = nullptr;
    std::string committed_;
};

void CandidateWindow::onLButtonDown(int x, int y) {
    POINT pt{ x, y };
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

BOOL WINAPI DllMain(HINSTANCE hinstDLL, DWORD fdwReason, LPVOID) {
    if (fdwReason == DLL_PROCESS_ATTACH) {
        g_hInst = hinstDLL;
    }
    return TRUE;
}

STDAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void** ppv) {
    if (!IsEqualCLSID(rclsid, CLSID_ChaosetTIP)) return CLASS_E_CLASSNOTAVAILABLE;
    static ChaosetFactory factory;  // 无状态，静态实例即可
    return factory.QueryInterface(riid, ppv);
}

STDAPI DllCanUnloadNow() {
    return g_liveObjects == 0 ? S_OK : S_FALSE;  // 常驻：S_FALSE 最安全
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
