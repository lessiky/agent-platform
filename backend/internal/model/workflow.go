package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// 工作流状态
const (
	WorkflowStatusDraft    = "draft"    // 草稿
	WorkflowStatusActive   = "active"   // 已激活 (可触发)
	WorkflowStatusArchived = "archived" // 已归档
)

// 触发类型
const (
	WorkflowTriggerManual  = "manual"  // 手动触发
	WorkflowTriggerCron    = "cron"    // 定时调度
	WorkflowTriggerWebhook = "webhook" // Webhook 事件触发
)

// 执行状态
const (
	ExecutionStatusRunning         = "running"          // 执行中
	ExecutionStatusWaitingApproval = "waiting_approval" // 等待人工审核 (MCP 节点挂起)
	ExecutionStatusSuccess         = "success"          // 成功完成
	ExecutionStatusFailed          = "failed"           // 失败
	ExecutionStatusCancelled       = "cancelled"        // 已取消
)

// 节点执行状态
const (
	NodeStatusPending         = "pending"          // 等待依赖
	NodeStatusRunning         = "running"          // 执行中
	NodeStatusSuccess         = "success"          // 成功
	NodeStatusFailed          = "failed"           // 失败 (重试耗尽)
	NodeStatusSkipped         = "skipped"          // 跳过 (上游失败/分支未选中)
	NodeStatusWaitingApproval = "waiting_approval" // 等待人工审核
	NodeStatusCancelled       = "cancelled"        // 已取消
)

// 节点类型 (M5 Phase 1: 循环节点延后到 Phase 2)
const (
	NodeTypeAgent     = "agent"     // 调用 Agent (单轮对话)
	NodeTypeMCPTool   = "mcp_tool"  // 调用 MCP 工具 (可触发人工审核)
	NodeTypeHTTP      = "http"      // 外部 HTTP 调用
	NodeTypeDelay     = "delay"     // 延迟等待
	NodeTypeCondition = "condition" // 条件分支 (true/false 双出口)
)

// Workflow 工作流定义 (PRD 5.5)
type Workflow struct {
	ID              string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name            string         `gorm:"type:varchar(64);not null" json:"name"`
	Description     string         `gorm:"type:text" json:"description"`
	Definition      datatypes.JSON `gorm:"type:jsonb;not null" json:"definition"` // DAG 定义 {version,nodes,edges}
	Status          string         `gorm:"type:varchar(16);not null;default:'draft';index" json:"status"`
	InputSchema     datatypes.JSON `gorm:"type:jsonb" json:"input_schema"`
	OutputSchema    datatypes.JSON `gorm:"type:jsonb" json:"output_schema"`
	Version         int            `gorm:"not null;default:1" json:"version"`
	Schedule        datatypes.JSON `gorm:"type:jsonb" json:"schedule"` // {cron,input,timezone}
	ScheduleEnabled bool           `gorm:"not null;default:false" json:"schedule_enabled"`
	WebhookToken    string         `gorm:"type:varchar(64);index" json:"webhook_token"`
	CreatedBy       *string        `gorm:"type:uuid" json:"created_by"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Workflow) TableName() string {
	return "workflows"
}

// WorkflowVersion 工作流版本快照 (每次保存生成, 支持回溯)
type WorkflowVersion struct {
	ID           string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	WorkflowID   string         `gorm:"type:uuid;not null;index" json:"workflow_id"`
	Version      int            `gorm:"not null" json:"version"`
	Definition   datatypes.JSON `gorm:"type:jsonb;not null" json:"definition"`
	InputSchema  datatypes.JSON `gorm:"type:jsonb" json:"input_schema"`
	OutputSchema datatypes.JSON `gorm:"type:jsonb" json:"output_schema"`
	CreatedBy    *string        `gorm:"type:uuid" json:"created_by"`
	CreatedAt    time.Time      `json:"created_at"`
}

func (WorkflowVersion) TableName() string {
	return "workflow_versions"
}

// WorkflowExecution 工作流执行记录 (PRD 5.6)
type WorkflowExecution struct {
	ID              string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	WorkflowID      string         `gorm:"type:uuid;not null;index" json:"workflow_id"`
	WorkflowName    string         `gorm:"type:varchar(64)" json:"workflow_name"`
	WorkflowVersion int            `gorm:"not null" json:"workflow_version"`
	TriggerType     string         `gorm:"type:varchar(16);not null;index" json:"trigger_type"`
	TriggeredBy     *string        `gorm:"type:uuid" json:"triggered_by"`
	Status          string         `gorm:"type:varchar(24);not null;default:'running';index" json:"status"`
	Input           datatypes.JSON `gorm:"type:jsonb" json:"input"`
	Output          datatypes.JSON `gorm:"type:jsonb" json:"output"`
	TraceID         string         `gorm:"type:varchar(32);index" json:"trace_id"`
	Error           string         `gorm:"type:text" json:"error"`
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      *time.Time     `json:"finished_at"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func (WorkflowExecution) TableName() string {
	return "workflow_executions"
}

// WorkflowNodeExecution 节点执行记录 (PRD 5.7, 执行追踪的最小粒度)
type WorkflowNodeExecution struct {
	ID          string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ExecutionID string         `gorm:"type:uuid;not null;index:idx_wf_node_exec,priority:1" json:"execution_id"`
	NodeID      string         `gorm:"type:varchar(64);not null;index:idx_wf_node_exec,priority:2" json:"node_id"`
	NodeType    string         `gorm:"type:varchar(16)" json:"node_type"`
	NodeName    string         `gorm:"type:varchar(128)" json:"node_name"`
	Status      string         `gorm:"type:varchar(24);not null;default:'pending';index" json:"status"`
	Attempt     int            `gorm:"not null;default:1" json:"attempt"`
	Input       datatypes.JSON `gorm:"type:jsonb" json:"input"`
	Output      datatypes.JSON `gorm:"type:jsonb" json:"output"`
	Error       string         `gorm:"type:text" json:"error"`
	ApprovalID  *string        `gorm:"type:uuid" json:"approval_id"` // 审核挂起时关联的审核请求
	DurationMs  int64          `gorm:"not null;default:0" json:"duration_ms"`
	StartedAt   *time.Time     `json:"started_at"`
	FinishedAt  *time.Time     `json:"finished_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

func (WorkflowNodeExecution) TableName() string {
	return "workflow_node_executions"
}
