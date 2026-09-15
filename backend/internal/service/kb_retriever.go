package service

// kb_retriever.go — M11 知识库检索组件: 两阶段流水线 (计划 §2 检索组件)
//
// 阶段一 召回 (KB_RECALL_SIZE, 默认 20):
//   a. embedding 已配置: 查询向量化 -> pgvector 余弦召回 (HNSW 部分索引);
//   b. embedding 未配置 (或调用失败降级): 关键词预筛 (M10 打分公式, 候选集 TTL 缓存, 上限 KB_MAX_CANDIDATES) -> top 20。
//
// 阶段二 排序 (top-K, KB_TOP_K 默认 3):
//   - rerank 已配置: POST /rerank 对候选重排 (KB_RERANK_TIMEOUT 超时); 超时/失败 -> 按召回序排序;
//   - rerank 未配置: 直接按召回序取 top-K。
//
// 四级降级阶梯 (按模型配置组合决定主路径, 运行时故障继续向下降级):
//  1. embed + rerank: 向量召回 + rerank 重排
//  2. 仅 embed:       向量召回 + 召回序排序
//  3. 仅 rerank:      关键词预筛 + rerank 重排
//  4. 均未配置:       关键词打分 (W1 行为)
//
// 硬约束: 任何一步失败/超时 -> 返回空/降级结果, 永不阻断对话 (调用方对错误仅告警)。

import (
	"context"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-platform/internal/config"
	"agent-platform/internal/model"
	"agent-platform/internal/repository"
)

// KBSearchOptions 单次检索参数
type KBSearchOptions struct {
	TopK         int // 0 = KB_TOP_K
	ExcerptRunes int // 每条摘录 rune 上限; 0 = kbExcerptRunes (300, 注入/试算); 工具路径 1500
	BumpAccess   bool // 命中条目异步回写访问统计 (Agent 真实检索路径 true; 平台试算 false)
}

// KBRetriever 知识库两阶段检索器 (M11; M11.5: chunks 非 nil = 块级向量召回 + 按条目聚合)
type KBRetriever struct {
	docs     repository.KBDocumentRepository
	cats     repository.KBCategoryRepository
	bindings repository.AgentKBBindingRepository
	chunks   repository.KBChunkRepository // M11.5: nil = 整条向量路径 (KB_CHUNK_ENABLED=false 回退)
	embedder KBEmbedder
	reranker KBReranker
	cfg      config.KBConfig
	cache    *kbCandidateCache
}

// NewKBRetriever 创建检索器; cacheTTL 为关键词候选集缓存 TTL (默认 60s); chunks 非 nil = 块级召回路径
func NewKBRetriever(
	docs repository.KBDocumentRepository,
	cats repository.KBCategoryRepository,
	bindings repository.AgentKBBindingRepository,
	chunks repository.KBChunkRepository,
	embedder KBEmbedder,
	reranker KBReranker,
	cfg config.KBConfig,
	cacheTTL time.Duration,
) *KBRetriever {
	if cacheTTL <= 0 {
		cacheTTL = 60 * time.Second
	}
	return &KBRetriever{
		docs:     docs,
		cats:     cats,
		bindings: bindings,
		chunks:   chunks,
		embedder: embedder,
		reranker: reranker,
		cfg:      cfg,
		cache:    newKBCandidateCache(cacheTTL),
	}
}

// InvalidateCandidates 写失效: 条目/分类变更后清空候选集缓存 (全量, P0 规模下成本可忽略)
func (r *KBRetriever) InvalidateCandidates() {
	if r != nil {
		r.cache.invalidateAll()
	}
}

// BoundCategoryIDs 服务端加载 Agent 当前绑定的读作用域分类 ID 集合 (三条路径重新鉴权的数据源)。
// M11.5: 绑定顶级分类自动含其全部子级 (绑父不建子绑定行, 扩展在运行时完成);
// 存量无父子 (parent_id 全 NULL) 时扩展为 no-op。只读绑定与读写绑定读作用域一致 (只读仅约束写路径)。
func (r *KBRetriever) BoundCategoryIDs(ctx context.Context, agentID string) ([]string, error) {
	bindings, err := r.bindings.ListByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(bindings))
	for i := range bindings {
		ids = append(ids, bindings[i].CategoryID)
	}
	return r.expandCategoryChildren(ctx, ids)
}

// WritableCategoryIDs Agent 可写作用域 (M11.5 只读绑定): 仅读写绑定 + 其子级扩展。
// 一键总结入库目标分类必须 ∈ 该集合, 只读分类 (含其子级) 禁止写入。
func (r *KBRetriever) WritableCategoryIDs(ctx context.Context, agentID string) ([]string, error) {
	bindings, err := r.bindings.ListByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(bindings))
	for i := range bindings {
		if !bindings[i].ReadOnly {
			ids = append(ids, bindings[i].CategoryID)
		}
	}
	return r.expandCategoryChildren(ctx, ids)
}

// expandCategoryChildren 绑定分类 ∪ 其直接子级 (两级层级, 子级不再展开; 去重, 顺序稳定)。
// 分类树查询失败时返回原集合并告警 (读路径降级为仅绑定分类, 不阻断检索)。
func (r *KBRetriever) expandCategoryChildren(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return ids, nil
	}
	children, err := r.cats.ListByParents(ctx, ids)
	if err != nil {
		log.Printf("kb: expand bound categories failed: %v (按未扩展作用域继续)", err)
		return ids, nil
	}
	seen := make(map[string]bool, len(ids)+len(children))
	out := make([]string, 0, len(ids)+len(children))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for i := range children {
		if !seen[children[i].ID] {
			seen[children[i].ID] = true
			out = append(out, children[i].ID)
		}
	}
	return out, nil
}

// Retrieve Agent 作用域检索 (注入 / search_knowledge 工具 / Agent 试算共用):
// 按 Agent 当前绑定分类重新鉴权 (不信任调用方传参), 命中条目异步回写访问统计
func (r *KBRetriever) Retrieve(ctx context.Context, agentID, query string, opts KBSearchOptions) ([]KBSearchHit, error) {
	catIDs, err := r.BoundCategoryIDs(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if len(catIDs) == 0 {
		return []KBSearchHit{}, nil
	}
	hits, err := r.RetrieveByCategories(ctx, catIDs, query, opts)
	if err != nil {
		return nil, err
	}
	if opts.BumpAccess && len(hits) > 0 {
		r.bumpAccess(hits)
	}
	return hits, nil
}

// kbScoredDoc 携带相关度分的召回候选 (分 = 召回得分或 rerank 分, 随候选流转至 KBSearchHit.Score)
type kbScoredDoc struct {
	doc       model.KBDocument
	score     float64
	bestChunk *string // M11.5: 最佳命中块 (块级召回聚合); nil = 整条/关键词路径 (摘录取正文头部)
}

// RetrieveByCategories 分类作用域检索 (平台级试算传任意分类; 空分类集 = 全部)
func (r *KBRetriever) RetrieveByCategories(ctx context.Context, categoryIDs []string, query string, opts KBSearchOptions) ([]KBSearchHit, error) {
	query = strings.TrimSpace(query)
	topK := opts.TopK
	if topK <= 0 {
		topK = r.cfg.TopK
	}
	if topK > model.KBSearchMaxTop {
		topK = model.KBSearchMaxTop
	}

	candidates := r.recall(ctx, categoryIDs, query)
	if len(candidates) == 0 {
		return []KBSearchHit{}, nil
	}

	// 阶段二: rerank 重排 (仅非空查询; 候选 <=1 条时重排无意义); 成功时 rerank 分取代召回分成为相关度
	if query != "" && r.reranker != nil && r.reranker.Enabled() && len(candidates) > 1 {
		if scores := r.tryRerank(ctx, query, candidates); scores != nil {
			candidates = sortByRerankScores(candidates, scores)
		}
	}

	if len(candidates) > topK {
		candidates = candidates[:topK]
	}

	excerptRunes := opts.ExcerptRunes
	if excerptRunes <= 0 {
		excerptRunes = kbExcerptRunes
	}
	nameByID := r.categoryNames(ctx, docsOf(candidates))
	hits := make([]KBSearchHit, 0, len(candidates))
	for i := range candidates {
		d := &candidates[i].doc
		// M11.5: 块级召回命中长条目时, 摘录输出最佳命中块 (替代固定头部摘录); 访问统计仍按条目
		excerptSource := d.Content
		if candidates[i].bestChunk != nil {
			excerptSource = *candidates[i].bestChunk
		}
		hits = append(hits, KBSearchHit{
			ID:           d.ID,
			CategoryID:   d.CategoryID,
			CategoryName: nameByID[d.CategoryID],
			Title:        d.Title,
			Excerpt:      kbExcerptWithLimit(excerptSource, excerptRunes),
			MatchedChunk: candidates[i].bestChunk,
			Score:        candidates[i].score,
			UpdatedAt:    d.UpdatedAt,
		})
	}
	return hits, nil
}

// recall 阶段一: 向量召回优先, 不可用/未配置时关键词预筛 (四级降级阶梯)
// 候选携带召回得分: 向量路径 = 余弦相似度 (与 pgvector <=> 距离同语义), 关键词路径 = kbKeywordScore / kbEmptyQueryScore
// M11.5: 块模式下向量召回走 kb_chunks (块级召回 + 按条目聚合); 关键词路径保持整条打分不变
func (r *KBRetriever) recall(ctx context.Context, categoryIDs []string, query string) []kbScoredDoc {
	// 阶梯 1/2: embedding 已配置 -> 向量召回 (HNSW)
	if query != "" && r.embedder != nil && r.embedder.Enabled() {
		vec, err := r.embedder.EmbedOne(ctx, query)
		if err != nil {
			// 运行时故障降级 (A8): 向量模型不可用 -> 关键词路径, 对话不阻断
			log.Printf("kb: query embed failed: %v (降级关键词召回)", err)
		} else if r.chunks != nil {
			// M11.5 块模式: 块级召回 + 按条目聚合 (失败返回 nil -> 降级关键词召回)
			if out := r.chunkRecall(ctx, categoryIDs, vec); out != nil {
				return out
			}
		} else if docs, vErr := r.docs.SearchByVector(ctx, categoryIDs, &model.Vector{V: vec}, r.cfg.RecallSize); vErr == nil {
			qv := f32ToF64(vec)
			out := make([]kbScoredDoc, 0, len(docs))
			for i := range docs {
				score := 0.0
				if !docs[i].Embedding.IsNull() {
					score = cosineSimilarity(qv, f32ToF64(docs[i].Embedding.V))
				}
				out = append(out, kbScoredDoc{doc: docs[i], score: score})
			}
			return out
		} else {
			log.Printf("kb: vector recall failed: %v (降级关键词召回)", vErr)
		}
	}
	// 阶梯 3/4: 关键词路径 (rerank-only 或纯关键词; 含空查询的续答轮)
	return r.keywordRecall(ctx, categoryIDs, query)
}

// chunkRecall 块级向量召回 (M11.5 事项 4): kb_chunks HNSW 召回 top RecallSize*ChunkRecallMult 块
// -> 按条目聚合 (每条目取最佳块余弦分) -> top RecallSize 条目; 失败/故障返回 nil (调用方降级关键词召回)
func (r *KBRetriever) chunkRecall(ctx context.Context, categoryIDs []string, vec []float32) []kbScoredDoc {
	recallSize := r.cfg.RecallSize
	if recallSize <= 0 {
		recallSize = 20
	}
	mult := r.cfg.ChunkRecallMult
	if mult <= 0 {
		mult = 2
	}
	chunks, err := r.chunks.SearchByVector(ctx, categoryIDs, &model.Vector{V: vec}, recallSize*mult)
	if err != nil {
		log.Printf("kb: chunk recall failed: %v (降级关键词召回)", err)
		return nil
	}
	if len(chunks) == 0 {
		return []kbScoredDoc{}
	}
	seen := make(map[string]bool, len(chunks))
	docIDs := make([]string, 0, len(chunks))
	for i := range chunks {
		if !seen[chunks[i].DocID] {
			seen[chunks[i].DocID] = true
			docIDs = append(docIDs, chunks[i].DocID)
		}
	}
	docs, err := r.docs.GetByIDs(ctx, docIDs)
	if err != nil {
		log.Printf("kb: chunk recall load docs failed: %v (降级关键词召回)", err)
		return nil
	}
	qv := f32ToF64(vec)
	type bestChunkScore struct {
		score float64
		chunk string
	}
	bestByDoc := make(map[string]bestChunkScore, len(docIDs))
	for i := range chunks {
		if chunks[i].Embedding.IsNull() {
			continue
		}
		d, ok := docs[chunks[i].DocID]
		if !ok {
			continue
		}
		score := cosineSimilarity(qv, f32ToF64(chunks[i].Embedding.V))
		if b, ok2 := bestByDoc[d.ID]; !ok2 || score > b.score {
			bestByDoc[d.ID] = bestChunkScore{score: score, chunk: chunks[i].Content}
		}
	}
	out := make([]kbScoredDoc, 0, len(bestByDoc))
	for id, b := range bestByDoc {
		c := b.chunk
		out = append(out, kbScoredDoc{doc: *docs[id], score: b.score, bestChunk: &c})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].score != out[b].score {
			return out[a].score > out[b].score
		}
		return out[a].doc.UpdatedAt.After(out[b].doc.UpdatedAt)
	})
	if len(out) > recallSize {
		out = out[:recallSize]
	}
	return out
}

// keywordRecall 关键词召回: 候选集 (TTL 缓存) -> 打分取 top RecallSize。
// 空查询 (审核续答轮) 无关键词要求, 按时间衰减 + 使用频率取近期条目 (同 M10 空查询语义)
func (r *KBRetriever) keywordRecall(ctx context.Context, categoryIDs []string, query string) []kbScoredDoc {
	candidates, err := r.cachedCandidates(ctx, categoryIDs)
	if err != nil {
		log.Printf("kb: load keyword candidates failed: %v", err)
		return nil
	}
	queryTokens := tokenizeText(query)
	now := time.Now()
	scored := make([]kbScoredDoc, 0, len(candidates))
	for i := range candidates {
		var score float64
		if len(queryTokens) == 0 {
			score = kbEmptyQueryScore(&candidates[i], now)
		} else {
			score = kbKeywordScore(query, &candidates[i], queryTokens, now)
		}
		if score > 0 {
			scored = append(scored, kbScoredDoc{doc: candidates[i], score: score})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].doc.UpdatedAt.After(scored[j].doc.UpdatedAt)
	})
	limit := r.cfg.RecallSize
	if limit <= 0 {
		limit = 20
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// tryRerank 阶段二: rerank 重排 (KB_RERANK_TIMEOUT 超时); 失败/超时返回 nil (调用方保持召回序)
func (r *KBRetriever) tryRerank(ctx context.Context, query string, candidates []kbScoredDoc) []float64 {
	rctx, cancel := context.WithTimeout(ctx, r.cfg.RerankTimeout)
	defer cancel()
	docs := make([]string, len(candidates))
	for i := range candidates {
		docs[i] = kbRerankDocText(&candidates[i].doc, candidates[i].bestChunk)
	}
	scores, err := r.reranker.Rerank(rctx, query, docs)
	if err != nil {
		log.Printf("kb: rerank failed: %v (按召回序排序)", err)
		return nil
	}
	if len(scores) != len(candidates) {
		log.Printf("kb: rerank score count mismatch (scores=%d, docs=%d) (按召回序排序)", len(scores), len(candidates))
		return nil
	}
	return scores
}

// kbRerankDocText 重排输入文本 (M11.5: 块模式 = 标题 + 最佳命中块; 整条/关键词模式 = 标题 + 正文头部;
// 单条上限 500 rune 控制请求体积)
const kbRerankDocRunes = 500

func kbRerankDocText(doc *model.KBDocument, bestChunk *string) string {
	text := doc.Title + ": "
	if bestChunk != nil {
		text += *bestChunk
	} else {
		text += doc.Content
	}
	runes := []rune(text)
	if len(runes) > kbRerankDocRunes {
		runes = runes[:kbRerankDocRunes]
	}
	return string(runes)
}

// sortByRerankScores 按 rerank 分数降序重排, 并以 rerank 分取代召回分作为相关度 (分数缺失视为 -inf 排最后; 同分保持召回序)
func sortByRerankScores(candidates []kbScoredDoc, scores []float64) []kbScoredDoc {
	idx := make([]int, len(candidates))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return scores[idx[a]] > scores[idx[b]]
	})
	out := make([]kbScoredDoc, len(candidates))
	for i, j := range idx {
		c := candidates[j]
		c.score = scores[j]
		out[i] = c
	}
	return out
}

// docsOf 提取候选文档 (批量解析分类名等)
func docsOf(cands []kbScoredDoc) []model.KBDocument {
	out := make([]model.KBDocument, 0, len(cands))
	for i := range cands {
		out = append(out, cands[i].doc)
	}
	return out
}

// f32ToF64 向量元素升精度 (复用 memory 模块的 cosineSimilarity)
func f32ToF64(v []float32) []float64 {
	out := make([]float64, len(v))
	for i := range v {
		out[i] = float64(v[i])
	}
	return out
}

// categoryNames 批量解析分类名 (失败降级空串, 不阻断检索)
func (r *KBRetriever) categoryNames(ctx context.Context, docs []model.KBDocument) map[string]string {
	seen := make(map[string]bool, len(docs))
	ids := make([]string, 0, len(docs))
	for i := range docs {
		if !seen[docs[i].CategoryID] {
			seen[docs[i].CategoryID] = true
			ids = append(ids, docs[i].CategoryID)
		}
	}
	names := make(map[string]string, len(ids))
	for _, id := range ids {
		if cat, err := r.cats.Get(ctx, id); err == nil {
			names[id] = cat.Name
		}
	}
	return names
}

// bumpAccess 命中条目访问统计回写 (异步 fire-and-forget, 同 M10)
func (r *KBRetriever) bumpAccess(hits []KBSearchHit) {
	now := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for i := range hits {
			if err := r.docs.BumpAccess(ctx, hits[i].ID, now); err != nil {
				log.Printf("kb: bump access failed doc=%s: %v", hits[i].ID, err)
				return
			}
		}
	}()
}

// kbEmptyQueryScore 空查询得分 (审核续答轮: 无关键词, 按时间衰减 + 使用频率取近期条目; 同 M10 空查询语义)
func kbEmptyQueryScore(doc *model.KBDocument, now time.Time) float64 {
	ageDays := now.Sub(doc.UpdatedAt).Hours() / 24
	recency := math.Exp(-ageDays / memDecayDays)
	usage := math.Log1p(float64(doc.AccessCount)) / math.Log1p(memUsageCap)
	return memWeightRecency*recency + memWeightUsage*usage
}

// ---------- 关键词候选集 TTL 缓存 (写入失效) ----------

// kbCandidateKey 缓存键: 分类集排序后拼接 (与顺序无关)
func kbCandidateKey(categoryIDs []string) string {
	ids := append([]string(nil), categoryIDs...)
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

type kbCandidateCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]kbCandidateEntry
}

type kbCandidateEntry struct {
	docs []model.KBDocument
	exp  time.Time
}

func newKBCandidateCache(ttl time.Duration) *kbCandidateCache {
	return &kbCandidateCache{ttl: ttl, entries: make(map[string]kbCandidateEntry)}
}

func (c *kbCandidateCache) get(key string) ([]model.KBDocument, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.exp) {
		if ok {
			delete(c.entries, key)
		}
		return nil, false
	}
	return e.docs, true
}

func (c *kbCandidateCache) set(key string, docs []model.KBDocument) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) > 64 { // 防御: 分类集爆炸时强制清空重建
		c.entries = make(map[string]kbCandidateEntry)
	}
	c.entries[key] = kbCandidateEntry{docs: docs, exp: time.Now().Add(c.ttl)}
}

func (c *kbCandidateCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]kbCandidateEntry)
}

// cachedCandidates 关键词候选集 (active, 分类范围, 上限 KB_MAX_CANDIDATES, updated_at 降序; TTL 缓存)
func (r *KBRetriever) cachedCandidates(ctx context.Context, categoryIDs []string) ([]model.KBDocument, error) {
	key := kbCandidateKey(categoryIDs)
	if docs, ok := r.cache.get(key); ok {
		return docs, nil
	}
	limit := r.cfg.MaxCandidates
	docs, err := r.docs.SearchCandidates(ctx, categoryIDs, model.KBStatusActive, limit)
	if err != nil {
		return nil, err
	}
	r.cache.set(key, docs)
	return docs, nil
}
