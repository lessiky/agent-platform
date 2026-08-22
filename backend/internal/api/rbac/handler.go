package rbac

import (
	"strconv"

	"agent-platform/internal/middleware"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

// Handler 用户/角色/权限管理 API (M1 遗留: RBAC 落地 + M4.5 遗留: RBAC 管理 UI)
type Handler struct {
	svc *service.RBACService
}

func NewHandler(svc *service.RBACService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 注册路由 (用户管理需 user:manage, 角色/权限管理需 role:manage, 均可由 admin 使用)
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	users := router.Group("/users")
	users.Use(middleware.Auth())
	{
		users.GET("", middleware.AuthCheck("user:manage"), h.ListUsers)
		users.POST("", middleware.AuthCheck("user:manage"), h.CreateUser)
		users.GET("/:id", middleware.AuthCheck("user:manage"), h.GetUser)
		users.PUT("/:id", middleware.AuthCheck("user:manage"), h.UpdateUser)
		users.PUT("/:id/roles", middleware.AuthCheck("user:manage"), h.AssignUserRoles)
		users.DELETE("/:id", middleware.AuthCheck("user:manage"), h.DeleteUser)
	}

	roles := router.Group("/roles")
	roles.Use(middleware.Auth())
	{
		roles.GET("", middleware.AuthCheck("role:manage"), h.ListRoles)
		roles.POST("", middleware.AuthCheck("role:manage"), h.CreateRole)
		roles.PUT("/:id", middleware.AuthCheck("role:manage"), h.UpdateRole)
		roles.DELETE("/:id", middleware.AuthCheck("role:manage"), h.DeleteRole)
	}

	router.GET("/permissions", middleware.Auth(), middleware.AuthCheck("role:manage"), h.ListPermissions)

	// 当前登录用户信息 (含角色与权限码), 供前端菜单/按钮级权限控制
	router.GET("/auth/me", middleware.Auth(), h.Me)
}

// ---------- 用户 ----------

// ListUsers 用户列表 (q 模糊搜索, status 过滤, page/size 分页)
func (h *Handler) ListUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	var status *int8
	if raw := c.Query("status"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err == nil {
			s := int8(v)
			status = &s
		}
	}

	items, total, err := h.svc.ListUsers(c.Request.Context(), repository.UserListFilter{
		Keyword:  c.Query("q"),
		Status:   status,
		Page:     page,
		PageSize: size,
	})
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

// CreateUser 创建用户 (roles 为空时分配默认 user 角色)
func (h *Handler) CreateUser(c *gin.Context) {
	var req service.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	user, err := h.svc.CreateUser(c.Request.Context(), req, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, gin.H{"id": user.ID, "username": user.Username})
}

// GetUser 用户详情 (含角色)
func (h *Handler) GetUser(c *gin.Context) {
	item, err := h.svc.GetUser(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, item)
}

// UpdateUser 更新用户 (email / status / password, 均可为空表示不变)
func (h *Handler) UpdateUser(c *gin.Context) {
	var req service.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	user, err := h.svc.UpdateUser(c.Request.Context(), c.Param("id"), req, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"id": user.ID, "username": user.Username})
}

// AssignUserRoles 全量替换用户角色
func (h *Handler) AssignUserRoles(c *gin.Context) {
	var req service.AssignUserRolesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	roles, err := h.svc.AssignUserRoles(c.Request.Context(), c.Param("id"), req.Roles, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"roles": roles})
}

// DeleteUser 删除用户 (保护: 不可删自己/最后一个管理员)
func (h *Handler) DeleteUser(c *gin.Context) {
	if err := h.svc.DeleteUser(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP()); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"message": "deleted"})
}

// ---------- 角色 ----------

// ListRoles 角色列表 (含权限码与用户数)
func (h *Handler) ListRoles(c *gin.Context) {
	items, err := h.svc.ListRoles(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

// CreateRole 创建角色 (permissions 为权限码列表)
func (h *Handler) CreateRole(c *gin.Context) {
	var req service.CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	role, err := h.svc.CreateRole(c.Request.Context(), req, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, role)
}

// UpdateRole 更新角色 (description / status / permissions, permissions 非 null 时全量替换)
func (h *Handler) UpdateRole(c *gin.Context) {
	var req service.UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	role, err := h.svc.UpdateRole(c.Request.Context(), c.Param("id"), req, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, role)
}

// DeleteRole 删除角色 (内置角色与已分配角色不可删)
func (h *Handler) DeleteRole(c *gin.Context) {
	if err := h.svc.DeleteRole(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP()); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"message": "deleted"})
}

// ---------- 权限 ----------

// ListPermissions 权限点定义列表
func (h *Handler) ListPermissions(c *gin.Context) {
	items, err := h.svc.ListPermissions(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

// Me 当前登录用户 (含角色与权限码)
func (h *Handler) Me(c *gin.Context) {
	me, err := h.svc.Me(c.Request.Context(), c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, me)
}
