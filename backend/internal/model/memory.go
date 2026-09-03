package model

import (
	"time"

	"gorm.io/datatypes"
)

// 记忆类型 (M10)
const (
	MemoryKindPreference = "preference" // 用户偏好
	MemoryKindFact       = "fact"       // 稳定事实
	MemoryKindDecision   = "decision"   // 重要决定
	MemoryKindEvent      = "event"      // 关键事件
)

// 记忆来源 (M10)
const (
	MemorySourceUserExplicit = "user_explicit" // 用户显式添加
	MemorySourceLLMExtracted = "llm_extracted" // 模型自动抽取
)

// 记忆状态 (M10)
const (
	MemoryStatusActive   = "active"
	MemoryStatusArchived = "archived"
)

// 列表范围 (M10): mine=当前用户 user 级 + Agent 级, agent=仅 Agent 级, all=全部 (仅 admin)
const (
	MemoryScopeMine  = "mine"
	MemoryScopeAgent = "agent"
	MemoryScopeAll   = "all"
)

// MemoryContentMaxLen 单条记忆内容长度上限 (rune)
const MemoryContentMaxLen = 500

// Memory 长期记忆 (M10): user_id 为空表示 Agent 级全局记忆
type Memory struct {
	ID             string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID        string         `gorm:"type:uuid;not null;index:idx_mem_scope,priority:1" json:"agent_id"`
	UserID         *string        `gorm:"type:uuid;index:idx_mem_scope,priority:2" json:"user_id"` // NULL = Agent 级
	Kind           string         `gorm:"type:varchar(16);not null;default:'fact'" json:"kind"`
	Content        string         `gorm:"type:text;not null" json:"content"`
	Source         string         `gorm:"type:varchar(16);not null;default:'llm_extracted'" json:"source"`
	Status         string         `gorm:"type:varchar(16);not null;default:'active';index:idx_mem_scope,priority:3" json:"status"`
	AccessCount    int            `gorm:"not null;default:0" json:"access_count"`
	LastAccessedAt *time.Time     `json:"last_accessed_at"`
	Embedding      datatypes.JSON `gorm:"type:jsonb" json:"-"` // 预留 (M10.3 语义检索), 可空
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func (Memory) TableName() string {
	return "agent_memories"
}
