import AppKit
import InputMethodKit

// ChaosetController 是 IMK 输入法控制器：把系统按键事件翻译成
// 对 chaosetd 的 IPC 调用，并用内联预编辑 + 壳内原生候选窗（AppKit 自绘，
// 见 CandidatePanel.swift）展示；daemon 不可达时回退 IMKCandidates。
// daemon 未运行时所有按键原样透传，不影响宿主应用输入。
// @objc 显式注册 ObjC 类名：IMK 经 Info.plist 的
// InputMethodServerControllerClass 用 NSClassFromString 查找控制器，
// Swift 类不注册 ObjC 名会查不到。
@objc(ChaosetController)
final class ChaosetController: IMKInputController {
    // 2026-11-06 架构拆分：状态与跨文件使用的方法改为 internal，供 KeyRouter.swift
    // 的按键路由扩展访问；类型仍是进程内控制器，语义未改。
    // main.swift 装配 IMKServer 后挂到这里，候选窗构造需要它
    static weak var sharedServer: IMKServer?

    let engine = EngineClient()
    var current: Comp?
    // 渐进上屏的「已确认段」缓冲（2026-10-30 兼容性整改）。
    //
    // SelectPrefix 返回的已确认文本**不再立即 insertText 写入文档**，而是并入
    // 预编辑（marked text）一起展示，直到整条组词结束时一次 insertText 落盘。
    //
    // 为什么（用户实测：长串组词中选 1（今），部分 App 里「今」丢失、候选清空、
    // 剩余拼音卡死在文档里）：旧实现对宿主做「insertText 已确认段 + setMarkedText
    // 剩余拼音」两步文档变更。正确实现 IMK 语义的 App 会用 insertText 替换整个
    // marked 区，但不少 App（部分版本 Notes、Electron、终端等）对「marked 区
    // 激活期间收到 insertText」处理有缺陷：丢弃插入、或按过期的 marked 范围回写
    // 随后的 setMarkedText——结果正是「已选的今丢失 + 剩余拼音变成普通文本 +
    // 组词中断」。逐键输入之所以在所有 App 都正常，是因为它只做一步 setMarkedText。
    // 并入后，渐进上屏与逐键输入使用完全相同的原语（单步 setMarkedText），
    // 兼容面与逐键输入一致；落盘只保留「整条组词结束」一个时机——与 macOS
    // 系统拼音「合成内的已转换段」模型相同（选择候选后，已转换的字仍带下划线
    // 留在合成里，最后一起上屏）。
    var confirmedText = ""
    // 联想模式：选词上屏后候选窗继续展示续接词组（非 nil 即联想中）；
    // 用户开始输入新字母或结束会话时清空，回到正常拼音匹配。
    var associateCandidates: [Candidate]?
    // 联想来源音节：右键删除联想候选后按同一来源重新查询续接词组，
    // 保证删除刷新不改变联想链（不清空、不跳到其它词）。
    var associateSylls: [String] = []

    /// 联想当前页（0 起）。enterAssociate 归零，翻页键/面板点击切换。
    var associatePage = 0

    var candidateWindow: IMKCandidates?

    // 中英模式：英文模式下按键全部放行（组词/选词/标点映射不介入）。
    // 切换手势为 Shift **空按**：按下先装填，松开时若期间没有其它按键才切换。
    // 这样即便系统把 Shift 的按下与松开都路由进来也只切一次，Shift+A 之类的
    // 组合也不会误切换（旧实现按下/松开各切一次，净效果为不切换）。
    var enMode = false
    /// 全/半角标点开关：壳本地持有（2026-10-29 起），随 punct 请求下发
    var halfWidth = false
    var shiftArmed = false

    // MARK: - 事件声明（按键处理主流程见 KeyRouter.swift）

    /// 声明本输入控制器要接收的事件类型。
    ///
    /// IMK 只投递 recognizedEvents 里声明过的事件；默认实现只包含 keyDown，
    /// flagsChanged 不会被投递——Shift 空按切换中英因此永远收不到事件，
    /// 真机上表现为「Shift 切不了中英」（2026-11-02 用户反馈；合成 NSEvent
    /// 直驱 handle 的单测覆盖不到投递层，所以此前测试全绿但真机无效）。
    /// 参照 Rime/Squirrel：mask = keyDown + flagsChanged。
    override func recognizedEvents(_ sender: Any!) -> Int {
        return Int(NSEvent.EventTypeMask([.keyDown, .flagsChanged]).rawValue)
    }

    // 懒创建候选窗；IMKServer 未就绪时返回 nil，后续按键会再试。
    private func ensureCandidateWindow() -> IMKCandidates? {
        if candidateWindow == nil, let server = ChaosetController.sharedServer {
            candidateWindow = IMKCandidates(
                server: server,
                panelType: kIMKSingleColumnScrollingCandidatePanel,
                styleType: kIMKMain
            )
        }
        return candidateWindow
    }

    /// 客户端当前标记区的长度（0 = 没有标记区）。IMKTextInput 没有 hasMarkedText
    /// 成员（协议文档见 HIToolbox/IMKInputSession.h），markedRange 是唯一可用的
    /// 标记区探测：文档约定「没有标记区或客户端不支持 TSMDocumentAccess 时
    /// location 为 NSNotFound」；NSTextView 在清空后返回 length=0，故用 length 判定。
    private func clientMarkedLength(_ client: any IMKTextInput) -> Int {
        let range = client.markedRange()
        return range.location == NSNotFound ? 0 : range.length
    }

    /// 组词结束收尾：把客户端里残留的标记区（marked text）真正清掉。
    ///
    /// 2026-11-01 用户真机反馈（macOS，v0.3.5）：输入 jin 退格到只剩 j 时，第一次
    /// 退格看起来无效、要按第二次才删掉；且第一次之后那个 j **仍带下划线**（还是
    /// 输入法预编辑），说明壳已经把组词状态清空（current=nil）并调用过
    /// insertText("")，但该客户端**忽略了空字符串插入**，marked 文本原样留在文档里；
    /// 下一次退格 current 已为 nil → 走「无组词放行」分支，宿主自己把 marked 的 j
    /// 删掉，于是表现为要按两次。
    ///
    /// 为什么保留 insertText("")：多数客户端一次就能清掉标记区（最小 AppKit App
    /// 直接调 NSTextInputClient 的对照实验里，NSTextView 空插入会删字且
    /// markedRange 归零、不留空标记区），同时它是 2026-10-22 起验证过的 inline
    /// session 收尾原语；直接换成 setMarkedText("") 曾在部分客户端留下空标记区
    ///（「退格把已输入的词和拼音一起删了」）。因此这里按客户端真实状态分级兜底：
    /// 只对「忽略空插入」的客户端多走一步，正常路径行为完全不变。
    func clearMarkedText(_ client: (any IMKTextInput)?) {
        guard let client else { return }
        // ① 既有收尾：空插入。多数客户端（含 NSTextView）清掉标记区并结束会话。
        client.insertText("", replacementRange: NSRange(location: NSNotFound, length: 0))
        // ② 仍有标记区 → 该客户端忽略了空插入：用 setMarkedText("") 显式清空内容
        //   （标准「清空标记区」原语；直接对 NSTextView 的实验确认可删字且不留空标记区）。
        guard clientMarkedLength(client) > 0 else { return }
        client.setMarkedText(
            "",
            selectionRange: NSRange(location: 0, length: 0),
            replacementRange: NSRange(location: NSNotFound, length: 0)
        )
        // ③ 仍残留（极端客户端，连空 setMarkedText 也不认）：按真实 markedRange
        //   把标记区显式替换为空串，保证不留「看不见的预编辑」。
        let markedRange = client.markedRange()
        guard markedRange.location != NSNotFound, markedRange.length > 0 else { return }
        client.insertText("", replacementRange: markedRange)
    }

    /// 把已确认段一次性落盘并清空缓冲。调用时机 = 所有「不再继续组词」的
    /// 路径：整条组词选完、退格删空剩余拼音、失焦、透传、模式切换。
    /// 落盘必须用 insertText（替换整个 marked 区）——这是与整句上屏相同的、
    /// 已在全部 App 验证兼容的操作；绝不 marked 区激活期间分步写入文档。
    func flushConfirmed(_ client: (any IMKTextInput)?) {
        guard !confirmedText.isEmpty else { return }
        let text = confirmedText
        confirmedText = ""
        client?.insertText(text, replacementRange: NSRange(location: NSNotFound, length: 0))
    }

    // MARK: - 候选上屏与联想协调

    func commit(index: Int, client: (any IMKTextInput)?) {
        // 越界直接返回（调用方应已按候选数判定；这里是最后一道防线）。
        guard let cur = current, index >= 0, index < cur.candidates.count else { return }
        // 渐进上屏（2026-10-29）：SelectPrefix 只消耗候选音节对应的拼音
        // 前缀——打 jintian'wo'chi… 选「今天」后剩余 wo'chi… 继续组词
        // （2026-10-28 用户反馈「选一个候选就清空整个拼音」不合适，
        // 渐进拼句才是吸纳用户习惯的主途径）。
        if let comp = engine.selectPrefix(index: index), !comp.committedText.isEmpty {
            let assocSylls = cur.candidates[index].syllables
                .split(separator: "'").map(String.init)
            // 已确认段并入预编辑，**不立即写文档**（见 confirmedText 注释）：
            //   还剩拼音 → 与逐键输入同一原语，单步 setMarkedText 刷新；
            //   整串耗尽 → 整条组词一次 insertText 落盘（与整句上屏同一收尾，
            //   同为「替换 marked 区」这一全 App 兼容操作）。
            confirmedText += comp.committedText
            if comp.input.isEmpty {
                current = nil
                client?.insertText(
                    confirmedText,
                    replacementRange: NSRange(location: NSNotFound, length: 0)
                )
                confirmedText = ""
                refresh(client)
                // 剩余拼音耗尽 = 上屏收尾，与整句上屏同一收尾语义：进入联想。
                // 2026-10-29 三端一致性审查：Windows/Linux 壳均在剩余耗尽后
                // 进入联想。还剩拼音时继续组词，不进入联想。
                enterAssociate(sylls: assocSylls, client: client)
            } else {
                current = Comp(input: comp.input, preedit: comp.preedit,
                               candidates: comp.candidates,
                               page: comp.page, pageCount: comp.pageCount)
                refresh(client)
            }
            return
        }
        // daemon 不可达（selectPrefix 返回 nil）：绝不能静默失败（键被消费、
        // 文字没进文档 = 丢字）。退化为整句上屏，保证用户输入不丢失。
        // 已确认段随本次落盘一起收尾（缓冲必须清空，避免串进下一轮组词）。
        let committed = confirmedText + (engine.select(index) ?? cur.input)
        confirmedText = ""
        current = nil
        client?.insertText(
            committed,
            replacementRange: NSRange(location: NSNotFound, length: 0)
        )
        refresh(client)
        // 联想：选词上屏后候选窗继续展示续接词组（用户可直接数字键连选）
        enterAssociate(sylls: cur.candidates[index].syllables.split(separator: "'").map(String.init), client: client)
    }

    // MARK: - 候选窗右键删除学习记录

    /// 右键「删除该词」：按候选的「词 + 音节」删除 daemon 学习数据。
    ///
    /// 约束：
    ///   - 只删学习记录，不上屏、不改文档、不结束组词（右键事件走 NSView 的
    ///     上下文菜单通道，不进 mouseDown/onSelect 路径，面板也不抢焦点）；
    ///   - 写类 IPC 不重试（重试会把「已删除」变成「未找到」，写请求不应重放）；
    ///   - 删除成功后用当前剩余拼音重新组词刷新候选（daemon 不缓存候选列表）。
    private func deleteCandidate(index: Int, client: (any IMKTextInput)?) {
        guard let cur = current, index >= 0, index < cur.candidates.count else { return }
        let pick = cur.candidates[index]
        let sylls = pick.syllables.split(separator: "'").map(String.init)
        guard let deleted = engine.userDelete(word: pick.text, syllables: sylls) else {
            showToast("删除失败：daemon 不可达", client: client)
            return
        }
        guard deleted else {
            showToast("未找到该词的学习记录", client: client)
            return
        }
        recompose(client)
        showToast("已删除该词", client: client)
    }

    /// 删除学习记录后重新组词：daemon 不缓存候选列表，reset 后重放当前剩余
    /// 拼音即可拿到最新候选。已确认段 confirmedText 是壳侧状态，刷新不影响它。
    private func recompose(_ client: (any IMKTextInput)?) {
        guard let cur = current else {
            refresh(client)
            return
        }
        let raw = cur.input
        _ = engine.resetComposition()
        if raw.isEmpty {
            current = nil
        } else {
            current = engine.input(raw, halfWidth: halfWidth)
        }
        refresh(client)
    }

    /// 联想态右键删除：删除学习记录后按当前联想来源音节重新查询续接词组；
    /// 删空则收起联想面板（不把别的词硬塞给用户）。
    private func deleteAssociateCandidate(_ pick: Candidate, client: (any IMKTextInput)?) {
        let sylls = pick.syllables.split(separator: "'").map(String.init)
        guard let deleted = engine.userDelete(word: pick.text, syllables: sylls) else {
            showToast("删除失败：daemon 不可达", client: client)
            return
        }
        guard deleted else {
            showToast("未找到该词的学习记录", client: client)
            return
        }
        let source = associateSylls
        associateCandidates = nil
        associatePage = 0
        if !source.isEmpty, let comp = engine.associate(sylls: source), !comp.candidates.isEmpty {
            associateCandidates = comp.candidates
        }
        refresh(client)
        showToast("已删除该词", client: client)
    }

    // MARK: - 联想模式

    /// 联想每页条数（与引擎 pageSize 一致，数字键 1-6 指位）。
    static let associatePageSize = 6

    /// 进入联想模式：按上屏词音节查续接词组并展示（无候选则不进入）。
    func enterAssociate(sylls: [String], client: (any IMKTextInput)?) {
        guard AssociateSetting.enabled else { return }
        guard let comp = engine.associate(sylls: sylls), !comp.candidates.isEmpty else { return }
        associateCandidates = comp.candidates
        associateSylls = sylls
        associatePage = 0
        refresh(client)
    }

    func exitAssociate() {
        associateCandidates = nil
        associateSylls = []
        associatePage = 0
        Self.hideCandidatePanel()
    }

    /// 联想翻页（- / = 键或面板折叠区）。边界页保持当前页（与组词翻页的
    /// 钳制语义一致）。
    func associateFlip(next: Bool, client: (any IMKTextInput)?) {
        guard let list = associateCandidates else { return }
        let pageCount = (list.count + Self.associatePageSize - 1) / Self.associatePageSize
        if next, associatePage + 1 < pageCount {
            associatePage += 1
        } else if !next, associatePage > 0 {
            associatePage -= 1
        }
        refresh(client)
    }

    /// 展示联想候选（数据源为续接词组而非组词状态，本地分页）。
    private func showAssociatePanel(client: (any IMKTextInput)?, candidates: [Candidate]) -> Bool {
        let pageCount = max((candidates.count + Self.associatePageSize - 1) / Self.associatePageSize, 1)
        if associatePage >= pageCount { associatePage = pageCount - 1 }
        let start = associatePage * Self.associatePageSize
        let slice = Array(candidates[start..<min(start + Self.associatePageSize, candidates.count)])
        let items = slice.map { CandidatePanelView.Item(text: $0.text, fromUser: false) }
        let screen = Self.screenForCursor(client) ?? NSScreen.main
        CandidatePanelController.onSelect = { [weak self] index in
            // index 是**当前页切片**内的序号（视图按页渲染），查全局列表
            // 会错选其它页的同位词
            guard let self, index < slice.count, let client = self.client() else { return }
            let pick = slice[index]
            if let committed = self.engine.associateSelect(word: pick.text, sylls: pick.syllables.split(separator: "'").map(String.init)) {
                client.insertText(
                    committed,
                    replacementRange: NSRange(location: NSNotFound, length: 0)
                )
                self.enterAssociate(sylls: pick.syllables.split(separator: "'").map(String.init), client: client)
            }
        }
        // 折叠区点击翻页（与翻页键同一逻辑）
        CandidatePanelController.onPageDown = { [weak self] in
            self?.associateFlip(next: true, client: self?.client())
        }
        CandidatePanelController.onPageUp = { [weak self] in
            self?.associateFlip(next: false, client: self?.client())
        }
        CandidatePanelController.onSettings = { [weak self] in
            self?.showSettingsMenu()
        }
        // 右键「删除该词」：删联想候选的学习记录后按同一联想源音节重新查询
        // （只改学习数据，不上屏、不动文档、不结束联想；见 deleteAssociateCandidate）。
        CandidatePanelController.onDelete = { [weak self] index in
            guard let self, index < slice.count else { return }
            self.deleteAssociateCandidate(slice[index], client: self.client())
        }
        return CandidatePanelController.show(
            candidates: items,
            page: associatePage,
            pageCount: pageCount,
            originProvider: { size in
                Self.candidateOrigin(size: size, client: client, screen: screen)
            }
        )
    }

    // MARK: - 设置（候选栏齿轮入口，微信输入法交互参考）

    /// 齿轮点击 → 打开设置窗口（独立窗口含全部设置项；参考市面输入法，
    /// 入口不止一个勾选框而是完整设置面板）。
    private func showSettingsMenu() {
        SettingsWindowController.show()
    }

    // MARK: - 展示

    /// 在光标附近短暂显示模式提示（中英/全半角切换反馈，三端统一）；
    /// 光标矩形取不到时退回鼠标位置。
    func showToast(_ text: String, client: (any IMKTextInput)?) {
        let size = ModeToastView.size(for: text)
        let origin = Self.candidateOrigin(size: size, client: client, screen: Self.screenForCursor(client))
        ModeToastController.show(text: text, topLeft: origin)
    }

    func refresh(_ client: (any IMKTextInput)?) {
        // 组词开始即退出联想（新输入优先于联想展示）
        if current?.input.isEmpty == false {
            associateCandidates = nil
        }
        // 联想模式：候选窗展示续接词组
        if let assoc = associateCandidates, !assoc.isEmpty {
            if showAssociatePanel(client: client, candidates: assoc) {
                return
            }
        }
        // 预编辑 = 已确认段（渐进上屏的已选字）+ 剩余拼音：二者同属一条
        // marked 区，整条组词结束才落盘。壳对文档只做两类操作——
        // setMarkedText 更新（与逐键输入同原语）与 insertText 终态落盘
        // （与整句上屏同原语），不存在「组词中写正文」这一易碎操作。
        let marked = confirmedText + (current?.preedit ?? "")
        if marked.isEmpty {
            // 组词结束：收掉客户端里的标记区（in-place 预编辑）。
            clearMarkedText(client)
        } else {
            // selectionRange 用 (0,0) 而不是 (0,length)：整段全选会让部分客户端
            // 把后续输入当成「替换整个选区」，退格时连带删掉已上屏内容。
            client?.setMarkedText(
                marked,
                selectionRange: NSRange(location: (marked as NSString).length, length: 0),
                replacementRange: NSRange(location: NSNotFound, length: 0)
            )
        }
        let hasCandidates = !(current?.candidates.isEmpty ?? true)
        if !hasCandidates {
            exitAssociate()
            candidateWindow?.hide()
            return
        }
        // 原生候选窗优先（壳内 AppKit 自绘）；失败回退 IMKCandidates
        if showNativePanel(client: client) {
            candidateWindow?.hide()
            return
        }
        if let window = ensureCandidateWindow() {
            window.update()
        }
    }

    // ---- 原生候选窗面板（CandidatePanel.swift：壳内 AppKit 自绘） ----

    // 全进程共用一个候选窗面板：IMK 会为不同客户端/输入上下文创建多个控制器
    // 实例，若每个实例各持有一个 NSPanel，而会话结束时系统只发
    // commitComposition:（点击文本框外）而没有 deactivateServer:，旧面板就会
    // 永远留在屏幕上、点不掉也关不掉（2026-10-21 实测同时残留多个候选窗）。
    // 改为共用面板 + 在全部会话结束路径收起，保证任何时刻最多一个可见候选窗。
    // 面板单例与绘制在 CandidatePanel.swift，此处只做数据组装与定位。

    // 候选窗与光标行的间距（pt）：太贴会压住行下沿的下伸部，太远又像飘着。
    private static let candidateGap: CGFloat = 6

    private static func hideCandidatePanel() {
        CandidatePanelController.hide()
    }

    /// 显示壳内原生候选窗；返回 false 表示不可用（回退 IMKCandidates）。
    /// client 用于取插入点矩形，取不到时退回鼠标位置。
    private func showNativePanel(client: (any IMKTextInput)?) -> Bool {
        guard let cur = current, !cur.candidates.isEmpty else {
            exitAssociate()
            return false
        }
        let items = cur.candidates.map { CandidatePanelView.Item(text: $0.text, fromUser: $0.fromUser) }
        let screen = Self.screenForCursor(client) ?? NSScreen.main
        // 点击候选 → 选词上屏。weak self：IMK 每个输入上下文建/销 Controller，
        // 不得让静态面板的闭包延长已结束会话的实例生命周期。
        CandidatePanelController.onSelect = { [weak self] index in
            guard let self, let client = self.client() else { return }
            self.commit(index: index, client: client)
        }
        // 点击折叠区 → 翻下一页（与 = / ] 键等效；daemon 对边界页钳制）
        CandidatePanelController.onPageDown = { [weak self] in
            guard let self, let client = self.client() else { return }
            self.current = self.engine.nextPage()
            self.refresh(client)
        }
        // 末页折叠区箭头变 ⌃：点击翻上一页（与 - / [ 键等效）
        CandidatePanelController.onPageUp = { [weak self] in
            guard let self, let client = self.client() else { return }
            self.current = self.engine.prevPage()
            self.refresh(client)
        }
        // 候选栏齿轮 → 设置菜单（微信输入法交互参考）
        CandidatePanelController.onSettings = { [weak self] in
            self?.showSettingsMenu()
        }
        // 右键「删除该词」：按候选「词 + 音节」删学习记录后重新组词
        // （只改学习数据，不上屏、不动文档、不结束组词；见 deleteCandidate）。
        CandidatePanelController.onDelete = { [weak self] index in
            guard let self, let client = self.client() else { return }
            self.deleteCandidate(index: index, client: client)
        }
        return CandidatePanelController.show(
            candidates: items,
            page: cur.page,
            pageCount: cur.pageCount,
            originProvider: { size in
                Self.candidateOrigin(size: size, client: client, screen: screen)
            }
        )
    }

    // MARK: - 候选窗定位（跟随光标、不遮挡正在输入的行）

    /// 取插入点矩形（屏幕坐标）。IMK 的 attributesForCharacterIndex:lineHeightRectangle:
    /// 返回"宽 1px、高为行高"的矩形；index=0 表示取当前选区（无内联会话时）。
    /// 取不到或返回异常值时返回 nil（调用方退回鼠标位置）。
    private static func caretRect(client: (any IMKTextInput)?) -> NSRect? {
        guard let client else { return nil }
        var rect = NSRect.zero
        _ = client.attributes(forCharacterIndex: 0, lineHeightRectangle: &rect)
        // 全零 / 非有限值 / 尺寸不合理都视为"取不到"：某些客户端会返回零矩形，
        // 直接拿来定位会把候选窗丢到屏幕左下角。
        guard rect.origin.x.isFinite, rect.origin.y.isFinite,
              rect.size.width.isFinite, rect.size.height.isFinite,
              rect.size.height > 0, rect.size.height < 1000,
              !(rect.origin.x == 0 && rect.origin.y == 0) else {
            return nil
        }
        return rect
    }

    /// 候选窗左上角坐标：优先放光标行**下方**，下方空间不足放**上方**，
    /// 最后用屏幕可见区域钳制，保证整块候选窗都在屏内。
    private static func candidateOrigin(size: NSSize, client: (any IMKTextInput)?,
                                        screen: NSScreen?) -> NSPoint {
        let visible = (screen ?? NSScreen.main)?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        guard let caret = caretRect(client: client) else {
            // 兜底：光标矩形取不到时才退回鼠标位置（旧行为）
            let mouse = NSEvent.mouseLocation
            return clampTopLeft(NSPoint(x: mouse.x + 8, y: mouse.y - 4), size: size, in: visible)
        }
        // 下方：面板顶边在光标行底边之下 gap；上方：面板底边在光标行顶边之上 gap。
        let below = caret.minY - candidateGap            // 顶边 y（向下生长）
        let above = caret.maxY + candidateGap + size.height // 顶边 y（向上放时顶边更高）
        // 空间判断用「放得下整块」为判据：下方剩余 = caret.minY - visible.minY
        let spaceBelow = caret.minY - visible.minY
        let spaceAbove = visible.maxY - caret.maxY
        var top = below
        if spaceBelow < size.height + candidateGap && spaceAbove > spaceBelow {
            top = above
        }
        return clampTopLeft(NSPoint(x: caret.minX, y: top), size: size, in: visible)
    }

    /// 把面板左上角钳制到屏幕可见区域内（不超出左右与上下边界）。
    private static func clampTopLeft(_ topLeft: NSPoint, size: NSSize, in visible: NSRect) -> NSPoint {
        var x = topLeft.x
        var top = topLeft.y
        // 左边越界 → 贴左边；右边越界 → 右移（优先保持左侧对齐光标起点）
        if x + size.width > visible.maxX { x = visible.maxX - size.width }
        if x < visible.minX { x = visible.minX }
        // 顶边越界 → 下拉；底边越界 → 上推
        if top > visible.maxY { top = visible.maxY }
        if top - size.height < visible.minY { top = visible.minY + size.height }
        return NSPoint(x: x, y: top)
    }

    /// 光标所在屏幕：按插入点矩形中心点找屏幕，取不到用鼠标所在屏幕。
    private static func screenForCursor(_ client: (any IMKTextInput)?) -> NSScreen? {
        if let caret = caretRect(client: client),
           let hit = NSScreen.screens.first(where: { $0.frame.contains(NSPoint(x: caret.midX, y: caret.midY)) }) {
            return hit
        }
        let mouse = NSEvent.mouseLocation
        return NSScreen.screens.first(where: { $0.frame.contains(mouse) }) ?? NSScreen.main
    }

    // MARK: - IMKCandidates 数据源

    override func candidates(_ sender: Any!) -> [Any]! {
        current?.candidates.map(\.text) ?? []
    }

    override func candidateSelected(_ candidateString: NSAttributedString!) {
        let text = candidateString.string
        guard let list = current?.candidates,
              let idx = list.firstIndex(where: { $0.text == text }),
              let client = self.client() else { return }
        commit(index: idx, client: client)
    }

    // MARK: - 生命周期

    // 输入会话被提交（点击文本框外、切换应用/输入法时系统调用）：必须清空
    // 会话状态并收起候选窗，保证候选面板从屏幕彻底消失且不影响焦点。
    override func commitComposition(_ sender: Any!) {
        // 宿主要求结束本次输入会话（点击文本框外/切换应用）：已确认段先落盘
        //（用户确认过的字不能丢；旧实现里这些段早已在文档，净效果一致），
        // 剩余拼音丢弃。
        flushConfirmed(self.client())
        if current?.input.isEmpty == false {
            current = engine.reset()
        } else {
            current = nil
        }
        exitAssociate()
        Self.hideCandidatePanel()
        ModeToastController.hide()
        candidateWindow?.hide()
    }

    override func activateServer(_ sender: Any!) {
        // 新的输入上下文：先收掉上一个上下文可能残留的候选窗，并丢弃上一个
        // 上下文遗留的组词状态。若不清，切到本输入法后的首次按键会带着旧
        // 会话的 preedit 去和 daemon 组词，表现为「刚切换过来就打错字」。
        // 这里不调 daemon（reset 只请求 daemon 重置会话，代价一次 IPC）；
        // 直接丢弃本地状态即可——daemon 侧会话按连接维护，新连接本就是空的。
        // 已确认段缓冲同样不得带入：此时写入会插进**新的**输入上下文。
        current = nil
        confirmedText = ""
        exitAssociate()
        Self.hideCandidatePanel()
        ModeToastController.hide()
        candidateWindow?.hide()
    }

    override func deactivateServer(_ sender: Any!) {
        // 切换输入法或失去焦点时：已确认段先落盘（同一上下文内允许最后一次
        // 插入；旧实现里这些段早已在文档，净效果一致），剩余拼音丢弃。
        // 若无未决组词，坚决不发生任何阻塞性 IPC，保证 WindowServer 与系统
        // 输入法切换 HUD 丝滑无卡顿。
        flushConfirmed(self.client())
        if current?.input.isEmpty == false {
            current = engine.reset()
        } else {
            current = nil
        }
        exitAssociate()
        Self.hideCandidatePanel()
        ModeToastController.hide()
        candidateWindow?.hide()
    }

    deinit {
        // deinit 拿不到 client，无法补写已确认段；正常路径 deactivateServer /
        // commitComposition 已先行落盘，这里只兜底清理 UI。
        exitAssociate()
        Self.hideCandidatePanel()
        ModeToastController.hide()
        candidateWindow?.hide()
    }
}
