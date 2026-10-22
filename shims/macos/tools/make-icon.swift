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

// 系统输入法（SCIM pinyin.tiff / ABC / KoreanIM 等）的输入源图标实测规格：
//   - 尺寸与比例：扁平 keycap（宽 22 pt × 高 16 pt，长宽比 1.375，圆角半径 ≈ 3.2 pt）；
//   - 双帧 TIFF：22×16@72dpi (1x) + 44×32@144dpi (2x)，LZW 压缩；
//   - **ExtraSamples = 2（UNASSOCIATED，非预乘 alpha）**。用 CGImageDestination
//     直接写多帧会写成 1（预乘），系统按模板渲染时边缘颜色被重复相乘；
//   - RGB 通道恒为 (0,0,0)，形状完全由 alpha 表达（系统按模板图只取 alpha 着色）；
//   - 按键满幅扁平圆角（宽 22 pt × 高 16 pt），圆角半径 0.20 × 高度；
//   - 字形挖空单字符「C」（占高度 ≈ 0.72×高度），与 ABC「A」、简体拼音「拼」的扁平按键视觉重量一致。
//
// 实现要点：直接在小画布上画字体，笔画会断裂且边缘锯齿严重。
// 先在 8× 超采样画布上用 CGBitmapContext 画「圆角按键挖去字形」，
// 再降采样到目标尺寸，最后逐像素写入 NSBitmapImageRep。
// 特别注意：CGBitmapContext 原点在左下角，读出字节缓冲时第 0 行在底部，
// 降采样写入 NSBitmapImageRep（第 0 行在顶部）时进行 Y 轴反转，防止字形颠倒。

func inputSourceRep(ptW: Int, ptH: Int, scale: Int) -> NSBitmapImageRep {
    let pxW = ptW * scale
    let pxH = ptH * scale
    let ss = 8                      // 针对当前 scale 的超采样倍数
    let bigW = pxW * ss
    let bigH = pxH * ss
    let bytesPerRow = bigW * 4

    let cs = CGColorSpace(name: CGColorSpace.sRGB)!
    guard let ctx = CGContext(data: nil, width: bigW, height: bigH,
                              bitsPerComponent: 8, bytesPerRow: bytesPerRow,
                              space: cs,
                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else {
        fatalError("无法创建 CGContext（输入源图标）")
    }
    ctx.setShouldAntialias(true)
    ctx.clear(CGRect(x: 0, y: 0, width: bigW, height: bigH))

    // 1) 满幅圆角扁平 keycap（圆角 0.20×高度，在 16pt 下约 3.2pt）
    let body = CGPath(roundedRect: CGRect(x: 0, y: 0, width: bigW, height: bigH),
                      cornerWidth: CGFloat(bigH) * 0.20, cornerHeight: CGFloat(bigH) * 0.20,
                      transform: nil)
    ctx.setFillColor(red: 0, green: 0, blue: 0, alpha: 1)
    ctx.addPath(body)
    ctx.fillPath()

    // 2) 挖空单字符「C」：墨迹高度 ≈ 0.72×高度
    ctx.setBlendMode(.destinationOut)
    let targetH = CGFloat(bigH) * 0.72
    func metrics(_ size: CGFloat) -> (line: CTLine, bounds: CGRect) {
        let f = CTFontCreateWithName("Helvetica-Bold" as CFString, size, nil)
        let a = NSAttributedString(string: "C", attributes: [
            .font: f, .foregroundColor: NSColor.white,
        ])
        let l = CTLineCreateWithAttributedString(a)
        return (l, CTLineGetBoundsWithOptions(l, .useGlyphPathBounds))
    }
    var lo = CGFloat(bigH) * 0.3
    var hi = CGFloat(bigH) * 4.0
    var bestLine = metrics(CGFloat(bigH)).line
    var bestBounds = metrics(CGFloat(bigH)).bounds
    for _ in 0..<32 {
        let mid = (lo + hi) / 2
        let (l, b) = metrics(mid)
        if b.height < targetH && b.width < CGFloat(bigW) * 0.82 { lo = mid } else { hi = mid }
        bestLine = l
        bestBounds = b
    }
    let line = bestLine
    let glyphBounds = bestBounds
    ctx.textPosition = CGPoint(x: (CGFloat(bigW) - glyphBounds.width) / 2 - glyphBounds.minX,
                               y: (CGFloat(bigH) - glyphBounds.height) / 2 - glyphBounds.minY)
    CTLineDraw(line, ctx)
    ctx.setBlendMode(.normal)

    // 3) 取 CGImage，用 alphaOnly 灰度上下文读出干净的 alpha 通道
    guard let bigImage = ctx.makeImage() else { fatalError("超采样画布取图失败") }
    var alphaBuf = [UInt8](repeating: 0, count: bigW * bigH)
    alphaBuf.withUnsafeMutableBytes { raw in
        let readCtx = CGContext(data: raw.baseAddress, width: bigW, height: bigH,
                                bitsPerComponent: 8, bytesPerRow: bigW,
                                space: CGColorSpaceCreateDeviceGray(),
                                bitmapInfo: CGImageAlphaInfo.alphaOnly.rawValue)!
        readCtx.draw(bigImage, in: CGRect(x: 0, y: 0, width: bigW, height: bigH))
    }

    // 4) 降采样并写入 NSBitmapImageRep
    // 注意：readCtx 原点在左下角，alphaBuf 第 0 行是图像底部；
    // NSBitmapImageRep 第 0 行是图像顶部，因此读取时做 Y 轴翻转。
    guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pxW, pixelsHigh: pxH,
                                     bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                                     isPlanar: false, colorSpaceName: .deviceRGB,
                                     bytesPerRow: pxW * 4, bitsPerPixel: 32),
          let data = rep.bitmapData else {
        fatalError("无法创建输入源图标位图")
    }
    let bpr = rep.bytesPerRow
    let count = Double(ss * ss)
    for y in 0..<pxH {
        let srcY = (pxH - 1 - y) * ss
        for x in 0..<pxW {
            var sum = 0.0
            for dy in 0..<ss {
                for dx in 0..<ss {
                    sum += Double(alphaBuf[(srcY + dy) * bigW + (x * ss + dx)])
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
    let frames: [(ptW: Int, ptH: Int, scale: Int, dpi: Int)] = [
        (22, 16, 1, 72),
        (22, 16, 2, 144)
    ]
    let tmpDir = NSTemporaryDirectory() + "chaoset-icon-\(ProcessInfo.processInfo.processIdentifier)"
    try? FileManager.default.createDirectory(atPath: tmpDir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(atPath: tmpDir) }

    // 每一步先把像素写成 PNG，再由 tiffutil 合成多帧 TIFF。
    var pngPaths: [String] = []
    for frame in frames {
        let pxW = frame.ptW * frame.scale
        let pxH = frame.ptH * frame.scale
        guard let rep = inputSourceRep(ptW: frame.ptW, ptH: frame.ptH, scale: frame.scale).cgImage else {
            fatalError("输入源图标取 CGImage 失败")
        }
        let pngPath = tmpDir + "/icon_\(pxW)x\(pxH).png"
        let url = URL(fileURLWithPath: pngPath)
        guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil) else {
            fatalError("创建 PNG 失败: \(pngPath)")
        }
        CGImageDestinationAddImage(dest, rep, [
            kCGImagePropertyDPIWidth: frame.dpi,
            kCGImagePropertyDPIHeight: frame.dpi,
        ] as CFDictionary)
        guard CGImageDestinationFinalize(dest) else { fatalError("PNG 写盘失败: \(pngPath)") }
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

    // 改写多帧 TIFF 中每帧的 XResolution/YResolution 标签（282/283）。
    resolveTagsInTiff(path: path, firstDPI: 72, secondDPI: 144)
}

/// 直接改写多帧 TIFF 中每帧的 XResolution/YResolution（标签 282/283）。
/// 用 tiffutil 保证 ExtraSamples=2 与 LZW 结构，只补它没做对的 DPI 标记。
private func resolveTagsInTiff(path: String, firstDPI: Double, secondDPI: Double) {
    guard var data = FileManager.default.contents(atPath: path) else { return }
    let isLE = data.count >= 2 && data[0] == 0x49 && data[1] == 0x49
    func rd16(_ o: Int) -> Int { isLE ? Int(data[o]) | Int(data[o+1]) << 8 : Int(data[o]) << 8 | Int(data[o+1]) }
    func rd32(_ o: Int) -> Int { isLE
        ? Int(data[o]) | Int(data[o+1]) << 8 | Int(data[o+2]) << 16 | Int(data[o+3]) << 24
        : Int(data[o]) << 24 | Int(data[o+1]) << 16 | Int(data[o+2]) << 8 | Int(data[o+3]) }
    func wrRational(_ o: Int, _ v: Double) {
        let num = UInt32((v * 100).rounded())
        let den: UInt32 = 100
        if isLE {
            data[o] = UInt8(num & 0xFF); data[o+1] = UInt8((num >> 8) & 0xFF)
            data[o+2] = UInt8((num >> 16) & 0xFF); data[o+3] = UInt8((num >> 24) & 0xFF)
            data[o+4] = UInt8(den & 0xFF); data[o+5] = UInt8((den >> 8) & 0xFF)
            data[o+6] = UInt8((den >> 16) & 0xFF); data[o+7] = UInt8((den >> 24) & 0xFF)
        } else {
            data[o] = UInt8((num >> 24) & 0xFF); data[o+1] = UInt8((num >> 16) & 0xFF)
            data[o+2] = UInt8((num >> 8) & 0xFF); data[o+3] = UInt8(num & 0xFF)
            data[o+4] = UInt8((den >> 24) & 0xFF); data[o+5] = UInt8((den >> 16) & 0xFF)
            data[o+6] = UInt8((den >> 8) & 0xFF); data[o+7] = UInt8(den & 0xFF)
        }
    }

    var offset = rd32(4)
    var frameIndex = 0
    let targetDPI = [firstDPI, secondDPI]
    while offset > 0 && offset + 2 <= data.count && frameIndex < targetDPI.count {
        let count = rd16(offset)
        var i = 0
        while i < count {
            let entry = offset + 2 + i * 12
            guard entry + 12 <= data.count else { break }
            let tag = rd16(entry)
            if tag == 282 || tag == 283 {          // XResolution / YResolution
                let valueOffset = rd32(entry + 8)  // RATIONAL 直接存在值域里
                if valueOffset + 8 <= data.count {
                    wrRational(valueOffset, targetDPI[frameIndex])
                }
            }
            i += 1
        }
        frameIndex += 1
        offset = rd32(offset + 2 + count * 12)     // 下一帧 IFD 偏移
    }
    try? data.write(to: URL(fileURLWithPath: path))
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
    .appendingPathComponent("ChaosetIME-icon2.tiff").path)

print("已生成 \(outDir)/ChaosetIME.iconset 与 ChaosetIME-icon2.tiff（22×16+44×32 扁平按键模板图）")