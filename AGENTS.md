# chaoset_ime

跨平台中文输入法：Go 核心引擎（chaosetd） + IPC 通信（Unix socket / TCP） + 平台原生薄壳（macOS IMK / Windows TSF）。

通用约束以全局 `~/AGENTS.md` 为准，本文件只做项目特有补充。

## 架构要点

- **引擎与平台解耦**：`engine/` 为纯 Go 逻辑，禁止引入任何平台特定 API 或 CGO 依赖；所有引擎状态机与算法必须 100% 能够通过纯单测覆盖。
- **IPC 协议契约**：4 字节大端长度 + JSON 载荷（上限 1 MiB）。macOS/Linux 默认走 Unix socket，Windows 走本机回环 TCP（127.0.0.1:7949）。
- **候选窗渲染（v3，2026-10-25 起）**：**macOS 壳用 AppKit 原生自绘候选窗**（`shims/macos/src/CandidatePanel.swift`，视觉复刻 `renderer.Skin` 设计：键帽序号、首选蓝胶囊、学习标记），数据来自 daemon 的 Composition（纯数据 IPC，`input`/`select` 等不变）；深浅色用 NSColor 动态色自动跟随，支持鼠标点选候选与 VoiceOver 读取。**Windows/Linux 壳继续用 daemon `renderer` PNG 贴图**（`candidateImage` IPC 保留，亦是 macOS 的回退路径）。候选窗视觉调整需双端同步：`renderer/renderer.go`（贴图端）与 `CandidatePanel.swift`（macOS 原生端）。
- **自学习持久化**：用户词典、调频与上下文共现数据随 daemon 原子写盘（`chaoset-userdict.json`，已 `.gitignore`）。

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
make vet         # 静态分析（go vet + workflow 结构 + 版本号单一真源）
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
| lint | gofmt / go vet / workflow 结构 / 版本检查 / 构建 | 快，但统一在 CI 跑保证一致 |
| test | go test ./...（含 CJK 字体渲染断言） | 要装字体、约 30s |
| race | `go test -race` | 最重的一项，本机 45s+ |
| linux-e2e | 真实 ibus-daemon 下的组词/上屏/多实例隔离/中英切换/翻页 | 需要 dbus + ibus，占资源 |
| macos-build | macOS 壳构建 + universal 自检 + 等价性 + 双端视觉一致性 | swiftc 构建 2-3 分钟 |

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

## 约束

- 词库导入：非法音节解析必须报错拦截，防止脏数据污染词库。
- 内置词库：`data/dict_common.tsv` 是 `cmd/chaoset-dictgen` 的**生成产物，禁止手工编辑**；
  需要增删词条或改词频，先改生成工具再重新生成（同一输入必须逐字节一致）。来源与许可见文件头注释。
- 中间文件：所有测试日志、原型草稿一律放 `.workwork/`，禁止提交根目录临时生成的 `*.sock` 或词库临时产物。
