#!/usr/bin/env python3
"""
scripts/generate_icons.py: 生成跨平台一致的高清图标资产。
- Windows: shims/windows/resources/chaoset.ico (包含 16, 20, 24, 28, 32, 48, 64, 128, 256 多尺寸)
- Linux: shims/linux/icons/hicolor/ (16, 22, 24, 32, 48, 64, 128, 256) 与 scalable SVG

设计规范（彻底占满任务栏，消除周围留白）：
1. 100% 满幅撑满（0 pad 留白）：
   所有尺寸（16 到 512）的按键底板均从 (0, 0) 撑满至 (sz, sz)，彻底消除任何外边距与透明留白；
   圆角紧凑微敛（rx=1.5~2.5px，大尺寸 sz*0.10），避免大圆角削切造成视觉收缩；
2. 品牌核心元素黄金平衡：
   - 品牌大写字母「C」放大至约 78%~82% 高度，加粗字重，挺拔醒目，视觉重量与汉字方块字完全等同；
   - 右上角璀璨四芒星标「✦」采用精确内凹贝塞尔矢量路径，与字母「C」自然咬合，低分辨率下锐利纯净、绝不糊化；
3. Windows .ico 规范打包：
   - ≤64px 采用标准未压缩 32bpp DIB BMP 封装；
   - 128/256px 采用 PNG 封装，完美适配 Windows TSF、任务栏托盘与高 DPI 缩放。
"""

import os
import struct
import subprocess
import tempfile
import zlib

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

def gen_star_path(cx: float, cy: float, r: float) -> str:
    """生成优雅锐利的四芒星（✦）贝塞尔内弧矢量路径"""
    return (f"M {cx:.2f} {cy - r:.2f} "
            f"Q {cx:.2f} {cy:.2f} {cx + r:.2f} {cy:.2f} "
            f"Q {cx:.2f} {cy:.2f} {cx:.2f} {cy + r:.2f} "
            f"Q {cx:.2f} {cy:.2f} {cx - r:.2f} {cy:.2f} "
            f"Q {cx:.2f} {cy:.2f} {cx:.2f} {cy - r:.2f} Z")

def gen_svg(sz: int) -> str:
    if sz <= 48:
        # 小尺寸：0 留白满幅键帽 + 大字号加粗 C + 高锐度星标 ✦
        rx = 1.5 if sz <= 20 else (2.0 if sz <= 24 else (2.5 if sz <= 28 else (3.0 if sz <= 32 else 4.0)))
        rim_w = max(0.6, sz * 0.04)
        
        # 字母 C 定位与大小（撑满视觉主体）
        fsz = sz * 0.78
        cx = sz * 0.40
        cy = sz * 0.78
        c_stroke_w = max(0.4, sz * 0.04)

        # 星标 ✦ 定位与半径（右上角璀璨点缀）
        star_cx = sz * 0.77
        star_cy = sz * 0.26
        star_r = sz * 0.22
        star_p = gen_star_path(star_cx, star_cy, star_r)

        return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{sz}" height="{sz}" viewBox="0 0 {sz} {sz}">
  <defs>
    <linearGradient id="blueGrad" x1="0" y1="0" x2="0" y2="{sz}" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#4DA3FF"/>
      <stop offset="50%" stop-color="#007AFF"/>
      <stop offset="100%" stop-color="#0062CC"/>
    </linearGradient>
  </defs>
  <!-- Full-bleed Keycap: 0 到 sz 完全占满，无外边距 -->
  <rect x="0" y="0" width="{sz}" height="{sz}" rx="{rx:.1f}" ry="{rx:.1f}" fill="url(#blueGrad)"/>
  <!-- Specular Rim Line -->
  <line x1="{rx + 0.5:.1f}" y1="1" x2="{sz - rx - 0.5:.1f}" y2="1" stroke="#90C5FF" stroke-width="{rim_w:.1f}" stroke-opacity="0.85" stroke-linecap="round"/>
  <!-- Bold Centered-Left C -->
  <text x="{cx:.2f}" y="{cy:.2f}" text-anchor="middle" font-family="'Segoe UI', 'Microsoft YaHei UI', 'Liberation Sans', Arial, sans-serif" font-size="{fsz:.2f}" font-weight="900" fill="#FFFFFF" stroke="#FFFFFF" stroke-width="{c_stroke_w:.2f}" paint-order="stroke fill">C</text>
  <!-- Sharp Sparkle Star (✦) -->
  <path d="{star_p}" fill="#FFFFFF"/>
</svg>"""
    else:
        # 大尺寸：0 留白满幅微实体（squircle + 同心微纹 + 顶边高光 + C + ✦ + CHAOS）
        rx = sz * 0.10  # 紧凑圆角，避免削角留白
        fsz = sz * 0.68
        cx = sz * 0.42
        cy = sz * 0.74
        
        star_cx = sz * 0.76
        star_cy = sz * 0.28
        star_r = sz * 0.20
        star_p = gen_star_path(star_cx, star_cy, star_r)

        return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{sz}" height="{sz}" viewBox="0 0 {sz} {sz}">
  <defs>
    <linearGradient id="azureGrad" x1="0" y1="0" x2="{sz}" y2="{sz}" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#4DA3FF"/>
      <stop offset="48%" stop-color="#007AFF"/>
      <stop offset="100%" stop-color="#0062CC"/>
    </linearGradient>
    <clipPath id="squircleClip">
      <rect x="0" y="0" width="{sz}" height="{sz}" rx="{rx:.1f}" ry="{rx:.1f}"/>
    </clipPath>
  </defs>

  <!-- 1. Full-bleed Squircle Base: 0 pad 占满 -->
  <rect x="0" y="0" width="{sz}" height="{sz}" rx="{rx:.1f}" ry="{rx:.1f}" fill="url(#azureGrad)"/>

  <!-- 2. Clipped Texture & Highlights -->
  <g clip-path="url(#squircleClip)">
    <circle cx="{sz*0.5:.1f}" cy="{sz*0.5:.1f}" r="{sz*0.30:.1f}" stroke="#EAF3FF" stroke-opacity="0.15" stroke-width="{sz*0.005:.1f}" fill="none"/>
    <circle cx="{sz*0.5:.1f}" cy="{sz*0.5:.1f}" r="{sz*0.46:.1f}" stroke="#EAF3FF" stroke-opacity="0.15" stroke-width="{sz*0.005:.1f}" fill="none"/>
    <circle cx="{sz*0.5:.1f}" cy="{sz*0.5:.1f}" r="{sz*0.62:.1f}" stroke="#EAF3FF" stroke-opacity="0.15" stroke-width="{sz*0.005:.1f}" fill="none"/>
    <!-- Top-edge Specular Rim Line -->
    <rect x="1" y="1" width="{sz-2:.1f}" height="{sz-2:.1f}" rx="{rx-1:.1f}" ry="{rx-1:.1f}" stroke="#FFFFFF" stroke-opacity="0.55" stroke-width="{max(1.0, sz*0.004):.1f}" fill="none"/>
  </g>

  <!-- 3. Monogram C -->
  <text x="{cx:.1f}" y="{cy:.1f}" text-anchor="middle" font-family="'Segoe UI', 'Microsoft YaHei UI', 'Liberation Sans', Arial, sans-serif" font-size="{fsz:.1f}" font-weight="900" fill="#FFFFFF">C</text>

  <!-- 4. Bright Star (✦) -->
  <path d="{star_p}" fill="#FFFFFF"/>

  <!-- 5. Bottom Brand Subtext -->
  <text x="{sz*0.5:.1f}" y="{sz*0.93:.1f}" text-anchor="middle" font-family="'Segoe UI', 'Liberation Sans', Arial, sans-serif" font-size="{sz*0.045:.1f}" font-weight="bold" letter-spacing="{sz*0.01:.1f}" fill="#EAF3FF" fill-opacity="0.85">CHAOS</text>
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
    print("==> 生成跨平台 100% 满幅撑满高清图标资产（0 留白，完整保留星标✦与字母C）...")
    
    # 1. 生成 Windows chaoset.ico
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
                w, h, rows = decode_png_rgba(png_bytes)
                bmp_bytes = rgba_to_ico_bmp(w, h, rows)
                ico_layers.append((sz, sz, bmp_bytes))
            else:
                ico_layers.append((sz, sz, png_bytes))
        
        ico_bytes = pack_ico(ico_layers)
        ico_dest = os.path.join(REPO_ROOT, "shims/windows/resources/chaoset.ico")
        os.makedirs(os.path.dirname(ico_dest), exist_ok=True)
        with open(ico_dest, "wb") as f:
            f.write(ico_bytes)
        print(f"✓ 已更新 Windows 图标: {ico_dest} (包含 {len(win_sizes)} 个多分辨率层)")

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
        f.write(gen_svg(512))
    print(f"✓ 已更新 Linux scalable SVG: {scalable_svg}")

if __name__ == "__main__":
    main()
