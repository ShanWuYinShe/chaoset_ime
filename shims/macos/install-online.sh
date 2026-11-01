#!/bin/bash
# macOS 免 Gatekeeper 隔离安装入口（无需 Apple 开发者账号）。
#
# 通过 curl 下载 release zip 后，用 ditto 解包并清除 com.apple.quarantine，
# 再启动「Chaoset 安装与设置」。这样 app 不会带有浏览器下载产生的隔离属性，
# 因而不会触发“Apple 无法验证开发者”的提示。
#
# 用法（在「终端」粘贴一行）：
#   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/ShanWuYinShe/chaoset_ime/main/shims/macos/install-online.sh)"
#
# 也可先下载本脚本人工检查；或指定本地 zip：
#   CHAOSET_ZIP=/path/to/chaoset-macos.zip CHAOSET_NO_LAUNCH=1 bash install-online.sh
set -euo pipefail

REPO="https://github.com/ShanWuYinShe/chaoset_ime"
ZIP_URL="$REPO/releases/latest/download/chaoset-macos.zip"
TMP=$(mktemp -d "${TMPDIR:-/tmp}/chaoset-install.XXXXXX")
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

echo "==> 准备下载 Chaoset 安装器"
if [ -n "${CHAOSET_ZIP:-}" ]; then
  [ -f "$CHAOSET_ZIP" ] || { echo "错误：找不到 $CHAOSET_ZIP" >&2; exit 1; }
  cp "$CHAOSET_ZIP" "$TMP/chaoset-macos.zip"
else
  command -v curl >/dev/null 2>&1 || { echo "错误：系统缺少 curl" >&2; exit 1; }
  curl -fL --retry 3 --connect-timeout 20 -o "$TMP/chaoset-macos.zip" "$ZIP_URL"
fi

echo "==> 解包并清除 quarantine 隔离标记"
ditto -x -k "$TMP/chaoset-macos.zip" "$TMP/unpack"
SETUP_SRC="$TMP/unpack/ChaosetSetup.app"
[ -d "$SETUP_SRC" ] || { echo "错误：压缩包中缺少 ChaosetSetup.app" >&2; exit 1; }
xattr -dr com.apple.quarantine "$TMP/unpack" 2>/dev/null || true

echo "==> 安装「Chaoset 安装与设置」到 ~/Applications"
mkdir -p "$HOME/Applications"
rm -rf "$HOME/Applications/ChaosetSetup.app"
ditto "$SETUP_SRC" "$HOME/Applications/ChaosetSetup.app"
xattr -dr com.apple.quarantine "$HOME/Applications/ChaosetSetup.app" 2>/dev/null || true

if [ "${CHAOSET_NO_LAUNCH:-0}" = "1" ]; then
  echo "==> 已安装到 $HOME/Applications/ChaosetSetup.app（CHAOSET_NO_LAUNCH=1，未启动）"
  exit 0
fi

echo "==> 启动安装器；后续在窗口里点「一键安装 / 更新」"
open "$HOME/Applications/ChaosetSetup.app"