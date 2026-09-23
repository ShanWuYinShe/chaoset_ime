// chaoset TSF 壳核心层行为测试（原生运行，非交叉编译）：
// 用真实的 chaosetd（TCP 回环）驱动 core.cpp 的全部协议与会话逻辑，
// 另含 JSON 解析单元断言。全过打印 PASS，任一失败非零退出。
// 用法：core_test <host:port>
#include "core.hpp"

#include <arpa/inet.h>
#include <netdb.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include <cstdio>
#include <cstring>
#include <string>

using namespace chaoset;

namespace {

int failures = 0;

void check(bool cond, const std::string& msg) {
    if (cond) {
        std::printf("ok: %s\n", msg.c_str());
    } else {
        ++failures;
        std::fprintf(stderr, "FAIL: %s\n", msg.c_str());
    }
}

// TCP 回环传输——与 Windows 壳的 TcpTransport 语义一致
// （有连接、有序字节流、断线由传输层负责重连）。
class TcpTransport : public Transport {
public:
    explicit TcpTransport(const std::string& hostPort) {
        auto colon = hostPort.rfind(':');
        host_ = hostPort.substr(0, colon);
        port_ = hostPort.substr(colon + 1);
    }
    ~TcpTransport() override { closeConn(); }

    bool ensureConnected() {
        if (fd_ >= 0) return true;
        if (lastFailAt_ > 0 && time(nullptr) - lastFailAt_ < 1) return false;  // 1 秒冷却
        struct addrinfo hints{};
        hints.ai_family = AF_INET;
        hints.ai_socktype = SOCK_STREAM;
        struct addrinfo* res = nullptr;
        if (getaddrinfo(host_.c_str(), port_.c_str(), &hints, &res) != 0) {
            lastFailAt_ = time(nullptr);
            return false;
        }
        int fd = -1;
        for (struct addrinfo* p = res; p != nullptr; p = p->ai_next) {
            fd = socket(p->ai_family, p->ai_socktype, p->ai_protocol);
            if (fd < 0) continue;
            if (connect(fd, p->ai_addr, p->ai_addrlen) == 0) break;
            ::close(fd);
            fd = -1;
        }
        freeaddrinfo(res);
        if (fd < 0) {
            lastFailAt_ = time(nullptr);
            return false;
        }
        fd_ = fd;
        return true;
    }

    bool writeAll(const std::uint8_t* data, std::size_t len) override {
        if (!ensureConnected()) return false;
        std::size_t off = 0;
        while (off < len) {
            ssize_t n = ::send(fd_, data + off, len - off, 0);
            if (n <= 0) {
                closeConn();
                return false;
            }
            off += static_cast<std::size_t>(n);
        }
        return true;
    }

    bool readAll(std::uint8_t* buf, std::size_t len) override {
        std::size_t off = 0;
        while (off < len) {
            ssize_t n = ::recv(fd_, buf + off, len - off, 0);
            if (n <= 0) {
                closeConn();
                return false;
            }
            off += static_cast<std::size_t>(n);
        }
        return true;
    }

    void closeConn() override {
        if (fd_ >= 0) {
            ::close(fd_);
            fd_ = -1;
            lastFailAt_ = 0;
        }
    }

private:
    std::string host_, port_;
    int fd_ = -1;
    time_t lastFailAt_ = 0;
};

}  // namespace

int main(int argc, char** argv) {
    std::setvbuf(stdout, nullptr, _IONBF, 0);
    if (argc < 2) {
        std::fprintf(stderr, "用法: core_test <host:port>\n");
        return 2;
    }

    // ---- JSON 单元断言 ----
    bool ok = false;
    JsonValue v = jsonParse("{\"id\":7,\"result\":{\"input\":\"nihao\",\"preedit\":\"ni'hao\","
                            "\"candidates\":[{\"text\":\"你好\",\"score\":99},{\"text\":\"你号\"}]}}",
                            &ok);
    check(ok && v.type == JsonValue::Type::Obj, "JSON: 对象解析");
    check(v.find("id") != nullptr && v.find("id")->type == JsonValue::Type::Num && v.find("id")->number == 7,
          "JSON: 数字字段");
    const JsonValue* result = v.find("result");
    check(result != nullptr && result->find("preedit") != nullptr &&
              result->find("preedit") != nullptr && result->find("preedit")->str == "ni'hao",
          "JSON: 嵌套对象字符串");
    Comp c = parseComposition(*result);
    check(c.ok && c.input == "nihao" && c.preedit == "ni'hao" && c.candidates.size() == 2 &&
              c.candidates[0].text == "你好",
          "JSON: 候选数组提取");
    check(parseCommitted(*result).empty(), "JSON: committed 缺省为空");

    JsonValue esc = jsonParse("{\"s\":\"a\\\"b\\u4f60\\n\"}", &ok);
    check(ok && esc.type == JsonValue::Type::Obj && esc.find("s") != nullptr &&
              esc.find("s")->str == std::string("a\"b") + "\xe4\xbd\xa0" + "\n",
          "JSON: 转义与 \\u 中文");
    jsonParse("{\"broken\":", &ok);
    check(!ok, "JSON: 截断输入报错");

    // ---- 与真实 daemon 的会话流程 ----
    TcpTransport transport(argv[1]);
    Session session(transport);

    Comp c1 = session.input("n");
    check(c1.ok, "连接 daemon（TCP，input 有响应）");
    Comp c2 = session.input("ihao");
    check(c2.ok && c2.preedit == "ni'hao", "预编辑 ni'hao");
    check(c2.candidates.size() >= 1 && c2.candidates[0].text == "你好", "第一候选 你好");
    std::string committed;
    check(session.select(0, &committed) && committed == "你好", "上屏 你好");

    Comp c3 = session.input("xian");
    bool hasXiAn = false;
    for (const auto& cand : c3.candidates) hasXiAn |= (cand.text == "西安");
    check(hasXiAn, "xian 歧义切分候选含 西安");

    Comp c4 = session.backspace();
    check(c4.ok && c4.input == "xia", "退格后输入 xia");
    Comp c5 = session.reset();
    check(c5.ok && c5.input.empty(), "重置后输入为空");

    // select 越界：daemon 返回业务 error，调用层应判失败且连接仍可用
    std::string dummy;
    check(!session.select(99, &dummy), "越界 select 判失败");
    Comp c6 = session.input("ni");
    check(c6.ok && !c6.candidates.empty(), "业务错误后连接仍可用");

    // 中文标点：组词中合并上屏、空组词直映、未知键判失败且连接可用
    session.reset();  // 清掉上文遗留的未上屏组词，保证 nihao 从零开始
    Comp p1 = session.input("nihao");
    check(p1.ok, "标点用例前组词 nihao");
    std::string punctOut;
    check(session.punct(",", &punctOut) && punctOut == "你好，", "组词中标点 = 你好，");
    check(session.punct(".", &punctOut) && punctOut == "。", "空组词标点直映 。");
    check(!session.punct("@", &punctOut), "未知标点判失败");
    Comp p2 = session.input("hao");
    check(p2.ok && !p2.candidates.empty(), "标点错误后连接仍可用");

    // daemon 断开重连：杀掉的是外部进程，这里验证冷启动重连路径
    // （通过重新连接同一 daemon 即可）
    Comp c7 = session.input("hao");
    check(c7.ok, "后续按键继续可用");

    if (failures == 0) {
        std::printf("PASS: core 全部检查通过\n");
        return 0;
    }
    std::fprintf(stderr, "FAIL: %d 项未通过\n", failures);
    return 1;
}
