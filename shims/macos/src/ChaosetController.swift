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

        let mods = event.modifierFlags.intersection(.deviceIndependentFlagsMask)

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

        let isComposing = !(current?.input.isEmpty ?? true)

        // Esc 键（keyCode 53）：直接全部删除（清空当前组词并关闭候选窗）
        if event.keyCode == 53 {
            if isComposing {
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
                current = engine.reset()
                refresh(client)
                return true // 必须消费，绝不让快捷删除穿透到宿主应用误删文档内容
            }

            // B. 按音节部分删除（整词/音节回退）：Option+Delete (⌥⌫)
            if mods.contains(.option) {
                current = engine.backspaceWord()
                if current?.input.isEmpty == true {
                    current = nil
                }
                refresh(client)
                return true
            }

            // C. 慢慢部分删除（逐字符退格）：普通退格键单按，每按一次退一个拼音字母
            current = engine.backspace()
            if current?.input.isEmpty == true {
                current = nil
            }
            refresh(client)
            return true
        }

        // Ctrl+U：类终端整行清空，组词中直接全部删除
        if isComposing, mods.contains(.control), (rawChars == "u" || rawChars == "\u{15}") {
            current = engine.reset()
            refresh(client)
            return true
        }

        // 带 Command/Control/Option 的系统快捷键（如 Cmd+C/Cmd+V）一律放行
        if mods.contains(.command) || mods.contains(.control) || mods.contains(.option) {
            return passThrough(client)
        }

        guard let chars = event.charactersIgnoringModifiers, !chars.isEmpty else {
            return passThrough(client)
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

        // 回车：组词中不论有无候选，按回车直接将输入的原始拼音上屏，清空组词并消费回车键
        if (chars == "\r" || chars == "\n"), let cur = current, !cur.input.isEmpty {
            let raw = engine.commitRaw() ?? cur.input
            current = nil
            client?.insertText(
                raw,
                replacementRange: NSRange(location: NSNotFound, length: 0)
            )
            refresh(client)
            return true
        }

        // 空格：首选上屏；无候选时放行
        if chars == " " && hasCandidates {
            commit(index: 0, client: client)
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
            Self.hideCandidatePanel()
            candidateWindow?.hide()
            return
        }
        // v2 优先：daemon 渲染的统一候选窗贴图；失败回退 IMKCandidates（v1）
        if showImagePanel(client: client) {
            candidateWindow?.hide()
            return
        }
        if let window = ensureCandidateWindow() {
            window.update()
        }
    }

    // ---- v2 统一候选窗贴图面板 ----

    // 全进程共用一个贴图面板：IMK 会为不同客户端/输入上下文创建多个控制器
    // 实例，若每个实例各持有一个 NSPanel，而会话结束时系统只发
    // commitComposition:（点击文本框外）而没有 deactivateServer:，旧面板就会
    // 永远留在屏幕上、点不掉也关不掉（2026-10-21 实测同时残留多个候选窗）。
    // 改为共用面板 + 在全部会话结束路径收起，保证任何时刻最多一个可见候选窗。
    private static var imagePanel: NSPanel?

    // 候选窗与光标行的间距（pt）：太贴会压住行下沿的下伸部，太远又像飘着。
    private static let candidateGap: CGFloat = 6

    private static func hideCandidatePanel() {
        imagePanel?.orderOut(nil)
    }

    /// 懒创建全进程共用的贴图面板（首次显示时建一次）。
    private static func ensureImagePanel() -> NSPanel {
        if let existing = imagePanel { return existing }
        let panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 10, height: 10),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        panel.level = .popUpMenu
        panel.isOpaque = false
        panel.backgroundColor = .clear
        // 候选窗只负责展示：不拦截鼠标，点击应落到宿主应用并触发会话提交
        panel.ignoresMouseEvents = true
        // 贴图本身已含 1px 细边框与圆角，系统投影按贴图 alpha 生成，与系统候选窗观感一致
        panel.hasShadow = true
        let imageView = NSImageView(frame: NSRect(x: 0, y: 0, width: 10, height: 10))
        imageView.autoresizingMask = [.width, .height]
        imageView.imageScaling = .scaleNone
        panel.contentView = imageView
        imagePanel = panel
        return panel
    }

    /// 显示 daemon 渲染的候选窗贴图；返回 false 表示不可用（回退 v1）。
    /// client 用于取插入点矩形，取不到时退回鼠标位置。
    private func showImagePanel(client: (any IMKTextInput)?) -> Bool {
        // 深色/浅色皮肤跟随候选窗自身的外观（继承自 App，即系统外观）。
        // 倍率取光标所在屏幕的 backingScaleFactor，**只传 1 或 2**：daemon 对其它
        // 值一律按 1 渲染，传原值反而会让 NSImage.size 与像素对不上。
        let panel = Self.ensureImagePanel()
        let dark = Self.isDarkAppearance(panel.effectiveAppearance)
        let screen = Self.screenForCursor(client) ?? panel.screen ?? NSScreen.main
        let scale = (screen?.backingScaleFactor ?? 1) >= 1.5 ? 2 : 1
        guard let image = engine.candidateImage(dark: dark, scale: scale), let png = image.png,
              let nsImage = NSImage(data: png) else {
            Self.hideCandidatePanel()
            return false
        }
        // daemon 已按 scale 倍像素渲染，点尺寸 = 像素/scale（Retina 下才不发虚）
        let pixelScale = CGFloat(max(1, image.scale))
        let size = NSSize(width: CGFloat(image.width) / pixelScale,
                          height: CGFloat(image.height) / pixelScale)
        // NSImage.size 显式设成点尺寸：图片自带 72dpi，不设会被按像素当点
        nsImage.size = size
        panel.setContentSize(size)
        if let view = panel.contentView as? NSImageView {
            view.image = nsImage
            view.imageScaling = .scaleAxesIndependently
        }
        panel.setFrameTopLeftPoint(
            Self.candidateOrigin(size: size, client: client, screen: screen)
        )
        panel.orderFront(nil)
        return true
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

    /// 是否深色外观（候选窗贴图按此选皮肤）。
    /// 取候选窗自身的 effectiveAppearance：面板是 NSAppearanceCustomization，
    /// 未显式设 appearance 时继承 App（即系统）外观。NSScreen 没有该属性。
    private static func isDarkAppearance(_ appearance: NSAppearance?) -> Bool {
        let resolved = appearance ?? NSApplication.shared.effectiveAppearance
        return resolved.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
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
    // 会话状态并收起候选窗，否则 v2 贴图面板会残留在屏幕上且无法关闭。
    override func commitComposition(_ sender: Any!) {
        current = engine.reset()
        Self.hideCandidatePanel()
        candidateWindow?.hide()
    }

    override func activateServer(_ sender: Any!) {
        // 新的输入上下文：先收掉上一个上下文可能残留的候选窗
        Self.hideCandidatePanel()
        candidateWindow?.hide()
    }

    override func deactivateServer(_ sender: Any!) {
        current = engine.reset()
        Self.hideCandidatePanel()
        candidateWindow?.hide()
    }

    deinit {
        Self.hideCandidatePanel()
        candidateWindow?.hide()
    }
}