// chaoset TSF 壳的可移植 IPC 核心：
// 协议帧（4 字节大端长度 + JSON）、极小 JSON 解析、会话控制逻辑。
// 本文件不依赖任何 OS API——同一份代码在 Windows DLL（TCP 回环传输）
// 与 macOS 测试台（Unix socket / TCP）下编译运行，行为级测试在 macOS
// 上对着真实 chaosetd 完成。
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <utility>
#include <vector>

namespace chaoset {

struct Candidate {
    std::string text;
    std::string syllables;  // 撇号分隔的音节串（如 xian'zai），联想续接需要
};

struct Comp {
    bool ok = false;  // 通信是否成功；失败时壳层必须放行按键（降级透传）
    std::string input;
    std::string preedit;
    std::vector<Candidate> candidates;
    int page = 0;       // 当前候选页（0 起）
    int pageCount = 0;  // 总页数（无候选为 0）
};

// 传输抽象：Windows 下是 TCP 回环（与 daemon -tcp 配对），
// macOS 测试台下是 Unix socket / TCP 皆可。
class Transport {
public:
    virtual ~Transport() = default;
    virtual bool writeAll(const std::uint8_t* data, std::size_t len) = 0;
    virtual bool readAll(std::uint8_t* buf, std::size_t len) = 0;
    virtual void closeConn() = 0;
};

// 极小 JSON 值树：足以解析 daemon 响应（对象/数组/字符串/数字/字面量）。
struct JsonValue {
    enum class Type { Null, Bool, Num, Str, Arr, Obj };
    Type type = Type::Null;
    bool boolean = false;
    double number = 0.0;
    std::string str;
    std::vector<JsonValue> arr;
    std::vector<std::pair<std::string, JsonValue>> members;  // 键查找线性即可

    const JsonValue* find(const std::string& key) const {
        for (const auto& kv : members) {
            if (kv.first == key) return &kv.second;
        }
        return nullptr;
    }
    bool isString(const std::string& key, std::string* out) const {
        const JsonValue* v = find(key);
        if (v == nullptr || v->type != Type::Str) return false;
        if (out != nullptr) *out = v->str;
        return true;
    }
};

// 解析整个文本；失败时 *ok=false。
JsonValue jsonParse(const std::string& text, bool* ok);
std::string jsonEscape(const std::string& s);

// 帧与请求构造（与 Go 侧 ipc 包协议一致）
std::string buildRequest(std::uint64_t id, const std::string& method,
                         const std::string& paramsJson);  // paramsJson 为完整 JSON 值文本，空串则省略
Comp parseComposition(const JsonValue& result);
std::string parseCommitted(const JsonValue& result);

// 会话：单连接内串行请求（输入法按键天然顺序）。
// 传输失败时对应调用返回 ok=false 并断开，下次调用由传输层重连。
class Session {
public:
    explicit Session(Transport& t) : t_(t) {}

    Comp input(const std::string& text);
    Comp backspace();
    Comp reset();
    // 候选翻页：daemon 钳制边界；通信失败返回 ok=false。
    Comp nextPage();
    Comp prevPage();
    bool select(int index, std::string* committed);
    // 标点键：daemon 侧统一映射中文标点（组词中先上屏首选候选）。
    bool punct(const std::string& key, std::string* committed);
    // 全角/半角标点模式切换，返回切换后是否为半角；通信失败返回 false。
    bool toggleHalfWidth();
    // 词组联想：按上屏词音节查续接词组（响应结构与 Composition 同构，
    // candidates 为联想列表）；通信失败返回 ok=false。
    bool associate(const std::vector<std::string>& sylls, Comp* out);
    // 联想候选直接上屏：学习路径与 select 完全一致（调频/共现/词组进化）。
    bool associateSelect(const std::string& word, const std::vector<std::string>& sylls,
                         std::string* committed);

private:
    JsonValue call(const std::string& method, const std::string& params, bool* ok);
    Transport& t_;
    std::uint64_t nextID_ = 0;
};

}  // namespace chaoset
