// SettingsWindowPages.swift：设置窗三页视图构建（输入 / 词库 / 关于）。
//
// 从 SettingsWindowController.show() 原地抽出（2026-11-06 架构拆分，布局/文案/
// 约束与控件语义零改动）；左导航与窗口骨架仍在 SettingsWindow.swift。

import AppKit

// MARK: - 三页视图构建

enum SettingsWindowPages {

    // —— 输入页 ——
    /// 输入页：输入行为（词组联想开关）+ 中英状态（共享作用域三选一）。
    static func makeInputPage(switchTarget: AssociateSwitchTarget,
                              modeTarget: ModeScopeTarget) -> NSStackView {
        let inputPage = NSStackView()
        inputPage.orientation = .vertical
        inputPage.alignment = .leading
        inputPage.spacing = 12

        let group = NSTextField(labelWithString: "输入行为")
        group.font = .boldSystemFont(ofSize: 13)
        group.textColor = .secondaryLabelColor
        inputPage.addArrangedSubview(group)

        let row = NSStackView()
        row.orientation = .horizontal
        row.spacing = 12
        let label = NSTextField(labelWithString: "词组联想")
        label.font = .systemFont(ofSize: 13)
        let sw = NSSwitch()
        sw.state = AssociateSetting.enabled ? .on : .off
        sw.target = switchTarget
        sw.action = #selector(AssociateSwitchTarget.switchChanged(_:))
        row.addArrangedSubview(label)
        row.addArrangedSubview(sw)
        inputPage.addArrangedSubview(row)

        let hint = NSTextField(wrappingLabelWithString:
            "选词上屏后展示可续接的词组候选；关闭后不再弹出联想候选。")
        hint.font = .systemFont(ofSize: 11)
        hint.textColor = .secondaryLabelColor
        hint.preferredMaxLayoutWidth = 320
        inputPage.addArrangedSubview(hint)

        let modeGroup = NSTextField(labelWithString: "中英状态")
        modeGroup.font = .boldSystemFont(ofSize: 13)
        modeGroup.textColor = .secondaryLabelColor
        inputPage.addArrangedSubview(modeGroup)

        let modeRow = NSStackView()
        modeRow.orientation = .horizontal
        modeRow.spacing = 8
        for scope in ModeScope.allCases {
            let title: String
            switch scope {
            case .system: title = "系统共享"
            case .process: title = "每程序"
            case .window: title = "每窗口"
            }
            let btn = NSButton(radioButtonWithTitle: title, target: modeTarget,
                               action: #selector(ModeScopeTarget.scopeChanged(_:)))
            btn.state = ModeScopeSetting.current == scope ? .on : .off
            modeRow.addArrangedSubview(btn)
        }
        inputPage.addArrangedSubview(modeRow)

        let modeHint = NSTextField(wrappingLabelWithString:
            "切一次所有程序都生效（推荐）；后两者仅当前程序/窗口独立。")
        modeHint.font = .systemFont(ofSize: 11)
        modeHint.textColor = .secondaryLabelColor
        modeHint.preferredMaxLayoutWidth = 320
        inputPage.addArrangedSubview(modeHint)
        return inputPage
    }

    // —— 词库页 ——
    /// 词库页：学习数据（统计/清空/导出）+ 词库导入入口。
    static func makeDictPage(learningTarget: LearningDataTarget,
                             dictImportTarget: DictionaryImportTarget) -> NSStackView {
        let dictPage = NSStackView()
        dictPage.orientation = .vertical
        dictPage.alignment = .leading
        dictPage.spacing = 12

        let dataGroup = NSTextField(labelWithString: "学习数据")
        dataGroup.font = .boldSystemFont(ofSize: 13)
        dataGroup.textColor = .secondaryLabelColor
        dictPage.addArrangedSubview(dataGroup)

        let stats = NSTextField(labelWithString: "读取中…")
        stats.font = .systemFont(ofSize: 13)
        dictPage.addArrangedSubview(stats)
        learningTarget.statsLabel = stats

        let dataRow = NSStackView()
        dataRow.orientation = .horizontal
        dataRow.spacing = 12
        let clearBtn = NSButton(title: "清空学习数据…", target: learningTarget,
                               action: #selector(LearningDataTarget.clearLearningData(_:)))
        let exportBtn = NSButton(title: "导出学习数据…", target: learningTarget,
                                action: #selector(LearningDataTarget.exportLearningData(_:)))
        dataRow.addArrangedSubview(clearBtn)
        dataRow.addArrangedSubview(exportBtn)
        dictPage.addArrangedSubview(dataRow)

        let dataHint = NSTextField(wrappingLabelWithString:
            "学习数据仅保存在本机，用于调频与词组联想；清空后不可恢复。")
        dataHint.font = .systemFont(ofSize: 11)
        dataHint.textColor = .secondaryLabelColor
        dataHint.preferredMaxLayoutWidth = 320
        dictPage.addArrangedSubview(dataHint)

        let dictGroup = NSTextField(labelWithString: "词库")
        dictGroup.font = .boldSystemFont(ofSize: 13)
        dictGroup.textColor = .secondaryLabelColor
        dictPage.addArrangedSubview(dictGroup)

        let importBtn = NSButton(title: "导入词库…", target: dictImportTarget,
                                 action: #selector(DictionaryImportTarget.openDictionaryImport(_:)))
        dictPage.addArrangedSubview(importBtn)

        // 词库导入说明：2026-11-06 阶段2 三端统一句「支持 dict.yaml / CC-CEDICT
        // txt / TSV；导入后自动重启输入法。」（Windows/Linux 同句）；macOS 导入
        // 机制不同（打开 Setup.app），在统一句后补括注，不替换统一句。
        let dictHint = NSTextField(wrappingLabelWithString:
            "支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。（会打开「Chaoset 安装与设置」完成导入）")
        dictHint.font = .systemFont(ofSize: 11)
        dictHint.textColor = .secondaryLabelColor
        dictHint.preferredMaxLayoutWidth = 320
        dictPage.addArrangedSubview(dictHint)
        return dictPage
    }

    // —— 关于页 ——
    /// 关于页：应用说明 / 许可证 / 仓库链接（版本号统一在窗口底部页脚）。
    static func makeAboutPage() -> NSStackView {
        let aboutPage = NSStackView()
        aboutPage.orientation = .vertical
        aboutPage.alignment = .leading
        aboutPage.spacing = 12

        // 关于分组标题（2026-11-06 阶段2：与 Windows 卡片标题「关于」、Linux
        // 「关于」分组统一）。版本号统一在窗口底部页脚，本页不再重复。
        let aboutGroup = NSTextField(labelWithString: "关于")
        aboutGroup.font = .boldSystemFont(ofSize: 13)
        aboutGroup.textColor = .secondaryLabelColor
        aboutPage.addArrangedSubview(aboutGroup)

        let appDesc = NSTextField(labelWithString: "跨平台中文输入法（macOS IMK 壳）")
        appDesc.font = .systemFont(ofSize: 11)
        appDesc.textColor = .secondaryLabelColor
        aboutPage.addArrangedSubview(appDesc)

        let license = NSTextField(labelWithString: "代码 MIT · 词库 CC BY-SA 4.0")
        license.font = .systemFont(ofSize: 11)
        license.textColor = .secondaryLabelColor
        aboutPage.addArrangedSubview(license)

        let more = NSTextField(labelWithString: "更多设置将在后续版本提供")
        more.font = .systemFont(ofSize: 11)
        more.textColor = .tertiaryLabelColor
        aboutPage.addArrangedSubview(more)

        let repo = NSTextField(labelWithString: "https://github.com/ShanWuYinShe/chaoset_ime")
        repo.font = .systemFont(ofSize: 11)
        repo.textColor = .tertiaryLabelColor
        aboutPage.addArrangedSubview(repo)
        return aboutPage
    }
}
