// ChaosetTIP 按键路由：ITfKeyEventSink 五入口 + Shift 状态机 + 键位映射
// （Windows 壳拆分 TU，wave2）。
//
// 从 win32.cpp 的类内 inline 定义逐字拆出：OnTestKeyDown/OnKeyDown/OnTestKeyUp/
// OnKeyUp/OnPreservedKey、Shift 空按记账（位掩码 + 物理状态自愈）、修饰键/键位
// 映射（keyPunctChar/digitIndex/shiftDown/isModifierKey）、wouldHandle/handleKey。
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
        const bool shift = shiftDown();
        const bool letter = wParam >= kVkFirstLetter && wParam <= kVkLastLetter;
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
        if (isComposing() || hasDpPending() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        *pfEaten = FALSE;  // 英文模式全放行
        return S_OK;
    }
    if (!wouldHandle(wParam)) {
        clearAssociate();
        if (isComposing() || hasDpPending() || !confirmedPreedit_.empty()) {
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


// Ctrl/Alt/Win 此刻是否物理按下。用于区分「单独轻按 Shift」与「组合键里的
// Shift」：后者不是空按。典型场景是 UI 宿主注册的 Ctrl+Shift+Space 全局热键——
// 系统会吞掉 Space，TIP 只看到 Ctrl/Shift 的按下与释放；若仍把 Shift 当空按，
// UI 宿主已经翻转一次，TIP 松 Shift 时又翻转一次（双翻转），并把当前组词 flush。
// 只在开始一次按住会话时查询，正常打字路径零额外开销。
static bool otherModifierPhysicallyDown() {
    return (GetAsyncKeyState(VK_CONTROL) & 0x8000) != 0 ||
           (GetAsyncKeyState(VK_MENU) & 0x8000) != 0 ||
           (GetAsyncKeyState(VK_LWIN) & 0x8000) != 0 ||
           (GetAsyncKeyState(VK_RWIN) & 0x8000) != 0;
}


// 开始一次「按住会话」：位运算幂等（auto-repeat / 重复投递都不累积）。
void ChaosetTIP::beginShiftHold(unsigned bit) {
    if ((shiftHeld_ & bit) != 0) return;
    if (shiftHeld_ == 0) {
        // Ctrl/Alt/Win 已按住时，这次 Shift 是组合键的一部分，不是空按。
        // Shift 先按、其它修饰键后按的路径由 OnTestKeyDown/OnKeyDown 里的
        // sinceShift_ = true 覆盖。
        sinceShift_ = otherModifierPhysicallyDown();
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


// 物理 Shift 按下：数字/OEM 变体、Shift+Space、Ctrl+U 等修饰语义只看它
// （CapsLock 只影响字母大小写 `shiftDown != capsOn`，不把 1/'/-/[ 变成 Shift 变体）。
bool ChaosetTIP::shiftDown() {
    return (GetKeyState(VK_SHIFT) & 0x8000) != 0;
}


// 方向键语义（用户反馈）：上/下 = 翻页，左/右 = 页内预选移动。
bool ChaosetTIP::isArrowKey(WPARAM vk) {
    return vk == kVkLeft || vk == kVkUp || vk == kVkRight || vk == kVkDown;
}


// 组词态页内预选移动：只在当前页内前/后移动；页边界保持不动（仍消费该键）。
void ChaosetTIP::moveCompositionSelection(int delta) {
    if (!hasCandidates()) return;
    candidateWnd_.moveSelection(delta);
}


// 组词态翻页：上=上一页、下=下一页；翻页后页内预选归零。
// 边界页保持不动（仍消费该键）。异步管线（issue #149）：只入队，结果回来
// 由 tip_async.cpp 应用并刷新候选——本函数绝不等待 daemon。
void ChaosetTIP::flipCompositionPage(bool next, ITfContext* /*pic*/) {
    if (!hasCandidates()) return;
    if (next) {
        if (comp_.page + 1 >= comp_.pageCount) return;  // 末页：保持不动
    } else {
        if (comp_.page <= 0) return;  // 首页：保持不动
    }
    chaoset::win::EngineOp op;
    op.kind = next ? chaoset::win::EngineOpKind::kNextPage
                   : chaoset::win::EngineOpKind::kPrevPage;
    submitEngineOp(op);
    candidateWnd_.setSelectedIndex(0);
}


// 联想态页内预选移动（联想候选是壳本地分页：assocPage_ 起、每页 kPageSize）。
void ChaosetTIP::moveAssociateSelection(int delta) {
    if (assoc_.candidates.empty()) return;
    candidateWnd_.moveSelection(delta);
}


// 联想态翻页：上=上一页、下=下一页；翻页后页内预选归零。
// 边界页保持不动（仍消费该键）。
void ChaosetTIP::flipAssociatePage(bool next) {
    if (assoc_.candidates.empty()) return;
    const int total = static_cast<int>(assoc_.candidates.size());
    const int pageCount = (total + kPageSize - 1) / kPageSize;
    if (next) {
        if (assocPage_ + 1 >= pageCount) return;  // 末页：保持不动
    } else {
        if (assocPage_ <= 0) return;  // 首页：保持不动
    }
    if (flipAssociate(next)) candidateWnd_.setSelectedIndex(0);
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
bool ChaosetTIP::wouldHandle(WPARAM vk) {
    if (isModifierKey(vk)) return false;

    const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
    const bool alt  = (GetKeyState(VK_MENU) & 0x8000) != 0;
    const bool win  = ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);

    // 组词中 Backspace/Delete：修饰键映射与 macOS 对齐（2026-10-31 用户要求：
    // Control↔Ctrl、Option↔Alt、Command↔Win）。两者都消费，具体行为在
    // handleKey 里按「Command/Ctrl/Shift → 全部删除；Alt → 按音节回退；
    // 无修饰 → 逐字符回退」处理。非组词态一律放行给宿主。
    if ((isComposing() || hasDpPending()) && (vk == kVkBackspace || vk == kVkDelete)) return true;
    // Ctrl+U 整行清空；Ctrl+Shift+U 是宿主快捷键（mac/Linux 都放行），
    // 所以这里必须要求不带 Shift。
    if (ctrl) return (isComposing() || hasDpPending()) && vk == 'U' && !shiftDown();
    if (alt || win) return false;

    // 联想模式：数字（有对应项）/空格（选预选项）/方向键（移动页内预选）/
    // 回车/Esc/Backspace（收起）由联想分支消费；其余按键退出联想后按正常
    // 流程判定（继续往下走）。
    if (!assoc_.candidates.empty()) {
        // 方向键只在联想面板**实际可见**时消费（全屏免打扰/建窗失败/渲染失败时
        // 面板不可见，方向键应放行宿主，绝不做不可见的翻页/预选）。
        if (vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
            vk == kVkBackspace || (isArrowKey(vk) && candidateWnd_.itemCount() > 0)) return true;
        // 联想翻页键：PageUp/PageDown 不判 Shift（与 mac/Linux 一致：
        // 联想态 Shift+PageUp/PageDown 同样翻联想页，2026-10-30 审计 A7）。
        if (vk == kVkPrior || vk == kVkNext) return true;
        if (!shiftDown() && (vk == kVkOemMinus || vk == kVkOemPlus)) return true;
        const int digit = digitIndex(vk);
        if (digit >= 0 && (vk >= kVkNumpad1 || !shiftDown())) {
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
    // 该键是否与组词相关——大小写字母或标点才有「开始组词/出中文标点」
    // 的可能，其余键（数字/导航/F键/空格等）立即放行，且绝不触碰连接状态；
    // 内容键才允许尝试 daemon 连接（服务不可达仍放行）。
    if (!isComposing()) {
        const bool relevant = (vk >= kVkFirstLetter && vk <= kVkLastLetter) ||
                              keyPunctChar(vk, shiftDown()) != nullptr ||
                              (hasDpPending() && vk == kVkEscape);
        if (!relevant) return false;
        // 异步管线（issue #149）：连接状态只是原子快照；未连接则本键 fail-open
        // （原样进文档）并按节流发探测请求，daemon 恢复后下一键即可正常组词。
        if (!engineAvailable()) {
            probeEngineIfDown();
            return false;
        }
    }

    // 大小写字母一律进组词（与 handleKey 一致，保留原样、匹配按小写）。
    if (vk >= kVkFirstLetter && vk <= kVkLastLetter) return true;
    // 翻页键：- / = / [ / ] 不带 Shift（Shift 变体 _ + { } 是字符，
    // 与 mac 字符级判定一致放行）；PageUp/PageDown 无论 Shift 都翻页
    //（mac keyCode/Linux keysym 都不看 Shift，2026-10-31 三端对齐）。
    const bool isPageKey =
        (vk == kVkPrior || vk == kVkNext) ||
        (!shiftDown() && (vk == kVkOemMinus || vk == kVkOemPlus ||
                            vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
    if (isPageKey && isComposing()) return hasCandidates();
    // 双拼模式下 ';' 是引擎的组词/解码键（见 handleKey 的 DP 路由），
    // 同样要在这里预告吞键——TSF 两阶段模型里 OnTestKeyDown 预告 FALSE
    // 时 OnKeyDown 可能根本收不到该键（参照上方字母/标点判定）。
    if (!dpScheme_.empty() && isDpSemicolonKey(vk, shiftDown())) return true;
    if (keyPunctChar(vk, shiftDown()) != nullptr) return true;
    if (!isComposing()) return hasDpPending() && vk == kVkEscape;
    // 数字选词只在不带 Shift 且本页确有该序号候选时；Shift+数字产生的
    // !@# 等字符与本页没有的序号都放行（与 handleKey 的判定保持一致）。
    // 小键盘 1-9（VK_NUMPAD1-9）同样可选词，且不受 Shift 影响。
    const int digit = digitIndex(vk);
    // 方向键只在组词态**有候选**时消费（页内预选）；无候选时放行给宿主，
    // 与「输入法没有理由拦截应用自己的方向键」一致。
    return vk == kVkBackspace || vk == kVkEscape || vk == kVkReturn || vk == kVkSpace ||
           (isArrowKey(vk) && hasCandidates() && candidateWnd_.itemCount() > 0) ||
           (digit >= 0 && (vk >= kVkNumpad1 || !shiftDown()) &&
            (digit < static_cast<int>(comp_.candidates.size()) || !pendingOps_.empty()));
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
        if (isComposing() || hasDpPending() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);  // 切换瞬间没收干净的残留，这里兜底清理
        }
        return false;  // 放行给宿主
    }

    // 异步引擎管线（issue #149，见 tip_async.cpp）：先确保本 TIP 的引擎线程
    // 存在；连接不可用且当前没有本地组词时 fail-open（原样放行），并按节流发
    // 一个探测请求，daemon 恢复后下一键即可正常组词。有本地组词时必须消费按键
    // （绝不能把拼音字母漏进文档），继续乐观处理，由 2s 宽限期决定何时按原始串
    // 落盘。整条路径零等待：daemon 的慢/死都不会阻塞宿主 UI 线程。
    ensureEnginePump();
    if (!engineAvailable()) {
        probeEngineIfDown();
        if (!isComposing() && !hasDpPending() && confirmedPreedit_.empty()) return false;
    }
    setEngineContext(pic);

    // 词组联想分支（对齐 macOS 壳）：数字/空格/方向键/回车/Esc/退格消费；
    // 选词只入队（结果回来时上屏并继续联想），绝不等待 daemon。
    if (!assoc_.candidates.empty()) {
        const bool shift = shiftDown();
        if (vk == kVkEscape || vk == kVkReturn) {
            clearAssociate();
            return true;
        }
        if (vk == kVkSpace) {
            const int start = assocPage_ * kPageSize;
            int count = static_cast<int>(assoc_.candidates.size()) - start;
            if (count > kPageSize) count = kPageSize;
            int sel = candidateWnd_.selectedIndex();
            if (sel < 0 || sel >= count) sel = 0;
            const chaoset::Candidate pick = assoc_.candidates[start + sel];
            chaoset::win::EngineOp op;
            op.kind = chaoset::win::EngineOpKind::kAssociateSelect;
            op.text = pick.text;
            op.syllables = pick.syllables;
            submitNonComposingOp(op);
            return true;
        }
        if (isArrowKey(vk) && candidateWnd_.itemCount() > 0) {
            if (vk == kVkUp || vk == kVkDown) {
                flipAssociatePage(vk == kVkDown);
            } else {
                moveAssociateSelection(vk == kVkRight ? 1 : -1);
            }
            return true;
        }
        if (vk == kVkBackspace) {
            clearAssociate();
            return true;
        }
        const bool assocPageKey =
            vk == kVkPrior || vk == kVkNext ||
            (!shift && (vk == kVkOemMinus || vk == kVkOemPlus ||
                        vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
        if (assocPageKey) {
            const bool next = vk == kVkOemPlus || vk == kVkOemCloseBrackets || vk == kVkNext;
            flipAssociate(next);
            return true;
        }
        const int assocDigit = digitIndex(vk);
        if (assocDigit >= 0 && (vk >= kVkNumpad1 || !shift)) {
            const int index = assocPage_ * kPageSize + assocDigit;
            if (index < static_cast<int>(assoc_.candidates.size())) {
                const chaoset::Candidate pick = assoc_.candidates[index];
                chaoset::win::EngineOp op;
                op.kind = chaoset::win::EngineOpKind::kAssociateSelect;
                op.text = pick.text;
                op.syllables = pick.syllables;
                submitNonComposingOp(op);
                return true;
            }
            clearAssociate();  // 越界序号：收起联想放行数字
            return false;
        }
        clearAssociate();  // 其余按键退出联想，落到下方正常流程
    }

    const bool ctrl = (GetKeyState(VK_CONTROL) & 0x8000) != 0;
    const bool alt  = (GetKeyState(VK_MENU) & 0x8000) != 0;
    const bool win  = ((GetKeyState(VK_LWIN) & 0x8000) != 0) || ((GetKeyState(VK_RWIN) & 0x8000) != 0);

    // 组词中 Backspace/Delete：修饰键映射（Control↔Ctrl、Option↔Alt、Command↔Win）。
    // Command/Ctrl/Shift → 全部删除（本地立即生效 + 丢弃在飞结果）；Alt → 按音节回退。
    if ((isComposing() || hasDpPending()) && (vk == kVkBackspace || vk == kVkDelete)) {
        const bool shiftKey = (GetKeyState(VK_SHIFT) & 0x8000) != 0;
        if (win || ctrl || shiftKey) {
            resetComposition();
            comp_ = chaoset::Comp();
            confirmedPreedit_.clear();
            endComposition();
            return true;
        }
        if (alt) {
            chaoset::win::EngineOp op;
            op.kind = chaoset::win::EngineOpKind::kBackspaceWord;
            op.halfWidth = halfWidth_;
            submitEngineOp(op);
            render(pic);
            return true;
        }
    }

    // Alt 或 Win 快捷键：先收尾未决组词，然后放行原键给宿主。
    // 收尾走 flushOrCancel 而不是 endComposition：用户已确认的段不能随快捷键透传消失。
    if (alt || win) {
        if (isComposing() || hasDpPending() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        return false;
    }

    // Ctrl 组合键：
    if (ctrl) {
        // Ctrl+U：类终端整行清空（组词中消费并全部删除）；Ctrl+Shift+U 放行宿主。
        if (vk == 'U' && (isComposing() || hasDpPending()) && !shiftDown()) {
            resetComposition();
            endComposition();
            comp_ = chaoset::Comp();
            return true;
        }
        // 其余 Ctrl 组合键：若处于组词中，先收尾再放行原键给宿主。
        if (isComposing() || hasDpPending() || !confirmedPreedit_.empty()) {
            flushOrCancel(pic);
        }
        return false;
    }

    // 字母：追加组词。大小写都进组词（Shift+A 是组词里的 A，不再被放行成英文；
    // 实际大小写 = Shift ⊕ CapsLock）。乐观渲染后立即入队，绝不等待 daemon。
    if (vk >= kVkFirstLetter && vk <= kVkLastLetter) {
        const bool shiftDown = (GetKeyState(VK_SHIFT) & 0x8000) != 0;
        const bool capsOn = (GetKeyState(VK_CAPITAL) & 0x0001) != 0;
        const char base = (shiftDown != capsOn) ? 'A' : 'a';
        std::string ch(1, static_cast<char>(base + (vk - kVkFirstLetter)));
        chaoset::win::EngineOp op;
        op.kind = chaoset::win::EngineOpKind::kInput;
        op.text = ch;
        op.halfWidth = halfWidth_;
        const std::uint64_t seq = queuePendingOp(op);
        render(pic);  // 预编辑立即出现（optimisticComp 回放未应用操作），不等 daemon
        // 渲染后仍没有合成对象：当前上下文不接受 TSF 合成（Windows 11 开始菜单
        // 搜索框等）。撤掉乐观项并放行原键，否则就是「键被吃掉、字也没出来」。
        if (isComposing() && (composition_ == nullptr || !preeditWritten_)) {
            dropPendingOp(seq);
            return bailOutNoComposition();
        }
        dispatchPendingOp(seq);
        return true;
    }

    // 候选翻页：- / =、[ / ]、PageUp / PageDown（组词中且有候选时消费）
    const bool isPageKey =
        (vk == kVkPrior || vk == kVkNext) ||
        (!shiftDown() && (vk == kVkOemMinus || vk == kVkOemPlus ||
                            vk == kVkOemOpenBrackets || vk == kVkOemCloseBrackets));
    if (isPageKey && isComposing() && hasCandidates()) {
        const bool isPrev = (vk == kVkOemMinus || vk == kVkOemOpenBrackets || vk == kVkPrior);
        chaoset::win::EngineOp op;
        op.kind = isPrev ? chaoset::win::EngineOpKind::kPrevPage
                         : chaoset::win::EngineOpKind::kNextPage;
        submitEngineOp(op);
        return true;
    }

    // 方向键：上/下 = 翻页，左/右 = 页内预选移动；空格随后确认预选项。
    if (isArrowKey(vk) && isComposing() && hasCandidates() &&
        candidateWnd_.itemCount() > 0) {
        if (vk == kVkUp || vk == kVkDown) {
            flipCompositionPage(vk == kVkDown, pic);
        } else {
            moveCompositionSelection(vk == kVkRight ? 1 : -1);
        }
        return true;
    }

    // 双拼模式的 ';'：路由到 input（引擎的组词/解码键），不是标点。
    if (!dpScheme_.empty() && isDpSemicolonKey(vk, shiftDown())) {
        chaoset::win::EngineOp op;
        op.kind = chaoset::win::EngineOpKind::kInput;
        op.text = ";";
        op.halfWidth = halfWidth_;
        submitEngineOp(op);
        render(pic);
        return true;
    }

    // 标点键（US 布局字符级映射，含 Shift 变体）：组词中先上屏首选再出中文标点，
    // 空组词直接出中文标点。结果回来时上屏；daemon 不可达则按原 ASCII 字符兜底。
    if (const char* key = keyPunctChar(vk, shiftDown())) {
        if (vk == kVkOemQuotes && isComposing()) {
            // 组词中的撇号是音节分隔符，走组词流程
            chaoset::win::EngineOp op;
            op.kind = chaoset::win::EngineOpKind::kInput;
            op.text = "'";
            op.halfWidth = halfWidth_;
            submitEngineOp(op);
            render(pic);
            return true;
        }
        chaoset::win::EngineOp op;
        op.kind = chaoset::win::EngineOpKind::kPunct;
        op.text = key;
        op.halfWidth = halfWidth_;
        submitEngineOp(op);
        return true;
    }

    // Shift+空格：切换全角/半角标点模式（壳本地持有，不往返 daemon）。
    if (vk == kVkSpace && (GetKeyState(VK_SHIFT) & 0x8000)) {
        if (!isComposing() && assoc_.candidates.empty()) return false;
        halfWidth_ = !halfWidth_;
        showToast(halfWidth_ ? L"\u534a\u89d2\u6807\u70b9" : L"\u5168\u89d2\u6807\u70b9");
        return true;
    }

    if (!isComposing() && !hasDpPending()) return false;

    // 普通 Backspace / ForwardDelete（无修饰）：逐字符回退。乐观预编辑立即变化，
    // 结果回来用引擎态覆盖。
    if (vk == kVkBackspace || vk == kVkDelete) {
        chaoset::win::EngineOp op;
        op.kind = chaoset::win::EngineOpKind::kBackspace;
        op.halfWidth = halfWidth_;
        submitEngineOp(op);
        render(pic);
        return true;
    }

    // Esc：直接全部删除（清空全部拼音，含未落盘的已确认段）——本地立即生效，
    // 纪元递增丢弃一切在飞结果，引擎侧只丢组词（保留学习上下文）。
    if (vk == kVkEscape) {
        resetComposition();
        comp_ = chaoset::Comp();
        confirmedPreedit_.clear();
        endComposition();
        return true;
    }

    if (!isComposing()) {
        if (hasDpPending() || !confirmedPreedit_.empty()) flushOrCancel(pic);
        return false;
    }

    // 数字选词：只认不带 Shift 的 1-9，且按**本页实际候选数**判定。
    // 选词只入队：结果回来时按渐进上屏语义上屏；失败用壳侧候选文本兜底。
    const int composeDigit = digitIndex(vk);
    if (composeDigit >= 0 && (vk >= kVkNumpad1 || !shiftDown())) {
        const int index = composeDigit;
        if (index < static_cast<int>(comp_.candidates.size())) {
            const chaoset::Candidate pick = comp_.candidates[index];
            chaoset::win::EngineOp op;
            op.kind = chaoset::win::EngineOpKind::kSelectPrefix;
            op.index = index;
            op.text = pick.text;            // 失败兜底上屏文本
            op.syllables = pick.syllables;  // 上屏后联想来源音节
            submitEngineOp(op);
            return true;
        }
        if (!pendingOps_.empty()) {
            // 列表滞后（结果未应用）：仍按下标入队，引擎按完整输入解析；本次
            // 必须消费数字，绝不能让它漏进文档。
            chaoset::win::EngineOp op;
            op.kind = chaoset::win::EngineOpKind::kSelectPrefix;
            op.index = index;
            submitEngineOp(op);
            return true;
        }
    }

    // 回车：已确认段按确认文本、剩余拼音按原始串（含未应用按键）一起上屏。
    if (vk == kVkReturn && isComposing()) {
        std::string base, text;
        computeOptimistic(nullptr, &base, &text);
        std::string raw = confirmedPreedit_ + (base + text);
        confirmedPreedit_.clear();
        resetComposition();
        commit(pic, raw);
        return true;
    }

    // 空格：组词中一律消费（用户意图永远是「结束组词」，不应有裸空格漏进文档）
    // ——有候选选页内预选项；残缺组词顶屏原始拼音，与回车等效。
    if (vk == kVkSpace) {
        if (hasCandidates()) {
            int sel = candidateWnd_.selectedIndex();
            if (sel < 0 || sel >= static_cast<int>(comp_.candidates.size())) sel = 0;
            const chaoset::Candidate pick = comp_.candidates[sel];
            chaoset::win::EngineOp op;
            op.kind = chaoset::win::EngineOpKind::kSelectPrefix;
            op.index = sel;
            op.text = pick.text;
            op.syllables = pick.syllables;
            submitEngineOp(op);
            return true;
        }
        if (!pendingOps_.empty()) {
            // 结果尚未应用（首个按键的乐观窗口）：此时没有候选可展示，但用户
            // 按空格的意图是「选首选 / 结束组词」。绝不能把拼音当原始串顶屏，
            // 按页内第 0 项入队，由引擎对完整输入解析（失败则保持组词并重建）。
            chaoset::win::EngineOp op;
            op.kind = chaoset::win::EngineOpKind::kSelectPrefix;
            op.index = 0;
            submitEngineOp(op);
            return true;
        }
        std::string base, text;
        computeOptimistic(nullptr, &base, &text);
        std::string raw = confirmedPreedit_ + (base + text);
        confirmedPreedit_.clear();
        resetComposition();
        commit(pic, raw);
        return true;
    }

    // 不可处理的按键（F 键 / 数字 0 / Shift+数字的 @# 等字符）：组词中先收尾再放行。
    if (isComposing() || hasDpPending() || !confirmedPreedit_.empty()) {
        flushOrCancel(pic);
    }
    return false;
}
