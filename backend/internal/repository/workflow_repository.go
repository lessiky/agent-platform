package repository

import (
	"context"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// WorkflowListFilter 工作流列表过滤
type WorkflowListFilter struct {
	Status string
	Page   int
	Size   int
}

// ExecutionListFilter 执行记录列表过滤
type ExecutionListFilter struct {
	WorkflowID string
	Status     string
	Trigger    string
	Page       int
	PageSize   int
}

// WorkflowRepository 工作流定义仓储
type WorkflowRepository interface {
	Create(ctx context.Context, w *model.Workflow) error
	Get(ctx context.Context, id string) (*model.Workflow, error)
	GetByName(ctx context.Context, name string) (*model.Workflow, error)
	GetByWebhookToken(ctx context.Context, token string) (*model.Workflow, error)
	List(ctx context.Context, filter WorkflowListFilter) ([]model.Workflow, int64, error)
	Update(ctx context.Context, w *model.Workflow) error
	Delete(ctx context.Context, id string) error
	// ListScheduled 列出已激活且开启定时调度的工作流 (调度器重建用)
	ListScheduled(ctx context.Context) ([]model.Workflow, error)
	HasActiveExecutions(ctx context.Context, workflowID string) (bool, error)
}

type workflowRepository struct{}

func NewWorkflowRepository() WorkflowRepository {
	return &workflowRepository{}
}

func (r *workflowRepository) Create(ctx context.Context, w *model.Workflow) error {
	return database.DB.WithContext(ctx).Create(w).Error
}

func (r *workflowRepository) Get(ctx context.Context, id string) (*model.Workflow, error) {
	var w model.Workflow
	if err := database.DB.WithContext(ctx).First(&w, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &w, nil
}

func (r *workflowRepository) GetByName(ctx context.Context, name string) (*model.Workflow, error) {
	var w model.Workflow
	err := database.DB.WithContext(ctx).Where("name = ?", name).First(&w).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *workflowRepository) GetByWebhookToken(ctx context.Context, token string) (*model.Workflow, error) {
	var w model.Workflow
	err := database.DB.WithContext(ctx).Where("webhook_token = ?", token).First(&w).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *workflowRepository) List(ctx context.Context, filter WorkflowListFilter) ([]model.Workflow, int64, error) {
	query := database.DB.WithContext(ctx).Model(&model.Workflow{})
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := filter.Page, filter.Size
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	var items []model.Workflow
	if err := query.Order("updated_at DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *workflowRepository) Update(ctx context.Context, w *model.Workflow) error {
	return database.DB.WithContext(ctx).Save(w).Error
}

func (r *workflowRepository) Delete(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Delete(&model.Workflow{}, "id = ?", id).Error
}

func (r *workflowRepository) ListScheduled(ctx context.Context) ([]model.Workflow, error) {
	var items []model.Workflow
	err := database.DB.WithContext(ctx).
		Where("status = ? AND schedule_enabled = ?", model.WorkflowStatusActive, true).
		Find(&items).Error
	return items, err
}

func (r *workflowRepository) HasActiveExecutions(ctx context.Context, workflowID string) (bool, error) {
	var count int64
	err := database.DB.WithContext(ctx).Model(&model.WorkflowExecution{}).
		Where("workflow_id = ? AND status IN ?", workflowID,
			[]string{model.ExecutionStatusRunning, model.ExecutionStatusWaitingApproval}).
		Count(&count).Error
	return count > 0, err
}

// WorkflowVersionRepository 工作流版本快照仓储
type WorkflowVersionRepository interface {
	Create(ctx context.Context, v *model.WorkflowVersion) error
	ListByWorkflow(ctx context.Context, workflowID string) ([]model.WorkflowVersion, error)
}

type workflowVersionRepository struct{}

func NewWorkflowVersionRepository() WorkflowVersionRepository {
	return &workflowVersionRepository{}
}

func (r *workflowVersionRepository) Create(ctx context.Context, v *model.WorkflowVersion) error {
	return database.DB.WithContext(ctx).Create(v).Error
}

func (r *workflowVersionRepository) ListByWorkflow(ctx context.Context, workflowID string) ([]model.WorkflowVersion, error) {
	var items []model.WorkflowVersion
	err := database.DB.WithContext(ctx).
		Where("workflow_id = ?", workflowID).
		Order("version DESC").
		Find(&items).Error
	return items, err
}

// WorkflowExecutionRepository 工作流执行记录仓储
type WorkflowExecutionRepository interface {
	Create(ctx context.Context, e *model.WorkflowExecution) error
	Get(ctx context.Context, id string) (*model.WorkflowExecution, error)
	Update(ctx context.Context, e *model.WorkflowExecution) error
	// MarkFinished 终态迁移保护: 仅 running/waiting_approval 可迁移到终态
	MarkFinished(ctx context.Context, id, status, errMsg string, output datatypes.JSON, finishedAt time.Time) (bool, error)
	List(ctx context.Context, filter ExecutionListFilter) ([]model.WorkflowExecution, int64, error)
	// ListActive 列出未达终态的执行 (启动对账用)
	ListActive(ctx context.Context) ([]model.WorkflowExecution, error)
	CountsByStatus(ctx context.Context) (map[string]int64, error)
	Recent(ctx context.Context, limit int) ([]model.WorkflowExecution, error)
}

type workflowExecutionRepository struct{}

func NewWorkflowExecutionRepository() WorkflowExecutionRepository {
	return &workflowExecutionRepository{}
}

func (r *workflowExecutionRepository) Create(ctx context.Context, e *model.WorkflowExecution) error {
	return database.DB.WithContext(ctx).Create(e).Error
}

func (r *workflowExecutionRepository) Get(ctx context.Context, id string) (*model.WorkflowExecution, error) {
	var e model.WorkflowExecution
	if err := database.DB.WithContext(ctx).First(&e, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *workflowExecutionRepository) Update(ctx context.Context, e *model.WorkflowExecution) error {
	return database.DB.WithContext(ctx).Save(e).Error
}

func (r *workflowExecutionRepository) MarkFinished(ctx context.Context, id, status, errMsg string, output datatypes.JSON, finishedAt time.Time) (bool, error) {
	fields := map[string]interface{}{
		"status":      status,
		"error":       errMsg,
		"finished_at": finishedAt,
	}
	if output != nil {
		fields["output"] = output
	}
	res := database.DB.WithContext(ctx).Model(&model.WorkflowExecution{}).
		Where("id = ? AND status IN ?", id, []string{model.ExecutionStatusRunning, model.ExecutionStatusWaitingApproval}).
		Updates(fields)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *workflowExecutionRepository) List(ctx context.Context, filter ExecutionListFilter) ([]model.WorkflowExecution, int64, error) {
	query := database.DB.WithContext(ctx).Model(&model.WorkflowExecution{})
	if filter.WorkflowID != "" {
		query = query.Where("workflow_id = ?", filter.WorkflowID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Trigger != "" {
		query = query.Where("trigger_type = ?", filter.Trigger)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, size := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	var items []model.WorkflowExecution
	if err := query.Order("started_at DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *workflowExecutionRepository) ListActive(ctx context.Context) ([]model.WorkflowExecution, error) {
	var items []model.WorkflowExecution
	err := database.DB.WithContext(ctx).
		Where("status IN ?", []string{model.ExecutionStatusRunning, model.ExecutionStatusWaitingApproval}).
		Find(&items).Error
	return items, err
}

func (r *workflowExecutionRepository) CountsByStatus(ctx context.Context) (map[string]int64, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := database.DB.WithContext(ctx).Model(&model.WorkflowExecution{}).
		Select("status, count(*) as count").
		Group("status").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int64)
	for _, r := range rows {
		result[r.Status] = r.Count
	}
	return result, nil
}

func (r *workflowExecutionRepository) Recent(ctx context.Context, limit int) ([]model.WorkflowExecution, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var items []model.WorkflowExecution
	err := database.DB.WithContext(ctx).
		Order("started_at DESC").
		Limit(limit).
		Find(&items).Error
	return items, err
}

// WorkflowNodeExecutionRepository 节点执行记录仓储
type WorkflowNodeExecutionRepository interface {
	CreateBatch(ctx context.Context, items []model.WorkflowNodeExecution) error
	ListByExecution(ctx context.Context, executionID string) ([]model.WorkflowNodeExecution, error)
	Get(ctx context.Context, id string) (*model.WorkflowNodeExecution, error)
	Update(ctx context.Context, n *model.WorkflowNodeExecution) error
	ListWaitingByExecution(ctx context.Context, executionID string) ([]model.WorkflowNodeExecution, error)
	// MarkAll 将执行中处于非终态的节点批量置为目标状态 (取消/对账用)
	MarkAll(ctx context.Context, executionID, status string) error
}

type workflowNodeExecutionRepository struct{}

func NewWorkflowNodeExecutionRepository() WorkflowNodeExecutionRepository {
	return &workflowNodeExecutionRepository{}
}

func (r *workflowNodeExecutionRepository) CreateBatch(ctx context.Context, items []model.WorkflowNodeExecution) error {
	if len(items) == 0 {
		return nil
	}
	return database.DB.WithContext(ctx).Create(&items).Error
}

func (r *workflowNodeExecutionRepository) ListByExecution(ctx context.Context, executionID string) ([]model.WorkflowNodeExecution, error) {
	var items []model.WorkflowNodeExecution
	err := database.DB.WithContext(ctx).
		Where("execution_id = ?", executionID).
		Find(&items).Error
	return items, err
}

func (r *workflowNodeExecutionRepository) Get(ctx context.Context, id string) (*model.WorkflowNodeExecution, error) {
	var n model.WorkflowNodeExecution
	if err := database.DB.WithContext(ctx).First(&n, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &n, nil
}

func (r *workflowNodeExecutionRepository) Update(ctx context.Context, n *model.WorkflowNodeExecution) error {
	return database.DB.WithContext(ctx).Save(n).Error
}

func (r *workflowNodeExecutionRepository) ListWaitingByExecution(ctx context.Context, executionID string) ([]model.WorkflowNodeExecution, error) {
	var items []model.WorkflowNodeExecution
	err := database.DB.WithContext(ctx).
		Where("execution_id = ? AND status = ?", executionID, model.NodeStatusWaitingApproval).
		Find(&items).Error
	return items, err
}

func (r *workflowNodeExecutionRepository) MarkAll(ctx context.Context, executionID, status string) error {
	return database.DB.WithContext(ctx).Model(&model.WorkflowNodeExecution{}).
		Where("execution_id = ? AND status IN ?", executionID, []string{
			model.NodeStatusPending, model.NodeStatusRunning, model.NodeStatusWaitingApproval,
		}).
		Update("status", status).Error
}
