package repository

import (
	"context"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/gorm"
)

// ChatSessionRepository 对话会话仓储
type ChatSessionRepository interface {
	Create(ctx context.Context, s *model.ChatSession) error
	Get(ctx context.Context, id string) (*model.ChatSession, error)
	ListByAgent(ctx context.Context, agentID string, page, pageSize int) ([]model.ChatSession, int64, error)
	// UpdateTitle 修改会话标题 (手动重命名)
	UpdateTitle(ctx context.Context, id, title string) error
	// UpdateSummary 更新会话滚动摘要 (M10.2)
	UpdateSummary(ctx context.Context, id, summary string) error
	TouchLastMessage(ctx context.Context, id string) error
	DeleteByAgent(ctx context.Context, agentID string) error
	// DeleteByAgentCascade 级联删除 Agent 全部会话及其消息 (M2.5)
	DeleteByAgentCascade(ctx context.Context, agentID string) error
	DeleteCascade(ctx context.Context, id string) error
}

// ChatMessageRepository 会话消息仓储
type ChatMessageRepository interface {
	Append(ctx context.Context, msgs []*model.ChatMessage) error
	ListBySession(ctx context.Context, sessionID string, limit int) ([]model.ChatMessage, error)
	// CountChat 统计会话内 user/assistant 消息数 (M10.2 滚动摘要触发检查)
	CountChat(ctx context.Context, sessionID string) (int64, error)
	// ListForSummary 会话最旧的 user/assistant 消息 (时间升序, 排除最近 skipNewest 条; M10.2 滚动摘要压缩输入)
	ListForSummary(ctx context.Context, sessionID string, skipNewest int) ([]model.ChatMessage, error)
	DeleteBySession(ctx context.Context, sessionID string) error
	// GetByExecutionID 按 execution_id 查最新一条 assistant 消息 (审核决策续答回查)
	GetByExecutionID(ctx context.Context, executionID string) (*model.ChatMessage, error)
}

type chatSessionRepository struct{}

func NewChatSessionRepository() ChatSessionRepository {
	return &chatSessionRepository{}
}

func (r *chatSessionRepository) Create(ctx context.Context, s *model.ChatSession) error {
	return database.DB.WithContext(ctx).Create(s).Error
}

func (r *chatSessionRepository) Get(ctx context.Context, id string) (*model.ChatSession, error) {
	var s model.ChatSession
	if err := database.DB.WithContext(ctx).First(&s, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *chatSessionRepository) ListByAgent(ctx context.Context, agentID string, page, pageSize int) ([]model.ChatSession, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	var total int64
	query := database.DB.WithContext(ctx).Model(&model.ChatSession{}).Where("agent_id = ?", agentID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.ChatSession
	err := query.Order("last_message_at DESC, created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&items).Error
	return items, total, err
}

func (r *chatSessionRepository) UpdateTitle(ctx context.Context, id, title string) error {
	return database.DB.WithContext(ctx).Model(&model.ChatSession{}).
		Where("id = ?", id).Update("title", title).Error
}

// UpdateSummary 更新会话滚动摘要 (M10.2, 仅写 summary 列)
func (r *chatSessionRepository) UpdateSummary(ctx context.Context, id, summary string) error {
	return database.DB.WithContext(ctx).Model(&model.ChatSession{}).
		Where("id = ?", id).Update("summary", summary).Error
}

func (r *chatSessionRepository) TouchLastMessage(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Model(&model.ChatSession{}).
		Where("id = ?", id).Update("last_message_at", gorm.Expr("NOW()")).Error
}

func (r *chatSessionRepository) DeleteByAgent(ctx context.Context, agentID string) error {
	return database.DB.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&model.ChatSession{}).Error
}
func (r *chatSessionRepository) DeleteByAgentCascade(ctx context.Context, agentID string) error {
	tx := database.DB.WithContext(ctx).Begin()
	var ids []string
	if err := tx.Model(&model.ChatSession{}).Where("agent_id = ?", agentID).Pluck("id", &ids).Error; err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(ids) > 0 {
		if err := tx.Where("session_id IN ?", ids).Delete(&model.ChatMessage{}).Error; err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Where("agent_id = ?", agentID).Delete(&model.ChatSession{}).Error; err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit().Error
}
func (r *chatSessionRepository) DeleteCascade(ctx context.Context, id string) error {
	tx := database.DB.WithContext(ctx).Begin()
	if err := tx.Where("session_id = ?", id).Delete(&model.ChatMessage{}).Error; err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Where("id = ?", id).Delete(&model.ChatSession{}).Error; err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

type chatMessageRepository struct{}

func NewChatMessageRepository() ChatMessageRepository {
	return &chatMessageRepository{}
}

func (r *chatMessageRepository) Append(ctx context.Context, msgs []*model.ChatMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	// Batch rows in a single INSERT share one CreatedAt, which would break
	// time-order in ListBySession; assign strictly increasing timestamps.
	now := time.Now()
	for i, m := range msgs {
		if m.CreatedAt.IsZero() {
			m.CreatedAt = now.Add(time.Duration(i) * time.Microsecond)
		}
	}
	return database.DB.WithContext(ctx).Create(&msgs).Error
}

// ListBySession 返回会话最近 limit 条消息 (时间升序)
func (r *chatMessageRepository) ListBySession(ctx context.Context, sessionID string, limit int) ([]model.ChatMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	query := database.DB.WithContext(ctx).Model(&model.ChatMessage{}).Where("session_id = ?", sessionID)
	var recent []model.ChatMessage
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&recent).Error; err != nil {
		return nil, err
	}
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	return recent, nil
}

// CountChat 统计会话内 user/assistant 消息数 (M10.2 滚动摘要触发检查, 不含 tool 展示行)
func (r *chatMessageRepository) CountChat(ctx context.Context, sessionID string) (int64, error) {
	var n int64
	err := database.DB.WithContext(ctx).Model(&model.ChatMessage{}).
		Where("session_id = ? AND role IN ?", sessionID, []string{model.ChatRoleUser, model.ChatRoleAssistant}).
		Count(&n).Error
	return n, err
}

// ListForSummary 返回会话最旧的 user/assistant 消息 (时间升序, 排除最近 skipNewest 条, M10.2)
func (r *chatMessageRepository) ListForSummary(ctx context.Context, sessionID string, skipNewest int) ([]model.ChatMessage, error) {
	if skipNewest < 0 {
		skipNewest = 0
	}
	const sql = `SELECT m.* FROM (
		SELECT id, ROW_NUMBER() OVER (ORDER BY created_at DESC, id DESC) AS rn
		FROM agent_chat_messages
		WHERE session_id = ? AND role IN ('user','assistant')
	) t JOIN agent_chat_messages m ON m.id = t.id
	WHERE t.rn > ?
	ORDER BY m.created_at ASC, m.id ASC`
	var items []model.ChatMessage
	err := database.DB.WithContext(ctx).Raw(sql, sessionID, skipNewest).Scan(&items).Error
	return items, err
}

func (r *chatMessageRepository) DeleteBySession(ctx context.Context, sessionID string) error {
	return database.DB.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&model.ChatMessage{}).Error
}

// GetByExecutionID 按 execution_id 查最新一条 assistant 消息 (不存在返回 ErrNotFound)
func (r *chatMessageRepository) GetByExecutionID(ctx context.Context, executionID string) (*model.ChatMessage, error) {
	var m model.ChatMessage
	if err := database.DB.WithContext(ctx).
		Where("execution_id = ? AND role = ?", executionID, model.ChatRoleAssistant).
		Order("created_at DESC").
		First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}
