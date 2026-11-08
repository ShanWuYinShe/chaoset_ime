// fsutil.go：文件/文本读取与断言原语、逻辑文件聚合文件系统（bundleFS）。
// 所有检查函数都通过这里的 helper 读源码，聚合视图保证「拆分后仍按逻辑文件断言」。

package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// checkContains 断言文件包含全部子串；每个缺失项记一条问题。
func checkContains(fsys fs.FS, problems *[]string, group, path string, needles ...string) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, path, err))
		return
	}
	text := string(data)
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			*problems = append(*problems, fmt.Sprintf("%s：%s 缺少 %q", group, path, needle))
		}
	}
}

// checkExcludes 断言文件**不包含**子串（用于固定「不允许回退」的架构约束）。
func checkExcludes(fsys fs.FS, problems *[]string, group, path string, needles ...string) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：读取 %s 失败：%v", group, path, err))
		return
	}
	text := string(data)
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			*problems = append(*problems, fmt.Sprintf("%s：%s 不应包含 %q（架构/一致性约束）", group, path, needle))
		}
	}
}

// checkExists 断言路径存在（文件或目录）。
func checkExists(fsys fs.FS, problems *[]string, group, path string) {
	if _, err := fs.Stat(fsys, path); err != nil {
		*problems = append(*problems, fmt.Sprintf("%s：缺少 %s", group, path))
	}
}

func readText(fsys fs.FS, path string) (string, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func mustRead(fsys fs.FS, path string) string {
	text, err := readText(fsys, path)
	if err != nil {
		return ""
	}
	return text
}

// firstFloat 用正则从文本中提取第一个分组为数字的值。
func firstFloat(text, pattern string) (float64, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, false
	}
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// structFields 在匹配 struct 区域内按字段模式提取字段名。
func structFields(text, structRe, fieldRe string) []string {
	sre, err := regexp.Compile(structRe)
	if err != nil {
		return nil
	}
	region := sre.FindString(text)
	if region == "" {
		return nil
	}
	fre, err := regexp.Compile(fieldRe)
	if err != nil {
		return nil
	}
	var fields []string
	for _, m := range fre.FindAllStringSubmatch(region, -1) {
		fields = append(fields, m[1])
	}
	return fields
}

// ============================================================================
// 逻辑文件聚合（2026-11-06 架构拆分）
//
// 用户要求把 shims 大文件按职责拆分（见 docs/architecture.md）。拆分后
// winShell / macPanel / macSettings / linuxSetup 不再是单个文件，而是同目录
// 多文件聚合的「逻辑文件」；既有逐条文案/几何/行为断言保持不变。
// ============================================================================

type bundleSpec struct {
	dir     string
	exts    []string
	exclude map[string]bool
}

type bundleFS struct {
	base    fs.FS
	bundles map[string]bundleSpec
}

func (b bundleFS) Open(name string) (fs.File, error) {
	spec, ok := b.bundles[name]
	if !ok {
		return b.base.Open(name)
	}
	entries, err := fs.ReadDir(b.base, spec.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if spec.exclude[n] {
			continue
		}
		matched := false
		for _, ext := range spec.exts {
			if strings.HasSuffix(n, ext) {
				matched = true
				break
			}
		}
		if matched {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return b.base.Open(spec.dir)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	for _, n := range names {
		p := spec.dir + "/" + n
		data, err := fs.ReadFile(b.base, p)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&buf, "\n// ===== bundled source: %s =====\n", p)
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return &memFile{Reader: bytes.NewReader(buf.Bytes()), name: name, size: int64(buf.Len())}, nil
}

type memFile struct {
	*bytes.Reader
	name string
	size int64
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memFileInfo{f.name, f.size}, nil }

func (f *memFile) Close() error { return nil }

type memFileInfo struct {
	name string
	size int64
}

func (i memFileInfo) Name() string { return i.name }

func (i memFileInfo) Size() int64 { return i.size }

func (i memFileInfo) Mode() fs.FileMode { return 0o444 }

func (i memFileInfo) ModTime() time.Time { return time.Time{} }

func (i memFileInfo) IsDir() bool { return false }

func (i memFileInfo) Sys() any { return nil }

func lineCount(data []byte) int {
	return strings.Count(string(data), "\n") + 1
}

// defaultBundles 返回逻辑文件的聚合规格。Run 与单测共用同一份，避免单测
// 绕过聚合去读单文件（拆分后单文件不再包含完整实现）。
func defaultBundles() map[string]bundleSpec {
	return map[string]bundleSpec{
		winShell:    {dir: "shims/windows/src", exts: []string{".cpp", ".hpp"}, exclude: map[string]bool{"ui_main.cpp": true}},
		macPanel:    {dir: "shims/macos/src", exts: []string{".swift"}},
		macSettings: {dir: "shims/macos/src", exts: []string{".swift"}},
		"shims/macos/src/ChaosetController.swift": {dir: "shims/macos/src", exts: []string{".swift"}},
		"shims/macos/src/EngineClient.swift":      {dir: "shims/macos/src", exts: []string{".swift"}},
		linuxSetup:                                {dir: "shims/linux/setup", exts: []string{".c", ".h"}, exclude: map[string]bool{"test_setup_ctl.c": true}},
		linuxCtl:                                  {dir: "shims/linux/setup", exts: []string{".c", ".h"}, exclude: map[string]bool{"test_setup_ctl.c": true}},
	}
}

// bundledFS 用默认聚合规格包装 base（单测读真实仓库时与 Run 行为一致）。
func bundledFS(base fs.FS) fs.FS { return bundleFS{base: base, bundles: defaultBundles()} }

// firstInt 用正则从文本中提取第一个分组为整数的值（如页容量常量）。
func firstInt(text, pattern string) (int, bool) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, false
	}
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0, false
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return v, true
}
