import AppKit
import InputMethodKit

// ChaosetControllerAsync.swift：macOS 壳的异步引擎管线接入层（issue #149）。
//
// 负责：乐观组词态（最后已应用 Comp + 未应用输入/退格回放）、操作入队、结果按
// seq 应用、失败兜底与失联宽限、以及测试用的 settle。按键路由在 KeyRouter.swift，
// 候选窗/会话生命周期在 ChaosetController.swift。

extension ChaosetController {

    // MARK: - 乐观组词态

    /// 最后一次已应用组词态 + 未应用输入/退格的回放。
    /// 只回放会改变预编辑的操作：翻页/选词/标点在结果回来前不改变预编辑。
    /// 按音节退格同样本地回放（与 Windows/Linux 一致，见 backspaceLocally）。
    func computeOptimistic() -> (disp: Comp?, base: String, text: String) {
        guard !pendingOps.isEmpty else {
            return (current, current?.input ?? "", current?.dpPending ?? "")
        }
        var disp = current ?? Comp(input: "", preedit: "", candidates: [], page: 0, pageCount: 1)
        var pending = disp.dpPending
        var unappliedPreedit = ""
        let shuangpin = !shuangpinScheme.isEmpty
        for op in pendingOps {
            switch op.kind {
            case .input:
                pending += op.text
                unappliedPreedit += op.text
            case .backspace:
                backspaceLocally(&disp, &pending, shuangpin: shuangpin, bySyllable: false)
                if !unappliedPreedit.isEmpty { unappliedPreedit.removeLast() }
            case .backspaceWord:
                backspaceLocally(&disp, &pending, shuangpin: shuangpin, bySyllable: true)
                if !unappliedPreedit.isEmpty { unappliedPreedit.removeLast() }
            default:
                break
            }
        }
        let base = disp.input
        let text = pending
        // 引擎可能不产生与原始输入逐字符一致的 preedit（会插音节分隔符）；
        // 未应用按键直接拼在已应用 input/preedit 末尾，仅用于乐观显示与非空判断。
        disp.input += unappliedPreedit
        disp.preedit += unappliedPreedit
        return (disp, base, text)
    }

    func optimisticComp() -> Comp? { computeOptimistic().disp }

    func hasDpPending() -> Bool {
        let (_, base, text) = computeOptimistic()
        return !text.isEmpty && base.isEmpty && optimisticInput().isEmpty
    }

    /// 本地回退一格（不依赖引擎，乐观预编辑回放用）。与 Windows
    /// tip_desync.cpp / Linux async_pipeline.cpp 的 backspaceLocally 同语义：
    /// pending 非空时退最近一键；否则退 comp.input 末尾——全拼逐字符
    ///（bySyllable 时按预编辑末段整段），双拼恒按末段（已解码音节）。
    /// comp.preedit 同步裁掉对应部分，仅用于乐观显示；结果回来仍以引擎为准。
    private func backspaceLocally(_ comp: inout Comp, _ pending: inout String,
                                  shuangpin: Bool, bySyllable: Bool) {
        if !pending.isEmpty {
            pending.removeLast()
            return
        }
        if comp.input.isEmpty { return }
        if !shuangpin && !bySyllable {
            let removedSeparator = comp.input.hasSuffix("'")
            comp.input.removeLast()
            if comp.preedit.isEmpty { return }
            comp.preedit.removeLast()
            if comp.input.isEmpty {
                comp.preedit = ""
                return
            }
            // 末段被删空：连同引擎补的分隔符一起去掉（用户自己敲的分隔符保留）。
            if !removedSeparator, comp.preedit.count >= 2, comp.preedit.hasSuffix("'"),
               !comp.input.hasSuffix("'") {
                comp.preedit.removeLast()
            }
            return
        }
        let sep = comp.preedit.lastIndex(of: "'")
        let seg = sep == nil ? comp.preedit : String(comp.preedit[comp.preedit.index(after: sep!)...])
        if seg.isEmpty { return }
        if comp.input.count >= seg.count { comp.input.removeLast(seg.count) }
        comp.preedit = sep == nil ? "" : String(comp.preedit[..<sep!])
    }

    func optimisticInput() -> String { optimisticComp()?.input ?? "" }

    /// 组词中 = 引擎已确认的输入串或仍在飞的按键（乐观）。
    func isComposingOptimistic() -> Bool { !(optimisticComp()?.input.isEmpty ?? true) }

    // MARK: - 入队

    func submitOp(_ op: EngineOp) {
        var op = op
        engineSeq += 1
        op.seq = engineSeq
        op.epoch = engineEpoch
        pendingOps.append(op)
        pipeline.submit(op) { [weak self] result in
            self?.handleEngineResult(result)
        }
    }

    func submitNonComposingOp(_ op: EngineOp) {
        var op = op
        engineSeq += 1
        op.seq = engineSeq
        op.epoch = engineEpoch
        pipeline.submit(op) { [weak self] result in
            self?.handleEngineResult(result)
        }
    }

    func submitInput(_ text: String) {
        var op = EngineOp(kind: .input)
        op.text = text
        op.halfWidth = halfWidth
        submitOp(op)
    }

    func submitBackspace() { submitOp(EngineOp(kind: .backspace)) }

    func submitBackspaceWord() { submitOp(EngineOp(kind: .backspaceWord)) }

    func submitPage(next: Bool) { submitOp(EngineOp(kind: next ? .nextPage : .prevPage)) }

    func submitPunct(_ key: String) {
        var op = EngineOp(kind: .punct)
        op.text = key
        op.halfWidth = halfWidth
        submitOp(op)
    }

    func submitRebuild(_ text: String, base: String = "") {
        var op = EngineOp(kind: .rebuild)
        op.base = base
        op.text = text
        op.halfWidth = halfWidth
        submitOp(op)
    }

    func submitSelectPrefix(index: Int, text: String, syllables: [String]) {
        var op = EngineOp(kind: .selectPrefix)
        op.index = index
        op.text = text
        op.syllables = syllables
        op.halfWidth = halfWidth
        submitOp(op)
    }

    func submitAssociate(sylls: [String]) {
        var op = EngineOp(kind: .associate)
        op.syllables = sylls
        submitNonComposingOp(op)
    }

    func submitAssociateSelect(_ pick: Candidate) {
        var op = EngineOp(kind: .associateSelect)
        op.text = pick.text
        op.syllables = pick.syllables.split(separator: "'").map(String.init)
        submitNonComposingOp(op)
    }

    func submitUserDelete(_ pick: Candidate, associate: Bool, aux: String) {
        var op = EngineOp(kind: .userDelete)
        op.text = pick.text
        op.syllables = pick.syllables.split(separator: "'").map(String.init)
        op.associate = associate
        op.aux = aux
        submitNonComposingOp(op)
    }

    // MARK: - 管线复位

    func startEngineLostTimer() {
        guard engineLostTimer == nil else { return }
        engineLostTimer = Timer.scheduledTimer(withTimeInterval: 0.1, repeats: true) { [weak self] _ in
            self?.onEngineLostTimerTick()
        }
    }

    func stopEngineLostTimer() {
        engineLostTimer?.invalidate()
        engineLostTimer = nil
    }

    private func onEngineLostTimerTick() {
        guard engineLost else {
            stopEngineLostTimer()
            return
        }
        let now = Date().timeIntervalSince1970
        if now - engineLostAt >= EnginePipeline.graceSeconds {
            giveUpEngineState()
            return
        }
        scheduleRebuild()
    }

    /// 失焦/取消/会话结束：纪元递增（旧结果丢弃）+ 清空乐观项 + 让引擎复位。
    /// keepLearning=false 用 reset（会话结束，连学习上下文一起丢）；true 用
    /// resetComposition（还会继续打字，保留 prev/history 的 bigram 加成）。
    func resetEnginePipeline(keepLearning: Bool) {
        engineEpoch += 1
        pendingOps.removeAll()
        current?.dpPending = ""
        engineLost = false
        engineLostAt = 0
        lastRebuildAt = 0
        stopEngineLostTimer()
        engineSeq += 1
        var op = EngineOp(kind: keepLearning ? .resetComposition : .reset)
        op.seq = engineSeq
        op.epoch = engineEpoch
        pipeline.submit(op) { [weak self] result in
            self?.handleEngineResult(result)
        }
    }

    /// 显式选词/标点已在本地兜底上屏后，引擎侧状态不可信：作废在飞结果，
    /// 用本地剩余输入整串重建（不含刚上屏的内容）。
    func restartEngineFromLocalState() {
        let (_, base, text) = computeOptimistic()
        current = nil
        confirmedText = ""
        resetEnginePipeline(keepLearning: true)
        if !base.isEmpty || !text.isEmpty { submitRebuild(text, base: base) }
    }

    static func consumePrefixFallback(input: String, syllables: [String]) -> String {
        guard !input.isEmpty, !syllables.isEmpty else { return "" }
        func eq(_ a: Character, _ b: Character) -> Bool {
            let ca = a.lowercased() == "v" ? "u" : a.lowercased()
            let cb = b.lowercased() == "v" ? "u" : b.lowercased()
            return ca == cb
        }
        let chars = Array(input)
        var pos = 0
        for syl in syllables {
            while pos < chars.count && chars[pos] == "'" { pos += 1 }
            let sc = Array(syl)
            guard pos < chars.count, let first = sc.first, eq(chars[pos], first) else { return "" }
            var j = 0
            while pos < chars.count && j < sc.count {
                if eq(chars[pos], sc[j]) {
                    pos += 1
                    j += 1
                } else if j > 0 && eq(chars[pos], "o") && eq(sc[j - 1], "i") && eq(sc[j], "u") {
                    pos += 1
                } else if j > 0 && eq(chars[pos], "e") && eq(sc[j - 1], "u") &&
                            (eq(sc[j], "i") || eq(sc[j], "n")) {
                    pos += 1
                } else {
                    break
                }
            }
        }
        while pos < chars.count && chars[pos] == "'" { pos += 1 }
        return pos >= chars.count ? "" : String(chars[pos...])
    }

    private func applyOptimisticOp(_ op: EngineOp, _ disp: inout Comp, _ pending: inout String,
                                   shuangpin: Bool) -> Bool {
        switch op.kind {
        case .input:
            pending += op.text
            return true
        case .backspace, .backspaceWord:
            backspaceLocally(&disp, &pending, shuangpin: shuangpin,
                             bySyllable: op.kind == .backspaceWord)
            return true
        default:
            return false
        }
    }

    private func splitForPunctFallback(_ op: EngineOp) -> (head: String, restText: String) {
        var dispBefore = current ?? Comp(input: "", preedit: "", candidates: [], page: 0, pageCount: 1)
        var pendingBefore = dispBefore.dpPending
        var modifiedBefore = false
        var restComp = Comp(input: "", preedit: "", candidates: [], page: 0, pageCount: 1)
        var restPending = ""
        let shuangpin = !shuangpinScheme.isEmpty
        for p in pendingOps {
            if p.seq < op.seq {
                if applyOptimisticOp(p, &dispBefore, &pendingBefore, shuangpin: shuangpin) {
                    modifiedBefore = true
                }
            } else if p.seq > op.seq {
                _ = applyOptimisticOp(p, &restComp, &restPending, shuangpin: shuangpin)
            }
        }
        let head = (!modifiedBefore && !dispBefore.candidates.isEmpty)
            ? (dispBefore.candidates[0].text + pendingBefore)
            : (dispBefore.input + pendingBefore)
        return (head, restPending)
    }

    private func remainderAfterSelectPrefix(_ op: EngineOp) -> (base: String, text: String) {
        var restComp = Comp(input: "", preedit: "", candidates: [], page: 0, pageCount: 1)
        var restPending = ""
        let shuangpin = !shuangpinScheme.isEmpty
        if let cur = current, !op.syllables.isEmpty {
            let rem = Self.consumePrefixFallback(input: cur.input, syllables: op.syllables)
            if !rem.isEmpty {
                restComp.input = rem
                restComp.preedit = rem
                restPending = cur.dpPending
            }
        }
        for p in pendingOps where p.seq != op.seq {
            _ = applyOptimisticOp(p, &restComp, &restPending, shuangpin: shuangpin)
        }
        return (restComp.input, restPending)
    }

    private func resetAndRebuildRemainder(base: String, text: String) {
        resetEnginePipeline(keepLearning: true)
        if !base.isEmpty || !text.isEmpty { submitRebuild(text, base: base) }
    }

    // MARK: - 结果应用

    private func handleEngineResult(_ r: EngineResult) {
        guard r.op.epoch == engineEpoch else { return }  // 过期（失焦/取消后的结果）
        if r.op.kind == .probe {
            if r.ok && engineLost { scheduleRebuild() }
            return
        }
        if !r.ok {
            handleEngineFailure(r)
            return
        }
        // 成功结果按 seq 顺序到达：seq <= 本结果的乐观项都已被引擎覆盖。
        while let first = pendingOps.first, first.seq <= r.op.seq {
            pendingOps.removeFirst()
        }
        engineLost = false
        stopEngineLostTimer()
        applyEngineSuccess(r)
    }

    private func applyEngineSuccess(_ r: EngineResult) {
        let client = lastClient
        // 双拼方案随每次 Comp 响应更新（全拼为空，必须跟着清掉）。
        if let comp = r.comp, r.op.kind != .userDelete { shuangpinScheme = comp.shuangpin }
        switch r.op.kind {
        case .input, .rebuild:
            if let comp = r.comp {
                if !r.committed.isEmpty {
                    // 双拼 ';'：引擎把「首选 + 分号」放进 committed
                    confirmedText += r.committed
                    current = nil
                    flushConfirmed(client)
                } else {
                    current = comp
                    if optimisticInput().isEmpty && !hasDpPending() {
                        flushConfirmed(client)
                        current = nil
                    }
                }
            }
            refresh(client)
        case .backspace, .backspaceWord:
            if let comp = r.comp {
                current = comp
                if optimisticInput().isEmpty && !hasDpPending() {
                    flushConfirmed(client)
                    current = nil
                }
            }
            refresh(client)
        case .selectPrefix:
            if r.committed.isEmpty {
                // 语义失败（越界/引擎未消费）：用壳侧已显示文本兜底，绝不丢选择
                if !r.op.text.isEmpty {
                    let (restBase, restText) = remainderAfterSelectPrefix(r.op)
                    current = nil
                    client?.insertText(confirmedText + r.op.text,
                                       replacementRange: NSRange(location: NSNotFound, length: 0))
                    confirmedText = ""
                    resetAndRebuildRemainder(base: restBase, text: restText)
                } else if optimisticInput().isEmpty && !hasDpPending() {
                    flushConfirmed(client)
                    current = nil
                } else if let comp = r.comp, !comp.input.isEmpty || !comp.dpPending.isEmpty {
                    current = comp
                }
                refresh(client)
                return
            }
            confirmedText += r.committed
            current = r.comp
            if let comp = r.comp, comp.input.isEmpty && comp.dpPending.isEmpty {
                current = nil
                flushConfirmed(client)
                if !r.op.syllables.isEmpty { enterAssociate(sylls: r.op.syllables, client: client) }
            }
            refresh(client)
        case .punct:
            current = nil
            client?.insertText(confirmedText + r.committed,
                               replacementRange: NSRange(location: NSNotFound, length: 0))
            confirmedText = ""
            refresh(client)
        case .associateSelect:
            let text = r.committed.isEmpty ? r.op.text : r.committed
            if !text.isEmpty {
                client?.insertText(text, replacementRange: NSRange(location: NSNotFound, length: 0))
            }
            exitAssociate()
            if !r.op.syllables.isEmpty { enterAssociate(sylls: r.op.syllables, client: client) }
            refresh(client)
        case .associate:
            if isComposingOptimistic() || hasDpPending() {
                refresh(client)
                return
            }
            if let comp = r.comp, !comp.candidates.isEmpty {
                associateCandidates = comp.candidates
                associateSylls = r.op.syllables
                associatePage = 0
            } else {
                exitAssociate()
            }
            refresh(client)
        case .userDelete:
            if !r.deleted {
                showToast("未找到该词的学习记录", client: client)
                return
            }
            if r.op.associate {
                associateCandidates = nil
                associatePage = 0
                if !r.op.aux.isEmpty { submitAssociate(sylls: r.op.aux.split(separator: "'").map(String.init)) }
                refresh(client)
            } else {
                recompose(client)
            }
        case .nextPage, .prevPage:
            if let comp = r.comp { current = comp }
            refresh(client)
        case .reset, .resetComposition:
            break
        case .probe:
            break
        }
    }

    private func handleEngineFailure(_ r: EngineResult) {
        let client = lastClient
        switch r.op.kind {
        case .selectPrefix:
            // 显式选词失败：用壳侧已显示文本兜底上屏，绝不静默丢键。
            let text = r.committed.isEmpty ? r.op.text : r.committed
            let (restBase, restText) = remainderAfterSelectPrefix(r.op)
            if !text.isEmpty {
                current = nil
                client?.insertText(confirmedText + text,
                                   replacementRange: NSRange(location: NSNotFound, length: 0))
                confirmedText = ""
                resetAndRebuildRemainder(base: restBase, text: restText)
            } else {
                pendingOps.removeAll { $0.seq == r.op.seq }
                markEngineLost()
            }
            refresh(client)
        case .associateSelect:
            if !r.op.text.isEmpty {
                client?.insertText(r.op.text, replacementRange: NSRange(location: NSNotFound, length: 0))
            }
            associateCandidates = nil
            associateSylls = []
            associatePage = 0
            restartEngineFromLocalState()
            refresh(client)
        case .punct:
            // daemon 不可达：先补全组词中前缀，再按原 ASCII 字符兜底上屏（绝不吞键）。
            let (head, restText) = splitForPunctFallback(r.op)
            if !head.isEmpty { confirmedText += head }
            if !r.op.text.isEmpty {
                current = nil
                client?.insertText(confirmedText + r.op.text,
                                   replacementRange: NSRange(location: NSNotFound, length: 0))
                confirmedText = ""
            }
            resetAndRebuildRemainder(base: "", text: restText)
            refresh(client)
        case .userDelete:
            showToast("删除失败：daemon 不可达", client: client)
        case .associate:
            break  // 失败不收起联想面板：保留上一次已显示的候选
        case .reset, .resetComposition, .probe:
            break
        case .input, .rebuild, .backspace, .backspaceWord, .nextPage, .prevPage:
            // 保留本地组词与预编辑，进入失联宽限（不弹出乐观项）。
            markEngineLost()
        }
    }

    // MARK: - 失联宽限

    func markEngineLost() {
        let now = Date().timeIntervalSince1970
        if !engineLost {
            engineLost = true
            engineLostAt = now
        }
        startEngineLostTimer()
        if now - engineLostAt >= EnginePipeline.graceSeconds {
            giveUpEngineState()
            return
        }
        scheduleRebuild()
    }

    private func scheduleRebuild() {
        guard engineLost else { return }
        let (_, base, text) = computeOptimistic()
        guard !base.isEmpty || !text.isEmpty else { return }
        let now = Date().timeIntervalSince1970
        if now - lastRebuildAt < EnginePipeline.rebuildInterval { return }
        lastRebuildAt = now
        submitRebuild(text, base: base)
    }

    /// 宽限期到：本地内容按原始串落盘（一个键都不丢），再清空管线。
    private func giveUpEngineState() {
        let client = lastClient
        let (_, base, text) = computeOptimistic()
        let raw = confirmedText + (!text.isEmpty ? (base + text) : optimisticInput())
        resetEnginePipeline(keepLearning: false)
        confirmedText = ""
        current = nil
        if !raw.isEmpty {
            client?.insertText(raw, replacementRange: NSRange(location: NSNotFound, length: 0))
        }
        refresh(client)
    }

    // MARK: - 连接探测

    func engineAvailable() -> Bool { pipeline.connected }

    func probeEngineIfDown() {
        guard !pipeline.connected else { return }
        let now = Date().timeIntervalSince1970
        if now - lastProbeAt < EnginePipeline.probeInterval { return }
        lastProbeAt = now
        engineSeq += 1
        var op = EngineOp(kind: .probe)
        op.seq = engineSeq
        op.epoch = engineEpoch
        pipeline.submit(op) { [weak self] result in
            self?.handleEngineResult(result)
        }
    }

    // MARK: - 测试

    /// 等待在飞请求全部回投并应用（仅测试使用；生产路径绝不阻塞主线程）。
    func settleEngineForTests(timeout: TimeInterval = 6.0) {
        let deadline = Date().addingTimeInterval(timeout)
        while pipeline.inFlight > 0 && Date() < deadline {
            RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.001))
        }
        // inFlight 在 completion 之后递减；再转一圈确保最后一次应用完成。
        RunLoop.main.run(until: Date(timeIntervalSinceNow: 0.01))
    }
}
