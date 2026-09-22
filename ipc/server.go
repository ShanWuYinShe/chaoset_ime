package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"

	"chaoset.ime/engine"
)

// Server 在一条监听套接字上为每个连接维护独立的 engine.Session。
// 单连接内请求串行处理（输入法按键天然是顺序的），多连接互不影响。
type Server struct {
	ln   net.Listener
	dict *engine.Dictionary
	user *engine.UserDict
}

func NewServer(ln net.Listener, dict *engine.Dictionary, user *engine.UserDict) *Server {
	return &Server{ln: ln, dict: dict, user: user}
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

func (s *Server) dispatch(sess engine.Controller, req Request) (any, error) {
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
	default:
		return nil, fmt.Errorf("未知方法 %q", req.Method)
	}
}
