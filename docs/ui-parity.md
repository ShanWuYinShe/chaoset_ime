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
- Linux：ibus 面板没有独立 Toast 层（模式反馈由 ibus/桌面指示器承担），本表不适用。
- **机器断言**：mac 值经 `ModeToastView.palette` 由
  `shims/macos/tests/visual-parity.sh` F 段逐色断言（panel_metrics 导出
  toastLight/toastDark）；Windows 预混合常量由 `make check-uiparity`「Toast 配色」
  组以 mode_toast.cpp 的整行三元片段锁住（单色值与候选窗 theme.hpp 共享，
  无法唯一定位到 Toast）。任一端改配色不更新本表都会 FAIL。

## 托盘键帽配色（Windows UiTray；macOS 不适用）

任务栏「中/英」自绘键帽（键帽立体造型：底层边框阴影 + 主体 + 顶边高光 + 粗体字）：

| 模式 | 底 | 边框阴影 | 顶边高光 | 文字 |
| :--- | :--- | :--- | :--- | :--- |
| 中文 | `#007AFF` | `#005ABE` | `#5AAAFF` | `#FFFFFF` |
| 英文 | `#F6F6F8` | `#DDDDDF` | `#FFFFFF` | `#262628` |

- 中文底色与候选窗浅色高亮蓝/设置窗 accent 同值；英文模式并入蓝白浮窗浅色族
  （底/描边/文字与候选窗、Toast 浅色面板同值）。此前英文模式是暖陶配色残留
  （`#F6F3EE` 底 / `#C8C3B9` 边 / `#1E1B18` 字）。
- 顶边高光是键帽立体造型语义（与 macOS 输入源图标同理念的 keycap），随键帽
  底色自然选取，不进令牌表。
- 托盘不随系统深浅色切换变色（中/英两态即配色语义）。
- macOS 不适用：中英指示由系统菜单栏图标（系统绘制 mode 图标）承担，无自绘托盘。
- 机器断言：`make check-uiparity`「托盘配色」组以 ui_host.cpp 的整行三元片段
  锁住底/边框/文字三值（高光为造型色不锁）。改配色不更新本表都会 FAIL。

## 学习标记与按词删除

- 学习标记：macOS/Windows 原生候选窗在候选文本后绘制 *，宽度
  markW = 字符宽 + pt(2)；ibus 面板没有独立装饰层，不追加 *（学习加权仍生效）。
- 学习数据按词删除：macOS/Windows 候选行右键 →「删除该词」，经 IPC userDelete
  删除本机学习记录，不上屏、不结束组词；Linux ibus 面板没有右键菜单回调，
  替代入口为 `chaosetctl userdict delete`（安装包内软链 `chaoset-userdict` 等价）。
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
| 设置入口 | 候选栏齿轮（mouseUp 抬起才打开）→ 拉起 Slint 统一设置中心 | 候选栏齿轮 / 托盘图标 → 单例 UI 宿主 → 拉起 Slint 统一设置中心 | ibus-setup「首选项」/ 系统菜单 → chaoset-setup 启动器 → 拉起 Slint 统一设置中心 | **设置 UI 只有 Slint 一处实现**（`shims/settings`，产物 `chaoset-settings`）；三端壳只保留启动器，不实现原生设置窗，也没有回退旧窗口；齿轮按下只记录、抬起且仍在齿轮区才触发 |
| 中英状态指示器 | 无（系统菜单栏图标由系统绘制） | `chaoset-ui.exe` 的唯一托盘「中/英」图标，显示**当前输入上下文的实际状态**；中英状态由输入法自己维护（TSF 只提供线程隔间与焦点/激活原语，不代管中英模式），TIP 经命名管道上报，不在光标/鼠标旁弹提示。**两档同步方向**：`system`（全局共享）下托盘是权威——TIP 获得焦点时从托盘同步，托盘左键切换全局中英；`process`（每进程独立）下托盘是镜像——TIP 激活/切进程时把本进程状态上报到托盘，托盘左键只显示、不切换 | 由 ibus/桌面环境指示 | 每用户会话只许一个状态指示器；托盘可见性由 TSF 信号事件驱动：只要系统当前选中的输入法是本输入法就显示（与输入焦点解耦），切到其它输入法才隐藏（见下文「非激活与托盘可见性」） |
| Windows UI 宿主 | — | `chaoset-ui.exe`（**不写登录 Run 项；TIP 首次激活带 --mode 懒启动；托盘可见性事件驱动（无周期定时器/心跳）：TSF 激活状态变化时 TIP 上报带中英值与 pid 的 `active 1`（显示并把该进程设为 owner）或 `active 0`（用户切到其它输入法；300ms 防抖后隐藏，失焦/前台离开/Deactivate 只退场不隐藏，owner 进程退出只清 owner 不隐藏），UI 宿主用 `OpenProcess`+`RegisterWaitForSingleObject` 监听 owner 退出并清理，隐藏保留 300ms 防抖；mode 只更新图标；启动 --mode 不显示**；`Local\ChaosetUi-<SID>` 单例）；托盘 + 设置窗唯一宿主，UI 实现仍在 `ChaosetIME.dll` | — | TSF 会把 TIP 加载进每个宿主进程，进程内自绘托盘会出现多个图标；升级/便携包必须清掉旧版 `ChaosetUi` Run 项 |
| 关闭联想即时性 | 设置中心写入后，候选窗**下一次显示**时读取并收起联想 | 同左；候选窗自身保留 `Chaoset.AssociateChanged` 处理，齿轮快捷切换等进程内路径仍然即时 | 同左 | 设置中心是独立进程，不再有跨进程通知链（旧原生设置窗时代遗留）；文档口径为「下一次显示生效」 |
| 学习数据管理 | 统一设置中心：统计/清空（二次确认）/导出，经同目录 `chaosetctl userdict` | 同左 | 同左 | 数据都在 daemon；设置中心不直接读写用户词典文件 |
| 中英状态作用域 | UserDefaults ChaosetModeScope | HKCU Software\Chaoset\ModeScope | 配置文件 mode_scope | 三端统一两档：`system`（全局共享，默认）/ `process`（每进程独立）；旧三档值读取时归一化（window→process，未知→system），新写入只写 system/process |
| 词库导入 | 统一设置中心：选择文件 → `chaosetctl import -o … -backup` → 自动重启 daemon | 同左 | 同左 | 合并/备份/原子替换唯一实现是 Rust `crates/chaoset-tools`（CLI 入口 `chaosetctl import`，macOS app 内副本 `chaoset-import` 是同一 Rust 二进制）；mac 安装器 Setup.app 仍保留导入面板 |
| 鼠标点选候选 | 候选窗点击（按下记录、抬起上屏） | 同左（onLButtonDown 记录 / onLButtonUp 上屏） | CandidateClicked（ibus 面板决定点击语义） | 与数字键同语义；左键动作统一 mouseup（2026-11-06 用户要求） |
| 候选窗空白区点击 | 收起候选窗，不静默吞掉点击；命中候选/齿轮/翻页时不收起 | 同左（onLButtonUp 空白区 hide；翻页命中分支 return） | ibus 面板自绘，不可控 | 2026-10-29 鼠标零干扰契约；2026-11-06 改 mouseup 时漏回，现恢复；空白区 hide 必须在所有命中分支之后，翻页后保持候选窗 |
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
| 设置中心 | Slint 统一设置中心：640×560、148pt 导航、三页卡片、页脚版本号；深浅主题跟随系统或用户选择；唯一 UI 实现 | 同左（同一份 Slint UI） | 同左（同一份 Slint UI） | 三端不再各自绘制；入口见「设置入口」行 |

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

## 候选窗没有特权：焦点离开即收（2026-11-23 用户要求）

候选窗只是一个普通弹出窗口，不得在他处截走鼠标。用户切到别处（Cmd+Tab / Dock /
点击别的应用、切换 Space、面板失去 key）时它必须消失，候选与输入一并清掉；三端
语义一致：**已确认段先落盘、剩余拼音丢弃、联想退出、面板与提示收起**。

- **macOS（`ChaosetController` 焦点守卫）**：IMK 只在客户端文本焦点变化时回调
  `commitComposition` / `deactivateServer`——应用切换、Space 切换与联想态（没有
  marked text）都不会触发它们，旧实现候选窗因此留在新应用上面（`canJoinAllSpaces`
  浮在所有 Space）。现在进程级监听 `NSWorkspace.didActivateApplicationNotification`
  与 `activeSpaceDidChangeNotification`（`installFocusGuardIfNeeded`，只装一次），
  交给 `activeController`（`refresh` 入口登记）调 `abandonSessionOnFocusLoss`：走
  `endSession(client:)` 与 `commitComposition` 同语义，再 `refresh(lastClient)` 清掉
  客户端 marked 区（走 `clearMarkedText` 的忽略空插入兜底）；客户端取最近一次按键
  事件带来的 `lastClient`，不向 IMK 重新要（焦点通知不是 IMK 回调）。面板若曾成为
  key 窗口，`NSWindow.didResignKeyNotification` 经 `CandidatePanelController.onResignKey`
  同样结束会话。自己进程激活（面板 `nonactivatingPanel`，理论不会）忽略。
- **Windows**：`OnSetFocus(FALSE)` 对已激活实例 `resetSession` 并 `endComposition`
  （收候选窗；已确认段先落盘），前台离开本进程由前台钩子收口（见「零阻塞契约」）。
- **Linux（ibus）**：`focus_out` → `focusOut()`：已确认段 `commit`、`sessionReset`、
  `exitAssociate`、`hidePreedit`、`hideLookupTable`。
- 回归：macOS `tests/key_routing` 直接驱动 `ChaosetController.handleFocusLossSignal`
  （组词态 / 渐进已确认段落盘 / 联想退出 / 幂等 / 自己进程忽略，共 10 条）；
  `checkuiparity`「候选窗无特权（失焦即收）」组锁住三端实现与本文档（含变异测试）。

## 无文本输入焦点时的中英切换（issue #132）

TSF 只在**有文本输入焦点**时把按键投递给输入法：桌面、资源管理器空白处、任务栏等
没有输入框的场景，`Shift` 根本到不了 TIP。项目零副作用契约又禁止 `SetWindowsHookEx`
之类的全局键鼠钩子。因此补充入口是**系统热键**（不是钩子）：

- **实现**：单例 UI 宿主 `chaoset-ui.exe`（DLL 内的 `mode_hotkey.cpp` 叶子模块）调用
  `RegisterHotKey(Ctrl+Shift+Space)`，系统在按键时直接投递 `WM_HOTKEY`——不钩任何
  进程、不占按键流、与零阻塞契约相容；窗口过程收到后**转投 `kUiToggleModeMsg`**，
  复用托盘左键的同一条语义（只在 `mode_scope=system` 时翻转全局中英，`process` 档
  不改全局状态）。销毁窗口前 `UnregisterHotKey` 归还这个全局独占资源。
- **注册失败只降级**：组合键被其它程序占用时只写 `tsf.log` 并保持托盘左键可用，
  绝不影响输入；`MOD_NOREPEAT` 保证按住不放不会来回快速切换。
- **有输入焦点时的行为**：系统吞掉主键（Space）但把 Ctrl/Shift 的按下释放继续投递给
  TIP。TIP 的 Shift 空按状态机因此必须先用 `GetAsyncKeyState` 查 Ctrl/Alt/Win 物理状态，
  已按住就把这次 Shift 记成「非空按」（`sinceShift_ = true`）——否则松 Shift 时会再切一次，
  与 UI 宿主的翻转相互抵消并误 flush 当前组词。守卫到位后净效果是**切一次**：UI 宿主
  翻转（`system` 档），TIP 不再重复翻转。
- **已知代价（设计取舍）**：`RegisterHotKey` 是**全局独占**——有输入焦点时这个组合也
  会被输入法抢走，占用它的应用（部分 IDE 的「参数提示」等）收不到。选 `Ctrl+Shift+Space`
  是因为它在「没有输入框」这个真正需要它的场景里几乎不冲突。`mode_scope=process` 时
  热键仍注册但按下无动作（作用域可在运行时切换，UI 宿主不轮询设置，因此不按作用域
  条件注册/注销）；此时组合键仍被全局独占。
- **三端范围**：仅 Windows。macOS 的输入法菜单与 Linux 的 ibus 面板本身就能在没有
  输入框时切换/查看状态，未加额外热键。
- **回归**：`checkuiparity`「无焦点中英切换热键」组锁住注册/注销/`MOD_NOREPEAT`/转投
  复用/禁止全局钩子，并断言 `ui_host.cpp` 的行数预算（热键实现必须在独立叶子 TU）。

## 设置窗契约（Slint 唯一实现 · 蓝白）

设置 UI 只有一份实现：跨平台 Slint 设置中心（`shims/settings`，产物
`chaoset-settings`）。macOS / Windows / Linux 壳**不实现原生设置窗**，只保留启动器
把设置中心拉起来：

- macOS：`SettingsLauncher.swift` → app 内 `Contents/Resources/ChaosetSettings.app/Contents/MacOS/chaoset-settings`
  （独立 app bundle：有 Info.plist 与品牌图标，Finder/Dock 不会显示通用可执行文件图标）；
- Windows：单例 UI 宿主 `chaoset-ui.exe` → `settings_launcher` → 同目录
  `chaoset-settings.exe`（TIP 只经命名管道请求，不在 TSF 进程内建进程）；
- Linux：`chaoset-setup`（纯 POSIX 启动器，不链接 GTK）→ PATH/`/usr/bin` 下的
  `chaoset-settings`。

设置中心的冷路径读写统一走同目录 `chaosetctl`（multicall：config/userdict/import），
与 daemon 共用的设置单一真源（Rust `crates/chaoset-config`）保持一致；找不到设置中心或启动
失败时只明确报错，**不回退**旧原生窗口。随包位置：macOS app `Contents/Resources/`、
Windows 安装目录、Linux `/usr/bin/`；缺少设置中心的打包会直接失败。

- 尺寸与骨架：客户区 640×560；左侧导航列宽 148（三项「输入 / 词库 / 关于」，
  选中项 accentSoft 软胶囊 + accentText 半粗字）；内容区顶部避开标题栏；页脚固定
  高，**所有页**左下显示「Chaoset 版本 x.y.z」。Slint 自绘，无平台窗口差异。
- 主题：跟随系统/浅色/深色三档，浅深两套令牌见下表；设置中心自身即时切换。
- 视觉令牌（**单一事实源**；Slint `ui/tokens.slint` 逐值引用，浅色/深色两套 sRGB）：

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
  | shadow | #1B4A80 | #000000 | 卡片阴影（浅 12% / 深 38% 透明度） |

  由 tools/checkuiparity 的「设置窗视觉令牌」组逐值断言（Slint 是唯一被断言的实现）。
- 几何：卡片圆角 14、内边距 18、卡片间距 16；行高 36；开关轨道 38×22（滑钮 16）；
  choice 胶囊高 30、圆角 9；按钮高 32、圆角 9；说明 11；页脚 11。
  文字与底色对比：正文 ≥4.5:1、大字号 ≥3:1；不引入纯黑纯白。
- 控件形态（Slint `components.slint`）：`Card` / `ChaosSwitch` / `ChoicePill` /
  `ChaosButton`：开关标签左/开关右；二选一与双拼四选一用软胶囊；主按钮
  accentStrong 实底 + onAccent 文字，次按钮 controlBg + controlBorder（清空文字 danger）；
  导航选中 accentSoft 胶囊 + accentText。
- 图标规范：页面图标只用 **SVG**（`shims/settings/ui/icons/*.svg`，`Image` 的 `colorize` 跟随主题令牌着色），
  **禁止用 emoji/Unicode 符号当图标**（导航、卡片标题、候选预览箭头/齿轮、关于页品牌图同一口径）；
  `tools/checkuiparity`「图标规范」组机器断言 Slint 页面不含 emoji、引用的 SVG 均存在。
- 关于页：应用图标（品牌 `app.svg`）+「Chaoset 输入法」+ 平台副标题 + 「常用快捷键」
  帮助区（Shift / Shift+空格 / Ctrl+空格 / 方向键 / 空格/回车 / 数字 / Esc）+
  「检查更新」「项目主页」按钮（经系统浏览器打开 Release/仓库）+ 许可证
  「代码 MIT · 词库 CC BY-SA 4.0」+「设置项会随版本持续补充」+ 仓库地址
  `https://github.com/ShanWuYinShe/chaoset_ime`；**版本号只在页脚**，关于卡片不放。
- 页面与文案：
  - 输入页「输入行为」：词组联想开关、模糊音开关、双拼方案四选一胶囊
    「全拼 / 自然码 / 微软 / 小鹤」（修改后自动重启 daemon 生效）；
  - 输入页「中英状态」：二选一「全局共享 / 每进程独立」；
  - 输入页「外观主题」：三选一「跟随系统 / 浅色 / 深色」+ 候选窗微预览；预览与真实候选窗同构（序号键帽、蓝色预选胶囊、学习绿星、齿轮/页码/上下箭头，不画预编辑），深浅配色对齐 `CandidateTheme`；
  - 词库页「学习数据」：统计行 + 清空（二次确认）/导出按钮；
  - 词库页「词库」：导入按钮 + 说明「支持 dict.yaml / CC-CEDICT txt / TSV；导入后自动重启输入法。」；
  - 关于页「关于」：跨平台中文输入法、常用快捷键帮助、检查更新/项目主页按钮、许可证、仓库链接。
- 交互契约：单实例（`src/single_instance.rs`：Unix 本地 socket / Windows 命名互斥体 +
  `FindWindowW` 前置已有窗口），重复启动只转发页号或前置窗口；导入成功/模糊音/双拼
  修改后自动重启 daemon；清空学习数据先二次确认。
- 已知差异：关闭联想后，已显示的候选窗在下一次显示时才收起（「下一次显示」生效；
  设置中心与输入法壳是独立进程，不再有跨进程广播链）。
- 禁止色：0A84FF / F5EFE6 / FFFCF7 / C9703F / A85427 / E8DCCC / F6E6D9 / D16B29 / 994014
  不得出现在设置中心源码（旧系统蓝与暖陶色与蓝白设计冲突；蓝白令牌 #007AFF/#0066EB
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
  flipPage / CandidateWindow::show，以及失步恢复的 handleKeyDesynced /
  syncDaemonInput / keepCompositionOnTimeout。
- **副作用统一入口**：每进程一个 `ShellWorker` 后台线程（`ShellTask` 任务队列）
  + message-only 窗口（`HWND_MESSAGE`，`kTipForegroundMsg`/`kTipModeAuthorityMsg`/
  `kTipShowToastMsg` 回投结果）；建进程、管道重试、
  派发 UI、daemon 代拉起全部在 worker 执行。
- **异步日志**：`diagPrintf` 对 `tsf.log` 只做 `enqueueLogLine`（有界 ring，
  worker 批量写盘；worker 未启动直接丢弃，**绝不回退同步写**）；按键路径不写盘
  （`candidate shown` 整条删除），前台风暴日志限流 5s。
- **管道 I/O**：`FILE_FLAG_OVERLAPPED` + 明确截止时间（TSF 路径 30ms、
  worker/设置窗 200ms，`setDeadlineMs`）。5s 熔断（`circuitOpen`）只用于 worker 的
  慢连接路径 `ensureDaemonConnected`；**TSF 按键路径不熔断**——每键只做一次非阻塞
  `tryConnectOnce`，超时后的下一键才能立刻重连。
- **读超时不断连（三端）**：I/O 超时的处置按方向区分——读超时只说明 daemon 这次算得
  慢，**连接没坏**，断连会让 daemon 会话随连接消失、用户已输入拼音全丢（失步恢复 +
  `base` 重建只是补救）。现在读超时保留连接，把该响应记为「迟到待丢」，下次请求前
  按帧长整帧读掉丢弃（不看 id，对应请求已放弃）；如果预清理时迟到帧还没到（drain 自己
  也超时），本次请求发出后由响应循环按 `staleResponses_`/`staleResponses` 计数识别并丢弃
  排在前面的旧帧，再继续等本次响应——计数为 0 的 id 错位才是真错位，仍断连：
  Windows `PipeTransport::lastReadTimedOut`
  + `pipeIo` 读分支直接返回、共用 `Session::call`/`drainStaleResponse`（`shims/common/ipc`）、
  macOS `DaemonTransport` 用 `recv` 的 `EAGAIN/EWOULDBLOCK` 区分超时与 EOF、Linux
  `UnixTransport::lastReadTimedOut` 同口径（三端 `off == 0` 判据）。写超时、对端
  关闭、帧体读到一半才超时仍必须断连（帧无法再对齐）；连续读超时超 2 次也断连重整。
- **读超时后不得原样重放增量 `input`**：daemon 可能已经处理过那次请求（只是响应迟到），
  原样重发会把同一段字符追加两次（预编辑 `aa`、上屏错字）。三端都用 `base` 做幂等重放
  ——daemon 先把引擎输入串整体置为 `base` 再追加 `text`，与逐键输入同构：macOS
  `EngineClient.input(_:halfWidth:base:)`（`call` 层读超时直接返回、由 `input` 带 base 重放）、
  Linux `Daemon::inputWithRebuild`（失败后带 base 重发）、Windows 失步态 `syncDaemonInput`
  的 base 重建。**正常按键不带 base**——只有读超时/断连重放与 macOS `recompose` 的显式
  `rebuild` 才带，否则 daemon 每键 `restore_input` 会清空双拼暂存的半键（`dp_pending`），
  两键无法成对解码。非幂等方法（`input`/`backspace`）读超时后只能重建，不能盲目重试；
  幂等读方法（`nextPage` 等）才允许重试。
- **只有「一个字节都没消费」的超时才能保留连接**：读超时若本次 `readAll` 已经读进过
  字节（帧头部分到达、或跨 chunk 的前一段已完成），这些字节已从管道/socket 取出，随
  失败丢弃会让**帧边界永久错位**——保留连接只会让后续每次读取都解析到垃圾。三端用
  「本次 `readAll` 是否消费过字节」把这个分支分开（Windows `nothingConsumed = off == 0
  && cancelled == 0`，macOS/Linux `off == 0`），部分读取与 EOF 都必须断连；
  `drainStaleResponse` 遇到非干净超时同样必须 `closeConn`（不能只清计数）。
  由 `core_test::testReadTimeoutKeepsConnection`（10 条）与 `checkuiparity`「超时韧性」组的
  变异测试断言。
- **fail-open（daemon 不在）**：连不上/管道断开时 `ensureDaemonFast` 单次非阻塞
  探测，失败即原样放行并后台连接（`postDaemonConnect`）；按键不丢、不阻塞，下一键
  恢复候选。首 1–2 个内容键可能按英文直入（已确认可接受）。
- **超时不拆组词（失步恢复，`tip_desync.cpp`）**：30ms 截止在桌面系统上会被偶发
  调度停顿击穿，所以「超时」与「daemon 不在」分开处理。组词中任一引擎请求超时
  （`lastIoWasTimeout`）都经 `enterDesyncOnTimeout` 保留本地组词并置
  `daemonDesynced_`：追加键记入 `pendingKeys_` 并乐观显示在预编辑里；失步中的按键
  由 `handleKeyDesynced` 分流（退格 / Esc / 回车本地处理；选词、翻页、鼠标点选先
  重建、只刷新候选不动作；标点重建后照常处理）；重连后 `syncDaemonInput` 用
  `input` 的 `base` 参数把整串重建到新会话（daemon 侧 `Session::restore_input`）。
  daemon 在 2s 宽限期内一直连不上才放弃：本地内容按原始串落盘（一个键都不丢）。
  首键（尚无组词）仍 fail-open。由 `tools/checkuiparity` 的「超时韧性」组机器断言
  （含变异测试）。
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
| 字母/撇号 | 组词；双拼按两两成对解码。**大写字母同样入组词**：保留原样供回车原样上屏（CommitRaw），拼音匹配走小写口径——用户不必为了输大写英文切到英文模式；三端壳都不得把大写字母放行给宿主 |
| 数字 1-9（含小键盘） | 选本页候选（按序号直选，不移动页内预选）；越界收起联想/放行原键 |
| 空格 | 组词态确认**页内预选项**（默认首选）并渐进上屏；有已确认段时拼接确认段+原始拼音上屏；联想态确认预选联想词 |
| 方向键 上/下/左/右 | 组词/联想有候选时：**上/下 = 翻页**（上一页/下一页；翻页后预选归零，边界页保持不动且仍消费）；**左/右 = 页内预选移动**（前一项/后一项；页边界保持不动且仍消费）；空格确认预选项；无组词/无候选时放行宿主（绝不消费）；预选在新输入/翻页/选词后归零 |
| 回车 | 确认段按文本 + 剩余按原始拼音一次上屏 |
| - = [ ] PageUp/PageDown | 候选/联想翻页；无候选时 [ ] 输出【】；候选窗上/下箭头点击等效翻页（首页只有下箭头、末页只有上箭头、中间页两枚、单页置灰不可点） |
| Shift 空按 | 切中英；已确认段先落盘；释放事件放行给宿主。记账必须是**幂等**的（Windows 用 `shiftHeld_` 位掩码，Linux 用 `shiftDown` 计数但 focusIn/focusOut 复位），任一端丢 KeyUp 都不得永久失效。**纯事件驱动，不得引入常驻定时器轮询**；宿主的焦点抖动（浏览器在地址栏/页面元素间搬焦点同样触发 `OnSetFocus(FALSE)`）只能**挂起**按住状态、不得直接复位，否则表现为「能打字但 Shift 切不了中英」；丢 KeyUp 时在**下一次按键**上自愈收口。Ctrl/Alt/Win 已按住时的 Shift **不算空按**（组合键里的修饰键；UI 宿主 `Ctrl+Shift+Space` 热键依赖这一点，否则会双翻转并 flush 组词） |
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
- tools/checkuiparity：三端设置窗文案/单例、「设置核心（Rust 单一真源）」
  （`crates/chaoset-config` 是 schema/默认值/持久化唯一实现，daemon 与 CLI 共用，
  三端存储键不变）、「Rust CLI 发行面」（构建/打包链只从 `crates/chaosetctl` 产出 CLI；
  Linux 包 `chaosetctl` + 四个软链、macOS app `chaosetctl` + `chaoset-import` 两份副本、
  Windows 包 `chaosetctl.exe`；安装/升级用 `chaosetctl import -seed -backup`；已删的 Go
  运行期实现不得回归），设置窗契约（640×560pt、页脚版本号
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

macOS/Windows 候选数据都来自 daemon 的纯数据 IPC；单键处理性能以真实输入体验与
`cargo test --release` 为准，不在文档里写死数字。壳侧回归跑 macOS visual-parity.sh、
Windows core_test 与 release 冒烟。

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