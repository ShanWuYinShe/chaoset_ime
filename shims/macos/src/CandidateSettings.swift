// CandidateSettings.swift：候选窗/设置窗共享的本地设置模型（联想开关、中英作用域）。
//
// 从 CandidatePanel.swift 拆出（2026-11-06 架构拆分，纯移动、行为零改动）。
// 持久化仍是 macOS 本机 UserDefaults（与 Windows 注册表 / Linux config.json 同语义）；
// 设置窗（SettingsWindow.swift）复用这里的 AssociateSetting / ModeScope(Setting)。

import Foundation

// MARK: - 设置开关（词组联想）

// 联想开关的持久化与读取。macOS 用 UserDefaults（候选栏齿轮菜单切换）；
// Windows 用注册表、Linux 用 config.json——三端存储各自独立，语义一致
// （未配置 = 开启）。
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

/// 中英状态的共享作用域（与 Windows HKCU\Software\Chaoset\ModeScope、
/// Linux config.json mode_scope 同语义，三端统一两档）。
///
/// - system：全局共享（默认）——切一次所有程序都生效。
/// - process：每进程独立——每个程序各自记住中英状态。
///
/// macOS 壳的中英模式由输入法自己维护（本进程本地持有）；本枚举负责设置窗渲染与
/// UserDefaults 持久化，schema 与另外两端保持同一份契约（旧三档值读取时归一化）。
enum ModeScope: String, CaseIterable {
    case system
    case process

    /// 存储读取的宽容归一化（三端一致）：system/process 保持原值；
    /// 旧版本三档值 window 并入 process；大小写与首尾空白容忍；未知/空值
    /// 回退缺省 system。
    static func normalized(raw: String) -> ModeScope {
        switch raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "process", "window":
            return .process
        default:
            return .system
        }
    }
}

enum ModeScopeSetting {
    private static let key = "ChaosetModeScope"

    static var current: ModeScope {
        get {
            guard let raw = UserDefaults.standard.string(forKey: key) else {
                return .system
            }
            return ModeScope.normalized(raw: raw)
        }
        set { UserDefaults.standard.set(newValue.rawValue, forKey: key) }
    }
}
