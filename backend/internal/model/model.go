package model

import (
    "time"

    "gorm.io/datatypes"
    "gorm.io/gorm"
)

// 模型提供商 (PRD 2.3.1 P0: 预置主流提供商 + 自定义)
const (
    ModelProviderOpenAI   = "openai"
    ModelProviderAnthropic = "anthropic"
    ModelProviderGoogle   = "google"
    ModelProviderAzure    = "azure"
    ModelProviderCustom   = "custom"
)

// 模型模板状态 (PRD 5.3: Active / Inactive / Error)
const (
    ModelStatusActive   = "active"   // 可用 (路由可选)
    ModelStatusInactive = "inactive" // 手动停用 (路由跳过)
    ModelStatusError    = "error"    // 连通性检测失败
)

// ModelTemplate 模型模板 (PRD 5.3)
type ModelTemplate struct {
    ID              string                `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
    Name            string                `gorm:"type:varchar(64);not null" json:"name"`
    Provider        string                `gorm:"type:varchar(32);not null;index" json:"provider"`
    Model           string                `gorm:"type:varchar(128);not null" json:"model"` // 模型名 (gpt-4o/claude-3 等)
    APIKey          []byte                `gorm:"type:bytea" json:"-"`                      // AES-256-GCM 密文
    Endpoint        string                `gorm:"type:varchar(512)" json:"endpoint"`          // API 端点 (自定义必填)
    Config          datatypes.JSON        `gorm:"type:jsonb;default:'{}'" json:"config"`      // 生成参数 (temperature 等)
    Status          string                `gorm:"type:varchar(16);not null;default:'active';index" json:"status"`
    Priority        int                   `gorm:"not null;default:100;index" json:"priority"` // 越小优先级越高 (路由)
    TeamID          *string               `gorm:"type:uuid" json:"team_id"`                    // 所属团队 (预留)
    Tags            datatypes.JSONSlice[string] `gorm:"type:jsonb;default:'[]'" json:"tags"`
    HealthLastCheck *time.Time            `json:"health_last_check"`
    HealthLatencyMs *int                  `json:"health_latency_ms"`
    LastError       string                `gorm:"type:varchar(512)" json:"last_error"`
    CreatedBy       *string               `gorm:"type:uuid" json:"created_by"`
    CreatedAt       time.Time             `json:"created_at"`
    UpdatedAt       time.Time             `json:"updated_at"`
    DeletedAt       gorm.DeletedAt        `gorm:"index" json:"-"`
}

func (ModelTemplate) TableName() string {
    return "model_templates"
}

// ModelQuota 模型配额 (PRD 5.4, Phase 1 按模型维度, team_id 预留)
type ModelQuota struct {
    ID             string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
    ModelID        string    `gorm:"type:uuid;not null;uniqueIndex" json:"model_id"`
    TeamID         *string   `gorm:"type:uuid" json:"team_id"` // 预留
    DailyLimit       int       `gorm:"not null;default:0" json:"daily_limit"`       // 0 = 不限
    MonthlyLimit     int       `gorm:"not null;default:0" json:"monthly_limit"`     // 0 = 不限
    DailyTokenLimit  int       `gorm:"not null;default:0" json:"daily_token_limit"`  // 0 = 不限
    MonthlyTokenLimit int      `gorm:"not null;default:0" json:"monthly_token_limit"` // 0 = 不限
    DailyUsed        int       `gorm:"not null;default:0" json:"daily_used"`
    MonthlyUsed      int       `gorm:"not null;default:0" json:"monthly_used"`
    DailyTokenUsed   int       `gorm:"not null;default:0" json:"daily_token_used"`
    MonthlyTokenUsed int       `gorm:"not null;default:0" json:"monthly_token_used"`
    ResetDailyAt   time.Time `json:"reset_daily_at"`
    ResetMonthlyAt time.Time `json:"reset_monthly_at"`
    UpdatedAt      time.Time `json:"updated_at"`
}

func (ModelQuota) TableName() string {
    return "model_quotas"
}

// ModelUsageLog 模型调用用量日志 (PRD 2.3.2 负载监控 P1, 运行时消费写入)
type ModelUsageLog struct {
    ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
    ModelID   string    `gorm:"type:uuid;not null;index:idx_model_usage_lookup,priority:1" json:"model_id"`
    AgentID   *string   `gorm:"type:uuid" json:"agent_id"`
    OK        bool      `gorm:"not null" json:"ok"`
    Tokens    int       `gorm:"not null;default:0" json:"tokens"`
    LatencyMs int       `gorm:"not null;default:0" json:"latency_ms"`
    Error     string    `gorm:"type:varchar(512)" json:"error"`
    CreatedAt time.Time `gorm:"index:idx_model_usage_lookup,priority:2" json:"created_at"`
}

func (ModelUsageLog) TableName() string {
    return "model_usage_logs"
}
// ModelHealthLog 模型连通性检查历史 (健康监控, 每模型保留最近 N 条)
type ModelHealthLog struct {
    ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
    ModelID   string    `gorm:"type:uuid;not null;index:idx_model_health_lookup,priority:1" json:"model_id"`
    OK        bool      `gorm:"not null" json:"ok"`
    LatencyMs int       `gorm:"not null;default:0" json:"latency_ms"`
    Error     string    `gorm:"type:varchar(512)" json:"error"`
    CreatedAt time.Time `gorm:"index:idx_model_health_lookup,priority:2" json:"created_at"`
}

func (ModelHealthLog) TableName() string {
    return "model_health_logs"
}