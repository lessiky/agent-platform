package model

import (
	"strconv"

	"agent-platform/internal/middleware"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

// Handler 模型管理 HTTP handler (只解析请求, 业务在 service)
type Handler struct {
	svc service.ModelTemplateService
}

func NewHandler(svc service.ModelTemplateService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 注册模型管理路由 (PRD 6.3 + 用量扩展)
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	templates := router.Group("/model-templates")
	templates.Use(middleware.Auth())
	{
		templates.POST("", middleware.AuthCheck("model:write"), h.Create)
		templates.GET("", middleware.AuthCheck("model:read"), h.List)

		templates.GET("/:id", middleware.AuthCheck("model:read"), h.Get)
		templates.PUT("/:id", middleware.AuthCheck("model:write"), h.Update)
		templates.DELETE("/:id", middleware.AuthCheck("model:write"), h.Delete)

		templates.POST("/:id/test", middleware.AuthCheck("model:write"), h.Test)

		templates.POST("/:id/say-hi", middleware.AuthCheck("model:write"), h.SayHi)
		templates.GET("/:id/health", middleware.AuthCheck("model:read"), h.Health)
		templates.GET("/:id/usage", middleware.AuthCheck("model:read"), h.Usage)
	}

	quotas := router.Group("/model-quota")
	quotas.Use(middleware.Auth())
	{
		quotas.GET("", middleware.AuthCheck("model:read"), h.ListQuota)
		quotas.PUT("/:modelId", middleware.AuthCheck("model:write"), h.UpdateQuota)
	}

	router.GET("/model-usage", middleware.Auth(), middleware.AuthCheck("model:read"), h.UsageSummary)
	router.POST("/models/route", middleware.Auth(), middleware.AuthCheck("model:read"), h.Route)
}

// Create 注册模型模板
func (h *Handler) Create(c *gin.Context) {
	var req service.CreateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	t, credentials, err := h.svc.Create(c.Request.Context(), req, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, gin.H{
		"template":    t,
		"credentials": credentials,
	})
}

// List 分页列表 (q 搜索, provider/status/tag 过滤)
func (h *Handler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	filter := repository.ModelListFilter{
		Keyword:  c.Query("q"),
		Provider: c.Query("provider"),
		Status:   c.Query("status"),
		Tag:      c.Query("tag"),
		Page:     page,
		PageSize: size,
	}
	items, total, err := h.svc.List(c.Request.Context(), filter)
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

// Get 详情 (含 API Key 脱敏视图)
func (h *Handler) Get(c *gin.Context) {
	t, credentials, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"template":    t,
		"credentials": credentials,
	})
}

// Update 更新配置
func (h *Handler) Update(c *gin.Context) {
	var req service.UpdateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	t, credentials, err := h.svc.Update(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"template":    t,
		"credentials": credentials,
	})
}

// Delete 删除
func (h *Handler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		response.Error(c, err)
		return
	}
	c.JSON(200, response.Response{Code: "success", Message: "deleted"})
}

// Test 手动连通性测试
func (h *Handler) Test(c *gin.Context) {
	result, err := h.svc.Test(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// SayHi 发送Hi消息测试 (真实对话调用, 验证模型能否正常回复)
func (h *Handler) SayHi(c *gin.Context) {
	result, err := h.svc.SayHi(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// Health 健康状态 (最新状态 + 检查历史)
func (h *Handler) Health(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	data, err := h.svc.Health(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, data)
}

// Usage 单模型用量 (配额 + 最近调用日志)
func (h *Handler) Usage(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	data, err := h.svc.Usage(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, data)
}

// ListQuota 配额列表
func (h *Handler) ListQuota(c *gin.Context) {
	items, err := h.svc.ListQuota(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

// UpdateQuota 设置/更新配额 (0 = 不限)
func (h *Handler) UpdateQuota(c *gin.Context) {
	var req service.QuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	quota, err := h.svc.UpsertQuota(c.Request.Context(), c.Param("modelId"), req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, quota)
}

// UsageSummary 全部模型用量概览 (配额 + 近 24h 统计)
func (h *Handler) UsageSummary(c *gin.Context) {
	items, err := h.svc.UsageSummary(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

// Route 基础路由选择 (dry-run, 不消耗配额)
func (h *Handler) Route(c *gin.Context) {
	result, err := h.svc.Route(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}
