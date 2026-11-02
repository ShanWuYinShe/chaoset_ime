package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

// mustCommonDict 加载仓库内置常用词库（data/dict_common.tsv），
// 用于对真实词库规模做回归断言。
func mustCommonDict(t *testing.T) *Dictionary {
	t.Helper()
	d, err := LoadDictFiles([]string{filepath.Join("..", "data", "dict_common.tsv")})
	if err != nil {
		t.Fatalf("加载内置常用词库失败: %v", err)
	}
	return d
}

// 用户反馈的真实场景：输入 jingchang 必须能打出「经常」。
// 旧词库（terra 单字表）里没有这个词，导致该输入完全无候选。
func TestCommonDictJingchang(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp, err := s.Input("jingchang")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 {
		t.Fatal("jingchang 应有候选（内置词库缺「经常」会导致无候选）")
	}
	if got := comp.Candidates[0].Text; got != "经常" {
		t.Fatalf("jingchang 第一候选 = %q, 期望「经常」；候选 = %+v", got, comp.Candidates)
	}
}

// 单音节输入不能被生僻字淹没：jing 的第一候选应是常用字（词频最高者）。
func TestCommonDictSingleSyllableRanksCommonChar(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp, _ := s.Input("jing")
	if len(comp.Candidates) == 0 {
		t.Fatal("jing 应有候选")
	}
	if got := comp.Candidates[0].Text; got != "经" {
		t.Fatalf("jing 第一候选 = %q, 期望「经」（词频最高）", got)
	}
}

// 音节数完全匹配优先：打满两个音节时，二字词排在「以该序列开头的更长词」前面。
func TestExactSyllableCountPreferred(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	comp := allCandidates(s, mustComp(s, "jingchang"))
	var idxExact, idxLonger = -1, -1
	for i, c := range comp {
		switch c.Text {
		case "经常":
			idxExact = i
		case "经常化":
			idxLonger = i
		}
	}
	if idxExact < 0 || idxLonger < 0 {
		t.Fatalf("候选应同时含「经常」与「经常化」: %+v", comp)
	}
	if idxExact > idxLonger {
		t.Fatalf("完全匹配的「经常」应排在「经常化」前: 经常@%d 经常化@%d", idxExact, idxLonger)
	}
}

// 调频与「全消耗优先」的关系（2026-10-25 语义演进）：
// 学习过的长词（经常化，3 音节）不再压过「输入完全消耗」的短词（经常，
// 2 音节）——音节超段仲裁位于调频之前，准确匹配用户输入是硬约束。
// 学习效果体现为：「经常化」仍进入候选列表（此前根本查不到），排在
// 全消耗词之后；打满更多音节（jingchangh）时它回到首位。
func TestUserBoostBeatsExactMatch(t *testing.T) {
	udPath := filepath.Join(t.TempDir(), "user.json")
	d := mustCommonDict(t)
	s := NewSession(d, mustUserDict(t, udPath))
	// 打 jingchang 时选「经常化」（长词；可能落在后续页，翻页定位后按
	// 页内序号选择，Select 的 index 是页内相对序号）
	comp := mustComp(s, "jingchang")
	idx := -1
	for {
		for i, c := range comp.Candidates {
			if c.Text == "经常化" {
				idx = i
				break
			}
		}
		if idx >= 0 || comp.Page >= comp.PageCount-1 {
			break
		}
		next, err := s.NextPage()
		if err != nil {
			break
		}
		comp = next
	}
	if idx < 0 {
		t.Fatalf("候选应含「经常化」: %+v", comp.Candidates)
	}
	if _, err := s.Select(idx); err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	// 新会话加载同一份用户词典：
	// ① 全消耗的「经常」仍是第一（音节超段仲裁位于调频之前）
	s2 := NewSession(d, mustUserDict(t, udPath))
	comp2 := mustComp(s2, "jingchang")
	if len(comp2.Candidates) == 0 || comp2.Candidates[0].Text != "经常" {
		t.Fatalf("学习后全消耗词「经常」仍应第一，得到 %+v", comp2.Candidates)
	}
	// ② 「经常化」必须进入候选列表（学习生效；在本页或后续页）
	found := false
	for _, c := range allCandidates(s2, comp2) {
		if c.Text == "经常化" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("学习过的「经常化」应进入候选列表: %+v", comp2.Candidates)
	}
}

// 内置词库规模与词频区间回归：防止生成工具被改坏后词库退化。
func TestCommonDictScale(t *testing.T) {
	d := mustCommonDict(t)
	// 2026-11-01 起内置词库改用「常用性门槛」流水线（约 6.7 万条，旧 jieba 库
	// 14.5 万条），下限随产物口径调整；词条质量由 data_contract_test.go 的
	// 规范精度/手写词表断言把关，这里只防「词库静默缩水成空壳」。
	if d.Len() < 60000 {
		t.Fatalf("内置词库仅 %d 条，少于预期的 6 万条", d.Len())
	}
	// 常用词必须存在（缺词是旧词库最致命的问题）
	for _, w := range []string{"经常", "经济", "时间", "现在", "我们", "中国"} {
		found := false
		for _, e := range d.Entries() {
			if e.Word == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("内置词库缺少常用词 %q", w)
		}
	}
	// 词频区间：全部落在 1..99
	for _, e := range d.Entries() {
		if e.Freq < 1 || e.Freq > 99 {
			t.Fatalf("词条 %q 词频 %d 超出 1..99", e.Word, e.Freq)
		}
	}
}

// TestCommonDictSingleSyllableFullRecall：单音节必须**全量召回**同音字。
// 2026-10-28 用户反馈：输入 shi 打不出「示」——词频排 33 位的常用字被
// 精确匹配的 top20 裁剪挡在候选池外，翻页也不存在。修后同音字全部进入
// 候选池（落到后面的页），同时首选项与词组数量不受影响（双向回归）。
func TestCommonDictSingleSyllableFullRecall(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	comp, err := s.Input("shi")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if got := comp.Candidates[0].Text; got != "是" {
		t.Fatalf("shi 第一候选 = %q, 期望「是」（召回扩大不得冲乱首选）", got)
	}

	// 遍历全部页收集候选
	var all []Candidate
	page := comp
	for {
		all = append(all, page.Candidates...)
		if page.Page+1 >= page.PageCount {
			break
		}
		page, err = s.NextPage()
		if err != nil {
			t.Fatalf("NextPage 失败: %v", err)
		}
	}
	if comp.PageCount <= 1 {
		t.Fatalf("shi 应为多页（同音字 100+），实际 pageCount=%d", comp.PageCount)
	}

	// 正向：「示」必须在候选池中（此前不存在）
	findText := func(text string) (int, bool) {
		for i, c := range all {
			if c.Text == text {
				return i + 1, true
			}
		}
		return 0, false
	}
	pos, ok := findText("示")
	if !ok {
		t.Fatalf("shi 的全部 %d 条候选中找不到「示」——单音节召回仍被裁剪", len(all))
	}
	if pos > 60 {
		t.Fatalf("「示」位次 %d 过深（期望 ≤60，词频 50 属常用字）", pos)
	}

	// 反向：单段输入只出单字候选，prefix 前缀词（超过输入音节数的词）
	// 已不再出现；单字召回数 = 词典中 shi 单音节词条数（去繁体前 107、
	// 去繁体后 93）。
	singles := 0
	for _, c := range all {
		if !strings.Contains(c.Syllables, "'") {
			singles++
		}
	}
	dictSingles := len(d.QueryTokensExact([]Token{{Text: "shi", IsExact: true}}, 500))
	if singles != dictSingles {
		t.Fatalf("shi 单字召回 %d 应恰好等于词典同音字总数 %d（全量召回，不含繁体变体）", singles, dictSingles)
	}
	if dictSingles < 80 {
		t.Fatalf("词典 shi 同音字 %d 过少，词库生成疑似异常", dictSingles)
	}
	phrases := len(all) - singles
	if phrases != 0 {
		t.Fatalf("shi 单段输入不应出现前缀词候选，实际多出 %d 条", phrases)
	}
}

// TestCommonDictInitialsRecall：简拼必须能打出高频双音节词（正向），且
// 不反噬全拼首选（反向）。2026-10-28 扫描诊断：简拼精确匹配被 top20
// 裁剪，yb 的 229 个候选词里「一百」排 20 名之外完全不可达，initials
// 场景 miss 828/1455；exact 上限按形态放开到 120 后 miss 127。
func TestCommonDictInitialsRecall(t *testing.T) {
	s := NewSession(mustCommonDict(t), nil)
	probe := []struct{ input, word string }{
		{"yb", "一百"}, {"yy", "一一"}, {"sc", "上次"}, {"sj", "世界"},
	}
	for _, p := range probe {
		s.Reset()
		comp, err := s.Input(p.input)
		if err != nil {
			t.Fatalf("Input(%q) 失败: %v", p.input, err)
		}
		found := false
		page := comp
		for {
			for _, c := range page.Candidates {
				if c.Text == p.word {
					found = true
					break
				}
			}
			if found || page.Page+1 >= page.PageCount {
				break
			}
			if page, err = s.NextPage(); err != nil {
				break
			}
		}
		if !found {
			t.Errorf("简拼 %q 翻遍候选也打不出「%s」", p.input, p.word)
		}
	}

	// 反向：全拼首选不受 exact 放开影响
	s.Reset()
	comp, _ := s.Input("nihao")
	if got := comp.Candidates[0].Text; got != "你好" {
		t.Fatalf("nihao 第一候选 = %q, 期望「你好」", got)
	}
}

// TestCommonDictLongPinyinFirstSyllableFullRecall：>=4 音节的长拼音，逐级
// 回退到首音节时同音字必须全量可达（与单音节输入的 singleSyllableExactLimit
// 口径一致）。2026-11-01 用户反馈：输入较长的拼音、整词/整句候选都不符合
// 要求时想逐字挑选（SelectPrefix），但逐级回退只取 top10，首音节同音字
// 整体被截断。
//
// 回退本身只对 >=4 音节全量放开：2~3 音节是最高频输入形态，全量放进回退层
// 会让候选数量翻数倍（热路径 + 翻页观感双输）；2~3 音节的逐字需求由列表
// 末尾的单字兜底补全（见 TestCommonDictShortPinyinSingleFallbackAlways）。
func TestCommonDictLongPinyinFirstSyllableFullRecall(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct {
		input     string
		firstSyl  string
		word      string
		wantFirst string // 首选：最长已有词前缀；没有前缀词组时才是首音节单字
	}{
		{"changjiangdadao", "chang", "长", "长江"},         // 4 音节
		{"woaichangcheng", "wo", "我", "我"},              // 4 音节（wo 共 31 个同音字；无前缀词组）
		{"changjiangdadaodexingqi", "chang", "长", "长江"}, // 7 音节
	}
	for _, c := range cases {
		wantSingles := len(d.QueryTokensExact([]Token{{Text: c.firstSyl, IsExact: true}}, 500))
		if wantSingles < 20 {
			t.Fatalf("词典 %s 同音字仅 %d 个，词库生成疑似异常", c.firstSyl, wantSingles)
		}
		if wantSingles > singleSyllableExactLimit {
			wantSingles = singleSyllableExactLimit
		}
		s := NewSession(d, nil)
		all := allCandidates(s, mustComp(s, c.input))
		got := 0
		found := false
		for _, cand := range all {
			if cand.Syllables != c.firstSyl {
				continue
			}
			got++
			if cand.Text == c.word {
				found = true
			}
		}
		if got != wantSingles {
			t.Errorf("%s: 首音节 %s 单字召回 %d，期望全量 %d（>=4 音节未放开）",
				c.input, c.firstSyl, got, wantSingles)
		}
		if !found {
			t.Errorf("%s: 候选里必须有「%s」（用户反馈场景）", c.input, c.word)
		}
		// 反向：首音节单字只是逐字挑选的兜底。「最长已有词前缀」存在时它
		// 必须领跑；整串没有任何前缀词组时才轮到首音节单字（用户 2026-11-01
		// 明确要求：没有词组就回到拆成单字逐个匹配）。
		// 2026-11-01 移除 DP 拼合造词后，「部分消费单字不得占首选」只在有前缀
		// 词时成立——woaichangcheng 没有多音节前缀词，首选就是「我」。
		if len(all) == 0 {
			t.Errorf("%s: 应有候选", c.input)
			continue
		}
		if got := all[0].Text; got != c.wantFirst {
			t.Errorf("%s: 首选 = %q, 期望 %q（候选: %+v）", c.input, got, c.wantFirst, all[:min(6, len(all))])
		}
	}
}

// TestCommonDictShortPinyinSingleFallbackAlways：2~3 音节输入同样在列表末尾
// 追加首音节全量单字——任何时候都保留逐字挑选的兜底（用户要求），不再只给
// top10；整词/组合候选仍在前面，单字块收尾。
func TestCommonDictShortPinyinSingleFallbackAlways(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct {
		input    string
		firstSyl string
		word     string
	}{
		{"changjiang", "chang", "长"}, // 2 音节：「长」chang 本就在 top10 内可达
		{"jisuanji", "ji", "计"},      // 3 音节
	}
	for _, c := range cases {
		dictSingles := len(d.QueryTokensExact([]Token{{Text: c.firstSyl, IsExact: true}}, 500))
		if dictSingles <= 10 {
			t.Fatalf("词典 %s 同音字仅 %d 个，无法验证单字兜底", c.firstSyl, dictSingles)
		}
		s := NewSession(d, nil)
		all := allCandidates(s, mustComp(s, c.input))
		got := 0
		found := false
		for _, cand := range all {
			if cand.Syllables != c.firstSyl {
				continue
			}
			got++
			if cand.Text == c.word {
				found = true
			}
		}
		want := dictSingles
		if want > singleSyllableExactLimit {
			want = singleSyllableExactLimit
		}
		if got < want {
			t.Errorf("%s: 首音节单字召回 %d，应至少达到 %d（单字兜底始终可用，上限 %d）",
				c.input, got, want, singleSyllableExactLimit)
		}
		if !found {
			t.Errorf("%s: 候选里应有「%s」", c.input, c.word)
		}
	}
}

// TestCommonDictLongPinyinFirstSyllableReachable：>7 个 token 的长输入，
// 逐级回退的 drop 上限（6）让它永远回退不到首音节——修复前 8 音节输入
// 首音节单字数量为 0，逐字挑选完全不可用（2026-11-01 用户反馈）。
// compose 现在显式补一次首音节查询，要求同音字全量可达且能渐进上屏。
func TestCommonDictLongPinyinFirstSyllableReachable(t *testing.T) {
	d := mustCommonDict(t)
	cases := []struct {
		input    string
		firstSyl string
		word     string
		wantRest string
	}{
		{"changjiangdadaodexingqi", "chang", "长", "jiangdadaodexingqi"},     // 8 token
		{"zhonghuarenmingongheguode", "zhong", "中", "huarenmingongheguode"}, // 8 token
		{"womenyiqichanggemingba", "wo", "我", "menyiqichanggemingba"},       // 8 token
	}
	for _, c := range cases {
		wantSingles := len(d.QueryTokensExact([]Token{{Text: c.firstSyl, IsExact: true}}, 500))
		if wantSingles < 10 {
			t.Fatalf("词典 %s 同音字仅 %d 个，词库生成疑似异常", c.firstSyl, wantSingles)
		}
		s := NewSession(d, nil)
		all := allCandidates(s, mustComp(s, c.input))
		got, pos := 0, -1
		for i, cand := range all {
			if cand.Syllables != c.firstSyl {
				continue
			}
			got++
			if cand.Text == c.word {
				pos = i
			}
		}
		if got != wantSingles {
			t.Errorf("%s: 首音节 %s 单字召回 %d，期望全量 %d（长输入首音节兜底未生效）",
				c.input, c.firstSyl, got, wantSingles)
		}
		if pos < 0 {
			t.Errorf("%s: 候选里找不到「%s」", c.input, c.word)
			continue
		}
		if pos == 0 {
			t.Errorf("%s: 「%s」不应是首选（只有整句/整词候选都不存在时才轮到逐字挑选）", c.input, c.word)
		}
		// 逐字挑选路径必须可用：选首音节单字只消费首音节，剩余输入继续组词
		s2 := NewSession(d, nil)
		if _, err := s2.Input(c.input); err != nil {
			t.Fatalf("%s: Input 失败: %v", c.input, err)
		}
		idx := findCandidate(t, s2, c.word)
		rest, committed, err := s2.SelectPrefix(idx)
		if err != nil || committed != c.word {
			t.Fatalf("%s: 渐进选「%s」失败: committed=%q err=%v", c.input, c.word, committed, err)
		}
		if rest.Input != c.wantRest {
			t.Errorf("%s: 选「%s」后剩余输入 = %q, 期望 %q", c.input, c.word, rest.Input, c.wantRest)
		}
	}
}

// TestCommonDictSingleSyllableFullRecallBeyond120：旧上限 120 会把同音字超过
// 120 的音节（yi 141、ji 130、yu 124、qi 91）尾部裁掉；提高上限后必须全量
// 可达（用户反馈单字候选太少）。
func TestCommonDictSingleSyllableFullRecallBeyond120(t *testing.T) {
	d := mustCommonDict(t)
	entries := d.QueryTokensExact([]Token{{Text: "yi", IsExact: true}}, 500)
	if len(entries) <= 120 {
		t.Fatalf("词典 yi 同音字仅 %d 个，无法验证 >120 场景", len(entries))
	}
	s := NewSession(d, nil)
	all := allCandidates(s, mustComp(s, "yi"))
	got := 0
	for _, c := range all {
		if c.Syllables == "yi" {
			got++
		}
	}
	if got < len(entries) {
		t.Fatalf("yi 同音字 %d 个，候选只有 %d 个（仍被上限 %d 裁剪）",
			len(entries), got, singleSyllableExactLimit)
	}
	// 旧 120 上限之外的字（按词频排第 131 位）必须可达。
	target := entries[130].Word
	for _, c := range all {
		if c.Text == target {
			return
		}
	}
	t.Fatalf("yi 候选里找不到旧上限之外的字「%s」（%d 条候选）", target, len(all))
}

// TestSingleSegmentOnlySingleChars：单段输入（单音节全拼/单段声母）只出单字候选，
// 不再用「前缀扩展词」占位（diao 不应出现 调用/调查）。
func TestSingleSegmentOnlySingleChars(t *testing.T) {
	d := mustCommonDict(t)
	for _, input := range []string{"diao", "yi", "d"} {
		s := NewSession(d, nil)
		all := allCandidates(s, mustComp(s, input))
		if len(all) == 0 {
			t.Fatalf("%s 候选为空", input)
		}
		for _, c := range all {
			if len([]rune(c.Text)) != 1 {
				t.Fatalf("%s 出现多字候选「%s」（单段输入只应出单字）", input, c.Text)
			}
		}
		if input == "diao" {
			want := len(d.QueryTokensExact([]Token{{Text: "diao", IsExact: true}}, 500))
			if len(all) != want {
				t.Fatalf("diao 单字候选 %d，期望词典同音字全量 %d", len(all), want)
			}
		}
	}
}

// TestSingleSegmentExplicitSplitKeepsWord：单段 xian 只出单字；显式写成
// xi'an 才算多音节词输入，候选里出现「西安」。
func TestSingleSegmentExplicitSplitKeepsWord(t *testing.T) {
	d := mustDict(t)
	s := NewSession(d, nil)
	all := allCandidates(s, mustComp(s, "xian"))
	if len(all) == 0 {
		t.Fatal("xian 候选为空")
	}
	for _, c := range all {
		if len([]rune(c.Text)) > 1 {
			t.Fatalf("xian 是单段输入，不应出现词语「%s」", c.Text)
		}
	}
	s2 := NewSession(d, nil)
	found := false
	for _, c := range allCandidates(s2, mustComp(s2, "xi'an")) {
		if c.Text == "西安" {
			found = true
		}
	}
	if !found {
		t.Fatal("xi'an 应出现「西安」")
	}
}

// TestSingleSegmentNoWords：单段输入（jie/xian/tuan）拼音匹配候选里
// 只允许单字，连写全匹配词（饥饿/西安/图案）也不能出现；显式分段后
// （ji'e/xi'an/tu'an）词语才出现。
func TestSingleSegmentNoWords(t *testing.T) {
	d := mustCommonDict(t)
	for _, tc := range []struct{ input, split, word string }{
		{"jie", "ji'e", "饥饿"},
		{"xian", "xi'an", "西安"},
		{"tuan", "tu'an", "图案"},
	} {
		s := NewSession(d, nil)
		for _, c := range allCandidates(s, mustComp(s, tc.input)) {
			if len([]rune(c.Text)) > 1 {
				t.Fatalf("%s 是单段输入，不应出现词语「%s」[%s]", tc.input, c.Text, c.Syllables)
			}
		}
		s2 := NewSession(d, nil)
		found := false
		for _, c := range allCandidates(s2, mustComp(s2, tc.split)) {
			if c.Text == tc.word {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s 应出现「%s」", tc.split, tc.word)
		}
	}
}

// TestAllSingleSegmentInputsOnlySingleChars：遍历内置词库的全部单音节与
// 26 个单字母声母，确保任何单段输入的拼音匹配候选里只有单字——词语
// （前缀延伸词和连写全匹配词）一律不允许。
func TestAllSingleSegmentInputsOnlySingleChars(t *testing.T) {
	d := mustCommonDict(t)
	syls := map[string]bool{}
	for _, e := range d.entries {
		if len([]rune(e.Word)) == 1 && len(e.Syllables) == 1 {
			syls[e.Syllables[0]] = true
		}
	}
	if len(syls) < 400 {
		t.Fatalf("内置词库单音节只有 %d 个，预期 ≥400", len(syls))
	}
	for syl := range syls {
		s := NewSession(d, nil)
		all := allCandidates(s, mustComp(s, syl))
		if len(all) == 0 {
			t.Fatalf("单音节 %s 没有任何候选", syl)
		}
		for _, c := range all {
			if len([]rune(c.Text)) != 1 {
				t.Fatalf("单音节 %s 出现词语「%s」[%s]", syl, c.Text, c.Syllables)
			}
		}
	}
	for r := 'a'; r <= 'z'; r++ {
		input := string(r)
		s := NewSession(d, nil)
		for _, c := range allCandidates(s, mustComp(s, input)) {
			if len([]rune(c.Text)) > 1 {
				t.Fatalf("单字母声母 %s 出现多字词「%s」[%s]", input, c.Text, c.Syllables)
			}
		}
	}
}

// TestCommonDictDiaoSingleQuality：用户反馈「diao 打不出屌、却混入刀/跳/鸟」。
// 单字读音刷新后：屌正常收录但不特殊置顶（掉/调等级更高频的字在前），
// 刀/跳/鸟 的误收 diāo 读音已删除。
func TestCommonDictDiaoSingleQuality(t *testing.T) {
	d := mustCommonDict(t)
	s := NewSession(d, nil)
	all := allCandidates(s, mustComp(s, "diao"))
	if len(all) == 0 || all[0].Text != "掉" {
		t.Fatalf("diao 首选应为高频的「掉」，实际: %+v", firstTexts(all, 6))
	}
	bogus := map[string]bool{"刀": true, "跳": true, "鸟": true}
	pos := map[string]int{}
	for i, c := range all {
		if bogus[c.Text] {
			t.Fatalf("diao 不应出现误收读音的字「%s」", c.Text)
		}
		pos[c.Text] = i + 1
	}
	if pos["屌"] == 0 {
		t.Fatalf("diao 候选应包含「屌」，实际: %+v", firstTexts(all, 6))
	}
	if pos["调"] == 0 {
		t.Fatalf("diao 候选应保留「调」，实际: %+v", firstTexts(all, 6))
	}
	// 屌按常用度排在 掉/调 之后，不特殊置顶。
	if pos["屌"] < pos["调"] || pos["屌"] < pos["掉"] {
		t.Fatalf("屌应按常用度排序（掉/调 在前），实际位次: %v", pos)
	}
}
