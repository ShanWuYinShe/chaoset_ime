#!/usr/bin/env python3
# dictimport_ctl.py：Linux 设置窗口的「词库导入」操作（无 GTK，便于 CI 单测）。
#
# 架构边界（2026-10-31 用户要求）：engine 包保持绝对纯净，只做拼音匹配，
# 不引入任何平台相关代码。因此本模块：
#   - 不改 engine/；只调用随包 `chaoset-import` 把用户词库合并到
#     $XDG_DATA_HOME/chaoset/dict.tsv（用户可写，不需要 root）；
#   - ibus 引擎启动时自动加载该文件（-imported-dict，见 ibus-engine/main.go），
#     导入后重启 ibus 引擎让新词库立即生效；
#   - 替换用「临时文件 + 备份 + os.replace」保证原子性，导入失败旧词库不受影响。
#
# 无 GTK 依赖：便于在无桌面环境的 CI 里对编排逻辑做单测
# （见 test_dictimport_ctl.py）。

import os
import shutil
import subprocess
import time


def imported_dict_path():
    """与 ibus 引擎 main.go 的 defaultImportedDictPath 同口径。"""
    base = os.environ.get("XDG_DATA_HOME") or os.path.expanduser("~/.local/share")
    return os.path.join(base, "chaoset", "dict.tsv")


def find_tool():
    """定位 chaoset-import：优先 PATH（安装包装到 /usr/bin），
    其次源码树/解包形态的 bin/ 目录。找不到返回 None。"""
    exe = shutil.which("chaoset-import")
    if exe:
        return exe
    here = os.path.dirname(os.path.abspath(__file__))
    for rel in ("../../../bin/chaoset-import", "../../bin/chaoset-import",
                "../bin/chaoset-import"):
        cand = os.path.normpath(os.path.join(here, rel))
        if os.path.isfile(cand) and os.access(cand, os.X_OK):
            return cand
    return None


def _run(args, timeout=60):
    return subprocess.run(args, capture_output=True, text=True, timeout=timeout)


def _restart_engines():
    import userdict_ctl  # 同目录；复用 ibus 退出/重启编排
    return userdict_ctl.restart_engines()


def import_files(paths, target=None, tool=None, restart=None):
    """把词库文件导入用户词库并重启 ibus 引擎；返回 (ok, message)。

    paths 为源文件（Rime dict.yaml / CC-CEDICT txt / TSV，可多个）；已有用户
    词库作为底表一起传给 chaoset-import（重复词条取较大词频）。
    """
    paths = [p for p in (paths or []) if p]
    if not paths:
        return False, "未选择词库文件"
    target = target or imported_dict_path()
    tool = tool or find_tool()
    if tool is None:
        return False, "未找到 chaoset-import（请重新安装）"
    restart = restart or _restart_engines

    try:
        os.makedirs(os.path.dirname(target), exist_ok=True)
    except OSError as exc:
        return False, "创建词库目录失败：%s" % exc

    tmp = target + ".tmp"
    try:
        os.unlink(tmp)
    except OSError:
        pass

    args = [tool, "-o", tmp]
    if os.path.isfile(target):
        args.append(target)  # 已有用户词库作为底表
    args += list(paths)
    try:
        r = _run(args)
    except (OSError, subprocess.SubprocessError) as exc:
        _cleanup(tmp)
        return False, "导入失败：%s" % exc
    if r.returncode != 0:
        _cleanup(tmp)
        return False, (r.stderr or r.stdout or "导入失败").strip()
    if not os.path.isfile(tmp):
        _cleanup(tmp)
        return False, "导入失败：未生成词库文件"

    # 备份旧词库后原子替换：导入失败/中断时旧文件保持原样。
    try:
        if os.path.isfile(target):
            bak = "%s.bak.%s" % (target, time.strftime("%Y%m%d-%H%M%S"))
            shutil.copy2(target, bak)
        os.replace(tmp, target)
    except OSError as exc:
        _cleanup(tmp)
        return False, "写入词库失败（旧词库保持不变）：%s" % exc

    ok, msg = restart()
    if not ok:
        return False, "词库已导入，但 %s" % msg
    detail = (r.stdout or "").strip()
    if detail:
        detail = "；" + detail
    return True, "已导入到 %s%s；%s" % (target, detail, msg)


def _cleanup(path):
    try:
        os.unlink(path)
    except OSError:
        pass


if __name__ == "__main__":
    print(imported_dict_path())