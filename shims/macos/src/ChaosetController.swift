import AppKit
import InputMethodKit

// ChaosetController 是 IMK 输入法控制器：把系统按键事件翻译成
// 对 chaosetd 的 IPC 调用，并用内联预编辑 + IMKCandidates 候选窗展示。
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
    private var candidateWindow: IMKCandidates?

    // 中英模式：英文模式下按键全部放行（组词/选词/标点映射不介入）。
    // 切换手势为 Shift **空按**：按下先装填，松开时若期间没有其它按键才切换。
    // 这样即便系统把 Shift 的按下与松开都路由进来也只切一次，Shift+A 之类的
    // 组合也不会误切换（旧实现按下/松开各切一次，净效果为不切换）。
    private var enMode = false
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

        // 带 Command/Control/Option 的组合键一律放行（快捷键不能被输入法吞掉）
        let mods = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        if mods.contains(.command) || mods.contains(.control) || mods.contains(.option) {
            return passThrough(client)
        }

        // Shift 空按：按下装填、松开且期间无其它按键时才切换中英
        if Self.shiftKeyCodes.contains(event.keyCode) {
            if mods.contains(.shift) {
                shiftArmed = true
                return true
            }
            if shiftArmed {
                shiftArmed = false
                enMode.toggle()
                current = engine.reset()
                refresh(client)
            }
            return true
        }
        shiftArmed = false // 出现其它按键 → 不是"空按"，取消待判定

        // 英文模式：全部放行（passThrough 会先清掉未上屏的组词）
        if enMode {
            return passThrough(client)
        }

        guard let chars = event.charactersIgnoringModifiers, !chars.isEmpty else {
            return passThrough(client)
        }

        // 退格
        if chars == "\u{7F}" {
            current = engine.backspace()
            refresh(client)
            return true
        }

        // Esc：清空组词并隐藏候选窗
        if event.keyCode == 53 {
            current = engine.reset()
            refresh(client)
            return true
        }

        let hasCandidates = !(current?.candidates.isEmpty ?? true)

        // Shift+空格：切换全角/半角标点模式（优先于首选上屏；
        // daemon 不可达时放行空格）
        if chars == " ", event.modifierFlags.contains(.shift) {
            if engine.toggleHalfWidth() != nil {
                return true
            }
            return passThrough(client)
        }

        // 数字 1-9：选对应候选
        if hasCandidates, chars.count == 1,
           let scalar = chars.unicodeScalars.first,
           scalar.value >= 49, scalar.value <= 57 {
            commit(index: Int(scalar.value - 49), client: client)
            return true
        }

        // 空格 / 回车：首选上屏；无候选时放行
        if (chars == " " || chars == "\r") && hasCandidates {
            commit(index: 0, client: client)
            return true
        }

        // - / =：候选翻页（组词中且有候选时消费；空组词放行原键）
        if (chars == "-" || chars == "=") && hasCandidates {
            current = chars == "-" ? engine.prevPage() : engine.nextPage()
            refresh(client)
            return true
        }

        // 标点键：组词中先上屏首选再出中文标点，空组词直接出中文标点。
        // 组词中的撇号仍是音节分隔符，交回组词流程。
        if chars.count == 1, let scalar = chars.unicodeScalars.first,
           Self.isPunctKey(scalar) {
            if scalar == "'", current?.input.isEmpty == false {
                current = engine.input(chars)
                refresh(client)
                return true
            }
            guard let committed = engine.punct(chars) else {
                return passThrough(client) // daemon 不可达/未知标点：原键放行
            }
            current = nil
            client?.insertText(
                committed,
                replacementRange: NSRange(location: NSNotFound, length: 0)
            )
            refresh(client)
            return true
        }

        // 字母与撇号：进组词流程
        if isCompositionChar(chars) {
            current = engine.input(chars)
            refresh(client)
            return true
        }

        return passThrough(client)
    }

    // 不可处理的按键：组词中先清空再放行，避免预编辑与实文脱节
    private func passThrough(_ client: (any IMKTextInput)?) -> Bool {
        if current?.input.isEmpty == false {
            current = engine.reset()
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

    private func commit(index: Int, client: (any IMKTextInput)?) {
        guard let committed = engine.select(index) else { return }
        current = nil
        client?.insertText(
            committed,
            replacementRange: NSRange(location: NSNotFound, length: 0)
        )
        refresh(client)
    }

    // MARK: - 展示

    private func refresh(_ client: (any IMKTextInput)?) {
        let marked = current?.preedit ?? ""
        client?.setMarkedText(
            marked,
            selectionRange: NSRange(location: 0, length: (marked as NSString).length),
            replacementRange: NSRange(location: NSNotFound, length: 0)
        )
        let hasCandidates = !(current?.candidates.isEmpty ?? true)
        if !hasCandidates {
            imagePanel?.orderOut(nil)
            candidateWindow?.hide()
            return
        }
        // v2 优先：daemon 渲染的统一候选窗贴图；失败回退 IMKCandidates（v1）
        if showImagePanel() {
            candidateWindow?.hide()
            return
        }
        if let window = ensureCandidateWindow() {
            window.update()
        }
    }

    // ---- v2 统一候选窗贴图面板 ----

    private var imagePanel: NSPanel?

    /// 显示 daemon 渲染的候选窗贴图；返回 false 表示不可用（回退 v1）。
    private func showImagePanel() -> Bool {
        guard let image = engine.candidateImage(), let png = image.png,
              let nsImage = NSImage(data: png) else {
            imagePanel?.orderOut(nil)
            return false
        }
        let panel: NSPanel
        if let existing = imagePanel {
            panel = existing
        } else {
            panel = NSPanel(
                contentRect: NSRect(x: 0, y: 0, width: 10, height: 10),
                styleMask: [.borderless, .nonactivatingPanel],
                backing: .buffered,
                defer: false
            )
            panel.level = .floating
            panel.isOpaque = false
            panel.backgroundColor = .clear
            let imageView = NSImageView(frame: NSRect(x: 0, y: 0, width: 10, height: 10))
            imageView.autoresizingMask = [.width, .height]
            imageView.imageScaling = .scaleNone
            panel.contentView = imageView
        }
        let size = nsImage.size
        panel.setContentSize(size)
        (panel.contentView as? NSImageView)?.image = nsImage
        // 定位：跟随鼠标附近（光标精确坐标需 IMK 深度 API，作为已知限制记录）
        let mouse = NSEvent.mouseLocation
        panel.setFrameTopLeftPoint(NSPoint(x: mouse.x + 8, y: mouse.y - 4))
        panel.orderFront(nil)
        return true
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

    override func deactivateServer(_ sender: Any!) {
        current = engine.reset()
        candidateWindow?.hide()
        imagePanel?.orderOut(nil)
    }
}
