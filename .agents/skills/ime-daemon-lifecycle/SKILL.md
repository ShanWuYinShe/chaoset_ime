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

见 `shims/macos/src/KeyRouter.swift` 的字母分支（`ChaosetController.swift` 只保留 IMK 核心/生命周期）。**铁律：任何情况下
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

1. **TIS 表里有重复实例**（同一 InputSourceID 出现多条属性相同、指针不同的记录）：
   ```bash
   cd shims/macos && ./build/tis list | grep -c chaoset   # 应为 2（父容器 + Pinyin）
   ```
   根因是重复调用 `TISRegisterInputSource`：TIS 没有反注册 API，同一 bundle 每多调
   一次，**当前登录会话**的输入源表就多一条记录。预防靠安装/升级幂等：`tis register`
   先按 bundle id 查 TIS 表、已登记则跳过；`install.sh` / GUI 安装器 / `reregister.sh`
   统一走随包 `tis register`，**不要在安装/重注册代码里直接调 `TISRegisterInputSource`**
   （由 `checkuiparity` 的「macOS 输入源登记幂等」组断言）。
   已出现重复时：`./build/tis dedupe` 是尽力而为（全部 disable 让 TIS 合并、再只启用
   一个，直接 enable 无效）；**部分 macOS 版本上 disable/enable 不合并（条目数不变）**，
   此时唯一可靠修法是**注销重登**——登录扫描会按「已装 app + HIToolbox 首选项」重建表。
   重登前先确认 `AppleEnabledInputSources` 里只有 1 个父容器 + 1 个 Pinyin 条目：
   `defaults export com.apple.HIToolbox - | grep -c Chaoset`（备份用同一命令导出到文件）。
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
- **三端都有懒启动兜底**：LaunchAgent 缺失/被 bootout/安装后未重新登录时，
  `EngineClient.spawnDaemonIfNeeded` 会在连接失败后拉起 app 内 daemon（5 秒节流、
  不阻塞主线程），与 Windows TSF、Linux 引擎一致。排查时看
  `log show --predicate 'subsystem == "ime.chaoset.inputmethod"' --last 5m`：
  出现「chaosetd 懒启动已发起」说明是兜底路径在起作用，应回头查 LaunchAgent；
  出现「懒启动 daemon 失败」是 app 内 `Contents/Resources/chaosetd` 缺失或不可执行。
  launchd 已在跑时多起的实例会因 `<socket>.lock` flock 失败立刻退出，属正常。
- `bootout → bootstrap → kickstart` 三层有竞争：bootout 是异步的，要等它在域里
  真正消失再 bootstrap，且不要紧跟 kickstart（会杀掉刚拉起的进程）。
- 就绪判定**必须用真实 IPC 往返**（`build/smoke`），不能只看套接字文件存在
  或盲目 sleep。

## Windows 守护进程

安装器不启动 daemon。daemon 由当前交互用户启动，两处入口：登录时的
`HKLM\...\Run` 值（REG_EXPAND_SZ，按用户展开 `%LOCALAPPDATA%`）与 TSF 首次
激活/首键的懒启动；都传 `-pipe -seeddict`。

- 管道名 `\\.\pipe\chaoset-<当前用户 SID>`；安全描述符 = DACL（当前用户 +
  SYSTEM + ALL APPLICATION PACKAGES）+ 低完整性标签 `S:(ML;;NW;;;LW)`。
  只有命名管道，没有 TCP 传输/端口。
- **AppContainer 宿主（Windows 11 开始菜单、搜索框、UWP）**：壳在
  AppContainer 里**不发起进程**（`CreateProcessW` 会让子进程继承 AppContainer
  身份，写错用户目录、抢同一管道名），只负责连已在运行的 daemon。所以：
  - 「开始菜单/搜索框里打不出中文、普通程序里正常」= AppContainer 连不上或写不进。
    查点按顺序：① daemon 在不在（`%LOCALAPPDATA%\Chaoset\chaosetd.log`）；
    ② 管道安全描述符是否含低完整性标签（缺了就是「能连不能写」，见
    `ipc/pipe_sddl.go`）；③ AppContainer 宿主日志（可能在
    `%LOCALAPPDATA%\Packages\<包族>\LocalState\Chaoset\`）。
  - 恢复手段：在任意**普通程序**（记事本、浏览器）里打一次字触发懒启动，
    之后 AppContainer 宿主即可用。
- 候选只有英文：先看 daemon 日志是否启动、是否有 `ipc 连接 network=pipe`；
  再看 `tsf.log` 的 `daemon lazy start`、`pipe connect failed`、
  `daemon request failed`。
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
./build/tis dedupe             # TIS 重复实例尽力合并（无效则注销重登）
```

## 五、CI 相关

### 本地只跑轻量项，重活交给 CI

提交前本地只需 `gofmt -l $(git ls-files '*.go')`（应无输出）、`go build ./...` 与 `make vet`（静态契约检查，均秒级）；
本地跑测试会占用 CPU 影响正常使用电脑。其余由 7 个并行 job 覆盖（单次提交约 3-5 分钟出全部结论；
本地/CI 分工见项目 AGENTS.md「CI 门禁与本地验证分工」）：

| CI job | 覆盖内容 | 为什么不在本地跑 |
|---|---|---|
| lint | gofmt / go vet / workflow 结构 / 版本检查 / Windows GDI 契约 / 三端 UI 一致性（`check-uiparity`）/ 构建 | 快，但统一在 CI 跑保证一致 |
| test | go test ./...（数据契约/引擎/壳行为测试） | 约 30s |
| race | `go test -race` | 最重的一项，本机 45s+ |
| linux-e2e | 真实 ibus-daemon 下的组词/上屏/多实例隔离/中英切换/翻页；Linux 安装包自检（tar.gz/deb 必含设置窗模块） | 需要 dbus + ibus，占资源 |
| macos-build | macOS 壳构建 + universal 自检 + ZIP 安装包 + 等价性 + 双端视觉一致性 | swiftc 构建 2-3 分钟 |
| windows-build | Windows 壳 mingw 交叉编译 + `core_test`（真实 daemon）+ `makensis` 安装包编译 + ZIP 自检 | 需要 mingw-w64/NSIS，本机不装；每次提交都验证（不只在打 tag 时） |
| settings-windows | `windows-latest` 上真实运行 `internal/settings` 注册表存储单测（DWORD/REG_SZ 往返、类型损坏回退，测试后恢复键值） | 需要真实 Windows runner，交叉编译验证不了注册表行为 |

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

## 六、输入源置灰 / 无法选择

**现象**：菜单栏输入法列表里 Chaoset 变灰，点不过去；系统设置仍能看到条目，
tis list 也显示 enabled。先不要重装输入法，也不要反复重启 daemon——这大概率是
macOS 的**安全键盘输入（Secure Event Input）**，第三方输入法会被系统暂时禁用。

    PID=$(ioreg -l -w 0 | grep -o 'kCGSSessionSecureInputPID=[0-9]*' | head -1 | cut -d= -f2)
    echo "secure-input PID=$PID"
    if [ -n "$PID" ]; then
      ps -p "$PID" -o pid,comm,args
      lsof -p "$PID" 2>/dev/null | grep -m1 '\.app/'   # 定位是哪个 app
    fi

- 有 PID：该进程正持有安全输入（常见于 Chrome 的密码框/密码管理器、终端
  Secure Keyboard Entry、远程桌面、解锁弹窗）。在该进程释放安全输入前，
  Chaoset 等第三方 IME 在菜单里会一直灰；这不是 daemon、TIS 或词库故障。
- 释放：切回普通输入框、关闭密码页/弹窗；仍不释放就正常退出该进程（先 ⌘Q，
  不要直接 kill -9），必要时注销重登。释放后上面的命令应无输出。
- 无 PID 但菜单仍灰：killall TextInputMenuAgent 让菜单重建；再核对
  cd shims/macos && ./build/tis list | grep -c chaoset 为 2，
  defaults read com.apple.HIToolbox AppleEnabledInputSources | grep -c ChaosetIME ≥ 1。
  两处都正常仍灰，优先怀疑安全输入释放不彻底或另一个进程重新占用了它。
- tis list 的 [*] 只表示条目 enabled；安全输入开启时菜单仍会禁用第三方输入法，
  所以不能用它否定安全输入这个根因。
- 安全输入已释放但菜单仍灰时，强制选中一次再刷新菜单代理：

      cd shims/macos
      ./build/tis select ime.chaoset.inputmethod.ChaosetIME.Pinyin
      killall TextInputMenuAgent

  `TISSelectInputSource` 成功且 TIS 属性 `selected=true`，说明系统层已允许选中，
  菜单灰色通常只是 `TextInputMenuAgent` 的陈旧缓存；必要时用 AX 检查状态菜单项
  `enabled` 是否为 true。

### 第三方输入源启用记录被清空

如果 TIS 表里仍有父容器 + Pinyin 两条、但输入菜单整条消失或 Chaoset 菜单项
enabled=false，检查第三方输入源启用记录：

    defaults read com.apple.inputsources AppleEnabledThirdPartyInputSources

- 返回空数组（或没有 Chaoset）时，菜单不会真正选中本输入法。父容器被
  disable、安装/刷新竞态或系统设置重写都可能清空该列表。
- 仅靠 TIS 命令不够：install.sh / reregister.sh 会幂等刷新输入源表与首选项并
  把菜单项画出来，但不负责把条目重新加入这个系统列表。完成修复必须在
  **系统设置 → 键盘 → 输入法 → 编辑 → +** 里重新添加 Chaoset，或注销重登让
  登录扫描重建。
- 此时 tis select 可能返回 0 或 -50，但 selected 仍为 false；不能用它证明
  已恢复。以系统设置添加后，菜单里能实际切换为准。
