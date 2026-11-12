
// Linux 原生 ibus 引擎（C++/libibus）：只做 IBus/按键/预编辑/候选表 UI，
// 拼音与联想算法全部通过 Unix socket 调用 Go chaosetd。与 macOS/Windows 壳同构。
#include <ibus.h>
#include <glib.h>

#include <algorithm>
#include <cstdio>
#include <cstring>
#include <string>
#include <utility>
#include <vector>

#include "core.hpp"
#include "daemon.hpp"

namespace {

constexpr guint kPageSize = 6;
constexpr guint kKeysymShiftLeft = 0xFFE1;
constexpr guint kKeysymShiftRight = 0xFFE2;
constexpr guint kKeysymBackspace = 0xFF08;
constexpr guint kKeysymDelete = 0xFFFF;
constexpr guint kKeysymReturn = 0xFF0D;
constexpr guint kKeysymEscape = 0xFF1B;
constexpr guint kKeysymPageUp = 0xFF55;
constexpr guint kKeysymPageDown = 0xFF56;
// 方向键（issue#2）：候选 lookup 显示时移动页内预选，空格确认 cursor 候选。
constexpr guint kKeysymLeft = 0xFF51, kKeysymUp = 0xFF52, kKeysymRight = 0xFF53, kKeysymDown = 0xFF54;
constexpr guint kKeysymKP1 = 0xFFB1;
constexpr guint kKeysymKP9 = 0xFFB9;

void logLine(const std::string& text) {
    std::fprintf(stderr, "chaoset-engine: %s\n", text.c_str());
}

bool isShiftKeyval(guint keyval) {
    return keyval == kKeysymShiftLeft || keyval == kKeysymShiftRight;
}

bool isArrowKey(guint keyval) {
    return keyval == kKeysymLeft || keyval == kKeysymUp || keyval == kKeysymRight || keyval == kKeysymDown;
}

bool isPunctKey(guint keyval) {
    switch (keyval) {
        case ',': case '.': case ';': case ':': case '?': case '!':
        case '(': case ')': case '[': case ']': case '<': case '>':
        case '\\': case '~': case '^': case '"': case '\'':
            return true;
        default:
            return false;
    }
}

struct ShiftTracker {
    int shiftDown = 0;
    bool sinceShift = false;

    void reset() { shiftDown = 0; sinceShift = false; }

    bool feed(guint keyval, guint state) {
        if (isShiftKeyval(keyval)) {
            if ((state & IBUS_RELEASE_MASK) != 0) {
                if (shiftDown == 0) return false;
                --shiftDown;
                const bool toggle = shiftDown == 0 && !sinceShift;
                if (shiftDown == 0) sinceShift = false;
                return toggle;
            }
            ++shiftDown;
            sinceShift = false;
            return false;
        }
        sinceShift = true;
        return false;
    }
};

struct EngineOptions {
    std::vector<std::string> dicts;
    std::string importedDict;
    std::string userdict;
    std::string socket;
    std::string config;
    std::string daemonPath;
    std::string log;
    bool fuzzy = false;
    bool traditional = false;
    std::string shuangpin;
    bool associate = true;
    bool associateFlagSet = false;
};

EngineOptions gOptions;

struct UserConfig {
    bool fuzzy = false;
    bool traditional = false;
    std::string shuangpin;
    bool associate = true;
};

bool readUserConfig(const std::string& path, UserConfig* out) {
    if (path.empty()) return true;
    gchar* contents = nullptr;
    gsize len = 0;
    GError* error = nullptr;
    if (!g_file_get_contents(path.c_str(), &contents, &len, &error)) {
        if (error != nullptr) g_error_free(error);
        return true;  // 文件不存在=默认
    }
    bool ok = false;
    chaoset::JsonValue root = chaoset::jsonParse(std::string(contents, len), &ok);
    g_free(contents);
    if (!ok || root.type != chaoset::JsonValue::Type::Obj) return false;
    if (const auto* v = root.find("fuzzy"); v && v->type == chaoset::JsonValue::Type::Bool) out->fuzzy = v->boolean;
    if (const auto* v = root.find("traditional"); v && v->type == chaoset::JsonValue::Type::Bool) out->traditional = v->boolean;
    if (const auto* v = root.find("shuangpin"); v && v->type == chaoset::JsonValue::Type::Str) out->shuangpin = v->str;
    if (const auto* v = root.find("associate"); v && v->type == chaoset::JsonValue::Type::Bool) out->associate = v->boolean;
    return true;
}

std::string executableDir() {
    gchar* exe = g_file_read_link("/proc/self/exe", nullptr);
    if (exe == nullptr) return std::string();
    gchar* dir = g_path_get_dirname(exe);
    std::string out = dir == nullptr ? std::string() : std::string(dir);
    g_free(dir);
    g_free(exe);
    return out;
}

bool knownDpScheme(const std::string& scheme) {
    // 与 engine/double_pinyin.go 的 DoublePinyinSchemes 保持一致。无效方案
    // 不能让 ';' 被当作双拼键，否则用户会打不出中文分号。
    return scheme == "zrm" || scheme == "mspy" || scheme == "flypy";
}

std::vector<std::string> splitSyllables(const std::string& s) {
    std::vector<std::string> out;
    std::string cur;
    for (char c : s) {
        if (c == '\'') {
            if (!cur.empty()) { out.push_back(cur); cur.clear(); }
        } else {
            cur.push_back(c);
        }
    }
    if (!cur.empty()) out.push_back(cur);
    return out;
}

class IbusEngineState {
public:
    explicit IbusEngineState(EngineOptions options, IBusEngine* engine)
        : engine_(engine),
          daemon_(makeDaemonOptions(std::move(options))),
          configPath_(daemon_.paths().config),
          assocOn_(gOptions.associate),
          assocFlagSet_(gOptions.associateFlagSet),
          dpScheme_(knownDpScheme(gOptions.shuangpin) ? gOptions.shuangpin : std::string()) {
        UserConfig cfg;
        if (readUserConfig(configPath_, &cfg)) {
            if (!assocFlagSet_) assocOn_ = cfg.associate;
        }
    }

    ~IbusEngineState() {
        if (toastId_ != 0) { g_source_remove(toastId_); toastId_ = 0; }
    }

    bool processKeyEvent(guint keyval, guint keycode, guint state) {
        (void)keycode;
        if (shift_.feed(keyval, state)) {
            enMode_ = !enMode_;
            if (!confirmed_.empty()) commit(confirmed_);
            exitAssociate();
            sessionResetComposition();
            hidePreedit();
            hideLookupTable();
            showToast(enMode_ ? "英文模式" : "中文模式");
            return false;
        }
        if (enMode_) return false;
        if ((state & IBUS_RELEASE_MASK) != 0) return false;

        if ((state & IBUS_MOD4_MASK) != 0) {
            if (isComposing() && (keyval == kKeysymBackspace || keyval == kKeysymDelete)) {
                confirmed_.clear();
                sessionResetComposition();
                hidePreedit();
                hideLookupTable();
                return true;
            }
            flushConfirmedAndReset();
            return false;
        }
        if ((state & (IBUS_CONTROL_MASK | IBUS_MOD1_MASK)) != 0) {
            if (isComposing() && (keyval == kKeysymBackspace || keyval == kKeysymDelete)) {
                if ((state & (IBUS_CONTROL_MASK | IBUS_SHIFT_MASK)) != 0) {
                    confirmed_.clear();
                    sessionResetComposition();
                    hidePreedit();
                    hideLookupTable();
                    return true;
                }
                chaoset::Comp comp = callComp([&] { return daemon_.session().backspaceWord(); });
                if (comp.input.empty() && !confirmed_.empty()) {
                    commit(confirmed_);
                    sessionResetComposition();
                    return true;
                }
                refresh(std::move(comp));
                return true;
            }
            if ((state & IBUS_CONTROL_MASK) != 0 && isComposing() && keyval == 'u') {
                confirmed_.clear();
                sessionResetComposition();
                hidePreedit();
                hideLookupTable();
                return true;
            }
            flushConfirmedAndReset();
            return false;
        }

        if (!assoc_.empty()) {
            const bool isDigit = (keyval >= '1' && keyval <= '9') ||
                                 (keyval >= kKeysymKP1 && keyval <= kKeysymKP9);
            if (keyval == kKeysymEscape || keyval == kKeysymReturn || keyval == kKeysymBackspace) {
                exitAssociate();
                return true;
            }
            if (isArrowKey(keyval)) {
                if (keyval == kKeysymUp || keyval == kKeysymDown) {
                    if (flipAssociate(keyval == kKeysymDown)) { lookupIndex_ = 0; showAssociateTable(); }
                } else {
                    moveLookup(keyval == kKeysymRight ? 1 : -1);
                }
                return true;
            }
            if (keyval == ' ') {
                if (selectCandidate(lookupIndex_)) return true;
                exitAssociate();
                return true;
            }
            if (keyval == '-' || keyval == '=' || keyval == '[' || keyval == ']') {
                flipAssociate(keyval == '=' || keyval == ']');
                return true;
            }
            if (keyval == kKeysymPageUp || keyval == kKeysymPageDown) {
                flipAssociate(keyval == kKeysymPageDown);
                return true;
            }
            if (isDigit) {
                int idx = (keyval >= kKeysymKP1) ? static_cast<int>(keyval - kKeysymKP1)
                                                 : static_cast<int>(keyval - '1');
                if (selectCandidate(idx)) return true;
                exitAssociate();
                return false;
            }
            exitAssociate();
        }

        if (!dpScheme_.empty() && keyval == ';' && (state & IBUS_SHIFT_MASK) == 0) {
            chaoset::Comp comp = callComp([&] { return daemon_.session().input(";", halfWidth_); });
            if (!comp.committed.empty()) {
                commit(confirmed_ + comp.committed);
            } else {
                refresh(std::move(comp));
            }
            return true;
        }

        if (keyval >= 'a' && keyval <= 'z') {
            const std::string text(1, static_cast<char>(keyval));
            refresh(callComp([&] { return daemon_.session().input(text, halfWidth_); }));
            return true;
        }
        if (keyval == kKeysymBackspace || keyval == kKeysymDelete) {
            if (!isComposing()) return false;
            if ((state & IBUS_SHIFT_MASK) != 0) {
                confirmed_.clear();
                sessionResetComposition();
                hidePreedit();
                hideLookupTable();
                return true;
            }
            chaoset::Comp comp = callComp([&] { return daemon_.session().backspace(); });
            if (comp.input.empty() && !confirmed_.empty()) {
                commit(confirmed_);
                sessionResetComposition();
                return true;
            }
            refresh(std::move(comp));
            return true;
        }
        if (keyval == kKeysymEscape) {
            if (isComposing() || !confirmed_.empty()) {
                confirmed_.clear();
                sessionResetComposition();
                hidePreedit();
                hideLookupTable();
                return true;
            }
            return false;
        }
        if ((keyval >= '1' && keyval <= '9') || (keyval >= kKeysymKP1 && keyval <= kKeysymKP9)) {
            int idx = (keyval >= kKeysymKP1) ? static_cast<int>(keyval - kKeysymKP1)
                                             : static_cast<int>(keyval - '1');
            return selectCandidate(idx);
        }
        if (keyval == kKeysymReturn) {
            if (isComposing() || !confirmed_.empty()) {
                const std::string raw = confirmed_ + last_.input;
                confirmed_.clear();
                sessionResetComposition();
                hidePreedit();
                hideLookupTable();
                commit(raw);
                return true;
            }
            return false;
        }
        if (keyval == ' ') {
            if ((state & IBUS_SHIFT_MASK) != 0) {
                if (!isComposing() && assoc_.empty()) return false;
                halfWidth_ = !halfWidth_;
                showToast(halfWidth_ ? "半角标点" : "全角标点");
                return true;
            }
            if (!isComposing()) return false;
            if (!last_.candidates.empty()) {
                if (selectCandidate(lookupIndex_)) return true;
                const std::string raw = last_.input;
                const std::string conf = confirmed_;
                confirmed_.clear();
                sessionResetComposition();
                hidePreedit();
                hideLookupTable();
                commit(conf + raw);
                return true;
            }
            const std::string raw = last_.input;
            const std::string conf = confirmed_;
            confirmed_.clear();
            sessionResetComposition();
            hidePreedit();
            hideLookupTable();
            commit(conf + raw);
            return true;
        }
        if (isArrowKey(keyval) && isComposing() && !last_.candidates.empty()) {
            if (keyval == kKeysymUp || keyval == kKeysymDown) {
                flipPage(keyval == kKeysymDown);
            } else {
                moveLookup(keyval == kKeysymRight ? 1 : -1);
            }
            return true;
        }
        if (keyval == kKeysymPageUp || keyval == kKeysymPageDown) {
            if (!isComposing()) return false;
            if (last_.candidates.empty()) {
                flushConfirmedAndReset();
                return false;
            }
            chaoset::Comp comp = callComp([&] {
                return keyval == kKeysymPageUp ? daemon_.session().prevPage()
                                                : daemon_.session().nextPage();
            });
            refresh(std::move(comp));
            return true;
        }
        if (keyval == '-' || keyval == '=' || keyval == '[' || keyval == ']') {
            if (isComposing() && !last_.candidates.empty()) {
                chaoset::Comp comp = callComp([&] {
                    return (keyval == '-' || keyval == '[') ? daemon_.session().prevPage()
                                                            : daemon_.session().nextPage();
                });
                refresh(std::move(comp));
                return true;
            }
            if (isPunctKey(keyval)) {
                std::string text;
                if (!daemon_.session().punct(std::string(1, static_cast<char>(keyval)), halfWidth_, &text)) return false;
                commit(confirmed_ + text);
                return true;
            }
            flushConfirmedAndReset();
            return false;
        }
        if (keyval == '\'' && isComposing()) {
            refresh(callComp([&] { return daemon_.session().input("'", halfWidth_); }));
            return true;
        }
        if (isPunctKey(keyval)) {
            std::string text;
            if (!daemon_.session().punct(std::string(1, static_cast<char>(keyval)), halfWidth_, &text)) return false;
            commit(confirmed_ + text);
            return true;
        }
        if (isComposing() || !confirmed_.empty()) flushConfirmedAndReset();
        return false;
    }

    void focusIn() { shift_.reset(); }
    void focusOut() {
        if (!confirmed_.empty()) commit(confirmed_);
        sessionReset();
        shift_.reset();
        exitAssociate();
        hidePreedit();
        hideLookupTable();
        last_ = chaoset::Comp();
    }
    void reset() {
        if (!confirmed_.empty()) commit(confirmed_);
        sessionReset();
        exitAssociate();
        hidePreedit();
        hideLookupTable();
        last_ = chaoset::Comp();
    }
    void pageUp() {
        if (!assoc_.empty()) { flipAssociate(false); return; }
        refresh(callComp([&] { return daemon_.session().prevPage(); }));
    }
    void pageDown() {
        if (!assoc_.empty()) { flipAssociate(true); return; }
        refresh(callComp([&] { return daemon_.session().nextPage(); }));
    }
    void candidateClicked(guint index) {
        if (!selectCandidate(static_cast<int>(index)) && !assoc_.empty()) exitAssociate();
    }

private:
    static chaoset::Daemon::Options makeDaemonOptions(EngineOptions options) {
        chaoset::Daemon::Options dopts;
        dopts.paths = chaoset::defaultLinuxPaths();
        if (!options.socket.empty()) dopts.paths.socket = options.socket;
        if (!options.userdict.empty()) dopts.paths.userdict = options.userdict;
        if (!options.importedDict.empty()) dopts.paths.importedDict = options.importedDict;
        if (!options.log.empty()) dopts.paths.log = options.log;
        dopts.daemonPath = options.daemonPath;
        dopts.dicts = options.dicts;
        dopts.importedDict = dopts.paths.importedDict;
        dopts.userdict = dopts.paths.userdict;
        dopts.log = dopts.paths.log;
        dopts.fuzzy = options.fuzzy;
        dopts.traditional = options.traditional;
        dopts.shuangpin = options.shuangpin;
        return dopts;
    }

    bool ensure() {
        if (!daemon_.ensureReady()) {
            if (!warnedIpc_) {
                warnedIpc_ = true;
                logLine("daemon 未就绪，按键先按英文透传；下次按键会重试");
            }
            return false;
        }
        return true;
    }

    template <typename Fn>
    chaoset::Comp callComp(Fn fn) {
        if (!ensure()) return chaoset::Comp();
        chaoset::Comp comp = fn();
        if (!comp.ok) {
            logLine("IPC 请求失败，已断开 daemon 连接");
            return chaoset::Comp();
        }
        return comp;
    }

    bool isComposing() const { return !last_.input.empty(); }

    void sessionReset() {
        if (ensure()) (void)daemon_.session().reset();
        last_ = chaoset::Comp();
    }

    // 只丢弃当前组词、保留学习上下文（prev/history）。
    // 「用户还会继续打字」的场景走这个（切中英、退格删空、Esc、全部删除、
    // 顶屏、候选越界等）；reset 只留给会话真正结束（focusOut）的场景。
    // 用 reset 会让引擎忘记刚上屏的字，后续候选失去 bigram 加成（2026-11-05）。
    void sessionResetComposition() {
        if (ensure()) (void)daemon_.session().resetComposition();
        last_ = chaoset::Comp();
    }
    // cursor：页内预选序号（默认 0 = 新输入/翻页/选词后回到第一项）。
    void refresh(chaoset::Comp comp, int cursor = 0) {
        last_ = std::move(comp);
        lookupIndex_ = cursor;
        const std::string preedit = confirmed_ + last_.preedit;
        updatePreedit(preedit, !preedit.empty());
        updateLookupTable(last_);
    }

    void updatePreedit(const std::string& text, bool visible) {
        if (engine_ == nullptr) return;
        if (!visible) { hidePreedit(); return; }
        IBusText* t = ibus_text_new_from_string(text.c_str());
        if (t == nullptr) return;
        ibus_text_append_attribute(t, IBUS_ATTR_TYPE_UNDERLINE, IBUS_ATTR_UNDERLINE_SINGLE, 0, -1);
        const glong cursor = g_utf8_strlen(text.c_str(), -1);
        ibus_engine_update_preedit_text(engine_, t, cursor, TRUE);
        // text 是 floating，update_preedit_text 会接管引用；不能再 unref。
    }

    void hidePreedit() {
        if (engine_ != nullptr) ibus_engine_hide_preedit_text(engine_);
    }

    void updateLookupTable(const chaoset::Comp& comp) {
        showLookup(comp.candidates, comp.page, comp.pageCount, lookupIndex_);
    }

    void showLookup(const std::vector<chaoset::Candidate>& cands, int page, int pageCount, int cursor) {
        if (engine_ == nullptr) return;
        if (cands.empty()) { hideLookupTable(); return; }
        if (cursor < 0) cursor = 0;
        if (cursor >= static_cast<int>(cands.size())) cursor = static_cast<int>(cands.size()) - 1;
        IBusLookupTable* table = ibus_lookup_table_new(kPageSize, cursor, TRUE, FALSE);
        for (std::size_t i = 0; i < cands.size(); ++i) {
            // IBusText 是 floating；append 取得/沉没引用后不能再 unref，否则
            // 表里留下悬垂指针，序列化时触发 IBUS_IS_SERIALIZABLE critical。
            IBusText* c = ibus_text_new_from_string(cands[i].text.c_str());
            ibus_lookup_table_append_candidate(table, c);
            char label[32];
            std::snprintf(label, sizeof(label), "%zu.", i + 1);
            IBusText* l = ibus_text_new_from_string(label);
            ibus_lookup_table_append_label(table, l);
        }
        ibus_lookup_table_set_cursor_pos(table, cursor);
        ibus_lookup_table_set_page_size(table, kPageSize);
        (void)page;
        (void)pageCount;
        ibus_engine_update_lookup_table(engine_, table, TRUE);
        // table 是 floating，_update_lookup_table 会接管引用。
    }

    void hideLookupTable() {
        if (engine_ != nullptr) ibus_engine_hide_lookup_table(engine_);
    }

    void commit(const std::string& text) {
        if (!text.empty() && engine_ != nullptr) {
            IBusText* t = ibus_text_new_from_string(text.c_str());
            if (t != nullptr) {
                ibus_engine_commit_text(engine_, t);
                // commit_text 接管 floating text 的引用。
            }
        }
        hidePreedit();
        hideLookupTable();
        confirmed_.clear();
        last_ = chaoset::Comp();
    }

    void flushConfirmedAndReset() {
        if (!isComposing() && confirmed_.empty() && assoc_.empty()) return;
        if (!confirmed_.empty()) commit(confirmed_);
        else { hidePreedit(); hideLookupTable(); }
        sessionResetComposition();
        exitAssociate();
    }

    bool associateEnabled() {
        if (assocFlagSet_ || configPath_.empty()) return assocOn_;
        UserConfig cfg;
        if (!readUserConfig(configPath_, &cfg)) {
            if (assocConfigErr_.empty()) {
                assocConfigErr_ = "配置读取失败";
                logLine("⚠ 读取联想设置失败，沿用上次值");
            }
            return assocOn_;
        }
        assocConfigErr_.clear();
        assocOn_ = cfg.associate;
        return assocOn_;
    }

    void enterAssociate(const std::vector<std::string>& sylls) {
        if (!associateEnabled() || sylls.empty()) return;
        chaoset::Comp comp;
        if (!ensure() || !daemon_.session().associate(sylls, &comp)) return;
        if (comp.candidates.empty()) return;
        assoc_ = std::move(comp.candidates);
        assocPage_ = 0;
        lookupIndex_ = 0;
        showAssociateTable();
    }

    void enterAssociateForPrev(const std::string& syllsOrWord) {
        if (syllsOrWord.empty() || !associateEnabled()) return;
        enterAssociate(splitSyllables(syllsOrWord));
    }

    void showAssociateTable() {
        const int total = static_cast<int>(assoc_.size());
        if (total == 0) { hideLookupTable(); return; }
        int pageCount = (total + kPageSize - 1) / kPageSize;
        if (assocPage_ * static_cast<int>(kPageSize) >= total) assocPage_ = pageCount - 1;
        if (assocPage_ < 0) assocPage_ = 0;
        const int start = assocPage_ * static_cast<int>(kPageSize);
        const int end = std::min(total, start + static_cast<int>(kPageSize));
        std::vector<chaoset::Candidate> page(assoc_.begin() + start, assoc_.begin() + end);
        showLookup(page, assocPage_, pageCount, lookupIndex_);
    }

    bool flipAssociate(bool next) {
        const int per = static_cast<int>(kPageSize);
        const int pageCount = (static_cast<int>(assoc_.size()) + per - 1) / per;
        if (next && assocPage_ + 1 < pageCount) ++assocPage_;
        else if (!next && assocPage_ > 0) --assocPage_;
        else return false;
        showAssociateTable();
        return true;
    }

    // 方向键（用户反馈）：上/下翻页、左/右页内预选移动；页边界保持不动（仍消费）。
    void moveLookup(int delta) {
        if (!assoc_.empty()) {
            const int total = static_cast<int>(assoc_.size());
            const int count = std::min(static_cast<int>(kPageSize), total - assocPage_ * static_cast<int>(kPageSize));
            if (lookupIndex_ + delta >= 0 && lookupIndex_ + delta < count) { lookupIndex_ += delta; showAssociateTable(); }
            return;
        }
        if (last_.candidates.empty()) return;
        if (lookupIndex_ + delta >= 0 && lookupIndex_ + delta < static_cast<int>(last_.candidates.size())) {
            lookupIndex_ += delta;
            updateLookupTable(last_);
        }
    }

    // 组词态翻页：上=上一页、下=下一页；边界页保持不动（仍消费按键）。
    void flipPage(bool next) {
        if (last_.candidates.empty()) return;
        if (next ? (last_.page + 1 >= last_.pageCount) : (last_.page <= 0)) return;
        chaoset::Comp comp = callComp([&] { return next ? daemon_.session().nextPage() : daemon_.session().prevPage(); });
        refresh(std::move(comp));
    }

    void exitAssociate() {
        if (assoc_.empty() && lookupIndex_ == 0) return;
        assoc_.clear();
        assocPage_ = 0;
        lookupIndex_ = 0;
        hideLookupTable();
    }

    bool selectCandidate(int idx) {
        if (!ensure()) return false;
        if (!assoc_.empty()) {
            const int global = assocPage_ * static_cast<int>(kPageSize) + idx;
            if (idx < 0 || global >= static_cast<int>(assoc_.size())) return false;
            const auto pick = assoc_[global];
            const auto sylls = splitSyllables(pick.syllables);
            std::string word;
            if (!daemon_.session().associateSelect(pick.text, sylls, &word)) return false;
            commit(word);
            exitAssociate();
            enterAssociate(sylls);
            return true;
        }
        if (idx < 0 || idx >= static_cast<int>(last_.candidates.size())) return false;
        const auto pick = last_.candidates[idx];
        chaoset::Comp rest;
        if (!daemon_.session().selectPrefix(idx, &rest)) return false;
        confirmed_ += rest.committed;
        const auto pickedSylls = splitSyllables(pick.syllables);
        selectedSylls_.insert(selectedSylls_.end(), pickedSylls.begin(), pickedSylls.end());
        if (!rest.input.empty()) {
            refresh(std::move(rest));
            return true;
        }
        const std::string text = confirmed_;
        confirmed_.clear();
        commit(text);
        std::string prev;
        for (std::size_t i = 0; i < selectedSylls_.size(); ++i) {
            if (i != 0) prev += "'";
            prev += selectedSylls_[i];
        }
        selectedSylls_.clear();
        if (!prev.empty()) enterAssociateForPrev(prev);
        return true;
    }

    void showToast(const std::string& text) {
        if (engine_ == nullptr) return;
        if (toastId_ != 0) { g_source_remove(toastId_); toastId_ = 0; }
        IBusText* t = ibus_text_new_from_string(text.c_str());
        if (t != nullptr) {
            ibus_engine_update_auxiliary_text(engine_, t, TRUE);
            // auxiliary text 同样接管 floating 引用。
        }
        toastSeq_++;
        struct ToastCtx { IbusEngineState* self; guint64 seq; };
        auto* ctx = new ToastCtx{this, toastSeq_};
        toastId_ = g_timeout_add_full(G_PRIORITY_DEFAULT, 1200,
            [](gpointer data) -> gboolean {
                auto* c = static_cast<ToastCtx*>(data);
                if (c->self->toastSeq_ == c->seq && c->self->engine_ != nullptr) {
                    ibus_engine_hide_auxiliary_text(c->self->engine_);
                }
                c->self->toastId_ = 0;
                return G_SOURCE_REMOVE;
            }, ctx, [](gpointer data) { delete static_cast<ToastCtx*>(data); });
    }

    IBusEngine* engine_ = nullptr;
    chaoset::Daemon daemon_;
    std::string configPath_;
    bool enMode_ = false;
    bool halfWidth_ = false;
    bool assocOn_ = true;
    bool assocFlagSet_ = false;
    std::string assocConfigErr_;
    ShiftTracker shift_;
    std::string dpScheme_;
    std::vector<chaoset::Candidate> assoc_;
    int assocPage_ = 0;
    int lookupIndex_ = 0;  // 页内预选序号（lookup cursor，0 起）
    std::string confirmed_;
    std::vector<std::string> selectedSylls_;
    chaoset::Comp last_;
    bool warnedIpc_ = false;
    guint toastId_ = 0;
    guint64 toastSeq_ = 0;
};

}  // namespace

// ---- GObject/IBus 薄胶水 ----
typedef struct _ChaosetIbusEngine {
    IBusEngine parent;
    IbusEngineState* state;
} ChaosetIbusEngine;

typedef struct _ChaosetIbusEngineClass {
    IBusEngineClass parent;
} ChaosetIbusEngineClass;

G_DEFINE_TYPE(ChaosetIbusEngine, chaoset_ibus_engine, IBUS_TYPE_ENGINE)

static gboolean on_process_key_event(IBusEngine* engine, guint keyval, guint keycode, guint state) {
    auto* self = reinterpret_cast<ChaosetIbusEngine*>(engine);
    return self->state != nullptr && self->state->processKeyEvent(keyval, keycode, state);
}
static void on_focus_in(IBusEngine* engine) {
    auto* self = reinterpret_cast<ChaosetIbusEngine*>(engine);
    if (self->state != nullptr) self->state->focusIn();
}
static void on_focus_out(IBusEngine* engine) {
    auto* self = reinterpret_cast<ChaosetIbusEngine*>(engine);
    if (self->state != nullptr) self->state->focusOut();
}
static void on_reset(IBusEngine* engine) {
    auto* self = reinterpret_cast<ChaosetIbusEngine*>(engine);
    if (self->state != nullptr) self->state->reset();
}
static void on_page_up(IBusEngine* engine) {
    auto* self = reinterpret_cast<ChaosetIbusEngine*>(engine);
    if (self->state != nullptr) self->state->pageUp();
}
static void on_page_down(IBusEngine* engine) {
    auto* self = reinterpret_cast<ChaosetIbusEngine*>(engine);
    if (self->state != nullptr) self->state->pageDown();
}
static void on_candidate_clicked(IBusEngine* engine, guint index, guint button, guint state) {
    (void)button;
    (void)state;
    auto* self = reinterpret_cast<ChaosetIbusEngine*>(engine);
    if (self->state != nullptr) self->state->candidateClicked(index);
}
static void chaoset_ibus_engine_init(ChaosetIbusEngine* self) {
    self->state = new IbusEngineState(gOptions, reinterpret_cast<IBusEngine*>(self));
}
static void chaoset_ibus_engine_finalize(GObject* object) {
    auto* self = reinterpret_cast<ChaosetIbusEngine*>(object);
    delete self->state;
    self->state = nullptr;
    G_OBJECT_CLASS(chaoset_ibus_engine_parent_class)->finalize(object);
}
static void chaoset_ibus_engine_class_init(ChaosetIbusEngineClass* klass) {
    GObjectClass* object_class = G_OBJECT_CLASS(klass);
    object_class->finalize = chaoset_ibus_engine_finalize;
    IBusEngineClass* engine_class = IBUS_ENGINE_CLASS(klass);
    engine_class->process_key_event = on_process_key_event;
    engine_class->focus_in = on_focus_in;
    engine_class->focus_out = on_focus_out;
    engine_class->reset = on_reset;
    engine_class->page_up = on_page_up;
    engine_class->page_down = on_page_down;
    engine_class->candidate_clicked = on_candidate_clicked;
}

static void parse_options(int argc, char** argv) {
    chaoset::LinuxPaths paths = chaoset::defaultLinuxPaths();
    gOptions.config = paths.config;
    gOptions.importedDict = paths.importedDict;
    gOptions.userdict = paths.userdict;
    gOptions.log = paths.log;
    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        auto next = [&](std::string* out) -> bool {
            if (i + 1 >= argc) return false;
            *out = argv[++i];
            return true;
        };
        if (arg == "-ibus" || arg == "--ibus") continue;
        else if (arg == "-dict") { std::string v; if (next(&v)) gOptions.dicts.push_back(v); }
        else if (arg == "-imported-dict") next(&gOptions.importedDict);
        else if (arg == "-userdict") next(&gOptions.userdict);
        else if (arg == "-socket") next(&gOptions.socket);
        else if (arg == "-config") next(&gOptions.config);
        else if (arg == "-daemon") next(&gOptions.daemonPath);
        else if (arg == "-log") next(&gOptions.log);
        else if (arg == "-shuangpin") next(&gOptions.shuangpin);
        else if (arg == "-fuzzy") gOptions.fuzzy = true;
        else if (arg == "-traditional") gOptions.traditional = true;
        else if (arg.rfind("-associate=", 0) == 0) {
            gOptions.associate = arg.substr(11) != "false" && arg.substr(11) != "0";
            gOptions.associateFlagSet = true;
        } else if (arg == "-associate") { gOptions.associate = true; gOptions.associateFlagSet = true; }
    }
    if (gOptions.dicts.empty()) {
        const std::string system = "/usr/share/chaoset/dict.tsv";
        if (g_file_test(system.c_str(), G_FILE_TEST_IS_REGULAR)) {
            gOptions.dicts.push_back(system);
        } else {
            // 解包即用（未跑 install.sh）时引擎在 bin/chaoset-engine，
            // 词库在 ../data/dict.tsv；源码树里则是 data/dict_common.tsv。
            const std::string dir = executableDir();
            const std::string green = dir + "/../data/dict.tsv";
            const std::string dev = dir + "/data/dict_common.tsv";
            if (g_file_test(green.c_str(), G_FILE_TEST_IS_REGULAR)) gOptions.dicts.push_back(green);
            else if (g_file_test(dev.c_str(), G_FILE_TEST_IS_REGULAR)) gOptions.dicts.push_back(dev);
            else gOptions.dicts.push_back(system);
        }
    }
    UserConfig cfg;
    if (readUserConfig(gOptions.config, &cfg)) {
        if (!gOptions.associateFlagSet) gOptions.associate = cfg.associate;
        if (!gOptions.fuzzy) gOptions.fuzzy = cfg.fuzzy;
        if (!gOptions.traditional) gOptions.traditional = cfg.traditional;
        if (gOptions.shuangpin.empty()) gOptions.shuangpin = cfg.shuangpin;
    }
}

int main(int argc, char** argv) {
    parse_options(argc, argv);
    logLine("配置生效：模糊音=" + std::string(gOptions.fuzzy ? "true" : "false") +
            " 繁体=" + std::string(gOptions.traditional ? "true" : "false") +
            " 双拼=\"" + gOptions.shuangpin + "\" 联想=" +
            std::string(gOptions.associate ? "true" : "false"));
    for (const auto& dict : gOptions.dicts) {
        if (!g_file_test(dict.c_str(), G_FILE_TEST_IS_REGULAR)) {
            // install.sh 靠这句判断安装完整性：主词库缺失时 daemon 会启动失败，
            // 必须在引擎侧先给出可被安装器识别的原因。
            logLine("打开词库 " + dict + " 失败: 文件不存在或不可读");
        }
        logLine("加载词库: " + dict);
    }
    if (!gOptions.importedDict.empty() && g_file_test(gOptions.importedDict.c_str(), G_FILE_TEST_IS_REGULAR)) {
        logLine("用户导入词库: " + gOptions.importedDict);
    }
    ibus_init();
    // ibus-daemon 使用自己的私有 D-Bus 总线（IBUS_ADDRESS），不是 session bus。
    // 必须用 ibus_bus_new() 连它；直接连 session bus 会导致 ibus-daemon 永远
    // 调用不到 Factory/Engine（SetGlobalEngine 超时）。
    IBusBus* bus = ibus_bus_new();
    if (bus == nullptr || !ibus_bus_is_connected(bus)) {
        logLine("无法连接 ibus-daemon（请确认 ibus 正在运行）");
        if (bus != nullptr) g_object_unref(bus);
        return 1;
    }
    GDBusConnection* conn = ibus_bus_get_connection(bus);
    IBusFactory* factory = ibus_factory_new(conn);
    ibus_factory_add_engine(factory, "chaoset", chaoset_ibus_engine_get_type());
    const guint32 named = ibus_bus_request_name(bus, "org.freedesktop.IBus.Chaoset", 0);
    if (named != IBUS_BUS_REQUEST_NAME_REPLY_PRIMARY_OWNER) {
        logLine("请求 ibus 总线名失败（另一个 Chaoset 引擎已在运行？）");
        g_object_unref(factory);
        g_object_unref(bus);
        return 1;
    }
    ibus_main();
    g_object_unref(factory);
    g_object_unref(bus);
    return 0;
}
