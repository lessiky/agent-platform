package service

import (
	"encoding/json"
	"context"
	"strings"
	"testing"
	"time"

	"agent-platform/internal/mcpclient"
	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"
	"agent-platform/internal/runtime"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
)

// fakeLogRepo Agent 执行日志仓储假实现 (防 execLog 空指针)
type fakeLogRepo struct {
	entries []*model.AgentLog
}

func (f *fakeLogRepo) Append(ctx context.Context, entries []*model.AgentLog) error {
	f.entries = append(f.entries, entries...)
	return nil
}

func (f *fakeLogRepo) List(ctx context.Context, filter repository.AgentLogFilter) ([]*model.AgentLog, int64, error) {
	return f.entries, int64(len(f.entries)), nil
}

func (f *fakeLogRepo) Trim(ctx context.Context, agentID string, keep int) error { return nil }

// fakeMCPForApproval MCP 服务假实现: approvalTools 中的工具返回待审核单, 其余返回正常结果
type fakeMCPForApproval struct {
	MCPServerService
	approvalTools map[string]string // 工具名 -> 审核单 ID
	calls         []string
}

func (f *fakeMCPForApproval) CallTool(ctx context.Context, id, tool string, arguments map[string]interface{}, opts CallOptions) (*CallToolOutcome, error) {
	f.calls = append(f.calls, tool)
	if aid, ok := f.approvalTools[tool]; ok {
		return &CallToolOutcome{PendingApproval: &model.ToolApproval{ID: aid, ToolName: tool}}, nil
	}
	return &CallToolOutcome{Result: &mcpclient.CallResult{
		Content: []mcpclient.ToolContent{{Type: "text", Text: "ok:" + tool}},
	}}, nil
}

// fakeModelForRounds 模型服务假实现: 第 N 次调用返回第 N 个预设结果
type fakeModelForRounds struct {
	ModelTemplateService
	rounds     []roundOutcome
	calls      int
	lastNTools int
}

type roundOutcome struct {
	content   string
	toolCalls []modelclient.ChatToolCall
}

func (f *fakeModelForRounds) RouteAndChat(ctx context.Context, agentID string, messages []modelclient.ChatMessage, tools []modelclient.ChatToolDef, gen modelclient.GenOptions) (*ChatOutcome, error) {
	f.calls++
	f.lastNTools = len(tools)
	var out roundOutcome
	if f.calls-1 < len(f.rounds) {
		out = f.rounds[f.calls-1]
	}
	return &ChatOutcome{Content: out.content, ToolCalls: out.toolCalls, TotalTokens: 1, Model: "fake", TemplateName: "fake"}, nil
}

func tcCall(id, name string) modelclient.ChatToolCall {
	tc := modelclient.ChatToolCall{ID: id, Type: "function"}
	tc.Function.Name = name
	return tc
}

// TestRunToolRoundsHaltOnApproval 审核门禁: 批次首个工具需人工审核时,
// 本轮应立即暂停 — 同批后续工具不执行, 不再发起后续模型轮, pending 携带审核单
func TestRunToolRoundsHaltOnApproval(t *testing.T) {
	mcp := &fakeMCPForApproval{approvalTools: map[string]string{"restart_service": "appr-1"}}
	modelSvc := &fakeModelForRounds{rounds: []roundOutcome{{content: "should not be used"}}}
	s := &chatService{logs: &fakeLogRepo{}, mcpSvc: mcp, modelSvc: modelSvc}

	toolIndex := map[string]toolRef{
		"restart_service": {MCPID: "mcp-1", MCPName: "ops"},
		"check_status":    {MCPID: "mcp-1", MCPName: "ops"},
	}
	var messages []modelclient.ChatMessage
	outcome := &ChatOutcome{ToolCalls: []modelclient.ChatToolCall{
		tcCall("call-1", "restart_service"),
		tcCall("call-2", "check_status"),
	}}
	var pending []runtime.PendingApproval
	var calls []MCPChatCall

	if _, err := s.runToolRounds(context.Background(), "agent-1", &messages, outcome, nil, toolIndex, modelclient.GenOptions{}, 5, "sess-1", "chat", model.ApprovalSourceChat, &pending, &calls, "exec-1", nil, nil); err != nil {
		t.Fatalf("runToolRounds: %v", err)
	}

	if strings.Join(mcp.calls, ",") != "restart_service" {
		t.Fatalf("executed tools = %v, want [restart_service] (check_status 不应执行)", mcp.calls)
	}
	if modelSvc.calls != 0 {
		t.Fatalf("model called %d times after halt, want 0", modelSvc.calls)
	}
	if len(pending) != 1 || pending[0].ApprovalID != "appr-1" || pending[0].ToolName != "restart_service" {
		t.Fatalf("pending = %+v, want one approval appr-1/restart_service", pending)
	}
	if len(calls) != 1 || calls[0].Status != "pending" {
		t.Fatalf("mcp calls = %+v, want one pending call", calls)
	}
	if len(messages) != 2 || !strings.Contains(messages[len(messages)-1].Content, "requires human approval") {
		t.Fatalf("messages = %+v, want assistant + pending tool msg", messages)
	}
}

// TestRunToolRoundsHaltMidBatch 审核门禁: 需审核工具位于批次中间时,
// 其前已执行工具保留, 其后工具不执行
func TestRunToolRoundsHaltMidBatch(t *testing.T) {
	mcp := &fakeMCPForApproval{approvalTools: map[string]string{"restart_service": "appr-2"}}
	modelSvc := &fakeModelForRounds{}
	s := &chatService{logs: &fakeLogRepo{}, mcpSvc: mcp, modelSvc: modelSvc}

	toolIndex := map[string]toolRef{
		"check_before":    {MCPID: "mcp-1", MCPName: "ops"},
		"restart_service": {MCPID: "mcp-1", MCPName: "ops"},
		"check_after":     {MCPID: "mcp-1", MCPName: "ops"},
	}
	var messages []modelclient.ChatMessage
	outcome := &ChatOutcome{ToolCalls: []modelclient.ChatToolCall{
		tcCall("call-1", "check_before"),
		tcCall("call-2", "restart_service"),
		tcCall("call-3", "check_after"),
	}}
	var pending []runtime.PendingApproval
	var calls []MCPChatCall

	if _, err := s.runToolRounds(context.Background(), "agent-1", &messages, outcome, nil, toolIndex, modelclient.GenOptions{}, 5, "sess-1", "chat", model.ApprovalSourceChat, &pending, &calls, "exec-1", nil, nil); err != nil {
		t.Fatalf("runToolRounds: %v", err)
	}

	if strings.Join(mcp.calls, ",") != "check_before,restart_service" {
		t.Fatalf("executed tools = %v, want [check_before restart_service] (check_after 不应执行)", mcp.calls)
	}
	if modelSvc.calls != 0 {
		t.Fatalf("model called %d times after halt, want 0", modelSvc.calls)
	}
	if len(pending) != 1 || pending[0].ApprovalID != "appr-2" {
		t.Fatalf("pending = %+v", pending)
	}
}

// TestRunToolRoundsNoApprovalUnchanged 无审核工具时轮循环行为不变:
// 工具正常执行, 模型继续下一轮直至给出终答
func TestRunToolRoundsNoApprovalUnchanged(t *testing.T) {
	mcp := &fakeMCPForApproval{}
	modelSvc := &fakeModelForRounds{rounds: []roundOutcome{{content: "done"}}}
	s := &chatService{logs: &fakeLogRepo{}, mcpSvc: mcp, modelSvc: modelSvc}

	toolIndex := map[string]toolRef{"check_status": {MCPID: "mcp-1", MCPName: "ops"}}
	var messages []modelclient.ChatMessage
	outcome := &ChatOutcome{ToolCalls: []modelclient.ChatToolCall{tcCall("call-1", "check_status")}}
	var pending []runtime.PendingApproval
	var calls []MCPChatCall

	if _, err := s.runToolRounds(context.Background(), "agent-1", &messages, outcome, nil, toolIndex, modelclient.GenOptions{}, 5, "sess-1", "chat", model.ApprovalSourceChat, &pending, &calls, "exec-1", nil, nil); err != nil {
		t.Fatalf("runToolRounds: %v", err)
	}

	if strings.Join(mcp.calls, ",") != "check_status" {
		t.Fatalf("executed tools = %v, want [check_status]", mcp.calls)
	}
	if modelSvc.calls != 1 {
		t.Fatalf("model called %d times, want 1", modelSvc.calls)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %+v, want none", pending)
	}
	if len(calls) != 1 || calls[0].Status != "ok" {
		t.Fatalf("mcp calls = %+v, want one ok call", calls)
	}
	if outcome.Content != "done" {
		t.Fatalf("outcome.Content = %q, want done", outcome.Content)
	}
}

// fakeExecFinish FinishByApproval 调用记录
type fakeExecFinish struct {
	approvalID string
	status     string
	errMsg     string
	result     datatypes.JSON
}

// fakeExecMark MarkWaitingApproval 调用记录
type fakeExecMark struct {
	id      string
	pending datatypes.JSON
	result  datatypes.JSON
	stage   string
}

// fakeExecRepoForApproval 执行任务仓储假实现: 返回预设的等待审核任务, 记录终态/等待审核写入
type fakeExecRepoForApproval struct {
	repository.AgentExecutionRepository // 其余方法未实现, 测试不应触达
	exec     *model.AgentExecution
	finished []fakeExecFinish
	marked   []fakeExecMark
}

func (f *fakeExecRepoForApproval) GetByApprovalID(ctx context.Context, approvalID string) (*model.AgentExecution, error) {
	if f.exec != nil {
		return f.exec, nil
	}
	return nil, errors.ErrNotFound
}

func (f *fakeExecRepoForApproval) FinishByApproval(ctx context.Context, approvalID, status, errMsg string, result datatypes.JSON) error {
	f.finished = append(f.finished, fakeExecFinish{approvalID: approvalID, status: status, errMsg: errMsg, result: result})
	return nil
}

func (f *fakeExecRepoForApproval) MarkWaitingApproval(ctx context.Context, id string, pendingApprovals, result datatypes.JSON, stage string) error {
	f.marked = append(f.marked, fakeExecMark{id: id, pending: pendingApprovals, result: result, stage: stage})
	return nil
}

// fakeMsgRepoForApproval 消息仓储假实现: 记录 Append
type fakeMsgRepoForApproval struct {
	repository.ChatMessageRepository
	appended [][]*model.ChatMessage
}

func (f *fakeMsgRepoForApproval) Append(ctx context.Context, msgs []*model.ChatMessage) error {
	f.appended = append(f.appended, msgs)
	return nil
}

// fakeSessRepoForApproval 会话仓储假实现: TouchLastMessage 空操作
type fakeSessRepoForApproval struct {
	repository.ChatSessionRepository
}

func (f *fakeSessRepoForApproval) TouchLastMessage(ctx context.Context, id string) error { return nil }

// TestCompleteExecutionsByApprovalIncludesPreReviewCalls 审核决策回填终态时,
// result 应携带审核前 (命中门禁轮) 的工具调用明细 pre_review_mcp_calls
func TestCompleteExecutionsByApprovalIncludesPreReviewCalls(t *testing.T) {
	intermediate, _ := json.Marshal(map[string]interface{}{
		"reply": "工具 restart_service 已提交人工审核, 审核通过后将继续执行。",
		"mcp_calls": []MCPChatCall{
			{MCPName: "ops", ToolName: "get_alerts", Status: "ok", LatencyMs: 22},
			{MCPName: "ops", ToolName: "restart_service", Status: "pending", Detail: "approval_id=appr-9"},
		},
	})
	repo := &fakeExecRepoForApproval{
		exec: &model.AgentExecution{ID: "exec-1", Result: datatypes.JSON(intermediate)},
	}
	s := &chatService{logs: &fakeLogRepo{}, executions: repo}

	approval := &model.ToolApproval{ID: "appr-9", ToolName: "restart_service", Status: model.ApprovalStatusApproved}
	chatResult := ChatResult{SessionID: "sess-1", Reply: "ok", MCPCalls: []MCPChatCall{
		{MCPName: "ops", ToolName: "get_alerts", Status: "ok", LatencyMs: 10},
	}}
	s.completeExecutionsByApproval(context.Background(), approval, chatResult)

	if len(repo.finished) != 1 {
		t.Fatalf("FinishByApproval called %d times, want 1", len(repo.finished))
	}
	f := repo.finished[0]
	if f.status != model.AgentExecutionStatusSuccess {
		t.Fatalf("status = %s, want success", f.status)
	}
	var payload struct {
		ApprovalID        string        `json:"approval_id"`
		ApprovalStatus    string        `json:"approval_status"`
		MCPCalls          []MCPChatCall `json:"mcp_calls"`
		PreReviewMCPCalls []MCPChatCall `json:"pre_review_mcp_calls"`
	}
	if err := json.Unmarshal(f.result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.ApprovalID != "appr-9" || payload.ApprovalStatus != model.ApprovalStatusApproved {
		t.Fatalf("approval fields = %q/%q", payload.ApprovalID, payload.ApprovalStatus)
	}
	if len(payload.PreReviewMCPCalls) != 2 || payload.PreReviewMCPCalls[1].Status != "pending" {
		t.Fatalf("pre_review_mcp_calls = %+v, want 2 calls incl. pending", payload.PreReviewMCPCalls)
	}
	if len(payload.MCPCalls) != 1 || payload.MCPCalls[0].ToolName != "get_alerts" {
		t.Fatalf("mcp_calls = %+v, want continuation calls only", payload.MCPCalls)
	}
}

// TestMarkWaitingForNewApprovalsAccumulatesApprovals 续答轮命中新审核门禁时:
// 任务重回等待审核, 审核单列表累积 (保留前轮审核单), 中间结果合并前轮工具调用明细
func TestMarkWaitingForNewApprovalsAccumulatesApprovals(t *testing.T) {
	prevResult, _ := json.Marshal(map[string]interface{}{
		"mcp_calls": []MCPChatCall{
			{MCPName: "ops", ToolName: "get_alerts", Status: "ok"},
			{MCPName: "ops", ToolName: "restart_service", Status: "pending", Detail: "approval_id=appr-A"},
		},
	})
	prevPending, _ := json.Marshal([]string{"appr-A"})
	repo := &fakeExecRepoForApproval{
		exec: &model.AgentExecution{ID: "exec-1", PendingApprovals: datatypes.JSON(prevPending), Result: datatypes.JSON(prevResult)},
	}
	msgRepo := &fakeMsgRepoForApproval{}
	s := &chatService{logs: &fakeLogRepo{}, executions: repo, messages: msgRepo, sessions: &fakeSessRepoForApproval{}}

	approval := &model.ToolApproval{ID: "appr-A", ToolName: "restart_service"}
	pending := []runtime.PendingApproval{{ApprovalID: "appr-B", ToolName: "notify"}}
	mcpCalls := []MCPChatCall{{MCPName: "ops", ToolName: "notify", Status: "pending", Detail: "approval_id=appr-B"}}
	outcome := &ChatOutcome{Model: "fake", TemplateName: "fake", TotalTokens: 5}

	s.markWaitingForNewApprovals(context.Background(), approval, &model.ChatSession{ID: "sess-1"}, "agent-1",
		&pending, &mcpCalls, outcome, nil, time.Now().Add(-time.Second), 3)

	if len(repo.marked) != 1 {
		t.Fatalf("MarkWaitingApproval called %d times, want 1", len(repo.marked))
	}
	m := repo.marked[0]
	if m.id != "exec-1" {
		t.Fatalf("marked id = %s, want exec-1", m.id)
	}
	var pendingIDs []string
	if err := json.Unmarshal(m.pending, &pendingIDs); err != nil || strings.Join(pendingIDs, ",") != "appr-A,appr-B" {
		t.Fatalf("pending_approvals = %s, want [appr-A appr-B]", string(m.pending))
	}
	var intermediate struct {
		MCPCalls []MCPChatCall `json:"mcp_calls"`
	}
	if err := json.Unmarshal(m.result, &intermediate); err != nil {
		t.Fatalf("unmarshal intermediate: %v", err)
	}
	if len(intermediate.MCPCalls) != 3 || intermediate.MCPCalls[2].ToolName != "notify" {
		t.Fatalf("intermediate mcp_calls = %+v, want prev 2 + notify", intermediate.MCPCalls)
	}
	if len(msgRepo.appended) != 1 || len(msgRepo.appended[0]) != 1 || !strings.Contains(msgRepo.appended[0][0].Content, "已提交人工审核") {
		t.Fatalf("persisted messages = %+v, want re-waiting reply", msgRepo.appended)
	}
}
