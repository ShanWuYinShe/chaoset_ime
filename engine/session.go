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
	FromUser  bool   `json:"from_user,omitempty"` // 是否已被用户词典加权
}

// Composition 是当前组词状态：原始输入、预编辑串与候选列表。
type Composition struct {
	Input      string      `json:"input"`
	Preedit    string      `json:"preedit"`
	Candidates []Candidate `json:"candidates"`
}

// Controller 是会话的统一操作接口。本地 Session 与 IPC Client 都实现它，
// CLI 与未来的平台壳只面对这一层，切换本地/远端无需改调用方。
type Controller interface {
	Input(text string) (Composition, error)
	Backspace() (Composition, error)
	Reset() (Composition, error)
	Select(index int) (string, error)
}

const (
	maxInputRunes     = 64
	maxSyllableLength = 6
	maxPathsPerChunk  = 16
	maxSegmentations  = 64
	maxCandidates     = 9
	userBoostPerCount = 500
)

// Session 是一次连续输入的组词会话。不保证并发安全：
// IPC 服务端为每个连接建独立 Session，本地使用方自行串行化。
type Session struct {
	dict  *Dictionary
	user  *UserDict
	input string // 仅小写字母与 '
}

func NewSession(dict *Dictionary, user *UserDict) *Session {
	return &Session{dict: dict, user: user}
}

// Input 追加输入。非小写字母与 ' 的字符被过滤（阶段 0 无符号处理，
// 标点经平台壳直接上屏，不进引擎）。
func (s *Session) Input(text string) (Composition, error) {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if (r >= 'a' && r <= 'z') || r == '\'' {
			b.WriteRune(r)
		}
	}
	s.input += b.String()
	s.truncate()
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
	return s.Compose(), nil
}

// Reset 清空当前组词。
func (s *Session) Reset() (Composition, error) {
	s.input = ""
	return s.Compose(), nil
}

// Select 选中候选（0 起）并「上屏」：记入用户词典、清空组词、返回候选文本。
func (s *Session) Select(index int) (string, error) {
	comp := s.Compose()
	if index < 0 || index >= len(comp.Candidates) {
		return "", fmt.Errorf("候选序号 %d 越界（当前共 %d 个候选）", index+1, len(comp.Candidates))
	}
	c := comp.Candidates[index]
	if s.user != nil {
		if err := s.user.Bump(c.Text, strings.Split(c.Syllables, "'")); err != nil {
			return "", fmt.Errorf("记录用户词频失败: %w", err)
		}
	}
	s.input = ""
	return c.Text, nil
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

// Compose 基于当前输入生成组词状态：枚举全部音节切分、查词典、
// 合并去重、按「基础词频 + 用户上屏加权」排序。
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
	for _, sylls := range paths {
		var entries []*Entry
		if frag == "" {
			entries = s.dict.Query(sylls)
		} else {
			entries = s.dict.QueryPrefix(sylls, frag)
		}
		for _, e := range entries {
			count := 0
			if s.user != nil {
				count = s.user.Count(e.Word, e.Syllables)
			}
			score := e.Freq + count*userBoostPerCount
			key := e.Word + "\x00" + strings.Join(e.Syllables, " ")
			if prev, ok := seen[key]; ok {
				if score > merged[prev].Score {
					merged[prev].Score = score
					merged[prev].FromUser = count > 0
				}
				continue
			}
			seen[key] = len(merged)
			merged = append(merged, Candidate{
				Text:      e.Word,
				Syllables: strings.Join(e.Syllables, "'"),
				Score:     score,
				FromUser:  count > 0,
			})
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
	if len(merged) > maxCandidates {
		merged = merged[:maxCandidates]
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
	comp.Candidates = merged
	return comp
}
