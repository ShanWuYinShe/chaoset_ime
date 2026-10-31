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
    // 用户学习标记：daemon 的 Candidate.FromUser（JSON "from_user"）——
    // 该候选被用户调频/词组/上下文数据加权。macOS 候选窗在文本后画绿色
    // 「*」（CandidatePanel.Item.fromUser）；Windows 此前完全不解析该字段，
    // 三端视觉不一致（2026-10-30 三端一致性审计 A3）。
    bool fromUser = false;
};

struct Comp {
    bool ok = false;  // 通信是否成功；失败时壳层必须放行按键（降级透传）
    std::string input;
    std::string preedit;
    std::vector<Candidate> candidates;
    int page = 0;       // 当前候选页（0 起）
    int pageCount = 0;  // 总页数（无候选为 0）
    std::string committed;  // 渐进上屏（selectPrefix）返回的已上屏前缀段文本
    // 双拼方案名（IPC "shuangpin"；zrm/mspy/flypy）。daemon 启用双拼时每次
    // Compose 都会回填，壳据此把 ';' 路由给 input（引擎当组词键处理）而不是
    // punct——双拼引擎把 ';' 当键码参与两两成对解码。
    std::string shuangpin;
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

    // 处理输入。halfWidth 是壳本地持有的全/半角标点状态，随每次 input 下发
    // （IPC 可选字段 "half"）：双拼 dpPending==0 的 ';' 会走引擎 Punct 语义，
    // 宽度必须用本次壳状态，否则刚切全/半角后单按 ';' 会按旧宽度出标点
    // （1 键瞬态，2026-10-31 修复）。默认 false 仅供核心层测试的旧调用。
    Comp input(const std::string& text, bool halfWidth = false);
    Comp backspace();
    Comp reset();
    // 候选翻页：daemon 钳制边界；通信失败返回 ok=false。
    Comp nextPage();
    Comp prevPage();
    bool select(int index, std::string* committed);
    // 渐进上屏：选中候选后只消耗其音节对应的 input 前缀，剩余拼音回灌
    // 继续组词。成功时 *out 为剩余组词态（committed=已上屏前缀段文本，
    // input/preedit/candidates=剩余），失败（通信/越界）返回 false。
    bool selectPrefix(int index, Comp* out);
    // 标点键：daemon 侧统一映射中文标点（组词中先上屏首选候选）。
    // 标点键提交；halfWidth 由壳本地持有并随请求下发（2026-10-29 起
    // 全/半角开关不再经 daemon 往返，daemon 只按状态产出标点文本）。
    bool punct(const std::string& key, bool halfWidth, std::string* committed);
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
