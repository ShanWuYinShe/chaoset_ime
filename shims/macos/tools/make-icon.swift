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


/// 应用图标单帧：方形画布上居中画印章徽标（彩色，用于访达/启动台/系统设置应用列表）。
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

// MARK: - 输入源图标（模板图，逐像素对齐系统规格）

// 系统输入法（SCIM pinyin.tiff / KoreanIM 等）的输入源图标实测规格
//（2026-10-22 用 16×16 与 32×32 两帧逐像素标定）：
//   - 双帧 TIFF：16×16@72dpi + 32×32@144dpi，LZW 压缩；
//   - **ExtraSamples = 2（UNASSOCIATED，非预乘 alpha）**。用 CGImageDestination
//     直接写多帧会写成 1（预乘），系统按模板渲染时边缘颜色被重复相乘，
//     表现为图标偏亮/偏暗、与别的输入法不一致；
//   - RGB 通道恒为 (0,0,0)，形状完全由 alpha 表达（系统按模板图只取 alpha 着色）；
//   - 方块**满幅**，圆角 ≈ 0.13×边长（首行 alpha = [6,145,237,255…]）；
//   - 字形挖空占第 2–12 行（11/16 ≈ 0.69×边长），且**只用单个字符**：
//     ABC 是「A」、简体拼音是「拼」、韩文是「한」。两字符（此前的「Cs」）
//     在 16px 下必然模糊且视觉重量不足，这正是用户反馈「风格/大小不一样」的原因。
//
// 实现要点：直接在 16×16 上画字体，笔画只有 1px 会断裂（实测「C」被切成 4 段）。
// 所以先在 8× 超采样画布上用 CGBitmapContext 画「圆角方块挖去字形」，
// 再降采样到目标尺寸，最后逐像素写入 NSBitmapImageRep。

func inputSourceRep(_ px: Int) -> NSBitmapImageRep {
    let ss = 8                      // 超采样倍数
    let big = px * ss
    let sideBig = CGFloat(big)
    let bytesPerRow = big * 4

    let cs = CGColorSpace(name: CGColorSpace.sRGB)!
    guard let ctx = CGContext(data: nil, width: big, height: big,
                              bitsPerComponent: 8, bytesPerRow: bytesPerRow,
                              space: cs,
                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else {
        fatalError("无法创建 CGContext（输入源图标）")
    }
    ctx.setShouldAntialias(true)
    ctx.clear(CGRect(x: 0, y: 0, width: sideBig, height: sideBig))

    // 1) 满幅圆角方块（圆角 0.13×边长）
    let body = CGPath(roundedRect: CGRect(x: 0, y: 0, width: sideBig, height: sideBig),
                      cornerWidth: sideBig * 0.13, cornerHeight: sideBig * 0.13,
                      transform: nil)
    ctx.setFillColor(red: 0, green: 0, blue: 0, alpha: 1)
    ctx.addPath(body)
    ctx.fillPath()

    // 2) 挖空单字符「C」：墨迹高度 ≈ 0.69×边长（二分调字号得到）
    ctx.setBlendMode(.destinationOut)
    let targetH = sideBig * 0.69
    var lo = sideBig * 0.3
    var hi = sideBig * 3.0
    var fontSize = sideBig
    for _ in 0..<30 {
        let mid = (lo + hi) / 2
        let f = CTFontCreateWithName("Helvetica-Bold" as CFString, mid, nil)
        let a = NSAttributedString(string: "C", attributes: [
            .font: f, .foregroundColor: NSColor.white,
        ])
        if a.size().height < targetH { lo = mid } else { hi = mid }
        fontSize = mid
    }
    let font = CTFontCreateWithName("Helvetica-Bold" as CFString, fontSize, nil)
    let text = NSAttributedString(string: "C", attributes: [
        .font: font, .foregroundColor: NSColor.white,
    ])
    let line = CTLineCreateWithAttributedString(text)
    let glyphBounds = CTLineGetBoundsWithOptions(line, .useGlyphPathBounds)
    ctx.textPosition = CGPoint(x: (sideBig - glyphBounds.width) / 2 - glyphBounds.minX,
                               y: (sideBig - glyphBounds.height) / 2 - glyphBounds.minY)
    CTLineDraw(line, ctx)
    ctx.setBlendMode(.normal)

    // 3) 取 CGImage，用 alphaOnly 灰度上下文读出干净的 alpha 通道
    //（直接读 CGContext 的 data 会踩字节序/预乘的坑，实测 alpha 全 0）
    guard let bigImage = ctx.makeImage() else { fatalError("超采样画布取图失败") }
    var alphaBuf = [UInt8](repeating: 0, count: big * big)
    alphaBuf.withUnsafeMutableBytes { raw in
        let readCtx = CGContext(data: raw.baseAddress, width: big, height: big,
                                bitsPerComponent: 8, bytesPerRow: big,
                                space: CGColorSpaceCreateDeviceGray(),
                                bitmapInfo: CGImageAlphaInfo.alphaOnly.rawValue)!
        readCtx.draw(bigImage, in: CGRect(x: 0, y: 0, width: big, height: big))
    }

    // 4) 降采样并直接写 bitmapData（6) 说明：用 setColor 写入时 RGB=0 的
    //    预乘像素会被算成整块透明（实测 alpha 全 0），直接写字节最可靠。
    guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
                                     bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                                     isPlanar: false, colorSpaceName: .deviceRGB,
                                     bytesPerRow: px * 4, bitsPerPixel: 32),
          let data = rep.bitmapData else {
        fatalError("无法创建输入源图标位图")
    }
    let bpr = rep.bytesPerRow
    let count = Double(ss * ss)
    for y in 0..<px {
        for x in 0..<px {
            var sum = 0.0
            for dy in 0..<ss {
                for dx in 0..<ss {
                    sum += Double(alphaBuf[(y * ss + dy) * big + (x * ss + dx)])
                }
            }
            let a = UInt8(max(0, min(255, (sum / count).rounded())))
            let off = y * bpr + x * 4
            // 模板图：RGB 恒为 0，只有 alpha 有信息
            data[off] = 0
            data[off + 1] = 0
            data[off + 2] = 0
            data[off + 3] = a
        }
    }
    return rep
}

func writeInputSourceTiff(_ path: String) {
    let frames: [(px: Int, dpi: Int)] = [(16, 72), (32, 144)]
    let tmpDir = NSTemporaryDirectory() + "chaoset-icon-\(ProcessInfo.processInfo.processIdentifier)"
    try? FileManager.default.createDirectory(atPath: tmpDir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(atPath: tmpDir) }

    // 每一步先把像素写成 PNG，再由 tiffutil 合成多帧 TIFF。
    var pngPaths: [String] = []
    for frame in frames {
        let png = inputSourceRep(frame.px).representation(using: .png, properties: [:])!
        let pngPath = tmpDir + "/icon_\(frame.px).png"
        try! png.write(to: URL(fileURLWithPath: pngPath))
        pngPaths.append(pngPath)
    }

    // tiffutil -cathidpicheck <1x> <2x> -out <目标>：写出的 TIFF 与系统图同规格
    //（双帧 + 逐帧 DPI + ExtraSamples=2 + LZW）。
    let proc = Process()
    proc.executableURL = URL(fileURLWithPath: "/usr/bin/tiffutil")
    proc.arguments = ["-cathidpicheck", pngPaths[0], pngPaths[1], "-out", path]
    let pipe = Pipe()
    proc.standardError = pipe
    proc.standardOutput = pipe
    try! proc.run()
    proc.waitUntilExit()
    if proc.terminationStatus != 0 {
        let msg = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
        fatalError("tiffutil 合成 TIFF 失败: \(msg)")
    }
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