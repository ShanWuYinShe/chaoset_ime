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
	<key>LSBackgroundOnly</key>
	<true/>
	<key>InputMethodConnectionName</key>
	<string>ChaosetIME_Connection</string>
	<key>InputMethodServerControllerClass</key>
	<string>ChaosetController</string>
</dict>
</plist>
PLIST
printf 'APPL????' > "$APP/Contents/PkgInfo"
plutil -lint "$APP/Contents/Info.plist"
codesign --force -s - "$APP"

echo "已生成 $(pwd)/$APP (daemon: build/chaosetd)"
