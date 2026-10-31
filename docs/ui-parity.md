# 候选窗三端参数对照表（UI 一致性审计）

> 审计/整改日期：2026-10-30（基线 main `b4785e6`；2026-10-30 三端一致性整改
> worktree `fix/ui-parity`）。上一版头注「基于 commit 53efd57」已过期，且 387d571
> 之后的三端 Shift+空格/Super 行为变更未回写——本版按**当前代码实际行为**重写。
> 维护规则见 AGENTS.md「候选窗为纯数据 IPC + 三端原生自绘」：候选窗任何 UI
> 改动（控制区/齿轮/翻页/配色/层级）必须三端同步检查——只改一个平台 = 未完成。
> 本表是审计基准；改动排版参数必须同步更新本表、`skin/skin_test.go`、
> `shims/macos/tests/visual-parity.sh` C 段期望值与各端源码常量。
>
> 2026-10-30 第二轮（worktree `fix/audit-p1`，审计后修复批次）：修复 Windows 设置窗
> UAF、Windows 描边线宽 pt 化、mac 超长候选截断、三端「已确认段」收尾路径统一、
> 双拼 mspy `;` 键全链路、Arch 包设置脚本、userExport 分页、Linux 延迟落盘；
> visual-parity C 段补 lineSpacing/maxWidth/cornerRadius 三项期望值（共 49 条断言）。

## 单位口径（2026-10-30 统一为 pt）

- **macOS**：AppKit 逻辑坐标即 pt。
- **Windows**：统一 `pt(v) = MulDiv(v, dpi, 72)`（pt → 设备像素，四舍五入）。
  历史上字号用 pt 口径，而间距/键帽/圆角/最大宽用 `MulDiv(v, dpi, 96)`
  （px@96，物理只有 0.75 倍）——同一面板两套基准，Windows 上「字一样大、留白
  和键帽小 25%」，最大宽只有 macOS 的 75%，候选更容易被丢弃/截断。2026-10-30
  已全部改为 pt，与 macOS 1:1（`win32.cpp` 的 `pt()` 注释含逐 DPI 像素表）。
  Windows 的多行/单行等布局机制差异见「已知功能差异」。
- **描边线宽**：两端统一 1pt——Windows 面板/键帽/分隔线/Toast 边框从固定 1 设备
  像素改为 `max(1, MulDiv(1, dpi, 72))`（2026-10-30 修复：此前 125%/150% 缩放下
  比 mac 细 33%–40%）。
- **Linux**：ibus 原生面板，无自绘参数。

Windows 布局参数在各 DPI 下的设备像素值（`MulDiv(pt,dpi,72)` 四舍五入）：

| 参数（pt） | 96dpi | 120dpi | 144dpi | 192dpi |
|---|---|---|---|---|
| padding 7 | 9 | 12 | 14 | 19 |
| firstPadLeft 9 | 12 | 15 | 18 | 24 |
| itemPadX 7 | 9 | 12 | 14 | 19 |
| gap 11 | 15 | 18 | 22 | 29 |
| chipPadX 4 / chipPadY 2 | 5 / 3 | 7 / 3 | 8 / 4 | 11 / 5 |
| pillMargin 2 | 3 | 3 | 4 | 5 |
| chevronBoxW 36 | 48 | 60 | 72 | 96 |
| gearBoxW 26 | 35 | 43 | 52 | 69 |
| panelRadius 8（RoundRect 直径 16pt） | 11（直径 21） | 13（直径 27） | 16（直径 32） | 21（直径 43） |
| textH 21 | 28 | 35 | 42 | 56 |
| maxWidth 720 | 960 | 1200 | 1440 | 1920 |
| minWidth 84 | 112 | 140 | 168 | 224 |
| 光标下间距 6 | 8 | 10 | 12 | 16 |

计算方式见 `win32.cpp` 的 `pt()` 注释；全部为小正整数像素，窗口宽度另有
显示器 `rcWork` 钳制，面板不会溢出屏幕。

## 排版参数（macOS Layout ↔ Windows pt，单位 pt）

| 参数 | macOS `CandidatePanel.swift` | Windows `win32.cpp` | 一致 |
|---|---|---|---|
| 主字号 | fontSize 15 | 字体高度 `-MulDiv(15,dpi,72)` | ✅ |
| 序号字号 | indexFontSize 12 | `-MulDiv(12,dpi,72)` | ✅ |
| 行高 | textH = ceil(fontSize*1.4) = 21（CJK 安全值） | `max(tmHeight, pt(21))` | ✅ 口径一致 |
| 面板内边距 | padding 7 | `pt(7)` | ✅（2026-10-30 修正，原 0.75×） |
| 首选左缩进 | firstPadLeft 9 | `pt(9)` | ✅（同上） |
| 候选间距 | itemPadX 7 | `pt(7)` | ✅（同上） |
| 项间隔 | gap 11 | `pt(11)` | ✅（同上） |
| 键帽内距 X/Y | chipPadX 4 / chipPadY 2 | `pt(4)` / `pt(2)` | ✅（同上） |
| 键帽圆角 | chipRadius 3 | RoundRect 直径 `pt(6)` | ✅ |
| 首选胶囊外扩 | pillMargin 2 | `pt(2)` | ✅ |
| 首选胶囊圆角 | max(panelRadius-pillMargin,4)=6 | RoundRect 直径 `pt(12)` | ✅ |
| 翻页区宽 | chevronBoxW 36 | `pt(36)` | ✅（同上） |
| 齿轮区宽 | SettingsGear.boxW 26 | `gearBoxW pt(26)` | ✅ |
| 面板圆角 | panelRadius 8 | RoundRect / SetWindowRgn 直径 `pt(16)` | ✅（形状等价；Windows 无抗锯齿） |
| 窗宽上限 | maxWidth 720（内容上限 638；常规窗宽 ≤714） | 内容上限 `pt(638)`；窗宽 = 内容 + pad×2 + 36 + 26（≤714pt） | ✅（2026-10-30 修正：原漏扣齿轮区与 pillMargin；mac 首项超长截断后同样 ≤714） |
| 最小窗宽 | padding×2 + chevronBoxW + gearBoxW + 8 = 84 | `pad*2 + chevronBoxW + gearBoxW + pt(8)` = 84pt | ✅（2026-10-30 修正，原 120px@96=90pt） |
| 最大行数 | maxRows 3 行折行（第 4 行起不绘制）；首项超宽截断加… | 单行 + 首项超宽省略号（后续项放不下不排列） | ❌ 折行机制不同（已知差异）；首项截断策略两端已一致（2026-10-30 mac 补齐） |
| 光标下间距 | candidateGap 6 | `pt(6)` | ✅（2026-10-30 修正，原 4px@96=3pt） |
| 页码/分隔线 | 见「折叠区几何」 | 同公式 pt 化 | ✅ |
| 学习标记宽度 | fromUser 时 markW = ceil("*")+2 | `markW = szMark.cx + pt(2)`，文本后 +1pt 起画 | ✅（2026-10-30 Windows 新增） |
| 描边线宽 | 1pt（面板/键帽/分隔线/Toast） | `max(1, MulDiv(1,dpi,72))` 设备像素（等价 1pt） | ✅（2026-10-30 修正，原固定 1px） |

## 折叠区几何（mac/Win 同公式）

- 齿轮区：`gearX = width - chevronBoxW - gearBoxW`；分隔线 `dividerX = gearX + 2`。
- 翻页/页码区：`flipZoneX = dividerX + gearBoxW`，中心 `chevX = flipZoneX + (width-flipZoneX)/2`。
- 内容块：`blockH = 8 + (多页 ? 3 + 页码高 : 0)`，`blockTop = (height-blockH)/2`；
  chevron 画在 `blockTop+4`（臂长 3、线宽 1.5、纵向 ±2.1），页码在
  `blockTop+8+3` 且**水平居中于 chevX**（Windows 2026-10-30 修正：此前从
  dividerX 到窗口右缘居中，中心偏左约 13px）。
- 分隔线：高 = `textH*55%`（21pt → 11.55pt），垂直居中（Windows 2026-10-30
  修正：此前用窗高 55%，约 19.5pt，明显偏长）。
- 翻页箭头方向：有下页 ⌄ / 末页有上页 ⌃；**单页时箭头对比度降到约 1/3**
  （mac alpha/3；Windows 2026-10-30 按同一混合几何折算为不透明色）。
- 齿轮：圆环 r3.4 + 线宽 1.8 + 8 齿辐条 4.6→7.2、线宽 2 圆头；颜色 = chipDigit；
  **只有 mac/Windows 两端**（Linux 用 ibus 原生面板、无齿轮）。

## 色值（主色两端逐字一致；派生色按 alpha 有效值对齐）

主色（自 `skin.DefaultSkinFor`）：

| 色 | 浅色 | 深色 |
|---|---|---|
| panelBg | #F6F6F8F6（mac，Windows 不透明 #F6F6F8） | #2C2C2EF5（Windows 不透明 #2C2C2E） |
| text | #1D1D1F | #FFFFFF |
| highlightBg | #007AFF | #0066EB |
| highlightText | #FFFFFF | #FFFFFF |
| chipBg | #FFFFFF | #5A5A5E |
| chipDigit | #1D1D1F | #FFFFFF |
| userMark（学习标记） | #34C759 | #30D158 |

派生色：macOS 用带 alpha 的 NSColor 在绘制时与底色实时混合；GDI 无逐字形
alpha，Windows 把**有效色**预混合成不透明常量（公式
`round(bg*(1-a)+fg*a)`，`win32.cpp CandidateColors::get` 注释列出逐色推导）。
2026-10-30 修正了此前手抄错误的 5 个有效色：

| 派生色 | mac 源色（alpha） | mac 有效值 | Windows 旧值 | Windows 现值 |
|---|---|---|---|---|
| panelBorder（浅） | 黑 1A on #F6F6F8 | #DDDDDF | #D4D4D8 | #DDDDDF |
| panelBorder（深） | 白 24 on #2C2C2E | #4A4A4C | #48484A | #4A4A4C |
| chipBorder（浅） | 黑 3C on #FFFFFF | #C3C3C3 | #D0D0D4 | #C3C3C3 |
| chipBorder（深） | 白 40 on #5A5A5E | #838386 | #78787C | #838386 |
| page/chevron（浅） | #8E8E93 α D0 | #A1A1A6 | #8E8E93 | #A1A1A6 |
| page/chevron（深） | #98989F α B8 | #7A7A80 | #98989F | #7A7A80 |
| divider（深） | 白 1E on #2C2C2E | #454547 | #3F3F42 | #454547 |
| highlightChipBg | 白 45 on 高亮蓝 | 浅 #459EFF / 深 #458FF0 | 同 | 同（两端精确一致） |
| divider（浅） | 黑 14 on #F6F6F8 | #E2E2E4（±1/255 舍入） | #E2E2E4 | #E2E2E4 |

> 有效值为四舍五入结果；个别通道与「目测取整」差 1/255 属正常。Windows
> 面板本身是不透明的（mac 是半透明 + 系统阴影），见「已知功能差异」。

## 学习标记 `*`

- daemon 的 `Candidate.from_user`（用户调频/词组/上下文加权）为 true 的候选，
  在候选文本后绘制绿色 `*`（文本后 +1pt，advance 为星号宽 +2pt；首选也画）。
- macOS：`CandidatePanelView.Item.fromUser` → theme.userMark。
- Windows：`core::Candidate.fromUser`（2026-10-30 新增解析）→
  `CandidateWindow::ItemPos.fromUser` → onPaint 用 `theme.userMark` 绘制
  （几何与 mac 同口径）。
- Linux：**不适用**——ibus 面板候选文本是唯一显示载体，没有独立装饰绘制层；
  追加 `*` 会污染候选文本/无障碍朗读。注意 IBusText 本身支持 per-item 文本属性
  （ibus 引擎自己就在用 underline），但只能改候选文本、没有独立装饰层，因此结论
  不变（2026-10-30 修正表述）。理由记录在 `shims/linux/README.md`；学习加权本身
  照常影响排序。

## 已知功能差异（有意/平台限制，非遗漏）

| 差异 | macOS | Windows | 说明/后续条件 |
|---|---|---|---|
| 多行折行 | maxRows 3 行折行；第 4 行起不绘制（数字键仍可能选中）；**首项超宽按字符截断加…**（2026-10-30 新增） | 单行布局；首项超宽逐字截断加…，后续放不下的项不排列（数字键仍可能选中） | 两端都存在「看不见但能选」的边角；Windows 折行需重排布局，待需求。首项截断策略两端已一致；数字键越界会收起联想/放行，不会静默选不可见项 |
| 面板半透明/阴影 | 半透明 sRGB 底 + 1pt 描边 + 系统阴影 | 不透明预混合色 + 1pt 描边（pt 化）+ CS_DROPSHADOW + SetWindowRgn 圆角（无抗锯齿） | GDI 限制；用户可见差异是「Windows 无毛玻璃/真实阴影、圆角有锯齿」 |
| 窗口层级 | .floating(3)（不遮蔽菜单栏） | WS_EX_TOPMOST + **owner=宿主窗口**（2026-10-30 实机修复） | Windows 下拉菜单是系统模态 topmost popup，候选窗不会拦菜单点击；mac 因 popUpMenu 会压菜单栏而降级。owner 让 owned 候选窗恒在宿主之上，可跨 Windows 11 开始菜单/搜索的沉浸式 z 序 band（此前无主 topmost 被压住导致完全看不到）；ModeToast 同策略（2026-10-31） |
| 全屏免打扰 | 无（AppKit 无对应机制） | D3D 全屏/演示模式下抑制候选窗（2026-10-31 实现）；`HKCU\Software\Chaoset\FullscreenSuppress` DWORD 缺省 1，可设 0 关闭 | 只抑制候选窗显示，不改按键/组词；无边框全屏返回 QUNS_ACCEPTS_NOTIFICATIONS 不触发（边界已记 windows README）；全屏浏览器/视频可能命中 D3D 全屏状态，可用注册表关闭 |
| 齿轮 hover | 无 | 无 | v5 起刻意移除（用户反馈「随输入变色」观感差） |
| 设置入口 | 齿轮 → NSSwitch 设置窗 | 齿轮 → Win32 设置窗 | Windows **没有**语言栏菜单/托盘入口（此前 README 描述有误，已修正）；唯一入口是候选栏齿轮 |
| 设置窗视觉 | 380×330pt（2026-10-31 加学习数据区）、NSSwitch、13/11pt、跟随系统深浅 | 380×390pt（2026-10-31 加词库区）、13/11pt、**跟随系统深浅**（DWM 深色标题栏 + 自绘背景/文字/复选框/按钮）、per-monitor DPI | Windows 深色/DPI 为编译级实现，**需真机验收**；mac/Linux 跟随系统 |
| 学习数据管理 | 设置窗「学习数据」区：统计 / 清空（二次确认）/ 导出 TSV（2026-10-31） | 同左（2026-10-31；设置窗独立 TcpTransport 连 daemon，不持有 TIP 指针） | 同左（2026-10-31；GTK 设置窗调用随包 `chaoset-userdict` 本地文件模式，清空时编排 `ibus exit` → 停引擎 → 清空 → `ibus restart`；**engine 包零改动**，无 DBus/平台代码进入引擎） |
| 词库导入 | Setup.app GUI：种子+现用+所选 → 备份 → 原子替换（2026-10-26） | 设置窗「词库」区：选择 Rime dict.yaml / CC-CEDICT txt / TSV（可多选）→ `chaoset-import` 合并 → 备份 → 同卷原子替换 → 重启 daemon（2026-10-31） | Linux 目前仅 CLI `chaoset-import`（GTK 设置窗导入待后续） |
| 设置窗单例 | 有 | 有 | Linux 每次点「首选项」可开多个 GTK 窗口（无单例），已知差异；Linux 设置保存**已即时生效**（2026-10-30） |
| 关闭联想即时性 | 立即收起联想窗 | **下一次按键/刷新时收起**（2026-10-30 改为不即时：设置窗不再持有 TIP 裸指针，消除 UAF） | Linux 同为下次按键生效（只写 config.json）；mac 走通知立即收起 |
| 双拼 mspy `;` 键 | 支持（2026-10-30） | 支持（2026-10-30） | 支持（2026-10-30） | 引擎 `Composition.shuangpin` 下发方案名，三端壳把双拼模式下的 `;` 路由到 `input`；会话级单测遍历全部音节锁可达性。**标点宽度瞬态已修（2026-10-31）**：`input` 新增可选 `half` 字段，三端壳每次按键下发全/半角状态，引擎先同步再处理 |
| Windows TSF 两阶段 | — | Shift 空按中英切换原写在 OnKeyDown/OnKeyUp；按 TSF 两阶段模型（OnTestKeyDown 返回 FALSE 的键不再调 OnKeyDown）可能永远收不到修饰键 | **2026-10-31 已把跟踪移到 OnTestKeyDown/OnTestKeyUp**（Test 阶段对每个按键都会调用），「非 Shift 键取消空按窗口」同步移到 Test 阶段；Ctrl/Alt/Win 按下与释放同样取消空按窗口（与 mac/Linux 对齐，2026-10-31）；`tsf.log` 诊断保留（`modifier in OnTestKeyDown/OnTestKeyUp/OnKeyDown/OnKeyUp`、`shift toggle`）。**仍需 Windows 真机确认**（若某宿主连 OnTestKeyUp 都不调，需改 GetKeyState 轮询） |
| 无障碍 | VoiceOver 虚拟子元素（AXPress 上屏） | 无 WM_GETOBJECT/UIA/MSAA（待做） | docs 旧版已列 TODO，仍未实现，需 Windows 真机 |
| 滚轮翻页 | 未实现 | **不可行**：`WM_MOUSEWHEEL` 只发给焦点窗口，候选窗 `WS_EX_NOACTIVATE` 永不获得焦点；要接收需全局鼠标钩子，与用户「游戏不干扰鼠标」硬要求冲突 | 三端一致缺失；mac 技术上可做，但为保持三端一致暂不做（2026-10-31 复核）。ibus 面板由上游决定 |
| Windows per-monitor DPI | 系统自动 | **已实现（2026-10-31）**：`dpiForPoint` 动态加载 shcore!GetDpiForMonitor，按锚点/窗口所在显示器取 DPI；候选窗、Toast、设置窗三处都处理 `WM_DPICHANGED` 重排 | 候选窗按光标所在显示器布局，Toast 按提示位置，设置窗按打开时所在显示器；仍**需真机验收混合 DPI 多屏**（本机无 Windows 运行时） |
| Linux 学习标记 | — | — | 平台机制不适用（见上） |
| 按键边角（ForwardDelete 等） | 见下 | 见下 | 三端主矩阵一致；以下边角差异按实际行为记录，未在本次改行为（避免无用户需求的行为变更） |

已修复并移除的差异（2026-10-30 第一轮）：Windows 单位口径（pt 统一）、Windows 学习
标记缺失、Windows 鼠标点选非渐进、Windows 页码偏左/分隔线过长/单页箭头不置灰、
Windows Toast 位置低一行/点击穿透缺失、Windows 小键盘数字选词、Windows 联想态
Shift+PageUp/Down 不翻页、Linux 设置项不即时生效、文档中与实际不符的 ✅。

第七轮（2026-10-31，`feat/linux-gui-userdict`）：Linux 学习数据管理进 GTK 设置窗
（调用 `chaoset-userdict` 本地文件模式；清空编排 `ibus exit`→停引擎→清空→
`ibus restart`；engine 包零改动，无 DBus/平台代码）。

第六轮（2026-10-31，`feat/win-settings-dark-dpi`）：Windows 设置窗深色跟随系统
（DWM 深色标题栏 + 自绘背景/文字/复选框/按钮）+ 三处 per-monitor DPI
（候选窗/Toast/设置窗，`dpiForPoint` 动态加载 shcore + `WM_DPICHANGED`）。

第五轮（2026-10-31，`fix/halfwidth-parity`）：双拼 `;` 标点宽度瞬态修复——`input`
请求新增可选 `half` 字段，三端壳每次按键下发全/半角状态，引擎先同步再处理
（此前 1 键瞬态：刚切全/半角后单按 `;` 可能按旧宽度出标点）。

第四轮（2026-10-31，`fix/p2-batch`）：Windows 主题切换重绘（`WM_SETTINGCHANGE`/
`WM_THEMECHANGED` 立即失效重绘，此前保持旧配色到下次输入）、Windows Shift 空按
跟踪从 Key 阶段移到 TSF Test 阶段（中英切换可达性，待真机复核）。

第三轮（2026-10-30/31，v0.2.17 真机 + `fix/win-followup`）：Windows 开始菜单/
搜索候选窗不显示（候选窗 owner 缺失 + UILess 类别声明与实现矛盾 + 安装器漏杀
`SearchApp.exe`；v0.2.17 真机确认恢复）、候选窗 owner 生命周期计数泄漏（owner
连带销毁时不归还 `g_liveCandidateWindows`）、ModeToast owner/防卸载计数缺失、
便携 ZIP 升级路径未清理旧 UILess 类别/未按模块杀宿主、NSIS 32 位 PowerShell
枚举 64 位模块失效、CI 缺 Windows 构建 job（已新增 `windows-build`）、全屏
免打扰（D3D 全屏/演示模式抑制候选窗，注册表可关）。

第二轮（同日 `fix/audit-p1`）：Windows 设置窗裸 TIP 指针 UAF、Windows 描边线宽
pt 化、mac 首项超长候选截断/钳宽、三端「已确认段」收尾路径统一（Linux 三条路径
泄漏/不落盘、Linux/Windows 空格无候选丢字、Windows Alt/Win 与 Shift 切换丢字）、
双拼 mspy `;` 键三端打通、Arch 包漏装设置脚本、userExport 分页（>1 MiB 可导出）、
Linux ibus 延迟落盘（不再每次选词全量重写）、userdict 写盘串行化。

## 按键与交互行为（三端已统一，2026-10-30 复核）

联想态与组词态的按键语义必须完全一致——历史上三端各写一份实现，长期漂移。

| 行为 | 键位 | 语义 |
|---|---|---|
| 组词翻页 | `-` `=` `[` `]` PageUp PageDown | 翻候选页；无候选/残缺组词时 `-` `=` PageUp/PageDown 清空组词后放行（`[` `]` 本身是标点键，此时输出【】——文档旧版「一律放行」不准确） |
| **联想翻页** | 同上六键 | 翻联想页（与组词态同集合）；面板翻页箭头同语义；**联想态带 Shift 的 PageUp/PageDown 同样翻页**（三端一致） |
| 选词 | 空格 / 数字 1-9（含小键盘） | 空格选首选并渐进上屏；数字选本页第 N 项，越界则收起联想/放行该键。**已确认段并入预编辑（以已转换形态显示），整条组词结束才一次落盘**——2026-10-30 起三端统一为「单一预编辑/合成」模型，修复部分程序渐进选段丢字（详见 README「渐进上屏的跨程序兼容模型」）。**空格在「有已确认段 + 剩余拼音无候选」时 = 确认段 + 原始拼音一次上屏**（三端 2026-10-30 统一，此前 Linux/Windows 丢确认段） |
| 联想选词 | 空格 / 数字 1-9（含小键盘） / Shift+空格 | 空格与 **Shift+空格都选当前页第一个联想词**并继续联想（Shift 不切全半角，三端实测一致）；数字越界收起联想并放行 |
| 组词回车 | Return | 已确认段按确认文本 + 剩余按原始拼音一次上屏（与系统拼音一致：已转换部分保留转换结果） |
| 组词退格 | Backspace（逐字符）；Shift/Ctrl+Backspace 全部删除 | 普通 Backspace 只回退剩余拼音，剩余删空时已确认段立即落盘收尾；Shift/Ctrl+Backspace 取消整条组词（含未落盘确认段）并消费。**Option/Alt+Backspace 三端语义不同，见「按键边角差异」** |
| 失焦 / 透传键 / 切换输入法 | — | 已确认段先落盘再切换/透传（用户确认过的字不丢；纯拼音维持「清空」旧语义） |
| 清空组词 | Ctrl+Backspace / Ctrl+U / Esc | 取消**整条组词**（含未落盘的已确认段）并消费按键 |
| 删到行首 | Ctrl+Backspace | 同清空（三端对齐） |
| 中英切换 | Shift 空按 | 提示「英文模式」/「中文模式」；**已确认段先落盘**（三端 2026-10-30 统一：此前 Linux 泄漏、Windows 丢弃）；Shift 释放事件仍放行给宿主（不吞修饰键状态机） |
| 双拼 `;` 键 | `;`（mspy 方案） | 双拼模式（`-shuangpin mspy`）下作为 `ing` 韵母键送组词（如 `m`+`;` = ming），不上屏标点；非双拼模式仍输出中文「；」；Shift+`;` 始终是「：」。2026-10-30 三端打通 |
| 全半角 | **组词态** Shift+空格 | 提示「半角标点」/「全角标点」，开关归壳本地持有；联想态不触发（见上） |

提示文案统一为上述四种，三端逐字一致（机器检查只锁了 Linux 的 2 条与文档
约束，其余靠 Code Review——见「机器检查覆盖」节，不做过度声明）。

按键边角差异（实际行为，未统一；需要用户明确需求再改）：

| 场景 | macOS | Windows | Linux |
|---|---|---|---|
| Option/Alt+Backspace | 按音节回退（`backspaceWord`） | 已确认段落盘后放行 Alt+Backspace 给宿主（宿主通常删文档前一个词；无按音节回退） | 视为整段清空（Mod1 与 Ctrl 同分支） |
| 组词态 ForwardDelete | 当作退格删拼音 | 未处理→清空组词后放行（删文档字符） | 未处理→清空组词后放行 |
| 联想态 Delete（非 Backspace） | 保留联想窗并透传 | 收起联想并透传 | 收起联想并透传 |
| 联想态 Ctrl/Alt/Super | 只清组词，联想窗残留到下次输入 | 收起联想 | 收起联想 |

## Linux

候选窗由 ibus 原生面板渲染（UpdateLookupTable/UpdatePreeditText），
无自绘 UI，本表排版参数部分不适用；模式提示经 UpdateAuxiliaryText。
**按键与交互行为矩阵同样适用**（联想态翻页键集合、空格选词、Ctrl+Backspace、
联想态 Shift+空格等均已在 `shims/linux/ibus-engine` 实现并由
`behavior_test.go` 锁定）。

设置入口经 ibus-setup 的「首选项」（组件 XML `<setup>`）打开同构的 GTK3 设置
窗口，写 `config.json` 的 `associate`。**保存即时生效**：引擎在每次进入联想前
重读配置（2026-10-30 修正，此前只在引擎启动时读一次，与 setup 脚本/README
宣称不符），已打开的输入上下文下一次选词即按新设置执行（与 macOS/Windows
「每次选词前重读设置」同语义）。`-associate` 显式传参时 flag 覆盖配置。

设置脚本的安装：deb 与 tar.gz 本来就带 `setup/chaoset-setup.py`；**Arch 二进制包
此前漏装**（2026-10-30 修复），release 冒烟现在对三个包都断言
`/usr/share/chaoset/chaoset-setup.py` 存在。

候选表/预编辑呈现语义（由 ibus 面板决定，文档明确记录以免当缺陷反复修）：
每页 6 个（`engine.PageSize()` 单一来源，标签 `1.`…`6.`）；候选高亮固定首选
（`CursorUp/Down` 空实现，面板内不可移动）；无候选时隐藏候选表，但预编辑
可独立可见（无法切分的输入仍显示拼音）；**翻页箭头可用性与页码由上游面板
绘制，引擎拿不到 pageCount 之外的分页状态，单页箭头无法像 mac/Win 那样置灰**；
`Enable/Disable/SetCursorLocation/Property*` 均为空实现。

## 机器检查覆盖（2026-10-30 据实修正）

- **事实边界**：`skin/skin.go` 是 macOS 视觉的 Go 侧设计基准，**没有运行时
  消费者**；真正渲染的是 `CandidatePanel.swift`（mac）与 `win32.cpp`
  `CandidateColors`（Win）两份实现。「视觉参数真源 = skin」的说法按此理解。
- `shims/macos/tests/visual-parity.sh`（CI macos-build 执行）实际断言 **49 条**
  （另有 2 条「导出成功」提示，不计入断言）：
  - B：布局常量 5 项 Go↔Swift（fontSize/padding/lineSpacing/maxWidth/cornerRadius）；
  - C：macOS Layout 其余 16 项参考常量（含 2026-10-30 新增的 lineSpacing/
    maxWidth/cornerRadius）+ divider height/y 的自检（macOS 是视觉参考，误改必须 FAIL）；
  - D：浅色 + 深色各 13 个色值 Go↔Swift 逐色比对。
- `skin/skin_test.go` 锁定 Go 侧浅/深各 13 个色值的期望值（CI test job）；
  visual-parity 负责 Go↔Swift 两侧一致。**注意**：lineSpacing/maxWidth/
  cornerRadius 现在同时被 C 段锁死，两端「同步改错」也会 FAIL（2026-10-30 修复
  此前只有 B 段互比、双端同改无人发现的缺口）。
- `shims/macos/tests/equivalence.sh` F 段只调用上面的脚本；文案已改为
  「布局常量（Go↔Swift）+ macOS 参考常量 + 浅/深色值一致」，不再声称「双端
  色值两段一致」或覆盖 Windows。
- **Windows 候选窗没有任何运行时/几何机器断言**：只有 `tools/checkgdi` 的
  ExtCreatePen 非空指针契约（`make vet`）+ 交叉编译（2026-10-31 起 ci.yml 的
  `windows-build` job 每次 push 都跑 mingw 交叉编译 + core_test + NSIS 安装包
  编译，不再只在打 tag 时验证）。学习标记/页码/分隔线/DPI/Toast/设置窗仍需
  Windows 真机人工验收（本机无 Windows 运行时）。
- `tests/key_routing` 是 macOS 按键路由回归测试，**未接入 Makefile/CI**（死
  测试），属于待补；Linux 按键行为由 `behavior_test.go` 覆盖（2026-10-30 新增
  已确认段收尾 4 条与 mspy `;` 用例）。双拼可达性由 engine 的会话级测试覆盖
  （遍历全部音节逐键喂 `Session.Input`）。
- IPC 侧 2026-10-30 新增 `userExport` 分页测试（含 >1MiB 场景）。
- 提示文案/设置窗文案/齿轮参数没有跨端机器检查；本表 + Code Review 仍是唯一
  防线。新增 `tools/checkuiparity` 静态检查器是后续建议（审计 G6），本次
  未做——文档不再宣称已有此检查。

## 性能与资源基线（2026-10-29，Apple M1，负载 5.35）

- 全拼组词 34µs/键（14 万词库）；简拼 ~520-585µs/键（宽前缀展开固有
  形态，BFS 参数 12/4x→8/3x 与 next 预分配实验均无显著收益，已评估
  不再动）；daemon RSS 64.2MB（含 14 万词库 Trie + 用户词典）。
  > 口径提示：`cmd/chaosetd/main.go` 注释另称常驻约 100–150MB，与本节实测值
  > 不一致（未复测，2026-10-30 审计发现）；引用内存数字时注明测量条件。
