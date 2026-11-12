# macOS 平台壳（ChaosetIME）

InputMethodKit 薄壳：只负责按键转发、文本上屏与原生候选窗；拼音、词库、学习和
联想全部在 `chaosetd`。daemon 不可达时按键原样透传，不阻塞宿主应用。

## 安装布局

安装后用户目录只有一个输入法 app：

```
~/Library/Input Methods/ChaosetIME.app/
  Contents/Resources/chaosetd
  Contents/Resources/chaoset-import
  Contents/Resources/dict_common.tsv
  Contents/Resources/ChaosetIME-input.tiff
```

- daemon/导入工具/种子词库都在 app 内，LaunchAgent 指向 app 内 daemon；
- `~/Library/Application Support/chaoset/` 放用户数据：`dict.tsv`、`userdict.json`、
  `chaoset.sock`、`chaosetd.log`；
- 安装器 `ChaosetSetup.app` 同时把「Chaoset 安装与设置」装到 `~/Applications/Chaoset.app`，
  设置窗的「导入词库」入口调用它；
- 发布包是 **universal**（arm64 + x86_64），ad-hoc 签名，不需要 Apple 开发者账号，
  不使用 DMG。

`dict.tsv` 是 daemon 实际加载的词库。安装/升级时不能盲目保留或覆盖：

- 首行是内置词库头 → 未导入过的纯种子副本，替换为新内置词库（先备份）；
- 首行是 `chaoset-import` 输出头 → 用 `chaoset-import -replace` 合并新内置词库，
  用户新增词保留；
- `dict.builtin.sha256` 记录已合并的内置版本，版本未变则跳过。

## 构建与安装

### 一行命令安装

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/ShanWuYinShe/chaoset_ime/main/shims/macos/install-online.sh)"
```

脚本下载 Release 的 `chaoset-macos.zip`，用 `ditto` 解包并移除 quarantine，把
`ChaosetSetup.app` 放到 `~/Applications` 后打开；在窗口中点「一键安装 / 更新」。
不需要 `sudo`，可指定本地包：

```bash
CHAOSET_ZIP=/path/to/chaoset-macos.zip CHAOSET_NO_LAUNCH=1 bash install-online.sh
```

### 手动安装

从 Release 下载 `chaoset-macos.zip` 解压，运行 `ChaosetSetup.app`，点
「一键安装 / 更新」，再按 `Ctrl + 空格`（或 `Fn / 🌐`）切换。

> 不要直接双击 `ChaosetIME.app`：它是安装后的输入法本体，不是安装器。
> 浏览器下载的 app 带 `com.apple.quarantine` 时，右键安装器 →「打开」。

安装器只通过 `ime.chaoset.daemon` LaunchAgent 管理 daemon，不会额外启动第二个
`chaosetd`。

### 从源码

```sh
./shims/macos/build.sh     # build/ChaosetIME.app、build/ChaosetSetup.app、build/tis、build/smoke
./shims/macos/install.sh   # 装入 ~/Library/Input Methods
./shims/macos/reregister.sh # 普通终端尽力登记并启用（不保证免注销）
./shims/macos/verify.sh     # TIS/daemon/IPC 全链路自检
```

`build.sh` 默认构建 universal（`ARCHS="arm64 x86_64"`），本地迭代可用
`ARCHS=arm64 ./shims/macos/build.sh`。图标由 `tools/make-icon.swift` 现场绘制，
不依赖外部素材。

### 登记幂等（重复安装/升级不得重复 TISRegisterInputSource）

TIS 没有公开的反注册 API：同一 bundle 每多调用一次 `TISRegisterInputSource`，**当前
登录会话**的输入源表就追加一条记录——系统设置/输入源菜单出现多行「Chaoset」，而且
只有**注销重登**才能重建输入源表。因此：

- `tis register` 先按 bundle id 查 TIS 表，**已登记则跳过注册**（幂等）；
- `install.sh`、GUI 安装器（`setup/main.swift`）、`reregister.sh` 统一走随包
  `tis register`，不得无条件直接调用 `TISRegisterInputSource`；
- 禁止用 `tis list | grep` 预判「是否已登记」再决定注册：TIS 缓存每进程一份，受限/
  沙盒进程读到的表可能为空，会误判成未登记而重复注册（那正是重复条目的根因）；
- `tis dedupe` 只对同一 ID 的多条记录做尽力合并（部分系统版本无效），兜底仍是注销重登。

契约由 `make check-uiparity` 的「macOS 输入源登记幂等」组机器断言。

## 输入源标识与显示名

| 项 | 值 |
|---|---|
| `CFBundleIdentifier` / `TISInputSourceID` | `ime.chaoset.inputmethod.ChaosetIME`（两者必须一致；id 中间必须含 `.inputmethod.` 段） |
| 输入模式 | `ime.chaoset.inputmethod.ChaosetIME.Pinyin`（父 ID + `.Pinyin`） |
| 显示名 | **Chaoset**（中英同名），由 `Resources/{en,zh-Hans,zh_CN}.lproj/InfoPlist.strings` 提供 |
| 应用图标 | `ChaosetIME.icns`：极光电光蓝底板 + 白/霓虹青「Cs」+ 声调符号 + PINYIN 字标 |

`.Hans` 是旧输入模式别名，`install.sh` 会禁用并清理残留。

### 输入源图标（已确认，禁止回退）

**统一为 22×16 宽扁 keycap**：满幅实心圆角按键 + alpha 挖空单字符「C」，双帧
模板 TIFF（22×16@72dpi + 44×32@144dpi、RGB 全黑、ExtraSamples=2、LZW），
五个 TIS 图标键全部指向 `ChaosetIME-input.tiff`。

macOS 第三方输入法只有 bundle 级与 mode 级两套图标键：

- System Settings 读取 bundle 级图标；
- 菜单栏/系统栏与快捷键切换 HUD 共用 mode 级图标；
- 没有第三套键把「菜单栏」和「HUD」再分开，因此统一用带边框 keycap，
  不做「HUD 无边框」变体。

修改图标必须同时保持：

1. `tools/make-icon.swift` 与 `build.sh` 的图标键映射；
2. `./shims/macos/tests/equivalence.sh` 的 keycap 契约与五键一致断言；
3. `./shims/macos/build/icon-size` 的 System Settings 渲染尺寸比对。

**禁止改测试或降低断言来通过。**

## 约定与行为

- 套接字：环境变量 `CHAOSET_SOCKET` → `~/Library/Application Support/chaoset/chaoset.sock`；
  daemon 必须用相同 `-socket` 启动。连接失败有 1 秒冷却重试。
- 候选窗：AppKit 原生自绘，键帽序号、页内预选蓝胶囊（方向键移动、空格确认）、
  上下翻页箭头（首页只有下箭头、末页只有上箭头、中间页两枚、单页置灰不可点）、
  学习标记 `*`、页码指示、鼠标点选、VoiceOver 读取；跟随光标与屏幕边缘防溢出。
- 按键：
  - a-z / `'` 组词；1-9 选择本页候选（每页 6 个，越界放行）；
  - 空格确认页内预选项（默认首选）并渐进上屏；回车确认段+原始拼音一次上屏；
  - 方向键移动页内预选（上/左=前一项、下/右=后一项；页首继续上移翻上一页选末项、
    页尾继续下移翻下一页选首项；无组词/无候选时放行宿主）；
  - `-` `=` `[` `]` PageUp/PageDown 翻页；无候选时 `[` `]` 是标点；
  - Shift 空按切中英（已确认段先落盘）；Shift+空格在组词态切全/半角；
  - 双拼 mspy 的 `;` 是 ing 韵母键，非双拼/Shift+`;` 仍输出标点；
  - 退格逐字符，Option+Delete 按音节，Shift/Ctrl+Delete 取消整条；
  - Esc 取消整条组词。
- 预编辑经 `setMarkedText` 展示；组词结束清标记区，兼容忽略空插入的客户端。

## LaunchAgent

`~/Library/LaunchAgents/ime.chaoset.daemon.plist` 管理 daemon：

```xml
<key>Label</key><string>ime.chaoset.daemon</string>
<key>ProgramArguments</key>
<array>
  <string>.../ChaosetIME.app/Contents/Resources/chaosetd</string>
  <string>-socket</string><string>.../chaoset.sock</string>
  <string>-dict</string><string>.../dict.tsv</string>
  <string>-userdict</string><string>.../userdict.json</string>
  <string>-log</string><string>.../chaosetd.log</string>
</array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ProcessType</key><string>Interactive</string>
```

`ProcessType=Interactive` 必须保留：默认 Background 调度会在高负载下节流 daemon，
造成打字卡顿/丢首字母。安装器会在写入 plist 后用 `grep` 断言该键。

输入法进程本身由系统 IMK 拉起，不再安装第二个常驻 LaunchAgent；
`install.sh` 会清理旧的 `ime.chaoset.inputmethod` 残留。

## 设置窗口

候选栏右侧齿轮 →「Chaoset 设置」：

- 输入行为：词组联想开关，保存即生效；
- 学习数据：调频词/用户词组/上下文统计、清空（二次确认）、导出 TSV；
  经 `EngineClient` IPC 操作 daemon，内存与磁盘一次生效；
- 词库：打开 `~/Applications/Chaoset.app --import`，由 Setup.app 弹出文件选择，
  合并 → 备份 → 原子替换 → 重启 daemon；
- 底部显示 `Chaoset 版本 x.y.z`。

设置窗与 macOS 输入法进程同源，不持有 TIP 裸指针；关闭联想通过通知让已显示
联想候选立即收起。

## 日志

- daemon：`~/Library/Application Support/chaoset/chaosetd.log`，含启动构建、
  词库加载、用户词典路径、每条 IPC 连接与请求错误。
- 壳侧：连接失败、IPC 协议/业务错误、候选窗创建失败写 macOS 统一日志，
  按 2 秒节流：

```sh
log show --last 10m --predicate 'subsystem == "ime.chaoset.inputmethod"' --info --debug
```

排障先跑 `.../Contents/Resources/chaosetd -version`，再对照 daemon 与统一日志。

## 卸载

打开「Chaoset 安装与设置」→「卸载」，会停止 daemon、移除输入法 app 与安装器，
保留 `Application Support` 中的学习数据/词库。命令行兜底：

```sh
launchctl bootout gui/$(id -u)/ime.chaoset.daemon
rm -f ~/Library/LaunchAgents/ime.chaoset.daemon.plist
rm -rf ~/Library/Input\ Methods/ChaosetIME.app
rm -rf ~/Applications/Chaoset.app ~/Applications/ChaosetSetup.app
```

## 验证

- `shims/macos/tests/key_routing/main.swift`：按键路由/组词/上屏行为；
- `shims/macos/tests/equivalence.sh`：图标契约、universal 架构、TIS 图标键；
- `shims/macos/tests/visual-parity.sh`：macOS 候选窗布局与配色常量；
- `build/smoke`：真实 daemon 的 IPC 组词/上屏/退格/重置；
- `shims/macos/verify.sh`：装入、TIS 枚举、daemon、IPC 全链路。

## 已知限制

- 候选窗跟随光标依赖 `attributes(forCharacterIndex:lineHeightRectangle:)`；个别
  Electron 应用或远程桌面返回零矩形时退回鼠标位置。
- VoiceOver 提供候选文字与窗口级信息；逐条候选子项动作未实现。
- 首项超长候选按字符截断加「…」，面板内容宽 ≤638pt、窗宽 ≤720pt；存储与
  VoiceOver 文本仍完整。
- 修改 app 后重装会清 TIS 缓存；若系统设置一直开着，关闭再打开才能看到新列表。
  已添加的输入法在列表中呈灰色表示已启用，不能再添加一次。
- 打字闭环的真机交互（GUI 点选、注销重登）需人工验收，脚本化测试不能完全替代。
