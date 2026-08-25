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

	log.Printf("mock model server listening on :%s (api key required: %v)", port, apiKey != "")
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("failed to start model server: %v", err)
	}
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
