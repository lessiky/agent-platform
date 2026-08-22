package agent

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-platform/internal/middleware"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc  service.AgentService
	chat service.ChatService
}

func NewHandler(svc service.AgentService, chat service.ChatService) *Handler {
	return &Handler{svc: svc, chat: chat}
}

// RegisterRoutes 注册 Agent 路由
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	agents := router.Group("/agents")
	agents.Use(middleware.Auth())
	{
		agents.POST("", middleware.AuthCheck("agent:write"), h.CreateAgent)
		agents.GET("", middleware.AuthCheck("agent:read"), h.ListAgents)
		agents.GET("/dashboard", middleware.AuthCheck("agent:read"), h.Dashboard)

		agents.GET("/:id", middleware.AuthCheck("agent:read"), h.GetAgent)
		agents.PUT("/:id", middleware.AuthCheck("agent:write"), h.UpdateAgent)
		agents.DELETE("/:id", middleware.AuthCheck("agent:write"), h.DeleteAgent)

		agents.POST("/:id/start", middleware.AuthCheck("agent:write"), h.StartAgent)
		agents.POST("/:id/stop", middleware.AuthCheck("agent:write"), h.StopAgent)
		agents.GET("/:id/metrics", middleware.AuthCheck("agent:read"), h.Metrics)
		agents.GET("/:id/logs", middleware.AuthCheck("agent:read"), h.Logs)

		agents.GET("/:id/mcps", middleware.AuthCheck("agent:read"), h.ListBoundMCPS)
		agents.GET("/:id/skills", middleware.AuthCheck("agent:read"), h.ListBoundSkills)
		agents.PUT("/:id/skills", middleware.AuthCheck("agent:write"), h.UpdateAgentSkills)
		agents.GET("/:id/versions", middleware.AuthCheck("agent:read"), h.ListVersions)
		agents.POST("/:id/rollback", middleware.AuthCheck("agent:write"), h.Rollback)

		agents.POST("/:id/keys", middleware.AuthCheck("agent:write"), h.CreateAPIKey)
		agents.GET("/:id/keys", middleware.AuthCheck("agent:read"), h.ListAPIKeys)
		agents.DELETE("/:id/keys/:keyId", middleware.AuthCheck("agent:write"), h.RevokeAPIKey)
		agents.POST("/:id/keys/:keyId/delete", middleware.AuthCheck("agent:write"), h.DeleteAPIKey)

		// 对话 (M2.5, PRD 2.1.4): 用户提示词 -> 模型调用 + 工具调用 -> 应答
		agents.POST("/:id/chat", middleware.AuthCheck("agent:write"), h.Chat)
		agents.GET("/:id/sessions", middleware.AuthCheck("agent:read"), h.ListSessions)
		agents.GET("/:id/sessions/:sid", middleware.AuthCheck("agent:read"), h.GetSession)
		agents.DELETE("/:id/sessions/:sid", middleware.AuthCheck("agent:write"), h.DeleteSession)
	}

	// 外部调用入口 (M2 待办: API Key 调用链): 使用 Agent API Key 认证, 不走用户 JWT
	router.POST("/agents/:id/invoke", h.InvokeAgent)
	// /invoke 审核结果查询: 外部系统凭 API Key 轮询 202 待审核请求的终态与工具执行结果
	router.GET("/agents/:id/invoke/approvals/:approvalId", h.GetInvokeApproval)
}

// CreateAgent 创建 Agent
func (h *Handler) CreateAgent(c *gin.Context) {
	var req service.CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	agent, err := h.svc.CreateAgent(c.Request.Context(), req, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, agent)
}

// ListAgents 分页列表 (q 搜索, status 过滤)
func (h *Handler) ListAgents(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	filter := repository.AgentListFilter{
		Keyword:  c.Query("q"),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: size,
	}
	agents, total, err := h.svc.ListAgents(c.Request.Context(), filter)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"items":     agents,
		"total":     total,
		"page":      page,
		"page_size": size,
	})
}

// GetAgent 详情 (含实例)
func (h *Handler) GetAgent(c *gin.Context) {
	agent, instance, err := h.svc.GetAgent(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"agent":    agent,
		"instance": instance,
	})
}

// UpdateAgent 更新配置
func (h *Handler) UpdateAgent(c *gin.Context) {
	var req service.UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	agent, err := h.svc.UpdateAgent(c.Request.Context(), c.Param("id"), req, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, agent)
}

// DeleteAgent 删除
func (h *Handler) DeleteAgent(c *gin.Context) {
	if err := h.svc.DeleteAgent(c.Request.Context(), c.Param("id")); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// StartAgent 启动实例
func (h *Handler) StartAgent(c *gin.Context) {
	instance, err := h.svc.StartAgent(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, instance)
}

// StopAgent 停止实例
func (h *Handler) StopAgent(c *gin.Context) {
	instance, err := h.svc.StopAgent(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, instance)
}

// Metrics 调用统计
func (h *Handler) Metrics(c *gin.Context) {
	var from, to time.Time
	if raw := c.Query("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, "invalid from, use RFC3339 format")
			return
		}
		from = parsed
	}
	if raw := c.Query("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, "invalid to, use RFC3339 format")
			return
		}
		to = parsed
	}

	metrics, err := h.svc.GetMetrics(c.Request.Context(), c.Param("id"), from, to)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, metrics)
}

// Logs 运行日志 (level/keyword/since 过滤)
func (h *Handler) Logs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "100"))

	filter := repository.AgentLogFilter{
		AgentID:  c.Param("id"),
		Level:    c.Query("level"),
		Keyword:  c.Query("keyword"),
		Page:     page,
		PageSize: size,
	}
	if raw := c.Query("since"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			filter.Since = parsed
		} else if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
			filter.Since = time.Unix(ts, 0)
		} else {
			response.BadRequest(c, "invalid since, use RFC3339 or unix timestamp")
			return
		}
	}

	logs, total, err := h.svc.GetLogs(c.Request.Context(), filter)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"items":     logs,
		"total":     total,
		"page":      page,
		"page_size": size,
	})
}

// ListVersions 版本历史
func (h *Handler) ListVersions(c *gin.Context) {
	versions, err := h.svc.ListVersions(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": versions})
}

// RollbackRequest 回滚请求
type RollbackRequest struct {
	Version int `json:"version" binding:"required"`
}

// Rollback 回滚到指定版本
func (h *Handler) Rollback(c *gin.Context) {
	var req RollbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: version is required")
		return
	}
	agent, err := h.svc.RollbackAgent(c.Request.Context(), c.Param("id"), req.Version, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, agent)
}

// CreateKeyRequest 创建 API Key 请求 (expires_at 可选, RFC3339 未来时间)
type CreateKeyRequest struct {
	Name      string `json:"name" binding:"max=64"`
	ExpiresAt string `json:"expires_at" binding:"omitempty,max=40"`
}

// CreateAPIKey 创建 API Key (明文仅返回一次)
func (h *Handler) CreateAPIKey(c *gin.Context) {
	var req CreateKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}
	var expiresAt *time.Time
	if s := strings.TrimSpace(req.ExpiresAt); s != "" {
		t, parseErr := time.Parse(time.RFC3339, s)
		if parseErr != nil {
			response.BadRequest(c, "expires_at 需为 RFC3339 格式, 如 2026-09-01T00:00:00+08:00")
			return
		}
		expiresAt = &t
	}
	key, plain, err := h.svc.CreateAPIKey(c.Request.Context(), c.Param("id"), req.Name, c.GetString("user_id"), expiresAt)
	if err != nil {
		response.Error(c, err)
		return
	}
	c.JSON(http.StatusCreated, response.Response{
		Code:    "success",
		Message: "created",
		Data: gin.H{
			"key":     plain, // 仅本次返回, 之后只存摘要
			"api_key": key,
		},
	})
}

// ListAPIKeys API Key 列表
func (h *Handler) ListAPIKeys(c *gin.Context) {
	keys, err := h.svc.ListAPIKeys(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": keys})
}

// RevokeAPIKey 吊销 API Key
func (h *Handler) RevokeAPIKey(c *gin.Context) {
	if err := h.svc.RevokeAPIKey(c.Request.Context(), c.Param("id"), c.Param("keyId")); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"revoked": true})
}

// DeleteAPIKey 删除 API Key (仅允许已吊销的 Key)
func (h *Handler) DeleteAPIKey(c *gin.Context) {
	if err := h.svc.DeleteAPIKey(c.Request.Context(), c.Param("id"), c.Param("keyId")); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// Dashboard 状态看板
func (h *Handler) Dashboard(c *gin.Context) {
	data, err := h.svc.Dashboard(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, data)
}

// ListBoundMCPS Agent 绑定的 MCP 服务器 (含已发现工具)
func (h *Handler) ListBoundMCPS(c *gin.Context) {
	items, err := h.svc.ListBoundMCPS(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": items})
}

// ListBoundSkills Agent 绑定的技能列表 (M9)
func (h *Handler) ListBoundSkills(c *gin.Context) {
	skills, err := h.svc.ListBoundSkills(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"skills": skills})
}

// UpdateAgentSkills 全量更新 Agent 技能关联 (M9) {"skills":["skill-id"]}
func (h *Handler) UpdateAgentSkills(c *gin.Context) {
	var req struct {
		Skills []string `json:"skills"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: " + err.Error())
		return
	}
	if err := h.svc.UpdateAgentSkills(c.Request.Context(), c.Param("id"), req.Skills, c.GetString("user_id")); err != nil {
		response.Error(c, err)
		return
	}
	skills, err := h.svc.ListBoundSkills(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"skills": skills})
}

// InvokeAgent 外部调用 (API Key 认证, 不走用户 JWT)
func (h *Handler) InvokeAgent(c *gin.Context) {
	plain := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(plain, "Bearer ") {
		plain = strings.TrimSpace(plain[len("Bearer "):])
	}
	var req service.InvokeAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	result, err := h.svc.InvokeAgent(c.Request.Context(), c.Param("id"), plain, req)
	if err != nil {
		response.Error(c, err)
		return
	}
	// M4.5: 存在待审核工具调用时返回 202, 执行结果在审核详情中查看
	if len(result.PendingApprovals) > 0 {
		response.Accepted(c, result)
		return
	}
	response.Success(c, result)
}

// GetInvokeApproval 查询 /invoke 待审核请求的结果 (API Key 认证, 不走用户 JWT)
// /invoke 返回 202 (pending_approvals) 后, 外部系统凭 API Key 轮询本端点:
// status 进入 approved/rejected/expired 即终态; approved 时 result 为工具执行结果
func (h *Handler) GetInvokeApproval(c *gin.Context) {
	plain := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(plain, "Bearer ") {
		plain = strings.TrimSpace(plain[len("Bearer "):])
	}
	view, err := h.svc.GetInvokeApproval(c.Request.Context(), c.Param("id"), plain, c.Param("approvalId"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, view)
}

// Chat 对话 (M2.5): 用户提示词 -> 系统提示词+历史上下文+模型调用(+工具调用轮) -> 应答
func (h *Handler) Chat(c *gin.Context) {
	var req service.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	result, err := h.chat.Chat(c.Request.Context(), c.Param("id"), req, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, result)
}

// ListSessions 对话会话列表
func (h *Handler) ListSessions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	items, total, err := h.chat.ListSessions(c.Request.Context(), c.Param("id"), page, size)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": page, "page_size": size})
}

// GetSession 会话详情 + 消息历史
func (h *Handler) GetSession(c *gin.Context) {
	session, msgs, err := h.chat.GetSession(c.Request.Context(), c.Param("id"), c.Param("sid"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"session": session, "messages": msgs})
}
func (h *Handler) DeleteSession(c *gin.Context) {
	if err := h.chat.DeleteSession(c.Request.Context(), c.Param("id"), c.Param("sid")); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
