package runtime

import (
    "context"
    "fmt"
    "log"
    "math/rand"
    "strings"
    "sync"
    "time"

    "agent-platform/internal/model"
    "agent-platform/internal/repository"
)

// 运行时常量
const (
    heartbeatInterval = 5 * time.Second  // 心跳间隔, 保证状态看板实时性
    heartbeatLogEvery = 6               // 每 N 次心跳写一条心跳日志
    logTrimKeep       = 5000            // 每个 Agent 保留的日志条数上限
)

// Runtime 进程内 Agent 运行时管理器 (Phase 1 模拟运行时)
//
// Phase 1 尚未接入真实 Agent 框架 (MCP/模型在 M3/M4 提供), 运行时负责:
//   - 启停: 启动后台 goroutine 维护实例, 停止时优雅退出
//   - 心跳: 每 5s 刷新 last_heartbeat, 状态看板可据此判断实例存活
//   - 日志: 生成启动/心跳/请求处理等运行日志 (写入 agent_logs)
//   - 流量: 模拟周期性调用, 产生调用统计数据 (写入 agent_call_stats)
//
// Phase 2 可将本包替换为真实的 Agent Runtime 调度器, 对外接口保持不变。
type Runtime struct {
    instances repository.AgentInstanceRepository
    logs      repository.AgentLogRepository
    stats     repository.AgentCallStatRepository

    simulateTraffic bool
    trafficCheck  func(ctx context.Context, agentID string) bool
    mcpInvoker    MCPToolInvoker
    modelRouter   ModelRouter

    mu      sync.Mutex
    entries map[string]*entry
}

type entry struct {
    agentID    string
    instanceID string
    cancel     context.CancelFunc
    done       chan struct{}
}

// New 创建运行时
func New(instances repository.AgentInstanceRepository, logs repository.AgentLogRepository, stats repository.AgentCallStatRepository) *Runtime {
    return &Runtime{
        instances:       instances,
        logs:            logs,
        stats:           stats,
        simulateTraffic: true,
        entries:         make(map[string]*entry),
    }
}

// SetSimulateTraffic 是否模拟调用流量 (生成统计与请求日志)
func (r *Runtime) SetSimulateTraffic(enabled bool) {
    r.simulateTraffic = enabled
}

// SetTrafficCheck 注入 Agent 级模拟流量开关检查 (M2.5: 默认关闭, 由 Agent config simulate_traffic 显式开启)
func (r *Runtime) SetTrafficCheck(fn func(ctx context.Context, agentID string) bool) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.trafficCheck = fn
}

// trafficEnabled 判断 Agent 是否生成模拟流量 (全局开关 && Agent 级开关)
func (r *Runtime) trafficEnabled(ctx context.Context, agentID string) bool {
    if !r.simulateTraffic {
        return false
    }
    r.mu.Lock()
    fn := r.trafficCheck
    r.mu.Unlock()
    if fn == nil {
        return false
    }
    return fn(ctx, agentID)
}

// PendingApproval 生成的待审核请求 (M4.5: 对应工具调用未实际执行)
type PendingApproval struct {
    ApprovalID string `json:"approval_id"`
    MCPName    string `json:"mcp_name"`
    ToolName   string `json:"tool_name"`
}

// MCPToolInvoker 调用 Agent 绑定的 MCP 工具 (M3 6.6: Agent -> MCP 调用代理)
// 实现方为 MCP 服务层; details 为调用结果日志行, failed 为失败计数, pending 为生成的待审核请求 (M4.5)
type MCPToolInvoker interface {
    InvokeMCPTools(ctx context.Context, agentID, source string) (details []string, failed int, pending []PendingApproval)
}

// SetMCPInvoker 注入 MCP 工具调用器 (可选, 未设置时模拟流量不调用 MCP)
func (r *Runtime) SetMCPInvoker(invoker MCPToolInvoker) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.mcpInvoker = invoker
}

// ModelRouter 按优先级路由模型并消费配额 (M4 6.5: Agent -> 模型)
// 实现方为模型服务层; 返回日志行与是否路由成功
type ModelRouter interface {
    RouteAndConsume(ctx context.Context, agentID string, tokens, latencyMs int, failed bool) (detail string, ok bool)
}

// SetModelRouter 注入模型路由器 (可选, 未设置时模拟流量不路由模型)
func (r *Runtime) SetModelRouter(router ModelRouter) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.modelRouter = router
}

// IsRunning 判断 Agent 实例是否在本进程内运行
func (r *Runtime) IsRunning(agentID string) bool {
    r.mu.Lock()
    defer r.mu.Unlock()
    _, ok := r.entries[agentID]
    return ok
}

// Start 启动 Agent 实例的后台 goroutine
func (r *Runtime) Start(ctx context.Context, agentID, instanceID string) error {
    r.mu.Lock()
    if _, ok := r.entries[agentID]; ok {
        r.mu.Unlock()
        return fmt.Errorf("instance already running")
    }
    runCtx, cancel := context.WithCancel(ctx)
    entry := &entry{
        agentID:    agentID,
        instanceID: instanceID,
        cancel:     cancel,
        done:       make(chan struct{}),
    }
    r.entries[agentID] = entry
    r.mu.Unlock()

    go r.run(runCtx, agentID, instanceID, entry)
    return nil
}

// Stop 停止 Agent 实例, 等待 goroutine 退出; 返回是否原本在运行
func (r *Runtime) Stop(agentID string) (bool, error) {
    r.mu.Lock()
    entry, ok := r.entries[agentID]
    if ok {
        entry.cancel()
    }
    r.mu.Unlock()

    if !ok {
        return false, nil
    }

    select {
    case <-entry.done:
    case <-time.After(5 * time.Second):
        log.Printf("runtime: instance %s did not exit in time", agentID)
    }
    return true, nil
}

// Shutdown 停止所有实例 (服务退出时调用)
func (r *Runtime) Shutdown() {
    r.mu.Lock()
    entries := make([]*entry, 0, len(r.entries))
    for _, e := range r.entries {
        entries = append(entries, e)
    }
    r.mu.Unlock()

    for _, e := range entries {
        e.cancel()
    }
    for _, e := range entries {
        select {
        case <-e.done:
        case <-time.After(3 * time.Second):
        }
    }
}

// run 实例后台循环: 心跳 + 模拟流量
func (r *Runtime) run(ctx context.Context, agentID, instanceID string, entry *entry) {
    defer func() {
        r.mu.Lock()
        delete(r.entries, agentID)
        r.mu.Unlock()
        close(entry.done)
    }()

    r.writeLog(ctx, agentID, instanceID, model.LogLevelInfo, "instance started (runtime=local-sim)")

    heartbeat := time.NewTicker(heartbeatInterval)
    defer heartbeat.Stop()
    beat := 0

    // Traffic ticker is always created: select evaluates traffic.C for every case, and a
    // nil traffic would panic with a nil pointer dereference (reproduced in M2.5 E2E).
    // Whether traffic is actually generated is re-checked on each tick (dynamic toggle).
    traffic := time.NewTicker(r.nextTrafficInterval())
    defer traffic.Stop()
    trafficOn := r.trafficEnabled(ctx, agentID)
    if trafficOn {
        r.writeLog(ctx, agentID, instanceID, model.LogLevelInfo, "instance ready (simulated_traffic=on)")
    } else {
        r.writeLog(ctx, agentID, instanceID, model.LogLevelInfo, "instance ready (simulated_traffic=off)")
    }

    for {
        select {
        case <-ctx.Done():
            r.writeLog(ctx, agentID, instanceID, model.LogLevelInfo, "instance stopped (reason=stop)")
            return

        case <-heartbeat.C:
            beat++
            if err := r.instances.TouchHeartbeat(context.Background(), agentID, time.Now()); err != nil {
                log.Printf("runtime: heartbeat update failed for %s: %v", agentID, err)
            }
            if beat%heartbeatLogEvery == 0 {
                r.writeLog(ctx, agentID, instanceID, model.LogLevelDebug, fmt.Sprintf("heartbeat ok (beat=%d)", beat))
            }

        case <-traffic.C:
            if r.trafficEnabled(ctx, agentID) {
                r.simulateCall(ctx, agentID, instanceID)
            }
            traffic.Reset(r.nextTrafficInterval())
        }
    }
}

// simulateCall 模拟一次 Agent 调用, 写入统计与日志
func (r *Runtime) simulateCall(ctx context.Context, agentID, instanceID string) {
    latencyMs := int64(200 + rand.Intn(1800))
    tokens := int64(50 + rand.Intn(450))
    failed := rand.Intn(100) < 8 // 8% 失败率

    errs := int64(0)
    if failed {
        errs = 1
    }
    if err := r.stats.Increment(context.Background(), agentID, time.Now(), 1, errs, tokens, latencyMs); err != nil {
        log.Printf("runtime: stat increment failed for %s: %v", agentID, err)
    }

    traceID := fmt.Sprintf("%08x", rand.Uint32())
    if failed {
        msg := fmt.Sprintf("simulated request failed trace_id=%s error=upstream_timeout latency=%dms", traceID, latencyMs)
        r.writeLog(ctx, agentID, instanceID, model.LogLevelError, msg)
    } else {
        msg := fmt.Sprintf("simulated request ok trace_id=%s latency=%dms tokens=%d", traceID, latencyMs, tokens)
        r.writeLog(ctx, agentID, instanceID, model.LogLevelInfo, msg)
    }

    modelDetail, modelOK, mcpDetails, _ := r.executeCall(ctx, agentID, int(tokens), int(latencyMs), failed, model.ApprovalSourceRuntime)
    if modelDetail != "" {
        level := model.LogLevelInfo
        if !modelOK {
            level = model.LogLevelWarn
        }
        r.writeLog(ctx, agentID, instanceID, level, modelDetail)
    }
    for _, detail := range mcpDetails {
        r.writeLog(ctx, agentID, instanceID, mcpLogLevel(detail), detail)
    }
}

// executeCall 执行 Agent 调用链: 模型路由消费配额 (M4 6.5) + 绑定 MCP 工具调用 (M3 6.6)
// 返回模型路由日志行/是否成功、MCP 调用日志行与生成的待审核请求 (M4.5);
// source 区分调用来源 (runtime/api_invoke), 模拟流量与 API Key 真实调用共用
func (r *Runtime) executeCall(ctx context.Context, agentID string, tokens, latencyMs int, failed bool, source string) (modelDetail string, modelOK bool, mcpDetails []string, pending []PendingApproval) {
    r.mu.Lock()
    router := r.modelRouter
    invoker := r.mcpInvoker
    r.mu.Unlock()
    if router != nil {
        modelDetail, modelOK = router.RouteAndConsume(ctx, agentID, tokens, latencyMs, failed)
    }
    if !failed && invoker != nil {
        mcpDetails, _, pending = invoker.InvokeMCPTools(ctx, agentID, source)
    }
    return
}

// mcpLogLevel MCP 调用日志行级别 (含 failed/error 视为 warn)
func mcpLogLevel(detail string) string {
    if strings.Contains(detail, "failed") || strings.Contains(detail, "error") {
        return model.LogLevelWarn
    }
    return model.LogLevelInfo
}

// InvokeResult 一次真实 Agent 调用的执行结果 (API Key 触发)
type InvokeResult struct {
    ModelDetail      string            // 模型路由日志行 (未接入模型路由时为空)
    ModelOK          bool              // 模型路由是否成功
    MCPDetails       []string          // MCP 工具调用日志行
    PendingApprovals []PendingApproval // 生成的待审核请求 (M4.5: 对应工具未执行)
    Tokens           int               // 本次调用 token 估算
    LatencyMs        int64             // 端到端耗时
}

// InvokeOnce 执行一次真实 Agent 调用 (M2 待办: API Key 调用链):
// 模型路由消费配额 (M4) + 绑定 MCP 工具调用 (M3), 写入调用统计与运行日志
func (r *Runtime) InvokeOnce(ctx context.Context, agentID string, tokens int) InvokeResult {
    start := time.Now()
    result := InvokeResult{Tokens: tokens}
    result.ModelDetail, result.ModelOK, result.MCPDetails, result.PendingApprovals = r.executeCall(ctx, agentID, tokens, 0, false, model.ApprovalSourceAPIInvoke)
    result.LatencyMs = time.Since(start).Milliseconds()

    if err := r.stats.Increment(context.Background(), agentID, time.Now(), 1, 0, int64(tokens), result.LatencyMs); err != nil {
        log.Printf("runtime: stat increment failed for %s: %v", agentID, err)
    }

    traceID := fmt.Sprintf("%08x", rand.Uint32())
    level := model.LogLevelInfo
    if !result.ModelOK {
        level = model.LogLevelWarn
    }
    r.writeLogAny(ctx, agentID, nil, level, fmt.Sprintf("api invoke ok trace_id=%s latency=%dms tokens=%d", traceID, result.LatencyMs, tokens))
    if result.ModelDetail != "" {
        r.writeLogAny(ctx, agentID, nil, model.LogLevelInfo, result.ModelDetail)
    }
    for _, detail := range result.MCPDetails {
        r.writeLogAny(ctx, agentID, nil, mcpLogLevel(detail), detail)
    }
    return result
}

// writeLog 写入一条运行日志并控制日志总量
func (r *Runtime) writeLog(ctx context.Context, agentID, instanceID, level, message string) {
    id := instanceID
    r.writeLogAny(ctx, agentID, &id, level, message)
}

// writeLogAny 写入运行日志, instanceID 可为 nil (API Key 触发的一次性调用无实例)
func (r *Runtime) writeLogAny(ctx context.Context, agentID string, instanceID *string, level, message string) {
    entry := &model.AgentLog{
        AgentID:    agentID,
        InstanceID: instanceID,
        Level:      level,
        Message:    message,
    }
    if err := r.logs.Append(context.Background(), []*model.AgentLog{entry}); err != nil {
        log.Printf("runtime: write log failed for %s: %v", agentID, err)
        return
    }
    if err := r.logs.Trim(context.Background(), agentID, logTrimKeep); err != nil {
        log.Printf("runtime: trim logs failed for %s: %v", agentID, err)
    }
}

// nextTrafficInterval 返回 8~15 秒的随机流量间隔
func (r *Runtime) nextTrafficInterval() time.Duration {
    return time.Duration(8+rand.Intn(8)) * time.Second
}