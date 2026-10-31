package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

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

// pingTimeout 是 Ping 的硬上限。
//
// Ping 的契约是「探活，不等待」：daemon 未就绪时调用方应降级放行按键。
// 但 call 本身是阻塞的（无 deadline），若 daemon 进程还在、socket 也活着
// 却不回包（极端情况：卡在 GC 或持锁写盘），Ping 会一直阻塞，与契约相反。
// 取值与壳侧 IPC 超时同量级（macOS 250ms / Windows 200ms）。
const pingTimeout = 250 * time.Millisecond

// Ping 探活：daemon 未就绪时调用方应降级放行按键，而不是等待。
//
// 自带 pingTimeout 截止时间。超时后**连接会被关闭**：流里可能残留半个
// 请求/响应，继续在同一连接上解析会导致请求与响应错位（串包），所以宁可
// 让调用方重新 Dial，也不在状态不可信的流上返回错误的结果。
func (c *Client) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetDeadline(time.Now().Add(pingTimeout)); err != nil {
		return fmt.Errorf("设置探活超时失败: %w", err)
	}
	err := c.callLocked("ping", nil, nil)
	// 清除截止时间：deadline 是连接级状态，不清除会让后续正常调用继承它。
	if clrErr := c.conn.SetDeadline(time.Time{}); clrErr != nil && err == nil {
		return fmt.Errorf("清除探活超时失败: %w", clrErr)
	}
	if err != nil {
		_ = c.conn.Close()
	}
	return err
}

func (c *Client) call(method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.callLocked(method, params, result)
}

// callLocked 是 call 的持锁主体（Ping 需要在同一把锁内设置/清除 deadline）。
func (c *Client) callLocked(method string, params any, result any) error {
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

// InputWithHalfWidth 与 engine.Session.InputWithHalfWidth 对应：把壳侧的
// 全/半角标点状态随 input 一起下发。双拼模式下 ';' 经 input 路径产生标点
// 时，引擎必须用本次壳状态而不是上一次 punct 请求留下的旧值（2026-10-31）。
func (c *Client) InputWithHalfWidth(text string, halfWidth bool) (engine.Composition, error) {
	var comp engine.Composition
	err := c.call("input", map[string]any{"text": text, "half": halfWidth}, &comp)
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

// userExportPageLimit 是分页导出的单页行数上限。
//
// 2000 行的典型行宽（词/拼音/次数/时间，约 50–150 字节）远小于 1 MiB 帧
// 上限；取固定行数而不是按字节估算，是为了让页边界稳定、便于测试。
const userExportPageLimit = 2000

// UserExport 导出学习记录 TSV；phrasesOnly 只导出用户词组
// （词\t拼音\t次数 三列，可直接作为 chaoset-import 输入）。
//
// 内部按 userExportPageLimit 行分页拉取后拼接，对外仍返回完整 TSV——
// 调用方无需感知分页。为什么必须分页（2026-10-30 审计 P1）：帧上限
// 1 MiB 只在读侧校验，整份 TSV 单帧在重度用户（学习数据无上限增长）下
// 必然超限；客户端读帧被拒后流里残留未读 body，同一连接继续用会串包。
func (c *Client) UserExport(phrasesOnly bool) (string, error) {
	var b strings.Builder
	offset := 0
	for {
		var out struct {
			TSV  string `json:"tsv"`
			Next int    `json:"next"`
		}
		params := map[string]any{
			"phrases_only": phrasesOnly,
			"offset":       offset,
			"limit":        userExportPageLimit,
		}
		if err := c.call("userExport", params, &out); err != nil {
			// 读帧失败（含单页超 maxMessageSize）时流状态不可信：body 可能
			// 残留未读，继续在同一连接上发请求会请求/响应错位（串包）。
			// 宁可关闭连接让调用方重新 Dial。
			_ = c.conn.Close()
			return "", err
		}
		if len(out.TSV) > maxMessageSize {
			_ = c.conn.Close()
			return "", fmt.Errorf("导出分页（offset=%d）超过协议上限 %d 字节", offset, maxMessageSize)
		}
		b.WriteString(out.TSV)
		if out.Next < 0 {
			return b.String(), nil
		}
		if out.Next <= offset {
			// 防御：next 必须严格前进，否则循环不终止。旧版 daemon 不返回
			// next（字段缺省为 0）也会走到这里——报错而不是把首屏当完整
			// 导出返回，避免静默截断用户数据。
			_ = c.conn.Close()
			return "", fmt.Errorf("导出分页 next=%d 未前进（当前 offset=%d；旧版 daemon 可能不支持分页，请升级两端）", out.Next, offset)
		}
		offset = out.Next
	}
}

// SelectPrefix 渐进上屏：选中候选后只消耗其音节对应的 input 前缀，
// 剩余拼音回灌继续组词。返回剩余组词态（Committed=已上屏前缀段文本，
// Preedit=剩余拼音，Candidates=剩余候选）与本次上屏文本，签名与本地
// engine.Session.SelectPrefix 一致，供 engine.Controller 统一调用。
func (c *Client) SelectPrefix(index int) (engine.Composition, string, error) {
	var comp engine.Composition
	err := c.call("selectPrefix", map[string]int{"index": index}, &comp)
	return comp, comp.Committed, err
}
