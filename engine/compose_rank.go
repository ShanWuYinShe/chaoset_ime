package engine

import (
	"sort"
	"strings"
)

// compose_rank.go —— 候选匹配层级常量与排序（原 compose() 的 M/N 段）。
//
// 层级常量从 compose() 函数体内的 const 块提到包级：数值是排序语义的一部分
// （rankComposition = rankBackoff+64、rankSingleBackfill = rankComposition+16
// 等相对关系不能变），搬动时保持逐值一致。
const (
	rankExact            = iota // 全拼单词完全匹配（如 jingchang -> 经常，ba -> 把）
	rankInitialExact            // 简拼/混拼单词完全匹配（如 zg -> 中国，bj -> 北京，jsj -> 计算机，rmb -> 人民币，zhongg -> 中国）
	rankContinuation            // 末段残缺续接（zhonggu → 中国，daxu → 大学）
	rankInitialPrefix           // 简拼偏长词（如 zg -> 中国人民，中关村）
	rankPrefix                  // 全拼偏长词续接（如 jing -> 经济）
	rankBackoff                 // 逐级回退前缀（如 nihaox -> 你好）
	rankContinuationLead        // 无法干净切分时的续接兜底（quanmia → 全面，而非 全米啊）
	// rankTypoFix 是错拼修正候选的**初始**层级：最低，仅保证能在列表末尾看到。
	// 是否提升到 rankExact 参与领跑，由排序前的词频比较决定（见
	// compose_typo.go 的 promoteTypoCorrections）——因为预筛只能看形态，无法
	// 区分手误（zhogn）与合法混拼（yiwys），必须靠「纠错命中词的词频远高于
	// 常规候选」这个词典事实才能安全提升。
	rankTypoFix
	// rankComposition 是正常输入的「已有词组合」候选（changshuru → 长+输入、
	// tuzhong → 图+中；2026-11-01 用户确认放开）。只允许词库已有词的序列。
	// 放在所有真实词条与逐级回退碎片之后（rankBackoff+64 保证大于任意
	// rankBackoff+drop），只作为列表末尾的额外选项：组合没有语言模型，
	// 不能改变任何原有候选的先后（用户此前的硬要求）。
	rankComposition = rankBackoff + 64
	// rankSingleBackfill 是任何多段输入末尾补的首段全量单字层级：
	// 排在**组合候选之后**、列表最末（纯单字收尾），只作为逐字挑选的兜底，
	// 不参与用户历史优先排序，也不改变原有候选先后。用户要求（2026-11-02）：
	// 单字块之后不能再出现词组（此前组合候选 是配/是培 落在单字块之后）。
	rankSingleBackfill = rankComposition + 16
)

// typoCandidateFixedPosition 是错拼修正候选的固定展示位置（1 起，第 3 位）。
const typoCandidateFixedPosition = 3

// sortCandidates 是候选池的最终排序（原 compose() 的 M 段）。
//
// 排序键（从高到低）：① 常规匹配层优先于逐级回退层；② 输入完全消耗的候选
// 优先于超段候选（音节超段仲裁，仅对全拼输入生效）；③ 用户数据加权候选优先
// （用户输入 > 默认字频）；④ 匹配层级（rank）；⑤ 综合得分、词长、字典序。
//
// 必须保持 sort.SliceStable + 全序尾巴（score/长度/文本）：不要改成
// sort.Slice，也不要改动插入顺序（同层同分候选的先后由插入顺序 = 稳定排序决定）。
func (st *composeState) sortCandidates() {
	sort.SliceStable(st.items, func(i, j int) bool {
		return lessCand(&st.items[i], &st.items[j], st.shape)
	})
}

// lessCand 是排序比较器（从 sort 闭包抽出便于单测）。语义见 sortCandidates 注释。
func lessCand(si, sj *candItem, sh composeShape) bool {
	a, b := si.cand, sj.cand
	aRank, bRank := si.rank, sj.rank
	aBackoff := aRank >= rankBackoff
	bBackoff := bRank >= rankBackoff
	if aBackoff != bBackoff {
		return !aBackoff
	}
	// 音节超段仲裁（**最高排序键之一，位于调频之前**）：词音节数多于
	// 输入段数的候选（用户词组/前缀续接，如完整多段输入时联想出的长词）
	// 不得排在「输入完全消耗」候选之前——准确匹配用户输入是硬约束，
	// 调频只在「同为全消耗」的候选之间决定先后；联想延伸交由上屏后的
	// 联想区域展示（见 associate）。
	// 单段输入（hao/tuan 等）已在上面过滤为纯单字，不会进入这里的词语比较。
	// 简拼/混拼的 token 数不是音节数（每个声母可展开成整个音节），
	// 用「候选音节数 > token 数」判超段会把学过的长词（ysfa → 原生方案）
	// 错误地压到默认短词后面。这类输入由 rankInitialExact <
	// rankInitialPrefix 保证「先完整匹配、后偏长词」，用户词再由下面的
	// FromUser 键优先。超段仲裁只对全拼输入（token 全是完整音节）生效。
	//
	// 全拼的显式分段按**所有合法切分的最大段数**判断用户词：tu'an 明确
	// 是 2 段，用户学过 tu'an 的词（图案）不会因为首选切分不同而被当成超段；
	// 单段 tuan 只出单字，不参与该仲裁。非用户候选仍按首选切分判超段，
	// 保持默认词频的既有排序不变。
	if sh.MaxInputSegs > 0 && !sh.InputHasInitials {
		aN := strings.Count(a.Syllables, "'") + 1
		bN := strings.Count(b.Syllables, "'") + 1
		aOver := aN > sh.MaxInputSegs || (!a.FromUser && aN > sh.InputSegs)
		bOver := bN > sh.MaxInputSegs || (!b.FromUser && bN > sh.InputSegs)
		if aOver != bOver {
			return !aOver
		}
	}
	if a.FromUser != b.FromUser {
		return a.FromUser
	}
	if aRank != bRank {
		return aRank < bRank
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if len(a.Text) != len(b.Text) {
		return len(a.Text) < len(b.Text)
	}
	return a.Text < b.Text
}

// placeTypoFixedPosition 把「已提升到精确层」的错拼修正候选固定排到第 3 位起
// （原 compose() 的 N 段；2026-11-02 用户要求：纠错是「猜测」，位置固定后
// 用户可以直接按 3 选中，不必每次找）。
//
// 只搬 rank < rankBackoff 的高置信度纠错——tailSuspicious 等低置信度纠错仍留在
// 列表末尾，不干扰正常输入；没有纠错候选时列表保持正常排序不变。
// len(items) > 0 守卫不是冗余：空列表时 normalItems/fixedItems 均为空，
// 重排结果也是空，保持与原实现一致的早退语义。
func (st *composeState) placeTypoFixedPosition() {
	if len(st.items) == 0 {
		return
	}
	var normalItems, fixedItems []candItem
	for _, it := range st.items {
		if it.cand.typoFix && it.rank < rankBackoff {
			fixedItems = append(fixedItems, it)
			continue
		}
		normalItems = append(normalItems, it)
	}
	if len(fixedItems) == 0 {
		return
	}
	insertAt := typoCandidateFixedPosition - 1
	if insertAt > len(normalItems) {
		insertAt = len(normalItems)
	}
	reordered := make([]candItem, 0, len(st.items))
	reordered = append(reordered, normalItems[:insertAt]...)
	reordered = append(reordered, fixedItems...)
	reordered = append(reordered, normalItems[insertAt:]...)
	st.items = reordered
}
