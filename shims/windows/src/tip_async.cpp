// 异步引擎管线：操作入队、乐观预编辑回放、结果按序应用、daemon 失联宽限
// （Windows 壳，issue #149；I/O 线程见 engine_pump.*）。
//
// 不变量（由 checkuiparity「异步引擎管线」组断言）：
//   1. TSF 回调里绝不出现 session_/transport_ 直连；所有引擎操作只经
//      submitEngineOp/queuePendingOp，宿主 UI 线程不等待 daemon。
//   2. 结果按 seq 顺序应用；应用前先丢弃 seq <= 本结果的乐观项。
//   3. 失焦/取消一律 resetEnginePipeline（纪元++、清 pending、取消积压），
//      过期结果在 applyEngineResults 入口被丢弃，绝不写进新会话。
//   4. 显式选词（数字/空格/鼠标/联想）在 daemon 失败时用壳侧已显示文本兜底
//      上屏，绝不静默丢键；其余请求失败保留本地组词，2s 宽限后按原始串落盘。

#include "tip.hpp"
#include "shell_worker.hpp"  // postDaemonConnect（零等待入队；worker 负责拉起 daemon）

using namespace chaoset::win;

namespace {
constexpr DWORD kEngineGraceMs = 2000;      // daemon 不可用宽限（到点按原始串落盘）
constexpr DWORD kRebuildIntervalMs = 200;   // 失联重建请求节流
constexpr DWORD kProbeIntervalMs = 500;     // 失联探测节流
}  // namespace

void ChaosetTIP::ensureEnginePump() {
    if (pump_) return;
    pump_.reset(new EnginePump());
    pump_->warmConnect();  // 单次非阻塞探测：首个内容键不因线程尚未连上而 fail-open
    pump_->start(GetCurrentThreadId());
    // daemon 懒启动交给 worker（AppContainer 宿主里 worker 自己跳过 CreateProcess）。
    postDaemonConnect();
}

bool ChaosetTIP::engineAvailable() {
    ensureEnginePump();
    return pump_ != nullptr && pump_->connected();
}

// 失联时按节流投一个探测请求：daemon 恢复后连接状态由结果更新，下一次按键
// 即可正常进入组词（按键路径本身仍然零等待）。
void ChaosetTIP::probeEngineIfDown() {
    if (!pump_ || pump_->connected()) return;
    const DWORD now = ::GetTickCount();
    if (static_cast<LONG>(now - lastProbeTick_) < static_cast<LONG>(kProbeIntervalMs)) return;
    lastProbeTick_ = now;
    EngineOp op;
    op.kind = EngineOpKind::kProbe;
    op.epoch = engineEpoch_;
    op.seq = ++engineSeq_;
    pump_->post(op);  // 不压 pendingOps_：不产生任何 UI 效果
}

void ChaosetTIP::setEngineContext(ITfContext* pic) {
    if (pic == nullptr || engineCtx_ == pic) return;
    if (engineCtx_ != nullptr) engineCtx_->Release();
    engineCtx_ = pic;
    engineCtx_->AddRef();
}

std::uint64_t ChaosetTIP::queuePendingOp(EngineOp op) {
    op.epoch = engineEpoch_;
    op.seq = ++engineSeq_;
    pendingOps_.push_back(op);
    return op.seq;
}

void ChaosetTIP::dispatchPendingOp(std::uint64_t seq) {
    if (!pump_) return;
    for (const EngineOp& op : pendingOps_) {
        if (op.seq == seq) {
            pump_->post(op);
            return;
        }
    }
}

void ChaosetTIP::dropPendingOp(std::uint64_t seq) {
    for (auto it = pendingOps_.begin(); it != pendingOps_.end(); ++it) {
        if (it->seq == seq) {
            pendingOps_.erase(it);
            return;
        }
    }
}

std::uint64_t ChaosetTIP::submitEngineOp(EngineOp op) {
    ensureEnginePump();
    const std::uint64_t seq = queuePendingOp(op);
    dispatchPendingOp(seq);
    return seq;
}

// 乐观显示：最后一个已应用组词态 + 未应用输入/退格操作的回放。
// 只回放会改变预编辑的操作（字母/撇号/双拼 ';' 与退格），翻页/选词/标点
// 在结果回来前不改变预编辑。候选窗始终显示 comp_ 的候选（不关闭、不清空），
// 结果回来后按序刷新。
chaoset::Comp ChaosetTIP::optimisticComp() const {
    chaoset::Comp disp = comp_;
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

std::string ChaosetTIP::optimisticInput() const { return optimisticComp().input; }

void ChaosetTIP::onWorkerEngineResult() { applyEngineResults(); }

// 上屏（commit 结束合成）后，若用户已经在后续按键里继续输入，立即用乐观
// 预编辑重建新合成：不能让这些键等自己的引擎结果回来才显示。
void ChaosetTIP::renderAfterCommit(ITfContext* ctx) {
    if (pendingOps_.empty()) return;
    render(ctx);
}

void ChaosetTIP::applyEngineResults() {
    if (!pump_ || !inputActive_) return;  // 非激活零副作用：结果留到下次激活再丢
    EngineResult r;
    while (pump_->popResult(&r)) {
        if (r.op.epoch != engineEpoch_) continue;  // 过期（失焦/取消后的结果）
        if (r.op.kind == EngineOpKind::kProbe) {
            if (r.ok && engineLost_) scheduleRebuild();
            continue;
        }
        applyEngineResult(r);
    }
}

bool ChaosetTIP::applyCommitted(const std::string& text) {
    if (text.empty()) {
        endComposition();
        return false;
    }
    ITfContext* ctx = (compCtx_ != nullptr) ? compCtx_ : engineCtx_;
    const bool ok = commit(ctx, text);
    if (!ok && diagBudget()) {
        diagPrintf(L"tsf.log", "async commit failed len=%lu\n",
                   static_cast<unsigned long>(text.size()));
    }
    return ok;
}

// 显式上屏/标点失败时已在本地把文本写进文档，引擎侧组词状态不可信（可能已执行
// 也可能没执行）：作废在飞结果，并用**本地剩余输入**整串重建（不包含刚上屏的
// 内容），保证下一键从干净状态开始。必须在 commit 之后调用（comp_ 已清空、
// pendingOps_ 里只剩后续按键）。
void ChaosetTIP::restartEngineFromLocalState() {
    const std::string rest = optimisticInput();
    resetEnginePipeline(false);
    if (rest.empty()) return;
    EngineOp op;
    op.kind = EngineOpKind::kRebuild;
    op.text = rest;
    op.halfWidth = halfWidth_;
    submitEngineOp(op);
}

void ChaosetTIP::applyEngineResult(const EngineResult& r) {
    if (!r.ok) {
        // 失败结果**不**弹出乐观项：本键的效果仍在本地显示里，等其后的整串
        // 重建结果（seq 更大）再一并覆盖，绝不能凭空抹掉用户刚输入的拼音。
        // 显式选词/标点/联想失败：用壳侧已显示的文本兜底上屏，绝不静默丢键。
        if (r.op.kind == EngineOpKind::kSelectPrefix) {
            const std::string text = r.committed.empty() ? r.op.text : r.committed;
            const std::string all = confirmedPreedit_ + text;
            confirmedPreedit_.clear();
            if (!text.empty()) applyCommitted(all);
            restartEngineFromLocalState();
            return;
        }
        if (r.op.kind == EngineOpKind::kAssociateSelect) {
            if (!r.op.text.empty()) applyCommitted(r.op.text);
            clearAssociate();
            restartEngineFromLocalState();
            return;
        }
        if (r.op.kind == EngineOpKind::kPunct) {
            const std::string all = confirmedPreedit_ + r.op.text;
            confirmedPreedit_.clear();
            if (!r.op.text.empty()) applyCommitted(all);
            restartEngineFromLocalState();
            return;
        }
        if (r.op.kind == EngineOpKind::kUserDelete) {
            showToast(L"删除失败：daemon 不可达", true);
            return;
        }
        if (r.op.kind == EngineOpKind::kAssociate) return;
        if (r.op.kind == EngineOpKind::kReset || r.op.kind == EngineOpKind::kResetComposition) return;
        // input/backspace/page/rebuild：保留本地组词，进入失联宽限。
        markEngineLost(r);
        return;
    }

    // 成功结果按 seq 顺序到达：seq <= 本结果的乐观项都已被引擎覆盖（失败请求
    // 也已包含在重建整串里），出队后再应用本次结果。
    while (!pendingOps_.empty() && pendingOps_.front().seq <= r.op.seq) pendingOps_.pop_front();
    engineLost_ = false;
    ITfContext* ctx = engineCtx_;
    switch (r.op.kind) {
        case EngineOpKind::kInput:
        case EngineOpKind::kRebuild:
        case EngineOpKind::kBackspace:
        case EngineOpKind::kBackspaceWord: {
            comp_ = r.comp;
            syncDpScheme(comp_);
            if (!comp_.committed.empty()) {
                // 双拼 ';' 把「首选 + 标点」放进 committed：与已确认段拼接后一次落盘
                const std::string all = confirmedPreedit_ + comp_.committed;
                confirmedPreedit_.clear();
                commit(ctx, all);
                renderAfterCommit(ctx);
                return;
            }
            // 用**乐观输入**判定是否还有组词：结果回来前用户可能已经继续输入，
            // 只按 comp_.input 结束合成会让后续按键的预编辑闪断/丢失。
            if (optimisticComp().input.empty()) {
                if (!confirmedPreedit_.empty()) {
                    const std::string all = confirmedPreedit_;
                    confirmedPreedit_.clear();
                    comp_ = chaoset::Comp();
                    commit(ctx, all);
                } else {
                    endComposition();
                }
            } else {
                render(ctx);
            }
            return;
        }
        case EngineOpKind::kNextPage:
        case EngineOpKind::kPrevPage:
            comp_ = r.comp;
            syncDpScheme(comp_);
            render(ctx);
            return;
        case EngineOpKind::kSelectPrefix: {
            if (r.comp.committed.empty()) {
                // 语义失败（越界等）：用壳侧已显示候选文本兜底上屏，绝不静默丢键。
                const std::string all = confirmedPreedit_ + r.op.text;
                confirmedPreedit_.clear();
                if (!r.op.text.empty()) commit(ctx, all);
                renderAfterCommit(ctx);
                restartEngineFromLocalState();
                return;
            }
            syncDpScheme(r.comp);
            confirmedPreedit_ += r.comp.committed;
            comp_ = r.comp;
            if (!comp_.input.empty()) {
                render(ctx);
                return;
            }
            comp_ = chaoset::Comp();
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(ctx, all);
            if (!r.op.syllables.empty()) enterAssociate(r.op.syllables);
            renderAfterCommit(ctx);
            return;
        }
        case EngineOpKind::kPunct: {
            const std::string all = confirmedPreedit_ + r.committed;
            confirmedPreedit_.clear();
            commit(ctx, all);
            renderAfterCommit(ctx);
            return;
        }
        case EngineOpKind::kAssociateSelect: {
            const std::string text = r.committed.empty() ? r.op.text : r.committed;
            if (!text.empty() && applyCommitted(text)) {
                if (!r.op.syllables.empty()) enterAssociate(r.op.syllables);
                renderAfterCommit(ctx);
            }
            return;
        }
        case EngineOpKind::kAssociate: {
            if (r.comp.candidates.empty()) {
                clearAssociate();
                return;
            }
            syncDpScheme(r.comp);
            assoc_ = r.comp;
            assocSylls_ = r.op.syllables;
            assocPage_ = 0;
            showAssociatePage();
            return;
        }
        case EngineOpKind::kUserDelete: {
            if (!r.deleted) {
                showToast(L"未找到该词的学习记录", true);
                return;
            }
            if (r.op.associate) {
                // 联想态：收起旧面板，按同一联想来源音节重查
                assoc_ = chaoset::Comp();
                assocPage_ = 0;
                candidateWnd_.hide();
                if (!r.op.aux.empty()) {
                    EngineOp op;
                    op.kind = EngineOpKind::kAssociate;
                    op.syllables = r.op.aux;
                    submitEngineOp(op);
                }
                return;
            }
            // 组词态：整串重建刷新候选（不再同步重放 input）
            if (!optimisticInput().empty()) {
                EngineOp op;
                op.kind = EngineOpKind::kRebuild;
                op.text = optimisticInput();
                op.halfWidth = halfWidth_;
                submitEngineOp(op);
            }
            return;
        }
        case EngineOpKind::kReset:
        case EngineOpKind::kResetComposition:
            syncDpScheme(r.comp);
            return;
        default:
            return;
    }
}

void ChaosetTIP::markEngineLost(const EngineResult& /*r*/) {
    if (!engineLost_) {
        engineLost_ = true;
        engineLostTick_ = ::GetTickCount();
        if (diagBudget()) diagPrintf(L"tsf.log", "engine lost (async)\n");
    }
    postDaemonConnect();  // 零等待：worker 负责拉起 daemon（AC 宿主自行跳过）
    const DWORD now = ::GetTickCount();
    if (static_cast<LONG>(now - engineLostTick_) >= static_cast<LONG>(kEngineGraceMs)) {
        giveUpEngineState();
        return;
    }
    scheduleRebuild();
}

void ChaosetTIP::scheduleRebuild() {
    if (!engineLost_ || !pump_) return;
    const DWORD now = ::GetTickCount();
    if (static_cast<LONG>(now - lastRebuildTick_) < static_cast<LONG>(kRebuildIntervalMs)) return;
    lastRebuildTick_ = now;
    EngineOp op;
    op.kind = EngineOpKind::kRebuild;
    op.text = optimisticInput();  // 壳侧已知整串：重建后逐键等价
    op.halfWidth = halfWidth_;
    submitEngineOp(op);
}

// 宽限期到（daemon 一直不可用）：本地内容按原始串落盘，一个键都不丢，然后
// 清空管线（纪元++ 丢弃在飞结果，取消积压并断开连接）。
void ChaosetTIP::giveUpEngineState() {
    const chaoset::Comp disp = optimisticComp();
    const std::string raw = confirmedPreedit_ + disp.input;
    ITfContext* ctx = (compCtx_ != nullptr) ? compCtx_ : engineCtx_;
    resetEnginePipeline(true);
    if (raw.empty()) {
        endComposition();
        return;
    }
    confirmedPreedit_.clear();
    comp_ = chaoset::Comp();
    if (!commit(ctx, raw)) endComposition();
}

// 管线重置：纪元递增（在飞结果全部作废）+ 清空壳侧待应用项 + 丢弃引擎线程
// 尚未开始执行的积压，最后投一个引擎复位请求。
//   newSession=true  → kReset：组词与学习上下文一起清（失焦/停用/合成被宿主终止）
//   newSession=false → kResetComposition：只丢组词，保留 prev/history（Esc/透传）
void ChaosetTIP::resetEnginePipeline(bool newSession) {
    ++engineEpoch_;
    pendingOps_.clear();
    engineLost_ = false;
    engineLostTick_ = 0;
    lastRebuildTick_ = 0;
    if (!pump_) return;
    pump_->clearPending();
    EngineOp op;
    op.kind = newSession ? EngineOpKind::kReset : EngineOpKind::kResetComposition;
    op.epoch = engineEpoch_;
    op.seq = ++engineSeq_;
    pump_->post(op);
}
