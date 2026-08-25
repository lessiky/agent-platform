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
	ID            string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID       string    `gorm:"type:uuid;not null;index" json:"agent_id"`
	Title         string    `gorm:"type:varchar(128)" json:"title"`
	UserID        *string   `gorm:"type:uuid" json:"user_id"`
	Status        string    `gorm:"type:varchar(16);not null;default:'active'" json:"status"`
	LastMessageAt time.Time `json:"last_message_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

// Agent 执行任务状态 (agent_executions)
const (
	AgentExecutionStatusRunning         = "running"          // 执行中 (模型/工具调用进行中)
	AgentExecutionStatusWaitingApproval = "waiting_approval" // 等待人工审核 (对应工具未执行, 审核决策后回填终态)
	AgentExecutionStatusSuccess         = "success"          // 成功完成
	AgentExecutionStatusFailed          = "failed"           // 失败 (执行错误 / 整体时限耗尽 / 审核未执行 / 服务重启)
	AgentExecutionStatusStalled         = "stalled"          // 卡死 (无进度心跳超时, watchdog 判定并取消)
	AgentExecutionStatusCancelled       = "cancelled"        // 已取消 (外部方经取消端点主动放弃, 执行上下文已取消)
)

// AgentExecution Agent 执行任务 (/invoke 202 异步化, 2026-08-24)
// 一次外部调用成为独立执行任务: HTTP 请求立即返回 202 后,
// 调用方凭 execution_id 查询 status / stage / last_activity_at / result;
// watchdog 依据 last_activity_at 判定卡死 (stalled)、依据 deadline 判定预算耗尽 (failed),
// 使外部方能明确区分 "执行中" 与 "卡死"
type AgentExecution struct {
	ID               string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID          string         `gorm:"type:uuid;not null;index" json:"agent_id"`
	Source           string         `gorm:"type:varchar(16);not null;default:'api_invoke'" json:"source"`
	SessionID        *string        `gorm:"type:uuid;index" json:"session_id"`
	Status           string         `gorm:"type:varchar(24);not null;default:'running';index" json:"status"`
	Stage            string         `gorm:"type:varchar(256);not null;default:''" json:"stage"` // 当前阶段 (如 model:round=2 / tool:mcp名/工具名 / 等待审核)
	PendingApprovals datatypes.JSON `gorm:"type:jsonb" json:"pending_approvals,omitempty"`      // 本次执行产生的审核请求 (approval_id 数组)
	Result           datatypes.JSON `gorm:"type:jsonb" json:"result,omitempty"`                 // 执行结果 (ChatResult JSON; 进入等待审核时先存中间应答)
	Error            string         `gorm:"type:text" json:"error,omitempty"`
	Deadline         time.Time      `json:"deadline"`         // 整体 deadline (起点+最大时长, 仅 running 状态生效)
	LastActivityAt   time.Time      `json:"last_activity_at"` // 最近一次进度心跳 (watchdog 卡死判定依据)
	StartedAt        time.Time      `json:"started_at"`
	FinishedAt       *time.Time     `json:"finished_at"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

func (AgentExecution) TableName() string {
	return "agent_executions"
}
