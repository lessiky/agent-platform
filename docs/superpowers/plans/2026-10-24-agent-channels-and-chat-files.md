# Agent Channels and Chat Files Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a production-ready first version where administrators can configure one Feishu app, users can chat with approved Agents through Feishu and files in both web/API conversations, Agents can list/read/create/send files, and expired or orphaned files are cleaned safely.

**Architecture:** Add a shared PostgreSQL metadata layer plus a local persistent `FileStorage` abstraction; web, Feishu, tools, and external API all use `FileService`. Add a Feishu connection manager that uses the official SDK WebSocket client with PostgreSQL leases, persists events/deliveries, serializes work per conversation, reuses `ChatService`, and retries delivery through a durable worker.

**Tech Stack:** Go 1.25, Gin, GORM/PostgreSQL, AES-256-GCM, `github.com/larksuite/oapi-sdk-go/v3 v3.12.2`, React 18, TypeScript, Vite, Ant Design, Nginx, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-10-24-agent-channels-and-chat-files-design.md`

## Global Constraints

- Single file limit is exactly `200MB` (`209715200` bytes); one message accepts at most `5` files totaling at most `1GB`; one session is limited to `10GB`; global storage is limited to `200GB`.
- Normal file retention is exactly `30 days`; unbound staging retention is exactly `24 hours`; `create_file` is limited to `2MB`.
- `read_file` defaults to `64KB`, accepts an offset, and never reads more than `1MB` in one call.
- `chat_files` payloads and uploads use streaming I/O; a `200MB` object must never be fully loaded into memory.
- Environment defaults are copied exactly: `CHAT_FILE_DIR=/app/data/chat-files`, `CHAT_FILE_MAX_SIZE=200MB`, `CHAT_FILE_MAX_PER_MESSAGE=5`, `CHAT_FILE_MAX_MESSAGE_BYTES=1GB`, `CHAT_FILE_MAX_SESSION_BYTES=10GB`, `CHAT_FILE_MAX_TOTAL_BYTES=200GB`, `CHAT_FILE_RETENTION=720h`, `CHAT_FILE_STAGING_RETENTION=24h`, `CHAT_FILE_CLEANUP_INTERVAL=1h`, `CHANNEL_EVENT_RETENTION=168h`, `CHANNEL_LEASE_TTL=45s`, `CHANNEL_RECONNECT_MAX=1m`.
- Only one enabled Feishu self-built app is supported. Use WebSocket long connection mode, never HTTP callbacks.
- Channel lease renewal interval is exactly `15s`, lease TTL is exactly `45s`, and reconnect backoff must never exceed `1m`.
- Delivery retry delays are exactly `1s, 5s, 30s, 2m`, with at most `5` total attempts.
- Feishu groups share one session and require `@机器人`; private chats are isolated by `identity + agent`; group messages route through an enabled group binding.
- Feishu inbound bodies must be downloaded with standard-library HTTP streaming after obtaining a tenant access token. Keep the original Feishu error when the upstream `100MB` download limit or `30MB` file/`10MB` image upload limit is exceeded.
- File tools do not use MCP human approval. `create_file` and `send_file` are enabled only by Agent configuration and remain subject to all file limits.
- Every new table uses a UUID primary key plus `created_at` and `updated_at` audit fields, except join tables whose timestamp requirements are defined by the spec.
- Nginx request-body limit is exactly `1100MB`; read/write timeouts are exactly `1800s`; proxy streaming must remain enabled.
- Secrets, storage paths, full event payloads, and file bodies must never be returned through ordinary APIs or written to application logs.
- Tests use Go's standard `testing` package and service fakes, matching `chat_service_test.go` and `platform_service_test.go`; frontend verification uses `npm run build`.

## File Structure

### Backend Domain and Storage

- Create `backend/internal/model/file.go`: `ChatFile`, `ChatMessageFile`, status/source constants, and public file views.
- Create `backend/internal/model/channel.go`: channel connections, leases, identities, bindings, preferences, conversations, events, deliveries, bind codes, and status constants.
- Modify `backend/internal/model/chat.go`: add session source/channel mapping and transient message file views.
- Create `backend/internal/repository/file_repository.go`: file metadata, quota sums, message-file aggregation, atomic cleanup claims, and usage queries.
- Create `backend/internal/repository/channel_repository.go`: channel configuration, lease acquisition/renewal, identities, bindings, preferences, event deduplication, and delivery claiming.
- Modify `backend/internal/repository/chat_repository.go`: transactional turn append with `chat_message_files`; delete message references with sessions.
- Create `backend/internal/service/file_storage.go`: `FileStorage` and local persistent-volume implementation.
- Create `backend/internal/service/file_service.go`: streaming upload, quotas, metadata, content access, deletion, and retention refresh.
- Create `backend/internal/service/file_tools.go`: built-in file tool definitions, execution, turn state, and tool result formatting.
- Create `backend/internal/service/file_cleanup.go`: hourly cleanup, orphan quarantine, retry state, and cleanup summaries.
- Create `backend/internal/service/file_delivery.go`: `TurnFileNotifier` abstraction shared by web and channel output.

### Backend Channels

- Create `backend/internal/service/channel_admin_service.go`: encrypted connection config, identity mapping, Agent bindings, group bindings, and bind codes.
- Create `backend/internal/service/channel_runtime.go`: runtime lifecycle, reconnect loop, SDK client construction, lease renewal, and status updates.
- Create `backend/internal/service/channel_event_service.go`: event persistence, deduplication, per-conversation serialization, routing, commands, and Agent invocation.
- Create `backend/internal/service/feishu_event_source.go`: SDK event conversion and WebSocket callback registration.
- Create `backend/internal/service/feishu_resource.go`: tenant token fetch and streaming download of Feishu message resources.
- Create `backend/internal/service/feishu_sender.go`: text/card/file send implementation through the Feishu SDK.
- Create `backend/internal/service/channel_delivery_service.go`: durable text/card/file delivery queue, retries, and status transitions.

### HTTP and Assembly

- Create `backend/internal/api/file/handler.go`: authenticated file upload/list/get/content/delete/storage routes.
- Create `backend/internal/api/channel/handler.go`: platform channel and Agent binding routes.
- Modify `backend/internal/api/agent/handler.go`: external API file upload/delivery registrations and `file_ids` request binding.
- Modify `backend/internal/service/agent_service.go`: API-key file operations, channel delivery authorization, and `file_ids` forwarding.
- Modify `backend/internal/service/chat_service.go`: scoped chat principal, file context, file tools, transactional file binding, and result/history hydration.
- Modify `backend/internal/config/config.go`: byte-size parsing and file/channel environment configuration.
- Modify `backend/internal/database/database.go`: migrations, partial unique indexes, and cleanup indexes.
- Modify `backend/cmd/server/main.go`: construct services, start cleanup/runtime/delivery workers, register handlers, and stop workers on shutdown.
- Modify `backend/go.mod` and `backend/go.sum`: add Feishu SDK `v3.12.2`.

### Frontend and Deployment

- Create `frontend/src/api/file.ts`: upload/list/content/delete/storage API calls.
- Create `frontend/src/api/channel.ts`: platform channel, identity, Agent binding, group, bind-code, and delivery API calls.
- Create `frontend/src/pages/agent/ChatAttachments.tsx`: upload list, file cards, preview/download, and generated-file rendering.
- Create `frontend/src/pages/agent/ChannelPanel.tsx`: Agent channel enablement, private authorization, group binding, and bind-code UI.
- Create `frontend/src/pages/system/ChannelManagementPanel.tsx`: Feishu config, test/status, identities, storage usage, and manual cleanup UI.
- Modify `frontend/src/pages/agent/ChatPanel.tsx`: upload button/drag-drop, send-time `file_ids`, progress, removal, and history attachments.
- Modify `frontend/src/pages/agent/AgentDetailPage.tsx`: add the channel tab.
- Modify `frontend/src/pages/system/PlatformSettingsPage.tsx`: add the channel-management tab.
- Modify `frontend/src/api/agent.ts` and `frontend/src/types/index.ts`: scoped chat/file/channel types and streaming request fields.
- Modify `frontend/nginx.conf`, `infra/docker-compose.yml`, `README.md`, `docs/api/api.md`, and `docs/api/external-api.md`: limits, volume, routes, and operational instructions.

---

### Task 1: File Models, Configuration, and Local Storage

**Files:**
- Create: `backend/internal/model/file.go`
- Create: `backend/internal/service/file_storage.go`
- Create: `backend/internal/service/file_storage_test.go`
- Create: `backend/internal/config/config_test.go`
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/database/database.go`
- Modify: `backend/internal/model/chat.go`

**Interfaces:**
- Consumes: existing PostgreSQL/GORM setup and `CHAT_FILE_*` naming from the spec.
- Produces: `model.ChatFile`, `model.ChatMessageFile`, `model.ChatFileView`, `service.FileStorage`, `service.LocalFileStorage`, `config.ByteSize`, `config.ChatFileConfig`, and `config.ChannelConfig`.

- [ ] **Step 1: Write failing model/storage/config tests**

```go
package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalFileStorageStreamsAndRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalFileStorage(root)
	if err != nil {
		t.Fatalf("NewLocalFileStorage: %v", err)
	}
	key := "11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222"
	payload := bytes.Repeat([]byte("abcdef"), 1024)
	n, err := store.Save(context.Background(), key, bytes.NewReader(payload))
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("Save = %d, %v; want %d, nil", n, err, len(payload))
	}
	rc, size, err := store.Open(context.Background(), key)
	if err != nil || size != int64(len(payload)) {
		t.Fatalf("Open = %d, %v; want %d, nil", size, err, len(payload))
	}
	defer rc.Close()
	got := make([]byte, len(payload))
	if _, err := rc.Read(got); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("stored bytes differ")
	}
	if _, err := store.Save(context.Background(), "../escape", strings.NewReader("x")); err == nil {
		t.Fatal("path traversal must be rejected")
	}
	if _, err := os.Stat(filepath.Join(root, "..")); err != nil {
		t.Fatalf("root parent should remain untouched: %v", err)
	}
}
```

```go
package config

import "testing"

func TestByteSizeAndDefaults(t *testing.T) {
	cases := map[string]int64{"200MB": 200 << 20, "1GB": 1 << 30}
	for raw, want := range cases {
		got, err := ParseByteSize(raw)
		if err != nil || got != want {
			t.Fatalf("ParseByteSize(%q) = %d, %v; want %d, nil", raw, got, err, want)
		}
	}
	if got, err := ParseByteSize("200MB"); err != nil || got != 200<<20 {
		t.Fatal("default single-file limit must be 200MB")
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:
```powershell
go test ./internal/service -run TestLocalFileStorageStreamsAndRejectsTraversal -v
go test ./internal/config -run TestByteSizeAndDefaults -v
```

Expected: FAIL because `NewLocalFileStorage` and `ParseByteSize` do not exist.

- [ ] **Step 3: Add file models and configuration**

```go
package model

import "time"

const (
	ChatFileStatusStaging  = "staging"
	ChatFileStatusReady    = "ready"
	ChatFileStatusDeleting = "deleting"
	ChatFileStatusDeleted  = "deleted"
	ChatFileStatusFailed   = "failed"

	ChatFileSourceWebUpload     = "web_upload"
	ChatFileSourceChannelInbound = "channel_inbound"
	ChatFileSourceToolCreated   = "tool_created"
	ChatFileSourceMCPArtifact   = "mcp_artifact"
	ChatFileSourceAPIUpload     = "api_upload"

	ChatMessageFileUserUpload        = "user_upload"
	ChatMessageFileAssistantGenerated = "assistant_generated"
)

type ChatFile struct {
	ID                 string     `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	AgentID            string     `gorm:"type:uuid;not null;index:idx_chat_files_agent_session_status,priority:1" json:"agent_id"`
	SessionID          *string    `gorm:"type:uuid;index:idx_chat_files_agent_session_status,priority:2" json:"session_id"`
	OwnerUserID        *string    `gorm:"type:uuid;index" json:"-"`
	OwnerIdentityID    *string    `gorm:"type:uuid;index" json:"-"`
	OwnerAPIKeyID      *string    `gorm:"type:uuid;index" json:"-"`
	Source             string     `gorm:"type:varchar(32);not null;index" json:"source"`
	OriginalName       string     `gorm:"type:varchar(512);not null" json:"original_name"`
	StorageKey         string     `gorm:"type:varchar(512);not null;uniqueIndex" json:"-"`
	MIMEType           string     `gorm:"type:varchar(128);not null;default:'application/octet-stream'" json:"mime_type"`
	SizeBytes          int64      `gorm:"not null" json:"size_bytes"`
	SHA256             string     `gorm:"type:varchar(64);not null;index" json:"sha256"`
	Status             string     `gorm:"type:varchar(16);not null;index:idx_chat_files_status_retention,priority:1" json:"status"`
	RetentionExpiresAt time.Time  `gorm:"not null;index:idx_chat_files_status_retention,priority:2" json:"retention_expires_at"`
	LastAccessedAt     *time.Time `json:"last_accessed_at"`
	AccessCount        int        `gorm:"not null;default:0" json:"access_count"`
	LastError          string     `gorm:"type:text" json:"-"`
	DeletedAt          *time.Time `json:"deleted_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func (ChatFile) TableName() string { return "chat_files" }

type ChatMessageFile struct {
	MessageID string    `gorm:"type:uuid;primaryKey" json:"message_id"`
	FileID    string    `gorm:"type:uuid;primaryKey" json:"file_id"`
	Relation  string    `gorm:"type:varchar(24);not null" json:"relation"`
	CreatedAt time.Time `json:"created_at"`
}

func (ChatMessageFile) TableName() string { return "chat_message_files" }

type ChatFileView struct {
	ID                 string     `json:"id"`
	AgentID            string     `json:"agent_id"`
	SessionID          *string    `json:"session_id"`
	Source             string     `json:"source"`
	Name               string     `json:"name"`
	MIMEType           string     `json:"mime_type"`
	SizeBytes          int64      `json:"size_bytes"`
	Status             string     `json:"status"`
	RetentionExpiresAt time.Time  `json:"retention_expires_at"`
	CreatedAt          time.Time  `json:"created_at"`
	URL                string     `json:"url"`
}
```

```go
type ByteSize int64

func ParseByteSize(raw string) (int64, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "GB"):
		mult, s = 1<<30, strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		mult, s = 1<<20, strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		mult, s = 1<<10, strings.TrimSuffix(s, "KB")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid byte size %q", raw)
	}
	return n * mult, nil
}

func (b *ByteSize) UnmarshalText(text []byte) error {
	n, err := ParseByteSize(string(text))
	if err != nil {
		return err
	}
	*b = ByteSize(n)
	return nil
}

type ChatFileConfig struct {
	Dir                string        `env:"CHAT_FILE_DIR" envDefault:"/app/data/chat-files"`
	MaxSize            ByteSize      `env:"CHAT_FILE_MAX_SIZE" envDefault:"200MB"`
	MaxPerMessage      int           `env:"CHAT_FILE_MAX_PER_MESSAGE" envDefault:"5"`
	MaxMessageBytes    ByteSize      `env:"CHAT_FILE_MAX_MESSAGE_BYTES" envDefault:"1GB"`
	MaxSessionBytes    ByteSize      `env:"CHAT_FILE_MAX_SESSION_BYTES" envDefault:"10GB"`
	MaxTotalBytes      ByteSize      `env:"CHAT_FILE_MAX_TOTAL_BYTES" envDefault:"200GB"`
	Retention          time.Duration `env:"CHAT_FILE_RETENTION" envDefault:"720h"`
	StagingRetention   time.Duration `env:"CHAT_FILE_STAGING_RETENTION" envDefault:"24h"`
	CleanupInterval    time.Duration `env:"CHAT_FILE_CLEANUP_INTERVAL" envDefault:"1h"`
	MaxCreatedFileSize ByteSize      `env:"CHAT_FILE_MAX_CREATED_SIZE" envDefault:"2MB"`
	ReadDefaultBytes   int64         `env:"CHAT_FILE_READ_DEFAULT_BYTES" envDefault:"65536"`
	ReadMaxBytes       int64         `env:"CHAT_FILE_READ_MAX_BYTES" envDefault:"1048576"`
}

type ChannelConfig struct {
	EventRetention time.Duration `env:"CHANNEL_EVENT_RETENTION" envDefault:"168h"`
	LeaseTTL       time.Duration `env:"CHANNEL_LEASE_TTL" envDefault:"45s"`
	ReconnectMax   time.Duration `env:"CHANNEL_RECONNECT_MAX" envDefault:"1m"`
}
```

Add `ChatFile ChatFileConfig` and `Channel ChannelConfig` to `config.Config`, add both models to `database.AutoMigrate`, and add `Source string` plus `ChannelConversationID *string` to `model.ChatSession`.

- [ ] **Step 4: Implement local storage and rerun tests**

```go
package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type FileStorage interface {
	Save(ctx context.Context, key string, r io.Reader) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (int64, error)
	Walk(ctx context.Context, fn func(key string, size int64) error) error
	Quarantine(ctx context.Context, key string) error
	RemoveQuarantine(ctx context.Context, key string) error
}

type LocalFileStorage struct {
	root       string
	quarantine string
}

func NewLocalFileStorage(root string) (*LocalFileStorage, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &LocalFileStorage{root: abs, quarantine: filepath.Join(abs, ".quarantine")}, nil
}

func (s *LocalFileStorage) path(key string) (string, error) {
	if key == "" || filepath.IsAbs(key) || strings.Contains(key, `\`) {
		return "", fmt.Errorf("invalid storage key")
	}
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid storage key")
	}
	full := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage key escapes root")
	}
	return full, nil
}
```

Implement `Save` with `os.CreateTemp` in the destination directory, `io.Copy`, `Sync`, `Close`, and atomic `Rename`; on context cancellation or copy error remove the temporary file. Implement `Open`, `Delete`, `Stat`, `Walk`, `Quarantine`, and `RemoveQuarantine` using the same checked path helper. Reject existing symlink components by walking each path component with `os.Lstat` and failing if `ModeSymlink` is set.

- [ ] **Step 5: Run focused and package tests**

Run:
```powershell
go test ./internal/config ./internal/service -run 'TestByteSizeAndDefaults|TestLocalFileStorageStreamsAndRejectsTraversal' -v
go test ./internal/model ./internal/config ./internal/service
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/model/file.go backend/internal/model/chat.go backend/internal/service/file_storage.go backend/internal/service/file_storage_test.go backend/internal/config/config.go backend/internal/config/config_test.go backend/internal/database/database.go
git commit -m "feat: add chat file models and local storage"
```

### Task 2: File Repository and Streaming File Service

**Files:**
- Create: `backend/internal/repository/file_repository.go`
- Create: `backend/internal/service/file_service.go`
- Create: `backend/internal/service/file_service_test.go`

**Interfaces:**
- Consumes: `model.ChatFile`, `model.ChatMessageFile`, `FileStorage`, `config.ChatFileConfig`.
- Produces: `repository.ChatFileRepository`, `service.FileScope`, `service.UploadFileRequest`, `service.FileListFilter`, `service.StorageUsage`, `service.FileService`.

```go
type FileScope struct {
	AgentID           string
	SessionID         string
	UserID            string
	ChannelIdentityID string
	APIKeyID          string
	Admin             bool
}

type UploadFileRequest struct {
	Scope       FileScope
	Source      string
	Name        string
	MIMEType    string
	SizeHint    int64
	Reader      io.Reader
}

type FileListFilter struct {
	SessionID string
	Source    string
	Status    string
	Page      int
	PageSize  int
}

type StorageUsage struct {
	TotalBytes       int64        `json:"total_bytes"`
	TotalLimit       int64        `json:"total_limit"`
	AgentBytes       int64        `json:"agent_bytes"`
	SessionBytes     []FileUsage  `json:"session_bytes"`
	PendingCleanup   int64        `json:"pending_cleanup"`
	LastCleanup      *CleanupRun  `json:"last_cleanup,omitempty"`
}

type CleanupRun struct {
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
	Scanned           int64     `json:"scanned"`
	Deleted           int64     `json:"deleted"`
	Failed            int64     `json:"failed"`
	ReleasedBytes     int64     `json:"released_bytes"`
	OrphansMoved      int64     `json:"orphans_moved"`
	OrphanBytes       int64     `json:"orphan_bytes"`
	EventsDeleted     int64     `json:"events_deleted"`
	DeliveriesDeleted int64     `json:"deliveries_deleted"`
	LastError         string    `json:"last_error,omitempty"`
}

type ReadTextResult struct {
	Content    string `json:"content"`
	Offset     int64  `json:"offset"`
	Bytes      int    `json:"bytes"`
	SizeBytes  int64  `json:"size_bytes"`
	NextOffset int64  `json:"next_offset,omitempty"`
}

type FileUsage struct {
	SessionID string `json:"session_id"`
	Bytes     int64  `json:"bytes"`
	Files     int64  `json:"files"`
}

type FileService interface {
	Upload(ctx context.Context, req UploadFileRequest) (*model.ChatFileView, error)
	List(ctx context.Context, scope FileScope, filter FileListFilter) ([]model.ChatFileView, int64, error)
	Get(ctx context.Context, scope FileScope, fileID string, allowStaging bool) (*model.ChatFileView, error)
	OpenContent(ctx context.Context, scope FileScope, fileID string) (io.ReadCloser, int64, *model.ChatFileView, error)
	Delete(ctx context.Context, scope FileScope, fileID string) error
	ReadText(ctx context.Context, scope FileScope, fileID string, offset, limit int64) (*ReadTextResult, error)
	CreateText(ctx context.Context, scope FileScope, name, content, mediaType string) (*model.ChatFileView, error)
	PrepareTurnFiles(ctx context.Context, scope FileScope, fileIDs []string) ([]model.ChatFileView, error)
	RefreshSessionRetention(ctx context.Context, scope FileScope) error
	Usage(ctx context.Context, scope FileScope) (*StorageUsage, error)
	GlobalUsage(ctx context.Context) (*StorageUsage, error)
	Cleanup(ctx context.Context, now time.Time) (*CleanupRun, error)
	Config() config.ChatFileConfig
}
```

- [ ] **Step 1: Write failing service tests for limits, sanitization, and binary reads**

```go
func TestFileServiceRejectsOversizeAndSanitizesName(t *testing.T) {
	store, _ := NewLocalFileStorage(t.TempDir())
	repo := newFakeChatFileRepository()
	svc := NewFileService(repo, store, ChatFileConfig{
		MaxSize: 4, MaxPerMessage: 5, MaxMessageBytes: 16,
		MaxSessionBytes: 16, MaxTotalBytes: 32, Retention: 30 * 24 * time.Hour,
		StagingRetention: 24 * time.Hour, ReadDefaultBytes: 64 << 10, ReadMaxBytes: 1 << 20,
	}, nil)
	view, err := svc.Upload(context.Background(), UploadFileRequest{
		Scope: FileScope{AgentID: "agent-1", UserID: "user-1"},
		Source: model.ChatFileSourceWebUpload, Name: "../a\x00/b.txt",
		Reader: bytes.NewBufferString("data"),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if view.Name != "a_b.txt" {
		t.Fatalf("name = %q, want a_b.txt", view.Name)
	}
	_, err = svc.Upload(context.Background(), UploadFileRequest{
		Scope: FileScope{AgentID: "agent-1", UserID: "user-1"},
		Source: model.ChatFileSourceWebUpload, Name: "big.bin",
		Reader: bytes.NewBufferString("12345"),
	})
	errApp, ok := err.(*errors.AppError)
	if !ok || errApp.Code != "file_too_large" {
		t.Fatalf("oversize error = %#v, want file_too_large", err)
	}
}

func TestFileServiceReadTextRangeAndBinaryGuard(t *testing.T) {
	store, _ := NewLocalFileStorage(t.TempDir())
	repo := newFakeChatFileRepository()
	svc := NewFileService(repo, store, testChatFileConfig(), nil)
	text, _ := svc.CreateText(context.Background(), FileScope{AgentID: "agent-1", UserID: "user-1"}, "note.txt", "abcdef", "text/plain")
	got, err := svc.ReadText(context.Background(), FileScope{AgentID: "agent-1", UserID: "user-1"}, text.ID, 2, 3)
	if err != nil || got.Content != "cde" || got.NextOffset != 5 {
		t.Fatalf("ReadText = %+v, %v; want cde/5", got, err)
	}
	binary, _ := svc.Upload(context.Background(), UploadFileRequest{
		Scope: FileScope{AgentID: "agent-1", UserID: "user-1"}, Source: model.ChatFileSourceWebUpload,
		Name: "x.bin", MIMEType: "application/octet-stream", Reader: bytes.NewReader([]byte{0, 1, 2, 3}),
	})
	_, err = svc.ReadText(context.Background(), FileScope{AgentID: "agent-1", UserID: "user-1"}, binary.ID, 0, 64)
	errApp, ok := err.(*errors.AppError)
	if !ok || errApp.Code != "file_binary_unsupported" {
		t.Fatalf("binary error = %#v, want file_binary_unsupported", err)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:
```powershell
go test ./internal/service -run 'TestFileServiceRejectsOversizeAndSanitizesName|TestFileServiceReadTextRangeAndBinaryGuard' -v
```

Expected: FAIL because `NewFileService`, `UploadFileRequest`, and the file service operations do not exist.

- [ ] **Step 3: Implement the repository and streaming service**

```go
type ChatFileRepository interface {
	Create(ctx context.Context, f *model.ChatFile) error
	Get(ctx context.Context, id string) (*model.ChatFile, error)
	List(ctx context.Context, filter FileListFilter) ([]model.ChatFile, int64, error)
	UpdateStatus(ctx context.Context, id, from, to, lastError string) (bool, error)
	Touch(ctx context.Context, id string, now time.Time) error
	TouchSession(ctx context.Context, sessionID string, now time.Time) error
	SumGlobal(ctx context.Context) (int64, error)
	SumAgent(ctx context.Context, agentID string) (int64, error)
	SumSession(ctx context.Context, sessionID string) (int64, error)
	ListSessionUsage(ctx context.Context, agentID string, limit int) ([]FileUsage, error)
	CountPendingCleanup(ctx context.Context, now time.Time) (int64, error)
	WithQuotaLock(ctx context.Context, agentID, sessionID string, fn func() error) error
}
```

Implement `Upload` with this exact sequence:

1. Trim and sanitize the name with a function that replaces `\`, `/`, NUL, and control characters with `_`, collapses whitespace, limits it to `512` bytes, and falls back to `file`.
2. Generate `fileID`, compute `storageKey = agentID + "/" + fileID`, and run quota checks inside `WithQuotaLock`.
3. Read through `bufio.Reader`; use `Peek(512)` only to detect MIME when `MIMEType` is empty.
4. Wrap the reader in `io.LimitReader(r, max+1)`, hash with `sha256.New`, and write to storage; if the byte count exceeds `max`, delete the object and return `AppError{Code:"file_too_large"}`.
5. Recheck global, Agent session, and message quota using the actual byte count.
6. Create the database row with `status=staging` and `retention_expires_at=now+StagingRetention`.
7. If metadata creation fails, delete the physical object before returning the error.

```go
func NewFileService(repo repository.ChatFileRepository, storage FileStorage, cfg config.ChatFileConfig, audit repository.AuditLogRepository) FileService {
	if cfg.ReadDefaultBytes <= 0 {
		cfg.ReadDefaultBytes = 64 << 10
	}
	if cfg.ReadMaxBytes <= 0 || cfg.ReadMaxBytes > 1<<20 {
		cfg.ReadMaxBytes = 1 << 20
	}
	return &fileService{repo: repo, storage: storage, cfg: cfg, audit: audit}
}

func (s *fileService) ReadText(ctx context.Context, scope FileScope, fileID string, offset, limit int64) (*ReadTextResult, error) {
	if limit <= 0 {
		limit = s.cfg.ReadDefaultBytes
	}
	if limit > s.cfg.ReadMaxBytes {
		limit = s.cfg.ReadMaxBytes
	}
	if offset < 0 {
		return nil, errors.NewValidationError("offset must be >= 0")
	}
	rc, size, view, err := s.OpenContent(ctx, scope, fileID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if !isReadableText(view.MIMEType, view.Name) {
		return nil, &errors.AppError{Code: "file_binary_unsupported", Message: "二进制文件不能按文本读取", HTTPCode: 400}
	}
	if offset > size {
		offset = size
	}
	if _, err := io.CopyN(io.Discard, rc, offset); err != nil && err != io.EOF {
		return nil, err
	}
	buf := make([]byte, limit)
	n, err := io.ReadFull(rc, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	content := string(buf[:n])
	if !utf8.ValidString(content) {
		return nil, &errors.AppError{Code: "file_binary_unsupported", Message: "文件不是有效 UTF-8 文本", HTTPCode: 400}
	}
	next := int64(0)
	if offset+int64(n) < size {
		next = offset + int64(n)
	}
	return &ReadTextResult{Content: content, Offset: offset, Bytes: n, SizeBytes: size, NextOffset: next}, nil
}
```

- [ ] **Step 4: Run the service tests and package tests**

Run:
```powershell
go test ./internal/service -run 'TestFileService' -v
go test ./internal/repository ./internal/service
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/repository/file_repository.go backend/internal/service/file_service.go backend/internal/service/file_service_test.go
git commit -m "feat: add streaming chat file service and quotas"
```

### Task 3: Transactional Message-File Binding and Chat Integration

**Files:**
- Modify: `backend/internal/repository/chat_repository.go`
- Modify: `backend/internal/service/chat_service.go`
- Modify: `backend/internal/service/chat_service_test.go`
- Modify: `backend/internal/model/chat.go`

**Interfaces:**
- Consumes: `FileService`, `FileScope`, `model.ChatFileView`, `ChatMessageRepository`.
- Produces: `repository.ChatTurnFileRef`, `ChatMessageRepository.AppendTurn`, scoped `ChatService.ChatFromChannel`, `ChatRequest.FileIDs`, `InvokeRequest.FileIDs`, `ChatResult.Files`, and `ChatResult.GeneratedFiles`.

```go
type ChatTurnFileRef struct {
	MessageIndex int
	FileID       string
	Relation     string
}

type ChatTurnAppendOptions struct {
	FileRefs        []ChatTurnFileRef
	FileSessionID   string
	FileRetention   time.Duration
}

AppendTurn(ctx context.Context, msgs []*model.ChatMessage, opts ChatTurnAppendOptions) error

type ChatPrincipal struct {
	UserID            string
	ChannelIdentityID string
	APIKeyID          string
	Admin             bool
}
```

- [ ] **Step 1: Write a failing transactional binding test**

```go
func TestPersistChatTurnBindsUserAndAssistantFilesInOneAppendTurn(t *testing.T) {
	msgRepo := &fakeAppendTurnRepo{}
	fileSvc := &fakeChatFileService{views: []model.ChatFileView{
		{ID: "33333333-3333-3333-3333-333333333333", Name: "in.txt"},
		{ID: "44444444-4444-4444-4444-444444444444", Name: "out.txt"},
	}}
	s := &chatService{messages: msgRepo, sessions: &fakeSessRepoForApproval{}, files: fileSvc}
	_, err := s.persistChatTurn(
		context.Background(),
		&model.ChatSession{ID: "session-1"},
		"exec-1",
		"read it",
		"done",
		nil, nil, nil, nil, time.Now(), "model", 1, "", nil, nil,
		[]string{"33333333-3333-3333-3333-333333333333"},
		[]string{"44444444-4444-4444-4444-444444444444"},
	)
	if err != nil {
		t.Fatalf("persistChatTurn: %v", err)
	}
	if msgRepo.appendTurnCalls != 1 || msgRepo.appendCalls != 0 {
		t.Fatalf("appendTurn=%d append=%d; want 1/0", msgRepo.appendTurnCalls, msgRepo.appendCalls)
	}
	if len(msgRepo.lastOpts.FileRefs) != 2 {
		t.Fatalf("file refs = %+v, want 2", msgRepo.lastOpts.FileRefs)
	}
	if msgRepo.lastOpts.FileRefs[0].MessageIndex != 0 ||
		msgRepo.lastOpts.FileRefs[0].Relation != model.ChatMessageFileUserUpload {
		t.Fatalf("user ref = %+v", msgRepo.lastOpts.FileRefs[0])
	}
	if msgRepo.lastOpts.FileRefs[1].MessageIndex != 2 ||
		msgRepo.lastOpts.FileRefs[1].Relation != model.ChatMessageFileAssistantGenerated {
		t.Fatalf("assistant ref = %+v", msgRepo.lastOpts.FileRefs[1])
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:
```powershell
go test ./internal/service -run TestPersistChatTurnBindsUserAndAssistantFilesInOneAppendTurn -v
```

Expected: FAIL because the existing repository has no `AppendTurn` and `persistChatTurn` accepts neither input nor output file IDs.

- [ ] **Step 3: Add transactional append and hydrate history files**

```go
func (r *chatMessageRepository) AppendTurn(ctx context.Context, msgs []*model.ChatMessage, opts ChatTurnAppendOptions) error {
	if len(msgs) == 0 {
		return nil
	}
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for i, m := range msgs {
			if m.CreatedAt.IsZero() {
				m.CreatedAt = now.Add(time.Duration(i) * time.Microsecond)
			}
		}
		if err := tx.Create(&msgs).Error; err != nil {
			return err
		}
		seen := make(map[string]bool, len(opts.FileRefs))
		refs := make([]model.ChatMessageFile, 0, len(opts.FileRefs))
		for _, ref := range opts.FileRefs {
			if ref.MessageIndex < 0 || ref.MessageIndex >= len(msgs) || ref.FileID == "" {
				return fmt.Errorf("invalid chat turn file reference")
			}
			refs = append(refs, model.ChatMessageFile{
				MessageID: msgs[ref.MessageIndex].ID,
				FileID:    ref.FileID,
				Relation:  ref.Relation,
			})
			seen[ref.FileID] = true
		}
		if len(refs) > 0 {
			if err := tx.Create(&refs).Error; err != nil {
				return err
			}
			ids := make([]string, 0, len(seen))
			for id := range seen {
				ids = append(ids, id)
			}
			res := tx.Model(&model.ChatFile{}).
				Where("id IN ? AND status IN ?", ids, []string{model.ChatFileStatusStaging, model.ChatFileStatusReady}).
				Updates(map[string]interface{}{
					"session_id":          opts.FileSessionID,
					"status":              model.ChatFileStatusReady,
					"retention_expires_at": gorm.Expr("NOW() + ?::interval", fmt.Sprintf("%f seconds", opts.FileRetention.Seconds())),
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != int64(len(ids)) {
				return errors.NewValidationError("file is not ready or does not belong to this session")
			}
		}
		return nil
	})
}
```

Modify `ListBySession` to collect message IDs, query `chat_message_files` joined with `chat_files`, and populate `ChatMessage.Files`. Set `URL` in `chatService.GetSession` using `strconv`-free string concatenation:

```go
func fileViewFromModel(f model.ChatFile, agentID string) model.ChatFileView {
	return model.ChatFileView{
		ID: f.ID, AgentID: f.AgentID, SessionID: f.SessionID, Source: f.Source,
		Name: f.OriginalName, MIMEType: f.MIMEType, SizeBytes: f.SizeBytes,
		Status: f.Status, RetentionExpiresAt: f.RetentionExpiresAt, CreatedAt: f.CreatedAt,
		URL: "/api/v1/agents/" + agentID + "/files/" + f.ID + "/content",
	}
}
```

- [ ] **Step 4: Wire scoped chat and turn file IDs**

Change request and result types exactly:

```go
type ChatRequest struct {
	SessionID    *string  `json:"session_id"`
	Message      string   `json:"message" binding:"required,max=8192"`
	FileIDs      []string `json:"file_ids,omitempty"`
	ShowThinking bool     `json:"show_thinking"`
}

type InvokeRequest struct {
	Message      string   `json:"message" binding:"required,max=8192"`
	SessionID    *string  `json:"session_id,omitempty"`
	FileIDs      []string `json:"file_ids,omitempty"`
	APIKeyID     string   `json:"-"`
}

type ChatResult struct {
	SessionID        string               `json:"session_id"`
	MessageID        string               `json:"message_id"`
	Reply            string               `json:"reply"`
	ExecutionID      string               `json:"execution_id"`
	Files            []model.ChatFileView `json:"files,omitempty"`
	GeneratedFiles   []model.ChatFileView `json:"generated_files,omitempty"`
	Model            string               `json:"model,omitempty"`
	ModelName        string               `json:"model_name,omitempty"`
	TotalTokens      int                  `json:"total_tokens"`
	LatencyMs        int64                `json:"latency_ms"`
	MCPCalls         []MCPChatCall        `json:"mcp_calls,omitempty"`
	SkillCalls       []SkillCall          `json:"skill_calls,omitempty"`
	PendingApprovals []runtime.PendingApproval `json:"pending_approvals,omitempty"`
}
```

Change `runTurn` to consume a `turnInput` struct instead of its current seven scalar arguments:

```go
type turnInput struct {
	Message        string
	FileIDs        []string
	Scope          FileScope
	Source         string
	ApprovalSource string
	ShowThinking   bool
}
```

Before appending the user message, call `s.files.PrepareTurnFiles`, append a system message containing only file metadata (`id`, sanitized name, MIME, bytes, and a `read_file` hint), and keep content out of that system message. Change `persistChatTurn` to accept `userFileIDs` and `assistantFileIDs` and call `AppendTurn` once. `Chat` uses `ChatPrincipal{UserID: operatorID}`; `Invoke` uses `ChatPrincipal{APIKeyID: req.APIKeyID}`. Add `ChatFromChannel(ctx, agentID, req, principal)` as a method that performs the same scoped execution path.

- [ ] **Step 5: Run focused and existing chat tests**

Run:
```powershell
go test ./internal/service -run 'TestPersistChatTurn|TestRunToolRounds|TestCompleteExecutions' -v
go test ./internal/repository ./internal/service
```

Expected: PASS, including pre-existing approval tests.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/model/chat.go backend/internal/repository/chat_repository.go backend/internal/service/chat_service.go backend/internal/service/chat_service_test.go
git commit -m "feat: bind files to chat turns transactionally"
```

### Task 4: Authenticated Web File API

**Files:**
- Create: `backend/internal/api/file/handler.go`
- Create: `backend/internal/api/file/handler_test.go`
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `FileService`, `FileScope`, JWT middleware, `agent:read`, `agent:write`.
- Produces: `file.Handler`, its `RegisterRoutes(*gin.RouterGroup)`, and all file endpoints defined in spec section 6.3.

```go
type Handler struct {
	files service.FileService
}

func NewHandler(files service.FileService) *Handler
func (h *Handler) RegisterRoutes(router *gin.RouterGroup)
```

- [ ] **Step 1: Write a failing handler test for scoped upload and streaming download**

```go
func TestFileHandlerUploadUsesFormSessionAndReturnsContentDisposition(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeFileHTTPReader{
		view: &model.ChatFileView{
			ID: "11111111-1111-1111-1111-111111111111", AgentID: "agent-1",
			Name: "报告.txt", MIMEType: "text/plain", SizeBytes: 5, Status: model.ChatFileStatusStaging,
		},
		content: "hello",
	}
	h := NewHandler(fake)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Next()
	})
	agents := r.Group("/api/v1/agents")
	agents.POST("/:id/files", h.Upload)
	agents.GET("/:id/files/:fileId/content", h.Content)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("session_id", "session-1")
	part, _ := mw.CreateFormFile("file", "报告.txt")
	_, _ = part.Write([]byte("hello"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-1/files", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body=%s", w.Code, w.Body.String())
	}
	if fake.lastUpload.Scope.UserID != "user-1" || fake.lastUpload.Scope.SessionID != "session-1" {
		t.Fatalf("upload scope = %+v", fake.lastUpload.Scope)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-1/files/11111111-1111-1111-1111-111111111111/content", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "hello" {
		t.Fatalf("download status/body = %d/%q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "filename*=UTF-8''") {
		t.Fatalf("Content-Disposition = %q", w.Header().Get("Content-Disposition"))
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:
```powershell
go test ./internal/api/file -run TestFileHandlerUploadUsesFormSessionAndReturnsContentDisposition -v
```

Expected: FAIL because `internal/api/file` does not exist.

- [ ] **Step 3: Implement the handler and register routes**

Register routes in this order so `storage` is not captured as a file ID:

```go
agents := router.Group("/agents")
agents.Use(middleware.Auth())
{
	agents.POST("/:id/files", middleware.AuthCheck("agent:write"), h.Upload)
	agents.GET("/:id/files", middleware.AuthCheck("agent:read"), h.List)
	agents.GET("/:id/files/storage", middleware.AuthCheck("agent:read"), h.Storage)
	agents.GET("/:id/files/:fileId", middleware.AuthCheck("agent:read"), h.Get)
	agents.GET("/:id/files/:fileId/content", middleware.AuthCheck("agent:read"), h.Content)
	agents.DELETE("/:id/files/:fileId", middleware.AuthCheck("agent:write"), h.Delete)
}
```

The upload handler must cap the request body and parse multipart to temporary disk rather than reading the file into memory:

```go
func (h *Handler) Upload(c *gin.Context) {
	maxRequest := int64(h.files.Config().MaxMessageBytes) + 16<<20
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequest)
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		response.BadRequest(c, "invalid multipart request: "+err.Error())
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file is required")
		return
	}
	f, err := header.Open()
	if err != nil {
		response.BadRequest(c, "cannot open uploaded file")
		return
	}
	defer f.Close()
	scope := h.scope(c, c.Param("id"), c.PostForm("session_id"))
	view, err := h.files.Upload(c.Request.Context(), service.UploadFileRequest{
		Scope: scope, Source: model.ChatFileSourceWebUpload,
		Name: filepath.Base(header.Filename), MIMEType: header.Header.Get("Content-Type"),
		SizeHint: header.Size, Reader: f,
	})
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Created(c, view)
}
```

Add `Config() config.ChatFileConfig` to `FileService` so the handler uses configured limits. Download with `io.Copy` and `Content-Length`; never call `io.ReadAll`. Set `Content-Disposition` with `mime.FormatMediaType("attachment", map[string]string{"filename": view.Name})`.

- [ ] **Step 4: Run handler and service tests**

Run:
```powershell
go test ./internal/api/file -v
go test ./internal/service -run TestFileService -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/api/file/handler.go backend/internal/api/file/handler_test.go backend/internal/service/file_service.go backend/cmd/server/main.go
git commit -m "feat: expose authenticated file APIs"
```

### Task 5: Agent File Tools and Output Coordination

**Files:**
- Create: `backend/internal/service/file_tools.go`
- Create: `backend/internal/service/file_tools_test.go`
- Create: `backend/internal/service/file_delivery.go`
- Modify: `backend/internal/service/chat_service.go`
- Modify: `backend/internal/service/agent_service.go`
- Modify: `backend/internal/api/agent/handler.go`
- Modify: `frontend/src/pages/agent/AgentFormPage.tsx`
- Modify: `frontend/src/types/index.ts`

**Interfaces:**
- Consumes: `FileService`, `model.ChatFileView`, Agent configuration, and chat tool round execution.
- Produces: `list_session_files`, `read_file`, `create_file`, `send_file`, `FileTurnState`, and `TurnFileNotifier`.

```go
const (
	ListSessionFilesToolName = "list_session_files"
	ReadFileToolName         = "read_file"
	CreateFileToolName       = "create_file"
	SendFileToolName         = "send_file"
)

type FileDelivery struct {
	FileID string
	Text   string
}

type TurnFileNotifier interface {
	NotifyTurnFiles(ctx context.Context, scope FileScope, assistantMessageID string, deliveries []FileDelivery) error
}
```

- [ ] **Step 1: Write failing built-in tool tests**

```go
func TestFileToolsListReadCreateAndQueueSend(t *testing.T) {
	files := &fakeChatFileService{
		views: []model.ChatFileView{{ID: "file-1", Name: "a.txt", MIMEType: "text/plain", SizeBytes: 3}},
		read:  &ReadTextResult{Content: "abc", SizeBytes: 3},
	}
	state := newFileTurnState(FileScope{AgentID: "agent-1", SessionID: "session-1", UserID: "user-1"}, nil)
	s := &chatService{files: files}

	list := s.executeFileTool(context.Background(), "chat", "exec-1", state, modelclient.ChatToolCall{
		Function: modelclient.ChatToolFunction{Name: ListSessionFilesToolName, Arguments: `{}`},
	})
	if !strings.Contains(list, `"id":"file-1"`) {
		t.Fatalf("list result = %s", list)
	}
	read := s.executeFileTool(context.Background(), "chat", "exec-1", state, modelclient.ChatToolCall{
		Function: modelclient.ChatToolFunction{Name: ReadFileToolName, Arguments: `{"file_id":"file-1","offset":0,"limit":3}`},
	})
	if !strings.Contains(read, `"content":"abc"`) {
		t.Fatalf("read result = %s", read)
	}
	created := s.executeFileTool(context.Background(), "chat", "exec-1", state, modelclient.ChatToolCall{
		Function: modelclient.ChatToolFunction{Name: CreateFileToolName, Arguments: `{"name":"out.txt","content":"ok"}`},
	})
	if !strings.Contains(created, `"file_id":"created-1"`) || len(state.generated) != 1 {
		t.Fatalf("create result=%s generated=%v", created, state.generated)
	}
	sent := s.executeFileTool(context.Background(), "chat", "exec-1", state, modelclient.ChatToolCall{
		Function: modelclient.ChatToolFunction{Name: SendFileToolName, Arguments: `{"file_id":"created-1","text":"结果"}`},
	})
	if !strings.Contains(sent, `"queued":true`) || len(state.deliveries) != 1 || state.deliveries[0].Text != "结果" {
		t.Fatalf("send result=%s deliveries=%v", sent, state.deliveries)
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run:
```powershell
go test ./internal/service -run TestFileToolsListReadCreateAndQueueSend -v
```

Expected: FAIL because `newFileTurnState` and `executeFileTool` do not exist.

- [ ] **Step 3: Add Agent file-tool configuration and definitions**

Add this field to `service.AgentConfig`, `CreateAgentRequest`, and `UpdateAgentRequest`:

```go
FileTools []string `json:"file_tools,omitempty"`
```

Define the accepted values as `list_session_files`, `read_file`, `create_file`, and `send_file`. Existing Agents with an empty list register `list_session_files` and `read_file`; `create_file` and `send_file` require an explicit entry.

Build OpenAI tool definitions with concrete schemas:

```go
func fileToolDefs(enabled []string) []modelclient.ChatToolDef {
	set := map[string]bool{}
	for _, name := range enabled {
		set[name] = true
	}
	// list_session_files and read_file are always available.
	out := []modelclient.ChatToolDef{
		funcDef(ListSessionFilesToolName, "列出当前会话可访问的文件", map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{},
		}),
		funcDef(ReadFileToolName, "按字节范围读取当前会话文件文本", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"file_id": map[string]interface{}{"type": "string"},
				"offset":  map[string]interface{}{"type": "integer", "minimum": 0},
				"limit":   map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 1048576},
			},
			"required": []string{"file_id"},
		}),
	}
	if set[CreateFileToolName] {
		out = append(out, funcDef(CreateFileToolName, "创建不超过 2MB 的文本文件并加入当前会话", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":       map[string]interface{}{"type": "string"},
				"content":    map[string]interface{}{"type": "string"},
				"media_type": map[string]interface{}{"type": "string"},
			},
			"required": []string{"name", "content"},
		}))
	}
	if set[SendFileToolName] {
		out = append(out, funcDef(SendFileToolName, "把当前会话文件发送到当前对话渠道", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"file_id": map[string]interface{}{"type": "string"},
				"text":    map[string]interface{}{"type": "string", "maxLength": 2000},
			},
			"required": []string{"file_id"},
		}))
	}
	return out
}
```

- [ ] **Step 4: Execute tools inside `runToolRounds` and notify after persistence**

Define turn state and dispatch before the MCP branch:

```go
type FileTurnState struct {
	Scope      FileScope
	Input      []model.ChatFileView
	Generated  []model.ChatFileView
	Deliveries []FileDelivery
}

func newFileTurnState(scope FileScope, input []model.ChatFileView) *FileTurnState {
	return &FileTurnState{Scope: scope, Input: input}
}
```

In `runToolRounds`, dispatch the four names before `executeToolCall`:

```go
if isFileTool(tc.Function.Name) {
	toolMsg = s.executeFileTool(ctx, source, executionID, fileTurn, tc)
} else if tc.Function.Name == loadSkillToolName && st.loadTool() {
	toolMsg = s.executeSkillLoad(agentID, source, executionID, toolIndex, tc, st)
} else if tc.Function.Name == searchKnowledgeToolName && kt.registerTool() {
	toolMsg = s.executeKnowledgeSearch(ctx, agentID, source, executionID, round, tc, kt)
} else {
	toolMsg, approvalPending = s.executeToolCall(ctx, agentID, sessionID, source, approvalSource, toolIndex, tc, pending, mcpCalls, executionID)
}
```

When persisting a successful turn, bind `state.Generated` file IDs to the assistant message and return them in `ChatResult.GeneratedFiles`. After `persistChatTurn` succeeds, call the optional notifier:

```go
if s.fileNotifier != nil && len(fileTurn.Deliveries) > 0 {
	if err := s.fileNotifier.NotifyTurnFiles(ctx, fileTurn.Scope, assistantID, fileTurn.Deliveries); err != nil {
		s.execLog(agentID, model.LogLevelWarn, fmt.Sprintf("%s file notification failed assistant=%s error=%s", source, assistantID, err))
	}
}
```

`ChatService.fileNotifier` is nil by default; Task 11 wires the `ChannelDeliveryService` implementation.

- [ ] **Step 5: Run tool, chat, and frontend type tests**

Run:
```powershell
go test ./internal/service -run 'TestFileTools|TestRunToolRounds|TestPersistChatTurn' -v
cd frontend
npm run build
```

Expected: PASS; the frontend form compiles with an optional `file_tools` multi-select.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/file_tools.go backend/internal/service/file_tools_test.go backend/internal/service/file_delivery.go backend/internal/service/chat_service.go backend/internal/service/agent_service.go backend/internal/api/agent/handler.go frontend/src/pages/agent/AgentFormPage.tsx frontend/src/types/index.ts
git commit -m "feat: add agent file tools"
```

### Task 6: Cleanup Worker, Usage Reporting, and Manual Cleanup

**Files:**
- Create: `backend/internal/service/file_cleanup.go`
- Create: `backend/internal/service/file_cleanup_test.go`
- Modify: `backend/internal/repository/file_repository.go`
- Modify: `backend/internal/service/file_service.go`
- Modify: `backend/internal/api/platform/handler.go`
- Modify: `backend/internal/service/platform_service.go`
- Modify: `backend/internal/repository/platform_repository.go`

**Interfaces:**
- Consumes: `ChatFileRepository`, `FileStorage`, `AuditLogRepository`, `config.ChatFileConfig`.
- Produces: `FileCleanupJob.Start/Stop`, `FileService.Cleanup`, `FileService.GlobalUsage`, and platform cleanup/storage endpoints.

- [ ] **Step 1: Write failing cleanup tests**

```go
func TestFileCleanupMarksDeletingThenDeletedAndRetriesFailure(t *testing.T) {
	repo := newFakeChatFileRepository()
	repo.expired = []model.ChatFile{
		{ID: "file-1", StorageKey: "agent/file-1", SizeBytes: 10, Status: model.ChatFileStatusReady},
		{ID: "file-2", StorageKey: "agent/file-2", SizeBytes: 20, Status: model.ChatFileStatusReady},
	}
	storage := &failingDeleteStorage{failKey: "agent/file-2"}
	svc := &fileService{repo: repo, storage: storage, cfg: testChatFileConfig()}
	run, err := svc.Cleanup(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if run.Deleted != 1 || run.Failed != 1 || run.ReleasedBytes != 10 {
		t.Fatalf("cleanup = %+v", run)
	}
	if repo.status["file-1"] != model.ChatFileStatusDeleted || repo.status["file-2"] != model.ChatFileStatusFailed {
		t.Fatalf("statuses = %+v", repo.status)
	}
}

func TestFileCleanupJobRunsImmediatelyThenOnInterval(t *testing.T) {
	svc := &fakeCleanupFileService{runs: make(chan struct{}, 2)}
	job := NewFileCleanupJob(svc, time.Hour)
	job.runNow(context.Background())
	job.runNow(context.Background())
	if len(svc.runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(svc.runs))
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:
```powershell
go test ./internal/service -run 'TestFileCleanup' -v
```

Expected: FAIL because cleanup methods and `NewFileCleanupJob` do not exist.

- [ ] **Step 3: Implement atomic claim and cleanup state machine**

Add this repository method and use one PostgreSQL statement so two instances cannot claim the same row:

```go
func (r *chatFileRepository) ClaimExpired(ctx context.Context, now time.Time, limit int) ([]model.ChatFile, error) {
	const sql = `
	WITH candidates AS (
		SELECT id FROM chat_files
		WHERE status IN ('staging','ready','failed')
		  AND retention_expires_at <= ?
		  AND NOT EXISTS (
			SELECT 1 FROM channel_deliveries d
			WHERE d.file_id = chat_files.id AND d.status IN ('pending','sending')
		  )
		ORDER BY retention_expires_at
		FOR UPDATE SKIP LOCKED
		LIMIT ?
	)
	UPDATE chat_files f SET status='deleting', updated_at=NOW(), last_error=''
	FROM candidates c WHERE f.id = c.id
	RETURNING f.*`
	var files []model.ChatFile
	err := database.DB.WithContext(ctx).Raw(sql, now, limit).Scan(&files).Error
	return files, err
}
```

For every claimed file call `storage.Delete`; on success set `deleted`, `deleted_at`, and increment released bytes. On failure set `failed` and store the original error. Query and delete expired event payloads older than `CHANNEL_EVENT_RETENTION`; delete sent/failed deliveries older than `30 days`; delete used bind codes older than their `used_at + 24h`.

For orphan scanning, collect all `storage_key` values, walk the root, move unknown keys to `.quarantine`, and remove quarantine files older than one cleanup interval. Never delete a path created by a file row that is currently `deleting`.

- [ ] **Step 4: Add the job and platform endpoints**

```go
type FileCleanupJob struct {
	svc      FileService
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

func NewFileCleanupJob(svc FileService, interval time.Duration) *FileCleanupJob {
	if interval <= 0 {
		interval = time.Hour
	}
	return &FileCleanupJob{svc: svc, interval: interval, stop: make(chan struct{}), done: make(chan struct{})}
}

func (j *FileCleanupJob) Start() {
	go func() {
		defer close(j.done)
		j.runNow(context.Background())
		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				j.runNow(context.Background())
			case <-j.stop:
				return
			}
		}
	}()
}
```

Register platform-only endpoints:

```go
group.GET("/files/storage", middleware.AuthCheck("platform:manage"), h.StorageUsage)
group.POST("/files/cleanup", middleware.AuthCheck("platform:manage"), h.Cleanup)
```

`StorageUsage` returns global bytes/limit, top sessions by bytes, pending cleanup count, and the latest `file.cleanup` audit result. `Cleanup` runs the same `FileService.Cleanup` path and writes a `file.cleanup` audit row; it never deletes a physical object directly.

- [ ] **Step 5: Run cleanup, platform, and service tests**

Run:
```powershell
go test ./internal/service -run 'TestFileCleanup|TestPlatformService' -v
go test ./internal/repository ./internal/service ./internal/api/platform
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/file_cleanup.go backend/internal/service/file_cleanup_test.go backend/internal/repository/file_repository.go backend/internal/service/file_service.go backend/internal/api/platform/handler.go backend/internal/service/platform_service.go backend/internal/repository/platform_repository.go
git commit -m "feat: add chat file cleanup and usage reporting"
```

### Task 7: Channel Models, Repository, Configuration, and Admin Service

**Files:**
- Create: `backend/internal/model/channel.go`
- Create: `backend/internal/repository/channel_repository.go`
- Create: `backend/internal/service/channel_admin_service.go`
- Create: `backend/internal/service/channel_admin_service_test.go`
- Modify: `backend/internal/database/database.go`
- Create: `backend/internal/api/channel/handler.go`
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `crypto.AesGCM`, `AgentRepository`, `AgentInstanceRepository`, `AuditLogRepository`, `ChannelConfig`.
- Produces: all channel models and repositories, `ChannelAdminService`, encrypted secret views, group bind codes, and channel administration HTTP endpoints.

```go
type ChannelAdminService interface {
	ListConnections(ctx context.Context) ([]model.ChannelConnectionView, error)
	GetConnection(ctx context.Context, id string) (*model.ChannelConnectionView, error)
	UpsertFeishuConnection(ctx context.Context, req UpsertChannelConnectionRequest, operatorID string) (*model.ChannelConnectionView, error)
	TestConnection(ctx context.Context, id string) (*ChannelTestResult, error)
	ListIdentities(ctx context.Context, connectionID string, page, size int) ([]model.ChannelIdentityView, int64, error)
	BindIdentityPlatformUser(ctx context.Context, identityID string, platformUserID *string, operatorID string) error
	GetAgentBinding(ctx context.Context, agentID, connectionID string) (*model.AgentChannelBindingView, error)
	UpdateAgentBinding(ctx context.Context, agentID, connectionID string, req UpdateAgentChannelBindingRequest, operatorID string) error
	ListGroupBindings(ctx context.Context, agentID, connectionID string) ([]model.ChannelGroupBinding, error)
	CreateGroupBinding(ctx context.Context, agentID, connectionID string, req CreateGroupBindingRequest, operatorID string) (*model.ChannelGroupBinding, error)
	DeleteGroupBinding(ctx context.Context, agentID, connectionID, groupID string) error
	CreateBindCode(ctx context.Context, agentID, connectionID string, operatorID string) (*BindCodeResult, error)
}
```

- [ ] **Step 1: Write failing admin service tests for one enabled connection, masking, and bind-code expiry**

```go
func TestChannelAdminAllowsOneEnabledConnectionAndMasksSecret(t *testing.T) {
	cipher, _ := crypto.NewAesGCM(strings.Repeat("ab", 32))
	repo := newFakeChannelAdminRepository()
	svc := NewChannelAdminService(repo, cipher, nil, nil, nil, ChannelConfig{})
	first := true
	view, err := svc.UpsertFeishuConnection(context.Background(), UpsertChannelConnectionRequest{
		Name: "主飞书", AppID: "cli_1", AppSecret: "secret-1", Enabled: &first,
	}, "user-1")
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	if view.AppSecretMask != "******" || repo.connection.AppSecretEncrypted == "secret-1" {
		t.Fatalf("secret was not masked/encrypted: view=%+v stored=%q", view, repo.connection.AppSecretEncrypted)
	}
	_, err = svc.UpsertFeishuConnection(context.Background(), UpsertChannelConnectionRequest{
		Name: "第二飞书", AppID: "cli_2", AppSecret: "secret-2", Enabled: &first,
	}, "user-1")
	appErr, ok := err.(*errors.AppError)
	if !ok || appErr.Code != "validation_error" {
		t.Fatalf("second enabled connection error = %#v", err)
	}
}

func TestBindCodeIsHashedSingleUseAndExpiresAfterTenMinutes(t *testing.T) {
	repo := newFakeChannelAdminRepository()
	svc := NewChannelAdminService(repo, nil, nil, nil, nil, ChannelConfig{})
	result, err := svc.CreateBindCode(context.Background(), "agent-1", "conn-1", "user-1")
	if err != nil {
		t.Fatalf("CreateBindCode: %v", err)
	}
	if result.Code == "" || repo.bindCode.CodeHash == result.Code {
		t.Fatalf("bind code must be returned once and stored hashed")
	}
	if got := result.ExpiresAt.Sub(result.CreatedAt); got != 10*time.Minute {
		t.Fatalf("bind-code lifetime = %s, want 10m", got)
	}
	ok, err := repo.ConsumeBindCode(context.Background(), sha256Hex(result.Code), time.Now())
	if err != nil || !ok {
		t.Fatalf("first consume = %v, %v", ok, err)
	}
	ok, err = repo.ConsumeBindCode(context.Background(), sha256Hex(result.Code), time.Now())
	if err != nil || ok {
		t.Fatalf("second consume = %v, %v; want false, nil", ok, err)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:
```powershell
go test ./internal/service -run 'TestChannelAdmin|TestBindCode' -v
```

Expected: FAIL because the channel model and admin service do not exist.

- [ ] **Step 3: Add channel models and migration indexes**

Create models matching spec section 5 exactly, plus these response views:

```go
type ChannelConnectionView struct {
	ID              string     `json:"id"`
	Provider        string     `json:"provider"`
	Name            string     `json:"name"`
	Mode            string     `json:"mode"`
	AppID           string     `json:"app_id"`
	AppSecretMask   string     `json:"app_secret_mask"`
	Enabled         bool       `json:"enabled"`
	Status          string     `json:"status"`
	LastConnectedAt *time.Time `json:"last_connected_at"`
	LastEventAt     *time.Time `json:"last_event_at"`
	LastError       string     `json:"last_error"`
	CreatedBy       *string    `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type ChannelIdentityView struct {
	model.ChannelIdentity
	PlatformUsername string `json:"platform_username,omitempty"`
}
```

Add all new models to `database.AutoMigrate` and create idempotent partial unique indexes:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_enabled_provider
ON channel_connections(provider) WHERE enabled = TRUE;

CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_identity_union
ON channel_identities(connection_id, union_id)
WHERE union_id IS NOT NULL AND union_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_identity_open
ON channel_identities(connection_id, open_id)
WHERE union_id IS NULL OR union_id = '';

CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_event_message
ON channel_events(connection_id, message_id)
WHERE message_id <> '';
```

- [ ] **Step 4: Implement repository and service rules**

`UpsertFeishuConnection` must:

1. Validate non-empty `AppID` and `AppSecret`.
2. Encrypt `AppSecret` with AES-GCM before writing.
3. Reject a second enabled connection with `validation_error`.
4. Set `mode=long_connection` and `provider=feishu`.
5. Append a `channel.connection.update` audit row with only masked metadata.

`ConsumeBindCode` must use one SQL update:

```sql
UPDATE channel_bind_codes
SET used_at = ?
WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?
RETURNING *
```

All identity, binding, group, and bind-code routes are registered under the existing permission model:

```go
platform := router.Group("/channels")
platform.Use(middleware.Auth())
{
	platform.GET("", middleware.AuthCheck("platform:manage"), h.ListConnections)
	platform.GET("/:id", middleware.AuthCheck("platform:manage"), h.GetConnection)
	platform.PUT("/:id", middleware.AuthCheck("platform:manage"), h.UpdateConnection)
	platform.POST("/:id/test", middleware.AuthCheck("platform:manage"), h.TestConnection)
}
router.GET("/channel-identities", middleware.Auth(), middleware.AuthCheck("platform:manage"), h.ListIdentities)
router.PUT("/channel-identities/:id", middleware.Auth(), middleware.AuthCheck("platform:manage"), h.UpdateIdentity)

agents := router.Group("/agents")
agents.Use(middleware.Auth())
{
	agents.GET("/:id/channels", middleware.AuthCheck("agent:read"), h.ListAgentChannels)
	agents.PUT("/:id/channels/:channelId", middleware.AuthCheck("agent:write"), h.UpdateAgentChannel)
	agents.GET("/:id/channels/:channelId/groups", middleware.AuthCheck("agent:read"), h.ListGroups)
	agents.POST("/:id/channels/:channelId/groups", middleware.AuthCheck("agent:write"), h.CreateGroup)
	agents.DELETE("/:id/channels/:channelId/groups/:groupId", middleware.AuthCheck("agent:write"), h.DeleteGroup)
	agents.POST("/:id/channels/:channelId/bind-code", middleware.AuthCheck("agent:write"), h.CreateBindCode)
}
```

- [ ] **Step 5: Run tests and commit**

Run:
```powershell
go test ./internal/service -run 'TestChannelAdmin|TestBindCode' -v
go test ./internal/repository ./internal/service ./internal/api/channel
```

Expected: PASS.

```bash
git add backend/internal/model/channel.go backend/internal/repository/channel_repository.go backend/internal/service/channel_admin_service.go backend/internal/service/channel_admin_service_test.go backend/internal/api/channel/handler.go backend/internal/database/database.go backend/cmd/server/main.go
git commit -m "feat: add channel configuration and bindings"
```

### Task 8: Feishu WebSocket Runtime, Lease, and Reconnect Loop

**Files:**
- Create: `backend/internal/service/channel_runtime.go`
- Create: `backend/internal/service/channel_runtime_test.go`
- Create: `backend/internal/service/feishu_event_source.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `ChannelConnectionRepository`, `ChannelRuntimeLeaseRepository`, decrypted credentials, `ChannelConfig`.
- Produces: `FeishuEventSource`, `DomainFeishuEvent`, `ChannelRuntimeManager.Start/Stop/Reload`, runtime status updates, and the official SDK event adapter.

```go
type DomainFeishuEvent struct {
	EventID       string
	EventType     string
	TenantKey     string
	MessageID     string
	ChatID        string
	ChatType      string
	MessageType   string
	Content       string
	MentionsBot   bool
	SenderType    string
	SenderOpenID  string
	SenderUnionID string
	SenderUserID  string
	CreatedAt     time.Time
}

type FeishuEventSource interface {
	Run(ctx context.Context, appID, appSecret string, handler func(context.Context, DomainFeishuEvent) error) error
}

type ChannelRuntimeManager interface {
	Start(ctx context.Context)
	Stop(ctx context.Context) error
	Reload(ctx context.Context, connectionID string) error
	Status(connectionID string) model.ChannelConnectionRuntime
}
```

- [ ] **Step 1: Add the official SDK dependency and write failing lease/reconnect tests**

Run:
```powershell
cd backend
go mod edit -require=github.com/larksuite/oapi-sdk-go/v3@v3.12.2
go mod tidy
```

```go
func TestRuntimeDoesNotStartSourceWithoutLease(t *testing.T) {
	lease := &fakeLeaseRepo{acquire: false}
	source := &fakeEventSource{started: make(chan struct{}, 1)}
	mgr := NewChannelRuntimeManager(&fakeChannelRuntimeRepo{connection: testFeishuConnection()}, lease, source, nil, ChannelConfig{LeaseTTL: 45 * time.Second, ReconnectMax: time.Minute}, testRuntimeBackoff)
	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)
	select {
	case <-source.started:
		t.Fatal("source started without a lease")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	_ = mgr.Stop(context.Background())
}

func TestRuntimeReconnectBackoffIsCappedAtOneMinute(t *testing.T) {
	got := exponentialBackoff(20, time.Minute)
	if got > time.Minute {
		t.Fatalf("backoff = %s, want <= 1m", got)
	}
}

func TestRuntimeRenewsLeaseEveryFifteenSecondsAndReleasesOnStop(t *testing.T) {
	lease := &fakeLeaseRepo{acquire: true, renews: make(chan struct{}, 2), releases: make(chan struct{}, 1)}
	source := &fakeEventSource{block: true, started: make(chan struct{}, 1)}
	mgr := NewChannelRuntimeManager(&fakeChannelRuntimeRepo{connection: testFeishuConnection()}, lease, source, nil, ChannelConfig{LeaseTTL: 45 * time.Second, ReconnectMax: time.Minute}, testRuntimeBackoff)
	mgr.renewEvery = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)
	<-source.started
	<-lease.renews
	cancel()
	_ = mgr.Stop(context.Background())
	select {
	case <-lease.releases:
	case <-time.After(time.Second):
		t.Fatal("lease was not released")
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:
```powershell
go test ./internal/service -run 'TestRuntime' -v
```

Expected: FAIL because the runtime manager does not exist.

- [ ] **Step 3: Implement lease acquisition and the reconnect loop**

Use `INSERT ... ON CONFLICT DO UPDATE ... WHERE expired` so acquisition is atomic:

```sql
INSERT INTO channel_runtime_leases (connection_id, owner_id, lease_expires_at, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (connection_id) DO UPDATE
SET owner_id = EXCLUDED.owner_id,
    lease_expires_at = EXCLUDED.lease_expires_at,
    updated_at = EXCLUDED.updated_at
WHERE channel_runtime_leases.lease_expires_at <= ?`
```

The runtime manager must create a fresh WebSocket client for every attempt; the SDK client cannot be reused. Use `WithAutoReconnect(false)`, register callbacks, run `Start(ctx)`, and close from a goroutine when the attempt context ends:

```go
client := larkws.NewClient(appID, appSecret,
	larkws.WithEventHandler(dispatcher),
	larkws.WithAutoReconnect(false),
	larkws.WithOnReady(func() { ready <- struct{}{} }),
	larkws.WithOnError(func(err error) { errors <- err }),
)
go func() {
	<-attemptCtx.Done()
	_ = client.CloseAndWait(context.Background())
}()
err := client.Start(attemptCtx)
```

Backoff is `1s, 2s, 4s, 8s, 16s, 32s, 60s, 60s...` with up to `10%` jitter and an absolute cap of `CHANNEL_RECONNECT_MAX` (`1m`).

Update connection status to `connecting`, `connected`, `reconnecting`, and `error` as specified. Set `last_connected_at`, `last_event_at`, and `last_error`. Renew every `15s`; release the lease and wait for the renewal goroutine on stop.

- [ ] **Step 4: Implement SDK event conversion**

```go
func newFeishuEventSource() FeishuEventSource {
	return &sdkFeishuEventSource{}
}

func (s *sdkFeishuEventSource) Run(ctx context.Context, appID, appSecret string, handler func(context.Context, DomainFeishuEvent) error) error {
	dispatcher := larkdispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			handler(ctx, convertFeishuMessageEvent(event))
			return nil
		})
	client := larkws.NewClient(appID, appSecret,
		larkws.WithEventHandler(dispatcher),
		larkws.WithAutoReconnect(false),
	)
	go func() {
		<-ctx.Done()
		_ = client.CloseAndWait(context.Background())
	}()
	return client.Start(ctx)
}
```

`convertFeishuMessageEvent` must read `Header.EventID`, `EventType`, `TenantKey`, sender IDs, and message IDs defensively, set `MentionsBot=true` when any `MentionEvent.MentionedType` equals `bot`, and parse `CreateTime` from Unix milliseconds.

- [ ] **Step 5: Wire startup and shutdown**

In `main.go`, start the runtime manager after AutoMigrate and service construction. On `SIGINT/SIGTERM`, stop the cleanup job, delivery worker, runtime manager, health checkers, watchdog, and runtime in that order. `Reload` is called after a successful connection update.

- [ ] **Step 6: Run tests and commit**

Run:
```powershell
go test ./internal/service -run 'TestRuntime' -v
go test ./...
```

Expected: PASS with `github.com/larksuite/oapi-sdk-go/v3 v3.12.2` resolved in `go.sum`.

```bash
git add backend/go.mod backend/go.sum backend/internal/service/channel_runtime.go backend/internal/service/channel_runtime_test.go backend/internal/service/feishu_event_source.go backend/cmd/server/main.go
git commit -m "feat: add feishu websocket runtime and lease"
```

### Task 9: Event Deduplication, Identity Routing, and Text Commands

**Files:**
- Create: `backend/internal/service/channel_event_service.go`
- Create: `backend/internal/service/channel_event_service_test.go`
- Modify: `backend/internal/repository/channel_repository.go`
- Modify: `backend/internal/service/channel_admin_service.go`

**Interfaces:**
- Consumes: `DomainFeishuEvent`, `ChannelAdminService`, `ChatService.ChatFromChannel`, `AgentRepository`, `AgentInstanceRepository`, `ChannelDeliveryService`.
- Produces: event status machine, per-conversation serialization, private/group routing, `/agent`, `/new`, `/help`, and `/bind` handling.

```go
type ChannelEventService interface {
	Handle(ctx context.Context, connectionID string, event DomainFeishuEvent) error
}

type ConversationLocker interface {
	Lock(key string) func()
}

type ChannelAgentOption struct {
	ID   string
	Name string
}

type ChannelRouter interface {
	RenderAvailableAgents(ctx context.Context, connectionID, identityID string) (string, error)
	ResolvePrivateAgent(ctx context.Context, connectionID, identityID, name string) (string, error)
	SavePrivatePreference(ctx context.Context, connectionID, identityID, agentID string) error
	ResetPrivateConversation(ctx context.Context, connectionID, identityID string) error
	ResolveGroupAgent(ctx context.Context, connectionID, chatID string) (string, error)
}

type ChannelDeliveryService interface {
	EnqueueText(ctx context.Context, connectionID, agentID, targetType, targetID, replyToMessageID, text string) error
	EnqueueReply(ctx context.Context, connectionID, agentID, sessionID, targetType, targetID, replyToMessageID string, result *ChatResult) error
	NotifyTurnFiles(ctx context.Context, scope FileScope, assistantMessageID string, deliveries []FileDelivery) error
	Start(ctx context.Context)
	Stop(ctx context.Context) error
}
```

- [ ] **Step 1: Write failing deduplication, group, and private-routing tests**

```go
type fakeChannelDeliveryService struct{}

func (*fakeChannelDeliveryService) EnqueueText(context.Context, string, string, string, string, string, string) error {
	return nil
}

func (*fakeChannelDeliveryService) EnqueueReply(context.Context, string, string, string, string, string, string, *ChatResult) error {
	return nil
}

func (*fakeChannelDeliveryService) NotifyTurnFiles(context.Context, FileScope, string, []FileDelivery) error {
	return nil
}

func (*fakeChannelDeliveryService) Start(context.Context) {}

func (*fakeChannelDeliveryService) Stop(context.Context) error { return nil }

func TestChannelEventDuplicateMessageDoesNotInvokeChatTwice(t *testing.T) {
	repo := newFakeChannelEventRepo()
	repo.messageDuplicate = true
	chat := &fakeChannelChatService{}
	svc := NewChannelEventService(repo, newFakeChannelRouterWithAgent("agent-1"), chat, &fakeChannelDeliveryService{}, newKeyedConversationLocker())
	err := svc.Handle(context.Background(), "conn-1", DomainFeishuEvent{
		EventID: "evt-1", MessageID: "msg-1", ChatType: "p2p",
		SenderOpenID: "ou_1", MessageType: "text", Content: `{"text":"你好"}`,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if chat.calls != 0 {
		t.Fatalf("chat calls = %d, want 0", chat.calls)
	}
	if repo.status != model.ChannelEventIgnored {
		t.Fatalf("status = %s, want ignored", repo.status)
	}
}

func TestGroupMessageRequiresBotMentionAndUsesSharedGroupSession(t *testing.T) {
	repo := newFakeChannelEventRepo()
	chat := &fakeChannelChatService{result: &ChatResult{SessionID: "shared-session", Reply: "ok"}}
	router := newFakeChannelRouterWithGroup("chat-1", "agent-1")
	svc := NewChannelEventService(repo, router, chat, &fakeChannelDeliveryService{}, newKeyedConversationLocker())
	_ = svc.Handle(context.Background(), "conn-1", DomainFeishuEvent{
		EventID: "evt-1", MessageID: "msg-1", ChatID: "chat-1", ChatType: "group",
		SenderOpenID: "ou_1", MessageType: "text", Content: `{"text":"你好"}`, MentionsBot: false,
	})
	if chat.calls != 0 {
		t.Fatal("group message without bot mention must be ignored")
	}
	_ = svc.Handle(context.Background(), "conn-1", DomainFeishuEvent{
		EventID: "evt-2", MessageID: "msg-2", ChatID: "chat-1", ChatType: "group",
		SenderOpenID: "ou_2", MessageType: "text", Content: `{"text":"@_user_1 你好"}`, MentionsBot: true,
	})
	if chat.calls != 1 || chat.lastRequest.SessionID == nil || *chat.lastRequest.SessionID != "shared-session" {
		t.Fatalf("group route = calls=%d request=%+v", chat.calls, chat.lastRequest)
	}
}

func TestPrivateAgentSelectionPersistsPreference(t *testing.T) {
	repo := newFakeChannelEventRepo()
	router := &fakeChannelRouter{available: []ChannelAgentOption{{ID: "agent-1", Name: "小助手"}}}
	svc := NewChannelEventService(repo, router, &fakeChannelChatService{}, &fakeChannelDeliveryService{}, newKeyedConversationLocker())
	_ = svc.Handle(context.Background(), "conn-1", p2pTextEvent("/agent 小助手"))
	if repo.preference.AgentID != "agent-1" {
		t.Fatalf("preference = %+v, want agent-1", repo.preference)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:
```powershell
go test ./internal/service -run 'TestChannelEvent|TestGroupMessage|TestPrivateAgent' -v
```

Expected: FAIL because the event service and router do not exist.

- [ ] **Step 3: Implement event persistence and idempotency**

Insert the source event with `ON CONFLICT DO NOTHING` on both unique keys. If no row is inserted or the existing status is `processed`, return without invoking chat:

```sql
INSERT INTO channel_events
  (connection_id, event_id, message_id, event_type, status, payload, attempts, received_at, expires_at)
VALUES (?, ?, ?, ?, 'received', ?::jsonb, 0, ?, ?)
ON CONFLICT (connection_id, event_id) DO NOTHING
RETURNING id
```

Status transitions are exactly `received -> processing -> processed|failed|ignored`. Store only IDs, sender type, chat type, message type, mention flag, and text length in `payload`; never store the complete SDK event.

Use an in-process keyed mutex per `conversation_key`; return the unlock function from `Lock`. The database lease remains the cross-instance guarantee.

- [ ] **Step 4: Implement commands and routing**

Private commands are parsed before Agent execution:

```go
switch {
case command == "/agent":
	reply = router.RenderAvailableAgents(ctx, connectionID, identityID)
case strings.HasPrefix(message, "/agent "):
	agentID, err := router.ResolvePrivateAgent(ctx, connectionID, identityID, strings.TrimSpace(strings.TrimPrefix(message, "/agent ")))
	if err == nil {
		err = router.SavePrivatePreference(ctx, connectionID, identityID, agentID)
	}
case command == "/new":
	err = router.ResetPrivateConversation(ctx, connectionID, identityID)
	reply = "已创建新会话。"
case command == "/help":
	reply = "/agent 列出可用 Agent\n/agent <名称> 选择 Agent\n/new 新建会话\n/help 帮助"
}
```

Group `/bind <code>` calls `ConsumeBindCode`; the command can only create a binding when no enabled binding exists for the same `chat_id`. Successful bind replies with the Agent name and does not invoke the model.

Before invoking chat verify all conditions from spec section 3.5. Return a user-visible failure card for unavailable Agents, disabled identities, department checks that cannot be performed, and missing group/private authorization.

- [ ] **Step 5: Invoke chat and enqueue text replies**

```go
result, err := chat.ChatFromChannel(ctx, agentID, ChatRequest{
	SessionID: sessionID,
	Message:   userText,
}, ChatPrincipal{ChannelIdentityID: identityID})
if err != nil {
	deliveries.EnqueueText(ctx, connectionID, agentID, target, replyToMessageID, userFacingChannelError(err))
	return err
}
deliveries.EnqueueReply(ctx, connectionID, agentID, result.SessionID, target, replyToMessageID, result)
```

For groups, `reply_to_message_id` is always the inbound message ID and the delivery payload includes the sender open ID for a leading `@` mention.

- [ ] **Step 6: Run tests and commit**

Run:
```powershell
go test ./internal/service -run 'TestChannelEvent|TestGroupMessage|TestPrivateAgent|TestBindCode' -v
go test ./internal/service
```

Expected: PASS.

```bash
git add backend/internal/service/channel_event_service.go backend/internal/service/channel_event_service_test.go backend/internal/repository/channel_repository.go backend/internal/service/channel_admin_service.go
git commit -m "feat: route feishu events and commands"
```

### Task 10: Feishu Inbound File Streaming

**Files:**
- Create: `backend/internal/service/feishu_resource.go`
- Create: `backend/internal/service/feishu_resource_test.go`
- Modify: `backend/internal/service/channel_event_service.go`
- Modify: `backend/internal/service/channel_event_service_test.go`
- Modify: `backend/internal/service/file_service.go`

**Interfaces:**
- Consumes: `FileService.Upload`, tenant access token client, `message_id`, `file_key` or `image_key`.
- Produces: `FeishuResourceDownloader.Download`, inbound file registration, and `file_ids` in channel chat requests.

```go
type FeishuResourceDownloader interface {
	Download(ctx context.Context, connection model.ChannelConnection, messageID, fileKey, resourceType string) (io.ReadCloser, string, int64, error)
}
```

- [ ] **Step 1: Write failing streaming download tests**

```go
func TestFeishuResourceStreamsBodyAndHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/im/v1/messages/msg-1/resources/file-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), 2048))
	}))
	defer server.Close()
	client := &http.Client{}
	d := NewFeishuResourceDownloader(client, func(ctx context.Context, appID, appSecret string) (string, error) {
		return "tenant-token", nil
	}, server.URL)
	rc, mime, size, err := d.Download(context.Background(), model.ChannelConnection{
		AppID: "cli_1", AppSecretEncrypted: "encrypted",
	}, "msg-1", "file-1", "file")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer rc.Close()
	if size != 2048 || mime != "application/octet-stream" {
		t.Fatalf("metadata = %s/%d", mime, size)
	}
}

func TestInboundFileOverPlatformLimitIsRejectedBeforeRegistration(t *testing.T) {
	files := &fakeChatFileService{uploadErr: &errors.AppError{Code: "file_too_large", Message: "too large", HTTPCode: 400}}
	eventSvc := &channelEventService{files: files, downloader: &fakeResourceDownloader{size: 200<<20 + 1}}
	err := eventSvc.registerInboundFiles(context.Background(), model.ChannelConnection{ID: "conn-1"}, "agent-1", "identity-1", DomainFeishuEvent{
		MessageID: "msg-1", MessageType: "file", Content: `{"file_key":"key-1","file_name":"a.bin"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want file_too_large", err)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:
```powershell
go test ./internal/service -run 'TestFeishuResource|TestInboundFile' -v
```

Expected: FAIL because the downloader does not exist.

- [ ] **Step 3: Implement tenant-token HTTP streaming**

Fetch the tenant token through the SDK exactly once per download attempt:

```go
client := lark.NewClient(appID, appSecret)
resp, err := client.GetTenantAccessTokenBySelfBuiltApp(ctx, &larkcore.SelfBuiltTenantAccessTokenReq{
	AppID: appID, AppSecret: appSecret,
})
token := resp.TenantAccessToken
```

Use a standard `http.Client` with `CheckRedirect` disabled and request:

```text
GET {base}/open-apis/im/v1/messages/{message_id}/resources/{file_key}?type={file|image}
Authorization: Bearer {tenant_access_token}
```

Return `resp.Body` directly. Do not call `io.ReadAll`, `bytes.NewBuffer`, `GetMessageResource`, `GetFile`, or `GetImage`. Require a 2xx response; include the response status and up to `512` bytes of the Feishu error body in the returned error, but never log the file body.

- [ ] **Step 4: Register inbound files and pass IDs into chat**

Parse `file_key`, `image_key`, `file_name`, and image type from message content. Download with `ChannelIdentityID` as the owner and `model.ChatFileSourceChannelInbound` as the source. Enforce `200MB` in `FileService.Upload` and allow up to five attachments per message.

Convert message input for the model without embedding file bytes:

```text
[本轮附件]
- file_id=<uuid> name=report.pdf mime=application/pdf size=1048576
需要内容时调用 read_file(file_id, offset, limit)。
```

Set `ChatRequest.FileIDs` to the new file IDs. Image files are stored as ordinary files with their actual MIME type and are not sent to a vision model.

- [ ] **Step 5: Run tests and commit**

Run:
```powershell
go test ./internal/service -run 'TestFeishuResource|TestInboundFile|TestChannelEvent' -v
go test ./internal/service
```

Expected: PASS.

```bash
git add backend/internal/service/feishu_resource.go backend/internal/service/feishu_resource_test.go backend/internal/service/channel_event_service.go backend/internal/service/channel_event_service_test.go backend/internal/service/file_service.go
git commit -m "feat: stream feishu inbound files"
```

### Task 11: Durable Feishu Delivery and Retry Worker

**Files:**
- Create: `backend/internal/service/channel_delivery_service.go`
- Create: `backend/internal/service/channel_delivery_service_test.go`
- Create: `backend/internal/service/feishu_sender.go`
- Create: `backend/internal/service/feishu_sender_test.go`
- Modify: `backend/internal/service/channel_event_service.go`
- Modify: `backend/internal/service/chat_service.go`
- Modify: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `ChannelDeliveryRepository`, `FileService`, `ChannelConnectionRepository`, Feishu SDK message/file upload, and `TurnFileNotifier`.
- Produces: the `ChannelDeliveryService` implementation, durable text/card/file deliveries, retry behavior, send status, and channel-aware `send_file`.

```go
type FeishuSender interface {
	SendText(ctx context.Context, conn model.ChannelConnection, targetType, targetID, replyToMessageID, text string) (string, error)
	SendCard(ctx context.Context, conn model.ChannelConnection, targetType, targetID, replyToMessageID, markdown string) (string, error)
	SendFile(ctx context.Context, conn model.ChannelConnection, targetType, targetID, replyToMessageID string, file *model.ChatFile, r io.Reader, text string) (string, error)
}
```

- [ ] **Step 1: Write failing retry and file-delivery tests**

```go
func TestDeliveryRetryScheduleAndMaxAttempts(t *testing.T) {
	want := []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute}
	cases := []int{1, 2, 3, 4}
	for i, attempts := range cases {
		if got := deliveryRetryDelay(attempts); got != want[i] {
			t.Fatalf("attempt %d delay = %s, want %s", attempts, got, want[i])
		}
	}
	if got := deliveryRetryDelay(5); got != 0 {
		t.Fatalf("attempt 5 delay = %s, want terminal", got)
	}
}

func TestDeliveryMarksSentAndStoresPlatformMessageID(t *testing.T) {
	repo := newFakeDeliveryRepo()
	repo.claim = []model.ChannelDelivery{{ID: "delivery-1", Kind: "text", Payload: datatypes.JSON(`{"text":"hello"}`)}}
	sender := &fakeFeishuSender{id: "om_sent_1"}
	svc := NewChannelDeliveryService(repo, &fakeDeliveryConnRepo{}, sender, &fakeDeliveryFileService{}, nil)
	_, err := svc.processClaim(context.Background(), repo.claim)
	if err != nil {
		t.Fatalf("processClaim: %v", err)
	}
	if repo.sentID != "om_sent_1" || repo.status != model.ChannelDeliverySent {
		t.Fatalf("status/id = %s/%s", repo.status, repo.sentID)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:
```powershell
go test ./internal/service -run 'TestDelivery|TestFeishuSender' -v
```

Expected: FAIL because delivery and sender services do not exist.

- [ ] **Step 3: Implement durable claiming and retries**

Claim rows with `FOR UPDATE SKIP LOCKED`:

```sql
WITH c AS (
  SELECT id FROM channel_deliveries
  WHERE ((status='pending') OR (status='failed' AND next_retry_at <= NOW()))
    AND (next_retry_at IS NULL OR next_retry_at <= NOW())
  ORDER BY created_at
  FOR UPDATE SKIP LOCKED
  LIMIT ?
)
UPDATE channel_deliveries d
SET status='sending', attempts=attempts+1, updated_at=NOW()
FROM c WHERE d.id=c.id
RETURNING d.*
```

For retryable errors (network, timeout, Feishu rate limit), set `pending`, increment attempts, and set `next_retry_at=now+delay`; after attempt `5`, set `failed`. For permanent credential/permission errors, set `failed` immediately. Store only the sanitized Feishu error code/message in `last_error`.

Text longer than Feishu's single-message limit is split at paragraph/newline boundaries while preserving order. Markdown uses an interactive card; tool approvals and errors use cards with plain text.

- [ ] **Step 4: Implement Feishu sends**

Use the SDK builders verified for `v3.12.2`:

```go
body := larkim.NewCreateMessageReqBodyBuilder().
	ReceiveId(targetID).
	MsgType("text").
	Content(`{"text":` + strconv.Quote(text) + `}`).
	Uuid(idempotencyKey).
	Build()
req := larkim.NewCreateMessageReqBuilder().
	ReceiveIdType(receiveIDType).
	Body(body).
	Build()
resp, err := client.Im.V1.Message.Create(ctx, req)
```

For replies use `NewReplyMessageReqBuilder().MessageId(replyToMessageID)` and `NewReplyMessageReqBodyBuilder()`. For files, guard at `30MB` and images at `10MB`, upload with `NewCreateFileReqBodyBuilder().FileType(...).FileName(...).File(reader)`, then send `msg_type=file` with `{"file_key":"..."}`. Preserve the original Feishu error when these upstream limits are exceeded.

- [ ] **Step 5: Connect `send_file` to channel deliveries**

`ChannelDeliveryService.NotifyTurnFiles` looks up the active `channel_conversations` row by scope/session, loads each file through `FileService`, and inserts one `kind=file` delivery per file. It returns an error if the target is no longer active. `ChatService` continues to persist the assistant file reference even when enqueue fails, and logs the enqueue failure with `file_id` and `assistantMessageID` only.

- [ ] **Step 6: Run tests and commit**

Run:
```powershell
go test ./internal/service -run 'TestDelivery|TestFeishuSender|TestFileTools' -v
go test ./...
```

Expected: PASS.

```bash
git add backend/internal/service/channel_delivery_service.go backend/internal/service/channel_delivery_service_test.go backend/internal/service/feishu_sender.go backend/internal/service/feishu_sender_test.go backend/internal/service/channel_event_service.go backend/internal/service/chat_service.go backend/cmd/server/main.go
git commit -m "feat: deliver feishu replies and files"
```

### Task 12: External API File Upload, Invocation IDs, and Authorized Delivery

**Files:**
- Modify: `backend/internal/api/agent/handler.go`
- Modify: `backend/internal/service/agent_service.go`
- Modify: `backend/internal/service/agent_kb_test.go` or create `backend/internal/service/agent_file_invoke_test.go`
- Modify: `docs/api/external-api.md`

**Interfaces:**
- Consumes: Agent API-key authentication, `FileService`, `ChannelAdminService`, `ChannelDeliveryService`.
- Produces: `POST /api/v1/agents/:id/invoke/files`, `POST /api/v1/agents/:id/invoke/channel-deliveries`, and `file_ids` forwarding through `/invoke`.

```go
type InvokeFileResult struct {
	File model.ChatFileView `json:"file"`
}

type DeliverInvokeFileRequest struct {
	FileID     string `json:"file_id" binding:"required"`
	TargetType string `json:"target_type" binding:"required"`
	TargetID   string `json:"target_id" binding:"required"`
	Text       string `json:"text" binding:"omitempty,max=2000"`
}
```

- [ ] **Step 1: Write failing authorization tests**

```go
func TestInvokeFileDeliveryRejectsUnauthorizedGroup(t *testing.T) {
	keyRepo := &fakeInvokeKeyRepo{key: activeKeyForAgent("agent-1")}
	channelAdmin := &fakeChannelAdminForInvoke{groupAuthorized: false}
	svc := &agentService{apiKeys: keyRepo, channelAdmin: channelAdmin, deliveries: &fakeInvokeDeliverySink{}}
	_, err := svc.DeliverInvokeFile(context.Background(), "agent-1", "akp_test", DeliverInvokeFileRequest{
		FileID: "file-1", TargetType: "chat", TargetID: "chat-not-authorized",
	})
	errApp, ok := err.(*errors.AppError)
	if !ok || errApp.Code != "channel_access_denied" {
		t.Fatalf("error = %#v, want channel_access_denied", err)
	}
}

func TestInvokeForwardsFileIDsToChatService(t *testing.T) {
	chat := &fakeInvokeChatService{}
	svc := &agentService{
		apiKeys: &fakeInvokeKeyRepo{key: activeKeyForAgent("agent-1")},
		agents:  &fakeAgentRepo{agent: runningApprovedAgent("agent-1")},
		modelSvc: &fakeInvokeModelSvc{available: true},
		chat: chat,
	}
	_, err := svc.InvokeAgent(context.Background(), "agent-1", "akp_test", InvokeAgentRequest{
		Message: "summarize", FileIDs: []string{"file-1"},
	})
	if err != nil {
		t.Fatalf("InvokeAgent: %v", err)
	}
	if strings.Join(chat.lastRequest.FileIDs, ",") != "file-1" || chat.lastRequest.APIKeyID == "" {
		t.Fatalf("chat request = %+v", chat.lastRequest)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```powershell
go test ./internal/service -run 'TestInvokeFile|TestInvokeForwards' -v
```

Expected: FAIL because `DeliverInvokeFile` and external file forwarding do not exist.

- [ ] **Step 3: Implement API-key upload and file ownership**

Add `owner_api_key_id` to `chat_files` and set it for `api_upload`. During file access, allow:

- same platform user,
- same channel identity,
- same API key ID,
- or an administrator with the corresponding Agent read permission.

Add an API-key-only route before the JSON invocation route:

```go
router.POST("/agents/:id/invoke/files", h.UploadInvokeFile)
```

The handler authenticates the Bearer API key, validates that it belongs to the Agent, parses `session_id` and `file` from multipart form data, and calls `FileService.Upload` with `Source=api_upload` and `OwnerAPIKeyID=key.ID`. The response is `201` with the public `file` view.

- [ ] **Step 4: Implement delivery authorization and forwarding**

Add:

```go
router.POST("/agents/:id/invoke/channel-deliveries", h.DeliverInvokeFile)
```

`DeliverInvokeFile` must:

1. Verify the Agent API key and running/approved Agent.
2. Verify the file belongs to that Agent and API key.
3. For `target_type=chat`, require an enabled `channel_group_bindings` row for the same Agent.
4. For `target_type=user`, require an enabled Agent private binding; `all` allows any identity, `users` requires an explicit allowlist match, and `departments` fails closed unless the identity is also explicitly allowlisted.
5. Enqueue `kind=file` through `ChannelDeliveryService` and return `202` with `delivery_id`.

Change all call sites of `s.chat.Invoke` and `s.chat.InvokeAsync` to pass `InvokeRequest{Message: req.Message, SessionID: req.SessionID, FileIDs: req.FileIDs, APIKeyID: key.ID}`. `InvokeAgentResult` and the persisted `ChatResult` include `files` and `generated_files`.

- [ ] **Step 5: Update external API documentation with exact request examples**

Document:

```http
POST /api/v1/agents/{agent_id}/invoke/files
Authorization: Bearer akp_...
Content-Type: multipart/form-data

session_id=<optional uuid>
file=<binary>
```

```json
{
  "message": "请读取附件并总结",
  "session_id": "uuid",
  "file_ids": ["uuid"]
}
```

```json
{
  "file_id": "uuid",
  "target_type": "chat",
  "target_id": "oc_xxx",
  "text": "处理结果"
}
```

- [ ] **Step 6: Run tests and commit**

Run:

```powershell
go test ./internal/service -run 'TestInvokeFile|TestInvokeForwards|TestInvokeAgent' -v
go test ./internal/api/agent ./internal/service
```

Expected: PASS.

```bash
git add backend/internal/api/agent/handler.go backend/internal/service/agent_service.go backend/internal/service/agent_file_invoke_test.go backend/internal/model/file.go backend/internal/repository/file_repository.go backend/internal/service/file_service.go docs/api/external-api.md
git commit -m "feat: add external file invoke APIs"
```

### Task 13: ChatPanel File Upload and Attachment Rendering

**Files:**
- Create: `frontend/src/api/file.ts`
- Create: `frontend/src/pages/agent/ChatAttachments.tsx`
- Modify: `frontend/src/pages/agent/ChatPanel.tsx`
- Modify: `frontend/src/api/agent.ts`
- Modify: `frontend/src/types/index.ts`
- Modify: `frontend/src/styles/global.css`

**Interfaces:**
- Consumes: `/agents/:id/files` APIs and `ChatRequest.file_ids`.
- Produces: drag/drop selection, per-file upload progress, removal before send, user attachment cards, assistant generated/sent cards, and history restoration.

```ts
export interface ChatFile {
  id: string;
  agent_id: string;
  session_id: string | null;
  source: string;
  name: string;
  mime_type: string;
  size_bytes: number;
  status: 'staging' | 'ready' | 'deleting' | 'deleted' | 'failed';
  retention_expires_at: string;
  created_at: string;
  url: string;
}

export interface UploadFileState {
  uid: string;
  file?: File;
  fileId?: string;
  name: string;
  size: number;
  progress: number;
  status: 'uploading' | 'done' | 'error';
  error?: string;
}
```

- [ ] **Step 1: Add API methods and run a failing build**

```ts
export const fileApi = {
  upload: (
    agentId: string,
    file: File,
    sessionId: string | undefined,
    onProgress: (percent: number) => void,
  ) => {
    const form = new FormData();
    if (sessionId) form.append('session_id', sessionId);
    form.append('file', file);
    return apiClient.post<ApiEnvelope<ChatFile>>(`/agents/${agentId}/files`, form, {
      timeout: 1800000,
      onUploadProgress: (event) => {
        if (event.total) onProgress(Math.round((event.loaded / event.total) * 100));
      },
    });
  },
  list: (agentId: string, params: { session_id?: string; page?: number; size?: number }) =>
    apiClient.get<ApiEnvelope<Paginated<ChatFile>>>(`/agents/${agentId}/files`, { params }),
  remove: (agentId: string, fileId: string) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(`/agents/${agentId}/files/${fileId}`),
};
```

Extend `chatStream` and the synchronous chat request with `file_ids`:

```ts
body: {
  session_id?: string;
  message: string;
  file_ids?: string[];
  show_thinking?: boolean;
}
```

Run:
```powershell
cd frontend
npm run build
```

Expected: FAIL until all changed call signatures compile.

- [ ] **Step 2: Implement the upload/attachment component**

Create `ChatAttachments.tsx` with:

- `Upload.Dragger` in the composer, `multiple`, and current pending/session files.
- A `files` prop, `onFilesChange` callback, and busy states.
- Per-item `Progress`, file icon, formatted byte size, and delete button.
- A file card renderer used by both upload previews and message bubbles.
- A preview modal for text/image MIME types with a download fallback.

Use `FileOutlined`, `PaperClipOutlined`, `DeleteOutlined`, `EyeOutlined`, and `DownloadOutlined` from `@ant-design/icons`; do not draw manual SVG icons.

The key component contract is:

```ts
export function ChatAttachments({
  agentId,
  sessionId,
  files,
  onFilesChange,
  disabled,
}: {
  agentId: string;
  sessionId?: string;
  files: UploadFileState[];
  onFilesChange: (files: UploadFileState[]) => void;
  disabled?: boolean;
}) {
  return (
    <div className="chat-attachments">
      {/* render upload dragger and file cards */}
    </div>
  );
}
```

- [ ] **Step 3: Integrate upload state into ChatPanel**

In `ChatPanel.tsx`:

1. Add `attachments` state and clear it when switching sessions or starting a new conversation.
2. Render `ChatAttachments` above the input.
3. Disable send while any attachment is uploading.
4. Reject send if any file failed or exceeds `200MB`.
5. Include `file_ids` in both `/chat` and `/chat/stream` bodies.
6. Keep the optimistic user message attachment preview until server messages reload.
7. On successful send, mark `ready`; on failure, restore upload state but keep the file for retry.
8. On `onNewSession`, allow upload before session creation and pass `session_id` only after a session exists.

The send path is:

```ts
const fileIds = attachments.filter((f) => f.status === 'done' && f.fileId).map((f) => f.fileId!);
const body = {
  ...(activeId ? { session_id: activeId } : {}),
  message: text,
  file_ids: fileIds,
  show_thinking: showThinking,
};
```

- [ ] **Step 4: Render message files and generated files**

Add `files?: ChatFile[]` to `ChatMessage`, `files?: ChatFile[]`, and `generated_files?: ChatFile[]` to `ChatResult`.

Modify `MessageBubble`:

- User bubbles render `msg.files` with a light background that remains readable against blue.
- Assistant bubbles render `msg.files`.
- Generated files returned in the final stream result render as a compact assistant file card until history reload occurs.
- Expired files render with a disabled state and the text `已过期清理`; do not show a download link.
- File names use `wordBreak: 'break-all'` and cards use `maxWidth: '100%'`.

- [ ] **Step 5: Verify responsive layout and build**

Run:
```powershell
cd frontend
npm run build
```

Expected: PASS with no TypeScript errors.

Perform a browser check at widths `375px`, `768px`, and `1440px`; verify drag/drop, upload progress, removal, send with five files, assistant file cards, and no overlap between the upload tray, textarea, and send button.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api/file.ts frontend/src/pages/agent/ChatAttachments.tsx frontend/src/pages/agent/ChatPanel.tsx frontend/src/api/agent.ts frontend/src/types/index.ts frontend/src/styles/global.css
git commit -m "feat: add chat file upload and attachment UI"
```

### Task 14: Channel Administration and Agent Channel UI

**Files:**
- Create: `frontend/src/api/channel.ts`
- Create: `frontend/src/pages/agent/ChannelPanel.tsx`
- Create: `frontend/src/pages/system/ChannelManagementPanel.tsx`
- Modify: `frontend/src/pages/agent/AgentDetailPage.tsx`
- Modify: `frontend/src/pages/system/PlatformSettingsPage.tsx`
- Modify: `frontend/src/types/index.ts`

**Interfaces:**
- Consumes: Task 7 HTTP routes, channel status views, Agent bindings, identities, and storage/cleanup endpoints.
- Produces: platform Feishu config/test/status, identity binding, agent channel enablement/authorization, group binding, and bind-code countdown.

```ts
export interface ChannelConnection {
  id: string;
  provider: 'feishu';
  name: string;
  mode: 'long_connection';
  app_id: string;
  app_secret_mask: string;
  enabled: boolean;
  status: 'disabled' | 'connecting' | 'connected' | 'reconnecting' | 'error';
  last_connected_at: string | null;
  last_event_at: string | null;
  last_error: string;
  created_at: string;
  updated_at: string;
}

export interface AgentChannelBindingView {
  connection_id: string;
  connection_name: string;
  enabled: boolean;
  private_access_mode: 'all' | 'users' | 'departments';
  allowed_user_ids: string[];
  allowed_department_ids: string[];
  status: ChannelConnection['status'];
  last_error: string;
}
```

- [ ] **Step 1: Add API client and failing build**

```ts
export const channelApi = {
  list: () => apiClient.get<ApiEnvelope<{ items: ChannelConnection[] }>>('/channels'),
  update: (id: string, data: UpdateChannelRequest) =>
    apiClient.put<ApiEnvelope<ChannelConnection>>(`/channels/${id}`, data),
  test: (id: string) => apiClient.post<ApiEnvelope<ChannelTestResult>>(`/channels/${id}/test`),
  listIdentities: (params: { page?: number; size?: number }) =>
    apiClient.get<ApiEnvelope<Paginated<ChannelIdentity>>>('/channel-identities', { params }),
  bindIdentity: (id: string, platformUserId: string | null) =>
    apiClient.put<ApiEnvelope<{ updated: boolean }>>(`/channel-identities/${id}`, {
      platform_user_id: platformUserId,
    }),
  listAgentChannels: (agentId: string) =>
    apiClient.get<ApiEnvelope<{ items: AgentChannelBindingView[] }>>(`/agents/${agentId}/channels`),
  updateAgentChannel: (agentId: string, channelId: string, data: UpdateAgentChannelRequest) =>
    apiClient.put<ApiEnvelope<AgentChannelBindingView>>(`/agents/${agentId}/channels/${channelId}`, data),
  listGroups: (agentId: string, channelId: string) =>
    apiClient.get<ApiEnvelope<{ items: ChannelGroupBinding[] }>>(
      `/agents/${agentId}/channels/${channelId}/groups`,
    ),
  createGroup: (agentId: string, channelId: string, data: CreateGroupRequest) =>
    apiClient.post<ApiEnvelope<ChannelGroupBinding>>(
      `/agents/${agentId}/channels/${channelId}/groups`,
      data,
    ),
  deleteGroup: (agentId: string, channelId: string, groupId: string) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(
      `/agents/${agentId}/channels/${channelId}/groups/${groupId}`,
    ),
  createBindCode: (agentId: string, channelId: string) =>
    apiClient.post<ApiEnvelope<BindCodeResult>>(
      `/agents/${agentId}/channels/${channelId}/bind-code`,
    ),
};
```

Run:
```powershell
cd frontend
npm run build
```

Expected: FAIL until types and component imports are added.

- [ ] **Step 2: Build platform channel management**

Create `ChannelManagementPanel.tsx` and embed it in `PlatformSettingsPage.tsx` under a Tabs item labeled `渠道管理`. The panel must contain:

- Feishu `App ID`, secret input, enable switch, save button.
- Secret field displays only `app_secret_mask` and is write-only; blank means keep current secret.
- Connection status, last event time, last error, and a test button with a spinner.
- Identity table with display name, open/union IDs, status, and platform-user binding action.
- Storage summary from `/platform/files/storage`: total bytes/limit, top sessions, pending cleanup count, and last cleanup result.
- `立即清理` button that calls `/platform/files/cleanup` and refreshes usage.

Use `Form`, `Input`, `Switch`, `Tabs`, `Table`, `Progress`, `Statistic`, `Button`, `Modal`, and `message` from Ant Design. Do not render the secret in a plain visible field after loading.

- [ ] **Step 3: Build Agent channel panel**

Create `ChannelPanel.tsx` and add a `渠道` tab to `AgentDetailPage.tsx`. The panel must:

- Load Agent channel bindings and show connection status.
- Allow enable/disable.
- Offer private access mode `all`, `users`, `departments`.
- Edit user and department ID lists with `Select mode="tags"`.
- Show a warning that `departments` fails closed when directory access is unavailable.
- Show group bindings and allow manual `chat_id` binding.
- Allow unbinding.
- Generate a bind code, display it in a modal with a 10-minute countdown, and invalidate the modal content when the timer expires.
- Show recent delivery failures and the last channel error when present.

The group create action uses:

```ts
await channelApi.createGroup(agentId, connectionId, {
  chat_id: chatId.trim(),
  chat_name: chatName.trim(),
  enabled: true,
});
```

- [ ] **Step 4: Add permission-aware rendering**

Use `useHasPermission()` for:

- `platform:manage` to render and invoke platform channel controls.
- `agent:read` to render status and group lists.
- `agent:write` to render and invoke update/bind actions.

If a permission is absent, keep the tab visible only when the user has `agent:read`; hide mutation controls.

- [ ] **Step 5: Build and manually verify**

Run:
```powershell
cd frontend
npm run build
```

Expected: PASS.

Verify the following in the browser:

- Secret masking and update behavior.
- Test connection result rendering.
- Identity binding and unbinding.
- Private `all`, `users`, and `departments` forms.
- Group manual bind and unbind.
- Bind-code countdown and expiration.
- Storage usage and cleanup result refresh.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api/channel.ts frontend/src/pages/agent/ChannelPanel.tsx frontend/src/pages/system/ChannelManagementPanel.tsx frontend/src/pages/agent/AgentDetailPage.tsx frontend/src/pages/system/PlatformSettingsPage.tsx frontend/src/types/index.ts
git commit -m "feat: add channel administration UI"
```

### Task 15: Deployment, Documentation, and Release Verification

**Files:**
- Create: `backend/internal/service/agent_channels_files_integration_test.go`
- Modify: `frontend/nginx.conf`
- Modify: `infra/docker-compose.yml`
- Modify: `README.md`
- Modify: `docs/api/api.md`
- Modify: `docs/api/external-api.md`
- Modify: `docs/superpowers/specs/2026-10-24-agent-channels-and-chat-files-design.md` only if implementation names differ from the finalized interfaces

**Interfaces:**
- Consumes all prior tasks.
- Produces deployable configuration, documented operational limits, and end-to-end verification evidence.

- [ ] **Step 1: Write the integration test for file lifecycle and authorization**

```go
func TestChatFileLifecycleIntegration(t *testing.T) {
	// Uses a test PostgreSQL database with migrations already applied.
	storage, _ := service.NewLocalFileStorage(t.TempDir())
	files := service.NewFileService(repository.NewChatFileRepository(), storage, testChatFileConfig(), repository.NewAuditLogRepository())
	uploaded, err := files.Upload(context.Background(), service.UploadFileRequest{
		Scope: service.FileScope{AgentID: "agent-1", UserID: "user-1"},
		Source: model.ChatFileSourceWebUpload, Name: "note.txt",
		Reader: strings.NewReader("hello"),
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := files.Get(context.Background(), service.FileScope{AgentID: "agent-1", UserID: "user-2"}, uploaded.ID, true); err == nil {
		t.Fatal("cross-user access must be denied")
	}
	if _, err := files.Delete(context.Background(), service.FileScope{AgentID: "agent-1", UserID: "user-1"}, uploaded.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
```

Add service-level integration tests for:

- Duplicate `event_id` and `message_id` produce one chat execution.
- A fake two-instance lease race yields exactly one active consumer.
- `create_file` produces an assistant message file and a Feishu file delivery when the turn originates from Feishu.
- API-key delivery to an unauthorized group returns `channel_access_denied`.
- Cleanup skips a file with a pending delivery and deletes it after delivery reaches a terminal state.

- [ ] **Step 2: Update deployment limits and persistent volume**

In `frontend/nginx.conf`, change the API location to:

```nginx
location /api/ {
    proxy_pass http://backend:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_request_buffering off;
    proxy_buffering off;
    proxy_connect_timeout 10s;
    proxy_read_timeout 1800s;
    proxy_send_timeout 1800s;
    client_max_body_size 1100m;
}
```

In `infra/docker-compose.yml`, add the persistent volume and environment defaults:

```yaml
    volumes:
      - backend_logs:/app/logs
      - chat_files:/app/data/chat-files
```

```yaml
      CHAT_FILE_DIR: /app/data/chat-files
      CHAT_FILE_MAX_SIZE: "200MB"
      CHAT_FILE_MAX_PER_MESSAGE: "5"
      CHAT_FILE_MAX_MESSAGE_BYTES: "1GB"
      CHAT_FILE_MAX_SESSION_BYTES: "10GB"
      CHAT_FILE_MAX_TOTAL_BYTES: "200GB"
      CHAT_FILE_RETENTION: "720h"
      CHAT_FILE_STAGING_RETENTION: "24h"
      CHAT_FILE_CLEANUP_INTERVAL: "1h"
```

```yaml
volumes:
  postgres_data:
  backend_logs:
  chat_files:
```

Do not create a shared file directory deployment for multiple backend instances; the documented local-storage deployment supports one backend container unless a shared object store is introduced.

- [ ] **Step 3: Update documentation**

Update `README.md` with:

- Feishu app setup and required permissions.
- Environment variables and limits.
- Web/chat file workflow.
- Channel configuration steps.
- Cleanup operations and disk monitoring.
- Single enabled Feishu connection and single-backend local-storage constraints.

Update `docs/api/api.md` with all user-facing endpoints and stable error codes:

```text
channel_not_configured
channel_disabled
channel_disconnected
channel_not_bound
channel_identity_disabled
channel_access_denied
agent_not_available
file_too_large
file_count_exceeded
file_quota_exceeded
file_not_ready
file_not_found
file_binary_unsupported
channel_delivery_failed
```

Update `docs/api/external-api.md` with API-key upload, `file_ids`, authorized channel delivery, async result `files`/`generated_files`, and the upstream Feishu `100MB` download / `30MB` file / `10MB` image limits.

- [ ] **Step 4: Run the full verification suite**

Run:
```powershell
cd backend
go test ./...
cd ..\frontend
npm run build
cd ..
docker compose -f infra/docker-compose.yml config
```

Expected:

- All Go tests pass.
- TypeScript/Vite production build succeeds.
- Docker Compose configuration validates and declares the `chat_files` volume.

If a real Feishu app is available, run the manual checks:

1. Save credentials and verify the status changes to `connected`.
2. Send a private `/agent <name>` command and receive the available Agent list.
3. Send a text message and receive a card reply.
4. Bind a group with `/bind <code>`, mention the bot, and receive a shared-session reply.
5. Send a file to the bot and verify `read_file` can read it.
6. Ask the Agent to call `create_file` and `send_file`, then verify the generated file arrives in Feishu.
7. Restart the backend and verify `chat_files` persists.
8. Stop the backend for more than `45s` and verify another allowed runtime can acquire the lease.
9. Trigger a Feishu rate-limit or oversized upload and verify the original technical error remains visible to administrators, while users receive a readable message.

- [ ] **Step 5: Run the final spec self-check**

Check each acceptance criterion from spec section 15 against a concrete task:

| Acceptance criterion | Implementing task |
| --- | --- |
| Admin configures one Feishu app and sees status | Task 7, Task 8, Task 14 |
| Private selection and group routing | Task 9, Task 14 |
| Shared group session and isolated private contexts | Task 9 |
| Duplicate events do not re-execute | Task 9 |
| Web and Feishu upload/read | Task 4, Task 9, Task 10, Task 13 |
| Agent creates and sends files | Task 5, Task 11 |
| API-key upload/delivery cannot bypass authorization | Task 12 |
| `200MB` / `1GB` / `10GB` / `200GB` limits | Task 1, Task 2, Task 4 |
| Expired/unreferenced files cleaned | Task 6 |
| Usage, quota, pending cleanup, and cleanup result visible | Task 6, Task 14 |
| Secrets, paths, and bodies not leaked | Task 1, Task 2, Task 7, Task 12, Task 15 |
| Permissions, unit tests, integration tests, and error coverage | All tasks; full suite in Task 15 |

Run a placeholder scan:

```powershell
rg -n "TBD|TODO|implement later|fill in details|add appropriate|similar to Task" docs/superpowers/plans/2026-10-24-agent-channels-and-chat-files.md
```

Expected: no matches.

Run a type consistency scan:

```powershell
rg -n "ChatPrincipal|FileScope|AppendTurn|FeishuEventSource|ChannelDeliveryService|NotifyTurnFiles|FileCleanupJob" docs/superpowers/plans/2026-10-24-agent-channels-and-chat-files.md
```

Expected: each name is defined before it is consumed and used consistently in later tasks.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/agent_channels_files_integration_test.go frontend/nginx.conf infra/docker-compose.yml README.md docs/api/api.md docs/api/external-api.md docs/superpowers/plans/2026-10-24-agent-channels-and-chat-files.md
git commit -m "docs: add channel and chat file deployment guidance"
```
