package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// BinaryDictMagic 是 Chaoset 二进制词库的文件头魔数（Chaoset Dict Binary v1）。
const BinaryDictMagic = "CDB1"

// BinaryDictVersion 是当前二进制词库格式版本。
const BinaryDictVersion uint16 = 1

// IsBinaryDict 检查字节切片头部是否为 CDB 二进制词库魔数。
func IsBinaryDict(header []byte) bool {
	return len(header) >= 4 && string(header[:4]) == BinaryDictMagic
}

// WriteBinaryDict 将词条列表导出为紧凑二进制格式（CDB1）。
// 格式规范：
// 1. Header (16 字节):
//   - Magic: [4]byte "CDB1"
//   - Version: uint16 (1)
//   - Flags: uint16 (0, 保留)
//   - EntryCount: uint32 (词条数量)
//   - SyllableCount: uint16 (独立音节表大小)
//   - Reserved: uint16 (0)
//
// 2. Syllable Table:
//   - 连续存放每一个音节: Len (uint8) + Bytes ([Len]byte, ASCII)
//
// 3. Entries Data:
//   - 连续存放每一个词条:
//   - WordLen: uint8 (UTF-8 字节长度)
//   - WordBytes: [WordLen]byte
//   - SyllableCount: uint8
//   - SyllableIDs: [SyllableCount]uint16 (BigEndian)
//   - Freq: uint8 (1..99)
func WriteBinaryDict(w io.Writer, entries []*Entry) error {
	// 收集并排序所有唯一音节，生成音节表
	syllMap := make(map[string]uint16)
	var syllList []string
	for _, e := range entries {
		for _, s := range e.Syllables {
			if _, ok := syllMap[s]; !ok {
				syllMap[s] = 0
				syllList = append(syllList, s)
			}
		}
	}
	sort.Strings(syllList)
	if len(syllList) > 65535 {
		return fmt.Errorf("唯一音节数量超标 (%d > 65535)", len(syllList))
	}
	for i, s := range syllList {
		syllMap[s] = uint16(i)
	}

	// 1. 写 Header
	var hdr [16]byte
	copy(hdr[0:4], BinaryDictMagic)
	binary.BigEndian.PutUint16(hdr[4:6], BinaryDictVersion)
	binary.BigEndian.PutUint16(hdr[6:8], 0) // Flags
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(entries)))
	binary.BigEndian.PutUint16(hdr[12:14], uint16(len(syllList)))
	binary.BigEndian.PutUint16(hdr[14:16], 0) // Reserved
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("写入二进制词库头部失败: %w", err)
	}

	// 2. 写音节表
	for _, s := range syllList {
		if len(s) > 255 {
			return fmt.Errorf("音节长度超过 255: %s", s)
		}
		if _, err := w.Write([]byte{uint8(len(s))}); err != nil {
			return err
		}
		if _, err := w.Write([]byte(s)); err != nil {
			return err
		}
	}

	// 3. 写词条数据
	idBuf := make([]byte, 2)
	for _, e := range entries {
		wordBytes := []byte(e.Word)
		if len(wordBytes) > 255 {
			return fmt.Errorf("词条 %q 长度超过 255 字节", e.Word)
		}
		if len(e.Syllables) > 255 {
			return fmt.Errorf("词条 %q 音节数超过 255", e.Word)
		}
		// WordLen + WordBytes
		if _, err := w.Write([]byte{uint8(len(wordBytes))}); err != nil {
			return err
		}
		if _, err := w.Write(wordBytes); err != nil {
			return err
		}
		// SyllableCount + SyllableIDs
		if _, err := w.Write([]byte{uint8(len(e.Syllables))}); err != nil {
			return err
		}
		for _, s := range e.Syllables {
			id, ok := syllMap[s]
			if !ok {
				return fmt.Errorf("词条 %q 未知音节: %s", e.Word, s)
			}
			binary.BigEndian.PutUint16(idBuf, id)
			if _, err := w.Write(idBuf); err != nil {
				return err
			}
		}
		// Freq
		freq := e.Freq
		if freq < 1 {
			freq = 1
		} else if freq > 99 {
			freq = 99
		}
		if _, err := w.Write([]byte{uint8(freq)}); err != nil {
			return err
		}
	}
	return nil
}

// ReadBinaryDict 从二进制格式（CDB1）中解析词条并加入目标 Dictionary 中。
func ReadBinaryDict(r io.Reader, d *Dictionary) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("读取二进制词库失败: %w", err)
	}
	if len(data) < 16 {
		return errors.New("二进制词库文件过小（不足 16 字节）")
	}
	if string(data[:4]) != BinaryDictMagic {
		return fmt.Errorf("无效的二进制词库 Magic: %q（应为 %q）", string(data[:4]), BinaryDictMagic)
	}
	version := binary.BigEndian.Uint16(data[4:6])
	if version != BinaryDictVersion {
		return fmt.Errorf("不支持的二进制词库版本 %d（当前仅支持 %d）", version, BinaryDictVersion)
	}
	entryCount := int(binary.BigEndian.Uint32(data[8:12]))
	syllCount := int(binary.BigEndian.Uint16(data[12:14]))

	offset := 16
	// 读取音节表
	syllList := make([]string, syllCount)
	for i := 0; i < syllCount; i++ {
		if offset >= len(data) {
			return errors.New("二进制词库音节表数据截断")
		}
		sLen := int(data[offset])
		offset++
		if offset+sLen > len(data) {
			return errors.New("二进制词库音节数据截断")
		}
		syllList[i] = string(data[offset : offset+sLen])
		offset += sLen
	}

	// 预分配 entries 空间
	if d.entries == nil {
		d.entries = make([]*Entry, 0, entryCount)
	}

	// 读取词条列表
	for i := 0; i < entryCount; i++ {
		if offset >= len(data) {
			return fmt.Errorf("二进制词库在解析第 %d 条词时数据截断", i)
		}
		wLen := int(data[offset])
		offset++
		if offset+wLen > len(data) {
			return errors.New("二进制词条文本数据截断")
		}
		word := string(data[offset : offset+wLen])
		offset += wLen

		if offset >= len(data) {
			return errors.New("二进制词条音节数数据截断")
		}
		numSylls := int(data[offset])
		offset++
		if offset+numSylls*2 > len(data) {
			return errors.New("二进制词条音节序列数据截断")
		}

		sylls := make([]string, numSylls)
		for j := 0; j < numSylls; j++ {
			id := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2
			if id >= len(syllList) {
				return fmt.Errorf("二进制词条音节 ID 越界 (%d >= %d)", id, len(syllList))
			}
			sylls[j] = syllList[id]
		}

		if offset >= len(data) {
			return errors.New("二进制词条词频数据截断")
		}
		freq := int(data[offset])
		offset++

		// 加入字典
		key := word + "\x00" + strings.Join(sylls, " ")
		if existingIdx, exists := d.seen[key]; exists {
			if freq > d.entries[existingIdx].Freq {
				d.entries[existingIdx].Freq = freq
			}
		} else {
			d.seen[key] = len(d.entries)
			d.add(word, sylls, freq)
		}
	}
	return nil
}
