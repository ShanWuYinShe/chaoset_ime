// 每 TIP 一个异步引擎线程：独占 daemon 连接、FIFO 执行、结果回投。
// 设计说明见 engine_pump.hpp（issue #149）。

#include "engine_pump.hpp"

#include <utility>
#include <vector>

#include "tip_thread.hpp"

using namespace chaoset::win;

namespace chaoset::win {
volatile LONG g_liveEnginePumps = 0;
}  // namespace chaoset::win

namespace {

void parseSyllables(const std::string& joined, std::vector<std::string>* out) {
    out->clear();
    std::string cur;
    for (char c : joined) {
        if (c == '\'') {
            if (!cur.empty()) out->push_back(cur);
            cur.clear();
        } else {
            cur.push_back(c);
        }
    }
    if (!cur.empty()) out->push_back(cur);
}

}  // namespace

EnginePump::EnginePump() {
    // 引擎线程允许「连接 + 重试」，但每次请求仍有明确截止（200ms）：截止只在
    // 本线程生效，宿主 UI 线程永不等待。
    transport_.setAutoConnect(true);
    transport_.setDeadlineMs(kEngineRequestDeadlineMs);
}

EnginePump::~EnginePump() { stop(); }

void EnginePump::warmConnect() {
    // 调用线程（TIP 线程）上只做一次非阻塞探测：不等待、不重试。
    if (transport_.isConnected()) {
        connected_.store(true);
        return;
    }
    if (transport_.tryConnectOnce()) connected_.store(true);
}

void EnginePump::start(DWORD tid) {
    {
        std::lock_guard<std::mutex> lk(mu_);
        if (alive_.load()) {
            tid_ = tid;
            return;
        }
        tid_ = tid;
        stop_ = false;
        alive_.store(true);
    }
    InterlockedIncrement(&g_liveEnginePumps);
    thread_ = ::CreateThread(nullptr, 0, &EnginePump::threadProc, this, 0, nullptr);
    if (thread_ == nullptr) {
        InterlockedDecrement(&g_liveEnginePumps);
        alive_.store(false);
        return;
    }
}

void EnginePump::stop() {
    HANDLE th = nullptr;
    {
        std::lock_guard<std::mutex> lk(mu_);
        if (thread_ == nullptr && !alive_.load()) {
            // 从未启动（或启动失败）：没有线程要 join，对象可以安全销毁。
            joinOk_ = true;
            shutdown();
            return;
        }
        stop_ = true;
        queue_.clear();
        cv_.notify_all();
        th = thread_;
        thread_ = nullptr;
    }
    if (th != nullptr) {
        // 有界等待：当前请求最长 kEngineRequestDeadlineMs，正常远小于该上限。
        const DWORD w = ::WaitForSingleObject(th, 4 * kEngineRequestDeadlineMs);
        ::CloseHandle(th);
        joinOk_ = (w == WAIT_OBJECT_0);
        if (!joinOk_) return;  // 线程仍引用本对象：调用方必须故意不销毁
    } else {
        joinOk_ = true;
    }
    shutdown();
}

std::uint64_t EnginePump::post(EngineOp op) {
    std::lock_guard<std::mutex> lk(mu_);
    op.seq = ++nextSeq_;
    const std::uint64_t seq = op.seq;
    queue_.push_back(std::move(op));
    cv_.notify_one();
    return seq;
}

bool EnginePump::popResult(EngineResult* out) {
    std::lock_guard<std::mutex> lk(mu_);
    if (results_.empty()) return false;
    *out = std::move(results_.front());
    results_.pop_front();
    return true;
}

void EnginePump::clearPending() {
    std::lock_guard<std::mutex> lk(mu_);
    queue_.clear();
}

DWORD WINAPI EnginePump::threadProc(LPVOID self) {
    static_cast<EnginePump*>(self)->run();
    InterlockedDecrement(&g_liveEnginePumps);
    return 0;
}

void EnginePump::run() {
    // 启动即尝试一次非阻塞连接：激活/首次按键通常早于真正输入，首个内容键
    // 就能落在已连接状态（不再 fail-open 成英文）。
    if (ensureConn(true)) connected_.store(true);
    for (;;) {
        EngineOp op;
        {
            std::unique_lock<std::mutex> lk(mu_);
            cv_.wait(lk, [this] { return stop_ || !queue_.empty(); });
            if (stop_) break;
            if (queue_.empty()) continue;
            op = std::move(queue_.front());
            queue_.pop_front();
        }
        EngineResult r = exec(op);
        {
            std::lock_guard<std::mutex> lk(mu_);
            results_.push_back(std::move(r));
            // 有界：宿主窗口已销毁（未激活/卸载）时没人取结果，不能无限增长。
            while (results_.size() > 256) results_.pop_front();
        }
        if (tid_ != 0) postTipThread(tid_, kTipEngineResultMsg, 0, 0);
    }
    shutdown();
    alive_.store(false);
}

void EnginePump::shutdown() {
    transport_.closeConn();
    connected_.store(false);
}

bool EnginePump::ensureConn(bool bypassCooldown) {
    if (transport_.isConnected()) return true;
    const DWORD now = ::GetTickCount();
    if (!bypassCooldown && static_cast<LONG>(now - connectRetryAfter_) < 0) return false;
    if (transport_.tryConnectOnce()) return true;
    connectRetryAfter_ = now + kEngineReconnectCooldownMs;
    return false;
}

EngineResult EnginePump::exec(const EngineOp& op) {
    EngineResult r;
    r.op = op;
    const bool isStatefulComposition =
        op.kind == EngineOpKind::kInput || op.kind == EngineOpKind::kBackspace ||
        op.kind == EngineOpKind::kBackspaceWord || op.kind == EngineOpKind::kNextPage ||
        op.kind == EngineOpKind::kPrevPage || op.kind == EngineOpKind::kSelectPrefix ||
        op.kind == EngineOpKind::kPunct;
    // 会话已失同步（曾断连或请求超时）：禁止在空会话/未知会话上跑增量操作，
    // 必须等 kRebuild 用 base+text 重建或 kReset/kResetComposition 复位。
    if (isStatefulComposition && sessionDesynced_) {
        r.ok = false;
        r.connectionLost = true;
        connected_.store(transport_.isConnected());
        return r;
    }
    // kRebuild/kProbe 负责探测恢复：不受冷却限制；其余请求在冷却期内快速失败。
    const bool bypass = (op.kind == EngineOpKind::kRebuild || op.kind == EngineOpKind::kProbe ||
                         op.kind == EngineOpKind::kReset ||
                         op.kind == EngineOpKind::kResetComposition);
    if (!ensureConn(bypass)) {
        if (isStatefulComposition && (hadCompositionOps_ || op.kind == EngineOpKind::kInput)) {
            sessionDesynced_ = true;
        }
        r.ok = false;
        r.connectionLost = true;
        connected_.store(false);
        return r;
    }
    const std::uint64_t gen = transport_.connGen();
    if (isStatefulComposition) {
        if (!hadCompositionOps_) {
            syncedConnGen_ = gen;
        } else if (gen != syncedConnGen_) {
            // 连接已换代（kProbe/kUserDelete 等重连到全新空会话）：拒绝跑增量操作。
            sessionDesynced_ = true;
            r.ok = false;
            r.connectionLost = true;
            connected_.store(true);
            return r;
        }
    }
    bool ok = false;
    std::vector<std::string> sylls;
    switch (op.kind) {
        case EngineOpKind::kInput: {
            chaoset::Comp c = session_.input(op.text, op.halfWidth);
            ok = c.ok;
            r.comp = c;
            break;
        }
        case EngineOpKind::kRebuild: {
            // op.base 是已确认/已解码前缀（双拼不二次解码），op.text 是未解码按键。
            chaoset::Comp c = session_.input(op.text, op.halfWidth, op.base.c_str());
            ok = c.ok;
            r.comp = c;
            break;
        }
        case EngineOpKind::kBackspace: {
            chaoset::Comp c = session_.backspace();
            ok = c.ok;
            r.comp = c;
            break;
        }
        case EngineOpKind::kBackspaceWord: {
            chaoset::Comp c = session_.backspaceWord();
            ok = c.ok;
            r.comp = c;
            break;
        }
        case EngineOpKind::kNextPage: {
            chaoset::Comp c = session_.nextPage();
            ok = c.ok;
            r.comp = c;
            break;
        }
        case EngineOpKind::kPrevPage: {
            chaoset::Comp c = session_.prevPage();
            ok = c.ok;
            r.comp = c;
            break;
        }
        case EngineOpKind::kSelectPrefix: {
            chaoset::Comp c;
            ok = session_.selectPrefix(op.index, &c);
            r.comp = c;
            break;
        }
        case EngineOpKind::kPunct: {
            std::string committed;
            ok = session_.punct(op.text, op.halfWidth, &committed);
            r.committed = committed;
            break;
        }
        case EngineOpKind::kAssociateSelect: {
            parseSyllables(op.syllables, &sylls);
            std::string committed;
            ok = session_.associateSelect(op.text, sylls, &committed);
            r.committed = committed;
            break;
        }
        case EngineOpKind::kAssociate: {
            parseSyllables(op.syllables, &sylls);
            chaoset::Comp c;
            ok = session_.associate(sylls, &c);
            r.comp = c;
            break;
        }
        case EngineOpKind::kUserDelete: {
            parseSyllables(op.syllables, &sylls);
            bool deleted = false;
            ok = session_.userDelete(op.text, sylls, &deleted);
            r.deleted = deleted;
            break;
        }
        case EngineOpKind::kReset: {
            chaoset::Comp c = session_.reset();
            ok = c.ok;
            r.comp = c;
            break;
        }
        case EngineOpKind::kResetComposition: {
            chaoset::Comp c = session_.resetComposition();
            ok = c.ok;
            r.comp = c;
            break;
        }
        case EngineOpKind::kProbe:
            // 只验证连接可用（ensureConn 已成功走到这里）：不碰 daemon 会话。
            ok = true;
            break;
        default:
            ok = false;
            break;
    }
    if (!ok) {
        // 区分「连接/超时」与「语义失败（候选越界等）」：前者必须断连并让壳
        // 整串重建（daemon 会话已不可信）；后者连接仍好，结果按各 op 语义处理。
        const bool ioFailure = !transport_.isConnected() || transport_.lastIoWasTimeout() ||
                               session_.lastCallWasTransportError();
        if (op.kind == EngineOpKind::kReset || op.kind == EngineOpKind::kResetComposition) {
            // reset 失败若不断连，下轮会带着上一会话残留输入继续组词；直接关闭连接
            // 保证下次是全新空会话。
            transport_.closeConn();
            connected_.store(false);
            connectRetryAfter_ = ::GetTickCount() + kEngineReconnectCooldownMs;
            sessionDesynced_ = false;
            hadCompositionOps_ = false;
            r.connectionLost = true;
        } else if (ioFailure) {
            r.connectionLost = true;
            transport_.closeConn();
            connected_.store(false);
            connectRetryAfter_ = ::GetTickCount() + kEngineReconnectCooldownMs;
            if (isStatefulComposition && (hadCompositionOps_ || op.kind == EngineOpKind::kInput)) {
                sessionDesynced_ = true;
            }
            if (op.kind == EngineOpKind::kRebuild) sessionDesynced_ = true;
        }
    } else {
        connected_.store(true);
        if (op.kind == EngineOpKind::kReset || op.kind == EngineOpKind::kResetComposition) {
            syncedConnGen_ = transport_.connGen();
            sessionDesynced_ = false;
            hadCompositionOps_ = false;
        } else if (op.kind == EngineOpKind::kRebuild) {
            syncedConnGen_ = transport_.connGen();
            sessionDesynced_ = false;
            hadCompositionOps_ = (!r.comp.input.empty() || !r.comp.dpPending.empty()) &&
                                 r.comp.committed.empty();
        } else if (op.kind == EngineOpKind::kInput || op.kind == EngineOpKind::kBackspace ||
                   op.kind == EngineOpKind::kBackspaceWord ||
                   op.kind == EngineOpKind::kSelectPrefix) {
            hadCompositionOps_ = (!r.comp.input.empty() || !r.comp.dpPending.empty()) &&
                                 r.comp.committed.empty();
        } else if (op.kind == EngineOpKind::kPunct) {
            hadCompositionOps_ = false;
        }
    }
    r.ok = ok;
    return r;
}
