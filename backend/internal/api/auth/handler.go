package auth

import (
	"agent-platform/internal/middleware"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc service.AuthService
}

func NewHandler() *Handler {
	repo := repository.NewUserRepository()
	svc := service.NewAuthService(repo)
	return &Handler{svc: svc}
}

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// Login 用户登录
func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	user, token, err := h.svc.Login(req.Username, req.Password)
	if err != nil {
		response.Unauthorized(c, "invalid username or password")
		return
	}

	response.Success(c, gin.H{
		"token": token,
		"user": gin.H{
			"id":       user.ID,
			"username": user.Username,
			"email":    user.Email,
		},
	})
}

// Register 用户注册
func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	user, err := h.svc.Register(req.Username, req.Email, req.Password)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Created(c, gin.H{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
	})
}

// Logout 用户登出
func (h *Handler) Logout(c *gin.Context) {
	response.Success(c, gin.H{"message": "logout successfully"})
}

// RegisterRoutes 注册路由
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	auth := router.Group("/auth")
	auth.Use(middleware.AuthSkip())
	{
		auth.POST("/login", h.Login)
		auth.POST("/register", h.Register)
		auth.POST("/logout", h.Logout)
	}
}
