# 架构与模块边界

本文是 chaoset_ime 的架构地图与拆分契约。**新增/移动平台壳代码或 Rust crate 模块前先更新本文件与
`tools/checkuiparity` 的模块清单**；shims 文件规模与模块存在性由机器断言锁死（见 §4），
crate 文件预算登记在 §3.1。

发行面 **100% Rust**：daemon（`crates/chaosetd`）、引擎（`crates/chaoset-engine`）、设置
schema/默认值/持久化（`crates/chaoset-config`）、IPC 协议与客户端（`crates/chaoset-ipc`）、
词库导入/用户词典离线逻辑（`crates/chaoset-tools`）与命令行（`crates/chaosetctl`）都是 Rust。
Go 只保留构建期词库生成 `cmd/chaoset-dictgen` 与静态检查
`tools/checkuiparity|checkworkflows|checkgdi`（及对应单测），不再有 engine/daemon/CLI/import/
userdict/settings/ipc。

设置 UI 只有一份实现：Slint 跨平台设置中心（`shims/settings/`）。三端壳不再各自
实现原生设置窗，只保留启动器把 `chaoset-settings` 拉起来（见 §2、§3）；shims 大文件
按职责拆分，文件规模由 checkuiparity 机器断言。

## 1. 分层与依赖方向

```
crates/                 Rust 发行面（唯一实现）
  chaoset-engine/         拼音/联想算法（纯逻辑，不依赖平台 API/UI）
  chaosetd/               守护进程：IPC 服务端 + 会话 + 用户词典落盘
  chaoset-config/         设置 schema/默认值/持久化 + 用户目录（三端唯一真源）
  chaoset-ipc/            IPC 帧协议 + 客户端（19 方法、1 MiB 上限）
  chaoset-tools/          词库导入/合并/备份/原子替换 + 用户词典离线读写
  chaosetctl/             单二进制 multicall CLI（config/import/userdict/client）
ipc/                    （Go 包已删除；IPC 壳侧实现只在 shims/common/ipc/）
shims/common/ipc/       三端壳共用的 C++ IPC 客户端（core.hpp/core.cpp）
shims/windows|macos|linux/   平台壳：只做平台集成 + 渲染 + 系统交互
internal/               仅 dict：dictgen 使用的词库格式/拼音归一化（Go 构建期）
cmd/                    仅 chaoset-dictgen：构建期词库生成（Go）
tools/                  Go 静态检查（不参与发行）
```

IPC 的壳侧实现只有 `shims/common/ipc/core.{hpp,cpp}`；Go 侧的 `ipc` 包（协议/客户端/服务端）
已随 Go 侧实现删除，客户端唯一实现是 `crates/chaoset-ipc`。

规则：

1. **单向依赖**：`chaosetctl` → `chaoset-tools` / `chaoset-ipc` / `chaoset-config`；
   `chaosetd` → `chaoset-engine` / `chaoset-ipc` / `chaoset-config`；`chaoset-tools` →
   `chaoset-config`。shims 只允许通过 IPC 调 daemon（C++ `core.cpp`），不 import Rust crate、
   不内嵌引擎。Go 只允许 dictgen/静态检查：不得重新引入 daemon/CLI/导入/设置/ipc 实现，
   也不得再引入引擎（`checkuiparity`「Go 引擎已移除」组断言）。
2. **shims 内**：平台壳只做「平台集成 + 渲染 + 系统交互」；业务逻辑（设置 schema/持久化、
   学习数据、词库合并/备份/导入、用户词典导出）归 Rust crate（`chaoset-config` /
   `chaoset-tools`），shim 不得把同一份业务逻辑再实现一遍。壳侧冷路径统一调同目录
   `chaosetctl`（config/userdict/import）；输入热路径读本机缓存/原生存储，**不得在
   TSF/IMK/ibus 回调里起进程**。
3. **大小预算**：单文件超过预算即视为架构回退，checkuiparity 会 FAIL。拆分时按职责，
   不要按行数硬切；拆分文件必须登记到本文件的模块地图。
4. **禁止反向依赖**：Windows 叶子模块不得 include/引用 TIP/CandidateWindow 类型；
   由 `win32.cpp`（DLL 生命周期编排）与 `tip.hpp` / `tip_*.cpp`（TIP 主体）向下编排。
   macOS/Linux 同理：页面/视图不反向依赖应用控制器。

## 2. 设置与 CLI 单一真源

### 2.1 现状

设置 UI 唯一实现是 Slint 设置中心（`shims/settings`）：页面/控件/文案/视觉令牌只有
一份。macOS AppKit / Windows GDI / Linux GTK 三套原生设置窗已删除，避免三份实现
漂移。三端壳只保留启动器，设置中心冷路径统一经同目录 `chaosetctl` 读写。

设置项的 schema/默认值/校验/持久化**唯一实现**是 `crates/chaoset-config`，daemon 与 CLI
共用；三端存储键保持不变（跨进程契约，由 `crates/chaoset-config/tests/constants_lock.rs`
锁定）：

| 平台 | 存储位置 |
| :--- | :--- |
| macOS | UserDefaults 域 `ime.chaoset.inputmethod.ChaosetIME` |
| Windows | `HKCU\\Software\\Chaoset`（Associate/Fuzzy/Shuangpin/ModeScope/UiEnglish/Theme） |
| Linux | `$XDG_CONFIG_HOME/chaoset/config.json`（associate/fuzzy/shuangpin/mode_scope/theme） |

### 2.2 目标结构

```
crates/chaoset-config/           设置 schema/默认值/持久化 + 用户目录（唯一真源）
  src/model.rs                   Settings / ModeScope / Shuangpin / Theme、默认值、单行 JSON 契约
  src/parse.rs                   on/off、shuangpin、mode-scope（旧值 window→process）、theme 解析
  src/paths.rs                   三端数据目录 / socket / config 路径（壳与脚本共用口径）
  src/store/{darwin,windows,linux}.rs   平台存储实现
  src/behavior.rs                daemon 热路径 lenient load（读不到用缺省）
crates/chaosetctl/               CLI（multicall：config/import/userdict/client/version）
shims/settings/                  基于 Slint 的跨平台统一设置中心（唯一设置 UI）
```

CLI 契约（供 shim/脚本调用；**冷路径**使用，热路径仍读本机缓存/原生存储）：

| 命令 | 输出/行为 |
| :--- | :--- |
| `chaosetctl config get` | 单行 JSON：`{"associate":true,"fuzzy":false,"shuangpin":"","mode_scope":"system","ui_english":false,"theme":"system"}` |
| `chaosetctl config set associate on\|off` | 写 Associate，成功退出 0 |
| `chaosetctl config set fuzzy on\|off` | 写模糊音开关（zh/z、ch/c、sh/s、n/l、an/ang、en/eng、in/ing） |
| `chaosetctl config set shuangpin full\|zrm\|mspy\|flypy` | 写双拼方案（full = 全拼，等同空串） |
| `chaosetctl config set mode-scope system\|process` | 写作用域（两档；旧值 window 也接受并归一化为 process），成功退出 0 |
| `chaosetctl config set theme system\|light\|dark` | 写设置中心外观主题 |
| `chaosetctl config set ui-english on\|off` | 写 UI 宿主持久化中英状态（内部用） |

退出码：0 成功；1 运行错误（存储不可用等）；2 参数非法。错误写 stderr，stdout 只放 JSON。
`chaoset-settingsctl` 是历史调用名：同一二进制按 argv[0] multicall 兼容分发（也可用
`chaosetctl settingsctl ...`），新脚本统一写 `chaosetctl config ...`；发行包不再单独提供
`chaoset-settingsctl` 可执行文件。

### 2.3 冷路径 / 热路径

热路径不调用 CLI/进程：三端壳沿用本机缓存（Windows config TTL 缓存、macOS/Linux 进程内
缓存），CLI 只负责**写**与设置窗**读**，保证输入延迟不受影响。设置中心是独立 GUI 进程，
导入成功或模糊音/双拼变更后重启 daemon 生效。

## 3. 模块地图

### 3.1 Rust crates（`crates/`）

| 文件 | 职责 | 预算 |
| :--- | :--- | :--- |
| `chaoset-engine/src/session.rs` | 组词会话状态机：切分/召回/排序/学习/联想 | ≤3000 |
| `chaoset-engine/src/dict.rs` | 音节 Trie 词典、TSV 加载、查询/续接/连写 DP | ≤800 |
| `chaoset-engine/src/userdict.rs` | 用户调频/词组/上下文学习记录与查询、持久化 | ≤1100 |
| `chaoset-engine/src/shuangpin.rs` | 自然码/微软/小鹤双拼键码管线 | ≤1100 |
| `chaoset-engine/src/syllable.rs` | 音节表、切分、续接、纠错生成 | ≤950 |
| `chaoset-engine/src/typo.rs` | 错拼修正与组合候选的 DP | ≤250 |
| `chaoset-engine/src/dict_binary.rs` | CSDB 二进制词库编解码 | ≤550 |
| `chaoset-engine/src/{punct,fuzzy,s2t}.rs` / `lib.rs` | 标点映射 / 模糊音 / 简繁 / 导出 | ≤200 / ≤50 |
| `chaoset-engine/src/bin/{dictc,replay}.rs` | 构建期词库编译 / 41 场景回放 | ≤150 / ≤350 |
| `chaosetd/src/main.rs` | daemon 参数、词库/用户词典加载、启动编排 | ≤600 |
| `chaosetd/src/ipc.rs` | IPC 方法分发（19 方法）与会话调用 | ≤500 |
| `chaosetd/src/server.rs` | Unix socket / 命名管道服务端与 ACL（含 SDDL 构造） | ≤320 |
| `chaosetd/src/singleton.rs` | 单实例锁（flock / Windows 互斥体） | ≤150 |
| `chaosetd/src/ui.rs` | Windows UI 宿主懒启动 | ≤80 |
| `chaoset-config/src/model.rs` | 设置模型、默认值、JSON 契约 | ≤200 |
| `chaoset-config/src/parse.rs` | 设置值解析（含旧值归一化） | ≤200 |
| `chaoset-config/src/paths.rs` | 三端用户目录/socket/config 路径 | ≤200 |
| `chaoset-config/src/store/{darwin,windows,linux}.rs` | 三端存储实现 | ≤180 / ≤260 / ≤220 |
| `chaoset-config/src/{behavior,constants}.rs` | daemon lenient 读取 / 跨进程常量表 | ≤100 / ≤100 |
| `chaoset-ipc/src/client.rs` | IPC 客户端（单连接串行、分页导出、关连接规则） | ≤750 |
| `chaoset-ipc/src/protocol.rs` | 帧协议（4 字节大端长度 + JSON、1 MiB） | ≤150 |
| `chaoset-ipc/src/dto.rs` | 协议 DTO（Composition/Candidate/UserStats） | ≤60 |
| `chaoset-tools/src/dictimport.rs` | 热词导入/合并/备份/原子替换 | ≤520 |
| `chaoset-tools/src/dictparse.rs` | TSV / Rime dict.yaml / CC-CEDICT 解析 | ≤420 |
| `chaoset-tools/src/fsutil.rs` | 原子写、备份、权限等文件原语 | ≤340 |
| `chaoset-tools/src/userdict.rs` | 用户词典本地模式：统计/清空/导出/删除 | ≤350 |
| `chaosetctl/src/multicall.rs` | argv[0] 多调用与子命令分发 | ≤250 |
| `chaosetctl/src/settingsctl.rs` | `config` 子命令（读写设置） | ≤280 |
| `chaosetctl/src/userdictcmd.rs` | `userdict` 子命令（IPC/本地模式、`-print-paths`） | ≤330 |
| `chaosetctl/src/client.rs` | `client` 终端测试台 | ≤400 |
| `chaosetctl/src/importcmd.rs` / `buildinfo.rs` / `main.rs` | import 委托 / 构建信息 / 入口 | ≤50 / ≤50 / ≤50 |

（`chaosetctl` 按 argv[0] 兼容 `chaoset` / `chaoset-cli` / `chaoset-import` /
`chaoset-userdict` / `chaoset-settingsctl`；Linux 发行包用软链共享同一二进制，
macOS app 带 `chaosetctl` + `chaoset-import` 两份同源副本（各自独立签名，签名后字节不同），
Windows 只有 `chaosetctl.exe`。）

### 3.2 Windows（`shims/windows/src/`）

| 文件 | 职责 | 依赖 | 预算 |
| :--- | :--- | :--- | :--- |
| `win32.cpp` | DLL 生命周期编排：ChaosetFactory、ChaosetRunUi 导出、DllMain/DllGetClassObject/DllCanUnloadNow、g_liveObjects 存活计数 | 全部 Windows 壳模块（含 tip.hpp） | ≤1200（当前 129；≤2000 目标已达成） |
| `tip.hpp` | TIP 主体 class ChaosetTIP 完整声明（成员/嵌套 ContextRef/继承接口与 TipThreadSink/UiElementHost）、kVk*/kPageSize 常量、UILess 协商成员 | candidate_window, mode_toast, tip_thread, tip_ids, shell_worker, winutil, config, transport, daemon, diagnostics, core | ≤500 |
| `tip_core.cpp` | TIP 生命周期/激活状态机/中英模式/前台钩子/worker 回投入口 | tip.hpp | ≤1100（当前 ~955） |
| `tip_profiles.cpp` | TSF 信号 sink：ITfThreadFocusSink/ITfActiveLanguageProfileNotifySink/ITfInputProcessorProfileActivationSink（键盘布局切换只有它通知，托盘隐藏权威事件）的挂载与回调、isActiveTip 激活档案查询 | tip.hpp | ≤300 |
| `tip_keys.cpp` | ITfKeyEventSink 五入口、Shift 状态机、修饰键/键位映射、wouldHandle/handleKey | tip.hpp | ≤1100 |
| `tip_composition.cpp` | EditSession + focusContext/can*/render/光标/预编辑/提交/结束合成/双拼方案与会话收尾；UILess 官方协商（BeginUIElement/UpdateUIElement/EndUIElement） | tip.hpp, core | ≤900 |
| `tip_candidates.cpp` | 候选点选/删除/翻页、showToast、设置窗入口、联想面板与 CandidateWindowHost 转发 | tip.hpp | ≤500 |
| `candidate_window.cpp` `.hpp` | 候选窗 GDI 渲染/DPI/鼠标与右键菜单/UIA 宿主 provider/齿轮笔/防卸载计数；TIP 侧只经 CandidateWindowHost 抽象接口 | winutil, theme, config, diagnostics, candidate_layout, core | ≤1200 / ≤300（当前 1179；接近上限，后续改动先按职责拆分） |
| `shell_worker.cpp` `.hpp` | 每进程后台 worker 任务队列、post* 投递、daemon 懒启动/连接、设置窗拉起、tsf.log 异步落盘 | winutil, core, transport, daemon, ui_client, diagnostics, tip_thread | ≤600 / ≤250 |
| `tip_thread.cpp` `.hpp` | kTip* 消息常量、worker→TIP 线程回投、message-only 窗口过程/创建销毁、按线程 TIP 注册表与前台钩子表；对 TIP 只经 TipThreadSink 抽象接口分发（不依赖具体 TIP 类） | winutil | ≤350 / ≤200 |
| `tip_registration.cpp` `.hpp` | COM/TSF 注册与反注册（DllRegisterServer/DllUnregisterServer/DllUninstallServer，冷路径） | tip_ids, winutil | ≤300 / ≤120 |
| `tip_ids.hpp` | CLSID/GUID/TSF 接口与类别常量（C++17 inline const GUID，namespace chaoset::win） | — | ≤150 |
| `mode_toast.cpp` `.hpp` | 模式切换提示条（窗口生命周期/绘制/防卸载计数） | winutil, theme, diagnostics | ≤400 / ≤150 |
| `diagnostics.cpp` `.hpp` | tsf.log/crash.log 诊断、VEH 崩溃取证 | — | ≤700 |
| `winutil.cpp` `.hpp` | 窗口类名、DPI、AppContainer 判定、当前用户 SID/会话、宽字符转换、浮窗 owner 判定 | — | ≤500 |
| `theme.cpp` `.hpp` | 候选窗配色/主题判定（与 macOS 视觉契约） | winutil | ≤500 |
| `config.cpp` `.hpp` | HKCU 设置读写、配置 TTL 缓存、全屏抑制、UILess 激活开关 | winutil, diagnostics | ≤700 |
| `ui_element.cpp` `.hpp` | UILess 官方协商：ITfCandidateListUIElement 实现（mingw 缺声明自补，IID/标志三源验证）+ UiElementHost 回呼；TIP 侧经 snapshot 灌数据、协商生命周期归 tip_composition/tip_core（开关 UiElementRender 启用时使用） | winutil | ≤450 / ≤200 |
| `transport.cpp` `.hpp` | daemon 命名管道名/探测/PipeTransport | winutil | ≤500 |
| `daemon.cpp` `.hpp` | daemon 懒启动/连接/熔断原语（仅 worker 调用） | winutil, transport, diagnostics | ≤500 |
| `ui_client.cpp` `.hpp` | UI 宿主管道低层（命令/查询/拉起原语） | winutil, diagnostics | ≤700 |
| `settings_launcher.cpp` `.hpp` | 统一设置中心启动器：定位/前置/启动 `chaoset-settings.exe`（不创建窗口、不在 TIP 进程调用） | diagnostics, winutil | ≤200 / ≤100 |
| `ui_host.cpp` `.hpp` | 单例 UI 宿主：UiTray、托盘菜单、runUiHost、命令管道线程、设置中心入口 | ui_client, settings_launcher, config, diagnostics | ≤1000 |
| `candidate_layout.hpp` | 候选窗纯布局数学（与 macOS 测试共用） | — | ≤200 |
| `ui_main.cpp` | 几十行 UI 宿主入口（LoadLibrary + ChaosetRunUi） | — | ≤120 |

### 3.3 macOS（`shims/macos/src/`）

| 文件 | 职责 | 预算 |
| :--- | :--- | :--- |
| `ChaosetController.swift` | IMK 输入控制器核心/生命周期 | ≤900 |
| `KeyRouter.swift` | 按键路由/修饰键/退格/翻页语义 | ≤700 |
| `CandidatePanel.swift` | 候选面板对外 controller/API 薄壳 | ≤400 |
| `CandidatePanelView.swift` | NSView 布局/绘制/鼠标命中 | ≤900 |
| `CandidatePanelWindow.swift` | NSPanel 生命周期/回调装配 | ≤400 |
| `CandidateTheme.swift` | 颜色/Layout 几何/齿轮参数 | ≤500 |
| `CandidateSettings.swift` | 联想开关热路径模型（其余设置由设置中心写同一 UserDefaults 域） | ≤200 |
| `EngineClient.swift` | 引擎 IPC 对外 API | ≤600 |
| `DaemonTransport.swift` | Unix socket 连接/帧读写/重连 | ≤500 |
| `SettingsLauncher.swift` | 设置入口：拉起 app 内 `Contents/Resources/ChaosetSettings.app/Contents/MacOS/chaoset-settings`（找不到/启动失败只弹错误，不回退旧窗） | ≤200 |
| `main.swift` | 入口 | ≤100 |

（若实际拆分与上表不同，以本表为准更新；`checkuiparity` 断言上表文件存在且单文件不超过上表预算。）

### 3.4 Linux 设置入口（`shims/linux/setup/`）

设置 UI 由 Slint 设置中心提供；这里只剩一个纯 POSIX 启动器（ibus component 的
`<setup>` 入口 `/usr/share/chaoset/chaoset-setup` 保持不变）：

| 文件 | 职责 | 预算 |
| :--- | :--- | :--- |
| `chaoset-setup.c` | 纯 POSIX 启动器：PATH → `/usr/bin/chaoset-settings` → 自身同目录，`execv`；找不到只报错（不链接 GTK、无回退 UI） | ≤200 |

### 3.5 Linux 原生引擎（`shims/linux/engine/`）

| 文件 | 职责 | 预算 |
| :--- | :--- | :--- |
| `main.cpp` | ibus engine 入口/按键处理/daemon IPC/懒启动 | ≤900 |
| `daemon.cpp` | daemon 连接与 XDG 路径解析 | ≤300 |

### 3.6 共用

| 文件 | 预算 |
| :--- | :--- |
| `shims/common/ipc/core.cpp` | ≤700 |
| `shims/common/ipc/core.hpp` | ≤300 |

### 3.7 跨平台设置中心（`shims/settings/`）

**唯一设置 UI**：基于 Slint 实现的跨平台设置中心，对齐 `docs/ui-parity.md` 视觉令牌，完全脱离 Chromium/Electron 庞大体积；三端壳只保留启动器。

| 文件 | 职责 | 预算 |
| :--- | :--- | :--- |
| `ui/tokens.slint` | 21 项视觉令牌定义（深色/浅色配色、卡片圆角、几何尺寸） | ≤100 |
| `ui/components.slint` | 自绘组件（ChaosSwitch 开关、ChoicePill 胶囊、ChaosButton、Card 卡片、候选窗实机微预览） | ≤400 |
| `ui/app.slint` | 主设置窗口：640×560pt，148pt 左导航列（输入、词库、关于），右侧卡片式分组布局 | ≤500 |
| `src/bridge.rs` | CLI 桥接：经同目录 `chaosetctl` 读写 `config`/`userdict`/`import`，并重启 daemon | ≤300 |
| `src/lib.rs` | Slint 模块接入、系统深浅色感知、模块导出与无头测试桩 | ≤150 |
| `src/events.rs` | 设置窗事件绑定（联想/模糊音/双拼/中英作用域/主题/学习数据/词库导入）与重启 daemon | ≤150 |
| `src/main.rs` | Slint 初始化（读设置/统计/版本）、单实例与事件循环 | ≤150 |
| `src/single_instance.rs` | 单实例：Unix 本地 socket 转发页号 / Windows 命名互斥体 + 前置已有窗口 | ≤250 |

## 4. 架构检查清单（checkuiparity）

- `checkShimModuleLayout`：上表每个 shims 文件必须存在；单文件行数不得超过预算；
  防止有人把拆开的代码搬回单文件（架构回退即 FAIL）。
- 「Rust CLI 发行面」组：构建/打包脚本只能从 `crates/chaosetctl` 产出 CLI（Makefile /
  macOS `build.sh` / Windows `package_msvc.sh` / Arch PKGBUILD 均 `cargo --bin chaosetctl`，
  禁止 `go build` 已迁移的 Go 命令）；发行面包按最终形态断言——Linux `bin/` 有
  `chaosetctl`（与 `bin/chaoset-settings` 同级）+ `chaoset`/`chaoset-cli`/`chaoset-import`/
  `chaoset-userdict` 四个软链，macOS app 内是 `Contents/Resources/chaosetctl` +
  `Contents/Resources/chaoset-import` 两份副本，Windows 包 `chaosetctl.exe`；安装/升级
  用 `chaosetctl import -seed -backup` 刷新词库；已删除的 Go 运行期实现（旧 CLI/daemon/
  导入/用户词典/设置/路径/构建信息包与 Go IPC 客户端）不得以任何路径回归。
- `crates/` 模块存在性与 §3.1 单文件预算目前由本文件登记 + 代码评审保证（checker 只机器
  断言 shims 模块与发行链）；新增 crate 文件先更新本表。
- 「设置核心（Rust 单一真源）」组：设置 schema/默认值/持久化唯一实现在
  `crates/chaoset-config`，daemon 与 CLI 共用；三端存储键与单行 JSON 契约由该组锁定。
- `checkArchitectureDoc`：本文件必须存在且包含「架构地图 / 文件预算 / 依赖方向 /
  Rust 单一真源」。
- 依赖方向（叶子模块不得反向 include TIP）目前由代码评审保证；`checkShimModuleLayout` 只校验
  模块存在性与单文件行数预算。
- 逻辑文件聚合：`winShell` / `macPanel` / `macSettings` / `linuxSetup` 在 checkuiparity 里
  从「单文件」改为「按目录聚合多文件读取」，既有逐条文案/几何/行为断言保持不变。
- 新增平台代码必须同步：本文件模块地图 + `checkuiparity` 模块清单 + 构建脚本源列表
  （Windows build.sh/Makefile 用 wildcard，无需手改清单）。

## 5. 验证

```
make vet                 # go vet + check-fmt + workflow + 版本 + GDI + checkuiparity（模块布局/零阻塞/三端契约/Rust 发行面）
make build               # Rust CLI + daemon + dictc（Go-free）
make build-tools         # 构建期 Go 工具：bin/chaoset-dictgen
sh shims/windows/build.sh
sh shims/linux/setup/build.sh /tmp/chaoset-setup
```
macOS Swift 由 CI `macos-build` 编译验证；本地装有 swiftc 时可直接 `swiftc -typecheck`（见 CI job 命令）。
