// CandidateTheme.swift：候选窗主题色 / 排版几何常量 / 设置齿轮参数。
//
// 从 CandidatePanel.swift 拆出（2026-11-06 架构拆分，纯移动、行为零改动）。
// 色值与几何是 macOS 视觉参考基准，改动必须同步 tests/visual-parity.sh 与
// docs/ui-parity.md（另见 CandidatePanel.swift 头部的原生自绘架构决策）。

import AppKit

// MARK: - 主题色（与 skin.DefaultSkinFor 色值一一对应）

// internal（非 private）：tools/panel_metrics.swift 需要导出 13 个色值，
// 与 Go 侧 skin/skin.go 做浅/深两套逐色比对（visual-parity.sh D 段）。
struct CandidateTheme {
    let panelBg: NSColor
    let panelBorder: NSColor
    let text: NSColor
    let highlightBg: NSColor
    let highlightText: NSColor
    let chipBg: NSColor
    let chipBorder: NSColor
    let chipDigit: NSColor
    let highlightChipBg: NSColor
    let pageColor: NSColor
    let userMark: NSColor
    let divider: NSColor
    let chevron: NSColor

    static func theme(dark: Bool) -> CandidateTheme {
        if dark {
            return CandidateTheme(
                panelBg: NSColor(srgbRed: 0x2C / 255.0, green: 0x2C / 255.0, blue: 0x2E / 255.0, alpha: 0xF5 / 255.0),
                panelBorder: NSColor(srgbRed: 1, green: 1, blue: 1, alpha: 0x24 / 255.0),
                text: .white,
                highlightBg: NSColor(srgbRed: 0x00 / 255.0, green: 0x66 / 255.0, blue: 0xEB / 255.0, alpha: 1),
                highlightText: .white,
                chipBg: NSColor(srgbRed: 0x5A / 255.0, green: 0x5A / 255.0, blue: 0x5E / 255.0, alpha: 1),
                chipBorder: NSColor(srgbRed: 1, green: 1, blue: 1, alpha: 0x40 / 255.0),
                chipDigit: .white,
                highlightChipBg: NSColor(white: 1, alpha: 0x45 / 255.0),
                pageColor: NSColor(srgbRed: 0x98 / 255.0, green: 0x98 / 255.0, blue: 0x9F / 255.0, alpha: 0xB8 / 255.0),
                userMark: NSColor(srgbRed: 0x30 / 255.0, green: 0xD1 / 255.0, blue: 0x58 / 255.0, alpha: 1),
                divider: NSColor(white: 1, alpha: 0x1E / 255.0),
                chevron: NSColor(srgbRed: 0x98 / 255.0, green: 0x98 / 255.0, blue: 0x9F / 255.0, alpha: 0xB8 / 255.0)
            )
        }
        return CandidateTheme(
            panelBg: NSColor(srgbRed: 0xF6 / 255.0, green: 0xF6 / 255.0, blue: 0xF8 / 255.0, alpha: 0xF6 / 255.0),
            panelBorder: NSColor(srgbRed: 0, green: 0, blue: 0, alpha: 0x1A / 255.0),
            text: NSColor(srgbRed: 0x1D / 255.0, green: 0x1D / 255.0, blue: 0x1F / 255.0, alpha: 1),
            highlightBg: NSColor(srgbRed: 0x00 / 255.0, green: 0x7A / 255.0, blue: 0xFF / 255.0, alpha: 1),
            highlightText: .white,
            chipBg: .white,
            chipBorder: NSColor(srgbRed: 0, green: 0, blue: 0, alpha: 0x3C / 255.0),
            chipDigit: NSColor(srgbRed: 0x1D / 255.0, green: 0x1D / 255.0, blue: 0x1F / 255.0, alpha: 1),
            highlightChipBg: NSColor(white: 1, alpha: 0x45 / 255.0),
            pageColor: NSColor(srgbRed: 0x8E / 255.0, green: 0x8E / 255.0, blue: 0x93 / 255.0, alpha: 0xD0 / 255.0),
            userMark: NSColor(srgbRed: 0x34 / 255.0, green: 0xC7 / 255.0, blue: 0x59 / 255.0, alpha: 1),
            divider: NSColor(srgbRed: 0, green: 0, blue: 0, alpha: 0x14 / 255.0),
            chevron: NSColor(srgbRed: 0x8E / 255.0, green: 0x8E / 255.0, blue: 0x93 / 255.0, alpha: 0xD0 / 255.0)
        )
    }
}

// MARK: - 布局几何常量
//
// Layout 仍是 CandidatePanelView 的嵌套类型（引用处无需改写，见下方 extension），
// 与主题色同文件；tools/panel_metrics.swift 会读取这些常量与 Go 侧做机器比对。
extension CandidatePanelView {
    // 排版参数（pt），与 skin/skin.go 的 Skin 字段保持一致
    // internal（非 private）：tools/panel_metrics.swift 需要读取这些常量，
    // 与 Go 侧 tools/render_metrics.go 导出的 skin 参数做机器比对，防止双端视觉漂移。
    enum Layout {
        static let fontSize: CGFloat = 15
        static let indexFontSize: CGFloat = 12      // 主字号 80%
        static let padding: CGFloat = 7
        static let firstPadLeft: CGFloat = 9
        static let itemPadX: CGFloat = 7
        static let gap: CGFloat = 11
        static let lineSpacing: CGFloat = 4
        static let maxWidth: CGFloat = 720
        static let maxRows = 3
        // 行高用 CJK 安全值：NSFont 的 ascender/descender 是西文 metrics，不含
        // 中文字形的上下延展，按它算的面板高度会把第二行文字下半裁掉
        // （2026-10-25 实机：两行时「金田村」被裁半行）。
        static let textH: CGFloat = ceil(fontSize * 1.4)
        static let chevronBoxW: CGFloat = 36
        static let chipPadX: CGFloat = 4
        static let chipPadY: CGFloat = 2
        static let chipRadius: CGFloat = 3
        static let panelRadius: CGFloat = 8
        static let pillMargin: CGFloat = 2
    }
}

// 齿轮区宽度（pt，折叠区内、chevron 左侧）：
// 齿轮矢量直径 2*7.2pt=14.4pt + 两侧各约 5.8pt 余量 = 26pt。
// 与 Windows win32.cpp 的 gearBoxW（pt(26)）对应。
// 历史：2026-10-28 995dd99 已删除「圆形淡底直径 22」方案，旧注释停留在
// 删除前（2026-10-30 审计 A16 修正）。
enum SettingsGear {
    static let boxW: CGFloat = 26
}
