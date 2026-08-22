package model

import (
    "time"

    "gorm.io/datatypes"
    "gorm.io/gorm"
)

// MCP 传输类型
const (
    MCPTransportStdio = "stdio" // 本地子进程 (Phase 1 平台不托管子进程, 仅允许注册)
    MCPTransportSSE   = "sse"   // SSE 传输 (legacy 握手, 不可用时回退直连 POST)
    MCPTransportHTTP  = "http"  // Streamable HTTP (JSON-RPC over POST, 响应可为 JSON 或 SSE)
)

// MCP 服务器状态
const (
    MCPStatusPending      = "pending"      // 未检测
    MCPStatusConnected    = "connected"    // 连通性检测通过
    MCPStatusDisconnected = "disconnected" // 连通性检测失败
    MCPStatusError        = "error"        // 检测过程异常
)

// MCPTool MCP 工具定义 (来自 tools/list)
type MCPTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
	// RequiresApproval 调用需人工审核 (M4.5): 为 true 时调用须先通过审核请求, 平台不直接执行
	RequiresApproval bool `json:"requires_approval"`
}

// MCPServer MCP 服务器定义
type MCPServer struct {
	ID            string                `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name          string                `gorm:"type:varchar(64);not null" json:"name"`
	Endpoint      string                `gorm:"type:varchar(512);not null" json:"endpoint"`
	Transport     string                `gorm:"type:varchar(16);not null;default:'http';index" json:"transport"`
	Description   string                `gorm:"type:text" json:"description"`
	Status        string                `gorm:"type:varchar(16);not null;default:'pending';index" json:"status"`
	Tools         datatypes.JSON        `gorm:"type:jsonb;default:'[]'" json:"tools"`              // 已发现的工具列表
	Credentials   []byte                `gorm:"type:bytea" json:"-"`                                // AES-256-GCM 密文 (PRD 2.2.3)
	Tags          datatypes.JSONSlice[string] `gorm:"type:jsonb;default:'[]'" json:"tags"`           // 标签分组 (P2)
	HealthLastCheck *time.Time          `json:"health_last_check"`
	HealthLatencyMs *int                `json:"health_latency_ms"`
	LastError     string                `gorm:"type:varchar(512)" json:"last_error"`
	CreatedBy     *string               `gorm:"type:uuid" json:"created_by"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	DeletedAt     gorm.DeletedAt        `gorm:"index" json:"-"`
}

func (MCPServer) TableName() string {
    return "mcp_servers"
}

// MCPHealthLog MCP 连通性检查历史 (健康监控看板, 每服务器保留最近 N 条)
type MCPHealthLog struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	MCPID     string    `gorm:"type:uuid;not null;index:idx_mcp_health_lookup,priority:1" json:"mcp_id"`
	OK        bool      `gorm:"not null" json:"ok"`
	LatencyMs int       `gorm:"not null;default:0" json:"latency_ms"`
	Error     string    `gorm:"type:varchar(512)" json:"error"`
	CreatedAt time.Time `gorm:"index:idx_mcp_health_lookup,priority:2" json:"created_at"`
}

func (MCPHealthLog) TableName() string {
    return "mcp_health_logs"
}

// MCPAgentBinding MCP 服务器 <-> Agent 绑定 (访问控制: 仅绑定的 Agent 可调用该 MCP)
type MCPAgentBinding struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	MCPID     string    `gorm:"type:uuid;not null;uniqueIndex:idx_mcp_agent,priority:1" json:"mcp_id"`
	AgentID   string    `gorm:"type:uuid;not null;uniqueIndex:idx_mcp_agent,priority:2" json:"agent_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (MCPAgentBinding) TableName() string {
    return "mcp_agent_bindings"
}