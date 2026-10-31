#!/usr/bin/env python3
# Chaoset 输入法设置窗口（Linux 端，GTK3）。
#
# 入口：ibus-setup 选中 Chaoset 后的「首选项」按钮（component XML 的
# <setup> 命令）；也可直接运行本脚本。
#
# 与 macOS SettingsWindow.swift / Windows 设置窗口三端同构：
#   标题「Chaoset 设置」；分组「输入行为」；「词组联想」开关；
#   分组「学习数据」（统计 / 清空 / 导出 TSV，2026-10-31 起）；
#   说明文字；底部「更多设置将在后续版本提供」。
#
# 配置文件：$XDG_CONFIG_HOME/chaoset/config.json（缺省 ~/.config/chaoset/），
# 键 "associate"：缺省（未写）开启，显式 false 关闭——与 ibus-engine
# config.go 的 *bool nil=开启语义一致。保存即生效：引擎在每次进入联想前
# 重读本文件（ibusEngine.associateEnabled），无需重启 ibus/引擎进程，已打开
# 的输入上下文下一次选词就按新设置执行（2026-10-30 起与 macOS/Windows
# 「每次选词前重读设置」同语义）。
#
# 学习数据操作全部在 userdict_ctl.py（无 GTK 依赖，便于 CI 单测）：只调用随包
# chaoset-userdict 的本地文件模式，engine 包零改动（保持纯拼音逻辑）。

import json
import os
import sys

# 与本脚本同目录（安装后 /usr/share/chaoset/）。
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
try:
    import userdict_ctl  # noqa: E402 —— 需先插入同目录到 sys.path
except ImportError as exc:
    print("缺少 userdict_ctl.py（安装包不完整）：%s" % exc, file=sys.stderr)
    sys.exit(1)

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

    # —— 学习数据（普通用户 GUI 入口；CLI 仅高级/无 GUI 环境兜底）——
    data_group = Gtk.Label(xalign=0)
    data_group.set_markup("<b>学习数据</b>")
    box.pack_start(data_group, False, False, 0)

    stats_label = Gtk.Label(xalign=0)
    stats_label.set_line_wrap(True)
    stats_label.set_max_width_chars(40)
    stats_label.set_text("读取中…")
    box.pack_start(stats_label, False, False, 0)

    data_row = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL, spacing=12)
    clear_btn = Gtk.Button(label="清空学习数据…")
    export_btn = Gtk.Button(label="导出学习数据…")
    data_row.pack_start(clear_btn, False, False, 0)
    data_row.pack_start(export_btn, False, False, 0)
    box.pack_start(data_row, False, False, 0)

    data_hint = Gtk.Label(xalign=0)
    data_hint.set_line_wrap(True)
    data_hint.set_max_width_chars(40)
    data_hint.get_style_context().add_class("dim-label")
    data_hint.set_text("学习数据仅保存在本机，用于调频与词组联想；清空后不可恢复。")
    box.pack_start(data_hint, False, False, 0)

    more = Gtk.Label(xalign=0)
    more.get_style_context().add_class("dim-label")
    more.set_text("更多设置将在后续版本提供")
    box.pack_start(more, False, False, 0)

    def on_toggled(switch, _data=None):
        cfg["associate"] = bool(switch.get_active())
        save_config(cfg)

    sw.connect("state-set", on_toggled)

    def show_message(kind, text):
        # 用纯文本（不 set_markup）：错误信息里可能含 < > 等字符，
        # 交给 Pango 解析会吞内容或报错。
        dlg = Gtk.MessageDialog(transient_for=win, modal=True, message_type=kind,
                                buttons=Gtk.ButtonsType.OK, text=text)
        dlg.run()
        dlg.destroy()

    def refresh_stats():
        ok, text = userdict_ctl.stats()
        stats_label.set_text(text if ok else "无法读取学习数据：%s" % text)

    def on_clear(_btn):
        dlg = Gtk.MessageDialog(transient_for=win, modal=True,
                                message_type=Gtk.MessageType.WARNING,
                                buttons=Gtk.ButtonsType.OK_CANCEL,
                                text="清空学习数据？")
        dlg.format_secondary_text(
            "将删除全部调频词、用户词组与上下文共现记录（仅本机），操作不可恢复。\n"
            "输入法正在运行时，需要先重启 ibus（期间输入法短暂不可用）。")
        resp = dlg.run()
        dlg.destroy()
        if resp != Gtk.ResponseType.OK:
            return
        win.set_sensitive(False)
        try:
            ok, text = userdict_ctl.clear_learning_data()
        finally:
            win.set_sensitive(True)
        show_message(Gtk.MessageType.INFO if ok else Gtk.MessageType.ERROR, text)
        refresh_stats()

    def on_export(_btn):
        chooser = Gtk.FileChooserDialog(
            title="导出学习数据", transient_for=win,
            action=Gtk.FileChooserAction.SAVE,
            buttons=(Gtk.STOCK_CANCEL, Gtk.ResponseType.CANCEL,
                     Gtk.STOCK_SAVE, Gtk.ResponseType.OK))
        chooser.set_do_overwrite_confirmation(True)
        chooser.set_current_name("chaoset-userdict.tsv")
        filt = Gtk.FileFilter()
        filt.set_name("TSV 文件 (*.tsv)")
        filt.add_pattern("*.tsv")
        chooser.add_filter(filt)
        resp = chooser.run()
        path = chooser.get_filename()
        chooser.destroy()
        if resp != Gtk.ResponseType.OK or not path:
            return
        ok, text = userdict_ctl.export(path)
        show_message(Gtk.MessageType.INFO if ok else Gtk.MessageType.ERROR, text)

    clear_btn.connect("clicked", on_clear)
    export_btn.connect("clicked", on_export)

    win.show_all()
    refresh_stats()  # 窗口显示后再读，避免读取慢拖慢打开
    Gtk.main()


if __name__ == "__main__":
    main()