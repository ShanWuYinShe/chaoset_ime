// ChaosetTIP 按键路由：ITfKeyEventSink 五入口 + Shift 状态机 + 键位映射
// （Windows 壳拆分 TU，wave2）。
//
// 从 win32.cpp 的类内 inline 定义逐字拆出：OnTestKeyDown/OnKeyDown/OnTestKeyUp/
// OnKeyUp/OnPreservedKey、Shift 空按记账（位掩码 + 物理状态自愈）、修饰键/键位
// 映射（keyPunctChar/digitIndex/shiftActive/isModifierKey）、wouldHandle/handleKey。
//
// 模块边界：只依赖 tip.hpp；按键语义断言（checkuiparity「非激活零副作用」/
// 「Shift 空按记账」）仍按 winShell 聚合文本断言，因此本 TU 的文案与调用顺序
// 必须保持与拆分前逐字一致。

#include "tip.hpp"

using namespace chaoset::win;


HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
BOOL* pfEaten) {
    g_lastOp = kOpTestKeyDown;
    if (bailOutIfInactive()) {
        *pfEaten = FALSE;
        return S_OK;
    }
    applyPendingStaleReset();  // 前台已离开过：先丢掉上一轮的残留组词
    // Shift 记账自愈必须覆盖所有按键（含修饰键）：若上一次按住的 KeyUp
    // 丢了，这里先复位，随后的 Shift 按下才能重新开一次干净的会话。
    reconcileShiftState();
    if (isModifierKey(wParam)) {
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            onShiftEvent(false, wParam, pic);
        } else if (shiftHeld_ != 0) {
            sinceShift_ = true;
        }
        *pfEaten = FALSE;  // 修饰键始终被动放行，绝不进入 handleKey 干扰系统状态
        return S_OK;
    }
    // 任何非 Shift 键按下都取消「Shift 空按」窗口。必须放在 Test 阶段：
    // 原实现放在 OnKeyDown，Shift+方向键/功能键等未组词的按键不会进
    // OnKeyDown → sinceShift_ 仍为 false → 松 Shift 时误切换中英。
    sinceShift_ = true;
    if (currentEnglish()) {
        *pfEaten = FALSE;  // 英文模式全放行
        return S_OK;
    }
    if (!wouldHandle(wParam)) {
        *pfEaten = FALSE;
        return S_OK;
    }
    // 需要新建合成（字母）或插入文本（标点）的键，在「不支持该操作的
    // 上下文」里必须放行：2026-10-29 实机反馈，Windows 11 开始菜单里
    // 按字母没有任何反应，搜索框也不出现——键在 OnTestKeyDown 被吃掉，
    // 而 applyPreedit 在这个上下文里建不出合成，字符既不进文档也不进
    // 宿主自己的搜索框。这里先做一次无副作用的接口探测（只读文档、
    // 缺 ITfInsertAtSelection / ITfContextComposition 的上下文直接放行）；
    // handleKey 里还有一道「渲染后仍无合成则放行」的兜底（OnKeyDown 的
    // pfEaten 才是最终裁决，见 ITfKeyEventSink 两阶段设计）。
    if (composition_ == nullptr) {
        const bool shift = shiftActive();
        const bool letter = !shift && wParam >= kVkFirstLetter && wParam <= kVkLastLetter;
        const bool punct = keyPunctChar(wParam, shift) != nullptr;
        if (letter || punct) {
            ITfContext* ctx = pic;
            ContextRef owned(nullptr);
            if (ctx == nullptr) {
                owned.ctx = focusContext();
                ctx = owned.ctx;
            }
            bool ok;
            if (letter && ctx == probeCtx_) {
                ok = probeCan_;  // 缓存命中：零 COM 调用
            } else {
                ok = letter ? canStartComposition(ctx) : canInsert(ctx);
                if (letter && ctx != nullptr) {
                    if (probeCtx_ != nullptr) probeCtx_->Release();
                    probeCtx_ = ctx;
                    probeCtx_->AddRef();
                    probeCan_ = ok;
                }
            }
            if (!ok) {
                *pfEaten = FALSE;
                return S_OK;
            }
        }
    }
    *pfEaten = TRUE;
    return S_OK;
}


HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyDown(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
BOOL* pfEaten) {
    g_lastOp = kOpKeyDown;
    if (bailOutIfInactive()) {
        *pfEaten = FALSE;
        return S_OK;
    }
    applyPendingStaleReset();
    // Shift 记账自愈也必须覆盖 Key 阶段：Chrome/conhost/Windows Terminal
    // 等宿主的内容键只进 Key 阶段（Test 阶段收不到修饰键），只挂 Test
    // 会让这些宿主丢 KeyUp 后没有收口路径（2026-11-05 回归审计 P2-11）。
    reconcileShiftState();
    if (isModifierKey(wParam)) {
        if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
            onShiftEvent(false, wParam, pic);
        } else if (shiftHeld_ != 0) {
            sinceShift_ = true;
        }
        *pfEaten = FALSE;  // 修饰键始终放行，绝不进入 handleKey 干扰系统状态
        return S_OK;
    }
    sinceShift_ = true;  // 幂等兜底（正常已在 OnTestKeyDown 置位）
    if (currentEnglish()) {
        clearAssociate();
        if (isComposing() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        *pfEaten = FALSE;  // 英文模式全放行
        return S_OK;
    }
    if (!wouldHandle(wParam)) {
        clearAssociate();
        if (isComposing() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        *pfEaten = FALSE;  // 非组词按键一律放行给宿主
        return S_OK;
    }
    *pfEaten = handleKey(wParam, pic) ? TRUE : FALSE;
    return S_OK;
}


HRESULT STDMETHODCALLTYPE ChaosetTIP::OnTestKeyUp(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
BOOL* pfEaten) {
    if (bailOutIfInactive()) {
        *pfEaten = FALSE;
        return S_OK;
    }
    // 只有 Shift 键释放才参与中英切换判定；
    // 其它任何按键（包括 Ctrl、Alt、Win 等修饰键）的释放，输入法绝不关心、不干预、直接放行
    if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
        onShiftEvent(true, wParam, pic);
    }
    *pfEaten = FALSE;  // KeyUp 一律不消费，保证宿主应用状态机完整，绝不导致宿主丢键或键盘卡死
    return S_OK;
}


HRESULT STDMETHODCALLTYPE ChaosetTIP::OnKeyUp(ITfContext* pic, WPARAM wParam, LPARAM /*lParam*/,
BOOL* pfEaten) {
    if (bailOutIfInactive()) {
        *pfEaten = FALSE;
        return S_OK;
    }
    // 只有 Shift 键释放才参与中英切换判定（Key 阶段对称兜底）；
    // 其它任何按键（包括 Ctrl、Alt、Win 等修饰键）的释放，输入法绝不关心、不干预、直接放行
    if (wParam == kVkShift || wParam == VK_LSHIFT || wParam == VK_RSHIFT) {
        onShiftEvent(true, wParam, pic);
    }
    *pfEaten = FALSE;  // 只透传不消费
    return S_OK;
}


HRESULT STDMETHODCALLTYPE ChaosetTIP::OnPreservedKey(ITfContext* /*pic*/, REFGUID /*rguid*/, BOOL* pfEaten) {
    *pfEaten = FALSE;
    return S_OK;
}

unsigned ChaosetTIP::shiftBit(WPARAM vk) {
    if (vk == VK_LSHIFT) return 1u;
    if (vk == VK_RSHIFT) return 2u;
    return 4u;  // 泛化 VK_SHIFT：多数宿主只投递这个
}


// 系统层面是否仍有任一 Shift 物理按下。只在持有按住会话时才查询
// （唯一调用点 reconcileShiftState 已先判 shiftHeld_），正常打字路径零额外开销。
// 用来兜住上下文切换/焦点变化造成的 KeyUp 丢失。
bool ChaosetTIP::shiftPhysicallyDown() {
    return (GetAsyncKeyState(VK_LSHIFT) & 0x8000) != 0 ||
           (GetAsyncKeyState(VK_RSHIFT) & 0x8000) != 0;
}


// 焦点/停用复位。Linux 壳在 focusIn/focusOut 调 shift_.reset()
// （shims/linux/engine/main.cpp:385-389），Windows 此前完全没有这一步：
// 按住 Shift 切走窗口时 KeyUp 不会回来，复位是唯一能保证不卡死的时机。
void ChaosetTIP::resetShiftTracking() {
    shiftHeld_ = 0;
    sinceShift_ = true;  // 复位后首个释放不得触发切换
    shiftHoldSuspended_ = false;
}


// 焦点暂时离开（仍在同一进程/同一窗口族）：不丢按住状态，只挂起。
void ChaosetTIP::suspendShiftHoldOnFocusLost() {
    if (shiftHeld_ != 0 && shiftPhysicallyDown()) {
        shiftHoldSuspended_ = true;
    } else {
        resetShiftTracking();
    }
}


// 开始一次「按住会话」：位运算幂等（auto-repeat / 重复投递都不累积）。
void ChaosetTIP::beginShiftHold(unsigned bit) {
    if ((shiftHeld_ & bit) != 0) return;
    if (shiftHeld_ == 0) {
        sinceShift_ = false;
        shiftHoldSuspended_ = false;
    }
    shiftHeld_ |= bit;
}


// 空按判定 + 切换。pic 为事件路径带来的上下文；自愈收口时传 nullptr，
// 内部再取 focusContext()（必须用 ContextRef 释放）。
void ChaosetTIP::finishShiftHold(ITfContext* pic) {
    const bool suspended = shiftHoldSuspended_;
    shiftHoldSuspended_ = false;
    // 纯事件驱动：按键只会在本 TIP 有焦点时到达，不需要额外的前台门控。
    if (!sinceShift_ && !suspended) {
        if (pic != nullptr) {
            toggleEnMode(pic);
        } else {
            ContextRef owned(nullptr);
            owned.ctx = focusContext();
            toggleEnMode(owned.ctx);
        }
    }
    sinceShift_ = true;
}


// 结束一次「按住会话」：只有最后一次抬起才结算。
void ChaosetTIP::endShiftHold(unsigned bit, ITfContext* pic) {
    if ((shiftHeld_ & bit) == 0) return;
    shiftHeld_ &= ~bit;
    if (shiftHeld_ != 0) return;  // 另一侧还按着，等它抬起
    finishShiftHold(pic);
}


// 无条件收口：事件路径留下的按住位（左右/泛化位混用）在物理松开后
// 由下一次按键统一结算，避免任何一位永远挂着。
void ChaosetTIP::settleAllShiftHolds() {
    if (shiftHeld_ == 0) return;
    shiftHeld_ = 0;
    finishShiftHold(nullptr);
}


// vk 必须是原始 WPARAM（VK_SHIFT / VK_LSHIFT / VK_RSHIFT），
// 据此决定置/清哪一位——左右 Shift 同时按住时各自记一位。
void ChaosetTIP::onShiftEvent(bool release, WPARAM vk, ITfContext* pic) {
    const unsigned bit = shiftBit(vk);
    if (!release) {
        beginShiftHold(bit);
        return;
    }
    if ((shiftHeld_ & bit) != 0) {
        endShiftHold(bit, pic);
        return;
    }
    // 无按下的孤立释放。宿主混用键码时（按下 VK_LSHIFT、释放 VK_SHIFT 或
    // 反之）位置对不上——若 Shift 确已不在物理按下状态就整体结算，
    // 否则会留下一位永远挂着。
    if (shiftHeld_ == 0 || shiftPhysicallyDown()) return;
    settleAllShiftHolds();
}


// 结算一次已经松开的 Shift 按住会话。
//
// 事件路径（OnTestKeyUp）与按键自愈共用这一个出口，保证两条来源的
// 判定规则完全一致：只有「期间没按过任何别的键/鼠标」才切换中英。
void ChaosetTIP::reconcileShiftState() {
    // 只做「物理已松开但状态还挂着」的收口；按住中一律不干预。
    // 旧实现会按焦点门控直接清状态——窗口内焦点抖动时正好把正在按的
    // Shift 吃掉，这是「能打字但 Shift 切不了」的直接原因之一
    //（2026-11-05 修）。
    //
    // 纯事件驱动：不靠定时器，只在**下一次按键**到达时顺手自愈。
    // 若这里真的收口了，说明宿主丢了 Shift 的释放事件——留一条一次性
    // 证据，排障时能直接看出「哪个宿主不投递 KeyUp」，零常驻开销。
    if (shiftHeld_ != 0 && !shiftPhysicallyDown()) {
        static volatile LONG loggedLostKeyUp = 0;
        if (!sinceShift_ && InterlockedCompareExchange(&loggedLostKeyUp, 1, 0) == 0 &&
            diagBudget()) {
            diagPrintf(L"tsf.log", "shift: 宿主丢弃了 Shift 释放事件，已按物理状态收口\n");
        }
        settleAllShiftHolds();
    }
}


// 按键 → 产生的 ASCII 字符（US 布局直映，含 Shift 变体），只返回
// engine.IsPunctKey 字符集内的成员，nullptr 表示「不是标点，放行」。
//
// 判定必须是**字符级**而非 VK 级：Shift+; 产生的字符是 ':'、Shift+/
// 是 '?'——此前纯 VK 直映不看 Shift，导致 Shift+; 出「；」而不是「：」、
// Shift+, 出「，」而不是「《」，且 ？！：《》"" 等十个中文标点完全不可达
// （2026-10-27 审查发现；macOS/ibus 壳按字符判定，此处对齐）。
// Shift 变体不在字符集的键（如 Shift+2 → @、Shift+[ → {）返回 nullptr，
// 原字符透传给宿主，与 mac 的 passThrough 行为一致。
const char* ChaosetTIP::keyPunctChar(WPARAM vk, bool shift) {
    if (shift) {
        switch (vk) {
            case kVkFirstDigit: return "!";          // Shift+1
            case '6': return "^";
            case '9': return "(";
            case '0': return ")";
            case kVkOemSemicolon: return ":";
            case kVkOemComma: return "<";
            case kVkOemPeriod: return ">";
            case kVkOem2: return "?";
            case kVkOem3: return "~";
            case kVkOemQuotes: return "\"";          // 开闭引号配对在 daemon
        }
        return nullptr;
    }
    switch (vk) {
        case kVkOemComma: return ",";
        case kVkOemPeriod: return ".";
        case kVkOemSemicolon: return ";";
        case kVkOemOpenBrackets: return "[";
        case kVkOemBackslash: return "\\";
        case kVkOemCloseBrackets: return "]";
        case kVkOemQuotes: return "'";
    }
    return nullptr;
}


// 双拼模式的 ';' 键：US 布局非 Shift 的 ;（VK_OEM_1）。双拼时它不是
// 标点键而是引擎的组词/解码键（引擎契约：input(";")，dpPending 非 0
// 时与字母一样成对解码，为 0 时按 punct(";") 语义上屏首选+分号）。
// Shift+; 产生的 ':' 不是它，仍走标点分支——与 mac 的字符级判定一致。
bool ChaosetTIP::isDpSemicolonKey(WPARAM vk, bool shift) {
    return !shift && vk == kVkOemSemicolon;
}


// 数字键 → 页内序号（0 起）：主键盘 1-9 与小键盘 1-9 都支持。
// 返回 -1 表示不是数字选词键。主键盘带 Shift 时产生 !@# 等字符，由调用方
// 另行判定（小键盘不受 Shift 影响）。
int ChaosetTIP::digitIndex(WPARAM vk) {
    if (vk >= kVkFirstDigit && vk <= kVkLastDigit) {
        return static_cast<int>(vk - kVkFirstDigit);
    }
    if (vk >= kVkNumpad1 && vk <= kVkNumpad9) {
        return static_cast<int>(vk - kVkNumpad1);
    }
    return -1;
}


// Shift/CapsLock 有效：字母按大写、数字/OEM 键按 Shift 变体处理
bool ChaosetTIP::shiftActive() {
    return (GetKeyState(VK_SHIFT) & 0x8000) != 0 || (GetKeyState(VK_CAPITAL) & 0x0001) != 0;
}


// 方向键：上/左=前一项，下/右=后一项（三端一致的预选移动语义）。
bool ChaosetTIP::isArrowKey(WPARAM vk) {
    return vk == kVkLeft || vk == kVkUp || vk == kVkRight || vk == kVkDown;
}


// 组词态页内预选移动：先让候选窗在页内移动；已在页首/页尾时按
// 「上翻选末项、下翻选首项」跨页；没有对应页时保持不动（仍消费该键）。
void ChaosetTIP::moveCompositionSelection(int delta, ITfContext* pic) {
    if (!hasCandidates()) return;
    if (candidateWnd_.moveSelection(delta)) return;
    if (delta < 0) {
        if (comp_.page <= 0) return;  // 首页页首：保持不动
        comp_ = session_.prevPage();
        if (!comp_.ok) {
            diagDaemonRequestFailure(transport_, "page(dir)");
            flushOrCancel(pic);  // daemon 故障：已确认段先落盘，不吞键
            return;
        }
        syncDpScheme(comp_);
        render(pic);
        candidateWnd_.setSelectedIndex(static_cast<int>(comp_.candidates.size()) - 1);
        return;
    }
    if (comp_.page + 1 >= comp_.pageCount) return;  // 末页页尾：保持不动
    comp_ = session_.nextPage();
    if (!comp_.ok) {
        diagDaemonRequestFailure(transport_, "page(dir)");
        flushOrCancel(pic);
        return;
    }
    syncDpScheme(comp_);
    render(pic);
    candidateWnd_.setSelectedIndex(0);
}


// 联想态页内预选移动（联想候选是壳本地分页：assocPage_ 起、每页 kPageSize）。
void ChaosetTIP::moveAssociateSelection(int delta) {
    if (assoc_.candidates.empty()) return;
    if (candidateWnd_.moveSelection(delta)) return;
    if (delta < 0) {
        if (assocPage_ <= 0) return;  // 首页页首：保持不动
        if (flipAssociate(false)) candidateWnd_.setSelectedIndex(kPageSize - 1);
        return;
    }
    const int total = static_cast<int>(assoc_.candidates.size());
    const int pageCount = (total + kPageSize - 1) / kPageSize;
    if (assocPage_ + 1 >= pageCount) return;  // 末页页尾：保持不动
    if (flipAssociate(true)) candidateWnd_.setSelectedIndex(0);
}


bool ChaosetTIP::isModifierKey(WPARAM vk) {
    return vk == VK_SHIFT || vk == VK_LSHIFT || vk == VK_RSHIFT ||
           vk == VK_CONTROL || vk == VK_LCONTROL || vk == VK_RCONTROL ||
           vk == VK_MENU || vk == VK_LMENU || vk == VK_RMENU ||
           vk == VK_LWIN || vk == VK_RWIN;
}


// 是否会吃掉该键：修饰键组合（Ctrl/Alt/Win 快捷键）一律放行给宿主系统与应用，
// 仅在组词中支持 Ctrl+Backspace 快速清空拼音；字母与标点键正常输入；
// 组词中再吃数字/空格/回车/退格/Esc/翻页。
bool ChaosetTIP::wouldHandle(WPARAM vk) const {
    if (isModifierKey(vk)) return false;

    const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
    const bool alt  = (GetKeyState(VK_MENU) & 0x8000) != 0;
    const bool win  = ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);

    // 组词中 Backspace/Delete：修饰键映射与 macOS 对齐（2026-10-31 用户要求：
    // Control↔Ctrl、Option↔Alt、Command↔Win）。两者都消费，具体行为在
    // handleKey 里按「Command/Ctrl/Shift → 全部删除；Alt → 按音节回退；
    // 无修饰 → 逐字符回退」处理。非组词态一律放行给宿主。
    if (isComposing() && (vk == kVkBackspace || vk == kVkDelete)) return true;
    // Ctrl+U 整行清空；Ctrl+Shift+U 是宿主快捷键（mac/Linux 都放行），
    // 所以这里必须要求不带 Shift。
    if (ctrl) return isComposing() && vk == 'U' && !shiftActive();
    if (alt || win) return false;

    // 联想模式：数字（有对应项）/空格（选预选项）/方向键（移动页内预选）/
    // 回车/Esc/Backspace（收起）由联想分支消费；其余按键退出联想后按正常
    // 流程判定（继续往下走）。
    if (!assoc_.candidates.empty()) {
        if (vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
            vk == kVkBackspace || isArrowKey(vk)) return true;
        // 联想翻页键：PageUp/PageDown 不判 Shift（与 mac/Linux 一致：
        // 联想态 Shift+PageUp/PageDown 同样翻联想页，2026-10-30 审计 A7）。
        if (vk == kVkPrior || vk == kVkNext) return true;
        if (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus)) return true;
        const int digit = digitIndex(vk);
        if (digit >= 0 && (vk >= kVkNumpad1 || !shiftActive())) {
            return assocPage_ * kPageSize + digit <
                   static_cast<int>(assoc_.candidates.size());
        }
    }

    // Shift+空格只在输入法介入时（组词/联想）才预告吞键。
    // 2026-10-29 修复（输入事件审计 F1）：此前无条件 return true，
    // 任何应用里 Shift+Space 都被拦——游戏里 Shift=冲刺/潜行、Space=跳。
    // 注意：联想态的 Shift+空格由上方联想分支先行消费——实际行为是
    // 「选当前页第一个联想词上屏」，**不**切换全半角（三端实测一致；
    // 2026-10-30 按实际行为修正文档与本节注释）；只有组词态才会走到
    // handleKey 的全半角切换分支。
    if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
        return isComposing() || !assoc_.candidates.empty();
    }

    // 只在需要打字时介入（2026-10-29 用户要求）：未组词状态下，先判
    // 该键是否与组词相关——字母（不带 Shift）或标点才有「开始组词/出
    // 中文标点」的可能，其余键（数字/导航/F键/空格等）立即放行，且绝不
    // 触碰连接状态；内容键才允许尝试 daemon 连接（服务不可达仍放行）。
    if (!isComposing()) {
        const bool relevant = (vk >= kVkFirstLetter && vk <= kVkLastLetter && !shiftActive()) ||
                              keyPunctChar(vk, shiftActive()) != nullptr;
        if (!relevant) return false;
        // 零阻塞（审计 T4）：只在已连接时介入；未连接则入队后台连接并
        // fail-open——本键原样进文档，下一次按键即可正常组词，绝不 Sleep/建进程。
        if (!ensureDaemonFast(const_cast<PipeTransport&>(transport_))) return false;
    }

    // Shift+字母（大写英文，含 CapsLock）：放行给宿主，不进组词
    if (vk >= kVkFirstLetter && vk <= kVkLastLetter) return !shiftActive();
    // 翻页键：- / = / [ / ] 不带 Shift（Shift 变体 _ + { } 是字符，
    // 与 mac 字符级判定一致放行）；PageUp/PageDown 无论 Shift 都翻页
    //（mac keyCode/Linux keysym 都不看 Shift，2026-10-31 三端对齐）。
    const bool isPageKey =
        (vk == kVkPrior || vk == kVkNext) ||
        (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus ||
                            vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
    if (isPageKey && isComposing()) return hasCandidates();
    // 双拼模式下 ';' 是引擎的组词/解码键（见 handleKey 的 DP 路由），
    // 同样要在这里预告吞键——TSF 两阶段模型里 OnTestKeyDown 预告 FALSE
    // 时 OnKeyDown 可能根本收不到该键（参照上方字母/标点判定）。
    if (!dpScheme_.empty() && isDpSemicolonKey(vk, shiftActive())) return true;
    if (keyPunctChar(vk, shiftActive()) != nullptr) return true;
    if (!isComposing()) return false;
    // 数字选词只在不带 Shift 且本页确有该序号候选时；Shift+数字产生的
    // !@# 等字符与本页没有的序号都放行（与 handleKey 的判定保持一致）。
    // 小键盘 1-9（VK_NUMPAD1-9）同样可选词，且不受 Shift 影响。
    const int digit = digitIndex(vk);
    // 方向键只在组词态**有候选**时消费（页内预选）；无候选时放行给宿主，
    // 与「输入法没有理由拦截应用自己的方向键」一致。
    return vk == kVkBackspace || vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
           (isArrowKey(vk) && hasCandidates()) ||
           (digit >= 0 && (vk >= kVkNumpad1 || !shiftActive()) &&
            digit < static_cast<int>(comp_.candidates.size()));
}


bool ChaosetTIP::handleKey(WPARAM vk, ITfContext* pic) {
    if (isModifierKey(vk)) return false;

    // 英文模式：内容键一律放行。**必须在实际处理点判断**，不能只靠
    // OnTestKeyDown 的预告分支——实测 Chrome / conhost / Windows Terminal
    // 连内容键也走 Key 阶段（Test 阶段收不到修饰键，同理收不到这些键），
    // 预告分支根本不执行，于是「提示切到英文、打字仍出拼音候选」。
    // 实机日志：16:13:42 shift toggle enMode=1 之后 16:13:43 仍在
    // candidate shown（2026-11-05）。
    if (currentEnglish()) {
        clearAssociate();
        if (isComposing() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);  // 切换瞬间没收干净的残留，这里兜底清理
        }
        return false;  // 放行给宿主
    }

    // 零阻塞（审计 T4）：Key-only 宿主（Chrome/conhost/Terminal 不投递 Test
    // 阶段）在这里补一次非阻塞连接探测；失败时各分支 fail-open（不阻塞、
    // 按键原样进文档）。Test 阶段已连接时这里只是一次分支预测。
    if (!transport_.isConnected()) ensureDaemonFast(transport_);

    // 词组联想分支（对齐 macOS 壳，2026-10-27 接入 Windows）：
    //   数字（有对应项）→ 联想词直接上屏并继续联想；
    //   空格 → 结束联想，空格透传；回车 → 结束联想并消费；
    //   Esc → 收起联想；- / = → 单页无翻页语义，收起放行；
    //   其余按键 → 退出联想走正常流程（字母进组词 / 标点出中文标点 /
    //   Ctrl 等修饰键照旧放行）。
    if (!assoc_.candidates.empty()) {
        const bool shift = shiftActive();
        if (vk == kVkEscape || vk == kVkReturn) {
            clearAssociate();
            return true;
        }
        // 空格：确认**联想页内预选项**（默认第 0 项；方向键可移动预选，
        // 渐进组句闭环，与 macOS 端一致），不再是透传
        if (vk == kVkSpace) {
            if (!assoc_.candidates.empty()) {
                const int start = assocPage_ * kPageSize;
                int count = static_cast<int>(assoc_.candidates.size()) - start;
                if (count > kPageSize) count = kPageSize;
                int sel = candidateWnd_.selectedIndex();
                if (sel < 0 || sel >= count) sel = 0;
                const chaoset::Candidate pick = assoc_.candidates[start + sel];
                std::string committed;
                if (session_.associateSelect(pick.text, splitSyllables(pick.syllables), &committed)) {
                    const std::string sylls = pick.syllables;
                    // 上屏确实写进了文档才继续联想：否则联想窗悬在
                    // 「词没上屏却推下一批」的假状态（2026-10-29 审查）。
                    if (commit(pic, committed)) enterAssociate(sylls);
                    return true;
                }
            }
            clearAssociate();
            return true;
        }
        // 方向键：移动联想页内预选（上/左前一项，下/右后一项；页首/页尾
        // 跨页）。联想面板可见时方向键属于输入法，不再「退出联想并透传」。
        if (isArrowKey(vk)) {
            moveAssociateSelection((vk == kVkUp || vk == kVkLeft) ? -1 : 1);
            return true;
        }
        // - / =：联想翻页（边界页保持当前页）
        // Backspace：收起联想并消费——透传会误删刚上屏的文字
        if (vk == kVkBackspace) {
            clearAssociate();
            return true;
        }
        // 联想翻页键：- / =、[ / ]、PageUp / PageDown——与组词态翻页
        // 键集合一致（2026-10-29 三端一致性审查）。此前只认 - / =，
        // 其余键落到「其余按键退出联想」：面板收起而按键透传给宿主
        // （文档跟着滚动），与 macOS/Linux 行为各不相同。
        const bool assocPageKey =
            vk == kVkPrior || vk == kVkNext ||
            (!shift && (vk == kVkOemMinus || vk == kVkOemPlus ||
                        vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
        if (assocPageKey) {
            const bool next = vk == kVkOemPlus || vk == kVkOemCloseBrackets ||
                              vk == kVkNext;
            flipAssociate(next);
            return true;
        }
        const int assocDigit = digitIndex(vk);
        if (assocDigit >= 0 && (vk >= kVkNumpad1 || !shift)) {
            // 全局序号 = 当前页 × 每页条数 + 页内序号（候选窗展示的是切片）
            const int index = assocPage_ * kPageSize + assocDigit;
            if (index < static_cast<int>(assoc_.candidates.size())) {
                const chaoset::Candidate pick = assoc_.candidates[index];
                std::string committed;
                if (session_.associateSelect(pick.text, splitSyllables(pick.syllables), &committed)) {
                    const std::string sylls = pick.syllables;
                    // 上屏确实写进了文档才继续联想：否则联想窗悬在
                    // 「词没上屏却推下一批」的假状态（2026-10-29 审查）。
                    if (commit(pic, committed)) enterAssociate(sylls);
                } else {
                    clearAssociate();
                }
                return true;
            }
            clearAssociate(); // 越界序号：收起联想放行数字
            return false;
        }
        clearAssociate(); // 其余按键退出联想，落到下方正常流程
    }

    const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
    const bool alt  = (GetKeyState(VK_MENU) & 0x8000) != 0;
    const bool win  = ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);

    // 组词中 Backspace/Delete：修饰键映射（2026-10-31 用户要求：
    // Control↔Ctrl、Option↔Alt、Command↔Win）。优先级与 macOS 实现一致：
    // Command/Ctrl/Shift → 全部删除（含未落盘确认段，与 Esc 同语义）；
    // Alt → 按音节回退（engine.backspaceWord）；无修饰 → 下方逐字符回退。
    // 非组词态一律放行给宿主。
    //
    // 注意：Win/Super 组合可能先被系统外壳拦截（TSF 未必收得到），
    // 输入法只在收到按键时按 Command 语义处理——真机以实际到达为准。
    if (isComposing() && (vk == kVkBackspace || vk == kVkDelete)) {
        const bool shift = (GetKeyState(VK_SHIFT) & 0x8000) != 0;
        if (win || ctrl || shift) {
            resetComposition();
            comp_ = chaoset::Comp();
            confirmedPreedit_.clear();
            endComposition();
            return true;
        }
        if (alt) {
            comp_ = session_.backspaceWord();
            if (!comp_.ok) {
                diagDaemonRequestFailure(transport_, "backspaceWord");
                flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                return true;
            }
            syncDpScheme(comp_);
            if (comp_.input.empty()) {
                if (!confirmedPreedit_.empty()) {
                    // 剩余拼音删空：已确认段立即落盘收尾（与普通 Backspace 同路径）
                    comp_ = chaoset::Comp();
                    const std::string all = confirmedPreedit_;
                    confirmedPreedit_.clear();
                    commit(pic, all);
                } else {
                    endComposition();
                }
            } else {
                render(pic);
            }
            return true;
        }
    }

    // Alt 或 Win 快捷键：先收尾未决组词，然后放行原键给宿主。
    // 收尾走 flushOrCancel 而不是 endComposition：用户已确认的段不能
    // 随快捷键透传消失（与 macOS passThrough → flushConfirmed 同语义，
    // 2026-10-30 审计 P1-3）。
    if (alt || win) {
        if (isComposing() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        return false;
    }

    // Ctrl 组合键：
    if (ctrl) {
        // Ctrl+U：类终端整行清空（对齐 macOS 壳，组词中消费并全部删除）；
        // Ctrl+Shift+U 是宿主快捷键（mac/Linux 都放行），不在此列。
        if (vk == 'U' && isComposing() && !shiftActive()) {
            resetComposition();
            endComposition();
            comp_ = chaoset::Comp();
            return true;
        }
        // 其余 Ctrl 组合键（如 Ctrl+A/C/V/Z）：若处于组词中，先收尾再放行
        // 原键给宿主——已确认段随透传落盘（用户确认过的字不能丢）
        if (isComposing() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        return false;
    }

    // 字母：追加组词。Shift+字母（大写英文，含 CapsLock）不进组词：
    // 放行原字符，组词中先清空——此前无条件小写化，Shift+A 会变成 a
    // 混进拼音（mac 壳 isCompositionChar 只认小写，此处对齐）。
    if (vk >= kVkFirstLetter && vk <= kVkLastLetter) {
        if (shiftActive()) {
            // 大写英文不进组词：放行原字符，组词先收尾（已确认段随透传落盘）
            if (isComposing() || !confirmedPreedit_.empty()) {
                flushOrCancel(pic);
            }
            return false;
        }
        std::string ch(1, static_cast<char>('a' + (vk - kVkFirstLetter)));
        comp_ = session_.input(ch, halfWidth_);
        if (!comp_.ok) {
            // daemon 确实不可达：收尾（已确认段落盘，不因 daemon 故障丢字）
            // 并放行原键，保证宿主应用不卡死
            diagDaemonRequestFailure(transport_, "input");
            flushOrCancel(pic);
            return false;
        }
        syncDpScheme(comp_);  // 双拼方案名（';' 路由依据，见 syncDpScheme）
        render(pic);
        // 渲染后仍没有合成对象：当前上下文不接受 TSF 合成（Windows 11
        // 开始菜单搜索框等）。此时必须撤掉本地未决状态并放行原键，
        // 否则就是「键被吃掉、字也没出来」——2026-10-29 实机反馈。
        // OnKeyDown 返回 false 是两阶段按键模型的最终裁决，TSF 会把
        // 原键交还宿主（OnTestKeyDown 的 eaten 只是预告）。
        //
        // 兜底条件必须同时覆盖两类拒绝：合成根本没建出来
        // （composition_ == nullptr），以及 StartComposition「成功」但
        // 预编辑 SetText 被拒（只读上下文在编辑会话里仍会拒绝写入，
        // 此时有合成对象却没有合成文本——TS_ES_READONLY）。两者都
        // 意味着键进不了文档，必须放行。
        if (isComposing() && (composition_ == nullptr || !preeditWritten_)) {
            return bailOutNoComposition();
        }
        return true;
    }

    // 候选翻页：- / =、[ / ]、PageUp / PageDown（组词中且有候选时消费；
    // 空组词放行原键/出标点）。- / = / [ / ] 的 Shift 变体是字符不翻页；
    // PageUp/PageDown 无论 Shift 都翻页（对齐 mac/Linux）。
    const bool isPageKey =
        (vk == kVkPrior || vk == kVkNext) ||
        (!shiftActive() && (vk == kVkOemMinus || vk == kVkOemPlus ||
                            vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
    if (isPageKey && isComposing() && hasCandidates()) {
        bool isPrev = (vk == kVkOemMinus || vk == kVkOemOpenBrackets || vk == kVkPrior);
        comp_ = isPrev ? session_.prevPage() : session_.nextPage();
        if (!comp_.ok) {
            diagDaemonRequestFailure(transport_, "page");
            flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
            return true;
        }
        syncDpScheme(comp_);
        render(pic);
        return true;
    }

    // 方向键预选（issue#2）：组词有候选时移动页内预选（页首/页尾跨页），
    // 与翻页键语义不同、互不影响；空格随后确认预选项。无候选/无组词时
    // wouldHandle 已放行，不会走到这里。
    if (isArrowKey(vk) && isComposing() && hasCandidates()) {
        moveCompositionSelection((vk == kVkUp || vk == kVkLeft) ? -1 : 1, pic);
        return true;
    }

    // 双拼模式的 ';'（配合引擎契约）：双拼时 ';' 不是标点键，而是引擎的
    // 组词/解码键——dpPending 非 0 时与字母一样组成 pair 解码，为 0 时
    // 按 punct(";") 语义（组词中先上屏首选+标点）。必须路由到 input 而
    // 不是 punct，引擎才能在双拼键位状态机里看到它。Shift+; 产生的
    // ':' 不在此列（仍走下方标点分支），与 mac 的字符级判定一致。
    if (!dpScheme_.empty() && isDpSemicolonKey(vk, shiftActive())) {
        comp_ = session_.input(";", halfWidth_);
        if (!comp_.ok) {
            diagDaemonRequestFailure(transport_, "input;");
            flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
            return false;       // 绝不吞键：daemon 不可达时让 ; 原样进文档
        }
        syncDpScheme(comp_);
        if (!comp_.committed.empty()) {
            // 引擎已把「上屏文本」（组词中首选 + 分号 / 空组词的分号）
            // 放进 committed：壳侧已确认段必须先拼接再一次性落盘——
            // 用户确认过的字不能丢（与 mac 的 confirmedText + committed
            // 同语义，2026-10-30 三端接口契约）。
            const std::string all = confirmedPreedit_ + comp_.committed;
            confirmedPreedit_.clear();
            commit(pic, all);
            return true;
        }
        render(pic);
        return true;
    }

    // 标点键（US 布局字符级映射，含 Shift 变体）：组词中先上屏首选再出
    // 中文标点，空组词直接出中文标点；映射规则在 daemon 侧统一完成。
    if (const char* key = keyPunctChar(vk, shiftActive())) {
        if (vk == kVkOemQuotes && isComposing()) {
            // 组词中的撇号是音节分隔符，走组词流程
            comp_ = session_.input("'", halfWidth_);
            if (!comp_.ok) {
                diagDaemonRequestFailure(transport_, "input'");
                flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
                return false;
            }
            syncDpScheme(comp_);
            render(pic);
            return true;
        }
        const bool composing = isComposing();
        std::string committed;
        if (session_.punct(key, halfWidth_, &committed)) {
            // 标点收尾：已确认段 + 首选候选 + 标点一次落盘
            //（上屏失败（上下文不可插入文本）且没有未决拼音时放行原键，
            // 避免中文标点凭空消失；有未决拼音时仍需消费，保持组词状态机完整）
            const std::string all = confirmedPreedit_ + committed;
            confirmedPreedit_.clear();
            if (commit(pic, all) || composing) return true;
            return false;
        }
        // daemon 不可达：收尾（已确认段落盘）后放行原键，不卡宿主应用
        if (composing || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        return false;
    }

    // Shift+空格：切换全角/半角标点模式。放在 isComposing 守卫**之前**：
    // 用户常在开始打字前先切模式，此前放在守卫之后导致空组词时永远
    // 切不动（mac 壳在组词判断之前处理，此处对齐；wouldHandle 已对
    // Shift+空格无条件返回 TRUE，两条路径此前互相矛盾）。
    // 联想态到不了这里：上方联想分支已把空格当「选第一个联想词」消费
    //（三端实测一致，2026-10-30 文档修正），因此本分支实际只处理组词态。
    if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
        // 无组词/无联想时放行给宿主：输入法没有理由拦截应用自己的
        // Shift+Space（2026-10-29 输入事件审计 F1）。
        if (!isComposing() && assoc_.candidates.empty()) return false;
        // 全/半角开关归壳本地持有（2026-10-29 起）：快捷键切换不再与
        // daemon 往返，状态随 punct 请求下发（daemon 只按状态产出文本）
        halfWidth_ = !halfWidth_;
        showToast(halfWidth_ ? L"半角标点" : L"全角标点");
        return true;
    }

    if (!isComposing()) return false;

    // 普通 Backspace / ForwardDelete（无修饰）：逐字符回退。
    // Delete 与 Backspace 同语义（对齐 macOS 组词中两者都删拼音）；
    // 带修饰的组合已在上面按映射处理。
    if (vk == kVkBackspace || vk == kVkDelete) {
        comp_ = session_.backspace();
        if (!comp_.ok) {
            diagDaemonRequestFailure(transport_, "backspace");
            flushOrCancel(pic); // daemon 故障：已确认段落盘收尾
            return true;
        }
        syncDpScheme(comp_);
        if (comp_.input.empty()) {
            if (!confirmedPreedit_.empty()) {
                // 剩余拼音删空：已确认段立即落盘收尾——不能继续挂在
                // 合成里变成「无拼音的孤合成」（对齐 macOS 壳）
                comp_ = chaoset::Comp();
                const std::string all = confirmedPreedit_;
                confirmedPreedit_.clear();
                commit(pic, all);
            } else {
                endComposition();
            }
        }
        else render(pic);
        return true;
    }

    if (vk == kVkEscape) {
        // Esc：直接全部删除（清空全部拼音）。含未落盘的已确认段一并取消
        //——与 macOS 壳/系统拼音语义一致：合成内已转换但未上屏的内容
        // 随整条组词一起丢弃。
        resetComposition();
        comp_ = chaoset::Comp();
        confirmedPreedit_.clear();
        endComposition();
        return true;
    }

    // 数字选词：只认不带 Shift 的 1-9，且按**本页实际候选数**判定——
    // 页面容量是 6，越界数字不消费、落到末尾兜底（清组词后放行原键）。
    // 此前 select 失败仍 return true：数字凭空消失，既不选词也不进文档
    // （mac 壳 2026-10-26 已修，此处对齐）。带 Shift 的数字（!@# 等字符）
    // 不属于选词，同样落到末尾放行。
    const int composeDigit = digitIndex(vk);
    if (composeDigit >= 0 && (vk >= kVkNumpad1 || !shiftActive())) {
        int index = composeDigit;
        if (index < static_cast<int>(comp_.candidates.size())) {
            // 上屏候选的音节串必须在 selectPrefix 之前取：成功后
            // comp_ 会被替换成剩余组词态（或清空），候选表丢了。
            const std::string pickedSyllables = comp_.candidates[index].syllables;
            // 渐进上屏：只消耗候选音节对应的拼音前缀，剩余回灌继续组词
            //（2026-10-29 用户反馈「选一个候选清空整个拼音」不合适）。
            // 单合成模型（见 confirmedPreedit_ 注释）：确认段并入当前
            // 合成，不再中途结束/重建合成——部分 App 对合成重建的选区
            // 记账有缺陷，会把刚上屏的段覆盖丢（用户实测选「今」丢字）。
            chaoset::Comp rest;
            if (session_.selectPrefix(index, &rest) && !rest.committed.empty()) {
                syncDpScheme(rest);
                confirmedPreedit_ += rest.committed;
                comp_ = rest;
                if (!comp_.input.empty()) {
                    render(pic);
                } else {
                    // 剩余耗尽：整串一次落盘，与整句上屏同收尾（进入联想）。
                    // 音节基准传候选自身的撇号音节串（2026-10-29 修复：
                    // 不能把上屏词文本当音节传，单字词会切错位）。
                    comp_ = chaoset::Comp();
                    const std::string all = confirmedPreedit_;
                    confirmedPreedit_.clear();
                    commit(pic, all);
                    enterAssociate(pickedSyllables);
                }
            } else {
                // daemon 不可达/越界：回退整句上屏（现状语义）
                std::string committed;
                if (session_.select(index, &committed)) {
                    const std::string all = confirmedPreedit_ + committed;
                    confirmedPreedit_.clear();
                    commit(pic, all);
                }
            }
            return true;
        }
    }

    if (vk == kVkReturn && isComposing()) {
        // 回车语义：已确认段按确认文本、剩余拼音按原始串一起上屏
        //（与 macOS 壳一致：已转换部分保留转换结果）
        std::string raw = confirmedPreedit_ + comp_.input;
        confirmedPreedit_.clear();
        resetComposition();
        commit(pic, raw);
        return true;
    }

    // 空格：组词中一律消费（用户意图永远是「结束组词」，不应有裸空格
    // 漏进文档）——有候选选首选；残缺组词顶屏原始拼音，与回车等效
    // （此前要求 hasCandidates，无候选时空格透传进文档，mac 2026-10-25
    // 实机反馈的同族问题）。Shift+空格已在前面的全半角分支处理。
    if (vk == kVkSpace) {
        if (hasCandidates()) {
            // 空格确认**页内预选项**（默认第 0 项；方向键可移动预选）。
            int sel = candidateWnd_.selectedIndex();
            if (sel < 0 || sel >= static_cast<int>(comp_.candidates.size())) sel = 0;
            // 同数字选词：音节奏必须在 selectPrefix 之前取（见上）
            const std::string pickedSyllables = comp_.candidates[sel].syllables;
            chaoset::Comp rest;
            if (session_.selectPrefix(sel, &rest) && !rest.committed.empty()) {
                syncDpScheme(rest);
                // 单合成渐进（见 confirmedPreedit_ 注释，与数字选词一致）
                confirmedPreedit_ += rest.committed;
                comp_ = rest;
                if (!comp_.input.empty()) {
                    render(pic);
                } else {
                    comp_ = chaoset::Comp();
                    const std::string all = confirmedPreedit_;
                    confirmedPreedit_.clear();
                    commit(pic, all);
                    enterAssociate(pickedSyllables);
                }
                return true;
            }
            std::string committed;
            if (session_.select(sel, &committed)) {
                const std::string all = confirmedPreedit_ + committed;
                confirmedPreedit_.clear();
                commit(pic, all);
            }
            return true;
        }
        // 残缺组词顶屏：已确认段按确认文本、剩余拼音按原始串一起上屏
        //（与上方回车分支一致：用户确认过的字不能丢，2026-10-30
        // 审计 P1-3）。先取出拼接再清缓冲，顺序与 flush 型收尾一致。
        std::string raw = confirmedPreedit_ + comp_.input;
        confirmedPreedit_.clear();
        resetComposition();
        commit(pic, raw);
        return true;
    }

    // 不可处理的按键（F 键 / 数字 0 / Shift+数字的 @# 等字符）：
    // 组词中先收尾再放行，避免预编辑与实文脱节（对齐 macOS 壳 passThrough；
    // 已确认段随透传落盘，用户确认过的字不能丢）
    if (isComposing() || !confirmedPreedit_.empty()) {
        flushOrCancel(pic);
    }
    return false;
}
