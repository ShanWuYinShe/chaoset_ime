package ipc

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"chaoset.ime/engine"
)

// Client 是 daemon 的客户端句柄，实现 engine.Controller，
// 与本地 engine.Session 可互换使用（CLI / 平台壳只面对接口）。
type Client struct {
	conn net.Conn
	br   *bufio.Reader
	mu   sync.Mutex
	next uint64
}

// Dial 连接 daemon。Unix socket 场景 network 为 "unix"、address 为套接字路径；
// Windows 命名管道接入时换传输参数即可，协议不变。
func Dial(network, address string) (*Client, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, fmt.Errorf("连接输入法 daemon 失败: %w", err)
	}
	return &Client{conn: conn, br: bufio.NewReader(conn)}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

// Ping 探活：daemon 未就绪时调用方应降级放行按键，而不是等待。
func (c *Client) Ping() error {
	return c.call("ping", nil, nil)
}

func (c *Client) call(method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	req := Request{ID: c.next, Method: method}
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = data
	}
	if err := writeMessage(c.conn, req); err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	var resp Response
	if err := readMessage(c.br, &resp); err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("daemon 返回错误: %s", resp.Error)
	}
	if result != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, result)
	}
	return nil
}

// CandidateImage 取当前组词的候选窗渲染图（v2 统一候选窗）。
// 无候选时 data 为 nil；daemon 未启用渲染时返回错误（壳回退 v1 UI）。
func (c *Client) CandidateImage() (width, height int, data []byte, err error) {
	var out struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
		PNG    string `json:"png"`
	}
	if err = c.call("candidateImage", nil, &out); err != nil {
		return 0, 0, nil, err
	}
	if out.PNG == "" {
		return out.Width, out.Height, nil, nil
	}
	data, err = base64.StdEncoding.DecodeString(out.PNG)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("解码渲染图失败: %w", err)
	}
	return out.Width, out.Height, data, nil
}

func (c *Client) Input(text string) (engine.Composition, error) {
	var comp engine.Composition
	err := c.call("input", map[string]string{"text": text}, &comp)
	return comp, err
}

func (c *Client) Backspace() (engine.Composition, error) {
	var comp engine.Composition
	err := c.call("backspace", nil, &comp)
	return comp, err
}

func (c *Client) Reset() (engine.Composition, error) {
	var comp engine.Composition
	err := c.call("reset", nil, &comp)
	return comp, err
}

func (c *Client) Select(index int) (string, error) {
	var out struct {
		Committed string `json:"committed"`
	}
	if err := c.call("select", map[string]int{"index": index}, &out); err != nil {
		return "", err
	}
	return out.Committed, nil
}
