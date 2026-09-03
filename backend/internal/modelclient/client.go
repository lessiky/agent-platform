package modelclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 各提供商默认端点 (用户未填 endpoint 时使用)
var DefaultEndpoints = map[string]string{
	"openai":    "https://api.openai.com/v1",
	"anthropic": "https://api.anthropic.com",
	"google":    "https://generativelanguage.googleapis.com",
}

// ProbeResult 连通性探测结果 (验证端点可达 + API Key 有效, PRD 2.3.2 P0)
type ProbeResult struct {
	OK        bool
	LatencyMs int
	Models    []string // 服务商返回的可用模型 (若接口支持)
	Error     string
}

// Client 模型提供商连通性探测客户端
//
// Phase 1 采用各提供商的"模型列表"接口做轻量探测 (不产生 token 消耗):
//   - openai/custom: GET {endpoint}/models, Authorization: Bearer <key>
//   - anthropic:     GET {endpoint}/v1/models, x-api-key
//   - google:        GET {endpoint}/v1beta/models?key=<key>
//   - azure:         GET {endpoint}/openai/models?api-version=2024-06-01, api-key
type Client struct {
	Provider string
	Endpoint string
	APIKey   string
	timeout  time.Duration
	http     *http.Client
}

func New(provider, endpoint, apiKey string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		Provider: provider,
		Endpoint: endpoint,
		APIKey:   apiKey,
		timeout:  timeout,
		http:     &http.Client{Timeout: timeout},
	}
}

// Probe 执行连通性探测
func (c *Client) Probe(ctx context.Context) *ProbeResult {
	target, err := c.buildRequest()
	if err != nil {
		return &ProbeResult{OK: false, Error: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
	if err != nil {
		return &ProbeResult{OK: false, Error: err.Error()}
	}
	for key, value := range target.headers {
		req.Header.Set(key, value)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return &ProbeResult{OK: false, LatencyMs: latency, Error: truncate(err.Error(), 500)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusOK:
		if isHTMLResponse(body) {
			return &ProbeResult{OK: false, LatencyMs: latency, Error: "endpoint returned HTML instead of JSON, endpoint path may be incorrect (missing /v1?)"}
		}
		return &ProbeResult{OK: true, LatencyMs: latency, Models: parseModels(c.Provider, body)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return &ProbeResult{OK: false, LatencyMs: latency, Error: fmt.Sprintf("unauthorized: HTTP %d (API Key 无效?)", resp.StatusCode)}
	default:
		return &ProbeResult{OK: false, LatencyMs: latency, Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))}
	}
}

type probeTarget struct {
	url     string
	headers map[string]string
}

func (c *Client) buildRequest() (*probeTarget, error) {
	base := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if base == "" {
		base = DefaultEndpoints[c.Provider]
	}
	if base == "" {
		return nil, fmt.Errorf("endpoint 未配置 (provider=%s)", c.Provider)
	}

	switch c.Provider {
	case "openai", "custom":
		return &probeTarget{
			url:     base + "/models",
			headers: map[string]string{"Authorization": "Bearer " + c.APIKey},
		}, nil
	case "anthropic":
		return &probeTarget{
			url: base + "/v1/models",
			headers: map[string]string{
				"x-api-key":         c.APIKey,
				"anthropic-version": "2023-06-01",
			},
		}, nil
	case "google":
		u, err := url.Parse(base + "/v1beta/models")
		if err != nil {
			return nil, err
		}
		u.RawQuery = "key=" + url.QueryEscape(c.APIKey)
		return &probeTarget{url: u.String(), headers: map[string]string{}}, nil
	case "azure":
		u, err := url.Parse(base + "/openai/models")
		if err != nil {
			return nil, err
		}
		u.RawQuery = "api-version=2024-06-01"
		return &probeTarget{
			url:     u.String(),
			headers: map[string]string{"api-key": c.APIKey},
		}, nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", c.Provider)
	}
}

// parseModels 尽力从响应体解析模型列表 (兼容 data/models/value 三种结构)
func parseModels(provider string, body []byte) []string {
	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
		Value []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	models := make([]string, 0, 10)
	add := func(id, name string) {
		if id == "" {
			id = name
		}
		if id == "" {
			return
		}
		if provider == "google" && strings.Contains(id, "/") {
			id = id[strings.LastIndex(id, "/")+1:]
		}
		models = append(models, id)
	}
	for _, item := range payload.Data {
		add(item.ID, item.Name)
	}
	for _, item := range payload.Models {
		add("", item.Name)
	}
	for _, item := range payload.Value {
		add(item.ID, item.Name)
	}
	if len(models) > 50 {
		models = models[:50]
	}
	return models
}

// isHTMLResponse 判断响应体是否为 HTML (网关首页/错误页误返回, 而非 JSON)
func isHTMLResponse(body []byte) bool {
	b := bytes.TrimLeft(body, " \t\r\n\ufeff")
	return len(b) > 0 && b[0] == '<'
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// ============ Chat Completion (M2.5) ============

// ChatMessage 对话消息 (OpenAI 兼容)
type ChatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

// ChatToolCall 模型返回的工具调用请求
type ChatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ChatToolDef 提供给模型的工具定义 (OpenAI tools 格式)
type ChatToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string      `json:"name"`
		Description string      `json:"description,omitempty"`
		Parameters  interface{} `json:"parameters,omitempty"`
	} `json:"function"`
}

// GenOptions 生成参数 (取自 Agent 配置/模型模板)
type GenOptions struct {
	Temperature *float64
	MaxTokens   *int
}

// ChatResult 一次对话调用结果
type ChatResult struct {
	Content      string
	Reasoning    string // 模型思考过程 (reasoning_content, 非思考模型为空)
	Model        string
	FinishReason string
	TotalTokens  int
	ToolCalls    []ChatToolCall
}

// Chat 调用 OpenAI 兼容 /chat/completions
//
// Phase 1 仅支持 openai/custom 提供商 (OpenAI 兼容端点); 其他提供商返回不支持错误。
func (c *Client) Chat(ctx context.Context, model string, messages []ChatMessage, tools []ChatToolDef, gen GenOptions) (*ChatResult, error) {
	if c.Provider != "openai" && c.Provider != "custom" {
		return nil, fmt.Errorf("provider %s 的对话接口 Phase 1 暂不支持 (仅 openai/custom)", c.Provider)
	}
	base := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if base == "" {
		base = DefaultEndpoints["openai"]
	}

	payload := map[string]interface{}{
		"model":    model,
		"messages": messages,
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	if gen.Temperature != nil {
		payload["temperature"] = *gen.Temperature
	}
	if gen.MaxTokens != nil {
		payload["max_tokens"] = *gen.MaxTokens
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat request failed: %s", truncate(err.Error(), 300))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("unauthorized: HTTP %d (API Key 无效?)", resp.StatusCode)
	default:
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          string         `json:"content"`
				ReasoningContent string         `json:"reasoning_content"`
				ToolCalls        []ChatToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if isHTMLResponse(respBody) {
		return nil, fmt.Errorf("chat response is HTML instead of JSON: endpoint path may be incorrect (missing /v1?), address: %s", base+"/chat/completions")
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("chat response parse failed: %s", truncate(err.Error(), 200))
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("chat response has no choices")
	}

	result := &ChatResult{
		Content:      parsed.Choices[0].Message.Content,
		Reasoning:    parsed.Choices[0].Message.ReasoningContent,
		Model:        parsed.Model,
		FinishReason: parsed.Choices[0].FinishReason,
		TotalTokens:  parsed.Usage.TotalTokens,
		ToolCalls:    parsed.Choices[0].Message.ToolCalls,
	}
	return result, nil
}

// streamToolCallDelta 流式工具调用增量 (按 index 分片累积)
type streamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ChatStream 流式调用 OpenAI 兼容 /chat/completions (stream: true):
// 逐块解析 SSE, 思考增量 (reasoning_content / reasoning) 经 onReasoning 实时回调,
// 正文与工具调用累积后以与 Chat 相同的 ChatResult 返回;
// onReasoning 为 nil 或非思考模型时不产生思考回调
func (c *Client) ChatStream(ctx context.Context, model string, messages []ChatMessage, tools []ChatToolDef, gen GenOptions, onReasoning func(delta string)) (*ChatResult, error) {
	if c.Provider != "openai" && c.Provider != "custom" {
		return nil, fmt.Errorf("provider %s 的对话接口 Phase 1 暂不支持 (仅 openai/custom)", c.Provider)
	}
	base := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if base == "" {
		base = DefaultEndpoints["openai"]
	}

	payload := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   true,
		"stream_options": map[string]interface{}{
			"include_usage": true, // 末尾块携带 usage, 保证配额/用量统计 (不支持的端点会忽略)
		},
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	if gen.Temperature != nil {
		payload["temperature"] = *gen.Temperature
	}
	if gen.MaxTokens != nil {
		payload["max_tokens"] = *gen.MaxTokens
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat stream request failed: %s", truncate(err.Error(), 300))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	var (
		content      string
		reasoning    string
		finishReason string
		modelOut     string
		totalTokens  int
		toolCalls    []ChatToolCall
		toolPos      = make(map[int]int) // 流式工具调用 index -> toolCalls 下标
	)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Model string `json:"model"`
			Usage struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
			Choices []struct {
				Delta struct {
					Content          string                `json:"content"`
					ReasoningContent string                `json:"reasoning_content"`
					Reasoning        string                `json:"reasoning"`
					ToolCalls        []streamToolCallDelta `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 非 JSON 块 (注释/网关杂音) 跳过
		}
		if chunk.Model != "" {
			modelOut = chunk.Model
		}
		if chunk.Usage.TotalTokens > 0 {
			totalTokens = chunk.Usage.TotalTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			content += delta.Content
		}
		rd := delta.ReasoningContent
		if rd == "" {
			rd = delta.Reasoning
		}
		if rd != "" {
			reasoning += rd
			if onReasoning != nil {
				onReasoning(rd)
			}
		}
		for _, td := range delta.ToolCalls {
			if pos, ok := toolPos[td.Index]; ok {
				if td.ID != "" {
					toolCalls[pos].ID = td.ID
				}
				if td.Type != "" {
					toolCalls[pos].Type = td.Type
				}
				toolCalls[pos].Function.Name += td.Function.Name
				toolCalls[pos].Function.Arguments += td.Function.Arguments
			} else {
				toolPos[td.Index] = len(toolCalls)
				toolCalls = append(toolCalls, ChatToolCall{ID: td.ID, Type: td.Type})
				toolCalls[len(toolCalls)-1].Function.Name = td.Function.Name
				toolCalls[len(toolCalls)-1].Function.Arguments = td.Function.Arguments
			}
		}
		if chunk.Choices[0].FinishReason != "" {
			finishReason = chunk.Choices[0].FinishReason
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("chat stream read failed: %s", truncate(err.Error(), 300))
	}
	if content == "" && len(toolCalls) == 0 && finishReason == "" {
		return nil, fmt.Errorf("chat stream returned no content")
	}

	return &ChatResult{
		Content:      content,
		Reasoning:    reasoning,
		Model:        modelOut,
		FinishReason: finishReason,
		TotalTokens:  totalTokens,
		ToolCalls:    toolCalls,
	}, nil
}

// EmbedResult embedding 调用结果 (M10.3 语义检索)
type EmbedResult struct {
	Vectors     [][]float64 // 与 input 顺序一致
	Model       string
	TotalTokens int
}

// Embed 调用 OpenAI 兼容 /embeddings (M10.3 语义检索)
//
// 仅支持 openai/custom 提供商 (OpenAI 兼容端点); input 为文本批次, 向量按 index 重排后与 input 顺序一致。
func (c *Client) Embed(ctx context.Context, model string, inputs []string) (*EmbedResult, error) {
	if c.Provider != "openai" && c.Provider != "custom" {
		return nil, fmt.Errorf("provider %s 的 embedding 接口暂不支持 (仅 openai/custom)", c.Provider)
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("embedding input is empty")
	}
	base := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if base == "" {
		base = DefaultEndpoints["openai"]
	}

	payload := map[string]interface{}{
		"model": model,
		"input": inputs,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request failed: %s", truncate(err.Error(), 300))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("unauthorized: HTTP %d (API Key 无效?)", resp.StatusCode)
	default:
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	if isHTMLResponse(respBody) {
		return nil, fmt.Errorf("embed response is HTML instead of JSON: endpoint path may be incorrect (missing /v1?), address: %s", base+"/embeddings")
	}
	var parsed struct {
		Model string `json:"model"`
		Data  []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("embed response parse failed: %s", truncate(err.Error(), 200))
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("embed response has no data")
	}

	type indexed struct {
		i int
		v []float64
	}
	list := make([]indexed, 0, len(parsed.Data))
	for i := range parsed.Data {
		d := &parsed.Data[i]
		pos := d.Index
		if pos < 0 || pos >= len(inputs) {
			pos = i
		}
		list = append(list, indexed{i: pos, v: d.Embedding})
	}
	sort.Slice(list, func(a, b int) bool { return list[a].i < list[b].i })

	vectors := make([][]float64, len(list))
	for i := range list {
		if len(list[i].v) == 0 {
			return nil, fmt.Errorf("embed response contains an empty vector (index=%d)", list[i].i)
		}
		vectors[i] = list[i].v
	}
	return &EmbedResult{
		Vectors:     vectors,
		Model:       parsed.Model,
		TotalTokens: parsed.Usage.TotalTokens,
	}, nil
}
