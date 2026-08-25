package repository

import (
	"context"
	"encoding/json"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// AgentExecutionRepository Agent 执行任务仓储 (/invoke 202 异步化)
// 所有状态迁移方法均带状态守卫: 终态 (success/failed/stalled) 不被后续写入覆盖
type AgentExecutionRepository interface {
	Create(ctx context.Context, e *model.AgentExecution) error
	Get(ctx context.Context, agentID, id string) (*model.AgentExecution, error)
	// GetByApprovalID 取出包含指定 approval_id 的等待审核任务 (jsonb 包含匹配)
	GetByApprovalID(ctx context.Context, approvalID string) (*model.AgentExecution, error)
	// SetStage 更新当前阶段 + 进度心跳 (仅 running 状态生效)
	SetStage(ctx context.Context, id, stage string) error
	// MarkWaitingApproval 标记等待审核 (写审核列表 + 中间结果 + 心跳, running / waiting 状态生效,
	// 后者用于审核续答轮再次命中审核门禁时重回等待状态)
	MarkWaitingApproval(ctx context.Context, id string, pendingApprovals, result datatypes.JSON, stage string) error
	// Finish 迁移到终态 (success/failed/stalled) 并写 finished_at; 已终态的行不覆盖
	Finish(ctx context.Context, id, status, errMsg string, result datatypes.JSON) error
	// FinishByApproval 审核决策后, 对包含指定 approval_id 的等待审核任务回填终态 (jsonb 包含匹配)
	FinishByApproval(ctx context.Context, approvalID, status, errMsg string, result datatypes.JSON) error
	// ListIdleRunning 列出心跳超时的 running 任务 (watchdog 卡死候选)
	ListIdleRunning(ctx context.Context, now time.Time, idleThreshold time.Duration) ([]model.AgentExecution, error)
	// ListExpired 列出 deadline 超时的 running 任务
	ListExpired(ctx context.Context, now time.Time) ([]model.AgentExecution, error)
	// ListStuckWaitingApproval 列出长期未恢复的等待审核任务 (进程崩溃等孤儿行兜底)
	ListStuckWaitingApproval(ctx context.Context, now time.Time, maxWait time.Duration) ([]model.AgentExecution, error)
	// ReconcileOrphans 进程启动时将遗留的 running 行置为 failed (等待审核保留, 决策后可恢复)
	ReconcileOrphans(ctx context.Context) (int64, error)
	// DeleteByAgent 删除 Agent 下全部执行任务 (删除 Agent 级联)
	DeleteByAgent(ctx context.Context, agentID string) error
}

type agentExecutionRepository struct{}

func NewAgentExecutionRepository() AgentExecutionRepository {
	return &agentExecutionRepository{}
}

func (r *agentExecutionRepository) Create(ctx context.Context, e *model.AgentExecution) error {
	return database.DB.WithContext(ctx).Create(e).Error
}

func (r *agentExecutionRepository) Get(ctx context.Context, agentID, id string) (*model.AgentExecution, error) {
	var e model.AgentExecution
	if err := database.DB.WithContext(ctx).Where("id = ? AND agent_id = ?", id, agentID).First(&e).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *agentExecutionRepository) GetByApprovalID(ctx context.Context, approvalID string) (*model.AgentExecution, error) {
	var e model.AgentExecution
	needle, _ := json.Marshal([]string{approvalID})
	err := database.DB.WithContext(ctx).
		Where("status = ? AND pending_approvals @> ?::jsonb", model.AgentExecutionStatusWaitingApproval, string(needle)).
		First(&e).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *agentExecutionRepository) SetStage(ctx context.Context, id, stage string) error {
	return database.DB.WithContext(ctx).Model(&model.AgentExecution{}).
		Where("id = ? AND status = ?", id, model.AgentExecutionStatusRunning).
		Updates(map[string]interface{}{
			"stage":            stage,
			"last_activity_at": time.Now(),
		}).Error
}

func (r *agentExecutionRepository) MarkWaitingApproval(ctx context.Context, id string, pendingApprovals, result datatypes.JSON, stage string) error {
	return database.DB.WithContext(ctx).Model(&model.AgentExecution{}).
		Where("id = ? AND status IN ?", id, []string{model.AgentExecutionStatusRunning, model.AgentExecutionStatusWaitingApproval}).
		Updates(map[string]interface{}{
			"status":            model.AgentExecutionStatusWaitingApproval,
			"stage":             stage,
			"pending_approvals": pendingApprovals,
			"result":            result,
			"last_activity_at":  time.Now(),
		}).Error
}

// finishUpdates 构建终态更新字段 (result 为空时不触碰, 保留中间结果)
func finishUpdates(status, errMsg string, result datatypes.JSON) map[string]interface{} {
	updates := map[string]interface{}{
		"status":      status,
		"finished_at": time.Now(),
	}
	if errMsg != "" {
		updates["error"] = errMsg
	}
	if len(result) > 0 {
		updates["result"] = result
	}
	return updates
}

func (r *agentExecutionRepository) Finish(ctx context.Context, id, status, errMsg string, result datatypes.JSON) error {
	return database.DB.WithContext(ctx).Model(&model.AgentExecution{}).
		Where("id = ? AND status IN ?", id, []string{model.AgentExecutionStatusRunning, model.AgentExecutionStatusWaitingApproval}).
		Updates(finishUpdates(status, errMsg, result)).Error
}

func (r *agentExecutionRepository) FinishByApproval(ctx context.Context, approvalID, status, errMsg string, result datatypes.JSON) error {
	needle, _ := json.Marshal([]string{approvalID})
	return database.DB.WithContext(ctx).Model(&model.AgentExecution{}).
		Where("status = ? AND pending_approvals @> ?::jsonb", model.AgentExecutionStatusWaitingApproval, string(needle)).
		Updates(finishUpdates(status, errMsg, result)).Error
}

func (r *agentExecutionRepository) ListIdleRunning(ctx context.Context, now time.Time, idleThreshold time.Duration) ([]model.AgentExecution, error) {
	var items []model.AgentExecution
	err := database.DB.WithContext(ctx).
		Where("status = ? AND last_activity_at < ?", model.AgentExecutionStatusRunning, now.Add(-idleThreshold)).
		Find(&items).Error
	return items, err
}

func (r *agentExecutionRepository) ListExpired(ctx context.Context, now time.Time) ([]model.AgentExecution, error) {
	var items []model.AgentExecution
	err := database.DB.WithContext(ctx).
		Where("status = ? AND deadline < ?", model.AgentExecutionStatusRunning, now).
		Find(&items).Error
	return items, err
}

func (r *agentExecutionRepository) ListStuckWaitingApproval(ctx context.Context, now time.Time, maxWait time.Duration) ([]model.AgentExecution, error) {
	var items []model.AgentExecution
	err := database.DB.WithContext(ctx).
		Where("status = ? AND updated_at < ?", model.AgentExecutionStatusWaitingApproval, now.Add(-maxWait)).
		Find(&items).Error
	return items, err
}

func (r *agentExecutionRepository) ReconcileOrphans(ctx context.Context) (int64, error) {
	res := database.DB.WithContext(ctx).Model(&model.AgentExecution{}).
		Where("status = ?", model.AgentExecutionStatusRunning).
		Updates(map[string]interface{}{
			"status":      model.AgentExecutionStatusFailed,
			"error":       "执行因服务重启中断",
			"finished_at": time.Now(),
		})
	return res.RowsAffected, res.Error
}

func (r *agentExecutionRepository) DeleteByAgent(ctx context.Context, agentID string) error {
	return database.DB.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&model.AgentExecution{}).Error
}
