# Linux 平台壳（ibus 原生 C++）

形态：**C++/libibus 薄壳 + Rust `chaosetd` 进程外引擎**：

- `shims/linux/engine/`：原生 `IBusEngine`，只做按键路由、预编辑/LookupTable/
  辅助文本等平台 UI；拼音、词库、学习逻辑全部在 daemon。
- `chaosetd`（纯 Rust，`crates/chaosetd`）：经 Unix socket 提供 `Session` 协议；
  首次按键时引擎自动拉起。
- 引擎使用 ibus 私有 D-Bus 总线（`IBUS_ADDRESS`），通过 `ibus_bus_new()`/
  `IBusFactory` 注册；不能直接连 session bus。
- 选择 libibus 而不是 fcitx5 addon：后者的引擎是加载进 fcitx5 守护进程的
  `.so`，与“引擎独立进程”架构冲突。

## 目录

```
engine/
  main.cpp   原生 IBusEngine：Factory、ProcessKeyEvent、预编辑、候选表、联想
  daemon.cpp Unix socket、XDG 路径、按需拉起 chaosetd
  build.sh   g++ + pkg-config ibus-1.0（C++17）
setup/
  chaoset-setup.c  设置入口启动器（纯 POSIX C）：查找并 exec 统一设置中心 chaoset-settings
  build.sh         编译启动器（cc -std=c11，无 GTK/pkg-config 依赖）
test-ci.sh   CI 端到端脚本（真实 dbus + ibus-daemon + Xvfb；含设置启动器冒烟）
setup-container.sh  可复现的本地容器验证环境
```

设置界面唯一实现是 Slint 统一设置中心（`shims/settings/`，构建产物
`chaoset-settings`），本目录只剩它的启动器；不再有 Linux 原生 GTK3 设置窗。

## 行为

- 字母组词；`1`-`9`（含小键盘）选候选，越界序号放行原键。
- 空格/回车首选上屏；残缺组词顶屏原始拼音；有已确认段时拼接后一次上屏。
- 退格逐字符删除；Delete 同语义；Shift/Ctrl/Super+退格/Delete、Ctrl+U 清空；
  Alt+退格/Delete 按音节回退；Esc 取消整条组词（含未落盘确认段）。
- `,` `.` `;` 等标点走中文标点；组词中的 `'` 是音节分隔符；
  句号/逗号连按全角⇄半角循环；`[` `]` 输出【】。
- 方向键预选/翻页：候选 lookup 显示时上/下=翻页（翻页后 cursor 归零、边界页保持
  不动），左/右=同一页内移动 lookup cursor（页边界保持不动），空格确认 cursor
  候选；无候选时方向键放行给宿主。翻页箭头由 ibus 面板按「上一页/下一页」绘制，
  壳不控制其可用性显示（见「已知限制」）。
- 翻页：`-` `=`、`[` `]`、Page Up/Down；残缺组词时清空后放行。
- 中英切换：Shift 空按；Shift 释放事件放行给应用；英文模式全透传。
- 全/半角标点：Shift+空格（仅组词态）；状态由壳本地持有，随标点请求下发。
- 预编辑带整串下划线与末尾光标位置；模式切换经辅助文本短暂提示。
- 双拼：`-shuangpin mspy` 时 `;` 是 ing 韵母键；非双拼/Shift+`;` 仍输出标点。
- 已确认段：失焦、Shift 切中英、修饰键透传、翻页失败等收尾路径先落盘。
- 联想：选词后按音节查续接词组；候选表上限 30 条、每页 6 条；数字/空格/
  Enter/Esc/字母与组词态翻页键语义统一；越界数字收起联想后放行。
- 学习标记 `*`：ibus LookupTable 没有独立装饰层，不追加到候选文本；学习加权
  仍照常影响排序。
- 候选右键删除：ibus 面板不提供上下文菜单回调，**不适用**；按词删除用
  `chaosetctl userdict delete`，整体清空用设置中心或 `chaosetctl userdict clear`
  （安装包内 `chaoset`/`chaoset-cli`/`chaoset-import`/`chaoset-userdict` 都是同一
  `chaosetctl` 二进制的软链）。
- 候选栏设置齿轮：Linux 不自绘候选窗/控制区，候选表与预编辑都由 ibus 面板
  渲染，壳拿不到齿轮的绘制与命中矩形（ibus 面板也没有「设置」按钮回调），
  因此**齿轮不适用**；设置入口是 ibus-setup 的「首选项」或
  `/usr/share/chaoset/chaoset-setup`（启动器，见「设置与词库」）。macOS/Windows
  候选栏的齿轮 mouseUp 契约只约束这两端。
- 候选点选/翻页的鼠标语义（按下还是抬起触发）由 ibus 面板控制：壳只收到
  `CandidateClicked` 回调，无法也不应改变系统面板的按键/鼠标语义；macOS/Windows
  自绘候选窗的「按下只记录、抬起才触发」契约只约束这两端。

## 设置与词库

- 设置统一由 **Slint 统一设置中心 `chaoset-settings`** 提供（`shims/settings/`，
  三端同一份实现，安装到 `/usr/bin/chaoset-settings`）。
- 应用菜单入口：安装包会装 `chaoset-settings.desktop`（Icon=chaoset），可直接搜索/点击带图标的「Chaoset 设置」。
- 设置入口：ibus-setup 中选中 Chaoset →「首选项」，或运行
  `/usr/share/chaoset/chaoset-setup`。后者只是**纯 POSIX C 启动器**：按
  PATH → `/usr/bin/chaoset-settings` → 自身同目录 的顺序查找并 `execv`
  设置中心，参数原样转交；不含 UI、不链接 GTK。找不到时在 stderr 报
  「未找到统一设置中心（chaoset-settings）」并以 1 退出，**不回退任何旧设置窗**。
- 配置写入 `$XDG_CONFIG_HOME/chaoset/config.json`（冷路径经
  `chaosetctl config get/set`，schema/默认值/校验/持久化的唯一真源是 Rust
  `crates/chaoset-config`）；引擎每次进入联想前重读 `associate`，保存即生效。
  手工编辑后建议 `jq .` 校验。
- 外观主题：设置中心「输入」页「外观主题」提供「跟随系统 / 浅色 / 深色」
  （config.json 键 `theme`，取值 `system`/`light`/`dark`，缺省 `system`）。
  该设置作用于设置中心自身的浅/深外观；候选窗/预编辑由 ibus 面板渲染，颜色
  完全由面板/桌面主题决定，壳不设置候选/预编辑颜色，也没有自定义颜色资源。
- 学习数据：统计/清空/导出都在设置中心（调用 `chaosetctl userdict`）。
  清空/按词删除经 IPC 由 daemon 在响应前同步落盘（内存与磁盘一次生效），
  **不要先停 daemon**；只有显式加 `-userdict FILE` 的本地模式才直接在 daemon
  之外改文件（daemon 运行时会被其内存态覆盖）。
- 词库导入：设置中心选择 Rime dict.yaml / CC-CEDICT txt / TSV，调用
  `chaosetctl import -o <用户导入词库> -backup` 合并（备份 + 原子替换）。
  daemon 启动时以 `-imported-dict` 加载用户导入词库；导入后重启 daemon
  （`pkill chaosetd`，下一次按键自动拉起）即可用上新词。
- 图标：component XML 声明 `chaoset`，hicolor 提供 16/22/24/32/48/64/128/256
  与 scalable 图标（ibus 面板与系统设置用）。设置启动器与 `chaoset-settings`
  都必须随包安装，否则「首选项」只会报错。

## 安装

发行包（tar.gz / deb / Arch）的 `bin/` 内容一致：真实文件 `chaosetctl`、`chaosetd`、
`chaoset-settings`、`chaoset-engine`；`chaoset` / `chaoset-cli` / `chaoset-import` /
`chaoset-userdict` 是指向 `chaosetctl` 的软链（单二进制 multicall）。整条构建/打包链
不依赖 Go 构建（词库生成 `chaoset-dictgen` 只在仓库构建期使用）。

### Debian / Ubuntu / Mint

从 Release 下载 `chaoset_amd64.deb`，双击用软件商店安装，或：

```sh
sudo dpkg -i chaoset_amd64.deb
# 卸载：sudo dpkg -r chaoset-ime
```

### Arch / Manjaro / EndeavourOS

从 Release 下载 `chaoset-ime-x86_64.pkg.tar.zst`，双击或用 Pamac 安装，或：

```sh
sudo pacman -U chaoset-ime-x86_64.pkg.tar.zst
# 卸载：sudo pacman -R chaoset-ime
```

本地构建 PKGBUILD：`cd shims/linux/arch && makepkg -si`。

### 通用压缩包

```sh
tar -xzf chaoset-linux-x86_64.tar.gz
cd chaoset-linux-x86_64
sudo ./install.sh          # 二进制/词库/ibus 组件/图标 + 自检
sudo ./install.sh --uninstall
```

安装后可在「设置 → 键盘 → 输入源」添加 **Chaoset Pinyin**；列表未刷新时
执行 `ibus restart` 或注销重登。

### 从源码

```sh
make build-settings        # Slint 统一设置中心（package-linux 必需，缺了打包直接失败）
make package-linux         # dist/chaoset-linux-x86_64.tar.gz
sh shims/linux/engine/build.sh chaoset-engine   # 只编引擎，需要 libibus 开发头文件
sh shims/linux/setup/build.sh chaoset-setup     # 只编设置启动器（普通 cc 即可）
```

## 词库路径

引擎按以下顺序查找主词库（命中即用）：

1. 命令行 `-dict`；
2. `/usr/share/chaoset/dict.tsv`；
3. 可执行文件同级 `../data/dict.tsv`；
4. 源码树 `data/dict_common.tsv`。

用户导入词库在 `$XDG_DATA_HOME/chaoset/dict.tsv`（默认
`~/.local/share/chaoset/dict.tsv`），由设置中心「词库 → 导入词库…」写入，
daemon 以 `-imported-dict` 加载；文件不存在时忽略。

## 日志

- 引擎/壳 stderr 前缀为 `chaoset-engine: `，包含 daemon 未就绪、IPC 失败、
  词库/配置加载失败等；由 ibus/systemd 收集：
  `journalctl --user -b | grep chaoset-engine`。
- daemon 启动构建、词库加载、用户词典落盘、每条 IPC 连接与请求错误写入
  `~/.local/share/chaoset/chaosetd.log`。

## 验证

`test-ci.sh` 在真实 dbus + ibus-daemon + Xvfb 下覆盖：

- 引擎注册和两个实例交错打字/上屏/状态隔离；
- 中英切换、翻页、修饰键退格、Alt+U 与 Ctrl+Shift+U 放行、鼠标点选；
- 首次按键拉起 daemon、词库加载；
- 导入词真实出现在 LookupTable 候选；
- 设置启动器（`chaoset-setup`）exec `chaoset-settings` 并原样转交参数，
  且不链接 GTK（设置界面唯一实现在 Slint 设置中心）。

```sh
./shims/linux/test-ci.sh    # 需要 dbus/ibus/Xvfb（Rust 与 C++ 构建，不再需要 Go）
```

## 已知限制

- 候选表/预编辑由 ibus 面板渲染；高亮是 ibus lookup cursor（左/右移动预选、上/下翻页，
  空格确认），但「双箭头可用性」由 ibus 面板绘制：壳拿不到箭头绘制/命中回调，
  无法像 macOS/Windows 那样按首页/末页控制箭头显隐；候选分页状态由面板决定。
- 多 InputContext 各自独立会话/中英模式；FocusOut 清组词并收起面板，
  FocusIn 重置 Shift 跟踪。
- 候选右键删除不支持（见上）；学习标记 `*` 不显示。
- 设置中心冷路径读写设置（`chaosetctl config get/set`）：文件缺失/为空按缺省值，
  JSON 损坏时报错而不是静默覆盖；保存只改对应键、保留文件里其它键（Rust
  `crates/chaoset-config` 保证）。
- 设置中心不暂停 daemon：词库导入后由设置中心自动重启 daemon 生效；清空学习数据
  经 IPC 立即落盘、无需停 daemon。
- 光标跟随依赖桌面/ibus 面板实现，虚拟终端、Wayland 合成器等环境可能表现不同。
