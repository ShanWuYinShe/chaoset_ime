// 单例 UI 宿主进程（chaoset-ui.exe）：UiTray、托盘菜单、runUiHost、命令管道线程。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 win32.cpp 的
// TIP 侧（win32.cpp）的类型。UI 宿主进程里的 ShellWorker（异步日志）属于
// win32.cpp 的 worker 调度，导出入口 ChaosetRunUi 在调用 runUiHost 前注入
// 启动/停止原语（依赖倒置，避免 leaf 反向引用 TIP TU）。
//
// 进程模型：chaoset-ui.exe 只有几十行（LoadLibrary 本 DLL + 调用导出
// ChaosetRunUi），托盘绘制/设置窗/命名管道全部复用本模块，不存在第二套
// UI 实现。单实例：Local\ChaosetUi-<SID>-<session> 互斥体。

#ifndef CHAOSET_WIN_UI_HOST_HPP_
#define CHAOSET_WIN_UI_HOST_HPP_

#include <windows.h>

namespace chaoset::win {

// UI 宿主进程里的 ShellWorker 生命周期原语（实现见 win32.cpp 的 worker 段）。
using UiHostWorkerFn = void (*)();
using UiHostWorkerWaitFn = void (*)(DWORD ms);

// 由导出入口 ChaosetRunUi 在调用 runUiHost 前注入；未注入时按「无 worker」
// 处理（日志直接丢弃，绝不回退同步写盘）。
void uiHostSetWorkerHooks(UiHostWorkerFn start, UiHostWorkerFn stop,
                          UiHostWorkerWaitFn waitStopped);

// UI 宿主主函数（由导出 ChaosetRunUi 调用，运行在 chaoset-ui.exe 进程里）。
int runUiHost(LPCWSTR cmdLine);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_UI_HOST_HPP_
