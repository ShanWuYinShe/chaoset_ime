import AppKit
import InputMethodKit

// KeyRouter.swift：按键事件路由与修饰键语义（IMK handle 主流程）。
//
// 从 ChaosetController.swift 拆出（2026-11-06 架构拆分，纯移动、行为零改动）：
// Shift 空按切中英、Esc/退格/Ctrl+U/Ctrl+Backspace、Cmd/Ctrl/Option 放行、
// 联想键位、空格选词/全半角、数字选词、方向键页内预选、翻页、双拼 ';'、
// 标点与字母组词等判定全部在此；控制器状态与候选/联想呈现见
// ChaosetController.swift。

extension ChaosetController {

    /// 左/右 Shift 的 keyCode（56/60）。系统可能以 flagsChanged 或 keyDown/keyUp
    /// 投递，两种都按"按下→装填、松开→判定"处理，不依赖具体投递方式。
    private static let shiftKeyCodes: Set<UInt16> = [56, 60]

    // MARK: - 按键路由

    override func handle(_ event: NSEvent!, client sender: Any!) -> Bool {
        let client = sender as? any IMKTextInput

        // 只处理 keyDown 与 flagsChanged；keyUp 一律放行，
        // 否则同一个按键会被处理两次（重复组词／重复上屏）。
        if event.type != .keyDown && event.type != .flagsChanged {
            return false
        }

        let mods = event.modifierFlags.intersection(.deviceIndependentFlagsMask)

        // Shift 空按：按下装填、松开且期间无其它按键时才切换中英
        if Self.shiftKeyCodes.contains(event.keyCode) {
            if mods.contains(.shift) {
                shiftArmed = true
                return false // 放行 Shift 按下事件，确保修饰键状态完整同步给宿主应用（如 Shift+点击多选、范围选择、快捷键）
            }
            if shiftArmed {
                shiftArmed = false
                enMode.toggle()
                // 切到英文模式时收起联想窗（英文模式不展示中文联想）
                if enMode {
                    exitAssociate()
                }
                if current?.input.isEmpty == false {
                    current = engine.resetComposition()
                } else {
                    current = nil
                }
                // 已确认段随模式切换落盘：用户确认过的字不能丢（旧实现里这些
                // 段早已写入文档，此处保持净效果一致）
                flushConfirmed(client)
                showToast(enMode ? "英文模式" : "中文模式", client: client)
                refresh(client)
            }
            return false // 放行 Shift 释放事件，绝不吞掉宿主应用的按键状态机
        }
        shiftArmed = false // 出现其它按键 → 不是"空按"，取消待判定

        // 英文模式：全部放行（passThrough 会先清掉未上屏的组词），
        // 同时收起联想窗（中英切换时不得残留）
        if enMode {
            exitAssociate()
            return passThrough(client)
        }

        let isComposing = !(current?.input.isEmpty ?? true)

        // Esc 键（keyCode 53）：联想模式先关闭联想窗；组词中直接全部删除
        // （清空当前组词并关闭候选窗）
        if event.keyCode == 53 {
            if associateCandidates != nil {
                exitAssociate()
                return true
            }
            if isComposing || !confirmedText.isEmpty {
                // 取消整条组词，**含未落盘的已确认段**——与系统拼音的 Esc 语义
                // 一致：合成内已转换但未上屏的内容一并丢弃（2026-10-30 起
                // 已确认段住在预编辑里，必须显式丢弃而不是留在文档里）
                confirmedText = ""
                current = engine.resetComposition()
                refresh(client)
                return true
            }
            return false
        }

        // 退格键判定（Backspace/Delete: keyCode 51, ForwardDelete: keyCode 117, chars: \u{7F} / \u{08}）
        let rawChars = event.charactersIgnoringModifiers ?? ""
        let isDeleteChar = rawChars == "\u{7F}" || rawChars == "\u{08}"
        let isDeleteKey = event.keyCode == 51 || event.keyCode == 117 || isDeleteChar

        if isDeleteKey {
            if !isComposing {
                // 联想态：与 Windows/Linux 对齐——Backspace 收起联想并消费
                //（透传会误删刚上屏的文字）；ForwardDelete 收起联想后透传。
                if associateCandidates != nil {
                    exitAssociate()
                    let isBackspace = event.keyCode == 51 ||
                        rawChars == "\u{7F}" || rawChars == "\u{08}"
                    return isBackspace
                }
                current = nil
                refresh(client)
                return false // 无组词：放行给宿主应用正常删除已有文本
            }

            // 组词中按退格，独立分为全部删除与部分删除两种行为：
            // A. 直接全部删除（清空全部拼音）：Cmd+Delete / Ctrl+Delete / Shift+Delete
            if mods.contains(.command) || mods.contains(.control) || mods.contains(.shift) {
                confirmedText = "" // 全部删除：未落盘的已确认段一并取消（与 Esc 一致）
                current = engine.resetComposition()
                refresh(client)
                return true // 必须消费，绝不让快捷删除穿透到宿主应用误删文档内容
            }

            // B. 按音节部分删除（整词/音节回退）：Option+Delete (⌥⌫)
            if mods.contains(.option) {
                current = engine.backspaceWord()
                if current?.input.isEmpty == true {
                    current = nil
                    flushConfirmed(client) // 同普通退格：剩余删空即落盘收尾
                }
                refresh(client)
                return true
            }

            // C. 慢慢部分删除（逐字符退格）：普通退格键单按，每按一次退一个拼音字母
            current = engine.backspace()
            if current?.input.isEmpty == true {
                current = nil
                // 剩余拼音删空：已确认段立即落盘收尾——它们不能继续挂在
                // 预编辑里变成「无组词的孤标记区」
                flushConfirmed(client)
            }
            refresh(client)
            return true
        }

        // Ctrl+U：类终端整行清空，组词中直接全部删除
        if isComposing, mods.contains(.control), (rawChars == "u" || rawChars == "\u{15}") {
            confirmedText = "" // 同「全部删除」：未落盘的已确认段一并取消
            current = engine.resetComposition()
            refresh(client)
            return true
        }

        // Ctrl+Backspace：类终端「删到行首」，组词中直接全部删除。
        // Windows/Linux 壳都有这一项（清空全部拼音并消费按键），macOS 此前
        // 缺失——同一个快捷键在三端行为不同（2026-10-29 三端一致性审查）。
        if isComposing, mods.contains(.control),
           event.keyCode == 51 || event.keyCode == 117 {
            confirmedText = "" // 同「全部删除」：未落盘的已确认段一并取消
            current = engine.resetComposition()
            refresh(client)
            return true
        }

        // 带 Command/Control/Option 的系统快捷键（如 Cmd+C/Cmd+V）一律放行。
        // 联想态先收起联想再透传（与 Windows/Linux 壳对齐；此前 mac 面板会
        // 残留到下一次输入，2026-10-31 三端一致性收尾）。
        if mods.contains(.command) || mods.contains(.control) || mods.contains(.option) {
            if associateCandidates != nil { exitAssociate() }
            return passThrough(client)
        }

        let hasCandidates = !(current?.candidates.isEmpty ?? true)

        // 方向键预选（issue#2）：组词有候选时移动页内预选（上/左=前一项、
        // 下/右=后一项；页首/页尾跨页选末项/首项，无对应页保持不动）。
        // 无组词/无候选时**不消费**，落到下方 passThrough 放行给宿主。
        if isComposing, hasCandidates, let delta = Self.arrowDelta(event.keyCode) {
            moveCompositionSelection(delta: delta, client: client)
            return true
        }

        // 联想模式：数字键选联想词直接上屏（并继续联想）；空格/回车结束
        // 联想放行；字母键退出联想进入正常组词（落到下方常规流程）。
        if associateCandidates != nil {
            // 联想分支在 guard chars 之前：部分客户端空格的 characters 失灵
            // （同空格选词修复的口径），数字/字母改用 event 自带的键值判定。
            let rawChars = event.charactersIgnoringModifiers ?? ""
            let firstScalar = rawChars.unicodeScalars.first?.value ?? 0
            // 方向键：移动联想页内预选（页首/页尾跨页）。联想面板可见时
            // 方向键属于输入法，不再「退出联想并透传」。
            if let delta = Self.arrowDelta(event.keyCode) {
                moveAssociateSelection(delta: delta, client: client)
                return true
            }
            if event.keyCode == 49 || event.keyCode == 36 {
                // 空格：**默认选当前页第一个联想词上屏**（市面输入法惯例，
                // 渐进组句闭环——上屏后继续弹续接联想；2026-10-28 用户反馈
                // 联想时空格不能默认选第一个很不方便）。回车：结束联想（消费）。
                if event.keyCode == 49, let list = associateCandidates, !list.isEmpty {
                    // 空格确认**联想页内预选项**（默认第 0 项；方向键可移动预选）。
                    let start = associatePage * Self.associatePageSize
                    let count = min(Self.associatePageSize, list.count - start)
                    let sel = min(max(CandidatePanelController.selectedIndex, 0), max(count - 1, 0))
                    let pick = start + sel
                    if pick < list.count {
                        if let committed = engine.associateSelect(word: list[pick].text, sylls: list[pick].syllables.split(separator: "'").map(String.init)) {
                            client?.insertText(
                                committed,
                                replacementRange: NSRange(location: NSNotFound, length: 0)
                            )
                            enterAssociate(sylls: list[pick].syllables.split(separator: "'").map(String.init), client: client)
                            return true
                        }
                    }
                }
                exitAssociate()
                if event.keyCode == 36 {
                    return true // 回车消费
                }
                // 空格一律消费：联想态下空格是「选第一个联想词」的意图，
                // 上屏失败（daemon 不可达/越界）时也不能把裸空格漏进文档。
                // Windows/Linux 壳在此场景都是 return true（2026-10-29
                // 三端一致性审查：此前 macOS 走 passThrough，文档多个空格）。
                return true
            } else if firstScalar >= 49, firstScalar <= 57,
                      let list = associateCandidates {
                // 数字范围必须是 1-9 而不是 1-6：联想每页 6 条，但 7/8/9 属于
                // 「越界序号」——必须进本分支后由下面的 exitAssociate() 收起
                // 联想窗再透传。写成 1-6 时 7/8/9 落不到任何分支、直接穿到
                // 下面：数字进了文档而联想窗挂在屏幕上不消失（Windows/Linux
                // 都按 1-9 判定，三端不一致，2026-10-29 审查发现）。
                // 数字选当前页内的联想词（联想已翻页化，见 associateFlip）
                let pick = associatePage * Self.associatePageSize + Int(firstScalar) - 49
                if pick < list.count {
                    if let committed = engine.associateSelect(word: list[pick].text, sylls: list[pick].syllables.split(separator: "'").map(String.init)) {
                        client?.insertText(
                            committed,
                            replacementRange: NSRange(location: NSNotFound, length: 0)
                        )
                        enterAssociate(sylls: list[pick].syllables.split(separator: "'").map(String.init), client: client)
                        return true
                    }
                }
                // 越界序号或上屏失败：收起联想，数字落到正常流程进文档。
                // 此前越界序号不匹配任何分支，落到 passThrough 只清组词不清
                // 联想窗——数字进了文档而联想窗还挂在屏幕上（对齐 Win/Linux
                // 壳的 clearAssociate 语义，2026-10-27 审查发现）。
                exitAssociate()
            } else if firstScalar >= 97, firstScalar <= 122 {
                // 字母键：退出联想，进入正常组词
                exitAssociate()
            } else if firstScalar == 45 || firstScalar == 61 ||
                      event.keyCode == 116 || event.keyCode == 121 ||
                      firstScalar == 91 || firstScalar == 93 {
                // 联想翻页键：- / =、[ / ]、PageUp / PageDown——与组词态
                // 翻页键集合完全一致（2026-10-29 三端一致性审查）。
                // 此前只认 - / =，其余键落不到任何分支：associateCandidates
                // 不清理就穿到下面的 passThrough，文档滚动了而联想窗还挂在
                // 屏幕上（三端行为各不相同，Linux 改为翻页、Windows 收起）。
                let next = firstScalar == 61 || firstScalar == 93 ||
                    event.keyCode == 121
                associateFlip(next: next, client: client)
                return true
            } else {
                // 其余按键（Tab/F 键等）：与 Windows/Linux 对齐，先收起联想再
                // 落到下方正常流程/透传（此前面板会残留到下一次输入）。
                exitAssociate()
            }
        }

        // 空格（keyCode 49）：组词中一律消费——首选上屏；无候选（残缺组词）
        // 时顶屏原始拼音（与回车等效）。放在 guard chars **之前**：部分客户端
        // 快速输入时 charactersIgnoringModifiers 对空格判定失灵（返回空/nil），
        // 会在前置守卫处透传——选词落空、空格漏进文档（2026-10-25 实机反馈）。
        // 组词中用户按空格的意图永远是「结束组词」，不应有裸空格进文档。
        // Shift+空格：切换全角/半角标点模式（优先于首选上屏）。开关归壳本地
        // 持有（2026-10-29 起），切换不再与 daemon 往返，状态随 punct 请求下发。
        // 非组词状态的空格照旧放行。切换后短暂提示当前状态（三端统一反馈）。
        // 2026-10-29 修复（输入事件审计 F1）：Shift+空格**只在输入法真正介入时**
        // 才消费。此前无状态前提地吃掉任何应用里的 Shift+Space：游戏里
        // Shift=冲刺/潜行、Space=跳，「按住 Shift 跳不起来」；全屏应用同理。
        // 输入法没有任何理由拦截非组词状态的组合键，此时一律放行给宿主。
        //
        // 注意（2026-10-30 三端一致性整改）：联想态实际走不到本分支——上方
        // 联想分支已把空格（含 Shift+空格）按「选当前页第一个联想词上屏」
        // 消费，**不**切换全/半角。docs/ui-parity.md 已按实测行为修正；本分支
        // 保留 associating 判定仅作防御，行为不变。
        if event.keyCode == 49, event.modifierFlags.contains(.shift),
           event.modifierFlags.contains(.command) == false,
           event.modifierFlags.contains(.control) == false,
           event.modifierFlags.contains(.option) == false {
            let composing = !(current?.input.isEmpty ?? true)
            let associating = !(associateCandidates?.isEmpty ?? true)
            if composing || associating {
                halfWidth.toggle()
                showToast(halfWidth ? "半角标点" : "全角标点", client: client)
                return true
            }
            return passThrough(client)
        }
        if event.keyCode == 49, let cur = current, !cur.input.isEmpty {
            if hasCandidates {
                // 空格确认**页内预选项**（默认第 0 项；方向键可移动预选）。
                let sel = min(max(CandidatePanelController.selectedIndex, 0),
                              max(cur.candidates.count - 1, 0))
                commit(index: sel, client: client)
            } else {
                let raw = engine.commitRaw() ?? cur.input
                current = nil
                client?.insertText(
                    confirmedText + raw,
                    replacementRange: NSRange(location: NSNotFound, length: 0)
                )
                confirmedText = ""
                refresh(client)
            }
            return true
        }

        guard let chars = event.charactersIgnoringModifiers, !chars.isEmpty else {
            return passThrough(client)
        }

        // 数字 1-N 选对应候选（N = 本页实际候选数）。
        //
        // 必须按**实际候选数**判定而不是写死 1-9：页面容量从 9 改成 6 后，
        // 组词中按 7/8/9 会走到 commit() 里因越界直接 return，而这里无条件
        // return true → 键被吞掉，既不选词也不进文档（2026-10-26 审查发现）。
        // 越界时落到后面的 passThrough/标点分支，让数字正常进入文档。
        if hasCandidates, chars.count == 1,
           let scalar = chars.unicodeScalars.first,
           scalar.value >= 49, scalar.value <= 57 {
            let idx = Int(scalar.value - 49)
            if idx < (current?.candidates.count ?? 0) {
                commit(index: idx, client: client)
                return true
            }
            // 超出本页候选数：不吞键，继续走后续分支（数字按原样处理）
        }

        // 回车：组词中不论有无候选，按回车直接将输入的原始拼音上屏，清空组词并消费回车键
        if (chars == "\r" || chars == "\n"), let cur = current, !cur.input.isEmpty {
            let raw = engine.commitRaw() ?? cur.input
            current = nil
            // 回车语义：已确认段按确认文本上屏，剩余拼音按原始串上屏
            //（与系统拼音回车一致：已转换部分保留转换结果）
            client?.insertText(
                confirmedText + raw,
                replacementRange: NSRange(location: NSNotFound, length: 0)
            )
            confirmedText = ""
            refresh(client)
            return true
        }

        // - / =、[ / ]、PageUp / PageDown：候选翻页（组词中且有候选时消费；空组词放行原键/出标点）
        let isPageUp = chars == "-" || chars == "[" || event.keyCode == 116 || chars == "\u{F72C}"
        let isPageDown = chars == "=" || chars == "]" || event.keyCode == 121 || chars == "\u{F72D}"
        if (isPageUp || isPageDown) && hasCandidates {
            current = isPageUp ? engine.prevPage() : engine.nextPage()
            refresh(client)
            return true
        }

        // 双拼模式下 ';' 是引擎的组词/解码键（mspy 的 ing 韵母键，如 m;=ming），
        // 不能按标点走 punct——否则明/听/星/京整类音节打不出，还会误上屏「；」。
        // 引擎契约：input(";") 在 dpPending!=0 时参与两两解码，为 0 时按 punct
        // 语义把「组词中首选词 + 分号」放进 Committed。Shift+';' 产生 ':'，
        // 不在此列（仍走下方标点分支，与 Windows/Linux 字符级判定一致）。
        if !engine.doublePinyinScheme.isEmpty, chars == ";" {
            guard let comp = engine.input(chars, halfWidth: halfWidth) else {
                return passThrough(client) // daemon 不可达：绝不吞键
            }
            current = comp
            if !comp.committedText.isEmpty {
                // 已确认段 + 本次上屏文本一次落盘（与 selectPrefix 收尾同原语）
                client?.insertText(
                    confirmedText + comp.committedText,
                    replacementRange: NSRange(location: NSNotFound, length: 0)
                )
                confirmedText = ""
                current = nil
            }
            refresh(client)
            return true
        }

        // 标点键：组词中先上屏首选再出中文标点，空组词直接出中文标点。
        // 组词中的撇号仍是音节分隔符，交回组词流程。
        if chars.count == 1, let scalar = chars.unicodeScalars.first,
           Self.isPunctKey(scalar) {
            if associateCandidates != nil {
                exitAssociate()
            }
            if scalar == "'", current?.input.isEmpty == false {
                current = engine.input(chars, halfWidth: halfWidth)
                refresh(client)
                return true
            }
            guard let committed = engine.punct(chars, halfWidth: halfWidth) else {
                return passThrough(client) // daemon 不可达/未知标点：原键放行
            }
            current = nil
            // 标点收尾：已确认段 + 首选候选 + 标点一次落盘
            client?.insertText(
                confirmedText + committed,
                replacementRange: NSRange(location: NSNotFound, length: 0)
            )
            confirmedText = ""
            refresh(client)
            return true
        }

        // 字母与撇号：进组词流程。
        //
        // engine.input 返回 nil 只有一种可能：daemon 不可达（IPC 失败）。
        // 此时**绝不能消费按键**——2026-10-26 用户实测「切换到本输入法后
        // 首字母丢失」的根因就在这里：旧代码把 current 置空后 return true，
        // 按键被吞掉，而宿主应用什么都没收到，字母就凭空消失了。
        // 输入法的铁律是「任何情况下都不能丢键」：daemon 出问题时应当原样
        // 放行这个字母，让用户至少还能用英文继续输入，而不是静默吞掉。
        // （EngineClient 内部有 1 秒失败冷却，daemon 恢复后下一次按键会自动重连。）
        if isCompositionChar(chars) {
            guard let comp = engine.input(chars, halfWidth: halfWidth) else {
                return passThrough(client)
            }
            current = comp
            refresh(client)
            return true
        }

        return passThrough(client)
    }

    // MARK: - 方向键页内预选（issue#2）

    /// 方向键 keyCode → 页内预选位移：左/上 = -1（前一项），右/下 = +1。
    private static func arrowDelta(_ keyCode: UInt16) -> Int? {
        switch keyCode {
        case 123, 126: return -1  // ← / ↑
        case 124, 125: return 1   // → / ↓
        default: return nil
        }
    }

    /// 组词态页内预选移动：先页内移动；页首/页尾跨页选末项/首项；没有对应
    /// 页时保持不动（仍消费按键）。
    private func moveCompositionSelection(delta: Int, client: (any IMKTextInput)?) {
        guard let cur = current, !cur.candidates.isEmpty else { return }
        if CandidatePanelController.moveSelection(delta) { return }
        if delta < 0 {
            guard cur.page > 0 else { return }  // 首页页首：保持不动
            current = engine.prevPage()
            refresh(client)
            CandidatePanelController.setSelectedIndex((current?.candidates.count ?? 1) - 1)
            return
        }
        guard cur.page + 1 < cur.pageCount else { return }  // 末页页尾：保持不动
        current = engine.nextPage()
        refresh(client)
        CandidatePanelController.setSelectedIndex(0)
    }

    /// 联想态页内预选移动（联想是壳本地分页：associatePage 起、每页 6 条）。
    private func moveAssociateSelection(delta: Int, client: (any IMKTextInput)?) {
        guard let list = associateCandidates, !list.isEmpty else { return }
        if CandidatePanelController.moveSelection(delta) { return }
        let pageCount = (list.count + Self.associatePageSize - 1) / Self.associatePageSize
        if delta < 0 {
            guard associatePage > 0 else { return }  // 首页页首：保持不动
            associateFlip(next: false, client: client)
            let start = associatePage * Self.associatePageSize
            CandidatePanelController.setSelectedIndex(min(Self.associatePageSize, list.count - start) - 1)
            return
        }
        guard associatePage + 1 < pageCount else { return }  // 末页页尾：保持不动
        associateFlip(next: true, client: client)
        CandidatePanelController.setSelectedIndex(0)
    }

    // 不可处理的按键：组词中先清空再放行，避免预编辑与实文脱节。
    // 已确认段先落盘——用户确认过的字不能随透传消失（旧实现里这些段
    // 早已写入文档，此处保持净效果一致）。
    private func passThrough(_ client: (any IMKTextInput)?) -> Bool {
        if current?.input.isEmpty == false || !confirmedText.isEmpty {
            current = engine.resetComposition()
            flushConfirmed(client)
            refresh(client)
        }
        return false
    }

    private func isCompositionChar(_ chars: String) -> Bool {
        for scalar in chars.unicodeScalars {
            let isLower = scalar.value >= 97 && scalar.value <= 122 // a-z
            if !isLower && scalar != "'" { return false }
        }
        return true
    }

    // 与 engine.IsPunctKey 一致的标点键集合（引号映射在引擎内开闭配对）
    private static let punctScalars: Set<UInt32> = [
        0x2C, 0x2E, 0x3B, 0x3A, 0x3F, 0x21, 0x28, 0x29, 0x5B, 0x5D,
        0x3C, 0x3E, 0x5C, 0x7E, 0x5E, 0x22, 0x27,
    ]

    private static func isPunctKey(_ scalar: Unicode.Scalar) -> Bool {
        scalar.isASCII && punctScalars.contains(scalar.value)
    }
}
