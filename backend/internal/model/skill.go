package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// 技能状态
const (
	SkillStatusActive   = "active"   // 启用 (运行时注入)
	SkillStatusDisabled = "disabled" // 禁用 (运行时不注入, 关联保留)
)

// Skill 技能包 (M9, PRD 5.10)
type Skill struct {
	ID            string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name          string         `gorm:"type:varchar(64);not null" json:"name"` // 全局唯一 (部分唯一索引 uk_skills_name_alive, 见 database.go) // 全局唯一 (部分唯一索引见 database.go)
	Version       int            `gorm:"not null;default:1" json:"version"`     // 每次升级 +1
	VersionSpec   string         `gorm:"type:varchar(32)" json:"version_spec"`  // 包内声明版本号 (semver)
	Description   string         `gorm:"type:text" json:"description"`
	Author        string         `gorm:"type:varchar(64)" json:"author"`
	Tags          datatypes.JSON `gorm:"type:jsonb;default:'[]'" json:"tags"`
	RequiredTools datatypes.JSON `gorm:"type:jsonb;default:'[]'" json:"required_tools"`
	EntryContent  string         `gorm:"type:text;not null" json:"entry_content"` // SKILL.md 指令正文 (不含 frontmatter)
	SizeBytes     int64          `gorm:"not null;default:0" json:"size_bytes"`
	FileCount     int            `gorm:"not null;default:0" json:"file_count"`
	Status        string         `gorm:"type:varchar(16);not null;default:'active';index" json:"status"`
	CreatedBy     *string        `gorm:"type:uuid" json:"created_by"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Skill) TableName() string {
	return "skills"
}

// SkillFile 技能包资源文件 (M9, PRD 5.11); MVP 存库, 后续可迁移对象存储
type SkillFile struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	SkillID   string    `gorm:"type:uuid;not null;uniqueIndex:idx_skill_file,priority:1" json:"skill_id"`
	Path      string    `gorm:"type:varchar(512);not null;uniqueIndex:idx_skill_file,priority:2" json:"path"` // 包内相对路径
	Size      int64     `gorm:"not null;default:0" json:"size"`
	Sha256    string    `gorm:"type:varchar(64)" json:"sha256"`
	Content   []byte    `gorm:"type:bytea" json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

func (SkillFile) TableName() string {
	return "skill_files"
}

// SkillAgentBinding 技能 <-> Agent 关联 (M9, PRD 5.12), 表结构与 MCPAgentBinding 对齐
type SkillAgentBinding struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	SkillID   string    `gorm:"type:uuid;not null;uniqueIndex:idx_skill_agent,priority:1" json:"skill_id"`
	AgentID   string    `gorm:"type:uuid;not null;uniqueIndex:idx_skill_agent,priority:2" json:"agent_id"`
	CreatedBy *string   `gorm:"type:uuid" json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

func (SkillAgentBinding) TableName() string {
	return "skill_agent_bindings"
}
