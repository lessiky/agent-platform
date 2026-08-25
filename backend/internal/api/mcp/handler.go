package mcp

import (
	"strconv"

	"agent-platform/internal/middleware"
	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

// Handler MCP HTTP handler (只解析请求, 业务在 service)
type Handler struct {
	svc       service.MCPServerService
	approvals service.ApprovalService
}

func NewHandler(svc service.MCPServerService, approvals service.ApprovalService) *Handler {
	return &Handler{svc: svc, approvals: approvals}
}

// RegisterRoutes 注册 MCP 路由 (PRD 6.2 + 绑定扩展)
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	mcps := router.Group("/mcp-servers")
	mcps.Use(middleware.Auth())
	{
		mcps.POST("", middleware.AuthCheck("mcp:write"), h.Create)
		mcps.GET("", middleware.AuthCheck("mcp:read"), h.List)

		mcps.GET("/:id", middleware.AuthCheck("mcp:read"), h.Get)
		mcps.PUT("/:id", middleware.AuthCheck("mcp:write"), h.Update)
		mcps.DELETE("/:id", middleware.AuthCheck("mcp:write"), h.Delete)

		mcps.POST("/:id/test", middleware.AuthCheck("mcp:write"), h.Test)
		mcps.GET("/:id/health", middleware.AuthCheck("mcp:read"), h.Health)

		mcps.GET("/:id/tools", middleware.AuthCheck("mcp:read"), h.ListTools)
		mcps.PUT("/:id/tools", middleware.AuthCheck("mcp:write"), h.UpdateToolApprovals)
		mcps.POST("/:id/tools/call", middleware.AuthCheck("mcp:write"), h.CallTool)

		mcps.GET("/:id/agents", middleware.AuthCheck("mcp:read"), h.ListAgents)
		mcps.POST("/:id/agents", middleware.AuthCheck("mcp:write"), h.BindAgent)
		mcps.DELETE("/:id/agents/:agentId", middleware.AuthCheck("mcp:write"), h.UnbindAgent)
	}
}

// Create 注册 MCP 服务器
func (h *Handler) Create(c *gin.Context) {
	var req service.CreateMCPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	server, err := h.svc.Create(c.Request.Context(), req, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, server)
}

// List 分页列表 (q 搜索, status/tag 过滤)
func (h *Handler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	filter := repository.MCPListFilter{
		Keyword:  c.Query("q"),
		Status:   c.Query("status"),
		Tag:      c.Query("tag"),
		Page:     page,
		PageSize: size,
	}
	servers, total, err := h.svc.List(c.Request.Context(), filter)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"items":     servers,
		"total":     total,
		"page":      page,
		"page_size": size,
	})
}

// Get 详情 (含凭证脱敏视图)
func (h *Handler) Get(c *gin.Context) {
	server, credentials, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"server":      server,
		"credentials": credentials,
	})
}

// Update 更新配置
func (h *Handler) Update(c *gin.Context) {
	var req service.UpdateMCPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	server, err := h.svc.Update(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, server)
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

// ListTools 已发现的工具列表
func (h *Handler) ListTools(c *gin.Context) {
	tools, err := h.svc.ListTools(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"tools": tools})
}

// CallTool 工具调用代理 (M4.5: 需审核工具返回 202 + 审核请求 ID, 不直接执行)
func (h *Handler) CallTool(c *gin.Context) {
	var req struct {
		Name      string                 `json:"name" binding:"required,max=128"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	outcome, err := h.svc.CallTool(c.Request.Context(), c.Param("id"), req.Name, req.Arguments, service.CallOptions{Source: model.ApprovalSourceManual})
	if err != nil {
		response.Error(c, err)
		return
	}
	if outcome.PendingApproval != nil {
		response.Accepted(c, gin.H{
			"approval_id": outcome.PendingApproval.ID,
			"approval":    outcome.PendingApproval,
		})
		return
	}
	response.Success(c, outcome.Result)
}

// UpdateToolApprovals 更新工具级审核配置 (M4.5)
func (h *Handler) UpdateToolApprovals(c *gin.Context) {
	var req service.UpdateToolApprovalsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	tools, err := h.approvals.UpdateToolApprovals(c.Request.Context(), c.Param("id"), req, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"tools": tools})
}

// ListAgents 绑定的 Agent 列表
func (h *Handler) ListAgents(c *gin.Context) {
	bindings, err := h.svc.ListBoundAgents(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"agents": bindings})
}

// BindAgent 绑定 Agent
func (h *Handler) BindAgent(c *gin.Context) {
	var req struct {
		AgentID string `json:"agent_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	if err := h.svc.BindAgent(c.Request.Context(), c.Param("id"), req.AgentID); err != nil {
		response.Error(c, err)
		return
	}
	c.JSON(200, response.Response{Code: "success", Message: "bound"})
}

// UnbindAgent 解绑 Agent
func (h *Handler) UnbindAgent(c *gin.Context) {
	if err := h.svc.UnbindAgent(c.Request.Context(), c.Param("id"), c.Param("agentId")); err != nil {
		response.Error(c, err)
		return
	}
	c.JSON(200, response.Response{Code: "success", Message: "unbound"})
}
