# 候选窗三端参数对照表（UI 一致性审计）

> 审计日期：2026-10-29（基于 commit 53efd57）。
> 维护规则见 AGENTS.md「候选窗为纯数据 IPC + 三端原生自绘」：候选窗任何 UI
> 改动（控制区/齿轮/翻页/配色/层级）必须三端同步检查——只改一个平台 = 未完成。
> 本表是审计基准：改动排版参数后必须同步更新两侧与本文。

## 排版参数（macOS Layout ↔ Windows scale，单位 pt/px@96dpi）

| 参数 | macOS `CandidatePanel.swift` Layout | Windows `win32.cpp` | 一致 |
|---|---|---|---|
| 主字号 | fontSize 15 | MulDiv(15, dpi, 72) | ✅ |
| 序号字号 | indexFontSize 12 | MulDiv(12, dpi, 72) | ✅ |
| 面板内边距 | padding 7 | pad scale(7) | ✅ |
| 首选左缩进 | firstPadLeft 9 | firstPadLeft scale(9) | ✅ |
| 候选间距 | itemPadX 7 | itemPadX scale(7) | ✅ |
| 项间隔 | gap 11 | gap scale(11) | ✅ |
| 键帽内距 X/Y | chipPadX 4 / chipPadY 2 | chipPadX scale(4) / chipPadY scale(2) | ✅ |
| 首选胶囊外扩 | pillMargin 2 | pillMargin scale(2) | ✅ |
| 翻页区宽 | chevronBoxW 36 | chevronBoxW scale(36) | ✅ |
| 齿轮区宽 | SettingsGear.boxW 26 | gearBoxW scale(26) | ✅ |
| 面板最大宽 | maxWidth 720 | scale(720) | ✅ |
| 面板圆角 | panelRadius 8（NSBezierPath 抗锯齿） | RoundRect scale(16,16) 直径（=r8；SetWindowRgn 剪裁无抗锯齿） | ✅（形状等价，Windows 边缘锯齿为 GDI 限制） |

## 齿轮（三端统一矢量算法，2026-10-29）

中心圆环 r3.4/线宽 1.8 + 8 齿辐条 4.6→7.2/线宽 2 圆头线帽；颜色
chipDigit 语义（暗色纯白/浅色 #1D1D1F）。macOS NSBezierPath 手绘；
Windows GDI Ellipse(NULL_BRUSH)+ExtCreatePen(PS_GEOMETRIC 圆头)，几何参数
按 dpi/96 与 mac pt 值一一对应。

## 已知功能差异（有意/平台限制，非遗漏）

| 差异 | macOS | Windows | 说明 |
|---|---|---|---|
| 多行折行 | maxRows 3 行折行 | 单行 + 超宽截断省略号 | Windows 单行布局为既有实现；折行需重排布局，待需求 |
| 窗口层级 | .floating（不遮蔽菜单栏） | WS_EX_TOPMOST | Windows 下拉菜单为系统模态 popup，TOPMOST 不拦菜单点击；设计依据见 win32.cpp 注释 |
| 齿轮 hover 高亮 | 无（固定色） | 无 | v5 起刻意移除（用户反馈「随输入变色」观感差） |
| 设置入口 | 齿轮 → 设置窗口（NSSwitch 面板） | 齿轮 → 设置窗口（Win32 窗口 + BS_AUTOCHECKBOX） | 2026-10-29 起两端同构：标题「Chaoset 设置」/ 分组「输入行为」/ 词组联想开关 / 说明文字。Windows 旧的 TrackPopupMenu 单勾选项已删除 |
| 无障碍 | VoiceOver 子元素（AXPress 上屏） | 无 WM_GETOBJECT（待做） | 见审查报告遗留项 |

## 按键与交互行为（三端已统一，2026-10-29 审计）

联想态与组词态的按键语义必须完全一致——历史上三端各写一份实现，长期漂移
（翻页键集合不同、macOS 联想窗残留、Windows 面板箭头点了没反应等）。

| 行为 | 键位 | 语义 |
|---|---|---|
| 组词翻页 | `-` `=` `[` `]` PageUp PageDown | 翻候选页；无候选/残缺组词时清空组词后放行 |
| **联想翻页** | 同上六键 | 翻联想页（**与组词态同集合**）；面板翻页箭头同语义 |
| 选词 | 空格 / 数字 1-N | 空格选首选并渐进上屏；数字选本页第 N 项，越界则收起联想并放行该键 |
| 联想选词 | 空格 / 数字 1-9 | 空格选本页第 1 个联想词；1-9 越界时收起联想并放行 |
| 清空组词 | Ctrl+Backspace / Ctrl+U / Esc | 清空全部拼音并消费按键 |
| 删到行首 | Ctrl+Backspace | 同清空（三端对齐） |
| 中英切换 | Shift 空按 | 提示「英文模式」/「中文模式」并消费 |
| 全半角 | Shift+空格 | 提示「半角标点」/「全角标点」，开关归壳本地持有 |

提示文案统一为上述四种，三端逐字一致（机器检查不到的项靠本表约束）。

## Linux

候选窗由 ibus 原生面板渲染（UpdateLookupTable/UpdatePreeditText），
无自绘 UI，本表排版参数部分不适用；模式提示经 UpdateAuxiliaryText。
**按键与交互行为矩阵同样适用**（联想态翻页键集合、空格选词、Ctrl+Backspace
等均已在 `shims/linux/ibus-engine` 实现并由 `behavior_test.go` 锁定）。
设置入口经 ibus-setup 的「首选项」（组件 XML `<setup>`）打开同构的 GTK3 设置窗口。

## 性能与资源基线（2026-10-29，Apple M1，负载 5.35）

- 全拼组词 34µs/键（14 万词库）；简拼 ~520-585µs/键（宽前缀展开固有
  形态，BFS 参数 12/4x→8/3x 与 next 预分配实验均无显著收益，已评估
  不再动）；daemon RSS 64.2MB（含 14 万词库 Trie + 用户词典）。
