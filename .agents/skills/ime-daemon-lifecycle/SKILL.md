---
name: ime-daemon-lifecycle
description: chaoset_ime 守护进程（chaosetd）与 macOS 输入源（TIS）的生命周期排障：多实例、僵尸套接字、首字母丢失、菜单栏重复条目、LaunchAgent 自启、输入源被杀。触发：活动监视器里出现多个 chaosetd、切换输入法后首字母丢失或打不出字、菜单栏/系统设置出现多个 Chaoset、装完输入法从菜单栏消失、kill daemon 后不自动恢复、TIS 条目数不是 2。
---

# 守护进程与输入源生命周期（本项目专用）

这套排查来自 2026-10-26 用户实际遇到的两个现象：「后台有多个 chaosetd」与
「切换到我这个输入法后首字母丢失」。两者根因相通，按下面的顺序查最快。

## 一、「打不出字 / 首字母丢失」的排查顺序

**不要一上来怀疑壳的按键逻辑**，先确认 IPC 通道是否真的通。

### 第 1 步：套接字是不是僵尸

```bash
SOCK="$HOME/Library/Application Support/chaoset/chaoset.sock"
ls -li "$SOCK"                                   # 磁盘 inode
lsof -p $(pgrep -f 'Application Support/chaoset/chaosetd') | grep unix
python3 -c "
import socket,sys
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM); s.settimeout(3)
try: s.connect('$SOCK'); print('可连接')
except Exception as e: print('失败:',e)
"
```

判定：**套接字文件存在 ≠ 有进程在 listen**。若 connect 报
`ECONNREFUSED (errno 61)` 而 daemon 进程还在跑，就是僵尸——daemon 持有的
unix socket 对象（lsof 里那串 `0x...`）与磁盘上的 inode 不是同一个。

成因（2026-10-26 实测）：daemon 在 `net.Listen` 前 `os.Remove(socket)`，
这个删除分不清「残留文件」与「另一个运行中 daemon 的套接字」。已用 flock
单实例锁修复（`cmd/chaosetd/singleton_unix.go`）。

### 第 2 步：daemon 是否被 SIGTERM 打断

```bash
launchctl print "gui/$(id -u)/ime.chaoset.daemon" | grep -E 'state|last exit'
```

`launchctl kickstart -k` 与各平台 init 都用 **SIGTERM**。只监听 SIGINT 时
进程被直接杀死 → 用户词典来不及落盘、套接字残留成僵尸。已修（同时监听
SIGTERM 并在退出时删除套接字）。

### 第 3 步：壳在 IPC 失败时是否吞键

见 `shims/macos/src/ChaosetController.swift` 的字母分支。**铁律：任何情况下
都不能丢键**——daemon 不可达时必须 `passThrough` 放行原键，绝不能
`current = nil; return true`（那正是首字母凭空消失的直接原因）。

### 第 4 步：超时是否与 GC 尖峰咬合

```bash
# daemon 单键处理 p50 约 1.5ms 但 p99 可达 51ms（GC），客户端超时 100ms 时
# 会偶发拿不到响应 → 壳判定失败 → 丢键。
```
已修：daemon `runtime/debug.SetGCPercent(400)`（p99 → 18ms），客户端超时
放宽到 250ms，读类方法失败重试一次（写类不重试，避免学习数据记两遍）。

## 二、多实例 / 重复条目的排查

### 进程重复

```bash
pgrep -fl 'Application Support/chaoset/chaosetd'   # 应恰好 1 条
```

若 >1：先看是否**自己探测时起的临时进程**（路径是 /tmp/xxx.sock 而非
Application Support），别急着当成 bug。真正的双实例是单实例锁缺失导致的，
现在 `<socket>.lock` 的 flock 会拒绝第二个。

### 菜单栏/系统设置出现多个 Chaoset

两个独立原因，都要查：

1. **TIS 表里有重复实例**（同一 InputSourceID 出现两条属性相同、指针不同的记录）：
   ```bash
   cd shims/macos && ./build/tis list | grep -c chaoset   # 应为 2
   ```
   修复：`./build/tis dedupe`。原理是**全部 disable 让 TIS 合并记录，再只启用
   一个**——直接 enable 无效。
2. **父容器被禁用**：`tis sync` 曾把父容器 `ime.chaoset.inputmethod.ChaosetIME`
   也 disable，而它是 TIS 骨架节点（系统「简体拼音」同样是 mode+container
   两条且都启用）。禁用会触发系统清空 `AppleEnabledThirdPartyInputSources`，
   表现为**装完输入法从菜单栏整个消失**。已修：只禁用 .Hans 历史模式。

正确的 TIS 结构（与系统输入法一致）：

| 条目 | type | selectable |
|---|---|---|
| `...ChaosetIME` | TISTypeKeyboardInputMethodModeEnabled | false（容器） |
| `...ChaosetIME.Pinyin` | TISTypeKeyboardInputMode | true（可点） |

**菜单栏只会列出 selectable 的那条**。若 `tis list` 是 2 条但菜单栏显示更多，
是 `TextInputMenuAgent` 持有陈旧缓存——`killall TextInputMenuAgent` 让它重建。

## 三、安装/自启相关

- **LaunchAgent 必须由 install.sh 写入**（2026-10-26 前只 kickstart 已存在的
  plist，全新机器打不出中文）。检查：`launchctl print gui/$(id -u)/ime.chaoset.daemon`。
- `bootout → bootstrap → kickstart` 三层有竞争：bootout 是异步的，要等它在域里
  真正消失再 bootstrap，且不要紧跟 kickstart（会杀掉刚拉起的进程）。
- 就绪判定**必须用真实 IPC 往返**（`build/smoke`），不能只看套接字文件存在
  或盲目 sleep。

## 四、验证命令（改完这一块必跑）

```bash
cd shims/macos
./verify.sh                    # 退出码 0=通过（2026-10-26 前它永远返回 0，已修）
./tests/equivalence.sh         # A 同源 / B 导入等价 / C 判定等价 / D 图标 / E 渲染尺寸 / F 双端视觉
./build/tis dedupe             # TIS 重复实例合并
```
