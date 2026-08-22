package service

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "sync"
    "time"

    "agent-platform/internal/mcpclient"
    "agent-platform/internal/model"
    "agent-platform/internal/repository"
    "agent-platform/pkg/errors"
)

// ApprovalTimeoutScanInterval 审核超时扫描间隔
const ApprovalTimeoutScanInterval = 30 * time.Second

// ToolApprovalConfig 单工具审核配置 (PUT /mcp-servers/:id/tools)
type ToolApprovalConfig struct {
    Name             string `json:"name" binding:"required,max=128"`
    RequiresApproval bool   `json:"requires_approval"`
}

// UpdateToolApprovalsRequest 工具级审核配置更新 (增量: 仅更新的工具, 其余保持不变)
type UpdateToolApprovalsRequest struct {
    Tools []ToolApprovalConfig `json:"tools" binding:"required,min=1,max=100"`
}

// UpdateApprovalSettingsRequest 审核全局配置更新 (字段为 nil/空 表示保持不变)
type UpdateApprovalSettingsRequest struct {
    DefaultTimeoutMinutes *int   `json:"default_timeout_minutes" binding:"omitempty,min=1,max=1440"`
    OnTimeout             string `json:"on_timeout" binding:"omitempty,oneof=reject approve"`
}

// CreateApprovalRequest 创建审核请求 (调用链内部使用)
type CreateApprovalRequest struct {
    MCPServerID string
    ToolName    string
    AgentID     *string
    Source      string // manual / runtime / api_invoke / workflow
    WorkflowExecutionID *string // 工作流执行 ID (source=workflow 时必填, M5)
    ChatSessionID   *string // 对话会话 ID (source=chat 时记录, 决策后恢复会话)
    Arguments   map[string]interface{}
}

// MCPToolExecutor 绕过审核门禁直接执行 MCP 工具 (审核通过后调用), 由 MCP 服务实现
type MCPToolExecutor interface {
    ExecuteTool(ctx context.Context, mcpID, tool string, arguments map[string]interface{}) (*mcpclient.CallResult, error)
}

// ApprovalService MCP 工具调用人工审核 (M4.5, PRD 2.2.4)
type ApprovalService interface {
    // 审核全局配置
    GetSettings(ctx context.Context) (*model.ApprovalSettings, error)
    UpdateSettings(ctx context.Context, req UpdateApprovalSettingsRequest, operatorID, username, ip string) (*model.ApprovalSettings, error)

    // 工具级审核配置 (重新发现工具时由 MCP 服务合并保留)
    UpdateToolApprovals(ctx context.Context, mcpID string, req UpdateToolApprovalsRequest, operatorID, username, ip string) ([]model.MCPTool, error)

    // 审核请求生命周期
    CreateRequest(ctx context.Context, req CreateApprovalRequest) (*model.ToolApproval, error)
    Get(ctx context.Context, id string) (*model.ToolApproval, error)
    List(ctx context.Context, filter repository.ApprovalListFilter) ([]ApprovalView, int64, error)
    CountPendingByMCP(ctx context.Context, mcpID string) (int64, error)
    Approve(ctx context.Context, id, operatorID, username, ip, comment string) (*model.ToolApproval, error)
    Reject(ctx context.Context, id, operatorID, username, ip, comment string) (*model.ToolApproval, error)
    ProcessExpired(ctx context.Context) (int, error)

    // SetDecisionHook 注入决策后钩子 (M5: 工作流审核节点恢复), 避免构造循环
    SetDecisionHook(hook ApprovalDecisionHook)
}

// ApprovalDecisionHook 审核决策完成 (通过/驳回/超时) 后的回调
// 参数为决策后的审核记录 (已含 result); source=workflow 的决策会触发工作流恢复
 type ApprovalDecisionHook = func(ctx context.Context, approval *model.ToolApproval)

// ApprovalView 审核请求视图 (附加关联名称, 供列表/详情展示)
type ApprovalView struct {
    model.ToolApproval
    MCPName   string `json:"mcp_name"`
    AgentName string `json:"agent_name,omitempty"`
}

type approvalService struct {
    approvals repository.ToolApprovalRepository
    settings  repository.ApprovalSettingsRepository
    audits    repository.AuditLogRepository
    servers   repository.MCPServerRepository
    agents    repository.AgentRepository
    executor  MCPToolExecutor

    hookMu    sync.RWMutex
    decisionHook ApprovalDecisionHook
}

func NewApprovalService(
    approvals repository.ToolApprovalRepository,
    settings repository.ApprovalSettingsRepository,
    audits repository.AuditLogRepository,
    servers repository.MCPServerRepository,
    agents repository.AgentRepository,
    executor MCPToolExecutor,
) ApprovalService {
    return &approvalService{
        approvals: approvals,
        settings:  settings,
        audits:    audits,
        servers:   servers,
        agents:    agents,
        executor:  executor,
    }
}

// ---------- 审核全局配置 ----------

func (s *approvalService) GetSettings(ctx context.Context) (*model.ApprovalSettings, error) {
    return s.settings.Get(ctx)
}

func (s *approvalService) UpdateSettings(ctx context.Context, req UpdateApprovalSettingsRequest, operatorID, username, ip string) (*model.ApprovalSettings, error) {
    settings, err := s.settings.Get(ctx)
    if err != nil {
        return nil, err
    }
    changed := false
    if req.DefaultTimeoutMinutes != nil {
        if settings.DefaultTimeoutMinutes != *req.DefaultTimeoutMinutes {
            changed = true
        }
        settings.DefaultTimeoutMinutes = *req.DefaultTimeoutMinutes
    }
    if req.OnTimeout != "" {
        if settings.OnTimeout != req.OnTimeout {
            changed = true
        }
        settings.OnTimeout = req.OnTimeout
    }
    if !changed {
        return settings, nil
    }
    if operatorID != "" {
        settings.UpdatedBy = strPtr(operatorID)
    }
    if err := s.settings.Update(ctx, settings); err != nil {
        return nil, err
    }
    s.audit(ctx, nil, username, "mcp.approval_settings.update", "approval_settings", nil, ip, map[string]interface{}{
        "default_timeout_minutes": settings.DefaultTimeoutMinutes,
        "on_timeout":              settings.OnTimeout,
    })
    return settings, nil
}

// ---------- 工具级审核配置 ----------

func (s *approvalService) UpdateToolApprovals(ctx context.Context, mcpID string, req UpdateToolApprovalsRequest, operatorID, username, ip string) ([]model.MCPTool, error) {
    server, err := s.servers.Get(ctx, mcpID)
    if err != nil {
        return nil, err
    }
    tools, decErr := decodeTools(server.Tools)
    if decErr != nil {
        return nil, errors.NewValidationError("MCP 工具列表解析失败, 请先执行连通性测试")
    }

    byName := make(map[string]*model.MCPTool, len(tools))
    for i := range tools {
        byName[tools[i].Name] = &tools[i]
    }

    changed := 0
    for _, cfg := range req.Tools {
        tool, ok := byName[cfg.Name]
        if !ok {
            return nil, errors.NewValidationError(fmt.Sprintf("工具不存在于已发现列表: %s", cfg.Name))
        }
        if tool.RequiresApproval != cfg.RequiresApproval {
            tool.RequiresApproval = cfg.RequiresApproval
            changed++
        }
    }

    if changed > 0 {
        payload, err := json.Marshal(tools)
        if err != nil {
            return nil, errors.Wrap(err, "failed to marshal tools")
        }
        server.Tools = payload
        if err := s.servers.Update(ctx, server); err != nil {
            return nil, err
        }
    }

    s.audit(ctx, strPtr(operatorID), username, "mcp.tool_approval.update", "mcp_server", &mcpID, ip, map[string]interface{}{
        "tools":   req.Tools,
        "changed": changed,
    })
    return tools, nil
}

// ---------- 审核请求生命周期 ----------

// CreateRequest 创建待审核请求 (一次调用一个请求, 并发不合并); 超时时间取当前全局配置
func (s *approvalService) CreateRequest(ctx context.Context, req CreateApprovalRequest) (*model.ToolApproval, error) {
    if _, err := s.servers.Get(ctx, req.MCPServerID); err != nil {
        return nil, err
    }
    settings, err := s.settings.Get(ctx)
    if err != nil {
        return nil, err
    }
    timeout := settings.DefaultTimeoutMinutes
    if timeout <= 0 {
        timeout = 30
    }
    now := time.Now()
    args := req.Arguments
    if args == nil {
        args = map[string]interface{}{}
    }
    payload, err := json.Marshal(args)
    if err != nil {
        return nil, errors.Wrap(err, "failed to marshal arguments")
    }
    source := req.Source
    if source == "" {
        source = model.ApprovalSourceManual
    }
    approval := &model.ToolApproval{
        MCPServerID: req.MCPServerID,
        ToolName:    req.ToolName,
        AgentID:     req.AgentID,
        Source:              source,
        WorkflowExecutionID: req.WorkflowExecutionID,
        ChatSessionID:   req.ChatSessionID,
        Arguments:   payload,
        Status:      model.ApprovalStatusPending,
        RequestedAt: now,
        ExpiresAt:   now.Add(time.Duration(timeout) * time.Minute),
    }
    if err := s.approvals.Create(ctx, approval); err != nil {
        return nil, err
    }
    s.audit(ctx, nil, "system", "mcp.approval.created", "tool_approval", &approval.ID, "", map[string]interface{}{
        "mcp_server_id": approval.MCPServerID,
        "tool_name":     approval.ToolName,
        "source":        approval.Source,
        "expires_at":    approval.ExpiresAt,
    })
    return approval, nil
}

func (s *approvalService) Get(ctx context.Context, id string) (*model.ToolApproval, error) {
    return s.approvals.Get(ctx, id)
}

// List 分页列表 (附加 MCP / Agent 名称)
func (s *approvalService) List(ctx context.Context, filter repository.ApprovalListFilter) ([]ApprovalView, int64, error) {
    items, total, err := s.approvals.List(ctx, filter)
    if err != nil {
        return nil, 0, err
    }
    if len(items) == 0 {
        return []ApprovalView{}, total, nil
    }

    servers, _ := s.servers.ListAll(ctx)
    mcpNames := make(map[string]string, len(servers))
    for i := range servers {
        mcpNames[servers[i].ID] = servers[i].Name
    }

    agentIDs := make(map[string]bool)
    for i := range items {
        if items[i].AgentID != nil && *items[i].AgentID != "" {
            agentIDs[*items[i].AgentID] = true
        }
    }
    agentNames := make(map[string]string, len(agentIDs))
    for id := range agentIDs {
        if agent, err := s.agents.GetByID(ctx, id); err == nil {
            agentNames[agent.ID] = agent.Name
        }
    }

    views := make([]ApprovalView, 0, len(items))
    for i := range items {
        view := ApprovalView{ToolApproval: items[i], MCPName: mcpNames[items[i].MCPServerID]}
        if items[i].AgentID != nil {
            view.AgentName = agentNames[*items[i].AgentID]
        }
        views = append(views, view)
    }
    return views, total, nil
}

func (s *approvalService) CountPendingByMCP(ctx context.Context, mcpID string) (int64, error) {
    return s.approvals.CountPendingByMCP(ctx, mcpID)
}

// Approve 通过并执行工具 (仅 mcp:approve 权限, 状态机保护不可重复审批)
// 执行失败仅将失败原因记入 result, 不回滚审核状态 (PRD 2.2.4)
func (s *approvalService) Approve(ctx context.Context, id, operatorID, username, ip, comment string) (*model.ToolApproval, error) {
    approval, err := s.approvals.Get(ctx, id)
    if err != nil {
        return nil, err
    }
    if approval.Status != model.ApprovalStatusPending {
        return nil, errors.NewValidationError(fmt.Sprintf("该审核请求已处理 (当前状态: %s)", approval.Status))
    }

    decided, err := s.approvals.MarkDecided(ctx, id, model.ApprovalStatusApproved, strPtr(operatorID), strPtr(comment))
    if err != nil {
        return nil, err
    }
    if !decided {
        return nil, errors.NewValidationError("该审核请求已被处理, 请刷新后重试")
    }

    execErr := s.executeAfterDecision(ctx, approval)
    s.audit(ctx, strPtr(operatorID), username, "mcp.approval.approved", "tool_approval", &id, ip, map[string]interface{}{
        "mcp_server_id": approval.MCPServerID,
        "tool_name":     approval.ToolName,
        "comment":       comment,
        "executed":      execErr == nil,
        "exec_error":    errString(execErr),
    })
    s.fireDecisionHook(ctx, approval)

    fresh, err := s.approvals.Get(ctx, id)
    if err != nil {
        return nil, err
    }
    return fresh, nil
}

// Reject 驳回 (终止本次调用, 不执行工具)
func (s *approvalService) Reject(ctx context.Context, id, operatorID, username, ip, comment string) (*model.ToolApproval, error) {
    approval, err := s.approvals.Get(ctx, id)
    if err != nil {
        return nil, err
    }
    if approval.Status != model.ApprovalStatusPending {
        return nil, errors.NewValidationError(fmt.Sprintf("该审核请求已处理 (当前状态: %s)", approval.Status))
    }

    decided, err := s.approvals.MarkDecided(ctx, id, model.ApprovalStatusRejected, strPtr(operatorID), strPtr(comment))
    if err != nil {
        return nil, err
    }
    if !decided {
        return nil, errors.NewValidationError("该审核请求已被处理, 请刷新后重试")
    }

    s.audit(ctx, strPtr(operatorID), username, "mcp.approval.rejected", "tool_approval", &id, ip, map[string]interface{}{
        "mcp_server_id": approval.MCPServerID,
        "tool_name":     approval.ToolName,
        "comment":       comment,
    })
    s.fireDecisionHook(ctx, approval)

    fresh, err := s.approvals.Get(ctx, id)
    if err != nil {
        return nil, err
    }
    return fresh, nil
}

// ProcessExpired 处理到期未审核的请求 (超时扫描器调用):
// 标记 expired 后按 on_timeout 策略执行: reject 终止 / approve 自动执行工具
func (s *approvalService) ProcessExpired(ctx context.Context) (int, error) {
    expiredList, err := s.approvals.ListExpiredPending(ctx, time.Now())
    if err != nil {
        return 0, err
    }
    if len(expiredList) == 0 {
        return 0, nil
    }

    settings, err := s.settings.Get(ctx)
    if err != nil {
        return 0, err
    }
    autoApprove := settings.OnTimeout == model.ApprovalOnTimeoutApprove

    processed := 0
    for i := range expiredList {
        approval := expiredList[i]
        comment := "审核超时"
        if autoApprove {
            comment += ", 按 on_timeout=approve 策略自动通过并执行"
        } else {
            comment += ", 按 on_timeout=reject 策略终止"
        }
        decided, err := s.approvals.MarkDecided(ctx, approval.ID, model.ApprovalStatusExpired, nil, &comment)
        if err != nil {
            log.Printf("approval: mark expired failed id=%s: %v", approval.ID, err)
            continue
        }
        if !decided {
            continue // 已被人工处理
        }
        if autoApprove {
            if execErr := s.executeAfterDecision(ctx, &approval); execErr != nil {
                log.Printf("approval: timeout auto-approve execute failed id=%s: %v", approval.ID, execErr)
            }
        }
        s.audit(ctx, nil, "system", "mcp.approval.expired", "tool_approval", &approval.ID, "", map[string]interface{}{
            "mcp_server_id": approval.MCPServerID,
            "tool_name":     approval.ToolName,
            "auto_approve":  autoApprove,
        })
        s.fireDecisionHook(ctx, &approval)
        processed++
    }
    return processed, nil
}

// executeAfterDecision 审核通过/超时自动通过后执行工具并回填 result
func (s *approvalService) executeAfterDecision(ctx context.Context, approval *model.ToolApproval) error {
    args := map[string]interface{}{}
    if len(approval.Arguments) > 0 {
        _ = json.Unmarshal(approval.Arguments, &args)
    }
    now := time.Now()
    if s.executor == nil {
        failPayload, _ := json.Marshal(map[string]interface{}{"error": "工具执行器未配置 (MCP 服务未接入)"})
        _ = s.approvals.UpdateResult(ctx, approval.ID, failPayload, &now)
        return fmt.Errorf("tool executor not configured")
    }
    callResult, err := s.executor.ExecuteTool(ctx, approval.MCPServerID, approval.ToolName, args)
    if err != nil {
        payload, _ := json.Marshal(map[string]interface{}{"error": err.Error()})
        _ = s.approvals.UpdateResult(ctx, approval.ID, payload, &now)
        return err
    }
    payload, err := json.Marshal(callResult)
    if err != nil {
        return err
    }
    return s.approvals.UpdateResult(ctx, approval.ID, payload, &now)
}

// SetDecisionHook 注入决策后钩子 (M5 接线)
func (s *approvalService) SetDecisionHook(hook ApprovalDecisionHook) {
    s.hookMu.Lock()
    s.decisionHook = hook
    s.hookMu.Unlock()
}

// fireDecisionHook 决策完成且来源为工作流/对话/外部调用时, 通知对应引擎恢复 (工作流恢复 / 对话续答)
func (s *approvalService) fireDecisionHook(ctx context.Context, approval *model.ToolApproval) {
    if approval == nil {
        return
    }
    isWorkflow := approval.Source == model.ApprovalSourceWorkflow && approval.WorkflowExecutionID != nil
    isChat := (approval.Source == model.ApprovalSourceChat || approval.Source == model.ApprovalSourceAPIInvoke) && approval.ChatSessionID != nil
    if !isWorkflow && !isChat {
        return
    }
    s.hookMu.RLock()
    hook := s.decisionHook
    s.hookMu.RUnlock()
    if hook == nil {
        return
    }
    // 重新读取决策后的最新状态 (内存对象是决策前快照), 并用后台 context 独立完成恢复
    fresh, err := s.approvals.Get(context.Background(), approval.ID)
    if err != nil {
        return
    }
    go hook(context.Background(), fresh)
}

// audit 写审计日志 (失败仅告警, 不阻塞主流程)
func (s *approvalService) audit(ctx context.Context, userID *string, username, action, resource string, resourceID *string, ip string, detail map[string]interface{}) {
    if s.audits == nil {
        return
    }
    payload, _ := json.Marshal(detail)
    entry := &model.AuditLog{
        UserID:     userID,
        Username:   username,
        Action:     action,
        Resource:   resource,
        ResourceID: resourceID,
        Detail:     payload,
        IP:         ip,
    }
    if err := s.audits.Append(ctx, entry); err != nil {
        log.Printf("approval: audit append failed action=%s: %v", action, err)
    }
}

func errString(err error) string {
    if err == nil {
        return ""
    }
    return err.Error()
}

// ---------- 超时扫描器 ----------

// ApprovalTimeoutChecker 审核超时定时扫描 (PRD 2.2.4 审核超时)
type ApprovalTimeoutChecker struct {
    svc      ApprovalService
    interval time.Duration

    mu      sync.Mutex
    stopCh  chan struct{}
    doneCh  chan struct{}
    started bool
}

func NewApprovalTimeoutChecker(svc ApprovalService, interval time.Duration) *ApprovalTimeoutChecker {
    if interval <= 0 {
        interval = ApprovalTimeoutScanInterval
    }
    return &ApprovalTimeoutChecker{svc: svc, interval: interval}
}

// Start 启动定时扫描 (幂等)
func (h *ApprovalTimeoutChecker) Start() {
    h.mu.Lock()
    defer h.mu.Unlock()
    if h.started {
        return
    }
    h.stopCh = make(chan struct{})
    h.doneCh = make(chan struct{})
    h.started = true
    go h.loop()
    log.Printf("approval timeout checker started (interval=%s)", h.interval)
}

// Stop 停止定时扫描 (服务退出时调用)
func (h *ApprovalTimeoutChecker) Stop() {
    h.mu.Lock()
    if !h.started {
        h.mu.Unlock()
        return
    }
    close(h.stopCh)
    done := h.doneCh
    h.started = false
    h.mu.Unlock()

    select {
    case <-done:
    case <-time.After(3 * time.Second):
        log.Println("approval timeout checker did not stop in time")
    }
}

func (h *ApprovalTimeoutChecker) loop() {
    defer close(h.doneCh)
    ticker := time.NewTicker(h.interval)
    defer ticker.Stop()
    for {
        select {
        case <-h.stopCh:
            return
        case <-ticker.C:
            if n, err := h.svc.ProcessExpired(context.Background()); err != nil {
                log.Printf("approval timeout scan failed: %v", err)
            } else if n > 0 {
                log.Printf("approval timeout scan: processed %d expired request(s)", n)
            }
        }
    }
}