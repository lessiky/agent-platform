package repository

import (
	"context"

	"agent-platform/internal/database"
	"agent-platform/internal/model"

	"gorm.io/gorm"
)

// PlatformSettingsRepository 平台设置仓储 (单例 id=1)
type PlatformSettingsRepository interface {
	// Get 读取设置, 不存在时创建默认值 (平台名默认 "Agent 管理平台", 无自定义图标)
	Get(ctx context.Context) (*model.PlatformSettings, error)
	// Update 保存设置
	Update(ctx context.Context, s *model.PlatformSettings) error
}

type platformSettingsRepository struct{}

func NewPlatformSettingsRepository() PlatformSettingsRepository {
	return &platformSettingsRepository{}
}

func (r *platformSettingsRepository) Get(ctx context.Context) (*model.PlatformSettings, error) {
	var s model.PlatformSettings
	err := database.DB.WithContext(ctx).First(&s, "id = ?", "1").Error
	if err == gorm.ErrRecordNotFound {
		s = model.PlatformSettings{ID: "1", Name: model.DefaultPlatformName}
		if cerr := database.DB.WithContext(ctx).Create(&s).Error; cerr != nil {
			return nil, cerr
		}
		return &s, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *platformSettingsRepository) Update(ctx context.Context, s *model.PlatformSettings) error {
	return database.DB.WithContext(ctx).Save(s).Error
}
