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

/// 中英状态的共享作用域（2026-11-06 用户要求可配置，与 Windows
/// HKCU\Software\Chaoset\ModeScope 同语义）。
///
/// - system：全系统共享（默认）——切一次所有程序都生效。
/// - process：每个程序各自独立。
/// - window：每个窗口/输入上下文独立。
///
/// macOS 侧由本进程（IMK 输入法）持有模式状态，三档分别落到：
/// system → UserDefaults（跨进程共享）；process → 静态量；window → 按 client 标识。
enum ModeScope: String, CaseIterable {
    case system
    case process
    case window
}

enum ModeScopeSetting {
    private static let key = "ChaosetModeScope"

    static var current: ModeScope {
        get {
            if let raw = UserDefaults.standard.string(forKey: key),
               let scope = ModeScope(rawValue: raw) {
                return scope
            }
            return .system
        }
        set { UserDefaults.standard.set(newValue.rawValue, forKey: key) }
    }
}
