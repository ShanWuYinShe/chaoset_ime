package engine

import (
	"fmt"
	"sort"
	"strings"
)

// Candidate 是一个候选项。Syllables 以 ' 连接，便于展示与传输。
type Candidate struct {
	Text      string `json:"text"`
	Syllables string `json:"syllables"`
	Score     int    `json:"score"`
	FromUser  bool   `json:"from_user,omitempty"` // 是否被用户数据（调频/词组/上下文）加权
}

// Composition 是当前组词状态：原始输入、预编辑串、当前页候选与分页信息。
// Page 为 0 起页码，PageCount 为总页数（每页 pageSize 个候选；无候选时为 0）。
type Composition struct {
	Input      string      `json:"input"`
	Preedit    string      `json:"preedit"`
	Candidates []Candidate `json:"candidates"`
	Page       int         `json:"page"`
	PageCount  int         `json:"page_count"`
}

// Controller 是会话的统一操作接口。本地 Session 与 IPC Client 都实现它，
// CLI 与未来的平台壳只面对这一层，切换本地/远端无需改调用方。
type Controller interface {
	Input(text string) (Composition, error)
	Backspace() (Composition, error)
	Reset() (Composition, error)
	Select(index int) (string, error)
	// Punct 提交一个标点键：组词中先上屏首选候选（无候选的残缺输入
	// 丢弃），随后输出映射后的中文标点；空组词直接输出中文标点。
	Punct(key string) (string, error)
	// NextPage / PrevPage 翻动候选页：组词中移动页码并返回当前页组成
	// 状态；空组词为无操作。页码在边界钳制，不回绕。
	NextPage() (Composition, error)
	PrevPage() (Composition, error)
	// CommitRaw 清空当前组词并返回用户输入的原始拼音（供回车直接上屏使用）。
	CommitRaw() (string, error)
}

const (
	maxInputRunes     = 64
	maxSyllableLength = 6
	maxPathsPerChunk  = 16
	maxSegmentations  = 64
	// pageSize：每页候选数。翻页状态在 Session.page，边界在 Compose 钳制。
	pageSize = 9

	// learnThreshold：相邻两词共现多少次后固化为用户词组（词组进化）。
	learnThreshold = 2
	// maxLearnSylls：可固化词组的最大音节数，避免学出无意义长串。
	maxLearnSylls = 4
	// maxHistory 限制参与学习观察的上屏历史长度。
	maxHistory = 8
)

// learnEntry 记录一次上屏，供词组进化观察「相邻共现」。
type learnEntry struct {
	word  string
	sylls []string
}

// Session 是一次连续输入的组词会话，持有用户习惯上下文：
// prev 是上一个上屏词（bigram 加成），history 是最近的选词轨迹
// （词组进化）。不保证并发安全：IPC 服务端为每个连接建独立
// Session，本地使用方自行串行化。
type Session struct {
	dict       *Dictionary
	user       *UserDict
	input      string // 仅小写字母与 '
	prev       string
	history    []learnEntry
	dquoteOpen bool // 中文双引号 “” 的开闭交替状态
	squoteOpen bool // 中文单引号 ‘’ 的开闭交替状态
	page       int  // 当前候选页（0 起；输入变化时重置为 0）

	lastPunct    rune // 上一次标点键（同键连按循环判定；0 = 无）
	lastPunctIdx int  // 上一次循环位置

	fuzzy bool // 模糊音：查询时展开声母/韵母等价音节（默认关闭）

	halfWidth bool // 半角模式：标点键原样输出（默认全角）

	traditional bool // 繁体输出：候选词经简繁映射（默认关闭，见 s2t.go）

	dpScheme  string // 双拼方案（zrm/mspy，见 double_pinyin.go；空 = 关闭）
	dpPending rune   // 双拼首键（等待第二键成对解码；0 = 无）
	dpLens    []int  // 每次「按键事件」实际追加到 input 的字符数（退格回滚用）
}

// SetDoublePinyin 设置双拼方案（DPSchemeZRM / DPSchemeMSPY；
// 空串关闭，未知方案按关闭处理）。须在首次输入前设置。
func (s *Session) SetDoublePinyin(scheme string) {
	if scheme == "" {
		s.dpScheme = ""
		return
	}
	if _, ok := dpSchemes[scheme]; ok {
		s.dpScheme = scheme
	}
}

// SetFuzzy 开关模糊音（规则表见 fuzzy.go）。须在首次查询前设置。
func (s *Session) SetFuzzy(on bool) { s.fuzzy = on }

// SetTraditional 开关繁体输出（零错误单字映射，见 s2t.go）。
// 须在首次查询前设置。
func (s *Session) SetTraditional(on bool) { s.traditional = on }

// ToggleHalfWidth 切换全角/半角标点模式，返回切换后的状态
// （true = 半角：标点键原样输出 ASCII）。
func (s *Session) ToggleHalfWidth() bool {
	s.halfWidth = !s.halfWidth
	s.resetPunctCycle()
	return s.halfWidth
}

// HalfWidth 报告当前是否为半角标点模式。
func (s *Session) HalfWidth() bool { return s.halfWidth }

func NewSession(dict *Dictionary, user *UserDict) *Session {
	return &Session{dict: dict, user: user}
}

// Input 追加输入。非小写字母与 ' 的字符被过滤；标点键走 Punct
// （引擎侧统一映射为中文标点，壳只负责转发标点键）。
// 双拼模式下按键两两成对解码为音节（见 double_pinyin.go）；
// 首键暂存不产生组成变化，无效键码对整体丢弃。
func (s *Session) Input(text string) (Composition, error) {
	for _, r := range strings.ToLower(text) {
		if s.dpScheme != "" {
			switch {
			case r == '\'':
				s.input += "'"
				s.dpLens = append(s.dpLens, 1)
				s.afterAppend()
			case r >= 'a' && r <= 'z':
				if s.dpPending == 0 {
					s.dpPending = r
					continue // 首键暂存；同一文本中的后续键继续成对解码
				}
				pair := string(s.dpPending) + string(r)
				s.dpPending = 0
				syl, ok := DoublePinyinDecode(s.dpScheme, pair[0], pair[1])
				if !ok {
					return s.Compose(), nil // 无效键码对：整体丢弃
				}
				s.input += syl
				s.dpLens = append(s.dpLens, len(syl))
				s.afterAppend()
			}
			continue
		}
		if (r >= 'a' && r <= 'z') || r == '\'' {
			s.input += string(r)
			s.afterAppend()
		}
	}
	return s.Compose(), nil
}

// afterAppend 处理追加后的公共状态：截断、回第一页、打断标点循环。
func (s *Session) afterAppend() {
	s.truncate()
	s.reconcileDpLens()
	s.page = 0 // 组词内容变化，回到第一页
	s.resetPunctCycle()
}

// reconcileDpLens 让双拼退格栈的累计追加数与截断后的 input 长度
// 保持一致（输入超过 maxInputRunes 被截断时栈可能超出）。
func (s *Session) reconcileDpLens() {
	runes := []rune(s.input)
	total := 0
	for _, n := range s.dpLens {
		total += n
	}
	for total > len(runes) && len(s.dpLens) > 0 {
		total -= s.dpLens[0]
		s.dpLens = s.dpLens[1:]
	}
}

func (s *Session) truncate() {
	runes := []rune(s.input)
	if len(runes) > maxInputRunes {
		s.input = string(runes[len(runes)-maxInputRunes:])
	}
}

// Backspace 删除最后一个输入字符。双拼模式下删除粒度是「按键事件」：
// 暂存中的首键直接清除；已解码音节整段回滚（对应两键）。
func (s *Session) Backspace() (Composition, error) {
	if s.dpScheme != "" {
		if s.dpPending != 0 {
			s.dpPending = 0
			return s.Compose(), nil
		}
		if n := len(s.dpLens); n > 0 {
			cnt := s.dpLens[n-1]
			s.dpLens = s.dpLens[:n-1]
			runes := []rune(s.input)
			if cnt > len(runes) {
				cnt = len(runes)
			}
			s.input = string(runes[:len(runes)-cnt])
		}
		s.page = 0
		s.resetPunctCycle()
		return s.Compose(), nil
	}
	if s.input != "" {
		runes := []rune(s.input)
		s.input = string(runes[:len(runes)-1])
	}
	s.page = 0 // 组词内容变化，回到第一页
	s.resetPunctCycle()
	return s.Compose(), nil
}

// Reset 清空当前组词与学习上下文（对应 Esc / 失焦：
// 输入流被切断后，相邻共现关系不再成立）。
// resetPunctCycle 清除同键连按循环状态（任何非标点路径的输入/上屏
// 都会打断循环，下一次标点回到序列首项）。
func (s *Session) resetPunctCycle() {
	s.lastPunct = 0
	s.lastPunctIdx = 0
}

func (s *Session) Reset() (Composition, error) {
	s.input = ""
	s.prev = ""
	s.history = nil
	s.page = 0
	s.resetPunctCycle()
	s.dpPending = 0
	s.dpLens = nil
	return s.Compose(), nil
}

// CommitRaw 清空当前组词并返回用户输入的原始拼音（供回车直接上屏使用）。
func (s *Session) CommitRaw() (string, error) {
	raw := s.input
	_, err := s.Reset()
	return raw, err
}

// NextPage 翻到下一页候选，返回当前页组成状态。空组词为无操作；
// 已在末页时页码被 Compose 钳制（返回内容不变，Page 保持）。
func (s *Session) NextPage() (Composition, error) {
	s.page++
	return s.Compose(), nil
}

// PrevPage 翻到上一页候选，返回当前页组成状态。空组词为无操作；
// 已在首页时页码被 Compose 钳制（返回内容不变，Page 保持）。
func (s *Session) PrevPage() (Composition, error) {
	s.page--
	return s.Compose(), nil
}

// Select 选中候选（0 起）并「上屏」：记调频、记上下文、观察词组进化，
// 清空组词并返回候选文本。
func (s *Session) Select(index int) (string, error) {
	comp := s.Compose()
	if index < 0 || index >= len(comp.Candidates) {
		return "", fmt.Errorf("候选序号 %d 越界（当前共 %d 个候选）", index+1, len(comp.Candidates))
	}
	c := comp.Candidates[index]
	sylls := strings.Split(c.Syllables, "'")
	text := c.Text
	if s.user != nil {
		if err := s.user.BumpWord(text, sylls); err != nil {
			return "", fmt.Errorf("记录用户词频失败: %w", err)
		}
		if s.prev != "" {
			if err := s.user.BumpBigram(s.prev, text); err != nil {
				return "", fmt.Errorf("记录上下文共现失败: %w", err)
			}
		}
	}
	s.history = append(s.history, learnEntry{word: text, sylls: sylls})
	if len(s.history) > maxHistory {
		s.history = s.history[len(s.history)-maxHistory:]
	}
	if s.user != nil {
		// 词组学习失败不阻断上屏（数据已落盘，学习是增量优化）
		s.learnPhrase()
	}
	s.prev = text
	s.input = ""
	s.resetPunctCycle()
	return text, nil
}

// learnPhrase 观察「最后两次上屏」：相邻共现达到阈值且主词典还没有
// 这个组合时，固化为用户词组。链式扩展（A+B→AB 之后 AB+又一词）
// 由后续真实上屏自然发生，无需特殊处理。
func (s *Session) learnPhrase() {
	if s.user == nil || len(s.history) < 2 {
		return
	}
	a := s.history[len(s.history)-2]
	b := s.history[len(s.history)-1]
	sylls := make([]string, 0, len(a.sylls)+len(b.sylls))
	sylls = append(sylls, a.sylls...)
	sylls = append(sylls, b.sylls...)
	if len(sylls) > maxLearnSylls {
		return
	}
	word := a.word + b.word
	if s.dict.Contains(word, sylls) {
		return
	}
	if s.user.PairCount(a.word, b.word) < learnThreshold {
		return
	}
	_ = s.user.BumpPhrase(word, sylls)
}

func isInitial(s string) bool {
	if s == "zh" || s == "ch" || s == "sh" {
		return true
	}
	if len(s) == 1 {
		b := s[0]
		return (b >= 'a' && b <= 'z') && b != 'v' && b != 'u' && b != 'i'
	}
	return false
}

// tokenizeChunk 枚举一段拼音的音节切分。
// 优先寻找能够完整覆盖该片段的纯全拼音节序列（最长贪心）；
// 仅当无法纯全拼切分时（如简拼 zg/bj/jsj、混拼 zhongg/womd/chifl），才启用声母简拼约束。
func tokenizeChunk(chunk string) [][]Token {
	if len(chunk) == 0 {
		return nil
	}

	// 1. 优先纯全拼切分（长音节优先，贪心最长）
	var fullPaths [][]Token
	var cur []Token
	var dfsFull func(pos int)
	dfsFull = func(pos int) {
		if pos == len(chunk) {
			fullPaths = append(fullPaths, append([]Token(nil), cur...))
			return
		}
		if len(fullPaths) >= maxPathsPerChunk {
			return
		}
		maxLen := len(chunk) - pos
		if maxLen > maxSyllableLength {
			maxLen = maxSyllableLength
		}
		for l := maxLen; l >= 1; l-- {
			sub := chunk[pos : pos+l]
			if IsSyllable(sub) {
				cur = append(cur, Token{Text: sub, IsExact: true})
				dfsFull(pos + l)
				cur = cur[:len(cur)-1]
			}
		}
	}
	dfsFull(0)

	if len(fullPaths) > 0 {
		// 单字母零声母（a/o/e）本身是独立音节（啊/哦/饿），
		// 亦作为该元音开头的音节（an/ai/ao、ou、en/er）的简拼声母（如 x'a 匹配「西安」，b'a 匹配「保安」）。
		if len(chunk) == 1 && (chunk == "a" || chunk == "o" || chunk == "e") {
			fullPaths = append(fullPaths, []Token{{Text: chunk, IsExact: false}})
		}
		return fullPaths
	}

	// 2. 无法完整纯全拼切分时，尝试简拼与混拼切分
	var mixedPaths [][]Token
	var curMixed []Token
	var dfsMixed func(pos int)
	dfsMixed = func(pos int) {
		if pos == len(chunk) {
			mixedPaths = append(mixedPaths, append([]Token(nil), curMixed...))
			return
		}
		if len(mixedPaths) >= maxPathsPerChunk {
			return
		}
		maxLen := len(chunk) - pos
		if maxLen > maxSyllableLength {
			maxLen = maxSyllableLength
		}

		foundMultiSyllable := false
		for l := maxLen; l >= 1; l-- {
			sub := chunk[pos : pos+l]
			if IsSyllable(sub) {
				if l >= 2 {
					foundMultiSyllable = true
				}
				curMixed = append(curMixed, Token{Text: sub, IsExact: true})
				dfsMixed(pos + l)
				curMixed = curMixed[:len(curMixed)-1]
			}
		}

		if !foundMultiSyllable {
			if pos+2 <= len(chunk) {
				sub := chunk[pos : pos+2]
				if sub == "zh" || sub == "ch" || sub == "sh" {
					curMixed = append(curMixed, Token{Text: sub, IsExact: false})
					dfsMixed(pos + 2)
					curMixed = curMixed[:len(curMixed)-1]
				}
			}
			if pos+1 <= len(chunk) {
				sub := chunk[pos : pos+1]
				if isInitial(sub) {
					curMixed = append(curMixed, Token{Text: sub, IsExact: false})
					dfsMixed(pos + 1)
					curMixed = curMixed[:len(curMixed)-1]
				}
			}
		}
	}
	dfsMixed(0)
	return mixedPaths
}

// enumerateAllTokens 将以 ' 分隔的各段切分路径做笛卡尔积，生成音节约束序列切分方案。
func enumerateAllTokens(rawChunks []string) [][]Token {
	opts := [][]Token{{}}
	for _, c := range rawChunks {
		if c == "" {
			continue
		}
		chunkPaths := tokenizeChunk(c)
		if len(chunkPaths) == 0 {
			return nil
		}
		var next [][]Token
		for _, base := range opts {
			for _, cp := range chunkPaths {
				if len(next) >= maxSegmentations {
					break
				}
				merged := append([]Token(nil), base...)
				merged = append(merged, cp...)
				next = append(next, merged)
			}
		}
		opts = next
	}
	return opts
}

// expandTokenVariants 在开启模糊音时，展开完整音节（IsExact=true）的模糊音变体。
func expandTokenVariants(tokens []Token, fuzzy bool) [][]Token {
	if !fuzzy || len(tokens) == 0 {
		return [][]Token{tokens}
	}
	sets := make([][]Token, len(tokens))
	for i, tok := range tokens {
		if tok.IsExact {
			fuzzies := fuzzyVariants(tok.Text)
			var list []Token
			for _, fz := range fuzzies {
				list = append(list, Token{Text: fz, IsExact: true})
			}
			sets[i] = list
		} else {
			sets[i] = []Token{tok}
		}
	}
	out := make([][]Token, 0, maxFuzzyPaths)
	cur := make([]Token, 0, len(tokens))
	var build func(i int)
	build = func(i int) {
		if len(out) >= maxFuzzyPaths {
			return
		}
		if i == len(sets) {
			out = append(out, append([]Token(nil), cur...))
			return
		}
		for _, v := range sets[i] {
			cur = append(cur, v)
			build(i + 1)
			cur = cur[:len(cur)-1]
		}
	}
	build(0)
	return out
}

// Compose 基于当前输入生成组词状态：枚举全部音节约束切分（支持全拼、简拼与混拼）、
// 查主词典与用户词库、生成整句候选、合并去重，并按匹配优先级与综合得分排序。
func (s *Session) Compose() Composition {
	comp := Composition{Input: s.input}
	if s.input == "" {
		return comp
	}
	rawChunks := strings.Split(s.input, "'")
	tokenPatterns := enumerateAllTokens(rawChunks)
	if len(tokenPatterns) == 0 {
		comp.Preedit = s.input
		return comp
	}

	// candItem 将候选与匹配层级绑定，确保排序时 rank 与候选始终保持一致。
	type candItem struct {
		cand Candidate
		rank int
	}
	var items []candItem
	seen := make(map[string]int)

	addCandidate := func(e *Entry, score int, fromUser bool, r int) {
		word := e.Word
		if s.traditional {
			word = ToTraditional(word) // 候选显示与上屏均为繁体
		}
		key := word + "\x00" + strings.Join(e.Syllables, " ")
		if prev, ok := seen[key]; ok {
			if score > items[prev].cand.Score {
				items[prev].cand.Score = score
				items[prev].cand.FromUser = fromUser
			} else if fromUser {
				items[prev].cand.FromUser = true
			}
			if r < items[prev].rank {
				items[prev].rank = r
			}
			return
		}
		seen[key] = len(items)
		items = append(items, candItem{
			cand: Candidate{
				Text:      word,
				Syllables: strings.Join(e.Syllables, "'"),
				Score:     score,
				FromUser:  fromUser,
			},
			rank: r,
		})
	}

	const (
		rankExact           = 0 // 全拼单词完全匹配（如 jingchang -> 经常，ba -> 把）
		rankSentence        = 1 // 全拼多词 DP 拼接复合候选（如 womende -> 我们的）
		rankInitialExact    = 2 // 简拼/混拼单词完全匹配（如 zg -> 中国，bj -> 北京，jsj -> 计算机，rmb -> 人民币，zhongg -> 中国）
		rankInitialSentence = 3 // 简拼/混拼多词 DP 拼接复合候选（如 wmd/womd -> 我们的）
		rankPartial         = 4 // 末段残缺续接（如 shix -> 史学）
		rankInitialPrefix   = 5 // 简拼偏长词（如 zg -> 中国人民，中关村）
		rankPrefix          = 6 // 全拼偏长词续接（如 jing -> 经济）
		rankBackoff         = 7 // 逐级回退前缀（如 nihaox -> 你好）
	)

	minPatternLen := len(tokenPatterns[0])
	for _, pat := range tokenPatterns {
		if len(pat) < minPatternLen {
			minPatternLen = len(pat)
		}
	}

	for _, pat := range tokenPatterns {
		for _, qp := range expandTokenVariants(pat, s.fuzzy) {
			hasInitials := false
			for _, tok := range qp {
				if !tok.IsExact {
					hasInitials = true
					break
				}
			}

			exacts, prefixes := s.dict.QueryTokens(qp, 20)

			if !hasInitials {
				// 纯全拼匹配
				for _, e := range exacts {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					addCandidate(e, e.Freq+boost, boost > 0, rankExact)
				}
				// 整句合成（DP）仅在最少音节数（即未将拼音过度碎片化）的切分路径上执行，
				// 避免生成如「看以西啊」(4音节) 压过「看一下」(3音节)、「地安娜哦上」(5音节) 压过「电脑上」(3音节)
				if len(qp) == minPatternLen && len(qp) >= 2 {
					for _, sc := range FindSentenceCandidatesTokens(s.dict, s.user, qp, 6) {
						if len(sc.Words) > 1 {
							boost := 0
							fromUser := false
							if s.user != nil {
								boost = s.user.Score(sc.Text, sc.Syllables, s.prev)
								if boost > 0 {
									fromUser = true
								}
							}
							e := &Entry{
								Word:      sc.Text,
								Syllables: sc.Syllables,
								Freq:      sc.Score,
							}
							addCandidate(e, sc.Score+boost, fromUser, rankSentence)
						}
					}
				}
				for _, e := range prefixes {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					addCandidate(e, e.Freq+boost, boost > 0, rankPrefix)
				}
			} else {
				// 简拼 / 混拼匹配
				for _, e := range exacts {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					addCandidate(e, e.Freq+boost, boost > 0, rankInitialExact)
				}
				if len(qp) == minPatternLen && len(qp) >= 2 {
					for _, sc := range FindSentenceCandidatesTokens(s.dict, s.user, qp, 6) {
						if len(sc.Words) > 1 {
							avgScore := sc.Score / len(sc.Words)
							boost := 0
							fromUser := false
							if s.user != nil {
								boost = s.user.Score(sc.Text, sc.Syllables, s.prev)
								if boost > 0 {
									fromUser = true
								}
							}
							e := &Entry{
								Word:      sc.Text,
								Syllables: sc.Syllables,
								Freq:      avgScore,
							}
							addCandidate(e, avgScore+boost, fromUser, rankInitialSentence)
						}
					}
				}
				for _, e := range prefixes {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					addCandidate(e, e.Freq+boost, boost > 0, rankInitialPrefix)
				}
			}

			// 查询用户词库中的自定义短语/自学习词
			if s.user != nil {
				uExacts, uPrefixes := s.user.QueryTokens(qp, 10)
				for _, e := range uExacts {
					r := rankExact
					if hasInitials {
						r = rankInitialExact
					}
					addCandidate(e, e.Freq, true, r)
				}
				for _, e := range uPrefixes {
					r := rankPrefix
					if hasInitials {
						r = rankInitialPrefix
					}
					addCandidate(e, e.Freq, true, r)
				}
			}

			// 逐级回退前缀：多音节输入叠加更短的前缀匹配（如输入 womende 时也出「我们」；
			// 打 nihaox 时，因已完整的「你好」之后是残缺声母，将「你好」置顶推出）
			limit := len(qp) - 1
			if limit > 6 {
				limit = 6
			}
			for drop := 1; drop <= limit; drop++ {
				shorter := qp[:len(qp)-drop]
				allExactShorter := true
				for _, t := range shorter {
					if !t.IsExact {
						allExactShorter = false
						break
					}
				}
				sExacts, _ := s.dict.QueryTokens(shorter, 10)
				for _, e := range sExacts {
					r := rankBackoff + drop
					if allExactShorter && drop == 1 && len(exacts) == 0 {
						hasDroppedInitial := false
						for _, t := range qp[len(qp)-drop:] {
							if !t.IsExact {
								hasDroppedInitial = true
								break
							}
						}
						if hasDroppedInitial {
							r = rankExact
						}
					}
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					addCandidate(e, e.Freq+boost, boost > 0, r)
				}
				if len(shorter) >= 2 && len(qp) == minPatternLen {
					for _, sc := range FindSentenceCandidatesTokens(s.dict, s.user, shorter, 4) {
						if len(sc.Words) > 1 {
							r := rankBackoff + drop + 1
							boost := 0
							fromUser := false
							if s.user != nil {
								boost = s.user.Score(sc.Text, sc.Syllables, s.prev)
								if boost > 0 {
									fromUser = true
								}
							}
							e := &Entry{
								Word:      sc.Text,
								Syllables: sc.Syllables,
								Freq:      sc.Score,
							}
							addCandidate(e, sc.Score+boost, fromUser, r)
						}
					}
				}
				if s.user != nil {
					uExacts, _ := s.user.QueryTokens(shorter, 5)
					for _, e := range uExacts {
						r := rankBackoff + drop
						if allExactShorter && drop == 1 && len(exacts) == 0 {
							hasDroppedInitial := false
							for _, t := range qp[len(qp)-drop:] {
								if !t.IsExact {
									hasDroppedInitial = true
									break
								}
							}
							if hasDroppedInitial {
								r = rankExact
							}
						}
						addCandidate(e, e.Freq, true, r)
					}
				}
			}
		}
	}

	// 排序（稳定）：
	//   ① 匹配了完整音节（精确匹配/整句候选）的候选永远优先于丢弃尾部音节的回退候选；
	//   ② 用户数据加权（调频/上下文/词组）——优先提升用户常用词；
	//   ③ 匹配层级（完全匹配 > 整句拼接 > 简拼完全匹配 > 简拼整句 > 偏长前缀 > 回退前缀）；
	//   ④ 简拼低频单字 vs 高频整句权衡：当简拼命中极低频词（Freq<20，如 wmd 命中「无目的」）
	//      而复合句得分极高（Score>=100，如「我们的」），优先展示高频整句；
	//   ⑤ 整句候选音节数仲裁：对于字符数相同的整句候选，音节数较少（音节更长、未碎片化）的优先（如「看一下」优于「看以西啊」）；
	//   ⑥ 综合得分、词长、字典序。
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].cand, items[j].cand
		aRank, bRank := items[i].rank, items[j].rank
		aBackoff := aRank >= rankBackoff
		bBackoff := bRank >= rankBackoff
		if aBackoff != bBackoff {
			return !aBackoff
		}
		if a.FromUser != b.FromUser {
			return a.FromUser
		}
		if aRank != bRank {
			if aRank == rankInitialExact && bRank == rankInitialSentence && a.Score < 20 && b.Score >= 100 {
				return false
			}
			if bRank == rankInitialExact && aRank == rankInitialSentence && b.Score < 20 && a.Score >= 100 {
				return true
			}
			return aRank < bRank
		}
		if aRank == rankSentence && bRank == rankSentence && len([]rune(a.Text)) == len([]rune(b.Text)) {
			aCount := strings.Count(a.Syllables, "'")
			bCount := strings.Count(b.Syllables, "'")
			if aCount != bCount {
				return aCount < bCount
			}
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if len(a.Text) != len(b.Text) {
			return len(a.Text) < len(b.Text)
		}
		return a.Text < b.Text
	})

	merged := make([]Candidate, len(items))
	for i, it := range items {
		merged[i] = it.cand
	}

	// 分页处理
	pageCount := (len(merged) + pageSize - 1) / pageSize
	if pageCount == 0 {
		s.page = 0
	} else if s.page >= pageCount {
		s.page = pageCount - 1
	} else if s.page < 0 {
		s.page = 0
	}
	start := s.page * pageSize
	end := start + pageSize
	if end > len(merged) {
		end = len(merged)
	}

	preedit := s.input
	if len(merged) > 0 && len(tokenPatterns) > 0 && len(tokenPatterns[0]) > 0 {
		var parts []string
		for _, tok := range tokenPatterns[0] {
			parts = append(parts, tok.Text)
		}
		preedit = strings.Join(parts, "'")
	}

	comp.Preedit = preedit
	comp.Page = s.page
	comp.PageCount = pageCount
	comp.Candidates = merged[start:end]
	return comp
}
