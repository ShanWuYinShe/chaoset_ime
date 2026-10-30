package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// 已知键位抽查——自然码（与 Rime 自然码方案语义一致）：
// ni=ni、hao=hk、shi=ui、zhang=vh、zhuang=vd、liang=ld、kuai=ky、
// juan=jr、lve=lt、jue=jt、zhong=vs、jiong=js、hua=hw、bei=bz、
// niu=nq、dun=dp、a=aa、ai=al、ang=ah、en=ef、eng=eg、ou=ob、o=oo。
func TestDoublePinyinKnownPairsZRM(t *testing.T) {
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
		syl, ok := DoublePinyinDecode(DPSchemeZRM, tc.a[0], tc.b[0])
		if !ok || syl != tc.syl {
			t.Errorf("zrm 键码 %s%s 应解码为 %s, 得到 %q (ok=%v)", tc.a, tc.b, tc.syl, syl, ok)
		}
	}
}

// 已知键位抽查——微软双拼（与 Rime mspy 方案语义一致）：
// hao=hk、shi=ui、zhuang=vd、lv=ly、xue=xt/xv、lin=ln、ling=l;、
// er=r（单键）、an=oj/aj、ai=ol/al、ou=ob、kuai=ky、juan=jr、
// jiong=js、zhong=vs。
func TestDoublePinyinKnownPairsMSPY(t *testing.T) {
	cases := []struct {
		syl, a, b string
	}{
		{"ni", "n", "i"}, {"hao", "h", "k"}, {"shi", "u", "i"},
		{"zhuang", "v", "d"}, {"lv", "l", "y"}, {"xue", "x", "t"},
		{"lin", "l", "n"}, {"er", "e", "r"}, {"an", "o", "j"},
		{"an", "a", "j"}, {"ai", "o", "l"}, {"ai", "a", "l"},
		{"ou", "o", "b"}, {"kuai", "k", "y"}, {"juan", "j", "r"},
		{"jiong", "j", "s"}, {"zhong", "v", "s"}, {"ling", "l", ";"},
	}
	for _, tc := range cases {
		syl, ok := DoublePinyinDecode(DPSchemeMSPY, tc.a[0], tc.b[0])
		if !ok || syl != tc.syl {
			t.Errorf("mspy 键码 %s%s 应解码为 %s, 得到 %q (ok=%v)", tc.a, tc.b, tc.syl, syl, ok)
		}
	}
}

// 反向表完整性（逐方案）：每个合法音节至少有一个两键码；键码冲突时
// 「规则派生优先于透传」可消解（如自然码 luo 派生键码 lo 优先于拟声
// 字 lo），仅当双方同为派生或同为透传时才是不可消解的冲突。
func TestDoublePinyinTableIntegrity(t *testing.T) {
	for scheme := range dpSchemes {
		t.Run(scheme, func(t *testing.T) {
			type entry struct {
				syl     string
				derived bool
			}
			reverse := map[string]entry{}
			for _, syl := range syllableTable {
				codes := dpApply(scheme, syl)
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
			if scheme == DPSchemeZRM {
				if got := reverse["lo"].syl; got != "luo" {
					t.Errorf("zrm lo 键码应消解给 luo, 实际 %q", got)
				}
			}
			if len(reverse) < len(syllableTable)*9/10 {
				t.Fatalf("反向表覆盖 %d 键码, 音节 %d 个, 覆盖率过低", len(reverse), len(syllableTable))
			}
		})
	}
}

// 会话级流式集成：n→i→h→k 逐键输入，解码出 ni/hao 并命中「你好」；
// 退格按「按键事件」粒度回滚。
func TestDoublePinyinSession(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.SetDoublePinyin(DPSchemeZRM)

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

// 已知键位抽查——小鹤（与 Rime double_pinyin_flypy 方案语义一致）：
// hao=hc、shi=ui、zhang=vh、zhuang=vl、xian=xm、kuai=kk、jiong=js、
// hua=hx、bei=bw、dui=dv、xun=xy、ying=yk、an=aj、ang=ah、ai=ad、
// ei=ew、ou=oz、en=ef、eng=eg、guan=gr、jia=jx、o=oo、a=aa。
func TestDoublePinyinKnownPairsFLYPY(t *testing.T) {
	cases := []struct {
		syl, a, b string
	}{
		{"ni", "n", "i"}, {"hao", "h", "c"}, {"shi", "u", "i"},
		{"zhang", "v", "h"}, {"zhuang", "v", "l"}, {"xian", "x", "m"},
		{"kuai", "k", "k"}, {"jiong", "j", "s"}, {"hua", "h", "x"},
		{"bei", "b", "w"}, {"dui", "d", "v"}, {"xun", "x", "y"},
		{"ying", "y", "k"}, {"er", "e", "r"}, {"an", "a", "j"},
		{"ang", "a", "h"}, {"ai", "a", "d"}, {"ei", "e", "w"},
		{"ou", "o", "z"}, {"o", "o", "o"}, {"a", "a", "a"},
		{"en", "e", "f"}, {"eng", "e", "g"}, {"guan", "g", "r"},
		{"jia", "j", "x"},
	}
	for _, tc := range cases {
		syl, ok := DoublePinyinDecode(DPSchemeFLYPY, tc.a[0], tc.b[0])
		if !ok || syl != tc.syl {
			t.Errorf("flypy 键码 %s%s 应解码为 %s, 得到 %s (ok=%v)", tc.a, tc.b, tc.syl, syl, ok)
		}
	}
}

// 未知方案按关闭处理；关闭后按键走全拼。
func TestDoublePinyinSchemeValidation(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.SetDoublePinyin("xiaohe") // 未知方案名（小鹤的合法名是 flypy）按关闭处理
	comp, _ := s.Input("n")
	if comp.Input != "n" {
		t.Fatalf("未知方案应按关闭处理, Input = %q", comp.Input)
	}
	s.SetDoublePinyin("")
	if comp := func() Composition { c, _ := s.Input("i"); return c }(); comp.Input != "ni" {
		t.Fatalf("关闭后应走全拼, Input = %q", comp.Input)
	}
}

// 整行输入（CLI/IPC 的一次 Input 传入多键）：应逐对解码全部键码，
// 而非只暂存首键丢弃剩余文本。
func TestDoublePinyinWholeLineInput(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.SetDoublePinyin(DPSchemeZRM)
	comp, _ := s.Input("nihk") // 自然码：ni + hk(hao)
	if comp.Input != "nihao" {
		t.Fatalf("zrm 整行 nihk 应解码为 nihao, Input = %q", comp.Input)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("zrm nihk 应出候选 你好, 得到 %+v", comp.Candidates)
	}
	f := NewSession(mustDict(t), nil)
	f.SetDoublePinyin(DPSchemeFLYPY)
	comp, _ = f.Input("nihc") // 小鹤：ni + hc(hao)，ao 在 c 键
	if comp.Input != "nihao" {
		t.Fatalf("flypy 整行 nihc 应解码为 nihao, Input = %q", comp.Input)
	}
	if len(comp.Candidates) == 0 || comp.Candidates[0].Text != "你好" {
		t.Fatalf("flypy nihc 应出候选 你好, 得到 %+v", comp.Candidates)
	}
}

// 无效键码对整体丢弃。测试运行期探测一个未分配键码（qq 实为 qiu 的
// 合法键码，不能硬编码）。
func TestDoublePinyinInvalidPair(t *testing.T) {
	var a, b byte
	found := false
	for x := byte('a'); x <= 'z' && !found; x++ {
		for y := byte('a'); y <= 'z' && !found; y++ {
			if _, ok := DoublePinyinDecode(DPSchemeZRM, x, y); !ok {
				a, b, found = x, y, true
			}
		}
	}
	if !found {
		t.Skip("676 键码全部已分配，无从构造无效对")
	}
	s := NewSession(mustDict(t), nil)
	s.SetDoublePinyin(DPSchemeZRM)
	s.Input(string(a))
	comp, _ := s.Input(string(b))
	if comp.Input != "" {
		t.Fatalf("无效键码对 %c%c 应丢弃, Input = %q", a, b, comp.Input)
	}
}

// 超长输入触发截断（maxInputRunes）后，退格栈（dpLens）必须与输入
// 长度保持对齐：连续退格应能一路清空且不 panic。
func TestDoublePinyinTruncateBackspaceAlignment(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.SetDoublePinyin(DPSchemeZRM)
	// 40 对按键 → 80 字符（超过 maxInputRunes=64）
	keys := ""
	for i := 0; i < 40; i++ {
		keys += "ni"
	}
	for _, r := range keys {
		s.Input(string(r))
	}
	comp, _ := s.Input("")
	runes := []rune(comp.Input)
	if len(runes) > maxInputRunes {
		t.Fatalf("输入应被截断到 %d 字符内, 实际 %d", maxInputRunes, len(runes))
	}
	// 连续退格直至清空：每次回滚一个按键事件（2 字符）
	for guard := 0; guard < maxInputRunes+8; guard++ {
		comp, _ = s.Backspace()
		if comp.Input == "" {
			break
		}
	}
	comp, _ = s.Input("")
	if comp.Input != "" {
		t.Fatalf("连续退格后应清空, Input = %q", comp.Input)
	}
}

// DoublePinyinSchemes 应列出全部已实现方案。
func TestDoublePinyinSchemesList(t *testing.T) {
	schemes := DoublePinyinSchemes()
	if len(schemes) != 3 {
		t.Fatalf("方案数 = %d, 期望 3", len(schemes))
	}
	have := map[string]bool{schemes[0]: true, schemes[1]: true, schemes[2]: true}
	if !have[DPSchemeZRM] || !have[DPSchemeMSPY] || !have[DPSchemeFLYPY] {
		t.Fatalf("方案列表缺项: %v", schemes)
	}
}

// TestDoublePinyinSessionReachability 是「反向表完整 → 会话可达」的升级
// 回归（2026-10-30 审计 P1-1）：旧测试只验证 DoublePinyinDecode 解码层，
// 而 Session.Input 过滤了 ';'，导致 mspy 的 ing 类音节（明/听/零/星/京）
// 整类打不出且按 ';' 会误上屏中文「；」。这里对每个方案反向表的全部键码，
// 逐键喂 Session.Input，断言会话 Input 与 Compose().Input 都等于对应音节，
// 且键码对解码不产生 Committed。
func TestDoublePinyinSessionReachability(t *testing.T) {
	d := mustSeqDict(t)
	for _, scheme := range DoublePinyinSchemes() {
		t.Run(scheme, func(t *testing.T) {
			s := NewSession(d, nil)
			s.SetDoublePinyin(scheme)
			for code, syl := range dpCodes[scheme] {
				if len(code) != 2 {
					t.Fatalf("%s 反向表出现非两键码 %q", scheme, code)
				}
				if code[0] < 'a' || code[0] > 'z' {
					t.Fatalf("%s 键码 %q 首键不是字母", scheme, code)
				}
				s.Reset()
				comp, err := s.Input(string(code[0]))
				if err != nil {
					t.Fatalf("%s 键码 %q 首键输入失败: %v", scheme, code, err)
				}
				if comp.Input != "" {
					t.Fatalf("%s 键码 %q 首键应暂存, Input = %q", scheme, code, comp.Input)
				}
				comp, err = s.Input(string(code[1]))
				if err != nil {
					t.Fatalf("%s 键码 %q 第二键输入失败: %v", scheme, code, err)
				}
				if comp.Input != syl {
					t.Fatalf("%s 键码 %q 应解码为 %q, 会话 Input = %q", scheme, code, syl, comp.Input)
				}
				if comp.Committed != "" {
					t.Fatalf("%s 键码 %q 是解码键，不应触发 Committed: %q", scheme, code, comp.Committed)
				}
				if got := s.Compose().Input; got != syl {
					t.Fatalf("%s 键码 %q Compose().Input = %q, 期望 %q", scheme, code, got, syl)
				}
			}
		})
	}
}

// TestDoublePinyinSessionMSPYIngKeys 锁定 mspy 的 ing 韵母键（';'）在会话级
// 可用：ming/ling/ting/xing/jing/ying 都要能经 Input 解码（审计 P1-1 的
// 直接复现场景）。
func TestDoublePinyinSessionMSPYIngKeys(t *testing.T) {
	s := NewSession(mustSeqDict(t), nil)
	s.SetDoublePinyin(DPSchemeMSPY)
	cases := []struct{ keys, syl string }{
		{"m;", "ming"}, {"l;", "ling"}, {"t;", "ting"},
		{"x;", "xing"}, {"j;", "jing"}, {"y;", "ying"},
	}
	for _, c := range cases {
		s.Reset()
		if _, err := s.Input(string(c.keys[0])); err != nil {
			t.Fatalf("%s 首键输入失败: %v", c.keys, err)
		}
		comp, err := s.Input(";")
		if err != nil {
			t.Fatalf("%s 输入 ';' 失败: %v", c.keys, err)
		}
		if comp.Input != c.syl {
			t.Fatalf("mspy 键码 %s 应解码为 %s, 实际 %q", c.keys, c.syl, comp.Input)
		}
		if comp.Committed != "" {
			t.Fatalf("mspy 键码 %s 解码不应产生 Committed: %q", c.keys, comp.Committed)
		}
	}
}

// TestDoublePinyinSessionSemicolonPunct 锁定「首键为空时 ';' 退回标点」的
// 语义：上屏文本（组词首选 + 「；」）经 Composition.Committed 交给壳写文档；
// 壳若仍按 punct 请求转发，组词中的首选词会被丢掉。空组词、半角模式与
// 全拼模式的既有行为一并锁定。
func TestDoublePinyinSessionSemicolonPunct(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	s.SetDoublePinyin(DPSchemeMSPY)
	if _, err := s.Input("ni"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	comp, err := s.Input(";")
	if err != nil {
		t.Fatalf("双拼 ';' 输入失败: %v", err)
	}
	if comp.Committed != "你好；" {
		t.Fatalf("组词中 ';' 应上屏首选词+「；」, Committed = %q", comp.Committed)
	}
	if comp.Input != "" || len(comp.Candidates) != 0 {
		t.Fatalf("';' 上屏后组词应为空: %+v", comp)
	}
	comp, _ = s.Input(";") // 空组词：直接输出「；」
	if comp.Committed != "；" {
		t.Fatalf("空组词 ';' 应上屏「；」, Committed = %q", comp.Committed)
	}
	s.SetHalfWidth(true) // 半角模式：原样输出 ASCII ';'
	comp, _ = s.Input(";")
	if comp.Committed != ";" {
		t.Fatalf("半角模式 ';' 应上屏 ASCII ';', Committed = %q", comp.Committed)
	}
	// 全拼模式行为不变：';' 仍被过滤，不经 Committed
	f := NewSession(mustDict(t), nil)
	comp, _ = f.Input("ni;")
	if comp.Input != "ni" || comp.Committed != "" {
		t.Fatalf("全拼模式 ';' 应被过滤: Input=%q Committed=%q", comp.Input, comp.Committed)
	}
}

// TestCompositionShuangpinField 锁定 Composition.Shuangpin：双拼模式下每次
// 响应（空组词路径、缓存写入路径、缓存命中路径）都带方案名，全拼模式经
// omitempty 省略；切换方案后记忆化缓存必须失效，否则壳会继续按旧方案路由
// ';' 键。
func TestCompositionShuangpinField(t *testing.T) {
	s := NewSession(mustDict(t), nil)
	if comp := s.Compose(); comp.Shuangpin != "" {
		t.Fatalf("全拼模式 Shuangpin 应为空, 实际 %q", comp.Shuangpin)
	}
	s.SetDoublePinyin(DPSchemeMSPY)
	// 空组词路径（compose 提前返回，不写缓存）
	if comp := s.Compose(); comp.Shuangpin != DPSchemeMSPY {
		t.Fatalf("空组词路径 Shuangpin = %q, 期望 mspy", comp.Shuangpin)
	}
	if _, err := s.Input("n"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	comp, err := s.Input("i")
	if err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if comp.Shuangpin != DPSchemeMSPY {
		t.Fatalf("缓存写入路径 Shuangpin = %q, 期望 mspy", comp.Shuangpin)
	}
	if cached := s.Compose(); cached.Shuangpin != DPSchemeMSPY {
		t.Fatalf("缓存命中路径 Shuangpin = %q, 期望 mspy", cached.Shuangpin)
	}
	b, err := json.Marshal(comp)
	if err != nil {
		t.Fatalf("json.Marshal 失败: %v", err)
	}
	if !strings.Contains(string(b), "\"shuangpin\":\"mspy\"") {
		t.Fatalf("双拼模式 JSON 应含 shuangpin=mspy: %s", b)
	}
	// 方案切换后缓存失效：Shuangpin 立即反映新状态
	s.SetDoublePinyin("")
	if comp := s.Compose(); comp.Shuangpin != "" {
		t.Fatalf("切回全拼后 Shuangpin = %q, 期望空", comp.Shuangpin)
	}
	// 全拼模式 JSON：omitempty 省略字段
	f := NewSession(mustDict(t), nil)
	fb, err := json.Marshal(f.Compose())
	if err != nil {
		t.Fatalf("json.Marshal 失败: %v", err)
	}
	if strings.Contains(string(fb), "shuangpin") {
		t.Fatalf("全拼模式 JSON 不应含 shuangpin: %s", fb)
	}
}
