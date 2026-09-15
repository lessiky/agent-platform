package service

// kb_embedder.go — M11 知识库向量/重排组件 (计划 §2 向量/重排)
//
// 两个独立组件, 均仅依赖 ModelTemplateService + TemplateSource:
//   - KBEmbedder: 条目/查询向量化 (POST /embeddings); 模型输出维度须与 KB_VECTOR_DIM
//     一致, 不匹配明确报错 (平台保存 embedding 模型时探测校验, 检索路径失败即降级);
//   - KBReranker: 候选重排 (POST /rerank, vLLM / Xinference 兼容); 超时/失败由调用方
//     按召回序降级。
//
// 模型名称运行时来源: 平台设置页 (kb_embed_model / kb_rerank_model) 优先,
// KB_EMBED_MODEL / KB_RERANK_MODEL 环境变量兜底 (MutableTemplateSource, 免重启切换);
// 空 = 对应能力整体不生效 (由 KBRetriever 降级阶梯处理)。
//
// 写入路径: 条目创建/更新成功后触发 VectorizeAsync (detached goroutine, best-effort),
// 失败仅告警留待手动回填 (BackfillEmbeddings); 任何故障不影响条目落库主流程。

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"agent-platform/internal/model"

	"agent-platform/internal/repository"
)

// KBEmbedder 知识库向量化 (M11)
type KBEmbedder interface {
	// Enabled 向量召回是否可用 (仅当配置了 embedding 模型模板时为 true)
	Enabled() bool
	// EmbedOne 计算单条文本向量; 返回 []float32 (列类型 vector(1024)); 失败/维度不匹配返回 error
	EmbedOne(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch 批量计算向量 (回填用; 与 inputs 顺序一致)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// KBReranker 知识库重排 (M11)
type KBReranker interface {
	// Enabled 重排是否可用 (仅当配置了 rerank 模型模板时为 true)
	Enabled() bool
	// Rerank 对文档列表按与 query 相关度打分, 返回与 docs 顺序一致的分数; 失败返回 error (调用方降级)
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

type kbEmbedder struct {
	modelSvc ModelTemplateService
	nameSrc  TemplateSource // 运行时来源 (平台设置优先, KB_EMBED_MODEL 兜底)
	dim      int            // 向量列维度 (KB_VECTOR_DIM, 列级固定)
	timeout  time.Duration  // 单次向量计算超时上限 (回填/写入路径; 检索路径由调用方 ctx 限制)
}

// NewKBEmbedder 创建知识库向量组件; dim 取自 KB_VECTOR_DIM (默认 1024), timeout 默认 10s
func NewKBEmbedder(modelSvc ModelTemplateService, nameSrc TemplateSource, dim int, timeout time.Duration) KBEmbedder {
	if dim <= 0 {
		dim = 1024
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &kbEmbedder{modelSvc: modelSvc, nameSrc: nameSrc, dim: dim, timeout: timeout}
}

func (e *kbEmbedder) Enabled() bool {
	return e.modelSvc != nil && e.nameSrc != nil && e.nameSrc.Current() != ""
}

// validateDim 维度校验 (A11): 模型输出维度须与列维度一致, 不匹配明确报错
func (e *kbEmbedder) validateDim(vec []float64) error {
	if len(vec) != e.dim {
		return fmt.Errorf("embedding 模型输出维度 %d 与知识库向量列维度 %d 不匹配 (模型名: %s), 请更换 embedding 模型或调整 KB_VECTOR_DIM 后重建向量列", len(vec), e.dim, e.nameSrc.Current())
	}
	return nil
}

func (e *kbEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty text")
	}
	if !e.Enabled() {
		return nil, fmt.Errorf("embedding model template not configured (平台设置 kb_embed_model / KB_EMBED_MODEL)")
	}
	bctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	vecs, err := e.modelSvc.EmbedForKB(bctx, e.nameSrc.Current(), []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("unexpected embedding response (vectors=%d)", len(vecs))
	}
	if err := e.validateDim(vecs[0]); err != nil {
		return nil, err
	}
	out := make([]float32, len(vecs[0]))
	for i, v := range vecs[0] {
		out[i] = float32(v)
	}
	return out, nil
}

// kbEmbedText 条目向量化文本 (标题 + 正文, 截断至 8000 字符控制 token 成本)
const kbEmbedMaxRunes = 8000

func kbEmbedText(doc *model.KBDocument) string {
	runes := []rune(doc.Title + "\n" + doc.Content)
	if len(runes) > kbEmbedMaxRunes {
		runes = runes[:kbEmbedMaxRunes]
	}
	return string(runes)
}

func (e *kbEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if !e.Enabled() {
		return nil, fmt.Errorf("embedding model template not configured (平台设置 kb_embed_model / KB_EMBED_MODEL)")
	}
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	bctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	vecs, err := e.modelSvc.EmbedForKB(bctx, e.nameSrc.Current(), texts)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("unexpected embedding response (vectors=%d, inputs=%d)", len(vecs), len(texts))
	}
	out := make([][]float32, len(vecs))
	for i, v := range vecs {
		if err := e.validateDim(v); err != nil {
			return nil, err
		}
		f32 := make([]float32, len(v))
		for j, x := range v {
			f32[j] = float32(x)
		}
		out[i] = f32
	}
	return out, nil
}

type kbReranker struct {
	modelSvc ModelTemplateService
	nameSrc  TemplateSource // 运行时来源 (平台设置优先, KB_RERANK_MODEL 兜底)
}

// NewKBReranker 创建知识库重排组件 (单次调用超时由调用方 ctx 控制: 注入路径 KB_RERANK_TIMEOUT)
func NewKBReranker(modelSvc ModelTemplateService, nameSrc TemplateSource) KBReranker {
	return &kbReranker{modelSvc: modelSvc, nameSrc: nameSrc}
}

func (r *kbReranker) Enabled() bool {
	return r.modelSvc != nil && r.nameSrc != nil && r.nameSrc.Current() != ""
}

func (r *kbReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	if !r.Enabled() {
		return nil, fmt.Errorf("rerank model template not configured (平台设置 kb_rerank_model / KB_RERANK_MODEL)")
	}
	if len(docs) == 0 {
		return nil, nil
	}
	return r.modelSvc.RerankForKB(ctx, r.nameSrc.Current(), query, docs)
}

// ---------- 写入路径异步向量化 + 回填 ----------
//
// M11.5 事项 4: chunks 非 nil = 块级向量化 (KB_CHUNK_ENABLED): 写路径按块异步向量化,
// 回填任务批处理未向量化块; chunks nil = 整条向量路径全量回退 (M11 行为不变).
// 两种模式向量写均幂等 (仅 NULL 时写入), 与写路径并发无竞争.

// KBVectorWriter 条目向量化写入 (异步 fire-and-forget + 后台回填任务; M11.5: 向量写幂等, 仅 NULL 时写入)
type KBVectorWriter struct {
	docs repository.KBDocumentRepository
	chunks repository.KBChunkRepository // nil = 整条向量路径 (回退/存量)
	embedder KBEmbedder
	batch    int
}

// NewKBVectorWriter 创建向量写入器 (batch 默认 32; chunks 非 nil = 块级向量化路径)
func NewKBVectorWriter(docs repository.KBDocumentRepository, chunks repository.KBChunkRepository, embedder KBEmbedder, batch int) *KBVectorWriter {
	if batch <= 0 {
		batch = 32
	}
	return &KBVectorWriter{docs: docs, chunks: chunks, embedder: embedder, batch: batch}
}

// Enabled 向量化能力是否可用 (embedding 模型模板已配置)
func (w *KBVectorWriter) Enabled() bool {
	return w != nil && w.embedder != nil && w.embedder.Enabled()
}

// VectorizeAsync 条目写入后异步计算向量回写 (detached, best-effort, 失败仅告警; 不启用向量时跳过)
// M11.5: 块模式 = 按块向量化 (块已同事务落库); 整条模式 = 整条单向量 (M11 行为)
func (w *KBVectorWriter) VectorizeAsync(doc *model.KBDocument) {
	if w == nil || doc == nil || w.embedder == nil || !w.embedder.Enabled() {
		return
	}
	if w.chunks != nil {
		go w.vectorizeChunks(doc)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vec, err := w.embedder.EmbedOne(ctx, kbEmbedText(doc))
		if err != nil {
			log.Printf("kb: async embed failed doc=%s: %v (可经平台设置页向量回填重试)", doc.ID, err)
			return
		}
		if err := w.docs.SetEmbeddingIfNull(ctx, doc.ID, &model.Vector{V: vec}); err != nil {
			log.Printf("kb: async embed persist failed doc=%s: %v", doc.ID, err)
			return
		}
		log.Printf("kb: async embed ok doc=%s dim=%d", doc.ID, len(vec))
	}()
}

// vectorizeChunks 块级异步向量化 (按 w.batch 分批, 块文本 = 标题 + 块正文; 失败仅告警留待回填)
func (w *KBVectorWriter) vectorizeChunks(doc *model.KBDocument) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	chunks, err := w.chunks.ListByDoc(ctx, doc.ID)
	if err != nil {
		log.Printf("kb: async chunk load failed doc=%s: %v (可经平台设置页向量回填重试)", doc.ID, err)
		return
	}
	for start := 0; start < len(chunks); start += w.batch {
		end := start + w.batch
		if end > len(chunks) {
			end = len(chunks)
		}
		group := chunks[start:end]
		texts := make([]string, len(group))
		for i := range group {
			texts[i] = KBChunkEmbedText(group[i].Content)
		}
		vecs, err := w.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			log.Printf("kb: async chunk embed failed doc=%s: %v (可经平台设置页向量回填重试)", doc.ID, err)
			return
		}
		for i := range group {
			if err := w.chunks.SetEmbeddingIfNull(ctx, group[i].ID, &model.Vector{V: vecs[i]}); err != nil {
				log.Printf("kb: async chunk persist failed chunk=%s: %v", group[i].ID, err)
			}
		}
	}
	log.Printf("kb: async chunk embed ok doc=%s chunks=%d", doc.ID, len(chunks))
}

// BackfillResult 向量回填结果
type BackfillResult struct {
	Total    int `json:"total"`     // 本批处理 (含失败) 的条目数
	Embedded int `json:"embedded"`  // 成功向量化的条目数
	Failed   int `json:"failed"`    // 失败的条目数
	Leftover int `json:"leftover"`  // 回填后仍未向量化的 active 条目数
}

// BackfillBatch 处理一个回填批次 (M11.5 任务服务与同步回填共用):
// 块模式: 取未向量化块 (上限 w.batch) -> 批量计算向量 -> 幂等回写;
// 整条模式: 取未向量化条目 (上限 w.batch) -> 批量计算向量 -> 幂等回写 (仅 embedding IS NULL 时写入,
// 与写入路径 VectorizeAsync 并发无竞争); 返回本批处理数 (embedded+failed) / 成功数 / 失败数 /
// 是否可能还有剩余 (false = 本批为空); fatal = 系统性错误 (维度不匹配/未配置), 调用方应终止
func (w *KBVectorWriter) BackfillBatch(ctx context.Context) (processed, embedded, failed int, more bool, fatal error) {
	if w == nil || w.embedder == nil {
		return 0, 0, 0, false, fmt.Errorf("vector writer not initialized")
	}
	if !w.embedder.Enabled() {
		return 0, 0, 0, false, fmt.Errorf("embedding model template not configured (平台设置 kb_embed_model / KB_EMBED_MODEL)")
	}
	if w.chunks != nil {
		return w.backfillChunkBatch(ctx)
	}
	batch, err := w.docs.ListUnembedded(ctx, w.batch)
	if err != nil {
		return 0, 0, 0, true, err
	}
	if len(batch) == 0 {
		return 0, 0, 0, false, nil
	}
	processed = len(batch)
	texts := make([]string, len(batch))
	for i := range batch {
		texts[i] = kbEmbedText(&batch[i])
	}
	vecs, err := w.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		// 维度不匹配等系统性错误: 终止回填并明确报错 (避免空转消耗配额)
		if strings.Contains(err.Error(), "维度") || strings.Contains(err.Error(), "not configured") {
			return 0, 0, len(batch), true, err
		}
		log.Printf("kb: backfill embed batch failed size=%d: %v", len(batch), err)
		return len(batch), 0, len(batch), false, nil
	}
	for i := range batch {
		if err := w.docs.SetEmbeddingIfNull(ctx, batch[i].ID, &model.Vector{V: vecs[i]}); err != nil {
			failed++
			log.Printf("kb: backfill persist failed doc=%s: %v", batch[i].ID, err)
			continue
		}
		embedded++
	}
	// 满批时假设还有剩余 (本批可能已被并发写入消费, 下轮空批结束并复核)
	more = len(batch) >= w.batch
	return processed, embedded, failed, more, nil
}

// backfillChunkBatch 块级回填批次: 未向量化块 (上限 w.batch) -> 块文本 (标题 + 块正文) 批量向量 -> 幂等回写
func (w *KBVectorWriter) backfillChunkBatch(ctx context.Context) (processed, embedded, failed int, more bool, fatal error) {
	batch, err := w.chunks.ListUnembedded(ctx, w.batch)
	if err != nil {
		return 0, 0, 0, true, err
	}
	if len(batch) == 0 {
		return 0, 0, 0, false, nil
	}
	processed = len(batch)
	texts := make([]string, len(batch))
	for i := range batch {
		texts[i] = KBChunkEmbedText(batch[i].Content)
	}
	vecs, err := w.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		if strings.Contains(err.Error(), "维度") || strings.Contains(err.Error(), "not configured") {
			return 0, 0, len(batch), true, err
		}
		log.Printf("kb: backfill chunk embed batch failed size=%d: %v", len(batch), err)
		return len(batch), 0, len(batch), false, nil
	}
	for i := range batch {
		if err := w.chunks.SetEmbeddingIfNull(ctx, batch[i].ID, &model.Vector{V: vecs[i]}); err != nil {
			failed++
			log.Printf("kb: backfill chunk persist failed chunk=%s: %v", batch[i].ID, err)
			continue
		}
		embedded++
	}
	more = len(batch) >= w.batch
	return processed, embedded, failed, more, nil
}

// CountUnembedded 未向量化目标数 (回填任务 total 统计; 块模式 = 块数, 整条模式 = 条目数)
func (w *KBVectorWriter) CountUnembedded(ctx context.Context) (int64, error) {
	if w == nil || w.docs == nil {
		return 0, fmt.Errorf("vector writer not initialized")
	}
	if w.chunks != nil {
		return w.chunks.CountUnembedded(ctx)
	}
	return w.docs.CountUnembedded(ctx)
}

// Backfill 同步回填: 分批为未向量化的 active 条目计算并回写向量;
// 单条失败仅计数继续 (维度不匹配等系统性错误提前终止, 明确返回错误)
func (w *KBVectorWriter) Backfill(ctx context.Context) (*BackfillResult, error) {
	res := &BackfillResult{}
	for {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		processed, embedded, failed, more, fatal := w.BackfillBatch(ctx)
		if fatal != nil {
			return res, fatal
		}
		res.Total += processed
		res.Embedded += embedded
		res.Failed += failed
		if !more {
			break
		}
	}
	if left, err := w.CountUnembedded(ctx); err == nil {
		res.Leftover = int(left)
	}
	return res, nil
}
