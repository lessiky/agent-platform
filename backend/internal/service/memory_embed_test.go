package service

// memory_embed_test.go — M10.3 向量组件 + 异步写入单测 (httptest 假 /embeddings 端点, 不依赖 DB)

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent-platform/internal/model"
)

// embedTestServer 返回一个假 /embeddings 端点 (固定向量 + 记录请求数)
func embedTestServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		*calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"mock-embed","data":[{"index":0,"embedding":[0.25,0.75]}],"usage":{"total_tokens":3}}`)
	}))
}

// TestMemoryEmbedder_Disabled 未配置模板 -> 不可用, EmbedOne 返回错误
func TestMemoryEmbedder_Disabled(t *testing.T) {
	e := NewMemoryEmbedder(nil, StaticTemplateSource(""), 0)
	if e.Enabled() {
		t.Fatalf("Enabled = true, want false for empty name")
	}
	if _, err := e.EmbedOne(context.Background(), "你好"); err == nil {
		t.Fatalf("EmbedOne: want error when disabled")
	}
}

// TestMemoryEmbedder_EmptyText 空文本返回错误 (不发起调用)
func TestMemoryEmbedder_EmptyText(t *testing.T) {
	calls := 0
	srv := embedTestServer(t, &calls)
	defer srv.Close()
	tpl := newTestTemplate(t, mustCipher(t), "custom", "mock-embed")
	tpl.Name = "embed-tpl"
	tpl.Endpoint = srv.URL + "/v1"
	s, _ := newEmbedTestService(t, tpl, "embed-tpl")

	e := NewMemoryEmbedder(s, StaticTemplateSource("embed-tpl"), 0)
	if _, err := e.EmbedOne(context.Background(), "  "); err == nil {
		t.Fatalf("EmbedOne: want error for blank text")
	}
	if calls != 0 {
		t.Fatalf("upstream calls = %d, want 0 for blank text", calls)
	}
}

// TestMemoryEmbedder_Success 正常路径: 返回向量
func TestMemoryEmbedder_Success(t *testing.T) {
	calls := 0
	srv := embedTestServer(t, &calls)
	defer srv.Close()
	tpl := newTestTemplate(t, mustCipher(t), "custom", "mock-embed")
	tpl.Name = "embed-tpl"
	tpl.Endpoint = srv.URL + "/v1"
	s, _ := newEmbedTestService(t, tpl, "embed-tpl")

	e := NewMemoryEmbedder(s, StaticTemplateSource("embed-tpl"), 0)
	if !e.Enabled() {
		t.Fatalf("Enabled = false, want true")
	}
	vec, err := e.EmbedOne(context.Background(), "你好")
	if err != nil {
		t.Fatalf("EmbedOne: %v", err)
	}
	if len(vec) != 2 || vec[0] != 0.25 || vec[1] != 0.75 {
		t.Fatalf("vec = %v, want [0.25 0.75]", vec)
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
}

// TestEmbedAsync_WritesVector 异步写入: 向量回写到仓储 + 缓存失效
func TestEmbedAsync_WritesVector(t *testing.T) {
	calls := 0
	srv := embedTestServer(t, &calls)
	defer srv.Close()
	tpl := newTestTemplate(t, mustCipher(t), "custom", "mock-embed")
	tpl.Name = "embed-tpl"
	tpl.Endpoint = srv.URL + "/v1"
	s, _ := newEmbedTestService(t, tpl, "embed-tpl")
	embedder := NewMemoryEmbedder(s, StaticTemplateSource("embed-tpl"), time.Second)

	repo := newFakeMemRepo()
	mem := repo.add("agent-1", "", model.MemoryKindFact, "异步向量写入测试")
	svc := NewMemoryService(repo, &fakeAuditRepo{}, true, 10, 800, 500*time.Millisecond, time.Hour, embedder).(*memoryService)

	svc.EmbedAsync("agent-1", []model.Memory{*mem})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := repo.Get(context.Background(), "agent-1", mem.ID)
		if err == nil && len(got.Embedding) > 0 {
			if vec := parseMemoryVector(got.Embedding); len(vec) != 2 || vec[0] != 0.25 || vec[1] != 0.75 {
				t.Fatalf("stored vec = %v, want [0.25 0.75]", vec)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("embedding not written within 3s")
}

// TestEmbedAsync_FailureKeepsEmpty 上游失败: 向量留空 (降级, 不阻塞)
func TestEmbedAsync_FailureKeepsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	tpl := newTestTemplate(t, mustCipher(t), "custom", "mock-embed")
	tpl.Name = "embed-tpl"
	tpl.Endpoint = srv.URL + "/v1"
	s, _ := newEmbedTestService(t, tpl, "embed-tpl")
	embedder := NewMemoryEmbedder(s, StaticTemplateSource("embed-tpl"), 200*time.Millisecond)

	repo := newFakeMemRepo()
	mem := repo.add("agent-1", "", model.MemoryKindFact, "失败降级测试")
	svc := NewMemoryService(repo, &fakeAuditRepo{}, true, 10, 800, 500*time.Millisecond, time.Hour, embedder).(*memoryService)

	svc.EmbedAsync("agent-1", []model.Memory{*mem})

	// 等待异步 goroutine 结束 (失败路径耗时很短), 断言向量仍为空
	time.Sleep(600 * time.Millisecond)
	got, err := repo.Get(context.Background(), "agent-1", mem.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Embedding) != 0 {
		t.Fatalf("embedding = %v, want empty on failure", got.Embedding)
	}
}

// TestEmbedAsync_Disabled 未配置 embedder: 空操作 (不 panic)
func TestEmbedAsync_Disabled(t *testing.T) {
	repo := newFakeMemRepo()
	mem := repo.add("agent-1", "", model.MemoryKindFact, "空操作测试")
	svc := NewMemoryService(repo, &fakeAuditRepo{}, true, 10, 800, 500*time.Millisecond, time.Hour, nil).(*memoryService)
	svc.EmbedAsync("agent-1", []model.Memory{*mem}) // 不应 panic
	svc.EmbedAsync("agent-1", nil)                  // 空切片也不应 panic
}

// TestMemoryEmbedder_DynamicSwitch 运行时切换 (平台设置): 覆盖值 / 清空回退 env / 双空禁用 / 无效名降级
func TestMemoryEmbedder_DynamicSwitch(t *testing.T) {
	calls := 0
	srv := embedTestServer(t, &calls)
	defer srv.Close()
	tpl := newTestTemplate(t, mustCipher(t), "custom", "mock-embed")
	tpl.Name = "embed-tpl"
	tpl.Endpoint = srv.URL + "/v1"
	s, _ := newEmbedTestService(t, tpl, "embed-tpl")

	src := NewMutableTemplateSource("embed-tpl") // env 兜底
	e := NewMemoryEmbedder(s, src, 0)

	// 平台设置未配置: 跟随 env, 可用
	if !e.Enabled() {
		t.Fatalf("Enabled = false, want true via env fallback")
	}
	// 平台设置显式启用: 正常计算向量
	src.Set("embed-tpl")
	if vec, err := e.EmbedOne(context.Background(), "你好"); err != nil || len(vec) != 2 {
		t.Fatalf("EmbedOne with override: %v", err)
	}
	// 平台设置清空: 回退 env, 仍可用
	src.Set("")
	if !e.Enabled() {
		t.Fatalf("Enabled = false after clearing override, want env fallback")
	}

	// 平台设置与 env 均空: 禁用
	e2 := NewMemoryEmbedder(s, NewMutableTemplateSource(""), 0)
	if e2.Enabled() {
		t.Fatalf("Enabled = true, want false when both empty")
	}
	if _, err := e2.EmbedOne(context.Background(), "x"); err == nil {
		t.Fatalf("EmbedOne: want error when disabled")
	}

	// 切换到不存在的模板: Enabled 仍为 true (名称非空), EmbedOne 返回错误由调用方降级
	src2 := NewMutableTemplateSource("")
	e3 := NewMemoryEmbedder(s, src2, 0)
	src2.Set("no-such-tpl")
	if !e3.Enabled() {
		t.Fatalf("Enabled = false, want true for non-empty name")
	}
	if _, err := e3.EmbedOne(context.Background(), "x"); err == nil {
		t.Fatalf("EmbedOne: want error for unknown template")
	}
}
