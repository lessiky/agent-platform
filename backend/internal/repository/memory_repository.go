package repository

import (
	"context"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// MemoryListFilter 记忆列表过滤 (M10.1)
type MemoryListFilter struct {
	AgentID  string
	UserID   string // scope=mine: 仅该用户 user 级 + Agent 级
	Kind     string
	Status   string
	Scope    string // mine (默认) / agent / all (all 需 admin, 由服务层校验)
	Page     int
	PageSize int
}

// MemoryRepository 长期记忆仓储 (M10.1)
type MemoryRepository interface {
	Create(ctx context.Context, m *model.Memory) error
	// Get 按 ID 查询 (限本 Agent 范围)
	Get(ctx context.Context, agentID, id string) (*model.Memory, error)
	List(ctx context.Context, f MemoryListFilter) ([]model.Memory, int64, error)
	// ListActiveForRetrieval 检索预筛: Agent 下全部活跃记忆 (Agent 级 + 各用户级),
	// 按 updated_at 降序, 限量 (用户维度的过滤与打分在 Go 侧完成)
	ListActiveForRetrieval(ctx context.Context, agentID string, limit int) ([]model.Memory, error)
	// ListActiveForScope 同 scope 活跃记忆 (userID 为 nil = Agent 级; M10.2 抽取去重 / 上限归档用)
	ListActiveForScope(ctx context.Context, agentID string, userID *string, limit int) ([]model.Memory, error)
	// Update 更新指定字段并返回最新记录
	Update(ctx context.Context, agentID, id string, fields map[string]interface{}) (*model.Memory, error)
	Delete(ctx context.Context, agentID, id string) error
	// DeleteByAgent 删除 Agent 全部记忆 (删除 Agent 级联)
	DeleteByAgent(ctx context.Context, agentID string) error
	// BumpAccess 异步更新本轮注入记忆的访问统计 (fire-and-forget)
	BumpAccess(ctx context.Context, ids []string) error
	// UpdateEmbedding 写入向量 (M10.3 语义检索; 异步写入/回填路径, 失败仅告警)
	UpdateEmbedding(ctx context.Context, agentID, id string, embedding datatypes.JSON) error
	// ListMissingEmbedding 回填: 活跃但无向量的记忆 (M10.3, id 升序分页)
	ListMissingEmbedding(ctx context.Context, limit, offset int) ([]model.Memory, error)
}

type memoryRepository struct{}

func NewMemoryRepository() MemoryRepository {
	return &memoryRepository{}
}

func (r *memoryRepository) Create(ctx context.Context, m *model.Memory) error {
	return database.DB.WithContext(ctx).Create(m).Error
}

func (r *memoryRepository) Get(ctx context.Context, agentID, id string) (*model.Memory, error) {
	var m model.Memory
	if err := database.DB.WithContext(ctx).First(&m, "id = ? AND agent_id = ?", id, agentID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

func (r *memoryRepository) List(ctx context.Context, f MemoryListFilter) ([]model.Memory, int64, error) {
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 20
	}
	query := database.DB.WithContext(ctx).Model(&model.Memory{}).Where("agent_id = ?", f.AgentID)
	if f.Kind != "" {
		query = query.Where("kind = ?", f.Kind)
	}
	if f.Status != "" {
		query = query.Where("status = ?", f.Status)
	}
	switch f.Scope {
	case model.MemoryScopeAgent:
		query = query.Where("user_id IS NULL")
	case model.MemoryScopeAll:
		// 不加属主条件 (admin 全量, 校验在服务层)
	default: // mine
		if f.UserID != "" {
			query = query.Where("user_id = ? OR user_id IS NULL", f.UserID)
		}
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.Memory
	err := query.Order("updated_at DESC, created_at DESC").
		Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).
		Find(&items).Error
	return items, total, err
}

func (r *memoryRepository) ListActiveForRetrieval(ctx context.Context, agentID string, limit int) ([]model.Memory, error) {
	if limit <= 0 {
		limit = 200
	}
	var items []model.Memory
	err := database.DB.WithContext(ctx).
		Where("agent_id = ? AND status = ?", agentID, model.MemoryStatusActive).
		Order("updated_at DESC").
		Limit(limit).
		Find(&items).Error
	return items, err
}

// ListActiveForScope 同 scope 活跃记忆 (M10.2 抽取去重 / 上限归档; userID 为 nil = Agent 级)
func (r *memoryRepository) ListActiveForScope(ctx context.Context, agentID string, userID *string, limit int) ([]model.Memory, error) {
	if limit <= 0 {
		limit = 500
	}
	query := database.DB.WithContext(ctx).
		Where("agent_id = ? AND status = ?", agentID, model.MemoryStatusActive)
	if userID == nil {
		query = query.Where("user_id IS NULL")
	} else {
		query = query.Where("user_id = ?", *userID)
	}
	var items []model.Memory
	err := query.Order("updated_at DESC").Limit(limit).Find(&items).Error
	return items, err
}

func (r *memoryRepository) Update(ctx context.Context, agentID, id string, fields map[string]interface{}) (*model.Memory, error) {
	res := database.DB.WithContext(ctx).Model(&model.Memory{}).
		Where("id = ? AND agent_id = ?", id, agentID).
		Updates(fields)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, errors.ErrNotFound
	}
	return r.Get(ctx, agentID, id)
}

func (r *memoryRepository) Delete(ctx context.Context, agentID, id string) error {
	res := database.DB.WithContext(ctx).Where("id = ? AND agent_id = ?", id, agentID).Delete(&model.Memory{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.ErrNotFound
	}
	return nil
}

func (r *memoryRepository) DeleteByAgent(ctx context.Context, agentID string) error {
	return database.DB.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&model.Memory{}).Error
}

func (r *memoryRepository) BumpAccess(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return database.DB.WithContext(ctx).Model(&model.Memory{}).
		Where("id IN ? AND status = ?", ids, model.MemoryStatusActive).
		Updates(map[string]interface{}{
			"access_count":     gorm.Expr("access_count + 1"),
			"last_accessed_at": gorm.Expr("NOW()"),
		}).Error
}

// UpdateEmbedding 写入向量 (M10.3); embedding 传 nil 表示清空
func (r *memoryRepository) UpdateEmbedding(ctx context.Context, agentID, id string, embedding datatypes.JSON) error {
	return database.DB.WithContext(ctx).Model(&model.Memory{}).
		Where("id = ? AND agent_id = ?", id, agentID).
		Update("embedding", embedding).Error
}

// ListMissingEmbedding 活跃但无向量的记忆 (M10.3 历史回填, id 升序分页)
func (r *memoryRepository) ListMissingEmbedding(ctx context.Context, limit, offset int) ([]model.Memory, error) {
	if limit <= 0 {
		limit = 64
	}
	var items []model.Memory
	err := database.DB.WithContext(ctx).
		Where("status = ? AND (embedding IS NULL OR embedding = ?)", model.MemoryStatusActive, "null").
		Order("id ASC").
		Offset(offset).Limit(limit).
		Find(&items).Error
	return items, err
}
