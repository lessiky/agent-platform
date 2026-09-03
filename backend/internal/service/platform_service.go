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
	// PlatformEmbedModelMaxLen 向量模型模板名最大长度 (与 platform_settings.memory_embed_model 列一致)
	PlatformEmbedModelMaxLen = 64
	// PlatformIconMaxSize 平台图标原图字节上限 (base64 编码前), 1 MiB
	PlatformIconMaxSize = 1 << 20
)

// platformIconDataURLRe 仅接受受支持图片类型的 base64 data URL
var platformIconDataURLRe = regexp.MustCompile(`^data:image/(png|jpe?g|svg\+xml|webp|gif);base64,`)

// PlatformInfo 平台设置 (API 输出)
type PlatformInfo struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
	// MemoryEmbedModel 平台设置值: 记忆语义检索向量专用 ModelTemplate 名称 (空 = 跟随 MEMORY_EMBED_MODEL 环境变量)
	MemoryEmbedModel string `json:"memory_embed_model"`
	// MemoryEmbedModelEffective 当前生效值 (平台设置优先, 空时回退环境变量)
	MemoryEmbedModelEffective string `json:"memory_embed_model_effective"`
	// MemoryExtractModel 平台设置值: 记忆抽取/会话摘要用 ModelTemplate 名称 (空 = 跟随 MEMORY_EXTRACT_MODEL 环境变量, 再空 = Agent 当前模型)
	MemoryExtractModel string `json:"memory_extract_model"`
	// MemoryExtractModelEffective 当前生效值 (平台设置优先, 空时回退环境变量; 空 = Agent 当前模型)
	MemoryExtractModelEffective string `json:"memory_extract_model_effective"`
	UpdatedAt                   string `json:"updated_at,omitempty"`
}

// UpdatePlatformRequest 更新平台设置
// Name 必填 (1-64 字符); Icon 为 *string: nil = 不修改, 空串 = 清除自定义图标, 其余为 base64 data URL;
// MemoryEmbedModel / MemoryExtractModel 为 *string: nil = 不修改, 空串 = 跟随对应环境变量, 其余为 ModelTemplate 名称
type UpdatePlatformRequest struct {
	Name               string  `json:"name"`
	Icon               *string `json:"icon"`
	MemoryEmbedModel   *string `json:"memory_embed_model"`
	MemoryExtractModel *string `json:"memory_extract_model"`
}

// PlatformModelSources 平台设置模型模板名的运行时来源 (向量 embed / 抽取 extract)
// Src 读取当前生效值 (回显 effective), Sink 在平台设置更新后推送覆盖值 (即时生效, 免重启); 字段可为 nil (对应功能不参与同步/回显)
type PlatformModelSources struct {
	Embed       TemplateSource
	EmbedSink   TemplateSetter
	Extract     TemplateSource
	ExtractSink TemplateSetter
}

// PlatformService 平台设置服务 (平台名/图标, 登录页与侧边导航展示)
type PlatformService interface {
	// Get 读取平台设置 (不存在时返回默认值)
	Get(ctx context.Context) (*PlatformInfo, error)
	// Update 更新平台设置 (写入审计日志)
	Update(ctx context.Context, req UpdatePlatformRequest, userID *string, username, ip string) (*PlatformInfo, error)
	// SyncModelSettings 将库中已保存的向量/抽取模型名同步到运行时来源 (服务启动时调用一次, 使重启后平台设置值即时生效)
	SyncModelSettings(ctx context.Context) error
}

type platformService struct {
	repo  repository.PlatformSettingsRepository
	audit repository.AuditLogRepository
	// models 向量/抽取模型名的运行时来源 (回显 effective + 更新后即时推送)
	models PlatformModelSources
}

func NewPlatformService(repo repository.PlatformSettingsRepository, audit repository.AuditLogRepository, models PlatformModelSources) PlatformService {
	return &platformService{repo: repo, audit: audit, models: models}
}

func (s *platformService) Get(ctx context.Context) (*PlatformInfo, error) {
	settings, err := s.repo.Get(ctx)
	if err != nil {
		return nil, err
	}
	return toPlatformInfo(settings, s.effectiveEmbedModel(settings), s.effectiveExtractModel(settings)), nil
}

// effectiveEmbedModel 当前生效的向量模型名: 有运行时来源则取生效值 (平台设置优先 / env 兜底), 否则仅回显平台设置值
func (s *platformService) effectiveEmbedModel(settings *model.PlatformSettings) string {
	if s.models.Embed != nil {
		return s.models.Embed.Current()
	}
	return settings.MemoryEmbedModel
}

// effectiveExtractModel 当前生效的抽取/摘要模型名 (语义同 effectiveEmbedModel; 空 = Agent 当前模型)
func (s *platformService) effectiveExtractModel(settings *model.PlatformSettings) string {
	if s.models.Extract != nil {
		return s.models.Extract.Current()
	}
	return settings.MemoryExtractModel
}

// SyncModelSettings 启动时把库中已保存的向量/抽取模型名推送到运行时来源 (失败由调用方告警, 不阻塞启动)
func (s *platformService) SyncModelSettings(ctx context.Context) error {
	settings, err := s.repo.Get(ctx)
	if err != nil {
		return err
	}
	if s.models.EmbedSink != nil {
		s.models.EmbedSink.Set(settings.MemoryEmbedModel)
	}
	if s.models.ExtractSink != nil {
		s.models.ExtractSink.Set(settings.MemoryExtractModel)
	}
	return nil
}

func (s *platformService) Update(ctx context.Context, req UpdatePlatformRequest, userID *string, username, ip string) (*PlatformInfo, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.NewValidationError("平台名不能为空")
	}
	if len([]rune(name)) > PlatformNameMaxLen {
		return nil, errors.NewValidationError("平台名长度不能超过 " + strconv.Itoa(PlatformNameMaxLen) + " 个字符")
	}

	embedModel := ""
	if req.MemoryEmbedModel != nil {
		embedModel = strings.TrimSpace(*req.MemoryEmbedModel)
		if len([]rune(embedModel)) > PlatformEmbedModelMaxLen {
			return nil, errors.NewValidationError("向量模型名称长度不能超过 " + strconv.Itoa(PlatformEmbedModelMaxLen) + " 个字符")
		}
	}

	extractModel := ""
	if req.MemoryExtractModel != nil {
		extractModel = strings.TrimSpace(*req.MemoryExtractModel)
		if len([]rune(extractModel)) > PlatformEmbedModelMaxLen {
			return nil, errors.NewValidationError("抽取模型名称长度不能超过 " + strconv.Itoa(PlatformEmbedModelMaxLen) + " 个字符")
		}
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
	embedModelChanged := false
	if req.MemoryEmbedModel != nil && embedModel != settings.MemoryEmbedModel {
		settings.MemoryEmbedModel = embedModel
		embedModelChanged = true
	}
	extractModelChanged := false
	if req.MemoryExtractModel != nil && extractModel != settings.MemoryExtractModel {
		settings.MemoryExtractModel = extractModel
		extractModelChanged = true
	}
	settings.Name = name
	settings.UpdatedBy = userID
	settings.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, settings); err != nil {
		return nil, err
	}
	// 平台设置页免重启: 变更即时推送到运行时来源 (embedder / 模型路由 / 抽取器按新名称工作)
	if embedModelChanged && s.models.EmbedSink != nil {
		s.models.EmbedSink.Set(settings.MemoryEmbedModel)
	}
	if extractModelChanged && s.models.ExtractSink != nil {
		s.models.ExtractSink.Set(settings.MemoryExtractModel)
	}

	s.appendAudit(ctx, userID, username, ip, before, *settings, iconChanged, embedModelChanged, extractModelChanged)
	return toPlatformInfo(settings, s.effectiveEmbedModel(settings), s.effectiveExtractModel(settings)), nil
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

func toPlatformInfo(settings *model.PlatformSettings, embedModelEffective, extractModelEffective string) *PlatformInfo {
	info := &PlatformInfo{
		Name:                        settings.Name,
		Icon:                        settings.Icon,
		MemoryEmbedModel:            settings.MemoryEmbedModel,
		MemoryEmbedModelEffective:   embedModelEffective,
		MemoryExtractModel:          settings.MemoryExtractModel,
		MemoryExtractModelEffective: extractModelEffective,
	}
	if !settings.UpdatedAt.IsZero() {
		info.UpdatedAt = settings.UpdatedAt.Format("2006-01-02 15:04:05")
	}
	return info
}

// appendAudit 写审计日志 (失败仅告警, 不阻塞主流程)
func (s *platformService) appendAudit(ctx context.Context, userID *string, username, ip string, before, after model.PlatformSettings, iconChanged, embedModelChanged, extractModelChanged bool) {
	if s.audit == nil {
		return
	}
	detail := map[string]interface{}{
		"name_before":           before.Name,
		"name_after":            after.Name,
		"icon_changed":          iconChanged,
		"name_changed":          before.Name != after.Name,
		"embed_model_before":    before.MemoryEmbedModel,
		"embed_model_after":     after.MemoryEmbedModel,
		"embed_model_changed":   embedModelChanged,
		"extract_model_before":  before.MemoryExtractModel,
		"extract_model_after":   after.MemoryExtractModel,
		"extract_model_changed": extractModelChanged,
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
