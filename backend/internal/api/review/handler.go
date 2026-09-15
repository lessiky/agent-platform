package review

import (
	"strconv"

	"agent-platform/internal/middleware"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

// Handler Agent/工作流审核 API (管理员权限)
type Handler struct {
	svc service.ReviewService
}

func NewHandler(svc service.ReviewService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 注册审核路由
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	reviews := router.Group("/reviews")
	reviews.Use(middleware.Auth())
	{
		// 审核队列 (type=agent|workflow, 需对应审核权限, 见 handler 内校验)
		reviews.GET("/pending", h.PendingList)
	}

	agents := router.Group("/agents")
	agents.Use(middleware.Auth())
	{
		agents.POST("/:id/review", middleware.AuthCheck("agent:review"), h.ReviewAgent)
	}

	workflows := router.Group("/workflows")
	workflows.Use(middleware.Auth())
	{
		workflows.POST("/:id/review", middleware.AuthCheck("workflow:review"), h.ReviewWorkflow)
	}
}

// PendingList 审核队列 (type=agent 需 agent:review, type=workflow 需 workflow:review)
func (h *Handler) PendingList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	kind := c.DefaultQuery("type", "agent")
	switch kind {
	case "workflow":
		if !h.requirePerm(c, "workflow:review") {
			return
		}
		items, total, err := h.svc.ListPendingWorkflows(c.Request.Context(), repository.WorkflowListFilter{Page: page, Size: size})
		if err != nil {
			response.Error(c, err)
			return
		}
		response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
	case "agent", "":
		if !h.requirePerm(c, "agent:review") {
			return
		}
		items, total, err := h.svc.ListPendingAgents(c.Request.Context(), repository.AgentListFilter{Page: page, PageSize: size})
		if err != nil {
			response.Error(c, err)
			return
		}
		response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
	default:
		response.BadRequest(c, "invalid type (agent|workflow)")
	}
}

// ReviewAgent 审核 Agent: {"decision":"approve|reject","comment":"..."} (驳回建议填意见)
func (h *Handler) ReviewAgent(c *gin.Context) {
	var req struct {
		Decision string `json:"decision" binding:"required,oneof=approve reject"`
		Comment  string `json:"comment" binding:"max=512"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	agent, err := h.svc.ReviewAgent(c.Request.Context(), c.Param("id"), req.Decision, req.Comment, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, agent)
}

// ReviewWorkflow 审核工作流: {"decision":"approve|reject","comment":"..."} (驳回建议填意见)
func (h *Handler) ReviewWorkflow(c *gin.Context) {
	var req struct {
		Decision string `json:"decision" binding:"required,oneof=approve reject"`
		Comment  string `json:"comment" binding:"max=512"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	workflow, err := h.svc.ReviewWorkflow(c.Request.Context(), c.Param("id"), req.Decision, req.Comment, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, workflow)
}

// requirePerm 校验当前用户持有任一给定权限码 (不满足则 403 并中止)
func (h *Handler) requirePerm(c *gin.Context, codes ...string) bool {
	perms := middleware.GetUserPermissions(c.Request.Context(), c.GetString("user_id"))
	set := make(map[string]bool, len(perms))
	for _, p := range perms {
		set[p] = true
	}
	for _, code := range codes {
		if set[code] {
			return true
		}
	}
	response.Forbidden(c, "permission denied")
	c.Abort()
	return false
}
