// pngfast.go：固定 None 滤波的快速 PNG 编码器（候选窗每键一帧的性能路径）。
//
// image/png 的 Encoder 对每行做自适应滤波（逐一计算 None/Sub/Up/Average/Paeth
// 五种再择优）。pprof 实测（2026-10-25，scale=2 候选栏）滤波合计占单帧编码
// 耗时 ~57%（filter+paeth+abs8），而本项目压缩级别是 BestSpeed——快速压缩器
// 下自适应滤波几乎不提升压缩率。改为固定 None 滤波 + zlib(BestSpeed)：
//
//	1374×98 @2x：stdlib 6.35ms → 2.36ms（-63%），产物 12.3KB → 11.1KB
//	687×50 @1x ：stdlib 2.31ms → 0.82ms（-65%），产物 12.3KB → 11.1KB
//
// 产物反而更小：BestSpeed 对未滤波数据的压缩差异可忽略，而滤波选择本身
// 的字节试探开销消失。产物是完全合法的 PNG（8bit RGBA，IHDR/IDAT/IEND），
// 解码端（平台壳）无感知。
//
// 历史取舍（保留）：NoCompression 也试过，编码确实再快 ~100µs，但单帧
// 从 ~7KB 涨到 ~55KB，经 base64+JSON 走 socket 后总耗时持平甚至更差
// （2026-10-25 真机 daemon 对照：无压缩 1.6-4.1ms vs BestSpeed 1.6-2.9ms）。
// 本编码器保持 BestSpeed 压缩级别，只省滤波。
package renderer

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
)

var pngCRC = crc32.MakeTable(crc32.IEEE)

// encodeFastPNG 把 NRGBA 图像编码为 PNG 字节流。
// 依赖 image.NewNRGBA 的保证（Pix 紧凑、Stride == 4*宽）；不满足时逐像素回退。
func encodeFastPNG(img *image.NRGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	// 未滤波扫描线：每行 1 字节 filter type（0 = None）+ RGBA 像素。
	raw := make([]byte, h*(1+w*4))
	if img.Stride == 4*w && img.Rect.Min == b.Min {
		rowLen := 4 * w
		for y := 0; y < h; y++ {
			off := y * (1 + rowLen)
			raw[off] = 0 // None
			copy(raw[off+1:off+1+rowLen], img.Pix[y*img.Stride:(y+1)*img.Stride])
		}
	} else {
		for y := 0; y < h; y++ {
			off := y * (1 + 4*w)
			raw[off] = 0
			for x := 0; x < w; x++ {
				c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y)
				o := off + 1 + x*4
				raw[o], raw[o+1], raw[o+2], raw[o+3] = c.R, c.G, c.B, c.A
			}
		}
	}

	var comp bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&comp, zlib.BestSpeed)
	zw.Write(raw)
	zw.Close()

	out := make([]byte, 0, comp.Len()+64)
	out = append(out, 0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n') // PNG 签名
	// IHDR：宽、高、位深 8、颜色类型 6（RGBA）、压缩 0、滤波 0、隔行 0。
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9], ihdr[10], ihdr[11], ihdr[12] = 8, 6, 0, 0, 0
	out = appendPNGChunk(out, "IHDR", ihdr)
	out = appendPNGChunk(out, "IDAT", comp.Bytes())
	out = appendPNGChunk(out, "IEND", nil)
	return out
}

// appendPNGChunk 追加一个 PNG 数据块：长度 + 类型 + 数据 + CRC32。
func appendPNGChunk(out []byte, typ string, data []byte) []byte {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	out = append(out, hdr[:]...)
	out = append(out, typ...)
	out = append(out, data...)
	crc := crc32.Checksum([]byte(typ), pngCRC)
	crc = crc32.Update(crc, pngCRC, data)
	var tail [4]byte
	binary.BigEndian.PutUint32(tail[:], crc)
	return append(out, tail[:]...)
}
