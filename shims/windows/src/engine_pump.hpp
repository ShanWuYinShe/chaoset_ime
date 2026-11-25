// 每 TIP 一个异步引擎线程（Windows 壳叶子模块，issue #149）。
//
// 背景：TSF 回调在宿主 UI 线程上同步执行，旧的按键路径在回调里同步等 daemon
// 应答（30ms 截止）。截止再大也只是「把误判换成更久的阻塞」，弱机器上仍是
// 卡宿主。本模块把引擎 I/O 整体移出宿主线程：
//   * TIP 线程只入队（post）与读结果（popResult），绝不等待；
//   * 线程按 FIFO 在**独占连接**上执行操作（daemon 的组词会话按连接隔离，
//     同一进程多个 TIP 不能共用连接，否则各自的输入串会串在一起）；
//   * 结果装箱后由 TIP 线程经 message-only 窗口（kTipEngineResultMsg）取走。
//
// 失败语义：连接不上/请求超时都返回 ok=false + connectionLost=true，并断开
// 连接——daemon 会话随连接销毁，壳侧下一次 kRebuild 用整串重建恢复状态，绝不
// 出现「半个请求已生效」的猜测。
//
// 模块边界：只依赖 core.hpp/transport.hpp，不 include tip.hpp；反向依赖由
// checkuiparity「异步引擎管线」组断言。

#ifndef CHAOSET_WIN_ENGINE_PUMP_HPP_
#define CHAOSET_WIN_ENGINE_PUMP_HPP_

#include <windows.h>

#include <atomic>
#include <condition_variable>
#include <cstdint>
#include <deque>
#include <mutex>
#include <string>

#include "core.hpp"
#include "transport.hpp"

namespace chaoset::win {

// 引擎操作码。新增/删除种类必须同步 tip_async.cpp 的结果应用与
// checkuiparity 的断言（未知种类在 applyEngineResult 里会被丢弃）。
enum class EngineOpKind {
    kInput,       // 追加按键（字母 / 双拼 ';' / 撇号）
    kRebuild,     // 整串重建：input(text, base="")，连接重建后恢复组词
    kBackspace,   // 逐字符回退
    kBackspaceWord,  // 按音节回退
    kNextPage,    // 翻页
    kPrevPage,
    kSelectPrefix,   // 渐进上屏（数字/空格/鼠标点选）
    kPunct,       // 标点键
    kAssociateSelect,  // 联想候选上屏
    kAssociate,   // 查询联想续接候选
    kUserDelete,  // 删除学习记录
    kReset,       // 引擎会话复位（失焦/切中英/放弃组词）：清空组词与学习上下文
    kResetComposition,  // 只丢组词、保留学习上下文（同一会话内打断输入流）
    kProbe,       // 探测连接（daemon 恢复用；无会话副作用，结果只更新连接状态）
};

struct EngineOp {
    EngineOpKind kind = EngineOpKind::kInput;
    std::uint64_t seq = 0;    // EnginePump::post 分配
    std::uint32_t epoch = 0;  // TIP 管线纪元：结果纪元不匹配即丢弃
    std::string text;         // input/rebuild 的按键串、punct 的键、联想/删除的词
    std::string syllables;    // 撇号分隔音节（联想查询 / 词条分段）
    std::string aux;          // 辅助载荷（联想删除后的重查来源音节）
    bool associate = false;   // userDelete 来自联想面板（成功后按 aux 重查联想）
    bool halfWidth = false;
    int index = 0;
};

struct EngineResult {
    EngineOp op;              // 原样带回：结果应用需要 op 的载荷（音节/下标/兜底文本）
    bool ok = false;
    bool connectionLost = false;
    bool deleted = false;     // userDelete 的 daemon 返回值
    std::string committed;    // select/punct/associateSelect 的上屏文本
    chaoset::Comp comp;       // input/backspace/page/associate 的组词态
};

// 单请求截止：只在引擎线程上阻塞，宿主 UI 线程永不感知。取值只需覆盖
// 「daemon 算得慢」，比按键路径的旧预算宽得多也不会影响交互。
constexpr DWORD kEngineRequestDeadlineMs = 200;
// 连接失败后的重试冷却（仅普通排队请求；kRebuild 不受限，负责探测恢复）。
constexpr DWORD kEngineReconnectCooldownMs = 250;

class EnginePump {
public:
    EnginePump();
    ~EnginePump();
    EnginePump(const EnginePump&) = delete;
    EnginePump& operator=(const EnginePump&) = delete;

    // 启动前在调用线程做一次非阻塞连接探测（单次 CreateFileW、绝不等待）。
    // 让激活后的首个内容键通常已处于已连接状态。
    void warmConnect();
    // 启动线程（幂等）。tid 是 TIP 线程 id，结果回投到该线程的 message-only 窗口。
    void start(DWORD tid);
    // 停止并等待线程退出（有界：单个请求最长 kEngineRequestDeadlineMs）。
    // 线程未能在等待窗口内退出时（理论上不会发生）保留对象不销毁，避免线程
    // 引用已释放的 this：调用方据 joinOk() 决定是 delete 还是故意泄漏。
    void stop();
    bool alive() const { return alive_.load(); }
    bool joinOk() const { return joinOk_; }

    std::uint64_t post(EngineOp op);
    bool popResult(EngineResult* out);
    // 丢弃尚未开始执行的请求（kCancel 之外的重置路径用；不打断正在执行的那一个）。
    void clearPending();
    // 连接是否可用（原子快照；TIP 线程据此对首个内容键做 fail-open 判定）。
    bool connected() const { return connected_.load(); }

private:
    static DWORD WINAPI threadProc(LPVOID self);
    void run();
    void shutdown();
    EngineResult exec(const EngineOp& op);
    bool ensureConn(bool bypassCooldown);

    mutable std::mutex mu_;
    std::condition_variable cv_;
    std::deque<EngineOp> queue_;
    std::deque<EngineResult> results_;
    bool stop_ = false;
    std::atomic<bool> alive_{false};
    bool joinOk_ = false;
    std::atomic<bool> connected_{false};
    std::uint64_t nextSeq_ = 0;
    DWORD tid_ = 0;
    HANDLE thread_ = nullptr;
    DWORD connectRetryAfter_ = 0;

    // 传输与会话独占：只有引擎线程碰它们。
    PipeTransport transport_;
    chaoset::Session session_{transport_};
};

}  // namespace chaoset::win

#endif  // CHAOSET_WIN_ENGINE_PUMP_HPP_
