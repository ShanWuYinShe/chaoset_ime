package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 学习与调频参数。设计目标：越用越顺手——
// 最近用过的词权重高（时间衰减）、顺着上下文的词加分（bigram）、
// 反复分字打的组合自动固化成词组（词组进化）。
const (
	// UserBoostPerCount 是每次上屏对候选得分的加权基数。
	UserBoostPerCount = 500
	// BigramBonusPerCount 是上下文（前一词→当前词）每共现一次的加分。
	BigramBonusPerCount = 250
	// bigramMaxCount 限制上下文加成计入的共现次数，防止长尾刷分。
	bigramMaxCount = 2
)

// decay 按上次使用时间给出衰减系数：越新鲜权重越高。
// 桶式衰减（<1 天 / <7 天 / <30 天 / 更久）而非连续指数，
// 分数稳定可预期，且测试可用注入时钟精确断言。
func decay(lastUsed int64, now time.Time) float64 {
	days := now.Sub(time.Unix(lastUsed, 0)).Hours() / 24
	switch {
	case days < 1:
		return 1.0
	case days < 7:
		return 0.6
	case days < 30:
		return 0.3
	default:
		return 0.1
	}
}

type userRecord struct {
	Count    int   `json:"count"`
	LastUsed int64 `json:"last_used"`
}

// phraseRecord 是词组进化的产物：用户反复分字打出来的组合固化为词组。
type phraseRecord struct {
	Word     string `json:"word"`
	Sylls    string `json:"sylls"` // 空格分隔
	Count    int    `json:"count"`
	LastUsed int64  `json:"last_used"`
}

type userFile struct {
	Words   map[string]*userRecord   `json:"words"`   // 静态词的调频记录
	Phrases map[string]*phraseRecord `json:"phrases"` // 用户合成词组
	Bigrams map[string]*userRecord   `json:"bigrams"` // 前后词共现
}

// UserDict 是用户行为数据：静态词调频、合成词组、上下文共现。
// path 为空串时仅驻留内存；所有 bump 即时原子落盘。
// 多连接并发安全：daemon 的所有连接共享同一 UserDict，读（Score/
// QueryPhrases/Stats/Export）与写（Bump*/Clear/Save）经 mu 互斥。
type UserDict struct {
	mu   sync.RWMutex
	path string
	now  func() time.Time
	file userFile
}

// NewUserDict 加载用户词典；文件不存在视为空词典。
// 兼容阶段 0 的扁平格式（仅 words 的 JSON），读取时自动迁移。
func NewUserDict(path string) (*UserDict, error) {
	u := &UserDict{
		path: path,
		now:  time.Now,
		file: userFile{
			Words:   make(map[string]*userRecord),
			Phrases: make(map[string]*phraseRecord),
			Bigrams: make(map[string]*userRecord),
		},
	}
	if path == "" {
		return u, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return u, nil
		}
		return nil, fmt.Errorf("读取用户词典失败: %w", err)
	}
	if err := u.load(data); err != nil {
		return nil, fmt.Errorf("解析用户词典 %s 失败: %w", path, err)
	}
	return u, nil
}

func (u *UserDict) load(data []byte) error {
	// 先探测顶层键：新格式含 words/phrases/bigrams，旧格式是扁平 map
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if _, isNew := probe["phrases"]; isNew {
		return json.Unmarshal(data, &u.file)
	}
	// 旧格式：整体就是 word → record 的扁平表
	var legacy map[string]*userRecord
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	u.file.Words = legacy
	return nil
}

// setClock 注入时钟供测试控制时间衰减。
func (u *UserDict) setClock(f func() time.Time) { u.now = f }

// userKeySep 是用户词条键（词 + 分隔符 + 空格分隔的音节）的分隔符。
// 用 NUL 而非 TAB：词或音节中都不可能含 NUL，解析无歧义。
const userKeySep = "\x00"

func userKey(word string, syllables []string) string {
	return word + userKeySep + strings.Join(syllables, " ")
}

// parseUserKey 把 userKey 生成的键拆回「词 + 音节列表」，
// 与 userKey 使用同一个分隔符，避免两侧不一致导致查询恒空。
// 键缺少分隔符时 ok 为 false（调用方应跳过该条记录）。
func parseUserKey(key string) (word string, syllables []string, ok bool) {
	word, joined, found := strings.Cut(key, userKeySep)
	if !found || word == "" {
		return "", nil, false
	}
	syllables = strings.Fields(joined)
	if len(syllables) == 0 {
		return "", nil, false
	}
	return word, syllables, true
}

func (u *UserDict) touch(rec *userRecord) {
	rec.Count++
	rec.LastUsed = u.now().Unix()
}

// BumpWord 记录一次静态词上屏。
func (u *UserDict) BumpWord(word string, syllables []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	key := userKey(word, syllables)
	rec := u.file.Words[key]
	if rec == nil {
		rec = &userRecord{}
		u.file.Words[key] = rec
	}
	u.touch(rec)
	return u.saveLocked()
}

// BumpBigram 记录一对前后词共现。
func (u *UserDict) BumpBigram(prev, next string) error {
	if prev == "" || next == "" {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	key := prev + "\x00" + next
	rec := u.file.Bigrams[key]
	if rec == nil {
		rec = &userRecord{}
		u.file.Bigrams[key] = rec
	}
	u.touch(rec)
	return u.saveLocked()
}

// PairCount 返回前后词共现次数。
func (u *UserDict) PairCount(prev, next string) int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if rec := u.file.Bigrams[prev+"\x00"+next]; rec != nil {
		return rec.Count
	}
	return 0
}

// BumpPhrase 记录（或创建）一个用户合成词组的一次使用。
func (u *UserDict) BumpPhrase(word string, syllables []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	key := userKey(word, syllables)
	rec := u.file.Phrases[key]
	if rec == nil {
		rec = &phraseRecord{Word: word, Sylls: strings.Join(syllables, " ")}
		u.file.Phrases[key] = rec
	}
	rec.Count++
	rec.LastUsed = u.now().Unix()
	return u.saveLocked()
}

// PhraseCount 返回用户合成词组的使用次数（不存在为 0）。
func (u *UserDict) PhraseCount(word string, syllables []string) int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if rec := u.file.Phrases[userKey(word, syllables)]; rec != nil {
		return rec.Count
	}
	return 0
}

// Score 计算静态词条在当前上下文中的用户得分：
// 上屏次数 × 权重 × 时间衰减 + 上下文加成（后者独立生效，
// 顺着前文的词即使从未上屏过也能加分）。无任何记录返回 0。
func (u *UserDict) Score(word string, syllables []string, prev string) int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	score := 0
	if rec := u.file.Words[userKey(word, syllables)]; rec != nil {
		score = int(float64(rec.Count*UserBoostPerCount) * decay(rec.LastUsed, u.now()))
	}
	if prev != "" {
		if b := u.file.Bigrams[prev+"\x00"+word]; b != nil {
			n := b.Count
			if n > bigramMaxCount {
				n = bigramMaxCount
			}
			score += n * BigramBonusPerCount
		}
	}
	return score
}

func (u *UserDict) phraseScore(rec *phraseRecord) int {
	return int(float64(rec.Count*UserBoostPerCount) * decay(rec.LastUsed, u.now()))
}

// QueryPhrases 返回匹配「音节序列（+末尾残缺音节前缀）」的用户合成词组。
// 词组规模在数千以内时线性扫描足够；量级上来后再建音节索引。
func (u *UserDict) QueryPhrases(syllables []string, frag string) []*Entry {
	u.mu.RLock()
	defer u.mu.RUnlock()
	joined := strings.Join(syllables, " ")
	prefix := ""
	if joined != "" {
		prefix = joined + " "
	}
	var out []*Entry
	for _, rec := range u.file.Phrases {
		if frag == "" {
			if rec.Sylls != joined {
				continue
			}
		} else {
			if !strings.HasPrefix(rec.Sylls, prefix) {
				continue
			}
			rest := rec.Sylls[len(prefix):]
			next := rest
			if i := strings.IndexByte(rest, ' '); i >= 0 {
				next = rest[:i]
			}
			if !strings.HasPrefix(next, frag) {
				continue
			}
		}
		out = append(out, &Entry{
			Word:      rec.Word,
			Syllables: strings.Fields(rec.Sylls),
			Freq:      u.phraseScore(rec),
		})
	}
	return out
}

// QueryPhrasesPrefix 返回「音节序列为前缀」的用户合成词组（联想用）：
// 上屏「现在」后，打 xian'zai 的联想应含「现在时间」这类以该序列开头的
// 学习词组。词组规模在数千以内时线性扫描足够。
func (u *UserDict) QueryPhrasesPrefix(syllables []string) []*Entry {
	u.mu.RLock()
	defer u.mu.RUnlock()
	prefix := strings.Join(syllables, " ")
	if prefix == "" {
		return nil
	}
	prefix += " "
	var out []*Entry
	for _, rec := range u.file.Phrases {
		if !strings.HasPrefix(rec.Sylls, prefix) {
			continue
		}
		out = append(out, &Entry{
			Word:      rec.Word,
			Syllables: strings.Fields(rec.Sylls),
			Freq:      u.phraseScore(rec),
		})
	}
	return out
}

// QueryTokensExact 返回音节数恰好等于 len(tokens) 的完全匹配用户词条（按得分降序，最多 maxResults 条）。
func (u *UserDict) QueryTokensExact(tokens []Token, maxResults int) []*Entry {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if len(tokens) == 0 {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 20
	}
	var exacts []*Entry
	seen := make(map[string]bool)

	// 1. 查询用户短语
	for _, rec := range u.file.Phrases {
		sylls := strings.Fields(rec.Sylls)
		if len(sylls) != len(tokens) {
			continue
		}
		ok := true
		for i, tok := range tokens {
			if !matchSyllable(sylls[i], tok) {
				ok = false
				break
			}
		}
		if ok && !seen[rec.Word] {
			seen[rec.Word] = true
			exacts = append(exacts, &Entry{
				Word:      rec.Word,
				Syllables: sylls,
				Freq:      u.phraseScore(rec),
			})
		}
	}

	// 2. 查询用户调频单字/词
	for key, rec := range u.file.Words {
		word, sylls, valid := parseUserKey(key)
		if !valid {
			continue
		}
		if len(sylls) != len(tokens) {
			continue
		}
		ok := true
		for i, tok := range tokens {
			if !matchSyllable(sylls[i], tok) {
				ok = false
				break
			}
		}
		if ok && !seen[word] {
			seen[word] = true
			exacts = append(exacts, &Entry{
				Word:      word,
				Syllables: sylls,
				Freq:      int(float64(rec.Count*UserBoostPerCount) * decay(rec.LastUsed, u.now())),
			})
		}
	}

	sort.Slice(exacts, func(i, j int) bool {
		if exacts[i].Freq != exacts[j].Freq {
			return exacts[i].Freq > exacts[j].Freq
		}
		return len(exacts[i].Word) < len(exacts[j].Word)
	})
	if len(exacts) > maxResults {
		exacts = exacts[:maxResults]
	}
	return exacts
}

// QueryTokens 返回匹配 tokens 序列的用户词组与用户词。
// 返回 exactMatches（音节数恰好等于 len(tokens)）与 prefixMatches（更长的衍生词条），
// 两组结果均已按词频降序排序。
func (u *UserDict) QueryTokens(tokens []Token, maxResults int) ([]*Entry, []*Entry) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if len(tokens) == 0 {
		return nil, nil
	}
	if maxResults <= 0 {
		maxResults = 20
	}
	var exacts []*Entry
	var prefixes []*Entry
	seen := make(map[string]bool)

	// 1. 查询用户短语
	for _, rec := range u.file.Phrases {
		sylls := strings.Fields(rec.Sylls)
		if len(sylls) < len(tokens) {
			continue
		}
		ok := true
		for i, tok := range tokens {
			if !matchSyllable(sylls[i], tok) {
				ok = false
				break
			}
		}
		if ok && !seen[rec.Word] {
			seen[rec.Word] = true
			e := &Entry{
				Word:      rec.Word,
				Syllables: sylls,
				Freq:      u.phraseScore(rec),
			}
			if len(sylls) == len(tokens) {
				exacts = append(exacts, e)
			} else {
				prefixes = append(prefixes, e)
			}
		}
	}

	// 2. 查询用户调频单字/词
	for key, rec := range u.file.Words {
		word, sylls, valid := parseUserKey(key)
		if !valid {
			continue
		}
		if len(sylls) < len(tokens) {
			continue
		}
		ok := true
		for i, tok := range tokens {
			if !matchSyllable(sylls[i], tok) {
				ok = false
				break
			}
		}
		if ok && !seen[word] {
			seen[word] = true
			e := &Entry{
				Word:      word,
				Syllables: sylls,
				Freq:      int(float64(rec.Count*UserBoostPerCount) * decay(rec.LastUsed, u.now())),
			}
			if len(sylls) == len(tokens) {
				exacts = append(exacts, e)
			} else {
				prefixes = append(prefixes, e)
			}
		}
	}

	sortSlice := func(entries []*Entry) {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Freq != entries[j].Freq {
				return entries[i].Freq > entries[j].Freq
			}
			return len(entries[i].Word) < len(entries[j].Word)
		})
	}

	sortSlice(exacts)
	sortSlice(prefixes)

	if len(exacts) > maxResults {
		exacts = exacts[:maxResults]
	}
	if len(prefixes) > maxResults {
		prefixes = prefixes[:maxResults]
	}
	return exacts, prefixes
}

// QueryContinuation 在用户词库里做「末音节续接」匹配：tokens 是前面已完整的
// 音节约束，frag 是末段尚未打完的串。命中「tokens + 以 frag 为前缀的音节」
// 开头的用户词条（自学习词组与调频词），返回结果带各自需补全的剩余字符。
//
// 用户词条用 []string 存音节，无法走 Trie，只能线性扫描；
// 用户词条规模在数千以内，配合「先比音节数、再比前缀」的提前退出足够快。
func (u *UserDict) QueryContinuation(tokens []Token, frag string, maxResults int) []SyllableContinuation {
	if frag == "" || len(tokens) == 0 {
		return nil
	}
	if maxResults <= 0 {
		maxResults = 8
	}
	u.mu.RLock()
	defer u.mu.RUnlock()

	var out []SyllableContinuation
	seen := make(map[string]bool)
	match := func(word string, sylls []string, score int) {
		if len(sylls) < len(tokens)+1 {
			return
		}
		for i, tok := range tokens {
			if !matchSyllable(sylls[i], tok) {
				return
			}
		}
		next := sylls[len(tokens)]
		if !strings.HasPrefix(next, frag) {
			return
		}
		key := word + userKeySep + strings.Join(sylls, " ")
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, SyllableContinuation{
			Entry:  &Entry{Word: word, Syllables: sylls, Freq: score},
			Remain: next[len(frag):],
		})
	}
	for _, rec := range u.file.Phrases {
		match(rec.Word, strings.Fields(rec.Sylls), u.phraseScore(rec))
	}
	for key, rec := range u.file.Words {
		word, sylls, ok := parseUserKey(key)
		if !ok {
			continue
		}
		match(word, sylls, int(float64(rec.Count*UserBoostPerCount)*decay(rec.LastUsed, u.now())))
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Remain) != len(out[j].Remain) {
			return len(out[i].Remain) < len(out[j].Remain)
		}
		if out[i].Entry.Freq != out[j].Entry.Freq {
			return out[i].Entry.Freq > out[j].Entry.Freq
		}
		return out[i].Entry.Word < out[j].Entry.Word
	})
	if len(out) > maxResults {
		out = out[:maxResults]
	}
	return out
}

// Save 以「写临时文件 + rename」的原子方式落盘，避免写一半被读到。
func (u *UserDict) Save() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.saveLocked()
}

// saveLocked 落盘（调用方须已持有写锁）。
func (u *UserDict) saveLocked() error {
	if u.path == "" {
		return nil
	}
	if dir := filepath.Dir(u.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建用户词典目录失败: %w", err)
		}
	}
	data, err := json.MarshalIndent(u.file, "", "  ")
	if err != nil {
		return err
	}
	tmp := u.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入用户词典失败: %w", err)
	}
	if err := os.Rename(tmp, u.path); err != nil {
		return fmt.Errorf("替换用户词典失败: %w", err)
	}
	return nil
}

// UserStats 是用户词典三类学习记录的条目数。
type UserStats struct {
	Words   int `json:"words"`   // 静态词调频记录
	Phrases int `json:"phrases"` // 用户合成词组
	Bigrams int `json:"bigrams"` // 前后词共现
}

// Stats 返回三类学习记录的条目数。
func (u *UserDict) Stats() UserStats {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return UserStats{
		Words:   len(u.file.Words),
		Phrases: len(u.file.Phrases),
		Bigrams: len(u.file.Bigrams),
	}
}

// Clear 清空全部学习记录（调频/词组/上下文）并立即落盘。
// 运行中的 daemon 应经 IPC 调用本方法（userClear），直接改文件会被
// daemon 内存态在下一次 bump 时覆盖。
func (u *UserDict) Clear() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.file = userFile{
		Words:   make(map[string]*userRecord),
		Phrases: make(map[string]*phraseRecord),
		Bigrams: make(map[string]*userRecord),
	}
	return u.saveLocked()
}

// ExportTSV 把学习记录导出为多段 TSV（# 开头为注释段头）：
// 词 / 用户词组 / 上下文共现，条目按次数降序、同次数按键名排序，
// 输出稳定可 diff。phrasesOnly 为 true 时只导出用户词组，且仅
// 「词\t拼音\t次数」三列——可直接作为 chaoset-import 的输入词库。
func (u *UserDict) ExportTSV(w io.Writer, phrasesOnly bool) error {
	u.mu.RLock()
	defer u.mu.RUnlock()
	bw := bufio.NewWriter(w)

	writeWords := func() error {
		if len(u.file.Words) == 0 {
			return nil
		}
		if _, err := fmt.Fprint(bw, "# 词\t拼音\t次数\t最近使用\n"); err != nil {
			return err
		}
		type row struct {
			word string
			rec  *userRecord
		}
		rows := make([]row, 0, len(u.file.Words))
		for key, rec := range u.file.Words {
			// 与查询侧共用同一套键解析，保证导出与查询口径一致；
			// 键残缺时退回原键名，避免整条记录丢失
			word, sylls, valid := parseUserKey(key)
			if !valid {
				word, sylls = key, nil
			}
			rows = append(rows, row{word + "\t" + strings.Join(sylls, " "), rec})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].rec.Count != rows[j].rec.Count {
				return rows[i].rec.Count > rows[j].rec.Count
			}
			return rows[i].word < rows[j].word
		})
		for _, r := range rows {
			when := time.Unix(r.rec.LastUsed, 0).UTC().Format(time.RFC3339)
			if _, err := fmt.Fprintf(bw, "%s\t%d\t%s\n", r.word, r.rec.Count, when); err != nil {
				return err
			}
		}
		return nil
	}

	writePhrases := func(header bool) error {
		if len(u.file.Phrases) == 0 {
			return nil
		}
		if header {
			if _, err := fmt.Fprint(bw, "# 词组\t拼音\t次数\t最近使用\n"); err != nil {
				return err
			}
		}
		rows := make([]*phraseRecord, 0, len(u.file.Phrases))
		for _, rec := range u.file.Phrases {
			rows = append(rows, rec)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Count != rows[j].Count {
				return rows[i].Count > rows[j].Count
			}
			return rows[i].Word < rows[j].Word
		})
		for _, r := range rows {
			if header {
				when := time.Unix(r.LastUsed, 0).UTC().Format(time.RFC3339)
				if _, err := fmt.Fprintf(bw, "%s\t%s\t%d\t%s\n", r.Word, r.Sylls, r.Count, when); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintf(bw, "%s\t%s\t%d\n", r.Word, r.Sylls, r.Count); err != nil {
				return err
			}
		}
		return nil
	}

	writeBigrams := func() error {
		if len(u.file.Bigrams) == 0 {
			return nil
		}
		if _, err := fmt.Fprint(bw, "# 上下文\t前词\t后词\t次数\n"); err != nil {
			return err
		}
		type row struct {
			pair string
			rec  *userRecord
		}
		rows := make([]row, 0, len(u.file.Bigrams))
		for key, rec := range u.file.Bigrams {
			prev, next, _ := strings.Cut(key, "\x00")
			rows = append(rows, row{prev + "\t" + next, rec})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].rec.Count != rows[j].rec.Count {
				return rows[i].rec.Count > rows[j].rec.Count
			}
			return rows[i].pair < rows[j].pair
		})
		for _, r := range rows {
			if _, err := fmt.Fprintf(bw, "%s\t%d\n", r.pair, r.rec.Count); err != nil {
				return err
			}
		}
		return nil
	}

	if phrasesOnly {
		if err := writePhrases(false); err != nil {
			return err
		}
		return bw.Flush()
	}
	if err := writeWords(); err != nil {
		return err
	}
	if err := writePhrases(true); err != nil {
		return err
	}
	if err := writeBigrams(); err != nil {
		return err
	}
	return bw.Flush()
}
