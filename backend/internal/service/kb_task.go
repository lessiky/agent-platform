package service

// kb_task.go — M11.5 向量回填后台任务 (计划事项 3)
//
// 设计 (参照 M10 异步抽取模式 + tools/memory-embed-backfill):
//   - 单飞: 平台同时至多一个 pending/running 任务 (DB 任务表判定, 跨重启健壮);
//     重复启动 → 409 返回运行中任务;
//   - 执行: 进程内单 worker goroutine (零队列基建依赖), 批次 32 (复用 EmbedBatch),
//     批边界持久化进度 (done/failed 原子自增);
//   - 取消: 批边界协作取消 (当前批完成后停止, 不断当前批); pending 任务直接标 cancelled;
//   - 重启恢复: 服务启动时残留 pending/running → failed (last_error = "service restart"), 可再启动;
//   - 并发写: 向量写幂等 (SetEmbeddingIfNull 仅 NULL 时写), 与写入路径 VectorizeAsync 无竞争。

import (
	"context"
	"log"
	"sync"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	apperrors "agent-platform/pkg/errors"
)

// KBTaskService 向量回填任务服务 (M11.5)
type KBTaskService interface {
	// Start 启动回填任务; 已有进行中任务时返回该任务 + ErrKBTaskActive (409)
	Start(ctx context.Context, operatorID, operatorName, ip string) (*model.KBBackfillTask, error)
	// Get 任务详情 + 进度
	Get(ctx context.Context, id string) (*model.KBBackfillTask, error)
	// ListRecent 最近任务列表 (上限 20, created_at 降序)
	ListRecent(ctx context.Context) ([]model.KBBackfillTask, error)
	// Cancel 取消 (仅 pending/running; running 在批边界协作停止)
	Cancel(ctx context.Context, id string) (*model.KBBackfillTask, error)
	// RecoverOrphans 启动恢复: 残留 pending/running 任务标 failed (service restart)
	RecoverOrphans(ctx context.Context) (int64, error)
}

type kbTaskService struct {
	tasks  repository.KBBackfillTaskRepository
	writer *KBVectorWriter
	// cancels taskID -> 取消信号 (close 表示请求取消; worker 在批边界检查)
	mu      sync.Mutex
	cancels map[string]chan struct{}
}

// NewKBTaskService 创建回填任务服务 (writer 的 batch 即任务批大小)
func NewKBTaskService(tasks repository.KBBackfillTaskRepository, writer *KBVectorWriter) KBTaskService {
	return &kbTaskService{
		tasks:   tasks,
		writer:  writer,
		cancels: make(map[string]chan struct{}),
	}
}

// Start 启动任务 (单飞: DB active 任务存在即 409)
func (s *kbTaskService) Start(ctx context.Context, operatorID, operatorName, ip string) (*model.KBBackfillTask, error) {
	if s.writer == nil || !s.writer.Enabled() {
		return nil, apperrors.NewValidationError("未配置知识库向量模型, 请先在平台设置页配置后启动回填")
	}
	active, err := s.tasks.FindActive(ctx)
	if err != nil {
		return nil, err
	}
	if active != nil {
		return active, apperrors.ErrKBTaskActive
	}
	task := &model.KBBackfillTask{
		Status:    model.KBTaskStatusPending,
		CreatedBy: strPtr(operatorID),
	}
	if err := s.tasks.Create(ctx, task); err != nil {
		return nil, apperrors.Wrap(err, "failed to create backfill task")
	}
	cancelCh := make(chan struct{})
	s.mu.Lock()
	s.cancels[task.ID] = cancelCh
	s.mu.Unlock()
	// 进程内单飞 worker; 跨重启的单飞由任务表 active 状态保证
	go s.run(task.ID, cancelCh)
	log.Printf("kb task: started id=%s operator=%s", task.ID, operatorID)
	return task, nil
}

// Get 任务详情
func (s *kbTaskService) Get(ctx context.Context, id string) (*model.KBBackfillTask, error) {
	return s.tasks.Get(ctx, id)
}

// ListRecent 最近任务 (20 条)
func (s *kbTaskService) ListRecent(ctx context.Context) ([]model.KBBackfillTask, error) {
	return s.tasks.ListRecent(ctx, 20)
}

// Cancel 取消: pending 直接终态; running 发取消信号 (worker 批边界停止)
func (s *kbTaskService) Cancel(ctx context.Context, id string) (*model.KBBackfillTask, error) {
	task, err := s.tasks.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status != model.KBTaskStatusPending && task.Status != model.KBTaskStatusRunning {
		return nil, apperrors.ErrKBTaskNotCancellable
	}
	s.mu.Lock()
	ch, ok := s.cancels[task.ID]
	s.mu.Unlock()
	if ok {
		close(ch)
	}
	if task.Status == model.KBTaskStatusPending {
		// 尚未进入执行 (worker 启动时也会检查取消信号, 双保险)
		if err := s.tasks.Finish(ctx, id, model.KBTaskStatusCancelled, "", time.Now()); err != nil {
			return nil, err
		}
	}
	return s.tasks.Get(ctx, id)
}

// RecoverOrphans 启动恢复 (A23): 上次进程残留的 pending/running → failed (service restart)
func (s *kbTaskService) RecoverOrphans(ctx context.Context) (int64, error) {
	n, err := s.tasks.MarkOrphansFailed(ctx, "service restart")
	if err != nil {
		return 0, err
	}
	if n > 0 {
		log.Printf("kb task: recovered %d orphan task(s) -> failed (service restart)", n)
	}
	return n, nil
}

// cancelled 是否已请求取消
func (s *kbTaskService) cancelled(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.cancels[taskID]
	if !ok {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// dropCancel 清理取消信号
func (s *kbTaskService) dropCancel(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cancels, taskID)
}

// finish 写终态 (幂等: 后写覆盖, 取消与完成竞争时以先到者语义为准)
func (s *kbTaskService) finish(ctx context.Context, taskID, status, lastError string) {
	if err := s.tasks.Finish(ctx, taskID, status, lastError, time.Now()); err != nil {
		log.Printf("kb task %s: finish %s failed: %v", taskID, status, err)
		return
	}
	log.Printf("kb task: finished id=%s status=%s", taskID, status)
}

// run 单 worker 执行循环: pending -> running -> (succeeded | failed | cancelled)
func (s *kbTaskService) run(taskID string, cancelCh <-chan struct{}) {
	ctx := context.Background() // 长任务不受请求 ctx 约束; 取消经 cancelCh 协作完成
	defer s.dropCancel(taskID)

	if err := s.tasks.SetRunning(ctx, taskID, time.Now()); err != nil {
		log.Printf("kb task %s: mark running failed: %v", taskID, err)
		return
	}
	// pending 期间已请求取消 (Cancel 抢先写入 cancelled) -> 立即停止
	if s.cancelled(taskID) {
		s.finish(ctx, taskID, model.KBTaskStatusCancelled, "")
		return
	}
	total, err := s.writer.CountUnembedded(ctx)
	if err != nil {
		s.finish(ctx, taskID, model.KBTaskStatusFailed, "统计未向量化条目失败: "+err.Error())
		return
	}
	if err := s.tasks.SetTotal(ctx, taskID, int(total)); err != nil {
		log.Printf("kb task %s: set total failed: %v", taskID, err)
	}
	if total == 0 {
		s.finish(ctx, taskID, model.KBTaskStatusSucceeded, "")
		return
	}
	for {
		// 批边界协作取消 (不断当前批)
		if s.cancelled(taskID) {
			s.finish(ctx, taskID, model.KBTaskStatusCancelled, "")
			return
		}
		processed, _, failed, more, fatal := s.writer.BackfillBatch(ctx)
		if fatal != nil {
			// 系统性错误 (维度不匹配/未配置): 终止任务明确报错, 避免空转消耗配额
			_ = s.tasks.AddProgress(ctx, taskID, 0, failed)
			s.finish(ctx, taskID, model.KBTaskStatusFailed, fatal.Error())
			return
		}
		if processed > 0 {
			if err := s.tasks.AddProgress(ctx, taskID, processed, failed); err != nil {
				log.Printf("kb task %s: update progress failed: %v", taskID, err)
			}
		}
		if !more {
			// 本批为空: 复核运行期间新出现 (新建条目/异步向量化失败) 的未向量化条目
			if left, cerr := s.writer.CountUnembedded(ctx); cerr == nil && left > 0 {
				if err := s.tasks.AddTotal(ctx, taskID, int(left)); err == nil {
					continue
				}
			}
			s.finish(ctx, taskID, model.KBTaskStatusSucceeded, "")
			return
		}
		// 批边界限速: 避免连续批量调用占满模型配额
		time.Sleep(kbTaskBatchInterval)
	}
}

// kbTaskBatchInterval 批间间隔 (限速 + 批边界取消的响应粒度)
const kbTaskBatchInterval = 200 * time.Millisecond