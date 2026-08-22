package database

import (
    "fmt"
    "log"
    "time"

    "agent-platform/internal/config"
    applogger "agent-platform/pkg/logger"
    "agent-platform/internal/model"

    "gorm.io/driver/postgres"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init(cfg config.DatabaseConfig) (*gorm.DB, error) {
    dsn := fmt.Sprintf(
        "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=Asia/Shanghai",
        cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode,
    )

    var err error
    DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
                // Writer 使用 applogger.StdWriter() (原始 stdout + logs/ 日志文件), 使 SQL 日志一并落盘
                Logger: logger.New(applogger.PrintfWriter{W: applogger.StdWriter()}, logger.Config{
                    SlowThreshold:             200 * time.Millisecond,
                    IgnoreRecordNotFoundError: true,
                    Colorful:                  false,
                    LogLevel:                  logger.Info,
                }),
        // 必须开启 PrepareStmt: gorm postgres driver 的 AutoMigrate 检查已存在表时,
        // 会把 pgx.QueryExecModeSimpleProtocol 预置到 Statement.Vars 头部, 使 LIMIT
        // 占位符编号偏移 ($1 -> $2), pgx 剥离模式参数后参数数量不匹配,
        // 报 "insufficient arguments"。开启后走预编译路径, 问题消失。
        PrepareStmt: true,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to connect database: %w", err)
    }

    sqlDB, err := DB.DB()
    if err != nil {
        return nil, fmt.Errorf("failed to get sql.DB: %w", err)
    }

    sqlDB.SetMaxIdleConns(10)
    sqlDB.SetMaxOpenConns(100)
    sqlDB.SetConnMaxLifetime(time.Hour)

    log.Println("Database connection established")
    return DB, nil
}

func AutoMigrate(db *gorm.DB) error {
    err := db.AutoMigrate(
        &model.User{},
        &model.Role{},
        &model.Permission{},
        &model.UserRole{},
        &model.RolePermission{},
        &model.Agent{},
        &model.AgentVersion{},
        &model.AgentInstance{},
        &model.AgentLog{},
        &model.AgentAPIKey{},
        &model.AgentCallStat{},
        &model.MCPServer{},
        &model.MCPHealthLog{},
        &model.MCPAgentBinding{},
        &model.ModelTemplate{},
        &model.ModelQuota{},
        &model.ModelUsageLog{},
        &model.ModelHealthLog{},
        &model.ToolApproval{},
        &model.ApprovalSettings{},
        &model.AuditLog{},
        &model.ChatSession{},
        &model.ChatMessage{},
        &model.Workflow{},
        &model.WorkflowVersion{},
        &model.WorkflowExecution{},
        &model.WorkflowNodeExecution{},
        &model.Skill{},
        &model.SkillFile{},
        &model.SkillAgentBinding{},
    )
    if err != nil {
        return fmt.Errorf("failed to auto migrate: %w", err)
    }

    // skills.name 的唯一约束由部分唯一索引 uk_skills_name_alive 承担 (软删除友好);
    // 旧版本库上可能残留 GORM 全量唯一索引 idx_skills_name (会阻止同名重建), 先移除
    _ = db.Exec("DROP INDEX IF EXISTS idx_skills_name").Error

    // Soft-deleted rows still occupy the plain unique index on name/username/email,
    // so recreating an entity with the same name after deletion violates the unique
    // constraint (SQLSTATE 23505). Replace them with partial unique indexes that
    // only cover non-deleted rows. Idempotent: safe to run on every startup.
    partialUniqueSQLs := []string{
        "DROP INDEX IF EXISTS idx_workflows_name",
        "CREATE UNIQUE INDEX IF NOT EXISTS uk_workflows_name_alive ON workflows (name) WHERE deleted_at IS NULL",
        "DROP INDEX IF EXISTS idx_agents_name",
        "CREATE UNIQUE INDEX IF NOT EXISTS uk_agents_name_alive ON agents (name) WHERE deleted_at IS NULL",
        "DROP INDEX IF EXISTS idx_mcp_servers_name",
        "CREATE UNIQUE INDEX IF NOT EXISTS uk_mcp_servers_name_alive ON mcp_servers (name) WHERE deleted_at IS NULL",
        "DROP INDEX IF EXISTS idx_model_templates_name",
        "CREATE UNIQUE INDEX IF NOT EXISTS uk_model_templates_name_alive ON model_templates (name) WHERE deleted_at IS NULL",
        "DROP INDEX IF EXISTS idx_roles_name",
        "CREATE UNIQUE INDEX IF NOT EXISTS uk_roles_name_alive ON roles (name) WHERE deleted_at IS NULL",
        "DROP INDEX IF EXISTS idx_users_username",
        "CREATE UNIQUE INDEX IF NOT EXISTS uk_users_username_alive ON users (username) WHERE deleted_at IS NULL",
        "DROP INDEX IF EXISTS idx_users_email",
        "CREATE UNIQUE INDEX IF NOT EXISTS uk_users_email_alive ON users (email) WHERE deleted_at IS NULL",
        "DROP INDEX IF EXISTS idx_skills_name",
        "CREATE UNIQUE INDEX IF NOT EXISTS uk_skills_name_alive ON skills (name) WHERE deleted_at IS NULL",
    }
    for _, stmt := range partialUniqueSQLs {
        if err := db.Exec(stmt).Error; err != nil {
            return fmt.Errorf("failed to apply partial unique index migration: %w", err)
        }
    }

    log.Println("Database migration completed")
    return nil
}