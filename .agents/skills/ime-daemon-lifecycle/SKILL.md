---
name: ime-daemon-lifecycle
description: chaoset_ime 守护进程（chaosetd）与 macOS 输入源（TIS）/ Windows TSF 的生命周期排障：多实例、僵尸套接字、首字母丢失、菜单栏重复条目、LaunchAgent 自启、输入源被杀、Windows 安装后只有英文、命名管道/懒启动失败。触发：活动监视器里出现多个 chaosetd、切换输入法后首字母丢失或打不出字、菜单栏/系统设置出现多个 Chaoset、装完输入法从菜单栏消失、kill daemon 后不自动恢复、TIS 条目数不是 2、Windows 首键无候选。
---

# 守护进程与输入源生命周期（本项目专用）

两类高频现象根因相通：后台出现多个 chaosetd；切换到这个输入法后首字母丢失、打不出字。
按下面的顺序查最快。

## 零、系统高负载时打字卡顿 / 偶发丢拼音

**先查 daemon 的 QoS，不要先怀疑引擎或语言性能。**

- 现象：系统忙（编译/渲染/多任务满载）时按键明显延迟、候选窗慢半拍，
  偶发首字母丢失或整段拼音被清掉；空闲时一切正常。
- 根因：macOS LaunchAgent 未声明 `ProcessType` 时按 **Background** 调度，
  系统高负载下 CPU 被节流；输入法 daemon 的 IPC 往返 p99 会从 ~7ms 恶化到
  数百毫秒，超过壳侧 250ms 收发超时后按键被 passThrough/丢组词。
- 检查：`plutil -p ~/Library/LaunchAgents/ime.chaoset.daemon.plist | grep ProcessType`
  必须看到 `"ProcessType" => "Interactive"`。
- 修复：`shims/macos/install.sh` 生成 plist 时已带
  `<key>ProcessType</key><string>Interactive</string>`；手工补 key 后
  `launchctl bootout gui/$(id -u) <plist>` + `launchctl bootstrap gui/$(id -u) <plist>`
  让新调度生效。
- 实测（8 核 `yes` 满载、600 次 IPC 采样）：
  未声明 ProcessType 时 p50 13ms / p99 452ms / max 775ms / 27 次 >250ms；
  声明 Interactive 后 p50 4ms / p99 17ms / max 31ms / 零超时；空闲 p99 ~7ms 不变。

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

成因：daemon 启动时若在 `net.Listen` 前 `os.Remove(socket)`，会误删另一个运行中
daemon 的套接字。现行实现用 flock 单实例锁规避（`cmd/chaosetd/singleton_unix.go`）。

### 第 2 步：daemon 是否被 SIGTERM 打断

```bash
launchctl print "gui/$(id -u)/ime.chaoset.daemon" | grep -E 'state|last exit'
```

`launchctl kickstart -k` 与各平台 init 都用 **SIGTERM**。反例：只监听 SIGINT——进程被
直接杀死 → 用户词典来不及落盘、套接字残留成僵尸。现行实现同时监听 SIGTERM 并在退出时
删除套接字。

### 第 3 步：壳在 IPC 失败时是否吞键

见 `shims/macos/src/ChaosetController.swift` 的字母分支。**铁律：任何情况下
都不能丢键**——daemon 不可达时必须 `passThrough` 放行原键，绝不能
`current = nil; return true`（那正是首字母凭空消失的直接原因）。

### 第 4 步：超时是否与 GC 尖峰咬合

```bash
# daemon 单键处理 p50 约 1.5ms 但 p99 可达 51ms（GC），客户端超时 100ms 时
# 会偶发拿不到响应 → 壳判定失败 → 丢键。
```
现行实现：daemon `runtime/debug.SetGCPercent(400)`（p99 → 18ms），客户端超时
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
2. **父容器被禁用**：反例是 `tis sync` 把父容器 `ime.chaoset.inputmethod.ChaosetIME`
   也 disable——它是 TIS 骨架节点（系统「简体拼音」同样是 mode+container
   两条且都启用）。禁用会触发系统清空 `AppleEnabledThirdPartyInputSources`，
   表现为**装完输入法从菜单栏整个消失**。现行 `tis sync` 只禁用 .Hans 历史模式。

正确的 TIS 结构（与系统输入法一致）：

| 条目 | type | selectable |
|---|---|---|
| `...ChaosetIME` | TISTypeKeyboardInputMethodModeEnabled | false（容器） |
| `...ChaosetIME.Pinyin` | TISTypeKeyboardInputMode | true（可点） |

**菜单栏只会列出 selectable 的那条**。若 `tis list` 是 2 条但菜单栏显示更多，
是 `TextInputMenuAgent` 持有陈旧缓存——`killall TextInputMenuAgent` 让它重建。

## 三、安装/自启相关

- **LaunchAgent 必须由 install.sh 写入**：反例是只 kickstart 已存在的 plist——全新机器
  没有该 plist，打不出中文。检查：`launchctl print gui/$(id -u)/ime.chaoset.daemon`。
- `bootout → bootstrap → kickstart` 三层有竞争：bootout 是异步的，要等它在域里
  真正消失再 bootstrap，且不要紧跟 kickstart（会杀掉刚拉起的进程）。
- 就绪判定**必须用真实 IPC 往返**（`build/smoke`），不能只看套接字文件存在
  或盲目 sleep。

## Windows 守护进程

安装器不启动 daemon。daemon 由当前交互用户启动，两处入口：登录时的
`HKLM\...\Run` 值（REG_EXPAND_SZ，按用户展开 `%LOCALAPPDATA%`）与 TSF 首次
激活/首键的懒启动；都传 `-pipe -seeddict`。

- 管道名 `\\.\pipe\chaoset-<当前用户 SID>`；ACL 当前用户 + SYSTEM +
  ALL APPLICATION PACKAGES。TCP 回退只用于旧版 daemon。
- 候选只有英文：先看 `%LOCALAPPDATA%\Chaoset\chaosetd.log` 是否启动、是否
  有 `ipc 连接 network=pipe`；再看 `tsf.log` 的 `daemon lazy start`、
  `pipe connect failed`、`daemon request failed`。AppContainer 宿主日志可能在
  `%LOCALAPPDATA%\Packages\<包族>\LocalState\Chaoset\`。
- 手动复现：`chaosetd.exe -pipe -dict ... -seeddict ... -userdict ... -log ...`，
  再用 `chaoset-cli.exe -pipe` 验 IPC；不要以管理员身份启动 daemon。
- 安装/升级不得强杀用户程序或系统输入宿主；被占用 DLL 由改名/重启替换。
- 设置窗导入词库后重启 daemon 走 detached 启动，不能用等待进程退出的
  `settingsRunProcess`。

## 四、验证命令（改完这一块必跑）

```bash
cd shims/macos
./verify.sh                    # 退出码 0=通过
./tests/equivalence.sh         # A 同源 / B 导入等价 / C 判定等价 / D 图标 / E 渲染尺寸 / F 双端视觉
./build/tis dedupe             # TIS 重复实例合并
```

## 五、CI 相关

### 本地只跑轻量项，重活交给 CI

提交前本地只需 `gofmt -l $(git ls-files '*.go')`（应无输出）与 `go build ./...`（秒级）；
本地跑测试会占用 CPU 影响正常使用电脑。其余由 6 个并行 job 覆盖（单次提交约 3-5 分钟出全部结论）：

| CI job | 覆盖内容 | 为什么不在本地跑 |
|---|---|---|
| lint | gofmt / go vet / workflow 结构 / 版本检查 / Windows GDI 契约 / 三端 UI 一致性（`check-uiparity`）/ 构建 | 快，但统一在 CI 跑保证一致 |
| test | go test ./...（数据契约/引擎/壳行为测试） | 约 30s |
| race | `go test -race` | 最重的一项，本机 45s+ |
| linux-e2e | 真实 ibus-daemon 下的组词/上屏/多实例隔离/中英切换/翻页；Linux 安装包自检（tar.gz/deb 必含设置窗模块） | 需要 dbus + ibus，占资源 |
| macos-build | macOS 壳构建 + universal 自检 + ZIP 安装包 + 等价性 + 双端视觉一致性 | swiftc 构建 2-3 分钟 |
| windows-build | Windows 壳 mingw 交叉编译 + `core_test`（真实 daemon）+ `makensis` 安装包编译 + ZIP 自检 | 需要 mingw-w64/NSIS，本机不装；每次提交都验证（不只在打 tag 时） |

本地真需要跑完整验证时，`make test`、`shims/linux/test-ci.sh`、
`shims/macos/tests/equivalence.sh` 都能直接执行（与 CI 同一套脚本）。

### 陷阱：concurrency 的 cancel-in-progress 会取消正在跑的旧流水

`ci.yml` 里配了 `concurrency: group: ci-${{ github.ref }}, cancel-in-progress: true`。
连续 push 时**上一个运行会被标记 cancelled**——看到 Linux E2E 报
`##[error]The operation was canceled.` 时，先确认是不是被新 push 顶掉的，
不要当成代码失败去排查：

```bash
gh run list --limit 3                                   # 是否同一分支连续两次
gh run view <旧id> --json conclusion --jq .conclusion    # cancelled = 被取消，非失败
```

### 限制：macOS runner 排队

GitHub 会提示 "Due to capacity constraints, jobs targeting macOS arm64 runners
may experience longer queue times"。macos-build 排队几分钟是正常现象，
不要因为「怎么还不开始」去改 workflow。

### linux-e2e 用 test-ci.sh（不用 test.sh）

`test.sh` 假设本地已用 `setup-container.sh` 起了 docker 容器并通过 `docker exec`
操作；GitHub runner 本身就是一次性 VM，再套 docker 要多花时间拉镜像、装 ibus。
`test-ci.sh` 直接在 runner 上起 dbus + ibus-daemon 跑同样的断言。

本机不要用 docker 预演这套 E2E（容器内装 ibus 耗时不可控），直接推 CI 跑。
