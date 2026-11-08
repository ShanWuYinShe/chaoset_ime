// SettingsWindow.swift：输入法设置窗口（独立窗口，参考市面输入法：
// 候选栏齿轮 → 打开设置窗口，窗口内为可扩展的设置项列表）。
//
// 输入法进程的激活策略是 .prohibited（IMK 要求，不占 Dock），弹普通
// NSWindow 前需临时切到 .accessory 才能成为 key 窗口正常交互。
// 窗口单例：已打开则前置，关闭仅隐藏（isReleasedWhenClosed=false，
// 否则关闭后静态指针悬空崩溃）。
//
// 2026-10-31：新增「学习数据」区——统计 / 清空 / 导出全部在 GUI 完成，
// 普通用户不需要命令行（`chaoset-userdict` 降级为高级/排障兜底）。
// 数据经 EngineClient 走同一 IPC 通道，daemon 内存与磁盘一次生效。
//
// 2026-10-31：新增「词库」区——「导入词库…」打开随包安装的
// `~/Applications/Chaoset.app`（安装与设置 app）并传 `--import`，
// 由它弹出文件选择并完成合并/备份/原子替换/重启 daemon（入口与 Windows
// 设置窗「词库」区对齐；导入实现仍只有 Setup.app 一份，避免复制粘贴）。

import AppKit

// 2026-11-06 架构拆分：本文件只保留窗口/控制器骨架与左导航/页脚/约束；
// 三页视图构建在 SettingsWindowPages.swift，@objc target/action 在
// SettingsWindowTargets.swift。

// MARK: - 设置窗控制器

enum SettingsWindowController {
    private static var window: NSWindow?
    private static let switchTarget = AssociateSwitchTarget()
    private static let learningTarget = LearningDataTarget()
    private static let dictImportTarget = DictionaryImportTarget()
    /// 设置窗口独立使用一个 EngineClient（与输入法主流程的实例互不影响；
    /// 同一 daemon 支持多连接，每个连接有独立组词会话）。
    static let engine = EngineClient()

    // 中英状态三选一的 target（静态持有，避免窗口重建时悬空）。
    private static let modeTarget = ModeScopeTarget()

    static func show() {
        NSApp.setActivationPolicy(.accessory)
        NSApp.activate(ignoringOtherApps: true)

        if let w = window {
            learningTarget.refresh()
            w.makeKeyAndOrderFront(nil)
            w.center()
            return
        }

        // 布局：左侧导航（输入 / 词库 / 关于）+ 右侧内容页（2026-11-06
        // 卡片式重构，与 Windows 设置窗对齐；参照微信输入法设置窗的形态）。

        // —— 输入页 ——（三页视图构建见 SettingsWindowPages.swift）
        let inputPage = SettingsWindowPages.makeInputPage(
            switchTarget: switchTarget, modeTarget: modeTarget)

        // —— 词库页 ——
        let dictPage = SettingsWindowPages.makeDictPage(
            learningTarget: learningTarget, dictImportTarget: dictImportTarget)

        // —— 关于页 ——
        let aboutPage = SettingsWindowPages.makeAboutPage()

        // —— 左侧导航 ——
        // 宽度 132pt 与 Windows kNavWidth 一致；按钮固定 112×32pt，才能让
        // 选中项的 accent 胶囊（layer 圆角底）形成稳定矩形（见
        // PageSwitchTarget.styleNavButton）。
        let sidebar = NSStackView()
        sidebar.orientation = .vertical
        sidebar.alignment = .leading
        sidebar.spacing = 6
        sidebar.edgeInsets = NSEdgeInsets(top: 0, left: 10, bottom: 0, right: 10)
        sidebar.widthAnchor.constraint(equalToConstant: 132).isActive = true
        let appTitle = NSTextField(labelWithString: "Chaoset 设置")
        appTitle.font = .boldSystemFont(ofSize: 14)
        sidebar.addArrangedSubview(appTitle)
        PageSwitchTarget.shared.inputPage = inputPage
        PageSwitchTarget.shared.dictPage = dictPage
        PageSwitchTarget.shared.aboutPage = aboutPage
        PageSwitchTarget.shared.navButtons.removeAll()
        for (idx, title) in [("0", "输入"), ("1", "词库"), ("2", "关于")] {
            let b = NSButton(title: title, target: PageSwitchTarget.shared,
                             action: #selector(PageSwitchTarget.switchPage(_:)))
            b.isBordered = false
            b.identifier = NSUserInterfaceItemIdentifier(idx)
            b.font = .systemFont(ofSize: 13)
            b.widthAnchor.constraint(equalToConstant: 112).isActive = true
            b.heightAnchor.constraint(equalToConstant: 32).isActive = true
            sidebar.addArrangedSubview(b)
            PageSwitchTarget.shared.navButtons.append(b)
        }

        // 整窗：横向 [导航 | 内容页]，初始只显示输入页；底部固定页脚版本号。
        let content = NSStackView()
        content.orientation = .horizontal
        content.alignment = .top
        content.spacing = 24
        content.addArrangedSubview(sidebar)
        content.addArrangedSubview(inputPage)
        content.addArrangedSubview(dictPage)
        content.addArrangedSubview(aboutPage)
        dictPage.isHidden = true
        aboutPage.isHidden = true
        PageSwitchTarget.shared.select(0)

        // 页脚版本号：2026-11-06 阶段2 与 Windows 对齐——三端设置窗底部固定显示
        // 「Chaoset 版本 x.y.z」，不再只放在「关于」页（AGENTS.md:19 版本可见性）。
        let version = (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "dev"
        let footerLabel = NSTextField(labelWithString: "Chaoset 版本 \(version)")
        footerLabel.font = .systemFont(ofSize: 11)
        footerLabel.textColor = .tertiaryLabelColor

        // 内容与页脚分开：内容贴顶（20pt 边距），页脚贴底（12pt），中间不允许
        // 重叠（页脚 top >= 内容 bottom + 8）。窗口固定 640×560pt（不可缩放）。
        let root = NSView()
        root.addSubview(content)
        root.addSubview(footerLabel)
        content.translatesAutoresizingMaskIntoConstraints = false
        footerLabel.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            content.topAnchor.constraint(equalTo: root.topAnchor, constant: 20),
            content.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 20),
            content.trailingAnchor.constraint(lessThanOrEqualTo: root.trailingAnchor, constant: -28),
            footerLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            footerLabel.topAnchor.constraint(greaterThanOrEqualTo: content.bottomAnchor, constant: 8),
            footerLabel.bottomAnchor.constraint(equalTo: root.bottomAnchor, constant: -12),
        ])

        let w = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 640, height: 560),
            styleMask: [.titled, .closable],
            backing: .buffered,
            defer: false)
        w.title = "Chaoset 设置"
        w.contentView = root
        w.isReleasedWhenClosed = false
        w.center()
        w.makeKeyAndOrderFront(nil)
        window = w

        // 首次打开时拉取学习数据统计（daemon 不可达时标签显示提示）
        learningTarget.refresh()

        // 关闭设置窗口时退回输入法进程的常规激活策略（不占 Dock）
        NotificationCenter.default.addObserver(
            forName: NSWindow.willCloseNotification, object: w, queue: nil) { _ in
            NSApp.setActivationPolicy(.prohibited)
        }

        // 设置关闭联想时收起联想候选窗（Controller 侧的联想状态随之清空）
        NotificationCenter.default.addObserver(
            forName: .chaosetAssociateDisabled, object: nil, queue: nil) { _ in
            CandidatePanelController.hide()
        }
    }
}
