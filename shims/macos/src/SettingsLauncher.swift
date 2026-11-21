// SettingsLauncher.swift：拉起 Slint 统一设置中心（chaoset-settings）。
//
// 三端（macOS/Windows/Linux）共用同一个 Slint 设置中心（shims/settings，产物
// chaoset-settings）：候选栏齿轮 → 本文件拉起独立进程。macOS 壳内不再有
// AppKit 设置窗，也**不保留**「找不到就打开旧窗口」的回退——找不到/启动失败
// 一律弹 NSAlert 明确报错。
//
// 查找顺序（前面的优先）：
//   1. app bundle 内随包副本：Contents/Resources/chaoset-settings
//      （发行包里的唯一路径，build.sh 必带）；
//   2. 开发态：app bundle 同级的 build/chaoset-settings
//      （bundle 在 shims/macos/build/ 下时即构建产物 build/chaoset-settings）；
//   3. 开发态：当前工作目录下的 build/chaoset-settings。
//
// 注意：设置中心的所有设置读写都经随包 CLI（chaosetctl，与本文件同目录的
// Contents/Resources/chaosetctl）完成，与 Windows/Linux 发行包同口径；壳只负责
// 拉起进程，不重复实现设置逻辑。

import AppKit
import os

enum SettingsLauncher {
    private static let logger = Logger(subsystem: "ime.chaoset.inputmethod", category: "settings")

    /// 统一设置中心可执行文件名（三端统一产物名，不带扩展名）。
    private static let binaryName = "chaoset-settings"

    /// 候选栏齿轮入口：打开统一设置中心；找不到/启动失败弹窗报错，不回退旧窗口。
    static func open() {
        guard let path = resolveBinary() else {
            logger.error("未找到统一设置中心 chaoset-settings")
            showAlert(
                title: "未找到统一设置中心（chaoset-settings）",
                detail: """
                候选栏齿轮只负责拉起统一设置中心，输入法壳内不再提供旧的设置窗口。

                已查找以下位置：
                • app 内：Contents/Resources/chaoset-settings
                • 构建目录：build/chaoset-settings（相对 app bundle 与当前目录）

                源码构建请先运行 make build-settings（产出 bin/chaoset-settings），\
                再运行 shims/macos/build.sh 重新打包。
                """)
            return
        }
        do {
            try launch(path: path)
            logger.info("已拉起统一设置中心 path=\(path, privacy: .public)")
        } catch {
            logger.error(
                "拉起统一设置中心失败 path=\(path, privacy: .public) error=\(error.localizedDescription, privacy: .public)")
            showAlert(
                title: "无法启动统一设置中心（chaoset-settings）",
                detail: """
                路径：\(path)
                原因：\(error.localizedDescription)
                """)
        }
    }

    /// 按「bundle 内 → bundle 同级 build/ → 当前目录 build/」顺序找可执行文件。
    private static func resolveBinary() -> String? {
        var candidates: [String] = []
        if let resources = Bundle.main.resourceURL {
            candidates.append(resources.appendingPathComponent(binaryName).path)
        }
        // 开发态：build.sh 把 app 放在 build/ 下，产物就在 bundle 同级。
        let bundleDir = Bundle.main.bundleURL.deletingLastPathComponent()
        candidates.append(bundleDir.appendingPathComponent(binaryName).path)
        let cwd = FileManager.default.currentDirectoryPath
        candidates.append((cwd as NSString).appendingPathComponent("build/" + binaryName))

        let fm = FileManager.default
        for path in candidates {
            var isDir: ObjCBool = false
            guard fm.fileExists(atPath: path, isDirectory: &isDir), !isDir.boolValue,
                  fm.isExecutableFile(atPath: path) else { continue }
            return path
        }
        return nil
    }

    /// 拉起独立进程（脱钩于输入法壳：壳退出/被系统回收不影响已打开的设置中心）。
    private static func launch(path: String) throws {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        try process.run()
    }

    /// 主线程弹窗（齿轮回调在 AppKit 主线程，这里只做兜底）。
    private static func showAlert(title: String, detail: String) {
        guard Thread.isMainThread else {
            DispatchQueue.main.async { showAlert(title: title, detail: detail) }
            return
        }
        // 输入法进程平时是 .prohibited（不占 Dock、不进 Cmd-Tab）；弹窗需要临时
        // 切到 .accessory 才能成为前台可见，弹完恢复后台输入法身份。
        NSApp.setActivationPolicy(.accessory)
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = title
        alert.informativeText = detail
        alert.addButton(withTitle: "好")
        alert.runModal()
        NSApp.setActivationPolicy(.prohibited)
    }
}
