// template_source.go — M10 平台设置模型模板名的运行时来源
//
// 两个名称, 优先级均为: 平台设置页 (运行时可改, 免重启) > 环境变量 (部署基线):
//   - 向量 (Embed):  MEMORY_EMBED_MODEL,  空 = 语义检索整体不生效 (纯关键词检索);
//   - 抽取 (Extract): MEMORY_EXTRACT_MODEL, 空 = 使用 Agent 当前模型。
//
// 服务端使用 MutableTemplateSource (启动以 env 兜底, 平台设置更新时经 Set 即时生效);
// 离线工具 / 单测使用 StaticTemplateSource 固定名称。
// Current 返回空串时由调用方按各自语义降级, 无需感知来源差异。

package service

import (
	"strings"
	"sync"
)

// TemplateSource ModelTemplate 名称的运行时来源
type TemplateSource interface {
	// Current 返回当前生效的模板名 (空 = 未配置)
	Current() string
}

// TemplateSetter 平台设置更新后向运行时推送模板名的写入端
type TemplateSetter interface {
	Set(v string)
}

// StaticTemplateSource 固定名称来源 (离线工具 / 单测)
type StaticTemplateSource string

func (s StaticTemplateSource) Current() string {
	return strings.TrimSpace(string(s))
}

// MutableTemplateSource 可变来源: 平台设置覆盖值 + 环境变量兜底 (服务端)
type MutableTemplateSource struct {
	fallback string // 环境变量值 (部署基线)
	mu       sync.RWMutex
	override string // 平台设置值; 空 = 跟随 fallback
}

// NewMutableTemplateSource 以环境变量值创建可变来源
func NewMutableTemplateSource(fallback string) *MutableTemplateSource {
	return &MutableTemplateSource{fallback: strings.TrimSpace(fallback)}
}

// Set 写入平台设置值 (空串 = 跟随环境变量); 线程安全, 写入后即时生效
func (s *MutableTemplateSource) Set(v string) {
	s.mu.Lock()
	s.override = strings.TrimSpace(v)
	s.mu.Unlock()
}

// Override 返回平台设置当前存储值 (空 = 未配置, 跟随环境变量); 供平台设置 API 回显
func (s *MutableTemplateSource) Override() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.override
}

// Current 返回生效值: 平台设置优先, 空时回退环境变量
func (s *MutableTemplateSource) Current() string {
	s.mu.RLock()
	v := s.override
	s.mu.RUnlock()
	if v != "" {
		return v
	}
	return s.fallback
}

var _ TemplateSource = (*MutableTemplateSource)(nil)
var _ TemplateSetter = (*MutableTemplateSource)(nil)
var _ TemplateSource = StaticTemplateSource("")
