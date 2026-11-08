// Windows 公共小工具：模块句柄、窗口类名、DPI、AppContainer 判定、
// 当前用户 SID/会话、宽字符转换。
//
// 叶子模块（docs/architecture.md §3.1）：无内部依赖；不得 include/引用
// win32.cpp（TIP TU）的类型。实现见 winutil.cpp。

#ifndef CHAOSET_WIN_WINUTIL_HPP_
#define CHAOSET_WIN_WINUTIL_HPP_

#include <windows.h>

#include <string>

namespace chaoset::win {

// 本 DLL 的模块句柄（DllMain 赋值）：同目录 exe（chaosetd/chaoset-ui）定位
// 与窗口类名都靠它。
extern HINSTANCE g_hInst;

// 设置窗 / 托盘的生命周期计数（DllCanUnloadNow 防卸载）。定义分别在
// settings_window.cpp / ui_host.cpp；TIP 自己的三个计数是 TIP TU 私有的
// （win32.cpp 匿名命名空间），不跨 TU。
extern volatile LONG g_liveSettingsWindows;
extern volatile LONG g_liveTrayWindows;

// UTF-8 → UTF-16。
std::wstring toWide(const std::string& utf8);

// 当前进程是否运行在 AppContainer（UWP / 开始菜单 / 搜索）沙盒里。
bool isAppContainer();

// 窗口类名带上模块基址：DLL 重载后基址变化，避免沿用旧类里指向已卸载
// 内存的 lpfnWndProc（第二道防线，见 win32.cpp DllMain 注释）。
std::wstring windowClassName(const wchar_t* base, HINSTANCE inst);

// 浮窗（候选窗/模式提示条）的 owner：必须是当前前台窗口且属于本进程。
// 只有沉浸式 shell 宿主（开始菜单/搜索等）需要 owner（否则被 z 序 band 压住）；
// 普通桌面宿主不需要 owner（带 owner 会把 owner 窗口组抬到前面，见实现注释）。
bool shellHostNeedsOwner();
HWND popupOwner();

// 主屏 DPI / 指定屏幕坐标所在显示器的 DPI（Win8.1+ shcore，缺失时退回主屏）。
int systemDpi();
int dpiForPoint(POINT pt);

// 当前用户 SID 字符串（多用户/非 ASCII 安全的管道名）、当前会话 ID
// （RDP/快速用户切换时同一 SID 的两个会话必须区分）。
std::wstring currentUserSid();
DWORD currentSessionId();

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_WINUTIL_HPP_
