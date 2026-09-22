package engine

import (
	"strings"
	"testing"
)

// 测试词库：覆盖整词、一字多行（多音）、同音词竞争。
const testDict = `
# 测试词库
你好	ni hao	99
西安	xi an	70
先	xian	60
洗	xi	55
安	an	50
实施	shi shi	60
试试	shi shi	10
`

func mustDict(t *testing.T) *Dictionary {
	t.Helper()
	d, err := ParseDict(strings.NewReader(testDict))
	if err != nil {
		t.Fatalf("解析测试词库失败: %v", err)
	}
	return d
}

func TestParseDictRejectsBadSyllable(t *testing.T) {
	_, err := ParseDict(strings.NewReader("坏词\tbu shi abc\t10"))
	if err == nil || !strings.Contains(err.Error(), "abc") {
		t.Fatalf("非法音节应报错并指出音节，得到: %v", err)
	}
}

func TestParseDictDedupesKeepingMaxFreq(t *testing.T) {
	d, err := ParseDict(strings.NewReader("你好\tni hao\t10\n你好\tni hao\t88\n"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	entries := d.Query([]string{"ni", "hao"})
	if len(entries) != 1 || entries[0].Freq != 88 {
		t.Fatalf("重复词条应去重保留最大词频，得到 %+v", entries)
	}
}

func TestQueryExact(t *testing.T) {
	d := mustDict(t)
	entries := d.Query([]string{"ni", "hao"})
	if len(entries) != 1 || entries[0].Word != "你好" {
		t.Fatalf("Query(ni hao) = %+v, 期望 [你好]", entries)
	}
	if entries := d.Query([]string{"xian"}); len(entries) != 1 || entries[0].Word != "先" {
		t.Fatalf("Query(xian) = %+v, 期望 [先]", entries)
	}
	if entries := d.Query([]string{"no", "such"}); len(entries) != 0 {
		t.Fatalf("Query(no such) 应为空，得到 %+v", entries)
	}
}

func TestQueryPrefix(t *testing.T) {
	d := mustDict(t)
	// ni + h（hao 的前缀）应命中「你好」
	entries := d.QueryPrefix([]string{"ni"}, "h")
	found := false
	for _, e := range entries {
		if e.Word == "你好" {
			found = true
		}
	}
	if !found {
		t.Fatalf("QueryPrefix(ni, h) 应包含「你好」，得到 %+v", entries)
	}
	// 残缺片段本身就是音节前缀但词典无下挂：空结果
	if entries := d.QueryPrefix([]string{"shi"}, "zz"); len(entries) != 0 {
		t.Fatalf("QueryPrefix(shi, zz) 应为空，得到 %+v", entries)
	}
}
