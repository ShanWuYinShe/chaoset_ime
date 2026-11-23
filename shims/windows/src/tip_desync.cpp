// ChaosetTIP daemon 失步恢复：组词中引擎请求超时后的本地保留、失步期按键分流
// 与重连重建（Windows 壳拆分 TU）。
//
// 背景（issue #122）：壳侧按键请求有 30ms 截止，而桌面系统上几十毫秒的调度
// 停顿无法杜绝（引擎再快也一样）。旧实现在任一请求超时后立刻拆掉组词——预
// 编辑拼音被擦掉、候选窗关闭、当前键放行成英文——再叠加 5s 熔断，用户看到
// 的就是「候选窗突然关闭，之后只能打英文」。
//
// 现行语义：
//   * 只有「超时」才进入失步（断管/连不上说明 daemon 确实不在，沿用 fail-open；
//     尚无组词的首键也沿用 fail-open：键原样进文档，下一键恢复）。同步态里
//     每一处引擎请求失败都经 enterDesyncOnTimeout 分流，不分请求种类。
//   * 失步期间本地组词是唯一事实源：追加键记入 pendingKeys_ 并乐观显示在预
//     编辑里；退格、Esc、回车不依赖引擎，本地直接处理。
//   * 其余需要引擎状态的按键（选词/翻页/标点…）先重建：重连后新会话是空的，
//     用 input 的 base 参数整串重建。重建后**只刷新候选**——用户看到的旧候选
//     列表对应的是重建前的输入，不能照它选词；标点例外（「首选上屏 + 标点」
//     的意图与候选位置无关），重建后按正常流程处理。
//   * daemon 在宽限期内一直连不上才放弃：本地内容按原始串落盘（一个键都不
//     丢），当前键交还宿主。
//
// 模块边界：只依赖 tip.hpp；全部在 TSF 线程上执行，只做内存操作 + 有截止的
// 引擎请求（与 handleKey 同口径，零阻塞契约由 checkuiparity 断言）。

#include "tip.hpp"

using namespace chaoset::win;

namespace {

// daemon 连不上的宽限期：覆盖 daemon 重启（重新加载词库）与监听实例偶发
// 占满的时间；超过后才放弃本地组词。
constexpr DWORD kDesyncGraceMs = 2000;

}  // namespace

namespace chaoset::win {

// 本地回退一格（不依赖引擎）。pending 非空时退最近一键；否则退 comp.input 的
// 末尾：全拼逐字符（bySyllable 时按预编辑末段整段），双拼恒按末段（已解码
// 音节）。comp.preedit 同步裁掉对应部分，仅用于乐观显示。
void backspaceLocally(chaoset::Comp& comp, std::string& pending, bool shuangpin,
                      bool bySyllable) {
    if (!pending.empty()) {
        pending.pop_back();
        return;
    }
    if (comp.input.empty()) return;
    if (!shuangpin && !bySyllable) {
        const bool removedSeparator = comp.input.back() == '\'';
        comp.input.pop_back();
        if (comp.preedit.empty()) return;
        comp.preedit.pop_back();
        if (comp.input.empty()) {
            comp.preedit.clear();
            return;
        }
        // 末段被删空：连同引擎补的分隔符一起去掉（用户自己敲的分隔符保留）。
        if (!removedSeparator && comp.preedit.size() >= 2 && comp.preedit.back() == '\'' &&
            comp.input.back() != '\'') {
            comp.preedit.pop_back();
        }
        return;
    }
    const std::string::size_type sep = comp.preedit.rfind('\'');
    const std::string seg =
        sep == std::string::npos ? comp.preedit : comp.preedit.substr(sep + 1);
    if (seg.empty()) return;
    if (comp.input.size() >= seg.size()) comp.input.resize(comp.input.size() - seg.size());
    comp.preedit = sep == std::string::npos ? std::string() : comp.preedit.substr(0, sep);
}

}  // namespace chaoset::win

// 引擎请求失败后的分流（见 tip.hpp）。同步态里每一处引擎请求失败都先问它：
// 超时一律保留组词，否则旧实现会把一次调度停顿放大成「拼音被擦 + 5 秒只能
// 打英文」，而选词请求超时后管道已断，下一键重连拿到的是空会话、已输入的
// 拼音会被静默重置成单个字母。
bool ChaosetTIP::enterDesyncOnTimeout(const chaoset::Comp& prev) {
    // 只保留「超时」：管道断开/连不上说明 daemon 确实不在，沿用 fail-open；
    // 请求前没有组词（首键）也沿用 fail-open：键原样进文档，下一键恢复。
    if (prev.input.empty() || !transport_.lastIoWasTimeout()) return false;
    comp_ = prev;
    if (!daemonDesynced_) {
        daemonDesynced_ = true;
        desyncSinceTick_ = GetTickCount();
        diagDaemonRequestFailure(transport_, "timeout");  // 超时仍是要关注的信号
    }
    return true;
}

// 追加键的失步入口（字母 / 双拼 ';' / 撇号共用，见 tip.hpp）。
bool ChaosetTIP::keepCompositionOnTimeout(const chaoset::Comp& prev, const std::string& key,
                                           ITfContext* pic) {
    if (!enterDesyncOnTimeout(prev)) return false;
    pendingKeys_ += key;
    render(pic);  // 乐观预编辑：applyPreedit 把 pendingKeys_ 接在预编辑后面
    // 乐观显示也没写进文档（上下文已不可写）：键必须交还宿主，不能白吃。
    if (composition_ == nullptr || !preeditWritten_) {
        bailOutNoComposition();
        return false;
    }
    return true;
}

// 失步后的状态重建：非阻塞重连，再把本地输入串整串送到新会话上。
ChaosetTIP::SyncResult ChaosetTIP::syncDaemonInput() {
    if (!daemonDesynced_) return SyncResult::kSynced;
    // 超时后管道已断；新连接上的 daemon 会话是空的，base + 追加键一次重建。
    if (!ensureDaemonFast(transport_)) return SyncResult::kUnreachable;
    const std::string base = comp_.input;
    chaoset::Comp got = session_.input(pendingKeys_, halfWidth_, base.c_str());
    if (!got.ok) return SyncResult::kStalled;  // 连上了却没在截止内应答：仍在失步
    daemonDesynced_ = false;
    pendingKeys_.clear();
    comp_ = got;
    syncDpScheme(comp_);
    return SyncResult::kSynced;
}

// 失步中的按键分流。返回 false = 已重建成功且本键语义明确，落回 handleKey 的
// 正常流程；返回 true = 本键已处理完，结果见 *eaten。
bool ChaosetTIP::handleKeyDesynced(WPARAM vk, ITfContext* pic, bool* eaten) {
    *eaten = true;
    const bool shift = shiftActive();
    const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
    const bool alt = (GetKeyState(VK_MENU) & 0x8000) != 0;
    const bool win =
        ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);
    const bool backspace = vk == kVkBackspace || vk == kVkDelete;
    const auto withinGrace = [this]() {
        return static_cast<LONG>(GetTickCount() - desyncSinceTick_) <
               static_cast<LONG>(kDesyncGraceMs);
    };
    // daemon 在宽限期内一直连不上：本地内容按原始串落盘（一个键都不丢），
    // 本键交还宿主。
    const auto giveUp = [&]() {
        const std::string raw = confirmedPreedit_ + comp_.input + pendingKeys_;
        confirmedPreedit_.clear();
        comp_ = chaoset::Comp();
        clearDesync();
        resetComposition();
        commit(pic, raw);
        *eaten = false;
    };
    // 仍在失步（daemon 又超时或暂时连不上）：乐观渲染并保留组词。
    const auto stayDesynced = [&]() {
        render(pic);
        if (composition_ == nullptr || !preeditWritten_) {
            bailOutNoComposition();
            *eaten = false;
        }
    };

    // A. 不依赖引擎的收尾键：本地直接处理，语义与同步态一致。
    if (vk == kVkEscape || (ctrl && vk == 'U' && !shift) || (backspace && (ctrl || shift || win))) {
        // 全部删除：Esc / Ctrl+U / 带修饰键的退格（含未落盘的已确认段）
        resetComposition();
        comp_ = chaoset::Comp();
        confirmedPreedit_.clear();
        endComposition();
        return true;
    }
    if (vk == kVkReturn) {
        // 回车：已确认段按确认文本、拼音（含待确认键）按原始串一起上屏
        const std::string raw = confirmedPreedit_ + comp_.input + pendingKeys_;
        confirmedPreedit_.clear();
        resetComposition();
        commit(pic, raw);
        return true;
    }
    if (backspace) {
        backspaceLocally(comp_, pendingKeys_, !dpScheme_.empty(), alt);
        if (!comp_.input.empty() || !pendingKeys_.empty()) {
            render(pic);
        } else if (!confirmedPreedit_.empty()) {
            // 剩余拼音删空：已确认段立即落盘收尾（与同步态的退格一致）
            comp_ = chaoset::Comp();
            const std::string all = confirmedPreedit_;
            confirmedPreedit_.clear();
            commit(pic, all);
        } else {
            endComposition();
        }
        return true;
    }
    if (vk == kVkSpace && shift) {
        // 全/半角标点开关归壳本地持有，不需要引擎
        halfWidth_ = !halfWidth_;
        showToast(halfWidth_ ? L"\u534a\u89d2\u6807\u70b9" : L"\u5168\u89d2\u6807\u70b9");
        return true;
    }

    // B. 追加输入的键（字母 / 双拼 ';' / 组词中的撇号）：先本地记账，再尝试重建。
    std::string key;
    if (!ctrl && !alt && !win) {
        if (vk >= kVkFirstLetter && vk <= kVkLastLetter) {
            // 大小写 = Shift ⊕ CapsLock，与 handleKey 的字母分支同口径
            const bool capsOn = (GetKeyState(VK_CAPITAL) & 0x0001) != 0;
            const char base = (shift != capsOn) ? 'A' : 'a';
            key.assign(1, static_cast<char>(base + (vk - kVkFirstLetter)));
        } else if (!dpScheme_.empty() && isDpSemicolonKey(vk, shift)) {
            key = ";";
        } else if (vk == kVkOemQuotes && !shift) {
            key = "'";
        }
    }
    if (!key.empty()) {
        pendingKeys_ += key;
        const SyncResult r = syncDaemonInput();
        if (r == SyncResult::kSynced) {
            if (!comp_.committed.empty()) {
                // 双拼 ';' 落在标点语义上：引擎已把「首选 + 标点」放进 committed
                const std::string all = confirmedPreedit_ + comp_.committed;
                confirmedPreedit_.clear();
                commit(pic, all);
            } else {
                render(pic);
            }
        } else if (r == SyncResult::kStalled || withinGrace()) {
            stayDesynced();
        } else {
            giveUp();
        }
        return true;
    }

    // C. 其余需要引擎状态的按键（数字/空格/方向/翻页/标点）：先重建。
    const SyncResult r = syncDaemonInput();
    if (r == SyncResult::kSynced) {
        // 标点的意图与候选位置无关：重建后按正常流程处理本键
        if (keyPunctChar(vk, shift) != nullptr) return false;
        // 选词/翻页只用于「补课」：旧候选对应的是重建前的输入，本键不动作
        candidateWnd_.setSelectedIndex(0);
        render(pic);
    } else if (r == SyncResult::kStalled || withinGrace()) {
        // 保留组词，本键不动作
    } else {
        giveUp();
    }
    return true;
}
