package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ---------- 测试用假仓储 (审核功能) ----------

// fakeBindingsRepoForReview MCP 绑定假实现 (UpdateAgent 兜底读取绑定用)
type fakeBindingsRepoForReview struct{}

func (f *fakeBindingsRepoForReview) Bind(context.Context, string, string) error   { return nil }
func (f *fakeBindingsRepoForReview) Unbind(context.Context, string, string) error { return nil }
func (f *fakeBindingsRepoForReview) ListByMCP(context.Context, string) ([]model.MCPAgentBinding, error) {
	return nil, nil
}
func (f *fakeBindingsRepoForReview) ListByAgent(context.Context, string) ([]model.MCPAgentBinding, error) {
	return nil, nil
}
func (f *fakeBindingsRepoForReview) DeleteByMCP(context.Context, string) error { return nil }
func (f *fakeBindingsRepoForReview) DeleteByAgent(context.Context, string) error {
	return nil
}

// fakeAgentVersionRepoForReview Agent 版本快照假实现
type fakeAgentVersionRepoForReview struct {
	mu       sync.Mutex
	versions []*model.AgentVersion
}

func (f *fakeAgentVersionRepoForReview) Create(_ context.Context, v *model.AgentVersion) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versions = append(f.versions, v)
	return nil
}
func (f *fakeAgentVersionRepoForReview) ListByAgent(_ context.Context, _ string) ([]*model.AgentVersion, error) {
	return nil, nil
}
func (f *fakeAgentVersionRepoForReview) Get(_ context.Context, _ string, _ int) (*model.AgentVersion, error) {
	return nil, gorm.ErrRecordNotFound
}

// fakeWorkflowRepoForReview 工作流假实现 (List 支持 review_status 过滤)
type fakeWorkflowRepoForReview struct {
	mu    sync.Mutex
	items map[string]*model.Workflow
}

func newFakeWorkflowRepoForReview() *fakeWorkflowRepoForReview {
	return &fakeWorkflowRepoForReview{items: make(map[string]*model.Workflow)}
}
func (f *fakeWorkflowRepoForReview) add(w *model.Workflow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if w.ID == "" {
		w.ID = "wf-" + w.Name
	}
	f.items[w.ID] = w
}
func (f *fakeWorkflowRepoForReview) Create(_ context.Context, w *model.Workflow) error {
	f.add(w)
	return nil
}
func (f *fakeWorkflowRepoForReview) Get(_ context.Context, id string) (*model.Workflow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.items[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return w, nil
}
func (f *fakeWorkflowRepoForReview) GetByName(_ context.Context, _ string) (*model.Workflow, error) {
	return nil, nil
}
func (f *fakeWorkflowRepoForReview) GetByWebhookToken(_ context.Context, _ string) (*model.Workflow, error) {
	return nil, nil
}
func (f *fakeWorkflowRepoForReview) List(_ context.Context, filter repository.WorkflowListFilter) ([]model.Workflow, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]model.Workflow, 0, len(f.items))
	for _, w := range f.items {
		if filter.ReviewStatus != "" && w.ReviewStatus != filter.ReviewStatus {
			continue
		}
		if filter.Status != "" && w.Status != filter.Status {
			continue
		}
		items = append(items, *w)
	}
	return items, int64(len(items)), nil
}
func (f *fakeWorkflowRepoForReview) Update(_ context.Context, w *model.Workflow) error {
	f.add(w)
	return nil
}
func (f *fakeWorkflowRepoForReview) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, id)
	return nil
}
func (f *fakeWorkflowRepoForReview) ListScheduled(context.Context) ([]model.Workflow, error) {
	return nil, nil
}
func (f *fakeWorkflowRepoForReview) HasActiveExecutions(context.Context, string) (bool, error) {
	return false, nil
}

// fakeWorkflowVersionRepoForReview 版本快照假实现
type fakeWorkflowVersionRepoForReview struct{}

func (f *fakeWorkflowVersionRepoForReview) Create(context.Context, *model.WorkflowVersion) error {
	return nil
}
func (f *fakeWorkflowVersionRepoForReview) ListByWorkflow(context.Context, string) ([]model.WorkflowVersion, error) {
	return nil, nil
}

// fakeWorkflowExecutionRepoForReview 执行记录假实现
type fakeWorkflowExecutionRepoForReview struct{}

func (f *fakeWorkflowExecutionRepoForReview) Create(context.Context, *model.WorkflowExecution) error {
	return nil
}
func (f *fakeWorkflowExecutionRepoForReview) Get(context.Context, string) (*model.WorkflowExecution, error) {
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeWorkflowExecutionRepoForReview) Update(context.Context, *model.WorkflowExecution) error {
	return nil
}
func (f *fakeWorkflowExecutionRepoForReview) MarkFinished(context.Context, string, string, string, datatypes.JSON, datatypes.JSON, time.Time) (bool, error) {
	return false, nil
}
func (f *fakeWorkflowExecutionRepoForReview) List(context.Context, repository.ExecutionListFilter) ([]model.WorkflowExecution, int64, error) {
	return nil, 0, nil
}
func (f *fakeWorkflowExecutionRepoForReview) ListActive(context.Context) ([]model.WorkflowExecution, error) {
	return nil, nil
}
func (f *fakeWorkflowExecutionRepoForReview) CountsByStatus(context.Context) (map[string]int64, error) {
	return map[string]int64{}, nil
}
func (f *fakeWorkflowExecutionRepoForReview) Recent(context.Context, int) ([]model.WorkflowExecution, error) {
	return nil, nil
}

// fakeWorkflowNodeExecRepoForReview 节点执行假实现
type fakeWorkflowNodeExecRepoForReview struct{}

func (f *fakeWorkflowNodeExecRepoForReview) CreateBatch(context.Context, []model.WorkflowNodeExecution) error {
	return nil
}
func (f *fakeWorkflowNodeExecRepoForReview) ListByExecution(context.Context, string) ([]model.WorkflowNodeExecution, error) {
	return nil, nil
}
func (f *fakeWorkflowNodeExecRepoForReview) Get(context.Context, string) (*model.WorkflowNodeExecution, error) {
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeWorkflowNodeExecRepoForReview) Update(context.Context, *model.WorkflowNodeExecution) error {
	return nil
}
func (f *fakeWorkflowNodeExecRepoForReview) ListWaitingByExecution(context.Context, string) ([]model.WorkflowNodeExecution, error) {
	return nil, nil
}
func (f *fakeWorkflowNodeExecRepoForReview) MarkAll(context.Context, string, string) error {
	return nil
}

// ---------- 用例 ----------

// 新建/修改 Agent 后必须进入审核中
func TestAgentCreateAndUpdateResetReviewStatus(t *testing.T) {
	ctx := context.Background()
	agents := newFakeAgentRepo()
	svc := &agentService{
		agents:   agents,
		bindings: &fakeBindingsRepoForReview{},
		versions: &fakeAgentVersionRepoForReview{},
	}

	created, err := svc.CreateAgent(ctx, CreateAgentRequest{Name: "审核测试", Model: "gpt-test"}, "op-1")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if created.ReviewStatus != model.AgentReviewPending {
		t.Fatalf("新建后审核状态应为 pending, 实际 %s", created.ReviewStatus)
	}

	updated, err := svc.UpdateAgent(ctx, created.ID, UpdateAgentRequest{
		Name: "审核测试", Description: "改一下", Model: "gpt-test",
	}, "op-2")
	if err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	if updated.ReviewStatus != model.AgentReviewPending {
		t.Fatalf("修改后审核状态应回到 pending, 实际 %s", updated.ReviewStatus)
	}
}

// 审核门: 仅 approved 可被调用
func TestAssertAgentUsable(t *testing.T) {
	cases := []struct {
		status   string
		wantErr  bool
		errChunk string
	}{
		{model.AgentReviewApproved, false, ""},
		{model.AgentReviewPending, true, "审核中"},
		{model.AgentReviewRejected, true, "驳回"},
	}
	for _, c := range cases {
		agent := &model.Agent{ID: "a1", Name: "a", ReviewStatus: c.status}
		err := assertAgentUsable(agent)
		if c.wantErr {
			if err == nil {
				t.Fatalf("status=%s 应拒绝", c.status)
			}
			if !strings.Contains(err.Error(), c.errChunk) {
				t.Fatalf("status=%s 错误文案应含 %q, 实际 %q", c.status, c.errChunk, err.Error())
			}
		} else if err != nil {
			t.Fatalf("status=%s 应放行, 实际 %v", c.status, err)
		}
	}
}

// 审核决策: 通过/驳回 状态流转 + 重复操作拦截
func TestReviewServiceAgentDecisions(t *testing.T) {
	ctx := context.Background()
	agents := newFakeAgentRepo()
	agents.add(&model.Agent{ID: "a-pending", Name: "待审核", ReviewStatus: model.AgentReviewPending})
	agents.add(&model.Agent{ID: "a-approved", Name: "已通过", ReviewStatus: model.AgentReviewApproved})
	audits := newFakeKBAuditRepo()
	svc := NewReviewService(agents, newFakeWorkflowRepoForReview(), audits)

	// pending -> approve
	agent, err := svc.ReviewAgent(ctx, "a-pending", ReviewDecisionApprove, "looks good", "admin-1", "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if agent.ReviewStatus != model.AgentReviewApproved || agent.ReviewedBy == nil || *agent.ReviewedBy != "admin-1" || agent.ReviewedAt == nil {
		t.Fatalf("approve 后状态/审核信息不正确: %+v", agent)
	}

	// approved 不可重复审核
	if _, err := svc.ReviewAgent(ctx, "a-approved", ReviewDecisionApprove, "", "admin-1", "admin", "127.0.0.1"); err == nil {
		t.Fatal("已通过的 agent 不应允许重复审核")
	}

	// pending -> reject (带驳回原因)
	agents.add(&model.Agent{ID: "a-reject", Name: "将被驳回", ReviewStatus: model.AgentReviewPending})
	agent, err = svc.ReviewAgent(ctx, "a-reject", ReviewDecisionReject, "提示词不合规", "admin-1", "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if agent.ReviewStatus != model.AgentReviewRejected || agent.ReviewComment == nil || *agent.ReviewComment != "提示词不合规" {
		t.Fatalf("reject 后状态/驳回原因不正确: %+v", agent)
	}

	// rejected 不可再 reject, 但可以 approve (纠正误驳回)
	if _, err := svc.ReviewAgent(ctx, "a-reject", ReviewDecisionReject, "", "admin-1", "admin", "127.0.0.1"); err == nil {
		t.Fatal("已驳回的 agent 不应允许再次驳回")
	}
	agent, err = svc.ReviewAgent(ctx, "a-reject", ReviewDecisionApprove, "", "admin-1", "admin", "127.0.0.1")
	if err != nil || agent.ReviewStatus != model.AgentReviewApproved {
		t.Fatalf("误驳回后应允许重新通过: err=%v status=%s", err, agent.ReviewStatus)
	}

	// 审计日志
	actions := audits.actions()
	if len(actions) != 3 || actions[0] != "agent.review.approve" || actions[1] != "agent.review.reject" || actions[2] != "agent.review.approve" {
		t.Fatalf("审计日志不符: %v", actions)
	}
}

// 工作流审核决策 + 待审队列
func TestReviewServiceWorkflowDecisionsAndQueue(t *testing.T) {
	ctx := context.Background()
	agents := newFakeAgentRepo()
	wfs := newFakeWorkflowRepoForReview()
	wfs.add(&model.Workflow{ID: "w-pending", Name: "wf1", Status: model.WorkflowStatusActive, ReviewStatus: model.WorkflowReviewPending})
	wfs.add(&model.Workflow{ID: "w-approved", Name: "wf2", Status: model.WorkflowStatusActive, ReviewStatus: model.WorkflowReviewApproved})
	audits := newFakeKBAuditRepo()
	svc := NewReviewService(agents, wfs, audits)

	if _, total, err := svc.ListPendingWorkflows(ctx, repository.WorkflowListFilter{}); err != nil || total != 1 {
		t.Fatalf("待审工作流队列应为 1 条: total=%d err=%v", total, err)
	}

	wf, err := svc.ReviewWorkflow(ctx, "w-pending", ReviewDecisionReject, "缺少关键节点", "admin-1", "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if wf.ReviewStatus != model.WorkflowReviewRejected || wf.ReviewComment == nil {
		t.Fatalf("reject 后状态不正确: %+v", wf)
	}
	if _, total, _ := svc.ListPendingWorkflows(ctx, repository.WorkflowListFilter{}); total != 0 {
		t.Fatalf("驳回后应不在待审队列: total=%d", total)
	}
}

// 工作流触发: 审核中拦截 + agent 预检
func TestWorkflowTriggerReviewGate(t *testing.T) {
	ctx := context.Background()
	agentDef := datatypes.JSON(`{"version":1,"nodes":[{"id":"n1","type":"agent","name":"客服节点","config":{"agent_id":"agent-1","message":"你好"}}],"edges":[]}`)

	newSvc := func(wf *model.Workflow, agent *model.Agent) *workflowService {
		wfs := newFakeWorkflowRepoForReview()
		wfs.add(wf)
		agents := newFakeAgentRepo()
		if agent != nil {
			agents.add(agent)
		}
		return &workflowService{
			workflows:  wfs,
			versions:   &fakeWorkflowVersionRepoForReview{},
			executions: &fakeWorkflowExecutionRepoForReview{},
			nodes:      &fakeWorkflowNodeExecRepoForReview{},
			agents:     agents,
		}
	}

	// 1) 工作流自身审核中 -> 拒绝 (手工/定时/Webhook 均走 Trigger)
	svc := newSvc(
		&model.Workflow{ID: "wf-1", Name: "wf1", Status: model.WorkflowStatusActive, ReviewStatus: model.WorkflowReviewPending, Definition: agentDef},
		nil,
	)
	if _, err := svc.Trigger(ctx, "wf-1", nil, model.WorkflowTriggerManual, nil); err == nil || !strings.Contains(err.Error(), "工作流审核中") {
		t.Fatalf("审核中工作流应被拦截, err=%v", err)
	}

	// 2) 工作流已审核通过, 但引用审核中的 agent -> 拒绝且带 agent 名称
	svc = newSvc(
		&model.Workflow{ID: "wf-2", Name: "wf2", Status: model.WorkflowStatusActive, ReviewStatus: model.WorkflowReviewApproved, Definition: agentDef},
		&model.Agent{ID: "agent-1", Name: "客服A", ReviewStatus: model.AgentReviewPending},
	)
	if _, err := svc.Trigger(ctx, "wf-2", nil, model.WorkflowTriggerManual, nil); err == nil || !strings.Contains(err.Error(), "客服A") {
		t.Fatalf("含审核中 agent 的工作流应被拦截, err=%v", err)
	}

	// 3) 工作流与 agent 均审核通过 -> 预检放行
	svc = newSvc(
		&model.Workflow{ID: "wf-3", Name: "wf3", Status: model.WorkflowStatusActive, ReviewStatus: model.WorkflowReviewApproved, Definition: agentDef},
		&model.Agent{ID: "agent-1", Name: "客服A", ReviewStatus: model.AgentReviewApproved},
	)
	def, err := ParseDefinition(agentDef)
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if err := svc.checkAgentsUsable(ctx, def); err != nil {
		t.Fatalf("全部审核通过应放行, err=%v", err)
	}

	// 4) 工作流已驳回 -> 拒绝
	svc = newSvc(
		&model.Workflow{ID: "wf-4", Name: "wf4", Status: model.WorkflowStatusActive, ReviewStatus: model.WorkflowReviewRejected, Definition: agentDef},
		nil,
	)
	if _, err := svc.Trigger(ctx, "wf-4", nil, model.WorkflowTriggerCron, nil); err == nil || !strings.Contains(err.Error(), "驳回") {
		t.Fatalf("已驳回工作流应被拦截, err=%v", err)
	}
}

// 工作流新建/修改/调度变更 -> 回到审核中
func TestWorkflowCreateUpdateScheduleResetReviewStatus(t *testing.T) {
	ctx := context.Background()
	wfs := newFakeWorkflowRepoForReview()
	svc := NewWorkflowService(
		wfs,
		&fakeWorkflowVersionRepoForReview{},
		&fakeWorkflowExecutionRepoForReview{},
		&fakeWorkflowNodeExecRepoForReview{},
		newFakeAgentRepo(),
		nil,
	)

	def := datatypes.JSON(`{"version":1,"nodes":[{"id":"n1","type":"delay","name":"等待","config":{"seconds":1}}],"edges":[]}`)
	wf, err := svc.Create(ctx, CreateWorkflowRequest{Name: "wf-x", Definition: def}, "op-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wf.ReviewStatus != model.WorkflowReviewPending {
		t.Fatalf("新建工作流应为审核中, 实际 %s", wf.ReviewStatus)
	}

	// 无变化更新不触发重审
	updated, err := svc.Update(ctx, wf.ID, UpdateWorkflowRequest{}, "op-1")
	if err != nil {
		t.Fatalf("Update(无变化): %v", err)
	}
	if updated.ReviewStatus != model.WorkflowReviewPending {
		// 新建即是 pending, 无变化保持 pending
		t.Fatalf("无变化更新应保持原审核状态, 实际 %s", updated.ReviewStatus)
	}

	// 模拟已审核通过后修改 -> 回到审核中
	revSvc := NewReviewService(newFakeAgentRepo(), wfs, newFakeKBAuditRepo())
	if _, err := revSvc.ReviewWorkflow(ctx, wf.ID, ReviewDecisionApprove, "", "admin", "admin", "127.0.0.1"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	upd, err := svc.Update(ctx, wf.ID, UpdateWorkflowRequest{Description: ptrStr("改描述")}, "op-1")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.ReviewStatus != model.WorkflowReviewPending {
		t.Fatalf("修改后应回到审核中, 实际 %s", upd.ReviewStatus)
	}

	// 调度配置变更 -> 回到审核中
	if _, err := svc.UpdateSchedule(ctx, wf.ID, UpdateScheduleRequest{Enabled: true, Cron: "0 9 * * *"}); err != nil {
		t.Fatalf("UpdateSchedule: %v", err)
	}
	got, _ := wfs.Get(ctx, wf.ID)
	if got.ReviewStatus != model.WorkflowReviewPending {
		t.Fatalf("调度变更后应回到审核中, 实际 %s", got.ReviewStatus)
	}
}

func ptrStr(s string) *string { return &s }
