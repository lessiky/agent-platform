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

// ApprovalListFilter 审核请求列表过滤条件
type ApprovalListFilter struct {
	Status      string
	MCPServerID string
	ToolName    string
	AgentID     string
	Source      string
	From        *time.Time
	To          *time.Time
	Page        int
	PageSize    int
}

// ToolApprovalRepository 工具审核请求仓储
type ToolApprovalRepository interface {
	Create(ctx context.Context, a *model.ToolApproval) error
	Get(ctx context.Context, id string) (*model.ToolApproval, error)
	List(ctx context.Context, filter ApprovalListFilter) ([]model.ToolApproval, int64, error)
	// MarkDecided 状态机迁移: 仅 pending 可迁移 (乐观并发保护), 返回是否更新成功
	MarkDecided(ctx context.Context, id, status string, decidedBy *string, comment *string) (bool, error)
	UpdateResult(ctx context.Context, id string, result datatypes.JSON, executedAt *time.Time) error
	ListExpiredPending(ctx context.Context, before time.Time) ([]model.ToolApproval, error)
	CountPendingByMCP(ctx context.Context, mcpID string) (int64, error)
	DeleteByMCP(ctx context.Context, mcpID string) error
}

type toolApprovalRepository struct{}

func NewToolApprovalRepository() ToolApprovalRepository {
	return &toolApprovalRepository{}
}

func (r *toolApprovalRepository) Create(ctx context.Context, a *model.ToolApproval) error {
	return database.DB.WithContext(ctx).Create(a).Error
}

func (r *toolApprovalRepository) Get(ctx context.Context, id string) (*model.ToolApproval, error) {
	var a model.ToolApproval
	if err := database.DB.WithContext(ctx).First(&a, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *toolApprovalRepository) List(ctx context.Context, filter ApprovalListFilter) ([]model.ToolApproval, int64, error) {
	query := database.DB.WithContext(ctx).Model(&model.ToolApproval{})
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.MCPServerID != "" {
		query = query.Where("mcp_server_id = ?", filter.MCPServerID)
	}
	if filter.ToolName != "" {
		query = query.Where("tool_name = ?", filter.ToolName)
	}
	if filter.AgentID != "" {
		query = query.Where("agent_id = ?", filter.AgentID)
	}
	if filter.Source != "" {
		query = query.Where("source = ?", filter.Source)
	}
	if filter.From != nil {
		query = query.Where("requested_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("requested_at < ?", *filter.To)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	size := filter.PageSize
	if size < 1 || size > 100 {
		size = 20
	}

	var items []model.ToolApproval
	if err := query.Order("requested_at DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (r *toolApprovalRepository) MarkDecided(ctx context.Context, id, status string, decidedBy *string, comment *string) (bool, error) {
	res := database.DB.WithContext(ctx).Model(&model.ToolApproval{}).
		Where("id = ? AND status = ?", id, model.ApprovalStatusPending).
		Updates(map[string]interface{}{
			"status":     status,
			"decided_by": decidedBy,
			"decided_at": time.Now(),
			"comment":    comment,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *toolApprovalRepository) UpdateResult(ctx context.Context, id string, result datatypes.JSON, executedAt *time.Time) error {
	return database.DB.WithContext(ctx).Model(&model.ToolApproval{}).Where("id = ?", id).
		Updates(map[string]interface{}{"result": result, "executed_at": executedAt}).Error
}

func (r *toolApprovalRepository) ListExpiredPending(ctx context.Context, before time.Time) ([]model.ToolApproval, error) {
	var items []model.ToolApproval
	err := database.DB.WithContext(ctx).
		Where("status = ? AND expires_at < ?", model.ApprovalStatusPending, before).
		Order("expires_at ASC").Limit(200).Find(&items).Error
	return items, err
}

func (r *toolApprovalRepository) CountPendingByMCP(ctx context.Context, mcpID string) (int64, error) {
	var count int64
	err := database.DB.WithContext(ctx).Model(&model.ToolApproval{}).
		Where("mcp_server_id = ? AND status = ?", mcpID, model.ApprovalStatusPending).Count(&count).Error
	return count, err
}

func (r *toolApprovalRepository) DeleteByMCP(ctx context.Context, mcpID string) error {
	return database.DB.WithContext(ctx).Where("mcp_server_id = ?", mcpID).Delete(&model.ToolApproval{}).Error
}

// ApprovalSettingsRepository 审核全局配置仓储 (单例 id=1)
type ApprovalSettingsRepository interface {
	// Get 读取配置, 不存在时创建默认值
	Get(ctx context.Context) (*model.ApprovalSettings, error)
	Update(ctx context.Context, s *model.ApprovalSettings) error
}

type approvalSettingsRepository struct{}

func NewApprovalSettingsRepository() ApprovalSettingsRepository {
	return &approvalSettingsRepository{}
}

func (r *approvalSettingsRepository) Get(ctx context.Context) (*model.ApprovalSettings, error) {
	var s model.ApprovalSettings
	err := database.DB.WithContext(ctx).First(&s, "id = ?", "1").Error
	if err == gorm.ErrRecordNotFound {
		s = model.ApprovalSettings{ID: "1", DefaultTimeoutMinutes: 30, OnTimeout: model.ApprovalOnTimeoutReject}
		if cerr := database.DB.WithContext(ctx).Create(&s).Error; cerr != nil {
			return nil, cerr
		}
		return &s, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *approvalSettingsRepository) Update(ctx context.Context, s *model.ApprovalSettings) error {
	return database.DB.WithContext(ctx).Save(s).Error
}

// AuditLogRepository 审计日志仓储
type AuditLogRepository interface {
	Append(ctx context.Context, entry *model.AuditLog) error
	List(ctx context.Context, action string, page, size int) ([]model.AuditLog, int64, error)
}

type auditLogRepository struct{}

func NewAuditLogRepository() AuditLogRepository {
	return &auditLogRepository{}
}

func (r *auditLogRepository) Append(ctx context.Context, entry *model.AuditLog) error {
	return database.DB.WithContext(ctx).Create(entry).Error
}

func (r *auditLogRepository) List(ctx context.Context, action string, page, size int) ([]model.AuditLog, int64, error) {
	query := database.DB.WithContext(ctx).Model(&model.AuditLog{})
	if action != "" {
		query = query.Where("action = ?", action)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var items []model.AuditLog
	if err := query.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
