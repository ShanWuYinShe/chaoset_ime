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

void IbusEngineState::resetPipeline(bool newSession) {
        ++epoch_;
        pendingOps_.clear();
        engineLost_ = false;
        if (pump_ == nullptr) return;
        pump_->clearPending();
        EngineOp op;
        op.kind = newSession ? EngineOpKind::kReset : EngineOpKind::kResetComposition;
        op.epoch = epoch_;
        op.seq = ++seq_;
        pump_->post(std::move(op));
    }

chaoset::Comp IbusEngineState::optimisticComp() const {
        chaoset::Comp disp = last_;
        std::string pending;
        const bool shuangpin = !dpScheme_.empty();
        for (const EngineOp& op : pendingOps_) {
            switch (op.kind) {
                case EngineOpKind::kInput:
                    pending += op.text;
                    break;
                case EngineOpKind::kBackspace:
                    backspaceLocally(disp, pending, shuangpin, false);
                    break;
                case EngineOpKind::kBackspaceWord:
                    backspaceLocally(disp, pending, shuangpin, true);
                    break;
                default:
                    break;
            }
        }
        disp.preedit += pending;
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

void IbusEngineState::markEngineLost() {
        const std::int64_t now = monotonicMs();
        if (!engineLost_) {
            engineLost_ = true;
            engineLostMs_ = now;
        }
        if (now - engineLostMs_ >= kEngineGraceMs) {
            giveUpEngineState();
            return;
        }
        scheduleRebuild();
    }

void IbusEngineState::scheduleRebuild() {
        if (!engineLost_ || pump_ == nullptr) return;
        const std::int64_t now = monotonicMs();
        if (now - lastRebuildMs_ < kRebuildIntervalMs) return;
        lastRebuildMs_ = now;
        EngineOp op;
        op.kind = EngineOpKind::kRebuild;
        op.text = optimisticInput();
        op.halfWidth = halfWidth_;
        submit(std::move(op));
    }

void IbusEngineState::giveUpEngineState() {
        const chaoset::Comp disp = optimisticComp();
        const std::string raw = confirmed_ + disp.input;
        resetPipeline(true);
        if (raw.empty()) {
            hidePreedit();
            hideLookupTable();
            last_ = chaoset::Comp();
            return;
        }
        confirmed_.clear();
        commit(raw);
    }

void IbusEngineState::restartFromLocalState() {
        const std::string rest = optimisticInput();
        resetPipeline(false);
        if (rest.empty()) return;
        EngineOp op;
        op.kind = EngineOpKind::kRebuild;
        op.text = rest;
        op.halfWidth = halfWidth_;
        submit(std::move(op));
    }

void IbusEngineState::applyResult(const EngineResult& r) {
    if (!r.ok) {
        // 失败结果不弹出乐观项：本键效果仍在本地显示里，等整串重建覆盖。
        if (r.op.kind == EngineOpKind::kSelectPrefix) {
            const std::string text = r.committed.empty() ? r.op.text : r.committed;
            const std::string all = confirmed_ + text;
            confirmed_.clear();
            if (!text.empty()) commit(all);
            restartFromLocalState();
            return;
        }
        if (r.op.kind == EngineOpKind::kAssociateSelect) {
            if (!r.op.text.empty()) commit(r.op.text);
            exitAssociate();
            restartFromLocalState();
            return;
        }
        if (r.op.kind == EngineOpKind::kPunct) {
            const std::string all = confirmed_ + r.op.text;
            confirmed_.clear();
            if (!r.op.text.empty()) commit(all);
            restartFromLocalState();
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
            if (optimisticInput().empty()) {
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
                const std::string all = confirmed_ + r.op.text;
                confirmed_.clear();
                if (!r.op.text.empty()) commit(all);
                restartFromLocalState();
                return;
            }
            confirmed_ += r.comp.committed;
            selectedSylls_.insert(selectedSylls_.end(), r.op.sylls.begin(), r.op.sylls.end());
            refresh(r.comp);
            if (!last_.input.empty()) return;
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
            return;
        }
        case EngineOpKind::kAssociate: {
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
            const std::string rest = optimisticInput();
            if (!rest.empty()) {
                EngineOp op;
                op.kind = EngineOpKind::kRebuild;
                op.text = rest;
                op.halfWidth = halfWidth_;
                submit(std::move(op));
            }
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

;

// 主循环侧的唤醒：把结果队列交给 drainResults（只在主循环线程执行）。


// applyResult 需要访问私有 UI helper；单独实现，保持与 Windows tip_async.cpp 同语义。

}  // namespace ibus_shell
