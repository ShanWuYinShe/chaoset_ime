# chaoset_ime

跨平台中文输入法：Go 核心引擎（chaosetd） + IPC 通信（Unix socket / TCP） + 平台原生薄壳（macOS IMK / Windows TSF）。

通用约束以全局 `~/AGENTS.md` 为准，本文件只做项目特有补充。

## 架构要点

- **引擎与平台解耦**：`engine/` 为纯 Go 逻辑，禁止引入任何平台特定 API 或 CGO 依赖；所有引擎状态机与算法必须 100% 能够通过纯单测覆盖。
- **IPC 协议契约**：4 字节大端长度 + JSON 载荷（上限 1 MiB）。macOS 走 Unix socket，Windows 走本机回环 TCP（127.0.0.1:7949）；Linux ibus 引擎进程内直调 `engine`，无 IPC 往返（daemon 仍可用于终端测试台）。
- **候选窗为纯数据 IPC + mac/Windows 原生自绘、Linux ibus 面板（2026-10-27 起）**：daemon 只返回 Composition/Candidate 纯数据（`input`/`select` 等不变）。macOS 用 AppKit（`shims/macos/src/CandidatePanel.swift`：键帽序号、首选蓝胶囊、学习标记 `*`，浅/深两套静态 sRGB 按 appearance 选择，支持鼠标点选与 VoiceOver）；Windows 用 GDI（`win32.cpp`，同样绘制学习标记 `*`）；Linux **不重绘候选窗**，用 ibus 原生 LookupTable/Preedit 呈现（学习标记不适用理由记在 `shims/linux/README.md`）。**daemon 贴图渲染链路已删**（`renderer/` 包与 `candidateImage` IPC，2026-10-27；勿在文档或壳里引用）。
- **视觉参数与一致性检查**：设计基准是 `skin/skin.go`（`DefaultSkinFor(dark)`，**无运行时消费者**），实际渲染实现是 macOS `CandidatePanel.swift` 与 Windows `win32.cpp` 两份。Go↔macOS 布局 5 项 + macOS 参考常量（含 divider）+ 浅/深各 13 色由 `shims/macos/tests/visual-parity.sh` 逐项机器断言（故意改字号/色值必须 FAIL）；**Windows 没有运行时视觉断言**（`tools/checkuiparity` 只做静态几何/配色字段契约；另有 pt 口径、交叉编译与核心行为测试），不要把它写成「双端已验证」。改动排版参数/按键矩阵必须同步更新 `docs/ui-parity.md` 与机器断言。
- **候选窗任何 UI 改动（控制区/齿轮/翻页/配色/层级）必须三端同步检查**：macOS `CandidatePanel.swift`、Windows `win32.cpp`（`settingsRect_`/齿轮绘制）、Linux ibus（无自绘 UI 时在 `shims/linux/README.md` 记录不适用理由）——只改一个平台 = 未完成（2026-10-28 齿轮五轮迭代漏同步 Windows 的教训）。绘制机制按平台落地但**视觉形态必须一致**（齿轮 = 圆环+8 齿矢量算法，**仅 macOS/Windows 两端**同几何参数同色值；Linux ibus 面板无齿轮；macOS 端 SF Symbol 模板图离屏着色失效因此手绘矢量，Windows GDI 同算法实现），机制说明记录在各壳源码注释。Windows 布局/字号/间距统一 **pt 口径**（`pt(v)=MulDiv(v,dpi,72)`），不要退回 px@96。
- **三端设置窗（2026-10-31 新增）**：macOS `shims/macos/setup/`（`SettingsWindow.swift`，含「词库导入」入口——打开 Setup.app 并自动弹导入面板）、Windows 设置窗、Linux GTK 设置窗（`Gtk.Application` 单例 + hicolor 小尺寸图标，学习数据管理与词库导入搬进 GUI，engine 零改动）。三端的词库导入安全流程与设置项契约由 `tools/checkuiparity` 静态断言，改动任一端设置窗必须三端同步并更新 `docs/ui-parity.md`。
- **三端 UI 一致性静态检查（`make check-uiparity`）**：`tools/checkuiparity` 覆盖设置窗、学习数据、词库导入与安全、联想即时性、退格/修饰键语义、候选窗几何/配色、CI 覆盖、shell 转义、文档对照等三端契约（组数以 `make check-uiparity` 输出为准，勿写死数字），已接入 `make vet` 与 CI 的 lint job。改 UI 契约时同步改断言，否则 `make vet` 红。
- **自学习持久化**：用户词典、调频与上下文共现数据随 daemon 每 2 秒批量原子写盘（temp+rename，无 fsync；正常退出同步落盘，异常强杀最多丢最近 2 秒）。Linux ibus 引擎同样启用延迟落盘。`chaoset-userdict.json` 已 `.gitignore`。

## macOS 输入源图标（用户明确要求，禁止回退）

输入源图标统一为 **22×16 宽扁 keycap**（满幅实心圆角按键 + alpha 挖空单字符「C」），
五个 TIS 图标键全部指向 `ChaosetIME-input.tiff`：System Settings、菜单栏（系统栏）、
快捷键切换 HUD 共用同一形态。这是用户 2026-10-25 确认 macOS 限制后的最终要求。

为什么不做「系统设置+系统栏 keycap、HUD 无边框字形」的拆分（2026-10-25 探针实测）：
- System Settings → 读 **bundle 级**图标键（`tsInputMethodIconFileKey` / `Alternate`）；
- 快捷键切换 HUD 与**菜单栏（系统栏）** → 读 **mode 级**图标键
  （`tsInputModeMenuIconFileKey` / `Alternate`、palette 键）；
- macOS 没有第三套图标键可把「菜单栏」与「HUD」再分开（`InputSource`
  的菜单栏标题图取 mode 级图标；`kTISPropertyIconImageURL` 对 mode 返回 mode 级文件），
  因此第三方输入法无法同时满足「系统栏 keycap + HUD 无边框字形」；
- 用户选择统一为扁平带边框 keycap 版本（避免同一输入法在不同系统位置形态不一致）。

- 规格：22×16@72dpi + 44×32@144dpi 双帧、RGB 全黑、ExtraSamples=2、LZW 模板 TIFF；
  不要改成 16×16 正方形、描边空心块或纯色方块。
- 改动图标（尺寸/形态/配色/键映射）**前必须先问用户**；改完必须跑
  `./shims/macos/tests/equivalence.sh`（D 段 keycap 契约 + 五个键指向同一文件断言、
  E 段与系统渲染尺寸比对）与 `./shims/macos/build/icon-size`，不许改测试/降断言来“通过”。
- 详细要求与验收清单见 `shims/macos/README.md`「图标要求（用户确认，禁止回退）」。

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

### 验证交给 CI，本地只跑轻量项（2026-10-26 用户要求）

用户希望测试尽量由 GitHub Actions 承担，本地跑测试会占用 CPU 影响正常使用电脑。
因此 **提交前本地只需要**：

```bash
gofmt -l $(git ls-files '*.go')   # 应无输出
go build ./...                    # 秒级
```

其余全部由 CI 并行执行（单次提交约 3-5 分钟出全部结论）：

| CI job | 覆盖内容 | 为什么不在本地跑 |
|---|---|---|
| lint | gofmt / go vet / workflow 结构 / 版本检查 / Windows GDI 契约 / 三端 UI 一致性（`check-uiparity`）/ 构建 | 快，但统一在 CI 跑保证一致 |
| test | go test ./...（数据契约/引擎/壳行为测试） | 约 30s |
| race | `go test -race` | 最重的一项，本机 45s+ |
| linux-e2e | 真实 ibus-daemon 下的组词/上屏/多实例隔离/中英切换/翻页；Linux 安装包自检（tar.gz/deb 必含设置窗模块） | 需要 dbus + ibus，占资源 |
| macos-build | macOS 壳构建 + universal 自检 + 等价性 + 双端视觉一致性 | swiftc 构建 2-3 分钟 |
| windows-build | Windows 壳 mingw 交叉编译 + `core_test`（真实 daemon）+ `makensis` 安装包编译 + ZIP 自检 | 需要 mingw-w64/NSIS，本机不装；2026-10-31 起不再只在打 tag 时验证 |

本地真需要跑完整验证时，`make test`、`shims/linux/test-ci.sh`、
`shims/macos/tests/equivalence.sh` 都能直接执行（与 CI 同一套脚本）。

## 候选质量（拼音匹配 / 输入预测）

改动 `engine/` 里任何「输入串 → 候选列表」的逻辑（切分、匹配层级、排序打分、
续接预测）前，**必须先量化基线再改**，流程与已踩过的打分陷阱见项目技能
`.agents/skills/pinyin-quality-tuning/SKILL.md`。三条硬约束：

- 排序改动必须给出**前后对比数据**（命中率/首选率），不得以「看起来更合理」为由提交；
- 回归测试必须**双向**：既证明新能力生效，也证明不反噬既有行为
  （只写正向测试时，「把所有预测候选置顶」这种粗暴实现也能通过）；
- 经验参数（如 `continuationPenalty`）必须经完整基线数据扫描定型，
  取值理由写进代码注释与 README，不允许后来者凭手感调整。
- **整句学习安全约束（2026-10-30 用户硬性要求）**：整句式学习记录必须带分段
  （`Words.Segs` / `Phrases.Segs`），只在**完整输入**时作为整句候选；部分输入
  不得出现整词候选；联想只能在用户确认过的**分段边界**剥前缀——打「今」绝不
  得给出「天星期一」。改动学习/联想/候选生成逻辑后必须保留
  `engine/sequence_learn_test.go` 的双向回归（完整输入学到的整句可用 +
  部分输入不反噬「今天」）。

## 约束

- 词库校验分两层，不要混：
  - **构建期严格**——内置词库与 `data/dict_extra.tsv` 由 `cmd/chaoset-dictgen` 生成时
    强校验，`engine/data_contract_test.go` 在 CI 严格解析（规模/音节数/词频/extra 已合并），
    一行脏数据即失败。**不要为了让用户词库「能启动」而放宽这一层**。
  - **运行期健壮**——用户自备的第三方词库（`-dict`）走 `LoadDictFilesLenient`：
    坏行跳过并汇总警告、拼音归一化；但文件打不开、或所有词库加载后词条数为 0，
    仍报错退出（配置错误/整体不可用必须立刻暴露）。
  - 非法音节解析仍必须拦截：严格路径报错，宽松路径跳过并计入警告——绝不静默污染。
- 内置词库：`data/dict_common.tsv` 是 `cmd/chaoset-dictgen` 的**生成产物，禁止手工编辑**；
  需要增删词条或改词频，先改生成工具再重新生成（同一输入必须逐字节一致）。来源与许可见文件头注释。
- 中间文件：所有测试日志、原型草稿一律放 `.workwork/`，禁止提交根目录临时生成的 `*.sock` 或词库临时产物。

## 按需加载的 skill（`.agents/skills/`）

| skill | 何时加载 |
|---|---|
| `pinyin-quality-tuning` | 调拼音质量、候选排序、词库与评分参数 |
| `ime-daemon-lifecycle` | 输入法 daemon 的启停、注册、自启与生命周期排障 |
