package model

import (
	"time"

	"gorm.io/gorm"
)

type Role struct {
	ID          string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Name        string         `gorm:"type:varchar(64);not null" json:"name"`
	Description string         `gorm:"type:text" json:"description"`
	Status      int8           `gorm:"default:1" json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Role) TableName() string {
	return "roles"
}

type Permission struct {
	ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Code      string    `gorm:"type:varchar(128);uniqueIndex;not null" json:"code"`
	Name      string    `gorm:"type:varchar(64);not null" json:"name"`
	Resource  string    `gorm:"type:varchar(64);not null" json:"resource"`
	Action    string    `gorm:"type:varchar(32);not null" json:"action"`
	CreatedAt time.Time `json:"created_at"`
}

func (Permission) TableName() string {
	return "permissions"
}

type UserRole struct {
	ID     string `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID string `gorm:"type:uuid;not null;index" json:"user_id"`
	RoleID string `gorm:"type:uuid;not null;index" json:"role_id"`
}

func (UserRole) TableName() string {
	return "user_roles"
}

type RolePermission struct {
	ID           string `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	RoleID       string `gorm:"type:uuid;not null;index" json:"role_id"`
	PermissionID string `gorm:"type:uuid;not null;index" json:"permission_id"`
}

func (RolePermission) TableName() string {
	return "role_permissions"
}
