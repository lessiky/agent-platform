package repository

import (
	"context"
	"strings"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentListFilter Agent 列表查询条件
type AgentListFilter struct {
	Keyword  string // 名称/描述模糊搜索
	Status   string
	Page     int
	PageSize int
}

// AgentLogFilter Agent 日志查询条件
type AgentLogFilter struct {
	AgentID  string
	Level    string
	Keyword  string
	Since    time.Time // 起始时间 (零值表示不限)
	Page     int
	PageSize int
}

// ==================== Agent ====================

type AgentRepository interface {
	Create(ctx context.Context, agent *model.Agent) error
	GetByID(ctx context.Context, id string) (*model.Agent, error)
	GetByName(ctx context.Context, name string) (*model.Agent, error)
	Update(ctx context.Context, agent *model.Agent) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filter AgentListFilter) ([]*model.Agent, int64, error)
	CountByStatus(ctx context.Context) (map[string]int64, error)
}

type agentRepository struct {
	db *gorm.DB
}

func NewAgentRepository() AgentRepository {
	return &agentRepository{db: database.DB}
}

func (r *agentRepository) Create(ctx context.Context, agent *model.Agent) error {
	return r.db.WithContext(ctx).Create(agent).Error
}

func (r *agentRepository) GetByID(ctx context.Context, id string) (*model.Agent, error) {
	var agent model.Agent
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&agent).Error; err != nil {
		return nil, err
	}
	return &agent, nil
}

func (r *agentRepository) GetByName(ctx context.Context, name string) (*model.Agent, error) {
	var agent model.Agent
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&agent).Error; err != nil {
		return nil, err
	}
	return &agent, nil
}

func (r *agentRepository) Update(ctx context.Context, agent *model.Agent) error {
	return r.db.WithContext(ctx).Save(agent).Error
}

func (r *agentRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Agent{}).Error
}

func (r *agentRepository) List(ctx context.Context, filter AgentListFilter) ([]*model.Agent, int64, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > 100 {
		filter.PageSize = 20
	}

	query := r.db.WithContext(ctx).Model(&model.Agent{})
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("name ILIKE ? OR description ILIKE ?", like, like)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var agents []*model.Agent
	err := query.Order("created_at DESC").
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&agents).Error
	if err != nil {
		return nil, 0, err
	}
	return agents, total, nil
}

func (r *agentRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	var rows []struct {
		Status string
		Count  int64
	}
	err := r.db.WithContext(ctx).Model(&model.Agent{}).
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

// ==================== AgentVersion ====================

type AgentVersionRepository interface {
	Create(ctx context.Context, version *model.AgentVersion) error
	ListByAgent(ctx context.Context, agentID string) ([]*model.AgentVersion, error)
	Get(ctx context.Context, agentID string, version int) (*model.AgentVersion, error)
}

type agentVersionRepository struct {
	db *gorm.DB
}

func NewAgentVersionRepository() AgentVersionRepository {
	return &agentVersionRepository{db: database.DB}
}

func (r *agentVersionRepository) Create(ctx context.Context, version *model.AgentVersion) error {
	return r.db.WithContext(ctx).Create(version).Error
}

func (r *agentVersionRepository) ListByAgent(ctx context.Context, agentID string) ([]*model.AgentVersion, error) {
	var versions []*model.AgentVersion
	err := r.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("version DESC").
		Find(&versions).Error
	return versions, err
}

func (r *agentVersionRepository) Get(ctx context.Context, agentID string, version int) (*model.AgentVersion, error) {
	var v model.AgentVersion
	if err := r.db.WithContext(ctx).
		Where("agent_id = ? AND version = ?", agentID, version).
		First(&v).Error; err != nil {
		return nil, err
	}
	return &v, nil
}

// ==================== AgentInstance ====================

type AgentInstanceRepository interface {
	Create(ctx context.Context, instance *model.AgentInstance) error
	GetByAgent(ctx context.Context, agentID string) (*model.AgentInstance, error)
	SetRunning(ctx context.Context, agentID string, at time.Time) error
	SetStopped(ctx context.Context, agentID string, at time.Time) error
	SetError(ctx context.Context, agentID string) error
	TouchHeartbeat(ctx context.Context, agentID string, at time.Time) error
	SoftDelete(ctx context.Context, agentID string) error
	ReconcileOrphans(ctx context.Context) ([]*model.AgentInstance, error)
}

type agentInstanceRepository struct {
	db *gorm.DB
}

func NewAgentInstanceRepository() AgentInstanceRepository {
	return &agentInstanceRepository{db: database.DB}
}

func (r *agentInstanceRepository) Create(ctx context.Context, instance *model.AgentInstance) error {
	return r.db.WithContext(ctx).Create(instance).Error
}

func (r *agentInstanceRepository) GetByAgent(ctx context.Context, agentID string) (*model.AgentInstance, error) {
	var instance model.AgentInstance
	if err := r.db.WithContext(ctx).Where("agent_id = ?", agentID).First(&instance).Error; err != nil {
		return nil, err
	}
	return &instance, nil
}

func (r *agentInstanceRepository) SetRunning(ctx context.Context, agentID string, at time.Time) error {
	return r.db.WithContext(ctx).Model(&model.AgentInstance{}).
		Where("agent_id = ?", agentID).
		Updates(map[string]interface{}{
			"status":         model.InstanceStatusRunning,
			"started_at":     at,
			"last_heartbeat": at,
			"stopped_at":     nil,
		}).Error
}

func (r *agentInstanceRepository) SetStopped(ctx context.Context, agentID string, at time.Time) error {
	return r.db.WithContext(ctx).Model(&model.AgentInstance{}).
		Where("agent_id = ?", agentID).
		Updates(map[string]interface{}{
			"status":     model.InstanceStatusStopped,
			"stopped_at": at,
		}).Error
}

func (r *agentInstanceRepository) SetError(ctx context.Context, agentID string) error {
	return r.db.WithContext(ctx).Model(&model.AgentInstance{}).
		Where("agent_id = ?", agentID).
		Updates(map[string]interface{}{
			"status":     model.InstanceStatusError,
			"stopped_at": time.Now(),
		}).Error
}

func (r *agentInstanceRepository) TouchHeartbeat(ctx context.Context, agentID string, at time.Time) error {
	return r.db.WithContext(ctx).Model(&model.AgentInstance{}).
		Where("agent_id = ?", agentID).
		Update("last_heartbeat", at).Error
}

func (r *agentInstanceRepository) SoftDelete(ctx context.Context, agentID string) error {
	return r.db.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&model.AgentInstance{}).Error
}

// ReconcileOrphans 服务重启时将处于活动状态的实例标记为 error, 返回受影响的实例
func (r *agentInstanceRepository) ReconcileOrphans(ctx context.Context) ([]*model.AgentInstance, error) {
	var orphans []*model.AgentInstance
	err := r.db.WithContext(ctx).
		Where("status IN ?", []string{
			model.InstanceStatusPending,
			model.InstanceStatusRunning,
			model.InstanceStatusStopping,
		}).
		Find(&orphans).Error
	if err != nil {
		return nil, err
	}
	if len(orphans) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(orphans))
	for _, o := range orphans {
		ids = append(ids, o.ID)
	}
	if err := r.db.WithContext(ctx).Model(&model.AgentInstance{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"status":     model.InstanceStatusError,
			"stopped_at": time.Now(),
		}).Error; err != nil {
		return nil, err
	}
	return orphans, nil
}

// ==================== AgentLog ====================

type AgentLogRepository interface {
	Append(ctx context.Context, entries []*model.AgentLog) error
	List(ctx context.Context, filter AgentLogFilter) ([]*model.AgentLog, int64, error)
	Trim(ctx context.Context, agentID string, keep int) error
}

type agentLogRepository struct {
	db *gorm.DB
}

func NewAgentLogRepository() AgentLogRepository {
	return &agentLogRepository{db: database.DB}
}

func (r *agentLogRepository) Append(ctx context.Context, entries []*model.AgentLog) error {
	if len(entries) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(entries, 100).Error
}

func (r *agentLogRepository) List(ctx context.Context, filter AgentLogFilter) ([]*model.AgentLog, int64, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > 500 {
		filter.PageSize = 100
	}

	query := r.db.WithContext(ctx).Model(&model.AgentLog{}).
		Where("agent_id = ?", filter.AgentID)
	if filter.Level != "" {
		query = query.Where("level = ?", filter.Level)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		query = query.Where("message ILIKE ?", "%"+keyword+"%")
	}
	if !filter.Since.IsZero() {
		query = query.Where("created_at >= ?", filter.Since)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []*model.AgentLog
	err := query.Order("created_at DESC, id DESC").
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// Trim 只保留每个 Agent 最近 keep 条日志, 防止表无限膨胀
func (r *agentLogRepository) Trim(ctx context.Context, agentID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("agent_id = ? AND id NOT IN (SELECT id FROM agent_logs WHERE agent_id = ? ORDER BY created_at DESC, id DESC LIMIT ?)",
			agentID, agentID, keep).
		Delete(&model.AgentLog{}).Error
}

// ==================== AgentAPIKey ====================

type AgentAPIKeyRepository interface {
	Create(ctx context.Context, key *model.AgentAPIKey) error
	ListByAgent(ctx context.Context, agentID string) ([]*model.AgentAPIKey, error)
	GetByIDAndAgent(ctx context.Context, agentID, id string) (*model.AgentAPIKey, error)
	GetByHash(ctx context.Context, hash string) (*model.AgentAPIKey, error)
	Update(ctx context.Context, key *model.AgentAPIKey) error
	Delete(ctx context.Context, id string) error
}

type agentAPIKeyRepository struct {
	db *gorm.DB
}

func NewAgentAPIKeyRepository() AgentAPIKeyRepository {
	return &agentAPIKeyRepository{db: database.DB}
}

func (r *agentAPIKeyRepository) Create(ctx context.Context, key *model.AgentAPIKey) error {
	return r.db.WithContext(ctx).Create(key).Error
}

func (r *agentAPIKeyRepository) ListByAgent(ctx context.Context, agentID string) ([]*model.AgentAPIKey, error) {
	var keys []*model.AgentAPIKey
	err := r.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("created_at DESC").
		Find(&keys).Error
	return keys, err
}

func (r *agentAPIKeyRepository) GetByIDAndAgent(ctx context.Context, agentID, id string) (*model.AgentAPIKey, error) {
	var key model.AgentAPIKey
	if err := r.db.WithContext(ctx).
		Where("id = ? AND agent_id = ?", id, agentID).
		First(&key).Error; err != nil {
		return nil, err
	}
	return &key, nil
}

func (r *agentAPIKeyRepository) GetByHash(ctx context.Context, hash string) (*model.AgentAPIKey, error) {
	var key model.AgentAPIKey
	if err := r.db.WithContext(ctx).
		Where("key_hash = ?", hash).
		First(&key).Error; err != nil {
		return nil, err
	}
	return &key, nil
}

func (r *agentAPIKeyRepository) Update(ctx context.Context, key *model.AgentAPIKey) error {
	return r.db.WithContext(ctx).Save(key).Error
}

func (r *agentAPIKeyRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.AgentAPIKey{}, "id = ?", id).Error
}

// ==================== AgentCallStat ====================

type AgentCallStatRepository interface {
	Increment(ctx context.Context, agentID string, statDate time.Time, calls, errs, tokens, latencyMs int64) error
	SumRange(ctx context.Context, agentID string, from, to time.Time) (calls, errs, tokens, latencyMs int64, err error)
	DailySeries(ctx context.Context, agentID string, from, to time.Time) ([]*model.AgentCallStat, error)
}

type agentCallStatRepository struct {
	db *gorm.DB
}

func NewAgentCallStatRepository() AgentCallStatRepository {
	return &agentCallStatRepository{db: database.DB}
}

func (r *agentCallStatRepository) Increment(ctx context.Context, agentID string, statDate time.Time, calls, errs, tokens, latencyMs int64) error {
	stat := &model.AgentCallStat{
		AgentID:      agentID,
		StatDate:     statDate,
		Calls:        calls,
		Errors:       errs,
		TotalTokens:  tokens,
		TotalLatency: latencyMs,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "agent_id"}, {Name: "stat_date"}},
		DoUpdates: clause.Set{
			{Column: clause.Column{Name: "calls"}, Value: clause.Expr{SQL: `"agent_call_stats".calls + EXCLUDED.calls`}},
			{Column: clause.Column{Name: "errors"}, Value: clause.Expr{SQL: `"agent_call_stats".errors + EXCLUDED.errors`}},
			{Column: clause.Column{Name: "total_tokens"}, Value: clause.Expr{SQL: `"agent_call_stats".total_tokens + EXCLUDED.total_tokens`}},
			{Column: clause.Column{Name: "total_latency_ms"}, Value: clause.Expr{SQL: `"agent_call_stats".total_latency_ms + EXCLUDED.total_latency_ms`}},
			{Column: clause.Column{Name: "updated_at"}, Value: clause.Expr{SQL: "NOW()"}},
		},
	}).Create(stat).Error
}

func (r *agentCallStatRepository) SumRange(ctx context.Context, agentID string, from, to time.Time) (calls, errs, tokens, latencyMs int64, err error) {
	var res struct {
		Calls        int64
		Errors       int64
		TotalTokens  int64
		TotalLatency int64 `gorm:"column:total_latency_ms"`
	}
	err = r.db.WithContext(ctx).Model(&model.AgentCallStat{}).
		Select("COALESCE(sum(calls),0) as calls, COALESCE(sum(errors),0) as errors, "+
			"COALESCE(sum(total_tokens),0) as total_tokens, COALESCE(sum(total_latency_ms),0) as total_latency_ms").
		Where("agent_id = ? AND stat_date >= ? AND stat_date < ?", agentID, from, to).
		Scan(&res).Error
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return res.Calls, res.Errors, res.TotalTokens, res.TotalLatency, nil
}

func (r *agentCallStatRepository) DailySeries(ctx context.Context, agentID string, from, to time.Time) ([]*model.AgentCallStat, error) {
	var stats []*model.AgentCallStat
	err := r.db.WithContext(ctx).
		Where("agent_id = ? AND stat_date >= ? AND stat_date < ?", agentID, from, to).
		Order("stat_date ASC").
		Find(&stats).Error
	return stats, err
}
