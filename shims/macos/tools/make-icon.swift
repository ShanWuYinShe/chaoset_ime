// 生成 ChaosetIME 的图标资源：应用图标 .icns + 输入源菜单图标 .pdf。
// 用法: swift tools/make-icon.swift <输出目录>
//
// 现场用 AppKit 绘制「超」字徽标，不依赖任何外部素材与第三方图标——
// 2026-10-05 排障时曾被放进 ~/Library/Input Methods 的「鼠须管」是官方
// Rime/Squirrel 应用（im.rime.inputmethod.Squirrel 1.1.2，ad-hoc 重签），
// 与本项目无关，更不可复用其 Rime.icns/rime.pdf。
import AppKit
import CoreGraphics

let outDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."
try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)

// 徽标：圆角方底 + 白字，与系统输入源图标的显示惯例一致。
func drawBadge(_ s: CGFloat) {
    let inset = s * 0.06
    let rect = NSRect(x: inset, y: inset, width: s - inset * 2, height: s - inset * 2)
    let path = NSBezierPath(roundedRect: rect, xRadius: s * 0.22, yRadius: s * 0.22)
    NSColor.black.setFill()
    path.fill()
    let font = NSFont.systemFont(ofSize: s * 0.58, weight: .semibold)
    let text = NSAttributedString(string: "超", attributes: [
        .font: font, .foregroundColor: NSColor.white,
    ])
    let size = text.size()
    text.draw(at: NSPoint(x: (s - size.width) / 2, y: (s - size.height) / 2))
}

func png(_ px: Int) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                               isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    drawBadge(CGFloat(px))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

func pdf(_ size: CGFloat, to path: String) {
    var box = CGRect(x: 0, y: 0, width: size, height: size)
    let data = NSMutableData()
    let consumer = CGDataConsumer(data: data as CFMutableData)!
    let ctx = CGContext(consumer: consumer, mediaBox: &box, nil)!
    ctx.beginPDFPage(nil)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(cgContext: ctx, flipped: false)
    drawBadge(size)
    NSGraphicsContext.restoreGraphicsState()
    ctx.endPDFPage()
    ctx.closePDF()
    try! (data as Data).write(to: URL(fileURLWithPath: path))
}

let iconset = URL(fileURLWithPath: outDir).appendingPathComponent("ChaosetIME.iconset")
try? FileManager.default.removeItem(at: iconset)
try! FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)
for base in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let name = scale == 1 ? "icon_\(base)x\(base).png" : "icon_\(base)x\(base)@2x.png"
        try! png(base * scale).write(to: iconset.appendingPathComponent(name))
    }
}
pdf(22, to: URL(fileURLWithPath: outDir).appendingPathComponent("ChaosetIME.pdf").path)
print("已生成 \(outDir)/ChaosetIME.iconset 与 ChaosetIME.pdf")
