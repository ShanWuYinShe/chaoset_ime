// Package skin 保存候选窗的视觉设计参数（几何 + 配色），是双端视觉的
// Go 侧真源：macOS 原生候选窗（shims/macos/src/CandidatePanel.swift）
// 与双端视觉一致性检查（tools/render_metrics.go，被
// shims/macos/tests/visual-parity.sh 调用）都以本包为准。
//
// 历史背景：2026-10-25 前 daemon 还负责把候选窗渲染成 PNG（renderer 包 +
// candidateImage IPC），三端壳先后改为自绘后渲染链路整体删除（2026-10-27），
// 视觉参数保留为本包——后续若做「构建期由本包生成 Swift 常量」（单一真源），
// 生成源就是这里的 DefaultSkinFor。
package skin

import "image/color"

// defaultMaxWidth 是横排候选栏的最大宽度（px，scale=1 时约 720pt）。
const defaultMaxWidth = 720

// Skin 是候选窗的全部视觉参数（几何为 scale=1 的像素值）。
type Skin struct {
	FontSize     float64 // 字号（pt）；必须与 macOS CandidatePanel 的 Layout.fontSize 一致
	Padding      int     // 内边距（px）
	LineSpacing  int     // 行间距（px）
	MaxWidth     int     // 单行最大宽度（px）；超宽换行
	CornerRadius int     // 面板与首选高亮的圆角半径（px）

	BgColor             color.NRGBA // 面板底色（半透明毛玻璃感）
	BorderColor         color.NRGBA // 外框
	PreeditColor        color.NRGBA // 预编辑行文字；横排时兼作页码指示等淡色文字
	FgColor             color.NRGBA // 候选文字
	HighlightBg         color.NRGBA // 首选候选底色（系统高亮蓝胶囊）
	HighlightFg         color.NRGBA // 首选项文字高亮色
	HighlightIndexColor color.NRGBA // 首选项序号颜色
	UserMarkColor       color.NRGBA // 用户学习标记（*）颜色
	IndexColor          color.NRGBA // 候选序号颜色
	// 序号键帽（非首选候选的数字按键提示）：IndexChipBg 为键帽底色，
	// IndexChipBorder 为其 1px 圆角描边；HighlightIndexChipBg 是首选
	// 高亮胶囊内的键帽底色。
	IndexChipBg          color.NRGBA
	IndexChipBorder      color.NRGBA
	HighlightIndexChipBg color.NRGBA
}

// DefaultSkinFor 返回系统输入法风格的皮肤：dark=false 浅色、true 深色。
// 布局为横排候选栏 + 圆角。配色取 macOS 原生输入法语义色：首选苹果经典
// 高亮蓝（#0066EB/#007AFF），序号收进键帽（系统蓝/灰数字 + keycap 底）。
func DefaultSkinFor(dark bool) Skin {
	skin := Skin{
		FontSize:     15,
		Padding:      7,
		LineSpacing:  4,
		MaxWidth:     defaultMaxWidth,
		CornerRadius: 8,
	}
	if dark {
		skin.BgColor = color.NRGBA{0x2C, 0x2C, 0x2E, 0xF5}              // macOS 深色 HUD 底（半透深灰）
		skin.BorderColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0x24}          // 1px 半透明高光边框
		skin.PreeditColor = color.NRGBA{0x98, 0x98, 0x9F, 0xB8}         // 次级文字
		skin.IndexColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}           // 序号纯白：白 on #5A5A5E 键帽 ≈7:1，小字号可读
		skin.IndexChipBg = color.NRGBA{0x5A, 0x5A, 0x5E, 0xFF}          // 序号键帽：中灰实底（与 #2C2C2E 背景明显拉开）
		skin.IndexChipBorder = color.NRGBA{0xFF, 0xFF, 0xFF, 0x40}      // 键帽细边（半透高光，加强轮廓）
		skin.HighlightIndexChipBg = color.NRGBA{0xFF, 0xFF, 0xFF, 0x45} // 首选蓝胶囊内的半透白键帽
		skin.FgColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}              // 候选纯白文字
		skin.HighlightBg = color.NRGBA{0x00, 0x66, 0xEB, 0xFF}          // 苹果原生高亮蓝
		skin.HighlightFg = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}          // 首选纯白文字
		skin.HighlightIndexColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}  // 首选序号纯白
		skin.UserMarkColor = color.NRGBA{0x30, 0xD1, 0x58, 0xFF}        // 学习标记绿
		return skin
	}
	skin.BgColor = color.NRGBA{0xF6, 0xF6, 0xF8, 0xF6}              // macOS 浅色毛玻璃底
	skin.BorderColor = color.NRGBA{0x00, 0x00, 0x00, 0x1A}          // 黑色细边
	skin.PreeditColor = color.NRGBA{0x8E, 0x8E, 0x93, 0xD0}         // 次级文字
	skin.IndexColor = color.NRGBA{0x1D, 0x1D, 0x1F, 0xFF}           // 序号深黑：黑 on 白键帽 ≈16:1，缩小后仍清晰
	skin.IndexChipBg = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}          // 序号键帽：纯白实底（毛玻璃灰底上成块）
	skin.IndexChipBorder = color.NRGBA{0x00, 0x00, 0x00, 0x3C}      // 键帽细边（加强轮廓）
	skin.HighlightIndexChipBg = color.NRGBA{0xFF, 0xFF, 0xFF, 0x45} // 首选蓝胶囊内的半透白键帽
	skin.FgColor = color.NRGBA{0x1D, 0x1D, 0x1F, 0xFF}              // 苹果深黑字
	skin.HighlightBg = color.NRGBA{0x00, 0x7A, 0xFF, 0xFF}          // 苹果原生高亮蓝
	skin.HighlightFg = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}          // 首选纯白文字
	skin.HighlightIndexColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}  // 首选序号纯白
	skin.UserMarkColor = color.NRGBA{0x34, 0xC7, 0x59, 0xFF}        // 学习标记绿
	return skin
}

// DefaultSkin 返回默认皮肤：浅色横排候选栏，等价于 DefaultSkinFor(false)。
func DefaultSkin() Skin {
	return DefaultSkinFor(false)
}
