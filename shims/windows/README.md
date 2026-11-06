# Windows 平台壳（TSF）

形态：**TSF COM DLL 薄壳（C++） + 命名管道连接 `chaosetd`**。引擎、词库、候选数据与
用户学习全部在 daemon；DLL 只负责按键路由、预编辑合成、上屏与原生候选窗。

## 目录

```
../common/ipc/core.hpp|cpp  跨平台 IPC 核心：4 字节大端长度 + JSON、会话控制；
                             Windows/Linux 原生壳共用。
src/win32.cpp               TSF COM、键盘/文本编辑会话、候选窗、传输、设置窗实现，
                             以及单例 UI 宿主（托盘状态 + 设置窗 + 命令管道）的实现。
src/ui_main.cpp              chaoset-ui.exe 入口：几十行 LoadLibrary 壳，调用
                              ChaosetIME.dll 导出 ChaosetRunUi（无第二套 UI 代码）。
tests/core_test.cpp         核心层行为测试（Linux/CI 原生编译，真实 daemon）。
build.sh                    核心行为测试 + mingw 交叉编译（64/32 位 DLL + UI 宿主）。
installer/chaoset.nsi       NSIS 安装器。
install.bat                 便携 ZIP 安装/卸载脚本。
```

## 构建

```sh
# macOS 上交叉编译（需要 brew install mingw-w64）。
# 产物：build/ChaosetIME.dll、build/chaoset-ui.exe、build/core_test（make package-windows
# 另出 32 位 ChaosetIME32.dll）。
./shims/windows/build.sh

# 完整 Windows 分发包与 NSIS 安装包（需要 mingw-w64 + makensis）。
make package-windows
```

`make package-windows` 生成 `dist/chaoset-windows-x64.zip` 与 `dist/ChaosetSetup.exe`。
ZIP 内含 64/32 位 DLL、`chaoset-ui.exe`（单例 UI 宿主）、`chaosetd.exe`、`chaoset-cli.exe`、
`chaoset-import.exe`、`chaoset-userdict.exe`、`dict.tsv`、`install.bat` 和本 README。

## 安装

### 一键安装程序（推荐）

从 GitHub Release 下载 `ChaosetSetup.exe`，双击并允许 UAC。安装器会：

- 把程序安装到 `C:\Program Files\Chaoset`；
- 注册 64 位与 32 位 TSF COM 组件；
- 写两条 HKLM `Run` 项：`ChaosetDaemon`（daemon，`REG_EXPAND_SZ`，`%LOCALAPPDATA%`
  由每个登录用户展开）与 `ChaosetUi`（`chaoset-ui.exe`，单例 UI 宿主：托盘中英状态
  + 设置窗）；
- 用 Go 导入器把随包底库合并到当前用户 `%LOCALAPPDATA%\Chaoset\dict.tsv`
  （保留已导入词，旧文件备份）；
- 写入标准卸载信息。

安装器**不关闭任何已运行程序**：被占用的 DLL 由改名/重启替换处理。已打开程序继续
使用旧版 DLL，重新打开后加载新版；系统输入宿主在注销/重启后刷新。

### 版本信息与 app 信息

`ChaosetSetup.exe` 与安装目录里的 PE 二进制（`ChaosetIME.dll`、
`ChaosetIME32.dll`、`chaoset-ui.exe`）都带 `VS_VERSION_INFO`：
右键「属性 → 详细信息」可看到「Chaoset 拼音输入法」、公司
`ShanWuYinShe`、版权、文件说明、原始文件名、`@VERSION@`（如 0.3.17）
与 4 段数字版本（如 0.3.17.0）。安装器向导欢迎页标题也带版本。

版本号**只有一个真源**：`scripts/version.sh`（Makefile 的 `$(VERSION)`）。
**只有 push tag 触发的 GitHub CI 构建**才用 tag 号（`0.3.17`）；本地构建、
日常 CI、`workflow_dispatch`、PR 一律用当前 `git commit hash`（12 位，工作区
有未提交改动时追加 `-dirty`）——本地测试不必累加版本号，又能从 hash 定位到底
装的是哪个提交。`$(VERSION_QUAD)` 是 4 段数字版本（tag 构建 `0.3.17.0`，
hash 构建 `0.0.<hash 派生>.<hash 派生>`），因为 NSIS `VIProductVersion` 与
PE `VS_FIXEDFILEINFO` 只接受数字；人类可读的 hash 放在属性页字符串字段。
deb/Arch 的包版本用 `$(PKG_VERSION)`（`0.0.0+<hash>`，保证以数字开头）。
NSIS 脚本用 `${VERSION}` / `${VERSION_QUAD}`，资源模板
`shims/windows/resources/chaoset.rc` 用 `@VERSION@` 等占位符，二者都由
`make package-windows` / `build.sh` 注入，源码不写死版本号。中文说明必须
用 `windres -c 65001` 编译，否则属性页会显示 mojibake。

「设置 → 应用 → 已安装的应用」里的 Chaoset 条目还写入
`DisplayVersion`、`InstallLocation`、`InstallDate`（YYYYMMDD）、
`EstimatedSize`（安装后实测）、`HelpLink` / `URLInfoAbout` /
`URLUpdateInfo`、`NoModify` / `NoRepair` 等元数据。
`make check-version`、`make check-uiparity` 与 `release.yml` 冒烟共同
锁死这些契约。

### 便携 ZIP

解压后以管理员身份运行 `install.bat`；卸载运行 `install.bat uninstall`。
便携脚本不写 Run 项，daemon 在首次输入时懒启动；卸载会清理 HKLM/HKCU 的 Run 项。

便携包不注册 `chaoset-ui.exe` 的登录 Run 项：托盘指示器与设置窗由 TIP 首次在
**普通程序**里激活时懒启动。若只在开始菜单/搜索里用（AppContainer 宿主不能
CreateProcess），先在记事本等普通程序里打一次字，或手动运行一次安装目录里的
`chaoset-ui.exe`。

### 升级后的残留提示

升级/替换 DLL 后，**已打开的程序仍加载旧版 DLL**：旧版 TIP 进程里的多余托盘图标
会一直留到那些进程重启。升级后请注销/重启一次（或至少重启资源管理器与相关
应用），任务栏中英图标才会回到「每会话唯一」。旧 UI 进程由安装器在升级时结束，
新 UI 宿主随登录 Run 项或下一次普通程序输入启动。

### 从源码手动注册

```bat
regsvr32 ChaosetIME.dll
chaosetd.exe -pipe -dict dict.tsv -seeddict dict.tsv -userdict userdict.json -log chaosetd.log
```

然后在「设置 → 时间和语言 → 语言和区域」添加输入法 **Chaoset**，用 `Win+Space`
切换。卸载：`regsvr32 /u ChaosetIME.dll`。

## daemon 生命周期

安装器不启动 daemon。daemon 有两个启动入口，均运行在**当前交互用户**上下文：

1. 登录时 `Run` 项启动；
2. TSF 首次激活/首键时懒启动（`-pipe`，当前用户权限）。

daemon 参数：

```bat
"%ProgramFiles%\Chaoset\chaosetd.exe" -pipe ^
  -dict "%LOCALAPPDATA%\Chaoset\dict.tsv" ^
  -seeddict "%ProgramFiles%\Chaoset\dict.tsv" ^
  -userdict "%LOCALAPPDATA%\Chaoset\userdict.json" ^
  -log "%LOCALAPPDATA%\Chaoset\chaosetd.log"
```

`-seeddict` 只在目标词库不存在或为 0 字节时复制安装目录底库，不覆盖用户导入词。
TSF 懒启动只在尚未连接时尝试，失败后按退避重试，不会每个按键长时间阻塞宿主。

## 单例 UI 宿主（托盘中英状态 + 设置窗）

Windows 上「中/英」托盘图标与设置窗**不能放在 TIP DLL 所在的宿主进程里**：

- TSF 会把 TIP 加载进每个宿主进程（实测 explorer.exe 单进程就有 4 个 TIP 实例）。
  TIP 自己加托盘图标 → 任务栏同时出现多个「中/英」，且各实例 enMode_ 互相独立
  （中/英混显）——2026-11-05 用户反馈的截图正是这个原因。
- 设置窗由宿主进程创建时，开始菜单/搜索（SearchApp.exe，Low IL AppContainer）
  里窗口会被沉浸式 z 序 band 压住；且 AC 中文件对话框、CreateProcess、HKCU
  注册表都不可用。

因此 `chaoset-ui.exe` 是**每用户会话唯一**的后台 UI 进程：

- 登录 `Run` 项常驻；TIP 需要时按需拉起（AppContainer 宿主不能 CreateProcess，
  只连接已运行实例）。
- 持有唯一托盘图标（中/英），并创建设置窗（候选栏齿轮或托盘图标左键单击都打开它）。
- TIP 通过命名管道 `\\.\pipe\chaoset-ui-<SID>` 上报模式（`mode 0/1`）或请求
  设置窗（`settings`）。管道安全描述符与 daemon 完全同规则（当前用户 + SYSTEM +
  ALL APPLICATION PACKAGES + 低完整性标签 `S:(ML;;NW;;;LW)`），否则开始菜单/
  搜索框（低完整性）能连不能写。
- 设置窗/托盘实现全部在 `ChaosetIME.dll` 内（导出 `ChaosetRunUi`），
  `chaoset-ui.exe` 只是 LoadLibrary 壳——不存在第二套 UI 代码。
- 单实例用 `Local\ChaosetUi-<SID>` 互斥体；第二个实例带 `--settings` 时把请求
  转发给首实例后退出。升级/卸载由安装器结束旧 UI 进程（它是本输入法自己的
  后台程序，不是用户程序）。

排障：任务栏没有中/英状态先看 `chaoset-ui.exe` 是否在跑；设置窗打不开时查
`tsf.log` 的 `open settings failed: ui host unavailable` 以及管道
`chaoset-ui-<SID>` 是否存在（AC 宿主的 tsf.log 在包私有目录，见「日志与排障」）。

## 传输与安全

- 唯一传输：命名管道 `\\.\pipe\chaoset-<当前用户 SID>`；DLL 与 daemon 用同一 SID 规则。
- ACL：当前用户 + SYSTEM + ALL APPLICATION PACKAGES 读写；其他本地用户不可连。
- 强制完整性标签：低完整性可写（`S:(ML;;NW;;;LW)`）。AppContainer 宿主（开始菜单、
  搜索框、UWP）是低完整性进程，缺这个标签时 DACL 放行了也只能连不能写，表现就是
  「按了键、daemon 也在、没有候选」。见 `ipc/pipe_sddl.go`。
- 没有网络端口、没有 TCP 回退：管道连不上就是连不上，壳按约定降级放行按键。

## 日志与排障

先确认二进制构建。**用 `chaoset-cli.exe -version`**：`chaosetd.exe` 以
`-H=windowsgui` 构建（不挂控制台），在 cmd 里跑 `-version` 没有任何输出，
不能据此判断程序坏了。

```bat
chaoset-cli.exe -version
```

- `%LOCALAPPDATA%\Chaoset\chaosetd.log`：daemon 启动构建、用户词典路径、词库加载、
  每条 IPC 连接（`network=pipe`）与请求错误。
- `%LOCALAPPDATA%\Chaoset\tsf.log`：TIP 生命周期、宿主/AppContainer 标志、
  TSF 调用 HRESULT、候选窗失败、全屏抑制、Shift 跟踪、`daemon request failed`，
  以及 UI 宿主的托盘失败/管道失败。每个进程最多 200 条。
  **AppContainer 宿主（开始菜单/搜索）的日志不在这个路径**：实测 SearchApp 写到
  `%LOCALAPPDATA%\Packages\Microsoft.Windows.Search_cw5n1h2txyewy\AC\Chaoset\tsf.log`
  （`appcontainer=1`）；排查开始菜单问题必须去包私有 `AC\Chaoset` 目录找。
- `%LOCALAPPDATA%\Chaoset\crash.log`：宿主未处理异常（异常码/地址/模块/调用栈/
  最后一次 TSF 操作），记录后继续交给 WER。

### 装完只能打英文、完全没有候选

这是「壳连不上 daemon」的既定降级：连不上时字母原样透传给宿主（绝不吞键），
候选窗根本不会创建。判断 daemon 有没有起来，按诊断力从强到弱：

1. **daemon 日志断点**（最强）：`%LOCALAPPDATA%\Chaoset\chaosetd.log`

   | 日志内容 | 结论 |
   | :--- | :--- |
   | 文件不存在 | daemon 从未启动（CreateProcess 失败/exe 缺失/起在别的账户） |
   | 有「chaosetd 启动」但**没有**「chaosetd 就绪」 | 起来了但在监听前就退出，末行即退出原因（词库/单实例/端点解析错误；v0.3.0–v0.3.12 是「Windows 使用命名管道…没有 Unix socket 路径」） |
   | 有「chaosetd 就绪，pipe: \\.\pipe\chaoset-S-1-5-…」 | daemon 正常，问题在壳侧 |
   | 还有「ipc 连接: network=pipe」 | IPC 已通，转查候选渲染 |

2. **进程与命名管道**：

   ```bat
   tasklist /FI "IMAGENAME eq chaosetd.exe"
   powershell -NoProfile -Command "[System.IO.Directory]::GetFiles('\\.\pipe\') | Where-Object { $_ -like '*chaoset*' }"
   ```

   进程活着不代表在监听；管道列表里出现 `chaoset-S-1-5-…` 才说明到了 listen 阶段。

3. **直接验 IPC**（最硬）：

   ```bat
   chaoset-cli.exe -pipe
   ```

   能进交互测试台并输入拼音出候选 = daemon + IPC + 引擎全通，问题只在 TSF 壳；
   立刻报「连接输入法 daemon 失败」= 连不上。

4. **`tsf.log` 关键字**：`daemon lazy start ok=0 err=N`（CreateProcess 失败）、
   `pipe connect failed err=N`（N=2 管道不存在、N=5 ACL 拒绝）、
   `daemon request failed op=input connected=0`（连不上）与 `connected=1`
   （连上了但请求失败/超时，性质不同）。注意 exe 不存在时不会写 lazy start 行，
   且每进程只有 200 条预算，日志里没有不代表 TIP 没激活。

排查后仍只有英文，用 `chaoset-cli.exe -pipe` 的结果区分「daemon 侧」与「壳侧」，
再按上表定位。

## 命令行工具

```bat
REM 终端测试台（注意：CLI 本身不会拉起 daemon。daemon 未运行时先在任意
REM 程序里打一次拼音，或在安装目录手动运行 chaosetd.exe -pipe）
chaoset-cli.exe -pipe

REM 用户词典统计/清空/导出
chaoset-userdict.exe -pipe stats
chaoset-userdict.exe -pipe export -o userdict-backup.tsv
```

## 功能与验收

| 功能 | 当前状态 |
| :--- | :--- |
| 64/32 位 TSF COM，注册/卸载/类别配置 | 已实现；CI 校验导出 |
| 命名管道、当前用户按需启动、AppContainer ACL | 已实现；Windows 真机需验收 |
| 原生 GDI 候选窗、学习标记、鼠标点选、翻页 | 已实现；视觉静态契约由 `checkuiparity` 校验 |
| 中英状态指示器（任务栏「中/英」） | 单例 UI 宿主 `chaoset-ui.exe` 的唯一托盘图标；TIP 经命名管道上报；无浮动提示兜底 |
| 设置窗：联想、学习数据、词库导入、深浅色、per-monitor DPI | 由单例 UI 宿主创建（开始菜单/搜索等 AC 宿主里也能打开）；交互需真机验收 |
| Shift 空按中英切换、全/半角、双拼 `;`、渐进上屏 | 编译级 + 核心行为测试；Shift 记账有静态契约断言（`checkuiparity`）；投递层仍需真机验收 |
| 全屏免打扰、UIA 窗口级 provider | 编译级实现 |
| 游戏/全屏、UWP/AppContainer 细节、多会话（RDP/快切）、混合 DPI 多屏、Narrator | 待 Windows 真机验收 |

候选窗默认全屏抑制由 `HKCU\Software\Chaoset\FullscreenSuppress` 控制（DWORD，缺省 1）。
设置窗入口：候选栏齿轮，或左键单击托盘「中/英」图标（由 `chaoset-ui.exe` 打开）；
设置项存 `HKCU\Software\Chaoset`。

## 已知限制

- 中文输入法激活时字母键会进入组词，这是 IME 固有行为；游戏前应切英文模式。
- 全屏免打扰只抑制候选窗，不改变按键/组词；无边框全屏不命中 D3D 全屏判定。
- 候选窗/设置窗的深色、DPI、无障碍子项仍需 Windows 真机验收。
- **浮窗 owner 只给沉浸式 shell 宿主**（explorer/SearchApp/开始菜单/搜索/文本服务）：
  这些宿主自带独立 z 序 band，无主 topmost 会被压住。普通桌面宿主（浏览器/编辑器/
  终端）一律不设 owner——带 owner 的 TOPMOST 窗口显示时会调整 owner 所在窗口组
  的 z 序，同进程多个窗口时会让用户看到「另一个窗口弹到前面」（2026-11-05 实机）。
- **浮窗 owner 必须取前台窗口（且限本进程）**，不要用 TSF 上下文推出的窗口：
  带 owner 的 TOPMOST 窗口显示时会把 owner 所在窗口组抬到前面，用别的窗口
  当 owner 会让用户看到「弹到另一个窗口但焦点不动」（2026-11-05 用户反馈）。
  owner 不能去掉——开始菜单/搜索是沉浸式 z 序 band，无主 topmost 会被压住。
- **非激活时必须整键放行**（2026-11-05）：TSF 在某些宿主里会把按键继续投给未激活
  的 TIP 按键下沉，导致「选定的输入法不是 chaoset 时按 Shift 仍出中英提示」。
  四个按键入口都要先查 `ITfInputProcessorProfileMgr::GetActiveProfile
  (GUID_TFCAT_TIP_KEYBOARD)` 的 clsid 是否为 `CLSID_ChaosetTIP`，不是则 `pfEaten=FALSE`。
- **中英状态指示器由单例 UI 宿主持有**（`UiTray`）：TIP 绝不能自绘托盘——TSF
  会为一个进程的多个线程各创建一个 TIP 实例（实测 explorer 单进程 4 个），
  每实例一个图标就是任务栏多个「中/英」。`Shell_NotifyIconW` 必须在 UI 宿主
  自己的消息循环线程调用（在 TSF 按键/激活回调里同步调用曾实测阻塞宿主
  ~7 秒）；`ICONINFO.hbmMask` 必须非空，且掩码缓冲按 DPI 动态分配（250%/300%
  缩放下 `SM_CXSMICON` 会到 40/48 像素）。`diagBudget()` 每进程仅 200 次，
  高频日志会把预算烧光并吞掉失败原因——关键失败路径的日志必须绕开预算。
- **设置窗由 UI 宿主创建**（`openSettingsWindowStandalone` 在 `ChaosetRunUi` 的
  消息循环线程调用）：TIP 内只许发管道请求，不要退回「宿主进程内建设置窗」——
  AC 宿主里既会被 z 序 band 压住，也无法启动导入器/文件对话框/重启 daemon。
- **系统输入指示器只显示 profile 图标**（蓝色 C），不会显示中/英：第三方 TIP 要让
  系统输入指示器显示中/英，必须实现 `ITfLangBarItemButton` +
  `GUID_LBI_INPUTMODE`（2026-11-05 调研结论）。当前由 UI 宿主的托盘图标承担，
  不依赖该通道；将来若替换，需同步改 `checkuiparity` 与本文档。
- **crash.log 只记错误级异常**：VEH 里按 NTSTATUS 严重性位过滤，只有 `0xC0000000`
  档（访问违例/栈溢出/fail-fast/页内错误）才写。宿主的 COM/RPC 首次异常
（`0x80040155`、`0x800706BA`、`EPT_S_NOT_REGISTERED` 等）是正常控制流，不再记录——
  否则真正的崩溃会被淹没（2026-11-05 实机：AC 目录 197 条全为误报）。
- **中英模式仍写线程级隔间** `GUID_COMPARTMENT_KEYBOARD_OPENCLOSE`（非零 = 中文），
  供其他 TSF 客户端读取；但显示只由单例 UI 宿主的托盘图标负责。
- **陈旧组词清理必须先 `endComposition()`**：只清本地状态不结束合成，宿主文档里
  的预编辑文本会留着，重开窗口后又出现（开始菜单实机问题）。
- **开始菜单/搜索不投递 `OnSetFocus(FALSE)`**（实机 2026-11-05）：关闭开始菜单后
  重开，上一轮拼音与候选窗仍在。因此焦点清理不能只依赖 TSF 焦点事件，必须有
  `EVENT_SYSTEM_FOREGROUND` 钩子兜底；回调里**不得触碰 TSF**（旧上下文可能已销毁），
  只收浮窗 + 打标记，状态清理由下一次按键在 TIP 线程完成。
- **UILess 模式现状**：已实现 `ITfTextInputProcessorEx::ActivateEx`（微软文档要求，
  否则 UILess 线程里 TIP 不被激活），但**尚未**实现 `ITfUIElementMgr` 与
  `ITfCandidateListUIElement`，因此：
  - 已注册类别里**没有** `TFCAT_TIPCAP_UIELEMENTENABLED`（声明了就必须让所有
    UI 走 UI 元素管理器，否则候选窗会在部分宿主整体消失，见 2026-10-30 事故）；
  - 候选窗仍是进程内自绘 GDI 弹窗；
  - `tsf.log` 里 `activateEx ... uielementonly=1` 是「真的遇到了 UILess 宿主」
    的唯一证据；`ActivateEx` 遇到 `TF_TMAE_UIELEMENTENABLEDONLY` 线程会**拒绝
    激活**（`activateEx refused`），宁可让宿主走自己的输入路径，也不在 UILess
    线程里创建自绘候选窗/吃键——这是 2026-10-30 事故的同款风险边界。
- **Shift 跟踪必须同时挂在 Test 与 Key 两个按键阶段**（2026-11-05 实机日志结论）：
  宿主分两类——Firefox 把修饰键送进 OnTestKeyDown/OnTestKeyUp；Chrome、conhost、
  Windows Terminal **只**送 OnKeyDown/OnKeyUp（同一进程里前者无日志、后者有）。
  只挂一边会让另一类宿主彻底切不了中英。两阶段各投递一次不会双计（状态机幂等）。
- Shift 跟踪同时依赖 TSF Test 阶段。**2026-11-05 已补两层兜底**：
  1. 记账改用幂等位掩码（`shiftHeld_`）而不是 ++/-- 计数器，并在 `Deactivate`
     复位、每次按键前用 `GetAsyncKeyState` 自愈丢失的 KeyUp；失焦时只**挂起**
     （`shiftHoldSuspended_`，且只在获得焦点分支清除），不丢弃按住状态。
     此前若宿主漏投递一次 Shift KeyUp（典型：按住 Shift 切窗口，KeyUp 交给新前台
     窗口），计数器永远回不到 0，该进程内 Shift 空按会**永久失效**直到重启应用。
  2. **事件驱动的自愈（无常驻定时器）**：每次按键到达时，若发现物理 Shift 已经
     松开、但按住状态还挂着（宿主丢了 KeyUp），就地收口并留一条一次性证据
     `tsf.log: shift: 宿主丢弃了 Shift 释放事件`。不引入定时器——输入法由按键
     触发，常驻轮询在每个宿主进程里都是白耗 CPU，且 40ms 采样判断不了
     「快速轻按」与「Shift+点击」，有误切换风险（v0.3.16 曾加过轮询，确认
     真因后已撤除，见 CHANGELOG）。
- **焦点抖动不得吃掉 Shift 按住状态**：浏览器会在窗口内部把焦点在地址栏 /
  页面元素之间搬动，同样触发 `OnSetFocus(FALSE)`。失焦时若 Shift 仍物理按下，
  只能**挂起**（松开前焦点回来照常切换），不能直接复位。否则表现为
  「能打字但 Shift 切不了中英」（2026-11-05 用户反馈，根因即此）。
- **PWA / 「安装应用」窗口（Chrome app 窗口、Firefox 装到任务栏的独立窗口）**：
  这类窗口是宿主自己的独立顶层窗口，TSF 上下文与焦点归属都可能和主窗口不同。
  排障看 `tsf.log` 的 `fgpid`/`selfpid`：`activate` 与 `shift toggle` 两行现在都带
  这两个字段——`fgpid != selfpid` 说明本 TIP 不在拥有焦点的那个进程里，那就不是
  按键记账的问题，而是宿主没在这个进程加载 TIP（需要宿主侧或 IMM32 方案）。
  另外候选窗在拿不到 `GetTextExt` 光标矩形时会退回鼠标位置，此时锚点已下移一行，
  避免候选窗盖住正在输入的文字；`caretFromMouse_` 会标记这是鼠标兜底，短暂提示
  （半/全角等）不会因此跑到鼠标位置，只有鼠标触发的「删除该词」结果提示允许鼠标锚点。
- **console（cmd/旧 PowerShell）走的是 IMM32，不是 TSF**：本壳是纯 TSF TIP，
  没有 IMM32 入口（`ImeInquire`/`ImeProcessKey`），若 `tsf.log` 里完全没有
  `activate ... host=conhost.exe`，说明 TIP 根本没被加载——那不是本 Bug，
  需要单独实现 IMM32 壳才可能支持。
- 中文标点按 US 布局字符级映射；其他键盘布局未接入 `ToAscii`。
- `GUID_TFCAT_CATEGORY_OF_TIP` 在 mingw uuid 库缺失，当前只登记键盘类。
- 多会话（RDP/快速用户切换）与同一用户多会话的管道命名仍待验证。

## 测试

```sh
./shims/windows/build.sh      # core_test + 交叉编译（DLL/UI 宿主）+ 导出检查
make package-windows          # ZIP + NSIS 安装器（含 chaoset-ui.exe 自检）
```

单例 UI 宿主可脱离 TSF 单独冒烟：把 `ChaosetIME.dll` + `chaoset-ui.exe` 放同一目录，
运行 `chaoset-ui.exe`；应出现唯一托盘「中/英」图标，向管道
`\\.\pipe\chaoset-ui-<SID>` 写 `mode 1\n` 图标应变为「英」，写 `settings\n`
应弹出设置窗，再运行一次 `chaoset-ui.exe --settings` 不应产生第二个进程/窗口。

CI 的 `windows-build` 每次 push/PR 运行上述构建与包内容自检；Release 工作流在打 tag 时
重新构建并上传 `ChaosetSetup.exe` / `chaoset-windows-x64.zip`。
