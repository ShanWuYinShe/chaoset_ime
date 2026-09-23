package engine

import (
	"strings"
)

// 自然码双拼：每个音节固定两键（声母键 + 韵母键）。
//
// 键位表**不手工编写**——忠实转写 Rime 官方自然码方案
// (rime-double-pinyin/double_pinyin.schema.yaml) 的 speller.algebra
// 规则管线，对全部合法音节正向生成键码后建反向表。转录是否走样，
// 由完整性测试（全表无冲突、逐音节可解码、已知键位抽查）兜底。

// 规则在「拼写集合」上顺序作用：xform 改写全部匹配拼写，derive
// 保留原拼写并追加派生拼写（与 Rime algebra 语义一致）。

// isOneOf 报告 c 是否出现在 chars 中。
func isOneOf(c byte, chars string) bool {
	return strings.IndexByte(chars, c) >= 0
}

// dpApply 规则管线：输入标准音节，输出全部合法键码（含 derive 别名，
// 均为小写；通常 1-2 个，个别零声母音节 3 个）。
func dpApply(syl string) []string {
	set := map[string]struct{}{syl: {}}

	derive := func(fn func(string) (string, bool)) {
		for sp := range set {
			if out, ok := fn(sp); ok {
				set[out] = struct{}{}
			}
		}
	}
	xform := func(fn func(string) (string, bool)) {
		next := make(map[string]struct{}, len(set))
		for sp := range set {
			if out, ok := fn(sp); ok {
				next[out] = struct{}{}
			} else {
				next[sp] = struct{}{}
			}
		}
		set = next
	}

	// erase /^xx$/：音节表中不存在 xx，天然无操作。

	// derive /^([jqxy])u$/$1v/（ju→jv、qu→qv、xu→xv、yu→yv）
	derive(func(sp string) (string, bool) {
		if len(sp) == 2 && isOneOf(sp[0], "jqxy") && sp[1] == 'u' {
			return string(sp[0]) + "v", true
		}
		return "", false
	})

	// derive /^([aoe])([ioun])$/$1$1$2/（ai→aai、an→aan、ou→oou、en→een 等）
	derive(func(sp string) (string, bool) {
		if len(sp) == 2 && isOneOf(sp[0], "aoe") && isOneOf(sp[1], "ioun") {
			return sp[0:1] + sp[0:1] + sp[1:2], true
		}
		return "", false
	})

	// xform /^([aoe])(ng)?$/$1$1$2/（a→aa、e→ee、o→oo、ang→aang、eng→eeng）
	xform(func(sp string) (string, bool) {
		if len(sp) >= 1 && len(sp) <= 3 && isOneOf(sp[0], "aoe") {
			rest := sp[1:]
			if rest == "" || rest == "ng" {
				return sp[0:1] + sp[0:1] + rest, true
			}
		}
		return "", false
	})

	// 韵母/声母键位替换，顺序与 schema 一致。
	rules := []struct {
		suffix string // 后缀触发
		prefix string // 前缀触发（仅 sh/ch/zh 声母）
		key    byte   // 替换成的键位字母（大写）
	}{
		{suffix: "iu", key: 'Q'},
		{suffix: "ia", key: 'W'}, // 自然码 W 覆盖 ia/ua；Rime schema 缺 ia，按方案事实补齐
		{suffix: "ua", key: 'W'},
		{suffix: "va", key: 'W'},
		{suffix: "uan", key: 'R'},
		{suffix: "van", key: 'R'},
		{suffix: "ue", key: 'T'},
		{suffix: "ve", key: 'T'},
		{suffix: "ing", key: 'Y'},
		{suffix: "uai", key: 'Y'},
		{prefix: "sh", key: 'U'},
		{prefix: "ch", key: 'I'},
		{prefix: "zh", key: 'V'},
		{suffix: "uo", key: 'O'},
		{suffix: "un", key: 'P'},
		{suffix: "vn", key: 'P'},
		{suffix: "iong", key: 'S'}, // iong 必须先于 ong，否则 jiong 被 ong 抢先吞成三键
		{suffix: "ong", key: 'S'},
		{suffix: "iang", key: 'D'},
		{suffix: "uang", key: 'D'},
		{suffix: "en", key: 'F'},
		{suffix: "eng", key: 'G'},
		{suffix: "ang", key: 'H'},
		{suffix: "ian", key: 'M'},
		{suffix: "an", key: 'J'},
		{suffix: "iao", key: 'C'},
		{suffix: "ao", key: 'K'},
		{suffix: "ai", key: 'L'},
		{suffix: "ei", key: 'Z'},
		{suffix: "ie", key: 'X'},
		{suffix: "ui", key: 'V'},
		{suffix: "ou", key: 'B'},
		{suffix: "in", key: 'N'},
	}
	for _, rule := range rules {
		if rule.prefix != "" {
			pre := rule.prefix
			xform(func(sp string) (string, bool) {
				if strings.HasPrefix(sp, pre) {
					return string(rule.key) + sp[len(pre):], true
				}
				return "", false
			})
			continue
		}
		suf := rule.suffix
		xform(func(sp string) (string, bool) {
			if strings.HasSuffix(sp, suf) {
				return sp[:len(sp)-len(suf)] + string(rule.key), true
			}
			return "", false
		})
	}

	// xlit /QWRTYUIOPSDFGHMJCKLZXVBN/qwrtyuiopsdfghmjcklzxvbn/：
	// 大写键位字母转小写；其余（a/e/o 等原字母）不变。
	codes := make([]string, 0, len(set))
	for sp := range set {
		b := []byte(sp)
		for i, c := range b {
			if strings.IndexByte("QWRTYUIOPSDFGHMJCKLZXVBN", c) >= 0 {
				b[i] = c + ('a' - 'A')
			}
		}
		codes = append(codes, string(b))
	}
	return codes
}

// dpCodes 是双拼反向表：两键码 → 音节。init 时对全部合法音节正向
// 生成键码填充；同一音节的多个键码（derive 别名）都指向它。
// 键码冲突时「规则派生」优先于「原样透传」：如 luo 经 uo→O 派生出
// 的键码 lo，优先于拟声字 lo 的透传键码。
var dpCodes = func() map[string]string {
	type entry struct {
		syl     string
		derived bool
	}
	m := make(map[string]entry, len(syllableTable)*2)
	for _, syl := range syllableTable {
		for _, code := range dpApply(syl) {
			if len(code) != 2 {
				continue // 零声母派生码可能超两键，不收录
			}
			prev, dup := m[code]
			if !dup {
				m[code] = entry{syl: syl, derived: code != syl}
				continue
			}
			if prev.syl == syl {
				continue
			}
			if code != syl && !prev.derived {
				m[code] = entry{syl: syl, derived: true}
			}
			// 双方同为透传的冲突由完整性测试暴露，不在构建期悄悄裁决。
		}
	}
	out := make(map[string]string, len(m))
	for code, e := range m {
		out[code] = e.syl
	}
	return out
}()

// DoublePinyinDecode 解码一对双拼键码为音节。第二返回值 false 表示
// 该键码无对应音节（调用方应丢弃这对按键）。
func DoublePinyinDecode(a, b byte) (string, bool) {
	syl, ok := dpCodes[string(a)+string(b)]
	return syl, ok
}
