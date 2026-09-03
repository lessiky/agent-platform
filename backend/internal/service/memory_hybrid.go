package service

// memory_hybrid.go — M10.3 语义检索: MemoryRetriever 接口 + 关键词/混合两实现
//
// KeywordRetriever: M10.1 原打分 (关键词 bigram 覆盖 + 时间衰减 + 使用频率 + 显式加权)。
// HybridRetriever:  增加 embedding 余弦相似项, 融合分 = 0.6*sem + 0.4*kw (设计文档 §8):
//   - 查询向量每次检索计算一次 (独立预算, 超时/失败自动退回纯关键词打分);
//   - 文档向量与活跃记忆集同缓存 (加载时解析一次, ≤500 条, 暴力余弦 <1ms);
//   - 文档无向量时其 sem 为 0 (逐条降级);
//   - 混合硬过滤: 两信号均未命中且过时 (>90 天) 不参与注入。
//
// 切换: NewMemoryService 按 embedder 是否启用选择实现 (走配置 MEMORY_EMBED_MODEL), 调用方零改动。

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"agent-platform/internal/model"
)

// MemoryRetriever 记忆检索接口 (M10.3 §8): 隔离 KeywordRetriever / HybridRetriever 两实现, 切换走配置
type MemoryRetriever interface {
	// Retrieve 返回 topN 候选记忆 (按融合分降序; 加载失败返回 nil, 调用方跳过注入)
	Retrieve(ctx context.Context, agentID string, userID *string, message string) []model.Memory
}

// memActiveSetLoader 加载 Agent 活跃记忆集与解析后的向量 (带 TTL 缓存, 写入失效)
type memActiveSetLoader func(ctx context.Context, agentID string) ([]model.Memory, [][]float64, error)

// filterMemoriesByUser 用户维度过滤: Agent 级记忆全员可见, user 级记忆仅属主可见
func filterMemoriesByUser(items []model.Memory, userID *string) []model.Memory {
	filtered := make([]model.Memory, 0, len(items))
	for i := range items {
		if items[i].UserID == nil || (userID != nil && *items[i].UserID == *userID) {
			filtered = append(filtered, items[i])
		}
	}
	return filtered
}

// ---------- 关键词检索 (M10.1 行为) ----------

type keywordRetriever struct {
	load memActiveSetLoader
	topN int
}

// NewKeywordRetriever 纯关键词检索器 (M10.1 行为)
func NewKeywordRetriever(load memActiveSetLoader, topN int) MemoryRetriever {
	return &keywordRetriever{load: load, topN: topN}
}

func (r *keywordRetriever) Retrieve(ctx context.Context, agentID string, userID *string, message string) []model.Memory {
	items, _, err := r.load(ctx, agentID)
	if err != nil {
		return nil
	}
	return rankMemories(filterMemoriesByUser(items, userID), message, time.Now(), r.topN)
}

// ---------- 混合检索 (M10.3) ----------

// 混合融合权重 (设计文档 §8: 融合分 0.6*sem + 0.4*kw)
const (
	hybridWeightSem     = 0.6
	hybridWeightKw      = 0.4
	hybridSemNoiseFloor = 0.25 // sem 低于该值视为无语义命中 (噪声地板)
)

type hybridRetriever struct {
	load        memActiveSetLoader
	embedder    MemoryEmbedder
	embedBudget time.Duration // 查询向量计算独立预算 (超时/失败退回关键词打分)
	topN        int
}

// NewHybridRetriever 混合检索器 (关键词 + 语义融合); embedBudget 默认 2s
func NewHybridRetriever(load memActiveSetLoader, embedder MemoryEmbedder, embedBudget time.Duration, topN int) MemoryRetriever {
	if embedBudget <= 0 {
		embedBudget = 2 * time.Second
	}
	return &hybridRetriever{load: load, embedder: embedder, embedBudget: embedBudget, topN: topN}
}

func (r *hybridRetriever) Retrieve(ctx context.Context, agentID string, userID *string, message string) []model.Memory {
	items, vecs, err := r.load(ctx, agentID)
	if err != nil {
		return nil
	}
	filtered := make([]model.Memory, 0, len(items))
	fvecs := make([][]float64, 0, len(items))
	for i := range items {
		if items[i].UserID == nil || (userID != nil && *items[i].UserID == *userID) {
			filtered = append(filtered, items[i])
			if i < len(vecs) {
				fvecs = append(fvecs, vecs[i])
			} else {
				fvecs = append(fvecs, nil)
			}
		}
	}
	queryVec := r.queryEmbed(ctx, message)
	if queryVec == nil {
		// 语义检索未启用 / 查询向量失败或超时 -> 纯关键词打分 (降级)
		return rankMemories(filtered, message, time.Now(), r.topN)
	}
	return rankHybridMemories(filtered, fvecs, message, queryVec, time.Now(), r.topN)
}

// queryEmbed 计算查询向量 (父 ctx 之上再限 embedBudget; 任何失败返回 nil 触发降级)
func (r *hybridRetriever) queryEmbed(ctx context.Context, message string) []float64 {
	if r.embedder == nil || !r.embedder.Enabled() {
		return nil
	}
	bctx, cancel := context.WithTimeout(ctx, r.embedBudget)
	defer cancel()
	vec, err := r.embedder.EmbedOne(bctx, message)
	if err != nil {
		return nil
	}
	return vec
}

// hybridScore 融合分 (设计文档 §8): 0.6*sem + 0.4*kw
// kw 为既有综合分 (关键词覆盖 + 时间衰减 + 使用频率 + 显式加权), sem 为语义相似度 (无向量为 0)
func hybridScore(sem, kw float64) float64 {
	return hybridWeightSem*sem + hybridWeightKw*kw
}

// rankHybridMemories 混合打分 + 硬过滤 + topN (排序/截断与 rankMemories 同语义)
func rankHybridMemories(mems []model.Memory, vecs [][]float64, query string, queryVec []float64, now time.Time, topN int) []model.Memory {
	queryTokens := tokenizeText(query)
	candidates := make([]memCandidate, 0, len(mems))
	for i := range mems {
		m := &mems[i]
		if m.Status != model.MemoryStatusActive {
			continue
		}
		kw := 0.0
		if len(queryTokens) > 0 {
			mTokens := tokenizeText(m.Content)
			hit := 0
			for t := range queryTokens {
				if mTokens[t] {
					hit++
				}
			}
			kw = float64(hit) / float64(len(queryTokens))
			kw += substringBonus(query, m.Content)
			if kw > 1 {
				kw = 1
			}
		}
		sem := 0.0
		if queryVec != nil && i < len(vecs) {
			if s := cosineSimilarity(queryVec, vecs[i]); s >= hybridSemNoiseFloor {
				sem = s
			}
		}
		// 硬过滤: 两信号均未命中且已过时 -> 不参与注入 (空查询同样适用)
		if kw == 0 && sem == 0 && now.Sub(m.UpdatedAt).Hours() > memStaleDays*24 {
			continue
		}
		if score := hybridScore(sem, memoryScore(kw, m, now)); score > 0 {
			candidates = append(candidates, memCandidate{mem: *m, score: score})
		}
	}
	return sortAndTrim(candidates, topN)
}

// cosineSimilarity 余弦相似度 (维度不匹配/零向量返回 0; 负值截断为 0)
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	s := dot / (math.Sqrt(na) * math.Sqrt(nb))
	if s < 0 {
		s = 0
	}
	if s > 1 {
		s = 1
	}
	return s
}

// parseMemoryVector 解析入库向量 (jsonb 浮点数组); 空/非法返回 nil
func parseMemoryVector(raw []byte) []float64 {
	if len(raw) == 0 {
		return nil
	}
	var v []float64
	if err := json.Unmarshal(raw, &v); err != nil || len(v) == 0 {
		return nil
	}
	return v
}
