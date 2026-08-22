# 后端代码规范

> **版本：** v1.0
> **日期：** 2026-08-17
> **语言：** Go
> **框架：** Gin + GORM

---

## 1. 项目结构

`
backend/
├── cmd/
│   └── server/
│       └── main.go              # 服务入口
├── internal/
│   ├── api/                     # HTTP handler (按服务分)
│   │   ├── auth/
│   │   ├── agent/
│   │   ├── mcp/
│   │   ├── workflow/
│   │   └── model/               # 模型管理 API
│   ├── config/                  # 配置加载
│   ├── database/                # 数据库连接与迁移
│   ├── middleware/              # 中间件
│   ├── model/                   # 数据模型 (GORM struct)
│   ├── repository/              # 数据访问层
│   ├── service/                 # 业务逻辑层
│   └── worker/                  # 异步任务处理器
├── pkg/                         # 公共包
│   ├── logger/
│   ├── jwt/
│   ├── response/
│   └── errors/
├── migrations/                  # 数据库迁移文件
├── go.mod
├── go.sum
└── Makefile
`

### 1.1 目录职责

| 目录 | 职责 | 禁止事项 |
|------|------|----------|
| api/ | 解析请求、调用 service、返回响应 | 不含业务逻辑 |
| service/ | 业务逻辑、调用 repository | 不含 HTTP 细节 |
| repository/ | 数据库 CRUD | 不含业务逻辑 |
| model/ | GORM 模型定义 | 不含业务逻辑 |
| middleware/ | 认证、日志、CORS、限流 | 不含业务逻辑 |
| pkg/ | 跨服务共享的公共代码 | 不引用 internal |

---

## 2. 命名规范

### 2.1 通用命名

| 类型 | 规范 | 示例 |
|------|------|------|
| 包名 | 小写英文，简短 | agent，非 agent_service |
| 接口名 | 1-2 字母后缀 | Reader、Writer、Store |
| 接口方法 | 动词开头 | GetAgent、ListAgents |
| Struct 字段 | 大驼峰 | AgentName、CreatedAt |
| 错误变量 | 小写，带前缀 | ErrNotFound、ErrInvalidInput |

### 2.2 命名约定

`go
// GOOD
type AgentRepository interface {
    GetByID(ctx context.Context, id string) (*model.Agent, error)
    List(ctx context.Context, req ListRequest) ([]*model.Agent, error)
}

type agentRepo struct {
    db *gorm.DB
}

func NewAgentRepository(db *gorm.DB) AgentRepository {
    return &agentRepo{db: db}
}

// AVOID
type AgentServiceImplementation struct {
    DB *gorm.DB
}

func (a *AgentServiceImplementation) agent_list_request(request Request) ([]*model.Agent, error) {
}
`

---

## 3. 分层架构

### 3.1 请求处理链

`
HTTP Request
    |
    v
+-------------+
|  Middleware  |  (Auth, Logger, Recovery)
+-----+-------+
      |
      v
+-------------+
|    Router    |  (路由分发)
+-----+-------+
      |
      v
+-------------+
|    Handler   |  (解析请求参数)
+-----+-------+
      |
      v
+-------------+
|  Service     |  (业务逻辑)
+-----+-------+
      |
      v
+-------------+
| Repository   |  (数据访问)
+-----+-------+
      |
      v
  Database
`

### 3.2 Handler 示例

`go
// internal/api/agent/handler.go
package agent

import (
    "net/http"
    "github.com/gin-gonic/gin"
    "agent-platform/pkg/response"
    "agent-platform/internal/service"
)

type Handler struct {
    svc service.AgentService
}

func NewHandler(svc service.AgentService) *Handler {
    return &Handler{svc: svc}
}

func (h *Handler) CreateAgent(c *gin.Context) {
    var req service.CreateAgentRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        response.BadRequest(c, "invalid request body")
        return
    }

    agent, err := h.svc.CreateAgent(c.Request.Context(), req)
    if err != nil {
        response.Error(c, err)
        return
    }

    response.Created(c, agent)
}

func (h *Handler) Register(router *gin.RouterGroup) {
    h := h
    router.POST("/agents", h.CreateAgent)
    router.GET("/agents", h.ListAgents)
    router.GET("/agents/:id", h.GetAgent)
    router.PUT("/agents/:id", h.UpdateAgent)
    router.DELETE("/agents/:id", h.DeleteAgent)
}
`

### 3.3 Service 示例

`go
// internal/service/agent_service.go
package service

import (
    "context"
    "agent-platform/internal/model"
    "agent-platform/internal/repository"
    "agent-platform/pkg/errors"
)

type CreateAgentRequest struct {
    Name        string json:"name"
    Description string json:"description"
    Model       string json:"model"
}

type AgentService interface {
    CreateAgent(ctx context.Context, req CreateAgentRequest) (*model.Agent, error)
    GetByID(ctx context.Context, id string) (*model.Agent, error)
    List(ctx context.Context, req ListRequest) ([]*model.Agent, error)
}

type agentService struct {
    repo repository.AgentRepository
}

func NewAgentService(repo repository.AgentRepository) AgentService {
    return &agentService{repo: repo}
}

func (s *agentService) CreateAgent(ctx context.Context, req CreateAgentRequest) (*model.Agent, error) {
    // 1. 参数校验
    if req.Name == "" {
        return nil, errors.NewValidationError("name is required")
    }

    // 2. 业务逻辑
    agent := &model.Agent{
        Name:        req.Name,
        Description: req.Description,
        Model:       req.Model,
    }

    // 3. 调用 repository
    if err := s.repo.Create(ctx, agent); err != nil {
        return nil, errors.Wrap(err, "failed to create agent")
    }

    return agent, nil
}
`

---

## 4. 错误处理规范

### 4.1 自定义错误类型

`go
// pkg/errors/errors.go
package errors

import "fmt"

// AppError 应用层错误
type AppError struct {
    Code     string
    Message  string
    HTTPCode int
}

func (e *AppError) Error() string {
    return e.Message
}

// 预定义错误
var (
    ErrNotFound      = &AppError{Code: "not_found", Message: "资源不存在", HTTPCode: 404}
    ErrUnauthorized  = &AppError{Code: "unauthorized", Message: "未授权", HTTPCode: 401}
    ErrForbidden     = &AppError{Code: "forbidden", Message: "权限不足", HTTPCode: 403}
    ErrValidationError = &AppError{Code: "validation_error", Message: "参数校验失败", HTTPCode: 400}
    ErrInternal      = &AppError{Code: "internal_error", Message: "服务器内部错误", HTTPCode: 500}
)

// Wrap 包装错误
func Wrap(err error, msg string) *AppError {
    return &AppError{
        Code:    "wrapped_error",
        Message: fmt.Sprintf("%s: %v", msg, err),
        HTTPCode: 500,
    }
}
`

### 4.2 错误使用原则

| 原则 | 说明 |
|------|------|
| 不要吞错误 | 每个 if err != nil 都要处理 |
| 不要直接返回 err | 至少用 Wrap 包装 |
| 使用有意义的错误码 | 前端可按 Code 判断处理 |
| 不要暴露内部错误 | 生产环境不返回堆栈 |

`go
// GOOD
if err := s.repo.Create(ctx, agent); err != nil {
    return nil, errors.Wrap(err, "failed to create agent")
}

// AVOID
if err := s.repo.Create(ctx, agent); err != nil {
    log.Println(err)
    return nil, err
}
`

---

## 5. 数据库操作规范

### 5.1 Repository 层

`go
// internal/repository/agent_repository.go
package repository

import (
    "context"
    "agent-platform/internal/model"
    "gorm.io/gorm"
)

type AgentRepository interface {
    Create(ctx context.Context, agent *model.Agent) error
    GetByID(ctx context.Context, id string) (*model.Agent, error)
    List(ctx context.Context, req ListRequest) ([]*model.Agent, error)
    Update(ctx context.Context, agent *model.Agent) error
    Delete(ctx context.Context, id string) error
}

type agentRepo struct {
    db *gorm.DB
}

func NewAgentRepository(db *gorm.DB) AgentRepository {
    return &agentRepo{db: db}
}

func (r *agentRepo) Create(ctx context.Context, agent *model.Agent) error {
    return r.db.WithContext(ctx).Create(agent).Error
}

func (r *agentRepo) GetByID(ctx context.Context, id string) (*model.Agent, error) {
    var agent model.Agent
    err := r.db.WithContext(ctx).Where("id = ?", id).First(&agent).Error
    if err != nil {
        return nil, err
    }
    return &agent, nil
}
`

### 5.2 模型定义规范

`go
// internal/model/agent.go
package model

import (
    "time"
    "gorm.io/gorm"
)

type Agent struct {
    ID           string         gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"
    Name         string         gorm:"type:varchar(255);not null" json:"name"
    Description  string         gorm:"type:text" json:"description"
    Model        string         gorm:"type:varchar(128)" json:"model"
    Status       string         gorm:"type:varchar(32);default:idle" json:"status"
    Config       string         gorm:"type:jsonb" json:"config"
    TeamID       string         gorm:"type:uuid;index" json:"team_id"
    CreatedAt    time.Time      json:"created_at"
    UpdatedAt    time.Time      json:"updated_at"
    DeletedAt    gorm.DeletedAt gorm:"index" json:"-"
}

func (Agent) TableName() string {
    return "agents"
}
`

---

## 6. API 响应规范

### 6.1 统一响应格式

`go
// pkg/response/response.go
package response

import (
    "net/http"
    "github.com/gin-gonic/gin"
)

type Response struct {
    Code    string      json:"code"
    Message string      json:"message"
    Data    interface{} json:"data,omitempty"
    TraceID string      json:"trace_id,omitempty"
}

func Success(c *gin.Context, data interface{}) {
    c.JSON(http.StatusOK, Response{
        Code:    "success",
        Message: "ok",
        Data:    data,
        TraceID: getTraceID(c),
    })
}

func Created(c *gin.Context, data interface{}) {
    c.JSON(http.StatusCreated, Response{
        Code:    "success",
        Message: "created",
        Data:    data,
    })
}

func Error(c *gin.Context, err error) {
    appErr, ok := err.(*errors.AppError)
    if !ok {
        appErr = errors.ErrInternal
    }
    c.JSON(appErr.HTTPCode, Response{
        Code:    appErr.Code,
        Message: appErr.Message,
        TraceID: getTraceID(c),
    })
}

func BadRequest(c *gin.Context, msg string) {
    c.JSON(http.StatusBadRequest, Response{
        Code:    "validation_error",
        Message: msg,
    })
}
`

### 6.2 分页响应格式

`go
type PaginatedResponse struct {
    Items  interface{} json:"items"
    Total  int64       json:"total"
    Page   int         json:"page"
    Size   int         json:"size"
}
`

---

## 7. 中间件规范

### 7.1 必需中间件

| 中间件 | 作用 | 顺序 |
|--------|------|------|
| Recovery | 捕获 panic，返回 500 | 1 |
| Logger | 记录请求日志 | 2 |
| TraceID | 生成/传递 trace ID | 3 |
| CORS | 跨域处理 | 4 |
| Auth | JWT 验证 (公开路由跳过) | 5 |
| RBAC | 权限校验 (需要权限的路由) | 6 |

### 7.2 示例

`go
func SetupRoutes(r *gin.Engine) {
    r.Use(middleware.Recovery())
    r.Use(middleware.Logger())
    r.Use(middleware.TraceID())
    r.Use(middleware.CORS())

    api := r.Group("/api/v1")
    {
        // 公开路由
        auth := api.Group("/auth")
        auth.Use(middleware.AuthSkip())
        auth.POST("/login", handler.Login)
        auth.POST("/register", handler.Register)

        // 需要认证的路由
        agents := api.Group("/agents")
        agents.Use(middleware.Auth())
        agents.Use(middleware.RBAC("agent:read"))
        agents.GET("", handler.ListAgents)
        agents.GET("/:id", handler.GetAgent)
    }
}
`

---

## 8. 配置管理

### 8.1 配置结构

`go
// internal/config/config.go
package config

type Config struct {
    Server   ServerConfig
    Database DatabaseConfig
    Redis    RedisConfig
    RabbitMQ RabbitMQConfig
    JWT      JWTConfig
}

type ServerConfig struct {
    Port     int    env:"SERVER_PORT" envDefault:"8080"
    Mode     string env:"SERVER_MODE" envDefault:"release"
}

type DatabaseConfig struct {
    Host     string env:"DB_HOST" envDefault:"localhost"
    Port     int    env:"DB_PORT" envDefault:"5432"
    User     string env:"DB_USER"
    Password string env:"DB_PASSWORD"
    Name     string env:"DB_NAME" envDefault:"agent_platform"
}
`

### 8.2 加载方式

`go
func Load(envFile string) (*Config, error) {
    // 1. 加载 .env 文件
    if err := godotenv.Load(envFile); err != nil {
        log.Println("no .env file found, using env vars")
    }

    // 2. 解析环境变量
    cfg := &Config{}
    if err := env.Parse(cfg); err != nil {
        return nil, fmt.Errorf("config parse error: %w", err)
    }

    return cfg, nil
}
`

---

## 9. 测试规范

### 9.1 测试结构

`
backend/
├── internal/
│   ├── api/
│   │   └── agent/
│   │       ├── handler.go
│   │       └── handler_test.go
│   ├── service/
│   │   ├── agent_service.go
│   │   └── agent_service_test.go
│   └── repository/
│       ├── agent_repository.go
│       └── agent_repository_test.go
`

### 9.2 测试原则

| 层级 | 工具 | 覆盖率要求 |
|------|------|------------|
| 单元测试 | testing + testify | Service/Repository > 80% |
| 集成测试 | testify/httptest | API Handler > 60% |
| 契约测试 | contracttest | 跨服务接口 |

### 9.3 测试示例

`go
// internal/service/agent_service_test.go
package service

import (
    "context"
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
)

func TestAgentService_CreateAgent(t *testing.T) {
    mockRepo := new(mockAgentRepository)
    svc := NewAgentService(mockRepo)

    mockRepo.On("Create", mock.Anything, mock.MatchedBy(func(a *model.Agent) bool {
        return a.Name == "test-agent"
    })).Return(nil).Once()

    req := CreateAgentRequest{
        Name: "test-agent",
        Model: "gpt-4",
    }

    agent, err := svc.CreateAgent(context.Background(), req)

    assert.NoError(t, err)
    assert.NotNil(t, agent)
    assert.Equal(t, "test-agent", agent.Name)
    mockRepo.AssertExpectations(t)
}
`

---

## 10. 代码审查清单

- [ ] 命名是否符合规范 (PascalCase / camelCase)
- [ ] 错误处理是否完整，有无遗漏
- [ ] 是否有业务逻辑泄漏到 handler
- [ ] 数据库查询是否使用了 WithContext
- [ ] 是否有 SQL 注入风险 (避免拼接)
- [ ] 敏感信息是否被记录到日志
- [ ] 测试是否覆盖主要路径
- [ ] API 响应格式是否统一
- [ ] 是否添加了必要的注释

---

*文档维护：代码规范随项目进展持续更新。*