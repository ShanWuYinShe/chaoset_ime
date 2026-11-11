// ChaosetIME TSF 壳（Windows 专用胶水层）。
//
// 职责：COM TIP 注册与激活、按键拦截（ITfKeyEventSink + ITfKeystrokeMgr）、
// 预编辑合成（ITfContextComposition）、候选上屏；引擎逻辑全部经
// core::Session 与 chaosetd 通信，传输只有 Windows 命名管道一种
// （\\.\pipe\chaoset-<SID>：无端口、ACL 限当前用户，无任何回退通道）。
// daemon 不可达时所有按键原样透传（降级策略，见 README）。
//
// 验证等级：mingw-w64 编译级 + 核心层行为级（macOS 上对真实 daemon，
// 见 tests/core_test.cpp）。COM 注册表细节与 TSF 运行时行为需在
// Windows 真机上完成联调（已知限制，README 有说明）。
//
// 本 TU 只保留 TIP 主体（class ChaosetTIP）与 DLL 生命周期编排（类厂/DllMain/
// 导出/TIP 线程 message-only 窗口过程）；候选窗见 candidate_window.cpp、后台
// worker 见 shell_worker.cpp、COM 注册见 tip_registration.cpp、CLSID/GUID 常量
// 见 tip_ids.hpp、跨 TU 消息/注册表声明见 tip_thread.hpp。
//
// 构建见 build.sh（x86_64-w64-mingw32-g++）。

#define WIN32_LEAN_AND_MEAN
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <wingdi.h>
#include <appmodel.h>
#include <msctf.h>
#include <objbase.h>

#include <algorithm>
#include <cmath>
#include <cstdarg>
#include <cstdio>
#include <cstring>
#include <atomic>
#include <cwchar>
#include <deque>
#include <map>
#include <mutex>
#include <string>
#include <vector>

#include "candidate_layout.hpp"
#include "core.hpp"

// 叶子模块（docs/architecture.md §3.1）：win32.cpp 是 TIP TU，可向下包含所有
// 叶子模块头；叶子模块不得反向 include/引用本文件的 TIP/CandidateWindow 类型。
#include "winutil.hpp"
#include "diagnostics.hpp"
#include "theme.hpp"
#include "config.hpp"
#include "transport.hpp"
#include "daemon.hpp"
#include "ui_client.hpp"
#include "settings_window.hpp"
#include "ui_host.hpp"
#include "mode_toast.hpp"

// 本 TU 拆出的模块（wave1 续）：候选窗 / 后台 worker / TIP 线程回投声明 /
// CLSID/GUID 常量。模块边界见 docs/architecture.md §3.1：这些 TU 不得反向
// include/引用本文件的 TIP 类型。
#include "candidate_window.hpp"
#include "shell_worker.hpp"
#include "tip_ids.hpp"
#include "tip_thread.hpp"

#ifndef CHAOSET_VERSION
#define CHAOSET_VERSION "dev"
#endif




// 叶子模块里的公共符号统一在 namespace chaoset::win；TIP TU 仍保持匿名
// 命名空间（内部符号不外泄），这里用 using 指令做无歧义的降级引用。
using namespace chaoset::win;
namespace {
class ChaosetTIP;

// ---- 跨 TU 声明（wave1 续）----
// TIP 线程 message-only 窗口：消息常量与 worker 侧注册表在 tip_thread.hpp /
// tip_thread.cpp；本 TU 保留窗口过程与按线程的 TIP 注册表（分发 TIP 方法需要
// 完整类型，wave2 随 tip.hpp 一起迁移）。
static HWND ensureTipMsgWnd();
static void registerTipOnThread(ChaosetTIP* tip);
static void unregisterTipFromThread(ChaosetTIP* tip);

// 存活中的 TIP message-only 窗口数量：窗口过程指向本 DLL 代码，存活期间禁止
// 宿主 FreeLibrary（DllCanUnloadNow 复核）。
static volatile LONG g_liveTipMsgWindows = 0;

constexpr DWORD kVkShift = 0x10;
constexpr DWORD kVkBackspace = 0x08;
constexpr DWORD kVkDelete = 0x2E;  // VK_DELETE：组词中与 Backspace 同语义（对齐 mac）
constexpr DWORD kVkEscape = 0x1B;
constexpr DWORD kVkReturn = 0x0D;
constexpr DWORD kVkSpace = 0x20;
constexpr DWORD kVkPrior = 0x21;           // Page Up
constexpr DWORD kVkNext = 0x22;            // Page Down
constexpr DWORD kVkFirstLetter = 0x41;  // 'A'
constexpr DWORD kVkLastLetter = 0x5A;   // 'Z'
constexpr DWORD kVkFirstDigit = 0x31;   // '1'
constexpr DWORD kVkLastDigit = 0x39;    // '9'
// 小键盘数字（VK_NUMPAD0..9）。Windows 的大小键盘虚键完全独立，此前只认
// VK_1-9：按小键盘数字落到「不可处理键」清空组词后放行，三端不一致
//（macOS 按字符、Linux 认 KP_1-9，2026-10-30 审计 A7）。
constexpr DWORD kVkNumpad1 = 0x61;      // VK_NUMPAD1
constexpr DWORD kVkNumpad9 = 0x69;      // VK_NUMPAD9
// 每页候选数：与 engine.PageSize() 一致（数字键 1-6 指位）。此前联想分页
// 处散落 6 个字面量，引擎改页容量时会静默漂移（2026-10-30 审计 R20）。
constexpr int kPageSize = 6;
// OEM 标点虚键（US 键盘布局假设；真机联调时按实际布局核验）
constexpr DWORD kVkOemSemicolon = 0xBA;      // ';'
constexpr DWORD kVkOemComma = 0xBC;          // ','
constexpr DWORD kVkOemPeriod = 0xBE;         // '.'
constexpr DWORD kVkOemOpenBrackets = 0xDB;   // '['
constexpr DWORD kVkOemBackslash = 0xDC;      // '\\'
constexpr DWORD kVkOemCloseBrackets = 0xDD;  // ']'
constexpr DWORD kVkOemQuotes = 0xDE;         // '\''
constexpr DWORD kVkOemPlus = 0xBB;           // '='（US 布局；shift 为 '+'）
constexpr DWORD kVkOemMinus = 0xBD;          // '-'
constexpr DWORD kVkOem2 = 0xBF;              // '/'（US 布局；shift 为 '?'）
constexpr DWORD kVkOem3 = 0xC0;              // '`'（US 布局；shift 为 '~'）

 volatile LONG g_liveObjects = 0;

// ---- TIP 主体 ----



// ---- 同步编辑会话：TSF 规定文本操作必须经 RequestEditSession ----

class EditSession : public ITfEditSession {
public:
    using Fn = void (*)(void* user, ITfContext* ctx, TfEditCookie ec);
    EditSession(ITfContext* ctx, void* user, Fn fn)
        : ctx_(ctx), user_(user), fn_(fn) {}

    HRESULT run(ITfThreadMgr* tm, TfClientId tid) {
        (void)tm;
        HRESULT hrSession = E_FAIL;
        HRESULT hr = ctx_->RequestEditSession(tid, this, TF_ES_SYNC | TF_ES_READWRITE, &hrSession);
        if (FAILED(hr)) return hr;
        return hrSession;
    }

    // IUnknown（栈上使用：外层在 RequestEditSession 返回后 Release）
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown || riid == IID_ITfEditSession) {
            *ppv = static_cast<ITfEditSession*>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    // 栈上生命周期：TSF 在同步调用内 AddRef/Release 配对，
    // 由外层作用域析构，Release 绝不 delete this。
    ULONG STDMETHODCALLTYPE AddRef() override { return InterlockedIncrement(&ref_); }
    ULONG STDMETHODCALLTYPE Release() override { return InterlockedDecrement(&ref_); }
    HRESULT STDMETHODCALLTYPE DoEditSession(TfEditCookie ec) override {
        fn_(user_, ctx_, ec);
        return S_OK;
    }

private:
    ITfContext* ctx_;
    void* user_;
    Fn fn_;
    LONG ref_ = 1;
};


// 前台钩子：每线程最多一个系统级钩子（多个 TIP 实例共用一个，回调只
// PostMessage 到该线程的 message-only 窗口；不 hide、不写盘 —— 审计 T6）。
static thread_local HWINEVENTHOOK g_tlFgHook = nullptr;
static thread_local std::vector<ChaosetTIP*> g_tlFgTips;  // 本线程全部 TIP（消息派发）
static thread_local std::vector<ChaosetTIP*> g_tlHookTips;  // 已挂前台钩子的 TIP
static thread_local HWND g_tlTipMsgWnd = nullptr;  // 本线程 message-only 窗口

static void addTipToThreadHookList(ChaosetTIP* tip) {
    for (ChaosetTIP* p : g_tlHookTips) {
        if (p == tip) return;
    }
    g_tlHookTips.push_back(tip);  // 2026-11-07 修正：此前误 push 到 g_tlFgTips
}

static void removeTipFromThreadHookList(ChaosetTIP* tip) {
    for (auto it = g_tlHookTips.begin(); it != g_tlHookTips.end(); ++it) {
        if (*it == tip) {
            g_tlHookTips.erase(it);  // 2026-11-07 修正：此前误删 g_tlFgTips
            return;
        }
    }
}


// mingw-w64 的 msctf.h 只声明了 ITfTextInputProcessor，没有微软 SDK 里的
// ITfTextInputProcessorEx（UILess 模式所需）。这里按 Windows SDK 10 的
// msctf.idl 原样补声明，IID 与虚表顺序都取自 SDK：
//   [
//     object, local,
//     uuid(6e4e2102-f9cd-433d-b496-303ce03a6507),
//   ]
//   interface ITfTextInputProcessorEx : ITfTextInputProcessor
//   {
//       HRESULT ActivateEx([in] ITfThreadMgr *ptim, [in] TfClientId tid, [in] DWORD dwFlags);
//   };
//
// 为什么需要（2026-11-05，微软《UILess Mode Overview》原文）：
//   "To be UI less aware, TIP needs to implement ITfTextInputProcessorEx.
//    If this interface is not implemented, TIP will not be activated under
//    the UI less mode thread."
// 不实现它就等于对 UILess 宿主（游戏/全屏/部分 WinUI 应用）完全不可见，
// 而且连日志都不会产生——我们此前看到的 "uiless=0" 正是「被排除」而非
// 「不存在 UILess 宿主」。
static const GUID kIID_ITfTextInputProcessorEx = {
    0x6e4e2102, 0xf9cd, 0x433d, {0xb4, 0x96, 0x30, 0x3c, 0xe0, 0x3a, 0x65, 0x07}};

struct ITfTextInputProcessorEx : public ITfTextInputProcessor {
    virtual HRESULT STDMETHODCALLTYPE ActivateEx(ITfThreadMgr* ptim, TfClientId tid,
                                                 DWORD dwFlags) = 0;
};



class ChaosetTIP : public ITfTextInputProcessorEx,
                  public ITfKeyEventSink,
                  public ITfCompositionSink,
                  public CandidateWindowHost {
public:
    ChaosetTIP() {
        InterlockedIncrement(&g_liveObjects);
        // TSF 线程上的传输：只允许单次非阻塞连接 + 30ms 有界请求（fail-open）。
        transport_.setAutoConnect(false);
        transport_.setDeadlineMs(30);
    }
    virtual ~ChaosetTIP() {
        // 防御性收尾：正常路径由 Deactivate 清理，但宿主也可能在未调用
        // Deactivate 的情况下直接释放 TIP（TSF 不保证成对）。窗口过程与
        // SetWinEventHook 钩子都指向本 DLL 内的代码，不撤会让下次
        // WM_PAINT / 前台事件跳到已卸载内存（宿主进程崩溃）。
        //
        // 必须先摘掉线程注册表：析构后本实例若仍留在 g_tlFgTips 里，500ms
        // 激活档案定时器与前台/模式消息会把它当活对象解引用（悬垂指针 →
        // 宿主进程崩溃）。unregisterTipFromThread 还负责「本线程最后一个 TIP
        // 退出时销毁 message-only 窗口」——漏调会让 g_liveTipMsgWindows 永不
        // 归零、DllCanUnloadNow 恒 S_FALSE（DLL 常驻宿主、窗口类残留）。
        // 该函数幂等：Deactivate 已调用过时是空操作，不会双重销毁。
        // 限制：注册表是 thread_local，只在 TSF 常规路径（同线程 Release）有效；
        // 跨线程 Release 时这里看到的是其它线程的空表（原 Deactivate 路径同样
        // 无法清理），彻底修复需要全局 (tip, 线程 id) 注册表，本轮不做。
        unregisterTipFromThread(this);
        if (fgHook_ != nullptr) unhookForeground();
        candidateWnd_.destroy();
        if (tm_ != nullptr) { tm_->Release(); tm_ = nullptr; }
        InterlockedDecrement(&g_liveObjects);
    }

    // IUnknown
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void** ppv) override {
        if (riid == IID_IUnknown) {
            *ppv = static_cast<IUnknown*>(static_cast<ITfTextInputProcessor*>(this));
        } else if (riid == IID_ITfTextInputProcessor) {
            *ppv = static_cast<ITfTextInputProcessor*>(this);
        } else if (riid == kIID_ITfTextInputProcessorEx) {
            *ppv = static_cast<ITfTextInputProcessorEx*>(this);
        } else if (riid == IID_ITfKeyEventSink) {
            *ppv = static_cast<ITfKeyEventSink*>(this);
        } else if (riid == IID_ITfCompositionSink) {
            *ppv = static_cast<ITfCompositionSink*>(this);
        } else {
            *ppv = nullptr;
            return E_NOINTERFACE;
        }
        AddRef();
        return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return InterlockedIncrement(&ref_); }
    ULONG STDMETHODCALLTYPE Release() override {
        ULONG r = InterlockedDecrement(&ref_);
        if (r == 0) delete this;
        return r;
    }

    // ITfTextInputProcessor
    HRESULT STDMETHODCALLTYPE Activate(ITfThreadMgr* ptim, TfClientId tid) override {
        // worker 与 message-only 窗口延迟到「真正激活」（updateInputActive）时
        // 才创建：TSF 会把 TIP 预激活进每个宿主，后台实例不该有线程/窗口/定时器
        //（2026-11-07 用户要求：没激活时不做任何操作）。
        registerTipOnThread(this);
        g_lastOp = kOpActivate;
        // **不在 TSF 宿主进程安装 VEH**（2026-11-07 用户硬要求：输入法不得给
        // 系统/app 增加负担，也绝不能造成宿主 crash）。宿主进程里注册的 VEH
        // 在 TSF FreeLibrary 后会悬空（WER: ChaosetIME.dll_unloaded），且会给
        // 每个宿主的所有 first-chance 异常增加一次回调开销。诊断交给系统 WER；
        // 只有我们自己的 UI 宿主进程（runUiHost）注册 VEH 写 crash.log。
        // 每个宿主进程只记一次 Activate（异步写盘）：字段统一不变，同时避免
        // 反复聚焦耗尽 diagBudget（预算要留给真正的 daemon 失败/渲染失败日志）。
        static volatile LONG activateLogged = 0;
        if (InterlockedCompareExchange(&activateLogged, 1, 0) == 0 && diagBudget()) {
            char host[260];
            diagHostName(host, static_cast<int>(sizeof(host)));
            const HWND fg = GetForegroundWindow();
            DWORD fgPid = 0;
            if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
            diagPrintf(L"tsf.log", "activate pid=%lu fgpid=%lu host=%s appcontainer=%d build=%s %s\n",
                       static_cast<unsigned long>(GetCurrentProcessId()),
                       static_cast<unsigned long>(fgPid), host,
                       isAppContainer() ? 1 : 0, __DATE__, __TIME__);
        }
        // UILess 模式探测（2026-10-30 开始菜单候选窗排障）：每进程只记一次。
        static volatile LONG activeFlagsLogged = 0;
        if (ptim != nullptr && InterlockedCompareExchange(&activeFlagsLogged, 1, 0) == 0 &&
            diagBudget()) {
            ITfThreadMgrEx* tme = nullptr;
            DWORD flags = 0;
            HRESULT hrEx = ptim->QueryInterface(IID_ITfThreadMgrEx,
                                                reinterpret_cast<void**>(&tme));
            HRESULT hrFlags = (SUCCEEDED(hrEx) && tme != nullptr)
                                  ? tme->GetActiveFlags(&flags)
                                  : E_NOINTERFACE;
            if (tme != nullptr) tme->Release();
            diagPrintf(L"tsf.log", "activeflags hr_qi=0x%08lX hr=0x%08lX flags=0x%08lX uiless=%d\n",
                       static_cast<unsigned long>(hrEx),
                       static_cast<unsigned long>(hrFlags),
                       static_cast<unsigned long>(flags),
                       (SUCCEEDED(hrFlags) && (flags & 0x4)) ? 1 : 0);
        }
        // 部分宿主会重复调用 Activate：初始化必须幂等（否则泄漏 ITfThreadMgr、
        // 重复 Advise 会让每个按键投递两次）。
        if (tm_ != nullptr && tm_ != ptim) {
            tm_->Release();
            tm_ = nullptr;
        }
        if (tm_ == nullptr) {
            tm_ = ptim;
            if (tm_ != nullptr) tm_->AddRef();
        }
        clientId_ = tid;
        if (profileMgr_ == nullptr) {
            const HRESULT hrProfile = CoCreateInstance(
                kCLSID_TF_InputProcessorProfiles, nullptr, CLSCTX_INPROC_SERVER,
                IID_ITfInputProcessorProfileMgr, reinterpret_cast<void**>(&profileMgr_));
            static volatile LONG loggedProfileMgr = 0;
            if (InterlockedCompareExchange(&loggedProfileMgr, 1, 0) == 0) {
                diagPrintf(L"tsf.log", "activetip profile mgr hr=0x%08lX\n",
                           static_cast<unsigned long>(hrProfile));
            }
        }
        // 线程级隔间：把中英模式交给 Windows 输入指示器（任务栏「中/英」）。
        if (tm_ != nullptr && modeCompartment_ == nullptr) {
            ITfCompartmentMgr* compMgr = nullptr;
            if (SUCCEEDED(tm_->QueryInterface(IID_ITfCompartmentMgr,
                                              reinterpret_cast<void**>(&compMgr))) &&
                compMgr != nullptr) {
                compMgr->GetCompartment(kGUID_COMPARTMENT_KEYBOARD_OPENCLOSE,
                                        &modeCompartment_);
                compMgr->Release();
            }
        }
        syncModeCompartment();
        // 模式指示器归单例 UI 进程所有（chaoset-ui.exe 托盘图标唯一），TIP 只上报。
        // 只有「本线程激活档案确实是我们」才同步/上报；后台实例在这里就被拦下。
        caretFound_ = false;
        caretFromMouse_ = false;
        if (!keySinkAdvised_) {
            ITfKeystrokeMgr* mgr = nullptr;
            if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
                keySinkAdvised_ = SUCCEEDED(mgr->AdviseKeyEventSink(clientId_, keySink(), TRUE));
                mgr->Release();
            }
        }
        // 激活状态迁移：Activate 可能是后台宿主（TSF 会提前把 TIP 装进任意
        // 进程），只有本进程是前台且激活档案是 Chaoset 才进入「在用」状态。
        // 之后所有按键回调都以 inputActive_ 为闸门，未激活时零动作。
        focused_ = foregroundIsSelf();
        updateInputActive(true);
        return S_OK;
    }

    // ITfTextInputProcessorEx（2026-11-05）：UILess 线程下 TSF 调这个而不是 Activate。
    //
    // 当前只做两件事：记录 dwFlags（拿到「是否真的存在 UILess 宿主」的第一手
    // 数据），然后复用 Activate 的初始化。**渲染路径暂不变**——是否改用
    // ITfUIElementMgr 提交候选，等日志出现 uielementonly=1 再决定。
    //
    // 注意：在 UILess 线程里按微软文档「categorized by GUID_TFCAT_TIPCAP_
    // UIELEMENTENABLED 的 TIP 必须用 ITfUIElementMgr 显示所有 UI」。我们尚未
    // 注册该类别（注册会全局生效，声明与实现不一致时正是 2026-10-30
    // 「开始菜单里候选窗完全消失」的原因），所以这里不会主动走 UI 元素路径。
    HRESULT STDMETHODCALLTYPE ActivateEx(ITfThreadMgr* ptim, TfClientId tid,
                                         DWORD dwFlags) override {
        // 每进程只记一次（异步写盘）：UILess 宿主会反复激活，避免刷预算。
        static volatile LONG loggedEx = 0;
        if (InterlockedCompareExchange(&loggedEx, 1, 0) == 0 && diagBudget()) {
            diagPrintf(L"tsf.log", "activateEx flags=0x%08lX uielementonly=%d selfpid=%lu\n",
                       static_cast<unsigned long>(dwFlags),
                       (dwFlags & TF_TMAE_UIELEMENTENABLEDONLY) ? 1 : 0,
                       static_cast<unsigned long>(GetCurrentProcessId()));
        }
        if ((dwFlags & TF_TMAE_UIELEMENTENABLEDONLY) != 0) {
            // UILess 线程：本壳尚未实现 ITfUIElementMgr，选择不激活，等价于旧
            // 行为（不实现该接口的 TIP 在此类线程根本不会被激活）。
            return S_OK;
        }
        return Activate(ptim, tid);
    }

    HRESULT STDMETHODCALLTYPE Deactivate() override {
        // 停用即「非激活」：先从线程注册表/钩子表摘掉，再彻底退场。
        if (inputActive_) {
            inputActive_ = false;
            focused_ = false;
            postActiveReport(false, currentEnglish(), false);
        } else {
            focused_ = false;
        }
        unregisterTipFromThread(this);
        retireUiCompletely();
        pendingStaleReset_ = false;
        if (profileMgr_ != nullptr) {
            profileMgr_->Release();
            profileMgr_ = nullptr;
        }
        if (modeCompartment_ != nullptr) {
            modeCompartment_->Release();
            modeCompartment_ = nullptr;
        }
        // 已确认段先落盘（与 macOS deactivateServer 语义一致）。
        if (!confirmedPreedit_.empty()) {
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(compCtx_, all);
        }
        endComposition();
        resetSession();
        comp_ = chaoset::Comp();
        resetShiftTracking();
        dpScheme_.clear();
        if (keySinkAdvised_ && tm_ != nullptr) {
            ITfKeystrokeMgr* mgr = nullptr;
            if (SUCCEEDED(tm_->QueryInterface(IID_ITfKeystrokeMgr, reinterpret_cast<void**>(&mgr)))) {
                mgr->UnadviseKeyEventSink(clientId_);
                mgr->Release();
            }
            keySinkAdvised_ = false;
        }
        if (tm_ != nullptr) {
            tm_->Release();
            tm_ = nullptr;
        }
        if (probeCtx_ != nullptr) {
            probeCtx_->Release();
            probeCtx_ = nullptr;
            probeCan_ = false;
        }
        return S_OK;
    }

    // ITfKeyEventSink
    HRESULT STDMETHODCALLTYPE OnSetFocus(BOOL fForeground) override {
        if (!fForeground) {
            focused_ = false;
            // 从未激活的实例：**零动作**（不比较 goal、不清理、不上报）。
            if (!inputActive_) return S_OK;
            // 失焦 = 非激活：updateInputActive 内部上报 active 0、销毁浮窗、撤钩子。
            updateInputActive(false);
            if (isComposing() || !confirmedPreedit_.empty()) {
                resetSession();
                comp_ = chaoset::Comp();
                if (!confirmedPreedit_.empty()) {
                    const std::string all = confirmedPreedit_;
                    confirmedPreedit_.clear();
                    commit(compCtx_, all);
                } else {
                    endComposition();
                }
            }
            return S_OK;
        }
        // 焦点回来：TSF 已把焦点交给本上下文，不依赖 GetForegroundWindow 是否更新。
        focused_ = true;
        updateInputActive(true);
        if (inputActive_) {
            // 清掉「没有活合成、但本地还挂着组词」的陈旧状态。
            if (composition_ == nullptr && (isComposing() || !confirmedPreedit_.empty())) {
                resetComposition();
                comp_ = chaoset::Comp();
                confirmedPreedit_.clear();
                candidateWnd_.hide();
            }
            shiftHoldSuspended_ = false;  // 焦点回来：按住的 Shift 松开时仍可切换
        }
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                            BOOL* pfEaten) override {
        g_lastOp = kOpTestKeyDown;
        if (bailOutIfInactive()) {
            *pfEaten = FALSE;
            return S_OK;
        }
        applyPendingStaleReset();  // 前台已离开过：先丢掉上一轮的残留组词
        // Shift 记账自愈必须覆盖所有按键（含修饰键）：若上一次按住的 KeyUp
        // 丢了，这里先复位，随后的 Shift 按下才能重新开一次干净的会话。
        reconcileShiftState();
        if (isModifierKey(wParam)) {
            if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
                onShiftEvent(false, wParam, pic);
            } else if (shiftHeld_ != 0) {
                sinceShift_ = true;
            }
            *pfEaten = FALSE;  // 修饰键始终被动放行，绝不进入 handleKey 干扰系统状态
            return S_OK;
        }
        // 任何非 Shift 键按下都取消「Shift 空按」窗口。必须放在 Test 阶段：
        // 原实现放在 OnKeyDown，Shift+方向键/功能键等未组词的按键不会进
        // OnKeyDown → sinceShift_ 仍为 false → 松 Shift 时误切换中英。
        sinceShift_ = true;
        if (currentEnglish()) {
            *pfEaten = FALSE;  // 英文模式全放行
            return S_OK;
        }
        if (!wouldHandle(wParam)) {
            *pfEaten = FALSE;
            return S_OK;
        }
        // 需要新建合成（字母）或插入文本（标点）的键，在「不支持该操作的
        // 上下文」里必须放行：2026-10-29 实机反馈，Windows 11 开始菜单里
        // 按字母没有任何反应，搜索框也不出现——键在 OnTestKeyDown 被吃掉，
        // 而 applyPreedit 在这个上下文里建不出合成，字符既不进文档也不进
        // 宿主自己的搜索框。这里先做一次无副作用的接口探测（只读文档、
        // 缺 ITfInsertAtSelection / ITfContextComposition 的上下文直接放行）；
        // handleKey 里还有一道「渲染后仍无合成则放行」的兜底（OnKeyDown 的
        // pfEaten 才是最终裁决，见 ITfKeyEventSink 两阶段设计）。
        if (composition_ == nullptr) {
            const bool shift = shiftActive();
            const bool letter = !shift && wParam >= kVkFirstLetter && wParam <= kVkLastLetter;
            const bool punct = keyPunctChar(wParam, shift) != nullptr;
            if (letter || punct) {
                ITfContext* ctx = pic;
                ContextRef owned(nullptr);
                if (ctx == nullptr) {
                    owned.ctx = focusContext();
                    ctx = owned.ctx;
                }
                bool ok;
                if (letter && ctx == probeCtx_) {
                    ok = probeCan_;  // 缓存命中：零 COM 调用
                } else {
                    ok = letter ? canStartComposition(ctx) : canInsert(ctx);
                    if (letter && ctx != nullptr) {
                        if (probeCtx_ != nullptr) probeCtx_->Release();
                        probeCtx_ = ctx;
                        probeCtx_->AddRef();
                        probeCan_ = ok;
                    }
                }
                if (!ok) {
                    *pfEaten = FALSE;
                    return S_OK;
                }
            }
        }
        *pfEaten = TRUE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                        BOOL* pfEaten) override {
        g_lastOp = kOpKeyDown;
        if (bailOutIfInactive()) {
            *pfEaten = FALSE;
            return S_OK;
        }
        applyPendingStaleReset();
        // Shift 记账自愈也必须覆盖 Key 阶段：Chrome/conhost/Windows Terminal
        // 等宿主的内容键只进 Key 阶段（Test 阶段收不到修饰键），只挂 Test
        // 会让这些宿主丢 KeyUp 后没有收口路径（2026-11-05 回归审计 P2-11）。
        reconcileShiftState();
        if (isModifierKey(wParam)) {
            if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
                onShiftEvent(false, wParam, pic);
            } else if (shiftHeld_ != 0) {
                sinceShift_ = true;
            }
            *pfEaten = FALSE;  // 修饰键始终放行，绝不进入 handleKey 干扰系统状态
            return S_OK;
        }
        sinceShift_ = true;  // 幂等兜底（正常已在 OnTestKeyDown 置位）
        if (currentEnglish()) {
            clearAssociate();
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            *pfEaten = FALSE;  // 英文模式全放行
            return S_OK;
        }
        if (!wouldHandle(wParam)) {
            clearAssociate();
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            *pfEaten = FALSE;  // 非组词按键一律放行给宿主
            return S_OK;
        }
        *pfEaten = handleKey(wParam, pic) ? TRUE : FALSE;
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnTestKeyUp(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                          BOOL* pfEaten) override {
        if (bailOutIfInactive()) {
            *pfEaten = FALSE;
            return S_OK;
        }
        // 只有 Shift 键释放才参与中英切换判定；
        // 其它任何按键（包括 Ctrl、Alt、Win 等修饰键）的释放，输入法绝不关心、不干预、直接放行
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            onShiftEvent(true, wParam, pic);
        }
        *pfEaten = FALSE;  // KeyUp 一律不消费，保证宿主应用状态机完整，绝不导致宿主丢键或键盘卡死
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnKeyUp(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
                                      BOOL* pfEaten) override {
        if (bailOutIfInactive()) {
            *pfEaten = FALSE;
            return S_OK;
        }
        // 只有 Shift 键释放才参与中英切换判定（Key 阶段对称兜底）；
        // 其它任何按键（包括 Ctrl、Alt、Win 等修饰键）的释放，输入法绝不关心、不干预、直接放行
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            onShiftEvent(true, wParam, pic);
        }
        *pfEaten = FALSE;  // 只透传不消费
        return S_OK;
    }

    HRESULT STDMETHODCALLTYPE OnPreservedKey(ITfContext* /*pic*/, REFGUID /*rguid*/, BOOL* pfEaten) override {
        *pfEaten = FALSE;
        return S_OK;
    }

    // ITfCompositionSink：合成被外部终止（焦点切换等）
    HRESULT STDMETHODCALLTYPE OnCompositionTerminated(TfEditCookie /*ec*/,
                                                      ITfComposition* /*pComposition*/) override {
        g_lastOp = kOpCompositionTerminated;
        candidateWnd_.hide();
        // 引用所有权：TSF 只在「本服务以外的人」结束合成时回调（Microsoft
        // SampleIME Composition.cpp 注释原文：whenever someone other than
        // this service ends a composition）。这时成员仍持有引用，必须在此
        // 释放；自己 EndComposition 的路径已先把成员摘成局部引用并自行
        // Release，看到 nullptr 就不能再释放（否则二次释放）。
        if (composition_ != nullptr) {
            composition_->Release();
            composition_ = nullptr;
        }
        releaseCompositionContext();
        resetSession();
        comp_ = chaoset::Comp();
        // 合成被宿主终止时 TSF 会把合成内文本（含已确认段）留在文档里，
        // 缓冲使命已结束，必须清空——绝不能串进下一轮组词的预编辑。
        confirmedPreedit_.clear();
        return S_OK;
    }

    void selectCandidateByIndex(int index) override {
        g_lastOp = kOpCandidateClick;
        // 联想窗点击：联想候选直接上屏并继续联想（与数字键同路径）
        if (!assoc_.candidates.empty()) {
            const int global = assocPage_ * kPageSize + index;
            if (index < 0 || global >= static_cast<int>(assoc_.candidates.size())) return;
            const chaoset::Candidate pick = assoc_.candidates[global];
            ContextRef ctx(focusContext());
            std::string committed;
            if (session_.associateSelect(pick.text, splitSyllables(pick.syllables), &committed)) {
                const std::string sylls = pick.syllables;
                if (commit(ctx.get(), committed)) enterAssociate(sylls);
            } else {
                clearAssociate();
            }
            return;
        }
        if (!hasCandidates()) return;
        if (index < 0 || index >= static_cast<int>(comp_.candidates.size())) return;
        ContextRef ctx(focusContext());
        // 鼠标点选走与数字键/空格相同的**渐进上屏**路径（2026-10-30 三端
        // 一致性整改 A4）：此前直接 session_.select(index)，长串拼音下点一个
        // 候选会把整串拼音全部清空/上屏，而键盘点选只消耗该候选对应音节、
        // 剩余拼音回灌继续组词——同一个候选窗两种选词语义。macOS
        // ChaosetController.commit 与下方键盘路径都先 selectPrefix，失败
        //（daemon 不可达/越界）才回退整句上屏。
        const std::string pickedSyllables = comp_.candidates[index].syllables;
        chaoset::Comp rest;
        if (session_.selectPrefix(index, &rest) && !rest.committed.empty()) {
            syncDpScheme(rest);  // 契约：每次 Composition 响应都带双拼方案名
            // 单合成渐进（见 confirmedPreedit_ 注释）：确认段并入当前合成，
            // 不再中途结束/重建合成。剩余耗尽才整串一次落盘并进入联想。
            confirmedPreedit_ += rest.committed;
            comp_ = rest;
            if (!comp_.input.empty()) {
                render(ctx.get());
            } else {
                comp_ = chaoset::Comp();
                const std::string all = confirmedPreedit_;
                confirmedPreedit_.clear();
                commit(ctx.get(), all);
                enterAssociate(pickedSyllables);
            }
            return;
        }
        std::string committed;
        if (session_.select(index, &committed)) {
            const std::string all = confirmedPreedit_ + committed;
            confirmedPreedit_.clear();
            commit(ctx.get(), all);
        }
    }

    // 候选窗右键「删除该词」：按候选的「词 + 音节」删除本机学习数据
    // （daemon 的 Words 调频 + Phrases 词组；bigram 上下文共现保留，见
    // engine.UserDict.DeleteWord 注释）。约束与 macOS 壳一致：
    //   - 只删学习记录：不上屏、不改文档、不结束组词；
    //   - 写类 IPC 不重试（重试会把「已删除」变成「未找到」，也不应重放写请求）；
    //   - 删除成功后刷新候选：组词态 reset + 重放当前剩余拼音（daemon 不缓存
    //     候选列表）；联想态按同一联想来源音节重新查询，删空则收起面板。
    void deleteCandidateByIndex(int index) override {
        g_lastOp = kOpCandidateClick;
        if (!assoc_.candidates.empty()) {
            const int global = assocPage_ * kPageSize + index;
            if (index < 0 || global >= static_cast<int>(assoc_.candidates.size())) return;
            const chaoset::Candidate pick = assoc_.candidates[global];
            bool deleted = false;
            if (!session_.userDelete(pick.text, splitSyllables(pick.syllables), &deleted)) {
                showToast(L"删除失败：daemon 不可达", true);
                return;
            }
            if (!deleted) {
                showToast(L"未找到该词的学习记录", true);
                return;
            }
            const std::string src = assocSylls_;
            assoc_ = chaoset::Comp();
            assocPage_ = 0;
            std::vector<std::string> sylls = splitSyllables(src);
            chaoset::Comp refreshed;
            if (!sylls.empty() && session_.associate(sylls, &refreshed) &&
                !refreshed.candidates.empty()) {
                syncDpScheme(refreshed);
                assoc_ = std::move(refreshed);
                assocPage_ = 0;
                showAssociatePage();
            } else {
                candidateWnd_.hide();
            }
            showToast(L"已删除该词", true);
            return;
        }
        if (!hasCandidates()) return;
        if (index < 0 || index >= static_cast<int>(comp_.candidates.size())) return;
        const chaoset::Candidate pick = comp_.candidates[index];
        bool deleted = false;
        if (!session_.userDelete(pick.text, splitSyllables(pick.syllables), &deleted)) {
            showToast(L"删除失败：daemon 不可达", true);
            return;
        }
        if (!deleted) {
            showToast(L"未找到该词的学习记录", true);
            return;
        }
        // 重新组词刷新候选。已确认段 confirmedPreedit_ 是壳侧状态，不影响。
        ContextRef ctx(focusContext());
        const std::string raw = comp_.input;
        resetComposition();
        if (raw.empty()) {
            comp_ = chaoset::Comp();
            render(ctx.get());
        } else {
            chaoset::Comp rest = session_.input(raw, halfWidth_);
            if (!rest.ok) {
                // 极端故障（daemon 半死）：已确认段先落盘，避免合成悬空丢字。
                diagDaemonRequestFailure(transport_, "input(refresh)");
                comp_ = chaoset::Comp();
                flushOrCancel(ctx.get());
                showToast(L"删除后刷新候选失败", true);
                return;
            }
            syncDpScheme(rest);
            comp_ = std::move(rest);
            render(ctx.get());
        }
        showToast(L"已删除该词", true);
    }

    void flipPage(bool next) override {
        if (!hasCandidates() || comp_.pageCount <= 1) return;
        g_lastOp = kOpCandidateClick;
        ContextRef ctx(focusContext());
        comp_ = next ? session_.nextPage() : session_.prevPage();
        if (!comp_.ok) {
            // daemon 故障：已确认段先落盘收尾，不能随翻页失败被 endComposition 丢弃
            diagDaemonRequestFailure(transport_, "page(mouse)");
            flushOrCancel(ctx.get());
            return;
        }
        syncDpScheme(comp_);
        render(ctx.get());
    }

private:
    ITfKeyEventSink* keySink() { return static_cast<ITfKeyEventSink*>(this); }
    ITfCompositionSink* compositionSink() { return static_cast<ITfCompositionSink*>(this); }

    bool isComposing() const { return !comp_.input.empty(); }
    bool hasCandidates() const { return !comp_.candidates.empty(); }

    // ---- 中英切换：Shift 空按（按下后未按其他键即释放）----
    // 语义与 ibus 壳的 shiftTracker（shims/linux/engine/main.cpp:50-72）一致；
    // 事件路径见 OnTestKeyDown 等分流。
    //
    // **记账方式 2026-11-05 重写：按「一次按住会话」记账，不再按事件累加。**
    // 旧实现用 shiftDown_++/--：每收到一次 Shift 按下就 +1、每收到一次释放才 -1，
    // 只有计数回到 0 才结算。只要丢失**任意一次** KeyUp，计数就只增不减、永远
    // 回不到 0 —— 该进程内 Shift 空按**永久失效**，只能重启应用恢复。
    // 丢失 KeyUp 的典型场景（都不是宿主 bug，而是 Windows 的既定行为）：
    //   1. 按住 Shift 时切换窗口（Alt+Tab / 点别的窗口）：KeyUp 交给**新前台
    //      窗口**，本线程永远收不到；
    //   2. TIP 被 TSF 停用期间发生的按键（Deactivate → 再 Activate）；
    //   3. 宿主重复投递同一次按下（auto-repeat / TSF 与宿主各投递一次）。
    // 这正好解释实机症状：Terminal / 开始菜单 / Chrome（长驻进程 + 频繁切走
    // 窗口）切不了，Firefox（同一会话里连续空按）可以。
    // 本地中英模式缓存。真正语义由 ModeScope 决定（见 currentEnglish）：
    //   system  —— 权威在 UI 宿主，这里只是「上次查到的值」，按键时会刷新。
    //   process —— 本进程所有 TIP 实例共享（下面 g_procEnglish）。
    //   window  —— 按宿主窗口分开存（g_winEnglish）。
    bool enMode_ = false;

    // 进程级/窗口级状态（process / window 作用域用）。故意放在进程静态区：
    // TSF 会在同一宿主进程里创建多个 TIP 实例，process 档要求它们共享。
    static std::map<HWND, bool>& windowModes() {
        static std::map<HWND, bool> m;
        return m;
    }
    static bool& processEnglish() {
        static bool english = false;
        return english;
    }

    // 当前宿主窗口（window 作用域的分桶键）。拿不到就退化成进程级，
    // 绝不能因为取不到窗口而让状态随机跳。
    HWND modeBucketKey() const {
        const HWND fg = GetForegroundWindow();
        if (fg != nullptr) return fg;
        return GetActiveWindow();
    }

    // 读当前中英模式（**所有判断都应该走这里**，不要直接读 enMode_）。
    //
    // 关键性能约束（2026-11-06 用户要求）：「窗口在获取焦点以后才有必要去获取、
    // 更新这个状态，用不到的时候没必要浪费性能」。所以这里**只读本地缓存**，
    // 不做任何 IPC —— 它在每个按键的 OnTestKeyDown 里都会被调用。
    // system 作用域的跨进程同步只在 OnSetFocus / Activate 时做一次（见
    // syncEnglishFromAuthority）。
    // 读当前中英模式（所有判断都应该走这里，不要直接读 enMode_）。
    //
    // 性能约束（2026-11-06 用户要求）：按键路径只读本地缓存；ModeScope 查询
    // 也走 TTL 缓存（cachedModeScope），绝不每键读注册表（审计 T10）。
    bool currentEnglish() {
        switch (cachedModeScope()) {
            case ModeScope::System:
                return enMode_;
            case ModeScope::Process:
                return processEnglish();
            case ModeScope::Window: {
                const HWND key = modeBucketKey();
                auto it = windowModes().find(key);
                if (it != windowModes().end()) return it->second;
                windowModes()[key] = enMode_;
                return enMode_;
            }
        }
        return enMode_;
    }

    // 从权威源同步一次（只在获得焦点/激活时调用）。
    //
    // 2026-11-06 零阻塞审计（P0）：旧实现同步跨进程查询中英状态，真机实测其
    // 「200ms 上限」是死代码（同步句柄 + OVERLAPPED），UI 宿主挂起时可无限
    // 阻塞宿主 UI 线程。现在：HKCU UiEnglish 镜像微秒级读 + worker 异步校准
    // （结果经 message-only 窗口的 kTipModeAuthorityMsg 回投）。
    void syncEnglishFromAuthority() {
        if (cachedModeScope() != ModeScope::System) return;
        bool english = false;
        if (readHkcuEnglish(&english)) {
            enMode_ = english;
            syncModeCompartment();  // 让系统输入指示器/隔间也跟上
        }
        postModeQuery();
    }

    // 写当前中英模式（所有切换都必须走这里）。
    void storeEnglish(bool english) {
        enMode_ = english;
        switch (cachedModeScope()) {
            case ModeScope::System:
                // 权威在 UI 宿主：上报即写入（system 档下 wndproc 会更新托盘+HKCU）。
                break;
            case ModeScope::Process:
                processEnglish() = english;
                break;
            case ModeScope::Window:
                windowModes()[modeBucketKey()] = english;
                break;
        }
        syncModeCompartment();
        // 上报托盘：reportModeToUi 入口统一要求「本进程持有焦点」才放行，
        // 实际管道写入在 ShellWorker（零阻塞）。
        reportModeToUi(english, true);
    }
    // 当前按住的 Shift 物理键位掩码（bit0=左、bit1=右、bit2=泛化 VK_SHIFT）。
    // 用**掩码**而不是计数器：置位/清位天然幂等，键盘 auto-repeat 或宿主重复
    // 投递同一次按下都不会把它推到「永远回不到 0」——这正是旧实现 ++/-- 的
    // 致命缺陷。同时掩码能正确区分左右 Shift 同时按住的场景。
    unsigned shiftHeld_ = 0;
    bool sinceShift_ = true;    // 按住期间是否出现过其它键（true = 不是空按）
    // 按住期间焦点离开过。浏览器会在**窗口内部**把焦点在地址栏/页面元素之间
    // 搬来搬去，一样触发 OnSetFocus(FALSE)——此时不能直接丢弃按住状态，
    // 否则「按下 Shift → 焦点抖一下 → 松开」就永远切换不了（2026-11-05）。
    // 改为挂起：松开前焦点回来就照常切换，否则松开时只清状态不切换
    // （真·切走应用时不该切模式）。
    bool shiftHoldSuspended_ = false;

    // ---- 前台切换兜底（2026-11-05）----
    //
    // 为什么需要：Windows 11 开始菜单/搜索这类宿主**不投递 OnSetFocus(FALSE)**
    //（实机：关闭开始菜单后重开，上一轮的拼音与候选窗都还在）。TSF 焦点事件
    // 在这类宿主上不可靠，改用系统级的前台窗口事件（SetWinEventHook，
    // 事件驱动、非轮询）作为「用户已经离开本窗口」的可靠信号。
    //
    // 回调里**不碰 TSF**（可能不在本 TIP 的焦点上下文中，贸然 RequestEditSession
    // 会失败甚至干扰宿主）：只收起自己的浮窗并打标记，真正的状态清理由下一次
    // 按键在 TIP 线程上完成。
    HWINEVENTHOOK fgHook_ = nullptr;
    bool pendingStaleReset_ = false;

    // 线程级隔间（GUID_COMPARTMENT_KEYBOARD_OPENCLOSE）：把中英模式写进 TSF
    // 线程隔间（其他 TSF 客户端可读，语义与微软拼音一致）。任务栏「中/英」
    // 指示器由单例 UI 进程绘制，TIP 不再持有任何托盘状态。
    ITfCompartment* modeCompartment_ = nullptr;

    // **当前激活的输入法是不是我们**（2026-11-05 用户反馈：选定的输入法不是
    // chaoset 时按 Shift 仍会出中英提示）。
    //
    // 实测：切到其它输入法/英文键盘布局后，TSF 仍会把按键投给本 TIP 的按键
    // 下沉（我们的 Deactivate 未被调用或不及时），于是 Shift 照样触发切换。
    // 正确判据是查询本线程的**激活输入处理器档案**：
    //   ITfInputProcessorProfileMgr::GetActiveProfile(GUID_TFCAT_TIP_KEYBOARD)
    // 返回的 clsid 不是我们 → 直接放行所有按键，绝不参与输入。
    ITfInputProcessorProfileMgr* profileMgr_ = nullptr;

    bool isActiveTip() {
        // 查询器不可用 → **fail-closed**（按「不是我们」处理）。
        //
        // 2026-11-06 用户要求：「输入法不激活就不该有任何动作，让用户感觉不到
        // 它的存在」。旧实现这里 return true（怕把输入搞坏），结果是：只要
        // 档案查询器建不出来，后台 TIP 实例就会被当成激活着，继续建候选窗、
        // 挂全局钩子、处理按键——正是用户看到的「鼠标点了没反应 / 系统设置卡」。
        // 反过来 fail-closed 的最坏后果只是「输入法暂时不出候选」，比干扰系统小得多。
        if (profileMgr_ == nullptr) return false;
        TF_INPUTPROCESSORPROFILE prof{};
        const HRESULT hr = profileMgr_->GetActiveProfile(kGUID_TFCAT_TIP_KEYBOARD, &prof);
        // 首次查询结果留一条日志：否则「非激活整键放行」是否生效只能靠猜
        //（2026-11-05 回归审计 P2-5：全量日志 0 条 inactive 无法自证）。
        static volatile LONG loggedQuery = 0;
        if (InterlockedCompareExchange(&loggedQuery, 1, 0) == 0) {
            diagPrintf(L"tsf.log", "activetip query hr=0x%08lX type=%lu match=%d\n",
                       static_cast<unsigned long>(hr),
                       static_cast<unsigned long>(prof.dwProfileType),
                       prof.clsid == CLSID_ChaosetTIP ? 1 : 0);
        }
        // 查询失败同样 fail-closed：宁可放行按键，也不在不确定时干预用户。
        if (FAILED(hr)) return false;
        return prof.dwProfileType == TF_PROFILETYPE_INPUTPROCESSOR && prof.clsid == CLSID_ChaosetTIP;
    }
    // 前台钩子回调 → TIP 线程的映射。SetWinEventHook 没有 user data；这是
    // WINEVENT_OUTOFCONTEXT，回调在注册线程上派发，所以用 thread_local 容器。
    //
    // 2026-11-06 零阻塞审计（T6）：每线程只挂一个系统级钩子（多个 TIP 实例
    // 共用）；回调体内只 PostMessage（不 hide、不写盘）；真正的清理在 TIP
    // 线程消息处理里做，且前台离开本进程时自动撤钩子（shell 宿主不发
    // OnSetFocus(FALSE)，旧实现会让钩子整会话常驻并放大每次前台派发）。
    static void CALLBACK fgHookProc(HWINEVENTHOOK /*hook*/, DWORD event, HWND hwnd, LONG, LONG,
                                    DWORD, DWORD) {
        if (event != EVENT_SYSTEM_FOREGROUND) return;
        if (g_tlTipMsgWnd != nullptr) {
            PostMessageW(g_tlTipMsgWnd, kTipForegroundMsg,
                         reinterpret_cast<WPARAM>(hwnd), 0);
        }
    }

    // 在 TIP 线程处理前台变化（由 message-only 窗口回调）。
    void onForegroundChanged(HWND fg) {
        if (fg == nullptr) return;
        if (composeHostWnd_ != nullptr && fg == composeHostWnd_) return;
        DWORD fgPid = 0;
        GetWindowThreadProcessId(fg, &fgPid);
        if (fgPid == GetCurrentProcessId()) {
            // 前台回到本进程：恢复处理并按当前 app 的本地模式上报托盘
            //（process/window 作用域下托盘跟 app 对齐）。
            focused_ = true;
            updateInputActive(true);
            return;
        }
        // 前台离开本进程：开始菜单/搜索等宿主不投递 OnSetFocus(FALSE)，靠这里收口。
        const bool wasActive = inputActive_;
        inputActive_ = false;
        focused_ = false;
        if (wasActive) {
            postActiveReport(false, currentEnglish(), false);
            retireUiCompletely();
            suspendShiftHoldOnFocusLost();
        }
        candidateWnd_.hide();
        toast_.hide();
        if (isComposing() || !confirmedPreedit_.empty() || !assoc_.candidates.empty()) {
            pendingStaleReset_ = true;
            // 限流诊断（异步写盘）：前台风暴时不再每个事件写一行（审计 T5）。
            static DWORD lastLog = 0;
            const DWORD now = GetTickCount();
            if (diagBudget() && (lastLog == 0 || now - lastLog >= 5000)) {
                lastLog = now;
                diagPrintf(L"tsf.log", "stale state dropped (foreground changed) selfpid=%lu\n",
                           static_cast<unsigned long>(GetCurrentProcessId()));
            }
        }
        unhookForeground();
    }

    // 前台钩子的按需注册：只有「本线程确实在用我们的输入法 + 本进程是前台」
    // 时才挂；每线程最多一个系统级钩子（多 TIP 实例共用，审计 T6）。
    bool shouldWatchForeground() const {
        if (!shellHostNeedsOwner()) return false;
        if (!const_cast<ChaosetTIP*>(this)->isActiveTip()) return false;
        const HWND fg = GetForegroundWindow();
        if (fg == nullptr) return false;
        DWORD fgPid = 0;
        GetWindowThreadProcessId(fg, &fgPid);
        return fgPid == GetCurrentProcessId();
    }

    void ensureForegroundHook() {
        if (fgHook_ != nullptr || !shouldWatchForeground()) return;
        if (g_tlFgHook == nullptr) {
            g_tlFgHook = SetWinEventHook(EVENT_SYSTEM_FOREGROUND, EVENT_SYSTEM_FOREGROUND,
                                         nullptr, fgHookProc, 0, 0, WINEVENT_OUTOFCONTEXT);
            if (g_tlFgHook == nullptr) {
                if (diagBudget()) {
                    diagPrintf(L"tsf.log", "foreground hook FAILED err=%lu\n", GetLastError());
                }
                return;
            }
        }
        fgHook_ = g_tlFgHook;
        addTipToThreadHookList(this);
    }

    void unhookForeground() {
        removeTipFromThreadHookList(this);
        fgHook_ = nullptr;
        if (g_tlHookTips.empty() && g_tlFgHook != nullptr) {
            UnhookWinEvent(g_tlFgHook);
            g_tlFgHook = nullptr;
        }
    }
    // ---- 激活状态机（2026-11-07 用户要求：未激活时不做任何操作）----
    //
    // inputActive_ 是**所有 TSF 按键/焦点逻辑的唯一闸门**：只有「TSF 焦点在本
    // 上下文 + 本线程激活档案是 Chaoset」时才为 true。为 false 时按键回调立即
    // 放行、不查询/不清理/不上报；清理已在上一次 active→inactive 迁移时完成。
    bool focused_ = false;      // OnSetFocus 记录的 TSF 焦点
    bool inputActive_ = false;  // 前台激活输入法（真正的“在用”）
    bool warmed_ = false;       // daemon 预热每实例只发一次（修重复 spawn）

    static bool foregroundIsSelf() {
        const HWND fg = GetForegroundWindow();
        if (fg == nullptr) return false;
        DWORD fgPid = 0;
        GetWindowThreadProcessId(fg, &fgPid);
        return fgPid == GetCurrentProcessId();
    }

    // 唯一的激活状态迁移出口。fromFocusEvent：OnSetFocus(TRUE) 时 TSF 已把焦点
    // 交给本上下文，允许不等 GetForegroundWindow 更新；Activate 则需要前台校验
    //（后台宿主会重复 Activate）。
    void updateInputActive(bool fromFocusEvent) {
        const bool next = focused_ && isActiveTip() &&
                          (fromFocusEvent || foregroundIsSelf());
        if (next == inputActive_) {
            if (!next) {
                candidateWnd_.setActiveGate(false);
                unhookForeground();
            } else {
                candidateWnd_.setActiveGate(true);
            }
            return;
        }
        inputActive_ = next;
        if (next) {
            // 第一次真正激活：创建后台 worker + TIP 线程 message-only 窗口
            //（之后所有异步上报/守卫都依赖它们；未激活实例不创建）。
            shellWorkerStart();
            ensureTipMsgWnd();
            // system 作用域：从 HKCU 镜像同步 + worker 异步校准；其它 scope 用本地值。
            syncEnglishFromAuthority();
            syncModeCompartment();
            // 先 active 再 mode：UI 宿主只有收到 active 1 才显示托盘。
            postActiveReport(true, currentEnglish(), true);
            reportModeToUi(currentEnglish(), true);
            candidateWnd_.setActiveGate(true);
            ensureForegroundHook();
            if (!transport_.isConnected()) warmDaemonInBackgroundOnce();
            refreshActiveWatchTimer();
            static volatile LONG loggedOn = 0;
            if (InterlockedCompareExchange(&loggedOn, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "input active=1 pid=%lu fgpid=%lu\n",
                           static_cast<unsigned long>(GetCurrentProcessId()),
                           static_cast<unsigned long>(0));
            }
        } else {
            // 失焦/停用/前台离开：先通知托盘隐藏，再彻底退场。
            postActiveReport(false, currentEnglish(), false);
            retireUiCompletely();
            // 保留旧的「焦点抖一下不丢 Shift 会话」语义：物理键仍按着则挂起，
            // 释放后由下一次按键按「挂起」规则收口（真切走应用时不会误切中英）。
            suspendShiftHoldOnFocusLost();
            refreshActiveWatchTimer();
            static volatile LONG loggedOff = 0;
            if (InterlockedCompareExchange(&loggedOff, 1, 0) == 0 && diagBudget()) {
                diagPrintf(L"tsf.log", "input active=0 pid=%lu\n",
                           static_cast<unsigned long>(GetCurrentProcessId()));
            }
        }
    }

    // daemon 预热（保留原函数名/调用形态作为 checkuiparity 契约）：只入队，
    // CreateProcessW 在 ShellWorker 线程执行。每实例只发一次：旧实现每次焦点
    // 回来都发，连接失败时 worker 每次都 spawn chaosetd（tsf.log 大量
    // "daemon lazy start"、daemon.log 反复 "已有 chaosetd 在运行"）。
    void warmDaemonInBackgroundOnce() {
        if (warmed_) return;
        warmed_ = true;
        postDaemonWarm();
    }
    // ---- 「完全退场」：让用户感觉不到这个输入法的存在 ----
    //
    // 2026-11-06 用户要求：非激活 / 窗口失去焦点时，候选窗、提示条、钩子、定时器
    // 都必须消失，不留任何会在屏幕上或系统派发里冒头的东西。
    //
    // 与「只是 hide()」的区别：
    //   * hide()  只是 ShowWindow(SW_HIDE)，窗口对象还在、类还注册着，一旦哪条
    //     路径漏判又 ShowWindow 就会重新冒出来；TOPMOST 窗口还活着本身就是
    //     用户能感知的存在。
    //   * retire() 直接 destroy() 窗口 + 撤掉所有钩子 + 停掉定时器，下次真正
    //     激活时按需重建（建窗成本远小于「挡住用户鼠标」的代价）。
    void retireUiCompletely() {
        // 1) 候选窗/提示条：销毁而不是隐藏（destroy 内部会把 g_live* 计数归还）。
        candidateWnd_.setActiveGate(false);
        candidateWnd_.destroy();
        toast_.destroy();
        // 2) 所有钩子（全局前台钩子只在激活时挂，这里撤销即回到「零钩子」）。
        unhookForeground();
        // 3) 焦点锚点与联想态作废：它们只对「刚才那个窗口」有意义。
        caretFound_ = false;
        caretFromMouse_ = false;
        clearAssociate();
    }

    // 线程内是否有任一 TIP 真正在用：定时器只在有用例时运行，未激活时零定时器。
    void refreshActiveWatchTimer() {
        if (g_tlTipMsgWnd == nullptr) return;
        bool any = false;
        for (ChaosetTIP* t : g_tlFgTips) {
            if (t != nullptr && t->inputActive_) {
                any = true;
                break;
            }
        }
        if (any) {
            SetTimer(g_tlTipMsgWnd, kActiveWatchTimerId, 500, nullptr);
        } else {
            KillTimer(g_tlTipMsgWnd, kActiveWatchTimerId);
        }
    }

    // 切换到其它输入法（TSF 未调 Deactivate）时的统一收口：托盘隐藏 + 彻底退场。
    void switchToInactiveProfile() {
        inputActive_ = false;
        focused_ = false;
        postActiveReport(false, currentEnglish(), false);
        retireUiCompletely();
        suspendShiftHoldOnFocusLost();
        refreshActiveWatchTimer();
        static volatile LONG logged = 0;
        if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
            diagPrintf(L"tsf.log", "profile switched: input inactive pid=%lu\n",
                       static_cast<unsigned long>(GetCurrentProcessId()));
        }
    }

    bool bailOutIfInactive() {
        if (inputActive_) {
            // 双保险：TSF 在部分宿主里切走输入法后仍把按键下沉给本 TIP 且不调用
            // Deactivate（2026-11-05 实测）。必须每键复核激活档案，否则用户已经
            // 换到别的输入法时我们还会吃键/显示托盘（用户 2026-11-07 的 bug1/2）。
            if (!isActiveTip()) {
                switchToInactiveProfile();
                return true;
            }
            candidateWnd_.setActiveGate(true);
            return false;
        }
        // 非激活：**绝对零动作**（2026-11-07 用户要求：没激活时不要处理任何
        // 东西、不要拦截按键、不要获取鼠标事件）。这里不 retire、不查询档案、
        // 不写盘、不动钩子/候选窗——清理已在 active→inactive 迁移时完成。
        static volatile LONG loggedInactive = 0;
        if (InterlockedCompareExchange(&loggedInactive, 1, 0) == 0 && diagBudget()) {
            diagPrintf(L"tsf.log", "inactive: keys passed through untouched\n");
        }
        return true;
    }

    void applyPendingStaleReset() {
        if (!pendingStaleReset_) return;
        pendingStaleReset_ = false;
        // 用户用数字键确认过的段不能丢：与 OnSetFocus(FALSE) 同语义，先落盘
        //（挂起清理只该丢未确认的拼音，不该丢已确认文本）。
        if (!confirmedPreedit_.empty() && composition_ != nullptr) {
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(compCtx_, all);
        }
        // **必须先结束 TSF 合成本身**（2026-11-05 实机）：只清本地状态的话，
        // 宿主文档里的预编辑文本会原样留着——开始菜单失焦重开后旧拼音还在，
        // 原因就在这里。endComposition 会对合成所属上下文发编辑会话，
        // 把预编辑文本从文档里删掉。
        endComposition();
        resetSession();
        comp_ = chaoset::Comp();
        confirmedPreedit_.clear();
        clearAssociate();
        candidateWnd_.hide();
    }

    // AppContainer 宿主不能 CreateProcess：请求 daemon 代拉起 UI 宿主（IPC ui）。
    // **只入队**（ShellWorker 线程执行）；保留函数名/调用形态供 checkuiparity
    // 静态契约，实际实现见 ensureUiHostByDaemonWorker / session_.ensureUiHost。
    bool ensureUiHostStartedByDaemon(bool settings = false) {
        postEnsureUiHostByDaemon(settings);
        return true;
    }
    static unsigned shiftBit(WPARAM vk) {
        if (vk == VK_LSHIFT) return 1u;
        if (vk == VK_RSHIFT) return 2u;
        return 4u;  // 泛化 VK_SHIFT：多数宿主只投递这个
    }

    // 系统层面是否仍有任一 Shift 物理按下。只在持有按住会话时才查询
    // （唯一调用点 reconcileShiftState 已先判 shiftHeld_），正常打字路径零额外开销。
    // 用来兜住上下文切换/焦点变化造成的 KeyUp 丢失。
    static bool shiftPhysicallyDown() {
        return (GetAsyncKeyState(VK_LSHIFT) & 0x8000) != 0 ||
               (GetAsyncKeyState(VK_RSHIFT) & 0x8000) != 0;
    }

    // 焦点/停用复位。Linux 壳在 focusIn/focusOut 调 shift_.reset()
    // （shims/linux/engine/main.cpp:385-389），Windows 此前完全没有这一步：
    // 按住 Shift 切走窗口时 KeyUp 不会回来，复位是唯一能保证不卡死的时机。
    void resetShiftTracking() {
        shiftHeld_ = 0;
        sinceShift_ = true;  // 复位后首个释放不得触发切换
        shiftHoldSuspended_ = false;
    }

    // 焦点暂时离开（仍在同一进程/同一窗口族）：不丢按住状态，只挂起。
    void suspendShiftHoldOnFocusLost() {
        if (shiftHeld_ != 0 && shiftPhysicallyDown()) {
            shiftHoldSuspended_ = true;
        } else {
            resetShiftTracking();
        }
    }

    // 开始一次「按住会话」：位运算幂等（auto-repeat / 重复投递都不累积）。
    void beginShiftHold(unsigned bit) {
        if ((shiftHeld_ & bit) != 0) return;
        if (shiftHeld_ == 0) {
            sinceShift_ = false;
            shiftHoldSuspended_ = false;
        }
        shiftHeld_ |= bit;
    }

    // 空按判定 + 切换。pic 为事件路径带来的上下文；自愈收口时传 nullptr，
    // 内部再取 focusContext()（必须用 ContextRef 释放）。
    void finishShiftHold(ITfContext* pic) {
        const bool suspended = shiftHoldSuspended_;
        shiftHoldSuspended_ = false;
        // 纯事件驱动：按键只会在本 TIP 有焦点时到达，不需要额外的前台门控。
        if (!sinceShift_ && !suspended) {
            if (pic != nullptr) {
                toggleEnMode(pic);
            } else {
                ContextRef owned(nullptr);
                owned.ctx = focusContext();
                toggleEnMode(owned.ctx);
            }
        }
        sinceShift_ = true;
    }

    // 结束一次「按住会话」：只有最后一次抬起才结算。
    void endShiftHold(unsigned bit, ITfContext* pic = nullptr) {
        if ((shiftHeld_ & bit) == 0) return;
        shiftHeld_ &= ~bit;
        if (shiftHeld_ != 0) return;  // 另一侧还按着，等它抬起
        finishShiftHold(pic);
    }

    // 无条件收口：事件路径留下的按住位（左右/泛化位混用）在物理松开后
    // 由下一次按键统一结算，避免任何一位永远挂着。
    void settleAllShiftHolds() {
        if (shiftHeld_ == 0) return;
        shiftHeld_ = 0;
        finishShiftHold(nullptr);
    }

    // 把当前中英模式写进线程级隔间，供 Windows 输入指示器显示。
    // 语义（微软《predefined compartments》）：非零 = 键盘打开（中文输入）。
    void syncModeCompartment() {
        if (modeCompartment_ == nullptr) return;
        VARIANT var;
        ZeroMemory(&var, sizeof(var));
        var.vt = VT_I4;
        var.lVal = currentEnglish() ? 0 : 1;  // 非零 = 键盘打开（中文）
        const HRESULT hr = modeCompartment_->SetValue(clientId_, &var);
        VARIANT back;
        ZeroMemory(&back, sizeof(back));
        const HRESULT hrGet = modeCompartment_->GetValue(&back);
        // 日志只留失败（2s 限流、异步写盘）：成功路径不再每进程写一条。
        const bool failed = FAILED(hr) || FAILED(hrGet);
        static DWORD lastFailLog = 0;
        const DWORD now = GetTickCount();
        if (failed && diagBudget() && (lastFailLog == 0 || now - lastFailLog >= 2000)) {
            lastFailLog = now;
            diagPrintf(L"tsf.log",
                       "mode compartment failed en=%d set_hr=0x%08lX get_hr=0x%08lX read=%ld\n",
                       currentEnglish() ? 1 : 0, static_cast<unsigned long>(hr),
                       static_cast<unsigned long>(hrGet),
                       static_cast<long>(SUCCEEDED(hrGet) ? back.lVal : -1));
        }
    }

    void toggleEnMode(ITfContext* pic) {
        // 翻转：读当前（可能来自 UI 宿主的权威值）→ 取反 → 写回。
        storeEnglish(!currentEnglish());
        if (diagBudget()) {
            // 诊断补前台信息：Firefox/Chrome 的「安装应用」(PWA) 窗口是独立
            // 进程/窗口，若 fgpid != selfpid 说明本 TIP 并不在拥有焦点的那个
            // 进程里——这是判断「PWA 里切不了」到底卡在哪一层的直接证据。
            const HWND fg = GetForegroundWindow();
            DWORD fgPid = 0;
            if (fg != nullptr) GetWindowThreadProcessId(fg, &fgPid);
            // 合成上下文所属窗口：**只在真有合成时**才有意义（没合成时
            // compCtx_ 为空，之前据此算出的 samewnd 恒为 0，是无效字段）。
            // Firefox 多窗口场景下若合成窗口 != 前台窗口，切换时对旧窗口上下文
            // 做编辑会话就可能把那个窗口顶到前面（2026-11-05 用户反馈）。
            DWORD compPid = 0;
            HWND composeWnd = nullptr;
            if (compCtx_ != nullptr) {
                composeWnd = hostWindowOf(compCtx_);
                if (composeWnd != nullptr) GetWindowThreadProcessId(composeWnd, &compPid);
            }
            const bool pending = isComposing() || !confirmedPreedit_.empty();
            diagPrintf(L"tsf.log",
                       "shift toggle enMode=%d fgpid=%lu selfpid=%lu pending=%d "
                       "comppid=%lu samewnd=%d\n",
                       currentEnglish() ? 1 : 0, static_cast<unsigned long>(fgPid),
                       static_cast<unsigned long>(GetCurrentProcessId()), pending ? 1 : 0,
                       static_cast<unsigned long>(compPid),
                       (!pending || (composeWnd != nullptr && composeWnd == fg)) ? 1 : 0);
        }
        // 模式切换收尾：已确认段先落盘（用户确认过的字不能丢），纯拼音仍按
        // 旧语义丢弃；此前直接 endComposition 会清空 confirmedPreedit_
        // （审计 P1-3）。
        //
        // 收尾按「有没有未上屏内容」分流（2026-11-05 三审）：
        //
        // 切中英本身是纯壳本地状态，唯一需要通知 daemon 的理由是丢弃当前组词。
        // 没有未上屏内容时这次 IPC 纯属空转（同步往返，daemon 忙时可达 250ms），
        // 而且它发生在按键回调里——PWA/独立窗口这类宿主对 TSF 编辑会话更敏感，
        // 少做一次无意义往返就少一类干扰源。
        //
        // 保留 resetComposition 语义：只丢组词、**保留** prev/history，
        // 所以「切模式把刚上屏的词也忘掉」的问题不会回来。
        const bool pending = isComposing() || !confirmedPreedit_.empty();
        if (pending) {
            flushOrCancel(pic);
        } else if (!dpScheme_.empty()) {
            // 双拼例外：引擎可能持有「暂存首键」(dpPending)，壳侧 comp_ 为空
            // 也看不见它，必须让 daemon 清一次，否则下一轮会拿旧首键解码。
            resetComposition();
            comp_ = chaoset::Comp();
        } else {
            comp_ = chaoset::Comp();  // 全拼且无未上屏内容：零 IPC
        }
        candidateWnd_.hide();
        // 模式提示只有一个出口：单例 UI 进程的任务栏指示器。不再有浮动 toast
        // 兜底——那正是用户看到的「输入光标附近 / 鼠标附近也在提示」的来源
        //（2026-11-05 用户反馈：三处同时提示 + 任务栏多个图标）。
        // storeEnglish() 已经上报过，这里不再重复（每次按键一次管道往返太贵）。
    }

    // vk 必须是原始 WPARAM（VK_SHIFT / VK_LSHIFT / VK_RSHIFT），
    // 据此决定置/清哪一位——左右 Shift 同时按住时各自记一位。
    void onShiftEvent(bool release, WPARAM vk, ITfContext* pic) {
        const unsigned bit = shiftBit(vk);
        if (!release) {
            beginShiftHold(bit);
            return;
        }
        if ((shiftHeld_ & bit) != 0) {
            endShiftHold(bit, pic);
            return;
        }
        // 无按下的孤立释放。宿主混用键码时（按下 VK_LSHIFT、释放 VK_SHIFT 或
        // 反之）位置对不上——若 Shift 确已不在物理按下状态就整体结算，
        // 否则会留下一位永远挂着。
        if (shiftHeld_ == 0 || shiftPhysicallyDown()) return;
        settleAllShiftHolds();
    }

    // 结算一次已经松开的 Shift 按住会话。
    //
    // 事件路径（OnTestKeyUp）与按键自愈共用这一个出口，保证两条来源的
    // 判定规则完全一致：只有「期间没按过任何别的键/鼠标」才切换中英。
    void reconcileShiftState() {
        // 只做「物理已松开但状态还挂着」的收口；按住中一律不干预。
        // 旧实现会按焦点门控直接清状态——窗口内焦点抖动时正好把正在按的
        // Shift 吃掉，这是「能打字但 Shift 切不了」的直接原因之一
        //（2026-11-05 修）。
        //
        // 纯事件驱动：不靠定时器，只在**下一次按键**到达时顺手自愈。
        // 若这里真的收口了，说明宿主丢了 Shift 的释放事件——留一条一次性
        // 证据，排障时能直接看出「哪个宿主不投递 KeyUp」，零常驻开销。
        if (shiftHeld_ != 0 && !shiftPhysicallyDown()) {
            static volatile LONG loggedLostKeyUp = 0;
            if (!sinceShift_ && InterlockedCompareExchange(&loggedLostKeyUp, 1, 0) == 0 &&
                diagBudget()) {
                diagPrintf(L"tsf.log", "shift: 宿主丢弃了 Shift 释放事件，已按物理状态收口\n");
            }
            settleAllShiftHolds();
        }
    }

    // 按键 → 产生的 ASCII 字符（US 布局直映，含 Shift 变体），只返回
    // engine.IsPunctKey 字符集内的成员，nullptr 表示「不是标点，放行」。
    //
    // 判定必须是**字符级**而非 VK 级：Shift+; 产生的字符是 ':'、Shift+/
    // 是 '?'——此前纯 VK 直映不看 Shift，导致 Shift+; 出「；」而不是「：」、
    // Shift+, 出「，」而不是「《」，且 ？！：《》"" 等十个中文标点完全不可达
    // （2026-10-27 审查发现；macOS/ibus 壳按字符判定，此处对齐）。
    // Shift 变体不在字符集的键（如 Shift+2 → @、Shift+[ → {）返回 nullptr，
    // 原字符透传给宿主，与 mac 的 passThrough 行为一致。
    static const char* keyPunctChar(WPARAM vk, bool shift) {
        if (shift) {
            switch (vk) {
                case kVkFirstDigit: return "!";          // Shift+1
                case '6': return "^";
                case '9': return "(";
                case '0': return ")";
                case kVkOemSemicolon: return ":";
                case kVkOemComma: return "<";
                case kVkOemPeriod: return ">";
                case kVkOem2: return "?";
                case kVkOem3: return "~";
                case kVkOemQuotes: return "\"";          // 开闭引号配对在 daemon
            }
            return nullptr;
        }
        switch (vk) {
            case kVkOemComma: return ",";
            case kVkOemPeriod: return ".";
            case kVkOemSemicolon: return ";";
            case kVkOemOpenBrackets: return "[";
            case kVkOemBackslash: return "\\";
            case kVkOemCloseBrackets: return "]";
            case kVkOemQuotes: return "'";
        }
        return nullptr;
    }

    // 双拼模式的 ';' 键：US 布局非 Shift 的 ;（VK_OEM_1）。双拼时它不是
    // 标点键而是引擎的组词/解码键（引擎契约：input(";")，dpPending 非 0
    // 时与字母一样成对解码，为 0 时按 punct(";") 语义上屏首选+分号）。
    // Shift+; 产生的 ':' 不是它，仍走标点分支——与 mac 的字符级判定一致。
    static bool isDpSemicolonKey(WPARAM vk, bool shift) {
        return !shift && vk == kVkOemSemicolon;
    }

    // 数字键 → 页内序号（0 起）：主键盘 1-9 与小键盘 1-9 都支持。
    // 返回 -1 表示不是数字选词键。主键盘带 Shift 时产生 !@# 等字符，由调用方
    // 另行判定（小键盘不受 Shift 影响）。
    static int digitIndex(WPARAM vk) {
        if (vk >= kVkFirstDigit && vk <= kVkLastDigit) {
            return static_cast<int>(vk - kVkFirstDigit);
        }
        if (vk >= kVkNumpad1 && vk <= kVkNumpad9) {
            return static_cast<int>(vk - kVkNumpad1);
        }
        return -1;
    }

    // Shift/CapsLock 有效：字母按大写、数字/OEM 键按 Shift 变体处理
    static bool shiftActive() {
        return (GetKeyState(VK_SHIFT) & 0x8000) != 0 || (GetKeyState(VK_CAPITAL) & 0x0001) != 0;
    }

    static bool isModifierKey(WPARAM vk) {
        return vk == VK_SHIFT || vk == VK_LSHIFT || vk == VK_RSHIFT ||
               vk == VK_CONTROL || vk == VK_LCONTROL || vk == VK_RCONTROL ||
               vk == VK_MENU || vk == VK_LMENU || vk == VK_RMENU ||
               vk == VK_LWIN || vk == VK_RWIN;
    }

    // 是否会吃掉该键：修饰键组合（Ctrl/Alt/Win 快捷键）一律放行给宿主系统与应用，
    // 仅在组词中支持 Ctrl+Backspace 快速清空拼音；字母与标点键正常输入；
    // 组词中再吃数字/空格/回车/退格/Esc/翻页。
    bool wouldHandle(WPARAM vk) const {
        if (isModifierKey(vk)) return false;

        const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
        const bool alt  = (GetKeyState(VK_MENU) & 0x8000) != 0;
        const bool win  = ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);

        // 组词中 Backspace/Delete：修饰键映射与 macOS 对齐（2026-10-31 用户要求：
        // Control↔Ctrl、Option↔Alt、Command↔Win）。两者都消费，具体行为在
        // handleKey 里按「Command/Ctrl/Shift → 全部删除；Alt → 按音节回退；
        // 无修饰 → 逐字符回退」处理。非组词态一律放行给宿主。
        if (isComposing() && (vk == kVkBackspace || vk == kVkDelete)) return true;
        // Ctrl+U 整行清空；Ctrl+Shift+U 是宿主快捷键（mac/Linux 都放行），
        // 所以这里必须要求不带 Shift。
        if (ctrl) return isComposing() && vk == 'U' && !shiftActive();
        if (alt || win) return false;

        // 联想模式：数字（有对应项）/空格（选第一个）/回车/Esc/Backspace
        //（收起）由联想分支消费；- / = 收起联想放行；其余按键退出联想后
        // 按正常流程判定（继续往下走）。
        if (!assoc_.candidates.empty()) {
            if (vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
                vk == kVkBackspace) return true;
            // 联想翻页键：PageUp/PageDown 不判 Shift（与 mac/Linux 一致：
            // 联想态 Shift+PageUp/PageDown 同样翻联想页，2026-10-30 审计 A7）。
            if (vk == kVkPrior || vk == kVkNext) return true;
            if (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus)) return true;
            const int digit = digitIndex(vk);
            if (digit >= 0 && (vk >= kVkNumpad1 || !shiftActive())) {
                return assocPage_ * kPageSize + digit <
                       static_cast<int>(assoc_.candidates.size());
            }
        }

        // Shift+空格只在输入法介入时（组词/联想）才预告吞键。
        // 2026-10-29 修复（输入事件审计 F1）：此前无条件 return true，
        // 任何应用里 Shift+Space 都被拦——游戏里 Shift=冲刺/潜行、Space=跳。
        // 注意：联想态的 Shift+空格由上方联想分支先行消费——实际行为是
        // 「选当前页第一个联想词上屏」，**不**切换全半角（三端实测一致；
        // 2026-10-30 按实际行为修正文档与本节注释）；只有组词态才会走到
        // handleKey 的全半角切换分支。
        if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
            return isComposing() || !assoc_.candidates.empty();
        }

        // 只在需要打字时介入（2026-10-29 用户要求）：未组词状态下，先判
        // 该键是否与组词相关——字母（不带 Shift）或标点才有「开始组词/出
        // 中文标点」的可能，其余键（数字/导航/F键/空格等）立即放行，且绝不
        // 触碰连接状态；内容键才允许尝试 daemon 连接（服务不可达仍放行）。
        if (!isComposing()) {
            const bool relevant = (vk >= kVkFirstLetter && vk <= kVkLastLetter && !shiftActive()) ||
                                  keyPunctChar(vk, shiftActive()) != nullptr;
            if (!relevant) return false;
            // 零阻塞（审计 T4）：只在已连接时介入；未连接则入队后台连接并
            // fail-open——本键原样进文档，下一次按键即可正常组词，绝不 Sleep/建进程。
            if (!ensureDaemonFast(const_cast<PipeTransport&>(transport_))) return false;
        }

        // Shift+字母（大写英文，含 CapsLock）：放行给宿主，不进组词
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) return !shiftActive();
        // 翻页键：- / = / [ / ] 不带 Shift（Shift 变体 _ + { } 是字符，
        // 与 mac 字符级判定一致放行）；PageUp/PageDown 无论 Shift 都翻页
        //（mac keyCode/Linux keysym 都不看 Shift，2026-10-31 三端对齐）。
        const bool isPageKey =
            (vk == kVkPrior || vk == kVkNext) ||
            (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus ||
                                vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
        if (isPageKey && isComposing()) return hasCandidates();
        // 双拼模式下 ';' 是引擎的组词/解码键（见 handleKey 的 DP 路由），
        // 同样要在这里预告吞键——TSF 两阶段模型里 OnTestKeyDown 预告 FALSE
        // 时 OnKeyDown 可能根本收不到该键（参照上方字母/标点判定）。
        if (!dpScheme_.empty() && isDpSemicolonKey(vk, shiftActive())) return true;
        if (keyPunctChar(vk, shiftActive()) != nullptr) return true;
        if (!isComposing()) return false;
        // 数字选词只在不带 Shift 且本页确有该序号候选时；Shift+数字产生的
        // !@# 等字符与本页没有的序号都放行（与 handleKey 的判定保持一致）。
        // 小键盘 1-9（VK_NUMPAD1-9）同样可选词，且不受 Shift 影响。
        const int digit = digitIndex(vk);
        return vk == kVkBackspace || vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
               (digit >= 0 && (vk >= kVkNumpad1 || !shiftActive()) &&
                digit < static_cast<int>(comp_.candidates.size()));
    }

    bool handleKey(WPARAM vk, ITfContext* pic) {
        if (isModifierKey(vk)) return false;

        // 英文模式：内容键一律放行。**必须在实际处理点判断**，不能只靠
        // OnTestKeyDown 的预告分支——实测 Chrome / conhost / Windows Terminal
        // 连内容键也走 Key 阶段（Test 阶段收不到修饰键，同理收不到这些键），
        // 预告分支根本不执行，于是「提示切到英文、打字仍出拼音候选」。
        // 实机日志：16:13:42 shift toggle enMode=1 之后 16:13:43 仍在
        // candidate shown（2026-11-05）。
        if (currentEnglish()) {
            clearAssociate();
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);  // 切换瞬间没收干净的残留，这里兜底清理
            }
            return false;  // 放行给宿主
        }

        // 零阻塞（审计 T4）：Key-only 宿主（Chrome/conhost/Terminal 不投递 Test
        // 阶段）在这里补一次非阻塞连接探测；失败时各分支 fail-open（不阻塞、
        // 按键原样进文档）。Test 阶段已连接时这里只是一次分支预测。
        if (!transport_.isConnected()) ensureDaemonFast(transport_);

        // 词组联想分支（对齐 macOS 壳，2026-10-27 接入 Windows）：
        //   数字（有对应项）→ 联想词直接上屏并继续联想；
        //   空格 → 结束联想，空格透传；回车 → 结束联想并消费；
        //   Esc → 收起联想；- / = → 单页无翻页语义，收起放行；
        //   其余按键 → 退出联想走正常流程（字母进组词 / 标点出中文标点 /
        //   Ctrl 等修饰键照旧放行）。
        if (!assoc_.candidates.empty()) {
            const bool shift = shiftActive();
            if (vk == kVkEscape || vk == kVkReturn) {
                clearAssociate();
                return true;
            }
            // 空格：**默认选当前页第一个联想词上屏**（渐进组句闭环，
            // 与 macOS 端一致），不再是透传
            if (vk == kVkSpace) {
                if (!assoc_.candidates.empty()) {
                    const chaoset::Candidate pick = assoc_.candidates[assocPage_ * kPageSize];
                    std::string committed;
                    if (session_.associateSelect(pick.text, splitSyllables(pick.syllables), &committed)) {
                        const std::string sylls = pick.syllables;
                        // 上屏确实写进了文档才继续联想：否则联想窗悬在
                        // 「词没上屏却推下一批」的假状态（2026-10-29 审查）。
                        if (commit(pic, committed)) enterAssociate(sylls);
                        return true;
                    }
                }
                clearAssociate();
                return true;
            }
            // - / =：联想翻页（边界页保持当前页）
            // Backspace：收起联想并消费——透传会误删刚上屏的文字
            if (vk == kVkBackspace) {
                clearAssociate();
                return true;
            }
            // 联想翻页键：- / =、[ / ]、PageUp / PageDown——与组词态翻页
            // 键集合一致（2026-10-29 三端一致性审查）。此前只认 - / =，
            // 其余键落到「其余按键退出联想」：面板收起而按键透传给宿主
            // （文档跟着滚动），与 macOS/Linux 行为各不相同。
            const bool assocPageKey =
                vk == kVkPrior || vk == kVkNext ||
                (!shift && (vk == kVkOemMinus || vk == kVkOemPlus ||
                            vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
            if (assocPageKey) {
                const bool next = vk == kVkOemPlus || vk == kVkOemCloseBrackets ||
                                  vk == kVkNext;
                flipAssociate(next);
                return true;
            }
            const int assocDigit = digitIndex(vk);
            if (assocDigit >= 0 && (vk >= kVkNumpad1 || !shift)) {
                // 全局序号 = 当前页 × 每页条数 + 页内序号（候选窗展示的是切片）
                const int index = assocPage_ * kPageSize + assocDigit;
                if (index < static_cast<int>(assoc_.candidates.size())) {
                    const chaoset::Candidate pick = assoc_.candidates[index];
                    std::string committed;
                    if (session_.associateSelect(pick.text, splitSyllables(pick.syllables), &committed)) {
                        const std::string sylls = pick.syllables;
                        // 上屏确实写进了文档才继续联想：否则联想窗悬在
                        // 「词没上屏却推下一批」的假状态（2026-10-29 审查）。
                        if (commit(pic, committed)) enterAssociate(sylls);
                    } else {
                        clearAssociate();
                    }
                    return true;
                }
                clearAssociate(); // 越界序号：收起联想放行数字
                return false;
            }
            clearAssociate(); // 其余按键退出联想，落到下方正常流程
        }

        const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
        const bool alt  = (GetKeyState(VK_MENU) & 0x8000) != 0;
        const bool win  = ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);

        // 组词中 Backspace/Delete：修饰键映射（2026-10-31 用户要求：
        // Control↔Ctrl、Option↔Alt、Command↔Win）。优先级与 macOS 实现一致：
        // Command/Ctrl/Shift → 全部删除（含未落盘确认段，与 Esc 同语义）；
        // Alt → 按音节回退（engine.backspaceWord）；无修饰 → 下方逐字符回退。
        // 非组词态一律放行给宿主。
        //
        // 注意：Win/Super 组合可能先被系统外壳拦截（TSF 未必收得到），
        // 输入法只在收到按键时按 Command 语义处理——真机以实际到达为准。
        if (isComposing() && (vk == kVkBackspace || vk == kVkDelete)) {
            const bool shift = (GetKeyState(VK_SHIFT) & 0x8000) != 0;
            if (win || ctrl || shift) {
                resetComposition();
                comp_ = chaoset::Comp();
                confirmedPreedit_.clear();
                endComposition();
                return true;
            }
            if (alt) {
                comp_ = session_.backspaceWord();
                if (!comp_.ok) {
                    diagDaemonRequestFailure(transport_, "backspaceWord");
                    flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                    return true;
                }
                syncDpScheme(comp_);
                if (comp_.input.empty()) {
                    if (!confirmedPreedit_.empty()) {
                        // 剩余拼音删空：已确认段立即落盘收尾（与普通 Backspace 同路径）
                        comp_ = chaoset::Comp();
                        const std::string all = confirmedPreedit_;
                        confirmedPreedit_.clear();
                        commit(pic, all);
                    } else {
                        endComposition();
                    }
                } else {
                    render(pic);
                }
                return true;
            }
        }

        // Alt 或 Win 快捷键：先收尾未决组词，然后放行原键给宿主。
        // 收尾走 flushOrCancel 而不是 endComposition：用户已确认的段不能
        // 随快捷键透传消失（与 macOS passThrough → flushConfirmed 同语义，
        // 2026-10-30 审计 P1-3）。
        if (alt || win) {
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            return false;
        }

        // Ctrl 组合键：
        if (ctrl) {
            // Ctrl+U：类终端整行清空（对齐 macOS 壳，组词中消费并全部删除）；
            // Ctrl+Shift+U 是宿主快捷键（mac/Linux 都放行），不在此列。
            if (vk == 'U' && isComposing() && !shiftActive()) {
                resetComposition();
                endComposition();
                comp_ = chaoset::Comp();
                return true;
            }
            // 其余 Ctrl 组合键（如 Ctrl+A/C/V/Z）：若处于组词中，先收尾再放行
            // 原键给宿主——已确认段随透传落盘（用户确认过的字不能丢）
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            return false;
        }

        // 字母：追加组词。Shift+字母（大写英文，含 CapsLock）不进组词：
        // 放行原字符，组词中先清空——此前无条件小写化，Shift+A 会变成 a
        // 混进拼音（mac 壳 isCompositionChar 只认小写，此处对齐）。
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) {
            if (shiftActive()) {
                // 大写英文不进组词：放行原字符，组词先收尾（已确认段随透传落盘）
                if (isComposing() || !confirmedPreedit_.empty()) {
                    flushOrCancel(pic);
                }
                return false;
            }
            std::string ch(1, static_cast<char>('a' + (vk - kVkFirstLetter)));
            comp_ = session_.input(ch, halfWidth_);
            if (!comp_.ok) {
                // daemon 确实不可达：收尾（已确认段落盘，不因 daemon 故障丢字）
                // 并放行原键，保证宿主应用不卡死
                diagDaemonRequestFailure(transport_, "input");
                flushOrCancel(pic);
                return false;
            }
            syncDpScheme(comp_);  // 双拼方案名（';' 路由依据，见 syncDpScheme）
            render(pic);
            // 渲染后仍没有合成对象：当前上下文不接受 TSF 合成（Windows 11
            // 开始菜单搜索框等）。此时必须撤掉本地未决状态并放行原键，
            // 否则就是「键被吃掉、字也没出来」——2026-10-29 实机反馈。
            // OnKeyDown 返回 false 是两阶段按键模型的最终裁决，TSF 会把
            // 原键交还宿主（OnTestKeyDown 的 eaten 只是预告）。
            //
            // 兜底条件必须同时覆盖两类拒绝：合成根本没建出来
            // （composition_ == nullptr），以及 StartComposition「成功」但
            // 预编辑 SetText 被拒（只读上下文在编辑会话里仍会拒绝写入，
            // 此时有合成对象却没有合成文本——TS_ES_READONLY）。两者都
            // 意味着键进不了文档，必须放行。
            if (isComposing() && (composition_ == nullptr || !preeditWritten_)) {
                return bailOutNoComposition();
            }
            return true;
        }

        // 候选翻页：- / =、[ / ]、PageUp / PageDown（组词中且有候选时消费；
        // 空组词放行原键/出标点）。- / = / [ / ] 的 Shift 变体是字符不翻页；
        // PageUp/PageDown 无论 Shift 都翻页（对齐 mac/Linux）。
        const bool isPageKey =
            (vk == kVkPrior || vk == kVkNext) ||
            (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus ||
                                vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
        if (isPageKey && isComposing() && hasCandidates()) {
            bool isPrev = (vk == kVkOemMinus || vk == kVkOemOpenBrackets || vk == kVkPrior);
            comp_ = isPrev ? session_.prevPage() : session_.nextPage();
            if (!comp_.ok) {
                diagDaemonRequestFailure(transport_, "page");
                flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                return true;
            }
            syncDpScheme(comp_);
            render(pic);
            return true;
        }

        // 双拼模式的 ';'（配合引擎契约）：双拼时 ';' 不是标点键，而是引擎的
        // 组词/解码键——dpPending 非 0 时与字母一样组成 pair 解码，为 0 时
        // 按 punct(";") 语义（组词中先上屏首选+标点）。必须路由到 input 而
        // 不是 punct，引擎才能在双拼键位状态机里看到它。Shift+; 产生的
        // ':' 不在此列（仍走下方标点分支），与 mac 的字符级判定一致。
        if (!dpScheme_.empty() && isDpSemicolonKey(vk, shiftActive())) {
            comp_ = session_.input(";", halfWidth_);
            if (!comp_.ok) {
                diagDaemonRequestFailure(transport_, "input;");
                flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                return false;       // 绝不吞键：daemon 不可达时让 ; 原样进文档
            }
            syncDpScheme(comp_);
            if (!comp_.committed.empty()) {
                // 引擎已把「上屏文本」（组词中首选 + 分号 / 空组词的分号）
                // 放进 committed：壳侧已确认段必须先拼接再一次性落盘——
                // 用户确认过的字不能丢（与 mac 的 confirmedText + committed
                // 同语义，2026-10-30 三端接口契约）。
                const std::string all = confirmedPreedit_ + comp_.committed;
                confirmedPreedit_.clear();
                commit(pic, all);
                return true;
            }
            render(pic);
            return true;
        }

        // 标点键（US 布局字符级映射，含 Shift 变体）：组词中先上屏首选再出
        // 中文标点，空组词直接出中文标点；映射规则在 daemon 侧统一完成。
        if (const char* key = keyPunctChar(vk, shiftActive())) {
            if (vk == kVkOemQuotes && isComposing()) {
                // 组词中的撇号是音节分隔符，走组词流程
                comp_ = session_.input("'", halfWidth_);
                if (!comp_.ok) {
                    diagDaemonRequestFailure(transport_, "input'");
                    flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                    return false;
                }
                syncDpScheme(comp_);
                render(pic);
                return true;
            }
            const bool composing = isComposing();
            std::string committed;
            if (session_.punct(key, halfWidth_, &committed)) {
                // 标点收尾：已确认段 + 首选候选 + 标点一次落盘
                //（上屏失败（上下文不可插入文本）且没有未决拼音时放行原键，
                // 避免中文标点凭空消失；有未决拼音时仍需消费，保持组词状态机完整）
                const std::string all = confirmedPreedit_ + committed;
                confirmedPreedit_.clear();
                if (commit(pic, all) || composing) return true;
                return false;
            }
            // daemon 不可达：收尾（已确认段落盘）后放行原键，不卡宿主应用
            if (composing || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            return false;
        }

        // Shift+空格：切换全角/半角标点模式。放在 isComposing 守卫**之前**：
        // 用户常在开始打字前先切模式，此前放在守卫之后导致空组词时永远
        // 切不动（mac 壳在组词判断之前处理，此处对齐；wouldHandle 已对
        // Shift+空格无条件返回 TRUE，两条路径此前互相矛盾）。
        // 联想态到不了这里：上方联想分支已把空格当「选第一个联想词」消费
        //（三端实测一致，2026-10-30 文档修正），因此本分支实际只处理组词态。
        if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
            // 无组词/无联想时放行给宿主：输入法没有理由拦截应用自己的
            // Shift+Space（2026-10-29 输入事件审计 F1）。
            if (!isComposing() && assoc_.candidates.empty()) return false;
            // 全/半角开关归壳本地持有（2026-10-29 起）：快捷键切换不再与
            // daemon 往返，状态随 punct 请求下发（daemon 只按状态产出文本）
            halfWidth_ = !halfWidth_;
            showToast(halfWidth_ ? L"半角标点" : L"全角标点");
            return true;
        }

        if (!isComposing()) return false;

        // 普通 Backspace / ForwardDelete（无修饰）：逐字符回退。
        // Delete 与 Backspace 同语义（对齐 macOS 组词中两者都删拼音）；
        // 带修饰的组合已在上面按映射处理。
        if (vk == kVkBackspace || vk == kVkDelete) {
            comp_ = session_.backspace();
            if (!comp_.ok) {
                diagDaemonRequestFailure(transport_, "backspace");
                flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                return true;
            }
            syncDpScheme(comp_);
            if (comp_.input.empty()) {
                if (!confirmedPreedit_.empty()) {
                    // 剩余拼音删空：已确认段立即落盘收尾——不能继续挂在
                    // 合成里变成「无拼音的孤合成」（对齐 macOS 壳）
                    comp_ = chaoset::Comp();
                    const std::string all = confirmedPreedit_;
                    confirmedPreedit_.clear();
                    commit(pic, all);
                } else {
                    endComposition();
                }
            }
            else render(pic);
            return true;
        }

        if (vk == kVkEscape) {
            // Esc：直接全部删除（清空全部拼音）。含未落盘的已确认段一并取消
            //——与 macOS 壳/系统拼音语义一致：合成内已转换但未上屏的内容
            // 随整条组词一起丢弃。
            resetComposition();
            comp_ = chaoset::Comp();
            confirmedPreedit_.clear();
            endComposition();
            return true;
        }

        // 数字选词：只认不带 Shift 的 1-9，且按**本页实际候选数**判定——
        // 页面容量是 6，越界数字不消费、落到末尾兜底（清组词后放行原键）。
        // 此前 select 失败仍 return true：数字凭空消失，既不选词也不进文档
        // （mac 壳 2026-10-26 已修，此处对齐）。带 Shift 的数字（!@# 等字符）
        // 不属于选词，同样落到末尾放行。
        const int composeDigit = digitIndex(vk);
        if (composeDigit >= 0 && (vk >= kVkNumpad1 || !shiftActive())) {
            int index = composeDigit;
            if (index < static_cast<int>(comp_.candidates.size())) {
                // 上屏候选的音节串必须在 selectPrefix 之前取：成功后
                // comp_ 会被替换成剩余组词态（或清空），候选表丢了。
                const std::string pickedSyllables = comp_.candidates[index].syllables;
                // 渐进上屏：只消耗候选音节对应的拼音前缀，剩余回灌继续组词
                //（2026-10-29 用户反馈「选一个候选清空整个拼音」不合适）。
                // 单合成模型（见 confirmedPreedit_ 注释）：确认段并入当前
                // 合成，不再中途结束/重建合成——部分 App 对合成重建的选区
                // 记账有缺陷，会把刚上屏的段覆盖丢（用户实测选「今」丢字）。
                chaoset::Comp rest;
                if (session_.selectPrefix(index, &rest) && !rest.committed.empty()) {
                    syncDpScheme(rest);
                    confirmedPreedit_ += rest.committed;
                    comp_ = rest;
                    if (!comp_.input.empty()) {
                        render(pic);
                    } else {
                        // 剩余耗尽：整串一次落盘，与整句上屏同收尾（进入联想）。
                        // 音节基准传候选自身的撇号音节串（2026-10-29 修复：
                        // 不能把上屏词文本当音节传，单字词会切错位）。
                        comp_ = chaoset::Comp();
                        const std::string all = confirmedPreedit_;
                        confirmedPreedit_.clear();
                        commit(pic, all);
                        enterAssociate(pickedSyllables);
                    }
                } else {
                    // daemon 不可达/越界：回退整句上屏（现状语义）
                    std::string committed;
                    if (session_.select(index, &committed)) {
                        const std::string all = confirmedPreedit_ + committed;
                        confirmedPreedit_.clear();
                        commit(pic, all);
                    }
                }
                return true;
            }
        }

        if (vk == kVkReturn && isComposing()) {
            // 回车语义：已确认段按确认文本、剩余拼音按原始串一起上屏
            //（与 macOS 壳一致：已转换部分保留转换结果）
            std::string raw = confirmedPreedit_ + comp_.input;
            confirmedPreedit_.clear();
            resetComposition();
            commit(pic, raw);
            return true;
        }

        // 空格：组词中一律消费（用户意图永远是「结束组词」，不应有裸空格
        // 漏进文档）——有候选选首选；残缺组词顶屏原始拼音，与回车等效
        // （此前要求 hasCandidates，无候选时空格透传进文档，mac 2026-10-25
        // 实机反馈的同族问题）。Shift+空格已在前面的全半角分支处理。
        if (vk == kVkSpace) {
            if (hasCandidates()) {
                // 同数字选词：音节奏必须在 selectPrefix 之前取（见上）
                const std::string pickedSyllables = comp_.candidates[0].syllables;
                chaoset::Comp rest;
                if (session_.selectPrefix(0, &rest) && !rest.committed.empty()) {
                    syncDpScheme(rest);
                    // 单合成渐进（见 confirmedPreedit_ 注释，与数字选词一致）
                    confirmedPreedit_ += rest.committed;
                    comp_ = rest;
                    if (!comp_.input.empty()) {
                        render(pic);
                    } else {
                        comp_ = chaoset::Comp();
                        const std::string all = confirmedPreedit_;
                        confirmedPreedit_.clear();
                        commit(pic, all);
                        enterAssociate(pickedSyllables);
                    }
                    return true;
                }
                std::string committed;
                if (session_.select(0, &committed)) {
                    const std::string all = confirmedPreedit_ + committed;
                    confirmedPreedit_.clear();
                    commit(pic, all);
                }
                return true;
            }
            // 残缺组词顶屏：已确认段按确认文本、剩余拼音按原始串一起上屏
            //（与上方回车分支一致：用户确认过的字不能丢，2026-10-30
            // 审计 P1-3）。先取出拼接再清缓冲，顺序与 flush 型收尾一致。
            std::string raw = confirmedPreedit_ + comp_.input;
            confirmedPreedit_.clear();
            resetComposition();
            commit(pic, raw);
            return true;
        }

        // 不可处理的按键（方向键 / F 键 / 数字 0 / Shift+数字的 @# 等字符）：
        // 组词中先收尾再放行，避免预编辑与实文脱节（对齐 macOS 壳 passThrough；
        // 已确认段随透传落盘，用户确认过的字不能丢）
        if (isComposing() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        return false;
    }

    ITfContext* focusContext() {
        if (tm_ == nullptr) return nullptr;
        ITfDocumentMgr* doc = nullptr;
        if (FAILED(tm_->GetFocus(&doc)) || doc == nullptr) return nullptr;
        ITfContext* ctx = nullptr;
        doc->GetBase(&ctx);
        doc->Release();
        return ctx;  // 调用方必须用 ContextRef 释放
    }

    // focusContext() 返回的引用计数上下文：析构时 Release。此前大多数调用点
    // 直接丢弃返回值，每次鼠标选词/翻页都泄漏一个 ITfContext（2026-10-27
    // 审查发现，本次一并修正）。
    struct ContextRef {
        explicit ContextRef(ITfContext* c) : ctx(c) {}
        ~ContextRef() {
            if (ctx != nullptr) ctx->Release();
        }
        ContextRef(const ContextRef&) = delete;
        ContextRef& operator=(const ContextRef&) = delete;
        ITfContext* get() const { return ctx; }
        ITfContext* ctx;
    };

    // 上下文能力探测：只做 QI，不改文档、不产生副作用，可安全用于
    // OnTestKeyDown。Windows 11 开始菜单等 XAML 宿主可能不提供合成接口。
    static bool canInsert(ITfContext* ctx) {
        if (ctx == nullptr) return false;
        ITfInsertAtSelection* insert = nullptr;
        if (FAILED(ctx->QueryInterface(IID_ITfInsertAtSelection,
                                       reinterpret_cast<void**>(&insert))) ||
            insert == nullptr) {
            return false;
        }
        insert->Release();
        return true;
    }

    static bool canCompose(ITfContext* ctx) {
        if (!canInsert(ctx)) return false;
        ITfContextComposition* cc = nullptr;
        if (FAILED(ctx->QueryInterface(IID_ITfContextComposition,
                                       reinterpret_cast<void**>(&cc))) ||
            cc == nullptr) {
            return false;
        }
        cc->Release();
        return true;
    }

    // 合成所属上下文（render/applyPreedit 成功建立 composition_ 时记录）。
    // 生命周期与 composition_ 一致：建立时记下，OnCompositionTerminated /
    // commit / endComposition 清空。此后对合成的一切清理都必须对它做，
    // 不能再用 focusContext() 现查——焦点可能已经切到别的文档。
    ITfContext* compCtx_ = nullptr;

    // composition_ 所属上下文的清理 cookie：同步编辑会话成功后写入
    // （RequestEditSession 返回前 DoEditSession 已执行完）。endComposition
    // 在会话外被调用时无法拿 cookie，TF_ES_SYNC 失败（TS_E_SYNCHRONOUS）
    // 时保留上一次成功值比用 0 强——0 一定失败，旧值多半仍有效。
    TfEditCookie compEditCookie_ = 0;

    // canStartComposition 按上下文缓存：同一上下文反复按键时避免每键
    // GetStatus + 双 QueryInterface（不可合成宿主此前每键三次 COM 调用）。
    // 持有引用保证指针不悬垂；合成建立/上下文切换由指针比较自然失效。
    ITfContext* probeCtx_ = nullptr;
    bool probeCan_ = false;

    // StartComposition/InsertTextAtSelection 语义上要求可写文档：只读上下文
    // 上调用属于 TS_E_READONLY，且部分宿主对只读文档的回调和出参处理不可靠。
    // 探测顺序（与 SampleIME 一致）：GetStatus 查 TF_SD_READONLY → QueryInterface
    // ITfInsertAtSelection → ITfContextComposition。全程零文档副作用，可安全
    // 用于 OnTestKeyDown 预检（2026-10-29 实机反馈：Windows 11 开始菜单
    // 搜索框等宿主的上下文需要放行）。
    static bool canStartComposition(ITfContext* ctx) {
        if (ctx == nullptr) return false;
        TF_STATUS st{};
        if (FAILED(ctx->GetStatus(&st))) return false;
        if (st.dwStaticFlags & TF_SD_READONLY) return false;
        return canCompose(ctx);  // canInsert + ITfContextComposition
    }

    // 「该键现在确实处理不了」→ 撤掉引擎未决状态并放行原键。渲染后仍无
    // 合成对象（接口探测放过了但宿主仍拒绝建合成）即属此类：键必须还给
    // 宿主，否则就是「键被吃掉、字也没出来」（2026-10-29 实机反馈）。
    // OnKeyDown 返回 false 是两阶段按键模型的最终裁决，TSF 会把原键交还。
    bool bailOutNoComposition() {
        if (diagBudget()) {
            char host[260];
            diagHostName(host, static_cast<int>(sizeof(host)));
            diagPrintf(L"tsf.log", "no composition after render host=%s preedit_len=%lu\n",
                       host, static_cast<unsigned long>(comp_.preedit.size()));
        }
        resetComposition();
        comp_ = chaoset::Comp();
        confirmedPreedit_.clear(); // 上下文已不可写：缓冲一并放弃，绝不串进下轮组词
        candidateWnd_.hide();
        // 走到这里时合成对象必然已不存在（未建出或 SetText 失败已当场结束），
        // 上下文记录一并清掉，避免过期引用跨越焦点切换存活。
        releaseCompositionContext();
        return false;
    }

    // 把引擎候选状态渲染为预编辑合成与候选悬浮窗
    void render(ITfContext* pic) {
        g_lastOp = kOpRender;
        // 防御：非激活时绝不渲染任何 UI（候选窗/预编辑都不该出现在别人的应用里）。
        // 按键入口已有 bailOutIfInactive 闸门，这里再兜一层——render 还有别的
        // 调用点（联想、翻页、候选删除后的刷新），不能指望每个都先过闸门。
        // 用 inputActive_ 而不是 isActiveTip：后者在后台预激活实例仍可能为 true。
        if (!inputActive_) {
            candidateWnd_.hide();
            toast_.hide();
            return;
        }
        ContextRef owned(nullptr);
        ITfContext* ctx = pic;
        if (ctx == nullptr) {
            owned.ctx = focusContext();
            ctx = owned.ctx;
        }
        if (ctx == nullptr) return;
        // 记录合成所属上下文：此后所有对 composition_ 的清理必须对它做——
        // 此前一律 focusContext()，焦点已切走时拿到的是新文档的上下文，
        // 用它的 cookie 清旧合成必然失败，拼音文本永久残留（2026-10-29 审查）。
        if (compCtx_ != ctx) {
            if (compCtx_ != nullptr) compCtx_->Release();
            compCtx_ = ctx;
            ctx->AddRef();
        }
        caretFound_ = false;
        caretFromMouse_ = false;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyPreedit(c, ec);
        });
        const HRESULT hr = es.run(tm_, clientId_);
        es.Release();
        if (FAILED(hr)) diagTsfFail("RequestEditSession(render)", hr);

        // 缓存承载本合成的宿主窗口（供前台事件回调做无 COM 的比较）。
        composeHostWnd_ = contextHostWindow(ctx);

        if (comp_.candidates.empty()) {
            candidateWnd_.hide();
            return;
        }

        if (!caretFound_ || (caretPos_.x == 0 && caretPos_.y == 0)) {
            // 锚点兜底链：真实 GDI 光标 → 鼠标位置。rcCaret 只在 hwndCaret
            // 非空时有意义；浏览器等自绘应用没有 GDI 光标，借 hwndFocus 的
            // rcCaret 只会拿到 (0,0)，候选窗飞到屏幕左上角（2026-10-29 实机
            // 反馈）——该分支已删，直接退回鼠标位置（用户点哪儿鼠标在哪儿）。
            GUITHREADINFO gti{};
            gti.cbSize = sizeof(gti);
            if (GetGUIThreadInfo(0, &gti) && gti.hwndCaret != nullptr &&
                (gti.rcCaret.right > gti.rcCaret.left || gti.rcCaret.bottom > gti.rcCaret.top)) {
                POINT pt{gti.rcCaret.left, gti.rcCaret.bottom};
                ClientToScreen(gti.hwndCaret, &pt);
                caretPos_ = pt;
                caretLineHeight_ = (gti.rcCaret.bottom > gti.rcCaret.top) ? (gti.rcCaret.bottom - gti.rcCaret.top) : 20;
                caretFound_ = true;
                caretFromMouse_ = false;
            } else {
                // 鼠标兜底：**锚点要下移一行**。
                //
                // 鼠标通常正停在用户正在输入的那一行上，而候选窗是从锚点
                // 再往下 6pt 起画的——直接用鼠标坐标会把候选窗画在输入行
                // 上面，正好挡住正在打的字（Firefox 装到任务栏后的独立窗口
                // 实测「候选框遮挡输入」，2026-11-05）。下移一行后候选窗
                // 落在输入行下方，与有真实光标时的观感一致。
                GetCursorPos(&caretPos_);
                caretLineHeight_ = 20;
                caretPos_.y += caretLineHeight_;
                caretFromMouse_ = true;
            }
        }
        // 候选窗 owner = 宿主窗口（2026-10-30 开始菜单排障）。
        // ITfContextView::GetWnd 返回承载本上下文的宿主 HWND；拿不到时退回
        // 前台窗口。owner 关系让候选窗恒在其宿主之上，可跨 Windows 11
        // 开始菜单/搜索的沉浸式 z 序 band——无主 topmost 窗口会被压住。
        // owner 用**当前前台窗口**（且限定本进程），不要用 hostWindowOf(ctx)——
        // 后者可能是另一个窗口的上下文，带 owner 显示时会把它抬到前面，
        // 表现为「候选窗一出现就把别的窗口顶上来」（2026-11-05 用户反馈）。
        candidateWnd_.setOwner(popupOwner());
        candidateWnd_.show(g_hInst, caretPos_, caretLineHeight_, comp_, this);
    }

    // 取上下文宿主窗口（**只在 TSF 线程调用**）：与 hostWindowOf 的区别是
    // 不回退到前台窗口，供前台事件回调缓存后做纯 Win32 比较。
    static HWND contextHostWindow(ITfContext* ctx) {
        if (ctx == nullptr) return nullptr;
        ITfContextView* view = nullptr;
        if (FAILED(ctx->GetActiveView(&view)) || view == nullptr) return nullptr;
        HWND wnd = nullptr;
        const HRESULT hr = view->GetWnd(&wnd);
        view->Release();
        if (FAILED(hr) || wnd == nullptr || !IsWindow(wnd)) return nullptr;
        return wnd;
    }

    // 取上下文宿主窗口：优先 ITfContextView::GetWnd（TSF 标准途径，
    // 直接给出承载该文档的 HWND），失败退回前台窗口。
    static HWND hostWindowOf(ITfContext* ctx) {
        if (ctx != nullptr) {
            ITfContextView* view = nullptr;
            if (SUCCEEDED(ctx->GetActiveView(&view)) && view != nullptr) {
                HWND wnd = nullptr;
                const HRESULT hr = view->GetWnd(&wnd);
                view->Release();
                if (SUCCEEDED(hr) && wnd != nullptr && IsWindow(wnd)) return wnd;
            }
        }
        return GetForegroundWindow();
    }

    // 用合成范围更新候选窗锚点；View 不可用或拿到零矩形时保留旧位置——
    // Chromium 系浏览器对刚建立的合成文本还没完成布局，GetTextExt 会返回
    // S_OK 但矩形全零（2026-10-29 实机反馈：候选窗因此飞到屏幕左上角），
    // 必须按失败处理，等下一次渲染布局就绪后自然拿到真实位置。
    void updateCaretFromRange(ITfContext* ctx, TfEditCookie ec, ITfRange* range) {
        ITfContextView* view = nullptr;
        if (FAILED(ctx->GetActiveView(&view)) || view == nullptr) return;
        RECT rc{};
        BOOL fClipped = FALSE;
        if (SUCCEEDED(view->GetTextExt(ec, range, &rc, &fClipped)) &&
            (rc.right > rc.left || rc.bottom > rc.top)) {
            caretPos_.x = rc.left;
            caretPos_.y = rc.bottom;
            caretLineHeight_ = (rc.bottom > rc.top) ? (rc.bottom - rc.top) : 20;
            caretFound_ = true;
            caretFromMouse_ = false;
        }
        view->Release();
    }

    void applyPreedit(ITfContext* ctx, TfEditCookie ec) {
        g_lastOp = kOpPreedit;
        // 预编辑 = 已确认段（渐进上屏的已选字）+ 剩余拼音：二者同属**同一条
        // 合成**，整条组词结束才落盘（见 confirmedPreedit_ 注释）。壳对文档
        // 只做两类操作——同一合成内 SetText 更新（与逐键输入同原语）与
        // 落盘提交（见 applyCommit），不存在「中途结束再重建合成」的易碎操作。
        const std::string combinedPreedit = confirmedPreedit_ + comp_.preedit;
        // 「预编辑是否真的写进了文档」：字符串为空视作已写（无需合成）；
        // SetText 失败时保持 false，字母分支据此判定该上下文不可用并放行。
        preeditWritten_ = combinedPreedit.empty();
        const std::wstring wide = toWide(combinedPreedit);
        if (composition_ != nullptr) {
            ITfRange* range = nullptr;
            const HRESULT hrRange = composition_->GetRange(&range);
            if (FAILED(hrRange) || range == nullptr) {
                diagTsfFail("ITfComposition::GetRange(preedit)",
                            FAILED(hrRange) ? hrRange : E_POINTER);
                composition_->Release();
                composition_ = nullptr;
                return;
            }
            // 先按上一轮布局查光标再改文本：Firefox(Gecko) 对「本次会话刚
            // 修改过的范围」布局未刷新，SetText 之后再查会失败或拿到旧值；
            // 改文本前合成起点不变、文档布局有效，这一查才是可靠锚点
            // （Chrome 同步布局，两次都能成功，后一次覆盖前一次）。
            updateCaretFromRange(ctx, ec, range);
            const HRESULT hrText =
                range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
            if (FAILED(hrText)) {
                diagTsfFail("ITfRange::SetText(preedit)", hrText);
                // 写不进（只读上下文 / 会话已被终止）：合成已无意义。
                // EndComposition 交还引用，本地所有权一并清空——保留一个
                // 写不进字的合成只会让后续兜底永远认为「合成还在」。
                preeditWritten_ = false;
                composition_->EndComposition(ec);
                composition_->Release();
                composition_ = nullptr;
                updateCaretFromRange(ctx, ec, range);
                range->Release();
                return;
            }
            preeditWritten_ = true;
            updateCaretFromRange(ctx, ec, range);
            range->Release();
            return;
        }
        ITfInsertAtSelection* insert = nullptr;
        HRESULT hr = ctx->QueryInterface(IID_ITfInsertAtSelection, reinterpret_cast<void**>(&insert));
        if (FAILED(hr) || insert == nullptr) {
            diagTsfFail("ITfInsertAtSelection(preedit)", FAILED(hr) ? hr : E_NOINTERFACE);
            return;
        }
        ITfRange* range = nullptr;
        // QUERYONLY：只取插入点位置的范围，不插文本
        hr = insert->InsertTextAtSelection(ec, TF_IAS_QUERYONLY, nullptr, 0, &range);
        if (FAILED(hr) || range == nullptr) {
            diagTsfFail("InsertTextAtSelection(QUERYONLY)", FAILED(hr) ? hr : E_POINTER);
            insert->Release();
            return;
        }
        // 首次建合成前先锚定插入点：此处文档已布局完成（Firefox 对新合成
        // 文本的布局要等会话结束后才刷新，合成后再查会失败），即便首键
        // 也能贴住光标；合成建立后再查一次，成功则覆盖为更精确的值。
        updateCaretFromRange(ctx, ec, range);
        ITfContextComposition* cc = nullptr;
        hr = ctx->QueryInterface(IID_ITfContextComposition, reinterpret_cast<void**>(&cc));
        if (FAILED(hr) || cc == nullptr) {
            diagTsfFail("ITfContextComposition", FAILED(hr) ? hr : E_NOINTERFACE);
        } else {
            ITfComposition* comp = nullptr;
            hr = cc->StartComposition(ec, range, compositionSink(), &comp);
            if (FAILED(hr) || comp == nullptr) {
                diagTsfFail("StartComposition", FAILED(hr) ? hr : E_POINTER);
                if (comp != nullptr) comp->Release();
            } else {
                composition_ = comp;
                const HRESULT hrText =
                    range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                if (FAILED(hrText)) {
                    diagTsfFail("ITfRange::SetText(preedit)", hrText);
                    // StartComposition 成功但文本写不进：与上面同理，
                    // 必须立刻结束合成并清空所有权，否则字母分支的
                    // 「渲染后仍无合成」兜底永远不触发，键照样被吃。
                    preeditWritten_ = false;
                    composition_->EndComposition(ec);
                    composition_->Release();
                    composition_ = nullptr;
                } else {
                    preeditWritten_ = true;
                }
            }
            cc->Release();
        }
        // 合成建立失败也尝试取一次光标位置：候选窗仍能贴住插入点显示
        updateCaretFromRange(ctx, ec, range);
        range->Release();
        insert->Release();
    }

    // 上屏；返回是否确实写进了文档（调用方据此决定失败时是否放行原键）
    bool commit(ITfContext* pic, const std::string& text) {
        g_lastOp = kOpCommit;
        candidateWnd_.hide();
        ContextRef owned(nullptr);
        ITfContext* ctx = pic;
        if (ctx == nullptr) {
            owned.ctx = focusContext();
            ctx = owned.ctx;
        }
        if (ctx == nullptr) return false;
        committed_ = text;
        commitWritten_ = false;
        EditSession es(ctx, this, [](void* user, ITfContext* c, TfEditCookie ec) {
            static_cast<ChaosetTIP*>(user)->applyCommit(c, ec);
        });
        const HRESULT hr = es.run(tm_, clientId_);
        es.Release();
        if (FAILED(hr)) diagTsfFail("RequestEditSession(commit)", hr);
        comp_ = chaoset::Comp();
        // 提交即整条组词结束：确认段缓冲必须清空（调用方已先行取出拼接，
        // 这里幂等兜底，防止异常路径把旧段串进下一轮组词的预编辑）。
        confirmedPreedit_.clear();
        return SUCCEEDED(hr) && commitWritten_;
    }

    void applyCommit(ITfContext* ctx, TfEditCookie ec) {
        g_lastOp = kOpCommit;
        commitWritten_ = false;
        if (committed_.empty()) return;
        const std::wstring wide = toWide(committed_);

        // 先摘成员再用局部指针：EndComposition 之后成员值不再可信，且
        // 「我方结束」与「外部终止」两条路径的引用所有权必须唯一
        // （见 OnCompositionTerminated 的注释）。
        ITfComposition* comp = composition_;
        composition_ = nullptr;
        if (comp != nullptr) {
            ITfRange* range = nullptr;
            if (SUCCEEDED(comp->GetRange(&range)) && range != nullptr) {
                const HRESULT hrText =
                    range->SetText(ec, 0, wide.c_str(), static_cast<LONG>(wide.size()));
                if (FAILED(hrText)) diagTsfFail("ITfRange::SetText(commit)", hrText);
                else commitWritten_ = true;
                range->Release();
            }
            const HRESULT hrEnd = comp->EndComposition(ec);
            if (FAILED(hrEnd)) diagTsfFail("ITfComposition::EndComposition(commit)", hrEnd);
            comp->Release();
            releaseCompositionContext();
        } else {
            // 无合成（直接打标点 / 合成被外部终止）：必须自己插入文本。
            // 此前这里什么都不做，标点会凭空消失。
            ITfInsertAtSelection* insert = nullptr;
            HRESULT hr = ctx->QueryInterface(IID_ITfInsertAtSelection,
                                             reinterpret_cast<void**>(&insert));
            if (FAILED(hr) || insert == nullptr) {
                diagTsfFail("ITfInsertAtSelection(commit)", FAILED(hr) ? hr : E_NOINTERFACE);
            } else {
                ITfRange* range = nullptr;
                hr = insert->InsertTextAtSelection(ec, 0, wide.c_str(),
                                                   static_cast<LONG>(wide.size()), &range);
                if (FAILED(hr)) {
                    diagTsfFail("InsertTextAtSelection(commit)", hr);
                } else {
                    commitWritten_ = true;
                    if (range != nullptr) range->Release();
                }
                insert->Release();
            }
        }
        committed_.clear();
    }

    // 缓存引擎的双拼方案名（IPC Composition.shuangpin）。引擎契约：启用
    // 双拼时**每次 Compose 都回填**方案名；方案在 daemon 生命周期内固定，
    // 壳只需要任意一次响应就能判定 ';' 路由（见 handleKey），因此只在
    // 非空时覆盖，空值不清除（兼容未实现该字段的旧 daemon）。
    void syncDpScheme(const chaoset::Comp& comp) {
        if (!comp.shuangpin.empty()) dpScheme_ = comp.shuangpin;
    }

    // 结束组词并重置引擎会话。reset 响应同样是 Composition（也带 shuangpin），
    // 这里统一缓存方案名——所有调用点替换 session_.reset() 以免漏掉。
    void resetSession() { syncDpScheme(session_.reset()); }

    // 只丢弃当前组词、保留学习上下文（prev/history）的收尾。
    //
    // 用途判定（2026-11-05）：**用户还会继续打字**的场景一律走这里——
    // 切中英模式、退格删空、Esc 取消、回车/空格顶屏、宿主拒建合成兜底、
    // 传给宿主后清本地状态。这些情况下用户只是「打断了这一轮组词」，
    // 上一轮上屏的 prev/bigram 依然有效；用 reset 会让引擎忘记刚打的字，
    // 紧接着的候选失去上下文加成（实测：学过「我们→时」后输入 shi，
    // 带 prev 首候选是「时」，清掉后变成「是」）。
    //
    // reset 只保留给**会话真的结束**的场景：失焦（OnSetFocus(FALSE)）、
    // TIP 停用（Deactivate）、合成被宿主终止（OnCompositionTerminated）。
    void resetComposition() { syncDpScheme(session_.resetComposition()); }

    // 透传/故障型收尾（对齐 macOS 壳 passThrough）：已确认段先落盘——用户
    // 确认过的字不能随透传（Ctrl+C 等）或 daemon 故障消失；纯拼音维持
    // 「清空」旧语义。显式取消（Esc / 全部删除）不走这里，直接 endComposition。
    //
    // 用 resetComposition（不是 reset）：这条路径的全部调用方（切中英、
    // 方向键/功能键透传、daemon 故障降级、翻页失败）都意味着**用户还会
    // 继续输入**，不该把刚上屏的 prev/bigram 一起忘掉（2026-11-05）。
    void flushOrCancel(ITfContext* pic) {
        resetComposition();
        comp_ = chaoset::Comp();
        if (!confirmedPreedit_.empty()) {
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(pic, all);
        } else {
            endComposition();
        }
    }

    // 取消/清理当前合成：必须经真实编辑会话拿到有效 cookie。旧实现用
    // cookie 0 在编辑会话外调用 SetText/EndComposition，TSF 只会返回失败：
    // 预编辑文本留在文档里、合成对象也泄漏（2026-10-29 审查）。
    void endComposition() {
        candidateWnd_.hide();
        // 取消整条组词：未落盘的已确认段一并丢弃（与 macOS 壳 Esc 语义一致；
        // 幂等兜底——落盘型收尾走 commit() 且已先行取出拼接）。
        confirmedPreedit_.clear();
        ITfComposition* comp = composition_;
        if (comp == nullptr) return;
        g_lastOp = kOpEndComposition;
        composition_ = nullptr;  // 先摘成员：结束路径的引用所有权唯一（见 sink）
        // 必须对合成所属的上下文做（render 时记录），不能用 focusContext()：
        // 焦点切走后现查到的是新文档的上下文，RequestEditSession 会失败或
        // 拿到无效 cookie，拼音文本永久残留（2026-10-29 审查）。
        // 注意所有权：compCtx_ 的引用归 releaseCompositionContext() 释放，
        // 这里绝不能把它包进 owning 的 ContextRef（会双重 Release）。
        ITfContext* target = compCtx_;
        ContextRef owned(nullptr);
        if (target == nullptr) {
            owned.ctx = focusContext();
            target = owned.ctx;
        }
        if (target != nullptr) {
            endTarget_ = comp;
            EditSession es(target, this, [](void* user, ITfContext* c, TfEditCookie ec) {
                static_cast<ChaosetTIP*>(user)->applyEndComposition(c, ec);
            });
            HRESULT hrSession = S_OK;
            const HRESULT hr = target->RequestEditSession(
                clientId_, &es, TF_ES_SYNC | TF_ES_READWRITE, &hrSession);
            if (FAILED(hr)) {
                diagTsfFail("RequestEditSession(endComposition)", hr);
            } else if (FAILED(hrSession)) {
                // TF_ES_SYNC 在后台线程等场景会失败；保留最后一次成功的
                // cookie 收尾——比换 cookie 0 强（0 必失败，旧值多半仍有效）。
                diagTsfFail("RequestEditSession(endComposition).session", hrSession);
                if (compEditCookie_ > 0) applyEndComposition(nullptr, compEditCookie_);
            }
            es.Release();
            endTarget_ = nullptr;
        }
        comp->Release();
        releaseCompositionContext();
    }

    void applyEndComposition(ITfContext* /*ctx*/, TfEditCookie ec) {
        ITfComposition* comp = endTarget_;
        if (comp == nullptr) return;
        if (ec > 0) compEditCookie_ = ec;  // 记录成功 cookie（endComposition 兜底用）
        ITfRange* range = nullptr;
        if (SUCCEEDED(comp->GetRange(&range)) && range != nullptr) {
            const HRESULT hrText = range->SetText(ec, 0, L"", 0);
            if (FAILED(hrText)) diagTsfFail("ITfRange::SetText(clear)", hrText);
            range->Release();
        }
        const HRESULT hrEnd = comp->EndComposition(ec);
        if (FAILED(hrEnd)) diagTsfFail("ITfComposition::EndComposition(clear)", hrEnd);
    }

    LONG ref_ = 1;
    ITfThreadMgr* tm_ = nullptr;
    TfClientId clientId_ = 0;
    bool keySinkAdvised_ = false;

    CandidateWindow candidateWnd_;
    ModeToast toast_;

    // 短暂提示（半/全角、删除候选结果）：放光标行底边下方，与候选窗同锚点。
    //
    // **真实光标未知时不显示**，除非调用方明确允许鼠标锚点（删除候选是鼠标
    // 触发的操作，提示落在鼠标附近符合预期）。2026-11-05 用户反馈「中英文
    // 提示在光标和鼠标两处都出现」——模式提示已改由单例托盘承担；这里也
    // 不能再把鼠标兜底坐标当成光标，否则提示又会跟着鼠标跑。
    void showToast(const wchar_t* text, bool allowMouseAnchor = false) {
        // 非激活一律不弹任何浮窗（2026-11-06）：提示条虽带 WS_EX_TRANSPARENT
        // 不吃点击，但 TOPMOST 浮窗在别人的应用上突然出现同样是干扰。
        if (!isActiveTip()) return;
        if (!allowMouseAnchor) {
            if (!caretFound_ || caretFromMouse_) return;
        }
        POINT anchor = caretPos_;
        if (anchor.x == 0 && anchor.y == 0) return;
        // 与候选窗同锚点：光标行底边下方 6pt（mac showToast 复用
        // candidateOrigin，候选窗与提示条位置一致）。2026-10-30 审计 A12：
        // 此前是「光标行底 + 整行高 + 6px」，比候选窗低约一行且随 DPI 偏
        // 0.75 倍；工作区钳制在 ModeToast::show 内做。
        const int gap = MulDiv(6, systemDpi(), 72);
        toast_.show(g_hInst, text, anchor.x, anchor.y + gap);
    }
    POINT caretPos_{0, 0};
    int caretLineHeight_ = 20;
    // 本次渲染是否拿到了**真实** GDI 光标矩形（caretPos_ 可信）。
    bool caretFound_ = false;
    // caretPos_ 是否来自鼠标兜底（宿主没有真实光标时）。提示条据此拒绝
    // 在鼠标位置显示，避免「提示跟着鼠标跑」（2026-11-05 用户反馈）。
    bool caretFromMouse_ = false;

    PipeTransport transport_;
    chaoset::Session session_{transport_};
    chaoset::Comp comp_;
    // 渐进上屏的「已确认段」缓冲（2026-10-30 兼容性整改，与 macOS 壳
    // confirmedText 同语义）：SelectPrefix 返回的已确认文本**不再立即
    // 结束合成写盘**，而是并入当前合成一起展示（applyPreedit 写
    // confirmedPreedit_ + comp_.preedit），直到整条组词结束才一次落盘。
    //
    // 为什么（用户实测：长串组词中选 1（今），部分 App 里「今」丢失、
    // 剩余拼音卡死）：旧实现每次渐进选择都「EndComposition + 立刻
    // StartComposition」——对正确跟踪选区的 App 没问题，但对合成结束/
    // 重建时选区记账有缺陷的 App（部分 Chromium/Java/旧 Win32 宿主），
    // 新合成的锚点可能落回旧文本处，随后的 SetText 直接覆盖掉刚上屏的
    // 「今」，只剩剩余拼音。改单合成模型后，渐进上屏与逐键输入使用完全
    // 相同的原语（同一条合成内 SetText），不再中途结束/重建合成；落盘只剩
    // 「整条组词结束」一个时机，与微软拼音「合成内已转换段」的行为一致。
    // 清空时机：commit（落盘）、endComposition（取消）、OnCompositionTerminated
    // （宿主终止，文本已随合成留在文档）、activateServer 之外的一切
    // 会话结束路径——绝不能串进下一轮组词。
    std::string confirmedPreedit_;
    // 引擎双拼方案名（zrm/mspy/flypy；空 = 未启用）。只用于按键路由：
    // 双拼时 ';' 是引擎的组词/解码键，必须走 input 而不是 punct。
    // 来源见 syncDpScheme（IPC Composition.shuangpin）。
    std::string dpScheme_;
    bool halfWidth_ = false; // 全/半角标点开关（壳本地持有，随 punct 请求下发）
    // 词组联想（对齐 macOS 壳）：candidates 非空即联想中。选词上屏后按
    // 上屏词音节查续接词组继续展示，数字键可直接连选。
    chaoset::Comp assoc_;
    int assocPage_ = 0; // 联想当前页（0 起，本地分页每页 6 条）
    // 联想来源音节（撇号分隔）：右键删除联想候选后按同一来源重新查询续接
    // 词组，保证删除刷新不改变联想链（与 macOS associateSylls 同语义）。
    std::string assocSylls_;
    ITfComposition* composition_ = nullptr;
    // 合成所属宿主窗口（render 时缓存；前台事件回调只读，避免回调里碰 TSF）。
    HWND composeHostWnd_ = nullptr;
    ITfComposition* endTarget_ = nullptr;  // endComposition 编辑会话内的目标
    std::string committed_;
    bool commitWritten_ = false;
    bool preeditWritten_ = false;  // 预编辑确实写进了文档（渲染兜底判定用）

    // 合成结束（commit / endComposition / 外部终止）后调用：清空合成所属
    // 上下文记录。composition_ 为空时 compCtx_ 必须为空，避免持有过期引用。
    void releaseCompositionContext() {
        if (compCtx_ != nullptr) {
            compCtx_->Release();
            compCtx_ = nullptr;
        }
        composeHostWnd_ = nullptr;
        preeditWritten_ = false;
    }

    // 撇号分隔的音节串 → 音节数组（"xian'zai" → ["xian","zai"]）。
    static std::vector<std::string> splitSyllables(const std::string& syllables) {
        std::vector<std::string> out;
        std::size_t start = 0;
        while (start <= syllables.size()) {
            std::size_t pos = syllables.find('\'', start);
            if (pos == std::string::npos) {
                if (start < syllables.size()) out.push_back(syllables.substr(start));
                break;
            }
            if (pos > start) out.push_back(syllables.substr(start, pos - start));
            start = pos + 1;
        }
        return out;
    }

    // 选词上屏后进入联想：查续接词组并复用候选窗展示（单页，页码 1/1）。
    // 候选栏齿轮 → 设置菜单（微信输入法交互参考）：菜单项带勾选态，
    // 点击即切换并立即生效（关闭时收起联想窗，开启后下次选词生效）。
    // TrackPopupMenu 前需 SetForegroundWindow（否则菜单点外部不消失，
    // MSDN KB135788 的经典要求），返回后补发 WM_NULL 复位。
public:
    // ---- worker 消息回投入口（message-only 窗口 → TIP 线程）----
    // 500ms 激活档案复核（定时器只在 active 时运行）：部分宿主切换输入法后
    // 不会调用 Deactivate（2026-11-05 实测），必须有一处事件驱动兜底让托盘
    // 和按键闸门在同一时刻失效。
    void onActiveWatchTimer() {
        if (!inputActive_) return;
        if (isActiveTip()) return;
        switchToInactiveProfile();
    }
    void onWorkerForeground(HWND fg) { onForegroundChanged(fg); }
    void onWorkerModeAuthority() {
        if (cachedModeScope() != ModeScope::System) return;
        if (g_modeAuthorityValid.load() != 0) {
            enMode_ = g_modeAuthorityEnglish.load() != 0;
            syncModeCompartment();
        }
    }
    void onWorkerShowToast(int id) {
        if (id == 1) showToast(L"设置窗口打不开：chaoset-ui.exe 未运行", true);
    }

    // 设置窗口入口（三端一致的独立设置窗口，macOS SettingsWindow 同构；
    // 定义在本文件后部）。
    void showSettingsWindow() override;

    // 面板当前是否在展示联想候选（候选窗箭头点击据此选择翻哪一组页）。
    // 2026-10-29：联想与组词共用同一个候选窗，但分页数据源不同
    // （assoc_ vs comp_），此前箭头点击无条件翻组词页。
    bool isAssociatePanel() const override { return !assoc_.candidates.empty(); }

    // 联想翻页入口（供候选窗箭头调用；实现见下方 flipAssociate）。
    void flipAssociateFromPanel(bool next) override;

    // 设置窗关闭联想后立即收起（候选窗收到跨进程广播后转发；macOS 同语义）。
    void clearAssociateFromSettings() override;

private:

    void enterAssociate(const std::string& syllables) {
        if (!cachedAssociate()) return;  // 设置关闭词组联想（1s TTL，审计 T10）
        assoc_ = chaoset::Comp();
        assocSylls_.clear();
        std::vector<std::string> sylls = splitSyllables(syllables);
        if (sylls.empty()) return;
        chaoset::Comp assoc;
        if (!session_.associate(sylls, &assoc) || assoc.candidates.empty()) return;
        syncDpScheme(assoc);
        assoc_ = std::move(assoc);
        assocSylls_ = syllables;
        assocPage_ = 0;
        showAssociatePage();
    }

    // 展示联想当前页（本地分页：全量候选在 assoc_，每页 pageSize 条）。
    void showAssociatePage() {
        const int per = kPageSize; // 与 engine.PageSize 一致（数字键 1-6 指位）
        const int total = static_cast<int>(assoc_.candidates.size());
        const int pageCount = (total + per - 1) / per;
        if (assocPage_ >= pageCount) assocPage_ = pageCount - 1;
        const int start = assocPage_ * per;
        const int end = std::min(start + per, total);
        chaoset::Comp page;
        page.ok = true;
        page.page = assocPage_;
        page.pageCount = pageCount;
        page.candidates.assign(assoc_.candidates.begin() + start,
                               assoc_.candidates.begin() + end);
        candidateWnd_.show(g_hInst, caretPos_, caretLineHeight_, page, this);
    }

    // 联想翻页（- / = 键）：边界页保持当前页。返回是否发生了翻页。
    bool flipAssociate(bool next) {
        const int total = static_cast<int>(assoc_.candidates.size());
        const int pageCount = (total + kPageSize - 1) / kPageSize;
        if (next && assocPage_ + 1 < pageCount) {
            ++assocPage_;
        } else if (!next && assocPage_ > 0) {
            --assocPage_;
        } else {
            return false;
        }
        showAssociatePage();
        return true;
    }

    // 退出联想并收起联想窗。
    void clearAssociate() {
        if (assoc_.candidates.empty()) return;
        assoc_ = chaoset::Comp();
        assocSylls_.clear();
        assocPage_ = 0;
        candidateWnd_.hide();
    }
};

// ---- TIP 线程 message-only 窗口（worker / 钩子回调 → TIP 线程）----
//
// 2026-11-06 零阻塞：ShellWorker 与 SetWinEventHook 回调都不能碰 TSF/UI
// 对象，只能 PostMessage 到这个窗口；窗口过程在 TIP 线程上分发。
static LRESULT CALLBACK tipMsgWndProc(HWND hwnd, UINT msg, WPARAM wParam, LPARAM lParam) {
    switch (msg) {
        case WM_TIMER:
            if (wParam == kActiveWatchTimerId) {
                for (ChaosetTIP* tip : g_tlFgTips) {
                    if (tip != nullptr) tip->onActiveWatchTimer();
                }
                return 0;
            }
            break;
        case WM_NCDESTROY:
            if (g_tlTipMsgWnd == hwnd) g_tlTipMsgWnd = nullptr;
            unregTipWnd(hwnd);
            InterlockedDecrement(&g_liveTipMsgWindows);
            return 0;
        case kTipForegroundMsg: {
            HWND fg = reinterpret_cast<HWND>(wParam);
            for (ChaosetTIP* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerForeground(fg);
            }
            return 0;
        }
        case kTipModeAuthorityMsg: {
            for (ChaosetTIP* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerModeAuthority();
            }
            return 0;
        }
        case kTipShowToastMsg:
            for (ChaosetTIP* tip : g_tlFgTips) {
                if (tip != nullptr) tip->onWorkerShowToast(static_cast<int>(wParam));
            }
            return 0;
        default:
            break;
    }
    return DefWindowProcW(hwnd, msg, wParam, lParam);
}

static HWND ensureTipMsgWnd() {
    if (g_tlTipMsgWnd != nullptr) return g_tlTipMsgWnd;
    const std::wstring cls = windowClassName(L"ChaosetTipMsgWndClass", g_hInst);
    WNDCLASSEXW wc{};
    wc.cbSize = sizeof(wc);
    wc.lpfnWndProc = tipMsgWndProc;
    wc.hInstance = g_hInst;
    wc.lpszClassName = cls.c_str();
    RegisterClassExW(&wc);
    HWND wnd = CreateWindowExW(0, cls.c_str(), L"ChaosetTipMsg", 0, 0, 0, 0, 0,
                               HWND_MESSAGE, nullptr, g_hInst, nullptr);
    if (wnd == nullptr) return nullptr;
    g_tlTipMsgWnd = wnd;
    InterlockedIncrement(&g_liveTipMsgWindows);
    regTipWnd(GetCurrentThreadId(), wnd);
    return wnd;
}

static void registerTipOnThread(ChaosetTIP* tip) {
    if (tip == nullptr) return;
    for (ChaosetTIP* p : g_tlFgTips) {
        if (p == tip) return;
    }
    g_tlFgTips.push_back(tip);
}

static void unregisterTipFromThread(ChaosetTIP* tip) {
    if (tip == nullptr) return;
    removeTipFromThreadHookList(tip);
    for (auto it = g_tlFgTips.begin(); it != g_tlFgTips.end(); ++it) {
        if (*it == tip) {
            g_tlFgTips.erase(it);
            break;
        }
    }
    if (g_tlFgTips.empty() && g_tlTipMsgWnd != nullptr) {
        DestroyWindow(g_tlTipMsgWnd);  // WM_NCDESTROY 里归还计数并清 thread_local
    }
}

// 联想翻页的候选窗入口（public 转发，实现在 ChaosetTIP 私有区）。
void ChaosetTIP::flipAssociateFromPanel(bool next) { flipAssociate(next); }

// 设置窗关闭联想：立即收起当前联想面板（候选窗收到跨进程广播后转发；
// macOS 设置窗 post 通知后 Controller 也是立即收起）。
void ChaosetTIP::clearAssociateFromSettings() { clearAssociate(); }

// 候选栏齿轮：设置窗由单例 UI 进程（chaoset-ui.exe）创建，TIP 只发管道请求。
// 优先请求已在运行的 UI 宿主；没有宿主时由 UI 宿主带 --settings 启动自己
// （首实例直接弹设置窗，第二个实例把请求转给首实例后退出）。AppContainer
// 宿主不能发起进程，只能连接登录 Run 项常驻的 UI 宿主。
void ChaosetTIP::showSettingsWindow() {
    // TSF 线程零等待：openSettingsViaUi 内部只做前台授权 + worker 入队，
    // 恒返回 true（管道重试/拉起 UI 宿主/失败提示都在 ShellWorker）。
    if (openSettingsViaUi()) return;  // 保留既有静态契约：不是同步打开
    // 理论上的失败兜底（当前 openSettingsViaUi 恒 true）：AppContainer 宿主
    // 不能 CreateProcess，入队请 daemon 代拉起（worker 线程执行）。
    if (isAppContainer()) ensureUiHostStartedByDaemon(/*settings=*/true);
    static volatile LONG logged = 0;
    if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
        diagPrintf(L"tsf.log", "open settings failed: ui host unavailable appcontainer=%d\n",
                   isAppContainer() ? 1 : 0);
    }
    showToast(L"设置窗口打不开：chaoset-ui.exe 未运行", true);
}

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

// chaoset-ui.exe 的唯一入口：宿主托盘中英指示器 + 设置窗 + 命名管道服务。
// 实现全部在本 DLL 内（设置窗代码/托盘绘制/管道），exe 只是 LoadLibrary 壳。
extern "C" __declspec(dllexport) int WINAPI ChaosetRunUi(HINSTANCE /*exeInst*/,
                                                         LPCWSTR cmdLine) {
    // UI 宿主进程里的异步日志/后台任务 worker 属于本 TU（TIP 的 ShellWorker），
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

