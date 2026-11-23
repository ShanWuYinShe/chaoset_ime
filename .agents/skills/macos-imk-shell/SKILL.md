---
name: macos-imk-shell
description: chaoset_ime 的 macOS 输入法壳（IMK 控制器 + AppKit 候选窗）UI 生命周期与排障：候选窗失焦不消失/截走鼠标、切应用或切 Space 后候选窗残留、联想候选窗关不掉、IMK 回调覆盖不到的场景、非 IMK 回调里取 client 崩溃。触发：候选窗留在别的应用上面、鼠标事件被候选窗吃掉、Cmd+Tab / 切换 Space 后候选窗不消失、联想候选窗残留、输入法进程崩溃栈里出现 IMKInputController client。
---

# macOS 输入法壳（IMK）UI 生命周期

适用于 `shims/macos/src/`：`ChaosetController.swift`（IMK 控制器）、`KeyRouter.swift`（按键路由）、
`CandidatePanel*.swift`（AppKit 候选窗）。三端语义以 Windows `OnSetFocus(FALSE)` 与 Linux
`focus_out` 为参照：**失焦 = 会话结束**——已确认段落盘、剩余拼音丢弃、联想退出、候选窗与提示收起。

## 一、IMK 回调的覆盖盲区（最常踩）

`commitComposition(_:)` 与 `deactivateServer(_:)` **只在客户端文本焦点变化时**被调用，
下列场景一个都不触发：

| 场景 | 是否回调 IMK | 后果（只靠 IMK 收尾时） |
|---|---|---|
| Cmd+Tab / Dock / 点击别的应用切走 | 否（组词挂在原应用里是 macOS 常规行为） | 候选窗留在新应用上方，`ignoresMouseEvents=false` 吃掉该区域点击 |
| 切换 Space / 全屏应用 | 否 | 同上（候选窗带 `.canJoinAllSpaces` 会跟到每个 Space） |
| 联想态（选词上屏后的续接候选窗） | 否（没有 marked text 可 commit） | 联想候选窗关不掉 |

判据：只要候选窗能被「键盘以外」的方式留下（切应用、切 Space、面板成为 key），就必须有
**进程级/窗口级**的兜底信号，不能只在 IMK 回调里 `hide()`。

## 二、现行实现（不要用全局钩子/辅助功能权限）

`ChaosetController` 的焦点守卫（`installFocusGuardIfNeeded`，只装一次）：

```swift
NSWorkspace.shared.notificationCenter.addObserver(
    forName: NSWorkspace.didActivateApplicationNotification, ...)   // 切应用
NSWorkspace.shared.notificationCenter.addObserver(
    forName: NSWorkspace.activeSpaceDidChangeNotification, ...)     // 切 Space
CandidatePanelController.onResignKey = { activeController?.abandonSessionOnFocusLoss() }
// ← 面板 NSWindow.didResignKeyNotification 的回调（面板若曾成为 key 的兜底）
```

- 收到的信号交给 `activeController`（`refresh` 入口登记，IMK 为每个输入上下文各建一个
  实例，只收尾持有候选窗的那个），调 `abandonSessionOnFocusLoss`；
- 收尾统一走 `endSession(client:)`（与 `commitComposition` 共用），焦点路径再 `refresh(lastClient)`
  清掉客户端 marked 区（`clearMarkedText` 有「忽略空插入」客户端兜底）；
- 自己进程激活要忽略（`nonactivatingPanel` 理论上不会成为前台应用，但不做假设）；
- **禁止**为此引入 `CGEventTap` / 辅助功能权限 / 全局键鼠钩子：候选窗只是普通窗口，
  没有特权（见 AGENTS.md「候选窗没有特权」）。

## 三、三个会让改动白做的坑

1. **非 IMK 回调里不要取 `self.client()`**：`IMKInputController.client` 在没有活跃 IMK 会话
   （如单测进程、通知回调时机）时直接 `EXC_BAD_ACCESS` 段错误，崩溃栈长这样：

   ```text
   InputMethodKit -[IMKInputController client]
   key_routing ChaosetController.commitComposition(_:)
   key_routing ChaosetController.abandonSessionOnFocusLoss()
   ```

   正确做法：`KeyRouter.handle(_:client:)` 入口记下 `lastClient`（`weak`），焦点路径用它。
2. **`engine.reset()` 返回空 `Comp` 而不是 `nil`**：判「有无待收尾内容」要看
   `current?.input.isEmpty == false || associateCandidates != nil || !confirmedText.isEmpty`，
   不能写 `guard current != nil`（永远通过，幂等断言会红）。
3. **别名状态别忘清**：`CandidatePanelController.hide()` 会清 `onSelect`/`onDelete` 等回调但
   不清 `onResignKey`（它由焦点守卫装一次），改回调生命周期时两边一起看。

## 四、验证方式

```sh
# 单元/回归层：合成 NSEvent 驱动控制器，直接调焦点信号入口
./shims/macos/tests/key_routing/run.sh        # 需先有 shims/macos/build/chaosetd

# 静态契约（三端实现 + 文档 + 变异测试）
go test ./tools/checkuiparity/ -run TestCheckCandidateNoPrivilege
make check-uiparity

# 类型检查（不必完整构建壳）
cd shims/macos && xcrun swiftc -typecheck -target $(uname -m)-apple-macos15.0 \
  $(ls src/*.swift | grep -v 'src/main.swift')
```

真机走查（CI 无 GUI 会话，覆盖不到）：打 `nihao` 不选词 → Cmd+Tab 到别的应用，候选窗必须
立即消失；回到原应用时下划线拼音已清、已确认段（渐进上屏选过的词）保留；联想候选窗同样消失。

崩溃排查：`~/Library/Logs/DiagnosticReports/key_routing*.ips`（第一行是 JSON 头，第二行起才是
正文，`json.loads` 要跳过首行）；解析故障线程栈定位是否落在 `IMKInputController`。

## 五、写 checker 断言的坑

变异测试用 `strings.Replace` 注入回退版本时，**不要用整文件 `checkContains` 找 needle**——
同一符号在文件别处也出现（`hideLookupTable()` 在 `focusOut`/`reset`/`hide` 里都有），断言会
被别处的同名调用骗过、变异测试永远绿。按函数体判：

```go
if body, line, ok := cppFunctionBody(stripCppComments(mustRead(fsys, path)), "void focusOut()"); ok {
    // 在 body 里找 needle
}
```

Swift 没有 `cppFunctionBody`，用 `strings.Contains(text, "整段特征串")` 且特征串要包含函数名与
相邻语句，保证变异后唯一失配。