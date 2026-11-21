// 本文件是 chaoset-userdict 的「直接操作文件」模式（-userdict）所用实现：
// 不依赖 Go 引擎，只负责用户词典 JSON 的读写与统计/清空/删除/导出。
//
// 运行时（daemon）的学习数据管理与落盘在 Rust（crates/chaoset-engine 的
// userdict 模块）里；这里只服务命令行工具的无 daemon 场景，两者文件格式
// 必须保持一致（users 表兼容旧的扁平格式）。

package userdict

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

// userRecord 是「词/上下文」的调频记录（与 Rust 存储格式同名字段）。
type userRecord struct {
	Count    int      `json:"count"`
	LastUsed int64    `json:"last_used"`
	Segs     []string `json:"segs,omitempty"`
}

// phraseRecord 是用户合成词组（同词多读音用 Sylls 区分）。
type phraseRecord struct {
	Word     string   `json:"word"`
	Sylls    string   `json:"sylls"`
	Segs     []string `json:"segs,omitempty"`
	Count    int      `json:"count"`
	LastUsed int64    `json:"last_used"`
}

type localUserFile struct {
	Words   map[string]*userRecord   `json:"words"`
	Phrases map[string]*phraseRecord `json:"phrases"`
	Bigrams map[string]*userRecord   `json:"bigrams"`
}

// localUserDict 是本地文件形态的用户词典（path 非空时落盘）。
type localUserDict struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	file localUserFile
}

const localUserKeySep = "\x00"

func localUserKey(word string, syllables []string) string {
	return word + localUserKeySep + strings.Join(syllables, " ")
}

func parseLocalUserKey(key string) (word string, syllables []string, ok bool) {
	word, joined, found := strings.Cut(key, localUserKeySep)
	if !found || word == "" {
		return "", nil, false
	}
	if joined == "" {
		return word, nil, true
	}
	return word, strings.Fields(joined), true
}

func newLocalUserDict(path string) (*localUserDict, error) {
	u := &localUserDict{
		path: path,
		now:  time.Now,
		file: localUserFile{
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

// load 解析用户词典；兼容阶段 0 的扁平格式（顶层直接是 词 → 记录）。
func (u *localUserDict) load(data []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if _, isNew := probe["phrases"]; isNew {
		return json.Unmarshal(data, &u.file)
	}
	var legacy map[string]*userRecord
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	u.file.Words = legacy
	return nil
}

// Save 原子落盘（同目录 temp + rename，与 daemon 落盘方式一致）。
func (u *localUserDict) Save() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.saveLocked()
}

func (u *localUserDict) saveLocked() error {
	if u.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(u.file, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(u.path)
	tmp, err := os.CreateTemp(dir, ".chaoset-userdict-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	remove := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		remove()
		return fmt.Errorf("写入用户词典失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		remove()
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		remove()
		return fmt.Errorf("设置用户词典权限失败: %w", err)
	}
	if err := os.Rename(tmpName, u.path); err != nil {
		remove()
		return fmt.Errorf("替换用户词典失败: %w", err)
	}
	return nil
}

// BumpWord 记录一次上屏（词 + 音节），立即落盘。
func (u *localUserDict) BumpWord(word string, syllables []string) error {
	if word == "" || len(syllables) == 0 {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	key := localUserKey(word, syllables)
	rec := u.file.Words[key]
	if rec == nil {
		rec = &userRecord{}
		u.file.Words[key] = rec
	}
	rec.Count++
	rec.LastUsed = u.now().Unix()
	return u.saveLocked()
}

// BumpPhrase 记录一次用户词组上屏，立即落盘。
func (u *localUserDict) BumpPhrase(word string, syllables []string) error {
	if word == "" || len(syllables) == 0 {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	key := localUserKey(word, syllables)
	rec := u.file.Phrases[key]
	if rec == nil {
		rec = &phraseRecord{Word: word, Sylls: strings.Join(syllables, " ")}
		u.file.Phrases[key] = rec
	}
	rec.Count++
	rec.LastUsed = u.now().Unix()
	return u.saveLocked()
}

// Stats 返回三类学习记录的条目数。
func (u *localUserDict) Stats() (words, phrases, bigrams int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.file.Words), len(u.file.Phrases), len(u.file.Bigrams)
}

// Score 返回某词的调频分（0 = 没有学习记录）。本地工具只需区分「有/无」，
// 与引擎同口径地按次数与时间衰减合成一个正整数。
func (u *localUserDict) Score(word string, syllables []string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	rec := u.file.Words[localUserKey(word, syllables)]
	if rec == nil || rec.Count <= 0 {
		return 0
	}
	return localDecayedBoost(rec, u.now())
}

func localDecayedBoost(rec *userRecord, now time.Time) int {
	days := now.Sub(time.Unix(rec.LastUsed, 0)).Hours() / 24
	factor := 1.0
	switch {
	case days < 1:
	case days < 7:
		factor = 0.6
	case days < 30:
		factor = 0.3
	default:
		factor = 0.1
	}
	boost := int(float64(rec.Count) * factor * 500)
	if boost <= 0 {
		boost = 1
	}
	return boost
}

// PhraseCount 返回某用户词组的累计次数（0 = 没有该词组）。
func (u *localUserDict) PhraseCount(word string, syllables []string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	if rec := u.file.Phrases[localUserKey(word, syllables)]; rec != nil {
		return rec.Count
	}
	return 0
}

// Clear 清空全部学习记录并落盘。
func (u *localUserDict) Clear() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.file = localUserFile{
		Words:   make(map[string]*userRecord),
		Phrases: make(map[string]*phraseRecord),
		Bigrams: make(map[string]*userRecord),
	}
	return u.saveLocked()
}

// DeleteWord 删除一个词的学习记录（Words + Phrases），返回是否删到了东西。
// 与 daemon 语义一致：给了音节先按精确键删；没命中再按词文本兜底删同词
// 的其它读音记录；Bigrams 刻意保留（成对统计，删单侧会扭曲其它词）。
func (u *localUserDict) DeleteWord(word string, syllables []string) (bool, error) {
	if word == "" {
		return false, fmt.Errorf("待删除的词不能为空")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	deleted := false
	if len(syllables) > 0 {
		key := localUserKey(word, syllables)
		if _, ok := u.file.Words[key]; ok {
			delete(u.file.Words, key)
			deleted = true
		}
		if _, ok := u.file.Phrases[key]; ok {
			delete(u.file.Phrases, key)
			deleted = true
		}
	}
	if !deleted {
		for key := range u.file.Words {
			if w, _, ok := parseLocalUserKey(key); ok && w == word {
				delete(u.file.Words, key)
				deleted = true
			}
		}
		for key, rec := range u.file.Phrases {
			if rec.Word == word {
				delete(u.file.Phrases, key)
				deleted = true
			}
		}
	}
	if !deleted {
		return false, nil
	}
	return true, u.saveLocked()
}

// ExportTSV 导出学习记录：默认分词/词组/上下文三段（# 注释段头），
// phrasesOnly 只导出「词\t拼音\t次数」三列（可直接作为 chaoset-import 输入）。
func (u *localUserDict) ExportTSV(w io.Writer, phrasesOnly bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	bw := bufio.NewWriter(w)
	if phrasesOnly {
		if err := u.writePhrases(bw, false); err != nil {
			return err
		}
		return bw.Flush()
	}
	if err := u.writeWords(bw); err != nil {
		return err
	}
	if err := u.writePhrases(bw, true); err != nil {
		return err
	}
	if err := u.writeBigrams(bw); err != nil {
		return err
	}
	return bw.Flush()
}

func (u *localUserDict) writeWords(bw *bufio.Writer) error {
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
		word, sylls, valid := parseLocalUserKey(key)
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

func (u *localUserDict) writePhrases(bw *bufio.Writer, header bool) error {
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

func (u *localUserDict) writeBigrams(bw *bufio.Writer) error {
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
		prev, next, _ := strings.Cut(key, localUserKeySep)
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
