#include "core.hpp"

#include <cstdio>
#include <cstdlib>

namespace chaoset {
namespace {

constexpr std::uint32_t kMaxMessageSize = 1u << 20;

struct Parser {
    const std::string& s;
    std::size_t i = 0;
    bool ok = true;

    explicit Parser(const std::string& text) : s(text) {}

    void ws() {
        while (i < s.size()) {
            char c = s[i];
            if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
                ++i;
            } else {
                break;
            }
        }
    }

    bool eat(char c) {
        ws();
        if (i < s.size() && s[i] == c) {
            ++i;
            return true;
        }
        return false;
    }

    void utf16ToUtf8(std::uint32_t cp, std::string* out) {
        if (cp < 0x80) {
            out->push_back(static_cast<char>(cp));
        } else if (cp < 0x800) {
            out->push_back(static_cast<char>(0xC0 | (cp >> 6)));
            out->push_back(static_cast<char>(0x80 | (cp & 0x3F)));
        } else if (cp < 0x10000) {
            out->push_back(static_cast<char>(0xE0 | (cp >> 12)));
            out->push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3F)));
            out->push_back(static_cast<char>(0x80 | (cp & 0x3F)));
        } else {
            out->push_back(static_cast<char>(0xF0 | (cp >> 18)));
            out->push_back(static_cast<char>(0x80 | ((cp >> 12) & 0x3F)));
            out->push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3F)));
            out->push_back(static_cast<char>(0x80 | (cp & 0x3F)));
        }
    }

    std::string parseString() {
        std::string out;
        if (!eat('"')) {
            ok = false;
            return out;
        }
        while (i < s.size()) {
            char c = s[i++];
            if (c == '"') return out;
            if (c != '\\') {
                out.push_back(c);
                continue;
            }
            if (i >= s.size()) break;
            char e = s[i++];
            switch (e) {
                case '"': out.push_back('"'); break;
                case '\\': out.push_back('\\'); break;
                case '/': out.push_back('/'); break;
                case 'b': out.push_back('\b'); break;
                case 'f': out.push_back('\f'); break;
                case 'n': out.push_back('\n'); break;
                case 'r': out.push_back('\r'); break;
                case 't': out.push_back('\t'); break;
                case 'u': {
                    if (i + 4 > s.size()) { ok = false; return out; }
                    std::uint32_t cp = 0;
                    for (int k = 0; k < 4; ++k) {
                        char h = s[i++];
                        cp <<= 4;
                        if (h >= '0' && h <= '9') cp |= static_cast<std::uint32_t>(h - '0');
                        else if (h >= 'a' && h <= 'f') cp |= static_cast<std::uint32_t>(h - 'a' + 10);
                        else if (h >= 'A' && h <= 'F') cp |= static_cast<std::uint32_t>(h - 'A' + 10);
                        else { ok = false; return out; }
                    }
                    // 代理对：高代理后必须跟 \uXXXX 低代理
                    if (cp >= 0xD800 && cp <= 0xDBFF && i + 6 <= s.size() && s[i] == '\\' && s[i + 1] == 'u') {
                        std::uint32_t lo = 0;
                        std::size_t j = i + 2;
                        bool valid = true;
                        for (int k = 0; k < 4; ++k) {
                            char h = s[j++];
                            lo <<= 4;
                            if (h >= '0' && h <= '9') lo |= static_cast<std::uint32_t>(h - '0');
                            else if (h >= 'a' && h <= 'f') lo |= static_cast<std::uint32_t>(h - 'a' + 10);
                            else if (h >= 'A' && h <= 'F') lo |= static_cast<std::uint32_t>(h - 'A' + 10);
                            else { valid = false; break; }
                        }
                        if (valid && lo >= 0xDC00 && lo <= 0xDFFF) {
                            i = j;
                            cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
                        }
                    }
                    utf16ToUtf8(cp, &out);
                    break;
                }
                default:
                    ok = false;
                    return out;
            }
        }
        ok = false;
        return out;
    }

    double parseNumber() {
        ws();
        std::size_t start = i;
        while (i < s.size()) {
            char c = s[i];
            if ((c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E') {
                ++i;
            } else {
                break;
            }
        }
        if (i == start) { ok = false; return 0; }
        return std::strtod(s.c_str() + start, nullptr);
    }

    JsonValue parseValue(int depth) {
        JsonValue v;
        if (depth > 32) { ok = false; return v; }
        ws();
        if (i >= s.size()) { ok = false; return v; }
        char c = s[i];
        if (c == '{') {
            ++i;
            v.type = JsonValue::Type::Obj;
            if (eat('}')) return v;
            while (true) {
                ws();
                std::string key = parseString();
                if (!ok || !eat(':')) { ok = false; return v; }
                v.members.emplace_back(std::move(key), parseValue(depth + 1));
                if (!ok) return v;
                if (eat(',')) continue;
                if (eat('}')) return v;
                ok = false;
                return v;
            }
        }
        if (c == '[') {
            ++i;
            v.type = JsonValue::Type::Arr;
            if (eat(']')) return v;
            while (true) {
                v.arr.push_back(parseValue(depth + 1));
                if (!ok) return v;
                if (eat(',')) continue;
                if (eat(']')) return v;
                ok = false;
                return v;
            }
        }
        if (c == '"') {
            v.type = JsonValue::Type::Str;
            v.str = parseString();
            return v;
        }
        if (s.compare(i, 4, "true") == 0) { i += 4; v.type = JsonValue::Type::Bool; v.boolean = true; return v; }
        if (s.compare(i, 5, "false") == 0) { i += 5; v.type = JsonValue::Type::Bool; v.boolean = false; return v; }
        if (s.compare(i, 4, "null") == 0) { i += 4; return v; }
        v.type = JsonValue::Type::Num;
        v.number = parseNumber();
        return v;
    }
};

}  // namespace

JsonValue jsonParse(const std::string& text, bool* ok) {
    Parser p(text);
    JsonValue v = p.parseValue(0);
    if (ok != nullptr) *ok = p.ok;
    return v;
}

std::string jsonEscape(const std::string& s) {
    std::string out;
    out.reserve(s.size() + 8);
    char buf[8];
    for (unsigned char c : s) {
        switch (c) {
            case '"': out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\b': out += "\\b"; break;
            case '\f': out += "\\f"; break;
            case '\n': out += "\\n"; break;
            case '\r': out += "\\r"; break;
            case '\t': out += "\\t"; break;
            default:
                if (c < 0x20) {
                    std::snprintf(buf, sizeof(buf), "\\u%04x", c);
                    out += buf;
                } else {
                    out.push_back(static_cast<char>(c));  // UTF-8 原样透传
                }
        }
    }
    return out;
}

std::string buildRequest(std::uint64_t id, const std::string& method, const std::string& paramsJson) {
    std::string out = "{\"id\":";
    out += std::to_string(id);
    out += ",\"method\":\"";
    out += jsonEscape(method);
    out += "\"";
    if (!paramsJson.empty()) {
        out += ",\"params\":";
        out += paramsJson;
    }
    out += "}";
    return out;
}

Comp parseComposition(const JsonValue& result) {
    Comp c;
    if (result.type != JsonValue::Type::Obj) return c;
    c.ok = true;
    result.isString("input", &c.input);
    result.isString("preedit", &c.preedit);
    if (const JsonValue* p = result.find("page"); p != nullptr && p->type == JsonValue::Type::Num) {
        c.page = static_cast<int>(p->number);
    }
    if (const JsonValue* pc = result.find("page_count"); pc != nullptr && pc->type == JsonValue::Type::Num) {
        c.pageCount = static_cast<int>(pc->number);
    }
    const JsonValue* cands = result.find("candidates");
    if (cands != nullptr && cands->type == JsonValue::Type::Arr) {
        for (const JsonValue& item : cands->arr) {
            std::string text;
            if (item.isString("text", &text)) {
                c.candidates.push_back(Candidate{text});
            }
        }
    }
    return c;
}

std::string parseCommitted(const JsonValue& result) {
    std::string out;
    result.isString("committed", &out);
    return out;
}

JsonValue Session::call(const std::string& method, const std::string& params, bool* ok) {
    JsonValue none;
    if (ok != nullptr) *ok = false;
    ++nextID_;
    std::string req = buildRequest(nextID_, method, params);

    std::uint32_t len = static_cast<std::uint32_t>(req.size());
    std::uint8_t header[4] = {
        static_cast<std::uint8_t>((len >> 24) & 0xFF), static_cast<std::uint8_t>((len >> 16) & 0xFF),
        static_cast<std::uint8_t>((len >> 8) & 0xFF), static_cast<std::uint8_t>(len & 0xFF)};
    if (!t_.writeAll(header, 4) || !t_.writeAll(reinterpret_cast<const std::uint8_t*>(req.data()), req.size())) {
        t_.closeConn();
        return none;
    }
    std::uint8_t head[4];
    if (!t_.readAll(head, 4)) {
        t_.closeConn();
        return none;
    }
    std::uint32_t size = (static_cast<std::uint32_t>(head[0]) << 24) | (static_cast<std::uint32_t>(head[1]) << 16) |
                         (static_cast<std::uint32_t>(head[2]) << 8) | static_cast<std::uint32_t>(head[3]);
    if (size == 0 || size > kMaxMessageSize) {
        t_.closeConn();
        return none;
    }
    std::string body(size, '\0');
    if (!t_.readAll(reinterpret_cast<std::uint8_t*>(&body[0]), size)) {
        t_.closeConn();
        return none;
    }
    bool parsed = false;
    JsonValue resp = jsonParse(body, &parsed);
    if (!parsed || resp.type != JsonValue::Type::Obj) {
        t_.closeConn();
        return none;
    }
    const JsonValue* err = resp.find("error");
    if (err != nullptr && err->type != JsonValue::Type::Null) {
        return none;  // 业务错误：连接保持，调用方按失败处理
    }
    const JsonValue* result = resp.find("result");
    if (ok != nullptr) *ok = true;
    if (result != nullptr) return *result;
    return JsonValue();
}

Comp Session::input(const std::string& text) {
    bool ok = false;
    JsonValue result = call("input", "{\"text\":\"" + jsonEscape(text) + "\"}", &ok);
    if (!ok) return Comp();
    return parseComposition(result);
}

Comp Session::backspace() {
    bool ok = false;
    JsonValue result = call("backspace", "", &ok);
    if (!ok) return Comp();
    return parseComposition(result);
}

Comp Session::reset() {
    bool ok = false;
    JsonValue result = call("reset", "", &ok);
    if (!ok) return Comp();
    return parseComposition(result);
}

Comp Session::nextPage() {
    bool ok = false;
    JsonValue result = call("nextPage", "", &ok);
    if (!ok) return Comp();
    return parseComposition(result);
}

Comp Session::prevPage() {
    bool ok = false;
    JsonValue result = call("prevPage", "", &ok);
    if (!ok) return Comp();
    return parseComposition(result);
}

bool Session::select(int index, std::string* committed) {
    bool ok = false;
    JsonValue result = call("select", "{\"index\":" + std::to_string(index) + "}", &ok);
    if (!ok) return false;
    std::string text = parseCommitted(result);
    if (text.empty()) return false;
    *committed = text;
    return true;
}

bool Session::punct(const std::string& key, std::string* committed) {
    bool ok = false;
    JsonValue result = call("punct", "{\"key\":\"" + jsonEscape(key) + "\"}", &ok);
    if (!ok) return false;
    std::string text = parseCommitted(result);
    if (text.empty()) return false;
    *committed = text;
    return true;
}

}  // namespace chaoset
