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
| 标点键 `,` `.` `;` 等 | 输出中文标点（组词中先上屏首选候选）；组词中的 `'` 为音节分隔符 |
| 退格 | 删除一个输入字母 |
| Esc | 清空组词 |

## 套接字约定

壳按以下顺序找 daemon：环境变量 `CHAOSET_SOCKET` →
`~/Library/Application Support/chaoset/chaoset.sock`。
daemon 必须用相同路径启动（`-socket`）。连接失败有 1 秒冷却重试。

## 构建与安装

```sh
./build.sh          # 产出 build/ChaosetIME.app 与 build/chaosetd（ad-hoc 签名）

# 1. 安装输入法 app
cp -R build/ChaosetIME.app ~/Library/Input\ Methods/

# 2. 部署 daemon 与词库
mkdir -p "$HOME/Library/Application Support/chaoset"
cp build/chaosetd "$HOME/Library/Application Support/chaoset/"
cp ../../data/dict_seed.tsv "$HOME/Library/Application Support/chaoset/dict.tsv"

# 3. 启动 daemon（前台先试通）
"$HOME/Library/Application Support/chaoset/chaosetd" \
  -socket "$HOME/Library/Application Support/chaoset/chaoset.sock" \
  -dict "$HOME/Library/Application Support/chaoset/dict.tsv" \
  -userdict "$HOME/Library/Application Support/chaoset/userdict.json"

# 4. 系统设置 → 键盘 → 输入法 → 添加「ChaosetIME」，切到它开始打字
```

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

剩余需要**注销重登后人工完成**：

1. 注销并重新登录（macOS 行为：新装输入法重登后才进输入源列表，
   Squirrel 官方安装流程同样要求注销）。
2. 系统设置 → 键盘 → 输入法 → 添加「ChaosetIME」，切换到它。
3. 在任意文本框打字验证：字母组词、数字/空格选词、候选窗跟随。

若重登后列表仍无 ChaosetIME：`./build/tis list` 查看注册状态，
并把 `shims/macos/build/tis` 的输出反馈给开发者。

## 已知限制

- 候选窗跟随光标依赖 IMK 默认定位，某些应用里位置可能不理想。
- 修改 app 后重装，需要先把输入法从列表移除再重新添加。
- 打字闭环尚未人工验证（需要 GUI 交互与注销重登，无法脚本化）。
