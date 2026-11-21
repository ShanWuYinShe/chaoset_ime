// 统一设置中心启动器（叶子模块）。
//
// Windows 壳不再有原生设置窗：唯一设置 UI 是跨平台 Slint 设置中心
// （shims/settings，产物 chaoset-settings.exe，与 macOS/Linux 共用同一份 UI）。
// 本模块只负责在 UI 宿主进程里把该程序拉起来——不创建任何窗口、不重建设置 UI。
//
// 调用约束：只能在单例 UI 宿主进程（chaoset-ui.exe）的消息循环线程调用。
// TSF/TIP 进程内一律禁止 CreateProcessW（零阻塞契约）：TIP 只经命名管道发
// "settings" 请求，由 UI 宿主转发到这里（候选栏齿轮 → ui_client → UI 宿主）。
// 目录约定：chaoset-settings.exe 与 chaoset-ui.exe / ChaosetIME.dll 同目录
//（安装包与便携包都如此），打包脚本强制包含该文件，缺失即打包失败。

#ifndef CHAOSET_WIN_SETTINGS_LAUNCHER_HPP_
#define CHAOSET_WIN_SETTINGS_LAUNCHER_HPP_

#include <windows.h>

namespace chaoset::win {

// 启动统一设置中心（默认「输入」页）。
// 返回 true = CreateProcessW 已受理（设置中心自身的单实例逻辑负责去重前置）；
// false = 程序缺失/启动失败（实现里写一行 tsf.log 诊断，调用方给用户可见提示）。
bool LaunchSettingsCenter();

// 指定初始页启动：page 取 "input" / "dict" / "about"（与 shims/settings
// 的 --page 参数一致，未知值按默认页处理）。供托盘「关于」直达关于页。
// 单窗口语义由设置中心自己的单实例逻辑保证（重复启动的前置后退出）；Windows
// 上重复启动不切换已有窗口的页签，页签由用户自己切。
bool LaunchSettingsCenterAtPage(const wchar_t* page);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_SETTINGS_LAUNCHER_HPP_
