import AppKit
import InputMethodKit

// 入口：装配 IMKServer 并进入应用主循环。
// 系统通过 Info.plist 里的 InputMethodConnectionName / InputMethodServerControllerClass
// 与本进程建立连接；bundleIdentifier 用于 IMKServer 定位控制器类。
let connectionName = Bundle.main.object(forInfoDictionaryKey: "InputMethodConnectionName")
    as? String ?? "ChaosetIME_Connection"
let bundleID = Bundle.main.object(forInfoDictionaryKey: "CFBundleIdentifier")
    as? String ?? "ime.chaoset.inputmethod.ChaosetIME"
let server = IMKServer(name: connectionName, bundleIdentifier: bundleID)
ChaosetController.sharedServer = server

let app = NSApplication.shared
app.setActivationPolicy(.prohibited)
app.run()
