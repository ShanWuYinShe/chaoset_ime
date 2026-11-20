#!/usr/bin/env python3
"""
scripts/generate_icons.py: 生成跨平台一致的高清图标资产。
- Windows: shims/windows/resources/chaoset.ico (包含 16, 20, 24, 28, 32, 48, 64, 128, 256 多尺寸)
- Linux: shims/linux/icons/hicolor/ (16, 22, 24, 32, 48, 64, 128, 256) 与 scalable SVG

设计要点（解决任务栏图标偏小与不协调问题）：
1. 满幅实心圆角键帽（Full-bleed Keycap，x=0.5, y=0.5, width=sz-1, height=sz-1，无四周多余留白），
   外框几何轮廓与 UiTray 任务栏中英文指示器 1:1 完全对齐；
2. 移除所有尺寸中的小星标与多余噪点，全分辨率统一采用居中加粗的大写品牌英文字母「C」；
3. 字母「C」字号拉大至约 82% 满格高度（font-size=sz*0.82），笔画粗重（font-weight 900），
   在视觉份量、醒目度和字形高度上与方块汉字「中/英」达到视觉平衡；
4. Windows .ico 文件中小尺寸（≤64px）采用标准未压缩 32bpp DIB BMP 封装，128/256px 采用 PNG 封装，
   杜绝 Windows 原生 GDI / TSF 在高 DPI (125%/150%) 下因 PNG 解码不兼容而回退到 16x16 或粗暴缩放的问题。
"""

import os
import struct
import subprocess
import tempfile
import zlib

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

def gen_svg(sz: int) -> str:
    # 满幅键帽：几何、圆角、高光与 UiTray GDI 原生绘制（cr=3，半径仅 1.5px）1:1 绝对对齐
    rx = 1.5 if sz <= 20 else (2.0 if sz <= 24 else (2.5 if sz <= 28 else (3.0 if sz <= 32 else (4.5 if sz <= 48 else sz * 0.12))))
    fsz = sz * 1.02
    cy = sz * 0.81
    cx = sz * 0.5
    stroke_w = max(0.5, sz * 0.05)
    rim_w = max(0.8, sz * 0.04)

    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{sz}" height="{sz}" viewBox="0 0 {sz} {sz}">
  <defs>
    <linearGradient id="blueGrad" x1="0" y1="0" x2="0" y2="{sz}" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#4DA3FF"/>
      <stop offset="50%" stop-color="#007AFF"/>
      <stop offset="100%" stop-color="#0062CC"/>
    </linearGradient>
  </defs>
  <!-- Full-bleed Keycap: 满幅撑满 0 到 sz，无多余外框缩进 -->
  <rect x="0" y="0" width="{sz}" height="{sz}" rx="{rx:.1f}" ry="{rx:.1f}" fill="url(#blueGrad)"/>
  <!-- Specular Rim Line -->
  <line x1="{rx + 1:.1f}" y1="1" x2="{sz - rx - 1:.1f}" y2="1" stroke="#90C5FF" stroke-width="{rim_w:.1f}" stroke-opacity="0.8" stroke-linecap="round"/>
  <!-- Ultra Bold Centered C: 真正撑满方块、笔画粗重、在视觉重量与面积上与汉字完全等同 -->
  <text x="{cx:.2f}" y="{cy:.2f}" text-anchor="middle" font-family="'Segoe UI', 'Microsoft YaHei UI', 'Liberation Sans', Arial, sans-serif" font-size="{fsz:.2f}" font-weight="900" fill="#FFFFFF" stroke="#FFFFFF" stroke-width="{stroke_w:.2f}" paint-order="stroke fill">C</text>
</svg>"""

def render_png(svg_content: str, out_png: str, sz: int):
    with tempfile.NamedTemporaryFile("w", suffix=".svg", delete=False) as f:
        f.write(svg_content)
        temp_svg = f.name
    try:
        subprocess.run(["rsvg-convert", "-w", str(sz), "-h", str(sz), temp_svg, "-o", out_png], check=True)
    finally:
        os.remove(temp_svg)

def decode_png_rgba(png_bytes: bytes):
    idx = 8
    width, height = 0, 0
    idat = bytearray()
    while idx < len(png_bytes):
        l, t = struct.unpack('>I4s', png_bytes[idx:idx+8])
        idx += 8
        cdata = png_bytes[idx:idx+l]
        idx += l + 4
        if t == b'IHDR': width, height = struct.unpack('>II', cdata[:8])
        elif t == b'IDAT': idat.extend(cdata)
        elif t == b'IEND': break
    raw = zlib.decompress(idat)
    stride = width * 4
    rows = []
    prev = bytearray(stride)
    raw_idx = 0
    for y in range(height):
        ft = raw[raw_idx]
        raw_idx += 1
        line = bytearray(raw[raw_idx:raw_idx+stride])
        raw_idx += stride
        if ft == 1:
            for x in range(4, stride): line[x] = (line[x] + line[x-4]) & 0xFF
        elif ft == 2:
            for x in range(stride): line[x] = (line[x] + prev[x]) & 0xFF
        elif ft == 3:
            for x in range(stride):
                left = line[x-4] if x >= 4 else 0
                line[x] = (line[x] + ((left + prev[x]) >> 1)) & 0xFF
        elif ft == 4:
            for x in range(stride):
                left = line[x-4] if x >= 4 else 0
                up = prev[x]
                ul = prev[x-4] if x >= 4 else 0
                p = left + up - ul
                pa, pb, pc = abs(p - left), abs(p - up), abs(p - ul)
                pr = left if (pa <= pb and pa <= pc) else (up if pb <= pc else ul)
                line[x] = (line[x] + pr) & 0xFF
        prev = line
        row = [(line[x], line[x+1], line[x+2], line[x+3]) for x in range(0, stride, 4)]
        rows.append(row)
    return width, height, rows

def rgba_to_ico_bmp(width: int, height: int, rgba_rows) -> bytes:
    """转换为 Windows ICO 原生支持的标准 32bpp DIB（BITMAPINFOHEADER + BGRA + AND mask）"""
    xor_data = bytearray()
    and_mask = bytearray()
    mask_row_bytes = ((width + 31) // 32) * 4

    for y in reversed(range(height)):
        row = rgba_rows[y]
        mask_row = bytearray(mask_row_bytes)
        for x in range(width):
            r, g, b, a = row[x]
            xor_data.extend(struct.pack('BBBB', b, g, r, a))
            if a < 128:
                mask_row[x // 8] |= (1 << (7 - (x % 8)))
        and_mask.extend(mask_row)

    bih = struct.pack('<IIIHHIIIIII',
        40,                 # biSize
        width,              # biWidth
        height * 2,         # biHeight (doubled for ICO: XOR + AND mask)
        1,                  # biPlanes
        32,                 # biBitCount
        0,                  # biCompression (BI_RGB)
        len(xor_data) + len(and_mask), # biSizeImage
        0, 0, 0, 0
    )
    return bih + xor_data + and_mask

def pack_ico(layers):
    """layers: list of (width, height, raw_bytes)"""
    count = len(layers)
    header = struct.pack("<HHH", 0, 1, count)
    entries = bytearray()
    images = bytearray()
    offset = 6 + count * 16
    for w, h, data in layers:
        bw = 0 if w >= 256 else w
        bh = 0 if h >= 256 else h
        entry = struct.pack("<BBBBHHII", bw, bh, 0, 0, 1, 32, len(data), offset)
        entries.extend(entry)
        images.extend(data)
        offset += len(data)
    return header + entries + images

def main():
    print("==> 生成图标资产...")

    # 1. 生成 Windows chaoset.ico
    # 尺寸覆盖：16(100%), 20(125%), 24(150%), 28(175%), 32(200%), 48, 64, 128, 256
    # ≤64px 使用标准 32bpp DIB BMP 格式，保证 Windows GDI/TSF 绝对兼容；128/256px 使用 PNG 格式
    win_sizes = [16, 20, 24, 28, 32, 48, 64, 128, 256]
    ico_layers = []
    with tempfile.TemporaryDirectory() as tmpdir:
        for sz in win_sizes:
            png_path = os.path.join(tmpdir, f"win_{sz}.png")
            svg = gen_svg(sz)
            render_png(svg, png_path, sz)
            with open(png_path, "rb") as f:
                png_bytes = f.read()

            if sz <= 64:
                w, h, rgba_rows = decode_png_rgba(png_bytes)
                bmp_bytes = rgba_to_ico_bmp(w, h, rgba_rows)
                ico_layers.append((sz, sz, bmp_bytes))
            else:
                ico_layers.append((sz, sz, png_bytes))

        ico_bytes = pack_ico(ico_layers)
        ico_dest = os.path.join(REPO_ROOT, "shims/windows/resources/chaoset.ico")
        os.makedirs(os.path.dirname(ico_dest), exist_ok=True)
        with open(ico_dest, "wb") as f:
            f.write(ico_bytes)
        print(f"✓ 已更新 Windows 图标: {ico_dest} (包含 {len(win_sizes)} 个分辨率层，小尺寸采用原生 DIB 封装)")

    # 2. 生成 Linux hicolor 图标
    linux_sizes = [16, 22, 24, 32, 48, 64, 128, 256]
    for sz in linux_sizes:
        dest_dir = os.path.join(REPO_ROOT, f"shims/linux/icons/hicolor/{sz}x{sz}/apps")
        os.makedirs(dest_dir, exist_ok=True)
        dest_png = os.path.join(dest_dir, "chaoset.png")
        svg = gen_svg(sz)
        render_png(svg, dest_png, sz)
    print(f"✓ 已更新 Linux hicolor 图标 ({len(linux_sizes)} 组分辨率)")

    # 3. 更新 Linux scalable SVG
    scalable_dir = os.path.join(REPO_ROOT, "shims/linux/icons/hicolor/scalable/apps")
    os.makedirs(scalable_dir, exist_ok=True)
    scalable_svg = os.path.join(scalable_dir, "chaoset.svg")
    with open(scalable_svg, "w", encoding="utf-8") as f:
        f.write(gen_svg(256))
    print(f"✓ 已更新 Linux scalable SVG: {scalable_svg}")

if __name__ == "__main__":
    main()
