# macOS 平台壳（ChaosetIME）

InputMethodKit 薄壳：只做「按键转发 + 文本上屏 + 候选窗展示」，全部组词逻辑
在 chaosetd。daemon 未运行时所有按键原样透传，不会影响宿主应用输入。

## 按键

| 按键 | 行为 |
|---|---|
| a-z / ' | 进入或追加组词 |
| 1-9 | 选择对应候选上屏 |
| 空格 / 回车 | 首选候选上屏（无候选时放行原键） |
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

## 已知限制

- 当前只做到**编译级验证**（swiftc 编译、plutil 校验、codesign 通过、
  同路径 IPC 链路演练），尚未在真机 GUI 下完整输入验证；首次安装后若
  输入法列表没出现，注销重新登录一次。
- 候选窗跟随光标依赖 IMK 默认定位，某些应用里位置可能不理想。
- 修改 app 后重装，需要先把输入法从列表移除再重新添加。
