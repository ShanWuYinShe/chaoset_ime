package engine

import (
	"sort"
)

// SentenceCandidate 表示通过动态规划多词切分拼接得到的多音节复合候选（如「我们」+「的」=「我们的」）。
type SentenceCandidate struct {
	Text      string
	Syllables []string
	Score     int      // 内部综合得分（用于在多个整句候选中选优）
	Words     []string // 切分出的各个词
}

// FindSentenceCandidates 使用动态规划（Viterbi Beam Search）在词典与用户词库中
// 寻找能够完整拼合指定多音节序列（len(sylls) >= 2）的最优整句/复合短语候选。
func FindSentenceCandidates(dict *Dictionary, user *UserDict, sylls []string, beamWidth int) []SentenceCandidate {
	tokens := make([]Token, len(sylls))
	for i, s := range sylls {
		tokens[i] = Token{Text: s, IsExact: true}
	}
	return FindSentenceCandidatesTokens(dict, user, tokens, beamWidth)
}

// particle 是虚词/代词/助词等「可自由组合」的功能字：这些字与任意字
// 相邻拼合都可能合法（你说/把它/我来了），不参与双单字碎片降权。
// 覆盖：结构助词（的了着）、语气词（吗呢吧呀哦嗯）、代词（你我他她它
// 您咱谁哪这那）、常用副词/介词/连词（不很也都就还又才再被和与或在从
// 向让给比没别将）。
var particle = map[rune]bool{}

func init() {
	for _, r := range "的了着过吗呢吧呀哦嗯们是 不很也都就还又才再被和与或在从向让给比没别将你我他她它您咱谁哪这那个" {
		particle[r] = true
	}
}

// fragmentPenalty 是双音节输入中「两个实词单字拼合非词典词」的降权：
// 微+只 这类拼合不是词（2026-10-28 用户反馈「wei'zhi 候选里微只/为只/
// 未只 抢占真词位」），沉底让真词顶上；虚词/代词组合（你说/把它）豁免。
// 不直接过滤——沉底保留可达性，误伤成本最低。
const fragmentPenalty = 800

// userEntryBaseFreq 是「只存在于用户词库的词条」在整句规划中的基础分。
// 用户权重由 UserDict.Score 单独计入（见下方 uScore 分支），这里只给一个
// 与静态词频同量级的占位值，避免用户调频分（次数×500）被当作静态词频
// 再乘以音节数放大——那会让高频学过的单字霸榜，把完整词挤成碎字拼接。
const userEntryBaseFreq = 20

// mergeUserEntries 把用户词库命中的词条并入静态词条，并完成两件事：
//  1. 用户得分归一化：调频分（可达数千）不能直接当词频，统一压到
//     userEntryBaseFreq，真实权重交给 uScore 分支按上下文计算；
//  2. 去重：同一「词 + 音节序列」以静态词条为准（静态词频是真实语料频率），
//     用户词条只在静态词库没有该词时补位。
func mergeUserEntries(static, userEntries []*Entry) []*Entry {
	if len(userEntries) == 0 {
		return static
	}
	seen := make(map[string]bool, len(static))
	for _, e := range static {
		seen[userKey(e.Word, e.Syllables)] = true
	}
	out := static
	for _, e := range userEntries {
		key := userKey(e.Word, e.Syllables)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, &Entry{
			Word:      e.Word,
			Syllables: e.Syllables,
			Freq:      userEntryBaseFreq,
		})
	}
	return out
}

// FindSentenceCandidatesTokens 使用动态规划（Viterbi Beam Search）在词典与用户词库中
// 根据音节约束序列（支持全拼、简拼与混拼）寻找能够完整拼合的最优整句/复合短语候选。
//
// 评分策略：
//   - 多音节词奖励：鼓励组成词组（如「吃饭」而不是「吃」+「饭」）；
//   - 连续单字惩罚：避免把完整词拆成孤立碎字；
//   - 用户词库/上下文加成：结合 UserDict 的调频与 Bigram 上下文得分。
func FindSentenceCandidatesTokens(dict *Dictionary, user *UserDict, tokens []Token, beamWidth int) []SentenceCandidate {
	n := len(tokens)
	if n < 2 || dict == nil {
		return nil
	}
	if beamWidth <= 0 {
		beamWidth = 6
	}

	dp := make([][]SentenceCandidate, n+1)
	dp[0] = []SentenceCandidate{{}}

	for j := 1; j <= n; j++ {
		var candidates []SentenceCandidate
		maxLen := 4
		if j < maxLen {
			maxLen = j
		}
		for l := 1; l <= maxLen; l++ {
			i := j - l
			if len(dp[i]) == 0 {
				continue
			}
			part := tokens[i:j]

			allExact := true
			partSylls := make([]string, len(part))
			for k, tok := range part {
				partSylls[k] = tok.Text
				if !tok.IsExact {
					allExact = false
				}
			}

			var entries []*Entry
			var userEntries []*Entry
			if allExact {
				entries = dict.QueryExact(partSylls)
				if user != nil {
					userEntries = user.QueryPhrases(partSylls, "")
				}
			} else {
				entries = dict.QueryTokensExact(part, 6)
				if user != nil {
					userEntries = user.QueryTokensExact(part, 6)
				}
			}
			entries = mergeUserEntries(entries, userEntries)

			if len(entries) == 0 {
				continue
			}

			// 按频次排序，保留前 6 个高频词条用于转移，避免组合爆炸
			if len(entries) > 1 {
				sort.Slice(entries, func(a, b int) bool {
					return entries[a].Freq > entries[b].Freq
				})
			}
			if len(entries) > 6 {
				entries = entries[:6]
			}

			for _, prev := range dp[i] {
				var prevWord string
				if len(prev.Words) > 0 {
					prevWord = prev.Words[len(prev.Words)-1]
				}

				for _, entry := range entries {
					wordScore := entry.Freq * l
					if l > 1 {
						wordScore += (l - 1) * 100
					}

					// 碎字惩罚：若当前词是单字，且前驱已连续出现单字，则施加累进惩罚
					consecSingle := 0
					for k := len(prev.Words) - 1; k >= 0; k-- {
						if len([]rune(prev.Words[k])) == 1 {
							consecSingle++
						} else {
							break
						}
					}
					if l == 1 && consecSingle > 0 {
						// 连续单字累进惩罚（第 2 个单字 -120，第 3 个单字 -180...）
						wordScore -= (consecSingle + 1) * 60

						syllCount := len(prev.Syllables) + len(entry.Syllables)
						prevLast := ""
						if len(prev.Words) > 0 {
							prevLast = prev.Words[len(prev.Words)-1]
						}

						// 碎片拼合**过滤**（仅长句 ≥4 音节）：相邻两个单字段
						// 拼成的双字组合若不是词典词条（微+只=微只 ✗），该 DP
						// 路径不生成候选——用户词典的单字 Bigram（的→微）会给
						// 碎片路径巨额加成（实测碎片句 10268 分排第 2，且点选
						// →学习→排前形成污染循环；2026-10-28 用户反馈「文字
						// 输入的微只/为只/未只」）。
						// **为什么 ≥4 音节才过滤**：合法的全单字短句「他的车/
						// 在吗/我来了」都是 ≤3 音节且拼合词（他的/在吗/我来）
						// 不在词典——无差别过滤会把它们打挂（首轮实测）。长句
						// 尾部拼不成词的碎片才是污染源；≥4 音节真词有词典词条
						// 路径兜底（多路径冗余），只断纯碎片路径。
						if syllCount >= 4 && prevLast != "" {
							concatWord := prevLast + entry.Word
							concatSylls := append(append([]string(nil), prev.Syllables...), entry.Syllables...)
							if !dict.Contains(concatWord, concatSylls) {
								continue
							}
						}

						// 双音节降权（syllCount==2）：输入 wei'zhi 时「微只/为只/
						// 未只」这类**两个实词单字拼合的非词**应沉底——真词
						// （位置/为止/未知/为之）顶上。豁免：两字含虚词/代词
						//（你说/把它/我发——功能字与任意字相邻都可能合法）或
						// 拼合在词典。不直接过滤，仅沉底保可达性。
						if syllCount == 2 && l == 1 && len(prev.Words) > 0 {
							lastLen := len([]rune(prevLast))
							if lastLen == 1 && !dict.Contains(prevLast+entry.Word, append(append([]string(nil), prev.Syllables...), entry.Syllables...)) {
								a, b := []rune(prevLast)[0], []rune(entry.Word)[0]
								if !particle[a] && !particle[b] {
									wordScore -= fragmentPenalty
								}
							}
						}
					}

					if user != nil {
						uScore := user.Score(entry.Word, entry.Syllables, prevWord)
						if l == 1 && uScore > 50 {
							// 单字独立频次过高容易压制多字词组，非相邻共现加成时限制单字独立频次提权
							bigramBonus := 0
							if prevWord != "" {
								bigramBonus = user.PairCount(prevWord, entry.Word) * BigramBonusPerCount
							}
							wordBoost := uScore - bigramBonus
							if wordBoost > 50 {
								wordBoost = 50
							}
							uScore = wordBoost + bigramBonus
						}
						wordScore += uScore
					}
					totalScore := prev.Score + wordScore

					newWords := append([]string(nil), prev.Words...)
					newWords = append(newWords, entry.Word)

					candidates = append(candidates, SentenceCandidate{
						Text:      prev.Text + entry.Word,
						Syllables: append(append([]string(nil), prev.Syllables...), entry.Syllables...),
						Score:     totalScore,
						Words:     newWords,
					})
				}
			}
		}

		if len(candidates) == 0 {
			continue
		}

		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Score > candidates[j].Score
		})

		seen := make(map[string]bool)
		var unique []SentenceCandidate
		for _, c := range candidates {
			if !seen[c.Text] {
				seen[c.Text] = true
				unique = append(unique, c)
				if len(unique) >= beamWidth {
					break
				}
			}
		}
		dp[j] = unique
	}

	return dp[n]
}
