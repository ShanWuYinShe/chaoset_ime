// 生成 ChaosetIME 的图标资源：应用图标 .icns + 输入源菜单图标 .pdf。
// 用法: swift tools/make-icon.swift <输出目录>
//
// 全部用 AppKit 现场绘制，不依赖任何外部素材，也不复用第三方图标
// （2026-10-05 排障时放进 ~/Library/Input Methods 的「鼠须管」是官方
//  Rime/Squirrel 应用，与本项目无关，其 Rime.icns/rime.pdf 一律不可用）。
//
// 视觉：中国印章（朱文/白文语汇）——中国红底、白色双框、宋体「超」，
//       大尺寸再叠加云纹暗纹；小尺寸自动退化为干净的印章徽标。
import AppKit
import CoreGraphics

let outDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."
try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)

/// 宋体优先（中文印刷体的代表字形），取不到时退回系统中文字重
func sealFont(_ size: CGFloat) -> NSFont {
    for name in ["STSongti-SC-Bold", "Songti SC Bold", "STSongti-SC-Regular", "Songti SC", "PingFangSC-Semibold"] {
        if let f = NSFont(name: name, size: size) { return f }
    }
    return NSFont.systemFont(ofSize: size, weight: .semibold)
}

/// 云纹（如意云头）：三枚圆弧叠成一朵云，作为暗纹使用
func drawCloud(in rect: NSRect, _ s: CGFloat) {
    let baseY = rect.minY + s * 0.145
    let r = s * 0.085
    NSColor(white: 1, alpha: 0.16).setStroke()
    for (i, dx) in [-1.0, 0.0, 1.0].enumerated() {
        let path = NSBezierPath()
        path.lineWidth = max(0.6, s * 0.013)
        let radius = r * (i == 1 ? 1.3 : 0.9)
        path.appendArc(withCenter: NSPoint(x: rect.midX + CGFloat(dx) * r * 0.95, y: baseY),
                       radius: radius, startAngle: 0, endAngle: 180)
        path.stroke()
    }
}

/// 印章徽标：中国红底 + 白色双框 + 宋体「超」(+ 云纹暗纹)
func drawSeal(_ s: CGFloat, ornament: Bool) {
    let inset = s * 0.055
    let rect = NSRect(x: inset, y: inset, width: s - inset * 2, height: s - inset * 2)
    let body = NSBezierPath(roundedRect: rect, xRadius: s * 0.13, yRadius: s * 0.13)

    // 朱砂印泥：上浅下深
    let grad = NSGradient(colors: [
        NSColor(srgbRed: 0.80, green: 0.15, blue: 0.16, alpha: 1),
        NSColor(srgbRed: 0.52, green: 0.06, blue: 0.09, alpha: 1),
    ])!
    grad.draw(in: body, angle: -90)

    if ornament { drawCloud(in: rect, s) }

    // 印章双框（外粗内细）
    let outer = NSBezierPath(roundedRect: rect.insetBy(dx: s * 0.075, dy: s * 0.075),
                             xRadius: s * 0.085, yRadius: s * 0.085)
    outer.lineWidth = max(1, s * 0.022)
    NSColor(white: 1, alpha: 0.92).setStroke()
    outer.stroke()
    let inner = NSBezierPath(roundedRect: rect.insetBy(dx: s * 0.125, dy: s * 0.125),
                             xRadius: s * 0.06, yRadius: s * 0.06)
    inner.lineWidth = max(0.6, s * 0.010)
    NSColor(white: 1, alpha: 0.55).setStroke()
    inner.stroke()

    // 宋体「超」
    let text = NSAttributedString(string: "超", attributes: [
        .font: sealFont(s * 0.50),
        .foregroundColor: NSColor.white,
    ])
    let size = text.size()
    text.draw(at: NSPoint(x: (s - size.width) / 2, y: (s - size.height) / 2 + s * 0.035))
}

func png(_ px: Int) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                               isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    drawSeal(CGFloat(px), ornament: px >= 128)
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
    drawSeal(size, ornament: false)
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
