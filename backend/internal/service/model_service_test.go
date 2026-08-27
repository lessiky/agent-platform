package service

import (
	"context"
	"encoding/json"
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
	byID map[string]*model.ModelTemplate
}

func (f *fakeTemplateRepo) Get(ctx context.Context, id string) (*model.ModelTemplate, error) {
	t, ok := f.byID[id]
	if !ok {
		return nil, errors.ErrNotFound
	}
	return t, nil
}

func newTestModelService(t *testing.T, templates map[string]*model.ModelTemplate) *modelTemplateService {
	t.Helper()
	cipher, err := crypto.NewAesGCM(testAesKey)
	if err != nil {
		t.Fatalf("NewAesGCM: %v", err)
	}
	return NewModelTemplateService(&fakeTemplateRepo{byID: templates}, nil, nil, nil, nil, cipher, 0, 0).(*modelTemplateService)
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
