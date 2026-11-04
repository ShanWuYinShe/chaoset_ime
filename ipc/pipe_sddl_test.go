package ipc

import (
	"strings"
	"testing"
)

// 命名管道安全描述符是 AppContainer 支持的核心契约（Windows 11 开始菜单、
// 搜索框等低完整性宿主必须能连上并写入 daemon 的管道）。Windows 专属代码在
// CI 里只交叉编译、不运行，所以把字符串契约放到平台无关测试里固定住：
// 任何一段被删掉都会让「开始菜单里打不出字」以静默回归的方式重现。
const testUserSID = "S-1-5-21-1111111111-2222222222-3333333333-1001"

func TestPipeSecurityDescriptorAllowsAppContainer(t *testing.T) {
	sd := pipeSecurityDescriptor(testUserSID)

	// DACL：当前用户 + SYSTEM 完全控制，不放开给其他本地用户。
	if !strings.Contains(sd, "(A;;GA;;;SY)") {
		t.Fatalf("描述符缺少 SYSTEM 完全控制: %s", sd)
	}
	if !strings.Contains(sd, "(A;;GA;;;"+testUserSID+")") {
		t.Fatalf("描述符缺少当前用户完全控制: %s", sd)
	}
	// ALL APPLICATION PACKAGES（AppContainer 宿主）必须可读写。
	if !strings.Contains(sd, "(A;;GRGW;;;S-1-15-2-1)") {
		t.Fatalf("描述符缺少 ALL APPLICATION PACKAGES 读写（AppContainer 宿主将连不上）: %s", sd)
	}
	// 强制完整性标签：低完整性方不被 no-write-up 拦住。
	if !strings.Contains(sd, "S:(ML;;NW;;;LW)") {
		t.Fatalf("描述符缺少低完整性 NO_WRITE_UP 标签（AppContainer 能连但写不进）: %s", sd)
	}
	// 不得放开给 Everyone / Authenticated Users：管道承载击键内容。
	for _, forbidden := range []string{";;WD)", ";;AU)", "S-1-1-0", "S-1-5-11"} {
		if strings.Contains(sd, forbidden) {
			t.Fatalf("描述符不得对全体用户开放（含 %q）: %s", forbidden, sd)
		}
	}
	// 受保护描述符：不继承父对象 ACE。
	if !strings.HasPrefix(sd, "D:P") {
		t.Fatalf("描述符应为受保护形式（D:P）: %s", sd)
	}
}

// 兜底描述符：去掉完整性标签，但仍保留 DACL（且同样不放开给全体用户）。
func TestPipeSecurityDescriptorFallback(t *testing.T) {
	sd := pipeSecurityDescriptorNoIntegrity(testUserSID)
	if strings.Contains(sd, "S:(ML") {
		t.Fatalf("兜底描述符不应包含完整性标签: %s", sd)
	}
	for _, want := range []string{"(A;;GA;;;SY)", "(A;;GA;;;" + testUserSID + ")", "(A;;GRGW;;;S-1-15-2-1)"} {
		if !strings.Contains(sd, want) {
			t.Fatalf("兜底描述符缺少 %q: %s", want, sd)
		}
	}
	for _, forbidden := range []string{";;WD)", ";;AU)", "S-1-1-0"} {
		if strings.Contains(sd, forbidden) {
			t.Fatalf("兜底描述符不得对全体用户开放（含 %q）: %s", forbidden, sd)
		}
	}
}
