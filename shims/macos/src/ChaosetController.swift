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
        guard let chars = event.charactersIgnoringModifiers, !chars.isEmpty else {
            return passThrough(sender as? any IMKTextInput)
        }
        let client = sender as? any IMKTextInput

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
