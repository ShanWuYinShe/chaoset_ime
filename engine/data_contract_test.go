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

	// 规模断言：防止生成工具改坏导致词库静默缩水
	const minEntries = 140000
	if d.Len() < minEntries {
		t.Fatalf("内置词库词条数异常偏少: %d（期望 ≥%d）", d.Len(), minEntries)
	}

	// 逐条断言生成器的不变量。这些约束由 cmd/chaoset-dictgen 保证，
	// 在这里兜底：任何一条被破坏都说明生成链路出了问题。
	var badSyll, badFreq, multi int
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
		}
	}
	if badSyll > 0 {
		t.Fatalf("%d 条词条音节数与字数不等（生成器不变量被破坏），例: %v", badSyll, syllExamples)
	}
	if badFreq > 0 {
		t.Fatalf("%d 条词条词频超出 1..99，例: %v", badFreq, freqExamples)
	}
	if multi*10 < d.Len()*9 {
		t.Fatalf("多字词比例异常: %d/%d（期望 ≥90%%）", multi, d.Len())
	}
}

// data/dict_extra.tsv 是整个词库链路里**唯一允许手工编辑**的词表。
// 手改最容易引入脏数据，且最容易忘记重新生成产物——两件事都在 CI 断言：
//  1. 每一行格式合法（3 列、词与拼音非空、音节数==字数、音节合法、词频正）；
//  2. 每个词条都已合并进内置词库（否则用户看到的词库不含新词）。
func TestDictExtraValidAndMerged(t *testing.T) {
	extraPath := filepath.Join("..", "data", "dict_extra.tsv")
	data, err := os.ReadFile(extraPath)
	if err != nil {
		t.Fatalf("读取补充词表失败: %v", err)
	}

	type extraRow struct {
		word  string
		sylls []string
		freq  int
	}
	var rows []extraRow
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lineNo := i + 1
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			t.Fatalf("%s 第 %d 行应为 3 列（词/拼音/词频），实际 %d 列: %q",
				extraPath, lineNo, len(parts), line)
		}
		word := strings.TrimSpace(parts[0])
		sylls := strings.Fields(parts[1])
		freq, cerr := strconv.Atoi(strings.TrimSpace(parts[2]))
		if word == "" || len(sylls) == 0 {
			t.Fatalf("%s 第 %d 行词或拼音为空", extraPath, lineNo)
		}
		if cerr != nil || freq <= 0 {
			t.Fatalf("%s 第 %d 行词频不是正整数: %q", extraPath, lineNo, parts[2])
		}
		if len(sylls) != len([]rune(word)) {
			t.Fatalf("%s 第 %d 行音节数与字数不等: %s / %v", extraPath, lineNo, word, sylls)
		}
		for _, s := range sylls {
			if !IsSyllable(s) {
				t.Fatalf("%s 第 %d 行存在非法音节 %q", extraPath, lineNo, s)
			}
		}
		rows = append(rows, extraRow{word: word, sylls: sylls, freq: freq})
	}
	if len(rows) == 0 {
		t.Fatal("补充词表为空（至少应保留历史维护的词条）")
	}

	// 一致性：每条都必须已在生成产物里——防止「改了 extra 忘记重新生成」。
	common, err := LoadDictFiles([]string{filepath.Join("..", "data", "dict_common.tsv")})
	if err != nil {
		t.Fatalf("加载内置词库失败: %v", err)
	}
	for _, r := range rows {
		if !common.Contains(r.word, r.sylls) {
			t.Errorf("补充词条 %q（%s）未合并进 dict_common.tsv —— 改完 extra 必须重新运行 cmd/chaoset-dictgen 并提交生成产物",
				r.word, strings.Join(r.sylls, " "))
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
