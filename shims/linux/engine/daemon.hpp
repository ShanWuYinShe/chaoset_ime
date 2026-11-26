// Linux 原生 ibus 引擎的 daemon 传输层：Unix socket + 自动拉起 chaosetd。
// 只做平台/进程生命周期与路径解析；拼音/联想算法全部在 Go daemon。
#pragma once

#include <string>
#include <vector>

#include "core.hpp"

namespace chaoset {

struct LinuxPaths {
    std::string socket;
    std::string dataDir;
    std::string userdict;
    std::string importedDict;
    std::string config;
    std::string log;
};

/// 按 XDG 约定计算路径；环境变量 CHAOSET_SOCKET 优先。
LinuxPaths defaultLinuxPaths();

class UnixTransport final : public Transport {
public:
    explicit UnixTransport(std::string path);
    ~UnixTransport() override;
    bool connectSocket();
    // 单次 I/O 等待上限（毫秒）。异步引擎线程用 200ms；默认 300ms 与 mac 壳同量级。
    void setDeadlineMs(int ms) { deadlineMs_ = ms > 0 ? ms : 300; }
    bool writeAll(const std::uint8_t* data, std::size_t len) override;
    bool readAll(std::uint8_t* buf, std::size_t len) override;
    void closeConn() override;
    bool connected() const { return fd_ >= 0; }
    std::uint64_t connGen() const { return connGen_; }
    /// 上一次 readAll 失败是否只是「等到 SO_RCVTIMEO 仍无数据」（一个字节都没消费）。
    /// 只有这种干净超时才能保留连接与会话；部分读取或 EOF 必须断连（见 core.hpp 契约）。
    bool lastReadTimedOut() const override { return lastReadTimedOut_; }

private:
    std::string path_;
    int fd_ = -1;
    std::uint64_t connGen_ = 0;
    int deadlineMs_ = 300;
    bool lastReadTimedOut_ = false;
};

/// daemon 会话：首次调用前尝试连接；socket 不存在时拉起已安装的 chaosetd。
class Daemon {
public:
    struct Options {
        LinuxPaths paths;
        std::string daemonPath;
        std::vector<std::string> dicts;
        std::string importedDict;
        std::string userdict;
        std::string log;
        bool fuzzy = false;
        bool traditional = false;
        std::string shuangpin;
    };

    explicit Daemon(Options options);
    ~Daemon();
    bool ensureReady();
    // 引擎线程专用：单次非阻塞探测并在首次/冷却期外异步拉起 daemon，绝不休眠等待。
    bool tryConnectOrSpawn();
    // 单次非阻塞连接探测：不等待、不拉起 daemon（主循环线程可用）。
    bool tryConnectOnce() { return connectOnce(); }
    bool connected() const { return transport_.connected(); }
    void closeConn() { transport_.closeConn(); }
    std::uint64_t connGen() const { return transport_.connGen(); }
    void setDeadlineMs(int ms) { transport_.setDeadlineMs(ms); }
    /// 追加输入；失败时用 base 做幂等重放（base = 壳侧上次已知的完整输入串）。
    ///
    /// 读超时**不代表团没处理**——daemon 可能已经执行、只是响应迟到。此时若直接当成
    /// 失败（清空预编辑/放行键），daemon 会话里却已多了这段输入，下一次按键的预编辑
    /// 就比应用里多出字符。用 base 重放：daemon 先把引擎输入串整体置为 base 再追加
    /// text，与逐键输入同构，重放幂等；连接真断时新连接是空会话，带 base 同样正确。
    Comp inputWithRebuild(const std::string& text, bool halfWidth, const std::string& base);
    Session& session() { return session_; }
    const LinuxPaths& paths() const { return options_.paths; }
    bool failed() const { return failed_; }

private:
    bool spawnDaemon();
    bool connectOnce();

    Options options_;
    UnixTransport transport_;
    Session session_;
    bool spawned_ = false;
    bool failed_ = false;
    std::int64_t lastSpawnMs_ = 0;
};

}  // namespace chaoset