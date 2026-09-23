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
	ibusBusName  = "org.freedesktop.IBus.Chaoset"
	factoryPath  = "/org/freedesktop/IBus/Factory"
	factoryIface = "org.freedesktop.IBus.Factory"
	enginePath   = "/org/freedesktop/IBus/Engine/chaoset"
	engineIface  = "org.freedesktop.IBus.Engine"

	// ibus keysym（与 X keysym 一致）
	keysymBackspace = 0xFF08
	keysymReturn    = 0xFF0D
	keysymEscape    = 0xFF1B
	keysymSpace     = 0x20

	// ibus 修饰键掩码（IBUS_MODIFIER_MASK 中的常用位）
	ibusReleaseMask = 1 << 30
	ibusCtrlMask    = 1 << 2
	ibusMod1Mask    = 1 << 3
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

// ibusEngine 是导出给 ibus-daemon 的引擎对象（v0 单实例：
// 单用户场景下所有 InputContext 共享同一组词状态）。
type ibusEngine struct {
	conn *godbus.Conn
	dict *engine.Dictionary
	user *engine.UserDict

	mu   sync.Mutex
	sess *engine.Session
}

func newIBusEngine(dict *engine.Dictionary, user *engine.UserDict) (*ibusEngine, error) {
	conn, err := godbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("连接 session bus 失败: %w", err)
	}
	return &ibusEngine{
		conn: conn,
		dict: dict,
		user: user,
		sess: engine.NewSession(dict, user),
	}, nil
}

func (e *ibusEngine) run() error {
	// 引擎对象：ibus-daemon 与外部测试工具都直接调用其方法
	if err := e.conn.Export(e, enginePath, engineIface); err != nil {
		return fmt.Errorf("导出引擎对象失败: %w", err)
	}
	// 工厂对象：ibus-daemon 激活组件时调用 CreateEngine
	if err := e.conn.Export(e, factoryPath, factoryIface); err != nil {
		return fmt.Errorf("导出工厂对象失败: %w", err)
	}
	reply, err := e.conn.RequestName(ibusBusName, godbus.NameFlagAllowReplacement|godbus.NameFlagReplaceExisting)
	if err != nil {
		return fmt.Errorf("请求总线名失败: %w", err)
	}
	log.Printf("chaoset ibus 引擎就绪（request_name=%d）", reply)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	go func() {
		<-stop
		e.conn.Close()
		os.Exit(0)
	}()

	// 阻塞等待（导出对象的回调由 godbus 连接 goroutine 分发）
	select {}
}

// ---- org.freedesktop.IBus.Factory ----

// CreateEngine 由 ibus-daemon 在激活引擎时调用；v0 返回共享单实例路径。
func (e *ibusEngine) CreateEngine(name string) (godbus.ObjectPath, *godbus.Error) {
	if name != "chaoset" {
		return "", godbus.MakeFailedError(fmt.Errorf("未知引擎 %q", name))
	}
	return godbus.ObjectPath(enginePath), nil
}

// ---- org.freedesktop.IBus.Engine ----

// ProcessKeyEvent 处理按键：返回 true 表示已消费（不再透传给应用）。
func (e *ibusEngine) ProcessKeyEvent(keyval, keycode, state uint32) (bool, *godbus.Error) {
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

	case keyval == keysymReturn || keyval == keysymSpace:
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
	e.conn.Emit(enginePath, engineIface+".UpdatePreeditText",
		godbus.MakeVariant(newIBusText(preedit)), uint32(0), true, uint32(0))
}

func (e *ibusEngine) hidePreedit() {
	e.conn.Emit(enginePath, engineIface+".UpdatePreeditText",
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
	e.conn.Emit(enginePath, engineIface+".UpdateLookupTable",
		godbus.MakeVariant(newIBusLookupTable(comp)), true)
}

func (e *ibusEngine) hideLookupTable() {
	e.conn.Emit(enginePath, engineIface+".UpdateLookupTable",
		godbus.MakeVariant(newIBusLookupTable(engine.Composition{})), false)
}

// refresh 把组成状态同步到 ibus 面板：预编辑文本 + 当前页候选表。
func (e *ibusEngine) refresh(comp engine.Composition) {
	e.updatePreedit(comp.Preedit, len(comp.Candidates) > 0)
	e.updateLookupTable(comp)
}

func (e *ibusEngine) commit(text string) {
	e.conn.Emit(enginePath, engineIface+".CommitText",
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
func (e *ibusEngine) PageUp() *godbus.Error                            { return nil }
func (e *ibusEngine) PageDown() *godbus.Error                          { return nil }
func (e *ibusEngine) CursorUp() *godbus.Error                          { return nil }
func (e *ibusEngine) CursorDown() *godbus.Error                        { return nil }
func (e *ibusEngine) PropertyActivate(name string, state uint32) *godbus.Error {
	return nil
}
func (e *ibusEngine) PropertyShow(name string) *godbus.Error { return nil }
func (e *ibusEngine) PropertyHide(name string) *godbus.Error { return nil }
