// chaoset TSF 壳的可移植 IPC 核心：
// 协议帧（4 字节大端长度 + JSON）、极小 JSON 解析、会话控制逻辑。
// 本文件不依赖任何 OS API——同一份代码在 Windows DLL（命名管道传输）、
// Linux 核心层行为测试（Unix socket 传输）下编译运行，行为级测试对着
// 真实 chaosetd 完成。
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

// 传输抽象：平台壳各自提供实现——Windows 是命名管道，Linux 测试台是
// Unix socket，两者协议与帧格式完全相同。
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

// 用户学习数据统计（与 Go 侧 engine.UserStats 的 JSON 字段一致）。
struct UserStats {
    int words = 0;    // 静态词调频记录
    int phrases = 0;  // 用户合成词组
    int bigrams = 0;  // 前后词共现
};

// 会话：单连接内串行请求（输入法按键天然顺序）。
// 传输失败时对应调用返回 ok=false 并断开，下次调用由传输层重连。
class Session {
public:
    explicit Session(Transport& t) : t_(t) {}

    // 处理输入。halfWidth 是壳持有的全/半角标点状态，随每次请求下发
    // （IPC 字段 "half"）：双拼 dpPending==0 的 ';' 会走引擎 Punct 语义，
    // 宽度必须用本次壳状态，否则刚切全/半角后单按 ';' 会按旧宽度出标点。
    // **必填、无默认值**：daemon 不保存该状态（2026-11-05），壳是唯一真源，
    // 调用方必须显式传宽度，避免漏传时静默退化成全角。
    Comp input(const std::string& text, bool halfWidth);
    Comp backspace();
    // 按音节回退（Alt+Backspace，对齐 macOS ⌥⌫ 与引擎 BackspaceWord，2026-10-31）。
    Comp backspaceWord();
    // reset 清空组词**与**学习上下文（prev/history）——对应 Esc/失焦/新会话。
    Comp reset();
    // resetComposition 只丢弃当前组词、保留 prev/history：切中英模式、
    // 同一会话内的输入流打断走这个，避免引擎忘记刚上屏的字影响打分。
    Comp resetComposition();
    // 候选翻页：daemon 钳制边界；通信失败返回 ok=false。
    Comp nextPage();
    Comp prevPage();
    bool select(int index, std::string* committed);
    // 渐进上屏：选中候选后只消耗其音节对应的 input 前缀，剩余拼音回灌
    // 继续组词。成功时 *out 为剩余组词态（committed=已上屏前缀段文本，
    // input/preedit/candidates=剩余），失败（通信/越界）返回 false。
    bool selectPrefix(int index, Comp* out);
    // 标点键：daemon 侧统一映射中文标点（组词中先上屏首选候选）。
    // halfWidth 由壳持有并随每次请求下发，daemon 只按该参数产出文本、
    // 不保存任何副本（2026-11-05）。
    bool punct(const std::string& key, bool halfWidth, std::string* committed);
    // 词组联想：按上屏词音节查续接词组（响应结构与 Composition 同构，
    // candidates 为联想列表）；通信失败返回 ok=false。
    bool associate(const std::vector<std::string>& sylls, Comp* out);
    // 联想候选直接上屏：学习路径与 select 完全一致（调频/共现/词组进化）。
    bool associateSelect(const std::string& word, const std::vector<std::string>& sylls,
                         std::string* committed);

    // 学习数据管理（设置窗口 GUI，2026-10-31）：普通用户不再需要命令行。
    // 经同一 IPC 通道作用于 daemon（内存与磁盘一起生效，无覆盖竞态）。
    bool userStats(UserStats* out);
    bool userClear();
    // 按词删除学习数据（候选窗右键「删除该词」/ chaoset-userdict delete）：
    // word + 音节序列 → daemon 的 engine.UserDict.DeleteWord。返回 false 表示
    // 通信失败；*deleted 表示 daemon 侧是否真的删到了记录（幂等，重复删除为
    // false 但不是错误）。写类操作，调用方不得重试。
    bool userDelete(const std::string& word, const std::vector<std::string>& sylls,
                    bool* deleted);
    // 导出学习数据 TSV：daemon 单帧上限 1 MiB（2026-10-30 起分页），
    // 这里按 offset/limit 循环拼接，重度用户的大数据也能完整导出。
    bool userExportAll(bool phrasesOnly, std::string* out);

    // 请求 daemon 代拉起单例 UI 宿主（chaoset-ui.exe，Windows 专用兜底）：
    // AppContainer 宿主（开始菜单/搜索）内 TIP 不能 CreateProcess，daemon 以
    // 同一用户、普通完整性运行，创建的子进程不会继承 AC 身份。
    // 返回 true 表示 daemon 已成功发起；非 Windows 平台恒 false（无壳调用）。
    bool ensureUiHost();

private:
    JsonValue call(const std::string& method, const std::string& params, bool* ok);
    Transport& t_;
    std::uint64_t nextID_ = 0;
};

}  // namespace chaoset
