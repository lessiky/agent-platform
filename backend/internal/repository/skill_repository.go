package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/gorm"
)

// SkillListFilter 技能列表过滤条件
type SkillListFilter struct {
	Keyword  string
	Tag      string
	Status   string
	Page     int
	PageSize int
}

// SkillFileMeta 技能包文件元数据 (不含内容)
type SkillFileMeta struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
}

// SkillRepository 技能包仓储
type SkillRepository interface {
	Create(ctx context.Context, skill *model.Skill) error
	Get(ctx context.Context, id string) (*model.Skill, error)
	GetByName(ctx context.Context, name string) (*model.Skill, error)
	Update(ctx context.Context, skill *model.Skill) error
	UpdateStatus(ctx context.Context, id, status string) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filter SkillListFilter) ([]model.Skill, int64, error)
	ListActiveByAgent(ctx context.Context, agentID string) ([]model.Skill, error)
	CountAgents(ctx context.Context, skillIDs []string) (map[string]int, error)
}

type skillRepository struct{}

func NewSkillRepository() SkillRepository {
	return &skillRepository{}
}

func (r *skillRepository) Create(ctx context.Context, skill *model.Skill) error {
	return database.DB.WithContext(ctx).Create(skill).Error
}

func (r *skillRepository) Get(ctx context.Context, id string) (*model.Skill, error) {
	var skill model.Skill
	if err := database.DB.WithContext(ctx).First(&skill, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		// ID 不是合法 UUID 时 Postgres 报类型转换错误, 统一按不存在处理
		if strings.Contains(err.Error(), "invalid input syntax for type uuid") {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &skill, nil
}

func (r *skillRepository) GetByName(ctx context.Context, name string) (*model.Skill, error) {
	var skill model.Skill
	if err := database.DB.WithContext(ctx).First(&skill, "name = ?", name).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &skill, nil
}

func (r *skillRepository) Update(ctx context.Context, skill *model.Skill) error {
	return database.DB.WithContext(ctx).Save(skill).Error
}

func (r *skillRepository) UpdateStatus(ctx context.Context, id, status string) error {
	return database.DB.WithContext(ctx).Model(&model.Skill{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": status}).Error
}

func (r *skillRepository) Delete(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Delete(&model.Skill{}, "id = ?", id).Error
}

func (r *skillRepository) List(ctx context.Context, filter SkillListFilter) ([]model.Skill, int64, error) {
	query := database.DB.WithContext(ctx).Model(&model.Skill{})

	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("name ILIKE ? OR description ILIKE ?", like, like)
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

	var skills []model.Skill
	if err := query.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&skills).Error; err != nil {
		return nil, 0, err
	}
	return skills, total, nil
}

// ListActiveByAgent 加载 Agent 关联的启用技能 (运行时注入用, 名称排序稳定)
func (r *skillRepository) ListActiveByAgent(ctx context.Context, agentID string) ([]model.Skill, error) {
	var skills []model.Skill
	err := database.DB.WithContext(ctx).
		Joins("JOIN skill_agent_bindings ON skill_agent_bindings.skill_id = skills.id").
		Where("skill_agent_bindings.agent_id = ?", agentID).
		Where("skills.status = ?", model.SkillStatusActive).
		Order("skills.name ASC").
		Find(&skills).Error
	return skills, err
}

// CountAgents 批量统计技能关联的 Agent 数 (列表页 "使用中" 标记)
func (r *skillRepository) CountAgents(ctx context.Context, skillIDs []string) (map[string]int, error) {
	result := make(map[string]int, len(skillIDs))
	if len(skillIDs) == 0 {
		return result, nil
	}
	type row struct {
		SkillID string `gorm:"column:skill_id"`
		Cnt     int    `gorm:"column:cnt"`
	}
	var rows []row
	err := database.DB.WithContext(ctx).
		Model(&model.SkillAgentBinding{}).
		Select("skill_id, count(*) as cnt").
		Where("skill_id IN ?", skillIDs).
		Group("skill_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.SkillID] = r.Cnt
	}
	return result, nil
}

// SkillFileRepository 技能包文件仓储
type SkillFileRepository interface {
	ListMetaBySkill(ctx context.Context, skillID string) ([]SkillFileMeta, error)
	GetByPath(ctx context.Context, skillID, filePath string) (*model.SkillFile, error)
	DeleteBySkill(ctx context.Context, skillID string) error
	CreateMany(ctx context.Context, skillID string, files []model.SkillFile) error
}

type skillFileRepository struct{}

func NewSkillFileRepository() SkillFileRepository {
	return &skillFileRepository{}
}

func (r *skillFileRepository) ListMetaBySkill(ctx context.Context, skillID string) ([]SkillFileMeta, error) {
	var metas []SkillFileMeta
	err := database.DB.WithContext(ctx).
		Model(&model.SkillFile{}).
		Select("path, size, sha256").
		Where("skill_id = ?", skillID).
		Order("path ASC").
		Scan(&metas).Error
	return metas, err
}

func (r *skillFileRepository) GetByPath(ctx context.Context, skillID, filePath string) (*model.SkillFile, error) {
	var file model.SkillFile
	if err := database.DB.WithContext(ctx).First(&file, "skill_id = ? AND path = ?", skillID, filePath).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &file, nil
}

func (r *skillFileRepository) DeleteBySkill(ctx context.Context, skillID string) error {
	return database.DB.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&model.SkillFile{}).Error
}

func (r *skillFileRepository) CreateMany(ctx context.Context, skillID string, files []model.SkillFile) error {
	for i := range files {
		files[i].SkillID = skillID
	}
	for i := 0; i < len(files); i += 50 {
		end := i + 50
		if end > len(files) {
			end = len(files)
		}
		if err := database.DB.WithContext(ctx).Create(files[i:end]).Error; err != nil {
			return err
		}
	}
	return nil
}

// SkillAgentBindingRepository 技能 <-> Agent 绑定仓储 (表结构与 MCPAgentBindingRepository 对齐)
type SkillAgentBindingRepository interface {
	Bind(ctx context.Context, skillID, agentID string, operatorID *string) error
	Unbind(ctx context.Context, skillID, agentID string) error
	ListBySkill(ctx context.Context, skillID string) ([]model.SkillAgentBinding, error)
	ListByAgent(ctx context.Context, agentID string) ([]model.SkillAgentBinding, error)
	DeleteBySkill(ctx context.Context, skillID string) error
	DeleteByAgent(ctx context.Context, agentID string) error
	AgentsOfSkill(ctx context.Context, skillID string) ([]AgentSkillBindingView, error)
}

// AgentSkillBindingView 技能关联的 Agent 视图 (技能详情页展示)
type AgentSkillBindingView struct {
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	BoundAt   time.Time `json:"bound_at"`
}

type skillAgentBindingRepository struct{}

func NewSkillAgentBindingRepository() SkillAgentBindingRepository {
	return &skillAgentBindingRepository{}
}

func (r *skillAgentBindingRepository) Bind(ctx context.Context, skillID, agentID string, operatorID *string) error {
	// 幂等: 已存在则忽略
	return database.DB.WithContext(ctx).Exec(`
        INSERT INTO skill_agent_bindings (id, skill_id, agent_id, created_by, created_at)
        SELECT gen_random_uuid(), ?, ?, ?, now()
        WHERE NOT EXISTS (
            SELECT 1 FROM skill_agent_bindings WHERE skill_id = ? AND agent_id = ?
        )
    `, skillID, agentID, operatorID, skillID, agentID).Error
}

func (r *skillAgentBindingRepository) Unbind(ctx context.Context, skillID, agentID string) error {
	return database.DB.WithContext(ctx).
		Where("skill_id = ? AND agent_id = ?", skillID, agentID).
		Delete(&model.SkillAgentBinding{}).Error
}

func (r *skillAgentBindingRepository) ListBySkill(ctx context.Context, skillID string) ([]model.SkillAgentBinding, error) {
	var bindings []model.SkillAgentBinding
	err := database.DB.WithContext(ctx).Where("skill_id = ?", skillID).Find(&bindings).Error
	return bindings, err
}

func (r *skillAgentBindingRepository) ListByAgent(ctx context.Context, agentID string) ([]model.SkillAgentBinding, error) {
	var bindings []model.SkillAgentBinding
	err := database.DB.WithContext(ctx).Where("agent_id = ?", agentID).Find(&bindings).Error
	return bindings, err
}

func (r *skillAgentBindingRepository) DeleteBySkill(ctx context.Context, skillID string) error {
	return database.DB.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&model.SkillAgentBinding{}).Error
}

func (r *skillAgentBindingRepository) DeleteByAgent(ctx context.Context, agentID string) error {
	return database.DB.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&model.SkillAgentBinding{}).Error
}

// AgentsOfSkill 技能关联的 Agent 列表 (含 Agent 名称)
func (r *skillAgentBindingRepository) AgentsOfSkill(ctx context.Context, skillID string) ([]AgentSkillBindingView, error) {
	var views []AgentSkillBindingView
	err := database.DB.WithContext(ctx).
		Table("skill_agent_bindings b").
		Select("b.agent_id AS agent_id, a.name AS agent_name, b.created_at AS bound_at").
		Joins("LEFT JOIN agents a ON a.id = b.agent_id").
		Where("b.skill_id = ?", skillID).
		Order("b.created_at ASC").
		Scan(&views).Error
	return views, err
}

// SkillUsage 技能使用统计 (列表 / 详情页展示)
type SkillUsage struct {
	AgentCount   int        `json:"agent_count"`
	LoadCount30d int64      `json:"load_count_30d"`
	LastUsedAt   *time.Time `json:"last_used_at"`
}

// GetUsage 使用统计: 关联数 + 近 30 天技能加载次数 (execution_meta.skill_calls JSONB 聚合)
func GetSkillUsage(ctx context.Context, skill *model.Skill) (SkillUsage, error) {
	usage := SkillUsage{}

	var cnt int64
	if err := database.DB.WithContext(ctx).Model(&model.SkillAgentBinding{}).
		Where("skill_id = ?", skill.ID).Count(&cnt).Error; err != nil {
		return usage, err
	}
	usage.AgentCount = int(cnt)

	containment := `{"skill_calls":[{"skill_name":"` + skill.Name + `"}]}`
	if err := database.DB.WithContext(ctx).
		Model(&model.ChatMessage{}).
		Where("created_at >= ? AND execution_meta @> ?::jsonb",
			time.Now().AddDate(0, 0, -30), containment).
		Count(&usage.LoadCount30d).Error; err != nil {
		return usage, err
	}

	var last sql.NullTime
	err := database.DB.WithContext(ctx).
		Model(&model.ChatMessage{}).
		Where("created_at >= ? AND execution_meta @> ?::jsonb",
			time.Now().AddDate(0, 0, -30), containment).
		Select("MAX(created_at)").Scan(&last).Error
	if err != nil {
		return usage, err
	}
	if last.Valid {
		usage.LastUsedAt = &last.Time
	}
	return usage, nil
}
