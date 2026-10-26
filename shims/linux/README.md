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

1. 二进制装到 `/usr/local/bin`（chaosetd / chaoset-cli / chaoset-import / chaoset-userdict / chaoset-engine）；
2. **常用词库**（14 万条）装到 `/usr/share/chaoset/dict.tsv`；
3. ibus 组件描述装到 `/usr/share/ibus/component/chaoset.xml`，图标装进 hicolor 主题；
4. **自检**：从 `/` 拉起一次引擎，确认它能找到词库——找不到就直接报错退出，
   不会把「装完打不出字」的问题留给用户。

装完在「设置 → 键盘 → 输入源」里添加 **Chaoset Pinyin**。列表里没有的话
执行 `ibus restart` 或注销重登。

### 方式二：从源码

```sh
make package-linux         # 产物：dist/chaoset-linux-x86_64.tar.gz
# 或只编引擎：
GOOS=linux GOARCH=amd64 go build \
  -ldflags "-X main.buildTimeDict=/usr/share/chaoset/dict.tsv" \
  -o chaoset-engine ./shims/linux/ibus-engine/
```

### 词库路径是怎么找到的

ibus-daemon 按 component XML 的 `<exec>` 拉起引擎，**不保证工作目录**，
所以引擎不能依赖相对路径。查找顺序（命中即用）：

1. 编译期注入的绝对路径（打包时 `-ldflags -X main.buildTimeDict=...`，默认 `/usr/share/chaoset/dict.tsv`）；
2. 可执行文件同级 `../data/dict.tsv` 等（解包即用的绿色形态）；
3. `/usr/share/chaoset/dict.tsv`（安装包部署位置）；
4. `data/dict_common.tsv`（在仓库里直接 `go run` 时）。

> 2026-10-25 修的发行缺陷：此前默认值写死为相对路径 `data/dict_common.tsv`，
> 而发布包里只有 558 条的 `dict_seed.tsv`。结果是装完引擎立刻报
> `打开词库 data/dict_common.tsv 失败` 并退出——ibus 里选了输入法却一个字
> 都打不出来。现在包里带完整词库，且路径按上述顺序回退。
