#!/bin/sh
# 构建 Linux 原生 ibus 引擎（C++/libibus + 共享 C++ IPC 核心）。
# 用法：shims/linux/engine/build.sh [输出路径]
set -eu

OUT=${1:-chaoset-engine}
case "$OUT" in
    /*) ;;
    *) OUT="$(pwd)/$OUT" ;;
esac
cd "$(dirname "$0")"
CXX=${CXX:-g++}

# shellcheck disable=SC2086
$CXX -std=c++17 -O2 -Wall -Wextra \
    -I. -I../../common/ipc \
    $(pkg-config --cflags ibus-1.0) \
    main.cpp daemon.cpp ../../common/ipc/core.cpp \
    -o "$OUT" \
    $(pkg-config --libs ibus-1.0)

echo "built $OUT"