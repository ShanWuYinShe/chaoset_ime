# macOS 平台壳（ChaosetIME）

InputMethodKit 薄壳：只做「按键转发 + 文本上屏 + 候选窗展示」，全部组词逻辑
在 chaosetd。daemon 未运行时所有按键原样透传，不会影响宿主应用输入。

## 按键

| 按键 | 行为 |
|---|---|
| a-z / ' | 进入或追加组词 |
| 1-9 | 选择对应候选上屏 |
| 空格 / 回车 | 首选候选上屏（无候选时放行原键） |
| `-` / `=` | 候选上一页 / 下一页（有候选时；边界由 daemon 钳制） |
| Shift 空按 | 切换中英（英文模式全透传）。IMK 对修饰键事件的分发待真机验证 |
| Shift+空格 | 切换全角/半角标点（半角下标点原样输出） |
| 标点键 `,` `.` `;` 等 | 输出中文标点（组词中先上屏首选候选）；组词中的 `'` 为音节分隔符 |
| 退格 | 删除一个输入字母 |
| Esc | 清空组词 |

## 套接字约定

壳按以下顺序找 daemon：环境变量 `CHAOSET_SOCKET` →
`~/Library/Application Support/chaoset/chaoset.sock`。
daemon 必须用相同路径启动（`-socket`）。连接失败有 1 秒冷却重试。

## 构建与安装

```sh
./build.sh          # 产出 build/ChaosetIME.app、build/chaosetd 与验证工具 build/tis、build/smoke
                    # 图标由 tools/make-icon.swift 现场绘制，不依赖任何外部素材

# 1. 一键安装：输入法 app（移除旧版→拷入→lsregister→TIS 登记）
#    + daemon 与种子词库 + 输入法进程常驻 LaunchAgent
#    （daemon/输入法已在跑的话会重新拉起新版）
./install.sh

# 2. 若没有配 LaunchAgent，手动前台启动 daemon 先试通
"$HOME/Library/Application Support/chaoset/chaosetd" \
  -socket "$HOME/Library/Application Support/chaoset/chaoset.sock" \
  -dict "$HOME/Library/Application Support/chaoset/dict.tsv" \
  -userdict "$HOME/Library/Application Support/chaoset/userdict.json"

# 3a.（可选，免重登尝试）在普通终端里尽力登记并启用；沙盒进程做不了这件事
./reregister.sh
#     它会：重新登记 → 查系统输入源表是否可见 → 可见则 enable + select。
#     不可见就说明依旧只能走"注销重登"。

# 3b. 一键自检（装入 / TIS 枚举 / daemon / IPC 全链路四段结论）
./verify.sh
#    单独看枚举也可：./build/tis list | grep -i chaoset
#    期望出现 ime.chaoset.inputmethod.ChaosetIME 与 ime.chaoset.inputmethod.ChaosetIME.Hans 两行

# 5. 系统设置 → 键盘 → 输入法 → 添加「Chaoset」，切到它开始打字
```

> ℹ️ **2026-10-21 更正：bundle 有效时不需要注销重登。** 装好并调用
> `TISRegisterInputSource` 后，TIS 立即枚举到本输入法，系统设置「输入法」
> 里直接可添加、可切换（当日实测）。此前"新增输入法只能靠登录扫描"的结论是
> 误判：当时 bundle id 以 `inputmethod` 结尾，TIS 审讯不通过——注册返回 noErr
> 但不会收录（根因与修法见「输入源标识与显示名」）。历史观察（2026-10-06，
> 对象是无效 bundle）保留如下：`TISRegisterInputSource`、`lsregister -f`、
> `killall TextInputMenuAgent`、直接 `open` 都不会让它进入输入源列表。

## 输入源标识与显示名

| 项 | 值 |
|---|---|
| `CFBundleIdentifier` / `TISInputSourceID` | `ime.chaoset.inputmethod.ChaosetIME`（**两者必须一致**；bundle id 必须含内层 `.inputmethod.` 段，见下） |
| 输入模式（`ComponentInputModeDict.tsInputModeListKey`） | `ime.chaoset.inputmethod.ChaosetIME.Pinyin`（父 ID + `.Pinyin`，采用标准拼音模式命名避免与系统合成模式冲突） |
| 显示名 | **Chaoset**（中英同名，无中文译名），由 `Resources/{en,zh-Hans,zh_CN}.lproj/InfoPlist.strings` 按上面两个 ID 提供 |
| 图标 | 采用双层设计：输入源图标为系统同款纯字形模板图 `ChaosetIME-icon2.tiff`（16×16 双帧 TIFF、无外框纯字母「C」模板，透明背景，与系统 ABC / 拼 音 在悬浮输入法切换器及菜单栏中风格完全一致）；应用图标 `ChaosetIME.icns` 采用极光电光蓝全幅底板 + 纯白与霓虹青「Cs」双字母标志 + 拼音声调符号（¯），用于访达/启动台/活动监视器/系统设置的应用列表。 |
| 图标生成 | `tools/make-icon.swift` 现场绘制，不依赖外部素材。**改图标后必须重装并让 TIS 重建缓存**（`./install.sh` 已包含注册与重试）。 |

> ⚠️ **bundle id 必须含内层 `.inputmethod.` 段**（前后都还有别的段），例如
> `im.rime.inputmethod.Squirrel`、`app.msime.inputmethod.MetasequoiaIME`。
> 仅以 `inputmethod` 结尾（如曾经的 `ime.chaoset.inputmethod`）**不会生效**：
> 2026-10-21 本机（macOS 15.8.1）重启登录复验，TIS 输入源缓存（138,876 B，
> 与纯系统表等大）里仍无该 identifier，系统设置也看不到；这正是此前
> 「plist 键逐一对齐 Squirrel 仍不收录」的真正原因。依据：
> `TextInputSources.h`（InputSourceID 推导规则含 "ends in `.inputmethod.`"）、
> rdar://21376022（结尾写法实测无效、改到中间才生效）、SO 8960016。
>
> ⚠️ 系统「输入法」列表里若出现**鼠须管**，那是官方 Rime/Squirrel 应用
> （`im.rime.inputmethod.Squirrel`）。2026-10-05 排障时曾把官方 Squirrel 1.1.2
> 下载、ad-hoc 重签后放进 `~/Library/Input Methods/SquirrelAdhoc.app`，用作
> 「同目录第三方输入法能否被枚举」的对照样本；它**不是本项目**，其源码、图标与
> 名称都不得复用。本项目自身可被枚举后即可删除该样本。

## 开机自启（LaunchAgent）

本机 macOS 15.8.1 实测存在一个 IMK 侧限制：**imklaunchagent 无法按需拉起本输入法**
（统一日志：`LaunchInputMethod() Error, status=-50`；且 `man imklaunchagent` 写明它会
对"短时间内反复异常退出"的输入法**直接禁止拉起**——调试期反复 `killall` 会踩到）。
表现就是输入菜单里选中本输入法后**打字完全没反应**（进程没被拉起，按键被吞）。
规避方式：**让输入法进程常驻**（实测常驻时客户端能正常 Activate Server，打字正常）。
因此 `install.sh` 会额外安装两个 LaunchAgent：

- `ime.chaoset.inputmethod` → 直接运行 `ChaosetIME.app/Contents/MacOS/ChaosetIME`（常驻，KeepAlive）；
- `ime.chaoset.daemon` → 引擎 daemon（见下）。

`~/Library/LaunchAgents/ime.chaoset.daemon.plist`（把路径里的 `you` 换成实际值）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>ime.chaoset.daemon</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Users/you/Library/Application Support/chaoset/chaosetd</string>
		<string>-socket</string>
		<string>/Users/you/Library/Application Support/chaoset/chaoset.sock</string>
		<string>-dict</string>
		<string>/Users/you/Library/Application Support/chaoset/dict.tsv</string>
		<string>-userdict</string>
		<string>/Users/you/Library/Application Support/chaoset/userdict.json</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
</dict>
</plist>
```

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/ime.chaoset.daemon.plist
```

## 卸载

```sh
launchctl bootout gui/$(id -u)/ime.chaoset.inputmethod
launchctl bootout gui/$(id -u)/ime.chaoset.daemon
rm -f ~/Library/LaunchAgents/ime.chaoset.inputmethod.plist
rm -f ~/Library/LaunchAgents/ime.chaoset.daemon.plist
rm -rf ~/Library/Input\ Methods/ChaosetIME.app
```

## 验证状态（2026-09-23，本机 macOS 15 / arm64）

已在本机完成的运行时验证：

- **Swift IPC 客户端**：`build/smoke` 用与 app 相同的 EngineClient 实现
  驱动真实 daemon，6/6 项通过（连接/组词/预编辑/选词上屏/歧义切分/退格/重置）。
- **app 进程**：二进制可独立启动，IMKServer 初始化 + 主循环存活不崩溃。
- **ObjC 类注册**：控制器类以 `@objc(ChaosetController)` 显式注册
  （IMK 用 NSClassFromString 查找，Swift 类不注册会找不到），类名已确认编入二进制。
- **系统安装**：安装到 `~/Library/Input Methods`、`plutil` 校验、
  ad-hoc 签名、`TISRegisterInputSource` 返回成功。
- 过程中修复两处真缺陷：初版误用 `LSBackgroundOnly`（参照 Squirrel
  改为 `LSUIElement` + `NSPrincipalClass` + `TISInputSourceID` +
  `TISIntendedLanguage`）；控制器类未注册 ObjC 名。
- **v2 统一候选窗贴图**：`candidateImage` 客户端方法 + NSPanel 贴图面板
  已实现。组词时优先显示 daemon 渲染的统一候选窗图，daemon 未启用渲染时
  回退 IMKCandidates（v1）。
  **2026-10-22 起面板跟随光标而非鼠标**：经
  `client.attributes(forCharacterIndex: 0, lineHeightRectangle: &rect)` 取插入点
  矩形，优先放在光标行**下方**（`rect.minY - 6`）、下方空间不足时翻到**上方**
  （`rect.maxY + 6`），并用 `NSScreen.visibleFrame` 钳制不出屏；仅在矩形全零/
  非有限值时退回鼠标位置（部分 Electron、远程桌面客户端会返回零矩形）。
  同时按 `effectiveAppearance` 传 `dark`、按屏幕 `backingScaleFactor` 传 `scale`
  （只传 1 或 2），并按响应里的实际 `scale` 换算 `NSImage.size`，Retina 下 1:1 物理像素。
- **2026-09-28 真机反馈修复**：Info.plist 此前缺 `ComponentInputModeDict`
  （输入模式声明），TIS 不把本壳枚举为可添加输入源——已装进
  `~/Library/Input Methods` 且注销重登，系统设置仍无此输入法，
  `tis list` 全量输入源中也无条目（`TISRegisterInputSource` 却返回成功，
  即注册成功 ≠ 可被枚举）。对照 Squirrel `resources/Info.plist` 补
  单一 zh-Hans 模式（`…ChaosetIME.Hans`）修复。
- **2026-10-06 视觉与命名融入中国元素（显示名与图标部分已被 2026-10-22 取代）**：图标改为中国印章语汇——中国红渐变底、
  `0.022em` 白色外框 + 细内框、宋体（STSongti-SC-Bold，取不到则退系统中文字重）「超」字、
  大尺寸再叠三枚圆弧组成的如意云纹暗纹；当时中文显示名定为「超集拼音」（英文
  Chaoset Pinyin）。小尺寸（16/32px）自动退化为干净的印章徽标。
  **2026-10-22 更正**：项目名是 chaos + set（不是 chao + set），「超集」是误读，
  显示名已改为中英同名的 **Chaoset**；印章图标只保留为应用图标（.icns），
  输入源图标改为系统规格的模板图（见上表）。
- **2026-09-28 显示名本地化**：输入源显示名定名「Chaoset 拼音」
  （英文环境 Chaoset Pinyin），经 `Resources/*.lproj/InfoPlist.strings`
  提供——除 CFBundleDisplayName 外还按输入源 ID 逐模式命名
  （机制同 Squirrel 的 InfoPlist.xcstrings），避免与系统「简体拼音」混淆。
- **2026-10-06 重要机制发现：文件变化触发的重建"不新增"第三方输入法**。
  实测：把本项目 app（20:26:16 装入）以及随后的 4 个对照 bundle（含一个改名的官方
  Squirrel，21:43 正确签名装入）放进 `~/Library/Input Methods`，用户级输入源缓存
  仍随之被重建（mtime 20:26:19 → 21:42:24 → 21:43:37，均由文件监视触发），但**始终
  没有新增任何第三方条目**（头部计数保持 66 = 系统输入源数，大小恒为 138,876 B）。
  即：FS 事件重建负责**移除/刷新**，而**新增第三方输入法只能在登录扫描
  （`LoginLogoutSCNotificationCallback` → `kCacheRebuildAll`）时发生**——这与
  macOS 不允许应用在运行期"热插"输入法的安全设计一致。因此 20:26 那次重建没有收录
  本项目，**不能**说明本 bundle 被拒绝；判定依旧要靠一次登录。
- **2026-10-06 安装产物体检**：已装 app 的可执行文件为 `0755`、Mach-O arm64；
  `codesign --verify --deep --strict` → *valid on disk / satisfies its Designated Requirement*；
  xattr 只有 `com.apple.provenance`（与官方 Squirrel 样本一致）；`spctl` 两者同为
  *rejected*（ad-hoc 签名皆然，非差异项）。即**文件级问题（权限/签名/扩展属性）可排除**。
  另：TIS 缓存头部计数对照，19:11（含 Squirrel）为 69 个输入源、20:26 重建后为 66 个
  （正好少 3 条 `im.rime.inputmethod.Squirrel*`），其中仍无本项目条目——本项目是否在
  该次重建中被跳过（竞态）还是被审讯拒绝，只有下一次登录重建才能区分。
- **2026-10-06 缓存重建机制取证**：从本机 dyld 共享缓存的 HIToolbox 字符串确认，
  TIS 输入源表是**缓存 + 按需重建**：
  `LoginLogoutSCNotificationCallback -> islcRebuildInputSourceCache( kCacheRebuildAll )`
  （登录/登出重建）、`InputSourceFSEventCallback -> TISUpdateIntlFileCache`
  （被监视目录有文件变化时重建，监视列表含 `~/Library/Input Methods/`、`~/Library/Keyboard Layouts/`、
  `~/Library/Components/`、`/Library/Input Methods/` 等）。用户级主缓存在
  `$TMPDIR/C/com.apple.IntlDataCache.le.kbdx`（138,876 B）；对照 19:11 的旧副本（139,548 B）
  可见第三方输入法**确实会进这份表**（当时有 3 条 `im.rime.inputmethod.Squirrel*`）。
  该缓存与所在目录对本会话均不可写，`killall TextInputMenuAgent`、打开系统设置也不触发重建
  （系统设置自己的缓存副本停在 19:11），故"重建一次看结果"只能靠登录。
- **2026-10-06 键集完备性取证**：从本机 dyld 共享缓存的 HIToolbox 字符串里提取系统认识的
  全部 `tsInputMode*` 键共 12 个（`tsInputModeListKey`、`…KeyEquivalentModifiersKey`、
  `…KeyEquivalentKey`、`…ScriptKey`、`…PrimaryInScriptKey`、`…PaletteIconFileKey`、
  `…MenuIconFileKey`、`…IsVisibleKey`、`…DefaultUINameKey`、`…DefaultStateKey`、
  `…CharacterRepertoireKey`、`…AlternateMenuIconFileKey`）。本壳覆盖其中 11 个，
  未用的两个（KeyEquivalentKey / DefaultUINameKey）官方 Squirrel 同样没有——即不存在
  "漏写某个必需键"这一可能，剩余变量只剩登录扫描时机与旧 id 的记忆问题。
- **2026-10-06 按键路由修复与回归测试**：`tests/key_routing/main.swift` 用合成 NSEvent
  直接驱动 `ChaosetController.handle`：**新实现 8/8 通过**，同一套检查下**修复前旧实现
  4/8 不通过**（keyUp 未放行、Cmd+S/Ctrl+A 被输入法吞、Shift 空按根本没切换）。
  三处修复：① 只处理 keyDown/flagsChanged，keyUp 一律放行（否则同一按键可能被处理两次）；
  ② 带 Command/Control/Option 的组合键一律放行；③ Shift 空按改为"按下装填、松开判定、
  期间出现其它按键即取消"，Shift+字母 不再误切换。用法见测试文件头部注释。
- **2026-10-06 IPC 韧性修复**：客户端连接后设 `SO_NOSIGPIPE`。daemon 在写入途中退出时，
  `send()` 到已关闭套接字会触发 SIGPIPE（默认终止进程），**整个输入法进程会被信号杀死**；
  macOS 没有 `MSG_NOSIGNAL`，按 BSD 惯例设 `SO_NOSIGPIPE` 后写入返回 EPIPE，客户端按既有
  逻辑断开并降级（按键透传）。差分实测（连接→正常调用→杀掉 daemon→再调用）：
  **修复前 exit=141（被 SIGPIPE 杀死），修复后 exit=0 且第二次调用返回 nil**。
- **2026-10-06 候选窗字体实测**：本机（macOS 15.8.1）`/System/Library/Fonts/PingFang.ttc`
  **不存在**，renderer 的平台探测实际命中 `/System/Library/Fonts/Hiragino Sans GB.ttc`（覆盖率好：
  同一组 9 个候选全部有字形）。**不要把系统宋体加入探测序列**：`Supplemental/Songti.ttc` 的
  第 0 号 face 覆盖不全，实测同一组候选里有 6 个画成空白（像素统计：含文字行数 50 vs
  Hiragino 的 114）。若要换字体请用 `-font` 指定并先用候选窗实渲染验证覆盖率。
- **2026-10-06 本地化与"系统确实读过我们的 bundle"复核**：
  - `InfoPlist.strings` 按输入源 ID 逐模式命名（`CFBundleDisplayName`、`CFBundleName`、
    `ime.chaoset.inputmethod`、`ime.chaoset.inputmethod.Hans`），与官方 Squirrel 的命名方式
    一致（Squirrel 同样按 `im.rime.inputmethod.Squirrel[.Hans|.Hant]` 命名），故系统设置里
    显示的是「超集拼音 / Chaoset Pinyin」（当时显示名；现为 Chaoset），不会露出裸 ID。
  - `launchctl print gui/$(id -u)` 里有 `application.ime.chaoset.inputmethod.<...>` 处于
    **running**（program 指向 `~/Library/Input Methods/ChaosetIME.app`，immediate reason =
    launch job demand）：系统（runningboardd）**已按需拉起过我们的 bundle**，说明它能读到这个
    app；但输入源**表**（`$TMPDIR/C/…IntlDataCache.le.kbdx`）里仍无条目，佐证"表只在登录时扩充"。
  - 用 TIS 公共 API 做**过滤**查询（`TISCreateInputSourceList(filter, true)`）在本沙盒进程里
    一律返回 NULL（不带过滤的查询正常返回 310 条），所以无法在会话内直接
    `TISEnableInputSource` 把本输入法塞进已启用列表——只能走"注销重登"。
- **2026-10-06 运行时复查**：把已装好的 `~/Library/Input Methods/ChaosetIME.app`
  直接启动，能在本会话 bootstrap 里看到它注册的 IMK 服务
  `ime.chaoset.inputmethod_Connection`（launchctl 里也有 `application.ime.chaoset.inputmethod` 进程），
  即装在输入法目录里的这份 bundle 能正常作为输入法服务起来。注意 `IMKServer(name:bundleIdentifier:)`
  的连接名由 **bundle id 推导**（`<bundleID>_Connection`）：实测同一份 plist 连接名、
  换 bundle id 后注册名随之改变，官方 Squirrel 亦同（其 plist 写 `Squirrel_Connection`，
  实际注册名仍按 bundle id 推导），故 `InputMethodConnectionName` 只需保持与
  `CFBundleIdentifier` 同源即可，系统两侧一致。
- **2026-10-06 枚举失败根因排查（待系统级复验）**：本机 macOS 15.8.1 实测，
  输入法目录里的 ChaosetIME.app（已有 ComponentInputModeDict、`codesign -v`
  通过、当天 08:27 重启登录过）在 `tis list` 的 313 个输入源里**依然没有**；
  而**同目录**下 ad-hoc 重签的官方 Squirrel 1.1.2 能被正常枚举（3 条：
  `im.rime.inputmethod.Squirrel[.Hans/.Hant]`）。即：不是签名、不是
  LaunchServices、不是缓存没刷新，而是 TIS 对本 bundle 的 Info.plist 审讯不通过。
  对照已知可用样本，本壳原有三处结构性差异：① `TISInputSourceID`
  （`…ChaosetIME`）与 `CFBundleIdentifier`（`…inputmethod`）不一致；
  ② 完全没有图标键与图标资源；③ 缺 `LSMinimumSystemVersion`、
  `CFBundleSupportedPlatforms`、`CFBundleSignature`、`LSBackgroundOnly`、
  `TICapsLockLanguageSwitchCapable`、`InputMethodServerDelegateClass` 等
  样板键（`CFBundleDevelopmentRegion` 也从 `zh_CN` 改为 `en`）。
  已按「逐键对齐可用样本」重写 `build.sh` 的 plist 模板并把 ID 对齐为
  `ime.chaoset.inputmethod[.Hans]`，同时新增自有图标生成器；
  装入输入法目录后用 `./build/tis list | grep -i chaoset` 复验。
- **2026-10-21 根因确认（键集对齐不是充分条件）**：用户重启系统（12:37 重启、
  12:38 登录）后复验，TIS 输入源缓存仍为 138,876 B（=纯系统表大小，无第三方
  条目），`tis list` 310 条里无本输入法——即键集对齐后的 plist 依旧被拒。
  对照同一台机器上曾被枚举的 ad-hoc Squirrel（`im.rime.inputmethod.Squirrel`）
  与官方/同类实现（`app.msime.inputmethod.MetasequoiaIME` 等）后确认：
  **bundle id 缺少内层 `.inputmethod.` 段**（当时为 `ime.chaoset.inputmethod`，
  inputmethod 在末尾）是剩余唯一结构性差异。检索证据：
  - `TextInputSources.h`（本机 SDK）：InputSourceID 由 BundleID 推导时，
    InputModeID 后缀的裁剪规则写作 "…or that ends in `.inputmethod.`"；
  - rdar://21376022（Open Radar）：bundle id 为 `…MyInputMethodName.inputmethod`
    （结尾写法）时注销重登也不显示，改到中间 `…inputmethod.MyInputMethodName`
    后才出现；
  - SO 8960016 的公认答案：bundle id 必须含 `inputmethod` 段（`com.blugs.inputmethod.IPAPalette`）。
  已把 bundle id / TISInputSourceID / 模式 ID / 本地化键统一改为
  `ime.chaoset.inputmethod.ChaosetIME[.Hans]`，需再注销重登一次让登录扫描收录；登录后
  `./build/tis list | grep chaoset` 应出现两条，再用系统设置添加或
  `./reregister.sh` 启用+选中。

- **2026-10-21 候选窗残留修复**：真机使用反馈——输入后点击文本框外或切换应用，
  v2 贴图候选窗会残留在屏幕上、点不掉也关不掉（`CGWindowList` 实测 owner
  「超集拼音」（当时显示名）累计数十个 on-screen 窗口）。根因有两点：① 系统结束输入会话
  （点击文本框外、切换应用/输入法）走的是 `commitComposition:`，而壳只实现了
  `deactivateServer:`；② 每个控制器实例各持一个 `NSPanel`，会话结束后无人
  `orderOut`，窗口被 AppKit 的窗口表保留成"孤儿"。修复：候选面板改为全进程
  共用（静态），`commitComposition:`/`activateServer:`/`deactivateServer:`/
  `deinit` 全部收起面板，并设 `ignoresMouseEvents` 让点击穿透到宿主应用、
  触发正常的会话提交。

- **2026-10-21 输入源图标空白修复（已被 2026-10-22 的模板图方案取代，历史记录）**：输入菜单与系统设置里本输入法只显示一个空白
  圆角方块。根因不是图标文件加载失败（`NSBundle.image(forResource: "ChaosetIME.pdf")`
  能正常取到 22×22 图），而是系统把输入源图标当**模板图**渲染——只取 alpha 通道着色。
  原印章 PDF 的 alpha 在字形处也是不透明（中心区域透明像素 0%），整块被涂成白色；
  对照 Squirrel 的 rime.pdf 中心区域有 42% 透明像素（「鼠」字是挖空的）。
  修复：菜单/状态栏图标改为**多分辨率 TIFF（16/32/64）并用 `destinationOut` 把「超」
  字从 alpha 挖空**（TIFF 也是 TextInputSources.h 声明的输入模式图标常规格式）；.icns
  应用图标保持白字彩底不变。验证：TIFF 三页中心透明像素占 17%–22%，装回后菜单图标
  呈现「超」字形。
- **2026-10-22 输入源图标改为系统同款 keycap「Cs」+ 双变体**：真机反馈——① 输入菜单
  里本输入法条目呈灰色、图标仍是空白方块，而系统设置里能显示红色印章图；② 方形印章
  图标与 ABC/简体拼音的扁平 keycap 尺寸风格不一致，希望改用「Cs」（Chaoset）字标。
  对应调整：
  - 输入源图标改为 **20×16 扁平圆角 keycap + 系统字体「Cs」**（多分辨率
    20×16/40×32/60×48）；应用图标 `.icns` 也改为同一 keycap 设计（方形画布居中）；
  - 生成两个模式图标变体 `ChaosetIME-menu.tiff` / `ChaosetIME-palette.tiff`
    （均为字形 alpha 挖空），分别挂到 `tsInputModeMenuIconFileKey`/`…AlternateMenu…`
    与 `…Palette…`；
  - 补 bundle 级 `tsInputMethodIconFileKey`/`…AlternateIconFileKey`（指向 palette），
    让父输入源也有图标，避免菜单/系统设置里出现空白或裸 ID。
  - **为什么必须两个文件**：输入菜单按模板图渲染（只取 alpha），系统设置按彩色渲染；
    同一张图无法同时满足（挖空字形在彩色浅色界面会"隐形"，实心彩色图在模板路径会成
    实心方块）。
  - 系统设置列表与切换 HUD 的图标来源：两者取的是**应用图标**（`.icns` / TIS
    IconRef），不是 mode 图标；HUD 还按模板图渲染。所以 `.icns` 必须一起改成
    keycap 并挖空字形，否则系统设置显示旧印章、HUD 显示白方块（2026-10-22 实测）。
  - 灰色条目的真因（2026-10-22 定位）：输入菜单里的灰色条目 = **系统 TIS 输入源表
    里没有本 bundle，但 `~/Library/Preferences/com.apple.inputsources.plist` 的
    `AppleEnabledThirdPartyInputSources` 里仍有启用记录**（菜单按启用记录画出占位项，
    因查不到输入源元数据而置灰不可选）。而 TIS 表缺失，是**重装 bundle 的竞态**造成：
    替换 `~/Library/Input Methods` 里的文件会触发 TIS 缓存（`$TMPDIR/../C/
    com.apple.IntlDataCache.le.kbdx`）重建，重建只移除/刷新、**不新增第三方输入源**；
    登记动作若落在重建落盘之前就会被随后覆盖。
  - 判定：在**普通终端（无沙盒）**执行 `build/tis list | grep chaoset` 看不到条目，
    缓存大小是 138,876 B（纯系统表）；而 `defaults read`/`plutil` 里仍有启用记录。
    注意：沙盒进程读不到系统缓存，会临时扫描目录凑出一份列表（能"看到"本输入法），
    所以**必须在无沙盒终端判断**。
  - 恢复：普通终端执行 `build/tis register "$HOME/Library/Input Methods/ChaosetIME.app"`，
    缓存应变为 139,3xx B 并出现 `…ChaosetIME` / `…ChaosetIME.Hans` 两条，
    菜单里的灰色条目随即恢复可选，**无需注销重登**。`install.sh` 已改为拷贝后
    `sleep 2` 再登记并重试校验，避免再次踩到。

- **2026-10-22 IMK 连接名与按需拉起限制**：
  - `InputMethodConnectionName` 必须是 **`<CFBundleIdentifier>_Connection`** 形式
    （本壳为 `ime.chaoset.inputmethod.ChaosetIME_Connection`，main.swift 直接读 plist，
    两处不会再不一致）。不满足时 `imklaunchagent` 记录
    `Refusing connection name for bundle: unrecognized 'InputMethodConnectionName' value`。
    依据：imklaunchagent 行为实测 + metasequoiaime/msime PR #2989（同一坑的修复记录）。
  - 本机（macOS 15.8.1）实测 **imklaunchagent 仍无法按需拉起本输入法**：
    `LaunchInputMethod() Error, status=-50` → 客户端拿不到 endpoint → 选中后打字
    完全没反应。`man imklaunchagent` 还写明：对"短时间内反复异常退出"的输入法会
    **直接禁止拉起**——调试时反复 `killall` 很容易踩到。
  - 规避（已落地）：`install.sh` 安装 **`ime.chaoset.inputmethod` LaunchAgent**
    让输入法进程常驻；实测常驻时客户端能正常 `Activate Server`、打字正常。
    排查时不要把常驻进程当作"泄漏"随手 kill（会触发上面的禁启保护）。

安装后的人工步骤（**无需注销重登**，2026-10-21 实测）：

1. 系统设置 → 键盘 → 输入法 → 添加「Chaoset」并切换过去；
   不想用系统设置时，可运行 `./reregister.sh`（尽力启用+选中）。
2. 在任意文本框打字验证：字母组词、数字/空格选词、候选窗跟随光标（在窗口最下方输入时应翻到光标上方）。
3. 若系统设置里仍看不到：`./build/tis list | grep chaoset` 查看枚举状态，
   必要时注销重登一次刷新菜单缓存。

- **2026-10-22 输入源图标对齐系统规格（切换 HUD 空白方块的真正根因）**：
  真机反馈「用 ⌃空格 切换输入法时，系统弹窗里本输入法没有图标（一块空白圆角方块）」。
  逐像素对照系统 `SCIM.app/…/pinyin.tiff` 后定位到规格差异：
  ① 系统图标是 **16×16（72dpi）+ 32×32（144dpi）双帧 TIFF**，我们此前是 20×16/40×32/60×48
  三帧（尺寸与 Retina 帧比例都不符）；② 系统图标 **RGB 恒为 (0,0,0)**、形状只由 alpha 表达，
  我们此前是白底 RGB 图；③ 系统 plist 用 `tsInputMethodIconFileKey` 同时给父输入源与模式，
  我们此前父级指向 palette 变体、模式指向 menu 变体，两处不一致。
  修复：`tools/make-icon.swift` 改为生成 `ChaosetIME-input.tiff`（16×16+32×32、RGB 全黑、
  alpha 表达圆角方块 + 挖空「Cs」），Info.plist 五个图标键全部指向它，并补 `CFBundleIconName`。
  **验证**：`file` 确认 LZW 双帧、Pillow 确认 RGB=(0,0,0) 且 alpha 0/255 双峰、
  `tis` 确认 `kTISPropertyIconImageURL` 指向新文件。HUD 实际显示需重装后由人工确认。

- **2026-10-22 第三次修正：2x 帧 DPI 与字形度量（系统设置里仍显示不一致）**：
  用户反馈系统设置「添加输入法」列表里图标仍与其他输入法风格不符。逐项排查后
  又发现三处：
  ① **2x 帧的 Resolution 标签必须是 144**——`tiffutil -cathidpicheck` 会沿用
     输入 PNG 的 DPI，而 `NSBitmapImageRep.representation` 写的 PNG 不带 pHYs，
     结果两帧都标成 72dpi。系统据此认不出 32×32 是 2x，HiDPI 下拿 16×16 拉伸，
     表现为图标糊、字形位置偏。修法：PNG 用 `CGImageDestination` 带 DPI 写出，
     并在 tiffutil 之后**直接改 TIFF 二进制的 282/283 标签**（逐帧 72/144）。
  ② **字形高度必须按墨迹（glyph path bounds）二分，不能用行高**——
     `NSAttributedString.size()` 含上下留白，用它当目标高度会让实际墨迹偏小
     （实测 32×32 帧只占 16 行，期望 22 行）。改用
     `CTLineGetBoundsWithOptions(.useGlyphPathBounds)` 二分字号。
  ③ **CGBitmapContext 原点在左下**——像素缓冲按「第 0 行在上」读回时必须翻转 y，
     否则字形整体偏移。
  验证：与系统 `pinyin.tiff` 逐帧比对，`n_frames`、两帧尺寸、`282/283`、
  `ExtraSamples`、`compress`、`SamplesPerPixel`、`BitsPerSample` 全部一致；
  AppKit 解码路径下 alpha 网格与 `拼` 同构。

- **2026-10-22 输入源图标规格再修正（用户反馈颜色/大小/hover 不一致）**：
  系统设置与菜单栏里本输入法图标比 ABC、简体拼音偏亮、字形偏小、hover 无变化。
  逐标签比对系统 `pinyin.tiff` / `Korean.tiff` / `Katakana.tiff` 后确认三处差异：
  ① **ExtraSamples 标签**——系统是 `2`（UNASSOCIATED，非预乘 alpha），
     我们用 `CGImageDestination` 直接写多帧得到 `1`（ASSOCIATED，预乘），
     系统按模板图取 alpha 着色时两者观感不同。改用 `tiffutil -cathidpicheck`
     合成，输出与系统逐标签一致（`ExtraSamples=(2,)`、`tiffutil v350`、LZW）。
  ② **字形必须是单字符**——系统内置输入源全部单字符（ABC「A」、简体拼音「拼」、
     韩文「한」、假名「あ」）。此前的「Cs」两字符在 16px 里只能占一半宽度，
     视觉重量明显轻于系统图标。改为单字符「C」。
  ③ **字形高度 0.69×边长**（系统图实测 11/16 行）。直接在 16×16 上画字体笔画
     只有 1px 会断成 4 段，故先在 8× 超采样画布上用 `CGBitmapContext` 绘制
     「圆角方块挖去字形」，再用 alphaOnly 灰度上下文读回 alpha、降采样写入。
  验证：`file` 确认双帧 LZW、Pillow 确认 `ExtraSamples=(2,)` 且 RGB 恒 0、
  模板渲染目检与系统图标同款。**hover 是系统对模板图自动加的高亮，图标本身是
  纯 alpha 后即由系统统一处理。**

## 已知限制

- 候选窗跟随光标依赖 `attributes(forCharacterIndex:lineHeightRectangle:)`；
  少数客户端（个别 Electron 应用、远程桌面）返回零矩形，此时退回鼠标位置，
  位置可能不理想。
- 修改 app 后重装，需要先把输入法从列表移除再重新添加。
- 打字闭环尚未人工验证（需要 GUI 交互与注销重登，无法脚本化）。