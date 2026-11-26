import Foundation

// EnginePipeline：macOS 壳的异步引擎管线（issue #149）。
//
// IMK 的 handle/commitComposition/activateServer 等回调都在宿主 App 的主线程上
// 同步执行；旧实现直接在这条线程上等 daemon（每个请求都有阻塞截止），daemon 慢或
// 挂起就会卡住整个宿主。这里把引擎 I/O 全部移到每个控制器独占的串行队列上——
// 连接也独占：daemon 的组词会话按连接隔离，同进程多个控制器不能共用一条连接。
//
// 语义（与 Windows PR #153 同一设计）：
//  * IMK 侧只入队 + 用「最后已应用 Comp + 未应用输入/退格回放」做乐观预编辑；
//  * 操作 FIFO 执行，结果按 seq 顺序回主队列应用；失败结果不弹出乐观项；
//  * 失焦/取消递增 epoch，旧结果一律丢弃；daemon 不可用时保留本地组词，节流
//    整串重建（base 空 + 完整输入串），2s 宽限后按原始串落盘；
//  * 候选窗只按既有语义路径关闭（失焦、Esc/取消、上屏结束、模式切换），
//    daemon 慢/失败时继续显示最后一次已应用的候选。

enum EngineOpKind {
    case input           // text
    case rebuild         // text = 壳侧完整输入串（base 置空重建）
    case backspace
    case backspaceWord
    case nextPage
    case prevPage
    case selectPrefix    // index + text/syllables（失败兜底上屏文本）
    case punct           // text
    case associateSelect // text + syllables
    case associate       // syllables
    case userDelete      // text + syllables（associate = 联想态，aux = 重查来源）
    case reset
    case resetComposition
    case probe
}

struct EngineOp {
    var kind: EngineOpKind
    var seq: UInt64 = 0
    var epoch: UInt64 = 0
    var base: String = ""
    var text: String = ""
    var syllables: [String] = []
    var halfWidth: Bool = false
    var index: Int = 0
    var associate: Bool = false
    var aux: String = ""
}

struct EngineResult {
    var op: EngineOp
    var ok: Bool = false
    var connectionLost: Bool = false
    var deleted: Bool = false
    var committed: String = ""
    var comp: Comp?
}

final class EnginePipeline {
    /// daemon 一直不可用的宽限期：到点按壳侧原始串落盘（一个键都不丢）。
    static let graceSeconds: TimeInterval = 2.0
    /// 失联重建节流。
    static let rebuildInterval: TimeInterval = 0.2
    /// 失联探测节流。
    static let probeInterval: TimeInterval = 0.5

    /// 测试注入：为 true 时所有引擎请求直接按失败返回（不碰 IPC），用于验证
    /// 「失败不弹出乐观项、不关候选窗」的语义。生产路径永远为 false。
    static var failureInjectionForTests = false

    private let queue = DispatchQueue(label: "ime.chaoset.engine.pipeline", qos: .userInitiated)
    private let client: EngineClient
    private let lock = NSLock()
    private var connectedStorage = false
    private var inFlightStorage = 0
    // 仅在 serial queue 上读写：拦截「失联未重建 / 暗中重连空会话」上的有状态组词请求。
    private var sessionDesynced = false
    private var hadCompositionOps = false
    private var syncedConnGen: UInt64 = 0

    /// 连接状态是引擎线程维护的原子快照：IMK 线程只读，绝不等待。
    var connected: Bool {
        lock.lock(); defer { lock.unlock() }
        return connectedStorage
    }

    /// 在飞请求数（测试的 settle 用；IMK 线程只读）。
    var inFlight: Int {
        lock.lock(); defer { lock.unlock() }
        return inFlightStorage
    }

    init(socketPath: String = EngineClient.defaultSocketPath) {
        client = EngineClient(socketPath: socketPath)
        // 启动即预热一次连接（在引擎线程上）：首个内容键通常已处于已连接状态。
        queue.async { [weak self] in
            guard let self else { return }
            let ok = self.client.warmUp()
            self.setConnected(ok)
        }
    }

    /// 提交一个操作；completion 保证在主队列、按提交顺序回调。
    func submit(_ op: EngineOp, completion: @escaping (EngineResult) -> Void) {
        lock.lock(); inFlightStorage += 1; lock.unlock()
        queue.async { [weak self] in
            guard let self else { return }
            let result = EnginePipeline.failureInjectionForTests
                ? EngineResult(op: op, comp: nil)
                : self.run(op)
            self.setConnected(self.client.isConnected)
            DispatchQueue.main.async {
                // 先回调再递减：应用结果时可能继续入队（重建/联想），
                // inFlight 归零必须意味着「所有已提交请求都已应用完」。
                completion(result)
                self.lock.lock()
                self.inFlightStorage = max(0, self.inFlightStorage - 1)
                self.lock.unlock()
            }
        }
    }

    private func setConnected(_ ok: Bool) {
        lock.lock(); connectedStorage = ok; lock.unlock()
    }

    private func isSessionSynced() -> Bool {
        !sessionDesynced && (!hadCompositionOps || client.connGeneration == syncedConnGen)
    }

    private func run(_ op: EngineOp) -> EngineResult {
        var res = EngineResult(op: op, comp: nil)
        switch op.kind {
        case .reset, .resetComposition:
            sessionDesynced = false
            hadCompositionOps = false
            let comp = op.kind == .reset ? client.reset() : client.resetComposition()
            if let comp {
                syncedConnGen = client.connGeneration
                res.ok = true
                res.comp = comp
            } else {
                client.disconnect()
                res.connectionLost = client.lastFailureWasTransportError
            }
            return res
        case .probe:
            res.ok = client.isConnected || client.warmUp()
            return res
        case .rebuild:
            // base=已解码前缀, text=未解码新按键；reset 自身可能超时未生效，显式置为 base 再输入。
            sessionDesynced = false
            if let comp = client.input(op.text, halfWidth: op.halfWidth, base: op.base, rebuild: true) {
                syncedConnGen = client.connGeneration
                hadCompositionOps = comp.committedText.isEmpty && (!comp.input.isEmpty || !comp.dpPending.isEmpty)
                res.ok = true; res.committed = comp.committedText; res.comp = comp
            } else {
                sessionDesynced = true
                hadCompositionOps = true
                res.connectionLost = client.lastFailureWasTransportError
            }
            return res
        case .associate:
            if let comp = client.associate(sylls: op.syllables) {
                res.ok = true; res.comp = comp
            } else {
                res.connectionLost = client.lastFailureWasTransportError
            }
            return res
        case .associateSelect:
            if let committed = client.associateSelect(word: op.text, sylls: op.syllables) {
                res.ok = true; res.committed = committed
            } else {
                res.connectionLost = client.lastFailureWasTransportError
            }
            return res
        case .userDelete:
            if let deleted = client.userDelete(word: op.text, syllables: op.syllables) {
                res.ok = true; res.deleted = deleted
            } else {
                res.connectionLost = client.lastFailureWasTransportError
            }
            return res
        case .input, .backspace, .backspaceWord, .nextPage, .prevPage, .selectPrefix, .punct:
            break
        }

        if !isSessionSynced() {
            sessionDesynced = true
            res.connectionLost = true
            return res
        }

        switch op.kind {
        case .input:
            if let comp = client.input(op.text, halfWidth: op.halfWidth) {
                syncedConnGen = client.connGeneration
                hadCompositionOps = comp.committedText.isEmpty && (!comp.input.isEmpty || !comp.dpPending.isEmpty)
                res.ok = true; res.committed = comp.committedText; res.comp = comp
            } else {
                res.connectionLost = client.lastFailureWasTransportError
                sessionDesynced = true
            }
        case .backspace:
            if let comp = client.backspace() {
                syncedConnGen = client.connGeneration
                hadCompositionOps = !comp.input.isEmpty || !comp.dpPending.isEmpty
                res.ok = true; res.comp = comp
            } else {
                res.connectionLost = client.lastFailureWasTransportError
                sessionDesynced = true
            }
        case .backspaceWord:
            if let comp = client.backspaceWord() {
                syncedConnGen = client.connGeneration
                hadCompositionOps = !comp.input.isEmpty || !comp.dpPending.isEmpty
                res.ok = true; res.comp = comp
            } else {
                res.connectionLost = client.lastFailureWasTransportError
                sessionDesynced = true
            }
        case .nextPage:
            if let comp = client.nextPage() {
                res.ok = true; res.comp = comp
            } else {
                res.connectionLost = client.lastFailureWasTransportError
                sessionDesynced = true
            }
        case .prevPage:
            if let comp = client.prevPage() {
                res.ok = true; res.comp = comp
            } else {
                res.connectionLost = client.lastFailureWasTransportError
                sessionDesynced = true
            }
        case .selectPrefix:
            if let comp = client.selectPrefix(index: op.index) {
                syncedConnGen = client.connGeneration
                hadCompositionOps = comp.committedText.isEmpty || !comp.input.isEmpty || !comp.dpPending.isEmpty
                res.ok = true; res.committed = comp.committedText; res.comp = comp
            } else {
                res.connectionLost = client.lastFailureWasTransportError
                sessionDesynced = true
            }
        case .punct:
            if let committed = client.punct(op.text, halfWidth: op.halfWidth) {
                syncedConnGen = client.connGeneration
                hadCompositionOps = false
                res.ok = true; res.committed = committed
            } else {
                res.connectionLost = client.lastFailureWasTransportError
                sessionDesynced = true
            }
        case .rebuild, .associateSelect, .associate, .userDelete, .reset, .resetComposition, .probe:
            break
        }
        return res
    }
}
