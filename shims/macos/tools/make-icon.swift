// 生成 ChaosetIME 的图标资源：应用图标 .icns + 输入源菜单图标 .pdf。
// 用法: swift tools/make-icon.swift <输出目录>
//
// 全部用 AppKit 现场绘制，不依赖任何外部素材，也不复用第三方图标
// （2026-10-05 排障时放进 ~/Library/Input Methods 的「鼠须管」是官方
//  Rime/Squirrel 应用，与本项目无关，其 Rime.icns/rime.pdf 一律不可用）。
//
// 视觉：中国印章（朱文/白文语汇）——中国红底、白色双框、隶书「超」，
//       大尺寸四角叠加回纹（雷纹）角饰；小尺寸自动退化为干净的印章徽标。
import AppKit
import CoreGraphics

let outDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."
try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)

/// 隶书优先（蚕头燕尾，最有"中国印章"味），依次退回楷体、宋体、系统中文字重。
/// 本机可用性已实测：Libian SC 有、Kaiti SC 有、Songti SC 有（见 shims/macos/README）。
func sealFont(_ size: CGFloat) -> NSFont {
    for name in ["Libian SC", "Kaiti SC", "STKaiti", "STSongti-SC-Bold", "Songti SC", "PingFangSC-Semibold"] {
        if let f = NSFont(name: name, size: size) { return f }
    }
    return NSFont.systemFont(ofSize: size, weight: .semibold)
}

/// 回纹（雷纹）角饰：四角各画三层嵌套直角，是中国器物边缘的典型纹样
func drawCornerMeander(in rect: NSRect, _ s: CGFloat) {
    let lw = max(0.8, s * 0.014)
    NSColor(white: 1, alpha: 0.55).setStroke()
    let base = rect.insetBy(dx: s * 0.155, dy: s * 0.155)
    let len = s * 0.10
    for (cx, cy, dx, dy) in [(base.minX, base.minY, 1.0, 1.0), (base.maxX, base.minY, -1.0, 1.0),
                             (base.minX, base.maxY, 1.0, -1.0), (base.maxX, base.maxY, -1.0, -1.0)] {
        for k in 1...3 {
            let g = CGFloat(k) * s * 0.028
            let path = NSBezierPath()
            path.lineWidth = lw
            path.move(to: NSPoint(x: cx + dx * g, y: cy + dy * (g + len)))
            path.line(to: NSPoint(x: cx + dx * g, y: cy + dy * g))
            path.line(to: NSPoint(x: cx + dx * (g + len), y: cy + dy * g))
            path.stroke()
        }
    }
}

/// 印章徽标：中国红底 + 白色双框 + 隶书「超」（大尺寸再加四角回纹）
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

    if ornament { drawCornerMeander(in: rect, s) }

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

    // 隶书「超」
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
