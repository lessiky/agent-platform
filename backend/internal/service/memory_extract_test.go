package service

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/internal/model"
	"agent-platform/internal/modelclient"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"
)

// ---------- fakes (M10.2 抽取管线单测) ----------

// fakeExtractMemRepo 记忆仓储假实现: 内存切片 + Update 字段应用
type fakeExtractMemRepo struct {
	repository.MemoryRepository
	mu      sync.Mutex
	items   []model.Memory
	seq     int
	touched int // Update 调用次数
}

func (f *fakeExtractMemRepo) Create(ctx context.Context, m *model.Memory) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	m.ID = "mem-" + strconv.Itoa(f.seq)
	now := time.Now()
	m.CreatedAt = now
	m.UpdatedAt = now
	f.items = append(f.items, *m)
	return nil
}

func (f *fakeExtractMemRepo) ListActiveForScope(ctx context.Context, agentID string, userID *string, limit int) ([]model.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Memory
	for _, m := range f.items {
		if m.AgentID != agentID || m.Status != model.MemoryStatusActive {
			continue
		}
		if userID == nil {
			if m.UserID != nil {
				continue
			}
		} else if m.UserID == nil || *m.UserID != *userID {
			continue
		}
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeExtractMemRepo) Update(ctx context.Context, agentID, id string, fields map[string]interface{}) (*model.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.items {
		m := &f.items[i]
		if m.ID != id || m.AgentID != agentID {
			continue
		}
		f.touched++
		for k, v := range fields {
			switch k {
			case "content":
				m.Content = v.(string)
			case "access_count":
				m.AccessCount = v.(int)
			case "status":
				m.Status = v.(string)
			case "updated_at":
				m.UpdatedAt = v.(time.Time)
			}
		}
		return m, nil
	}
	return nil, errors.ErrNotFound
}

func (f *fakeExtractMemRepo) snapshot() []model.Memory {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.Memory, len(f.items))
	copy(out, f.items)
	return out
}

// fakeExtractMessages 会话消息仓储假实现
type fakeExtractMessages struct {
	repository.ChatMessageRepository
	msgs  []model.ChatMessage
	count int64
}

func (f *fakeExtractMessages) ListBySession(ctx context.Context, sessionID string, limit int) ([]model.ChatMessage, error) {
	if len(f.msgs) > limit {
		return f.msgs[len(f.msgs)-limit:], nil
	}
	return f.msgs, nil
}

func (f *fakeExtractMessages) CountChat(ctx context.Context, sessionID string) (int64, error) {
	return f.count, nil
}

func (f *fakeExtractMessages) ListForSummary(ctx context.Context, sessionID string, skipNewest int) ([]model.ChatMessage, error) {
	if skipNewest >= len(f.msgs) {
		return nil, nil
	}
	return f.msgs[:len(f.msgs)-skipNewest], nil
}

// fakeExtractSessions 会话仓储假实现: 记录 Summary 写入
type fakeExtractSessions struct {
	repository.ChatSessionRepository
	summaries map[string]string
}

func (f *fakeExtractSessions) UpdateSummary(ctx context.Context, id, summary string) error {
	f.summaries[id] = summary
	return nil
}

// fakeExtractModel 模型服务假实现: ChatForMemory 返回预设内容并记录调用
type fakeExtractModel struct {
	ModelTemplateService
	mu           sync.Mutex
	calls        int
	lastTemplate string
	lastMessages []modelclient.ChatMessage
	content      string
	err          error
}

func (f *fakeExtractModel) ChatForMemory(ctx context.Context, agentID, templateName string, messages []modelclient.ChatMessage, gen modelclient.GenOptions) (*ChatOutcome, error) {
	f.mu.Lock()
	f.calls++
	f.lastTemplate = templateName
	f.lastMessages = messages
	content, err := f.content, f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &ChatOutcome{Content: content, TemplateName: "fake-extract", TotalTokens: 7}, nil
}

func (f *fakeExtractModel) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeExtractModel) lastTemplateName() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTemplate
}

// waitForCalls 轮询等待模型调用次数达到 n (异步 goroutine 同步用)
func (f *fakeExtractModel) waitForCalls(n int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f.callCount() >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f.callCount() >= n
}

// newTestExtractor 组装测试用管线
func newTestExtractor(memRepo *fakeExtractMemRepo, msgs *fakeExtractMessages, sessions *fakeExtractSessions, modelSvc *fakeExtractModel, extractEnabled bool, minTurns, maxPerScope, summaryThreshold int) *MemoryExtractor {
	return NewMemoryExtractor(
		sessions,
		msgs,
		memRepo,
		nil, // memSvc 缓存失效: 单测不需要
		modelSvc,
		&fakeLogRepo{},
		true,
		extractEnabled,
		minTurns,
		StaticTemplateSource(""),
		maxPerScope,
		summaryThreshold,
	)
}

func testAgent() *model.Agent {
	return &model.Agent{ID: "agent-1", Name: "测试Agent"}
}

func testSession(userID *string) *model.ChatSession {
	return &model.ChatSession{ID: "session-1", AgentID: "agent-1", UserID: userID}
}

// ---------- 解析校验 (设计文档 §5.2) ----------

func TestParseExtractedMemories(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		want    int
		check   func(t *testing.T, items []ExtractedMemory)
	}{
		{
			name: "valid two items",
			raw:  `[{"content":"用户偏好简洁","kind":"preference","reason":"偏好"},{"content":"用户写 Go","kind":"fact","reason":"事实"}]`,
			want: 2,
			check: func(t *testing.T, items []ExtractedMemory) {
				if items[0].Kind != "preference" || items[1].Kind != "fact" {
					t.Fatalf("kinds = %v %v", items[0].Kind, items[1].Kind)
				}
			},
		},
		{name: "empty array", raw: `[]`, want: 0},
		{
			name:    "more than 3 items discarded",
			raw:     `[{"content":"a","kind":"fact","reason":""},{"content":"b","kind":"fact","reason":""},{"content":"c","kind":"fact","reason":""},{"content":"d","kind":"fact","reason":""}]`,
			wantErr: true,
		},
		{
			name:    "invalid kind discarded",
			raw:     `[{"content":"用户写 Go","kind":"opinion","reason":""}]`,
			wantErr: true,
		},
		{
			name:    "empty content discarded",
			raw:     `[{"content":"  ","kind":"fact","reason":""}]`,
			wantErr: true,
		},
		{
			name: "overlong content truncated",
			raw:  `[{"content":"` + strings.Repeat("记", 100) + `","kind":"fact","reason":"` + strings.Repeat("理", 40) + `"}]`,
			want: 1,
			check: func(t *testing.T, items []ExtractedMemory) {
				if got := len([]rune(items[0].Content)); got != memExtractContentMax {
					t.Fatalf("content len = %d, want %d", got, memExtractContentMax)
				}
				if got := len([]rune(items[0].Reason)); got != memExtractReasonMax {
					t.Fatalf("reason len = %d, want %d", got, memExtractReasonMax)
				}
			},
		},
		{name: "non-JSON discarded", raw: `这是自然语言，不是 JSON`, wantErr: true},
		{name: "null discarded", raw: `null`, wantErr: true},
		{
			name: "json fence wrapped accepted",
			raw:  "```json\n[{\"content\":\"用户写 Go\",\"kind\":\"fact\",\"reason\":\"\"}]\n```",
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := parseExtractedMemories(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", items)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(items) != tc.want {
				t.Fatalf("items = %d, want %d", len(items), tc.want)
			}
			if tc.check != nil {
				tc.check(t, items)
			}
		})
	}
}

// ---------- 限流 + 抽取 upsert ----------

func TestPostTurnThrottleAndExtract(t *testing.T) {
	userID := "user-1"
	memRepo := &fakeExtractMemRepo{}
	msgs := &fakeExtractMessages{
		msgs: []model.ChatMessage{
			{Role: model.ChatRoleUser, Content: "我是后端工程师，主力语言 Go"},
			{Role: model.ChatRoleAssistant, Content: "好的，记住了。"},
			{Role: model.ChatRoleUser, Content: "我喜欢简洁直接的回答"},
			{Role: model.ChatRoleAssistant, Content: "了解。"},
		},
	}
	sessions := &fakeExtractSessions{summaries: map[string]string{}}
	modelSvc := &fakeExtractModel{content: `[{"content":"用户是后端工程师","kind":"fact","reason":"自我介绍"},{"content":"用户偏好简洁直接的回答","kind":"preference","reason":"沟通偏好"}]`}
	e := newTestExtractor(memRepo, msgs, sessions, modelSvc, true, 2, 500, 40)

	e.PostTurn(testAgent(), testSession(&userID)) // 第 1 轮: 未达阈值
	time.Sleep(80 * time.Millisecond)
	if modelSvc.callCount() != 0 {
		t.Fatalf("extract triggered before minTurns: calls=%d", modelSvc.callCount())
	}

	e.PostTurn(testAgent(), testSession(&userID)) // 第 2 轮: 达到 minTurns, 触发异步管线
	if !modelSvc.waitForCalls(1) {
		t.Fatalf("extract not triggered on 2nd turn")
	}
	items := memRepo.snapshot()
	if len(items) != 2 {
		t.Fatalf("created %d memories, want 2: %+v", len(items), items)
	}
	for _, m := range items {
		if m.Source != model.MemorySourceLLMExtracted {
			t.Fatalf("source = %s, want llm_extracted", m.Source)
		}
		if m.UserID == nil || *m.UserID != userID {
			t.Fatalf("user scope wrong: %+v", m.UserID)
		}
	}
	// 提示词结构: system 抽取器 + user 含 Agent 名与历史
	if len(modelSvc.lastMessages) != 2 || !strings.Contains(modelSvc.lastMessages[0].Content, "记忆抽取器") {
		t.Fatalf("extract prompt missing system extractor: %+v", modelSvc.lastMessages)
	}
	userPrompt := modelSvc.lastMessages[1].Content
	if !strings.Contains(userPrompt, "测试Agent") || !strings.Contains(userPrompt, "后端工程师") {
		t.Fatalf("extract user prompt wrong: %s", userPrompt)
	}

	// 第 3 轮: 窗口内不重复抽取
	e.PostTurn(testAgent(), testSession(&userID))
	time.Sleep(80 * time.Millisecond)
	if modelSvc.callCount() != 1 {
		t.Fatalf("extract re-triggered within window: calls=%d", modelSvc.callCount())
	}

	// 第 4 轮: 再次触发, 相同条目走去重 (touch 而非新增)
	e.PostTurn(testAgent(), testSession(&userID))
	if !modelSvc.waitForCalls(2) {
		t.Fatalf("2nd extract not triggered on 4th turn")
	}
	items = memRepo.snapshot()
	if len(items) != 2 {
		t.Fatalf("dedup failed, %d memories after 2nd extract: %+v", len(items), items)
	}
	for _, m := range items {
		if m.AccessCount != 1 {
			t.Fatalf("access_count = %d, want 1 (dedup touch)", m.AccessCount)
		}
	}
}

func TestExtractAgentScopeForUserlessSession(t *testing.T) {
	memRepo := &fakeExtractMemRepo{}
	msgs := &fakeExtractMessages{msgs: []model.ChatMessage{
		{Role: model.ChatRoleUser, Content: "hi"},
		{Role: model.ChatRoleAssistant, Content: "hello"},
	}}
	modelSvc := &fakeExtractModel{content: `[{"content":"用户部署用 docker","kind":"fact","reason":""}]`}
	e := newTestExtractor(memRepo, msgs, &fakeExtractSessions{summaries: map[string]string{}}, modelSvc, true, 1, 500, 40)

	e.PostTurn(testAgent(), testSession(nil))
	if !modelSvc.waitForCalls(1) {
		t.Fatalf("extract not triggered")
	}
	items := memRepo.snapshot()
	if len(items) != 1 || items[0].UserID != nil {
		t.Fatalf("want one agent-level memory, got %+v", items)
	}
}

func TestExtractInvalidOutputDiscarded(t *testing.T) {
	memRepo := &fakeExtractMemRepo{}
	msgs := &fakeExtractMessages{msgs: []model.ChatMessage{
		{Role: model.ChatRoleUser, Content: "hi"},
		{Role: model.ChatRoleAssistant, Content: "hello"},
	}}
	modelSvc := &fakeExtractModel{content: `抱歉，我无法输出 JSON`}
	e := newTestExtractor(memRepo, msgs, &fakeExtractSessions{summaries: map[string]string{}}, modelSvc, true, 1, 500, 40)

	e.PostTurn(testAgent(), testSession(nil))
	if !modelSvc.waitForCalls(1) {
		t.Fatalf("extract not triggered")
	}
	if items := memRepo.snapshot(); len(items) != 0 {
		t.Fatalf("invalid output must not create memories: %+v", items)
	}
}

func TestExtractDisabledAndMasterSwitch(t *testing.T) {
	msgs := &fakeExtractMessages{}
	modelSvc := &fakeExtractModel{content: `[]`}
	e := newTestExtractor(&fakeExtractMemRepo{}, msgs, &fakeExtractSessions{summaries: map[string]string{}}, modelSvc, false, 1, 500, 40)
	e.PostTurn(testAgent(), testSession(nil))
	time.Sleep(80 * time.Millisecond)
	if modelSvc.callCount() != 0 {
		t.Fatalf("extract ran with MEMORY_EXTRACT_ENABLED=false")
	}

	modelSvc2 := &fakeExtractModel{content: `[]`}
	e2 := NewMemoryExtractor(
		&fakeExtractSessions{summaries: map[string]string{}}, msgs, &fakeExtractMemRepo{}, nil, modelSvc2,
		&fakeLogRepo{}, false, true, 1, StaticTemplateSource(""), 500, 40) // 总开关关闭
	e2.PostTurn(testAgent(), testSession(nil))
	time.Sleep(80 * time.Millisecond)
	if modelSvc2.callCount() != 0 {
		t.Fatalf("pipeline ran with MEMORY_ENABLED=false")
	}
}

// ---------- 上限归档 (设计文档 §3.3) ----------

func TestScopeCapArchivesLowestScore(t *testing.T) {
	userID := "user-1"
	memRepo := &fakeExtractMemRepo{}
	// 预置 3 条活跃 (不同时间, 最旧者得分最低)
	now := time.Now()
	pre := []model.Memory{
		{ID: "m-old", AgentID: "agent-1", UserID: &userID, Kind: "fact", Content: "旧记忆一", Source: "llm_extracted", Status: model.MemoryStatusActive, UpdatedAt: now.Add(-40 * 24 * time.Hour)},
		{ID: "m-mid", AgentID: "agent-1", UserID: &userID, Kind: "fact", Content: "旧记忆二", Source: "llm_extracted", Status: model.MemoryStatusActive, UpdatedAt: now.Add(-10 * 24 * time.Hour)},
		{ID: "m-new", AgentID: "agent-1", UserID: &userID, Kind: "fact", Content: "旧记忆三", Source: "llm_extracted", Status: model.MemoryStatusActive, UpdatedAt: now.Add(-1 * 24 * time.Hour)},
	}
	for i := range pre {
		memRepo.items = append(memRepo.items, pre[i])
	}
	msgs := &fakeExtractMessages{msgs: []model.ChatMessage{
		{Role: model.ChatRoleUser, Content: "hi"},
		{Role: model.ChatRoleAssistant, Content: "hello"},
	}}
	modelSvc := &fakeExtractModel{content: `[{"content":"全新记忆条目","kind":"fact","reason":""}]`}
	e := newTestExtractor(memRepo, msgs, &fakeExtractSessions{summaries: map[string]string{}}, modelSvc, true, 1, 2, 40)

	e.PostTurn(testAgent(), testSession(&userID))
	if !modelSvc.waitForCalls(1) {
		t.Fatalf("extract not triggered")
	}
	byID := map[string]model.Memory{}
	for _, m := range memRepo.snapshot() {
		byID[m.ID] = m
	}
	if len(byID) != 4 {
		t.Fatalf("want 4 memories, got %d", len(byID))
	}
	if byID["m-old"].Status != model.MemoryStatusArchived || byID["m-mid"].Status != model.MemoryStatusArchived {
		t.Fatalf("lowest-score memories not archived: old=%s mid=%s", byID["m-old"].Status, byID["m-mid"].Status)
	}
	if byID["m-new"].Status != model.MemoryStatusActive {
		t.Fatalf("m-new should stay active, got %s", byID["m-new"].Status)
	}
}

// ---------- 会话滚动摘要 (设计文档 §7) ----------

func TestRollSummaryTriggered(t *testing.T) {
	// 46 条 user/assistant 消息 (23 轮), 超过默认阈值 40
	var msgs []model.ChatMessage
	for i := 0; i < 23; i++ {
		msgs = append(msgs,
			model.ChatMessage{Role: model.ChatRoleUser, Content: "问题" + strconv.Itoa(i)},
			model.ChatMessage{Role: model.ChatRoleAssistant, Content: "回答" + strconv.Itoa(i)},
		)
	}
	fakeMsgs := &fakeExtractMessages{msgs: msgs, count: 46}
	sessions := &fakeExtractSessions{summaries: map[string]string{}}
	longSummary := strings.Repeat("摘", 400)
	modelSvc := &fakeExtractModel{content: longSummary}
	e := newTestExtractor(&fakeExtractMemRepo{}, fakeMsgs, sessions, modelSvc, false, 1, 500, 40)

	session := testSession(nil)
	session.Summary = "已有摘要: 用户讨论过部署方案"
	e.PostTurn(testAgent(), session)
	if !modelSvc.waitForCalls(1) {
		t.Fatalf("summary not triggered with 46 messages")
	}
	got, ok := sessions.summaries["session-1"]
	if !ok {
		t.Fatalf("summary not persisted")
	}
	if len([]rune(got)) > memSummaryMaxLen {
		t.Fatalf("summary len = %d, want <= %d", len([]rune(got)), memSummaryMaxLen)
	}
	// 压缩输入: 旧摘要 + 最早消息 (排除最近 20 条)
	prompt := modelSvc.lastMessages[1].Content
	if !strings.Contains(prompt, "用户讨论过部署方案") {
		t.Fatalf("summary prompt missing old summary: %s", prompt)
	}
	if !strings.Contains(prompt, "问题0") {
		t.Fatalf("summary prompt missing earliest messages: %s", prompt)
	}
	if strings.Contains(prompt, "问题22") {
		t.Fatalf("summary prompt must exclude the 20 most recent messages")
	}
}

func TestRollSummaryBelowThresholdSkipped(t *testing.T) {
	fakeMsgs := &fakeExtractMessages{count: 40} // 恰好等于阈值, 不触发
	modelSvc := &fakeExtractModel{content: "摘要"}
	e := newTestExtractor(&fakeExtractMemRepo{}, fakeMsgs, &fakeExtractSessions{summaries: map[string]string{}}, modelSvc, false, 1, 500, 40)
	e.PostTurn(testAgent(), testSession(nil))
	time.Sleep(150 * time.Millisecond)
	if modelSvc.callCount() != 0 {
		t.Fatalf("summary triggered at threshold (should require > 40)")
	}
}

// ---------- 摘要注入 (runTurn / 续答共用) ----------

func TestWithSessionSummary(t *testing.T) {
	messages := []modelclient.ChatMessage{{Role: "user", Content: "你好"}}

	got := withSessionSummary("", messages)
	if len(got) != 1 || got[0].Content != "你好" {
		t.Fatalf("empty summary must not inject: %+v", got)
	}

	got = withSessionSummary("  摘要内容  ", messages)
	if len(got) != 2 {
		t.Fatalf("want 2 messages, got %d", len(got))
	}
	want := "以下是更早对话的摘要：" + "摘要内容"
	if got[0].Role != model.ChatRoleUser || got[0].Content != want {
		t.Fatalf("summary injection wrong: %+v", got[0])
	}
	if got[1].Content != "你好" {
		t.Fatalf("history order broken: %+v", got[1])
	}
}

// TestExtractor_ModelSourceSwitch 抽取模型名来自运行时来源: 平台设置切换后新抽取按新模板调用 (免重启)
func TestExtractor_ModelSourceSwitch(t *testing.T) {
	userID := "user-1"
	memRepo := &fakeExtractMemRepo{}
	msgs := &fakeExtractMessages{
		msgs: []model.ChatMessage{
			{Role: model.ChatRoleUser, Content: "我是后端工程师，主力语言 Go"},
			{Role: model.ChatRoleAssistant, Content: "好的，记住了。"},
		},
	}
	sessions := &fakeExtractSessions{summaries: map[string]string{}}
	modelSvc := &fakeExtractModel{content: `[{"content":"用户是后端工程师","kind":"fact","reason":"自我介绍"}]`}

	// 未配置: 空串 (Agent 当前模型)
	src := NewMutableTemplateSource("")
	e := NewMemoryExtractor(sessions, msgs, memRepo, nil, modelSvc, &fakeLogRepo{}, true, true, 2, src, 500, 40)

	e.PostTurn(testAgent(), testSession(&userID)) // 第 1 轮: 未达阈值
	e.PostTurn(testAgent(), testSession(&userID)) // 第 2 轮: 触发
	if !modelSvc.waitForCalls(1) {
		t.Fatalf("extract not triggered")
	}
	if got := modelSvc.lastTemplateName(); got != "" {
		t.Fatalf("template = %q, want empty (agent current model)", got)
	}

	// 平台设置写入模板名: 下一轮抽取按新模板调用 (无需重启)
	src.Set("extract-gpt")
	e.PostTurn(testAgent(), testSession(&userID))
	e.PostTurn(testAgent(), testSession(&userID))
	if !modelSvc.waitForCalls(2) {
		t.Fatalf("second extract not triggered")
	}
	if got := modelSvc.lastTemplateName(); got != "extract-gpt" {
		t.Fatalf("template = %q, want extract-gpt after runtime switch", got)
	}
}
