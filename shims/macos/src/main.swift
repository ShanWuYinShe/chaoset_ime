import AppKit
import InputMethodKit

// 入口：装配 IMKServer 并进入应用主循环。
// 系统通过 Info.plist 里的 InputMethodConnectionName / InputMethodServerControllerClass
// 与本进程建立连接；bundleIdentifier 用于 IMKServer 定位控制器类。
// 注意：该值必须与 IMKServer 实际注册的 mach 服务名一致（由 bundle id 推导：
// <CFBundleIdentifier>_Connection）。不一致时 imklaunchagent 会拒绝连接：
// "Refusing connection name for bundle: unrecognized 'InputMethodConnectionName' value"
// （2026-10-22 实测，表现为选中输入法后打字完全没反应）。
let connectionName = Bundle.main.object(forInfoDictionaryKey: "InputMethodConnectionName")
    as? String ?? "ime.chaoset.inputmethod.ChaosetIME_Connection"
let bundleID = Bundle.main.object(forInfoDictionaryKey: "CFBundleIdentifier")
    as? String ?? "ime.chaoset.inputmethod.ChaosetIME"
let server = IMKServer(name: connectionName, bundleIdentifier: bundleID)
ChaosetController.sharedServer = server

let app = NSApplication.shared
app.setActivationPolicy(.prohibited)
app.run()
