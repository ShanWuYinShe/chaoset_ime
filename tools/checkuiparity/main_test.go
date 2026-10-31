package main

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// TestRunOnRepo 在真实仓库上跑一遍全部契约：
//   - 当前仓库必须通过（否则本工具会把 CI 卡红）；
//   - 任何一条规则对应的文件/文案被删改时，本测试会先失败（规则接线回归）。
func TestRunOnRepo(t *testing.T) {
	problems := Run(os.DirFS("../.."))
	if len(problems) != 0 {
		t.Fatalf("真实仓库未通过 UI 一致性契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

// TestCheckCandidateWindow 用真实候选窗源码验证候选窗契约，并做一次变异：
// 改 Windows 正文字号应被「候选窗几何」捕获。
func TestCheckCandidateWindow(t *testing.T) {
	macText, err := os.ReadFile("../../" + macPanel)
	if err != nil {
		t.Fatalf("读取 mac 候选窗源码失败：%v", err)
	}
	winText, err := os.ReadFile("../../" + winShell)
	if err != nil {
		t.Fatalf("读取 Windows 壳源码失败：%v", err)
	}
	fsys := fstest.MapFS{
		macPanel: &fstest.MapFile{Data: macText},
		winShell: &fstest.MapFile{Data: winText},
	}
	var problems []string
	checkCandidateWindow(fsys, &problems)
	if len(problems) != 0 {
		t.Fatalf("真实候选窗源码未通过契约（%d 项）：\n%s", len(problems), strings.Join(problems, "\n"))
	}

	mutated := strings.Replace(string(winText), "-MulDiv(15, dpi, 72)", "-MulDiv(16, dpi, 72)", 1)
	if mutated == string(winText) {
		t.Fatal("变异未生效：Windows 源码里找不到 -MulDiv(15, dpi, 72)")
	}
	problems = nil
	checkCandidateWindow(fstest.MapFS{
		macPanel: &fstest.MapFile{Data: macText},
		winShell: &fstest.MapFile{Data: []byte(mutated)},
	}, &problems)
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "正文字号") {
		t.Fatalf("改一端字号应被捕获，实际：%v", problems)
	}
}

func TestCheckContains(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("hello 学习数据")},
	}
	var problems []string
	checkContains(fsys, &problems, "g", "a.txt", "hello", "学习数据")
	if len(problems) != 0 {
		t.Fatalf("应通过，实际：%v", problems)
	}

	problems = nil
	checkContains(fsys, &problems, "g", "a.txt", "missing")
	if len(problems) != 1 || !strings.Contains(problems[0], "missing") {
		t.Fatalf("应报缺少子串，实际：%v", problems)
	}

	problems = nil
	checkContains(fsys, &problems, "g", "nope.txt", "x")
	if len(problems) != 1 || !strings.Contains(problems[0], "读取") {
		t.Fatalf("应报读取失败，实际：%v", problems)
	}
}

func TestCheckExcludes(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("chaoset-userdict")},
	}
	var problems []string
	checkExcludes(fsys, &problems, "g", "a.txt", "dbus-send")
	if len(problems) != 0 {
		t.Fatalf("应通过，实际：%v", problems)
	}

	problems = nil
	checkExcludes(fsys, &problems, "g", "a.txt", "chaoset-userdict")
	if len(problems) != 1 || !strings.Contains(problems[0], "不应包含") {
		t.Fatalf("应报禁止子串，实际：%v", problems)
	}
}

func TestCheckExists(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("x")},
	}
	var problems []string
	checkExists(fsys, &problems, "g", "a.txt")
	if len(problems) != 0 {
		t.Fatalf("应通过，实际：%v", problems)
	}

	problems = nil
	checkExists(fsys, &problems, "g", "nope.txt")
	if len(problems) != 1 || !strings.Contains(problems[0], "缺少") {
		t.Fatalf("应报缺少文件，实际：%v", problems)
	}
}
