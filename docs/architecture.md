# 架构与模块边界

本文是 chaoset_ime 的架构地图与拆分契约。**新增/移动平台壳代码前先更新本文件与
`tools/checkuiparity` 的模块清单**；文件规模与模块存在性由机器断言锁死（见文末）。

> 2026-11-06 用户要求：按方案 A 把三端设置窗的设置逻辑/契约收敛到 Go；shims 里大文件
> 太多，必须按职责拆分组织，「不要埋下屎山的隐患」。

## 1. 分层与依赖方向

```
engine/           纯 Go 拼音/联想算法（禁止平台 API、CGO、build tag；purity_test.go 守卫）
ipc/              IPC 帧协议与传输抽象（unix socket / Windows 命名管道；无 TCP）
internal/         跨平台 Go 基础设施（appdirs、buildinfo、settings ← 方案 A 单一真源）
cmd/              可执行工具（chaosetd、chaoset-cli、chaoset-import、chaoset-userdict、
                  chaoset-dictgen、chaoset-settingsctl）
shims/common/     三端壳共用的 C++ IPC 客户端（core.hpp/core.cpp）
shims/windows|macos|linux/   平台壳：只做平台集成 + 渲染 + 系统交互
```

规则：

1. **单向依赖**：engine ← cmd/daemon ← ipc；shims 只允许通过 IPC 调 daemon，不 import Go 包。
2. **shims 内**：平台壳只做「平台集成 + 渲染 + 系统交互」；业务逻辑（设置 schema/持久化、
   学习数据、词库合并/备份/导入）归 Go。shim 不得把同一份业务逻辑再实现一遍。
3. **大小预算**：单文件超过预算即视为架构回退，`checkuiparity` 会 FAIL。拆分时按职责，
   不要按行数硬切；拆分文件必须登记到本文件的模块地图。
4. **禁止反向依赖**：Windows 叶子模块不得 include/引用 TIP/CandidateWindow 类型；
   由 `win32.cpp`（DLL 生命周期编排）与 `tip.hpp` / `tip_*.cpp`（TIP 主体）向下编排。
   macOS/Linux 同理：页面/视图不反向依赖应用控制器。

## 2. 方案 A：设置逻辑收敛到 Go（单一真源）

### 2.1 现状问题

三端设置窗各自实现：默认值、取值/写值、学习数据统计/清空/导出、词库导入流程、
中英状态作用域读写。三份实现容易漂移（此前已出现 mac/Linux 设置窗编译不过、
页脚版本号不一致等）。

### 2.2 目标结构

```
internal/settings/            设置 schema、默认值、校验、平台持久化（唯一真源）
  settings.go                 与平台无关：Settings、ModeScope、默认值、Validate、JSON 契约
  store_windows.go            HKCU\\Software\\Chaoset（Associate / ModeScope / UiEnglish）
  store_darwin.go             UserDefaults.standard（AssociateEnabled / ChaosetModeScope；域 = ime.chaoset.inputmethod.ChaosetIME）
  store_linux.go              $XDG_CONFIG_HOME/chaoset/config.json（associate / mode_scope）
cmd/chaoset-settingsctl/      供三端设置窗调用的命令行接口（JSON）
```

CLI 契约（供 shim 调用；**冷路径**使用，热路径仍读本机缓存/原生存储）：

| 命令 | 输出/行为 |
| :--- | :--- |
| `chaoset-settingsctl get` | 单行 JSON：`{"associate":true,"mode_scope":"system","ui_english":false}` |
| `chaoset-settingsctl set associate on\|off` | 写 Associate，成功退出 0 |
| `chaoset-settingsctl set mode-scope system\|process` | 写作用域（两档；旧值 window 也接受并归一化为 process），成功退出 0 |
| `chaoset-settingsctl set ui-english on\|off` | 写 UI 宿主持久化中英状态（内部用） |

退出码：0 成功；1 运行错误（存储不可用等）；2 参数非法。错误写 stderr，stdout 只放 JSON。

### 2.3 迁移阶段

| 阶段 | 内容 | 状态 |
| :--- | :--- | :--- |
| A1 | Go 设置核心 + CLI + 单测 | 已完成（checkuiparity「设置核心（方案 A）」组锁定） |
| A2 | 三端设置窗的「读取/写入设置项」改调 CLI（冷路径）；热路径缓存继续读原生存储 | Windows 已完成；macOS/Linux 未接线 |
| A3 | 学习数据（统计/清空/导出）与词库导入流程收敛到 Go（复用 chaoset-userdict / chaoset-import） | 已完成：三端词库导入均调 chaoset-import，学习数据经 daemon IPC 或 chaoset-userdict，壳只渲染 |
| A4 | 热路径配置读取收敛 | **明确不做**：热路径零阻塞契约禁止在 TSF/IMK/ibus 回调里起进程，三端继续用本机 TTL/进程内缓存 |

热路径不调用 CLI/进程：三端壳沿用本机缓存（Windows config TTL 缓存、macOS/Linux 进程内
缓存），CLI 只负责**写**与设置窗**读**，保证输入延迟不受影响。

## 3. 模块地图

### 3.1 Windows（`shims/windows/src/`）

| 文件 | 职责 | 依赖 | 预算 |
| :--- | :--- | :--- | :--- |
| `win32.cpp` | DLL 生命周期编排：ChaosetFactory、ChaosetRunUi 导出、DllMain/DllGetClassObject/DllCanUnloadNow、g_liveObjects 存活计数 | 全部 Windows 壳模块（含 tip.hpp） | ≤1200（当前 129；≤2000 目标已达成） |
| `tip.hpp` | TIP 主体 class ChaosetTIP 完整声明（成员/嵌套 ContextRef/继承接口与 TipThreadSink）、kVk*/kPageSize 常量、mingw 缺失的 ITfTextInputProcessorEx 补声明 | candidate_window, mode_toast, tip_thread, tip_ids, shell_worker, winutil, config, transport, daemon, diagnostics, core | ≤500 |
| `tip_core.cpp` | TIP 生命周期/激活状态机/中英模式与激活档案/前台钩子/worker 回投入口 | tip.hpp | ≤1100 |
| `tip_keys.cpp` | ITfKeyEventSink 五入口、Shift 状态机、修饰键/键位映射、wouldHandle/handleKey | tip.hpp | ≤1100 |
| `tip_composition.cpp` | EditSession + focusContext/can*/render/光标/预编辑/提交/结束合成/双拼方案与会话收尾 | tip.hpp, core | ≤900 |
| `tip_candidates.cpp` | 候选点选/删除/翻页、showToast、设置窗入口、联想面板与 CandidateWindowHost 转发 | tip.hpp | ≤500 |
| `candidate_window.cpp` `.hpp` | 候选窗 GDI 渲染/DPI/鼠标与右键菜单/UIA 宿主 provider/齿轮笔/防卸载计数；TIP 侧只经 CandidateWindowHost 抽象接口 | winutil, theme, config, diagnostics, candidate_layout, core | ≤1200 / ≤300 |
| `shell_worker.cpp` `.hpp` | 每进程后台 worker 任务队列、post* 投递、daemon 懒启动/连接、设置窗拉起、tsf.log 异步落盘 | winutil, core, transport, daemon, ui_client, diagnostics, tip_thread | ≤600 / ≤250 |
| `tip_thread.cpp` `.hpp` | kTip* 消息常量、worker→TIP 线程回投、message-only 窗口过程/创建销毁、按线程 TIP 注册表与前台钩子表；对 TIP 只经 TipThreadSink 抽象接口分发（不依赖具体 TIP 类） | winutil | ≤350 / ≤200 |
| `tip_registration.cpp` `.hpp` | COM/TSF 注册与反注册（DllRegisterServer/DllUnregisterServer/DllUninstallServer，冷路径） | tip_ids, winutil | ≤300 / ≤120 |
| `tip_ids.hpp` | CLSID/GUID/TSF 接口与类别常量（C++17 inline const GUID，namespace chaoset::win） | — | ≤150 |
| `mode_toast.cpp` `.hpp` | 模式切换提示条（窗口生命周期/绘制/防卸载计数） | winutil, theme, diagnostics | ≤400 / ≤150 |
| `diagnostics.cpp` `.hpp` | tsf.log/crash.log 诊断、VEH 崩溃取证 | — | ≤700 |
| `winutil.cpp` `.hpp` | 窗口类名、DPI、AppContainer 判定、当前用户 SID/会话、宽字符转换、浮窗 owner 判定 | — | ≤500 |
| `theme.cpp` `.hpp` | 候选窗配色/主题判定（与 macOS 视觉契约） | winutil | ≤500 |
| `config.cpp` `.hpp` | HKCU 设置读写、配置 TTL 缓存、全屏抑制 | winutil, diagnostics | ≤700 |
| `transport.cpp` `.hpp` | daemon 命名管道名/探测/PipeTransport | winutil | ≤500 |
| `daemon.cpp` `.hpp` | daemon 懒启动/连接/熔断原语（仅 worker 调用） | winutil, transport, diagnostics | ≤500 |
| `ui_client.cpp` `.hpp` | UI 宿主管道低层（命令/查询/拉起原语） | winutil, diagnostics | ≤700 |
| `settings_window.cpp` `.hpp` | 设置窗（布局/绘制/控件/导入入口） | config, transport, diagnostics, winutil | ≤1600 |
| `ui_host.cpp` `.hpp` | 单例 UI 宿主：UiTray、托盘菜单、runUiHost、命令管道线程 | ui_client, settings_window, config, diagnostics | ≤1000 |
| `candidate_layout.hpp` | 候选窗纯布局数学（与 macOS 测试共用） | — | ≤200 |
| `ui_main.cpp` | 几十行 UI 宿主入口（LoadLibrary + ChaosetRunUi） | — | ≤120 |

### 3.2 macOS（`shims/macos/src/`）

| 文件 | 职责 | 预算 |
| :--- | :--- | :--- |
| `ChaosetController.swift` | IMK 输入控制器核心/生命周期 | ≤900 |
| `KeyRouter.swift` | 按键路由/修饰键/退格/翻页语义 | ≤700 |
| `CandidatePanel.swift` | 候选面板对外 controller/API 薄壳 | ≤400 |
| `CandidatePanelView.swift` | NSView 布局/绘制/鼠标命中 | ≤900 |
| `CandidatePanelWindow.swift` | NSPanel 生命周期/回调装配 | ≤400 |
| `CandidateTheme.swift` | 颜色/Layout 几何/齿轮参数 | ≤500 |
| `CandidateSettings.swift` | Associate/ModeScope 设置模型 | ≤200 |
| `EngineClient.swift` | 引擎 IPC 对外 API | ≤600 |
| `DaemonTransport.swift` | Unix socket 连接/帧读写/重连 | ≤500 |
| `SettingsWindow.swift` | 设置窗控制器 | ≤600 |
| `SettingsWindowPages.swift` | 三页视图构建 | ≤700 |
| `SettingsWindowTargets.swift` | @objc target/action | ≤400 |
| `main.swift` | 入口 | ≤100 |

（若实际拆分与上表不同，以本表为准更新；`checkuiparity` 断言上表文件存在且单文件不超过上表预算。）

### 3.3 Linux（`shims/linux/setup/`）

| 文件 | 职责 | 预算 |
| :--- | :--- | :--- |
| `chaoset-setup.c` | GtkApplication/窗口骨架/导航/页脚/入口 | ≤400 |
| `setup_page_input.c` | 输入页（联想开关、中英状态） | ≤400 |
| `setup_page_dict.c` | 词库页（统计/清空/导出/导入入口） | ≤400 |
| `setup_page_about.c` | 关于页 | ≤300 |
| `setup_pages.h` | 页面构建函数声明 | ≤120 |
| `setup_ctl.c` | 路径/tool_path/config 读写/输入法进程生命周期核心 | ≤700 |
| `setup_internal.h` | 跨 TU 内部声明（result_*/find_tool/run_tool/路径/进程生命周期） | ≤120 |
| `setup_import.c` | 词库导入流程 | ≤500 |
| `setup_learn.c` | 学习数据统计/导出/清空流程 | ≤500 |
| `setup_ctl.h` | 对外 API | ≤120 |
| `test_setup_ctl.c` | setup_ctl 单测 | ≤200 |

### 3.4 Linux 原生引擎（`shims/linux/engine/`）

| 文件 | 职责 | 预算 |
| :--- | :--- | :--- |
| `main.cpp` | ibus engine 入口/按键处理/daemon IPC/懒启动 | ≤900 |
| `daemon.cpp` | daemon 连接与 XDG 路径解析 | ≤300 |

### 3.5 共用

| 文件 | 预算 |
| :--- | :--- |
| `shims/common/ipc/core.cpp` | ≤700 |
| `shims/common/ipc/core.hpp` | ≤300 |

## 4. 机器断言（checkuiparity）

- `checkShimModuleLayout`：上表每个文件必须存在；单文件行数不得超过预算；
  防止有人把拆开的代码搬回单文件（架构回退即 FAIL）。
- `checkArchitectureDoc`：本文件必须存在且包含「模块地图 / 文件预算 / 依赖方向 / 方案 A」。
- 依赖方向（叶子模块不得反向 include TIP）目前由代码评审保证；`checkShimModuleLayout` 只校验
  模块存在性与单文件行数预算。
- 逻辑文件聚合：`winShell` / `macPanel` / `macSettings` / `linuxSetup` 在 checkuiparity 里
  从「单文件」改为「按目录聚合多文件读取」，既有逐条文案/几何/行为断言保持不变。
- 新增平台代码必须同步：本文件模块地图 + `checkuiparity` 模块清单 + 构建脚本源列表
  （Windows build.sh/Makefile 用 wildcard，无需手改清单）。

## 5. 验证

```
make vet                 # 含 checkuiparity（模块布局/零阻塞/三端契约）
sh shims/windows/build.sh
sh shims/linux/setup/build.sh /tmp/chaoset-setup
make build               # 含 chaoset-settingsctl
```
macOS Swift 由 CI `macos-build` 编译验证；本地装有 swiftc 时可直接 `swiftc -typecheck`（见 CI job 命令）。
