package engine

import (
	"strings"
	"testing"
)

// 词级消歧：OpenCC 词组表解决一对多字的上下文判定。
func TestToTraditionalPhraseDisambiguation(t *testing.T) {
	cases := map[string]string{
		"头发": "頭髮", // 词组表：发 在「头发」中为 髮
		"出发": "出發", // 词组表：发 在「出发」中為 發
		"公里": "公里", // 词组表：里 在「公里」中保留原形
		"台面": "檯面", // 词组表：台 在「台面」中为 檯
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

// ToSimplified 必须是 ToTraditional 的可靠反向：繁体词条识别依赖它，
// 映射错会误删词典简体词条（2026-10-28 dictgen 去繁体专项）。
func TestToSimplifiedRoundTrip(t *testing.T) {
	cases := []struct{ in, want string }{
		{"時", "时"}, {"師", "师"}, {"實驗室", "实验室"}, {"歷史", "历史"},
		{"適", "适"}, {"歷", "历"},
		// 简体/ASCII 原样
		{"时", "时"}, {"历史", "历史"}, {"hello 你好", "hello 你好"},
	}
	for _, c := range cases {
		if got := ToSimplified(c.in); got != c.want {
			t.Errorf("ToSimplified(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
