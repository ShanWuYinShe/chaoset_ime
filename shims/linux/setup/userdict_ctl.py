#!/usr/bin/env python3
# userdict_ctl.py：Linux 设置窗口的「学习数据」操作（统计 / 清空 / 导出）。
#
# 架构边界（2026-10-31 用户明确要求）：engine 包保持绝对纯净，只做拼音匹配，
# 不引入任何平台相关代码。因此本模块：
#   - 不改 engine/，也不改 ibus 引擎壳；
#   - 只调用随包 `chaoset-userdict` 的**本地文件模式**（-userdict FILE）完成
#     stats / clear / export（与 daemon 模式共用同一份 engine.UserDict 逻辑）；
#   - 清空时由本模块编排 ibus 的退出/重启：引擎内存态在延迟落盘（≤2s）时会把
#     旧数据写回文件，所以必须确认没有 chaoset-engine 进程存活后才清空文件。
#
# 无 GTK 依赖：便于在无桌面环境的 CI 里对编排逻辑做单测
# （见 test_userdict_ctl.py）。

import os
import shutil
import signal
import subprocess
import time


def userdict_path():
    """与 ibus 引擎 main.go 的 defaultUserDictPath 同口径。"""
    base = os.environ.get("XDG_DATA_HOME") or os.path.expanduser("~/.local/share")
    return os.path.join(base, "chaoset", "userdict.json")


def find_tool():
    """定位 chaoset-userdict：优先 PATH（安装包装到 /usr/bin），
    其次源码树/解包形态的 bin/ 目录。找不到返回 None。"""
    exe = shutil.which("chaoset-userdict")
    if exe:
        return exe
    here = os.path.dirname(os.path.abspath(__file__))
    for rel in ("../../../bin/chaoset-userdict", "../../bin/chaoset-userdict",
                "../bin/chaoset-userdict"):
        cand = os.path.normpath(os.path.join(here, rel))
        if os.path.isfile(cand) and os.access(cand, os.X_OK):
            return cand
    return None


def _run(args, timeout=20):
    return subprocess.run(args, capture_output=True, text=True, timeout=timeout)


def _run_tool(tool, args):
    return _run([tool] + list(args))


def stats(path=None):
    """返回 (ok, text)。text 形如「词 3 条 / 词组 1 条 / 上下文 2 条」。
    文件不存在时 chaoset-userdict 按空词典处理（引擎 NewUserDict 同语义）。"""
    path = path or userdict_path()
    tool = find_tool()
    if tool is None:
        return False, "未找到 chaoset-userdict（请重新安装）"
    try:
        r = _run_tool(tool, ["-userdict", path, "stats"])
    except (OSError, subprocess.SubprocessError) as exc:
        return False, "读取学习数据失败：%s" % exc
    if r.returncode != 0:
        return False, (r.stderr or r.stdout or "读取学习数据失败").strip()
    return True, r.stdout.strip()


def export(out_path, phrases_only=False, path=None):
    """导出 TSV 到 out_path（导出只读文件，引擎运行中也可安全执行：
    延迟落盘用 temp+rename 原子替换，读到的总是完整文件）。"""
    path = path or userdict_path()
    tool = find_tool()
    if tool is None:
        return False, "未找到 chaoset-userdict（请重新安装）"
    args = ["-userdict", path, "export"]
    if phrases_only:
        args.append("-phrases")
    args += ["-o", out_path]
    try:
        r = _run_tool(tool, args)
    except (OSError, subprocess.SubprocessError) as exc:
        return False, "导出失败：%s" % exc
    if r.returncode != 0:
        return False, (r.stderr or r.stdout or "导出失败").strip()
    return True, "已导出到 %s" % out_path


def engine_pids():
    """扫描 /proc 找 chaoset-engine 进程（不依赖 pgrep/procps）。"""
    pids = []
    try:
        names = os.listdir("/proc")
    except OSError:
        return pids
    for name in names:
        if not name.isdigit():
            continue
        try:
            with open("/proc/%s/comm" % name, "r", encoding="utf-8",
                      errors="replace") as fh:
                comm = fh.read().strip()
        except OSError:
            continue
        if comm == "chaoset-engine":
            pids.append(int(name))
    return pids


def _wait_engines_gone(timeout=8.0, sleep=time.sleep):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if not engine_pids():
            return True
        sleep(0.1)
    return not engine_pids()


def _terminate_engines(pids, timeout=8.0):
    """SIGTERM（引擎会先同步落盘再退出）→ 等待；超时再 SIGKILL。"""
    for pid in pids:
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except PermissionError:
            return False
    if _wait_engines_gone(timeout):
        return True
    for pid in engine_pids():
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        except PermissionError:
            return False
    return _wait_engines_gone(3.0)


def clear_learning_data(path=None):
    """清空学习数据，返回 (ok, message)。

    安全流程：引擎在运行时，先停 ibus-daemon（`ibus exit`）并 SIGTERM 所有
    chaoset-engine 进程、确认全部退出，再清空文件，最后恢复 ibus（`ibus restart`，
    失败再试 `ibus start`）。只有确认没有引擎存活才清空——否则引擎内存态会在
    下一次延迟落盘时把旧数据写回。
    """
    path = path or userdict_path()
    tool = find_tool()
    if tool is None:
        return False, "未找到 chaoset-userdict（请重新安装）"

    pids = engine_pids()
    ibus = shutil.which("ibus")
    if pids:
        if ibus is None:
            return False, "输入法引擎正在运行，但找不到 ibus 命令，未清空学习数据。"
        # 停 daemon。失败也继续：可能 daemon 本就没在跑，只剩孤儿引擎进程；
        # 下面的 SIGTERM 兜底仍能确保没有引擎存活后才清空。
        try:
            _run([ibus, "exit"], timeout=10)
        except (OSError, subprocess.SubprocessError):
            pass
        if not _terminate_engines(pids):
            return False, "无法停止输入法引擎，未清空学习数据。请注销后重新登录再试。"

    try:
        r = _run_tool(tool, ["-userdict", path, "clear"])
    except (OSError, subprocess.SubprocessError) as exc:
        result = (False, "清空失败：%s" % exc)
    else:
        if r.returncode != 0:
            result = (False, (r.stderr or r.stdout or "清空失败").strip())
        else:
            result = (True, r.stdout.strip() or "已清空学习数据")

    # 恢复 ibus（引擎已全部退出；新引擎会在下次输入时加载清空后的文件）。
    if pids and ibus is not None:
        restored = False
        for sub in ("restart", "start"):
            try:
                r = _run([ibus, sub], timeout=30)
            except (OSError, subprocess.SubprocessError):
                continue
            if r.returncode == 0:
                restored = True
                break
        if not restored and result[0]:
            return False, (result[1] +
                           "\n\n学习数据已清空，但 ibus 未能自动重启；请运行：ibus restart")
    return result


if __name__ == "__main__":
    # 便于命令行自检（设置窗口本身通过 GTK 调用；这里不做参数解析）。
    ok, text = stats()
    print(text)
    raise SystemExit(0 if ok else 1)