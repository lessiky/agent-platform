package config

import (
	"fmt"
	"log"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	JWT      JWTConfig
	MCP      MCPConfig
	Model    ModelConfig
	Memory   MemoryConfig
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
	// ChatTimeout 单次对话调用超时 (LLM 生成耗时较长, 需长于探测超时)
	ChatTimeout time.Duration `env:"MODEL_CHAT_TIMEOUT" envDefault:"120s"`
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
