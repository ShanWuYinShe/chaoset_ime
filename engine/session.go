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
	// typoFix 标记该候选来自「错拼修正」：compose 用 typoCorrections 对**整串**
	// 输入做单次编辑修正后查询得到。它只服务 SelectPrefix 的消费语义——修正
	// 覆盖的是整串输入，选中这类候选时修正没覆盖到的碎片不能当剩余拼音回灌
	// （lianxiagn 选「联想」残留 xiagn，2026-11-01 用户反馈）。
	// 未导出且不进 JSON：三端壳按序号选择、不需要也不应感知该字段。
	// 位置刻意紧挨 FromUser（Segments 之前）：两个 bool 共用对齐 padding，
	// Candidate 结构体大小保持 72 字节不变——候选切片在排序里反复交换复制，
	// 一个标记字段不能让它变大（否则每次组词都多付一份内存带宽）。
	typoFix bool
	// Segments 仅「整句式」候选携带：用户确认过的分段词文本（来自学习记录
	// Words 的 Segs）。壳不需要它；引擎用它把「整句候选上屏」按逐段确认的
	// 等价路径学习。
	Segments []string `json:"segments,omitempty"`
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
	// Shuangpin 是当前会话启用的双拼方案名（zrm/mspy/flypy），全拼为空。
	// 三端壳据此把 ';' 键路由到 input() 而不是 punct()：mspy 的 ing 韵母键
	// 正是 ';'，壳若一律当标点，明/听/零/星/京整类音节永远打不出，还会
	// 误上屏中文「；」。壳从任意一次响应缓存该字段即可。
	Shuangpin string `json:"shuangpin,omitempty"`
	// Committed 是本次调用额外上屏、需要壳写入文档的文本（空 = 无）。
	// 两个来源：① SelectPrefix 渐进上屏的前缀段；② 双拼模式下 dpPending
	// 为 0 时按下的 ';' 标点键（组词中先上屏首选词再上屏标点，壳不能自己
	// 把 ';' 当标点，否则首选词丢字）。壳据此写文档并用 Preedit（剩余
	// 拼音）恢复预编辑。
	Committed string `json:"committed,omitempty"`
}

// Controller 是会话的统一操作接口。本地 Session 与 IPC Client 都实现它，
// CLI 与未来的平台壳只面对这一层，切换本地/远端无需改调用方。
type Controller interface {
	// Input 追加输入。halfWidth 是壳的全/半角标点状态，只对本次调用生效
	// （引擎不保存该状态——壳是唯一真源，见 Session 注释）。
	Input(text string, halfWidth bool) (Composition, error)
	Backspace() (Composition, error)
	// BackspaceWord 逐词/逐音节回退：删除末尾的一个音节或词段。
	BackspaceWord() (Composition, error)
	// Reset 清空组词**与**学习上下文（prev/history），对应 Esc/失焦/新会话。
	Reset() (Composition, error)
	// ResetComposition 只丢弃当前组词、保留学习上下文：
	// 切中英/透传等「用户还会继续打字」的场景用它。
	ResetComposition() (Composition, error)
	Select(index int) (string, error)
	// SelectPrefix 渐进上屏：选中候选并只消耗其音节对应的输入前缀，
	// 剩余输入回灌继续组词（Composition.Committed 为本次上屏文本）。
	SelectPrefix(index int) (Composition, string, error)
	// Punct 提交一个标点键：组词中先上屏首选候选（无候选的残缺输入
	// 丢弃），随后输出映射后的中文标点；空组词直接输出中文标点。
	Punct(key string, halfWidth bool) (string, error)
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

	// maxAssociate 是联想候选的条数上限（**三端壳本地分页展示**，每页
	// pageSize 条）。2026-10-28 联想翻页化之前「必须与 pageSize 一致」——
	// 单页展示时超出的条数永远选不到；翻页接入后该约束解除：壳按页切片
	// 展示，数字键选当前页内的候选，翻页键在联想中切换页。排序不变
	// （词频 + 用户共现），只是从「取 6 条」变为「取 30 条分 5 页」。
	maxAssociate = 30

	// learnThreshold：相邻两词共现多少次后固化为用户词组（词组进化）。
	learnThreshold = 2
	// maxLearnSylls：可固化词组的最大音节数，避免学出无意义长串。
	maxLearnSylls = 4
	// maxHistory 限制参与学习观察的上屏历史长度。
	maxHistory = 8
	// maxSentenceSylls 是「整句式输入习惯」可学习的最长音节数。整句学习
	// 只服务完整输入的整句候选（2026-10-30 用户需求），超过该长度的输入
	// 已是连打/误触而非习惯词，不记；5 音节「今天星期一」、9 音节
	// 「今天天气真好」都在范围内。
	maxSentenceSylls = 12
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
	dict  *Dictionary
	user  *UserDict
	input string // 仅小写字母与 '
	prev  string
	// prevSylls 是 prev 上屏时的**音节序列**（引擎自己记录的权威值）。
	// 联想剥前缀（Associate）必须按它切，不能按壳传进来的音节数组长度切：
	// Linux/Windows 壳的渐进上屏收尾把「上屏词文本」当音节传
	// （enterAssociate(rest.committed)），若单字符词被当成 1 个音节，
	// 「测试→题」就会被剥成「题」=shí'tí（多字词全部错位），学习数据随之
	// 污染。macOS 壳按候选音节传，一直是对的。2026-10-29 修复。
	prevSylls []string
	history   []learnEntry
	// seqSegs 记录本次组词中已「渐进上屏」的各段（词+音节）。整串耗尽时
	// 拼成整句习惯写入用户词库（见 learnProgressiveSequence）；普通
	// 一次上屏清空组词时不会用到。
	seqSegs    []learnEntry
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

	// 注意：**没有 halfWidth 字段**。全/半角是壳的 UI 开关，唯一真源在壳
	// （macOS/Windows/Linux 各自本地持有），引擎每处理一次请求就接收一次该
	// 参数并按它产出文本，不保存任何副本（2026-11-05 架构收敛）。
	//
	// 为什么删干净而不是留镜像：存态就必须“同步”，而同步总有漏的路径——
	// 旧实现的 SetHalfWidth 只在 punct/input 请求里被调用，任何一次未携带
	// 该字段的请求都会让 daemon 用上一轮残留值出标点（2026-10-30 记录的
	// 「刚切全/半角就单按 ';' 按旧宽度出标点」1 键瞬态）。纯参数化后这种
	// 不同步在结构上不可能发生。

	traditional bool // 繁体输出：候选词经简繁映射（默认关闭，见 s2t.go）

	dpScheme  string // 双拼方案（zrm/mspy/flypy，见 double_pinyin.go；空 = 关闭）
	dpPending rune   // 双拼首键（等待第二键成对解码；0 = 无）
	dpLens    []int  // 每次「按键事件」实际追加到 input 的字符数（退格回滚用）
}

// SetDoublePinyin 设置双拼方案（DPSchemeZRM / DPSchemeMSPY；
// 空串关闭，未知方案按关闭处理）。须在首次输入前设置。
func (s *Session) SetDoublePinyin(scheme string) {
	if scheme == "" {
		s.dpScheme = ""
	} else if _, ok := dpSchemes[scheme]; ok {
		s.dpScheme = scheme
	} else {
		return // 未知方案按关闭处理（不改当前状态）
	}
	// 方案名会随每次 Compose 下发（Composition.Shuangpin），且决定 ';' 键
	// 的路由语义：切换后旧记忆化结果必须作废，否则壳会继续按旧方案路由。
	s.invalidateComposeMemo()
}

// SetFuzzy 开关模糊音（规则表见 fuzzy.go）。须在首次查询前设置。
func (s *Session) SetFuzzy(on bool) {
	s.fuzzy = on
	s.invalidateComposeMemo()
}

// SetTraditional 开关繁体输出（零错误单字映射，见 s2t.go）。
// 须在首次查询前设置。
func (s *Session) SetTraditional(on bool) {
	s.traditional = on
	s.invalidateComposeMemo()
}

// invalidateComposeMemo 作废 Compose 的记忆化结果。
//
// Compose 的缓存 key 是 input + page：输入变化与翻页会自然换 key，但
// fuzzy / traditional / 双拼方案只改变**查询与输出语义**、不改变 key，
// 必须显式作废——否则同一 input+page 下切换配置后仍返回旧结果（切繁体还是
// 简体、模糊音开关不生效、双拼方案名不随响应下发）。2026-11-08 GO-02 修复：
// 此前只有 SetDoublePinyin 清缓存，SetFuzzy/SetTraditional 漏清。
func (s *Session) invalidateComposeMemo() {
	s.composeMemoKey = ""
	s.composeMemo = Composition{}
}

func NewSession(dict *Dictionary, user *UserDict) *Session {
	return &Session{dict: dict, user: user}
}

// Input 追加输入。非小写字母与 ' 的字符被过滤；标点键走 Punct
// （引擎侧统一映射为中文标点，壳只负责转发标点键）。
//
// 双拼模式下按键两两成对解码为音节（见 double_pinyin.go）：首键暂存不产生
// 组成变化，无效键码对整体丢弃。双拼模式额外接受 ';'（mspy 的 ing 韵母键）：
//   - 首键暂存中：';' 与字母一样参与成对解码（如 ling 的键码 l;）；
//   - 首键为空：';' 退回标点身份，按 Punct(";") 语义处理（组词中先上屏
//     首选词再加「；」），上屏文本经返回的 Composition.Committed 交给壳写
//     文档。壳不得自己把 ';' 当标点，否则组词中的首选词永远上不了屏。
//
// 全拼模式行为不变（';' 被过滤，标点由壳走 Punct 请求）。
// Input 追加输入。halfWidth 是壳的全/半角标点状态，只对本次调用生效
// （2026-11-05：改为显式参数，引擎不再保存该状态——壳是唯一真源）。
// 原因：双拼模式下 dpPending==0 的 ';' 会走 Punct 语义产生标点，必须用
// 本次请求的宽度；存态方案在“某次请求没带该字段”时会用残留值出标点。
func (s *Session) Input(text string, halfWidth bool) (Composition, error) {
	// 从空组词重新开始：上一轮渐进上屏的未完成分段不能带入本轮组词
	// （否则退格到空后再打新词，会被拼成伪整句习惯，见 resetSeqIfEmpty）。
	if s.input == "" {
		s.seqSegs = nil
	}
	for _, r := range strings.ToLower(text) {
		if s.dpScheme != "" {
			switch {
			case r == '\'':
				s.input += "'"
				s.dpLens = append(s.dpLens, 1)
				s.afterAppend()
			case r == ';' && s.dpPending == 0:
				// 没有暂存首键时 ';' 只能是标点：走完整 Punct 语义（含半角、
				// 同键循环与「组词中先上屏首选」），把上屏文本放进 Committed。
				// 不处理同一 text 中 ';' 之后的字符：Punct 已结束本轮组词，
				// 壳按单键转发 ';'，批量文本的后续内容不属于本次上屏。
				// 宽度用本次请求的 halfWidth：双拼 ';' 的标点必须与壳开关一致。
				out, err := s.Punct(";", halfWidth)
				if err != nil {
					return s.Compose(), err
				}
				comp := s.Compose()
				comp.Committed = out
				return comp, nil
			case (r >= 'a' && r <= 'z') || r == ';':
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
	defer s.resetSeqIfEmpty() // 退格到空 = 本轮组词中止，丢弃未完成分段
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
	defer s.resetSeqIfEmpty() // 同 Backspace：清空组词即中止本轮渐进段累积
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

// ResetComposition 只丢弃**当前组词**，保留学习上下文（prev/history）。
//
// 这是壳在「输入流被打断但会话仍在继续」时应当调用的方法：
//   - 切换中英模式：用户要继续打字，上一轮上屏的 prev/bigram 仍然有效，
//     清掉会让紧接着的候选打分丢失上下文（实测可选：学习过「我们→时」后
//     输入 shi，带 prev 首候选是「时」，清掉后退回「是」）；
//   - 光标/文档变化等在**同一会话内**的收尾。
//
// 与 Reset 的区别只有一条：不清 prev/prevSylls/history/seqSegs。
// 清掉它们等于让引擎“忘记”用户刚打的字，那是失焦/新会话才该发生的事。
func (s *Session) ResetComposition() (Composition, error) {
	s.input = ""
	s.invalidateComposeMemo()
	s.page = 0
	s.resetPunctCycle()
	s.dpPending = 0
	s.dpLens = nil
	return s.Compose(), nil
}

// Reset 清空当前组词**与**学习上下文（对应 Esc / 失焦：输入流被切断后，
// 相邻共现关系不再成立，prev/bigram 不应再影响后续打分）。
//
// 只想丢弃组词、保留上下文请用 ResetComposition。
func (s *Session) Reset() (Composition, error) {
	s.prev = ""
	s.prevSylls = nil
	s.history = nil
	s.seqSegs = nil
	return s.ResetComposition()
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
// 清空组词并返回候选文本。候选若带分段（一次选中的整句复合词），学习按
// 「整句式」记录（见 learnSelection）。
func (s *Session) Select(index int) (string, error) {
	comp := s.Compose()
	if index < 0 || index >= len(comp.Candidates) {
		return "", fmt.Errorf("候选序号 %d 越界（当前共 %d 个候选）", index+1, len(comp.Candidates))
	}
	c := comp.Candidates[index]
	sylls := strings.Split(c.Syllables, "'")
	text := c.Text
	s.learnSelection(text, sylls, c.Segments)
	s.seqSegs = nil
	s.input = ""
	s.resetPunctCycle()
	return text, nil
}

// learnSelection 记录一次上屏的学习数据：词频、与上一上屏词的 Bigram、
// history 与词组进化。Select / SelectPrefix / AssociateSelect 共用。
//
// segs 是「整句式上屏」时用户确认过的分段词文本（逐段渐进拼出的整串，
// 或一次选中的整句候选自带的分段），普通单词为 nil。整句式记录带分段：
// 完整输入时作为整句候选参与调频加权；**部分输入不给整词候选、联想也只在
// 分段边界剥前缀**——否则「学过 今天星期一，输入 今 就给 天星期一」会把
// 用户真正想打的「今天」挤掉（2026-10-30 用户明确要求：不能直接匹配整个词）。
func (s *Session) learnSelection(text string, sylls, segs []string) {
	// 学习数据的写入失败**绝不能阻断上屏**。
	//
	// 输入法的铁律是「用户按了键就必须出字」。旧实现把 BumpWord/BumpBigram 的
	// 错误返回给调用方，于是磁盘满、路径只读、权限变更等任何落盘故障都会让
	// 这次上屏彻底失败——壳已经把按键消费掉了，用户看到的就是「按数字键毫无
	// 反应，字也没进文档」（2026-10-26 复现确认）。学习是增量优化，失败只应
	// 记录而不影响组词结果；下一次 flush 成功时会自动补上。
	sentence := s.isSentenceSelection(text, sylls, segs)
	if s.user != nil {
		if sentence {
			_ = s.user.BumpWordWithSegs(text, sylls, segs)
		} else {
			_ = s.user.BumpWord(text, sylls)
			if s.prev != "" {
				_ = s.user.BumpBigram(s.prev, text)
			}
		}
	}
	if sentence {
		// 整句一键上屏：按「用户逐段确认」的等价路径再学一遍分段词与相邻
		// 共现。这样一键整句与逐段渐进选择落下同一套习惯数据，后续打
		// jintian 时「今天」同样会被调频加权，两条路径的学习不分叉。
		parts := splitSyllsBySegs(sylls, segs)
		prevWord := s.prev
		for i, seg := range segs {
			segSylls := parts[i]
			if s.user != nil && seg != "" && len(segSylls) > 0 {
				_ = s.user.BumpWord(seg, segSylls)
				if prevWord != "" {
					_ = s.user.BumpBigram(prevWord, seg)
				}
			}
			s.history = append(s.history, learnEntry{word: seg, sylls: segSylls})
			if len(s.history) > maxHistory {
				s.history = s.history[len(s.history)-maxHistory:]
			}
			if s.user != nil {
				// 词组学习失败不阻断上屏（数据已落盘，学习是增量优化）
				s.learnPhrase()
			}
			prevWord = seg
		}
	} else {
		s.history = append(s.history, learnEntry{word: text, sylls: sylls})
		if len(s.history) > maxHistory {
			s.history = s.history[len(s.history)-maxHistory:]
		}
		if s.user != nil {
			s.learnPhrase()
		}
	}
	s.prev = text
	s.prevSylls = sylls
}

// SelectPrefix 渐进上屏：选中第 index 个候选，上屏其文本并只消耗其音节
// 对应的 input 前缀，**剩余 input 自动回灌继续组词**——返回剩余组词态
// （Composition）供壳刷新候选；已上屏文本经 committed 返回给壳上屏。
//
// 2026-10-29 用户场景：打 jintianwochi… 长串，候选 1 是「今天」——选中
// 后只消耗 jin'tian，剩余 wo'chi'le'yi'da'wan 继续弹「吃了一大碗」相关
// 候选，逐段渐进拼出整句；每个上屏段都进用户词库学习（吸收用户习惯）。
//
// 2026-10-30 两个补强：
//   - 输入带音节分隔符（jin'tian'…）时也能渐进——比较时忽略 '，剩余串
//     不会以分隔符开头（此前 HasPrefix 恒不匹配、整串被清空）；
//   - 整串由 >=2 段拼完时把整句记成带分段的整句习惯，下次完整输入同一串
//     直接给出整句候选（见 learnProgressiveSequence）。
//
// 候选音节是标准拼写，input 可能是省写/别名/模糊形态（xve→xue、jve→jue、
// shuei→shui、-fuzzy 下 zheng 命中 zen），也可能是声母（kkdz）或没打完的
// 拼音前缀（zhongg 的 g、gu→guo）：消费时逐音节匹配，能匹配多少消耗多少
// ——部分匹配的剩余输入继续组词（打 xveshengwomen 选「学生」不能丢 women；
// kkdz 选「看看」只吃 k'k，剩 d'z）。只有候选与输入一个音节都对不上时
// （整句/简拼展开形态差异）才回退旧语义：整句上屏、input 全清。
func (s *Session) SelectPrefix(index int) (comp Composition, committed string, err error) {
	c := s.Compose()
	if index < 0 || index >= len(c.Candidates) {
		return Composition{}, "", fmt.Errorf("候选序号 %d 越界（当前共 %d 个候选）", index+1, len(c.Candidates))
	}
	cand := c.Candidates[index]
	committed = cand.Text
	sylls := strings.Split(cand.Syllables, "'")
	rest, matched := consumeInputPrefix(s.input, sylls, s.fuzzy)
	if matched == 0 {
		rest = "" // 一个音节都对不上（整句/简拼展开形态差异）：回退全清
	} else if cand.typoFix {
		// 错拼修正候选把**整串**输入解释成了候选音节（lianxiagn → lian'xiang）：
		// 标准拼写与用户敲的串逐音节消费只能对上前面一部分（lian），剩下的
		// xiagn / jisuanjii 的尾字母 i 都是被修正掉/多敲的碎片、不是待继续组词
		// 的拼音——原样回灌会弹一堆无关候选（用户截图：选「联想」后剩 xiagn
		// 又出 想/向/现/相…）。
		//
		// 标记只打在**纯错拼来源**的候选上（见 compose 里的取消标记段）：常规
		// 切分路径也能命中的候选不算，合法混拼的部分消费（chifanl 选「吃饭」
		// 剩 l、xveshengwomen 选「学生」剩 women、nihaox 选「你好」剩 x）语义不变。
		rest = ""
	}

	s.learnSelection(committed, sylls, cand.Segments)
	s.prev = committed
	s.input = rest
	// 组词内容变了，回到第一页（与 afterAppend 一致）：否则用户在第 N 页
	// 选段后，剩余拼音会停在原页码，首屏候选被跳过、页码显示错乱。
	s.page = 0
	if rest == "" {
		// 本次组词由若干渐进上屏段拼完：把整串固化成整句习惯
		// （分段 = 各段上屏词）。单段选完整个输入不触发（不是整句）。
		if len(s.seqSegs) > 0 {
			all := append(append([]learnEntry(nil), s.seqSegs...), learnEntry{word: committed, sylls: sylls})
			s.learnProgressiveSequence(all)
			s.seqSegs = nil
		}
	} else {
		s.seqSegs = append(s.seqSegs, learnEntry{word: committed, sylls: sylls})
	}
	s.resetPunctCycle()
	comp = s.Compose()
	comp.Committed = committed
	return comp, committed, nil
}

// Associate 返回「以上屏词音节序列开头」的联想候选：用户选词上屏后拼音区
// 已清空，候选窗继续展示的续接词组（如上屏「现在」→ 联想「现在时间」）。
// 纯查询，不改会话状态；返回上限 maxAssociate 条（壳本地分页展示）。
//
// 2026-10-28 语义修正：续接按**上屏词的字**（s.prev）优先——同音不同字
// （测试/策士）上屏后的续接候选应当跟着字走：选「测试」弹「测试题/
// 测试员」，选「策士」弹「策士团」，而不是两者弹同一批 ce'shi 开头的
// 词（用户反馈「联想不能只靠拼音，要跟字对应起来」）。字前缀层：词典
// QueryWordPrefix + 用户词库 Phrases/Words，按词频与共现分排序。
//
// 2026-10-30 整句安全约束：「整句式」学习记录（今天星期一 / 今天天气）只在
// 用户确认过的**分段边界**剥前缀——上屏「今天」给出「星期一」，上屏「今」
// 不给出「天星期一」。否则学过整句后联想首条会变成整词尾巴（探针实测
// 天星期一 score=500 顶到首位），把用户真正想要的「今天」路径挤掉
// （用户明确要求：不能直接匹配整个词）。
func (s *Session) Associate(sylls []string) []Candidate {
	if s.dict == nil || len(sylls) == 0 {
		return nil
	}
	// 剥前缀的切分基准**一律取引擎自己记录的上屏音节**（s.prevSylls），
	// 调用方传的 sylls 只作兜底：壳传来的数组长度不可信——Linux/Windows
	// 壳的渐进上屏收尾把「上屏词文本」当音节传
	// （enterAssociate(rest.committed)），单字「题」会被当成 1 个音节，
	// 于是多字词（测试→题）剥错位成 shí'tí，学习数据跟着污染
	// （2026-10-29 实测：Associate(["shi"]) 返回的「题」音节为 "shi'ti"，
	// 而正确值是 "ti"）。引擎知道自己上屏了什么，就该用自己的记录。
	prevSylls := s.prevSylls
	if len(prevSylls) == 0 {
		prevSylls = sylls
	}
	type assocItem struct {
		cand   Candidate
		byWord bool // 字前缀层（同分时排在音节前缀层之前）
	}
	var out []assocItem
	seen := map[string]bool{}
	// add 处理一条字前缀命中候选。learned 表示来自用户学习记录
	// （Phrases/Words 表），否则来自主词典；只有学习记录才做整句分段边界校验。
	add := func(e *Entry, byWord, learned bool) {
		key := userKey(e.Word, e.Syllables)
		if seen[key] {
			return
		}
		seen[key] = true
		text, syllStr := e.Word, strings.Join(e.Syllables, "'")
		if byWord && s.prev != "" && strings.HasPrefix(e.Word, s.prev) && len(e.Syllables) > len(prevSylls) {
			if learned {
				if segs := s.entrySegs(e); len(segs) >= 2 {
					var ok bool
					text, syllStr, ok = s.stripAtSegmentBoundary(e, segs, s.prev, prevSylls)
					if !ok {
						return // 不在用户确认过的分段边界：不提供整词尾巴
					}
				} else {
					text = e.Word[len(s.prev):]
					syllStr = strings.Join(e.Syllables[len(prevSylls):], "'")
				}
			} else {
				text = e.Word[len(s.prev):]
				syllStr = strings.Join(e.Syllables[len(prevSylls):], "'")
			}
			if text == "" {
				return
			}
		}
		out = append(out, assocItem{
			cand: Candidate{
				Text:      text,
				Syllables: syllStr,
				Score:     e.Freq,
			},
			byWord: byWord,
		})
	}

	// 字前缀层：s.prev 是刚上屏的词（Select/AssociateSelect 设置）。
	// 收集上限 200（按字统计的最长尾也覆盖），不要传 maxAssociate——
	// 内部按词频截断会把 Bigram 加成候选裁掉（好家伙 41 分排 31+，
	// 加成后的 541 分根本进不了列表，2026-10-28 探针实测）。
	if s.prev != "" {
		for _, e := range s.dict.QueryWordPrefix(s.prev, 200) {
			add(e, true, false)
		}
		if s.user != nil {
			for _, e := range s.user.QueryPhrasesByWordPrefix(s.prev) {
				add(e, true, true)
			}
			// 用户调频词条（Words 表）：整句上屏/选词的记录都在这里，
			// 是「之前输入过的词」的主体——漏查它，用户打过的词就不会
			// 出现在联想候选（2026-10-28 用户反馈）。
			for _, e := range s.user.QueryWordsByWordPrefix(s.prev) {
				add(e, true, true)
			}
		}
	}
	// 2026-10-28 二次收紧：**严格按字**——不再回退到音节前缀层。回退层
	// 会混入同音其它字的续接词（选「测试」也弹「策士团」），用户反馈
	// 「同样的拼音不同的字却联想一样的候选词」正是这个观感。字前缀没有
	// 续接词时不弹联想（与市面输入法的「词组续接」语义一致）。
	if s.user != nil {
		// 上下文加成：user.Score 含调频与「上屏词 → 联想词」共现（Bigram），
		// 用户常打的联想词（如反复输入「现在的」跟随「现在」）排到前面。
		for i := range out {
			syllsList := strings.Split(out[i].cand.Syllables, "'")
			out[i].cand.Score += s.user.Score(out[i].cand.Text, syllsList, s.prev)
		}
	}
	// 排序：字前缀层优先（同音不同字的续接跟着字走），层内按加成后得分
	// 降序稳定排序（毫米 64 不应排在 豪快 9 之后）。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].byWord != out[j].byWord {
			return out[i].byWord
		}
		if out[i].cand.Score != out[j].cand.Score {
			return out[i].cand.Score > out[j].cand.Score
		}
		return out[i].cand.Text < out[j].cand.Text
	})
	if len(out) > maxAssociate {
		out = out[:maxAssociate]
	}
	result := make([]Candidate, len(out))
	for i, it := range out {
		result[i] = it.cand
	}
	return result
}

// AssociateSelect 联想候选直接上屏：不需要拼音组词状态，学习路径与
// Select 完全一致（调频、上下文共现、词组进化），保证「越用越顺手」
// 对联想上屏同样成立。
//
// 联想增量词一般是普通词（的话/题）；万一收到多段拼成的增量长词（旧学习
// 数据兼容），按整句式记录分段，避免把长尾巴写成不带分段的普通整词。
func (s *Session) AssociateSelect(word string, sylls []string) (string, error) {
	if word == "" || len(sylls) == 0 {
		return "", fmt.Errorf("联想上屏参数为空")
	}
	s.learnSelection(word, sylls, s.inferSegs(word, sylls))
	s.seqSegs = nil
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
	// 记录分段（进化时的前后两个上屏词）：联想剥前缀只允许在这些边界发生，
	// 否则学过「今天天气」后上屏「今」会得到「天天气」（2026-10-30 用户要求）。
	_ = s.user.BumpPhraseWithSegs(word, sylls, []string{a.word, b.word})
}

func isInitial(s string) bool {
	if s == "zh" || s == "ch" || s == "sh" {
		return true
	}
	if len(s) == 1 {
		b := s[0]
		// v 是 ü 的键盘写法（nv/lve），单独作为声母没有音节匹配，但保留在
		// 切分里可让 zxcv 这类串走常规回退拿到前面声母的单字候选，而不是
		// 因整串切不动被判错拼后一个候选都不给。
		return (b >= 'a' && b <= 'z') && b != 'u' && b != 'i'
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

// singleSyllableExactLimit 是「单个完整音节」的精确匹配上限：同音字全量
// 召回（单音节输入、>=4 音节长输入回退到首音节、>7 token 长输入显式补的首
// 音节查询、以及任何多段输入末尾的单字兜底，四处必须同一口径）。回退本身
// 仍只取首段 top10，全量只在列表末尾的单字兜底块里补，理由见 compose 注释。
//
// 取值 300：当前词典同音字最多的是 yi 141、ji 130、yu 124、qi 91——旧值 120
// 会把 yi/ji/yu/qi 的尾部常用字裁掉（用户反馈单字候选太少）。300 覆盖当前
// 词典全部单字并给用户词库/后续词库留余量；只对**单音节**放开是刻意的：
// 多音节全拼的 exact 上限仍是 20（歧义切分路径 × exact 条数是乘法放大，
// 放开会让热路径从 24.6µs 涨到 246µs，见下方注释）。
// 回归：TestCommonDictSingleSyllableFullRecall（shi）、
// TestCommonDictSingleSyllableFullRecallBeyond120（yi 全量）。
const singleSyllableExactLimit = 300

// initialsExactLimit 是**多段简拼/混拼**的词候选精确上限。它与单字上限分开：
// exact 放大到 300 会让 zg/dz 这类两段简拼的候选从 ~50 页膨胀到 ~80 页（词洪水
// 稀释单字页位）；多段输入需要的单字由列表末尾的 rankSingleBackfill 块全量补。
// 单段声母输入（y/j/zh…）结果全是单字，仍走 singleSyllableExactLimit。
const initialsExactLimit = 120

// typoMaxCorrections 是一次错拼修正最多**生成**的修正串数量。
//
// 单次编辑能生成的串数上限约 3n + 26(n+1) + 25n（n = 输入长度），十来个
// 字母就有上千条，全查太贵。取 32 是实测折中：纠错只在「输入连简拼路径都
// 排不出来」时触发（正常打字零开销），而 typoCorrections 已把最有价值的
// 「相邻互换 → 删除 → 粘键折叠 → 邻键替换」排在最前（实际顺序见
// syllable.go 的 typoCorrections），32 条足以覆盖它们，并给末尾的「插入」
// 留出额度——中段漏字（zhonguo → zhongguo，需要在中间插 g）就靠这部分。
// 实测把生成上限从 160 降到 32，zhogn 的单键耗时从 2.1ms 降到百微秒级，
// 而修正效果不变（zhogn → 中 仍为首选）。
//
// 注意「生成上限」与本文件下方的「查询上限」（typoMaxQueries）是两档：
// 生成只做纯音节 DP 校验、很便宜；真正花时间的是每条一次的词典 Trie 查询。
const typoMaxCorrections = 32

// typoMaxQueries 是一次错拼修正最多**查询**的修正串数量。
//
// 与 typoMaxCorrections 是两档上限：typoCorrections 生成很便宜（实测约
// 39µs，只做纯音节 DP 校验），贵的是每条一次的词典 Trie 遍历。实测把查询
// 上限从 160 降到 32，zhogn 的单键耗时从 2.1ms 降到百微秒级，而修正效果
// 不变（zhogn → 中 仍为首选）。
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

// PrevWord 返回最近一次上屏的词（渐进上屏/联想续接按字前缀查询用）。
func (s *Session) PrevWord() string { return s.prev }
