package service

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "log"
    "sort"
    "strings"
    "time"

    "agent-platform/internal/model"
    "agent-platform/internal/repository"
    "agent-platform/internal/runtime"
    "agent-platform/pkg/errors"

    "github.com/google/uuid"
    "gorm.io/datatypes"
    "gorm.io/gorm"
)

// AgentConfig Agent 动态配置 (存储于 JSONB)
type AgentConfig struct {
    Model        string   `json:"model,omitempty"`
    SystemPrompt string   `json:"system_prompt,omitempty"`
    Temperature  float64  `json:"temperature,omitempty"`
    MaxTokens    int      `json:"max_tokens,omitempty"`
    Tools        []string `json:"tools,omitempty"` // 可用 MCP 工具 (M3 接入后校验)
    MaxToolRounds int     `json:"max_tool_rounds,omitempty"` // 单次对话工具调用轮数上限 (0=默认 5)
    SkillsUsageMode string  `json:"skills_usage_mode,omitempty"` // 技能注入模式 (metadata_injection/full_injection, M9)
    SimulateTraffic bool   `json:"simulate_traffic,omitempty"` // 实例常驻时是否生成模拟流量 (默认 false, M2.5)
}

// CreateAgentRequest 创建 Agent 请求
type CreateAgentRequest struct {
    Name         string   `json:"name" binding:"required,min=2,max=64"`
    Description  string   `json:"description" binding:"max=512"`
    ModelID      string   `json:"model_id"` // 关联模型模板 (M4)
    Model        string   `json:"model" binding:"required"`
    SystemPrompt string   `json:"system_prompt"`
    Temperature  float64  `json:"temperature"`
    MaxTokens    int      `json:"max_tokens"`
    Tools        []string `json:"tools"`
    MaxToolRounds int     `json:"max_tool_rounds"`
    MCPIDs       []string `json:"mcp_ids"` // 绑定的 MCP 服务器 (可用工具来源)
    Skills         []string `json:"skills"` // 绑定的技能包 (M9)
    TeamID       string   `json:"team_id"`
    SimulateTraffic bool   `json:"simulate_traffic"`
    SkillsUsageMode string `json:"skills_usage_mode"` // 技能注入模式 (M9)
}

// UpdateAgentRequest 更新 Agent 请求 (全量更新; mcp_ids 为 nil 表示绑定不变)
type UpdateAgentRequest struct {
    Name         string   `json:"name" binding:"required,min=2,max=64"`
    Description  string   `json:"description" binding:"max=512"`
    ModelID      string   `json:"model_id"`
    Model        string   `json:"model" binding:"required"`
    SystemPrompt string   `json:"system_prompt"`
    Temperature  float64  `json:"temperature"`
    MaxTokens    int      `json:"max_tokens"`
    Tools        []string `json:"tools"`
    MaxToolRounds int     `json:"max_tool_rounds"`
    MCPIDs       []string `json:"mcp_ids"`
    Skills         []string `json:"skills"` // nil 表示关联不变; 空数组 = 清空 (M9)
    TeamID       string   `json:"team_id"`
    SimulateTraffic bool   `json:"simulate_traffic"`
    SkillsUsageMode string `json:"skills_usage_mode"` // 技能注入模式 (M9)
}

// AgentService Agent 业务服务
type AgentService interface {
    CreateAgent(ctx context.Context, req CreateAgentRequest, operatorID string) (*model.Agent, error)
    GetAgent(ctx context.Context, id string) (*model.Agent, *model.AgentInstance, error)
    ListAgents(ctx context.Context, filter repository.AgentListFilter) ([]*model.Agent, int64, error)
    UpdateAgent(ctx context.Context, id string, req UpdateAgentRequest, operatorID string) (*model.Agent, error)
    DeleteAgent(ctx context.Context, id string) error
    StartAgent(ctx context.Context, id string) (*model.AgentInstance, error)
    StopAgent(ctx context.Context, id string) (*model.AgentInstance, error)
    ListVersions(ctx context.Context, agentID string) ([]*model.AgentVersion, error)
    RollbackAgent(ctx context.Context, agentID string, version int, operatorID string) (*model.Agent, error)
    CreateAPIKey(ctx context.Context, agentID, name, operatorID string, expiresAt *time.Time) (*model.AgentAPIKey, string, error)
    ListAPIKeys(ctx context.Context, agentID string) ([]*model.AgentAPIKey, error)
    RevokeAPIKey(ctx context.Context, agentID, keyID string) error
    DeleteAPIKey(ctx context.Context, agentID, keyID string) error
    // InvokeAgent API Key 外部调用: 校验 key (状态/过期) + 更新 last_used_at, 执行 M3/M4 调用链
    InvokeAgent(ctx context.Context, agentID, plainKey string, req InvokeAgentRequest) (*InvokeAgentResult, error)
    // GetInvokeApproval 查询 /invoke 产生的审核请求结果 (API Key 鉴权; 仅限本 Agent 的审核请求)
    GetInvokeApproval(ctx context.Context, agentID, plainKey, approvalID string) (*InvokeApprovalView, error)
    // ListBoundMCPS Agent 绑定的 MCP 服务器 (含已发现工具, 供表单联动)
    ListBoundMCPS(ctx context.Context, agentID string) ([]BoundMCPView, error)
    // ListBoundSkills Agent 绑定的技能 (含依赖工具覆盖状态, 供表单联动, M9)
    ListBoundSkills(ctx context.Context, agentID string) ([]BoundSkillView, error)
    // UpdateAgentSkills 全量更新 Agent 技能绑定 (M9, 含依赖与预算校验)
    UpdateAgentSkills(ctx context.Context, agentID string, skillIDs []string, operatorID string) error
    GetMetrics(ctx context.Context, agentID string, from, to time.Time) (map[string]interface{}, error)
    GetLogs(ctx context.Context, filter repository.AgentLogFilter) ([]*model.AgentLog, int64, error)
    Dashboard(ctx context.Context) (map[string]interface{}, error)
    ReconcileInstances(ctx context.Context) error
}

// BoundMCPView Agent 绑定的 MCP 服务器视图 (含已发现工具)
type BoundMCPView struct {
    ID        string          `json:"id"`
    Name      string          `json:"name"`
    Status    string          `json:"status"`
    Tools     []model.MCPTool `json:"tools"`
    LastError string          `json:"last_error,omitempty"`
}

// BoundSkillView Agent 绑定的技能视图 (含依赖工具覆盖状态, M9)
type BoundSkillView struct {
    ID            string   `json:"id"`
    Name          string   `json:"name"`
    Version       int      `json:"version"`
    Description   string   `json:"description"`
    Status        string   `json:"status"`
    RequiredTools []string `json:"required_tools"`
    MissingTools  []string `json:"missing_tools"` // 未被当前可用工具集覆盖的 required_tools
}

type agentService struct {
    agents    repository.AgentRepository
    versions  repository.AgentVersionRepository
    instances repository.AgentInstanceRepository
    logs      repository.AgentLogRepository
    apiKeys   repository.AgentAPIKeyRepository
    stats     repository.AgentCallStatRepository
    runtime   *runtime.Runtime
    mcps         repository.MCPServerRepository
    bindings     repository.MCPAgentBindingRepository
    skillBindings repository.SkillAgentBindingRepository
    skillRepo    repository.SkillRepository
    chatSessions repository.ChatSessionRepository
    chat         ChatService // 对话链路 (API Key /invoke 复用, 返回模型应答)
    toolApprovals repository.ToolApprovalRepository // /invoke 202 待审核结果的 Key 鉴权查询
}

func NewAgentService(
    agents repository.AgentRepository,
    versions repository.AgentVersionRepository,
    instances repository.AgentInstanceRepository,
    logs repository.AgentLogRepository,
    apiKeys repository.AgentAPIKeyRepository,
    stats repository.AgentCallStatRepository,
    rt *runtime.Runtime,
    mcps repository.MCPServerRepository,
    bindings repository.MCPAgentBindingRepository,
    skillBindings repository.SkillAgentBindingRepository,
    skillRepo repository.SkillRepository,
    chatSessions repository.ChatSessionRepository,
    chat ChatService,
    toolApprovals repository.ToolApprovalRepository,
) AgentService {
    return &agentService{
        agents:    agents,
        versions:  versions,
        instances: instances,
        logs:      logs,
        apiKeys:   apiKeys,
        stats:     stats,
        runtime:   rt,
        mcps:      mcps,
        bindings:     bindings,
        skillBindings: skillBindings,
        skillRepo:     skillRepo,
        chatSessions: chatSessions,
        chat:         chat,
        toolApprovals: toolApprovals,
    }
}

// CreateAgent 创建 Agent (初始版本 1); 可用工具自动校验 (须来自绑定 MCP 的已发现工具)
func (s *agentService) CreateAgent(ctx context.Context, req CreateAgentRequest, operatorID string) (*model.Agent, error) {
    if err := s.ensureNameAvailable(ctx, req.Name, ""); err != nil {
        return nil, err
    }
    if err := s.validateTools(ctx, req.MCPIDs, req.Tools); err != nil {
        return nil, err
    }
    if err := s.validateSkills(ctx, req.MCPIDs, req.Tools, req.Skills, req.SkillsUsageMode); err != nil {
        return nil, err
    }

    configJSON, err := json.Marshal(AgentConfig{
        Model:        req.Model,
        SystemPrompt: req.SystemPrompt,
        Temperature:  req.Temperature,
        MaxTokens:    req.MaxTokens,
        Tools:        req.Tools,
        MaxToolRounds: req.MaxToolRounds,
        SkillsUsageMode: req.SkillsUsageMode,
        SimulateTraffic: req.SimulateTraffic,
    })
    if err != nil {
        return nil, errors.Wrap(err, "failed to marshal agent config")
    }

    agent := &model.Agent{
        Name:        req.Name,
        Description: req.Description,
        ModelID:     strPtr(req.ModelID),
        Status:      model.AgentStatusIdle,
        Version:     1,
        Config:      datatypes.JSON(configJSON),
        TeamID:      strPtr(req.TeamID),
        CreatedBy:   strPtr(operatorID),
    }
    if err := s.agents.Create(ctx, agent); err != nil {
        if strings.Contains(err.Error(), "duplicate key") {
            return nil, errors.NewValidationError("agent name already exists")
        }
        return nil, errors.Wrap(err, "failed to create agent")
    }

    if len(req.MCPIDs) > 0 {
        if err := s.syncMCPBindings(ctx, agent.ID, req.MCPIDs); err != nil {
            return nil, err
        }
    }
    if len(req.Skills) > 0 {
        if err := s.syncSkillBindings(ctx, agent.ID, req.Skills, operatorID); err != nil {
            return nil, err
        }
    }
    if err := s.snapshotVersion(ctx, agent, 1, operatorID); err != nil {
        return nil, err
    }
    return agent, nil
}

// syncMCPBindings 全量同步 Agent 的 MCP 绑定 (新增缺失, 移除多余)
func (s *agentService) syncMCPBindings(ctx context.Context, agentID string, mcpIDs []string) error {
    current, err := s.bindings.ListByAgent(ctx, agentID)
    if err != nil {
        return errors.Wrap(err, "failed to list mcp bindings")
    }
    wanted := make(map[string]bool, len(mcpIDs))
    for _, id := range mcpIDs {
        id = strings.TrimSpace(id)
        if id == "" {
            continue
        }
        if wanted[id] {
            continue
        }
        wanted[id] = true
        if err := s.bindings.Bind(ctx, id, agentID); err != nil {
            return errors.Wrap(err, "failed to bind mcp")
        }
    }
    for _, b := range current {
        if !wanted[b.MCPID] {
            if err := s.bindings.Unbind(ctx, b.MCPID, agentID); err != nil {
                return errors.Wrap(err, "failed to unbind mcp")
            }
        }
    }
    return nil
}

// validateTools 校验可用工具: 每个工具必须存在于绑定 MCP 的已发现工具列表 (M3 接入)
func (s *agentService) validateTools(ctx context.Context, mcpIDs, tools []string) error {
    if len(tools) == 0 {
        return nil
    }
    if len(mcpIDs) == 0 {
        return errors.NewValidationError("已配置可用工具, 但未绑定 MCP 服务器")
    }
    available := make(map[string]string) // tool name -> mcp name
    for _, id := range mcpIDs {
        id = strings.TrimSpace(id)
        if id == "" {
            continue
        }
        server, err := s.mcps.Get(ctx, id)
        if err != nil {
            return errors.NewValidationError("绑定的 MCP 不存在: " + id)
        }
        serverTools, _ := decodeTools(server.Tools)
        for _, tool := range serverTools {
            available[tool.Name] = server.Name
        }
    }
    var missing []string
    for _, tool := range tools {
        if _, ok := available[tool]; !ok {
            missing = append(missing, tool)
        }
    }
    if len(missing) > 0 {
        names := make([]string, 0, len(available))
        for name, mcpName := range available {
            names = append(names, name+"("+mcpName+")")
        }
        sort.Strings(names)
        return errors.NewValidationError(fmt.Sprintf("工具不存在于绑定 MCP 的已发现列表: %s (可用: %s)", strings.Join(missing, ", "), strings.Join(names, ", ")))
    }
    return nil
}

// ListBoundMCPS Agent 绑定的 MCP 服务器 (含已发现工具)
func (s *agentService) ListBoundMCPS(ctx context.Context, agentID string) ([]BoundMCPView, error) {
    if _, err := s.agents.GetByID(ctx, agentID); err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }
    bindings, err := s.bindings.ListByAgent(ctx, agentID)
    if err != nil {
        return nil, errors.Wrap(err, "failed to list mcp bindings")
    }
    views := make([]BoundMCPView, 0, len(bindings))
    for _, b := range bindings {
        server, err := s.mcps.Get(ctx, b.MCPID)
        if err != nil {
            continue // 孤儿绑定 (MCP 已删除)
        }
        views = append(views, BoundMCPView{
            ID:        server.ID,
            Name:      server.Name,
            Status:    server.Status,
            Tools:     func() []model.MCPTool { t, _ := decodeTools(server.Tools); return t }(),
            LastError: server.LastError,
        })
    }
    return views, nil
}

// GetAgent 获取 Agent 详情 (含实例信息)
func (s *agentService) GetAgent(ctx context.Context, id string) (*model.Agent, *model.AgentInstance, error) {
    agent, err := s.agents.GetByID(ctx, id)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, nil, errors.ErrNotFound
        }
        return nil, nil, errors.Wrap(err, "failed to get agent")
    }

    instance, err := s.instances.GetByAgent(ctx, id)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            instance = nil
        } else {
            return nil, nil, errors.Wrap(err, "failed to get agent instance")
        }
    }
    return agent, instance, nil
}

// ListAgents 分页列表
func (s *agentService) ListAgents(ctx context.Context, filter repository.AgentListFilter) ([]*model.Agent, int64, error) {
    agents, total, err := s.agents.List(ctx, filter)
    if err != nil {
        return nil, 0, errors.Wrap(err, "failed to list agents")
    }
    return agents, total, nil
}

// UpdateAgent 更新 Agent 配置 (仅允许停止状态, 产生新版本)
func (s *agentService) UpdateAgent(ctx context.Context, id string, req UpdateAgentRequest, operatorID string) (*model.Agent, error) {
    agent, err := s.agents.GetByID(ctx, id)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }
    if agent.Status == model.AgentStatusRunning {
        return nil, errors.NewValidationError("agent is running, stop it before updating")
    }
    if err := s.ensureNameAvailable(ctx, req.Name, agent.ID); err != nil {
        return nil, err
    }

    // 校验可用工具 (基于更新后的绑定集合)
    finalMCPIDs := req.MCPIDs
    if finalMCPIDs == nil {
        bindings, bErr := s.bindings.ListByAgent(ctx, agent.ID)
        if bErr != nil {
            return nil, errors.Wrap(bErr, "failed to list mcp bindings")
        }
        finalMCPIDs = make([]string, 0, len(bindings))
        for _, b := range bindings {
            finalMCPIDs = append(finalMCPIDs, b.MCPID)
        }
    }
    if err := s.validateTools(ctx, finalMCPIDs, req.Tools); err != nil {
        return nil, err
    }
    if req.Skills != nil {
        if err := s.validateSkills(ctx, finalMCPIDs, req.Tools, req.Skills, req.SkillsUsageMode); err != nil {
            return nil, err
        }
    }

    configJSON, err := json.Marshal(AgentConfig{
        Model:        req.Model,
        SystemPrompt: req.SystemPrompt,
        Temperature:  req.Temperature,
        MaxTokens:    req.MaxTokens,
        Tools:        req.Tools,
        MaxToolRounds: req.MaxToolRounds,
        SkillsUsageMode: req.SkillsUsageMode,
        SimulateTraffic: req.SimulateTraffic,
    })
    if err != nil {
        return nil, errors.Wrap(err, "failed to marshal agent config")
    }

    agent.Name = req.Name
    agent.Description = req.Description
    agent.ModelID = strPtr(req.ModelID)
    agent.Config = datatypes.JSON(configJSON)
    agent.TeamID = strPtr(req.TeamID)
    agent.Version++

    if err := s.agents.Update(ctx, agent); err != nil {
        return nil, errors.Wrap(err, "failed to update agent")
    }
    if req.MCPIDs != nil {
        if err := s.syncMCPBindings(ctx, agent.ID, req.MCPIDs); err != nil {
            return nil, err
        }
    }
    if req.Skills != nil {
        if err := s.syncSkillBindings(ctx, agent.ID, req.Skills, operatorID); err != nil {
            return nil, err
        }
    }
    if err := s.snapshotVersion(ctx, agent, agent.Version, operatorID); err != nil {
        return nil, err
    }
    return agent, nil
}

// DeleteAgent 删除 Agent (仅允许非运行状态)
func (s *agentService) DeleteAgent(ctx context.Context, id string) error {
    agent, err := s.agents.GetByID(ctx, id)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return errors.ErrNotFound
        }
        return errors.Wrap(err, "failed to get agent")
    }
    if agent.Status == model.AgentStatusRunning {
        return errors.NewValidationError("agent is running, stop it before deleting")
    }

    if err := s.instances.SoftDelete(ctx, id); err != nil {
        return errors.Wrap(err, "failed to delete agent instance")
    }
    if err := s.bindings.DeleteByAgent(ctx, id); err != nil {
        return errors.Wrap(err, "failed to unbind mcp")
    }
    if err := s.skillBindings.DeleteByAgent(ctx, id); err != nil {
        return errors.Wrap(err, "failed to unbind skills")
    }
        if err := s.chatSessions.DeleteByAgentCascade(ctx, id); err != nil {
            return errors.Wrap(err, "failed to delete chat sessions")
        }
    if err := s.agents.Delete(ctx, id); err != nil {
        return errors.Wrap(err, "failed to delete agent")
    }
    return nil
}

// StartAgent 启动 Agent 实例
func (s *agentService) StartAgent(ctx context.Context, id string) (*model.AgentInstance, error) {
    agent, err := s.agents.GetByID(ctx, id)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }
    if agent.Status == model.AgentStatusRunning {
        return nil, errors.NewValidationError("instance is already running")
    }
    if s.runtime.IsRunning(id) {
        return nil, errors.NewValidationError("instance is already running")
    }

    instance, err := s.instances.GetByAgent(ctx, id)
    if err == gorm.ErrRecordNotFound {
        instance = &model.AgentInstance{
            AgentID:  id,
            Status:   model.InstanceStatusPending,
            Endpoint: fmt.Sprintf("sim://%s", agent.Name),
        }
        if err := s.instances.Create(ctx, instance); err != nil {
            return nil, errors.Wrap(err, "failed to create agent instance")
        }
    } else if err != nil {
        return nil, errors.Wrap(err, "failed to get agent instance")
    }

    // 运行时 goroutine 不能绑定请求上下文, 否则请求结束实例会被误停
    if err := s.runtime.Start(context.Background(), id, instance.ID); err != nil {
        return nil, errors.NewValidationError(err.Error())
    }

    now := time.Now()
    if err := s.instances.SetRunning(ctx, id, now); err != nil {
        s.runtime.Stop(id)
        return nil, errors.Wrap(err, "failed to update instance status")
    }

    agent.Status = model.AgentStatusRunning
    if err := s.agents.Update(ctx, agent); err != nil {
        return nil, errors.Wrap(err, "failed to update agent status")
    }

    instance.Status = model.InstanceStatusRunning
    instance.StartedAt = &now
    instance.LastHeartbeat = &now
    return instance, nil
}

// StopAgent 停止 Agent 实例
func (s *agentService) StopAgent(ctx context.Context, id string) (*model.AgentInstance, error) {
    agent, err := s.agents.GetByID(ctx, id)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }
    if agent.Status != model.AgentStatusRunning {
        return nil, errors.NewValidationError("instance is not running")
    }

    s.runtime.Stop(id)

    now := time.Now()
    if err := s.instances.SetStopped(ctx, id, now); err != nil {
        return nil, errors.Wrap(err, "failed to update instance status")
    }

    agent.Status = model.AgentStatusStopped
    if err := s.agents.Update(ctx, agent); err != nil {
        return nil, errors.Wrap(err, "failed to update agent status")
    }

    instance, _ := s.instances.GetByAgent(ctx, id)
    return instance, nil
}

// ListVersions 版本历史 (倒序)
func (s *agentService) ListVersions(ctx context.Context, agentID string) ([]*model.AgentVersion, error) {
    if _, err := s.agents.GetByID(ctx, agentID); err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }
    versions, err := s.versions.ListByAgent(ctx, agentID)
    if err != nil {
        return nil, errors.Wrap(err, "failed to list agent versions")
    }
    return versions, nil
}

// RollbackAgent 回滚到指定版本 (恢复为当前配置, 产生新版本号)
func (s *agentService) RollbackAgent(ctx context.Context, agentID string, version int, operatorID string) (*model.Agent, error) {
    agent, err := s.agents.GetByID(ctx, agentID)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }
    if agent.Status == model.AgentStatusRunning {
        return nil, errors.NewValidationError("agent is running, stop it before rollback")
    }

    snapshot, err := s.versions.Get(ctx, agentID, version)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.NewValidationError("version not found")
        }
        return nil, errors.Wrap(err, "failed to get agent version")
    }

    agent.Name = snapshot.Name
    agent.Description = snapshot.Description
    agent.Config = snapshot.Config
    agent.Version++

    if err := s.agents.Update(ctx, agent); err != nil {
        return nil, errors.Wrap(err, "failed to rollback agent")
    }
    if err := s.snapshotVersion(ctx, agent, agent.Version, operatorID); err != nil {
        return nil, err
    }
    return agent, nil
}

// CreateAPIKey 创建 API Key (expiresAt 可选, nil 表示永不过期), 明文仅在创建时返回一次
func (s *agentService) CreateAPIKey(ctx context.Context, agentID, name, operatorID string, expiresAt *time.Time) (*model.AgentAPIKey, string, error) {
    if _, err := s.agents.GetByID(ctx, agentID); err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, "", errors.ErrNotFound
        }
        return nil, "", errors.Wrap(err, "failed to get agent")
    }
    if name == "" {
        name = "default"
    }
    if expiresAt != nil && !expiresAt.After(time.Now()) {
        return nil, "", errors.NewValidationError("expires_at 必须是未来的时间")
    }

    plain, err := generateAPIKey()
    if err != nil {
        return nil, "", errors.Wrap(err, "failed to generate api key")
    }
    sum := sha256.Sum256([]byte(plain))

    key := &model.AgentAPIKey{
        AgentID:   agentID,
        Name:      name,
        KeyPrefix: plain[:12],
        KeyHash:   hex.EncodeToString(sum[:]),
        Status:    model.APIKeyStatusActive,
        ExpiresAt: expiresAt,
        CreatedBy: strPtr(operatorID),
    }
    if err := s.apiKeys.Create(ctx, key); err != nil {
        return nil, "", errors.Wrap(err, "failed to create api key")
    }
    return key, plain, nil
}

// ListAPIKeys API Key 列表 (不含摘要)
func (s *agentService) ListAPIKeys(ctx context.Context, agentID string) ([]*model.AgentAPIKey, error) {
    if _, err := s.agents.GetByID(ctx, agentID); err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }
    keys, err := s.apiKeys.ListByAgent(ctx, agentID)
    if err != nil {
        return nil, errors.Wrap(err, "failed to list api keys")
    }
    return keys, nil
}

// RevokeAPIKey 吊销 API Key
func (s *agentService) RevokeAPIKey(ctx context.Context, agentID, keyID string) error {
    key, err := s.apiKeys.GetByIDAndAgent(ctx, agentID, keyID)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return errors.ErrNotFound
        }
        return errors.Wrap(err, "failed to get api key")
    }
    if key.Status == model.APIKeyStatusRevoked {
        return errors.NewValidationError("api key is already revoked")
    }

    now := time.Now()
    key.Status = model.APIKeyStatusRevoked
    key.RevokedAt = &now
    if err := s.apiKeys.Update(ctx, key); err != nil {
        return errors.Wrap(err, "failed to revoke api key")
    }
    return nil
}

// DeleteAPIKey 删除 API Key (仅允许已吊销的 Key, 防止误删仍在使用的密钥)
func (s *agentService) DeleteAPIKey(ctx context.Context, agentID, keyID string) error {
    key, err := s.apiKeys.GetByIDAndAgent(ctx, agentID, keyID)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return errors.ErrNotFound
        }
        return errors.Wrap(err, "failed to get api key")
    }
    if key.Status != model.APIKeyStatusRevoked {
        return errors.NewValidationError("仅已吊销的 API Key 可删除")
    }
    if err := s.apiKeys.Delete(ctx, key.ID); err != nil {
        return errors.Wrap(err, "failed to delete api key")
    }
    return nil
}

// InvokeAgentRequest API Key 外部调用请求
type InvokeAgentRequest struct {
    Message   string  `json:"message" binding:"required,max=8192"`
    SessionID *string `json:"session_id,omitempty"` // 可选: 复用对话会话 (多轮上下文, 落库到会话)
}

// InvokeAgentResult API Key 外部调用结果 (2026-08-21 升级: 返回模型应答 reply)
type InvokeAgentResult struct {
    AgentID          string                    `json:"agent_id"`
    KeyPrefix        string                    `json:"key_prefix"`
    Reply            string                    `json:"reply"` // 模型应答
    Model            string                    `json:"model,omitempty"`
    ModelName        string                    `json:"model_name,omitempty"`
    ModelDetail      string                    `json:"model_detail,omitempty"`
    ModelOK          bool                      `json:"model_ok"`
    MCPDetails       []string                  `json:"mcp_details,omitempty"`
    PendingApprovals []runtime.PendingApproval `json:"pending_approvals,omitempty"` // M4.5: 生成的待审核请求 (对应工具未执行)
    SessionID        string                    `json:"session_id,omitempty"` // 指定 session_id 时返回
    MessageID        string                    `json:"message_id,omitempty"`
    Tokens           int                       `json:"tokens"`
    LatencyMs        int64                     `json:"latency_ms"`
}

// InvokeApprovalView 外部调用 (/invoke) 审核结果查询视图 (API Key 鉴权)
// /invoke 存在待审核工具调用时返回 202 + pending_approvals; 外部系统凭 API Key 轮询本视图获取终态与工具执行结果
type InvokeApprovalView struct {
    ApprovalID  string         `json:"approval_id"`
    ToolName    string         `json:"tool_name"`
    Status      string         `json:"status"` // pending / approved / rejected / expired
    RequestedAt time.Time      `json:"requested_at"`
    ExpiresAt   time.Time      `json:"expires_at"`
    DecidedAt   *time.Time     `json:"decided_at,omitempty"`
    ExecutedAt  *time.Time     `json:"executed_at,omitempty"`
    Comment      *string        `json:"comment,omitempty"` // 审核意见
    Result       datatypes.JSON `json:"result,omitempty"`  // 工具执行结果 (通过并执行后回填)
    Continuation *InvokeContinuationView `json:"continuation,omitempty"` // 模型续答 (决策后模型续答轮落库后回填; 拒绝/超时为未执行说明)
}

// InvokeContinuationView 审核决策后的模型续答 (决策钩子驱动模型续答轮, 完成落库后可得)
type InvokeContinuationView struct {
    Reply     string    `json:"reply"`
    MessageID string    `json:"message_id"`
    CreatedAt time.Time `json:"created_at"`
}

// apiKeyAuthError API Key 认证失败 (无效/不属于该 Agent/已吊销/已过期)
func apiKeyAuthError(msg string) *errors.AppError {
    return &errors.AppError{Code: "unauthorized", Message: msg, HTTPCode: 401}
}

// instanceNotRunningError 实例未运行, 拒绝外部调用 (生命周期门禁, 409)
func instanceNotRunningError() *errors.AppError {
    return &errors.AppError{Code: "instance_not_running", Message: "实例未运行, 请先启动实例", HTTPCode: 409}
}

// InvokeAgent API Key 外部调用入口 (M2 待办: expires_at 校验 + last_used_at 更新):
// 1. 校验 API Key (hash 匹配 / 归属本 Agent / active / 未过期)
// 2. 校验实例运行状态 (未运行返回 409, 启停控制对外服务)
// 3. 更新 last_used_at
// 4. 执行调用链 (2026-08-21 升级): 复用对话执行链返回模型应答; 无可用模型时降级旧链 (token 估算 + MCP 调用)
func (s *agentService) InvokeAgent(ctx context.Context, agentID, plainKey string, req InvokeAgentRequest) (*InvokeAgentResult, error) {
    if strings.TrimSpace(plainKey) == "" {
        return nil, apiKeyAuthError("缺少 API Key (Authorization: Bearer akp_...)")
    }

    sum := sha256.Sum256([]byte(plainKey))
    key, err := s.apiKeys.GetByHash(ctx, hex.EncodeToString(sum[:]))
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, apiKeyAuthError("无效的 API Key")
        }
        return nil, errors.Wrap(err, "failed to verify api key")
    }
    if key.AgentID != agentID {
        return nil, apiKeyAuthError("API Key 不属于该 Agent")
    }
    if key.Status != model.APIKeyStatusActive {
        return nil, apiKeyAuthError("API Key 已吊销")
    }
    if key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now()) {
        return nil, apiKeyAuthError("API Key 已过期")
    }

    agent, err := s.agents.GetByID(ctx, agentID)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }
    if agent.Status != model.AgentStatusRunning {
        return nil, instanceNotRunningError()
    }

    // 更新 last_used_at (失败仅告警, 不阻断调用)
    now := time.Now()
    key.LastUsedAt = &now
    if err := s.apiKeys.Update(ctx, key); err != nil {
        log.Printf("agent service: update last_used_at failed for key %s: %v", key.ID, err)
    }

    // 执行调用链 (2026-08-21 升级): 复用对话执行链 (模型应答 + 工具调用 + 审核门禁 + 统计)
    chatResult, chatErr := s.chat.Invoke(ctx, agentID, InvokeRequest{Message: req.Message, SessionID: req.SessionID})
    if chatErr != nil {
        // 无可用模型时降级旧链路 (MCP 调用 + token 估算), 保持 model_ok=false 语义
        if appErr, ok := chatErr.(*errors.AppError); !ok || appErr.Code != "no_model_available" {
            return nil, chatErr
        }
        return s.invokeLegacy(ctx, agentID, key, req.Message), nil
    }
    return &InvokeAgentResult{
        AgentID:          agentID,
        KeyPrefix:        key.KeyPrefix,
        Reply:            chatResult.Reply,
        Model:            chatResult.Model,
        ModelName:        chatResult.ModelName,
        ModelOK:          true,
        ModelDetail:      fmt.Sprintf("model ok model=%s latency=%dms", chatResult.ModelName, chatResult.LatencyMs),
        MCPDetails:       mcpCallLines(chatResult.MCPCalls),
        PendingApprovals: chatResult.PendingApprovals,
        SessionID:        chatResult.SessionID,
        MessageID:        chatResult.MessageID,
        Tokens:           chatResult.TotalTokens,
        LatencyMs:        chatResult.LatencyMs,
    }, nil
}

// invokeLegacy 无可用模型时的降级路径: MCP 调用 + token 估算 (保持 model_ok=false 语义)
func (s *agentService) invokeLegacy(ctx context.Context, agentID string, key *model.AgentAPIKey, message string) *InvokeAgentResult {
    tokens := 50 + len([]rune(message))
    result := s.runtime.InvokeOnce(ctx, agentID, tokens)
    return &InvokeAgentResult{
        AgentID:          agentID,
        KeyPrefix:        key.KeyPrefix,
        ModelDetail:      result.ModelDetail,
        ModelOK:          result.ModelOK,
        MCPDetails:       result.MCPDetails,
        PendingApprovals: result.PendingApprovals,
        Tokens:           result.Tokens,
        LatencyMs:        result.LatencyMs,
    }
}

// mcpCallLines 将对话链路的 MCP 调用明细格式化为 /invoke 响应的 mcp_details 行
func mcpCallLines(calls []MCPChatCall) []string {
    lines := make([]string, 0, len(calls))
    for _, c := range calls {
        line := fmt.Sprintf("tool %s/%s status=%s", c.MCPName, c.ToolName, c.Status)
        if c.Detail != "" {
            line += " " + c.Detail
        }
        lines = append(lines, line)
    }
    return lines
}

// verifyInvokeKey 校验 /invoke 系端点的 API Key (归属/状态/过期), 返回 Key 记录
func (s *agentService) verifyInvokeKey(ctx context.Context, agentID, plainKey string) (*model.AgentAPIKey, error) {
    if strings.TrimSpace(plainKey) == "" {
        return nil, apiKeyAuthError("缺少 API Key (Authorization: Bearer akp_...)")
    }
    sum := sha256.Sum256([]byte(plainKey))
    key, err := s.apiKeys.GetByHash(ctx, hex.EncodeToString(sum[:]))
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, apiKeyAuthError("无效的 API Key")
        }
        return nil, errors.Wrap(err, "failed to verify api key")
    }
    if key.AgentID != agentID {
        return nil, apiKeyAuthError("API Key 不属于该 Agent")
    }
    if key.Status != model.APIKeyStatusActive {
        return nil, apiKeyAuthError("API Key 已吊销")
    }
    if key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now()) {
        return nil, apiKeyAuthError("API Key 已过期")
    }
    return key, nil
}

// GetInvokeApproval 查询 /invoke 产生的审核请求结果 (API Key 鉴权)
// 审核请求必须属于该 Agent (Key 的归属 Agent), 否则 404 (不泄露其他 Agent 的审核信息)
func (s *agentService) GetInvokeApproval(ctx context.Context, agentID, plainKey, approvalID string) (*InvokeApprovalView, error) {
    if _, err := s.verifyInvokeKey(ctx, agentID, plainKey); err != nil {
        return nil, err
    }
    if _, err := uuid.Parse(approvalID); err != nil {
        return nil, errors.ErrNotFound
    }
    approval, err := s.toolApprovals.Get(ctx, approvalID)
    if err != nil {
        if err == errors.ErrNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get approval")
    }
    if approval.AgentID == nil || *approval.AgentID != agentID {
        return nil, errors.ErrNotFound
    }
    view := &InvokeApprovalView{
        ApprovalID:  approval.ID,
        ToolName:    approval.ToolName,
        Status:      approval.Status,
        RequestedAt: approval.RequestedAt,
        ExpiresAt:   approval.ExpiresAt,
        DecidedAt:   approval.DecidedAt,
        ExecutedAt:  approval.ExecutedAt,
        Comment:     approval.Comment,
        Result:      approval.Result,
    }
    // 模型续答 (决策钩子异步驱动续答轮, 落库前查询为空, 继续轮询即可)
    if msg, cerr := s.chat.GetApprovalContinuation(ctx, approval.ID); cerr == nil {
        view.Continuation = &InvokeContinuationView{Reply: msg.Content, MessageID: msg.ID, CreatedAt: msg.CreatedAt}
    } else if cerr != errors.ErrNotFound {
        log.Printf("agent service: get approval continuation failed approval=%s: %v", approval.ID, cerr)
    }
    return view, nil
}

// GetMetrics 调用统计 (默认最近 7 天)
func (s *agentService) GetMetrics(ctx context.Context, agentID string, from, to time.Time) (map[string]interface{}, error) {
    if _, err := s.agents.GetByID(ctx, agentID); err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, errors.Wrap(err, "failed to get agent")
    }

    if to.IsZero() {
        to = time.Now()
    }
    if from.IsZero() {
        from = to.AddDate(0, 0, -7)
    }
    // 按天对齐: from 含当天 0 点, to 取次日 0 点 (上界不含)
    from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
    to = time.Date(to.Year(), to.Month(), to.Day()+1, 0, 0, 0, 0, to.Location())

    calls, errs, tokens, latencyMs, err := s.stats.SumRange(ctx, agentID, from, to)
    if err != nil {
        return nil, errors.Wrap(err, "failed to sum call stats")
    }
    daily, err := s.stats.DailySeries(ctx, agentID, from, to)
    if err != nil {
        return nil, errors.Wrap(err, "failed to get daily stats")
    }

    errorRate := 0.0
    avgLatency := 0.0
    if calls > 0 {
        errorRate = float64(errs) / float64(calls)
        avgLatency = float64(latencyMs) / float64(calls)
    }

    return map[string]interface{}{
        "from":           from,
        "to":             to,
        "total_calls":    calls,
        "total_errors":   errs,
        "error_rate":     errorRate,
        "total_tokens":   tokens,
        "avg_latency_ms": avgLatency,
        "daily":          daily,
    }, nil
}

// GetLogs 查询 Agent 运行日志 (支持关键词/级别/时间过滤)
func (s *agentService) GetLogs(ctx context.Context, filter repository.AgentLogFilter) ([]*model.AgentLog, int64, error) {
    if _, err := s.agents.GetByID(ctx, filter.AgentID); err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, 0, errors.ErrNotFound
        }
        return nil, 0, errors.Wrap(err, "failed to get agent")
    }
    logs, total, err := s.logs.List(ctx, filter)
    if err != nil {
        return nil, 0, errors.Wrap(err, "failed to list agent logs")
    }
    return logs, total, nil
}

// Dashboard 状态看板: 状态计数 + 运行中 Agent + 今日调用量
func (s *agentService) Dashboard(ctx context.Context) (map[string]interface{}, error) {
    counts, err := s.agents.CountByStatus(ctx)
    if err != nil {
        return nil, errors.Wrap(err, "failed to count agents by status")
    }
    normalized := map[string]int64{
        model.AgentStatusIdle:    0,
        model.AgentStatusRunning: 0,
        model.AgentStatusStopped: 0,
        model.AgentStatusError:   0,
    }
    total := int64(0)
    for status, count := range counts {
        normalized[status] = count
        total += count
    }

    running, _, err := s.agents.List(ctx, repository.AgentListFilter{
        Status:   model.AgentStatusRunning,
        Page:     1,
        PageSize: 50,
    })
    if err != nil {
        return nil, errors.Wrap(err, "failed to list running agents")
    }
    runningWithInstance := make([]map[string]interface{}, 0, len(running))
    for _, agent := range running {
        item := map[string]interface{}{
            "id":      agent.ID,
            "name":    agent.Name,
            "status":  agent.Status,
            "version": agent.Version,
        }
        if instance, err := s.instances.GetByAgent(ctx, agent.ID); err == nil {
            item["instance_id"] = instance.ID
            item["last_heartbeat"] = instance.LastHeartbeat
            item["started_at"] = instance.StartedAt
        }
        runningWithInstance = append(runningWithInstance, item)
    }

    return map[string]interface{}{
        "status_counts":    normalized,
        "total_agents":     total,
        "running_agents":   runningWithInstance,
    }, nil
}

// ReconcileInstances 服务启动时对账: 将上次进程遗留的活动实例标记为 error
func (s *agentService) ReconcileInstances(ctx context.Context) error {
    orphans, err := s.instances.ReconcileOrphans(ctx)
    if err != nil {
        return errors.Wrap(err, "failed to reconcile agent instances")
    }
    for _, instance := range orphans {
        if agent, err := s.agents.GetByID(ctx, instance.AgentID); err == nil {
            agent.Status = model.AgentStatusError
            if err := s.agents.Update(ctx, agent); err != nil {
                log.Printf("reconcile: failed to update agent %s: %v", agent.ID, err)
            }
        }
        logEntry := &model.AgentLog{
            AgentID:    instance.AgentID,
            InstanceID: &instance.ID,
            Level:      model.LogLevelError,
            Message:    "instance state lost after service restart, marked as error",
        }
        if err := s.logs.Append(ctx, []*model.AgentLog{logEntry}); err != nil {
            log.Printf("reconcile: failed to write log %s: %v", instance.AgentID, err)
        }
    }
    if len(orphans) > 0 {
        log.Printf("reconcile: %d orphan instances marked as error", len(orphans))
    }
    return nil
}

// snapshotVersion 写入版本快照
func (s *agentService) snapshotVersion(ctx context.Context, agent *model.Agent, version int, operatorID string) error {
    snapshot := &model.AgentVersion{
        AgentID:     agent.ID,
        Version:     version,
        Name:        agent.Name,
        Description: agent.Description,
        Config:      agent.Config,
        CreatedBy:   strPtr(operatorID),
    }
    if err := s.versions.Create(ctx, snapshot); err != nil {
        return errors.Wrap(err, "failed to create agent version snapshot")
    }
    return nil
}

// ensureNameAvailable 校验 Agent 名称唯一性
func (s *agentService) ensureNameAvailable(ctx context.Context, name, excludeID string) error {
    existing, err := s.agents.GetByName(ctx, name)
    if err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil
        }
        return errors.Wrap(err, "failed to check agent name")
    }
    if existing.ID != excludeID {
        return errors.NewValidationError("agent name already exists")
    }
    return nil
}

// generateAPIKey 生成 akp_ 前缀的 64 位十六进制 API Key
func generateAPIKey() (string, error) {
    buf := make([]byte, 32)
    if _, err := rand.Read(buf); err != nil {
        return "", err
    }
    return "akp_" + hex.EncodeToString(buf), nil
}

// strPtr 空字符串转 nil (uuid 字段以 NULL 存储)
func strPtr(s string) *string {
    if s == "" {
        return nil
    }
    return &s
}
