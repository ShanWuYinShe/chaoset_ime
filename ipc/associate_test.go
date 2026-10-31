package ipc

import (
	"strings"
	"testing"
)

// TestClientAssociateMethods：daemon 协议里的 associate / associateSelect
// 此前只有壳直接走 call，Go Client 没有便捷方法；补上后顺带覆盖 server
// dispatch 的联想分支（此前 0% 覆盖）。
func TestClientAssociateMethods(t *testing.T) {
	c := newIPCClient(t, "你好\tni hao\t99\n你\tni\t80\n好\thao\t85\n你好吗\tni hao ma\t70\n")
	if _, err := c.Input("ni"); err != nil {
		t.Fatalf("Input 失败: %v", err)
	}
	if _, err := c.Select(0); err != nil { // 上屏「你」，进入联想上下文
		t.Fatalf("Select 失败: %v", err)
	}
	cands, err := c.Associate([]string{"ni"})
	if err != nil {
		t.Fatalf("Associate 失败: %v", err)
	}
	if len(cands) == 0 {
		t.Fatal("上屏「你」后联想应有续接候选")
	}
	sylls := strings.Split(cands[0].Syllables, "'")
	committed, err := c.AssociateSelect(cands[0].Text, sylls)
	if err != nil {
		t.Fatalf("AssociateSelect 失败: %v", err)
	}
	if committed != cands[0].Text {
		t.Fatalf("AssociateSelect 上屏 %q，期望 %q", committed, cands[0].Text)
	}
}
