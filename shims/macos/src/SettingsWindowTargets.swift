// SettingsWindowTargets.swift：设置窗的 @objc target/action。
//
// 从 SettingsWindow.swift 拆出（2026-11-06 架构拆分，纯移动、行为零改动）：
// 联想开关、中英作用域二选一、左导航翻页、学习数据（统计/清空/导出）与
// 词库导入入口。窗口/控制器见 SettingsWindow.swift，三页视图见
// SettingsWindowPages.swift。

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

/// 中英状态作用域二选一的 target（与 Windows/Linux 设置窗对齐）。
@objc final class ModeScopeTarget: NSObject {
    @objc func scopeChanged(_ sender: NSButton) {
        // radioButtonWithTitle 是自动互斥的；用标题反查 scope 最稳妥
        // （tag 在初始化后绑定时容易与顺序耦合）。
        switch sender.title {
        case "每进程独立": ModeScopeSetting.current = .process
        default: ModeScopeSetting.current = .system
        }
    }
}

/// 设置窗左侧导航的页切换 target（2026-11-06 卡片式布局，与 Windows 对齐）。
/// 三个页面视图在构建时注册进来；按按钮 identifier（"0"/"1"/"2"）切页。
/// 2026-11-06 阶段2：补左导航选中态——当前页按钮用 accent 胶囊底 + 白字
/// （Windows 左导航同为 accent RGB 0A84FF 圆角高亮；此前 macOS 三个按钮外观
/// 完全相同，看不出当前在哪一页）。
final class PageSwitchTarget: NSObject {
    static let shared = PageSwitchTarget()
    weak var inputPage: NSView?
    weak var dictPage: NSView?
    weak var aboutPage: NSView?
    /// 左导航三个按钮（窗口单例生命周期内复用，切换选中态用）。
    var navButtons: [NSButton] = []

    @objc func switchPage(_ sender: NSButton) {
        select(Int(sender.identifier?.rawValue ?? "0") ?? 0)
    }

    /// 切到指定页并同步左导航选中态。
    func select(_ idx: Int) {
        inputPage?.isHidden = idx != 0
        dictPage?.isHidden = idx != 1
        aboutPage?.isHidden = idx != 2
        for (i, button) in navButtons.enumerated() {
            styleNavButton(button, selected: i == idx)
        }
    }

    /// 选中项：accent 胶囊底 + 白字；未选中：透明底 + 主文本色。
    private func styleNavButton(_ button: NSButton, selected: Bool) {
        let accent = NSColor(srgbRed: 0x0A / 255.0, green: 0x84 / 255.0, blue: 0xFF / 255.0, alpha: 1)
        button.wantsLayer = true
        button.layer?.cornerRadius = 8
        button.layer?.backgroundColor = selected ? accent.cgColor : NSColor.clear.cgColor
        button.attributedTitle = NSAttributedString(
            string: button.title,
            attributes: [
                .font: NSFont.systemFont(ofSize: 13),
                .foregroundColor: selected ? NSColor.white : NSColor.labelColor,
            ])
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
