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
// 代价：macOS 与 Windows/Linux 的候选窗视觉从此分叉（各自原生自绘），
// 视觉改动需要双端同步。三端壳都不再使用 daemon 贴图后，daemon 侧的
// PNG 渲染链路（renderer 包 + candidateImage IPC）已于 2026-10-27 删除。
//
// 视觉复刻 skin.DefaultSkinFor 的设计（键帽序号 + 首选蓝胶囊 + 学习标记），
// 色值与 skin/skin.go 保持一致；浅/深是两套**静态 sRGB NSColor**，每次 draw
// 按 effectiveAppearance 选择（不是 NSColor 动态色——机制描述以代码为准，
// 2026-10-30 审计 A16 修正过期注释）。

import AppKit

// MARK: - 主题色（与 skin.DefaultSkinFor 色值一一对应）

// internal（非 private）：tools/panel_metrics.swift 需要导出 13 个色值，
// 与 Go 侧 skin/skin.go 做浅/深两套逐色比对（visual-parity.sh D 段）。
struct CandidateTheme {
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
    /// 点击右侧折叠区触发翻下一页。
    var onPageDown: (() -> Void)?
    /// 末页时折叠区箭头变 ⌃，点击触发翻上一页。
    var onPageUp: (() -> Void)?
    /// 点击设置齿轮。
    var onSettings: (() -> Void)?

    // 排版参数（pt），与 skin/skin.go 的 Skin 字段保持一致
    // internal（非 private）：tools/panel_metrics.swift 需要读取这些常量，
    // 与 Go 侧 tools/render_metrics.go 导出的 skin 参数做机器比对，防止双端视觉漂移。
    enum Layout {
        static let fontSize: CGFloat = 15
        static let indexFontSize: CGFloat = 12      // 主字号 80%
        static let padding: CGFloat = 7
        static let firstPadLeft: CGFloat = 9
        static let itemPadX: CGFloat = 7
        static let gap: CGFloat = 11
        static let lineSpacing: CGFloat = 4
        static let maxWidth: CGFloat = 720
        static let maxRows = 3
        // 行高用 CJK 安全值：NSFont 的 ascender/descender 是西文 metrics，不含
        // 中文字形的上下延展，按它算的面板高度会把第二行文字下半裁掉
        // （2026-10-25 实机：两行时「金田村」被裁半行）。
        static let textH: CGFloat = ceil(fontSize * 1.4)
        static let chevronBoxW: CGFloat = 36
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
    // 布局缓存：layout() 时算一次，draw() 复用——此前 layout()（经
    // contentSize）与 draw() 各调一次 layoutRows()，每次按键对每个候选做
    // 多次 NSString 文本测量，纯属重复计算（2026-10-27 性能审查）。
    private var cachedRows: [Row] = []
    private var cachedLineH: CGFloat = 0
    // 首项超长候选的截断展示文本（索引 → 带省略号的文本）。布局时计算，
    // draw 复用；未截断的候选不在表中（用 items 原文）。与 Windows 壳
    // 「首项逐字截断 + …」策略对齐（2026-10-30 审计 A-P1-1）。
    private var displayTexts: [Int: String] = [:]
    // VoiceOver 子元素的强引用（accessibilityChildren 每次刷新重建）
    var axChildren: [CandidateAXItem] = []
    override var isFlipped: Bool { true }

    /// 用组词状态刷新内容；返回内容尺寸（pt）。items 为空时返回 .zero。
    func layout(candidates: [Item], page: Int, pageCount: Int) -> NSSize {
        self.items = candidates
        self.page = page
        self.pageCount = pageCount
        displayTexts = [:] // 每次布局重算截断文本
        let (rows, lineH) = layoutRows()
        cachedRows = rows
        cachedLineH = lineH
        let size = contentSize(rows: rows, lineH: lineH)
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
    private func layoutRows() -> (rows: [Row], lineHeight: CGFloat) {
        let textH = Layout.textH
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
            - SettingsGear.boxW - Layout.pillMargin * 2 - 2
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
            let chipTextGap = max(3, idxW * 45 / 100)
            let markW: CGFloat = p.fromUser ? ceil(("*" as NSString).size(withAttributes: [.font: textFont]).width) + 2 : 0
            var textW = ceil((p.text as NSString).size(withAttributes: [.font: textFont]).width)
            var boxW = p.padX * 2 + chipW + chipTextGap + textW + markW
            // 首项独超宽：单条候选宽于内容上限时会把面板撑破 720pt、甚至越出
            // 屏幕（Windows 壳已按「单条超宽逐字截断 + …」处理）。mac 是多行
            // 折行，只有「一条候选自己就超宽」这一种情况需要截断，对齐 Windows 策略。
            if rowFrames.isEmpty, boxW > maxContent {
                let avail = maxContent - p.padX * 2 - chipW - chipTextGap - markW
                let ellipsis = "…"
                let ellipsisW = ceil((ellipsis as NSString).size(withAttributes: [.font: textFont]).width)
                var display = ellipsis
                if avail > ellipsisW {
                    var chars = Array(p.text)
                    while !chars.isEmpty {
                        let candidate = String(chars) + ellipsis
                        if ceil((candidate as NSString).size(withAttributes: [.font: textFont]).width) <= avail {
                            display = candidate
                            break
                        }
                        chars.removeLast()
                    }
                    if chars.isEmpty { display = ellipsis }
                } else {
                    display = "" // 极窄（理论上不会发生）：宁可不显示也不溢出
                }
                displayTexts[next] = display
                textW = ceil((display as NSString).size(withAttributes: [.font: textFont]).width)
                boxW = p.padX * 2 + chipW + chipTextGap + textW + markW
                if boxW > maxContent { boxW = maxContent } // 防御：绝不突破内容上限
            }
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
        return (rows, lineH)
    }

    private func contentSize(rows: [Row], lineH: CGFloat) -> NSSize {
        var width: CGFloat = 0
        for row in rows {
            // 面板宽度必须包含齿轮区（SettingsGear.boxW）——漏加会让齿轮
            // 叠在最后一个候选/首选胶囊上（2026-10-28 实机截图「你是谁啊」
            // 单候选时齿轮压字）。
            let w = row.width + Layout.padding * 2 + Layout.chevronBoxW + SettingsGear.boxW
            width = max(width, w)
        }
        // 最小宽度也要容纳 控制区(chevron+gear)+padding：60 连控制区都不够
        let minW = Layout.padding * 2 + Layout.chevronBoxW + SettingsGear.boxW + 8
        contentHeight = Layout.padding * 2 + CGFloat(rows.count) * lineH - Layout.lineSpacing
        return NSSize(width: ceil(max(width, minW)), height: ceil(max(contentHeight, 20)))
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

        // 复用 layout() 的布局缓存（draw 必然发生在 layout 之后）；
        // 防御性兜底：缓存为空但 items 非空时现算（不应发生）
        var rows = cachedRows
        let lineH = cachedLineH
        if rows.isEmpty, !items.isEmpty {
            let fresh = layoutRows()
            rows = fresh.rows
        }
        let idxAscent = indexFont.ascender
        let idxDescent = -indexFont.descender
        let idxH = ceil(idxAscent + idxDescent)
        let textH = Layout.textH
        let textLineH = ceil(textFont.ascender - textFont.descender)
        let panelR = Layout.panelRadius

        hitAreas = []
        var y: CGFloat = Layout.padding
        for row in rows {
            var x = Layout.padding
            for frame in row.frames {
                let item = items[frame.index]
                // 首选判定用全局候选索引：行内枚举的 i 每行归零，会让每行
                // 首项都误画高亮胶囊（实机截图已复现）。
                let p = frame.index == 0
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

                // 候选文本（超长首项用布局阶段算好的截断文本，见 displayTexts；
                // 未截断的候选直接用 items 原文，像素布局不变）
                let displayText = displayTexts[frame.index] ?? item.text
                let textAttrs: [NSAttributedString.Key: Any] = [
                    .font: textFont,
                    .foregroundColor: p ? theme.highlightText : theme.text,
                ]
                (displayText as NSString).draw(at: NSPoint(x: curX, y: y + (textH - textLineH) / 2), withAttributes: textAttrs)
                let textW = ceil((displayText as NSString).size(withAttributes: [.font: textFont]).width)
                curX += textW

                // 学习标记 *
                if item.fromUser {
                    (("*" as NSString).draw(at: NSPoint(x: curX + 1, y: y + (textH - textLineH) / 2), withAttributes: [
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

        // 右侧折叠区：分割线 + chevron + 页码。页码固定钉在折叠区内——面板
        // 宽度随候选变化时它的相对位置恒定，不再浮在候选行尾随宽度漂移。
        // 折叠区可点击：翻到下一页（与 = / ] 键等效）。
        let width = bounds.width
        // 设置齿轮在折叠区（chevron）左侧：参考微信输入法候选栏右侧的
        // 设置入口。divider 右侧依次为齿轮 → 翻页/页码。
        let gearX = width - Layout.chevronBoxW - SettingsGear.boxW
        let dividerX = gearX + 2
        let divH = Layout.textH * 55 / 100
        theme.divider.setFill()
        // 分割线**垂直居中**（与 Windows 壳 win32.cpp 的定位约定一致）。
        // 此前这里是「贴底」（bounds.height - divH - padding），同一条竖线在
        // macOS 原生候选窗与 Windows 贴图端位置不同（2026-10-26 审查发现）。
        // 两端各写一份布局又无黄金对比测试，正是漂移的温床——双端视觉
        // 差分见 tests/visual-parity.sh。
        NSRect(x: dividerX, y: (bounds.height - divH) / 2, width: 1, height: divH).fill()

        // 折叠区内容整体垂直居中：chevron（翻页指示）在上、页码在下。
        // divider 右侧依次为齿轮区 → 翻页区，chevron 以翻页子区的中点居中
        //（此前误用「divider 到右缘」的中点，正好落回齿轮位置——齿轮与
        // 翻页箭头/页码重叠，2026-10-28 实机截图确认）。
        let flipZoneX = dividerX + SettingsGear.boxW
        let chevX = flipZoneX + (width - flipZoneX) / 2
        let pageText = pageCount > 1 ? "\(page + 1)/\(pageCount)" : nil
        let pageAttrs: [NSAttributedString.Key: Any] = [.font: indexFont, .foregroundColor: theme.pageColor]
        let pageSize = pageText.map { ($0 as NSString).size(withAttributes: pageAttrs) }
        let pageH = pageSize.map { ceil($0.height) } ?? 0
        let blockH: CGFloat = 8 + (pageText != nil ? 3 + pageH : 0)
        let blockTop = (bounds.height - blockH) / 2
        // 折叠区：箭头方向反映实际可用动作——有下一页 ⌄（点击翻下页）；
        // 已在末页但有上一页 ⌃（点击翻上页）；单页 ⌄ 低对比度（不可点）。
        let hasNext = page + 1 < pageCount
        let hasPrev = page > 0
        let dirDown = hasNext || !hasPrev
        let dim = pageCount <= 1
        let chevronColor = dim
            ? theme.chevron.withAlphaComponent(theme.chevron.alphaComponent / 3)
            : theme.chevron
        drawChevron(x: chevX, y: blockTop + 4, arm: 3, color: chevronColor, dirDown: dirDown)
        if let pageText, let pageSize {
            let pageW = ceil(pageSize.width)
            (pageText as NSString).draw(at: NSPoint(x: chevX - pageW / 2, y: blockTop + 8 + 3), withAttributes: pageAttrs)
        }

        // 折叠区点击 → 按箭头方向翻页（仅多页时有意义；命中区从齿轮
        // 右侧开始，齿轮点击单独走 onSettings）
        if pageCount > 1 {
            let flipRect = NSRect(x: flipZoneX, y: 0, width: width - flipZoneX, height: bounds.height)
            hitAreas.append((flipRect, dirDown ? -1 : -2))
        }

        // 设置齿轮：手绘矢量（见 drawGear），hitArea -3 由 mouseDown 转发
        // onSettings。颜色固定用 chipDigit（浅色深黑/深色纯白），不随
        // hover/输入内容变色。
        let gearRect = NSRect(x: dividerX, y: 0, width: SettingsGear.boxW, height: bounds.height)
        // 齿轮用**手绘矢量路径**（中心圆环 + 8 齿辐条）：SF Symbol 模板图
        // 在离屏/PDF 上下文的着色不可靠（tint.set 无效；蒙版填色在 PDF
        // 渲染下成实心方块——panel_preview 实测），手绘路径颜色完全受控。
        // 颜色 chipDigit：浅色深黑/深色纯白，两套主题对比度都足够。
        drawGear(center: NSPoint(x: gearRect.midX, y: bounds.height / 2),
                 color: theme.chipDigit)
        hitAreas.append((gearRect, -3))
    }

    /// 设置齿轮（矢量手绘）：中心圆环 + 8 齿辐条，约 15pt。SF Symbol
    /// 模板图在离屏/PDF 上下文的着色不可靠（多轮颜色改不动、蒙版填色在
    /// PDF 渲染下成实心方块——panel_preview 实测），手绘路径颜色完全受控。
    private func drawGear(center: NSPoint, color: NSColor) {
        color.setStroke()
        let ring = NSBezierPath(
            ovalIn: NSRect(x: center.x - 3.4, y: center.y - 3.4, width: 6.8, height: 6.8))
        ring.lineWidth = 1.8
        ring.stroke()
        let spokes = NSBezierPath()
        spokes.lineCapStyle = .round
        spokes.lineWidth = 2
        for i in 0..<8 {
            let angle = CGFloat(i) * .pi / 4
            let dx = cos(angle), dy = sin(angle)
            spokes.move(to: NSPoint(x: center.x + dx * 4.6, y: center.y + dy * 4.6))
            spokes.line(to: NSPoint(x: center.x + dx * 7.2, y: center.y + dy * 7.2))
        }
        spokes.stroke()
    }

    /// 折叠指示箭头：dirDown=true 画 ⌄（翻下页），false 画 ⌃（翻上页）。
    /// 视图 isFlipped（y 向下），两种形状的顶点分别在下方/上方。
    private func drawChevron(x: CGFloat, y: CGFloat, arm: CGFloat, color: NSColor, dirDown: Bool) {
        let path = NSBezierPath()
        if dirDown {
            path.move(to: NSPoint(x: x - arm, y: y - arm * 0.7))
            path.line(to: NSPoint(x: x, y: y + arm * 0.7))
            path.line(to: NSPoint(x: x + arm, y: y - arm * 0.7))
        } else {
            path.move(to: NSPoint(x: x - arm, y: y + arm * 0.7))
            path.line(to: NSPoint(x: x, y: y - arm * 0.7))
            path.line(to: NSPoint(x: x + arm, y: y + arm * 0.7))
        }
        path.lineWidth = 1.5
        path.lineCapStyle = .round
        color.setStroke()
        path.stroke()
    }

    // MARK: 鼠标点选（原生方案红利：贴图版 ignoresMouseEvents 无法实现）

    // 非 key 窗口的首击默认只用于激活窗口、不投递 mouseDown，会导致
    // 「第一次点候选词没反应，要点第二次」。候选窗永不成为 key 窗口
    // （nonactivatingPanel），因此必须显式接受首次点击。
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func mouseDown(with event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        for (rect, index) in hitAreas where rect.contains(point) {
            switch index {
            case -1: onPageDown?()
            case -2: onPageUp?()
            case -3: onSettings?()
            default: onSelect?(index)
            }
            return
        }
    }

    // MARK: 无障碍（贴图方案无法提供，本次回归）

    // 系统外观切换（浅色 ⇄ 深色）时重绘。面板在 draw 里读 effectiveAppearance
    // 取配色，但若面板正开着而用户切换了外观，视图不会自动重绘——候选窗会
    // 保持旧配色直到下次输入（2026-10-26 审查发现）。
    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        needsDisplay = true
    }


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
    /// 点击折叠区翻下一页的回调，与 onSelect 同约定。
    static var onPageDown: (() -> Void)?
    /// 末页时折叠区箭头变 ⌃，点击翻上一页。
    static var onPageUp: (() -> Void)?
    /// 候选栏齿轮点击（打开设置菜单）。
    static var onSettings: (() -> Void)?

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
        v.onPageDown = { onPageDown?() }
        v.onPageUp = { onPageUp?() }
        v.onSettings = { onSettings?() }
        let size = v.layout(candidates: candidates, page: page, pageCount: pageCount)
        p.setContentSize(size)
        p.setFrameTopLeftPoint(originProvider(size))
        p.orderFront(nil)
        return true
    }

    static func hide() {
        panel?.orderOut(nil)
        onSelect = nil
        onPageDown = nil
        onPageUp = nil
        onSettings = nil
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
        // 层级用 .floating(3) 而不是 .popUpMenu(101)：popUpMenu 高于系统
        // 菜单栏（mainMenu=24）与一切下拉菜单，候选窗在光标行贴近屏幕顶部
        // 向上翻到菜单区域时，会整块压住菜单拦截点击——表现为「有时候无法
        // 点击其他 app 的菜单」（2026-10-28 用户反馈）。floating 仍浮于
        // 全部普通窗口内容之上（配合 canJoinAllSpaces/fullScreenAuxiliary
        // 保持全屏与多 Space 可见），但不再遮蔽菜单栏。
        p.level = .floating
        p.isOpaque = false
        p.backgroundColor = .clear
        // 面板自身接收鼠标以支持点击选词；不抢焦点（nonactivatingPanel），
        // 面板外点击仍落到宿主应用并触发会话提交。
        p.ignoresMouseEvents = false
        p.hasShadow = true
        // 候选窗必须能跟随用户出现在任何 Space / 全屏应用之上。
        //
        // NSWindow.h 明确：windowLevel != NSNormalWindowLevel 时默认
        // collectionBehavior 是 Transient（"Floats in spaces, hidden by
        // exposé"），不参与 Space 切换。而输入法常在浏览器/编辑器全屏
        // （独立 Space）里使用——不加下面这三项，orderFront 后候选窗
        // 根本不会显示（2026-10-26 审查发现，此前只测了普通窗口场景）。
        //   .canJoinAllSpaces  —— 出现在所有 Space
        //   .fullScreenAuxiliary —— 允许在全屏应用的独立 Space 上显示
        //   .stationary        —— Exposé/Mission Control 时不随桌面移动
        p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary]
        // borderless 窗口默认不接受首次点击（非 key 窗口的首击只用来激活），
        // 会使「点击候选词上屏」第一次点击无效、需要点两次。
        // becomesKeyOnlyIfNeeded 让面板在需要时才取 key，配合视图的
        // acceptsFirstMouse 即可单击生效且不抢焦点（2026-10-26 审查发现）。
        p.becomesKeyOnlyIfNeeded = true
        let v = CandidatePanelView(frame: NSRect(x: 0, y: 0, width: 10, height: 10))
        v.autoresizingMask = [.width, .height]
        p.contentView = v
        panel = p
        view = v
        return p
    }
}
// MARK: - 双端视觉一致性（供 tools/panel_metrics.swift 导出）

extension CandidatePanelView {
    /// LayoutSnapshot 把布局常量导出给 panel_metrics 工具，与
    /// Go 侧 skin/skin.go 的 Skin 参数做机器比对。
    /// 这些值与 Go 端必须一致，由 tests/visual-parity.sh 断言。
    struct LayoutSnapshot {
        let fontSize: Double
        let padding: Double
        let lineSpacing: Double
        let maxWidth: Double
        let panelRadius: Double
        let textH: Double
        // 2026-10-30 审计 G8：以下参数此前没有任何机器断言，改动只靠肉眼。
        // 全部导出给 panel_metrics，由 visual-parity.sh C 段对照文档期望值锁定
        //（macOS 是视觉参考，这些常量被误改会直接改变三端对齐基准）。
        let indexFontSize: Double
        let firstPadLeft: Double
        let itemPadX: Double
        let gap: Double
        let chipPadX: Double
        let chipPadY: Double
        let chipRadius: Double
        let chevronBoxW: Double
        let gearBoxW: Double
        let pillMargin: Double
        let maxRows: Int
        /// 最小窗宽：与 contentSize() 的 minW 同公式。
        let minWidth: Double

        static var current: LayoutSnapshot {
            LayoutSnapshot(
                fontSize: Layout.fontSize,
                padding: Layout.padding,
                lineSpacing: Layout.lineSpacing,
                maxWidth: Layout.maxWidth,
                panelRadius: Layout.panelRadius,
                textH: Layout.textH,
                indexFontSize: Layout.indexFontSize,
                firstPadLeft: Layout.firstPadLeft,
                itemPadX: Layout.itemPadX,
                gap: Layout.gap,
                chipPadX: Layout.chipPadX,
                chipPadY: Layout.chipPadY,
                chipRadius: Layout.chipRadius,
                chevronBoxW: Layout.chevronBoxW,
                gearBoxW: SettingsGear.boxW,
                pillMargin: Layout.pillMargin,
                maxRows: Layout.maxRows,
                minWidth: Layout.padding * 2 + Layout.chevronBoxW + SettingsGear.boxW + 8
            )
        }
    }
}

// MARK: - 模式切换提示（短暂 toast）

// 中英/全半角切换此前零反馈，用户只能靠打字试探才知道当前模式
// （2026-10-27 审查，三端统一补齐）。与候选窗同等层级的小胶囊，
// 短暂显示后自动消失；连续切换时旧定时器作废，不会提前收起新提示。

/// 模式提示的展示时长（秒）。
private let modeToastDuration: TimeInterval = 1.2

final class ModeToastView: NSView {
    var text: String = "" { didSet { needsDisplay = true } }

    override func draw(_ dirtyRect: NSRect) {
        let isDark = effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        let bg = isDark ? NSColor(white: 0.18, alpha: 0.92) : NSColor(white: 0.97, alpha: 0.95)
        let fg = isDark ? NSColor.white : NSColor(calibratedWhite: 0.15, alpha: 1)
        let path = NSBezierPath(roundedRect: bounds, xRadius: 10, yRadius: 10)
        bg.setFill()
        path.fill()
        NSColor(white: 0, alpha: isDark ? 0.4 : 0.15).setStroke()
        path.lineWidth = 1
        path.stroke()
        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 13, weight: .medium),
            .foregroundColor: fg,
        ]
        let size = (text as NSString).size(withAttributes: attrs)
        (text as NSString).draw(
            at: NSPoint(x: (bounds.width - size.width) / 2, y: (bounds.height - size.height) / 2),
            withAttributes: attrs)
    }

    /// 按文本内容计算胶囊尺寸。
    static func size(for text: String) -> NSSize {
        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 13, weight: .medium)
        ]
        let s = (text as NSString).size(withAttributes: attrs)
        return NSSize(width: ceil(s.width) + 28, height: ceil(s.height) + 14)
    }
}

enum ModeToastController {
    private static var panel: NSPanel?
    private static var hideWork: DispatchWorkItem?

    /// 在给定左上角坐标显示提示，约 1.2 秒后自动消失。
    static func show(text: String, topLeft: NSPoint) {
        let p = ensurePanel()
        guard let v = p.contentView as? ModeToastView else { return }
        v.text = text
        let size = ModeToastView.size(for: text)
        p.setContentSize(size)
        p.setFrameTopLeftPoint(topLeft)
        p.orderFront(nil)
        hideWork?.cancel()
        let work = DispatchWorkItem { [weak p] in p?.orderOut(nil) }
        hideWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + modeToastDuration, execute: work)
    }

    /// 立即收起（会话结束/切换输入法时调用，不等定时器）。
    static func hide() {
        hideWork?.cancel()
        panel?.orderOut(nil)
    }

    private static func ensurePanel() -> NSPanel {
        if let panel, panel.contentView is ModeToastView { return panel }
        let p = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 10, height: 10),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        // 层级同候选窗用 .floating（不遮蔽菜单栏，见候选窗处注释）
        p.level = .floating
        p.isOpaque = false
        p.backgroundColor = .clear
        // 纯提示：不接收鼠标（点击穿透到宿主），不抢焦点，跟随 Space/全屏
        p.ignoresMouseEvents = true
        p.hasShadow = true
        p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary]
        let v = ModeToastView(frame: NSRect(x: 0, y: 0, width: 10, height: 10))
        v.autoresizingMask = [.width, .height]
        p.contentView = v
        panel = p
        return p
    }
}

// MARK: - VoiceOver 候选子元素

// 此前面板只能被 VoiceOver 整串朗读，无法逐项浏览/上屏（2026-10-27 审查）。
// 官方模式：NSAccessibilityElement 子类作为虚拟子元素暴露给辅助功能树，
// 每个候选一项——VoiceOver 光标可逐项停留朗读，AXPress（VO+空格）直接
// 走与鼠标点击相同的 onSelect 回调上屏，不依赖面板成为 key 窗口。

final class CandidateAXItem: NSAccessibilityElement {
    private let index: Int
    private weak var owner: CandidatePanelView?

    init(index: Int, owner: CandidatePanelView) {
        self.index = index
        self.owner = owner
        super.init()
    }

    @available(*, unavailable)
    required init(coder: NSCoder) {
        fatalError("unsupported")
    }

    private var itemText: String {
        guard let owner, index < owner.axItemTexts.count else { return "" }
        return owner.axItemTexts[index]
    }

    override func isAccessibilityElement() -> Bool { true }
    override func accessibilityRole() -> NSAccessibility.Role? { .button }
    override func accessibilityLabel() -> String? {
        "候选 \(index + 1)，\(itemText)"
    }
    override func accessibilityValue() -> Any? { itemText }

    /// 元素在屏幕上的 frame（hitAreas 存的是视图坐标）。
    override func accessibilityFrame() -> NSRect {
        guard let owner else { return .zero }
        return owner.axItemFrame(at: index)
    }

    /// VO+空格（AXPress）上屏：与鼠标点击同一条 onSelect 回调。
    override func accessibilityPerformPress() -> Bool {
        guard let owner else { return false }
        owner.axPressItem(at: index)
        return true
    }
}

extension CandidatePanelView {
    static let axMaxItems = 12

    /// 当前候选的文本快照（供 AX 子元素在 owner 释放前读取）。
    var axItemTexts: [String] {
        items.map(\.text)
    }

    /// AX 子元素的屏幕 frame（extension 内访问 private hitAreas）。
    func axItemFrame(at index: Int) -> NSRect {
        guard index < hitAreas.count, let window else { return .zero }
        let windowRect = convert(hitAreas[index].rect, to: nil)
        return window.convertToScreen(windowRect)
    }

    /// AX 子元素的弱引用入口：AXPress 直接走与鼠标点击一致的回调。
    func axPressItem(at index: Int) {
        guard index < hitAreas.count else { return }
        let hit = hitAreas[index].index
        switch hit {
        case -1: onPageDown?()
        case -2: onPageUp?()
        default: onSelect?(hit)
        }
    }

    /// 逐项暴露候选给 VoiceOver（面板级 accessibilityValue 的整串朗读保留）。
    override func accessibilityChildren() -> [Any]? {
        guard !items.isEmpty else { return nil }
        let count = min(items.count, Self.axMaxItems)
        let children = (0..<count).map { CandidateAXItem(index: $0, owner: self) }
        // AX 子元素是弱引用 owner 的临时对象，必须由视图持有强引用
        axChildren = children
        return children
    }
}

// MARK: - 设置开关（词组联想）

// 联想开关的持久化与读取。macOS 用 UserDefaults（候选栏齿轮菜单切换）；
// Windows 用注册表、Linux 用 config.json——三端存储各自独立，语义一致
// （未配置 = 开启）。
enum AssociateSetting {
    static let key = "AssociateEnabled"

    static var enabled: Bool {
        get { UserDefaults.standard.object(forKey: key) as? Bool ?? true }
        set { UserDefaults.standard.set(newValue, forKey: key) }
    }

    static func toggle() {
        enabled = !enabled
    }
}

// 齿轮区宽度（pt，折叠区内、chevron 左侧）：
// 齿轮矢量直径 2*7.2pt=14.4pt + 两侧各约 5.8pt 余量 = 26pt。
// 与 Windows win32.cpp 的 gearBoxW（pt(26)）对应。
// 历史：2026-10-28 995dd99 已删除「圆形淡底直径 22」方案，旧注释停留在
// 删除前（2026-10-30 审计 A16 修正）。
enum SettingsGear {
    static let boxW: CGFloat = 26
}
