// 本地 mock MCP 服务器, 用于 M3 端到端测试 (非生产组件)
//
// 运行: go run ./tools/mock-mcp-server
// 环境变量:
//
//	MOCK_MCP_PORT    监听端口 (默认 9100)
//	MOCK_MCP_API_KEY 如设置, 要求请求携带 Authorization: Bearer <key>
//
// 端点:
//
//	POST /mcp      JSON-RPC (initialize / tools/list / tools/call)
//	               默认返回 application/json; 带 ?sse=1 时返回 text/event-stream
//	GET  /sse      legacy SSE 握手: 先推送消息端点事件, 保持连接
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type toolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

var sessionCounter int64

func main() {
	port := getEnv("MOCK_MCP_PORT", "9100")
	apiKey := os.Getenv("MOCK_MCP_API_KEY")

	http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if err := checkAuth(r, apiKey); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32001,"message":"unauthorized"}}`)
			return
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		log.Printf("mock-mcp: %s %s", r.Method, req.Method)

		resp := handleRPC(&req)
		if resp == nil {
			// 通知类消息 (如 notifications/initialized)
			w.WriteHeader(http.StatusAccepted)
			return
		}

		if r.URL.Query().Get("sse") == "1" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(resp))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(resp)
	})

	// legacy SSE 握手: 推送消息端点后保持连接 (客户端完成调用后断开)
	http.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		if err := checkAuth(r, apiKey); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		sessionID := atomic.AddInt64(&sessionCounter, 1)
		fmt.Fprintf(w, "event: endpoint\ndata: /mcp?session=%d\n\n", sessionID)
		if flusher != nil {
			flusher.Flush()
		}
		log.Printf("mock-mcp: sse handshake session=%d", sessionID)
		// 保持连接, 直到客户端断开或超时
		select {
		case <-r.Context().Done():
		case <-time.After(60 * time.Second):
		}
	})

	addr := ":" + port
	log.Printf("mock-mcp listening on %s (auth=%v)", addr, apiKey != "")
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
}

func checkAuth(r *http.Request, apiKey string) error {
	if apiKey == "" {
		return nil
	}
	if r.Header.Get("Authorization") != "Bearer "+apiKey {
		return fmt.Errorf("bad token")
	}
	return nil
}

func handleRPC(req *rpcRequest) []byte {
	var result interface{}
	var rpcErr *rpcErrorPayload

	switch req.Method {
	case "initialize":
		result = map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"serverInfo":      map[string]interface{}{"name": "mock-mcp", "version": "1.0.0"},
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		}
	case "tools/list":
		result = map[string]interface{}{
			"tools": []toolDef{
				{
					Name:        "kb.search",
					Description: "Search the knowledge base",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"query": map[string]interface{}{"type": "string"},
						},
						"required": []string{"query"},
					},
				},
				{
					Name:        "ticket.create",
					Description: "Create a support ticket",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title": map[string]interface{}{"type": "string"},
						},
					},
				},
				{
					Name:        "echo",
					Description: "Echo the input message",
					InputSchema: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{"message": map[string]interface{}{"type": "string"}},
					},
				},
			},
		}
	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			rpcErr = &rpcErrorPayload{Code: -32602, Message: "invalid params"}
		} else {
			switch params.Name {
			case "kb.search":
				query, _ := params.Arguments["query"].(string)
				result = callResult(fmt.Sprintf("mock kb results for %q (3 hits)", query))
			case "ticket.create":
				result = callResult(fmt.Sprintf("ticket created: TK-%d", atomic.AddInt64(&sessionCounter, 1)))
			case "echo":
				msg, _ := params.Arguments["message"].(string)
				result = callResult("echo: " + msg)
			default:
				rpcErr = &rpcErrorPayload{Code: -32602, Message: "unknown tool: " + params.Name}
			}
		}
	default:
		rpcErr = &rpcErrorPayload{Code: -32601, Message: "method not found: " + req.Method}
	}

	payload := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
	if rpcErr != nil {
		payload["error"] = rpcErr
	} else {
		payload["result"] = result
	}
	data, _ := json.Marshal(payload)
	return data
}

func callResult(text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": false,
	}
}

type rpcErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
