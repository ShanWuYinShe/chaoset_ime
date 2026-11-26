// Linux 异步引擎管线：乐观组词态、操作入队、结果按 seq 应用、失败宽限与整串重建。
//
// 主循环只调用这里的 submit/optimistic 渲染；真正的 daemon I/O 在 EnginePump 线程。
// 本文件不直接调用任何 ibus API（结果由 main.cpp 的 g_idle 回调转入 UI）。
#include "engine_state.hpp"

#include <algorithm>

namespace ibus_shell {

constexpr std::int64_t kEngineGraceMs = 2000;
constexpr std::int64_t kRebuildIntervalMs = 200;
constexpr std::int64_t kProbeIntervalMs = 500;

std::int64_t monotonicMs() { return g_get_monotonic_time() / 1000; }

void backspaceLocally(chaoset::Comp& comp, std::string& pending, bool shuangpin, bool bySyllable) {
    if (!pending.empty()) {
        pending.pop_back();
        return;
    }
    if (comp.input.empty()) return;
    if (!shuangpin && !bySyllable) {
        const bool removedSeparator = comp.input.back() == '\'';
        comp.input.pop_back();
        if (comp.preedit.empty()) return;
        comp.preedit.pop_back();
        if (comp.input.empty()) {
            comp.preedit.clear();
            return;
        }
        // 末段被删空：连同引擎补的分隔符一起去掉（用户自己敲的分隔符保留）。
        if (!removedSeparator && comp.preedit.size() >= 2 && comp.preedit.back() == '\'' &&
            comp.input.back() != '\'') {
            comp.preedit.pop_back();
        }
        return;
    }
    const std::string::size_type sep = comp.preedit.rfind('\'');
    const std::string seg = sep == std::string::npos ? comp.preedit : comp.preedit.substr(sep + 1);
    if (seg.empty()) return;
    if (comp.input.size() >= seg.size()) comp.input.resize(comp.input.size() - seg.size());
    comp.preedit = sep == std::string::npos ? std::string() : comp.preedit.substr(0, sep);
}

void IbusEngineState::probeIfDown() {
        if (pump_ == nullptr || pump_->connected()) return;
        const std::int64_t now = monotonicMs();
        if (now - lastProbeMs_ < kProbeIntervalMs) return;
        lastProbeMs_ = now;
        EngineOp op;
        op.kind = EngineOpKind::kProbe;
        op.epoch = epoch_;
        op.seq = ++seq_;
        pump_->post(std::move(op));
    }

std::uint64_t IbusEngineState::submit(EngineOp op) {
        ensurePump();
        op.epoch = epoch_;
        op.seq = ++seq_;
        pendingOps_.push_back(op);
        if (pump_ != nullptr) pump_->post(std::move(op));
        return seq_;
    }

std::uint64_t IbusEngineState::submitNonComposingOp(EngineOp op) {
        ensurePump();
        op.epoch = epoch_;
        op.seq = ++seq_;
        if (pump_ != nullptr) pump_->post(std::move(op));
        return seq_;
    }

void IbusEngineState::submitInput(const std::string& text) {
        EngineOp op;
        op.kind = EngineOpKind::kInput;
        op.text = text;
        op.halfWidth = halfWidth_;
        submit(std::move(op));
    }

void IbusEngineState::submitPunct(const std::string& key) {
        EngineOp op;
        op.kind = EngineOpKind::kPunct;
        op.text = key;
        op.halfWidth = halfWidth_;
        submit(std::move(op));
    }

void IbusEngineState::submitPage(bool next) {
        EngineOp op;
        op.kind = next ? EngineOpKind::kNextPage : EngineOpKind::kPrevPage;
        submit(std::move(op));
    }

void IbusEngineState::submitRebuild(const std::string& base, const std::string& text) {
        if (base.empty() && text.empty()) return;
        EngineOp op;
        op.kind = EngineOpKind::kRebuild;
        op.base = base;
        op.text = text;
        op.halfWidth = halfWidth_;
        submit(std::move(op));
    }

void IbusEngineState::resetPipeline(bool newSession) {
        ++epoch_;
        pendingOps_.clear();
        last_.dpPending.clear();
        engineLost_ = false;
        stopLostTimer();
        if (pump_ == nullptr) return;
        pump_->clearPending();
        EngineOp op;
        op.kind = newSession ? EngineOpKind::kReset : EngineOpKind::kResetComposition;
        op.epoch = epoch_;
        op.seq = ++seq_;
        pump_->post(std::move(op));
    }

bool applyPendingOp(const EngineOp& op, chaoset::Comp& comp, std::string& pending,
                    bool shuangpin, std::string* unapplied = nullptr) {
        if (op.kind == EngineOpKind::kInput) {
            pending += op.text;
            if (unapplied != nullptr) *unapplied += op.text;
            return true;
        }
        if (op.kind == EngineOpKind::kBackspace || op.kind == EngineOpKind::kBackspaceWord) {
            backspaceLocally(comp, pending, shuangpin, op.kind == EngineOpKind::kBackspaceWord);
            if (unapplied != nullptr && !unapplied->empty()) unapplied->pop_back();
            return true;
        }
        return false;
    }

void IbusEngineState::computeOptimistic(chaoset::Comp* dispOut, std::string* baseOut,
                                        std::string* textOut) const {
        chaoset::Comp disp = last_;
        std::string pending = last_.dpPending;
        std::string unappliedPreedit;
        const bool shuangpin = !dpScheme_.empty();
        for (const EngineOp& op : pendingOps_) {
            applyPendingOp(op, disp, pending, shuangpin, &unappliedPreedit);
        }
        if (baseOut != nullptr) *baseOut = disp.input;
        if (textOut != nullptr) *textOut = pending;
        disp.input += unappliedPreedit;
        disp.preedit += unappliedPreedit;
        if (dispOut != nullptr) *dispOut = std::move(disp);
    }

bool IbusEngineState::hasDpPending() const {
        std::string text;
        computeOptimistic(nullptr, nullptr, &text);
        return !text.empty() && optimisticComp().input.empty();
    }

chaoset::Comp IbusEngineState::optimisticComp() const {
        chaoset::Comp disp;
        computeOptimistic(&disp, nullptr, nullptr);
        return disp;
    }

std::string IbusEngineState::optimisticInput() const { return optimisticComp().input; }

void IbusEngineState::display(const chaoset::Comp& disp) {
        const std::string preedit = confirmed_ + disp.preedit;
        updatePreedit(preedit, !preedit.empty());
        updateLookupTable(disp);
    }

void IbusEngineState::renderOptimistic() { display(optimisticComp()); }

void IbusEngineState::renderPendingAfterCommit() {
        if (!pendingOps_.empty()) display(optimisticComp());
    }

void IbusEngineState::stopLostTimer() {
        if (lostTimerId_ != 0) { g_source_remove(lostTimerId_); lostTimerId_ = 0; }
    }

void IbusEngineState::startLostTimer() {
        if (lostTimerId_ != 0) return;
        auto* holder = new std::shared_ptr<ResultWake>(wake_);
        lostTimerId_ = g_timeout_add_full(G_PRIORITY_DEFAULT, 100,
            [](gpointer data) -> gboolean {
                auto* held = static_cast<std::shared_ptr<ResultWake>*>(data);
                IbusEngineState* self = (*held)->state.load();
                return (self != nullptr && self->onLostTimer()) ? G_SOURCE_CONTINUE : G_SOURCE_REMOVE;
            },
            holder, [](gpointer data) { delete static_cast<std::shared_ptr<ResultWake>*>(data); });
    }

bool IbusEngineState::onLostTimer() {
        if (!engineLost_) { lostTimerId_ = 0; return false; }
        const std::int64_t now = monotonicMs();
        if (now - engineLostMs_ >= kEngineGraceMs) {
            giveUpEngineState();
            lostTimerId_ = 0;
            return false;
        }
        scheduleRebuild();
        return engineLost_;
    }

void IbusEngineState::markEngineLost() {
        const std::int64_t now = monotonicMs();
        if (!engineLost_) { engineLost_ = true; engineLostMs_ = now; }
        startLostTimer();
        if (now - engineLostMs_ >= kEngineGraceMs) { giveUpEngineState(); return; }
        scheduleRebuild();
    }

void IbusEngineState::scheduleRebuild() {
        if (!engineLost_ || pump_ == nullptr) return;
        std::string base, text;
        computeOptimistic(nullptr, &base, &text);
        if (base.empty() && text.empty()) return;
        const std::int64_t now = monotonicMs();
        if (now - lastRebuildMs_ < kRebuildIntervalMs) return;
        lastRebuildMs_ = now;
        submitRebuild(base, text);
    }

void IbusEngineState::giveUpEngineState() {
        const chaoset::Comp disp = optimisticComp();
        std::string base, text;
        computeOptimistic(nullptr, &base, &text);
        const std::string raw = confirmed_ + (!text.empty() ? (base + text) : disp.input);
        resetPipeline(true);
        last_ = chaoset::Comp();
        hidePreedit();
        hideLookupTable();
        if (raw.empty()) return;
        confirmed_.clear();
        commit(raw);
    }

void IbusEngineState::restartFromLocalState() {
        std::string base, text;
        computeOptimistic(nullptr, &base, &text);
        last_ = chaoset::Comp();
        confirmed_.clear();
        resetPipeline(false);
        submitRebuild(base, text);
    }

void IbusEngineState::commitPunctFallback(const EngineOp& op) {
        chaoset::Comp dispBefore = last_;
        std::string pendingBefore = last_.dpPending, restPending;
        bool modifiedBefore = false;
        chaoset::Comp restComp;
        const bool shuangpin = !dpScheme_.empty();
        for (const EngineOp& pending : pendingOps_) {
            if (pending.seq < op.seq && applyPendingOp(pending, dispBefore, pendingBefore, shuangpin)) {
                modifiedBefore = true;
            } else if (pending.seq > op.seq) {
                applyPendingOp(pending, restComp, restPending, shuangpin);
            }
        }
        const std::string head = (!modifiedBefore && !dispBefore.candidates.empty())
            ? (dispBefore.candidates[0].text + pendingBefore) : (dispBefore.input + pendingBefore);
        const std::string all = confirmed_ + head + op.text;
        confirmed_.clear();
        last_ = chaoset::Comp();
        resetPipeline(false);
        if (!all.empty()) commit(all);
        if (!restPending.empty()) { submitRebuild("", restPending); renderOptimistic(); }
    }

void IbusEngineState::handleSelectPrefixFallback(const EngineResult& r, bool alreadyCommitted) {
        const std::string text = r.committed.empty() ? r.op.text : r.committed;
        if (!alreadyCommitted && text.empty()) {
            for (auto it = pendingOps_.begin(); it != pendingOps_.end(); ++it) {
                if (it->seq == r.op.seq) { pendingOps_.erase(it); break; }
            }
            if (!r.ok) { markEngineLost(); return; }
            if (optimisticInput().empty() && !hasDpPending()) {
                if (!confirmed_.empty()) {
                    const std::string all = confirmed_;
                    confirmed_.clear();
                    commit(all);
                } else {
                    hidePreedit(); hideLookupTable(); last_ = chaoset::Comp();
                }
            } else renderOptimistic();
            return;
        }
        chaoset::Comp restComp;
        std::string restPending;
        const bool shuangpin = !dpScheme_.empty();
        if (!r.op.sylls.empty()) {
            const std::string rem = chaoset::consumePrefixFallback(last_.input, r.op.sylls);
            if (!rem.empty()) {
                restComp.input = rem;
                restComp.preedit = rem;
                restPending = last_.dpPending;
            }
        }
        for (const EngineOp& pending : pendingOps_) {
            if (pending.seq != r.op.seq) applyPendingOp(pending, restComp, restPending, shuangpin);
        }
        const std::string all = confirmed_ + text;
        last_ = chaoset::Comp();
        resetPipeline(false);
        if (!alreadyCommitted) { confirmed_.clear(); commit(all); }
        if (!restComp.input.empty() || !restPending.empty()) {
            submitRebuild(restComp.input, restPending);
            renderOptimistic();
        }
    }

void IbusEngineState::applyResult(const EngineResult& r) {
    if (!r.ok) {
        // 失败结果不弹出乐观项：本键效果仍在本地显示里，等整串重建覆盖。
        if (r.op.kind == EngineOpKind::kSelectPrefix) {
            const std::string text = r.committed.empty() ? r.op.text : r.committed;
            const std::string all = confirmed_ + text;
            if (!text.empty()) confirmed_.clear();
            if (!text.empty()) commit(all);
            handleSelectPrefixFallback(r, !text.empty());
            return;
        }
        if (r.op.kind == EngineOpKind::kAssociateSelect) {
            if (!r.op.text.empty()) commit(r.op.text);
            exitAssociate();
            restartFromLocalState();
            return;
        }
        if (r.op.kind == EngineOpKind::kPunct) {
            commitPunctFallback(r.op);
            return;
        }
        if (r.op.kind == EngineOpKind::kUserDelete) {
            showToast("删除失败：daemon 不可达");
            return;
        }
        if (r.op.kind == EngineOpKind::kAssociate) return;
        if (r.op.kind == EngineOpKind::kReset || r.op.kind == EngineOpKind::kResetComposition) return;
        // input/backspace/page/rebuild：保留本地组词，进入失联宽限。
        markEngineLost();
        return;
    }

    // 成功结果按 seq 顺序到达：seq <= 本结果的乐观项都已被引擎覆盖。
    while (!pendingOps_.empty() && pendingOps_.front().seq <= r.op.seq) pendingOps_.pop_front();
    engineLost_ = false;
    stopLostTimer();
    switch (r.op.kind) {
        case EngineOpKind::kInput:
        case EngineOpKind::kRebuild:
        case EngineOpKind::kBackspace:
        case EngineOpKind::kBackspaceWord: {
            if (!r.comp.committed.empty()) {
                const std::string all = confirmed_ + r.comp.committed;
                confirmed_.clear();
                commit(all);
                renderPendingAfterCommit();
                return;
            }
            refresh(r.comp);
            if (optimisticInput().empty() && !hasDpPending()) {
                if (!confirmed_.empty()) {
                    const std::string all = confirmed_;
                    confirmed_.clear();
                    commit(all);
                } else {
                    hidePreedit();
                    hideLookupTable();
                    last_ = chaoset::Comp();
                }
            }
            return;
        }
        case EngineOpKind::kNextPage:
        case EngineOpKind::kPrevPage:
            refresh(r.comp);
            return;
        case EngineOpKind::kSelectPrefix: {
            if (r.comp.committed.empty()) {
                // 语义失败（越界等）：用壳侧已显示候选文本兜底上屏，绝不静默丢键。
                handleSelectPrefixFallback(r, false);
                return;
            }
            confirmed_ += r.comp.committed;
            selectedSylls_.insert(selectedSylls_.end(), r.op.sylls.begin(), r.op.sylls.end());
            refresh(r.comp);
            if (!last_.input.empty() || !last_.dpPending.empty()) return;
            std::string prev;
            for (std::size_t i = 0; i < selectedSylls_.size(); ++i) {
                if (i != 0) prev += "'";
                prev += selectedSylls_[i];
            }
            selectedSylls_.clear();
            const std::string text = confirmed_;
            confirmed_.clear();
            commit(text);
            if (!prev.empty()) enterAssociateForPrev(prev);
            renderPendingAfterCommit();
            return;
        }
        case EngineOpKind::kPunct: {
            const std::string all = confirmed_ + r.committed;
            confirmed_.clear();
            commit(all);
            renderPendingAfterCommit();
            return;
        }
        case EngineOpKind::kAssociateSelect: {
            const std::string text = r.committed.empty() ? r.op.text : r.committed;
            if (!text.empty()) commit(text);
            exitAssociate();
            if (!r.op.sylls.empty()) enterAssociate(r.op.sylls);
            renderPendingAfterCommit();
            return;
        }
        case EngineOpKind::kAssociate: {
            if (isComposing() || hasDpPending()) return;
            if (r.comp.candidates.empty()) {
                exitAssociate();
                return;
            }
            assoc_ = r.comp.candidates;
            assocPage_ = 0;
            lookupIndex_ = 0;
            showAssociateTable();
            return;
        }
        case EngineOpKind::kUserDelete: {
            if (!r.deleted) {
                showToast("未找到该词的学习记录");
                return;
            }
            if (r.op.associate) {
                assoc_.clear();
                assocPage_ = 0;
                hideLookupTable();
                if (!r.op.aux.empty()) enterAssociateForPrev(r.op.aux);
                return;
            }
            std::string base, text;
            computeOptimistic(nullptr, &base, &text);
            submitRebuild(base, text);
            return;
        }
        case EngineOpKind::kReset:
        case EngineOpKind::kResetComposition:
        case EngineOpKind::kProbe:
            return;
    }
}

void IbusEngineState::refresh(chaoset::Comp comp, int cursor) {
    last_ = std::move(comp);
    lookupIndex_ = cursor;
    // 结果回来时用户可能已经在按键里继续输入：必须按乐观视图重绘，否则每来一条
    // 结果都会把未应用的后续字母从预编辑里抹掉再逐个补回（可见回退闪烁）。
    if (pendingOps_.empty()) {
        display(last_);
    } else {
        display(optimisticComp());
    }
}

void scheduleResultsIdle(const std::shared_ptr<ResultWake>& wake) {
    if (wake->scheduled.exchange(true)) return;
    auto* holder = new std::shared_ptr<ResultWake>(wake);
    g_idle_add_full(G_PRIORITY_DEFAULT,
        [](gpointer data) -> gboolean {
            auto* held = static_cast<std::shared_ptr<ResultWake>*>(data);
            std::shared_ptr<ResultWake> local = *held;
            local->scheduled.store(false);
            IbusEngineState* self = local->state.load();
            if (self != nullptr) self->drainResults();
            return G_SOURCE_REMOVE;
        },
        holder, [](gpointer data) { delete static_cast<std::shared_ptr<ResultWake>*>(data); });
}

// applyResult 需要访问私有 UI helper；单独实现，保持与 Windows tip_async.cpp 同语义。

}  // namespace ibus_shell
