// CandidatePanelWindow.swift：候选窗 NSPanel 的构造与窗口级配置。
//
// 从 CandidatePanel.swift 的 ensurePanel 拆出（2026-11-06 架构拆分，纯移动）：
// 只负责创建/配置 NSPanel 与内容视图；单例持有、show/hide、回调装配在
// CandidatePanel.swift 的 CandidatePanelController。

import AppKit

// MARK: - 候选窗面板构造（NSPanel 生命周期）

enum CandidatePanelWindow {
    /// 创建并配置候选窗面板与视图；调用方负责单例持有与回调装配。
    static func make() -> (panel: NSPanel, view: CandidatePanelView) {
        let p = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 10, height: 10),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        // 层级用 .floating(3) 而不是 .popUpMenu(101)：popUpMenu 高于系统
        // 菜单栏（mainMenu=24）与一切下拉菜单，候选窗在光标行贴近屏幕顶部
        // 向上翻到菜单区域时，会整块压住菜单拦截点击——表现为「有时候无法
        // 点击其他 app 的菜单」（2026-10-28 用户反馈）。floating 仍浮于
        // 全部普通窗口内容之上（配合 canJoinAllSpaces/fullScreenAuxiliary
        // 保持全屏与多 Space 可见），但不再遮蔽菜单栏。
        p.level = .floating
        p.isOpaque = false
        p.backgroundColor = .clear
        // 面板自身接收鼠标以支持点击选词；不抢焦点（nonactivatingPanel），
        // 面板外点击仍落到宿主应用并触发会话提交。
        p.ignoresMouseEvents = false
        p.hasShadow = true
        // 候选窗必须能跟随用户出现在任何 Space / 全屏应用之上。
        //
        // NSWindow.h 明确：windowLevel != NSNormalWindowLevel 时默认
        // collectionBehavior 是 Transient（"Floats in spaces, hidden by
        // exposé"），不参与 Space 切换。而输入法常在浏览器/编辑器全屏
        // （独立 Space）里使用——不加下面这三项，orderFront 后候选窗
        // 根本不会显示（2026-10-26 审查发现，此前只测了普通窗口场景）。
        //   .canJoinAllSpaces  —— 出现在所有 Space
        //   .fullScreenAuxiliary —— 允许在全屏应用的独立 Space 上显示
        //   .stationary        —— Exposé/Mission Control 时不随桌面移动
        p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary, .stationary]
        // borderless 窗口默认不接受首次点击（非 key 窗口的首击只用来激活），
        // 会使「点击候选词上屏」第一次点击无效、需要点两次。
        // becomesKeyOnlyIfNeeded 让面板在需要时才取 key，配合视图的
        // acceptsFirstMouse 即可单击生效且不抢焦点（2026-10-26 审查发现）。
        p.becomesKeyOnlyIfNeeded = true
        let v = CandidatePanelView(frame: NSRect(x: 0, y: 0, width: 10, height: 10))
        v.autoresizingMask = [.width, .height]
        p.contentView = v
        return (p, v)
    }
}
