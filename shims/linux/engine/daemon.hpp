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
    bool writeAll(const std::uint8_t* data, std::size_t len) override;
    bool readAll(std::uint8_t* buf, std::size_t len) override;
    void closeConn() override;
    bool connected() const { return fd_ >= 0; }

private:
    std::string path_;
    int fd_ = -1;
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
};

}  // namespace chaoset