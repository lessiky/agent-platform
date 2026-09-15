package service

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"agent-platform/internal/config"
	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
)

// KBService 知识库管理服务 (M11 W1: 分类/条目 CRUD + 归档 + 搜索 + 平台级试算)
type KBService interface {
	// ---- 分类管理 (KB-1; M11.5: 两级分类树) ----
	ListCategories(ctx context.Context) ([]repository.KBCategoryView, error)
	// CreateCategory 新建分类; parentID 可选 (nil/"" = 顶级), 须指向顶级分类, 同级重名 400
	CreateCategory(ctx context.Context, parentID, name, description, operatorID, operatorName, ip string) (*model.KBCategory, error)
	// UpdateCategory 重命名 / 改描述 / 移动父级; parentID nil = 不变, "" = 升顶级;
	// 已有子级的分类不可降为子级 (400); 移动重新校验同级唯一
	UpdateCategory(ctx context.Context, id string, parentID *string, name, description, operatorID, operatorName, ip string) (*model.KBCategory, error)
	DeleteCategory(ctx context.Context, id, operatorID, operatorName, ip string) error

	// ---- 条目管理 (KB-1/KB-3) ----
	ListDocuments(ctx context.Context, filter repository.KBDocumentListFilter) ([]repository.KBDocumentView, int64, error)
	GetDocument(ctx context.Context, id string) (*repository.KBDocumentView, error)
	CreateDocument(ctx context.Context, req CreateDocumentRequest, operatorID, operatorName, ip string) (*model.KBDocument, error)
	UpdateDocument(ctx context.Context, id string, req UpdateDocumentRequest, operatorID, operatorName, ip string) (*model.KBDocument, error)
	DeleteDocument(ctx context.Context, id, operatorID, operatorName, ip string) error
	SetDocumentStatus(ctx context.Context, id, status, operatorID, operatorName, ip string) (*model.KBDocument, error)

	// TrialSearch 平台级试算检索 (W2 起为两阶段流水线: 向量召回 + rerank, 未配置时降级关键词)
	TrialSearch(ctx context.Context, query string, categoryIDs []string, topK int) ([]KBSearchHit, error)
	// BackfillEmbeddings 向量回填: 为未向量化的 active 条目批量计算并回写向量 (需已配置 embedding 模型)
	BackfillEmbeddings(ctx context.Context) (*BackfillResult, error)
	// GetDocumentChunks 条目分块列表 (M11.5: 管理预览/调试; 条目不存在 404, 未启用分块返回空)
	GetDocumentChunks(ctx context.Context, id string) ([]KBChunkView, error)
	// BackfillStatus 未向量化目标统计 (平台设置回填提示; 块模式 = 块数, 整条模式 = 条目数)
	BackfillStatus(ctx context.Context) (*BackfillStatus, error)
}

// CreateDocumentRequest 条目创建请求 (PRD 6.1)
type CreateDocumentRequest struct {
	CategoryID      string `json:"category_id" binding:"required"`
	Title           string `json:"title" binding:"required"`
	Content         string `json:"content" binding:"required"`
	Source          string `json:"source"` // 空 = manual; chat_summary 须携带来源会话/Agent
	SourceSessionID string `json:"source_session_id"`
	SourceAgentID   string `json:"source_agent_id"`
}

// UpdateDocumentRequest 条目更新请求 (nil 字段 = 不变)
type UpdateDocumentRequest struct {
	Title      *string `json:"title"`
	Content    *string `json:"content"`
	CategoryID *string `json:"category_id"`
}

// KBSearchHit 试算检索命中项
type KBSearchHit struct {
	ID           string    `json:"id"`
	CategoryID   string    `json:"category_id"`
	CategoryName string    `json:"category_name"`
	Title        string    `json:"title"`
	Excerpt      string    `json:"excerpt"`
	MatchedChunk *string   `json:"matched_chunk,omitempty"` // M11.5: 最佳命中块全文 (块级召回; 短条目/关键词路径为空)
	Score        float64   `json:"score"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// KBChunkView 分块预览 (M11.5 事项 4: 序号/内容/向量化状态)
type KBChunkView struct {
	Index      int       `json:"chunk_index"`
	Content    string    `json:"content"`
	Vectorized bool      `json:"vectorized"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// BackfillStatus 未向量化目标统计 (块模式 = 未向量化块数, 整条模式 = 未向量化条目数)
type BackfillStatus struct {
	Unembedded int64  `json:"unembedded"`
	Unit       string `json:"unit"` // "chunk" | "document"
}

type kbService struct {
	cats     repository.KBCategoryRepository
	docs     repository.KBDocumentRepository
	bindings repository.AgentKBBindingRepository
	sessions repository.ChatSessionRepository
	audits   repository.AuditLogRepository
	cfg      config.KBConfig
	// retriever 两阶段检索器 (W2); nil 时试算退回 W1 关键词路径 (单测/未装配场景)
	retriever    *KBRetriever
	vectorWriter *KBVectorWriter
	chunks       repository.KBChunkRepository // M11.5: nil = 整条向量路径 (KB_CHUNK_ENABLED=false)
}

// NewKBService 创建知识库服务; retriever / vectorWriter / chunks 可为 nil (试算退回关键词路径, 不向量化, 不分块)
func NewKBService(
	cats repository.KBCategoryRepository,
	docs repository.KBDocumentRepository,
	bindings repository.AgentKBBindingRepository,
	sessions repository.ChatSessionRepository,
	audits repository.AuditLogRepository,
	cfg config.KBConfig,
	retriever *KBRetriever,
	vectorWriter *KBVectorWriter,
	chunks repository.KBChunkRepository,
) KBService {
	return &kbService{
		cats: cats, docs: docs, bindings: bindings, sessions: sessions, audits: audits, cfg: cfg,
		retriever: retriever, vectorWriter: vectorWriter, chunks: chunks,
	}
}

// chunkEnabled 分块路径总开关 (M11.5: KB_CHUNK_ENABLED, 启动时确定)
func (s *kbService) chunkEnabled() bool {
	return s.chunks != nil
}

// chunkOpts 分块参数 (对应环境变量 KB_CHUNK_SIZE / KB_CHUNK_OVERLAP / KB_CHUNK_THRESHOLD)
func (s *kbService) chunkOpts() KBChunkOptions {
	return KBChunkOptions{Size: s.cfg.ChunkSize, Overlap: s.cfg.ChunkOverlap, Threshold: s.cfg.ChunkThreshold}
}

// invalidate 候选集缓存写失效 + 异步向量化钩子 (条目内容变更后旧向量失效, 重新计算)
func (s *kbService) invalidate() {
	if s.retriever != nil {
		s.retriever.InvalidateCandidates()
	}
}

// ---------- 分类管理 ----------

// ListCategories 全部分类 (名称升序, 含 active 条目数)
func (s *kbService) ListCategories(ctx context.Context) ([]repository.KBCategoryView, error) {
	return s.cats.List(ctx)
}

// CreateCategory 新建分类 (M11.5: 名称 2-32 字符, 同一父级下同级唯一, 大小写不敏感;
// parentID 可选且须指向顶级分类, 子级不能再有子级)
func (s *kbService) CreateCategory(ctx context.Context, parentID, name, description, operatorID, operatorName, ip string) (*model.KBCategory, error) {
	name = strings.TrimSpace(name)
	if err := validateKBCategoryName(name); err != nil {
		return nil, err
	}
	if err := validateKBCategoryDesc(description); err != nil {
		return nil, err
	}
	parent, err := s.resolveCategoryParent(ctx, parentID)
	if err != nil {
		return nil, err
	}
	var parentPtr *string
	if parent != nil {
		parentPtr = strPtr(parent.ID)
	}
	if existing, err := s.cats.GetByNameUnderParent(ctx, name, parentPtr); err != nil {
		return nil, errors.Wrap(err, "failed to check category name")
	} else if existing != nil {
		return nil, errors.NewValidationError("同级分类名已存在 (大小写不敏感)")
	}
	cat := &model.KBCategory{Name: name, Description: description, CreatedBy: strPtr(operatorID)}
	if parent != nil {
		cat.ParentID = strPtr(parent.ID)
	}
	if err := s.cats.Create(ctx, cat); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return nil, errors.NewValidationError("同级分类名已存在 (大小写不敏感)")
		}
		return nil, errors.Wrap(err, "failed to create category")
	}
	detail := map[string]interface{}{"name": cat.Name, "parent_id": cat.ParentID}
	if parent != nil {
		detail["parent_name"] = parent.Name
	}
	s.audit(ctx, operatorID, operatorName, "kb.category_created", "kb_category", strPtr(cat.ID), ip, detail)
	return cat, nil
}

// resolveCategoryParent 解析父级分类: 空 = 顶级 (nil); 非空须存在且为顶级 (否则 400)
func (s *kbService) resolveCategoryParent(ctx context.Context, parentID string) (*model.KBCategory, error) {
	if strings.TrimSpace(parentID) == "" {
		return nil, nil
	}
	parent, err := s.cats.Get(ctx, strings.TrimSpace(parentID))
	if err != nil {
		if err == errors.ErrNotFound {
			return nil, errors.NewValidationError("父级分类不存在")
		}
		return nil, err
	}
	if parent.ParentID != nil {
		return nil, errors.NewValidationError("父级须为顶级分类 (仅支持两级层级)")
	}
	return parent, nil
}

// strPtrOrNil 空字符串返回 nil (父级 = 顶级的语义)
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// UpdateCategory 重命名 / 改描述 / 移动父级 (M11.5: parentID nil = 不变, "" = 升顶级;
// 已有子级的分类不可降为子级, 移动重新校验同级唯一, 审计含前后值)
func (s *kbService) UpdateCategory(ctx context.Context, id string, parentID *string, name, description, operatorID, operatorName, ip string) (*model.KBCategory, error) {
	cat, err := s.cats.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	oldName := cat.Name
	oldParentID := cat.ParentID
	name = strings.TrimSpace(name)
	if err := validateKBCategoryName(name); err != nil {
		return nil, err
	}
	if err := validateKBCategoryDesc(description); err != nil {
		return nil, err
	}
	if name != cat.Name {
		targetParent := cat.ParentID
		if parentID != nil {
			targetParent = strPtrOrNil(strings.TrimSpace(*parentID))
		}
		if existing, err := s.cats.GetByNameUnderParent(ctx, name, targetParent); err != nil {
			return nil, errors.Wrap(err, "failed to check category name")
		} else if existing != nil && existing.ID != cat.ID {
			return nil, errors.NewValidationError("同级分类名已存在 (大小写不敏感)")
		}
		cat.Name = name
	}
	// 移动父级 (仅当请求显式携带 parent_id)
	if parentID != nil {
		newParentID := strings.TrimSpace(*parentID)
		if newParentID != "" && (cat.ParentID == nil || *cat.ParentID != newParentID) {
			// 降为子级: 已有子级的分类不可降级 (会突破两级约束)
			if cnt, err := s.cats.CountChildren(ctx, cat.ID); err != nil {
				return nil, errors.Wrap(err, "failed to count category children")
			} else if cnt > 0 {
				return nil, errors.NewValidationError("该分类已有子级分类, 不可降为子级 (仅支持两级层级)")
			}
			parent, err := s.resolveCategoryParent(ctx, newParentID)
			if err != nil {
				return nil, err
			}
			if existing, err := s.cats.GetByNameUnderParent(ctx, cat.Name, strPtrOrNil(parent.ID)); err != nil {
				return nil, errors.Wrap(err, "failed to check category name")
			} else if existing != nil && existing.ID != cat.ID {
				return nil, errors.NewValidationError("同级分类名已存在 (大小写不敏感)")
			}
			cat.ParentID = strPtr(parent.ID)
		}
		if newParentID == "" && cat.ParentID != nil {
			// 升顶级: 重名校验 (顶级作用域排除自身)
			if existing, err := s.cats.GetByNameUnderParent(ctx, cat.Name, nil); err != nil {
				return nil, errors.Wrap(err, "failed to check category name")
			} else if existing != nil && existing.ID != cat.ID {
				return nil, errors.NewValidationError("顶级分类名已存在 (大小写不敏感)")
			}
			cat.ParentID = nil
		}
	}
	cat.Description = description
	if err := s.cats.Update(ctx, cat); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return nil, errors.NewValidationError("同级分类名已存在 (大小写不敏感)")
		}
		return nil, errors.Wrap(err, "failed to update category")
	}
	s.audit(ctx, operatorID, operatorName, "kb.category_updated", "kb_category", strPtr(cat.ID), ip, map[string]interface{}{
		"name":          cat.Name,
		"old_name":      oldName,
		"parent_id":     cat.ParentID,
		"old_parent_id": oldParentID,
	})
	return cat, nil
}

// DeleteCategory 删除分类: 有条目时 409 阻断 (不产生悬空绑定), 空分类硬删除 + 审计
func (s *kbService) DeleteCategory(ctx context.Context, id, operatorID, operatorName, ip string) error {
	cat, err := s.cats.Get(ctx, id)
	if err != nil {
		return err
	}
	if cnt, err := s.cats.CountChildren(ctx, cat.ID); err != nil {
		return errors.Wrap(err, "failed to count category children")
	} else if cnt > 0 {
		return errors.ErrKBCategoryHasChildren
	}
	cnt, err := s.cats.CountDocuments(ctx, cat.ID, "")
	if err != nil {
		return errors.Wrap(err, "failed to count category documents")
	}
	if cnt > 0 {
		return errors.ErrKBCategoryInUse
	}
	// 先清 Agent 绑定再删分类, 避免悬空引用
	if err := s.bindings.DeleteByCategory(ctx, cat.ID); err != nil {
		return errors.Wrap(err, "failed to unbind agents")
	}
	if err := s.cats.Delete(ctx, cat.ID); err != nil {
		return errors.Wrap(err, "failed to delete category")
	}
	s.invalidate()
	s.audit(ctx, operatorID, operatorName, "kb.category_deleted", "kb_category", strPtr(cat.ID), ip, map[string]interface{}{
		"name": cat.Name,
	})
	return nil
}

func validateKBCategoryName(name string) error {
	if e := len([]rune(name)); e < model.KBCategoryNameMinLen || e > model.KBCategoryNameMaxLen {
		return errors.NewValidationError(fmt.Sprintf("分类名称须为 %d-%d 个字符", model.KBCategoryNameMinLen, model.KBCategoryNameMaxLen))
	}
	return nil
}

func validateKBCategoryDesc(desc string) error {
	if len([]rune(desc)) > model.KBCategoryDescMaxLen {
		return errors.NewValidationError(fmt.Sprintf("分类描述须不超过 %d 个字符", model.KBCategoryDescMaxLen))
	}
	return nil
}

// ---------- 条目管理 ----------

// ListDocuments 条目分页列表 (分类/关键词/来源/状态过滤)
func (s *kbService) ListDocuments(ctx context.Context, filter repository.KBDocumentListFilter) ([]repository.KBDocumentView, int64, error) {
	return s.docs.List(ctx, filter)
}

// GetDocument 条目详情 (含分类名 + 分块数)
func (s *kbService) GetDocument(ctx context.Context, id string) (*repository.KBDocumentView, error) {
	doc, err := s.docs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	cat, err := s.cats.Get(ctx, doc.CategoryID)
	if err != nil {
		return nil, err
	}
	view := &repository.KBDocumentView{KBDocument: *doc, CategoryName: cat.Name}
	if s.chunkEnabled() {
		if cnt, err := s.chunks.CountByDoc(ctx, id); err == nil {
			view.ChunkCount = cnt
		}
	}
	return view, nil
}

// GetDocumentChunks 条目分块列表 (M11.5: 管理预览/调试; 条目不存在 404, 未启用分块返回空)
func (s *kbService) GetDocumentChunks(ctx context.Context, id string) ([]KBChunkView, error) {
	if _, err := s.docs.Get(ctx, id); err != nil {
		return nil, err
	}
	if !s.chunkEnabled() {
		return []KBChunkView{}, nil
	}
	chunks, err := s.chunks.ListByDoc(ctx, id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list kb chunks")
	}
	out := make([]KBChunkView, 0, len(chunks))
	for i := range chunks {
		out = append(out, KBChunkView{
			Index:      chunks[i].ChunkIndex,
			Content:    chunks[i].Content,
			Vectorized: !chunks[i].Embedding.IsNull(),
			UpdatedAt:  chunks[i].UpdatedAt,
		})
	}
	return out, nil
}

// BackfillStatus 未向量化目标统计 (平台设置「存在未向量化块时提示回填」)
func (s *kbService) BackfillStatus(ctx context.Context) (*BackfillStatus, error) {
	if s.chunkEnabled() {
		n, err := s.chunks.CountUnembedded(ctx)
		return &BackfillStatus{Unembedded: n, Unit: "chunk"}, err
	}
	n, err := s.docs.CountUnembedded(ctx)
	return &BackfillStatus{Unembedded: n, Unit: "document"}, err
}

// CreateDocument 新建条目; chat_summary 来源额外校验会话归属与 Agent 绑定 (越权 403)
func (s *kbService) CreateDocument(ctx context.Context, req CreateDocumentRequest, operatorID, operatorName, ip string) (*model.KBDocument, error) {
	cat, err := s.cats.Get(ctx, strings.TrimSpace(req.CategoryID))
	if err != nil {
		if err == errors.ErrNotFound {
			return nil, errors.NewValidationError("分类不存在")
		}
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if err := validateKBTitle(title); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errors.NewValidationError("正文不能为空")
	}
	if len(content) > s.cfg.MaxDocBytes {
		return nil, errors.NewValidationError(fmt.Sprintf("正文超出大小上限 %dKB", s.cfg.MaxDocBytes/1024))
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = model.KBSourceManual
	}
	if source != model.KBSourceManual && source != model.KBSourceChatSummary {
		return nil, errors.NewValidationError("source 须为 manual 或 chat_summary")
	}

	doc := &model.KBDocument{
		CategoryID: cat.ID,
		Title:      title,
		Content:    content,
		Source:     source,
		Status:     model.KBStatusActive,
		CreatedBy:  strPtr(operatorID),
	}
	if source == model.KBSourceChatSummary {
		if err := s.validateChatSummarySource(ctx, cat.ID, req.SourceSessionID, req.SourceAgentID); err != nil {
			return nil, err
		}
		doc.SourceSessionID = strPtr(strings.TrimSpace(req.SourceSessionID))
		doc.SourceAgentID = strPtr(strings.TrimSpace(req.SourceAgentID))
	}
	// M11.5: 块模式同事务重建块 (删旧 + 插新, 向量 NULL); 整条模式不分块
	var chunkContents []string
	if s.chunkEnabled() {
		chunkContents = ChunkMarkdown(content, s.chunkOpts())
	}
	if err := s.docs.CreateWithChunks(ctx, doc, chunkContents); err != nil {
		return nil, errors.Wrap(err, "failed to create document")
	}
	s.invalidate()
	s.vectorWriter.VectorizeAsync(doc)
	action := "kb.document_created"
	if source == model.KBSourceChatSummary {
		action = "kb.document_saved_from_chat"
	}
	s.audit(ctx, operatorID, operatorName, action, "kb_document", strPtr(doc.ID), ip, map[string]interface{}{
		"title":       doc.Title,
		"category_id": cat.ID,
		"session_id":  doc.SourceSessionID,
		"agent_id":    doc.SourceAgentID,
	})
	return doc, nil
}

// UpdateDocument 更新标题/正文/分类 (nil 字段不变)
func (s *kbService) UpdateDocument(ctx context.Context, id string, req UpdateDocumentRequest, operatorID, operatorName, ip string) (*model.KBDocument, error) {
	doc, err := s.docs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	titleChanged := false
	contentChanged := false
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if err := validateKBTitle(title); err != nil {
			return nil, err
		}
		if title != doc.Title {
			titleChanged = true
		}
		doc.Title = title
	}
	if req.Content != nil {
		content := strings.TrimSpace(*req.Content)
		if content == "" {
			return nil, errors.NewValidationError("正文不能为空")
		}
		if len(content) > s.cfg.MaxDocBytes {
			return nil, errors.NewValidationError(fmt.Sprintf("正文超出大小上限 %dKB", s.cfg.MaxDocBytes/1024))
		}
		if content != doc.Content {
			contentChanged = true
		}
		doc.Content = content
	}
	if req.CategoryID != nil {
		cat, err := s.cats.Get(ctx, strings.TrimSpace(*req.CategoryID))
		if err != nil {
			if err == errors.ErrNotFound {
				return nil, errors.NewValidationError("分类不存在")
			}
			return nil, err
		}
		doc.CategoryID = cat.ID
	}
	// M11.5: 块模式 — 正文变更同事务重建块 (向量 NULL); 仅改标题不重建块, 清空块向量触发重新向量化
	// (块向量化文本含标题); 整条模式 — 旧向量置空触发重新向量化 (未配置模型时留待回填)
	if s.chunkEnabled() {
		if contentChanged {
			if err := s.docs.UpdateWithChunks(ctx, doc, ChunkMarkdown(doc.Content, s.chunkOpts())); err != nil {
				return nil, errors.Wrap(err, "failed to update document")
			}
		} else {
			if titleChanged {
				if err := s.chunks.ClearEmbeddings(ctx, doc.ID); err != nil {
					log.Printf("kb: clear chunk embeddings failed doc=%s: %v (回填任务可补)", doc.ID, err)
				}
			}
			if err := s.docs.Update(ctx, doc); err != nil {
				return nil, errors.Wrap(err, "failed to update document")
			}
		}
	} else {
		if titleChanged || contentChanged {
			doc.Embedding = nil
		}
		if err := s.docs.Update(ctx, doc); err != nil {
			return nil, errors.Wrap(err, "failed to update document")
		}
	}
	s.invalidate()
	if titleChanged || contentChanged {
		s.vectorWriter.VectorizeAsync(doc)
	}
	s.audit(ctx, operatorID, operatorName, "kb.document_updated", "kb_document", strPtr(doc.ID), ip, map[string]interface{}{
		"title": doc.Title,
	})
	return doc, nil
}

// DeleteDocument 删除条目
func (s *kbService) DeleteDocument(ctx context.Context, id, operatorID, operatorName, ip string) error {
	doc, err := s.docs.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.docs.DeleteWithChunks(ctx, id); err != nil {
		return errors.Wrap(err, "failed to delete document")
	}
	s.invalidate()
	s.audit(ctx, operatorID, operatorName, "kb.document_deleted", "kb_document", strPtr(id), ip, map[string]interface{}{
		"title":       doc.Title,
		"category_id": doc.CategoryID,
	})
	return nil
}

// SetDocumentStatus 归档 / 恢复 (archived 不参与检索, 界面保留可恢复)
func (s *kbService) SetDocumentStatus(ctx context.Context, id, status, operatorID, operatorName, ip string) (*model.KBDocument, error) {
	if status != model.KBStatusActive && status != model.KBStatusArchived {
		return nil, errors.NewValidationError("status 须为 active 或 archived")
	}
	doc, err := s.docs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if doc.Status == status {
		return doc, nil
	}
	if err := s.docs.UpdateStatus(ctx, id, status); err != nil {
		return nil, errors.Wrap(err, "failed to update document status")
	}
	s.invalidate()
	doc.Status = status
	action := "kb.document_restored"
	if status == model.KBStatusArchived {
		action = "kb.document_archived"
	}
	s.audit(ctx, operatorID, operatorName, action, "kb_document", strPtr(doc.ID), ip, map[string]interface{}{
		"title":  doc.Title,
		"status": status,
	})
	return doc, nil
}

// validateChatSummarySource chat_summary 来源硬约束:
// 会话必须存在且属于该 Agent; 目标分类必须 ∈ Agent 读写绑定作用域 (服务端重新鉴权, 不信任客户端)。
// M11.5: 只读分类 (含其子级) 禁止写入 → 403; 绑定顶级分类可写入其任一子级。
func (s *kbService) validateChatSummarySource(ctx context.Context, categoryID, sessionID, agentID string) error {
	sessionID = strings.TrimSpace(sessionID)
	agentID = strings.TrimSpace(agentID)
	if sessionID == "" || agentID == "" {
		return errors.NewValidationError("chat_summary 来源须携带 source_session_id 与 source_agent_id")
	}
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		if err == errors.ErrNotFound {
			return errors.NewValidationError("来源会话不存在")
		}
		return err
	}
	if session.AgentID != agentID {
		return errors.NewValidationError("会话不属于该 Agent")
	}
	bindings, err := s.bindings.ListByAgent(ctx, agentID)
	if err != nil {
		return errors.Wrap(err, "failed to load agent kb bindings")
	}
	if !kbAgentWritableScope(ctx, s.cats, bindings)[categoryID] {
		return errors.ErrForbidden
	}
	return nil
}

// kbAgentWritableScope Agent 可写作用域集合 (M11.5: 读写绑定 ∪ 其直接子级)。
// 子级扩展查询失败时返回仅绑定集合 (保守放行读写绑定本身, 不阻断主流程)。
func kbAgentWritableScope(ctx context.Context, cats repository.KBCategoryRepository, bindings []model.AgentKBBinding) map[string]bool {
	writable := make(map[string]bool, len(bindings))
	for i := range bindings {
		if !bindings[i].ReadOnly {
			writable[bindings[i].CategoryID] = true
		}
	}
	if len(writable) > 0 {
		rwIDs := make([]string, 0, len(writable))
		for id := range writable {
			rwIDs = append(rwIDs, id)
		}
		if children, err := cats.ListByParents(ctx, rwIDs); err == nil {
			for i := range children {
				writable[children[i].ID] = true
			}
		}
	}
	return writable
}

func validateKBTitle(title string) error {
	if e := len([]rune(title)); e < model.KBTitleMinLen || e > model.KBTitleMaxLen {
		return errors.NewValidationError(fmt.Sprintf("标题须为 %d-%d 个字符", model.KBTitleMinLen, model.KBTitleMaxLen))
	}
	return nil
}

// ---------- 试算检索 (W1 关键词路径) ----------

// kbKeywordScore 单条条目关键词得分 (复用 M10 打分公式: 关键词覆盖 + 时间衰减 + 使用频率;
// 无关键词命中直接 0 分, 不参与排序)
func kbKeywordScore(query string, doc *model.KBDocument, queryTokens map[string]bool, now time.Time) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	text := doc.Title + "\n" + doc.Content
	dTokens := tokenizeText(text)
	hit := 0
	for t := range queryTokens {
		if dTokens[t] {
			hit++
		}
	}
	kw := float64(hit) / float64(len(queryTokens))
	if kw = kw + substringBonus(query, text); kw > 1 {
		kw = 1
	}
	if kw == 0 {
		return 0
	}
	ageDays := now.Sub(doc.UpdatedAt).Hours() / 24
	recency := math.Exp(-ageDays / memDecayDays)
	usage := math.Log1p(float64(doc.AccessCount)) / math.Log1p(memUsageCap)
	return memWeightKeyword*kw + memWeightRecency*recency + memWeightUsage*usage
}

// kbExcerpt 正文摘录 (截断到固定 rune 数)
const kbExcerptRunes = 300

func kbExcerpt(content string) string {
	return kbExcerptWithLimit(content, kbExcerptRunes)
}

// kbExcerptWithLimit 正文摘录 (截断到 limit rune; 工具路径 1500, 注入/试算 300)
func kbExcerptWithLimit(content string, limit int) string {
	runes := []rune(content)
	if len(runes) <= limit {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// TrialSearch 平台级试算检索 (W2: 两阶段流水线; 向量召回 + rerank, 未配置/故障时按阶梯降级关键词)
func (s *kbService) TrialSearch(ctx context.Context, query string, categoryIDs []string, topK int) ([]KBSearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []KBSearchHit{}, nil
	}
	if topK <= 0 {
		topK = 5
	}
	if topK > model.KBSearchMaxTop {
		topK = model.KBSearchMaxTop
	}
	if s.retriever == nil {
		// 未装配检索器 (单测/降级场景): 关键词路径
		return s.trialSearchKeyword(ctx, query, categoryIDs, topK)
	}
	hits, err := s.retriever.RetrieveByCategories(ctx, categoryIDs, query, KBSearchOptions{TopK: topK, BumpAccess: false})
	if err != nil {
		return nil, errors.Wrap(err, "failed to trial search kb")
	}
	return hits, nil
}

// trialSearchKeyword W1 关键词试算路径 (retriever 未装配时兜底)
func (s *kbService) trialSearchKeyword(ctx context.Context, query string, categoryIDs []string, topK int) ([]KBSearchHit, error) {
	candidates, err := s.docs.SearchCandidates(ctx, categoryIDs, model.KBStatusActive, s.cfg.MaxCandidates)
	if err != nil {
		return nil, errors.Wrap(err, "failed to search kb candidates")
	}
	queryTokens := tokenizeText(query)
	now := time.Now()
	type scoredDoc struct {
		doc   model.KBDocument
		score float64
	}
	scored := make([]scoredDoc, 0, len(candidates))
	for i := range candidates {
		if score := kbKeywordScore(query, &candidates[i], queryTokens, now); score > 0 {
			scored = append(scored, scoredDoc{doc: candidates[i], score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].doc.UpdatedAt.After(scored[j].doc.UpdatedAt)
	})
	if len(scored) > topK {
		scored = scored[:topK]
	}
	nameByID := make(map[string]string)
	for _, c := range scored {
		if _, ok := nameByID[c.doc.CategoryID]; !ok {
			if cat, err := s.cats.Get(ctx, c.doc.CategoryID); err == nil {
				nameByID[c.doc.CategoryID] = cat.Name
			}
		}
	}
	hits := make([]KBSearchHit, 0, len(scored))
	for _, c := range scored {
		hits = append(hits, KBSearchHit{
			ID:           c.doc.ID,
			CategoryID:   c.doc.CategoryID,
			CategoryName: nameByID[c.doc.CategoryID],
			Title:        c.doc.Title,
			Excerpt:      kbExcerpt(c.doc.Content),
			Score:        c.score,
			UpdatedAt:    c.doc.UpdatedAt,
		})
	}
	return hits, nil
}

// BackfillEmbeddings 向量回填 (平台设置页手动触发)
func (s *kbService) BackfillEmbeddings(ctx context.Context) (*BackfillResult, error) {
	if s.vectorWriter == nil {
		return nil, errors.NewValidationError("向量回填未启用 (未装配向量组件)")
	}
	return s.vectorWriter.Backfill(ctx)
}

// audit 写审计日志 (失败仅告警, 不阻塞主流程)
func (s *kbService) audit(ctx context.Context, operatorID, operatorName, action, resource string, resourceID *string, ip string, detail map[string]interface{}) {
	if s.audits == nil {
		return
	}
	entry := &model.AuditLog{
		Username:   operatorName,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		IP:         ip,
		CreatedAt:  time.Now(),
	}
	if operatorID != "" {
		entry.UserID = &operatorID
	}
	if detail != nil {
		entry.Detail = datatypes.JSON(mustMarshal(detail))
	}
	if err := s.audits.Append(ctx, entry); err != nil {
		log.Printf("kb: audit append failed action=%s: %v", action, err)
	}
}
