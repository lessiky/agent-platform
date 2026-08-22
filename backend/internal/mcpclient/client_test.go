package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// statefulTestServer 模拟有状态 streamable http MCP 服务器 (FastMCP 默认行为):
// initialize 响应头下发 Mcp-Session-Id; 其余请求缺少会话头或携带未知会话头时
// 返回 400 "Bad Request: Missing session ID"。
type statefulTestServer struct {
	server    *httptest.Server
	initCount int64

	mu       sync.Mutex
	sessions map[string]bool
}

func newStatefulTestServer() *statefulTestServer {
	s := &statefulTestServer{sessions: map[string]bool{}}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.Method == "initialize" {
			atomic.AddInt64(&s.initCount, 1)
			sid := fmt.Sprintf("sess-%d", atomic.LoadInt64(&s.initCount))
			s.mu.Lock()
			s.sessions[sid] = true
			s.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			s.respond(w, http.StatusOK, req.ID, map[string]interface{}{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]interface{}{"name": "stateful-test", "version": "1.0.0"},
				"capabilities":    map[string]interface{}{},
			})
			return
		}

		sid := r.Header.Get("Mcp-Session-Id")
		s.mu.Lock()
		known := s.sessions[sid]
		s.mu.Unlock()
		if !known {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":"server-error","error":{"code":-32600,"message":"Bad Request: Missing session ID"}}`)
			return
		}

		switch req.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			s.respond(w, http.StatusOK, req.ID, map[string]interface{}{
				"tools": []map[string]interface{}{{
					"name":        "echo",
					"description": "echo",
					"inputSchema": map[string]interface{}{"type": "object"},
				}},
			})
		case "tools/call":
			s.respond(w, http.StatusOK, req.ID, map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
				"isError": false,
			})
		default:
			s.respond(w, http.StatusOK, req.ID, map[string]interface{}{})
		}
	}))
	return s
}

func (s *statefulTestServer) respond(w http.ResponseWriter, status int, id json.RawMessage, result interface{}) {
	payload := map[string]interface{}{"jsonrpc": "2.0"}
	if len(id) > 0 {
		payload["id"] = json.RawMessage(id)
	}
	payload["result"] = result
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *statefulTestServer) Close() { s.server.Close() }

// TestStatefulServerSessionHandshake 有状态服务器: 显式 Initialize 后 ListTools/CallTool
// 复用同一会话 (只握手一次)。
func TestStatefulServerSessionHandshake(t *testing.T) {
	srv := newStatefulTestServer()
	defer srv.Close()

	client := New(srv.server.URL, "http", nil, 0)
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	res, err := client.CallTool(context.Background(), "echo", map[string]interface{}{"message": "hi"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "ok" {
		t.Fatalf("unexpected call result: %+v", res)
	}

	if got := atomic.LoadInt64(&srv.initCount); got != 1 {
		t.Fatalf("expected exactly 1 initialize handshake, got %d", got)
	}
}

// TestCallToolAutoHandshake executeTool 路径: 未显式 Initialize 直接调用工具,
// 客户端应自动先完成握手再发起 tools/call。
func TestCallToolAutoHandshake(t *testing.T) {
	srv := newStatefulTestServer()
	defer srv.Close()

	client := New(srv.server.URL, "http", nil, 0)
	res, err := client.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "ok" {
		t.Fatalf("unexpected call result: %+v", res)
	}
}

// TestStatelessServerNoSession 无状态服务器 (如自带 mock-mcp-server): 不要求会话头,
// 且多次调用只握手一次。
func TestStatelessServerNoSession(t *testing.T) {
	var inits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")

		var result interface{}
		switch req.Method {
		case "initialize":
			atomic.AddInt64(&inits, 1)
			result = map[string]interface{}{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]interface{}{"name": "stateless-test", "version": "1.0.0"},
				"capabilities":    map[string]interface{}{},
			}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = map[string]interface{}{"tools": []map[string]interface{}{{"name": "echo"}}}
		case "tools/call":
			result = map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": "ok"}},
				"isError": false,
			}
		default:
			result = map[string]interface{}{}
		}
		payload := map[string]interface{}{"jsonrpc": "2.0", "result": result}
		if len(req.ID) > 0 {
			payload["id"] = json.RawMessage(req.ID)
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := New(srv.URL, "http", nil, 0)
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if _, err := client.CallTool(context.Background(), "echo", nil); err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if got := atomic.LoadInt64(&inits); got != 1 {
		t.Fatalf("stateless server should be initialized once, got %d", got)
	}
}
