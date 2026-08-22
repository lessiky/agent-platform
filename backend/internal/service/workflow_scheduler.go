package service

import (
    "context"
    "encoding/json"
    "log"
    "sync"
    "time"

    "agent-platform/internal/model"

    "github.com/robfig/cron/v3"
)

// WorkflowScheduler 工作流定时调度 (M5, PRD 2.4.2 定时调度)
//
// 说明: Phase 1 后端为单进程架构, 定时调度采用进程内 cron 触发 (robfig/cron/v3),
// 调度配置持久化在 workflows.schedule 字段, 重启后自动重建; 不依赖 RabbitMQ。
type WorkflowScheduler struct {
    cron      *cron.Cron
    svc       WorkflowService
    workflows interface {
        ListScheduled(ctx context.Context) ([]model.Workflow, error)
    }

    mu      sync.Mutex
    started bool
    stopCh  chan struct{}
    doneCh  chan struct{}
}

// NewWorkflowScheduler 创建调度器; 时区固定 Asia/Shanghai (调度项内 timezone 字段预留)
func NewWorkflowScheduler(svc WorkflowService, workflows interface {
    ListScheduled(ctx context.Context) ([]model.Workflow, error)
}) *WorkflowScheduler {
    loc, err := time.LoadLocation("Asia/Shanghai")
    if err != nil {
        loc = time.FixedZone("CST", 8*3600)
    }
    return &WorkflowScheduler{
        cron:      cron.New(cron.WithLocation(loc)),
        svc:       svc,
        workflows: workflows,
    }
}

// Start 启动调度器 (幂等)
func (s *WorkflowScheduler) Start() {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.started {
        return
    }
    s.stopCh = make(chan struct{})
    s.doneCh = make(chan struct{})
    s.started = true
    s.cron.Start()
    go s.reloadLoop()
    log.Println("workflow scheduler started")
}

// Stop 停止调度器
func (s *WorkflowScheduler) Stop() {
    s.mu.Lock()
    if !s.started {
        s.mu.Unlock()
        return
    }
    close(s.stopCh)
    s.cron.Stop()
    done := s.doneCh
    s.started = false
    s.mu.Unlock()

    select {
    case <-done:
    case <-time.After(2 * time.Second):
        log.Println("workflow scheduler did not stop in time")
    }
}

// ReloadSchedules 重建全部 cron 条目 (工作流变更后调用, 线程安全)
func (s *WorkflowScheduler) ReloadSchedules(ctx context.Context) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if !s.started {
        return // 调度器未启动时不注册, Start 后会全量重建
    }
    for _, entry := range s.cron.Entries() {
        s.cron.Remove(entry.ID)
    }
    workflows, err := s.workflows.ListScheduled(ctx)
    if err != nil {
        log.Printf("workflow scheduler: list scheduled failed: %v", err)
        return
    }
    registered := 0
    for i := range workflows {
        workflow := workflows[i]
        schedule, err := parseSchedule(workflow.Schedule)
        if err != nil || schedule.Cron == "" {
            continue
        }
        entryID := workflow.ID
        job := func() {
            log.Printf("workflow scheduler: cron trigger workflow=%s (%s)", entryID, schedule.Cron)
            _, triggerErr := s.svc.Trigger(context.Background(), entryID, schedule.Input, model.WorkflowTriggerCron, nil)
            if triggerErr != nil {
                log.Printf("workflow scheduler: trigger failed workflow=%s: %v", entryID, triggerErr)
            }
        }
        if _, err := s.cron.AddFunc(schedule.Cron, job); err != nil {
            log.Printf("workflow scheduler: add cron failed workflow=%s cron=%s: %v", entryID, schedule.Cron, err)
        } else {
            registered++
        }
    }
    log.Printf("workflow scheduler: %d schedule(s) registered", registered)
}

// parseSchedule 解析 workflows.schedule JSONB
func parseSchedule(raw []byte) (*ScheduleConfig, error) {
    schedule := &ScheduleConfig{}
    if len(raw) == 0 {
        return schedule, nil
    }
    if err := json.Unmarshal(raw, schedule); err != nil {
        return schedule, err
    }
    return schedule, nil
}

// reloadLoop 定期自愈: 每 5 分钟对账一次 (防止运行期间 DB 被外部直接修改导致漏注册)
func (s *WorkflowScheduler) reloadLoop() {
    defer close(s.doneCh)
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    // 启动后立即全量重建一次
    s.ReloadSchedules(context.Background())
    for {
        select {
        case <-s.stopCh:
            return
        case <-ticker.C:
            s.ReloadSchedules(context.Background())
        }
    }
}