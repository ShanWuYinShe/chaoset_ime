// panel_metrics.swift：输出 macOS 原生候选窗（CandidatePanel）的几何与配色参数
// （JSON），供 tests/visual-parity.sh 与 Go 端 tools/render_metrics.go 做差分。
//
// 背景：两端是同一套视觉的两份独立实现，靠人工手抄同步，没有任何自动断言，
// 已实证漂移（分栏线 y、字号）。本工具让漂移可被 CI 捕获。
//
// 2026-10-30 三端一致性审计 G2/G8 加固：
//   - 导出浅/深**两套全部 13 个色值**（此前 Swift 端一个色值都没导出，
//     visual-parity 只能自证 Go 侧 4 个浅色）；
//   - 导出 Layout 的 12 个附加常量（indexFontSize/firstPadLeft/.../minWidth）
//     与 divider 的 height/y（divider 此前导出后无人消费，是死断言）；
//   - 由 visual-parity.sh 的 C 段对照 docs/ui-parity.md 的期望值锁定 macOS
//     参考常量，D 段做 Go↔Swift 逐色比对（浅+深）。
//
// 用法：
//   swiftc src/CandidatePanel.swift tools/panel_metrics.swift -o /tmp/panel_metrics
//   /tmp/panel_metrics
import AppKit
import Foundation

@main struct PanelMetrics {
    /// NSColor → #RRGGBBAA（转 sRGB 后按 255 四舍五入，与 Go 侧
    /// render_metrics.go 的 #RRGGBBAA 格式一致）。
    static func hex(_ color: NSColor) -> String {
        guard let srgb = color.usingColorSpace(.sRGB) else { return "#00000000" }
        func channel(_ v: CGFloat) -> Int { Int((v * 255.0).rounded()) }
        return String(
            format: "#%02X%02X%02X%02X",
            channel(srgb.redComponent), channel(srgb.greenComponent),
            channel(srgb.blueComponent), channel(srgb.alphaComponent))
    }

    /// 13 个色字段的 key 与 skin/skin.go、render_metrics.go 一一对应。
    static func colors(_ theme: CandidateTheme) -> [String: String] {
        [
            "panelBg": hex(theme.panelBg),
            "panelBorder": hex(theme.panelBorder),
            "text": hex(theme.text),
            "highlightBg": hex(theme.highlightBg),
            "highlightText": hex(theme.highlightText),
            "chipBg": hex(theme.chipBg),
            "chipBorder": hex(theme.chipBorder),
            "chipDigit": hex(theme.chipDigit),
            "highlightChipBg": hex(theme.highlightChipBg),
            "pageColor": hex(theme.pageColor),
            "userMark": hex(theme.userMark),
            "divider": hex(theme.divider),
            "chevron": hex(theme.chevron),
        ]
    }

    static func main() {
        // 用一组固定候选驱动一次真实布局（divider 导出需要 size）
        let items = ["今天", "今天时间", "明天", "昨天", "半天", "白天"]
        let view = CandidatePanelView(frame: NSRect(x: 0, y: 0, width: 800, height: 40))
        view.appearance = NSAppearance(named: .aqua)
        let list = items.enumerated().map { i, text in
            CandidatePanelView.Item(text: text, fromUser: i == 0)
        }
        let size = view.layout(candidates: list, page: 0, pageCount: 3)

        // 布局常量（与 Go 侧 skin/skin.go 字段一一对应；附加字段供 C 段自检）
        let layout = CandidatePanelView.LayoutSnapshot.current
        var out: [String: Any] = [:]
        out["skin"] = [
            "fontSize": layout.fontSize,
            "padding": layout.padding,
            "lineSpacing": layout.lineSpacing,
            "maxWidth": layout.maxWidth,
            "cornerRadius": layout.panelRadius,
            "textH": layout.textH,
            "indexFontSize": layout.indexFontSize,
            "firstPadLeft": layout.firstPadLeft,
            "itemPadX": layout.itemPadX,
            "gap": layout.gap,
            "chipPadX": layout.chipPadX,
            "chipPadY": layout.chipPadY,
            "chipRadius": layout.chipRadius,
            "chevronBoxW": layout.chevronBoxW,
            "gearBoxW": layout.gearBoxW,
            "pillMargin": layout.pillMargin,
            "maxRows": layout.maxRows,
            "minWidth": layout.minWidth,
        ]
        // 分栏线几何（关键漂移点，2026-10-26 审查实证漂移处；此前导出后
        // visual-parity 无人消费，2026-10-30 起 C 段做期望值断言）。
        let divH = layout.textH * 55 / 100
        out["divider"] = [
            "height": Double(divH),
            "y": Double((size.height - divH) / 2),
        ]
        // 配色：浅色 + 深色两套，Go↔Swift 逐色比对
        out["colors"] = colors(CandidateTheme.theme(dark: false))
        out["colorsDark"] = colors(CandidateTheme.theme(dark: true))

        let data = try! JSONSerialization.data(
            withJSONObject: out, options: [.prettyPrinted, .sortedKeys])
        FileHandle.standardOutput.write(data)
        FileHandle.standardOutput.write("\n".data(using: .utf8)!)
    }
}
