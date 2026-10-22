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
type ibusEngine struct {
	conn *godbus.Conn
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

	mu  sync.Mutex
	seq uint32 // 已创建的引擎实例计数（对象路径编号）
}

func newIBusFactory(dict *engine.Dictionary, user *engine.UserDict, fuzzy, trad bool, sp string) (*ibusFactory, error) {
	conn, err := godbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("连接 session bus 失败: %w", err)
	}
	return &ibusFactory{conn: conn, dict: dict, user: user, fuzzy: fuzzy, trad: trad, sp: sp}, nil
}

func (f *ibusFactory) run() error {
	// 工厂对象：ibus-daemon 激活组件时调用 CreateEngine
	if err := f.conn.Export(f, factoryPath, factoryIface); err != nil {
		return fmt.Errorf("导出工厂对象失败: %w", err)
	}
	reply, err := f.conn.RequestName(ibusBusName, godbus.NameFlagAllowReplacement|godbus.NameFlagReplaceExisting)
	if err != nil {
		return fmt.Errorf("请求总线名失败: %w", err)
	}
	log.Printf("chaoset ibus 引擎就绪（request_name=%d）", reply)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
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
	log.Printf("已创建引擎实例 %d（%s）", seq, path)
	return path, nil
}

// ---- org.freedesktop.IBus.Engine ----

// ProcessKeyEvent 处理按键：返回 true 表示已消费（不再透传给应用）。
func (e *ibusEngine) ProcessKeyEvent(keyval, keycode, state uint32) (bool, *godbus.Error) {
	// Shift 空按释放：切换中英模式（英文模式下丢弃未上屏的组词）
	if e.shift.feed(keyval, state) {
		e.enMode = !e.enMode
		e.sess.Reset()
		e.hidePreedit()
		e.hideLookupTable()
		return true, nil
	}
	// 英文模式：全部放行，组词/选词/标点映射均不介入
	if e.enMode {
		return false, nil
	}
	// 释放事件与 Ctrl/Alt 组合键一律放行
	if state&(ibusReleaseMask|ibusCtrlMask|ibusMod1Mask) != 0 {
		return false, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	switch {
	case keyval >= 'a' && keyval <= 'z':
		comp, _ := e.sess.Input(string(rune(keyval)))
		e.refresh(comp)
		return true, nil

	case keyval == keysymBackspace:
		comp, _ := e.sess.Backspace()
		e.refresh(comp)
		return true, nil

	case keyval == keysymEscape:
		e.sess.Reset()
		e.hidePreedit()
		e.hideLookupTable()
		return true, nil

	case keyval >= '1' && keyval <= '9':
		text, err := e.sess.Select(int(keyval - '1'))
		if err != nil {
			// 无对应候选：不消费，让数字原样上屏
			e.hidePreedit()
			e.hideLookupTable()
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
		if !hasCandidates(e.sess) {
			return false, nil
		}
		text, err := e.sess.Select(0)
		if err != nil {
			e.hidePreedit()
			e.hideLookupTable()
			return true, nil
		}
		e.commit(text)
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

	case keyval == '-' || keyval == '=':
		// 候选翻页（组词中消费；空组词放行原键；边界由 daemon 钳制）
		if !isComposing(e.sess) {
			return false, nil
		}
		var comp engine.Composition
		if keyval == '-' {
			comp, _ = e.sess.PrevPage()
		} else {
			comp, _ = e.sess.NextPage()
		}
		e.refresh(comp)
		return true, nil

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

// ibusPageSize 与引擎的每页候选数一致（engine 内部常量 pageSize）。
const ibusPageSize = 9

func newIBusLookupTable(comp engine.Composition) ibusLookupTable {
	cands := make([]godbus.Variant, 0, len(comp.Candidates))
	labels := make([]godbus.Variant, 0, len(comp.Candidates))
	for i, c := range comp.Candidates {
		cands = append(cands, godbus.MakeVariant(newIBusText(c.Text)))
		labels = append(labels, godbus.MakeVariant(newIBusText(fmt.Sprintf("%d.", (i+1)%10))))
	}
	return ibusLookupTable{
		TypeName:      "IBusLookupTable",
		Attachments:   map[string]godbus.Variant{},
		PageSize:      ibusPageSize,
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
func (e *ibusEngine) refresh(comp engine.Composition) {
	e.updatePreedit(comp.Preedit, len(comp.Candidates) > 0)
	e.updateLookupTable(comp)
}

func (e *ibusEngine) commit(text string) {
	e.conn.Emit(e.path, engineIface+".CommitText",
		godbus.MakeVariant(newIBusText(text)))
	e.hidePreedit()
	e.hideLookupTable()
}

// ---- 其余 Engine 方法：v0 为无操作，保持接口完整 ----

func (e *ibusEngine) Enable() *godbus.Error   { return nil }
func (e *ibusEngine) Disable() *godbus.Error  { return nil }
func (e *ibusEngine) FocusIn() *godbus.Error  { return nil }
func (e *ibusEngine) FocusOut() *godbus.Error { return nil }
func (e *ibusEngine) Reset() *godbus.Error {
	e.sess.Reset()
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
