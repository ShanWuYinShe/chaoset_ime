#!/bin/sh
# 版本号单一真源（2026-11-06 用户规则）：
#
#   * **只有 GitHub CI 上、由 push tag 触发的构建**才用 tag 里的版本号（去 v）；
#   * **其他一切情况**（本地构建、日常 CI、workflow_dispatch、PR、源码直跑）
#     都用当前 git commit hash（12 位），不再取「最近的 tag」。
#
# 动机（用户原话）：「本地测试的时候不想一直累加版本号，同时又想知道我本地
# 测试的到底是什么版本，方便排查问题」。以前 make version 用 git describe
# --tags，本地/日常 CI 打出来的包永远显示最近一次发布的 tag，根本分不清装的是
# 哪个提交；commit hash 才是构建的真实身份，而且不需要为每次测试累加版本号。
# 工作区有未提交改动时追加 -dirty：同一次提交但代码不同的本地构建也能区分。
#
# 用法（所有输出都是单行，供 Makefile / 打包脚本 / CI 注入）：
#   scripts/version.sh                 显示用版本（tag 号或 commit hash[-dirty]）
#   scripts/version.sh --quad          4 段数字版本（NSIS / PE VS_FIXEDFILEINFO）
#   scripts/version.sh --pkg           包管理器版本（deb / Arch，保证以数字开头）
#   scripts/version.sh --source        来源说明（tag / commit / nogit），用于日志
#
# tag 触发判据（全部满足才算）：GITHUB_ACTIONS=true、GITHUB_EVENT_NAME=push、
# GITHUB_REF_TYPE=tag 且 GITHUB_REF_NAME 像 v*；或调用方显式 --tag <号>
# （本地复跑同一条打包链用）。workflow_dispatch 即使选了 tag ref 也不算。
#
# 派生规则（纯函数，同一输入永远同一输出）：
#   --quad：tag 版 x.y.z → x.y.z.0；commit hash → 0.0.A.B，A/B 取 hash 前 8 位
#           十六进制每 4 位转成的十进制数（≤65535）。Windows 版本资源只接受
#           4 段数字，显示用版本仍是原始 hash[-dirty]。
#   --pkg ：dpkg 的 Version 与 Arch 的 pkgver 必须是合法版本串；裸 hash 可能以
#           字母开头（dpkg 直接拒绝）。统一为 0.0.0+<hash>[.dirty]：以数字开头、
#           含完整 hash、每次提交都不同；人类可读字段仍显示裸 hash。
#   不在 git 仓库（源码 tarball）时显示 nogit，--pkg 为 0.0.0+nogit。
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

# 把 4 位十六进制串转成十进制（POSIX awk，不依赖 bash / printf 进制）。
hex4dec() {
    printf '%s' "$1" | awk '
        { h = tolower($0); n = 0;
          for (i = 1; i <= 4; i++) {
              d = index("0123456789abcdef", substr(h, i, 1)) - 1;
              if (d < 0) { exit 1 }
              n = n * 16 + d
          }
          print n }'
}

tag_version=""
case "${1:-}" in
    --tag)
        [ $# -ge 2 ] || { echo "错误：--tag 需要版本号参数" >&2; exit 2; }
        tag_version="$2"
        shift 2
        ;;
esac

mode="${1:-display}"
case "$mode" in
    display|--quad|--pkg|--source) ;;
    *) echo "用法: $0 [--tag <版本号>] [display|--quad|--pkg|--source]" >&2; exit 2 ;;
esac

# 是否「tag 触发的 GitHub CI」：显式 --tag 优先，其次要求 push + tag 三个条件。
if [ -n "$tag_version" ]; then
    version=$(printf '%s' "$tag_version" | sed 's/^v//')
    source="tag"
elif [ "${GITHUB_ACTIONS:-}" = "true" ] \
     && [ "${GITHUB_EVENT_NAME:-}" = "push" ] \
     && [ "${GITHUB_REF_TYPE:-}" = "tag" ] \
     && printf '%s' "${GITHUB_REF_NAME:-}" | grep -Eq '^v[0-9]'; then
    version=$(printf '%s' "$GITHUB_REF_NAME" | sed 's/^v//')
    source="tag"
else
    version=$(git -C "$ROOT" rev-parse --short=12 HEAD 2>/dev/null || true)
    if [ -n "$version" ]; then
        source="commit"
        # 未提交的本地改动（排除未跟踪文件，构建产物不会误判）让同一 hash 的
        # 不同本地构建可区分；CI checkout 是干净工作区，不会带这个后缀。
        if [ -n "$(git -C "$ROOT" status --porcelain --untracked-files=no 2>/dev/null)" ]; then
            version="${version}-dirty"
        fi
    else
        version="nogit"
        source="nogit"
    fi
fi

# 数字/包版本只取 hash 主体，丢掉 -dirty 等后缀。
base=$(printf '%s' "$version" | sed 's/-.*$//')

case "$mode" in
    display)
        printf '%s\n' "$version"
        ;;
    --source)
        printf '%s\n' "$source"
        ;;
    --quad)
        if [ "$source" = "tag" ]; then
            printf '%s\n' "$base" \
                | sed -E 's/^v//; s/[^0-9.].*$//; s/[.]+$//' \
                | awk -F. '{print ($1+0)"."($2+0)"."($3+0)"."($4+0)}'
        else
            hex=$(printf '%s' "$base" | tr -cd '0-9a-fA-F' | cut -c1-8)
            while [ "${#hex}" -lt 8 ]; do hex="${hex}0"; done
            printf '0.0.%s.%s\n' "$(hex4dec "$(printf '%s' "$hex" | cut -c1-4)")" \
                "$(hex4dec "$(printf '%s' "$hex" | cut -c5-8)")"
        fi
        ;;
    --pkg)
        if [ "$source" = "tag" ]; then
            # tag 号本身已合法（以数字开头）；保留预发布后缀，只去掉前导 v。
            printf '%s\n' "$(printf '%s' "$version" | sed 's/^v//')"
        else
            # dpkg 要求 Version 以数字开头；Arch pkgver 也接受 +。
            # -dirty 换成 .dirty，避免被 deb 当成 debian_revision 分隔符。
            suffix=$(printf '%s' "$version" | sed "s/^[^.-]*//; s/^-/./")
            printf '0.0.0+%s%s\n' "$base" "$suffix"
        fi
        ;;
esac
