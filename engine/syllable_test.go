package engine

import (
	"testing"
)

func TestSyllableTable(t *testing.T) {
	valid := []string{"a", "ni", "hao", "zhuang", "lv", "nve", "xian", "shei", "dia", "rua", "yan", "yang"}
	invalid := []string{"b", "ng", "xx", "nih", "bianp", "wuang"}
	for _, s := range valid {
		if !IsSyllable(s) {
			t.Errorf("%q 应为合法音节", s)
		}
	}
	for _, s := range invalid {
		if IsSyllable(s) {
			t.Errorf("%q 不应为合法音节", s)
		}
	}
}

func TestSyllablesWithPrefix(t *testing.T) {
	got := SyllablesWithPrefix("zhua")
	want := []string{"zhua", "zhuai", "zhuan", "zhuang"}
	if len(got) != len(want) {
		t.Fatalf("zhua 前缀音节数 = %d, 期望 %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("zhua 前缀音节[%d] = %q, 期望 %q", i, got[i], want[i])
		}
	}
	if got := SyllablesWithPrefix("zz"); len(got) != 0 {
		t.Errorf("zz 不应有前缀匹配: %v", got)
	}
}
