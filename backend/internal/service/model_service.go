package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"agent-platform/internal/crypto"
	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
)

// ModelHealthKeep 每个模型保留的健康检查历史条数
const ModelHealthKeep = 500

// ModelUsageKeep 每个模型保留的用量日志条数
const ModelUsageKeep = 2000

// ModelGenConfig 模型生成参数 (PRD 2.3.1 P0)
type ModelGenConfig struct {
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
}

// CreateModelRequest 创建模型模板
type CreateModelRequest struct {
	Name     string          `json:"name" binding:"required,min=2,max=64"`
	Provider string          `json:"provider" binding:"required,oneof=openai anthropic google azure custom"`
	Model    string          `json:"model" binding:"required,max=128"`
	Endpoint string          `json:"endpoint" binding:"max=512"`
	APIKey   string          `json:"api_key" binding:"max=512"`
	Priority int             `json:"priority"` // 越小优先级越高, 缺省 100
	Status   string          `json:"status" binding:"omitempty,oneof=active inactive"`
	Config   *ModelGenConfig `json:"config"`
	Tags     []string        `json:"tags"`
}

// UpdateModelRequest 更新模型模板 (api_key 留空 = 保持不变; clear_api_key = 清空)
type UpdateModelRequest struct {
	CreateModelRequest
	ClearAPIKey bool `json:"clear_api_key"`
}

// APIKeyView API Key 脱敏视图 (API 永不回显明文)
type APIKeyView struct {
	Set  bool   `json:"api_key_set"`
	Mask string `json:"api_key_mask,omitempty"`
}

// ProbeView 连通性测试结果视图
type ProbeView struct {
	OK          bool     `json:"ok"`
	Status      string   `json:"status"`
	LatencyMs   int      `json:"latency_ms"`
	Models      []string `json:"models,omitempty"`
	ModelsCount int      `json:"models_count"`
	Error       string   `json:"error,omitempty"`
}

// HiView 发送Hi消息测试结果视图 (验证模型能否正常生成回复)
type HiView struct {
	OK           bool   `json:"ok"`
	LatencyMs    int    `json:"latency_ms"`
	Content      string `json:"content,omitempty"`
	Model        string `json:"model,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
	Error        string `json:"error,omitempty"`
}

// RouteSkip 路由候选跳过原因
type RouteSkip struct {
	Name   string `json:"name"`
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// RouteResult 路由选择结果 (dry-run, 不消耗配额)
type RouteResult struct {
	Selected *model.ModelTemplate `json:"selected,omitempty"`
	Reason   string               `json:"reason"`
	Skipped  []RouteSkip          `json:"skipped,omitempty"`
}

// QuotaRequest 配额更新 (字段为 nil 表示保持)
type QuotaRequest struct {
	DailyLimit        *int `json:"daily_limit"`
	MonthlyLimit      *int `json:"monthly_limit"`
	DailyTokenLimit   *int `json:"daily_token_limit"`
	MonthlyTokenLimit *int `json:"monthly_token_limit"`
}

// QuotaView 配额视图 (含模板信息)
type QuotaView struct {
	model.ModelQuota
	TemplateName string `json:"template_name"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
}

// UsageSummary 按模型的用量概览 (GET /model-usage)
type UsageSummary struct {
	ModelID           string `json:"model_id"`
	TemplateName      string `json:"template_name"`
	Model             string `json:"model"`
	Provider          string `json:"provider"`
	Status            string `json:"status"`
	DailyLimit        int    `json:"daily_limit"`
	DailyUsed         int    `json:"daily_used"`
	MonthlyLimit      int    `json:"monthly_limit"`
	MonthlyUsed       int    `json:"monthly_used"`
	DailyTokenLimit   int    `json:"daily_token_limit"`
	DailyTokenUsed    int    `json:"daily_token_used"`
	MonthlyTokenLimit int    `json:"monthly_token_limit"`
	MonthlyTokenUsed  int    `json:"monthly_token_used"`
	RecentCalls       int64  `json:"recent_calls"`
	RecentTokens      int64  `json:"recent_tokens"`
	RecentErrors      int64  `json:"recent_errors"`
}

// ModelTemplateService 模型模板业务服务
type ModelTemplateService interface {
	Create(ctx context.Context, req CreateModelRequest, operatorID string) (*model.ModelTemplate, *APIKeyView, error)
	Get(ctx context.Context, id string) (*model.ModelTemplate, *APIKeyView, error)
	List(ctx context.Context, filter repository.ModelListFilter) ([]model.ModelTemplate, int64, error)
	Update(ctx context.Context, id string, req UpdateModelRequest) (*model.ModelTemplate, *APIKeyView, error)
	Delete(ctx context.Context, id string) error

	Test(ctx context.Context, id string) (*ProbeView, error)
	SayHi(ctx context.Context, id string) (*HiView, error)
	CheckHealth(ctx context.Context, t *model.ModelTemplate) *ProbeView
	Health(ctx context.Context, id string, limit int) (map[string]interface{}, error)

	ListQuota(ctx context.Context) ([]QuotaView, error)
	UpsertQuota(ctx context.Context, modelID string, req QuotaRequest) (*QuotaView, error)
	Usage(ctx context.Context, modelID string, limit int) (map[string]interface{}, error)
	UsageSummary(ctx context.Context) ([]UsageSummary, error)

	Route(ctx context.Context) (*RouteResult, error)
	// RouteAndConsume 路由选择 + 配额消费 + 用量记录 (供运行时模拟流量, 返回日志行与是否成功)
	RouteAndConsume(ctx context.Context, agentID string, tokens, latencyMs int, failed bool) (string, bool)
	// RouteAndChat 路由选择 + 真实模型对话调用 (故障转移) + 配额消费 + 用量记录 (M2.5)
	RouteAndChat(ctx context.Context, agentID string, messages []modelclient.ChatMessage, tools []modelclient.ChatToolDef, gen modelclient.GenOptions) (*ChatOutcome, error)
	// ModelAvailable 该 Agent 是否有可路由的模型模板 (dry-run, 不消耗配额; 供 /invoke 降级决策)
	ModelAvailable(ctx context.Context, agentID string) bool
}

type modelTemplateService struct {
	templates repository.ModelTemplateRepository
	quotas    repository.ModelQuotaRepository
	usage     repository.ModelUsageLogRepository
	health    repository.ModelHealthLogRepository
	agents    repository.AgentRepository
	cipher    *crypto.AesGCM
	checkTime time.Duration
	chatTime  time.Duration
}

func NewModelTemplateService(
	templates repository.ModelTemplateRepository,
	quotas repository.ModelQuotaRepository,
	usage repository.ModelUsageLogRepository,
	health repository.ModelHealthLogRepository,
	agents repository.AgentRepository,
	cipher *crypto.AesGCM,
	checkTimeout time.Duration,
	chatTimeout time.Duration,
) ModelTemplateService {
	if checkTimeout <= 0 {
		checkTimeout = 5 * time.Second
	}
	if chatTimeout <= 0 {
		chatTimeout = 120 * time.Second
	}
	return &modelTemplateService{
		templates: templates,
		quotas:    quotas,
		usage:     usage,
		health:    health,
		agents:    agents,
		cipher:    cipher,
		checkTime: checkTimeout,
		chatTime:  chatTimeout,
	}
}

// Create 注册模型模板; active 状态注册后立即做一次连通性探测
func (s *modelTemplateService) Create(ctx context.Context, req CreateModelRequest, operatorID string) (*model.ModelTemplate, *APIKeyView, error) {
	if existing, err := s.templates.GetByName(ctx, req.Name); err != nil {
		return nil, nil, err
	} else if existing != nil {
		return nil, nil, errors.NewValidationError("模型名称已存在: " + req.Name)
	}

	configRaw := datatypes.JSON("{}")
	if req.Config != nil {
		b, err := json.Marshal(req.Config)
		if err != nil {
			return nil, nil, errors.NewValidationError("invalid config: " + err.Error())
		}
		configRaw = b
	}

	priority := req.Priority
	if priority <= 0 {
		priority = 100
	}
	status := model.ModelStatusActive
	if req.Status == model.ModelStatusInactive {
		status = model.ModelStatusInactive
	}

	t := &model.ModelTemplate{
		Name:      req.Name,
		Provider:  req.Provider,
		Model:     req.Model,
		Endpoint:  strings.TrimSpace(req.Endpoint),
		Config:    configRaw,
		Status:    status,
		Priority:  priority,
		Tags:      req.Tags,
		CreatedBy: &operatorID,
	}
	if req.APIKey != "" {
		enc, err := s.cipher.Encrypt([]byte(req.APIKey))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to encrypt api key: %w", err)
		}
		t.APIKey = enc
	}

	if err := s.templates.Create(ctx, t); err != nil {
		return nil, nil, err
	}

	// 立即探测 (best effort): 失败不阻塞注册, 状态记 error + last_error
	if status == model.ModelStatusActive {
		s.CheckHealth(ctx, t)
		if fresh, err := s.templates.Get(ctx, t.ID); err == nil {
			t = fresh
		}
	}
	return t, s.apiKeyView(t), nil
}

// Get 详情 (含 API Key 脱敏视图)
func (s *modelTemplateService) Get(ctx context.Context, id string) (*model.ModelTemplate, *APIKeyView, error) {
	t, err := s.templates.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return t, s.apiKeyView(t), nil
}

// List 分页列表 (q 搜索, provider/status/tag 过滤)
func (s *modelTemplateService) List(ctx context.Context, filter repository.ModelListFilter) ([]model.ModelTemplate, int64, error) {
	return s.templates.List(ctx, filter)
}

// Update 更新配置; 连接参数变化时自动重新探测
func (s *modelTemplateService) Update(ctx context.Context, id string, req UpdateModelRequest) (*model.ModelTemplate, *APIKeyView, error) {
	t, err := s.templates.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	if req.Name != t.Name {
		if existing, e := s.templates.GetByName(ctx, req.Name); e != nil {
			return nil, nil, e
		} else if existing != nil {
			return nil, nil, errors.NewValidationError("模型名称已存在: " + req.Name)
		}
	}

	beforeProvider := t.Provider
	beforeEndpoint := t.Endpoint
	beforeStatus := t.Status

	t.Name = req.Name
	t.Provider = req.Provider
	t.Model = req.Model
	t.Endpoint = strings.TrimSpace(req.Endpoint)
	if req.Priority > 0 {
		t.Priority = req.Priority
	}
	if req.Tags != nil {
		t.Tags = req.Tags
	}
	if req.Status == model.ModelStatusActive || req.Status == model.ModelStatusInactive {
		t.Status = req.Status
	}
	if req.Config != nil {
		b, err := json.Marshal(req.Config)
		if err != nil {
			return nil, nil, errors.NewValidationError("invalid config: " + err.Error())
		}
		t.Config = b
	}

	keyChanged := false
	if req.ClearAPIKey {
		t.APIKey = nil
		keyChanged = true
	} else if req.APIKey != "" {
		enc, err := s.cipher.Encrypt([]byte(req.APIKey))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to encrypt api key: %w", err)
		}
		t.APIKey = enc
		keyChanged = true
	}

	if err := s.templates.Update(ctx, t); err != nil {
		return nil, nil, err
	}

	// 非停用模板: 连接参数变化或刚从停用/异常恢复时重新探测
	reprobe := t.Status != model.ModelStatusInactive &&
		(keyChanged || t.Provider != beforeProvider || t.Endpoint != beforeEndpoint || beforeStatus != model.ModelStatusActive)
	if reprobe {
		s.CheckHealth(ctx, t)
		if fresh, err := s.templates.Get(ctx, id); err == nil {
			t = fresh
		}
	}
	return t, s.apiKeyView(t), nil
}

// Delete 删除 (级联配额 + 用量日志 + 健康历史)
func (s *modelTemplateService) Delete(ctx context.Context, id string) error {
	if err := s.quotas.DeleteByModel(ctx, id); err != nil {
		return err
	}
	if err := s.usage.DeleteByModel(ctx, id); err != nil {
		return err
	}
	if err := s.health.DeleteByModel(ctx, id); err != nil {
		return err
	}
	return s.templates.Delete(ctx, id)
}

// Test 手动连通性测试
func (s *modelTemplateService) Test(ctx context.Context, id string) (*ProbeView, error) {
	t, err := s.templates.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CheckHealth(ctx, t), nil
}

// SayHi 发送Hi消息测试: 真实调用一次对话接口, 验证模型能否正常生成回复
// (不消费配额, 不改变模板状态)
func (s *modelTemplateService) SayHi(ctx context.Context, id string) (*HiView, error) {
	t, err := s.templates.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status == model.ModelStatusInactive {
		return &HiView{OK: false, Error: "模板已手动停用"}, nil
	}
	if t.Provider != "openai" && t.Provider != "custom" {
		return &HiView{OK: false, Error: fmt.Sprintf("provider %s 的对话接口暂不支持 (仅 openai/custom)", t.Provider)}, nil
	}
	client, err := s.chatClient(t)
	if err != nil {
		return &HiView{OK: false, Error: err.Error()}, nil
	}
	start := time.Now()
	res, err := client.Chat(ctx, t.Model, []modelclient.ChatMessage{{Role: "user", Content: "Hi"}}, nil, genOptionsFromConfig(t))
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return &HiView{OK: false, LatencyMs: latency, Error: truncate(err.Error(), 300)}, nil
	}
	return &HiView{
		OK:           true,
		LatencyMs:    latency,
		Content:      res.Content,
		Model:        res.Model,
		FinishReason: res.FinishReason,
		TotalTokens:  res.TotalTokens,
	}, nil
}

// genOptionsFromConfig 解析模板生成参数为对话选项 (解析失败返回零值)
func genOptionsFromConfig(t *model.ModelTemplate) modelclient.GenOptions {
	var gen modelclient.GenOptions
	if len(t.Config) == 0 {
		return gen
	}
	var cfg ModelGenConfig
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return gen
	}
	gen.Temperature = cfg.Temperature
	gen.MaxTokens = cfg.MaxTokens
	return gen
}

// CheckHealth 连通性探测: 更新状态/延迟 + 记录历史
func (s *modelTemplateService) CheckHealth(ctx context.Context, t *model.ModelTemplate) *ProbeView {
	if t.Status == model.ModelStatusInactive {
		return &ProbeView{OK: false, Status: model.ModelStatusInactive, Error: "模板已手动停用"}
	}

	client, err := s.buildClient(t)
	if err != nil {
		view := &ProbeView{OK: false, Status: model.ModelStatusError, Error: err.Error()}
		s.recordHealth(ctx, t, view)
		return view
	}

	result := client.Probe(ctx)
	status := model.ModelStatusActive
	if !result.OK {
		status = model.ModelStatusError
	}
	view := &ProbeView{
		OK:          result.OK,
		Status:      status,
		LatencyMs:   result.LatencyMs,
		Models:      result.Models,
		ModelsCount: len(result.Models),
		Error:       result.Error,
	}
	s.recordHealth(ctx, t, view)
	return view
}

// Health 健康状态 (最新状态 + 检查历史)
func (s *modelTemplateService) Health(ctx context.Context, id string, limit int) (map[string]interface{}, error) {
	t, err := s.templates.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	history, err := s.health.List(ctx, id, limit)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"status":     t.Status,
		"last_check": t.HealthLastCheck,
		"latency_ms": t.HealthLatencyMs,
		"last_error": t.LastError,
		"history":    history,
	}, nil
}

// ListQuota 配额列表 (含模板信息, 展示值已应用重置逻辑)
func (s *modelTemplateService) ListQuota(ctx context.Context) ([]QuotaView, error) {
	quotas, err := s.quotas.List(ctx)
	if err != nil {
		return nil, err
	}
	templates, err := s.templates.ListForRoute(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*model.ModelTemplate, len(templates))
	for i := range templates {
		byID[templates[i].ID] = &templates[i]
	}
	views := make([]QuotaView, 0, len(quotas))
	for i := range quotas {
		q := quotas[i]
		ensureQuotaFresh(&q, time.Now())
		view := QuotaView{ModelQuota: q}
		if t, ok := byID[q.ModelID]; ok {
			view.TemplateName = t.Name
			view.Model = t.Model
			view.Provider = t.Provider
		}
		views = append(views, view)
	}
	return views, nil
}

// UpsertQuota 设置/更新配额 (0 = 不限; 已用计数保留)
func (s *modelTemplateService) UpsertQuota(ctx context.Context, modelID string, req QuotaRequest) (*QuotaView, error) {
	t, err := s.templates.Get(ctx, modelID)
	if err != nil {
		return nil, err
	}
	quota, err := s.quotas.GetByModel(ctx, modelID)
	if err != nil {
		return nil, err
	}
	if quota == nil {
		now := time.Now()
		quota = &model.ModelQuota{
			ModelID:        modelID,
			ResetDailyAt:   startOfDay(now),
			ResetMonthlyAt: startOfMonth(now),
		}
	}
	if req.DailyLimit != nil {
		quota.DailyLimit = *req.DailyLimit
	}
	if req.MonthlyLimit != nil {
		quota.MonthlyLimit = *req.MonthlyLimit
	}
	if req.DailyTokenLimit != nil {
		quota.DailyTokenLimit = *req.DailyTokenLimit
	}
	if req.MonthlyTokenLimit != nil {
		quota.MonthlyTokenLimit = *req.MonthlyTokenLimit
	}
	if err := s.quotas.Upsert(ctx, quota); err != nil {
		return nil, err
	}
	return &QuotaView{ModelQuota: *quota, TemplateName: t.Name, Model: t.Model, Provider: t.Provider}, nil
}

// Usage 单模型用量 (配额 + 最近调用日志)
func (s *modelTemplateService) Usage(ctx context.Context, modelID string, limit int) (map[string]interface{}, error) {
	t, err := s.templates.Get(ctx, modelID)
	if err != nil {
		return nil, err
	}
	quota, err := s.quotas.GetByModel(ctx, modelID)
	if err != nil {
		return nil, err
	}
	logs, err := s.usage.List(ctx, modelID, limit)
	if err != nil {
		return nil, err
	}
	var quotaView *QuotaView
	if quota != nil {
		ensureQuotaFresh(quota, time.Now())
		quotaView = &QuotaView{ModelQuota: *quota, TemplateName: t.Name, Model: t.Model, Provider: t.Provider}
	}
	return map[string]interface{}{
		"quota": quotaView,
		"logs":  logs,
	}, nil
}

// UsageSummary 全部模型的配额 + 近 24h 用量概览
func (s *modelTemplateService) UsageSummary(ctx context.Context) ([]UsageSummary, error) {
	templates, err := s.templates.ListForRoute(ctx)
	if err != nil {
		return nil, err
	}
	quotas, err := s.quotas.List(ctx)
	if err != nil {
		return nil, err
	}
	quotaByID := make(map[string]*model.ModelQuota, len(quotas))
	for i := range quotas {
		quotaByID[quotas[i].ModelID] = &quotas[i]
	}
	aggregates, err := s.usage.AggregateLast24h(ctx)
	if err != nil {
		return nil, err
	}

	summaries := make([]UsageSummary, 0, len(templates))
	for i := range templates {
		t := templates[i]
		summary := UsageSummary{
			ModelID:      t.ID,
			TemplateName: t.Name,
			Model:        t.Model,
			Provider:     t.Provider,
			Status:       t.Status,
		}
		if q, ok := quotaByID[t.ID]; ok {
			ensureQuotaFresh(q, time.Now())
			summary.DailyLimit = q.DailyLimit
			summary.DailyUsed = q.DailyUsed
			summary.MonthlyLimit = q.MonthlyLimit
			summary.MonthlyUsed = q.MonthlyUsed
			summary.DailyTokenLimit = q.DailyTokenLimit
			summary.DailyTokenUsed = q.DailyTokenUsed
			summary.MonthlyTokenLimit = q.MonthlyTokenLimit
			summary.MonthlyTokenUsed = q.MonthlyTokenUsed
		}
		if agg, ok := aggregates[t.ID]; ok {
			summary.RecentCalls = agg.Calls
			summary.RecentTokens = agg.Tokens
			summary.RecentErrors = agg.Errors
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// Route 基础路由选择 (PRD 2.3.4 P0 优先级路由 + 2.3.3 P2 超配额切换):
// 按优先级从小到大遍历 active 模板, 跳过异常状态与配额耗尽者, 返回首个可用模型
func (s *modelTemplateService) Route(ctx context.Context) (*RouteResult, error) {
	templates, err := s.templates.ListForRoute(ctx)
	if err != nil {
		return nil, err
	}
	skipped := make([]RouteSkip, 0, len(templates))
	for i := range templates {
		t := templates[i]
		if reason, skip := s.skipReason(ctx, &t); skip {
			skipped = append(skipped, RouteSkip{Name: t.Name, Model: t.Model, Reason: reason})
			continue
		}
		return &RouteResult{
			Selected: &t,
			Reason:   fmt.Sprintf("按优先级选中 (priority=%d)", t.Priority),
			Skipped:  skipped,
		}, nil
	}
	return &RouteResult{Reason: "无可用模型 (全部状态异常或配额耗尽)", Skipped: skipped}, nil
}

// RouteAndConsume 路由 + 配额消费 + 用量记录 (运行时模拟流量, 供 Agent -> 模型链路)
// 优先匹配 Agent 配置的模型 (config.model, 按模板名或模型名), 不可用时按优先级故障转移
func (s *modelTemplateService) RouteAndConsume(ctx context.Context, agentID string, tokens, latencyMs int, failed bool) (string, bool) {
	templates, err := s.templates.ListForRoute(ctx)
	if err != nil || len(templates) == 0 {
		return "model route failed reason=no_template", false
	}

	// Agent 配置的优先模型
	preferred := ""
	if agent, aErr := s.agents.GetByID(ctx, agentID); aErr == nil {
		var cfg struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(agent.Config, &cfg) == nil {
			preferred = cfg.Model
		}
	}

	var selected *model.ModelTemplate
	preferredUsed := false
	if preferred != "" {
		for i := range templates {
			t := templates[i]
			if !strings.EqualFold(t.Name, preferred) && !strings.EqualFold(t.Model, preferred) {
				continue
			}
			if _, skip := s.skipReason(ctx, &t); skip {
				continue
			}
			selected = &t
			preferredUsed = true
			break
		}
	}
	if selected == nil {
		for i := range templates {
			t := templates[i]
			if _, skip := s.skipReason(ctx, &t); skip {
				continue
			}
			selected = &t
			break
		}
	}
	if selected == nil {
		return "model route failed reason=all_unavailable", false
	}

	// 消费配额 (调用即计次, 失败调用同样消耗; token 按本次估算累加)
	quotaPart := ""
	if quota, qErr := s.quotas.GetByModel(ctx, selected.ID); qErr == nil && quota != nil {
		ensureQuotaFresh(quota, time.Now())
		quota.DailyUsed++
		quota.MonthlyUsed++
		quota.DailyTokenUsed += tokens
		quota.MonthlyTokenUsed += tokens
		if uErr := s.quotas.UpdateCounters(ctx, selected.ID, quota.DailyUsed, quota.MonthlyUsed, quota.DailyTokenUsed, quota.MonthlyTokenUsed, quota.ResetDailyAt, quota.ResetMonthlyAt); uErr != nil {
			logQuota := uErr.Error()
			quotaPart = fmt.Sprintf(" quota_update_error=%s", truncate(logQuota, 120))
		} else {
			if quota.DailyLimit > 0 {
				quotaPart += fmt.Sprintf(" quota=daily %d/%d", quota.DailyUsed, quota.DailyLimit)
			} else if quota.MonthlyLimit > 0 {
				quotaPart += fmt.Sprintf(" quota=monthly %d/%d", quota.MonthlyUsed, quota.MonthlyLimit)
			}
			if quota.DailyTokenLimit > 0 {
				quotaPart += fmt.Sprintf(" token_quota=daily %d/%d", quota.DailyTokenUsed, quota.DailyTokenLimit)
			} else if quota.MonthlyTokenLimit > 0 {
				quotaPart += fmt.Sprintf(" token_quota=monthly %d/%d", quota.MonthlyTokenUsed, quota.MonthlyTokenLimit)
			}
		}
	}

	// 用量日志
	logErr := ""
	if failed {
		logErr = "simulated error"
	}
	agentIDPtr := &agentID
	entry := &model.ModelUsageLog{
		ModelID:   selected.ID,
		AgentID:   agentIDPtr,
		OK:        !failed,
		Tokens:    tokens,
		LatencyMs: latencyMs,
		Error:     logErr,
	}
	if uErr := s.usage.Append(ctx, entry); uErr != nil {
		_ = uErr
	}
	if uErr := s.usage.Trim(ctx, selected.ID, ModelUsageKeep); uErr != nil {
		_ = uErr
	}

	preferredPart := ""
	if preferredUsed {
		preferredPart = " reason=agent_preferred"
	}
	detail := fmt.Sprintf("model route ok name=%s model=%s priority=%d tokens=%d latency=%dms%s%s",
		selected.Name, selected.Model, selected.Priority, tokens, latencyMs, quotaPart, preferredPart)
	return detail, true
}

// skipReason 返回 (跳过原因, 是否跳过)
func (s *modelTemplateService) skipReason(ctx context.Context, t *model.ModelTemplate) (string, bool) {
	if t.Status != model.ModelStatusActive {
		reason := "status=" + t.Status
		if t.Status == model.ModelStatusError && t.LastError != "" {
			reason += " (" + truncate(t.LastError, 60) + ")"
		}
		return reason, true
	}
	quota, err := s.quotas.GetByModel(ctx, t.ID)
	if err != nil {
		return "quota query failed", true
	}
	if quota != nil {
		ensureQuotaFresh(quota, time.Now())
		if exceeded, reason := quotaExceeded(quota); exceeded {
			return reason, true
		}
	}
	return "", false
}

// buildClient 构建探测客户端 (解密 API Key)
func (s *modelTemplateService) buildClient(t *model.ModelTemplate) (*modelclient.Client, error) {
	apiKey := ""
	if len(t.APIKey) > 0 {
		plain, err := s.cipher.Decrypt(t.APIKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt api key (key changed?)")
		}
		apiKey = string(plain)
	}
	return modelclient.New(t.Provider, t.Endpoint, apiKey, s.checkTime), nil
}

// apiKeyView API Key 脱敏视图
func (s *modelTemplateService) apiKeyView(t *model.ModelTemplate) *APIKeyView {
	view := &APIKeyView{}
	if len(t.APIKey) == 0 {
		return view
	}
	plain, err := s.cipher.Decrypt(t.APIKey)
	if err != nil {
		return view
	}
	view.Set = true
	view.Mask = maskSecret(string(plain))
	return view
}

// recordHealth 更新模板健康字段 + 追加检查历史 (裁剪超限部分)
func (s *modelTemplateService) recordHealth(ctx context.Context, t *model.ModelTemplate, view *ProbeView) {
	latency := view.LatencyMs
	if err := s.templates.UpdateHealth(ctx, t.ID, view.Status, &latency, view.Error); err != nil {
		_ = err
	}
	entry := &model.ModelHealthLog{
		ModelID:   t.ID,
		OK:        view.OK,
		LatencyMs: view.LatencyMs,
		Error:     view.Error,
	}
	if err := s.health.Append(ctx, entry); err != nil {
		_ = err
	}
	if err := s.health.Trim(ctx, t.ID, ModelHealthKeep); err != nil {
		_ = err
	}
}

// ensureQuotaFresh 按重置锚点滚动清零计数器 (内存操作, 不落库)
func ensureQuotaFresh(quota *model.ModelQuota, now time.Time) {
	for !now.Before(quota.ResetDailyAt) {
		quota.DailyUsed = 0
		quota.DailyTokenUsed = 0
		quota.ResetDailyAt = quota.ResetDailyAt.Add(24 * time.Hour)
	}
	for !now.Before(quota.ResetMonthlyAt) {
		quota.MonthlyUsed = 0
		quota.MonthlyTokenUsed = 0
		quota.ResetMonthlyAt = quota.ResetMonthlyAt.AddDate(0, 1, 0)
	}
}

// quotaExceeded 判断配额是否耗尽
func quotaExceeded(quota *model.ModelQuota) (bool, string) {
	if quota.DailyLimit > 0 && quota.DailyUsed >= quota.DailyLimit {
		return true, fmt.Sprintf("日配额耗尽 (%d/%d)", quota.DailyUsed, quota.DailyLimit)
	}
	if quota.MonthlyLimit > 0 && quota.MonthlyUsed >= quota.MonthlyLimit {
		return true, fmt.Sprintf("月配额耗尽 (%d/%d)", quota.MonthlyUsed, quota.MonthlyLimit)
	}
	if quota.DailyTokenLimit > 0 && quota.DailyTokenUsed >= quota.DailyTokenLimit {
		return true, fmt.Sprintf("日 token 配额耗尽 (%d/%d)", quota.DailyTokenUsed, quota.DailyTokenLimit)
	}
	if quota.MonthlyTokenLimit > 0 && quota.MonthlyTokenUsed >= quota.MonthlyTokenLimit {
		return true, fmt.Sprintf("月 token 配额耗尽 (%d/%d)", quota.MonthlyTokenUsed, quota.MonthlyTokenLimit)
	}
	return false, ""
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func startOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

// ============ 对话调用 (M2.5) ============

// ChatOutcome 一次真实模型对话调用的结果
type ChatOutcome struct {
	Content      string
	FinishReason string
	ToolCalls    []modelclient.ChatToolCall
	Model        string // 模型返回的模型名
	TemplateName string // 使用的模板名
	TemplateID   string
	TotalTokens  int
	LatencyMs    int
}

// orderedCandidates 返回按优先级排序的可路由模板 (Agent 优先模型置顶, 跳过不可用)
func (s *modelTemplateService) orderedCandidates(ctx context.Context, agentID string) []*model.ModelTemplate {
	templates, err := s.templates.ListForRoute(ctx)
	if err != nil || len(templates) == 0 {
		return nil
	}
	var available []*model.ModelTemplate
	for i := range templates {
		if _, skip := s.skipReason(ctx, &templates[i]); skip {
			continue
		}
		available = append(available, &templates[i])
	}

	preferred := ""
	if agentID != "" {
		if agent, aErr := s.agents.GetByID(ctx, agentID); aErr == nil {
			var cfg struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(agent.Config, &cfg) == nil {
				preferred = cfg.Model
			}
		}
	}
	if preferred == "" {
		return available
	}
	isPreferred := func(t *model.ModelTemplate) bool {
		return strings.EqualFold(t.Name, preferred) || strings.EqualFold(t.Model, preferred)
	}
	var ordered []*model.ModelTemplate
	for _, t := range available {
		if isPreferred(t) {
			ordered = append(ordered, t)
		}
	}
	for _, t := range available {
		if !isPreferred(t) {
			ordered = append(ordered, t)
		}
	}
	return ordered
}

// chatClient 构建对话用客户端 (对话超时可配置 MODEL_CHAT_TIMEOUT, 默认 120s, 长于探测超时)
func (s *modelTemplateService) chatClient(t *model.ModelTemplate) (*modelclient.Client, error) {
	apiKey := ""
	if len(t.APIKey) > 0 {
		plain, err := s.cipher.Decrypt(t.APIKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt api key (key changed?)")
		}
		apiKey = string(plain)
	}
	return modelclient.New(t.Provider, t.Endpoint, apiKey, s.chatTime), nil
}

// consumeUsage 配额消费 + 用量日志 (每次模型调用计次, 失败调用同样消耗)
func (s *modelTemplateService) consumeUsage(ctx context.Context, t *model.ModelTemplate, agentID string, tokens, latencyMs int, ok bool, errMsg string) {
	if quota, qErr := s.quotas.GetByModel(ctx, t.ID); qErr == nil && quota != nil {
		ensureQuotaFresh(quota, time.Now())
		quota.DailyUsed++
		quota.MonthlyUsed++
		quota.DailyTokenUsed += tokens
		quota.MonthlyTokenUsed += tokens
		if uErr := s.quotas.UpdateCounters(ctx, t.ID, quota.DailyUsed, quota.MonthlyUsed, quota.DailyTokenUsed, quota.MonthlyTokenUsed, quota.ResetDailyAt, quota.ResetMonthlyAt); uErr != nil {
			log.Printf("model service: quota update failed model=%s: %v", t.Name, uErr)
		}
	}
	agentIDPtr := &agentID
	entry := &model.ModelUsageLog{
		ModelID:   t.ID,
		AgentID:   agentIDPtr,
		OK:        ok,
		Tokens:    tokens,
		LatencyMs: latencyMs,
		Error:     errMsg,
	}
	if uErr := s.usage.Append(ctx, entry); uErr != nil {
		_ = uErr
	}
	if uErr := s.usage.Trim(ctx, t.ID, ModelUsageKeep); uErr != nil {
		_ = uErr
	}
}

// ErrNoModelAvailable 没有可用模型模板 (一个都没有, 或全部不可用)
var ErrNoModelAvailable = &errors.AppError{Code: "no_model_available", Message: "no available model template", HTTPCode: 400}

// RouteAndChat 路由选择 + 真实模型对话调用: 按优先级逐个尝试 (故障转移), 成功后消费配额并记录用量
// ModelAvailable 该 Agent 是否有可路由的模型模板 (dry-run, 不消耗配额)
func (s *modelTemplateService) ModelAvailable(ctx context.Context, agentID string) bool {
	return len(s.orderedCandidates(ctx, agentID)) > 0
}

func (s *modelTemplateService) RouteAndChat(ctx context.Context, agentID string, messages []modelclient.ChatMessage, tools []modelclient.ChatToolDef, gen modelclient.GenOptions) (*ChatOutcome, error) {
	candidates := s.orderedCandidates(ctx, agentID)
	if len(candidates) == 0 {
		return nil, ErrNoModelAvailable
	}

	var lastErr error
	for _, t := range candidates {
		client, err := s.chatClient(t)
		if err != nil {
			lastErr = err
			continue
		}
		start := time.Now()
		res, err := client.Chat(ctx, t.Model, messages, tools, gen)
		latency := int(time.Since(start).Milliseconds())
		if err != nil {
			lastErr = err
			s.consumeUsage(ctx, t, agentID, 0, latency, false, truncate(err.Error(), 300))
			continue
		}
		s.consumeUsage(ctx, t, agentID, res.TotalTokens, latency, true, "")
		return &ChatOutcome{
			Content:      res.Content,
			FinishReason: res.FinishReason,
			ToolCalls:    res.ToolCalls,
			Model:        res.Model,
			TemplateName: t.Name,
			TemplateID:   t.ID,
			TotalTokens:  res.TotalTokens,
			LatencyMs:    latency,
		}, nil
	}
	return nil, fmt.Errorf("all model templates failed: %v", lastErr)
}
