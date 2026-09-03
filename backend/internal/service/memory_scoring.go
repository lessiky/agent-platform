package service

import (
	"math"
	"strings"
	"time"
	"unicode"

	"agent-platform/internal/model"
)

// M10 记忆打分纯函数: 分词 / 关键词覆盖 / 综合得分 / 去重相似度。
// 全部为无状态纯函数, 表驱动单测覆盖 (memory_scoring_test.go)。

// 打分权重 (设计文档 §4.2)
const (
	memWeightKeyword = 0.5
	memWeightRecency = 0.3
	memWeightUsage   = 0.2
	memExplicitBoost = 1.2 // user_explicit 加权
	memDecayDays     = 30  // 时间衰减: exp(-age_days/30)
	memUsageCap      = 50  // 访问次数归一化上限 log1p(x)/log1p(cap)
	memStaleDays     = 90  // 硬过滤: 无关键词命中且 age > 90 天 → 不参与注入
)

// isCJK 判定 rune 是否中日韩文字 (bigram 分词对象)
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK 统一表意文字
		(r >= 0x3400 && r <= 0x4DBF) || // 扩展 A
		(r >= 0xF900 && r <= 0xFAFF) || // 兼容表意文字
		(r >= 0x3040 && r <= 0x30FF) || // 日文假名
		(r >= 0xAC00 && r <= 0xD7AF) // 谚文
}

// tokenizeText 分词: ASCII 小写按词切分 (长度 >= 2) + CJK 字符 bigram, 合并为集合
func tokenizeText(s string) map[string]bool {
	tokens := make(map[string]bool)
	runes := []rune(s)
	// ASCII 词 (小写)
	var word []rune
	flushWord := func() {
		if len(word) >= 2 {
			tokens[strings.ToLower(string(word))] = true
		}
		word = word[:0]
	}
	var cjk []rune
	flushCJK := func() {
		for i := 0; i+1 < len(cjk); i++ {
			tokens[string(cjk[i:i+2])] = true
		}
		cjk = cjk[:0]
	}
	for _, r := range runes {
		if isCJK(r) {
			flushWord()
			cjk = append(cjk, r)
			continue
		}
		flushCJK()
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word = append(word, r)
			continue
		}
		flushWord()
	}
	flushWord()
	flushCJK()
	return tokens
}

// normalizeContent 归一化 (精确去重): 小写 + 去空白与标点
func normalizeContent(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// substringBonus 子串命中: query 中长度 >= 4 的连续片段出现在 content 中 → 0.5
func substringBonus(query, content string) float64 {
	query = strings.ToLower(query)
	content = strings.ToLower(content)
	if len([]rune(query)) < 4 || len(query) == 0 {
		return 0
	}
	if strings.Contains(content, query) {
		return 0.5
	}
	return 0
}

// keywordScore 关键词覆盖度: |T(q) ∩ T(m)| / max(1, |T(q)|) + 子串加成, 上限 1.0
func keywordScore(query, content string) float64 {
	qTokens := tokenizeText(query)
	if len(qTokens) == 0 {
		return 0
	}
	mTokens := tokenizeText(content)
	hit := 0
	for t := range qTokens {
		if mTokens[t] {
			hit++
		}
	}
	score := float64(hit) / float64(len(qTokens))
	if score = score + substringBonus(query, content); score > 1 {
		score = 1
	}
	return score
}

// jaccard 集合 Jaccard 相似度
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// contentSimilar 两条记忆内容相似度 (去重判定用, >= memDedupThreshold 视为同一条)
func contentSimilar(a, b string) float64 {
	return jaccard(tokenizeText(a), tokenizeText(b))
}

// memDedupThreshold 去重 Jaccard 阈值
const memDedupThreshold = 0.7

// memCandidate 检索候选 (打分中间结果)
type memCandidate struct {
	mem   model.Memory
	score float64
}

// scoreMemory 单条记忆综合得分 (设计文档 §4.2 公式)
func scoreMemory(m *model.Memory, query string, queryTokens map[string]bool, now time.Time) float64 {
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
	// 硬过滤: 无关键词命中且已过时 → 不参与注入 (空查询同样适用)
	if kw == 0 && now.Sub(m.UpdatedAt).Hours() > memStaleDays*24 {
		return 0
	}
	return memoryScore(kw, m, now)
}

// memoryScore 加权公式
func memoryScore(kw float64, m *model.Memory, now time.Time) float64 {
	ageDays := now.Sub(m.UpdatedAt).Hours() / 24
	recency := math.Exp(-ageDays / memDecayDays)
	usage := math.Log1p(float64(m.AccessCount)) / math.Log1p(memUsageCap)
	score := memWeightKeyword*kw + memWeightRecency*recency + memWeightUsage*usage
	if m.Source == model.MemorySourceUserExplicit {
		score *= memExplicitBoost
	}
	return score
}

// rankMemories 打分 + 硬过滤 + 降序排序, 返回 top-n (n<=0 表示不限制)
func rankMemories(mems []model.Memory, query string, now time.Time, topN int) []model.Memory {
	queryTokens := tokenizeText(query)
	candidates := make([]memCandidate, 0, len(mems))
	for i := range mems {
		if mems[i].Status != model.MemoryStatusActive {
			continue
		}
		if score := scoreMemory(&mems[i], query, queryTokens, now); score > 0 {
			candidates = append(candidates, memCandidate{mem: mems[i], score: score})
		}
	}
	// 降序 (稳定: 同分按 updated_at 新者优先)
	return sortAndTrim(candidates, topN)
}

// sortAndTrim 候选降序 (同分按 updated_at 新者优先) + topN 截断 (n<=0 不限)
func sortAndTrim(candidates []memCandidate, topN int) []model.Memory {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0; j-- {
			a, b := candidates[j], candidates[j-1]
			if a.score > b.score || (a.score == b.score && a.mem.UpdatedAt.After(b.mem.UpdatedAt)) {
				candidates[j], candidates[j-1] = b, a
			} else {
				break
			}
		}
	}
	out := make([]model.Memory, 0, len(candidates))
	for _, c := range candidates {
		if topN > 0 && len(out) >= topN {
			break
		}
		out = append(out, c.mem)
	}
	return out
}
