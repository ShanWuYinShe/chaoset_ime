package engine

import (
	"path/filepath"
	"testing"
)

func BenchmarkLoadCommonDict(b *testing.B) {
	path := filepath.Join("..", "data", "dict_common.tsv")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LoadDictFiles([]string{path}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkComposeCommonDict(b *testing.B) {
	d, err := LoadDictFiles([]string{filepath.Join("..", "data", "dict_common.tsv")})
	if err != nil {
		b.Fatal(err)
	}
	s := NewSession(d, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Reset()
		s.Input("jingchang")
	}
}
