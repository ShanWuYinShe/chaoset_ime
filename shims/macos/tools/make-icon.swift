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

// MARK: - 应用图标（.icns，日式温润白瓷与晨曦焦糖赤陶徽章）

/// 应用图标：晨曦赤陶温润渐变底板 + 和纸微米光环 + 3D 白瓷浮雕「C」+ 晨曦暖金四芒星 (✦)。
/// 彻底根除冷电光蓝极客风，全平台同构日式手账与器物生活美学。
func drawAppIcon(_ s: CGFloat) {
    let inset = s * 0.08
    let rect = CGRect(x: inset, y: inset, width: s - inset * 2, height: s - inset * 2)
    let squircleR = rect.width * 0.224
    guard let ctx = NSGraphicsContext.current?.cgContext else { return }

    // 1. 底板柔和环境多层阴影
    if s >= 128 {
        ctx.saveGState()
        ctx.setShadow(offset: CGSize(width: 0, height: -s * 0.035), blur: s * 0.05, color: NSColor(srgbRed: 0.25, green: 0.10, blue: 0.04, alpha: 0.35).cgColor)
        let bgPath = CGPath(roundedRect: rect, cornerWidth: squircleR, cornerHeight: squircleR, transform: nil)
        ctx.addPath(bgPath)
        ctx.fillPath()
        ctx.restoreGState()
    }

    // 2. 底板晨曦焦糖与赤陶渐变 (Warm Amber -> Caramel Amber -> Deep Terracotta)
    ctx.saveGState()
    let bgPath = CGPath(roundedRect: rect, cornerWidth: squircleR, cornerHeight: squircleR, transform: nil)
    ctx.addPath(bgPath)
    ctx.clip()
    let bgGrad = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(),
                            colors: [
                                NSColor(srgbRed: 0.94, green: 0.56, blue: 0.26, alpha: 1.0).cgColor,  // #F08F42
                                NSColor(srgbRed: 0.82, green: 0.42, blue: 0.16, alpha: 1.0).cgColor,  // #D16B29
                                NSColor(srgbRed: 0.60, green: 0.25, blue: 0.08, alpha: 1.0).cgColor   // #994014
                            ] as CFArray, locations: [0.0, 0.48, 1.0])!
    ctx.drawLinearGradient(bgGrad, start: CGPoint(x: rect.minX, y: rect.maxY), end: CGPoint(x: rect.maxX, y: rect.minY), options: [])

    // 3. 背景温润和纸微米光环 (大于 64 像素时呈现精密纹理，小尺寸保持纯净)
    if s >= 64 {
        ctx.setLineWidth(max(0.8, s * 0.005))
        ctx.setStrokeColor(NSColor(srgbRed: 1.0, green: 0.95, blue: 0.88, alpha: 0.15).cgColor)
        for rStep in [0.28, 0.44, 0.60] {
            let arcR = rect.width * CGFloat(rStep)
            let arcRect = CGRect(x: rect.midX - arcR, y: rect.midY - arcR, width: arcR * 2, height: arcR * 2)
            ctx.addEllipse(in: arcRect)
            ctx.strokePath()
        }
    }

    // 顶沿透光玻璃微高光细线
    let rim = CGPath(roundedRect: rect.insetBy(dx: 1, dy: 1), cornerWidth: squircleR - 1, cornerHeight: squircleR - 1, transform: nil)
    ctx.setLineWidth(max(1.0, s * 0.008))
    ctx.setStrokeColor(NSColor(srgbRed: 1.0, green: 0.96, blue: 0.90, alpha: 0.50).cgColor)
    ctx.addPath(rim)
    ctx.strokePath()
    ctx.restoreGState()

    // 4. 「C」核心白瓷微浮雕字母排版
    let cFontSize = rect.height * 0.54
    let cFont = NSFont(name: "AvenirNext-Heavy", size: cFontSize)
        ?? NSFont(name: "HelveticaNeue-Bold", size: cFontSize)
        ?? NSFont.boldSystemFont(ofSize: cFontSize)

    let cShadow = NSShadow()
    if s >= 64 {
        cShadow.shadowBlurRadius = max(2.5, s * 0.035)
        cShadow.shadowOffset = NSSize(width: 0, height: -max(1.2, s * 0.016))
        cShadow.shadowColor = NSColor(srgbRed: 0.35, green: 0.12, blue: 0.04, alpha: 0.65)
    }

    let cAttr: [NSAttributedString.Key: Any] = [
        .font: cFont,
        .foregroundColor: NSColor(srgbRed: 0.98, green: 0.97, blue: 0.95, alpha: 1.0),
        .shadow: cShadow
    ]
    let cText = NSAttributedString(string: "C", attributes: cAttr)
    let cSize = cText.size()

    let cX = rect.midX - cSize.width / 2 - s * 0.02
    let cY = rect.midY - cSize.height / 2 + s * 0.012

    cText.draw(at: NSPoint(x: cX, y: cY))

    // 5. 镶嵌晨曦暖金四芒星 (✦)
    let starFontSize = cFontSize * 0.40
    let starFont = NSFont.systemFont(ofSize: starFontSize, weight: .bold)
    let starShadow = NSShadow()
    if s >= 64 {
        starShadow.shadowBlurRadius = max(2.0, s * 0.03)
        starShadow.shadowOffset = NSSize(width: 0, height: 0)
        starShadow.shadowColor = NSColor(srgbRed: 1.0, green: 0.78, blue: 0.38, alpha: 0.75)
    }
    let starAttr: [NSAttributedString.Key: Any] = [
        .font: starFont,
        .foregroundColor: NSColor(srgbRed: 1.0, green: 0.88, blue: 0.54, alpha: 1.0), // #FFE08A
        .shadow: starShadow
    ]
    let starText = NSAttributedString(string: "✦", attributes: starAttr)
    let starX = cX + cSize.width * 0.78
    let starY = cY + cSize.height * 0.56
    starText.draw(at: NSPoint(x: starX, y: starY))

    // 6. 底部微标「CHAOS」字标（在大于等于 128 像素尺寸显示，小尺寸自动隐去以保清晰）
    if s >= 128 {
        let subFont = NSFont(name: "AvenirNext-Bold", size: s * 0.040)
            ?? NSFont.systemFont(ofSize: s * 0.040, weight: .bold)
        let subAttr: [NSAttributedString.Key: Any] = [
            .font: subFont,
            .foregroundColor: NSColor(srgbRed: 1.0, green: 0.94, blue: 0.88, alpha: 0.80),
            .kern: 3.5
        ]
        let subText = NSAttributedString(string: "CHAOS", attributes: subAttr)
        let subSize = subText.size()
        subText.draw(at: NSPoint(x: (s - subSize.width) / 2, y: rect.minY + rect.height * 0.10))
    }
}

/// 应用图标单帧：方形画布上居中画 C 键帽徽标（彩色，用于访达/启动台/活动监视器/系统设置应用列表）。
func appIconRep(_ px: Int) -> NSBitmapImageRep {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                               isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    drawAppIcon(CGFloat(px))
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

// MARK: - 输入源图标（模板图，逐像素对齐系统规格）

// ⚠️ 用户最终决定（2026-10-25），禁止回退：
//    输入源图标统一为 **22×16 宽扁 keycap**（满幅实心圆角按键 + alpha 挖空单字符「C」），
//    五个 TIS 图标键全部指向这一份模板图 —— System Settings、菜单栏（系统栏）、
//    快捷键切换 HUD 共用同一形态。
//
//    为什么不再按场景拆分（2026-10-25 探针实测，本机 macOS 15.8.1）：
//    - System Settings「添加输入法」列表 → 读 **bundle 级**图标键
//      （tsInputMethodIconFileKey / tsInputMethodAlternateIconFileKey）；
//    - 快捷键切换 HUD 与菜单栏（系统栏）→ 读 **mode 级**图标键
//      （tsInputModeMenuIconFileKey / Alternate、palette 键）；
//    - macOS 没有第三套图标键把「菜单栏」与「HUD」再分开，所以第三方输入法
//      **无法**做到「系统设置+系统栏 keycap、HUD 无边框字形」；
//    - 用户确认该限制后要求统一为扁平的带边框 keycap 版本（2026-10-25 指示）。
//
//    不要改回 16×16 正方形、描边空心块或纯色方块；动尺寸/形态/配色前先问用户，
//    改完必须跑 tests/equivalence.sh（D 段 keycap 契约 + 五个键指向同一文件）。
//
// 规格依据：
//   - 尺寸：系统输入源的 `IPInputSource.iconImage` 实测为 22×16（44×32 @2x）；第三方
//     输入源按 TIS 图标文件**原尺寸**显示（16×16 会显示成方块，22×16 才是系统同款比例）。
//   - 双帧模板 TIFF：22×16@72dpi (1x) + 44×32@144dpi (2x)、LZW、ExtraSamples=2（非预乘）；
//   - RGB 恒为 (0,0,0)，形状完全由 alpha 表达；template 渲染后即随浅/深色自动反色；
//   - 字形只用单字符「C」：系统内置输入源全部单字符（ABC「A」、简体拼音「拼」…）。
//   - keycap：满幅实心圆角按键（半径 0.20×高），字形从 alpha 挖空（destinationOut）；
//   - 字形：墨迹高度 ≈ 0.74×画布高，居中；无任何背景/边框。
//
// 实现要点：先在 8× 超采样画布上用 CGBitmapContext 画字形，再用 alphaOnly 灰度上下文
// 读出 alpha、降采样写入 NSBitmapImageRep。CGBitmapContext 原点在左下，读回第 0 行是
// 图像底部，写 NSBitmapImageRep（顶部为第 0 行）时需 Y 反转。

/// 在给定上下文中绘制水平/垂直居中的单字符字形；用**墨迹高度**（glyph path bounds）
/// 二分字号而不是行高（NSAttributedString.size() 含上下留白，会让实际墨迹偏小）。
private func drawCenteredGlyph(_ text: String, targetH: CGFloat, maxWidth: CGFloat, in ctx: CGContext) {
    func metrics(_ size: CGFloat) -> (line: CTLine, bounds: CGRect) {
        let f = CTFontCreateWithName("Helvetica-Bold" as CFString, size, nil)
        let a = NSAttributedString(string: text, attributes: [
            .font: f, .foregroundColor: NSColor.white,
        ])
        let l = CTLineCreateWithAttributedString(a)
        return (l, CTLineGetBoundsWithOptions(l, .useGlyphPathBounds))
    }
    var lo = targetH * 0.3
    var hi = targetH * 6.0
    var bestLine = metrics(targetH).line
    var bestBounds = metrics(targetH).bounds
    for _ in 0..<40 {
        let mid = (lo + hi) / 2
        let (l, b) = metrics(mid)
        if b.height < targetH && b.width < maxWidth { lo = mid } else { hi = mid }
        bestLine = l
        bestBounds = b
    }
    let bounds = bestBounds
    ctx.textPosition = CGPoint(
        x: (CGFloat(ctx.width) - bounds.width) / 2 - bounds.minX,
        y: (CGFloat(ctx.height) - bounds.height) / 2 - bounds.minY
    )
    CTLineDraw(bestLine, ctx)
}

/// 生成输入源模板图单帧（22×16 画布）：满幅实心圆角按键 + alpha 挖空单字符「C」。
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

    // 1) 满幅实心圆角按键：圆角半径 0.20×高度（系统 pinyin.tiff 实测约 3/16）
    let radius = CGFloat(bigH) * 0.20
    let body = CGPath(roundedRect: CGRect(x: 0, y: 0, width: CGFloat(bigW), height: CGFloat(bigH)),
                      cornerWidth: radius, cornerHeight: radius, transform: nil)
    ctx.setFillColor(red: 0, green: 0, blue: 0, alpha: 1)
    ctx.addPath(body)
    ctx.fillPath()

    // 2) 把单字符「C」从 alpha 里挖空（destinationOut）：字形处 alpha=0，
    //    模板渲染后即「实心按键 + 反白字形」，与系统 ABC / 简体拼音一致。
    ctx.setBlendMode(.destinationOut)
    drawCenteredGlyph("C", targetH: CGFloat(bigH) * 0.72, maxWidth: CGFloat(bigW) * 0.62, in: ctx)
    ctx.setBlendMode(.normal)

    // 取 CGImage，用 alphaOnly 灰度上下文读出干净的 alpha 通道
    guard let bigImage = ctx.makeImage() else { fatalError("超采样画布取图失败") }
    var alphaBuf = [UInt8](repeating: 0, count: bigW * bigH)
    alphaBuf.withUnsafeMutableBytes { raw in
        let readCtx = CGContext(data: raw.baseAddress, width: bigW, height: bigH,
                                bitsPerComponent: 8, bytesPerRow: bigW,
                                space: CGColorSpaceCreateDeviceGray(),
                                bitmapInfo: CGImageAlphaInfo.alphaOnly.rawValue)!
        readCtx.draw(bigImage, in: CGRect(x: 0, y: 0, width: bigW, height: bigH))
    }

    // 降采样并写入 NSBitmapImageRep
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
    // 22×16：与系统输入源 IPInputSource.iconImage 的实际渲染尺寸一致
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

// 1. System Settings「添加输入法」列表 / 输入源列表：bundle 级图标键读这份
//    → 系统同款 22×16 宽扁 keycap（实心圆角按键 + 挖空「C」）
//    五个 TIS 图标键（bundle 级 + mode 级）统一指向这一份文件：
//    macOS 没有第三套键把「菜单栏」与「HUD」分开，统一形态是用户确认后的最终方案。
writeInputSourceTiff(URL(fileURLWithPath: outDir)
    .appendingPathComponent("ChaosetIME-input.tiff").path)

print("已生成 \(outDir)/ChaosetIME.iconset 与 ChaosetIME-input.tiff（22×16 扁平 keycap，全场景统一）")

// MARK: - 跨平台同步（Windows .ico 与 Linux hicolor PNG/SVG）

func writeWindowsIco(to path: String) {
    let sizes = [16, 24, 32, 48, 64, 256]
    var data = Data()

    var reserved: UInt16 = 0
    var iconType: UInt16 = 1
    var count: UInt16 = UInt16(sizes.count)
    data.append(contentsOf: withUnsafeBytes(of: &reserved) { Array($0) })
    data.append(contentsOf: withUnsafeBytes(of: &iconType) { Array($0) })
    data.append(contentsOf: withUnsafeBytes(of: &count) { Array($0) })

    var currentOffset = 6 + sizes.count * 16
    var pngMap: [Int: Data] = [:]

    for sz in sizes {
        let png = appIconRep(sz).representation(using: .png, properties: [:])!
        pngMap[sz] = png

        var bWidth: UInt8 = sz >= 256 ? 0 : UInt8(sz)
        var bHeight: UInt8 = sz >= 256 ? 0 : UInt8(sz)
        var bColorCount: UInt8 = 0
        var bReserved: UInt8 = 0
        var wPlanes: UInt16 = 1
        var wBitCount: UInt16 = 32
        var dwBytesInRes: UInt32 = UInt32(png.count)
        var dwImageOffset: UInt32 = UInt32(currentOffset)

        data.append(contentsOf: withUnsafeBytes(of: &bWidth) { Array($0) })
        data.append(contentsOf: withUnsafeBytes(of: &bHeight) { Array($0) })
        data.append(contentsOf: withUnsafeBytes(of: &bColorCount) { Array($0) })
        data.append(contentsOf: withUnsafeBytes(of: &bReserved) { Array($0) })
        data.append(contentsOf: withUnsafeBytes(of: &wPlanes) { Array($0) })
        data.append(contentsOf: withUnsafeBytes(of: &wBitCount) { Array($0) })
        data.append(contentsOf: withUnsafeBytes(of: &dwBytesInRes) { Array($0) })
        data.append(contentsOf: withUnsafeBytes(of: &dwImageOffset) { Array($0) })

        currentOffset += png.count
    }

    for sz in sizes {
        data.append(pngMap[sz]!)
    }

    try? FileManager.default.createDirectory(at: URL(fileURLWithPath: path).deletingLastPathComponent(), withIntermediateDirectories: true)
    try! data.write(to: URL(fileURLWithPath: path))
    print("已同步 Windows 图标: \(path)")
}

func writeLinuxIcons(baseDir: String) {
    let sizes = [48, 128, 256]
    for sz in sizes {
        let dir = "\(baseDir)/\(sz)x\(sz)/apps"
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let png = appIconRep(sz).representation(using: .png, properties: [:])!
        let path = "\(dir)/chaoset.png"
        try! png.write(to: URL(fileURLWithPath: path))
        print("已同步 Linux 图标: \(path)")
    }
}

func writeLinuxSvg(to path: String) {
    let svgContent = """
<?xml version="1.0" encoding="UTF-8"?>
<svg width="512" height="512" viewBox="0 0 512 512" fill="none" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <linearGradient id="terracottaGrad" x1="41" y1="41" x2="471" y2="471" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#F08F42"/>
      <stop offset="48%" stop-color="#D16B29"/>
      <stop offset="100%" stop-color="#994014"/>
    </linearGradient>
    <filter id="shadow" x="0" y="8" width="512" height="512" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">
      <feDropShadow dx="0" dy="16" stdDeviation="16" flood-color="#401808" flood-opacity="0.35"/>
    </filter>
    <filter id="starGlow" x="280" y="100" width="160" height="160" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">
      <feDropShadow dx="0" dy="0" stdDeviation="8" flood-color="#FFA838" flood-opacity="0.8"/>
    </filter>
    <filter id="glyphShadow" x="80" y="80" width="360" height="360" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">
      <feDropShadow dx="0" dy="6" stdDeviation="10" flood-color="#4A1806" flood-opacity="0.65"/>
    </filter>
    <clipPath id="squircleClip">
      <rect x="41" y="41" width="430" height="430" rx="96" ry="96"/>
    </clipPath>
  </defs>

  <!-- 1. Squircle Base with Soft Drop Shadow -->
  <rect x="41" y="41" width="430" height="430" rx="96" ry="96" fill="url(#terracottaGrad)" filter="url(#shadow)"/>

  <!-- 2. Clipped Texture & Highlights -->
  <g clip-path="url(#squircleClip)">
    <!-- Washi Concentric Micro-Rings -->
    <circle cx="256" cy="256" r="120" stroke="#FFF0E0" stroke-opacity="0.15" stroke-width="2.5" fill="none"/>
    <circle cx="256" cy="256" r="189" stroke="#FFF0E0" stroke-opacity="0.15" stroke-width="2.5" fill="none"/>
    <circle cx="256" cy="256" r="258" stroke="#FFF0E0" stroke-opacity="0.15" stroke-width="2.5" fill="none"/>
    
    <!-- Top-edge Specular Rim Line -->
    <rect x="42" y="42" width="428" height="428" rx="95" ry="95" stroke="#FFF8F0" stroke-opacity="0.50" stroke-width="2" fill="none"/>
  </g>

  <!-- 3. Monogram "C" White Porcelain Typography -->
  <g filter="url(#glyphShadow)">
    <text x="240" y="335" text-anchor="middle" font-family="'Avenir Next', 'Helvetica Neue', system-ui, sans-serif" font-size="240" font-weight="900" fill="#FAF6F0">C</text>
  </g>

  <!-- 4. Morning Sun Golden Star (✦) -->
  <g filter="url(#starGlow)">
    <text x="360" y="210" text-anchor="middle" font-family="system-ui, sans-serif" font-size="96" font-weight="bold" fill="#FFE08A">✦</text>
  </g>

  <!-- 5. Bottom Subtext "CHAOS" -->
  <text x="256" y="428" text-anchor="middle" font-family="'SF Pro Text', 'Avenir Next', system-ui, sans-serif" font-size="18" font-weight="bold" letter-spacing="4.5" fill="#FFF0E0" fill-opacity="0.80">CHAOS</text>
</svg>
"""
    try? FileManager.default.createDirectory(at: URL(fileURLWithPath: path).deletingLastPathComponent(), withIntermediateDirectories: true)
    try! svgContent.write(to: URL(fileURLWithPath: path), atomically: true, encoding: .utf8)
    print("已同步 Linux 矢量 SVG 图标: \(path)")
}

if CommandLine.arguments.contains("--sync-all") {
    // 自动寻找仓库根目录并同步 Windows 与 Linux 图标
    let scriptPath = URL(fileURLWithPath: CommandLine.arguments[0]).standardized.path
    var rootDir = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
    if scriptPath.contains("/shims/macos/tools/") {
        let prefix = scriptPath.components(separatedBy: "/shims/macos/tools/")[0]
        rootDir = URL(fileURLWithPath: prefix)
    }
    let winIcoPath = rootDir.appendingPathComponent("shims/windows/resources/chaoset.ico").path
    let linuxHicolor = rootDir.appendingPathComponent("shims/linux/icons/hicolor").path
    let linuxSvgPath = rootDir.appendingPathComponent("shims/linux/icons/hicolor/scalable/apps/chaoset.svg").path

    writeWindowsIco(to: winIcoPath)
    writeLinuxIcons(baseDir: linuxHicolor)
    writeLinuxSvg(to: linuxSvgPath)
}
