package cliutil

import (
	"flag"
	"testing"
)

// RepeatFlag 必须能被 flag.Var 多次 Set 并保持传入顺序（-dict a -dict b）。
func TestRepeatFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var got RepeatFlag
	fs.Var(&got, "dict", "test")
	if err := fs.Parse([]string{"-dict", "a.tsv", "-dict", "b.tsv", "-dict", "c.tsv"}); err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if len(got) != 3 || got[0] != "a.tsv" || got[1] != "b.tsv" || got[2] != "c.tsv" {
		t.Fatalf("RepeatFlag = %v, 期望 [a.tsv b.tsv c.tsv]", got)
	}
	if s := got.String(); s != "a.tsv,b.tsv,c.tsv" {
		t.Fatalf("String() = %q", s)
	}
}
