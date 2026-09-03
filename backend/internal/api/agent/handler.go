package agent

import (
	"encoding/json"
	"fmt"
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
	mem  service.MemoryService // 长期记忆 (M10.1)
}

func NewHandler(svc service.AgentService, chat service.ChatService, mem service.MemoryService) *Handler {
	return &Handler{svc: svc, chat: chat, mem: mem}
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
		// 对话 (SSE 流式版, 2026-08-24): body 与 /chat 相同, 响应为 text/event-stream,
		// 实时推送执行阶段事件 (turn_start/model_round/tool_start/tool_end/final/error)
		agents.POST("/:id/chat/stream", middleware.AuthCheck("agent:write"), h.ChatStream)
		agents.GET("/:id/sessions", middleware.AuthCheck("agent:read"), h.ListSessions)
		agents.GET("/:id/sessions/:sid", middleware.AuthCheck("agent:read"), h.GetSession)
		agents.PUT("/:id/sessions/:sid", middleware.AuthCheck("agent:write"), h.RenameSession)
		agents.DELETE("/:id/sessions/:sid", middleware.AuthCheck("agent:write"), h.DeleteSession)

		// 长期记忆 (M10.1): 显式 CRUD + 属主隔离 (user 级记忆仅属主/admin);
		// 检索注入发生在对话链内 (runTurn / ContinueAfterApproval), 无独立端点
		agents.GET("/:id/memories", middleware.AuthCheck("agent:read"), h.ListMemories)
		agents.POST("/:id/memories", middleware.AuthCheck("agent:write"), h.CreateMemory)
		agents.GET("/:id/memories/:mid", middleware.AuthCheck("agent:read"), h.GetMemory)
		agents.PATCH("/:id/memories/:mid", middleware.AuthCheck("agent:write"), h.UpdateMemory)
		agents.DELETE("/:id/memories/:mid", middleware.AuthCheck("agent:write"), h.DeleteMemory)
	}

	// 外部调用入口 (M2 待办: API Key 调用链): 使用 Agent API Key 认证, 不走用户 JWT
	// 2026-08-24 起为异步执行任务: 立即返回 202 + execution_id, 状态经 executions 端点查询
	router.POST("/agents/:id/invoke", h.InvokeAgent)
	// /invoke 执行任务状态查询: 外部系统凭 API Key 轮询异步执行的 状态/阶段/结果 (区分执行中与卡死)
	router.GET("/agents/:id/invoke/executions/:executionId", h.GetInvokeExecution)
	// /invoke 执行任务取消: 外部方主动放弃进行中的任务 (API Key 认证);
	// 平台取消执行上下文 (透传进行中的模型/MCP 调用) 并标记终态 cancelled, 已终态任务幂等返回
	router.POST("/agents/:id/invoke/executions/:executionId/cancel", h.CancelInvokeExecution)
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
		response.BadRequest(c, "invalid request body: "+err.Error())
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
	// 异步执行任务: 202 + execution_id, 状态/结果经 GET /agents/:id/invoke/executions/:executionId 查询
	if result.ExecutionID != "" {
		response.AcceptedTask(c, result)
		return
	}
	// M4.5: 降级同步路径存在待审核工具调用时返回 202, 执行结果在审核详情中查看
	if len(result.PendingApprovals) > 0 {
		response.Accepted(c, result)
		return
	}
	response.Success(c, result)
}

// GetInvokeExecution 查询 /invoke 执行任务状态 (API Key 认证, 不走用户 JWT)
// /invoke 返回 202 (execution_id) 后, 外部系统凭 API Key 轮询本端点:
// status=running 时 stage 为当前阶段, last_activity_at 为最近进度心跳;
// status 进入 success/failed/stalled/cancelled 即终态, success 时 result 为模型应答与调用明细
func (h *Handler) GetInvokeExecution(c *gin.Context) {
	plain := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(plain, "Bearer ") {
		plain = strings.TrimSpace(plain[len("Bearer "):])
	}
	view, err := h.svc.GetInvokeExecution(c.Request.Context(), c.Param("id"), plain, c.Param("executionId"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, view)
}

// CancelInvokeExecution 取消 /invoke 执行任务 (API Key 认证, 不走用户 JWT)
// 外部方主动放弃进行中的任务: 平台取消执行上下文 (透传进行中的模型/MCP 调用) 并标记终态 cancelled;
// cancelled=true 表示本次调用触发了取消; 已终态任务幂等返回 (cancelled=false + 当前终态)
func (h *Handler) CancelInvokeExecution(c *gin.Context) {
	plain := strings.TrimSpace(c.GetHeader("Authorization"))
	if strings.HasPrefix(plain, "Bearer ") {
		plain = strings.TrimSpace(plain[len("Bearer "):])
	}
	exec, cancelled, err := h.svc.CancelInvokeExecution(c.Request.Context(), c.Param("id"), plain, c.Param("executionId"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"execution_id": exec.ID,
		"cancelled":    cancelled,
		"status":       exec.Status,
	})
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

// ChatStream 对话 (SSE 流式版): 请求体与 /chat 相同 (show_thinking 开启时额外推送 thinking_delta);
// 响应 text/event-stream:
// event: turn_start | model_round | thinking_delta | tool_start | tool_end | final | error
// 前端用 fetch + ReadableStream 读取 (EventSource 不支持 POST);
// 客户端断开时请求 ctx 取消, 后台执行链随之中止
func (h *Handler) ChatStream(c *gin.Context) {
	var req service.ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	events, err := h.chat.ChatStream(c.Request.Context(), c.Param("id"), req, c.GetString("user_id"))
	if err != nil {
		response.Error(c, err)
		return
	}

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // nginx 等代理禁用缓冲, 保证事件实时下发
	c.Status(http.StatusOK)
	c.Writer.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return
			}
			data, mErr := json.Marshal(evt.Data)
			if mErr != nil {
				continue
			}
			if _, wErr := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", evt.Type, data); wErr != nil {
				return
			}
			c.Writer.Flush()
		case <-keepAlive.C:
			if _, wErr := fmt.Fprint(c.Writer, ": keepalive\n\n"); wErr != nil {
				return
			}
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
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

// RenameSession 修改会话名
func (h *Handler) RenameSession(c *gin.Context) {
	var req struct {
		Title string `json:"title" binding:"required,max=128"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: title is required (max 128 chars)")
		return
	}
	session, err := h.chat.RenameSession(c.Request.Context(), c.Param("id"), c.Param("sid"), req.Title)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, session)
}

func (h *Handler) DeleteSession(c *gin.Context) {
	if err := h.chat.DeleteSession(c.Request.Context(), c.Param("id"), c.Param("sid")); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ---------- 长期记忆 (M10.1) ----------

// hasAdminRole 当前请求是否携带 admin 角色 (JWT claims.roles)
func hasAdminRole(c *gin.Context) bool {
	v, ok := c.Get("roles")
	if !ok {
		return false
	}
	roles, ok := v.([]string)
	if !ok {
		return false
	}
	for _, r := range roles {
		if r == "admin" {
			return true
		}
	}
	return false
}

// ListMemories 记忆分页列表 (scope=mine 默认 / agent / all; kind/status 过滤)
func (h *Handler) ListMemories(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	f := repository.MemoryListFilter{
		AgentID:  c.Param("id"),
		UserID:   c.GetString("user_id"),
		Kind:     c.Query("kind"),
		Status:   c.Query("status"),
		Scope:    c.DefaultQuery("scope", "mine"),
		Page:     page,
		PageSize: size,
	}
	items, total, err := h.mem.ListMemories(c.Request.Context(), c.GetString("user_id"), hasAdminRole(c), f)
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

// CreateMemory 显式添加记忆 (scope=user 默认绑定当前用户 / agent 全局)
func (h *Handler) CreateMemory(c *gin.Context) {
	var req service.CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	mem, err := h.mem.CreateMemory(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP(), req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, mem)
}

// GetMemory 记忆详情
func (h *Handler) GetMemory(c *gin.Context) {
	mem, err := h.mem.GetMemory(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.Param("mid"), hasAdminRole(c))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, mem)
}

// UpdateMemory 更新记忆 (user 级记忆仅属主/admin)
func (h *Handler) UpdateMemory(c *gin.Context) {
	var req service.UpdateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	mem, err := h.mem.UpdateMemory(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP(), c.Param("mid"), hasAdminRole(c), req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, mem)
}

// DeleteMemory 删除记忆 (user 级记忆仅属主/admin)
func (h *Handler) DeleteMemory(c *gin.Context) {
	if err := h.mem.DeleteMemory(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP(), c.Param("mid"), hasAdminRole(c)); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
