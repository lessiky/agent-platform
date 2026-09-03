package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
)

// fakeMemRepo 记忆仓储假实现 (内存 map, 可注入阻塞模拟超时)
type fakeMemRepo struct {
	mu        sync.Mutex
	items     map[string]*model.Memory
	nextID    int
	bumps     [][]string
	blockRead bool
	readCalls int
}

func newFakeMemRepo() *fakeMemRepo {
	return &fakeMemRepo{items: make(map[string]*model.Memory)}
}

func (f *fakeMemRepo) Create(ctx context.Context, m *model.Memory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	m.ID = "mem-" + string(rune('0'+f.nextID%10)) + string(rune('a'+f.nextID/10))
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	m.UpdatedAt = m.CreatedAt
	f.items[m.ID] = m
	return nil
}

func (f *fakeMemRepo) Get(ctx context.Context, agentID, id string) (*model.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.items[id]
	if !ok || m.AgentID != agentID {
		return nil, errors.ErrNotFound
	}
	return m, nil
}

func (f *fakeMemRepo) List(ctx context.Context, flt repository.MemoryListFilter) ([]model.Memory, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Memory
	for _, m := range f.items {
		if m.AgentID != flt.AgentID {
			continue
		}
		if flt.Kind != "" && m.Kind != flt.Kind {
			continue
		}
		if flt.Status != "" && m.Status != flt.Status {
			continue
		}
		switch flt.Scope {
		case model.MemoryScopeAgent:
			if m.UserID != nil {
				continue
			}
		case model.MemoryScopeMine:
			if m.UserID != nil && *m.UserID != flt.UserID {
				continue
			}
		}
		out = append(out, *m)
	}
	return out, int64(len(out)), nil
}

func (f *fakeMemRepo) ListActiveForRetrieval(ctx context.Context, agentID string, limit int) ([]model.Memory, error) {
	f.mu.Lock()
	f.readCalls++
	block := f.blockRead
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Memory
	for _, m := range f.items {
		if m.AgentID == agentID && m.Status == model.MemoryStatusActive {
			out = append(out, *m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListActiveForScope 同 scope 活跃记忆 (M10.2 接口新增, 单测假实现)
func (f *fakeMemRepo) ListActiveForScope(ctx context.Context, agentID string, userID *string, limit int) ([]model.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Memory
	for _, m := range f.items {
		if m.AgentID != agentID || m.Status != model.MemoryStatusActive {
			continue
		}
		if userID == nil {
			if m.UserID != nil {
				continue
			}
		} else if m.UserID == nil || *m.UserID != *userID {
			continue
		}
		out = append(out, *m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeMemRepo) Update(ctx context.Context, agentID, id string, fields map[string]interface{}) (*model.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.items[id]
	if !ok || m.AgentID != agentID {
		return nil, errors.ErrNotFound
	}
	if v, ok := fields["content"].(string); ok {
		m.Content = v
	}
	if v, ok := fields["kind"].(string); ok {
		m.Kind = v
	}
	if v, ok := fields["status"].(string); ok {
		m.Status = v
	}
	m.UpdatedAt = time.Now()
	return m, nil
}

func (f *fakeMemRepo) Delete(ctx context.Context, agentID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.items[id]
	if !ok || m.AgentID != agentID {
		return errors.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

func (f *fakeMemRepo) DeleteByAgent(ctx context.Context, agentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, m := range f.items {
		if m.AgentID == agentID {
			delete(f.items, id)
		}
	}
	return nil
}

func (f *fakeMemRepo) BumpAccess(ctx context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bumps = append(f.bumps, ids)
	return nil
}

func (f *fakeMemRepo) UpdateEmbedding(ctx context.Context, agentID, id string, embedding datatypes.JSON) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.items[id]; ok {
		m.Embedding = embedding
		return nil
	}
	return errors.ErrNotFound
}

func (f *fakeMemRepo) ListMissingEmbedding(ctx context.Context, limit, offset int) ([]model.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Memory
	for _, m := range f.items {
		if m.Status == model.MemoryStatusActive && len(m.Embedding) == 0 {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (f *fakeMemRepo) add(agentID, userID, kind, content string) *model.Memory {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	m := &model.Memory{
		ID:        "seed-" + string(rune('0'+f.nextID)),
		AgentID:   agentID,
		UserID:    ptrOrNil(userID),
		Kind:      kind,
		Content:   content,
		Source:    model.MemorySourceLLMExtracted,
		Status:    model.MemoryStatusActive,
		CreatedAt: time.Now(),
	}
	m.UpdatedAt = m.CreatedAt
	f.items[m.ID] = m
	return m
}

// fakeAuditRepo 审计仓储假实现 (记录条目)
type fakeAuditRepo struct {
	mu      sync.Mutex
	entries []*model.AuditLog
}

func (f *fakeAuditRepo) Append(ctx context.Context, entry *model.AuditLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, entry)
	return nil
}

func (f *fakeAuditRepo) List(ctx context.Context, action string, page, size int) ([]model.AuditLog, int64, error) {
	return nil, 0, nil
}

func (f *fakeAuditRepo) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, e.Action)
	}
	return out
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func newTestMemoryService(repo *fakeMemRepo, enabled bool) (*memoryService, *fakeAuditRepo) {
	audit := &fakeAuditRepo{}
	svc := NewMemoryService(repo, audit, enabled, 10, 800, 500*time.Millisecond, time.Hour, nil).(*memoryService)
	return svc, audit
}

func sessionWith(user string) *model.ChatSession {
	return &model.ChatSession{ID: "sess-1", AgentID: "agent-1", UserID: ptrOrNil(user)}
}

func TestBuildMemorySectionDisabled(t *testing.T) {
	repo := newFakeMemRepo()
	repo.add("agent-1", "u1", model.MemoryKindPreference, "用户偏好简洁")
	svc, _ := newTestMemoryService(repo, false)
	section, ids := svc.BuildMemorySection(context.Background(), "agent-1", sessionWith("u1"), "偏好")
	if section != "" || ids != nil {
		t.Fatalf("disabled service injected: section=%q ids=%v", section, ids)
	}
}

func TestBuildMemorySectionInjection(t *testing.T) {
	repo := newFakeMemRepo()
	repo.add("agent-1", "u1", model.MemoryKindPreference, "用户偏好简洁的回答")
	repo.add("agent-1", "", model.MemoryKindFact, "Agent 运行在 K8s 集群")
	repo.add("agent-1", "u2", model.MemoryKindFact, "其他用户的记忆不应出现")
	repo.add("agent-1", "u1", model.MemoryKindFact, "已归档的记忆不应出现") // 下面改为 archived
	repo.mu.Lock()
	repo.items["seed-4"].Status = model.MemoryStatusArchived
	repo.mu.Unlock()

	svc, _ := newTestMemoryService(repo, true)
	section, ids := svc.BuildMemorySection(context.Background(), "agent-1", sessionWith("u1"), "我的回答偏好是什么")
	if len(ids) == 0 {
		t.Fatalf("no memories injected:\n%s", section)
	}
	for _, want := range []string{"## 长期记忆", "用户偏好简洁的回答", "Agent 运行在 K8s 集群"} {
		if !strings.Contains(section, want) {
			t.Errorf("section missing %q:\n%s", want, section)
		}
	}
	for _, banned := range []string{"其他用户的记忆", "已归档的记忆"} {
		if strings.Contains(section, banned) {
			t.Errorf("section leaked %q:\n%s", banned, section)
		}
	}
	// 访问统计异步触发 (等待 goroutine)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		n := len(repo.bumps)
		repo.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	repo.mu.Lock()
	bumped := repo.bumps
	repo.mu.Unlock()
	if len(bumped) != 1 || len(bumped[0]) != len(ids) {
		t.Errorf("BumpAccess = %v, want 1 call with %d ids", bumped, len(ids))
	}
}

func TestBuildMemorySectionTimeout(t *testing.T) {
	repo := newFakeMemRepo()
	repo.blockRead = true
	svc, _ := newTestMemoryService(repo, true)
	// 收紧超时, 验证降级为空段
	svc.retrievalTimeout = 30 * time.Millisecond
	section, ids := svc.BuildMemorySection(context.Background(), "agent-1", sessionWith("u1"), "偏好")
	if section != "" || ids != nil {
		t.Fatalf("timeout should degrade to empty, got section=%q ids=%v", section, ids)
	}
}

func TestBuildMemorySectionCache(t *testing.T) {
	repo := newFakeMemRepo()
	repo.add("agent-1", "u1", model.MemoryKindFact, "记忆甲")
	repo.add("agent-1", "u1", model.MemoryKindFact, "记忆乙")
	svc, _ := newTestMemoryService(repo, true)
	svc.BuildMemorySection(context.Background(), "agent-1", sessionWith("u1"), "记忆")
	svc.BuildMemorySection(context.Background(), "agent-1", sessionWith("u1"), "记忆")
	repo.mu.Lock()
	calls := repo.readCalls
	repo.mu.Unlock()
	if calls != 1 {
		t.Fatalf("active set loaded %d times, want 1 (TTL 缓存)", calls)
	}
	// 写入失效: 创建后应立即重新加载
	if _, err := svc.CreateMemory(context.Background(), "agent-1", "u1", "u1", "", CreateMemoryRequest{Content: "记忆丙"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	svc.BuildMemorySection(context.Background(), "agent-1", sessionWith("u1"), "记忆")
	repo.mu.Lock()
	calls = repo.readCalls
	repo.mu.Unlock()
	if calls != 2 {
		t.Fatalf("after write, active set loads = %d, want 2 (缓存失效)", calls)
	}
}

func TestCreateMemoryValidation(t *testing.T) {
	repo := newFakeMemRepo()
	svc, _ := newTestMemoryService(repo, true)

	if _, err := svc.CreateMemory(context.Background(), "agent-1", "u1", "u1", "", CreateMemoryRequest{Content: "  "}); err == nil {
		t.Error("empty content accepted, want validation error")
	}
	long := strings.Repeat("长", model.MemoryContentMaxLen+1)
	if _, err := svc.CreateMemory(context.Background(), "agent-1", "u1", "u1", "", CreateMemoryRequest{Content: long}); err == nil {
		t.Error("overlong content accepted, want validation error")
	}
	if _, err := svc.CreateMemory(context.Background(), "agent-1", "u1", "u1", "", CreateMemoryRequest{Content: "x", Kind: "bad"}); err == nil {
		t.Error("invalid kind accepted, want validation error")
	}
	// scope=user 默认绑定操作者
	m, err := svc.CreateMemory(context.Background(), "agent-1", "u1", "u1", "", CreateMemoryRequest{Content: "用户偏好简洁"})
	if err != nil {
		t.Fatalf("create user scope: %v", err)
	}
	if m.UserID == nil || *m.UserID != "u1" || m.Source != model.MemorySourceUserExplicit {
		t.Errorf("user scope memory = %+v, want user_id=u1 source=user_explicit", m)
	}
	// scope=agent 全局 (user_id 空)
	m, err = svc.CreateMemory(context.Background(), "agent-1", "u1", "u1", "", CreateMemoryRequest{Content: "Agent 级记忆", Scope: model.MemoryScopeAgent})
	if err != nil {
		t.Fatalf("create agent scope: %v", err)
	}
	if m.UserID != nil {
		t.Errorf("agent scope memory user_id = %v, want nil", m.UserID)
	}
}

func TestGetMemoryOwnerIsolation(t *testing.T) {
	repo := newFakeMemRepo()
	m := repo.add("agent-1", "u1", model.MemoryKindFact, "甲的记忆")
	svc, _ := newTestMemoryService(repo, true)

	if _, err := svc.GetMemory(context.Background(), "agent-1", "u2", m.ID, false); err != errors.ErrNotFound {
		t.Errorf("non-owner get err = %v, want not_found (不泄露存在性)", err)
	}
	got, err := svc.GetMemory(context.Background(), "agent-1", "u1", m.ID, false)
	if err != nil || got.ID != m.ID {
		t.Errorf("owner get = (%v, %v), want found", got, err)
	}
	if _, err := svc.GetMemory(context.Background(), "agent-1", "u2", m.ID, true); err != nil {
		t.Errorf("admin get err = %v, want nil", err)
	}
}

func TestUpdateMemoryOwnerIsolation(t *testing.T) {
	repo := newFakeMemRepo()
	m := repo.add("agent-1", "u1", model.MemoryKindFact, "甲的记忆")
	svc, audit := newTestMemoryService(repo, true)

	if _, err := svc.UpdateMemory(context.Background(), "agent-1", "u2", "u2", "", m.ID, false, UpdateMemoryRequest{Content: "被篡改"}); err != errors.ErrForbidden {
		t.Errorf("non-owner update err = %v, want forbidden", err)
	}
	updated, err := svc.UpdateMemory(context.Background(), "agent-1", "u1", "u1", "", m.ID, false, UpdateMemoryRequest{Status: model.MemoryStatusArchived})
	if err != nil {
		t.Fatalf("owner update: %v", err)
	}
	if updated.Status != model.MemoryStatusArchived {
		t.Errorf("status = %s, want archived", updated.Status)
	}
	// 状态变更审计
	found := false
	for _, a := range audit.actions() {
		if a == auditMemoryStatusChanged {
			found = true
		}
	}
	if !found {
		t.Errorf("audit actions = %v, want contain %s", audit.actions(), auditMemoryStatusChanged)
	}
	// agent 级记忆 (user_id 空) 任何持 agent:write 者均可更新
	am := repo.add("agent-1", "", model.MemoryKindFact, "Agent 级记忆")
	if _, err := svc.UpdateMemory(context.Background(), "agent-1", "u2", "u2", "", am.ID, false, UpdateMemoryRequest{Content: "Agent 级记忆 (更新)"}); err != nil {
		t.Errorf("agent-level update by non-admin err = %v, want nil", err)
	}
}

func TestDeleteMemoryOwnerIsolation(t *testing.T) {
	repo := newFakeMemRepo()
	m := repo.add("agent-1", "u1", model.MemoryKindFact, "甲的记忆")
	svc, audit := newTestMemoryService(repo, true)

	if err := svc.DeleteMemory(context.Background(), "agent-1", "u2", "u2", "", m.ID, false); err != errors.ErrForbidden {
		t.Errorf("non-owner delete err = %v, want forbidden", err)
	}
	if err := svc.DeleteMemory(context.Background(), "agent-1", "u1", "u1", "", m.ID, false); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	repo.mu.Lock()
	_, exists := repo.items[m.ID]
	repo.mu.Unlock()
	if exists {
		t.Error("memory still exists after delete")
	}
	found := false
	for _, a := range audit.actions() {
		if a == auditMemoryDeleted {
			found = true
		}
	}
	if !found {
		t.Errorf("audit actions = %v, want contain %s", audit.actions(), auditMemoryDeleted)
	}
}

func TestListMemoriesScopeAllNonAdmin(t *testing.T) {
	repo := newFakeMemRepo()
	svc, _ := newTestMemoryService(repo, true)
	_, _, err := svc.ListMemories(context.Background(), "u1", false, repository.MemoryListFilter{AgentID: "agent-1", UserID: "u1", Scope: model.MemoryScopeAll})
	if err != errors.ErrForbidden {
		t.Errorf("scope=all non-admin err = %v, want forbidden", err)
	}
}
