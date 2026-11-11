// ChaosetIME TSF 壳（Windows 专用胶水层）——DLL 生命周期编排 TU。
//
// 职责：类厂、导出（ChaosetRunUi）、DllMain/DllGetClassObject/DllCanUnloadNow 与
// 跨 TU 存活计数（g_liveObjects）。TIP 主体见 tip.hpp + tip_core/keys/composition/
// candidates.cpp；候选窗见 candidate_window.cpp、后台 worker 见 shell_worker.cpp、
// TIP 线程消息窗口/注册表见 tip_thread.cpp、COM 注册见 tip_registration.cpp、
// CLSID/GUID 常量见 tip_ids.hpp。
//
// 依赖方向（docs/architecture.md §3.1）：本 TU 是唯一编排点，可向下 include 全部
// Windows 壳模块；叶子模块不得反向 include 本 TU/TIP 类型。
//
// 验证等级：mingw-w64 编译级 + 核心层行为级（macOS 上对真实 daemon，
// 见 tests/core_test.cpp）。COM 注册表细节与 TSF 运行时行为需在 Windows 真机上
// 完成联调（已知限制，README 有说明）。
//
// 构建见 build.sh（x86_64-w64-mingw32-g++）。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <objbase.h>

#include "candidate_window.hpp"
#include "diagnostics.hpp"
#include "mode_toast.hpp"
#include "shell_worker.hpp"
#include "tip.hpp"
#include "tip_ids.hpp"
#include "tip_thread.hpp"
#include "ui_host.hpp"
#include "winutil.hpp"

using namespace chaoset::win;

namespace chaoset::win {
// 存活 TIP 实例数：TIP ctor/dtor（tip_core.cpp）与 DllCanUnloadNow 共用。
volatile LONG g_liveObjects = 0;
}  // namespace chaoset::win

namespace {

// ---- 类厂 ----

class ChaosetFactory : public IClassFactory {
public:
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown || riid == IID_IClassFactory) {
            *ppv = static_cast<IClassFactory*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return 2; }
    ULONG STDMETHODCALLTYPE Release() override { return 1; }
    HRESULT STDMETHODCALLTYPE CreateInstance(IUnknown* outer, REFIID riid, void** ppv) override {
        if (outer != nullptr) return CLASS_E_NOAGGREGATION;
        ChaosetTIP* tip = new ChaosetTIP();
        HRESULT hr = tip->QueryInterface(riid, ppv);
        tip->Release();
        return hr;
    }
    HRESULT STDMETHODCALLTYPE LockServer(BOOL /*fLock*/) override { return S_OK; }
};

}  // namespace

// ---- 导出 ----

extern "C" __declspec(dllexport) int WINAPI ChaosetRunUi(HINSTANCE /*exeInst*/,
                                                         LPCWSTR cmdLine) {
    // UI 宿主进程里的异步日志/后台任务 worker 由 shell_worker.cpp 提供，
    // 叶子模块 ui_host 不反向依赖；这里把生命周期原语注入给 runUiHost，
    // 启动/停止顺序与拆分前一致（先起 worker 再建窗口，退出前先停 worker）。
    uiHostSetWorkerHooks(&shellWorkerStart, &shellWorkerStop, &shellWorkerWaitStopped);
    return runUiHost(cmdLine);
}

BOOL WINAPI DllMain(HINSTANCE hinstDLL, DWORD fdwReason, LPVOID reserved) {
    if (fdwReason == DLL_PROCESS_ATTACH) {
        g_hInst = hinstDLL;
        // tsf.log 的异步落盘入口：诊断是叶子模块，worker 段在 DllMain 注册
        // enqueueLogLine；未注册/worker 未启动时直接丢弃，绝不回退同步写。
        diagSetAsyncLog(&enqueueLogLine);
    } else if (fdwReason == DLL_PROCESS_DETACH && reserved == nullptr && hinstDLL != nullptr) {
        // 动态卸载（FreeLibrary）：先撤 VEH 回调（否则宿主下一次任意异常都会
        // 跳进已卸载内存 → WER「ChaosetIME.dll_unloaded」崩溃），再停后台 worker
        //（它跑本 DLL 的代码），并注销本模块注册的窗口类。Windows 不会随 DLL
        // 卸载自动注销窗口类，不注销的话下次加载时同名类的 lpfnWndProc 可能仍
        // 指向已卸载内存。进程终止（reserved != nullptr）时无需处理。
        removeCrashHandler();
        shellWorkerStop();
        UnregisterClassW(windowClassName(L"ChaosetCandidateWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetModeToastWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetSettingsWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetTrayWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetUiHostWndClass", hinstDLL).c_str(), hinstDLL);
        UnregisterClassW(windowClassName(L"ChaosetTipMsgWndClass", hinstDLL).c_str(), hinstDLL);
    }
    return TRUE;
}

STDAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void** ppv) {
    if (!IsEqualCLSID(rclsid, CLSID_ChaosetTIP)) return CLASS_E_CLASSNOTAVAILABLE;
    // 2026-11-06 零阻塞：此处不再同步写 tsf.log（classobject 属于加载期回调；
    // Activate 行已记录 host，足以区分「DLL 加载了但 Activate 没跑」）。
    static ChaosetFactory factory;  // 无状态，静态实例即可
    return factory.QueryInterface(riid, ppv);
}

STDAPI DllCanUnloadNow() {
    // 必须同时考虑 TIP 实例、各类窗口与后台 worker：
    //   * 窗口过程都指向本 DLL 代码，残留窗口时 FreeLibrary 会让下一次
    //     WM_PAINT 跳到已卸载内存；
    //   * ShellWorker 线程同样执行本 DLL 代码，线程存活期间绝不能卸载。
    // 没有 TIP/窗口后才停 worker；线程尚未退干净前继续返回 S_FALSE。
    if (g_liveObjects != 0) return S_FALSE;
    if (g_liveCandidateWindows != 0) return S_FALSE;
    if (g_liveSettingsWindows != 0) return S_FALSE;
    if (g_liveToastWindows != 0) return S_FALSE;
    if (g_liveTrayWindows != 0) return S_FALSE;
    if (g_liveTipMsgWindows != 0) return S_FALSE;
    shellWorkerStop();
    if (shellWorkerAlive()) return S_FALSE;
    return S_OK;
}
