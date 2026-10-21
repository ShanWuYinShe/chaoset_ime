// 生成 ChaosetIME 的图标资源：应用图标 .icns + 输入源 keycap 图标（menu/palette）。
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
import ImageIO

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
    drawSealGlyph(s)
}

/// 只画印章里的「超」字（模板图标做 alpha 挖空时复用）
func drawSealGlyph(_ s: CGFloat) {
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

/// 输入源图标（与 macOS 系统输入法一致的扁平 keycap 风格）：
/// 20×16 圆角矩形 + 系统字体「Cs」（Chaoset）字标。
///
/// 2026-10-22 用户反馈：方形印章图标在系统设置/输入菜单里和 ABC、简体拼音的
/// 扁平 keycap 尺寸/风格都不一致，且菜单里显示空白。故改成 keycap 风格，并分别
/// 产出两个变体：
/// - **menu** 变体（tsInputModeMenuIconFileKey / …Alternate…）：字形挖成透明。
///   输入菜单按模板图渲染（只取 alpha 通道），挖空后在深色菜单里呈现为白底
///   keycap + 深色「Cs」（与 ABC 的「A」、简体拼音的「拼」一致）。
/// - **palette** 变体（tsInputModePaletteIconFileKey）：白底 + 深色字形，供按
///   彩色渲染的界面（系统设置「所有输入法」列表）使用，两种渲染方式都不露馅。
func drawKeycap(w: CGFloat, h: CGFloat, knockout: Bool) {
    let rect = NSRect(x: 0.5, y: 0.5, width: w - 1, height: h - 1)
    let path = NSBezierPath(roundedRect: rect, xRadius: h * 0.22, yRadius: h * 0.22)
    NSColor.white.setFill()
    path.fill()
    if !knockout {
        NSColor(white: 0, alpha: 0.35).setStroke()
        path.lineWidth = max(0.8, h * 0.05)
        path.stroke()
    }
    func attrs(_ size: CGFloat) -> [NSAttributedString.Key: Any] {
        [.font: NSFont.systemFont(ofSize: size, weight: .semibold),
         .foregroundColor: knockout ? NSColor.white : NSColor(white: 0.10, alpha: 1)]
    }
    var size = h * 0.62
    var text = NSAttributedString(string: "Cs", attributes: attrs(size))
    while text.size().width > w * 0.74 && size > h * 0.30 {
        size -= 0.5
        text = NSAttributedString(string: "Cs", attributes: attrs(size))
    }
    let ts = text.size()
    let origin = NSPoint(x: (w - ts.width) / 2, y: (h - ts.height) / 2 + h * 0.02)
    if knockout {
        // destinationOut：在位图 alpha 里挖出字形
        NSGraphicsContext.current?.cgContext.setBlendMode(.destinationOut)
        text.draw(at: origin)
        NSGraphicsContext.current?.cgContext.setBlendMode(.normal)
    } else {
        text.draw(at: origin)
    }
}

func keycapRep(w: CGFloat, h: CGFloat, knockout: Bool) -> NSBitmapImageRep {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(w), pixelsHigh: Int(h),
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                               isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    drawKeycap(w: w, h: h, knockout: knockout)
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

/// 多分辨率 TIFF（20×16 / 40×32 / 60×48），系统按 Retina 需要挑页面。
/// 用 TIFF 而非 PDF：TextInputSources.h 明确 "TIFF files are the normal icon
/// format for input modes"，且系统取图标走 NSBundle imageForResource:。
func keycapTiff(_ path: String, knockout: Bool) {
    let url = URL(fileURLWithPath: path)
    let sizes: [(CGFloat, CGFloat)] = [(20, 16), (40, 32), (60, 48)]
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.tiff" as CFString, sizes.count, nil) else {
        fatalError("无法创建 TIFF: \(path)")
    }
    for (w, h) in sizes {
        guard let cg = keycapRep(w: w, h: h, knockout: knockout).cgImage else {
            fatalError("TIFF 编码失败 \(w)x\(h)")
        }
        CGImageDestinationAddImage(dest, cg, nil)
    }
    guard CGImageDestinationFinalize(dest) else { fatalError("TIFF 写盘失败: \(path)") }
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
keycapTiff(URL(fileURLWithPath: outDir).appendingPathComponent("ChaosetIME-menu.tiff").path, knockout: true)
keycapTiff(URL(fileURLWithPath: outDir).appendingPathComponent("ChaosetIME-palette.tiff").path, knockout: false)
print("已生成 \(outDir)/ChaosetIME.iconset、ChaosetIME-menu.tiff 与 ChaosetIME-palette.tiff")
