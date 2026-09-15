package service

// kb_task_test.go — M11.5 回填任务状态机单测 (事项 3: 单飞 / 进度 / 取消 / 重启恢复 / 失败)

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/internal/model"
	apperrors "agent-platform/pkg/errors"
)

// ---------- 假任务仓储 (内存, 线程安全, 模拟 WHERE 原子条件) ----------

type fakeKBTasksRepo struct {
	mu    sync.Mutex
	items map[string]*model.KBBackfillTask
	next  int
}

func newFakeKBTasksRepo() *fakeKBTasksRepo {
	return &fakeKBTasksRepo{items: make(map[string]*model.KBBackfillTask)}
}

func (f *fakeKBTasksRepo) seed(t *model.KBBackfillTask) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	if t.ID == "" {
		t.ID = "task-" + string(rune('0'+f.next))
	}
	f.items[t.ID] = t
}

func (f *fakeKBTasksRepo) Create(_ context.Context, t *model.KBBackfillTask) error {
	f.seed(t)
	return nil
}

func (f *fakeKBTasksRepo) Get(_ context.Context, id string) (*model.KBBackfillTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.items[id]
	if !ok {
		return nil, apperrors.ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (f *fakeKBTasksRepo) FindActive(_ context.Context) (*model.KBBackfillTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var oldest *model.KBBackfillTask
	for _, t := range f.items {
		if t.Status != model.KBTaskStatusPending && t.Status != model.KBTaskStatusRunning {
			continue
		}
		if oldest == nil || t.CreatedAt.Before(oldest.CreatedAt) {
			oldest = t
		}
	}
	if oldest == nil {
		return nil, nil
	}
	cp := *oldest
	return &cp, nil
}

func (f *fakeKBTasksRepo) ListRecent(_ context.Context, limit int) ([]model.KBBackfillTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.KBBackfillTask
	for _, t := range f.items {
		out = append(out, *t)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeKBTasksRepo) SetTotal(_ context.Context, id string, total int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id].Total = total
	return nil
}

func (f *fakeKBTasksRepo) AddTotal(_ context.Context, id string, delta int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id].Total += delta
	return nil
}

// SetRunning 模拟 WHERE status='pending' 原子条件
func (f *fakeKBTasksRepo) SetRunning(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.items[id]
	if t.Status != model.KBTaskStatusPending {
		return nil
	}
	t.Status = model.KBTaskStatusRunning
	t.StartedAt = &at
	return nil
}

func (f *fakeKBTasksRepo) AddProgress(_ context.Context, id string, done, failed int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id].Done += done
	f.items[id].Failed += failed
	return nil
}

func (f *fakeKBTasksRepo) Finish(_ context.Context, id, status, lastError string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.items[id]
	t.Status = status
	t.LastError = lastError
	t.FinishedAt = &at
	return nil
}

func (f *fakeKBTasksRepo) MarkOrphansFailed(_ context.Context, reason string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, t := range f.items {
		if t.Status == model.KBTaskStatusPending || t.Status == model.KBTaskStatusRunning {
			t.Status = model.KBTaskStatusFailed
			t.LastError = reason
			now := time.Now()
			t.FinishedAt = &now
			n++
		}
	}
	return n, nil
}

// ---------- 假向量器 (可慢/可维度失败) ----------

type fakeKBTaskEmbedder struct {
	mu      sync.Mutex
	slow    time.Duration
	failDim bool
	batches int
}

func (f *fakeKBTaskEmbedder) Enabled() bool { return true }

func (f *fakeKBTaskEmbedder) EmbedOne(_ context.Context, _ string) ([]float32, error) {
	return []float32{1}, nil
}

func (f *fakeKBTaskEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.batches++
	f.mu.Unlock()
	if f.slow > 0 {
		time.Sleep(f.slow)
	}
	if f.failDim {
		return nil, fmt.Errorf("embedding 模型输出维度 1 与知识库向量列维度 1024 不匹配")
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1}
	}
	return out, nil
}

// ---------- 工具 ----------

func seedUnembeddedDocs(docs *fakeKBDocRepo, n int) {
	docs.mu.Lock()
	defer docs.mu.Unlock()
	docs.items = make(map[string]*model.KBDocument)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("doc-%03d", i)
		docs.items[id] = &model.KBDocument{ID: id, Title: "T", Content: "C", Status: model.KBStatusActive}
	}
}

func newTestKBTaskService(tasks *fakeKBTasksRepo, docs *fakeKBDocRepo, emb KBEmbedder, batch int) KBTaskService {
	return NewKBTaskService(tasks, NewKBVectorWriter(docs, nil, emb, batch))
}

func waitTaskStatus(t *testing.T, svc KBTaskService, id, want string) *model.KBBackfillTask {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var task *model.KBBackfillTask
	for time.Now().Before(deadline) {
		task, _ = svc.Get(context.Background(), id)
		if task != nil && task.Status == want {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := "nil"
	if task != nil {
		got = task.Status
	}
	t.Fatalf("task %s 未到达 %s (当前 %s)", id, want, got)
	return nil
}

// ---------- 用例 ----------

// TestKBTaskSingleFlight 单飞: 重复启动 409 返回运行中任务 (A22)
func TestKBTaskSingleFlight(t *testing.T) {
	tasks, docs := newFakeKBTasksRepo(), newFakeKBDocRepo()
	seedUnembeddedDocs(docs, 5)
	svc := newTestKBTaskService(tasks, docs, &fakeKBTaskEmbedder{slow: 50 * time.Millisecond}, 32)
	ctx := context.Background()

	t1, err := svc.Start(ctx, "op", "op", "ip")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t2, err := svc.Start(ctx, "op", "op", "ip")
	if err != apperrors.ErrKBTaskActive {
		t.Fatalf("second start: err = %v, want ErrKBTaskActive", err)
	}
	if t2.ID != t1.ID {
		t.Fatalf("second start returned task %s, want running task %s", t2.ID, t1.ID)
	}
	final := waitTaskStatus(t, svc, t1.ID, model.KBTaskStatusSucceeded)
	if final.Total != 5 || final.Done != 5 {
		t.Fatalf("progress: total=%d done=%d, want 5/5", final.Total, final.Done)
	}
	// 终态后可再启动
	t3, err := svc.Start(ctx, "op", "op", "ip")
	if err != nil {
		t.Fatalf("restart after finish: %v", err)
	}
	if t3.ID == t1.ID {
		t.Fatalf("restart should create a new task")
	}
	waitTaskStatus(t, svc, t3.ID, model.KBTaskStatusSucceeded)
}

// TestKBTaskProgress 状态机 pending→running→succeeded, 进度与 DB 一致 (A21)
func TestKBTaskProgress(t *testing.T) {
	tasks, docs := newFakeKBTasksRepo(), newFakeKBDocRepo()
	seedUnembeddedDocs(docs, 70)
	svc := newTestKBTaskService(tasks, docs, &fakeKBTaskEmbedder{}, 32)

	task, err := svc.Start(context.Background(), "op", "op", "ip")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitTaskStatus(t, svc, task.ID, model.KBTaskStatusSucceeded)
	if final.Total != 70 {
		t.Fatalf("total = %d, want 70", final.Total)
	}
	if final.Done != 70 {
		t.Fatalf("done = %d, want 70 (进度与 DB 一致)", final.Done)
	}
	if final.Failed != 0 || final.StartedAt == nil || final.FinishedAt == nil {
		t.Fatalf("task = %+v, want failed=0 + started/finished set", final)
	}
	// 全部条目已向量化
	un, _ := docs.CountUnembedded(context.Background())
	if un != 0 {
		t.Fatalf("unembedded left = %d, want 0", un)
	}
}

// TestKBTaskCancel 取消: pending 直接取消; running 批边界停止, 不断当前批 (A22)
func TestKBTaskCancel(t *testing.T) {
	ctx := context.Background()

	// running: 慢批 300ms, 100 条 batch 32 → 取消时停在某批边界
	{
		tasks, docs := newFakeKBTasksRepo(), newFakeKBDocRepo()
		seedUnembeddedDocs(docs, 100)
		svc := newTestKBTaskService(tasks, docs, &fakeKBTaskEmbedder{slow: 300 * time.Millisecond}, 32)
		task, err := svc.Start(ctx, "op", "op", "ip")
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		waitTaskStatus(t, svc, task.ID, model.KBTaskStatusRunning)
		if _, err := svc.Cancel(ctx, task.ID); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		final := waitTaskStatus(t, svc, task.ID, model.KBTaskStatusCancelled)
		// 批边界停止: done 为批大小倍数 (0/32/64/96), 且当前批未中断 (done ≥ 32, 首批必完成)
		if final.Done%32 != 0 {
			t.Fatalf("done = %d, 非批边界 (batch=32)", final.Done)
		}
		if final.Done < 32 {
			t.Fatalf("done = %d, 首批未完成即停止 (应不断当前批)", final.Done)
		}
	}
	// pending (无 worker 残留任务): 直接取消终态
	{
		tasks, docs := newFakeKBTasksRepo(), newFakeKBDocRepo()
		seedUnembeddedDocs(docs, 1)
		svc := newTestKBTaskService(tasks, docs, &fakeKBTaskEmbedder{}, 32)
		tasks.seed(&model.KBBackfillTask{Status: model.KBTaskStatusPending})
		var id string
		tasks.mu.Lock()
		for k := range tasks.items {
			id = k
		}
		tasks.mu.Unlock()
		task, err := svc.Cancel(ctx, id)
		if err != nil {
			t.Fatalf("cancel pending: %v", err)
		}
		if task.Status != model.KBTaskStatusCancelled {
			t.Fatalf("cancel pending: status = %s, want cancelled", task.Status)
		}
		// 终态任务不可再取消
		if _, err := svc.Cancel(ctx, id); err != apperrors.ErrKBTaskNotCancellable {
			t.Fatalf("cancel finished: err = %v, want ErrKBTaskNotCancellable", err)
		}
	}
}

// TestKBTaskRecoverOrphans 重启恢复: 残留 pending/running → failed (service restart), 可再启动 (A23)
func TestKBTaskRecoverOrphans(t *testing.T) {
	tasks, docs := newFakeKBTasksRepo(), newFakeKBDocRepo()
	seedUnembeddedDocs(docs, 3)
	svc := newTestKBTaskService(tasks, docs, &fakeKBTaskEmbedder{}, 32)
	ctx := context.Background()

	tasks.seed(&model.KBBackfillTask{Status: model.KBTaskStatusPending})
	tasks.seed(&model.KBBackfillTask{Status: model.KBTaskStatusRunning})
	n, err := svc.RecoverOrphans(ctx)
	if err != nil || n != 2 {
		t.Fatalf("recover: n=%d err=%v, want 2", n, err)
	}
	tasks.mu.Lock()
	for _, task := range tasks.items {
		if task.Status != model.KBTaskStatusFailed || task.LastError != "service restart" || task.FinishedAt == nil {
			t.Fatalf("orphan %s: %+v, want failed/service restart/finished", task.ID, task)
		}
	}
	tasks.mu.Unlock()
	// 恢复后可再启动 (单飞解除)
	if _, err := svc.Start(ctx, "op", "op", "ip"); err != nil {
		t.Fatalf("start after recover: %v", err)
	}
}

// TestKBTaskFail 系统性错误 (维度不匹配) → failed + last_error (不空转)
func TestKBTaskFail(t *testing.T) {
	tasks, docs := newFakeKBTasksRepo(), newFakeKBDocRepo()
	seedUnembeddedDocs(docs, 10)
	svc := newTestKBTaskService(tasks, docs, &fakeKBTaskEmbedder{failDim: true}, 32)

	task, err := svc.Start(context.Background(), "op", "op", "ip")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitTaskStatus(t, svc, task.ID, model.KBTaskStatusFailed)
	if !strings.Contains(final.LastError, "维度") {
		t.Fatalf("last_error = %q, want 含 维度", final.LastError)
	}
}

// TestKBTaskListRecent 最近任务列表 (上限 20, 新→旧)
func TestKBTaskListRecent(t *testing.T) {
	tasks, docs := newFakeKBTasksRepo(), newFakeKBDocRepo()
	seedUnembeddedDocs(docs, 1)
	svc := newTestKBTaskService(tasks, docs, &fakeKBTaskEmbedder{}, 32)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		task, err := svc.Start(ctx, "op", "op", "ip")
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		waitTaskStatus(t, svc, task.ID, model.KBTaskStatusSucceeded)
	}
	list, err := svc.ListRecent(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: len=%d err=%v, want 3", len(list), err)
	}
	for i := 1; i < len(list); i++ {
		if list[i].CreatedAt.After(list[i-1].CreatedAt) {
			t.Fatalf("list not in created_at desc order")
		}
	}
}