package service

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"agent-platform/internal/model"
	"agent-platform/internal/repository"
)

// fakePlatformRepo 平台设置仓储假实现
type fakePlatformRepo struct {
	settings *model.PlatformSettings
	updated  int
}

func (f *fakePlatformRepo) Get(ctx context.Context) (*model.PlatformSettings, error) {
	if f.settings == nil {
		f.settings = &model.PlatformSettings{ID: "1", Name: model.DefaultPlatformName}
	}
	return f.settings, nil
}

func (f *fakePlatformRepo) Update(ctx context.Context, s *model.PlatformSettings) error {
	f.updated++
	f.settings = s
	return nil
}

// fakePlatformAudit 审计日志仓储假实现
type fakePlatformAudit struct {
	entries []*model.AuditLog
}

func (f *fakePlatformAudit) Append(ctx context.Context, entry *model.AuditLog) error {
	f.entries = append(f.entries, entry)
	return nil
}

func (f *fakePlatformAudit) List(ctx context.Context, action string, page, size int) ([]model.AuditLog, int64, error) {
	var out []model.AuditLog
	for _, e := range f.entries {
		if action == "" || e.Action == action {
			out = append(out, *e)
		}
	}
	return out, int64(len(out)), nil
}

var _ repository.AuditLogRepository = (*fakePlatformAudit)(nil)

func TestPlatformServiceGetDefaults(t *testing.T) {
	src := NewMutableTemplateSource("env-embed")
	extractSrc := NewMutableTemplateSource("env-extract")
	svc := NewPlatformService(&fakePlatformRepo{}, &fakePlatformAudit{}, PlatformModelSources{Embed: src, EmbedSink: src, Extract: extractSrc, ExtractSink: extractSrc})
	info, err := svc.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if info.Name != model.DefaultPlatformName {
		t.Fatalf("default name = %q, want %q", info.Name, model.DefaultPlatformName)
	}
	if info.Icon != "" {
		t.Fatalf("default icon = %q, want empty", info.Icon)
	}
	// 平台设置未配置向量模型: 回显空, 生效值回退 env
	if info.MemoryEmbedModel != "" {
		t.Fatalf("memory_embed_model = %q, want empty", info.MemoryEmbedModel)
	}
	if info.MemoryEmbedModelEffective != "env-embed" {
		t.Fatalf("effective = %q, want env-embed fallback", info.MemoryEmbedModelEffective)
	}
	// 平台设置未配置抽取模型: 回显空, 生效值回退 env
	if info.MemoryExtractModel != "" {
		t.Fatalf("memory_extract_model = %q, want empty", info.MemoryExtractModel)
	}
	if info.MemoryExtractModelEffective != "env-extract" {
		t.Fatalf("extract effective = %q, want env-extract fallback", info.MemoryExtractModelEffective)
	}
}

func TestPlatformServiceUpdateName(t *testing.T) {
	repo := &fakePlatformRepo{}
	audit := &fakePlatformAudit{}
	svc := NewPlatformService(repo, audit, PlatformModelSources{})

	userID := "user-1"
	info, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "  智能体平台 "}, &userID, "alice", "127.0.0.1")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if info.Name != "智能体平台" {
		t.Fatalf("name not trimmed/stored: %q", info.Name)
	}
	if repo.settings.UpdatedBy == nil || *repo.settings.UpdatedBy != userID {
		t.Fatalf("updated_by not set: %+v", repo.settings.UpdatedBy)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(audit.entries))
	}
	entry := audit.entries[0]
	if entry.Action != "platform.update" || entry.Resource != "platform" || entry.Username != "alice" {
		t.Fatalf("audit entry wrong: %+v", entry)
	}
	if !strings.Contains(string(entry.Detail), `"name_before":"Agent 管理平台"`) {
		t.Fatalf("audit detail missing name_before: %s", entry.Detail)
	}
}

func TestPlatformServiceUpdateInvalidName(t *testing.T) {
	svc := NewPlatformService(&fakePlatformRepo{}, &fakePlatformAudit{}, PlatformModelSources{})

	if _, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "   "}, nil, "alice", ""); err == nil {
		t.Fatal("empty name: want validation error")
	}
	longName := strings.Repeat("名", PlatformNameMaxLen+1)
	if _, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: longName}, nil, "alice", ""); err == nil {
		t.Fatal("overlong name: want validation error")
	}
}

func TestPlatformServiceUpdateIcon(t *testing.T) {
	// 1x1 透明 PNG
	png := base64.StdEncoding.EncodeToString([]byte{
		0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A,
	})
	validIcon := "data:image/png;base64," + png

	repo := &fakePlatformRepo{}
	svc := NewPlatformService(repo, &fakePlatformAudit{}, PlatformModelSources{})

	info, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", Icon: &validIcon}, nil, "alice", "")
	if err != nil {
		t.Fatalf("Update with valid icon: %v", err)
	}
	if info.Icon != validIcon {
		t.Fatalf("icon not stored: %q", info.Icon)
	}

	// 清除图标 (显式空串)
	emptyIcon := ""
	info, err = svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", Icon: &emptyIcon}, nil, "alice", "")
	if err != nil {
		t.Fatalf("Update clearing icon: %v", err)
	}
	if info.Icon != "" {
		t.Fatalf("icon not cleared: %q", info.Icon)
	}

	// 不传 icon (nil) 保持原值
	kept, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台"}, nil, "alice", "")
	if err != nil {
		t.Fatalf("Update without icon: %v", err)
	}
	if kept.Icon != "" {
		t.Fatalf("icon unexpectedly changed: %q", kept.Icon)
	}
}

func TestPlatformServiceUpdateIconRejected(t *testing.T) {
	svc := NewPlatformService(&fakePlatformRepo{}, &fakePlatformAudit{}, PlatformModelSources{})

	cases := map[string]string{
		"非 data URL": "https://example.com/logo.png",
		"不支持的类型":     "data:image/bmp;base64," + base64.StdEncoding.EncodeToString([]byte("x")),
		"非法 base64":  "data:image/png;base64,!!!not-base64!!!",
		"空文件":        "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte{}),
		"超大小上限":      "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, PlatformIconMaxSize+1)),
	}
	for name, icon := range cases {
		ic := icon
		if _, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", Icon: &ic}, nil, "alice", ""); err == nil {
			t.Fatalf("%s: want validation error", name)
		}
	}
}

// TestPlatformServiceUpdateEmbedModel 向量模型: 设置即时生效 / 清空回退 env / nil 不修改 + 审计
func TestPlatformServiceUpdateEmbedModel(t *testing.T) {
	repo := &fakePlatformRepo{}
	audit := &fakePlatformAudit{}
	src := NewMutableTemplateSource("env-embed")
	svc := NewPlatformService(repo, audit, PlatformModelSources{Embed: src, EmbedSink: src})

	userID := "user-1"
	v := "text-embed-3"
	info, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", MemoryEmbedModel: &v}, &userID, "alice", "")
	if err != nil {
		t.Fatalf("Update embed model: %v", err)
	}
	if info.MemoryEmbedModel != "text-embed-3" {
		t.Fatalf("memory_embed_model = %q, want text-embed-3", info.MemoryEmbedModel)
	}
	if info.MemoryEmbedModelEffective != "text-embed-3" {
		t.Fatalf("effective = %q, want text-embed-3", info.MemoryEmbedModelEffective)
	}
	if src.Current() != "text-embed-3" {
		t.Fatalf("runtime source not synced: %q", src.Current())
	}

	// 显式空串: 回退 env
	e := ""
	info, err = svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", MemoryEmbedModel: &e}, &userID, "alice", "")
	if err != nil {
		t.Fatalf("Update clearing embed model: %v", err)
	}
	if info.MemoryEmbedModel != "" || info.MemoryEmbedModelEffective != "env-embed" {
		t.Fatalf("after clear: stored=%q effective=%q, want empty/env-embed", info.MemoryEmbedModel, info.MemoryEmbedModelEffective)
	}
	if src.Current() != "env-embed" {
		t.Fatalf("runtime source after clear = %q, want env-embed", src.Current())
	}

	// nil = 不修改
	info, err = svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台"}, &userID, "alice", "")
	if err != nil {
		t.Fatalf("Update without embed model: %v", err)
	}
	if info.MemoryEmbedModel != "" {
		t.Fatalf("embed model unexpectedly changed: %q", info.MemoryEmbedModel)
	}

	// 审计含前后值
	found := false
	for _, entry := range audit.entries {
		if strings.Contains(string(entry.Detail), `"embed_model_after":"text-embed-3"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit detail missing embed_model_after: %+v", audit.entries)
	}
}

func TestPlatformServiceUpdateEmbedModelRejected(t *testing.T) {
	svc := NewPlatformService(&fakePlatformRepo{}, &fakePlatformAudit{}, PlatformModelSources{})
	long := strings.Repeat("x", PlatformEmbedModelMaxLen+1)
	if _, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", MemoryEmbedModel: &long}, nil, "alice", ""); err == nil {
		t.Fatal("overlong embed model: want validation error")
	}
}

// TestPlatformServiceUpdateExtractModel 抽取模型: 设置即时生效 / 清空回退 env / nil 不修改 + 审计
func TestPlatformServiceUpdateExtractModel(t *testing.T) {
	repo := &fakePlatformRepo{}
	audit := &fakePlatformAudit{}
	embedSrc := NewMutableTemplateSource("env-embed")
	extractSrc := NewMutableTemplateSource("env-extract")
	svc := NewPlatformService(repo, audit, PlatformModelSources{Embed: embedSrc, EmbedSink: embedSrc, Extract: extractSrc, ExtractSink: extractSrc})

	userID := "user-1"
	v := "extract-gpt"
	info, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", MemoryExtractModel: &v}, &userID, "alice", "")
	if err != nil {
		t.Fatalf("Update extract model: %v", err)
	}
	if info.MemoryExtractModel != "extract-gpt" {
		t.Fatalf("memory_extract_model = %q, want extract-gpt", info.MemoryExtractModel)
	}
	if info.MemoryExtractModelEffective != "extract-gpt" {
		t.Fatalf("extract effective = %q, want extract-gpt", info.MemoryExtractModelEffective)
	}
	if extractSrc.Current() != "extract-gpt" {
		t.Fatalf("extract source not synced: %q", extractSrc.Current())
	}
	// 向量模型不受影响
	if embedSrc.Current() != "env-embed" || info.MemoryEmbedModel != "" {
		t.Fatalf("embed unexpectedly changed: src=%q stored=%q", embedSrc.Current(), info.MemoryEmbedModel)
	}

	// 显式空串: 回退 env
	e := ""
	info, err = svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", MemoryExtractModel: &e}, &userID, "alice", "")
	if err != nil {
		t.Fatalf("Update clearing extract model: %v", err)
	}
	if info.MemoryExtractModel != "" || info.MemoryExtractModelEffective != "env-extract" {
		t.Fatalf("after clear: stored=%q effective=%q, want empty/env-extract", info.MemoryExtractModel, info.MemoryExtractModelEffective)
	}
	if extractSrc.Current() != "env-extract" {
		t.Fatalf("extract source after clear = %q, want env-extract", extractSrc.Current())
	}

	// 审计含前后值
	found := false
	for _, entry := range audit.entries {
		if strings.Contains(string(entry.Detail), `"extract_model_after":"extract-gpt"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit detail missing extract_model_after: %+v", audit.entries)
	}
}

func TestPlatformServiceUpdateExtractModelRejected(t *testing.T) {
	svc := NewPlatformService(&fakePlatformRepo{}, &fakePlatformAudit{}, PlatformModelSources{})
	long := strings.Repeat("x", PlatformEmbedModelMaxLen+1)
	if _, err := svc.Update(context.Background(), UpdatePlatformRequest{Name: "Agent 管理平台", MemoryExtractModel: &long}, nil, "alice", ""); err == nil {
		t.Fatal("overlong extract model: want validation error")
	}
}

// TestPlatformServiceSyncModelSettings 启动同步: 库中已保存的向量/抽取模型名推入运行时来源
func TestPlatformServiceSyncModelSettings(t *testing.T) {
	repo := &fakePlatformRepo{settings: &model.PlatformSettings{ID: "1", Name: model.DefaultPlatformName, MemoryEmbedModel: "db-embed", MemoryExtractModel: "db-extract"}}
	embedSrc := NewMutableTemplateSource("env-embed")
	extractSrc := NewMutableTemplateSource("env-extract")
	svc := NewPlatformService(repo, &fakePlatformAudit{}, PlatformModelSources{Embed: embedSrc, EmbedSink: embedSrc, Extract: extractSrc, ExtractSink: extractSrc})

	if embedSrc.Current() != "env-embed" || extractSrc.Current() != "env-extract" {
		t.Fatalf("pre-sync Current = %q/%q, want env-embed/env-extract", embedSrc.Current(), extractSrc.Current())
	}
	if err := svc.SyncModelSettings(context.Background()); err != nil {
		t.Fatalf("SyncModelSettings: %v", err)
	}
	if embedSrc.Current() != "db-embed" {
		t.Fatalf("after sync embed Current = %q, want db-embed", embedSrc.Current())
	}
	if extractSrc.Current() != "db-extract" {
		t.Fatalf("after sync extract Current = %q, want db-extract", extractSrc.Current())
	}
}
