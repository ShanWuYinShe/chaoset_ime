package ipc

import (
	"bufio"
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

func (c *Client) BackspaceWord() (engine.Composition, error) {
	var comp engine.Composition
	err := c.call("backspaceWord", nil, &comp)
	return comp, err
}

func (c *Client) Reset() (engine.Composition, error) {
	var comp engine.Composition
	err := c.call("reset", nil, &comp)
	return comp, err
}

// NextPage / PrevPage 翻动候选页，返回当前页组成状态（边界由 daemon 钳制）。
func (c *Client) NextPage() (engine.Composition, error) {
	var comp engine.Composition
	err := c.call("nextPage", nil, &comp)
	return comp, err
}

func (c *Client) PrevPage() (engine.Composition, error) {
	var comp engine.Composition
	err := c.call("prevPage", nil, &comp)
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

// CommitRaw 清空当前组词并返回用户输入的原始拼音（供回车直接上屏使用）。
func (c *Client) CommitRaw() (string, error) {
	var out struct {
		Committed string `json:"committed"`
	}
	if err := c.call("commitRaw", nil, &out); err != nil {
		return "", err
	}
	return out.Committed, nil
}

// Punct 提交一个标点键：组词中先上屏首选候选，随后输出映射后的
// 中文标点；空组词直接输出中文标点（映射规则见 engine.Session.Punct）。
func (c *Client) Punct(key string) (string, error) {
	var out struct {
		Committed string `json:"committed"`
	}
	if err := c.call("punct", map[string]string{"key": key}, &out); err != nil {
		return "", err
	}
	return out.Committed, nil
}

// UserStats 查询用户词典三类学习记录的条目数。
func (c *Client) UserStats() (engine.UserStats, error) {
	var st engine.UserStats
	err := c.call("userStats", nil, &st)
	return st, err
}

// UserClear 清空 daemon 内存与磁盘上的全部学习记录（调频/词组/上下文）。
func (c *Client) UserClear() error {
	return c.call("userClear", nil, nil)
}

// UserExport 导出学习记录 TSV；phrasesOnly 只导出用户词组
// （词\t拼音\t次数 三列，可直接作为 chaoset-import 输入）。
func (c *Client) UserExport(phrasesOnly bool) (string, error) {
	var out struct {
		TSV string `json:"tsv"`
	}
	params := map[string]bool{"phrases_only": phrasesOnly}
	if err := c.call("userExport", params, &out); err != nil {
		return "", err
	}
	return out.TSV, nil
}

// ToggleHalfWidth 切换全角/半角标点模式，返回切换后是否为半角。
func (c *Client) ToggleHalfWidth() (bool, error) {
	var out struct {
		HalfWidth bool `json:"half_width"`
	}
	if err := c.call("toggleHalfWidth", nil, &out); err != nil {
		return false, err
	}
	return out.HalfWidth, nil
}
