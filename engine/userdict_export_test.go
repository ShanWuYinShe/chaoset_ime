package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mustManagedUserDict 构造带确定性时钟与固定学习记录的用户词典：
// 你好×3、世界×1、bigram(你好→世界)×2、词组「键盘鼠标」×2。
func mustManagedUserDict(t *testing.T) *UserDict {
	t.Helper()
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	fixed := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	u.setClock(func() time.Time { return fixed })

	bump := func(n int, f func() error) {
		t.Helper()
		for i := 0; i < n; i++ {
			if err := f(); err != nil {
				t.Fatalf("bump 失败: %v", err)
			}
		}
	}
	bump(3, func() error { return u.BumpWord("你好", []string{"ni", "hao"}) })
	bump(1, func() error { return u.BumpWord("世界", []string{"shi", "jie"}) })
	bump(2, func() error { return u.BumpBigram("你好", "世界") })
	bump(2, func() error { return u.BumpPhrase("键盘鼠标", []string{"jian", "pan", "shu", "biao"}) })
	return u
}

// Stats 统计三类学习记录的条目数。
func TestUserStats(t *testing.T) {
	u := mustManagedUserDict(t)
	st := u.Stats()
	if st.Words != 2 || st.Phrases != 1 || st.Bigrams != 1 {
		t.Fatalf("Stats = %+v, 期望 words=2 phrases=1 bigrams=1", st)
	}
}

// ExportTSV 全量导出：段头齐全、按次数降序、时间可读。
func TestExportTSVFull(t *testing.T) {
	u := mustManagedUserDict(t)
	var b strings.Builder
	if err := u.ExportTSV(&b, false); err != nil {
		t.Fatalf("ExportTSV 失败: %v", err)
	}
	out := b.String()
	for _, want := range []string{
		"# 词\t拼音\t次数\t最近使用",
		"你好\tni hao\t3\t2026-09-24T12:00:00Z",
		"# 词组\t拼音\t次数\t最近使用",
		"键盘鼠标\tjian pan shu biao\t2\t",
		"# 上下文\t前词\t后词\t次数",
		"你好\t世界\t2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("导出缺少 %q，实际：\n%s", want, out)
		}
	}
	// 次数降序：你好(3) 在 世界(1) 之前
	if strings.Index(out, "你好\tni hao") > strings.Index(out, "世界\tshi jie") {
		t.Fatalf("导出应按次数降序：\n%s", out)
	}
}

// phrasesOnly 导出即合法词库 TSV：可直接被 ParseDict 解析。
func TestExportTSVPhrasesOnly(t *testing.T) {
	u := mustManagedUserDict(t)
	var b strings.Builder
	if err := u.ExportTSV(&b, true); err != nil {
		t.Fatalf("ExportTSV 失败: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "键盘鼠标\tjian pan shu biao\t2\n") {
		t.Fatalf("词组导出格式不符：\n%s", out)
	}
	if strings.Contains(out, "#") || strings.Contains(out, "你好") {
		t.Fatalf("phrasesOnly 不应包含注释或其他段：\n%s", out)
	}
	if _, err := ParseDict(strings.NewReader(out)); err != nil {
		t.Fatalf("phrasesOnly 导出应可直接 ParseDict: %v\n%s", err, out)
	}
}

// Clear 清空全部记录并落盘；重载文件为空。
func TestUserClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}
	u.setClock(func() time.Time { return time.Unix(1758700000, 0) })
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	if err := u.Clear(); err != nil {
		t.Fatalf("Clear 失败: %v", err)
	}
	if st := u.Stats(); st.Words != 0 || st.Phrases != 0 || st.Bigrams != 0 {
		t.Fatalf("Clear 后内存 Stats = %+v", st)
	}
	reloaded, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("重载失败: %v", err)
	}
	if st := reloaded.Stats(); st.Words != 0 || st.Phrases != 0 || st.Bigrams != 0 {
		t.Fatalf("Clear 后磁盘 Stats = %+v", st)
	}
}
