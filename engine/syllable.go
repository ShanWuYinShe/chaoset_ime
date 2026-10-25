package engine

import (
	"sort"
	"strings"
)

// syllableTable 是无声调标准普通话音节表（ü 以 v 输入：nv=女、lve=略）。
var syllableTable = []string{
	// 零声母
	"a", "ai", "an", "ang", "ao", "e", "ei", "en", "eng", "er", "o", "ou",
	// b
	"ba", "bai", "ban", "bang", "bao", "bei", "ben", "beng", "bi", "bian", "biao", "bie", "bin", "bing", "bo", "bu",
	// p
	"pa", "pai", "pan", "pang", "pao", "pei", "pen", "peng", "pi", "pian", "piao", "pie", "pin", "ping", "po", "pou", "pu",
	// m
	"ma", "mai", "man", "mang", "mao", "me", "mei", "men", "meng", "mi", "mian", "miao", "mie", "min", "ming", "miu", "mo", "mou", "mu",
	// f
	"fa", "fan", "fang", "fei", "fen", "feng", "fo", "fou", "fu",
	// d
	"da", "dai", "dan", "dang", "dao", "de", "dei", "den", "deng", "di", "dia", "dian", "diao", "die", "ding", "diu", "dong", "dou", "du", "duan", "dui", "dun", "duo",
	// t
	"ta", "tai", "tan", "tang", "tao", "te", "teng", "ti", "tian", "tiao", "tie", "ting", "tong", "tou", "tu", "tuan", "tui", "tun", "tuo",
	// n
	"na", "nai", "nan", "nang", "nao", "ne", "nei", "nen", "neng", "ni", "nian", "niang", "niao", "nie", "nin", "ning", "niu", "nong", "nou", "nu", "nuan", "nuo", "nv", "nve",
	// l
	"la", "lai", "lan", "lang", "lao", "le", "lei", "leng", "li", "lia", "lian", "liang", "liao", "lie", "lin", "ling", "liu", "lo", "long", "lou", "lu", "luan", "lun", "luo", "lv", "lve",
	// z
	"za", "zai", "zan", "zang", "zao", "ze", "zei", "zen", "zeng", "zi", "zong", "zou", "zu", "zuan", "zui", "zun", "zuo",
	// c
	"ca", "cai", "can", "cang", "cao", "ce", "cen", "ceng", "ci", "cong", "cou", "cu", "cuan", "cui", "cun", "cuo",
	// s
	"sa", "sai", "san", "sang", "sao", "se", "sen", "seng", "si", "song", "sou", "su", "suan", "sui", "sun", "suo",
	// zh
	"zha", "zhai", "zhan", "zhang", "zhao", "zhe", "zhen", "zheng", "zhi", "zhong", "zhou", "zhu", "zhua", "zhuai", "zhuan", "zhuang", "zhui", "zhun", "zhuo",
	// ch
	"cha", "chai", "chan", "chang", "chao", "che", "chen", "cheng", "chi", "chong", "chou", "chu", "chua", "chuai", "chuan", "chuang", "chui", "chun", "chuo",
	// sh
	"sha", "shai", "shan", "shang", "shao", "she", "shei", "shen", "sheng", "shi", "shou", "shu", "shua", "shuai", "shuan", "shuang", "shui", "shun", "shuo",
	// r
	"ran", "rang", "rao", "re", "ren", "reng", "ri", "rong", "rou", "ru", "rua", "ruan", "rui", "run", "ruo",
	// j
	"ji", "jia", "jian", "jiang", "jiao", "jie", "jin", "jing", "jiong", "jiu", "ju", "juan", "jue", "jun",
	// q
	"qi", "qia", "qian", "qiang", "qiao", "qie", "qin", "qing", "qiong", "qiu", "qu", "quan", "que", "qun",
	// x
	"xi", "xia", "xian", "xiang", "xiao", "xie", "xin", "xing", "xiong", "xiu", "xu", "xuan", "xue", "xun",
	// g
	"ga", "gai", "gan", "gang", "gao", "ge", "gei", "gen", "geng", "gong", "gou", "gu", "gua", "guai", "guan", "guang", "gui", "gun", "guo",
	// k
	"ka", "kai", "kan", "kang", "kao", "ke", "kei", "ken", "keng", "kong", "kou", "ku", "kua", "kuai", "kuan", "kuang", "kui", "kun", "kuo",
	// h
	"ha", "hai", "han", "hang", "hao", "he", "hei", "hen", "heng", "hong", "hou", "hu", "hua", "huai", "huan", "huang", "hui", "hun", "huo",
	// y
	"ya", "yan", "yang", "yao", "ye", "yi", "yin", "ying", "yo", "yong", "you", "yu", "yuan", "yue", "yun",
	// w
	"wa", "wai", "wan", "wang", "wei", "wen", "weng", "wo", "wu",
}

var (
	syllableSet    = make(map[string]struct{}, len(syllableTable))
	syllableSorted = append([]string(nil), syllableTable...)
)

func init() {
	for _, s := range syllableTable {
		syllableSet[s] = struct{}{}
	}
	sort.Strings(syllableSorted)
}

// IsSyllable 判断给定串是否为合法音节。
func IsSyllable(s string) bool {
	_, ok := syllableSet[s]
	return ok
}

// SyllablesWithPrefix 返回所有以 frag 为前缀的音节。
// 音节表规模小（约 420 个），线性扫描足够，无需索引。
func SyllablesWithPrefix(frag string) []string {
	var out []string
	for _, s := range syllableSorted {
		if strings.HasPrefix(s, frag) {
			out = append(out, s)
		}
	}
	return out
}

// syllableAliases 是「用户可能敲出的非标准拼写 → 标准音节」表。
//
// 只收录**输入侧**的等价写法，不改音节表本身：音节表统一用 v 记写 ü
// （nv=女、lve=略），这是词库与导入管线的既定格式；但用户键盘上敲 ü 的
// 习惯写法是 u（lue=略、nue=虐），且多数输入法都接受。缺了这张表，
// 敲 lue 只能打出「路鄂」（lu + e），敲 nue 只能打出「怒鄂」，观感是坏掉。
//
// 注意 jue/que/xue/yue 在标准拼音里本来就用 u（jue 不是 jüe 的另一种写法），
// 因此不能把它们映射成 jve/qve/xve/yve——那些不是合法音节。
var syllableAliases = map[string]string{
	"lue": "lve",
	"nue": "nve",
}

// canonicalSyllable 返回音节的标准写法：是合法音节则原样返回；
// 否则尝试别名表（lue → lve）；都不匹配时原样返回，由调用方判定非法。
func canonicalSyllable(s string) string {
	if IsSyllable(s) {
		return s
	}
	if canon, ok := syllableAliases[s]; ok {
		return canon
	}
	return s
}

// misspelledFromPatterns 判断输入是否「像打错了」——错拼修正的预筛。
// 调用方把已经算好的切分路径传进来（避免对同一输入重复切分，切分是
// Compose 里最贵的纯计算步骤之一）。
//
// 判据（刻意保守）：只要**任一条切分路径里出现完整音节**（>= 2 字母），
// 就信任这是正常输入——混拼与简拼都属于这种形态：
//
//	zhongg  → zhong + g?      完整音节 zhong，是合法混拼
//	yiwys   → yi + w? + y? + s?  完整音节 yi，是合法混拼
//	shangxj → shang + x? + j?   完整音节 shang
//
// 手误的特征相反：错拼把本该完整的音节打散了，整串只能靠单字母声母碎片
// 拼凑（zhogn → z?|h?|o|g?|n?，唯一的「完整音节」是单字母的 o）。
// 因此再加一条长度门槛：短串（<=4，如 zg / bj / jsj）一律信任。
//
// 已知漏判：nihap 能被切成 ni + ha + p?（有完整音节），所以不会被修正。
// 这是**刻意取舍**——它与合法写法 ni'ha'p 在串上完全同形，强行纠错会误伤
// 所有混拼输入（实测 mixed 首选率会从 34.32% 掉到 20.75%，且
// zhongg → 中国 这类既有用例直接失败）。
func misspelledFromPatterns(s string, patterns [][]Token) bool {
	if s == "" {
		return false
	}
	// 切分路径为空 = 整串无法切成任何音节——这正是最典型的错拼
	// （zhonguo / jisuanjii / niihao 都切不动），必须走纠错。
	if len(patterns) == 0 {
		return true
	}
	for _, pat := range patterns {
		for _, tok := range pat {
			if tok.IsExact && len(tok.Text) >= 2 {
				return false
			}
		}
	}
	return len(s) > 4
}

// mayBeMisspelled 是 misspelledFromPatterns 的自切分包装，
// 供 typoCorrections 在没有现成切分结果时使用。
func mayBeMisspelled(s string) bool {
	if s == "" {
		return false
	}
	return misspelledFromPatterns(s, tokenizeChunk(s))
}

// hasCleanSyllableSplit 判断整段能否切成**纯完整音节**序列（无简拼碎片）。
// 修正串的正确形态就是合法音节序列，用这个 DP 校验最合适。
func hasCleanSyllableSplit(chunk string) bool {
	ok := make([]bool, len(chunk)+1)
	ok[0] = true
	for i := 1; i <= len(chunk); i++ {
		for l := 1; l <= maxSyllableLength && l <= i; l++ {
			if ok[i-l] && IsSyllable(chunk[i-l:i]) {
				ok[i] = true
				break
			}
		}
	}
	return ok[len(chunk)]
}

// typoCorrections 生成 input 的「单次编辑」修正候选（只生成能完整切分成
// 合法音节的那些串），按编辑类型与字典序稳定输出。
//
// 覆盖四类最常见的手误（实测 12 个典型错拼有 10 个属于这四类）：
//   - transpose：相邻两字母颠倒（zhogn → zhong，手速快时的经典错误）
//   - delete：多敲了一个字母（zhonng / niihao → zhong / nihao）
//   - insert：漏敲了一个字母（zhonguo → zhongguo）
//   - substitute：敲错了一个键（nihap / nihso → nihao）
//
// 生成量级：输入长度 n 时约 n(转置) + n(删除) + 26n(插入) + 25n(替换) 个串，
// 再逐个做「能否切分」校验（O(n·6)），单键输入成本在微秒级。
// 调用方只应在原输入无法切分时才调用，避免正常输入白付这份开销。
func typoCorrections(input string, maxResults int) []string {
	if input == "" {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 24
	}
	seen := make(map[string]bool, maxResults*4)
	var out []string
	// 过滤条件用「能否切成纯完整音节」，而不是 misspelledFromPatterns：
	// 后者内部还要做一次完整切分（含简拼路径枚举），而这里要过滤的候选
	// 数量是 maxResults 的数十倍，用重判据会把单键延迟推到毫秒级
	// （实测曾达 25ms）。修正串的正确形态就是「合法音节序列」，
	// 用纯音节 DP 校验既便宜又精确。
	add := func(cand string) {
		if len(out) >= maxResults || seen[cand] {
			return
		}
		if cand == input || !hasCleanSyllableSplit(cand) {
			return
		}
		seen[cand] = true
		out = append(out, cand)
	}
	r := []byte(input)
	n := len(r)

	// 1) 相邻互换：最优先，因为它是「同一个词、顺序打反」，语义最可能正确。
	for i := 0; i+1 < n; i++ {
		if r[i] == r[i+1] {
			continue
		}
		c := append([]byte(nil), r...)
		c[i], c[i+1] = c[i+1], c[i]
		add(string(c))
	}
	// 2) 删除一个字母（多敲）
	for i := 0; i < n; i++ {
		add(string(r[:i]) + string(r[i+1:]))
	}
	// 3) 插入一个字母（漏敲）。
	//
	// 遍历顺序很关键：名额有限（maxResults），若按「位置在外、字母在内」
	// 遍历，位置 0 的 26 个插入就会吃光全部名额，末位置的补全（zhonguo
	// 需要在中段插 g）永远轮不到。改为「字母在外、位置在内」交错后，
	// 每个位置的候选都能分到名额。
	for ch := byte('a'); ch <= 'z'; ch++ {
		for i := 0; i <= n; i++ {
			add(string(r[:i]) + string(ch) + string(r[i:]))
		}
	}
	// 4) 替换一个字母（敲错键），同样按字母交错。
	for ci := 0; ci < 26; ci++ {
		ch := byte('a' + ci)
		for i := 0; i < n; i++ {
			if r[i] == ch {
				continue
			}
			c := append([]byte(nil), r...)
			c[i] = ch
			add(string(c))
		}
	}
	return out
}
