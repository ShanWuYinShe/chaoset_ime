// CandidatePanelView.swift：macOS 原生候选窗视图（布局/绘制/鼠标命中/无障碍）。
//
// 从 CandidatePanel.swift 拆出（2026-11-06 架构拆分，纯移动、行为零改动）：
// NSView 子类本体 + VoiceOver 子元素 + 供 tools/panel_metrics.swift 导出的布局快照。
// 主题色/布局常量/齿轮参数见 CandidateTheme.swift；面板单例与对外 API 见
// CandidatePanel.swift；面板构造见 CandidatePanelWindow.swift。

import AppKit

// MARK: - 候选窗视图

final class CandidatePanelView: NSView {
    struct Item {
        let text: String
        let fromUser: Bool
    }

    private var items: [Item] = []
    private var page = 0
    private var pageCount = 0
    /// 页内预选序号（0 起，当前页切片内）。方向键移动、空格确认与高亮 pill
    /// 共用；每次 show（新输入/翻页/选词刷新）由 controller 重置为 0。
    var selectedIndex = 0
    /// 点击命中的候选回调（index 即数字序号-1，等于 daemon select 的候选索引）。
    var onSelect: ((Int) -> Void)?
    /// 点击右侧折叠区触发翻下一页。
    var onPageDown: (() -> Void)?
    /// 末页时折叠区箭头变 ⌃，点击触发翻上一页。
    var onPageUp: (() -> Void)?
    /// 点击设置齿轮。
    var onSettings: (() -> Void)?
    /// 右键候选「删除该词」回调：index 与 onSelect 同约定（当前页切片内序号）。
    var onDelete: ((Int) -> Void)?

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

    /// 已布局的候选数（方向键只在可见项内移动）。
    var itemCount: Int { items.count }

    /// 用组词状态刷新内容；返回内容尺寸（pt）。items 为空时返回 .zero。
    func layout(candidates: [Item], page: Int, pageCount: Int) -> NSSize {
        self.items = candidates
        self.page = page
        self.pageCount = pageCount
        // 预选序号钳制到本页可见候选（候选变少/被 maxRows 截断时防越界）。
        if items.isEmpty {
            selectedIndex = 0
        } else if selectedIndex >= items.count {
            selectedIndex = items.count - 1
        }
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
                // 预选判定用全局候选索引：行内枚举的 i 每行归零，会让每行
                // 首项都误画高亮胶囊（实机截图已复现）。高亮 pill 跟随页内
                // 预选项（此前固定画在第一项）。
                let p = frame.index == selectedIndex
                // 预选项：蓝色高亮胶囊（圆角承接面板圆角）
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

        // 折叠区内容整体垂直居中：上箭头 / 页码 / 下箭头（issue#2：两枚
        // 箭头分别反映上一页/下一页是否可用）。divider 右侧依次为齿轮区 →
        // 翻页区，箭头与页码以翻页子区的中点居中（此前误用「divider 到右缘」
        // 的中点，正好落回齿轮位置——齿轮与翻页箭头/页码重叠，2026-10-28 实机确认）。
        let flipZoneX = dividerX + SettingsGear.boxW
        let chevX = flipZoneX + (width - flipZoneX) / 2
        let pageText = pageCount > 1 ? "\(page + 1)/\(pageCount)" : nil
        let pageAttrs: [NSAttributedString.Key: Any] = [.font: indexFont, .foregroundColor: theme.pageColor]
        let pageSize = pageText.map { ($0 as NSString).size(withAttributes: pageAttrs) }
        let pageH = pageSize.map { ceil($0.height) } ?? 0
        // 上/下箭头块各 6pt，页码夹在中间；整块垂直居中。单页没有页码，
        // 但仍按「两枚箭头合计 12pt」居中（blockH=12），两枚箭头以面板中线对称。
        let arrowH: CGFloat = 6
        let blockH = pageCount > 1 ? arrowH * 2 + pageH : arrowH * 2
        let blockTop = (bounds.height - blockH) / 2
        let hasNext = page + 1 < pageCount
        let hasPrev = page > 0
        if pageCount <= 1 {
            // 单页：没有上一页/下一页，两枚箭头置灰（不可点，保留控制区形态）。
            let dimColor = theme.chevron.withAlphaComponent(theme.chevron.alphaComponent / 3)
            drawChevron(x: chevX, y: blockTop + arrowH / 2, arm: 3, color: dimColor, dirDown: false)
            drawChevron(x: chevX, y: blockTop + arrowH + arrowH / 2, arm: 3, color: dimColor, dirDown: true)
        } else {
            // 多页：首页只画下箭头、末页只画上箭头、中间页两枚都画——正是
            // issue#2 用户要求（此前只画一枚，中间页看不到上箭头）。
            if hasPrev {
                drawChevron(x: chevX, y: blockTop + arrowH / 2, arm: 3, color: theme.chevron, dirDown: false)
            }
            if let pageText, let pageSize {
                let pageW = ceil(pageSize.width)
                (pageText as NSString).draw(at: NSPoint(x: chevX - pageW / 2, y: blockTop + arrowH), withAttributes: pageAttrs)
            }
            if hasNext {
                drawChevron(x: chevX, y: blockTop + arrowH + pageH + arrowH / 2, arm: 3, color: theme.chevron, dirDown: true)
            }
        }

        // 两枚箭头各自的命中区（只有可用方向可点）：上箭头 -2 翻上一页、
        // 下箭头 -1 翻下一页；单页/不可用方向不注册（点空白无动作）。
        if pageCount > 1 {
            let half = blockH / 2
            if hasPrev {
                hitAreas.append((NSRect(x: flipZoneX, y: blockTop, width: width - flipZoneX, height: half), -2))
            }
            if hasNext {
                hitAreas.append((NSRect(x: flipZoneX, y: blockTop + half, width: width - flipZoneX, height: blockH - half), -1))
            }
        }

        // 设置齿轮：手绘矢量（见 drawGear），hitArea -3 由 mouseDown 记录、
        // mouseUp 且仍在齿轮区才转发 onSettings（设置入口不用按下触发，理由
        // 见 mouseDown/mouseUp 处注释）。颜色固定用 chipDigit（浅色深黑/
        // 深色纯白），不随 hover/输入内容变色。
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
    // （nonactivatingPanel），因此必须显式接受首次点击。齿轮改成 mouseUp
    // 触发后仍依赖这条：首击若只用于激活窗口，mouseDown/mouseUp 都不会送到
    // 视图，也就不会误开设置窗。
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    // 鼠标左键按下命中的控制区（齿轮 -3 / 翻页 -1,-2 / 候选 >=0）。
    // 2026-11-06 用户要求「鼠标用抬起触发才正常，按下触发太奇怪」：按下只记录，
    // 抬起点仍在同一命中区才执行；按下后拖走再松开不触发。齿轮单独保留
    // settingsPressed 标记（checkuiparity 契约）。
    private var pressedIndex: Int?
    private var settingsPressed = false

    override func mouseDown(with event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        // 任何一次新的按下都先清掉上一次未完成的状态：mouseUp 可能因为面板被
        // 收起/切换而丢失，残留状态会让下一次抬起误触发。
        settingsPressed = false
        pressedIndex = nil
        for (rect, index) in hitAreas where rect.contains(point) {
            if index == -3 {
                settingsPressed = true
            } else {
                pressedIndex = index
            }
            return
        }
    }

    /// 左键抬起：按下与抬起都落在同一命中区才执行（候选上屏 / 翻页 / 打开设置）。
    /// 按下后拖走再松开不触发（2026-11-06 用户要求：鼠标抬起触发才是常规语义）。
    override func mouseUp(with event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        let wasSettings = settingsPressed
        let wasIndex = pressedIndex
        settingsPressed = false
        pressedIndex = nil
        if wasSettings {
            guard hitAreas.contains(where: { $0.index == -3 && $0.rect.contains(point) })
            else { return }
            onSettings?()
            return
        }
        guard let index = wasIndex,
              hitAreas.contains(where: { $0.index == index && $0.rect.contains(point) })
        else { return }
        switch index {
        case -1: onPageDown?()
        case -2: onPageUp?()
        default: onSelect?(index)
        }
    }

    // MARK: 右键删除学习记录

    // 右键走 AppKit 的上下文菜单通道：默认 rightMouseDown 只调用 menu(for:)
    // 并弹出返回的菜单，**不会**触发 mouseDown/onSelect——右键绝不上屏；
    // 菜单弹出也不改变面板的 key 状态（nonactivatingPanel + acceptsFirstMouse），
    // 不抢宿主应用焦点。命中区用 draw 时记录的 hitAreas（负 index 是翻页/齿轮
    // 等控制区，右键不提供删除菜单）。
    override func menu(for event: NSEvent) -> NSMenu? {
        let point = convert(event.locationInWindow, from: nil)
        guard let hit = hitAreas.first(where: { $0.rect.contains(point) && $0.index >= 0 })
        else { return nil }
        let menu = NSMenu(title: "")
        // 菜单项始终可用：该候选是否有学习记录由 daemon 判定，壳拿到
        // deleted=false 后 toast 说明（不静默失败）。
        menu.autoenablesItems = false
        let item = NSMenuItem(
            title: "删除该词",
            action: #selector(deleteCandidateMenuItem(_:)),
            keyEquivalent: ""
        )
        item.target = self
        item.representedObject = hit.index
        menu.addItem(item)
        return menu
    }

    @objc private func deleteCandidateMenuItem(_ sender: NSMenuItem) {
        guard let index = sender.representedObject as? Int else { return }
        onDelete?(index)
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
