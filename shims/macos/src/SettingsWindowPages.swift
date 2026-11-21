// SettingsWindowPages.swift：设置窗三页视图构建（输入 / 词库 / 关于）。
//
// 从 SettingsWindowController.show() 原地抽出（2026-11-06 架构拆分），
// 三端同构的卡片布局：每页由 SettingsCardView
// （见 SettingsWindow.swift）分组，行/说明/按钮/胶囊统一由本文件 builder 产出。
// 布局只引用 SettingsTheme 令牌，不写字面色值（checkuiparity 断言色值唯一来源）。

import AppKit

// MARK: - 三页视图构建

enum SettingsWindowPages {

    // MARK: 共享 builder

    private static func makePage() -> NSStackView {
        let page = NSStackView()
        page.orientation = .vertical
        page.alignment = .leading
        page.spacing = SettingsTheme.cardGap
        return page
    }

    /// 卡片与所在页同宽（页面宽度由窗口约束给定，卡片撑满内容区）。
    private static func addCard(_ card: SettingsCardView, to page: NSStackView) {
        card.translatesAutoresizingMaskIntoConstraints = false
        page.addArrangedSubview(card)
        card.widthAnchor.constraint(equalTo: page.widthAnchor).isActive = true
    }

    /// 行：左标签 + 右控件，行高 36（三端同一节奏）。
    private static func makeRow(labelText: String, control: NSView) -> NSView {
        let row = NSView()
        row.translatesAutoresizingMaskIntoConstraints = false
        let label = NSTextField(labelWithString: labelText)
        label.font = .systemFont(ofSize: 13)
        label.textColor = SettingsTheme.textPrimary
        label.translatesAutoresizingMaskIntoConstraints = false
        control.translatesAutoresizingMaskIntoConstraints = false
        row.addSubview(label)
        row.addSubview(control)
        NSLayoutConstraint.activate([
            row.heightAnchor.constraint(equalToConstant: SettingsTheme.rowHeight),
            label.leadingAnchor.constraint(equalTo: row.leadingAnchor),
            label.centerYAnchor.constraint(equalTo: row.centerYAnchor),
            control.trailingAnchor.constraint(equalTo: row.trailingAnchor),
            control.centerYAnchor.constraint(equalTo: row.centerYAnchor),
        ])
        return row
    }

    /// 说明文字：11pt 次级文字，两行内自动换行（卡片内容宽 408pt）。
    private static func makeHint(_ text: String) -> NSTextField {
        let hint = NSTextField(wrappingLabelWithString: text)
        hint.font = .systemFont(ofSize: 11)
        hint.textColor = SettingsTheme.textSecondary
        hint.preferredMaxLayoutWidth = 408
        return hint
    }

    /// 横向按钮组（间距 8，左对齐）。
    private static func makeButtonRow(_ buttons: [NSView]) -> NSStackView {
        let row = NSStackView(views: buttons)
        row.orientation = .horizontal
        row.spacing = 8
        row.alignment = .centerY
        return row
    }

    // MARK: 输入页

    /// 输入页：输入行为（词组联想/模糊音/双拼方案）+ 中英状态（共享作用域二选一）
    /// + 外观主题（跟随系统/浅色/深色）。
    static func makeInputPage(switchTarget: AssociateSwitchTarget,
                               fuzzyTarget: FuzzySwitchTarget,
                               shuangpinTarget: ShuangpinTarget,
                               modeTarget: ModeScopeTarget,
                               themeTarget: ThemeSegmentTarget) -> NSView {
        let page = makePage()

        // 卡 1：输入行为——词组联想 / 模糊音 / 双拼方案（自绘蓝白开关与软胶囊，
        // 三端同形态）。模糊音与双拼由 daemon 启动时读取，改动后 target 里重启 daemon。
        let toggle = WarmSwitch()
        toggle.state = AssociateSetting.enabled ? .on : .off
        toggle.target = switchTarget
        toggle.action = #selector(AssociateSwitchTarget.switchChanged(_:))

        let fuzzyToggle = WarmSwitch()
        fuzzyToggle.state = FuzzySetting.enabled ? .on : .off
        fuzzyToggle.target = fuzzyTarget
        fuzzyToggle.action = #selector(FuzzySwitchTarget.switchChanged(_:))

        var shuangpinButtons: [WarmPillButton] = []
        for scheme in Shuangpin.allCases {
            let button = WarmPillButton(title: scheme.displayName, kind: .choice)
            button.target = shuangpinTarget
            button.action = #selector(ShuangpinTarget.schemeChanged(_:))
            button.setPillSelected(ShuangpinSetting.current == scheme)
            shuangpinButtons.append(button)
        }
        shuangpinTarget.shuangpinButtons = shuangpinButtons

        addCard(SettingsCardView(title: "输入行为", content: [
            .full(makeRow(labelText: "词组联想", control: toggle)),
            .full(makeHint("选词上屏后展示可续接的词组候选；关闭后不再弹出联想候选。")),
            .full(makeRow(labelText: "模糊音", control: fuzzyToggle)),
            .full(makeHint("让 zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing 混用也能命中；打开后候选可能变多。")),
            .natural(makeButtonRow(shuangpinButtons)),
            .full(makeHint("双拼方案：全拼（默认）/自然码/微软/小鹤，两键一音节。修改后输入法会短暂重启以生效。")),
        ]), to: page)

        // 卡 2：中英状态二选一（两个软胶囊，选中 accentSoft + accentText）
        var scopeButtons: [WarmPillButton] = []
        for scope in ModeScope.allCases {
            let title: String
            switch scope {
            case .system: title = "全局共享"
            case .process: title = "每进程独立"
            }
            let button = WarmPillButton(title: title, kind: .choice)
            button.target = modeTarget
            button.action = #selector(ModeScopeTarget.scopeChanged(_:))
            button.setPillSelected(ModeScopeSetting.current == scope)
            scopeButtons.append(button)
        }
        modeTarget.scopeButtons = scopeButtons
        addCard(SettingsCardView(title: "中英状态", content: [
            .natural(makeButtonRow(scopeButtons)),
            .full(makeHint("切一次所有程序都生效（推荐）；每进程则各自独立。")),
        ]), to: page)

        // 卡 3：外观主题三选一（跟随系统/浅色/深色，软胶囊与二选一同形态）
        var themeButtons: [WarmPillButton] = []
        for theme in Theme.allCases {
            let button = WarmPillButton(title: theme.displayName, kind: .choice)
            button.target = themeTarget
            button.action = #selector(ThemeSegmentTarget.themeChanged(_:))
            button.setPillSelected(ThemeSetting.current == theme)
            themeButtons.append(button)
        }
        themeTarget.themeButtons = themeButtons
        addCard(SettingsCardView(title: "外观主题", content: [
            .natural(makeButtonRow(themeButtons)),
            .full(makeHint("跟随系统按 macOS 外观自动切换；浅色/深色固定候选窗与设置窗配色。")),
        ]), to: page)
        return page
    }

    // MARK: 词库页

    /// 词库页：学习数据（统计/清空/导出）+ 词库导入入口。
    static func makeDictPage(learningTarget: LearningDataTarget,
                             dictImportTarget: DictionaryImportTarget) -> NSView {
        let page = makePage()

        // 卡 1：学习数据（统计 + 次按钮组 + 说明）
        let stats = NSTextField(labelWithString: "读取中…")
        stats.font = .systemFont(ofSize: 13)
        stats.textColor = SettingsTheme.textPrimary
        learningTarget.statsLabel = stats

        let clearButton = WarmButton(title: "清空学习数据…", style: .destructive)
        clearButton.target = learningTarget
        clearButton.action = #selector(LearningDataTarget.clearLearningData(_:))
        let exportButton = WarmButton(title: "导出学习数据…", style: .secondary)
        exportButton.target = learningTarget
        exportButton.action = #selector(LearningDataTarget.exportLearningData(_:))
        addCard(SettingsCardView(title: "学习数据", content: [
            .natural(stats),
            .natural(makeButtonRow([clearButton, exportButton])),
            .full(makeHint("学习数据仅保存在本机，用于调频与词组联想；清空后不可恢复。")),
        ]), to: page)

        // 卡 2：词库导入（主按钮 + 三端统一说明）
        let importButton = WarmButton(title: "导入词库…", style: .primary)
        importButton.target = dictImportTarget
        importButton.action = #selector(DictionaryImportTarget.openDictionaryImport(_:))
        addCard(SettingsCardView(title: "词库", content: [
            .natural(importButton),
            .full(makeHint("支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。（会打开「Chaoset 安装与设置」完成导入）")),
        ]), to: page)
        return page
    }

    // MARK: 关于页

    /// 关于页：应用图标 + 名称/平台 + 许可证/更多设置/仓库链接（版本号在页脚）。
    static func makeAboutPage() -> NSView {
        let page = makePage()

        let iconView = NSImageView()
        iconView.image = NSImage(named: NSImage.applicationIconName) ?? NSApp.applicationIconImage
        iconView.imageScaling = .scaleProportionallyUpOrDown
        iconView.translatesAutoresizingMaskIntoConstraints = false
        iconView.widthAnchor.constraint(equalToConstant: 44).isActive = true
        iconView.heightAnchor.constraint(equalToConstant: 44).isActive = true

        let name = NSTextField(labelWithString: "Chaoset 输入法")
        name.font = .systemFont(ofSize: 15, weight: .semibold)
        name.textColor = SettingsTheme.textPrimary
        let subtitle = NSTextField(labelWithString: "跨平台中文输入法（macOS IMK 壳）")
        subtitle.font = .systemFont(ofSize: 11)
        subtitle.textColor = SettingsTheme.textSecondary
        let titleText = NSStackView(views: [name, subtitle])
        titleText.orientation = .vertical
        titleText.alignment = .leading
        titleText.spacing = 2

        let titleRow = NSStackView(views: [iconView, titleText])
        titleRow.orientation = .horizontal
        titleRow.alignment = .centerY
        titleRow.spacing = 14

        let license = NSTextField(labelWithString: "代码 MIT · 词库 CC BY-SA 4.0")
        license.font = .systemFont(ofSize: 11)
        license.textColor = SettingsTheme.textSecondary
        let more = NSTextField(labelWithString: "更多设置将在后续版本提供")
        more.font = .systemFont(ofSize: 11)
        more.textColor = SettingsTheme.textTertiary
        let repo = NSTextField(labelWithString: "https://github.com/ShanWuYinShe/chaoset_ime")
        repo.font = .systemFont(ofSize: 11)
        repo.textColor = SettingsTheme.accentText

        addCard(SettingsCardView(title: "关于", content: [
            .natural(titleRow),
            .natural(license),
            .natural(more),
            .natural(repo),
        ]), to: page)
        return page
    }
}
