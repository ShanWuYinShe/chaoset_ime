// ChaosetTIP 候选交互：点选/删除/翻页、短暂提示、设置中心入口与联想面板
// （Windows 壳拆分 TU，wave2）。
//
// 从 win32.cpp 拆出：selectCandidateByIndex/deleteCandidateByIndex/flipPage、
// showToast、联想面板（enterAssociate/showAssociatePage/flipAssociate/
// clearAssociate）、候选窗 CandidateWindowHost 的转发入口（isAssociatePanel/
// flipAssociateFromPanel/clearAssociateFromSettings）与 showSettingsWindow
// （齿轮/设置中心：TIP 只发管道请求，由单例 UI 宿主启动 chaoset-settings.exe）。
//
// 模块边界：只依赖 tip.hpp；候选窗对 TIP 的接触面仍是 CandidateWindowHost
// 抽象接口（wave1 定稿），本 TU 不反向 include 候选窗实现。

#include "tip.hpp"

using namespace chaoset::win;


void ChaosetTIP::selectCandidateByIndex(int index) {
    g_lastOp = kOpCandidateClick;
    // 异步管线（issue #149）：点击只入队，结果回来时按渐进上屏语义上屏；
    // daemon 失败时用壳侧已显示的候选文本兜底（见 tip_async.cpp）。
    ensureEnginePump();
    if (engineCtx_ == nullptr) {
        ITfContext* ctx = focusContext();
        if (ctx != nullptr) {
            setEngineContext(ctx);
            ctx->Release();
        }
    }
    if (!assoc_.candidates.empty()) {
        const int global = assocPage_ * kPageSize + index;
        if (index < 0 || global >= static_cast<int>(assoc_.candidates.size())) return;
        const chaoset::Candidate pick = assoc_.candidates[global];
        chaoset::win::EngineOp op;
        op.kind = chaoset::win::EngineOpKind::kAssociateSelect;
        op.text = pick.text;
        op.syllables = pick.syllables;
        submitNonComposingOp(op);
        return;
    }
    if (index < 0 || index >= static_cast<int>(comp_.candidates.size())) {
        // 异步乐观窗口（issue #149）：壳侧候选列表还没追上按键时，不能把选词
        // 意图丢掉——按同一下标入队，由引擎对**完整输入**解析；失败分支无文本
        // 可兜底，保持组词并整串重建，键不会丢。列表已是最新时越界下标照旧忽略。
        if (pendingOps_.empty()) return;
        chaoset::win::EngineOp op;
        op.kind = chaoset::win::EngineOpKind::kSelectPrefix;
        op.index = index;
        submitEngineOp(op);
        return;
    }
    // 与数字键/空格相同的渐进上屏路径（2026-10-30 三端一致性整改 A4）：
    // 长串拼音下点一个候选只消耗该候选对应音节，剩余拼音继续组词。
    const chaoset::Candidate pick = comp_.candidates[index];
    chaoset::win::EngineOp op;
    op.kind = chaoset::win::EngineOpKind::kSelectPrefix;
    op.index = index;
    op.text = pick.text;
    op.syllables = pick.syllables;
    submitEngineOp(op);
}

void ChaosetTIP::deleteCandidateByIndex(int index) {
    g_lastOp = kOpCandidateClick;
    // 右键菜单是模态的，菜单期间 TSF 焦点回调可能已把本实例退场（非激活零副作用）：
    // 不删、不刷新、不 render。
    if (diagBudget()) {
        diagPrintf(L"tsf.log",
                   "candidate delete enter active=%d focused=%d composing=%d candidates=%lu raw_len=%lu\n",
                   inputActive_ ? 1 : 0, focused_ ? 1 : 0, isComposing() ? 1 : 0,
                   static_cast<unsigned long>(comp_.candidates.size()),
                   static_cast<unsigned long>(comp_.input.size()));
    }
    if (!inputActive_) {
        if (diagBudget()) diagPrintf(L"tsf.log", "candidate delete skipped: inactive\n");
        return;
    }
    // 异步管线（issue #149）：删除只入队；成功后的刷新由 tip_async.cpp 的
    // kUserDelete 应用分支完成（组词态整串重建、联想态按同一来源重查）。
    if (!assoc_.candidates.empty()) {
        const int global = assocPage_ * kPageSize + index;
        if (index < 0 || global >= static_cast<int>(assoc_.candidates.size())) return;
        const chaoset::Candidate pick = assoc_.candidates[global];
        chaoset::win::EngineOp op;
        op.kind = chaoset::win::EngineOpKind::kUserDelete;
        op.text = pick.text;
        op.syllables = pick.syllables;
        op.associate = true;
        op.aux = assocSylls_;
        submitNonComposingOp(op);
        return;
    }
    if (!hasCandidates()) return;
    if (index < 0 || index >= static_cast<int>(comp_.candidates.size())) return;
    const chaoset::Candidate pick = comp_.candidates[index];
    chaoset::win::EngineOp op;
    op.kind = chaoset::win::EngineOpKind::kUserDelete;
    op.text = pick.text;
    op.syllables = pick.syllables;
    submitNonComposingOp(op);
}

HWND ChaosetTIP::menuOwnerWindow() {
    auto sameProcess = [](HWND wnd) -> bool {
        if (wnd == nullptr || !IsWindow(wnd)) return false;
        DWORD pid = 0;
        GetWindowThreadProcessId(wnd, &pid);
        return pid == GetCurrentProcessId();
    };
    // 1) 本进程的前台窗口：普通宿主 = 文档顶层窗口；沉浸式 shell 宿主 = shell 窗口。
    //    （candidateWnd_.hwnd() 只在异常情况下才可能成为前台，显式排除。）
    const HWND fg = GetForegroundWindow();
    if (fg != candidateWnd_.hwnd() && sameProcess(fg)) return fg;
    // 2) TSF 焦点上下文的宿主窗口：前台窗口属于别的进程（UWP/宿主桥接窗口）时兜底。
    ContextRef ctx(focusContext());
    const HWND host = contextHostWindow(ctx.get());
    if (sameProcess(host)) return host;
    // 3) render 缓存的合成宿主窗口（合成仍有效时的快速兜底，免一次 COM 查询）。
    if (sameProcess(composeHostWnd_)) return composeHostWnd_;
    // 取不到：候选窗放弃弹菜单（绝不退回候选窗自身当 owner/前台）。
    return nullptr;
}


void ChaosetTIP::setContextMenuTracking(bool active) {
    contextMenuTracking_ = active;
}


void ChaosetTIP::flipPage(bool next) {
    if (!hasCandidates() || comp_.pageCount <= 1) return;
    g_lastOp = kOpCandidateClick;
    // 异步管线：翻页只入队，结果回来由 tip_async.cpp 应用并刷新候选。
    ensureEnginePump();
    chaoset::win::EngineOp op;
    op.kind = next ? chaoset::win::EngineOpKind::kNextPage
                   : chaoset::win::EngineOpKind::kPrevPage;
    submitEngineOp(op);
    candidateWnd_.setSelectedIndex(0);
}

void ChaosetTIP::showToast(const wchar_t* text, bool allowMouseAnchor) {
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


// 面板当前是否在展示联想候选（候选窗箭头点击据此选择翻哪一组页）。
// 2026-10-29：联想与组词共用同一个候选窗，但分页数据源不同
// （assoc_ vs comp_），此前箭头点击无条件翻组词页。
bool ChaosetTIP::isAssociatePanel() const { return !assoc_.candidates.empty(); }


void ChaosetTIP::enterAssociate(const std::string& syllables) {
    if (!cachedAssociate()) return;  // 设置关闭词组联想（1s TTL，审计 T10）
    assoc_ = chaoset::Comp();
    assocSylls_.clear();
    if (splitSyllables(syllables).empty()) return;
    // 异步管线：联想查询只入队，结果回来时展示（绝不阻塞按键回调）。
    chaoset::win::EngineOp op;
    op.kind = chaoset::win::EngineOpKind::kAssociate;
    op.syllables = syllables;
    submitNonComposingOp(op);
}

void ChaosetTIP::showAssociatePage() {
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
bool ChaosetTIP::flipAssociate(bool next) {
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
void ChaosetTIP::clearAssociate() {
    if (assoc_.candidates.empty()) return;
    assoc_ = chaoset::Comp();
    assocSylls_.clear();
    assocPage_ = 0;
    candidateWnd_.hide();
}


// 联想翻页的候选窗入口（public 转发，实现在 ChaosetTIP 私有区）。
void ChaosetTIP::flipAssociateFromPanel(bool next) { flipAssociate(next); }

// 设置中心关闭联想：立即收起当前联想面板（候选窗收到跨进程广播后转发；
// macOS 设置窗 post 通知后 Controller 也是立即收起）。
void ChaosetTIP::clearAssociateFromSettings() { clearAssociate(); }

// 候选栏齿轮：唯一设置 UI 是跨平台 Slint 设置中心（chaoset-settings.exe），
// 由单例 UI 宿主进程按请求启动；TIP 只发管道请求（TSF 线程零阻塞，且 AC
// 宿主里 TIP 不能 CreateProcess）。优先请求已在运行的 UI 宿主；没有宿主时
// 由 UI 宿主带 --settings 启动自己（首实例随即拉起设置中心）。
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
