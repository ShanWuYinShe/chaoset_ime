package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	godbus "github.com/godbus/dbus/v5"

	"chaoset.ime/engine"
)

// fakeEmitter 记录全部 Emit 调用，让面板行为（预编辑/候选表/上屏信号）
// 可以被断言，而不是只靠人工在容器里肉眼确认。godbus.Conn 无法在测试里
// 实例化（需要真实 session bus），这正是引入 emitter 接口的原因。
type fakeEmitter struct {
	mu      sync.Mutex
	emitted []emittedSignal
}

// emittedSignal 记录一次 Emit 的完整参数（类型名避开 os/signal 包冲突）。
// 记录 values 是为了断言可见性等参数——例如 hidePreedit 与正常预编辑更新
// 发的是同一个信号名 UpdatePreeditText，只数次数无法区分修复前后。
type emittedSignal struct {
	path   godbus.ObjectPath
	name   string
	values []interface{}
}

func (f *fakeEmitter) Emit(path godbus.ObjectPath, name string, values ...interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted = append(f.emitted, emittedSignal{path: path, name: name, values: values})
	return nil
}

func (f *fakeEmitter) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.emitted {
		if strings.Contains(s.name, name) {
			n++
		}
	}
	return n
}

// unwrapText 解包 godbus.Variant 取出其中的 ibusText 文本。
// 注意 Emit 的 values[0] 是 godbus.Variant（MakeVariant 的包装），必须先解包
// Variant 再断言 ibusText——直接断言会失败并表现为「取到空串」。
func unwrapText(v interface{}) (string, bool) {
	variant, ok := v.(godbus.Variant)
	if !ok {
		return "", false
	}
	it, ok := variant.Value().(ibusText)
	if !ok {
		return "", false
	}
	return it.Text, true
}

// lastPreedit 返回最后一次 UpdatePreeditText 的（文本, 可见）。
func (f *fakeEmitter) lastPreedit() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.emitted) - 1; i >= 0; i-- {
		s := f.emitted[i]
		if !strings.Contains(s.name, "UpdatePreeditText") {
			continue
		}
		text := ""
		if len(s.values) > 0 {
			text, _ = unwrapText(s.values[0])
		}
		visible := false
		if len(s.values) > 2 {
			if b, ok := s.values[2].(bool); ok {
				visible = b
			}
		}
		return text, visible
	}
	return "", false
}

// lastCommitText 返回最后一次 CommitText 的文本（无则空串）。
func (f *fakeEmitter) lastCommitText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.emitted) - 1; i >= 0; i-- {
		s := f.emitted[i]
		if !strings.Contains(s.name, "CommitText") {
			continue
		}
		if len(s.values) > 0 {
			if text, ok := unwrapText(s.values[0]); ok {
				return text
			}
		}
	}
	return ""
}

// newTestEngine 造一个挂 fakeEmitter 的引擎 + 真实词库（含「你好」「你」）。
func newTestEngine(t *testing.T) (*ibusEngine, *fakeEmitter) {
	t.Helper()
	src := "你好\tni hao\t90\n你\tni\t80\n"
	d, err := engine.ParseDict(strings.NewReader(src))
	if err != nil {
		t.Fatalf("构造测试词库失败: %v", err)
	}
	fe := &fakeEmitter{}
	e := &ibusEngine{conn: fe, dict: d, user: nil, sess: engine.NewSession(d, nil), assocOn: true}
	return e, fe
}

// 辅助：模拟敲一串小写字母（逐键）。
func typeKeys(e *ibusEngine, text string) {
	for _, ch := range text {
		e.ProcessKeyEvent(uint32(ch), 0, 0)
	}
}

// TestCommitEmitsSignals：组词 → 空格上屏，应发出 CommitText 并收起
// 预编辑与候选表。锁提升（ProcessKeyEvent 全程持 e.mu）不得改变信号行为。
func TestCommitEmitsSignals(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "nihao")
	_, gerr := e.ProcessKeyEvent(keysymSpace, 0, 0)
	if gerr != nil {
		t.Fatalf("空格上屏出错: %v", gerr)
	}
	if got := fe.count("CommitText"); got != 1 {
		t.Fatalf("CommitText 应恰好 1 次，实际 %d", got)
	}
	if got := fe.count("UpdatePreeditText"); got < 2 {
		t.Logf("UpdatePreeditText 调用 %d 次（组词 1 次 + 收起 1 次）", got)
	}
}

// TestConcurrentMethodsRace：并发调用 ProcessKeyEvent / PageUp / PageDown /
// Reset / FocusOut，在 -race 下验证全程持锁。
//
// 回归背景（2026-10-26 审查发现）：ProcessKeyEvent 的 Shift/enMode/Ctrl+Backspace
// 分支此前在 e.mu 之外读写共享状态，而 godbus 每个方法调用一个 goroutine，
// 与加锁的 PageUp 构成真数据竞争。修复后整个函数持锁。
func TestConcurrentMethodsRace(t *testing.T) {
	e, _ := newTestEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				switch j % 5 {
				case 0:
					typeKeys(e, "ni")
				case 1:
					_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0)
				case 2:
					_, _ = e.ProcessKeyEvent(keysymBackspace, 0, 0)
				case 3:
					_ = e.PageUp()
					_ = e.PageDown()
				case 4:
					_ = e.Reset()
					_ = e.FocusOut()
					_ = e.FocusIn()
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestPageKeyRequiresCandidates：翻页键只在「组词中且有候选」时消费。
// 残缺输入（无候选）时按 - 必须放行（此前被静默吞掉）；[ ] 此时应落到
// 标点分支输出【】——这是 Linux 上打不出【】的根因。
func TestPageKeyRequiresCandidates(t *testing.T) {
	e, fe := newTestEngine(t)
	// z 是声母：zzz 无法构成完整音节，引擎给出行不行…直接断言零候选，
	// 若该词库下 zz 有候选（纠错/简拼命中）则换明确无法命中的串。
	typeKeys(e, "zzz")
	if hasCandidates(e.sess) {
		t.Skip("该词库下 zzz 有候选，用例前提不成立")
	}
	handled, _ := e.ProcessKeyEvent(uint32('-'), 0, 0)
	if handled {
		t.Fatal("无候选时 - 不应被消费（此前被静默吞掉）")
	}
	handled, _ = e.ProcessKeyEvent(uint32('['), 0, 0)
	if !handled {
		t.Fatal("[ 无候选时应落到标点分支被消费")
	}
	if got := fe.count("CommitText"); got != 1 {
		t.Fatalf("[ 应上屏【，CommitText 实际 %d 次", got)
	}
	if got := fe.lastCommitText(); got != "【" {
		t.Fatalf("[ 无候选时应输出【，实际 %q", got)
	}
}

// TestPreeditVisibleWithoutCandidates：残缺输入（preedit 非空、零候选）
// 时预编辑必须可见——否则用户敲了字母却什么都看不见。
// 回归背景：可见性曾绑在「有无候选」上（refresh → updatePreedit 的第二参）。
func TestPreeditVisibleWithoutCandidates(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "zzz")
	if hasCandidates(e.sess) {
		t.Skip("该词库下 zzz 有候选，用例前提不成立")
	}
	before := fe.count("UpdatePreeditText")
	e.ProcessKeyEvent(uint32('z'), 0, 0)
	if n := fe.count("UpdatePreeditText"); n <= before {
		t.Fatalf("残缺输入后应刷新预编辑（before=%d after=%d）", before, n)
	}
	text, visible := fe.lastPreedit()
	if !visible {
		t.Fatal("残缺输入时预编辑应可见（此前可见性绑在「有无候选」上，用户敲了字母却看不见）")
	}
	if text == "" {
		t.Fatal("可见预编辑的文本不应为空")
	}
	t.Logf("残缺输入预编辑: %q (visible=%v)", text, visible)
}

// 候选表 page_size 必须与引擎导出的每页容量一致（防两份真相漂移）。
func TestLookupTablePageSizeMatchesEngine(t *testing.T) {
	want := engine.PageSize()
	if ibusPageSize != want {
		t.Fatalf("ibusPageSize=%d 与 engine.PageSize()=%d 不一致", ibusPageSize, want)
	}
}

// 候选序号必须是 1..N 递增，不能 %10 回绕成 0（第 10 项会变成 "0." 且选不到）。
func TestLookupTableLabelsDoNotWrap(t *testing.T) {
	var cands []engine.Candidate
	for i := 1; i <= 11; i++ {
		cands = append(cands, engine.Candidate{Text: fmt.Sprintf("词%d", i), Score: i})
	}
	comp := engine.Composition{Input: "test", Preedit: "test", Candidates: cands}
	table := newIBusLookupTable(comp)

	labels, ok := table.Labels.Value().([]godbus.Variant)
	if !ok {
		t.Fatalf("labels 类型异常: %T", table.Labels.Value())
	}
	if len(labels) != len(cands) {
		t.Fatalf("标签数 %d 与候选数 %d 不一致", len(labels), len(cands))
	}
	for i, v := range labels {
		txt, ok := v.Value().(ibusText)
		if !ok {
			t.Fatalf("第 %d 个标签类型异常: %T", i, v.Value())
		}
		want := fmt.Sprintf("%d.", i+1)
		if txt.Text != want {
			t.Fatalf("第 %d 个标签应为 %q，实际 %q（序号回绕会导致该项选不到）",
				i, want, txt.Text)
		}
	}
}

// FocusOut 必须清空组词状态（跨窗口不再残留）。
func TestFocusOutClearsComposition(t *testing.T) {
	e, _ := newTestEngine(t)
	typeKeys(e, "ni")
	if got := e.sess.Compose().Input; got != "ni" {
		t.Fatalf("前置条件失败：组词应为 ni，实际 %q", got)
	}
	_ = e.FocusOut()
	if got := e.sess.Compose().Input; got != "" {
		t.Fatalf("FocusOut 后组词应清空，实际 %q", got)
	}
}

// FocusIn 必须重置 Shift 空按跟踪（跨窗口不再误判中英切换）。
func TestFocusInResetsShiftTracker(t *testing.T) {
	e, _ := newTestEngine(t)
	e.shift.shiftDown = 1 // 模拟「Shift 按下未释放」就切走了窗口
	_ = e.FocusIn()
	if e.shift.shiftDown != 0 || e.shift.sinceShift {
		t.Fatalf("FocusIn 应重置 shift 跟踪，实际 shiftDown=%d sinceShift=%v",
			e.shift.shiftDown, e.shift.sinceShift)
	}
}

// ---- 2026-10-27 三端交互一致性修复（对齐 macOS 壳行为）----

// TestShiftReleaseNotConsumed：Shift 空按切换中英后，释放事件必须放行。
// 应用已收到 Shift 按下，吞掉释放会让依赖修饰键状态机的应用
// （Shift+点击范围选择等）认为 Shift 一直按着。
func TestShiftReleaseNotConsumed(t *testing.T) {
	e, _ := newTestEngine(t)
	e.ProcessKeyEvent(keysymShiftLeft, 50, 0) // press
	handled, _ := e.ProcessKeyEvent(keysymShiftLeft, 50, ibusReleaseMask)
	if handled {
		t.Fatal("Shift 空按释放不应被消费（宿主应用需要完整的按下/释放对）")
	}
	if !e.enMode {
		t.Fatal("Shift 空按应已切换到英文模式")
	}
}

// TestSpaceCommitsRawWithoutCandidates：残缺组词按空格——顶屏原始拼音并
// 消费空格。此前直接放行：裸空格进文档、看不见的活组词继续挂着（与
// 回车分支行为分裂，macOS 2026-10-25 实机反馈的同族问题）。
func TestSpaceCommitsRawWithoutCandidates(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "zzz")
	if hasCandidates(e.sess) {
		t.Skip("该词库下 zzz 有候选，用例前提不成立")
	}
	handled, _ := e.ProcessKeyEvent(keysymSpace, 0, 0)
	if !handled {
		t.Fatal("组词中空格应被消费（裸空格不应漏进文档）")
	}
	if got := fe.lastCommitText(); got != "zzz" {
		t.Fatalf("残缺组词按空格应顶屏原始拼音 zzz，实际 %q", got)
	}
	if got := e.sess.Compose().Input; got != "" {
		t.Fatalf("空格后组词应清空，实际 %q", got)
	}
}

// TestOutOfRangeDigitKeepsComposition：越界数字（超出本页候选数）不消费、
// 组词保持原样。此前隐藏预编辑/候选表但 session 仍在组词——拼音从屏幕
// 消失、数字漏进文档，下一次击键组词又「复活」。
func TestOutOfRangeDigitKeepsComposition(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "ni")
	if len(e.sess.Compose().Candidates) == 0 {
		t.Fatal("前置条件失败：ni 应有候选")
	}
	before := fe.count("CommitText")
	handled, _ := e.ProcessKeyEvent(uint32('9'), 0, 0)
	if handled {
		t.Fatal("越界数字不应被消费（应原样进文档）")
	}
	if got := fe.count("CommitText"); got != before {
		t.Fatalf("越界数字不应上屏，CommitText 前后 %d→%d", before, got)
	}
	if got := e.sess.Compose().Input; got != "ni" {
		t.Fatalf("越界数字后组词应保持原样，实际 %q", got)
	}
	_, visible := fe.lastPreedit()
	if !visible {
		t.Fatal("越界数字后预编辑应保持可见（此前被隐藏，用户以为输入被取消）")
	}
}

// TestKeypadDigitSelectsCandidate：小键盘 KP_1..KP_9 同样选候选（此前
// 完全未处理，数字漏进文档并叠加组词不清）。
func TestKeypadDigitSelectsCandidate(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "nihao")
	handled, _ := e.ProcessKeyEvent(keysymKP1, 0, 0) // KP_1
	if !handled {
		t.Fatal("KP_1 应被消费")
	}
	if got := fe.lastCommitText(); got != "你好" {
		t.Fatalf("KP_1 应选首选「你好」，实际 %q", got)
	}
}

// TestUnhandledKeyClearsComposition：组词中按未处理的键（方向键等）——
// 先清空组词再放行。此前直接放行：字符进文档的同时预编辑挂在原地，
// 光标移走后预编辑与实文脱节。
func TestUnhandledKeyClearsComposition(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "ni")
	before := fe.count("CommitText")
	handled, _ := e.ProcessKeyEvent(0xFF51, 0, 0) // Left
	if handled {
		t.Fatal("方向键应放行给宿主应用")
	}
	if got := e.sess.Compose().Input; got != "" {
		t.Fatalf("放行后组词应清空，实际 %q", got)
	}
	if got := fe.count("CommitText"); got != before {
		t.Fatalf("不应误触上屏，CommitText 前后 %d→%d", before, got)
	}
}

// ---- 2026-10-27 第二批：预编辑标记与模式反馈 ----

// lastPreeditCursor 取最后一次可见 UpdatePreeditText 的 cursor 参数。
func (f *fakeEmitter) lastPreeditCursor() (uint32, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.emitted) - 1; i >= 0; i-- {
		s := f.emitted[i]
		if !strings.Contains(s.name, "UpdatePreeditText") {
			continue
		}
		if len(s.values) > 2 {
			if b, ok := s.values[2].(bool); ok && b {
				cursor := uint32(0)
				if len(s.values) > 1 {
					if u, ok := s.values[1].(uint32); ok {
						cursor = u
					}
				}
				return cursor, true
			}
		}
	}
	return 0, false
}

// TestPreeditUnderlineAndCursor：预编辑必须带整串下划线属性（否则应用把
// 预编辑渲染成与已提交文本无差别的裸文本），cursor 必须指向文本末尾
// （此前恒为 0，内联光标画在拼音串开头）。
func TestPreeditUnderlineAndCursor(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "nihao")
	cursor, ok := fe.lastPreeditCursor()
	if !ok {
		t.Fatal("组词后应发出可见的 UpdatePreeditText")
	}
	const want = "ni'hao"
	if cursor != uint32(utf8.RuneCountInString(want)) {
		t.Fatalf("预编辑 cursor 应指向末尾（%d），实际 %d", utf8.RuneCountInString(want), cursor)
	}
	e.mu.Lock()
	var last ibusText
	for i := len(fe.emitted) - 1; i >= 0; i-- {
		s := fe.emitted[i]
		if !strings.Contains(s.name, "UpdatePreeditText") {
			continue
		}
		if text, ok := unwrapText(s.values[0]); ok && text != "" {
			last = s.values[0].(godbus.Variant).Value().(ibusText)
			break
		}
	}
	e.mu.Unlock()
	var attrs ibusAttrList
	if v, ok := last.Attrs.Value().(ibusAttrList); ok {
		attrs = v
	} else {
		t.Fatalf("IBusText.Attrs 应为 IBusAttrList 变体，实际 %T", last.Attrs.Value())
	}
	if len(attrs.Attrs) == 0 {
		t.Fatal("预编辑应携带下划线属性（裸文本让应用无法分辨正在组词）")
	}
	a, ok := attrs.Attrs[0].Value().(ibusAttribute)
	if !ok {
		t.Fatalf("属性元素应为 IBusAttribute 变体，实际 %T", attrs.Attrs[0].Value())
	}
	if a.AttrType != 1 || a.Value != 1 || a.StartIndex != 0 {
		t.Fatalf("应为整串单线下划线（type=1 value=1 start=0），实际 %+v", a)
	}
	if a.EndIndex != uint32(utf8.RuneCountInString(want)) {
		t.Fatalf("下划线应覆盖到末尾 %d，实际 %d", utf8.RuneCountInString(want), a.EndIndex)
	}
}

// lastAuxiliary 取最后一次 UpdateAuxiliaryText 的（文本, 可见）。
func (f *fakeEmitter) lastAuxiliary() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.emitted) - 1; i >= 0; i-- {
		s := f.emitted[i]
		if !strings.Contains(s.name, "UpdateAuxiliaryText") {
			continue
		}
		text, _ := unwrapText(s.values[0])
		visible := false
		if len(s.values) > 1 {
			if b, ok := s.values[1].(bool); ok {
				visible = b
			}
		}
		return text, visible
	}
	return "", false
}

// TestModeToastOnShiftToggle：Shift 空按切换中英后，辅助文本应短暂显示
// 目标模式——此前切换零反馈，用户只能靠打字试探。
func TestModeToastOnShiftToggle(t *testing.T) {
	e, fe := newTestEngine(t)
	e.ProcessKeyEvent(keysymShiftLeft, 50, 0) // press
	e.ProcessKeyEvent(keysymShiftLeft, 50, ibusReleaseMask)
	text, visible := fe.lastAuxiliary()
	// 文案三端统一为「英文模式」（macOS/Windows 同口径；2026-10-29 审查）
	if !visible || text != "英文模式" {
		t.Fatalf("切英文后应提示「英文模式」，实际 visible=%v text=%q", visible, text)
	}
}

// TestHalfWidthToastOnShiftSpace：Shift+空格切全/半角后应短暂提示当前
// 半角状态（ToggleHalfWidth 返回 true = 已进入半角）。
func TestHalfWidthToastOnShiftSpace(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "ni")
	// 先人工切到全角态（奇数次往返），保证按键后必然进入半角
	for e.sess.ToggleHalfWidth() {
	}
	handled, _ := e.ProcessKeyEvent(keysymSpace, 0, ibusShiftMask)
	if !handled {
		t.Fatal("Shift+空格应被消费")
	}
	text, visible := fe.lastAuxiliary()
	if !visible || text != "半角标点" {
		t.Fatalf("切半角后应提示「半角标点」，实际 visible=%v text=%q", visible, text)
	}
}

// ---- 2026-10-27 第三批：词组联想接入 Linux（对齐 macOS 壳）----

// lastLookupCandidates 取最后一次可见 UpdateLookupTable 的候选文本列表。
func (f *fakeEmitter) lastLookupCandidates() ([]string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.emitted) - 1; i >= 0; i-- {
		s := f.emitted[i]
		if !strings.Contains(s.name, "UpdateLookupTable") {
			continue
		}
		visible := false
		if len(s.values) > 1 {
			if b, ok := s.values[1].(bool); ok {
				visible = b
			}
		}
		if !visible {
			continue
		}
		table, ok := s.values[0].(godbus.Variant)
		if !ok {
			return nil, false
		}
		lt, ok := table.Value().(ibusLookupTable)
		if !ok {
			return nil, false
		}
		candsV, ok := lt.Candidates.Value().([]godbus.Variant)
		if !ok {
			return nil, false
		}
		var out []string
		for _, v := range candsV {
			if it, ok := v.Value().(ibusText); ok {
				out = append(out, it.Text)
			}
		}
		return out, true
	}
	return nil, false
}

// newAssociateTestEngine：含「ni」续接词组的词库（上屏「你」后，
// 以 ni 开头的「你好/你们」都是续接候选）。
func newAssociateTestEngine(t *testing.T) (*ibusEngine, *fakeEmitter) {
	t.Helper()
	src := "你好\tni hao\t90\n你\tni\t80\n你们\tni men\t70\n"
	d, err := engine.ParseDict(strings.NewReader(src))
	if err != nil {
		t.Fatalf("构造测试词库失败: %v", err)
	}
	fe := &fakeEmitter{}
	e := &ibusEngine{conn: fe, dict: d, user: nil, sess: engine.NewSession(d, nil), assocOn: true}
	return e, fe
}

// TestAssociateFlow：选词上屏 → 联想候选表 → 数字连选 → 续接耗尽退出；
// 以及字母退出联想进入正常组词。
func TestAssociateFlow(t *testing.T) {
	e, fe := newAssociateTestEngine(t)

	// 组词 ni → 空格上屏首选「你」（单音节输入：音节数一致优先）
	typeKeys(e, "ni")
	handled, _ := e.ProcessKeyEvent(keysymSpace, 0, 0)
	if !handled {
		t.Fatal("空格应被消费")
	}
	if got := fe.lastCommitText(); got != "你" {
		t.Fatalf("空格应上屏首选「你」，实际 %q", got)
	}
	if len(e.assoc) == 0 {
		t.Fatal("上屏「你」后应进入联想（词库含 ni 开头的更长词条）")
	}
	cands, visible := fe.lastLookupCandidates()
	if !visible {
		t.Fatal("联想候选表应可见")
	}
	if len(cands) == 0 {
		t.Fatal("联想候选表不应为空")
	}
	// 联想列表应含以 ni 开头的续接词且不含刚上屏的「你」本身
	for _, c := range cands {
		if c == "你" {
			t.Fatalf("联想列表不应含刚上屏的词本身，实际 %v", cands)
		}
	}

	// 数字 1 连选联想首选（词频 90「你好」> 70「你们」，应为「你好」），
	// 上屏后继续查 ni'hao 续接——词库无更深层词条，联想应结束
	handled, _ = e.ProcessKeyEvent(uint32('1'), 0, 0)
	if !handled {
		t.Fatal("联想中数字 1 应被消费")
	}
	if got := fe.lastCommitText(); got != cands[0] {
		t.Fatalf("数字 1 应上屏联想首选 %q，实际 %q", cands[0], got)
	}
	if len(e.assoc) != 0 {
		t.Fatalf("续接耗尽后联想应结束，实际 %v", e.assoc)
	}

	// 第二轮：字母退出联想进入正常组词
	typeKeys(e, "ni")
	handled, _ = e.ProcessKeyEvent(keysymSpace, 0, 0)
	if !handled || len(e.assoc) == 0 {
		t.Fatalf("前置失败：应再次进入联想（assoc=%d）", len(e.assoc))
	}
	handled, _ = e.ProcessKeyEvent(uint32('a'), 0, 0)
	if !handled {
		t.Fatal("联想中字母键应被消费（进入正常组词）")
	}
	if len(e.assoc) != 0 {
		t.Fatal("字母键应退出联想")
	}
	if got := e.sess.Compose().Input; got != "a" {
		t.Fatalf("字母应进入正常组词，实际输入 %q", got)
	}
}

// TestAssociateEscapeAndPageKeys：联想中 Esc/-/= 的收起语义。
func TestAssociateEscapeAndPageKeys(t *testing.T) {
	e, _ := newAssociateTestEngine(t)
	typeKeys(e, "ni")
	_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0)
	if len(e.assoc) == 0 {
		t.Fatal("前置失败：应进入联想")
	}
	// 2026-10-28 翻页化：- / = 变为联想翻页（消费），不再收起联想
	before := e.assocPage
	handled, _ := e.ProcessKeyEvent(uint32('-'), 0, 0)
	if !handled {
		t.Fatal("联想中 - 应被消费（翻上一页）")
	}
	if len(e.assoc) == 0 {
		t.Fatal("- 不应收起联想")
	}
	if e.assocPage != before {
		t.Fatalf("首页按 - 应保持第 0 页（钳制），实际 %d", e.assocPage)
	}
	handled, _ = e.ProcessKeyEvent(uint32('='), 0, 0)
	if !handled {
		t.Fatal("联想中 = 应被消费（翻下一页）")
	}
	// 重新进入联想后 Esc 消费并收起
	typeKeys(e, "ni")
	_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0)
	handled, _ = e.ProcessKeyEvent(keysymEscape, 0, 0)
	if !handled {
		t.Fatal("联想中 Esc 应被消费")
	}
	if len(e.assoc) != 0 {
		t.Fatal("Esc 应收起联想")
	}
	if got := e.sess.Compose().Input; got != "" {
		t.Fatalf("Esc 后组词应为空，实际 %q", got)
	}
}

// ---- 2026-10-27 第四批：三端一致性别项（对齐 macOS 壳）----

// TestShiftBackspaceClearsAll：Shift+Backspace 直接全部删除（对齐 macOS 壳
// 把 Shift+Delete 归入全部删除；此前走逐字符回退，三端不一致）。
func TestShiftBackspaceClearsAll(t *testing.T) {
	e, fe := newTestEngine(t)
	typeKeys(e, "nihao")
	if got := e.sess.Compose().Input; got != "nihao" {
		t.Fatalf("前置失败：组词应为 nihao，实际 %q", got)
	}
	handled, _ := e.ProcessKeyEvent(keysymBackspace, 0, ibusShiftMask)
	if !handled {
		t.Fatal("组词中 Shift+Backspace 应被消费")
	}
	if got := e.sess.Compose().Input; got != "" {
		t.Fatalf("Shift+Backspace 应全部删除，实际剩余 %q", got)
	}
	if _, visible := fe.lastPreedit(); visible {
		t.Fatal("全部删除后预编辑应收起")
	}
}

// TestCtrlUClearsComposition：Ctrl+U 类终端整行清空（对齐 macOS 壳，
// 组词中消费并全部删除；此前直接放行，控制字符可能进文档）。
func TestCtrlUClearsComposition(t *testing.T) {
	e, _ := newTestEngine(t)
	typeKeys(e, "nihao")
	handled, _ := e.ProcessKeyEvent(uint32('u'), 0, ibusCtrlMask)
	if !handled {
		t.Fatal("组词中 Ctrl+U 应被消费")
	}
	if got := e.sess.Compose().Input; got != "" {
		t.Fatalf("Ctrl+U 应清空组词，实际 %q", got)
	}
	// 非组词时 Ctrl+U 照旧放行
	handled, _ = e.ProcessKeyEvent(uint32('u'), 0, ibusCtrlMask)
	if handled {
		t.Fatal("非组词 Ctrl+U 应放行给应用")
	}
}

// TestPageKeyClearsCompositionWithoutCandidates：残缺组词按 PgUp/PgDn——
// 先清空组词再放行（对齐 macOS/Windows 壳；此前静默吞键）。
func TestPageKeyClearsCompositionWithoutCandidates(t *testing.T) {
	e, _ := newTestEngine(t)
	typeKeys(e, "zzz")
	if hasCandidates(e.sess) {
		t.Skip("该词库下 zzz 有候选，用例前提不成立")
	}
	handled, _ := e.ProcessKeyEvent(keysymPageDown, 0, 0)
	if handled {
		t.Fatal("无候选时 PgDn 不应被消费（宿主应正常滚动）")
	}
	if got := e.sess.Compose().Input; got != "" {
		t.Fatalf("放行前组词应清空，实际 %q", got)
	}
}

// TestAssociatePaging：联想翻页化——上限 30 条分页展示，`=` 翻页后数字键
// 选到第 2 页的词（翻页前该词永远选不到）。
func TestAssociatePaging(t *testing.T) {
	tails := []string{"hao", "men", "de", "shuo", "kan", "xiang", "yao", "neng",
		"hui", "qu", "lai", "zuo", "xie", "gei", "zhao", "wen", "ting", "jiao",
		"dai", "ban", "pan", "wan", "tan", "man", "zhan", "guan", "chuan",
		"zhuan", "duan", "ruan", "suan", "nuan", "luan", "kuan", "huan", "tuan",
		"duo", "hai", "hei", "hun"}
	var src strings.Builder
	src.WriteString("你\tni\t95\n") // 单字「你」须是 ni 首选，上屏后按 ni 查续接
	for i, tail := range tails {
		fmt.Fprintf(&src, "你%s\tni %s\t%d\n", tail, tail, 90-i)
	}
	d, err := engine.ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	fe := &fakeEmitter{}
	e := &ibusEngine{conn: fe, dict: d, user: nil, sess: engine.NewSession(d, nil), assocOn: true}

	typeKeys(e, "ni")
	_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0)
	if len(e.assoc) != 30 {
		t.Fatalf("联想应返回上限 30 条，实际 %d", len(e.assoc))
	}
	// 第 1 页展示 6 条
	cands, visible := fe.lastLookupCandidates()
	if !visible || len(cands) != 6 {
		t.Fatalf("联想第 1 页应展示 6 条，实际 visible=%v len=%d", visible, len(cands))
	}
	// = 翻到第 2 页
	if _, gerr := e.ProcessKeyEvent(uint32('='), 0, 0); gerr != nil {
		t.Fatalf("= 翻页出错: %v", gerr)
	}
	cands2, _ := fe.lastLookupCandidates()
	if len(cands2) != 6 || cands2[0] == cands[0] {
		t.Fatalf("翻页后应展示第 2 页切片（首条 %q ≠ %q），实际 %v", cands[0], cands2[0], cands2)
	}
	// 数字 1 在第 2 页应选中全局第 7 条（enterAssociate 会清空 assoc，先存引用）
	want := e.assoc[e.assocPage*6]
	_, _ = e.ProcessKeyEvent(uint32('1'), 0, 0)
	if got := fe.lastCommitText(); got != want.Text {
		t.Fatalf("第 2 页数字 1 应选全局第 7 条 %q，实际上屏 %q", want.Text, got)
	}
}

// TestAssociatePagingKeySets：联想态的翻页键集合必须与组词态完全一致——
// - / =、[ / ]、PageUp / PageDown 六键都翻联想页并消费。
//
// 2026-10-29 三端一致性审查：联想态此前只认 - / =，其余键落到「退出联想 +
// 透传」：文档滚动了而联想窗收起（Linux/Windows），macOS 更糟——不清理
// associateCandidates 就穿到 passThrough，面板挂在屏幕上不消失。三端行为
// 各不相同，这条测试锁死统一语义。
func TestAssociatePagingKeySets(t *testing.T) {
	tails := []string{"hao", "men", "de", "shuo", "kan", "xiang", "yao", "neng",
		"hui", "qu", "lai", "zuo", "xie", "gei", "zhao", "wen", "ting", "jiao",
		"dai", "ban", "pan", "wan", "tan", "man", "zhan", "guan", "chuan",
		"zhuan", "duan", "ruan", "suan", "nuan", "luan", "kuan", "huan", "tuan",
		"duo", "hai", "hei", "hun"}
	var src strings.Builder
	src.WriteString("你	ni	95\n")
	for i, tail := range tails {
		fmt.Fprintf(&src, "你%s	ni %s	%d\n", tail, tail, 90-i)
	}
	d, err := engine.ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	cases := []struct {
		name   string
		keyval uint32
		next   bool
	}{
		{"=", uint32('='), true},
		{"]", uint32(']'), true},
		{"PageDown", keysymPageDown, true},
		{"-", uint32('-'), false},
		{"[", uint32('['), false},
		{"PageUp", keysymPageUp, false},
	}
	for _, c := range cases {
		fe := &fakeEmitter{}
		e := &ibusEngine{conn: fe, dict: d, user: nil, sess: engine.NewSession(d, nil), assocOn: true}
		typeKeys(e, "ni")
		_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0)
		if len(e.assoc) != 30 {
			t.Fatalf("%s: 前置失败，联想应 30 条，实际 %d", c.name, len(e.assoc))
		}
		first, _ := fe.lastLookupCandidates()
		handled, _ := e.ProcessKeyEvent(c.keyval, 0, 0)
		if !handled {
			t.Fatalf("%s: 联想态翻页键应被消费（此前会透传给宿主）", c.name)
		}
		if len(e.assoc) == 0 {
			t.Fatalf("%s: 联想态翻页键不应退出联想", c.name)
		}
		got, _ := fe.lastLookupCandidates()
		if c.next {
			if len(got) == 0 || got[0] == first[0] {
				t.Fatalf("%s: 应翻到下一页（首条 %q 未变）", c.name, first[0])
			}
		} else if len(got) == 0 || got[0] != first[0] {
			t.Fatalf("%s: 首页按上一页键应保持首页（首条 %q → %q）", c.name, first[0], got[0])
		}
	}
}

// TestAssociatePanelArrowFlips：联想中面板翻页箭头（ibus PageUp/PageDown
// 回调）必须翻**联想页**，不是翻不可见的组词页。
// 2026-10-29 三端一致性审查：此前无条件翻组词页——联想窗挂着时点箭头
// 纹丝不动（macOS 面板箭头在联想态走 associateFlip，Windows 同理）。
func TestAssociatePanelArrowFlips(t *testing.T) {
	tails := []string{"hao", "men", "de", "shuo", "kan", "xiang", "yao", "neng",
		"hui", "qu", "lai", "zuo", "xie", "gei", "zhao", "wen", "ting", "jiao",
		"dai", "ban", "pan", "wan", "tan", "man", "zhan", "guan", "chuan",
		"zhuan", "duan", "ruan", "suan", "nuan", "luan", "kuan", "huan", "tuan",
		"duo", "hai", "hei", "hun"}
	var src strings.Builder
	src.WriteString("你	ni	95\n")
	for i, tail := range tails {
		fmt.Fprintf(&src, "你%s	ni %s	%d\n", tail, tail, 90-i)
	}
	d, err := engine.ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	fe := &fakeEmitter{}
	e := &ibusEngine{conn: fe, dict: d, user: nil, sess: engine.NewSession(d, nil), assocOn: true}
	typeKeys(e, "ni")
	_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0)
	if len(e.assoc) != 30 {
		t.Fatalf("前置失败，联想应 30 条，实际 %d", len(e.assoc))
	}
	first, _ := fe.lastLookupCandidates()
	_ = e.PageDown()
	if e.assocPage != 1 {
		t.Fatalf("联想中 PageDown 回调应翻到联想第 2 页，assocPage=%d", e.assocPage)
	}
	got, _ := fe.lastLookupCandidates()
	if len(got) == 0 || got[0] == first[0] {
		t.Fatalf("联想中 PageDown 后应展示联想第 2 页（首条 %q 未变）", first[0])
	}
	_ = e.PageUp()
	if e.assocPage != 0 {
		t.Fatalf("联想中 PageUp 回调应回到联想第 1 页，assocPage=%d", e.assocPage)
	}
}

// TestAssociateDisabledBySetting：config associate=false 时选词上屏后
// 不进入联想（设置项生效，2026-10-28）。
func TestAssociateDisabledBySetting(t *testing.T) {
	var src strings.Builder
	src.WriteString("你好\tni hao\t90\n你\tni\t80\n你们\tni men\t70\n")
	d, err := engine.ParseDict(strings.NewReader(src.String()))
	if err != nil {
		t.Fatalf("构造词库失败: %v", err)
	}
	fe := &fakeEmitter{}
	e := &ibusEngine{conn: fe, dict: d, user: nil, sess: engine.NewSession(d, nil), assocOn: false}
	typeKeys(e, "ni")
	_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0) // 上屏首选「你」
	if got := fe.lastCommitText(); got != "你" {
		t.Fatalf("上屏应正常（联想关闭不影响选词），实际 %q", got)
	}
	if len(e.assoc) != 0 {
		t.Fatalf("联想关闭时不应进入联想，实际 %d 条", len(e.assoc))
	}
}

// TestAssociateSpacePicksFirst：联想挂着时按空格应**默认选第一个联想词
// 上屏**（渐进组句闭环；此前是透传空格 + 收起联想）。
func TestAssociateSpacePicksFirst(t *testing.T) {
	e, fe := newAssociateTestEngine(t)
	typeKeys(e, "ni")
	_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0) // 上屏「你」，进入联想
	if len(e.assoc) == 0 {
		t.Fatal("前置失败：应进入联想")
	}
	handled, _ := e.ProcessKeyEvent(keysymSpace, 0, 0)
	if !handled {
		t.Fatal("联想中空格应被消费")
	}
	// 第一个联想词是「你好」的**续接部分「好」**（好 90 > 们 70）。
	// 2026-10-29 引擎语义修正（95adb9f）：联想候选只含续接部分，上屏「你」
	// 之后联想项是「好」而非「你好」——原断言按剥前缀之前的口径写成
	// 「你好」，引擎改了语义而该测试没同步，CI 从 95adb9f 起一直红。
	if got := fe.lastCommitText(); got != "好" {
		t.Fatalf("空格应上屏第一个联想词（续接部分「好」），实际 %q", got)
	}
}

// TestAssociateKeepsSyllablesAligned：上屏多字词后，联想项的音节必须与
// 上屏词对齐剥前缀——不能把上屏词**文本**当音节数组的长度基准。
// 2026-10-29 回归：Linux 壳 enterAssociate 曾传音节串末位（或上屏文本），
// 引擎按该参数长度切音节，导致「测试」→「题」被记成 shí'tí（字数 1 而
// 音节 2），学习数据被污染。
func TestAssociateKeepsSyllablesAligned(t *testing.T) {
	src := `测试	ce shi	80
测试题	ce shi ti	70
`
	d, err := engine.ParseDict(strings.NewReader(src))
	if err != nil {
		t.Fatalf("构造测试词库失败: %v", err)
	}
	fe := &fakeEmitter{}
	e := &ibusEngine{conn: fe, dict: d, user: nil, sess: engine.NewSession(d, nil), assocOn: true}
	typeKeys(e, "ceshi")
	_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0) // 上屏「测试」，进入联想
	if len(e.assoc) == 0 {
		t.Fatal("前置失败：上屏「测试」后应进入联想")
	}
	pick := e.assoc[0]
	if pick.Text != "题" {
		t.Fatalf("联想首选应是续接部分「题」（测试题剥前缀），实际 %q", pick.Text)
	}
	// 音节必须严格对齐：单字「题」只应有 1 个音节 ti，不能是多音节的残片
	if pick.Syllables != "ti" {
		t.Fatalf("联想项音节应与续接部分对齐为「ti」，实际 %q", pick.Syllables)
	}
	// 选中后学习记录的音节同样必须对齐（污染检查）
	e.ProcessKeyEvent(keysymSpace, 0, 0)
	if got := fe.lastCommitText(); got != "题" {
		t.Fatalf("空格应上屏续接词「题」，实际 %q", got)
	}
}

// TestAssociateBackspaceConsumed：联想挂着时按 Backspace 应收起联想并
// 消费（透传会误删刚上屏的文字）。
func TestAssociateBackspaceConsumed(t *testing.T) {
	e, fe := newAssociateTestEngine(t)
	typeKeys(e, "ni")
	_, _ = e.ProcessKeyEvent(keysymSpace, 0, 0)
	if len(e.assoc) == 0 {
		t.Fatal("前置失败：应进入联想")
	}
	before := fe.count("CommitText")
	handled, _ := e.ProcessKeyEvent(keysymBackspace, 0, 0)
	if !handled {
		t.Fatal("联想中 Backspace 应被消费（透传会误删刚上屏的字）")
	}
	if len(e.assoc) != 0 {
		t.Fatal("Backspace 应收起联想")
	}
	if fe.count("CommitText") != before {
		t.Fatal("收起联想不应产生新的上屏")
	}
}
