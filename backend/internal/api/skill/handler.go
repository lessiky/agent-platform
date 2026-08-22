package skill

import (
	"strconv"
	"strings"

	"agent-platform/internal/middleware"
	"agent-platform/internal/repository"
	"agent-platform/internal/service"
	"agent-platform/pkg/errors"
	"agent-platform/pkg/response"

	"github.com/gin-gonic/gin"
)

// maxImportSize 上传大小上限 (解压前); 解压后由 service 二次校验
const maxImportSize = 20 << 20 // 20MB

// Handler 技能管理 HTTP handler (只解析请求, 业务在 service)
type Handler struct {
	svc service.SkillService
}

func NewHandler(svc service.SkillService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 注册技能路由 (PRD 6.5)
func (h *Handler) RegisterRoutes(router *gin.RouterGroup) {
	skills := router.Group("/skills")
	skills.Use(middleware.Auth())
	{
		skills.POST("/import", middleware.AuthCheck("skill:write"), h.Import)
		skills.GET("", middleware.AuthCheck("skill:read"), h.List)

		skills.GET("/:id", middleware.AuthCheck("skill:read"), h.Get)
		skills.PUT("/:id", middleware.AuthCheck("skill:write"), h.UpdateStatus)
		skills.DELETE("/:id", middleware.AuthCheck("skill:write"), h.Delete)

		skills.GET("/:id/files", middleware.AuthCheck("skill:read"), h.ListFiles)
		skills.GET("/:id/files/*path", middleware.AuthCheck("skill:read"), h.GetFile)
		skills.GET("/:id/agents", middleware.AuthCheck("skill:read"), h.ListAgents)
		skills.GET("/:id/usage", middleware.AuthCheck("skill:read"), h.Usage)
	}
}

// Import 导入技能包 (multipart/form-data: file + force)
func (h *Handler) Import(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "缺少上传文件 (字段名 file): "+err.Error())
		return
	}
	if file.Size > maxImportSize {
		response.BadRequest(c, "技能包文件过大 (上传上限 20MB)")
		return
	}
	src, err := file.Open()
	if err != nil {
		response.Error(c, err)
		return
	}
	defer src.Close()

	data := make([]byte, 0, file.Size)
	buf := make([]byte, 64*1024)
	for {
		n, rErr := src.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
			if int64(len(data)) > maxImportSize {
				response.BadRequest(c, "技能包文件过大 (上传上限 20MB)")
				return
			}
		}
		if rErr != nil {
			break
		}
	}
	force := c.PostForm("force") == "true"
	skill, err := h.svc.Import(c.Request.Context(), data, force, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, skill)
}

// List 分页列表 (q 搜索, status/tag 过滤)
func (h *Handler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	items, total, err := h.svc.List(c.Request.Context(), repository.SkillListFilter{
		Keyword:  c.Query("q"),
		Tag:      c.Query("tag"),
		Status:   c.Query("status"),
		Page:     page,
		PageSize: size,
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

// Get 详情 (元数据 + 指令正文 + 文件清单)
func (h *Handler) Get(c *gin.Context) {
	skill, files, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{
		"skill": skill,
		"files": files,
	})
}

// UpdateStatus 启用 / 禁用
func (h *Handler) UpdateStatus(c *gin.Context) {
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body: "+err.Error())
		return
	}
	skill, err := h.svc.UpdateStatus(c.Request.Context(), c.Param("id"), req.Status, c.GetString("user_id"), c.GetString("username"), c.ClientIP())
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, skill)
}

// Delete 删除 (?force=true 级联解绑; 否则有关联返回 409 + 关联列表)
func (h *Handler) Delete(c *gin.Context) {
	force := c.Query("force") == "true"
	if err := h.svc.Delete(c.Request.Context(), c.Param("id"), force, c.GetString("user_id"), c.GetString("username"), c.ClientIP()); err != nil {
		if appErr, ok := err.(*errors.AppError); ok && appErr.Code == "skill_in_use" {
			agents, _ := h.svc.ListAgents(c.Request.Context(), c.Param("id"))
			c.JSON(409, response.Response{Code: "skill_in_use", Message: appErr.Message, Data: gin.H{"agents": agents}})
			return
		}
		response.Error(c, err)
		return
	}
	c.JSON(200, response.Response{Code: "success", Message: "deleted"})
}

// ListFiles 文件清单 (元数据, 不含内容)
func (h *Handler) ListFiles(c *gin.Context) {
	_, files, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"files": files})
}

// GetFile 单个资源文件内容 (二进制原样返回; 文本类型前端自行渲染)
func (h *Handler) GetFile(c *gin.Context) {
	file, err := h.svc.GetFile(c.Request.Context(), c.Param("id"), strings.TrimPrefix(c.Param("path"), "/"))
	if err != nil {
		response.Error(c, err)
		return
	}
	c.Header("Content-Disposition", `inline; filename="`+file.Path+`"`)
	c.Data(200, "application/octet-stream", file.Content)
}

// ListAgents 关联 Agent 列表
func (h *Handler) ListAgents(c *gin.Context) {
	agents, err := h.svc.ListAgents(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, gin.H{"agents": agents})
}

// Usage 使用统计
func (h *Handler) Usage(c *gin.Context) {
	usage, err := h.svc.Usage(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, usage)
}