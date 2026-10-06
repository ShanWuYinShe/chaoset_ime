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

# 1. 安装输入法 app（内部会先移除旧版，再拷入并登记；换新版一律走它）
./install.sh

# 2. 部署 daemon 与词库
mkdir -p "$HOME/Library/Application Support/chaoset"
cp build/chaosetd "$HOME/Library/Application Support/chaoset/"
cp ../../data/dict_seed.tsv "$HOME/Library/Application Support/chaoset/dict.tsv"

# 3. 启动 daemon（前台先试通）
"$HOME/Library/Application Support/chaoset/chaosetd" \
  -socket "$HOME/Library/Application Support/chaoset/chaoset.sock" \
  -dict "$HOME/Library/Application Support/chaoset/dict.tsv" \
  -userdict "$HOME/Library/Application Support/chaoset/userdict.json"

# 4. 核验是否被 TIS 枚举（出现 ime.chaoset.inputmethod[.Hans] 才算成功）
./build/tis list | grep -i chaoset

# 5. 系统设置 → 键盘 → 输入法 → 添加「Chaoset 拼音」，切到它开始打字
```

> ⚠️ **第 1 步与第 4 步之间必须注销并重新登录。** macOS 的输入源表在**登录时**才
> 扩充新输入法，`TISRegisterInputSource` 只是"登记"：未重登前 `tis list` 里不会
> 有本输入法，系统设置里也看不到。官方 Squirrel 安装包同理——它的 `PackageInfo`
> 写着 `postinstall-action="logout"`，postinstall 还调用 `--register-input-source`
> 与 `--enable-input-source`/`--select-input-source`。
>
> 2026-10-06 实测（macOS 15.8.1）：装好后仅靠 `TISRegisterInputSource`、
> `lsregister -f`、`killall TextInputMenuAgent`、甚至直接 `open` 该 app，
> 都不会让它在当前会话进入输入源列表；而移走一个已在表里的输入法后，其条目
> 会先变成无名字（`?`）再消失——即"删除是懒校验、新增只在登录扫描"。

## 输入源标识与显示名

| 项 | 值 |
|---|---|
| `CFBundleIdentifier` / `TISInputSourceID` | `ime.chaoset.inputmethod`（**两者必须一致**） |
| 输入模式（`ComponentInputModeDict.tsInputModeListKey`） | `ime.chaoset.inputmethod.Hans`（父 ID + `.Hans`） |
| 显示名 | 「Chaoset 拼音」（英文环境 Chaoset Pinyin），由 `Resources/{en,zh-Hans,zh_CN}.lproj/InfoPlist.strings` 按上面两个 ID 提供 |
| 图标 | `ChaosetIME.icns`（应用图标）+ `ChaosetIME.pdf`（输入源菜单图标），由 `tools/make-icon.swift` 生成 |

> ⚠️ 系统「输入法」列表里若出现**鼠须管**，那是官方 Rime/Squirrel 应用
> （`im.rime.inputmethod.Squirrel`）。2026-10-05 排障时曾把官方 Squirrel 1.1.2
> 下载、ad-hoc 重签后放进 `~/Library/Input Methods/SquirrelAdhoc.app`，用作
> 「同目录第三方输入法能否被枚举」的对照样本；它**不是本项目**，其源码、图标与
> 名称都不得复用。本项目自身可被枚举后即可删除该样本。

## 开机自启（LaunchAgent）

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
launchctl bootout gui/$(id -u)/ime.chaoset.daemon
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
  已实现（编译级验证）。组词时优先显示 daemon 渲染的统一候选窗图，
  daemon 未启用渲染时回退 IMKCandidates（v1）。面板跟随鼠标定位；
  光标精确坐标需 IMK 深度 API，作为后续优化项。
- **2026-09-28 真机反馈修复**：Info.plist 此前缺 `ComponentInputModeDict`
  （输入模式声明），TIS 不把本壳枚举为可添加输入源——已装进
  `~/Library/Input Methods` 且注销重登，系统设置仍无此输入法，
  `tis list` 全量输入源中也无条目（`TISRegisterInputSource` 却返回成功，
  即注册成功 ≠ 可被枚举）。对照 Squirrel `resources/Info.plist` 补
  单一 zh-Hans 模式（`…ChaosetIME.Hans`）修复。
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

剩余需要**注销重登后人工完成**：

1. 注销并重新登录（macOS 行为：新装输入法重登后才进输入源列表，
   Squirrel 官方安装流程同样要求注销）。
2. 系统设置 → 键盘 → 输入法 → 添加「Chaoset 拼音」，切换到它。
3. 在任意文本框打字验证：字母组词、数字/空格选词、候选窗跟随。

若重登后列表仍无 ChaosetIME：`./build/tis list` 查看注册状态，
并把 `shims/macos/build/tis` 的输出反馈给开发者。

## 已知限制

- 候选窗跟随光标依赖 IMK 默认定位，某些应用里位置可能不理想。
- 修改 app 后重装，需要先把输入法从列表移除再重新添加。
- 打字闭环尚未人工验证（需要 GUI 交互与注销重登，无法脚本化）。
