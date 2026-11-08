package engine

import "strings"

// compose_query.go —— 常规查询路径（原 compose() 的 G/H/I/J 段）。
//
//   - addJoinedMatches：不依赖切分的「输入 == 词条标准拼音连写」匹配；
//   - addTokenMatches：按音节切分查词典/用户词库 + 逐级回退 + 长输入首音节；
//   - addSingleCharBackfill：多段输入末尾的首段全量单字兜底（列表最末纯单字）；
//   - addCompositionCandidates：正常输入的「已有词组合」候选（rankComposition 层）。

// addJoinedMatches 做连写匹配（原 compose() 的 G 段）。
//
// 常规候选依赖音节切分枚举（有上限），长输入可能漏掉词库里真实存在的长词；
// 这一步不依赖切分枚举，只沿词库里已有的词条边走，因此既不会造词，也不会
// 漏掉词库里有的词（用户 2026-11-01 澄清：去掉的是「拼合造词」，词库里有的
// 词组仍要用 DP 匹配；单字由首音节层始终提供，不受影响）。
//
// 仅对**没有分隔符**的连续输入生效：输入里出现 ' 时用户已显式划分音节
// （如 b'a 是简拼 b + 全拼 a），把分隔符去掉按连写匹配会把 b'a 误当成
// 全拼 ba（把/八 压过 保安/备案）。有分隔符时交给常规切分路径即可。
func (st *composeState) addJoinedMatches() {
	s := st.s
	if st.shape.TypoSuspicious || strings.ContainsRune(s.input, '\'') {
		return
	}
	joined := s.input
	for _, e := range s.dict.QueryJoinedPinyin(joined, 20) {
		boost := 0
		if s.user != nil {
			boost = s.user.Score(e.Word, e.Syllables, s.prev)
		}
		st.add(e, e.Freq+boost, boost > 0, rankExact)
	}
	if s.user != nil {
		for _, e := range s.user.QueryJoinedPinyin(joined, 10) {
			st.add(e, e.Freq, true, rankExact)
		}
	}
}

// addTokenMatches 是常规 token 主循环（原 compose() 的 H 段）：对每条切分路径
// （模糊音开启时再展开模糊变体）查词典精确/前缀匹配、用户词库、逐级回退前缀，
// 并对 >7 token 的长输入显式补首音节查询。
func (st *composeState) addTokenMatches() {
	s := st.s
	for _, pat := range st.patterns {
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
			if st.shape.TypoSuspicious {
				exacts = s.dict.QueryTokensExact(qp, 20)
			} else {
				// 精确匹配上限按形态分配，prefix 一律维持 20。此前一律裁到
				// 词频 top 20 的后果随输入形态逐步暴露：
				//   - 单音节全拼（2026-10-28 用户反馈）：shi 的 107 个同音
				//     字里排 33 位的「示」进不了候选池，翻页也打不出来；
				//   - 简拼/混拼（2026-10-28 扫描诊断）：yb 有 229 个满足
				//     y?+b? 的双音节词，简拼「一百」排 20 名之外同样不可达，
				//     initials 场景命中率仅 16.43%（miss 828/1455）。
				// 上限分配依据（2026-10-28 实测）：
				//   - 单音节全拼 → 120：同音字全量召回，热路径 31µs 无回归；
				//   - 简拼/混拼 → 120：延迟由 prefix BFS 主导（0.93ms），
				//     exact 放开近乎免费，miss 828 → 127；
				//   - 多音节全拼保持 20：多条歧义切分路径 × exact 条数是
				//     乘法放大（全形态 120 时 24.6µs → 246µs，60 仍 74µs）。
				// prefix 若随 exact 一并放大，低频衍生词组洪水会稀释单字页位
				// 并进一步拖慢热路径（实测 32.9µs → 71µs），故两类上限独立。
				exactLimit, prefixLimit := 20, 20
				switch {
				case len(qp) == 1 && qp[0].IsExact:
					// 单音节全拼：同音字全量（yi 141 / ji 130 / yu 124 / qi 91）。
					exactLimit = singleSyllableExactLimit
				case len(qp) == 1:
					// 单段声母（y/j/zh…）：QueryTokensExact 只返回单字，放开到
					// 单字上限，让单字母简拼也能选到更多同声母单字。
					exactLimit = singleSyllableExactLimit
				case hasInitials:
					// 多段简拼/混拼：词候选上限保持 120，避免页面膨胀；
					// 单字由末尾兜底块全量补。
					exactLimit = initialsExactLimit
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
					st.add(e, e.Freq+boost, boost > 0, rankExact)
				}
				for _, e := range prefixes {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					st.add(e, e.Freq+boost, boost > 0, rankPrefix)
				}
			} else {
				// 简拼 / 混拼匹配
				for _, e := range exacts {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					st.add(e, e.Freq+boost, boost > 0, rankInitialExact)
				}
				for _, e := range prefixes {
					boost := 0
					if s.user != nil {
						boost = s.user.Score(e.Word, e.Syllables, s.prev)
					}
					st.add(e, e.Freq+boost, boost > 0, rankInitialPrefix)
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
					st.add(e, e.Freq, true, r)
				}
				for _, e := range uPrefixes {
					// 整句式学习记录（今天星期一 / 今天天气）只在**完整输入**
					// 时作为整句候选（走上面的 uExacts），绝不能在只打了前缀时
					// 以「整词」候选出现——否则打 今 就会出现 今天星期一，
					// 用户想打 今天 时反而被整词候选干扰（2026-10-30 用户要求）。
					if s.isSentenceEntry(e) {
						continue
					}
					r := rankPrefix
					if hasInitials {
						r = rankInitialPrefix
					}
					st.add(e, e.Freq, true, r)
				}
			}

			// 逐级回退前缀：多音节输入叠加更短的前缀匹配（如输入 womende 时也出「我们」；
			// 打 nihaox 时，因已完整的「你好」之后是残缺声母，将「你好」置顶推出）
			limit := len(qp) - 1
			if limit > 6 { // 上限 6：更深的回退由下方「长输入首音节兜底」单独补
				limit = 6
			}
			for drop := 1; !st.shape.TypoSuspicious && drop <= limit; drop++ {
				shorter := qp[:len(qp)-drop]
				sKey := tokensKey(shorter)
				if st.seenShorter[sKey] {
					continue
				}
				st.seenShorter[sKey] = true

				allExactShorter := true
				for _, t := range shorter {
					if !t.IsExact {
						allExactShorter = false
						break
					}
				}
				// 回退到单个**完整音节**（首音节）且输入是长拼音（>=4 音节）时，对齐
				// 单音节输入的召回口径：同音字全量进入候选池，逐字挑选（SelectPrefix）
				// 才选得到 top10 之外的常用字。
				//
				// 回退本身保持 top10（2 音节是最高频输入形态，全量放进回退层会让
				// 每次组词的候选从 ~20 条膨胀到 ~80-150 条，翻页观感与候选池排序
				// 成本都变差）；需要逐字挑选时由下方「单字兜底」统一在列表末尾
				// 追加首段全量单字（rankSingleBackfill），整词/组合候选仍在前。
				// 「长」chang（词频 40）在 chang 同音字里排第 6，本就 top10 可达；
				// 用户打不出「长」的真实主因是安装词库过期（旧内置词库只有
				// 长 zhang，安装器升级缺陷另行修复）。回归见
				// TestCommonDictShortPinyinSingleFallbackAlways。
				// 单字母简拼段（IsExact=false）不放开：它按声母跨多个音节匹配，候选量
				// 爆炸且与 SelectPrefix 的按音节消费对不上。
				exactLimit := 10
				if len(shorter) == 1 && shorter[0].IsExact && len(qp) >= 4 {
					exactLimit = singleSyllableExactLimit
				}
				sExacts := s.dict.QueryTokensExact(shorter, exactLimit)
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
					st.add(e, e.Freq+boost, boost > 0, r)
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
						st.add(e, e.Freq, true, r)
					}
				}
			}
			// >7 个 token 的长输入：drop 上限 6 让回退永远到不了首音节（8 音节输入
			// 最多回退到前 2 个音节），逐字挑选时首音节单字候选整体不可达。显式补
			// 一次首音节查询，与单音节输入对齐 120 的同音字召回口径；排在所有常规
			// 回退层之后（rankBackoff+limit+1），不改变原有层级顺序。
			// 只看完整音节（IsExact）：单字母简拼段跨多个音节，与 SelectPrefix 的
			// 按音节消费对不上，补进来也选不了，只会稀释候选。
			if !st.shape.TypoSuspicious && len(qp)-1 > limit && qp[0].IsExact {
				first := qp[:1]
				sKey := tokensKey(first)
				if !st.seenShorter[sKey] {
					st.seenShorter[sKey] = true
					r := rankBackoff + limit + 1
					for _, e := range s.dict.QueryTokensExact(first, singleSyllableExactLimit) {
						boost := 0
						if s.user != nil {
							boost = s.user.Score(e.Word, e.Syllables, s.prev)
						}
						st.add(e, e.Freq+boost, boost > 0, r)
					}
					if s.user != nil {
						for _, e := range s.user.QueryTokensExact(first, 5) {
							st.add(e, e.Freq, true, r)
						}
					}
				}
			}
		}
	}
}

// addSingleCharBackfill 追加首段全量单字兜底（原 compose() 的 I 段）。
//
// 任何多段输入（全拼/混拼/简拼）都在候选列表最末追加**首段全量单字**，让用户
// 随时可以逐字挑选、拼出词库没有的词。候选只来自词典/用户词库真实单字，不造词；
// 单段输入本身已是全量单字，无需重复。追加层排在组合候选之后、列表最末
// （rankSingleBackfill = rankComposition+16），不挤占整词/组合候选的首屏；
// 单字块收尾后不再出现词组。错拼输入例外：此时纠错候选才是主路径，单字兜底
// 会破坏「纠错固定第 3 位」的排序约定。
func (st *composeState) addSingleCharBackfill() {
	s := st.s
	if st.shape.TypoSuspicious || len(st.patterns) == 0 {
		return
	}
	clean := st.patterns[0]
	if len(clean) < 2 {
		return
	}
	first := []Token{clean[0]}
	for _, e := range s.dict.QueryTokensExact(first, singleSyllableExactLimit) {
		if len([]rune(e.Word)) != 1 {
			continue
		}
		boost := 0
		if s.user != nil {
			boost = s.user.Score(e.Word, e.Syllables, s.prev)
		}
		st.add(e, e.Freq+boost, boost > 0, rankSingleBackfill)
	}
	if s.user != nil {
		for _, e := range s.user.QueryTokensExact(first, singleSyllableExactLimit) {
			if len([]rune(e.Word)) != 1 {
				continue
			}
			st.add(e, e.Freq, true, rankSingleBackfill)
		}
	}
}

// addCompositionCandidates 收集正常输入的「已有词组合」候选（原 compose() 的 J 段）。
//
// 正确拼写的多音节输入如果整串在词库里没有单条词条、但能切成已有词的序列
// （changshuru → 长+输入、tuzhong → 图+中），也作为候选给出。约束：只允许
// 词典/用户词库中真实存在的词段（不造词）；放在 rankComposition 层（真实词条
// 之后、逐级回退碎片之前），不会挤掉正常首选；最多 4 条。
func (st *composeState) addCompositionCandidates() {
	s := st.s
	if st.shape.TypoSuspicious || st.shape.MidSuspicious || st.shape.TailSuspicious || len(st.patterns) == 0 {
		return
	}
	clean := st.patterns[0]
	cleanExact := len(clean) >= 2
	for _, tok := range clean {
		if !tok.IsExact {
			cleanExact = false
			break
		}
	}
	if !cleanExact {
		return
	}
	for _, e := range s.composedPhraseEntries(clean, 4) {
		boost := 0
		if s.user != nil {
			boost = s.user.Score(e.Word, e.Syllables, s.prev)
		}
		st.add(e, e.Freq+boost, boost > 0, rankComposition)
	}
}
