// mode_hotkey.hpp：无文本输入焦点时的中英切换热键（issue #132）。
//
// 为什么需要：TSF 只在**有文本输入焦点**时把按键投递给输入法——桌面、资源管理器
// 空白处、任务栏等没有输入框的场景 Shift 根本到不了 TIP。项目零副作用契约又禁止
// SetWindowsHookEx 之类的全局键鼠钩子，所以唯一可行的补充入口是**系统热键**
// `RegisterHotKey`：由系统在按键时直接投递 WM_HOTKEY，不钩任何进程、不占按键流。
//
// 叶子模块：只依赖 windows.h + diagnostics，不引用 TIP/候选窗/托盘类型。
#pragma once

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

namespace chaoset::win {

/// 注册热键（UI 宿主隐藏消息窗口就绪后调用一次）。失败只记日志并降级：
/// 热键是可选的补充入口，托盘左键始终可用，绝不影响输入。
void uiRegisterToggleModeHotkey(HWND hwnd);

/// 注销热键（窗口销毁前调用）。热键是全局独占资源，不注销会在进程存活期内一直
/// 占着该组合键，其它程序永远收不到。
void uiUnregisterToggleModeHotkey(HWND hwnd);

/// WM_HOTKEY 的 wParam 是否为本热键（窗口过程据此转投中英切换消息）。
bool uiIsToggleModeHotkey(WPARAM wParam);

}  // namespace chaoset::win