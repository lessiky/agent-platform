package overview

import (
	"agent-platform/internal/middleware"
	"agent-platform/internal/service"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc service.OverviewService
}

func NewHandler(svc service.OverviewService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 注册概览路由
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	overview := router.Group("/overview")
	overview.Use(middleware.Auth())
	{
		overview.GET("/summary", h.Summary)
	}
}

// Summary 概览页基本情况统计 (Agent / MCP / 模型 / 工作流 / 审核)
func (h *Handler) Summary(c *gin.Context) {
	data, err := h.svc.Summary(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, data)
}
