package mcp

import (
    "strconv"
    "time"

    "agent-platform/internal/middleware"
    "agent-platform/internal/repository"
    "agent-platform/internal/service"
    "agent-platform/pkg/response"

    "github.com/gin-gonic/gin"
)

// ApprovalHandler MCP 工具调用人工审核 API (M4.5, PRD 6.2)
type ApprovalHandler struct {
    svc service.ApprovalService
}

func NewApprovalHandler(svc service.ApprovalService) *ApprovalHandler {
    return &ApprovalHandler{svc: svc}
}

// RegisterRoutes 注册审核路由
func (h *ApprovalHandler) RegisterRoutes(router *gin.RouterGroup) {
    approvals := router.Group("/approvals")
    approvals.Use(middleware.Auth())
    {
        approvals.GET("", middleware.AuthCheck("mcp:read"), h.List)
        approvals.GET("/settings", middleware.AuthCheck("mcp:read"), h.GetSettings)
        approvals.PUT("/settings", middleware.AuthCheck("mcp:write"), h.UpdateSettings)
        approvals.GET("/:id", middleware.AuthCheck("mcp:read"), h.Get)
        approvals.POST("/:id/approve", middleware.AuthCheck("mcp:approve"), h.Approve)
        approvals.POST("/:id/reject", middleware.AuthCheck("mcp:approve"), h.Reject)
    }
}

// List 审核请求列表 (status / mcp_server_id / tool / agent_id / source / from / to 过滤)
func (h *ApprovalHandler) List(c *gin.Context) {
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

    var from, to *time.Time
    if raw := c.Query("from"); raw != "" {
        if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
            from = &parsed
        }
    }
    if raw := c.Query("to"); raw != "" {
        if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
            to = &parsed
        }
    }

    filter := repository.ApprovalListFilter{
        Status:      c.Query("status"),
        MCPServerID: c.Query("mcp_server_id"),
        ToolName:    c.Query("tool"),
        AgentID:     c.Query("agent_id"),
        Source:      c.Query("source"),
        From:        from,
        To:          to,
        Page:        page,
        PageSize:    size,
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

// Get 审核详情 (含参数快照与执行结果)
func (h *ApprovalHandler) Get(c *gin.Context) {
    approval, err := h.svc.Get(c.Request.Context(), c.Param("id"))
    if err != nil {
        response.Error(c, err)
        return
    }
    response.Success(c, approval)
}

// Approve 通过并执行工具 (mcp:approve 权限)
func (h *ApprovalHandler) Approve(c *gin.Context) {
    var req struct {
        Comment string `json:"comment" binding:"max=512"`
    }
    if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
        response.BadRequest(c, "invalid request body: "+err.Error())
        return
    }
    approval, err := h.svc.Approve(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP(), req.Comment)
    if err != nil {
        response.Error(c, err)
        return
    }
    response.Success(c, approval)
}

// Reject 驳回 (mcp:approve 权限)
func (h *ApprovalHandler) Reject(c *gin.Context) {
    var req struct {
        Comment string `json:"comment" binding:"max=512"`
    }
    if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
        response.BadRequest(c, "invalid request body: "+err.Error())
        return
    }
    approval, err := h.svc.Reject(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP(), req.Comment)
    if err != nil {
        response.Error(c, err)
        return
    }
    response.Success(c, approval)
}

// GetSettings 审核全局配置
func (h *ApprovalHandler) GetSettings(c *gin.Context) {
    settings, err := h.svc.GetSettings(c.Request.Context())
    if err != nil {
        response.Error(c, err)
        return
    }
    response.Success(c, settings)
}

// UpdateSettings 更新审核全局配置
func (h *ApprovalHandler) UpdateSettings(c *gin.Context) {
    var req service.UpdateApprovalSettingsRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        response.BadRequest(c, "invalid request body: "+err.Error())
        return
    }
    settings, err := h.svc.UpdateSettings(c.Request.Context(), req, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
    if err != nil {
        response.Error(c, err)
        return
    }
    response.Success(c, settings)
}