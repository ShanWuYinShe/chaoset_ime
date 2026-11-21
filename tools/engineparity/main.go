// engineparity 是 Go ↔ Rust 引擎差分对比工具（rust 重构的量化基线）。
//
// 同一份场景 JSONL（-scenarios）分别驱动：
//   - Go 侧：进程内直接调用 engine.Session（与 daemon 同一引擎库）；
//   - Rust 侧：crates/chaoset-engine 的 replay bin（cargo run 或 -replay 指定的
//     预编译二进制）。
//
// 两侧输出同构的规范化响应 JSONL，compare 模式逐场景逐步骤做语义对比：
//   - 判定字段：ok、committed、comp（input/preedit/page/page_count/committed/
//     shuangpin）与候选序列（text/syllables/from_user，顺序敏感——这是用户可见行为）；
//   - score 只做信息性统计（内部打分差异不直接可见，但提示迁移完成度）。
//
// 用法：
//
//	go run ./tools/engineparity -mode compare -dict data/dict_common.tsv \
//	  -scenarios tools/engineparity/scenarios.jsonl
//	-mode go     只回放 Go 侧，结果写 -out
//	-mode rust   只回放 Rust 侧（经 cargo run），结果写 -out
//	-mode compare 两侧回放并对比
//
// 退出码（compare 模式）：0=用户可见行为完全一致；1=存在可见差异；2=工具错误。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"chaoset.ime/engine"
)

// scenario / step 的 JSON 结构（与 crates/chaoset-engine/src/bin/replay.rs 对齐）。
type scenario struct {
	Name   string    `json:"name"`
	Config cfg       `json:"config"`
	User   *userSeed `json:"user,omitempty"`
	Steps  []step    `json:"steps"`
}

// userSeed 描述场景预置的用户学习数据（内存用户词典，不落盘）：用于验证
// 「用户词库参与组词召回 / 整句安全约束 / 上下文共现排序」等路径。
type userSeed struct {
	Words   []userSeedWord `json:"words,omitempty"`
	Bigrams [][2]string    `json:"bigrams,omitempty"`
}

type userSeedWord struct {
	Word   string   `json:"word"`
	Sylls  []string `json:"sylls"`
	Segs   []string `json:"segs,omitempty"`
	Count  int      `json:"count,omitempty"`  // 重复上屏次数（模拟调频）
	Phrase bool     `json:"phrase,omitempty"` // 写入 Phrases 表（词组进化）
}

type cfg struct {
	Fuzzy       bool   `json:"fuzzy"`
	Traditional bool   `json:"traditional"`
	Shuangpin   string `json:"shuangpin"`
}

type step struct {
	Op    string   `json:"op"`
	Text  string   `json:"text,omitempty"`
	Half  bool     `json:"half,omitempty"`
	Index int      `json:"index,omitempty"`
	Key   string   `json:"key,omitempty"`
	Word  string   `json:"word,omitempty"`
	Sylls []string `json:"sylls,omitempty"`
}

// normCandidate / normComp 输出与 Rust 侧 norm_comp / norm_candidate 同构的
// 规范化 JSON（字段恒在，空值显式输出），保证对比与运行时表示无关。
func normCandidate(c engine.Candidate) map[string]any {
	return map[string]any{
		"text":      c.Text,
		"syllables": c.Syllables,
		"score":     c.Score,
		"from_user": c.FromUser,
	}
}

func normComp(c engine.Composition) map[string]any {
	cands := make([]map[string]any, 0, len(c.Candidates))
	for _, cd := range c.Candidates {
		cands = append(cands, normCandidate(cd))
	}
	return map[string]any{
		"input":      c.Input,
		"preedit":    c.Preedit,
		"page":       c.Page,
		"page_count": c.PageCount,
		"candidates": cands,
		"committed":  c.Committed,
		"shuangpin":  c.Shuangpin,
	}
}

func compOK(c engine.Composition) map[string]any {
	return map[string]any{"ok": true, "comp": normComp(c)}
}

func committedOK(text string) map[string]any {
	return map[string]any{"ok": true, "committed": text}
}

func errVal(err error) map[string]any {
	return map[string]any{"ok": false, "error": err.Error()}
}

func candidatesOK(cands []engine.Candidate) map[string]any {
	list := make([]map[string]any, 0, len(cands))
	for _, cd := range cands {
		list = append(list, normCandidate(cd))
	}
	return map[string]any{"ok": true, "candidates": list}
}

// runGoScenarios 在进程内驱动 Go engine.Session 回放全部场景。
func runGoScenarios(dict *engine.Dictionary, scens []scenario) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, sc := range scens {
		var user *engine.UserDict
		if sc.User != nil {
			ud, err := engine.NewUserDict("")
			if err != nil {
				return nil, fmt.Errorf("构造内存用户词典失败: %w", err)
			}
			for _, w := range sc.User.Words {
				n := w.Count
				if n <= 0 {
					n = 1
				}
				for i := 0; i < n; i++ {
					if w.Phrase {
						if err := ud.BumpPhraseWithSegs(w.Word, w.Sylls, w.Segs); err != nil {
							return nil, err
						}
					} else if err := ud.BumpWordWithSegs(w.Word, w.Sylls, w.Segs); err != nil {
						return nil, err
					}
				}
			}
			for _, bg := range sc.User.Bigrams {
				if err := ud.BumpBigram(bg[0], bg[1]); err != nil {
					return nil, err
				}
			}
			user = ud
		}
		sess := engine.NewSession(dict, user)
		sess.SetFuzzy(sc.Config.Fuzzy)
		sess.SetTraditional(sc.Config.Traditional)
		if sc.Config.Shuangpin != "" {
			sess.SetDoublePinyin(sc.Config.Shuangpin)
		}

		results := make([]map[string]any, 0, len(sc.Steps))
		for _, st := range sc.Steps {
			results = append(results, runGoStep(sess, st))
		}
		if err := enc.Encode(map[string]any{"name": sc.Name, "steps": results}); err != nil {
			return nil, err
		}
	}
	return []byte(b.String()), nil
}

func runGoStep(sess *engine.Session, st step) map[string]any {
	switch st.Op {
	case "input":
		c, err := sess.Input(st.Text, st.Half)
		if err != nil {
			return errVal(err)
		}
		return compOK(c)
	case "backspace":
		c, err := sess.Backspace()
		if err != nil {
			return errVal(err)
		}
		return compOK(c)
	case "backspaceWord":
		c, err := sess.BackspaceWord()
		if err != nil {
			return errVal(err)
		}
		return compOK(c)
	case "reset":
		c, err := sess.Reset()
		if err != nil {
			return errVal(err)
		}
		return compOK(c)
	case "resetComposition":
		c, err := sess.ResetComposition()
		if err != nil {
			return errVal(err)
		}
		return compOK(c)
	case "nextPage":
		c, err := sess.NextPage()
		if err != nil {
			return errVal(err)
		}
		return compOK(c)
	case "prevPage":
		c, err := sess.PrevPage()
		if err != nil {
			return errVal(err)
		}
		return compOK(c)
	case "select":
		text, err := sess.Select(st.Index)
		if err != nil {
			return errVal(err)
		}
		return committedOK(text)
	case "selectPrefix":
		comp, _, err := sess.SelectPrefix(st.Index)
		if err != nil {
			return errVal(err)
		}
		return compOK(comp)
	case "commitRaw":
		text, err := sess.CommitRaw()
		if err != nil {
			return errVal(err)
		}
		return committedOK(text)
	case "punct":
		text, err := sess.Punct(st.Key, st.Half)
		if err != nil {
			return errVal(err)
		}
		return committedOK(text)
	case "associate":
		return candidatesOK(sess.Associate(st.Sylls))
	case "associateSelect":
		text, err := sess.AssociateSelect(st.Word, st.Sylls)
		if err != nil {
			return errVal(err)
		}
		return committedOK(text)
	default:
		return errVal(fmt.Errorf("未知操作 %s", st.Op))
	}
}

// runRustReplay 通过 replay bin 回放 Rust 侧。replayCmd 为空时用 cargo run。
func runRustReplay(replayCmd, dictPath, scenPath string) ([]byte, error) {
	if replayCmd != "" {
		return exec.Command(replayCmd, "--dict", dictPath, "--scenarios", scenPath).Output()
	}
	return exec.Command("cargo", "run", "--quiet", "--manifest-path", "crates/Cargo.toml",
		"--bin", "replay", "--", "--dict", dictPath, "--scenarios", scenPath).Output()
}

// parseReplayOut 把回放输出解析成 name -> steps。
func parseReplayOut(data []byte) (map[string][]map[string]any, []string, error) {
	out := make(map[string][]map[string]any)
	var order []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec struct {
			Name  string           `json:"name"`
			Steps []map[string]any `json:"steps"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, nil, fmt.Errorf("输出第 %d 行解析失败: %w", i+1, err)
		}
		if _, dup := out[rec.Name]; !dup {
			order = append(order, rec.Name)
		}
		out[rec.Name] = rec.Steps
	}
	return out, order, nil
}

// diffReport 保存一次对比的所有差异明细。
type diffReport struct {
	Scenario string
	Step     int
	Op       string
	Detail   string
}

// fieldDiffs 递归对比两个已解析的 JSON 值；candidates[].score 是信息性字段
// （内部打分差异不直接可见），只计数、不计入可见差异。
func fieldDiffs(path string, goV, rustV any, scoreDeltas *int) []string {
	gm, gok := goV.(map[string]any)
	rm, rok := rustV.(map[string]any)
	if gok && rok {
		var keys []string
		for k := range gm {
			keys = append(keys, k)
		}
		for k := range rm {
			if _, seen := gm[k]; !seen {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			sub := k
			if path != "" {
				sub = path + "." + k
			}
			out = append(out, fieldDiffs(sub, gm[k], rm[k], scoreDeltas)...)
		}
		return out
	}
	ga, gaok := goV.([]any)
	ra, raok := rustV.([]any)
	if gaok && raok {
		if strings.HasSuffix(path, ".candidates") {
			// 候选序列：顺序敏感对比 text/syllables/from_user；score 记信息差。
			var out []string
			if len(ga) != len(ra) {
				out = append(out, fmt.Sprintf("%s: 候选数不同 go=%d rust=%d", path, len(ga), len(ra)))
			}
			n := len(ga)
			if len(ra) < n {
				n = len(ra)
			}
			for i := 0; i < n; i++ {
				gc, gcok := ga[i].(map[string]any)
				rc, rcok := ra[i].(map[string]any)
				if !gcok || !rcok {
					out = append(out, fmt.Sprintf("%s[%d]: 结构异常", path, i))
					continue
				}
				for _, f := range []string{"text", "syllables", "from_user"} {
					if fmt.Sprintf("%v", gc[f]) != fmt.Sprintf("%v", rc[f]) {
						out = append(out, fmt.Sprintf("%s[%d].%s: go=%v rust=%v", path, i, f, gc[f], rc[f]))
					}
				}
				if fmt.Sprintf("%v", gc["score"]) != fmt.Sprintf("%v", rc["score"]) {
					*scoreDeltas++
				}
			}
			return out
		}
		if fmt.Sprintf("%v", goV) != fmt.Sprintf("%v", rustV) {
			return []string{fmt.Sprintf("%s: go=%v rust=%v", path, goV, rustV)}
		}
		return nil
	}
	if fmt.Sprintf("%v", goV) != fmt.Sprintf("%v", rustV) {
		return []string{fmt.Sprintf("%s: go=%v rust=%v", path, goV, rustV)}
	}
	return nil
}

func main() {
	mode := flag.String("mode", "compare", "go | rust | compare")
	dictPath := flag.String("dict", "data/dict_common.tsv", "词库 TSV 路径")
	scenPath := flag.String("scenarios", "tools/engineparity/scenarios.jsonl", "场景 JSONL 路径")
	outPath := flag.String("out", "", "go/rust 模式的输出文件路径")
	goOut := flag.String("go-out", "", "compare 模式保留 Go 侧输出（可选）")
	rustOut := flag.String("rust-out", "", "compare 模式保留 Rust 侧输出（可选）")
	replayBin := flag.String("replay", "", "Rust replay 预编译二进制路径；空=用 cargo run")
	flag.Parse()

	scenData, err := os.ReadFile(*scenPath)
	if err != nil {
		fatal("读取场景失败: %v", err)
	}
	var scens []scenario
	for i, line := range strings.Split(string(scenData), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var sc scenario
		if err := json.Unmarshal([]byte(line), &sc); err != nil {
			fatal("场景第 %d 行解析失败: %v", i+1, err)
		}
		scens = append(scens, sc)
	}

	switch *mode {
	case "go":
		dict, _, err := engine.LoadDictFilesLenient([]string{*dictPath})
		if err != nil {
			fatal("加载词库失败: %v", err)
		}
		data, err := runGoScenarios(dict, scens)
		if err != nil {
			fatal("Go 回放失败: %v", err)
		}
		writeOut(*outPath, data)
	case "rust":
		data, err := runRustReplay(*replayBin, *dictPath, *scenPath)
		if err != nil {
			fatal("Rust 回放失败: %v", err)
		}
		writeOut(*outPath, data)
	case "compare":
		runCompare(*dictPath, *scenPath, scens, *goOut, *rustOut, *replayBin)
	default:
		fatal("未知 mode %q（应为 go/rust/compare）", *mode)
	}
}

func runCompare(dictPath, scenPath string, scens []scenario, goOutPath, rustOutPath, replayBin string) {
	dict, _, err := engine.LoadDictFilesLenient([]string{dictPath})
	if err != nil {
		fatal("加载词库失败: %v", err)
	}
	goData, err := runGoScenarios(dict, scens)
	if err != nil {
		fatal("Go 回放失败: %v", err)
	}
	rustData, err := runRustReplay(replayBin, dictPath, scenPath)
	if err != nil {
		fatal("Rust 回放失败: %v", err)
	}
	if goOutPath != "" {
		writeOut(goOutPath, goData)
	}
	if rustOutPath != "" {
		writeOut(rustOutPath, rustData)
	}

	goSteps, _, err := parseReplayOut(goData)
	if err != nil {
		fatal("解析 Go 输出: %v", err)
	}
	rustSteps, order, err := parseReplayOut(rustData)
	if err != nil {
		fatal("解析 Rust 输出: %v", err)
	}

	var diffs []diffReport
	scoreDeltas := 0
	passed := 0
	for _, name := range order {
		g, inGo := goSteps[name]
		r, inRust := rustSteps[name]
		if !inGo || !inRust {
			diffs = append(diffs, diffReport{Scenario: name, Detail: "一侧缺少该场景"})
			continue
		}
		if len(g) != len(r) {
			diffs = append(diffs, diffReport{Scenario: name,
				Detail: fmt.Sprintf("步骤数不同 go=%d rust=%d", len(g), len(r))})
			continue
		}
		scenOK := true
		for i := range g {
			var sd int
			fd := fieldDiffs("", g[i], r[i], &sd)
			scoreDeltas += sd
			if len(fd) > 0 {
				scenOK = false
				op := stepOp(scens, name, i)
				diffs = append(diffs, diffReport{Scenario: name, Step: i, Op: op,
					Detail: strings.Join(fd, "; ")})
			}
		}
		if scenOK {
			passed++
		}
	}

	total := len(order)
	fmt.Printf("engineparity: 共 %d 个场景，用户可见行为一致 %d 个，存在差异 %d 个（score 信息性差异 %d 处）\n",
		total, passed, total-passed, scoreDeltas)
	const maxDetail = 20
	for i, d := range diffs {
		if i >= maxDetail {
			fmt.Printf("  … 另有 %d 条差异未展开（用 -go-out/-rust-out 保留全量输出排查）\n", len(diffs)-maxDetail)
			break
		}
		if d.Op != "" {
			fmt.Printf("  ✗ %s 步骤 %d(%s): %s\n", d.Scenario, d.Step, d.Op, d.Detail)
		} else {
			fmt.Printf("  ✗ %s: %s\n", d.Scenario, d.Detail)
		}
	}
	if len(diffs) > 0 {
		os.Exit(1)
	}
}

// stepOp 查询场景第 step 步的操作名（报告展示用）。
func stepOp(scens []scenario, name string, idx int) string {
	for _, sc := range scens {
		if sc.Name == name && idx < len(sc.Steps) {
			return sc.Steps[idx].Op
		}
	}
	return "?"
}

func writeOut(path string, data []byte) {
	if path == "" {
		os.Stdout.Write(data)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal("创建输出目录失败: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fatal("写出失败: %v", err)
	}
	fmt.Printf("已写出 %s\n", path)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "engineparity: "+format+"\n", args...)
	os.Exit(2)
}
