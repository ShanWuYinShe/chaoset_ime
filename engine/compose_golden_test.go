package engine

// compose_golden_test.go —— compose() 拆分/调优的「完整候选序列」金标准回归。
//
// 背景：engine 现有测试多数只断言首选/次选/包含关系，不锁定完整候选序列。
// 拆分 compose（纯重构）时若某条候选的 rank/score/顺序被静默改动，现有测试
// 仍可能全绿。本文件对语料逐条锁定 Composition 的全部可观测输出：
//   - 跨所有页拍平后的完整候选：Text / Syllables / Score / FromUser / Segments；
//   - Preedit / PageCount，并校验分页不变量（Page 逐页递增、非末页恰为
//     pageSize 条、末页 1..pageSize 条、len(candidates) 与 PageCount 自洽）；
//   - 空输入（无组词）不变量。
//
// 语料（testdata/compose_golden_corpus.txt）生成规则：
//   a) engine/ipc 测试代码里出现过的输入串；
//   b) 内置词库 data/dict_common.tsv 按词频分带等距抽样派生的输入：整词连写、
//      简拼、混拼（首音节全拼 + 其余声母）、末音节残缺（前面完整 + 末音节打一半）；
//   c) 固定边界样例：单音节/单声母、显式分隔符（xi'an / tu'an / b'a）、续接
//      （zhonggu / beijin / quanmia）、错拼（zhogn / niihao / chagnshuru / hzong）、
//      双拼键串、64 rune 上限、超段长词等。
//
// 模式：plain / fuzzy（模糊音）/ trad（繁体输出）/ zrm（双拼）/ user（内存用户
// 词库预热，覆盖 FromUser 与 Segments 两条输出路径）。
//
// 刷新：仅在确认输出变化是有意的之后执行
//
//	go test ./engine -run ComposeGolden -update
//
// 并重新生成 testdata/compose_golden.jsonl.gz（词库 data/dict_common.tsv 重新生成
// 后也必须刷新）。刷新必须在提交说明里写清输出变化原因，禁止「跑红就 -update」。
// 文件是 gzip 压缩的 JSONL（纯文本约 3.9MB，压缩后约 320KB，避免仓库膨胀）；
// 查看：gunzip -c engine/testdata/compose_golden.jsonl.gz | head。gzip 头不含
// 时间戳/文件名，同样输入压缩结果逐字节稳定，可放心按内容做 golden 比较。
//
// 确定性（2026-11-08 已修复）：dict.go 的前缀 BFS 此前在命中 maxResults /
// maxPrefixScan 上限时依赖 map 遍历序（Go 每次 range 随机起点），部分短简拼/
// 混拼输入的候选总数随机波动。修复方式：Trie 节点记录子树最小词条下标
// （dictNode.subtreeMin），BFS 起点与每层排名都用它做全序决胜键；各查询的
// sort 比较器补齐 Word/音节决胜键（同词多音、同频同长不再依赖输入顺序）。
// 原「已知非确定性」输入已稳定并纳入语料；composeGoldenKnownUnstable 保留为
// 空清单 + 守卫测试：将来若发现新的非确定性输入，先补根因再纳入，不要直接
// 塞进 golden（会随机翻红）。
//
// 维护工具（默认跳过，手动执行）：
//
//	go test ./engine -run ComposeGoldenDetectUnstable -detect-unstable -v
//
// 对当前语料 × 各模式各跑 20 次，若发现非确定性 (mode,input) 会列出；用于
// 新增语料后的排除清单维护与「根因修复后复核」。

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
)

var (
	updateComposeGolden = flag.Bool("update", false,
		"重新生成 compose 全量候选 golden 文件（仅在确认输出变化有意时使用）")
	detectComposeGoldenUnstable = flag.Bool("detect-unstable", false,
		"检测 golden 语料中的非确定性 (mode,input)（每对跑 20 次），维护排除清单用")
)

const (
	composeGoldenCorpusPath = "testdata/compose_golden_corpus.txt"
	composeGoldenPath       = "testdata/compose_golden.jsonl.gz"
	// detectComposeGoldenRounds 是 -detect-unstable 对每个 (mode,input) 的重复次数：
	// map 遍历序每次 range 都随机，命中上限的截断结果很容易在 20 次内翻出差异。
	detectComposeGoldenRounds = 20
)

// composeGoldenKnownUnstable 是「已知非确定性、不得进入 golden 语料」的输入清单。
//
// 2026-11-08 前缀 BFS 确定性修复后清空；保留空清单是为了让守卫测试与
// -detect-unstable 工具继续可用：将来新增语料若实测不稳定，先把根因修掉，
// 不要直接塞进 golden（会随机翻红、掩盖真正的排序漂移）。
var composeGoldenKnownUnstable = []string{}

// composeGoldenModes 是 golden 覆盖的子串配置；顺序即 golden 文件记录顺序。
var composeGoldenModes = []string{"plain", "fuzzy", "trad", "zrm", "user"}

type goldenCandidate struct {
	Text      string   `json:"text"`
	Syllables string   `json:"syllables"`
	Score     int      `json:"score"`
	FromUser  bool     `json:"from_user,omitempty"`
	Segments  []string `json:"segments,omitempty"`
	// TypoFix 是未导出字段 Candidate.typoFix 的快照（同包测试可直接读）：
	// 它决定 SelectPrefix 的「整串消费」语义与纠错候选固定第 3 位的重排，
	// 拆分/调优时必须一并锁定。JSON 字段用下划线，仅 golden 文件内部使用。
	TypoFix bool `json:"typo_fix,omitempty"`
}

// goldenRecord 是一条 (mode,input) 的完整输出。Candidates 是跨页拍平的全量
// 候选序列（Composition 本身只带当前页）。
type goldenRecord struct {
	Mode       string            `json:"mode"`
	Input      string            `json:"input"`
	Preedit    string            `json:"preedit"`
	PageCount  int               `json:"page_count"`
	Candidates []goldenCandidate `json:"candidates"`
}

// TestComposeGolden 锁定 engine/testdata/compose_golden.jsonl.gz 里的完整候选序列。
func TestComposeGolden(t *testing.T) {
	got := buildComposeGoldenRecords(t)
	if *updateComposeGolden {
		writeComposeGolden(t, got)
		t.Logf("已重新生成 %s（%d 条记录）", composeGoldenPath, len(got))
		return
	}
	want := readComposeGolden(t)
	if len(got) != len(want) {
		t.Fatalf("golden 记录数不一致: 实际生成 %d 条，文件 %d 条；"+
			"若语料/模式有意变化请用 -update 刷新", len(got), len(want))
	}
	for i := range want {
		if goldenRecordEqual(got[i], want[i]) {
			continue
		}
		t.Fatalf("compose 输出漂移（第 %d 条）:\n%s", i, describeComposeGoldenDiff(got[i], want[i]))
	}
}

// TestComposeGoldenCorpusExcludesKnownUnstable 守卫「已知非确定性输入不得混入
// golden 语料」：否则测试会随机翻红，掩盖真正的排序漂移。当前清单为空
// （2026-11-08 修复后）；将来新增不稳定输入时由 -detect-unstable 发现。
func TestComposeGoldenCorpusExcludesKnownUnstable(t *testing.T) {
	corpus := readComposeGoldenCorpus(t)
	inCorpus := make(map[string]bool, len(corpus))
	for _, input := range corpus {
		inCorpus[input] = true
	}
	for _, input := range composeGoldenKnownUnstable {
		if inCorpus[input] {
			t.Errorf("输入 %q 属已知非确定性，不得进入 golden 语料；"+
				"见 composeGoldenKnownUnstable 注释（先修根因或排除，再纳入）", input)
		}
	}
}

// TestComposeGoldenDetectUnstable 是 -detect-unstable 的维护工具：对当前语料
// × 全部模式各跑 detectComposeGoldenRounds 次，列出输出不稳定的 (mode,input)。
// 正常情况下应为 0 条（已知非确定性输入已在语料外）；新增语料后跑一次确认。
func TestComposeGoldenDetectUnstable(t *testing.T) {
	if !*detectComposeGoldenUnstable {
		t.Skip("默认跳过；用 go test ./engine -run ComposeGoldenDetectUnstable -detect-unstable -v 手动执行")
	}
	dict := mustCommonDict(t)
	corpus := readComposeGoldenCorpus(t)
	inputs := append([]string{""}, corpus...)
	var unstable []string
	for _, mode := range composeGoldenModes {
		for _, input := range inputs {
			variants := make(map[string]bool, 1)
			for round := 0; round < detectComposeGoldenRounds; round++ {
				variants[encodeGoldenRecord(composeGoldenRecord(t, dict, mode, input))] = true
				if len(variants) > 1 {
					break
				}
			}
			if len(variants) > 1 {
				unstable = append(unstable, fmt.Sprintf("%s\t%s", mode, input))
			}
		}
	}
	if len(unstable) > 0 {
		t.Fatalf("检测到 %d 个非确定性的 (mode,input)：\n%s\n"+
			"请从语料中排除（并记录到 composeGoldenKnownUnstable），或修复 dict.go 前缀 BFS 后再纳入",
			len(unstable), strings.Join(unstable, "\n"))
	}
}

// TestComposeMemoInvalidatedBySetters 锁定 compose memo（Compose 记忆化）与
// 配置 setter 的一致性：同一 input/page 下切换 fuzzy/traditional/double-pinyin
// 后，Compose 必须重新计算，不能返回旧配置的缓存结果（GO-02 回归）。
func TestComposeMemoInvalidatedBySetters(t *testing.T) {
	dict := mustCommonDict(t)

	t.Run("traditional", func(t *testing.T) {
		s := NewSession(dict, nil)
		if _, err := s.Input("xuesheng", false); err != nil {
			t.Fatalf("Input 失败: %v", err)
		}
		plain := s.Compose()
		if len(plain.Candidates) == 0 {
			t.Fatal("xuesheng 应有候选")
		}
		s.SetTraditional(true)
		trad := s.Compose() // 同 input、同 page：必须重新计算
		if sameCandidateTexts(plain.Candidates, trad.Candidates) {
			t.Fatalf("SetTraditional(true) 后同一输入的候选未变化（memo 未失效）: %v", candidateTexts(trad.Candidates))
		}
		found := false
		for _, c := range trad.Candidates {
			if strings.Contains(c.Text, "學") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("繁体模式下候选应含繁体字（學），实际: %v", candidateTexts(trad.Candidates))
		}
	})

	t.Run("fuzzy", func(t *testing.T) {
		s := NewSession(dict, nil)
		if _, err := s.Input("zheng", false); err != nil {
			t.Fatalf("Input 失败: %v", err)
		}
		plain := s.Compose()
		s.SetFuzzy(true)
		fuzzy := s.Compose() // 同 input、同 page：必须重新计算
		if sameCandidateTexts(plain.Candidates, fuzzy.Candidates) {
			t.Fatalf("SetFuzzy(true) 后同一输入的候选未变化（memo 未失效）: %v", candidateTexts(fuzzy.Candidates))
		}
	})

	t.Run("double-pinyin", func(t *testing.T) {
		s := NewSession(dict, nil)
		if _, err := s.Input("nihao", false); err != nil {
			t.Fatalf("Input 失败: %v", err)
		}
		if comp := s.Compose(); comp.Shuangpin != "" {
			t.Fatalf("全拼模式下 Shuangpin = %q, 期望空", comp.Shuangpin)
		}
		s.SetDoublePinyin("zrm")
		comp := s.Compose() // 同 input、同 page：必须重新计算（Shuangpin 字段随之下发）
		if comp.Shuangpin != "zrm" {
			t.Fatalf("SetDoublePinyin(zrm) 后 Shuangpin = %q, 期望 zrm（memo 未失效）", comp.Shuangpin)
		}
	})
}

// --- golden 记录生成 ---

func buildComposeGoldenRecords(t *testing.T) []goldenRecord {
	t.Helper()
	dict := mustCommonDict(t)
	corpus := readComposeGoldenCorpus(t)
	// 空串由测试固定注入：语料文件用空行表示会被跳过，而空输入是必须锁定的边界。
	inputs := append([]string{""}, corpus...)
	records := make([]goldenRecord, 0, len(inputs)*len(composeGoldenModes))
	for _, mode := range composeGoldenModes {
		for _, input := range inputs {
			records = append(records, composeGoldenRecord(t, dict, mode, input))
		}
	}
	return records
}

// newComposeGoldenSession 按模式构造会话。user 模式注入内存用户词库预热数据：
// 覆盖普通调频词（FromUser 输出）与带分段的整句记录（Segments 输出）。
func newComposeGoldenSession(t *testing.T, dict *Dictionary, mode string) *Session {
	t.Helper()
	switch mode {
	case "plain":
		return NewSession(dict, nil)
	case "fuzzy":
		s := NewSession(dict, nil)
		s.SetFuzzy(true)
		return s
	case "trad":
		s := NewSession(dict, nil)
		s.SetTraditional(true)
		return s
	case "zrm":
		s := NewSession(dict, nil)
		s.SetDoublePinyin("zrm")
		return s
	case "user":
		u, err := NewUserDict("") // 空路径 = 纯内存
		if err != nil {
			t.Fatalf("创建内存用户词库失败: %v", err)
		}
		for _, seed := range composeGoldenUserSeed {
			var err error
			if len(seed.segs) > 0 {
				err = u.BumpWordWithSegs(seed.word, seed.sylls, seed.segs)
			} else {
				err = u.BumpWord(seed.word, seed.sylls)
			}
			if err != nil {
				t.Fatalf("用户词库预热失败（%s）: %v", seed.word, err)
			}
		}
		return NewSession(dict, u)
	default:
		t.Fatalf("未知 golden 模式 %q", mode)
		return nil
	}
}

// composeGoldenUserSeed 是 user 模式的用户词库预热：普通调频词 + 带分段的整句
// 记录（今天星期一 → 分段 今天/星期一）。词频加成固定，输出可重复。
var composeGoldenUserSeed = []struct {
	word  string
	sylls []string
	segs  []string
}{
	{word: "你好", sylls: []string{"ni", "hao"}},
	{word: "我们", sylls: []string{"wo", "men"}},
	{word: "今天", sylls: []string{"jin", "tian"}},
	{word: "时间", sylls: []string{"shi", "jian"}},
	{word: "测试", sylls: []string{"ce", "shi"}},
	{word: "输入法", sylls: []string{"shu", "ru", "fa"}},
	{word: "今天星期一", sylls: []string{"jin", "tian", "xing", "qi", "yi"}, segs: []string{"今天", "星期一"}},
}

// composeGoldenRecord 生成一条记录，同时校验分页与全量候选口径自洽：
// NextPage 逐页翻到末页，每页的 Page/PageCount/Preedit 必须与首页一致。
func composeGoldenRecord(t *testing.T, dict *Dictionary, mode, input string) goldenRecord {
	t.Helper()
	s := newComposeGoldenSession(t, dict, mode)
	comp, err := s.Input(input, false)
	if err != nil {
		t.Fatalf("[%s] Input(%q) 失败: %v", mode, input, err)
	}
	rec := goldenRecord{
		Mode:       mode,
		Input:      input,
		Preedit:    comp.Preedit,
		PageCount:  comp.PageCount,
		Candidates: make([]goldenCandidate, 0, comp.PageCount*pageSize),
	}
	appendPage := func(c Composition, page int) {
		if c.Page != page {
			t.Fatalf("[%s] Input(%q) 第 %d 页返回 Page=%d", mode, input, page, c.Page)
		}
		if c.PageCount != rec.PageCount {
			t.Fatalf("[%s] Input(%q) 翻页后 PageCount 变化: %d -> %d", mode, input, rec.PageCount, c.PageCount)
		}
		if c.Preedit != rec.Preedit {
			t.Fatalf("[%s] Input(%q) 翻页后 Preedit 变化: %q -> %q", mode, input, rec.Preedit, c.Preedit)
		}
		if page < rec.PageCount-1 && len(c.Candidates) != pageSize {
			t.Fatalf("[%s] Input(%q) 第 %d 页候选数 = %d, 期望 %d", mode, input, page, len(c.Candidates), pageSize)
		}
		if page == rec.PageCount-1 && (len(c.Candidates) < 1 || len(c.Candidates) > pageSize) {
			t.Fatalf("[%s] Input(%q) 末页候选数 = %d, 期望 1..%d", mode, input, len(c.Candidates), pageSize)
		}
		for _, cand := range c.Candidates {
			rec.Candidates = append(rec.Candidates, goldenCandidate{
				Text:      cand.Text,
				Syllables: cand.Syllables,
				Score:     cand.Score,
				FromUser:  cand.FromUser,
				Segments:  cand.Segments,
				TypoFix:   cand.typoFix,
			})
		}
	}
	appendPage(comp, 0)
	for page := 1; page < rec.PageCount; page++ {
		next, err := s.NextPage()
		if err != nil {
			t.Fatalf("[%s] Input(%q) NextPage 失败: %v", mode, input, err)
		}
		appendPage(next, page)
	}
	// 全量候选与 PageCount 自洽（空输入为 0 条候选）。
	wantPages := (len(rec.Candidates) + pageSize - 1) / pageSize
	if rec.PageCount != wantPages {
		t.Fatalf("[%s] Input(%q) PageCount=%d, 但全量候选 %d 条对应 %d 页",
			mode, input, rec.PageCount, len(rec.Candidates), wantPages)
	}
	if input == "" && (rec.Preedit != "" || rec.PageCount != 0 || len(rec.Candidates) != 0) {
		t.Fatalf("[%s] 空输入不变量被破坏: preedit=%q pages=%d candidates=%d",
			mode, rec.Preedit, rec.PageCount, len(rec.Candidates))
	}
	return rec
}

// --- 读写与比较 ---

func readComposeGoldenCorpus(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(composeGoldenCorpusPath)
	if err != nil {
		t.Fatalf("打开 golden 语料 %s 失败: %v", composeGoldenCorpusPath, err)
	}
	defer f.Close()
	seen := make(map[string]bool)
	var inputs []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if seen[line] {
			t.Fatalf("golden 语料含重复输入 %q（去重后重新生成语料）", line)
		}
		seen[line] = true
		inputs = append(inputs, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读取 golden 语料失败: %v", err)
	}
	if len(inputs) < 300 {
		t.Fatalf("golden 语料只有 %d 条输入，低于 300 条下限（语料被误删/截断？）", len(inputs))
	}
	return inputs
}

func encodeGoldenRecord(rec goldenRecord) string {
	b, err := json.Marshal(rec)
	if err != nil {
		panic("compose golden 记录序列化失败: " + err.Error())
	}
	return string(b)
}

func writeComposeGolden(t *testing.T, records []goldenRecord) {
	t.Helper()
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		t.Fatalf("创建 gzip 写入器失败: %v", err)
	}
	zw.Write([]byte("# compose golden：由 go test ./engine -run ComposeGolden -update 生成，禁止手工编辑。\n"))
	zw.Write([]byte("# 每条记录 = 一个 (mode,input) 的全量候选（跨页拍平）+ Preedit/PageCount；说明见 engine/compose_golden_test.go。\n"))
	for _, rec := range records {
		zw.Write([]byte(encodeGoldenRecord(rec)))
		zw.Write([]byte{'\n'})
	}
	if err := zw.Close(); err != nil { // Close 会写出 gzip 尾部校验和
		t.Fatalf("结束 gzip 写入失败: %v", err)
	}
	if err := os.WriteFile(composeGoldenPath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", composeGoldenPath, err)
	}
}

func readComposeGolden(t *testing.T) []goldenRecord {
	t.Helper()
	f, err := os.Open(composeGoldenPath)
	if err != nil {
		t.Fatalf("打开 golden 文件 %s 失败（首次使用需 -update 生成）: %v", composeGoldenPath, err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("解压 golden 文件 %s 失败: %v", composeGoldenPath, err)
	}
	defer zr.Close()
	var records []goldenRecord
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rec goldenRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("解析 golden 记录失败: %v\n行: %s", err, line)
		}
		records = append(records, rec)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读取 golden 文件失败: %v", err)
	}
	return records
}

func goldenRecordEqual(a, b goldenRecord) bool {
	return encodeGoldenRecord(a) == encodeGoldenRecord(b)
}

// describeComposeGoldenDiff 给出可读的漂移报告：定位第一条不同的候选。
func describeComposeGoldenDiff(got, want goldenRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  mode=%s input=%q\n", got.Mode, got.Input)
	fmt.Fprintf(&b, "  preedit: got=%q want=%q\n", got.Preedit, want.Preedit)
	fmt.Fprintf(&b, "  page_count: got=%d want=%d\n", got.PageCount, want.PageCount)
	fmt.Fprintf(&b, "  candidates: got=%d want=%d\n", len(got.Candidates), len(want.Candidates))
	for i := 0; i < len(got.Candidates) && i < len(want.Candidates); i++ {
		if encodeGoldenCandidate(got.Candidates[i]) == encodeGoldenCandidate(want.Candidates[i]) {
			continue
		}
		fmt.Fprintf(&b, "  第一条不同候选 #%d:\n    got  = %s\n    want = %s\n", i,
			encodeGoldenCandidate(got.Candidates[i]), encodeGoldenCandidate(want.Candidates[i]))
		return b.String()
	}
	if len(got.Candidates) != len(want.Candidates) {
		fmt.Fprintf(&b, "  候选数量不同（前 %d 条相同）\n", minInt(len(got.Candidates), len(want.Candidates)))
	}
	return b.String()
}

func encodeGoldenCandidate(c goldenCandidate) string {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Sprintf("%+v", c)
	}
	return string(b)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// sameCandidateTexts 判断两组候选的文本序列是否完全相同（用于 memo 失效断言）。
func sameCandidateTexts(a, b []Candidate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Text != b[i].Text || a[i].Syllables != b[i].Syllables {
			return false
		}
	}
	return true
}

func candidateTexts(cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Text
	}
	return out
}
