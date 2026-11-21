// CandidateSettings.swift：候选窗热路径的本地设置模型（联想开关）。
//
// 从 CandidatePanel.swift 拆出（2026-11-06 架构拆分，纯移动、行为零改动）。
// 持久化仍是 macOS 本机 UserDefaults（与 Windows 注册表 / Linux config.json 同语义）。
//
// 2026-11-21：原生 AppKit 设置窗删除，设置入口只剩 Slint 统一设置中心
// （shims/settings，经随包 chaosetctl 读写）。本文件随之裁掉仅设置窗渲染/持久化
// 使用的模糊音、双拼方案、中英作用域、外观主题枚举——那些设置项统一由设置中心
// 写入同一批 UserDefaults 键，读取方是 daemon（输入行为，见
// crates/chaosetd/src/input_settings.rs）与候选窗热路径（外观主题见
// CandidateTheme.swift 的 CandidateAppearance）；壳内不再重复维护设置窗 schema。

import Foundation

// MARK: - 设置开关（词组联想）

// 联想开关的持久化与读取。macOS 用 UserDefaults（齿轮 → 统一设置中心切换；
// 候选窗热路径每次取开关值时直接读，改完下一次进入联想即生效，未配置 = 开启）。
enum AssociateSetting {
    static let key = "AssociateEnabled"

    static var enabled: Bool {
        get { UserDefaults.standard.object(forKey: key) as? Bool ?? true }
        set { UserDefaults.standard.set(newValue, forKey: key) }
    }

    static func toggle() {
        enabled = !enabled
    }
}
