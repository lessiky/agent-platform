package service

// memory_extract.go — M10.2 turn 结束后的异步记忆管线: 自动抽取 + 会话滚动摘要
//
// 触发时机: 对话轮次成功落库后 (runTurn / ContinueAfterApproval), chatService 调用
// PostTurn (同步, O(1)) 累计会话轮次; 达到 ExtractMinTurns 时启动一次 detached 异步
// goroutine (context.Background + 90s 预算), 内部顺序执行:
//  1. LLM 事实抽取 -> 严格解析校验 -> per-scope 进程内锁去重 upsert -> 上限归档;
//  2. 会话 user/assistant 消息数超阈值时滚动摘要, 写回 ChatSession.Summary。
//
// 失败隔离: 任何失败仅记录日志 (AgentLog + std log), 绝不阻塞或回滚对话 (best-effort);
// 模型用量经 ChatForMemory -> consumeUsage 计入 ModelUsageLog / 配额, 与对话用量可观测。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"
)

const (
	memExtractDefaultTimeout = 90 * time.Second // 单次异步预算 (抽取 + 摘要共用, 设计文档 §2/§5)
	memExtractMaxItems       = 3                // 单批抽取数组长度上限 (设计文档 §5.2)
	memExtractContentMax     = 80               // 抽取单条 content 上限 (rune, 超长截断)
	memExtractReasonMax      = 30               // 抽取单条 reason 上限 (rune, 仅记 AgentLog 不入表)
	memExtractHistoryRounds  = 10               // 抽取上下文: 最近 N 轮 (1 轮 = 1 user + 1 assistant)
	memExtractMsgTruncate    = 500              // 提示词内单条消息截断上限 (rune, 控制成本)
	memSummaryMaxLen         = 300              // 滚动摘要输出上限 (rune, 设计文档 §7)
	memSummaryKeepRecent     = 20               // 摘要压缩时保留最近 N 条 user/assistant 不参与压缩
	memScopeLockLimit        = 10000            // 进程内计数器/锁 map 上限 (超出清理, best-effort)
)

// memExtractSystemPrompt 抽取提示词 (设计文档 §5.1)
const memExtractSystemPrompt = `你是一个记忆抽取器。从给定对话中提取值得跨会话长期记住的信息。
只提取: 用户偏好 / 稳定事实 / 重要决定 / 关键事件。
不要提取: 一次性任务细节、临时上下文、寒暄。
输出严格 JSON 数组 (无其他文本):
[{"content": "一句话记忆(<=80字)", "kind": "preference|fact|decision|event", "reason": "提取理由(<=30字)"}]
没有可提取内容时输出 []。`

// memSummarySystemPrompt 滚动摘要提示词 (设计文档 §7)
const memSummarySystemPrompt = `你是一个对话摘要器。将给定对话压缩为不超过 300 字的摘要。
要求: 保留用户的关键诉求、已做出的决定、稳定事实与未完成事项; 省略寒暄与过程细节。
只输出摘要正文, 不要标题、前缀或解释。`

// ExtractedMemory 单条抽取记忆 (模型输出, 设计文档 §5.1)
type ExtractedMemory struct {
	Content string `json:"content"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason"`
}

// parseExtractedMemories 模型输出严格解析校验 (纯函数, 设计文档 §5.2):
// 严格 JSON 数组 (兼容 ```json 围栏包裹); 长度 0-3; kind 白名单; content 非空且 <=80 字 (超长截断);
// 任一条目非法 -> 整批丢弃 (返回 error, 不做部分入库)
func parseExtractedMemories(raw string) ([]ExtractedMemory, error) {
	s := strings.TrimSpace(raw)
	// 兼容: 模型可能以 ```json 代码围栏包裹输出
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		if strings.HasSuffix(s, "```") {
			s = s[:len(s)-3]
		}
		s = strings.TrimSpace(s)
	}
	if !strings.HasPrefix(s, "[") {
		return nil, fmt.Errorf("output is not a JSON array: %.120s", s)
	}
	var items []ExtractedMemory
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %v", err)
	}
	if items == nil {
		return nil, fmt.Errorf("output is null, not a JSON array")
	}
	if len(items) > memExtractMaxItems {
		return nil, fmt.Errorf("too many items: %d > %d", len(items), memExtractMaxItems)
	}
	for i := range items {
		it := &items[i]
		it.Content = strings.TrimSpace(it.Content)
		if it.Content == "" {
			return nil, fmt.Errorf("item %d: empty content", i)
		}
		if _, ok := memoryKindLabels[it.Kind]; !ok {
			return nil, fmt.Errorf("item %d: invalid kind %q", i, it.Kind)
		}
		if runes := []rune(it.Content); len(runes) > memExtractContentMax {
			it.Content = string(runes[:memExtractContentMax])
		}
		it.Reason = strings.TrimSpace(it.Reason)
		if r := []rune(it.Reason); len(r) > memExtractReasonMax {
			it.Reason = string(r[:memExtractReasonMax])
		}
	}
	return items, nil
}

// MemoryExtractor turn 结束异步记忆管线 (M10.2): 自动抽取 + 会话滚动摘要
type MemoryExtractor struct {
	sessions repository.ChatSessionRepository
	messages repository.ChatMessageRepository
	memRepo  repository.MemoryRepository
	memSvc   MemoryService // 活跃记忆集缓存失效 (可为 nil)
	modelSvc ModelTemplateService
	logs     repository.AgentLogRepository

	enabled          bool           // 总开关 MEMORY_ENABLED
	extractEnabled   bool           // MEMORY_EXTRACT_ENABLED
	minTurns         int            // MEMORY_EXTRACT_MIN_TURNS
	extractModel     TemplateSource // 抽取/摘要模板名运行时来源 (平台设置优先, MEMORY_EXTRACT_MODEL 兜底; 空 = Agent 当前模型)
	maxPerScope      int
	summaryThreshold int
	asyncTimeout     time.Duration

	turnMu sync.Mutex
	turns  map[string]int // sessionID -> 距上次管线运行的轮次

	scopeMu  sync.Mutex
	scopeLks map[string]*sync.Mutex // agentID|userID -> 进程内锁 (并发去重, 设计文档 §13)
}

// NewMemoryExtractor 创建 turn 结束异步记忆管线 (M10.2); enabled=false 时整体不生效
func NewMemoryExtractor(
	sessions repository.ChatSessionRepository,
	messages repository.ChatMessageRepository,
	memRepo repository.MemoryRepository,
	memSvc MemoryService,
	modelSvc ModelTemplateService,
	logs repository.AgentLogRepository,
	enabled, extractEnabled bool,
	minTurns int,
	extractModel TemplateSource,
	maxPerScope, summaryThreshold int,
) *MemoryExtractor {
	if minTurns <= 0 {
		minTurns = 5
	}
	if maxPerScope <= 0 {
		maxPerScope = 500
	}
	if summaryThreshold <= 0 {
		summaryThreshold = 40
	}
	return &MemoryExtractor{
		sessions:         sessions,
		messages:         messages,
		memRepo:          memRepo,
		memSvc:           memSvc,
		modelSvc:         modelSvc,
		logs:             logs,
		enabled:          enabled,
		extractEnabled:   extractEnabled,
		minTurns:         minTurns,
		extractModel:     extractModel,
		maxPerScope:      maxPerScope,
		summaryThreshold: summaryThreshold,
		asyncTimeout:     memExtractDefaultTimeout,
		turns:            make(map[string]int),
		scopeLks:         make(map[string]*sync.Mutex),
	}
}

// extractModelName 当前生效的抽取/摘要模型模板名 (运行时来源; 空 = Agent 当前模型, 由 ChatForMemory 路由兜底)
func (e *MemoryExtractor) extractModelName() string {
	if e.extractModel == nil {
		return ""
	}
	return e.extractModel.Current()
}

// PostTurn 对话轮次成功落库后调用 (同步, O(1)): 累计会话轮次, 达到 minTurns 时
// 启动异步管线 (自动抽取 + 滚动摘要检查)。nil / 开关关闭时为空操作, 绝不影响对话主链。
func (e *MemoryExtractor) PostTurn(agent *model.Agent, session *model.ChatSession) {
	if e == nil || !e.enabled || agent == nil || session == nil || session.ID == "" {
		return
	}
	e.turnMu.Lock()
	e.turns[session.ID]++
	n := e.turns[session.ID]
	reach := n >= e.minTurns
	if reach {
		e.turns[session.ID] = 0
	} else if len(e.turns) > memScopeLockLimit {
		// 计数器 map 增长保护: 清理未达阈值的会话, 保留即将触发的
		for sid, c := range e.turns {
			if c < e.minTurns {
				delete(e.turns, sid)
			}
		}
	}
	e.turnMu.Unlock()
	if !reach {
		return
	}
	go e.runAsync(agent, session)
}

// runAsync 异步管线主体 (detached context + 90s 预算, 设计文档 §2)
func (e *MemoryExtractor) runAsync(agent *model.Agent, session *model.ChatSession) {
	ctx, cancel := context.WithTimeout(context.Background(), e.asyncTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("memory: async pipeline panic agent=%s session=%s: %v", agent.ID, session.ID, r)
		}
	}()
	if e.extractEnabled {
		e.extract(ctx, agent, session)
	}
	e.rollSummary(ctx, agent, session)
}

// extract LLM 事实抽取 + 去重 upsert + 上限归档 (best-effort, 失败仅日志)
func (e *MemoryExtractor) extract(ctx context.Context, agent *model.Agent, session *model.ChatSession) {
	if e.modelSvc == nil {
		return
	}
	// 抽取上下文: 最近 10 轮 user/assistant (ListBySession 含 tool 展示行, 多取后过滤)
	msgs, err := e.messages.ListBySession(ctx, session.ID, memExtractHistoryRounds*2+40)
	if err != nil {
		log.Printf("memory: extract load history failed session=%s: %v", session.ID, err)
		return
	}
	hist := make([]model.ChatMessage, 0, memExtractHistoryRounds*2)
	for i := len(msgs) - 1; i >= 0 && len(hist) < memExtractHistoryRounds*2; i-- {
		m := msgs[i]
		if m.Role != model.ChatRoleUser && m.Role != model.ChatRoleAssistant {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		hist = append(hist, m)
	}
	if len(hist) == 0 {
		return
	}
	var b strings.Builder
	for i := len(hist) - 1; i >= 0; i-- { // 恢复时间正序
		b.WriteString(hist[i].Role)
		b.WriteString(": ")
		b.WriteString(truncateRunes(hist[i].Content, memExtractMsgTruncate))
		b.WriteString("\n")
	}
	userPrompt := fmt.Sprintf("Agent 名称: %s\n最近对话 (最多 %d 轮):\n%s", agent.Name, memExtractHistoryRounds, b.String())
	out, err := e.modelSvc.ChatForMemory(ctx, agent.ID, e.extractModelName(), []modelclient.ChatMessage{
		{Role: "system", Content: memExtractSystemPrompt},
		{Role: "user", Content: userPrompt},
	}, modelclient.GenOptions{})
	if err != nil {
		e.logAgent(agent.ID, model.LogLevelWarn, fmt.Sprintf("memory extract model call failed session=%s: %v", session.ID, err))
		return
	}
	items, err := parseExtractedMemories(out.Content)
	if err != nil {
		e.logAgent(agent.ID, model.LogLevelWarn, fmt.Sprintf("memory extract output invalid session=%s error=%s raw=%.200s", session.ID, err, out.Content))
		return
	}
	if len(items) == 0 {
		return
	}
	e.upsertExtracted(ctx, agent, session, items, out.TemplateName, out.TotalTokens)
}

// upsertExtracted 抽取条目去重 upsert (设计文档 §4.4):
// 归一化后精确相等 -> touch; bigram Jaccard >= 0.7 -> 保留更长 content; 否则新增。
// per-scope 进程内锁并发去重; 写入后按上限自动归档最低分 (设计文档 §3.3)。
func (e *MemoryExtractor) upsertExtracted(ctx context.Context, agent *model.Agent, session *model.ChatSession, items []ExtractedMemory, modelUsed string, tokens int) {
	agentID := agent.ID
	userID := session.UserID // user 级记忆归属会话属主; 无用户会话 (如 /invoke) -> Agent 级
	lock := e.scopeLock(agentID, userID)
	lock.Lock()
	defer lock.Unlock()

	existing, err := e.memRepo.ListActiveForScope(ctx, agentID, userID, e.maxPerScope+len(items))
	if err != nil {
		log.Printf("memory: extract load scope failed agent=%s: %v", agentID, err)
		return
	}
	written := 0
	reembed := []model.Memory{} // M10.3: 内容新增/变更的记忆 (异步重算向量)
	for i := range items {
		it := &items[i]
		normalized := normalizeContent(it.Content)
		matched := false
		for j := range existing {
			m := &existing[j]
			if normalizeContent(m.Content) == normalized {
				// 精确重复: touch updated_at + access_count
				if _, err := e.memRepo.Update(ctx, agentID, m.ID, map[string]interface{}{
					"access_count": m.AccessCount + 1,
					"updated_at":   time.Now(),
				}); err == nil {
					matched = true
					m.AccessCount++
				}
				break
			}
			if contentSimilar(m.Content, it.Content) >= memDedupThreshold {
				// 近重复: 保留更长 content, 更新访问统计
				fields := map[string]interface{}{
					"access_count": m.AccessCount + 1,
					"updated_at":   time.Now(),
				}
				if len([]rune(it.Content)) > len([]rune(m.Content)) {
					fields["content"] = it.Content
				}
				if _, err := e.memRepo.Update(ctx, agentID, m.ID, fields); err == nil {
					matched = true
					if c, ok := fields["content"]; ok {
						m.Content = c.(string)
						reembed = append(reembed, *m) // M10.3: content 被替换, 向量过期
					}
					m.AccessCount++
				}
				break
			}
		}
		if matched {
			continue
		}
		mem := &model.Memory{
			AgentID: agentID,
			UserID:  userID,
			Kind:    it.Kind,
			Content: it.Content,
			Source:  model.MemorySourceLLMExtracted,
			Status:  model.MemoryStatusActive,
		}
		if err := e.memRepo.Create(ctx, mem); err != nil {
			log.Printf("memory: extract create failed agent=%s: %v", agentID, err)
			continue
		}
		existing = append(existing, *mem)
		reembed = append(reembed, *mem) // M10.3: 新记忆需算向量
		written++
	}
	// 上限归档: 超出 maxPerScope 时自动归档最低分 (设计文档 §3.3)
	archived := e.enforceScopeCap(ctx, agentID, userID, existing)
	if written > 0 || archived > 0 {
		if e.memSvc != nil {
			e.memSvc.InvalidateCache(agentID)
		}
	}
	if len(reembed) > 0 && e.memSvc != nil {
		e.memSvc.EmbedAsync(agentID, reembed) // M10.3: 异步算向量 (失败留空降级)
	}
	// reason 仅记 AgentLog 不入表 (设计文档 §5.2)
	for i := range items {
		if items[i].Reason != "" {
			e.logAgent(agentID, model.LogLevelInfo, fmt.Sprintf("memory extract reason session=%s kind=%s content=%.80s reason=%s",
				session.ID, items[i].Kind, items[i].Content, items[i].Reason))
		}
	}
	e.logAgent(agentID, model.LogLevelInfo, fmt.Sprintf("memory extract ok session=%s items=%d written=%d archived=%d model=%s tokens=%d",
		session.ID, len(items), written, archived, modelUsed, tokens))
}

// enforceScopeCap 每 scope 活跃上限 (设计文档 §3.3): 超限自动归档最低分
// (无查询词时综合分 = 时间衰减 + 使用频率, 见 memoryScore)
func (e *MemoryExtractor) enforceScopeCap(ctx context.Context, agentID string, userID *string, active []model.Memory) int {
	if len(active) <= e.maxPerScope {
		return 0
	}
	type scored struct {
		id    string
		score float64
		upd   time.Time
	}
	list := make([]scored, 0, len(active))
	now := time.Now()
	for i := range active {
		m := &active[i]
		list = append(list, scored{id: m.ID, score: memoryScore(0, m, now), upd: m.UpdatedAt})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score < list[j].score
		}
		return list[i].upd.Before(list[j].upd)
	})
	archived := 0
	for _, s := range list {
		if len(active)-archived <= e.maxPerScope {
			break
		}
		if _, err := e.memRepo.Update(ctx, agentID, s.id, map[string]interface{}{"status": model.MemoryStatusArchived}); err != nil {
			log.Printf("memory: scope cap archive failed agent=%s id=%s: %v", agentID, s.id, err)
			break
		}
		archived++
	}
	return archived
}

// rollSummary 会话滚动摘要 (设计文档 §7):
// 触发: user/assistant 消息数 > summaryThreshold;
// 压缩输入: 旧 Summary (若有) + 最早至倒数第 20 条之前的消息; 输出 <=300 字写回 ChatSession.Summary
func (e *MemoryExtractor) rollSummary(ctx context.Context, agent *model.Agent, session *model.ChatSession) {
	if e.modelSvc == nil {
		return
	}
	count, err := e.messages.CountChat(ctx, session.ID)
	if err != nil {
		log.Printf("memory: summary count failed session=%s: %v", session.ID, err)
		return
	}
	if count <= int64(e.summaryThreshold) {
		return
	}
	oldest, err := e.messages.ListForSummary(ctx, session.ID, memSummaryKeepRecent)
	if err != nil {
		log.Printf("memory: summary load history failed session=%s: %v", session.ID, err)
		return
	}
	if len(oldest) == 0 {
		return
	}
	var b strings.Builder
	if s := strings.TrimSpace(session.Summary); s != "" {
		b.WriteString("已有摘要:\n")
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString("待压缩对话 (更早部分):\n")
	for i := range oldest {
		m := &oldest[i]
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(truncateRunes(m.Content, memExtractMsgTruncate))
		b.WriteString("\n")
	}
	out, err := e.modelSvc.ChatForMemory(ctx, agent.ID, e.extractModelName(), []modelclient.ChatMessage{
		{Role: "system", Content: memSummarySystemPrompt},
		{Role: "user", Content: b.String()},
	}, modelclient.GenOptions{})
	if err != nil {
		e.logAgent(agent.ID, model.LogLevelWarn, fmt.Sprintf("memory summary model call failed session=%s: %v", session.ID, err))
		return
	}
	summary := strings.TrimSpace(out.Content)
	summary = strings.Trim(summary, "\"\u201c\u201d'")
	if summary == "" || summary == strings.TrimSpace(session.Summary) {
		return
	}
	if runes := []rune(summary); len(runes) > memSummaryMaxLen {
		summary = string(runes[:memSummaryMaxLen])
	}
	if err := e.sessions.UpdateSummary(ctx, session.ID, summary); err != nil {
		e.logAgent(agent.ID, model.LogLevelWarn, fmt.Sprintf("memory summary persist failed session=%s: %v", session.ID, err))
		return
	}
	e.logAgent(agent.ID, model.LogLevelInfo, fmt.Sprintf("memory summary ok session=%s msgs=%d chars=%d model=%s tokens=%d",
		session.ID, count, len([]rune(summary)), out.TemplateName, out.TotalTokens))
}

// scopeLock per-scope (agent, user) 进程内锁 (设计文档 §13: 并发抽取重复条目防护)
func (e *MemoryExtractor) scopeLock(agentID string, userID *string) *sync.Mutex {
	key := agentID
	if userID != nil {
		key += "|" + *userID
	}
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	if len(e.scopeLks) > memScopeLockLimit {
		e.scopeLks = make(map[string]*sync.Mutex)
	}
	lk, ok := e.scopeLks[key]
	if !ok {
		lk = &sync.Mutex{}
		e.scopeLks[key] = lk
	}
	return lk
}

// logAgent 写 Agent 执行日志 (尽力而为, 失败仅 std log)
func (e *MemoryExtractor) logAgent(agentID, level, message string) {
	if e.logs == nil {
		return
	}
	entry := &model.AgentLog{AgentID: agentID, Level: level, Message: message}
	if err := e.logs.Append(context.Background(), []*model.AgentLog{entry}); err != nil {
		log.Printf("memory: write agent log failed agent=%s: %v", agentID, err)
	}
}
