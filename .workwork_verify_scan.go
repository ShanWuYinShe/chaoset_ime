// 候选质量基线扫描（pinyin-quality-tuning 纪律）。
// 放在主树 .workwork/verify/scan/（不随 worktree 删除丢失），从仓库根
// `go run ./.workwork/verify/scan`；SCAN_MISS=1 输出各场景未命中样本。
//
// 口径（固定，前后可比）：词条池 = 词典中 2-4 音节、词频≥30、词长==音节数、
// 且为简体（繁体变体不入池）的规则词条；按 (词,拼音) 排序后每 25 条取 1；
// rank = 词与音节序列同时匹配的全列表位次（翻页到底）。词典拼音列是空格
// 分隔，候选 Syllables 是撇号连写——判定前须转换。
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"chaoset.ime/engine"
)

type entry struct {
	word, pinyin string
}

func main() {
	data, err := os.ReadFile("data/dict_common.tsv")
	if err != nil {
		panic(err)
	}
	t2s := map[rune]rune{}
	s2traw, err := os.ReadFile("engine/dp_data/STCharacters.txt")
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(s2traw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		key := []rune(parts[0])
		if len(key) != 1 {
			continue
		}
		for _, trad := range strings.Fields(parts[1]) {
			tr := []rune(trad)
			if len(tr) == 1 {
				if _, ok := t2s[tr[0]]; !ok {
					t2s[tr[0]] = key[0]
				}
			}
		}
	}
	t2sWord := func(w string) string {
		r := []rune(w)
		for i, c := range r {
			if s, ok := t2s[c]; ok {
				r[i] = s
			}
		}
		return string(r)
	}

	var pool []entry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}
		word, pinyin := parts[0], parts[1]
		if t2sWord(word) != word {
			continue // 繁体词条不入池（两边口径一致）
		}
		freq := 0
		fmt.Sscanf(parts[2], "%d", &freq)
		if freq < 30 {
			continue
		}
		nSyll := len(strings.Fields(pinyin))
		if nSyll < 2 || nSyll > 4 || len([]rune(word)) != nSyll {
			continue
		}
		pool = append(pool, entry{word: word, pinyin: pinyin})
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].word != pool[j].word {
			return pool[i].word < pool[j].word
		}
		return pool[i].pinyin < pool[j].pinyin
	})
	var sampled []entry
	for i, e := range pool {
		if i%25 == 0 {
			sampled = append(sampled, e)
		}
	}

	d, err := engine.LoadDictFiles([]string{"data/dict_common.tsv"})
	if err != nil {
		panic(err)
	}

	type stat struct {
		n, top1, top3, miss int
		rankSum             int64
	}
	scenes := []string{"full", "tail1", "tail2", "initials", "mixed"}
	stats := map[string]*stat{}
	for _, k := range scenes {
		stats[k] = &stat{}
	}

	makeInput := func(e entry, scene string) string {
		sylls := strings.Fields(e.pinyin) // 词典拼音列为空格分隔
		switch scene {
		case "full":
			return strings.Join(sylls, "")
		case "tail1":
			last := sylls[len(sylls)-1]
			if len(last) <= 1 {
				return ""
			}
			return strings.Join(append(sylls[:len(sylls)-1], last[:len(last)-1]), "")
		case "tail2":
			last := sylls[len(sylls)-1]
			if len(last) <= 2 {
				return ""
			}
			return strings.Join(append(sylls[:len(sylls)-1], last[:len(last)-2]), "")
		case "initials":
			var b strings.Builder
			for _, syl := range sylls {
				b.WriteString(syl[:1])
			}
			return b.String()
		case "mixed":
			var b strings.Builder
			b.WriteString(sylls[0])
			for _, syl := range sylls[1:] {
				b.WriteString(syl[:1])
			}
			return b.String()
		}
		return ""
	}

	missLog := os.Getenv("SCAN_MISS") != ""
	missed := map[string][]string{}

	for _, e := range sampled {
		sess := engine.NewSession(d, nil)
		for _, scene := range scenes {
			input := makeInput(e, scene)
			if input == "" {
				continue
			}
			sess.Reset()
			comp, err := sess.Input(input)
			if err != nil {
				continue
			}
			key := strings.ReplaceAll(e.pinyin, " ", "'")
			st := stats[scene]
			st.n++
			pos := 0
			for {
				hit := false
				for _, c := range comp.Candidates {
					pos++
					if c.Text == e.word && c.Syllables == key {
						hit = true
						break
					}
				}
				if hit {
					if pos == 1 {
						st.top1++
					}
					if pos <= 3 {
						st.top3++
					}
					st.rankSum += int64(pos)
					break
				}
				if comp.Page+1 >= comp.PageCount {
					st.miss++
					if missLog {
						missed[scene] = append(missed[scene], fmt.Sprintf("%s(%s)", e.word, e.pinyin))
					}
					break
				}
				if comp, err = sess.NextPage(); err != nil {
					st.miss++
					break
				}
			}
		}
	}

	if missLog {
		for _, scene := range scenes {
			list := missed[scene]
			if len(list) > 15 {
				list = list[:15]
			}
			fmt.Printf("MISS[%s]: %s\n", scene, strings.Join(list, " "))
		}
	}
	fmt.Printf("词条池 %d → 采样 %d\n", len(pool), len(sampled))
	for _, k := range scenes {
		st := stats[k]
		hit := st.n - st.miss
		rankAvg := 0.0
		if hit > 0 {
			rankAvg = float64(st.rankSum) / float64(hit)
		}
		fmt.Printf("%-9s n=%-5d top1=%5.2f%%  top3=%5.2f%%  miss=%-4d rankAvg=%.1f\n",
			k, st.n, float64(st.top1)*100/float64(st.n), float64(st.top3)*100/float64(st.n), st.miss, rankAvg)
	}
}
