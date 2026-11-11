// daemon 懒启动/连接/熔断原语（只有 worker 线程/设置窗调用）。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 TIP TU（tip.hpp /
// tip_*.cpp）的类型。TSF 线程的零等待版本 ensureDaemonFast 在
// shell_worker.cpp（只入队后台任务，不阻塞）。实现见 daemon.cpp。

#ifndef CHAOSET_WIN_DAEMON_HPP_
#define CHAOSET_WIN_DAEMON_HPP_

#include <windows.h>

#include "transport.hpp"

namespace chaoset::win {

// 返回 true 表示本次确实发起了 daemon 进程（CreateProcess 成功）。
bool ensureDaemonStarted();

// worker 线程专用：建立/等待 daemon 连接（有界，总共约 <=1s）。
bool ensureDaemonConnected(PipeTransport& transport);

// daemon 请求失败（连接还在但 session 方法返回 !ok）限流日志。
void diagDaemonRequestFailure(PipeTransport& transport, const char* op);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_DAEMON_HPP_
