// CandidateSettings.swift：候选窗/设置窗共享的本地设置模型（联想开关、中英作用域）。
//
// 从 CandidatePanel.swift 拆出（2026-11-06 架构拆分，纯移动、行为零改动）。
// 持久化仍是 macOS 本机 UserDefaults（与 Windows 注册表 / Linux config.json 同语义）；
// 设置窗（SettingsWindow.swift）复用这里的 AssociateSetting / ModeScope(Setting)。

import Foundation
import AppKit

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

// MARK: - 模糊音（输入行为）

/// 模糊音开关的持久化与读取（三端同语义：macOS UserDefaults `ChaosetFuzzy`、
/// Windows HKCU `Fuzzy`、Linux config.json `fuzzy`，缺省关闭）。
/// daemon 启动时读同一键；设置窗改动后由 target 重启 daemon 生效。
enum FuzzySetting {
    static let key = "ChaosetFuzzy"

    static var enabled: Bool {
        get { UserDefaults.standard.object(forKey: key) as? Bool ?? false }
        set { UserDefaults.standard.set(newValue, forKey: key) }
    }
}

// MARK: - 双拼方案（输入行为）

/// 双拼方案（与 daemon `-shuangpin` / 引擎 ShuangpinScheme 名逐字一致）：
/// 空串 = 全拼；zrm 自然码；mspy 微软；flypy 小鹤。
enum Shuangpin: String, CaseIterable {
    case full = ""
    case zrm
    case mspy
    case flypy

    /// 设置窗分段控件显示名。
    var displayName: String {
        switch self {
        case .full: return "全拼"
        case .zrm: return "自然码"
        case .mspy: return "微软"
        case .flypy: return "小鹤"
        }
    }

    /// 存储读取的宽容归一化：大小写/首尾空白容忍，未知回退全拼
    /// （与 Go settings.parseStoredShuangpin 同口径）。
    static func normalized(raw: String) -> Shuangpin {
        switch raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "zrm": return .zrm
        case "mspy": return .mspy
        case "flypy": return .flypy
        default: return .full
        }
    }
}

enum ShuangpinSetting {
    static let key = "ChaosetShuangpin"

    static var current: Shuangpin {
        get {
            guard let raw = UserDefaults.standard.string(forKey: key) else {
                return .full
            }
            return Shuangpin.normalized(raw: raw)
        }
        set { UserDefaults.standard.set(newValue.rawValue, forKey: key) }
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

// MARK: - 外观主题（跟随系统 / 浅色 / 深色）

/// 用户可选外观（三端统一 schema：system|light|dark，缺省 system；
/// 与 Go internal/settings.theme、chaoset-settingsctl get/set theme 同语义）。
/// macOS 壳持久化在 UserDefaults `ChaosetTheme`（checker 断言该键存在）。
enum Theme: String, CaseIterable {
    case system
    case light
    case dark

    /// 宽容归一化：system/light/dark 保留；大小写/首尾空白容忍；未知回退 system。
    static func normalized(raw: String) -> Theme {
        switch raw.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "light": return .light
        case "dark": return .dark
        default: return .system
        }
    }

    /// 设置窗分段控件显示名。
    var displayName: String {
        switch self {
        case .system: return "跟随系统"
        case .light: return "浅色"
        case .dark: return "深色"
        }
    }

    /// 把用户选择与系统 appearance 合成为「是否深色」：
    /// system 跟随 appearance，light 恒 false，dark 恒 true。
    func isDark(effectiveAppearance: NSAppearance?) -> Bool {
        switch self {
        case .light: return false
        case .dark: return true
        case .system:
            return effectiveAppearance?.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
        }
    }
}

enum ThemeSetting {
    static let key = "ChaosetTheme"

    static var current: Theme {
        get {
            guard let raw = UserDefaults.standard.string(forKey: key) else {
                return .system
            }
            return Theme.normalized(raw: raw)
        }
        set { UserDefaults.standard.set(newValue.rawValue, forKey: key) }
    }
}

/// 主题解析助手：热路径（候选窗每次 draw）直接读 UserDefaults，不起进程、
/// 不读文件；设置窗据此强制窗口 appearance。
enum AppTheme {
    static var current: Theme { ThemeSetting.current }

    static func isDark(effectiveAppearance: NSAppearance?) -> Bool {
        current.isDark(effectiveAppearance: effectiveAppearance)
    }

    /// 设置窗窗口 appearance：system → nil（跟随系统），light → .aqua，dark → .darkAqua。
    static var windowAppearance: NSAppearance? {
        switch current {
        case .system: return nil
        case .light: return NSAppearance(named: .aqua)
        case .dark: return NSAppearance(named: .darkAqua)
        }
    }
}
