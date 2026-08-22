package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"

	"gorm.io/datatypes"
)

// ---------- fakes ----------

type fakeAIGenChat struct {
	replies []string
	calls   int
	lastMessages []modelclient.ChatMessage
}

func (f *fakeAIGenChat) RouteAndChat(ctx context.Context, agentID string, messages []modelclient.ChatMessage, tools []modelclient.ChatToolDef, gen modelclient.GenOptions) (*ChatOutcome, error) {
	f.calls++
	f.lastMessages = messages
	if f.calls > len(f.replies) {
		return nil, errors.New("fake: no more replies")
	}
	if f.replies[f.calls-1] == "__no_model__" {
		return nil, ErrNoModelAvailable
	}
	return &ChatOutcome{
		Content:      f.replies[f.calls-1],
		Model:        "mock-model",
		TemplateName: "fake-template",
		TemplateID:   "tpl-1",
		TotalTokens:  100,
	}, nil
}

type fakeAIGenAgents struct{ agents []model.Agent }

func (f *fakeAIGenAgents) List(ctx context.Context, filter repository.AgentListFilter) ([]*model.Agent, int64, error) {
	out := make([]*model.Agent, 0, len(f.agents))
	for i := range f.agents {
		out = append(out, &f.agents[i])
	}
	return out, int64(len(out)), nil
}

type fakeAIGenMCPServers struct{ servers []model.MCPServer }

func (f *fakeAIGenMCPServers) List(ctx context.Context, filter repository.MCPListFilter) ([]model.MCPServer, int64, error) {
	return f.servers, int64(len(f.servers)), nil
}

// ---------- extractJSONPayload ----------

func TestExtractJSONPayload(t *testing.T) {
	plain := `{"a":1}`
	got, err := extractJSONPayload(plain)
	if err != nil || got != plain {
		t.Fatalf("plain: got=%q err=%v", got, err)
	}

	fenced := "```json\n" + plain + "\n```"
	got, err = extractJSONPayload(fenced)
	if err != nil || got != plain {
		t.Fatalf("fenced: got=%q err=%v", got, err)
	}

	chatty := "好的, 这是生成的工作流:\n```json\n" + plain + "\n```\n如需调整请告诉我。"
	got, err = extractJSONPayload(chatty)
	if err != nil || got != plain {
		t.Fatalf("chatty: got=%q err=%v", got, err)
	}

	if _, err := extractJSONPayload("no json here"); err == nil {
		t.Fatal("expected error for non-json content")
	}
	if _, err := extractJSONPayload("   "); err == nil {
		t.Fatal("expected error for empty content")
	}
}

// ---------- Generate ----------

const validGenReply = `{
  "name": "订单处理",
  "description": "调用订单工具并等待",
  "input_schema": {"type": "object", "properties": {"order_id": {"type": "string"}}},
  "definition": {
    "version": 3,
    "nodes": [
      {"id": "n1", "type": "delay", "name": "等待", "config": {"seconds": 1}},
      {"id": "n2", "type": "mcp_tool", "name": "查询订单", "config": {"mcp_server_id": "mcp-1", "tool": "order.query", "arguments": {"order_id": "$inputs.order_id"}}}
    ],
    "edges": [{"id": "e1", "source": "n1", "target": "n2"}]
  }
}`

const cycleGenReply = `{
  "name": "环",
  "definition": {
    "version": 1,
    "nodes": [
      {"id": "n1", "type": "delay", "name": "a", "config": {"seconds": 1}},
      {"id": "n2", "type": "delay", "name": "b", "config": {"seconds": 1}}
    ],
    "edges": [{"id": "e1", "source": "n1", "target": "n2"}, {"id": "e2", "source": "n2", "target": "n1"}]
  }
}`

func newTestGenerator(chat *fakeAIGenChat) *WorkflowAIGenerator {
	return NewWorkflowAIGenerator(chat,
		&fakeAIGenAgents{agents: []model.Agent{{ID: "agent-1", Name: "客服"}}},
		&fakeAIGenMCPServers{servers: []model.MCPServer{{
			ID:    "mcp-1",
			Name:  "order-mcp",
			Tools: datatypes.JSON(`[{"name":"order.query","description":"查询订单"}]`),
		}}},
	)
}


func TestAIGenerate_OK_FirstAttempt(t *testing.T) {
	chat := &fakeAIGenChat{replies: []string{validGenReply}}
	gen := newTestGenerator(chat)

	result, err := gen.Generate(context.Background(), AIGenerateWorkflowRequest{Description: "处理订单"})
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if result.Attempts != 1 || chat.calls != 1 {
		t.Fatalf("expected 1 attempt, got attempts=%d calls=%d", result.Attempts, chat.calls)
	}
	if result.Name != "订单处理" {
		t.Fatalf("unexpected name: %s", result.Name)
	}
	if !strings.Contains(string(result.Definition), `"n1"`) {
		t.Fatalf("definition missing nodes: %s", result.Definition)
	}
	if result.Model != "fake-template" || result.ModelID != "tpl-1" {
		t.Fatalf("unexpected model info: %+v", result)
	}
	// input_schema 透传
	if !strings.Contains(string(result.InputSchema), "order_id") {
		t.Fatalf("input_schema missing: %s", result.InputSchema)
	}
	// 版本强制为 1
	if !strings.Contains(string(result.Definition), `"version":1`) {
		t.Fatalf("version not normalized: %s", result.Definition)
	}
}

func TestAIGenerate_RetryAfterValidationError(t *testing.T) {
	chat := &fakeAIGenChat{replies: []string{cycleGenReply, validGenReply}}
	gen := newTestGenerator(chat)

	result, err := gen.Generate(context.Background(), AIGenerateWorkflowRequest{Description: "x"})
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if result.Attempts != 2 || chat.calls != 2 {
		t.Fatalf("expected retry, got attempts=%d calls=%d", result.Attempts, chat.calls)
	}
	// 第二次请求应携带第一次的错误反馈 (循环依赖)
	retryMsg := chat.lastMessages[len(chat.lastMessages)-1].Content
	if !strings.Contains(retryMsg, "未通过校验") || !strings.Contains(retryMsg, "循环依赖") {
		t.Fatalf("retry feedback missing: %s", retryMsg)
	}
}

func TestAIGenerate_HallucinatedAgentID_Retry(t *testing.T) {
	hallucinated := `{
	  "name": "幻觉",
	  "definition": {
	    "version": 1,
	    "nodes": [{"id": "n1", "type": "agent", "name": "调用", "config": {"agent_id": "no-such-agent", "message": "hi"}}],
	    "edges": []
	  }
	}`
	chat := &fakeAIGenChat{replies: []string{hallucinated, validGenReply}}
	gen := newTestGenerator(chat)

	result, err := gen.Generate(context.Background(), AIGenerateWorkflowRequest{Description: "x"})
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if result.Attempts != 2 {
		t.Fatalf("expected retry, got attempts=%d", result.Attempts)
	}
}

func TestAIGenerate_BothAttemptsInvalid(t *testing.T) {
	chat := &fakeAIGenChat{replies: []string{"这不是 JSON", cycleGenReply}}
	gen := newTestGenerator(chat)

	_, err := gen.Generate(context.Background(), AIGenerateWorkflowRequest{Description: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "未通过校验") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAIGenerate_NoModelAvailable(t *testing.T) {
	chat := &fakeAIGenChat{replies: []string{"__no_model__"}}
	gen := newTestGenerator(chat)

	_, err := gen.Generate(context.Background(), AIGenerateWorkflowRequest{Description: "x"})
	if !errors.Is(err, ErrNoModelAvailable) {
		t.Fatalf("expected ErrNoModelAvailable, got %v", err)
	}
	// 环境错误不重试
	if chat.calls != 1 {
		t.Fatalf("expected no retry, got %d calls", chat.calls)
	}
}

func TestAIGenerate_EmptyDescription(t *testing.T) {
	gen := newTestGenerator(&fakeAIGenChat{})
	if _, err := gen.Generate(context.Background(), AIGenerateWorkflowRequest{Description: "   "}); err == nil {
		t.Fatal("expected error for empty description")
	}
}

func TestAIGenerate_NameFallbackAndTruncate(t *testing.T) {
	longName := strings.Repeat("长", 100)
	reply := `{
	  "name": "` + longName + `",
	  "definition": {"version": 1, "nodes": [{"id": "n1", "type": "delay", "config": {"seconds": 1}}], "edges": []}
	}`
	chat := &fakeAIGenChat{replies: []string{reply}}
	gen := newTestGenerator(chat)

	result, err := gen.Generate(context.Background(), AIGenerateWorkflowRequest{Description: "x"})
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if got := len([]rune(result.Name)); got != 64 {
		t.Fatalf("name not truncated to 64 runes, got %d", got)
	}

	noName := `{"definition": {"version": 1, "nodes": [{"id": "n1", "type": "delay", "config": {"seconds": 1}}], "edges": []}}`
	chat2 := &fakeAIGenChat{replies: []string{noName}}
	gen2 := newTestGenerator(chat2)
	result2, err := gen2.Generate(context.Background(), AIGenerateWorkflowRequest{Description: "我的描述"})
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if result2.Name != "AI 生成工作流" {
		t.Fatalf("expected fallback name, got %q", result2.Name)
	}
	if result2.Description != "我的描述" {
		t.Fatalf("expected fallback description, got %q", result2.Description)
	}
}
