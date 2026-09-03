package model

import "time"

// DefaultPlatformName 平台默认名称 (未配置平台设置时展示)
const DefaultPlatformName = "Agent 管理平台"

// PlatformSettings 平台级设置 (单例, id=1): 平台名 + 平台图标
// Icon 为图片 base64 data URL (data:image/png|jpeg|svg+xml|webp|gif;base64,...), 空串表示使用内置默认图标
type PlatformSettings struct {
	ID   string `gorm:"primaryKey" json:"id"`
	Name string `gorm:"type:varchar(64);not null" json:"name"`
	Icon string `gorm:"type:text;not null;default:''" json:"icon"`
	// MemoryEmbedModel 记忆语义检索 (M10.3) 向量专用 ModelTemplate 名称; 空 = 跟随 MEMORY_EMBED_MODEL 环境变量
	MemoryEmbedModel string `gorm:"column:memory_embed_model;type:varchar(64);not null;default:''" json:"memory_embed_model"`
	// MemoryExtractModel 记忆抽取/会话摘要 (M10.2) 用 ModelTemplate 名称; 空 = 跟随 MEMORY_EXTRACT_MODEL 环境变量 (再空 = Agent 当前模型)
	MemoryExtractModel string    `gorm:"column:memory_extract_model;type:varchar(64);not null;default:''" json:"memory_extract_model"`
	UpdatedBy          *string   `gorm:"type:uuid" json:"updated_by"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (PlatformSettings) TableName() string {
	return "platform_settings"
}
