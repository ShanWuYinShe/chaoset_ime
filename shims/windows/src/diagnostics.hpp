// 诊断日志（tsf.log / crash.log）与 VEH 崩溃取证。
//
// 叶子模块（docs/architecture.md §3.1）：不得 include/引用 win32.cpp 的
// TIP 侧（win32.cpp）的类型；由 win32.cpp（TIP TU）向下编排。实现见 diagnostics.cpp。

#ifndef CHAOSET_WIN_DIAGNOSTICS_HPP_
#define CHAOSET_WIN_DIAGNOSTICS_HPP_

#include <windows.h>

namespace chaoset::win {

// 最后一次 TSF 操作（崩溃日志用；TIP 各入口只写这个原子值）。
enum DiagOp : LONG {
    kOpNone = 0,
    kOpActivate,
    kOpDeactivate,
    kOpTestKeyDown,
    kOpKeyDown,
    kOpRender,
    kOpPreedit,
    kOpCommit,
    kOpEndComposition,
    kOpCompositionTerminated,
    kOpCandidateClick,
};

extern volatile LONG g_lastOp;

const char* diagOpName(LONG op);
void diagHex64(char* out, unsigned long long v);
void diagStamp(char* out, int cap);
void diagWrite(const wchar_t* name, const char* text, int len);
void diagPrintf(const wchar_t* name, const char* fmt, ...);
bool diagBudget();
void diagTsfFail(const char* op, HRESULT hr);
void diagHostName(char* out, int cap);
bool diagModuleOf(const void* addr, char* module, int cap, char* offsetHex);
LONG CALLBACK diagCrashHandler(EXCEPTION_POINTERS* info);
void ensureCrashHandler();
// 动态卸载前撤销 VEH（否则宿主后续异常会跳进已卸载 DLL，见 diagnostics.cpp）。
void removeCrashHandler();

// tsf.log 的异步落盘入口。诊断是叶子模块，不能反向链接 TIP TU 的 ShellWorker：
// worker 段在 DllMain 里把 enqueueLogLine 注册进来。未注册时 tsf.log 直接丢弃，
// 绝不回退同步写盘（零阻塞契约）。
using AsyncLogFn = void (*)(const char* text, int len);
void diagSetAsyncLog(AsyncLogFn fn);

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_DIAGNOSTICS_HPP_
