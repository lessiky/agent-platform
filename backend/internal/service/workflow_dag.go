package service

import (
    "encoding/json"
    "fmt"
    "math"
    "strconv"
    "strings"

    "agent-platform/internal/model"
    "agent-platform/pkg/errors"

    "gorm.io/datatypes"
)

// 节点数量/边数量上限 (防过大 DAG)
const (
    MaxWorkflowNodes = 100
    MaxWorkflowEdges = 500
)

// WorkflowDefinition DAG 定义 (definition JSONB)
type WorkflowDefinition struct {
    Version int               `json:"version"`
    Nodes   []WorkflowNodeDef `json:"nodes"`
    Edges   []WorkflowEdgeDef `json:"edges"`
}

// WorkflowNodeDef 节点定义
type WorkflowNodeDef struct {
    ID             string                 `json:"id"`
    Type           string                 `json:"type"`
    Name           string                 `json:"name"`
    Config         map[string]interface{} `json:"config"`
    Retry          *NodeRetryPolicy       `json:"retry,omitempty"`
    TimeoutSeconds int                    `json:"timeout_seconds,omitempty"`
}

// NodeRetryPolicy 节点级重试策略 (PRD 2.4.3 失败重试)
type NodeRetryPolicy struct {
    MaxAttempts     int    `json:"max_attempts"`      // 总尝试次数 (含首次), 默认 1
    IntervalSeconds int    `json:"interval_seconds"`  // 重试间隔秒数
    Backoff         string `json:"backoff"`           // fixed (默认) | exponential
}

// WorkflowEdgeDef 有向边 (condition 节点出口需标注 true/false)
type WorkflowEdgeDef struct {
    ID        string `json:"id"`
    Source    string `json:"source"`
    Target    string `json:"target"`
    Condition string `json:"condition,omitempty"` // true | false
}

// ParseDefinition 解析并校验 DAG 定义
func ParseDefinition(raw datatypes.JSON) (*WorkflowDefinition, error) {
    if len(raw) == 0 {
        return nil, errors.NewValidationError("DAG 定义不能为空")
    }
    var def WorkflowDefinition
    if err := json.Unmarshal(raw, &def); err != nil {
        // 兼容 definition 以 JSON 字符串形式传入 (curl/PS 转义场景)
        var rawString string
        if json.Unmarshal(raw, &rawString) != nil {
            return nil, errors.NewValidationError("DAG 定义 JSON 解析失败: " + err.Error())
        }
        if err2 := json.Unmarshal([]byte(rawString), &def); err2 != nil {
            return nil, errors.NewValidationError("DAG 定义 JSON 解析失败: " + err2.Error())
        }
    }
    if err := ValidateDefinition(&def); err != nil {
        return nil, err
    }
    return &def, nil
}

// ValidateDefinition 校验 DAG: 节点/边合法性 + 拓扑无环
func ValidateDefinition(def *WorkflowDefinition) error {
    if len(def.Nodes) == 0 {
        return errors.NewValidationError("DAG 至少需要 1 个节点")
    }
    if len(def.Nodes) > MaxWorkflowNodes {
        return errors.NewValidationError(fmt.Sprintf("节点数不能超过 %d", MaxWorkflowNodes))
    }
    if len(def.Edges) > MaxWorkflowEdges {
        return errors.NewValidationError(fmt.Sprintf("边数不能超过 %d", MaxWorkflowEdges))
    }

    validTypes := map[string]bool{
        model.NodeTypeAgent: true, model.NodeTypeMCPTool: true, model.NodeTypeHTTP: true,
        model.NodeTypeDelay: true, model.NodeTypeCondition: true,
    }
    nodeSet := make(map[string]bool, len(def.Nodes))
    for i := range def.Nodes {
        node := &def.Nodes[i]
        node.ID = strings.TrimSpace(node.ID)
        if node.ID == "" {
            return errors.NewValidationError(fmt.Sprintf("第 %d 个节点缺少 id", i+1))
        }
        if nodeSet[node.ID] {
            return errors.NewValidationError("节点 id 重复: " + node.ID)
        }
        nodeSet[node.ID] = true
        if !validTypes[node.Type] {
            return errors.NewValidationError(fmt.Sprintf("节点 %s 类型无效: %s (支持 agent/mcp_tool/http/delay/condition)", node.ID, node.Type))
        }
        if err := validateNodeConfig(node); err != nil {
            return err
        }
    }

    edgeSet := make(map[string]bool, len(def.Edges))
    for i := range def.Edges {
        edge := &def.Edges[i]
        edge.ID = strings.TrimSpace(edge.ID)
        if edge.ID == "" {
            return errors.NewValidationError(fmt.Sprintf("第 %d 条边缺少 id", i+1))
        }
        if edgeSet[edge.ID] {
            return errors.NewValidationError("边 id 重复: " + edge.ID)
        }
        edgeSet[edge.ID] = true
        if !nodeSet[edge.Source] || !nodeSet[edge.Target] {
            return errors.NewValidationError(fmt.Sprintf("边 %s 引用了不存在的节点 (%s -> %s)", edge.ID, edge.Source, edge.Target))
        }
        if edge.Source == edge.Target {
            return errors.NewValidationError(fmt.Sprintf("边 %s 不允许自环 (%s)", edge.ID, edge.Source))
        }
        sourceNode := findNode(def, edge.Source)
        if sourceNode.Type == model.NodeTypeCondition {
            if edge.Condition != "true" && edge.Condition != "false" {
                return errors.NewValidationError(fmt.Sprintf("条件节点 %s 的出口边 %s 必须标注 condition=true 或 false", edge.Source, edge.ID))
            }
        } else if edge.Condition != "" {
            return errors.NewValidationError(fmt.Sprintf("边 %s 仅条件节点出口可标注 condition", edge.ID))
        }
    }

    // 条件节点必须有出口
    for i := range def.Nodes {
        if def.Nodes[i].Type == model.NodeTypeCondition {
            hasOut := false
            for j := range def.Edges {
                if def.Edges[j].Source == def.Nodes[i].ID {
                    hasOut = true
                    break
                }
            }
            if !hasOut {
                return errors.NewValidationError(fmt.Sprintf("条件节点 %s 至少需要 1 条出口边", def.Nodes[i].ID))
            }
        }
    }

    // 孤立节点: 既无入边也无出边且不是唯一节点
    if len(def.Nodes) > 1 {
        for i := range def.Nodes {
            id := def.Nodes[i].ID
            hasEdge := false
            for j := range def.Edges {
                if def.Edges[j].Source == id || def.Edges[j].Target == id {
                    hasEdge = true
                    break
                }
            }
            if !hasEdge {
                return errors.NewValidationError(fmt.Sprintf("节点 %s 未连接任何边 (孤立节点)", id))
            }
        }
    }

    // 拓扑排序检测环
    if err := checkAcyclic(def); err != nil {
        return err
    }
    return nil
}

func findNode(def *WorkflowDefinition, id string) *WorkflowNodeDef {
    for i := range def.Nodes {
        if def.Nodes[i].ID == id {
            return &def.Nodes[i]
        }
    }
    return nil
}

func validateNodeConfig(node *WorkflowNodeDef) error {
    cfg := node.Config
    switch node.Type {
    case model.NodeTypeAgent:
        if strOf(cfg, "agent_id") == "" {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: agent 节点缺少 config.agent_id", node.ID))
        }
    case model.NodeTypeMCPTool:
        if strOf(cfg, "mcp_server_id") == "" {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: mcp_tool 节点缺少 config.mcp_server_id", node.ID))
        }
        if strOf(cfg, "tool") == "" {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: mcp_tool 节点缺少 config.tool", node.ID))
        }
    case model.NodeTypeHTTP:
        url := strOf(cfg, "url")
        if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: http 节点 config.url 必须以 http(s):// 开头", node.ID))
        }
        method := strings.ToUpper(strOf(cfg, "method"))
        if method == "" {
            method = "GET"
        }
        validMethod := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
        if !validMethod[method] {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: http 方法无效: %s", node.ID, method))
        }
    case model.NodeTypeDelay:
        seconds := numOf(cfg, "seconds")
        if seconds <= 0 || seconds > 3600 {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: delay 节点 config.seconds 必须在 (0, 3600] 之间", node.ID))
        }
    case model.NodeTypeCondition:
        left, _ := cfg["left"]
        if left == nil {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: condition 节点缺少 config.left", node.ID))
        }
        op := strOf(cfg, "operator")
        validOp := map[string]bool{"==": true, "!=": true, ">": true, "<": true, ">=": true, "<=": true, "contains": true, "exists": true}
        if !validOp[op] {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: condition 操作符无效: %s (支持 == != > < >= <= contains exists)", node.ID, op))
        }
        if op != "exists" {
            _, hasRight := cfg["right"]
            if !hasRight {
                return errors.NewValidationError(fmt.Sprintf("节点 %s: condition 节点缺少 config.right", node.ID))
            }
        }
    }

    // 重试策略
    if node.Retry != nil {
        if node.Retry.MaxAttempts < 1 || node.Retry.MaxAttempts > 10 {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: retry.max_attempts 必须在 [1,10]", node.ID))
        }
        if node.Retry.IntervalSeconds < 0 || node.Retry.IntervalSeconds > 600 {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: retry.interval_seconds 必须在 [0,600]", node.ID))
        }
        if node.Retry.Backoff != "" && node.Retry.Backoff != "fixed" && node.Retry.Backoff != "exponential" {
            return errors.NewValidationError(fmt.Sprintf("节点 %s: retry.backoff 仅支持 fixed/exponential", node.ID))
        }
    }
    if node.TimeoutSeconds < 0 || node.TimeoutSeconds > 3600 {
        return errors.NewValidationError(fmt.Sprintf("节点 %s: timeout_seconds 必须在 [0,3600]", node.ID))
    }
    return nil
}

func strOf(cfg map[string]interface{}, key string) string {
    if cfg == nil {
        return ""
    }
    v, ok := cfg[key]
    if !ok {
        return ""
    }
    s, ok := v.(string)
    return s
}

func numOf(cfg map[string]interface{}, key string) float64 {
    if cfg == nil {
        return 0
    }
    v, ok := cfg[key]
    if !ok {
        return 0
    }
    switch n := v.(type) {
    case float64:
        return n
    case int:
        return float64(n)
    case json.Number:
        f, _ := n.Float64()
        return f
    }
    return 0
}

// checkAcyclic Kahn 拓扑排序检测环
func checkAcyclic(def *WorkflowDefinition) error {
    inDegree := make(map[string]int, len(def.Nodes))
    adjacency := make(map[string][]string, len(def.Nodes))
    for i := range def.Nodes {
        inDegree[def.Nodes[i].ID] = 0
    }
    for i := range def.Edges {
        adjacency[def.Edges[i].Source] = append(adjacency[def.Edges[i].Source], def.Edges[i].Target)
        inDegree[def.Edges[i].Target]++
    }

    queue := make([]string, 0)
    for id, degree := range inDegree {
        if degree == 0 {
            queue = append(queue, id)
        }
    }
    visited := 0
    for len(queue) > 0 {
        current := queue[0]
        queue = queue[1:]
        visited++
        for _, next := range adjacency[current] {
            inDegree[next]--
            if inDegree[next] == 0 {
                queue = append(queue, next)
            }
        }
    }
    if visited != len(def.Nodes) {
        return errors.NewValidationError("DAG 存在循环依赖, 请检查连线")
    }
    return nil
}

// ---------- 变量解析 ----------

// VarContext 变量解析上下文
type VarContext struct {
    Inputs      map[string]interface{}
    NodeOutputs map[string]interface{} // nodeID -> 输出
    ExecutionID string
    TriggeredBy string                 // 触发人用户 ID (cron/webhook 为空), agent 节点会话归属
}

// 变量引用语法 (Phase 1 子集):
//
//	$inputs / $inputs.a.b[0].c     工作流输入
//	$nodes.<nodeID> / $nodes.<nodeID>.a.b   上游节点输出
//	$execution.id                   当前执行 ID
// 整串为引用时保留原始类型; 嵌入字符串时按文本格式化
func ResolveVariables(value interface{}, ctx *VarContext) interface{} {
    switch v := value.(type) {
    case string:
        return resolveString(v, ctx)
    case map[string]interface{}:
        result := make(map[string]interface{}, len(v))
        for key, val := range v {
            result[key] = ResolveVariables(val, ctx)
        }
        return result
    case []interface{}:
        result := make([]interface{}, len(v))
        for i, val := range v {
            result[i] = ResolveVariables(val, ctx)
        }
        return result
    default:
        return value
    }
}

func resolveString(raw string, ctx *VarContext) interface{} {
    // 整串引用: 保留类型
    if strings.HasPrefix(raw, "$") {
        if value, ok := lookupRef(raw, ctx); ok {
            return value
        }
    }
    // 嵌入引用: 文本格式化
    result := raw
    for start := 0; start < len(raw); {
        if raw[start] != '$' {
            start++
            continue
        }
        end := nextRefEnd(raw, start)
        ref := raw[start:end]
        if value, ok := lookupRef(ref, ctx); ok {
            result = strings.Replace(result, ref, formatValue(value), 1)
            start = end
        } else {
            start++
        }
    }
    return result
}

// nextRefEnd 从 start (指向 $) 找到引用结束位置: 到空白或字符串结尾
func nextRefEnd(raw string, start int) int {
    for i := start; i < len(raw); i++ {
        if raw[i] == ' ' || raw[i] == '\t' || raw[i] == '"' || raw[i] == '\'' {
            return i
        }
    }
    return len(raw)
}

func lookupRef(ref string, ctx *VarContext) (interface{}, bool) {
    rest := strings.TrimPrefix(ref, "$")
    if rest == "" {
        return nil, false
    }
    parts := strings.SplitN(rest, ".", 2)
    root := parts[0]
    var target interface{}
    var found bool
    switch root {
    case "inputs":
        target = ctx.Inputs
        found = true
    case "nodes":
        if len(parts) == 2 && parts[1] != "" {
            // 第一个点分段 = 节点 ID, 其余为输出内路径
            rest := parts[1]
            nodeID := rest
            remaining := ""
            if idx := strings.Index(rest, "."); idx >= 0 {
                nodeID = rest[:idx]
                remaining = rest[idx+1:]
            }
            value, ok := ctx.NodeOutputs[nodeID]
            if !ok {
                return nil, false
            }
            if remaining == "" {
                return value, true
            }
            return walkPath(value, remaining)
        }
        return nil, false
    case "execution":
        if len(parts) == 2 && parts[1] == "id" {
            return ctx.ExecutionID, true
        }
        return nil, false
    default:
        return nil, false
    }
    if !found {
        return nil, false
    }
    if len(parts) == 1 {
        return target, true
    }
    return walkPath(target, parts[1])
}

// walkPath 按 a.b[0].c 路径取值
func walkPath(target interface{}, path string) (interface{}, bool) {
    current := target
    for _, segment := range strings.Split(path, ".") {
        if segment == "" {
            continue
        }
        for {
            if idx := strings.Index(segment, "["); idx >= 0 {
                closeIdx := strings.Index(segment[idx:], "]")
                if closeIdx < 0 {
                    return nil, false
                }
                closeIdx += idx
                key := segment[:idx]
                indexStr := segment[idx+1 : closeIdx]
                if key != "" {
                    m, ok := current.(map[string]interface{})
                    if !ok {
                        return nil, false
                    }
                    current, ok = m[key]
                    if !ok {
                        return nil, false
                    }
                }
                index, err := strconv.Atoi(indexStr)
                if err != nil {
                    return nil, false
                }
                arr, ok := current.([]interface{})
                if !ok || index < 0 || index >= len(arr) {
                    return nil, false
                }
                current = arr[index]
                segment = segment[closeIdx+1:]
            } else {
                m, ok := current.(map[string]interface{})
                if !ok {
                    return nil, false
                }
                current, ok = m[segment]
                if !ok {
                    return nil, false
                }
                break
            }
        }
    }
    return current, true
}

// formatValue 文本格式化 (嵌入引用)
func formatValue(value interface{}) string {
    switch v := value.(type) {
    case nil:
        return ""
    case string:
        return v
    case float64:
        if math.Trunc(v) == v && !math.IsInf(v, 0) {
            return strconv.FormatInt(int64(v), 10)
        }
        return strconv.FormatFloat(v, 'f', -1, 64)
    case bool:
        return strconv.FormatBool(v)
    default:
        raw, _ := json.Marshal(value)
        return string(raw)
    }
}