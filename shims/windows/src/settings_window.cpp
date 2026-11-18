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
// 齿轮/托盘菜单 → 独立单例设置窗；只读写设置项、不访问 TIP（可活过 TIP），
// 学习数据经独立 PipeTransport 连 daemon。视觉：蓝白令牌（SettingsColors）+
// 自绘导航/卡片/页脚，跟随外观主题与 per-monitor DPI，控件用 BS_OWNERDRAW。
static HWND g_settingsWnd = nullptr;        // 单例句柄
static int g_settingsDpi = 96;              // 设置窗所在显示器 DPI（per-monitor）
static HFONT g_settingsFont = nullptr;      // 正文/行标签 13pt
static HFONT g_settingsFontBold = nullptr;  // 卡片标题/选中项 13pt semibold
static HFONT g_settingsFontHint = nullptr;  // 说明/页脚/关于页正文 11pt
static HFONT g_settingsFontTitle = nullptr; // 窗口标题/关于页标题 15pt semibold
static HICON g_settingsIcon = nullptr;      // 关于页应用图标（资源 1，按 DPI 重取）
static HBRUSH g_settingsBgBrush = nullptr;    // 窗口背景刷（主题切换时重建）
static HBRUSH g_settingsCardBrush = nullptr; // 卡片背景刷（子控件擦除背景必须用它）
static bool g_settingsDark = false;           // 当前系统是否深色
static bool g_settingsAssociate = true;     // 词组联想开关状态（自绘开关）
// 悬停状态：导航项由父窗口 WM_MOUSEMOVE 维护，自绘控件由窗口过程子类化维护。
// 单独存 id/index 而不是每帧查鼠标位置——重绘是高频动作，查窗口代价更高。
static int g_settingsHoverNav = -1;         // 悬停中的导航项（-1=无）
static int g_settingsHoverId = 0;           // 悬停中的自绘控件 id（0=无）
static bool g_settingsTrackingMouse = false;  // 父窗口是否已注册 TME_LEAVE
// 中英状态作用域缓存（方案 A）：打开设置窗时从 chaoset-settingsctl get 读到，
// 点选时经 set 写入。自绘控件每帧都读它——绝不能在 WM_DRAWITEM 里读注册表/
// 起进程（每次重绘都发一次进程会明显拖慢设置窗）。
static ModeScope g_settingsModeScope = ModeScope::System;
static Theme g_settingsTheme = Theme::System;  // 外观主题缓存，绘制时只读

// ---- 设置窗布局：左侧导航 + 右侧分组卡片 + 滚动 ----
// 导航/卡片背景自绘（Win32 标准控件不跟随系统深色），内容行用 BS_OWNERDRAW
// 子窗口保留原生可访问性；卡片标题自绘，控件按「标题带 + 行」固定节奏摆放。

// 几何令牌（pt，规范 §2）。kWinW/kWinH/kFooterH 被 tools/checkuiparity 的
// 「设置窗契约」逐字断言，改名或改值会让 make vet 变红。
static constexpr int kNavWidth = 148;      // 左侧导航宽度（pt）
static constexpr int kContentPadX = 24;    // 内容区左右外边距（pt）
static constexpr int kContentTop = 44;     // 内容区顶部（pt）
static constexpr int kFooterGap = 22;      // 内容区底边到页脚带（pt）
static constexpr int kCardPad = 18;        // 卡片内边距（pt）
static constexpr int kCardGap = 16;        // 卡片间距（pt）
static constexpr int kCardRadius = 14;     // 卡片圆角（pt）
static constexpr int kRowHeight = 36;      // 卡片内单行高度（pt）
static constexpr int kWinW = 640;          // 客户区宽度（pt）
static constexpr int kWinH = 560;          // 客户区高度（pt）
static constexpr int kFooterH = 34;        // 页脚（版本号）带高度（pt）

// 卡片内纵向节奏（规范 §3）：行与行 8、行与说明 10；控件高度按类型固定，
// 标题带固定在 y+16..38、内容从 y+50 起——标题与控件永不重叠。
static constexpr int kRowGap = 8;
static constexpr int kHintGap = 10;
static constexpr int kHintH = 18;
static constexpr int kHintH2 = 36;   // 两行说明（词库导入说明较长，408pt 内宽下会换行）
static constexpr int kChoiceH = 30;
static constexpr int kButtonH = 32;
static constexpr int kStatsH = 20;
// 内容区/卡片内宽（pt）：卡片横跨内容区，控件按卡片内宽排布。
static constexpr int kContentW = kWinW - 2 * kContentPadX - kNavWidth;  // 444
static constexpr int kInnerW = kContentW - 2 * kCardPad;                // 408

static SettingsPage g_settingsPage = SettingsPage::Input;
static int g_settingsScroll = 0;           // 内容区滚动偏移（pt）

// 当前页可用的内容区尺寸（已扣除导航/边距/页脚带）。
static RECT settingsContentRect() {
    RECT r{};
    r.left = kNavWidth + kContentPadX;
    r.top = kContentTop;
    r.right = kWinW - kContentPadX;
    r.bottom = kWinH - kFooterH - kFooterGap;
    return r;
}

// 卡片定义（pt，y 相对内容区顶部、h 含上下内边距）。卡片标题由 settingsPaint
// 绘制；控件按「标题带 + 行」摆放（kSettingsCtl），标题带 y+16..38、内容 y+50 起。
struct SettingsCardDef {
    int y;
    int h;
    const wchar_t* caption;
};

// 卡片高度 = 内容起点 50 + 内容块高 + 底部内边距 kCardPad；第二张卡的 y 由
// 第一张的 y+h 加 kCardGap 推导，控件表 kSettingsCtl 引用同一常量，改高度时
// 坐标不会互相脱节。
static constexpr int kInputCard1H = 50 + kRowHeight + kHintGap + kHintH + kCardPad;   // 132
static constexpr int kInputCard2Y = kInputCard1H + kCardGap;                          // 148
static constexpr int kInputCard2H = 50 + kChoiceH + kHintGap + kHintH + kCardPad;     // 126
static constexpr int kInputCard3Y = kInputCard2Y + kInputCard2H + kCardGap;
static constexpr int kInputCard3H = kInputCard2H;
static constexpr int kDictCard1H = 50 + kStatsH + kRowGap + kButtonH + kHintGap + kHintH + kCardPad;  // 156
static constexpr int kDictCard2Y = kDictCard1H + kCardGap;                            // 172
static constexpr int kDictCard2H = 50 + kButtonH + kHintGap + kHintH2 + kCardPad;    // 146
static constexpr int kAboutCardH = 50 + 44 + 16 + 3 * 22 + kCardPad;                  // 194

static const SettingsCardDef kInputCards[] = {
    {0, kInputCard1H, L"输入行为"},
    {kInputCard2Y, kInputCard2H, L"中英状态"},
    {kInputCard3Y, kInputCard3H, L"外观主题"},
};
static const SettingsCardDef kDictionaryCards[] = {
    {0, kDictCard1H, L"学习数据"},
    {kDictCard2Y, kDictCard2H, L"词库"},
};
static const SettingsCardDef kAboutCards[] = {
    {0, kAboutCardH, L"关于"},
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

// 视觉令牌表（规范 §1：light/dark 两套 sRGB 值）。GDI 没有系统动态色，深色
// 靠这里手工取两套值 + 系统深色判定切换；所有绘制只读令牌，不写散落色值。
// 字段顺序与规范表一致，逐值对应（注释里的 hex 便于与文档核对）。
struct SettingsColors {
    COLORREF windowBg;
    COLORREF navBg;
    COLORREF cardBg;
    COLORREF cardBorder;
    COLORREF divider;
    COLORREF textPrimary;
    COLORREF textSecondary;
    COLORREF textTertiary;
    COLORREF accent;
    COLORREF accentStrong;
    COLORREF accentPressed;
    COLORREF accentSoft;
    COLORREF accentText;
    COLORREF controlBg;
    COLORREF controlHover;
    COLORREF controlBorder;
    COLORREF controlText;
    COLORREF knob;
    COLORREF danger;
    COLORREF onAccent;
    COLORREF shadow;  // 阴影基色（规范 §1）：GDI 无 alpha，按百分比混到窗口底
};

static SettingsColors settingsColors(bool dark) {
    if (dark) {
        return {
            RGB(0x10, 0x1A, 0x2C),  // windowBg #101A2C
            RGB(0x0C, 0x16, 0x26),  // navBg #0C1626
            RGB(0x1A, 0x27, 0x40),  // cardBg #1A2740
            RGB(0x2D, 0x3E, 0x5C),  // cardBorder #2D3E5C
            RGB(0x24, 0x35, 0x52),  // divider #243552
            RGB(0xF2, 0xF6, 0xFC),  // textPrimary #F2F6FC
            RGB(0xAF, 0xBF, 0xD6),  // textSecondary #AFBFD6
            RGB(0x7E, 0x90, 0xAC),  // textTertiary #7E90AC
            RGB(0x00, 0x66, 0xEB),  // accent #0066EB
            RGB(0x00, 0x7A, 0xFF),  // accentStrong #007AFF
            RGB(0x4D, 0xA3, 0xFF),  // accentPressed #4DA3FF
            RGB(0x16, 0x34, 0x5C),  // accentSoft #16345C
            RGB(0x7F, 0xB4, 0xFF),  // accentText #7FB4FF
            RGB(0x22, 0x33, 0x53),  // controlBg #223353
            RGB(0x2C, 0x41, 0x65),  // controlHover #2C4165
            RGB(0x3A, 0x50, 0x74),  // controlBorder #3A5074
            RGB(0xF2, 0xF6, 0xFC),  // controlText #F2F6FC
            RGB(0xF2, 0xF6, 0xFC),  // knob #F2F6FC
            RGB(0xFF, 0x7B, 0x72),  // danger #FF7B72
            RGB(0xFF, 0xFF, 0xFF),  // onAccent #FFFFFF
            RGB(0x00, 0x00, 0x00),  // shadow #000000（rgba 0.38 的基色）
        };
    }
    return {
        RGB(0xF4, 0xF7, 0xFB),  // windowBg #F4F7FB
        RGB(0xEA, 0xF1, 0xF9),  // navBg #EAF1F9
        RGB(0xFF, 0xFF, 0xFF),  // cardBg #FFFFFF
        RGB(0xD9, 0xE4, 0xF0),  // cardBorder #D9E4F0
        RGB(0xE4, 0xED, 0xF6),  // divider #E4EDF6
        RGB(0x14, 0x21, 0x3D),  // textPrimary #14213D
        RGB(0x4A, 0x5A, 0x70),  // textSecondary #4A5A70
        RGB(0x7A, 0x88, 0x99),  // textTertiary #7A8899
        RGB(0x00, 0x7A, 0xFF),  // accent #007AFF
        RGB(0x00, 0x66, 0xEB),  // accentStrong #0066EB
        RGB(0x00, 0x52, 0xBE),  // accentPressed #0052BE
        RGB(0xE5, 0xF1, 0xFF),  // accentSoft #E5F1FF
        RGB(0x00, 0x66, 0xEB),  // accentText #0066EB
        RGB(0xF0, 0xF5, 0xFB),  // controlBg #F0F5FB
        RGB(0xE3, 0xED, 0xF8),  // controlHover #E3EDF8
        RGB(0xC9, 0xD9, 0xEB),  // controlBorder #C9D9EB
        RGB(0x14, 0x21, 0x3D),  // controlText #14213D
        RGB(0xFF, 0xFF, 0xFF),  // knob #FFFFFF
        RGB(0xD3, 0x2F, 0x2F),  // danger #D32F2F
        RGB(0xFF, 0xFF, 0xFF),  // onAccent #FFFFFF
        RGB(0x1B, 0x4A, 0x80),  // shadow #1B4A80（rgba 0.12 的基色）
    };
}

// 颜色混合：把 fg 按 pct% 叠到 bg 上（等价 alpha 合成；GDI 无 alpha 只能预混）。
static COLORREF settingsBlendColor(COLORREF fg, COLORREF bg, int pct) {
    if (pct < 0) pct = 0;
    if (pct > 100) pct = 100;
    const int r = GetRValue(bg) + (GetRValue(fg) - GetRValue(bg)) * pct / 100;
    const int g = GetGValue(bg) + (GetGValue(fg) - GetGValue(bg)) * pct / 100;
    const int b = GetBValue(bg) + (GetBValue(fg) - GetBValue(bg)) * pct / 100;
    return RGB(r, g, b);
}

static int settingsPt(int v) { return MulDiv(v, g_settingsDpi, 72); }

// 描边宽度 pt 化（下限 1px）：1pt 在 96dpi 下仍是 1px，高缩放才变粗。
static int settingsStrokePx(int pt) { return (std::max)(1, MulDiv(pt, g_settingsDpi, 72)); }

static void settingsDeleteFonts() {
    if (g_settingsFont) { DeleteObject(g_settingsFont); g_settingsFont = nullptr; }
    if (g_settingsFontBold) { DeleteObject(g_settingsFontBold); g_settingsFontBold = nullptr; }
    if (g_settingsFontHint) { DeleteObject(g_settingsFontHint); g_settingsFontHint = nullptr; }
    if (g_settingsFontTitle) { DeleteObject(g_settingsFontTitle); g_settingsFontTitle = nullptr; }
}

static HFONT settingsMakeFont(int ptSize, int weight) {
    return CreateFontW(-settingsPt(ptSize), 0, 0, 0, weight, FALSE, FALSE, FALSE,
                       DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                       CLEARTYPE_QUALITY, DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei UI");
}

static void settingsCreateFonts() {
    g_settingsFont = settingsMakeFont(13, FW_NORMAL);        // 正文/行标签
    g_settingsFontBold = settingsMakeFont(13, FW_SEMIBOLD);  // 卡片标题/选中导航/选中 choice
    g_settingsFontHint = settingsMakeFont(11, FW_NORMAL);    // 说明/页脚/关于页正文
    g_settingsFontTitle = settingsMakeFont(15, FW_SEMIBOLD); // 窗口标题/关于页标题
}

// DWM 属性写入：dwmapi 动态加载（旧系统/服务器核心没有该 API，失败静默）。
static bool settingsDwmSetAttribute(HWND wnd, DWORD attr, const void* value, DWORD size) {
    typedef HRESULT(WINAPI* DwmSetWindowAttributeFn)(HWND, DWORD, LPCVOID, DWORD);
    static DwmSetWindowAttributeFn fn = []() -> DwmSetWindowAttributeFn {
        HMODULE mod = LoadLibraryW(L"dwmapi.dll");
        if (mod == nullptr) return nullptr;
        return reinterpret_cast<DwmSetWindowAttributeFn>(
            reinterpret_cast<void*>(GetProcAddress(mod, "DwmSetWindowAttribute")));
    }();
    return fn != nullptr && SUCCEEDED(fn(wnd, attr, value, size));
}

// 标题栏跟随暖色主题：Win10 1903+ 深色标题栏（属性 20，1809 用 19），Win11
// 再用 35/36 把标题栏底/字染成 windowBg/textPrimary——否则系统灰白标题栏与
// 暖色内容区割裂。35/36 在 Win10 上调用失败，静默忽略（不报错、不降级）。
static void settingsApplyTitleBarTheme(HWND wnd) {
    const BOOL dark = g_settingsDark ? TRUE : FALSE;
    if (!settingsDwmSetAttribute(wnd, 20, &dark, sizeof(dark))) {
        settingsDwmSetAttribute(wnd, 19, &dark, sizeof(dark));
    }
    const SettingsColors c = settingsColors(g_settingsDark);
    COLORREF caption = c.windowBg;
    COLORREF captionText = c.textPrimary;
    settingsDwmSetAttribute(wnd, 35, &caption, sizeof(caption));
    settingsDwmSetAttribute(wnd, 36, &captionText, sizeof(captionText));
    // Win11 圆角偏好设置（DWMWA_WINDOW_CORNER_PREFERENCE = 33, DWMWCP_ROUND = 2）
    const DWORD corner = 2;
    settingsDwmSetAttribute(wnd, 33, &corner, sizeof(corner));
}

// 卡片底色：自绘子控件的背景必须与所在卡片一致，否则卡片上会留下一条窗口
// 底色的横条（子控件擦除背景用错刷子的经典观感 bug）。
static COLORREF settingsCardColor() { return settingsColors(g_settingsDark).cardBg; }

// 子控件（Static / 自绘按钮）擦除背景统一用它；刷子还没建好时退回窗口刷。
static HBRUSH settingsControlBg() {
    return g_settingsCardBrush != nullptr ? g_settingsCardBrush : g_settingsBgBrush;
}

// 主题切换：重建背景/卡片刷、标题栏着色、重绘所有自绘控件（候选窗/Toast 另有实现）。
static void settingsUpdateTheme(HWND wnd) {
    g_settingsDark = uiEffectiveDark();  // 用户主题优先，system 跟随系统
    if (g_settingsBgBrush != nullptr) DeleteObject(g_settingsBgBrush);
    g_settingsBgBrush = CreateSolidBrush(settingsColors(g_settingsDark).windowBg);
    if (g_settingsCardBrush != nullptr) DeleteObject(g_settingsCardBrush);
    g_settingsCardBrush = CreateSolidBrush(settingsCardColor());
    settingsApplyTitleBarTheme(wnd);
    for (int id : {101, 102, 105, 106, 107, 108, 110, 112, 121, 122, 124, 131, 132, 133, 134}) {
        HWND c = GetDlgItem(wnd, id);
        if (c != nullptr) InvalidateRect(c, nullptr, TRUE);
    }
    InvalidateRect(wnd, nullptr, TRUE);
}

// 控件布局（pt 坐标；创建 / 切页 / 滚动 / WM_DPICHANGED 共用，单一口径）。
// 每个控件声明「所属页 + 卡片内相对位置」，这里统一加内容区偏移与滚动量；
// 未在当前页的控件直接 SWP_HIDEWINDOW，避免跨页串味。
struct SettingsCtlItem {
    int id;
    SettingsPage page;
    int x, y, w, h;   // 相对内容区左上角（pt）
    int fontKind;     // 0=正文 1=加粗 2=提示
};

// 控件相对内容区左上角的坐标（pt），必须落在卡片标题带（y+16..38）之下：
// 卡片内容统一从 y=50 起，x=kCardPad、宽按卡片内宽 kInnerW 排布；行与行 8、
// 行与说明 10。第二张卡的 y 引用卡片常量，卡片高度改动时这里自动跟随。
static const SettingsCtlItem kSettingsCtl[] = {
    // ---- 输入页 ----
    {101, SettingsPage::Input, kCardPad, 50, kInnerW, kRowHeight, 0},  // 词组联想（整行标签+开关）
    {102, SettingsPage::Input, kCardPad, 50 + kRowHeight + kHintGap, kInnerW, kHintH, 2},  // 说明
    {121, SettingsPage::Input, kCardPad, kInputCard2Y + 50, 110, kChoiceH, 0},             // 全局共享
    {122, SettingsPage::Input, kCardPad + 110 + kRowGap, kInputCard2Y + 50, 110, kChoiceH, 0},  // 每进程独立
    {124, SettingsPage::Input, kCardPad, kInputCard2Y + 50 + kChoiceH + kHintGap, kInnerW, kHintH, 2},  // 说明
    {131, SettingsPage::Input, kCardPad, kInputCard3Y + 50, 110, kChoiceH, 0},  // 跟随系统
    {132, SettingsPage::Input, kCardPad + 110 + kRowGap, kInputCard3Y + 50, 110, kChoiceH, 0},  // 浅色
    {133, SettingsPage::Input, kCardPad + 2 * (110 + kRowGap), kInputCard3Y + 50, 110, kChoiceH, 0},  // 深色
    {134, SettingsPage::Input, kCardPad, kInputCard3Y + 50 + kChoiceH + kHintGap, kInnerW, kHintH, 2},  // 说明
    // ---- 词库页 ----
    {105, SettingsPage::Dictionary, kCardPad, 50, kInnerW, kStatsH, 0},                    // 学习数据统计
    {106, SettingsPage::Dictionary, kCardPad, 50 + kStatsH + kRowGap, 130, kButtonH, 0},   // 清空
    {107, SettingsPage::Dictionary, kCardPad + 130 + kRowGap, 50 + kStatsH + kRowGap, 130, kButtonH, 0},  // 导出
    {108, SettingsPage::Dictionary, kCardPad, 50 + kStatsH + kRowGap + kButtonH + kHintGap, kInnerW, kHintH, 2},  // 说明
    {110, SettingsPage::Dictionary, kCardPad, kDictCard2Y + 50, 150, kButtonH, 0},         // 导入词库（主按钮）
    {112, SettingsPage::Dictionary, kCardPad, kDictCard2Y + 50 + kButtonH + kHintGap, kInnerW, kHintH2, 2},  // 说明（两行）
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

// 关于页图标按 DPI 取对应尺寸；创建/DPI 变化时重建，避免每次重绘 LoadImage。
static void settingsLoadIcon() {
    if (g_settingsIcon != nullptr) { DestroyIcon(g_settingsIcon); g_settingsIcon = nullptr; }
    const int size = settingsPt(44);
    g_settingsIcon = static_cast<HICON>(LoadImageW(g_hInst, MAKEINTRESOURCEW(1), IMAGE_ICON,
                                                   size, size, LR_DEFAULTCOLOR));
}

static void settingsApplyDpi(HWND wnd, int dpi) {
    if (dpi <= 0) dpi = systemDpi();
    g_settingsDpi = dpi;
    settingsDeleteFonts();
    settingsCreateFonts();
    settingsLoadIcon();
    settingsLayoutControls(wnd);
    InvalidateRect(wnd, nullptr, TRUE);
}

// ---- 设置窗自绘：导航/卡片/页脚/关于页（蓝白令牌）----

// 圆角填充矩形（卡片/胶囊/开关轨道共用）。radius 是圆角半径（pt）；GDI
// RoundRect 的后两个参数是圆角椭圆直径，因此这里乘 2 才能得到规范的圆角。
static void settingsFillRoundRect(HDC dc, const RECT& r, int radius, COLORREF fill) {
    HBRUSH brush = CreateSolidBrush(fill);
    HGDIOBJ oldBrush = SelectObject(dc, brush);
    HGDIOBJ oldPen = SelectObject(dc, GetStockObject(NULL_PEN));
    RoundRect(dc, r.left, r.top, r.right, r.bottom, radius * 2, radius * 2);
    SelectObject(dc, oldPen);
    SelectObject(dc, oldBrush);
    DeleteObject(brush);
}

// 圆角描边（卡片 1pt 边框 / 胶囊描边 / 焦点环共用；只描边不填充；radius 同口径）。
static void settingsStrokeRoundRect(HDC dc, const RECT& r, int radius, COLORREF color, int widthPx) {
    HPEN pen = CreatePen(PS_SOLID, widthPx, color);
    HGDIOBJ oldPen = SelectObject(dc, pen);
    HGDIOBJ oldBrush = SelectObject(dc, GetStockObject(NULL_BRUSH));
    RoundRect(dc, r.left, r.top, r.right, r.bottom, radius * 2, radius * 2);
    SelectObject(dc, oldBrush);
    SelectObject(dc, oldPen);
    DeleteObject(pen);
}

// 卡片柔和阴影：以多层外扩填充的微透明渐变阶梯模拟现代高斯阴影（y+2、blur≈12pt），
// 告别空心线框条纹，让卡片在浅色与深色背景上都具备自然精致的高级悬浮质感。
static void settingsDrawCardShadow(HDC dc, const RECT& card) {
    const SettingsColors c = settingsColors(g_settingsDark);
    const COLORREF shadow = c.shadow;
    const int steps = 4;
    for (int i = steps; i >= 1; --i) {
        RECT s = card;
        const int grow = settingsPt(i);
        InflateRect(&s, grow, grow);
        s.top += settingsPt(2);
        s.bottom += settingsPt(2);
        const int pct = g_settingsDark ? (38 * (steps - i + 1) / steps) : (12 * (steps - i + 1) / steps);
        settingsFillRoundRect(dc, s, settingsPt(kCardRadius + i),
                              settingsBlendColor(shadow, c.windowBg, pct));
    }
}

// 焦点环：统一 accent 1.5pt 圆角描边（inset 2pt），替代系统点线 DrawFocusRect。
static void settingsDrawFocusRing(HDC dc, const RECT& r, int radius) {
    RECT fr = r;
    InflateRect(&fr, -settingsPt(2), -settingsPt(2));
    settingsStrokeRoundRect(dc, fr, radius, settingsColors(g_settingsDark).accent,
                            (std::max)(1, MulDiv(3, g_settingsDpi, 144)));
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

// 导航项命中矩形（自绘 + 点击 + 悬停命中都用它，避免两套坐标）：112×34、间距 4。
static RECT settingsNavItemRect(int index) {
    RECT r{};
    r.left = 12;
    r.top = 64 + index * (34 + 4);
    r.right = kNavWidth - 24;  // 112pt 胶囊（列宽 148、左右各 12 内边距）
    r.bottom = r.top + 34;
    return r;
}

static void settingsPaint(HDC dc, HWND wnd) {
    const SettingsColors c = settingsColors(g_settingsDark);
    RECT client{};
    GetClientRect(wnd, &client);
    const int W = client.right - client.left;
    const int H = client.bottom - client.top;
    if (W <= 0 || H <= 0) return;

    // 1) 整窗背景 + 左侧导航列（navBg 比 windowBg 深一档，形成柔和分栏）
    FillRect(dc, &client, g_settingsBgBrush);
    RECT nav{0, 0, settingsPt(kNavWidth), H};
    HBRUSH navBrush = CreateSolidBrush(c.navBg);
    FillRect(dc, &nav, navBrush);
    DeleteObject(navBrush);
    RECT divRect{settingsPt(kNavWidth) - settingsStrokePx(1), 0, settingsPt(kNavWidth), H};
    HBRUSH divBrush = CreateSolidBrush(c.divider);
    FillRect(dc, &divRect, divBrush);
    DeleteObject(divBrush);

    // 2) 应用标题（13pt semibold textPrimary）。132pt 导航列下 15pt 的 CJK 标题
    //    在 96dpi（20px）会超宽被裁切，三端统一 13pt；x=10 与导航胶囊左缘对齐。
    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, c.textPrimary);
    HGDIOBJ oldFont = SelectObject(dc, g_settingsFontBold);
    RECT title{settingsPt(12), settingsPt(20), settingsPt(kNavWidth - 8), settingsPt(46)};
    DrawTextW(dc, L"Chaoset 设置", -1, &title, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, oldFont);

    // 3) 导航项：选中=accentSoft 胶囊 + accentText semibold；悬停未选=controlHover
    for (int i = 0; i < 3; ++i) {
        RECT r = settingsNavItemRect(i);
        RECT pr{settingsPt(r.left), settingsPt(r.top), settingsPt(r.right), settingsPt(r.bottom)};
        const bool selected = (static_cast<int>(g_settingsPage) == i);
        const bool hovered = (g_settingsHoverNav == i);
        if (selected) settingsFillRoundRect(dc, pr, settingsPt(10), c.accentSoft);
        else if (hovered) settingsFillRoundRect(dc, pr, settingsPt(10), c.controlHover);
        SetTextColor(dc, selected ? c.accentText : c.textPrimary);
        HGDIOBJ of = SelectObject(dc, selected ? g_settingsFontBold : g_settingsFont);
        RECT tr = pr;
        tr.left += settingsPt(14);
        DrawTextW(dc, settingsNavLabel(i), -1, &tr, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, of);
    }

    // 4) 卡片：柔和阴影 + cardBg 底 + 1pt cardBorder 描边 + 13pt semibold 标题；
    //    交互控件是子窗口、叠在卡片上（标题带 y+16..38，内容从 y+50 起）。
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
        settingsDrawCardShadow(dc, rc);
        settingsFillRoundRect(dc, rc, settingsPt(kCardRadius), c.cardBg);
        settingsStrokeRoundRect(dc, rc, settingsPt(kCardRadius), c.cardBorder, settingsStrokePx(1));
        SetTextColor(dc, c.textSecondary);
        HGDIOBJ of = SelectObject(dc, g_settingsFontBold);
        RECT cap{rc.left + settingsPt(kCardPad), rc.top + settingsPt(16),
                 rc.right - settingsPt(kCardPad), rc.top + settingsPt(38)};
        DrawTextW(dc, card.caption, -1, &cap, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, of);
    }

    // 5) 关于页：应用图标 + 标题/副标题 + 三行说明（版本号只在页脚，避免重复）
    if (g_settingsPage == SettingsPage::About) {
        const int x = cardX + settingsPt(kCardPad);
        const int right = cardX + cardW - settingsPt(kCardPad);
        const int icon = settingsPt(44);
        const int y = baseY + 50;
        if (g_settingsIcon != nullptr) {
            DrawIconEx(dc, x, settingsPt(y), g_settingsIcon, icon, icon, 0, nullptr, DI_NORMAL);
        }
        SetTextColor(dc, c.textPrimary);
        HGDIOBJ of = SelectObject(dc, g_settingsFontTitle);
        RECT tr{x + icon + settingsPt(14), settingsPt(y), right, settingsPt(y + 24)};
        DrawTextW(dc, L"Chaoset 输入法", -1, &tr, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, of);
        SetTextColor(dc, c.textSecondary);
        HGDIOBJ of2 = SelectObject(dc, g_settingsFontHint);
        RECT sub{tr.left, settingsPt(y + 24), right, settingsPt(y + 44)};
        DrawTextW(dc, L"跨平台中文输入法（Windows TSF 壳）", -1, &sub,
                  DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        const int ly = y + 44 + 16;
        RECT l1{x, settingsPt(ly), right, settingsPt(ly + 18)};
        DrawTextW(dc, L"代码 MIT · 词库 CC BY-SA 4.0", -1, &l1,
                  DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SetTextColor(dc, c.textTertiary);
        RECT l2{x, settingsPt(ly + 22), right, settingsPt(ly + 40)};
        DrawTextW(dc, L"更多设置将在后续版本提供", -1, &l2,
                  DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SetTextColor(dc, c.accentText);
        RECT l3{x, settingsPt(ly + 44), right, settingsPt(ly + 62)};
        DrawTextW(dc, L"https://github.com/ShanWuYinShe/chaoset_ime", -1, &l3,
                  DT_LEFT | DT_VCENTER | DT_SINGLELINE);
        SelectObject(dc, of2);
    }

    // 6) 页脚版本号（固定底部，不随滚动；11pt textSecondary）
    RECT footer{settingsPt(kNavWidth + kContentPadX), settingsPt(kWinH - 46),
                settingsPt(kWinW - kContentPadX), settingsPt(kWinH - 22)};
    SetTextColor(dc, c.textSecondary);
    HGDIOBJ ff = SelectObject(dc, g_settingsFontHint);
    wchar_t footerVer[128];
    swprintf_s(footerVer, L"Chaoset 版本 %hs", CHAOSET_VERSION);
    DrawTextW(dc, footerVer, -1, &footer, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, ff);

    // 7) 滚动条（内容超出一页时右侧细条，暖色 textTertiary）
    const int maxScroll = settingsMaxScroll();
    if (maxScroll > 0) {
        const int trackTop = settingsPt(content.top);
        const int trackH = settingsPt(content.bottom - content.top);
        const int thumbH = trackH * (content.bottom - content.top) /
                           (settingsPageContentHeight(g_settingsPage));
        const int thumbTop = trackTop + (trackH - thumbH) * g_settingsScroll / maxScroll;
        RECT thumb{settingsPt(kWinW - 8), thumbTop, settingsPt(kWinW - 4), thumbTop + thumbH};
        settingsFillRoundRect(dc, thumb, settingsPt(2), c.textTertiary);
    }
}

// 自绘开关整行（词组联想）：左侧 13pt 标签 + 右侧 38×22 开关轨道与 16pt 滑钮。
// 关=controlBg 轨道 + controlBorder；开=accent 轨道；hover 分别用 controlHover /
// accentPressed。整行都是点击热区（与旧复选框一致，点标签也能切换）。
static void settingsDrawSwitch(const DRAWITEMSTRUCT* dis) {
    const SettingsColors c = settingsColors(g_settingsDark);
    HDC dc = dis->hDC;
    FillRect(dc, &dis->rcItem, settingsControlBg());
    const bool hovered = (g_settingsHoverId == static_cast<int>(dis->CtlID));
    const bool on = g_settingsAssociate;
    const RECT row = dis->rcItem;
    const int trackW = settingsPt(38);
    const int trackH = settingsPt(22);
    RECT track{row.right - trackW, row.top + (row.bottom - row.top - trackH) / 2, row.right, 0};
    track.bottom = track.top + trackH;
    const int radius = settingsPt(11);
    const COLORREF trackFill = on ? (hovered ? c.accentPressed : c.accent)
                                  : (hovered ? c.controlHover : c.controlBg);
    settingsFillRoundRect(dc, track, radius, trackFill);
    settingsStrokeRoundRect(dc, track, radius, on ? trackFill : c.controlBorder,
                            settingsStrokePx(1));
    const int knob = settingsPt(16);
    const int pad = (trackH - knob) / 2;
    const int knobX = on ? (track.right - pad - knob) : (track.left + pad);
    RECT kr{knobX, track.top + pad, knobX + knob, track.top + pad + knob};
    RECT krShadow = kr;
    krShadow.top += settingsStrokePx(1);
    krShadow.bottom += settingsStrokePx(1);
    settingsFillRoundRect(dc, krShadow, knob / 2, on ? settingsBlendColor(c.accentPressed, c.accent, 40)
                                                     : settingsBlendColor(c.shadow, c.controlBg, 15));
    settingsFillRoundRect(dc, kr, knob / 2, c.knob);
    settingsStrokeRoundRect(dc, kr, knob / 2, on ? settingsBlendColor(c.accent, c.knob, 25)
                                                 : c.controlBorder, settingsStrokePx(1));
    wchar_t label[64] = {0};
    GetWindowTextW(dis->hwndItem, label, _countof(label));
    RECT tr = row;
    tr.right = track.left - settingsPt(12);
    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, c.textPrimary);
    HGDIOBJ oldFont = SelectObject(dc, g_settingsFont);
    DrawTextW(dc, label, -1, &tr, DT_LEFT | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, oldFont);
    if ((dis->itemState & ODS_FOCUS) != 0) {
        // 焦点环画在开关外圈（accent 环画在 accent 轨道里会看不见）。
        RECT fr = track;
        InflateRect(&fr, settingsPt(2), settingsPt(2));
        settingsStrokeRoundRect(dc, fr, settingsPt(13), c.accent,
                                (std::max)(1, MulDiv(3, g_settingsDpi, 144)));
    }
}

// 自绘 choice 胶囊：选中=accentSoft+accent 描边+accentText；未选=controlBg+
// controlBorder+textPrimary，hover 未选=controlHover。只读缓存（重绘高频）。
static void settingsDrawChoice(const DRAWITEMSTRUCT* dis) {
    const SettingsColors c = settingsColors(g_settingsDark);
    HDC dc = dis->hDC;
    FillRect(dc, &dis->rcItem, settingsControlBg());
    const ModeScope current = g_settingsModeScope;
    bool selected = false;
    if (dis->CtlID == 121) selected = current == ModeScope::System;
    else if (dis->CtlID == 122) selected = current == ModeScope::Process;
    else if (dis->CtlID == 131) selected = g_settingsTheme == Theme::System;
    else if (dis->CtlID == 132) selected = g_settingsTheme == Theme::Light;
    else if (dis->CtlID == 133) selected = g_settingsTheme == Theme::Dark;
    const bool hovered = (g_settingsHoverId == static_cast<int>(dis->CtlID));
    RECT r = dis->rcItem;
    const COLORREF fill = selected ? c.accentSoft : (hovered ? c.controlHover : c.controlBg);
    settingsFillRoundRect(dc, r, settingsPt(9), fill);
    settingsStrokeRoundRect(dc, r, settingsPt(9), selected ? c.accent : c.controlBorder,
                            settingsStrokePx(1));
    wchar_t text[64] = {0};
    GetWindowTextW(dis->hwndItem, text, _countof(text));
    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, selected ? c.accentText : c.textPrimary);
    HGDIOBJ oldFont = SelectObject(dc, selected ? g_settingsFontBold : g_settingsFont);
    DrawTextW(dc, text, -1, &r, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, oldFont);
    if ((dis->itemState & ODS_FOCUS) != 0) settingsDrawFocusRing(dc, r, settingsPt(9));
}

// 自绘按钮：110 是主按钮（accentStrong 底 + onAccent 字），106/107 是次按钮
// （controlBg + controlBorder + controlText，清空用 danger 字）；hover/按下
// 主按钮 accentPressed、次按钮 controlHover。
static void settingsDrawButton(const DRAWITEMSTRUCT* dis) {
    const SettingsColors c = settingsColors(g_settingsDark);
    HDC dc = dis->hDC;
    FillRect(dc, &dis->rcItem, settingsControlBg());
    const bool primary = (dis->CtlID == 110);
    const bool pressed = (dis->itemState & ODS_SELECTED) != 0;
    const bool hovered = (g_settingsHoverId == static_cast<int>(dis->CtlID));
    RECT r = dis->rcItem;
    const COLORREF fill = primary ? ((pressed || hovered) ? c.accentPressed : c.accentStrong)
                                  : ((pressed || hovered) ? c.controlHover : c.controlBg);
    settingsFillRoundRect(dc, r, settingsPt(9), fill);
    if (!primary) {
        settingsStrokeRoundRect(dc, r, settingsPt(9), c.controlBorder, settingsStrokePx(1));
    }
    wchar_t text[64] = {0};
    GetWindowTextW(dis->hwndItem, text, _countof(text));
    SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, primary ? c.onAccent : (dis->CtlID == 106 ? c.danger : c.controlText));
    HGDIOBJ oldFont = SelectObject(dc, g_settingsFont);
    DrawTextW(dc, text, -1, &r, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
    SelectObject(dc, oldFont);
    if ((dis->itemState & ODS_FOCUS) != 0) settingsDrawFocusRing(dc, r, settingsPt(9));
}

// ---- 自绘控件 hover / 手型光标 ----
// BS_OWNERDRAW 不转发鼠标消息；子类化每个控件维护 hover id，WM_SETCURSOR 返回手型。
struct SettingsCtlProcEntry {
    HWND hwnd;
    WNDPROC old;
};
static SettingsCtlProcEntry g_ctlProcs[8] = {};
static int g_ctlProcCount = 0;

static LRESULT CALLBACK settingsCtlProc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    WNDPROC old = DefWindowProcW;
    for (int i = 0; i < g_ctlProcCount; ++i) {
        if (g_ctlProcs[i].hwnd == hwnd) {
            old = g_ctlProcs[i].old;
            break;
        }
    }
    const int id = GetDlgCtrlID(hwnd);
    if (msg == WM_MOUSEMOVE) {
        if (g_settingsHoverId != id) {
            g_settingsHoverId = id;
            InvalidateRect(hwnd, nullptr, FALSE);
        }
        TRACKMOUSEEVENT tme{};
        tme.cbSize = sizeof(tme);
        tme.dwFlags = TME_LEAVE;
        tme.hwndTrack = hwnd;
        TrackMouseEvent(&tme);
    } else if (msg == WM_MOUSELEAVE) {
        if (g_settingsHoverId == id) {
            g_settingsHoverId = 0;
            InvalidateRect(hwnd, nullptr, FALSE);
        }
    } else if (msg == WM_SETCURSOR && LOWORD(lp) == HTCLIENT) {
        // IDC_HAND 的宏是 ANSI 变体，本壳按 W 版 API 编译，直接用资源号 32649。
        SetCursor(LoadCursorW(nullptr, MAKEINTRESOURCEW(32649)));
        return TRUE;
    }
    return CallWindowProcW(old, hwnd, msg, wp, lp);
}

static void settingsSubclassControl(HWND ctl) {
    if (ctl == nullptr || g_ctlProcCount >= 8) return;
    const LONG_PTR oldRaw = SetWindowLongPtrW(ctl, GWLP_WNDPROC,
                                              reinterpret_cast<LONG_PTR>(settingsCtlProc));
    if (oldRaw == 0) return;
    g_ctlProcs[g_ctlProcCount].hwnd = ctl;
    g_ctlProcs[g_ctlProcCount].old = reinterpret_cast<WNDPROC>(oldRaw);
    ++g_ctlProcCount;
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
    if (MessageBoxW(wnd, L"将删除全部调频词、用户词组与上下文共现记录（仅本机），操作不可恢复。\n\n确定清空学习数据？",
                    L"清空学习数据", MB_YESNO | MB_ICONWARNING | MB_DEFBUTTON2) != IDYES) return;
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

// ---- 词库导入（GUI）----
// 设置窗选 dict.yaml/txt/TSV → chaoset-import 合并到用户词库（先备份、
// 原子替换）后重启 daemon；与 macOS 同语义，必须检查导入器退出码。

static std::wstring settingsQuote(const std::wstring& s) { return L"\"" + s + L"\""; }

// ---- 方案 A（冷路径）：设置项读写统一走 Go chaoset-settingsctl ----
// 设置窗是冷路径，允许起进程但每次调用必须有 ≤1s 硬超时（UI 线程不无限等待）；
// 热路径（TSF 回调）读 config.cpp 的 TTL 缓存。CLI 与 DLL 同目录，缺失/超时/
// JSON 非法时回退原生 HKCU 读写，保证旧包仍可用（回退只记一条限流 diag）。
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
    HANDLE outRead = nullptr, outWrite = nullptr;
    if (!CreatePipe(&outRead, &outWrite, &sa, 0)) return false;
    SetHandleInformation(outRead, HANDLE_FLAG_INHERIT, 0);
    HANDLE nulIn = CreateFileW(L"NUL", GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, 0, nullptr);
    HANDLE nulErr = CreateFileW(L"NUL", GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, 0, nullptr);
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

// 解析 get 单行 JSON（见 chaoset-settingsctl）：只认本窗用的 associate/
// mode_scope/theme；mode_scope 宽容归一化（旧 window→Process），未知值回退原生。
static bool settingsParseGetJSON(const std::string& json, bool* associate, ModeScope* scope,
                                 Theme* theme) {
    if (associate == nullptr || scope == nullptr || theme == nullptr) return false;
    const size_t a = json.find("\"associate\":");
    const size_t m = json.find("\"mode_scope\":\"");
    if (a == std::string::npos || m == std::string::npos) return false;
    size_t p = a + sizeof("\"associate\":") - 1;
    while (p < json.size() && (json[p] == ' ' || json[p] == '\t')) ++p;
    if (p + 4 <= json.size() && json.compare(p, 4, "true") == 0) *associate = true;
    else if (p + 5 <= json.size() && json.compare(p, 5, "false") == 0) *associate = false;
    else return false;
    const size_t v = m + sizeof("\"mode_scope\":\"") - 1;
    const size_t q = json.find('"', v);
    if (q == std::string::npos) return false;
    const std::string raw = json.substr(v, q - v);
    if (raw == "system") *scope = ModeScope::System;
    else if (raw == "process") *scope = ModeScope::Process;
    else if (raw == "window") *scope = ModeScope::Process;  // 旧档迁移
    else return false;
    *theme = Theme::System;  // theme 可选：旧 CLI 无此字段时默认 system
    const size_t t = json.find("\"theme\":\"");
    if (t != std::string::npos) {
        const size_t tv = t + sizeof("\"theme\":\"") - 1;
        const size_t tq = json.find('"', tv);
        if (tq == std::string::npos) return false;
        const std::string traw = json.substr(tv, tq - tv);
        if (traw == "light") *theme = Theme::Light;
        else if (traw == "dark") *theme = Theme::Dark;
        else if (traw != "system") return false;
    }
    return true;
}

static const wchar_t* settingsScopeName(ModeScope scope) {
    return scope == ModeScope::Process ? L"process" : L"system";
}
static const wchar_t* settingsThemeName(Theme theme) {
    if (theme == Theme::Light) return L"light";
    if (theme == Theme::Dark) return L"dark";
    return L"system";
}

// 冷路径读取「词组联想 + 中英状态作用域」：优先 Go 单一真源，失败回退原生。
static void settingsLoadShared() {
    std::string raw;
    if (settingsRunCtl(L"get", &raw, kSettingsCtlTimeoutMs)) {
        bool associate = true;
        ModeScope scope = ModeScope::System;
        Theme theme = Theme::System;
        if (settingsParseGetJSON(raw, &associate, &scope, &theme)) {
            g_settingsAssociate = associate;
            g_settingsModeScope = scope;
            g_settingsTheme = theme;
            return;
        }
        settingsDiagCtlFallback("get-json");
    } else {
        settingsDiagCtlFallback("get");
    }
    // 回退：旧安装包/便携包/CLI 被拦时保证设置窗仍能显示与修改。
    g_settingsAssociate = RegGetAssociate();
    g_settingsModeScope = uiLoadModeScope();
    g_settingsTheme = uiLoadTheme();
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
    HANDLE logFile = INVALID_HANDLE_VALUE, nul = INVALID_HANDLE_VALUE;
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
        si.hStdOutput = si.hStdError = logFile;
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
    ofn.Flags = OFN_ALLOWMULTISELECT | OFN_EXPLORER | OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR;
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
            RECT client{};
            GetClientRect(hwnd, &client);
            const int w = client.right - client.left;
            const int h = client.bottom - client.top;
            if (w > 0 && h > 0) {
                HDC memDC = CreateCompatibleDC(dc);
                HBITMAP memBmp = CreateCompatibleBitmap(dc, w, h);
                HGDIOBJ oldBmp = SelectObject(memDC, memBmp);
                settingsPaint(memDC, hwnd);
                BitBlt(dc, 0, 0, w, h, memDC, 0, 0, SRCCOPY);
                SelectObject(memDC, oldBmp);
                DeleteObject(memBmp);
                DeleteDC(memDC);
            }
            EndPaint(hwnd, &ps);
            return 0;
        }
        case WM_ERASEBKGND:
            return 1;  // 背景由 WM_PAINT 统一画，避免闪烁
        // 导航项 hover：导航是自绘的（无子窗口），只能在这里按坐标命中；控件
        // hover 由 settingsCtlProc 维护。TME_LEAVE 只在首次移动时注册一次。
        case WM_MOUSEMOVE: {
            const POINT pt{GET_X_LPARAM(lParam), GET_Y_LPARAM(lParam)};
            int hover = -1;
            for (int i = 0; i < 3; ++i) {
                RECT r = settingsNavItemRect(i);
                RECT pr{settingsPt(r.left), settingsPt(r.top), settingsPt(r.right),
                        settingsPt(r.bottom)};
                if (PtInRect(&pr, pt)) { hover = i; break; }
            }
            if (hover != g_settingsHoverNav) {
                g_settingsHoverNav = hover;
                InvalidateRect(hwnd, nullptr, FALSE);
            }
            if (!g_settingsTrackingMouse) {
                TRACKMOUSEEVENT tme{sizeof(tme), TME_LEAVE, hwnd, 0};
                TrackMouseEvent(&tme);
                g_settingsTrackingMouse = true;
            }
            return 0;
        }
        case WM_MOUSELEAVE:
            g_settingsTrackingMouse = false;
            if (g_settingsHoverNav != -1) {
                g_settingsHoverNav = -1;
                InvalidateRect(hwnd, nullptr, FALSE);
            }
            return 0;
        // 自绘导航项是点击热区：悬停时给手型光标（控件由 settingsCtlProc 处理）。
        case WM_SETCURSOR:
            if (LOWORD(lParam) == HTCLIENT) {
                POINT pt{};
                GetCursorPos(&pt);
                ScreenToClient(hwnd, &pt);
                for (int i = 0; i < 3; ++i) {
                    RECT r = settingsNavItemRect(i);
                    RECT pr{settingsPt(r.left), settingsPt(r.top), settingsPt(r.right),
                            settingsPt(r.bottom)};
                    if (PtInRect(&pr, pt)) {
                        SetCursor(LoadCursorW(nullptr, MAKEINTRESOURCEW(32649)));
                        return TRUE;
                    }
                }
            }
            break;
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
            if (HIWORD(wParam) == BN_CLICKED && LOWORD(wParam) == 106) { SettingsClearLearningData(hwnd); return 0; }
            if (HIWORD(wParam) == BN_CLICKED && LOWORD(wParam) == 107) { SettingsExportLearningData(hwnd); return 0; }
            if (HIWORD(wParam) == BN_CLICKED && LOWORD(wParam) == 110) { SettingsImportDictionary(hwnd); return 0; }
            if (HIWORD(wParam) == BN_CLICKED &&
                (LOWORD(wParam) == 121 || LOWORD(wParam) == 122)) {
                // 中英作用域两档：写 Go 单一真源 set mode-scope，失败回退 uiSaveModeScope。
                ModeScope scope = ModeScope::System;
                if (LOWORD(wParam) == 122) scope = ModeScope::Process;
                g_settingsModeScope = scope;
                const std::wstring scopeArg = L"set mode-scope " + std::wstring(settingsScopeName(scope));
                if (!settingsWriteShared(scopeArg, "set-mode-scope")) uiSaveModeScope(scope);
                for (int id : {121, 122}) {
                    HWND b = GetDlgItem(hwnd, id);
                    if (b != nullptr) InvalidateRect(b, nullptr, TRUE);
                }
                return 0;
            }
            if (HIWORD(wParam) == BN_CLICKED &&
                (LOWORD(wParam) == 131 || LOWORD(wParam) == 132 || LOWORD(wParam) == 133)) {
                Theme theme = Theme::System;
                if (LOWORD(wParam) == 132) theme = Theme::Light;
                else if (LOWORD(wParam) == 133) theme = Theme::Dark;
                g_settingsTheme = theme;
                const std::wstring arg = L"set theme " + std::wstring(settingsThemeName(theme));
                if (!settingsWriteShared(arg, "set-theme")) uiSaveTheme(theme);
                invalidateThemeCache();
                settingsUpdateTheme(hwnd);
                PostMessageW(HWND_BROADCAST, WM_SETTINGCHANGE, 0, reinterpret_cast<LPARAM>(L"ImmersiveColorSet"));
                for (int id : {131, 132, 133}) {
                    HWND b = GetDlgItem(hwnd, id);
                    if (b != nullptr) InvalidateRect(b, nullptr, TRUE);
                }
                return 0;
            }
            if (LOWORD(wParam) == 101 && HIWORD(wParam) == BN_CLICKED) {
                // 自绘复选框：手动翻转状态。关闭联想即广播通知各宿主候选窗收起。
                g_settingsAssociate = !g_settingsAssociate;
                const std::wstring assocArg = L"set associate " + std::wstring(g_settingsAssociate ? L"on" : L"off");
                if (!settingsWriteShared(assocArg, "set-associate")) RegSetAssociate(g_settingsAssociate);
                if (!g_settingsAssociate) PostMessageW(HWND_BROADCAST, associateChangedMessage(), 0, 0);
                HWND chk = GetDlgItem(hwnd, 101);
                if (chk != nullptr) InvalidateRect(chk, nullptr, TRUE);
            }
            return 0;
        case WM_DRAWITEM: {
            const DRAWITEMSTRUCT* dis = reinterpret_cast<const DRAWITEMSTRUCT*>(lParam);
            if (dis != nullptr) {
                if (dis->CtlID == 101) { settingsDrawSwitch(dis); return TRUE; }
                if (dis->CtlID == 106 || dis->CtlID == 107 || dis->CtlID == 110) { settingsDrawButton(dis); return TRUE; }
                if (dis->CtlID == 121 || dis->CtlID == 122 || dis->CtlID == 131 || dis->CtlID == 132 || dis->CtlID == 133) {
                    settingsDrawChoice(dis); return TRUE;
                }
            }
            break;
        }
        case WM_CTLCOLORSTATIC: {
            // 说明文字用次级文本色（macOS secondaryLabel 对应），正文用主文本色。
            // 背景返回**卡片刷**：这些 Static 都在卡片上，用窗口背景刷会画出
            // 一条深色横条（2026-11-06 用户截图）。
            if (g_settingsBgBrush == nullptr) {
                g_settingsBgBrush = CreateSolidBrush(settingsColors(g_settingsDark).windowBg);
            }
            if (g_settingsCardBrush == nullptr) {
                g_settingsCardBrush = CreateSolidBrush(settingsCardColor());
            }
            const int id = GetDlgCtrlID(reinterpret_cast<HWND>(lParam));
            const SettingsColors c = settingsColors(g_settingsDark);
            const bool secondary = (id == 102 || id == 108 || id == 112 || id == 124);
            HDC dc = reinterpret_cast<HDC>(wParam);
            SetTextColor(dc, secondary ? c.textSecondary : c.textPrimary);
            SetBkMode(dc, TRANSPARENT);
            return reinterpret_cast<LRESULT>(settingsControlBg());
        }
        case WM_SETTINGCHANGE:
        case WM_THEMECHANGED:
            // 系统深浅色/主题切换：重建背景刷 + DWM 标题栏 + 重绘自绘控件。
            settingsUpdateTheme(hwnd);
            return 0;
        case WM_DPICHANGED: {
            // 窗口跨到不同缩放的显示器：按新 DPI 重排控件与窗口尺寸（优先采用系统建议矩形）。
            settingsApplyDpi(hwnd, static_cast<int>(HIWORD(wParam)));
            const RECT* prc = reinterpret_cast<const RECT*>(lParam);
            if (prc != nullptr) {
                SetWindowPos(hwnd, nullptr, prc->left, prc->top,
                             prc->right - prc->left, prc->bottom - prc->top,
                             SWP_NOZORDER | SWP_NOACTIVATE);
            }
            return 0;
        }
        case WM_CLOSE:
            DestroyWindow(hwnd);
            return 0;
        case WM_DESTROY:
            settingsDeleteFonts();
            if (g_settingsIcon) { DestroyIcon(g_settingsIcon); g_settingsIcon = nullptr; }
            if (g_settingsBgBrush) { DeleteObject(g_settingsBgBrush); g_settingsBgBrush = nullptr; }
            if (g_settingsCardBrush) { DeleteObject(g_settingsCardBrush); g_settingsCardBrush = nullptr; }
            g_settingsWnd = nullptr;
            InterlockedDecrement(&g_liveSettingsWindows);
            return 0;
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

// 真正创建入口：只在单例 UI 宿主（chaoset-ui.exe）里调用（AC 宿主里齿轮
// 也能弹出，宿主退出不带走窗口）。page 缺省输入页；已存在则切页前置。
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

    // per-monitor DPI：按鼠标所在显示器取 DPI（副屏也能开对）。
    POINT anchor{};
    GetCursorPos(&anchor);
    g_settingsDpi = dpiForPoint(anchor);
    g_settingsDark = uiEffectiveDark();
    // 方案 A 冷路径：打开设置窗时读 Go 单一真源，失败回退原生 HKCU。此时窗口
    // 尚未创建，最坏情况是开窗慢 1s，绝不会卡住任何宿主进程（设置窗属于
    // chaoset-ui.exe，与 TIP 进程无关）。
    settingsLoadShared();
    settingsCreateFonts();
    settingsLoadIcon();

    // 居中于光标所在显示器的工作区（macOS center 同语义）
    const DWORD style = WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU;
    const DWORD exStyle = 0; // 现代平整窗体（去除旧式双重凹陷边框 WS_EX_DLGMODALFRAME）
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
    settingsApplyTitleBarTheme(wnd);
    // 注意：这里**不再写 GWLP_USERDATA**（此前存的是裸 this）。设置窗的
    // 窗口过程只做 RegSetAssociate，不需要访问 TIP；不存指针就没有「TIP
    // 已释放、窗口还活着」的悬垂解引用（审计 P1-2）。
    g_settingsWnd = wnd;
    InterlockedIncrement(&g_liveSettingsWindows);

    // 分组标题/页脚/关于正文由 settingsPaint 自绘；交互控件用 BS_OWNERDRAW
    // 子窗口保留 Tab 可达性，背景由 settingsControlBg 擦除。
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
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"跟随系统", 131);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"浅色", 132);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"深色", 133);
    makeCtl(L"Static", SS_LEFT, L"选择界面配色；跟随系统则随 Windows 浅/深色自动切换。", 134);
    makeCtl(L"Static", SS_LEFT, L"读取中…", 105);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"清空学习数据…", 106);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"导出学习数据…", 107);
    makeCtl(L"Static", SS_LEFT, L"学习数据仅保存在本机，用于调频与词组联想；清空后不可恢复。", 108);
    makeCtl(L"Button", BS_OWNERDRAW | WS_TABSTOP, L"导入词库…", 110);
    makeCtl(L"Static", SS_LEFT, L"支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。", 112);

    // 自绘控件的 hover/手型光标由窗口过程子类化维护（BS_OWNERDRAW 不转发鼠标消息）。
    for (int id : {101, 106, 107, 110, 121, 122, 131, 132, 133}) settingsSubclassControl(GetDlgItem(wnd, id));
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
