// chaoset TSF 壳核心层行为测试（原生运行，非交叉编译）：
// 用真实的 chaosetd（Unix socket；Windows 壳本身走命名管道，测试台无管道
// API 时用同协议同帧格式的 Unix socket 驱动）驱动 core.cpp 的全部协议与会话
// 逻辑，另含 JSON 解析单元断言。全过打印 PASS，任一失败非零退出。
// 用法：core_test <socket-path>
#include "candidate_layout.hpp"
#include "core.hpp"

#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <ctime>
#include <string>
#include <utility>
#include <vector>

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

// 脚本化传输：按预设脚本返回「读超时」或「注入一段字节」，不碰真实 socket。
// 用来把「读超时不断连 + 迟到响应按帧丢弃」这条契约变成可跑的 UT（用户要求：
// 之前修复的问题被回归引入，必须有单元测试把关）。
class ScriptedTransport : public Transport {
public:
    struct Step {
        bool timeout;   // true = 本次 readAll 报「等到截止无数据」
        std::string bytes;  // 否则把这段字节追加到待读缓冲
        // true = 读到一部分后超时（字节已从传输取出却被丢弃）——字节流从此错位，
        // 上层必须按「连接已坏」断连，不得当成可保留的干净超时。
        bool partialTimeout = false;
    };
    std::vector<Step> steps;
    bool failWrite = false;
    bool closeCalled = false;
    int closeCount = 0;
    std::vector<std::string> writes;

    bool writeAll(const std::uint8_t* data, std::size_t len) override {
        if (failWrite) return false;
        writes.emplace_back(reinterpret_cast<const char*>(data), len);
        return true;
    }

    bool readAll(std::uint8_t* buf, std::size_t len) override {
        timedOut_ = false;
        while (pending_.size() < len) {
            if (next_ >= steps.size()) {
                // 脚本耗尽：视为超时（保留连接），由用例断言是否该走到这一步。
                timedOut_ = true;
                return false;
            }
            const Step& s = steps[next_++];
            if (s.partialTimeout) {
                // 模拟真实传输：已到达的字节被消费后随失败丢弃，传输层报告
                // 「不是干净超时」（lastReadTimedOut() == false）。
                pending_.clear();
                timedOut_ = false;
                return false;
            }
            if (s.timeout) {
                timedOut_ = true;
                return false;
            }
            pending_ += s.bytes;
        }
        std::memcpy(buf, pending_.data(), len);
        pending_.erase(0, len);
        return true;
    }

    void closeConn() override {
        closeCalled = true;
        ++closeCount;
    }
    bool lastReadTimedOut() const override { return timedOut_; }

    /// 换一批脚本并**重置消费游标**（只 clear() 不重置 next_ 会让下一轮从错误的
    /// step 开始读——UT 自身踩过这个坑）。
    void resetSteps(std::vector<Step> next) {
        steps = std::move(next);
        next_ = 0;
    }

private:
    std::string pending_;
    std::size_t next_ = 0;
    bool timedOut_ = false;
};

// 构造一帧（4 字节大端长度 + JSON），与 daemon 写出的帧格式一致。
std::string frame(const std::string& json) {
    const std::uint32_t n = static_cast<std::uint32_t>(json.size());
    std::string out;
    out.push_back(static_cast<char>((n >> 24) & 0xFF));
    out.push_back(static_cast<char>((n >> 16) & 0xFF));
    out.push_back(static_cast<char>((n >> 8) & 0xFF));
    out.push_back(static_cast<char>(n & 0xFF));
    out += json;
    return out;
}

// 「读超时不断连」契约：超时只放弃本次响应，连接与会话保留；迟到响应在下一次
// 请求前被按帧长丢弃，本次请求拿到的是自己的响应（不错位、不上屏错字）。
void testReadTimeoutKeepsConnection() {
    ScriptedTransport t;
    Session s(t);
    // 第 1 次 call：写成功，读帧头时超时（daemon 慢）。
    t.steps.push_back({true, ""});
    bool ok = true;
    Comp first = s.input("ni", false);
    (void)first;
    check(!t.closeCalled, "读超时不得断开连接（daemon 会话必须保留）");
    check(t.writes.size() == 1, "读超时前请求已完整写出");

    // 第 2 次 call：管道里先有第 1 次的迟到响应（id=1），然后才是本次响应（id=2）。
    t.resetSteps({});
    t.steps.push_back({false, frame("{\"id\":1,\"result\":{\"input\":\"ni\",\"preedit\":\"ni\","
                                       "\"candidates\":[{\"text\":\"你\"}]}}")});
    t.steps.push_back({false, frame("{\"id\":2,\"result\":{\"input\":\"niha\",\"preedit\":\"ni'ha\","
                                       "\"candidates\":[{\"text\":\"你哈\"}]}}")});
    Comp second = s.input("ha", false);
    if (second.candidates.empty() || second.candidates[0].text != "你哈") {
        std::fprintf(stderr, "  [diag] second: input=%s preedit=%s cands=%zu close=%d writes=%zu\n",
                     second.input.c_str(), second.preedit.c_str(), second.candidates.size(),
                     t.closeCalled ? 1 : 0, t.writes.size());
    }
    ok = !second.candidates.empty() && second.candidates[0].text == "你哈";
    check(ok, "迟到响应被丢弃后，本次请求拿到自己的响应（不上屏错字）");
    check(second.input == "niha", "会话状态来自本次响应（input=niha）");
    check(!t.closeCalled, "丢弃迟到响应不得断开连接");

    // drain 自己也超时：迟到响应在本次请求发出之后才到（第 1 次读超时后，下一键
    // 的预清理也没读到它）。此时必须按 stale 计数丢弃旧帧、继续等本次响应，
    // 不能把旧帧当成本次响应而断连——这是 #134「读超时不断连」的最后一段时序。
    ScriptedTransport t4;
    Session s4(t4);
    t4.steps.push_back({true, ""});
    (void)s4.input("ni", false);
    t4.resetSteps({});
    t4.steps.push_back({true, ""});  // 预清理 drain 读超时
    t4.steps.push_back({false, frame("{\"id\":1,\"result\":{\"input\":\"ni\",\"preedit\":\"ni\"}}")});
    t4.steps.push_back({false, frame("{\"id\":2,\"result\":{\"input\":\"niha\",\"preedit\":\"ni'ha\","
                                       "\"candidates\":[{\"text\":\"你哈\"}]}}")});
    Comp late = s4.input("ha", false);
    check(late.ok && late.input == "niha" && !late.candidates.empty(),
          "预清理超时后迟到的旧响应仍被丢弃，本次请求拿到自己的响应");
    check(!t4.closeCalled, "预清理超时不得断开连接");

    // 没有待丢计数时的 id 错位是真正的协议错位，必须断连（循环不得吞掉）。
    ScriptedTransport t5;
    Session s5(t5);
    t5.steps.push_back({false, frame("{\"id\":99,\"result\":{}}")});
    (void)s5.input("x", false);
    check(t5.closeCalled, "无待丢计数时响应 id 不匹配必须断连");

    // 连续超时超过上限（2 次）后必须断连重整，避免无限积累。
    ScriptedTransport t2;
    Session s2(t2);
    for (int i = 0; i < 3; ++i) {
        t2.resetSteps({{true, ""}});
        (void)s2.input("a", false);
    }
    check(t2.closeCalled, "连续读超时超过上限必须断连重整（防止无限积累）");

    // **部分读取后超时**：已到达的字节被消费却随失败丢弃，帧边界永久错位——
    // 这时必须断连，不能当成「可保留的干净超时」（两者在传输层用 lastReadTimedOut
    // 区分；实现里靠「本次 readAll 是否消费过字节」判定）。
    ScriptedTransport t6;
    Session s6(t6);
    t6.steps.push_back({false, "", true});  // partialTimeout
    (void)s6.input("a", false);
    check(t6.closeCalled, "部分读取后超时（字节流已错位）必须断连");

    // drain 自身遇到部分读取：同样已错位，必须断连并清计数。
    ScriptedTransport t7;
    Session s7(t7);
    t7.steps.push_back({true, ""});  // 第 1 次：干净超时 → 记一笔待丢
    (void)s7.input("a", false);
    check(!t7.closeCalled, "（前置）干净读超时保留连接");
    t7.resetSteps({{false, "", true}});  // 第 2 次：drain 遇部分读取
    (void)s7.input("b", false);
    check(t7.closeCalled, "drainStaleResponse 遇到部分读取必须断连");

    // 写失败（含写超时）必须断连：请求帧可能只写了一半，字节流无法再对齐。
    ScriptedTransport t3;
    Session s3(t3);
    t3.failWrite = true;
    (void)s3.input("a", false);
    check(t3.closeCalled, "写失败必须断连（帧完整性无法恢复）");
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

// Unix socket 传输——与 Windows 壳的 PipeTransport 语义一致
// （有连接、有序字节流、断线由传输层负责重连）；阻塞式 connect 即可，
// 测试进程没有 UI 线程要保护。
class UnixTransport : public Transport {
public:
    explicit UnixTransport(std::string path) : path_(std::move(path)) {}
    ~UnixTransport() override { closeConn(); }

    bool ensureConnected() {
        if (fd_ >= 0) return true;
        if (lastFailAt_ > 0 && time(nullptr) - lastFailAt_ < 1) return false;  // 1 秒冷却
        struct sockaddr_un addr{};
        addr.sun_family = AF_UNIX;
        if (path_.size() >= sizeof(addr.sun_path)) {
            lastFailAt_ = time(nullptr);
            return false;  // 路径装不进 sockaddr_un（macOS 上限 104 字节）
        }
        std::memcpy(addr.sun_path, path_.c_str(), path_.size() + 1);
        int fd = ::socket(AF_UNIX, SOCK_STREAM, 0);
        if (fd < 0) {
            lastFailAt_ = time(nullptr);
            return false;
        }
        if (connect(fd, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) != 0) {
            ::close(fd);
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
    std::string path_;
    int fd_ = -1;
    time_t lastFailAt_ = 0;
};

}  // namespace

int main(int argc, char** argv) {
    std::setvbuf(stdout, nullptr, _IONBF, 0);
    if (argc < 2) {
        std::fprintf(stderr, "用法: core_test <socket-path>\n");
        return 2;
    }

    // ---- IPC 读超时韧性（用户要求：修复过的问题不得被回归引入，UT 把关）----
    testReadTimeoutKeepsConnection();

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

    // ---- 候选窗多行折行布局数学（纯函数，与 macOS layoutRows 同算法）----
    {
        using chaoset::layout::Box;
        // 每项宽 = padX*2 + chipW + chipTextGap + textW + markW = 5*2+20+10+40 = 80
        std::vector<Box> boxes(4, Box{5, 20, 10, 40, 0});
        auto r = chaoset::layout::wrapRows(boxes, 100, 10, 20, 3);
        check(r.rows.size() == 3, "折行：4 项/内容宽 100 → 3 行");
        check(r.rows[0].items.size() == 1 && r.rows[0].width == 80,
              "折行：首行 1 项、行宽 80");
        int laid = 0;
        for (const auto& row : r.rows) laid += static_cast<int>(row.items.size());
        check(laid == 3, "折行：maxRows=3 时第 4 项不布局（与 mac 一致）");

        // 窄项可同行：40 + gap10 + 40 = 90 <= 100
        std::vector<Box> small(4, Box{0, 0, 0, 40, 0});
        auto r2 = chaoset::layout::wrapRows(small, 100, 10, 20, 3);
        check(r2.rows.size() == 2 && r2.rows[0].items.size() == 2 &&
                  r2.rows[0].width == 90,
              "折行：40+10+40=90 两项同行");

        // 单条超宽防御：宽 200 钳到内容上限 100
        auto r3 = chaoset::layout::wrapRows({Box{0, 0, 0, 200, 0}}, 100, 10, 20, 3);
        check(r3.rows.size() == 1 && r3.rows[0].width == 100,
              "折行：单条超宽钳到内容上限");
    }

    // ---- 与真实 daemon 的会话流程 ----
    UnixTransport transport(argv[1]);
    Session session(transport);

    Comp c1 = session.input("n", false);
    check(c1.ok, "连接 daemon（Unix socket，input 有响应）");
    Comp c2 = session.input("ihao", false);
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
    Comp c3 = session.input("xian", false);
    check(c3.ok && !c3.candidates.empty(), "xian 有候选");
    session.reset();
    Comp c3b = session.input("xi'an", false);
    bool hasXiAn = false;
    for (const auto& cand : c3b.candidates) hasXiAn |= (cand.text == "西安");
    check(c3b.ok && hasXiAn, "xi'an 歧义切分候选含 西安");
    session.reset();
    session.input("xian", false);

    Comp c4 = session.backspace();
    check(c4.ok && c4.input == "xia", "退格后输入 xia");
    Comp c5 = session.reset();
    check(c5.ok && c5.input.empty(), "重置后输入为空");

    // 按音节回退（Alt+Backspace）：三端统一走引擎 BackspaceWord（2026-10-31）。
    Comp w1 = session.input("nihao", false);
    check(w1.ok, "回退词用例前组词 nihao");
    Comp w2 = session.backspaceWord();
    check(w2.ok && w2.input == "ni", "按音节回退到 ni（Alt+Backspace）");
    session.reset();

    // select 越界：daemon 返回业务 error，调用层应判失败且连接仍可用
    std::string dummy;
    check(!session.select(99, &dummy), "越界 select 判失败");
    Comp c6 = session.input("ni", false);
    check(c6.ok && !c6.candidates.empty(), "业务错误后连接仍可用");

    // 中文标点：组词中合并上屏、空组词直映、未知键判失败且连接可用
    session.reset();  // 清掉上文遗留的未上屏组词，保证 nihao 从零开始
    Comp p1 = session.input("nihao", false);
    check(p1.ok, "标点用例前组词 nihao");
    std::string punctOut;
    // punct 第三参数前还有个 halfWidth（2026-10-29 起全/半角开关由壳下发，
    // f7919ae 给 core.hpp 加了该参数却漏改本测试——Windows 核心测试从那时起
    // 编译不过，而它只在发版流水线里编译，CI 不覆盖，一直没人发现）。
    // 这里全程用全角（halfWidth=false），与默认模式一致。
    check(session.punct(",", false, &punctOut) && punctOut == "你好，", "组词中标点 = 你好，");
    check(session.punct(".", false, &punctOut) && punctOut == "。", "空组词标点直映 。");
    check(!session.punct("@", false, &punctOut), "未知标点判失败");
    Comp p2 = session.input("hao", false);
    check(p2.ok && !p2.candidates.empty(), "标点错误后连接仍可用");

    // 候选翻页：边界钳制、页内序号选词。
    //
    // 断言**不写死每页候选数**：页容量是引擎内部常量（当前 pageSize=6，
    // 2026-10-25 由 9 改成 6），测试若硬编码 9 就会在引擎调整后误报——此前
    // 这里写死「9 个候选 / 2 页」，引擎改成 6 后一直失败但没人发现
    // （CI 根本不跑本测试，2026-10-26 修复）。改为从响应里推导页容量。
    session.reset();
    Comp g1 = session.input("yi", false);
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
        session.input("yi", false);
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
    Comp c7 = session.input("hao", false);
    check(c7.ok, "后续按键继续可用");

    // 词组联想（2026-10-27 接入 Windows 壳）：上屏词的音节 → 续接词组 →
    // associateSelect 直接上屏（学习路径与 select 一致）。
    {
        session.reset();
        Comp n = session.input("ni", false);
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

    // userDelete（2026-11-01，候选窗右键「删除该词」的 IPC 层）：
    // 上屏产生学习记录 → 删除返回 deleted=true → 重复删除 deleted=false
    // （幂等，不报错）→ 连接仍可用。
    {
        session.reset();
        Comp u = session.input("nihao", false);
        bool learned = false;
        if (u.ok && !u.candidates.empty() && u.candidates[0].text == "你好") {
            std::string committed;
            learned = session.select(0, &committed) && committed == "你好";
        }
        check(learned, "userDelete 前置：上屏「你好」产生学习记录");
        bool deleted = false;
        check(session.userDelete("你好", {"ni", "hao"}, &deleted) && deleted,
              "userDelete 删除已学习词 deleted=true");
        bool repeated = true;
        check(session.userDelete("你好", {"ni", "hao"}, &repeated) && !repeated,
              "userDelete 重复删除 deleted=false（幂等）");
        Comp after = session.input("nihao", false);
        check(after.ok && !after.candidates.empty(), "userDelete 后连接仍可用");
        session.reset();
    }

    // 重连重建（issue #122）：壳侧请求超时后管道断开，daemon 会话随连接消失；
    // 新连接上 input 的 base 参数把「上次确认的输入串」整体重建后再追加 text，
    // 结果必须与逐键输入完全一致——否则用户已输入的拼音会丢、候选对不上。
    {
        // 参照：新会话逐键敲出 duanko
        UnixTransport refTransport(argv[1]);
        Session ref(refTransport);
        Comp refc;
        bool refOk = true;
        for (char key : std::string("duanko")) {
            refc = ref.input(std::string(1, key), false);
            refOk = refOk && refc.ok;
        }
        check(refOk && refc.input == "duanko" && !refc.candidates.empty(),
              "重连重建前置：逐键输入 duanko 有候选");

        // 模拟超时：旧连接上已确认 duank，随后管道断开（daemon 侧会话消失）
        session.reset();
        session.input("duank", false);
        transport.closeConn();
        // 新连接：base=duank + 追加 o，一次请求重建
        Comp rebuilt = session.input("o", false, "duank");
        check(rebuilt.ok && rebuilt.input == "duanko", "重连重建：base + 追加键得到完整输入串");
        bool same = rebuilt.candidates.size() == refc.candidates.size();
        for (std::size_t i = 0; same && i < refc.candidates.size(); ++i) {
            same = rebuilt.candidates[i].text == refc.candidates[i].text &&
                   rebuilt.candidates[i].syllables == refc.candidates[i].syllables &&
                   rebuilt.candidates[i].fromUser == refc.candidates[i].fromUser;
        }
        check(same && rebuilt.preedit == refc.preedit,
              "重连重建：候选/音节/学习标记/预编辑与逐键输入逐条一致");
        // 重建后的后续操作也必须一致：退格
        Comp rb = session.backspace();
        Comp rbb = ref.backspace();
        check(rb.ok && rbb.ok && rb.input == rbb.input, "重连重建后退格与逐键输入一致");

        // 不带 base 的调用点行为不变
        session.reset();
        Comp plain = session.input("nihao", false);
        check(plain.ok && plain.preedit == "ni'hao", "不带 base 的 input 行为不变");
    }

    // 2026-11-26 回归保护：consumePrefixFallback / dp_pending 解析 / lastCallWasTransportError 区分。
    {
        check(consumePrefixFallback("nihaoshijie", {"ni", "hao"}) == "shijie",
              "consumePrefixFallback: nihaoshijie 消费 ni+hao 剩 shijie");
        check(consumePrefixFallback("ni'hao'shijie", {"ni", "hao"}) == "shijie",
              "consumePrefixFallback: 自动跳过撇号分隔符");
        check(consumePrefixFallback("nvhao", {"nu"}) == "hao",
              "consumePrefixFallback: v/u 归一化");
        check(consumePrefixFallback("niounai", {"niu"}) == "nai",
              "consumePrefixFallback: 省写 iu 匹配 iou");
        check(consumePrefixFallback("gueihua", {"gui"}) == "hua",
              "consumePrefixFallback: 省写 ui 匹配 uei");
        check(consumePrefixFallback("duenhuang", {"dun"}) == "huang",
              "consumePrefixFallback: 省写 un 匹配 uen");
        check(consumePrefixFallback("duankou", {"ni"}) == "",
              "consumePrefixFallback: 前缀不匹配时视作整串消费返回空");

        bool jOk = false;
        JsonValue jPending = jsonParse(
            "{\"input\":\"\",\"preedit\":\"k\",\"dp_pending\":\"k\",\"candidates\":[]}", &jOk);
        Comp cPending = parseComposition(jPending);
        check(jOk && cPending.ok && cPending.dpPending == "k",
              "parseComposition 解析 dp_pending");

        JsonValue jPlain = jsonParse("{\"input\":\"ni\",\"preedit\":\"ni\"}", &jOk);
        Comp cPlain = parseComposition(jPlain);
        check(jOk && cPlain.ok && cPlain.dpPending.empty(),
              "parseComposition 缺失 dp_pending 时为空");

        ScriptedTransport st;
        Session ss(st);
        // 1) daemon 返回 JSON 业务 error：lastCallWasTransportError 必须为 false（不误断连）
        st.steps.push_back({false, frame("{\"id\":1,\"error\":\"invalid index\"}")});
        Comp selOut;
        bool selOk = ss.selectPrefix(99, &selOut);
        check(!selOk && !ss.lastCallWasTransportError() && !st.closeCalled,
              "业务 JSON error 后 lastCallWasTransportError=false 且不断连");

        // 2) 干净读超时：lastCallWasTransportError=true 且不断连
        st.resetSteps({{true, ""}});
        Comp to = ss.input("a", false);
        check(!to.ok && ss.lastCallWasTransportError() && !st.closeCalled,
              "干净读超时后 lastCallWasTransportError=true 且不断连");

        // 3) 下一次成功响应：清掉 lastCallWasTransportError（带迟到 id=2 帧先被丢弃）
        st.resetSteps({
            {false, frame("{\"id\":2,\"result\":{\"input\":\"a\",\"preedit\":\"a\"}}")},
            {false, frame("{\"id\":3,\"result\":{\"input\":\"ab\",\"preedit\":\"ab\"}}")},
        });
        Comp okAfter = ss.input("b", false);
        check(okAfter.ok && okAfter.input == "ab" && !ss.lastCallWasTransportError(),
              "恢复成功后 lastCallWasTransportError 清零并正确丢弃迟到帧");

        // 4) 写失败（传输故障）：lastCallWasTransportError=true 且断连
        st.failWrite = true;
        Comp wf = ss.input("c", false);
        check(!wf.ok && ss.lastCallWasTransportError() && st.closeCalled,
              "写失败时 lastCallWasTransportError=true 并断开连接");
    }

    if (failures == 0) {
        std::printf("PASS: core 全部检查通过\n");
        return 0;
    }
    std::fprintf(stderr, "FAIL: %d 项未通过\n", failures);
    return 1;
}
