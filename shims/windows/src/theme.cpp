// 候选窗配色/主题判定（模块实现；CandidateColors 定义在 theme.hpp）。
// 叶子模块：不得 include/引用 TIP TU（tip.hpp / tip_*.cpp）的类型。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <algorithm>

#include "theme.hpp"

namespace chaoset::win {

// 描边线宽 pt 化：1pt 在 96dpi 下 MulDiv 四舍五入仍是 1px，125%/150%/200%
// 下为 2/2/3px——与 macOS 的 1pt 描边同口径（2026-10-30 三端一致性审计
// A2：面板/键帽/分隔线/Toast 此前固定 1 设备像素，缩放下系统性偏细
// 25%-50%）。下限钳 1：任何 DPI 下描边都不能消失。
//
// 实现注意：这些 Pen 都是 CreatePen 的 PS_SOLID（非几何笔），没有几何
// 笔的圆头线帽与抗锯齿，高 DPI 下边缘是硬像素；而 mac 是 AppKit 矢量
// 描边（亚像素抗锯齿）。线宽/几何语义正确即可，观感差异属已知平台差异
// （不引入 GDI+ 只为四道描边）。
int strokePx(int dpi) { return (std::max)(1, MulDiv(1, dpi, 72)); }

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

}  // namespace chaoset::win
