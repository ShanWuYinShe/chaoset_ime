#include "daemon.hpp"

#include <glib.h>
#include <sys/socket.h>
#include <sys/time.h>
#include <sys/un.h>
#include <unistd.h>

#include <cerrno>
#include <cstdio>
#include <cstring>

namespace chaoset {
namespace {

std::string gbuild(const char* a, const char* b) {
    char* p = g_build_filename(a, b, nullptr);
    std::string s = p == nullptr ? std::string() : std::string(p);
    g_free(p);
    return s;
}

void logLine(const std::string& text) { std::fprintf(stderr, "chaoset-engine: %s\n", text.c_str()); }

}  // namespace

LinuxPaths defaultLinuxPaths() {
    LinuxPaths p;
    const char* envSocket = g_getenv("CHAOSET_SOCKET");
    const char* dataHome = g_get_user_data_dir();
    // 必须直接读 XDG_RUNTIME_DIR，不能用 g_get_user_runtime_dir()：
    // GLib 在环境变量缺失时会自动回退到 $XDG_CACHE_HOME（~/.cache），
    // 而 Go 侧 internal/appdirs.SocketPath 回退到数据目录
    //（~/.local/share/chaoset）。两边算出不同 socket 路径 = 管理工具连不上
    // daemon，且单实例锁按路径生效而各锁各的，能同时起两个 daemon
    //（2026-11-04 实测：GLib 返回 ~/.cache）。
    const char* runtime = g_getenv("XDG_RUNTIME_DIR");
    const char* configHome = g_get_user_config_dir();
    p.dataDir = gbuild(dataHome, "chaoset");
    if (envSocket != nullptr && envSocket[0] != '\0') {
        p.socket = envSocket;
    } else if (runtime != nullptr && runtime[0] != '\0') {
        p.socket = gbuild(runtime, "chaoset");
        p.socket = gbuild(p.socket.c_str(), "chaoset.sock");
    } else {
        p.socket = gbuild(p.dataDir.c_str(), "chaoset.sock");
    }
    p.userdict = gbuild(p.dataDir.c_str(), "userdict.json");
    p.importedDict = gbuild(p.dataDir.c_str(), "dict.tsv");
    p.config = gbuild(configHome, "chaoset");
    p.config = gbuild(p.config.c_str(), "config.json");
    p.log = gbuild(p.dataDir.c_str(), "chaosetd.log");
    return p;
}

UnixTransport::UnixTransport(std::string path) : path_(std::move(path)) {}
UnixTransport::~UnixTransport() { closeConn(); }

bool UnixTransport::connectSocket() {
    closeConn();
    const int fd = ::socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return false;
    struct timeval tv {};
    tv.tv_sec = 0;
    tv.tv_usec = 300000;  // 300ms：与 mac EngineClient 同量级，GC 尖峰不误杀；
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));
    struct sockaddr_un addr {};
    addr.sun_family = AF_UNIX;
    if (path_.size() >= sizeof(addr.sun_path)) { ::close(fd); return false; }
    std::memcpy(addr.sun_path, path_.c_str(), path_.size() + 1);
    if (::connect(fd, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) != 0) {
        ::close(fd);
        return false;
    }
    fd_ = fd;
    return true;
}

bool UnixTransport::writeAll(const std::uint8_t* data, std::size_t len) {
    if (fd_ < 0) return false;
    std::size_t off = 0;
    while (off < len) {
        const ssize_t n = ::send(fd_, data + off, len - off, MSG_NOSIGNAL);
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) { closeConn(); return false; }
        off += static_cast<std::size_t>(n);
    }
    return true;
}

bool UnixTransport::readAll(std::uint8_t* buf, std::size_t len) {
    if (fd_ < 0) return false;
    std::size_t off = 0;
    while (off < len) {
        const ssize_t n = ::recv(fd_, buf + off, len - off, 0);
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) { closeConn(); return false; }
        off += static_cast<std::size_t>(n);
    }
    return true;
}

void UnixTransport::closeConn() {
    if (fd_ >= 0) { ::close(fd_); fd_ = -1; }
}

Daemon::Daemon(Options options)
    : options_(std::move(options)),
      transport_(options_.paths.socket),
      session_(transport_) {}

Daemon::~Daemon() = default;

bool Daemon::connectOnce() { return transport_.connectSocket(); }

bool Daemon::ensureReady() {
    if (transport_.connected()) return true;
    if (connectOnce()) { failed_ = false; return true; }
    if (!spawned_) {
        spawned_ = true;
        if (!spawnDaemon()) { failed_ = true; return false; }
    }
    // 首次按键会拉起 daemon，词库较大时要给足加载时间；之后每次按键都会
    // 重新尝试连接，failed_ 不参与短路（否则 daemon 晚到会永久降级）。
    for (int i = 0; i < 60; ++i) {
        if (connectOnce()) { failed_ = false; return true; }
        g_usleep(80 * 1000);
    }
    // 这次没连上（daemon 启动失败或被设置窗杀掉）：允许下一次按键重新拉起。
    spawned_ = false;
    failed_ = true;
    return false;
}

static std::string executableDir() {
    gchar* exe = g_file_read_link("/proc/self/exe", nullptr);
    if (exe == nullptr) return std::string();
    gchar* dir = g_path_get_dirname(exe);
    std::string out = dir == nullptr ? std::string() : std::string(dir);
    g_free(dir);
    g_free(exe);
    return out;
}

bool Daemon::spawnDaemon() {
    // 运行时目录可能还没有：daemon 自己也会建，但 socket 目录必须先存在。
    gchar* dirname = g_path_get_dirname(options_.paths.socket.c_str());
    const std::string socketDir = dirname == nullptr ? std::string(".") : std::string(dirname);
    g_free(dirname);
    g_mkdir_with_parents(socketDir.c_str(), 0700);
    g_mkdir_with_parents(options_.paths.dataDir.c_str(), 0700);

    std::string daemon = options_.daemonPath;
    if (daemon.empty()) {
        // 解包即用的绿色形态：daemon 与引擎同在 bin/ 目录。
        const std::string sibling = executableDir() + "/chaosetd";
        if (g_file_test(sibling.c_str(), G_FILE_TEST_IS_EXECUTABLE)) {
            daemon = sibling;
        } else {
            char* found = g_find_program_in_path("chaosetd");
            if (found != nullptr) { daemon = found; g_free(found); }
        }
    }
    if (daemon.empty()) daemon = "/usr/bin/chaosetd";
    if (!g_file_test(daemon.c_str(), G_FILE_TEST_IS_EXECUTABLE)) {
        logLine("找不到可执行的 chaosetd（" + daemon + "），输入法将降级为英文透传");
        return false;
    }

    GPtrArray* argv = g_ptr_array_new_with_free_func(g_free);
    auto add = [&argv](const std::string& s) { g_ptr_array_add(argv, g_strdup(s.c_str())); };
    add(daemon);
    add("-socket"); add(options_.paths.socket);
    for (const auto& dict : options_.dicts) { add("-dict"); add(dict); }
    if (!options_.importedDict.empty() && g_file_test(options_.importedDict.c_str(), G_FILE_TEST_IS_REGULAR)) {
        add("-dict"); add(options_.importedDict);
    }
    if (!options_.userdict.empty()) { add("-userdict"); add(options_.userdict); }
    if (!options_.log.empty()) { add("-log"); add(options_.log); }
    if (options_.fuzzy) add("-fuzzy");
    if (options_.traditional) add("-traditional");
    if (!options_.shuangpin.empty()) { add("-shuangpin"); add(options_.shuangpin); }
    g_ptr_array_add(argv, nullptr);
    logLine("启动 chaosetd: " + daemon);

    GError* error = nullptr;
    const gboolean ok = g_spawn_async(
        nullptr, reinterpret_cast<gchar**>(argv->pdata), nullptr,
        static_cast<GSpawnFlags>(G_SPAWN_SEARCH_PATH | G_SPAWN_STDOUT_TO_DEV_NULL |
                                G_SPAWN_STDERR_TO_DEV_NULL),
        nullptr, nullptr, nullptr, &error);
    g_ptr_array_free(argv, TRUE);
    if (!ok) {
        logLine(std::string("拉起 chaosetd 失败: ") + (error == nullptr ? "unknown" : error->message));
        if (error != nullptr) g_error_free(error);
        return false;
    }
    logLine("已拉起 chaosetd（socket=" + options_.paths.socket + "）");
    return true;
}

}  // namespace chaoset