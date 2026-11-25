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
    daemon_.setDeadlineMs(kEngineRequestDeadlineMs);
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
    {
        std::lock_guard<std::mutex> lk(mu_);
        stop_ = true;
    }
    cv_.notify_all();
    {
        std::unique_lock<std::mutex> lk(mu_);
        // 有界等待：单个请求最长 kEngineRequestDeadlineMs，正常远小于该上限。
        if (!doneCv_.wait_for(lk, std::chrono::seconds(5), [this] { return done_; })) {
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
    // ensureReady 会懒启动 daemon（最长约 5s）。只允许在引擎线程上发生。
    if (daemon_.ensureReady()) {
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
    if (op.kind == EngineOpKind::kProbe) {
        result.ok = ensureConn(false);
        return result;
    }
    if (!ensureConn(false)) return result;  // 连不上：失败结果，主循环走失联宽限

    Session& session = daemon_.session();
    switch (op.kind) {
        case EngineOpKind::kInput:
            result.comp = session.input(op.text, op.halfWidth);  // 正常输入不带 base
            result.ok = result.comp.ok;
            break;
        case EngineOpKind::kRebuild:
            // base 传空串（非 nullptr）：先把引擎输入整串重建为空，再重放壳侧完整输入。
            result.comp = session.input(op.text, op.halfWidth, "");
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
    } else if (!daemon_.connected()) {
        // 传输层已断（写失败/EOF/部分读取）：快照置为不可用，下一键 fail-open/探测。
        connected_.store(false);
        connectRetryAfterMs_ = 0;
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
            if (stop_ && queue_.empty()) break;
            op = std::move(queue_.front());
            queue_.pop_front();
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
