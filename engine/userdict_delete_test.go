package engine

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// 学到的词与进化词组删除后必须从查询里彻底消失（含重启重载后），
// 重复删除返回 false 且不报错，其它词的学习记录不受影响。
func TestUserDictDeleteWordRemovesLearnData(t *testing.T) {
	u, path := newQueryTestUserDict(t)
	bumpN(t, 3, func() error { return u.BumpWord("今天", []string{"jin", "tian"}) })
	bumpN(t, 2, func() error { return u.BumpPhrase("单位", []string{"dan", "wei"}) })
	bumpN(t, 1, func() error { return u.BumpWord("你好", []string{"ni", "hao"}) })

	before := u.Stats()
	deleted, err := u.DeleteWord("今天", []string{"jin", "tian"})
	if err != nil {
		t.Fatalf("DeleteWord 失败: %v", err)
	}
	if !deleted {
		t.Fatal("删除已学习的词应返回 true")
	}
	if got := u.Score("今天", []string{"jin", "tian"}, ""); got != 0 {
		t.Fatalf("删除后「今天」调频分 = %d，期望 0", got)
	}
	hits := u.QueryTokensExact([]Token{{Text: "jin", IsExact: true}, {Text: "tian", IsExact: true}}, 10)
	for _, e := range hits {
		if e.Word == "今天" {
			t.Fatalf("删除后仍能查到「今天」: %+v", hits)
		}
	}

	deleted, err = u.DeleteWord("单位", []string{"dan", "wei"})
	if err != nil || !deleted {
		t.Fatalf("删除词组失败: deleted=%v err=%v", deleted, err)
	}
	if got := u.PhraseCount("单位", []string{"dan", "wei"}); got != 0 {
		t.Fatalf("删除后词组计数 = %d，期望 0", got)
	}
	if got := u.QueryPhrases([]string{"dan", "wei"}, ""); len(got) != 0 {
		t.Fatalf("删除后精确词组查询应为空: %+v", got)
	}

	after := u.Stats()
	if after.Words != before.Words-1 {
		t.Fatalf("Words 条数 %d → %d，期望减少 1", before.Words, after.Words)
	}
	if after.Phrases != before.Phrases-1 {
		t.Fatalf("Phrases 条数 %d → %d，期望减少 1", before.Phrases, after.Phrases)
	}

	// 其它词不受影响
	if got := u.Score("你好", []string{"ni", "hao"}, ""); got != UserBoostPerCount {
		t.Fatalf("删除「今天/单位」不应影响「你好」，Score = %d", got)
	}

	// 重复删除：幂等，返回 false 且不报错
	deleted, err = u.DeleteWord("今天", []string{"jin", "tian"})
	if err != nil || deleted {
		t.Fatalf("重复删除应返回 (false, nil)，实际 (%v, %v)", deleted, err)
	}

	// 重启（重新加载文件）后仍是删除态、文件仍可严格解析
	if err := u.Save(); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	reloaded, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("重载失败: %v", err)
	}
	if got := reloaded.Score("今天", []string{"jin", "tian"}, ""); got != 0 {
		t.Fatalf("重载后「今天」仍有调频分 %d", got)
	}
	if got := reloaded.PhraseCount("单位", []string{"dan", "wei"}); got != 0 {
		t.Fatalf("重载后「单位」词组计数 %d", got)
	}
	// 重载用真实时钟：注入时钟写入的 last_used 会按真实时间衰减，
	// 只断言「仍在」（> 0），不锁定具体衰减值。
	if got := reloaded.Score("你好", []string{"ni", "hao"}, ""); got <= 0 {
		t.Fatalf("重载后「你好」应保留学习记录，Score = %d", got)
	}
}

// 精确键未命中（同词不同读音/分词）时按词文本兜底删除：候选窗右键传的是
// 候选音节的精确键；CLI 可省略音节只给词。两条路径都要真的删掉。
func TestUserDictDeleteWordFallbackByWord(t *testing.T) {
	u, _ := newQueryTestUserDict(t)
	// 「长」只学过 zhang 读音；调用方按 chang（词典读音）请求删除。
	if err := u.BumpWord("长", []string{"zhang"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	if err := u.BumpPhrase("长", []string{"chang", "du"}); err != nil {
		t.Fatalf("BumpPhrase 失败: %v", err)
	}
	deleted, err := u.DeleteWord("长", []string{"chang"})
	if err != nil || !deleted {
		t.Fatalf("按词兜底删除应命中，实际 (%v, %v)", deleted, err)
	}
	if got := u.Score("长", []string{"zhang"}, ""); got != 0 {
		t.Fatalf("兜底删除后「长 zhang」仍有调频分 %d", got)
	}
	if got := u.PhraseCount("长", []string{"chang", "du"}); got != 0 {
		t.Fatalf("兜底删除后「长 chang du」词组计数 %d", got)
	}

	// 空词是调用方错误（IPC/CLI 会先拦截），不得悄悄成功
	if deleted, err := u.DeleteWord("", nil); err == nil || deleted {
		t.Fatalf("空词删除应返回错误，实际 (%v, %v)", deleted, err)
	}
}

// 整句式学习记录（Words 带 Segs）也按词删除，不能留下整句候选/联想。
func TestUserDictDeleteWordWithSegs(t *testing.T) {
	u, _ := newQueryTestUserDict(t)
	if err := u.BumpWordWithSegs("今天星期一", []string{"jin", "tian", "xing", "qi", "yi"},
		[]string{"今天", "星期一"}); err != nil {
		t.Fatalf("BumpWordWithSegs 失败: %v", err)
	}
	// 整句记录的音节数 = 5，必须用完整音节序列精确查询（部分输入时
	// 整句候选由 compose 层的 isSentenceEntry 过滤，不在这里验证）。
	tokens := []Token{
		{Text: "jin", IsExact: true}, {Text: "tian", IsExact: true},
		{Text: "xing", IsExact: true}, {Text: "qi", IsExact: true},
		{Text: "yi", IsExact: true},
	}
	if got := u.QueryTokensExact(tokens, 10); len(got) != 1 || got[0].Word != "今天星期一" {
		t.Fatalf("学习后应能查到整句，实际 %+v", got)
	}
	deleted, err := u.DeleteWord("今天星期一", []string{"jin", "tian", "xing", "qi", "yi"})
	if err != nil || !deleted {
		t.Fatalf("DeleteWord 失败: (%v, %v)", deleted, err)
	}
	if got := u.QueryTokensExact(tokens, 10); len(got) != 0 {
		t.Fatalf("删除后整句候选应消失，实际 %+v", got)
	}
}

// 延迟落盘模式：删除只标脏，FlushIfDirty 后重载可见。
func TestUserDictDeleteWordPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("NewUserDict: %v", err)
	}
	u.SetDeferred(true)
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	if _, err := u.FlushIfDirty(); err != nil {
		t.Fatalf("FlushIfDirty: %v", err)
	}
	if deleted, err := u.DeleteWord("你好", []string{"ni", "hao"}); err != nil || !deleted {
		t.Fatalf("DeleteWord: (%v, %v)", deleted, err)
	}
	// 延迟模式：删除只标脏，此刻文件内容仍是删除前状态（与 bump 同契约）
	wrote, err := u.FlushIfDirty()
	if err != nil || !wrote {
		t.Fatalf("删除后 FlushIfDirty 应写盘: wrote=%v err=%v", wrote, err)
	}
	reloaded, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("重载: %v", err)
	}
	if got := reloaded.Score("你好", []string{"ni", "hao"}, ""); got != 0 {
		t.Fatalf("重载后仍能查到已删除的词，Score = %d", got)
	}
}

// 立即落盘模式（CLI 本地文件模式/测试默认）：删除当场原子写盘，重载即已删除。
func TestUserDictDeleteWordImmediatePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("NewUserDict: %v", err)
	}
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord: %v", err)
	}
	if deleted, err := u.DeleteWord("你好", []string{"ni", "hao"}); err != nil || !deleted {
		t.Fatalf("DeleteWord: (%v, %v)", deleted, err)
	}
	reloaded, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("重载: %v", err)
	}
	if got := reloaded.Score("你好", []string{"ni", "hao"}, ""); got != 0 {
		t.Fatalf("立即落盘模式删除后重载仍有调频分 %d", got)
	}
}

// Bigram（上下文共现）刻意保留：删除单词不删成对统计，最多残留
// ≤2×BigramBonusPerCount 的上下文加分（语义边界见 DeleteWord 注释）。
// 本测试锁定该设计，防止后来者误以为「删除不彻底」而顺手改坏成对统计。
func TestUserDictDeleteWordKeepsBigrams(t *testing.T) {
	u, _ := newQueryTestUserDict(t)
	if err := u.BumpWord("今天", []string{"jin", "tian"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	if err := u.BumpBigram("昨天", "今天"); err != nil {
		t.Fatalf("BumpBigram 失败: %v", err)
	}
	if deleted, err := u.DeleteWord("今天", []string{"jin", "tian"}); err != nil || !deleted {
		t.Fatalf("DeleteWord: (%v, %v)", deleted, err)
	}
	if got := u.PairCount("昨天", "今天"); got != 1 {
		t.Fatalf("bigram 应保留，实际 PairCount = %d", got)
	}
	if got := u.Score("今天", []string{"jin", "tian"}, "昨天"); got != BigramBonusPerCount {
		t.Fatalf("保留的 bigram 应只贡献上下文加分 %d，实际 Score = %d",
			BigramBonusPerCount, got)
	}
}

// 并发安全（配合 go test -race）：删除与 bump/查询/flush/clear 交错执行
// 不得 panic / 数据竞争，收尾后统计一致。
func TestUserDictDeleteWordConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("NewUserDict: %v", err)
	}
	u.SetDeferred(true)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				switch (i + j) % 5 {
				case 0:
					_ = u.BumpWord("你好", []string{"ni", "hao"})
				case 1:
					_, _ = u.DeleteWord("你好", []string{"ni", "hao"})
				case 2:
					_, _ = u.DeleteWord("你好", nil)
				case 3:
					_ = u.Score("你好", []string{"ni", "hao"}, "")
					_ = u.QueryTokensExact([]Token{{Text: "ni", IsExact: true}}, 10)
				case 4:
					_, _ = u.FlushIfDirty()
				}
			}
		}(i)
	}
	wg.Wait()
	if err := u.Save(); err != nil {
		t.Fatalf("收尾 Save 失败: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("并发删除后文件应存在: %v", err)
	}
}
