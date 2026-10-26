#!/bin/sh
set -e

SRC_DIR="$1"
OUT_PKG="$2"
PKG_VER="${3:-0.2.7}"

if [ -z "$SRC_DIR" ] || [ -z "$OUT_PKG" ]; then
    echo "用法: $0 <staging_dir> <output_pkg> [version]" >&2
    exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
TMP_DIR="$(mktemp -d /tmp/chaoset-arch-build.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

# 组装文件树
mkdir -p "$TMP_DIR/usr/bin"
mkdir -p "$TMP_DIR/usr/local/bin"
mkdir -p "$TMP_DIR/usr/share/chaoset"
mkdir -p "$TMP_DIR/usr/share/ibus/component"
mkdir -p "$TMP_DIR/usr/share/icons"

cp "$SRC_DIR/bin/"* "$TMP_DIR/usr/bin/"
cp "$SRC_DIR/data/dict.tsv" "$TMP_DIR/usr/share/chaoset/"
cp "$SRC_DIR/component/chaoset.xml" "$TMP_DIR/usr/share/ibus/component/"
cp -r "$SRC_DIR/icons/hicolor" "$TMP_DIR/usr/share/icons/"

# 创建兼容软链接到 /usr/local/bin
ln -sf /usr/bin/chaoset-engine "$TMP_DIR/usr/local/bin/chaoset-engine"
ln -sf /usr/bin/chaosetd "$TMP_DIR/usr/local/bin/chaosetd"
ln -sf /usr/bin/chaoset-cli "$TMP_DIR/usr/local/bin/chaoset-cli"

chmod 0755 "$TMP_DIR/usr/bin/"*
chmod 0644 "$TMP_DIR/usr/share/chaoset/dict.tsv"
chmod 0644 "$TMP_DIR/usr/share/ibus/component/chaoset.xml"

# 复制 install 钩子
cp "$SCRIPT_DIR/chaoset-ime.install" "$TMP_DIR/.INSTALL"
chmod 0644 "$TMP_DIR/.INSTALL"

# 计算已安装大小 (以字节计算)
TOTAL_SIZE=$(du -sb "$TMP_DIR/usr" | awk '{print $1}')
BUILD_DATE=$(date +%s)

# 生成 .PKGINFO
cat << EOF > "$TMP_DIR/.PKGINFO"
pkgname = chaoset-ime
pkgbase = chaoset-ime
xdata = pkgtype=pkg
pkgver = ${PKG_VER}-1
pkgdesc = Chaoset Pinyin IME - 傻瓜式现代拼音输入法
url = https://github.com/ShanWuYinShe/chaoset_ime
builddate = ${BUILD_DATE}
packager = ShanWuYinShe <https://github.com/ShanWuYinShe/chaoset_ime>
size = ${TOTAL_SIZE}
arch = x86_64
license = MIT
depend = ibus
EOF
chmod 0644 "$TMP_DIR/.PKGINFO"

mkdir -p "$(dirname "$OUT_PKG")"
# 使用真实绝对路径写入输出包
REAL_OUT_PKG="$(cd "$(dirname "$OUT_PKG")" && pwd)/$(basename "$OUT_PKG")"
(cd "$TMP_DIR" && tar --sort=name --owner=0 --group=0 --numeric-owner -cf - .PKGINFO .INSTALL usr | zstd -c -T0 > "$REAL_OUT_PKG")
echo "Arch Linux 包已生成: $REAL_OUT_PKG"
