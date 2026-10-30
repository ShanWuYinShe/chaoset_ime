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
	// Segs 仅整句式调频记录携带：用户确认过的分段（词文本，按上屏顺序）。
	// 整句（「今天星期一」）与单词共用 Words 表，靠它区分两种语义——
	// 有分段的是「整句习惯」，只允许在完整输入时作为整句候选、联想只在
	// 分段边界剥前缀；无分段的是普通词调频，行为与历史一致。
	Segs []string `json:"segs,omitempty"`
}

// phraseRecord 是词组进化的产物：用户反复分字打出来的组合固化为词组。
type phraseRecord struct {
	Word  string `json:"word"`
	Sylls string `json:"sylls"` // 空格分隔
	// Segs 是合成词组的分段（进化时的两个上屏词）。联想剥前缀只允许在
	// 这些分段边界上发生（2026-10-30 用户要求：不能因为学过「今天天气」
	// 就在上屏「今」后给出「天天气」）。
	Segs     []string `json:"segs,omitempty"`
	Count    int      `json:"count"`
	LastUsed int64    `json:"last_used"`
}

type userFile struct {
	Words   map[string]*userRecord   `json:"words"`   // 静态词的调频记录
	Phrases map[string]*phraseRecord `json:"phrases"` // 用户合成词组
	Bigrams map[string]*userRecord   `json:"bigrams"` // 前后词共现
}

// UserDict 是用户行为数据：静态词调频、合成词组、上下文共现。
// path 为空串时仅驻留内存。
//
// 多连接并发安全：daemon 的所有连接共享同一 UserDict，读（Score/
// QueryPhrases/Stats/Export）与写（Bump*/Clear/Save）经 mu 互斥。
type UserDict struct {
	mu   sync.RWMutex
	path string
	now  func() time.Time
	file userFile

	// dirty 表示内存态有尚未落盘的变更。
	//
	// 2026-10-29：FlushIfDirty 改为「锁内快照、锁外写盘」——此前写锁横跨
	// MarshalIndent+WriteFile+Rename（Windows 上 Defender 让这口气几十到
	// 几百 ms，词典越大越糟），期间所有按键 IPC 请求在锁上排队，表现为
	// 「切到输入法后整个系统发卡」。writes 是变更加代计数：写盘期间若有
	// 新变更（代数变化）则保持 dirty 留给下一轮。
	//
	// 为什么引入「延迟落盘」（2026-10-26）：一次上屏会触发 BumpWord +
	// BumpBigram（+ BumpPhrase），即最多 3 次「全量 MarshalIndent + 写临时
	// 文件 + rename」，全部在 IPC 响应返回前同步完成——壳因此阻塞在上屏路径
	// 上。该文件随使用单调增长（无淘汰），实测 3 年重度用量级（约 10MB）单次
	// Save 到 100ms+，直接卡按键。
	//
	// 行为契约（改动时必须同时满足）：
	//   - 默认（deferred=false，测试与短生命周期场景）：bump 立即落盘，语义
	//     与旧版完全一致，NewUserDict(path) 重新打开即可见刚才写入的数据；
	//   - 延迟模式（deferred=true，daemon 使用）：bump 只标脏，由后台 ticker
	//     定期 FlushIfDirty，进程退出时同步落盘。
	// 两种模式下 Save() 都保证把当前内存态写盘。
	dirty    bool
	deferred bool
	writes   uint64 // 变更加代计数（FlushIfDirty 快照一致性判定）

	// writeMu 串行化所有「写临时文件 + rename」的落盘操作。
	//
	// 与 mu 分离：mu 保护内存态（快照/查询/变更），writeMu 只覆盖磁盘写入
	// 段。锁序恒为 mu → writeMu（saveLocked 在持 mu 时获取 writeMu；
	// writeSnapshot 不持 mu、只获取 writeMu），任何路径都不反向获取，
	// 因此不会锁序反转死锁。
	//
	// 为什么需要（2026-10-30 审计 P1）：FlushIfDirty 的锁外写盘与
	// Save/Clear 的 saveLocked 此前共用固定 <path>.tmp 且无串行化——两个
	// 写者交错写同一 tmp 再 rename 可能落盘半截 JSON（下次启动 lenient 把
	// 整份学习数据备份重置），Clear 也可能被更早的快照覆盖（清空失效）。
	writeMu sync.Mutex
	// lastWrittenGen 是最近一次成功落盘对应的变更加代（由 writeMu 保护）。
	//
	// 串行化只保证两个写者不交错，不保证获取 writeMu 的顺序：若先落盘的
	// Save/Clear 被后到的旧 FlushIfDirty 快照覆盖，Clear 就白清了。所以
	// 写盘前比较代数：比已落盘状态更旧的快照直接丢弃（见 writeState）。
	lastWrittenGen uint64
}

// NewUserDict 加载用户词典；文件不存在视为空词典。
// 兼容阶段 0 的扁平格式（仅 words 的 JSON），读取时自动迁移。
//
// 文件存在但**无法解析**时返回错误（严格语义）。daemon 等常驻进程应改用
// NewUserDictLenient——理由见其注释。
func NewUserDict(path string) (*UserDict, error) {
	u, _, err := newUserDict(path, false)
	return u, err
}

// NewUserDictLenient 与 NewUserDict 相同，但把「文件存在却无法解析」视为
// **可恢复**，返回空词典与一条警告（调用方应写日志）。这是 daemon /
// ibus 引擎启动路径必须的语义：
//
//	学习数据是增量优化，绝不能因为它让整个输入法起不来。
//
// 这条原则在落盘路径上已经确立（见 cmd/chaosetd 关于 SetDeferred 的注释：
// 「落盘失败不能丢字符」）。此前加载路径没有对齐：userdict.json 一旦损坏
// （断电、磁盘满、手工编辑、旧版本残留），NewUserDict 报错 → daemon
// log.Fatal 退出 → 输入法彻底打不出字，用户只能手工删文件恢复。
//
// 损坏文件不静默删除：重命名为 <path>.corrupt-<时间戳> 保留现场便于排查。
// 仅「解析失败」降级；**读取失败仍返回错误**（权限不足、路径是目录等），
// 那类问题改名也解决不了，静默吞掉只会让用户误以为数据还在。
func NewUserDictLenient(path string) (*UserDict, string, error) {
	return newUserDict(path, true)
}

func newUserDict(path string, lenient bool) (*UserDict, string, error) {
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
		return u, "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return u, "", nil
		}
		return nil, "", fmt.Errorf("读取用户词典失败: %w", err)
	}
	if err := u.load(data); err != nil {
		if !lenient {
			return nil, "", fmt.Errorf("解析用户词典 %s 失败: %w", path, err)
		}
		// 解析可能已部分填充内存态，先清空再以空词典继续。
		u.file = userFile{
			Words:   make(map[string]*userRecord),
			Phrases: make(map[string]*phraseRecord),
			Bigrams: make(map[string]*userRecord),
		}
		bak := corruptUserDictBackupPath(path)
		if rerr := os.Rename(path, bak); rerr != nil {
			return u, fmt.Sprintf("用户词典 %s 解析失败（%v）；备份改名也失败（%v），本次以空词典启动",
				path, err, rerr), nil
		}
		return u, fmt.Sprintf("用户词典 %s 解析失败（%v），已备份为 %s；本次以空词典启动",
			path, err, bak), nil
	}
	return u, "", nil
}

// corruptUserDictBackupPath 生成损坏文件的备份路径。带毫秒时间戳：
// 同一秒内连续两次恢复也几乎不可能撞名，且便于按时间定位现场。
func corruptUserDictBackupPath(path string) string {
	return path + ".corrupt-" + time.Now().Format("20060102-150405.000")
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

// SetDeferred 开启/关闭延迟落盘（见 UserDict.deferred 的契约说明）。
// daemon 启动时开启；测试与一次性工具保持默认的立即落盘语义。
func (u *UserDict) SetDeferred(on bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.deferred = on
}

// persistAfterBump 按当前模式决定 bump 之后是否立即落盘。
// 调用方须已持写锁。
func (u *UserDict) persistAfterBump() error {
	if u.path == "" {
		return nil
	}
	u.dirty = true
	u.writes++
	if u.deferred {
		return nil
	}
	return u.saveLocked()
}

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

// BumpWord 记录一次静态词上屏（普通单词/单字，无整句分段）。
func (u *UserDict) BumpWord(word string, syllables []string) error {
	return u.bumpWord(word, syllables, nil)
}

// BumpWordWithSegs 记录一次**整句式**上屏：text 是用户确认过的整句/长词组，
// segs 是用户逐段确认的分段（词文本，顺序与 syllables 对齐）。
//
// 与 BumpWord 的区别只在记录上带 Segs：整句习惯只允许在**完整输入**时作为
// 整句候选出现，且在联想里只在分段边界剥前缀——否则「学过 今天星期一，
// 打 今 就给 天星期一」这种整词贪婪匹配会把用户想要的「今天」候选挤掉
// （2026-10-30 用户明确要求）。普通单词的 BumpWord 行为完全不变。
func (u *UserDict) BumpWordWithSegs(word string, syllables, segs []string) error {
	if len(segs) < 2 {
		return u.bumpWord(word, syllables, nil)
	}
	return u.bumpWord(word, syllables, append([]string(nil), segs...))
}

// bumpWord 是 BumpWord / BumpWordWithSegs 的共同实现（调用方不持锁）。
func (u *UserDict) bumpWord(word string, syllables, segs []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	key := userKey(word, syllables)
	rec := u.file.Words[key]
	if rec == nil {
		rec = &userRecord{}
		u.file.Words[key] = rec
	}
	if len(segs) >= 2 {
		rec.Segs = append(rec.Segs[:0], segs...)
	}
	u.touch(rec)
	return u.persistAfterBump()
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
	return u.persistAfterBump()
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
	return u.bumpPhrase(word, syllables, nil)
}

// BumpPhraseWithSegs 与 BumpPhrase 相同，但记录合成词组的分段（进化时的
// 前后两个上屏词）。联想剥前缀只允许在这些边界上发生。
func (u *UserDict) BumpPhraseWithSegs(word string, syllables, segs []string) error {
	return u.bumpPhrase(word, syllables, segs)
}

func (u *UserDict) bumpPhrase(word string, syllables, segs []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	key := userKey(word, syllables)
	rec := u.file.Phrases[key]
	if rec == nil {
		rec = &phraseRecord{Word: word, Sylls: strings.Join(syllables, " ")}
		u.file.Phrases[key] = rec
	}
	if len(segs) >= 2 {
		rec.Segs = append(rec.Segs[:0], segs...)
	}
	rec.Count++
	rec.LastUsed = u.now().Unix()
	return u.persistAfterBump()
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
			Segs:      append([]string(nil), rec.Segs...),
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
			Segs:      append([]string(nil), rec.Segs...),
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
				Segs:      append([]string(nil), rec.Segs...),
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
				Segs:      append([]string(nil), rec.Segs...),
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
				Segs:      append([]string(nil), rec.Segs...),
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
				Segs:      append([]string(nil), rec.Segs...),
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
	match := func(word string, sylls, segs []string, score int) {
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
			Entry: &Entry{Word: word, Syllables: sylls, Freq: score,
				Segs: append([]string(nil), segs...)},
			Remain: next[len(frag):],
		})
	}
	for _, rec := range u.file.Phrases {
		match(rec.Word, strings.Fields(rec.Sylls), rec.Segs, u.phraseScore(rec))
	}
	for key, rec := range u.file.Words {
		word, sylls, ok := parseUserKey(key)
		if !ok {
			continue
		}
		match(word, sylls, rec.Segs, int(float64(rec.Count*UserBoostPerCount)*decay(rec.LastUsed, u.now())))
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
//
// 语义是「保证把当前内存态写盘」，不做「无变更就跳过」的优化：
// 加载旧格式文件后即使没有 bump，Save 也应把它重写成新格式（迁移路径）。
func (u *UserDict) Save() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.saveLocked()
}

// FlushIfDirty 在后台定时调用：有变更才落盘。
// 返回是否真的写了盘，便于调用方统计/调试。
//
// 与 Save 的区别只是语义命名（不强调「必须写」），实现一致。
// daemon 用 time.Ticker 定期调用它，把「每次上屏都阻塞落盘」摊薄成
// 「最多每 flushInterval 写一次」。
// userFileSnapshot：FlushIfDirty 的锁内快照。记录是纯值结构，按值拷贝
// 即可脱离 u.mu；拷贝耗时是微秒级内存操作，磁盘写入在锁外进行——按键
// IPC 请求不再被落盘阻塞（2026-10-29 实测根因：Windows 上 Defender 使
// 持锁写盘长达几十到几百 ms，表现为切到输入法后系统整体发卡）。
type userFileSnapshot struct {
	words   map[string]userRecord
	phrases map[string]phraseRecord
	bigrams map[string]userRecord
	gen     uint64
}

func (u *UserDict) snapshotLocked() userFileSnapshot {
	snap := userFileSnapshot{
		words:   make(map[string]userRecord, len(u.file.Words)),
		phrases: make(map[string]phraseRecord, len(u.file.Phrases)),
		bigrams: make(map[string]userRecord, len(u.file.Bigrams)),
		gen:     u.writes,
	}
	for k, v := range u.file.Words {
		snap.words[k] = *v
	}
	for k, v := range u.file.Phrases {
		snap.phrases[k] = *v
	}
	for k, v := range u.file.Bigrams {
		snap.bigrams[k] = *v
	}
	return snap
}

func (u *UserDict) writeSnapshot(snap userFileSnapshot) error {
	if u.path == "" {
		return nil
	}
	out := userFile{
		Words:   make(map[string]*userRecord, len(snap.words)),
		Phrases: make(map[string]*phraseRecord, len(snap.phrases)),
		Bigrams: make(map[string]*userRecord, len(snap.bigrams)),
	}
	for k, v := range snap.words {
		vv := v
		out.Words[k] = &vv
	}
	for k, v := range snap.phrases {
		vv := v
		out.Phrases[k] = &vv
	}
	for k, v := range snap.bigrams {
		vv := v
		out.Bigrams[k] = &vv
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = u.writeState(data, snap.gen)
	return err
}

// writeState 把一份完整 JSON 快照落盘；gen 是这份数据对应的变更加代。
// 调用方不持 writeMu（saveLocked 可持 mu）。返回是否实际写了文件。
//
// 代数守卫：拿到 writeMu 时若已有更新的状态落盘（gen < lastWrittenGen），
// 这份旧快照直接丢弃——否则并发 Save/Clear 写下的新状态会被更早的
// FlushIfDirty 快照覆盖（清空失效）。
func (u *UserDict) writeState(data []byte, gen uint64) (bool, error) {
	u.writeMu.Lock()
	defer u.writeMu.Unlock()
	if gen < u.lastWrittenGen {
		return false, nil
	}
	if err := u.writeFileLocked(data); err != nil {
		return false, err
	}
	u.lastWrittenGen = gen
	return true, nil
}

// writeFileLocked 以「临时文件 + rename」原子写入用户词典文件。
// 调用方须持有 writeMu。
//
// tmp 用 os.CreateTemp 生成唯一名：即便未来有新的写者忘记串行化，也不会
// 互相截断同一份 tmp；失败路径统一清理 tmp，不在用户数据目录留垃圾。
func (u *UserDict) writeFileLocked(data []byte) error {
	dir := filepath.Dir(u.path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建用户词典目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(u.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建用户词典临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	// 统一失败清理：rename 成功后 tmpName 已不存在，Remove 报错忽略即可。
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("写入用户词典失败: %w", err)
	}
	// 与旧实现的 0644 保持一致（CreateTemp 默认 0600）。
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("设置用户词典权限失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("关闭用户词典临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, u.path); err != nil {
		cleanup()
		return fmt.Errorf("替换用户词典失败: %w", err)
	}
	return nil
}

// FlushIfDirty：锁内快照（微秒级）、锁外写盘。写盘期间有新 bump（代数
// 变化）则保持 dirty，由下一轮 ticker 带上；返回 true 表示确实写了一次盘。
func (u *UserDict) FlushIfDirty() (bool, error) {
	u.mu.Lock()
	if !u.dirty {
		u.mu.Unlock()
		return false, nil
	}
	snap := u.snapshotLocked()
	u.mu.Unlock()
	if err := u.writeSnapshot(snap); err != nil {
		return true, err
	}
	u.mu.Lock()
	if u.writes == snap.gen {
		u.dirty = false
	}
	u.mu.Unlock()
	return true, nil
}

// saveLocked 落盘（调用方须已持有写锁）。
func (u *UserDict) saveLocked() error {
	if u.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(u.file, "", "  ")
	if err != nil {
		return err
	}
	// 文件写入段在 writeMu 内：与锁外写盘的 FlushIfDirty 串行化，避免两者
	// 交错写 tmp/rename 写坏文件（锁序 mu → writeMu，见 UserDict.writeMu）。
	if _, err := u.writeState(data, u.writes); err != nil {
		return err
	}
	u.dirty = false
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
	// 清空也是一次全量状态变更：递增代数，让并发中的旧快照（gen 更小）
	// 在 writeState 的代数守卫里被丢弃，避免清空结果被 FlushIfDirty 的
	// 旧数据覆盖（2026-10-30 审计 P1）。
	u.writes++
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

// QueryPhrasesByWordPrefix 按**词字面**前缀查用户合成词组（与音节前缀的
// QueryPhrasesPrefix 互补）：联想续接按上屏词的字走，同音不同字（测试/
// 策士）的续接词组应当不同（2026-10-28 用户反馈）。线性扫描用户词组
// （数千条量级）足够快。
func (u *UserDict) QueryPhrasesByWordPrefix(prefix string) []*Entry {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if prefix == "" {
		return nil
	}
	var out []*Entry
	for _, rec := range u.file.Phrases {
		if len(rec.Word) > len(prefix) && strings.HasPrefix(rec.Word, prefix) {
			out = append(out, &Entry{
				Word:      rec.Word,
				Syllables: strings.Fields(rec.Sylls),
				Freq:      u.phraseScore(rec),
				Segs:      append([]string(nil), rec.Segs...),
			})
		}
	}
	return out
}

// QueryWordsByWordPrefix 按**词字面**前缀查用户调频词条（Words 表）——
// 用户整句上屏/选词的调频记录都在这张表，是「之前输入过的词」的主体
// （Phrases 只存词组进化合成的词）。联想续接按上屏词的字走时必须查它，
// 否则用户打过的词不会出现在联想候选（2026-10-28 用户反馈）。
// 返回按调频分降序的词条（Freq 已含次数×衰减）。
func (u *UserDict) QueryWordsByWordPrefix(prefix string) []*Entry {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if prefix == "" {
		return nil
	}
	var out []*Entry
	for key, rec := range u.file.Words {
		word, sylls, ok := parseUserKey(key)
		if !ok {
			continue
		}
		if len(word) > len(prefix) && strings.HasPrefix(word, prefix) {
			out = append(out, &Entry{
				Word:      word,
				Syllables: sylls,
				Freq:      int(float64(rec.Count*UserBoostPerCount) * decay(rec.LastUsed, u.now())),
				Segs:      append([]string(nil), rec.Segs...),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Freq != out[j].Freq {
			return out[i].Freq > out[j].Freq
		}
		return out[i].Word < out[j].Word
	})
	return out
}
