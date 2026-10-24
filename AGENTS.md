# chaoset_ime

跨平台中文输入法：Go 核心引擎（chaosetd） + IPC 通信（Unix socket / TCP） + 平台原生薄壳（macOS IMK / Windows TSF）。

通用约束以全局 `~/AGENTS.md` 为准，本文件只做项目特有补充。

## 架构要点

- **引擎与平台解耦**：`engine/` 为纯 Go 逻辑，禁止引入任何平台特定 API 或 CGO 依赖；所有引擎状态机与算法必须 100% 能够通过纯单测覆盖。
- **IPC 协议契约**：4 字节大端长度 + JSON 载荷（上限 1 MiB）。macOS/Linux 默认走 Unix socket，Windows 走本机回环 TCP（127.0.0.1:7949）。
- **统一候选窗渲染（v2）**：`renderer/` 负责将候选词组绘制为内存 PNG 贴图；平台壳仅作贴图展示与事件转发，不自建渲染逻辑。候选窗的**布局（横排/竖排）、圆角、浅/深色配色全部由 daemon 侧 `renderer.Skin` 决定**（`Horizontal` / `MaxWidth` / `CornerRadius`，`DefaultSkinFor(dark)`），壳只按 `candidateImage` 的 `dark` / `scale` 传参，不得在壳里另写皮肤或布局逻辑。
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
make vet         # 静态分析 (go vet ./...)
make fmt         # 代码格式化 (gofmt -l -w .)
```

- 交互测试台：`make run-cli`（本地内嵌引擎）或先 `make run-daemon` 再测试 socket 通信。

## 约束

- 词库导入：非法音节解析必须报错拦截，防止脏数据污染词库。
- 内置词库：`data/dict_common.tsv` 是 `cmd/chaoset-dictgen` 的**生成产物，禁止手工编辑**；
  需要增删词条或改词频，先改生成工具再重新生成（同一输入必须逐字节一致）。来源与许可见文件头注释。
- 中间文件：所有测试日志、原型草稿一律放 `.workwork/`，禁止提交根目录临时生成的 `*.sock` 或词库临时产物。
