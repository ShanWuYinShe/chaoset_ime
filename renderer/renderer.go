// Package renderer 把组词状态渲染为候选窗 PNG，供「daemon 渲染、壳贴图」
// 的调用方使用（Windows/Linux 壳，以及 macOS 壳的原生候选窗不可用时的回退；
// macOS 自 v3 起默认壳内 AppKit 原生自绘，见 shims/macos/src/CandidatePanel.swift，
// 其视觉与本包 Skin 设计保持双端同步）。
// 纯 Go（x/image 栅格化 + stdlib PNG），渲染结果可单测、可人工目检。
//
// 默认皮肤 DefaultSkin() 为系统输入法风格：半透明圆角底 + 1px 细边框 +
// 横排候选栏（首选蓝底高亮 + 序号键帽）；DefaultSkinFor(true) 给出深色版。
// Skin.Horizontal=false 时退回旧的竖排列表布局（预编辑行 + 每候选一行），
// 该路径保留给旧调用方，配色与装帧（朱批竖记、内框细线）完全由 Skin 字段决定。
package renderer

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"reflect"
	"runtime"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"chaoset.ime/engine"
)

const (
	// defaultMaxWidth 是横排候选栏的默认最大宽度（px，scale=1 时约 720pt）。
	// 超宽自动换行，最多 maxBarRows 行；壳侧按图片实际像素摆面板，不再二次截断。
	defaultMaxWidth = 720
	// maxBarRows 是横排候选栏的最大行数，放不下的候选以 … 截断。
	maxBarRows = 3
	// defaultCornerRadius 是默认皮肤的面板圆角半径（px）。
	defaultCornerRadius = 10
)

// Skin 是候选窗皮肤。零值不可用：用 DefaultSkin() 并按需覆盖字段。
type Skin struct {
	FontPath            string      // CJK 字体（TTF/TTC）；空串则按平台常见路径探测
	FontSize            float64     // 字号（pt）
	Padding             int         // 内边距（px）
	LineSpacing         int         // 行间距（px）
	BgColor             color.NRGBA // 背景半透明
	BorderColor         color.NRGBA // 外框
	InnerBorderColor    color.NRGBA // 内框细线（竖排/印章双框语汇）；Alpha=0 时不画
	PreeditColor        color.NRGBA // 预编辑行文字；横排时兼作页码指示等淡色文字
	FgColor             color.NRGBA // 候选文字
	HighlightBg         color.NRGBA // 首选候选底色
	HighlightBarColor   color.NRGBA // 首选候选左侧「朱批」竖记；Alpha=0 时不画
	UserMarkColor       color.NRGBA // 用户学习标记（*）颜色
	IndexColor          color.NRGBA // 候选序号颜色；Alpha=0 时退回 PreeditColor
	HighlightFg         color.NRGBA // 首选项文字高亮色；Alpha=0 时退回 FgColor
	HighlightIndexColor color.NRGBA // 首选项序号颜色；Alpha=0 时退回 HighlightFg

	// 序号键帽（非首选候选的数字按键提示）：IndexChipBg 为键帽底色，
	// IndexChipBorder 为其 1px 圆角描边，Alpha=0 时均不画（序号退回纯文字）；
	// HighlightIndexChipBg 是首选高亮胶囊内的键帽底色，Alpha=0 时回退 IndexChipBg。
	IndexChipBg          color.NRGBA
	IndexChipBorder      color.NRGBA
	HighlightIndexChipBg color.NRGBA

	// Horizontal 为 true 时渲染横排候选栏（系统输入法风格）：不再画预编辑行，
	// 候选按「序号+文本」横向流式排列，页码指示作为末尾一项。零值 false =
	// 旧的竖排列表布局。
	Horizontal bool
	// MaxWidth 是横排时单行最大宽度（px，含内边距）；超宽换行，最多 3 行，
	// 0 表示不换行。竖排布局忽略此字段。
	MaxWidth int
	// CornerRadius 是面板与首选高亮的圆角半径（px），0 表示直角。
	CornerRadius int
}

// DefaultSkinFor 返回系统输入法风格的皮肤：dark=false 浅色、true 深色。
// 字体仍走 probeFontPath() 的平台探测；布局为横排候选栏 + 圆角。
// 配色取 macOS 原生输入法语义色：首选苹果经典高亮蓝（#0066EB/#007AFF），
// 序号收进白色键帽（系统蓝数字 + keycap 底），右侧带极简折叠指示符。
func DefaultSkinFor(dark bool) Skin {
	skin := Skin{
		FontPath:          probeFontPath(),
		FontSize:          15,
		Padding:           7,
		LineSpacing:       4,
		Horizontal:        true,
		MaxWidth:          defaultMaxWidth,
		CornerRadius:      8,
		HighlightBarColor: color.NRGBA{}, // 系统风格不画朱批竖记
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

// DefaultSkin 返回默认皮肤：系统风格浅色横排候选栏，等价于 DefaultSkinFor(false)。
// 竖排 + 宣纸/朱红装帧的旧观感仍可用 Horizontal=false 的皮肤复现（见 Skin 字段）。
func DefaultSkin() Skin {
	return DefaultSkinFor(false)
}

// WithDarkMode 把皮肤配色切换为浅色/深色主题，保留字体与布局等非配色字段
// （FontPath/FontSize/Padding/LineSpacing/Horizontal/MaxWidth/CornerRadius）。
// daemon 用 -font/-fontsize 定制字体后，壳仍可只按外观切换配色。
//
// 配色字段（全部 color.NRGBA）用反射逐字段同步，而非手动清单：Skin 新增
// 配色字段时此函数自动覆盖。手动清单曾漏拷序号键帽三字段（2026-10-25），
// daemon 从 DefaultSkin() 浅色起步收到 dark 请求后键帽仍是浅色纯白，
// 深色下白数字画在纯白键帽上完全不可见（白色实心方块）。
func WithDarkMode(skin Skin, dark bool) Skin {
	theme := DefaultSkinFor(dark)
	sv, tv := reflect.ValueOf(&skin).Elem(), reflect.ValueOf(theme)
	t := sv.Type()
	nrgba := reflect.TypeOf(color.NRGBA{})
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Type == nrgba {
			sv.Field(i).Set(tv.Field(i))
		}
	}
	return skin
}

// ScaleSkin 返回按 scale 倍像素渲染的皮肤副本：字号、内边距、行距、
// 最大宽度、圆角全部乘以 scale，壳侧按 图片像素/scale 设置 NSImage.size
// 即可得到 Retina 清晰度。只接受 2（其余值等价于 1，见 EffectiveScale）。
func ScaleSkin(skin Skin, scale int) Skin {
	f := EffectiveScale(scale)
	if f == 1 {
		return skin
	}
	skin.FontSize *= float64(f)
	skin.Padding *= f
	skin.LineSpacing *= f
	skin.MaxWidth *= f
	skin.CornerRadius *= f
	return skin
}

// EffectiveScale 把请求的渲染倍率规整为受支持的值：2 保持，其余一律按 1。
func EffectiveScale(scale int) int {
	if scale == 2 {
		return 2
	}
	return 1
}

// 探测顺序：常见 CJK 字体的平台惯例路径，首个存在的即用。
func probeFontPath() string {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/System/Library/Fonts/PingFang.ttc",
			"/System/Library/Fonts/Hiragino Sans GB.ttc",
			"/System/Library/Fonts/STHeiti Light.ttc",
			"/System/Library/Fonts/STHeiti Medium.ttc",
		}
	case "windows":
		candidates = []string{
			`C:\Windows\Fonts\msyh.ttc`,
			`C:\Windows\Fonts\simsun.ttc`,
		}
	default: // linux 及其他
		candidates = []string{
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
			"/usr/share/fonts/wenquanyi/wqy-microhei/wqy-microhei.ttc",
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ---- 字体缓存：daemon 每次击键都可能渲染，TTF/TTC 只解析一次 ----

type faceKey struct {
	path string
	size float64
}

var (
	faceMu      sync.Mutex
	parsedFonts = map[string]*opentype.Font{}
	faceMon     = map[faceKey]font.Face{}
)

func loadFaceWithSize(path string, size float64) (font.Face, error) {
	if path == "" {
		return nil, fmt.Errorf("未找到 CJK 字体（请用 -font 指定路径）")
	}
	if size <= 0 {
		return nil, fmt.Errorf("字号必须为正")
	}
	faceMu.Lock()
	defer faceMu.Unlock()
	key := faceKey{path, size}
	if f, ok := faceMon[key]; ok {
		return f, nil
	}
	ft, ok := parsedFonts[path]
	if !ok {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取字体失败: %w", err)
		}
		var ferr error
		ft, ferr = parseFont(data)
		if ferr != nil {
			return nil, fmt.Errorf("解析字体 %s 失败: %w", path, ferr)
		}
		parsedFonts[path] = ft
	}
	face, err := opentype.NewFace(ft, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("创建字体面失败: %w", err)
	}
	faceMon[key] = face
	return face, nil
}

func loadFace(s Skin) (font.Face, error) {
	return loadFaceWithSize(s.FontPath, s.FontSize)
}

// loadIndexFace 加载用于序号与页码的小字号面（主字号约 80%）：70% 时
// Retina 缩视下序号笔画过细、与键帽底色对比不足（2026-10-25 用户反馈
// 可读性差），提到 80% 兼顾层级感与可读性。
func loadIndexFace(s Skin) (font.Face, error) {
	return loadFaceWithSize(s.FontPath, s.FontSize*0.80)
}

func parseFont(data []byte) (*opentype.Font, error) {
	if coll, err := opentype.ParseCollection(data); err == nil {
		return coll.Font(0)
	}
	return opentype.Parse(data)
}

var renderMu sync.Mutex // 字体面并发不安全；渲染串行化（耗时微秒级）

// RenderWithBounds 把组词状态画成候选窗 PNG，并直接返回像素宽高。
// 无候选时返回 (nil, 0, 0, nil)；字体不可用时返回错误。
func RenderWithBounds(comp engine.Composition, skin Skin) ([]byte, int, int, error) {
	face, err := loadFace(skin)
	if err != nil {
		return nil, 0, 0, err
	}
	indexFace, err := loadIndexFace(skin)
	if err != nil {
		indexFace = face
	}
	if len(comp.Candidates) == 0 {
		return nil, 0, 0, nil
	}

	renderMu.Lock()
	defer renderMu.Unlock()

	var img *image.NRGBA
	if skin.Horizontal {
		img = renderBar(comp, skin, face, indexFace)
	} else {
		img = renderList(comp, skin, face)
	}
	if img == nil {
		return nil, 0, 0, nil
	}

	b := img.Bounds()
	return encodeFastPNG(img), b.Dx(), b.Dy(), nil
}

// RenderImageForTest 直接返回未编码的图像，仅供像素级回归测试使用。
// 生产路径请用 RenderWithBounds（它负责 PNG 编码与尺寸）。
func RenderImageForTest(comp engine.Composition, skin Skin) (*image.NRGBA, error) {
	face, err := loadFace(skin)
	if err != nil {
		return nil, err
	}
	indexFace, err := loadIndexFace(skin)
	if err != nil {
		indexFace = face
	}
	if len(comp.Candidates) == 0 {
		return nil, nil
	}
	renderMu.Lock()
	defer renderMu.Unlock()
	if skin.Horizontal {
		return renderBar(comp, skin, face, indexFace), nil
	}
	return renderList(comp, skin, face), nil
}

// Render 把组词状态画成候选窗 PNG。
// 无候选时返回 (nil, nil)（壳应隐藏窗口）；字体不可用时返回错误，
// 壳应回退到各平台 v1 候选 UI。
// Skin.Horizontal=true 走横排候选栏，否则走旧的竖排列表。
func Render(comp engine.Composition, skin Skin) ([]byte, error) {
	data, _, _, err := RenderWithBounds(comp, skin)
	return data, err
}

// renderBar 画横排候选栏：macOS 原生风格，首项苹果蓝圆角高亮，
// 序号采用精细小字号并垂直居中对齐，右侧包含系统折叠指示符。
func renderBar(comp engine.Composition, skin Skin, face, indexFace font.Face) *image.NRGBA {
	pad := skin.Padding
	if pad <= 0 {
		pad = 7
	}
	scale := EffectiveScale(int(math.Round(skin.FontSize / 15.0)))
	metrics := face.Metrics()
	ascent, descent := metrics.Ascent.Ceil(), metrics.Descent.Ceil()
	textH := ascent + descent
	spacing := skin.LineSpacing
	if spacing < 0 {
		spacing = 0
	}

	lineH := textH + spacing

	// macOS 原生风格排版留白参数：
	firstPadLeft := 9 * scale
	itemPadX := 7 * scale
	gap := 11 * scale
	chevronBoxW := 20 * scale

	idxColor := skin.IndexColor
	if idxColor.A == 0 {
		idxColor = skin.PreeditColor
	}
	highIdxColor := skin.HighlightIndexColor
	if highIdxColor.A == 0 {
		highIdxColor = skin.HighlightFg
	}
	// 序号键帽（数字按键提示）：Alpha=0 关闭时序号退回纯文字，序号宽即原宽。
	chipBg := skin.IndexChipBg
	chipBorder := skin.IndexChipBorder
	hiChipBg := skin.HighlightIndexChipBg
	if hiChipBg.A == 0 {
		hiChipBg = chipBg
	}
	chipPadX, chipPadY, chipR := 4*scale, 2*scale, 3*scale // 键帽几何：左右内衬、上下内衬、圆角

	items := make([]barItem, 0, len(comp.Candidates)+1)
	for i, cand := range comp.Candidates {
		idxText := fmt.Sprintf("%d", i+1)
		candFg := skin.FgColor
		cIdx := idxColor
		px := itemPadX
		if i == 0 {
			if skin.HighlightFg.A != 0 {
				candFg = skin.HighlightFg
			}
			cIdx = highIdxColor
			px = firstPadLeft
		}
		it := newBarCandidateItem(face, indexFace, idxText, cand.Text, candFg, cIdx, px, i == 0, cand.FromUser)
		if chipBg.A != 0 {
			it.chipPad = chipPadX // 键帽左右各加一层内衬，序号收进 keycap
			it.boxW += 2 * chipPadX
		}
		items = append(items, it)
	}
	// 多页时在末尾附页码指示，如 1/4
	if comp.PageCount > 1 {
		text := pageIndicator(face, comp.Page+1, comp.PageCount)
		items = append(items, newBarSimpleItem(indexFace, text, skin.PreeditColor, itemPadX))
	}
	if len(items) == 0 {
		return nil
	}

	// maxContent = 单行可用内容宽（不含左右内边距与右侧折叠区）；0 表示不换行
	maxContent := 0
	if skin.MaxWidth > 0 {
		maxContent = skin.MaxWidth - pad*2 - chevronBoxW
		if maxContent < 1 {
			maxContent = 1
		}
	}

	// 贪心分行，最多 maxBarRows 行；放不下的候选以 … 截断
	var rows [][]barItem
	next := 0
	for next < len(items) && len(rows) < maxBarRows {
		var row []barItem
		w := 0
		for next < len(items) {
			it := items[next]
			add := it.boxW
			if len(row) > 0 {
				add += gap
			}
			if maxContent > 0 && len(row) > 0 && w+add > maxContent {
				break
			}
			row = append(row, it)
			w += add
			next++
		}
		rows = append(rows, row)
	}
	if next < len(items) {
		ell := newBarSimpleItem(face, "…", skin.PreeditColor, itemPadX)
		last := len(rows) - 1
		for len(rows[last]) > 0 && maxContent > 0 &&
			rowWidth(rows[last], gap)+gap+ell.boxW > maxContent {
			rows[last] = rows[last][:len(rows[last])-1]
		}
		rows[last] = append(rows[last], ell)
	}

	width := 0
	for _, row := range rows {
		if w := rowWidth(row, gap) + pad*2 + chevronBoxW; w > width {
			width = w
		}
	}
	height := pad*2 + len(rows)*lineH - spacing
	if width <= 0 || height <= 0 {
		return nil
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	drawPanel(img, skin)

	// 计算小序号垂直光学居中位置
	indexMetrics := indexFace.Metrics()
	indexH := indexMetrics.Ascent.Ceil() + indexMetrics.Descent.Ceil()
	indexAscent := indexMetrics.Ascent.Ceil()

	pillMargin := 2 * scale
	pillR := clampRadius(image.Rect(0, 0, 100, height), 6*scale)
	panelR := clampRadius(img.Bounds(), skin.CornerRadius)

	d := &font.Drawer{Dst: img}
	y := pad
	for _, row := range rows {
		x := pad
		baseY := y + ascent
		indexBaseY := y + (textH-indexH)/2 + indexAscent
		for _, it := range row {
			box := image.Rect(x-pillMargin, y-pillMargin, x+it.boxW, y+textH+pillMargin)
			if it.highlight {
				// macOS 原生高亮胶囊：贴合面板内侧，左圆角承接面板圆角、右圆角微收
				leftR := panelR - pillMargin
				if leftR < 4*scale {
					leftR = 4 * scale
				}
				drawCapsule(img, box, leftR, pillR, skin.HighlightBg)
			}

			curX := x + it.padX

			if it.indexText != "" {
				if it.chipPad > 0 {
					// 序号键帽：白/深灰 keycap 底 + 细边，首选在蓝胶囊内用半透白底。
					// 边框不走 drawRoundBorder：它按整幅图分配遮罩并做全图合成，
					// 每个候选调用一次的开销随候选数线性放大（实测吃掉整帧数百
					// µs）；小面积键帽改双层填充——外层边框色、内层底色。
					bg := chipBg
					if it.highlight {
						bg = hiChipBg
					}
					chipTop := indexBaseY - indexAscent - chipPadY
					chipRect := image.Rect(curX, chipTop, curX+it.indexW+2*it.chipPad, chipTop+indexH+2*chipPadY)
					if !it.highlight && chipBorder.A != 0 {
						drawRoundRect(img, chipRect, chipR, chipBorder, draw.Over)
						if inner := chipRect.Inset(1); !inner.Empty() {
							innerR := chipR - 1
							if innerR < 1 {
								innerR = 1
							}
							drawRoundRect(img, inner, innerR, bg, draw.Over)
						}
					} else {
						drawRoundRect(img, chipRect, chipR, bg, draw.Over)
					}
				}
				d.Face = indexFace
				d.Src = image.NewUniform(it.idxColor)
				d.Dot = fixed.P(curX+it.chipPad, indexBaseY)
				d.DrawString(it.indexText)
				curX += it.indexW + 2*it.chipPad + it.gapW
			}

			d.Face = face
			d.Src = image.NewUniform(it.color)
			d.Dot = fixed.P(curX, baseY)
			d.DrawString(it.text)
			curX += it.textW

			if it.userMark && skin.UserMarkColor.A != 0 {
				d.Src = image.NewUniform(skin.UserMarkColor)
				d.Dot = fixed.P(curX+1, baseY)
				d.DrawString("*")
			}
			x += it.boxW + gap
		}
		y += lineH
	}

	// 绘制右侧折叠区：1px 细分割线 + 下拉指示箭头 ⌄
	dividerColor := color.NRGBA{0xFF, 0xFF, 0xFF, 0x1E}
	chevronColor := color.NRGBA{0x98, 0x98, 0x9F, 0xB8}
	if skin.BgColor.R > 200 { // 浅色皮肤
		dividerColor = color.NRGBA{0x00, 0x00, 0x00, 0x14}
		chevronColor = color.NRGBA{0x8E, 0x8E, 0x93, 0xD0}
	}
	dividerX := width - chevronBoxW + (2 * scale)
	divH := textH * 55 / 100
	divY0 := (height - divH) / 2
	for dy := 0; dy < divH; dy++ {
		for dx := 0; dx < scale; dx++ {
			blendPixel(img, dividerX+dx, divY0+dy, dividerColor)
		}
	}

	chevX := dividerX + ((chevronBoxW - 2*scale) / 2)
	chevY := height / 2
	drawChevron(img, chevX, chevY, 3*scale, chevronColor)

	return img
}

// pageIndicator 生成页码指示文本，形如 1/5（现代输入法简约规范）。
func pageIndicator(face font.Face, page, pageCount int) string {
	return fmt.Sprintf("%d/%d", page, pageCount)
}

// hasGlyph 报告字体面是否含该字符的字形（缺字形时 font.Face 返回 ok=false）。
func hasGlyph(face font.Face, r rune) bool {
	_, ok := face.GlyphAdvance(r)
	return ok
}

// barItem 是横排候选栏的一项（候选或页码指示）。
type barItem struct {
	indexText string      // 序号（如 "1"）；非候选项时为空
	text      string      // 候选主体文本或页码指示
	color     color.NRGBA // 主体文字颜色
	idxColor  color.NRGBA // 序号颜色
	highlight bool
	userMark  bool
	padX      int // 内部左侧留白
	chipPad   int // 序号键帽单侧内衬；0 = 不画键帽（序号纯文字）
	indexW    int // 序号像素宽
	gapW      int // 序号与候选词间距像素宽
	textW     int // 文本像素宽（不含序号、留白与标记）
	markW     int // 学习标记宽
	boxW      int // 含左右留白的整项宽（键帽开启时含 2*chipPad）
}

func newBarCandidateItem(face, indexFace font.Face, idxText, text string, c, idxC color.NRGBA, itemPadX int, highlight, userMark bool) barItem {
	iw := 0
	gw := 0
	if idxText != "" {
		iw = font.MeasureString(indexFace, idxText).Ceil()
		gw = max(3, iw*45/100)
	}
	tw := font.MeasureString(face, text).Ceil()
	mw := 0
	if userMark {
		mw = font.MeasureString(face, "*").Ceil() + 2
	}
	boxW := itemPadX*2 + iw + gw + tw + mw
	return barItem{
		indexText: idxText,
		text:      text,
		color:     c,
		idxColor:  idxC,
		highlight: highlight,
		userMark:  userMark,
		padX:      itemPadX,
		indexW:    iw,
		gapW:      gw,
		textW:     tw,
		markW:     mw,
		boxW:      boxW,
	}
}

func newBarSimpleItem(face font.Face, text string, c color.NRGBA, itemPadX int) barItem {
	tw := font.MeasureString(face, text).Ceil()
	return barItem{
		text:  text,
		color: c,
		padX:  itemPadX,
		textW: tw,
		boxW:  tw + itemPadX*2,
	}
}

// rowWidth 是一行内容宽（项宽之和 + 项间距）。
func rowWidth(row []barItem, gap int) int {
	w := 0
	for i, it := range row {
		if i > 0 {
			w += gap
		}
		w += it.boxW
	}
	return w
}

// renderList 画旧的竖排列表：第 1 行预编辑（可选），之后每候选一行。
// 该布局保留给 Horizontal=false 的皮肤（零值兼容）。
func renderList(comp engine.Composition, skin Skin, face font.Face) *image.NRGBA {
	lineH := int(face.Metrics().Ascent.Ceil()+face.Metrics().Descent.Ceil()) + skin.LineSpacing
	pad := skin.Padding
	if pad <= 0 {
		pad = 8
	}

	// 布局：第 1 行预编辑（可选），之后每候选一行
	type line struct {
		text      string
		color     color.NRGBA
		highlight bool
		userMark  bool
	}
	var lines []line
	if comp.Preedit != "" {
		preeditText := comp.Preedit
		if comp.PageCount > 1 {
			// 多页时在预编辑行尾附页码指示（与 CLI 输出格式一致）
			preeditText += fmt.Sprintf("  [%d/%d]", comp.Page+1, comp.PageCount)
		}
		lines = append(lines, line{text: preeditText, color: skin.PreeditColor})
	}
	for i, cand := range comp.Candidates {
		text := fmt.Sprintf("%d.%s", (i+1)%10, cand.Text)
		if cand.FromUser {
			text += " *"
		}
		lines = append(lines, line{
			text:      text,
			color:     skin.FgColor,
			highlight: i == 0,
			userMark:  cand.FromUser,
		})
	}

	// 测量：宽度 = 最宽行；高度 = 行数 × lineH - 末行 spacing + 2×pad
	maxW := 0
	for _, ln := range lines {
		w := font.MeasureString(face, ln.text).Ceil()
		if w > maxW {
			maxW = w
		}
	}
	if maxW == 0 {
		return nil
	}
	width := maxW + pad*2
	height := pad*2 + len(lines)*lineH - skin.LineSpacing
	if width <= 0 || height <= 0 {
		return nil
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	drawPanel(img, skin)

	// 逐行绘制
	ascent, descent := face.Metrics().Ascent.Ceil(), face.Metrics().Descent.Ceil()
	d := &font.Drawer{Dst: img, Face: face}
	y := pad + ascent
	for _, ln := range lines {
		if ln.highlight {
			// 首选高亮：整行底色（从行顶到行底，宽度铺满内区）
			rect := image.Rect(pad/2, y-ascent, width-pad/2, y+descent)
			drawRoundRect(img, rect, clampRadius(rect, skin.CornerRadius), skin.HighlightBg, draw.Over)
			// 朱批：首选行左侧一道朱红竖记（中国传统批注记号）
			if skin.HighlightBarColor.A != 0 && rect.Dy() >= 3 {
				bar := image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+3, rect.Max.Y)
				draw.Draw(img, bar, &image.Uniform{skin.HighlightBarColor}, image.Point{}, draw.Over)
			}
		}
		d.Src = image.NewUniform(ln.color)
		d.Dot = fixed.P(pad, y)
		d.DrawString(ln.text)
		if ln.userMark {
			// 学习标记：绿色 * 覆盖行尾（文本本身已含 *，换色强调）
			markX := pad + font.MeasureString(face, ln.text).Ceil() - font.MeasureString(face, "*").Ceil()
			d.Src = image.NewUniform(skin.UserMarkColor)
			d.Dot = fixed.P(markX, y)
			d.DrawString("*")
		}
		y += lineH
	}

	// 内框细线后画，保证在朱红高亮之上仍清晰（双框 = 印章/宣纸装帧语汇）。
	// 圆角面板下 1px 方框会在四角探出圆角外（透明区），故仅在直角面板时绘制；
	// 默认圆角皮肤 InnerBorderColor 为零值，本就不会走到这里。
	if skin.CornerRadius <= 0 {
		drawInnerBorder(img, skin.InnerBorderColor, 3)
	}
	return img
}

// drawPanel 铺背景并描边：CornerRadius>0 走像素级圆角（圆角外 alpha=0，
// 壳侧透明面板贴图即得圆角），否则直角。
func drawPanel(img *image.NRGBA, skin Skin) {
	b := img.Bounds()
	if skin.CornerRadius > 0 {
		drawRoundRect(img, b, clampRadius(b, skin.CornerRadius), skin.BgColor, draw.Src)
		drawRoundBorder(img, b, clampRadius(b, skin.CornerRadius), skin.BorderColor)
		return
	}
	draw.Draw(img, b, &image.Uniform{skin.BgColor}, image.Point{}, draw.Src)
	drawBorder(img, skin.BorderColor)
}

// drawRoundRect 用纯色填充圆角矩形（无抗锯齿，圆角外像素保持原样）。
//
// 不走 mask + draw.DrawMask：那条通用路径会对每个像素做一次源/目标合成
// （还带 interface 调用），实测占单帧渲染三成以上。这里填充色是纯色，
// 直接按行写 Pix；圆角外的少量像素靠 mask 跳过即可。
func drawRoundRect(img *image.NRGBA, rect image.Rectangle, radius int, c color.NRGBA, op draw.Op) {
	rect = rect.Intersect(img.Bounds())
	if rect.Empty() {
		return
	}
	if radius <= 0 {
		draw.Draw(img, rect, &image.Uniform{c}, image.Point{}, op)
		return
	}
	r := clampRadius(rect, radius)
	mask := roundRectMask(rect, r)
	stride := img.Stride
	pix := img.Pix
	mstride := mask.Stride
	mpix := mask.Pix
	base := img.Rect.Min
	w := rect.Dx()
	sr, sg, sb, sa := uint32(c.R), uint32(c.G), uint32(c.B), uint32(c.A)
	for y := 0; y < rect.Dy(); y++ {
		row := (rect.Min.Y+y-base.Y)*stride + (rect.Min.X-base.X)*4
		mrow := y * mstride
		for x := 0; x < w; x++ {
			if mpix[mrow+x] == 0 {
				continue
			}
			i := row + x*4
			if op == draw.Src {
				// 与 draw.DrawMask 的 Src 语义对齐：NRGBA 目标是**非预乘**存储，
				// 纯色源直接写入即为正确结果（实测 draw.DrawMask 对带 alpha 的
				// Uniform 源也是原样写出，不做预乘换算）。
				pix[i+0], pix[i+1], pix[i+2], pix[i+3] = c.R, c.G, c.B, c.A
				continue
			}
			// Over：按源 alpha 做标准合成（纯色源，圆角遮罩非 0 即全覆盖）
			if sa == 0 {
				continue
			}
			ia := 255 - sa
			// alpha 用带 +127 的四舍五入式，与 draw.DrawMask 逐位对齐。
			pix[i+0] = uint8((sr*sa + uint32(pix[i+0])*ia) / 255)
			pix[i+1] = uint8((sg*sa + uint32(pix[i+1])*ia) / 255)
			pix[i+2] = uint8((sb*sa + uint32(pix[i+2])*ia) / 255)
			pix[i+3] = uint8((sa*255 + uint32(pix[i+3])*ia + 127) / 255)
		}
	}
}

// roundRectMask 生成圆角矩形遮罩（圆角外 alpha=0）。
//
// 性能关键：整幅图逐像素做圆角判定曾经是候选窗渲染的最大开销——
// 611×30 的候选栏就要做 1.8 万次浮点判断（实测 1.08ms，占单帧六成），
// 而这一步在**每次击键**都会跑一遍，直接造成可感知的卡顿。
//
// 实际只有四个角的 r×r 方块可能落在圆角外，中间区域必然是内部像素。
// 因此这里把工作拆成：
//  1. 先把整行按「直边矩形」批量填满（纯 memset，无逐像素判断）；
//  2. 再只对四个角的方块做精确判定，把圆角外的像素清零。
//
// 复杂度从 O(宽×高) 降到 O(r²)，而 r 通常只有 10。
// 圆角遮罩缓存：面板尺寸在连续输入中高度重复（同高度、宽度渐变且翻页/
// 主题切换时完全一致），每帧重新分配 + 填充 Alpha 位图纯属浪费。遮罩内容
// 只取决于宽/高/半径（与位置无关：唯一调用方 drawRoundRect 按 0 基相对
// 坐标索引）；渲染已被 renderMu 串行化，缓存仍用独立锁以防测试并发。
// 超过 256 项整表清空，防止长会话随宽度渐变无限膨胀。
type maskKey struct{ w, h, r int }

var (
	maskMu    sync.Mutex
	maskCache = map[maskKey]*image.Alpha{}
)

// roundRectMask 返回 (w,h,r) 对应的圆角遮罩（0 基），命中缓存时共享只读实例。
func roundRectMask(rect image.Rectangle, radius int) *image.Alpha {
	w, h := rect.Dx(), rect.Dy()
	r := clampRadius(rect, radius)
	key := maskKey{w, h, r}
	maskMu.Lock()
	m, ok := maskCache[key]
	maskMu.Unlock()
	if ok {
		return m
	}
	m = newRoundRectMask(w, h, r)
	maskMu.Lock()
	if len(maskCache) >= 256 {
		maskCache = make(map[maskKey]*image.Alpha, 64)
	}
	maskCache[key] = m
	maskMu.Unlock()
	return m
}

// newRoundRectMask 生成 0 基圆角矩形遮罩（圆角外 alpha=0）。
//
// 性能关键：整幅图逐像素做圆角判定曾经是候选窗渲染的最大开销——
// 611×30 的候选栏就要做 1.8 万次浮点判断（实测 1.08ms，占单帧六成），
// 而这一步在**每次击键**都会跑一遍，直接造成可感知的卡顿。
//
// 实际只有四个角的 r×r 方块可能落在圆角外，中间区域必然是内部像素。
// 因此这里把工作拆成：
//  1. 先把整行按「直边矩形」批量填满（纯 memset，无逐像素判断）；
//  2. 再只对四个角的方块做精确判定，把圆角外的像素清零。
//
// 复杂度从 O(宽×高) 降到 O(r²)，而 r 通常只有 10。
func newRoundRectMask(w, h, r int) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, w, h))
	if r <= 0 {
		for i := range m.Pix {
			m.Pix[i] = 0xFF
		}
		return m
	}
	stride := m.Stride
	pix := m.Pix
	// 1) 整幅先按实心矩形填满。
	for y := 0; y < h; y++ {
		row := y * stride
		for x := 0; x < w; x++ {
			pix[row+x] = 0xFF
		}
	}
	// 2) 只修正四个角：把落在圆角外的像素清零。
	//    corner 给出该角的方块范围（在 0 基坐标系内判定，与遮罩位置无关）。
	type corner struct{ x0, y0, x1, y1 int }
	corners := [4]corner{
		{0, 0, r, r},         // 左上
		{w - r, 0, w, r},     // 右上
		{0, h - r, r, h},     // 左下
		{w - r, h - r, w, h}, // 右下
	}
	base := image.Rect(0, 0, w, h)
	for _, c := range corners {
		if c.x1 <= c.x0 || c.y1 <= c.y0 {
			continue
		}
		for y := c.y0; y < c.y1; y++ {
			row := y * stride
			for x := c.x0; x < c.x1; x++ {
				// 这里直接用「整图坐标系」判定，insideRoundRect 已处理直角区域。
				if !insideRoundRect(base, r, x, y) {
					pix[row+x] = 0
				}
			}
		}
	}
	return m
}

// drawRoundBorder 沿圆角矩形内侧描 1px 边（Alpha=0 时跳过）。
func drawRoundBorder(img *image.NRGBA, rect image.Rectangle, radius int, c color.NRGBA) {
	if c.A == 0 {
		return
	}
	b := img.Bounds()
	r := clampRadius(rect, radius)
	mask := image.NewAlpha(b)
	stride := mask.Stride
	pix := mask.Pix
	neighbors := [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	// 描边只可能出现在「落在矩形内、但四邻中有一个落在矩形外」的像素上，
	// 这些像素必在距矩形边缘 r+1 以内的四条带内；只扫这四条带，
	// 避免对整幅图逐像素做 5 次浮点判断（@2x 时开销显著）。
	band := r + 1
	rows := [][2]int{
		{rect.Min.Y, min(rect.Min.Y+band, rect.Max.Y)},
		{max(rect.Min.Y, rect.Max.Y-band), rect.Max.Y},
	}
	for _, yb := range rows {
		for y := yb[0]; y < yb[1]; y++ {
			row := (y - b.Min.Y) * stride
			for x := rect.Min.X; x < rect.Max.X; x++ {
				if !insideRoundRect(rect, r, x, y) {
					continue
				}
				for _, d := range neighbors {
					if !insideRoundRect(rect, r, x+d[0], y+d[1]) {
						pix[row+(x-b.Min.X)] = 0xFF
						break
					}
				}
			}
		}
	}
	cols := [][2]int{
		{rect.Min.X, min(rect.Min.X+band, rect.Max.X)},
		{max(rect.Min.X, rect.Max.X-band), rect.Max.X},
	}
	// 上下带已扫过整行，纵向带只补中间段，避免重复
	yMid := [2]int{min(rect.Min.Y+band, rect.Max.Y), max(rect.Min.Y, rect.Max.Y-band)}
	for _, xb := range cols {
		for y := yMid[0]; y < yMid[1]; y++ {
			row := (y - b.Min.Y) * stride
			for x := xb[0]; x < xb[1]; x++ {
				if !insideRoundRect(rect, r, x, y) {
					continue
				}
				for _, d := range neighbors {
					if !insideRoundRect(rect, r, x+d[0], y+d[1]) {
						pix[row+(x-b.Min.X)] = 0xFF
						break
					}
				}
			}
		}
	}
	draw.DrawMask(img, b, &image.Uniform{c}, image.Point{}, mask, b.Min, draw.Over)
}

// clampRadius 把圆角半径钳制到矩形短边的一半。
func clampRadius(rect image.Rectangle, radius int) int {
	if radius <= 0 {
		return 0
	}
	if maxR := min(rect.Dx(), rect.Dy()) / 2; radius > maxR {
		return maxR
	}
	return radius
}

// insideRoundRect 判断像素（按像素中心）是否落在圆角矩形内（无抗锯齿）。
// 内联的比较代替 math.Max：候选窗每次击键都渲染，math.Max 的调用开销在
// 逐像素路径上不可忽略（profile 显示占边框绘制的大头）。
func insideRoundRect(rect image.Rectangle, r, px, py int) bool {
	if px < rect.Min.X || px >= rect.Max.X || py < rect.Min.Y || py >= rect.Max.Y {
		return false
	}
	if r <= 0 {
		return true
	}
	// 以像素中心为判断点，圆心取四个角各自的内切圆心
	fx, fy := float64(px)+0.5, float64(py)+0.5
	var dx, dy float64
	if lo := float64(rect.Min.X + r); fx < lo {
		dx = lo - fx
	} else if hi := float64(rect.Max.X - r); fx > hi {
		dx = fx - hi
	}
	if lo := float64(rect.Min.Y + r); fy < lo {
		dy = lo - fy
	} else if hi := float64(rect.Max.Y - r); fy > hi {
		dy = fy - hi
	}
	return dx*dx+dy*dy <= float64(r*r)
}

// drawInnerBorder 在距边缘 inset 像素处画一圈 1px 细线（Alpha=0 时跳过）。
func drawInnerBorder(img *image.NRGBA, c color.NRGBA, inset int) {
	if c.A == 0 || inset <= 0 {
		return
	}
	b := img.Bounds()
	if b.Dx() <= inset*2+1 || b.Dy() <= inset*2+1 {
		return
	}
	u := &image.Uniform{c}
	draw.Draw(img, image.Rect(b.Min.X+inset, b.Min.Y+inset, b.Max.X-inset, b.Min.Y+inset+1), u, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(b.Min.X+inset, b.Max.Y-inset-1, b.Max.X-inset, b.Max.Y-inset), u, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(b.Min.X+inset, b.Min.Y+inset+1, b.Min.X+inset+1, b.Max.Y-inset-1), u, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(b.Max.X-inset-1, b.Min.Y+inset+1, b.Max.X-inset, b.Max.Y-inset-1), u, image.Point{}, draw.Over)
}

func drawBorder(img *image.NRGBA, c color.NRGBA) {
	b := img.Bounds()
	for x := b.Min.X; x < b.Max.X; x++ {
		img.Set(x, b.Min.Y, c)
		img.Set(x, b.Max.Y-1, c)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		img.Set(b.Min.X, y, c)
		img.Set(b.Max.X-1, y, c)
	}
}

// blendPixel 将 src 颜色以 Alpha 混合到 (x, y) 像素上。
func blendPixel(dst *image.NRGBA, x, y int, src color.NRGBA) {
	if src.A == 0 {
		return
	}
	b := dst.Bounds()
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		return
	}
	bg := dst.NRGBAAt(x, y)
	if bg.A == 0 {
		dst.SetNRGBA(x, y, src)
		return
	}
	sa := float64(src.A) / 255.0
	ba := float64(bg.A) / 255.0
	outA := sa + ba*(1.0-sa)
	if outA <= 0 {
		return
	}
	r := (float64(src.R)*sa + float64(bg.R)*ba*(1.0-sa)) / outA
	g := (float64(src.G)*sa + float64(bg.G)*ba*(1.0-sa)) / outA
	bVal := (float64(src.B)*sa + float64(bg.B)*ba*(1.0-sa)) / outA
	dst.SetNRGBA(x, y, color.NRGBA{
		R: uint8(math.Round(r)),
		G: uint8(math.Round(g)),
		B: uint8(math.Round(bVal)),
		A: uint8(math.Round(outA * 255)),
	})
}

// drawCapsule 绘制带有独立左右圆角的高亮背景胶囊（首选承接面板左侧圆角）。
func drawCapsule(dst *image.NRGBA, r image.Rectangle, rLeft, rRight int, c color.NRGBA) {
	r = r.Intersect(dst.Bounds())
	if r.Empty() {
		return
	}
	w, h := r.Dx(), r.Dy()
	if rLeft <= 0 && rRight <= 0 {
		draw.Draw(dst, r, &image.Uniform{c}, image.Point{}, draw.Over)
		return
	}
	rlf, rrf := float64(rLeft), float64(rRight)
	for dy := 0; dy < h; dy++ {
		py := r.Min.Y + dy
		for dx := 0; dx < w; dx++ {
			px := r.Min.X + dx
			var cx, cy, rad float64
			inCorner := false
			if dx < rLeft && dy < rLeft {
				cx, cy, rad = rlf-0.5, rlf-0.5, rlf
				inCorner = true
			} else if dx >= w-rRight && dy < rRight {
				cx, cy, rad = float64(w)-rrf-0.5, rrf-0.5, rrf
				inCorner = true
			} else if dx < rLeft && dy >= h-rLeft {
				cx, cy, rad = rlf-0.5, float64(h)-rlf-0.5, rlf
				inCorner = true
			} else if dx >= w-rRight && dy >= h-rRight {
				cx, cy, rad = float64(w)-rrf-0.5, float64(h)-rrf-0.5, rrf
				inCorner = true
			}
			alpha := float64(c.A) / 255.0
			if inCorner {
				dist := math.Hypot(float64(dx)-cx, float64(dy)-cy)
				if dist > rad+0.5 {
					continue
				} else if dist > rad-0.5 {
					cov := rad + 0.5 - dist
					alpha *= cov
				}
			}
			outColor := c
			outColor.A = uint8(math.Round(alpha * 255))
			blendPixel(dst, px, py, outColor)
		}
	}
}

// drawChevron 绘制 macOS 系统风格的极简折叠下拉箭头 ⌄。
func drawChevron(dst *image.NRGBA, cx, cy int, arm int, c color.NRGBA) {
	for dx := -arm; dx <= arm; dx++ {
		dy := int(math.Abs(float64(dx)) * 0.7)
		blendPixel(dst, cx+dx, cy+dy-1, c)
		blendPixel(dst, cx+dx, cy+dy, c)
	}
}
