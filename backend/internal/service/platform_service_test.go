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
	svc := NewPlatformService(&fakePlatformRepo{}, &fakePlatformAudit{})
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
}

func TestPlatformServiceUpdateName(t *testing.T) {
	repo := &fakePlatformRepo{}
	audit := &fakePlatformAudit{}
	svc := NewPlatformService(repo, audit)

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
	svc := NewPlatformService(&fakePlatformRepo{}, &fakePlatformAudit{})

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
	svc := NewPlatformService(repo, &fakePlatformAudit{})

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
	svc := NewPlatformService(&fakePlatformRepo{}, &fakePlatformAudit{})

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
