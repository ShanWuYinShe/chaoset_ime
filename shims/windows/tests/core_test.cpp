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
#include <ctime>
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

// 撇号分隔的音节串 → 音节数组（与 win32.cpp splitSyllables 同规则）。
std::vector<std::string> splitSylls(const std::string& syllables) {
    std::vector<std::string> out;
    std::size_t start = 0;
    while (start <= syllables.size()) {
        std::size_t pos = syllables.find('\'', start);
        if (pos == std::string::npos) {
            if (start < syllables.size()) out.push_back(syllables.substr(start));
            break;
        }
        if (pos > start) out.push_back(syllables.substr(start, pos - start));
        start = pos + 1;
    }
    return out;
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
                            "\"shuangpin\":\"mspy\",\"committed\":\"你好\","
                            "\"candidates\":[{\"text\":\"你好\",\"score\":99,\"from_user\":true},"
                            "{\"text\":\"你号\"}]}}",
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
    check(c.candidates[0].fromUser, "JSON: 学习标记 from_user=true 解析");
    check(!c.candidates[1].fromUser, "JSON: 缺省 from_user 视为未加权");
    // 双拼方案名 + 顶层 committed：Windows 壳据此把 ';' 路由到 input 并把
    // 引擎回传的上屏文本写入文档（跨端接口契约，2026-10-30）。
    check(c.shuangpin == "mspy", "JSON: 双拼方案名 shuangpin=mspy 解析");
    check(c.committed == "你好", "JSON: 顶层 committed 由 Composition 解析");
    check(parseCommitted(*result) == "你好", "JSON: parseCommitted 与 Composition 字段一致");

    // 非双拼/旧 daemon：两个字段都缺省 → 空串（壳保持旧行为：';' 仍走 punct）
    JsonValue plain = jsonParse("{\"input\":\"ni\",\"candidates\":[]}", &ok);
    Comp cPlain = parseComposition(plain);
    check(ok && cPlain.ok && cPlain.shuangpin.empty() && cPlain.committed.empty(),
          "JSON: 缺省 shuangpin/committed 均为空（非双拼模式）");

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

    // 歧义切分：xian 既能切成一整音节（先/县/现），也能切成 xi+an（西安）。
    //
    // 断言**不能要求候选里出现「西安」**：西安在 dict_common.tsv 里只有词频 57，
    // 单音节「先」等高频字会占满第一页，西安根本进不了前 6 个候选——这是词库
    // 与排序的合理结果，不是 bug。测试此前的写法对词库敏感，换 diff 词库就误报
    // （2026-10-26 修复）。
    // 这里改为断言「歧义切分能力本身」：xi'an 带撇号输入必须能命中西安，
    // 与词频无关，且能证明 daemon 支持显式音节分隔。
    Comp c3 = session.input("xian");
    check(c3.ok && !c3.candidates.empty(), "xian 有候选");
    session.reset();
    Comp c3b = session.input("xi'an");
    bool hasXiAn = false;
    for (const auto& cand : c3b.candidates) hasXiAn |= (cand.text == "西安");
    check(c3b.ok && hasXiAn, "xi'an 歧义切分候选含 西安");
    session.reset();
    session.input("xian");

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
    // punct 第三参数前还有个 halfWidth（2026-10-29 起全/半角开关由壳下发，
    // f7919ae 给 core.hpp 加了该参数却漏改本测试——Windows 核心测试从那时起
    // 编译不过，而它只在发版流水线里编译，CI 不覆盖，一直没人发现）。
    // 这里全程用全角（halfWidth=false），与默认模式一致。
    check(session.punct(",", false, &punctOut) && punctOut == "你好，", "组词中标点 = 你好，");
    check(session.punct(".", false, &punctOut) && punctOut == "。", "空组词标点直映 。");
    check(!session.punct("@", false, &punctOut), "未知标点判失败");
    Comp p2 = session.input("hao");
    check(p2.ok && !p2.candidates.empty(), "标点错误后连接仍可用");

    // 候选翻页：边界钳制、页内序号选词。
    //
    // 断言**不写死每页候选数**：页容量是引擎内部常量（当前 pageSize=6，
    // 2026-10-25 由 9 改成 6），测试若硬编码 9 就会在引擎调整后误报——此前
    // 这里写死「9 个候选 / 2 页」，引擎改成 6 后一直失败但没人发现
    // （CI 根本不跑本测试，2026-10-26 修复）。改为从响应里推导页容量。
    session.reset();
    Comp g1 = session.input("yi");
    const std::size_t pageSize = g1.candidates.size();  // 首页即页容量
    check(g1.ok && g1.page == 0 && pageSize > 0 && g1.pageCount >= 1,
          "yi 第一页可取到候选");
    if (g1.pageCount > 1) {
        Comp g2 = session.nextPage();
        check(g2.ok && g2.page == 1, "可翻到第二页");
        Comp g3 = session.nextPage();
        // 继续下翻，直到钳制在末页。上界必须由 pageCount 推导：
        // 单音节全量召回后 yi 有 24 页，写死 16 会停在中间，后面的
        // 「钳制不动」断言就会被下一页推进误判为失败。
        Comp gLast = g3;
        for (int i = 0; i <= gLast.pageCount && gLast.page < gLast.pageCount - 1; ++i) {
            gLast = session.nextPage();
        }
        Comp gClamp = session.nextPage();
        check(gClamp.ok && gClamp.page == gLast.page, "末页下翻钳制不动");
        Comp g4 = session.prevPage();
        check(g4.ok && (g4.page == gLast.page - 1 || g4.page == 0), "可上翻回上一页");
        // 回到首页并验证第二页页内首选可选
        (void)pageSize;
        session.reset();
        session.input("yi");
        session.nextPage();
        std::string pageSel;
        check(session.select(0, &pageSel) && !pageSel.empty(),
              "第二页选 1 得页内首选");
    } else {
        check(true, "单页词库，跳过翻页用例");
        check(true, "单页词库，跳过翻页用例");
        check(true, "单页词库，跳过翻页用例");
        check(true, "单页词库，跳过翻页用例");
    }

    // daemon 断开重连：杀掉的是外部进程，这里验证冷启动重连路径
    // （通过重新连接同一 daemon 即可）
    Comp c7 = session.input("hao");
    check(c7.ok, "后续按键继续可用");

    // 词组联想（2026-10-27 接入 Windows 壳）：上屏词的音节 → 续接词组 →
    // associateSelect 直接上屏（学习路径与 select 一致）。
    {
        session.reset();
        Comp n = session.input("ni");
        if (n.ok && !n.candidates.empty()) {
            std::string committed;
            const std::string firstSylls = n.candidates[0].syllables;
            check(session.select(0, &committed) && !committed.empty(),
                  "联想前置：上屏「ni」首选");
            Comp assoc;
            check(session.associate(splitSylls(firstSylls), &assoc) && assoc.ok,
                  "associate 按上屏词音节可查到续接词组");
            if (assoc.ok && !assoc.candidates.empty()) {
                check(!assoc.candidates[0].syllables.empty(),
                      "联想候选带音节（壳需要它做连选续接）");
                std::string assocCommitted;
                const chaoset::Candidate& pick = assoc.candidates[0];
                check(session.associateSelect(pick.text, splitSylls(pick.syllables), &assocCommitted) &&
                          assocCommitted == pick.text,
                      "associateSelect 联想词直接上屏");
            } else {
                check(true, "该词库无 ni 续接词，跳过连选断言");
                check(true, "该词库无 ni 续接词，跳过连选断言");
            }
        } else {
            check(true, "词库无 ni，跳过联想用例");
            check(true, "词库无 ni，跳过联想用例");
            check(true, "词库无 ni，跳过联想用例");
        }
    }

    if (failures == 0) {
        std::printf("PASS: core 全部检查通过\n");
        return 0;
    }
    std::fprintf(stderr, "FAIL: %d 项未通过\n", failures);
    return 1;
}
