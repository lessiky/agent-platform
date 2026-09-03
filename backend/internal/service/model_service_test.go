package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-platform/internal/crypto"
	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"
)

// testAesKey 64 位 hex 测试密钥 (AES-256)
const testAesKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeTemplateRepo 模板仓储最小假实现 (仅 Get; 其余方法不应被 SayHi 调用)
type fakeTemplateRepo struct {
	repository.ModelTemplateRepository
	byID   map[string]*model.ModelTemplate
	byName map[string]*model.ModelTemplate
}

func (f *fakeTemplateRepo) Get(ctx context.Context, id string) (*model.ModelTemplate, error) {
	t, ok := f.byID[id]
	if !ok {
		return nil, errors.ErrNotFound
	}
	return t, nil
}

func (f *fakeTemplateRepo) GetByName(ctx context.Context, name string) (*model.ModelTemplate, error) {
	t, ok := f.byName[name]
	if !ok {
		return nil, nil
	}
	return t, nil
}

func (f *fakeTemplateRepo) ListForRoute(ctx context.Context) ([]model.ModelTemplate, error) {
	var out []model.ModelTemplate
	for _, t := range f.byID {
		out = append(out, *t)
	}
	return out, nil
}

// fakeQuotaRepo 配额仓储最小假实现 (M10.3 向量路径: 无配额 = 不限)
type fakeQuotaRepo struct {
	repository.ModelQuotaRepository
}

func (f *fakeQuotaRepo) GetByModel(ctx context.Context, modelID string) (*model.ModelQuota, error) {
	return nil, nil
}

// fakeUsageRepo 用量日志假实现 (M10.3 向量路径: 记录条目供断言)
type fakeUsageRepo struct {
	repository.ModelUsageLogRepository
	entries []*model.ModelUsageLog
}

func (f *fakeUsageRepo) Append(ctx context.Context, entry *model.ModelUsageLog) error {
	f.entries = append(f.entries, entry)
	return nil
}

func (f *fakeUsageRepo) Trim(ctx context.Context, modelID string, keep int) error { return nil }

func newTestModelService(t *testing.T, templates map[string]*model.ModelTemplate) *modelTemplateService {
	t.Helper()
	cipher, err := crypto.NewAesGCM(testAesKey)
	if err != nil {
		t.Fatalf("NewAesGCM: %v", err)
	}
	return NewModelTemplateService(&fakeTemplateRepo{byID: templates}, nil, nil, nil, nil, cipher, 0, 0, 0, nil).(*modelTemplateService)
}

func newTestTemplate(t *testing.T, cipher *crypto.AesGCM, provider, modelName string) *model.ModelTemplate {
	t.Helper()
	enc, err := cipher.Encrypt([]byte("sk-test"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return &model.ModelTemplate{
		ID:       "m-1",
		Name:     "test",
		Provider: provider,
		Model:    modelName,
		Status:   model.ModelStatusActive,
		APIKey:   enc,
	}
}

// TestSayHi_OK 正常路径: 真实对话调用成功, 返回模型回复内容
func TestSayHi_OK(t *testing.T) {
	var gotModel string
	var gotMessages []modelclient.ChatMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		var payload struct {
			Model    string                    `json:"model"`
			Messages []modelclient.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotModel = payload.Model
		gotMessages = payload.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o-mini","choices":[{"message":{"role":"assistant","content":"Hello! How can I help you today?"},"finish_reason":"stop"}],"usage":{"total_tokens":42}}`))
	}))
	defer srv.Close()

	cipher, err := crypto.NewAesGCM(testAesKey)
	if err != nil {
		t.Fatalf("NewAesGCM: %v", err)
	}
	tpl := newTestTemplate(t, cipher, "openai", "gpt-4o-mini")
	tpl.Endpoint = srv.URL + "/v1"
	s := newTestModelService(t, map[string]*model.ModelTemplate{"m-1": tpl})

	view, err := s.SayHi(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("SayHi: %v", err)
	}
	if !view.OK {
		t.Fatalf("ok = false: %s", view.Error)
	}
	if gotModel != "gpt-4o-mini" {
		t.Errorf("request model = %s, want gpt-4o-mini", gotModel)
	}
	if len(gotMessages) != 1 || gotMessages[0].Role != "user" || gotMessages[0].Content != "Hi" {
		t.Errorf("messages = %+v, want single user message 'Hi'", gotMessages)
	}
	if view.Content == "" {
		t.Errorf("content is empty")
	}
	if view.TotalTokens != 42 {
		t.Errorf("total_tokens = %d, want 42", view.TotalTokens)
	}
	if view.FinishReason != "stop" {
		t.Errorf("finish_reason = %s, want stop", view.FinishReason)
	}
	if view.LatencyMs < 0 {
		t.Errorf("latency_ms = %d, want >= 0", view.LatencyMs)
	}
}

// TestSayHi_Inactive 手动停用的模板直接返回失败, 不发起调用
func TestSayHi_Inactive(t *testing.T) {
	cipher, err := crypto.NewAesGCM(testAesKey)
	if err != nil {
		t.Fatalf("NewAesGCM: %v", err)
	}
	tpl := newTestTemplate(t, cipher, "openai", "gpt-4o-mini")
	tpl.Status = model.ModelStatusInactive
	s := newTestModelService(t, map[string]*model.ModelTemplate{"m-1": tpl})

	view, err := s.SayHi(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("SayHi: %v", err)
	}
	if view.OK {
		t.Fatalf("ok = true, want false for inactive template")
	}
	if !strings.Contains(view.Error, "停用") {
		t.Errorf("error = %s, want 包含 '停用'", view.Error)
	}
}

// TestSayHi_UnsupportedProvider 非 openai/custom 提供商返回明确提示
func TestSayHi_UnsupportedProvider(t *testing.T) {
	cipher, err := crypto.NewAesGCM(testAesKey)
	if err != nil {
		t.Fatalf("NewAesGCM: %v", err)
	}
	tpl := newTestTemplate(t, cipher, "anthropic", "claude-3-5-sonnet")
	s := newTestModelService(t, map[string]*model.ModelTemplate{"m-1": tpl})

	view, err := s.SayHi(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("SayHi: %v", err)
	}
	if view.OK {
		t.Fatalf("ok = true, want false for unsupported provider")
	}
	if !strings.Contains(view.Error, "暂不支持") {
		t.Errorf("error = %s, want 包含 '暂不支持'", view.Error)
	}
}

// TestSayHi_UpstreamError 上游返回 500 时 ok=false 且携带错误信息
func TestSayHi_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	cipher, err := crypto.NewAesGCM(testAesKey)
	if err != nil {
		t.Fatalf("NewAesGCM: %v", err)
	}
	tpl := newTestTemplate(t, cipher, "openai", "gpt-4o-mini")
	tpl.Endpoint = srv.URL + "/v1"
	s := newTestModelService(t, map[string]*model.ModelTemplate{"m-1": tpl})

	view, err := s.SayHi(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("SayHi: %v", err)
	}
	if view.OK {
		t.Fatalf("ok = true, want false on upstream 500")
	}
	if !strings.Contains(view.Error, "500") {
		t.Errorf("error = %s, want 包含 '500'", view.Error)
	}
}

// TestSayHi_NotFound 模板不存在返回错误
func TestSayHi_NotFound(t *testing.T) {
	s := newTestModelService(t, map[string]*model.ModelTemplate{})
	if _, err := s.SayHi(context.Background(), "missing"); err == nil {
		t.Fatalf("SayHi: want error for missing template")
	}
}

// TestSayHi_Embed_OK 向量专用模板: 走 /embeddings 而非 /chat/completions, 返回向量摘要
func TestSayHi_Embed_OK(t *testing.T) {
	var gotPath, gotModel string
	var gotInput []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var payload struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotModel = payload.Model
		gotInput = payload.Input
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"bge-m3","data":[{"index":0,"embedding":[0.1,-0.25,0.5,0.75]}],"usage":{"total_tokens":3}}`)
	}))
	defer srv.Close()

	cipher, _ := crypto.NewAesGCM(testAesKey)
	tpl := newTestTemplate(t, cipher, "openai", "bge-m3")
	tpl.Name = "dhzq-bge-m3"
	tpl.Endpoint = srv.URL + "/v1"
	s, _ := newEmbedTestService(t, tpl, "dhzq-bge-m3")

	view, err := s.SayHi(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("SayHi: %v", err)
	}
	if !view.OK {
		t.Fatalf("ok = false: %s", view.Error)
	}
	if gotPath != "/v1/embeddings" {
		t.Errorf("path = %s, want /v1/embeddings", gotPath)
	}
	if gotModel != "bge-m3" {
		t.Errorf("request model = %s, want bge-m3", gotModel)
	}
	if len(gotInput) != 1 || gotInput[0] != "Hi" {
		t.Errorf("input = %+v, want [Hi]", gotInput)
	}
	if !strings.Contains(view.Content, "4 维") {
		t.Errorf("content = %s, want 包含向量维度 4", view.Content)
	}
	if view.Model != "bge-m3" {
		t.Errorf("model = %s, want bge-m3", view.Model)
	}
	if view.TotalTokens != 3 {
		t.Errorf("total_tokens = %d, want 3", view.TotalTokens)
	}
}

// TestSayHi_Embed_UpstreamError 向量模板 /embeddings 返回 404 时 ok=false 且携带错误信息
func TestSayHi_Embed_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"Not Found"}}`))
	}))
	defer srv.Close()

	cipher, _ := crypto.NewAesGCM(testAesKey)
	tpl := newTestTemplate(t, cipher, "openai", "bge-m3")
	tpl.Name = "dhzq-bge-m3"
	tpl.Endpoint = srv.URL + "/v1"
	s, _ := newEmbedTestService(t, tpl, "dhzq-bge-m3")

	view, err := s.SayHi(context.Background(), "m-1")
	if err != nil {
		t.Fatalf("SayHi: %v", err)
	}
	if view.OK {
		t.Fatalf("ok = true, want false on upstream 404")
	}
	if !strings.Contains(view.Error, "404") {
		t.Errorf("error = %s, want 包含 '404'", view.Error)
	}
}

// newEmbedTestService 构造带配额/用量假仓储 + embedding 模板名的模型服务 (M10.3)
func newEmbedTestService(t *testing.T, tpl *model.ModelTemplate, embedName string) (*modelTemplateService, *fakeUsageRepo) {
	t.Helper()
	cipher, err := crypto.NewAesGCM(testAesKey)
	if err != nil {
		t.Fatalf("NewAesGCM: %v", err)
	}
	byID := map[string]*model.ModelTemplate{tpl.ID: tpl}
	byName := map[string]*model.ModelTemplate{}
	if embedName != "" {
		byName[embedName] = tpl
	}
	usage := &fakeUsageRepo{}
	s := NewModelTemplateService(
		&fakeTemplateRepo{byID: byID, byName: byName},
		&fakeQuotaRepo{},
		usage,
		nil, nil, cipher, 0, 0, 0, StaticTemplateSource(embedName),
	).(*modelTemplateService)
	return s, usage
}

// TestEmbedForMemory_OK 正常路径: 向量返回 (按 index 重排) + 用量日志计次 + 路由排除 embedding 模板
func TestEmbedForMemory_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"mock-embed","data":[{"index":1,"embedding":[0.3,0.4]},{"index":0,"embedding":[0.1,0.2]}],"usage":{"total_tokens":7}}`)
	}))
	defer srv.Close()

	cipher, _ := crypto.NewAesGCM(testAesKey)
	tpl := newTestTemplate(t, cipher, "custom", "mock-embed")
	tpl.Name = "embed-tpl"
	tpl.Endpoint = srv.URL + "/v1"
	s, usage := newEmbedTestService(t, tpl, "embed-tpl")

	vecs, err := s.EmbedForMemory(context.Background(), "embed-tpl", []string{"hello", "world"})
	if err != nil {
		t.Fatalf("EmbedForMemory: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 2 || vecs[0][0] != 0.1 || vecs[1][1] != 0.4 {
		t.Fatalf("vecs = %v, want [[0.1 0.2] [0.3 0.4]]", vecs)
	}
	if len(usage.entries) != 1 || !usage.entries[0].OK || usage.entries[0].Tokens != 7 || usage.entries[0].AgentID != nil {
		t.Fatalf("usage = %+v, want 1 ok entry tokens=7 agent=nil", usage.entries)
	}
	// embedding 模板不参与对话路由
	if !s.isEmbedTemplate(tpl) {
		t.Fatalf("isEmbedTemplate = false, want true")
	}
	if got := s.orderedCandidates(context.Background(), ""); len(got) != 0 {
		t.Fatalf("orderedCandidates = %d items, want 0 (embedding template excluded)", len(got))
	}
}

// TestEmbedForMemory_NotConfigured 未配置模板名返回错误
func TestEmbedForMemory_NotConfigured(t *testing.T) {
	tpl := newTestTemplate(t, mustCipher(t), "custom", "mock-embed")
	s, _ := newEmbedTestService(t, tpl, "")
	if _, err := s.EmbedForMemory(context.Background(), "", []string{"x"}); err == nil {
		t.Fatalf("want error for empty template name")
	}
}

// TestEmbedForMemory_TemplateMissing 模板不存在返回错误
func TestEmbedForMemory_TemplateMissing(t *testing.T) {
	tpl := newTestTemplate(t, mustCipher(t), "custom", "mock-embed")
	s, _ := newEmbedTestService(t, tpl, "other-name")
	if _, err := s.EmbedForMemory(context.Background(), "embed-tpl", []string{"x"}); err == nil {
		t.Fatalf("want error for missing template")
	}
}

// TestEmbedForMemory_UpstreamError 上游 500: 返回错误且记录失败用量
func TestEmbedForMemory_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	cipher, _ := crypto.NewAesGCM(testAesKey)
	tpl := newTestTemplate(t, cipher, "custom", "mock-embed")
	tpl.Name = "embed-tpl"
	tpl.Endpoint = srv.URL + "/v1"
	s, usage := newEmbedTestService(t, tpl, "embed-tpl")

	if _, err := s.EmbedForMemory(context.Background(), "embed-tpl", []string{"x"}); err == nil {
		t.Fatalf("want error on upstream 500")
	}
	if len(usage.entries) != 1 || usage.entries[0].OK || !strings.Contains(usage.entries[0].Error, "500") {
		t.Fatalf("usage = %+v, want 1 failed entry with 500", usage.entries)
	}
}

func mustCipher(t *testing.T) *crypto.AesGCM {
	t.Helper()
	cipher, err := crypto.NewAesGCM(testAesKey)
	if err != nil {
		t.Fatalf("NewAesGCM: %v", err)
	}
	return cipher
}
