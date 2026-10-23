// Package renderer 把组词状态渲染为候选窗 PNG——「daemon 渲染、壳贴图」
// 的统一候选窗方案：三端壳显示同一张图，皮肤与布局只收敛在 daemon 一处。
// 纯 Go（x/image 栅格化 + stdlib PNG），渲染结果可单测、可人工目检。
//
// 默认皮肤 DefaultSkin() 为系统输入法风格：半透明圆角底 + 1px 细边框 +
// 横排候选栏（首选浅灰高亮）；DefaultSkinFor(true) 给出深色版。
// Skin.Horizontal=false 时退回旧的竖排列表布局（预编辑行 + 每候选一行），
// 该路径保留给旧调用方，配色与装帧（朱批竖记、内框细线）完全由 Skin 字段决定。
package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
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
	FontPath          string      // CJK 字体（TTF/TTC）；空串则按平台常见路径探测
	FontSize          float64     // 字号（pt）
	Padding           int         // 内边距（px）
	LineSpacing       int         // 行间距（px）
	BgColor           color.NRGBA // 背景半透明
	BorderColor       color.NRGBA // 外框
	InnerBorderColor  color.NRGBA // 内框细线（竖排/印章双框语汇）；Alpha=0 时不画
	PreeditColor      color.NRGBA // 预编辑行文字；横排时兼作页码指示等淡色文字
	FgColor           color.NRGBA // 候选文字
	HighlightBg       color.NRGBA // 首选候选底色
	HighlightBarColor color.NRGBA // 首选候选左侧「朱批」竖记；Alpha=0 时不画
	UserMarkColor     color.NRGBA // 用户学习标记（*）颜色
	IndexColor        color.NRGBA // 候选序号颜色；Alpha=0 时退回 PreeditColor
	HighlightFg       color.NRGBA // 首选项文字高亮色；Alpha=0 时退回 FgColor

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
// 配色取系统语义色：浅色 白底/深灰字/浅灰首选高亮，深色 近黑底/白字/深灰高亮，
// 两者都是 1px 半透明细边框（贴图叠在任意桌面背景上都自然）。
func DefaultSkinFor(dark bool) Skin {
	skin := Skin{
		FontPath:          probeFontPath(),
		FontSize:          15,
		Padding:           8,
		LineSpacing:       4,
		Horizontal:        true,
		MaxWidth:          defaultMaxWidth,
		CornerRadius:      defaultCornerRadius,
		HighlightBarColor: color.NRGBA{}, // 系统风格不画朱批竖记
	}
	if dark {
		skin.BgColor = color.NRGBA{0x1C, 0x1C, 0x1E, 0xF2}       // 近黑底
		skin.BorderColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0x20}   // 白色细边
		skin.PreeditColor = color.NRGBA{0x98, 0x98, 0x9F, 0xFF}  // 次级文字
		skin.IndexColor = color.NRGBA{0x98, 0x98, 0x9F, 0xFF}    // 序号采用静音灰，与候选字层次分明
		skin.FgColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}       // 白字
		skin.HighlightBg = color.NRGBA{0x3A, 0x3A, 0x3C, 0xFF}   // 首选高亮
		skin.HighlightFg = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}   // 首选高亮文字
		skin.UserMarkColor = color.NRGBA{0x0A, 0x84, 0xFF, 0xFF} // 学习标记
		return skin
	}
	skin.BgColor = color.NRGBA{0xFF, 0xFF, 0xFF, 0xF2}       // 白底
	skin.BorderColor = color.NRGBA{0x00, 0x00, 0x00, 0x18}   // 黑色细边
	skin.PreeditColor = color.NRGBA{0x8E, 0x8E, 0x93, 0xFF}  // 次级文字
	skin.IndexColor = color.NRGBA{0x8E, 0x8E, 0x93, 0xFF}    // 序号采用系统次级灰，扫视更轻松
	skin.FgColor = color.NRGBA{0x1C, 0x1C, 0x1E, 0xFF}       // 深灰字
	skin.HighlightBg = color.NRGBA{0xE5, 0xE5, 0xEA, 0xFF}   // 首选高亮（浅灰，文字保持深色）
	skin.HighlightFg = color.NRGBA{0x1C, 0x1C, 0x1E, 0xFF}   // 首选高亮文字
	skin.UserMarkColor = color.NRGBA{0x00, 0x7A, 0xFF, 0xFF} // 学习标记
	return skin
}

// DefaultSkin 返回默认皮肤：系统风格浅色横排候选栏，等价于 DefaultSkinFor(false)。
// 竖排 + 宣纸/朱红装帧的旧观感仍可用 Horizontal=false 的皮肤复现（见 Skin 字段）。
func DefaultSkin() Skin {
	return DefaultSkinFor(false)
}

// WithDarkMode 把皮肤配色切换为浅色/深色主题，保留字体与布局字段
// （FontPath/FontSize/Padding/LineSpacing/Horizontal/MaxWidth/CornerRadius）。
// daemon 用 -font/-fontsize 定制字体后，壳仍可只按外观切换配色。
func WithDarkMode(skin Skin, dark bool) Skin {
	theme := DefaultSkinFor(dark)
	skin.BgColor = theme.BgColor
	skin.BorderColor = theme.BorderColor
	skin.InnerBorderColor = theme.InnerBorderColor
	skin.PreeditColor = theme.PreeditColor
	skin.FgColor = theme.FgColor
	skin.HighlightBg = theme.HighlightBg
	skin.HighlightFg = theme.HighlightFg
	skin.HighlightBarColor = theme.HighlightBarColor
	skin.UserMarkColor = theme.UserMarkColor
	skin.IndexColor = theme.IndexColor
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
	faceMu  sync.Mutex
	faceMon = map[faceKey]font.Face{}
)

func loadFace(s Skin) (font.Face, error) {
	if s.FontPath == "" {
		return nil, fmt.Errorf("未找到 CJK 字体（请用 -font 指定路径）")
	}
	if s.FontSize <= 0 {
		return nil, fmt.Errorf("字号必须为正")
	}
	faceMu.Lock()
	defer faceMu.Unlock()
	key := faceKey{s.FontPath, s.FontSize}
	if f, ok := faceMon[key]; ok {
		return f, nil
	}
	data, err := os.ReadFile(s.FontPath)
	if err != nil {
		return nil, fmt.Errorf("读取字体失败: %w", err)
	}
	// TTC 字体集取第一个 face；解析失败则按单字体 TTF 再试
	faceFont, ferr := parseFont(data)
	if ferr != nil {
		return nil, fmt.Errorf("解析字体 %s 失败: %w", s.FontPath, ferr)
	}
	face, err := opentype.NewFace(faceFont, &opentype.FaceOptions{
		Size:    s.FontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("创建字体面失败: %w", err)
	}
	faceMon[key] = face
	return face, nil
}

func parseFont(data []byte) (*opentype.Font, error) {
	if coll, err := opentype.ParseCollection(data); err == nil {
		return coll.Font(0)
	}
	return opentype.Parse(data)
}

var renderMu sync.Mutex // 字体面并发不安全；渲染串行化（耗时微秒级）

// Render 把组词状态画成候选窗 PNG。
// 无候选时返回 (nil, nil)（壳应隐藏窗口）；字体不可用时返回错误，
// 壳应回退到各平台 v1 候选 UI。
// Skin.Horizontal=true 走横排候选栏，否则走旧的竖排列表。
func Render(comp engine.Composition, skin Skin) ([]byte, error) {
	face, err := loadFace(skin)
	if err != nil {
		return nil, err
	}
	if len(comp.Candidates) == 0 {
		return nil, nil
	}

	renderMu.Lock()
	defer renderMu.Unlock()

	var img *image.NRGBA
	if skin.Horizontal {
		img = renderBar(comp, skin, face)
	} else {
		img = renderList(comp, skin, face)
	}
	if img == nil {
		return nil, nil
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("编码 PNG 失败: %w", err)
	}
	return buf.Bytes(), nil
}

// renderBar 画横排候选栏：候选横向流式排列，首项圆角高亮，页码指示为末尾淡色项。
// 横排不画预编辑行（预编辑由壳的 marked text 承担）。
func renderBar(comp engine.Composition, skin Skin, face font.Face) *image.NRGBA {
	pad := skin.Padding
	if pad <= 0 {
		pad = 8
	}
	metrics := face.Metrics()
	ascent, descent := metrics.Ascent.Ceil(), metrics.Descent.Ceil()
	textH := ascent + descent
	spacing := skin.LineSpacing
	if spacing < 0 {
		spacing = 0
	}
	lineH := textH + spacing
	itemPadX := pad / 2 // 高亮块内文字左右留白
	if itemPadX < 2 {
		itemPadX = 2
	}
	gap := pad / 2 // 相邻候选项间距
	if gap < 2 {
		gap = 2
	}

	idxColor := skin.IndexColor
	if idxColor.A == 0 {
		idxColor = skin.PreeditColor
	}

	items := make([]barItem, 0, len(comp.Candidates)+1)
	for i, cand := range comp.Candidates {
		idxText := fmt.Sprintf("%d", i+1)
		candFg := skin.FgColor
		if i == 0 && skin.HighlightFg.A != 0 {
			candFg = skin.HighlightFg
		}
		items = append(items, newBarCandidateItem(face, idxText, cand.Text, candFg, idxColor, itemPadX, i == 0, cand.FromUser))
	}
	// 多页时页码指示作为末尾一项，用淡色（与旧竖排的 [1/2] 语义一致）
	if comp.PageCount > 1 {
		text := pageIndicator(face, comp.Page+1, comp.PageCount)
		items = append(items, newBarSimpleItem(face, text, skin.PreeditColor, itemPadX))
	}
	if len(items) == 0 {
		return nil
	}

	// maxContent = 单行可用内容宽（不含左右内边距）；0 表示不换行
	maxContent := 0
	if skin.MaxWidth > 0 {
		maxContent = skin.MaxWidth - pad*2
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
		// 3 行仍放不下：末行尾补省略号（必要时先丢弃末行末尾项）
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
		if w := rowWidth(row, gap) + pad*2; w > width {
			width = w
		}
	}
	height := pad*2 + len(rows)*lineH - spacing
	if width <= 0 || height <= 0 {
		return nil
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	drawPanel(img, skin)

	d := &font.Drawer{Dst: img, Face: face}
	y := pad
	for _, row := range rows {
		x := pad
		for _, it := range row {
			box := image.Rect(x, y, x+it.boxW, y+textH)
			if it.highlight {
				// 首选高亮：圆角微胶囊块（CornerRadius=0 时即整项直角底色）
				pillR := clampRadius(box, skin.CornerRadius)
				if pillR > 6 {
					pillR = 6
				}
				drawRoundRect(img, box, pillR, skin.HighlightBg, draw.Over)
			}
			curX := x + itemPadX
			if it.indexText != "" {
				d.Src = image.NewUniform(it.idxColor)
				d.Dot = fixed.P(curX, y+ascent)
				d.DrawString(it.indexText)
				curX += it.indexW + it.gapW
			}
			d.Src = image.NewUniform(it.color)
			d.Dot = fixed.P(curX, y+ascent)
			d.DrawString(it.text)
			curX += it.textW
			if it.userMark && skin.UserMarkColor.A != 0 {
				// 学习标记：行尾 * 换色强调（单独绘制）
				d.Src = image.NewUniform(skin.UserMarkColor)
				d.Dot = fixed.P(curX, y+ascent)
				d.DrawString("*")
			}
			x += it.boxW + gap
		}
		y += lineH
	}
	return img
}

// pageIndicator 生成页码指示文本，形如 ‹1/5›。
// 并非所有 CJK 字体都含 U+2039/U+203A（本机 Hiragino Sans GB 即缺，
// 直接用会渲染成缺字方块），故按可用性回退到 CJK 尖括号 〈〉、再退 ASCII 尖括号。
func pageIndicator(face font.Face, page, pageCount int) string {
	pairs := [][2]string{{"‹", "›"}, {"〈", "〉"}, {"<", ">"}}
	l, r := "<", ">"
	for _, pair := range pairs {
		if hasGlyph(face, []rune(pair[0])[0]) && hasGlyph(face, []rune(pair[1])[0]) {
			l, r = pair[0], pair[1]
			break
		}
	}
	return fmt.Sprintf("%s%d/%d%s", l, page, pageCount, r)
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
	indexW    int // 序号像素宽
	gapW      int // 序号与候选词间距像素宽
	textW     int // 文本像素宽（不含序号、留白与标记）
	markW     int // 学习标记宽
	boxW      int // 含左右留白的整项宽
}

func newBarCandidateItem(face font.Face, idxText, text string, c, idxC color.NRGBA, itemPadX int, highlight, userMark bool) barItem {
	iw := font.MeasureString(face, idxText).Ceil()
	// 序号与候选词微间隙：约为序号宽度的 35%（微小精细空隙，比空格更紧凑整齐）
	gw := max(3, iw*35/100)
	tw := font.MeasureString(face, text).Ceil()
	mw := 0
	if userMark {
		mw = font.MeasureString(face, "*").Ceil()
	}
	boxW := itemPadX*2 + iw + gw + tw + mw
	return barItem{
		indexText: idxText,
		text:      text,
		color:     c,
		idxColor:  idxC,
		highlight: highlight,
		userMark:  userMark,
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
func drawRoundRect(img *image.NRGBA, rect image.Rectangle, radius int, c color.NRGBA, op draw.Op) {
	rect = rect.Intersect(img.Bounds())
	if rect.Empty() {
		return
	}
	if radius <= 0 {
		draw.Draw(img, rect, &image.Uniform{c}, image.Point{}, op)
		return
	}
	mask := roundRectMask(rect, radius)
	draw.DrawMask(img, rect, &image.Uniform{c}, image.Point{}, mask, rect.Min, op)
}

// roundRectMask 生成圆角矩形遮罩（圆角外 alpha=0）。
// 直写 Pix 而非 SetAlpha：候选窗每次击键都要渲染，@2x 时逐像素调用开销显著。
func roundRectMask(rect image.Rectangle, radius int) *image.Alpha {
	m := image.NewAlpha(rect)
	r := clampRadius(rect, radius)
	stride := m.Stride
	pix := m.Pix
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		row := (y - rect.Min.Y) * stride
		for x := rect.Min.X; x < rect.Max.X; x++ {
			if insideRoundRect(rect, r, x, y) {
				pix[row+(x-rect.Min.X)] = 0xFF
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
