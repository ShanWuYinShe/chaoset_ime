// 设置窗（三端一致：macOS SettingsWindow.swift 同构；由 chaoset-ui.exe 创建）。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 win32.cpp 的
// TIP 侧（win32.cpp）的类型；设置窗可活过 TIP（不持有 TIP 指针）。实现见
// settings_window.cpp。

#ifndef CHAOSET_WIN_SETTINGS_WINDOW_HPP_
#define CHAOSET_WIN_SETTINGS_WINDOW_HPP_

#include <windows.h>

namespace chaoset::win {

// 设置窗三页（左侧导航顺序即枚举顺序）。
enum class SettingsPage { Input = 0, Dictionary = 1, About = 2 };

// 设置窗的唯一创建入口：只在单例 UI 宿主进程（chaoset-ui.exe）里调用。
// 窗口已存在时只切页并前置，不另开新窗。
void openSettingsWindowStandalone(SettingsPage page = SettingsPage::Input);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_SETTINGS_WINDOW_HPP_
