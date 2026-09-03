package modelclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbe_HTMLResponseFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><body>New API</body></html>"))
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "test-key", 5*time.Second)
	res := c.Probe(context.Background())
	if res.OK {
		t.Fatalf("expected probe to fail for HTML response, got OK")
	}
	if !strings.Contains(res.Error, "HTML") {
		t.Fatalf("expected error mentioning HTML, got: %s", res.Error)
	}
}

func TestProbe_JSONResponseOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1","object":"model"}]}`))
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "test-key", 5*time.Second)
	res := c.Probe(context.Background())
	if !res.OK {
		t.Fatalf("expected probe OK, got error: %s", res.Error)
	}
	if len(res.Models) != 1 || res.Models[0] != "m1" {
		t.Fatalf("expected model m1, got %v", res.Models)
	}
}

func TestChat_HTMLResponseFailsClearly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>spa</body></html>"))
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "test-key", 5*time.Second)
	_, err := c.Chat(context.Background(), "m1", []ChatMessage{{Role: "user", Content: "hi"}}, nil, GenOptions{})
	if err == nil {
		t.Fatal("expected error for HTML chat response")
	}
	if !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("expected clear HTML error, got: %s", err.Error())
	}
}

func TestChat_JSONResponseOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m1","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"total_tokens":5}}`))
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "test-key", 5*time.Second)
	res, err := c.Chat(context.Background(), "m1", []ChatMessage{{Role: "user", Content: "hi"}}, nil, GenOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if res.Content != "hello" {
		t.Fatalf("unexpected content: %s", res.Content)
	}
}

func TestChatStream_SSEParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		if req["stream"] != true {
			t.Errorf("expected stream=true in request body")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`data: {"model":"m1","choices":[{"delta":{"reasoning_content":"thinking "}}]}`,
			`data: {"choices":[{"delta":{"reasoning_content":"hard"}}]}`,
			`data: {"choices":[{"delta":{"content":"hel"}}]}`,
			`data: {"choices":[{"delta":{"content":"lo","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"get_","arguments":""}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"weather","arguments":"{\"city\":\"bj\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"total_tokens":42}}`,
			`data: [DONE]`,
		}
		for _, line := range lines {
			_, _ = w.Write([]byte(line + "\n\n"))
		}
	}))
	defer srv.Close()

	var reasoning []string
	c := New("custom", srv.URL, "test-key", 5*time.Second)
	res, err := c.ChatStream(context.Background(), "m1", []ChatMessage{{Role: "user", Content: "hi"}}, nil, GenOptions{}, func(d string) {
		reasoning = append(reasoning, d)
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if res.Content != "hello" {
		t.Fatalf("unexpected content: %s", res.Content)
	}
	if res.Reasoning != "thinking hard" {
		t.Fatalf("unexpected reasoning: %s", res.Reasoning)
	}
	if len(reasoning) != 2 || reasoning[0] != "thinking " || reasoning[1] != "hard" {
		t.Fatalf("unexpected reasoning callbacks: %v", reasoning)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.ID != "call-1" || tc.Function.Name != "get_weather" {
		t.Fatalf("unexpected tool call: %s %s", tc.ID, tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"bj"}` {
		t.Fatalf("unexpected tool args: %s", tc.Function.Arguments)
	}
	if res.TotalTokens != 42 || res.FinishReason != "tool_calls" || res.Model != "m1" {
		t.Fatalf("unexpected meta: tokens=%d finish=%s model=%s", res.TotalTokens, res.FinishReason, res.Model)
	}
}

// TestEmbed_JSONResponseOK 正常路径: 向量按 index 重排, usage 解析
func TestEmbed_JSONResponseOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path = %s, want /embeddings", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k1" {
			t.Fatalf("auth = %s, want Bearer k1", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"m1","data":[{"index":1,"embedding":[0.2,0.4]},{"index":0,"embedding":[0.1,0.3]}],"usage":{"total_tokens":9}}`)
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "k1", 5*time.Second)
	res, err := c.Embed(context.Background(), "m1", []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(res.Vectors) != 2 || res.Vectors[0][0] != 0.1 || res.Vectors[1][1] != 0.4 {
		t.Fatalf("vectors = %v, want [[0.1 0.3] [0.2 0.4]]", res.Vectors)
	}
	if res.TotalTokens != 9 || res.Model != "m1" {
		t.Fatalf("meta = %+v, want model=m1 tokens=9", res)
	}
}

// TestEmbed_Unauthorized 401 返回错误
func TestEmbed_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"bad key"}`)
	}))
	defer srv.Close()

	c := New("custom", srv.URL, "bad", 5*time.Second)
	if _, err := c.Embed(context.Background(), "m1", []string{"a"}); err == nil {
		t.Fatalf("Embed: want error on 401")
	}
}

// TestEmbed_UnsupportedProvider 非 openai/custom 提供商返回错误
func TestEmbed_UnsupportedProvider(t *testing.T) {
	c := New("anthropic", "http://127.0.0.1:1", "k1", 5*time.Second)
	if _, err := c.Embed(context.Background(), "m1", []string{"a"}); err == nil {
		t.Fatalf("Embed: want error for unsupported provider")
	}
}

// TestEmbed_EmptyInput 空输入返回错误 (不发起请求)
func TestEmbed_EmptyInput(t *testing.T) {
	c := New("custom", "http://127.0.0.1:1", "k1", 5*time.Second)
	if _, err := c.Embed(context.Background(), "m1", nil); err == nil {
		t.Fatalf("Embed: want error for empty input")
	}
}
