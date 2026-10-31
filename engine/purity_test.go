package engine

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestEngineStaysPlatformNeutral 是 engine 包纯净约束的机器守卫：
// engine 只做拼音匹配/学习/词库解析，**不得**引入任何平台相关代码。
//
// 规则（2026-11-01 用户再次强调「engine 永远保持纯净」后固化为测试）：
//  1. 只允许标准库 import（第三方路径首段带 "." 一律拒绝，含 chaoset.ime/...）；
//  2. 禁止 CGO（import "C"）与 platform/OS 专属包（syscall/unsafe/os/exec/
//     net/plugin/runtime/cgo）；
//  3. 禁止任何 //go:build 构建标签与 *_windows.go / *_linux.go / *_darwin.go
//     等平台文件——出现即说明 engine 里混进了平台分支。
//
// 平台差异只能留在壳（shims/）里；engine 通过参数/接口接收平台状态。
func TestEngineStaysPlatformNeutral(t *testing.T) {
	deniedImports := map[string]bool{
		"C": true, "syscall": true, "unsafe": true, "os/exec": true,
		"net": true, "net/http": true, "plugin": true, "runtime/cgo": true,
	}
	platformSuffixes := []string{
		"_windows.go", "_linux.go", "_darwin.go", "_freebsd.go", "_openbsd.go",
		"_netbsd.go", "_solaris.go", "_android.go", "_ios.go", "_unix.go",
	}
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
		for _, suffix := range platformSuffixes {
			if strings.HasSuffix(name, suffix) {
				t.Errorf("engine/%s 是平台专属文件；engine 必须保持平台中立（平台差异放 shims/）", name)
			}
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("解析 engine/%s 失败: %v", name, err)
		}
		parsed++
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if strings.HasPrefix(comment.Text, "//go:build") || strings.HasPrefix(comment.Text, "// +build") {
					t.Errorf("engine/%s 含构建标签 %q；engine 不得按平台条件编译", name, comment.Text)
				}
			}
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, "\"")
			if deniedImports[path] {
				t.Errorf("engine/%s 导入了平台相关包 %q；engine 必须保持纯净", name, path)
				continue
			}
			first := path
			if idx := strings.IndexByte(first, '/'); idx >= 0 {
				first = first[:idx]
			}
			if strings.Contains(first, ".") {
				t.Errorf("engine/%s 导入了非标准库 %q；engine 只允许标准库（拼音逻辑无外部依赖）", name, path)
			}
		}
	}
	if parsed == 0 {
		t.Fatal("engine 目录里没有解析到任何 .go 文件（测试路径错误？）")
	}
}
