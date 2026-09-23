package engine

import (
	"fmt"
	"strings"
	"testing"
)

// syntheticEntries 生成 count 条合成词条：双音节 CJK 词（音节取自
// 音节表的两两组合），词频递减。用于在不受外部词库文件影响的前提下
// 复现「十万级词库」的规模化行为。
func syntheticEntries(count int) []*Entry {
	out := make([]*Entry, 0, count)
	n := 0
	for i := 0; i < len(syllableTable) && n < count; i++ {
		for j := 0; j < len(syllableTable) && n < count; j++ {
			si, sj := syllableTable[i], syllableTable[j]
			out = append(out, &Entry{
				Word:      fmt.Sprintf("词%06d", n),
				Syllables: []string{si, sj},
				Freq:      99 - n%90,
			})
			n++
		}
	}
	return out
}

func mustSyntheticDict(b *testing.B, count int) *Dictionary {
	b.Helper()
	entries := syntheticEntries(count)
	d := NewDictionary()
	for _, e := range entries {
		d.add(e.Word, e.Syllables, e.Freq)
	}
	return d
}

// BenchmarkLoadDict100k 衡量十万级 TSV 词库的加载耗时（daemon 启动路径）。
func BenchmarkLoadDict100k(b *testing.B) {
	entries := syntheticEntries(100000)
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Word)
		sb.WriteByte('\t')
		sb.WriteString(strings.Join(e.Syllables, " "))
		sb.WriteByte('\t')
		sb.WriteString(fmt.Sprint(e.Freq))
		sb.WriteByte('\n')
	}
	src := sb.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		b.StartTimer()
		if _, err := ParseDict(strings.NewReader(src)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompose100k 衡量十万级词库下单次组词（含全量候选排序与
// 分页切片）。
func BenchmarkCompose100k(b *testing.B) {
	d := mustSyntheticDict(b, 100000)
	s := NewSession(d, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Reset()
		s.Input("ni")
		s.Compose()
	}
}

// BenchmarkQueryPrefix100k 衡量残缺音节前缀查询的线性扫描开销。
func BenchmarkQueryPrefix100k(b *testing.B) {
	d := mustSyntheticDict(b, 100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.QueryPrefix(nil, "n")
	}
}
