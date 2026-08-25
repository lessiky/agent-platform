package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Agent 运行状态
const (
	AgentStatusIdle    = "idle"    // 未启动
	AgentStatusRunning = "running" // 运行中
	AgentStatusStopped = "stopped" // 已停止
	AgentStatusError   = "error"   // 异常
)

// Agent 实例状态
const (
	InstanceStatusPending  = "pending"
	InstanceStatusRunning  = "running"
	InstanceStatusStopping = "stopping"
	InstanceStatusStopped  = "stopped"
	InstanceStatusError    = "error"
)

// 日志级别
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

// API Key 状态
const (
	APIKeyStatusActive  = "active"
	APIKeyStatusRevoked = "revoked"
)

// Agent Agent 定义
type Agent struct {
	ID          string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name        string         `gorm:"type:varchar(64);not null" json:"name"`
	Description string         `gorm:"type:text" json:"description"`
	ModelID     *string        `gorm:"type:uuid" json:"model_id"` // 关联模型模板 (M4)
	Status      string         `gorm:"type:varchar(16);not null;default:'idle';index" json:"status"`
	Version     int            `gorm:"not null;default:1" json:"version"`
	Config      datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"config"`
	TeamID      *string        `gorm:"type:uuid" json:"team_id"` // 所属团队 (预留)
	CreatedBy   *string        `gorm:"type:uuid" json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Agent) TableName() string {
	return "agents"
}

// AgentVersion Agent 配置版本快照 (支持回滚)
type AgentVersion struct {
	ID          string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID     string         `gorm:"type:uuid;not null;uniqueIndex:idx_agent_version" json:"agent_id"`
	Version     int            `gorm:"not null;uniqueIndex:idx_agent_version" json:"version"`
	Name        string         `gorm:"type:varchar(64);not null" json:"name"`
	Description string         `gorm:"type:text" json:"description"`
	Config      datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"config"`
	CreatedBy   *string        `gorm:"type:uuid" json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
}

func (AgentVersion) TableName() string {
	return "agent_versions"
}

// AgentInstance Agent 运行实例 (MVP 阶段一个 Agent 对应一个实例)
type AgentInstance struct {
	ID            string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID       string         `gorm:"type:uuid;not null;uniqueIndex" json:"agent_id"`
	Status        string         `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	Endpoint      string         `gorm:"type:varchar(128)" json:"endpoint"`
	StartedAt     *time.Time     `json:"started_at"`
	StoppedAt     *time.Time     `json:"stopped_at"`
	LastHeartbeat *time.Time     `json:"last_heartbeat"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

func (AgentInstance) TableName() string {
	return "agent_instances"
}

// AgentLog Agent 运行日志
type AgentLog struct {
	ID         string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID    string    `gorm:"type:uuid;not null;index:idx_agent_log_lookup,priority:1" json:"agent_id"`
	InstanceID *string   `gorm:"type:uuid" json:"instance_id"`
	Level      string    `gorm:"type:varchar(8);not null;default:'info'" json:"level"`
	Message    string    `gorm:"type:text;not null" json:"message"`
	CreatedAt  time.Time `gorm:"index:idx_agent_log_lookup,priority:2" json:"created_at"`
}

func (AgentLog) TableName() string {
	return "agent_logs"
}

// AgentAPIKey Agent API Key (仅存 SHA-256 摘要, 明文只在创建时返回一次)
type AgentAPIKey struct {
	ID         string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID    string         `gorm:"type:uuid;not null;index" json:"agent_id"`
	Name       string         `gorm:"type:varchar(64);not null" json:"name"`
	KeyPrefix  string         `gorm:"type:varchar(16)" json:"key_prefix"` // 展示用前缀
	KeyHash    string         `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	Status     string         `gorm:"type:varchar(16);not null;default:'active'" json:"status"`
	LastUsedAt *time.Time     `json:"last_used_at"`
	ExpiresAt  *time.Time     `json:"expires_at"`
	CreatedBy  *string        `gorm:"type:uuid" json:"created_by"`
	CreatedAt  time.Time      `json:"created_at"`
	RevokedAt  *time.Time     `json:"revoked_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

func (AgentAPIKey) TableName() string {
	return "agent_api_keys"
}

// AgentCallStat Agent 调用统计 (按天聚合)
type AgentCallStat struct {
	ID           string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID      string    `gorm:"type:uuid;not null;uniqueIndex:idx_agent_stat" json:"agent_id"`
	StatDate     time.Time `gorm:"type:date;not null;uniqueIndex:idx_agent_stat" json:"stat_date"`
	Calls        int64     `gorm:"not null;default:0" json:"calls"`
	Errors       int64     `gorm:"not null;default:0" json:"errors"`
	TotalTokens  int64     `gorm:"not null;default:0" json:"total_tokens"`
	TotalLatency int64     `gorm:"column:total_latency_ms;not null;default:0" json:"total_latency_ms"`
	UpdatedAt    time.Time `json:"-"`
}

func (AgentCallStat) TableName() string {
	return "agent_call_stats"
}
