// TSF 信号 sink 与激活档案查询（Windows 壳拆分 TU，2026-11-13）。
//
// 从 tip_core.cpp 按职责拆出（tip_core 已到行数预算上限）：ITfThreadFocusSink /
// ITfActiveLanguageProfileNotifySink 的挂载与回调、ITfInputProcessorProfileActivationSink
//（2026-11-13 托盘常显收口新增：键盘布局切换只有它通知）、以及「本线程激活档案
// 是不是本输入法」的查询（isActiveTip）。
//
// 为什么需要第三个 sink：ITfActiveLanguageProfileNotifySink 只覆盖「输入处理器
// （TIP）档案」的切换；用户切到**键盘布局**（美式键盘等，TF_PROFILETYPE_
// KEYBOARDLAYOUT、clsid=CLSID_NULL）时它不通知，按键也不再下沉给本 TIP——
// 两条隐藏路径同时失效，托盘就永久残留（用户 2026-11-13 反馈「切换输入法后
// trayicon 仍然在显示，好像做成永久显示了一样」）。ITfInputProcessorProfileActivationSink
// 挂在同一个 ITfSource 上，但微软文档明确其通知覆盖 TF_PROFILETYPE_INPUTPROCESSOR
// 与 TF_PROFILETYPE_KEYBOARDLAYOUT 两类，dwFlags 的 TF_IPSINK_FLAG_ACTIVE 区分
// 激活/失活，是语言栏自己用的权威档案事件源。
//
// 模块边界：只依赖 tip.hpp；回调在宿主 TSF 线程同步执行，只做内存判断 + 入队
//（零阻塞契约，由 checkuiparity「TSF 回调零跨进程等待」组断言）。

#include "tip.hpp"

using namespace chaoset::win;


// ---- TSF 焦点/输入法切换原语（2026-11-11，替换 GetForegroundWindow 的 pid 启发式）----
//
// 挂/摘三个 sink 都走 tm_ 的 ITfSource（TSF 标准连接点）：AdviseSink 会
// AddRef 本对象，因此 cookie 有效期间对象不会被提前释放；UnadviseSink 必须在
// tm_ 释放前成对调用（Deactivate/析构幂等，见 unadviseThreadSinks）。
void ChaosetTIP::adviseThreadSinks() {
    if (tm_ == nullptr) return;
    ITfSource* source = nullptr;
    if (FAILED(tm_->QueryInterface(IID_ITfSource, reinterpret_cast<void**>(&source))) ||
        source == nullptr) {
        return;
    }
    if (threadFocusCookie_ == TF_INVALID_COOKIE) {
        DWORD cookie = TF_INVALID_COOKIE;
        if (SUCCEEDED(source->AdviseSink(IID_ITfThreadFocusSink,
                                         static_cast<ITfThreadFocusSink*>(this), &cookie))) {
            threadFocusCookie_ = cookie;
        }
    }
    if (languageProfileCookie_ == TF_INVALID_COOKIE) {
        DWORD cookie = TF_INVALID_COOKIE;
        if (SUCCEEDED(source->AdviseSink(IID_ITfActiveLanguageProfileNotifySink,
                                         static_cast<ITfActiveLanguageProfileNotifySink*>(this),
                                         &cookie))) {
            languageProfileCookie_ = cookie;
        }
    }
    if (inputProcessorCookie_ == TF_INVALID_COOKIE) {
        DWORD cookie = TF_INVALID_COOKIE;
        if (SUCCEEDED(source->AdviseSink(IID_ITfInputProcessorProfileActivationSink,
                                         static_cast<ITfInputProcessorProfileActivationSink*>(this),
                                         &cookie))) {
            inputProcessorCookie_ = cookie;
        }
    }
    // 每进程一条：三个 cookie 是「托盘该隐藏时收不到事件」的第一手排障数据
    //（TF_INVALID_COOKIE = 挂载失败，托盘常显/不显示都先看这里）。
    static volatile LONG loggedAdvise = 0;
    if (InterlockedCompareExchange(&loggedAdvise, 1, 0) == 0 && diagBudget()) {
        diagPrintf(L"tsf.log", "thread sinks advised tf=%lu lp=%lu ip=%lu\n",
                   static_cast<unsigned long>(threadFocusCookie_),
                   static_cast<unsigned long>(languageProfileCookie_),
                   static_cast<unsigned long>(inputProcessorCookie_));
    }
    source->Release();
}

void ChaosetTIP::unadviseThreadSinks() {
    if (threadFocusCookie_ == TF_INVALID_COOKIE &&
        languageProfileCookie_ == TF_INVALID_COOKIE &&
        inputProcessorCookie_ == TF_INVALID_COOKIE) {
        return;
    }
    if (tm_ != nullptr) {
        ITfSource* source = nullptr;
        if (SUCCEEDED(tm_->QueryInterface(IID_ITfSource, reinterpret_cast<void**>(&source))) &&
            source != nullptr) {
            if (threadFocusCookie_ != TF_INVALID_COOKIE) {
                source->UnadviseSink(threadFocusCookie_);
                threadFocusCookie_ = TF_INVALID_COOKIE;
            }
            if (languageProfileCookie_ != TF_INVALID_COOKIE) {
                source->UnadviseSink(languageProfileCookie_);
                languageProfileCookie_ = TF_INVALID_COOKIE;
            }
            if (inputProcessorCookie_ != TF_INVALID_COOKIE) {
                source->UnadviseSink(inputProcessorCookie_);
                inputProcessorCookie_ = TF_INVALID_COOKIE;
            }
            source->Release();
            return;
        }
    }
    // tm_ 已释放/取不到 source：至少清掉 cookie，绝不重复 Unadvise 失效值。
    threadFocusCookie_ = TF_INVALID_COOKIE;
    languageProfileCookie_ = TF_INVALID_COOKIE;
    inputProcessorCookie_ = TF_INVALID_COOKIE;
}


// ITfThreadFocusSink：TSF 线程 UI 焦点。回调在宿主 TSF 线程同步执行——只做
// 内存操作 + 入队（零阻塞契约：不等待、不建进程、不同步写盘）。
//
// 焦点获得时 updateInputActive(true) 只在 focused_ + 激活档案是本输入法 + 线程
// 焦点三者同时成立时才上报 active 1（后台预激活实例不会误报）；没有 TSF 文本
// 焦点的窗口（桌面/无文本框）走下面的独立托盘显示上报。
HRESULT STDMETHODCALLTYPE ChaosetTIP::OnSetThreadFocus() {
    focused_ = true;
    updateInputActive(true);
    if (!inputActive_ && isActiveTip() && foregroundIsSelf()) {
        applyModeForCurrentScope();
        postActiveReport(true, enMode_, true);
    }
    return S_OK;
}

// 线程焦点丢失**只退场、不隐藏托盘**（issue #3）：托盘看的是「系统选中的
// 输入法是不是本输入法」，不是「当前线程有没有焦点」。
HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKillThreadFocus() {
    focused_ = false;
    updateInputActive(false);
    return S_OK;
}


// ITfActiveLanguageProfileNotifySink：TSF 的「输入法之间切换」通知。
//
// 为什么需要：用户从 Chaoset 切到其它输入法时，很多宿主**不调用 Deactivate**
//（2026-11-05 实测：按键仍下沉给本 TIP），托盘会一直显示、按键闸门也不收口。
// 这个 sink 在语言档案激活/失活时同步回调，是拍平这条路径的权威事件源；
// bailOutIfInactive 的按键复核仍保留作兜底。它**不通知键盘布局切换**——那条
// 路径由下面的 ITfInputProcessorProfileActivationSink 覆盖。
HRESULT STDMETHODCALLTYPE ChaosetTIP::OnActivated(REFCLSID clsid, REFGUID /*guidProfile*/,
                                                   BOOL fActivated) {
    if (clsid != CLSID_ChaosetTIP) return S_OK;  // 其它输入法切换：不归本 TIP 管
    if (fActivated) {
        focused_ = true;
        // 先走激活状态迁移（建 worker/窗口、上报 active 1）；再按作用域应用
        // 模式：System 从权威同步、Process 上报本进程模式（与焦点事件同语义）。
        updateInputActive(true);
        if (inputActive_) {
            applyModeForCurrentScope();
        } else if (isActiveTip() && foregroundIsSelf()) {
            // 用户从其它输入法切回本输入法（上一次切走已隐藏托盘）：本窗口可能
            // 没有 TSF 文本焦点（没有文本框/桌面），仍然必须让托盘重新出现。
            applyModeForCurrentScope();
            postActiveReport(true, enMode_, true);
        }
        return S_OK;
    }
    // 本输入法被切走（用户选了别的输入法）＝**托盘唯一的隐藏入口**：无条件上报
    // active 0，不依赖宿主是否调用 Deactivate，也不再用 active 守卫。为什么去掉
    // 守卫：失焦路径已不再上报 active 0（见 updateInputActive），这条 TSF 通知是
    // 「系统当前选中的输入法已经不是本输入法」的权威信号；同进程其它实例/后台
    // 预激活实例也可能收到该通知，UI 宿主对 active 0 统一走 300ms 防抖（期间任何
    // active 1 会取消），不会因为多实例重复上报而闪/误隐藏。收口统一走
    // switchToInactiveProfile（上报 + 退场 + Shift 挂起，幂等）。
    switchToInactiveProfile();
    return S_OK;
}


// ITfInputProcessorProfileActivationSink（2026-11-13 托盘常显收口）：见文件头。
//
// 语义（事件驱动，无轮询；重复上报由 UI 宿主 300ms 防抖吸收）：
//   * 失活且是本输入法 → switchToInactiveProfile（与上面的失活通知同语义）；
//   * 激活且是本输入法 → 不处理（激活路径归上面的 OnActivated(TRUE)）；
//   * 激活且是其它输入法/键盘布局 → 键盘槽位互斥，本输入法已被切走。但只认
//     「本进程是前台」的那次通知（逐应用输入法下别的进程自己的输入法/布局选择
//     不是切走信号，AGENTS 边界），并复核本线程激活档案确实已不是本输入法——
//     语音/手写类 TIP 激活不挤占键盘槽位，档案仍是我们就绝不能隐藏托盘。
HRESULT STDMETHODCALLTYPE ChaosetTIP::OnActivated(DWORD dwProfileType, LANGID /*langid*/,
REFCLSID clsid, REFGUID /*catid*/, REFGUID /*guidProfile*/, HKL /*hkl*/, DWORD dwFlags) {
    // 每进程只记一次：这条日志是「切走输入法托盘没隐藏」的第一手证据（本 sink
    // 是否真的收到事件、事件类型与方向）。
    static volatile LONG logged = 0;
    if (InterlockedCompareExchange(&logged, 1, 0) == 0 && diagBudget()) {
        diagPrintf(L"tsf.log", "activation sink: type=%lu activated=%d ours=%d\n",
                   static_cast<unsigned long>(dwProfileType),
                   (dwFlags & TF_IPSINK_FLAG_ACTIVE) ? 1 : 0,
                   clsid == CLSID_ChaosetTIP ? 1 : 0);
    }
    if ((dwFlags & TF_IPSINK_FLAG_ACTIVE) == 0) {
        // 失活通知只认本输入法（别的 TIP 失活与我们无关）。
        if (clsid == CLSID_ChaosetTIP) switchToInactiveProfile();
        return S_OK;
    }
    if (clsid == CLSID_ChaosetTIP) return S_OK;  // 自身激活：归上面的 OnActivated(TRUE)
    // 其它输入法/键盘布局占用键盘槽位 = 本输入法已被切走（边界见函数头注释）。
    if (foregroundIsSelf() && !isActiveTip()) switchToInactiveProfile();
    return S_OK;
}


// 本线程激活档案查询（热路径缓存判定与按键闸门共用）：见 tip.hpp 成员注释。
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
