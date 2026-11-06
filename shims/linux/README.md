# Linux 平台壳（ibus 原生 C++）

形态：**C++/libibus 薄壳 + Go `chaosetd` 进程外引擎**：

- `shims/linux/engine/`：原生 `IBusEngine`，只做按键路由、预编辑/LookupTable/
  辅助文本等平台 UI；拼音、词库、学习逻辑全部在 daemon。
- `chaosetd`（纯 Go）：经 Unix socket 提供 `Session` 协议；首次按键时引擎自动拉起。
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
test-ci.sh   CI 端到端脚本（真实 dbus + ibus-daemon + Xvfb）
setup-container.sh  可复现的本地容器验证环境
```

## 行为

- 字母组词；`1`-`9`（含小键盘）选候选，越界序号放行原键。
- 空格/回车首选上屏；残缺组词顶屏原始拼音；有已确认段时拼接后一次上屏。
- 退格逐字符删除；Delete 同语义；Shift/Ctrl/Super+退格/Delete、Ctrl+U 清空；
  Alt+退格/Delete 按音节回退；Esc 取消整条组词（含未落盘确认段）。
- `,` `.` `;` 等标点走中文标点；组词中的 `'` 是音节分隔符；
  句号/逗号连按全角⇄半角循环；`[` `]` 输出【】。
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
  `chaoset-userdict delete`，整体清空用设置窗或 `chaoset-userdict clear`。
- 候选栏设置齿轮：Linux 不自绘候选窗/控制区，候选表与预编辑都由 ibus 面板
  渲染，壳拿不到齿轮的绘制与命中矩形（ibus 面板也没有「设置」按钮回调），
  因此**齿轮不适用**；设置入口是 ibus-setup 的「首选项」或
  `/usr/share/chaoset/chaoset-setup`（见「设置与词库」）。macOS/Windows
  候选栏的齿轮 mouseUp 契约只约束这两端。
- 候选点选/翻页的鼠标语义（按下还是抬起触发）由 ibus 面板控制：壳只收到
  `CandidateClicked` 回调，无法也不应改变系统面板的按键/鼠标语义；macOS/Windows
  自绘候选窗的「按下只记录、抬起才触发」契约只约束这两端。

## 设置与词库

- 设置入口：ibus-setup 中选中 Chaoset →「首选项」，或运行
  `/usr/share/chaoset/chaoset-setup`（原生 GTK3 C）。
- 设置窗为 `Gtk.Application` 单例（`org.chaoset.Setup`），重复点击激活已有窗口。
- 配置写入 `$XDG_CONFIG_HOME/chaoset/config.json`；引擎每次进入联想前重读
  `associate`，保存即生效。手工编辑后建议 `jq .` 校验。
- 学习数据：统计/清空/导出都在设置窗；实现调用 `chaoset-userdict` 本地文件模式，
  engine 包不引入平台代码。清空时先停 daemon/引擎，再清空并重启 ibus，
  防止内存旧状态回写。
- 词库导入：设置窗选择 Rime dict.yaml / CC-CEDICT txt / TSV，调用
  `chaoset-import` 合并到 `$XDG_DATA_HOME/chaoset/dict.tsv`（备份 + 原子替换），
  重启 ibus 引擎后由 daemon `-imported-dict` 加载。
- 图标：component XML 声明 `chaoset`，hicolor 提供 16/22/24/32/48/64/128/256
  与 scalable 图标；设置窗二进制必须随包安装，否则「首选项」无响应。

## 安装

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
make package-linux         # dist/chaoset-linux-x86_64.tar.gz
sh shims/linux/engine/build.sh chaoset-engine   # 只编引擎，需要 libibus 开发头文件
```

## 词库路径

引擎按以下顺序查找主词库（命中即用）：

1. 命令行 `-dict`；
2. `/usr/share/chaoset/dict.tsv`；
3. 可执行文件同级 `../data/dict.tsv`；
4. 源码树 `data/dict_common.tsv`。

用户导入词库在 `$XDG_DATA_HOME/chaoset/dict.tsv`（默认
`~/.local/share/chaoset/dict.tsv`），由设置窗写入，daemon 以
`-imported-dict` 加载；文件不存在时忽略。

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
- 导入词真实出现在 LookupTable 候选。

```sh
./shims/linux/test-ci.sh    # 需要 dbus/ibus/Xvfb/Go
```

## 已知限制

- 候选表/预编辑由 ibus 面板渲染；高亮固定首选（面板内不可移动），单页箭头
  无法像 macOS/Windows 那样置灰；候选分页状态由面板决定。
- 多 InputContext 各自独立会话/中英模式；FocusOut 清组词并收起面板，
  FocusIn 重置 Shift 跟踪。
- 候选右键删除不支持（见上）；学习标记 `*` 不显示。
- 设置窗读坏 JSON 时回退默认值；保存只更新 `associate`，保留其他配置键。
- 光标跟随依赖桌面/ibus 面板实现，虚拟终端、Wayland 合成器等环境可能表现不同。
