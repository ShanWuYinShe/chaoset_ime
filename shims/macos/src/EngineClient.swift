import Foundation

// EngineClient 是 chaosetd 的最小 IPC 客户端。
// 协议与 Go 侧 ipc 包一致：4 字节大端长度 + JSON 载荷，
// 请求 {"id","method","params"}，响应 {"id","result","error"}。
// daemon 不可达时调用返回 nil，壳层据此降级为按键透传。
struct Candidate {
    let text: String
    let syllables: String  // ' 连接的音节序列（联想上屏回传 daemon 学习用）
    let fromUser: Bool  // from_user：用户学习标记（*），原生候选窗展示用
}

struct Comp {
    let input: String
    let preedit: String
    let candidates: [Candidate]
    let page: Int       // 0 起页码
    let pageCount: Int  // 总页数（page_count）
    // 渐进上屏（selectPrefix）返回时非空：本次实际上屏的前缀段文本
    var committedText: String = ""
}

final class EngineClient {
    // 套接字路径：优先环境变量 CHAOSET_SOCKET，否则用用户目录下的默认位置
    //（输入法进程没有可靠的工作目录，不能用相对路径）。
    static let defaultSocketPath: String = {
        if let env = ProcessInfo.processInfo.environment["CHAOSET_SOCKET"], !env.isEmpty {
            return env
        }
        return NSHomeDirectory() + "/Library/Application Support/chaoset/chaoset.sock"
    }()

    private var fd: Int32 = -1
    private var nextID: UInt64 = 0
    private var lastFailAt: TimeInterval = 0
    private let cooldown: TimeInterval = 1.0 // 连不上时的重试冷却，避免每次击键都白连
    private let socketPath: String

    init(socketPath: String = EngineClient.defaultSocketPath) {
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
        let sock = socket(AF_UNIX, SOCK_STREAM, 0)
        guard sock >= 0 else {
            lastFailAt = now
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
            Darwin.close(sock)
            lastFailAt = now
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
                    Darwin.close(sock)
                    lastFailAt = now
                    return false
                }
                var sockErr: Int32 = 0
                var errLen = socklen_t(MemoryLayout<Int32>.size)
                if getsockopt(sock, SOL_SOCKET, SO_ERROR, &sockErr, &errLen) != 0 || sockErr != 0 {
                    Darwin.close(sock)
                    lastFailAt = now
                    return false
                }
            } else {
                Darwin.close(sock)
                lastFailAt = now
                return false
            }
        }
        // 连上后恢复常规模式（受 SO_RCVTIMEO/SO_SNDTIMEO 100ms 保护）
        _ = fcntl(sock, F_SETFL, flags)
        fd = sock
        return true
    }

    private func writeAll(_ bytes: [UInt8]) -> Bool {
        var off = 0
        while off < bytes.count {
            let n = bytes.withUnsafeBufferPointer { buf in
                Darwin.send(fd, buf.baseAddress! + off, bytes.count - off, 0)
            }
            guard n > 0 else { return false }
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
            guard n > 0 else { return nil }
            off += n
        }
        return out
    }

    // call 发一次请求并返回 result 对象；任何失败返回 nil 并断开重连。
    //
    // 读类方法（input/backspace/nextPage 等）在失败且**尚未产生副作用**时重试一次：
    // daemon 偶发的 GC 停顿或瞬时忙会让单次调用超时，若直接放弃，调用方（壳）
    // 就会丢掉这一次按键——正是用户报的「首字母丢失」。这类方法重试是安全的，
    // 因为它们只读会话状态。写类方法（select/associateSelect/punct 会改学习数据）
    // 不重试，避免同一次上屏被记录两遍。
    private func call(method: String, params: [String: Any], retryOnFailure: Bool = true) -> [String: Any]? {
        if let r = callOnce(method: method, params: params) {
            return r
        }
        guard retryOnFailure else { return nil }
        // 重连后重试一次：上一次失败通常已 disconnect()，这里会重新建立连接。
        // 清零冷却时间，保证重试不会被 1 秒冷却直接挡掉。
        lastFailAt = 0
        return callOnce(method: method, params: params)
    }

    private func callOnce(method: String, params: [String: Any]) -> [String: Any]? {
        guard connectIfNeeded() else { return nil }
        nextID += 1
        let reqID = nextID
        var req: [String: Any] = ["id": reqID, "method": method]
        if !params.isEmpty { req["params"] = params }
        guard let payload = try? JSONSerialization.data(withJSONObject: req) else { return nil }

        let len = payload.count
        let header: [UInt8] = [
            UInt8((len >> 24) & 0xFF), UInt8((len >> 16) & 0xFF),
            UInt8((len >> 8) & 0xFF), UInt8(len & 0xFF),
        ]
        guard writeAll(header + [UInt8](payload)),
              let headBytes = readAll(4) else {
            disconnect()
            return nil
        }
        let size = Int(headBytes[0]) << 24 | Int(headBytes[1]) << 16 | Int(headBytes[2]) << 8 | Int(headBytes[3])
        guard size > 0, size <= (1 << 20), let body = readAll(size),
              let resp = try? JSONSerialization.jsonObject(with: Data(body)) as? [String: Any] else {
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
            disconnect()
            return nil
        }
        if resp["error"] != nil {
            return nil
        }
        return resp["result"] as? [String: Any] ?? [:]
    }

    private func parseComp(_ result: [String: Any]?) -> Comp? {
        guard let result,
              let input = result["input"] as? String else { return nil }
        let preedit = result["preedit"] as? String ?? ""
        var list: [Candidate] = []
        if let arr = result["candidates"] as? [[String: Any]] {
            for item in arr {
                if let text = item["text"] as? String {
                    list.append(Candidate(text: text,
                                          syllables: item["syllables"] as? String ?? "",
                                          fromUser: item["from_user"] as? Bool ?? false))
                }
            }
        }
        let page = result["page"] as? Int ?? 0
        let pageCount = result["page_count"] as? Int ?? 1
        return Comp(input: input, preedit: preedit, candidates: list, page: page, pageCount: pageCount)
    }

    func input(_ text: String) -> Comp? {
        parseComp(call(method: "input", params: ["text": text]))
    }

    func backspace() -> Comp? {
        parseComp(call(method: "backspace", params: [:]))
    }

    func backspaceWord() -> Comp? {
        parseComp(call(method: "backspaceWord", params: [:]))
    }

    func reset() -> Comp? {
        parseComp(call(method: "reset", params: [:]))
    }

    func commitRaw() -> String? {
        guard let result = call(method: "commitRaw", params: [:]) else { return nil }
        return result["committed"] as? String
    }

    /// 选词上屏。**不重试**：该调用会写学习数据（调频/bigram/词组进化），
    /// 重试会让同一次上屏被记录两遍。
    func select(_ index: Int) -> String? {
        guard let result = call(method: "select", params: ["index": index],
                                retryOnFailure: false) else { return nil }
        return result["committed"] as? String
    }

    // nextPage / prevPage 翻动候选页，返回当前页组成状态（边界由 daemon 钳制）。
    func nextPage() -> Comp? {
        parseComp(call(method: "nextPage", params: [:]))
    }

    func prevPage() -> Comp? {
        parseComp(call(method: "prevPage", params: [:]))
    }

    /// 标点键提交。**不重试**：组词中会先上屏首选（写学习数据），重试会重复记录。
    /// halfWidth 由壳本地持有、随请求下发（2026-10-29 起：全/半角开关不再
    /// 与 daemon 往返，daemon 只按状态产出标点文本）。返回 nil 表示 daemon
    /// 不可达（调用方放行原键）。
    func punct(_ key: String, halfWidth: Bool) -> String? {
        guard let result = call(method: "punct",
                                params: ["key": key, "half": halfWidth],
                                retryOnFailure: false) else { return nil }
        return result["committed"] as? String
    }

    // associate 取联想候选：上屏词后拼音区已清空，候选窗继续展示的续接词组。
    // sylls 为上屏词的音节序列（' 连接）。返回 nil 表示 daemon 不可达。
    func associate(sylls: [String]) -> Comp? {
        parseComp(call(method: "associate", params: ["sylls": sylls]))
    }

    // associateSelect 联想候选直接上屏：学习路径与 select 一致。
    /// 联想候选上屏。**不重试**：与 select 一样会写学习数据。
    /// 渐进上屏：选中候选后只消耗其音节对应的拼音前缀，剩余拼音回灌继续
    /// 组词。返回剩余组词态（Committed=已上屏前缀段文本，Preedit=剩余拼音，
    /// Candidates=剩余候选）。
    func selectPrefix(index: Int) -> Comp? {
        guard let result = call(method: "selectPrefix",
                                params: ["index": index]) else { return nil }
        let committed = result["committed"] as? String ?? ""
        let input = result["input"] as? String ?? ""
        let preedit = result["preedit"] as? String ?? ""
        let page = (result["page"] as? Int ?? 0)
        let pageCount = (result["page_count"] as? Int ?? 1)
        var list: [Candidate] = []
        if let arr = result["candidates"] as? [[String: Any]] {
            for item in arr {
                if let text = item["text"] as? String {
                    list.append(Candidate(text: text,
                                          syllables: item["syllables"] as? String ?? "",
                                          fromUser: item["from_user"] as? Bool ?? false))
                }
            }
        }
        return Comp(input: input, preedit: preedit, candidates: list,
                    page: page, pageCount: pageCount, committedText: committed)
    }

    func associateSelect(word: String, sylls: [String]) -> String? {
        guard let result = call(method: "associateSelect",
                                params: ["word": word, "sylls": sylls],
                                retryOnFailure: false) else { return nil }
        return result["committed"] as? String
    }
}
