import AppKit
import InputMethodKit

// ChaosetController 是 IMK 输入法控制器：把系统按键事件翻译成
// 对 chaosetd 的 IPC 调用，并用内联预编辑 + 壳内原生候选窗（AppKit 自绘，
// 见 CandidatePanel.swift）展示；daemon 不可达时回退 IMKCandidates。
// daemon 未运行时所有按键原样透传，不影响宿主应用输入。
// @objc 显式注册 ObjC 类名：IMK 经 Info.plist 的
// InputMethodServerControllerClass 用 NSClassFromString 查找控制器，
// Swift 类不注册 ObjC 名会查不到。
@objc(ChaosetController)
final class ChaosetController: IMKInputController {
    // main.swift 装配 IMKServer 后挂到这里，候选窗构造需要它
    static weak var sharedServer: IMKServer?

    private let engine = EngineClient()
    private var current: Comp?
    // 渐进上屏的「已确认段」缓冲（2026-10-30 兼容性整改）。
    //
    // SelectPrefix 返回的已确认文本**不再立即 insertText 写入文档**，而是并入
    // 预编辑（marked text）一起展示，直到整条组词结束时一次 insertText 落盘。
    //
    // 为什么（用户实测：长串组词中选 1（今），部分 App 里「今」丢失、候选清空、
    // 剩余拼音卡死在文档里）：旧实现对宿主做「insertText 已确认段 + setMarkedText
    // 剩余拼音」两步文档变更。正确实现 IMK 语义的 App 会用 insertText 替换整个
    // marked 区，但不少 App（部分版本 Notes、Electron、终端等）对「marked 区
    // 激活期间收到 insertText」处理有缺陷：丢弃插入、或按过期的 marked 范围回写
    // 随后的 setMarkedText——结果正是「已选的今丢失 + 剩余拼音变成普通文本 +
    // 组词中断」。逐键输入之所以在所有 App 都正常，是因为它只做一步 setMarkedText。
    // 并入后，渐进上屏与逐键输入使用完全相同的原语（单步 setMarkedText），
    // 兼容面与逐键输入一致；落盘只保留「整条组词结束」一个时机——与 macOS
    // 系统拼音「合成内的已转换段」模型相同（选择候选后，已转换的字仍带下划线
    // 留在合成里，最后一起上屏）。
    private var confirmedText = ""
    // 联想模式：选词上屏后候选窗继续展示续接词组（非 nil 即联想中）；
    // 用户开始输入新字母或结束会话时清空，回到正常拼音匹配。
    private var associateCandidates: [Candidate]?
    private var candidateWindow: IMKCandidates?

    // 中英模式：英文模式下按键全部放行（组词/选词/标点映射不介入）。
    // 切换手势为 Shift **空按**：按下先装填，松开时若期间没有其它按键才切换。
    // 这样即便系统把 Shift 的按下与松开都路由进来也只切一次，Shift+A 之类的
    // 组合也不会误切换（旧实现按下/松开各切一次，净效果为不切换）。
    private var enMode = false
    /// 全/半角标点开关：壳本地持有（2026-10-29 起），随 punct 请求下发
    private var halfWidth = false
    private var shiftArmed = false

    /// 左/右 Shift 的 keyCode（56/60）。系统可能以 flagsChanged 或 keyDown/keyUp
    /// 投递，两种都按"按下→装填、松开→判定"处理，不依赖具体投递方式。
    private static let shiftKeyCodes: Set<UInt16> = [56, 60]

    // 懒创建候选窗；IMKServer 未就绪时返回 nil，后续按键会再试。
    private func ensureCandidateWindow() -> IMKCandidates? {
        if candidateWindow == nil, let server = ChaosetController.sharedServer {
            candidateWindow = IMKCandidates(
                server: server,
                panelType: kIMKSingleColumnScrollingCandidatePanel,
                styleType: kIMKMain
            )
        }
        return candidateWindow
    }

    // MARK: - 按键处理

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
                    current = engine.reset()
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
                current = engine.reset()
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
                current = nil
                refresh(client)
                return false // 无组词：放行给宿主应用正常删除已有文本
            }

            // 组词中按退格，独立分为全部删除与部分删除两种行为：
            // A. 直接全部删除（清空全部拼音）：Cmd+Delete / Ctrl+Delete / Shift+Delete
            if mods.contains(.command) || mods.contains(.control) || mods.contains(.shift) {
                confirmedText = "" // 全部删除：未落盘的已确认段一并取消（与 Esc 一致）
                current = engine.reset()
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
            current = engine.reset()
            refresh(client)
            return true
        }

        // Ctrl+Backspace：类终端「删到行首」，组词中直接全部删除。
        // Windows/Linux 壳都有这一项（清空全部拼音并消费按键），macOS 此前
        // 缺失——同一个快捷键在三端行为不同（2026-10-29 三端一致性审查）。
        if isComposing, mods.contains(.control),
           event.keyCode == 51 || event.keyCode == 117 {
            confirmedText = "" // 同「全部删除」：未落盘的已确认段一并取消
            current = engine.reset()
            refresh(client)
            return true
        }

        // 带 Command/Control/Option 的系统快捷键（如 Cmd+C/Cmd+V）一律放行
        if mods.contains(.command) || mods.contains(.control) || mods.contains(.option) {
            return passThrough(client)
        }

        let hasCandidates = !(current?.candidates.isEmpty ?? true)

        // 联想模式：数字键选联想词直接上屏（并继续联想）；空格/回车结束
        // 联想放行；字母键退出联想进入正常组词（落到下方常规流程）。
        if associateCandidates != nil {
            // 联想分支在 guard chars 之前：部分客户端空格的 characters 失灵
            // （同空格选词修复的口径），数字/字母改用 event 自带的键值判定。
            let rawChars = event.charactersIgnoringModifiers ?? ""
            let firstScalar = rawChars.unicodeScalars.first?.value ?? 0
            if event.keyCode == 49 || event.keyCode == 36 {
                // 空格：**默认选当前页第一个联想词上屏**（市面输入法惯例，
                // 渐进组句闭环——上屏后继续弹续接联想；2026-10-28 用户反馈
                // 联想时空格不能默认选第一个很不方便）。回车：结束联想（消费）。
                if event.keyCode == 49, let list = associateCandidates, !list.isEmpty {
                    let pick = associatePage * Self.associatePageSize
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
                return passThrough(client)
            } else if event.keyCode == 51 {
                // 删除键：收起联想并**消费**——透传会误删刚上屏的文字
                //（联想模式下用户连按删除的本意是回退编辑，误删文档字
                // 代价高；2026-10-28 用户反馈）
                exitAssociate()
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
                commit(index: 0, client: client)
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
            guard let comp = engine.input(chars) else {
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
                current = engine.input(chars)
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
            guard let comp = engine.input(chars) else {
                return passThrough(client)
            }
            current = comp
            refresh(client)
            return true
        }

        return passThrough(client)
    }

    // 不可处理的按键：组词中先清空再放行，避免预编辑与实文脱节。
    // 已确认段先落盘——用户确认过的字不能随透传消失（旧实现里这些段
    // 早已写入文档，此处保持净效果一致）。
    private func passThrough(_ client: (any IMKTextInput)?) -> Bool {
        if current?.input.isEmpty == false || !confirmedText.isEmpty {
            current = engine.reset()
            flushConfirmed(client)
            refresh(client)
        }
        return false
    }

    /// 把已确认段一次性落盘并清空缓冲。调用时机 = 所有「不再继续组词」的
    /// 路径：整条组词选完、退格删空剩余拼音、失焦、透传、模式切换。
    /// 落盘必须用 insertText（替换整个 marked 区）——这是与整句上屏相同的、
    /// 已在全部 App 验证兼容的操作；绝不 marked 区激活期间分步写入文档。
    private func flushConfirmed(_ client: (any IMKTextInput)?) {
        guard !confirmedText.isEmpty else { return }
        let text = confirmedText
        confirmedText = ""
        client?.insertText(text, replacementRange: NSRange(location: NSNotFound, length: 0))
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

    private func commit(index: Int, client: (any IMKTextInput)?) {
        // 越界直接返回（调用方应已按候选数判定；这里是最后一道防线）。
        guard let cur = current, index >= 0, index < cur.candidates.count else { return }
        // 渐进上屏（2026-10-29）：SelectPrefix 只消耗候选音节对应的拼音
        // 前缀——打 jintian'wo'chi… 选「今天」后剩余 wo'chi… 继续组词
        // （2026-10-28 用户反馈「选一个候选就清空整个拼音」不合适，
        // 渐进拼句才是吸纳用户习惯的主途径）。
        if let comp = engine.selectPrefix(index: index), !comp.committedText.isEmpty {
            let assocSylls = cur.candidates[index].syllables
                .split(separator: "'").map(String.init)
            // 已确认段并入预编辑，**不立即写文档**（见 confirmedText 注释）：
            //   还剩拼音 → 与逐键输入同一原语，单步 setMarkedText 刷新；
            //   整串耗尽 → 整条组词一次 insertText 落盘（与整句上屏同一收尾，
            //   同为「替换 marked 区」这一全 App 兼容操作）。
            confirmedText += comp.committedText
            if comp.input.isEmpty {
                current = nil
                client?.insertText(
                    confirmedText,
                    replacementRange: NSRange(location: NSNotFound, length: 0)
                )
                confirmedText = ""
                refresh(client)
                // 剩余拼音耗尽 = 上屏收尾，与整句上屏同一收尾语义：进入联想。
                // 2026-10-29 三端一致性审查：Windows/Linux 壳均在剩余耗尽后
                // 进入联想。还剩拼音时继续组词，不进入联想。
                enterAssociate(sylls: assocSylls, client: client)
            } else {
                current = Comp(input: comp.input, preedit: comp.preedit,
                               candidates: comp.candidates,
                               page: comp.page, pageCount: comp.pageCount)
                refresh(client)
            }
            return
        }
        // daemon 不可达（selectPrefix 返回 nil）：绝不能静默失败（键被消费、
        // 文字没进文档 = 丢字）。退化为整句上屏，保证用户输入不丢失。
        // 已确认段随本次落盘一起收尾（缓冲必须清空，避免串进下一轮组词）。
        let committed = confirmedText + (engine.select(index) ?? cur.input)
        confirmedText = ""
        current = nil
        client?.insertText(
            committed,
            replacementRange: NSRange(location: NSNotFound, length: 0)
        )
        refresh(client)
        // 联想：选词上屏后候选窗继续展示续接词组（用户可直接数字键连选）
        enterAssociate(sylls: cur.candidates[index].syllables.split(separator: "'").map(String.init), client: client)
    }

    // MARK: - 联想模式

    /// 联想每页条数（与引擎 pageSize 一致，数字键 1-6 指位）。
    private static let associatePageSize = 6
    /// 联想当前页（0 起）。enterAssociate 归零，翻页键/面板点击切换。
    private var associatePage = 0

    /// 进入联想模式：按上屏词音节查续接词组并展示（无候选则不进入）。
    private func enterAssociate(sylls: [String], client: (any IMKTextInput)?) {
        guard AssociateSetting.enabled else { return }
        guard let comp = engine.associate(sylls: sylls), !comp.candidates.isEmpty else { return }
        associateCandidates = comp.candidates
        associatePage = 0
        refresh(client)
    }

    private func exitAssociate() {
        associateCandidates = nil
        associatePage = 0
        Self.hideCandidatePanel()
    }

    /// 联想翻页（- / = 键或面板折叠区）。边界页保持当前页（与组词翻页的
    /// 钳制语义一致）。
    private func associateFlip(next: Bool, client: (any IMKTextInput)?) {
        guard let list = associateCandidates else { return }
        let pageCount = (list.count + Self.associatePageSize - 1) / Self.associatePageSize
        if next, associatePage + 1 < pageCount {
            associatePage += 1
        } else if !next, associatePage > 0 {
            associatePage -= 1
        }
        refresh(client)
    }

    /// 展示联想候选（数据源为续接词组而非组词状态，本地分页）。
    private func showAssociatePanel(client: (any IMKTextInput)?, candidates: [Candidate]) -> Bool {
        let pageCount = max((candidates.count + Self.associatePageSize - 1) / Self.associatePageSize, 1)
        if associatePage >= pageCount { associatePage = pageCount - 1 }
        let start = associatePage * Self.associatePageSize
        let slice = Array(candidates[start..<min(start + Self.associatePageSize, candidates.count)])
        let items = slice.map { CandidatePanelView.Item(text: $0.text, fromUser: false) }
        let screen = Self.screenForCursor(client) ?? NSScreen.main
        CandidatePanelController.onSelect = { [weak self] index in
            // index 是**当前页切片**内的序号（视图按页渲染），查全局列表
            // 会错选其它页的同位词
            guard let self, index < slice.count, let client = self.client() else { return }
            let pick = slice[index]
            if let committed = self.engine.associateSelect(word: pick.text, sylls: pick.syllables.split(separator: "'").map(String.init)) {
                client.insertText(
                    committed,
                    replacementRange: NSRange(location: NSNotFound, length: 0)
                )
                self.enterAssociate(sylls: pick.syllables.split(separator: "'").map(String.init), client: client)
            }
        }
        // 折叠区点击翻页（与翻页键同一逻辑）
        CandidatePanelController.onPageDown = { [weak self] in
            self?.associateFlip(next: true, client: self?.client())
        }
        CandidatePanelController.onPageUp = { [weak self] in
            self?.associateFlip(next: false, client: self?.client())
        }
        CandidatePanelController.onSettings = { [weak self] in
            self?.showSettingsMenu()
        }
        return CandidatePanelController.show(
            candidates: items,
            page: associatePage,
            pageCount: pageCount,
            originProvider: { size in
                Self.candidateOrigin(size: size, client: client, screen: screen)
            }
        )
    }

    // MARK: - 设置（候选栏齿轮入口，微信输入法交互参考）

    /// 齿轮点击 → 打开设置窗口（独立窗口含全部设置项；参考市面输入法，
    /// 入口不止一个勾选框而是完整设置面板）。
    private func showSettingsMenu() {
        SettingsWindowController.show()
    }

    // MARK: - 展示

    /// 在光标附近短暂显示模式提示（中英/全半角切换反馈，三端统一）；
    /// 光标矩形取不到时退回鼠标位置。
    private func showToast(_ text: String, client: (any IMKTextInput)?) {
        let size = ModeToastView.size(for: text)
        let origin = Self.candidateOrigin(size: size, client: client, screen: Self.screenForCursor(client))
        ModeToastController.show(text: text, topLeft: origin)
    }

    private func refresh(_ client: (any IMKTextInput)?) {
        // 组词开始即退出联想（新输入优先于联想展示）
        if current?.input.isEmpty == false {
            associateCandidates = nil
        }
        // 联想模式：候选窗展示续接词组
        if let assoc = associateCandidates, !assoc.isEmpty {
            if showAssociatePanel(client: client, candidates: assoc) {
                return
            }
        }
        // 预编辑 = 已确认段（渐进上屏的已选字）+ 剩余拼音：二者同属一条
        // marked 区，整条组词结束才落盘。壳对文档只做两类操作——
        // setMarkedText 更新（与逐键输入同原语）与 insertText 终态落盘
        // （与整句上屏同原语），不存在「组词中写正文」这一易碎操作。
        let marked = confirmedText + (current?.preedit ?? "")
        if marked.isEmpty {
            // 组词结束：用 insertText("") 收掉 inline session。
            // 直接 setMarkedText("") 在多数客户端里等于「留一个空标记区」，
            // 之后的退格可能被继续路由到输入法或把相邻文本一起替换掉
            //（2026-10-22 用户反馈「退格把已输入的词和拼音一起删了」）。
            client?.insertText("", replacementRange: NSRange(location: NSNotFound, length: 0))
        } else {
            // selectionRange 用 (0,0) 而不是 (0,length)：整段全选会让部分客户端
            // 把后续输入当成「替换整个选区」，退格时连带删掉已上屏内容。
            client?.setMarkedText(
                marked,
                selectionRange: NSRange(location: (marked as NSString).length, length: 0),
                replacementRange: NSRange(location: NSNotFound, length: 0)
            )
        }
        let hasCandidates = !(current?.candidates.isEmpty ?? true)
        if !hasCandidates {
            exitAssociate()
            candidateWindow?.hide()
            return
        }
        // 原生候选窗优先（壳内 AppKit 自绘）；失败回退 IMKCandidates
        if showNativePanel(client: client) {
            candidateWindow?.hide()
            return
        }
        if let window = ensureCandidateWindow() {
            window.update()
        }
    }

    // ---- 原生候选窗面板（CandidatePanel.swift：壳内 AppKit 自绘） ----

    // 全进程共用一个候选窗面板：IMK 会为不同客户端/输入上下文创建多个控制器
    // 实例，若每个实例各持有一个 NSPanel，而会话结束时系统只发
    // commitComposition:（点击文本框外）而没有 deactivateServer:，旧面板就会
    // 永远留在屏幕上、点不掉也关不掉（2026-10-21 实测同时残留多个候选窗）。
    // 改为共用面板 + 在全部会话结束路径收起，保证任何时刻最多一个可见候选窗。
    // 面板单例与绘制在 CandidatePanel.swift，此处只做数据组装与定位。

    // 候选窗与光标行的间距（pt）：太贴会压住行下沿的下伸部，太远又像飘着。
    private static let candidateGap: CGFloat = 6

    private static func hideCandidatePanel() {
        CandidatePanelController.hide()
    }

    /// 显示壳内原生候选窗；返回 false 表示不可用（回退 IMKCandidates）。
    /// client 用于取插入点矩形，取不到时退回鼠标位置。
    private func showNativePanel(client: (any IMKTextInput)?) -> Bool {
        guard let cur = current, !cur.candidates.isEmpty else {
            exitAssociate()
            return false
        }
        let items = cur.candidates.map { CandidatePanelView.Item(text: $0.text, fromUser: $0.fromUser) }
        let screen = Self.screenForCursor(client) ?? NSScreen.main
        // 点击候选 → 选词上屏。weak self：IMK 每个输入上下文建/销 Controller，
        // 不得让静态面板的闭包延长已结束会话的实例生命周期。
        CandidatePanelController.onSelect = { [weak self] index in
            guard let self, let client = self.client() else { return }
            self.commit(index: index, client: client)
        }
        // 点击折叠区 → 翻下一页（与 = / ] 键等效；daemon 对边界页钳制）
        CandidatePanelController.onPageDown = { [weak self] in
            guard let self, let client = self.client() else { return }
            self.current = self.engine.nextPage()
            self.refresh(client)
        }
        // 末页折叠区箭头变 ⌃：点击翻上一页（与 - / [ 键等效）
        CandidatePanelController.onPageUp = { [weak self] in
            guard let self, let client = self.client() else { return }
            self.current = self.engine.prevPage()
            self.refresh(client)
        }
        // 候选栏齿轮 → 设置菜单（微信输入法交互参考）
        CandidatePanelController.onSettings = { [weak self] in
            self?.showSettingsMenu()
        }
        return CandidatePanelController.show(
            candidates: items,
            page: cur.page,
            pageCount: cur.pageCount,
            originProvider: { size in
                Self.candidateOrigin(size: size, client: client, screen: screen)
            }
        )
    }

    // MARK: - 候选窗定位（跟随光标、不遮挡正在输入的行）

    /// 取插入点矩形（屏幕坐标）。IMK 的 attributesForCharacterIndex:lineHeightRectangle:
    /// 返回"宽 1px、高为行高"的矩形；index=0 表示取当前选区（无内联会话时）。
    /// 取不到或返回异常值时返回 nil（调用方退回鼠标位置）。
    private static func caretRect(client: (any IMKTextInput)?) -> NSRect? {
        guard let client else { return nil }
        var rect = NSRect.zero
        _ = client.attributes(forCharacterIndex: 0, lineHeightRectangle: &rect)
        // 全零 / 非有限值 / 尺寸不合理都视为"取不到"：某些客户端会返回零矩形，
        // 直接拿来定位会把候选窗丢到屏幕左下角。
        guard rect.origin.x.isFinite, rect.origin.y.isFinite,
              rect.size.width.isFinite, rect.size.height.isFinite,
              rect.size.height > 0, rect.size.height < 1000,
              !(rect.origin.x == 0 && rect.origin.y == 0) else {
            return nil
        }
        return rect
    }

    /// 候选窗左上角坐标：优先放光标行**下方**，下方空间不足放**上方**，
    /// 最后用屏幕可见区域钳制，保证整块候选窗都在屏内。
    private static func candidateOrigin(size: NSSize, client: (any IMKTextInput)?,
                                        screen: NSScreen?) -> NSPoint {
        let visible = (screen ?? NSScreen.main)?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        guard let caret = caretRect(client: client) else {
            // 兜底：光标矩形取不到时才退回鼠标位置（旧行为）
            let mouse = NSEvent.mouseLocation
            return clampTopLeft(NSPoint(x: mouse.x + 8, y: mouse.y - 4), size: size, in: visible)
        }
        // 下方：面板顶边在光标行底边之下 gap；上方：面板底边在光标行顶边之上 gap。
        let below = caret.minY - candidateGap            // 顶边 y（向下生长）
        let above = caret.maxY + candidateGap + size.height // 顶边 y（向上放时顶边更高）
        // 空间判断用「放得下整块」为判据：下方剩余 = caret.minY - visible.minY
        let spaceBelow = caret.minY - visible.minY
        let spaceAbove = visible.maxY - caret.maxY
        var top = below
        if spaceBelow < size.height + candidateGap && spaceAbove > spaceBelow {
            top = above
        }
        return clampTopLeft(NSPoint(x: caret.minX, y: top), size: size, in: visible)
    }

    /// 把面板左上角钳制到屏幕可见区域内（不超出左右与上下边界）。
    private static func clampTopLeft(_ topLeft: NSPoint, size: NSSize, in visible: NSRect) -> NSPoint {
        var x = topLeft.x
        var top = topLeft.y
        // 左边越界 → 贴左边；右边越界 → 右移（优先保持左侧对齐光标起点）
        if x + size.width > visible.maxX { x = visible.maxX - size.width }
        if x < visible.minX { x = visible.minX }
        // 顶边越界 → 下拉；底边越界 → 上推
        if top > visible.maxY { top = visible.maxY }
        if top - size.height < visible.minY { top = visible.minY + size.height }
        return NSPoint(x: x, y: top)
    }

    /// 光标所在屏幕：按插入点矩形中心点找屏幕，取不到用鼠标所在屏幕。
    private static func screenForCursor(_ client: (any IMKTextInput)?) -> NSScreen? {
        if let caret = caretRect(client: client),
           let hit = NSScreen.screens.first(where: { $0.frame.contains(NSPoint(x: caret.midX, y: caret.midY)) }) {
            return hit
        }
        let mouse = NSEvent.mouseLocation
        return NSScreen.screens.first(where: { $0.frame.contains(mouse) }) ?? NSScreen.main
    }

    // MARK: - IMKCandidates 数据源

    override func candidates(_ sender: Any!) -> [Any]! {
        current?.candidates.map(\.text) ?? []
    }

    override func candidateSelected(_ candidateString: NSAttributedString!) {
        let text = candidateString.string
        guard let list = current?.candidates,
              let idx = list.firstIndex(where: { $0.text == text }),
              let client = self.client() else { return }
        commit(index: idx, client: client)
    }

    // MARK: - 生命周期

    // 输入会话被提交（点击文本框外、切换应用/输入法时系统调用）：必须清空
    // 会话状态并收起候选窗，保证候选面板从屏幕彻底消失且不影响焦点。
    override func commitComposition(_ sender: Any!) {
        // 宿主要求结束本次输入会话（点击文本框外/切换应用）：已确认段先落盘
        //（用户确认过的字不能丢；旧实现里这些段早已在文档，净效果一致），
        // 剩余拼音丢弃。
        flushConfirmed(self.client())
        if current?.input.isEmpty == false {
            current = engine.reset()
        } else {
            current = nil
        }
        exitAssociate()
        Self.hideCandidatePanel()
        ModeToastController.hide()
        candidateWindow?.hide()
    }

    override func activateServer(_ sender: Any!) {
        // 新的输入上下文：先收掉上一个上下文可能残留的候选窗，并丢弃上一个
        // 上下文遗留的组词状态。若不清，切到本输入法后的首次按键会带着旧
        // 会话的 preedit 去和 daemon 组词，表现为「刚切换过来就打错字」。
        // 这里不调 daemon（reset 只请求 daemon 重置会话，代价一次 IPC）；
        // 直接丢弃本地状态即可——daemon 侧会话按连接维护，新连接本就是空的。
        // 已确认段缓冲同样不得带入：此时写入会插进**新的**输入上下文。
        current = nil
        confirmedText = ""
        exitAssociate()
        Self.hideCandidatePanel()
        ModeToastController.hide()
        candidateWindow?.hide()
    }

    override func deactivateServer(_ sender: Any!) {
        // 切换输入法或失去焦点时：已确认段先落盘（同一上下文内允许最后一次
        // 插入；旧实现里这些段早已在文档，净效果一致），剩余拼音丢弃。
        // 若无未决组词，坚决不发生任何阻塞性 IPC，保证 WindowServer 与系统
        // 输入法切换 HUD 丝滑无卡顿。
        flushConfirmed(self.client())
        if current?.input.isEmpty == false {
            current = engine.reset()
        } else {
            current = nil
        }
        exitAssociate()
        Self.hideCandidatePanel()
        ModeToastController.hide()
        candidateWindow?.hide()
    }

    deinit {
        // deinit 拿不到 client，无法补写已确认段；正常路径 deactivateServer /
        // commitComposition 已先行落盘，这里只兜底清理 UI。
        exitAssociate()
        Self.hideCandidatePanel()
        ModeToastController.hide()
        candidateWindow?.hide()
    }
}