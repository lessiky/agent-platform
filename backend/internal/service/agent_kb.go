package service

// agent_kb.go — M11 Agent 知识库分类绑定 (计划 §2 绑定机制, 参照 M9 syncSkillBindings)
//
// 绑定语义: 全量同步 (新增缺失 / 移除多余), 校验分类存在 (400 返回缺失列表);
// 变更逐分类审计 (kb.agent_bound / kb.agent_unbound); Agent 删除级联清理绑定。

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"agent-platform/internal/model"
	"agent-platform/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// AgentKBCategoryView 绑定分类视图 (详情页签; M11.5: 只读标志 + 父级名展示 「父/子」 路径)
type AgentKBCategoryView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DocumentCount int    `json:"document_count"`
	ReadOnly      bool   `json:"read_only"`
	ParentName    string `json:"parent_name,omitempty"`
}

// AgentKBView Agent 知识库绑定视图
type AgentKBView struct {
	KbSearchMode string                 `json:"kb_search_mode"` // 生效值 (空已归一为 auto)
	Categories   []AgentKBCategoryView `json:"categories"`
}

// normalizeKbSearchMode 归一检索模式: 空 = auto; 非法值报错由调用方先校验
func normalizeKbSearchMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case model.KBSearchModeTool, model.KBSearchModeOff:
		return strings.TrimSpace(mode)
	default:
		return model.KBSearchModeAuto
	}
}

// validateKbSearchMode 校验检索模式取值 (auto/tool/off)
func validateKbSearchMode(mode string) error {
	if mode == "" {
		return nil
	}
	switch mode {
	case model.KBSearchModeAuto, model.KBSearchModeTool, model.KBSearchModeOff:
		return nil
	default:
		return errors.NewValidationError("kb_search_mode 须为 auto / tool / off")
	}
}

// validateKBCategoryPair 校验知识库分类绑定双集合 (M11.5 只读绑定):
// 两个集合全部存在 (400 返回缺失列表) + 交集非空 → 400 (同一分类不可同时读写与只读)
func (s *agentService) validateKBCategoryPair(ctx context.Context, rwIDs, roIDs []string) error {
	if err := s.validateKBCategories(ctx, rwIDs); err != nil {
		return err
	}
	if err := s.validateKBCategories(ctx, roIDs); err != nil {
		return err
	}
	seen := make(map[string]bool, len(rwIDs))
	for _, id := range rwIDs {
		if id = strings.TrimSpace(id); id != "" {
			seen[id] = true
		}
	}
	var overlap []string
	for _, id := range roIDs {
		id = strings.TrimSpace(id)
		if id != "" && seen[id] {
			overlap = append(overlap, id)
			seen[id] = false
		}
	}
	if len(overlap) > 0 {
		return errors.NewValidationError("knowledge_categories 与 knowledge_categories_readonly 交集须为空 (同一分类不可同时读写与只读): " + strings.Join(overlap, ", "))
	}
	return nil
}

// validateKBCategories 校验知识库分类绑定 (M11-1.8 参照): 全部存在, 400 返回缺失列表
func (s *agentService) validateKBCategories(ctx context.Context, categoryIDs []string) error {
	if len(categoryIDs) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var missing []string
	for _, id := range categoryIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := s.kbCats.Get(ctx, id); err != nil {
			if err == errors.ErrNotFound {
				missing = append(missing, id)
			} else {
				return errors.Wrap(err, "failed to check kb category")
			}
		}
	}
	if len(missing) > 0 {
		return errors.NewValidationError("绑定的知识库分类不存在: " + strings.Join(missing, ", "))
	}
	return nil
}

// syncKBBindings 全量同步 Agent 的知识库分类绑定 (M11.5: 读写 + 只读双集合; 新增缺失, 移除多余),
// 逐分类审计 (kb.agent_bound 含 read_only; 标志变更 rw<->ro 记录前后值)
func (s *agentService) syncKBBindings(ctx context.Context, agentID string, rwIDs, roIDs []string, operatorID string) error {
	current, err := s.kbBindings.ListByAgent(ctx, agentID)
	if err != nil {
		return errors.Wrap(err, "failed to list kb bindings")
	}
	currentRO := make(map[string]bool, len(current))
	for i := range current {
		currentRO[current[i].CategoryID] = current[i].ReadOnly
	}
	wanted := make(map[string]bool, len(rwIDs)+len(roIDs))
	wantedRO := make(map[string]bool, len(roIDs))
	bindOne := func(id string, readOnly bool) error {
		id = strings.TrimSpace(id)
		if id == "" || wanted[id] {
			return nil
		}
		wanted[id] = true
		wantedRO[id] = readOnly
		if err := s.kbBindings.Bind(ctx, agentID, id, readOnly, strPtr(operatorID)); err != nil {
			return errors.Wrap(err, "failed to bind kb category")
		}
		return nil
	}
	for _, id := range rwIDs {
		if err := bindOne(id, false); err != nil {
			return err
		}
	}
	for _, id := range roIDs {
		if err := bindOne(id, true); err != nil {
			return err
		}
	}
	agent, aErr := s.agents.GetByID(ctx, agentID)
	if aErr != nil {
		agent = nil
	}
	agentName := ""
	if agent != nil {
		agentName = agent.Name
	}
	// 新增 / 标志变更 -> kb.agent_bound (detail 含 read_only; 变更时含 old_read_only)
	for id := range wanted {
		oldRO, existed := currentRO[id]
		if existed && oldRO == wantedRO[id] {
			continue
		}
		detail := map[string]interface{}{
			"category_id": id, "category_name": s.kbCategoryName(ctx, id), "read_only": wantedRO[id],
		}
		if existed {
			detail["old_read_only"] = oldRO
		}
		s.kbAudit(ctx, operatorID, "kb.agent_bound", agentID, agentName, detail)
	}
	// 移除 -> kb.agent_unbound
	for _, b := range current {
		if wanted[b.CategoryID] {
			continue
		}
		if err := s.kbBindings.Unbind(ctx, agentID, b.CategoryID); err != nil {
			return errors.Wrap(err, "failed to unbind kb category")
		}
		s.kbAudit(ctx, operatorID, "kb.agent_unbound", agentID, agentName, map[string]interface{}{"category_id": b.CategoryID, "category_name": s.kbCategoryName(ctx, b.CategoryID), "read_only": b.ReadOnly})
	}
	return nil
}

// kbCategoryName 分类名 (审计/视图用, 失败返回空串)
func (s *agentService) kbCategoryName(ctx context.Context, categoryID string) string {
	if cat, err := s.kbCats.Get(ctx, categoryID); err == nil {
		return cat.Name
	}
	return ""
}

// kbAudit 绑定变更审计 (失败仅告警, 不阻塞主流程)
func (s *agentService) kbAudit(ctx context.Context, operatorID, action, agentID, agentName string, detail map[string]interface{}) {
	if s.audits == nil {
		return
	}
	entry := &model.AuditLog{
		Username: "",
		Action:   action,
		Resource: "agent",
		ResourceID: strPtr(agentID),
		CreatedAt:  time.Now(),
	}
	if operatorID != "" {
		entry.UserID = &operatorID
	}
	if detail != nil {
		payload, _ := json.Marshal(detail)
		entry.Detail = datatypes.JSON(payload)
	}
	if err := s.audits.Append(ctx, entry); err != nil {
		log.Printf("agent kb: audit append failed action=%s agent=%s: %v", action, agentID, err)
	}
}

// ListAgentKB Agent 知识库绑定视图 (GET /agents/:id/kb)
func (s *agentService) ListAgentKB(ctx context.Context, agentID string) (*AgentKBView, error) {
	agent, err := s.agents.GetByID(ctx, agentID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "failed to get agent")
	}
	var cfg AgentConfig
	_ = json.Unmarshal(agent.Config, &cfg)
	bindings, err := s.kbBindings.ListByAgent(ctx, agentID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list kb bindings")
	}
	view := &AgentKBView{KbSearchMode: normalizeKbSearchMode(cfg.KbSearchMode), Categories: make([]AgentKBCategoryView, 0, len(bindings))}
	parentNameCache := make(map[string]string)
	parentName := func(id string) string {
		if name, ok := parentNameCache[id]; ok {
			return name
		}
		if parent, err := s.kbCats.Get(ctx, id); err == nil {
			parentNameCache[id] = parent.Name
		} else {
			parentNameCache[id] = ""
		}
		return parentNameCache[id]
	}
	for i := range bindings {
		cat, err := s.kbCats.Get(ctx, bindings[i].CategoryID)
		if err != nil {
			continue // 孤儿绑定 (分类已删除)
		}
		cnt, _ := s.kbCats.CountDocuments(ctx, cat.ID, model.KBStatusActive)
		v := AgentKBCategoryView{
			ID:            cat.ID,
			Name:          cat.Name,
			Description:   cat.Description,
			DocumentCount: int(cnt),
			ReadOnly:      bindings[i].ReadOnly,
		}
		if cat.ParentID != nil {
			v.ParentName = parentName(*cat.ParentID)
		}
		view.Categories = append(view.Categories, v)
	}
	return view, nil
}

// TrialSearchForAgent Agent 作用域检索试算 (GET /agents/:id/kb/search):
// 服务端按 Agent 当前绑定重新鉴权 (不信任前端), 未配置检索组件时返回空结果
func (s *agentService) TrialSearchForAgent(ctx context.Context, agentID, query string, topK int) ([]KBSearchHit, error) {
	if _, err := s.agents.GetByID(ctx, agentID); err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "failed to get agent")
	}
	if s.kbRetriever == nil {
		return []KBSearchHit{}, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return []KBSearchHit{}, nil
	}
	return s.kbRetriever.Retrieve(ctx, agentID, query, KBSearchOptions{TopK: topK, BumpAccess: false})
}
