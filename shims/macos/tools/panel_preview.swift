// render_panel_preview.swift：离屏渲染 CandidatePanelView 出 PNG（浅色+深色），
// 用于不干扰桌面的绘制验证。运行：swiftc src/CandidatePanel.swift tools/panel_preview.swift -o /tmp/panel_preview && /tmp/panel_preview <outDir>
import AppKit

let args = CommandLine.arguments
let outDir = args.count > 1 ? args[1] : "/tmp"

@main struct PanelPreview {
    static func main() {
        // 两行长候选场景（复刻真机：一行 8 个 + 第二行若干）
        let texts = ["今天", "今天时间", "进天", "近天", "金天", "仅天", "尽天", "金田起义", "金田村"]
        render(dark: false, page: 0, pageCount: 3, tag: "light", out: outDir + "/panel-light.png")
        render(dark: true, page: 0, pageCount: 3, tag: "dark", out: outDir + "/panel-dark.png")
        // 末页场景：箭头应变 ⌃（翻上一页）
        render(dark: true, page: 2, pageCount: 3, tag: "dark-last", out: outDir + "/panel-dark-last.png")
        // 单页场景：箭头低对比度
        render(dark: true, page: 0, pageCount: 1, tag: "dark-single", out: outDir + "/panel-dark-single.png")
    }

    static func render(dark: Bool, page: Int, pageCount: Int, tag: String, out: String) {
        let view = CandidatePanelView(frame: NSRect(x: 0, y: 0, width: 800, height: 40))
        view.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        let texts = ["今天", "今天时间", "进天", "近天", "金天", "仅天", "尽天", "金田起义", "金田村"]
        let items = texts.enumerated().map { i, text in
            CandidatePanelView.Item(text: text, fromUser: i == 0)
        }
        let size = view.layout(candidates: items, page: page, pageCount: pageCount)
        view.frame = NSRect(origin: .zero, size: size)
        let pdf = view.dataWithPDF(inside: view.bounds)
        let img = NSImage(data: pdf)!
        let rep = NSBitmapImageRep(data: img.tiffRepresentation!)!
        rep.size = size
        let png = rep.representation(using: .png, properties: [:])!
        try! png.write(to: URL(fileURLWithPath: out))
        print("已写出 \(out) \(Int(size.width))x\(Int(size.height)) page=\(page+1)/\(pageCount)")
    }
}
