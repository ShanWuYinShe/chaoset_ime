# Windows 平台壳（TSF）

形态：**TSF COM DLL 薄壳（C++）+ TCP 回环连 chaosetd**（同 macOS 壳架构：
引擎与候选逻辑全在 daemon，DLL 只做按键转发 + 预编辑合成 + 上屏）。

## 目录

```
src/core.hpp|cpp   可移植核心：协议帧（4 字节大端长度 + JSON）、极小 JSON 解析、
                   会话控制。零 OS 依赖——在 macOS 上对着真实 daemon 做行为测试。
src/win32.cpp      Windows 胶水：COM TIP（ITfTextInputProcessor + ITfKeyEventSink）、
                   预编辑合成（ITfContextComposition）、TCP 回环传输（winsock2）。
tests/core_test.cpp 核心层行为测试（原生编译，非交叉）。
build.sh           一键：核心行为测试（真实 daemon）+ mingw 交叉编译 + 导出检查。
```

## 验证状态（2026-10-31 更新）

- **真机验证（2026-10-30/31，Windows 11）**：开始菜单/搜索框候选窗恢复显示
  （owner 缺失 + UILess 类别误声明 + 安装器杀错进程三处修复，v0.2.17 实机
  确认）；记事本/浏览器/终端等常规宿主正常。游戏/全屏、UWP/AppContainer 细节、
  多会话（RDP/快切）、无障碍仍待验；per-monitor DPI 与设置窗深色已做编译级实现
  （2026-10-31），需真机验收（见「已知限制」）。
- **CI 构建（2026-10-31 新增）**：`ci.yml` 的 `windows-build` job 每次 push/PR
  都跑 mingw 交叉编译 + `core_test`（真实 daemon）+ `makensis` 编译安装脚本 +
  ZIP 内容自检；不再只在打 tag 的 release 流程里编译 C++/NSIS。
- **核心层：行为级验证通过**——`core_test` 用真实 chaosetd（TCP 回环，
  即 Windows 壳的实际传输形态）跑通 17 项：协议帧、JSON（转义/中文/截断）、
  组词/预编辑/选词上屏/歧义切分/退格/重置、业务错误后连接复用。
- **Win32 胶水层：双架构全面支持与原生候选窗**——同时编译出 64 位 `ChaosetIME.dll` 与 32 位 `ChaosetIME32.dll`，
  规范导出 `DllRegisterServer/DllUnregisterServer/DllUninstallServer/DllGetClassObject/DllCanUnloadNow`，
  内置原生双缓冲高 DPI 浮动候选窗（光标精准跟随、屏幕边缘防溢出、首选高亮与翻页指示器），
  注册环节全面支持现代 Windows TSF COM 接口（`ITfInputProcessorProfiles` 与 `ITfCategoryMgr`）及键盘类别/沉浸式/托盘支持，
  同一个安装包自动同时注册 64 位原生与 32 位 WOW64（如 Steam、游戏、32位办公软件），实现全环境无死角打字。
- **后台服务：静默 GUI 子系统运行**——`chaosetd.exe` 采用 Windows GUI 子系统构建，开机与运行 0 黑框、0 弹窗，后台完全隐形，日志直接落盘到 `%LOCALAPPDATA%\Chaoset\chaosetd.log`。

## 安装（Windows 真机）

### 方式一：一键安装程序（最推荐，适合所有普通用户）

从 GitHub Release 直接下载 **`ChaosetSetup.exe`**：

1. **直接双击 `ChaosetSetup.exe`**，系统弹出 UAC 提权提示时点击「是」；
2. 安装向导自动完成：
   - 释放核心文件到 `C:\Program Files\Chaoset`
   - 自动注册 64 位与 32 位 TSF COM 输入法组件（`regsvr32`）
   - 配置开机自动启动后台守护进程（写入注册表 `Run`，静默隐形运行，重启电脑永不失效）
   - 写入 Windows「已安装的应用 / 控制面板」标准卸载程序
   - 立即启动输入法后台服务（完全静默无控制台黑框）
3. 按 `Win+Space` 切换到 **Chaoset 拼音输入法** 即可开始打字。
4. **覆盖升级提示**：新打开的任何软件已自动加载新版本；若在安装前已经打开了软件（如记事本、聊天软件），只需将其【关闭并重新打开】即可体验新版！

> 卸载：直接在 Windows「设置 → 应用 → 安装的应用」中找到 **Chaoset 拼音输入法** 点击「卸载」，或在开始菜单中点击「卸载 Chaoset 输入法」。

### 方式二：便携 ZIP 压缩包（免安装）

从 GitHub Release 下载 `chaoset-windows-x64.zip`，解包后：

1. 右键 `install.bat` → 以管理员身份运行；
2. 卸载：右键管理员运行 `install.bat uninstall`。

> 覆盖升级（v0.2.17+）：`install.bat` 会先清理旧版残留的 UILess 类别并终止
> 加载了旧 `ChaosetIME*.dll` 的进程（按模块名枚举，含 SearchApp 等开始菜单
> 宿主），再注册新 DLL——与 NSIS 安装器的升级路径行为一致（2026-10-31）。

### 方式三：从源码

```sh
# macOS 上交叉编译（brew install mingw-w64）
./build.sh    # 产出 build/ChaosetIME.dll

# Windows 机器上（管理员）：
regsvr32 ChaosetIME.dll
chaosetd.exe -tcp 127.0.0.1:7949 -dict dict.tsv -userdict userdict.json -log chaosetd.log
# 系统设置 → 时间和语言 → 语言和区域 → 添加输入法 ChaosetIME
# 卸载：regsvr32 /u ChaosetIME.dll
```

> `make package-windows` 产出的是可直接分发的 zip，内含
> `dict.tsv`（14 万条常用词库）、`install.bat`、DLL 与三个 exe。

## 命令行工具

Windows 上 daemon 走 TCP 回环（127.0.0.1:7949），命令行工具必须用 `-tcp`
（Unix 套接字形态不存在）；`-version` 打印构建信息里的提交号/构建时间，
**真机排障第一步先跑它确认装的是新构建**（2026-10-31 新增）：

```bat
REM 确认当前安装/解包的二进制是不是最新提交
chaosetd.exe -version
chaoset-cli.exe -version

REM 终端测试台连 daemon（先确保 daemon 已启动）
chaoset-cli.exe -tcp 127.0.0.1:7949
```

> 随包携带的 `chaoset-userdict.exe` 同样支持 `-tcp 127.0.0.1:7949 stats|clear|export`
> （2026-10-31 起与 Linux 包对齐；此前只有源码构建才有该工具）。

| 功能特性 | 状态 |
| :--- | :--- |
| 预编辑经 ITfContextComposition 合成；上屏即合成文本替换 | 已实现（编译级） |
| 原生浮动候选词窗口（双缓冲 GDI、首选高亮、分页指示、**学习标记 `*`**、单页箭头置灰） | 已实现（编译级） |
| 候选窗布局单位（2026-10-30）：全部 pt 口径 `MulDiv(v,dpi,72)`，与 macOS 1:1（间距/键帽/圆角/最大宽此前是 px@96 的 0.75 倍；96/120/144/192 dpi 像素表见 win32.cpp `pt()` 注释） | 已实现（编译级） |
| 描边线宽（2026-10-30 补）：面板/键帽/分隔线/Toast 边框改为 pt 化 `max(1,MulDiv(1,dpi,72))`，与 mac 1pt 对齐（此前固定 1 设备像素，125%/150% 缩放下比 mac 细 33%–40%） | 已实现（编译级） |
| 鼠标点选候选（2026-10-30）：与数字键/空格同路径——先 selectPrefix 渐进上屏、失败才整句 select；此前直接 select，长串下点候选会清空整串拼音 | 已实现（编译级） |
| 主题切换重绘（2026-10-31）：`WM_SETTINGCHANGE`/`WM_THEMECHANGED` 立即重绘候选窗与提示条，不再保持旧配色直到下次输入 | 已实现（编译级） |
| 模式提示 Toast：光标行下方 6pt（与候选窗同锚点）+ WS_EX_TRANSPARENT 点击穿透（mac ignoresMouseEvents 对应） | 已实现（编译级，待真机确认观感） |
| 光标跟随与屏幕边缘碰撞反弹（GetTextExt -> GetGUIThreadInfo -> GetCursorPos） | 已实现（编译级） |
| 后台服务完全静默（GUI Subsystem，零黑框，零控制台窗口常驻） | 已实现（编译级） |
| `-` / `=` / `[` / `]` / PageUp / PageDown 候选翻页 | 已实现（核心行为级） |
| Shift 空按切换中英（英文模式全放行；已确认段先落盘）。2026-10-31 跟踪移到 TSF Test 阶段（OnTestKeyDown/OnTestKeyUp），「非 Shift 键取消空按窗口」同步移入；见「已知限制」 | 已实现（编译级，待真机确认） |
| Shift+空格切换全角/半角标点（**仅组词态可切**；联想态=选第一个联想词，不切全半角；GetKeyState 查 Shift 状态） | 已实现（编译级） |
| 已确认段（渐进上屏）收尾（2026-10-30）：Alt/Win 透传、Shift 切中英、空格无候选、翻页通信失败都先落盘/拼接确认段再收尾（与 mac 语义一致，此前会丢已确认的字） | 已实现（编译级） |
| 双拼 mspy `;` 键（2026-10-30）：`Comp.shuangpin` 下发方案名，双拼模式把 `;` 路由到 `input` 并上屏 `committed`（`m`+`;`=ming）；非双拼/Shift+`;` 仍是标点 | 已实现（编译级） |
| 模式切换提示（中英/全半角切换时光标旁短暂 toast，约 1.2s 自动消失） | 已实现（编译级） |
| 词组联想（选词后按上屏词音节查续接词组，复用候选窗展示；上限 30 条分页展示，数字连选/空格结束/字母退出/- = 翻页；associateSelect 学习路径与 select 一致） | 已实现（核心行为级） |
| 候选栏设置入口（折叠区左侧齿轮 → 独立设置窗口「Chaoset 设置」：输入行为分组 + 词组联想开关 + **学习数据（统计/清空/导出 TSV，2026-10-31）** + **词库导入（GUI，2026-10-31）** + 说明文字，与 macOS SettingsWindow 同构；存 HKCU\Software\Chaoset）。**关闭联想后已显示的联想候选在下一次按键/刷新时收起**（2026-10-30 改为不即时：设置窗不再持有 TIP 裸指针，消除宿主进程 UAF；mac 仍即时收起） | 已实现（编译级；窗口交互待真机确认） |
| 候选窗置顶层级：TOPMOST 保持——Windows 下拉菜单是系统模态 topmost popup（弹出时位于 Z 序最上并独占鼠标），候选窗 frame 重叠不会拦截菜单点击；与 macOS 端「popUpMenu 拦截菜单点击 → 降级 floating」的修复互为对照 | 已实现（编译级） |
| Shift 组合键语义（大写字母/Shift+数字字符不进组词；`?!:<>~"^()` 字符级标点映射） | 已实现（编译级） |
| 快捷键零 daemon 往返（2026-10-29）：全/半角开关等 UI 开关归壳本地持有，状态随 punct 请求下发，daemon 只按状态产出文本；修饰键组合在 sink 内微秒级放行 | 已实现（编译级） |
| 键盘只在需要打字时介入（2026-10-29）：未组词时先判相关性（字母/标点）再触碰连接，无关键（数字/导航/F键/修饰组合）微秒级放行；鼠标零特例，候选栏/toast 均为普通窗口行为 | 已实现（编译级） |
| 宿主上下文不支持 TSF 合成/插入时按键放行（开始菜单搜索框可正常唤起） | 已实现（编译级，待真机确认） |
| 开始菜单/搜索框拼音：AppContainer 宿主默认禁回环，安装器授予 CheckNetIsolation 回环豁免（StartMenuExperienceHost / Search），壳尝试连接、失败即时降级英文透传 | 已实现（编译级，待真机确认） |
| 候选窗锚点：合成范围 GetTextExt 拿到零矩形（Chromium 首帧未布局）或宿主无 GDI 光标时退回鼠标位置，不再借 hwndFocus 的恒零 rcCaret（此前候选窗飞到屏幕左上角） | 已实现（编译级，待真机确认） |
| UWP/AppContainer 场景、多会话（RDP/快切） | 待真机验证 |

## 排障日志（2026-10-29 起）

DLL 只在失败/异常路径落盘，正常打字不产生磁盘 I/O。

> 真机排障第一步（2026-10-31 教训）：`chaosetd.exe -version` / `chaoset-cli.exe
> -version` 打印提交号与构建时间，先确认装的是不是新构建，再看日志。

- `%LOCALAPPDATA%\Chaoset\tsf.log`：TIP 生命周期（宿主进程名、AppContainer 标志）
  与 TSF 调用失败（`RequestEditSession` / `StartComposition` / `SetText` 的 HRESULT；
  `no composition after render` 表示宿主上下文不接受合成——预编辑没建出或只读文档
  拒绝写入，按键已放行给宿主）。每个进程最多 200 条。
  2026-10-30/31 起新增真机排障行：`candidate shown ... visible=...`（候选窗最终
  落点/可见性）、`candidate window create FAILED`（创建失败错误码）、
  `candidate suppressed (fullscreen)`（全屏免打扰命中）、
  `candidate setOwner FAILED`（owner 设置被拒）、`activeflags ... uiless=0`
  （宿主是否仍以 UILess 模式运行，修复生效后应为 0）。
  Shift 跟踪一次性诊断（2026-10-31）：`modifier in OnTestKeyDown/OnTestKeyUp/
  OnKeyDown/OnKeyUp vk=...` 与 `shift toggle enMode=...`。真机按一次 Shift 后
  正常应看到 `OnTestKeyDown` + `OnTestKeyUp` + `shift toggle`（OnKeyDown/OnKeyUp
  可能不出现，这正是两阶段模型的表现）；若连 OnTestKeyUp 与 toggle 都没有，
  说明该宿主连 Test 阶段都不完整（见「已知限制」）。
- `%LOCALAPPDATA%\Chaoset\crash.log`：宿主进程未处理异常。记录异常码、异常地址、
  故障模块 + 偏移、访问类型（读/写/执行）、调用栈与崩溃前最后一次 TSF 操作。
  记录后返回 `EXCEPTION_CONTINUE_SEARCH`，WER 与宿主自己的崩溃处理不受影响。

反馈崩溃问题时附上这两个文件（如存在），可直接定位到具体模块与偏移。

## 踩坑记录：开始菜单候选窗不显示（2026-10-30）

**症状**：记事本/浏览器/终端里候选窗正常，Windows 11 开始菜单/搜索框里
完全看不到候选窗；预编辑（带下划线的拼音）显示正常。

**根因是两层，缺一不可**：

1. **候选窗 owner 为空**。`CreateWindowExW` 的 owner 参数一直是 `nullptr`，
   候选窗是无主顶层窗口，只靠 `WS_EX_TOPMOST` 维持可见性。普通桌面宿主够用，
   但开始菜单/搜索是**沉浸式 z 序 band** 窗口，无主 topmost 窗口会被压在整个
   band 之下。修法：用 `ITfContextView::GetWnd()` 取宿主 HWND 作 owner
   （`CandidateWindow::setOwner` / `hostWindowOf`），owned window 恒在 owner
   之上，可跨 band。
2. **安装器杀错进程名**。安装器硬编码 `ctfmon/TextInputHost/SearchHost/
   StartMenuExperienceHost` 四个名字，但实机承载开始菜单输入的宿主是
   **`SearchApp.exe`**（Windows 11 各版本叫法不同：`SearchUI.exe` →
   `SearchApp.exe` → `SearchHost.exe`）。漏杀的那个进程带着旧 DLL 一直活着，
   新版修复对它完全无效。修法：改为按模块名动态枚举——终止所有加载了
   `ChaosetIME*.dll` 的进程（排除 `explorer`/`dwm`），不再依赖进程名。

**为什么这个坑极难定位**（排障方法论，下次照此做）：

- 「预编辑正常但没候选窗」把范围收窄到候选窗自身——排除了 daemon、IPC、
  TSF 合成三类问题，因为预编辑就来自 daemon 返回的 `comp_.preedit`。
- **`tsf.log` 零新增是最强的误导信号**。它可能是「没激活」，也可能是
  「激活了但日志写不进」。AppContainer 进程的 `LOCALAPPDATA` 被系统重定向，
  原实现两条路径都失败就静默 `return`。现加了第三条兜底：`TEMP`
  （`\Packages\<Family>\LocalCache\Local\Temp\Chaoset\`）。
- **决定性证据来自三份数据的交叉对照**：① 进程列表显示 `SearchApp` 加载了
  DLL；② 日志零新增；③ `EnumWindows` 枚举发现 `firefox`/`WindowsTerminal`
  有候选窗但 `SearchApp` 连窗口对象都没有。三者结合才排除「被盖住」与
  「没渲染」，指向 owner 缺失。
- 教训：**先确认装的是不是新构建**。用户 `LastWriteTime` 停留在旧时间戳，
  前面几轮的修复其实从未被测到。`Get-Item <dll> | Select LastWriteTime`
  应作为真机测试的第一步。

**验证等级**：已通过 Windows 11 真机确认（2026-10-30，用户报告开始菜单
候选窗恢复显示）。

**2026-10-31 收尾（审计 A 批）**：owner 生命周期回收（owner 销毁时 owned
候选窗被系统连带销毁，`WM_NCDESTROY` 统一清句柄 + 归还防卸载计数，避免计数
永久泄漏导致 `DllCanUnloadNow` 恒 `S_FALSE`）、ModeToast 补 owner 与计数、
便携 ZIP 升级路径补旧 UILess 类别清理与按模块杀宿主、NSIS 改用 Sysnative
64 位 PowerShell 枚举 64 位模块、全屏免打扰、Shift 诊断日志。游戏/全屏独占
场景仍未真机验证，见「已知限制」。

## 图标与设置界面

- **应用与资源图标**：`resources/chaoset.ico` 提供包含 16×16、24×24、32×32、48×48、64×64、256×256
  六帧合一的 Windows 标准 ICO 资源，图标统一为极光电光蓝底板 + 纯白与霓虹青「Cs」组合标志 + 顶部拼音声调符号。
- **语言栏图标**：TSF 不会自动显示应用图标。需实现
  `ITfLangBarItemButton::GetIcon`（返回 `HICON`），并在 DLL 资源中提供
  16×16 与 32×32（高 DPI）两档图标；`ITfInputProcessorProfileMgr::RegisterProfile`
  的 `pszIconFile`/`iconIndex` 也要指向该资源，否则语言栏只有系统默认图标。
- **设置界面**：Windows 不会给第三方 TSF 输入法提供配置页，因此本壳自绘设置
  窗口。**当前唯一入口是候选栏右侧齿轮**（`CandidateWindow::onLButtonDown` →
  `ChaosetTIP::showSettingsWindow`）；语言栏菜单/托盘入口**尚未实现**
  （`ITfLangBarItem`/托盘菜单在全仓 grep 无命中，此前本文写的「从语言栏菜单/
  托盘入口打开」与代码不符，2026-10-30 修正）。设置项存
  `HKCU\\Software\\Chaoset`（`Associate` DWORD），每次进入联想前重读、即时生效。
- **设置窗口视觉**：380×390pt、13pt 标签 / 11pt 说明（与 macOS SettingsWindow
  对齐；2026-10-31 增加「学习数据」「词库」区后由 380×190 调高）。**跟随系统深浅**：
  DWM 深色标题栏（动态加载 dwmapi，属性 20/19）+ 自绘背景/文字/复选框/按钮，
  `WM_SETTINGCHANGE`/`WM_THEMECHANGED` 即时重绘；分组标题改为加粗 Static
  （与 mac 的 bold label 同构，避免 GroupBox 在深色下留下浅色边框）。
  **per-monitor DPI**：按设置窗所在显示器 DPI 布局，`WM_DPICHANGED` 重排。
  以上为编译级实现，**需真机验收**。
- **词库导入（2026-10-31，GUI）**：设置窗「词库」区 →「导入词库…」→ 选择
  Rime dict.yaml / CC-CEDICT txt / TSV（可多选）→ 调随包 `chaoset-import.exe`
  与 `%LOCALAPPDATA%\Chaoset\dict.tsv` 合并 → 备份旧词库
  （`dict.tsv.bak.<时间戳>`）→ 同卷 staging + `MoveFileEx` 原子替换 → 读安装器
  写入的 `ChaosetDaemon` Run 项命令行重启 daemon。导入器退出码非 0 / 无产物 /
  备份或替换失败都会中止并提示（绝不留下「旧词库已删、新词库没到位」的中间态）。
  仅作用于安装器管理的默认词库路径；自定义 `-dict` 的部署需手工替换。

## 已知限制

- **游戏/全屏场景（2026-10-30 用户提出，未真机测试）**：用户明确要求
  「游戏中一定不能干扰快捷键、鼠标，那会严重影响使用」。现状分析：
  - **不会干扰鼠标**：全仓无 `SetWindowsHookEx` / `ClipCursor` / `SetCapture` /
    `SendInput` / `mouse_event` / `keybd_event`（逐项 grep 均为 0）。鼠标事件
    只有候选窗自身的 `WM_LBUTTONDOWN`（窗口内点击），不拦截全局鼠标。
  - **不会干扰快捷键**：`wouldHandle` 对 `Alt`/`Win` 组合恒放行；`Ctrl` 组合
    仅在组词中认 `Ctrl+Backspace`/`Ctrl+U`，其余放行；数字/导航/F 键/空格在
    未组词时立即放行且不触碰 daemon 连接。
  - **但中文输入法激活时字母键会被拦**：未组词状态按字母（不带 Shift）会
    进入组词（`wouldHandle` 的 `relevant` 判定 → `handleKey` 字母分支）。
    游戏里 WASD 会变成打拼音——这是**任何中文输入法激活时的固有行为**，
    不是本壳缺陷。用户需在游戏前切英文模式（Shift 空按或 Win+Space）。
    注意「Shift 空按切换中英」本身待真机确认（见上一条限制）。
  - **全屏免打扰（2026-10-31 实现）**：`CandidateWindow::show` 前用
    `SHQueryUserNotificationState` 判定 `QUNS_RUNNING_D3D_FULL_SCREEN` /
    `QUNS_PRESENTATION_MODE` 时抑制候选窗显示（不改变按键/组词逻辑，避免
    置顶窗口盖在全屏游戏上闪屏/干扰焦点）。
    `HKCU\Software\Chaoset\FullscreenSuppress`（DWORD，缺省 1）可关闭——
    部分全屏浏览器/视频也会命中 D3D 全屏状态，此时设 0 恢复候选窗。
  - **边界**：无边框全屏（borderless fullscreen）返回
    `QUNS_ACCEPTS_NOTIFICATIONS`，**不会**触发抑制；独占全屏游戏里是否还有
    其它干扰仍需真机验证。
  - **游戏内 WASD 被组词**：中文输入法激活时字母键会进入组词，这是 IME 固有
    行为，不是本壳缺陷；游戏前请切英文模式（Shift 空按或 Win+Space，Shift
    切换本身待真机确认）。

- **候选窗 DPI（2026-10-31 已改，待真机验收）**：`layoutAndPosition()` 改用
  `dpiForPoint(targetPos_)`（动态加载 shcore!GetDpiForMonitor）按锚点所在显示器
  布局，`onPaint` 复用缓存的 `layoutDpi_`；`WM_DPICHANGED` 重新布局+重绘。
  Toast 同改（按提示位置取 DPI）。混合 DPI 多屏仍需真机验证。
- **候选窗为单行布局**（macOS 为 3 行折行）：首项超宽逐字截断加…，后续候选
  放不下时不排列（数字键仍可选）。折行需重排布局，待需求。
- **中英切换已改到 TSF Test 阶段（2026-10-31，待真机确认）**：Shift 空按跟踪
  原写在 `OnKeyDown`/`OnKeyUp`；按 TSF 两阶段模型，`OnTestKeyDown` 返回 `FALSE`
  的按键不会再调 `OnKeyDown`，修饰键可能永远进不了跟踪函数（中英切换失效）。
  现移到 `OnTestKeyDown`/`OnTestKeyUp`（Test 阶段对每个按键都会调用），
  「非 Shift 键取消空按窗口」也移入 Test 阶段——顺带修掉 Shift+方向键等
  未组词按键松 Shift 时误切换中英的问题；Ctrl/Alt/Win 的按下与释放同样
  取消空按窗口（2026-10-31 三端复核补齐，此前 Shift→Ctrl→松 Shift 会误切换）。
  诊断日志保留（见「排障日志」）：
  真机按一次 Shift，正常应看到 `modifier in OnTestKeyDown` +
  `modifier in OnTestKeyUp` + `shift toggle`；若仍只有 TestKeyDown 而无
  toggle，说明该宿主连 OnTestKeyUp 都不调（罕见），需改 GetKeyState 轮询。
  这条必须真机验证。
- **无障碍**：未实现 WM_GETOBJECT/UIA/MSAA（macOS 有 VoiceOver 子元素），
  待 Windows 真机阶段补。
- 传输用 TCP 回环（stdlib 可监听，协议不变）；命名管道（会话隔离更干净）
  留待 Windows 环境验证后切换。
- **TCP 回环没有 Unix socket 权限等价物（2026-10-31 记录）**：mac/Linux 的
  daemon 套接字已 `chmod 0600`（同机其它用户不可连）；Windows 用
  `127.0.0.1:7949`，本机任何用户进程都能连接 daemon 收发组词/上屏数据
  （Windows 没有 socket 文件权限模型）。后续方案：命名管道 + 安全描述符，
  需真机验证后再切换。
- 双拼 `;` 的标点宽度瞬态**已修复（2026-10-31）**：`input` 请求新增可选 `half`
  字段，壳每次按键下发全/半角状态，引擎先同步再处理——刚切全/半角后单按 `;`
  不再按旧宽度出标点。
- 中文标点按 US 布局**字符级映射**（含 Shift 变体：`,` `.` `;` `[` `]` `\` `'`
  与 Shift 产生的 `:` `<` `>` `?` `~` `!` `^` `(` `)` `"`，已对齐 macOS/ibus
  壳与 daemon 的字符集；2026-10-27 前 Shift 组合全部错映射、十个中文标点
  不可达，已修）。仅覆盖 US 键盘布局；其它布局（如德法 AZERTY 的 OEM 键位
  不同）待真机按需接 ToAscii。
- `GUID_TFCAT_CATEGORY_OF_TIP` 的值不在 mingw uuid 库中，注册分类暂只
  登记键盘类；注册完备性待真机确认（`regsvr32` 报错与否即可判断）。
