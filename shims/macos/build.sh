#!/bin/sh
# 构建 macOS 输入法壳：编译 Swift 壳 + Go daemon，组装 ChaosetIME.app。
set -e
cd "$(dirname "$0")"

mkdir -p build
echo "[1/4] 编译 Go daemon..."
(cd ../.. && go build -o shims/macos/build/chaosetd ./cmd/chaosetd)

echo "[2/4] 编译 Swift 输入法壳与验证工具..."
APP=build/ChaosetIME.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
swiftc -O -framework InputMethodKit -framework AppKit src/*.swift -o "$APP/Contents/MacOS/ChaosetIME"
# README 的两步核验都用这两个工具，随构建一起产出：
#   tis   —— 注册/枚举 TIS 输入源（判断系统是否收录本输入法）
#   smoke —— 与 daemon 的 IPC 全链路自检（需要先跑起 daemon）
swiftc -O -framework Carbon tools/tis.swift -o build/tis
swiftc -O -framework InputMethodKit -framework AppKit smoke/main.swift src/EngineClient.swift -o build/smoke

echo "[3/4] 生成图标（本家徽标，不复用第三方素材）..."
mkdir -p build/Resources
swift tools/make-icon.swift build/Resources
iconutil -c icns build/Resources/ChaosetIME.iconset -o build/Resources/ChaosetIME.icns

echo "[4/4] 组装 app bundle..."
mkdir -p "$APP/Contents/Resources"
cp build/Resources/ChaosetIME.icns build/Resources/ChaosetIME.pdf "$APP/Contents/Resources/"

# Info.plist 必须声明 ComponentInputModeDict（输入模式），TIS 才会把本壳
# 枚举为可添加的输入源。除该字典外，以下两组键对齐同目录下可正常枚举的
# 第三方输入法（2026-10-06 以官方 Squirrel 1.1.2 为对照取证）：
#   ① 输入源 ID 与 CFBundleIdentifier 一致（bundle 与顶层 TISInputSourceID
#      都是 ime.chaoset.inputmethod，输入模式为其加 .Hans 后缀）；
#   ② 图标键与 Xcode 样板键（CFBundleIconFile/…、LSMinimumSystemVersion、
#      CFBundleSupportedPlatforms、CFBundleSignature、LSBackgroundOnly、
#      TICapsLockLanguageSwitchCapable、InputMethodServerDelegateClass）。
cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>ChaosetIME</string>
	<key>CFBundleIconFile</key>
	<string>ChaosetIME</string>
	<key>CFBundleIdentifier</key>
	<string>ime.chaoset.inputmethod</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>ChaosetIME</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>0.1.0</string>
	<key>CFBundleSignature</key>
	<string>????</string>
	<key>CFBundleSupportedPlatforms</key>
	<array>
		<string>MacOSX</string>
	</array>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>LSBackgroundOnly</key>
	<false/>
	<key>LSMinimumSystemVersion</key>
	<string>15.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSPrincipalClass</key>
	<string>NSApplication</string>
	<key>InputMethodConnectionName</key>
	<string>ChaosetIME_Connection</string>
	<key>InputMethodServerControllerClass</key>
	<string>ChaosetController</string>
	<key>InputMethodServerDelegateClass</key>
	<string>ChaosetController</string>
	<key>TICapsLockLanguageSwitchCapable</key>
	<true/>
	<key>TISInputSourceID</key>
	<string>ime.chaoset.inputmethod</string>
	<key>TISIntendedLanguage</key>
	<string>zh-Hans</string>
	<key>ComponentInputModeDict</key>
	<dict>
		<key>tsInputModeListKey</key>
		<dict>
			<key>ime.chaoset.inputmethod.Hans</key>
			<dict>
				<key>TISInputSourceID</key>
				<string>ime.chaoset.inputmethod.Hans</string>
				<key>TISIntendedLanguage</key>
				<string>zh-Hans</string>
				<key>tsInputModeAlternateMenuIconFileKey</key>
				<string>ChaosetIME.pdf</string>
				<key>tsInputModeCharacterRepertoireKey</key>
				<array>
					<string>Hans</string>
				</array>
				<key>tsInputModeDefaultStateKey</key>
				<true/>
				<key>tsInputModeIsVisibleKey</key>
				<true/>
				<key>tsInputModeKeyEquivalentModifiersKey</key>
				<integer>4608</integer>
				<key>tsInputModeMenuIconFileKey</key>
				<string>ChaosetIME.pdf</string>
				<key>tsInputModePaletteIconFileKey</key>
				<string>ChaosetIME.pdf</string>
				<key>tsInputModePrimaryInScriptKey</key>
				<true/>
				<key>tsInputModeScriptKey</key>
				<string>smUnicodeScript</string>
			</dict>
		</dict>
		<key>tsVisibleInputModeOrderedArrayKey</key>
		<array>
			<string>ime.chaoset.inputmethod.Hans</string>
		</array>
	</dict>
</dict>
</plist>
PLIST
printf 'APPL????' > "$APP/Contents/PkgInfo"

# 输入源显示名走 bundle 的本地化 InfoPlist.strings（对照 Squirrel 的
# InfoPlist.xcstrings：除 CFBundleDisplayName 外，还按输入源 ID 逐模式
# 命名）。缺省名与系统组件难分辨，定名「Chaoset 拼音 / Chaoset Pinyin」。
NAME_ZH="Chaoset 拼音"
NAME_EN="Chaoset Pinyin"
for spec in "en:$NAME_EN" "zh-Hans:$NAME_ZH" "zh_CN:$NAME_ZH"; do
  lang="${spec%%:*}"
  name="${spec#*:}"
  dir="$APP/Contents/Resources/$lang.lproj"
  mkdir -p "$dir"
  cat > "$dir/InfoPlist.strings" <<STR
"CFBundleDisplayName" = "$name";
"CFBundleName" = "$name";
"ime.chaoset.inputmethod" = "$name";
"ime.chaoset.inputmethod.Hans" = "$name";
STR
done
plutil -lint "$APP/Contents/Resources/"*/InfoPlist.strings

plutil -lint "$APP/Contents/Info.plist"
codesign --force -s - "$APP"

echo "已生成 $(pwd)/$APP (daemon: build/chaosetd)"
