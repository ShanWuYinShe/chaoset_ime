#!/usr/bin/env python3
# Chaoset 输入法设置窗口（Linux 端，GTK3）。
#
# 入口：ibus-setup 选中 Chaoset 后的「首选项」按钮（component XML 的
# <setup> 命令）；也可直接运行本脚本。
#
# 与 macOS SettingsWindow.swift / Windows 设置窗口三端同构：
#   标题「Chaoset 设置」；分组「输入行为」；「词组联想」开关；
#   说明文字；底部「更多设置将在后续版本提供」。
#
# 配置文件：$XDG_CONFIG_HOME/chaoset/config.json（缺省 ~/.config/chaoset/），
# 键 "associate"：缺省（未写）开启，显式 false 关闭——与 ibus-engine
# config.go 的 *bool nil=开启语义一致。保存后对新开的输入上下文生效；
# 要对所有已开窗口立即生效可运行 `ibus restart`。

import json
import os
import sys

try:
    import gi
    gi.require_version("Gtk", "3.0")
    from gi.repository import Gtk
except Exception as exc:  # noqa: BLE001 —— 无头环境/缺 GTK 时给出可读错误
    print("需要 GTK3（Debian/Ubuntu 装 python3-gi）：%s" % exc, file=sys.stderr)
    sys.exit(1)

CONFIG_DIR = os.path.join(
    os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config"), "chaoset")
CONFIG_PATH = os.path.join(CONFIG_DIR, "config.json")


def load_config():
    try:
        with open(CONFIG_PATH, "r", encoding="utf-8") as fh:
            cfg = json.load(fh)
        return cfg if isinstance(cfg, dict) else {}
    except (OSError, ValueError):
        return {}


def save_config(cfg):
    os.makedirs(CONFIG_DIR, exist_ok=True)
    tmp = CONFIG_PATH + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(cfg, fh, ensure_ascii=False, indent=2)
        fh.write("\n")
    os.replace(tmp, CONFIG_PATH)  # 原子替换，避免引擎读到半截 JSON


def main():
    cfg = load_config()
    associate = cfg.get("associate")
    enabled = True if associate is None else bool(associate)

    win = Gtk.Window(title="Chaoset 设置")
    win.set_resizable(False)
    win.set_border_width(24)
    win.set_position(Gtk.WindowPosition.CENTER)
    win.connect("destroy", Gtk.main_quit)

    box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
    win.add(box)

    group = Gtk.Label(xalign=0)
    group.set_markup("<b>输入行为</b>")
    box.pack_start(group, False, False, 0)

    row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=12)
    row_label = Gtk.Label(label="词组联想", xalign=0)
    sw = Gtk.Switch(active=enabled)
    row.pack_start(row_label, False, False, 0)
    row.pack_end(sw, False, False, 0)
    box.pack_start(row, False, False, 0)

    hint = Gtk.Label(xalign=0)
    hint.set_line_wrap(True)
    hint.set_max_width_chars(40)
    hint.get_style_context().add_class("dim-label")
    hint.set_text("选词上屏后展示可续接的词组候选；关闭后不再弹出联想候选。")
    box.pack_start(hint, False, False, 0)

    more = Gtk.Label(xalign=0)
    more.get_style_context().add_class("dim-label")
    more.set_text("更多设置将在后续版本提供")
    box.pack_start(more, False, False, 0)

    def on_toggled(switch, _data=None):
        cfg["associate"] = bool(switch.get_active())
        save_config(cfg)

    sw.connect("state-set", on_toggled)

    win.show_all()
    Gtk.main()


if __name__ == "__main__":
    main()
