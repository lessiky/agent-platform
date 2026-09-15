package kb

import (
	"strconv"
	"strings"

	"agent-platform/internal/middleware"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	apperrors "agent-platform/pkg/errors"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

// Handler 知识库管理 HTTP handler (M11: 分类/条目 CRUD + 归档 + 试算检索; M11.5: 分类树 + 回填任务)
type Handler struct {
	svc   service.KBService
	tasks service.KBTaskService
}

func NewHandler(svc service.KBService, tasks service.KBTaskService) *Handler {
	return &Handler{svc: svc, tasks: tasks}
}

// RegisterRoutes 注册知识库路由 (PRD 6.1)
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	kb := router.Group("/kb")
	kb.Use(middleware.Auth())
	{
		kb.GET("/categories", middleware.AuthCheck("kb:read"), h.ListCategories)
		kb.POST("/categories", middleware.AuthCheck("kb:write"), h.CreateCategory)
		kb.PUT("/categories/:id", middleware.AuthCheck("kb:write"), h.UpdateCategory)
		kb.DELETE("/categories/:id", middleware.AuthCheck("kb:write"), h.DeleteCategory)

		kb.GET("/documents", middleware.AuthCheck("kb:read"), h.ListDocuments)
		kb.POST("/documents", middleware.AuthCheck("kb:write"), h.CreateDocument)
		kb.GET("/documents/:id", middleware.AuthCheck("kb:read"), h.GetDocument)
		// M11.5 事项 4: 分块预览 (管理预览/调试; 详情响应另含 chunk_count)
		kb.GET("/documents/:id/chunks", middleware.AuthCheck("kb:read"), h.GetDocumentChunks)
		kb.PUT("/documents/:id", middleware.AuthCheck("kb:write"), h.UpdateDocument)
		kb.DELETE("/documents/:id", middleware.AuthCheck("kb:write"), h.DeleteDocument)
		kb.PATCH("/documents/:id/status", middleware.AuthCheck("kb:write"), h.SetDocumentStatus)

		kb.GET("/search", middleware.AuthCheck("kb:read"), h.TrialSearch)

		// 回填状态 (M11.5: 未向量化目标统计, 平台设置「存在未向量化块时提示回填」)
		kb.GET("/backfill-status", middleware.AuthCheck("kb:read"), h.BackfillStatus)

		// 向量回填 (M11.5: 后台任务; 启动 / 列表 / 详情 / 取消)
		kb.POST("/backfill-tasks", middleware.AuthCheck("kb:write"), h.StartBackfillTask)
		kb.GET("/backfill-tasks", middleware.AuthCheck("kb:read"), h.ListBackfillTasks)
		kb.GET("/backfill-tasks/:id", middleware.AuthCheck("kb:read"), h.GetBackfillTask)
		kb.POST("/backfill-tasks/:id/cancel", middleware.AuthCheck("kb:write"), h.CancelBackfillTask)
		// 旧同步回填端点 (M11): 已弃用 → 410 Gone (消息指明新端点)
		kb.POST("/documents/backfill-embeddings", middleware.AuthCheck("kb:write"), h.BackfillEmbeddingsGone)
	}
}

// ListCategories 分类列表 (含 active 条目数)
func (h *Handler) ListCategories(c *gin.Context) {
	cats, err := h.svc.ListCategories(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": cats})
}

// CreateCategory 新建分类 (M11.5: parent_id 可选, 须指向顶级分类)
func (h *Handler) CreateCategory(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
		ParentID    string `json:"parent_id"` // 可选; 空 = 顶级
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	cat, err := h.svc.CreateCategory(c.Request.Context(), req.ParentID, req.Name, req.Description, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, cat)
}

// UpdateCategory 重命名 / 改描述 / 移动父级 (M11.5: parent_id 缺省 = 不变, "" = 升顶级)
func (h *Handler) UpdateCategory(c *gin.Context) {
	var req struct {
		Name        string  `json:"name" binding:"required"`
		Description string  `json:"description"`
		ParentID    *string `json:"parent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	cat, err := h.svc.UpdateCategory(c.Request.Context(), c.Param("id"), req.ParentID, req.Name, req.Description, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, cat)
}

// DeleteCategory 删除 (有条目 409 阻断)
func (h *Handler) DeleteCategory(c *gin.Context) {
	if err := h.svc.DeleteCategory(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP()); err != nil {
		response.Error(c, err)
		return
	}
	c.JSON(200, response.Response{Code: "success", Message: "deleted"})
}

// ListDocuments 条目列表 (分类/关键词/来源/状态过滤, 分页)
func (h *Handler) ListDocuments(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	categoryIDs := splitIDList(c.Query("category_id"))
	items, total, err := h.svc.ListDocuments(c.Request.Context(), repository.KBDocumentListFilter{
		CategoryIDs: categoryIDs,
		Keyword:     c.Query("keyword"),
		Source:      c.Query("source"),
		Status:      c.Query("status"),
		Page:        page,
		PageSize:    size,
	})
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

// CreateDocument 新建条目 (chat_summary 来源校验会话归属与 Agent 绑定)
func (h *Handler) CreateDocument(c *gin.Context) {
	var req service.CreateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	doc, err := h.svc.CreateDocument(c.Request.Context(), req, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, doc)
}

// GetDocument 条目详情
func (h *Handler) GetDocument(c *gin.Context) {
	doc, err := h.svc.GetDocument(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, doc)
}

// UpdateDocument 更新标题/正文/分类
func (h *Handler) UpdateDocument(c *gin.Context) {
	var req service.UpdateDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	doc, err := h.svc.UpdateDocument(c.Request.Context(), c.Param("id"), req, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, doc)
}

// DeleteDocument 删除条目
func (h *Handler) DeleteDocument(c *gin.Context) {
	if err := h.svc.DeleteDocument(c.Request.Context(), c.Param("id"), c.GetString("user_id"), c.GetString("username"), c.ClientIP()); err != nil {
		response.Error(c, err)
		return
	}
	c.JSON(200, response.Response{Code: "success", Message: "deleted"})
}

// SetDocumentStatus 归档 / 恢复
func (h *Handler) SetDocumentStatus(c *gin.Context) {
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	doc, err := h.svc.SetDocumentStatus(c.Request.Context(), c.Param("id"), req.Status, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, doc)
}

// TrialSearch 平台级试算检索 (query / category_ids / top_k)
func (h *Handler) TrialSearch(c *gin.Context) {
	categoryIDs := splitIDList(c.Query("category_ids"))
	topK, _ := strconv.Atoi(c.DefaultQuery("top_k", "5"))
	hits, err := h.svc.TrialSearch(c.Request.Context(), c.Query("query"), categoryIDs, topK)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"hits": hits})
}

// splitIDList 逗号分隔 ID 列表 (category_id / category_ids 查询参数, M11.5 多分类)
func splitIDList(raw string) []string {
	var ids []string
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// GetDocumentChunks 条目分块列表 (M11.5 事项 4: 序号/内容/向量化状态; 条目不存在 404)
func (h *Handler) GetDocumentChunks(c *gin.Context) {
	chunks, err := h.svc.GetDocumentChunks(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"doc_id": c.Param("id"), "chunk_count": len(chunks), "chunks": chunks})
}

// BackfillStatus 未向量化目标统计 (块模式 = 块数 unit=chunk; 整条模式 = 条目数 unit=document)
func (h *Handler) BackfillStatus(c *gin.Context) {
	st, err := h.svc.BackfillStatus(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, st)
}

// BackfillEmbeddingsGone 旧同步回填端点 (M11.5 → 410 Gone, 消息指明新端点)
func (h *Handler) BackfillEmbeddingsGone(c *gin.Context) {
	response.Gone(c, apperrors.ErrKBBackfillGone.Message)
}

// StartBackfillTask 启动向量回填任务 (单飞: 已有运行中任务 409 返回运行中任务)
func (h *Handler) StartBackfillTask(c *gin.Context) {
	task, err := h.tasks.Start(c.Request.Context(), c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		if appErr, ok := err.(*apperrors.AppError); ok && appErr.Code == "kb_task_active" {
			c.JSON(appErr.HTTPCode, response.Response{Code: appErr.Code, Message: appErr.Message, Data: gin.H{"task": task}})
			return
		}
		response.Error(c, err)
		return
	}
	response.Created(c, task)
}

// ListBackfillTasks 最近任务列表 (20 条)
func (h *Handler) ListBackfillTasks(c *gin.Context) {
	tasks, err := h.tasks.ListRecent(c.Request.Context())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"items": tasks})
}

// GetBackfillTask 任务详情 + 进度
func (h *Handler) GetBackfillTask(c *gin.Context) {
	task, err := h.tasks.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, task)
}

// CancelBackfillTask 取消任务 (批边界协作取消)
func (h *Handler) CancelBackfillTask(c *gin.Context) {
	task, err := h.tasks.Cancel(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, task)
}
