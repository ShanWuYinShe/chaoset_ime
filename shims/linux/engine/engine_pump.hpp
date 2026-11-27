// Linux ibus 壳的异步引擎线程（issue #149 三端统一：异步 + 慢/失败不关候选窗）。
//
// 为什么需要它：IBus 把按键回调放在主循环线程上同步执行，旧实现直接在这条线程上
// 调 daemon 的 `input/backspace/selectPrefix/...`。daemon 慢或挂起时（SO_RCVTIMEO
// 300ms 起步、懒启动最长约 5s）整个输入法主循环被按住，候选窗/预编辑全部停摆。
//
// 现设计：每个 ibus engine 实例一个 EnginePump 线程，独占自己的 daemon 连接
// （daemon 的组词会话按连接隔离，同进程多个 engine 不能共享）。主循环只把操作压入
// FIFO 并立即用乐观组词态渲染；引擎线程按序执行，结果经通知回调投回主循环，
// 由 IbusEngineState 按 seq 应用。工作线程绝不触碰任何 ibus API。
#pragma once

#include <atomic>
#include <condition_variable>
#include <cstdint>
#include <deque>
#include <functional>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

#include "core.hpp"
#include "daemon.hpp"

namespace chaoset {

enum class EngineOpKind {
    kInput,           // 追加输入（不带 base）
    kRebuild,         // base=op.base 重建引擎输入后重放 op.text 未解码按键
    kBackspace,
    kBackspaceWord,
    kNextPage,
    kPrevPage,
    kSelectPrefix,
    kPunct,
    kAssociateSelect,
    kAssociate,
    kUserDelete,
    kReset,           // 清空组词与学习上下文（失焦/会话结束）
    kResetComposition,// 只丢组词、保留学习上下文
    kProbe,           // 探测连接（无会话副作用）
};

struct EngineOp {
    EngineOpKind kind = EngineOpKind::kInput;
    std::uint64_t seq = 0;
    std::uint32_t epoch = 0;
    std::string text;                    // 输入字符 / 标点键 / 联想词 / rebuild 未解码按键
    std::string base;                    // rebuild 基底（已应用/已解码输入串）
    std::vector<std::string> sylls;      // 联想查询/联想上屏的音节序列
    std::string aux;                     // 联想删除后重查的来源音节串
    bool associate = false;              // userDelete：来源是联想面板
    bool halfWidth = false;
    int index = 0;                       // selectPrefix 下标
};

struct EngineResult {
    EngineOp op;
    bool ok = false;
    bool connectionLost = false;
    bool deleted = false;   // userDelete：daemon 侧是否真的删到记录
    std::string committed;  // punct / associateSelect / selectPrefix 的上屏文本
    Comp comp;
};

// 停机等待线程退出的上限（stop 时先 abort 唤醒阻塞读取，正常瞬时退出）。
constexpr int kEngineStopWaitMs = 1000;
// 连不上时同一窗口内不再反复探测（kRebuild/kProbe/kReset 除外）。
constexpr int kEngineReconnectCooldownMs = 250;

class EnginePump {
public:
    using Notify = std::function<void()>;

    EnginePump(Daemon::Options options, Notify notify);
    ~EnginePump();
    EnginePump(const EnginePump&) = delete;
    EnginePump& operator=(const EnginePump&) = delete;

    // 启动线程前在主循环线程做一次**非阻塞**连接探测（单次 connect，不等待、不拉起
    // daemon）：让激活后的首个内容键通常已处于已连接状态。
    void warmConnect();
    void start();
    // 有界等待线程退出。joinOk()==false 表示线程仍引用本对象，调用方必须故意不
    // 销毁它（std::thread 仍 joinable），绝不能让线程跑进已释放内存。
    void stop();
    bool joinOk() const { return joinOk_; }

    void post(EngineOp op);
    void clearPending();
    bool popResult(EngineResult* out);
    bool connected() const { return connected_.load(); }
    void setShuangpinEnabled(bool v) { shuangpinEnabled_.store(v); }

private:
    void run();
    bool ensureConn(bool bypassCooldown);
    EngineResult exec(const EngineOp& op);
    void pushResult(EngineResult result);

    Daemon daemon_;
    Notify notify_;
    std::thread thread_;
    mutable std::mutex mu_;
    std::condition_variable cv_;
    std::condition_variable doneCv_;
    std::deque<EngineOp> queue_;
    std::deque<EngineResult> results_;
    std::atomic<bool> connected_{false};
    std::atomic<bool> shuangpinEnabled_{false};
    bool stop_ = false;
    bool done_ = false;
    bool joinOk_ = false;
    std::int64_t connectRetryAfterMs_ = 0;
    bool sessionDesynced_ = false;
    bool hadCompositionOps_ = false;
    std::uint64_t syncedConnGen_ = 0;
};

}  // namespace chaoset
