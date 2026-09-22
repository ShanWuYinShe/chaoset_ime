package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScoreDecayBuckets(t *testing.T) {
	u := mustUserDict(t, "")
	base := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	now := base
	u.setClock(func() time.Time { return now })
	if err := u.BumpWord("你好", []string{"ni", "hao"}); err != nil {
		t.Fatalf("BumpWord 失败: %v", err)
	}
	cases := []struct {
		offsetDays int
		want       int
	}{
		{0, UserBoostPerCount},                      // 当天：不衰减
		{3, int(float64(UserBoostPerCount) * 0.6)},  // 3 天：0.6
		{20, int(float64(UserBoostPerCount) * 0.3)}, // 20 天：0.3
		{40, int(float64(UserBoostPerCount) * 0.1)}, // 40 天：0.1
	}
	for _, c := range cases {
		now = base.AddDate(0, 0, c.offsetDays)
		if got := u.Score("你好", []string{"ni", "hao"}, ""); got != c.want {
			t.Errorf("+%d 天 Score = %d, 期望 %d", c.offsetDays, got, c.want)
		}
	}
}

func TestScoreBigramBonus(t *testing.T) {
	u := mustUserDict(t, "")
	if got := u.Score("试试", []string{"shi", "shi"}, "实施"); got != 0 {
		t.Fatalf("无记录时 Score = %d, 期望 0", got)
	}
	if err := u.BumpBigram("实施", "试试"); err != nil {
		t.Fatalf("BumpBigram 失败: %v", err)
	}
	if got := u.Score("试试", []string{"shi", "shi"}, "实施"); got != BigramBonusPerCount {
		t.Fatalf("共现 1 次加分 = %d, 期望 %d", got, BigramBonusPerCount)
	}
	u.BumpBigram("实施", "试试")
	u.BumpBigram("实施", "试试")
	if got := u.Score("试试", []string{"shi", "shi"}, "实施"); got != 2*BigramBonusPerCount {
		t.Fatalf("共现 3 次应封顶在 2 次加分，得到 %d", got)
	}
	if got := u.Score("试试", []string{"shi", "shi"}, ""); got != 0 {
		t.Fatalf("无上下文时不应加分，得到 %d", got)
	}
}

func TestUserDictLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	legacy := `{"你好\u0000ni hao": {"count": 2, "last_used": 100}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("加载旧格式失败: %v", err)
	}
	// last_used=100（1970 年）→ 衰减 0.1：2×500×0.1 = 100
	if got := u.Score("你好", []string{"ni", "hao"}, ""); got != 100 {
		t.Fatalf("旧格式迁移后 Score = %d, 期望 100", got)
	}
	// 再次落盘应升级为新格式
	if err := u.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var probe map[string]json.RawMessage
	if json.Unmarshal(data, &probe) != nil || len(probe["phrases"]) == 0 {
		t.Fatalf("保存后应升级为新格式（含 phrases 键）")
	}
}

func TestQueryPhrases(t *testing.T) {
	u := mustUserDict(t, "")
	if err := u.BumpPhrase("单位", []string{"dan", "wei"}); err != nil {
		t.Fatal(err)
	}
	got := u.QueryPhrases([]string{"dan", "wei"}, "")
	if len(got) != 1 || got[0].Word != "单位" || got[0].Freq != UserBoostPerCount {
		t.Fatalf("精确查询词组失败: %+v", got)
	}
	if got := u.QueryPhrases([]string{"dan"}, "w"); len(got) != 1 || got[0].Word != "单位" {
		t.Fatalf("前缀查询词组失败: %+v", got)
	}
	if got := u.QueryPhrases([]string{"dan"}, "x"); len(got) != 0 {
		t.Fatalf("不匹配前缀应为空: %+v", got)
	}
}
