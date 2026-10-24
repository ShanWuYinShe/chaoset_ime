# Linux 平台壳（ibus，已实现）

形态：**ibus engine（Go）直接内嵌 chaoset 引擎**。Linux 上 ibus engine
本就是独立进程，与「daemon 形态」天然一致，无需额外 IPC——engine 进程
= 统一候选窗/词库/自学习逻辑的宿主。

选 ibus 而非 fcitx5 addon 的原因：fcitx5 addon 是加载进 fcitx5 守护
进程的进程内 .so，引擎会被迫进宿主进程，与项目架构冲突。

## 目录

```
ibus-engine/
  main.go     入口：加载词库、DBus 连接、常驻
  ibus.go     DBus 胶水：Factory.CreateEngine、Engine.ProcessKeyEvent
              （按键映射到 engine.Session）、CommitText/UpdatePreeditText
              信号发射（IBusText 序列化 = 元组 ("IBusText", a{sv}, 文本)，
              与 libibus ibus_text_serialize 逐字段对应）
../test.sh    容器内端到端验证脚本
../setup-container.sh  可复现的容器验证环境（Ubuntu 24.04 + ibus 1.5）
```

按键：字母组词、`1`-`9` 选候选、空格/回车首选上屏、退格删除、Esc 清空、
标点键（`,` `.` `;` 等）输出中文标点（组词中先上屏首选；组词中的 `'` 为音节
分隔符；句号/逗号连按全角⇄半角循环）、`-`/`=` 与 Page Up/Down 候选翻页
（候选表随页刷新）、Shift 空按切换中英（英文模式全透传；混按
Shift+字母 不误触）、Shift+空格切换全角/半角标点。
带 Ctrl/Alt 修饰或未消费的按键一律放行给应用。

## 验证状态（2026-09-23，Docker linux/arm64 + ibus 1.5.29）

已在容器内完成的验证：

- **组件注册**：component XML 就位后 `ibus list-engine` 列出
  `chaoset - Chaoset Pinyin`；`ibus engine chaoset` 激活成功，
  `ibus engine` 确认全局引擎为 chaoset。
- **引擎进程**：ibus-daemon 按组件 exec 拉起 Go 二进制（参数正确），
  进程稳定存活（已修复：忽略 SIGHUP——容器会话退出会向进程组传播）。
- **按键行为（端到端）**：`test.sh` 向引擎对象直发
  `ProcessKeyEvent`——n/i/h/a/o 五键全部 handled=true，空格触发
  `CommitText` 信号，dbus-monitor 捕获到中文「你好」上屏。
- **候选表（端到端）**：组词中引擎发出 `UpdateLookupTable` 信号，
  dbus-monitor 确认载荷为 `IBusLookupTable` 元组（page_size=9、
  candidates 为 IBusText av），与 ibus_lookup_table_serialize 逐字段
  对应；数字键选择不再是盲选。
- **多实例（端到端）**：`CreateEngine` 两次创建独立实例，交错打字
  （实例 1=ni、实例 2=hao）后各自空格，`CommitText` 分别为 你好 / 好，
  证明组词状态按 InputContext 分离、互不串扰。
- 过程中修复一个真缺陷：引擎实例的 `engine.Session` 未初始化，
  首次按键即空指针崩溃。

## 已知限制

- 预编辑与候选列表均经 ibus 面板显示（UpdatePreeditText /
  UpdateLookupTable 信号）；面板的具体渲染样式由 ibus 决定。
- 多 InputContext 已按实例隔离（每上下文独立会话/中英模式，
  容器内验证状态分离）；焦点跟随（FocusIn/Out 精细化）待真机桌面。
- 候选窗自绘贴图（candidateImage）在此壳的显示接入待做。

## 图标与设置界面

- **引擎与应用图标**：component XML 的 `<engine>` 声明 `<icon>chaoset</icon>`，
  对应 `icons/hicolor/{48x48,128x128,256x256,scalable}/apps/chaoset.{png,svg}`。
  全套图标统一为跨端新品牌标识：极光电光蓝渐变底板 + 纯白与霓虹青「Cs」双字母标志 + 拼音声调符号（¯），在桌面环境与应用列表均保持高辨识度与现代质感。
- **设置入口**：`<engine>` 还支持 `<setup>命令</setup>`，ibus-setup 会据此显示该引擎的
  Preferences 按钮；系统不会自动生成配置项，当前引擎也没有配置界面。
- 已核对 IBus 上游源码（`ibusenginedesc.c` 的 XML 解析字段表，2026-10-22）：
  `icon`、`setup`、`symbol` 等元素均受支持。

## 真机使用（Linux 桌面）

```sh
GOOS=linux GOARCH=<arch> go build -o chaoset-engine ./shims/linux/ibus-engine/
sudo cp chaoset-engine /usr/local/bin/
sudo cp data/dict_seed.tsv /usr/local/share/chaoset/dict.tsv
# 组件文件 shims/linux/component/chaoset.xml 按 README 注释安装到
# /usr/share/ibus/component/ 后重启 ibus-daemon 即可
```
