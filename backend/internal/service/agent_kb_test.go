package service

// agent_kb_test.go — M11 W2 Agent 知识库绑定单测 (计划 D10):
// 检索模式归一 / 分类校验 / 绑定同步与审计 / 绑定视图 / 试算降级

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	apperrors "agent-platform/pkg/errors"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ---------- Agent 仓库 fake (本测试所需最小实现) ----------

type fakeAgentRepo struct {
	mu    sync.Mutex
	items map[string]*model.Agent
}

func newFakeAgentRepo() *fakeAgentRepo {
	return &fakeAgentRepo{items: make(map[string]*model.Agent)}
}

func (f *fakeAgentRepo) add(a *model.Agent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a.ID == "" {
		a.ID = "agent-" + a.Name
	}
	f.items[a.ID] = a
}

func (f *fakeAgentRepo) Create(_ context.Context, a *model.Agent) error {
	f.add(a)
	return nil
}

func (f *fakeAgentRepo) GetByID(_ context.Context, id string) (*model.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.items[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return a, nil
}

func (f *fakeAgentRepo) GetByName(_ context.Context, name string) (*model.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.items {
		if a.Name == name {
			return a, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeAgentRepo) Update(_ context.Context, a *model.Agent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[a.ID] = a
	return nil
}

func (f *fakeAgentRepo) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, id)
	return nil
}

func (f *fakeAgentRepo) List(_ context.Context, _ repository.AgentListFilter) ([]*model.Agent, int64, error) {
	return nil, 0, nil
}

func (f *fakeAgentRepo) ListByIDs(_ context.Context, ids []string) ([]model.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]model.Agent, 0, len(ids))
	for _, id := range ids {
		if a, ok := f.items[id]; ok {
			result = append(result, *a)
		}
	}
	return result, nil
}

func (f *fakeAgentRepo) CountByStatus(_ context.Context) (map[string]int64, error) {
	return map[string]int64{}, nil
}

// ---------- 组装 ----------

func newTestAgentKBService() (*agentService, *fakeAgentRepo, *fakeKBCatRepo, *fakeKBBindingRepo, *fakeKBAuditRepo) {
	agents := newFakeAgentRepo()
	cats := newFakeKBCatRepo()
	bindings := newFakeKBBindingRepo()
	audits := newFakeKBAuditRepo()
	svc := &agentService{agents: agents, kbBindings: bindings, kbCats: cats, audits: audits}
	return svc, agents, cats, bindings, audits
}

func addKBCat(cats *fakeKBCatRepo, id, name, desc string) {
	cats.items[id] = &model.KBCategory{ID: id, Name: name, Description: desc, CreatedAt: time.Now()}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---------- 检索模式 ----------

func TestNormalizeKbSearchMode(t *testing.T) {
	for in, want := range map[string]string{
		"": "auto", "  tool  ": "tool", "off": "off", "AUTO": "auto", "garbage": "auto",
	} {
		if got := normalizeKbSearchMode(in); got != want {
			t.Fatalf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateKbSearchMode(t *testing.T) {
	for _, ok := range []string{"", "auto", "tool", "off"} {
		if err := validateKbSearchMode(ok); err != nil {
			t.Fatalf("validate(%q): %v", ok, err)
		}
	}
	if err := validateKbSearchMode("bad"); err == nil {
		t.Fatal("validate(bad) should error")
	}
}

// ---------- 分类校验 ----------

func TestAgentValidateKBCategories(t *testing.T) {
	svc, _, cats, _, _ := newTestAgentKBService()
	addKBCat(cats, "cat-a", "分类A", "")
	addKBCat(cats, "cat-b", "分类B", "")
	ctx := context.Background()

	if err := svc.validateKBCategories(ctx, nil); err != nil {
		t.Fatalf("empty binding: %v", err)
	}
	if err := svc.validateKBCategories(ctx, []string{"cat-a", "cat-b"}); err != nil {
		t.Fatalf("all exist: %v", err)
	}
	if err := svc.validateKBCategories(ctx, []string{"cat-a", "cat-a", " cat-b ", ""}); err != nil {
		t.Fatalf("dedup and trim: %v", err)
	}
	err := svc.validateKBCategories(ctx, []string{"cat-a", "cat-x"})
	if err == nil || !strings.Contains(err.Error(), "cat-x") {
		t.Fatalf("missing category should error with id, err=%v", err)
	}
}

// ---------- 绑定同步与审计 ----------

func TestAgentSyncKBBindings(t *testing.T) {
	svc, _, cats, bindings, audits := newTestAgentKBService()
	addKBCat(cats, "cat-a", "分类A", "")
	addKBCat(cats, "cat-b", "分类B", "")
	addKBCat(cats, "cat-c", "分类C", "")
	ctx := context.Background()
	_ = bindings.Bind(ctx, "agent-1", "cat-a", false, nil)
	_ = bindings.Bind(ctx, "agent-1", "cat-b", false, nil)

	// 移除 cat-a, 新增 cat-c
	if err := svc.syncKBBindings(ctx, "agent-1", []string{"cat-b", "cat-c"}, nil, "op-1"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	got, _ := bindings.ListByAgent(ctx, "agent-1")
	if len(got) != 2 {
		t.Fatalf("bindings = %d, want 2", len(got))
	}
	set := map[string]bool{}
	for _, b := range got {
		set[b.CategoryID] = true
	}
	if !set["cat-b"] || !set["cat-c"] || set["cat-a"] {
		t.Fatalf("binding set = %v, want {cat-b, cat-c}", set)
	}
	actions := audits.actions()
	if !containsStr(actions, "kb.agent_bound") || !containsStr(actions, "kb.agent_unbound") || len(actions) != 2 {
		t.Fatalf("audit actions = %v, want exactly kb.agent_bound + kb.agent_unbound", actions)
	}

	// 幂等: 无变化不产生新审计
	if err := svc.syncKBBindings(ctx, "agent-1", []string{"cat-b", "cat-c"}, nil, "op-1"); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if n := len(audits.actions()); n != 2 {
		t.Fatalf("audit count after idempotent re-sync = %d, want 2", n)
	}
}

// ---------- 绑定视图 ----------

func TestAgentListAgentKB(t *testing.T) {
	svc, agents, cats, bindings, _ := newTestAgentKBService()
	addKBCat(cats, "cat-b", "分类B", "desc")
	cats.countFn = func(id string) int64 {
		if id == "cat-b" {
			return 7
		}
		return 0
	}
	agents.add(&model.Agent{
		ID:     "agent-1",
		Name:   "助手",
		Config: datatypes.JSON(`{"kb_search_mode":"","knowledge_categories":["cat-b","cat-gone"]}`),
	})
	ctx := context.Background()
	_ = bindings.Bind(ctx, "agent-1", "cat-b", false, nil)
	_ = bindings.Bind(ctx, "agent-1", "cat-gone", false, nil) // 孤儿绑定 (分类已删除)

	view, err := svc.ListAgentKB(ctx, "agent-1")
	if err != nil {
		t.Fatalf("ListAgentKB: %v", err)
	}
	if view.KbSearchMode != "auto" {
		t.Fatalf("kb_search_mode = %q, want 归一为 auto", view.KbSearchMode)
	}
	if len(view.Categories) != 1 || view.Categories[0].ID != "cat-b" || view.Categories[0].DocumentCount != 7 {
		t.Fatalf("view.Categories = %+v, want 仅 cat-b 且 7 条", view.Categories)
	}

	if _, err := svc.ListAgentKB(ctx, "nope"); err != apperrors.ErrNotFound {
		t.Fatalf("missing agent: err=%v, want ErrNotFound", err)
	}
}

// ---------- 试算 ----------

func TestAgentTrialSearchForAgent(t *testing.T) {
	svc, agents, _, _, _ := newTestAgentKBService()
	agents.add(&model.Agent{ID: "agent-1", Name: "助手", Config: datatypes.JSON(`{}`)})
	ctx := context.Background()

	// 未配置检索组件 → 返回空不报错 (试算不阻断)
	hits, err := svc.TrialSearchForAgent(ctx, "agent-1", "q", 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("no retriever: hits=%d err=%v, want 空且不报错", len(hits), err)
	}
	// 空查询 → 空
	if _, err := svc.TrialSearchForAgent(ctx, "agent-1", "   ", 5); err != nil {
		t.Fatalf("empty query: %v", err)
	}
	// Agent 不存在 → 404
	if _, err := svc.TrialSearchForAgent(ctx, "nope", "q", 5); err != apperrors.ErrNotFound {
		t.Fatalf("missing agent: err=%v, want ErrNotFound", err)
	}
}
