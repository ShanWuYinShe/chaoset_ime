#!/bin/sh
# 构建 macOS 输入法壳：编译 Swift 壳 + Go daemon，组装 ChaosetIME.app。
set -e
cd "$(dirname "$0")"

mkdir -p build
echo "[1/3] 编译 Go daemon..."
(cd ../.. && go build -o shims/macos/build/chaosetd ./cmd/chaosetd)

echo "[2/3] 编译 Swift 输入法壳..."
APP=build/ChaosetIME.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
swiftc -O -framework InputMethodKit -framework AppKit src/*.swift -o "$APP/Contents/MacOS/ChaosetIME"

echo "[3/3] 组装 app bundle..."
# Info.plist 必须声明 ComponentInputModeDict（输入模式），TIS 才会把本壳
# 枚举为可添加的输入源（对照 Squirrel resources/Info.plist；缺它则
# 系统设置不显示、tis list 无条目，注销重登也无效）。
cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>zh_CN</string>
	<key>CFBundleExecutable</key>
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
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSPrincipalClass</key>
	<string>NSApplication</string>
	<key>InputMethodConnectionName</key>
	<string>ChaosetIME_Connection</string>
	<key>InputMethodServerControllerClass</key>
	<string>ChaosetController</string>
	<key>TISInputSourceID</key>
	<string>ime.chaoset.inputmethod.ChaosetIME</string>
	<key>TISIntendedLanguage</key>
	<string>zh-Hans</string>
	<key>ComponentInputModeDict</key>
	<dict>
		<key>tsInputModeListKey</key>
		<dict>
			<key>ime.chaoset.inputmethod.ChaosetIME.Hans</key>
			<dict>
				<key>TISInputSourceID</key>
				<string>ime.chaoset.inputmethod.ChaosetIME.Hans</string>
				<key>TISIntendedLanguage</key>
				<string>zh-Hans</string>
				<key>tsInputModeCharacterRepertoireKey</key>
				<array>
					<string>Hans</string>
				</array>
				<key>tsInputModeDefaultStateKey</key>
				<true/>
				<key>tsInputModeIsVisibleKey</key>
				<true/>
				<key>tsInputModePrimaryInScriptKey</key>
				<true/>
				<key>tsInputModeScriptKey</key>
				<string>smUnicodeScript</string>
			</dict>
		</dict>
		<key>tsVisibleInputModeOrderedArrayKey</key>
		<array>
			<string>ime.chaoset.inputmethod.ChaosetIME.Hans</string>
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
for spec in "zh_CN:$NAME_ZH" "zh-Hans:$NAME_ZH" "en:$NAME_EN"; do
  lang="${spec%%:*}"
  name="${spec#*:}"
  dir="$APP/Contents/Resources/$lang.lproj"
  mkdir -p "$dir"
  cat > "$dir/InfoPlist.strings" <<STR
"CFBundleDisplayName" = "$name";
"CFBundleName" = "$name";
"ime.chaoset.inputmethod.ChaosetIME" = "$name";
"ime.chaoset.inputmethod.ChaosetIME.Hans" = "$name";
STR
done
plutil -lint "$APP/Contents/Resources/"*/InfoPlist.strings

plutil -lint "$APP/Contents/Info.plist"
codesign --force -s - "$APP"

echo "已生成 $(pwd)/$APP (daemon: build/chaosetd)"
