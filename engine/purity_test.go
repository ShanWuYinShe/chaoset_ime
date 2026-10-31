package engine

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// engine 平台中立性守卫的规则集中在这里（2026-11-01 用户再次强调
// 「engine 永远保持纯净」后固化）：
//  1. 只允许标准库 import（第三方路径首段带 "." 一律拒绝，含项目内部包）；
//  2. 禁止 CGO（import "C"）与 platform/OS 专属包；
//  3. 禁止任何 //go:build 构建标签与平台专属文件名。
//
// 平台差异只能留在壳（shims/）里；engine 通过参数/接口接收平台状态。
var engineDeniedImports = map[string]bool{
	"C": true, "syscall": true, "unsafe": true, "os/exec": true,
	"net": true, "net/http": true, "plugin": true, "runtime/cgo": true,
}

var enginePlatformSuffixes = []string{
	"_windows.go", "_linux.go", "_darwin.go", "_freebsd.go", "_openbsd.go",
	"_netbsd.go", "_solaris.go", "_android.go", "_ios.go", "_unix.go",
}

// enginePurityProblems 返回单个 engine .go 文件违反平台中立约束的问题。
// 纯函数（文件名 + 源码），便于用合成源码做变异测试，避免守卫本身悄悄失效。
func enginePurityProblems(name string, src []byte) []string {
	var problems []string
	for _, suffix := range enginePlatformSuffixes {
		if strings.HasSuffix(name, suffix) {
			problems = append(problems, fmt.Sprintf(
				"engine/%s 是平台专属文件；engine 必须保持平台中立（平台差异放 shims/）", name))
		}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return append(problems, fmt.Sprintf("解析 engine/%s 失败: %v", name, err))
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:build") ||
				strings.HasPrefix(comment.Text, "// +build") {
				problems = append(problems, fmt.Sprintf(
					"engine/%s 含构建标签 %q；engine 不得按平台条件编译", name, comment.Text))
			}
		}
	}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, "\"")
		if engineDeniedImports[path] {
			problems = append(problems, fmt.Sprintf(
				"engine/%s 导入了平台相关包 %q；engine 必须保持纯净", name, path))
			continue
		}
		first := path
		if idx := strings.IndexByte(first, '/'); idx >= 0 {
			first = first[:idx]
		}
		if strings.Contains(first, ".") {
			problems = append(problems, fmt.Sprintf(
				"engine/%s 导入了非标准库 %q；engine 只允许标准库（拼音逻辑无外部依赖）",
				name, path))
		}
	}
	return problems
}

// TestEngineStaysPlatformNeutral 对真实 engine 目录跑守卫：违反任何一条规则
// 即 go test ./... 红。
func TestEngineStaysPlatformNeutral(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取 engine 目录失败: %v", err)
	}
	parsed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读取 engine/%s 失败: %v", name, err)
		}
		parsed++
		for _, problem := range enginePurityProblems(name, src) {
			t.Error(problem)
		}
	}
	if parsed == 0 {
		t.Fatal("engine 目录里没有解析到任何 .go 文件（测试路径错误？）")
	}
}

// TestEnginePurityGuardCatchesViolations 用合成源码变异测试守卫规则本身：
// 平台文件/构建标签/CGO/平台包/第三方 import 都必须被捕获，干净文件不得误报。
func TestEnginePurityGuardCatchesViolations(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // 空 = 期望无问题
	}{
		{"x_windows.go", "package engine\n", "平台专属文件"},
		{"x.go", "//go:build linux\n\npackage engine\n", "构建标签"},
		{"x.go", "package engine\n\nimport \"syscall\"\n", "平台相关包"},
		{"x.go", "package engine\n\nimport \"C\"\n", "平台相关包"},
		{"x.go", "package engine\n\nimport \"github.com/foo/bar\"\n", "非标准库"},
		{"x.go", "package engine\n\nimport \"chaoset.ime/engine\"\n", "非标准库"},
		{"x.go", "package engine\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name+"/"+c.want, func(t *testing.T) {
			problems := enginePurityProblems(c.name, []byte(c.src))
			if c.want == "" {
				if len(problems) != 0 {
					t.Fatalf("干净文件不应报错，实际 %v", problems)
				}
				return
			}
			if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Fatalf("应捕获 %q，实际 %v", c.want, problems)
			}
		})
	}
}
