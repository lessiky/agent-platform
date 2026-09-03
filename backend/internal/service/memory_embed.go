package service

// memory_embed.go — M10.3 语义检索: 记忆向量计算组件
//
// MemoryEmbedder 为独立组件 (仅依赖 ModelTemplateService):
//   - 写入路径: MemoryService / MemoryExtractor 在记忆写入后触发 EmbedAsync, 异步计算向量回写
//     agent_memories.embedding (列已在 M10.1 预留);
//   - 读取路径: HybridRetriever 计算查询向量。
//
// 降级: 向量模型模板名未配置 (整体不生效; 平台设置页 / MEMORY_EMBED_MODEL) 或调用失败时返回错误/空向量,
// 调用方自动退回纯关键词打分; 向量列留空, 平台其余功能不受影响。

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MemoryEmbedder 记忆向量计算 (M10.3)
type MemoryEmbedder interface {
	// Enabled 语义检索是否可用 (仅当配置了 embedding 模型模板时为 true)
	Enabled() bool
	// EmbedOne 计算单条文本向量 (失败返回 error, 由调用方降级)
	EmbedOne(ctx context.Context, text string) ([]float64, error)
}

type memoryEmbedder struct {
	modelSvc ModelTemplateService
	nameSrc  TemplateSource // 运行时来源 (平台设置优先, MEMORY_EMBED_MODEL 兜底; 运行时可切换)
	timeout  time.Duration
}

// NewMemoryEmbedder 创建记忆向量组件 (M10.3):
// nameSrc 为向量模型模板名的运行时来源 (平台设置页可免重启切换, 空值 = 语义检索整体不生效);
// timeout 为单次向量计算超时 (MEMORY_EMBED_TIMEOUT, 默认 10s)
func NewMemoryEmbedder(modelSvc ModelTemplateService, nameSrc TemplateSource, timeout time.Duration) MemoryEmbedder {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &memoryEmbedder{
		modelSvc: modelSvc,
		nameSrc:  nameSrc,
		timeout:  timeout,
	}
}

func (e *memoryEmbedder) Enabled() bool {
	return e.modelSvc != nil && e.nameSrc != nil && e.nameSrc.Current() != ""
}

func (e *memoryEmbedder) EmbedOne(ctx context.Context, text string) ([]float64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty text")
	}
	if !e.Enabled() {
		return nil, fmt.Errorf("embedding model template not configured (平台设置 memory_embed_model / MEMORY_EMBED_MODEL)")
	}
	name := e.nameSrc.Current()
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	vecs, err := e.modelSvc.EmbedForMemory(ctx, name, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("unexpected embedding response (vectors=%d)", len(vecs))
	}
	return vecs[0], nil
}
