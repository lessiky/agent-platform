package model

import (
	"time"
)

// 知识库分类约束 (PRD 3.1)
const (
	KBCategoryNameMinLen = 2  // 名称最少字符 (rune)
	KBCategoryNameMaxLen = 32 // 名称最多字符 (rune)
	KBCategoryDescMaxLen = 200
)

// 知识条目约束 (PRD 3.2)
const (
	KBTitleMinLen  = 2 // 标题最少字符 (rune)
	KBTitleMaxLen  = 100
	KBMaxDocBytes  = 204800 // 单条目正文上限 200KB
	KBSearchMaxTop = 20     // 试算/工具检索 top_k 上限
)

// 知识条目来源
const (
	KBSourceManual      = "manual"       // 人工创建/编辑
	KBSourceChatSummary = "chat_summary" // 对话总结入库 (记录来源会话与 Agent)
)

// 知识条目状态
const (
	KBStatusActive   = "active"   // 参与检索
	KBStatusArchived = "archived" // 归档 (不参与检索, 界面保留可恢复)
)

// 检索模式 (AgentConfig.kb_search_mode)
const (
	KBSearchModeAuto = "auto" // 每轮自动注入 top-K + 注册 search_knowledge 工具
	KBSearchModeTool = "tool" // 仅注册工具, 模型按需检索
	KBSearchModeOff  = "off"  // 不启用
)

// KBCategory 知识库分类 (M11)
type KBCategory struct {
	ID          string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name        string    `gorm:"type:varchar(64);not null" json:"name"`
	Description string    `gorm:"type:text" json:"description"`
	ParentID    *string   `gorm:"type:uuid" json:"parent_id"` // 父级分类 (M11.5: 两级层级, NULL = 顶级; 名称同一父级下同级唯一)
	CreatedBy   *string   `gorm:"type:uuid" json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (KBCategory) TableName() string {
	return "kb_categories"
}

// 知识库向量回填任务状态 (M11.5: 后台异步任务, 状态持久化 DB, 重启可恢复)
const (
	KBTaskStatusPending   = "pending"   // 已创建, 等待执行
	KBTaskStatusRunning   = "running"   // 执行中 (批边界协作取消)
	KBTaskStatusSucceeded = "succeeded" // 全部完成
	KBTaskStatusFailed    = "failed"    // 失败 (含服务重启残留)
	KBTaskStatusCancelled = "cancelled" // 用户取消 (批边界停止)
)

// KBBackfillTask 向量回填任务 (M11.5: 替代同步回填端点; 平台同时仅一个任务可运行)
type KBBackfillTask struct {
	ID         string     `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Status     string     `gorm:"type:varchar(16);not null;default:'pending';index" json:"status"`
	Total      int        `gorm:"not null;default:0" json:"total"` // 启动时统计的未向量化条目数
	Done       int        `gorm:"not null;default:0" json:"done"`  // 已处理 (成功 + 失败) 条目数
	Failed     int        `gorm:"not null;default:0" json:"failed"`
	LastError  string     `gorm:"type:text" json:"last_error"` // 末次错误 (失败/重启残留)
	CreatedBy  *string    `gorm:"type:uuid" json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

func (KBBackfillTask) TableName() string {
	return "kb_backfill_tasks"
}

// KBDocument 知识条目 (M11)
type KBDocument struct {
	ID              string     `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	CategoryID      string     `gorm:"type:uuid;not null;index:idx_kb_doc_scope,priority:1" json:"category_id"`
	Title           string     `gorm:"type:varchar(200);not null" json:"title"`
	Content         string     `gorm:"type:text;not null" json:"content"`
	Source          string     `gorm:"type:varchar(16);not null;default:'manual'" json:"source"`
	SourceSessionID *string    `gorm:"type:uuid" json:"source_session_id"`
	SourceAgentID   *string    `gorm:"type:uuid" json:"source_agent_id"`
	Status          string     `gorm:"type:varchar(16);not null;default:'active';index:idx_kb_doc_scope,priority:2" json:"status"`
	AccessCount     int        `gorm:"not null;default:0" json:"access_count"`
	LastAccessedAt  *time.Time `json:"last_accessed_at"`
	Embedding       *Vector    `gorm:"type:vector(1024)" json:"-"` // pgvector 向量 (NULL = 未向量化, 异步回填), 不出现在 API
	CreatedBy       *string    `gorm:"type:uuid" json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (KBDocument) TableName() string {
	return "kb_documents"
}

// AgentKBBinding Agent 与知识库分类绑定 (M11, 绑定 = 读 + 写)
type AgentKBBinding struct {
	ID         string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID    string    `gorm:"type:uuid;not null;uniqueIndex:idx_agent_kb,priority:1" json:"agent_id"`
	CategoryID string    `gorm:"type:uuid;not null;uniqueIndex:idx_agent_kb,priority:2" json:"category_id"`
	ReadOnly   bool      `gorm:"not null;default:false" json:"read_only"` // 预留 (P1 只读绑定), 本期恒 false
	CreatedAt  time.Time `json:"created_at"`
}

func (AgentKBBinding) TableName() string {
	return "agent_kb_bindings"
}

// KBChunk 知识条目分块 (M11.5 事项 4: 块级向量; 正文 Markdown 结构感知切分, 块向量 NULL = 未向量化)
type KBChunk struct {
	ID         string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	DocID      string    `gorm:"type:uuid;not null;uniqueIndex:idx_kb_chunk_doc_order,priority:1" json:"doc_id"`
	ChunkIndex int       `gorm:"not null;default:0;uniqueIndex:idx_kb_chunk_doc_order,priority:2" json:"chunk_index"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	Embedding  *Vector   `gorm:"type:vector(1024)" json:"-"` // 块级向量 (NULL = 未向量化, 回填任务补齐), 不出现在 API
	UpdatedAt  time.Time `json:"updated_at"`
}

func (KBChunk) TableName() string {
	return "kb_chunks"
}
