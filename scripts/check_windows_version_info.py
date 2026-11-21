#!/usr/bin/env python3
"""
scripts/check_windows_version_info.py:
校验 Windows 发行二进制（DLL/EXE/安装器）的版本信息与 app 资源（VS_VERSION_INFO）。
- 确认包含 VS_VERSION_INFO 与 VS_FIXEDFILEINFO（数字版本）
- 确认关键字符串（ProductName, CompanyName, FileVersion, ProductVersion）存在
- 确认中文 UTF-16LE 资源未出现乱码（如「拼音输入法」正确嵌入）
- 确认 Rust 发行 exe（chaosetctl/chaosetd/chaoset-settings）同时带图标
  （RT_GROUP_ICON + RT_ICON）与 VS_VERSION_INFO——资源由各 crate 的 build.rs
  从 shims/windows/resources/chaoset.rc 模板生成，缺任何一个都不允许发布
- 若环境变量 CHAOSET_VERSION_QUAD 存在，断言 Rust 发行 exe 的 FileVersion 字符串
  等于该 4 段数字版本（与 NSIS / PE VS_FIXEDFILEINFO 同源，防止构建时漏注入）
"""

import os
import re
import struct
import sys

# 保证在 Windows 控制台环境下输出 UTF-8 文本不抛 charmap / cp1252 异常
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")

DIST = "dist/chaoset-windows-x64"

# 包内 Rust 发行 exe：build.rs 必须已注入图标 + VS_VERSION_INFO。
RUST_EXES = [
    f"{DIST}/chaosetctl.exe",
    f"{DIST}/chaosetd.exe",
    f"{DIST}/chaoset-settings.exe",
]

# 构建期工具：随包分发供安装器为用户词库预生成 dict.bin。chaoset-engine 没有
# build.rs（dictc 是 crates/chaoset-engine/src/bin 下的工具），因此只校验它是
# 有效 PE，不要求版本/图标资源。
AUX_FILES = [
    f"{DIST}/chaoset-dictc.exe",
]

DEFAULT_FILES = [
    f"{DIST}/ChaosetIME.dll",
    f"{DIST}/ChaosetIME32.dll",
    f"{DIST}/chaoset-ui.exe",
    "dist/ChaosetSetup.exe",
] + RUST_EXES + AUX_FILES

# PE 资源类型 ID（winuser.h / pe 规范）：图标组 + 单图标 + 版本信息。
RT_ICON = 3
RT_GROUP_ICON = 14
RT_VERSION = 16
RESOURCE_DIRECTORY_INDEX = 2


def _pe_layout(data: bytes):
    """返回 (可选头偏移, 节表偏移, 节数量)；无效 PE 返回 None。"""
    if len(data) < 0x40 or data[:2] != b"MZ":
        return None
    try:
        pe_off = struct.unpack_from("<I", data, 0x3C)[0]
        if data[pe_off:pe_off + 4] != b"PE\0\0":
            return None
        coff = pe_off + 4
        num_sections = struct.unpack_from("<H", data, coff + 2)[0]
        opt_size = struct.unpack_from("<H", data, coff + 16)[0]
        opt_off = coff + 20
        magic = struct.unpack_from("<H", data, opt_off)[0]
        if magic == 0x10B:
            dd_off = opt_off + 96
        elif magic == 0x20B:
            dd_off = opt_off + 112
        else:
            return None
        return dd_off, opt_off + opt_size, num_sections
    except struct.error:
        return None


def resource_type_ids(data: bytes):
    """解析 PE 资源目录，返回顶层资源类型 ID 集合（含 RT_ICON / RT_GROUP_ICON / RT_VERSION）。"""
    layout = _pe_layout(data)
    if layout is None:
        return None
    dd_off, section_off, num_sections = layout
    try:
        res_rva, _res_size = struct.unpack_from("<II", data, dd_off + RESOURCE_DIRECTORY_INDEX * 8)
    except struct.error:
        return None
    if res_rva == 0:
        return set()
    for i in range(num_sections):
        sec = section_off + i * 40
        vsize, vaddr, rawsize, rawptr = struct.unpack_from("<IIII", data, sec + 8)
        if vaddr <= res_rva < vaddr + max(vsize, rawsize):
            base = rawptr + (res_rva - vaddr)
            break
    else:
        return None
    try:
        n_named, n_id = struct.unpack_from("<HH", data, base + 12)
        entries = base + 16
        ids = set()
        for i in range(n_named + n_id):
            name_or_id, _offset = struct.unpack_from("<II", data, entries + i * 8)
            # 顶层资源类型用整数 ID（命名类型很少见，这里只需判断标准 ID）。
            if i >= n_named:
                ids.add(name_or_id & 0xFFFF)
        return ids
    except struct.error:
        return None


def version_file_version(data: bytes):
    """从 VS_VERSION_INFO 的 StringFileInfo 里取出 FileVersion 字符串。"""
    marker = "FileVersion".encode("utf-16-le")
    pos = data.find(marker)
    if pos < 0:
        return None
    chunk = data[pos + len(marker): pos + len(marker) + 512]
    text = chunk.decode("utf-16-le", "replace")
    match = re.search(r"\d+\.\d+\.\d+\.\d+", text)
    return match.group(0) if match else None


def _expected_quad() -> str:
    return os.environ.get("CHAOSET_VERSION_QUAD", "").strip()


def check_file(path: str, need_version: bool = True) -> bool:
    if not os.path.isfile(path):
        print(f"错误: 文件不存在: {path}")
        return False

    with open(path, "rb") as f:
        d = f.read()

    if _pe_layout(d) is None:
        print(f"错误: {path}: 不是有效的 PE 文件")
        return False

    name = os.path.basename(path)
    if not need_version:
        print(f"✓ {path}: PE 文件有效（构建期工具，不要求版本/图标资源）")
        return True

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

    # Rust 发行 exe：图标（RT_GROUP_ICON + RT_ICON）与版本资源缺一不可，
    # 否则语言栏/资源管理器没有图标、属性页看不到版本（build.rs 回归）。
    if name in {os.path.basename(p) for p in RUST_EXES}:
        types = resource_type_ids(d)
        if types is None:
            print(f"错误: {path}: 无法解析 PE 资源目录（图标/版本资源不可查）")
            return False
        for type_id, type_name in ((RT_GROUP_ICON, "RT_GROUP_ICON"), (RT_ICON, "RT_ICON"), (RT_VERSION, "RT_VERSION")):
            if type_id not in types:
                print(f"错误: {path}: 缺少 {type_name} 资源（图标/版本信息未嵌入）")
                return False
        expected = _expected_quad()
        if expected:
            got = version_file_version(d)
            if got != expected:
                print(f"错误: {path}: FileVersion = {got!r}，期望 CHAOSET_VERSION_QUAD = {expected!r}")
                return False
            print(f"  {path}: 图标（RT_GROUP_ICON/RT_ICON）与 VS_VERSION_INFO 均存在，FileVersion = {got}")

    print(f"✓ {path}: 版本信息与中文资源校验通过")
    return True


def main():
    target_files = sys.argv[1:] if len(sys.argv) > 1 else DEFAULT_FILES
    print("==> 开始校验 Windows PE 二进制版本信息与 App 资源...")
    all_ok = True
    aux = {os.path.basename(p) for p in AUX_FILES}
    for p in target_files:
        if not check_file(p, need_version=os.path.basename(p) not in aux):
            all_ok = False

    if not all_ok:
        sys.exit(1)
    print("==> 全部 Windows 二进制版本信息校验通过！")


if __name__ == "__main__":
    main()
