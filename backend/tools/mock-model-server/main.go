// mock-model-server 本地 OpenAI 兼容 mock 模型服务器 (M4 开发/测试用)
//
// 用法:
//
//	go run ./tools/mock-model-server
//
// 环境变量:
//
//	MOCK_MODEL_PORT    监听端口 (默认 9101)
//	MOCK_MODEL_API_KEY 如设置, 要求请求携带 Authorization: Bearer <key>
//
// 端点:
//
//	GET  /v1/models              模型列表 (连通性探测入口)
//	POST /v1/chat/completions    固定应答 (验证调用链)
//
// 对话行为 (M2.5):
//
//   - 请求含 role=tool 消息            -> 返回确认文本 (工具轮结束)
//   - 最后一条用户消息含 CALL_TOOL:<name> 或 CALL_TOOL:<name>:<arg> -> 返回 tool_calls 请求调用 <name> (load_skill 时 arg 作为 skill_name)
//   - 其他                             -> 固定应答
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	port := getEnv("MOCK_MODEL_PORT", "9101")
	apiKey := os.Getenv("MOCK_MODEL_API_KEY")

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, apiKey) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": map[string]interface{}{"message": "Invalid API key", "type": "invalid_request_error"},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{"id": "mock-gpt-mini", "object": "model", "owned_by": "mock"},
				{"id": "mock-gpt-mini-2024-01", "object": "model", "owned_by": "mock"},
			},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, apiKey) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": map[string]interface{}{"message": "Invalid API key", "type": "invalid_request_error"},
			})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)

		// 工具轮: 已收到工具结果 -> 确认文本
		hasToolMsg := false
		// 最后一条用户消息是否请求工具调用
		callTool := ""
		callToolArg := ""
		lastUserMsg := ""
		// M10.2 记忆管线: system 提示词含 "记忆抽取器" / "对话摘要器" -> 固定抽取/摘要应答
		isExtractPrompt, isSummaryPrompt := false, false
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "记忆抽取器") {
				isExtractPrompt = true
			}
			if strings.Contains(m.Content, "对话摘要器") {
				isSummaryPrompt = true
			}
		}
		for _, m := range req.Messages {
			if m.Role == "tool" {
				hasToolMsg = true
			}
			if m.Role == "user" {
				lastUserMsg = m.Content
				if i := strings.Index(m.Content, "CALL_TOOL:"); i >= 0 {
					spec := strings.TrimSpace(m.Content[i+len("CALL_TOOL:"):])
					if j := strings.Index(spec, ":"); j >= 0 {
						callTool = strings.TrimSpace(spec[:j])
						callToolArg = strings.TrimSpace(spec[j+1:])
					} else {
						callTool = spec
					}
				}
			}
		}
		// M10.2 自动抽取: 返回固定 JSON (请求体含 MEMORY_CORRUPT 时返回乱码, 验证故障隔离)
		if isExtractPrompt {
			content := `[{"content":"用户是后端工程师, 主力语言 Go","kind":"fact","reason":"自我介绍"},{"content":"用户偏好简洁直接的回答","kind":"preference","reason":"沟通偏好"}]`
			if strings.Contains(string(body), "MEMORY_CORRUPT") {
				content = "garbage output, definitely not json"
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id": "mock-mem-1", "object": "chat.completion", "model": "mock-gpt-mini",
				"choices": []map[string]interface{}{
					{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": content}, "finish_reason": "stop"},
				},
				"usage": map[string]interface{}{"prompt_tokens": 60, "completion_tokens": 20, "total_tokens": 80},
			})
			return
		}
		// M10.2 滚动摘要: 返回固定摘要
		if isSummaryPrompt {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id": "mock-mem-2", "object": "chat.completion", "model": "mock-gpt-mini",
				"choices": []map[string]interface{}{
					{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "摘要: 用户咨询了部署方案, 决定采用 docker compose 部署, 并确认数据库为 PostgreSQL。"}, "finish_reason": "stop"},
				},
				"usage": map[string]interface{}{"prompt_tokens": 100, "completion_tokens": 30, "total_tokens": 130},
			})
			return
		}
		// M10.2 摘要注入验证: 请求中任一 user 消息以摘要前缀开头 -> 标记应答
		hasSummaryMsg := false
		for _, m := range req.Messages {
			if m.Role == "user" && strings.HasPrefix(m.Content, "以下是更早对话的摘要：") {
				hasSummaryMsg = true
			}
		}
		if hasSummaryMsg {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id": "mock-mem-3", "object": "chat.completion", "model": "mock-gpt-mini",
				"choices": []map[string]interface{}{
					{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "mock: 已读取更早对话摘要"}, "finish_reason": "stop"},
				},
				"usage": map[string]interface{}{"prompt_tokens": 15, "completion_tokens": 6, "total_tokens": 21},
			})
			return
		}
		if hasToolMsg {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id": "mock-2", "object": "chat.completion", "model": "mock-gpt-mini",
				"choices": []map[string]interface{}{
					{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "mock-gpt-mini: tool result received, task complete."}, "finish_reason": "stop"},
				},
				"usage": map[string]interface{}{"prompt_tokens": 20, "completion_tokens": 12, "total_tokens": 32},
			})
			return
		}
		if callTool != "" {
			var argsPayload interface{}
			if callTool == "load_skill" && callToolArg != "" {
				argsPayload = map[string]string{"skill_name": callToolArg}
			} else {
				argsPayload = map[string]string{"message": "hello-from-model"}
			}
			args, _ := json.Marshal(argsPayload)
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id": "mock-3", "object": "chat.completion", "model": "mock-gpt-mini",
				"choices": []map[string]interface{}{
					{"index": 0, "finish_reason": "tool_calls", "message": map[string]interface{}{
						"role": "assistant", "content": "",
						"tool_calls": []map[string]interface{}{
							{"id": "call_1", "type": "function", "function": map[string]interface{}{"name": callTool, "arguments": string(args)}},
						},
					}},
				},
				"usage": map[string]interface{}{"prompt_tokens": 12, "completion_tokens": 4, "total_tokens": 16},
			})
			return
		}
		// AI 生成工作流测试: 最后一条用户消息含 GEN_WORKFLOW -> 返回带代码块围栏的固定工作流 JSON
		if strings.Contains(lastUserMsg, "GEN_WORKFLOW") {
			content := "已根据描述生成工作流:\n```json\n" + genWorkflowJSON + "\n```"
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"id": "mock-4", "object": "chat.completion", "model": "mock-gpt-mini",
				"choices": []map[string]interface{}{
					{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": content}, "finish_reason": "stop"},
				},
				"usage": map[string]interface{}{"prompt_tokens": 100, "completion_tokens": 80, "total_tokens": 180},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id": "mock-1", "object": "chat.completion", "model": "mock-gpt-mini",
			"choices": []map[string]interface{}{
				{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "This is a mocked response from mock-model-server."}, "finish_reason": "stop"},
			},
			"usage": map[string]interface{}{"prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18},
		})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, apiKey) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"error": map[string]interface{}{"message": "Invalid API key", "type": "invalid_request_error"},
			})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Input) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "input required"})
			return
		}
		modelName := req.Model
		if modelName == "" {
			modelName = "mock-embed"
		}
		data := make([]map[string]interface{}, 0, len(req.Input))
		totalTokens := 0
		for i, text := range req.Input {
			totalTokens += mockEmbedTokens(text)
			data = append(data, map[string]interface{}{"object": "embedding", "index": i, "embedding": mockEmbedding(text)})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"object": "list",
			"data":   data,
			"model":  modelName,
			"usage":  map[string]interface{}{"prompt_tokens": totalTokens, "total_tokens": totalTokens},
		})
	})

	log.Printf("mock model server listening on :%s (api key required: %v)", port, apiKey != "")
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("failed to start model server: %v", err)
	}
}

// mockEmbedTokens 粗略 token 估算 (E2E 用量断言用)
func mockEmbedTokens(text string) int {
	if len([]rune(text)) <= 4 {
		return 1
	}
	return (len([]rune(text)) + 3) / 4
}

// mockEmbedding 确定性伪向量 (M10.3 E2E 用): CJK bigram / ASCII 词 FNV-1a 哈希到 64 维计数后 L2 归一化;
// 两文本共享字符越多余弦越接近 1 (模拟语义相似信号), 完全无关文本接近 0
func mockEmbedding(text string) []float64 {
	const dim = 64
	vec := make([]float64, dim)
	add := func(s string) {
		if s == "" {
			return
		}
		var h uint64 = 2166136261
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= 16777619
		}
		vec[int(h%uint64(dim))]++
	}
	runes := []rune(text)
	for i := 0; i+1 < len(runes); i++ {
		if isCJKRune(runes[i]) && isCJKRune(runes[i+1]) {
			add(string(runes[i : i+2]))
		}
	}
	var word []rune
	flush := func() {
		if len(word) >= 2 {
			add(strings.ToLower(string(word)))
		}
		word = word[:0]
	}
	for _, r := range runes {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			word = append(word, r)
			continue
		}
		flush()
	}
	flush()
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		vec[0] = 1 // 空文本: 固定单位向量
		return vec
	}
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

// isCJKRune 中日韩文字判定 (与后端 bigram 分词范围一致的主要区段)
func isCJKRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) || (r >= 0x3040 && r <= 0x30FF)
}

// genWorkflowJSON AI 生成工作流测试用固定应答 (delay + http 两节点, 无平台资源引用)
const genWorkflowJSON = `{
  "name": "示例数据同步",
  "description": "拉取外部数据后等待处理完成的示例流程 (mock 生成)",
  "input_schema": {"type": "object", "properties": {"source": {"type": "string"}}},
  "definition": {
    "version": 1,
    "nodes": [
      {"id": "n1", "type": "http", "name": "拉取数据", "config": {"method": "GET", "url": "https://example.com/api/data", "headers": {"X-Source": "$inputs.source"}}},
      {"id": "n2", "type": "delay", "name": "等待处理", "config": {"seconds": 2}}
    ],
    "edges": [{"id": "e1", "source": "n1", "target": "n2"}]
  }
}`

func authorized(r *http.Request, apiKey string) bool {
	if apiKey == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	return auth == "Bearer "+apiKey
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		fmt.Fprintln(w, "encode error:", err)
	}
}
