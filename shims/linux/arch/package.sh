#!/bin/sh
set -e

SRC_DIR="$1"
OUT_PKG="$2"
# 版本号必须由调用方（Makefile 的 $(PKG_VERSION)）显式注入：tag 构建用 tag 号，其余为 0.0.0+<commit hash>。
# 此前这里有默认值 0.2.12，一旦忘记传参就会打出与 tag 不符的包。
PKG_VER="$3"
if [ -z "$PKG_VER" ]; then
    echo "错误：缺少版本号参数（由 Makefile 的 $(PKG_VERSION) 传入）" >&2
    exit 1
fi

if [ -z "$SRC_DIR" ] || [ -z "$OUT_PKG" ]; then
    echo "用法: $0 <staging_dir> <output_pkg> [version]" >&2
    exit 1
fi

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
TMP_DIR="$(mktemp -d /tmp/chaoset-arch-build.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

# 组装文件树
mkdir -p "$TMP_DIR/usr/bin"
mkdir -p "$TMP_DIR/usr/share/chaoset"
mkdir -p "$TMP_DIR/usr/share/ibus/component"
mkdir -p "$TMP_DIR/usr/share/icons"
mkdir -p "$TMP_DIR/usr/share/applications"

# 统一设置中心是设置界面的唯一实现：包里缺了它设置入口只剩报错，直接打包失败。
if [ ! -x "$SRC_DIR/bin/chaoset-settings" ]; then
    echo "错误：打包源缺少统一设置中心 bin/chaoset-settings" >&2
    exit 1
fi
cp -d "$SRC_DIR/bin/"* "$TMP_DIR/usr/bin/"
cp "$SRC_DIR/data/dict.tsv" "$TMP_DIR/usr/share/chaoset/"
cp "$SRC_DIR/data/dict.bin" "$TMP_DIR/usr/share/chaoset/dict.bin"
# 设置启动器：component XML 的 <setup> 指向 /usr/share/chaoset/chaoset-setup
# （纯 POSIX C，只 exec chaoset-settings）。此前 Arch 二进制包漏装该文件 →
# ibus-setup「首选项」点了没反应；deb/tar.gz 两个包本来就有，只有 Arch 缺。
cp "$SRC_DIR/setup/chaoset-setup" "$TMP_DIR/usr/share/chaoset/"
cp "$SRC_DIR/component/chaoset.xml" "$TMP_DIR/usr/share/ibus/component/"
cp -r "$SRC_DIR/icons/hicolor" "$TMP_DIR/usr/share/icons/"
cp "$SRC_DIR/chaoset-settings.desktop" "$TMP_DIR/usr/share/applications/chaoset-settings.desktop"

# FHS：/usr/local 属于系统管理员自建内容区域，发行包不得写入；旧版本遗留的
# /usr/local/bin 兼容链由 chaoset-ime.install 的安装/升级钩子清理。这里显式断言
# 打包结果不含 /usr/local，防止再次悄悄加回兼容链。
if [ -e "$TMP_DIR/usr/local" ]; then
    echo "错误：Arch 包不得写入 /usr/local（FHS；旧兼容链由 install 钩子清理）" >&2
    exit 1
fi

chmod 0755 "$TMP_DIR/usr/bin/"*
chmod 0755 "$TMP_DIR/usr/share/chaoset/chaoset-setup"
chmod 0644 "$TMP_DIR/usr/share/chaoset/dict.tsv" "$TMP_DIR/usr/share/chaoset/dict.bin"
chmod 0644 "$TMP_DIR/usr/share/ibus/component/chaoset.xml"

# 复制 install 钩子
cp "$SCRIPT_DIR/chaoset-ime.install" "$TMP_DIR/.INSTALL"
chmod 0644 "$TMP_DIR/.INSTALL"

# 计算已安装大小（字节）。
#
# 不能用 `du -sb`：BSD du（macOS 自带）不支持 -b，在开发机上打包会直接报
# "du: invalid option -- b" 然后因 set -e 中止（2026-10-26 实测）。
# `du -sk` 在 BSD 与 GNU 上都可用，× 1024 换算成字节即可。
TOTAL_SIZE=$(du -sk "$TMP_DIR/usr" | awk '{print $1 * 1024}')
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
# 打包命令要兼容 GNU tar 与 bsdtar（macOS 自带）。
#
# `--sort=name` 两个实现都不支持（它是 bsdtar 的选项名，实际不接受）：
# bsdtar 直接报 "Option --sort=name is not supported"，而管道会把退出码吞掉，
# 结果是产出一个只有 zstd 头、没有任何内容的 13 字节坏包（2026-10-26 实测）。
# 这里按 tar 实现分支：GNU tar 用 --sort=name 保证可复现；bsdtar 用
# --uid/--gid 归零（不支持 --sort，顺序由文件系统决定，可接受）。
if tar --version 2>/dev/null | grep -q 'GNU tar'; then
    TAR_SORT="--sort=name"
    TAR_OWNER="--owner=0 --group=0 --numeric-owner"
else
    TAR_SORT=""
    TAR_OWNER="--uid=0 --gid=0 --uname=root --gname=root"
fi
# shellcheck disable=SC2086
(cd "$TMP_DIR" && tar $TAR_SORT $TAR_OWNER -cf - .PKGINFO .INSTALL usr | zstd -c -T0 > "$REAL_OUT_PKG")

# 产物自检：包不能是空的。此前管道吞掉 tar 的错误码，坏包照样"成功生成"。
if [ ! -s "$REAL_OUT_PKG" ]; then
    echo "错误：生成的 Arch 包为空（tar 失败但错误码被管道吞掉）" >&2
    exit 1
fi
if ! zstd -dc "$REAL_OUT_PKG" 2>/dev/null | tar -tf - >/dev/null 2>&1; then
    echo "错误：生成的 Arch 包无法解包（内容损坏）" >&2
    exit 1
fi
echo "Arch Linux 包已生成: $REAL_OUT_PKG"
