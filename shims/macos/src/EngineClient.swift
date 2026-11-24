import Foundation

// EngineClient 是 chaosetd 的最小 IPC 客户端。
// 协议与 Go 侧 ipc 包一致：4 字节大端长度 + JSON 载荷，
// 请求 {"id","method","params"}，响应 {"id","result","error"}。
// daemon 不可达时先尝试懒启动一次（见 spawnDaemonIfNeeded），仍不可达则调用
// 返回 nil，壳层据此降级为按键透传。
//
// 2026-11-06 架构拆分：socket 连接/帧读写/重连/懒启动移入 DaemonTransport.swift；
// 本文件只保留对外 API（组词/学习数据/联想）与响应解析。
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
    // 本次调用额外上屏、需要壳写入文档的文本（空 = 无）：
    // ① selectPrefix 渐进上屏的前缀段；② 双拼模式下按下的 ';' 标点键
    //（引擎把「组词中首选词 + 分号」放进 committed，壳不能自己把 ';' 当标点）。
    var committedText: String = ""
    // 双拼方案名（IPC "shuangpin"，空 = 全拼）。壳据此把 ';' 路由到 input：
    // mspy 的 ing 韵母键是 ';'，若一律当标点，明/听/星/京整类音节打不出。
    var shuangpin: String = ""
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

    /// socket 传输层：连接/帧读写/重连/懒启动（见 DaemonTransport.swift）。
    private let transport: DaemonTransport

    /// 最近一次 Composition 响应里的双拼方案名（空 = 全拼）。每次响应都会
    /// 覆盖——daemon 从双拼切回全拼时字段为空，缓存必须跟着清掉，否则 ';'
    /// 会被错误路由到 input。
    private(set) var doublePinyinScheme: String = ""

    init(socketPath: String = EngineClient.defaultSocketPath) {
        self.transport = DaemonTransport(socketPath: socketPath)
    }

    // call 发一次请求并返回 result 对象；任何失败返回 nil（读超时保留连接，其它失败断连）。
    //
    // 只有「连接已断」才重试一次：新连接是全新会话，重放不会重复追加。读超时
    // 说明 daemon 可能已经处理（只是响应迟到），盲目重试会让 input/backspace
    // 这类增量操作被应用两次；input 由调用方用 base 做幂等重放（见 input）。
    // 写类方法（select/associateSelect/punct 会改学习数据）不重试，避免同一次
    // 上屏被记录两遍。
    private func call(method: String, params: [String: Any], retryOnFailure: Bool = true) -> [String: Any]? {
        if let r = transport.callOnce(method: method, params: params) {
            return r
        }
        guard retryOnFailure else { return nil }
        // 读超时不能原样重发：daemon 可能已经处理了这次请求（只是响应迟到），
        // 重发会让 `input` 把同一段字符追加两次（预编辑 "aa"、上屏错字）。
        // 连接还在，交给调用方的 input(base:) 做幂等重放（不知道完整串才放弃本次）。
        if transport.lastFailureWasReadTimeout { return nil }
        // 连接已断：重连后重试一次。新连接是全新会话，重发 input 不会重复。
        // 清零冷却时间，保证重试不会被 1 秒冷却直接挡掉。
        transport.resetFailureCooldown()
        return transport.callOnce(method: method, params: params)
    }

    private func parseComp(_ result: [String: Any]?) -> Comp? {
        guard let result,
              let input = result["input"] as? String else {
            transport.logFailure("daemon composition 响应缺少 input 字段")
            return nil
        }
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
        // 顶层 committed：渐进上屏段与双拼 ';' 的上屏文本都放这里；
        // shuangpin：每次响应都带（全拼为空），壳据此路由 ';'。
        let committed = result["committed"] as? String ?? ""
        let shuangpin = result["shuangpin"] as? String ?? ""
        doublePinyinScheme = shuangpin
        return Comp(input: input, preedit: preedit, candidates: list, page: page,
                    pageCount: pageCount, committedText: committed, shuangpin: shuangpin)
    }

    /// 处理输入。halfWidth 是壳本地持有的全/半角标点状态，随每次 input 下发：
    /// 双拼 dpPending==0 的 ';' 会走引擎的 Punct 语义，宽度必须用本次壳状态，
    /// 否则刚切全/半角后单按 ';' 会按旧宽度出标点（1 键瞬态，2026-10-31 修复）。
    /// 追加输入（增量语义）。`base` 是壳侧已知的完整输入串：
    ///
    /// - **正常路径不带 base**：daemon 直接在当前会话上追加。带 base 会让 daemon 每次
    ///   按键都 `restore_input` 整串重建，双拼暂存的半键（`dp_pending`）随重建被清空，
    ///   两键成对解码永远无法完成——所以 base 只在下面两种情况下发送；
    /// - **读超时后重放必须传 base**：daemon 可能已经处理过上一次请求（只是响应迟到），
    ///   原样重发会把同一段字符追加两次（预编辑 "aa"、上屏错字）。带 base 时 daemon
    ///   先把引擎输入串整体置为 base 再追加 text，与逐键输入同构，重放幂等；
    /// - `rebuild: true` 时首次请求就带 base：`recompose` 的 `reset` 自身可能超时未
    ///   生效，必须显式「先置为 base 再输入」，否则会把整串追加两次。普通按键不要用。
    func input(_ text: String, halfWidth: Bool, base: String? = nil,
               rebuild: Bool = false) -> Comp? {
        var params: [String: Any] = ["text": text, "half": halfWidth]
        if rebuild, let base { params["base"] = base }
        if let r = transport.callOnce(method: "input", params: params) { return parseComp(r) }
        if transport.lastFailureWasReadTimeout {
            // 读超时：连接与会话都还在（不再断连），盲目重放会重复追加。
            // 用 base 做幂等重放；调用方没给 base（不知道完整串）时只能放弃本次。
            guard let base else { return nil }
            params["base"] = base
            transport.resetFailureCooldown()
            if let r = transport.callOnce(method: "input", params: params) {
                return parseComp(r)
            }
            return nil
        }
        // 连接已断：新连接是空会话，必须带 base 才能恢复已有组词（重放不会重复追加）。
        if let base { params["base"] = base }
        transport.resetFailureCooldown()
        if let r = transport.callOnce(method: "input", params: params) { return parseComp(r) }
        return nil
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

    /// 只丢弃当前组词、保留学习上下文（prev/history）。
    ///
    /// 「用户还会继续打字」的场景走这个：切中英、Esc 取消、全部删除、
    /// passThrough、重新组词。用 reset 会让引擎忘记刚上屏的字，紧接着的
    /// 候选失去 bigram 上下文加成（2026-11-05）。
    /// reset 只留给会话真正结束的场景（activateServer/deactivateServer）。
    func resetComposition() -> Comp? {
        parseComp(call(method: "resetComposition", params: [:]))
    }

    func commitRaw() -> String? {
        guard let result = call(method: "commitRaw", params: [:]) else { return nil }
        return result["committed"] as? String
    }

    // MARK: - 学习数据管理（设置窗口 GUI，2026-10-31）
    //
    // 普通用户不该为了「清空学习数据 / 导出学习数据」去用命令行；macOS 设置
    // 窗口直接经同一 IPC 通道管理（daemon 内存与磁盘一起生效，无覆盖竞态）。

    /// 学习数据统计（调频词 / 用户词组 / 上下文共现条数）。
    /// daemon 未启用用户词典或不可达时返回 nil。
    func userStats() -> (words: Int, phrases: Int, bigrams: Int)? {
        guard let r = call(method: "userStats", params: [:]) else { return nil }
        return (r["words"] as? Int ?? 0,
                r["phrases"] as? Int ?? 0,
                r["bigrams"] as? Int ?? 0)
    }

    /// 清空全部学习数据（daemon 内存 + 磁盘一次生效）。成功返回 true。
    func userClear() -> Bool {
        call(method: "userClear", params: [:]) != nil
    }

    /// 按「词 + 音节」删除一条学习记录（候选窗右键「删除该词」）。
    /// 返回值三态：nil = daemon 不可达/业务错误；false = 该词没有学习记录；
    /// true = 已删除（daemon 内存与磁盘一起生效）。
    /// **不重试**：写类操作，重试会把「已删除」变成「未找到」，且写请求不应重放。
    func userDelete(word: String, syllables: [String]) -> Bool? {
        // syllables 用 ' 连接，与登录候选 Candidate.syllables 的展示格式一致；
        // daemon 侧同时接受 ' 与空格分隔（见 ipc server 的 userDelete 分支）。
        guard let r = call(method: "userDelete",
                           params: ["word": word, "syllables": syllables.joined(separator: "'")],
                           retryOnFailure: false) else { return nil }
        return r["deleted"] as? Bool
    }

    /// 导出学习数据 TSV。daemon 单帧上限 1 MiB（2026-10-30 起分页），
    /// 这里按 offset/limit 循环拼接，重度用户的大数据也能完整导出。
    func userExportAll(phrasesOnly: Bool = false) -> String? {
        var out = ""
        var offset = 0
        let limit = 2000
        for _ in 0..<1000 { // 防御：最多 1000 页（200 万行）
            guard let r = call(method: "userExport",
                               params: ["phrases_only": phrasesOnly, "offset": offset, "limit": limit]) else {
                return nil
            }
            out += r["tsv"] as? String ?? ""
            guard let next = r["next"] as? Int, next >= 0 else { return out }
            offset = next
        }
        return out
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
        let shuangpin = result["shuangpin"] as? String ?? ""
        doublePinyinScheme = shuangpin
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
                    page: page, pageCount: pageCount, committedText: committed,
                    shuangpin: shuangpin)
    }

    func associateSelect(word: String, sylls: [String]) -> String? {
        guard let result = call(method: "associateSelect",
                                params: ["word": word, "sylls": sylls],
                                retryOnFailure: false) else { return nil }
        return result["committed"] as? String
    }
}
