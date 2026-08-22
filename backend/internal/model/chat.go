package model

import (
	"time"

	"gorm.io/datatypes"
)

// 会话消息角色
const (
	ChatRoleUser      = "user"
	ChatRoleAssistant = "assistant"
	ChatRoleTool      = "tool"
)

// 会话状态
const (
	ChatSessionActive   = "active"
	ChatSessionArchived = "archived"
)

// ChatSession Agent 对话会话 (M2.5, PRD 5.9)
type ChatSession struct {
	ID             string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID        string    `gorm:"type:uuid;not null;index" json:"agent_id"`
	Title          string    `gorm:"type:varchar(128)" json:"title"`
	UserID         *string   `gorm:"type:uuid" json:"user_id"`
	Status         string    `gorm:"type:varchar(16);not null;default:'active'" json:"status"`
	LastMessageAt  time.Time `json:"last_message_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (ChatSession) TableName() string {
	return "agent_chat_sessions"
}

// ChatMessage 会话消息 (user/assistant/tool; assistant 携带执行元数据)
type ChatMessage struct {
	ID            string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	SessionID     string         `gorm:"type:uuid;not null;index" json:"session_id"`
	Role          string         `gorm:"type:varchar(16);not null" json:"role"`
	Content       string         `gorm:"type:text;not null" json:"content"`
	ExecutionID   *string        `json:"execution_id"`
	ExecutionMeta datatypes.JSON `json:"execution_meta"`
	CreatedAt     time.Time      `json:"created_at"`
}

func (ChatMessage) TableName() string {
	return "agent_chat_messages"
}