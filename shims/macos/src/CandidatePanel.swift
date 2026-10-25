// CandidatePanel.swift：macOS 原生候选窗（AppKit 自绘）。
//
// 架构决策（2026-10-25，用户拍板）：macOS 壳从「daemon 渲染 PNG 贴图」
// 切换到壳内原生绘制。理由：
//   1. 消除跨进程像素管线（daemon 栅格化+PNG+base64+JSON+socket+壳解码
//      ≈5ms/帧 → NSView 直接绘制，Retina 由系统自动处理，无需 scale 逻辑）；
//   2. 系统原生能力回归：NSColor 动态色自动跟随深浅色、系统字体渲染、
//      辅助功能（VoiceOver 可读候选）、鼠标点选候选（贴图方案的
//      ignoresMouseEvents=true 曾是功能缺口）；
//   3. daemon 渲染管线的一处皮肤状态机缺陷（WithDarkMode 漏拷配色）曾
//      导致深色键帽全坏且单测未拦——原生方案不存在跨进程皮肤状态机。
// 代价：macOS 与 Windows/Linux 的候选窗视觉从此分叉（后两者继续用
// daemon renderer 贴图），视觉改动需要双端同步；daemon renderer 与
// candidateImage IPC 保留，作为 Win/Linux 渲染路径与 macOS 回退。
//
// 视觉复刻 renderer.Skin 当前设计（键帽序号 + 首选蓝胶囊 + 学习标记），
// 色值与 renderer/renderer.go 保持一致，浅/深用动态色自动切换。

import AppKit

// MARK: - 主题色（与 renderer.DefaultSkinFor 色值一一对应）

private struct CandidateTheme {
    let panelBg: NSColor
    let panelBorder: NSColor
    let text: NSColor
    let highlightBg: NSColor
    let highlightText: NSColor
    let chipBg: NSColor
    let chipBorder: NSColor
    let chipDigit: NSColor
    let highlightChipBg: NSColor
    let pageColor: NSColor
    let userMark: NSColor
    let divider: NSColor
    let chevron: NSColor

    static func theme(dark: Bool) -> CandidateTheme {
        if dark {
            return CandidateTheme(
                panelBg: NSColor(srgbRed: 0x2C / 255.0, green: 0x2C / 255.0, blue: 0x2E / 255.0, alpha: 0xF5 / 255.0),
                panelBorder: NSColor(srgbRed: 1, green: 1, blue: 1, alpha: 0x24 / 255.0),
                text: .white,
                highlightBg: NSColor(srgbRed: 0x00 / 255.0, green: 0x66 / 255.0, blue: 0xEB / 255.0, alpha: 1),
                highlightText: .white,
                chipBg: NSColor(srgbRed: 0x5A / 255.0, green: 0x5A / 255.0, blue: 0x5E / 255.0, alpha: 1),
                chipBorder: NSColor(srgbRed: 1, green: 1, blue: 1, alpha: 0x40 / 255.0),
                chipDigit: .white,
                highlightChipBg: NSColor(white: 1, alpha: 0x45 / 255.0),
                pageColor: NSColor(srgbRed: 0x98 / 255.0, green: 0x98 / 255.0, blue: 0x9F / 255.0, alpha: 0xB8 / 255.0),
                userMark: NSColor(srgbRed: 0x30 / 255.0, green: 0xD1 / 255.0, blue: 0x58 / 255.0, alpha: 1),
                divider: NSColor(white: 1, alpha: 0x1E / 255.0),
                chevron: NSColor(srgbRed: 0x98 / 255.0, green: 0x98 / 255.0, blue: 0x9F / 255.0, alpha: 0xB8 / 255.0)
            )
        }
        return CandidateTheme(
            panelBg: NSColor(srgbRed: 0xF6 / 255.0, green: 0xF6 / 255.0, blue: 0xF8 / 255.0, alpha: 0xF6 / 255.0),
            panelBorder: NSColor(srgbRed: 0, green: 0, blue: 0, alpha: 0x1A / 255.0),
            text: NSColor(srgbRed: 0x1D / 255.0, green: 0x1D / 255.0, blue: 0x1F / 255.0, alpha: 1),
            highlightBg: NSColor(srgbRed: 0x00 / 255.0, green: 0x7A / 255.0, blue: 0xFF / 255.0, alpha: 1),
            highlightText: .white,
            chipBg: .white,
            chipBorder: NSColor(srgbRed: 0, green: 0, blue: 0, alpha: 0x3C / 255.0),
            chipDigit: NSColor(srgbRed: 0x1D / 255.0, green: 0x1D / 255.0, blue: 0x1F / 255.0, alpha: 1),
            highlightChipBg: NSColor(white: 1, alpha: 0x45 / 255.0),
            pageColor: NSColor(srgbRed: 0x8E / 255.0, green: 0x8E / 255.0, blue: 0x93 / 255.0, alpha: 0xD0 / 255.0),
            userMark: NSColor(srgbRed: 0x34 / 255.0, green: 0xC7 / 255.0, blue: 0x59 / 255.0, alpha: 1),
            divider: NSColor(srgbRed: 0, green: 0, blue: 0, alpha: 0x14 / 255.0),
            chevron: NSColor(srgbRed: 0x8E / 255.0, green: 0x8E / 255.0, blue: 0x93 / 255.0, alpha: 0xD0 / 255.0)
        )
    }
}

// MARK: - 候选窗视图

final class CandidatePanelView: NSView {
    struct Item {
        let text: String
        let fromUser: Bool
    }

    private var items: [Item] = []
    private var page = 0
    private var pageCount = 0
    /// 点击命中的候选回调（index 即数字序号-1，等于 daemon select 的候选索引）。
    var onSelect: ((Int) -> Void)?

    // 排版参数（pt），与 renderer.renderBar 保持一致
    private enum Layout {
        static let fontSize: CGFloat = 15
        static let indexFontSize: CGFloat = 12      // 主字号 80%
        static let padding: CGFloat = 7
        static let firstPadLeft: CGFloat = 9
        static let itemPadX: CGFloat = 7
        static let gap: CGFloat = 11
        static let lineSpacing: CGFloat = 4
        static let maxWidth: CGFloat = 720
        static let maxRows = 3
        static let chevronBoxW: CGFloat = 20
        static let chipPadX: CGFloat = 4
        static let chipPadY: CGFloat = 2
        static let chipRadius: CGFloat = 3
        static let panelRadius: CGFloat = 8
        static let pillMargin: CGFloat = 2
    }

    private let textFont = NSFont.systemFont(ofSize: Layout.fontSize)
    private let indexFont = NSFont.systemFont(ofSize: Layout.indexFontSize)

    // 每项的命中区域（候选索引，0 起），draw 布局时更新
    private var hitAreas: [(rect: NSRect, index: Int)] = []
    private var contentHeight: CGFloat = 0

    override var isFlipped: Bool { true }

    /// 用组词状态刷新内容；返回内容尺寸（pt）。items 为空时返回 .zero。
    func layout(candidates: [Item], page: Int, pageCount: Int) -> NSSize {
        self.items = candidates
        self.page = page
        self.pageCount = pageCount
        let size = contentSize()
        frame.size = size
        needsDisplay = true
        return size
    }

    // MARK: 布局

    private struct Row {
        var frames: [(rect: NSRect, index: Int)] = []
        var width: CGFloat = 0
    }

    /// 计算一行内每项的布局；返回该行宽度与各行项 rect（左上角坐标系，pt）。
    private func layoutRows() -> (rows: [Row], lineHeight: CGFloat, textAscent: CGFloat) {
        let theme = CandidateTheme.theme(dark: isDark)
        _ = theme
        let ascent = textFont.ascender
        let descent = -textFont.descender
        let textH = ceil(ascent + descent)
        let lineH = textH + Layout.lineSpacing
        // 单项布局：键帽(可选) + 文本 + 学习标记
        struct Piece {
            let indexText: String?
            let text: String
            let fromUser: Bool
            let highlight: Bool
            let padX: CGFloat
        }
        var pieces: [Piece] = []
        for (i, item) in items.enumerated() {
            pieces.append(Piece(
                indexText: String(i + 1),
                text: item.text,
                fromUser: item.fromUser,
                highlight: i == 0,
                padX: i == 0 ? Layout.firstPadLeft : Layout.itemPadX
            ))
        }
        // 多页时附页码（按简单项处理，无键帽）
        var rows: [Row] = []
        var next = 0
        let maxContent = Layout.maxWidth - Layout.padding * 2 - Layout.chevronBoxW
        var rowFrames: [(rect: NSRect, index: Int)] = []
        var rowW: CGFloat = 0
        var y: CGFloat = Layout.padding
        func newRow() {
            rows.append(Row(frames: rowFrames, width: rowW))
            rowFrames = []
            rowW = 0
            y += lineH
        }
        while next < pieces.count, rows.count < Layout.maxRows {
            let p = pieces[next]
            let idxW = p.indexText.map { ceil(($0 as NSString).size(withAttributes: [.font: indexFont]).width) } ?? 0
            let chipW = idxW + Layout.chipPadX * 2
            let textW = ceil((p.text as NSString).size(withAttributes: [.font: textFont]).width)
            let markW: CGFloat = p.fromUser ? ceil(("*" as NSString).size(withAttributes: [.font: textFont]).width) + 2 : 0
            let boxW = p.padX * 2 + chipW + max(3, idxW * 45 / 100) + textW + markW
            let add = (rowFrames.isEmpty ? 0 : Layout.gap) + boxW
            if !rowFrames.isEmpty, rowW + add > maxContent {
                newRow()
                continue
            }
            let x = Layout.padding + rowW
            rowFrames.append((NSRect(x: x, y: y, width: boxW, height: textH), next))
            rowW += add
            next += 1
        }
        if !rowFrames.isEmpty { newRow() }
        return (rows, lineH, ascent)
    }

    private func contentSize() -> NSSize {
        let (rows, lineH, _) = layoutRows()
        var width: CGFloat = 0
        for row in rows {
            let w = row.width + Layout.padding * 2 + Layout.chevronBoxW
            width = max(width, w)
        }
        contentHeight = Layout.padding * 2 + CGFloat(rows.count) * lineH - Layout.lineSpacing
        return NSSize(width: ceil(max(width, 60)), height: ceil(max(contentHeight, 20)))
    }

    // MARK: 绘制

    private var isDark: Bool {
        effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
    }

    override func draw(_ dirtyRect: NSRect) {
        let theme = CandidateTheme.theme(dark: isDark)
        let bounds = self.bounds

        // 面板圆角底 + 1px 边
        let panelPath = NSBezierPath(roundedRect: bounds, xRadius: Layout.panelRadius, yRadius: Layout.panelRadius)
        theme.panelBg.setFill()
        panelPath.fill()
        theme.panelBorder.setStroke()
        panelPath.lineWidth = 1
        panelPath.stroke()

        let (rows, lineH, ascent) = layoutRows()
        let idxAscent = indexFont.ascender
        let idxDescent = -indexFont.descender
        let idxH = ceil(idxAscent + idxDescent)
        let textH = ceil(textFont.ascender - textFont.descender)
        let panelR = Layout.panelRadius

        hitAreas = []
        var y: CGFloat = Layout.padding
        for row in rows {
            var x = Layout.padding
            for (i, frame) in row.frames.enumerated() {
                let item = items[frame.index]
                let p = i == 0
                // 首选：蓝色高亮胶囊（圆角承接面板圆角）
                if p {
                    let box = frame.rect.insetBy(dx: -Layout.pillMargin, dy: -Layout.pillMargin)
                    let leftR = max(panelR - Layout.pillMargin, 4)
                    let pillPath = NSBezierPath(roundedRect: box, xRadius: leftR, yRadius: leftR)
                    theme.highlightBg.setFill()
                    pillPath.fill()
                }

                let padX = p ? Layout.firstPadLeft : Layout.itemPadX
                var curX = frame.rect.minX + padX

                // 序号键帽：双层填充（外边框色 + 内缩底色），数字高亮时用半透白
                let idxText = String(frame.index + 1)
                let idxAttrs: [NSAttributedString.Key: Any] = [
                    .font: indexFont,
                    .foregroundColor: p ? theme.highlightText : theme.chipDigit,
                ]
                let idxSize = (idxText as NSString).size(withAttributes: [.font: indexFont])
                let idxW = ceil(idxSize.width)
                let idxX = curX + Layout.chipPadX
                let chipTop = y + (textH - idxH) / 2 - Layout.chipPadY
                let chipRect = NSRect(x: curX, y: chipTop, width: idxW + Layout.chipPadX * 2, height: idxH + Layout.chipPadY * 2)
                let chipBg = p ? theme.highlightChipBg : theme.chipBg
                if p {
                    // 蓝胶囊内：单层半透白键帽，无边框
                    let path = NSBezierPath(roundedRect: chipRect, xRadius: Layout.chipRadius, yRadius: Layout.chipRadius)
                    chipBg.setFill()
                    path.fill()
                } else {
                    let outer = NSBezierPath(roundedRect: chipRect, xRadius: Layout.chipRadius, yRadius: Layout.chipRadius)
                    theme.chipBorder.setFill()
                    outer.fill()
                    let inner = chipRect.insetBy(dx: 1, dy: 1)
                    if inner.width > 0, inner.height > 0 {
                        let innerR = max(Layout.chipRadius - 1, 1)
                        let path = NSBezierPath(roundedRect: inner, xRadius: innerR, yRadius: innerR)
                        theme.chipBg.setFill()
                        path.fill()
                    }
                }
                (idxText as NSString).draw(at: NSPoint(x: idxX, y: chipTop + Layout.chipPadY + (idxH - ceil(idxSize.height)) / 2), withAttributes: idxAttrs)
                curX = chipRect.maxX + max(3, idxW * 45 / 100)

                // 候选文本
                let textAttrs: [NSAttributedString.Key: Any] = [
                    .font: textFont,
                    .foregroundColor: p ? theme.highlightText : theme.text,
                ]
                (item.text as NSString).draw(at: NSPoint(x: curX, y: y + (textH + Layout.lineSpacing - Layout.lineSpacing) / 2 + (ascent - ceil(textFont.ascender))), withAttributes: textAttrs)
                let textW = ceil((item.text as NSString).size(withAttributes: [.font: textFont]).width)
                curX += textW

                // 学习标记 *
                if item.fromUser {
                    (("*" as NSString).draw(at: NSPoint(x: curX + 1, y: y + (ascent - ceil(textFont.ascender))), withAttributes: [
                        .font: textFont,
                        .foregroundColor: theme.userMark,
                    ]))
                    curX += ceil(("*" as NSString).size(withAttributes: [.font: textFont]).width) + 2
                }

                hitAreas.append((frame.rect, frame.index))
                x = frame.rect.maxX + Layout.gap
                _ = x
            }
            y += lineH
        }

        // 右侧折叠区：分割线 + chevron（与贴图版一致）
        let width = bounds.width
        let dividerX = width - Layout.chevronBoxW + 2
        let divH = textH * 55 / 100
        let divY0 = (bounds.height - divH) / 2
        theme.divider.setFill()
        NSRect(x: dividerX, y: bounds.height - divY0 - divH, width: 1, height: divH).fill()
        let chevX = dividerX + (Layout.chevronBoxW - 2) / 2
        let chevY = bounds.height / 2
        drawChevron(x: chevX, y: chevY, arm: 3, color: theme.chevron)

        // 页码指示（多页时，右下角简化样式）
        if pageCount > 1 {
            let text = "\(page + 1)/\(pageCount)"
            let attrs: [NSAttributedString.Key: Any] = [.font: indexFont, .foregroundColor: theme.pageColor]
            (text as NSString).draw(at: NSPoint(x: bounds.width - Layout.chevronBoxW - 34, y: bounds.height - Layout.padding - 12), withAttributes: attrs)
        }
    }

    private func drawChevron(x: CGFloat, y: CGFloat, arm: CGFloat, color: NSColor) {
        let path = NSBezierPath()
        path.move(to: NSPoint(x: x - arm, y: y + arm * 0.7))
        path.line(to: NSPoint(x: x, y: y - arm * 0.3))
        path.line(to: NSPoint(x: x + arm, y: y + arm * 0.7))
        path.lineWidth = 1.5
        path.lineCapStyle = .round
        color.setStroke()
        path.stroke()
    }

    // MARK: 鼠标点选（原生方案红利：贴图版 ignoresMouseEvents 无法实现）

    override func mouseDown(with event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        for (rect, index) in hitAreas where rect.contains(point) {
            onSelect?(index)
            return
        }
    }

    // MARK: 无障碍（贴图方案无法提供，本次回归）

    override func isAccessibilityElement() -> Bool { true }
    override func accessibilityRole() -> NSAccessibility.Role? { .list }
    override func accessibilityLabel() -> String? { "输入法候选词" }
    override func accessibilityValue() -> Any? {
        let list = items.enumerated().map { "\($0.offset + 1) \($0.element.text)" }.joined(separator: "，")
        return list.isEmpty ? nil : list
    }
}

// MARK: - 面板单例管理

/// 全进程共用一个候选窗面板（同旧 imagePanel 的理由：IMK 每个输入上下文
/// 建一个 Controller 实例，实例各自持面板会残留；任何时刻最多一个可见）。
enum CandidatePanelController {
    private static var panel: NSPanel?
    private static var view: CandidatePanelView?
    /// 点击候选的回调，由 Controller 在显示时设置（weak self 由调用方保证）。
    static var onSelect: ((Int) -> Void)?

    /// 显示候选窗。originProvider 在内容尺寸确定后被调用，返回面板左上角
    /// 屏幕坐标（Controller 用它把候选窗钉在光标下方）。
    static func show(candidates: [CandidatePanelView.Item], page: Int, pageCount: Int,
                     originProvider: (NSSize) -> NSPoint) -> Bool {
        guard !candidates.isEmpty else {
            hide()
            return false
        }
        let p = ensurePanel()
        guard let v = view else { return false }
        v.onSelect = { onSelect?($0) }
        let size = v.layout(candidates: candidates, page: page, pageCount: pageCount)
        p.setContentSize(size)
        p.setFrameTopLeftPoint(originProvider(size))
        p.orderFront(nil)
        return true
    }

    static func hide() {
        panel?.orderOut(nil)
        onSelect = nil
    }

    private static func ensurePanel() -> NSPanel {
        if let panel, let view = panel.contentView as? CandidatePanelView {
            _ = view
            return panel
        }
        let p = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 10, height: 10),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        p.level = .popUpMenu
        p.isOpaque = false
        p.backgroundColor = .clear
        // 面板自身接收鼠标以支持点击选词；不抢焦点（nonactivatingPanel），
        // 面板外点击仍落到宿主应用并触发会话提交。
        p.ignoresMouseEvents = false
        p.hasShadow = true
        let v = CandidatePanelView(frame: NSRect(x: 0, y: 0, width: 10, height: 10))
        v.autoresizingMask = [.width, .height]
        p.contentView = v
        panel = p
        view = v
        return p
    }
}
