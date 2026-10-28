// SettingsWindow.swift：输入法设置窗口（独立窗口，参考市面输入法：
// 候选栏齿轮 → 打开设置窗口，窗口内为可扩展的设置项列表）。
//
// 输入法进程的激活策略是 .prohibited（IMK 要求，不占 Dock），弹普通
// NSWindow 前需临时切到 .accessory 才能成为 key 窗口正常交互。
// 窗口单例：已打开则前置，关闭仅隐藏（isReleasedWhenClosed=false，
// 否则关闭后静态指针悬空崩溃）。

import AppKit

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

enum SettingsWindowController {
    private static var window: NSWindow?
    private static let switchTarget = AssociateSwitchTarget()

    static func show() {
        NSApp.setActivationPolicy(.accessory)
        NSApp.activate(ignoringOtherApps: true)

        if let w = window {
            w.makeKeyAndOrderFront(nil)
            w.center()
            return
        }

        let content = NSStackView()
        content.orientation = .vertical
        content.alignment = .leading
        content.spacing = 12
        content.edgeInsets = NSEdgeInsets(top: 24, left: 28, bottom: 24, right: 28)

        // —— 分组标题：输入行为 ——
        let group = NSTextField(labelWithString: "输入行为")
        group.font = .boldSystemFont(ofSize: 13)
        group.textColor = .secondaryLabelColor
        content.addArrangedSubview(group)

        // 词组联想开关行：标签 + NSSwitch
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
        content.addArrangedSubview(row)

        // 说明文字
        let hint = NSTextField(wrappingLabelWithString:
            "选词上屏后展示可续接的词组候选；关闭后不再弹出联想候选。")
        hint.font = .systemFont(ofSize: 11)
        hint.textColor = .secondaryLabelColor
        hint.preferredMaxLayoutWidth = 320
        content.addArrangedSubview(hint)

        // 底部提示：更多设置后续版本提供
        let more = NSTextField(labelWithString: "更多设置将在后续版本提供")
        more.font = .systemFont(ofSize: 11)
        more.textColor = .tertiaryLabelColor
        content.addArrangedSubview(more)

        let w = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 380, height: 190),
            styleMask: [.titled, .closable],
            backing: .buffered,
            defer: false)
        w.title = "Chaoset 设置"
        w.contentView = content
        w.isReleasedWhenClosed = false
        w.center()
        w.makeKeyAndOrderFront(nil)
        window = w

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
