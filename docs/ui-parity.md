# 候选窗与交互三端一致性

本文件记录 macOS / Windows / Linux 三端的当前视觉与交互契约。权威实现：

- 基准期望值（布局常量 + 浅/深各 13 色）：内联在 shims/macos/tests/visual-parity.sh；
- macOS 渲染：shims/macos/src/CandidatePanelView.swift（视觉/布局/鼠标），
  shims/macos/src/CandidatePanel.swift 为对外 controller，CandidateTheme.swift 为色值/几何；
- Windows 渲染：shims/windows/src/candidate_window.cpp 与 shims/windows/src/candidate_layout.hpp；
- Linux：ibus 原生 LookupTable/Preedit，不自绘候选窗；
- 机器校验：tools/checkuiparity、shims/macos/tests/visual-parity.sh、
  shims/macos/tests/equivalence.sh。

## 单位

Windows 全部布局常量使用 pt 口径：

    int pt(int v, UINT dpi) { return MulDiv(v, dpi, 72); }

字号、内边距、键帽、圆角、最大宽、描边线宽均按 pt 换算，禁止退回 px@96。
macOS 使用 AppKit 的 pt；Linux 面板由 ibus 上游负责，不参与此表。

Windows 常用 pt 值在各 DPI 下的设备像素（四舍五入）：

| 参数 | 96dpi | 120dpi | 144dpi | 192dpi |
| :--- | ---: | ---: | ---: | ---: |
| padding 7 | 9 | 12 | 14 | 19 |
| firstPadLeft 9 | 12 | 15 | 18 | 24 |
| itemPadX 7 | 9 | 12 | 14 | 19 |
| gap 11 | 15 | 18 | 22 | 29 |
| chipPadX/Y 4/2 | 5/3 | 7/3 | 8/4 | 11/5 |
| pillMargin 2 | 3 | 3 | 4 | 5 |
| chevronBoxW 36 | 48 | 60 | 72 | 96 |
| gearBoxW 26 | 35 | 43 | 52 | 69 |
| panelRadius 8（直径 16） | 21 | 27 | 32 | 43 |
| textH 21 | 28 | 35 | 42 | 56 |
| maxWidth 720 | 960 | 1200 | 1440 | 1920 |
| minWidth 84 | 112 | 140 | 168 | 224 |
| caretGap 6 | 8 | 10 | 12 | 16 |

窗宽另有显示器工作区钳制，面板不会溢出屏幕。

## 排版参数（macOS Layout 与 Windows pt）

| 项 | macOS | Windows |
| :--- | :--- | :--- |
| 正文字号 | 15pt | pt(15) |
| 序号字号 | 12pt | pt(12) |
| 面板内边距 | 7pt | pt(7) |
| 首项左内边距 | 9pt | pt(9) |
| 候选内边距 | 7pt | pt(7) |
| 候选间距 | 11pt | pt(11) |
| 行高 | textH=21pt + lineSpacing=4pt | pt(21) + pt(4) |
| 光标下间距 | 6pt | pt(6) |
| 键帽 | 序号胶囊，圆角 3pt | pt(3) 圆角 |
| 折叠区 | chevron 36pt | pt(36) |
| 齿轮区 | 26pt | pt(26) |
| 内容最大宽 | 638pt | pt(638) |
| 窗宽上限 | 720pt | 内容 + pad×2 + 折叠/齿轮区，≤720pt |
| 最小窗宽 | 84pt | pad*2 + chevronBoxW + gearBoxW + pt(8) |
| 最大行数 | 3 行折行 | 3 行折行（同一纯布局函数） |
| 首项超长 | 截断加「…」 | 截断加「…」 |
| 描边线宽 | 1pt | max(1, pt(1)) |

布局函数在 candidate_layout.hpp 中只做纯数学计算，macOS 单测与 Windows 渲染共用
同一套换行/宽度规则；修改常量必须同步 macOS 测试、Windows 静态契约与本表。

## 折叠区几何

- 齿轮区：gearX = width - chevronBoxW - gearBoxW；分隔线 dividerX = gearX + 2；
- 翻页/页码区：flipZoneX = dividerX + gearBoxW，chevX = flipZoneX + (width - flipZoneX) / 2；
- 内容块：blockH = 8 + (多页 ? 3 + 页码高 : 0)，blockTop = (height - blockH) / 2；
- chevron 与齿轮同高，垂直居中；chevX = blockTop + 8 + 3，水平居中；
- 分隔线高 = textH * 55%，垂直居中；
- 齿轮 = 圆环 + 8 齿矢量算法，macOS/Windows 同几何参数、同色值；Linux 无齿轮；
- 左键动作（候选上屏 / 翻页 / 打开设置）统一在**鼠标抬起**（mouseUp/WM_LBUTTONUP）
  触发：按下（mouseDown/WM_LBUTTONDOWN）只记录命中目标，抬起点仍在同一目标上才
  执行；按下后拖走再松开不触发（2026-11-06 用户要求：鼠标抬起触发才符合常规
  按钮语义，按下即上屏/即弹窗属误触）；
- 面板圆角、键帽圆角、Toast 圆角按 pt 统一。

## 色值

- 浅色/深色两套静态 sRGB 色，按系统 appearance 选择；不在绘制时临时混色；
- 字段集合：background、text、secondaryText、selectionFill、selectionText、
  keycapFill、keycapText、divider、border、gear、gearHover、mark、shadow；
- Windows 静态契约要求字段名集合与 macOS 一致，具体色值以 visual-parity.sh 内联期望值为准。

主色（skin.DefaultSkinFor）：

| 字段 | 浅色 | 深色 |
| :--- | :--- | :--- |
| panelBg | #F6F6F8（mac alpha F6） | #2C2C2E（mac alpha F5） |
| text | #1D1D1F | #FFFFFF |
| highlightBg | #007AFF | #0066EB |
| highlightText | #FFFFFF | #FFFFFF |
| chipBg | #FFFFFF | #5A5A5E |
| chipDigit | #1D1D1F | #FFFFFF |
| userMark | #34C759 | #30D158 |

macOS 使用带 alpha 的 NSColor 与底色实时混合；GDI 无逐字形 alpha，Windows
按 round(bg*(1-a)+fg*a) 预混合成不透明常量。常用派生有效值：

| 派生色 | 浅色 | 深色 |
| :--- | :--- | :--- |
| panelBorder | #DDDDDF | #4A4A4C |
| chipBorder | #C3C3C3 | #838386 |
| page/chevron | #A1A1A6 | #7A7A80 |
| divider | #E2E2E4 | #454547 |

Windows 面板为不透明底 + 系统阴影/圆角；macOS 为半透明底 + 1pt 描边 + 系统阴影。

## 学习标记与按词删除

- 学习标记：macOS/Windows 原生候选窗在候选文本后绘制 *，宽度
  markW = 字符宽 + pt(2)；ibus 面板没有独立装饰层，不追加 *（学习加权仍生效）。
- 学习数据按词删除：macOS/Windows 候选行右键 →「删除该词」，经 IPC userDelete
  删除本机学习记录，不上屏、不结束组词；Linux ibus 面板没有右键菜单回调，
  替代入口为 chaoset-userdict delete。
- 右键删除只删学习记录，不删内置词库词条；删除 Words/Phrases（含整句式
  Segs 记录），上下文 bigram 刻意保留。重复删除幂等，无记录时提示
  「未找到该词的学习记录」。

## 已知功能差异

| 能力 | macOS | Windows | Linux | 说明 |
| :--- | :--- | :--- | :--- | :--- |
| 设置入口 | 候选栏齿轮（mouseUp 抬起才打开）→ AppKit 设置窗 | 候选栏齿轮（mouseUp 抬起才打开）/ 托盘图标 → 单例 UI 宿主里的 Win32 设置窗 | ibus-setup → GTK3 设置窗 | 三端设置窗单例；Windows 设置窗不属于 TIP 宿主进程（开始菜单/搜索等 AC 宿主里也能打开）；齿轮按下只记录、抬起且仍在齿轮区才触发 |
| 中英状态指示器 | 无（系统菜单栏图标由系统绘制） | `chaoset-ui.exe` 的唯一托盘「中/英」图标，显示**当前输入上下文的实际状态**；中英状态由输入法自己维护（TSF 只提供线程隔间与焦点/激活原语，不代管中英模式），TIP 经命名管道上报，不在光标/鼠标旁弹提示。**两档同步方向**：`system`（全局共享）下托盘是权威——TIP 获得焦点时从托盘同步，托盘左键切换全局中英；`process`（每进程独立）下托盘是镜像——TIP 激活/切进程时把本进程状态上报到托盘，托盘左键只显示、不切换 | 由 ibus/桌面环境指示 | 每用户会话只许一个状态指示器；激活/失活与托盘可见性由 TSF 信号事件驱动、无输入焦点即隐藏（见下文「非激活与托盘可见性」） |
| Windows UI 宿主 | — | `chaoset-ui.exe`（**不写登录 Run 项；TIP 首次激活带 --mode 懒启动；托盘可见性事件驱动（无周期定时器/心跳）：TSF 激活状态变化时 TIP 上报带中英值与 pid 的 `active 1`（显示并把该进程设为 owner）或 `active 0`（仅 owner 生效；失焦/切换输入法/前台离开），UI 宿主用 `OpenProcess`+`RegisterWaitForSingleObject` 监听 owner 退出并清理，隐藏保留 300ms 防抖；mode 只更新图标；启动 --mode 不显示**；`Local\ChaosetUi-<SID>` 单例）；托盘 + 设置窗唯一宿主，UI 实现仍在 `ChaosetIME.dll` | — | TSF 会把 TIP 加载进每个宿主进程，进程内自绘托盘会出现多个图标；升级/便携包必须清掉旧版 `ChaosetUi` Run 项 |
| 关闭联想即时性 | 设置窗通知，已显示联想立即收起 | HWND_BROADCAST → 各宿主候选窗收起 | 下一次按键生效 | Linux 为平台机制差异 |
| 学习数据管理 | 设置窗统计/清空/导出 | 同左 | 同左（调用 chaoset-userdict 本地模式） | 数据都在 daemon |
| 中英状态作用域 | UserDefaults ChaosetModeScope | HKCU Software\Chaoset\ModeScope | 配置文件 mode_scope | 三端统一两档：`system`（全局共享，默认）/ `process`（每进程独立）；旧三档值读取时归一化（window→process，未知→system），新写入只写 system/process |
| 词库导入 | Setup.app：选择文件 → chaoset-import -o -seed -backup → 重启 daemon | 设置窗同样调用 chaoset-import → 重启 daemon | 设置窗调用 chaoset-import → 重启 ibus | 合并在 Go 工具中完成 |
| 鼠标点选候选 | 候选窗点击（按下记录、抬起上屏） | 同左（onLButtonDown 记录 / onLButtonUp 上屏） | CandidateClicked（ibus 面板决定点击语义） | 与数字键同语义；左键动作统一 mouseup（2026-11-06 用户要求） |
| 滚轮翻页 | 未实现 | 不可行：候选窗不获得焦点，需全局鼠标钩子 | 不适用 | 三端不做，避免干扰游戏鼠标 |
| 无障碍 | VoiceOver 候选文字/窗口级 | UIA 窗口级 provider | 由 ibus/桌面读屏决定 | Windows 候选子项未实现 |
| Windows per-monitor DPI | 系统自动 | GetDpiForMonitor 按锚点/窗口 DPI，处理 WM_DPICHANGED | 由桌面环境决定 | 混合 DPI 多屏需真机验收 |
| 双拼 mspy ; | 支持 | 支持 | 支持 | 引擎下发 shuangpin，壳路由到 input |
| 学习标记 * | 显示 | 显示 | 不适用 | ibus 无独立装饰层 |
| 面板质感 | 半透明 sRGB 底 + 1pt 描边 + 系统阴影 | 不透明预混合色 + 1pt 描边 + CS_DROPSHADOW + SetWindowRgn 圆角 | GDI 限制；Windows 无毛玻璃、圆角有锯齿 |
| 窗口层级 | .floating(3)，不遮蔽菜单栏 | WS_EX_TOPMOST + owner=宿主窗口 | owned 候选窗在宿主之上，可跨开始菜单/搜索的沉浸式 z 序 |
| 全屏免打扰 | 无 AppKit 对应机制 | D3D 全屏/演示模式抑制候选窗；FullscreenSuppress 注册表可关 | 只抑制显示，不改变按键/组词；无边框全屏不触发 |
| 齿轮 hover | 无 | 无 | 刻意不做随输入变色 |
| 齿轮触发时机 | mouseDown 只记录命中，mouseUp 仍在齿轮区才打开设置窗 | 同左（gearPressed_） | 不适用（ibus 面板无齿轮） | 同一条 mouseup 契约也适用于候选词点选/翻页：按下只记录、抬起才执行，按下后拖走不触发（2026-11-06 用户要求） |
| 设置窗 | 左导航三页（输入/词库/关于，132pt）+ 内容页 + 底部固定页脚版本号，640×560pt；原生分组（无卡片容器），选中项 accent 胶囊 | 左导航 132pt + 右侧圆角卡片 + 底部页脚版本号 + 滚动，640×560pt；深色/DPI 需真机验收 | GtkStackSidebar + 三页 + 底部固定页脚版本号，640×560pt；原生分组（无卡片容器） |

## 非激活与托盘可见性（2026-11-07 用户要求）

- **非激活零动作**：TSF 回调只在 `inputActive_`（本线程激活档案是 Chaoset +
  TSF 焦点在本上下文）为真时处理按键；否则立即 `*pfEaten = FALSE` 放行，不查询、
  不清理、不挂/撤钩子、不读鼠标、不写盘、不起进程。切换输入法后即使宿主不投递
  Deactivate，也会在按键路径复核激活档案兜底。
- **修饰键永不消费**：所有 KeyUp 恒返回 FALSE（含 Shift/Ctrl/Alt/Win），避免 OS
  出现「修饰键卡住」；Ctrl 组合键（除组词中 Ctrl+U）一律透传宿主。RDP/macOS 本地
  快捷键吞掉 Ctrl keyup 属于远端输入通道问题，IME 不得注入按键去"修复"。
- **托盘可见性（TSF 事件驱动，无周期定时器/心跳）**：激活/失活完全由
  `Activate`/`Deactivate`、`ITfThreadFocusSink`、`ITfActiveLanguageProfileNotifySink`
  等 TSF 信号驱动；TIP 在激活状态变化时发 `active 1 <0|1> <pid>`（显示托盘并把该进程
  设为 owner）或 `active 0 <pid>`（仅当 pid 是当前 owner 时隐藏）。UI 宿主不轮询，
  用 `OpenProcess`+`RegisterWaitForSingleObject` 监听 owner 进程退出并清理；隐藏保留
  300ms 防抖（同一进程多 TIP 实例焦点抖动时不闪图标）；UI 宿主重启后靠下一次 TSF 事件
  重发恢复。**没有输入焦点（桌面/任务栏）或切到其它输入法即隐藏**；输入法未激活时隐藏，
  切换回来自动重新出现。`mode <0|1> <pid>` 只更新图标，启动 `--mode` 不显示。
- **中英状态归属与输入法切换链路**：中英模式由输入法自己维护（TSF 只提供线程隔间
  与焦点/激活原语，不代管中英模式）；用户切换输入法（切走/切回）由系统/TSF 触发，
  TIP 在 `Activate`/`Deactivate` 与 `ITfActiveLanguageProfileNotifySink` 中响应，
  据此更新激活态与托盘可见性。
- **作用域同步方向**：`system`（全局共享）→ 托盘是权威（TIP 获得焦点时从托盘同步，
  托盘左键切换全局中英），状态在所有程序间共享；`process`（每进程独立）→ 托盘镜像
  当前进程状态（每个程序各自记住中英状态，同一程序的多个 TIP 实例共享；TIP 激活/
  切进程时把本进程状态上报到托盘，托盘左键只显示、不切换）。

## 设置窗契约（2026-11-06 阶段2）

三端设置窗共享骨架、分组标题与文案；**卡片容器是 Windows 的视觉实现**，macOS/Linux
用原生分组（AppKit/GTK 不做像素级卡片），但分组标题/顺序/组内行序/文案必须一致，
由 tools/checkuiparity 静态断言。

- 尺寸与骨架：客户区 640×560pt；左侧导航宽 132pt，三项「输入 / 词库 / 关于」，
  选中项 accent（RGB 0A84FF）胶囊高亮；右侧内容区左右各 20pt 边距；页脚固定
  34pt 高，**所有页**显示「Chaoset 版本 x.y.z」。
- 卡片外观（仅 Windows）：圆角 10pt，浅/深底色 #FAFAFC / #2A2A2E，卡片标题
  13pt bold secondary；标题带 y+12..34pt，内容从 y+42pt 起（标题与控件不重叠，
  控件背景按所在卡片颜色绘制）。macOS/Linux 必须满足：分组标题文案与顺序一致、
  组内行序一致、无标题被控件遮挡。
- 滚动：仅 Windows 实现（640×560pt 下三页都放得下，代码留作扩展）；macOS/Linux
  不要求滚动。
- 分组与文案（三端统一）：
  - 输入页卡 1「输入行为」：行「词组联想」开关；说明「选词上屏后展示可续接的
    词组候选；关闭后不再弹出联想候选。」
  - 输入页卡 2「中英状态」：二选一「全局共享 / 每进程独立」；说明「切一次
    所有程序都生效（推荐）；每进程则各自独立。」
  - 词库页卡 1「学习数据」：统计行 + 清空/导出按钮 + 说明「学习数据仅保存在
    本机，用于调频与词组联想；清空后不可恢复。」
  - 词库页卡 2「词库」：导入按钮 + 统一说明「支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。」
    （macOS 导入机制不同，统一句后补「会打开『Chaoset 安装与设置』完成导入」括注。）
  - 关于页卡「关于」：正文含「跨平台中文输入法」「代码 MIT · 词库 CC BY-SA 4.0」
    「更多设置将在后续版本提供」+ 仓库链接；**版本号只在页脚**，关于卡片不放
    （避免重复）。
- 入口：候选栏齿轮（macOS/Windows，mouseUp 抬起才打开）或 Windows 托盘图标；
  Linux 为 ibus-setup「首选项」/ 系统菜单里的设置入口。

## 零阻塞契约（Windows，2026-11-06）

用户要求：「输入法在任何情况下都不应阻塞任何进程或 app」。TSF 把 TIP 加载进每个
宿主进程，**所有 TSF 回调都在宿主 UI 线程上同步执行**，因此回调体内禁止任何跨进程
等待/建进程/同步写盘。该契约由 `tools/checkuiparity` 的「TSF 回调零跨进程等待」组
逐函数机器断言（含变异测试），禁止回退。

- **禁止项（TSF 回调体内）**：`Sleep(`、`WaitNamedPipeW`、
  `WaitForSingleObject`、`CreateProcessW`、`CreateFileW`、`WriteFile(`、
  `MessageBoxW`、`GetOpenFileNameW`、`RegSetValueExW`、慢连接
  `ensureDaemonConnected(`。覆盖函数：Activate / ActivateEx / OnSetFocus /
  OnSetThreadFocus / OnKillThreadFocus / OnActivated / OnTestKeyDown / OnKeyDown /
  OnTestKeyUp / OnKeyUp / render / handleKey / showSettingsWindow /
  onForegroundChanged / selectCandidateByIndex / deleteCandidateByIndex /
  flipPage / CandidateWindow::show。
- **副作用统一入口**：每进程一个 `ShellWorker` 后台线程（`ShellTask` 任务队列）
  + message-only 窗口（`HWND_MESSAGE`，`kTipForegroundMsg`/`kTipModeAuthorityMsg`/
  `kTipShowToastMsg` 回投结果）；建进程、管道重试、
  派发 UI、daemon 代拉起全部在 worker 执行。
- **异步日志**：`diagPrintf` 对 `tsf.log` 只做 `enqueueLogLine`（有界 ring，
  worker 批量写盘；worker 未启动直接丢弃，**绝不回退同步写**）；按键路径不写盘
  （`candidate shown` 整条删除），前台风暴日志限流 5s。
- **管道 I/O**：`FILE_FLAG_OVERLAPPED` + 明确截止时间（TSF 路径 30ms、
  worker/设置窗 200ms，`setDeadlineMs`）+ 5s 熔断（`circuitOpen`）。
- **fail-open**：daemon 未就绪时 `ensureDaemonFast` 单次非阻塞探测，失败即原样
  放行并后台连接（`postDaemonConnect`）；按键不丢、不阻塞，下一键恢复候选。
  首 1–2 个内容键可能按英文直入（已确认可接受）。
- **配置缓存**：ModeScope / Associate / 深色主题 / 全屏抑制均带 TTL 缓存，
  候选窗每键不再做注册表/Shell 查询。
- **前台钩子**：每线程最多一个系统级钩子，回调体内只 `PostMessage`（不 hide、
  不写盘）；真正的清理在 TIP 线程做，前台离开本进程自动撤销；按键/下一次
  Activate 时按需自愈重挂。
- **生命周期安全**：worker 线程与 message-only 窗口计入 `DllCanUnloadNow`
  （`shellWorkerAlive()`、`g_liveTipMsgWindows`），未退干净时返回 `S_FALSE`；
  `DllMain(DETACH)` 只置停止标志不 join。

## 按键与交互

### 修饰键映射

| 语义 | macOS Command | Windows Win | Linux Super |
| :--- | :--- | :--- | :--- |
| 主修饰 | Command | Win | Super |
| 控制 | Control | Ctrl | Ctrl |
| 备选 | Option | Alt | Alt |

组词中 Backspace/Delete 的映射必须一致：普通退格逐字符、Option/Alt+
Backspace/Delete 按音节回退、Shift/Ctrl/Super+Backspace/Delete 全部删除；
修饰键其余组合先收尾已确认段再透传。修饰键映射表是机器检查契约的一部分。

### 按键矩阵

| 操作 | 语义 |
| :--- | :--- |
| 字母/撇号 | 组词；双拼按两两成对解码 |
| 数字 1-9（含小键盘） | 选本页候选；越界收起联想/放行原键 |
| 空格 | 组词态选首选并渐进上屏；有已确认段时拼接确认段+原始拼音上屏；联想态选第一个联想词 |
| 回车 | 确认段按文本 + 剩余按原始拼音一次上屏 |
| - = [ ] PageUp/PageDown | 候选/联想翻页；无候选时 [ ] 输出【】 |
| Shift 空按 | 切中英；已确认段先落盘；释放事件放行给宿主。记账必须是**幂等**的（Windows 用 `shiftHeld_` 位掩码，Linux 用 `shiftDown` 计数但 focusIn/focusOut 复位），任一端丢 KeyUp 都不得永久失效。**纯事件驱动，不得引入常驻定时器轮询**；宿主的焦点抖动（浏览器在地址栏/页面元素间搬焦点同样触发 `OnSetFocus(FALSE)`）只能**挂起**按住状态、不得直接复位，否则表现为「能打字但 Shift 切不了中英」；丢 KeyUp 时在**下一次按键**上自愈收口 |
| Shift+空格 | 组词态切全/半角标点；联想态选词不切 |
| 双拼 ; | mspy 下作为 ing 韵母键；其他模式/Shift+; 输出标点 |
| 标点键 | 组词中先上屏首选，再输出中文标点；组词中的撇号是音节分隔符 |
| 退格/Delete | 逐字符；删空剩余拼音立即落盘已确认段 |
| Esc | 取消整条组词（含未落盘确认段） |
| 收尾语义分层 | **继续打字**（切中英/透传/退格删空/Esc/顶屏/候选越界）走 `resetComposition`，只丢组词、**保留** prev/history；**会话结束**（失焦/停用/focusOut/activateServer）走 `reset`，连学习上下文一起清——清掉 prev 会让下一轮候选丢失 bigram 加成（三端壳与 daemon 都必须遵守） |

## 机器检查

- shims/macos/tests/visual-parity.sh：macOS 布局 5 项 + 参考常量（含 divider）+
  浅/深各 13 色；故意改字号/色值会 FAIL。
- shims/macos/tests/equivalence.sh：图标 keycap 契约、universal 架构、TIS 图标键。
- tools/checkuiparity：三端设置窗文案/单例、设置窗契约（640×560pt、页脚版本号
  所有页、卡片契约标识、关于页正文）、学习数据 GUI、词库导入安全、齿轮 mouseUp
  （按下不得打开设置）、托盘生命周期（不写登录 Run 项、--mode 懒启动、首条模式
  上报后才显示图标）、**TSF 回调零跨进程等待（15 个回调函数体禁用语 + AsyncLog +
  熔断 + 钩子只 PostMessage + DllCanUnloadNow 计数）**、联想即时性、退格/修饰键
  语义、候选窗几何/配色字段、候选点选/右键删除、单例 UI 宿主与模式指示器
  （不许回退为 TIP 自绘托盘/toast）、打包安装、CI 覆盖、shell 转义与文档对照。
- Windows 候选窗常量由上述静态工具校验；像素级渲染仍需真机观察，不能把
  编译级验证写成“双端已验证”。

## 性能参考

macOS/Windows 候选数据都来自 daemon 的纯数据 IPC；daemon 单键处理基准在
engine/scale_bench_test.go，用 go test ./engine -bench . 复跑，不以固定数字作为
硬指标。壳侧回归跑 macOS visual-parity.sh、Windows core_test 与 release 冒烟。
