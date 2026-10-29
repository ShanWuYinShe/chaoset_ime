package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 运行时宽松加载的语义（2026-10-30 用户要求）：
// 用户从网上下载或手写的第三方词库常夹杂坏行（编码问题、多一列、
// 拼音写错），不能让整个输入法起不来。内置词库的正确性由 CI 保证
// （data_contract_test.go 严格解析），运行时只负责「跳过坏行 + 汇总报告」。
func TestLoadDictFilesLenientSkipsBadLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thirdparty.tsv")
	content := "# 用户自备词库\n" + // 1
		"好词\thao ci\t50\n" + // 2 合法
		"坏行没有制表符\n" + // 3 缺列
		"空拼音\t\t30\n" + // 4 拼音为空
		"非法音节\tbu shi abc\t10\n" + // 5 音节非法
		"缺词频\tque ci\n" + // 6 合法（缺省词频补 20）
		"多列\tduo lie\t40\t多余列\n" // 7 合法（取第三列）
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	d, warnings, err := LoadDictFilesLenient([]string{path})
	if err != nil {
		t.Fatalf("宽松加载不应因坏行失败: %v", err)
	}

	// 合法行必须都进来
	for _, want := range []struct {
		word  string
		sylls []string
	}{
		{"好词", []string{"hao", "ci"}},
		{"缺词频", []string{"que", "ci"}},
		{"多列", []string{"duo", "lie"}},
	} {
		if !d.Contains(want.word, want.sylls) {
			t.Errorf("合法词条 %q（%v）应被加载", want.word, want.sylls)
		}
	}
	// 坏行必须被丢弃
	for _, bad := range []string{"空拼音", "非法音节", "坏行没有制表符"} {
		if d.Contains(bad, nil) || len(d.QueryWordPrefix(bad, 1)) > 0 {
			t.Errorf("坏行 %q 不应进入词库", bad)
		}
	}

	// 警告要汇总：跳过 3 行，且给出前几条样本（含行号便于定位）
	if len(warnings) != 1 {
		t.Fatalf("应有 1 条警告（每个文件一条汇总），实际 %d 条: %v", len(warnings), warnings)
	}
	w := warnings[0]
	if !strings.Contains(w, "跳过 3 行") {
		t.Errorf("警告应说明跳过 3 行: %s", w)
	}
	for _, lineNo := range []string{"第 3 行", "第 4 行", "第 5 行"} {
		if !strings.Contains(w, lineNo) {
			t.Errorf("警告样本应含 %s: %s", lineNo, w)
		}
	}
	// 样本上限：坏行多时不刷屏
	if strings.Count(w, "第 ") > parseErrSamples {
		t.Errorf("警告样本超过上限 %d 条: %s", parseErrSamples, w)
	}
}

// 归一化只在严格校验失败后启用：用户手写的「带声调」「连写」拼音也能用。
func TestLoadDictFilesLenientAcceptsTonesAndRunOnPinyin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handmade.tsv")
	content := "你好\tnǐ hǎo\t90\n" + // 带声调
		"世界\tshijie\t80\n" + // 连写
		"绿色\tlü sè\t70\n" // ü
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	d, warnings, err := LoadDictFilesLenient([]string{path})
	if err != nil {
		t.Fatalf("宽松加载失败: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("这三行都应能归一化成功，不该有警告: %v", warnings)
	}
	for _, want := range []struct {
		word  string
		sylls []string
	}{
		{"你好", []string{"ni", "hao"}},
		{"世界", []string{"shi", "jie"}},
		{"绿色", []string{"lv", "se"}},
	} {
		if !d.Contains(want.word, want.sylls) {
			t.Errorf("%q 应归一化为 %v", want.word, want.sylls)
		}
	}
}

// 文件打不开仍是错误：那是配置错误（路径写错/权限不足），必须立刻暴露，
// 不能静默少加载一个词库让用户以为输入法坏了。
func TestLoadDictFilesLenientKeepsIOErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "不存在的.tsv")
	if _, _, err := LoadDictFilesLenient([]string{missing}); err == nil {
		t.Fatal("文件不存在必须报错，不能静默跳过")
	}
}

// 干净文件不产生警告（避免日志噪音）。
func TestLoadDictFilesLenientCleanFileNoWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.tsv")
	if err := os.WriteFile(path, []byte("你好\tni hao\t99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, warnings, err := LoadDictFilesLenient([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("干净文件不应有警告: %v", warnings)
	}
	if d.Len() != 1 {
		t.Fatalf("应加载 1 条，实际 %d", d.Len())
	}
}

// 严格加载保持原语义：一行坏数据即报错（内置词库/生成链路用）。
func TestLoadDictFilesStrictRejectsBadLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.tsv")
	if err := os.WriteFile(path, []byte("好词\thao ci\t50\n坏行\tbu shi abc\t10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDictFiles([]string{path}); err == nil {
		t.Fatal("严格加载遇到非法音节必须报错")
	}
}

// 端到端：模拟用户下载的第三方词库（CRLF 行尾 + 注释 + 坏行）仍能正常出候选。
func TestLenientThirdPartyDictUsable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "download.tsv")
	content := "你好\tni hao\t99\r\n" +
		"坏行\tbad syllable xyz\t10\r\n" +
		"世界\tshi jie\t80\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	d, warnings, err := LoadDictFilesLenient([]string{path})
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("应报告 1 条坏行汇总警告: %v", warnings)
	}
	s := NewSession(d, nil)
	comp, err := s.Input("nihao")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("坏行不应影响候选：期望首选「你好」，实际 %+v", comp.Candidates)
	}
	// CRLF 不应让词条带 \r
	if !d.Contains("世界", []string{"shi", "jie"}) {
		t.Fatal("CRLF 行尾的词条应被正确加载")
	}
}
