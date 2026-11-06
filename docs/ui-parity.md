# 候选窗与交互三端一致性

本文件记录 macOS / Windows / Linux 三端的当前视觉与交互契约。权威实现：

- 基准期望值（布局常量 + 浅/深各 13 色）：内联在 shims/macos/tests/visual-parity.sh；
- macOS 渲染：shims/macos/src/CandidatePanel.swift；
- Windows 渲染：shims/windows/src/win32.cpp 与 shims/windows/src/candidate_layout.hpp；
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
| 设置入口 | 候选栏齿轮 → AppKit 设置窗 | 候选栏齿轮 / 托盘图标 → 单例 UI 宿主里的 Win32 设置窗 | ibus-setup → GTK3 设置窗 | 三端设置窗单例；Windows 设置窗不属于 TIP 宿主进程（开始菜单/搜索等 AC 宿主里也能打开） |
| 中英状态指示器 | 无（系统菜单栏图标由系统绘制） | `chaoset-ui.exe` 的唯一托盘「中/英」图标；TIP 经命名管道上报模式，不在光标/鼠标旁弹提示 | 由 ibus/桌面环境指示 | 2026-11-05 用户反馈定稿：每用户会话只许一个状态指示器 |
| Windows UI 宿主 | — | `chaoset-ui.exe`（登录 Run 项 + TIP 懒启动；`Local\ChaosetUi-<SID>` 单例）；托盘 + 设置窗唯一宿主，UI 实现仍在 `ChaosetIME.dll` | — | TSF 会把 TIP 加载进每个宿主进程，进程内自绘托盘会出现多个图标 |
| 关闭联想即时性 | 设置窗通知，已显示联想立即收起 | HWND_BROADCAST → 各宿主候选窗收起 | 下一次按键生效 | Linux 为平台机制差异 |
| 学习数据管理 | 设置窗统计/清空/导出 | 同左 | 同左（调用 chaoset-userdict 本地模式） | 数据都在 daemon |
| 中英状态作用域 | UserDefaults ChaosetModeScope | HKCU Software\Chaoset\ModeScope | 配置文件 mode_scope | 三端都支持 system/process/window 三档，默认 system |
| 词库导入 | Setup.app：选择文件 → chaoset-import -o -seed -backup → 重启 daemon | 设置窗同样调用 chaoset-import → 重启 daemon | 设置窗调用 chaoset-import → 重启 ibus | 合并在 Go 工具中完成 |
| 鼠标点选候选 | 原生候选窗点击 | 原生候选窗点击 | CandidateClicked | 与数字键同语义 |
| 滚轮翻页 | 未实现 | 不可行：候选窗不获得焦点，需全局鼠标钩子 | 不适用 | 三端不做，避免干扰游戏鼠标 |
| 无障碍 | VoiceOver 候选文字/窗口级 | UIA 窗口级 provider | 由 ibus/桌面读屏决定 | Windows 候选子项未实现 |
| Windows per-monitor DPI | 系统自动 | GetDpiForMonitor 按锚点/窗口 DPI，处理 WM_DPICHANGED | 由桌面环境决定 | 混合 DPI 多屏需真机验收 |
| 双拼 mspy ; | 支持 | 支持 | 支持 | 引擎下发 shuangpin，壳路由到 input |
| 学习标记 * | 显示 | 显示 | 不适用 | ibus 无独立装饰层 |
| 面板质感 | 半透明 sRGB 底 + 1pt 描边 + 系统阴影 | 不透明预混合色 + 1pt 描边 + CS_DROPSHADOW + SetWindowRgn 圆角 | GDI 限制；Windows 无毛玻璃、圆角有锯齿 |
| 窗口层级 | .floating(3)，不遮蔽菜单栏 | WS_EX_TOPMOST + owner=宿主窗口 | owned 候选窗在宿主之上，可跨开始菜单/搜索的沉浸式 z 序 |
| 全屏免打扰 | 无 AppKit 对应机制 | D3D 全屏/演示模式抑制候选窗；FullscreenSuppress 注册表可关 | 只抑制显示，不改变按键/组词；无边框全屏不触发 |
| 齿轮 hover | 无 | 无 | 刻意不做随输入变色 |
| 设置窗 | 左导航三页（输入/词库/关于）+ 右侧内容页，NSSwitch | 左导航三页（输入/词库/关于）+ 右侧卡片 + 页脚版本号 + 滚动，640×560pt | GtkStackSidebar + GtkStack 三页（输入/词库/关于）；Windows 深色/DPI 需真机验收 |

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
- tools/checkuiparity：三端设置窗文案/单例、学习数据 GUI、词库导入安全、
  联想即时性、退格/修饰键语义、候选窗几何/配色字段、候选点选/右键删除、
  单例 UI 宿主与模式指示器（不许回退为 TIP 自绘托盘/toast）、打包安装、
  CI 覆盖、shell 转义与文档对照。
- Windows 候选窗常量由上述静态工具校验；像素级渲染仍需真机观察，不能把
  编译级验证写成“双端已验证”。

## 性能参考

macOS/Windows 候选数据都来自 daemon 的纯数据 IPC；daemon 单键处理基准在
engine/scale_bench_test.go，用 go test ./engine -bench . 复跑，不以固定数字作为
硬指标。壳侧回归跑 macOS visual-parity.sh、Windows core_test 与 release 冒烟。
