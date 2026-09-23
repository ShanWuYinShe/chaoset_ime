package engine

import (
	"sort"
	"strings"
)

// 双拼方案：自然码（zrm）与微软双拼（mspy）。
//
// 键位表**不手工编写**——分别忠实转写 Rime 官方
// rime-double-pinyin 仓库两个 schema（double_pinyin.schema.yaml 与
// double_pinyin_mspy.schema.yaml）的 speller.algebra 规则管线，
// 对全部合法音节正向生成两键码后建反向表。转录是否走样，由完整性
// 测试（全表无冲突、逐音节可解码、已知键位抽查）兜底。
//
// 已知差异并如实标注：Rime 的 double_pinyin_flypy（小鹤）与官方
// 小鹤键位存在出入（如 ia/ua 键位），官方键位图暂无法以文本核验，
// 故暂不提供小鹤方案。
const (
	DPSchemeZRM  = "zrm"  // 自然码双拼
	DPSchemeMSPY = "mspy" // 微软双拼
)

// dpOp 是单条后缀替换规则：命中 suffix 时把该后缀替换为 key。
// keepHead 对应 Rime 的 (.)X$ 写法——额外保留后缀前的第一个字符
// （an→aJ 而非 J），这正是「声母键 + 韵母键」两键结构的来源。
type dpOp struct {
	suffix   string
	key      byte
	keepHead bool
	prefix   string // 非空时按前缀触发（zh/ch/sh 声母键），keepHead 不生效
}

// dpDerive 是 derive 规则：保留原拼写，追加一个匹配派生拼写。
type dpDerive = func(sp string) (string, bool)

// dpXform 是 xform 规则：命中时原位改写拼写。
type dpXform = func(sp string) (string, bool)

// dpSchemeDef 是一个双拼方案的完整规则集。
type dpSchemeDef struct {
	derives []dpDerive // 前置派生（保留原拼写并追加）
	xforms  []dpXform  // 前置原位改写（方案特有）
	ops     []dpOp     // 后缀/前缀键位替换
}

// isOneOf 报告 c 是否出现在 chars 中。
func isOneOf(c byte, chars string) bool {
	return strings.IndexByte(chars, c) >= 0
}

// 自然码前置 xform：零声母双写（a→aa、e→ee、o→oo、ang→aang、
// eng→eeng），对应 schema 的 xform /^([aoe])(ng)?$/$1$1$2/。
func dpXformZrmDouble(sp string) (string, bool) {
	if len(sp) >= 1 && len(sp) <= 3 && isOneOf(sp[0], "aoe") {
		rest := sp[1:]
		if rest == "" || rest == "ng" {
			return sp[0:1] + sp[0:1] + rest, true
		}
	}
	return "", false
}

// 微软前置 xform：a/e 开头双写首字母（an→aan），对应 schema 的
// xform /^([ae])(.*)$/$1$1$2/。
func dpXformMspyDouble(sp string) (string, bool) {
	if len(sp) >= 2 && isOneOf(sp[0], "ae") {
		return sp[0:1] + sp[0:1] + sp[1:], true
	}
	return "", false
}

var dpSchemes = map[string]dpSchemeDef{
	// 自然码：double_pinyin.schema.yaml 的 algebra。
	DPSchemeZRM: {
		derives: []dpDerive{
			// derive /^([jqxy])u$/$1v/（ju→jv、qu→qv、xu→xv、yu→yv）
			func(sp string) (string, bool) {
				if len(sp) == 2 && isOneOf(sp[0], "jqxy") && sp[1] == 'u' {
					return string(sp[0]) + "v", true
				}
				return "", false
			},
			// derive /^([aoe])([ioun])$/$1$1$2/（ai→aai、an→aan、ou→oou）
			func(sp string) (string, bool) {
				if len(sp) == 2 && isOneOf(sp[0], "aoe") && isOneOf(sp[1], "ioun") {
					return sp[0:1] + sp[0:1] + sp[1:2], true
				}
				return "", false
			},
		},
		xforms: []dpXform{dpXformZrmDouble},
		ops: []dpOp{
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
			{suffix: "iong", key: 'S'}, // iong 必须先于 ong，否则 jiong 被吞成三键
			{suffix: "ong", key: 'S'},
			{suffix: "iang", key: 'D'},
			{suffix: "uang", key: 'D'},
			{suffix: "en", key: 'F', keepHead: true},
			{suffix: "eng", key: 'G', keepHead: true},
			{suffix: "ang", key: 'H', keepHead: true},
			{suffix: "ian", key: 'M', keepHead: true},
			{suffix: "an", key: 'J', keepHead: true},
			{suffix: "iao", key: 'C'},
			{suffix: "ao", key: 'K', keepHead: true},
			{suffix: "ai", key: 'L', keepHead: true},
			{suffix: "ei", key: 'Z', keepHead: true},
			{suffix: "ie", key: 'X'},
			{suffix: "ui", key: 'V'},
			{suffix: "ou", key: 'B', keepHead: true},
			{suffix: "in", key: 'N'},
		},
	},
	// 微软双拼：double_pinyin_mspy.schema.yaml 的 algebra。
	// 特征：零声母加 O 前缀（oan=安）、ü/v 与 uai 同键 Y、ing 在 ; 键、
	// er 单键 R。
	DPSchemeMSPY: {
		derives: []dpDerive{
			// derive /^([jqxy])u$/$1v/
			func(sp string) (string, bool) {
				if len(sp) == 2 && isOneOf(sp[0], "jqxy") && sp[1] == 'u' {
					return string(sp[0]) + "v", true
				}
				return "", false
			},
			// derive /^([aoe].*)$/o$1/（零声母加 O 前缀：an→oan、e→oe）
			func(sp string) (string, bool) {
				if len(sp) >= 1 && isOneOf(sp[0], "aoe") {
					return "o" + sp, true
				}
				return "", false
			},
		},
		xforms: []dpXform{dpXformMspyDouble},
		ops: []dpOp{
			{suffix: "iu", key: 'Q'},
			{suffix: "ia", key: 'W'},
			{suffix: "ua", key: 'W'},
			{suffix: "er", key: 'R'},
			{suffix: "uan", key: 'R'},
			{suffix: "van", key: 'R'},
			{suffix: "ue", key: 'T'},
			{suffix: "ve", key: 'T'},
			{suffix: "v", key: 'Y'},
			{suffix: "uai", key: 'Y'},
			{prefix: "sh", key: 'U'},
			{prefix: "ch", key: 'I'},
			{prefix: "zh", key: 'V'},
			{suffix: "uo", key: 'O'},
			{suffix: "un", key: 'P'},
			{suffix: "vn", key: 'P'},
			{suffix: "iong", key: 'S'}, // iong 先于 ong（对应 i?ong$，jiong/qiong/xiong 用）
			{suffix: "ong", key: 'S'},
			{suffix: "iang", key: 'D'},
			{suffix: "uang", key: 'D'},
			{suffix: "en", key: 'F', keepHead: true},
			{suffix: "eng", key: 'G', keepHead: true},
			{suffix: "ang", key: 'H', keepHead: true},
			{suffix: "ian", key: 'M', keepHead: true},
			{suffix: "an", key: 'J', keepHead: true},
			{suffix: "iao", key: 'C'},
			{suffix: "ao", key: 'K', keepHead: true},
			{suffix: "ai", key: 'L', keepHead: true},
			{suffix: "ei", key: 'Z', keepHead: true},
			{suffix: "ie", key: 'X'},
			{suffix: "ui", key: 'V'},
			{suffix: "ou", key: 'B', keepHead: true},
			{suffix: "in", key: 'N'},
			{suffix: "ing", key: ';'},
		},
	},
}

// dpXLit 把大写键位字母转小写；其余（a/e/o 等原字母）不变。
// 对应 schema 的 xlit /QWRTYUIOPSDFGHMJCKLZXVBN/qwrtyuiopsdfghmjcklzxvbn/。
func dpXLit(sp string) string {
	b := []byte(sp)
	for i, c := range b {
		if strings.IndexByte("QWRTYUIOPSDFGHMJCKLZXVBN", c) >= 0 {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// dpApply 规则管线：输入标准音节与方案，输出全部合法键码（含 derive
// 别名，均为小写；个别零声母派生码可能超两键）。
func dpApply(scheme, syl string) []string {
	def, ok := dpSchemes[scheme]
	if !ok {
		return nil
	}
	set := map[string]struct{}{syl: {}}

	derive := func(fn dpDerive) {
		for sp := range set {
			if out, ok := fn(sp); ok {
				set[out] = struct{}{}
			}
		}
	}
	xform := func(fn dpXform) {
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

	for _, d := range def.derives {
		derive(d)
	}
	for _, x := range def.xforms {
		xform(x)
	}
	for _, op := range def.ops {
		if op.prefix != "" {
			pre := op.prefix
			xform(func(sp string) (string, bool) {
				if strings.HasPrefix(sp, pre) {
					return string(op.key) + sp[len(pre):], true
				}
				return "", false
			})
			continue
		}
		suf := op.suffix
		xform(func(sp string) (string, bool) {
			if !strings.HasSuffix(sp, suf) {
				return "", false
			}
			if op.keepHead {
				// (.)X$ 语义：保留后缀前的一个字符（an→aJ、Van→VJ）
				head := len(sp) - len(suf) - 1
				if head < 0 {
					return "", false
				}
				return sp[:head+1] + string(op.key), true
			}
			return sp[:len(sp)-len(suf)] + string(op.key), true
		})
	}

	codes := make([]string, 0, len(set))
	for sp := range set {
		codes = append(codes, dpXLit(sp))
	}
	return codes
}

// dpCodes 是每个方案的双拼反向表：两键码 → 音节。键码冲突时「规则
// 派生」优先于「原样透传」：如自然码 luo 经 uo→O 派生出键码 lo，
// 优先于拟声字 lo 的透传键码。
var dpCodes = func() map[string]map[string]string {
	type entry struct {
		syl     string
		derived bool
	}
	out := make(map[string]map[string]string, len(dpSchemes))
	for scheme := range dpSchemes {
		m := make(map[string]entry, len(syllableTable)*2)
		for _, syl := range syllableTable {
			for _, code := range dpApply(scheme, syl) {
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
				transformed := code != syl
				if transformed && !prev.derived {
					m[code] = entry{syl: syl, derived: true}
				}
				// 双方同为透传的冲突由完整性测试暴露。
			}
		}
		decoded := make(map[string]string, len(m))
		for code, e := range m {
			decoded[code] = e.syl
		}
		out[scheme] = decoded
	}
	return out
}()

// DoublePinyinSchemes 返回支持的双拼方案名（字典序）。
func DoublePinyinSchemes() []string {
	out := make([]string, 0, len(dpSchemes))
	for s := range dpSchemes {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// DoublePinyinDecode 解码指定方案的一对双拼键码为音节。第二返回值
// false 表示该键码无对应音节（调用方应丢弃这对按键）。
func DoublePinyinDecode(scheme string, a, b byte) (string, bool) {
	m, ok := dpCodes[scheme]
	if !ok {
		return "", false
	}
	syl, ok := m[string(a)+string(b)]
	return syl, ok
}
