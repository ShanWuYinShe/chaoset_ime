package engine

import (
	"fmt"
	"sort"
	"strconv"
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
// PageSize 返回每页候选数（数字键 1..PageSize 对应本页候选）。
// 壳侧应按此值判定数字键是否选中候选，避免越界静默吞键：
// pageSize 由 9 改成 6 后，壳仍接受 1-9，按 7/8/9 会既不选词也不进文档。
func PageSize() int { return pageSize }

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
	// BackspaceWord 逐词/逐音节回退：删除末尾的一个音节或词段。
	BackspaceWord() (Composition, error)
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
	maxPathsPerChunk  = 8
	maxSegmentations  = 16
	// pageSize：每页候选数。6 个是逐键选词的最舒适页容量（数字键 1-6 全部
	// 在标准指位内，且候选行不易换行；2026-10-25 用户反馈由 9 调整）。
	// 翻页状态在 Session.page，边界在 Compose 钳制。
	pageSize = 6

	// maxAssociate 是联想候选的条数上限（单页展示）。
	// **必须与 pageSize 一致**：壳按数字键 1..N 选联想词，若联想返回的条数
	// 多于 pageSize，多出来的候选用户永远选不到（2026-10-26 审查发现：此前
	// 联想上限写死 9 而 pageSize=6，第 7-9 条联想词无法选中）。
	maxAssociate = pageSize

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

	lastPunct rune // 上一次标点键（同键连按循环判定；0 = 无）
	// Compose 记忆化：key 是当前输入串。排序依赖的 prev/user 数据只在
	// 上屏（清空 input）时变化，因此 input 相同即结果相同。
	composeMemoKey string
	composeMemo    Composition
	lastPunctIdx   int // 上一次循环位置

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

// BackspaceWord 删除末尾的一个完整音节或词段（对应 Option+Backspace）。
// 若输入包含显式撇号或完整音节切分，回退到前一个音节边界；仅剩单音节或无法切分时清空。
func (s *Session) BackspaceWord() (Composition, error) {
	if s.dpScheme != "" {
		// 双拼模式下单次 Backspace 已是完整音节回滚，直接复用
		return s.Backspace()
	}
	if s.input == "" {
		return s.Compose(), nil
	}

	trimmed := strings.TrimRight(s.input, "'")
	if trimmed == "" {
		s.input = ""
		s.page = 0
		s.resetPunctCycle()
		return s.Compose(), nil
	}

	// 若输入含有显式撇号，优先回退到最后一个撇号处
	if idx := strings.LastIndexByte(trimmed, '\''); idx >= 0 {
		s.input = trimmed[:idx]
		s.page = 0
		s.resetPunctCycle()
		return s.Compose(), nil
	}

	// 根据音节切分方案找到最后一个音节的长度
	rawChunks := strings.Split(trimmed, "'")
	tokenPatterns := enumerateAllTokens(rawChunks)
	if len(tokenPatterns) > 0 && len(tokenPatterns[0]) > 1 {
		best := tokenPatterns[0]
		lastTok := best[len(best)-1]
		delLen := len([]rune(lastTok.Text))
		runes := []rune(trimmed)
		if delLen > 0 && delLen < len(runes) {
			s.input = string(runes[:len(runes)-delLen])
			s.input = strings.TrimRight(s.input, "'")
			s.page = 0
			s.resetPunctCycle()
			return s.Compose(), nil
		}
	}

	// 仅剩一个音节或无法切分时全部清空
	s.input = ""
	s.page = 0
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
	s.composeMemoKey = ""
	s.composeMemo = Composition{}
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
	// 学习数据的写入失败**绝不能阻断上屏**。
	//
	// 输入法的铁律是「用户按了键就必须出字」。旧实现把 BumpWord/BumpBigram 的
	// 错误返回给调用方，于是磁盘满、路径只读、权限变更等任何落盘故障都会让
	// 这次上屏彻底失败——壳已经把按键消费掉了，用户看到的就是「按数字键毫无
	// 反应，字也没进文档」（2026-10-26 复现确认）。学习是增量优化，失败只应
	// 记录而不影响组词结果；下一次 flush 成功时会自动补上。
	if s.user != nil {
		_ = s.user.BumpWord(text, sylls)
		if s.prev != "" {
			_ = s.user.BumpBigram(s.prev, text)
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

// Associate 返回「以上屏词音节序列开头」的联想候选：用户选词上屏后拼音区
// 已清空，候选窗继续展示的续接词组（如上屏「现在」→ 联想「现在时间」）。
// 纯查询，不改会话状态；最多返回 9 个（单页）。
func (s *Session) Associate(sylls []string) []Candidate {
	if s.dict == nil || len(sylls) == 0 {
		return nil
	}
	var out []Candidate
	seen := map[string]bool{}
	add := func(e *Entry) {
		key := userKey(e.Word, e.Syllables)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Candidate{
			Text:      e.Word,
			Syllables: strings.Join(e.Syllables, "'"),
			Score:     e.Freq,
		})
	}
	for _, e := range s.dict.QueryPrefix(sylls, "") {
		add(e)
	}
	if s.user != nil {
		for _, e := range s.user.QueryPhrasesPrefix(sylls) {
			add(e)
		}
	}
	if s.user != nil {
		// 上下文加成：user.Score 含调频与「上屏词 → 联想词」共现（Bigram），
		// 用户常打的联想词（如反复输入「现在的」跟随「现在」）排到前面。
		for i := range out {
			syllsList := strings.Split(out[i].Syllables, "'")
			out[i].Score += s.user.Score(out[i].Text, syllsList, s.prev)
		}
	}
	// QueryPrefix 按 Trie 子树序收集、顺序与词频无关；联想候选展示给用户时
	// 应高频在前（毫米 64 不应排在 豪快 9 之后），按加成后得分降序稳定排序。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > maxAssociate {
		out = out[:maxAssociate]
	}
	return out
}

// AssociateSelect 联想候选直接上屏：不需要拼音组词状态，学习路径与
// Select 完全一致（调频、上下文共现、词组进化），保证「越用越顺手」
// 对联想上屏同样成立。
func (s *Session) AssociateSelect(word string, sylls []string) (string, error) {
	if word == "" || len(sylls) == 0 {
		return "", fmt.Errorf("联想上屏参数为空")
	}
	// 同 Select：学习写入失败不阻断联想上屏。
	if s.user != nil {
		_ = s.user.BumpWord(word, sylls)
		if s.prev != "" {
			_ = s.user.BumpBigram(s.prev, word)
		}
	}
	s.history = append(s.history, learnEntry{word: word, sylls: sylls})
	if len(s.history) > maxHistory {
		s.history = s.history[len(s.history)-maxHistory:]
	}
	if s.user != nil {
		s.learnPhrase()
	}
	s.prev = word
	return word, nil
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
//
// 复杂度保证：使用 dead-end 记忆化剪枝（failedFull / failedMixed），
// 保证任意长度（包括输入残缺、无法完全切分的超长串）搜索在 O(N) 线性时间内完成，
// 彻底杜绝指数级回溯导致的 CPU 飙高与卡死。
func tokenizeChunk(chunk string) [][]Token {
	if len(chunk) == 0 {
		return nil
	}

	// 1. 优先纯全拼切分（长音节优先，贪心最长）
	var fullPaths [][]Token
	var cur []Token
	failedFull := make([]bool, len(chunk)+1)
	var dfsFull func(pos int) bool
	dfsFull = func(pos int) bool {
		if pos == len(chunk) {
			fullPaths = append(fullPaths, append([]Token(nil), cur...))
			return true
		}
		if len(fullPaths) >= maxPathsPerChunk {
			return true
		}
		if failedFull[pos] {
			return false
		}
		maxLen := len(chunk) - pos
		if maxLen > maxSyllableLength {
			maxLen = maxSyllableLength
		}
		anySuccess := false
		for l := maxLen; l >= 1; l-- {
			// 别名归一（lue → lve）：用户按键盘习惯敲 u 代替 ü 时，
			// 仍应命中词库中以 v 记写的音节（略 / 虐）。
			sub := canonicalSyllable(chunk[pos : pos+l])
			if IsSyllable(sub) {
				cur = append(cur, Token{Text: sub, IsExact: true})
				if dfsFull(pos + l) {
					anySuccess = true
				}
				cur = cur[:len(cur)-1]
				if len(fullPaths) >= maxPathsPerChunk {
					return true
				}
			}
		}
		if !anySuccess {
			failedFull[pos] = true
		}
		return anySuccess
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
	failedMixed := make([]bool, len(chunk)+1)
	var dfsMixed func(pos int) bool
	dfsMixed = func(pos int) bool {
		if pos == len(chunk) {
			mixedPaths = append(mixedPaths, append([]Token(nil), curMixed...))
			return true
		}
		if len(mixedPaths) >= maxPathsPerChunk {
			return true
		}
		if failedMixed[pos] {
			return false
		}
		maxLen := len(chunk) - pos
		if maxLen > maxSyllableLength {
			maxLen = maxSyllableLength
		}

		anySuccess := false
		foundMultiSyllable := false
		for l := maxLen; l >= 1; l-- {
			sub := chunk[pos : pos+l]
			if IsSyllable(sub) {
				if l >= 2 {
					foundMultiSyllable = true
				}
				curMixed = append(curMixed, Token{Text: sub, IsExact: true})
				if dfsMixed(pos + l) {
					anySuccess = true
				}
				curMixed = curMixed[:len(curMixed)-1]
				if len(mixedPaths) >= maxPathsPerChunk {
					return true
				}
			}
		}

		if !foundMultiSyllable {
			if pos+2 <= len(chunk) {
				sub := chunk[pos : pos+2]
				if sub == "zh" || sub == "ch" || sub == "sh" {
					curMixed = append(curMixed, Token{Text: sub, IsExact: false})
					if dfsMixed(pos + 2) {
						anySuccess = true
					}
					curMixed = curMixed[:len(curMixed)-1]
					if len(mixedPaths) >= maxPathsPerChunk {
						return true
					}
				}
			}
			if pos+1 <= len(chunk) {
				sub := chunk[pos : pos+1]
				if isInitial(sub) {
					curMixed = append(curMixed, Token{Text: sub, IsExact: false})
					if dfsMixed(pos + 1) {
						anySuccess = true
					}
					curMixed = curMixed[:len(curMixed)-1]
					if len(mixedPaths) >= maxPathsPerChunk {
						return true
					}
				}
			}
		}

		if !anySuccess {
			failedMixed[pos] = true
		}
		return anySuccess
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

// typoMaxCorrections 是一次错拼修正最多尝试的修正串数量。
//
// 单次编辑能生成的串数上限约 3n + 26(n+1) + 25n（n = 输入长度），十来个
// 字母就有上千条，全查太贵。取 160 的依据：纠错只在「输入连简拼路径都
// 排不出来」时触发（正常打字零开销），typoCorrections 已把最有价值的
// 「相邻互换 + 删除」排在最前，160 条足以覆盖它们并给插入留足额度。
// typoMaxQueries 限制实际拿去查词典的条数（见下方），生成可以更多。
// 实测 24 条会让「中段漏字」（zhonguo → zhongguo，需要在中间插 g）的
// 修正被靠前位置的候选挤掉。
const typoMaxCorrections = 32

// typoMaxQueries 限制实际拿去查词典的修正串数量。
//
// 生成（typoCorrections）很便宜——实测约 39µs，因为它只做纯音节 DP 校验；
// 贵的是每条一次的词典 Trie 遍历。实测把上限从 160 降到 32，zhogn 的单键
// 耗时从 2.1ms 降到百微秒级，而修正效果不变（zhogn → 中 仍为首选）。
const typoMaxQueries = 32

// typoPromoteRatio 是纠错候选升级为领跑的门槛：纠错命中的词频必须达到
// 「常规单词级候选最高词频」的这个倍数。
//
// 取 2 的依据（实测词频对比）：
//
//	zhogn（手误）    → 纠错命中 中(90)  ，常规碎片里最好的是 中欧(40) → 90/40 = 2.25 ≥ 2，提升 ✓
//	yiwys（合法混拼）→ 纠错命中 一无所有(37)，常规候选有一五一十(36) → 37/36 ≈ 1.03 < 2，不提升 ✓
//
// 即：只有「纠错结果明显比常规解析更像真实词」时才抢占首位，
// 避免为了修手误而牺牲正常混拼输入的首选率（实测不设门槛会让
// mixed 首选率从 34.32% 掉到 20.75%）。
const typoPromoteRatio = 2

// nonAdjacentTypoPenalty 是对远端非邻近键替换纠错候选的降权分值。
// 键盘输入错按物理相邻键（如 p->o, r->e）属于高置信度物理手误，保留原词频；
// 跨键区的非邻近替换（如 r->a）置信度显著更低，扣除该分值以防止巧合高频词
// 反客为主（如 yishrng 纠错时「以上」反压「一声/医生」）。
const nonAdjacentTypoPenalty = 15

// continuationPenalty 是续接候选每多补一个字符的得分折价。
//
// 量纲说明：静态词频区间是 1..99，「每补 1 个字符扣 30 分」等价于
// 「预测词必须比同输入下的完整词高频约 30 档才能赢」，正好落在分界线上：
//   - 中国(85) 补 o 后 55 > 中古(36)：残缺输入 zhonggu 首选「中国」；
//   - 一霎(30) 对比 以上(73)：yisha 本身是完整输入，首选仍是「一霎」。
//
// 取值由数据扫描确定（见 README「输入预测」实测表）：30 是唯一同时保住
// 「全拼完整输入首选率」与「末音节残缺首选率」的点——20 会侵蚀完整输入
// （80.75% → 79.86%），40 则让残缺输入的预测收益明显缩水（44.38% → 42.47%）。
const continuationPenalty = 30

// partialTailSplit 是「末段残缺」的一种拆法：前面已完整的音节 + 待补全串。
type partialTailSplit struct {
	tokens []Token
	frag   string
}

// partialTailSplits 枚举「末段整体尚未构成完整音节」的续接形态。
// 与 syllableContinuations 那条路径的区别：那里末音节本身是合法音节
// （gu / jin / xu 只是还能延长），这里末段连音节都不是（mia / nchen），
// 必须整段作为 prefix 去续接，否则只会被切成碎片出假词。
//
// 做法：从最长 6 个字符往短试末段 frag，前面剩下的部分必须能被完整
// 切分成音节；frag 一旦本身是合法音节就停止（那种情况由上一条路径覆盖）。
func partialTailSplits(input string) []partialTailSplit {
	if input == "" || strings.HasSuffix(input, "'") {
		return nil
	}
	lastChunk := input
	if i := strings.LastIndexByte(input, '\''); i >= 0 {
		lastChunk = input[i+1:]
	}
	if lastChunk == "" {
		return nil
	}
	var out []partialTailSplit
	// frag 从 2 个字符起递增：**优先把最长的一段当作前置完整音节**
	// （quanmia → quan + mia，而不是 qu + anmia），这样续接出的
	// 「全面」才不会被更长的 frag 拆成噪声。
	// 1 个字符的末段属于声母简拼的领域，交给常规路径。
	for fl := 2; fl <= maxSyllableLength && len(out) < 2; fl++ {
		if fl >= len(lastChunk) {
			continue
		}
		frag := lastChunk[len(lastChunk)-fl:]
		if IsSyllable(frag) {
			break // 末段已是完整音节，这条路径不适用
		}
		syls, ok := splitExactSyllables(lastChunk[:len(lastChunk)-fl])
		if !ok || len(syls) == 0 {
			continue
		}
		toks := make([]Token, len(syls))
		for i, syl := range syls {
			toks[i] = Token{Text: syl, IsExact: true}
		}
		out = append(out, partialTailSplit{tokens: toks, frag: frag})
	}
	return out
}

// splitExactSyllables 把一段拼音贪心切成完整音节；有任何字符无法归入
// 合法音节时返回 ok=false。
func splitExactSyllables(chunk string) ([]string, bool) {
	var syls []string
	for len(chunk) > 0 {
		maxL := len(chunk)
		if maxL > maxSyllableLength {
			maxL = maxSyllableLength
		}
		matched := false
		for l := maxL; l >= 1; l-- {
			if IsSyllable(chunk[:l]) {
				syls = append(syls, chunk[:l])
				chunk = chunk[l:]
				matched = true
				break
			}
		}
		if !matched {
			return nil, false
		}
	}
	return syls, true
}

// syllableContinuations 返回「以 syl 为前缀、比 syl 更长的合法音节」中
// syl 之后需要补敲的那一段（如 gu → ["o","i","ang"...] 中的 o/i/ang 等）。
// 空结果表示该音节无法再延长，也就没有续接预测价值。
func syllableContinuations(syl string) []string {
	if syl == "" {
		return nil
	}
	var out []string
	for _, s := range syllableSorted {
		if len(s) > len(syl) && strings.HasPrefix(s, syl) {
			out = append(out, s[len(syl):])
		}
	}
	return out
}

func tokensKey(tokens []Token) string {
	var b strings.Builder
	for i, t := range tokens {
		if i > 0 {
			b.WriteByte('\'')
		}
		b.WriteString(t.Text)
		if !t.IsExact {
			b.WriteByte('?')
		}
	}
	return b.String()
}

// Compose 基于当前输入生成组词状态：枚举全部音节约束切分（支持全拼、简拼与混拼）、
// 查主词典与用户词库、生成整句候选、合并去重，并按匹配优先级与综合得分排序。
func (s *Session) Compose() Composition {
	// 记忆化：同一输入串的 Compose 结果必然相同（排序依赖的 prev/user 数据
	// 只在上屏时变化，而上屏会清空 input 使 key 失效）。壳的调用模式是
	// input() 拿候选后还会连续取翻页/选择等，翻页路径会再 Compose 一次——
	// 不加缓存时每键要做两次完整组词（实测 zh 多花 1.1ms）。
	// key 必须包含页码：NextPage/PrevPage 只改 s.page 不改输入串。
	key := s.input + "\x00" + strconv.Itoa(s.page)
	if key == s.composeMemoKey {
		return s.composeMemo
	}
	return s.compose()
}

// compose 是 Compose 的实际实现（见上方 Compose 的记忆化说明）。
func (s *Session) compose() Composition {
	comp := Composition{Input: s.input}
	if s.input == "" {
		return comp
	}
	rawChunks := strings.Split(s.input, "'")
	tokenPatterns := enumerateAllTokens(rawChunks)
	// 注意：这里**不能**在 tokenPatterns 为空时直接返回。完全无法切分的输入
	// （beijngi / zhonguo / niihao）正是「打错字」的典型形态，必须继续往下走
	// 错拼修正路径才能救回来；常规 token 循环本身对空切片是安全的空操作。

	// candItem 将候选与匹配层级绑定，确保排序时 rank 与候选始终保持一致。
	type candItem struct {
		cand Candidate
		rank int
	}
	var items []candItem
	seen := make(map[string]int)
	seenShorter := make(map[string]bool)

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

	// 匹配层级（数字越小越靠前）。
	//
	// 注意 rankContinuation 与 rankExact 是**两个不同层级**，续接默认排在
	// 完整匹配之后：输入能被切成干净音节时（yisha = yi'sha），续接只是额外
	// 的预测补充。但续接候选与精确匹配的得分口径相同（都是「词频 - 补全代价」），
	// 所以高频预测词仍能在同层级内凭分数胜出（zhonggu → 中国）。
	//
	// rankContinuationLead 是兜底：输入压根无法切成干净音节时（quanmia 只能切成
	// quan'mi'a），同层候选全是「全米啊」这类碎片假词，必须由续接领跑。
	const (
		rankExact            = iota // 全拼单词完全匹配（如 jingchang -> 经常，ba -> 把）
		rankSentence                // 全拼多词 DP 拼接复合候选（如 womende -> 我们的）
		rankInitialExact            // 简拼/混拼单词完全匹配（如 zg -> 中国，bj -> 北京，jsj -> 计算机，rmb -> 人民币，zhongg -> 中国）
		rankInitialSentence         // 简拼/混拼多词 DP 拼接复合候选（如 wmd/womd -> 我们的）
		rankContinuation            // 末段残缺续接（zhonggu → 中国，daxu → 大学）
		rankInitialPrefix           // 简拼偏长词（如 zg -> 中国人民，中关村）
		rankPrefix                  // 全拼偏长词续接（如 jing -> 经济）
		rankBackoff                 // 逐级回退前缀（如 nihaox -> 你好）
		rankContinuationLead        // 无法干净切分时的续接兜底（quanmia → 全面，而非 全米啊）
		// rankTypoFix 是错拼修正候选的**初始**层级：最低，仅保证能在列表末尾
		// 看到。是否提升到 rankExact 参与领跑，由排序前的词频比较决定
		// （见「纠错候选的层级提升」段）——因为预筛只能看形态，无法区分
		// 手误（zhogn）与合法混拼（yiwys），必须靠「纠错命中词的词频远高于
		// 常规候选」这个词典事实才能安全提升。
		rankTypoFix
	)

	minPatternLen := 0
	if len(tokenPatterns) > 0 {
		minPatternLen = len(tokenPatterns[0])
	}
	allExactShape := true
	for _, pat := range tokenPatterns {
		if len(pat) < minPatternLen {
			minPatternLen = len(pat)
		}
	}
	// 输入的「最干净解析」：所有音节都是完整全拼、且没有任何一段被拆成
	// 声母简拼。只有当输入存在这样一条干净解析时，续接才退居次要位置——
	// 否则（quanmia 被切成 quan'mi'a、zhonggu 被切成 zhong'gu）候选里的
	// 东西全是碎片，应当由续接候选领跑。
	for _, pat := range tokenPatterns {
		if len(pat) != minPatternLen {
			continue
		}
		if !allExactShape {
			break
		}
		for _, tok := range pat {
			if !tok.IsExact {
				allExactShape = false
				break
			}
		}
	}

	// 末音节续接预测：末音节虽然已经打成一个合法音节，但它同时是某个更长
	// 音节的前缀时（gu→guo、jin→jing、xu→xue、ha→hao、ma→mian…），用户很
	// 可能还没打完。此时把末音节当作「待补全」做一次续接查询，才能让
	// zhonggu 打出「中国」、beijin 打出「北京」、daxu 打出「大学」。
	// 不加这一步时这些输入只能出「中古 / 被进 / 大需」这类巧合的碎词。
	//
	// 分层规则（两条续接路径共用）。
	//
	// 关键点：输入的末音节既可以按「已完整」解析（zhonggu = zhong'gu → 中古），
	// 也可以按「还差几个字母」解析（zhong'gu→guo → 中国）。两种解析在输入串上
	// **完全无法区分**，所以正确的做法不是人为规定谁优先，而是让它们同层竞争，
	// 由「词频 - 补全代价」决定（与真实输入法的拼音格 lattice + 语言模型一致）：
	// 中国(85) 即使扣掉补全代价也高于 中古(36)，而 一霎(30) 低于 以上(73)。
	// 因此 yisha 首选会变成「以上」——这是**预测生效的正确表现**（用户敲的确实
	// 是 yishang 的前缀），不是回归。
	//
	// 例外：输入**无法**被干净切分成完整音节时（quanmia 只能切成 quan'mi'a），
	// 同级候选全是「全米啊」这类碎片假词，此时续接必须领跑，否则根本出不来。
	continuationRank := rankExact
	if !allExactShape {
		continuationRank = rankContinuationLead
	}
	addedContinuation := map[string]bool{}
	for _, pat := range tokenPatterns {
		if len(pat) < 2 {
			continue
		}
		last := pat[len(pat)-1]
		if !last.IsExact || len(syllableContinuations(last.Text)) == 0 {
			continue
		}
		head := pat[:len(pat)-1]
		r := continuationRank
		entryList := s.dict.QueryContinuation(head, last.Text, 24)
		if s.user != nil {
			entryList = append(entryList, s.user.QueryContinuation(head, last.Text, 8)...)
		}
		for _, sc := range entryList {
			key := userKey(sc.Entry.Word, sc.Entry.Syllables)
			if addedContinuation[key] {
				continue
			}
			addedContinuation[key] = true
			if sc.Remain == "" {
				continue // 末音节本身已是完整音节：等同精确匹配，由常规路径覆盖
			}
			boost := 0
			if s.user != nil {
				boost = s.user.Score(sc.Entry.Word, sc.Entry.Syllables, s.prev)
			}
			// 补全越长越不确定（gu→guo 远强于 gu→guang），按字符数折价。
			score := sc.Entry.Freq + boost - continuationPenalty*len(sc.Remain)
			addCandidate(sc.Entry, score, boost > 0, r)
		}
	}
	// 情况二：末段本身不是一个完整音节（quanmia / shangmia / zhonghuarenming），
	// 常规切分会把残缺段拆成「完整音节 + 声母 + 韵母」的碎片（quan'mi'a），
	// 命中「全米啊」这类假词。这里把「前面已完整 + 末段整体当作待补全串」
	// 再查一次，才能出「全面 / 上面」。
	for _, tail := range partialTailSplits(s.input) {
		entryList := s.dict.QueryContinuation(tail.tokens, tail.frag, 24)
		if s.user != nil {
			entryList = append(entryList, s.user.QueryContinuation(tail.tokens, tail.frag, 8)...)
		}
		for _, sc := range entryList {
			key := userKey(sc.Entry.Word, sc.Entry.Syllables)
			if addedContinuation[key] {
				continue
			}
			addedContinuation[key] = true
			boost := 0
			if s.user != nil {
				boost = s.user.Score(sc.Entry.Word, sc.Entry.Syllables, s.prev)
			}
			// 补全越长越不确定（qua→quan 稳，qua→quang 更勉强），按字符数折价；
			// 层级统一由 continuationRank 决定（干净解析时退后，否则领跑）。
			score := sc.Entry.Freq + boost - continuationPenalty*len(sc.Remain)
			addCandidate(sc.Entry, score, boost > 0, continuationRank)
		}
	}

	// 错拼修正：用户手滑打错一两个字母时（zhogn / niihao / zhonguo / nihap），
	// 常规路径要么拼出一堆碎片假词（zhogn → 「中欧国内」），要么直接没候选。
	// 这里对输入做「单次编辑」修正，把能完整切分的修正串拿去做正常查询，
	// 才能把「中 / 你好 / 中国」找回来。
	//
	// 生成时统一用最低层级（rankTypoFix），是否「领跑」由排序前的词频比较
	// 决定（见下方「错拼修正的结果处理」）。
	//
	// 预筛直接复用上面已经算好的 tokenPatterns：切分是 Compose 里最贵的纯计算
	// 步骤之一，对同一输入再做一次会让逐键延迟明显上升。
	// 性能约束：这部分只在「输入切不动」时执行，但单次编辑会产出上百个
	// 修正串，若每个都做多路径枚举 + 全文查询，最坏情况会到几十毫秒——
	// 逐键延迟不可接受（实测 cap=160 时 zhonguo 达 25ms）。
	//
	// 因此这里刻意只用「一条」切分：修正串经 typoCorrections 的
	// misspelledFromPatterns 校验，已经能切成合法音节序列；取它的首条切分
	// （enumerateAllTokens 按最长音节优先产出）即可代表该修正串，不再枚举
	// 所有歧义切分。这样每个修正串只做一次线性遍历 + 一次词典查询。
	// typoSuspicious 同时决定两件事：① 是否生成纠错候选；② 常规路径是否
	// 做昂贵的 DP 整句与 prefix 收集——碎片输入的这些产物（zhogn 的
	// 「中欧国内」「中欧个」）最终都会被纠错候选压掉，却花费数毫秒
	// （实测 zhogn 单键 6ms 里常规路径占九成），是打错字瞬间卡顿的来源。
	typoSuspicious := misspelledFromPatterns(s.input, tokenPatterns)
	// 中间孤立声母形态：最优切分里「非末位」出现单字母声母碎片
	// （zhengchagn → zheng'cha'g'n，g 夹在两个完整音节之间）。可能是相邻
	// 颠倒手误（chagn → chang），也可能是合法混拼（shangxj → shang'x'j）
	// ——串形态不可分，放行常规路径，是否让纠错候选参与领跑由词典事实
	// 决定（见下方领跑判定）。
	midSuspicious := !typoSuspicious && hasMidIsolatedInitial(tokenPatterns)
	tailSuspicious := !typoSuspicious && !midSuspicious && hasTailIsolatedInitial(tokenPatterns)
	// 音节超段仲裁的输入段数：首选切分的段数（与跑分/纠错口径一致）。
	inputSegs := 0
	if len(tokenPatterns) > 0 {
		inputSegs = len(tokenPatterns[0])
	}
	typoAdded := make(map[string]bool)
	typoIsNear := make(map[string]bool)
	typoLen := make(map[string]int)
	if typoSuspicious || midSuspicious || tailSuspicious {
		fixes := typoCorrections(s.input, typoMaxCorrections)
		// 只查询靠前的若干条：生成很便宜（约 39µs），而每条一次的 Trie 查询
		// 才是这部分的主要成本。typoCorrections 已按价值排序
		// （相邻互换 → 删除 → 插入 → 替换），前若干条覆盖绝大多数真实手误。
		if len(fixes) > typoMaxQueries {
			fixes = fixes[:typoMaxQueries]
		}
		for _, fix := range fixes {
			patterns := enumerateAllTokens(strings.Split(fix.text, "'"))
			if len(patterns) == 0 {
				continue
			}
			qp := patterns[0]
			penalty := 0
			if !fix.isNear {
				penalty = nonAdjacentTypoPenalty
			}
			exacts, _ := s.dict.QueryTokens(qp, 8)
			for _, e := range exacts {
				key := userKey(e.Word, e.Syllables)
				if typoAdded[key] {
					continue
				}
				typoAdded[key] = true
				typoIsNear[key] = fix.isNear
				typoLen[key] = len(fix.text)
				boost := 0
				if s.user != nil {
					boost = s.user.Score(e.Word, e.Syllables, s.prev)
				}
				score := e.Freq + boost - penalty
				if score < 1 {
					score = 1
				}
				addCandidate(e, score, boost > 0, rankTypoFix)
			}
			if s.user != nil {
				uExacts, _ := s.user.QueryTokens(qp, 4)
				for _, e := range uExacts {
					key := userKey(e.Word, e.Syllables)
					if typoAdded[key] {
						continue
					}
					typoAdded[key] = true
					typoIsNear[key] = fix.isNear
					typoLen[key] = len(fix.text)
					score := e.Freq - penalty
					if score < 1 {
						score = 1
					}
					addCandidate(e, score, true, rankTypoFix)
				}
			}
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

			// 错拼形态：主查询换 QueryTokensExact（不展开 prefix 子树）。
			// 碎片前缀的 BFS 展开量大且产物全是假词。
			var exacts, prefixes []*Entry
			if typoSuspicious {
				exacts = s.dict.QueryTokensExact(qp, 20)
			} else {
				// 单音节完整音节：全量召回同音字（exact 上限放开、prefix
				// 维持 20）。词典中单音节可达 100+ 字（shi 107 个），此前
				// 精确匹配裁到词频 top 20——排 20 位之后的常用字（如 shi
				// 的「示」第 33 位）完全进不了候选池，翻页也打不出来
				// （2026-10-28 用户反馈）。排序仍在 compose 统一按词频，
				// 多出的字只是落到后面的页；prefix 若随 exact 一并放大，
				// 低频衍生词组会洪水般稀释单字页位并拖慢热路径（实测
				// 34.9µs → 71µs），故拆分为独立上限。
				exactLimit, prefixLimit := 20, 20
				if len(qp) == 1 && qp[0].IsExact {
					exactLimit = 120
				}
				exacts, prefixes = s.dict.queryTokens(qp, prefixLimit, exactLimit)
			}

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
				if !typoSuspicious && len(qp) == minPatternLen && len(qp) >= 2 {
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
				if !typoSuspicious && len(qp) == minPatternLen && len(qp) >= 2 {
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
			for drop := 1; !typoSuspicious && drop <= limit; drop++ {
				shorter := qp[:len(qp)-drop]
				sKey := tokensKey(shorter)
				if seenShorter[sKey] {
					continue
				}
				seenShorter[sKey] = true

				allExactShorter := true
				for _, t := range shorter {
					if !t.IsExact {
						allExactShorter = false
						break
					}
				}
				sExacts := s.dict.QueryTokensExact(shorter, 10)
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
				if drop == 1 && len(shorter) >= 2 && len(qp) == minPatternLen {
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
					uExacts := s.user.QueryTokensExact(shorter, 5)
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

	if len(typoAdded) > 0 {
		typoKey := func(c Candidate) string {
			return userKey(c.Text, strings.Split(c.Syllables, "'"))
		}
		bestTypoFreq := 0
		for i := range items {
			k := typoKey(items[i].cand)
			if typoAdded[k] && typoIsNear[k] && (!tailSuspicious || typoLen[k] >= len(s.input)) && items[i].cand.Score > bestTypoFreq {
				bestTypoFreq = items[i].cand.Score
			}
		}
		// 「常规候选有多强」只能采信**单词级精确匹配**，必须排除一切拼接产物：
		// zhogn 的候选里有「中欧个」(265 分)，它是靠把残段拆成 1 字母音节拼出来
		// 的（zhong'ou'ge，层级为 rankContinuationLead）——这类分数是「多字词
		// 奖励叠出来的虚高值」，与真实词频不可比，用它做门槛会把纠错永远挡住。
		//
		// 只采信 rankExact / rankInitialExact 两级（直接命中词典词条）。
		bestRegularFreq := 0
		hasRegularWord := false
		for i := range items {
			if typoAdded[typoKey(items[i].cand)] {
				continue
			}
			if items[i].rank != rankExact && items[i].rank != rankInitialExact {
				continue
			}
			// transpose 触发的手误场景：只有「音节数 == 输入段数」的全消耗
			// 精确词才算常规候选。中间夹声母的组合产物（zhengchagn 的
			// 「正茶馆你」）与单字碎片（正/茶）都是强行切分的产物——用户
			// 打 zhengchagn 的意图显然不是「正」单字，拿它们做门槛分母会让
			// 正确的纠错（正常）永远无法领跑（2026-10-25 用户反馈）。
			if (typoSuspicious || midSuspicious || tailSuspicious) && inputSegs > 0 && strings.Count(items[i].cand.Syllables, "'")+1 != inputSegs {
				continue
			}
			hasRegularWord = true
			if items[i].cand.Score > bestRegularFreq {
				bestRegularFreq = items[i].cand.Score
			}
		}
		if bestTypoFreq > 0 && (!hasRegularWord || bestTypoFreq >= typoPromoteRatio*bestRegularFreq) {
			for i := range items {
				k := typoKey(items[i].cand)
				if typoAdded[k] && typoIsNear[k] && (!tailSuspicious || typoLen[k] >= len(s.input)) {
					items[i].rank = rankExact
				}
			}
		}
	}

	//   ④ 简拼低频单字 vs 高频整句权衡：当简拼命中极低频词（Freq<20，如 wmd 命中「无目的」）
	//      而复合句得分极高（Score>=100，如「我们的」），优先展示高频整句；
	//   ⑤ 整句候选音节数仲裁：对于字符数相同的整句候选，音节数较少（音节更长、未碎片化）的优先（如「看一下」优于「看以西啊」）；
	//   ⑥ 综合得分、词长、字典序。
	// 部分消耗标记：rankExact/rankInitialExact 候选按序匹配的输入段数
	// 少于输入段数（打满多段却只命中前面几段）时为 true。
	partial := make([]bool, len(items))
	for i := range items {
		if items[i].rank != rankExact && items[i].rank != rankInitialExact {
			continue
		}
		sylls := strings.Split(items[i].cand.Syllables, "'")
		cons := 0
		for k, cs := range sylls {
			if k >= inputSegs {
				break
			}
			is := ""
			if k < len(tokenPatterns[0]) {
				is = tokenPatterns[0][k].Text
			}
			if cs == is || strings.HasPrefix(cs, is) || strings.HasPrefix(is, cs) {
				cons++
			} else {
				break
			}
		}
		partial[i] = cons < inputSegs
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].cand, items[j].cand
		aRank, bRank := items[i].rank, items[j].rank
		aBackoff := aRank >= rankBackoff
		bBackoff := bRank >= rankBackoff
		if aBackoff != bBackoff {
			return !aBackoff
		}
		// 音节超段仲裁（**最高排序键之一，位于调频之前**）：词音节数多于
		// 输入段数的候选（用户词组联想/前缀续接，如输入 xianzai、hao 时
		// 联想出的「现在时间」「好家伙」）不得排在「输入完全消耗」候选
		// （「现在」「好」）之前——即使后者是调频过的单字。
		// 2026-10-25 用户反馈二度修正：单音节输入 hao 的首候选被学习词组
		// 「好家伙」占据、单字「好」退到第 5 位。准确匹配用户输入是硬约束，
		// 调频只在「同为全消耗」的候选之间决定先后；联想延伸交由上屏后的
		// 联想区域展示（见 associate）。
		if inputSegs > 0 {
			aOver := strings.Count(a.Syllables, "'")+1 > inputSegs
			bOver := strings.Count(b.Syllables, "'")+1 > inputSegs
			if aOver != bOver {
				return !aOver
			}
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
	// 写入记忆化缓存（见 Compose 注释）
	s.composeMemoKey = s.input + "\x00" + strconv.Itoa(s.page)
	s.composeMemo = comp
	return comp
}
