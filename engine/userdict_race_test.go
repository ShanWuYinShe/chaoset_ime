package engine

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 并发安全：多会话共享同一 UserDict 时，bump/查询/导出/清空并发执行
// 不应产生数据竞争（配合 go test -race 运行）。真实场景即 daemon 的
// 多条 IPC 连接同时打字。
func TestUserDictConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.json")
	u, err := NewUserDict(path)
	if err != nil {
		t.Fatalf("创建用户词典失败: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess := NewSession(mustDict(t), u)
			for j := 0; j < 50; j++ {
				sess.Input("nihao")
				sess.Select(0)
				sess.Input("shi")
				sess.Punct(",")
				_ = u.Score("你好", []string{"ni", "hao"}, "")
				_ = u.QueryPhrases([]string{"ni", "hao"}, "")
				_ = u.Stats()
				var b strings.Builder
				_ = u.ExportTSV(&b, j%2 == 0)
			}
		}(i)
	}
	wg.Wait()

	// 收尾：清空与统计亦需并发安全
	if err := u.Clear(); err != nil {
		t.Fatalf("Clear 失败: %v", err)
	}
	if st := u.Stats(); st.Words != 0 || st.Phrases != 0 || st.Bigrams != 0 {
		t.Fatalf("Clear 后 Stats = %+v", st)
	}
}
