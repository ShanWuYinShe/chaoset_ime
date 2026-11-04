# chaoset_ime

跨平台中文输入法：Go 核心引擎（chaosetd） + IPC 通信（Unix socket / Windows 命名管道） + 平台原生薄壳（macOS IMK / Windows TSF / Linux C++/libibus）。

通用约束以全局 `~/AGENTS.md` 为准，本文件只做项目特有补充。

## 架构要点

- **引擎与平台解耦**：`engine/` 为纯 Go 逻辑，禁止引入任何平台特定 API 或 CGO 依赖；所有引擎状态机与算法必须 100% 能够通过纯单测覆盖。该约束由 `engine/purity_test.go` 机器守卫：只允许标准库 import、禁止 `//go:build` 构建标签 / CGO / 平台文件（`*_windows.go` 等），违反即 `go test ./...` 红。
- **macOS 安装布局**：运行组件随 `ChaosetIME.app` 安装，daemon/导入工具/种子词库位于 `Contents/Resources/`；LaunchAgent 指向 app 内 daemon；`~/Library/Application Support/chaoset` 只放用户数据/词库/socket/日志。禁止把 daemon 单独拷到 Application Support。
- **职责边界**：`engine/` 只做拼音/联想算法；`shims/` 只做平台集成 + UI（设置窗也是 shim：macOS Swift / Windows C++ / Linux 原生 GTK3 C，**不要把设置窗搬进 Go**）；工具逻辑（词库合并、备份、原子替换、用户词典导入导出）必须在 Go `cmd/chaoset-*`，禁止在 shims 里重复实现。IPC 传输层归 `ipc/`；三端壳共用 `shims/common/ipc/core.{hpp,cpp}` 的 JSON/帧协议与 `Session` 封装。三端词库导入均委托 `chaoset-import -o ... -seed ... -backup`。Linux 壳必须是原生 C++/libibus（`shims/linux/engine/`），不得回退为 Go 进程内引擎。
- **IPC 协议契约**：4 字节大端长度 + JSON 载荷（上限 1 MiB），传输通过 `ipc.Endpoint` 抽象（只有 `unix`/`pipe` 两种，**没有 TCP**：不监听任何端口，也没有回退传输）。macOS/Linux 用 Unix socket；Windows 命名管道：daemon `-pipe` + C++ 壳只走管道，安全描述符 = DACL（当前用户 + SYSTEM + ALL APPLICATION PACKAGES）+ 低完整性标签 `S:(ML;;NW;;;LW)`（AppContainer 宿主是低完整性，缺标签只能连不能写，见 `ipc/pipe_sddl.go` 与其单测）；安装器 Run 项与 TSF 懒启动均用 `-pipe`；安装/升级**不得强杀用户程序或系统输入宿主**（由 `checkuiparity` 机器校验）。**daemon 自启与恢复**：登录 Run 项 / LaunchAgent 常驻，三端 shell 都有「连不上就拉起 daemon」的懒启动（Windows `ensureDaemonStarted`、macOS `EngineClient.spawnDaemonIfNeeded`、Linux `Daemon::ensureReady`，由 `checkuiparity` 断言三端都存在）；**AppContainer 宿主内不发起进程**（`CreateProcessW` 会继承 AppContainer 身份），只连接已运行的 daemon。Linux 原生引擎首次按键自动拉起 `chaosetd`；设置窗清空/导入词库前必须先停 daemon，防止内存旧状态回写。
- **日志**：daemon 用 `-log` 写启动构建/词库加载/用户词典路径/IPC 连接与请求错误；Windows TSF 壳写 `tsf.log`（失败与生命周期，单进程预算）与 `crash.log`；macOS 壳写统一日志 `subsystem=ime.chaoset.inputmethod`；Linux 壳写 stderr（journal）。排障先跑 `chaosetd -version`/`chaoset-cli -version` 确认构建，日志只覆盖启动/连接/失败路径，不按键写盘。
- **候选窗为纯数据 IPC + mac/Windows 原生自绘、Linux ibus 面板**：daemon 只返回 Composition/Candidate 纯数据（`input`/`select` 等不变）。macOS 用 AppKit（`shims/macos/src/CandidatePanel.swift`：键帽序号、首选蓝胶囊、学习标记 `*`，浅/深两套静态 sRGB 按 appearance 选择，支持鼠标点选与 VoiceOver）；Windows 用 GDI（`win32.cpp`，同样绘制学习标记 `*`）；Linux **不重绘候选窗**，用 ibus 原生 LookupTable/Preedit 呈现（学习标记不适用理由记在 `shims/linux/README.md`）。**daemon 不提供贴图渲染**（无 `renderer/` 包与 `candidateImage` IPC，勿在文档或壳里引用）。
- **视觉参数与一致性检查**：设计基准是 `skin/skin.go`（`DefaultSkinFor(dark)`，**无运行时消费者**），实际渲染实现是 macOS `CandidatePanel.swift` 与 Windows `win32.cpp` 两份。Go↔macOS 布局 5 项 + macOS 参考常量（含 divider）+ 浅/深各 13 色由 `shims/macos/tests/visual-parity.sh` 逐项机器断言（故意改字号/色值必须 FAIL）；**Windows 没有运行时视觉断言**（`tools/checkuiparity` 只做静态几何/配色字段契约；另有 pt 口径、交叉编译与核心行为测试），不要把它写成「双端已验证」。改动排版参数/按键矩阵必须同步更新 `docs/ui-parity.md` 与机器断言。
- **候选窗任何 UI 改动（控制区/齿轮/翻页/配色/层级）必须三端同步检查**：macOS `CandidatePanel.swift`、Windows `win32.cpp`（`settingsRect_`/齿轮绘制）、Linux ibus（无自绘 UI 时在 `shims/linux/README.md` 记录不适用理由）——只改一个平台 = 未完成。绘制机制按平台落地但**视觉形态必须一致**（齿轮 = 圆环+8 齿矢量算法，**仅 macOS/Windows 两端**同几何参数同色值；Linux ibus 面板无齿轮；macOS 端 SF Symbol 模板图离屏着色失效因此手绘矢量，Windows GDI 同算法实现），机制说明记录在各壳源码注释。Windows 布局/字号/间距统一 **pt 口径**（`pt(v)=MulDiv(v,dpi,72)`），不要退回 px@96。
- **三端设置窗**：macOS `shims/macos/setup/`（`SettingsWindow.swift`，含「词库导入」入口——打开 Setup.app 并自动弹导入面板）、Windows 设置窗、Linux 原生 GTK3 C 设置窗（`shims/linux/setup/chaoset-setup.c` + `setup_ctl.c`，`Gtk.Application` 单例 + hicolor 小尺寸图标，学习数据管理与词库导入在 GUI 内，只调用 Go 工具/ibus，engine 零改动；**禁止使用 Python/PyGObject**）。三端的词库导入安全流程与设置项契约由 `tools/checkuiparity` 静态断言，改动任一端设置窗必须三端同步并更新 `docs/ui-parity.md`。
- **版本可见性（三端统一）**：三端设置窗底部必须显示 `Chaoset 版本 x.y.z`；macOS 发布包为 `chaoset-macos.zip`（不提供 DMG）；`make check-uiparity` 的共享文案断言含「Chaoset 版本」，改设置窗时同步改。
- **三端 UI 一致性静态检查（`make check-uiparity`）**：`tools/checkuiparity` 覆盖设置窗、学习数据、词库导入与安全、联想即时性、退格/修饰键语义、候选窗几何/配色、CI 覆盖、shell 转义、文档对照等三端契约（组数以 `make check-uiparity` 输出为准，勿写死数字），已接入 `make vet` 与 CI 的 lint job。改 UI 契约时同步改断言，否则 `make vet` 红。
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
make build       # 编译 bin/ 下的 chaosetd, chaoset-cli, chaoset-import, chaoset-dictgen, chaoset-userdict
make test        # 运行引擎与协议测试 (go test ./...)
make vet         # 静态分析（go vet + workflow 结构 + 版本号单一真源 + Windows GDI 契约 + 三端 UI 一致性 check-uiparity）
make fmt         # 代码格式化 (gofmt -l -w .)
make version     # 当前版本（由 git tag 推导）
make check-version  # 断言源码里没有写死的版本号
```

- 交互测试台：`make run-cli`（本地内嵌引擎）或先 `make run-daemon` 再测试 socket 通信。

### 验证交给 CI，本地只跑轻量项

测试/构建重活一律交给 CI，本地跑会占用 CPU 影响正常使用电脑。**提交前本地只需要**：

```bash
gofmt -l $(git ls-files '*.go')   # 应无输出
go build ./...                    # 秒级
```

6 个并行 job 的覆盖范围、本地复跑方式与 CI 相关陷阱见 skill `ime-daemon-lifecycle`「五、CI 相关」。

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
