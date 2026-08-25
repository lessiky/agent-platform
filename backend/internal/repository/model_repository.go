package repository

import (
	"context"
	"strings"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/gorm"
)

// ModelListFilter 模型模板列表过滤条件
type ModelListFilter struct {
	Keyword  string
	Provider string
	Status   string
	Tag      string
	Page     int
	PageSize int
}

// ModelTemplateRepository 模型模板仓储
type ModelTemplateRepository interface {
	Create(ctx context.Context, t *model.ModelTemplate) error
	Get(ctx context.Context, id string) (*model.ModelTemplate, error)
	GetByName(ctx context.Context, name string) (*model.ModelTemplate, error)
	Update(ctx context.Context, t *model.ModelTemplate) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filter ModelListFilter) ([]model.ModelTemplate, int64, error)
	ListForRoute(ctx context.Context) ([]model.ModelTemplate, error)
	UpdateHealth(ctx context.Context, id string, status string, latencyMs *int, lastErr string) error
}

type modelTemplateRepository struct{}

func NewModelTemplateRepository() ModelTemplateRepository {
	return &modelTemplateRepository{}
}

func (r *modelTemplateRepository) Create(ctx context.Context, t *model.ModelTemplate) error {
	return database.DB.WithContext(ctx).Create(t).Error
}

func (r *modelTemplateRepository) Get(ctx context.Context, id string) (*model.ModelTemplate, error) {
	var t model.ModelTemplate
	if err := database.DB.WithContext(ctx).First(&t, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *modelTemplateRepository) GetByName(ctx context.Context, name string) (*model.ModelTemplate, error) {
	var t model.ModelTemplate
	if err := database.DB.WithContext(ctx).First(&t, "name = ?", name).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *modelTemplateRepository) Update(ctx context.Context, t *model.ModelTemplate) error {
	return database.DB.WithContext(ctx).Save(t).Error
}

func (r *modelTemplateRepository) Delete(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Delete(&model.ModelTemplate{}, "id = ?", id).Error
}

func (r *modelTemplateRepository) List(ctx context.Context, filter ModelListFilter) ([]model.ModelTemplate, int64, error) {
	query := database.DB.WithContext(ctx).Model(&model.ModelTemplate{})

	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("name ILIKE ? OR model ILIKE ? OR endpoint ILIKE ?", like, like, like)
	}
	if filter.Provider != "" {
		query = query.Where("provider = ?", filter.Provider)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Tag != "" {
		query = query.Where("tags @> ?::jsonb", `["`+filter.Tag+`"]`)
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

	var items []model.ModelTemplate
	if err := query.Order("priority ASC, created_at DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListForRoute 路由候选: 全部模板按优先级排序 (业务层再过滤状态/配额)
func (r *modelTemplateRepository) ListForRoute(ctx context.Context) ([]model.ModelTemplate, error) {
	var items []model.ModelTemplate
	if err := database.DB.WithContext(ctx).Order("priority ASC, created_at ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *modelTemplateRepository) UpdateHealth(ctx context.Context, id string, status string, latencyMs *int, lastErr string) error {
	updates := map[string]interface{}{
		"status":            status,
		"health_last_check": time.Now(),
		"health_latency_ms": latencyMs,
		"last_error":        lastErr,
	}
	return database.DB.WithContext(ctx).Model(&model.ModelTemplate{}).Where("id = ?", id).Updates(updates).Error
}

// ModelQuotaRepository 模型配额仓储
type ModelQuotaRepository interface {
	GetByModel(ctx context.Context, modelID string) (*model.ModelQuota, error) // 不存在返回 nil
	Upsert(ctx context.Context, quota *model.ModelQuota) error
	UpdateCounters(ctx context.Context, modelID string, dailyUsed, monthlyUsed, dailyTokenUsed, monthlyTokenUsed int, resetDailyAt, resetMonthlyAt time.Time) error
	List(ctx context.Context) ([]model.ModelQuota, error)
	DeleteByModel(ctx context.Context, modelID string) error
}

type modelQuotaRepository struct{}

func NewModelQuotaRepository() ModelQuotaRepository {
	return &modelQuotaRepository{}
}

func (r *modelQuotaRepository) GetByModel(ctx context.Context, modelID string) (*model.ModelQuota, error) {
	var quota model.ModelQuota
	if err := database.DB.WithContext(ctx).First(&quota, "model_id = ?", modelID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &quota, nil
}

func (r *modelQuotaRepository) Upsert(ctx context.Context, quota *model.ModelQuota) error {
	return database.DB.WithContext(ctx).Save(quota).Error
}

func (r *modelQuotaRepository) UpdateCounters(ctx context.Context, modelID string, dailyUsed, monthlyUsed, dailyTokenUsed, monthlyTokenUsed int, resetDailyAt, resetMonthlyAt time.Time) error {
	updates := map[string]interface{}{
		"daily_used":         dailyUsed,
		"monthly_used":       monthlyUsed,
		"daily_token_used":   dailyTokenUsed,
		"monthly_token_used": monthlyTokenUsed,
		"reset_daily_at":     resetDailyAt,
		"reset_monthly_at":   resetMonthlyAt,
		"updated_at":         time.Now(),
	}
	return database.DB.WithContext(ctx).Model(&model.ModelQuota{}).Where("model_id = ?", modelID).Updates(updates).Error
}

func (r *modelQuotaRepository) List(ctx context.Context) ([]model.ModelQuota, error) {
	var quotas []model.ModelQuota
	if err := database.DB.WithContext(ctx).Order("updated_at DESC").Find(&quotas).Error; err != nil {
		return nil, err
	}
	return quotas, nil
}

func (r *modelQuotaRepository) DeleteByModel(ctx context.Context, modelID string) error {
	return database.DB.WithContext(ctx).Where("model_id = ?", modelID).Delete(&model.ModelQuota{}).Error
}

// ModelUsageLogRepository 模型调用用量日志仓储
type ModelUsageLogRepository interface {
	Append(ctx context.Context, entry *model.ModelUsageLog) error
	List(ctx context.Context, modelID string, limit int) ([]model.ModelUsageLog, error)
	Trim(ctx context.Context, modelID string, keep int) error
	DeleteByModel(ctx context.Context, modelID string) error
	AggregateLast24h(ctx context.Context) (map[string]UsageAggregate, error)
}

// UsageAggregate 近 24h 用量聚合
type UsageAggregate struct {
	ModelID string `gorm:"column:model_id"`
	Calls   int64  `gorm:"column:calls"`
	Tokens  int64  `gorm:"column:tokens"`
	Errors  int64  `gorm:"column:errs"`
}

type modelUsageLogRepository struct{}

func NewModelUsageLogRepository() ModelUsageLogRepository {
	return &modelUsageLogRepository{}
}

func (r *modelUsageLogRepository) Append(ctx context.Context, entry *model.ModelUsageLog) error {
	return database.DB.WithContext(ctx).Create(entry).Error
}

func (r *modelUsageLogRepository) List(ctx context.Context, modelID string, limit int) ([]model.ModelUsageLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var logs []model.ModelUsageLog
	if err := database.DB.WithContext(ctx).
		Where("model_id = ?", modelID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

func (r *modelUsageLogRepository) Trim(ctx context.Context, modelID string, keep int) error {
	return database.DB.WithContext(ctx).Exec(
		`DELETE FROM model_usage_logs WHERE model_id = ? AND id NOT IN (
            SELECT id FROM model_usage_logs WHERE model_id = ? ORDER BY created_at DESC LIMIT ?
         )`, modelID, modelID, keep,
	).Error
}

func (r *modelUsageLogRepository) DeleteByModel(ctx context.Context, modelID string) error {
	return database.DB.WithContext(ctx).Where("model_id = ?", modelID).Delete(&model.ModelUsageLog{}).Error
}
func (r *modelUsageLogRepository) AggregateLast24h(ctx context.Context) (map[string]UsageAggregate, error) {
	var rows []UsageAggregate
	err := database.DB.WithContext(ctx).Raw(
		`SELECT model_id,
                COUNT(*) AS calls,
                COALESCE(SUM(tokens), 0) AS tokens,
                COALESCE(SUM(CASE WHEN ok THEN 0 ELSE 1 END), 0) AS errs
         FROM model_usage_logs
         WHERE created_at > now() - interval '24 hours'
         GROUP BY model_id`,
	).Scan(&rows).Error
	result := make(map[string]UsageAggregate, len(rows))
	for _, row := range rows {
		result[row.ModelID] = row
	}
	return result, err
}

// ModelHealthLogRepository 模型健康检查历史仓储
type ModelHealthLogRepository interface {
	Append(ctx context.Context, entry *model.ModelHealthLog) error
	List(ctx context.Context, modelID string, limit int) ([]model.ModelHealthLog, error)
	Trim(ctx context.Context, modelID string, keep int) error
	DeleteByModel(ctx context.Context, modelID string) error
}

type modelHealthLogRepository struct{}

func NewModelHealthLogRepository() ModelHealthLogRepository {
	return &modelHealthLogRepository{}
}

func (r *modelHealthLogRepository) Append(ctx context.Context, entry *model.ModelHealthLog) error {
	return database.DB.WithContext(ctx).Create(entry).Error
}

func (r *modelHealthLogRepository) List(ctx context.Context, modelID string, limit int) ([]model.ModelHealthLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var logs []model.ModelHealthLog
	if err := database.DB.WithContext(ctx).
		Where("model_id = ?", modelID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

func (r *modelHealthLogRepository) Trim(ctx context.Context, modelID string, keep int) error {
	return database.DB.WithContext(ctx).Exec(
		`DELETE FROM model_health_logs WHERE model_id = ? AND id NOT IN (
            SELECT id FROM model_health_logs WHERE model_id = ? ORDER BY created_at DESC LIMIT ?
         )`, modelID, modelID, keep,
	).Error
}

func (r *modelHealthLogRepository) DeleteByModel(ctx context.Context, modelID string) error {
	return database.DB.WithContext(ctx).Where("model_id = ?", modelID).Delete(&model.ModelHealthLog{}).Error
}
