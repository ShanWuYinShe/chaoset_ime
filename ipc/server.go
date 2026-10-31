package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"runtime/debug"
	"strings"

	"chaoset.ime/engine"
)

// Server 在一条监听套接字上为每个连接维护独立的 engine.Session。
// 单连接内请求串行处理（输入法按键天然是顺序的），多连接互不影响。
type Server struct {
	ln           net.Listener
	dict         *engine.Dictionary
	user         *engine.UserDict
	fuzzy        bool   // 可选：新会话启用模糊音
	traditional  bool   // 可选：新会话启用繁体输出
	doublePinyin string // 可选：新会话的双拼方案（空 = 关闭）
}

// Option 定制 Server 可选能力。
type Option func(*Server)

// WithFuzzy 为每个连接的新会话启用模糊音（规则见 engine/fuzzy.go）。
func WithFuzzy(on bool) Option {
	return func(s *Server) { s.fuzzy = on }
}

// WithTraditional 为每个连接的新会话启用繁体输出（见 engine/s2t.go）。
func WithTraditional(on bool) Option {
	return func(s *Server) { s.traditional = on }
}

// WithDoublePinyin 为每个连接的新会话启用指定双拼方案（zrm/mspy，
// 见 engine/double_pinyin.go）。
func WithDoublePinyin(scheme string) Option {
	return func(s *Server) { s.doublePinyin = scheme }
}

func NewServer(ln net.Listener, dict *engine.Dictionary, user *engine.UserDict, opts ...Option) *Server {
	s := &Server{ln: ln, dict: dict, user: user}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Serve 阻塞接受连接，直到 listener 被关闭（返回 nil）或出错。
func (s *Server) Serve() error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("接受连接失败: %w", err)
		}
		// TCP 连接记录到 daemon 日志（每壳进程一条持久连接，不会刷屏）：
		// 开始菜单等 AppContainer 宿主能否到达 daemon，由此一眼可辨——
		// 排障时与壳的 tsf.log 对读（2026-10-29 开始菜单排障）。
		if _, isTCP := conn.RemoteAddr().(*net.TCPAddr); isTCP {
			log.Printf("ipc 连接: %s", conn.RemoteAddr())
		}
		go s.handleConn(conn)
	}
}

// Close 停止接受连接并保存用户词典。
func (s *Server) Close() error {
	if err := s.ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	if s.user != nil {
		if err := s.user.Save(); err != nil {
			return fmt.Errorf("保存用户词典失败: %w", err)
		}
	}
	return nil
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	// 单连接 panic 隔离（2026-10-31 审计 P2）：畸形请求或引擎边界 bug 触发
	// panic 时，Go 默认会带走整个 daemon——用户侧表现为输入法突然全部失效。
	// 这里 recover 后只结束该连接并记录堆栈，其余会话/壳不受影响。
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ipc 连接处理 panic（已隔离，daemon 继续运行）: %v\n%s", r, debug.Stack())
		}
	}()
	sess := engine.NewSession(s.dict, s.user)
	if s.fuzzy {
		sess.SetFuzzy(true)
	}
	if s.traditional {
		sess.SetTraditional(true)
	}
	if s.doublePinyin != "" {
		sess.SetDoublePinyin(s.doublePinyin)
	}
	for {
		var req Request
		if err := readMessage(conn, &req); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("ipc 连接读取异常: %v", err)
			}
			return
		}
		result, err := s.dispatch(sess, req)
		resp := Response{ID: req.ID}
		if err != nil {
			resp.Error = err.Error()
		} else if result != nil {
			data, merr := json.Marshal(result)
			if merr != nil {
				resp.Error = merr.Error()
			} else {
				resp.Result = data
			}
		}
		if err := writeMessage(conn, resp); err != nil {
			log.Printf("ipc 响应写出失败: %v", err)
			return
		}
	}
}

func (s *Server) dispatch(sess *engine.Session, req Request) (any, error) {
	switch req.Method {
	case "ping":
		return "pong", nil
	case "input":
		var p struct {
			Text string `json:"text"`
			// Half 可选：壳侧全/半角标点状态（2026-10-31 起每次 input 下发）。
			// nil = 旧壳未提供，保持会话内旧状态（向后兼容）；非 nil 时先同步
			// 再处理——双拼 dpPending==0 的 ';' 走 Punct 语义，宽度必须用本次
			// 壳状态，否则刚切全/半角后单按 ';' 会按旧宽度出标点（1 键瞬态）。
			Half *bool `json:"half"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("解析 input 参数失败: %w", err)
		}
		if p.Half != nil {
			return sess.InputWithHalfWidth(p.Text, *p.Half)
		}
		return sess.Input(p.Text)
	case "backspace":
		return sess.Backspace()
	case "backspaceWord":
		return sess.BackspaceWord()
	case "reset":
		return sess.Reset()
	case "commitRaw":
		text, err := sess.CommitRaw()
		if err != nil {
			return nil, err
		}
		return map[string]string{"committed": text}, nil
	case "nextPage":
		return sess.NextPage()
	case "prevPage":
		return sess.PrevPage()
	case "select":
		var p struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("解析 select 参数失败: %w", err)
		}
		text, err := sess.Select(p.Index)
		if err != nil {
			return nil, err
		}
		return map[string]string{"committed": text}, nil
	case "selectPrefix":
		// 渐进上屏：选中候选后只消耗其音节对应的 input 前缀，剩余拼音
		// 回灌继续组词。返回剩余组词态（committed=本次确认段文本——壳
		// 据此把它并入预编辑/合成，整条组词结束才落盘（2026-10-30 起三
		// 端统一，兼容对「组词中写正文」处理有缺陷的程序）；input/preedit=
		// 剩余拼音，candidates=剩余候选），壳据此刷新预编辑与候选。
		var p struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("解析 selectPrefix 参数失败: %w", err)
		}
		comp, committed, err := sess.SelectPrefix(p.Index)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"committed":  committed,
			"input":      comp.Input,
			"preedit":    comp.Preedit,
			"candidates": comp.Candidates,
			"page":       comp.Page,
			"page_count": comp.PageCount,
			// 双拼方案名随响应下发（壳据此路由 ';'）：selectPrefix 是手工
			// 拼的 map，不会自动带 Composition 字段，漏了会让壳在渐进上屏
			// 后把双拼 ';' 错当标点。
			"shuangpin": comp.Shuangpin,
		}, nil
	case "associate":
		// 联想候选：选词上屏后拼音区已清空，按上屏词音节查续接词组
		// （Candidate 列表，结构同 input 响应）。macOS 壳进入联想模式时调用。
		var pAsso struct {
			Sylls []string `json:"sylls"`
		}
		if err := json.Unmarshal(req.Params, &pAsso); err != nil {
			return nil, fmt.Errorf("解析 associate 参数失败: %w", err)
		}
		return map[string]any{
			"input": "", "preedit": "", "page": 0, "page_count": 1,
			"candidates": sess.Associate(pAsso.Sylls),
		}, nil
	case "associateSelect":
		// 联想候选直接上屏：学习路径与 select 完全一致（调频/共现/词组进化）。
		var pAssoSel struct {
			Word  string   `json:"word"`
			Sylls []string `json:"sylls"`
		}
		if err := json.Unmarshal(req.Params, &pAssoSel); err != nil {
			return nil, fmt.Errorf("解析 associateSelect 参数失败: %w", err)
		}
		text, err := sess.AssociateSelect(pAssoSel.Word, pAssoSel.Sylls)
		if err != nil {
			return nil, err
		}
		return map[string]string{"committed": text}, nil
	case "punct":
		var p struct {
			Key string `json:"key"`
			// Half 由壳随请求下发（壳本地持有全/半角开关，2026-10-29 起
			// 快捷键切换不再与 daemon 往返）。指针区分「旧壳未携带」与
			// 显式值：nil 时保持会话现状态。
			Half *bool `json:"half"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("解析 punct 参数失败: %w", err)
		}
		if p.Half != nil {
			sess.SetHalfWidth(*p.Half)
		}
		text, err := sess.Punct(p.Key)
		if err != nil {
			return nil, err
		}
		return map[string]string{"committed": text}, nil
	case "userStats":
		if s.user == nil {
			return nil, fmt.Errorf("未启用用户词典")
		}
		return s.user.Stats(), nil
	case "userClear":
		if s.user == nil {
			return nil, fmt.Errorf("未启用用户词典")
		}
		if err := s.user.Clear(); err != nil {
			return nil, err
		}
		return map[string]bool{"cleared": true}, nil
	case "userExport":
		if s.user == nil {
			return nil, fmt.Errorf("未启用用户词典")
		}
		var p struct {
			PhrasesOnly bool `json:"phrases_only"`
			// Offset/Limit 是分页参数：行偏移（0 起）与本页最大行数。
			//
			// 为什么分页（2026-10-30 审计 P1）：帧上限 1 MiB 只在读侧校验，
			// 此前整份 TSV 塞进单帧——学习数据无上限增长，重度用户的导出
			// 必然超过 1 MiB，客户端读帧被拒且流里残留未读 body（连接失步）。
			// Limit<=0 保持旧行为（全量导出、next=-1），兼容旧客户端。
			Offset int `json:"offset"`
			Limit  int `json:"limit"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				return nil, fmt.Errorf("解析 userExport 参数失败: %w", err)
			}
		}
		var buf bytes.Buffer
		if err := s.user.ExportTSV(&buf, p.PhrasesOnly); err != nil {
			return nil, err
		}
		tsv, next := paginateTSV(buf.String(), p.Offset, p.Limit)
		return map[string]any{"tsv": tsv, "next": next}, nil
	default:
		return nil, fmt.Errorf("未知方法 %q", req.Method)
	}
}

// paginateTSV 把整份导出 TSV 按「行」切页：offset 是起始行号（0 起），
// limit 是本页最大行数。返回本页文本与下一页 offset（全部导出完为 -1）。
//
// 每行保留行尾换行（SplitAfter），因此各页按序拼接与整份导出**逐字节一致**
// ——客户端无需关心段头/排序，直接拼接即可。limit<=0 时保持旧协议行为：
// 返回全量、next=-1。
func paginateTSV(full string, offset, limit int) (page string, next int) {
	if limit <= 0 {
		return full, -1
	}
	if offset < 0 {
		offset = 0
	}
	lines := strings.SplitAfter(full, "\n")
	// SplitAfter 对以换行结尾的文本会多出一个空元素，去掉它，避免空页/多算一行。
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if offset >= len(lines) {
		return "", -1
	}
	end := offset + limit
	if end >= len(lines) {
		return strings.Join(lines[offset:], ""), -1
	}
	return strings.Join(lines[offset:end], ""), end
}
