// CandidatePanel.swift：macOS 原生候选窗（AppKit 自绘）。
//
// 架构决策（2026-10-25，用户拍板）：macOS 壳从「daemon 渲染 PNG 贴图」
// 切换到壳内原生绘制。理由：
//   1. 消除跨进程像素管线（daemon 栅格化+PNG+base64+JSON+socket+壳解码
//      ≈5ms/帧 → NSView 直接绘制，Retina 由系统自动处理，无需 scale 逻辑）；
//   2. 系统原生能力回归：NSColor 动态色自动跟随深浅色、系统字体渲染、
//      辅助功能（VoiceOver 可读候选）、鼠标点选候选（贴图方案的
//      ignoresMouseEvents=true 曾是功能缺口）；
//   3. daemon 渲染管线的一处皮肤状态机缺陷（WithDarkMode 漏拷配色）曾
//      导致深色键帽全坏且单测未拦——原生方案不存在跨进程皮肤状态机。
// 代价：macOS 与 Windows/Linux 的候选窗视觉从此分叉（各自原生自绘），
// 视觉改动需要双端同步。三端壳都不再使用 daemon 贴图后，daemon 侧的
// PNG 渲染链路（renderer 包 + candidateImage IPC）已于 2026-10-27 删除。
//
// 视觉复刻 skin.DefaultSkinFor 的设计（键帽序号 + 首选蓝胶囊 + 学习标记），
// 色值与 skin/skin.go 保持一致；浅/深是两套**静态 sRGB NSColor**，每次 draw
// 按 effectiveAppearance 选择（不是 NSColor 动态色——机制描述以代码为准，
// 2026-10-30 审计 A16 修正过期注释）。

import AppKit
import os

// 2026-11-06 架构拆分：本文件只保留对外 API 薄壳（单例/show/hide/回调装配）与
// 模式提示；视图在 CandidatePanelView.swift，NSPanel 构造在 CandidatePanelWindow.swift，
// 主题/布局常量在 CandidateTheme.swift，设置模型在 CandidateSettings.swift。

// MARK: - 面板单例管理

/// 全进程共用一个候选窗面板（同旧 imagePanel 的理由：IMK 每个输入上下文
/// 建一个 Controller 实例，实例各自持面板会残留；任何时刻最多一个可见）。
enum CandidatePanelController {
    private static let logger = Logger(subsystem: "ime.chaoset.inputmethod", category: "candidate")

    private static var panel: NSPanel?
    private static var view: CandidatePanelView?
    /// 点击候选的回调，由 Controller 在显示时设置（weak self 由调用方保证）。
    static var onSelect: ((Int) -> Void)?
    /// 点击折叠区翻下一页的回调，与 onSelect 同约定。
    static var onPageDown: (() -> Void)?
    /// 末页时折叠区箭头变 ⌃，点击翻上一页。
    static var onPageUp: (() -> Void)?
    /// 候选栏齿轮点击（mouseUp 且仍在齿轮区才打开设置窗口）。
    static var onSettings: (() -> Void)?
    /// 右键候选「删除该词」回调，与 onSelect 同约定。
    static var onDelete: ((Int) -> Void)?
    /// 页内预选序号（0 起，当前页切片内）。方向键移动、空格确认与高亮 pill
    /// 共用；show()（新输入/翻页/选词刷新）时归零。
    static var selectedIndex = 0

    /// 已布局（可见）候选数：方向键只在可见项内移动。
    static var itemCount: Int { view?.itemCount ?? 0 }

    /// 页内移动预选：成功返回 true；已在页首/页尾返回 false（调用方据此翻页）。
    static func moveSelection(_ delta: Int) -> Bool {
        let count = itemCount
        guard count > 0 else { return false }
        let next = selectedIndex + delta
        guard next >= 0, next < count else { return false }
        setSelectedIndex(next)
        return true
    }

    /// 指定页内预选（内部钳制到可见候选范围），立即重绘高亮 pill。
    static func setSelectedIndex(_ index: Int) {
        let count = itemCount
        let clamped = count <= 0 ? 0 : min(max(index, 0), count - 1)
        selectedIndex = clamped
        guard let v = view else { return }
        v.selectedIndex = clamped
        v.needsDisplay = true
    }

    /// 显示候选窗。originProvider 在内容尺寸确定后被调用，返回面板左上角
    /// 屏幕坐标（Controller 用它把候选窗钉在光标下方）。
    static func show(candidates: [CandidatePanelView.Item], page: Int, pageCount: Int,
                     originProvider: (NSSize) -> NSPoint) -> Bool {
        guard !candidates.isEmpty else {
            hide()
            return false
        }
        let p = ensurePanel()
        guard let v = view else {
            // 候选窗创建失败时没有系统错误弹窗；写统一日志，配合
            // log show --predicate 'subsystem == "ime.chaoset.inputmethod"' 排障。
            logger.error("候选窗 view 创建失败，本次不显示候选")
            return false
        }
        v.onSelect = { onSelect?($0) }
        v.onPageDown = { onPageDown?() }
        v.onPageUp = { onPageUp?() }
        v.onSettings = { onSettings?() }
        v.onDelete = { onDelete?($0) }
        // 新输入/翻页/选词后的刷新：页内预选归零（方向键跨页移动时由
        // KeyRouter 在 show 之后用 setSelectedIndex 指定末项/首项）。
        selectedIndex = 0
        v.selectedIndex = 0
        let size = v.layout(candidates: candidates, page: page, pageCount: pageCount)
        p.setContentSize(size)
        p.setFrameTopLeftPoint(originProvider(size))
        p.orderFront(nil)
        return true
    }

    static func hide() {
        panel?.orderOut(nil)
        selectedIndex = 0
        onSelect = nil
        onPageDown = nil
        onPageUp = nil
        onSettings = nil
        onDelete = nil
    }

    private static func ensurePanel() -> NSPanel {
        if let panel, let view = panel.contentView as? CandidatePanelView {
            _ = view
            return panel
        }
        // NSPanel 的构造与窗口级配置细节见 CandidatePanelWindow.swift。
        let built = CandidatePanelWindow.make()
        panel = built.panel
        view = built.view
        return built.panel
    }
}

// MARK: - 模式切换提示（短暂 toast）

// 中英/全半角切换此前零反馈，用户只能靠打字试探才知道当前模式
// （2026-10-27 审查，三端统一补齐）。与候选窗同等层级的小胶囊，
// 短暂显示后自动消失；连续切换时旧定时器作废，不会提前收起新提示。

/// 模式提示的展示时长（秒）。
private let modeToastDuration: TimeInterval = 1.2

final class ModeToastView: NSView {
    var text: String = "" { didSet { needsDisplay = true } }

    override func draw(_ dirtyRect: NSRect) {
        let isDark = effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        let bg = isDark ? NSColor(white: 0.18, alpha: 0.92) : NSColor(white: 0.97, alpha: 0.95)
        let fg = isDark ? NSColor.white : NSColor(calibratedWhite: 0.15, alpha: 1)
        let path = NSBezierPath(roundedRect: bounds, xRadius: 10, yRadius: 10)
        bg.setFill()
        path.fill()
        NSColor(white: 0, alpha: isDark ? 0.4 : 0.15).setStroke()
        path.lineWidth = 1
        path.stroke()
        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 13, weight: .medium),
            .foregroundColor: fg,
        ]
        let size = (text as NSString).size(withAttributes: attrs)
        (text as NSString).draw(
            at: NSPoint(x: (bounds.width - size.width) / 2, y: (bounds.height - size.height) / 2),
            withAttributes: attrs)
    }

    /// 按文本内容计算胶囊尺寸。
    static func size(for text: String) -> NSSize {
        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 13, weight: .medium)
        ]
        let s = (text as NSString).size(withAttributes: attrs)
        return NSSize(width: ceil(s.width) + 28, height: ceil(s.height) + 14)
    }
}

enum ModeToastController {
    private static var panel: NSPanel?
    private static var hideWork: DispatchWorkItem?

    /// 在给定左上角坐标显示提示，约 1.2 秒后自动消失。
    static func show(text: String, topLeft: NSPoint) {
        let p = ensurePanel()
        guard let v = p.contentView as? ModeToastView else { return }
        v.text = text
        let size = ModeToastView.size(for: text)
        p.setContentSize(size)
        p.setFrameTopLeftPoint(topLeft)
        p.orderFront(nil)
        hideWork?.cancel()
        let work = DispatchWorkItem { [weak p] in p?.orderOut(nil) }
        hideWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + modeToastDuration, execute: work)
    }

    /// 立即收起（会话结束/切换输入法时调用，不等定时器）。
    static func hide() {
        hideWork?.cancel()
        panel?.orderOut(nil)
    }

    private static func ensurePanel() -> NSPanel {
        if let panel, panel.contentView is ModeToastView { return panel }
        let p = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 10, height: 10),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        // 层级同候选窗用 .floating（不遮蔽菜单栏，见候选窗处注释）
        p.level = .floating
        p.isOpaque = false
        p.backgroundColor = .clear
        // 纯提示：不接收鼠标（点击穿透到宿主），不抢焦点，跟随 Space/全屏
        p.ignoresMouseEvents = true
        p.hasShadow = true
        p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary]
        let v = ModeToastView(frame: NSRect(x: 0, y: 0, width: 10, height: 10))
        v.autoresizingMask = [.width, .height]
        p.contentView = v
        panel = p
        return p
    }
}
