// Linux 异步引擎线程实现（设计见 engine_pump.hpp）。
//
// 线程模型：主循环（IBus）只 post/pop，工作线程只碰 daemon 与本类队列；
// 结果通过构造时传入的 notify 回调让主循环来取（main.cpp 用 g_idle 调度）。
// 这里不 include 任何 ibus 头，也绝不调用任何 ibus API。
#include "engine_pump.hpp"

#include <algorithm>
#include <chrono>
#include <utility>

namespace chaoset {

namespace {

std::int64_t nowMs() {
    return std::chrono::duration_cast<std::chrono::milliseconds>(
               std::chrono::steady_clock::now().time_since_epoch())
        .count();
}

constexpr std::size_t kMaxQueuedResults = 256;

}  // namespace

EnginePump::EnginePump(Daemon::Options options, Notify notify)
    : daemon_(std::move(options)), notify_(std::move(notify)) {
    // 无界异步读取：poll 等待响应或 abort 唤醒，不再使用 200ms 硬等待超时。
    daemon_.setDeadlineMs(0);
}

EnginePump::~EnginePump() {
    if (thread_.joinable()) stop();  // 正常路径调用方已 stop 并 join；这里只兜底
}

void EnginePump::warmConnect() {
    if (daemon_.connected()) {
        connected_.store(true);
        return;
    }
    if (daemon_.tryConnectOnce()) connected_.store(true);
}

void EnginePump::start() {
    if (thread_.joinable()) return;
    thread_ = std::thread([this] { run(); });
}

void EnginePump::stop() {
    if (!thread_.joinable()) {
        joinOk_ = true;
        return;
    }
    daemon_.abort();
    {
        std::lock_guard<std::mutex> lk(mu_);
        stop_ = true;
        queue_.clear();
    }
    cv_.notify_all();
    {
        std::unique_lock<std::mutex> lk(mu_);
        if (!doneCv_.wait_for(lk, std::chrono::milliseconds(kEngineStopWaitMs), [this] { return done_; })) {
            joinOk_ = false;
            return;  // 线程仍引用 this：调用方必须故意泄漏，绝不能销毁
        }
    }
    thread_.join();
    joinOk_ = true;
}

void EnginePump::post(EngineOp op) {
    {
        std::lock_guard<std::mutex> lk(mu_);
        queue_.push_back(std::move(op));
    }
    cv_.notify_one();
}

void EnginePump::clearPending() {
    std::lock_guard<std::mutex> lk(mu_);
    queue_.clear();
}

bool EnginePump::popResult(EngineResult* out) {
    if (out == nullptr) return false;
    std::lock_guard<std::mutex> lk(mu_);
    if (results_.empty()) return false;
    *out = std::move(results_.front());
    results_.pop_front();
    return true;
}

bool EnginePump::ensureConn(bool bypassCooldown) {
    if (daemon_.connected()) {
        connected_.store(true);
        return true;
    }
    const std::int64_t now = nowMs();
    if (!bypassCooldown && now < connectRetryAfterMs_) return false;
    // 只做单次非阻塞连接与冷却内异步懒启动，绝不休眠等待 4.8s 卡死管线与宽限。
    if (daemon_.tryConnectOrSpawn()) {
        connected_.store(true);
        return true;
    }
    connectRetryAfterMs_ = nowMs() + kEngineReconnectCooldownMs;
    connected_.store(false);
    return false;
}

EngineResult EnginePump::exec(const EngineOp& op) {
    EngineResult result;
    result.op = op;
    const bool isStatefulComposition =
        op.kind == EngineOpKind::kInput || op.kind == EngineOpKind::kBackspace ||
        op.kind == EngineOpKind::kBackspaceWord || op.kind == EngineOpKind::kNextPage ||
        op.kind == EngineOpKind::kPrevPage || op.kind == EngineOpKind::kSelectPrefix ||
        op.kind == EngineOpKind::kPunct;
    if (isStatefulComposition && sessionDesynced_) {
        result.ok = false;
        result.connectionLost = true;
        connected_.store(daemon_.connected());
        return result;
    }
    const bool bypass = (op.kind == EngineOpKind::kRebuild || op.kind == EngineOpKind::kProbe ||
                         op.kind == EngineOpKind::kReset ||
                         op.kind == EngineOpKind::kResetComposition);
    if (op.kind == EngineOpKind::kProbe) {
        result.ok = ensureConn(true);
        return result;
    }
    if (!ensureConn(bypass)) {  // 连不上：失败结果，主循环走失联宽限
        if (isStatefulComposition && (hadCompositionOps_ || op.kind == EngineOpKind::kInput)) {
            sessionDesynced_ = true;
        }
        result.connectionLost = true;
        return result;
    }
    const std::uint64_t gen = daemon_.connGen();
    if (isStatefulComposition) {
        if (!hadCompositionOps_) {
            syncedConnGen_ = gen;
        } else if (gen != syncedConnGen_) {
            sessionDesynced_ = true;
            result.ok = false;
            result.connectionLost = true;
            return result;
        }
    }

    Session& session = daemon_.session();
    switch (op.kind) {
        case EngineOpKind::kInput:
            result.comp = session.input(op.text, op.halfWidth);  // 正常输入不带 base
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kRebuild:
            // base=op.base（已解码/已确认前缀），text=op.text（未解码按键）。
            result.comp = session.input(op.text, op.halfWidth, op.base.c_str());
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kBackspace:
            result.comp = session.backspace();
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kBackspaceWord:
            result.comp = session.backspaceWord();
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kNextPage:
            result.comp = session.nextPage();
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kPrevPage:
            result.comp = session.prevPage();
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kSelectPrefix:
            result.ok = session.selectPrefix(op.index, &result.comp);
            result.committed = result.comp.committed;
            break;
        case EngineOpKind::kPunct:
            result.ok = session.punct(op.text, op.halfWidth, &result.committed);
            break;
        case EngineOpKind::kAssociateSelect:
            result.ok = session.associateSelect(op.text, op.sylls, &result.committed);
            break;
        case EngineOpKind::kAssociate:
            result.ok = session.associate(op.sylls, &result.comp);
            break;
        case EngineOpKind::kUserDelete:
            result.ok = session.userDelete(op.text, op.sylls, &result.deleted);
            break;
        case EngineOpKind::kReset:
            result.comp = session.reset();
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kResetComposition:
            result.comp = session.resetComposition();
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kProbe:
            break;
    }
    if (result.ok) {
        connected_.store(true);
        if (op.kind == EngineOpKind::kReset || op.kind == EngineOpKind::kResetComposition) {
            syncedConnGen_ = daemon_.connGen();
            sessionDesynced_ = false;
            hadCompositionOps_ = false;
        } else if (op.kind == EngineOpKind::kRebuild) {
            syncedConnGen_ = daemon_.connGen();
            sessionDesynced_ = false;
            hadCompositionOps_ =
                (!result.comp.input.empty() || !result.comp.dpPending.empty()) &&
                result.comp.committed.empty();
        } else if (op.kind == EngineOpKind::kInput || op.kind == EngineOpKind::kBackspace ||
                   op.kind == EngineOpKind::kBackspaceWord ||
                   op.kind == EngineOpKind::kSelectPrefix) {
            hadCompositionOps_ =
                (!result.comp.input.empty() || !result.comp.dpPending.empty()) &&
                result.comp.committed.empty();
        } else if (op.kind == EngineOpKind::kPunct) {
            hadCompositionOps_ = false;
        }
    } else {
        const bool ioFailure = !daemon_.connected() || session.lastCallWasTransportError();
        if (op.kind == EngineOpKind::kReset || op.kind == EngineOpKind::kResetComposition) {
            daemon_.closeConn();
            connected_.store(false);
            connectRetryAfterMs_ = nowMs() + kEngineReconnectCooldownMs;
            sessionDesynced_ = false;
            hadCompositionOps_ = false;
            result.connectionLost = true;
        } else {
            if (!daemon_.connected()) {
                connected_.store(false);
                connectRetryAfterMs_ = nowMs() + kEngineReconnectCooldownMs;
            }
            if (ioFailure) {
                result.connectionLost = true;
                if (isStatefulComposition &&
                    (hadCompositionOps_ || op.kind == EngineOpKind::kInput)) {
                    sessionDesynced_ = true;
                }
                if (op.kind == EngineOpKind::kRebuild) sessionDesynced_ = true;
            }
        }
    }
    return result;
}

void EnginePump::pushResult(EngineResult result) {
    {
        std::lock_guard<std::mutex> lk(mu_);
        results_.push_back(std::move(result));
        while (results_.size() > kMaxQueuedResults) results_.pop_front();
    }
    if (notify_) notify_();  // 只唤醒主循环；不在这里碰任何 UI
}

void EnginePump::run() {
    // 启动即尝试一次连接：激活后的首个按键通常落在已连接状态。
    ensureConn(true);
    for (;;) {
        EngineOp op;
        {
            std::unique_lock<std::mutex> lk(mu_);
            cv_.wait(lk, [this] { return stop_ || !queue_.empty(); });
            if (stop_) break;
            op = std::move(queue_.front());
            queue_.pop_front();
            if (!shuangpinEnabled_.load() && op.kind == EngineOpKind::kInput) {
                while (!queue_.empty() && queue_.front().kind == EngineOpKind::kInput &&
                       queue_.front().epoch == op.epoch &&
                       queue_.front().halfWidth == op.halfWidth) {
                    op.text += queue_.front().text;
                    op.seq = queue_.front().seq;
                    queue_.pop_front();
                }
            }
        }
        EngineResult result = exec(op);
        pushResult(std::move(result));
    }
    {
        std::lock_guard<std::mutex> lk(mu_);
        done_ = true;
    }
    doneCv_.notify_all();
}

}  // namespace chaoset
