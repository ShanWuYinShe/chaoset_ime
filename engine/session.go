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
}

func NewSession(dict *Dictionary, user *UserDict) *Session {
	return &Session{dict: dict, user: user}
}

// Input 追加输入。非小写字母与 ' 的字符被过滤；标点键走 Punct
// （引擎侧统一映射为中文标点，壳只负责转发标点键）。
func (s *Session) Input(text string) (Composition, error) {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if (r >= 'a' && r <= 'z') || r == '\'' {
			b.WriteRune(r)
		}
	}
	s.input += b.String()
	s.truncate()
	s.page = 0 // 组词内容变化，回到第一页
	s.resetPunctCycle()
	return s.Compose(), nil
}

func (s *Session) truncate() {
	runes := []rune(s.input)
	if len(runes) > maxInputRunes {
		s.input = string(runes[len(runes)-maxInputRunes:])
	}
}

// Backspace 删除最后一个输入字符。
func (s *Session) Backspace() (Composition, error) {
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
	return s.Compose(), nil
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

// chunkSeg 是一段（以 ' 分隔的）连续字母的切分结果：
// paths 为完整音节路径；maxReach/tailPath 是最长可切前缀（其余为残缺音节）。
type chunkSeg struct {
	chunk    string
	paths    [][]string
	maxReach int
	tailPath []string
}

// enumerateChunk 枚举一段字母的全部音节切分（先试长音节，保证
// 第一条路径是贪心最长，用于预编辑展示），并记录最长可切前缀。
func enumerateChunk(chunk string) chunkSeg {
	seg := chunkSeg{chunk: chunk}
	var path []string
	var dfs func(pos int)
	dfs = func(pos int) {
		if pos > seg.maxReach {
			seg.maxReach = pos
			seg.tailPath = append(seg.tailPath[:0], path...)
		}
		if pos == len(chunk) {
			if len(seg.paths) < maxPathsPerChunk {
				seg.paths = append(seg.paths, append([]string(nil), path...))
			}
			return
		}
		maxLen := len(chunk) - pos
		if maxLen > maxSyllableLength {
			maxLen = maxSyllableLength
		}
		for l := maxLen; l >= 1; l-- {
			frag := chunk[pos : pos+l]
			if IsSyllable(frag) {
				path = append(path, frag)
				dfs(pos + l)
				path = path[:len(path)-1]
			}
		}
	}
	dfs(0)
	return seg
}

// combineChunks 把各段的切分路径做笛卡尔积（' 只是强制音节边界，
// 词可以跨段，如 xi'an=西安），超出上限时截断。残缺音节只可能出现在
// 末段，以 frag 返回。
func combineChunks(chunks []chunkSeg) ([][]string, string) {
	opts := [][]string{{}}
	frag := ""
	for i, cs := range chunks {
		last := i == len(chunks)-1
		var next [][]string
		appendOpt := func(sylls []string) {
			for _, base := range opts {
				if len(next) >= maxSegmentations {
					return
				}
				merged := make([]string, 0, len(base)+len(sylls))
				merged = append(merged, base...)
				merged = append(merged, sylls...)
				next = append(next, merged)
			}
		}
		for _, p := range cs.paths {
			appendOpt(p)
		}
		if last && cs.maxReach < len(cs.chunk) {
			appendOpt(cs.tailPath)
			frag = cs.chunk[cs.maxReach:]
		}
		if len(next) == 0 {
			return nil, ""
		}
		opts = next
	}
	return opts, frag
}

// Compose 基于当前输入生成组词状态：枚举全部音节切分、查主词典与
// 用户词组、合并去重，按「基础词频 + 用户得分（调频/上下文加权）」排序。
func (s *Session) Compose() Composition {
	comp := Composition{Input: s.input}
	if s.input == "" {
		return comp
	}
	rawChunks := strings.Split(s.input, "'")
	chunks := make([]chunkSeg, len(rawChunks))
	for i, c := range rawChunks {
		chunks[i] = enumerateChunk(c)
	}
	paths, frag := combineChunks(chunks)

	var merged []Candidate
	seen := make(map[string]int)
	addCandidate := func(e *Entry, score int, fromUser bool) {
		key := e.Word + "\x00" + strings.Join(e.Syllables, " ")
		if prev, ok := seen[key]; ok {
			if score > merged[prev].Score {
				merged[prev].Score = score
				merged[prev].FromUser = fromUser
			} else if fromUser {
				merged[prev].FromUser = true
			}
			return
		}
		seen[key] = len(merged)
		merged = append(merged, Candidate{
			Text:      e.Word,
			Syllables: strings.Join(e.Syllables, "'"),
			Score:     score,
			FromUser:  fromUser,
		})
	}
	for _, sylls := range paths {
		var entries []*Entry
		if frag == "" {
			entries = s.dict.Query(sylls)
		} else {
			entries = s.dict.QueryPrefix(sylls, frag)
		}
		for _, e := range entries {
			boost := 0
			if s.user != nil {
				boost = s.user.Score(e.Word, e.Syllables, s.prev)
			}
			addCandidate(e, e.Freq+boost, boost > 0)
		}
		if s.user != nil {
			for _, e := range s.user.QueryPhrases(sylls, frag) {
				addCandidate(e, e.Freq, true)
			}
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		a, b := merged[i], merged[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if len(a.Text) != len(b.Text) {
			return len(a.Text) < len(b.Text)
		}
		return a.Text < b.Text
	})
	// 分页：按 pageSize 切片返回当前页；页码越界（组词变短等）在此钳制
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
	if len(paths) > 0 {
		pre := strings.Join(paths[0], "'")
		if frag != "" {
			if pre != "" {
				pre += "'"
			}
			pre += frag
		}
		if pre != "" {
			preedit = pre
		}
	}
	comp.Preedit = preedit
	comp.Page = s.page
	comp.PageCount = pageCount
	comp.Candidates = merged[start:end]
	return comp
}
