package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// maxFullInjectionChars full_injection 模式正文预算 (M9, PRD 2.5.4)
const maxFullInjectionChars = 128 * 1024

// decodeSkillStringList 解码 JSONB 字符串数组
func decodeSkillStringList(data datatypes.JSON) []string {
	var out []string
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

// availableToolNames Agent 可用工具集: 绑定 MCP 已发现工具, 受 config.tools 白名单过滤 (与 ListAgentTools 同语义)
func (s *agentService) availableToolNames(ctx context.Context, mcpIDs, tools []string) (map[string]bool, error) {
	allowed := make(map[string]bool)
	if len(tools) > 0 {
		for _, tool := range tools {
			allowed[tool] = true
		}
	}
	available := make(map[string]bool)
	for _, id := range mcpIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		server, err := s.mcps.Get(ctx, id)
		if err != nil {
			return nil, errors.NewValidationError("绑定的 MCP 不存在: " + id)
		}
		serverTools, _ := decodeTools(server.Tools)
		for _, tool := range serverTools {
			if len(allowed) > 0 && !allowed[tool.Name] {
				continue
			}
			available[tool.Name] = true
		}
	}
	return available, nil
}

// validateSkills 校验 Agent 技能绑定 (M9-1.8): 技能存在 + required_tools 依赖覆盖 + full_injection 预算
func (s *agentService) validateSkills(ctx context.Context, mcpIDs, tools, skillIDs []string, usageMode string) error {
	if len(skillIDs) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var skills []model.Skill
	for _, id := range skillIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		skill, err := s.skillRepo.Get(ctx, id)
		if err != nil {
			return errors.NewValidationError("绑定的技能不存在: " + id)
		}
		skills = append(skills, *skill)
	}
	available, err := s.availableToolNames(ctx, mcpIDs, tools)
	if err != nil {
		return err
	}
	for i := range skills {
		required := decodeSkillStringList(skills[i].RequiredTools)
		if len(required) == 0 {
			continue
		}
		var missing []string
		for _, tool := range required {
			if !available[tool] {
				missing = append(missing, tool)
			}
		}
		if len(missing) > 0 {
			return errors.NewValidationError(fmt.Sprintf("技能 %s 依赖不可用工具: %s", skills[i].Name, strings.Join(missing, ", ")))
		}
	}
	if usageMode == "full_injection" {
		var total int
		for i := range skills {
			total += len(skills[i].EntryContent)
		}
		if total > maxFullInjectionChars {
			return errors.NewValidationError(fmt.Sprintf("full_injection 模式技能正文总长 %d 超出预算 %d 字符, 请减少关联技能或改用渐进式披露模式", total, maxFullInjectionChars))
		}
	}
	return nil
}

// syncSkillBindings 全量同步 Agent 的技能绑定 (新增缺失, 移除多余)
func (s *agentService) syncSkillBindings(ctx context.Context, agentID string, skillIDs []string, operatorID string) error {
	current, err := s.skillBindings.ListByAgent(ctx, agentID)
	if err != nil {
		return errors.Wrap(err, "failed to list skill bindings")
	}
	wanted := make(map[string]bool, len(skillIDs))
	for _, id := range skillIDs {
		id = strings.TrimSpace(id)
		if id == "" || wanted[id] {
			continue
		}
		wanted[id] = true
		if err := s.skillBindings.Bind(ctx, id, agentID, strPtr(operatorID)); err != nil {
			return errors.Wrap(err, "failed to bind skill")
		}
	}
	for _, b := range current {
		if !wanted[b.SkillID] {
			if err := s.skillBindings.Unbind(ctx, b.SkillID, agentID); err != nil {
				return errors.Wrap(err, "failed to unbind skill")
			}
		}
	}
	return nil
}

// ListBoundSkills Agent 绑定的技能列表 (含 required_tools 覆盖状态)
func (s *agentService) ListBoundSkills(ctx context.Context, agentID string) ([]BoundSkillView, error) {
	agent, err := s.agents.GetByID(ctx, agentID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "failed to get agent")
	}
	bindings, err := s.skillBindings.ListByAgent(ctx, agentID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list skill bindings")
	}
	// 计算可用工具集 (缺失工具展示用; 计算失败降级为全缺失提示)
	var available map[string]bool
	if mcpBs, bErr := s.bindings.ListByAgent(ctx, agentID); bErr == nil {
		mcpIDs := make([]string, 0, len(mcpBs))
		for _, b := range mcpBs {
			mcpIDs = append(mcpIDs, b.MCPID)
		}
		var cfg AgentConfig
		_ = json.Unmarshal(agent.Config, &cfg)
		available, _ = s.availableToolNames(ctx, mcpIDs, cfg.Tools)
	}
	if available == nil {
		available = map[string]bool{}
	}
	views := make([]BoundSkillView, 0, len(bindings))
	for _, b := range bindings {
		skill, err := s.skillRepo.Get(ctx, b.SkillID)
		if err != nil {
			continue // 孤儿绑定 (技能已删除)
		}
		required := decodeSkillStringList(skill.RequiredTools)
		var missing []string
		for _, tool := range required {
			if !available[tool] {
				missing = append(missing, tool)
			}
		}
		views = append(views, BoundSkillView{
			ID:            skill.ID,
			Name:          skill.Name,
			Version:       skill.Version,
			Description:   skill.Description,
			Status:        skill.Status,
			RequiredTools: required,
			MissingTools:  missing,
		})
	}
	return views, nil
}

// UpdateAgentSkills 全量更新 Agent 技能绑定 (PUT /agents/:id/skills, M9)
func (s *agentService) UpdateAgentSkills(ctx context.Context, agentID string, skillIDs []string, operatorID string) error {
	agent, err := s.agents.GetByID(ctx, agentID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return errors.ErrNotFound
		}
		return errors.Wrap(err, "failed to get agent")
	}
	mcpBs, err := s.bindings.ListByAgent(ctx, agentID)
	if err != nil {
		return errors.Wrap(err, "failed to list mcp bindings")
	}
	mcpIDs := make([]string, 0, len(mcpBs))
	for _, b := range mcpBs {
		mcpIDs = append(mcpIDs, b.MCPID)
	}
	var cfg AgentConfig
	_ = json.Unmarshal(agent.Config, &cfg)
	if err := s.validateSkills(ctx, mcpIDs, cfg.Tools, skillIDs, cfg.SkillsUsageMode); err != nil {
		return err
	}
	return s.syncSkillBindings(ctx, agentID, skillIDs, operatorID)
}