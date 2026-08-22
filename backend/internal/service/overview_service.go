package service

import (
	"context"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
)

// AgentOverviewStats Agent 基本状态
type AgentOverviewStats struct {
	Total   int64 `json:"total"`
	Running int64 `json:"running"`
	Stopped int64 `json:"stopped"`
	Idle    int64 `json:"idle"`
	Error   int64 `json:"error"`
}

// MCPOverviewStats MCP 基本状态
// Normal = connected; Abnormal = 其余 (disconnected / error / pending)
type MCPOverviewStats struct {
	Total      int64 `json:"total"`
	Normal     int64 `json:"normal"`
	Abnormal   int64 `json:"abnormal"`
	ToolsTotal int64 `json:"tools_total"`
}

// ModelOverviewStats 模型基本状态
// Available = active; Abnormal = error (inactive 为手动停用, 不计入两者)
type ModelOverviewStats struct {
	Total     int64 `json:"total"`
	Available int64 `json:"available"`
	Abnormal  int64 `json:"abnormal"`
}

// WorkflowOverviewStats 工作流基本状态
type WorkflowOverviewStats struct {
	Active   int64 `json:"active"`
	Draft    int64 `json:"draft"`
	Archived int64 `json:"archived"`
}

// ApprovalOverviewStats 审核基本状态
// Reviewed = 已处置 (approved / rejected / expired)
type ApprovalOverviewStats struct {
	Total    int64 `json:"total"`
	Pending  int64 `json:"pending"`
	Reviewed int64 `json:"reviewed"`
}

// SkillOverviewStats 技能基本状态
// Active = active (启用); Disabled = disabled (禁用)
type SkillOverviewStats struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	Disabled int64 `json:"disabled"`
}

// OverviewSummary 概览页 (基本情况) 统计
type OverviewSummary struct {
	Agents    AgentOverviewStats    `json:"agents"`
	MCPS      MCPOverviewStats      `json:"mcps"`
	Models    ModelOverviewStats    `json:"models"`
	Workflows WorkflowOverviewStats `json:"workflows"`
	Approvals ApprovalOverviewStats `json:"approvals"`
	Skills    SkillOverviewStats    `json:"skills"`
}

// OverviewService 概览统计服务
type OverviewService interface {
	Summary(ctx context.Context) (*OverviewSummary, error)
}

type overviewService struct {
	repo repository.OverviewRepository
}

func NewOverviewService(repo repository.OverviewRepository) OverviewService {
	return &overviewService{repo: repo}
}

func (s *overviewService) Summary(ctx context.Context) (*OverviewSummary, error) {
	agentCounts, err := s.repo.AgentStatusCounts(ctx)
	if err != nil {
		return nil, err
	}
	mcpCounts, err := s.repo.MCPStatusCounts(ctx)
	if err != nil {
		return nil, err
	}
	toolsTotal, err := s.repo.MCPToolsTotal(ctx)
	if err != nil {
		return nil, err
	}
	modelCounts, err := s.repo.ModelStatusCounts(ctx)
	if err != nil {
		return nil, err
	}
	workflowCounts, err := s.repo.WorkflowStatusCounts(ctx)
	if err != nil {
		return nil, err
	}
	approvalCounts, err := s.repo.ApprovalStatusCounts(ctx)
	if err != nil {
		return nil, err
	}
	skillCounts, err := s.repo.SkillStatusCounts(ctx)
	if err != nil {
		return nil, err
	}

	summary := &OverviewSummary{
		Agents: AgentOverviewStats{
			Total:   sumCounts(agentCounts),
			Running: agentCounts[model.AgentStatusRunning],
			Stopped: agentCounts[model.AgentStatusStopped],
			Idle:    agentCounts[model.AgentStatusIdle],
			Error:   agentCounts[model.AgentStatusError],
		},
		MCPS: MCPOverviewStats{
			Total:      sumCounts(mcpCounts),
			Normal:     mcpCounts[model.MCPStatusConnected],
			ToolsTotal: toolsTotal,
		},
		Models: ModelOverviewStats{
			Total:     sumCounts(modelCounts),
			Available: modelCounts[model.ModelStatusActive],
			Abnormal:  modelCounts[model.ModelStatusError],
		},
		Workflows: WorkflowOverviewStats{
			Active:   workflowCounts[model.WorkflowStatusActive],
			Draft:    workflowCounts[model.WorkflowStatusDraft],
			Archived: workflowCounts[model.WorkflowStatusArchived],
		},
		Approvals: ApprovalOverviewStats{
			Total:   sumCounts(approvalCounts),
			Pending: approvalCounts[model.ApprovalStatusPending],
		},
		Skills: SkillOverviewStats{
			Total:    sumCounts(skillCounts),
			Active:   skillCounts[model.SkillStatusActive],
			Disabled: skillCounts[model.SkillStatusDisabled],
		},
	}
	summary.MCPS.Abnormal = summary.MCPS.Total - summary.MCPS.Normal
	summary.Approvals.Reviewed = summary.Approvals.Total - summary.Approvals.Pending
	return summary, nil
}

func sumCounts(counts map[string]int64) int64 {
	var total int64
	for _, v := range counts {
		total += v
	}
	return total
}
