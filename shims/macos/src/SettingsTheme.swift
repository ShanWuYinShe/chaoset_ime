// SettingsWindow.swift：输入法设置窗口（独立窗口，参考市面输入法：
// 候选栏齿轮 → 打开设置窗口，窗口内为可扩展的设置项列表）。
//
// 输入法进程的激活策略是 .prohibited（IMK 要求，不占 Dock），弹普通
// NSWindow 前需临时切到 .accessory 才能成为 key 窗口正常交互。
// 窗口单例：已打开则前置，关闭仅隐藏（isReleasedWhenClosed=false，
// 否则关闭后静态指针悬空崩溃）。
//
// 2026-10-31：新增「学习数据」区——统计 / 清空 / 导出全部在 GUI 完成，
// 普通用户不需要命令行（`chaoset-userdict` 降级为高级/排障兜底）。
// 数据经 EngineClient 走同一 IPC 通道，daemon 内存与磁盘一次生效。
//
// 2026-10-31：新增「词库」区——「导入词库…」打开随包安装的
// `~/Applications/Chaoset.app`（安装与设置 app）并传 `--import`，
// 由它弹出文件选择并完成合并/备份/原子替换/重启 daemon（入口与 Windows
// 设置窗「词库」区对齐；导入实现仍只有 Setup.app 一份，避免复制粘贴）。
//
// 视觉系统：蓝白配色——浅蓝白底 + 白卡片 + 品牌蓝强调色，
// 自绘开关/胶囊/按钮与左导航统一同一套令牌；深色为深海军蓝。令牌是三端共享
// 设计系统的 macOS 实现（docs/ui-parity.md「设置窗视觉令牌」逐值机器断言，
// 改色值必须三端同步）。
//
// 2026-11-06 架构拆分：设置窗拆分为窗口骨架 / 主题与自绘控件 / 三页视图 /
// @objc target；视觉系统升级把主题令牌与自绘控件抽到
// SettingsTheme.swift（三端共享设计系统的 macOS 实现，docs/ui-parity.md
// 「设置窗视觉令牌」逐值机器断言）。

import AppKit

// 本文件：设置窗视觉令牌（三端共享设计系统的 macOS 实现）+ 自绘基础视图与
// 控件（窗口底/导航列/卡片/开关/胶囊/按钮/焦点环）。令牌色值必须与
// docs/ui-parity.md「设置窗视觉令牌」逐值一致（tools/checkuiparity 断言），
// 改色/改几何要三端同步。窗口骨架与控制器见 SettingsWindow.swift。

// MARK: - 主题令牌（三端共享设计系统）

/// 设置窗视觉令牌：三端（macOS/Windows/Linux）同一套色值与几何，
/// 由 docs/ui-parity.md「设置窗视觉令牌」表 + tools/checkuiparity 逐值断言。
/// 对比度按 WCAG AA 选值（正文 ≥4.5:1），不要凭手感调浅——调浅会先让
/// accentText/accentStrong 上的文字跌破可读线。
enum SettingsTheme {
    /// 令牌色：light/dark 两种 sRGB 十六进制，随系统外观自动切换。
    static func dynamic(light: UInt32, dark: UInt32,
                        lightAlpha: CGFloat = 1, darkAlpha: CGFloat = 1) -> NSColor {
        NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            return rgb(isDark ? dark : light, alpha: isDark ? darkAlpha : lightAlpha)
        }
    }

    static func rgb(_ value: UInt32, alpha: CGFloat = 1) -> NSColor {
        NSColor(srgbRed: CGFloat((value >> 16) & 0xFF) / 255.0,
                green: CGFloat((value >> 8) & 0xFF) / 255.0,
                blue: CGFloat(value & 0xFF) / 255.0,
                alpha: alpha)
    }

    // 底色与分层（蓝白配色，值来自 docs/ui-parity.md「设置窗视觉令牌」表）
    static let windowBg = dynamic(light: 0xF4F7FB, dark: 0x101A2C)
    static let navBg = dynamic(light: 0xEAF1F9, dark: 0x0C1626)
    static let cardBg = dynamic(light: 0xFFFFFF, dark: 0x1A2740)
    static let cardBorder = dynamic(light: 0xD9E4F0, dark: 0x2D3E5C)
    static let divider = dynamic(light: 0xE4EDF6, dark: 0x243552)
    // 文字
    static let textPrimary = dynamic(light: 0x14213D, dark: 0xF2F6FC)
    static let textSecondary = dynamic(light: 0x4A5A70, dark: 0xAFBFD6)
    static let textTertiary = dynamic(light: 0x7A8899, dark: 0x7E90AC)
    // 强调色（蓝，与候选窗高亮同色系）
    static let accent = dynamic(light: 0x007AFF, dark: 0x0066EB)
    static let accentStrong = dynamic(light: 0x0066EB, dark: 0x007AFF)
    static let accentPressed = dynamic(light: 0x0052BE, dark: 0x4DA3FF)
    static let accentSoft = dynamic(light: 0xE5F1FF, dark: 0x16345C)
    static let accentText = dynamic(light: 0x0066EB, dark: 0x7FB4FF)
    // 控件
    static let controlBg = dynamic(light: 0xF0F5FB, dark: 0x223353)
    static let controlHover = dynamic(light: 0xE3EDF8, dark: 0x2C4165)
    static let controlBorder = dynamic(light: 0xC9D9EB, dark: 0x3A5074)
    static let controlText = dynamic(light: 0x14213D, dark: 0xF2F6FC)
    static let knob = dynamic(light: 0xFFFFFF, dark: 0xF2F6FC)
    static let danger = dynamic(light: 0xD32F2F, dark: 0xFF7B72)
    static let onAccent = dynamic(light: 0xFFFFFF, dark: 0xFFFFFF)
    static let shadow = dynamic(light: 0x1B4A80, dark: 0x000000,
                                lightAlpha: 0.12, darkAlpha: 0.38)

    // 几何（pt，三端同一口径）
    static let windowSize = NSSize(width: 640, height: 560)
    static let navWidth: CGFloat = 148
    static let navItemWidth: CGFloat = 112
    static let navItemHeight: CGFloat = 34
    static let contentTop: CGFloat = 44
    static let contentLeftGap: CGFloat = 24
    static let contentRight: CGFloat = 24
    static let footerBottom: CGFloat = 16
    static let cardRadius: CGFloat = 14
    static let cardPad: CGFloat = 18
    static let cardGap: CGFloat = 16
    static let cardTitleTop: CGFloat = 16
    static let cardContentTop: CGFloat = 50
    static let rowHeight: CGFloat = 36
    static let controlRadius: CGFloat = 9
    static let buttonHeight: CGFloat = 32
    static let switchWidth: CGFloat = 38
    static let switchHeight: CGFloat = 22
}

// MARK: - 自绘基础视图

/// 窗口底色：显式画 windowBg，保证窗口/离屏截图与主题切换一致。
final class SettingsBackgroundView: NSView {
    override func draw(_ dirtyRect: NSRect) {
        SettingsTheme.windowBg.setFill()
        dirtyRect.fill()
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }
}

/// 左侧导航列底色：比窗口底略深一档，形成浅蓝分栏（动态色需随外观重绘，
/// 层背景的 CGColor 不会自动跟随深色，所以用自绘而不是 layer.backgroundColor）。
final class SettingsNavColumnView: NSView {
    override func draw(_ dirtyRect: NSRect) {
        SettingsTheme.navBg.setFill()
        dirtyRect.fill()
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }
}

/// 卡片内容条目：行/说明需要撑满卡片内容宽（左对齐、右侧控件贴边），
/// 按钮/胶囊/统计行按内容自然宽度排布（不该被拉成长条）。
struct SettingsCardEntry {
    let view: NSView
    let fullWidth: Bool

    static func full(_ view: NSView) -> SettingsCardEntry {
        SettingsCardEntry(view: view, fullWidth: true)
    }

    static func natural(_ view: NSView) -> SettingsCardEntry {
        SettingsCardEntry(view: view, fullWidth: false)
    }
}

/// 卡片容器：卡片底 + 1pt 描边 + 柔和阴影；标题与内容由约束排出，
/// 三页的所有分组都用它（三端同构）。
final class SettingsCardView: NSView {
    private let titleLabel = NSTextField(labelWithString: "")
    private let contentStack = NSStackView()

    init(title: String, content: [SettingsCardEntry]) {
        super.init(frame: .zero)
        wantsLayer = true
        layer?.cornerRadius = SettingsTheme.cardRadius
        layer?.borderWidth = 1
        layer?.shadowRadius = 12
        layer?.shadowOffset = CGSize(width: 0, height: -2)
        layer?.masksToBounds = false

        titleLabel.stringValue = title
        titleLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(titleLabel)

        contentStack.orientation = .vertical
        contentStack.alignment = .leading
        contentStack.spacing = 8
        contentStack.translatesAutoresizingMaskIntoConstraints = false
        for entry in content {
            entry.view.translatesAutoresizingMaskIntoConstraints = false
            contentStack.addArrangedSubview(entry.view)
            if entry.fullWidth {
                entry.view.widthAnchor.constraint(equalTo: contentStack.widthAnchor).isActive = true
            }
        }
        addSubview(contentStack)

        NSLayoutConstraint.activate([
            titleLabel.topAnchor.constraint(equalTo: topAnchor, constant: SettingsTheme.cardTitleTop),
            titleLabel.leadingAnchor.constraint(equalTo: leadingAnchor, constant: SettingsTheme.cardPad),
            titleLabel.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor,
                                                 constant: -SettingsTheme.cardPad),
            contentStack.topAnchor.constraint(equalTo: topAnchor, constant: SettingsTheme.cardContentTop),
            contentStack.leadingAnchor.constraint(equalTo: leadingAnchor, constant: SettingsTheme.cardPad),
            contentStack.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -SettingsTheme.cardPad),
            contentStack.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -SettingsTheme.cardPad),
        ])
        updateAppearance()
    }

    required init?(coder: NSCoder) { nil }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        updateAppearance()
    }

    private func updateAppearance() {
        effectiveAppearance.performAsCurrentDrawingAppearance {
            layer?.backgroundColor = SettingsTheme.cardBg.cgColor
            layer?.borderColor = SettingsTheme.cardBorder.cgColor
            layer?.shadowColor = SettingsTheme.shadow.cgColor
            titleLabel.textColor = SettingsTheme.textSecondary
        }
    }
}

// MARK: - 自绘控件（统一蓝白令牌；不用系统默认 accent）

/// 焦点环：accent 1.5pt 圆角描边（替代系统虚线/系统蓝 focus ring）。
private func drawSettingsFocusRing(in rect: NSRect, radius: CGFloat) {
    let ring = NSBezierPath(roundedRect: rect.insetBy(dx: 0.75, dy: 0.75),
                            xRadius: radius, yRadius: radius)
    SettingsTheme.accent.setStroke()
    ring.lineWidth = 1.5
    ring.stroke()
}

/// 自绘开关（词组联想）：轨道 38×22 + 滑钮 16，替代系统 NSSwitch 的
/// 绿色/系统蓝——三端同一形态与色值。
final class WarmSwitch: NSButton {
    private var hovering = false
    private var hoverTracking: NSTrackingArea?

    init() {
        super.init(frame: .zero)
        setButtonType(.pushOnPushOff)
        isBordered = false
        title = ""
        focusRingType = .none
        translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: SettingsTheme.switchWidth),
            heightAnchor.constraint(equalToConstant: SettingsTheme.switchHeight),
        ])
    }

    required init?(coder: NSCoder) { nil }

    override var intrinsicContentSize: NSSize {
        NSSize(width: SettingsTheme.switchWidth, height: SettingsTheme.switchHeight)
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let hoverTracking { removeTrackingArea(hoverTracking) }
        let area = NSTrackingArea(rect: bounds,
                                  options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
                                  owner: self, userInfo: nil)
        addTrackingArea(area)
        hoverTracking = area
    }

    override func mouseEntered(with event: NSEvent) { hovering = true; needsDisplay = true }
    override func mouseExited(with event: NSEvent) { hovering = false; needsDisplay = true }
    override func mouseDown(with event: NSEvent) {
        super.mouseDown(with: event)
        needsDisplay = true
    }
    override func becomeFirstResponder() -> Bool {
        let ok = super.becomeFirstResponder()
        needsDisplay = true
        return ok
    }
    override func resignFirstResponder() -> Bool {
        let ok = super.resignFirstResponder()
        needsDisplay = true
        return ok
    }

    override func draw(_ dirtyRect: NSRect) {
        let on = state == .on
        let trackRect = NSRect(x: 0.5, y: (bounds.height - SettingsTheme.switchHeight) / 2 + 0.5,
                               width: SettingsTheme.switchWidth - 1,
                               height: SettingsTheme.switchHeight - 1)
        let track = NSBezierPath(roundedRect: trackRect, xRadius: 11, yRadius: 11)
        let trackColor: NSColor
        if on {
            trackColor = hovering ? SettingsTheme.accentPressed : SettingsTheme.accent
        } else {
            trackColor = hovering ? SettingsTheme.controlHover : SettingsTheme.controlBg
        }
        trackColor.setFill()
        track.fill()
        SettingsTheme.controlBorder.setStroke()
        track.lineWidth = 1
        track.stroke()

        let knobSize: CGFloat = 16
        let knobX = on ? trackRect.maxX - knobSize - 3 : trackRect.minX + 3
        let knobRect = NSRect(x: knobX, y: trackRect.midY - knobSize / 2,
                              width: knobSize, height: knobSize)
        let knob = NSBezierPath(ovalIn: knobRect)
        SettingsTheme.knob.setFill()
        knob.fill()
        SettingsTheme.controlBorder.withAlphaComponent(0.6).setStroke()
        knob.lineWidth = 1
        knob.stroke()

        if window?.firstResponder === self { drawSettingsFocusRing(in: bounds, radius: 11) }
    }
}

/// 胶囊按钮：左导航项（nav，宽 112×34）与中英状态二选一（choice，高 30）
/// 共用；选中=accentSoft 底 + accent 描边 + accentText 半粗，未选=透明/controlBg。
final class WarmPillButton: NSButton {
    enum Kind { case nav, choice }

    private let kind: Kind
    private(set) var pillSelected = false
    private var hovering = false
    private var hoverTracking: NSTrackingArea?

    init(title: String, kind: Kind) {
        self.kind = kind
        super.init(frame: .zero)
        setButtonType(.momentaryPushIn)
        isBordered = false
        self.title = title
        focusRingType = .none
        translatesAutoresizingMaskIntoConstraints = false
        if kind == .nav {
            NSLayoutConstraint.activate([
                widthAnchor.constraint(equalToConstant: SettingsTheme.navItemWidth),
                heightAnchor.constraint(equalToConstant: SettingsTheme.navItemHeight),
            ])
        } else {
            heightAnchor.constraint(equalToConstant: 30).isActive = true
        }
    }

    required init?(coder: NSCoder) { nil }

    func setPillSelected(_ selected: Bool) {
        guard pillSelected != selected else { return }
        pillSelected = selected
        needsDisplay = true
    }

    private var fontSize: CGFloat { kind == .nav ? 13 : 12.5 }

    override var intrinsicContentSize: NSSize {
        let size = (title as NSString).size(withAttributes: [.font: NSFont.systemFont(ofSize: fontSize)])
        if kind == .nav {
            return NSSize(width: SettingsTheme.navItemWidth, height: SettingsTheme.navItemHeight)
        }
        return NSSize(width: ceil(size.width) + 24, height: 30)
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let hoverTracking { removeTrackingArea(hoverTracking) }
        let area = NSTrackingArea(rect: bounds,
                                  options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
                                  owner: self, userInfo: nil)
        addTrackingArea(area)
        hoverTracking = area
    }

    override func mouseEntered(with event: NSEvent) { hovering = true; needsDisplay = true }
    override func mouseExited(with event: NSEvent) { hovering = false; needsDisplay = true }
    override func mouseDown(with event: NSEvent) {
        super.mouseDown(with: event)
        needsDisplay = true
    }
    override func becomeFirstResponder() -> Bool {
        let ok = super.becomeFirstResponder()
        needsDisplay = true
        return ok
    }
    override func resignFirstResponder() -> Bool {
        let ok = super.resignFirstResponder()
        needsDisplay = true
        return ok
    }

    override func draw(_ dirtyRect: NSRect) {
        let radius: CGFloat = kind == .nav ? 10 : SettingsTheme.controlRadius
        let rect = bounds.insetBy(dx: 0.5, dy: 0.5)
        let path = NSBezierPath(roundedRect: rect, xRadius: radius, yRadius: radius)

        let textColor: NSColor
        let font: NSFont
        if pillSelected {
            SettingsTheme.accentSoft.setFill()
            path.fill()
            SettingsTheme.accent.setStroke()
            path.lineWidth = 1
            path.stroke()
            textColor = SettingsTheme.accentText
            font = .systemFont(ofSize: fontSize, weight: .semibold)
        } else {
            if kind == .choice {
                (hovering ? SettingsTheme.controlHover : SettingsTheme.controlBg).setFill()
                path.fill()
                SettingsTheme.controlBorder.setStroke()
                path.lineWidth = 1
                path.stroke()
            } else if hovering {
                SettingsTheme.controlHover.setFill()
                path.fill()
            }
            textColor = SettingsTheme.textPrimary
            font = .systemFont(ofSize: fontSize, weight: .regular)
        }

        let attributes: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: textColor]
        let text = NSAttributedString(string: title, attributes: attributes)
        let textSize = text.size()
        let textX: CGFloat
        if kind == .nav {
            textX = 14
        } else {
            textX = (bounds.width - textSize.width) / 2
        }
        text.draw(at: NSPoint(x: textX, y: (bounds.height - textSize.height) / 2))

        if window?.firstResponder === self { drawSettingsFocusRing(in: bounds, radius: radius) }
    }
}

/// 普通按钮：主按钮（accentStrong 实底）/ 次按钮（controlBg + 描边）/
/// 危险操作（次按钮形态 + danger 文字）。hover/按下态自带，不用系统按钮
/// 的灰色渐变与系统蓝。
final class WarmButton: NSButton {
    enum Style { case primary, secondary, destructive }

    private let style: Style
    private var hovering = false
    private var pressed = false
    private var hoverTracking: NSTrackingArea?

    init(title: String, style: Style) {
        self.style = style
        super.init(frame: .zero)
        setButtonType(.momentaryPushIn)
        isBordered = false
        self.title = title
        focusRingType = .none
        translatesAutoresizingMaskIntoConstraints = false
        heightAnchor.constraint(equalToConstant: SettingsTheme.buttonHeight).isActive = true
    }

    required init?(coder: NSCoder) { nil }

    override var intrinsicContentSize: NSSize {
        let size = (title as NSString).size(withAttributes: [.font: NSFont.systemFont(ofSize: 12.5)])
        return NSSize(width: ceil(size.width) + 28, height: SettingsTheme.buttonHeight)
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let hoverTracking { removeTrackingArea(hoverTracking) }
        let area = NSTrackingArea(rect: bounds,
                                  options: [.mouseEnteredAndExited, .activeInKeyWindow, .inVisibleRect],
                                  owner: self, userInfo: nil)
        addTrackingArea(area)
        hoverTracking = area
    }

    override func mouseEntered(with event: NSEvent) { hovering = true; needsDisplay = true }
    override func mouseExited(with event: NSEvent) { hovering = false; needsDisplay = true }
    override func mouseDown(with event: NSEvent) {
        pressed = true
        needsDisplay = true
        super.mouseDown(with: event)
        pressed = false
        needsDisplay = true
    }
    override func becomeFirstResponder() -> Bool {
        let ok = super.becomeFirstResponder()
        needsDisplay = true
        return ok
    }
    override func resignFirstResponder() -> Bool {
        let ok = super.resignFirstResponder()
        needsDisplay = true
        return ok
    }

    override func draw(_ dirtyRect: NSRect) {
        let rect = bounds.insetBy(dx: 0.5, dy: 0.5)
        let path = NSBezierPath(roundedRect: rect, xRadius: SettingsTheme.controlRadius,
                                yRadius: SettingsTheme.controlRadius)
        let active = pressed || hovering
        let fill: NSColor
        let textColor: NSColor
        var stroke: NSColor?
        switch style {
        case .primary:
            fill = active ? SettingsTheme.accentPressed : SettingsTheme.accentStrong
            textColor = SettingsTheme.onAccent
        case .secondary:
            fill = active ? SettingsTheme.controlHover : SettingsTheme.controlBg
            textColor = SettingsTheme.controlText
            stroke = SettingsTheme.controlBorder
        case .destructive:
            fill = active ? SettingsTheme.controlHover : SettingsTheme.controlBg
            textColor = SettingsTheme.danger
            stroke = SettingsTheme.controlBorder
        }
        fill.setFill()
        path.fill()
        if let stroke {
            stroke.setStroke()
            path.lineWidth = 1
            path.stroke()
        }
        let attributes: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 12.5, weight: .medium),
            .foregroundColor: textColor,
        ]
        let text = NSAttributedString(string: title, attributes: attributes)
        let size = text.size()
        text.draw(at: NSPoint(x: (bounds.width - size.width) / 2,
                              y: (bounds.height - size.height) / 2))
        if window?.firstResponder === self {
            drawSettingsFocusRing(in: bounds, radius: SettingsTheme.controlRadius)
        }
    }
}

