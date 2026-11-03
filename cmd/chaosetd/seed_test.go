package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedDictFilesCopiesOnlyMissingTargets(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.tsv")
	if err := os.WriteFile(seed, []byte("种子\tzhi\t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "existing.tsv")
	if err := os.WriteFile(existing, []byte("已有\tyou\t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nested", "dict.tsv")

	got := seedDictFiles(seed, []string{existing, missing})
	if len(got) != 2 || got[0] != existing || got[1] != missing {
		t.Fatalf("seedDictFiles 返回值 = %v", got)
	}
	body, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "已有\tyou\t1\n" {
		t.Fatalf("已存在的目标被覆盖: %q", body)
	}
	body, err = os.ReadFile(missing)
	if err != nil {
		t.Fatalf("缺失目标未初始化: %v", err)
	}
	if string(body) != "种子\tzhi\t1\n" {
		t.Fatalf("种子内容 = %q", body)
	}
}

func TestSeedDictFilesReplacesEmptyTarget(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.tsv")
	if err := os.WriteFile(seed, []byte("种子\tzhi\t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "dict.tsv")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := seedDictFiles(seed, []string{target})
	if len(got) != 1 || got[0] != target {
		t.Fatalf("空目标应重建为种子词库: %v", got)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "种子\tzhi\t1\n" {
		t.Fatalf("空目标内容 = %q", body)
	}
}

func TestSeedDictFilesFallsBackToSeedOnUnreadableSeed(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "missing-seed.tsv")
	target := filepath.Join(dir, "dict.tsv")
	got := seedDictFiles(seed, []string{target})
	if len(got) != 1 || got[0] != target {
		t.Fatalf("种子不可读时应原样返回目标: %v", got)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("种子不可读时不应创建目标: %v", err)
	}
}

func TestEnsureParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "userdict.json")
	ensureParentDir(path)
	if st, err := os.Stat(filepath.Dir(path)); err != nil || !st.IsDir() {
		t.Fatalf("父目录未创建: %v", err)
	}
}
