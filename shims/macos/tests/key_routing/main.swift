import AppKit
import InputMethodKit

// 按键路由回归测试：用合成 NSEvent 直接驱动 ChaosetController.handle，
// 覆盖 keyUp 放行、字母进组词、Cmd/Ctrl/Option 放行、Shift 空按切换中英、
// Shift+字母不误切换、CapsLock 大写保留（2026-11-22 用户反馈）、
// Option+Backspace 按音节回退（2026-10-31 三端统一）、
// 方向键页内预选与空格确认预选项（issue#2，2026-11-12），
// 以及「退格删最后一个拼音字母」在忽略空插入的客户端上也能一次删空
// （2026-11-01 用户真机反馈，根因见 clearMarkedText 注释）。
// 修复前（2026-10-06 之前）旧实现 4/8 不通过。
//
// 推荐用法（自动编译 + 起临时 daemon + 清理）：
//   shims/macos/tests/key_routing/run.sh
//
// 手动用法（在 shims/macos 目录下，需要 daemon 在跑）:
//   swiftc -O -framework InputMethodKit -framework AppKit \
//     tests/key_routing/main.swift $(ls src/*.swift | grep -v 'src/main.swift$') -o build/key_routing
//   ./build/key_routing
// （2026-11-06 架构拆分后 src/*.swift 为多文件；入口只能是 tests/key_routing/main.swift）
// （入口文件必须叫 main.swift：与其它文件一起编译时，顶层语句只允许出现在 main.swift）
final class MockClient: NSObject, IMKTextInput {
    var marked = ""
    var inserted: [String] = []
    /// 模拟「忽略空插入」的客户端（2026-11-01 用户真机症状）：insertText("")
    /// 不改变 marked 区，客户端继续把最后一个字母显示为下划线预编辑。
    /// 默认 false = 与真实 NSTextView 一致（空插入会删掉 marked、结束 inline session；
    /// 最小 AppKit App 直接调 NSTextInputClient 的对照实验已验证）。
    var ignoresEmptyInsert = false
    func insertText(_ string: Any!, replacementRange: NSRange) {
        // Any! 实际是 String?；String(describing:) 会得到 "Optional(…)" 并把
        // 清空标记区（insertText("")）误记为普通插入。这里显式解包。
        let s = (string as? String) ?? String(describing: string)
        if s.isEmpty {
            // 真实 NSTextView 会删掉 marked；忽略空插入的客户端则原样保留，
            // 壳必须靠 markedRange() 探测（IMKTextInput 无 hasMarkedText）
            // + setMarkedText("") 兜底清掉。
            if !ignoresEmptyInsert { marked = "" }
        } else {
            // 非空插入 = 提交，AppKit 客户端此时会清掉 marked 区。
            marked = ""
        }
        inserted.append(s)
    }
    func setMarkedText(_ string: Any!, selectionRange: NSRange, replacementRange: NSRange) {
        marked = (string as? String) ?? String(describing: string)
    }
    func unmarkText() { marked = "" }
    func selectedRange() -> NSRange { return NSRange(location: 0, length: 0) }
    func markedRange() -> NSRange {
        return marked.isEmpty ? NSRange(location: NSNotFound, length: 0)
                              : NSRange(location: 0, length: (marked as NSString).length)
    }
    func hasMarkedText() -> Bool { return !marked.isEmpty }
    func attributedSubstring(from range: NSRange) -> NSAttributedString! { return NSAttributedString(string: "") }
    func length() -> Int { return 0 }
    func characterIndex(for point: NSPoint, tracking mode: IMKLocationToOffsetMappingMode,
                        inMarkedRange: UnsafeMutablePointer<ObjCBool>?) -> Int { return 0 }
    func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer!) -> NSRect { return .zero }
    func validAttributesForMarkedText() -> [Any]! { return [] }
    func overrideKeyboard(withKeyboardNamed keyboardUniqueName: String!) {}
    func selectMode(_ modeName: String!) {}
    func bundleIdentifier() -> String! { return "test.mock" }
    func supportsUnicode() -> Bool { return true }
    func supportsProperty(_ property: TSMDocumentPropertyTag) -> Bool { return false }
    func windowLevel() -> CGWindowLevel { return 0 }
    func attributes(forCharacterIndex index: Int,
                    lineHeightRectangle lineRect: UnsafeMutablePointer<NSRect>!) -> [AnyHashable: Any]! { return [:] }
    func uniqueClientIdentifierString() -> String! { return "mock" }
    func string(from range: NSRange, actualRange: NSRangePointer!) -> String! { return "" }
}

/// 合成按键事件。charactersIgnoringModifiers 的语义必须与 AppKit 一致：
/// 它忽略除 Shift 外的修饰键——CapsLock 被忽略（CapsLock+A 返回 "a"），
/// Shift 仍生效（Shift+A 返回 "A"）。默认与 characters 相同；CapsLock 大写
/// 用例显式传小写 ignoring，才能复现旧壳把大写吞成小写的真实路径。
func ev(_ chars: String, _ code: UInt16, _ flags: NSEvent.ModifierFlags = [],
        _ type: NSEvent.EventType = .keyDown, ignoring: String? = nil) -> NSEvent {
    NSEvent.keyEvent(with: type, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: 0,
                     context: nil, characters: chars, charactersIgnoringModifiers: ignoring ?? chars,
                     isARepeat: false, keyCode: code)!
}

/// 合成 flagsChanged 事件：NSEvent.keyEvent(with: .flagsChanged…) 返回 nil，
/// 与真机 IMK 投递路径对齐需要用 CGEvent 桥接（修饰键状态变化正是本组断言的输入）。
func flagsEv(_ code: UInt16, _ flags: NSEvent.ModifierFlags = []) -> NSEvent {
    let cg = CGEvent(source: nil)!
    cg.type = .flagsChanged
    cg.setIntegerValueField(.keyboardEventKeycode, value: Int64(code))
    cg.flags = CGEventFlags(rawValue: UInt64(flags.rawValue))
    return NSEvent(cgEvent: cg)!
}

let c = ChaosetController()
let m = MockClient()

/// 异步引擎管线（issue #149）：每个按键后等结果回投并应用。
/// 生产路径绝不阻塞；测试用它把「入队 → 引擎线程 → 主队列应用」跑完再断言。
@discardableResult
func k(_ e: NSEvent, client: MockClient = m) -> Bool {
    let r = c.handle(e, client: client)
    c.settleEngineForTests()
    return r
}
var pass = 0, fail = 0
func check(_ name: String, _ cond: Bool, _ detail: String = "") {
    if cond { pass += 1; print("ok   \(name)") } else { fail += 1; print("FAIL \(name) \(detail)") }
}

check("keyUp 放行（避免同一按键处理两次）", k(ev("a", 0, [], .keyUp), client: m) == false)

let r2 = k(ev("n", 45), client: m)
check("字母 keyDown 进组词", r2 == true, "ret=\(r2)")
check("预编辑含 n", m.marked.contains("n"), "marked=\(m.marked)")

check("Cmd+S 放行（快捷键不被吞）", k(ev("s", 1, [.command]), client: m) == false)
check("Ctrl+A 放行", k(ev("a", 0, [.control]), client: m) == false)

// IMK 只投递 recognizedEvents 声明过的事件类型；缺 flagsChanged 时 Shift 空按
// 在真机永远收不到（合成事件直驱 handle 的测试覆盖不到投递层）。
let recognized = NSEvent.EventTypeMask(rawValue: UInt64(c.recognizedEvents(nil)))
check("recognizedEvents 声明 keyDown + flagsChanged（否则 Shift 空按真机无效）",
      recognized.contains(NSEvent.EventTypeMask.keyDown) && recognized.contains(NSEvent.EventTypeMask.flagsChanged),
      "mask=\(recognized.rawValue)")

_ = k(ev("", 56, [.shift]), client: m)              // Shift 按下
_ = k(ev("", 56, [], .flagsChanged), client: m)     // Shift 松开（空按）
let r5 = k(ev("a", 0), client: m)
check("Shift 空按切到英文（字母放行）", r5 == false, "ret=\(r5)")

_ = k(ev("", 56, [.shift]), client: m)
_ = k(ev("", 56, [], .flagsChanged), client: m)
let r6 = k(ev("a", 0), client: m)
check("再空按切回中文（字母被处理）", r6 == true, "ret=\(r6)")

_ = k(ev("", 56, [.shift]), client: m)              // Shift 按下
_ = k(ev("a", 0, [.shift]), client: m)              // Shift+A
_ = k(ev("", 56, [], .flagsChanged), client: m)     // Shift 松开
let r7 = k(ev("b", 11), client: m)
check("Shift+字母不误切换（仍中文）", r7 == true, "ret=\(r7)")

// 大写保留（用户反馈）：charactersIgnoringModifiers 会忽略 CapsLock（返回
// 小写），修复前壳读它，CapsLock 大写在这一步就被吞成小写（Shift 不受影响，
// 它仍然生效）。合成事件的 ignoring 必须按 AppKit 真实语义传（CapsLock 被
// 忽略、Shift 不被忽略），否则会把 Shift 伪造成失败、掩盖根因。
_ = k(ev("", 53), client: m) // Esc 清空
for (ch, code) in [("N", 45), ("I", 34), ("H", 4), ("A", 0), ("O", 31)] {
    _ = k(ev(ch, UInt16(code), [.capsLock], ignoring: ch.lowercased()), client: m)
}
check("CapsLock 字母预编辑保留大小写", m.marked == "NI'HAO", "marked=\(m.marked)")
let rCaps = k(ev("\r", 36), client: m)
check("回车原样上屏 CapsLock 大写拼音", rCaps == true && m.inserted.contains("NIHAO"),
      "ret=\(rCaps) inserted=\(m.inserted)")

// Shift 大写本来就由 charactersIgnoringModifiers 保留（它不忽略 Shift）；
// 这条断言防止后续改动把 Shift 路径改坏，不代表旧壳在这里丢大写。
_ = k(ev("", 53), client: m) // Esc 清空
for (ch, code) in [("N", 45), ("I", 34), ("H", 4), ("A", 0), ("O", 31)] {
    _ = k(ev(ch, UInt16(code), [.shift]), client: m)
}
check("Shift+字母预编辑保留大小写", m.marked == "NI'HAO", "marked=\(m.marked)")
let rUpper = k(ev("\r", 36), client: m)
check("回车原样上屏大写拼音", rUpper == true && m.inserted.contains("NIHAO"),
      "ret=\(rUpper) inserted=\(m.inserted)")

// Option+Backspace（⌥⌫）：组词中按音节回退（三端统一，2026-10-31）。
// 先 Esc 清空上面的残余组词，再输入 nihao → ⌥⌫ 应回退到 ni。
_ = k(ev("", 53), client: m) // Esc
for (ch, code) in [("n", 45), ("i", 34), ("h", 4), ("a", 0), ("o", 31)] {
    _ = k(ev(ch, UInt16(code)), client: m)
}
check("Option+Backspace 前组词含 hao", m.marked.contains("hao"), "marked=\(m.marked)")
let r8 = k(ev("", 51, [.option]), client: m)
check("Option+Backspace 按音节回退（消费）", r8 == true, "ret=\(r8)")
check("Option+Backspace 回退到 ni", m.marked.contains("ni") && !m.marked.contains("hao"), "marked=\(m.marked)")

// 修饰键映射（2026-10-31 用户要求）：Command↔Win/Super、Control↔Ctrl、
// Option↔Alt。组词中 Backspace/Delete 的优先级：Cmd/Ctrl/Shift 全部删除，
// Option 按音节回退，无修饰逐字符回退；Delete 与 Backspace 同语义。
func typeNihao() {
    _ = k(ev("", 53), client: m) // Esc 清空
    for (ch, code) in [("n", 45), ("i", 34), ("h", 4), ("a", 0), ("o", 31)] {
        _ = k(ev(ch, UInt16(code)), client: m)
    }
}

typeNihao()
let r9 = k(ev("", 51, [.command]), client: m) // Cmd+Backspace
check("Cmd+Backspace 全部删除（消费）", r9 == true, "ret=\(r9)")
check("Cmd+Backspace 后组词为空", m.marked.isEmpty, "marked=\(m.marked)")

typeNihao()
let r10 = k(ev("", 117, [.control]), client: m) // Ctrl+ForwardDelete
check("Ctrl+Delete 全部删除（消费）", r10 == true, "ret=\(r10)")
check("Ctrl+Delete 后组词为空", m.marked.isEmpty, "marked=\(m.marked)")

typeNihao()
let r11 = k(ev("", 117), client: m) // 普通 ForwardDelete
check("普通 Delete 逐字符回退（消费）", r11 == true, "ret=\(r11)")
check("普通 Delete 只删一个字符", m.marked.contains("ha") && !m.marked.contains("hao"), "marked=\(m.marked)")

typeNihao()
let r12 = k(ev("", 117, [.option]), client: m) // Option+ForwardDelete
check("Option+Delete 按音节回退（消费）", r12 == true, "ret=\(r12)")
check("Option+Delete 回退到 ni", m.marked.contains("ni") && !m.marked.contains("hao"), "marked=\(m.marked)")

// 联想态 Backspace：三端统一为「收起联想并消费」（2026-10-31）。
// 用 zhongguo → 中国 进入联想（词典有「中国共产党」等续接词）。
func typeZhongguo() {
    _ = k(ev("", 53), client: m) // Esc 清空
    for (ch, code) in [("z", 6), ("h", 4), ("o", 31), ("n", 45), ("g", 5),
                        ("g", 5), ("u", 32), ("o", 31)] {
        _ = k(ev(ch, UInt16(code)), client: m)
    }
}
typeZhongguo()
_ = k(ev("", 49), client: m) // 空格选首选（中国），进入联想
let r13 = k(ev("", 51), client: m) // 联想态 Backspace
check("联想态 Backspace 收起联想并消费", r13 == true, "ret=\(r13)")

// Shift+PageUp 组词中不翻页（放行给宿主滚动）；PageUp/PageDown 无论 Shift
// 都翻页只适用于联想态（三端一致，2026-10-31）。
typeNihao()
let r14 = k(ev("", 116, [.shift]), client: m)
check("Shift+PageUp 组词中放行", r14 == false, "ret=\(r14)")

// Ctrl+Shift+U 是宿主快捷键：mac 按 rawChars "U" 不匹配 Ctrl+U，放行。
typeNihao()
let shiftU = NSEvent.keyEvent(with: .keyDown, location: .zero,
                              modifierFlags: [.control, .shift], timestamp: 0,
                              windowNumber: 0, context: nil, characters: "U",
                              charactersIgnoringModifiers: "U", isARepeat: false,
                              keyCode: 32)!
let r15 = k(shiftU, client: m)
check("Ctrl+Shift+U 放行（不清空组词）", r15 == false, "ret=\(r15)")

// 用户真机回归（2026-11-01）：输入 jin 退格到只剩 j 时，第一次退格看起来无效、
// 要按第二次才删掉。根因：壳在组词收尾时用 insertText("") 收掉标记区，但部分
// 客户端忽略空插入，marked 文本原样留在文档里（仍是下划线预编辑）；下一次退格
// 时 current 已 nil → 放行给宿主，宿主才删掉 marked 的字母（= 按两次）。
// 修复：clearMarkedText 先空插入、再用 markedRange() 探测，仍残留则
// setMarkedText("")（必要时按真实 markedRange 显式替换）。
func typeJin(_ client: MockClient) {
    _ = k(ev("", 53), client: client) // Esc 清空
    for (ch, code) in [("j", 38), ("i", 34), ("n", 45)] {
        _ = k(ev(ch, UInt16(code)), client: client)
    }
}

// ① 忽略空插入的客户端（用户实际症状）：第 3 次退格必须一次删空。
let mIgnore = MockClient()
mIgnore.ignoresEmptyInsert = true
typeJin(mIgnore)
check("忽略空插入：jin 组词预编辑完整", mIgnore.marked == "jin", "marked=\(mIgnore.marked)")
_ = k(ev("\u{7F}", 51), client: mIgnore)
_ = k(ev("\u{7F}", 51), client: mIgnore)
check("忽略空插入：退格两次后剩 j", mIgnore.marked == "j", "marked=\(mIgnore.marked)")
let rJinIgnore3 = k(ev("\u{7F}", 51), client: mIgnore)
check("忽略空插入：第 3 次退格一次删掉最后一个字母", mIgnore.marked.isEmpty, "marked=\(mIgnore.marked)")
check("忽略空插入：第 3 次退格被消费", rJinIgnore3 == true, "ret=\(rJinIgnore3)")

// ② 正常客户端（NSTextView 语义：空插入能删 marked）：兜底不得改坏正常路径。
typeJin(m)
_ = k(ev("\u{7F}", 51), client: m)
_ = k(ev("\u{7F}", 51), client: m)
let rJinNormal3 = k(ev("\u{7F}", 51), client: m)
check("正常客户端：jin 三次退格后标记区为空", m.marked.isEmpty, "marked=\(m.marked)")
check("正常客户端：第 3 次退格被消费", rJinNormal3 == true, "ret=\(rJinNormal3)")
let rJinNormal4 = k(ev("\u{7F}", 51), client: m)
check("正常客户端：删空后再次退格放行给宿主", rJinNormal4 == false, "ret=\(rJinNormal4)")

// ---- 方向键：上/下翻页、左/右页内预选 + 空格确认（2026-11-12 用户反馈）----
// 上/下 = 上一页/下一页（翻页后预选归零；边界页保持不动，仍消费按键）；
// 左/右 = 页内前/后一项（页边界保持不动，仍消费按键）；空格确认预选项。
func typeYi() {
    _ = k(ev("", 53), client: m) // Esc 清空
    for (ch, code) in [("y", 16), ("i", 34)] {
        _ = k(ev(ch, UInt16(code)), client: m)
    }
}
let downArrow = ev("\u{F701}", 125)
let upArrow = ev("\u{F700}", 126)
let leftArrow = ev("\u{F702}", 123)
let rightArrow = ev("\u{F703}", 124)
typeYi()
check("方向键前置：yi 有候选", !(c.current?.candidates.isEmpty ?? true),
      "candidates=\(c.current?.candidates.count ?? 0)")
let yiPageCount = c.current?.pageCount ?? 0
var firstPageStaysOk = true
var nextPageOk = true
var lastPageStaysOk = true
if yiPageCount > 1 {
    firstPageStaysOk = k(upArrow, client: m) == true
        && (c.current?.page ?? -1) == 0
    nextPageOk = k(downArrow, client: m) == true
        && (c.current?.page ?? -1) == 1
        && CandidatePanelController.selectedIndex == 0
    for _ in 1..<yiPageCount { _ = k(downArrow, client: m) }
    lastPageStaysOk = k(downArrow, client: m) == true
        && (c.current?.page ?? -1) == yiPageCount - 1
    for _ in 0..<(yiPageCount - 1) { _ = k(upArrow, client: m) }
} else {
    print("skip 词库 yi 单页，跳过上下翻页断言")
}
check("首页继续上移保持不动（仍消费）", firstPageStaysOk,
      "page=\(c.current?.page ?? -1)")
check("下方向键翻下一页（预选归零）", nextPageOk,
      "page=\(c.current?.page ?? -1) index=\(CandidatePanelController.selectedIndex)")
check("末页继续下移保持不动（仍消费）", lastPageStaysOk,
      "page=\(c.current?.page ?? -1) pageCount=\(yiPageCount)")
// 左/右：页内预选移动
typeYi()
check("右方向键移动预选并消费",
      k(rightArrow, client: m) == true && CandidatePanelController.selectedIndex == 1,
      "index=\(CandidatePanelController.selectedIndex)")
check("左方向键回到前一项",
      k(leftArrow, client: m) == true && CandidatePanelController.selectedIndex == 0,
      "index=\(CandidatePanelController.selectedIndex)")
check("页首继续左移保持不动（仍消费）",
      k(leftArrow, client: m) == true && CandidatePanelController.selectedIndex == 0,
      "index=\(CandidatePanelController.selectedIndex)")
// 空格确认当前预选项（不再固定第 0 项）
typeYi()
_ = k(rightArrow, client: m)
let secondText = c.current?.candidates[1].text ?? ""
_ = k(ev(" ", 49), client: m)
check("空格确认预选项", m.inserted.last(where: { !$0.isEmpty }) == secondText,
      "inserted=\(m.inserted.last(where: { !$0.isEmpty }) ?? "nil") want=\(secondText)")
// 无组词时方向键必须放行宿主（绝不消费）
_ = k(ev("", 53), client: m)
check("无组词时方向键放行宿主", k(upArrow, client: m) == false)
// 带 Command 的方向键是宿主快捷键：放行
typeYi()
check("Cmd+方向键放行（宿主快捷键）", k(ev("\u{F702}", 123, [.command]), client: m) == false)
_ = k(ev("", 53), client: m)
// 联想态：左/右移动联想预选，空格确认预选联想词
typeZhongguo()
_ = k(ev(" ", 49), client: m) // 空格选首选 → 进入联想
let assocList = c.associateCandidates ?? []
var assocMoveOk = true
var assocSpaceOk = true
if assocList.count >= 2 {
    let assocSecond = assocList[1].text
    assocMoveOk = k(rightArrow, client: m) == true
        && CandidatePanelController.selectedIndex == 1
    _ = k(ev(" ", 49), client: m)
    assocSpaceOk = m.inserted.last == assocSecond
} else {
    print("skip 该词库联想候选不足 2 条，跳过方向键断言")
}
check("联想态下右方向键移动预选并消费", assocMoveOk,
      "index=\(CandidatePanelController.selectedIndex)")
check("联想态空格确认预选联想词", assocSpaceOk,
      "inserted=\(m.inserted.last(where: { !$0.isEmpty }) ?? "nil")")

// ---- 非 Shift 修饰键：立即原样放行、不改输入状态（远程桌面 Ctrl 卡键的 IME 侧加固）----
// macOS 全局快捷键（Ctrl+↑ 进 Mission Control 等）会与远程桌面客户端的按键转发叠加。
// 输入法若把修饰键 flagsChanged 当普通按键走完整流程，`passThrough` 会同步 daemon IPC
// 并清掉组词——修饰键事件被延迟/打乱后，远端 Windows 可能收不到 Ctrl keyUp → Ctrl 卡住。
// 契约：Cmd/Option/Ctrl/CapsLock/Fn 的状态事件一律 false 且零副作用（不改组词/联想）。
_ = k(ev("", 53), client: m) // Esc：清掉上一条断言留下的组词/联想
let modBase = k(ev("n", 45), client: m)
check("修饰键断言前：字母重新进组词", modBase && m.marked.contains("n"),
      "ret=\(modBase) marked=\(m.marked)")
let modInputBefore = c.current?.input ?? ""
check("Ctrl 按下 flagsChanged 放行",
      k(flagsEv(59, [.control]), client: m) == false)
check("Ctrl 按下不清组词", (c.current?.input ?? "") == modInputBefore,
      "before=\(modInputBefore) after=\(c.current?.input ?? "nil")")
check("Ctrl 释放 flagsChanged 放行",
      k(flagsEv(59), client: m) == false)
check("Ctrl 释放不清组词", (c.current?.input ?? "") == modInputBefore)
check("Option 按下 flagsChanged 放行",
      k(flagsEv(58, [.option]), client: m) == false)
check("Cmd 按下 flagsChanged 放行",
      k(flagsEv(55, [.command]), client: m) == false)
check("CapsLock flagsChanged 放行",
      k(flagsEv(57, [.capsLock]), client: m) == false)
_ = k(ev("", 53), client: m) // Esc：恢复干净状态，供后续断言使用

// ---- 候选窗没有特权：焦点离开即收起并清空（2026-11-23 用户反馈）----
// Cmd+Tab / Dock 切应用、切换 Space、联想态都不会触发 commitComposition /
// deactivateServer，旧实现候选窗留在新应用上面截走鼠标。进程级焦点守卫：别的
// 应用激活 / Space 切换 → 已确认段落盘、剩余拼音丢弃、联想退出、面板收起。
_ = k(ev("", 53), client: m) // Esc：干净状态
for key in [("n", UInt16(45)), ("i", UInt16(34))] {
    _ = k(ev(key.0, key.1), client: m)
}
check("焦点守卫前：字母进组词且候选窗可见",
      (c.current?.input ?? "") == "ni" && CandidatePanelController.isVisible,
      "input=\(c.current?.input ?? "nil") visible=\(CandidatePanelController.isVisible)")
ChaosetController.handleFocusLossSignal(pid: ProcessInfo.processInfo.processIdentifier)
check("自己进程激活不结束会话", (c.current?.input ?? "") == "ni")
ChaosetController.handleFocusLossSignal(pid: 99999) // 别的应用激活
check("别的应用激活：组词清空", (c.current?.input ?? "").isEmpty && (c.current?.candidates.isEmpty ?? true),
      "current=\(String(describing: c.current?.input))")
check("别的应用激活：候选窗收起", !CandidatePanelController.isVisible)
check("别的应用激活：联想退出", c.associateCandidates == nil)
check("别的应用激活：客户端标记区清掉", m.marked.isEmpty, "marked=\(m.marked)")
// 幂等：没有待收尾状态时再次触发（Space 切换信号）不插入任何文本
let insertedBefore = m.inserted.count
ChaosetController.handleFocusLossSignal(pid: nil)
check("无会话时焦点信号零副作用", m.inserted.count == insertedBefore && (c.current?.input ?? "").isEmpty)
// 渐进上屏后失焦：已确认段落盘，剩余拼音丢弃
for ch in "nihaoshijie" {
    _ = k(ev(String(ch), UInt16(ch.asciiValue ?? 0)), client: m)
}
_ = k(ev("1", 18), client: m) // 选 1「你好」→ 已确认段「你好」+ 剩余 shijie
check("渐进上屏前置于失焦用例：已确认段存在", c.confirmedText == "你好",
      "confirmed=\(c.confirmedText) input=\(c.current?.input ?? "nil")")
let insertedProgressive = m.inserted.count
ChaosetController.handleFocusLossSignal(pid: 99999)
check("失焦时已确认段落盘", m.inserted.count >= insertedProgressive + 1
      && m.inserted[insertedProgressive] == "你好", "inserted=\(m.inserted.suffix(2))")
check("失焦后无残留状态", (c.current?.input ?? "").isEmpty && c.confirmedText.isEmpty
      && !CandidatePanelController.isVisible && m.marked.isEmpty)

// ---- 异步引擎管线（issue #149）：乐观预编辑 / 有序选词 / 失败不关候选窗 ----
// 1) 乐观预编辑：结果未回投前，按键也必须立即显示（IMK 主线程绝不等待）。
_ = c.handle(ev("", 53), client: m) // Esc
c.settleEngineForTests()
_ = c.handle(ev("n", 45), client: m)  // 不 settle：结果还在飞
check("异步：结果未回投时按键立即出现在乐观预编辑里",
      m.marked.contains("n"), "marked=\(m.marked)")
c.settleEngineForTests()
check("异步：结果回投后仍保持 n", m.marked.contains("n"), "marked=\(m.marked)")

// 2) 有序管线：快打 nihao 后立刻空格选词（每个键都不 settle）——选词操作排在
//    所有输入之后，引擎按完整输入解析，不会只按已应用的 "n" 选。
_ = c.handle(ev("", 53), client: m)
c.settleEngineForTests()
for (ch, code) in [("n", 45), ("i", 34), ("h", 4), ("a", 0), ("o", 31)] {
    _ = c.handle(ev(ch, UInt16(code)), client: m)
}
_ = c.handle(ev(" ", 49), client: m)
c.settleEngineForTests()
let orderedPick = m.inserted.last(where: { !$0.isEmpty }) ?? ""
check("异步：快打后空格按完整输入选词（不是原始拼音）",
      !orderedPick.isEmpty && orderedPick != "nihao"
      && orderedPick.unicodeScalars.contains(where: { $0.value >= 0x4E00 && $0.value <= 0x9FFF }),
      "picked=\(orderedPick)")

// 3) daemon 失败：乐观预编辑与候选窗都保留（不弹出、不关闭）。
_ = c.handle(ev("", 53), client: m)
c.settleEngineForTests()
for (ch, code) in [("n", 45), ("i", 34)] {
    _ = k(ev(ch, UInt16(code)), client: m)
}
check("失败注入前：组词与候选窗可见",
      (c.current?.input ?? "") == "ni" && CandidatePanelController.itemCount > 0,
      "input=\(c.current?.input ?? "nil") items=\(CandidatePanelController.itemCount)")
EnginePipeline.failureInjectionForTests = true
_ = k(ev("h", 4), client: m)
check("daemon 失败：乐观预编辑保留", m.marked.contains("nih"), "marked=\(m.marked)")
check("daemon 失败：候选窗不关闭", CandidatePanelController.itemCount > 0,
      "items=\(CandidatePanelController.itemCount)")
EnginePipeline.failureInjectionForTests = false
_ = c.handle(ev("", 53), client: m)
c.settleEngineForTests()
// 4) ⌥⌫ 乐观回放：与 Windows/Linux 一致，结果未回投前预编辑立即退掉末音节。
_ = c.handle(ev("", 53), client: m) // Esc
c.settleEngineForTests()
for (ch, code) in [("n", 45), ("i", 34), ("h", 4), ("a", 0), ("o", 31)] {
    _ = k(ev(ch, UInt16(code)), client: m)
}
check("⌥⌫ 前置：已应用预编辑为 ni'hao", m.marked == "ni'hao", "marked=\(m.marked)")
_ = c.handle(ev("", 51, [.option]), client: m)  // 不 settle：结果还在飞
check("⌥⌫ 乐观回放：结果未回投时预编辑立即退掉末音节",
      m.marked == "ni", "marked=\(m.marked)")
check("⌥⌫ 乐观回放：末尾不保留引擎补的分隔符",
      !m.marked.hasSuffix("'"), "marked=\(m.marked)")
c.settleEngineForTests()

// 5) 用户自己敲的分隔符：xi'an → ⌥⌫ 本地回放为 xi'（分隔符是用户输入的一部分）。
_ = c.handle(ev("", 53), client: m)
c.settleEngineForTests()
for (ch, code) in [("x", 7), ("i", 34), ("'", 39), ("a", 0), ("n", 45)] {
    _ = k(ev(ch, UInt16(code)), client: m)
}
check("⌥⌫ 前置：xi'an 预编辑", m.marked == "xi'an", "marked=\(m.marked)")
_ = c.handle(ev("", 51, [.option]), client: m)
// 与 Windows/Linux 同一近似：预编辑去掉末段后不保留「悬空分隔符」（xi，而不是
// xi'），但用户自己敲的分隔符仍留在输入串里（xi'），不会被回放吞掉。
check("⌥⌫ 乐观回放：预编辑去掉末段且不留悬空分隔符",
      m.marked == "xi", "marked=\(m.marked)")
check("⌥⌫ 乐观回放：用户自己敲的分隔符保留在输入串里",
      c.optimisticInput() == "xi'", "input=\(c.optimisticInput())")
c.settleEngineForTests()
// 断言用例数未减少：删掉一条断言会让回归测试静默变弱（2026-10-31）。
// 新增/删除断言时同步更新 expectedChecks。
// 76 = 原有 64 + 「异步引擎管线」6 条 + 「⌥⌫ 乐观回放」6 条。
let expectedChecks = 76
if pass != expectedChecks {
    fail += 1
    print("FAIL 用例数量 \(pass) != \(expectedChecks)（删/加断言需同步更新）")
}

print("\n结果: pass=\(pass) fail=\(fail)")
exit(fail == 0 ? 0 : 1)
