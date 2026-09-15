package config

import (
	"fmt"
	"log"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// KBConfig 知识库模块配置 (M11)
type KBConfig struct {
	// Enabled 总开关 (false 时所有 Agent 不注入/不注册工具, 管理端 CRUD 不受影响)
	Enabled bool `env:"KB_ENABLED" envDefault:"true"`
	// RetrievalTimeout 注入路径检索超时, 超时跳过注入 (检索故障永不阻断对话)
	RetrievalTimeout time.Duration `env:"KB_RETRIEVAL_TIMEOUT" envDefault:"500ms"`
	// TopK 注入 top-K
	TopK int `env:"KB_TOP_K" envDefault:"3"`
	// CharBudget 注入段总字符预算
	CharBudget int `env:"KB_CHAR_BUDGET" envDefault:"4000"`
	// MaxCandidates 单 Agent 检索候选集上限 (关键词路径)
	MaxCandidates int `env:"KB_MAX_CANDIDATES" envDefault:"500"`
	// EmbedModel embedding 模型模板名 (OpenAI 兼容 /embeddings); 空 = 不启用向量召回
	EmbedModel string `env:"KB_EMBED_MODEL"`
	// RerankModel rerank 模型模板名 (OpenAI 兼容 /rerank, vLLM/Xinference 兼容格式); 空 = 不重排
	RerankModel string `env:"KB_RERANK_MODEL"`
	// RecallSize 召回阶段候选数 (向量召回 / 关键词预筛上限, 亦为 rerank 候选上限)
	RecallSize int `env:"KB_RECALL_SIZE" envDefault:"20"`
	// RerankTimeout rerank 调用超时, 超时按召回序排序
	RerankTimeout time.Duration `env:"KB_RERANK_TIMEOUT" envDefault:"300ms"`
	// VectorDim 向量列维度 (列级固定, 保存 embedding 模型时探测校验维度匹配)
	VectorDim int `env:"KB_VECTOR_DIM" envDefault:"1024"`
	// SummaryModel 一键总结模型模板名; 空 = Agent 当前模型
	SummaryModel string `env:"KB_SUMMARY_MODEL"`
	// SummaryMaxTurns 总结输入消息上限
	SummaryMaxTurns int `env:"KB_SUMMARY_MAX_TURNS" envDefault:"20"`
	// SummaryTimeout 一键总结生成超时 (LLM 长文本输出, 慢模型可调大; 前端请求超时 150s 须大于此值)
	SummaryTimeout time.Duration `env:"KB_SUMMARY_TIMEOUT" envDefault:"120s"`
	// MaxDocBytes 单条目正文大小上限 (字节)
	MaxDocBytes int `env:"KB_MAX_DOC_BYTES" envDefault:"204800"`
	// ---- M11.5 条目分块 (块级向量; KB_CHUNK_ENABLED=false 回退整条向量路径) ----
	// ChunkEnabled 分块路径总开关 (false = 整条向量路径全量回退)
	ChunkEnabled bool `env:"KB_CHUNK_ENABLED" envDefault:"true"`
	// ChunkSize 分块目标大小 (rune)
	ChunkSize int `env:"KB_CHUNK_SIZE" envDefault:"500"`
	// ChunkOverlap 相邻块重叠 (rune)
	ChunkOverlap int `env:"KB_CHUNK_OVERLAP" envDefault:"50"`
	// ChunkThreshold 分块阈值: 正文 ≤ 阈值整条 1 块
	ChunkThreshold int `env:"KB_CHUNK_THRESHOLD" envDefault:"1000"`
	// ChunkRecallMult 块级召回候选数 = KB_RECALL_SIZE × 本值
	ChunkRecallMult int `env:"KB_CHUNK_RECALL_MULT" envDefault:"2"`
}
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	JWT      JWTConfig
	MCP      MCPConfig
	Model    ModelConfig
	Memory   MemoryConfig
	KB       KBConfig
}

type ServerConfig struct {
	Port int    `env:"SERVER_PORT" envDefault:"8080"`
	Mode string `env:"SERVER_MODE" envDefault:"release"`
}

type DatabaseConfig struct {
	Host     string `env:"DB_HOST" envDefault:"localhost"`
	Port     int    `env:"DB_PORT" envDefault:"5432"`
	User     string `env:"DB_USER"`
	Password string `env:"DB_PASSWORD"`
	Name     string `env:"DB_NAME" envDefault:"agent_platform"`
	SSLMode  string `env:"DB_SSLMODE" envDefault:"disable"`
	// SQLLogLevel GORM SQL 日志级别: silent / error / warn / info
	// info 输出全部 SQL (调试用, 日志刷屏); warn (默认) 仅输出错误与超过 SlowThreshold 的慢查询
	SQLLogLevel string `env:"DB_SQL_LOG_LEVEL" envDefault:"warn"`
}
type JWTConfig struct {
	Secret     string `env:"JWT_SECRET"`
	ExpireHour int    `env:"JWT_EXPIRE_HOUR" envDefault:"24"`
}

// MCPConfig MCP 模块配置 (M3)
type MCPConfig struct {
	// CredentialsKey 凭证加密密钥, 64 位 hex (32 字节, AES-256)
	CredentialsKey string `env:"MCP_CREDENTIALS_KEY"`
	// HealthCheckInterval 连通性定时检测间隔
	HealthCheckInterval time.Duration `env:"MCP_HEALTH_INTERVAL" envDefault:"1m"`
	// CheckTimeout 单次连通性检测/工具调用超时
	CheckTimeout time.Duration `env:"MCP_CHECK_TIMEOUT" envDefault:"5s"`
}

// ModelConfig 模型管理模块配置 (M4)
type ModelConfig struct {
	// CredentialsKey API Key 加密密钥, 64 位 hex (32 字节, AES-256)
	CredentialsKey string `env:"MODEL_CREDENTIALS_KEY"`
	// HealthCheckInterval 连通性定时检测间隔
	HealthCheckInterval time.Duration `env:"MODEL_HEALTH_INTERVAL" envDefault:"1m"`
	// CheckTimeout 单次连通性探测超时
	CheckTimeout time.Duration `env:"MODEL_CHECK_TIMEOUT" envDefault:"5s"`
	// ChatTimeout 单次对话调用超时 (LLM 生成耗时较长, 需长于探测超时; 慢模型长输出可调大, 前端对应请求超时须大于此值)
	ChatTimeout time.Duration `env:"MODEL_CHAT_TIMEOUT" envDefault:"300s"`
}

// MemoryConfig 记忆模块配置 (M10.1 检索注入 / M10.2 自动抽取 + 滚动摘要 / M10.3 语义检索)
type MemoryConfig struct {
	// Enabled 总开关 (false 时不注入记忆, 也不触发抽取/摘要, 对话链路行为与 M10 之前一致)
	Enabled bool `env:"MEMORY_ENABLED" envDefault:"true"`
	// MaxInject 每轮注入记忆条数上限
	MaxInject int `env:"MEMORY_MAX_INJECT" envDefault:"10"`
	// CharBudget 记忆段内容字符预算 (不含段头声明)
	CharBudget int `env:"MEMORY_CHAR_BUDGET" envDefault:"800"`
	// RetrievalTimeout 检索超时, 超时跳过注入 (记忆故障不阻断对话)
	RetrievalTimeout time.Duration `env:"MEMORY_RETRIEVAL_TIMEOUT" envDefault:"500ms"`
	// CacheTTL 活跃记忆集进程内缓存 TTL
	CacheTTL time.Duration `env:"MEMORY_CACHE_TTL" envDefault:"60s"`
	// ExtractEnabled 自动抽取开关 (M10.2, false 时 turn 结束不触发 LLM 抽取, 不影响注入)
	ExtractEnabled bool `env:"MEMORY_EXTRACT_ENABLED" envDefault:"true"`
	// ExtractMinTurns 同 session 两次抽取的最小轮次间隔 (M10.2 限流)
	ExtractMinTurns int `env:"MEMORY_EXTRACT_MIN_TURNS" envDefault:"5"`
	// ExtractModel 抽取/摘要用 ModelTemplate 名称 (M10.2, 空 = Agent 当前模型)
	ExtractModel string `env:"MEMORY_EXTRACT_MODEL"`
	// MaxActivePerScope 每 (agent, user) / Agent 级活跃记忆上限 (M10.2, 超限自动归档最低分)
	MaxActivePerScope int `env:"MEMORY_MAX_ACTIVE_PER_SCOPE" envDefault:"500"`
	// SessionSummaryThreshold 会话滚动摘要触发阈值 (M10.2, 会话 user/assistant 消息数)
	SessionSummaryThreshold int `env:"MEMORY_SESSION_SUMMARY_THRESHOLD" envDefault:"40"`
	// EmbedModel 语义检索 (M10.3) 向量专用 ModelTemplate 名称; 空 = 语义检索整体不生效 (纯关键词检索)
	EmbedModel string `env:"MEMORY_EMBED_MODEL"`
	// EmbedTimeout 向量计算 (查询/写入/回填) 单次超时
	EmbedTimeout time.Duration `env:"MEMORY_EMBED_TIMEOUT" envDefault:"10s"`
}

func Load(envFile string) (*Config, error) {
	// 加载 .env 文件
	if err := godotenv.Load(envFile); err != nil {
		log.Println("no .env file found, using env vars")
	}

	// 解析环境变量
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config parse error: %w", err)
	}

	// 验证必填字段
	if cfg.JWT.Secret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if cfg.Database.User == "" || cfg.Database.Password == "" {
		return nil, fmt.Errorf("DB_USER and DB_PASSWORD are required")
	}
	if cfg.MCP.CredentialsKey == "" {
		return nil, fmt.Errorf("MCP_CREDENTIALS_KEY is required (64 hex chars, e.g. openssl rand -hex 32)")
	}
	if cfg.Model.CredentialsKey == "" {
		return nil, fmt.Errorf("MODEL_CREDENTIALS_KEY is required (64 hex chars, e.g. openssl rand -hex 32)")
	}

	return cfg, nil
}
