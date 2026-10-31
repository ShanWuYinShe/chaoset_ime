import AppKit
import InputMethodKit

// 按键路由回归测试：用合成 NSEvent 直接驱动 ChaosetController.handle，
// 覆盖 keyUp 放行、字母进组词、Cmd/Ctrl/Option 放行、Shift 空按切换中英、
// Shift+字母不误切换、Option+Backspace 按音节回退（2026-10-31 三端统一）。
// 修复前（2026-10-06 之前）旧实现 4/8 不通过。
//
// 推荐用法（自动编译 + 起临时 daemon + 清理）：
//   shims/macos/tests/key_routing/run.sh
//
// 手动用法（在 shims/macos 目录下，需要 daemon 在跑）:
//   swiftc -O -framework InputMethodKit -framework AppKit \
//     tests/key_routing/main.swift src/ChaosetController.swift src/EngineClient.swift \
//     src/CandidatePanel.swift src/SettingsWindow.swift -o build/key_routing
//   ./build/key_routing
// （入口文件必须叫 main.swift：与其它文件一起编译时，顶层语句只允许出现在 main.swift）
final class MockClient: NSObject, IMKTextInput {
    var marked = ""
    var inserted: [String] = []
    func insertText(_ string: Any!, replacementRange: NSRange) {
        // Any! 实际是 String?；String(describing:) 会得到 "Optional(…)" 并把
        // 清空标记区（insertText("")）误记为普通插入。这里显式解包，
        // 空串时同步清空 marked（真实客户端行为）。
        let s = (string as? String) ?? String(describing: string)
        if s.isEmpty { marked = "" }
        inserted.append(s)
    }
    func setMarkedText(_ string: Any!, selectionRange: NSRange, replacementRange: NSRange) {
        marked = (string as? String) ?? String(describing: string)
    }
    func unmarkText() { marked = "" }
    func selectedRange() -> NSRange { return NSRange(location: 0, length: 0) }
    func markedRange() -> NSRange { return NSRange(location: NSNotFound, length: 0) }
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

func ev(_ chars: String, _ code: UInt16, _ flags: NSEvent.ModifierFlags = [],
        _ type: NSEvent.EventType = .keyDown) -> NSEvent {
    NSEvent.keyEvent(with: type, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: 0,
                     context: nil, characters: chars, charactersIgnoringModifiers: chars.lowercased(),
                     isARepeat: false, keyCode: code)!
}

let c = ChaosetController()
let m = MockClient()
var pass = 0, fail = 0
func check(_ name: String, _ cond: Bool, _ detail: String = "") {
    if cond { pass += 1; print("ok   \(name)") } else { fail += 1; print("FAIL \(name) \(detail)") }
}

check("keyUp 放行（避免同一按键处理两次）", c.handle(ev("a", 0, [], .keyUp), client: m) == false)

let r2 = c.handle(ev("n", 45), client: m)
check("字母 keyDown 进组词", r2 == true, "ret=\(r2)")
check("预编辑含 n", m.marked.contains("n"), "marked=\(m.marked)")

check("Cmd+S 放行（快捷键不被吞）", c.handle(ev("s", 1, [.command]), client: m) == false)
check("Ctrl+A 放行", c.handle(ev("a", 0, [.control]), client: m) == false)

_ = c.handle(ev("", 56, [.shift]), client: m)              // Shift 按下
_ = c.handle(ev("", 56, [], .flagsChanged), client: m)     // Shift 松开（空按）
let r5 = c.handle(ev("a", 0), client: m)
check("Shift 空按切到英文（字母放行）", r5 == false, "ret=\(r5)")

_ = c.handle(ev("", 56, [.shift]), client: m)
_ = c.handle(ev("", 56, [], .flagsChanged), client: m)
let r6 = c.handle(ev("a", 0), client: m)
check("再空按切回中文（字母被处理）", r6 == true, "ret=\(r6)")

_ = c.handle(ev("", 56, [.shift]), client: m)              // Shift 按下
_ = c.handle(ev("a", 0, [.shift]), client: m)              // Shift+A
_ = c.handle(ev("", 56, [], .flagsChanged), client: m)     // Shift 松开
let r7 = c.handle(ev("b", 11), client: m)
check("Shift+字母不误切换（仍中文）", r7 == true, "ret=\(r7)")

// Option+Backspace（⌥⌫）：组词中按音节回退（三端统一，2026-10-31）。
// 先 Esc 清空上面的残余组词，再输入 nihao → ⌥⌫ 应回退到 ni。
_ = c.handle(ev("", 53), client: m) // Esc
for (ch, code) in [("n", 45), ("i", 34), ("h", 4), ("a", 0), ("o", 31)] {
    _ = c.handle(ev(ch, UInt16(code)), client: m)
}
check("Option+Backspace 前组词含 hao", m.marked.contains("hao"), "marked=\(m.marked)")
let r8 = c.handle(ev("", 51, [.option]), client: m)
check("Option+Backspace 按音节回退（消费）", r8 == true, "ret=\(r8)")
check("Option+Backspace 回退到 ni", m.marked.contains("ni") && !m.marked.contains("hao"), "marked=\(m.marked)")

// 修饰键映射（2026-10-31 用户要求）：Command↔Win/Super、Control↔Ctrl、
// Option↔Alt。组词中 Backspace/Delete 的优先级：Cmd/Ctrl/Shift 全部删除，
// Option 按音节回退，无修饰逐字符回退；Delete 与 Backspace 同语义。
func typeNihao() {
    _ = c.handle(ev("", 53), client: m) // Esc 清空
    for (ch, code) in [("n", 45), ("i", 34), ("h", 4), ("a", 0), ("o", 31)] {
        _ = c.handle(ev(ch, UInt16(code)), client: m)
    }
}

typeNihao()
let r9 = c.handle(ev("", 51, [.command]), client: m) // Cmd+Backspace
check("Cmd+Backspace 全部删除（消费）", r9 == true, "ret=\(r9)")
check("Cmd+Backspace 后组词为空", m.marked.isEmpty, "marked=\(m.marked)")

typeNihao()
let r10 = c.handle(ev("", 117, [.control]), client: m) // Ctrl+ForwardDelete
check("Ctrl+Delete 全部删除（消费）", r10 == true, "ret=\(r10)")
check("Ctrl+Delete 后组词为空", m.marked.isEmpty, "marked=\(m.marked)")

typeNihao()
let r11 = c.handle(ev("", 117), client: m) // 普通 ForwardDelete
check("普通 Delete 逐字符回退（消费）", r11 == true, "ret=\(r11)")
check("普通 Delete 只删一个字符", m.marked.contains("ha") && !m.marked.contains("hao"), "marked=\(m.marked)")

typeNihao()
let r12 = c.handle(ev("", 117, [.option]), client: m) // Option+ForwardDelete
check("Option+Delete 按音节回退（消费）", r12 == true, "ret=\(r12)")
check("Option+Delete 回退到 ni", m.marked.contains("ni") && !m.marked.contains("hao"), "marked=\(m.marked)")

// 联想态 Backspace：三端统一为「收起联想并消费」（2026-10-31）。
// 用 zhongguo → 中国 进入联想（词典有「中国共产党」等续接词）。
func typeZhongguo() {
    _ = c.handle(ev("", 53), client: m) // Esc 清空
    for (ch, code) in [("z", 6), ("h", 4), ("o", 31), ("n", 45), ("g", 5),
                        ("g", 5), ("u", 32), ("o", 31)] {
        _ = c.handle(ev(ch, UInt16(code)), client: m)
    }
}
typeZhongguo()
_ = c.handle(ev("", 49), client: m) // 空格选首选（中国），进入联想
let r13 = c.handle(ev("", 51), client: m) // 联想态 Backspace
check("联想态 Backspace 收起联想并消费", r13 == true, "ret=\(r13)")

// Shift+PageUp 组词中不翻页（放行给宿主滚动）；PageUp/PageDown 无论 Shift
// 都翻页只适用于联想态（三端一致，2026-10-31）。
typeNihao()
let r14 = c.handle(ev("", 116, [.shift]), client: m)
check("Shift+PageUp 组词中放行", r14 == false, "ret=\(r14)")

// Ctrl+Shift+U 是宿主快捷键：mac 按 rawChars "U" 不匹配 Ctrl+U，放行。
typeNihao()
let shiftU = NSEvent.keyEvent(with: .keyDown, location: .zero,
                              modifierFlags: [.control, .shift], timestamp: 0,
                              windowNumber: 0, context: nil, characters: "U",
                              charactersIgnoringModifiers: "U", isARepeat: false,
                              keyCode: 32)!
let r15 = c.handle(shiftU, client: m)
check("Ctrl+Shift+U 放行（不清空组词）", r15 == false, "ret=\(r15)")

// 断言用例数未减少：删掉一条断言会让回归测试静默变弱（2026-10-31）。
// 新增/删除断言时同步更新 expectedChecks。
let expectedChecks = 22
if pass != expectedChecks {
    fail += 1
    print("FAIL 用例数量 \(pass) != \(expectedChecks)（删/加断言需同步更新）")
}

print("\n结果: pass=\(pass) fail=\(fail)")
exit(fail == 0 ? 0 : 1)
