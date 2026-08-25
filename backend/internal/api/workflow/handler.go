package workflow

import (
	"context"
	"encoding/json"
	"strconv"

	"agent-platform/internal/middleware"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	"agent-platform/pkg/errors"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

// AIGenerator AI 工作流生成能力 (由 service.WorkflowAIGenerator 满足)
type AIGenerator interface {
	Generate(ctx context.Context, req service.AIGenerateWorkflowRequest) (*service.AIGenerateWorkflowResult, error)
}

// Handler 工作流 HTTP handler (M5)
type Handler struct {
	svc   service.WorkflowService
	aiGen AIGenerator
}

func NewHandler(svc service.WorkflowService, aiGen AIGenerator) *Handler {
	return &Handler{svc: svc, aiGen: aiGen}
}

// RegisterRoutes 注册工作流路由 (PRD 2.4)
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	workflows := router.Group("/workflows")
	workflows.Use(middleware.Auth())
	{
		workflows.POST("", middleware.AuthCheck("workflow:write"), h.Create)
		workflows.GET("", middleware.AuthCheck("workflow:read"), h.List)
		workflows.GET("/dashboard", middleware.AuthCheck("workflow:read"), h.Dashboard)
		workflows.POST("/validate", middleware.AuthCheck("workflow:write"), h.Validate)
		workflows.POST("/ai-generate", middleware.AuthCheck("workflow:write"), h.AIGenerate)

		workflows.POST("/:id/validate", middleware.AuthCheck("workflow:write"), h.Validate)
		workflows.GET("/:id", middleware.AuthCheck("workflow:read"), h.Get)
		workflows.PUT("/:id", middleware.AuthCheck("workflow:write"), h.Update)
		workflows.DELETE("/:id", middleware.AuthCheck("workflow:write"), h.Delete)

		workflows.POST("/:id/activate", middleware.AuthCheck("workflow:write"), h.Activate)
		workflows.POST("/:id/archive", middleware.AuthCheck("workflow:write"), h.Archive)
		workflows.PUT("/:id/schedule", middleware.AuthCheck("workflow:write"), h.UpdateSchedule)
		workflows.POST("/:id/trigger", middleware.AuthCheck("workflow:execute"), h.Trigger)
		workflows.GET("/:id/versions", middleware.AuthCheck("workflow:read"), h.ListVersions)
		workflows.GET("/:id/executions", middleware.AuthCheck("workflow:read"), h.ListExecutions)
	}

	// 执行记录 (独立前缀避免与 /workflows/:id 路由冲突)
	executions := router.Group("/workflow-executions")
	executions.Use(middleware.Auth())
	{
		executions.GET("/:id", middleware.AuthCheck("workflow:read"), h.GetExecution)
		executions.POST("/:id/cancel", middleware.AuthCheck("workflow:execute"), h.CancelExecution)
	}
}

// RegisterWebhookRoutes 注册公开 webhook 端点 (token 鉴权, 无 JWT)
func (h *Handler) RegisterWebhookRoutes(router *gin.Engine) {
	router.POST("/api/v1/webhooks/workflows/:token", h.Webhook)
	// 执行状态公开查询: 外部系统仅凭 webhook token 轮询本工作流的执行状态
	router.GET("/api/v1/webhooks/workflows/:token/executions/:id", h.WebhookExecution)
}

// Create 创建工作流
func (h *Handler) Create(c *gin.Context) {
	var req service.CreateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	workflow, err := h.svc.Create(c.Request.Context(), req, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, workflow)
}

// List 分页列表 (status 过滤)
func (h *Handler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	items, total, err := h.svc.List(c.Request.Context(), repository.WorkflowListFilter{
		Status: c.Query("status"),
		Page:   page,
		Size:   size,
	})
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": size,
	})
}

// Dashboard 执行看板 (实时状态统计 + 最近执行)
func (h *Handler) Dashboard(c *gin.Context) {
	data, err := h.svc.ExecutionDashboard(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, data)
}

// Get 详情
func (h *Handler) Get(c *gin.Context) {
	workflow, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, workflow)
}

// Update 更新 (DAG 变更会生成新版本快照)
func (h *Handler) Update(c *gin.Context) {
	var req service.UpdateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	workflow, err := h.svc.Update(c.Request.Context(), c.Param("id"), req, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, workflow)
}

// Delete 删除 (存在活动执行时拒绝)
func (h *Handler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": c.Param("id")})
}

// Validate 校验 DAG 定义 (不落库)
func (h *Handler) Validate(c *gin.Context) {
	var body struct {
		Definition interface{} `json:"definition"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Definition == nil {
		response.BadRequest(c, "invalid request body: definition 必填")
		return
	}
	raw, err := json.Marshal(body.Definition)
	if err != nil {
		response.BadRequest(c, "definition 序列化失败: "+err.Error())
		return
	}
	if err := h.svc.ValidateDefinition(c.Request.Context(), raw); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"valid": true})
}

// AIGenerate AI 自动生成工作流 (M5 Phase 2): 自然语言描述 -> LLM 生成并校验 DAG 定义
// 不落库: 前端拿到草稿后在编辑器中人工确认, 再经 Create/Update 保存
func (h *Handler) AIGenerate(c *gin.Context) {
	var req service.AIGenerateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: description 必填 (≤2000 字)")
		return
	}
	if h.aiGen == nil {
		response.Error(c, &errors.AppError{Code: "ai_generate_unavailable", Message: "AI 生成未启用", HTTPCode: 503})
		return
	}
	result, err := h.aiGen.Generate(c.Request.Context(), req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// Activate 激活 (draft/archived -> active)
func (h *Handler) Activate(c *gin.Context) {
	workflow, err := h.svc.Activate(c.Request.Context(), c.Param("id"), c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, workflow)
}

// Archive 归档 (active -> archived, 停止调度)
func (h *Handler) Archive(c *gin.Context) {
	workflow, err := h.svc.Archive(c.Request.Context(), c.Param("id"), c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, workflow)
}

// UpdateSchedule 更新定时调度
func (h *Handler) UpdateSchedule(c *gin.Context) {
	var req service.UpdateScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	workflow, err := h.svc.UpdateSchedule(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, workflow)
}

// Trigger 手动触发
func (h *Handler) Trigger(c *gin.Context) {
	var req struct {
		Input map[string]interface{} `json:"input"`
	}
	_ = c.ShouldBindJSON(&req) // 允许空 body
	execution, err := h.svc.Trigger(c.Request.Context(), c.Param("id"), req.Input, "manual", strPtrOrEmpty(c.GetString("user_id")))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, execution)
}

// ListVersions 版本快照列表
func (h *Handler) ListVersions(c *gin.Context) {
	versions, err := h.svc.ListVersions(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": versions})
}

// ListExecutions 执行历史 (status/trigger 过滤)
func (h *Handler) ListExecutions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	items, total, err := h.svc.ListExecutions(c.Request.Context(), repository.ExecutionListFilter{
		WorkflowID: c.Param("id"),
		Status:     c.Query("status"),
		Trigger:    c.Query("trigger"),
		Page:       page,
		PageSize:   size,
	})
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": size,
	})
}

// GetExecution 执行详情 (含节点级记录)
func (h *Handler) GetExecution(c *gin.Context) {
	detail, err := h.svc.GetExecution(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, detail)
}

// CancelExecution 取消执行
func (h *Handler) CancelExecution(c *gin.Context) {
	if err := h.svc.CancelExecution(c.Request.Context(), c.Param("id")); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"cancelled": c.Param("id")})
}

// Webhook 事件触发 (公开端点: POST /api/v1/webhooks/workflows/:token)
func (h *Handler) Webhook(c *gin.Context) {
	payload, err := c.GetRawData()
	if err != nil {
		response.BadRequest(c, "读取请求体失败")
		return
	}
	execution, err := h.svc.HandleWebhook(c.Request.Context(), c.Param("token"), payload)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, execution)
}

// WebhookExecution 通过 webhook token 查询执行状态
// 公开端点: GET /api/v1/webhooks/workflows/:token/executions/:id
// token 仅能查询其所属工作流的执行; 仅返回状态视图 (不含输入/输出 payload), 完整详情用 /workflow-executions/:id (JWT)
func (h *Handler) WebhookExecution(c *gin.Context) {
	view, err := h.svc.GetExecutionByWebhookToken(c.Request.Context(), c.Param("token"), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, view)
}

func strPtrOrEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
