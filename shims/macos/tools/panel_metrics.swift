// panel_metrics.swift：输出 macOS 原生候选窗（CandidatePanel）的几何与配色参数
// （JSON），供 tests/visual-parity.sh 与 Go 端 tools/render_metrics.go 做差分。
//
// 背景：两端是同一套视觉的两份独立实现，靠人工手抄同步，没有任何自动断言，
// 已实证漂移（分栏线 y、字号）。本工具让漂移可被 CI 捕获。
//
// 用法：
//   swiftc src/CandidatePanel.swift tools/panel_metrics.swift -o /tmp/panel_metrics
//   /tmp/panel_metrics
import AppKit
import Foundation

@main struct PanelMetrics {
    static func main() {
        // 用一组固定候选驱动一次真实布局（divider 导出需要 size）
        let items = ["今天", "今天时间", "明天", "昨天", "半天", "白天"]
        let view = CandidatePanelView(frame: NSRect(x: 0, y: 0, width: 800, height: 40))
        view.appearance = NSAppearance(named: .aqua)
        let list = items.enumerated().map { i, text in
            CandidatePanelView.Item(text: text, fromUser: i == 0)
        }
        let size = view.layout(candidates: list, page: 0, pageCount: 3)

        // 布局常量（与 Go 侧 skin/skin.go 字段一一对应）
        let layout = CandidatePanelView.LayoutSnapshot.current
        var out: [String: Any] = [:]
        out["skin"] = [
            "fontSize": layout.fontSize,
            "padding": layout.padding,
            "lineSpacing": layout.lineSpacing,
            "maxWidth": layout.maxWidth,
            "cornerRadius": layout.panelRadius,
        ]
        // 分栏线垂直位置（关键漂移点，2026-10-26 审查实证漂移处）
        let divH = layout.textH * 55 / 100
        out["divider"] = [
            "height": Double(divH),
            "y": Double((size.height - divH) / 2),
        ]

        let data = try! JSONSerialization.data(
            withJSONObject: out, options: [.prettyPrinted, .sortedKeys])
        FileHandle.standardOutput.write(data)
        FileHandle.standardOutput.write("\n".data(using: .utf8)!)
    }
}
