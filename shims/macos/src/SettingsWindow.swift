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
import UniformTypeIdentifiers

@objc final class AssociateSwitchTarget: NSObject {
    @objc func switchChanged(_ sender: NSSwitch) {
        AssociateSetting.enabled = sender.state == .on
        if !AssociateSetting.enabled {
            NotificationCenter.default.post(
                name: .chaosetAssociateDisabled, object: nil)
        }
    }
}

extension Notification.Name {
    static let chaosetAssociateDisabled =
        Notification.Name("chaosetAssociateDisabled")
}

/// 中英状态作用域三选一的 target（2026-11-06，与 Windows 设置窗对齐）。
@objc final class ModeScopeTarget: NSObject {
    @objc func scopeChanged(_ sender: NSButton) {
        // radioButtonWithTitle 是自动互斥的；用标题反查 scope 最稳妥
        // （tag 在初始化后绑定时容易与顺序耦合）。
        switch sender.title {
        case "每程序": ModeScopeSetting.current = .process
        case "每窗口": ModeScopeSetting.current = .window
        default: ModeScopeSetting.current = .system
        }
    }
}

/// 设置窗左侧导航的页切换 target（2026-11-06 卡片式布局，与 Windows 对齐）。
/// 三个页面视图在构建时注册进来；按按钮 identifier（"0"/"1"/"2"）切页。
final class PageSwitchTarget: NSObject {
    static let shared = PageSwitchTarget()
    weak var inputPage: NSView?
    weak var dictPage: NSView?
    weak var aboutPage: NSView?

    @objc func switchPage(_ sender: NSButton) {
        let idx = sender.identifier?.rawValue ?? "0"
        inputPage?.isHidden = idx != "0"
        dictPage?.isHidden = idx != "1"
        aboutPage?.isHidden = idx != "2"
    }
}

/// 学习数据区的按钮动作（清空/导出）与统计刷新。
/// 单独成类是为了 @objc target/action 能拿到稳定对象；不持有窗口强引用。
@objc final class LearningDataTarget: NSObject {
    weak var statsLabel: NSTextField?

    @objc func clearLearningData(_ sender: NSButton) {
        let alert = NSAlert()
        alert.messageText = "清空学习数据？"
        alert.informativeText = "将删除全部调频词、用户词组与上下文共现记录（仅本机），操作不可恢复。"
        alert.alertStyle = .warning
        alert.addButton(withTitle: "清空")
        alert.addButton(withTitle: "取消")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        if SettingsWindowController.engine.userClear() {
            refresh()
        } else {
            Self.showError("清空失败：daemon 未运行，或未启用用户词典。")
        }
    }

    @objc func exportLearningData(_ sender: NSButton) {
        let panel = NSSavePanel()
        panel.title = "导出学习数据"
        panel.nameFieldStringValue = "chaoset-userdict.tsv"
        if let tsv = UTType(filenameExtension: "tsv") {
            panel.allowedContentTypes = [tsv]
        }
        guard panel.runModal() == .OK, let url = panel.url else { return }
        guard let tsv = SettingsWindowController.engine.userExportAll() else {
            Self.showError("导出失败：daemon 未运行，或未启用用户词典。")
            return
        }
        do {
            try tsv.write(to: url, atomically: true, encoding: .utf8)
        } catch {
            Self.showError("写入文件失败：\(error.localizedDescription)")
        }
    }

    /// 从 daemon 拉取统计并更新标签；daemon 不可达时显示提示而不是报错弹窗
    /// （用户只是打开设置窗口，不应该被错误弹窗打断）。
    func refresh() {
        guard let label = statsLabel else { return }
        if let s = SettingsWindowController.engine.userStats() {
            label.stringValue = "调频词 \(s.words) 条 · 用户词组 \(s.phrases) 条 · 上下文 \(s.bigrams) 条"
            label.textColor = .labelColor
        } else {
            label.stringValue = "daemon 未运行，无法读取学习数据"
            label.textColor = .secondaryLabelColor
        }
    }

    private static func showError(_ text: String) {
        let alert = NSAlert()
        alert.messageText = "学习数据"
        alert.informativeText = text
        alert.alertStyle = .warning
        alert.addButton(withTitle: "好")
        alert.runModal()
    }
}

/// 「词库」区的按钮动作：打开随包安装的 Chaoset.app（安装与设置）并请求它
/// 直接弹出导入面板（`--import`）。导入实现只在 Setup.app 里有一份，输入法
/// 壳不复制粘贴合并/备份/原子替换逻辑（避免两套实现漂移）。
@objc final class DictionaryImportTarget: NSObject {
    @objc func openDictionaryImport(_ sender: NSButton) {
        let fm = FileManager.default
        let home = NSHomeDirectory()
        var candidates = [
            home + "/Applications/Chaoset.app",
            home + "/Applications/ChaosetSetup.app",
            "/Applications/Chaoset.app",
        ]
        // 开发构建：与本 app 同级的 build/ChaosetSetup.app。
        candidates.append(Bundle.main.bundleURL.deletingLastPathComponent()
            .appendingPathComponent("ChaosetSetup.app").path)
        guard let path = candidates.first(where: { fm.fileExists(atPath: $0) }) else {
            let alert = NSAlert()
            alert.messageText = "导入词库"
            alert.informativeText = "未找到「Chaoset 安装与设置」。请从安装包重新安装，或直接打开「Chaoset」应用导入词库。"
            alert.alertStyle = .warning
            alert.addButton(withTitle: "好")
            alert.runModal()
            return
        }
        let config = NSWorkspace.OpenConfiguration()
        config.arguments = ["--import"]
        NSWorkspace.shared.openApplication(at: URL(fileURLWithPath: path),
                                           configuration: config) { _, error in
            guard let error else { return }
            DispatchQueue.main.async {
                let alert = NSAlert()
                alert.messageText = "导入词库"
                alert.informativeText = "无法打开「Chaoset 安装与设置」：\(error.localizedDescription)"
                alert.alertStyle = .warning
                alert.addButton(withTitle: "好")
                alert.runModal()
            }
        }
    }
}

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
        let pageTarget = PageSwitchTarget.shared

        // —— 输入页 ——
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

        // —— 词库页 ——
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

        let dictHint = NSTextField(wrappingLabelWithString:
            "打开「Chaoset 安装与设置」导入 Rime dict.yaml / CC-CEDICT txt / TSV；导入后自动重启 daemon。")
        dictHint.font = .systemFont(ofSize: 11)
        dictHint.textColor = .secondaryLabelColor
        dictHint.preferredMaxLayoutWidth = 320
        dictPage.addArrangedSubview(dictHint)

        // —— 关于页 ——
        let aboutPage = NSStackView()
        aboutPage.orientation = .vertical
        aboutPage.alignment = .leading
        aboutPage.spacing = 12

        let more = NSTextField(labelWithString: "更多设置将在后续版本提供")
        more.font = .systemFont(ofSize: 11)
        more.textColor = .tertiaryLabelColor
        aboutPage.addArrangedSubview(more)

        let version = (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "dev"
        let versionLabel = NSTextField(labelWithString: "Chaoset 版本 \(version)")
        versionLabel.font = .systemFont(ofSize: 11)
        versionLabel.textColor = .tertiaryLabelColor
        aboutPage.addArrangedSubview(versionLabel)

        let repo = NSTextField(labelWithString: "https://github.com/ShanWuYinShe/chaoset_ime")
        repo.font = .systemFont(ofSize: 11)
        repo.textColor = .tertiaryLabelColor
        aboutPage.addArrangedSubview(repo)

        // —— 左侧导航 ——
        let sidebar = NSStackView()
        sidebar.orientation = .vertical
        sidebar.alignment = .leading
        sidebar.spacing = 6
        let appTitle = NSTextField(labelWithString: "Chaoset 设置")
        appTitle.font = .boldSystemFont(ofSize: 14)
        sidebar.addArrangedSubview(appTitle)
        PageSwitchTarget.shared.inputPage = inputPage
        PageSwitchTarget.shared.dictPage = dictPage
        PageSwitchTarget.shared.aboutPage = aboutPage
        for (idx, title) in [("0", "输入"), ("1", "词库"), ("2", "关于")] {
            let b = NSButton(title: title, target: PageSwitchTarget.shared,
                             action: #selector(PageSwitchTarget.switchPage(_:)))
            b.isBordered = false
            b.identifier = NSUserInterfaceItemIdentifier(idx)
            b.font = .systemFont(ofSize: 13)
            sidebar.addArrangedSubview(b)
        }

        // 整窗：横向 [导航 | 内容页]；初始只显示输入页。
        let content = NSStackView()
        content.orientation = .horizontal
        content.alignment = .top
        content.spacing = 24
        content.edgeInsets = NSEdgeInsets(top: 20, left: 20, bottom: 24, right: 28)
        content.addArrangedSubview(sidebar)
        content.addArrangedSubview(inputPage)
        content.addArrangedSubview(dictPage)
        content.addArrangedSubview(aboutPage)
        dictPage.isHidden = true
        aboutPage.isHidden = true

        let w = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 640, height: 480),
            styleMask: [.titled, .closable],
            backing: .buffered,
            defer: false)
        w.title = "Chaoset 设置"
        w.contentView = content
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