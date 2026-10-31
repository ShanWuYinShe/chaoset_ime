package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWithImportedDict：Linux 设置窗导入的用户词库只在该文件可读且为普通
// 文件时追加到主词库列表；不存在/空路径/目录必须跳过（可选文件异常不能让
// 引擎起不来）。
func TestWithImportedDict(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "dict.tsv")
	if err := os.WriteFile(present, []byte("测试词\tce shi ci\t99\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := withImportedDict([]string{"main.tsv"}, present)
	if len(got) != 2 || got[1] != present {
		t.Fatalf("文件存在时应追加：%v", got)
	}
	if got := withImportedDict([]string{"main.tsv"}, filepath.Join(dir, "missing.tsv")); len(got) != 1 {
		t.Fatalf("文件不存在时不应追加：%v", got)
	}
	// 目录（不可读为词库）也不能追加，否则引擎启动时 LoadDictFilesLenient 报错退出。
	if got := withImportedDict([]string{"main.tsv"}, dir); len(got) != 1 {
		t.Fatalf("目录不应追加：%v", got)
	}
	if got := withImportedDict([]string{"main.tsv"}, ""); len(got) != 1 {
		t.Fatalf("空路径不应追加：%v", got)
	}
}
