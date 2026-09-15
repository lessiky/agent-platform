package service

// chat_kb_test.go — M11 W2 注入链单测 (计划 D10):
// prepareKBTurn 开关/会话/绑定/模式门禁 + auto 注入 + 故障不阻断 (A2/A8/A10)

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agent-platform/internal/config"
	"agent-platform/internal/model"
)

// newTestChatKBService 组装最小 chatService (仅注入链所需依赖)
func newTestChatKBService(enabled bool, agentID string, bindCatIDs []string, doc *model.KBDocument) (*chatService, *fakeKBDocRepo) {
	fdocs := newFakeKBDocRepo()
	if doc != nil {
		fdocs.items[doc.ID] = doc
	}
	fb := newFakeKBBindingRepo()
	for _, cid := range bindCatIDs {
		fb.items[bindingKey(agentID, cid)] = &model.AgentKBBinding{ID: "bnd-" + agentID + "-" + cid, AgentID: agentID, CategoryID: cid}
	}
	retriever := newTestRetriever(fdocs, newFakeKBCatRepo(), fb, &fakeKBEmbedder{}, &fakeKBReranker{})
	svc := &chatService{
		logs:        &fakeLogRepo{},
		kbRetriever: retriever,
		kbCfg: config.KBConfig{
			TopK:             3,
			RecallSize:       20,
			MaxCandidates:    500,
			RerankTimeout:    5 * time.Second,
			RetrievalTimeout: 2 * time.Second,
			CharBudget:       4000,
		},
		kbEnabled: NewKBEnabledSource(enabled),
	}
	return svc, fdocs
}

func kbTestDoc() *model.KBDocument {
	return &model.KBDocument{
		ID: "da", CategoryID: "cat-a", Title: "postgres 备份手册",
		Content: "postgres 备份失败的处理流程与检查清单", Status: model.KBStatusActive, UpdatedAt: time.Now(),
	}
}

// auto 模式: 命中注入 + 数据声明/分隔符 + 注入 ID 记录 + 注册工具
func TestPrepareKBTurnAutoInjects(t *testing.T) {
	svc, _ := newTestChatKBService(true, "agent-1", []string{"cat-a"}, kbTestDoc())

	kt := svc.prepareKBTurn(context.Background(), "agent-1", &AgentConfig{KbSearchMode: ""}, "chat", "exec-1", &model.ChatSession{ID: "sess-1"}, "postgres 备份失败怎么办")
	if kt == nil {
		t.Fatal("kt = nil, want auto 模式注入轮")
	}
	if kt.mode != model.KBSearchModeAuto || !kt.registerTool() {
		t.Fatalf("mode=%q registerTool=%v, want auto + true", kt.mode, kt.registerTool())
	}
	if !strings.Contains(kt.section, "### postgres 备份手册") {
		t.Fatalf("section 未包含命中条目:\n%s", kt.section)
	}
	if !strings.Contains(kt.section, "[知识库参考数据 开始]") || !strings.Contains(kt.section, "[知识库参考数据 结束]") {
		t.Fatalf("section 缺少数据声明/分隔符:\n%s", kt.section)
	}
	if len(kt.injected) != 1 || kt.injected[0] != "da" {
		t.Fatalf("injected = %v, want [da]", kt.injected)
	}
}

// A2: 无绑定 → 不注入不注册工具
func TestPrepareKBTurnNoBinding(t *testing.T) {
	svc, _ := newTestChatKBService(true, "agent-1", nil, kbTestDoc())
	if kt := svc.prepareKBTurn(context.Background(), "agent-1", &AgentConfig{KbSearchMode: "auto"}, "chat", "exec-1", &model.ChatSession{ID: "s1"}, "postgres 备份"); kt != nil {
		t.Fatalf("kt = %+v, want nil (无绑定)", kt)
	}
}

// A10: KB 总开关关闭 → 不启用
func TestPrepareKBTurnDisabled(t *testing.T) {
	svc, _ := newTestChatKBService(false, "agent-1", []string{"cat-a"}, kbTestDoc())
	if kt := svc.prepareKBTurn(context.Background(), "agent-1", &AgentConfig{KbSearchMode: "auto"}, "chat", "exec-1", &model.ChatSession{ID: "s1"}, "postgres 备份"); kt != nil {
		t.Fatalf("kt = %+v, want nil (开关关闭)", kt)
	}
}

// 模式 off → 不启用
func TestPrepareKBTurnModeOff(t *testing.T) {
	svc, _ := newTestChatKBService(true, "agent-1", []string{"cat-a"}, kbTestDoc())
	if kt := svc.prepareKBTurn(context.Background(), "agent-1", &AgentConfig{KbSearchMode: "off"}, "chat", "exec-1", &model.ChatSession{ID: "s1"}, "postgres 备份"); kt != nil {
		t.Fatalf("kt = %+v, want nil (mode off)", kt)
	}
}

// 模式 tool: 不自动注入 (空段), 但允许注册工具
func TestPrepareKBTurnModeTool(t *testing.T) {
	svc, _ := newTestChatKBService(true, "agent-1", []string{"cat-a"}, kbTestDoc())
	kt := svc.prepareKBTurn(context.Background(), "agent-1", &AgentConfig{KbSearchMode: "tool"}, "chat", "exec-1", &model.ChatSession{ID: "s1"}, "postgres 备份")
	if kt == nil || kt.mode != model.KBSearchModeTool || kt.section != "" || !kt.registerTool() {
		t.Fatalf("kt = %+v, want tool 模式: 空注入段 + 注册工具", kt)
	}
}

// 无会话 → 不启用
func TestPrepareKBTurnNoSession(t *testing.T) {
	svc, _ := newTestChatKBService(true, "agent-1", []string{"cat-a"}, kbTestDoc())
	if kt := svc.prepareKBTurn(context.Background(), "agent-1", &AgentConfig{KbSearchMode: "auto"}, "chat", "exec-1", nil, "postgres 备份"); kt != nil {
		t.Fatalf("kt = %+v, want nil (无会话)", kt)
	}
}

// A8: 检索故障 → 空注入段不阻断 (返回 kt 而非 nil)
func TestPrepareKBTurnRetrievalFailureNonBlocking(t *testing.T) {
	svc, fdocs := newTestChatKBService(true, "agent-1", []string{"cat-a"}, kbTestDoc())
	fdocs.searchErr = errors.New("database unavailable")

	kt := svc.prepareKBTurn(context.Background(), "agent-1", &AgentConfig{KbSearchMode: "auto"}, "chat", "exec-1", &model.ChatSession{ID: "s1"}, "postgres 备份失败")
	if kt == nil {
		t.Fatal("kt = nil, want 非阻断 (空注入段)")
	}
	if kt.section != "" || len(kt.injected) != 0 {
		t.Fatalf("section=%q injected=%v, want 全空", kt.section, kt.injected)
	}
}
