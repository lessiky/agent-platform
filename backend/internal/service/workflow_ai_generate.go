package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
)

// AI 生成工作流 (M5 Phase 2): 用自然语言描述 -> LLM 生成 DAG 定义
//
// 流程: 收集平台上下文 (可用 Agent / MCP 服务器+工具) -> 组装提示词 -> 路由模型对话 ->
// 解析 JSON -> 结构校验 + 上下文校验 (agent_id/mcp_server_id/tool 必须真实存在) ->
// 失败携带错误信息重试一次 -> 返回校验通过的草稿 (不落库, 由用户在前端确认后保存)
const (
	aiGenMaxAttempts       = 2    // 首次 + 1 次带错误反馈的重试
	aiGenMaxAgents         = 20   // 提示词中最多携带的 Agent 数
	aiGenMaxMCPServers     = 20   // 提示词中最多携带的 MCP 服务器数
	aiGenMaxToolsPerServer = 30   // 单服务器最多携带的工具数
	aiGenDescMaxRunes      = 2000 // 流程描述长度上限 (字)
)

// AIGenerateWorkflowRequest AI 生成工作流请求
type AIGenerateWorkflowRequest struct {
	Description string `json:"description" binding:"required,max=2000"` // 自然语言流程描述
}

// AIGenerateWorkflowResult AI 生成结果 (校验通过的草稿定义, 不落库)
type AIGenerateWorkflowResult struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Definition  datatypes.JSON `json:"definition"`
	InputSchema datatypes.JSON `json:"input_schema,omitempty"`
	Model       string         `json:"model"`        // 使用的模型模板名
	ModelID     string         `json:"model_id"`     // 使用的模型模板 ID
	ModelName   string         `json:"model_name"`   // 模型返回的模型名
	Attempts    int            `json:"attempts"`     // 实际尝试次数 (1 或 2)
	TotalTokens int            `json:"total_tokens"` // 模型消耗 token
}

// aiWorkflowChatProvider 模型对话能力 (由 ModelTemplateService 满足: 路由+故障转移+配额)
type aiWorkflowChatProvider interface {
	RouteAndChat(ctx context.Context, agentID string, messages []modelclient.ChatMessage, tools []modelclient.ChatToolDef, gen modelclient.GenOptions) (*ChatOutcome, error)
}

// aiWorkflowAgentRepo Agent 目录 (由 repository.AgentRepository 满足)
type aiWorkflowAgentRepo interface {
	List(ctx context.Context, filter repository.AgentListFilter) ([]*model.Agent, int64, error)
}

// aiWorkflowMCPRepo MCP 服务器目录 (由 repository.MCPServerRepository 满足)
type aiWorkflowMCPRepo interface {
	List(ctx context.Context, filter repository.MCPListFilter) ([]model.MCPServer, int64, error)
}

// WorkflowAIGenerator AI 工作流生成器
type WorkflowAIGenerator struct {
	chat   aiWorkflowChatProvider
	agents aiWorkflowAgentRepo
	mcps   aiWorkflowMCPRepo
}

// NewWorkflowAIGenerator 创建 AI 工作流生成器
func NewWorkflowAIGenerator(chat aiWorkflowChatProvider, agents aiWorkflowAgentRepo, mcps aiWorkflowMCPRepo) *WorkflowAIGenerator {
	return &WorkflowAIGenerator{chat: chat, agents: agents, mcps: mcps}
}

// aiGeneratedWorkflow 模型应答的期望结构
type aiGeneratedWorkflow struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
	Definition  WorkflowDefinition     `json:"definition"`
}

// Generate 生成工作流定义 (校验通过才返回; 不落库)
func (g *WorkflowAIGenerator) Generate(ctx context.Context, req AIGenerateWorkflowRequest) (*AIGenerateWorkflowResult, error) {
	description := strings.TrimSpace(req.Description)
	if description == "" {
		return nil, errors.NewValidationError("description 不能为空")
	}
	if len([]rune(description)) > aiGenDescMaxRunes {
		return nil, errors.NewValidationError(fmt.Sprintf("description 过长 (≤%d 字)", aiGenDescMaxRunes))
	}

	catalog, err := g.collectCatalog(ctx)
	if err != nil {
		return nil, err
	}
	systemPrompt := buildAIGenerateSystemPrompt(catalog)

	temp := 0.2
	maxTokens := 4096
	gen := modelclient.GenOptions{Temperature: &temp, MaxTokens: &maxTokens}

	var feedback string
	var lastErr error
	for attempt := 1; attempt <= aiGenMaxAttempts; attempt++ {
		userPrompt := "请为以下业务流程生成工作流定义:\n\n" + description
		if feedback != "" {
			userPrompt += "\n\n上一次生成的定义未通过校验: " + feedback + "\n请修正后重新输出完整的 JSON。"
		}
		messages := []modelclient.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		}
		outcome, err := g.chat.RouteAndChat(ctx, "", messages, nil, gen)
		if err != nil {
			// 无可用模型/模型调用失败等环境错误直接上抛, 不重试
			return nil, err
		}

		parsed, parseErr := parseAIGeneratedWorkflow(outcome.Content)
		if parseErr == nil {
			parsed.Definition.Version = 1
			if validateErr := ValidateDefinition(&parsed.Definition); validateErr != nil {
				parseErr = validateErr
			} else if catalogErr := validateDefinitionAgainstCatalog(&parsed.Definition, catalog); catalogErr != nil {
				parseErr = catalogErr
			}
		}
		if parseErr == nil {
			return buildAIGenerateResult(parsed, description, outcome, attempt), nil
		}
		lastErr = parseErr
		feedback = parseErr.Error()
	}
	return nil, errors.NewValidationError("AI 生成的工作流定义未通过校验: " + lastErr.Error())
}

// aiGenServerEntry MCP 服务器目录条目 (提示词展示用, 保持有序)
type aiGenServerEntry struct {
	ID    string
	Tools []model.MCPTool
}

// aiGenCatalog 平台能力目录 (提示词 + 上下文校验共用)
type aiGenCatalog struct {
	agents    []model.Agent
	servers   []aiGenServerEntry
	agentIDs  map[string]struct{}
	serverIDs map[string]map[string]struct{} // serverID -> toolName set
}

// collectCatalog 收集可用 Agent / MCP 服务器及其已发现工具 (按名称排序, 保证提示词稳定)
func (g *WorkflowAIGenerator) collectCatalog(ctx context.Context) (*aiGenCatalog, error) {
	catalog := &aiGenCatalog{
		agentIDs:  map[string]struct{}{},
		serverIDs: map[string]map[string]struct{}{},
	}
	agentList, _, err := g.agents.List(ctx, repository.AgentListFilter{Page: 1, PageSize: aiGenMaxAgents})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list agents for ai generate")
	}
	sort.Slice(agentList, func(i, j int) bool { return agentList[i].Name < agentList[j].Name })
	for i := range agentList {
		catalog.agents = append(catalog.agents, *agentList[i])
		catalog.agentIDs[agentList[i].ID] = struct{}{}
	}
	serverList, _, err := g.mcps.List(ctx, repository.MCPListFilter{Page: 1, PageSize: aiGenMaxMCPServers})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list mcp servers for ai generate")
	}
	sort.Slice(serverList, func(i, j int) bool { return serverList[i].Name < serverList[j].Name })
	for i := range serverList {
		entry := aiGenServerEntry{ID: serverList[i].ID}
		if len(serverList[i].Tools) > 0 {
			_ = json.Unmarshal(serverList[i].Tools, &entry.Tools)
		}
		sort.Slice(entry.Tools, func(i, j int) bool { return entry.Tools[i].Name < entry.Tools[j].Name })
		if len(entry.Tools) > aiGenMaxToolsPerServer {
			entry.Tools = entry.Tools[:aiGenMaxToolsPerServer]
		}
		catalog.servers = append(catalog.servers, entry)
		toolSet := map[string]struct{}{}
		for _, tool := range entry.Tools {
			toolSet[tool.Name] = struct{}{}
		}
		catalog.serverIDs[serverList[i].ID] = toolSet
	}
	return catalog, nil
}

// buildAIGenerateSystemPrompt 组装系统提示词 (平台 DAG 规范 + 可用资源目录)
func buildAIGenerateSystemPrompt(catalog *aiGenCatalog) string {
	var b strings.Builder
	b.WriteString("你是 Agent 管理平台的工作流编排专家。用户会用自然语言描述业务流程, 你将其转换为平台可直接执行的工作流 DAG 定义。\n\n")
	b.WriteString("## 输出要求 (严格遵守)\n")
	b.WriteString("- 只输出一个 JSON 对象, 不要输出解释、注释或 Markdown 代码块标记\n")
	b.WriteString("- 结构: {\"name\": \"工作流名称(中文,≤32字)\", \"description\": \"描述(1-2句)\", \"input_schema\": {\"type\":\"object\",\"properties\":{...}}, \"definition\": {\"version\": 1, \"nodes\": [...], \"edges\": [...]}}\n")
	b.WriteString("- input_schema 为工作流输入参数的 JSON Schema; 无输入参数时给 {}\n\n")
	b.WriteString("## 节点 (nodes[])\n")
	b.WriteString("- 公共字段: id(唯一, 建议 n1/n2/c1)、type、name(中文短名)、config(对象); 可选 retry{max_attempts:1-10, interval_seconds:0-600, backoff:fixed|exponential}、timeout_seconds(0-3600)\n")
	b.WriteString("- type=agent: 调用平台 Agent 单轮对话。config: {agent_id, message(提示词, 支持变量)}。输出: reply(应答文本)、session_id、model_name、total_tokens\n")
	b.WriteString("- type=mcp_tool: 调用 MCP 工具。config: {mcp_server_id, tool(工具名), arguments(参数对象, 支持变量)}。输出: text(工具文本结果)、is_error。工具需人工审核时执行会挂起等待审批\n")
	b.WriteString("- type=http: 外部 HTTP 调用。config: {url(必须 http(s):// 开头), method(GET|POST|PUT|PATCH|DELETE), headers?(对象), body?(对象, 非 GET/DELETE 时发送)}。输出: status_code、body、latency_ms\n")
	b.WriteString("- type=delay: 延迟等待。config: {seconds(1-3600 整数)}。输出: waited_seconds\n")
	b.WriteString("- type=condition: 条件分支。config: {left(变量或字面量), operator(==|!=|>|<|>=|<=|contains|exists), right(比较值, exists 时省略)}。输出: result(bool)、chosen\n")
	b.WriteString("- agent 节点的 agent_id 与 mcp_tool 节点的 mcp_server_id/tool 必须来自下方目录, 不得编造; 若所需资源不存在, 用 http/delay/condition 节点替代, 并在 description 中说明\n\n")
	b.WriteString("## 边 (edges[])\n")
	b.WriteString("- 结构: {id(唯一, 建议 e1/e2), source, target}\n")
	b.WriteString("- condition 节点必须有出口边, 每条出口边必须带 \"condition\":\"true\" 或 \"false\"; 其他节点的边不能带 condition 字段\n")
	b.WriteString("- 有向无环; 允许并行分支 (多个入度 0 的起点); 除单节点 DAG 外不允许孤立节点\n\n")
	b.WriteString("## 变量引用 (config 字符串/对象值中)\n")
	b.WriteString("- $inputs.<key>: 工作流输入\n- $nodes.<节点id>.<字段>: 上游节点输出 (如 $nodes.n1.reply)\n- $execution.id: 当前执行 ID\n")
	b.WriteString("- 支持点号路径与数组下标 (如 $inputs.items[0].name)\n\n")
	b.WriteString("## 设计原则\n")
	b.WriteString("- 节点命名清晰; condition 的 true/false 出口各自接合理后续节点, 两条出口都要有边\n")
	b.WriteString("- 输入参数优先引用 $inputs; 用户未提供具体 URL 时, http 节点用 https://example.com/api 占位并在 description 中注明需补充\n")
	b.WriteString("- 保持简洁: 通常 2-6 个节点即可, 不要为凑数添加无意义的 delay\n\n")
	b.WriteString("## 可用 Agent (agent 节点 agent_id 只能选以下 id)\n")
	if len(catalog.agents) == 0 {
		b.WriteString("(无)\n")
	} else {
		for i := range catalog.agents {
			agent := &catalog.agents[i]
			b.WriteString(fmt.Sprintf("- %s | %s | %s\n", agent.ID, agent.Name, truncateRunes(agent.Description, 120)))
		}
	}
	b.WriteString("\n## 可用 MCP 服务器与工具 (mcp_tool 节点 mcp_server_id/tool 只能选以下)\n")
	if len(catalog.servers) == 0 {
		b.WriteString("(无)\n")
	} else {
		for i := range catalog.servers {
			server := &catalog.servers[i]
			b.WriteString(fmt.Sprintf("- mcp_server_id=%s\n", server.ID))
			if len(server.Tools) == 0 {
				b.WriteString("  (未发现工具)\n")
				continue
			}
			for j := range server.Tools {
				tool := &server.Tools[j]
				b.WriteString(fmt.Sprintf("  - tool: %s — %s\n", tool.Name, truncateRunes(tool.Description, 120)))
			}
		}
	}
	return b.String()
}

// parseAIGeneratedWorkflow 从模型应答中提取并解析工作流 JSON (容忍代码块/前后赘述)
func parseAIGeneratedWorkflow(content string) (*aiGeneratedWorkflow, error) {
	payload, err := extractJSONPayload(content)
	if err != nil {
		return nil, err
	}
	var parsed aiGeneratedWorkflow
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil, fmt.Errorf("模型输出不是合法 JSON: %s", err.Error())
	}
	return &parsed, nil
}

// extractJSONPayload 提取应答中的 JSON 对象子串 (去 Markdown 代码块, 取首个 { 到末个 })
func extractJSONPayload(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", fmt.Errorf("模型未返回内容")
	}
	// 去除 ```json ... ``` / ``` ... ``` 围栏
	if strings.HasPrefix(trimmed, "```") {
		if firstNewline := strings.Index(trimmed, "\n"); firstNewline >= 0 {
			trimmed = strings.TrimSpace(trimmed[firstNewline+1:])
		}
		if end := strings.LastIndex(trimmed, "```"); end >= 0 {
			trimmed = strings.TrimSpace(trimmed[:end])
		}
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("模型输出中未找到 JSON 对象")
	}
	return trimmed[start : end+1], nil
}

// validateDefinitionAgainstCatalog 校验生成定义引用的资源真实存在 (LLM 幻觉防护)
func validateDefinitionAgainstCatalog(def *WorkflowDefinition, catalog *aiGenCatalog) error {
	for i := range def.Nodes {
		node := &def.Nodes[i]
		switch node.Type {
		case model.NodeTypeAgent:
			agentID := strOf(node.Config, "agent_id")
			if _, ok := catalog.agentIDs[agentID]; !ok {
				return fmt.Errorf("节点 %s 的 agent_id 不存在于平台: %s", node.ID, agentID)
			}
		case model.NodeTypeMCPTool:
			serverID := strOf(node.Config, "mcp_server_id")
			tools, ok := catalog.serverIDs[serverID]
			if !ok {
				return fmt.Errorf("节点 %s 的 mcp_server_id 不存在于平台: %s", node.ID, serverID)
			}
			tool := strOf(node.Config, "tool")
			if _, ok := tools[tool]; !ok {
				return fmt.Errorf("节点 %s 的 tool 在 MCP 服务器 %s 中不存在: %s", node.ID, serverID, tool)
			}
		}
	}
	return nil
}

// buildAIGenerateResult 组装生成结果 (名称/描述兜底 + 定义序列化)
func buildAIGenerateResult(parsed *aiGeneratedWorkflow, userDescription string, outcome *ChatOutcome, attempts int) *AIGenerateWorkflowResult {
	name := strings.TrimSpace(parsed.Name)
	if name == "" {
		name = "AI 生成工作流"
	}
	name = truncateRunes(name, 64)
	description := strings.TrimSpace(parsed.Description)
	if description == "" {
		description = userDescription
	}

	definition, _ := json.Marshal(parsed.Definition)
	result := &AIGenerateWorkflowResult{
		Name:        name,
		Description: description,
		Definition:  definition,
		Model:       outcome.TemplateName,
		ModelID:     outcome.TemplateID,
		ModelName:   outcome.Model,
		Attempts:    attempts,
		TotalTokens: outcome.TotalTokens,
	}
	if len(parsed.InputSchema) > 0 {
		if raw, err := json.Marshal(parsed.InputSchema); err == nil {
			result.InputSchema = raw
		}
	}
	return result
}

// truncateRunes 按 rune 截断
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
