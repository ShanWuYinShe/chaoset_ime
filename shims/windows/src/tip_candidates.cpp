// ChaosetTIP 候选交互：点选/删除/翻页、短暂提示、设置窗入口与联想面板
// （Windows 壳拆分 TU，wave2）。
//
// 从 win32.cpp 拆出：selectCandidateByIndex/deleteCandidateByIndex/flipPage、
// showToast、联想面板（enterAssociate/showAssociatePage/flipAssociate/
// clearAssociate）、候选窗 CandidateWindowHost 的转发入口（isAssociatePanel/
// flipAssociateFromPanel/clearAssociateFromSettings）与 showSettingsWindow
// （齿轮/设置窗：TIP 只发管道请求，窗口由单例 chaos-ui 宿主创建）。
//
// 模块边界：只依赖 tip.hpp；候选窗对 TIP 的接触面仍是 CandidateWindowHost
// 抽象接口（wave1 定稿），本 TU 不反向 include 候选窗实现。

#include "tip.hpp"

using namespace chaoset::win;


void ChaosetTIP::selectCandidateByIndex(int index) {
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
void ChaosetTIP::deleteCandidateByIndex(int index) {
    g_lastOp = kOpCandidateClick;
    // B2（issue #1）：右键菜单是模态的，菜单期间 TSF 焦点回调可能已把本实例退场
    //（inputActive_=false、候选窗 destroy；OnKillThreadFocus 路径不保证清 comp_）。
    // 非激活时**不删、不刷新、不 render**：否则会在已失效的上下文里重建候选/合成，
    // 违反「非激活零副作用」并可能留下悬空预编辑。
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
    if (composition_ == nullptr) {
        // 菜单期间 TSF 已外部终止合成：旧预编辑文本已按 TSF 语义留在文档里。
        // **绝不能** flushOrCancel/commit（会把 confirmedPreedit_ 重复写进文档）或
        // render 重建合成（会把旧输入重新插进可能已变化的文档位置）。只清本地/
        // 引擎会话并收起候选窗；学习记录已删除，给用户 toast。
        resetComposition();
        comp_ = chaoset::Comp();
        confirmedPreedit_.clear();
        candidateWnd_.hide();
        if (diagBudget()) {
            diagPrintf(L"tsf.log", "candidate delete refresh: composition terminated, state dropped\n");
        }
        showToast(L"已删除该词", true);
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
    // 刷新后一致性兜底（P2 与 B2 配套）：render 失败（预编辑没写进文档）时按
    // bailOutNoComposition 语义收口，绝不留「候选窗显示但文档无预编辑」的状态。
    if (!comp_.input.empty() && (composition_ == nullptr || !preeditWritten_)) {
        bailOutNoComposition();
        if (diagBudget()) {
            diagPrintf(L"tsf.log", "candidate delete refresh inconsistent, bail out\n");
        }
        showToast(L"删除后刷新候选失败", true);
        return;
    }
    showToast(L"已删除该词", true);
}


// 候选窗右键菜单的 owner（issue #1 修复核心）：优先用本进程的**前台窗口**——
// 它本来就是前台，SetForegroundWindow 等于零变化，最满足 KB135788「菜单 owner
// 必须是前台窗口，否则点菜单外不消失」，且绝不会把焦点从宿主文档移走。
// 绝不能用候选窗自身（WS_EX_NOACTIVATE 但仍会被 SetForegroundWindow 程序化激活，
// 宿主文档失去 TSF 键盘焦点 → 按键闸门关闭、合成被外部终止）。
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


void ChaosetTIP::flipPage(bool next) {
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


// 短暂提示（半/全角、删除候选结果）：放光标行底边下方，与候选窗同锚点。
//
// **真实光标未知时不显示**，除非调用方明确允许鼠标锚点（删除候选是鼠标
// 触发的操作，提示落在鼠标附近符合预期）。2026-11-05 用户反馈「中英文
// 提示在光标和鼠标两处都出现」——模式提示已改由单例托盘承担；这里也
// 不能再把鼠标兜底坐标当成光标，否则提示又会跟着鼠标跑。
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
