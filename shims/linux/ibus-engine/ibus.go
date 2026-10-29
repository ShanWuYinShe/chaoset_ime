package main

// IBus DBus 线缆胶水。接口与方法签名逐项对照官方 libibus
// （src/ibusengine.c 的 introspection XML）：
//   ProcessKeyEvent(u keyval, u keycode, u state) -> b
//   信号 CommitText(v text) / UpdatePreeditText(v text, u cursor, b visible, u mode)
//   信号 UpdateAuxiliaryText(v text, b visible)
// 其中 v 是 IBusSerializable 序列化变体。IBusText 的完整线缆格式是
// (s 类型名, a{sv} 附件, s 文本, v 属性列表)，属性列表为 IBusAttrList
// （(s, a{sv}, av)），每个属性 IBusAttribute 为 (s, a{sv}, u type,
// u value, u start, u end)——与 ibus_text_serialize/ibusattribute.c
// 逐字段对应。此前缺属性列表字段，预编辑无法携带下划线标记。

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

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
	ibusMod4Mask    = 1 << 6 // Super/Win 键（GNOME/KDE 大量占用 Super+字母）
	ibusShiftMask   = 1 << 0
)

// ibusText 对应 libibus 的 IBusText 线缆序列化元组 (s, a{sv}, s, v)。
// 第 4 个字段是 IBusAttrList 变体（文本属性：下划线/前景色等）。
type ibusText struct {
	TypeName    string
	Attachments map[string]godbus.Variant
	Text        string
	Attrs       godbus.Variant
}

// ibusAttribute 对应 (s, a{sv}, u type, u value, u start, u end)。
type ibusAttribute struct {
	TypeName    string
	Attachments map[string]godbus.Variant
	AttrType    uint32
	Value       uint32
	StartIndex  uint32
	EndIndex    uint32
}

// ibusAttrList 对应 (s, a{sv}, av)。
type ibusAttrList struct {
	TypeName    string
	Attachments map[string]godbus.Variant
	Attrs       []godbus.Variant
}

// ibus 常量：IBUS_ATTR_TYPE_UNDERLINE=1（下划线标记），
// IBUS_ATTR_UNDERLINE_SINGLE=1（单线下划线）。
const (
	ibusAttrTypeUnderline   = 1
	ibusAttrUnderlineSingle = 1
)

func newIBusAttrList(attrs []godbus.Variant) godbus.Variant {
	if attrs == nil {
		attrs = []godbus.Variant{}
	}
	return godbus.MakeVariant(ibusAttrList{
		TypeName: "IBusAttrList", Attachments: map[string]godbus.Variant{}, Attrs: attrs,
	})
}

func newIBusText(text string) ibusText {
	return ibusText{TypeName: "IBusText", Attachments: map[string]godbus.Variant{},
		Text: text, Attrs: newIBusAttrList(nil)}
}

// newPreeditText 生成带整串下划线标记的预编辑文本：没有下划线时，应用把
// 预编辑渲染成与已提交文本无差别的裸文本，部分应用无法分辨「正在组词」。
// index 以字符（非字节）为单位，与 libibus 的 unichar 索引一致。
func newPreeditText(text string) ibusText {
	t := newIBusText(text)
	if text != "" {
		underline := ibusAttribute{TypeName: "IBusAttribute",
			Attachments: map[string]godbus.Variant{},
			AttrType:    ibusAttrTypeUnderline, Value: ibusAttrUnderlineSingle,
			StartIndex: 0, EndIndex: uint32(utf8.RuneCountInString(text)),
		}
		t.Attrs = newIBusAttrList([]godbus.Variant{godbus.MakeVariant(underline)})
	}
	return t
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
	// assoc 非空即联想中：选词上屏后按上屏词音节查到的续接词组，候选表
	// 分页展示（assocPage 当前页）、数字键连选（对齐 macOS 壳，2026-10-27
	// 接入 Linux；2026-10-28 翻页化）。
	assoc     []engine.Candidate
	assocPage int
	// assocOn 词组联想开关的**当前生效值**（config.json associate，默认开启）。
	// 运行中由 associateEnabled() 在每次进入联想前重读配置文件刷新；命令行
	// 显式传 -associate 时（assocFlagSet）不再重读，flag 优先——排障覆盖语义。
	assocOn bool
	// assocConfigPath 用户配置文件路径（与 chaoset-setup.py 写入的是同一个
	// $XDG_CONFIG_HOME/chaoset/config.json）；空串表示不重读。
	assocConfigPath string
	// assocFlagSet 表示命令行显式传了 -associate（flag.Visit 判定）。
	assocFlagSet bool
	// assocConfigErr 上次记录的配置文件读取错误，用于去重日志：配置坏掉时
	// 每次进入联想都会重读，不能每个词刷一条日志。
	assocConfigErr string
	// halfWidth 全/半角标点开关：壳本地持有（2026-10-29 起），随标点请求
	// 经 SetHalfWidth 告知引擎，快捷键切换不与 daemon 往返。
	halfWidth bool
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
	conn    *godbus.Conn
	dict    *engine.Dictionary
	user    *engine.UserDict
	fuzzy   bool
	trad    bool
	sp      string
	assocOn bool // 词组联想开关初始值（config.json associate，默认 true）
	// assocConfigPath/assocFlagSet 用于「设置即时生效」：每个引擎实例在每次
	// 进入联想前重读配置文件（见 ibusEngine.associateEnabled）；命令行显式
	// 传 -associate 时 flag 覆盖配置，不再重读。
	assocConfigPath string
	assocFlagSet    bool

	mu      sync.Mutex
	seq     uint32 // 已创建的引擎实例计数（对象路径编号）
	engines map[godbus.ObjectPath]*ibusEngine
}

func newIBusFactory(dict *engine.Dictionary, user *engine.UserDict, fuzzy, trad bool, sp string,
	assocOn bool, assocConfigPath string, assocFlagSet bool) (*ibusFactory, error) {
	conn, err := godbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("连接 session bus 失败: %w", err)
	}
	return &ibusFactory{
		conn:            conn,
		dict:            dict,
		user:            user,
		fuzzy:           fuzzy,
		trad:            trad,
		sp:              sp,
		assocOn:         assocOn,
		assocConfigPath: assocConfigPath,
		assocFlagSet:    assocFlagSet,
		engines:         make(map[godbus.ObjectPath]*ibusEngine),
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
		conn:            f.conn,
		dict:            f.dict,
		user:            f.user,
		path:            path,
		sess:            engine.NewSession(f.dict, f.user),
		assocOn:         f.assocOn,
		assocConfigPath: f.assocConfigPath,
		assocFlagSet:    f.assocFlagSet,
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
		// 提示文案三端统一为「英文模式」/「中文模式」：macOS
		// （ChaosetController）与 Windows（win32.cpp）都是这个口径，
		// 此前 Linux 单独用「英文」/「中文」，同一操作在三端文案不同
		// （2026-10-29 三端一致性审查）。
		if e.enMode {
			e.showModeToast("英文模式")
		} else {
			e.showModeToast("中文模式")
		}
		return false, nil
	}
	// 英文模式：全部放行，组词/选词/标点映射均不介入
	if e.enMode {
		return false, nil
	}
	if state&ibusReleaseMask != 0 {
		return false, nil
	}
	// Mod4(Super) 一并放行：2026-10-29 输入事件审计 F7——此前只覆盖 Ctrl/Mod1，
	// Super+字母在非英文模式下会被当成普通字母喂进组词，吞掉桌面快捷键
	// （GNOME Super+A/S/E、KDE Super 系）。Windows 壳有等价的 win 判定，
	// Linux 此前缺失，三端不一致。
	if state&ibusMod4Mask != 0 {
		e.exitAssociate()
		return false, nil
	}
	if state&(ibusCtrlMask|ibusMod1Mask) != 0 {
		if isComposing(e.sess) && keyval == keysymBackspace {
			e.sess.Reset()
			e.hidePreedit()
			e.hideLookupTable()
			return true, nil
		}
		// Ctrl+U：类终端整行清空（对齐 macOS 壳，组词中消费并全部删除）
		if isComposing(e.sess) && keyval == 'u' {
			e.sess.Reset()
			e.hidePreedit()
			e.hideLookupTable()
			return true, nil
		}
		e.exitAssociate()
		return false, nil
	}

	// 词组联想分支（对齐 macOS 壳，2026-10-27 接入 Linux）：
	//   数字（含小键盘）有对应项 → 联想词直接上屏并继续联想；
	//   空格 → 结束联想，空格透传；回车/Esc → 结束联想并消费；
	//   - / = → 单页无翻页语义，收起放行；
	//   字母 → 退出联想落入下方正常组词；
	//   其余按键 → 退出联想走正常流程（未处理键的「先清空再放行」兜底
	//   在 switch 末尾，此处已收起联想窗不会残留）。
	if len(e.assoc) > 0 {
		isDigit := (keyval >= '1' && keyval <= '9') || (keyval >= keysymKP1 && keyval <= keysymKP9)
		switch {
		case keyval == keysymEscape || keyval == keysymReturn || keyval == keysymBackspace:
			// Backspace：收起联想并消费——透传会误删刚上屏的文字
			e.exitAssociate()
			return true, nil
		case keyval == keysymSpace:
			// 空格：**默认选当前页第一个联想词上屏**（渐进组句闭环，
			// 与 macOS/Windows 端一致），不再是透传
			if len(e.assoc) > 0 {
				pick := e.assoc[0]
				sylls := strings.Split(pick.Syllables, "'")
				if word, err := e.sess.AssociateSelect(pick.Text, sylls); err == nil {
					e.commit(word)
					e.enterAssociate(sylls)
					return true, nil
				}
			}
			e.exitAssociate()
			return true, nil
		case keyval == '-' || keyval == '=' || keyval == '[' || keyval == ']':
			// 联想翻页（边界页保持当前页）。键集合与组词态翻页键完全一致：
			// - / =、[ / ]（2026-10-29 三端一致性审查）。
			e.flipAssociate(keyval == '=' || keyval == ']')
			return true, nil
		case keyval == keysymPageUp || keyval == keysymPageDown:
			// 联想中 Page Up / Down 翻联想页——与 - / = / [ / ] 同语义，
			// 对齐 macOS/Windows 壳（2026-10-29 三端一致性审查）。
			// 此前该键落到 default「退出联想 + 透传」，文档跟着滚动；
			// 下方 switch 里那条「Page Up/Down 翻联想页」分支因此永远
			// 不可达（死代码），现改由这里处理。
			e.flipAssociate(keyval == keysymPageDown)
			return true, nil
		case isDigit:
			// 全局序号 = 当前页 × 每页条数 + 页内序号（候选表展示的是切片）
			idx := e.assocPage*engine.PageSize() + int(keyval-'1')
			if keyval >= keysymKP1 {
				idx = e.assocPage*engine.PageSize() + int(keyval-keysymKP1)
			}
			if idx < len(e.assoc) {
				pick := e.assoc[idx]
				sylls := strings.Split(pick.Syllables, "'")
				if word, err := e.sess.AssociateSelect(pick.Text, sylls); err == nil {
					e.commit(word)
					e.enterAssociate(sylls)
					return true, nil
				}
			}
			e.exitAssociate()
			return false, nil
		default:
			e.exitAssociate()
		}
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
		// Shift+Backspace：直接全部删除（对齐 macOS 壳把 Shift+Delete 归入
		// 全部删除；此前走逐字符回退，三端不一致）
		if state&ibusShiftMask != 0 {
			e.sess.Reset()
			e.hidePreedit()
			e.hideLookupTable()
			return true, nil
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
		comp := e.sess.Compose()
		if idx >= len(comp.Candidates) {
			return false, nil
		}
		// 渐进上屏：只消耗候选音节对应的拼音前缀，剩余回灌继续组词
		rest, committed, err := e.sess.SelectPrefix(idx)
		if err != nil {
			return false, nil
		}
		e.commit(committed)
		if rest.Input != "" {
			// 剩余拼音回灌：预编辑显示剩余串，候选表刷新为剩余组词
			e.refresh(rest)
			return true, nil
		}
		// 剩余耗尽：进入联想（与整句上屏同收尾）
		e.enterAssociateForPrev(e.sess.PrevWord())
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
		// Shift+空格：切换全角/半角标点模式（优先于首选上屏），并短暂提示。
		// 开关归壳本地持有（2026-10-29 起），SetHalfWidth 随后告知引擎（进程内
		// 直调，无往返），daemon/引擎只按状态产出标点文本。
		if state&ibusShiftMask != 0 {
			// 只在输入法介入时（组词/联想）才消费。2026-10-29 修复
			// （输入事件审计 F1）：此前无状态前提地吃掉任何应用里的
			// Shift+Space——游戏里 Shift=冲刺、Space=跳。
			// 注意（2026-10-30 三端一致性整改）：联想态实际到不了本分支——
			// 上方联想分支已把空格（含 Shift+空格）当「选第一个联想词」消费，
			// **不**切换全/半角（三端实测一致）；这里只处理组词态。
			if !isComposing(e.sess) && len(e.assoc) == 0 {
				return false, nil
			}
			e.halfWidth = !e.halfWidth
			e.sess.SetHalfWidth(e.halfWidth)
			if e.halfWidth {
				e.showModeToast("半角标点")
			} else {
				e.showModeToast("全角标点")
			}
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
			// 渐进上屏：只消耗首选音节对应的拼音前缀，剩余回灌继续组词
			rest, committed, err := e.sess.SelectPrefix(0)
			if err != nil {
				text, serr := e.sess.Select(0)
				if serr != nil {
					e.sess.Reset()
					e.hidePreedit()
					e.hideLookupTable()
					return true, nil
				}
				e.commit(text)
				return true, nil
			}
			e.commit(committed)
			if rest.Input != "" {
				e.refresh(rest)
				return true, nil
			}
			e.enterAssociateForPrev(e.sess.PrevWord())
			return true, nil
		}
		raw := e.sess.Compose().Input
		e.sess.Reset()
		e.hidePreedit()
		e.hideLookupTable()
		e.commit(raw)
		return true, nil

	case keyval == keysymPageUp || keyval == keysymPageDown:
		// 联想中：Page Up / Down 翻联想页（与 - / = 同语义）
		// 联想态已在函数前半段的联想分支里 return（见上方 switch），
		// 这里只处理组词态，无需再判 e.assoc。
		// Page Up / Page Down 键盘翻页（组词中且有候选时消费；空组词放行
		// 原键）。残缺组词（无候选）时对齐 macOS/Windows 壳：先清空组词再
		// 放行——此前静默吞键，宿主文档不滚动，用户感知为「按键失灵」。
		if !isComposing(e.sess) {
			return false, nil
		}
		if !hasCandidates(e.sess) {
			e.sess.Reset()
			e.hidePreedit()
			e.hideLookupTable()
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
	// cursor 取文本末尾（字符数）：此前恒传 0，内联光标画在拼音串开头
	// 而不是末尾（2026-10-27 审查发现）。
	e.emitSigned(engineIface+".UpdatePreeditText",
		godbus.MakeVariant(newPreeditText(preedit)), uint32(utf8.RuneCountInString(preedit)), true, uint32(0))
}

func (e *ibusEngine) hidePreedit() {
	e.emitSigned(engineIface+".UpdatePreeditText",
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
	e.emitSigned(engineIface+".UpdateLookupTable",
		godbus.MakeVariant(newIBusLookupTable(comp)), true)
}

func (e *ibusEngine) hideLookupTable() {
	e.emitSigned(engineIface+".UpdateLookupTable",
		godbus.MakeVariant(newIBusLookupTable(engine.Composition{})), false)
}

// modeToastSeq 让连续快速切换时旧的隐藏定时器不再提前收起新提示。
var modeToastSeq atomic.Uint64

// modeToastDuration 是模式提示的展示时长。
const modeToastDuration = 1200 * time.Millisecond

// showModeToast 经 UpdateAuxiliaryText 短暂显示模式提示（如「英文」）。
// 中英/全半角此前切换零反馈，用户只能靠打字试探（2026-10-27 审查发现）。
// 定时器只碰 conn/path（godbus.Emit 线程安全），不取 e.mu，避免与输入
// 线程竞争；仅当期间没有更新的提示时才收起。
func (e *ibusEngine) showModeToast(text string) {
	seq := modeToastSeq.Add(1)
	e.emitSigned(engineIface+".UpdateAuxiliaryText",
		godbus.MakeVariant(newIBusText(text)), true)
	time.AfterFunc(modeToastDuration, func() {
		if modeToastSeq.Load() == seq {
			e.emitSigned(engineIface+".UpdateAuxiliaryText",
				godbus.MakeVariant(newIBusText("")), false)
		}
	})
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

// emitSigned 发送面板信号并记录失败：此前所有 Emit 返回值都被丢弃——
// 总线异常时 UI 停更而按键仍被消费，且无日志可查（2026-10-27 审查）。
// 打日志要避免每键刷屏：只记录非上下文关闭类错误（ECONNRESET/EOF 属
// 于正常断开）。
func (e *ibusEngine) emitSigned(name string, values ...interface{}) {
	if err := e.conn.Emit(e.path, name, values...); err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, godbus.ErrClosed) {
			log.Printf("面板信号 %s 发送失败: %v", name, err)
		}
	}
}

func (e *ibusEngine) commit(text string) {
	e.emitSigned(engineIface+".CommitText",
		godbus.MakeVariant(newIBusText(text)))
	e.hidePreedit()
	e.hideLookupTable()
}

// associateEnabled 返回当前词组联想开关，并在每次进入联想前重读 config.json。
//
// 为什么必须重读（2026-10-30 三端一致性整改）：此前 assocOn 由 main.go 在
// 引擎启动时一次性快照进 ibusEngine，运行中不再读取——设置窗口（chaoset-setup.py）
// 保存后要等整个 chaoset-engine 进程重启才生效，而脚本与 README 却宣称
// 「保存后对新开的输入上下文生效」。macOS（ChaosetController 每次选词前读
// UserDefaults）与 Windows（enterAssociate 每次 RegGetAssociate）都是即时生效，
// 三端语义不一致。这里按同一语义在每次进入联想前重读。
//
// 成本：每次上屏后进联想读一次小 JSON（KB 级），用户感知不到；配置文件由
// 设置脚本原子替换（os.replace），不会读到半截内容。读取失败时沿用上次生效值
// 并只在错误文本变化时记日志（输入法不能因为配置坏了就不能用）。
func (e *ibusEngine) associateEnabled() bool {
	if e.assocFlagSet || e.assocConfigPath == "" {
		return e.assocOn // 命令行显式覆盖，或没有配置文件路径：用启动值
	}
	cfg, err := loadUserConfig(e.assocConfigPath)
	if err != nil {
		if msg := err.Error(); msg != e.assocConfigErr {
			e.assocConfigErr = msg
			log.Printf("⚠ 读取联想设置失败（沿用上次值 %v）：%v", e.assocOn, err)
		}
		return e.assocOn
	}
	e.assocConfigErr = ""
	e.assocOn = cfg.AssociateEnabled()
	return e.assocOn
}

// enterAssociate 选词上屏后按上屏词查续接词组并经候选表展示。
//
// 2026-10-29 修复：此前把上屏段音节的**最后一个音节**当查询参数
// （enterAssociateForPrev(sylls[len(sylls)-1])），而引擎的剥前缀逻辑按
// 该参数的长度对齐——传 ["ni"] 查不着「你好」以外的多字续接，传
// 单音节查多字上屏词时更是直接把音节切错位（上屏「测试」→「题」被记成
// shí'tí）。音节基准应由引擎自己的上屏记录决定（engine 侧已改为
// s.prevSylls），壳这里只负责进入联想，参数传上屏词音节即可。
func (e *ibusEngine) enterAssociate(sylls []string) {
	if !e.associateEnabled() {
		return // 词组联想已被设置关闭（config.json associate=false）
	}
	e.assoc = nil
	e.enterAssociateForPrev(strings.Join(sylls, "'"))
}

// enterAssociateForPrev 按上屏词（字前缀）查续接联想：渐进上屏的剩余
// 耗尽收尾用。syllsOrWord 可为「撇号分隔的音节串」或上屏词文本——
// 引擎会优先用自己记录的上屏音节（s.prevSylls）作为切分基准，
// 因此传文本也不会造成音节错位（见 engine.Associate 注释）。
func (e *ibusEngine) enterAssociateForPrev(syllsOrWord string) {
	if syllsOrWord == "" || !e.associateEnabled() {
		return
	}
	cands := e.sess.Associate(strings.Split(syllsOrWord, "'"))
	if len(cands) == 0 {
		return
	}
	e.assoc = cands
	e.assocPage = 0
	e.showAssociateTable()
}

// showAssociateTable 展示联想当前页（本地分页：全量在 e.assoc，每页
// pageSize 条；2026-10-28 联想翻页化）。
func (e *ibusEngine) showAssociateTable() {
	per := engine.PageSize()
	total := len(e.assoc)
	if e.assocPage*per >= total {
		e.assocPage = total/per - 1
		if e.assocPage < 0 {
			e.assocPage = 0
		}
	}
	start := e.assocPage * per
	end := start + per
	if end > total {
		end = total
	}
	e.updateLookupTable(engine.Composition{
		Candidates: e.assoc[start:end],
		Page:       e.assocPage,
		PageCount:  (total + per - 1) / per,
	})
}

// flipAssociate 联想翻页（- / = / PageUp / PageDown）。返回是否切换了页。
func (e *ibusEngine) flipAssociate(next bool) bool {
	per := engine.PageSize()
	pageCount := (len(e.assoc) + per - 1) / per
	if next && e.assocPage+1 < pageCount {
		e.assocPage++
	} else if !next && e.assocPage > 0 {
		e.assocPage--
	} else {
		return false
	}
	e.showAssociateTable()
	return true
}

// exitAssociate 退出联想并收起联想候选表。
func (e *ibusEngine) exitAssociate() {
	if len(e.assoc) == 0 {
		return
	}
	e.assoc = nil
	e.assocPage = 0
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
	e.exitAssociate()
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
	e.exitAssociate()
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
	// 联想中：面板翻页箭头翻联想页（与 macOS 面板 onPageUp、联想态翻页键
	// 同一语义）。2026-10-29 修复：此前无条件翻组词页——联想挂着时端点
	// 箭头翻的是不可见的组词候选，联想窗纹丝不动（三端不一致）。
	if len(e.assoc) > 0 {
		e.flipAssociate(false)
		return nil
	}
	comp, _ := e.sess.PrevPage()
	e.refresh(comp)
	return nil
}

func (e *ibusEngine) PageDown() *godbus.Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 联想中：翻联想页（见 PageUp 注释）
	if len(e.assoc) > 0 {
		e.flipAssociate(true)
		return nil
	}
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
