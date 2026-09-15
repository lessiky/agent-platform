package service

// kb_scope_test.go — M11.5 作用域扩展 + 只读校验单测 (事项 1 绑定语义 / 事项 2 只读绑定)

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent-platform/internal/config"
	"agent-platform/internal/model"
	apperrors "agent-platform/pkg/errors"
)

// newTestKBRetriever 组装 fake 检索器 (无 embedder/reranker = 纯作用域逻辑)
func newTestKBRetriever(cats *fakeKBCatRepo, docs *fakeKBDocRepo, bindings *fakeKBBindingRepo) *KBRetriever {
	cfg := config.KBConfig{TopK: 3, RecallSize: 20, MaxCandidates: 500}
	return NewKBRetriever(docs, cats, bindings, nil, nil, nil, cfg, 0)
}

// seedTree 造树: top (无子) / parent (child1 + child2) / roParent (roChild)
func seedTree(cats *fakeKBCatRepo) {
	addKBCat(cats, "top-1", "顶级", "")
	addKBCat(cats, "parent-1", "父级", "")
	addKBCat(cats, "child-1", "子一", "")
	addKBCat(cats, "child-2", "子二", "")
	addKBCat(cats, "ro-parent", "只读父", "")
	addKBCat(cats, "ro-child", "只读子", "")
	cats.items["child-1"].ParentID = strPtr("parent-1")
	cats.items["child-2"].ParentID = strPtr("parent-1")
	cats.items["ro-child"].ParentID = strPtr("ro-parent")
}

func TestBoundCategoryIDsExpansion(t *testing.T) {
	ctx := context.Background()

	// 平铺绑定 (存量无父子) → no-op
	{
		cats, docs, bindings := newFakeKBCatRepo(), newFakeKBDocRepo(), newFakeKBBindingRepo()
		addKBCat(cats, "top-1", "顶级", "")
		r := newTestKBRetriever(cats, docs, bindings)
		_ = bindings.Bind(ctx, "a1", "top-1", false, nil)
		ids, err := r.BoundCategoryIDs(ctx, "a1")
		if err != nil || len(ids) != 1 || ids[0] != "top-1" {
			t.Fatalf("flat binding: ids = %v (err=%v), want [top-1]", ids, err)
		}
	}
	// 绑定父级 → 扩展含全部子级
	{
		cats, docs, bindings := newFakeKBCatRepo(), newFakeKBDocRepo(), newFakeKBBindingRepo()
		seedTree(cats)
		r := newTestKBRetriever(cats, docs, bindings)
		_ = bindings.Bind(ctx, "a2", "parent-1", false, nil)
		ids, _ := r.BoundCategoryIDs(ctx, "a2")
		if len(ids) != 3 || !containsStr(ids, "parent-1") || !containsStr(ids, "child-1") || !containsStr(ids, "child-2") {
			t.Fatalf("parent expansion: ids = %v, want parent-1+child-1+child-2", ids)
		}
	}
	// 父 + 子同时绑定 → 去重
	{
		cats, docs, bindings := newFakeKBCatRepo(), newFakeKBDocRepo(), newFakeKBBindingRepo()
		seedTree(cats)
		r := newTestKBRetriever(cats, docs, bindings)
		_ = bindings.Bind(ctx, "a3", "parent-1", false, nil)
		_ = bindings.Bind(ctx, "a3", "child-1", false, nil)
		ids, _ := r.BoundCategoryIDs(ctx, "a3")
		if len(ids) != 3 {
			t.Fatalf("dedupe: ids = %v, want 3 unique", ids)
		}
	}
	// 无绑定 → 空
	{
		cats, docs, bindings := newFakeKBCatRepo(), newFakeKBDocRepo(), newFakeKBBindingRepo()
		seedTree(cats)
		r := newTestKBRetriever(cats, docs, bindings)
		ids, _ := r.BoundCategoryIDs(ctx, "a4")
		if len(ids) != 0 {
			t.Fatalf("no binding: ids = %v, want empty", ids)
		}
	}
}

func TestWritableCategoryIDs(t *testing.T) {
	ctx := context.Background()

	// 读写绑定父级 → 可写 = 父 + 子 (子级扩展进入可写作用域, A14)
	{
		cats, docs, bindings := newFakeKBCatRepo(), newFakeKBDocRepo(), newFakeKBBindingRepo()
		seedTree(cats)
		r := newTestKBRetriever(cats, docs, bindings)
		_ = bindings.Bind(ctx, "a1", "parent-1", false, nil)
		ids, _ := r.WritableCategoryIDs(ctx, "a1")
		if len(ids) != 3 || !containsStr(ids, "child-1") {
			t.Fatalf("rw parent: writable = %v, want parent+children", ids)
		}
	}
	// 仅只读绑定父级 → 可写为空 (A18/A19)
	{
		cats, docs, bindings := newFakeKBCatRepo(), newFakeKBDocRepo(), newFakeKBBindingRepo()
		seedTree(cats)
		r := newTestKBRetriever(cats, docs, bindings)
		_ = bindings.Bind(ctx, "a2", "ro-parent", true, nil)
		ids, _ := r.WritableCategoryIDs(ctx, "a2")
		if len(ids) != 0 {
			t.Fatalf("ro-only: writable = %v, want empty (含子级也不进入可写作用域)", ids)
		}
		// 读作用域仍然生效 (A17: 只读分类照常进入检索作用域)
		readIDs, _ := r.BoundCategoryIDs(ctx, "a2")
		if len(readIDs) != 2 || !containsStr(readIDs, "ro-child") {
			t.Fatalf("ro read scope: ids = %v, want ro-parent+ro-child", readIDs)
		}
	}
}

// TestChatSummaryWriteScope 总结入库目标分类可写作用域校验 (validateChatSummarySource)
func TestChatSummaryWriteScope(t *testing.T) {
	cats, docs, bindings, sessions, audits := newFakeKBCatRepo(), newFakeKBDocRepo(), newFakeKBBindingRepo(), newFakeSessionRepo(), newFakeKBAuditRepo()
	svc := NewKBService(cats, docs, bindings, sessions, audits, config.KBConfig{MaxDocBytes: model.KBMaxDocBytes}, nil, nil, nil).(*kbService)
	ctx := context.Background()

	seedTree(cats)
	sessions.add(&model.ChatSession{ID: "s1", AgentID: "a1"})
	_ = bindings.Bind(ctx, "a1", "parent-1", false, nil) // 读写父级
	_ = bindings.Bind(ctx, "a1", "ro-parent", true, nil)  // 只读父级

	cases := []struct {
		cat string
		ok  bool
	}{
		{"parent-1", true},  // 读写绑定本身
		{"child-1", true},   // 读写父级的子级 (A14)
		{"child-2", true},   // 读写父级的另一子级
		{"ro-parent", false}, // 只读分类 (A18)
		{"ro-child", false},  // 只读父级的子级
		{"top-1", false},     // 未绑定分类
	}
	for _, c := range cases {
		err := svc.validateChatSummarySource(ctx, c.cat, "s1", "a1")
		if c.ok && err != nil {
			t.Fatalf("category %s should be writable: %v", c.cat, err)
		}
		if !c.ok && err != apperrors.ErrForbidden {
			t.Fatalf("category %s should be forbidden: err = %v, want 403", c.cat, err)
		}
	}
}

// TestKBCategoryPairDisjoint 读写/只读数组交集 → 400 (A20)
func TestKBCategoryPairDisjoint(t *testing.T) {
	svc, _, cats, _, _ := newTestAgentKBService()
	ctx := context.Background()
	addKBCat(cats, "cat-a", "A", "")
	addKBCat(cats, "cat-b", "B", "")

	if err := svc.validateKBCategoryPair(ctx, []string{"cat-a"}, []string{"cat-b"}); err != nil {
		t.Fatalf("disjoint arrays: %v", err)
	}
	err := svc.validateKBCategoryPair(ctx, []string{"cat-a"}, []string{"cat-a"})
	if err == nil || !strings.Contains(err.Error(), "交集须为空") {
		t.Fatalf("overlap: err = %v, want 交集须为空", err)
	}
	if err := svc.validateKBCategoryPair(ctx, []string{"cat-gone"}, nil); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing category: err = %v, want 不存在", err)
	}
}

// TestKBBindFlagChangeAudit 只读标志变更审计 (rw↔ro 记录前后值, A20)
func TestKBBindFlagChangeAudit(t *testing.T) {
	svc, agents, cats, bindings, audits := newTestAgentKBService()
	ctx := context.Background()
	agents.add(&model.Agent{ID: "agent-1", Name: "AgentA"})
	addKBCat(cats, "cat-a", "A", "")

	lastKBAction := func() (string, map[string]interface{}) {
		for i := len(audits.entries) - 1; i >= 0; i-- {
			if strings.HasPrefix(audits.entries[i].Action, "kb.agent_") {
				var d map[string]interface{}
				_ = json.Unmarshal(audits.entries[i].Detail, &d)
				return audits.entries[i].Action, d
			}
		}
		return "", nil
	}

	// 首次读写绑定
	if err := svc.syncKBBindings(ctx, "agent-1", []string{"cat-a"}, nil, "op"); err != nil {
		t.Fatalf("bind rw: %v", err)
	}
	action, detail := lastKBAction()
	if action != "kb.agent_bound" || detail["read_only"] != false {
		t.Fatalf("first bind audit: action=%s detail=%v, want kb.agent_bound read_only=false", action, detail)
	}
	// 切换为只读: 审计含前后值
	if err := svc.syncKBBindings(ctx, "agent-1", nil, []string{"cat-a"}, "op"); err != nil {
		t.Fatalf("bind ro: %v", err)
	}
	action, detail = lastKBAction()
	if action != "kb.agent_bound" || detail["read_only"] != true || detail["old_read_only"] != false {
		t.Fatalf("flag change audit: action=%s detail=%v, want read_only=true old_read_only=false", action, detail)
	}
	// 绑定行标志同步
	bs, _ := bindings.ListByAgent(ctx, "agent-1")
	if len(bs) != 1 || !bs[0].ReadOnly {
		t.Fatalf("binding row: %+v, want ReadOnly=true", bs)
	}
	// 解绑
	if err := svc.syncKBBindings(ctx, "agent-1", nil, nil, "op"); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	action, detail = lastKBAction()
	if action != "kb.agent_unbound" || detail["read_only"] != true {
		t.Fatalf("unbind audit: action=%s detail=%v, want kb.agent_unbound read_only=true", action, detail)
	}
}