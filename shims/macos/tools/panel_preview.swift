// render_panel_preview.swift：离屏渲染 CandidatePanelView 出 PNG（浅色+深色），
// 用于不干扰桌面的绘制验证。运行：swiftc src/CandidatePanel.swift tools/panel_preview.swift -o /tmp/panel_preview && /tmp/panel_preview <outDir>
import AppKit

let args = CommandLine.arguments
let outDir = args.count > 1 ? args[1] : "/tmp"

// 壳进程真实链路：DefaultSkin 浅色起步无对应物，原生方案深浅色由
// NSAppearance 驱动——直接给 view 强制 appearance 分别出两版。
func render(dark: Bool, out: String) {
    let view = CandidatePanelView(frame: NSRect(x: 0, y: 0, width: 800, height: 40))
    view.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
    let texts = ["你好", "你号", "你灏", "泥好", "你豪", "尼好", "你", "泥", "尼"]
    let items = texts.enumerated().map { i, text in
        CandidatePanelView.Item(text: text, fromUser: i == 0)
    }
    let size = view.layout(candidates: items, page: 0, pageCount: 2)
    view.frame = NSRect(origin: .zero, size: size)
    let pdf = view.dataWithPDF(inside: view.bounds)
    let img = NSImage(data: pdf)!
    let rep = NSBitmapImageRep(data: img.tiffRepresentation!)!
    rep.size = size
    let png = rep.representation(using: .png, properties: [:])!
    try! png.write(to: URL(fileURLWithPath: out))
    print("已写出 \(out) \(Int(size.width))x\(Int(size.height))")
}

@main struct PanelPreview {
    static func main() {
        render(dark: false, out: outDir + "/panel-light.png")
        render(dark: true, out: outDir + "/panel-dark.png")
    }
}
