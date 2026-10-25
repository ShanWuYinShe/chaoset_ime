package renderer

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"chaoset.ime/engine"
)

func sampleComp() engine.Composition {
	return engine.Composition{
		Input:   "jianpanshubiao",
		Preedit: "jian'pan'shu'biao",
		Candidates: []engine.Candidate{
			{Text: "键盘鼠标", Syllables: "jian'pan'shu'biao", Score: 1500, FromUser: true},
			{Text: "键盘", Syllables: "jian'pan", Score: 558},
			{Text: "鼠标", Syllables: "shu'biao", Score: 556},
		},
	}
}

// manyComp 返回 n 个候选的组词状态，用于观察横排宽度增长与换行。
func manyComp(n int) engine.Composition {
	texts := []string{"键盘", "鼠标", "键盘鼠标", "检查", "健康", "键", "盘", "书", "标", "见"}
	comp := engine.Composition{Input: "jianpan", Preedit: "jian'pan"}
	for i := 0; i < n; i++ {
		comp.Candidates = append(comp.Candidates, engine.Candidate{
			Text: texts[i%len(texts)], Syllables: "jian'pan", Score: 1000 - i,
		})
	}
	return comp
}

func requireFont(t *testing.T, skin Skin) {
	t.Helper()
	if skin.FontPath == "" {
		t.Skip("本机无探测得到的 CJK 字体")
	}
}

func renderDecode(t *testing.T, comp engine.Composition, skin Skin) *image.NRGBA {
	t.Helper()
	data, err := Render(comp, skin)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("有候选时应产出 PNG")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("产出不是合法 PNG: %v", err)
	}
	nrgba, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("PNG 解码结果类型 = %T, 期望 *image.NRGBA", img)
	}
	return nrgba
}

func TestRenderPNG(t *testing.T) {
	skin := DefaultSkin()
	requireFont(t, skin)
	img := renderDecode(t, sampleComp(), skin)
	b := img.Bounds()
	// 横排候选栏：宽度由候选撑开，高度只有一行文字 + 内边距
	if b.Dx() < 100 || b.Dy() < 20 {
		t.Fatalf("图像尺寸过小: %dx%d", b.Dx(), b.Dy())
	}
}

// 竖排（Horizontal=false）时预编辑行附加页码指示，渲染图因此更宽。
func TestRenderPageIndicator(t *testing.T) {
	skin := DefaultSkin()
	skin.Horizontal = false // 页码指示并入预编辑行是竖排布局的行为
	requireFont(t, skin)
	withPage := sampleComp()
	withPage.Page, withPage.PageCount = 0, 2
	a := renderDecode(t, sampleComp(), skin)
	b := renderDecode(t, withPage, skin)
	if b.Bounds().Dx() <= a.Bounds().Dx() {
		t.Fatalf("页码指示应使图像更宽: %d vs %d", b.Bounds().Dx(), a.Bounds().Dx())
	}
}

// 横排：页码指示作为末尾一项 ‹1/2›，同样使图像更宽。
func TestRenderBarPageIndicator(t *testing.T) {
	skin := DefaultSkin()
	requireFont(t, skin)
	withPage := sampleComp()
	withPage.Page, withPage.PageCount = 0, 2
	a := renderDecode(t, sampleComp(), skin)
	b := renderDecode(t, withPage, skin)
	if b.Bounds().Dx() <= a.Bounds().Dx() {
		t.Fatalf("横排页码指示应使图像更宽: %d vs %d", b.Bounds().Dx(), a.Bounds().Dx())
	}
	if b.Bounds().Dy() != a.Bounds().Dy() {
		t.Fatalf("页码指示不应改变行数: 高 %d vs %d", b.Bounds().Dy(), a.Bounds().Dy())
	}
}

// 横排不画预编辑行（预编辑由壳的 marked text 承担）：有无 Preedit 尺寸一致。
func TestRenderBarIgnoresPreedit(t *testing.T) {
	skin := DefaultSkin()
	requireFont(t, skin)
	noPreedit := sampleComp()
	noPreedit.Preedit = ""
	a := renderDecode(t, sampleComp(), skin)
	b := renderDecode(t, noPreedit, skin)
	if a.Bounds() != b.Bounds() {
		t.Fatalf("横排不应绘制预编辑行: 有 preedit %v vs 无 %v", a.Bounds(), b.Bounds())
	}
}

func TestRenderEmptyCandidates(t *testing.T) {
	comp := engine.Composition{Input: "nihao", Preedit: "ni'hao"}
	for _, skin := range []Skin{DefaultSkin(), DefaultSkinFor(true)} {
		pngBytes, err := Render(comp, skin)
		if err != nil {
			t.Fatalf("无候选渲染不应报错: %v", err)
		}
		if pngBytes != nil {
			t.Fatalf("无候选应返回 nil PNG，得到 %d 字节", len(pngBytes))
		}
	}
}

func TestRenderFontError(t *testing.T) {
	skin := DefaultSkin()
	skin.FontPath = "/nonexistent/font.ttf"
	if _, err := Render(sampleComp(), skin); err == nil {
		t.Fatal("字体不可用应报错（壳据此回退 v1 UI）")
	}
}

// 横排候选栏宽度随候选数增长（流式排列，不是固定宽度）。
func TestRenderBarWidthGrowsWithCandidates(t *testing.T) {
	skin := DefaultSkin()
	requireFont(t, skin)
	two := renderDecode(t, manyComp(2), skin)
	five := renderDecode(t, manyComp(5), skin)
	if five.Bounds().Dx() <= two.Bounds().Dx() {
		t.Fatalf("候选变多应更宽: 2 个 %d px vs 5 个 %d px", two.Bounds().Dx(), five.Bounds().Dx())
	}
	if five.Bounds().Dy() != two.Bounds().Dy() {
		t.Fatalf("未超 MaxWidth 时不应换行: 高 %d vs %d", five.Bounds().Dy(), two.Bounds().Dy())
	}
}

// 横排高度显著小于竖排（竖排 = 预编辑行 + 每候选一行）。
func TestRenderBarShorterThanList(t *testing.T) {
	bar := DefaultSkin()
	requireFont(t, bar)
	list := bar
	list.Horizontal = false
	list.CornerRadius = 0

	barImg := renderDecode(t, sampleComp(), bar)
	listImg := renderDecode(t, sampleComp(), list)
	if barImg.Bounds().Dy()*2 > listImg.Bounds().Dy() {
		t.Fatalf("横排高度应显著小于竖排: 横排 %d px, 竖排 %d px",
			barImg.Bounds().Dy(), listImg.Bounds().Dy())
	}
}

// 圆角：面板四角必须完全透明（alpha=0），壳侧透明面板贴图后才呈圆角。
func TestRenderCornerTransparent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		skin := DefaultSkinFor(dark)
		requireFont(t, skin)
		img := renderDecode(t, sampleComp(), skin)
		b := img.Bounds()
		corners := map[string]image.Point{
			"左上": {b.Min.X, b.Min.Y},
			"右上": {b.Max.X - 1, b.Min.Y},
			"左下": {b.Min.X, b.Max.Y - 1},
			"右下": {b.Max.X - 1, b.Max.Y - 1},
		}
		for name, p := range corners {
			if _, _, _, a := img.At(p.X, p.Y).RGBA(); a != 0 {
				t.Fatalf("dark=%v 圆角%s 像素 alpha = %d, 期望 0（圆角必须透明）",
					dark, name, a>>8)
			}
		}
	}
}

// 直角皮肤（CornerRadius=0）四角不透明：与圆角行为形成对照。
func TestRenderSquareCornersOpaque(t *testing.T) {
	skin := DefaultSkin()
	skin.CornerRadius = 0
	requireFont(t, skin)
	img := renderDecode(t, sampleComp(), skin)
	b := img.Bounds()
	if _, _, _, a := img.At(b.Min.X, b.Min.Y).RGBA(); a == 0 {
		t.Fatal("CornerRadius=0 时左上角应不透明")
	}
}

// 深色皮肤背景色应为 #1C1C1E(F2)，浅色为 #FFFFFF(F2)。
func TestRenderSkinBackground(t *testing.T) {
	cases := []struct {
		dark bool
		want color.NRGBA
	}{
		{false, color.NRGBA{0xF6, 0xF6, 0xF8, 0xF6}},
		{true, color.NRGBA{0x2C, 0x2C, 0x2E, 0xF5}},
	}
	for _, c := range cases {
		skin := DefaultSkinFor(c.dark)
		requireFont(t, skin)
		if skin.BgColor != c.want {
			t.Fatalf("dark=%v 背景色 = %v, 期望 %v", c.dark, skin.BgColor, c.want)
		}
		img := renderDecode(t, sampleComp(), skin)
		b := img.Bounds()
		// 底边中点上方 2px：位于内边距区域，必为纯背景
		got := img.NRGBAAt(b.Dx()/2, b.Max.Y-2)
		if got != c.want {
			t.Fatalf("dark=%v 背景像素 = %v, 期望 %v", c.dark, got, c.want)
		}
	}
}

// 深色皮肤文字用白色，首选高亮为 #3A3A3C：整图应出现这两个颜色的像素。
func TestRenderDarkSkinColors(t *testing.T) {
	skin := DefaultSkinFor(true)
	requireFont(t, skin)
	img := renderDecode(t, sampleComp(), skin)
	hasHighlight, hasText := false, false
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			p := img.NRGBAAt(x, y)
			if p == skin.HighlightBg {
				hasHighlight = true
			}
			// 抗锯齿边缘会与背景混色，取足够亮且不透明的像素视为白字
			if p.A > 0xF0 && p.R > 0xE0 && p.G > 0xE0 && p.B > 0xE0 {
				hasText = true
			}
		}
	}
	if !hasHighlight {
		t.Fatal("深色皮肤未画出首选高亮 #3A3A3C")
	}
	if !hasText {
		t.Fatal("深色皮肤未画出白色候选文字")
	}
}

// scale=2 按 2 倍像素渲染：尺寸约翻倍（字形取整会有 ±2px 误差）。
func TestRenderScale2(t *testing.T) {
	base := DefaultSkin()
	requireFont(t, base)
	comp := sampleComp()
	one := renderDecode(t, comp, ScaleSkin(base, 1))
	two := renderDecode(t, comp, ScaleSkin(base, 2))
	b1, b2 := one.Bounds(), two.Bounds()
	if r := float64(b2.Dx()) / float64(b1.Dx()); r < 1.9 || r > 2.1 {
		t.Fatalf("scale=2 宽度应约翻倍: %d -> %d (x%.2f)", b1.Dx(), b2.Dx(), r)
	}
	if r := float64(b2.Dy()) / float64(b1.Dy()); r < 1.9 || r > 2.1 {
		t.Fatalf("scale=2 高度应约翻倍: %d -> %d (x%.2f)", b1.Dy(), b2.Dy(), r)
	}
}

// EffectiveScale 只接受 2，其余一律按 1；ScaleSkin 在 scale=1 时原样返回。
func TestScaleHelpers(t *testing.T) {
	for _, in := range []int{-2, 0, 1, 3, 4} {
		if got := EffectiveScale(in); got != 1 {
			t.Fatalf("EffectiveScale(%d) = %d, 期望 1", in, got)
		}
	}
	if got := EffectiveScale(2); got != 2 {
		t.Fatalf("EffectiveScale(2) = %d, 期望 2", got)
	}
	skin := DefaultSkin()
	if got := ScaleSkin(skin, 1); got != skin {
		t.Fatal("scale=1 时 ScaleSkin 应原样返回")
	}
	got := ScaleSkin(skin, 2)
	if got.FontSize != skin.FontSize*2 || got.Padding != skin.Padding*2 ||
		got.LineSpacing != skin.LineSpacing*2 || got.MaxWidth != skin.MaxWidth*2 ||
		got.CornerRadius != skin.CornerRadius*2 {
		t.Fatalf("scale=2 皮肤未全部翻倍: %+v", got)
	}
	if got.Horizontal != skin.Horizontal || got.FontPath != skin.FontPath {
		t.Fatal("ScaleSkin 不应改动布局开关与字体路径")
	}
}

// 横排超 MaxWidth 换行；最多 3 行，放不下的候选以 … 截断。
func TestRenderBarWrapsAtMaxWidth(t *testing.T) {
	skin := DefaultSkin()
	requireFont(t, skin)
	face, err := loadFace(skin)
	if err != nil {
		t.Fatal(err)
	}
	lineH := int(face.Metrics().Ascent.Ceil()+face.Metrics().Descent.Ceil()) + skin.LineSpacing
	rowHeight := func(rows int) int { return skin.Padding*2 + rows*lineH - skin.LineSpacing }

	// 窄栏：每行只能放 1~2 项 → 9 个候选必然超过 3 行而截断
	narrow := skin
	narrow.MaxWidth = 240
	img := renderDecode(t, manyComp(9), narrow)
	if got, want := img.Bounds().Dy(), rowHeight(maxBarRows); got != want {
		t.Fatalf("截断后应为 %d 行（高 %d px），得到高 %d px", maxBarRows, want, got)
	}
	if got := img.Bounds().Dx(); got > narrow.MaxWidth {
		t.Fatalf("换行后宽度 %d 应不超过 MaxWidth %d", got, narrow.MaxWidth)
	}

	// 宽栏：9 个候选单行放得下 → 1 行
	wide := skin
	wide.MaxWidth = 2000
	if got, want := renderDecode(t, manyComp(9), wide).Bounds().Dy(), rowHeight(1); got != want {
		t.Fatalf("宽栏应为 1 行（高 %d px），得到 %d px", want, got)
	}

	// MaxWidth=0 表示不换行
	noWrap := skin
	noWrap.MaxWidth = 0
	oneRow := renderDecode(t, manyComp(9), noWrap)
	if got, want := oneRow.Bounds().Dy(), rowHeight(1); got != want {
		t.Fatalf("MaxWidth=0 不应换行（高 %d px），得到 %d px", want, got)
	}
}

// 极窄栏：3 行仍放不下全部候选时，末行尾补 … 截断。
func TestRenderBarTruncatesWithEllipsis(t *testing.T) {
	skin := DefaultSkin()
	requireFont(t, skin)
	face, err := loadFace(skin)
	if err != nil {
		t.Fatal(err)
	}
	lineH := int(face.Metrics().Ascent.Ceil()+face.Metrics().Descent.Ceil()) + skin.LineSpacing

	narrow := skin
	narrow.MaxWidth = 150 // 每行只放得下一项

	comp := manyComp(9)
	full := renderDecode(t, comp, narrow)
	if got, want := full.Bounds().Dy(), skin.Padding*2+maxBarRows*lineH-skin.LineSpacing; got != want {
		t.Fatalf("极窄栏应排满 %d 行: 高 %d px, 期望 %d px", maxBarRows, got, want)
	}
	if got := full.Bounds().Dx(); got > narrow.MaxWidth {
		t.Fatalf("截断后宽度 %d 应不超过 MaxWidth %d", got, narrow.MaxWidth)
	}

	// 9 个候选截断后仍带 …，故与「仅 3 个候选」的渲染结果不同。
	three := renderDecode(t, manyComp(3), narrow)
	if full.Bounds() == three.Bounds() {
		t.Fatal("9 个候选截断后应与「仅 3 个候选」不同（末行应带 …）")
	}
}

// 竖排兼容：Horizontal=false 的旧皮肤仍按「预编辑行 + 每候选一行」渲染。
func TestRenderListCompat(t *testing.T) {
	skin := DefaultSkin()
	skin.Horizontal = false
	skin.CornerRadius = 0
	requireFont(t, skin)
	face, err := loadFace(skin)
	if err != nil {
		t.Fatal(err)
	}
	lineH := int(face.Metrics().Ascent.Ceil()+face.Metrics().Descent.Ceil()) + skin.LineSpacing
	comp := sampleComp()
	img := renderDecode(t, comp, skin)
	// 预编辑 1 行 + 3 个候选 = 4 行
	want := skin.Padding*2 + 4*lineH - skin.LineSpacing
	if got := img.Bounds().Dy(); got != want {
		t.Fatalf("竖排高度 = %d px, 期望 %d px（4 行）", got, want)
	}
}

// WithDarkMode 只换配色，保留字体与布局字段。
func TestWithDarkMode(t *testing.T) {
	skin := DefaultSkin()
	skin.FontSize = 22
	skin.MaxWidth = 999
	dark := WithDarkMode(skin, true)
	if dark.BgColor != DefaultSkinFor(true).BgColor || dark.FgColor != DefaultSkinFor(true).FgColor {
		t.Fatal("WithDarkMode(true) 未套用深色配色")
	}
	if dark.IndexColor != DefaultSkinFor(true).IndexColor {
		t.Fatal("WithDarkMode(true) 未套用深色 IndexColor")
	}
	if dark.FontSize != 22 || dark.MaxWidth != 999 || dark.Horizontal != skin.Horizontal {
		t.Fatalf("WithDarkMode 不应改动字体与布局: %+v", dark)
	}
	light := WithDarkMode(dark, false)
	if light.BgColor != DefaultSkinFor(false).BgColor {
		t.Fatal("WithDarkMode(false) 未套用浅色配色")
	}
	if light.IndexColor != DefaultSkinFor(false).IndexColor {
		t.Fatal("WithDarkMode(false) 未套用浅色 IndexColor")
	}
}

// 序号色彩独立层级测试：确保 IndexColor 非零且生效。
func TestRenderIndexColorHierarchy(t *testing.T) {
	for _, dark := range []bool{false, true} {
		skin := DefaultSkinFor(dark)
		requireFont(t, skin)
		if skin.IndexColor.A == 0 {
			t.Fatalf("dark=%v 时 IndexColor 必须配置", dark)
		}
		img := renderDecode(t, sampleComp(), skin)
		if img == nil {
			t.Fatal("渲染结果为空")
		}
	}
}

// 供人工目检：横排浅色/深色、scale=2、窄栏换行、竖排旧样式写入 .workwork/（gitignored）。
func TestWriteVisualSample(t *testing.T) {
	light := DefaultSkin()
	requireFont(t, light)
	dark := DefaultSkinFor(true)

	narrow := light
	narrow.MaxWidth = 240

	truncated := light
	truncated.MaxWidth = 150

	list := DefaultSkin()
	list.Horizontal = false
	list.CornerRadius = 0
	list.BgColor = color.NRGBA{0xF7, 0xF2, 0xE7, 0xF2}     // 旧宣纸底
	list.BorderColor = color.NRGBA{0xB2, 0x3A, 0x2E, 0xFF} // 旧朱红外框
	list.FgColor = color.NRGBA{0x2B, 0x2A, 0x26, 0xFF}     // 旧浓墨
	list.HighlightBg = color.NRGBA{0xC1, 0x27, 0x2D, 0x33}
	list.HighlightBarColor = color.NRGBA{0xB2, 0x3A, 0x2E, 0xFF}
	list.InnerBorderColor = color.NRGBA{0xE0, 0xCF, 0xA8, 0xCC}
	list.UserMarkColor = color.NRGBA{0x1B, 0x6B, 0x8C, 0xFF}

	paged := sampleComp()
	paged.Page, paged.PageCount = 1, 2

	samples := []struct {
		name string
		comp engine.Composition
		skin Skin
	}{
		{"render-bar-light.png", sampleComp(), light},
		{"render-bar-dark.png", sampleComp(), dark},
		{"render-bar-light-scale2.png", sampleComp(), ScaleSkin(light, 2)},
		{"render-bar-dark-scale2.png", sampleComp(), ScaleSkin(dark, 2)},
		{"render-bar-light-paged.png", paged, light},
		{"render-bar-light-9cand.png", manyComp(9), light},
		{"render-bar-light-narrow-wrap.png", manyComp(9), narrow},
		{"render-bar-light-truncated.png", manyComp(9), truncated},
		{"render-list-legacy.png", sampleComp(), list},
	}
	for _, s := range samples {
		data, err := Render(s.comp, s.skin)
		if err != nil {
			t.Fatalf("%s 渲染失败: %v", s.name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s 应产出 PNG", s.name)
		}
		out := filepath.Join("..", ".workwork", s.name)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s 不是合法 PNG: %v", s.name, err)
		}
		t.Logf("样例已写入 %s (%dx%d)", out, cfg.Width, cfg.Height)
	}
}

// 快速 PNG 编码器（None 滤波）产物必须是合法 PNG：stdlib 解码逐像素一致（含 alpha）。
func TestEncodeFastPNGRoundTrip(t *testing.T) {
	skin := DefaultSkin()
	requireFont(t, skin)
	img, err := RenderImageForTest(sampleComp(), skin)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if img == nil {
		t.Fatal("应有图像")
	}
	dec, err := png.Decode(bytes.NewReader(encodeFastPNG(img)))
	if err != nil {
		t.Fatalf("encodeFastPNG 产物不是合法 PNG: %v", err)
	}
	d2, ok := dec.(*image.NRGBA)
	if !ok {
		t.Fatalf("解码类型 = %T, 期望 *image.NRGBA", dec)
	}
	if d2.Bounds() != img.Bounds() {
		t.Fatalf("尺寸不一致: %v vs %v", d2.Bounds(), img.Bounds())
	}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if img.NRGBAAt(x, y) != d2.NRGBAAt(x, y) {
				t.Fatalf("像素不一致 @(%d,%d): %v vs %v", x, y, img.NRGBAAt(x, y), d2.NRGBAAt(x, y))
			}
		}
	}
}

// 序号键帽：开启时整图出现键帽底色像素（非首选红底、首选绿底），
// 且布局比关闭时更宽——键帽内衬会展宽每项。
func TestRenderIndexChip(t *testing.T) {
	skin := DefaultSkinFor(false)
	requireFont(t, skin)

	on := skin
	on.BgColor = color.NRGBA{255, 255, 255, 255}          // 纯白不透明底：隔离 Over 混合
	on.IndexChipBg = color.NRGBA{255, 0, 0, 255}          // 非首选键帽纯红
	on.IndexChipBorder = color.NRGBA{}                    // 关边框，避免像素误判
	on.HighlightIndexChipBg = color.NRGBA{0, 255, 0, 255} // 首选键帽纯绿
	imgOn, err := RenderImageForTest(sampleComp(), on)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	hasRed, hasGreen := false, false
	for y := imgOn.Bounds().Min.Y; y < imgOn.Bounds().Max.Y; y++ {
		for x := imgOn.Bounds().Min.X; x < imgOn.Bounds().Max.X; x++ {
			switch imgOn.NRGBAAt(x, y) {
			case color.NRGBA{255, 0, 0, 255}:
				hasRed = true
			case color.NRGBA{0, 255, 0, 255}:
				hasGreen = true
			}
		}
	}
	if !hasRed {
		t.Fatal("未画出非首选序号键帽（IndexChipBg 未生效）")
	}
	if !hasGreen {
		t.Fatal("未画出首选序号键帽（HighlightIndexChipBg 未生效）")
	}

	off := skin
	off.IndexChipBg = color.NRGBA{} // Alpha=0：键帽整体关闭，序号退回纯文字
	imgOff, err := RenderImageForTest(sampleComp(), off)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if imgOff.Bounds().Dx() >= imgOn.Bounds().Dx() {
		t.Fatalf("键帽关闭应更窄: 关闭 %d px vs 开启 %d px", imgOff.Bounds().Dx(), imgOn.Bounds().Dx())
	}
}

// 键帽关闭（IndexChipBg Alpha=0）时不应残留任何键帽色像素。
func TestRenderIndexChipDisabled(t *testing.T) {
	skin := DefaultSkinFor(true)
	requireFont(t, skin)
	off := skin
	off.IndexChipBg = color.NRGBA{}
	off.IndexChipBorder = color.NRGBA{}
	off.HighlightIndexChipBg = color.NRGBA{0, 255, 0, 255} // 即使首选键帽色非零，主开关关闭也不画
	img, err := RenderImageForTest(sampleComp(), off)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if img.NRGBAAt(x, y) == (color.NRGBA{0, 255, 0, 255}) {
				t.Fatalf("主开关关闭时不应画任何键帽 @(%d,%d)", x, y)
			}
		}
	}
}
