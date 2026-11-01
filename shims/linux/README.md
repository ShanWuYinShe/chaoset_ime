# Linux 平台壳（ibus 原生 C++，已实现）

形态：**C++/libibus 薄壳 + Go chaosetd 跨进程 IPC**，与 macOS/Windows 同构：

- `shims/linux/engine/`：原生 C++ `IBusEngine` 子类，只做 IBus 按键路由、
  预编辑/LookupTable/辅助文本等平台 UI；不包含拼音/词库/学习逻辑。
- `chaosetd`（纯 Go）：词库、拼音匹配、联想、用户词典，经 Unix socket 提供
  `Session` 协议（`shims/common/ipc/core.cpp` 的 `chaoset::Session`）。
- 首次按键时壳自动拉起 `chaosetd`；设置窗清空/导入词库前会先停 daemon，
  避免内存旧状态回写。
- ibus 使用私有 D-Bus 总线（`IBUS_ADDRESS`），不是 session bus：引擎必须走
  `ibus_bus_new()`/`IBusFactory`，直接连 session bus 会导致 ibus-daemon 调不到
  Factory（实测 `SetGlobalEngine` 超时）。

选 ibus 而非 fcitx5 addon 的原因：fcitx5 addon 是加载进 fcitx5 守护
进程的进程内 .so，引擎会被迫进宿主进程，与项目架构冲突。

## 目录

```
engine/
  main.cpp   原生 IBusEngine：Factory/ProcessKeyEvent/预编辑/候选表/联想
  daemon.cpp Unix socket 连接、XDG 路径、按需拉起 chaosetd
  build.sh   g++ + pkg-config ibus-1.0（C++17）
../test-ci.sh   CI 原生端到端验证脚本（真实 dbus + ibus + daemon）
../setup-container.sh  可复现的本地容器验证环境
```

按键：字母组词、`1`-`9`（主键盘与小键盘 KP_1-9）选候选（越界序号放行原键、
组词保持原样）、空格/回车首选上屏（残缺组词顶屏原始拼音，裸空格不进文档；
**有已确认段时 = 确认段 + 原始拼音一次上屏**）、退格删除（逐字符；Delete 同语义；
Shift/Ctrl/Super+退格或 Delete 整段清空；Alt+退格或 Delete 按音节回退；Ctrl+U
整行清空）、Esc 取消整条组词、标点键（`,` `.` `;` 等）输出中文标点
（组词中先上屏首选；组词中的 `'` 为音节分隔符；句号/逗号连按全角⇄半角循环；
`[` `]` 本身是标点键，输出【】）、`-`/`=` 与 Page Up/Down 候选翻页（候选表随页刷新；
残缺组词时清空组词放行）、Shift 空按切换中英（释放事件放行给应用，英文模式全透传；
混按 Shift+字母 不误触）、Shift+空格切换全角/半角标点。
两类切换均经辅助文本（auxiliary text）短暂提示目标模式（约 1.2s 自动消失）；
预编辑带整串下划线标记与末尾光标位置（应用可分辨「正在组词」）。
**已确认段（渐进上屏）**：与 mac 语义一致——退格删空剩余拼音立即落盘；
Shift 切中英先落盘；Shift/Ctrl/Super+Backspace/Delete、Ctrl+U、Esc 取消整条
（含未落盘确认段）；Alt+Backspace/Delete 按音节回退；Ctrl/Alt/Super 其余组合
透传前先落盘确认段（防预编辑与实文脱节，2026-10-30 统一）。
**双拼 `;` 键（2026-10-30）**：`-shuangpin mspy` 时 `;` 是 ing 韵母键
（`m`+`;`=ming），非双拼模式/Shift+`;` 仍是中文标点。

**词组联想**（2026-10-27 接入，对齐 macOS 壳）：选词上屏后按上屏词音节查续接
词组并经候选表展示（**上限 30 条，本地分页每页 6 条**），数字/小键盘选当前页
候选（继续联想）、空格选当前页第一个联想词上屏、回车/Esc 收起、字母退出联想
进入正常组词。**联想中翻页键与组词态完全同集合**：`-` `=` `[` `]` 与
Page Up/Down 六个键都翻联想页；面板翻页箭头（ibus PageUp/PageDown 回调）在
联想中同样翻联想页。越界数字收起联想后放行原键（不吞键）。
提示文案与 macOS/Windows 统一：中英切换「英文模式」/「中文模式」、
全半角切换「半角标点」/「全角标点」。
**全/半角标点**（Shift+空格）：开关归壳本地持有（2026-10-29 起三端统一），
切换不经 daemon 往返，状态经 SetHalfWidth 随标点请求告知引擎。

**设置项**：「词组联想」「学习数据」（统计/清空/导出）与「词库导入」都在三端一致的
**设置窗口**里配置（联想 2026-10-29 起，学习数据 2026-10-31 起）：
ibus-setup 选中 Chaoset 点「首选项」按钮（组件 XML `<setup>` 入口），或直接
运行 `/usr/share/chaoset/chaoset-setup`（原生 GTK3 C，与 macOS SettingsWindow /
Windows 设置窗口同构：标题「Chaoset 设置」/ 分组「输入行为」/ 词组联想开关 /
分组「学习数据」/ 说明文字）。窗口写 config.json 的 `"associate"`（缺省开启，显式 false 关闭），
**保存即生效**：引擎在每次进入联想前重读 config.json，已打开的输入上下文下一次
选词就按新设置执行（2026-10-30 起与 macOS/Windows「每次选词前重读设置」同语义，
无需 `ibus restart`；此前只在引擎启动时读一次，是文档与实现不符的遗留问题）。
config.json 仍可直接手工编辑。

**词库导入（2026-11-01）**：设置窗「词库」区选择 Rime dict.yaml / CC-CEDICT txt /
TSV（可多选），由原生设置窗 `setup_ctl.c` 调用随包 `chaoset-import` 合并到
`$XDG_DATA_HOME/chaoset/dict.tsv`（旧文件先备份、临时文件原子替换）。导入前
设置窗先停 `chaosetd`、重启 ibus 引擎，避免旧 daemon 继续使用内存中的旧词库；
新引擎首次按键会自动拉起 daemon 并用 `-dict` 加载系统词库与 `-imported-dict`
用户词库（不需要 root）。CI 的 linux-e2e 会捕获引擎 LookupTable 信号，断言
导入词「测试词」真的出现在候选列表（不只是日志里有加载路径）。

**学习数据（2026-11-01）**：设置窗「学习数据」区显示调频词/用户词组/上下文条数，
提供「清空学习数据…」（二次确认）与「导出学习数据…」（TSV，可回灌
`chaoset-import`）。实现刻意**不碰 engine 包**（保持纯拼音逻辑）：原生设置窗调用
随包 `chaoset-userdict` 的本地文件模式（-userdict FILE）完成 stats/clear/export；
清空时编排 `ibus exit` → 终止全部 `chaoset-engine` 与 `chaosetd` 并等待退出 →
清空文件 → `ibus restart`（失败再试 `ibus start`），防止 daemon 内存态在下一次
延迟落盘时把旧数据写回。导入同样先停 daemon。清空/导入会短暂重启输入法
（期间不可用），确认框已提示；`chaoset-setup` 二进制与设置窗逻辑同包安装
（缺了设置窗无法启动，发布冒烟已断言）。清空/导入/导出等耗时操作在 GThread
后台线程执行，完成后经 `g_idle_add` 回主线程更新 UI（GTK 非线程安全，工作线程
只跑 subprocess/文件 IO），避免设置窗「假死」。

**学习标记（不适用）**：macOS/Windows 候选窗对「被用户数据加权」的候选在文本后
绘制绿色 `*`；ibus 面板端不适用——`IBusLookupTable` 的候选文本是唯一显示载体
（同时用于无障碍朗读与上屏数据源），没有 macOS/Windows 那样的独立装饰绘制层，
追加 `*` 会污染候选文本。IBusText 本身支持 per-item 文本属性（引擎自己就在用
underline），但只能改候选文本本身、没有独立装饰层，因此结论不变（2026-10-30
修正表述）。学习加权本身在 Linux 端照常生效（只影响排序）。

**联想态 Shift+空格**：与普通空格同语义（选当前页第一个联想词上屏并继续联想），
**不**切换全/半角标点——三端实测一致（macOS/Windows 的联想分支同样先消费空格、
不判 Shift）。全/半角切换只在组词态 Shift+空格生效。

## 验证状态

`test-ci.sh` 在真实 dbus + ibus-daemon + Xvfb 下覆盖：

- **引擎注册**：原生引擎持有 `org.freedesktop.IBus.Chaoset`，Factory
  `CreateEngine` 创建独立实例；两个实例交错打字、各自上屏，状态互不串扰。
- **按键路由**：中英切换、翻页、Alt/Ctrl/Super+Backspace/Delete、
  Alt+U 放行、Ctrl+Shift+U 放行、CandidateClicked 鼠标点选。
- **daemon 自动拉起**：首键拉起 `chaosetd`，日志断言词库加载；
  用户导入词库「测试词」经 LookupTable 信号真实出现在候选列表。
- **构建**：`sh shims/linux/engine/build.sh` 用 g++/libibus 构建；
  CI 安装 `libibus-1.0-dev`。

本地可用 `./shims/linux/test-ci.sh` 复现（需要 dbus/ibus/Xvfb/Go）。

## 已知限制

- 预编辑与候选列表均经 ibus 面板显示（UpdatePreeditText /
  UpdateLookupTable 信号）；面板的具体渲染样式由 ibus 决定。
- 多 InputContext 已按实例隔离（每上下文独立会话/中英模式，
  容器内验证状态分离）。FocusOut 清组词并收起面板、FocusIn 重置
  Shift 跟踪（2026-10-26 实现，防跨窗口状态残留）；光标跟随定位
  （SetCursorLocation）待真机桌面。
- 候选窗由 ibus 原生面板显示（历史上的 daemon 贴图 candidateImage
  已随 renderer 包于 2026-10-27 删除，此壳无接入计划）。
- **候选表/预编辑语义由上游面板决定**：候选高亮固定首选（`CursorUp/Down` 空实现，
  面板内不可移动）；无候选时隐藏候选表但预编辑可独立可见；翻页箭头可用性与
  页码由面板绘制，引擎拿不到 pageCount 之外的分页状态，**单页箭头无法像
  mac/Win 那样置灰**。这些是平台机制差异，不是缺陷。
- 设置窗口读 `config.json` 失败时回退默认值；保存只更新 `associate` 并保留
  `fuzzy`/`traditional`/`shuangpin` 等其它键（原生 C 实现，避免整文件覆盖）。
  手工编辑后仍建议用编辑器的 JSON 校验（或 `jq .`）确认语法。

## 图标与设置界面

- **引擎与应用图标**：component XML 的 `<engine>` 声明 `<icon>chaoset</icon>`，
  对应 `icons/hicolor/{16x16,22x22,24x24,32x32,48x48,64x64,128x128,256x256,scalable}/apps/chaoset.{png,svg}`
  （2026-10-31 补齐 16/22/24/32/64 小尺寸 PNG，托盘/任务栏/小图标列表不再只依赖
  scalable 回退；全部由 256px 母图 LANCZOS 缩放，视觉与既有尺寸一致）。
  全套图标统一为跨端新品牌标识：极光电光蓝渐变底板 + 纯白与霓虹青「Cs」双字母标志 + 拼音声调符号（¯），在桌面环境与应用列表均保持高辨识度与现代质感。
- **设置入口**：`<engine>` 的 `<setup>` 指向 `/usr/share/chaoset/chaoset-setup`，
  ibus-setup 据此显示「首选项」按钮，点开即三端同构的设置窗口（原生 GTK3 C）。
- **设置窗单例（2026-10-31）**：改用 `Gtk.Application`（application_id
  `org.chaoset.Setup`）注册唯一实例——重复点「首选项」只会激活并前置已打开的
  窗口，不再开出多个 GTK 窗口（与 macOS/Windows 设置窗单例语义对齐）。
  系统不会自动生成配置项，设置项由该脚本读写 config.json。
  **安装包必须带这个脚本**：deb 与 tar.gz 本来就有；Arch 二进制包此前漏装
  （点「首选项」无反应），2026-10-30 已补，release 冒烟对三个包都断言该文件存在。
- 已核对 IBus 上游源码（`ibusenginedesc.c` 的 XML 解析字段表，2026-10-22）：
  `icon`、`setup`、`symbol` 等元素均受支持。

## 安装（Linux 桌面）

### 方式一：Ubuntu / Debian / Mint 用户双击一键安装（小白首选，推荐）

从 GitHub Release 下载 `chaoset_amd64.deb`：
- **图形化安装**：直接在文件管理器中**双击** `.deb` 文件，系统应用商店（Ubuntu 软件中心 / GDebi）将弹出安装窗口，点击「安装」即可。
- **终端一行安装**（可选）：
  ```sh
  sudo dpkg -i chaoset_amd64.deb
  ```
  安装程序会自动部署二进制、14万词库、ibus组件描述与图标，并自动重启 ibus-daemon。
- **卸载**：在系统应用商店中直接点击「卸载」，或执行 `sudo dpkg -r chaoset-ime`。

### 方式二：Arch Linux / Manjaro / EndeavourOS 用户（小白首选·双击或一键 pacman）

- **双击一键安装（推荐）**：
  从 GitHub Release 下载 `chaoset-ime-x86_64.pkg.tar.zst`：
  - **图形化安装**：在文件管理器中**双击**该包，使用系统软件中心（Pamac / 发现）点击「安装」；
  - **终端安装**：
    ```sh
    sudo pacman -U chaoset-ime-x86_64.pkg.tar.zst
    ```
  - **卸载**：在应用中心点击卸载，或执行 `sudo pacman -R chaoset-ime`。

- **通过 PKGBUILD 本地编译**：
  克隆仓库后进入 `shims/linux/arch/`：
  ```sh
  cd shims/linux/arch && makepkg -si
  ```

### 方式三：通用压缩包安装（其他发行版 / 脚本）

从 GitHub Release 下载 `chaoset-linux-x86_64.tar.gz`，解包后：

```sh
tar -xzf chaoset-linux-x86_64.tar.gz
cd chaoset-linux-x86_64
sudo ./install.sh          # 装二进制 + 词库 + ibus 组件 + 图标，并自检
# 卸载：sudo ./install.sh --uninstall
```

安装脚本会做四件事：

1. 二进制装到 `/usr/bin`（chaosetd / chaoset-cli / chaoset-import / chaoset-userdict / chaoset-engine），
   另在 `/usr/local/bin` 建兼容软链；
2. **常用词库**（14 万条）装到 `/usr/share/chaoset/dict.tsv`；
3. ibus 组件描述装到 `/usr/share/ibus/component/chaoset.xml`，图标装进 hicolor 主题；
4. **自检**：从 `/` 拉起一次引擎，确认它能找到词库——找不到就直接报错退出，
   不会把「装完打不出字」的问题留给用户。

装完在「设置 → 键盘 → 输入源」里添加 **Chaoset Pinyin**。列表里没有的话
执行 `ibus restart` 或注销重登。

### 方式四：从源码

```sh
make package-linux         # 产物：dist/chaoset-linux-x86_64.tar.gz
# 或只编原生引擎（需要 Linux + libibus 开发头文件）：
sh shims/linux/engine/build.sh chaoset-engine
```

### 词库路径是怎么找到的

ibus-daemon 按 component XML 的 `<exec>` 拉起原生引擎，**不保证工作目录**；
引擎把路径解析后传给自动拉起的 `chaosetd`（真正加载词库的是 Go daemon）。
主词库查找顺序（命中即用）：

1. 命令行 `-dict`（测试/高级用法）；
2. `/usr/share/chaoset/dict.tsv`（安装包部署位置）；
3. 可执行文件同级 `../data/dict.tsv`（解包即用的绿色形态）；
4. 源码树 `data/dict_common.tsv`；
5. **用户导入词库（可选）**：`$XDG_DATA_HOME/chaoset/dict.tsv`
   （缺省 `~/.local/share/chaoset/dict.tsv`），设置窗「导入词库…」写入；
   引擎用 `-imported-dict` 传给 daemon，文件不存在则忽略。

安装器自检仍会检查主词库缺失并报错，避免「装完打不出字」。
