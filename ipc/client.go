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
	return c.CandidateImageWith(CandidateImageOptions{})
}

// CandidateImageOptions 是 candidateImage 的可选渲染参数（零值 = 旧行为）：
//   - Dark：深色皮肤（跟随系统外观）
//   - Scale：像素倍率，2 = Retina 双倍像素，其余按 1
//
// 壳侧应把 NSImage.size 设为 图片像素/Scale，得到 Retina 清晰度。
type CandidateImageOptions struct {
	Dark  bool `json:"dark,omitempty"`
	Scale int  `json:"scale,omitempty"`
}

// CandidateImageWith 按 opts 取候选窗渲染图；返回的 scale 是 daemon 实际
// 采用的倍率（1 或 2），壳侧据此换算 NSImage.size，避免传了不支持的倍率
// 却按它缩放图片。
func (c *Client) CandidateImageWith(opts CandidateImageOptions) (width, height int, data []byte, err error) {
	w, h, d, _, err := c.CandidateImageDetailed(opts)
	return w, h, d, err
}

// CandidateImageDetailed 同 CandidateImageWith，但额外返回实际渲染倍率。
func (c *Client) CandidateImageDetailed(opts CandidateImageOptions) (width, height int, data []byte, scale int, err error) {
	var out struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
		PNG    string `json:"png"`
		Scale  int    `json:"scale"`
	}
	if err = c.call("candidateImage", opts, &out); err != nil {
		return 0, 0, nil, 0, err
	}
	scale = out.Scale
	if scale != 2 {
		scale = 1
	}
	if out.PNG == "" {
		return out.Width, out.Height, nil, scale, nil
	}
	data, err = base64.StdEncoding.DecodeString(out.PNG)
	if err != nil {
		return 0, 0, nil, 0, fmt.Errorf("解码渲染图失败: %w", err)
	}
	return out.Width, out.Height, data, scale, nil
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
