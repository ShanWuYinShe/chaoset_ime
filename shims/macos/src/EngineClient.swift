import Foundation

// EngineClient 是 chaosetd 的最小 IPC 客户端。
// 协议与 Go 侧 ipc 包一致：4 字节大端长度 + JSON 载荷，
// 请求 {"id","method","params"}，响应 {"id","result","error"}。
// daemon 不可达时调用返回 nil，壳层据此降级为按键透传。
struct Candidate {
    let text: String
}

struct Comp {
    let input: String
    let preedit: String
    let candidates: [Candidate]
}

// CandidateImage 是 daemon 渲染的统一候选窗贴图（PNG）。
struct CandidateImage {
    let width: Int
    let height: Int
    let png: Data?  // nil = 无候选（壳应隐藏窗口）
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
        let result = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                Darwin.connect(sock, sa, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard result == 0 else {
            Darwin.close(sock)
            lastFailAt = now
            return false
        }
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
    private func call(method: String, params: [String: Any]) -> [String: Any]? {
        guard connectIfNeeded() else { return nil }
        nextID += 1
        var req: [String: Any] = ["id": nextID, "method": method]
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
                    list.append(Candidate(text: text))
                }
            }
        }
        return Comp(input: input, preedit: preedit, candidates: list)
    }

    func input(_ text: String) -> Comp? {
        parseComp(call(method: "input", params: ["text": text]))
    }

    func backspace() -> Comp? {
        parseComp(call(method: "backspace", params: [:]))
    }

    func reset() -> Comp? {
        parseComp(call(method: "reset", params: [:]))
    }

    func select(_ index: Int) -> String? {
        guard let result = call(method: "select", params: ["index": index]) else { return nil }
        return result["committed"] as? String
    }

    // candidateImage 取统一候选窗渲染图（v2）。png 为 nil 表示无候选；
    // 返回 nil 表示 daemon 不可达或未启用渲染（调用方回退 IMKCandidates）。
    func candidateImage() -> CandidateImage? {
        guard let result = call(method: "candidateImage", params: [:]) else { return nil }
        let width = result["width"] as? Int ?? 0
        let height = result["height"] as? Int ?? 0
        guard let b64 = result["png"] as? String else {
            return CandidateImage(width: width, height: height, png: nil)
        }
        guard let data = Data(base64Encoded: b64) else { return nil }
        return CandidateImage(width: width, height: height, png: data)
    }
}
