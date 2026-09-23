package ipc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"log"
	"net"

	"chaoset.ime/engine"
	"chaoset.ime/renderer"
)

// Server 在一条监听套接字上为每个连接维护独立的 engine.Session。
// 单连接内请求串行处理（输入法按键天然是顺序的），多连接互不影响。
type Server struct {
	ln           net.Listener
	dict         *engine.Dictionary
	user         *engine.UserDict
	skin         *renderer.Skin // 可选：启用 candidateImage 候选窗渲染
	fuzzy        bool           // 可选：新会话启用模糊音
	traditional  bool           // 可选：新会话启用繁体输出
	doublePinyin bool           // 可选：新会话启用自然码双拼
}

// Option 定制 Server 可选能力。
type Option func(*Server)

// WithSkin 启用统一候选窗渲染（v2）：壳经 candidateImage 方法取
// 渲染好的 PNG 贴图显示。
func WithSkin(skin renderer.Skin) Option {
	return func(s *Server) { s.skin = &skin }
}

// WithFuzzy 为每个连接的新会话启用模糊音（规则见 engine/fuzzy.go）。
func WithFuzzy(on bool) Option {
	return func(s *Server) { s.fuzzy = on }
}

// WithTraditional 为每个连接的新会话启用繁体输出（见 engine/s2t.go）。
func WithTraditional(on bool) Option {
	return func(s *Server) { s.traditional = on }
}

// WithDoublePinyin 为每个连接的新会话启用自然码双拼（见 engine/double_pinyin.go）。
func WithDoublePinyin(on bool) Option {
	return func(s *Server) { s.doublePinyin = on }
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
	sess := engine.NewSession(s.dict, s.user)
	if s.fuzzy {
		sess.SetFuzzy(true)
	}
	if s.traditional {
		sess.SetTraditional(true)
	}
	if s.doublePinyin {
		sess.SetDoublePinyin(true)
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
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("解析 input 参数失败: %w", err)
		}
		return sess.Input(p.Text)
	case "backspace":
		return sess.Backspace()
	case "reset":
		return sess.Reset()
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
	case "punct":
		var p struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("解析 punct 参数失败: %w", err)
		}
		text, err := sess.Punct(p.Key)
		if err != nil {
			return nil, err
		}
		return map[string]string{"committed": text}, nil
	case "toggleHalfWidth":
		return map[string]bool{"half_width": sess.ToggleHalfWidth()}, nil
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
		return map[string]string{"tsv": buf.String()}, nil
	case "candidateImage":
		if s.skin == nil {
			return nil, fmt.Errorf("未启用候选窗渲染（daemon 未配置字体）")
		}
		comp := sess.Compose()
		data, err := renderer.Render(comp, *s.skin)
		if err != nil {
			return nil, fmt.Errorf("渲染候选窗失败: %w", err)
		}
		out := map[string]any{"width": 0, "height": 0, "png": ""}
		if len(data) > 0 {
			cfg, derr := png.DecodeConfig(bytes.NewReader(data))
			if derr != nil {
				return nil, fmt.Errorf("解析渲染结果失败: %w", derr)
			}
			out["width"] = cfg.Width
			out["height"] = cfg.Height
			out["png"] = base64.StdEncoding.EncodeToString(data)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("未知方法 %q", req.Method)
	}
}
