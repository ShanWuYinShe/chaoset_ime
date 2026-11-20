#!/usr/bin/env python3
"""
scripts/check_windows_version_info.py:
校验 Windows PE 二进制文件（DLL/EXE/安装器）的版本信息与 app 资源（VS_VERSION_INFO）。
- 确认包含 VS_VERSION_INFO 与 VS_FIXEDFILEINFO（数字版本）
- 确认关键字符串（ProductName, CompanyName, FileVersion, ProductVersion）存在
- 确认中文 UTF-16LE 资源未出现乱码（如「拼音输入法」正确嵌入）
"""

import os
import struct
import sys

# 保证在 Windows 控制台环境下输出 UTF-8 文本不抛 charmap / cp1252 异常
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")

DEFAULT_FILES = [
    "dist/chaoset-windows-x64/ChaosetIME.dll",
    "dist/chaoset-windows-x64/ChaosetIME32.dll",
    "dist/chaoset-windows-x64/chaoset-ui.exe",
    "dist/ChaosetSetup.exe",
]

def check_file(path: str) -> bool:
    if not os.path.isfile(path):
        print(f"错误: 文件不存在: {path}")
        return False

    with open(path, "rb") as f:
        d = f.read()

    i = d.find("VS_VERSION_INFO".encode("utf-16-le"))
    if i < 0:
        print(f"错误: {path}: 缺少 VS_VERSION_INFO 版本资源")
        return False

    if d.find(struct.pack("<I", 0xFEEF04BD)) < 0:
        print(f"错误: {path}: 缺少 VS_FIXEDFILEINFO（数字版本）")
        return False

    for k in ("ProductName", "CompanyName", "FileVersion", "ProductVersion"):
        if k.encode("utf-16-le") not in d:
            print(f"错误: {path}: 版本资源缺少 {k}")
            return False

    if "拼音输入法".encode("utf-16-le") not in d:
        print(f"错误: {path}: 版本资源中文乱码（未正确转为 UTF-16LE）")
        return False

    print(f"✓ {path}: 版本信息与中文资源校验通过")
    return True

def main():
    target_files = sys.argv[1:] if len(sys.argv) > 1 else DEFAULT_FILES
    print("==> 开始校验 Windows PE 二进制版本信息与 App 资源...")
    all_ok = True
    for p in target_files:
        if not check_file(p):
            all_ok = False

    if not all_ok:
        sys.exit(1)
    print("==> 全部 Windows 二进制版本信息校验通过！")

if __name__ == "__main__":
    main()
