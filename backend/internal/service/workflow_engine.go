package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agent-platform/internal/mcpclient"
	"agent-platform/internal/model"
	"agent-platform/internal/repository"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// 默认节点超时 (秒)
const DefaultNodeTimeoutSeconds = 300

// httpBodyLimit HTTP 节点响应体读取上限
const httpBodyLimit = 1 << 20

// WorkflowMCPGateway MCP 调用网关 (由 MCPServerService 实现)
type WorkflowMCPGateway interface {
	ExecuteTool(ctx context.Context, mcpID, tool string, arguments map[string]interface{}) (*mcpclient.CallResult, error)
	ToolRequiresApproval(ctx context.Context, mcpID, tool string) (bool, error)
}

// approvalPendingError MCP 节点需人工审核, 节点挂起等待
type approvalPendingError struct {
	approvalID string
}

func (e *approvalPendingError) Error() string {
	return "waiting for approval: " + e.approvalID
}

// WorkflowEngine DAG 执行引擎 (M5): 依赖调度 + 重试/超时 + 审核挂起恢复 + 取消
type WorkflowEngine struct {
	executions  repository.WorkflowExecutionRepository
	nodes       repository.WorkflowNodeExecutionRepository
	workflows   repository.WorkflowRepository
	approvals   repository.ToolApprovalRepository
	mcp         WorkflowMCPGateway
	approvalSvc ApprovalService
	chat        ChatService

	mu       sync.Mutex
	cancelCh map[string]chan struct{} // executionID -> 取消通道

	lockMu sync.Mutex
	locks  map[string]*sync.Mutex // executionID -> 互斥锁 (恢复串行化)
}

func NewWorkflowEngine(
	workflows repository.WorkflowRepository,
	executions repository.WorkflowExecutionRepository,
	nodes repository.WorkflowNodeExecutionRepository,
	approvals repository.ToolApprovalRepository,
	mcp WorkflowMCPGateway,
	approvalSvc ApprovalService,
	chat ChatService,
) *WorkflowEngine {
	return &WorkflowEngine{
		executions:  executions,
		nodes:       nodes,
		workflows:   workflows,
		approvals:   approvals,
		mcp:         mcp,
		approvalSvc: approvalSvc,
		chat:        chat,
		cancelCh:    make(map[string]chan struct{}),
		locks:       make(map[string]*sync.Mutex),
	}
}

// ---------- 生命周期 ----------

// Start 启动执行 (创建执行记录 + 节点记录后由服务层调用)
func (e *WorkflowEngine) RunAsync(execID string) {
	e.mu.Lock()
	if _, exists := e.cancelCh[execID]; exists {
		e.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	e.cancelCh[execID] = ch
	e.mu.Unlock()

	go func() {
		defer e.deregister(execID)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-ch:
				cancel()
			case <-ctx.Done():
			}
		}()
		e.loop(ctx, execID)
	}()
}

// Cancel 取消执行: 运行中 -> 取消; 审核挂起 -> 直接置终态
func (e *WorkflowEngine) Cancel(ctx context.Context, execID string) error {
	e.mu.Lock()
	ch, active := e.cancelCh[execID]
	e.mu.Unlock()

	exec, err := e.executions.Get(ctx, execID)
	if err != nil {
		return err
	}
	if exec.Status == model.ExecutionStatusRunning {
		if active {
			close(ch)
			return nil
		}
		// 运行中但引擎未接管 (异常状态), 直接置终态
		return e.forceTerminal(ctx, exec, model.ExecutionStatusCancelled, "手动取消")
	}
	if exec.Status == model.ExecutionStatusWaitingApproval {
		return e.forceTerminal(ctx, exec, model.ExecutionStatusCancelled, "手动取消")
	}
	return fmt.Errorf("执行已处于终态 (%s), 无法取消", exec.Status)
}

func (e *WorkflowEngine) forceTerminal(ctx context.Context, exec *model.WorkflowExecution, status, errMsg string) error {
	now := time.Now()
	ok, err := e.executions.MarkFinished(ctx, exec.ID, status, errMsg, nil, nil, now)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("执行已处于终态, 无法取消")
	}
	_ = e.nodes.MarkAll(ctx, exec.ID, model.NodeStatusCancelled)
	e.deregister(exec.ID)
	return nil
}

// ReconcileOnStartup 启动对账: 上次进程遗留的 running 执行置为失败; waiting_approval 保留 (审核决策后可恢复)
func (e *WorkflowEngine) ReconcileOnStartup(ctx context.Context) {
	active, err := e.executions.ListActive(ctx)
	if err != nil {
		log.Printf("workflow: reconcile list active failed: %v", err)
		return
	}
	now := time.Now()
	for i := range active {
		exec := active[i]
		if exec.Status != model.ExecutionStatusRunning {
			continue
		}
		if _, err := e.executions.MarkFinished(ctx, exec.ID, model.ExecutionStatusFailed, "服务重启, 执行中断", nil, nil, now); err == nil {
			_ = e.nodes.MarkAll(ctx, exec.ID, model.NodeStatusFailed)
			log.Printf("workflow: reconciled interrupted execution %s", exec.ID)
		}
	}
}

func (e *WorkflowEngine) deregister(execID string) {
	e.mu.Lock()
	delete(e.cancelCh, execID)
	e.mu.Unlock()
}

func (e *WorkflowEngine) execLock(execID string) *sync.Mutex {
	e.lockMu.Lock()
	defer e.lockMu.Unlock()
	if lock, ok := e.locks[execID]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	e.locks[execID] = lock
	return lock
}

// ---------- 主循环 ----------

type nodeState struct {
	def    *WorkflowNodeDef
	record *model.WorkflowNodeExecution
}

func (e *WorkflowEngine) loop(ctx context.Context, execID string) {
	var def *WorkflowDefinition
	for {
		if ctx.Err() != nil {
			// 取消收尾: 用后台 context 将执行置为 cancelled (幂等, 已被其他终态接管时 MarkFinished 不生效)
			now := time.Now()
			if ok, err := e.executions.MarkFinished(context.Background(), execID, model.ExecutionStatusCancelled, "手动取消", nil, nil, now); err == nil && ok {
				_ = e.nodes.MarkAll(context.Background(), execID, model.NodeStatusCancelled)
				log.Printf("workflow: execution %s cancelled", execID)
			}
			e.deregister(execID)
			return
		}
		exec, err := e.executions.Get(ctx, execID)
		if err != nil || exec.Status != model.ExecutionStatusRunning {
			return
		}
		if def == nil {
			workflow, err := e.workflows.Get(ctx, exec.WorkflowID)
			if err != nil {
				_ = e.finish(ctx, execID, model.ExecutionStatusFailed, "工作流定义不存在 (可能已删除)", nil, nil)
				return
			}
			def, err = ParseDefinition(workflow.Definition)
			if err != nil {
				_ = e.finish(ctx, execID, model.ExecutionStatusFailed, "DAG 定义校验失败: "+err.Error(), nil, nil)
				return
			}
		}

		records, err := e.nodes.ListByExecution(ctx, execID)
		if err != nil {
			log.Printf("workflow: list node executions failed exec=%s: %v", execID, err)
			return
		}
		stateMap := make(map[string]*nodeState, len(def.Nodes))
		for i := range def.Nodes {
			nodeDef := &def.Nodes[i]
			rec := findNodeRecord(records, nodeDef.ID)
			if rec != nil {
				stateMap[nodeDef.ID] = &nodeState{def: nodeDef, record: rec}
			}
		}

		// 1. 判定可跳过节点 (上游失败 / 分支未选中 / 全部上游被跳过)
		for _, ns := range stateMap {
			if ns.record.Status != model.NodeStatusPending {
				continue
			}
			verdict, reason := e.skipVerdict(def, ns.def.ID, stateMap)
			if verdict {
				now := time.Now()
				ns.record.Status = model.NodeStatusSkipped
				ns.record.Error = reason
				ns.record.FinishedAt = &now
				_ = e.nodes.Update(ctx, ns.record)
			}
		}

		// 2. 找就绪节点 (pending 且全部上游终态)
		var ready []*nodeState
		anyWaiting := false
		allTerminal := true
		for _, ns := range stateMap {
			switch ns.record.Status {
			case model.NodeStatusSuccess:
			case model.NodeStatusFailed:
			case model.NodeStatusSkipped:
			case model.NodeStatusCancelled:
			case model.NodeStatusWaitingApproval:
				anyWaiting = true
			default:
				allTerminal = false
				if allTerminalStatus(def, ns.def.ID, stateMap) {
					ready = append(ready, ns)
				}
			}
		}

		// 3. 失败即终止: 有节点失败 -> 整个执行失败
		for _, ns := range stateMap {
			if ns.record.Status == model.NodeStatusFailed {
				_ = e.nodes.MarkAll(ctx, execID, model.NodeStatusSkipped)
				errMsg := fmt.Sprintf("节点 %s (%s) 执行失败: %s", ns.def.ID, ns.def.Name, ns.record.Error)
				_ = e.finish(ctx, execID, model.ExecutionStatusFailed, errMsg, e.collectOutput(stateMap), e.collectPrintOutput(def, stateMap))
				return
			}
		}

		// 4. 审核挂起: 有节点等待审核 -> 执行挂起, 循环退出等待恢复
		if anyWaiting {
			exec.Status = model.ExecutionStatusWaitingApproval
			if err := e.executions.Update(ctx, exec); err == nil {
				log.Printf("workflow: execution %s suspended, waiting for approval(s)", execID)
			}
			e.deregister(execID)
			return
		}

		// 5. 全部终态 -> 成功
		if allTerminal {
			_ = e.finish(ctx, execID, model.ExecutionStatusSuccess, "", e.collectOutput(stateMap), e.collectPrintOutput(def, stateMap))
			return
		}

		if len(ready) == 0 {
			// 无就绪且未全部终态: 理论上校验过的 DAG 不会发生, 兜底防死锁
			_ = e.finish(ctx, execID, model.ExecutionStatusFailed, "DAG 调度死锁 (无可执行节点)", nil, e.collectPrintOutput(def, stateMap))
			return
		}

		// 6. 并行执行就绪节点
		var wg sync.WaitGroup
		for _, ns := range ready {
			wg.Add(1)
			go func(ns *nodeState) {
				defer wg.Done()
				e.runNode(ctx, execID, def, ns, stateMap)
			}(ns)
		}
		wg.Wait()
	}
}

// skipVerdict 判定 pending 节点是否应跳过
func (e *WorkflowEngine) skipVerdict(def *WorkflowDefinition, nodeID string, stateMap map[string]*nodeState) (bool, string) {
	edges := incomingEdgesOf(def, nodeID)
	if len(edges) == 0 {
		return false, ""
	}
	for _, edge := range edges {
		pred := stateMap[edge.Source]
		if pred == nil {
			continue
		}
		if pred.record.Status == model.NodeStatusFailed {
			return true, "上游节点 " + pred.def.ID + " 失败"
		}
		if pred.record.Status == model.NodeStatusCancelled {
			return true, "上游节点 " + pred.def.ID + " 已取消"
		}
	}
	for _, edge := range edges {
		pred := stateMap[edge.Source]
		if pred == nil {
			continue
		}
		if !isTerminalNodeStatus(pred.record.Status) {
			return false, "" // 还有上游未结束, 不能判定
		}
		if pred.record.Status == model.NodeStatusSuccess || pred.record.Status == model.NodeStatusWaitingApproval {
			// 成功 (或审核中) 的上游: 检查条件边是否命中
			if edge.Condition != "" && !conditionMatches(pred, edge.Condition) {
				continue // 该边未命中, 不作为可执行依据
			}
			return false, ""
		}
		// 上游 skipped: 继续看其他边
	}
	return true, "上游节点均被跳过或分支未命中"
}

// allTerminalStatus 节点的全部上游是否都终态
func allTerminalStatus(def *WorkflowDefinition, nodeID string, stateMap map[string]*nodeState) bool {
	for _, edge := range incomingEdgesOf(def, nodeID) {
		pred := stateMap[edge.Source]
		if pred == nil {
			continue
		}
		if !isTerminalNodeStatus(pred.record.Status) {
			return false
		}
	}
	return true
}

func isTerminalNodeStatus(status string) bool {
	switch status {
	case model.NodeStatusSuccess, model.NodeStatusFailed, model.NodeStatusSkipped, model.NodeStatusCancelled:
		return true
	}
	return false
}

// conditionMatches 条件节点输出是否命中边的 condition 标签
func conditionMatches(pred *nodeState, condition string) bool {
	if pred.record.Output == nil {
		return false
	}
	var out struct {
		Chosen string `json:"chosen"`
	}
	if err := json.Unmarshal(pred.record.Output, &out); err != nil {
		return false
	}
	return out.Chosen == condition
}

func incomingEdgesOf(def *WorkflowDefinition, nodeID string) []WorkflowEdgeDef {
	result := make([]WorkflowEdgeDef, 0)
	for i := range def.Edges {
		if def.Edges[i].Target == nodeID {
			result = append(result, def.Edges[i])
		}
	}
	return result
}

func findNodeRecord(records []model.WorkflowNodeExecution, nodeID string) *model.WorkflowNodeExecution {
	for i := range records {
		if records[i].NodeID == nodeID {
			return &records[i]
		}
	}
	return nil
}

func (e *WorkflowEngine) collectOutput(stateMap map[string]*nodeState) datatypes.JSON {
	output := make(map[string]interface{})
	for id, ns := range stateMap {
		var value interface{}
		if len(ns.record.Output) > 0 {
			_ = json.Unmarshal(ns.record.Output, &value)
		}
		output[id] = value
	}
	payload, _ := json.Marshal(output)
	return payload
}

// printOutputEntry 执行历史「工作流输出」的单节点条目 (前端按 message/color 逐行着色展示)
type printOutputEntry struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Message  string `json:"message"`
	Color    string `json:"color,omitempty"`
}

// collectPrintOutput 汇总打印输出: 按 DAG 定义顺序取已成功的 print 节点, 无则返回 nil
func (e *WorkflowEngine) collectPrintOutput(def *WorkflowDefinition, stateMap map[string]*nodeState) datatypes.JSON {
	var entries []printOutputEntry
	for i := range def.Nodes {
		nodeDef := &def.Nodes[i]
		if nodeDef.Type != model.NodeTypePrint {
			continue
		}
		ns := stateMap[nodeDef.ID]
		if ns == nil || ns.record.Status != model.NodeStatusSuccess || len(ns.record.Output) == 0 {
			continue
		}
		var out struct {
			Message string `json:"message"`
			Color   string `json:"color"`
		}
		if json.Unmarshal(ns.record.Output, &out) != nil {
			continue
		}
		name := strings.TrimSpace(nodeDef.Name)
		if name == "" {
			name = nodeDef.ID
		}
		entries = append(entries, printOutputEntry{NodeID: nodeDef.ID, NodeName: name, Message: out.Message, Color: out.Color})
	}
	if len(entries) == 0 {
		return nil
	}
	payload, _ := json.Marshal(entries)
	return payload
}

func (e *WorkflowEngine) finish(ctx context.Context, execID, status, errMsg string, output, printOutput datatypes.JSON) error {
	now := time.Now()
	ok, err := e.executions.MarkFinished(ctx, execID, status, errMsg, output, printOutput, now)
	if err != nil {
		return err
	}
	if !ok {
		log.Printf("workflow: execution %s already in terminal state, skip finish(%s)", execID, status)
		return nil
	}
	log.Printf("workflow: execution %s finished status=%s", execID, status)
	e.deregister(execID)
	return nil
}

// ---------- 节点执行 (含重试/超时) ----------

func (e *WorkflowEngine) runNode(ctx context.Context, execID string, def *WorkflowDefinition, ns *nodeState, stateMap map[string]*nodeState) {
	nodeDef := ns.def
	rec := ns.record

	// 变量上下文: 输入 + 当前已知节点输出
	varCtx := e.buildVarContext(execID, stateMap)

	maxAttempts := 1
	intervalSec := 0
	backoff := "fixed"
	if nodeDef.Retry != nil {
		maxAttempts = nodeDef.Retry.MaxAttempts
		intervalSec = nodeDef.Retry.IntervalSeconds
		if nodeDef.Retry.Backoff != "" {
			backoff = nodeDef.Retry.Backoff
		}
	}
	timeoutSec := nodeDef.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = DefaultNodeTimeoutSeconds
	}

	startedAt := time.Now()
	rec.Status = model.NodeStatusRunning
	rec.StartedAt = &startedAt
	rec.Attempt = 0
	_ = e.nodes.Update(context.Background(), rec)
	log.Printf("workflow: node start exec=%s node=%s type=%s", execID, nodeDef.ID, nodeDef.Type)

	var lastErr error
	var nodeOutput interface{}
	var pending *approvalPendingError
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		rec.Attempt = attempt
		nodeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		resolvedConfig, _ := ResolveVariables(nodeDef.Config, varCtx).(map[string]interface{})
		inputPayload, _ := json.Marshal(resolvedConfig)
		rec.Input = inputPayload
		_ = e.nodes.Update(context.Background(), rec)

		attemptOutput, err := e.runOnce(nodeCtx, nodeDef, resolvedConfig, varCtx)
		cancel()
		if err == nil {
			nodeOutput = attemptOutput
			lastErr = nil
			break
		}
		if pendingErr, ok := err.(*approvalPendingError); ok {
			pending = pendingErr
			lastErr = nil
			break
		}
		if nodeCtx.Err() == context.DeadlineExceeded {
			lastErr = fmt.Errorf("节点超时 (>%ds)", timeoutSec)
		} else {
			lastErr = err
		}
		if ctx.Err() != nil {
			lastErr = nil // 外部取消, 按取消处理
			break
		}
		if attempt < maxAttempts {
			wait := time.Duration(intervalSec) * time.Second
			if backoff == "exponential" && intervalSec > 0 {
				wait = time.Duration(intervalSec*(1<<uint(attempt-1))) * time.Second
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				lastErr = nil
				attempt = maxAttempts
			}
		}
	}

	now := time.Now()
	rec.FinishedAt = &now
	rec.DurationMs = now.Sub(*rec.StartedAt).Milliseconds()

	switch {
	case pending != nil:
		rec.Status = model.NodeStatusWaitingApproval
		rec.ApprovalID = &pending.approvalID
		rec.Error = ""
		log.Printf("workflow: node %s suspended, approval=%s", nodeDef.ID, pending.approvalID)
	case ctx.Err() != nil:
		rec.Status = model.NodeStatusCancelled
		rec.Error = "执行已取消"
	case lastErr != nil:
		rec.Status = model.NodeStatusFailed
		rec.Error = lastErr.Error()
		log.Printf("workflow: node failed exec=%s node=%s: %v", execID, nodeDef.ID, lastErr)
	default:
		rec.Status = model.NodeStatusSuccess
		payload, _ := json.Marshal(nodeOutput)
		rec.Output = payload
	}
	_ = e.nodes.Update(context.Background(), rec)
}

func (e *WorkflowEngine) buildVarContext(execID string, stateMap map[string]*nodeState) *VarContext {
	varCtx := &VarContext{
		ExecutionID: execID,
		NodeOutputs: make(map[string]interface{}),
	}
	exec, err := e.executions.Get(context.Background(), execID)
	if err == nil {
		if len(exec.Input) > 0 {
			_ = json.Unmarshal(exec.Input, &varCtx.Inputs)
		}
		if exec.TriggeredBy != nil {
			varCtx.TriggeredBy = *exec.TriggeredBy
		}
	}
	if varCtx.Inputs == nil {
		varCtx.Inputs = map[string]interface{}{}
	}
	for id, ns := range stateMap {
		if ns.record.Status == model.NodeStatusSuccess && len(ns.record.Output) > 0 {
			var value interface{}
			if json.Unmarshal(ns.record.Output, &value) == nil {
				varCtx.NodeOutputs[id] = value
			}
		}
	}
	return varCtx
}

// runOnce 单次执行节点
func (e *WorkflowEngine) runOnce(ctx context.Context, nodeDef *WorkflowNodeDef, config map[string]interface{}, varCtx *VarContext) (interface{}, error) {
	switch nodeDef.Type {
	case model.NodeTypeAgent:
		return e.runAgentNode(ctx, config, varCtx)
	case model.NodeTypeMCPTool:
		return e.runMCPNode(ctx, config, varCtx)
	case model.NodeTypeHTTP:
		return e.runHTTPNode(ctx, config)
	case model.NodeTypeDelay:
		return e.runDelayNode(ctx, config)
	case model.NodeTypeCondition:
		return e.runConditionNode(config)
	case model.NodeTypePrint:
		return e.runPrintNode(config, varCtx)
	}
	return nil, fmt.Errorf("未知节点类型: %s", nodeDef.Type)
}

func (e *WorkflowEngine) runAgentNode(ctx context.Context, config map[string]interface{}, varCtx *VarContext) (interface{}, error) {
	agentID, _ := config["agent_id"].(string)
	message, _ := ResolveVariables(config["message"], varCtx).(string)
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("agent 节点消息为空 (config.message 解析后为空)")
	}
	result, err := e.chat.Chat(ctx, agentID, ChatRequest{Message: message}, varCtx.TriggeredBy)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"reply":             result.Reply,
		"session_id":        result.SessionID,
		"model_name":        result.ModelName,
		"total_tokens":      result.TotalTokens,
		"latency_ms":        result.LatencyMs,
		"mcp_calls":         result.MCPCalls,
		"pending_approvals": len(result.PendingApprovals),
	}, nil
}

func (e *WorkflowEngine) runMCPNode(ctx context.Context, config map[string]interface{}, varCtx *VarContext) (interface{}, error) {
	mcpID, _ := config["mcp_server_id"].(string)
	tool, _ := config["tool"].(string)
	arguments := map[string]interface{}{}
	if rawArgs, ok := config["arguments"]; ok && rawArgs != nil {
		resolved := ResolveVariables(rawArgs, varCtx)
		if m, ok := resolved.(map[string]interface{}); ok {
			arguments = m
		}
	}

	requires, err := e.mcp.ToolRequiresApproval(ctx, mcpID, tool)
	if err != nil {
		return nil, err
	}
	if requires {
		approval, err := e.approvalSvc.CreateRequest(ctx, CreateApprovalRequest{
			MCPServerID:         mcpID,
			ToolName:            tool,
			Source:              model.ApprovalSourceWorkflow,
			Arguments:           arguments,
			WorkflowExecutionID: &varCtx.ExecutionID,
		})
		if err != nil {
			return nil, fmt.Errorf("创建审核请求失败: %w", err)
		}
		return nil, &approvalPendingError{approvalID: approval.ID}
	}

	callResult, err := e.mcp.ExecuteTool(ctx, mcpID, tool, arguments)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"content":  callResult.Content,
		"text":     flattenMCPText(callResult.Content),
		"is_error": callResult.IsError,
	}, nil
}

// flattenMCPText 展平 MCP 工具返回的文本块 (节点输出 text 字段, 便于下游节点引用)
func flattenMCPText(content []mcpclient.ToolContent) string {
	texts := make([]string, 0, len(content))
	for _, block := range content {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func (e *WorkflowEngine) runHTTPNode(ctx context.Context, config map[string]interface{}) (interface{}, error) {
	url, _ := config["url"].(string)
	method, _ := config["method"].(string)
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}

	var bodyReader io.Reader = http.NoBody
	if body, ok := config["body"]; ok && body != nil && method != http.MethodGet && method != http.MethodDelete {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("http body 序列化失败: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if headers, ok := config["headers"]; ok {
		if headerMap, ok := headers.(map[string]interface{}); ok {
			for key, value := range headerMap {
				if s, ok := value.(string); ok {
					req.Header.Set(key, s)
				}
			}
		}
	}

	client := &http.Client{}
	startedAt := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, httpBodyLimit))
	if err != nil {
		return nil, err
	}
	var bodyValue interface{}
	if len(rawBody) > 0 {
		if json.Unmarshal(rawBody, &bodyValue) == nil {
			// JSON 原样保留
		} else {
			bodyValue = string(rawBody)
		}
	}
	return map[string]interface{}{
		"status_code": resp.StatusCode,
		"body":        bodyValue,
		"latency_ms":  time.Since(startedAt).Milliseconds(),
	}, nil
}

func (e *WorkflowEngine) runDelayNode(ctx context.Context, config map[string]interface{}) (interface{}, error) {
	seconds := 0.0
	switch v := config["seconds"].(type) {
	case float64:
		seconds = v
	case int:
		seconds = float64(v)
	}
	if seconds <= 0 {
		seconds = 1
	}
	select {
	case <-time.After(time.Duration(seconds * float64(time.Second))):
		return map[string]interface{}{"waited_seconds": seconds}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (e *WorkflowEngine) runPrintNode(config map[string]interface{}, varCtx *VarContext) (interface{}, error) {
	message := formatValue(ResolveVariables(config["message"], varCtx))
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("打印输出内容解析后为空 (config.message)")
	}
	color, _ := config["color"].(string)
	return map[string]interface{}{
		"message": message,
		"color":   color,
	}, nil
}

func (e *WorkflowEngine) runConditionNode(config map[string]interface{}) (interface{}, error) {
	op, _ := config["operator"].(string)
	left := config["left"]
	var right interface{}
	if _, ok := config["right"]; ok {
		right = config["right"]
	}
	result := evaluateCondition(left, op, right)
	chosen := "false"
	if result {
		chosen = "true"
	}
	return map[string]interface{}{
		"result": result,
		"chosen": chosen,
	}, nil
}

// evaluateCondition 条件求值 (config 已完成变量解析)
func evaluateCondition(left interface{}, op string, right interface{}) bool {
	switch op {
	case "exists":
		return left != nil
	case "==":
		return looseEqual(left, right)
	case "!=":
		return !looseEqual(left, right)
	case "contains":
		return containsValue(left, right)
	case ">", "<", ">=", "<=":
		lf, lok := toFloat(left)
		rf, rok := toFloat(right)
		if !lok || !rok {
			return false
		}
		switch op {
		case ">":
			return lf > rf
		case "<":
			return lf < rf
		case ">=":
			return lf >= rf
		case "<=":
			return lf <= rf
		}
	}
	return false
}

func looseEqual(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == b
	}
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
	}
	return formatValue(a) == formatValue(b)
}

func containsValue(haystack, needle interface{}) bool {
	switch h := haystack.(type) {
	case string:
		n, _ := needle.(string)
		return strings.Contains(h, n)
	case []interface{}:
		for _, item := range h {
			if looseEqual(item, needle) {
				return true
			}
		}
	}
	return false
}

func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// ---------- 审核挂起 / 恢复 (M4.5 联动) ----------

// ResumeAfterApproval 审核决策后恢复执行 (由审核服务决策钩子调用)
func (e *WorkflowEngine) ResumeAfterApproval(ctx context.Context, approval *model.ToolApproval) {
	if approval == nil || approval.WorkflowExecutionID == nil {
		return
	}
	execID := *approval.WorkflowExecutionID
	lock := e.execLock(execID)
	lock.Lock()
	defer lock.Unlock()

	exec, err := e.executions.Get(ctx, execID)
	if err != nil {
		return
	}
	if exec.Status != model.ExecutionStatusWaitingApproval {
		return // 已取消/已完成, 无需恢复
	}

	records, err := e.nodes.ListByExecution(ctx, execID)
	if err != nil {
		return
	}
	target := findNodeRecord(records, "") // 占位, 实际按 approval_id 查找
	_ = target
	for i := range records {
		rec := &records[i]
		if rec.Status != model.NodeStatusWaitingApproval || rec.ApprovalID == nil || *rec.ApprovalID != approval.ID {
			continue
		}
		now := time.Now()
		rec.FinishedAt = &now
		if rec.StartedAt != nil {
			rec.DurationMs = now.Sub(*rec.StartedAt).Milliseconds()
		}
		switch approval.Status {
		case model.ApprovalStatusApproved:
			if nodeFailedFromResult(approval.Result) {
				rec.Status = model.NodeStatusFailed
				rec.Error = toolResultError(approval.Result)
			} else {
				rec.Status = model.NodeStatusSuccess
				rec.Output = normalizeApprovalResult(approval.Result)
			}
		default: // rejected / expired
			rec.Status = model.NodeStatusFailed
			reason := "审核被驳回"
			if approval.Status == model.ApprovalStatusExpired {
				reason = "审核超时"
			}
			if approval.Comment != nil && *approval.Comment != "" {
				reason += ": " + *approval.Comment
			}
			rec.Error = reason
		}
		_ = e.nodes.Update(ctx, rec)
		log.Printf("workflow: node %s resolved by approval %s -> %s", rec.NodeID, approval.ID, rec.Status)
	}

	// 仍有其他节点在等审核 -> 继续挂起
	waiting, err := e.nodes.ListWaitingByExecution(ctx, execID)
	if err != nil || len(waiting) > 0 {
		return
	}

	// 恢复执行: waiting_approval -> running
	fresh, err := e.executions.Get(ctx, execID)
	if err != nil || fresh.Status != model.ExecutionStatusWaitingApproval {
		return
	}
	fresh.Status = model.ExecutionStatusRunning
	if err := e.executions.Update(ctx, fresh); err != nil {
		return
	}
	log.Printf("workflow: execution %s resumed after approval", execID)
	e.RunAsync(execID)
}

// normalizeApprovalResult 把审核回填结果 (CallResult JSONB) 归一化为 mcp_tool 节点输出格式
// ({content, text, is_error}), 与常规 MCP 节点执行保持一致; 解析失败时原样返回
func normalizeApprovalResult(result datatypes.JSON) datatypes.JSON {
	var probe map[string]interface{}
	if len(result) == 0 || json.Unmarshal(result, &probe) != nil {
		return result
	}
	// 执行失败回填形如 {"error": "..."}, 原样保留
	if _, hasErr := probe["error"]; hasErr {
		return result
	}
	var cr mcpclient.CallResult
	if json.Unmarshal(result, &cr) != nil {
		return result
	}
	payload, err := json.Marshal(map[string]interface{}{
		"content":  cr.Content,
		"text":     flattenMCPText(cr.Content),
		"is_error": cr.IsError,
	})
	if err != nil {
		return result
	}
	return payload
}

func nodeFailedFromResult(result datatypes.JSON) bool {
	var payload map[string]interface{}
	if len(result) == 0 || json.Unmarshal(result, &payload) != nil {
		return false
	}
	_, ok := payload["error"]
	return ok
}

func toolResultError(result datatypes.JSON) string {
	var payload map[string]interface{}
	if len(result) == 0 || json.Unmarshal(result, &payload) != nil {
		return "工具执行失败"
	}
	if msg, ok := payload["error"].(string); ok && msg != "" {
		return msg
	}
	return "工具执行失败"
}

// NewTraceID 生成执行链路追踪 ID (8 位十六进制)
func NewTraceID() string {
	return uuid.NewString()[0:8]
}
