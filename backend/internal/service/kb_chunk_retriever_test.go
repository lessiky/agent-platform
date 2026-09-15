package service

// kb_chunk_retriever_test.go — M11.5 W2 块级向量路径单测 (事项 4):
// 块召回 + 按条目聚合 (最佳块) / 召回故障降级关键词 / 存量分块迁移幂等 / 块级异步向量化 / 块级回填批次

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/internal/config"
	"agent-platform/internal/model"
)

// ---------- 假块仓储 (内存实现) ----------

type fakeKBChunkRepo struct {
	mu       sync.Mutex
	byDoc    map[string][]model.KBChunk
	next     int
	searchFn func(categoryIDs []string, vec *model.Vector, limit int) ([]model.KBChunk, error)
	missing  []model.KBDocument // MissingDocs 队列 (Rebuild 后移除)
}

func newFakeKBChunkRepo() *fakeKBChunkRepo {
	return &fakeKBChunkRepo{byDoc: make(map[string][]model.KBChunk)}
}

func (f *fakeKBChunkRepo) seedUnembedded(docID string, contents ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	chunks := make([]model.KBChunk, 0, len(contents))
	for i, c := range contents {
		f.next++
		chunks = append(chunks, model.KBChunk{
			ID: fmt.Sprintf("chunk-%d", f.next), DocID: docID, ChunkIndex: i, Content: c,
			UpdatedAt: time.Now(),
		})
	}
	f.byDoc[docID] = chunks
}

func (f *fakeKBChunkRepo) seedEmbedded(docID string, index int, content string, vec []float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.byDoc[docID] = append(f.byDoc[docID], model.KBChunk{
		ID: fmt.Sprintf("chunk-%d", f.next), DocID: docID, ChunkIndex: index, Content: content,
		Embedding: &model.Vector{V: vec}, UpdatedAt: time.Now(),
	})
}

func (f *fakeKBChunkRepo) Rebuild(_ context.Context, docID string, contents []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	chunks := make([]model.KBChunk, 0, len(contents))
	for i, c := range contents {
		f.next++
		chunks = append(chunks, model.KBChunk{
			ID: fmt.Sprintf("chunk-%d", f.next), DocID: docID, ChunkIndex: i, Content: c,
			UpdatedAt: time.Now(),
		})
	}
	f.byDoc[docID] = chunks
	f.removeMissing(docID)
	return nil
}

func (f *fakeKBChunkRepo) removeMissing(docID string) {
	out := f.missing[:0]
	for _, d := range f.missing {
		if d.ID != docID {
			out = append(out, d)
		}
	}
	f.missing = out
}

func (f *fakeKBChunkRepo) DeleteByDoc(_ context.Context, docID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byDoc, docID)
	return nil
}

func (f *fakeKBChunkRepo) ListByDoc(_ context.Context, docID string) ([]model.KBChunk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	chunks := f.byDoc[docID]
	out := make([]model.KBChunk, len(chunks))
	copy(out, chunks)
	return out, nil
}

func (f *fakeKBChunkRepo) CountByDoc(_ context.Context, docID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.byDoc[docID])), nil
}

func (f *fakeKBChunkRepo) CountByDocs(_ context.Context, docIDs []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(docIDs))
	for _, id := range docIDs {
		out[id] = int64(len(f.byDoc[id]))
	}
	return out, nil
}

func (f *fakeKBChunkRepo) ClearEmbeddings(_ context.Context, docID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.byDoc[docID] {
		f.byDoc[docID][i].Embedding = nil
	}
	return nil
}

func (f *fakeKBChunkRepo) SearchByVector(_ context.Context, categoryIDs []string, vec *model.Vector, limit int) ([]model.KBChunk, error) {
	if f.searchFn != nil {
		return f.searchFn(categoryIDs, vec, limit)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.KBChunk
	for _, docID := range sortedDocIDs(f.byDoc) {
		for _, c := range f.byDoc[docID] {
			if !c.Embedding.IsNull() {
				out = append(out, c)
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeKBChunkRepo) ListUnembedded(_ context.Context, limit int) ([]model.KBChunk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.KBChunk
	for _, docID := range sortedDocIDs(f.byDoc) {
		for _, c := range f.byDoc[docID] {
			if c.Embedding.IsNull() {
				out = append(out, c)
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func sortedDocIDs(m map[string][]model.KBChunk) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (f *fakeKBChunkRepo) CountUnembedded(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, chunks := range f.byDoc {
		for _, c := range chunks {
			if c.Embedding.IsNull() {
				n++
			}
		}
	}
	return n, nil
}

func (f *fakeKBChunkRepo) SetEmbeddingIfNull(_ context.Context, id string, vec *model.Vector) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for docID := range f.byDoc {
		for i := range f.byDoc[docID] {
			if f.byDoc[docID][i].ID == id && f.byDoc[docID][i].Embedding.IsNull() {
				f.byDoc[docID][i].Embedding = vec
				return nil
			}
		}
	}
	return fmt.Errorf("chunk not found: %s", id)
}

func (f *fakeKBChunkRepo) MissingDocs(_ context.Context, limit, _ int) ([]model.KBDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.missing) > limit {
		return f.missing[:limit], nil
	}
	return f.missing, nil
}

// ---------- 块召回 + 聚合 ----------

func TestChunkRecallAggregatesBestChunkPerDoc(t *testing.T) {
	docs := newFakeKBDocRepo()
	cats := newFakeKBCatRepo()
	bindings := newFakeKBBindingRepo()
	chunks := newFakeKBChunkRepo()

	seedKBDoc(docs, "doc-a", "cat-1", "长文档 A", "整条正文内容很长, 但块召回只看块", time.Now(), 0)
	seedKBDoc(docs, "doc-b", "cat-1", "文档 B", "文档 B 的正文内容", time.Now(), 0)
	chunks.seedEmbedded("doc-a", 0, "头部块: 背景介绍内容", []float32{0.2, 0.98, 0})
	chunks.seedEmbedded("doc-a", 1, "关键块: 慢查询排查步骤", []float32{1, 0, 0})
	chunks.seedEmbedded("doc-b", 0, "文档 B 块内容", []float32{0.6, 0.8, 0})

	emb := &fakeKBEmbedder{enabled: true, vec: []float32{1, 0, 0}}
	r := NewKBRetriever(docs, cats, bindings, chunks, emb, nil,
		config.KBConfig{TopK: 3, RecallSize: 20, MaxCandidates: 500}, 0)

	hits, err := r.RetrieveByCategories(context.Background(), nil, "慢查询 排查", KBSearchOptions{TopK: 3})
	if err != nil {
		t.Fatalf("RetrieveByCategories: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 aggregated hits, got %d", len(hits))
	}
	// 聚合: doc-a 取最佳块 (余弦 1.0 的"关键块"), 排序 top-1
	if hits[0].ID != "doc-a" {
		t.Fatalf("top-1 = %s, want doc-a", hits[0].ID)
	}
	if hits[0].Score <= 0.99 {
		t.Fatalf("doc-a score = %v, want ~1.0 (best chunk)", hits[0].Score)
	}
	if hits[0].MatchedChunk == nil || *hits[0].MatchedChunk != "关键块: 慢查询排查步骤" {
		t.Fatalf("doc-a matched_chunk = %v, want 关键块", hits[0].MatchedChunk)
	}
	if !strings.Contains(hits[0].Excerpt, "慢查询排查步骤") {
		t.Fatalf("doc-a excerpt = %q, want 来自最佳命中块 (非头部)", hits[0].Excerpt)
	}
	if hits[1].ID != "doc-b" || hits[1].Score <= 0.59 {
		t.Fatalf("second hit = %s score %v, want doc-b ~0.6", hits[1].ID, hits[1].Score)
	}
}

func TestChunkRecallTopKLimit(t *testing.T) {
	docs := newFakeKBDocRepo()
	chunks := newFakeKBChunkRepo()
	for i, id := range []string{"doc-1", "doc-2", "doc-3"} {
		seedKBDoc(docs, id, "cat-1", "标题"+string(rune('A'+i)), "正文内容"+string(rune('a'+i)), time.Now(), 0)
		chunks.seedEmbedded(id, 0, "块内容"+string(rune('a'+i)), []float32{1, 0, 0})
	}
	emb := &fakeKBEmbedder{enabled: true, vec: []float32{1, 0, 0}}
	r := NewKBRetriever(docs, newFakeKBCatRepo(), newFakeKBBindingRepo(), chunks, emb, nil,
		config.KBConfig{TopK: 3, RecallSize: 20, MaxCandidates: 500}, 0)

	hits, err := r.RetrieveByCategories(context.Background(), nil, "查询", KBSearchOptions{TopK: 2})
	if err != nil {
		t.Fatalf("RetrieveByCategories: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("top-k=2 want 2 hits, got %d", len(hits))
	}
}

func TestChunkRecallErrorFallsBackToKeyword(t *testing.T) {
	docs := newFakeKBDocRepo()
	chunks := newFakeKBChunkRepo()
	chunks.searchFn = func(_ []string, _ *model.Vector, _ int) ([]model.KBChunk, error) {
		return nil, fmt.Errorf("db down")
	}
	// 关键词路径候选: 正文含查询词
	seedKBDoc(docs, "doc-kw", "cat-1", "慢查询排查手册", "慢查询 排查 的 方法 与 步骤 说明", time.Now(), 0)

	emb := &fakeKBEmbedder{enabled: true, vec: []float32{1, 0, 0}}
	r := NewKBRetriever(docs, newFakeKBCatRepo(), newFakeKBBindingRepo(), chunks, emb, nil,
		config.KBConfig{TopK: 3, RecallSize: 20, MaxCandidates: 500}, 0)

	hits, err := r.RetrieveByCategories(context.Background(), nil, "慢查询 排查", KBSearchOptions{TopK: 3})
	if err != nil {
		t.Fatalf("RetrieveByCategories: %v", err)
	}
	if len(hits) == 0 || hits[0].ID != "doc-kw" {
		t.Fatalf("chunk recall 故障应降级关键词召回, hits = %+v", hits)
	}
	if hits[0].MatchedChunk != nil {
		t.Fatalf("关键词路径不应携带 matched_chunk")
	}
}

// ---------- 存量分块迁移 ----------

func TestMigrateExistingKBChunksIdempotent(t *testing.T) {
	chunks := newFakeKBChunkRepo()
	chunks.missing = []model.KBDocument{
		{ID: "doc-1", Title: "A", Content: "正文一"},
		{ID: "doc-2", Title: "B", Content: "正文二"},
	}
	n, err := MigrateExistingKBChunks(context.Background(), chunks, KBChunkOptions{Size: 500, Overlap: 50, Threshold: 1000})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 2 {
		t.Fatalf("migrated = %d, want 2", n)
	}
	if got, _ := chunks.CountByDoc(context.Background(), "doc-1"); got != 1 {
		t.Fatalf("doc-1 chunks = %d, want 1 (短条目 1 块)", got)
	}
	// 幂等: 重跑无新增 (已有块的条目被 MissingDocs 跳过)
	n2, err := MigrateExistingKBChunks(context.Background(), chunks, KBChunkOptions{Size: 500, Overlap: 50, Threshold: 1000})
	if err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("re-migrated = %d, want 0 (幂等)", n2)
	}
}

func TestMigrateExistingKBChunksStallOnEmptyContent(t *testing.T) {
	chunks := newFakeKBChunkRepo()
	chunks.missing = []model.KBDocument{{ID: "doc-empty", Title: "空", Content: "   "}}
	n, err := MigrateExistingKBChunks(context.Background(), chunks, KBChunkOptions{})
	if err != nil {
		t.Fatalf("migrate: %v (空正文应防御性退出, 不进入死循环)", err)
	}
	if n != 0 {
		t.Fatalf("migrated = %d, want 0", n)
	}
}

// ---------- 块级异步向量化 + 回填批次 ----------

type fakeChunkBatchEmbedder struct {
	mu       sync.Mutex
	vec      []float32
	err      error
	batches  [][]string
	batchesN int
}

func (f *fakeChunkBatchEmbedder) Enabled() bool { return true }

func (f *fakeChunkBatchEmbedder) EmbedOne(_ context.Context, _ string) ([]float32, error) {
	return f.vec, nil
}

func (f *fakeChunkBatchEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.batchesN++
	f.batches = append(f.batches, texts)
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = f.vec
	}
	return out, nil
}

func TestVectorizeAsyncChunkMode(t *testing.T) {
	docs := newFakeKBDocRepo()
	chunks := newFakeKBChunkRepo()
	chunks.seedUnembedded("doc-1", "块一内容", "块二内容")
	emb := &fakeChunkBatchEmbedder{vec: []float32{1, 0, 0}}
	w := NewKBVectorWriter(docs, chunks, emb, 32)

	w.VectorizeAsync(&model.KBDocument{ID: "doc-1", Title: "标题"})

	deadline := time.Now().Add(5 * time.Second)
	for {
		left, _ := chunks.CountUnembedded(context.Background())
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("块向量化未完成, 剩余 %d", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
	emb.mu.Lock()
	defer emb.mu.Unlock()
	if emb.batchesN != 1 || len(emb.batches[0]) != 2 {
		t.Fatalf("EmbedBatch 调用 = %d 次 / %d 条, want 1 次 2 条", emb.batchesN, len(emb.batches))
	}
	if emb.batches[0][0] != "块一内容" || emb.batches[0][1] != "块二内容" {
		t.Fatalf("块向量化文本 = %v, want 块正文本身", emb.batches[0])
	}
}

func TestBackfillChunkBatch(t *testing.T) {
	docs := newFakeKBDocRepo()
	chunks := newFakeKBChunkRepo()
	chunks.seedUnembedded("doc-1", "块一内容", "块二内容")
	chunks.seedUnembedded("doc-2", "块三内容")
	emb := &fakeChunkBatchEmbedder{vec: []float32{1, 0, 0}}
	w := NewKBVectorWriter(docs, chunks, emb, 32)

	total, err := w.CountUnembedded(context.Background())
	if err != nil || total != 3 {
		t.Fatalf("CountUnembedded = %d err=%v, want 3", total, err)
	}
	processed, embedded, failed, more, fatal := w.BackfillBatch(context.Background())
	if fatal != nil {
		t.Fatalf("BackfillBatch fatal: %v", fatal)
	}
	if processed != 3 || embedded != 3 || failed != 0 || more {
		t.Fatalf("processed=%d embedded=%d failed=%d more=%v, want 3/3/0/false", processed, embedded, failed, more)
	}
	if left, _ := chunks.CountUnembedded(context.Background()); left != 0 {
		t.Fatalf("回填后未向量化块 = %d, want 0", left)
	}
	// 空批结束
	if p, _, _, more, _ := w.BackfillBatch(context.Background()); p != 0 || more {
		t.Fatalf("空批 = processed %d more %v, want 0/false", p, more)
	}
}

func TestBackfillChunkBatchFatalDimMismatch(t *testing.T) {
	docs := newFakeKBDocRepo()
	chunks := newFakeKBChunkRepo()
	chunks.seedUnembedded("doc-1", "块一内容")
	emb := &fakeChunkBatchEmbedder{err: fmt.Errorf("embedding 模型输出维度 1 与知识库向量列维度 1024 不匹配")}
	w := NewKBVectorWriter(docs, chunks, emb, 32)

	_, _, _, _, fatal := w.BackfillBatch(context.Background())
	if fatal == nil {
		t.Fatalf("维度不匹配应返回 fatal (任务终止明确报错)")
	}
}
