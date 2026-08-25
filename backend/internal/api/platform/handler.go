package platform

import (
	"agent-platform/internal/middleware"
	"agent-platform/internal/service"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

// Handler 平台设置 API (平台名 / 平台图标)
type Handler struct {
	svc service.PlatformService
}

func NewHandler(svc service.PlatformService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 注册路由
// GET 公开 (登录页/侧边导航需展示品牌信息, 非敏感); PUT 需 platform:manage 权限
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/platform")
	{
		group.GET("/settings", h.GetSettings)
		group.PUT("/settings", middleware.AuthCheck("platform:manage"), h.UpdateSettings)
	}
}

// GetSettings 获取平台设置 (平台名 + 平台图标)
func (h *Handler) GetSettings(c *gin.Context) {
	info, err := h.svc.Get(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, info)
}

// UpdateSettings 更新平台设置 (平台名 + 平台图标)
func (h *Handler) UpdateSettings(c *gin.Context) {
	var req service.UpdatePlatformRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	userID := c.GetString("user_id")
	info, err := h.svc.Update(c.Request.Context(), req, &userID, c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, info)
}
