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

	// rank 与 merged 平行，记录候选的匹配层级（0 最高）。层级只用于排序，
	// 语义见下方排序注释。
	var merged []Candidate
	var rank []int
	seen := make(map[string]int)
	// addCandidate 合并一个候选。r 是本次匹配的层级，同一候选多次命中取最优。
	// 合并时 Score 取大、rank 取小，保证「更贴近用户输入」的那次匹配胜出。
	addCandidate := func(e *Entry, score int, fromUser bool, r int) {
		word := e.Word
		if s.traditional {
			word = ToTraditional(word) // 候选显示与上屏均为繁体
		}
		key := word + "\x00" + strings.Join(e.Syllables, " ")
		if prev, ok := seen[key]; ok {
			if score > merged[prev].Score {
				merged[prev].Score = score
				merged[prev].FromUser = fromUser
			} else if fromUser {
				merged[prev].FromUser = true
			}
			if r < rank[prev] {
				rank[prev] = r
			}
			return
		}
		seen[key] = len(merged)
		merged = append(merged, Candidate{
			Text:      word,
			Syllables: strings.Join(e.Syllables, "'"),
			Score:     score,
			FromUser:  fromUser,
		})
		rank = append(rank, r)
	}
	// 匹配层级（数字越小越优先）：
	//   rankExact    —— 单词完全匹配（打满 jingchang 命中「经常」）
	//   rankSentence —— 多词 DP 拼接复合候选（打 womende 命中「我们的」）
	//   rankPartial  —— 末段残缺音节匹配成功（打 shix 时的「史学」）
	//   rankPrefix   —— 偏长词的续接（打 jing 时的「经济」）
	//   rankBackoff  —— 末段拼音打错了/无法续接，退回到「已完整音节」本身的候选
	//                   （打 nihaox 非但不出候选，还应能打出「你好」）
	const (
		rankExact = iota
		rankSentence
		rankPartial
		rankPrefix
		rankBackoff
	)
	for _, sylls := range paths {
		// 空路径（整段都无法切分成合法音节，如 zzz）不查询：
		// Query([]) 会从 Trie 根返回全部词条，等于把整本词库当候选吐出来。
		if len(sylls) == 0 && frag == "" {
			continue
		}
		// 一条切分路径先按原样查；如果连一个候选都查不到，说明用户多打的
		// 字母凑成了合法音节却组不成词（zhongguoo 的末段 'o'）：此时逐级
		// 去掉尾部音节再查，让「中国」这类已完整的前缀仍有候选。
		// 逐级最长保留 8 个音节，避免长输入退化成大量查询。
		hit := 0
		for _, qp := range queryVariants(sylls, frag, s.fuzzy) {
			if frag == "" {
				for _, e := range s.dict.Query(qp) {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					r := rankPrefix
					if len(e.Syllables) == len(qp) {
						r = rankExact
					}
					addCandidate(e, e.Freq+boost, boost > 0, r)
					hit++
				}
				if len(qp) >= 2 {
					for _, sc := range FindSentenceCandidates(s.dict, s.user, qp, 6) {
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
							hit++
						}
					}
				}
				// 为多音节输入叠加「逐级回退前缀」（例如输入 womende 时不仅出「我们的」，
				// 也把已完整的「我们」加入候选；打 zhongguoo 时也包含「中国」）
				limit := len(sylls) - 1
				if limit > 8 {
					limit = 8
				}
				for drop := 1; drop <= limit; drop++ {
					shorter := sylls[:len(sylls)-drop]
					if len(shorter) == 0 {
						break
					}
					for _, sqp := range queryVariants(shorter, "", s.fuzzy) {
						if len(sqp) == 0 {
							continue
						}
						for _, e := range s.dict.Query(sqp) {
							if len(e.Syllables) == len(sqp) {
								boost := 0
								if s.user != nil {
									boost = s.user.Score(e.Word, e.Syllables, s.prev)
								}
								addCandidate(e, e.Freq+boost, boost > 0, rankBackoff+drop)
							}
						}
					}
					if s.user != nil {
						for _, e := range s.user.QueryPhrases(shorter, "") {
							if len(e.Syllables) == len(shorter) {
								addCandidate(e, e.Freq, true, rankBackoff+drop)
							}
						}
					}
				}
				continue
			}
			// 末段有残缺音节：先查「下一个音节以 frag 开头」的续接候选。
			// 当 len(qp)==0 且 frag!="" 时（如用户刚输入首字母 'n'/'c'/'w'），
			// QueryPrefix([], frag) 从根节点筛选以该字母开头的音节（合法音节前缀），
			// 乱码输入（如 zzz）因无任何音节以此为前缀自然返回空。
			if len(qp) > 0 || frag != "" {
				for _, e := range s.dict.QueryPrefix(qp, frag) {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					addCandidate(e, e.Freq+boost, boost > 0, rankPartial)
					hit++
				}
			}
			// 再叠加「音节在 qp 处正好结束」的候选（必须 len(qp)>0，避免空序列查根节点吐出整库）：
			// 用户多打了一个（或几个）对不上的字母时，仍应能打出前面已经完整的部分（nihaox → 你好）。
			// 这类候选排在 rankPartial 之后，不影响正常续接匹配的顺序。
			if len(qp) > 0 {
				for _, e := range s.dict.Query(qp) {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					addCandidate(e, e.Freq+boost, boost > 0, rankBackoff)
					hit++
				}
			}
		}
		if hit == 0 {
			// 整条路径无候选：
			// 1. 若已有 2 个及以上完整音节（如 nizaig 中的 nizai），先用 DP 生成前缀复合短语（如「你在」）
			if len(sylls) >= 2 {
				for _, sc := range FindSentenceCandidates(s.dict, s.user, sylls, 6) {
					avgFreq := sc.Score / len(sc.Syllables)
					if avgFreq > 99 {
						avgFreq = 99
					}
					e := &Entry{
						Word:      sc.Text,
						Syllables: sc.Syllables,
						Freq:      avgFreq,
					}
					addCandidate(e, avgFreq, false, rankBackoff)
				}
			}
			// 2. 逐级去掉尾部音节重查（最多 8 级）。
			// 触发条件限定为「多打了对不上的音节或残缺音节」：
			// 至少保留一个完整音节（len(sylls) >= 2），单音节查不到就是查不到。
			limit := len(sylls) - 1
			if limit > 8 {
				limit = 8
			}
			for drop := 1; drop <= limit; drop++ {
				shorter := sylls[:len(sylls)-drop]
				if len(shorter) == 0 {
					break
				}
				found := 0
				for _, qp := range queryVariants(shorter, "", s.fuzzy) {
					if len(qp) == 0 {
						continue
					}
					if frag != "" {
						for _, e := range s.dict.QueryPrefix(qp, frag) {
							boost := 0
							if s.user != nil {
								boost = s.user.Score(e.Word, e.Syllables, s.prev)
							}
							addCandidate(e, e.Freq+boost, boost > 0, rankBackoff+drop)
							found++
						}
					}
					for _, e := range s.dict.Query(qp) {
						boost := 0
						if s.user != nil {
							boost = s.user.Score(e.Word, e.Syllables, s.prev)
						}
						addCandidate(e, e.Freq+boost, boost > 0, rankBackoff+drop)
						found++
					}
				}
				if s.user != nil {
					if frag != "" {
						for _, e := range s.user.QueryPhrases(shorter, frag) {
							addCandidate(e, e.Freq, true, rankBackoff+drop)
							found++
						}
					}
					for _, e := range s.user.QueryPhrases(shorter, "") {
						addCandidate(e, e.Freq, true, rankBackoff+drop)
						found++
					}
				}
				if found > 0 {
					break // 找到最近一级可用前缀即止，避免把更短的前缀也塞进来
				}
			}
		}
		if s.user != nil {
			// 用户词组：完整匹配优先，其次是末段续接，最后是回退到前缀本身。
			for _, e := range s.user.QueryPhrases(sylls, frag) {
				r := rankExact
				if frag != "" {
					r = rankPartial
					if len(e.Syllables) == len(sylls) {
						r = rankBackoff
					}
				}
				addCandidate(e, e.Freq, true, r)
			}
			if frag != "" {
				for _, e := range s.user.QueryPhrases(sylls, "") {
					addCandidate(e, e.Freq, true, rankBackoff)
				}
			}
		}
	}
	// 排序（稳定，保证同分同长时结果可复现）。优先级：
	//   ① 匹配了完整音节（精确匹配/整句候选）的候选永远优先于丢弃尾部音节的回退候选；
	//   ② 用户数据加权（调频/上下文）——一次上屏 +500，必须压过静态词频；
	//   ③ 匹配层级：完全匹配 > 整句拼接 > 末段续接 > 偏长前缀 > 回退到已完整前缀。
	//   ④ 得分（基础词频 + 用户加成）、词长、字典序。
	sort.SliceStable(merged, func(i, j int) bool {
		a, b := merged[i], merged[j]
		aBackoff := rank[i] >= rankBackoff
		bBackoff := rank[j] >= rankBackoff
		if aBackoff != bBackoff {
			return !aBackoff
		}
		if a.FromUser != b.FromUser {
			return a.FromUser
		}
		if rank[i] != rank[j] {
			return rank[i] < rank[j]
		}
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
