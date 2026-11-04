package ipc

import "fmt"

// 命名管道的安全描述符。这里刻意不带 build tag：描述符是纯字符串契约，
// 放在平台无关文件里才能在 Linux CI 上直接断言（Windows 专属代码在 CI 里
// 只交叉编译、不运行，ACL 写错不会有任何测试变红）。
//
// 三段缺一不可：
//
//   - DACL：当前用户 + SYSTEM 完全控制，ALL APPLICATION PACKAGES(S-1-15-2-1)
//     读写。后者是 Windows 11 开始菜单、搜索框等 AppContainer 宿主能连上
//     daemon 的前提；其他本地用户仍然连不上（没有 Everyone/Authenticated Users）。
//   - SACL 强制完整性标签（ML;;NW;;;LW）：daemon 以普通（中等）完整性运行，
//     而 AppContainer 宿主是**低**完整性。按 MIC 的 no-write-up 默认策略，
//     低完整性进程对没有显式完整性标签的对象只能读不能写——表现就是
//     「开始菜单里按了键、daemon 也在、却没有候选」。加上 NO_WRITE_UP 标签
//     后低完整性方被允许写，更高完整性进程不受影响。
//   - P 前缀：描述符受保护，不继承父对象的 ACE。
func pipeSecurityDescriptor(userSID string) string {
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)(A;;GRGW;;;S-1-15-2-1)S:(ML;;NW;;;LW)", userSID)
}

// pipeSecurityDescriptorNoIntegrity 是不带完整性标签的兜底描述符：个别
// 系统/策略下设置 SACL 可能被拒，此时宁可降级成「只有 DACL」也不能让
// daemon 起不来——AppContainer 宿主写管道会失败，但普通程序照常可用。
func pipeSecurityDescriptorNoIntegrity(userSID string) string {
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)(A;;GRGW;;;S-1-15-2-1)", userSID)
}
