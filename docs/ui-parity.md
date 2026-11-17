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
- 翻页块（双箭头，issue#2）：多页时 blockH = 6pt 上箭头块 + 页码高 + 6pt 下箭头块，
  blockTop = (height - blockH) / 2（整块垂直居中）：首页只绘制/启用下箭头、末页只
  绘制/启用上箭头、中间页两枚都绘制/启用；单页没有翻页动作：两枚箭头置灰、
  不注册命中区（无页码，blockH = 2×6pt，两枚箭头以面板中线对称绘制）；
- 两枚箭头命中区上下分开（各占翻页块一半）：上箭头 = 翻上一页、下箭头 = 翻下一页，
  左键仍遵守「按下只记录、抬起仍在同一命中区才触发」的 mouseUp 契约；
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

主色（macOS CandidateTheme 为视觉参考实现）：

| 字段 | 浅色 | 深色 |
| :--- | :--- | :--- |
| panelBg | #F6F6F8（mac alpha F6） | #1A2740（mac alpha F5） |
| text | #1D1D1F | #F2F6FC |
| highlightBg | #007AFF | #0066EB |
| highlightText | #FFFFFF | #FFFFFF |
| chipBg | #FFFFFF | #223353 |
| chipDigit | #1D1D1F | #F2F6FC |
| userMark | #34C759 | #30D158 |

深色板 = **深海军蓝家族**（与设置窗深色视觉令牌同族，不再用中性灰）：
panelBg=设置窗 cardBg #1A2740、chipBg=controlBg #223353、text/chipDigit=textPrimary
#F2F6FC、page/chevron=textTertiary #7E90AC；Toast 深色同用 #1A2740 面板底 + 白 alpha
描边（描边与候选窗同款）。高亮蓝与学习标记绿为语义色，两版深浅都不变；浅色板不变。

macOS 使用带 alpha 的 NSColor 与底色实时混合；GDI 无逐字形 alpha，Windows
按 round(bg*(1-a)+fg*a) 预混合成不透明常量。常用派生有效值：

| 派生色 | 浅色 | 深色 |
| :--- | :--- | :--- |
| panelBorder | #DDDDDF | #3A455B |
| chipBorder | #C3C3C3 | #59667E |
| page/chevron | #A1A1A6 | #62738E |
| divider | #E2E2E4 | #354056 |

Windows 面板为不透明底 + 系统阴影/圆角；macOS 为半透明底 + 1pt 描边 + 系统阴影。

## Toast 配色（macOS ModeToastView / Windows mode_toast，两端同值）

| 字段 | light | dark |
| :--- | :--- | :--- |
| bg | #F6F6F8（mac alpha 0.95，Windows 不透明） | #1A2740（mac alpha 0.92，Windows 不透明） |
| 描边 | 黑 alpha 1A（有效 #DDDDDF，同候选窗浅色描边；Windows 直接用 #DDDDDF 画笔） | 白 alpha 24（有效 #3A455B，同候选窗深色描边） |
| text | #262628 | #F2F6FC |

- 底色/描边族与候选窗面板完全同族（浅=蓝白浅面板、深=深海军蓝面板）；圆角 10pt
  （Windows GDI 画直径 pt(10)*2；Win11 由 DWM 抗锯齿圆角呈现，见「已知功能差异·面板质感」）。
- 不用 calibratedWhite / white 灰阶（其 sRGB 有效值与本表不符，见两端源码注释）；
  改动本表必须两端同步。
- **机器断言**：mac 值经 `ModeToastView.palette` 由
  `shims/macos/tests/visual-parity.sh` F 段逐色断言（panel_metrics 导出
  toastLight/toastDark）；Windows 预混合常量由 `make check-uiparity`「Toast 配色」
  组以 mode_toast.cpp 的整行三元片段锁住（单色值与候选窗 theme.hpp 共享，
  无法唯一定位到 Toast）。任一端改配色不更新本表都会 FAIL。

## 学习标记与按词删除

- 学习标记：macOS/Windows 原生候选窗在候选文本后绘制 *，宽度
  markW = 字符宽 + pt(2)；ibus 面板没有独立装饰层，不追加 *（学习加权仍生效）。
- 学习数据按词删除：macOS/Windows 候选行右键 →「删除该词」，经 IPC userDelete
  删除本机学习记录，不上屏、不结束组词；Linux ibus 面板没有右键菜单回调，
  替代入口为 chaoset-userdict delete。
- 右键删除只删学习记录，不删内置词库词条；删除 Words/Phrases（含整句式
  Segs 记录），上下文 bigram 刻意保留。重复删除幂等，无记录时提示
  「未找到该词的学习记录」；**删除成功静默**（只刷新候选窗，不弹成功提示，用户反馈成功
  toast 多余），失败仍提示「删除失败：daemon 不可达」。
- **Windows 右键菜单不得激活候选窗自身（issue#1）**：候选窗是 `WS_EX_NOACTIVATE`，
  但 `SetForegroundWindow` 仍会**程序化激活**它（微软文档明确），激活会夺走宿主文档的
  键盘/TSF 焦点并终止合成（表现为打字变英文直通、Shift 失效、未上屏拼音留在文档）。
  菜单 owner 取 TIP 返回的**本进程宿主窗口**：本进程前台窗口优先；前台窗口不属于本进程
  时退到 TSF 焦点上下文宿主窗口/合成缓存窗口（pid 必须本进程）；取不到 owner 时
  **不弹菜单**（安全优先于可用性）。**不得对候选窗 `SetForegroundWindow`，也不得以
  候选窗为菜单 owner**。菜单返回后先重校验候选窗/TIP 状态与序号再执行删除（菜单期间
  TSF 回调可能已退场）。
- **Windows 删除后刷新的一致性**：若 TSF 已在菜单/焦点迁移期间外部终止当前合成
  （TSF 会把合成内文本留在文档，见 `ITfCompositionSink::OnCompositionTerminated`），
  壳不得再 flush/commit（会把已确认段重复写进文档）或重建合成；只清本地/引擎会话并
  收起候选窗。刷新后若预编辑未写入文档（`composition_` 为空或 SetText 失败），按
  `bailOutNoComposition` 语义收口，不留「候选窗显示但文档无预编辑」。
- **合成被外部终止时的保护**：壳只在「文档里的合成文本 == 最后一次写入的
  `confirmedPreedit_ + comp_.preedit`」时把合成文本改写成 `confirmedPreedit_`（保留
  用户已确认段、清掉未上屏拼音）；取不到/内容不一致/写失败一律只记 `tsf.log`，绝不强改。

## 已知功能差异

| 能力 | macOS | Windows | Linux | 说明 |
| :--- | :--- | :--- | :--- | :--- |
| 设置入口 | 候选栏齿轮（mouseUp 抬起才打开）→ AppKit 设置窗 | 候选栏齿轮（mouseUp 抬起才打开）/ 托盘图标 → 单例 UI 宿主里的 Win32 设置窗 | ibus-setup → GTK3 设置窗 | 三端设置窗单例；Windows 设置窗不属于 TIP 宿主进程（开始菜单/搜索等 AC 宿主里也能打开）；齿轮按下只记录、抬起且仍在齿轮区才触发 |
| 中英状态指示器 | 无（系统菜单栏图标由系统绘制） | `chaoset-ui.exe` 的唯一托盘「中/英」图标，显示**当前输入上下文的实际状态**；中英状态由输入法自己维护（TSF 只提供线程隔间与焦点/激活原语，不代管中英模式），TIP 经命名管道上报，不在光标/鼠标旁弹提示。**两档同步方向**：`system`（全局共享）下托盘是权威——TIP 获得焦点时从托盘同步，托盘左键切换全局中英；`process`（每进程独立）下托盘是镜像——TIP 激活/切进程时把本进程状态上报到托盘，托盘左键只显示、不切换 | 由 ibus/桌面环境指示 | 每用户会话只许一个状态指示器；托盘可见性由 TSF 信号事件驱动：只要系统当前选中的输入法是本输入法就显示（与输入焦点解耦），切到其它输入法才隐藏（见下文「非激活与托盘可见性」） |
| Windows UI 宿主 | — | `chaoset-ui.exe`（**不写登录 Run 项；TIP 首次激活带 --mode 懒启动；托盘可见性事件驱动（无周期定时器/心跳）：TSF 激活状态变化时 TIP 上报带中英值与 pid 的 `active 1`（显示并把该进程设为 owner）或 `active 0`（用户切到其它输入法；300ms 防抖后隐藏，失焦/前台离开/Deactivate 只退场不隐藏，owner 进程退出只清 owner 不隐藏），UI 宿主用 `OpenProcess`+`RegisterWaitForSingleObject` 监听 owner 退出并清理，隐藏保留 300ms 防抖；mode 只更新图标；启动 --mode 不显示**；`Local\ChaosetUi-<SID>` 单例）；托盘 + 设置窗唯一宿主，UI 实现仍在 `ChaosetIME.dll` | — | TSF 会把 TIP 加载进每个宿主进程，进程内自绘托盘会出现多个图标；升级/便携包必须清掉旧版 `ChaosetUi` Run 项 |
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
| 面板质感 | 半透明 sRGB 底 + 1pt 描边 + 系统阴影 | 不透明预混合色 + 1pt 描边 + CS_DROPSHADOW；**Win11 申请 DWM 抗锯齿圆角（DWMWCP_ROUND，自带系统阴影，不再套 region）**，Win10 申请失败保持 SetWindowRgn 圆角（region 轮廓有锯齿，属已知旧系统限制） | 候选窗与 Toast 同处理；Win11 圆角观感/阴影是否叠加 CS_DROPSHADOW 需真机验收；Windows 无毛玻璃 |
| 窗口层级 | .floating(3)，不遮蔽菜单栏 | WS_EX_TOPMOST + owner=宿主窗口 | owned 候选窗在宿主之上，可跨开始菜单/搜索的沉浸式 z 序 |
| 全屏免打扰 | 无 AppKit 对应机制 | D3D 全屏/演示模式抑制候选窗；FullscreenSuppress 注册表可关 | 只抑制显示，不改变按键/组词；无边框全屏不触发 |
| 游戏/UILess 宿主输入 | 无对应机制（宿主不经文本输入系统则任何 IMK 输入法收不到按键） | UILess 线程照常激活 + 候选默认 GDI 自绘（通用无名单）；锚点优先取宿主经 IMM32 兼容层写进 HIMC 的输入框位置，并用同值把系统组字窗挪到输入区；拿不到才退回 GDI 光标/鼠标；官方协商为显式开关 UiElementRender（缺省 0）；UILessActivate 注册表可关 | ibus 由面板/宿主呈现候选，无 UILess 概念 |
| 齿轮 hover | 无 | 无 | 刻意不做随输入变色 |
| 齿轮触发时机 | mouseDown 只记录命中，mouseUp 仍在齿轮区才打开设置窗 | 同左（gearPressed_） | 不适用（ibus 面板无齿轮） | 同一条 mouseup 契约也适用于候选词点选/翻页：按下只记录、抬起才执行，按下后拖走不触发（2026-11-06 用户要求） |
| 翻页箭头显示 | 双箭头：首页只有下箭头、末页只有上箭头、中间页都有；单页两枚置灰不可点 | 同左（同几何：6pt 箭头块 + 页码） | ibus 面板自绘，壳拿不到箭头绘制/命中回调，无法控制箭头显隐（按键语义一致） | Windows 像素效果需真机验收 |
| 设置窗 | 左导航三页（输入/词库/关于，148pt）+ 卡片 + 底部页脚版本号，640×560pt；窗口蓝白底 + 透明标题栏，自绘蓝色开关/软胶囊/按钮，选中项 accentSoft 胶囊 | 左导航 148pt + 卡片 + 底部页脚版本号 + 滚动，640×560pt；蓝白底 + 深海军蓝深色，DWM 标题栏跟随；深色/DPI 需真机验收 | 自绘胶囊导航（148pt）+ 三页卡片 + 底部页脚版本号，640×560pt；蓝白 GTK CSS 主题（深浅跟随系统 + 用户主题选择），与 macOS/Windows 同令牌同形态 |

## 非激活与托盘可见性（2026-11-07 用户要求）

- **非激活零动作**：TSF 回调只在 `inputActive_`（本线程激活档案是 Chaoset +
  TSF 焦点在本上下文）为真时处理按键；否则立即 `*pfEaten = FALSE` 放行，不查询、
  不清理、不挂/撤钩子、不读鼠标、不写盘、不起进程。切换输入法由 TSF 档案信号驱动
  （失活通知 / 激活档案 sink / Deactivate 档案复核），按键路径零查询、不做每键复核
  （2026-11-13 用户决策：用对的通知）。**例外（issue#3 托盘可见性）**：
  `OnSetFocus(TRUE)`/`OnSetThreadFocus`/`OnActivated(TRUE)`/前台回到本进程等 TSF
  焦点事件入口，在 `!inputActive_` 但本输入法被选中且本进程在前台时，允许按作用域
  同步一次中英模式并仅入队托盘显示上报（必要时启动每进程一次的 worker）；不得借此
  改按键闸门或做清理/钩子/键鼠读取。
- **修饰键永不消费、零捕获**：Windows 所有 KeyUp 恒返回 FALSE（含 Shift/Ctrl/Alt/Win）；
  macOS 非 Shift 修饰键（Cmd/Option/Ctrl/CapsLock/Fn）的 `flagsChanged`/`keyDown` 必须
  **入口即早退**原样放行——修饰键状态事件不得消费、不得进入组词/联想主流程、不得访问
  `charactersIgnoringModifiers`（AppKit 只允许 keyDown/keyUp 访问，flagsChanged 上会抛
  `NSInternalInconsistencyException`，事件处理中断会让远程桌面客户端收不到 Ctrl keyUp）、
  也不得触发 `passThrough` 的同步 daemon IPC/清组词；Ctrl 组合键（除组词中 Ctrl+U）
  一律透传宿主。RDP/远程桌面下 macOS 全局快捷键（如 Ctrl+↑ Mission Control）会在客户端
  之前吞掉 keyup，远端 Windows 显示修饰键卡住属系统/客户端按键通道问题，IME 不得注入
  按键去"修复"（应改 Mission Control 快捷键或改用 Cmd+Tab）。
- **托盘可见性（TSF 事件驱动，无周期定时器/心跳）**：托盘可见性＝系统当前选中的
  输入法是不是本输入法，与是否有输入焦点解耦。显示由 `Activate`、
  `ITfActiveLanguageProfileNotifySink` 激活通知、`ITfThreadFocusSink`/`OnSetFocus(TRUE)`
  焦点获得驱动（条件是本线程激活档案是本输入法且前台属于本进程，防 TSF 后台预激活误报），
  TIP 上报 `active 1 <0|1> <pid>`（显示托盘并把该进程设为 owner）；`OnSetFocus(FALSE)`、
  `OnKillThreadFocus`、前台离开本进程只退场（关按键/候选窗闸门、销毁浮窗、撤钩子），
  **不隐藏托盘**。切到其它输入法才上报 `active 0 <pid>`，UI 宿主 300ms 防抖后 `NIM_DELETE`
  （期间 `active 1` 取消）。**只要系统当前选中的输入法是本输入法就显示**，没有输入焦点的
  桌面/任务栏/无文本框窗口保持显示；**切到其它输入法才隐藏**。
  **隐藏事件源（2026-11-13 托盘常显收口，共三条）**：① `ITfActiveLanguageProfileNotifySink`
  失活通知（TIP 之间切换）；② `ITfInputProcessorProfileActivationSink`（键盘布局切换**只有
  它通知**——①不覆盖 `TF_PROFILETYPE_KEYBOARDLAYOUT`，此前「切到美式键盘」托盘永久残留；
  两个 sink 统一收口到 `switchToInactiveProfile`）；③ `Deactivate` 档案复核：宿主在切换输入法
  时也会调用 Deactivate，此时激活档案已换人、失活通知不会再发，按「`tm_ != nullptr &&
  !isActiveTip()` → 上报 active 0」补一次（档案仍是本输入法即宿主关闭/TSF 卸载，只退场，
  issue#3 关掉应用不代表切走）。边界：逐应用输入法
  （Windows 可选特性）下其它应用自己的选择不是切走信号（②都以「本进程是前台 + 档案复核
  已不是本输入法」为准）；宿主既不投递任何失活/档案事件也不把按键下沉给 TIP 时，TSF 侧没有
  可用的切走事件（事件驱动、无心跳）。UI 宿主不轮询，owner 进程退出用
  `OpenProcess`+`RegisterWaitForSingleObject` 只清 owner/句柄（不隐藏）；UI 宿主重启后靠
  下一次 TSF 激活/焦点事件重发恢复。`mode <0|1> <pid>` 只更新图标，启动 `--mode` 不显示。
- **中英状态归属与输入法切换链路**：中英模式由输入法自己维护（TSF 只提供线程隔间
  与焦点/激活原语，不代管中英模式）；用户切换输入法（切走/切回）由系统/TSF 触发，
  TIP 在 `Activate`/`Deactivate`、`ITfActiveLanguageProfileNotifySink` 与
  `ITfInputProcessorProfileActivationSink` 中响应，据此更新按键/候选窗激活态；
  托盘只在切走（失活/档案事件，见上）时隐藏。
- **自身浮窗不算前台信号（issue#1）**：Windows 前台钩子回调忽略本进程自身的候选窗/
  提示条窗口（按 `windowClassName` 生成的类名匹配）：它们成为前台是本 TIP 程序化激活
  自身浮窗的副作用，不是宿主焦点信号，不得据此修改 `focused_`/`inputActive_` 或重播
  激活上报。
- **焦点恢复（issue#1）**：`OnSetFocus(TRUE)`/`ITfThreadFocusSink::OnSetThreadFocus`/
  `ITfActiveLanguageProfileNotifySink::OnActivated(TRUE)` 是 TSF 明确告知「本服务接收
  键盘焦点」的事件；事件到达时 `ITfThreadMgr::IsThreadFocus` 可能尚未更新为真，壳以
  事件为准恢复按键/候选窗激活闸门（否则切回应用后可能一直英文直通、只能多点几次/
  点托盘才恢复）。该判定只影响 `inputActive_` 闸门，不改变托盘可见性语义（失焦仍不上报
  active 0）。
- **作用域同步方向**：`system`（全局共享）→ 托盘是权威（TIP 获得焦点时从托盘同步，
  托盘左键切换全局中英），状态在所有程序间共享；`process`（每进程独立）→ 托盘镜像
  当前进程状态（每个程序各自记住中英状态，同一程序的多个 TIP 实例共享；TIP 激活/
  切进程时把本进程状态上报到托盘，托盘左键只显示、不切换）。

## 设置窗契约（三端同构 · 蓝白）

三端设置窗共享骨架、视觉令牌、控件形态、分组标题与文案；配色为蓝白体系
（对齐候选窗 #007AFF/#0066EB 高亮蓝 + 近白面板），用户也可在「输入」页切换
跟随系统/浅色/深色。卡片容器三端都有
（macOS `SettingsCardView` / Windows 自绘圆角卡片 / Linux `.card` CSS），
由 tools/checkuiparity 的「设置窗视觉令牌」组逐值断言。

- 尺寸与骨架：客户区 640×560pt；左侧导航列宽 148pt（导航标题 13pt semibold、
  胶囊 112×34 左右内边距 12），三项「输入 / 词库 / 关于」，选中项 accentSoft 软胶囊 +
  accentText 半粗字（不再用大面积实心 accent 底）；
  内容区左边 = 导航列右侧 +24pt、右边 24pt、顶部 44pt（macOS 透明标题栏下避开
  红绿灯）；页脚固定 34pt 高，**所有页**左下显示「Chaoset 版本 x.y.z」。
  macOS/Windows 窗口固定 640×560pt（macOS 透明标题栏、Windows DWM 着色标题栏）；
  Linux 客户区同样固定 640×560、非 resizable，标题栏由 GTK 自绘 headerbar（跟随窗口
  底色，避免 GNOME/Wayland 的系统灰标题栏与另两端割裂；标题栏高度在客户区之外）。
  字号按平台口径：macOS/Windows 用 pt，Linux 用 px——GTK 的 pt 按 96dpi 换算比 mac 的
  pt 约大 33%，用 px 才能三端视觉字号一致（CJK 说明文字的换行位置也才对得上）。
- 视觉令牌（**单一事实源**；三端实现逐值一致，浅色/深色两套 sRGB）：

  | 令牌 | light | dark | 用途 |
  | --- | --- | --- | --- |
  | windowBg | #F4F7FB | #101A2C | 窗口底色（蓝白） |
  | navBg | #EAF1F9 | #0C1626 | 左侧导航列 |
  | cardBg | #FFFFFF | #1A2740 | 卡片底 |
  | cardBorder | #D9E4F0 | #2D3E5C | 卡片/控件描边 |
  | divider | #E4EDF6 | #243552 | 行分隔线 |
  | textPrimary | #14213D | #F2F6FC | 主文字（深海军蓝/白） |
  | textSecondary | #4A5A70 | #AFBFD6 | 次文字（说明/卡片标题/页脚） |
  | textTertiary | #7A8899 | #7E90AC | 仅装饰/弱提示 |
  | accent | #007AFF | #0066EB | 开关开启轨道/焦点环（与候选窗高亮蓝同值） |
  | accentStrong | #0066EB | #007AFF | 主按钮底 |
  | accentPressed | #0052BE | #4DA3FF | 主按钮按下/hover |
  | accentSoft | #E5F1FF | #16345C | 选中/悬停软底 |
  | accentText | #0066EB | #7FB4FF | 软底上的强调文字 |
  | controlBg | #F0F5FB | #223353 | 次按钮/未选胶囊/开关轨道关 |
  | controlHover | #E3EDF8 | #2C4165 | hover 底 |
  | controlBorder | #C9D9EB | #3A5074 | 控件描边 |
  | controlText | #14213D | #F2F6FC | 控件文字 |
  | knob | #FFFFFF | #F2F6FC | 开关滑钮 |
  | danger | #D32F2F | #FF7B72 | 危险操作文字 |
  | onAccent | #FFFFFF | #FFFFFF | accentStrong 上的文字 |
  | shadow | #1B4A80 | #000000 | 卡片阴影（浅 12% / 深 38% 透明度；按平台绘制能力落地） |

- 几何：卡片圆角 14pt、内边距 18pt、卡片间距 16pt；卡片标题 13pt semibold
  textSecondary，标题带 y+16..38pt，内容从 y+50pt 起（标题与控件不重叠）；
  行高 36pt；开关轨道 38×22pt（滑钮 16pt）；choice 胶囊高 30pt、圆角 9pt；
  按钮高 32pt、圆角 9pt；说明 11pt textSecondary；页脚 11pt textSecondary。
  Linux 用 px 落地且要按「含描边的总高」换算：GTK 的 `min-height` 是内容盒，
  choice 写 28px、按钮写 30px 才得到含 1px 描边的 30/32px（与另两端等高）；
  控件圆角取 12px（Windows 的 9pt 在 96dpi 就是 12px，比 mac 的 9pt 略圆，
  抵消 GTK 1x 抗锯齿更硬造成的「更方」观感），导航项与品牌标同为 12px。
  文字与底色对比：正文 ≥4.5:1、大字号 ≥3:1；不引入纯黑纯白。
- 控件形态（三端同构，绘制机制按平台落地）：
  - 开关：标签左/开关右；开 = accent 轨道 + knob，关 = controlBg 轨道 + 1pt
    controlBorder；hover 关 controlHover、开 accentPressed；
  - 中英状态二选一：两个软胶囊（选中 accentSoft + 1pt accent 描边 + accentText
    半粗，未选 controlBg + controlBorder + textPrimary，hover controlHover）；
  - 按钮：主按钮 accentStrong 实底 + onAccent 文字（导入词库…），次按钮
    controlBg + controlBorder + controlText（清空/导出；清空文字 danger）；
  - 导航：选中 accentSoft 胶囊 + accentText；未选中透明底 + textPrimary，hover
    controlHover 胶囊；focus 环统一 accent，不用系统蓝与系统虚线；
  - 卡片：卡片底 + 1pt cardBorder + 柔和阴影，行/说明垂直排布（行间 8、行与
    说明 10）。
- 关于页：应用图标 44pt + 「Chaoset 输入法」+ 平台副标题 + 许可证
  「代码 MIT · 词库 CC BY-SA 4.0」+「更多设置将在后续版本提供」+ 仓库地址
  （accentText）；**版本号只在页脚**，关于卡片不放（避免重复）。
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
    「更多设置将在后续版本提供」+ 仓库链接。
- 入口：候选栏齿轮（macOS/Windows，mouseUp 抬起才打开）或 Windows 托盘图标；
  Linux 为 ibus-setup「首选项」/ 系统菜单里的设置入口。
- 禁止色：0A84FF / F5EFE6 / FFFCF7 / C9703F / A85427 / E8DCCC / F6E6D9 / D16B29 / 994014
  不得出现在设置窗源码（旧系统蓝与暖陶色与蓝白设计冲突；蓝白令牌 #007AFF/#0066EB
  是允许且期望的强调色）。


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
- **配置缓存**：ModeScope / Associate / 深色主题 / 全屏抑制 / UILess 激活开关
  均带 TTL 缓存，候选窗每键不再做注册表/Shell 查询。
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
| 数字 1-9（含小键盘） | 选本页候选（按序号直选，不移动页内预选）；越界收起联想/放行原键 |
| 空格 | 组词态确认**页内预选项**（默认首选）并渐进上屏；有已确认段时拼接确认段+原始拼音上屏；联想态确认预选联想词 |
| 方向键 上/下/左/右 | 组词/联想有候选时：**上/下 = 翻页**（上一页/下一页；翻页后预选归零，边界页保持不动且仍消费）；**左/右 = 页内预选移动**（前一项/后一项；页边界保持不动且仍消费）；空格确认预选项；无组词/无候选时放行宿主（绝不消费）；预选在新输入/翻页/选词后归零 |
| 回车 | 确认段按文本 + 剩余按原始拼音一次上屏 |
| - = [ ] PageUp/PageDown | 候选/联想翻页；无候选时 [ ] 输出【】；候选窗上/下箭头点击等效翻页（首页只有下箭头、末页只有上箭头、中间页两枚、单页置灰不可点） |
| Shift 空按 | 切中英；已确认段先落盘；释放事件放行给宿主。记账必须是**幂等**的（Windows 用 `shiftHeld_` 位掩码，Linux 用 `shiftDown` 计数但 focusIn/focusOut 复位），任一端丢 KeyUp 都不得永久失效。**纯事件驱动，不得引入常驻定时器轮询**；宿主的焦点抖动（浏览器在地址栏/页面元素间搬焦点同样触发 `OnSetFocus(FALSE)`）只能**挂起**按住状态、不得直接复位，否则表现为「能打字但 Shift 切不了中英」；丢 KeyUp 时在**下一次按键**上自愈收口 |
| Shift+空格 | 组词态切全/半角标点；联想态选词不切 |
| 双拼 ; | mspy 下作为 ing 韵母键；其他模式/Shift+; 输出标点 |
| 标点键 | 组词中先上屏首选，再输出中文标点；组词中的撇号是音节分隔符 |
| 退格/Delete | 逐字符；删空剩余拼音立即落盘已确认段 |
| Esc | 取消整条组词（含未落盘确认段） |
| 收尾语义分层 | **继续打字**（切中英/透传/退格删空/Esc/顶屏/候选越界）走 `resetComposition`，只丢组词、**保留** prev/history；**会话结束**（失焦/停用/focusOut/activateServer）走 `reset`，连学习上下文一起清——清掉 prev 会让下一轮候选丢失 bigram 加成（三端壳与 daemon 都必须遵守） |

## 机器检查

- shims/macos/tests/visual-parity.sh：macOS 布局 5 项 + 参考常量（含 divider）+
  浅/深各 13 色；故意改字号/色值会 FAIL。E 段（源码级，issue#2）：折叠区双箭头
  可用性/命中区分开/预选高亮跟随。
- shims/macos/tests/equivalence.sh：图标 keycap 契约、universal 架构、TIS 图标键。
- tools/checkuiparity：三端设置窗文案/单例、设置窗契约（640×560pt、页脚版本号
  所有页、三端卡片标识、关于页正文）、**设置窗视觉令牌（21 个令牌逐值三端比对 +
  几何令牌 + 禁止色）**、学习数据 GUI、词库导入安全、齿轮 mouseUp
  （按下不得打开设置）、托盘生命周期（不写登录 Run 项、--mode 懒启动、首条模式
  上报后才显示图标）、**TSF 回调零跨进程等待（15 个回调函数体禁用语 + AsyncLog +
  熔断 + 钩子只 PostMessage + DllCanUnloadNow 计数）**、联想即时性、退格/修饰键
  语义、候选窗几何/配色字段、候选点选/右键删除、候选窗翻页箭头（双箭头可用性/
  命中区/单页处理）、候选窗方向键预选（消费与放行、空格确认预选项、三端路由）、
  单例 UI 宿主与模式指示器
  （不许回退为 TIP 自绘托盘/toast）、打包安装、CI 覆盖、shell 转义与文档对照。
- Windows 候选窗常量由上述静态工具校验；像素级渲染仍需真机观察，不能把
  编译级验证写成“双端已验证”。

## 性能参考

macOS/Windows 候选数据都来自 daemon 的纯数据 IPC；daemon 单键处理基准在
engine/scale_bench_test.go，用 go test ./engine -bench . 复跑，不以固定数字作为
硬指标。壳侧回归跑 macOS visual-parity.sh、Windows core_test 与 release 冒烟。

## 渐进上屏的跨程序兼容模型

三端壳对「逐段渐进拼句」（`jintianxingqiyi` 选「今天」→ 剩余继续组词）采用
**同一模型：已确认段并入预编辑/合成，整条组词结束才一次落盘**：

- macOS：已确认段并入 marked text——壳对文档只做两类操作：「`setMarkedText`
  更新」（与逐键输入同原语）与「组词结束 `insertText` 终态落盘」（与整句
  上屏同原语），不再在 marked 区激活期间插入正文；
- Windows：已确认段并入**当前合成**（同一条合成内 `SetText`），不再
  「`EndComposition` → 立刻 `StartComposition`」；落盘 = 同一合成
  `SetText` + `EndComposition` 一次完成；
- Linux：已确认段并入 `UpdatePreeditText`，不再在预编辑激活期间发
  `CommitText`；落盘 = 整串一次 `CommitText`。

为什么：逐段选择若「先写正文、再刷新预编辑」，会对宿主产生**两步文档变更**，
部分程序处理不好——刚选的字被丢掉、候选清空、剩余拼音卡在文档里（Notes/
Electron/终端对 marked 区激活期间的 insertText 有缺陷；Windows 部分宿主对
合成结束/重建的选区记账有缺陷；Linux XIM 桥接/自绘输入同理）。逐键输入只有
一步预编辑更新，因此把渐进上屏也收进同一条合成，兼容面与逐键输入一致，
并与 macOS 系统拼音/微软拼音「合成内已转换段」的行为对齐。

随之统一的行为（三端一致，矩阵见本文各节）：已确认段在预编辑里
以**已转换形态**（汉字而非拼音）随剩余拼音一起显示；Esc / 全部删除取消整条
组词（含未落盘的已确认段）；回车 = 已确认段按确认文本 + 剩余按原始拼音
上屏；退格只回退剩余拼音，剩余删空时已确认段立即落盘收尾；失焦 / 透传键 /
切换输入法时已确认段先落盘（用户确认过的字不丢）。引擎侧不变：
`SelectPrefix` 依旧只消耗候选音节对应的前缀、剩余回灌、即时学习（纯算法，
与平台无关）。