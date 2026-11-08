package engine

import "strings"

// compose_state.go —— compose() 拆分后的唯一可变状态容器。
//
// 原 compose()（1009 行）里的候选池/去重 map/形态判定结果/错拼状态全部收敛到
// 这里；各职责文件（compose_continuation / compose_typo / compose_query /
// compose_rank / compose.go）只通过 composeState 的方法读写同一份状态。
// 这是「纯重构」的等价性基础：
//   - 候选插入顺序 = items 切片顺序（同层稳定排序的次键）；
//   - typoFix 标记跟随 items 下标（add 返回下标）；
//   - seen/seenShorter/addedContinuation 的去重口径与拆分前逐字一致。
type candItem struct {
	cand Candidate
	rank int
}

// composeState 是一次 compose 调用的全部可变状态。底层字段（items / seen /
// addedContinuation / typo）在调用链里共享，禁止拷贝本结构体。
type composeState struct {
	s *Session

	patterns [][]Token // 全部合法音节切分（enumerateAllTokens 结果）
	items    []candItem

	// seen 以「词 + 音节串」为键记录候选在 items 里的下标：同一词条被多条路径
	// 命中时合并分数/FromUser/rank/分段（见 add）。
	seen map[string]int
	// seenShorter 记录已做过的逐级回退前缀查询，避免同一前缀重复查询。
	seenShorter map[string]bool
	// addedContinuation 是两条续接路径共用的去重（词 + 音节串）。
	addedContinuation map[string]bool

	// shape 是输入形态判定结果（compose_shape.go 的纯函数），决定续接层级、
	// 常规路径是否走昂贵的 prefix 收集、超段仲裁口径等。
	shape composeShape

	// typo 是错拼修正路径的共享状态（compose_typo.go）。
	typo typoState
}

// newComposeState 构造一次 compose 的初始状态：输入切分 + 形态判定。
//
// 注意：输入切分失败（patterns 为空）**不能**提前返回——完全无法切分的输入
// （beijngi / zhonguo / niihao）正是「打错字」的典型形态，必须继续走错拼修正
// 路径才能救回来；常规 token 循环对空切片是安全的空操作。
func newComposeState(s *Session) *composeState {
	patterns := enumerateAllTokens(strings.Split(s.input, "'"))
	return &composeState{
		s:                 s,
		patterns:          patterns,
		seen:              make(map[string]int),
		seenShorter:       make(map[string]bool),
		addedContinuation: make(map[string]bool),
		shape:             analyzeShape(s.input, patterns),
		typo:              newTypoState(),
	}
}

// add 把一条词条加入候选池并返回其在 items 里的下标：错拼路径据此给候选打
// typoFix 标记（同一词条可能先由错拼路径加入、后由常规路径去重合并，标记跟着
// 下标走）。
//
// 合并规则（与拆分前 addCandidate 完全一致）：
//   - 更高分覆盖 Score/FromUser；FromUser 只升不降；
//   - rank 只降不升（同一候选被更靠前的层级命中时提升）；
//   - 同一词条先由词典路径加入（无分段）、后由学习记录加入（有分段）时补分段，
//     保证上屏学习走整句式路径。
func (st *composeState) add(e *Entry, score int, fromUser bool, r int) int {
	word := e.Word
	if st.s.traditional {
		word = ToTraditional(word) // 候选显示与上屏均为繁体
	}
	var segs []string
	if len(e.Segs) >= 2 {
		segs = append([]string(nil), e.Segs...)
		if st.s.traditional {
			// 繁体模式：候选显示/上屏是繁体，分段也要同步转成繁体，
			// 保证 learnSelection 的「分段拼接 == 候选文本」校验成立。
			for i := range segs {
				segs[i] = ToTraditional(segs[i])
			}
		}
	}
	key := word + "\x00" + strings.Join(e.Syllables, " ")
	if prev, ok := st.seen[key]; ok {
		if score > st.items[prev].cand.Score {
			st.items[prev].cand.Score = score
			st.items[prev].cand.FromUser = fromUser
		} else if fromUser {
			st.items[prev].cand.FromUser = true
		}
		if r < st.items[prev].rank {
			st.items[prev].rank = r
		}
		// 同一词条可能先由词典路径加入（无分段）、后由学习记录加入
		// （有分段）：补全分段，保证上屏学习走整句式路径。
		if len(st.items[prev].cand.Segments) == 0 && len(segs) > 0 {
			st.items[prev].cand.Segments = segs
		}
		return prev
	}
	st.seen[key] = len(st.items)
	st.items = append(st.items, candItem{
		cand: Candidate{
			Text:      word,
			Syllables: strings.Join(e.Syllables, "'"),
			Score:     score,
			FromUser:  fromUser,
			Segments:  segs,
		},
		rank: r,
	})
	return len(st.items) - 1
}
