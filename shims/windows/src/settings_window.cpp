// 设置窗（三端一致：macOS SettingsWindow.swift 同构；由 chaoset-ui.exe 创建）。
// 叶子模块：不得 include/引用 TIP TU（tip.hpp / tip_*.cpp）的类型；
// 设置窗可活过 TIP（不持有 TIP 指针，审计 P1-2）。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <windowsx.h>  // GET_X_LPARAM / GET_Y_LPARAM（设置窗导航命中）
#include <commdlg.h>    // GetSaveFileNameW（导出学习数据）
#include <shlobj.h>     // SHGetFolderPathW（导入词库定位 %LOCALAPPDATA%）
#include <cderr.h>      // FNERR_BUFFERTOOSMALL（多选缓冲区不足）
#include <shellapi.h>
#include <cwchar>
#include <string>
#include <vector>

#include "core.hpp"
#include "settings_window.hpp"
#include "config.hpp"
#include "transport.hpp"
#include "daemon.hpp"
#include "diagnostics.hpp"
#include "theme.hpp"
#include "winutil.hpp"

namespace chaoset::win {

// 存活中的设置窗口数量。设置窗口过程同样指向本 DLL 代码，开着时禁止
// 宿主 FreeLibrary（与候选窗同一防卸载机制，见 DllCanUnloadNow）。
 volatile LONG g_liveSettingsWindows = 0;

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
static HBRUSH g_settingsBgBrush = nullptr;    // 窗口背景刷（主题切换时重建）
static HBRUSH g_settingsCardBrush = nullptr; // 卡片背景刷（子控件擦除背景必须用它）
static bool g_settingsDark = false;           // 当前系统是否深色
static bool g_settingsAssociate = true;     // 词组联想开关状态（自绘复选框）
// 中英状态作用域缓存（方案 A）：打开设置窗时从 chaoset-settingsctl get 读到，
// 点选时经 set 写入。自绘控件每帧都读它——绝不能在 WM_DRAWITEM 里读注册表/
// 起进程（每次重绘都发一次进程会明显拖慢设置窗）。
static ModeScope g_settingsModeScope = ModeScope::System;

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

// 卡片定义（pt，y 相对内容区顶部、h 含上下内边距）。卡片标题由 settingsPaint
// 绘制；卡片内控件按「标题带 + 行」固定节奏摆放（kSettingsCtl），标题带占
// y+12..y+34，控件从 y+42 开始——两者不重叠。此前用一组固定坐标手摆控件，
// 第二张卡的标题（中英状态）被作用域按钮压住（2026-11-06 用户截图）。
struct SettingsCardDef {
    int y;
    int h;
    const wchar_t* caption;
};

static const SettingsCardDef kInputCards[] = {
    {0, 108, L"输入行为"},
    {122, 110, L"中英状态"},
};
static const SettingsCardDef kDictionaryCards[] = {
    {0, 134, L"学习数据"},
    {146, 110, L"词库"},
};
static const SettingsCardDef kAboutCards[] = {
    {0, 150, L"关于"},
};

// 返回本页卡片表与数量（数组顺序即绘制顺序）。
static const SettingsCardDef* settingsCards(SettingsPage page, int* count) {
    switch (page) {
        case SettingsPage::Input:
            *count = static_cast<int>(sizeof(kInputCards) / sizeof(kInputCards[0]));
            return kInputCards;
        case SettingsPage::Dictionary:
            *count = static_cast<int>(sizeof(kDictionaryCards) / sizeof(kDictionaryCards[0]));
            return kDictionaryCards;
        case SettingsPage::About:
            *count = static_cast<int>(sizeof(kAboutCards) / sizeof(kAboutCards[0]));
            return kAboutCards;
    }
    *count = 0;
    return nullptr;
}

// 本页内容总高度（滚动条范围）。当前 640×560pt 下三页都放得下，滚动代码留作
// 以后加设置项时直接生效。
static int settingsPageContentHeight(SettingsPage page) {
    int count = 0;
    const SettingsCardDef* cards = settingsCards(page, &count);
    if (cards == nullptr || count == 0) return 0;
    const SettingsCardDef& last = cards[count - 1];
    return last.y + last.h;
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
        return {RGB(0x1A, 0x18, 0x16), RGB(0xF4, 0xF2, 0xEE), RGB(0xA0, 0x9B, 0x94),
                RGB(0xE2, 0x7B, 0x38), RGB(0x44, 0x3E, 0x38)};
    }
    return {RGB(0xF6, 0xF3, 0xEC), RGB(0x1D, 0x1D, 0x1F), RGB(0x78, 0x73, 0x6C),
            RGB(0xD1, 0x6B, 0x29), RGB(0xE2, 0xDC, 0xD2)};
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

// 卡片底色：自绘子控件的背景必须与所在卡片一致。此前统一用窗口背景刷，
// 卡片（0x2A2A2E）上会留下一条窗口底色（0x1E1E20）的横条——用户截图里
// 「设置项标题被挡住」的观感就是它（2026-11-06）。
static COLORREF settingsCardColor() {
    return g_settingsDark ? RGB(0x25, 0x22, 0x1F) : RGB(0xFF, 0xFF, 0xFF);
}

// 子控件（Static / 自绘按钮）擦除背景统一用它；刷子还没建好时退回窗口刷。
static HBRUSH settingsControlBg() {
    return g_settingsCardBrush != nullptr ? g_settingsCardBrush : g_settingsBgBrush;
}

// 主题切换：重建背景/卡片刷、标题栏、重绘所有自绘控件（候选窗/Toast 另有实现）。
static void settingsUpdateTheme(HWND wnd) {
    g_settingsDark = isSystemDarkTheme();
    if (g_settingsBgBrush != nullptr) DeleteObject(g_settingsBgBrush);
    g_settingsBgBrush = CreateSolidBrush(settingsColors(g_settingsDark).bg);
    if (g_settingsCardBrush != nullptr) DeleteObject(g_settingsCardBrush);
    g_settingsCardBrush = CreateSolidBrush(settingsCardColor());
    settingsApplyDarkTitleBar(wnd, g_settingsDark);
    for (int id : {101, 102, 105, 106, 107, 108, 110, 112, 121, 122, 124}) {
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

// 控件相对内容区左上角的坐标（pt），必须落在上面卡片定义的「标题带之下」：
// 输入页卡 1 从 y=42 开始、卡 2 从 122+42=164 开始；词库页卡 1 从 42、卡 2 从
// 146+42=188 开始。x=16 即卡片内边距 kCardPad。
static const SettingsCtlItem kSettingsCtl[] = {
    // ---- 输入页 ----
    {101, SettingsPage::Input, 16, 42, 240, 26, 0},   // 词组联想
    {102, SettingsPage::Input, 16, 74, 436, 18, 2},   // 说明
    {121, SettingsPage::Input, 16, 164, 110, 28, 0},  // 全局共享
    {122, SettingsPage::Input, 134, 164, 110, 28, 0}, // 每进程独立
    {124, SettingsPage::Input, 16, 198, 436, 18, 2},  // 说明
    // ---- 词库页 ----
    {105, SettingsPage::Dictionary, 16, 42, 436, 18, 0},   // 学习数据统计
    {106, SettingsPage::Dictionary, 16, 66, 130, 28, 0},   // 清空
    {107, SettingsPage::Dictionary, 158, 66, 130, 28, 0},  // 导出
    {108, SettingsPage::Dictionary, 16, 100, 436, 18, 2},  // 说明
    {110, SettingsPage::Dictionary, 16, 188, 150, 28, 0},  // 导入词库
    {112, SettingsPage::Dictionary, 16, 222, 436, 18, 2},  // 说明
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
    const COLORREF navBg = g_settingsDark ? RGB(0x20, 0x1E, 0x1B) : RGB(0xEF, 0xEB, 0xE2);
    HBRUSH navBrush = CreateSolidBrush(navBg);
    FillRect(dc, &nav, navBrush);
    DeleteObject(navBrush);

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

    // 5) 右侧内容卡片：卡片底 + 卡片标题（自绘）；交互控件是子窗口、叠在卡片上。
    //    标题带固定 top+12..top+34，卡片内控件从 top+42 起（kSettingsCtl 同口径）。
    const RECT content = settingsContentRect();
    const int cardX = settingsPt(content.left);
    const int cardW = settingsPt(content.right - content.left);
    const int baseY = content.top - g_settingsScroll;
    int cardCount = 0;
    const SettingsCardDef* cards = settingsCards(g_settingsPage, &cardCount);
    for (int i = 0; i < cardCount; ++i) {
        const SettingsCardDef& card = cards[i];
        RECT rc{cardX, settingsPt(baseY + card.y), cardX + cardW,
                settingsPt(baseY + card.y + card.h)};
        settingsFillRoundRect(dc, rc, settingsPt(kCardRadius), settingsCardColor());
        SetTextColor(dc, c.secondary);
        HGDIOBJ of = SelectObject(dc, g_settingsFontBold);
        RECT cap{rc.left + settingsPt(kCardPad), rc.top + settingsPt(12),
                 rc.right - settingsPt(kCardPad), rc.top + settingsPt(34)};
        DrawTextW(dc, card.caption, -1, &cap, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, of);
    }
    if (g_settingsPage == SettingsPage::About) {
        // 关于页没有交互控件：正文直接画在卡片里；版本号只在页脚出现，避免重复。
        RECT dr{cardX + settingsPt(kCardPad), settingsPt(baseY + 44),
                cardX + cardW - settingsPt(kCardPad), settingsPt(baseY + 136)};
        SetTextColor(dc, c.text);
        HGDIOBJ of = SelectObject(dc, g_settingsFontHint);
        DrawTextW(dc, L"跨平台中文输入法（Windows TSF 壳）\n代码 MIT · 词库 CC BY-SA 4.0\n"
                      L"更多设置将在后续版本提供\n"
                      L"https://github.com/ShanWuYinShe/chaoset_ime",
                  -1, &dr, DT_LEFT | DT_WORDBREAK);
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

// 自绘「两档」按钮（中英状态作用域：全局共享 / 每进程独立）。选中项用主题
// 强调色描边 + 浅底，与自绘按钮同一套配色，深浅色都跟随主题（Win32 标准按钮
// 不跟随）。
static void settingsDrawChoice(const DRAWITEMSTRUCT* dis) {
    const SettingsColors c = settingsColors(g_settingsDark);
    HDC dc = dis->hDC;
    FillRect(dc, &dis->rcItem, settingsControlBg());
    const bool focused = (dis->itemState & ODS_FOCUS) != 0;
    // 读打开窗口时缓存的共享设置（settingsLoadShared）：WM_DRAWITEM 里绝不
    // 读注册表/起进程，重绘是高频动作（鼠标划过、主题切换都会触发）。
    const ModeScope current = g_settingsModeScope;
    bool selected = false;
    if (dis->CtlID == 121) selected = current == ModeScope::System;
    else if (dis->CtlID == 122) selected = current == ModeScope::Process;
    RECT r = dis->rcItem;
    const COLORREF fillColor = selected
                                   ? (g_settingsDark ? RGB(0x3E, 0x28, 0x18) : RGB(0xFD, 0xF3, 0xEB))
                                   : (g_settingsDark ? RGB(0x2A, 0x26, 0x23) : RGB(0xFA, 0xF7, 0xF2));
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
    FillRect(dc, &dis->rcItem, settingsControlBg());
    const bool pressed = (dis->itemState & ODS_SELECTED) != 0;
    RECT r = dis->rcItem;
    const COLORREF fillColor = pressed ? c.border
                                      : (g_settingsDark ? RGB(0x2E, 0x2A, 0x26) : RGB(0xF6, 0xF2, 0xEA));
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
    FillRect(dc, &dis->rcItem, settingsControlBg());
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

// ---- 方案 A（冷路径）：设置项读写统一走 Go chaoset-settingsctl ----
//
// 设置窗是**冷路径**（用户手动打开、低频），允许起进程；但每次调用必须有硬
// 超时，UI 线程绝不无限等待（任务契约 ≤1s；正常一次 get/set 在 100ms 内）。
// 输入热路径（TSF 回调）不经过这里，仍读 config.cpp 的 TTL 缓存。
//
// 可执行文件定位：<ChaosetIME.dll 所在目录>\chaoset-settingsctl.exe。设置窗
// 代码编在 DLL 里、由同目录 chaoset-ui.exe 宿主加载，g_hInst 是 DLL 模块句柄，
// 与安装/便携包布局（Makefile package-windows 把 CLI 放进同一目录）一致。
//
// 回退：CLI 不存在 / 起不来 / 超时 / 非 0 退出 / JSON 非法 → 回退原生 HKCU
// 读写（RegGetAssociate/RegSetAssociate/uiLoadModeScope/uiSaveModeScope），
// 保证旧安装包、便携包、被杀软拦截时设置窗仍可用；回退只记一条限流 diag。
static const DWORD kSettingsCtlTimeoutMs = 1000;   // ≤1s 硬上限（任务契约）
static const size_t kSettingsCtlMaxOutput = 4096;  // get 契约是单行 JSON，防御上限

static std::wstring settingsCtlPath() {
    wchar_t mod[MAX_PATH] = {0};
    const DWORD n = GetModuleFileNameW(g_hInst, mod, MAX_PATH);
    if (n == 0 || n >= MAX_PATH) return std::wstring();
    std::wstring path(mod, n);
    const size_t slash = path.find_last_of(L"\\/");
    if (slash == std::wstring::npos) return std::wstring();
    return path.substr(0, slash + 1) + L"chaoset-settingsctl.exe";
}

// 方案 A 回退诊断：CLI 缺失/失败是「每开一次窗、每点一次」都会发生的事，
// 不能把 tsf.log 刷爆；60s 内只记一条（diagBudget 本身还有 200 行总预算）。
static void settingsDiagCtlFallback(const char* op) {
    static ULONGLONG lastTick = 0;
    const ULONGLONG now = GetTickCount64();
    if (lastTick != 0 && now - lastTick < 60000) return;
    lastTick = now;
    if (!diagBudget()) return;
    diagPrintf(L"tsf.log", "settings ctl fallback op=%s (native HKCU read/write)\n", op);
}

// 运行 chaoset-settingsctl 并捕获 stdout（UTF-8）。timeoutMs 内未退出则
// TerminateProcess 并返回 false——调用方在 UI 线程，必须有硬上限。
// 只在进程正常退出（退出码 0）时才读 stdout，避免超时/被杀时读管道阻塞。
static bool settingsRunCtl(const std::wstring& args, std::string* out, DWORD timeoutMs) {
    if (out != nullptr) out->clear();
    const std::wstring exe = settingsCtlPath();
    if (exe.empty()) return false;
    // 缺文件是最常见的回退路径：先 stat，避免无谓的 CreateProcessW 失败。
    if (GetFileAttributesW(exe.c_str()) == INVALID_FILE_ATTRIBUTES) return false;

    SECURITY_ATTRIBUTES sa{};
    sa.nLength = sizeof(sa);
    sa.bInheritHandle = TRUE;
    HANDLE outRead = nullptr;
    HANDLE outWrite = nullptr;
    if (!CreatePipe(&outRead, &outWrite, &sa, 0)) return false;
    SetHandleInformation(outRead, HANDLE_FLAG_INHERIT, 0);
    HANDLE nulIn = CreateFileW(L"NUL", GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE,
                               &sa, OPEN_EXISTING, 0, nullptr);
    HANDLE nulErr = CreateFileW(L"NUL", GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE,
                                &sa, OPEN_EXISTING, 0, nullptr);

    STARTUPINFOW si{};
    si.cb = sizeof(si);
    si.dwFlags = STARTF_USESTDHANDLES;
    si.hStdInput = nulIn;
    si.hStdOutput = outWrite;
    si.hStdError = nulErr;
    std::wstring cmd = settingsQuote(exe) + L" " + args;
    PROCESS_INFORMATION pi{};
    const BOOL ok = CreateProcessW(exe.c_str(), cmd.data(), nullptr, nullptr, TRUE,
                                   CREATE_NO_WINDOW, nullptr, nullptr, &si, &pi);
    // 父进程必须立刻关掉写端，否则读端等不到 EOF。
    CloseHandle(outWrite);
    if (nulIn != nullptr && nulIn != INVALID_HANDLE_VALUE) CloseHandle(nulIn);
    if (nulErr != nullptr && nulErr != INVALID_HANDLE_VALUE) CloseHandle(nulErr);
    if (!ok) {
        CloseHandle(outRead);
        return false;
    }

    bool success = false;
    const DWORD wait = WaitForSingleObject(pi.hProcess, timeoutMs);
    if (wait == WAIT_OBJECT_0) {
        DWORD code = 1;
        if (GetExitCodeProcess(pi.hProcess, &code) && code == 0) success = true;
    } else {
        // 超时：杀掉避免残留进程；本次按失败回退（不再读管道）。
        TerminateProcess(pi.hProcess, 2);
        WaitForSingleObject(pi.hProcess, 100);
    }
    std::string data;
    if (success) {
        char buf[512];
        DWORD read = 0;
        while (ReadFile(outRead, buf, sizeof(buf), &read, nullptr) && read > 0) {
            data.append(buf, read);
            if (data.size() >= kSettingsCtlMaxOutput) break;
        }
        // 去掉行尾换行/空白（契约是单行 JSON）。
        while (!data.empty() && (data.back() == '\n' || data.back() == '\r' || data.back() == ' ')) {
            data.pop_back();
        }
        if (data.empty() || data.size() > kSettingsCtlMaxOutput) success = false;
    }
    CloseHandle(outRead);
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    if (!success) return false;
    if (out != nullptr) *out = data;
    return true;
}

// 解析 get 的单行 JSON 契约（见 cmd/chaoset-settingsctl/main.go 与
// internal/settings/settings.go）：
//   {"associate":true,"mode_scope":"system","ui_english":false}
// 只认本窗用到的 associate + mode_scope；mode_scope 读取宽容归一化（三端一致）：
// system→System；process→Process；旧 window→Process；未知枚举一律算失败
//（回退原生，不把半个设置项当真源——写错键比读不到更糟）。
static bool settingsParseGetJSON(const std::string& json, bool* associate, ModeScope* scope) {
    if (associate == nullptr || scope == nullptr) return false;
    const size_t a = json.find("\"associate\":");
    const size_t m = json.find("\"mode_scope\":\"");
    if (a == std::string::npos || m == std::string::npos) return false;
    size_t p = a + sizeof("\"associate\":") - 1;
    while (p < json.size() && (json[p] == ' ' || json[p] == '\t')) ++p;
    if (p + 4 <= json.size() && json.compare(p, 4, "true") == 0) {
        *associate = true;
    } else if (p + 5 <= json.size() && json.compare(p, 5, "false") == 0) {
        *associate = false;
    } else {
        return false;
    }
    const size_t v = m + sizeof("\"mode_scope\":\"") - 1;
    const size_t q = json.find('"', v);
    if (q == std::string::npos) return false;
    const std::string raw = json.substr(v, q - v);
    if (raw == "system") *scope = ModeScope::System;
    else if (raw == "process") *scope = ModeScope::Process;
    else if (raw == "window") *scope = ModeScope::Process;  // 旧档迁移
    else return false;
    return true;
}

static const wchar_t* settingsScopeName(ModeScope scope) {
    return scope == ModeScope::Process ? L"process" : L"system";
}

// 冷路径读取「词组联想 + 中英状态作用域」：优先 Go 单一真源，失败回退原生。
static void settingsLoadShared() {
    std::string raw;
    if (settingsRunCtl(L"get", &raw, kSettingsCtlTimeoutMs)) {
        bool associate = true;
        ModeScope scope = ModeScope::System;
        if (settingsParseGetJSON(raw, &associate, &scope)) {
            g_settingsAssociate = associate;
            g_settingsModeScope = scope;
            return;
        }
        settingsDiagCtlFallback("get-json");
    } else {
        settingsDiagCtlFallback("get");
    }
    // 回退：旧安装包/便携包/CLI 被拦时保证设置窗仍能显示与修改。
    g_settingsAssociate = RegGetAssociate();
    g_settingsModeScope = uiLoadModeScope();
}

// 冷路径写设置项：成功返回 true；失败返回 false 并限流记一条 diag，
// 由调用方执行原生 HKCU 回退写。
static bool settingsWriteShared(const std::wstring& args, const char* op) {
    std::string ignored;
    if (settingsRunCtl(args, &ignored, kSettingsCtlTimeoutMs)) return true;
    settingsDiagCtlFallback(op);
    return false;
}

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
                (LOWORD(wParam) == 121 || LOWORD(wParam) == 122)) {
                // 中英状态作用域两档切换（方案 A 冷路径）：写 Go 单一真源
                // （chaoset-settingsctl set mode-scope system|process），立即生效
                //（TIP 下次获得焦点时读注册表）。CLI 缺失/失败时回退原生
                // uiSaveModeScope。
                ModeScope scope = ModeScope::System;
                if (LOWORD(wParam) == 122) scope = ModeScope::Process;
                g_settingsModeScope = scope;
                const std::wstring scopeArg = L"set mode-scope " + std::wstring(settingsScopeName(scope));
                if (!settingsWriteShared(scopeArg, "set-mode-scope")) {
                    uiSaveModeScope(scope);
                }
                for (int id : {121, 122}) {
                    HWND b = GetDlgItem(hwnd, id);
                    if (b != nullptr) InvalidateRect(b, nullptr, TRUE);
                }
                return 0;
            }
            if (LOWORD(wParam) == 101 && HIWORD(wParam) == BN_CLICKED) {
                // 自绘复选框：手动翻转状态（BS_OWNERDRAW 不自动切换）。
                // 只写注册表，**不解引用 TIP**：设置窗的生命周期与 TIP 无关
                // （TSF 随时可能 Release TIP，而窗口还开着——此前把裸
                // TIP 对象指针 存在 GWLP_USERDATA，TIP 释放后点复选框就是
                // 宿主进程内 UAF，2026-10-30 三端一致性审计 P1-2）。
                //
                // 关闭联想时不再只能等下一次按键：用跨进程广播通知所有宿主
                // 进程的候选窗立即收起（候选窗自己转发给同进程 TIP），与 macOS
                // 设置窗通知即时收起对齐；设置窗仍不持有 TIP 指针，不引入 UAF。
                g_settingsAssociate = !g_settingsAssociate;
                // 方案 A 冷路径：写 Go 单一真源（chaoset-settingsctl set
                // associate on|off）；CLI 缺失/失败时回退原生 RegSetAssociate。
                const std::wstring assocArg =
                    L"set associate " + std::wstring(g_settingsAssociate ? L"on" : L"off");
                if (!settingsWriteShared(assocArg, "set-associate")) {
                    RegSetAssociate(g_settingsAssociate);
                }
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
                // 中英状态两档（121/122）：选中项高亮。
                if (dis->CtlID == 121 || dis->CtlID == 122) {
                    settingsDrawChoice(dis);
                    return TRUE;
                }
            }
            break;
        }
        case WM_CTLCOLORSTATIC: {
            // 说明文字用次级文本色（macOS secondaryLabel 对应），正文用主文本色。
            // 背景返回**卡片刷**：这些 Static 都在卡片上，用窗口背景刷会画出
            // 一条深色横条（2026-11-06 用户截图）。
            if (g_settingsBgBrush == nullptr) {
                g_settingsBgBrush = CreateSolidBrush(settingsColors(g_settingsDark).bg);
            }
            if (g_settingsCardBrush == nullptr) {
                g_settingsCardBrush = CreateSolidBrush(settingsCardColor());
            }
            const int id = GetDlgCtrlID(reinterpret_cast<HWND>(lParam));
            const SettingsColors c = settingsColors(g_settingsDark);
            const bool secondary = (id == 102 || id == 108 || id == 112 || id == 124);
            HDC dc = reinterpret_cast<HDC>(wParam);
            SetTextColor(dc, secondary ? c.secondary : c.text);
            SetBkMode(dc, TRANSPARENT);
            return reinterpret_cast<LRESULT>(settingsControlBg());
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
            if (g_settingsCardBrush != nullptr) { DeleteObject(g_settingsCardBrush); g_settingsCardBrush = nullptr; }
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
void openSettingsWindowStandalone(SettingsPage page) {
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
    // 方案 A 冷路径：打开设置窗时读 Go 单一真源（chaoset-settingsctl get，
    // ≤1s 硬超时）；失败回退原生 HKCU（旧安装包/便携包/CLI 被拦）。此时窗口
    // 尚未创建，最坏情况是开窗慢 1s，绝不会卡住任何宿主进程（设置窗属于
    // chaoset-ui.exe，与 TIP 进程无关）。
    settingsLoadShared();
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

    // 分组标题/页脚版本号/关于页正文由 settingsPaint 自绘（卡片标题），不再
    // 创建 Static 标题控件——旧实现里 100/103/104/109/111/113/120 这 7 个
    // 标题控件从未参与布局，既不可见又容易和卡片标题重复（2026-11-06）。
    // 交互控件用 BS_OWNERDRAW 子窗口，保留 Tab/键盘可达性；背景由自绘函数
    // 用卡片刷擦除（见 settingsControlBg）。
    auto makeCtl = [&](const wchar_t* ctlClass, DWORD ctlStyle, const wchar_t* text, int id) {
        return CreateWindowExW(0, ctlClass, text, WS_CHILD | WS_VISIBLE | ctlStyle,
                               0, 0, 0, 0, wnd,
                               reinterpret_cast<HMENU>(static_cast<INT_PTR>(id)), g_hInst, nullptr);
    };
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"词组联想", 101);
    makeCtl(L"Static", SS_LEFT, L"选词上屏后展示可续接的词组候选；关闭后不再弹出联想候选。", 102);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"全局共享", 121);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"每进程独立", 122);
    makeCtl(L"Static", SS_LEFT, L"切一次所有程序都生效（推荐）；每进程则各自独立。", 124);
    makeCtl(L"Static", SS_LEFT, L"读取中…", 105);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"清空学习数据…", 106);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"导出学习数据…", 107);
    makeCtl(L"Static", SS_LEFT, L"学习数据仅保存在本机，用于调频与词组联想；清空后不可恢复。", 108);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"导入词库…", 110);
    makeCtl(L"Static", SS_LEFT, L"支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。", 112);

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

}  // namespace chaoset::win
