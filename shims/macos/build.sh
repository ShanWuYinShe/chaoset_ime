#!/bin/sh
# 构建 macOS 输入法壳：编译 Swift 壳 + Go daemon，组装 ChaosetIME.app。
set -e
cd "$(dirname "$0")"

mkdir -p build
ROOT=$(cd ../.. && pwd)

# ---- 输出架构：默认 universal（arm64 + x86_64）----
# 背景：CI 的 macos-latest 是 Apple Silicon runner，裸 swiftc/go build 只产出
# arm64，Intel Mac 用户装上会直接提示「无法打开」。因此默认同时构建两种架构
# 并用 lipo 合并；设 ARCHS=arm64 可只出单架构（本地快速迭代用）。
ARCHS=${ARCHS:-"arm64 x86_64"}
MIN_MACOS=15.0
# 交叉编译 Swift 需要独立的模块缓存目录：默认缓存在部分环境不可写
# （沙盒 / 只读 HOME），统一放到 build/（已 gitignore）下且可复用。
MODCACHE="$ROOT/shims/macos/build/modulecache"
mkdir -p "$MODCACHE"

# swift_universal <输出> <framework...> -- <源文件...>
swift_universal() {
  out=$1; shift
  fws=""
  while [ "$1" != "--" ]; do fws="$fws -framework $1"; shift; done
  shift
  rm -f "$out"
  for arch in $ARCHS; do
    case "$arch" in
      arm64)  target="arm64-apple-macos$MIN_MACOS" ;;
      x86_64) target="x86_64-apple-macos$MIN_MACOS" ;;
      *) echo "未知架构: $arch" >&2; return 1 ;;
    esac
    swiftc -O -module-cache-path "$MODCACHE" -target "$target" $fws "$@" -o "$out.$arch" || return 1
  done
  if [ $(echo $ARCHS | wc -w) -gt 1 ]; then
    lipo -create "$out".* -output "$out"
    rm -f "$out".arm64 "$out".x86_64
  else
    mv "$out.$ARCHS" "$out"
  fi
}

# go_universal <输出> <包路径>
go_universal() {
  out=$1; pkg=$2
  rm -f "$out"
  for arch in $ARCHS; do
    # Go 的目标架构名与 lipo/Swift 不同：x86_64 在 Go 里叫 amd64。
    case "$arch" in
      arm64)  goarch=arm64 ;;
      x86_64) goarch=amd64 ;;
      *) echo "未知架构: $arch" >&2; return 1 ;;
    esac
    (cd "$ROOT" && GOOS=darwin GOARCH=$goarch CGO_ENABLED=0 go build -o "$out.$arch" "$pkg") || return 1
  done
  if [ $(echo $ARCHS | wc -w) -gt 1 ]; then
    lipo -create "$out".* -output "$out"
    rm -f "$out".arm64 "$out".x86_64
  else
    mv "$out.$ARCHS" "$out"
  fi
}

echo "[1/5] 编译 Go daemon（架构: $ARCHS）..."
go_universal "$ROOT/shims/macos/build/chaosetd" ./cmd/chaosetd

echo "[2/5] 编译 Swift 输入法壳与验证工具..."
APP=build/ChaosetIME.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
swift_universal "$APP/Contents/MacOS/ChaosetIME" InputMethodKit AppKit -- src/*.swift
# README 的两步核验都用这两个工具，随构建一起产出：
#   tis   —— 注册/枚举 TIS 输入源（判断系统是否收录本输入法）
#   smoke —— 与 daemon 的 IPC 全链路自检（需要先跑起 daemon）
swift_universal build/tis Carbon -- tools/tis.swift
swift_universal build/smoke InputMethodKit AppKit -- smoke/main.swift src/EngineClient.swift
#   icon-size —— 对比系统设置/菜单栏**实际渲染**的输入源图标尺寸是否与系统原生一致
#                （icon.tiff 的像素尺寸 ≠ 系统显示尺寸；见 README「图标要求」）
rm -f build/icon-size
for arch in $ARCHS; do
  case "$arch" in
    arm64)  carch="arm64" ;;
    x86_64) carch="x86_64" ;;
    *) echo "未知架构: $arch" >&2; exit 1 ;;
  esac
  clang -O2 -arch "$carch" -framework Foundation -framework AppKit tools/icon-size.m -o "build/icon-size.$arch"
done
if [ $(echo $ARCHS | wc -w) -gt 1 ]; then
  lipo -create build/icon-size.* -output build/icon-size && rm -f build/icon-size.arm64 build/icon-size.x86_64
else
  mv "build/icon-size.$ARCHS" build/icon-size
fi

echo "[3/5] 生成图标（本家徽标，不复用第三方素材）..."
mkdir -p build/Resources
# 清掉上一版遗留的资源：旧版曾生成 menu/palette/icon2/pdf 等文件，
# 残留在目录里会让人误以为仍是当前图标（也避免被打进 setup.app）。
rm -rf build/Resources/ChaosetIME.iconset build/Resources/ChaosetIME-menu.tiff \
       build/Resources/ChaosetIME-input.tiff build/Resources/ChaosetIME-hud.tiff \
       build/Resources/ChaosetIME-palette.tiff build/Resources/ChaosetIME-icon2.tiff \
       build/Resources/ChaosetIME.pdf
swift tools/make-icon.swift build/Resources
iconutil -c icns build/Resources/ChaosetIME.iconset -o build/Resources/ChaosetIME.icns

echo "[4/5] 组装 app bundle..."
mkdir -p "$APP/Contents/Resources"
cp build/Resources/ChaosetIME.icns \
   build/Resources/ChaosetIME-input.tiff "$APP/Contents/Resources/"

# Info.plist 必须声明 ComponentInputModeDict（输入模式），TIS 才会把本壳
# 枚举为可添加的输入源。除该字典外，以下三组键对齐同目录下可正常枚举的
# 第三方输入法（2026-10-06 以官方 Squirrel 1.1.2 为对照取证）：
#   ① **CFBundleIdentifier 必须含内层 ".inputmethod." 段**——即 inputmethod
#      前后都还有别的段（如 im.rime.inputmethod.Squirrel），仅以 inputmethod
#      结尾不生效。2026-10-21 本机（macOS 15.8.1）登录复验：以
#      "…chaoset.inputmethod" 结尾的 bundle 重启登录后仍不被 TIS 收录；
#      同目录 ad-hoc 签名的 Squirrel 1.1.2 可正常收录。依据：
#      TextInputSources.h 的 InputSourceID 推导规则（"ends in .inputmethod."）、
#      rdar://21376022（结尾写法实测无效）、SO 8960016。
#   ② 输入源 ID 与 CFBundleIdentifier 一致（bundle 与顶层 TISInputSourceID
#      相同，输入模式为其加 .Hans 后缀）；
#   ③ 图标键与 Xcode 样板键（CFBundleIconFile/…、LSMinimumSystemVersion、
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
	<string>ChaosetIME.icns</string>
	<key>CFBundleIconName</key>
	<string>ChaosetIME</string>
	<key>CFBundleIdentifier</key>
	<string>ime.chaoset.inputmethod.ChaosetIME</string>
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
	<string>ime.chaoset.inputmethod.ChaosetIME_Connection</string>
	<key>InputMethodServerControllerClass</key>
	<string>ChaosetController</string>
	<key>InputMethodServerDelegateClass</key>
	<string>ChaosetController</string>
	<key>TICapsLockLanguageSwitchCapable</key>
	<true/>
	<key>TISParticipatesInTouchBar</key>
	<true/>
	<key>TISIconIsTemplate</key>
	<true/>
	<key>TISIconLabels</key>
	<dict>
		<key>Primary</key>
		<string>C</string>
	</dict>
	<key>tsInputMethodIconFileKey</key>
	<string>ChaosetIME-input.tiff</string>
	<key>tsInputMethodAlternateIconFileKey</key>
	<string>ChaosetIME-input.tiff</string>
	<key>TISInputSourceID</key>
	<string>ime.chaoset.inputmethod.ChaosetIME</string>
	<key>ComponentInputModeDict</key>
	<dict>
		<key>tsInputModeListKey</key>
		<dict>
			<key>ime.chaoset.inputmethod.ChaosetIME.Pinyin</key>
			<dict>
				<key>TISIconIsTemplate</key>
				<true/>
				<key>TISIconLabels</key>
				<dict>
					<key>Primary</key>
					<string>C</string>
				</dict>
				<key>TISInputSourceID</key>
				<string>ime.chaoset.inputmethod.ChaosetIME.Pinyin</string>
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
				<key>tsInputModeKeyEquivalentModifiersKey</key>
				<integer>4608</integer>
				<key>tsInputModeMenuIconFileKey</key>
				<string>ChaosetIME-input.tiff</string>
				<key>tsInputModeAlternateMenuIconFileKey</key>
				<string>ChaosetIME-input.tiff</string>
				<key>tsInputModePaletteIconFileKey</key>
				<string>ChaosetIME-input.tiff</string>
				<key>tsInputModePrimaryInScriptKey</key>
				<true/>
				<key>tsInputModeScriptKey</key>
				<string>smUnicodeScript</string>
			</dict>
		</dict>
		<key>tsVisibleInputModeOrderedArrayKey</key>
		<array>
			<string>ime.chaoset.inputmethod.ChaosetIME.Pinyin</string>
		</array>
	</dict>
</dict>
</plist>
PLIST
printf 'APPL????' > "$APP/Contents/PkgInfo"

# 输入源显示名走 bundle 的本地化 InfoPlist.strings（对照 Squirrel 的
# InfoPlist.xcstrings：除 CFBundleDisplayName 外，还按输入源 ID 逐模式
# 命名）。
# 定名「Chaoset」：项目名是 chaos + set（混沌 + 集合），不是 chao + set，
# 故中英文统一用专名 Chaoset，不做「超集」这类音译（2026-10-22 用户确认）。
NAME_ZH="Chaoset"
NAME_EN="Chaoset"
for spec in "en:$NAME_EN" "zh-Hans:$NAME_ZH" "zh_CN:$NAME_ZH"; do
  lang="${spec%%:*}"
  name="${spec#*:}"
  dir="$APP/Contents/Resources/$lang.lproj"
  mkdir -p "$dir"
  cat > "$dir/InfoPlist.strings" <<STR
"CFBundleDisplayName" = "$name";
"CFBundleName" = "$name";
"ime.chaoset.inputmethod.ChaosetIME" = "$name";
"ime.chaoset.inputmethod.ChaosetIME.Hans" = "$name";
"ime.chaoset.inputmethod.ChaosetIME.Pinyin" = "$name";
STR
done
plutil -lint "$APP/Contents/Resources/"*/InfoPlist.strings

plutil -lint "$APP/Contents/Info.plist"
codesign --force -s - "$APP"

echo "[5/5] 组装图形化安装/设置 app（Chaoset.app）..."
# 面向不用命令行的用户：双击即可安装/更新、导入词库、看状态。
# 随包携带输入法壳、daemon、导入工具与内置常用词库，全部操作在 app 内完成。
SETUP=build/ChaosetSetup.app
rm -rf "$SETUP"
mkdir -p "$SETUP/Contents/MacOS" "$SETUP/Contents/Resources/zh-Hans.lproj"
swift_universal "$SETUP/Contents/MacOS/ChaosetSetup" AppKit Carbon UniformTypeIdentifiers -- setup/main.swift
(cd ../.. && go build -o shims/macos/build/chaoset-import ./cmd/chaoset-import)
cp -R "$APP" "$SETUP/Contents/Resources/ChaosetIME.embedded"
cp build/chaosetd build/chaoset-import build/tis build/smoke \
  ../../data/dict_common.tsv "$SETUP/Contents/Resources/"
cp build/Resources/ChaosetIME.icns "$SETUP/Contents/Resources/"
cat > "$SETUP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>zh-Hans</string>
	<key>CFBundleDisplayName</key>
	<string>Chaoset</string>
	<key>CFBundleExecutable</key>
	<string>ChaosetSetup</string>
	<key>CFBundleIconFile</key>
	<string>ChaosetIME</string>
	<key>CFBundleIdentifier</key>
	<string>ime.chaoset.setup</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>ChaosetSetup</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>0.1.0</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>LSMinimumSystemVersion</key>
	<string>15.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSPrincipalClass</key>
	<string>NSApplication</string>
</dict>
</plist>
PLIST
cat > "$SETUP/Contents/Resources/zh-Hans.lproj/InfoPlist.strings" <<'STR'
"CFBundleDisplayName" = "Chaoset 安装与设置";
"CFBundleName" = "Chaoset";
STR
printf 'APPL????' > "$SETUP/Contents/PkgInfo"
plutil -lint "$SETUP/Contents/Info.plist" "$SETUP/Contents/Resources/zh-Hans.lproj/InfoPlist.strings"
codesign --force -s - "$SETUP"

echo "已生成 $(pwd)/$APP (daemon: build/chaosetd) 与 $(pwd)/$SETUP"