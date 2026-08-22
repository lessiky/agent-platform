package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MCP JSON-RPC 2.0 客户端 (M3 工具发现/连通性检测/调用代理, PRD 2.2.1)
//
// 协议参考 Model Context Protocol:
//   - initialize: 握手, 协商协议版本并获取服务器信息
//   - tools/list: 工具发现
//   - tools/call: 工具调用
//
// 传输:
//   - http: POST JSON-RPC 到 endpoint, 响应可为 application/json 或 text/event-stream
//           有状态服务器 (如 FastMCP 默认) 在 initialize 响应头下发 Mcp-Session-Id,
//           客户端捕获后在后续请求中回传; 未握手时 ListTools/CallTool 自动先握手
//   - sse:  先 GET endpoint 做 legacy 握手获取消息端点, 再 POST 到该端点;
//           服务器不支持 legacy 握手时回退为直接 POST endpoint
//   - stdio: Phase 1 平台不托管子进程, 不支持 (返回明确错误)

const (
	TransportSSE = "sse"

	defaultTimeout = 10 * time.Second
	maxBodyBytes   = 1 << 20
)

var rpcIDCounter int64

func nextID() int64 {
	return atomic.AddInt64(&rpcIDCounter, 1)
}

// Client MCP 服务器客户端
type Client struct {
	endpoint  string
	transport string
	headers   map[string]string // 发送到服务器的额外请求头 (含认证凭证)
	http      *http.Client

	mu          sync.Mutex
	sessionId   string // streamable http 会话 ID (initialize 响应头 Mcp-Session-Id)
	initialized bool   // 已完成 initialize 握手 (无状态服务器握手一次即可)
}

// ServerInfo MCP 服务器信息
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult initialize 握手结果
type InitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	ServerInfo      ServerInfo             `json:"serverInfo"`
	Capabilities    map[string]interface{} `json:"capabilities,omitempty"`
}

// Tool MCP 工具定义
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
	// RequiresApproval 调用需人工审核 (M4.5), 平台侧配置, MCP 协议不携带该字段
	RequiresApproval bool `json:"requires_approval"`
}

// ToolContent tools/call 返回的内容块
type ToolContent struct {
	Type string      `json:"type"`
	Text string      `json:"text,omitempty"`
	Data interface{} `json:"data,omitempty"`
}

// CallResult tools/call 结果
type CallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"is_error"`
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	ID     json.Number     `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// New 创建 MCP 客户端; timeout <= 0 时使用默认 10s
func New(endpoint, transport string, headers map[string]string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		endpoint:  strings.TrimRight(endpoint, "/"),
		transport: transport,
		headers:   headers,
		http:      &http.Client{Timeout: timeout},
	}
}

// Initialize 协议握手。有状态 streamable http 服务器会在响应头下发会话 ID,
// 后续请求自动携带; 握手完成后按协议发送 notifications/initialized (尽力而为)。
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	c.mu.Lock()
	c.sessionId = "" // 新握手从干净会话开始, 不回传旧会话 ID
	c.initialized = false
	c.mu.Unlock()

	var result InitializeResult
	if err := c.call(ctx, "initialize", map[string]interface{}{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "agent-platform", "version": "0.1.0"},
	}, &result); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.initialized = true
	c.mu.Unlock()

	_ = c.notify(ctx, "notifications/initialized")
	return &result, nil
}

// ListTools 工具发现
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}
	var payload struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.call(ctx, "tools/list", map[string]interface{}{}, &payload); err != nil {
		return nil, err
	}
	return payload.Tools, nil
}

// CallTool 调用工具
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallResult, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}
	var result CallResult
	params := map[string]interface{}{"name": name}
	if arguments != nil {
		params["arguments"] = arguments
	}
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// call 发送 JSON-RPC 请求并把 result 解码到 out
func (c *Client) call(ctx context.Context, method string, params interface{}, out interface{}) error {
	target, cleanup, err := c.messageEndpoint(ctx)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	id := nextID()
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	if method != "initialize" {
		c.applySessionHeader(req)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mcp request failed: %w", err)
	}
	defer resp.Body.Close()

	c.captureSessionID(resp)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		c.invalidateSession(resp.StatusCode, msg)
		return fmt.Errorf("mcp server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var respData []byte
	if isSSE(resp) {
		respData, err = readSSEForResponse(resp.Body, id)
	} else {
		respData, err = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	}
	if err != nil {
		return fmt.Errorf("read mcp response failed: %w", err)
	}
	if len(bytes.TrimSpace(respData)) == 0 {
		return fmt.Errorf("mcp server returned empty response (202 session-only SSE not supported in phase 1)")
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respData, &rpcResp); err != nil {
		return fmt.Errorf("decode mcp response failed: %w", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("mcp rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if out != nil && len(rpcResp.Result) > 0 {
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			return fmt.Errorf("decode mcp result failed: %w", err)
		}
	}
	return nil
}

// ensureSession 确保已完成 initialize 握手: 有状态 streamable http 服务器要求
// 先建立会话才能发起其他请求; 无状态服务器完成一次握手后不再重复。
func (c *Client) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	ready := c.sessionId != "" || c.initialized
	c.mu.Unlock()
	if ready {
		return nil
	}
	_, err := c.Initialize(ctx)
	return err
}

// applySessionHeader 已建立会话时附加 Mcp-Session-Id 请求头
func (c *Client) applySessionHeader(req *http.Request) {
	c.mu.Lock()
	sid := c.sessionId
	c.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
}

// captureSessionID 捕获响应头中的 (新) 会话 ID, 服务器可能在任意响应中下发或轮换
func (c *Client) captureSessionID(resp *http.Response) {
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		return
	}
	c.mu.Lock()
	c.sessionId = sid
	c.mu.Unlock()
}

// invalidateSession 会话失效 (服务器重启/超时) 时清空, 使下次调用重新握手。
// 典型响应: 404 (TS SDK) 或 400 "Missing session ID" (FastMCP)。
func (c *Client) invalidateSession(status int, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stale := status == http.StatusNotFound ||
		(status == http.StatusBadRequest && bytes.Contains(bytes.ToLower(body), []byte("session")))
	if stale {
		c.sessionId = ""
		c.initialized = false
	}
}

// notify 发送 JSON-RPC 通知 (无 id, 服务器按规范不返回响应体); 202/200 均视为成功
func (c *Client) notify(ctx context.Context, method string) error {
	target, cleanup, err := c.messageEndpoint(ctx)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	body, err := json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	c.applySessionHeader(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mcp notification failed: %w", err)
	}
	defer resp.Body.Close()

	c.captureSessionID(resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("mcp server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	return nil
}

// messageEndpoint 返回 JSON-RPC 的 POST 目标 URL。
// sse 传输先尝试 legacy 握手 (GET endpoint 解析消息端点); 握手不可用时回退直连。
// 第二个返回值为连接清理函数 (握手成功后需保持 GET 连接直到调用完成)。
func (c *Client) messageEndpoint(ctx context.Context) (string, func(), error) {
	if c.transport != TransportSSE {
		return c.endpoint, nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return c.endpoint, nil, nil
	}

	if !isSSE(resp) {
		resp.Body.Close()
		return c.endpoint, nil, nil
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}

		// legacy 握手: data 为消息端点路径 (可能带 session 参数), 也可能是 JSON
		ref := data
		if strings.HasPrefix(data, "{") {
			var evt struct {
				Endpoint string `json:"endpoint"`
			}
			if json.Unmarshal([]byte(data), &evt) != nil || evt.Endpoint == "" {
				continue
			}
			ref = evt.Endpoint
		}

		abs, err := c.resolveURL(ref)
		if err != nil {
			resp.Body.Close()
			return "", nil, fmt.Errorf("resolve mcp message endpoint failed: %w", err)
		}
		return abs, func() { resp.Body.Close() }, nil
	}
	resp.Body.Close()
	return c.endpoint, nil, nil
}

// resolveURL 把相对端点解析为绝对 URL
func (c *Client) resolveURL(ref string) (string, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref, nil
	}
	base, err := url.Parse(c.endpoint)
	if err != nil {
		return "", err
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(refURL).String(), nil
}

func isSSE(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

// readSSEForResponse 读取 SSE 流, 返回与请求 id 匹配的 JSON-RPC 响应
func readSSEForResponse(body io.Reader, id int64) ([]byte, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var data []string
	tryParse := func() ([]byte, bool) {
		defer func() { data = nil }()
		if len(data) == 0 {
			return nil, false
		}
		payload := []byte(strings.Join(data, "\n"))
		var probe struct {
			ID *json.Number `json:"id"`
		}
		if err := json.Unmarshal(payload, &probe); err != nil || probe.ID == nil {
			return nil, false
		}
		if matched, _ := probe.ID.Int64(); matched == id {
			return payload, true
		}
		return nil, false
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if payload, ok := tryParse(); ok {
				return payload, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if payload, ok := tryParse(); ok {
		return payload, nil
	}
	return nil, fmt.Errorf("no matching json-rpc response in sse stream")
}
