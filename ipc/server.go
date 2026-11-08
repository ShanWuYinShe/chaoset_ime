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
	"sync"

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

	// 连接生命周期（2026-11-08 审计 GO-09）：Close 要「先停接受、关掉活跃连接、
	// 等 handler 退出，再 Save」——否则 Save 之后仍可能有会话在 BumpWord，
	// 退出时丢失最后一次学习。connMu 保护 closing/conns，wg 只统计已登记的
	// handler；Add 必须在同一把锁内检查 closing，避免与 Wait 竞争。
	connMu  sync.Mutex
	conns   map[net.Conn]struct{}
	closing bool
	wg      sync.WaitGroup

	closeOnce sync.Once
	closeErr  error
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
	s := &Server{ln: ln, dict: dict, user: user, conns: make(map[net.Conn]struct{})}
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
		// 每条持久连接记录一行（每个壳进程一条，不会按键刷屏）：排查「壳是否
		// 真正到达 daemon」时，与壳侧日志对读即可区分未连接 / ACL 拒绝 / 连上后
		// 业务错误。Unix socket 与命名管道两种传输用同一行格式记录，便于对读。
		remote := conn.RemoteAddr()
		network, addr := "unknown", ""
		if remote != nil {
			network, addr = remote.Network(), remote.String()
		}
		log.Printf("ipc 连接: network=%s addr=%s", network, addr)
		if !s.trackConn(conn) {
			_ = conn.Close()
			return nil
		}
		go func() {
			defer s.untrackConn(conn)
			s.handleConn(conn)
		}()
	}
}

// trackConn 登记一条已接受的连接；Close 开始后返回 false（调用方应立即关闭）。
//
// Add 与 closing 检查在同一把锁内完成：否则 Accept 与 Close 竞争时可能在
// Wait 启动之后才 Add，违反 WaitGroup 的使用约定（也会漏掉这条连接的退出等待）。
func (s *Server) trackConn(conn net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.closing {
		return false
	}
	s.conns[conn] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) untrackConn(conn net.Conn) {
	s.connMu.Lock()
	delete(s.conns, conn)
	s.connMu.Unlock()
	s.wg.Done()
}

// Close 停止接受连接、关闭所有活跃连接并等待 handler 退出，最后保存用户词典。
//
// 退出顺序（2026-11-08 审计 GO-09 P2）：旧实现只关 listener 就 Save，已接受连接
// 的 goroutine 仍可能在 Save 之后写学习数据（daemon 退出丢最后一次学习）。
// 现在先关活跃连接（让 handler 的阻塞读立刻返回）并等它们全部退出，再 Save；
// 调用方可重复调用（幂等）。
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.connMu.Lock()
		s.closing = true
		conns := make([]net.Conn, 0, len(s.conns))
		for conn := range s.conns {
			conns = append(conns, conn)
		}
		s.connMu.Unlock()

		if err := s.ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.closeErr = err
		}
		// 关掉活跃连接：handler 的 readMessage 会立即返回，随后退出循环。
		for _, conn := range conns {
			_ = conn.Close()
		}
		s.wg.Wait()
		if s.closeErr == nil && s.user != nil {
			if err := s.user.Save(); err != nil {
				s.closeErr = fmt.Errorf("保存用户词典失败: %w", err)
			}
		}
	})
	return s.closeErr
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
	// 每个连接只记第一条业务错误，避免某个异常请求把日志刷满；错误本身仍
	// 原样返回给壳，不改变协议行为。
	var loggedRequestError bool
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
			if !loggedRequestError {
				loggedRequestError = true
				log.Printf("ipc 请求失败: method=%s err=%v", req.Method, err)
			}
		} else if result != nil {
			data, merr := json.Marshal(result)
			if merr != nil {
				resp.Error = merr.Error()
			} else if len(data) > maxMessageSize {
				// 单帧超过协议上限：返回错误响应而不是写半帧（写了会让对端
				// 读帧失败、流里残留 body）。正常路径由分页保证不触发。
				resp.Error = fmt.Sprintf("响应长度 %d 超过上限 %d", len(data), maxMessageSize)
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

// 请求参数结构体：字段名/类型即 IPC JSON 契约。集中定义 + 泛型解码，避免每个
// case 重复「匿名 struct + json.Unmarshal + 错误包装」（旧实现 12 处重复）；
// 新增方法时只加结构体与一行动词，错误文案由 req.Method 统一生成。
type inputParams struct {
	Text string `json:"text"`
	// Half 可选：壳侧全/半角标点状态，**本次请求内生效，daemon 不保存**
	// （2026-11-05 起：壳是唯一真源，daemon 不再持有镜像状态）。
	// nil = 旧壳未携带，按全角处理（历史默认），不回落到会话残留值。
	Half *bool `json:"half"`
}

// indexParams 是「按候选序号操作」类方法的参数（select / selectPrefix）。
type indexParams struct {
	Index int `json:"index"`
}

type associateParams struct {
	Sylls []string `json:"sylls"`
}

type associateSelectParams struct {
	Word  string   `json:"word"`
	Sylls []string `json:"sylls"`
}

type punctParams struct {
	Key string `json:"key"`
	// Half 由壳随请求下发（壳本地持有全/半角开关）。指针区分「旧壳未携带」
	// 与「显式 false」：nil 时按全角处理（历史默认），不回落会话状态。
	Half *bool `json:"half"`
}

type userDeleteParams struct {
	Word string `json:"word"`
	// Syllables 是 ' 或空格分隔的音节串（macOS 壳/CLI 用）；
	// Sylls 是结构化数组（Windows core::Session 用），二者取其一。
	Syllables string   `json:"syllables"`
	Sylls     []string `json:"sylls"`
}

type uiParams struct {
	Settings bool `json:"settings"`
}

type userExportParams struct {
	PhrasesOnly bool `json:"phrases_only"`
	// Offset/Limit 是分页参数：行偏移（0 起）与本页最大行数。
	//
	// 为什么分页（2026-10-30 审计 P1）：帧上限 1 MiB 只在读侧校验，此前整份
	// TSV 塞进单帧——学习数据无上限增长，重度用户的导出必然超过 1 MiB，客户端
	// 读帧被拒且流里残留未读 body（连接失步）。Limit<=0 保持旧行为（全量导出、
	// next=-1），兼容旧客户端。
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

// decodeParams 解码**必填**参数：空 params 与非法 JSON 都返回「解析 <method>
// 参数失败」，与旧实现逐字一致的错误文案（客户端会透传该文案）。
func decodeParams[T any](req Request) (T, error) {
	var p T
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return p, fmt.Errorf("解析 %s 参数失败: %w", req.Method, err)
	}
	return p, nil
}

// decodeOptionalParams 解码**可选**参数：空 params 返回零值（兼容旧客户端不
// 传参的调用；userDelete/ui/userExport 三处历史语义）。
func decodeOptionalParams[T any](req Request) (T, error) {
	var p T
	if len(req.Params) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return p, fmt.Errorf("解析 %s 参数失败: %w", req.Method, err)
	}
	return p, nil
}

// requireUser 统一「未启用用户词典」错误（userStats/userClear/userDelete/
// userExport 四个 case 共用）。
func (s *Server) requireUser() error {
	if s.user == nil {
		return fmt.Errorf("未启用用户词典")
	}
	return nil
}

func (s *Server) dispatch(sess *engine.Session, req Request) (any, error) {
	switch req.Method {
	case "ping":
		return "pong", nil
	case "input":
		// 宽度随请求下发（壳是唯一真源）：nil = 旧壳未携带，按全角处理；
		// 引擎不保存该状态，因此不存在「上次残留」的可能。
		p, err := decodeParams[inputParams](req)
		if err != nil {
			return nil, err
		}
		return sess.Input(p.Text, p.Half != nil && *p.Half)
	case "backspace":
		return sess.Backspace()
	case "backspaceWord":
		return sess.BackspaceWord()
	case "reset":
		return sess.Reset()
	case "resetComposition":
		// 只丢弃当前组词、保留 prev/history 等学习上下文。
		// 切中英模式/光标变化等「同一会话内继续打字」的场景应走这个，
		// 走 reset 会让引擎忘记刚上屏的字，影响后续候选打分（2026-11-05）。
		return sess.ResetComposition()
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
		p, err := decodeParams[indexParams](req)
		if err != nil {
			return nil, err
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
		p, err := decodeParams[indexParams](req)
		if err != nil {
			return nil, err
		}
		// 直接回传 Composition：Session.SelectPrefix 已把 committed 写进
		// comp.Committed（engine/session.go）。此前这里手工拼 map 复制字段，
		// Composition 新增字段时会被漏掉（shuangpin 就人工补过一次），是协议
		// 漂移点。
		comp, _, err := sess.SelectPrefix(p.Index)
		if err != nil {
			return nil, err
		}
		return comp, nil
	case "associate":
		// 联想候选：选词上屏后拼音区已清空，按上屏词音节查续接词组
		// （Candidate 列表，结构同 input 响应）。macOS 壳进入联想模式时调用。
		p, err := decodeParams[associateParams](req)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"input": "", "preedit": "", "page": 0, "page_count": 1,
			"candidates": sess.Associate(p.Sylls),
		}, nil
	case "associateSelect":
		// 联想候选直接上屏：学习路径与 select 完全一致（调频/共现/词组进化）。
		p, err := decodeParams[associateSelectParams](req)
		if err != nil {
			return nil, err
		}
		text, err := sess.AssociateSelect(p.Word, p.Sylls)
		if err != nil {
			return nil, err
		}
		return map[string]string{"committed": text}, nil
	case "punct":
		p, err := decodeParams[punctParams](req)
		if err != nil {
			return nil, err
		}
		half := p.Half != nil && *p.Half
		text, err := sess.Punct(p.Key, half)
		if err != nil {
			return nil, err
		}
		return map[string]string{"committed": text}, nil
	case "userStats":
		if err := s.requireUser(); err != nil {
			return nil, err
		}
		return s.user.Stats(), nil
	case "userClear":
		if err := s.requireUser(); err != nil {
			return nil, err
		}
		if err := s.user.Clear(); err != nil {
			return nil, err
		}
		return map[string]bool{"cleared": true}, nil
	case "userDelete":
		// 按词删除学习数据（候选窗右键「删除该词」/ chaoset-userdict delete）：
		// 只从共享 UserDict 删记录，不触碰任何连接的组词会话——壳拿到 deleted
		// 后自行重新组词刷新候选（daemon 不缓存候选列表，重算即最新）。
		if err := s.requireUser(); err != nil {
			return nil, err
		}
		p, err := decodeOptionalParams[userDeleteParams](req)
		if err != nil {
			return nil, err
		}
		word := strings.TrimSpace(p.Word)
		if word == "" {
			return nil, fmt.Errorf("userDelete 缺少 word 参数")
		}
		sylls := p.Sylls
		if len(sylls) == 0 && strings.TrimSpace(p.Syllables) != "" {
			sylls = strings.Fields(strings.ReplaceAll(p.Syllables, "'", " "))
		}
		deleted, err := s.user.DeleteWord(word, sylls)
		if err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": deleted}, nil
	case "ui":
		// 由 daemon 代 AppContainer 宿主拉起单例 UI 宿主（chaoset-ui.exe）。
		// 仅 Windows 有实现；其它平台返回错误（不会有壳调用该方法）。
		// daemon 以当前交互用户、普通完整性运行，CreateProcess 创建的子进程
		// 不会继承 AC 身份，因此开始菜单/搜索里也能打开设置窗。
		//
		// settings=true 时让 UI 宿主带 --settings 启动（2026-11-06）：这样
		// 首实例直接弹出设置窗，TIP 侧不需要再轮询等管道就绪。
		p, err := decodeOptionalParams[uiParams](req)
		if err != nil {
			return nil, err
		}
		if err := ensureUIHost(p.Settings); err != nil {
			return nil, err
		}
		return map[string]bool{"started": true}, nil
	case "userExport":
		if err := s.requireUser(); err != nil {
			return nil, err
		}
		p, err := decodeOptionalParams[userExportParams](req)
		if err != nil {
			return nil, err
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
