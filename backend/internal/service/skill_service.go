package service

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// SkillService 技能管理服务 (M9, PRD 2.5)
type SkillService interface {
	// Import 导入技能包: force=false 同名拒绝 (409), force=true 同名升级 (版本 +1, 关联保留)
	Import(ctx context.Context, data []byte, force bool, operatorID, operatorName, ip string) (*model.Skill, error)
	List(ctx context.Context, filter repository.SkillListFilter) ([]SkillListItem, int64, error)
	Get(ctx context.Context, id string) (*model.Skill, []repository.SkillFileMeta, error)
	GetFile(ctx context.Context, id, filePath string) (*model.SkillFile, error)
	UpdateStatus(ctx context.Context, id, status, operatorID, operatorName, ip string) (*model.Skill, error)
	Delete(ctx context.Context, id string, force bool, operatorID, operatorName, ip string) error
	ListAgents(ctx context.Context, skillID string) ([]repository.AgentSkillBindingView, error)
	Usage(ctx context.Context, skillID string) (repository.SkillUsage, error)
	// LoadActiveSkillsForAgent 运行时加载 Agent 关联的启用技能 (对话注入用)
	LoadActiveSkillsForAgent(ctx context.Context, agentID string) ([]model.Skill, error)
}

// SkillListItem 技能列表项 (含关联 Agent 数 / 使用中标记)
type SkillListItem struct {
	model.Skill
	AgentCount int  `json:"agent_count"`
	InUse      bool `json:"in_use"`
}

type skillService struct {
	skills   repository.SkillRepository
	files    repository.SkillFileRepository
	bindings repository.SkillAgentBindingRepository
	audits   repository.AuditLogRepository
	limits   SkillLimits
}

// NewSkillService 创建技能服务
func NewSkillService(
	skills repository.SkillRepository,
	files repository.SkillFileRepository,
	bindings repository.SkillAgentBindingRepository,
	audits repository.AuditLogRepository,
	limits SkillLimits,
) SkillService {
	return &skillService{skills: skills, files: files, bindings: bindings, audits: audits, limits: limits}
}

// Import 导入技能包 (M9-1.2/1.3): 解析校验 -> 冲突检查 -> 事务写入 -> 审计
func (s *skillService) Import(ctx context.Context, data []byte, force bool, operatorID, operatorName, ip string) (*model.Skill, error) {
	parsed, err := parseSkillPackage(data, s.limits)
	if err != nil {
		return nil, err
	}
	existing, err := s.skills.GetByName(ctx, parsed.Name)
	if err != nil {
		return nil, errors.Wrap(err, "failed to check skill name")
	}
	if existing != nil && !force {
		return nil, errors.ErrSkillConflict
	}

	tagsJSON, _ := json.Marshal(parsed.Tags)
	toolsJSON, _ := json.Marshal(parsed.RequiredTools)
	fileRows := make([]model.SkillFile, 0, len(parsed.Files))
	for i := range parsed.Files {
		fileRows = append(fileRows, model.SkillFile{
			Path:    parsed.Files[i].Path,
			Size:    parsed.Files[i].Size,
			Sha256:  parsed.Files[i].Sha256,
			Content: parsed.Files[i].Data,
		})
	}

	var result *model.Skill
	err = database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if existing != nil {
			// 升级: 版本 +1, 元数据与文件替换, 关联保留
			existing.Version += 1
			existing.VersionSpec = parsed.VersionSpec
			existing.Description = parsed.Description
			existing.Author = parsed.Author
			existing.Tags = datatypes.JSON(tagsJSON)
			existing.RequiredTools = datatypes.JSON(toolsJSON)
			existing.EntryContent = parsed.EntryContent
			existing.SizeBytes = parsed.SizeBytes + int64(len([]byte(parsed.EntryContent)))
			existing.FileCount = len(parsed.Files) + 1
			if err := tx.Save(existing).Error; err != nil {
				return err
			}
			if err := tx.Where("skill_id = ?", existing.ID).Delete(&model.SkillFile{}).Error; err != nil {
				return err
			}
			result = existing
		} else {
			skill := &model.Skill{
				Name:          parsed.Name,
				Version:       1,
				VersionSpec:   parsed.VersionSpec,
				Description:   parsed.Description,
				Author:        parsed.Author,
				Tags:          datatypes.JSON(tagsJSON),
				RequiredTools: datatypes.JSON(toolsJSON),
				EntryContent:  parsed.EntryContent,
				SizeBytes:     parsed.SizeBytes + int64(len([]byte(parsed.EntryContent))),
				FileCount:     len(parsed.Files) + 1,
				Status:        model.SkillStatusActive,
				CreatedBy:     strPtr(operatorID),
			}
			if err := tx.Create(skill).Error; err != nil {
				return err
			}
			result = skill
		}
		for i := 0; i < len(fileRows); i += 50 {
			end := i + 50
			if end > len(fileRows) {
				end = len(fileRows)
			}
			for j := i; j < end; j++ {
				fileRows[j].SkillID = result.ID
			}
			if err := tx.Create(fileRows[i:end]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return nil, errors.ErrSkillConflict // 并发同名导入
		}
		return nil, errors.Wrap(err, "failed to import skill")
	}

	if existing != nil {
		s.audit(ctx, operatorID, operatorName, "skill.upgraded", "skill", strPtr(result.ID), ip, map[string]interface{}{
			"name": result.Name, "version": result.Version, "previous_version": result.Version - 1,
		})
	} else {
		s.audit(ctx, operatorID, operatorName, "skill.imported", "skill", strPtr(result.ID), ip, map[string]interface{}{
			"name": result.Name, "version": 1, "size_bytes": result.SizeBytes, "file_count": result.FileCount,
		})
	}
	return result, nil
}

// List 分页列表 (含关联 Agent 数 / 使用中标记)
func (s *skillService) List(ctx context.Context, filter repository.SkillListFilter) ([]SkillListItem, int64, error) {
	skills, total, err := s.skills.List(ctx, filter)
	if err != nil {
		return nil, 0, errors.Wrap(err, "failed to list skills")
	}
	ids := make([]string, len(skills))
	for i := range skills {
		ids[i] = skills[i].ID
	}
	counts, err := s.skills.CountAgents(ctx, ids)
	if err != nil {
		return nil, 0, errors.Wrap(err, "failed to count skill agents")
	}
	items := make([]SkillListItem, 0, len(skills))
	for i := range skills {
		cnt := counts[skills[i].ID]
		items = append(items, SkillListItem{
			Skill:      skills[i],
			AgentCount: cnt,
			InUse:      cnt > 0,
		})
	}
	return items, total, nil
}

// Get 详情 (含文件元数据列表)
func (s *skillService) Get(ctx context.Context, id string) (*model.Skill, []repository.SkillFileMeta, error) {
	skill, err := s.skills.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	metas, err := s.files.ListMetaBySkill(ctx, id)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to list skill files")
	}
	if metas == nil {
		metas = []repository.SkillFileMeta{}
	}
	return skill, metas, nil
}

// GetFile 获取单个资源文件 (路径穿越防护由 safeSkillPath 保证)
func (s *skillService) GetFile(ctx context.Context, id, filePath string) (*model.SkillFile, error) {
	filePath, ok := safeSkillPath(filePath)
	if !ok || filePath == skillManifestName {
		return nil, errors.ErrNotFound
	}
	skill, err := s.skills.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	file, err := s.files.GetByPath(ctx, skill.ID, filePath)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// UpdateStatus 启用 / 禁用 (禁用后运行时不注入, 关联保留)
func (s *skillService) UpdateStatus(ctx context.Context, id, status, operatorID, operatorName, ip string) (*model.Skill, error) {
	if status != model.SkillStatusActive && status != model.SkillStatusDisabled {
		return nil, errors.NewValidationError("status 须为 active 或 disabled")
	}
	skill, err := s.skills.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.skills.UpdateStatus(ctx, id, status); err != nil {
		return nil, errors.Wrap(err, "failed to update skill status")
	}
	skill.Status = status
	s.audit(ctx, operatorID, operatorName, "skill.status_changed", "skill", strPtr(skill.ID), ip, map[string]interface{}{
		"name": skill.Name, "status": status,
	})
	return skill, nil
}

// Delete 删除技能: 有关联默认拦截 (409), force=true 级联解绑; 文件与绑定一并清理
func (s *skillService) Delete(ctx context.Context, id string, force bool, operatorID, operatorName, ip string) error {
	skill, err := s.skills.Get(ctx, id)
	if err != nil {
		return err
	}
	if !force {
		agents, aErr := s.bindings.ListBySkill(ctx, id)
		if aErr != nil {
			return errors.Wrap(aErr, "failed to list skill bindings")
		}
		if len(agents) > 0 {
			return errors.ErrSkillInUse
		}
	}
	if err := s.bindings.DeleteBySkill(ctx, id); err != nil {
		return errors.Wrap(err, "failed to unbind agents")
	}
	if err := s.files.DeleteBySkill(ctx, id); err != nil {
		return errors.Wrap(err, "failed to delete skill files")
	}
	if err := s.skills.Delete(ctx, id); err != nil {
		return errors.Wrap(err, "failed to delete skill")
	}
	s.audit(ctx, operatorID, operatorName, "skill.deleted", "skill", strPtr(skill.ID), ip, map[string]interface{}{
		"name": skill.Name, "version": skill.Version, "force": force,
	})
	return nil
}

// ListAgents 技能关联的 Agent 列表
func (s *skillService) ListAgents(ctx context.Context, skillID string) ([]repository.AgentSkillBindingView, error) {
	if _, err := s.skills.Get(ctx, skillID); err != nil {
		return nil, err
	}
	views, err := s.bindings.AgentsOfSkill(ctx, skillID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list skill agents")
	}
	if views == nil {
		views = []repository.AgentSkillBindingView{}
	}
	return views, nil
}

// Usage 使用统计 (关联数 / 近 30 天加载次数 / 最近使用时间)
func (s *skillService) Usage(ctx context.Context, skillID string) (repository.SkillUsage, error) {
	skill, err := s.skills.Get(ctx, skillID)
	if err != nil {
		return repository.SkillUsage{}, err
	}
	usage, err := repository.GetSkillUsage(ctx, skill)
	if err != nil {
		return repository.SkillUsage{}, errors.Wrap(err, "failed to get skill usage")
	}
	return usage, nil
}

// LoadActiveSkillsForAgent 运行时加载 Agent 关联的启用技能 (名称排序稳定)
func (s *skillService) LoadActiveSkillsForAgent(ctx context.Context, agentID string) ([]model.Skill, error) {
	skills, err := s.skills.ListActiveByAgent(ctx, agentID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load agent skills")
	}
	return skills, nil
}

// audit 写审计日志 (失败仅告警, 不阻塞主流程)
func (s *skillService) audit(ctx context.Context, operatorID, operatorName, action, resource string, resourceID *string, ip string, detail map[string]interface{}) {
	if s.audits == nil {
		return
	}
	entry := &model.AuditLog{
		Username:   operatorName,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		IP:         ip,
		CreatedAt:  time.Now(),
	}
	if operatorID != "" {
		entry.UserID = &operatorID
	}
	if detail != nil {
		entry.Detail = datatypes.JSON(mustMarshal(detail))
	}
	if err := s.audits.Append(ctx, entry); err != nil {
		log.Printf("skill: audit append failed action=%s: %v", action, err)
	}
}

func mustMarshal(v interface{}) datatypes.JSON {
	b, err := json.Marshal(v)
	if err != nil {
		return datatypes.JSON("null")
	}
	return datatypes.JSON(b)
}
