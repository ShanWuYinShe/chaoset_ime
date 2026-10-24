# chaoset_ime

跨平台中文输入法：Go 核心引擎（chaosetd） + IPC 通信（Unix socket / TCP） + 平台原生薄壳（macOS IMK / Windows TSF）。

通用约束以全局 `~/AGENTS.md` 为准，本文件只做项目特有补充。

## 架构要点

- **引擎与平台解耦**：`engine/` 为纯 Go 逻辑，禁止引入任何平台特定 API 或 CGO 依赖；所有引擎状态机与算法必须 100% 能够通过纯单测覆盖。
- **IPC 协议契约**：4 字节大端长度 + JSON 载荷（上限 1 MiB）。macOS/Linux 默认走 Unix socket，Windows 走本机回环 TCP（127.0.0.1:7949）。
- **统一候选窗渲染（v2）**：`renderer/` 负责将候选词组绘制为内存 PNG 贴图；平台壳仅作贴图展示与事件转发，不自建渲染逻辑。候选窗的**布局（横排/竖排）、圆角、浅/深色配色全部由 daemon 侧 `renderer.Skin` 决定**（`Horizontal` / `MaxWidth` / `CornerRadius`，`DefaultSkinFor(dark)`），壳只按 `candidateImage` 的 `dark` / `scale` 传参，不得在壳里另写皮肤或布局逻辑。
- **自学习持久化**：用户词典、调频与上下文共现数据随 daemon 原子写盘（`chaoset-userdict.json`，已 `.gitignore`）。

## macOS 输入源图标（用户明确要求，禁止回退）

系统设置「添加输入法」列表、菜单栏与系统栏里的输入源图标必须是**系统原生同款的
22×16 宽扁 keycap**：实心圆角按键 + alpha 挖空的单字符「C」模板图
（`ChaosetIME-input.tiff`，22×16@72dpi + 44×32@144dpi 双帧、RGB 全黑、ExtraSamples=2）；
`ChaosetIME-palette.tiff` 为同尺寸纯字形 HUD 变体。

- **不要**改回 16×16 正方形、描边空心块或纯色方块——2026-10-24 用户已两次纠正。
- 依据：System Settings 对系统输入源按 `IPInputSource.iconImage` 实测的 **22×16** 渲染，
  第三方输入源按 TIS 图标文件原尺寸显示；给 16×16 就会显示成方块。
- 改动图标（尺寸/形态/配色）**前必须先问用户**；改完必须跑
  `./shims/macos/tests/equivalence.sh`（D 段尺寸/形态断言 + E 段与系统渲染尺寸比对）
  与 `./shims/macos/build/icon-size`，不许改测试/降断言来“通过”。
- 详细要求与验收清单见 `shims/macos/README.md`「图标要求（用户确认，禁止回退）」。

## 本地构建与验证

```bash
make build       # 编译 bin/ 下的 chaosetd, chaoset-cli, chaoset-import, chaoset-dictgen, chaoset-userdict
make test        # 运行引擎与协议测试 (go test ./...)
make vet         # 静态分析 (go vet ./...)
make fmt         # 代码格式化 (gofmt -l -w .)
```

- 交互测试台：`make run-cli`（本地内嵌引擎）或先 `make run-daemon` 再测试 socket 通信。

## 约束

- 词库导入：非法音节解析必须报错拦截，防止脏数据污染词库。
- 内置词库：`data/dict_common.tsv` 是 `cmd/chaoset-dictgen` 的**生成产物，禁止手工编辑**；
  需要增删词条或改词频，先改生成工具再重新生成（同一输入必须逐字节一致）。来源与许可见文件头注释。
- 中间文件：所有测试日志、原型草稿一律放 `.workwork/`，禁止提交根目录临时生成的 `*.sock` 或词库临时产物。
