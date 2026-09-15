package repository

import (
	"context"
	"strings"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/gorm"
)

// KBCategoryView 分类列表项 (含 active 条目数, 避免 N+1)
type KBCategoryView struct {
	model.KBCategory
	DocumentCount int `json:"document_count"`
}

// KBCategoryRepository 知识库分类仓储
type KBCategoryRepository interface {
	Create(ctx context.Context, cat *model.KBCategory) error
	Get(ctx context.Context, id string) (*model.KBCategory, error)
	// GetByName 按名称查 (大小写不敏感, 不存在返回 nil, nil)
	GetByName(ctx context.Context, name string) (*model.KBCategory, error)
	// GetByNameUnderParent 同一父级下按名称查 (大小写不敏感; parentID nil = 顶级; 不存在返回 nil, nil)
	GetByNameUnderParent(ctx context.Context, name string, parentID *string) (*model.KBCategory, error)
	// CountChildren 父级分类下的子级分类数
	CountChildren(ctx context.Context, parentID string) (int64, error)
	// ListByParents 批量查父级下的全部子级 (M11.5 绑定作用域扩展热路径, 走 idx_kb_category_parent)
	ListByParents(ctx context.Context, parentIDs []string) ([]model.KBCategory, error)
	Update(ctx context.Context, cat *model.KBCategory) error
	Delete(ctx context.Context, id string) error
	// List 全部分类 (名称升序, 带 active 条目数)
	List(ctx context.Context) ([]KBCategoryView, error)
	// CountDocuments 分类下条目数 (状态可选: 空 = 全部)
	CountDocuments(ctx context.Context, categoryID, status string) (int64, error)
}

type kbCategoryRepository struct{}

func NewKBCategoryRepository() KBCategoryRepository {
	return &kbCategoryRepository{}
}

func (r *kbCategoryRepository) Create(ctx context.Context, cat *model.KBCategory) error {
	return database.DB.WithContext(ctx).Create(cat).Error
}

func (r *kbCategoryRepository) Get(ctx context.Context, id string) (*model.KBCategory, error) {
	var cat model.KBCategory
	if err := database.DB.WithContext(ctx).First(&cat, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound || strings.Contains(err.Error(), "invalid input syntax for type uuid") {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &cat, nil
}

func (r *kbCategoryRepository) GetByName(ctx context.Context, name string) (*model.KBCategory, error) {
	var cat model.KBCategory
	if err := database.DB.WithContext(ctx).Where("LOWER(name) = LOWER(?)", name).First(&cat).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &cat, nil
}

// GetByNameUnderParent 同一父级下按名称查 (M11.5: 名称唯一性放宽为同级唯一, 大小写不敏感)
func (r *kbCategoryRepository) GetByNameUnderParent(ctx context.Context, name string, parentID *string) (*model.KBCategory, error) {
	var cat model.KBCategory
	q := database.DB.WithContext(ctx).Where("LOWER(name) = LOWER(?)", name)
	if parentID == nil {
		q = q.Where("parent_id IS NULL")
	} else {
		q = q.Where("parent_id = ?", *parentID)
	}
	if err := q.First(&cat).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &cat, nil
}

// ListByParents 批量查父级下的全部子级 (M11.5 绑定作用域扩展热路径)
func (r *kbCategoryRepository) ListByParents(ctx context.Context, parentIDs []string) ([]model.KBCategory, error) {
	if len(parentIDs) == 0 {
		return nil, nil
	}
	var cats []model.KBCategory
	if err := database.DB.WithContext(ctx).Where("parent_id IN ?", parentIDs).Find(&cats).Error; err != nil {
		return nil, err
	}
	return cats, nil
}

// CountChildren 父级分类下的子级分类数 (删除保护: 有子级 → 409)
func (r *kbCategoryRepository) CountChildren(ctx context.Context, parentID string) (int64, error) {
	var cnt int64
	if err := database.DB.WithContext(ctx).Model(&model.KBCategory{}).Where("parent_id = ?", parentID).Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}

func (r *kbCategoryRepository) Update(ctx context.Context, cat *model.KBCategory) error {
	return database.DB.WithContext(ctx).Save(cat).Error
}

func (r *kbCategoryRepository) Delete(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Delete(&model.KBCategory{}, "id = ?", id).Error
}

func (r *kbCategoryRepository) List(ctx context.Context) ([]KBCategoryView, error) {
	var cats []model.KBCategory
	if err := database.DB.WithContext(ctx).Order("name ASC").Find(&cats).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(cats))
	if len(cats) > 0 {
		ids := make([]string, 0, len(cats))
		for i := range cats {
			ids = append(ids, cats[i].ID)
		}
		type row struct {
			CategoryID string `gorm:"column:category_id"`
			Cnt        int    `gorm:"column:cnt"`
		}
		var rows []row
		if err := database.DB.WithContext(ctx).Model(&model.KBDocument{}).
			Select("category_id, count(*) as cnt").
			Where("category_id IN ? AND status = ?", ids, model.KBStatusActive).
			Group("category_id").Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			counts[row.CategoryID] = row.Cnt
		}
	}
	views := make([]KBCategoryView, 0, len(cats))
	for i := range cats {
		views = append(views, KBCategoryView{KBCategory: cats[i], DocumentCount: counts[cats[i].ID]})
	}
	return views, nil
}

func (r *kbCategoryRepository) CountDocuments(ctx context.Context, categoryID, status string) (int64, error) {
	query := database.DB.WithContext(ctx).Model(&model.KBDocument{}).Where("category_id = ?", categoryID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var cnt int64
	if err := query.Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}

// KBDocumentListFilter 条目列表过滤条件
type KBDocumentListFilter struct {
	CategoryIDs []string // 分类范围 (M11.5: 支持多分类, 顶级点选 = 父 + 子)
	Keyword     string   // 标题 + 正文 ILIKE
	Source      string
	Status      string
	Page        int
	PageSize    int
}

// KBDocumentView 条目视图 (含分类名)
type KBDocumentView struct {
	model.KBDocument
	CategoryName string `json:"category_name"`
	ChunkCount   int64  `json:"chunk_count,omitempty"` // M11.5: 分块数 (0 = 未分块/未启用分块)
}

// KBDocumentRepository 知识条目仓储
type KBDocumentRepository interface {
	Create(ctx context.Context, doc *model.KBDocument) error
	Get(ctx context.Context, id string) (*model.KBDocument, error)
	Update(ctx context.Context, doc *model.KBDocument) error
	Delete(ctx context.Context, id string) error
	UpdateStatus(ctx context.Context, id, status string) error
	List(ctx context.Context, filter KBDocumentListFilter) ([]KBDocumentView, int64, error)
	// SearchCandidates 关键词路径候选集: active 条目, 可选分类范围, updated_at 降序, 上限 limit
	SearchCandidates(ctx context.Context, categoryIDs []string, status string, limit int) ([]model.KBDocument, error)
	// SearchByVector 向量召回 (M11 两阶段检索一阶段): 余弦距离升序 (pgvector HNSW 索引),
	// 限定 active + 已向量化, categoryIDs 为空 = 全部分类 (平台级试算); 返回上限 limit
	SearchByVector(ctx context.Context, categoryIDs []string, vec *model.Vector, limit int) ([]model.KBDocument, error)
	// ListUnembedded 未向量化的 active 条目 (回填用, updated_at 升序, 上限 limit)
	ListUnembedded(ctx context.Context, limit int) ([]model.KBDocument, error)
	// CountUnembedded 未向量化的 active 条目数 (回填任务 total 统计)
	CountUnembedded(ctx context.Context) (int64, error)
	// SetEmbeddingIfNull 回写向量 (M11.5 幂等: 仅 embedding IS NULL 时写入, 并发后写不覆盖)
	SetEmbeddingIfNull(ctx context.Context, id string, vec *model.Vector) error
	// SetEmbedding 回写单条向量 (异步向量化 / 回填; vec 为空时忽略)
	SetEmbedding(ctx context.Context, id string, vec *model.Vector) error
	// BumpAccess 命中统计回写 (access_count +1 / last_accessed_at, 异步 fire-and-forget 调用)
	BumpAccess(ctx context.Context, id string, at time.Time) error
	// GetByIDs 批量查条目 (块级向量召回后按条目聚合; 查不到的 ID 跳过)
	GetByIDs(ctx context.Context, ids []string) (map[string]*model.KBDocument, error)
	// CreateWithChunks 新建条目 (M11.5 块模式: chunkContents 非 nil 时同事务写入分块, 任一失败整体回滚)
	CreateWithChunks(ctx context.Context, doc *model.KBDocument, chunkContents []string) error
	// UpdateWithChunks 更新条目 (M11.5 块模式: 同事务重建分块, 任一失败整体回滚)
	UpdateWithChunks(ctx context.Context, doc *model.KBDocument, chunkContents []string) error
	// DeleteWithChunks 删除条目 (M11.5 块模式: 同事务删除分块, 不产生孤儿块)
	DeleteWithChunks(ctx context.Context, id string) error
}

type kbDocumentRepository struct{}

func NewKBDocumentRepository() KBDocumentRepository {
	return &kbDocumentRepository{}
}

func (r *kbDocumentRepository) Create(ctx context.Context, doc *model.KBDocument) error {
	return database.DB.WithContext(ctx).Create(doc).Error
}

func (r *kbDocumentRepository) Get(ctx context.Context, id string) (*model.KBDocument, error) {
	var doc model.KBDocument
	if err := database.DB.WithContext(ctx).First(&doc, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound || strings.Contains(err.Error(), "invalid input syntax for type uuid") {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &doc, nil
}

func (r *kbDocumentRepository) Update(ctx context.Context, doc *model.KBDocument) error {
	return database.DB.WithContext(ctx).Save(doc).Error
}

func (r *kbDocumentRepository) Delete(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Delete(&model.KBDocument{}, "id = ?", id).Error
}

func (r *kbDocumentRepository) UpdateStatus(ctx context.Context, id, status string) error {
	return database.DB.WithContext(ctx).Model(&model.KBDocument{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": status}).Error
}

func (r *kbDocumentRepository) List(ctx context.Context, filter KBDocumentListFilter) ([]KBDocumentView, int64, error) {
	query := database.DB.WithContext(ctx).Model(&model.KBDocument{})
	if len(filter.CategoryIDs) > 0 {
		query = query.Where("kb_documents.category_id IN ?", filter.CategoryIDs)
	}
	if filter.Source != "" {
		query = query.Where("kb_documents.source = ?", filter.Source)
	}
	if filter.Status != "" {
		query = query.Where("kb_documents.status = ?", filter.Status)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("kb_documents.title ILIKE ? OR kb_documents.content ILIKE ?", like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	size := filter.PageSize
	if size < 1 || size > 100 {
		size = 20
	}

	var docs []model.KBDocument
	if err := query.Order("kb_documents.updated_at DESC, kb_documents.created_at DESC").
		Offset((page - 1) * size).Limit(size).Find(&docs).Error; err != nil {
		return nil, 0, err
	}

	views := make([]KBDocumentView, 0, len(docs))
	if len(docs) > 0 {
		catIDs := make([]string, 0, len(docs))
		seen := make(map[string]bool)
		for i := range docs {
			if !seen[docs[i].CategoryID] {
				seen[docs[i].CategoryID] = true
				catIDs = append(catIDs, docs[i].CategoryID)
			}
		}
		names := make(map[string]string, len(catIDs))
		var cats []model.KBCategory
		if err := database.DB.WithContext(ctx).Where("id IN ?", catIDs).Find(&cats).Error; err != nil {
			return nil, 0, err
		}
		// M11.5: 子级分类展示完整路径 「父/子」 消除同级重名歧义 (顶级保持原名)
		parentByID := make(map[string]*model.KBCategory, len(cats))
		parentIDs := make([]string, 0)
		for i := range cats {
			if cats[i].ParentID != nil {
				parentByID[cats[i].ID] = &cats[i]
				parentIDs = append(parentIDs, *cats[i].ParentID)
			}
		}
		if len(parentIDs) > 0 {
			var parents []model.KBCategory
			if err := database.DB.WithContext(ctx).Where("id IN ?", parentIDs).Find(&parents).Error; err != nil {
				return nil, 0, err
			}
			for i := range parents {
				names[parents[i].ID] = parents[i].Name
			}
		}
		// M11.5: 批量统计分块数 (避免 N+1; 无块 = 0, omitempty 不输出)
		chunkCounts := map[string]int64{}
		{
			docIDs := make([]string, 0, len(docs))
			for i := range docs {
				docIDs = append(docIDs, docs[i].ID)
			}
			type row struct {
				DocID string
				Cnt   int64
			}
			var rows []row
			if err := database.DB.WithContext(ctx).Model(&model.KBChunk{}).
				Select("doc_id, count(*) as cnt").Where("doc_id IN ?", docIDs).
				Group("doc_id").Scan(&rows).Error; err == nil {
				chunkCounts = make(map[string]int64, len(rows))
				for _, x := range rows {
					chunkCounts[x.DocID] = x.Cnt
				}
			}
		}
		for i := range docs {
			cname := names[docs[i].CategoryID]
			if parent, ok := parentByID[docs[i].CategoryID]; ok {
				if pname := names[*parent.ParentID]; pname != "" {
					cname = pname + "/" + cname
				}
			}
			views = append(views, KBDocumentView{KBDocument: docs[i], CategoryName: cname, ChunkCount: chunkCounts[docs[i].ID]})
		}
	}
	return views, total, nil
}

func (r *kbDocumentRepository) SearchCandidates(ctx context.Context, categoryIDs []string, status string, limit int) ([]model.KBDocument, error) {
	if limit <= 0 {
		limit = 500
	}
	query := database.DB.WithContext(ctx).Model(&model.KBDocument{})
	if len(categoryIDs) > 0 {
		query = query.Where("category_id IN ?", categoryIDs)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var docs []model.KBDocument
	if err := query.Order("updated_at DESC").Limit(limit).Find(&docs).Error; err != nil {
		return nil, err
	}
	return docs, nil
}

// SearchByVector 向量召回 (pgvector 余弦距离, 走 HNSW 部分索引 idx_kb_doc_embedding)
func (r *kbDocumentRepository) SearchByVector(ctx context.Context, categoryIDs []string, vec *model.Vector, limit int) ([]model.KBDocument, error) {
	if vec == nil || vec.IsNull() {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	vecVal, err := vec.Value()
	if err != nil {
		return nil, err
	}
	// 分类过滤必须写入 raw SQL 模板: Raw().Where() 会把条件追加到 LIMIT 之后导致语法错误
	where := "status = ? AND embedding IS NOT NULL"
	args := []interface{}{model.KBStatusActive}
	if len(categoryIDs) > 0 {
		where += " AND category_id IN ?"
		args = append(args, categoryIDs)
	}
	query := database.DB.WithContext(ctx).Raw(
		"SELECT * FROM kb_documents WHERE "+where+" ORDER BY embedding <=> ?::vector LIMIT ?",
		append(args, vecVal, limit)...,
	)
	var docs []model.KBDocument
	if err := query.Scan(&docs).Error; err != nil {
		return nil, err
	}
	return docs, nil
}

// ListUnembedded 未向量化的 active 条目 (回填入口, 批次上限 limit)
func (r *kbDocumentRepository) ListUnembedded(ctx context.Context, limit int) ([]model.KBDocument, error) {
	if limit <= 0 {
		limit = 500
	}
	var docs []model.KBDocument
	if err := database.DB.WithContext(ctx).
		Where("status = ? AND embedding IS NULL", model.KBStatusActive).
		Order("updated_at ASC").Limit(limit).Find(&docs).Error; err != nil {
		return nil, err
	}
	return docs, nil
}

// CountUnembedded 未向量化的 active 条目数 (回填任务 total 统计)
func (r *kbDocumentRepository) CountUnembedded(ctx context.Context) (int64, error) {
	var cnt int64
	if err := database.DB.WithContext(ctx).Model(&model.KBDocument{}).
		Where("status = ? AND embedding IS NULL", model.KBStatusActive).Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}

// SetEmbeddingIfNull 回写向量 (M11.5 幂等: 仅 embedding IS NULL 时写入;
// 与异步向量化 / 回填任务并发时不互相覆盖, 内容确定故后写胜等价)
func (r *kbDocumentRepository) SetEmbeddingIfNull(ctx context.Context, id string, vec *model.Vector) error {
	if vec == nil || vec.IsNull() {
		return nil
	}
	vecVal, err := vec.Value()
	if err != nil {
		return err
	}
	return database.DB.WithContext(ctx).Model(&model.KBDocument{}).
		Where("id = ? AND embedding IS NULL", id).
		Updates(map[string]interface{}{"embedding": vecVal}).Error
}

// SetEmbedding 回写单条向量 (text 形式, Postgres 按列类型转 vector)
func (r *kbDocumentRepository) SetEmbedding(ctx context.Context, id string, vec *model.Vector) error {
	if vec == nil || vec.IsNull() {
		return nil
	}
	vecVal, err := vec.Value()
	if err != nil {
		return err
	}
	return database.DB.WithContext(ctx).Model(&model.KBDocument{}).Where("id = ?", id).
		Updates(map[string]interface{}{"embedding": vecVal}).Error
}

func (r *kbDocumentRepository) BumpAccess(ctx context.Context, id string, at time.Time) error {
	return database.DB.WithContext(ctx).Model(&model.KBDocument{}).Where("id = ?", id).
		Updates(map[string]interface{}{"access_count": gorm.Expr("access_count + 1"), "last_accessed_at": at}).Error
}

// GetByIDs 批量查条目 (块级向量召回后按条目聚合; 查不到的 ID 跳过)
func (r *kbDocumentRepository) GetByIDs(ctx context.Context, ids []string) (map[string]*model.KBDocument, error) {
	if len(ids) == 0 {
		return map[string]*model.KBDocument{}, nil
	}
	var docs []model.KBDocument
	if err := database.DB.WithContext(ctx).Where("id IN ?", ids).Find(&docs).Error; err != nil {
		return nil, err
	}
	out := make(map[string]*model.KBDocument, len(docs))
	for i := range docs {
		out[docs[i].ID] = &docs[i]
	}
	return out, nil
}

// chunksOf 块内容列表 -> KBChunk 行 (向量 NULL, 同事务写入)
func chunksOf(docID string, contents []string) []model.KBChunk {
	now := time.Now()
	chunks := make([]model.KBChunk, 0, len(contents))
	for i, c := range contents {
		chunks = append(chunks, model.KBChunk{DocID: docID, ChunkIndex: i, Content: c, UpdatedAt: now})
	}
	return chunks
}

// CreateWithChunks 新建条目 + 同事务写块 (M11.5 块模式写路径)
func (r *kbDocumentRepository) CreateWithChunks(ctx context.Context, doc *model.KBDocument, chunkContents []string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(doc).Error; err != nil {
			return err
		}
		if len(chunkContents) == 0 {
			return nil
		}
		rows := chunksOf(doc.ID, chunkContents)
		return tx.Create(&rows).Error
	})
}

// UpdateWithChunks 更新条目 + 同事务重建块 (M11.5 块模式写路径; 块向量 NULL 待异步向量化)
func (r *kbDocumentRepository) UpdateWithChunks(ctx context.Context, doc *model.KBDocument, chunkContents []string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(doc).Error; err != nil {
			return err
		}
		if err := tx.Where("doc_id = ?", doc.ID).Delete(&model.KBChunk{}).Error; err != nil {
			return err
		}
		if len(chunkContents) == 0 {
			return nil
		}
		rows := chunksOf(doc.ID, chunkContents)
		return tx.Create(&rows).Error
	})
}

// DeleteWithChunks 删除条目 + 同事务删块 (M11.5 块模式; 不产生孤儿块)
func (r *kbDocumentRepository) DeleteWithChunks(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("doc_id = ?", id).Delete(&model.KBChunk{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.KBDocument{}, "id = ?", id).Error
	})
}

// AgentKBBindingRepository Agent 知识库分类绑定仓储
type AgentKBBindingRepository interface {
	// Bind 幂等绑定 (M11.5: 已存在时同步 read_only 标志; 新绑定按 readOnly 创建)
	Bind(ctx context.Context, agentID, categoryID string, readOnly bool, operatorID *string) error
	Unbind(ctx context.Context, agentID, categoryID string) error
	ListByAgent(ctx context.Context, agentID string) ([]model.AgentKBBinding, error)
	ListByCategory(ctx context.Context, categoryID string) ([]model.AgentKBBinding, error)
	DeleteByAgent(ctx context.Context, agentID string) error
	DeleteByCategory(ctx context.Context, categoryID string) error
}

type agentKBBindingRepository struct{}

func NewAgentKBBindingRepository() AgentKBBindingRepository {
	return &agentKBBindingRepository{}
}

func (r *agentKBBindingRepository) Bind(ctx context.Context, agentID, categoryID string, readOnly bool, operatorID *string) error {
	var existing model.AgentKBBinding
	err := database.DB.WithContext(ctx).
		Where("agent_id = ? AND category_id = ?", agentID, categoryID).
		First(&existing).Error
	if err == nil {
		if existing.ReadOnly != readOnly {
			return database.DB.WithContext(ctx).Model(&model.AgentKBBinding{}).Where("id = ?", existing.ID).
				Update("read_only", readOnly).Error
		}
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	return database.DB.WithContext(ctx).Create(&model.AgentKBBinding{
		AgentID:    agentID,
		CategoryID: categoryID,
		ReadOnly:   readOnly,
	}).Error
}

func (r *agentKBBindingRepository) Unbind(ctx context.Context, agentID, categoryID string) error {
	return database.DB.WithContext(ctx).
		Where("agent_id = ? AND category_id = ?", agentID, categoryID).
		Delete(&model.AgentKBBinding{}).Error
}

func (r *agentKBBindingRepository) ListByAgent(ctx context.Context, agentID string) ([]model.AgentKBBinding, error) {
	var bindings []model.AgentKBBinding
	if err := database.DB.WithContext(ctx).Where("agent_id = ?", agentID).Find(&bindings).Error; err != nil {
		return nil, err
	}
	return bindings, nil
}

func (r *agentKBBindingRepository) ListByCategory(ctx context.Context, categoryID string) ([]model.AgentKBBinding, error) {
	var bindings []model.AgentKBBinding
	if err := database.DB.WithContext(ctx).Where("category_id = ?", categoryID).Find(&bindings).Error; err != nil {
		return nil, err
	}
	return bindings, nil
}

func (r *agentKBBindingRepository) DeleteByAgent(ctx context.Context, agentID string) error {
	return database.DB.WithContext(ctx).Where("agent_id = ?", agentID).
		Delete(&model.AgentKBBinding{}).Error
}

func (r *agentKBBindingRepository) DeleteByCategory(ctx context.Context, categoryID string) error {
	return database.DB.WithContext(ctx).Where("category_id = ?", categoryID).
		Delete(&model.AgentKBBinding{}).Error
}

// KBBackfillTaskRepository 向量回填任务仓储 (M11.5: 状态持久化, 批边界进度更新)
type KBBackfillTaskRepository interface {
	Create(ctx context.Context, task *model.KBBackfillTask) error
	Get(ctx context.Context, id string) (*model.KBBackfillTask, error)
	// FindActive 进行中的任务 (pending/running; 无则 nil, nil)
	FindActive(ctx context.Context) (*model.KBBackfillTask, error)
	// ListRecent 最近任务 (created_at 降序, 上限 limit)
	ListRecent(ctx context.Context, limit int) ([]model.KBBackfillTask, error)
	// SetTotal 启动时写入 total
	SetTotal(ctx context.Context, id string, total int) error
	// AddTotal 增量追加 total (任务运行期间新出现未向量化条目时)
	AddTotal(ctx context.Context, id string, delta int) error
	// SetRunning pending -> running (started_at 写入)
	SetRunning(ctx context.Context, id string, at time.Time) error
	// AddProgress 批边界增量更新 done/failed (原子自增)
	AddProgress(ctx context.Context, id string, done, failed int) error
	// Finish 写终态 (succeeded/failed/cancelled) + finished_at
	Finish(ctx context.Context, id, status, lastError string, at time.Time) error
	// MarkOrphansFailed 启动恢复: 残留 pending/running 任务标 failed (服务重启)
	MarkOrphansFailed(ctx context.Context, reason string) (int64, error)
}

type kbBackfillTaskRepository struct{}

func NewKBBackfillTaskRepository() KBBackfillTaskRepository {
	return &kbBackfillTaskRepository{}
}

func (r *kbBackfillTaskRepository) Create(ctx context.Context, task *model.KBBackfillTask) error {
	return database.DB.WithContext(ctx).Create(task).Error
}

func (r *kbBackfillTaskRepository) Get(ctx context.Context, id string) (*model.KBBackfillTask, error) {
	var task model.KBBackfillTask
	if err := database.DB.WithContext(ctx).First(&task, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound || strings.Contains(err.Error(), "invalid input syntax for type uuid") {
			return nil, errors.ErrNotFound
		}
		return nil, err
	}
	return &task, nil
}

func (r *kbBackfillTaskRepository) FindActive(ctx context.Context) (*model.KBBackfillTask, error) {
	var task model.KBBackfillTask
	err := database.DB.WithContext(ctx).
		Where("status IN ?", []string{model.KBTaskStatusPending, model.KBTaskStatusRunning}).
		Order("created_at ASC").First(&task).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &task, nil
}

func (r *kbBackfillTaskRepository) ListRecent(ctx context.Context, limit int) ([]model.KBBackfillTask, error) {
	if limit <= 0 {
		limit = 20
	}
	var tasks []model.KBBackfillTask
	if err := database.DB.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *kbBackfillTaskRepository) SetTotal(ctx context.Context, id string, total int) error {
	return database.DB.WithContext(ctx).Model(&model.KBBackfillTask{}).Where("id = ?", id).
		Update("total", total).Error
}

func (r *kbBackfillTaskRepository) AddTotal(ctx context.Context, id string, delta int) error {
	return database.DB.WithContext(ctx).Model(&model.KBBackfillTask{}).Where("id = ?", id).
		Updates(map[string]interface{}{"total": gorm.Expr("total + ?", delta)}).Error
}

func (r *kbBackfillTaskRepository) SetRunning(ctx context.Context, id string, at time.Time) error {
	return database.DB.WithContext(ctx).Model(&model.KBBackfillTask{}).
		Where("id = ? AND status = ?", id, model.KBTaskStatusPending).
		Updates(map[string]interface{}{"status": model.KBTaskStatusRunning, "started_at": at}).Error
}

func (r *kbBackfillTaskRepository) AddProgress(ctx context.Context, id string, done, failed int) error {
	return database.DB.WithContext(ctx).Model(&model.KBBackfillTask{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"done":   gorm.Expr("done + ?", done),
			"failed": gorm.Expr("failed + ?", failed),
		}).Error
}

func (r *kbBackfillTaskRepository) Finish(ctx context.Context, id, status, lastError string, at time.Time) error {
	return database.DB.WithContext(ctx).Model(&model.KBBackfillTask{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": status, "finished_at": at, "last_error": lastError}).Error
}

func (r *kbBackfillTaskRepository) MarkOrphansFailed(ctx context.Context, reason string) (int64, error) {
	res := database.DB.WithContext(ctx).Model(&model.KBBackfillTask{}).
		Where("status IN ?", []string{model.KBTaskStatusPending, model.KBTaskStatusRunning}).
		Updates(map[string]interface{}{
			"status":      model.KBTaskStatusFailed,
			"last_error":  reason,
			"finished_at": time.Now(),
		})
	return res.RowsAffected, res.Error
}

// ---------- M11.5 事项 4: 分块仓储 (块级向量; 仅 active 条目参与召回/回填) ----------

// KBChunkRepository 知识条目分块仓储
type KBChunkRepository interface {
	// Rebuild 同事务重建条目全部分块 (删旧 + 插新, 向量 NULL)
	Rebuild(ctx context.Context, docID string, contents []string) error
	// DeleteByDoc 删除条目全部分块
	DeleteByDoc(ctx context.Context, docID string) error
	// ListByDoc 条目全部分块 (chunk_index 升序)
	ListByDoc(ctx context.Context, docID string) ([]model.KBChunk, error)
	// CountByDoc 单条目块数
	CountByDoc(ctx context.Context, docID string) (int64, error)
	// CountByDocs 批量块数 (条目列表 chunk_count 避免 N+1)
	CountByDocs(ctx context.Context, docIDs []string) (map[string]int64, error)
	// ClearEmbeddings 清空条目块向量 (标题变更 → 重新向量化)
	ClearEmbeddings(ctx context.Context, docID string) error
	// SearchByVector 块级向量召回 (active 条目 + 已向量化块, HNSW 部分索引; categoryIDs 空 = 全部)
	SearchByVector(ctx context.Context, categoryIDs []string, vec *model.Vector, limit int) ([]model.KBChunk, error)
	// ListUnembedded 未向量化的 active 条目块 (回填入口, 条目 updated_at 升序, 上限 limit)
	ListUnembedded(ctx context.Context, limit int) ([]model.KBChunk, error)
	// CountUnembedded 未向量化的 active 条目块数 (回填任务 total 统计)
	CountUnembedded(ctx context.Context) (int64, error)
	// SetEmbeddingIfNull 回写块向量 (幂等: 仅 embedding IS NULL 时写入)
	SetEmbeddingIfNull(ctx context.Context, id string, vec *model.Vector) error
	// MissingDocs 无块的 active 条目 (存量分块迁移入口, 分页)
	MissingDocs(ctx context.Context, limit, offset int) ([]model.KBDocument, error)
}

type kbChunkRepository struct{}

func NewKBChunkRepository() KBChunkRepository {
	return &kbChunkRepository{}
}

// Rebuild 同事务重建 (删旧 + 插新, 向量 NULL; contents 为空 = 仅清空)
func (r *kbChunkRepository) Rebuild(ctx context.Context, docID string, contents []string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("doc_id = ?", docID).Delete(&model.KBChunk{}).Error; err != nil {
			return err
		}
		if len(contents) == 0 {
			return nil
		}
		now := time.Now()
		chunks := make([]model.KBChunk, 0, len(contents))
		for i, c := range contents {
			chunks = append(chunks, model.KBChunk{DocID: docID, ChunkIndex: i, Content: c, UpdatedAt: now})
		}
		return tx.Create(&chunks).Error
	})
}

func (r *kbChunkRepository) DeleteByDoc(ctx context.Context, docID string) error {
	return database.DB.WithContext(ctx).Where("doc_id = ?", docID).Delete(&model.KBChunk{}).Error
}

func (r *kbChunkRepository) ListByDoc(ctx context.Context, docID string) ([]model.KBChunk, error) {
	var chunks []model.KBChunk
	if err := database.DB.WithContext(ctx).Where("doc_id = ?", docID).
		Order("chunk_index ASC").Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

func (r *kbChunkRepository) CountByDoc(ctx context.Context, docID string) (int64, error) {
	var cnt int64
	if err := database.DB.WithContext(ctx).Model(&model.KBChunk{}).
		Where("doc_id = ?", docID).Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}

func (r *kbChunkRepository) CountByDocs(ctx context.Context, docIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(docIDs))
	if len(docIDs) == 0 {
		return out, nil
	}
	type row struct {
		DocID string
		Cnt   int64
	}
	var rows []row
	if err := database.DB.WithContext(ctx).Model(&model.KBChunk{}).
		Select("doc_id, count(*) as cnt").Where("doc_id IN ?", docIDs).
		Group("doc_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r2 := range rows {
		out[r2.DocID] = r2.Cnt
	}
	return out, nil
}

func (r *kbChunkRepository) ClearEmbeddings(ctx context.Context, docID string) error {
	return database.DB.WithContext(ctx).Model(&model.KBChunk{}).
		Where("doc_id = ?", docID).
		Updates(map[string]interface{}{"embedding": nil, "updated_at": time.Now()}).Error
}

// SearchByVector 块级向量召回 (JOIN 条目: active + 分类范围; 分类过滤写入 raw SQL 模板)
func (r *kbChunkRepository) SearchByVector(ctx context.Context, categoryIDs []string, vec *model.Vector, limit int) ([]model.KBChunk, error) {
	if vec == nil || vec.IsNull() {
		return nil, nil
	}
	if limit <= 0 {
		limit = 40
	}
	vecVal, err := vec.Value()
	if err != nil {
		return nil, err
	}
	where := "d.status = ? AND c.embedding IS NOT NULL"
	args := []interface{}{model.KBStatusActive}
	if len(categoryIDs) > 0 {
		where += " AND d.category_id IN ?"
		args = append(args, categoryIDs)
	}
	query := database.DB.WithContext(ctx).Raw(
		"SELECT c.* FROM kb_chunks c JOIN kb_documents d ON d.id = c.doc_id WHERE "+where+" ORDER BY c.embedding <=> ?::vector LIMIT ?",
		append(args, vecVal, limit)...,
	)
	var chunks []model.KBChunk
	if err := query.Scan(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// ListUnembedded 未向量化的 active 条目块 (回填入口; 按条目更新时间 + 块序稳定排序)
func (r *kbChunkRepository) ListUnembedded(ctx context.Context, limit int) ([]model.KBChunk, error) {
	if limit <= 0 {
		limit = 32
	}
	var chunks []model.KBChunk
	err := database.DB.WithContext(ctx).Raw(
		"SELECT c.* FROM kb_chunks c JOIN kb_documents d ON d.id = c.doc_id WHERE d.status = ? AND c.embedding IS NULL ORDER BY d.updated_at ASC, c.chunk_index ASC LIMIT ?",
		model.KBStatusActive, limit,
	).Scan(&chunks).Error
	return chunks, err
}

func (r *kbChunkRepository) CountUnembedded(ctx context.Context) (int64, error) {
	var cnt int64
	err := database.DB.WithContext(ctx).Raw(
		"SELECT count(*) FROM kb_chunks c JOIN kb_documents d ON d.id = c.doc_id WHERE d.status = ? AND c.embedding IS NULL",
		model.KBStatusActive,
	).Scan(&cnt).Error
	return cnt, err
}

func (r *kbChunkRepository) SetEmbeddingIfNull(ctx context.Context, id string, vec *model.Vector) error {
	if vec == nil || vec.IsNull() {
		return nil
	}
	vecVal, err := vec.Value()
	if err != nil {
		return err
	}
	return database.DB.WithContext(ctx).Model(&model.KBChunk{}).
		Where("id = ? AND embedding IS NULL", id).
		Updates(map[string]interface{}{"embedding": vecVal, "updated_at": time.Now()}).Error
}

// MissingDocs 无块的 active 条目 (存量分块迁移: 幂等, 已有块的条目跳过)
func (r *kbChunkRepository) MissingDocs(ctx context.Context, limit, offset int) ([]model.KBDocument, error) {
	if limit <= 0 {
		limit = 500
	}
	var docs []model.KBDocument
	if err := database.DB.WithContext(ctx).Raw(
		"SELECT d.* FROM kb_documents d WHERE d.status = ? AND NOT EXISTS (SELECT 1 FROM kb_chunks c WHERE c.doc_id = d.id) ORDER BY d.updated_at ASC LIMIT ? OFFSET ?",
		model.KBStatusActive, limit, offset,
	).Scan(&docs).Error; err != nil {
		return nil, err
	}
	return docs, nil
}
