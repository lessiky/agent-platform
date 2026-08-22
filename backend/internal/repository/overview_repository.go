package repository

import (
	"context"

	"agent-platform/internal/database"
	domain "agent-platform/internal/model"

	"gorm.io/gorm"
)

// OverviewRepository 概览看板 (基本情况) 统计查询
type OverviewRepository interface {
	AgentStatusCounts(ctx context.Context) (map[string]int64, error)
	MCPStatusCounts(ctx context.Context) (map[string]int64, error)
	MCPToolsTotal(ctx context.Context) (int64, error)
	ModelStatusCounts(ctx context.Context) (map[string]int64, error)
	WorkflowStatusCounts(ctx context.Context) (map[string]int64, error)
	ApprovalStatusCounts(ctx context.Context) (map[string]int64, error)
	SkillStatusCounts(ctx context.Context) (map[string]int64, error)
}

type overviewRepository struct {
	db *gorm.DB
}

func NewOverviewRepository() OverviewRepository {
	return &overviewRepository{db: database.DB}
}

func (r *overviewRepository) AgentStatusCounts(ctx context.Context) (map[string]int64, error) {
	return statusCounts(ctx, r.db, &domain.Agent{})
}

func (r *overviewRepository) MCPStatusCounts(ctx context.Context) (map[string]int64, error) {
	return statusCounts(ctx, r.db, &domain.MCPServer{})
}

func (r *overviewRepository) MCPToolsTotal(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&domain.MCPServer{}).
		Select("COALESCE(SUM(jsonb_array_length(tools)), 0)").
		Scan(&total).Error
	if err != nil {
		return 0, err
	}
	return total, nil
}

func (r *overviewRepository) ModelStatusCounts(ctx context.Context) (map[string]int64, error) {
	return statusCounts(ctx, r.db, &domain.ModelTemplate{})
}

func (r *overviewRepository) WorkflowStatusCounts(ctx context.Context) (map[string]int64, error) {
	return statusCounts(ctx, r.db, &domain.Workflow{})
}

func (r *overviewRepository) ApprovalStatusCounts(ctx context.Context) (map[string]int64, error) {
	return statusCounts(ctx, r.db, &domain.ToolApproval{})
}

func (r *overviewRepository) SkillStatusCounts(ctx context.Context) (map[string]int64, error) {
	return statusCounts(ctx, r.db, &domain.Skill{})
}

// statusCounts 按 status 分组计数 (带 DeletedAt 的模型自动过滤软删除)
func statusCounts(ctx context.Context, db *gorm.DB, m interface{}) (map[string]int64, error) {
	var rows []struct {
		Status string
		Count  int64
	}
	err := db.WithContext(ctx).Model(m).
		Select("status, count(*) as count").
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, row := range rows {
		result[row.Status] = row.Count
	}
	return result, nil
}
