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
