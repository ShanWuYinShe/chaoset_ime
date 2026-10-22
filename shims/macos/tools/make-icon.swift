// 生成 ChaosetIME 的图标资源：应用图标 .icns + 输入源模板图标 .tiff。
// 用法: swift tools/make-icon.swift <输出目录>
//
// 全部用 AppKit 现场绘制，不依赖任何外部素材，也不复用第三方图标
// （2026-10-05 排障时放进 ~/Library/Input Methods 的「鼠须管」是官方
//  Rime/Squirrel 应用，与本项目无关，其 Rime.icns/rime.pdf 一律不可用）。
//
// 两套视觉，用途完全不同：
// - **应用图标 .icns**：中国印章（朱文语汇）——中国红渐变底、白色双框、隶书「超」，
//   大尺寸四角叠回纹（雷纹）角饰。它是彩色的，用在访达/启动台/系统设置的应用列表。
// - **输入源图标 .tiff**：**模板图**，形状全由 alpha 表达、RGB 恒为 (0,0,0)。
//   系统在输入菜单、切换输入法 HUD 里按模板渲染（只取 alpha 再着色），
//   所以彩色图会被涂成一块实心方块——必须是模板图。
import AppKit
import CoreGraphics
import ImageIO

let outDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."
try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)

// MARK: - 应用图标（.icns，彩色印章）

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

/// 只画印章里的「超」字
func drawSealGlyph(_ s: CGFloat) {
    let text = NSAttributedString(string: "超", attributes: [
        .font: sealFont(s * 0.50),
        .foregroundColor: NSColor.white,
    ])
    let size = text.size()
    text.draw(at: NSPoint(x: (s - size.width) / 2, y: (s - size.height) / 2 + s * 0.035))
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

    drawSealGlyph(s)
}

/// 应用图标单帧：方形画布上居中画印章徽标。
func appIconRep(_ px: Int) -> NSBitmapImageRep {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                               isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    drawSeal(CGFloat(px), ornament: px >= 128)
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

// MARK: - 输入源图标（模板图，系统规格）

// 系统输入法（SCIM pinyin.tiff / Squirrel rime.pdf 同源做法）的输入源图标规格：
//   帧：16×16@72dpi + 32×32@144dpi 两帧，LZW 压缩，RGB 通道恒为 (0,0,0)，
//       形状（圆角方块 + 挖空字形）完全由 alpha 表达。
//   系统在输入菜单与「切换输入法」HUD 里按模板图渲染：只读 alpha 再着色，
//   所以彩色/白色 RGB 的图会被涂成一整块。2026-10-22 实测对照
//   /System/Library/Input Methods/SCIM.app/…/pinyin.tiff 逐像素确认。
//
// 形状：16×16 满幅圆角方块（圆角半径 ≈ 边长的 0.18，与系统图的 alpha 轮廓一致：
//   首行 alpha ≈ [6,145,237,255…]），中心用系统字体挖空「Cs」（Chaoset 字标）。

/// 输入源模板图单帧：满幅圆角方块 + 挖空字形。
/// - 底色画成纯黑：模板图只取 alpha，RGB 值本身不参与渲染，但必须统一为
///   (0,0,0)，否则一旦被按彩色渲染（个别系统设置页面）就会露出杂色。
func inputSourceRep(_ px: Int) -> NSBitmapImageRep {
    let side = CGFloat(px)
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                               isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let ctx = NSGraphicsContext.current!.cgContext
    ctx.setShouldAntialias(true)

    // 满幅圆角方块：不留边距，与系统图一致（系统图 alpha 覆盖到第 0/15 行）
    let body = NSBezierPath(roundedRect: NSRect(x: 0, y: 0, width: side, height: side),
                            xRadius: side * 0.18, yRadius: side * 0.18)
    NSColor.black.setFill()
    body.fill()

    // 字形挖空：destinationOut 在位图 alpha 里挖出「Cs」。
    // 字号取 0.62em、bold：16×16 下仍是可辨识的两字形（实测比 0.58/0.72 更清楚）。
    let font = NSFont.systemFont(ofSize: side * 0.62, weight: .bold)
    let text = NSAttributedString(string: "Cs", attributes: [
        .font: font,
        .foregroundColor: NSColor.white,
    ])
    let ts = text.size()
    ctx.setBlendMode(.destinationOut)
    text.draw(at: NSPoint(x: (side - ts.width) / 2, y: (side - ts.height) / 2))
    ctx.setBlendMode(.normal)

    NSGraphicsContext.restoreGraphicsState()
    return rep
}

/// 双帧模板 TIFF（16×16@72dpi + 32×32@144dpi，LZW）。
/// 用 ImageIO 直接写：DPI 逐帧写进 TIFF 标签，NSImage 才能按"点尺寸"正确挑帧；
/// 两帧的像素尺寸比必须是 2:1（tiffutil -cathidpicheck 的 hidpi 约定）。
func writeInputSourceTiff(_ path: String) {
    let frames: [(px: Int, dpi: Int)] = [(16, 72), (32, 144)]
    guard let dest = CGImageDestinationCreateWithURL(URL(fileURLWithPath: path) as CFURL,
                                                     "public.tiff" as CFString,
                                                     frames.count, nil) else {
        fatalError("无法创建 TIFF: \(path)")
    }
    for frame in frames {
        guard let cg = inputSourceRep(frame.px).cgImage else {
            fatalError("TIFF 编码失败 \(frame.px)x\(frame.px)")
        }
        let props: [CFString: Any] = [
            kCGImagePropertyDPIWidth: frame.dpi,
            kCGImagePropertyDPIHeight: frame.dpi,
            kCGImagePropertyTIFFDictionary: [
                kCGImagePropertyTIFFXResolution: frame.dpi,
                kCGImagePropertyTIFFYResolution: frame.dpi,
                kCGImagePropertyTIFFResolutionUnit: 2, // 2 = 英寸
                kCGImagePropertyTIFFCompression: 5,     // 5 = LZW，与系统图一致
                kCGImagePropertyTIFFSoftware: "chaoset-make-icon",
                kCGImagePropertyTIFFImageDescription:
                    frame.px == 16 ? "ChaosetIME-input.png" : "ChaosetIME-input@2x.png",
            ],
        ]
        CGImageDestinationAddImage(dest, cg, props as CFDictionary)
    }
    guard CGImageDestinationFinalize(dest) else { fatalError("TIFF 写盘失败: \(path)") }
}

// MARK: - 产出

let iconset = URL(fileURLWithPath: outDir).appendingPathComponent("ChaosetIME.iconset")
try? FileManager.default.removeItem(at: iconset)
try! FileManager.default.createDirectory(at: iconset, withIntermediateDirectories: true)
for base in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let name = scale == 1 ? "icon_\(base)x\(base).png" : "icon_\(base)x\(base)@2x.png"
        let png = appIconRep(base * scale).representation(using: .png, properties: [:])!
        try! png.write(to: iconset.appendingPathComponent(name))
    }
}

// 输入源图标：plist 的四个图标键（tsInputMethodIconFileKey / …AlternateIconFileKey /
// tsInputModeMenuIconFileKey / tsInputModePaletteIconFileKey）全部指向这一个文件。
writeInputSourceTiff(URL(fileURLWithPath: outDir)
    .appendingPathComponent("ChaosetIME-input.tiff").path)

print("已生成 \(outDir)/ChaosetIME.iconset 与 ChaosetIME-input.tiff（16×16+32×32 模板图）")
