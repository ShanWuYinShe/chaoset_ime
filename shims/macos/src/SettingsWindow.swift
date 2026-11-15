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
//
// 2026-11-16：视觉系统「暖陶纸感」——米白纸底 + 白瓷卡片 + 赤陶强调色，
// 自绘开关/胶囊/按钮与左导航统一同一套令牌；深色为暖炭灰。令牌是三端共享
// 设计系统的 macOS 实现（docs/ui-parity.md「设置窗视觉令牌」逐值机器断言，
// 改色值必须三端同步）。
//
// 2026-11-06 架构拆分：设置窗拆分为窗口骨架 / 主题与自绘控件 / 三页视图 /
// @objc target；2026-11-16「暖陶纸感」视觉升级把主题令牌与自绘控件抽到
// SettingsTheme.swift（三端共享设计系统的 macOS 实现，docs/ui-parity.md
// 「设置窗视觉令牌」逐值机器断言）。

import AppKit

// 本文件：设置窗窗口骨架与控制器（show / makeContentView：左侧导航、
// 三页容器、页脚版本号）。主题令牌与自绘控件见 SettingsTheme.swift，
// 三页视图构建见 SettingsWindowPages.swift，@objc target 见
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

    // 中英状态二选一的 target（静态持有，避免窗口重建时悬空）。
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

        let w = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: SettingsTheme.windowSize.width,
                                height: SettingsTheme.windowSize.height),
            styleMask: [.titled, .closable, .fullSizeContentView],
            backing: .buffered,
            defer: false)
        w.title = "Chaoset 设置"
        // 透明标题栏 + 内容延伸到底：暖色窗口底与内容连成一片，避免
        // 「灰白标题栏 + 暖色内容」的割裂感（Windows/Linux 同语义）。
        w.titlebarAppearsTransparent = true
        w.titleVisibility = .hidden
        w.isMovableByWindowBackground = true
        w.backgroundColor = SettingsTheme.windowBg
        w.contentView = makeContentView()
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

    /// 构建设置窗内容视图（窗口骨架与内容解耦：线上窗口与离屏视觉回归/截图
    /// 共用同一份布局，避免「截图专用布局」漂移）。
    static func makeContentView() -> NSView {
        // —— 三页内容 ——（视图构建见 SettingsWindowPages.swift）
        let inputPage = SettingsWindowPages.makeInputPage(
            switchTarget: switchTarget, modeTarget: modeTarget)
        let dictPage = SettingsWindowPages.makeDictPage(
            learningTarget: learningTarget, dictImportTarget: dictImportTarget)
        let aboutPage = SettingsWindowPages.makeAboutPage()

        // —— 左侧导航：米色分栏 + 赤陶软胶囊选中态 ——
        let sidebar = NSStackView()
        sidebar.orientation = .vertical
        sidebar.alignment = .leading
        sidebar.spacing = 4
        sidebar.edgeInsets = NSEdgeInsets(top: 0, left: 0, bottom: 0, right: 0)

        // 导航标题 13pt semibold：132pt 导航列下 15pt 的 CJK 标题在 Windows/Linux
        // 的 pt 口径（96dpi 下 20px）会超宽被裁切；三端统一 13pt 保证完整显示。
        let appTitle = NSTextField(labelWithString: "Chaoset 设置")
        appTitle.font = .systemFont(ofSize: 13, weight: .semibold)
        appTitle.textColor = SettingsTheme.textPrimary
        sidebar.addArrangedSubview(appTitle)
        sidebar.setCustomSpacing(18, after: appTitle)

        PageSwitchTarget.shared.inputPage = inputPage
        PageSwitchTarget.shared.dictPage = dictPage
        PageSwitchTarget.shared.aboutPage = aboutPage
        PageSwitchTarget.shared.navButtons.removeAll()
        for (idx, title) in [("0", "输入"), ("1", "词库"), ("2", "关于")] {
            let button = WarmPillButton(title: title, kind: .nav)
            button.target = PageSwitchTarget.shared
            button.action = #selector(PageSwitchTarget.switchPage(_:))
            button.identifier = NSUserInterfaceItemIdentifier(idx)
            sidebar.addArrangedSubview(button)
            PageSwitchTarget.shared.navButtons.append(button)
        }

        // —— 整窗：左侧导航列 | 内容页；底部固定页脚版本号 ——
        let root = SettingsBackgroundView()
        let navColumn = SettingsNavColumnView()
        navColumn.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(navColumn)
        sidebar.translatesAutoresizingMaskIntoConstraints = false
        navColumn.addSubview(sidebar)

        let contentArea = NSView()
        contentArea.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(contentArea)

        let pageStack = NSStackView(views: [inputPage, dictPage, aboutPage])
        pageStack.orientation = .vertical
        pageStack.alignment = .leading
        pageStack.spacing = 0
        pageStack.translatesAutoresizingMaskIntoConstraints = false
        contentArea.addSubview(pageStack)
        for page in [inputPage, dictPage, aboutPage] {
            page.widthAnchor.constraint(equalTo: pageStack.widthAnchor).isActive = true
        }
        dictPage.isHidden = true
        aboutPage.isHidden = true
        PageSwitchTarget.shared.select(0)

        // 页脚版本号：三端设置窗底部固定显示「Chaoset 版本 x.y.z」。
        let version = (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "dev"
        let footerLabel = NSTextField(labelWithString: "Chaoset 版本 \(version)")
        footerLabel.font = .systemFont(ofSize: 11)
        footerLabel.textColor = SettingsTheme.textSecondary
        footerLabel.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(footerLabel)

        NSLayoutConstraint.activate([
            navColumn.topAnchor.constraint(equalTo: root.topAnchor),
            navColumn.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            navColumn.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            navColumn.widthAnchor.constraint(equalToConstant: SettingsTheme.navWidth),

            sidebar.topAnchor.constraint(equalTo: navColumn.topAnchor, constant: SettingsTheme.contentTop),
            sidebar.leadingAnchor.constraint(equalTo: navColumn.leadingAnchor, constant: 12),
            sidebar.trailingAnchor.constraint(lessThanOrEqualTo: navColumn.trailingAnchor, constant: -12),

            contentArea.topAnchor.constraint(equalTo: root.topAnchor, constant: SettingsTheme.contentTop),
            contentArea.leadingAnchor.constraint(equalTo: navColumn.trailingAnchor,
                                                constant: SettingsTheme.contentLeftGap),
            contentArea.trailingAnchor.constraint(equalTo: root.trailingAnchor,
                                                 constant: -SettingsTheme.contentRight),
            contentArea.bottomAnchor.constraint(equalTo: root.bottomAnchor,
                                               constant: -SettingsTheme.footerBottom - 20),

            pageStack.topAnchor.constraint(equalTo: contentArea.topAnchor),
            pageStack.leadingAnchor.constraint(equalTo: contentArea.leadingAnchor),
            pageStack.trailingAnchor.constraint(equalTo: contentArea.trailingAnchor),
            pageStack.bottomAnchor.constraint(lessThanOrEqualTo: contentArea.bottomAnchor),

            footerLabel.leadingAnchor.constraint(equalTo: contentArea.leadingAnchor),
            footerLabel.bottomAnchor.constraint(equalTo: root.bottomAnchor,
                                               constant: -SettingsTheme.footerBottom),
        ])
        return root
    }
}
