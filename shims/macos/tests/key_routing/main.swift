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
    func insertText(_ string: Any!, replacementRange: NSRange) { inserted.append(String(describing: string)) }
    func setMarkedText(_ string: Any!, selectionRange: NSRange, replacementRange: NSRange) {
        marked = String(describing: string)
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

print("\n结果: pass=\(pass) fail=\(fail)")
exit(fail == 0 ? 0 : 1)
