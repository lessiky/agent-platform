package model

import (
	"time"

	"gorm.io/datatypes"
)

// 审核请求状态
const (
	ApprovalStatusPending  = "pending"  // 待审核
	ApprovalStatusApproved = "approved" // 已通过 (已执行/执行中)
	ApprovalStatusRejected = "rejected" // 已驳回
	ApprovalStatusExpired  = "expired"  // 已超时 (按 on_timeout 策略处理)
)

// 审核请求调用来源
const (
	ApprovalSourceManual    = "manual"     // 控制台手动调用 (MCP 详情测试)
	ApprovalSourceRuntime   = "runtime"    // Agent 运行时模拟流量
	ApprovalSourceAPIInvoke = "api_invoke" // API Key 外部调用 (/invoke)
	ApprovalSourceWorkflow  = "workflow"   // 工作流 MCP 节点 (M5)
	ApprovalSourceChat      = "chat"       // Agent 对话执行 (M2.5)
)

// 超时策略
const (
	ApprovalOnTimeoutReject  = "reject"  // 超时拒绝 (默认)
	ApprovalOnTimeoutApprove = "approve" // 超时自动通过
)

// ApprovalSettings 审核全局配置 (单例, id=1)
type ApprovalSettings struct {
	ID                    string    `gorm:"primaryKey" json:"id"`
	DefaultTimeoutMinutes int       `gorm:"not null;default:30" json:"default_timeout_minutes"`
	OnTimeout             string    `gorm:"type:varchar(16);not null;default:'reject'" json:"on_timeout"`
	UpdatedBy             *string   `gorm:"type:uuid" json:"updated_by"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func (ApprovalSettings) TableName() string {
	return "approval_settings"
}

// ToolApproval MCP 工具调用人工审核请求 (PRD 5.8)
type ToolApproval struct {
	ID                  string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	MCPServerID         string         `gorm:"type:uuid;not null;index" json:"mcp_server_id"`
	ToolName            string         `gorm:"type:varchar(128);not null;index" json:"tool_name"`
	AgentID             *string        `gorm:"type:uuid;index" json:"agent_id"`
	Source              string         `gorm:"type:varchar(16);not null;default:'manual';index" json:"source"`
	WorkflowExecutionID *string        `gorm:"type:uuid" json:"workflow_execution_id"`
	ChatSessionID       *string        `gorm:"type:uuid" json:"chat_session_id"`
	Arguments           datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"arguments"`
	Status              string         `gorm:"type:varchar(16);not null;default:'pending';index" json:"status"`
	RequestedAt         time.Time      `gorm:"index" json:"requested_at"`
	ExpiresAt           time.Time      `gorm:"index" json:"expires_at"`
	DecidedBy           *string        `gorm:"type:uuid" json:"decided_by"`
	DecidedAt           *time.Time     `json:"decided_at"`
	Comment             *string        `gorm:"type:text" json:"comment"`
	Result              datatypes.JSON `gorm:"type:jsonb" json:"result"`
	ExecutedAt          *time.Time     `json:"executed_at"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

func (ToolApproval) TableName() string {
	return "tool_approvals"
}

// AuditLog 审计日志 (M4.5 用于审核相关操作, 后续模块可复用)
type AuditLog struct {
	ID         string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID     *string        `gorm:"type:uuid;index" json:"user_id"`
	Username   string         `gorm:"type:varchar(64)" json:"username"`
	Action     string         `gorm:"type:varchar(64);index" json:"action"`
	Resource   string         `gorm:"type:varchar(64)" json:"resource"`
	ResourceID *string        `gorm:"type:varchar(128)" json:"resource_id"`
	Detail     datatypes.JSON `gorm:"type:jsonb" json:"detail"`
	IP         string         `gorm:"type:varchar(64)" json:"ip"`
	CreatedAt  time.Time      `gorm:"index" json:"created_at"`
}

func (AuditLog) TableName() string {
	return "audit_logs"
}
