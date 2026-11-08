package ipc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// 帧格式：4 字节大端长度 + JSON 载荷。请求与响应通过 ID 关联。
const maxMessageSize = 1 << 20

type Request struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func writeMessage(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("序列化消息失败: %w", err)
	}
	// 写侧也校验帧上限（与读侧 readMessage 同口径）：超限响应若只写出去一半，
	// 对端读帧失败后流里会残留 body，连接失步。正常响应走分页，不会触发。
	if len(data) > maxMessageSize {
		return fmt.Errorf("消息长度 %d 超过上限 %d", len(data), maxMessageSize)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("写出消息头失败: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("写出消息体失败: %w", err)
	}
	return nil
}

func readMessage(r io.Reader, v any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > maxMessageSize {
		return fmt.Errorf("消息长度 %d 超过上限 %d", size, maxMessageSize)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
