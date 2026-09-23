package main

import "testing"

// ibus 修饰键常量（与实现中的位定义一致）
const (
	testRelease = 1 << 30
	testShift   = 1 << 0
	testCtrl    = 1 << 2
)

// Shift 空按：按下 → 释放，应触发一次切换。
func TestShiftTrackerEmptyPress(t *testing.T) {
	var tr shiftTracker
	if tr.feed(65505, testShift) {
		t.Fatal("Shift 按下不应触发")
	}
	if !tr.feed(65505, testShift|testRelease) {
		t.Fatal("Shift 空按释放应触发切换")
	}
	// 释放后再来一次孤立释放：无按下的释放不触发
	if tr.feed(65505, testShift|testRelease) {
		t.Fatal("无按下的孤立释放不应触发")
	}
}

// Shift 与其他键混按（如 Shift+A）：不触发切换。
func TestShiftTrackerInterrupted(t *testing.T) {
	var tr shiftTracker
	tr.feed(65505, testShift) // Shift 按下
	tr.feed('A', testShift)   // Shift+A：其他键按下
	if tr.feed('A', testShift|testRelease) {
		t.Fatal("字母释放不应触发")
	}
	if tr.feed(65505, testShift|testRelease) {
		t.Fatal("被其他键打断的 Shift 释放不应触发")
	}
}

// 先正常切换一次，再混按：第二次不触发；再空按：恢复触发。
func TestShiftTrackerToggleThenInterrupt(t *testing.T) {
	var tr shiftTracker
	tr.feed(65505, testShift)
	if !tr.feed(65505, testShift|testRelease) {
		t.Fatal("第一次空按应触发")
	}
	tr.feed(65505, testShift)
	tr.feed('x', 0)
	tr.feed('x', testRelease)
	if tr.feed(65505, testShift|testRelease) {
		t.Fatal("混按后的释放不应触发")
	}
	tr.feed(65505, testShift)
	if !tr.feed(65505, testShift|testRelease) {
		t.Fatal("恢复空按后应再次触发")
	}
}

// Ctrl 组合键出现在窗口期内：取消切换。
func TestShiftTrackerCtrlInterrupts(t *testing.T) {
	var tr shiftTracker
	tr.feed(65505, testShift)
	tr.feed('a', testCtrl)
	if tr.feed(65505, testShift|testRelease) {
		t.Fatal("Ctrl 组合打断后不应触发")
	}
}

// 右 Shift 同样生效；两颗 Shift 都按下时，松开其中一颗不触发，
// 全部松开才判定。
func TestShiftTrackerRightShift(t *testing.T) {
	var tr shiftTracker
	tr.feed(65506, testShift) // 右 Shift 按下
	if !tr.feed(65506, testShift|testRelease) {
		t.Fatal("右 Shift 空按应触发")
	}
	tr.feed(65505, testShift) // 左 Shift 按下
	tr.feed(65506, testShift) // 右 Shift 也按下（换手）
	if tr.feed(65505, testShift|testRelease) {
		t.Fatal("另一颗 Shift 尚未释放时不应触发")
	}
	if !tr.feed(65506, testShift|testRelease) {
		t.Fatal("最后一颗释放应触发")
	}
}
