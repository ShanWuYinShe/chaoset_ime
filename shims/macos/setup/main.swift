import AppKit
import Carbon
import Foundation
import UniformTypeIdentifiers

// ChaosetSetup —— Chaoset 输入法的图形化安装 / 词库导入工具。
// 目标：不用命令行的用户也能完成「安装/更新 → 导入词库 → 看状态 → 按提示注销重登」。
//
// 启动方式：
//   直接双击 = 图形界面
//   ChaosetSetup --selftest <supportDir> [词库文件...]  = 无界面自检（会话内验证用）
//
// 本 app 随包携带 ChaosetIME.app / chaosetd / chaoset-import / dict_common.tsv，
// 全部操作都在自身进程内完成，不依赖仓库路径与终端。

let imeTargetBundleName = "ChaosetIME.app"
let imeResourceBundleName = "ChaosetIME.embedded"
let sourceID = "ime.chaoset.inputmethod.ChaosetIME"
let modeID = "ime.chaoset.inputmethod.ChaosetIME.Pinyin"

func res(_ name: String) -> String { (Bundle.main.resourcePath ?? ".") + "/" + name }
func supportDir() -> String { NSHomeDirectory() + "/Library/Application Support/chaoset" }
func inputMethodsDir() -> String { NSHomeDirectory() + "/Library/Input Methods" }
func imeAppPath() -> String { inputMethodsDir() + "/" + imeTargetBundleName }
func socketPath() -> String { supportDir() + "/chaoset.sock" }
func stamp() -> String {
    let f = DateFormatter()
    f.dateFormat = "yyyyMMdd-HHmmss"
    return f.string(from: Date())
}

// ---- 子进程 ----
@discardableResult
func runTool(_ path: String, _ args: [String]) -> (status: Int32, output: String) {
    guard FileManager.default.isExecutableFile(atPath: path) else { return (-1, "缺少可执行文件: \(path)") }
    let p = Process()
    p.executableURL = URL(fileURLWithPath: path)
    p.arguments = args
    let pipe = Pipe()
    p.standardOutput = pipe
    p.standardError = pipe
    do { try p.run() } catch { return (-1, "启动失败: \(error)") }
    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    p.waitUntilExit()
    return (p.terminationStatus, String(data: data, encoding: .utf8) ?? "")
}

// ---- TIS：本输入法是否已被系统收录 ----
func allSources() -> [TISInputSource] {
    guard let raw = TISCreateInputSourceList(nil, true)?.takeRetainedValue() else { return [] }
    return (raw as NSArray) as? [TISInputSource] ?? []
}
func sourceProp(_ s: TISInputSource, _ k: CFString) -> String {
    guard let p = TISGetInputSourceProperty(s, k) else { return "" }
    return (Unmanaged<CFTypeRef>.fromOpaque(p).takeUnretainedValue() as? String) ?? ""
}
func sourceFlag(_ s: TISInputSource, _ k: CFString) -> Bool {
    guard let p = TISGetInputSourceProperty(s, k) else { return false }
    return (Unmanaged<CFTypeRef>.fromOpaque(p).takeUnretainedValue() as? Bool) ?? false
}
func findSource() -> TISInputSource? {
    allSources().first { sourceProp($0, kTISPropertyInputSourceID).hasPrefix(sourceID) }
}
func chaosetSourceCount() -> Int {
    allSources().filter { sourceProp($0, kTISPropertyInputSourceID).hasPrefix(sourceID) }.count
}

// ---- 系统 UI / TIS 缓存刷新（与 install.sh 同一套动作，保持 GUI 与 CLI 等价） ----
// TIS 输入源缓存是「每进程一份」的 IntlDataCache.le[.kbdx]。只重启进程不删缓存，
// 系统设置/输入菜单会继续显示旧图标或重复条目（旧版 GUI 安装缺这一步）。
func killInputSourceUI() {
    _ = runTool("/usr/bin/killall", ["System Settings"])
    for pat in ["KeyboardSettings", "com.apple.Keyboard-Settings", "TextInputSwitcher",
                "TextInputMenuAgent", "CursorUIViewService"] {
        _ = runTool("/usr/bin/pkill", ["-9", "-f", pat])
    }
}

func clearTISCache() {
    let fm = FileManager.default
    let cacheRoot = (NSTemporaryDirectory() as NSString).deletingLastPathComponent + "/C"
    guard let en = fm.enumerator(atPath: cacheRoot) else { return }
    for case let rel as String in en {
        let name = (rel as NSString).lastPathComponent
        guard name.hasPrefix("com.apple.IntlDataCache") else { continue }
        try? fm.removeItem(atPath: cacheRoot + "/" + rel)
    }
}

func disableLegacyModes() {
    let legacy = "ime.chaoset.inputmethod.ChaosetIME.Hans"
    for s in allSources() where sourceProp(s, kTISPropertyInputSourceID) == legacy {
        _ = TISDisableInputSource(s)
    }
}

// ---- 词库导入：种子 + 现用 + 用户所选 → 备份 → 原子替换 ----
func importDictionaries(files: [String], support: String, importer: String, seed: String,
                        restart: Bool = true) -> String {
    let fm = FileManager.default
    guard !files.isEmpty else { return "没有选择词库文件。" }
    for f in files where !fm.fileExists(atPath: f) { return "找不到文件：\(f)" }
    try? fm.createDirectory(atPath: support, withIntermediateDirectories: true)
    let dictPath = support + "/dict.tsv"
    let tmp = NSTemporaryDirectory() + "chaoset-import-\(UUID().uuidString)"
    try? fm.createDirectory(atPath: tmp, withIntermediateDirectories: true)
    defer { try? fm.removeItem(atPath: tmp) }

    var log = ""
    let base = tmp + "/base.tsv"
    if fm.fileExists(atPath: dictPath) {
        let r = runTool(importer, ["-o", base, seed, dictPath])
        log += r.output
    } else if fm.fileExists(atPath: seed) {
        try? fm.copyItem(atPath: seed, toPath: base)
    } else {
        return "随包缺少种子词库。"
    }
    let merged = tmp + "/merged.tsv"
    let r2 = runTool(importer, ["-o", merged, base] + files)
    log += r2.output
    guard fm.fileExists(atPath: merged) else { return "合并失败：\n" + log }

    if fm.fileExists(atPath: dictPath) {
        let bak = dictPath + ".bak." + stamp()
        try? fm.copyItem(atPath: dictPath, toPath: bak)
        log += "旧词库已备份：\(bak)\n"
    }
    let staging = dictPath + ".tmp"
    try? fm.removeItem(atPath: staging)
    do {
        try fm.copyItem(atPath: merged, toPath: staging)
        try? fm.removeItem(atPath: dictPath)
        try fm.moveItem(atPath: staging, toPath: dictPath)
    } catch { return "写入词库失败：\(error)" }

    let lines = (try? String(contentsOfFile: dictPath, encoding: .utf8))?.split(separator: "\n").count ?? 0
    log += "词库已更新：\(dictPath)（\(lines) 行）\n"
    if restart {
        log += restartDaemon(support: support)
        log += smokeCheck()
    }
    return log
}

// ---- daemon 部署与重启 ----
func restartDaemon(support: String) -> String {
    var out = ""
    let kill = runTool("/usr/bin/killall", ["chaosetd"])
    if kill.status == 0 {
        out += "已重启 daemon（LaunchAgent 会自动拉起）\n"
        Thread.sleep(forTimeInterval: 1.5)
        return out
    }
    let daemon = res("chaosetd")
    let dict = support + "/dict.tsv"
    guard FileManager.default.isExecutableFile(atPath: daemon) else { return "daemon 未运行，且随包缺少 chaosetd\n" }
    let p = Process()
    p.executableURL = URL(fileURLWithPath: daemon)
    p.arguments = ["-socket", support + "/chaoset.sock", "-dict", dict, "-userdict", support + "/userdict.json"]
    p.standardOutput = FileHandle.nullDevice
    p.standardError = FileHandle.nullDevice
    do { try p.run() } catch { return "daemon 启动失败：\(error)\n" }
    out += "daemon 原未运行，已用随包版本启动\n"
    Thread.sleep(forTimeInterval: 1.5)
    return out
}

// ---- 安装 / 更新输入法 ----
func installIME() -> String {
    let fm = FileManager.default
    var out = ""
    let src = res(imeResourceBundleName)
    guard fm.fileExists(atPath: src) else { return "随包缺少 \(imeResourceBundleName)，请重新构建。" }
    try? fm.createDirectory(atPath: inputMethodsDir(), withIntermediateDirectories: true)
    let dst = imeAppPath()
    if fm.fileExists(atPath: dst) {
        let bak = dst + ".bak." + stamp()
        try? fm.moveItem(atPath: dst, toPath: bak)
        if fm.fileExists(atPath: bak) { out += "旧版本已备份：\(bak)\n" }
    }
    do { try fm.copyItem(atPath: src, toPath: dst) } catch { return "装入失败：\(error)" }
    out += "已装入：\(dst)\n"

    // 替换 bundle 后先清掉各进程的 TIS 缓存并重启系统设置，避免旧图标/重复条目残留
    killInputSourceUI()
    clearTISCache()
    Thread.sleep(forTimeInterval: 1.0)

    let st = TISRegisterInputSource(URL(fileURLWithPath: dst) as CFURL)
    out += st == noErr ? "已向系统登记（TISRegisterInputSource）\n" : "登记失败（状态码 \(st)）\n"

    let support = supportDir()
    try? fm.createDirectory(atPath: support, withIntermediateDirectories: true)
    let daemonDst = support + "/chaosetd"
    if fm.fileExists(atPath: res("chaosetd")) {
        try? fm.removeItem(atPath: daemonDst + ".prev")
        if fm.fileExists(atPath: daemonDst) { try? fm.copyItem(atPath: daemonDst, toPath: daemonDst + ".prev") }
        try? fm.removeItem(atPath: daemonDst)
        try? fm.copyItem(atPath: res("chaosetd"), toPath: daemonDst)
        try? fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: daemonDst)
        out += "已部署 daemon\n"
    }
    if !fm.fileExists(atPath: support + "/dict.tsv"), fm.fileExists(atPath: res("dict_common.tsv")) {
        try? fm.copyItem(atPath: res("dict_common.tsv"), toPath: support + "/dict.tsv")
        out += "已写入种子词库（可用「导入词库…」换成自己的）\n"
    }
    out += restartDaemon(support: support)

    disableLegacyModes()
    if let s = findSource() {
        _ = TISEnableInputSource(s)
        _ = TISSelectInputSource(s)
        out += "系统已收录本输入法，已启用并选中。\n"
    } else {
        out += "\n注意：系统输入源表里还看不到本输入法。macOS 只在【登录时】扩充输入源表，\n"
        out += "请注销并重新登录，然后回到本工具点「重新检查」；届时到「系统设置 → 键盘 →\n"
        out += "输入法」添加「Chaoset」即可。\n"
    }
    // 收起系统设置/输入菜单进程，保证下次打开看到的是新图标与新列表，而不是旧快照
    killInputSourceUI()
    let n = chaosetSourceCount()
    out += "系统输入源表 Chaoset 条目：\(n) 条（预期 2：父容器 + Pinyin 模式）\n"
    if n > 2 {
        out += "⚠ 发现重复条目：请注销重登一次重建输入源表，或在本机终端运行 ./reregister.sh 清理。\n"
    }
    return out
}

// ---- IPC 六项自检：与 verify.sh 第 4 段同用随包 smoke（同一实现，保证等价） ----
func smokeCheck() -> String {
    let sock = socketPath()
    guard FileManager.default.fileExists(atPath: sock),
          FileManager.default.isExecutableFile(atPath: res("smoke")) else {
        return "IPC 自检跳过（缺 smoke 或套接字）\n"
    }
    let r = runTool(res("smoke"), [sock])
    let last = r.output.split(separator: "\n").last.map(String.init) ?? ""
    return "IPC 自检：\(last)\n"
}

// ---- 深度核验：与 shims/macos/verify.sh 四段一一对应 ----
// 约定：GUI 与 CLI 必须等价，本函数与 --verify 就是 GUI 侧的 verify.sh。
// 第 2 段特意用「内嵌 TIS API」与「随包 tis list」双重判定并互相对照，
// tests/equivalence.sh 会持续断言两者结论一致。
func verifyText() -> String {
    let fm = FileManager.default
    var fails = 0
    var out = ""

    // 1) 输入法装入（且 Info.plist 键齐全——与 verify.sh 的 plutil 检查等价）
    let app = imeAppPath()
    let keys = fm.fileExists(atPath: app)
        ? Bundle(url: URL(fileURLWithPath: app))?
            .object(forInfoDictionaryKey: "TISInputSourceID") as? String : nil
    if keys == sourceID {
        out += "PASS 1) 输入法已装入，Info.plist 输入源键齐全\n"
    } else {
        fails += 1
        out += "FAIL 1) 输入法未装入或 Info.plist 缺输入源键\n"
    }

    // 2) TIS 收录：内嵌 API 与随包 tis list 两条实现，结论必须一致
    let inline = findSource() != nil
    let cli = runTool(res("tis"), ["list"]).output.lowercased().contains(sourceID.lowercased())
    if inline != cli {
        fails += 1
        out += "FAIL 2) 两条 TIS 查询实现结论不一致（内嵌=\(inline)，tis list=\(cli)）——等价性破坏\n"
    } else if inline {
        out += "PASS 2) 系统输入源表已收录（两条实现结论一致）\n"
    } else {
        fails += 1
        out += "FAIL 2) 系统输入源表未收录 —— 需注销重登（两条实现结论一致）\n"
    }

    // 2b) 重复条目巡检：系统设置「添加输入法」里出现多条 Chaoset 的直接判据
    let n = chaosetSourceCount()
    if n == 2 {
        out += "PASS 2b) 无重复条目（父容器 + Pinyin 模式各 1 条）\n"
    } else {
        fails += 1
        out += "FAIL 2b) 系统表内 Chaoset 条目为 \(n) 条（预期 2；重复会让系统设置列表出现多条）\n"
    }

    // 3) daemon + IPC 六项
    if fm.fileExists(atPath: socketPath()) {
        let r = runTool(res("smoke"), [socketPath()])
        let last = r.output.split(separator: "\n").last.map(String.init) ?? ""
        if r.status == 0, last.contains("PASS") {
            out += "PASS 3) daemon 在运行，\(last)\n"
        } else {
            fails += 1
            out += "FAIL 3) daemon 套接字在但 IPC 自检失败：\(last)\n"
        }
    } else {
        fails += 1
        out += "FAIL 3) daemon 未运行\n"
    }

    // 4) 词库
    if let c = try? String(contentsOfFile: supportDir() + "/dict.tsv", encoding: .utf8) {
        out += "PASS 4) 当前词库 \(c.split(separator: "\n").count) 行\n"
    } else {
        fails += 1
        out += "FAIL 4) 尚未部署词库\n"
    }

    out += fails == 0 ? "结论：全部通过\n" : "结论：\(fails) 项未过\n"
    return out
}

// ---- 状态检查 ----
func statusText() -> String {
    let fm = FileManager.default
    var out = ""
    out += fm.fileExists(atPath: imeAppPath()) ? "✓ 输入法已装入\n" : "✗ 输入法尚未装入（点「安装 / 更新」）\n"
    if let s = findSource() {
        out += "✓ 系统已收录：\(sourceProp(s, kTISPropertyInputSourceID))\n"
        out += "    已启用：\(sourceFlag(s, kTISPropertyInputSourceIsEnabled) ? "是" : "否")；可选中：\(sourceFlag(s, kTISPropertyInputSourceIsSelectCapable) ? "是" : "否")\n"
    } else {
        out += "✗ 系统输入源表里还没有本输入法 —— 装入/更新后必须【注销并重新登录】\n"
    }
    out += fm.fileExists(atPath: socketPath()) ? "✓ 引擎服务（daemon）在运行\n" : "✗ 引擎服务未运行（点「安装 / 更新」会部署并启动）\n"
    if let c = try? String(contentsOfFile: supportDir() + "/dict.tsv", encoding: .utf8) {
        out += "✓ 当前词库：\(c.split(separator: "\n").count) 行\n"
    } else {
        out += "✗ 尚未部署词库\n"
    }
    return out
}

// ---- 图形界面 ----
final class AppDelegate: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    var statusField: NSTextField!
    var logView: NSTextView!
    var busy = false

    func applicationDidFinishLaunching(_ note: Notification) {
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 640, height: 520),
                         styleMask: [.titled, .closable, .miniaturizable, .resizable],
                         backing: .buffered, defer: false)
        w.title = "Chaoset"
        w.center()

        let title = NSTextField(labelWithString: "Chaoset · 设置与词库导入")
        title.font = NSFont.boldSystemFont(ofSize: 16)

        statusField = NSTextField(wrappingLabelWithString: "正在检查…")
        statusField.font = NSFont.systemFont(ofSize: 12)

        func button(_ text: String, _ sel: Selector) -> NSButton {
            let b = NSButton(title: text, target: self, action: sel)
            b.bezelStyle = .rounded
            b.setContentHuggingPriority(.defaultLow, for: .horizontal)
            return b
        }
        let row = NSStackView(views: [button("安装 / 更新输入法", #selector(doInstall)),
                                      button("导入词库…", #selector(doImport)),
                                      button("重新检查", #selector(doRefresh)),
                                      button("打开数据文件夹", #selector(doOpenFolder))])
        row.orientation = .horizontal
        row.spacing = 8
        row.distribution = .fillEqually

        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        logView = NSTextView()
        logView.isEditable = false
        logView.font = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
        logView.autoresizingMask = [.width]
        scroll.documentView = logView

        let stack = NSStackView(views: [title, statusField, row, scroll])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 10
        stack.edgeInsets = NSEdgeInsets(top: 16, left: 16, bottom: 16, right: 16)
        stack.translatesAutoresizingMaskIntoConstraints = false
        scroll.translatesAutoresizingMaskIntoConstraints = false
        w.contentView = NSView()
        w.contentView!.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: w.contentView!.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: w.contentView!.trailingAnchor),
            stack.topAnchor.constraint(equalTo: w.contentView!.topAnchor),
            stack.bottomAnchor.constraint(equalTo: w.contentView!.bottomAnchor),
            scroll.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -32),
            row.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -32),
            statusField.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -32),
            scroll.heightAnchor.constraint(greaterThanOrEqualToConstant: 240),
        ])
        window = w
        w.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        refresh()
    }

    func append(_ s: String) {
        logView.string += s
        logView.scrollToEndOfDocument(nil)
    }

    func refresh() {
        statusField.stringValue = statusText()
    }

    @objc func doRefresh() { refresh(); append("\n=== 状态已刷新 ===\n" + statusText()) }

    @objc func doInstall() {
        guard !busy else { return }
        busy = true
        append("\n=== 安装 / 更新输入法 ===\n")
        let r = installIME()
        append(r + "\n")
        refresh()
        busy = false
    }

    @objc func doImport() {
        guard !busy else { return }
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        panel.message = "选择要导入的词库：Rime dict.yaml / CC-CEDICT txt / TSV（可多选）"
        var types: [UTType] = [.plainText]
        if let y = UTType(filenameExtension: "yaml") { types.append(y) }
        if let t = UTType(filenameExtension: "tsv") { types.append(t) }
        panel.allowedContentTypes = types
        guard panel.runModal() == .OK else { return }
        busy = true
        append("\n=== 导入词库 ===\n")
        let r = importDictionaries(files: panel.urls.map { $0.path }, support: supportDir(),
                                   importer: res("chaoset-import"), seed: res("dict_common.tsv"))
        append(r + "\n")
        refresh()
        busy = false
    }

    @objc func doOpenFolder() {
        NSWorkspace.shared.open(URL(fileURLWithPath: supportDir()))
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { true }
}

// 无界面状态查询：ChaosetSetup --status
if CommandLine.arguments.count >= 2, CommandLine.arguments[1] == "--status" {
    print(statusText())
    exit(0)
}

// 无界面核验：ChaosetSetup --verify —— 与 verify.sh 四段一一对应；
// 全过退出 0，任何一段不过退出 1（可被 tests/equivalence.sh 与 CI 断言）。
if CommandLine.arguments.count >= 2, CommandLine.arguments[1] == "--verify" {
    let out = verifyText()
    print(out)
    exit(out.contains("FAIL") ? 1 : 0)
}

// 无界面自检：ChaosetSetup --selftest <supportDir> [词库文件...]
if CommandLine.arguments.count >= 3, CommandLine.arguments[1] == "--selftest" {
    let support = CommandLine.arguments[2]
    let files = Array(CommandLine.arguments.dropFirst(3))
    // 自检模式不碰用户的 daemon，只验证"合并 → 备份 → 原子替换"这条链路
    print(importDictionaries(files: files, support: support,
                             importer: res("chaoset-import"), seed: res("dict_common.tsv"),
                             restart: false))
    exit(0)
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()