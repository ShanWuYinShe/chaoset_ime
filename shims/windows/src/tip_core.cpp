// ChaosetTIP 生命周期/激活状态机/中英模式/前台钩子（Windows 壳拆分 TU，wave2）。
//
// 从 win32.cpp 的类内 inline 定义逐字拆出：ctor/dtor/QueryInterface/AddRef/
// Release、Activate/ActivateEx/Deactivate、OnSetFocus/OnCompositionTerminated、
// 中英模式与激活档案（currentEnglish/storeEnglish/isActiveTip/updateInputActive）、
// 前台钩子（SetWinEventHook 回调只 PostMessage）、retire/收口路径，以及 worker
// 回投入口（message-only 窗口 → 本线程 TIP）。
//
// 模块边界：只依赖 tip.hpp（经它拿到 TIP 声明与叶子模块头）与 tip_thread.hpp 的
// 线程注册表原语；不 include win32.cpp / 其它 tip_*.cpp。

#include "tip.hpp"

using namespace chaoset::win;

ChaosetTIP::ChaosetTIP() {
    InterlockedIncrement(&g_liveObjects);
    // TSF 线程上的传输：只允许单次非阻塞连接 + 30ms 有界请求（fail-open）。
    transport_.setAutoConnect(false);
    transport_.setDeadlineMs(30);
}

ChaosetTIP::~ChaosetTIP() {
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
HRESULT STDMETHODCALLTYPE ChaosetTIP::QueryInterface(REFIID riid, void** ppv) {
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

ULONG STDMETHODCALLTYPE ChaosetTIP::AddRef() { return InterlockedIncrement(&ref_); }

ULONG STDMETHODCALLTYPE ChaosetTIP::Release() {
    ULONG r = InterlockedDecrement(&ref_);
    if (r == 0) delete this;
    return r;
}


// ITfTextInputProcessor
HRESULT STDMETHODCALLTYPE ChaosetTIP::Activate(ITfThreadMgr* ptim, TfClientId tid) {
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
HRESULT STDMETHODCALLTYPE ChaosetTIP::ActivateEx(ITfThreadMgr* ptim, TfClientId tid,
DWORD dwFlags) {
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


HRESULT STDMETHODCALLTYPE ChaosetTIP::Deactivate() {
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
HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetFocus(BOOL fForeground) {
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


// ITfCompositionSink：合成被外部终止（焦点切换等）
HRESULT STDMETHODCALLTYPE ChaosetTIP::OnCompositionTerminated(TfEditCookie /*ec*/,
ITfComposition* /*pComposition*/) {
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

ITfKeyEventSink* ChaosetTIP::keySink() { return static_cast<ITfKeyEventSink*>(this); }

ITfCompositionSink* ChaosetTIP::compositionSink() { return static_cast<ITfCompositionSink*>(this); }


bool ChaosetTIP::isComposing() const { return !comp_.input.empty(); }

bool ChaosetTIP::hasCandidates() const { return !comp_.candidates.empty(); }


// 进程级/窗口级状态（process / window 作用域用）。故意放在进程静态区：
// TSF 会在同一宿主进程里创建多个 TIP 实例，process 档要求它们共享。
std::map<HWND, bool>& ChaosetTIP::windowModes() {
    static std::map<HWND, bool> m;
    return m;
}

bool& ChaosetTIP::processEnglish() {
    static bool english = false;
    return english;
}


// 当前宿主窗口（window 作用域的分桶键）。拿不到就退化成进程级，
// 绝不能因为取不到窗口而让状态随机跳。
HWND ChaosetTIP::modeBucketKey() const {
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
bool ChaosetTIP::currentEnglish() {
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
void ChaosetTIP::syncEnglishFromAuthority() {
    if (cachedModeScope() != ModeScope::System) return;
    bool english = false;
    if (readHkcuEnglish(&english)) {
        enMode_ = english;
        syncModeCompartment();  // 让系统输入指示器/隔间也跟上
    }
    postModeQuery();
}


// 写当前中英模式（所有切换都必须走这里）。
void ChaosetTIP::storeEnglish(bool english) {
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


bool ChaosetTIP::isActiveTip() {
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
void CALLBACK ChaosetTIP::fgHookProc(HWINEVENTHOOK /*hook*/, DWORD event, HWND hwnd, LONG, LONG,
DWORD, DWORD) {
    if (event != EVENT_SYSTEM_FOREGROUND) return;
    if (g_tlTipMsgWnd != nullptr) {
        PostMessageW(g_tlTipMsgWnd, kTipForegroundMsg,
                     reinterpret_cast<WPARAM>(hwnd), 0);
    }
}


// 在 TIP 线程处理前台变化（由 message-only 窗口回调）。
void ChaosetTIP::onForegroundChanged(HWND fg) {
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
bool ChaosetTIP::shouldWatchForeground() const {
    if (!shellHostNeedsOwner()) return false;
    if (!const_cast<ChaosetTIP*>(this)->isActiveTip()) return false;
    const HWND fg = GetForegroundWindow();
    if (fg == nullptr) return false;
    DWORD fgPid = 0;
    GetWindowThreadProcessId(fg, &fgPid);
    return fgPid == GetCurrentProcessId();
}


void ChaosetTIP::ensureForegroundHook() {
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


void ChaosetTIP::unhookForeground() {
    removeTipFromThreadHookList(this);
    fgHook_ = nullptr;
    if (g_tlHookTips.empty() && g_tlFgHook != nullptr) {
        UnhookWinEvent(g_tlFgHook);
        g_tlFgHook = nullptr;
    }
}


bool ChaosetTIP::foregroundIsSelf() {
    const HWND fg = GetForegroundWindow();
    if (fg == nullptr) return false;
    DWORD fgPid = 0;
    GetWindowThreadProcessId(fg, &fgPid);
    return fgPid == GetCurrentProcessId();
}


// 唯一的激活状态迁移出口。fromFocusEvent：OnSetFocus(TRUE) 时 TSF 已把焦点
// 交给本上下文，允许不等 GetForegroundWindow 更新；Activate 则需要前台校验
//（后台宿主会重复 Activate）。
void ChaosetTIP::updateInputActive(bool fromFocusEvent) {
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
void ChaosetTIP::warmDaemonInBackgroundOnce() {
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
void ChaosetTIP::retireUiCompletely() {
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
void ChaosetTIP::refreshActiveWatchTimer() {
    if (g_tlTipMsgWnd == nullptr) return;
    bool any = false;
    for (TipThreadSink* t : g_tlFgTips) {
        if (t != nullptr && t->tipInputActive()) {
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
void ChaosetTIP::switchToInactiveProfile() {
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


bool ChaosetTIP::bailOutIfInactive() {
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


void ChaosetTIP::applyPendingStaleReset() {
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
bool ChaosetTIP::ensureUiHostStartedByDaemon(bool settings) {
    postEnsureUiHostByDaemon(settings);
    return true;
}


// 把当前中英模式写进线程级隔间，供 Windows 输入指示器显示。
// 语义（微软《predefined compartments》）：非零 = 键盘打开（中文输入）。
void ChaosetTIP::syncModeCompartment() {
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


void ChaosetTIP::toggleEnMode(ITfContext* pic) {
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

// ---- worker 消息回投入口（message-only 窗口 → TIP 线程）----
// 500ms 激活档案复核（定时器只在 active 时运行）：部分宿主切换输入法后
// 不会调用 Deactivate（2026-11-05 实测），必须有一处事件驱动兜底让托盘
// 和按键闸门在同一时刻失效。
void ChaosetTIP::onActiveWatchTimer() {
    if (!inputActive_) return;
    if (isActiveTip()) return;
    switchToInactiveProfile();
}

void ChaosetTIP::onWorkerForeground(HWND fg) { onForegroundChanged(fg); }

void ChaosetTIP::onWorkerModeAuthority() {
    if (cachedModeScope() != ModeScope::System) return;
    if (g_modeAuthorityValid.load() != 0) {
        enMode_ = g_modeAuthorityEnglish.load() != 0;
        syncModeCompartment();
    }
}

void ChaosetTIP::onWorkerShowToast(int id) {
    if (id == 1) showToast(L"设置窗口打不开：chaoset-ui.exe 未运行", true);
}
