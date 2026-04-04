package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/Woo-kk/tmux-ghostty/internal/logx"
)

const (
	CodeBrokerUnavailable  = -32001
	CodeGhosttyUnavailable = -32002
	CodeTmuxUnavailable    = -32003
	CodePaneNotFound       = -32004
	CodeNotController      = -32005
	CodeApprovalRequired   = -32006
	CodeInvalidState       = -32007
	CodeJumpAttachFailed   = -32008
)

const (
	ReasonBrokerUnavailable  = "broker_unavailable"
	ReasonGhosttyUnavailable = "ghostty_unavailable"
	ReasonTmuxUnavailable    = "tmux_unavailable"
	ReasonPaneNotFound       = "pane_not_found"
	ReasonNotController      = "not_controller"
	ReasonApprovalRequired   = "approval_required"
	ReasonInvalidState       = "invalid_state"
	ReasonJumpAttachFailed   = "jump_attach_failed"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      string    `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	if e.Data == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Data)
}

type Handler func(ctx context.Context, method string, params json.RawMessage) (any, *RPCError)

type Server struct {
	SocketPath string
	Log        *logx.Logger
	Handler    Handler
}

type Client struct {
	SocketPath string
	Timeout    time.Duration
}

func NewError(code int, reason string, detail any) *RPCError {
	return &RPCError{
		Code:    code,
		Message: reason,
		Data:    detail,
	}
}

func NewClient(socketPath string) *Client {
	return &Client{
		SocketPath: socketPath,
		Timeout:    10 * time.Second,
	}
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return NewError(CodeBrokerUnavailable, ReasonBrokerUnavailable, err.Error())
	}
	defer conn.Close()

	request := Request{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		request.Params = raw
	}

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return err
	}

	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return err
	}
	if response.Error != nil {
		return response.Error
	}
	if result == nil || response.Result == nil {
		return nil
	}
	raw, err := json.Marshal(response.Result)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func (s *Server) Listen(ctx context.Context) error {
	if err := os.MkdirAll(filepathDir(s.SocketPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(s.SocketPath)
	ln, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return err
	}
	defer func() {
		ln.Close()
		_ = os.Remove(s.SocketPath)
	}()
	if err := os.Chmod(s.SocketPath, 0o600); err != nil && s.Log != nil {
		s.Log.Error("rpc.chmod_socket_failed", map[string]any{"path": s.SocketPath, "error": err.Error()})
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	var request Request
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{
			JSONRPC: "2.0",
			Error:   NewError(CodeInvalidState, ReasonInvalidState, err.Error()),
		})
		return
	}
	result, rpcErr := s.Handler(ctx, request.Method, request.Params)
	response := Response{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  result,
		Error:   rpcErr,
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func filepathDir(path string) string {
	index := len(path) - 1
	for index >= 0 {
		if path[index] == '/' {
			if index == 0 {
				return "/"
			}
			return path[:index]
		}
		index--
	}
	return "."
}
