#!/usr/bin/env python3
"""
scripts/generate_icons.py: 生成跨平台一致的高清图标资产。
- Windows: shims/windows/resources/chaoset.ico (包含 16, 20, 24, 28, 32, 48, 64, 128, 256 多尺寸)
- Linux: shims/linux/icons/hicolor/ (16, 22, 24, 32, 48, 64, 128, 256) 与 scalable SVG

设计要点（解决任务栏图标偏小问题）：
对于 ≤48 像素的任务栏/指示器小尺寸：
- 采用满幅实心圆角键帽（Full-bleed Keycap，inset=0），几何外形与 UiTray 中英文指示器完全对齐；
- 移除多余的微星与留白边缘，将品牌白色字母「C」居中放大至约 75% 满格高度，与中英文指示器的「中/英」在视觉份量、
  字体高度和按键轮廓上 1:1 协调一致；
- 针对 Windows 高 DPI 场景，补齐 20x20 (125%) 与 28x28 (175%) 分辨率层，避免系统因缺失中间尺寸而降级回退到 16x16。
"""

import os
import struct
import subprocess
import tempfile

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

def gen_svg(sz: int) -> str:
    if sz <= 48:
        # 小尺寸：满幅键帽 + 大字号居中「C」，对齐 UiTray
        rx = 3.0 if sz <= 16 else (3.5 if sz <= 20 else (4.0 if sz <= 22 else (4.5 if sz <= 24 else (5.0 if sz <= 28 else (5.5 if sz <= 32 else 8.0)))))
        fsz = sz * 0.75
        cy = sz * 0.765
        cx = sz * 0.5
        stroke_w = 0.6 if sz <= 20 else 0.8
        rim_w = 0.8 if sz <= 20 else 1.0
        return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{sz}" height="{sz}" viewBox="0 0 {sz} {sz}">
  <defs>
    <linearGradient id="blueGrad" x1="0" y1="0" x2="0" y2="{sz}" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#4DA3FF"/>
      <stop offset="50%" stop-color="#007AFF"/>
      <stop offset="100%" stop-color="#0062CC"/>
    </linearGradient>
  </defs>
  <!-- Full-bleed Keycap -->
  <rect x="0.5" y="0.5" width="{sz - 1}" height="{sz - 1}" rx="{rx}" ry="{rx}" fill="url(#blueGrad)" stroke="#0058B8" stroke-width="{stroke_w}"/>
  <!-- Specular Rim Line -->
  <line x1="{rx}" y1="1.5" x2="{sz - rx}" y2="1.5" stroke="#90C5FF" stroke-width="{rim_w}" stroke-linecap="round"/>
  <!-- Bold C -->
  <text x="{cx:.2f}" y="{cy:.2f}" text-anchor="middle" font-family="Liberation Sans, Arial, Helvetica, sans-serif" font-size="{fsz:.2f}" font-weight="900" fill="#FFFFFF">C</text>
</svg>"""
    else:
        # 大尺寸：带品牌星标与质感高光
        pad = sz * 0.03
        w = sz - pad * 2
        rx = w * 0.20
        fsz = w * 0.65
        cx = sz * 0.44
        cy = sz * 0.71
        star_x = cx + fsz * 0.40
        star_y = cy - fsz * 0.46
        star_fsz = fsz * 0.35
        return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{sz}" height="{sz}" viewBox="0 0 {sz} {sz}">
  <defs>
    <linearGradient id="blueGrad" x1="0" y1="0" x2="0" y2="{sz}" gradientUnits="userSpaceOnUse">
      <stop offset="0%" stop-color="#4DA3FF"/>
      <stop offset="48%" stop-color="#007AFF"/>
      <stop offset="100%" stop-color="#0062CC"/>
    </linearGradient>
  </defs>
  <rect x="{pad:.1f}" y="{pad:.1f}" width="{w:.1f}" height="{w:.1f}" rx="{rx:.1f}" ry="{rx:.1f}" fill="url(#blueGrad)" stroke="#0058B8" stroke-width="1.2"/>
  <line x1="{pad+rx:.1f}" y1="{pad+1.5:.1f}" x2="{pad+w-rx:.1f}" y2="{pad+1.5:.1f}" stroke="#FFFFFF" stroke-opacity="0.55" stroke-width="1.5" stroke-linecap="round"/>
  <text x="{star_x:.1f}" y="{star_y:.1f}" text-anchor="middle" font-family="Liberation Sans, Arial, system-ui, sans-serif" font-size="{star_fsz:.1f}" font-weight="bold" fill="#FFFFFF">✦</text>
  <text x="{cx:.1f}" y="{cy:.1f}" text-anchor="middle" font-family="Liberation Sans, Arial, Helvetica, sans-serif" font-size="{fsz:.1f}" font-weight="900" fill="#FFFFFF">C</text>
</svg>"""

def render_png(svg_content: str, out_png: str, sz: int):
    with tempfile.NamedTemporaryFile("w", suffix=".svg", delete=False) as f:
        f.write(svg_content)
        temp_svg = f.name
    try:
        subprocess.run(["rsvg-convert", "-w", str(sz), "-h", str(sz), temp_svg, "-o", out_png], check=True)
    finally:
        os.remove(temp_svg)

def pack_ico(png_layers):
    """png_layers: list of (width, height, bytes)"""
    count = len(png_layers)
    header = struct.pack("<HHH", 0, 1, count)
    entries = bytearray()
    images = bytearray()
    offset = 6 + count * 16
    for w, h, data in png_layers:
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
    win_sizes = [16, 20, 24, 28, 32, 48, 64, 128, 256]
    ico_layers = []
    with tempfile.TemporaryDirectory() as tmpdir:
        for sz in win_sizes:
            png_path = os.path.join(tmpdir, f"win_{sz}.png")
            svg = gen_svg(sz)
            render_png(svg, png_path, sz)
            with open(png_path, "rb") as f:
                ico_layers.append((sz, sz, f.read()))
        
        ico_bytes = pack_ico(ico_layers)
        ico_dest = os.path.join(REPO_ROOT, "shims/windows/resources/chaoset.ico")
        os.makedirs(os.path.dirname(ico_dest), exist_ok=True)
        with open(ico_dest, "wb") as f:
            f.write(ico_bytes)
        print(f"✓ 已更新 Windows 图标: {ico_dest} (包含 {len(win_sizes)} 个分辨率层)")

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
