package repository

import (
	"context"
	"fmt"

	"agent-platform/internal/database"
	"agent-platform/internal/model"

	"gorm.io/gorm"
)

type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	// CreateWithRoles 创建用户并分配角色 (事务), roleNames 为角色名
	CreateWithRoles(ctx context.Context, user *model.User, roleNames []string) error
	GetByUsername(ctx context.Context, username string) (*model.User, error)
	GetByID(ctx context.Context, id string) (*model.User, error)
	ListRoleNames(ctx context.Context, userID string) ([]string, error)
	List(ctx context.Context, filter UserListFilter) ([]UserWithRoles, int64, error)
	UpdateProfile(ctx context.Context, id string, email *string, status *int8) error
	UpdatePassword(ctx context.Context, id, hashedPassword string) error
	Delete(ctx context.Context, id string) error
	// AssignRoles 全量替换用户角色 (事务), roleIDs 为空则清空
	AssignRoles(ctx context.Context, userID string, roleIDs []string) error
	// CountActiveUsersWithRole 统计拥有指定角色名的活动用户数 (删除保护用)
	CountActiveUsersWithRole(ctx context.Context, roleName string) (int64, error)
}

type userRepository struct {
	db *gorm.DB
}

func NewUserRepository() UserRepository {
	return &userRepository{db: database.DB}
}

func (r *userRepository) Create(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *userRepository) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepository) GetByID(ctx context.Context, id string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserRoles 获取用户角色
func GetUserRoles(db *gorm.DB, userID string) ([]string, error) {
	var roles []string
	err := db.Table("roles").
		Joins("JOIN user_roles ON user_roles.role_id = roles.id").
		Where("user_roles.user_id = ?", userID).
		Pluck("roles.name", &roles).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get user roles: %w", err)
	}
	return roles, nil
}
