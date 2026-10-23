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
			if allExact {
				entries = dict.QueryExact(partSylls)
				if user != nil {
					userEntries := user.QueryPhrases(partSylls, "")
					if len(userEntries) > 0 {
						entries = append(append([]*Entry(nil), entries...), userEntries...)
					}
				}
			} else {
				exacts := dict.QueryTokensExact(part, 6)
				entries = exacts
				if user != nil {
					userExacts := user.QueryTokensExact(part, 6)
					if len(userExacts) > 0 {
						entries = append(append([]*Entry(nil), entries...), userExacts...)
					}
				}
			}

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
