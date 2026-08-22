package repository

import (
    "context"
    "strings"
    "time"

    "agent-platform/internal/database"
    "agent-platform/internal/model"
    "agent-platform/pkg/errors"

    "gorm.io/gorm"
)

// MCPListFilter MCP 列表过滤条件
type MCPListFilter struct {
    Keyword  string
    Status   string
    Tag      string
    Page     int
    PageSize int
}

// MCPServerRepository MCP 服务器定义仓储
type MCPServerRepository interface {
    Create(ctx context.Context, server *model.MCPServer) error
    Get(ctx context.Context, id string) (*model.MCPServer, error)
    GetByName(ctx context.Context, name string) (*model.MCPServer, error)
    Update(ctx context.Context, server *model.MCPServer) error
    Delete(ctx context.Context, id string) error
    List(ctx context.Context, filter MCPListFilter) ([]model.MCPServer, int64, error)
    ListAll(ctx context.Context) ([]model.MCPServer, error)
    UpdateHealth(ctx context.Context, id string, status string, latencyMs *int, lastErr string) error
}

type mcpServerRepository struct{}

func NewMCPServerRepository() MCPServerRepository {
    return &mcpServerRepository{}
}

func (r *mcpServerRepository) Create(ctx context.Context, server *model.MCPServer) error {
    if err := database.DB.WithContext(ctx).Create(server).Error; err != nil {
        return err
    }
    return nil
}

func (r *mcpServerRepository) Get(ctx context.Context, id string) (*model.MCPServer, error) {
    var server model.MCPServer
    if err := database.DB.WithContext(ctx).First(&server, "id = ?", id).Error; err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, errors.ErrNotFound
        }
        return nil, err
    }
    return &server, nil
}

func (r *mcpServerRepository) GetByName(ctx context.Context, name string) (*model.MCPServer, error) {
    var server model.MCPServer
    if err := database.DB.WithContext(ctx).First(&server, "name = ?", name).Error; err != nil {
        if err == gorm.ErrRecordNotFound {
            return nil, nil
        }
        return nil, err
    }
    return &server, nil
}

func (r *mcpServerRepository) Update(ctx context.Context, server *model.MCPServer) error {
    if err := database.DB.WithContext(ctx).Save(server).Error; err != nil {
        return err
    }
    return nil
}

func (r *mcpServerRepository) Delete(ctx context.Context, id string) error {
    return database.DB.WithContext(ctx).Delete(&model.MCPServer{}, "id = ?", id).Error
}

func (r *mcpServerRepository) List(ctx context.Context, filter MCPListFilter) ([]model.MCPServer, int64, error) {
    query := database.DB.WithContext(ctx).Model(&model.MCPServer{})

    if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
        like := "%" + keyword + "%"
        query = query.Where("name ILIKE ? OR description ILIKE ? OR endpoint ILIKE ?", like, like, like)
    }
    if filter.Status != "" {
        query = query.Where("status = ?", filter.Status)
    }
    if filter.Tag != "" {
        // tags 为 JSONB 数组, 用 JSON 包含匹配
        query = query.Where("tags @> ?::jsonb", `["`+filter.Tag+`"]`)
    }

    var total int64
    if err := query.Count(&total).Error; err != nil {
        return nil, 0, err
    }

    page := filter.Page
    if page < 1 {
        page = 1
    }
    size := filter.PageSize
    if size < 1 || size > 100 {
        size = 20
    }

    var servers []model.MCPServer
    if err := query.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&servers).Error; err != nil {
        return nil, 0, err
    }
    return servers, total, nil
}

func (r *mcpServerRepository) ListAll(ctx context.Context) ([]model.MCPServer, error) {
    var servers []model.MCPServer
    if err := database.DB.WithContext(ctx).Find(&servers).Error; err != nil {
        return nil, err
    }
    return servers, nil
}

func (r *mcpServerRepository) UpdateHealth(ctx context.Context, id string, status string, latencyMs *int, lastErr string) error {
    updates := map[string]interface{}{
        "status":            status,
        "health_last_check": time.Now(),
        "health_latency_ms": latencyMs,
        "last_error":        lastErr,
    }
    return database.DB.WithContext(ctx).Model(&model.MCPServer{}).Where("id = ?", id).Updates(updates).Error
}

// MCPHealthLogRepository MCP 健康检查历史仓储
type MCPHealthLogRepository interface {
    Append(ctx context.Context, entry *model.MCPHealthLog) error
    List(ctx context.Context, mcpID string, limit int) ([]model.MCPHealthLog, error)
    Trim(ctx context.Context, mcpID string, keep int) error
    DeleteByMCP(ctx context.Context, mcpID string) error
}

type mcpHealthLogRepository struct{}

func NewMCPHealthLogRepository() MCPHealthLogRepository {
    return &mcpHealthLogRepository{}
}

func (r *mcpHealthLogRepository) Append(ctx context.Context, entry *model.MCPHealthLog) error {
    return database.DB.WithContext(ctx).Create(entry).Error
}

func (r *mcpHealthLogRepository) List(ctx context.Context, mcpID string, limit int) ([]model.MCPHealthLog, error) {
    if limit <= 0 || limit > 500 {
        limit = 100
    }
    var logs []model.MCPHealthLog
    err := database.DB.WithContext(ctx).
        Where("mcp_id = ?", mcpID).
        Order("created_at DESC").
        Limit(limit).
        Find(&logs).Error
    return logs, err
}

func (r *mcpHealthLogRepository) Trim(ctx context.Context, mcpID string, keep int) error {
    // 保留每个 MCP 服务器最近 keep 条, 超出部分删除
    return database.DB.WithContext(ctx).Exec(`
        DELETE FROM mcp_health_logs
        WHERE mcp_id = ?
          AND id NOT IN (
              SELECT id FROM mcp_health_logs
              WHERE mcp_id = ?
              ORDER BY created_at DESC
              LIMIT ?
          )
    `, mcpID, mcpID, keep).Error
}

func (r *mcpHealthLogRepository) DeleteByMCP(ctx context.Context, mcpID string) error {
    return database.DB.WithContext(ctx).Where("mcp_id = ?", mcpID).Delete(&model.MCPHealthLog{}).Error
}

// MCPAgentBindingRepository MCP <-> Agent 绑定仓储
type MCPAgentBindingRepository interface {
    Bind(ctx context.Context, mcpID, agentID string) error
    Unbind(ctx context.Context, mcpID, agentID string) error
    ListByMCP(ctx context.Context, mcpID string) ([]model.MCPAgentBinding, error)
    ListByAgent(ctx context.Context, agentID string) ([]model.MCPAgentBinding, error)
    DeleteByMCP(ctx context.Context, mcpID string) error
    DeleteByAgent(ctx context.Context, agentID string) error
}

type mcpAgentBindingRepository struct{}

func NewMCPAgentBindingRepository() MCPAgentBindingRepository {
    return &mcpAgentBindingRepository{}
}

func (r *mcpAgentBindingRepository) Bind(ctx context.Context, mcpID, agentID string) error {
    // 幂等: 已存在则忽略
    return database.DB.WithContext(ctx).Exec(`
        INSERT INTO mcp_agent_bindings (id, mcp_id, agent_id, created_at)
        SELECT gen_random_uuid(), ?, ?, now()
        WHERE NOT EXISTS (
            SELECT 1 FROM mcp_agent_bindings WHERE mcp_id = ? AND agent_id = ?
        )
    `, mcpID, agentID, mcpID, agentID).Error
}

func (r *mcpAgentBindingRepository) Unbind(ctx context.Context, mcpID, agentID string) error {
    return database.DB.WithContext(ctx).
        Where("mcp_id = ? AND agent_id = ?", mcpID, agentID).
        Delete(&model.MCPAgentBinding{}).Error
}

func (r *mcpAgentBindingRepository) ListByMCP(ctx context.Context, mcpID string) ([]model.MCPAgentBinding, error) {
    var bindings []model.MCPAgentBinding
    err := database.DB.WithContext(ctx).Where("mcp_id = ?", mcpID).Find(&bindings).Error
    return bindings, err
}

func (r *mcpAgentBindingRepository) ListByAgent(ctx context.Context, agentID string) ([]model.MCPAgentBinding, error) {
    var bindings []model.MCPAgentBinding
    err := database.DB.WithContext(ctx).Where("agent_id = ?", agentID).Find(&bindings).Error
    return bindings, err
}

func (r *mcpAgentBindingRepository) DeleteByMCP(ctx context.Context, mcpID string) error {
    return database.DB.WithContext(ctx).Where("mcp_id = ?", mcpID).Delete(&model.MCPAgentBinding{}).Error
}

func (r *mcpAgentBindingRepository) DeleteByAgent(ctx context.Context, agentID string) error {
    return database.DB.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&model.MCPAgentBinding{}).Error
}