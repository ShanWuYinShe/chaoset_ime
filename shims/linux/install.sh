#!/bin/sh
# chaoset Linux 安装脚本（ibus engine）。
#
# 用法：解包后在本目录执行
#   sudo ./install.sh            # 安装（词库装到 /usr/share/chaoset）
#   sudo ./install.sh --uninstall  # 卸载
#
# 装完需要重启 ibus-daemon（脚本会尝试自动做），再在
# 「设置 → 键盘 → 输入源」里添加 Chaoset Pinyin。
set -e
cd "$(dirname "$0")"

PREFIX_BIN=/usr/bin
PREFIX_LOCAL_BIN=/usr/local/bin
PREFIX_SHARE=/usr/share/chaoset
COMPONENT_DIR=/usr/share/ibus/component
ICON_ROOT=/usr/share/icons

uninstall() {
  echo "==> 卸载 chaoset"
  rm -f "$PREFIX_BIN/chaosetd" "$PREFIX_BIN/chaoset-cli" \
        "$PREFIX_BIN/chaoset-import" "$PREFIX_BIN/chaoset-userdict" \
        "$PREFIX_BIN/chaoset-engine"
  rm -f "$PREFIX_LOCAL_BIN/chaosetd" "$PREFIX_LOCAL_BIN/chaoset-cli" \
        "$PREFIX_LOCAL_BIN/chaoset-import" "$PREFIX_LOCAL_BIN/chaoset-userdict" \
        "$PREFIX_LOCAL_BIN/chaoset-engine"
  rm -f "$COMPONENT_DIR/chaoset.xml"
  rm -rf "$PREFIX_SHARE"
  # 设置窗口脚本随 PREFIX_SHARE 目录一起删除（chaoset-setup.py 在其下）
  # 图标按 hicolor 目录逐个删（不整目录删，避免误删系统图标）
  find "$ICON_ROOT/hicolor" -path '*apps/chaoset.*' -delete 2>/dev/null || true
  echo "==> 已卸载。重启 ibus-daemon 生效：ibus restart"
}

restart_ibus() {
  # ibus 在用户会话里；root 下 ibus 命令通常不可用，给出明确提示即可。
  if command -v ibus >/dev/null 2>&1; then
    ibus restart >/dev/null 2>&1 && echo "==> 已重启 ibus-daemon" || true
  fi
}

if [ "$1" = "--uninstall" ]; then
  uninstall
  exit 0
fi

if [ "$(id -u)" -ne 0 ]; then
  echo "需要 root 权限安装到系统目录，请用 sudo 运行：sudo ./install.sh" >&2
  exit 1
fi

# 词库是必需的：缺了引擎会在启动时直接退出（ibus 里表现为打不出字）。
if [ ! -f data/dict.tsv ]; then
  echo "错误：包内缺少词库 data/dict.tsv，安装包不完整" >&2
  exit 1
fi

# 设置窗口脚本（ibus-setup 的 <setup> 入口；GTK3 设置窗与 macOS/Windows 同构）
#
# 必须先建目录再 install：deb 安装时 dpkg 会创建 /usr/share/chaoset，但
# tar.gz 安装（sudo ./install.sh）没有任何东西保证该目录存在。2026-10-29 加入
# 设置窗口时本段被排在下面的 install -d 之前，于是**所有 tar.gz 用户安装即失败**：
#   install: cannot create regular file '/usr/share/chaoset/chaoset-setup.py':
#   No such file or directory
# deb 冒烟先建了目录、随后随 dpkg -r 被删，正好轮到 tar.gz 冒烟才暴露——v0.2.15
# 与 v0.2.16 两次 release 都挂在这里（2026-10-30 修）。
if [ -f setup/chaoset-setup.py ]; then
  echo "==> 安装设置窗口到 $PREFIX_SHARE"
  install -d "$PREFIX_SHARE"
  install -m755 setup/chaoset-setup.py "$PREFIX_SHARE/chaoset-setup.py"
  # 设置窗的「学习数据」操作模块（2026-10-31）；与设置窗同目录，缺失时
  # GTK 脚本 import 失败 → 整个设置窗打不开，所以必须随 setup 一起装。
  install -m755 setup/userdict_ctl.py "$PREFIX_SHARE/userdict_ctl.py"
fi

echo "==> 安装二进制到 $PREFIX_BIN"
install -d "$PREFIX_BIN"
install -d "$PREFIX_LOCAL_BIN"
install -d "$PREFIX_SHARE"
for b in chaosetd chaoset-cli chaoset-import chaoset-userdict chaoset-engine; do
  install -m 0755 "bin/$b" "$PREFIX_BIN/$b"
  ln -sf "$PREFIX_BIN/$b" "$PREFIX_LOCAL_BIN/$b" 2>/dev/null || true
done

echo "==> 安装词库到 $PREFIX_SHARE"
install -d "$PREFIX_SHARE"
install -m 0644 data/dict.tsv "$PREFIX_SHARE/dict.tsv"

echo "==> 安装 ibus 组件描述到 $COMPONENT_DIR"
install -d "$COMPONENT_DIR"
install -m 0644 component/chaoset.xml "$COMPONENT_DIR/chaoset.xml"

if [ -d icons/hicolor ]; then
  echo "==> 安装图标"
  cp -r icons/hicolor "$ICON_ROOT/" 2>/dev/null || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 && \
    gtk-update-icon-cache -f -t "$ICON_ROOT/hicolor" >/dev/null 2>&1 || true
fi

# 冒烟自检：按 ibus 的方式（无 cwd 依赖）拉起引擎一次，确认词库能找到。
# 这一步是必须的——2026-10-25 的发行缺陷正是「装完引擎起不来」，
# 若只装不验，用户会在 ibus 里看到输入法却打不出任何字。
echo "==> 自检：确认引擎能找到词库"
if ! ( cd / && timeout 5 chaoset-engine --ibus >/tmp/chaoset-selfcheck.log 2>&1 || true ); then
  : # timeout 杀掉后台常驻进程属正常
fi
if grep -q '打开词库.*失败' /tmp/chaoset-selfcheck.log 2>/dev/null; then
  echo "错误：引擎找不到词库，安装不完整。日志：" >&2
  cat /tmp/chaoset-selfcheck.log >&2
  exit 1
fi
echo "    引擎自检通过"

restart_ibus
echo
echo "==> 安装完成。请在「设置 → 键盘 → 输入源」里添加 Chaoset Pinyin。"
echo "    如列表里没有，请注销重登或执行：ibus restart"
echo "    卸载：sudo ./install.sh --uninstall"
