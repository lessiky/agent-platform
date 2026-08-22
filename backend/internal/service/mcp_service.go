package service

import (
    "context"
    "encoding/json"
    "fmt"
    "math/rand"
    "strings"
    "sync"
    "time"

    "agent-platform/internal/crypto"
    "agent-platform/internal/mcpclient"
    "agent-platform/internal/model"
    "agent-platform/internal/modelclient"
    "agent-platform/internal/repository"
    "agent-platform/internal/runtime"
    "agent-platform/pkg/errors"

    "gorm.io/datatypes"
)

// MCPHealthKeep 每个 MCP 服务器保留的健康检查历史条数
const MCPHealthKeep = 500

// MCPCredentials MCP 认证凭证 (请求中为明文, 存储前加密)
type MCPCredentials struct {
    APIKey  string            `json:"api_key"`
    Headers map[string]string `json:"headers"`
}

// CreateMCPRequest 注册 MCP 服务器
type CreateMCPRequest struct {
    Name        string          `json:"name" binding:"required,min=2,max=64"`
    Endpoint    string          `json:"endpoint" binding:"required,max=512"`
    Transport   string          `json:"transport" binding:"required,oneof=stdio sse http"`
    Description string          `json:"description" binding:"max=512"`
    Tags        []string        `json:"tags"`
    Credentials *MCPCredentials `json:"credentials"`
}

// UpdateMCPRequest 更新 MCP 服务器 (全量更新, credentials 为 nil 表示保持不变)
type UpdateMCPRequest struct {
    Name        string          `json:"name" binding:"required,min=2,max=64"`
    Endpoint    string          `json:"endpoint" binding:"required,max=512"`
    Transport   string          `json:"transport" binding:"required,oneof=stdio sse http"`
    Description string          `json:"description" binding:"max=512"`
    Tags        []string        `json:"tags"`
    Credentials *MCPCredentials `json:"credentials"`
}

// CredentialsView 凭证脱敏视图 (API 永不回显明文)
type CredentialsView struct {
    APIKeySet  bool     `json:"api_key_set"`
    APIKeyMask string   `json:"api_key_mask,omitempty"`
    HeaderKeys []string `json:"header_keys"`
}

// HealthResult 连通性检测结果
type HealthResult struct {
    OK        bool      `json:"ok"`
    Status    string    `json:"status"`
    LatencyMs int       `json:"latency_ms"`
    Server    *mcpclient.ServerInfo `json:"server,omitempty"`
    ToolsCount int      `json:"tools_count"`
    Error     string    `json:"error,omitempty"`
}

// MCPServerService MCP 业务服务
type MCPServerService interface {
    Create(ctx context.Context, req CreateMCPRequest, operatorID string) (*model.MCPServer, error)
    Get(ctx context.Context, id string) (*model.MCPServer, *CredentialsView, error)
    List(ctx context.Context, filter repository.MCPListFilter) ([]model.MCPServer, int64, error)
    Update(ctx context.Context, id string, req UpdateMCPRequest) (*model.MCPServer, error)
    Delete(ctx context.Context, id string) error

    Test(ctx context.Context, id string) (*HealthResult, error)
    CheckHealth(ctx context.Context, server *model.MCPServer) *HealthResult
    Health(ctx context.Context, id string, limit int) (map[string]interface{}, error)

    ListTools(ctx context.Context, id string) ([]model.MCPTool, error)
    // ListAgentTools 汇总 Agent 绑定 MCP 的已发现工具 (M2.5, 按可用工具集白名单过滤)
    ListAgentTools(ctx context.Context, agentID string) ([]AgentToolDef, error)
    // CallTool 带审核门禁的工具调用 (M4.5): 需审核工具返回 PendingApproval 而非执行结果
    CallTool(ctx context.Context, id, tool string, arguments map[string]interface{}, opts CallOptions) (*CallToolOutcome, error)

    BindAgent(ctx context.Context, mcpID, agentID string) error
    UnbindAgent(ctx context.Context, mcpID, agentID string) error
    ListBoundAgents(ctx context.Context, mcpID string) ([]model.MCPAgentBinding, error)

    // InvokeMCPTools 调用 Agent 绑定的 MCP 工具 (M3 6.6 Agent -> MCP 调用代理, 供运行时模拟流量使用)
    // source 为调用来源 (runtime/api_invoke, M4.5 审核请求记录); pending 返回生成的待审核请求
    InvokeMCPTools(ctx context.Context, agentID, source string) (details []string, failed int, pending []runtime.PendingApproval)

    // ExecuteTool 直接执行 MCP 工具 (绕过审核门禁, 供审核服务通过后执行)
    ExecuteTool(ctx context.Context, mcpID, tool string, arguments map[string]interface{}) (*mcpclient.CallResult, error)

    // SetApprovalRequester 注入审核请求创建者 (M4.5 接线钩子, 避免构造循环依赖)
    SetApprovalRequester(requester ApprovalRequester)

    // ToolRequiresApproval 工具是否需人工审核 (M4.5, M5 工作流节点复用)
    ToolRequiresApproval(ctx context.Context, mcpID, tool string) (bool, error)
}

// ApprovalRequester 审核请求创建者 (M4.5), 由审核服务实现; 经 SetApprovalRequester 注入避免构造循环
type ApprovalRequester interface {
    CreateRequest(ctx context.Context, req CreateApprovalRequest) (*model.ToolApproval, error)
}

type mcpServerService struct {
    servers   repository.MCPServerRepository
    health    repository.MCPHealthLogRepository
    bindings  repository.MCPAgentBindingRepository
    agents    repository.AgentRepository
    approvals repository.ToolApprovalRepository
    cipher    *crypto.AesGCM
    checkTime time.Duration

    mu          sync.Mutex
    approvalReq ApprovalRequester
}

func NewMCPServerService(
    servers repository.MCPServerRepository,
    health repository.MCPHealthLogRepository,
    bindings repository.MCPAgentBindingRepository,
    agents repository.AgentRepository,
    approvals repository.ToolApprovalRepository,
    cipher *crypto.AesGCM,
    checkTimeout time.Duration,
) MCPServerService {
    if checkTimeout <= 0 {
        checkTimeout = 5 * time.Second
    }
    return &mcpServerService{
        servers:   servers,
        health:    health,
        bindings:  bindings,
        agents:    agents,
        approvals: approvals,
        cipher:    cipher,
        checkTime: checkTimeout,
    }
}

// Create 注册 MCP 服务器; 非 stdio 传输注册后立即做一次连通性检测 + 工具发现
func (s *mcpServerService) Create(ctx context.Context, req CreateMCPRequest, operatorID string) (*model.MCPServer, error) {
    if existing, err := s.servers.GetByName(ctx, req.Name); err != nil {
        return nil, err
    } else if existing != nil {
        return nil, errors.NewValidationError("MCP 名称已存在: " + req.Name)
    }

    server := &model.MCPServer{
        Name:        req.Name,
        Endpoint:    strings.TrimSpace(req.Endpoint),
        Transport:   req.Transport,
        Description: req.Description,
        Status:      model.MCPStatusPending,
        Tools:       datatypes.JSON("[]"),
        Tags:        req.Tags,
        CreatedBy:   &operatorID,
    }

    if req.Credentials != nil && (req.Credentials.APIKey != "" || len(req.Credentials.Headers) > 0) {
        encrypted, err := s.encryptCredentials(req.Credentials)
        if err != nil {
            return nil, err
        }
        server.Credentials = encrypted
    }

    if err := s.servers.Create(ctx, server); err != nil {
        return nil, err
    }

    // 立即检测 (best effort): 失败不阻塞注册, 状态记录为 disconnected
    if server.Transport != model.MCPTransportStdio {
        s.CheckHealth(ctx, server)
        if fresh, getErr := s.servers.Get(ctx, server.ID); getErr == nil {
            return fresh, nil
        }
    }
    return server, nil
}

// Get 详情 (凭证脱敏)
func (s *mcpServerService) Get(ctx context.Context, id string) (*model.MCPServer, *CredentialsView, error) {
    server, err := s.servers.Get(ctx, id)
    if err != nil {
        return nil, nil, err
    }
    return server, s.credentialsView(server), nil
}

// List 分页列表
func (s *mcpServerService) List(ctx context.Context, filter repository.MCPListFilter) ([]model.MCPServer, int64, error) {
    return s.servers.List(ctx, filter)
}

// Update 更新配置; 连接参数/凭证变化后状态重置为 pending 并重新检测
func (s *mcpServerService) Update(ctx context.Context, id string, req UpdateMCPRequest) (*model.MCPServer, error) {
    server, err := s.servers.Get(ctx, id)
    if err != nil {
        return nil, err
    }

    if req.Name != server.Name {
        if existing, err := s.servers.GetByName(ctx, req.Name); err != nil {
            return nil, err
        } else if existing != nil {
            return nil, errors.NewValidationError("MCP 名称已存在: " + req.Name)
        }
    }

    connectionChanged := req.Endpoint != server.Endpoint || req.Transport != server.Transport
    server.Name = req.Name
    server.Endpoint = strings.TrimSpace(req.Endpoint)
    server.Transport = req.Transport
    server.Description = req.Description
    server.Tags = req.Tags

    if req.Credentials != nil {
        if req.Credentials.APIKey == "" && len(req.Credentials.Headers) == 0 {
            server.Credentials = nil // 显式清空凭证
        } else {
            encrypted, err := s.encryptCredentials(req.Credentials)
            if err != nil {
                return nil, err
            }
            server.Credentials = encrypted
            connectionChanged = true
        }
    }

    if connectionChanged {
        server.Status = model.MCPStatusPending
        server.LastError = ""
    }
    if err := s.servers.Update(ctx, server); err != nil {
        return nil, err
    }

    if connectionChanged && server.Transport != model.MCPTransportStdio {
        s.CheckHealth(ctx, server)
        return s.servers.Get(ctx, id)
    }
    return server, nil
}

// Delete 删除 (级联删除健康历史与绑定)
func (s *mcpServerService) Delete(ctx context.Context, id string) error {
    if _, _, err := s.Get(ctx, id); err != nil {
        return err
    }
    if err := s.servers.Delete(ctx, id); err != nil {
        return err
    }
    if err := s.health.DeleteByMCP(ctx, id); err != nil {
        return err
    }
    if err := s.approvals.DeleteByMCP(ctx, id); err != nil {
        return err
    }
    return s.bindings.DeleteByMCP(ctx, id)
}

// Test 手动连通性测试 (initialize + 工具发现刷新)
func (s *mcpServerService) Test(ctx context.Context, id string) (*HealthResult, error) {
    server, err := s.servers.Get(ctx, id)
    if err != nil {
        return nil, err
    }

    if server.Transport == model.MCPTransportStdio {
        result := &HealthResult{
            OK:      false,
            Status:  model.MCPStatusError,
            Error:   "stdio 传输在 Phase 1 平台不支持 (不托管本地子进程), 请使用 http/sse",
        }
        s.recordHealth(ctx, server, result, false)
        return result, nil
    }

    result := s.CheckHealth(ctx, server)
    return result, nil
}

// CheckHealth 对单个 MCP 服务器执行连通性检测 + 工具发现, 更新状态并记录历史
func (s *mcpServerService) CheckHealth(ctx context.Context, server *model.MCPServer) *HealthResult {
    client := s.buildClient(server)
    if client == nil {
        result := &HealthResult{
            OK:     false,
            Status: model.MCPStatusError,
            Error:  "failed to decrypt credentials (key changed?)",
        }
        s.recordHealth(ctx, server, result, true)
        return result
    }

    start := time.Now()
    initResult, err := client.Initialize(ctx)
    latency := int(time.Since(start).Milliseconds())

    if err != nil {
        result := &HealthResult{
            OK:        false,
            Status:    model.MCPStatusDisconnected,
            LatencyMs: latency,
            Error:     truncate(err.Error(), 500),
        }
        s.recordHealth(ctx, server, result, true)
        return result
    }

    // 握手成功, 顺带刷新工具列表 (失败不影响连通状态)
    toolsCtx, cancel := context.WithTimeout(ctx, s.checkTime)
    defer cancel()
    tools, listErr := client.ListTools(toolsCtx)
    toolsCount := 0
    if listErr == nil {
        toolsCount = len(tools)
        // 工具列表落库 (M4.5: 合并保留已人工配置 requires_approval 的旧工具标记, 不覆盖)
        if fresh, getErr := s.servers.Get(ctx, server.ID); getErr == nil {
            oldTools, decErr := decodeTools(fresh.Tools)
            if decErr == nil && len(oldTools) > 0 {
                flags := make(map[string]bool, len(oldTools))
                for _, t := range oldTools {
                    flags[t.Name] = t.RequiresApproval
                }
                for i := range tools {
                    if flags[tools[i].Name] {
                        tools[i].RequiresApproval = true
                    }
                }
            }
            payload, _ := json.Marshal(tools)
            fresh.Tools = payload
            fresh.Status = model.MCPStatusConnected
            fresh.LastError = ""
            _ = s.servers.Update(ctx, fresh)
        }
    }

    status := model.MCPStatusConnected
    errMsg := ""
    if listErr != nil {
        errMsg = truncate("tools/list: " + listErr.Error(), 500)
    }

    result := &HealthResult{
        OK:         true,
        Status:     status,
        LatencyMs:  latency,
        Server:     &initResult.ServerInfo,
        ToolsCount: toolsCount,
        Error:      errMsg,
    }
    s.recordHealth(ctx, server, result, true)
    return result
}

// Health 健康状态 (最新状态 + 最近检查历史)
func (s *mcpServerService) Health(ctx context.Context, id string, limit int) (map[string]interface{}, error) {
    server, _, err := s.Get(ctx, id)
    if err != nil {
        return nil, err
    }
    logs, err := s.health.List(ctx, id, limit)
    if err != nil {
        return nil, err
    }
    return map[string]interface{}{
        "status":          server.Status,
        "last_check":      server.HealthLastCheck,
        "latency_ms":      server.HealthLatencyMs,
        "last_error":      server.LastError,
        "history":         logs,
    }, nil
}

// ListTools 返回已发现的工具列表
func (s *mcpServerService) ListTools(ctx context.Context, id string) ([]model.MCPTool, error) {
    server, err := s.servers.Get(ctx, id)
    if err != nil {
        return nil, err
    }
    return decodeTools(server.Tools)
}

// AgentToolDef 对话中模型可用工具 (M2.5): OpenAI tool 定义 + 所属 MCP
type AgentToolDef struct {
    modelclient.ChatToolDef
    MCPID   string
    MCPName string
}

// ListAgentTools 汇总 Agent 绑定 MCP 的已发现工具 (跳过 stdio/未连通; 按 Agent config 可用工具集白名单过滤, 空=不限制)
func (s *mcpServerService) ListAgentTools(ctx context.Context, agentID string) ([]AgentToolDef, error) {
    bindings, err := s.bindings.ListByAgent(ctx, agentID)
    if err != nil {
        return nil, err
    }
    if len(bindings) == 0 {
        return nil, nil
    }

    allowedTools := make(map[string]bool)
    if agent, aErr := s.agents.GetByID(ctx, agentID); aErr == nil {
        var cfg struct {
            Tools []string `json:"tools"`
        }
        if json.Unmarshal(agent.Config, &cfg) == nil {
            for _, tool := range cfg.Tools {
                allowedTools[tool] = true
            }
        }
    }

    var defs []AgentToolDef
    for _, binding := range bindings {
        server, err := s.servers.Get(ctx, binding.MCPID)
        if err != nil || server.Transport == model.MCPTransportStdio || server.Status != model.MCPStatusConnected {
            continue
        }
        tools, _ := decodeTools(server.Tools)
        for _, tool := range tools {
            if len(allowedTools) > 0 && !allowedTools[tool.Name] {
                continue
            }
            var def AgentToolDef
            def.MCPID = server.ID
            def.MCPName = server.Name
            def.Type = "function"
            def.Function.Name = tool.Name
            def.Function.Description = tool.Description
            def.Function.Parameters = tool.InputSchema
            defs = append(defs, def)
        }
    }
    return defs, nil
}

// CallOptions 工具调用上下文 (M4.5 审核门禁)
type CallOptions struct {
    Source  string // manual / runtime / api_invoke / workflow
    AgentID *string
    SessionID *string // 对话会话 ID (source=chat 时记录, 审核决策后恢复会话)
}

// CallToolOutcome 工具调用结果: 或生成审核请求 (未执行), 或返回执行结果
type CallToolOutcome struct {
    PendingApproval *model.ToolApproval
    Result          *mcpclient.CallResult
}

// CallTool 工具调用代理 (M3 6.6 + M4.5 审核门禁): 平台代调用 MCP 工具, 凭证由平台注入
// 标记 requires_approval 的工具不直接执行, 生成审核请求返回 (调用方按来源处理: 202/日志)
func (s *mcpServerService) CallTool(ctx context.Context, id, tool string, arguments map[string]interface{}, opts CallOptions) (*CallToolOutcome, error) {
    server, err := s.servers.Get(ctx, id)
    if err != nil {
        return nil, err
    }
    if server.Transport == model.MCPTransportStdio {
        return nil, errors.NewValidationError("stdio 传输在 Phase 1 平台不支持")
    }

    if s.toolRequiresApproval(server, tool) {
        requester := s.approvalRequester()
        if requester == nil {
            return nil, errors.NewValidationError(fmt.Sprintf("工具 %s 需人工审核, 但审核服务不可用", tool))
        }
        approval, err := requester.CreateRequest(ctx, CreateApprovalRequest{
            MCPServerID: id,
            ToolName:    tool,
            AgentID:     opts.AgentID,
            Source:      opts.Source,
            ChatSessionID: opts.SessionID,
            Arguments:   arguments,
        })
        if err != nil {
            return nil, err
        }
        return &CallToolOutcome{PendingApproval: approval}, nil
    }

    result, err := s.executeTool(ctx, server, tool, arguments)
    if err != nil {
        return nil, err
    }
    return &CallToolOutcome{Result: result}, nil
}

// ExecuteTool 直接执行 MCP 工具 (绕过审核门禁, 供审核服务通过后执行)
func (s *mcpServerService) ExecuteTool(ctx context.Context, mcpID, tool string, arguments map[string]interface{}) (*mcpclient.CallResult, error) {
    server, err := s.servers.Get(ctx, mcpID)
    if err != nil {
        return nil, err
    }
    return s.executeTool(ctx, server, tool, arguments)
}

// executeTool 底层工具执行 (无审核门禁)
func (s *mcpServerService) executeTool(ctx context.Context, server *model.MCPServer, tool string, arguments map[string]interface{}) (*mcpclient.CallResult, error) {
    client := s.buildClient(server)
    if client == nil {
        return nil, errors.NewValidationError("failed to decrypt mcp credentials (key changed?)")
    }
    callCtx, cancel := context.WithTimeout(ctx, s.checkTime)
    defer cancel()
    return client.CallTool(callCtx, tool, arguments)
}

// toolRequiresApproval 检查工具是否标记需人工审核 (M4.5)
func (s *mcpServerService) toolRequiresApproval(server *model.MCPServer, tool string) bool {
    tools, decErr := decodeTools(server.Tools)
    if decErr != nil {
        return false
    }
    for i := range tools {
        if tools[i].Name == tool {
            return tools[i].RequiresApproval
        }
    }
    return false
}

func (s *mcpServerService) approvalRequester() ApprovalRequester {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.approvalReq
}

// ToolRequiresApproval 工具是否需人工审核 (M4.5, M5 工作流节点复用)
func (s *mcpServerService) ToolRequiresApproval(ctx context.Context, mcpID, tool string) (bool, error) {
    server, err := s.servers.Get(ctx, mcpID)
    if err != nil {
        return false, err
    }
    return s.toolRequiresApproval(server, tool), nil
}

// SetApprovalRequester 注入审核请求创建者 (M4.5)
func (s *mcpServerService) SetApprovalRequester(requester ApprovalRequester) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.approvalReq = requester
}

// BindAgent 绑定 Agent (幂等)
func (s *mcpServerService) BindAgent(ctx context.Context, mcpID, agentID string) error {
    if _, err := s.servers.Get(ctx, mcpID); err != nil {
        return err
    }
    return s.bindings.Bind(ctx, mcpID, agentID)
}

// UnbindAgent 解绑 Agent
func (s *mcpServerService) UnbindAgent(ctx context.Context, mcpID, agentID string) error {
    return s.bindings.Unbind(ctx, mcpID, agentID)
}

// ListBoundAgents 列出绑定的 Agent
func (s *mcpServerService) ListBoundAgents(ctx context.Context, mcpID string) ([]model.MCPAgentBinding, error) {
    return s.bindings.ListByMCP(ctx, mcpID)
}

// InvokeMCPTools 调用 Agent 绑定的 MCP 工具 (最多 2 个), 供运行时模拟流量使用
// 若 Agent 配置了可用工具 (config.tools), 仅调用列表内的工具
// M4.5: 需审核工具不执行, 生成审核请求并计入 pending; source 区分 runtime/api_invoke
func (s *mcpServerService) InvokeMCPTools(ctx context.Context, agentID, source string) (details []string, failed int, pending []runtime.PendingApproval) {
    bindings, err := s.bindings.ListByAgent(ctx, agentID)
    if err != nil || len(bindings) == 0 {
        return nil, 0, nil
    }

    // 读取 Agent 配置的可用工具 (空 = 不限制)
    allowedTools := make(map[string]bool)
    if agent, aErr := s.agents.GetByID(ctx, agentID); aErr == nil {
        var cfg struct {
            Tools []string `json:"tools"`
        }
        if json.Unmarshal(agent.Config, &cfg) == nil {
            for _, tool := range cfg.Tools {
                allowedTools[tool] = true
            }
        }
    }

    for i, binding := range bindings {
        if i >= 2 {
            break
        }
        server, err := s.servers.Get(ctx, binding.MCPID)
        if err != nil {
            failed++
            details = append(details, fmt.Sprintf("mcp skip mcp_id=%s error=not_found", binding.MCPID))
            continue
        }
        if server.Transport == model.MCPTransportStdio || server.Status != model.MCPStatusConnected {
            failed++
            details = append(details, fmt.Sprintf("mcp skip name=%s reason=%s", server.Name, server.Status))
            continue
        }
        tools, _ := decodeTools(server.Tools)
        if len(tools) == 0 {
            failed++
            details = append(details, fmt.Sprintf("mcp skip name=%s reason=no_tools", server.Name))
            continue
        }
        if len(allowedTools) > 0 {
            filtered := make([]model.MCPTool, 0, len(tools))
            for _, tool := range tools {
                if allowedTools[tool.Name] {
                    filtered = append(filtered, tool)
                }
            }
            if len(filtered) == 0 {
                failed++
                details = append(details, fmt.Sprintf("mcp skip name=%s reason=no_allowed_tools", server.Name))
                continue
            }
            tools = filtered
        }

        tool := tools[rand.Intn(len(tools))]
        start := time.Now()
        outcome, callErr := s.CallTool(ctx, server.ID, tool.Name, map[string]interface{}{}, CallOptions{Source: source, AgentID: &agentID})
        latency := time.Since(start).Milliseconds()
        if callErr != nil {
            failed++
            details = append(details, fmt.Sprintf("mcp call failed name=%s tool=%s latency=%dms error=%v", server.Name, tool.Name, latency, callErr))
        } else if outcome.PendingApproval != nil {
            pending = append(pending, runtime.PendingApproval{ApprovalID: outcome.PendingApproval.ID, MCPName: server.Name, ToolName: tool.Name})
            details = append(details, fmt.Sprintf("mcp approval requested name=%s tool=%s approval_id=%s (等待人工审核, 本次不执行)", server.Name, tool.Name, outcome.PendingApproval.ID))
        } else if outcome.Result.IsError {
            failed++
            details = append(details, fmt.Sprintf("mcp call error name=%s tool=%s latency=%dms content=%s", server.Name, tool.Name, latency, summarizeContent(outcome.Result.Content)))
        } else {
            details = append(details, fmt.Sprintf("mcp call ok name=%s tool=%s latency=%dms", server.Name, tool.Name, latency))
        }
    }
    return details, failed, pending
}

// buildClient 根据服务器配置构建客户端 (解密凭证注入请求头)
func (s *mcpServerService) buildClient(server *model.MCPServer) *mcpclient.Client {
    headers := map[string]string{}
    if len(server.Credentials) > 0 {
        plain, err := s.cipher.Decrypt(server.Credentials)
        if err != nil {
            return nil
        }
        var creds MCPCredentials
        if err := json.Unmarshal(plain, &creds); err != nil {
            return nil
        }
        if creds.APIKey != "" {
            headers["Authorization"] = "Bearer " + creds.APIKey
        }
        for key, value := range creds.Headers {
            headers[key] = value
        }
    }
    return mcpclient.New(server.Endpoint, server.Transport, headers, s.checkTime)
}

func (s *mcpServerService) encryptCredentials(creds *MCPCredentials) ([]byte, error) {
    payload, err := json.Marshal(creds)
    if err != nil {
        return nil, errors.NewValidationError("invalid credentials: " + err.Error())
    }
    encrypted, err := s.cipher.Encrypt(payload)
    if err != nil {
        return nil, fmt.Errorf("failed to encrypt credentials: %w", err)
    }
    return encrypted, nil
}

// credentialsView 凭证脱敏视图
func (s *mcpServerService) credentialsView(server *model.MCPServer) *CredentialsView {
    view := &CredentialsView{HeaderKeys: []string{}}
    if len(server.Credentials) == 0 {
        return view
    }
    plain, err := s.cipher.Decrypt(server.Credentials)
    if err != nil {
        return view
    }
    var creds MCPCredentials
    if err := json.Unmarshal(plain, &creds); err != nil {
        return view
    }
    if creds.APIKey != "" {
        view.APIKeySet = true
        view.APIKeyMask = maskSecret(creds.APIKey)
    }
    for key := range creds.Headers {
        view.HeaderKeys = append(view.HeaderKeys, key)
    }
    return view
}

// recordHealth 更新服务器健康字段 + 追加检查历史 (裁剪超限部分)
func (s *mcpServerService) recordHealth(ctx context.Context, server *model.MCPServer, result *HealthResult, updateRow bool) {
    if updateRow {
        if err := s.servers.UpdateHealth(ctx, server.ID, result.Status, &result.LatencyMs, result.Error); err != nil {
            // 健康记录失败不阻塞主流程
            _ = err
        }
    }
    entry := &model.MCPHealthLog{
        MCPID:     server.ID,
        OK:        result.OK,
        LatencyMs: result.LatencyMs,
        Error:     result.Error,
    }
    if err := s.health.Append(ctx, entry); err != nil {
        _ = err
    }
    if err := s.health.Trim(ctx, server.ID, MCPHealthKeep); err != nil {
        _ = err
    }
}

func decodeTools(raw []byte) ([]model.MCPTool, error) {
    var tools []model.MCPTool
    if len(raw) == 0 {
        return tools, nil
    }
    if err := json.Unmarshal(raw, &tools); err != nil {
        return tools, nil
    }
    return tools, nil
}

func summarizeContent(content []mcpclient.ToolContent) string {
    parts := make([]string, 0, len(content))
    for i, block := range content {
        if i >= 3 {
            parts = append(parts, "...")
            break
        }
        text := block.Text
        if text == "" {
            data, _ := json.Marshal(block.Data)
            text = string(data)
        }
        parts = append(parts, truncate(text, 120))
    }
    return strings.Join(parts, " | ")
}

func maskSecret(secret string) string {
    if len(secret) <= 8 {
        return "****"
    }
    return secret[:4] + "****" + secret[len(secret)-4:]
}

func truncate(s string, max int) string {
    if len(s) <= max {
        return s
    }
    return s[:max]
}

