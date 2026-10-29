package skin

import (
	"image/color"
	"reflect"
	"testing"
)

// TestFontSizeParity 锁定字号契约：Go 设计基准与 macOS CandidatePanel 的
// Layout.fontSize 必须一致（2026-10-26 曾因 16 vs 15 漂移被审查发现）。
func TestFontSizeParity(t *testing.T) {
	if got := DefaultSkin().FontSize; got != 15 {
		t.Fatalf("FontSize = %v, 期望 15（与 CandidatePanel.swift 一致）", got)
	}
}

// TestDefaultSkinColors 锁定浅/深两套**全部 13 个色值**的期望值。
//
// 2026-10-30 三端一致性审计 G4：此前只锁浅色 4 个、深色仅校验「非零」，
// 深色 12 色任意漂移都无人发现；divider/chevron 两个 Swift 有而 Go 无的
// 色也不在 Go 侧——visual-parity 的跨端色值比对因此有三个盲区。现在 Go 与
// Swift 的 13 个字段一一对应（CandidatePanel.CandidateTheme），本测试锁 Go
// 侧、visual-parity 锁 Go↔Swift 两侧。
func TestDefaultSkinColors(t *testing.T) {
	type themeCase struct {
		name string
		dark bool
		want map[string]color.NRGBA
	}
	light := map[string]color.NRGBA{
		"panelBg":         {0xF6, 0xF6, 0xF8, 0xF6},
		"panelBorder":     {0x00, 0x00, 0x00, 0x1A},
		"text":            {0x1D, 0x1D, 0x1F, 0xFF},
		"highlightBg":     {0x00, 0x7A, 0xFF, 0xFF},
		"highlightText":   {0xFF, 0xFF, 0xFF, 0xFF},
		"chipBg":          {0xFF, 0xFF, 0xFF, 0xFF},
		"chipBorder":      {0x00, 0x00, 0x00, 0x3C},
		"chipDigit":       {0x1D, 0x1D, 0x1F, 0xFF},
		"highlightChipBg": {0xFF, 0xFF, 0xFF, 0x45},
		"pageColor":       {0x8E, 0x8E, 0x93, 0xD0},
		"userMark":        {0x34, 0xC7, 0x59, 0xFF},
		"divider":         {0x00, 0x00, 0x00, 0x14},
		"chevron":         {0x8E, 0x8E, 0x93, 0xD0},
	}
	dark := map[string]color.NRGBA{
		"panelBg":         {0x2C, 0x2C, 0x2E, 0xF5},
		"panelBorder":     {0xFF, 0xFF, 0xFF, 0x24},
		"text":            {0xFF, 0xFF, 0xFF, 0xFF},
		"highlightBg":     {0x00, 0x66, 0xEB, 0xFF},
		"highlightText":   {0xFF, 0xFF, 0xFF, 0xFF},
		"chipBg":          {0x5A, 0x5A, 0x5E, 0xFF},
		"chipBorder":      {0xFF, 0xFF, 0xFF, 0x40},
		"chipDigit":       {0xFF, 0xFF, 0xFF, 0xFF},
		"highlightChipBg": {0xFF, 0xFF, 0xFF, 0x45},
		"pageColor":       {0x98, 0x98, 0x9F, 0xB8},
		"userMark":        {0x30, 0xD1, 0x58, 0xFF},
		"divider":         {0xFF, 0xFF, 0xFF, 0x1E},
		"chevron":         {0x98, 0x98, 0x9F, 0xB8},
	}
	colorsOf := func(s Skin) map[string]color.NRGBA {
		return map[string]color.NRGBA{
			"panelBg":         s.BgColor,
			"panelBorder":     s.BorderColor,
			"text":            s.FgColor,
			"highlightBg":     s.HighlightBg,
			"highlightText":   s.HighlightFg,
			"chipBg":          s.IndexChipBg,
			"chipBorder":      s.IndexChipBorder,
			"chipDigit":       s.IndexColor,
			"highlightChipBg": s.HighlightIndexChipBg,
			"pageColor":       s.PreeditColor,
			"userMark":        s.UserMarkColor,
			"divider":         s.DividerColor,
			"chevron":         s.ChevronColor,
		}
	}
	for _, tc := range []themeCase{{"light", false, light}, {"dark", true, dark}} {
		s := DefaultSkinFor(tc.dark)
		if got := colorsOf(s); !reflect.DeepEqual(tc.want, got) {
			t.Fatalf("%s 色值漂移：want %v got %v", tc.name, tc.want, got)
		}
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
