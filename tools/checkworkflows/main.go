// checkworkflows 校验 .github/workflows/*.yml 的结构完整性。
//
// 为什么需要这个工具（2026-10-25 的事故）：给 package-macos 插入新步骤时，
// 文本替换的范围覆盖过头，把紧随其后的 package-linux / package-windows
// 两个 job 的头部整段删掉了。结果：
//   - YAML 依然能正常解析（语法没坏）；
//   - 但 jobs 从 5 个变成 3 个，GitHub 直接判「workflow file issue」，
//     整个 CI 跑不起来，三端打包与 Release 全部无法产出；
//   - release.needs 引用了不存在的 job，纯 YAML 校验也不会报错。
//
// 也就是说「yaml 能解析」远不足以证明 workflow 可用，必须校验**结构**。
//
// 检查项：
//  1. 每个 workflow 必须有非空 jobs；
//  2. 每个 job 必须有 runs-on 与 steps，且每个 step 有 uses 或 run；
//  3. needs 引用的 job 必须真实存在（拼错/被删会在这里暴露）；
//  4. needs 不得成环（DFS 检测）；
//  5. 可选：--expect-jobs 断言关键 job 存在（防止被静默删掉）。
//
// 为什么手写 YAML 子集解析器而不是用第三方库：项目 go.mod 是零第三方
// 依赖，为一个检查工具引入 yaml 库不值得。GitHub workflow 实际用到的
// 结构特性有限（块映射/块序列/flow 序列/块标量，无锚点、无 matrix），
// 解析器只覆盖这个子集；遇到解析不了的行显式报错而不是静默跳过。
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// node 是解析出的 YAML 子集值：标量、映射或序列三选一。
type node struct {
	scalar string
	m      map[string]*node
	order  []string // 文档顺序（jobs 输出按 YAML 文档序，与 Python 版一致）
	seq    []*node
	kind   byte // 's' 标量 / 'm' 映射 / 'q' 序列
}

func (n *node) get(key string) *node {
	if n == nil || n.kind != 'm' {
		return nil
	}
	return n.m[key]
}

func (n *node) str() string {
	if n == nil || n.kind != 's' {
		return ""
	}
	return n.scalar
}

func (n *node) put(key string, v *node) {
	if n.m == nil {
		n.m = map[string]*node{}
	}
	if _, exists := n.m[key]; !exists {
		n.order = append(n.order, key)
	}
	n.m[key] = v
}

func (n *node) keys() []string {
	if n == nil || n.kind != 'm' {
		return nil
	}
	return n.order
}

// needsList 把 needs 值统一成字符串列表。
// GitHub Actions 允许两种写法：needs: test 与 needs: [a, b]。
// 早期 Python 版直接对 needs 做 for 循环，字符串被按字符迭代，
// needs: test 被报成「引用了不存在的 job: t / e / s / t」——
// 校验器自己先出错。显式区分标量与序列。
func needsList(n *node) []string {
	if n == nil {
		return nil
	}
	switch n.kind {
	case 's':
		if v := strings.TrimSpace(n.scalar); v != "" {
			return []string{v}
		}
		return nil
	case 'q':
		out := make([]string, 0, len(n.seq))
		for _, it := range n.seq {
			if v := strings.TrimSpace(it.str()); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return nil
}

type line struct {
	indent  int
	content string
	num     int // 1 起行号
}

// scanLines 按行读入（bufio.Scanner 自动处理行尾与 CRLF），去注释/空行，
// 算缩进；识别块标量（key: 竖线 或 key: >）并吞掉其全部字面内容行——
// 块标量里可能含任意文本（run 脚本里的 YAML 形状文本），必须整体跳过。
func scanLines(path string) ([]line, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var raw []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		raw = append(raw, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	var out []line
	for i := 0; i < len(raw); i++ {
		text := strings.TrimRight(raw[i], " ")
		trimmed := strings.TrimLeft(text, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(text) - len(trimmed)
		l := line{indent: indent, content: trimmed, num: i + 1}
		if v, ok := splitKeyScalar(trimmed); ok && isBlockScalar(v) {
			for j := i + 1; j < len(raw); j++ {
				t2 := strings.TrimRight(raw[j], " ")
				t2t := strings.TrimLeft(t2, " ")
				if t2t == "" {
					continue
				}
				ind2 := len(t2) - len(t2t)
				if ind2 <= indent {
					i = j - 1
					break
				}
				if j == len(raw)-1 {
					i = j
				}
			}
		}
		out = append(out, l)
	}
	return out, nil
}

func splitKeyScalar(content string) (string, bool) {
	idx := strings.Index(content, ": ")
	if idx >= 0 {
		return strings.TrimSpace(content[idx+2:]), true
	}
	if strings.HasSuffix(content, ":") {
		return "", true
	}
	return "", false
}

func isBlockScalar(v string) bool {
	v = strings.TrimSpace(v)
	if len(v) == 0 || (v[0] != '|' && v[0] != '>') {
		return false
	}
	for _, c := range v[1:] {
		if c != '-' && c != '+' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func splitKV(content string) (string, string, bool) {
	idx := strings.Index(content, ": ")
	if idx < 0 {
		if strings.HasSuffix(content, ":") {
			return strings.TrimSpace(content[:len(content)-1]), "", true
		}
		return "", "", false
	}
	key := strings.Trim(strings.TrimSpace(content[:idx]), string(rune(34)))
	value := stripComment(strings.TrimSpace(content[idx+2:]))
	return key, value, true
}

func stripComment(s string) string {
	inStr := byte(0)
	dq := byte(34)
	sq := byte(39)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if c == inStr {
				inStr = 0
			}
			continue
		}
		if c == dq || c == sq {
			inStr = c
			continue
		}
		if c == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == 9) {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

// parseScalarOrFlow 解析标量或 flow 序列（needs: [a, b] 这类写法）。
func parseScalarOrFlow(value string) *node {
	v := strings.TrimSpace(value)
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		inner := strings.TrimSpace(v[1 : len(v)-1])
		n := &node{kind: 'q'}
		if inner == "" {
			return n
		}
		for _, part := range strings.Split(inner, ",") {
			item := strings.Trim(strings.TrimSpace(part), string(rune(34)))
			if item == "" {
				continue
			}
			n.seq = append(n.seq, &node{kind: 's', scalar: item})
		}
		return n
	}
	return &node{kind: 's', scalar: v}
}

// parseBlock 递归解析 indent 层级的块。
func parseBlock(lines []line, i int, indent int) (*node, int, error) {
	if i >= len(lines) || lines[i].indent < indent {
		return nil, i, nil
	}
	if strings.HasPrefix(lines[i].content, "- ") || lines[i].content == "-" {
		n := &node{kind: 'q'}
		for i < len(lines) && lines[i].indent == indent &&
			(strings.HasPrefix(lines[i].content, "- ") || lines[i].content == "-") {
			item := lines[i]
			rest := strings.TrimPrefix(strings.TrimPrefix(item.content, "-"), " ")
			if rest == "" {
				child, next, err := parseBlock(lines, i+1, indent+1)
				if err != nil {
					return nil, i, err
				}
				n.seq = append(n.seq, child)
				i = next
				continue
			}
			if _, _, ok := splitKV(rest); ok {
				// "- key: value" 且后续行逻辑缩进错位：重排成虚拟行再递归
				virt := []line{{indent: indent + 2, content: rest, num: item.num}}
				j := i + 1
				for j < len(lines) && lines[j].indent > indent &&
					!strings.HasPrefix(lines[j].content, "- ") {
					virt = append(virt, line{indent: indent + 2, content: lines[j].content, num: lines[j].num})
					j++
				}
				child, _, err := parseBlock(virt, 0, indent+2)
				if err != nil {
					return nil, i, err
				}
				n.seq = append(n.seq, child)
				i = j
				continue
			}
			n.seq = append(n.seq, &node{kind: 's', scalar: stripComment(rest)})
			i++
		}
		return n, i, nil
	}
	n := &node{kind: 'm'}
	for i < len(lines) && lines[i].indent == indent {
		content := lines[i].content
		if strings.HasPrefix(content, "- ") {
			break
		}
		key, value, ok := splitKV(content)
		if !ok {
			return nil, i, fmt.Errorf("第 %d 行无法解析: %q", lines[i].num, content)
		}
		if value == "" {
			if i+1 < len(lines) && lines[i+1].indent > indent {
				child, next, err := parseBlock(lines, i+1, lines[i+1].indent)
				if err != nil {
					return nil, i, err
				}
				n.put(key, child)
				i = next
				continue
			}
			if i+1 < len(lines) && lines[i+1].indent == indent &&
				strings.HasPrefix(lines[i+1].content, "- ") {
				child, next, err := parseBlock(lines, i+1, indent)
				if err != nil {
					return nil, i, err
				}
				n.put(key, child)
				i = next
				continue
			}
			n.put(key, &node{kind: 's'})
			i++
			continue
		}
		if isBlockScalar(value) {
			// 块标量内容已被 scanLines 吞掉
			n.put(key, &node{kind: 's', scalar: "<block>"})
			i++
			continue
		}
		n.put(key, parseScalarOrFlow(value))
		i++
	}
	return n, i, nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	var expectJobs []string
	var files []string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--expect-jobs":
			i++
			if i >= len(argv) {
				fmt.Fprintln(os.Stderr, "--expect-jobs 需要一个逗号分隔的 job 列表")
				return 2
			}
			for _, x := range strings.Split(argv[i], ",") {
				if x = strings.TrimSpace(x); x != "" {
					expectJobs = append(expectJobs, x)
				}
			}
		case "-h", "--help":
			fmt.Println("用法: checkworkflows [--expect-jobs job1,job2,...] <workflow.yml> [...]")
			return 0
		default:
			files = append(files, argv[i])
		}
	}
	if len(files) == 0 {
		m1, _ := filepath.Glob(".github/workflows/*.yml")
		m2, _ := filepath.Glob(".github/workflows/*.yaml")
		files = append(m1, m2...)
		sort.Strings(files)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "没有找到任何 workflow 文件")
		return 1
	}

	var allErrors []string
	for _, path := range files {
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			allErrors = append(allErrors, path+": 文件不存在")
			continue
		}
		errs, jobs := checkFile(path, expectJobs)
		if len(errs) > 0 {
			allErrors = append(allErrors, errs...)
			continue
		}
		fmt.Printf("ok   %s（jobs: %s）\n", path, strings.Join(jobs, ", "))
	}
	if len(allErrors) > 0 {
		fmt.Println()
		for _, e := range allErrors {
			fmt.Println("FAIL " + e)
		}
		return 1
	}
	return 0
}

func checkFile(path string, expectJobs []string) ([]string, []string) {
	lines, err := scanLines(path)
	if err != nil {
		return []string{path + ": " + err.Error()}, nil
	}
	if len(lines) == 0 {
		return []string{path + ": 空文件"}, nil
	}
	doc, _, err := parseBlock(lines, 0, lines[0].indent)
	if err != nil {
		return []string{path + ": " + err.Error()}, nil
	}
	if doc == nil || doc.kind != 'm' {
		return []string{path + ": 顶层不是映射"}, nil
	}
	jobsNode := doc.get("jobs")
	if jobsNode == nil || jobsNode.kind != 'm' || len(jobsNode.m) == 0 {
		return []string{path + ": 没有 jobs（或为空）"}, nil
	}
	jobs := jobsNode.keys()

	var errs []string
	for _, j := range expectJobs {
		if _, ok := jobsNode.m[j]; !ok {
			errs = append(errs, fmt.Sprintf("%s: 缺少预期 job: %s（现有: %s）", path, j, strings.Join(jobs, ", ")))
		}
	}

	for _, name := range jobs {
		job := jobsNode.m[name]
		if job.kind != 'm' {
			errs = append(errs, fmt.Sprintf("%s: job %s 不是映射", path, name))
			continue
		}
		if v := job.get("runs-on"); v == nil || v.str() == "" {
			errs = append(errs, fmt.Sprintf("%s: job %s 缺少 runs-on", path, name))
		}
		steps := job.get("steps")
		if steps == nil || steps.kind != 'q' || len(steps.seq) == 0 {
			errs = append(errs, fmt.Sprintf("%s: job %s 缺少 steps", path, name))
		} else {
			for i, step := range steps.seq {
				if step.kind != 'm' {
					errs = append(errs, fmt.Sprintf("%s: job %s 第 %d 个 step 不是映射", path, name, i+1))
					continue
				}
				if step.get("uses") == nil && step.get("run") == nil {
					errs = append(errs, fmt.Sprintf("%s: job %s 第 %d 个 step 既没有 uses 也没有 run", path, name, i+1))
				}
			}
		}
		for _, need := range needsList(job.get("needs")) {
			if _, ok := jobsNode.m[need]; !ok {
				errs = append(errs, fmt.Sprintf("%s: job %s 的 needs 引用了不存在的 job: %s", path, name, need))
			}
		}
	}

	// needs 成环检测（DFS 三色标记）
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	for _, j := range jobs {
		color[j] = white
	}
	var dfs func(n string, stack []string)
	dfs = func(n string, stack []string) {
		color[n] = gray
		for _, need := range needsList(jobsNode.m[n].get("needs")) {
			if _, ok := jobsNode.m[need]; !ok {
				continue
			}
			switch color[need] {
			case gray:
				errs = append(errs, fmt.Sprintf("%s: needs 存在环: %s -> %s", path, strings.Join(stack, " -> "), need))
			case white:
				dfs(need, append(stack, need))
			}
		}
		color[n] = black
	}
	for _, j := range jobs {
		if color[j] == white {
			dfs(j, []string{j})
		}
	}
	return errs, jobs
}
