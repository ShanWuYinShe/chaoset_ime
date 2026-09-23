package engine

import (
	"strings"
	"testing"
)

// 单字转换：一对多歧义字（发/干/面/里/后/台）保留原形。
func TestToTraditionalAmbiguousPreserved(t *testing.T) {
	cases := map[string]string{
		"头发": "頭发", // 发（髮/發）歧义保留；头→頭 无歧义转换
		"出发": "出发", // 出发无映射字出? 出 无映射、发 保留
		"公里": "公里", // 里 歧义保留
		"台面": "台面", // 台/面 均歧义保留
	}
	for in, want := range cases {
		if got := ToTraditional(in); got != want {
			t.Errorf("ToTraditional(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// 无歧义字与混合文本转换；ASCII/标点透传。
func TestToTraditionalBasics(t *testing.T) {
	cases := map[string]string{
		"中国":          "中國",
		"时间":          "時間",
		"学习":          "學習",
		"Motorola 手机": "Motorola 手機",
		"你好，世界。":      "你好，世界。", // 标点简繁同形
	}
	for in, want := range cases {
		if got := ToTraditional(in); got != want {
			t.Errorf("ToTraditional(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// 会话级开关：候选文本转繁体；关闭时不变。
func TestSessionTraditional(t *testing.T) {
	dictSrc := `
中国	zhong guo	99
时光	shi guang	80
`
	dict, err := ParseDict(strings.NewReader(dictSrc))
	if err != nil {
		t.Fatalf("解析词库失败: %v", err)
	}

	// 关闭：简体
	s := NewSession(dict, nil)
	comp, _ := s.Input("zhong")
	if !hasCand(comp, "中国") {
		t.Fatalf("关闭繁体应输出 中国, 得到 %v", comp.Candidates)
	}

	// 开启：候选与上屏均为繁体
	s2 := NewSession(dict, nil)
	s2.SetTraditional(true)
	comp, err = s2.Input("zhong")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if !hasCand(comp, "中國") {
		t.Fatalf("开启繁体应输出 中國, 得到 %v", comp.Candidates)
	}
	text, err := s2.Select(0)
	if err != nil {
		t.Fatalf("Select 失败: %v", err)
	}
	if text != "中國" {
		t.Fatalf("上屏 = %q, 期望 中國", text)
	}

	// 时光 → 時光（时/光 均无歧义）
	s3 := NewSession(dict, nil)
	s3.SetTraditional(true)
	comp, err = s3.Input("shiguang")
	if !hasCand(comp, "時光") {
		t.Fatalf("应输出 時光, 得到 %v", comp.Candidates)
	}
}
