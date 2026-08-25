package model

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID        string         `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Username  string         `gorm:"type:varchar(64);not null" json:"username"`
	Email     *string        `gorm:"type:varchar(128)" json:"email"`
	Password  string         `gorm:"type:varchar(256);not null" json:"-"`
	Status    int8           `gorm:"default:1" json:"status"` // 1:active 0:disabled
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (User) TableName() string {
	return "users"
}
