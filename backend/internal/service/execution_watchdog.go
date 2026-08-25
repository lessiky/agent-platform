package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
)

// ExecutionWatchdogScanInterval 执行任务 watchdog 扫描间隔
const ExecutionWatchdogScanInterval = 15 * time.Second

// ExecutionWaitingApprovalMaxWait 等待审核任务安全网阈值:
// 等待审核任务超过该时长仍未完成续答 (进程崩溃 / 决策钩子丢失等) 时标记 failed
const ExecutionWaitingApprovalMaxWait = 24 * time.Hour

// ExecutionWatchdog Agent 执行任务 watchdog: 让外部方明确区分 "执行中" 与 "卡死",
// 周期扫描非终态执行任务:
//   - running 且心跳超时 (无进度超过 idle 阈值) → 标记 stalled, 取消进行中的执行上下文
//   - running 且 deadline 超时 → 标记 failed (整体预算耗尽)
//   - waiting_approval 且长期未恢复 → 标记 failed (孤儿行兜底)
type ExecutionWatchdog struct {
	svc        ChatService
	executions repository.AgentExecutionRepository
	interval   time.Duration
	idle       time.Duration

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewExecutionWatchdog 创建 watchdog; idle = 无心跳卡死阈值 (由 ChatService 依据单步上限推导)
func NewExecutionWatchdog(svc ChatService, executions repository.AgentExecutionRepository, idle, interval time.Duration) *ExecutionWatchdog {
	if interval <= 0 {
		interval = ExecutionWatchdogScanInterval
	}
	if idle <= 0 {
		idle = 3 * time.Minute
	}
	return &ExecutionWatchdog{
		svc:        svc,
		executions: executions,
		interval:   interval,
		idle:       idle,
		stopCh:     make(chan struct{}),
	}
}

// Start 启动后台扫描
func (w *ExecutionWatchdog) Start() {
	go w.loop()
}

// Stop 停止扫描 (幂等)
func (w *ExecutionWatchdog) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}

func (w *ExecutionWatchdog) loop() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.scan()
		}
	}
}

// scan 执行一轮扫描: 卡死 / deadline 超时 / 等待审核兜底
func (w *ExecutionWatchdog) scan() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now()

	// 1) 心跳超时 (仅 running; waiting_approval 在等人工, 不算卡死)
	idleRows, err := w.executions.ListIdleRunning(ctx, now, w.idle)
	if err != nil {
		log.Printf("execution watchdog: list idle running failed: %v", err)
	}
	for i := range idleRows {
		e := idleRows[i]
		w.svc.CancelExecution(e.ID)
		if fErr := w.executions.Finish(ctx, e.ID, model.AgentExecutionStatusStalled,
			fmt.Sprintf("执行卡死: 超过 %s 无进度心跳 (stage=%s), watchdog 已取消", w.idle, e.Stage), nil); fErr != nil {
			log.Printf("execution watchdog: mark stalled failed id=%s: %v", e.ID, fErr)
		}
		log.Printf("execution watchdog: execution stalled id=%s stage=%s last_activity=%s", e.ID, e.Stage, e.LastActivityAt.Format(time.RFC3339))
	}

	// 2) 整体 deadline 超时 (running)
	expiredRows, err := w.executions.ListExpired(ctx, now)
	if err != nil {
		log.Printf("execution watchdog: list expired failed: %v", err)
	}
	for i := range expiredRows {
		e := expiredRows[i]
		w.svc.CancelExecution(e.ID)
		if fErr := w.executions.Finish(ctx, e.ID, model.AgentExecutionStatusFailed,
			fmt.Sprintf("执行超时: 整体 deadline 已过期 (stage=%s)", e.Stage), nil); fErr != nil {
			log.Printf("execution watchdog: mark expired failed id=%s: %v", e.ID, fErr)
		}
	}

	// 3) 等待审核长期未恢复兜底
	stuckRows, err := w.executions.ListStuckWaitingApproval(ctx, now, ExecutionWaitingApprovalMaxWait)
	if err != nil {
		log.Printf("execution watchdog: list stuck waiting approval failed: %v", err)
	}
	for i := range stuckRows {
		e := stuckRows[i]
		if fErr := w.executions.Finish(ctx, e.ID, model.AgentExecutionStatusFailed,
			"执行失败: 等待审核长期未完成续答, watchdog 标记失败", nil); fErr != nil {
			log.Printf("execution watchdog: mark stuck waiting failed id=%s: %v", e.ID, fErr)
		}
	}
}
