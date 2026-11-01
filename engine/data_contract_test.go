package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 内置词库是生成产物（cmd/chaoset-dictgen），**正确性必须由 CI 保证**：
// 这里用严格模式解析，任何一行脏数据都让 CI 失败，而不是留到用户机器上
// 被运行时宽松加载吞掉。
//
// 分工（2026-10-30 与用户讨论后明确）：
//   - 构建期：生成工具严格校验 + 本测试严格解析 → 脏数据在 CI 暴露；
//   - 运行期：LoadDictFilesLenient 跳过用户自备词库里的坏行 →
//     第三方词库不会让输入法起不来。
func TestBuiltinDictStrictlyValid(t *testing.T) {
	path := filepath.Join("..", "data", "dict_common.tsv")
	d, err := LoadDictFiles([]string{path})
	if err != nil {
		t.Fatalf("内置词库必须能被严格解析（脏数据应在生成阶段拦截）: %v", err)
	}

	// 规模断言：防止生成工具改坏导致词库静默缩水。
	// 2026-11-01 词库改用「常用性门槛」流水线后，规模从旧 jieba 库的 14.5 万条
	// 降到约 6.8 万条（词表只保留规范常用词 + 高频用法 + 热词，单字改收
	// 通用规范汉字表 8105），因此下限同步调整；上限不卡死，允许后续扩充。
	const minEntries = 60000
	if d.Len() < minEntries {
		t.Fatalf("内置词库词条数异常偏少: %d（期望 ≥%d）", d.Len(), minEntries)
	}

	// 逐条断言生成器的不变量。这些约束由 cmd/chaoset-dictgen 保证，
	// 在这里兜底：任何一条被破坏都说明生成链路出了问题。
	var badSyll, badFreq, multi, single int
	var syllExamples, freqExamples []string
	for _, e := range d.Entries() {
		if len(e.Syllables) != len([]rune(e.Word)) {
			badSyll++
			if len(syllExamples) < 3 {
				syllExamples = append(syllExamples, fmt.Sprintf("%s/%s", e.Word, strings.Join(e.Syllables, " ")))
			}
		}
		if e.Freq < 1 || e.Freq > 99 {
			badFreq++
			if len(freqExamples) < 3 {
				freqExamples = append(freqExamples, fmt.Sprintf("%s=%d", e.Word, e.Freq))
			}
		}
		if len(e.Syllables) > 1 {
			multi++
		} else {
			single++
		}
	}
	if badSyll > 0 {
		t.Fatalf("%d 条词条音节数与字数不等（生成器不变量被破坏），例: %v", badSyll, syllExamples)
	}
	if badFreq > 0 {
		t.Fatalf("%d 条词条词频超出 1..99，例: %v", badFreq, freqExamples)
	}
	if multi*100 < d.Len()*80 {
		t.Fatalf("多字词比例异常: %d/%d（期望 ≥80%%：单字表来自通用规范汉字表 8105，约 0.9 万条）", multi, d.Len())
	}
	if single < 8000 {
		t.Fatalf("单字条目异常偏少: %d（期望 ≥8000，通用规范汉字表 8105 应基本全覆盖）", single)
	}

	// 常用词守门：防止词表门槛/词频源被改坏导致高频常用词静默丢失。
	wordSet := make(map[string]bool, d.Len())
	for _, e := range d.Entries() {
		wordSet[e.Word] = true
	}
	for _, w := range []string{"的", "我们", "你好", "数据", "微信"} {
		if !wordSet[w] {
			t.Errorf("内置词库缺少常用词 %q（词表门槛或拼音链路被改坏）", w)
		}
	}
}

// data/dict_extra.tsv / data/dict_hot.tsv 是词库链路里**允许手工编辑**的词表。
// 手改最容易引入脏数据，且最容易忘记重新生成产物——两件事都在 CI 断言：
//  1. 每一行格式合法（3 列、词与拼音非空、音节数==字数、音节合法、权重正）；
//  2. 每个词条都已合并进内置词库（否则用户看到的词库不含新词）。
func TestDictExtraValidAndMerged(t *testing.T) {
	checkSupplementTable(t, filepath.Join("..", "data", "dict_extra.tsv"), "dict_extra")
}

func TestDictHotValidAndMerged(t *testing.T) {
	checkSupplementTable(t, filepath.Join("..", "data", "dict_hot.tsv"), "dict_hot")
}

func checkSupplementTable(t *testing.T, path, name string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 词表失败: %v", name, err)
	}

	type row struct {
		word   string
		sylls  []string
		weight int
	}
	var rows []row
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lineNo := i + 1
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			t.Fatalf("%s 第 %d 行应为 3 列（词/拼音/权重），实际 %d 列: %q",
				name, lineNo, len(parts), line)
		}
		word := strings.TrimSpace(parts[0])
		sylls := strings.Fields(parts[1])
		weight, cerr := strconv.Atoi(strings.TrimSpace(parts[2]))
		if word == "" || len(sylls) == 0 {
			t.Fatalf("%s 第 %d 行词或拼音为空", name, lineNo)
		}
		if cerr != nil || weight <= 0 {
			t.Fatalf("%s 第 %d 行权重不是正整数: %q", name, lineNo, parts[2])
		}
		if len(sylls) != len([]rune(word)) {
			t.Fatalf("%s 第 %d 行音节数与字数不等: %s / %v", name, lineNo, word, sylls)
		}
		for _, s := range sylls {
			if !IsSyllable(s) {
				t.Fatalf("%s 第 %d 行存在非法音节 %q", name, lineNo, s)
			}
		}
		rows = append(rows, row{word: word, sylls: sylls, weight: weight})
	}
	if len(rows) == 0 {
		t.Fatalf("%s 词表为空（至少应保留历史维护的词条）", name)
	}

	// 一致性：每条都必须已在生成产物里——防止「改了词表忘记重新生成」。
	common, err := LoadDictFiles([]string{filepath.Join("..", "data", "dict_common.tsv")})
	if err != nil {
		t.Fatalf("加载内置词库失败: %v", err)
	}
	for _, r := range rows {
		if !common.Contains(r.word, r.sylls) {
			t.Errorf("%s 词条 %q（%s）未合并进 dict_common.tsv —— 改完 %s 必须重新运行 cmd/chaoset-dictgen 并提交生成产物",
				name, r.word, strings.Join(r.sylls, " "), name)
		}
	}
}

// 一致性守卫：内置词库经**宽松**加载与经**严格**加载必须得到完全相同的词条集合。
//
// 这条约束保护一个真实风险：daemon 在运行时用宽松加载，而 CI 用严格加载做
// 断言；如果宽松路径对已规范化的拼音做了任何额外变换（例如无条件归一化），
// 用户机器上的候选行为就会与 CI 验证过的行为不一致。实现上用「先严格校验、
// 通过则原样保留」保证两者等价（见 parseInto 的宽松分支注释）。
//
// 说明（2026-10-30 实测）：今天 normalizePinyin 对**本质合法**的音节是恒等
// 映射（specialSylls 只处理 lue/nue 这类非法写法 → lve/nve），所以即使把实现
// 改成「无条件归一化」本测试也不会失败——它目前是预防性的，约束未来给
// normalizePinyin 增加别名映射时不得改变内置词库的既有结果。
func TestLenientLoadMatchesStrictForBuiltinDict(t *testing.T) {
	path := filepath.Join("..", "data", "dict_common.tsv")
	strictDict, err := LoadDictFiles([]string{path})
	if err != nil {
		t.Fatalf("严格加载失败: %v", err)
	}
	lenientDict, warnings, err := LoadDictFilesLenient([]string{path})
	if err != nil {
		t.Fatalf("宽松加载失败: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("内置词库在宽松加载下不应产生任何警告，实际: %v", warnings)
	}
	if strictDict.Len() != lenientDict.Len() {
		t.Fatalf("词条数不一致：严格 %d vs 宽松 %d", strictDict.Len(), lenientDict.Len())
	}
	for _, e := range strictDict.Entries() {
		if !lenientDict.Contains(e.Word, e.Syllables) {
			t.Fatalf("严格加载有、宽松加载没有的词条: %s/%s", e.Word, strings.Join(e.Syllables, " "))
		}
	}
}
