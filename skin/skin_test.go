package skin

import (
	"image/color"
	"reflect"
	"testing"
)

// TestFontSizeParity 锁定双端字号契约：Go 真源与 macOS CandidatePanel 的
// Layout.fontSize 必须一致（2026-10-26 曾因 16 vs 15 漂移被审查发现）。
func TestFontSizeParity(t *testing.T) {
	if got := DefaultSkin().FontSize; got != 15 {
		t.Fatalf("FontSize = %v, 期望 15（与 CandidatePanel.swift 一致）", got)
	}
}

// TestDefaultSkinKeyColors 是 shims/macos/tests/visual-parity.sh D 段
// 硬编码期望值的来源，两边改任何一边都必须同步。
func TestDefaultSkinKeyColors(t *testing.T) {
	s := DefaultSkinFor(false)
	want := map[string]color.NRGBA{
		"panelBg":     {0xF6, 0xF6, 0xF8, 0xF6},
		"highlightBg": {0x00, 0x7A, 0xFF, 0xFF},
		"text":        {0x1D, 0x1D, 0x1F, 0xFF},
		"chipBg":      {0xFF, 0xFF, 0xFF, 0xFF},
	}
	got := map[string]color.NRGBA{
		"panelBg":     s.BgColor,
		"highlightBg": s.HighlightBg,
		"text":        s.FgColor,
		"chipBg":      s.IndexChipBg,
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("关键色值漂移：want %v got %v", want, got)
	}
}

// TestDefaultSkinForCoversAllColors 保证深浅两套皮肤的每个颜色字段都被
// 显式赋值：新增字段忘了配色时在此暴露，而不是上线后变成透明。
func TestDefaultSkinForCoversAllColors(t *testing.T) {
	test := func(name string, s Skin) {
		t.Helper()
		v := reflect.ValueOf(s)
		typ := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := typ.Field(i)
			if f.Type != reflect.TypeOf(color.NRGBA{}) {
				continue
			}
			c := v.Field(i).Interface().(color.NRGBA)
			if c == (color.NRGBA{}) {
				t.Errorf("%s.%s 是零值（未配色）", name, f.Name)
			}
		}
	}
	test("light", DefaultSkinFor(false))
	test("dark", DefaultSkinFor(true))
}

func TestDefaultSkin(t *testing.T) {
	if DefaultSkin() != DefaultSkinFor(false) {
		t.Fatal("DefaultSkin 应等价于 DefaultSkinFor(false)")
	}
}
