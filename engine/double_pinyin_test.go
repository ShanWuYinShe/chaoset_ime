package engine

import "testing"

// 已知键位抽查（与 Rime 自然码方案语义一致）：
// ni=ni、hao=hk、shi=ui、zhang=vh、zhuang=vd、liang=ld、kuai=ky、
// juan=jr、lve=lt、jue=jt、zhong=vs、jiong=js、hua=hw、bei=bz、
// niu=nq、dun=dp、a=aa、ai=al、ang=ah、en=ef、eng=eg、ou=ob、o=oo。
func TestDoublePinyinKnownPairs(t *testing.T) {
	cases := []struct {
		syl, a, b string
	}{
		{"ni", "n", "i"}, {"hao", "h", "k"}, {"shi", "u", "i"},
		{"zhang", "v", "h"}, {"zhuang", "v", "d"}, {"liang", "l", "d"},
		{"kuai", "k", "y"}, {"juan", "j", "r"}, {"lve", "l", "t"},
		{"jue", "j", "t"}, {"zhong", "v", "s"}, {"jiong", "j", "s"},
		{"hua", "h", "w"}, {"bei", "b", "z"}, {"niu", "n", "q"},
		{"dun", "d", "p"}, {"a", "a", "a"}, {"ai", "a", "l"},
		{"ang", "a", "h"}, {"en", "e", "f"}, {"eng", "e", "g"},
		{"ou", "o", "b"}, {"o", "o", "o"},
	}
	for _, tc := range cases {
		syl, ok := DoublePinyinDecode(tc.a[0], tc.b[0])
		if !ok || syl != tc.syl {
			t.Errorf("键码 %s%s 应解码为 %s, 得到 %q (ok=%v)", tc.a, tc.b, tc.syl, syl, ok)
		}
	}
}

// 反向表完整性：每个合法音节至少有一个两键码；键码冲突时「规则派生
// 优先于透传」可消解（如 luo 派生键码 lo 优先于拟声字 lo），仅当双方
// 同为派生或同为透传时才是不可消解的冲突（说明转录走样）。
func TestDoublePinyinTableIntegrity(t *testing.T) {
	type entry struct {
		syl     string
		derived bool
	}
	reverse := map[string]entry{}
	for _, syl := range syllableTable {
		codes := dpApply(syl)
		hit := false
		for _, code := range codes {
			if len(code) != 2 {
				continue // 零声母派生长码不收录
			}
			hit = true
			transformed := code != syl
			prev, dup := reverse[code]
			if !dup {
				reverse[code] = entry{syl: syl, derived: transformed}
				continue
			}
			if prev.syl == syl {
				continue
			}
			if transformed && !prev.derived {
				reverse[code] = entry{syl: syl, derived: true}
			} else if !transformed && prev.derived {
				// 保留派生方
			} else {
				t.Errorf("键码 %q 不可消解冲突: %s 与 %s", code, prev.syl, syl)
			}
		}
		if !hit {
			t.Errorf("音节 %s 无两键编码", syl)
		}
	}
	// 冲突消解抽查：lo 键码应归 luo（uo→O 派生）而非拟声字 lo
	if got := reverse["lo"].syl; got != "luo" {
		t.Errorf("lo 键码应消解给 luo, 实际 %q", got)
	}
	// 表覆盖率下限（自然码应覆盖绝大多数音节）
	if len(reverse) < len(syllableTable)*9/10 {
		t.Fatalf("反向表覆盖 %d 键码, 音节 %d 个, 覆盖率过低", len(reverse), len(syllableTable))
	}
}

// 会话级流式集成：n→i→h→k 逐键输入，解码出 ni/hao 并命中「你好」；
// 退格按「按键事件」粒度回滚。
func TestDoublePinyinSession(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.SetDoublePinyin(true)

	comp, _ := s.Input("n")
	if len(comp.Candidates) != 0 || comp.Input != "" {
		t.Fatalf("首键暂存应为空组成: %+v", comp)
	}
	comp, _ = s.Input("i")
	if comp.Input != "ni" {
		t.Fatalf("成对后 Input = %q, 期望 ni", comp.Input)
	}
	comp, _ = s.Input("h")
	comp, _ = s.Input("k")
	if comp.Input != "nihao" || !hasCand(comp, "你好") {
		t.Fatalf("nihao 应命中 你好: %+v", comp)
	}

	// 退格：整段回滚 hao → 只剩 ni
	comp, _ = s.Backspace()
	if comp.Input != "ni" {
		t.Fatalf("退格后 Input = %q, 期望 ni", comp.Input)
	}
	// 暂存首键的退格：清除暂存
	s.Input("h")
	comp, _ = s.Backspace()
	if comp.Input != "ni" {
		t.Fatalf("暂存退格后 Input = %q, 期望 ni", comp.Input)
	}
}

// 无效键码对整体丢弃。测试运行期探测一个未分配键码（qq 实为 qiu 的
// 合法键码，不能硬编码）。
func TestDoublePinyinInvalidPair(t *testing.T) {
	var a, b byte
	found := false
	for x := byte('a'); x <= 'z' && !found; x++ {
		for y := byte('a'); y <= 'z' && !found; y++ {
			if _, ok := DoublePinyinDecode(x, y); !ok {
				a, b, found = x, y, true
			}
		}
	}
	if !found {
		t.Skip("676 键码全部已分配，无从构造无效对")
	}
	s := NewSession(mustDict(t), nil)
	s.SetDoublePinyin(true)
	s.Input(string(a))
	comp, _ := s.Input(string(b))
	if comp.Input != "" {
		t.Fatalf("无效键码对 %c%c 应丢弃, Input = %q", a, b, comp.Input)
	}
}
