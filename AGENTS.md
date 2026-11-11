# chaoset_ime

跨平台中文输入法：Go 核心引擎（chaosetd） + IPC 通信（Unix socket / Windows 命名管道） + 平台原生薄壳（macOS IMK / Windows TSF / Linux C++/libibus）。

通用约束以全局 `~/AGENTS.md` 为准，本文件只做项目特有补充。

## 架构要点

- **引擎与平台解耦**：`engine/` 为纯 Go 逻辑，禁止引入任何平台特定 API 或 CGO 依赖；所有引擎状态机与算法必须 100% 能够通过纯单测覆盖。该约束由 `engine/purity_test.go` 机器守卫：只允许标准库 import、禁止 `//go:build` 构建标签 / CGO / 平台文件（`*_windows.go` 等），违反即 `go test ./...` 红。
- **macOS 安装布局**：运行组件随 `ChaosetIME.app` 安装，daemon/导入工具/种子词库位于 `Contents/Resources/`；LaunchAgent 指向 app 内 daemon；`~/Library/Application Support/chaoset` 只放用户数据/词库/socket/日志。禁止把 daemon 单独拷到 Application Support。
- **职责边界**：`engine/` 只做拼音/联想算法；`shims/` 只做平台集成 + UI（设置窗也是 shim：macOS Swift / Windows C++ / Linux 原生 GTK3 C，**不要把设置窗搬进 Go**）；Windows 的托盘中英状态与设置窗统一由单例 UI 宿主 `chaoset-ui.exe`（shim 内的 C++ 实现，导出 `ChaosetRunUi`）托管，TIP 只经命名管道上报/请求，**禁止在 TIP 进程内自绘托盘或创建设置窗**；UI 宿主不随登录启动（TIP 首次激活懒启动，托盘图标此时才出现），升级不得依赖登录 Run 项；工具逻辑（词库合并、备份、原子替换、用户词典导入导出）必须在 Go `cmd/chaoset-*`，禁止在 shims 里重复实现。IPC 传输层归 `ipc/`；三端壳共用 `shims/common/ipc/core.{hpp,cpp}` 的 JSON/帧协议与 `Session` 封装。三端词库导入均委托 `chaoset-import -o ... -seed ... -backup`。Linux 壳必须是原生 C++/libibus（`shims/linux/engine/`），不得回退为 Go 进程内引擎。
- **架构地图与文件预算（2026-11-06 用户要求：shims 大文件按职责拆分，不要埋屎山）**：shims 大文件必须按 `docs/architecture.md` 的模块地图拆分——Windows `win32.cpp` 只保留 DLL 生命周期编排（类厂/导出/DllMain/DllCanUnloadNow/g_liveObjects），TIP 主体拆成 `tip.hpp`（类声明与常量）+ `tip_core/tip_keys/tip_composition/tip_candidates.cpp`，候选窗（`candidate_window`）、后台 worker（`shell_worker`）、TIP 线程消息窗口（`tip_thread`，经 `TipThreadSink` 抽象接口分发）、TIP 注册（`tip_registration`）、模式提示（`mode_toast`）与叶子模块 `diagnostics/winutil/theme/config/transport/daemon/ui_client/settings_window/ui_host` 独立编译，禁止 leaf 反向 include TIP；macOS/Linux 按职责拆文件（Swift 同模块、C 用头文件声明）。单文件行数预算与模块存在性由 `make check-uiparity` 的「模块布局与文件预算」组机器断言（依赖方向「叶子模块不得反向 include TIP」目前靠代码评审，未机器断言）（`winShell`/`macPanel`/`macSettings`/`linuxSetup` 是 checker 按目录多源聚合的逻辑文件，既有文案/几何/行为断言不变）。新增/移动平台代码必须先更新 `docs/architecture.md` 模块地图与 checker 清单，否则 `make vet` 红。
- **IPC 协议契约**：4 字节大端长度 + JSON 载荷（上限 1 MiB），传输通过 `ipc.Endpoint` 抽象（只有 `unix`/`pipe` 两种，**没有 TCP**：不监听任何端口，也没有回退传输）。Windows 另有一条 **UI 宿主命令管道** `\\.\pipe\chaoset-ui-<SID>`（TIP → 单例 `chaoset-ui.exe` 上报中英模式/请求设置窗）：命令是几十字节的短行文本，安全描述符与 daemon 管道完全同规则（AC 宿主必须能写）。macOS/Linux 用 Unix socket；Windows 命名管道：daemon `-pipe` + C++ 壳只走管道，安全描述符 = DACL（当前用户 + SYSTEM + ALL APPLICATION PACKAGES）+ 低完整性标签 `S:(ML;;NW;;;LW)`（AppContainer 宿主是低完整性，缺标签只能连不能写，见 `ipc/pipe_sddl.go` 与其单测）；安装器 Run 项与 TSF 懒启动均用 `-pipe`；安装/升级**不得强杀用户程序或系统输入宿主**（由 `checkuiparity` 机器校验）。**daemon 自启与恢复**：登录 Run 项 / LaunchAgent 常驻，三端 shell 都有「连不上就拉起 daemon」的懒启动（Windows `ensureDaemonStarted`、macOS `EngineClient.spawnDaemonIfNeeded`、Linux `Daemon::ensureReady`，由 `checkuiparity` 断言三端都存在）；**AppContainer 宿主内不发起进程**（`CreateProcessW` 会继承 AppContainer 身份），只连接已运行的 daemon。Linux 原生引擎首次按键自动拉起 `chaosetd`；设置窗清空/导入词库前必须先停 daemon，防止内存旧状态回写。
- **日志**：daemon 用 `-log` 写启动构建/词库加载/用户词典路径/IPC 连接与请求错误；Windows TSF 壳写 `tsf.log`（失败与生命周期，单进程预算）与 `crash.log`；**AppContainer 宿主（开始菜单/搜索）的 `tsf.log` 在包私有目录** `%LOCALAPPDATA%\Packages\<包族>\AC\Chaoset\tsf.log`（实测 SearchApp），排障必须去那里找；macOS 壳写统一日志 `subsystem=ime.chaoset.inputmethod`；Linux 壳写 stderr（journal）。排障先跑 `chaosetd -version`/`chaoset-cli -version` 确认构建，日志只覆盖启动/连接/失败路径，不按键写盘。
- **中英状态指示器（Windows）**：任务栏「中/英」只能由单例 `chaoset-ui.exe` 的 `UiTray` 绘制——TSF 把 TIP 加载进每个宿主进程（实测 explorer 单进程 4 个实例），TIP 内自绘 = 任务栏多个图标 + 各实例模式不一致；模式仍写线程隔间 `GUID_COMPARTMENT_KEYBOARD_OPENCLOSE`，禁止用浮层 toast 兜底（用户可见的「光标/鼠标旁也在提示」）。系统输入指示器只显示 profile 图标，要显示中/英需实现 `ITfLangBarItemButton` + `GUID_LBI_INPUTMODE`（调研结论，尚未采用）。**托盘图标生命周期（2026-11-06/07 用户要求）**：`chaoset-ui.exe` 不写登录 Run 项、开机不显示图标；TIP 首次激活时带 `--mode 0/1` 懒启动 UI 宿主，**但只有 TIP 上报 `active 1`（前台激活本输入法）才第一次 `Shell_NotifyIcon(NIM_ADD)`**；`active 0`（失焦/停用/切换输入法/前台离开）防抖 300ms 后 `NIM_DELETE` 隐藏；`mode` 只更新图标，启动 `--mode` 不显示。切换输入法而不触发 Deactivate 的宿主，由 TIP 的按键闸门 + 500ms 激活档案定时器兜底。安装器/便携包升级时必须清掉旧版 `ChaosetUi` Run 项。
- **零阻塞契约（Windows，2026-11-06 用户要求「任何情况下都不应阻塞任何进程或 app」）**：TSF 把 TIP 加载进每个宿主进程，**所有 TSF 回调都在宿主 UI 线程上同步执行**，因此回调体内禁止任何跨进程等待/建进程/同步写盘：不得 `CreateProcessW`、`WaitNamedPipeW`、同步管道 `ReadFile/WriteFile`、`Sleep`、`MessageBox`、`GetOpenFileName`、同步 `diagPrintf`。这些副作用统一交给每进程一个 `ShellWorker` 后台线程（任务队列 + message-only 窗口回投结果）；管道 I/O 一律 `FILE_FLAG_OVERLAPPED` + 明确截止时间（TSF 路径 30ms、worker/设置窗 200ms）+ 5s 熔断；daemon 未就绪时按键 **fail-open 原样放行**（不阻塞、不丢键，下一键恢复）；配置查询（ModeScope/Associate/深色/全屏）用 TTL 缓存；前台钩子每线程最多一个、回调只 PostMessage、前台离开自动撤销；worker 线程与 message-only 窗口计入 `DllCanUnloadNow`。由 `checkuiparity` 的「TSF 回调零跨进程等待」组机器断言，禁止回退。**非激活零副作用（2026-11-07 用户要求「没激活时不做任何操作、不拦截按键、不获取鼠标事件」）**：所有 TSF 按键/焦点回调以 `inputActive_` 为唯一闸门，未激活时立即放行、不清理/不查询/不写盘/不动钩子/不读鼠标；修饰键 KeyUp 恒 `FALSE`（绝不消费，避免 OS 修饰键卡住）；全壳禁止 `SetWindowsHookEx`/键鼠钩子/`SendInput`/读鼠标；daemon/UI 宿主副作用只在 worker 且非 AppContainer 时发生（每实例预热一次；`daemonPipeExists` 必须把 `ERROR_PIPE_BUSY`/`ERROR_ACCESS_DENIED` 视为存在，spawn 前再查 `Local\ChaosetDaemon` 互斥体，`chaosetd` 单实例锁前移到词库加载前——修 2026-11-07 重复 spawn）。由 `checkuiparity` 的「非激活零副作用」「daemon 懒启动去抖」组断言。 **动态卸载安全（2026-11-07 用户硬要求：输入法最多不能打字，绝不能 crash/阻塞系统或增加负担）**：**TSF 宿主进程不得注册 VEH 或任何全局异常/键鼠回调**（宿主崩溃交给系统 WER；VEH 只在 UI 宿主自己的进程注册写 crash.log）；`DllMain(DLL_PROCESS_DETACH)` 必须 `removeCrashHandler()` 并 `UnregisterClassW` 注销窗口类——否则 TSF `FreeLibrary` 后宿主任意异常会跳进已卸载内存（WER: `ChaosetIME.dll_unloaded`）。由「DLL 卸载安全」组断言。
- **候选窗为纯数据 IPC + mac/Windows 原生自绘、Linux ibus 面板**：daemon 只返回 Composition/Candidate 纯数据（`input`/`select` 等不变）。macOS 用 AppKit（`shims/macos/src/CandidatePanel.swift` controller + `CandidatePanelView.swift` 渲染：键帽序号、首选蓝胶囊、学习标记 `*`，浅/深两套静态 sRGB 按 appearance 选择，支持鼠标点选与 VoiceOver）；Windows 用 GDI（`shims/windows/src/candidate_window.cpp`，同样绘制学习标记 `*`）；Linux **不重绘候选窗**，用 ibus 原生 LookupTable/Preedit 呈现（学习标记不适用理由记在 `shims/linux/README.md`）。**daemon 不提供贴图渲染**（无 `renderer/` 包与 `candidateImage` IPC，勿在文档或壳里引用）。
- **视觉参数与一致性检查**：实际渲染实现是 macOS `CandidatePanelView.swift`（含 `CandidateTheme.swift` 几何/色值）与 Windows `shims/windows/src/candidate_window.cpp` 两份（macOS 为视觉参考）。**基准期望值内联在 `shims/macos/tests/visual-parity.sh`**（原 `skin/` 包已于 2026-11-06 删除——它在产品里零调用、只在 macOS 测试里当对照物，容易被误认为跨端视觉真源）。布局 5 项 + macOS 参考常量（含 divider）+ 浅/深各 13 色由该脚本逐项断言（故意改字号/色值必须 FAIL）；**Windows 没有运行时视觉断言**（`tools/checkuiparity` 只做静态几何/配色字段契约；另有 pt 口径、交叉编译与核心行为测试），不要把它写成「双端已验证」。改动排版参数/按键矩阵必须同步更新 `docs/ui-parity.md` 与机器断言。
- **候选窗任何 UI 改动（控制区/齿轮/翻页/配色/层级）必须三端同步检查**：macOS `CandidatePanelView.swift`（渲染，`CandidateTheme.swift` 几何/色值）、Windows `candidate_window.cpp`（`settingsRect_`/齿轮绘制）、Linux ibus（无自绘 UI 时在 `shims/linux/README.md` 记录不适用理由）——只改一个平台 = 未完成。绘制机制按平台落地但**视觉形态必须一致**（齿轮 = 圆环+8 齿矢量算法，**仅 macOS/Windows 两端**同几何参数同色值；Linux ibus 面板无齿轮；macOS 端 SF Symbol 模板图离屏着色失效因此手绘矢量，Windows GDI 同算法实现），机制说明记录在各壳源码注释。Windows 布局/字号/间距统一 **pt 口径**（`pt(v)=MulDiv(v,dpi,72)`），不要退回 px@96。
- **三端设置窗**：macOS 设置窗在 `shims/macos/src/SettingsWindow*.swift`（含「词库导入」入口——打开 Setup.app 并自动弹导入面板；`shims/macos/setup/main.swift` 是安装器 Setup.app 本体）、Windows 设置窗（由 `chaoset-ui.exe` 创建，**不在 TIP 宿主进程内**，开始菜单/搜索等 AC 宿主才能打开；入口 = 候选栏齿轮或托盘图标）、Linux 原生 GTK3 C 设置窗（`shims/linux/setup/chaoset-setup.c` 骨架 + `setup_page_*.c` 页面 + `setup_ctl.c`/`setup_learn.c`/`setup_import.c` 逻辑，`Gtk.Application` 单例 + hicolor 小尺寸图标，学习数据管理与词库导入在 GUI 内，只调用 Go 工具/ibus，engine 零改动；**禁止使用 Python/PyGObject**）。三端的词库导入安全流程与设置项契约由 `tools/checkuiparity` 静态断言，改动任一端设置窗必须三端同步并更新 `docs/ui-parity.md`。**方案 A（2026-11-06 用户要求）**：设置项的 schema/默认值/校验/持久化唯一真源在 Go `internal/settings`，三端设置窗通过 `cmd/chaoset-settingsctl`（`get` 输出 JSON、`set <key> <value>`）做冷路径读写；输入热路径仍读本机缓存/原生存储（Windows config TTL 缓存、macOS/Linux 进程内缓存），**不得在 TSF/IMK/ibus 回调里起进程**。迁移阶段与 CLI 契约见 `docs/architecture.md` §2。**A2 Windows 试点（2026-11-07）**：Windows 设置窗的联想开关/中英状态读写已改调 `chaoset-settingsctl get/set`（1s 硬超时、native HKCU 回退、CLI 随安装包分发）；macOS/Linux 设置窗接线下一批。
- **版本可见性（三端统一）**：三端设置窗底部必须显示 `Chaoset 版本 x.y.z`；macOS 发布包为 `chaoset-macos.zip`（不提供 DMG）；`make check-uiparity` 的共享文案断言含「Chaoset 版本」，改设置窗时同步改。**Windows 安装器同样必须带版本信息 / app 信息**（2026-11-06）：`ChaosetSetup.exe` 用 `VIProductVersion` / `VIFileVersion` + `VIAddVersionKey` 写产品名/公司/版权/说明/版本，卸载注册表项写 `DisplayVersion` / `InstallLocation` / `InstallDate` / `EstimatedSize` / `HelpLink` 等；`ChaosetIME{32}.dll` 与 `chaoset-ui.exe` 也带 `VS_VERSION_INFO`（资源模板 `shims/windows/resources/chaoset.rc`，占位符由 Makefile / `build.sh` 注入）。版本号单一真源是 `scripts/version.sh`：**只读环境变量 `CHAOS_VERSION_TAG`**——`release.yml` 顶层 env 在 push tag 触发时把它设成 tag 版本，其他任何环境（本地、日常 CI、`workflow_dispatch`、PR）都不设，脚本读不到就生成「构建当天年月日 + commit hash」（如 `20261106-8b2eb0ca0c38`，工作区有改动追加 `-dirty`）。本地测试不必累加版本号，又能看出构建日期与提交。`$(VERSION)` 是显示版本；`$(VERSION_QUAD)` 是 4 段数字版本（NSIS 与 PE 的 `VS_FIXEDFILEINFO` 只接受数字，日期放在属性页字符串）；`$(PKG_VERSION)` 是 deb/Arch 合法版本（`0.0.0+<日期>.<hash>`）。禁止再用 `git describe --tags` 当版本源；中文资源必须 `windres -c 65001`。契约由 `make check-version`、`make check-uiparity` 的「Windows 版本信息」/「版本号来源」组与 `release.yml` 冒烟锁死。
- **三端 UI 一致性静态检查（`make check-uiparity`）**：`tools/checkuiparity` 覆盖设置窗、学习数据、词库导入与安全、联想即时性、退格/修饰键语义、候选窗几何/配色、单例 UI 宿主与模式指示器（禁止回退 TIP 自绘托盘/toast，构建/安装器/发行包必须带 `chaoset-ui.exe` 与 `ChaosetRunUi` 导出）、CI 覆盖、shell 转义、文档对照等三端契约（组数以 `make check-uiparity` 输出为准，勿写死数字），已接入 `make vet` 与 CI 的 lint job。改 UI 契约时同步改断言，否则 `make vet` 红。
- **用户目录约定**：daemon / Go 工具走 `internal/appdirs`（macOS Application Support、Linux XDG、Windows LOCALAPPDATA；Linux socket 优先 `$XDG_RUNTIME_DIR`）；Linux 原生引擎 `daemon.cpp` 按同一 XDG 口径解析路径，壳/脚本需要路径时仍应调 `chaoset-userdict -print-paths`，不要在壳里各自拼路径。
- **自学习持久化**：用户词典、调频与上下文共现数据随 daemon 每 2 秒批量原子写盘（temp+rename，无 fsync；正常退出同步落盘，异常强杀最多丢最近 2 秒）。Linux 壳不再直接持有用户词典，落盘统一由 daemon 负责。`chaoset-userdict.json` 已 `.gitignore`。

## macOS 输入源图标（禁止回退）

输入源图标统一为 **22×16 宽扁 keycap**（满幅实心圆角按键 + alpha 挖空单字符「C」），
五个 TIS 图标键全部指向 `ChaosetIME-input.tiff`：System Settings、菜单栏（系统栏）、
快捷键切换 HUD 共用同一形态。该形态是硬性要求，不做拆分。

图标键只有 bundle / mode 两级，第三方输入法无法同时满足「系统栏 keycap + HUD 无边框字形」：
- System Settings 读 **bundle 级**图标键（`tsInputMethodIconFileKey` / `Alternate`）；
- 快捷键切换 HUD 与**菜单栏（系统栏）**读 **mode 级**图标键
  （`tsInputModeMenuIconFileKey` / `Alternate`、palette 键）；
- macOS 没有第三套图标键可把「菜单栏」与「HUD」再分开（`InputSource`
  的菜单栏标题图取 mode 级图标；`kTISPropertyIconImageURL` 对 mode 返回 mode 级文件），
  因此统一采用扁平带边框 keycap 版本，不做「HUD 无边框」变体（避免同一输入法在不同系统位置形态不一致）。

- 规格：22×16@72dpi + 44×32@144dpi 双帧、RGB 全黑、ExtraSamples=2、LZW 模板 TIFF；
  不要改成 16×16 正方形、描边空心块或纯色方块。
- 改动图标（尺寸/形态/配色/键映射）**前必须先问用户**；改完必须跑
  `./shims/macos/tests/equivalence.sh`（D 段 keycap 契约 + 五个键指向同一文件断言、
  E 段与系统渲染尺寸比对）与 `./shims/macos/build/icon-size`，不许改测试/降断言来“通过”。
- 详细要求与验收清单见 `shims/macos/README.md`「输入源图标（已确认，禁止回退）」。

## 本地构建与验证

```bash
make build       # 编译 bin/ 下的 chaosetd, chaoset-cli, chaoset-import, chaoset-dictgen, chaoset-userdict, chaoset-settingsctl
make test        # 运行引擎与协议测试 (go test ./...)
make vet         # 静态分析（go vet + workflow 结构 + 版本号单一真源 + Windows GDI 契约 + 三端 UI 一致性 check-uiparity）
make fmt         # 代码格式化（只格式化 git 跟踪的 .go，不扫 .workwork/）
make version     # 当前版本（tag CI 用 tag 号，其余用 年月日-commit hash）
make check-version  # 断言源码里没有写死的版本号、没有回退到 git describe --tags
```

- 交互测试台：`make run-cli`（本地内嵌引擎）或先 `make run-daemon` 再测试 socket 通信。

### 验证交给 CI，本地只跑轻量项

测试/构建重活一律交给 CI，本地跑会占用 CPU 影响正常使用电脑。**提交前本地只需要**：

```bash
gofmt -l $(git ls-files '*.go')   # 应无输出
go build ./...                    # 秒级
```

7 个并行 job 的覆盖范围、本地复跑方式与 CI 相关陷阱见 skill `ime-daemon-lifecycle`「五、CI 相关」。

### 发布必须由用户触发（禁止代理自行发版）

推 `v*` tag 会直接触发 `.github/workflows/release.yml` 并产出对外发布包。
代理**不得**自行 `git tag` / `git push origin v*` / `gh release create`——
未经验证的修复一旦打 tag 就变成用户可见的正式版本，问题代码会被当成发布版扩散。

- 修完只做：提交 → 合并到 `main` → `git push origin main`（只跑 CI，不发版）。
- 版本号、tag、Release 由用户决定并发起；需要发版时按用户指示操作。
- CHANGELOG 的 `## 未发布` 段落可以持续累积多条，等用户发版时再改成 `## vX.Y.Z（日期）`。

## 候选质量（拼音匹配 / 输入预测）

改动 `engine/` 里任何「输入串 → 候选列表」的逻辑（切分、匹配层级、排序打分、
续接预测）前，**必须先量化基线再改**，流程与打分陷阱见 skill `pinyin-quality-tuning`。
以下硬约束：

- 排序改动必须给出**前后对比数据**（命中率/首选率），不得以「看起来更合理」为由提交；
- 回归测试必须**双向**：既证明新能力生效，也证明不反噬既有行为
  （只写正向测试时，「把所有预测候选置顶」这种粗暴实现也能通过）；
- 经验参数（如 `continuationPenalty`）必须经完整基线数据扫描定型，
  取值理由写进代码注释与 README，不允许后来者凭手感调整。
- **整句学习安全约束**：整句式学习记录必须带分段（`Words.Segs` / `Phrases.Segs`），
  只在**完整输入**时作为整句候选、只在用户确认过的**分段边界**剥前缀；部分输入不得
  出现整词候选（打「今」绝不得给出「天星期一」）。细节与双向回归见 skill
  `pinyin-quality-tuning` 陷阱 6。
- **候选/排序硬约束**：候选只来自真实词条；组合候选只在 `rankComposition`，单字兜底
  只在 `rankSingleBackfill`（列表最末纯单字收尾）；错拼输入不追加单字兜底（纠错固定
  第 3 位）；单段输入只出单字；超段仲裁仅全拼。排序键顺序、回归测试清单与判据见
  skill `pinyin-quality-tuning`「1.1 当前排序键与用户优先规则」。

## 约束

- 词库校验分两层，不要混：
  - **构建期严格**——内置词库与 `data/dict_extra.tsv`/`data/dict_hot.tsv` 由 `cmd/chaoset-dictgen` 生成时
    强校验，`engine/data_contract_test.go` 在 CI 严格解析（规模/音节数/词频/手工词表已合并），
    一行脏数据即失败。**不要为了让用户词库「能启动」而放宽这一层**。
  - **运行期健壮**——用户自备的第三方词库（`-dict`）走 `LoadDictFilesLenient`：
    坏行跳过并汇总警告、拼音归一化；但文件打不开、或所有词库加载后词条数为 0，
    仍报错退出（配置错误/整体不可用必须立刻暴露）。
  - 非法音节解析仍必须拦截：严格路径报错，宽松路径跳过并计入警告——绝不静默污染。
- 内置词库：`data/dict_common.tsv` 是 `cmd/chaoset-dictgen` 的**生成产物，禁止手工编辑**；
  需要增删词条或改词频，先改生成工具（或 `data/dict_extra.tsv`/`data/dict_hot.tsv` 手工补充表）
  再重新生成（同一输入必须逐字节一致）。来源与许可见文件头注释与 `data/NOTICE.md`：
  **代码 MIT，词库数据 CC BY-SA 4.0（方案 A）**；单字 Jun Da 字频有商用许可风险，发布前须处理。
- 中间文件：所有测试日志、原型草稿一律放 `.workwork/`，禁止提交根目录临时生成的 `*.sock` 或词库临时产物。

## 按需加载的 skill

| skill | 何时加载 |
|---|---|
| `pinyin-quality-tuning` | 调拼音质量、候选排序、词库与评分参数 |
| `ime-daemon-lifecycle` | 输入法 daemon 的启停、注册、自启与生命周期排障 |
