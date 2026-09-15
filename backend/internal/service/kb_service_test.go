package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/internal/config"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	apperrors "agent-platform/pkg/errors"
)

// ---------- 假仓储 (内存实现, 参照 memory_service_test.go 模式) ----------

type fakeKBCatRepo struct {
	mu      sync.Mutex
	items   map[string]*model.KBCategory
	next    int
	countFn func(categoryID string) int64
}

func newFakeKBCatRepo() *fakeKBCatRepo {
	return &fakeKBCatRepo{items: make(map[string]*model.KBCategory)}
}

func (f *fakeKBCatRepo) Create(_ context.Context, cat *model.KBCategory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.items {
		// M11.5: 名称同一父级下同级唯一 (fake 与服务端语义一致)
		if strings.EqualFold(c.Name, cat.Name) && sameParent(c.ParentID, cat.ParentID) {
			return errors.New("duplicate key value violates unique constraint")
		}
	}
	f.next++
	cat.ID = "cat-" + string(rune('a'+f.next%26)) + string(rune('0'+f.next/26))
	if cat.CreatedAt.IsZero() {
		cat.CreatedAt = time.Now()
	}
	cat.UpdatedAt = cat.CreatedAt
	f.items[cat.ID] = cat
	return nil
}

func (f *fakeKBCatRepo) Get(_ context.Context, id string) (*model.KBCategory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.items[id]
	if !ok {
		return nil, apperrors.ErrNotFound
	}
	return c, nil
}

func (f *fakeKBCatRepo) GetByName(_ context.Context, name string) (*model.KBCategory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.items {
		if strings.EqualFold(c.Name, name) {
			return c, nil
		}
	}
	return nil, nil
}

// sameParent 两级父级指针相等 (nil = 顶级)
func sameParent(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// GetByNameUnderParent fake: 同一父级下按名称查
func (f *fakeKBCatRepo) GetByNameUnderParent(_ context.Context, name string, parentID *string) (*model.KBCategory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.items {
		if strings.EqualFold(c.Name, name) && sameParent(c.ParentID, parentID) {
			return c, nil
		}
	}
	return nil, nil
}

// CountChildren fake
func (f *fakeKBCatRepo) CountChildren(_ context.Context, parentID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var cnt int64
	for _, c := range f.items {
		if c.ParentID != nil && *c.ParentID == parentID {
			cnt++
		}
	}
	return cnt, nil
}

// ListByParents fake
func (f *fakeKBCatRepo) ListByParents(_ context.Context, parentIDs []string) ([]model.KBCategory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := make(map[string]bool, len(parentIDs))
	for _, id := range parentIDs {
		want[id] = true
	}
	var out []model.KBCategory
	for _, c := range f.items {
		if c.ParentID != nil && want[*c.ParentID] {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeKBCatRepo) Update(_ context.Context, cat *model.KBCategory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.items {
		if strings.EqualFold(c.Name, cat.Name) && c.ID != cat.ID && sameParent(c.ParentID, cat.ParentID) {
			return errors.New("duplicate key value violates unique constraint")
		}
	}
	if _, ok := f.items[cat.ID]; !ok {
		return apperrors.ErrNotFound
	}
	cat.UpdatedAt = time.Now()
	f.items[cat.ID] = cat
	return nil
}

func (f *fakeKBCatRepo) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, id)
	return nil
}

func (f *fakeKBCatRepo) List(_ context.Context) ([]repository.KBCategoryView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	views := make([]repository.KBCategoryView, 0, len(f.items))
	for _, c := range f.items {
		views = append(views, repository.KBCategoryView{KBCategory: *c})
	}
	return views, nil
}

func (f *fakeKBCatRepo) CountDocuments(_ context.Context, categoryID string, _ string) (int64, error) {
	if f.countFn != nil {
		return f.countFn(categoryID), nil
	}
	return 0, nil
}

type fakeKBDocRepo struct {
	mu    sync.Mutex
	items map[string]*model.KBDocument
	next  int
	// 测试钩子 (M11 W2 检索测试): 设置后覆盖默认行为
	vectorFn     func(categoryIDs []string, limit int) ([]model.KBDocument, error)
	candidatesFn func(categoryIDs []string, limit int) ([]model.KBDocument, error)
	searchErr    error
}

func newFakeKBDocRepo() *fakeKBDocRepo {
	return &fakeKBDocRepo{items: make(map[string]*model.KBDocument)}
}

func (f *fakeKBDocRepo) Create(_ context.Context, doc *model.KBDocument) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	doc.ID = "doc-" + string(rune('a'+f.next%26)) + string(rune('0'+f.next/26))
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = time.Now()
	}
	doc.UpdatedAt = doc.CreatedAt
	if doc.Status == "" {
		doc.Status = model.KBStatusActive
	}
	f.items[doc.ID] = doc
	return nil
}

func (f *fakeKBDocRepo) Get(_ context.Context, id string) (*model.KBDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.items[id]
	if !ok {
		return nil, apperrors.ErrNotFound
	}
	return d, nil
}

func (f *fakeKBDocRepo) Update(_ context.Context, doc *model.KBDocument) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[doc.ID]; !ok {
		return apperrors.ErrNotFound
	}
	doc.UpdatedAt = time.Now()
	f.items[doc.ID] = doc
	return nil
}

func (f *fakeKBDocRepo) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, id)
	return nil
}

func (f *fakeKBDocRepo) GetByIDs(_ context.Context, ids []string) (map[string]*model.KBDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]*model.KBDocument, len(ids))
	for _, id := range ids {
		if d, ok := f.items[id]; ok {
			out[id] = d
		}
	}
	return out, nil
}

// M11.5 块模式写路径 fake (块表由 fakeKBChunkRepo 独立模拟; 此处仅委托条目操作)
func (f *fakeKBDocRepo) CreateWithChunks(_ context.Context, doc *model.KBDocument, _ []string) error {
	return f.Create(context.Background(), doc)
}

func (f *fakeKBDocRepo) UpdateWithChunks(_ context.Context, doc *model.KBDocument, _ []string) error {
	return f.Update(context.Background(), doc)
}

func (f *fakeKBDocRepo) DeleteWithChunks(_ context.Context, id string) error {
	return f.Delete(context.Background(), id)
}

func (f *fakeKBDocRepo) UpdateStatus(_ context.Context, id, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.items[id]
	if !ok {
		return apperrors.ErrNotFound
	}
	d.Status = status
	d.UpdatedAt = time.Now()
	return nil
}

func (f *fakeKBDocRepo) List(_ context.Context, flt repository.KBDocumentListFilter) ([]repository.KBDocumentView, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []repository.KBDocumentView
	catSet := make(map[string]bool, len(flt.CategoryIDs))
	for _, id := range flt.CategoryIDs {
		catSet[id] = true
	}
	for _, d := range f.items {
		if len(catSet) > 0 && !catSet[d.CategoryID] {
			continue
		}
		if flt.Source != "" && d.Source != flt.Source {
			continue
		}
		if flt.Status != "" && d.Status != flt.Status {
			continue
		}
		out = append(out, repository.KBDocumentView{KBDocument: *d})
	}
	return out, int64(len(out)), nil
}

func (f *fakeKBDocRepo) SearchCandidates(_ context.Context, categoryIDs []string, status string, limit int) ([]model.KBDocument, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if f.candidatesFn != nil {
		return f.candidatesFn(categoryIDs, limit)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		limit = 500
	}
	ids := make(map[string]bool, len(categoryIDs))
	for _, id := range categoryIDs {
		ids[id] = true
	}
	var out []model.KBDocument
	for _, d := range f.items {
		if len(ids) > 0 && !ids[d.CategoryID] {
			continue
		}
		if status != "" && d.Status != status {
			continue
		}
		out = append(out, *d)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeKBDocRepo) BumpAccess(_ context.Context, id string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.items[id]
	if !ok {
		return apperrors.ErrNotFound
	}
	d.AccessCount++
	return nil
}

// SearchByVector fake: 仅按分类过滤 (忽略向量, 用于验证检索流水线降级链)
func (f *fakeKBDocRepo) SearchByVector(ctx context.Context, categoryIDs []string, _ *model.Vector, limit int) ([]model.KBDocument, error) {
	if f.vectorFn != nil {
		return f.vectorFn(categoryIDs, limit)
	}
	return f.SearchCandidates(ctx, categoryIDs, model.KBStatusActive, limit)
}

// ListUnembedded fake: active 且无向量
func (f *fakeKBDocRepo) ListUnembedded(_ context.Context, limit int) ([]model.KBDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	var out []model.KBDocument
	for _, d := range f.items {
		if d.Status != model.KBStatusActive || !d.Embedding.IsNull() {
			continue
		}
		out = append(out, *d)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetEmbedding fake: 回写向量
func (f *fakeKBDocRepo) SetEmbedding(_ context.Context, id string, vec *model.Vector) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.items[id]
	if !ok {
		return apperrors.ErrNotFound
	}
	if vec != nil {
		d.Embedding = vec
	}
	return nil
}

// CountUnembedded fake
func (f *fakeKBDocRepo) CountUnembedded(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var cnt int64
	for _, d := range f.items {
		if d.Status == model.KBStatusActive && d.Embedding.IsNull() {
			cnt++
		}
	}
	return cnt, nil
}

// SetEmbeddingIfNull fake: 仅 NULL 时写入 (幂等)
func (f *fakeKBDocRepo) SetEmbeddingIfNull(_ context.Context, id string, vec *model.Vector) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.items[id]
	if !ok {
		return apperrors.ErrNotFound
	}
	if vec != nil && d.Embedding.IsNull() {
		d.Embedding = vec
	}
	return nil
}

type fakeKBBindingRepo struct {
	mu    sync.Mutex
	items map[string]*model.AgentKBBinding // key: agentID|categoryID
}

func newFakeKBBindingRepo() *fakeKBBindingRepo {
	return &fakeKBBindingRepo{items: make(map[string]*model.AgentKBBinding)}
}

func bindingKey(agentID, categoryID string) string { return agentID + "|" + categoryID }

func (f *fakeKBBindingRepo) Bind(_ context.Context, agentID, categoryID string, readOnly bool, _ *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := bindingKey(agentID, categoryID)
	if b, ok := f.items[key]; ok {
		b.ReadOnly = readOnly
		return nil
	}
	b := &model.AgentKBBinding{ID: "bnd-" + key, AgentID: agentID, CategoryID: categoryID, ReadOnly: readOnly, CreatedAt: time.Now()}
	f.items[key] = b
	return nil
}

func (f *fakeKBBindingRepo) Unbind(_ context.Context, agentID, categoryID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, bindingKey(agentID, categoryID))
	return nil
}

func (f *fakeKBBindingRepo) ListByAgent(_ context.Context, agentID string) ([]model.AgentKBBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.AgentKBBinding
	for _, b := range f.items {
		if b.AgentID == agentID {
			out = append(out, *b)
		}
	}
	return out, nil
}

func (f *fakeKBBindingRepo) ListByCategory(_ context.Context, categoryID string) ([]model.AgentKBBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.AgentKBBinding
	for _, b := range f.items {
		if b.CategoryID == categoryID {
			out = append(out, *b)
		}
	}
	return out, nil
}

func (f *fakeKBBindingRepo) DeleteByAgent(_ context.Context, agentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, b := range f.items {
		if b.AgentID == agentID {
			delete(f.items, k)
		}
	}
	return nil
}

func (f *fakeKBBindingRepo) DeleteByCategory(_ context.Context, categoryID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, b := range f.items {
		if b.CategoryID == categoryID {
			delete(f.items, k)
		}
	}
	return nil
}

type fakeSessionRepo struct {
	mu    sync.Mutex
	items map[string]*model.ChatSession
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{items: make(map[string]*model.ChatSession)}
}

func (f *fakeSessionRepo) add(s *model.ChatSession) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[s.ID] = s
}

func (f *fakeSessionRepo) Get(_ context.Context, id string) (*model.ChatSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[id]
	if !ok {
		return nil, apperrors.ErrNotFound
	}
	return s, nil
}

func (f *fakeSessionRepo) Create(context.Context, *model.ChatSession) error { return nil }
func (f *fakeSessionRepo) ListByAgent(context.Context, string, int, int) ([]model.ChatSession, int64, error) {
	return nil, 0, nil
}
func (f *fakeSessionRepo) UpdateTitle(context.Context, string, string) error   { return nil }
func (f *fakeSessionRepo) UpdateSummary(context.Context, string, string) error { return nil }
func (f *fakeSessionRepo) TouchLastMessage(context.Context, string) error      { return nil }
func (f *fakeSessionRepo) DeleteByAgent(context.Context, string) error         { return nil }
func (f *fakeSessionRepo) DeleteByAgentCascade(context.Context, string) error  { return nil }
func (f *fakeSessionRepo) DeleteCascade(context.Context, string) error         { return nil }

type fakeKBAuditRepo struct {
	mu      sync.Mutex
	entries []model.AuditLog
}

func newFakeKBAuditRepo() *fakeKBAuditRepo {
	return &fakeKBAuditRepo{}
}
func (f *fakeKBAuditRepo) Append(_ context.Context, entry *model.AuditLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, *entry)
	return nil
}

func (f *fakeKBAuditRepo) List(context.Context, string, int, int) ([]model.AuditLog, int64, error) {
	return nil, 0, nil
}

func (f *fakeKBAuditRepo) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, e.Action)
	}
	return out
}

// newTestKBService 组装测试服务
func newTestKBService() (KBService, *fakeKBCatRepo, *fakeKBDocRepo, *fakeKBBindingRepo, *fakeSessionRepo, *fakeKBAuditRepo) {
	cats := newFakeKBCatRepo()
	docs := newFakeKBDocRepo()
	bindings := newFakeKBBindingRepo()
	sessions := newFakeSessionRepo()
	audits := newFakeKBAuditRepo()
	svc := NewKBService(cats, docs, bindings, sessions, audits, config.KBConfig{MaxDocBytes: model.KBMaxDocBytes}, nil, nil, nil)
	return svc, cats, docs, bindings, sessions, audits
}

// ---------- 分类 ----------

func TestKBCategoryNameValidation(t *testing.T) {
	svc, _, _, _, _, _ := newTestKBService()
	ctx := context.Background()
	for _, name := range []string{"", "a", strings.Repeat("x", 33)} {
		if _, err := svc.CreateCategory(ctx, "", name, "", "op", "op", "1.2.3.4"); err == nil {
			t.Fatalf("name %q should be rejected", name)
		}
	}
	cat, err := svc.CreateCategory(ctx, "", "运维手册", "描述", "op", "op", "1.2.3.4")
	if err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
	if cat.ID == "" {
		t.Fatal("created category has no ID")
	}
}

func TestKBCategoryDuplicateCaseInsensitive(t *testing.T) {
	svc, _, _, _, _, audits := newTestKBService()
	ctx := context.Background()
	if _, err := svc.CreateCategory(ctx, "", "Runbook", "", "op", "op", "ip"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.CreateCategory(ctx, "", "runbook", "", "op", "op", "ip"); err == nil {
		t.Fatal("duplicate (case-insensitive) should be rejected")
	}
	actions := audits.actions()
	if len(actions) != 1 || actions[0] != "kb.category_created" {
		t.Fatalf("audit = %v, want 1x kb.category_created", actions)
	}
}

func TestKBCategoryRenameSelfExcluded(t *testing.T) {
	svc, _, _, _, _, _ := newTestKBService()
	ctx := context.Background()
	cat, err := svc.CreateCategory(ctx, "", "旧名", "", "op", "op", "ip")
	if err != nil {
		t.Fatal(err)
	}
	// 大小写变化重命名为自身不应报错
	if _, err := svc.UpdateCategory(ctx, cat.ID, nil, "旧名", "", "op", "op", "ip"); err != nil {
		t.Fatalf("rename to self (case variant) rejected: %v", err)
	}
	// 与他人重名应报错
	if _, err := svc.CreateCategory(ctx, "", "其他分类", "", "op", "op", "ip"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateCategory(ctx, cat.ID, nil, "其他分类", "", "op", "op", "ip"); err == nil {
		t.Fatal("rename to existing name should be rejected")
	}
}

func TestKBCategoryDeleteProtection(t *testing.T) {
	svc, cats, docs, bindings, _, audits := newTestKBService()
	ctx := context.Background()
	cat, _ := svc.CreateCategory(ctx, "", "可删分类", "", "op", "op", "ip")

	// 有条目时: 直接写一条 doc (绕过 service), 让 CountDocuments 可见
	doc := &model.KBDocument{CategoryID: cat.ID, Title: "t", Content: "c", Source: model.KBSourceManual, Status: model.KBStatusActive}
	_ = docs.Create(ctx, doc)
	cats.countFn = func(categoryID string) int64 {
		var n int64
		for _, d := range docsAll(docs) {
			if d.CategoryID == categoryID {
				n++
			}
		}
		return n
	}

	err := svc.DeleteCategory(ctx, cat.ID, "op", "op", "ip")
	if err == nil {
		t.Fatal("delete non-empty category should fail")
	}
	if !strings.Contains(err.Error(), "分类下存在条目") {
		t.Fatalf("err = %v, want in-use message", err)
	}

	// 空分类可删 + 审计 + 绑定清理
	other, _ := svc.CreateCategory(ctx, "", "空分类", "", "op", "op", "ip")
	_ = bindings.Bind(ctx, "agent-1", other.ID, false, nil)
	if err := svc.DeleteCategory(ctx, other.ID, "op", "op", "ip"); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
	if bs, _ := bindings.ListByAgent(ctx, "agent-1"); len(bs) != 0 {
		t.Fatalf("bindings after delete = %d, want 0", len(bs))
	}
	actions := audits.actions()
	last := actions[len(actions)-1]
	if last != "kb.category_deleted" {
		t.Fatalf("last audit = %s, want kb.category_deleted", last)
	}
}

func docsAll(f *fakeKBDocRepo) []model.KBDocument {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.KBDocument, 0, len(f.items))
	for _, d := range f.items {
		out = append(out, *d)
	}
	return out
}

// ---------- 条目 ----------

func TestKBDocumentCreateValidation(t *testing.T) {
	svc, cats, _, _, _, _ := newTestKBService()
	ctx := context.Background()
	cat, _ := svc.CreateCategory(ctx, "", "分类A", "", "op", "op", "ip")

	valid := CreateDocumentRequest{CategoryID: cat.ID, Title: "正常标题", Content: "正文内容"}

	cases := []struct {
		name string
		req  CreateDocumentRequest
	}{
		{"title too short", CreateDocumentRequest{CategoryID: cat.ID, Title: "x", Content: "正文内容"}},
		{"title too long", CreateDocumentRequest{CategoryID: cat.ID, Title: strings.Repeat("标", 101), Content: "正文内容"}},
		{"empty content", CreateDocumentRequest{CategoryID: cat.ID, Title: "正常标题", Content: "   "}},
		{"bad category", CreateDocumentRequest{CategoryID: "cat-none", Title: "正常标题", Content: "正文内容"}},
		{"bad source", CreateDocumentRequest{CategoryID: cat.ID, Title: "正常标题", Content: "正文内容", Source: "unknown"}},
		{"oversized content", CreateDocumentRequest{CategoryID: cat.ID, Title: "正常标题", Content: strings.Repeat("a", model.KBMaxDocBytes+1)}},
	}
	for _, tc := range cases {
		if _, err := svc.CreateDocument(ctx, tc.req, "op", "op", "ip"); err == nil {
			t.Fatalf("%s: should be rejected", tc.name)
		}
	}
	if _, err := svc.CreateDocument(ctx, valid, "op", "op", "ip"); err != nil {
		t.Fatalf("valid doc rejected: %v", err)
	}
	_ = cats
}

func TestKBDocumentChatSummaryAuthz(t *testing.T) {
	svc, cats, _, bindings, sessions, audits := newTestKBService()
	ctx := context.Background()
	bound, _ := svc.CreateCategory(ctx, "", "绑定分类", "", "op", "op", "ip")
	unbound, _ := svc.CreateCategory(ctx, "", "未绑定分类", "", "op", "op", "ip")
	_ = bindings.Bind(ctx, "agent-1", bound.ID, false, nil)
	sess := &model.ChatSession{ID: "sess-1", AgentID: "agent-1"}
	sessions.add(sess)
	// 其他 Agent 的会话
	otherSess := &model.ChatSession{ID: "sess-2", AgentID: "agent-2"}
	sessions.add(otherSess)

	base := CreateDocumentRequest{Title: "总结标题", Content: "总结正文内容", Source: model.KBSourceChatSummary}

	// 缺字段
	req := base
	if _, err := svc.CreateDocument(ctx, req, "op", "op", "ip"); err == nil {
		t.Fatal("missing session/agent should be rejected")
	}
	// 会话不存在
	req = base
	req.CategoryID = bound.ID
	req.SourceSessionID = "sess-none"
	req.SourceAgentID = "agent-1"
	if _, err := svc.CreateDocument(ctx, req, "op", "op", "ip"); err == nil {
		t.Fatal("nonexistent session should be rejected")
	}
	// 会话不属于该 Agent
	req = base
	req.CategoryID = bound.ID
	req.SourceSessionID = "sess-2"
	req.SourceAgentID = "agent-1"
	if _, err := svc.CreateDocument(ctx, req, "op", "op", "ip"); err == nil {
		t.Fatal("session of another agent should be rejected")
	}
	// 分类未绑定 -> 403
	req = base
	req.CategoryID = unbound.ID
	req.SourceSessionID = "sess-1"
	req.SourceAgentID = "agent-1"
	if _, err := svc.CreateDocument(ctx, req, "op", "op", "ip"); err == nil {
		t.Fatal("unbound category should be rejected")
	} else if !strings.Contains(err.Error(), "权限不足") {
		t.Fatalf("want forbidden, got: %v", err)
	}
	// 合法路径
	req = base
	req.CategoryID = bound.ID
	req.SourceSessionID = "sess-1"
	req.SourceAgentID = "agent-1"
	doc, err := svc.CreateDocument(ctx, req, "op", "op", "ip")
	if err != nil {
		t.Fatalf("valid chat_summary rejected: %v", err)
	}
	if doc.Source != model.KBSourceChatSummary || doc.SourceSessionID == nil || *doc.SourceSessionID != "sess-1" {
		t.Fatalf("doc = %+v", doc)
	}
	actions := audits.actions()
	last := actions[len(actions)-1]
	if last != "kb.document_saved_from_chat" {
		t.Fatalf("last audit = %s, want kb.document_saved_from_chat", last)
	}
	_ = cats
}

func TestKBDocumentStatusToggle(t *testing.T) {
	svc, _, docs, _, _, audits := newTestKBService()
	ctx := context.Background()
	cat, _ := svc.CreateCategory(ctx, "", "分类B", "", "op", "op", "ip")
	doc, _ := svc.CreateDocument(ctx, CreateDocumentRequest{CategoryID: cat.ID, Title: "状态测试", Content: "正文"}, "op", "op", "ip")

	if _, err := svc.SetDocumentStatus(ctx, doc.ID, "unknown", "op", "op", "ip"); err == nil {
		t.Fatal("invalid status should be rejected")
	}
	archived, err := svc.SetDocumentStatus(ctx, doc.ID, model.KBStatusArchived, "op", "op", "ip")
	if err != nil || archived.Status != model.KBStatusArchived {
		t.Fatalf("archive: %v %+v", err, archived)
	}
	// 幂等
	if _, err := svc.SetDocumentStatus(ctx, doc.ID, model.KBStatusArchived, "op", "op", "ip"); err != nil {
		t.Fatalf("idempotent archive: %v", err)
	}
	restored, err := svc.SetDocumentStatus(ctx, doc.ID, model.KBStatusActive, "op", "op", "ip")
	if err != nil || restored.Status != model.KBStatusActive {
		t.Fatalf("restore: %v %+v", err, restored)
	}
	if d, _ := docs.Get(ctx, doc.ID); d.Status != model.KBStatusActive {
		t.Fatalf("stored status = %s", d.Status)
	}
	actions := audits.actions()
	var hasArchived, hasRestored bool
	for _, a := range actions {
		if a == "kb.document_archived" {
			hasArchived = true
		}
		if a == "kb.document_restored" {
			hasRestored = true
		}
	}
	if !hasArchived || !hasRestored {
		t.Fatalf("audits = %v, want archived+restored", actions)
	}
}

// ---------- 试算检索 ----------

func TestKBTrialSearch(t *testing.T) {
	svc, cats, _, _, _, _ := newTestKBService()
	ctx := context.Background()
	cat1, _ := svc.CreateCategory(ctx, "", "数据库", "", "op", "op", "ip")
	cat2, _ := svc.CreateCategory(ctx, "", "网络", "", "op", "op", "ip")

	d1, _ := svc.CreateDocument(ctx, CreateDocumentRequest{CategoryID: cat1.ID, Title: "PostgreSQL 慢查询", Content: "慢查询优化: 索引、执行计划、统计信息"}, "op", "op", "ip")
	d2, _ := svc.CreateDocument(ctx, CreateDocumentRequest{CategoryID: cat1.ID, Title: "Docker 网络", Content: "docker compose 网络模式与端口映射"}, "op", "op", "ip")
	d3, _ := svc.CreateDocument(ctx, CreateDocumentRequest{CategoryID: cat2.ID, Title: "Nginx 配置", Content: "nginx 反向代理与负载均衡配置"}, "op", "op", "ip")

	// 空查询
	hits, err := svc.TrialSearch(ctx, "  ", nil, 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("empty query: %v %d", err, len(hits))
	}
	// 无命中
	hits, err = svc.TrialSearch(ctx, "量子纠缠", nil, 5)
	if err != nil || len(hits) != 0 {
		t.Fatalf("no-match query: %v %d", err, len(hits))
	}
	// 命中 + 分类范围
	hits, err = svc.TrialSearch(ctx, "慢查询", []string{cat1.ID}, 5)
	if err != nil || len(hits) == 0 || hits[0].ID != d1.ID {
		t.Fatalf("scope search: %v %+v", err, hits)
	}
	if hits[0].CategoryName != "数据库" {
		t.Fatalf("category name = %s", hits[0].CategoryName)
	}
	// topK 截断
	all, _ := svc.TrialSearch(ctx, "配置", nil, 1)
	if len(all) != 1 {
		t.Fatalf("topK=1 got %d", len(all))
	}
	// 归档条目不参与检索
	_, _ = svc.SetDocumentStatus(ctx, d1.ID, model.KBStatusArchived, "op", "op", "ip")
	hits, _ = svc.TrialSearch(ctx, "慢查询", nil, 5)
	for _, h := range hits {
		if h.ID == d1.ID {
			t.Fatal("archived doc should not be searched")
		}
	}
	// 跨分类检索
	hits, _ = svc.TrialSearch(ctx, "网络", nil, 5)
	found := false
	for _, h := range hits {
		if h.ID == d2.ID || h.ID == d3.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("cross-category search missed: %+v", hits)
	}
	_ = cats
	_ = d3
}
