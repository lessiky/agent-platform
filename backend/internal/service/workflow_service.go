package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// CreateWorkflowRequest 创建工作流
type CreateWorkflowRequest struct {
	Name         string          `json:"name" binding:"required,max=64"`
	Description  string          `json:"description"`
	Definition   datatypes.JSON  `json:"definition" binding:"required"`
	InputSchema  datatypes.JSON  `json:"input_schema"`
	OutputSchema datatypes.JSON  `json:"output_schema"`
	Schedule     *ScheduleConfig `json:"schedule"`
}

// UpdateWorkflowRequest 更新工作流 (字段为 nil 表示保持不变)
type UpdateWorkflowRequest struct {
	Name         *string         `json:"name" binding:"omitempty,max=64"`
	Description  *string         `json:"description"`
	Definition   *datatypes.JSON `json:"definition"`
	InputSchema  *datatypes.JSON `json:"input_schema"`
	OutputSchema *datatypes.JSON `json:"output_schema"`
}

// ScheduleConfig 定时调度配置 (PRD 2.4.2 定时调度)
type ScheduleConfig struct {
	Cron     string                 `json:"cron" binding:"required"`
	Input    map[string]interface{} `json:"input"`
	Timezone string                 `json:"timezone"`
}

// UpdateScheduleRequest 更新定时调度
type UpdateScheduleRequest struct {
	Enabled  bool                   `json:"enabled"`
	Cron     string                 `json:"cron"`
	Input    map[string]interface{} `json:"input"`
	Timezone string                 `json:"timezone"`
}

// WorkflowService 工作流管理服务 (M5)
type WorkflowService interface {
	// 定义 CRUD
	Create(ctx context.Context, req CreateWorkflowRequest, operatorID string) (*model.Workflow, error)
	Get(ctx context.Context, id string) (*model.Workflow, error)
	List(ctx context.Context, filter repository.WorkflowListFilter) ([]model.Workflow, int64, error)
	Update(ctx context.Context, id string, req UpdateWorkflowRequest, operatorID string) (*model.Workflow, error)
	Delete(ctx context.Context, id string) error
	ValidateDefinition(ctx context.Context, definition datatypes.JSON) error

	// 状态流转
	Activate(ctx context.Context, id string, operatorID string) (*model.Workflow, error)
	Archive(ctx context.Context, id string, operatorID string) (*model.Workflow, error)

	// 调度与触发
	UpdateSchedule(ctx context.Context, id string, req UpdateScheduleRequest) (*model.Workflow, error)
	Trigger(ctx context.Context, id string, input map[string]interface{}, triggerType string, triggeredBy *string) (*model.WorkflowExecution, error)
	HandleWebhook(ctx context.Context, token string, payload []byte) (*model.WorkflowExecution, error)

	// 执行查询
	ListExecutions(ctx context.Context, filter repository.ExecutionListFilter) ([]model.WorkflowExecution, int64, error)
	GetExecution(ctx context.Context, id string) (*ExecutionDetail, error)
	// GetExecutionByWebhookToken 通过 webhook token 查询执行状态 (公开端点, 仅返回状态视图, 不含输入/输出 payload)
	GetExecutionByWebhookToken(ctx context.Context, token, executionID string) (*WebhookExecutionView, error)
	CancelExecution(ctx context.Context, id string) error
	ExecutionDashboard(ctx context.Context) (interface{}, error)

	// 版本
	ListVersions(ctx context.Context, workflowID string) ([]model.WorkflowVersion, error)

	// SetSchedulerRefresher 注入调度器刷新回调
	SetSchedulerRefresher(refresher WorkflowSchedulerRefresher)
}

// ExecutionDetail 执行详情 (含节点执行记录)
type ExecutionDetail struct {
	*model.WorkflowExecution
	Nodes []model.WorkflowNodeExecution `json:"nodes"`
}

// WebhookExecutionView 执行状态公开查询视图 (外部系统仅凭 webhook token 轮询状态用)
// 出于安全考虑不含 input/output payload (含节点输入输出); 完整详情请用 JWT 端点 GetExecution
type WebhookExecutionView struct {
	ExecutionID     string            `json:"id"`
	WorkflowID      string            `json:"workflow_id"`
	WorkflowName    string            `json:"workflow_name"`
	WorkflowVersion int               `json:"workflow_version"`
	TriggerType     string            `json:"trigger_type"`
	Status          string            `json:"status"`
	Error           string            `json:"error,omitempty"`
	TraceID         string            `json:"trace_id,omitempty"`
	StartedAt       time.Time         `json:"started_at"`
	FinishedAt      *time.Time        `json:"finished_at"`
	Nodes           []WebhookNodeView `json:"nodes"`
}

// WebhookNodeView 节点级状态视图 (不含节点输入/输出)
type WebhookNodeView struct {
	NodeID     string  `json:"node_id"`
	NodeType   string  `json:"node_type"`
	NodeName   string  `json:"node_name"`
	Status     string  `json:"status"`
	Attempt    int     `json:"attempt"`
	Error      string  `json:"error,omitempty"`
	ApprovalID *string `json:"approval_id,omitempty"`
	DurationMs int64   `json:"duration_ms"`
}

type workflowService struct {
	workflows  repository.WorkflowRepository
	versions   repository.WorkflowVersionRepository
	executions repository.WorkflowExecutionRepository
	nodes      repository.WorkflowNodeExecutionRepository
	engine     *WorkflowEngine
	scheduler  WorkflowSchedulerRefresher
}

// WorkflowSchedulerRefresher 调度器刷新回调 (避免 service -> scheduler 构造循环)
type WorkflowSchedulerRefresher interface {
	ReloadSchedules(ctx context.Context)
}

func NewWorkflowService(
	workflows repository.WorkflowRepository,
	versions repository.WorkflowVersionRepository,
	executions repository.WorkflowExecutionRepository,
	nodes repository.WorkflowNodeExecutionRepository,
	engine *WorkflowEngine,
) WorkflowService {
	return &workflowService{
		workflows:  workflows,
		versions:   versions,
		executions: executions,
		nodes:      nodes,
		engine:     engine,
	}
}

// SetSchedulerRefresher 注入调度器 (M5: 定义变更/状态流转后重建 cron)
func (s *workflowService) SetSchedulerRefresher(refresher WorkflowSchedulerRefresher) {
	s.scheduler = refresher
}

// ---------- CRUD ----------

func (s *workflowService) Create(ctx context.Context, req CreateWorkflowRequest, operatorID string) (*model.Workflow, error) {
	if existing, err := s.workflows.GetByName(ctx, req.Name); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, errors.NewValidationError("工作流名称已存在: " + req.Name)
	}
	def, err := ParseDefinition(req.Definition)
	if err != nil {
		return nil, err
	}
	_ = def

	workflow := &model.Workflow{
		Name:         req.Name,
		Description:  req.Description,
		Definition:   req.Definition,
		Status:       model.WorkflowStatusDraft,
		InputSchema:  req.InputSchema,
		OutputSchema: req.OutputSchema,
		Version:      1,
		WebhookToken: newWebhookToken(),
		CreatedBy:    strPtr(operatorID),
	}
	if err := applySchedule(workflow, req.Schedule); err != nil {
		return nil, err
	}
	if err := s.workflows.Create(ctx, workflow); err != nil {
		return nil, errors.Wrap(err, "failed to create workflow")
	}
	if err := s.versions.Create(ctx, &model.WorkflowVersion{
		WorkflowID:   workflow.ID,
		Version:      workflow.Version,
		Definition:   workflow.Definition,
		InputSchema:  workflow.InputSchema,
		OutputSchema: workflow.OutputSchema,
		CreatedBy:    strPtr(operatorID),
	}); err != nil {
		return nil, errors.Wrap(err, "failed to snapshot workflow version")
	}
	s.reloadSchedules(ctx)
	return workflow, nil
}

func (s *workflowService) Get(ctx context.Context, id string) (*model.Workflow, error) {
	return s.workflows.Get(ctx, id)
}

func (s *workflowService) List(ctx context.Context, filter repository.WorkflowListFilter) ([]model.Workflow, int64, error) {
	return s.workflows.List(ctx, filter)
}

func (s *workflowService) Update(ctx context.Context, id string, req UpdateWorkflowRequest, operatorID string) (*model.Workflow, error) {
	workflow, err := s.workflows.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	changed := false
	if req.Name != nil && *req.Name != workflow.Name {
		existing, err := s.workflows.GetByName(ctx, *req.Name)
		if err != nil {
			return nil, err
		}
		if existing != nil && existing.ID != id {
			return nil, errors.NewValidationError("工作流名称已存在: " + *req.Name)
		}
		workflow.Name = *req.Name
		changed = true
	}
	if req.Description != nil && *req.Description != workflow.Description {
		workflow.Description = *req.Description
		changed = true
	}
	if req.Definition != nil && string(*req.Definition) != string(workflow.Definition) {
		if _, err := ParseDefinition(*req.Definition); err != nil {
			return nil, err
		}
		workflow.Definition = *req.Definition
		changed = true
	}
	if req.InputSchema != nil && string(*req.InputSchema) != string(workflow.InputSchema) {
		workflow.InputSchema = *req.InputSchema
		changed = true
	}
	if req.OutputSchema != nil && string(*req.OutputSchema) != string(workflow.OutputSchema) {
		workflow.OutputSchema = *req.OutputSchema
		changed = true
	}
	if !changed {
		return workflow, nil
	}
	workflow.Version++
	if err := s.workflows.Update(ctx, workflow); err != nil {
		return nil, errors.Wrap(err, "failed to update workflow")
	}
	if err := s.versions.Create(ctx, &model.WorkflowVersion{
		WorkflowID:   workflow.ID,
		Version:      workflow.Version,
		Definition:   workflow.Definition,
		InputSchema:  workflow.InputSchema,
		OutputSchema: workflow.OutputSchema,
		CreatedBy:    strPtr(operatorID),
	}); err != nil {
		return nil, errors.Wrap(err, "failed to snapshot workflow version")
	}
	s.reloadSchedules(ctx)
	return workflow, nil
}

func (s *workflowService) Delete(ctx context.Context, id string) error {
	if _, err := s.workflows.Get(ctx, id); err != nil {
		return err
	}
	active, err := s.workflows.HasActiveExecutions(ctx, id)
	if err != nil {
		return err
	}
	if active {
		return errors.NewValidationError("存在执行中/等待审核的执行记录, 无法删除工作流")
	}
	if err := s.workflows.Delete(ctx, id); err != nil {
		return errors.Wrap(err, "failed to delete workflow")
	}
	s.reloadSchedules(ctx)
	return nil
}

func (s *workflowService) ValidateDefinition(ctx context.Context, definition datatypes.JSON) error {
	_, err := ParseDefinition(definition)
	return err
}

// ---------- 状态流转 ----------

func (s *workflowService) Activate(ctx context.Context, id string, operatorID string) (*model.Workflow, error) {
	workflow, err := s.workflows.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if workflow.Status == model.WorkflowStatusActive {
		return workflow, nil
	}
	if _, err := ParseDefinition(workflow.Definition); err != nil {
		return nil, err
	}
	workflow.Status = model.WorkflowStatusActive
	if err := s.workflows.Update(ctx, workflow); err != nil {
		return nil, errors.Wrap(err, "failed to activate workflow")
	}
	s.reloadSchedules(ctx)
	return workflow, nil
}

func (s *workflowService) Archive(ctx context.Context, id string, operatorID string) (*model.Workflow, error) {
	workflow, err := s.workflows.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if workflow.Status == model.WorkflowStatusArchived {
		return workflow, nil
	}
	active, err := s.workflows.HasActiveExecutions(ctx, id)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, errors.NewValidationError("存在执行中/等待审核的执行记录, 无法归档")
	}
	workflow.Status = model.WorkflowStatusArchived
	workflow.ScheduleEnabled = false
	if err := s.workflows.Update(ctx, workflow); err != nil {
		return nil, errors.Wrap(err, "failed to archive workflow")
	}
	s.reloadSchedules(ctx)
	return workflow, nil
}

// ---------- 调度与触发 ----------

func (s *workflowService) UpdateSchedule(ctx context.Context, id string, req UpdateScheduleRequest) (*model.Workflow, error) {
	workflow, err := s.workflows.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	schedule := &ScheduleConfig{}
	if len(workflow.Schedule) > 0 {
		_ = json.Unmarshal(workflow.Schedule, schedule)
	}
	if req.Cron != "" {
		schedule.Cron = req.Cron
	}
	if req.Input != nil {
		schedule.Input = req.Input
	}
	if req.Timezone != "" {
		schedule.Timezone = req.Timezone
	}
	if req.Enabled && schedule.Cron == "" {
		return nil, errors.NewValidationError("开启定时调度必须提供 cron 表达式")
	}
	workflow.ScheduleEnabled = req.Enabled
	if err := applySchedule(workflow, schedule); err != nil {
		return nil, err
	}
	if err := s.workflows.Update(ctx, workflow); err != nil {
		return nil, errors.Wrap(err, "failed to update schedule")
	}
	s.reloadSchedules(ctx)
	return workflow, nil
}

// Trigger 手动/定时/事件触发执行 (仅 active 工作流可触发)
func (s *workflowService) Trigger(ctx context.Context, id string, input map[string]interface{}, triggerType string, triggeredBy *string) (*model.WorkflowExecution, error) {
	workflow, err := s.workflows.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if workflow.Status != model.WorkflowStatusActive {
		return nil, errors.NewValidationError("工作流未激活 (当前状态: " + workflow.Status + "), 无法触发")
	}
	def, err := ParseDefinition(workflow.Definition)
	if err != nil {
		return nil, err
	}
	if input == nil {
		input = map[string]interface{}{}
	}
	inputPayload, _ := json.Marshal(input)

	now := time.Now()
	execution := &model.WorkflowExecution{
		WorkflowID:      workflow.ID,
		WorkflowName:    workflow.Name,
		WorkflowVersion: workflow.Version,
		TriggerType:     triggerType,
		TriggeredBy:     triggeredBy,
		Status:          model.ExecutionStatusRunning,
		Input:           inputPayload,
		TraceID:         NewTraceID(),
		StartedAt:       now,
	}
	if err := s.executions.Create(ctx, execution); err != nil {
		return nil, errors.Wrap(err, "failed to create execution")
	}

	nodeRecords := make([]model.WorkflowNodeExecution, 0, len(def.Nodes))
	for i := range def.Nodes {
		node := &def.Nodes[i]
		name := node.Name
		if name == "" {
			name = node.ID
		}
		nodeRecords = append(nodeRecords, model.WorkflowNodeExecution{
			ExecutionID: execution.ID,
			NodeID:      node.ID,
			NodeType:    node.Type,
			NodeName:    name,
			Status:      model.NodeStatusPending,
		})
	}
	if err := s.nodes.CreateBatch(ctx, nodeRecords); err != nil {
		return nil, errors.Wrap(err, "failed to create node executions")
	}

	s.engine.RunAsync(execution.ID)
	return execution, nil
}

// HandleWebhook Webhook 事件触发 (公开端点, token 鉴权)
func (s *workflowService) HandleWebhook(ctx context.Context, token string, payload []byte) (*model.WorkflowExecution, error) {
	workflow, err := s.workflows.GetByWebhookToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if workflow == nil {
		return nil, errors.ErrNotFound
	}
	input := map[string]interface{}{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &input); err != nil {
			return nil, errors.NewValidationError("webhook payload 必须是 JSON 对象")
		}
	}
	return s.Trigger(ctx, workflow.ID, input, model.WorkflowTriggerWebhook, nil)
}

// ---------- 执行查询 ----------

func (s *workflowService) ListExecutions(ctx context.Context, filter repository.ExecutionListFilter) ([]model.WorkflowExecution, int64, error) {
	return s.executions.List(ctx, filter)
}

func (s *workflowService) GetExecution(ctx context.Context, id string) (*ExecutionDetail, error) {
	execution, err := s.executions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	nodes, err := s.nodes.ListByExecution(ctx, id)
	if err != nil {
		return nil, err
	}
	return &ExecutionDetail{WorkflowExecution: execution, Nodes: nodes}, nil
}

// GetExecutionByWebhookToken 通过 webhook token 查询执行状态 (公开端点)
// token 仅能查询其所属工作流的执行; 执行不存在或不属于该工作流时统一 404 (不泄露其他执行的存在性)
func (s *workflowService) GetExecutionByWebhookToken(ctx context.Context, token, executionID string) (*WebhookExecutionView, error) {
	workflow, err := s.workflows.GetByWebhookToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if workflow == nil {
		return nil, errors.ErrNotFound
	}
	// 非法 UUID 直接 404 (避免 uuid 类型转换错误落到 500)
	if _, err := uuid.Parse(executionID); err != nil {
		return nil, errors.ErrNotFound
	}
	execution, err := s.executions.Get(ctx, executionID)
	if err != nil {
		return nil, err
	}
	if execution.WorkflowID != workflow.ID {
		return nil, errors.ErrNotFound
	}
	nodes, err := s.nodes.ListByExecution(ctx, executionID)
	if err != nil {
		return nil, err
	}
	view := &WebhookExecutionView{
		ExecutionID:     execution.ID,
		WorkflowID:      execution.WorkflowID,
		WorkflowName:    execution.WorkflowName,
		WorkflowVersion: execution.WorkflowVersion,
		TriggerType:     execution.TriggerType,
		Status:          execution.Status,
		Error:           execution.Error,
		TraceID:         execution.TraceID,
		StartedAt:       execution.StartedAt,
		FinishedAt:      execution.FinishedAt,
		Nodes:           make([]WebhookNodeView, 0, len(nodes)),
	}
	for i := range nodes {
		n := &nodes[i]
		view.Nodes = append(view.Nodes, WebhookNodeView{
			NodeID:     n.NodeID,
			NodeType:   n.NodeType,
			NodeName:   n.NodeName,
			Status:     n.Status,
			Attempt:    n.Attempt,
			Error:      n.Error,
			ApprovalID: n.ApprovalID,
			DurationMs: n.DurationMs,
		})
	}
	return view, nil
}

func (s *workflowService) CancelExecution(ctx context.Context, id string) error {
	return s.engine.Cancel(ctx, id)
}

func (s *workflowService) ExecutionDashboard(ctx context.Context) (interface{}, error) {
	counts, err := s.executions.CountsByStatus(ctx)
	if err != nil {
		return nil, err
	}
	recent, err := s.executions.Recent(ctx, 10)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"counts_by_status": counts,
		"running":          counts[model.ExecutionStatusRunning],
		"waiting_approval": counts[model.ExecutionStatusWaitingApproval],
		"success":          counts[model.ExecutionStatusSuccess],
		"failed":           counts[model.ExecutionStatusFailed],
		"cancelled":        counts[model.ExecutionStatusCancelled],
		"recent":           recent,
	}, nil
}

func (s *workflowService) ListVersions(ctx context.Context, workflowID string) ([]model.WorkflowVersion, error) {
	return s.versions.ListByWorkflow(ctx, workflowID)
}

// ---------- helpers ----------

func (s *workflowService) reloadSchedules(ctx context.Context) {
	if s.scheduler != nil {
		s.scheduler.ReloadSchedules(ctx)
	}
}

// applySchedule 校验并写入调度配置 (ScheduleConfig 可为 nil 表示清空)
func applySchedule(workflow *model.Workflow, cfg *ScheduleConfig) error {
	if cfg == nil || (cfg.Cron == "" && !workflow.ScheduleEnabled) {
		if cfg == nil {
			workflow.Schedule = nil
			workflow.ScheduleEnabled = false
		}
		return nil
	}
	if err := validateCronExpression(cfg.Cron); err != nil {
		return errors.NewValidationError("cron 表达式无效: " + err.Error())
	}
	tz := cfg.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	payload, err := json.Marshal(map[string]interface{}{
		"cron":     cfg.Cron,
		"input":    cfg.Input,
		"timezone": tz,
	})
	if err != nil {
		return err
	}
	workflow.Schedule = payload
	return nil
}

func validateCronExpression(expr string) error {
	fields := strings.Split(strings.TrimSpace(expr), " ")
	if len(fields) != 5 {
		return fmt.Errorf("需要 5 段 cron 表达式 (分 时 日 月 周), 当前 %d 段", len(fields))
	}
	ranges := []struct {
		min, max int
	}{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range fields {
		if err := validateCronField(field, ranges[i].min, ranges[i].max); err != nil {
			return fmt.Errorf("第 %d 段 (%s) 无效: %v", i+1, field, err)
		}
	}
	return nil
}

func validateCronField(field string, min, max int) error {
	for _, part := range splitCronParts(field) {
		// */n 或 *
		star := ""
		rest := part
		if strings.HasPrefix(part, "*") {
			star = "*"
			rest = part[1:]
			if strings.HasPrefix(rest, "/") {
				rest = rest[1:]
			}
		}
		// 支持 a-b 区间
		if idx := strings.Index(rest, "-"); idx >= 0 {
			lo, err := strconv.Atoi(rest[:idx])
			if err != nil {
				return err
			}
			hi, err := strconv.Atoi(rest[idx+1:])
			if err != nil {
				return err
			}
			if lo < min || hi > max || lo > hi {
				return fmt.Errorf("区间超出范围 [%d,%d]", min, max)
			}
			continue
		}
		if star == "" {
			if rest == "" {
				return fmt.Errorf("空字段")
			}
			v, err := strconv.Atoi(rest)
			if err != nil {
				return err
			}
			if v < min || v > max {
				return fmt.Errorf("数值超出范围 [%d,%d]", min, max)
			}
		} else if rest != "" {
			step, err := strconv.Atoi(rest)
			if err != nil || step <= 0 {
				return fmt.Errorf("步长无效")
			}
		}
	}
	return nil
}

func splitCronParts(field string) []string {
	parts := make([]string, 0)
	current := ""
	for _, ch := range field {
		if ch == ',' {
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
			continue
		}
		current += string(ch)
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// newWebhookToken 生成 webhook 鉴权 token (32 位十六进制)
func newWebhookToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 回退: 时间戳 (理论上不会发生)
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
