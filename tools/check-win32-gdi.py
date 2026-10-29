#!/usr/bin/env python3
"""校验 Windows 壳的 Win32 GDI 调用契约（2026-10-29 崩溃事故）。

背景：v0.2.14 候选窗齿轮为了圆头线帽调用
    ExtCreatePen(PS_GEOMETRIC | PS_ENDCAP_ROUND, width, nullptr, 0, nullptr)
ExtCreatePen 的第三个参数 plbrush 是必填的 LOGBRUSH 指针，gdi32 在用户态
直接读 lbStyle/lbColor（ReactOS win32ss/gdi/gdi32/objects/brush.c 同构），
传 nullptr 就是空指针解引用。TSF DLL 跑在宿主进程里，用户每敲一个字母，
候选窗首帧绘制就会把宿主程序带走（现象：只出现一个空候选框，随后进程退出）。

本脚本检查 shims/windows/src 下所有 ExtCreatePen 调用：
  1. 必须正好 5 个参数；
  2. 第三个参数（plbrush）不得是 nullptr / NULL / 0。

用法：python3 tools/check-win32-gdi.py shims/windows/src
退出码：0 = 通过，1 = 发现违规。
"""
import pathlib
import re
import sys

CALL_RE = re.compile(r"\bExtCreatePen\s*\(")
NULL_ARGS = {"nullptr", "NULL", "0"}


def extract_args(text, open_paren):
    """从 open_paren 指向的 '(' 开始，返回顶层逗号分隔的参数列表。"""
    args = []
    depth = 0
    start = open_paren + 1
    i = open_paren
    in_str = None
    while i < len(text):
        ch = text[i]
        if in_str is not None:
            if ch == "\\":
                i += 2
                continue
            if ch == in_str:
                in_str = None
        elif ch in "\"'":
            in_str = ch
        elif ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
            if depth == 0:
                args.append(text[start:i])
                return args
        elif ch == "," and depth == 1:
            args.append(text[start:i])
            start = i + 1
        i += 1
    return None


def main():
    if len(sys.argv) != 2:
        print("用法: check-win32-gdi.py <源码目录>", file=sys.stderr)
        return 2

    src_dir = pathlib.Path(sys.argv[1])
    files = sorted(list(src_dir.glob("*.cpp")) + list(src_dir.glob("*.hpp")))
    if not files:
        print(f"未找到任何 C++ 源文件: {src_dir}", file=sys.stderr)
        return 2

    problems = []
    checked = 0
    for path in files:
        text = path.read_text(encoding="utf-8")
        for match in CALL_RE.finditer(text):
            open_paren = text.index("(", match.start())
            args = extract_args(text, open_paren)
            line = text.count("\n", 0, match.start()) + 1
            where = f"{path}:{line}"
            checked += 1
            if args is None:
                problems.append(f"{where}: ExtCreatePen 参数括号不闭合，无法校验")
                continue
            if len(args) != 5:
                problems.append(
                    f"{where}: ExtCreatePen 需要 5 个参数，实际 {len(args)} 个")
                continue
            plbrush = args[2].strip()
            if plbrush in NULL_ARGS:
                problems.append(
                    f"{where}: ExtCreatePen 的 plbrush 传了 {plbrush}；"
                    "必须指向有效 LOGBRUSH（gdi32 会直接解引用）")

    if problems:
        print("Windows GDI 契约检查失败：", file=sys.stderr)
        for p in problems:
            print("  " + p, file=sys.stderr)
        return 1

    print(f"Windows GDI 契约检查通过（{checked} 处 ExtCreatePen 调用）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
