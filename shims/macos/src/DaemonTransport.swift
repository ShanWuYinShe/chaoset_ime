import Foundation
import os

// DaemonTransport.swift：chaosetd 的 Unix socket 传输层（连接/帧读写/重连/懒启动）。
//
// 从 EngineClient.swift 拆出（2026-11-06 架构拆分，纯移动、行为零改动）。
// 协议与 Go 侧 ipc 包一致：4 字节大端长度 + JSON 载荷（上限 1 MiB），
// 请求 {"id","method","params"}，响应 {"id","result","error"}。
// 对外 API（组词/学习数据/联想）与响应解析见 EngineClient.swift。

final class DaemonTransport {
    private var fd: Int32 = -1
    private var nextID: UInt64 = 0

    private var lastFailAt: TimeInterval = 0
    private let cooldown: TimeInterval = 1.0 // 连不上时的重试冷却，避免每次击键都白连
    // 懒启动 daemon 的节流时间戳与间隔。必须与 lastFailAt 的 1s 连接冷却分开：
    // daemon 缺失时每次击键都会连接失败，若复用同一时间戳就会每键 fork 一个
    // 进程。5s 足够让刚拉起的 daemon 加载完词库（实测约 100ms）并被下一次
    // 按键连上，又不会在 daemon 永久不可用时频繁产生进程。
    private var lastSpawnAt: TimeInterval = 0
    private let spawnCooldown: TimeInterval = 5.0
    private let socketPath: String

    // 壳侧 IPC 失败日志：macOS 输入法进程没有控制台，daemon 日志也看不到
    // 「壳为什么降级成英文」。用统一日志（Console.app / log show）记录，且
    // 2 秒内只记一条，避免 daemon 故障时每次击键都写日志影响输入延迟。
    private let logger = Logger(subsystem: "ime.chaoset.inputmethod", category: "ipc")
    private var lastFailureLogAt: TimeInterval = 0

    func logFailure(_ message: @autoclosure () -> String) {
        let now = Date().timeIntervalSince1970
        guard now - lastFailureLogAt >= 2 else { return }
        lastFailureLogAt = now
        let text = message()
        logger.error("\(text, privacy: .public)")
    }

    init(socketPath: String) {
        self.socketPath = socketPath
    }

    deinit {
        disconnect()
    }

    private func disconnect() {
        if fd >= 0 {
            Darwin.close(fd)
            fd = -1
        }
    }

    private func connectIfNeeded() -> Bool {
        if fd >= 0 { return true }
        let now = Date().timeIntervalSince1970
        if now - lastFailAt < cooldown { return false }
        if connectOnce(now: now) { return true }
        // 确实没连上：daemon 可能根本没在运行（LaunchAgent 缺失 / 被 bootout /
        // 崩溃后没人拉起）。补一次懒启动；本次按键仍按英文透传，下次按键在
        // 1s 冷却后重连，自然接上刚拉起的 daemon。
        spawnDaemonIfNeeded()
        return false
    }

    // connectOnce 执行一次真实的非阻塞连接尝试：成功时把 fd 置为已连接的
    // 套接字，失败时更新 lastFailAt 并记日志。任何路径都不等待 daemon 就绪。
    private func connectOnce(now: TimeInterval) -> Bool {
        let sock = socket(AF_UNIX, SOCK_STREAM, 0)
        guard sock >= 0 else {
            lastFailAt = now
            logFailure("socket(AF_UNIX) 创建失败 errno=\(errno)")
            return false
        }
        // daemon 在写入途中消失时不能触发 SIGPIPE：否则整个输入法进程会被信号杀死
        //（macOS 没有 MSG_NOSIGNAL，BSD 的做法是给套接字设 SO_NOSIGPIPE）。
        var nosigpipe: Int32 = 1
        // 必须检查返回值：设置失败时，daemon 若在我们写入途中消失，write 会触发
        // SIGPIPE 直接把整个输入法进程杀掉（默认动作），表现为「打着字输入法突然
        // 没了」。失败就放弃这次连接，下次按键再试（2026-10-26 审查发现）。
        if setsockopt(sock, SOL_SOCKET, SO_NOSIGPIPE, &nosigpipe,
                      socklen_t(MemoryLayout<Int32>.size)) != 0 {
            let err = errno
            Darwin.close(sock)
            lastFailAt = now
            logFailure("SO_NOSIGPIPE 设置失败 errno=\(err)")
            return false
        }

        // 收发超时：既要绝不无限阻塞输入法主线程（拖慢 WindowServer 与切换 HUD），
        // 又要给 daemon 留足正常处理时间。
        //
        // 2026-10-26 实测：daemon 单键处理 p50 1.5ms，但 GC 尖峰会让单次调用到
        // 50–90ms；原先的 100ms 超时与尖峰咬合，客户端偶发拿到 nil，表现为
        // 「切换后首字母丢失」。daemon 侧已把 GOGC 调到 400 削峰（p99 → 18ms），
        // 这里再放宽到 250ms 作为安全边界——正常路径远用不到，尖峰不至于误杀，
        // 而 250ms 仍是用户可感知卡顿的上限，不会出现「输入法卡死」的观感。
        var tv = timeval(tv_sec: 0, tv_usec: 250_000) // 250ms
        setsockopt(sock, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(sock, SOL_SOCKET, SO_SNDTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = Array(socketPath.utf8)
        guard pathBytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            Darwin.close(sock)
            lastFailAt = now
            logFailure("socket 路径过长（\(pathBytes.count) 字节）")
            return false
        }
        withUnsafeMutableBytes(of: &addr.sun_path) { dest in
            dest.copyBytes(from: pathBytes)
        }

        // 设置非阻塞模式，防止 connect 在主线程上挂起
        let flags = fcntl(sock, F_GETFL, 0)
        _ = fcntl(sock, F_SETFL, flags | O_NONBLOCK)

        let result = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                Darwin.connect(sock, sa, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        if result != 0 {
            let err = errno
            if err == EINPROGRESS {
                var pfd = pollfd(fd: sock, events: Int16(POLLOUT), revents: 0)
                let pollRes = poll(&pfd, 1, 20) // 20ms 超短超时
                if pollRes <= 0 || (pfd.revents & Int16(POLLOUT)) == 0 {
                    let pollErr = errno
                    Darwin.close(sock)
                    lastFailAt = now
                    logFailure("connect 超时/未就绪 poll=\(pollRes) errno=\(pollErr)")
                    return false
                }
                var sockErr: Int32 = 0
                var errLen = socklen_t(MemoryLayout<Int32>.size)
                if getsockopt(sock, SOL_SOCKET, SO_ERROR, &sockErr, &errLen) != 0 || sockErr != 0 {
                    let getErr = errno
                    Darwin.close(sock)
                    lastFailAt = now
                    logFailure("connect SO_ERROR 失败 errno=\(getErr) sockErr=\(sockErr)")
                    return false
                }
            } else {
                Darwin.close(sock)
                lastFailAt = now
                logFailure("connect 失败 errno=\(err) socket=\(socketPath)")
                return false
            }
        }
        // 连上后恢复常规模式（受上面设置的 SO_RCVTIMEO/SO_SNDTIMEO 250ms 保护）
        _ = fcntl(sock, F_SETFL, flags)
        fd = sock
        if lastFailureLogAt != 0 {
            logger.info("daemon 连接已恢复")
            lastFailureLogAt = 0
        }
        return true
    }

    // 懒启动 daemon：macOS 壳此前只依赖 LaunchAgent（ime.chaoset.daemon），
    // plist 缺失、被 launchctl bootout 或没被 launchd 加载时，输入法会永久
    // 降级成英文透传。Windows TSF 壳（win32.cpp 的 ensureDaemonStarted）与
    // Linux 引擎（daemon.cpp 的 Daemon::spawnDaemon）都会在发现 daemon 不可达
    // 时自行拉起；这里补上同一语义，做到三端行为对齐。
    //
    // 参数与 install.sh 写入 LaunchAgent 的 ProgramArguments 完全一致（同一份
    // 词库/用户词典/日志/套接字），不额外加旗标，保证两种启动方式读写同一份
    // 状态。启动后立即返回、绝不 waitUntilExit：输入法主线程不能为等 daemon
    // 加载词库而阻塞，本次按键继续英文透传，下次按键由 connectIfNeeded 重连；
    // daemon 之后若崩溃，也会由下一次按键的懒启动重新拉起。
    //
    // 与 LaunchAgent 共存时可能多起一个 daemon：若 launchd 已拉起实例，我们
    // 拉起的第二个实例会因 <socket>.lock 的 flock 单实例检测失败而立刻退出
    // （cmd/chaosetd/singleton_unix.go）。不会出现两个常驻 daemon，属正常且
    // 无害，不要当 bug 修。
    private func spawnDaemonIfNeeded() {
        let now = Date().timeIntervalSince1970
        guard now - lastSpawnAt >= spawnCooldown else { return }
        // 先记账再启动：即使 run() 抛错（chaosetd 缺失/无执行权限）也照样节流，
        // 避免 daemon 没部署时每个按键都尝试 fork 一次。
        lastSpawnAt = now
        guard let resourcePath = Bundle.main.resourcePath else {
            logFailure("懒启动 daemon 失败：Bundle.main.resourcePath 不可用")
            return
        }
        // 用户数据目录与 LaunchAgent 的 SUPPORT 同一口径（install.sh 里
        // SUPPORT="$HOME/Library/Application Support/chaoset"）：两种启动方式
        // 必须读写同一份词库/用户词典/日志，否则会各写一套。套接字不另算，
        // 复用 EngineClient 的 socketPath（支持 CHAOSET_SOCKET 覆盖）。
        let support = NSHomeDirectory() + "/Library/Application Support/chaoset"
        let daemon = resourcePath + "/chaosetd"
        let process = Process()
        process.executableURL = URL(fileURLWithPath: daemon)
        // 参数顺序与 LaunchAgent 的 ProgramArguments 保持一致。
        process.arguments = [
            "-socket", socketPath,
            "-dict", support + "/dict.tsv",
            "-userdict", support + "/userdict.json",
            "-log", support + "/chaosetd.log",
        ]
        do {
            try process.run()
            // OSLog 的 \(值, privacy: .public) 是 autoclosure，闭包内引用实例属性
            // 必须显式写 self.，否则 swiftc 报「requires explicit use of 'self'」。
            logger.info("chaosetd 懒启动已发起 path=\(daemon, privacy: .public) socket=\(self.socketPath, privacy: .public)")
        } catch {
            logFailure("懒启动 daemon 失败 path=\(daemon) error=\(error.localizedDescription)")
        }
    }

    private func writeAll(_ bytes: [UInt8]) -> Bool {
        var off = 0
        while off < bytes.count {
            let n = bytes.withUnsafeBufferPointer { buf in
                Darwin.send(fd, buf.baseAddress! + off, bytes.count - off, 0)
            }
            guard n > 0 else {
                logFailure("IPC send 失败 errno=\(errno) fd=\(fd)")
                return false
            }
            off += n
        }
        return true
    }

    private func readAll(_ count: Int) -> [UInt8]? {
        var out = [UInt8](repeating: 0, count: count)
        var off = 0
        while off < count {
            let n = out.withUnsafeMutableBufferPointer { buf in
                Darwin.recv(fd, buf.baseAddress! + off, count - off, 0)
            }
            guard n > 0 else {
                logFailure("IPC recv 失败/EOF errno=\(errno) fd=\(fd)")
                return nil
            }
            off += n
        }
        return out
    }

    func callOnce(method: String, params: [String: Any]) -> [String: Any]? {
        guard connectIfNeeded() else { return nil }
        nextID += 1
        let reqID = nextID
        var req: [String: Any] = ["id": reqID, "method": method]
        if !params.isEmpty { req["params"] = params }
        guard let payload = try? JSONSerialization.data(withJSONObject: req) else {
            logFailure("IPC 请求序列化失败 method=\(method)")
            return nil
        }

        let len = payload.count
        let header: [UInt8] = [
            UInt8((len >> 24) & 0xFF), UInt8((len >> 16) & 0xFF),
            UInt8((len >> 8) & 0xFF), UInt8(len & 0xFF),
        ]
        guard writeAll(header + [UInt8](payload)),
              let headBytes = readAll(4) else {
            logFailure("IPC 帧头收发失败 method=\(method)")
            disconnect()
            return nil
        }
        let size = Int(headBytes[0]) << 24 | Int(headBytes[1]) << 16 | Int(headBytes[2]) << 8 | Int(headBytes[3])
        guard size > 0, size <= (1 << 20), let body = readAll(size),
              let resp = try? JSONSerialization.jsonObject(with: Data(body)) as? [String: Any] else {
            logFailure("IPC 响应帧无效 method=\(method) size=\(size)")
            disconnect()
            return nil
        }
        // 响应 id 必须与本次请求一致。
        //
        // 协议规定请求/响应经 id 关联（见 Go 侧 ipc/protocol.go）。当前是实现
        // 全串行的一问一答，理论上不会错位；但一旦将来引入并发或连接复用，
        // 不校验就会把**别的请求的结果当成本次的**——在输入法里就是上屏错字。
        // 出问题即断开重连，让下一次按键走干净路径（2026-10-26 审查发现）。
        guard let respID = resp["id"] as? UInt64, respID == reqID else {
            logFailure("IPC 响应 id 不匹配 method=\(method) req=\(reqID)")
            disconnect()
            return nil
        }
        if let error = resp["error"] as? String {
            logFailure("daemon 业务错误 method=\(method): \(error)")
            return nil
        }
        return resp["result"] as? [String: Any] ?? [:]
    }

    /// 重试前清零连接冷却（EngineClient.call 的网络重试用；语义同旧 lastFailAt = 0）。
    func resetFailureCooldown() {
        lastFailAt = 0
    }
}
