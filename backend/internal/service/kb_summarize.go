package service

// kb_summarize.go — M11 一键总结入库 (计划 §2 一键总结)
//
// SummarizeSession 生成流程:
//  1. 校验: 会话归属 + Agent 绑定非空 + KB_ENABLED (A5: 越权目标分类在入库环节 403, 下拉仅含绑定分类)
//  2. 取最近 KB_SUMMARY_MAX_TURNS 条 user/assistant 消息 (无 user 消息 -> 400)
//  3. LLM 调用: KB_SUMMARY_MODEL 优先, 空 = Agent 当前模型 (超时 KB_SUMMARY_TIMEOUT, 默认 120s; 用量经 ModelUsageLog 计量)
//  4. 固定结构化提示词 -> 严格 JSON 解析校验 (失败明确报错, 不产生半成品)
//  5. 建议分类: (首条 user 消息 + 标题) 对绑定分类名/描述做关键词打分, 取最高 (无区分度不返回)
//
// 落库走 POST /kb/documents (source=chat_summary, 服务端再次校验会话归属与 Agent 绑定)。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"agent-platform/internal/config"
	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"
)

// KB 总结生成约束
const (
	kbSummaryTitleMaxRunes   = 60                // 标题上限 (PRD 3.4: <=60 字)
	kbSummaryContentMinRunes = 300               // 正文下限 (质量门禁: 过短视为无有效知识, 不产出)
	kbSummaryContentMaxRunes = 1500              // 正文上限 (超出截断)
	kbSummaryDefaultTimeout  = 120 * time.Second // 总结生成超时默认值 (KB_SUMMARY_TIMEOUT 可配)
)

// KBSummaryDraft 总结草稿 (前端确认后经 POST /kb/documents 入库)
type KBSummaryDraft struct {
	Title               string                `json:"title"`
	Content             string                `json:"content"`
	SuggestedCategoryID string                `json:"suggested_category_id,omitempty"` // 建议分类 (无区分度为空)
	Categories          []AgentKBCategoryView `json:"categories"`                      // Agent 绑定分类 (前端下拉数据源)
}

// KBSummarizer 一键总结组件 (M11)
type KBSummarizer struct {
	sessions   repository.ChatSessionRepository
	messages   repository.ChatMessageRepository
	agents     repository.AgentRepository
	bindings   repository.AgentKBBindingRepository
	cats       repository.KBCategoryRepository
	modelSvc   ModelTemplateService
	summarySrc TemplateSource // 总结模型名运行时来源 (平台设置优先, KB_SUMMARY_MODEL 兜底; 空 = Agent 当前模型)
	enabled    *KBEnabledSource
	cfg        config.KBConfig
}

// NewKBSummarizer 创建总结组件
func NewKBSummarizer(
	sessions repository.ChatSessionRepository,
	messages repository.ChatMessageRepository,
	agents repository.AgentRepository,
	bindings repository.AgentKBBindingRepository,
	cats repository.KBCategoryRepository,
	modelSvc ModelTemplateService,
	summarySrc TemplateSource,
	enabled *KBEnabledSource,
	cfg config.KBConfig,
) *KBSummarizer {
	return &KBSummarizer{
		sessions:   sessions,
		messages:   messages,
		agents:     agents,
		bindings:   bindings,
		cats:       cats,
		modelSvc:   modelSvc,
		summarySrc: summarySrc,
		enabled:    enabled,
		cfg:        cfg,
	}
}

// SummarizeSession 生成会话总结草稿 (POST /agents/:id/sessions/:sid/kb-summary)
func (s *KBSummarizer) SummarizeSession(ctx context.Context, agentID, sessionID, focus string) (*KBSummaryDraft, error) {
	if s.enabled != nil && !s.enabled.Current() {
		return nil, errors.NewValidationError("知识库总开关已关闭")
	}
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.AgentID != agentID {
		return nil, errors.NewValidationError("会话不属于该 Agent")
	}
	if _, err := s.agents.GetByID(ctx, agentID); err != nil {
		return nil, errors.Wrap(err, "failed to get agent")
	}

	// 目标分类 (M11.5 只读绑定: 草稿分类仅含读写作用域 = 读写绑定 + 顶级 → 子级扩展;
	// 无任何读写绑定 -> 403 明确报错, 前端「总结入库」按钮禁用)
	bindings, err := s.bindings.ListByAgent(ctx, agentID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list agent kb bindings")
	}
	writable := kbAgentWritableScope(ctx, s.cats, bindings)
	if len(writable) == 0 {
		return nil, &errors.AppError{Code: "forbidden", Message: "该 Agent 未绑定可读写的知识分类, 无法总结入库 (只读绑定不可写入)", HTTPCode: 403}
	}
	catIDs := make([]string, 0, len(writable))
	for id := range writable {
		catIDs = append(catIDs, id)
	}
	cats := make([]AgentKBCategoryView, 0, len(catIDs))
	for _, id := range catIDs {
		cat, err := s.cats.Get(ctx, id)
		if err != nil {
			continue
		}
		cnt, _ := s.cats.CountDocuments(ctx, cat.ID, model.KBStatusActive)
		v := AgentKBCategoryView{ID: cat.ID, Name: cat.Name, Description: cat.Description, DocumentCount: int(cnt)}
		if cat.ParentID != nil {
			if parent, perr := s.cats.Get(ctx, *cat.ParentID); perr == nil {
				v.ParentName = parent.Name
			}
		}
		cats = append(cats, v)
	}
	// 顶级在前、子级在后, 同级按名称 (UI 展示稳定)
	sort.SliceStable(cats, func(i, j int) bool {
		if cats[i].ParentName == "" && cats[j].ParentName != "" {
			return true
		}
		if cats[i].ParentName != "" && cats[j].ParentName == "" {
			return false
		}
		return cats[i].Name < cats[j].Name
	})
	if len(cats) == 0 {
		return nil, errors.NewValidationError("该 Agent 绑定的知识库分类均不存在")
	}

	// 最近 N 条消息 (user/assistant)
	maxTurns := s.cfg.SummaryMaxTurns
	if maxTurns <= 0 {
		maxTurns = 20
	}
	msgs, err := s.messages.ListBySession(ctx, session.ID, maxTurns)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load session messages")
	}
	turns := make([]modelclient.ChatMessage, 0, maxTurns)
	var firstUserMsg string
	for i := range msgs {
		m := &msgs[i]
		if m.Role != model.ChatRoleUser && m.Role != model.ChatRoleAssistant {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		if m.Role == model.ChatRoleUser && firstUserMsg == "" {
			firstUserMsg = content
		}
		turns = append(turns, modelclient.ChatMessage{Role: m.Role, Content: content})
	}
	hasUser := false
	for i := range turns {
		if turns[i].Role == model.ChatRoleUser {
			hasUser = true
			break
		}
	}
	if !hasUser {
		return nil, errors.NewValidationError("会话中没有可总结的用户消息")
	}

	// LLM 调用 (超时 KB_SUMMARY_TIMEOUT, 默认 120s; 模板名: 平台设置/KB_SUMMARY_MODEL 优先, 空 = Agent 当前模型)
	timeout := s.cfg.SummaryTimeout
	if timeout <= 0 {
		timeout = kbSummaryDefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	prompt := kbSummaryPrompt(focus)
	llmMsgs := append([]modelclient.ChatMessage{{Role: "system", Content: prompt}}, turns...)
	outcome, err := s.modelSvc.ChatForMemory(cctx, agentID, s.summarySrc.Current(), llmMsgs, modelclient.GenOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "总结模型调用失败: "+err.Error())
	}

	// 严格解析校验 (失败不产出半成品)
	title, content, perr := parseKBSummary(outcome.Content)
	if perr != nil {
		return nil, perr
	}

	// 建议分类 (首条 user 消息 + 标题; 无区分度不返回)
	suggested := suggestKBCategory(firstUserMsg+" "+title, cats)

	return &KBSummaryDraft{
		Title:               title,
		Content:             content,
		SuggestedCategoryID: suggested,
		Categories:          cats,
	}, nil
}

// kbSummaryPrompt 固定结构化提示词 (计划 §2: 输出 JSON {title<=60字, content markdown 300-1500字})
func kbSummaryPrompt(focus string) string {
	var b strings.Builder
	b.WriteString("你是知识库总结助手。请基于下面的对话内容, 提炼出一条可复用的知识库条目。\n")
	b.WriteString("输出要求: 严格输出一个 JSON 对象, 不要输出任何其他文字、解释或代码块标记, 格式: {\"title\": \"...\", \"content\": \"...\"}\n")
	b.WriteString("- title: 条目标题, 不超过 60 个字符, 简明扼要概括条目主题;\n")
	b.WriteString("- content: 条目正文, Markdown 格式, 300-1500 个字符, 包含: 问题是什么 / 结论或答案 / 关键步骤或事实; 去除寒暄、跑题与无知识价值的内容; 保留具体数据、命令、配置等细节。\n")
	if focus := strings.TrimSpace(focus); focus != "" {
		fmt.Fprintf(&b, "用户指定的总结重点: %s (优先提炼与该重点相关的知识)。\n", focus)
	}
	b.WriteString("\n对话内容:\n")
	return b.String()
}

// parseKBSummary 严格解析 LLM 输出: 去代码块包裹 -> JSON 解析 -> 字段校验
func parseKBSummary(raw string) (string, string, error) {
	s := strings.TrimSpace(raw)
	// 容忍模型输出代码块包裹 (固定提示词下不应出现, 防御性处理)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		if strings.HasSuffix(s, "```") {
			s = strings.TrimSuffix(s, "```")
		}
		s = strings.TrimSpace(s)
	}
	// 截取首个 JSON 对象 (防御前后杂散文字)
	if start := strings.Index(s, "{"); start >= 0 {
		if end := strings.LastIndex(s, "}"); end > start {
			s = s[start : end+1]
		}
	}
	var parsed struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return "", "", errors.NewValidationError(fmt.Sprintf("总结结果不是合法 JSON, 请重试: %v", err))
	}
	title := strings.TrimSpace(parsed.Title)
	content := strings.TrimSpace(parsed.Content)
	if e := len([]rune(title)); e < 1 || e > kbSummaryTitleMaxRunes {
		return "", "", errors.NewValidationError(fmt.Sprintf("总结标题不合法 (须 1-%d 字符), 请重试", kbSummaryTitleMaxRunes))
	}
	if e := len([]rune(content)); e < kbSummaryContentMinRunes {
		return "", "", errors.NewValidationError(fmt.Sprintf("总结正文过短 (%d < %d 字符), 未提炼出有效知识, 请调整重点后重试", e, kbSummaryContentMinRunes))
	}
	if e := len([]rune(content)); e > kbSummaryContentMaxRunes {
		content = string([]rune(content)[:kbSummaryContentMaxRunes])
	}
	return title, content, nil
}

// suggestKBCategory 建议分类: 查询文本对绑定分类名/描述做关键词打分 (M10 打分工具),
// 取最高分; 最高分为 0 或与次高分持平 (无区分度) 时不返回
func suggestKBCategory(query string, cats []AgentKBCategoryView) string {
	queryTokens := tokenizeText(query)
	if len(queryTokens) == 0 || len(cats) == 0 {
		return ""
	}
	type scored struct {
		id    string
		score float64
	}
	scores := make([]scored, 0, len(cats))
	for i := range cats {
		cat := &cats[i]
		text := cat.Name + " " + cat.Description
		catTokens := tokenizeText(text)
		hit := 0
		for t := range queryTokens {
			if catTokens[t] {
				hit++
			}
		}
		score := float64(hit) / float64(len(queryTokens))
		if score += substringBonus(query, text); score > 1 {
			score = 1
		}
		scores = append(scores, scored{id: cat.ID, score: score})
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	if scores[0].score <= 0 {
		return ""
	}
	if len(scores) > 1 && scores[0].score == scores[1].score {
		return "" // 无区分度
	}
	return scores[0].id
}
