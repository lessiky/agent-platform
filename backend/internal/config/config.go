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
