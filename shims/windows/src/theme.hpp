// 候选窗配色/主题判定（与 macOS CandidatePanel.swift 视觉契约）。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 TIP TU（tip.hpp /
// tip_*.cpp）的类型。CandidateColors 的字段/色值由
// tools/checkuiparity「候选窗几何 / 候选窗配色」静态断言锁定。实现见 theme.cpp。

#ifndef CHAOSET_WIN_THEME_HPP_
#define CHAOSET_WIN_THEME_HPP_

#include <windows.h>

namespace chaoset::win {

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

// 描边线宽 pt 化：1pt 在 96dpi 下 MulDiv 四舍五入仍是 1px（下限钳 1）。
int strokePx(int dpi);

// 系统浅色/深色判定（HKCU Themes\\Personalize\\AppsUseLightTheme；未配置按浅色）。
bool isSystemDarkTheme();

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_THEME_HPP_
