#!/bin/sh
# 构建 Linux 设置启动器 chaoset-setup（纯 POSIX C）。
#
# 它只负责找到并启动 Slint 统一设置中心 chaoset-settings：不含 UI、不链接 GTK，
# 因此不需要 pkg-config 与任何 GTK 开发包，普通 C 编译器即可。
# 用法：shims/linux/setup/build.sh [输出路径]
set -eu

OUT=${1:-chaoset-setup}
# 输出路径必须在 cd 前解析成绝对路径：打包脚本传的是相对仓库根目录的路径。
case "$OUT" in
    /*) ;;
    *) OUT="$(pwd)/$OUT" ;;
esac
cd "$(dirname "$0")"
CC=${CC:-cc}

$CC -std=c11 -O2 -Wall -Wextra -Werror -o "$OUT" chaoset-setup.c

echo "built $OUT"
