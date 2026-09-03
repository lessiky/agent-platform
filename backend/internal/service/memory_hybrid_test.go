package service

// memory_hybrid_test.go — M10.3 语义检索单测: 余弦/向量解析/融合分/混合排序/降级回退
// 全部为纯函数与假依赖, 不依赖 DB。

import (
	"context"
	"errors"
	"testing"
	"time"

	"agent-platform/internal/model"
)

// ---------- 纯函数 ----------

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"identical", []float64{1, 2, 3}, []float64{1, 2, 3}, 1},
		{"opposite_clamped_zero", []float64{1, 0}, []float64{-1, 0}, 0},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}, 0},
		{"dim_mismatch", []float64{1, 2}, []float64{1, 2, 3}, 0},
		{"zero_vector", []float64{0, 0}, []float64{1, 2}, 0},
		{"empty", []float64{}, []float64{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cosineSimilarity(c.a, c.b); mathAbs(got-c.want) > 1e-9 {
				t.Fatalf("cosineSimilarity = %v, want %v", got, c.want)
			}
		})
	}
}

// mathAbs 绝对值 (避免额外 import)
func mathAbs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestParseMemoryVector(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want []float64
	}{
		{"nil", nil, nil},
		{"empty", []byte{}, nil},
		{"invalid_json", []byte("{oops"), nil},
		{"json_object", []byte("{\"a\":1}"), nil},
		{"empty_array", []byte("[]"), nil},
		{"valid", []byte("[0.1,0.2,-0.3]"), []float64{0.1, 0.2, -0.3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseMemoryVector(c.raw)
			if c.want == nil {
				if got != nil {
					t.Fatalf("parseMemoryVector = %v, want nil", got)
				}
				return
			}
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d", len(got), len(c.want))
			}
			for i := range got {
				if mathAbs(got[i]-c.want[i]) > 1e-12 {
					t.Fatalf("vec[%d] = %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestHybridScore(t *testing.T) {
	if got := hybridScore(1.0, 1.0); mathAbs(got-1.0) > 1e-12 {
		t.Fatalf("hybridScore(1,1) = %v, want 1.0", got)
	}
	if got := hybridScore(1.0, 0.0); mathAbs(got-0.6) > 1e-12 {
		t.Fatalf("hybridScore(1,0) = %v, want 0.6", got)
	}
	if got := hybridScore(0.0, 0.5); mathAbs(got-0.2) > 1e-12 {
		t.Fatalf("hybridScore(0,0.5) = %v, want 0.2", got)
	}
}

// ---------- 混合排序 ----------

// hybridTestMem 构造测试记忆 (daysAgo 控制新鲜度)
func hybridTestMem(id, content string, daysAgo float64, userID *string) *model.Memory {
	return &model.Memory{
		ID:          id,
		AgentID:     "agent-1",
		UserID:      userID,
		Kind:        model.MemoryKindFact,
		Content:     content,
		Source:      model.MemorySourceLLMExtracted,
		Status:      model.MemoryStatusActive,
		UpdatedAt:   time.Now().Add(-time.Duration(daysAgo*24) * time.Hour),
		AccessCount: 0,
	}
}

// TestRankHybrid_SemanticRescue 语义命中可救回关键词零命中的过时记忆 (M10.3 核心价值)
func TestRankHybrid_SemanticRescue(t *testing.T) {
	now := time.Now()
	// 120 天前的记忆 (超过 memStaleDays=90 天硬过滤线)
	m := hybridTestMem("m1", "用户喜欢简洁直接的回答风格", 120, nil)
	// 查询: 与记忆内容零 bigram 重叠 (kw=0)
	query := "请尽量简短一些"
	if got := keywordScore(query, m.Content); got != 0 {
		t.Fatalf("前置条件: keywordScore = %v, want 0", got)
	}
	// 向量: 查询 q=[1,0], 记忆 v=[0.9, sqrt(1-0.81)] -> cos=0.9
	qvec := []float64{1, 0}
	mvec := []float64{0.9, 0.4358898943540674}

	// 关键词模式: 零命中 + 过时 -> 排除
	if got := rankMemories([]model.Memory{*m}, query, now, 10); len(got) != 0 {
		t.Fatalf("rankMemories = %v, want empty (no kw hit + stale)", got)
	}
	// 混合模式 + 语义命中 (0.9 >= 噪声地板): 救回
	got := rankHybridMemories([]model.Memory{*m}, [][]float64{mvec}, query, qvec, now, 10)
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("rankHybrid = %+v, want [m1]", got)
	}
	// 混合模式但记忆无向量: 与关键词模式一致 (逐条降级)
	got = rankHybridMemories([]model.Memory{*m}, [][]float64{nil}, query, qvec, now, 10)
	if len(got) != 0 {
		t.Fatalf("rankHybrid(no vector) = %+v, want empty", got)
	}
}

// TestRankHybrid_NoiseFloor 低相似度噪声 (sem < 0.25) 视为未命中
func TestRankHybrid_NoiseFloor(t *testing.T) {
	now := time.Now()
	m := hybridTestMem("m1", "用户喜欢简洁直接的回答风格", 120, nil)
	query := "请尽量简短一些"
	qvec := []float64{1, 0}

	// sem = 0.2 (< 0.25 地板) + kw=0 + 过时 -> 排除
	weak := rankHybridMemories([]model.Memory{*m}, [][]float64{{0.2, 0.9797958971132712}}, query, qvec, now, 10)
	if len(weak) != 0 {
		t.Fatalf("sem=0.2: got %+v, want empty (below noise floor)", weak)
	}
	// sem = 0.3 (>= 地板) + kw=0 + 过时 -> 保留
	strong := rankHybridMemories([]model.Memory{*m}, [][]float64{{0.3, 0.9539392014169456}}, query, qvec, now, 10)
	if len(strong) != 1 {
		t.Fatalf("sem=0.3: got %+v, want [m1]", strong)
	}
}

// TestRankHybrid_FusionOrdering 融合分排序: 语义强命中 > 纯关键词强命中
func TestRankHybrid_FusionOrdering(t *testing.T) {
	now := time.Now()
	// B: 关键词全覆盖 (kw=1.0), 无语义 (sem=0)
	b := hybridTestMem("b", "数据库是 PostgreSQL", 0, nil)
	// C: 关键词零命中, 语义满命中 (sem=1.0)
	c := hybridTestMem("c", "存储用pgsql", 0, nil)
	query := "PostgreSQL 数据库"
	if keywordScore(query, b.Content) < 0.99 {
		t.Fatalf("前置条件: b keywordScore too low")
	}
	if keywordScore(query, c.Content) != 0 {
		t.Fatalf("前置条件: c keywordScore = want 0")
	}
	qvec := []float64{1, 0, 0}
	got := rankHybridMemories([]model.Memory{*b, *c}, [][]float64{{0, 0, 1}, {1, 0, 0}}, query, qvec, now, 10)
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "b" {
		t.Fatalf("order = %+v, want [c b]", got)
	}
}

// TestRankHybrid_TopN topN 截断
func TestRankHybrid_TopN(t *testing.T) {
	now := time.Now()
	mems := []model.Memory{
		*hybridTestMem("m1", "一条记忆甲", 0, nil),
		*hybridTestMem("m2", "一条记忆乙", 0, nil),
		*hybridTestMem("m3", "一条记忆丙", 0, nil),
	}
	qvec := []float64{1, 0}
	got := rankHybridMemories(mems, [][]float64{{1, 0}, {0.9, 0.4358898943540674}, {0.8, 0.6}}, "一条记忆", qvec, now, 2)
	if len(got) != 2 {
		t.Fatalf("topN=2: got %d items, want 2", len(got))
	}
}

// ---------- 检索器 (接口 + 降级) ----------

// fakeMemEmbedder 可注入向量/失败的假向量组件
type fakeMemEmbedder struct {
	vecs    map[string][]float64
	fail    bool
	enabled bool
}

func (f *fakeMemEmbedder) Enabled() bool { return f.enabled }
func (f *fakeMemEmbedder) EmbedOne(ctx context.Context, text string) ([]float64, error) {
	if f.fail {
		return nil, errors.New("embed boom")
	}
	if v, ok := f.vecs[text]; ok {
		return v, nil
	}
	return nil, errors.New("no vector for text")
}

func fixedLoader(items []model.Memory, vecs [][]float64) memActiveSetLoader {
	return func(ctx context.Context, agentID string) ([]model.Memory, [][]float64, error) {
		return items, vecs, nil
	}
}

func errLoader() memActiveSetLoader {
	return func(ctx context.Context, agentID string) ([]model.Memory, [][]float64, error) {
		return nil, nil, errors.New("db down")
	}
}

// TestKeywordRetriever_Basic 关键词检索器: 正常排序 / 加载失败返回 nil
func TestKeywordRetriever_Basic(t *testing.T) {
	mems := []model.Memory{
		*hybridTestMem("m1", "数据库是 PostgreSQL", 0, nil),
		*hybridTestMem("m2", "无关内容甲", 0, nil),
	}
	r := NewKeywordRetriever(fixedLoader(mems, nil), 10)
	got := r.Retrieve(context.Background(), "agent-1", nil, "PostgreSQL")
	if len(got) == 0 || got[0].ID != "m1" {
		t.Fatalf("keyword retrieve = %+v, want m1 first", got)
	}

	r = NewKeywordRetriever(errLoader(), 10)
	if got := r.Retrieve(context.Background(), "agent-1", nil, "PostgreSQL"); got != nil {
		t.Fatalf("retrieve(load err) = %+v, want nil", got)
	}
}

// TestHybridRetriever_UserFilter user 级记忆属主隔离 + 向量随过滤保持对齐
func TestHybridRetriever_UserFilter(t *testing.T) {
	userA := "user-a"
	mems := []model.Memory{
		*hybridTestMem("m1", "agent级记忆", 0, nil),
		*hybridTestMem("m2", "甲的记忆", 0, &userA),
		*hybridTestMem("m3", "乙的记忆", 0, ptrOrNil("user-b")),
	}
	vecs := [][]float64{{1, 0}, {1, 0}, {1, 0}}
	embedder := &fakeMemEmbedder{enabled: true, vecs: map[string][]float64{"x": {1, 0}}}
	r := NewHybridRetriever(fixedLoader(mems, vecs), embedder, time.Second, 10)
	got := r.Retrieve(context.Background(), "agent-1", &userA, "x")
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if !ids["m1"] || !ids["m2"] || ids["m3"] {
		t.Fatalf("retrieve(userA) = %+v, want m1+m2 only", got)
	}
}

// TestHybridRetriever_FallbackOnEmbedFailure 查询向量失败 -> 与纯关键词结果一致
func TestHybridRetriever_FallbackOnEmbedFailure(t *testing.T) {
	mems := []model.Memory{
		*hybridTestMem("m1", "数据库是 PostgreSQL", 0, nil),
		*hybridTestMem("m2", "无关内容甲", 0, nil),
	}
	query := "PostgreSQL 数据库"
	kw := NewKeywordRetriever(fixedLoader(mems, nil), 10).Retrieve(context.Background(), "agent-1", nil, query)

	hybrid := NewHybridRetriever(fixedLoader(mems, [][]float64{{1, 0}, {0, 1}}), &fakeMemEmbedder{fail: true}, time.Second, 10)
	got := hybrid.Retrieve(context.Background(), "agent-1", nil, query)
	if len(got) != len(kw) {
		t.Fatalf("fallback len = %d, keyword len = %d", len(got), len(kw))
	}
	for i := range got {
		if got[i].ID != kw[i].ID {
			t.Fatalf("fallback order = %+v, keyword = %+v", got, kw)
		}
	}
}

// TestHybridRetriever_EmbedderDisabled embedder 未启用 -> 等同关键词模式
func TestHybridRetriever_EmbedderDisabled(t *testing.T) {
	mems := []model.Memory{
		*hybridTestMem("m1", "数据库是 PostgreSQL", 0, nil),
	}
	query := "PostgreSQL 数据库"
	kw := NewKeywordRetriever(fixedLoader(mems, nil), 10).Retrieve(context.Background(), "agent-1", nil, query)
	hybrid := NewHybridRetriever(fixedLoader(mems, nil), &fakeMemEmbedder{enabled: false}, time.Second, 10)
	got := hybrid.Retrieve(context.Background(), "agent-1", nil, query)
	if len(got) != len(kw) {
		t.Fatalf("disabled embedder: got %d, keyword %d", len(got), len(kw))
	}
}

// TestNewMemoryService_RetrieverSwitch 两实现构造时均构建, 生效实现按 embedder 启用状态运行时判定 (平台设置页可免重启切换)
func TestNewMemoryService_RetrieverSwitch(t *testing.T) {
	repo := newFakeMemRepo()
	// 未配置 embedder: 仅关键词实现
	s := NewMemoryService(repo, &fakeAuditRepo{}, true, 10, 800, 500*time.Millisecond, time.Hour, nil).(*memoryService)
	if s.keywordRetriever == nil {
		t.Fatalf("keywordRetriever = nil, want built")
	}
	if s.hybridRetriever != nil {
		t.Fatalf("hybridRetriever = %T, want nil without embedder", s.hybridRetriever)
	}

	// 过时且关键词零命中的记忆: 只能被语义路径救回
	query := "请尽量简短一些"
	m := repo.add("agent-1", "", model.MemoryKindPreference, "用户喜欢简洁直接的回答风格")
	m.UpdatedAt = time.Now().Add(-120 * 24 * time.Hour)
	m.Embedding = []byte("[0.9,0.4358898943540674]")
	if keywordScore(query, m.Content) != 0 {
		t.Fatalf("前置条件: keywordScore = %v, want 0", keywordScore(query, m.Content))
	}

	embedder := &fakeMemEmbedder{vecs: map[string][]float64{query: {1, 0}}}
	s = NewMemoryService(repo, &fakeAuditRepo{}, true, 10, 800, 500*time.Millisecond, time.Hour, embedder).(*memoryService)
	if s.keywordRetriever == nil || s.hybridRetriever == nil {
		t.Fatalf("retrievers not built: keyword=%v hybrid=%v", s.keywordRetriever, s.hybridRetriever)
	}

	ctx := context.Background()
	// 停用: 关键词路径 -> 过时零命中排除
	embedder.enabled = false
	if got := s.retrieve(ctx, "agent-1", nil, query); len(got) != 0 {
		t.Fatalf("disabled: got %v, want empty (keyword excludes stale zero-hit)", got)
	}
	// 启用: 混合路径 -> 语义命中救回 (无需重启即时生效)
	embedder.enabled = true
	if got := s.retrieve(ctx, "agent-1", nil, query); len(got) != 1 || got[0].ID != m.ID {
		t.Fatalf("enabled: got %v, want the memory rescued by semantics", got)
	}
}
