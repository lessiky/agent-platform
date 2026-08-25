package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"
)

// 平台设置约束
const (
	// PlatformNameMaxLen 平台名最大长度 (按字符计)
	PlatformNameMaxLen = 64
	// PlatformIconMaxSize 平台图标原图字节上限 (base64 编码前), 1 MiB
	PlatformIconMaxSize = 1 << 20
)

// platformIconDataURLRe 仅接受受支持图片类型的 base64 data URL
var platformIconDataURLRe = regexp.MustCompile(`^data:image/(png|jpe?g|svg\+xml|webp|gif);base64,`)

// PlatformInfo 平台设置 (API 输出)
type PlatformInfo struct {
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// UpdatePlatformRequest 更新平台设置
// Name 必填 (1-64 字符); Icon 为 *string: nil = 不修改, 空串 = 清除自定义图标, 其余为 base64 data URL
type UpdatePlatformRequest struct {
	Name string  `json:"name"`
	Icon *string `json:"icon"`
}

// PlatformService 平台设置服务 (平台名/图标, 登录页与侧边导航展示)
type PlatformService interface {
	// Get 读取平台设置 (不存在时返回默认值)
	Get(ctx context.Context) (*PlatformInfo, error)
	// Update 更新平台设置 (写入审计日志)
	Update(ctx context.Context, req UpdatePlatformRequest, userID *string, username, ip string) (*PlatformInfo, error)
}

type platformService struct {
	repo  repository.PlatformSettingsRepository
	audit repository.AuditLogRepository
}

func NewPlatformService(repo repository.PlatformSettingsRepository, audit repository.AuditLogRepository) PlatformService {
	return &platformService{repo: repo, audit: audit}
}

func (s *platformService) Get(ctx context.Context) (*PlatformInfo, error) {
	settings, err := s.repo.Get(ctx)
	if err != nil {
		return nil, err
	}
	return toPlatformInfo(settings), nil
}

func (s *platformService) Update(ctx context.Context, req UpdatePlatformRequest, userID *string, username, ip string) (*PlatformInfo, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.NewValidationError("平台名不能为空")
	}
	if len([]rune(name)) > PlatformNameMaxLen {
		return nil, errors.NewValidationError("平台名长度不能超过 " + strconv.Itoa(PlatformNameMaxLen) + " 个字符")
	}

	settings, err := s.repo.Get(ctx)
	if err != nil {
		return nil, err
	}
	before := *settings

	iconChanged := false
	if req.Icon != nil {
		icon := strings.TrimSpace(*req.Icon)
		if icon != "" {
			if verr := validatePlatformIcon(icon); verr != nil {
				return nil, verr
			}
		}
		if icon != settings.Icon {
			settings.Icon = icon
			iconChanged = true
		}
	}
	settings.Name = name
	settings.UpdatedBy = userID
	settings.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, settings); err != nil {
		return nil, err
	}

	s.appendAudit(ctx, userID, username, ip, before, *settings, iconChanged)
	return toPlatformInfo(settings), nil
}

// validatePlatformIcon 校验图标 data URL: 类型白名单 + base64 可解码 + 原图大小上限
func validatePlatformIcon(icon string) error {
	if !platformIconDataURLRe.MatchString(icon) {
		return errors.NewValidationError("图标必须是 PNG / JPG / SVG / WebP / GIF 图片")
	}
	_, encoded, ok := strings.Cut(icon, ",")
	if !ok {
		return errors.NewValidationError("图标数据不合法")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return errors.NewValidationError("图标数据不合法")
	}
	if len(raw) == 0 || len(raw) > PlatformIconMaxSize {
		return errors.NewValidationError("图标大小必须在 1B - 1MB 之间")
	}
	return nil
}

func toPlatformInfo(settings *model.PlatformSettings) *PlatformInfo {
	info := &PlatformInfo{Name: settings.Name, Icon: settings.Icon}
	if !settings.UpdatedAt.IsZero() {
		info.UpdatedAt = settings.UpdatedAt.Format("2006-01-02 15:04:05")
	}
	return info
}

// appendAudit 写审计日志 (失败仅告警, 不阻塞主流程)
func (s *platformService) appendAudit(ctx context.Context, userID *string, username, ip string, before, after model.PlatformSettings, iconChanged bool) {
	if s.audit == nil {
		return
	}
	detail := map[string]interface{}{
		"name_before":  before.Name,
		"name_after":   after.Name,
		"icon_changed": iconChanged,
		"name_changed": before.Name != after.Name,
	}
	payload, _ := json.Marshal(detail)
	entry := &model.AuditLog{
		UserID:   userID,
		Username: username,
		Action:   "platform.update",
		Resource: "platform",
		Detail:   payload,
		IP:       ip,
	}
	if err := s.audit.Append(ctx, entry); err != nil {
		log.Printf("platform: audit append failed: %v", err)
	}
}
