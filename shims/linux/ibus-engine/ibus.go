package main

// IBus DBus 线缆胶水。接口与方法签名逐项对照官方 libibus
// （src/ibusengine.c 的 introspection XML）：
//   ProcessKeyEvent(u keyval, u keycode, u state) -> b
//   信号 CommitText(v text) / UpdatePreeditText(v text, u cursor, b visible, u mode)
// 其中 v 是 IBusSerializable 序列化变体：元组 (类型名 s, 附件 a{sv}, 内容 s)，
// 对 IBusText 即 ("IBusText", {}, "你好")，与 ibus_text_serialize 逐字段对应。

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	godbus "github.com/godbus/dbus/v5"

	"chaoset.ime/engine"
)

const (
	ibusBusName    = "org.freedesktop.IBus.Chaoset"
	factoryPath    = "/org/freedesktop/IBus/Factory"
	factoryIface   = "org.freedesktop.IBus.Factory"
	enginePathBase = "/org/freedesktop/IBus/Engine/chaoset"
	engineIface    = "org.freedesktop.IBus.Engine"

	// ibus keysym（与 X keysym 一致）
	keysymBackspace = 0xFF08
	keysymReturn    = 0xFF0D
	keysymEscape    = 0xFF1B
	keysymSpace     = 0x20
	keysymPageUp    = 0xFF55
	keysymPageDown  = 0xFF56
	keysymKP1       = 0xFFB1 // 小键盘 1..9 连续（KP_1..KP_9）
	keysymKP9       = 0xFFB9

	// ibus 修饰键掩码（IBUS_MODIFIER_MASK 中的常用位）
	ibusReleaseMask = 1 << 30
	ibusCtrlMask    = 1 << 2
	ibusMod1Mask    = 1 << 3
	ibusShiftMask   = 1 << 0
)

// ibusText 对应 libibus 的 IBusText 线缆序列化元组 (s, a{sv}, s)。
type ibusText struct {
	TypeName    string
	Attachments map[string]godbus.Variant
	Text        string
}

func newIBusText(text string) ibusText {
	return ibusText{TypeName: "IBusText", Attachments: map[string]godbus.Variant{}, Text: text}
}

// ibusEngine 是导出给 ibus-daemon 的单个引擎实例：每个 InputContext
// 经工厂 CreateEngine 获得独立实例（独立组词会话/中英模式/Shift
// 跟踪），多应用同时打字互不干扰。UserDict 与词典为全实例共享
// （并发安全见 engine.UserDict 的锁）。
// emitter 是 ibusEngine 对 dbus 连接的最小依赖（只用到 Emit 一个方法）。
// 抽成接口是为了测试：*godbus.Conn 天然满足它，而测试可以用 fake 记录信号
// 调用序列，从而覆盖「面板更新/上屏/竞态」这些此前 0% 覆盖、只能靠人工
// 在容器里肉眼确认的行为（2026-10-26 审查实测覆盖率仅 6.5%）。
type emitter interface {
	// 与 (*godbus.Conn).Emit 同签名（返回 error）。
	Emit(path godbus.ObjectPath, name string, values ...interface{}) error
}

type ibusEngine struct {
	conn emitter
	dict *engine.Dictionary
	user *engine.UserDict
	path godbus.ObjectPath // 本实例的对象路径（信号发射目标）

	mu     sync.Mutex
	sess   *engine.Session
	enMode bool // 英文模式：按键全部放行，组词/标点映射不介入
	shift  shiftTracker
}

// shiftTracker 识别「Shift 空按」手势——按下 Shift 后未按任何其他键
// 即释放。这是中文输入法惯用的中英切换方式（连按 Shift+字母不会误触发：
// 其他任何按键事件都会取消本次空按窗口）。纯逻辑，可单测。
type shiftTracker struct {
	shiftDown  int  // 仍处于按下状态的 Shift 键数（左右两颗）
	sinceShift bool // Shift 全部按下期间出现过其他按键事件
}

const (
	keysymShiftLeft  = 0xFFE1
	keysymShiftRight = 0xFFE2
)

func isShiftKeyval(keyval uint32) bool {
	return keyval == keysymShiftLeft || keyval == keysymShiftRight
}

// reset 丢弃当前的空按跟踪状态。
// 用于焦点切换：上一个窗口按了一半的 Shift 不应带到新窗口里被判成空按。
func (t *shiftTracker) reset() {
	t.shiftDown = 0
	t.sinceShift = false
}

// feed 处理一次按键事件（含按下与释放），返回该事件是否为
// 「Shift 空按释放」——即应触发中英切换。两颗 Shift 同时按住时，
// 松开其中一颗不触发，全部松开才判定。
func (t *shiftTracker) feed(keyval, state uint32) bool {
	if isShiftKeyval(keyval) {
		if state&ibusReleaseMask != 0 {
			if t.shiftDown == 0 {
				return false // 无按下的孤立释放（或重复释放）：不判定
			}
			t.shiftDown--
			toggle := t.shiftDown == 0 && !t.sinceShift
			if t.shiftDown == 0 {
				t.sinceShift = false
			}
			return toggle
		}
		t.shiftDown++
		t.sinceShift = false
		return false
	}
	// 其他任何按键事件（按下或释放，含 Ctrl/Alt）都关闭空按窗口
	t.sinceShift = true
	return false
}

// ibusFactory 是导出给 ibus-daemon 的工厂：CreateEngine 每次调用
// 创建并导出一个独立的引擎实例（独立对象路径与组词会话）。
type ibusFactory struct {
	conn  *godbus.Conn
	dict  *engine.Dictionary
	user  *engine.UserDict
	fuzzy bool
	trad  bool
	sp    string

	mu      sync.Mutex
	seq     uint32 // 已创建的引擎实例计数（对象路径编号）
	engines map[godbus.ObjectPath]*ibusEngine
}

func newIBusFactory(dict *engine.Dictionary, user *engine.UserDict, fuzzy, trad bool, sp string) (*ibusFactory, error) {
	conn, err := godbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("连接 session bus 失败: %w", err)
	}
	return &ibusFactory{
		conn:    conn,
		dict:    dict,
		user:    user,
		fuzzy:   fuzzy,
		trad:    trad,
		sp:      sp,
		engines: make(map[godbus.ObjectPath]*ibusEngine),
	}, nil
}

func (f *ibusFactory) run() error {
	// 工厂对象：ibus-daemon 激活组件时调用 CreateEngine
	if err := f.conn.Export(f, factoryPath, factoryIface); err != nil {
		return fmt.Errorf("导出工厂对象失败: %w", err)
	}
	// 不带 AllowReplacement/ReplaceExisting：另一个 chaoset-engine 进程若已
	// 持有总线名，这里必须失败退出，而不是「抢到名字后旧进程变成收不到任何
	// 调用的幽灵进程」（2026-10-26 审查发现：旧实现用 AllowReplacement 且
	// 只打印 reply 不校验结果）。
	reply, err := f.conn.RequestName(ibusBusName, 0)
	if err != nil {
		return fmt.Errorf("请求总线名失败: %w", err)
	}
	if reply != godbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("总线名 %s 已被其他进程持有（可能已有 chaoset-engine 在运行），本实例退出", ibusBusName)
	}
	log.Printf("chaoset ibus 引擎就绪（bus=%s）", ibusBusName)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		f.conn.Close()
		os.Exit(0)
	}()

	// 阻塞等待（导出对象的回调由 godbus 连接 goroutine 分发）
	select {}
}

// ---- org.freedesktop.IBus.Factory ----

// CreateEngine 由 ibus-daemon 在激活每个 InputContext 时调用：
// 创建独立引擎实例（独立会话/中英模式/Shift 跟踪）并导出到唯一
// 对象路径 /org/freedesktop/IBus/Engine/chaoset/<seq>。
func (f *ibusFactory) CreateEngine(name string) (godbus.ObjectPath, *godbus.Error) {
	if name != "chaoset" {
		return "", godbus.MakeFailedError(fmt.Errorf("未知引擎 %q", name))
	}
	f.mu.Lock()
	f.seq++
	seq := f.seq
	f.mu.Unlock()

	path := godbus.ObjectPath(fmt.Sprintf("%s/%d", enginePathBase, seq))
	eng := &ibusEngine{
		conn: f.conn,
		dict: f.dict,
		user: f.user,
		path: path,
		sess: engine.NewSession(f.dict, f.user),
	}
	if f.fuzzy {
		eng.sess.SetFuzzy(true)
	}
	if f.trad {
		eng.sess.SetTraditional(true)
	}
	if f.sp != "" {
		eng.sess.SetDoublePinyin(f.sp)
	}
	if err := f.conn.Export(eng, path, engineIface); err != nil {
		return "", godbus.MakeFailedError(fmt.Errorf("导出引擎实例失败: %w", err))
	}
	// 注册到实例表，供 DestroyEngine 回收：每个 InputContext 一个实例
	// （含整份组词会话），关闭的应用不回收会持续涨内存（2026-10-26 审查发现）。
	f.mu.Lock()
	f.engines[path] = eng
	f.mu.Unlock()
	log.Printf("已创建引擎实例 %d（%s）", seq, path)
	return path, nil
}

// DestroyEngine 是 IBus 工厂协议的标准方法：ibus-daemon 在 InputContext
// 关闭时调用，引擎应释放对应实例。此前未实现，实例只增不减。
func (f *ibusFactory) DestroyEngine(path string) *godbus.Error {
	op := godbus.ObjectPath(path)
	f.mu.Lock()
	eng, ok := f.engines[op]
	if ok {
		delete(f.engines, op)
	}
	f.mu.Unlock()
	if !ok {
		return nil
	}
	// godbus v5 没有 public Unexport：Export(nil, path, iface) 即取消导出
	//（其内部注释「Remove a previous export if the interface is nil」）。
	if err := f.conn.Export(nil, op, engineIface); err != nil {
		log.Printf("取消导出 %s 失败: %v", path, err)
	}
	_ = eng // 会话随对象一起被 GC
	log.Printf("已销毁引擎实例 %s", path)
	return nil
}

// ---- org.freedesktop.IBus.Engine ----

// ProcessKeyEvent 处理按键：返回 true 表示已消费（不再透传给应用）。
//
// 整个函数从头到尾持 e.mu：godbus 对每个方法调用各起一个 goroutine
// （conn.handleCall），本方法与 PageUp/PageDown/Reset/FocusOut 是真并发。
// Shift 空按读写的 enMode、Ctrl+Backspace 早退分支读写的 sess 此前都在锁外，
// 与加锁的 PageUp 等构成数据竞争——测试此前没并发调用本方法的用例，
// go test -race 一直没发现（2026-10-26 审查实测 ProcessKeyEvent 覆盖 0%）。
func (e *ibusEngine) ProcessKeyEvent(keyval, keycode, state uint32) (bool, *godbus.Error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Shift 空按释放：切换中英模式（英文模式下丢弃未上屏的组词）。
	// 判定切换后仍**放行**该 Shift 释放事件：应用已收到 Shift 按下，
	// 若吞掉释放，依赖修饰键状态机的应用（如 Shift+点击范围选择）会
	// 一直认为 Shift 仍按着（对齐 macOS 壳「绝不吞掉宿主应用的按键
	// 状态机」的铁律，2026-10-27 审查发现）。
	if e.shift.feed(keyval, state) {
		e.enMode = !e.enMode
		e.sess.Reset()
		e.hidePreedit()
		e.hideLookupTable()
		return false, nil
	}
	// 英文模式：全部放行，组词/选词/标点映射均不介入
	if e.enMode {
		return false, nil
	}
	if state&ibusReleaseMask != 0 {
		return false, nil
	}
	if state&(ibusCtrlMask|ibusMod1Mask) != 0 {
		if isComposing(e.sess) && keyval == keysymBackspace {
			e.sess.Reset()
			e.hidePreedit()
			e.hideLookupTable()
			return true, nil
		}
		return false, nil
	}

	switch {
	case keyval >= 'a' && keyval <= 'z':
		comp, _ := e.sess.Input(string(rune(keyval)))
		e.refresh(comp)
		return true, nil

	case keyval == keysymBackspace:
		if !isComposing(e.sess) {
			return false, nil
		}
		// 普通 Backspace：慢慢部分删除（逐字符）
		comp, _ := e.sess.Backspace()
		e.refresh(comp)
		return true, nil

	case keyval == keysymEscape:
		// Esc：直接全部删除
		if isComposing(e.sess) {
			e.sess.Reset()
			e.hidePreedit()
			e.hideLookupTable()
			return true, nil
		}
		return false, nil

	case (keyval >= '1' && keyval <= '9') || (keyval >= keysymKP1 && keyval <= keysymKP9):
		// 数字键（主键盘 1-9 与小键盘 KP_1..KP_9）选对应候选。
		// 每页容量是 6（engine.PageSize），越界数字**不消费**且候选窗保持
		// 原样：此前越界时隐藏预编辑/候选表但 session 仍在组词——用户刚打
		// 的拼音从屏幕消失、数字又漏进文档，下一次击键组词又「复活」
		// （对齐 macOS 壳按实际候选数判定的修复，2026-10-27 审查发现）。
		idx := int(keyval - '1')
		if keyval >= keysymKP1 {
			idx = int(keyval - keysymKP1)
		}
		text, err := e.sess.Select(idx)
		if err != nil {
			return false, nil
		}
		e.commit(text)
		return true, nil

	case keyval == keysymReturn:
		// 回车：组词中不论有无候选，按回车直接将输入的原始拼音上屏，清空组词并消费回车键
		if e.sess != nil {
			comp := e.sess.Compose()
			if comp.Input != "" {
				raw := comp.Input
				e.sess.Reset()
				e.hidePreedit()
				e.hideLookupTable()
				e.commit(raw)
				return true, nil
			}
		}
		return false, nil

	case keyval == keysymSpace:
		// Shift+空格：切换全角/半角标点模式（优先于首选上屏）
		if state&ibusShiftMask != 0 {
			e.sess.ToggleHalfWidth()
			return true, nil
		}
		// 组词中空格一律消费（用户意图永远是「结束组词」，不应有裸空格
		// 漏进文档）：有候选选首选；残缺组词（无候选）顶屏原始拼音，与
		// 回车分支等效。此前无候选时直接放行——一个多余空格进文档、
		// 看不见的活组词继续挂着（对齐 macOS 壳 2026-10-25 实机修复）。
		if !isComposing(e.sess) {
			return false, nil // 非组词：空格照旧放行
		}
		if hasCandidates(e.sess) {
			text, err := e.sess.Select(0)
			if err != nil {
				e.sess.Reset()
				e.hidePreedit()
				e.hideLookupTable()
				return true, nil
			}
			e.commit(text)
			return true, nil
		}
		raw := e.sess.Compose().Input
		e.sess.Reset()
		e.hidePreedit()
		e.hideLookupTable()
		e.commit(raw)
		return true, nil

	case keyval == keysymPageUp || keyval == keysymPageDown:
		// Page Up / Page Down 键盘翻页（组词中消费；空组词放行原键）
		if !isComposing(e.sess) {
			return false, nil
		}
		var comp engine.Composition
		if keyval == keysymPageUp {
			comp, _ = e.sess.PrevPage()
		} else {
			comp, _ = e.sess.NextPage()
		}
		e.refresh(comp)
		return true, nil

	case keyval == '-' || keyval == '=' || keyval == '[' || keyval == ']':
		// 翻页键与标点键的分流（对齐 macOS 壳 ChaosetController 的行为）：
		//   组词中且有候选：- = [ ] 都是翻页键；
		//   其余情况：[ ] 是标点键输出【】（走下方标点分支），
		//             - = 不是标点键透传原键。
		//
		// 此前只判 isComposing 就消费：残缺输入（无候选）时按键被吞且毫无
		// 反馈；[ ] 也永远到不了标点分支——Linux 上组词中打不出【】，
		// 空组词时也只会透传出半角 [（2026-10-26 审查发现）。
		if isComposing(e.sess) && hasCandidates(e.sess) {
			var comp engine.Composition
			if keyval == '-' || keyval == '[' {
				comp, _ = e.sess.PrevPage()
			} else {
				comp, _ = e.sess.NextPage()
			}
			e.refresh(comp)
			return true, nil
		}
		if engine.IsPunctKey(rune(keyval)) {
			text, err := e.sess.Punct(string(rune(keyval)))
			if err != nil {
				return false, nil
			}
			e.commit(text)
			return true, nil
		}
		return false, nil

	case keyval == '\'' && isComposing(e.sess):
		// 组词中的撇号是音节分隔符（0x27 不在 a-z 分支内，在此分流）
		comp, _ := e.sess.Input("'")
		e.refresh(comp)
		return true, nil

	case engine.IsPunctKey(rune(keyval)):
		// 标点键：组词中先上屏首选再出中文标点，空组词直接出中文标点
		text, err := e.sess.Punct(string(rune(keyval)))
		if err != nil {
			return false, nil // 未知标点：不消费，放行原键
		}
		e.commit(text)
		return true, nil
	}

	// 不可处理的按键（方向键 / Tab / F 键 / 大写字母 / 数字 0 / Alt+字母等）：
	// 组词中先清空再放行——否则字符插进文档的同时预编辑与候选窗仍挂在原地，
	// 光标移走后下一个字母会接着旧组词继续，预编辑与实文彻底脱节
	// （对齐 macOS 壳 passThrough，2026-10-27 审查发现）。
	if isComposing(e.sess) {
		e.sess.Reset()
		e.hidePreedit()
		e.hideLookupTable()
	}
	return false, nil
}

func hasCandidates(s *engine.Session) bool {
	comp := s.Compose()
	return len(comp.Candidates) > 0
}

func isComposing(s *engine.Session) bool {
	return s.Compose().Input != ""
}

func (e *ibusEngine) updatePreedit(preedit string, visible bool) {
	if !visible {
		e.hidePreedit()
		return
	}
	e.conn.Emit(e.path, engineIface+".UpdatePreeditText",
		godbus.MakeVariant(newIBusText(preedit)), uint32(0), true, uint32(0))
}

func (e *ibusEngine) hidePreedit() {
	e.conn.Emit(e.path, engineIface+".UpdatePreeditText",
		godbus.MakeVariant(newIBusText("")), uint32(0), false, uint32(0))
}

// ibusLookupTable 对应 libibus 的 IBusLookupTable 线缆序列化元组：
// ("IBusLookupTable", a{sv}, u page_size, u cursor_pos, b cursor_visible,
// b round, v candidates, v labels)，其中两个 v 均为 av（IBusText 变体
// 数组），字段顺序与 ibus_lookup_table_serialize 一致。
type ibusLookupTable struct {
	TypeName      string
	Attachments   map[string]godbus.Variant
	PageSize      uint32
	CursorPos     uint32
	CursorVisible bool
	Round         bool
	Candidates    godbus.Variant
	Labels        godbus.Variant
}

// ibusPageSize 直接取引擎导出的每页容量，**不要再写死一个数字**。
//
// 2026-10-26 审查发现：这里曾写死 9 并注释「与引擎一致」，而引擎早在
// 2026-10-25 就把 pageSize 从 9 改成了 6。IBusLookupTable.page_size 是面板
// 布局与「还有没有下一页」的判断依据，声明 9 而实际只给 6 项会让面板末尾
// 留空、翻页按钮状态错乱。两份真相必然漂移，改为单一来源。
var ibusPageSize = engine.PageSize()

func newIBusLookupTable(comp engine.Composition) ibusLookupTable {
	cands := make([]godbus.Variant, 0, len(comp.Candidates))
	labels := make([]godbus.Variant, 0, len(comp.Candidates))
	for i, c := range comp.Candidates {
		cands = append(cands, godbus.MakeVariant(newIBusText(c.Text)))
		// 序号 1..N，不做 %10 回绕：第 10 项会显示成 "0."，而 ProcessKeyEvent
		// 只认数字键 1-9、ibus 面板也不认 0 —— 那一项将永远选不到
		// （2026-10-26 审查发现）。当前 pageSize=6 触发不到，但只要页容量
		// 调到 >9 就会暴露，这里直接按真实序号生成。
		labels = append(labels, godbus.MakeVariant(newIBusText(fmt.Sprintf("%d.", i+1))))
	}
	return ibusLookupTable{
		TypeName:      "IBusLookupTable",
		Attachments:   map[string]godbus.Variant{},
		PageSize:      uint32(ibusPageSize),
		CursorPos:     0, // 首选高亮
		CursorVisible: true,
		Round:         false,
		Candidates:    godbus.MakeVariant(cands),
		Labels:        godbus.MakeVariant(labels),
	}
}

// updateLookupTable 把当前页候选上报给 ibus 面板（预编辑之外的第二路
// 展示，数字键选择由此从「盲选」变为可见）。
func (e *ibusEngine) updateLookupTable(comp engine.Composition) {
	if len(comp.Candidates) == 0 {
		e.hideLookupTable()
		return
	}
	e.conn.Emit(e.path, engineIface+".UpdateLookupTable",
		godbus.MakeVariant(newIBusLookupTable(comp)), true)
}

func (e *ibusEngine) hideLookupTable() {
	e.conn.Emit(e.path, engineIface+".UpdateLookupTable",
		godbus.MakeVariant(newIBusLookupTable(engine.Composition{})), false)
}

// refresh 把组成状态同步到 ibus 面板：预编辑文本 + 当前页候选表。
//
// 预编辑可见性的判据是「preedit 非空」而不是「有候选」：引擎对无法切分的
// 输入（如 beijngi、niihao）仍返回非空 Preedit 但零候选（这类输入要走纠错
// 路径）。若按「有无候选」决定可见性，用户敲了字母却什么都看不见——
// 与 macOS 壳（preedit 非空即显示）不一致，2026-10-26 审查发现。
func (e *ibusEngine) refresh(comp engine.Composition) {
	e.updatePreedit(comp.Preedit, comp.Preedit != "")
	e.updateLookupTable(comp)
}

func (e *ibusEngine) commit(text string) {
	e.conn.Emit(e.path, engineIface+".CommitText",
		godbus.MakeVariant(newIBusText(text)))
	e.hidePreedit()
	e.hideLookupTable()
}

// ---- Engine 生命周期方法 ----

func (e *ibusEngine) Enable() *godbus.Error  { return nil }
func (e *ibusEngine) Disable() *godbus.Error { return nil }

// FocusIn：输入上下文获得焦点。
//
// 必须重置 shift 空按跟踪：否则在上一个窗口按了一半的 Shift 会带到这里，
// 跨窗口误判成「空按切换中英」（2026-10-26 审查发现此前是空实现）。
func (e *ibusEngine) FocusIn() *godbus.Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shift.reset()
	return nil
}

// FocusOut：输入上下文失去焦点。
//
// 必须清空组词并收起预编辑与候选面板：否则在 A 窗口打了 nihao 没上屏、
// 切到 B 窗口后，组词状态仍残留（下次击键会接着 A 的内容继续组），而
// 候选面板还可能挂在 B 窗口上显示 A 的候选（2026-10-26 审查发现此前是空实现）。
// 中英模式（enMode）**不清**：那属于用户输入习惯，不该随窗口切换改变。
func (e *ibusEngine) FocusOut() *godbus.Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sess.Reset()
	e.shift.reset()
	e.hidePreedit()
	e.hideLookupTable()
	return nil
}

// Reset：ibus 要求重置输入状态（如按 Esc 或切换上下文时）。
// 清空组词**并收起面板**——此前只清 session、不发隐藏信号，面板会显示
// 过期候选直到下一次按键。
func (e *ibusEngine) Reset() *godbus.Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sess.Reset()
	e.hidePreedit()
	e.hideLookupTable()
	return nil
}
func (e *ibusEngine) SetCursorLocation(x, y, w, h int32) *godbus.Error { return nil }
func (e *ibusEngine) SetCapabilities(caps uint32) *godbus.Error        { return nil }

// PageUp / PageDown 由 ibus-daemon 在面板翻页按钮点击时调用：
// 翻动引擎页码并把当前页同步到候选表。
func (e *ibusEngine) PageUp() *godbus.Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	comp, _ := e.sess.PrevPage()
	e.refresh(comp)
	return nil
}

func (e *ibusEngine) PageDown() *godbus.Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	comp, _ := e.sess.NextPage()
	e.refresh(comp)
	return nil
}
func (e *ibusEngine) CursorUp() *godbus.Error   { return nil }
func (e *ibusEngine) CursorDown() *godbus.Error { return nil }
func (e *ibusEngine) PropertyActivate(name string, state uint32) *godbus.Error {
	return nil
}
func (e *ibusEngine) PropertyShow(name string) *godbus.Error { return nil }
func (e *ibusEngine) PropertyHide(name string) *godbus.Error { return nil }
