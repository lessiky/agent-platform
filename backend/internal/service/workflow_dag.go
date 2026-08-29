package service

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
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
	MaxAttempts     int    `json:"max_attempts"`     // 总尝试次数 (含首次), 默认 1
	IntervalSeconds int    `json:"interval_seconds"` // 重试间隔秒数
	Backoff         string `json:"backoff"`          // fixed (默认) | exponential
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
		model.NodeTypeDelay: true, model.NodeTypeCondition: true, model.NodeTypePrint: true,
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
			return errors.NewValidationError(fmt.Sprintf("节点 %s 类型无效: %s (支持 agent/mcp_tool/http/delay/condition/print)", node.ID, node.Type))
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
	case model.NodeTypePrint:
		if strings.TrimSpace(strOf(cfg, "message")) == "" {
			return errors.NewValidationError(fmt.Sprintf("节点 %s: print 节点缺少 config.message (输出内容)", node.ID))
		}
		if color := strOf(cfg, "color"); color != "" && !isHexColor(color) {
			return errors.NewValidationError(fmt.Sprintf("节点 %s: print 节点 config.color 无效: %s (仅支持 #rgb 或 #rrggbb)", node.ID, color))
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

var hexColorPattern = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// isHexColor 是否为 #rgb / #rrggbb 形式的颜色值
func isHexColor(s string) bool {
	return hexColorPattern.MatchString(s)
}

// ---------- 变量解析 ----------

// VarContext 变量解析上下文
type VarContext struct {
	Inputs      map[string]interface{}
	NodeOutputs map[string]interface{} // nodeID -> 输出
	ExecutionID string
	TriggeredBy string // 触发人用户 ID (cron/webhook 为空), agent 节点会话归属
}

// 变量引用语法 (Phase 1 子集):
//
//	$inputs / $inputs.a.b[0].c     工作流输入
//	$nodes.<nodeID> / $nodes.<nodeID>.a.b   上游节点输出
//	$execution.id                   当前执行 ID
//	json(<引用>).a[0].b            将引用值按 JSON 解析后取路径 (如 json($nodes.n1.text).data.id)
//
// 整串为引用时保留原始类型; 嵌入字符串时按文本格式化。
// 变量后可紧跟中文等非空白字符 (如 "检查$inputs.url访问是否正常"), 解析时按最长前缀回退匹配
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
	// 整串引用: 保留类型 ($ 引用, 或 json($nodes.n1.text).data 等函数调用)
	if value, ok := lookupWholeRef(raw, ctx); ok {
		return value
	}
	// 嵌入引用: 文本格式化
	var result strings.Builder
	result.Grow(len(raw))
	for start := 0; start < len(raw); {
		if raw[start] == '$' {
			end := nextRefEnd(raw, start)
			// 从最长前缀开始尝试: 变量后紧跟中文等字符时, 回退到能命中的边界
			// (如 "检查$inputs.testurl访问是否正常" 解析出 $inputs.testurl)
			replaced := false
			for e := end; e > start+1; {
				ref := raw[start:e]
				// 截断处后随 ASCII 引用字符说明是更长路径的一部分, 不截取前缀 (避免 $inputs.a 误配 $inputs.a.b);
				// ')' 是函数实参的结束符, 允许在函数整体未命中时回退解析内层引用
				if value, ok := lookupRef(ref, ctx); ok && (e == end || raw[e] >= 0x80 || raw[e] == ')') {
					result.WriteString(formatValue(value))
					start = e
					replaced = true
					break
				}
				e = prevRuneStart(raw, start, e)
			}
			if !replaced {
				result.WriteByte(raw[start])
				start++
			}
			continue
		}
		// 函数调用引用: json($nodes.n1.text).data (函数名以 ASCII 字母开头)
		if isLetter(raw[start]) {
			if consumed, ok := resolveFuncCall(raw[start:], &result, ctx); ok {
				start += consumed
				continue
			}
		}
		result.WriteByte(raw[start])
		start++
	}
	return result.String()
}

// lookupWholeRef 整串引用: $ 引用或函数调用 (json($...)); 非引用文本返回 false
func lookupWholeRef(raw string, ctx *VarContext) (interface{}, bool) {
	if !strings.HasPrefix(raw, "$") {
		if _, _, _, ok := splitFunctionCall(raw); !ok {
			return nil, false
		}
	}
	return lookupRef(raw, ctx)
}

// resolveFuncCall 尝试把 base (以字母开头) 解析为函数调用引用 <func>(<引用>)<路径>:
// 命中时写入格式化值并返回消耗长度, 未命中返回 0/false
func resolveFuncCall(base string, result *strings.Builder, ctx *VarContext) (int, bool) {
	openIdx := strings.IndexByte(base, '(')
	if openIdx <= 0 || !isFuncName(base[:openIdx]) {
		return 0, false
	}
	parenEnd := funcCallParenEnd(base)
	if parenEnd < 0 {
		return 0, false
	}
	if tail := base[parenEnd+1:]; tail != "" && !strings.HasPrefix(tail, ".") && !strings.HasPrefix(tail, "[") {
		return 0, false
	}
	// 尾随路径的结束边界: 空白/引号/字符串结尾
	tailEnd := len(base)
	for i := parenEnd + 1; i < len(base); i++ {
		if base[i] == ' ' || base[i] == '\t' || base[i] == '"' || base[i] == '\'' {
			tailEnd = i
			break
		}
	}
	// 从最长路径回退: 路径后紧跟中文等字符时回退到能命中的边界
	for e := tailEnd; ; e = prevRuneStart(base, 0, e) {
		candidate := base[:e]
		if _, _, _, ok := splitFunctionCall(candidate); ok {
			// 截断处后随 ASCII 引用字符说明是更长路径的一部分, 不截取前缀; ')' 为用户自定义包裹符号, 允许命中
			if value, ok := lookupRef(candidate, ctx); ok && (e == tailEnd || base[e] >= 0x80 || base[e] == ')') {
				result.WriteString(formatValue(value))
				return e, true
			}
		}
		if e <= parenEnd+1 {
			break
		}
	}
	return 0, false
}

// funcCallParenEnd 返回 base 中第一个 '(' (函数实参起点) 的配对右括号下标, 未闭合返回 -1
func funcCallParenEnd(base string) int {
	depth := 0
	for i := 0; i < len(base); i++ {
		switch base[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// isLetter 是否 ASCII 字母 (函数名以字母开头)
func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// nextRefEnd 从 start (指向 $) 找到引用结束位置: 到空白或字符串结尾 (函数调用括号内允许空白)
func nextRefEnd(raw string, start int) int {
	depth := 0
	for i := start; i < len(raw); i++ {
		switch raw[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '"' || raw[i] == '\'') {
			return i
		}
	}
	return len(raw)
}

// prevRuneStart 返回 raw[start:e] 中最后一个 rune 的起始下标 (按字符回退引用边界)
func prevRuneStart(raw string, start, e int) int {
	for i := e - 1; i >= start; i-- {
		if raw[i] < 0x80 || raw[i] >= 0xC0 {
			return i
		}
	}
	return start
}

func lookupRef(ref string, ctx *VarContext) (interface{}, bool) {
	rest := strings.TrimPrefix(ref, "$")
	if rest == "" {
		return nil, false
	}
	// 函数调用: <func>(<引用>)<路径>, 如 json($nodes.n1.text).data[0]; 实参可为嵌套函数调用
	if name, arg, tail, isFunc := splitFunctionCall(rest); isFunc {
		value, ok := lookupRef(arg, ctx)
		if !ok {
			return nil, false
		}
		parsed, ok := applyFunc(name, value)
		if !ok {
			return nil, false
		}
		if tail == "" {
			return parsed, true
		}
		return walkPath(parsed, tail)
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

// splitFunctionCall 拆分函数调用 <name>(<arg>)<tail>:
// 返回函数名、括号内原始实参 (已 trim)、尾随路径 (空, 或以 . / [ 开头)
func splitFunctionCall(rest string) (name, arg, tail string, ok bool) {
	openIdx := strings.IndexByte(rest, '(')
	if openIdx <= 0 {
		return "", "", "", false
	}
	name = rest[:openIdx]
	if !isFuncName(name) {
		return "", "", "", false
	}
	depth := 0
	for i := openIdx; i < len(rest); i++ {
		switch rest[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				tail = rest[i+1:]
				if tail != "" && !strings.HasPrefix(tail, ".") && !strings.HasPrefix(tail, "[") {
					return "", "", "", false
				}
				return name, strings.TrimSpace(rest[openIdx+1 : i]), tail, true
			}
		}
	}
	return "", "", "", false
}

// isFuncName 函数名是否合法 (字母/数字/下划线)
func isFuncName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// applyFunc 内置函数注册表: 对已解析的引用值做转换, ok=false 表示函数不存在或执行失败 (引用按未命中处理)
func applyFunc(name string, value interface{}) (interface{}, bool) {
	switch name {
	case "json":
		return jsonParseValue(value)
	}
	return nil, false
}

// jsonParseValue json() 函数: 将值按 JSON 解析后返回 (对象/数组/标量)
//   - 字符串: 按 JSON 解析, 解析失败 ok=false
//   - map/slice: 已是 JSON 结构, 原样返回
//   - 其他标量: 序列化后重新解析 (兼容数字/布尔)
func jsonParseValue(value interface{}) (interface{}, bool) {
	if s, ok := value.(string); ok {
		raw := strings.TrimSpace(s)
		if raw == "" {
			return nil, false
		}
		var out interface{}
		if json.Unmarshal([]byte(raw), &out) != nil {
			return nil, false
		}
		return out, true
	}
	if _, isMap := value.(map[string]interface{}); isMap {
		return value, true
	}
	if _, isArr := value.([]interface{}); isArr {
		return value, true
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out interface{}
	if json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return out, true
}

// walkPath 按 a.b[0].c 路径取值
func walkPath(target interface{}, path string) (interface{}, bool) {
	current := target
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			continue
		}
		for {
			if segment == "" {
				break
			}
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
