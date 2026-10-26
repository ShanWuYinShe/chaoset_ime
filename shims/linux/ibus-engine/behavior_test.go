package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"

	"chaoset.ime/engine"
)

// 这些测试补上 ibus 壳此前几乎为零的覆盖（审查实测仅 6.5%，且只测了
// shiftTracker）。重点锁住 2026-10-26 修复的几处行为：
//   - FocusOut 必须清组词（防跨窗口状态残留）
//   - FocusIn 必须重置 Shift 空按跟踪（防跨窗口误判中英切换）
//   - 候选表 page_size 必须与引擎一致（防两份真相漂移）
//   - 候选序号不能回绕成 0（第 10 项会永远选不到）

// newTestEngine 造一个不连真实 dbus 的引擎。注意 conn 为 nil，任何 Emit
// 路径都会 panic，因此调用方需用 recover 兜住，或用只读路径。
func newTestEngine(t *testing.T) (*ibusEngine, *engine.Session) {
	t.Helper()
	src := "你好\tni hao\t90\n你\tni\t80\n"
	d, err := engine.ParseDict(strings.NewReader(src))
	if err != nil {
		t.Fatalf("构造测试词库失败: %v", err)
	}
	sess := engine.NewSession(d, nil)
	return &ibusEngine{sess: sess}, sess
}

// FocusOut 必须清空组词状态。
func TestFocusOutClearsComposition(t *testing.T) {
	e, sess := newTestEngine(t)
	sess.Input("ni")
	if got := sess.Compose().Input; got != "ni" {
		t.Fatalf("前置条件失败：组词应为 ni，实际 %q", got)
	}
	// FocusOut 会调 hidePreedit/hideLookupTable（需要 conn）；nil conn 会 panic，
	// 用 recover 兜住，只验证会话状态被清空——这正是本用例的核心断言。
	func() {
		defer func() { _ = recover() }()
		_ = e.FocusOut()
	}()
	if got := sess.Compose().Input; got != "" {
		t.Fatalf("FocusOut 后组词应清空，实际 %q", got)
	}
}

// FocusIn 必须重置 Shift 空按跟踪。
func TestFocusInResetsShiftTracker(t *testing.T) {
	e, _ := newTestEngine(t)
	// 模拟「Shift 按下但没释放」就切换了窗口
	e.shift.shiftDown = 1
	e.shift.sinceShift = false
	func() {
		defer func() { _ = recover() }()
		_ = e.FocusIn()
	}()
	if e.shift.shiftDown != 0 || e.shift.sinceShift {
		t.Fatalf("FocusIn 应重置 shift 跟踪，实际 shiftDown=%d sinceShift=%v",
			e.shift.shiftDown, e.shift.sinceShift)
	}
}

// 候选表 page_size 必须与引擎导出的每页容量一致。
//
// 回归背景：这里曾写死 9 并注释「与引擎一致」，而引擎早已改成 6。
// IBusLookupTable.page_size 是面板布局与「还有没有下一页」的依据。
func TestLookupTablePageSizeMatchesEngine(t *testing.T) {
	want := engine.PageSize()
	if ibusPageSize != want {
		t.Fatalf("ibusPageSize=%d 与 engine.PageSize()=%d 不一致",
			ibusPageSize, want)
	}
}

// 候选序号必须是 1..N 递增，不能 %10 回绕成 0。
func TestLookupTableLabelsDoNotWrap(t *testing.T) {
	var cands []engine.Candidate
	for i := 1; i <= 11; i++ {
		cands = append(cands, engine.Candidate{Text: fmt.Sprintf("词%d", i), Score: i})
	}
	comp := engine.Composition{Input: "test", Preedit: "test", Candidates: cands}
	table := newIBusLookupTable(comp)

	labels, ok := table.Labels.Value().([]dbus.Variant)
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
