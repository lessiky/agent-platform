package service

import (
	"context"
	"encoding/json"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
)

// 审核决策
const (
	ReviewDecisionApprove = "approve"
	ReviewDecisionReject  = "reject"
)

// ReviewService Agent/工作流审核服务 (审核中 -> 通过/驳回, 仅管理员权限可操作)
type ReviewService interface {
	// ListPendingAgents 审核中 Agent 列表 (审核中心)
	ListPendingAgents(ctx context.Context, filter repository.AgentListFilter) ([]model.Agent, int64, error)
	// ListPendingWorkflows 审核中工作流列表 (审核中心)
	ListPendingWorkflows(ctx context.Context, filter repository.WorkflowListFilter) ([]model.Workflow, int64, error)
	// ReviewAgent 审核 Agent (approve/reject), 落库审核人/时间/意见并写审计日志
	ReviewAgent(ctx context.Context, id, decision, comment, operatorID, operatorName, ip string) (*model.Agent, error)
	// ReviewWorkflow 审核工作流 (approve/reject), 落库审核人/时间/意见并写审计日志
	ReviewWorkflow(ctx context.Context, id, decision, comment, operatorID, operatorName, ip string) (*model.Workflow, error)
}

type reviewService struct {
	agents    repository.AgentRepository
	workflows repository.WorkflowRepository
	audits    repository.AuditLogRepository
}

// NewReviewService 创建审核服务
func NewReviewService(
	agents repository.AgentRepository,
	workflows repository.WorkflowRepository,
	audits repository.AuditLogRepository,
) ReviewService {
	return &reviewService{agents: agents, workflows: workflows, audits: audits}
}

func (s *reviewService) ListPendingAgents(ctx context.Context, filter repository.AgentListFilter) ([]model.Agent, int64, error) {
	filter.ReviewStatus = model.AgentReviewPending
	agents, total, err := s.agents.List(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	result := make([]model.Agent, 0, len(agents))
	for i := range agents {
		result = append(result, *agents[i])
	}
	return result, total, nil
}

func (s *reviewService) ListPendingWorkflows(ctx context.Context, filter repository.WorkflowListFilter) ([]model.Workflow, int64, error) {
	filter.ReviewStatus = model.WorkflowReviewPending
	return s.workflows.List(ctx, filter)
}

func (s *reviewService) ReviewAgent(ctx context.Context, id, decision, comment, operatorID, operatorName, ip string) (*model.Agent, error) {
	agent, err := s.agents.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := validateReviewState(agent.ReviewStatus, decision, "Agent"); err != nil {
		return nil, err
	}
	if decision == ReviewDecisionApprove {
		agent.ReviewStatus = model.AgentReviewApproved
	} else {
		agent.ReviewStatus = model.AgentReviewRejected
	}
	applyReviewInfo(&agent.ReviewedBy, &agent.ReviewedAt, &agent.ReviewComment, operatorID, comment)
	if err := s.agents.Update(ctx, agent); err != nil {
		return nil, errors.Wrap(err, "failed to update agent review status")
	}
	s.auditReview(ctx, operatorID, operatorName, "agent", agent.ID, decision, comment, map[string]interface{}{"version": agent.Version}, ip)
	return agent, nil
}

func (s *reviewService) ReviewWorkflow(ctx context.Context, id, decision, comment, operatorID, operatorName, ip string) (*model.Workflow, error) {
	workflow, err := s.workflows.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := validateReviewState(workflow.ReviewStatus, decision, "工作流"); err != nil {
		return nil, err
	}
	if decision == ReviewDecisionApprove {
		workflow.ReviewStatus = model.WorkflowReviewApproved
	} else {
		workflow.ReviewStatus = model.WorkflowReviewRejected
	}
	applyReviewInfo(&workflow.ReviewedBy, &workflow.ReviewedAt, &workflow.ReviewComment, operatorID, comment)
	if err := s.workflows.Update(ctx, workflow); err != nil {
		return nil, errors.Wrap(err, "failed to update workflow review status")
	}
	s.auditReview(ctx, operatorID, operatorName, "workflow", workflow.ID, decision, comment, map[string]interface{}{"version": workflow.Version}, ip)
	return workflow, nil
}

// validateReviewState 校验审核操作的状态流转:
// approve: pending/rejected 可 (rejected 允许重新通过以纠正误驳回), approved 为终态;
// reject: 仅 pending 可
func validateReviewState(current, decision, resourceLabel string) error {
	switch decision {
	case ReviewDecisionApprove:
		if current == model.AgentReviewApproved || current == model.WorkflowReviewApproved {
			return errors.NewValidationError(resourceLabel + " 已审核通过, 无需重复审核")
		}
		return nil
	case ReviewDecisionReject:
		if current != model.AgentReviewPending && current != model.WorkflowReviewPending {
			return errors.NewValidationError("仅审核中的" + resourceLabel + "可驳回")
		}
		return nil
	default:
		return errors.NewValidationError("无效的审核决策 (仅支持 approve/reject): " + decision)
	}
}

// applyReviewInfo 回填审核人/时间/意见 (无意见时留空)
func applyReviewInfo(reviewedBy **string, reviewedAt **time.Time, reviewComment **string, operatorID, comment string) {
	*reviewedBy = strPtr(operatorID)
	now := time.Now()
	*reviewedAt = &now
	comment = trimSpace(comment)
	if comment == "" {
		*reviewComment = nil
		return
	}
	*reviewComment = strPtr(comment)
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// auditReview 写审计日志 (失败仅记日志, 不影响审核主流程)
func (s *reviewService) auditReview(ctx context.Context, operatorID, operatorName, resource, resourceID, decision, comment string, extra map[string]interface{}, ip string) {
	detail := map[string]interface{}{
		"decision": decision,
		"comment":  comment,
	}
	for k, v := range extra {
		detail[k] = v
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		payload = []byte("{}")
	}
	entry := &model.AuditLog{
		UserID:     strPtr(operatorID),
		Username:   operatorName,
		Action:     resource + ".review." + decision,
		Resource:   resource,
		ResourceID: strPtr(resourceID),
		Detail:     datatypes.JSON(payload),
		IP:         ip,
	}
	if err := s.audits.Append(ctx, entry); err != nil {
		_ = err // 审计失败不阻断审核
	}
}
