package repository

import (
	"context"
	"errors"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
	apperrors "agent-platform/pkg/errors"

	"gorm.io/gorm"
)

// UserListFilter 用户列表过滤条件
type UserListFilter struct {
	Keyword  string // username/email 模糊匹配
	Status   *int8  // nil = 不过滤
	Page     int
	PageSize int
}

// UserWithRoles 用户及其角色名 (列表展示用)
type UserWithRoles struct {
	model.User
	Roles []string `json:"roles"`
}

// RoleWithPermissions 角色及其权限码/用户数 (列表展示用)
type RoleWithPermissions struct {
	model.Role
	Permissions []string `json:"permissions"`
	UserCount   int64    `json:"user_count"`
}

func (r *userRepository) CreateWithRoles(ctx context.Context, user *model.User, roleNames []string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		roleIDs, err := roleIDsByNames(tx, roleNames)
		if err != nil {
			return err
		}
		rows := make([]model.UserRole, 0, len(roleIDs))
		for _, rid := range roleIDs {
			rows = append(rows, model.UserRole{UserID: user.ID, RoleID: rid})
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *userRepository) List(ctx context.Context, filter UserListFilter) ([]UserWithRoles, int64, error) {
	db := database.DB.WithContext(ctx).Model(&model.User{})
	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		db = db.Where("username LIKE ? OR email LIKE ?", like, like)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
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

	var users []model.User
	if err := db.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	// 批量取当前页用户的角色名, 避免 N+1
	roleNamesByUser := make(map[string][]string, len(users))
	if len(users) > 0 {
		userIDs := make([]string, 0, len(users))
		for _, u := range users {
			userIDs = append(userIDs, u.ID)
		}
		var urRows []model.UserRole
		if err := database.DB.WithContext(ctx).Where("user_id IN ?", userIDs).Find(&urRows).Error; err != nil {
			return nil, 0, err
		}
		roleIDSet := make(map[string]struct{})
		for _, ur := range urRows {
			roleIDSet[ur.RoleID] = struct{}{}
		}
		roleIDs := make([]string, 0, len(roleIDSet))
		for rid := range roleIDSet {
			roleIDs = append(roleIDs, rid)
		}
		roleNameByID := make(map[string]string, len(roleIDs))
		if len(roleIDs) > 0 {
			var roleRows []model.Role
			if err := database.DB.WithContext(ctx).Where("id IN ?", roleIDs).Find(&roleRows).Error; err != nil {
				return nil, 0, err
			}
			for _, rr := range roleRows {
				roleNameByID[rr.ID] = rr.Name
			}
		}
		for _, ur := range urRows {
			if name, ok := roleNameByID[ur.RoleID]; ok {
				roleNamesByUser[ur.UserID] = append(roleNamesByUser[ur.UserID], name)
			}
		}
	}

	items := make([]UserWithRoles, 0, len(users))
	for _, u := range users {
		items = append(items, UserWithRoles{User: u, Roles: roleNamesByUser[u.ID]})
	}
	return items, total, nil
}

func (r *userRepository) ListRoleNames(ctx context.Context, userID string) ([]string, error) {
	var names []string
	err := database.DB.WithContext(ctx).
		Table("user_roles ur").
		Joins("JOIN roles rl ON rl.id = ur.role_id AND rl.deleted_at IS NULL").
		Where("ur.user_id = ?", userID).
		Pluck("rl.name", &names).Error
	return names, err
}

func (r *userRepository) UpdateProfile(ctx context.Context, id string, email *string, status *int8) error {
	assignments := map[string]interface{}{}
	if email != nil {
		assignments["email"] = email
	}
	if status != nil {
		assignments["status"] = *status
	}
	if len(assignments) == 0 {
		return nil
	}
	res := database.DB.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Updates(assignments)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *userRepository) UpdatePassword(ctx context.Context, id, hashedPassword string) error {
	res := database.DB.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", id).Update("password", hashedPassword)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Delete 软删除用户并硬删其角色关联 (join 表无软删, 避免孤儿数据)
func (r *userRepository) Delete(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&model.UserRole{}).Error; err != nil {
			return err
		}
		res := tx.Where("id = ?", id).Delete(&model.User{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (r *userRepository) AssignRoles(ctx context.Context, userID string, roleIDs []string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserRole{}).Error; err != nil {
			return err
		}
		rows := make([]model.UserRole, 0, len(roleIDs))
		for _, rid := range roleIDs {
			rows = append(rows, model.UserRole{UserID: userID, RoleID: rid})
		}
		if len(rows) > 0 {
			return tx.Create(&rows).Error
		}
		return nil
	})
}

// CountActiveUsersWithRole 统计拥有指定角色名的活动用户数 (join 表查询, 需手工排除软删行)
func (r *userRepository) CountActiveUsersWithRole(ctx context.Context, roleName string) (int64, error) {
	var count int64
	err := database.DB.WithContext(ctx).
		Raw("SELECT COUNT(DISTINCT ur.user_id) FROM user_roles ur "+
			"JOIN roles rl ON rl.id = ur.role_id AND rl.deleted_at IS NULL "+
			"JOIN users u ON u.id = ur.user_id AND u.deleted_at IS NULL AND u.status = 1 "+
			"WHERE rl.name = ?", roleName).
		Scan(&count).Error
	return count, err
}

// GetUserPermissionCodes 用户有效权限码 (user_roles + role_permissions + permissions 联表)
func GetUserPermissionCodes(ctx context.Context, userID string) ([]string, error) {
	var perms []string
	err := database.DB.WithContext(ctx).
		Model(&model.UserRole{}).
		Joins("JOIN role_permissions ON role_permissions.role_id = user_roles.role_id").
		Joins("JOIN permissions ON permissions.id = role_permissions.permission_id").
		Where("user_roles.user_id = ?", userID).
		Distinct().
		Order("permissions.code ASC").
		Pluck("permissions.code", &perms).Error
	if err != nil {
		return nil, err
	}
	return perms, nil
}

// RoleRepository 角色与权限仓储
type RoleRepository interface {
	List(ctx context.Context) ([]RoleWithPermissions, error)
	GetByID(ctx context.Context, id string) (*model.Role, error)
	GetByName(ctx context.Context, name string) (*model.Role, error)
	Create(ctx context.Context, role *model.Role, permissionIDs []string) error
	Update(ctx context.Context, role *model.Role) error
	// SetPermissions 全量替换角色权限 (事务)
	SetPermissions(ctx context.Context, roleID string, permissionIDs []string) error
	Delete(ctx context.Context, id string) error
	ListPermissions(ctx context.Context) ([]model.Permission, error)
	ListPermissionCodesByRole(ctx context.Context, roleID string) ([]string, error)
}

type roleRepository struct{}

func NewRoleRepository() RoleRepository {
	return &roleRepository{}
}

func (r *roleRepository) List(ctx context.Context) ([]RoleWithPermissions, error) {
	var roles []model.Role
	if err := database.DB.WithContext(ctx).Order("created_at ASC").Find(&roles).Error; err != nil {
		return nil, err
	}
	if len(roles) == 0 {
		return []RoleWithPermissions{}, nil
	}
	roleIDs := make([]string, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
	}

	// 角色权限码
	var rpRows []model.RolePermission
	if err := database.DB.WithContext(ctx).Where("role_id IN ?", roleIDs).Find(&rpRows).Error; err != nil {
		return nil, err
	}
	permIDSet := make(map[string]struct{})
	for _, rp := range rpRows {
		permIDSet[rp.PermissionID] = struct{}{}
	}
	permIDs := make([]string, 0, len(permIDSet))
	for pid := range permIDSet {
		permIDs = append(permIDs, pid)
	}
	codeByPermID := make(map[string]string, len(permIDs))
	if len(permIDs) > 0 {
		var permRows []model.Permission
		if err := database.DB.WithContext(ctx).Where("id IN ?", permIDs).Find(&permRows).Error; err != nil {
			return nil, err
		}
		for _, p := range permRows {
			codeByPermID[p.ID] = p.Code
		}
	}
	permsByRole := make(map[string][]string)
	for _, rp := range rpRows {
		if code, ok := codeByPermID[rp.PermissionID]; ok {
			permsByRole[rp.RoleID] = append(permsByRole[rp.RoleID], code)
		}
	}

	// 角色用户数 (仅活动用户, 手工排除软删)
	var countRows []struct {
		RoleID    string
		UserCount int64
	}
	if err := database.DB.WithContext(ctx).
		Raw("SELECT ur.role_id, COUNT(DISTINCT ur.user_id) AS user_count FROM user_roles ur "+
			"JOIN users u ON u.id = ur.user_id AND u.deleted_at IS NULL "+
			"WHERE ur.role_id IN ? GROUP BY ur.role_id", roleIDs).
		Scan(&countRows).Error; err != nil {
		return nil, err
	}
	countByRole := make(map[string]int64, len(countRows))
	for _, cr := range countRows {
		countByRole[cr.RoleID] = cr.UserCount
	}

	items := make([]RoleWithPermissions, 0, len(roles))
	for _, role := range roles {
		items = append(items, RoleWithPermissions{
			Role:        role,
			Permissions: permsByRole[role.ID],
			UserCount:   countByRole[role.ID],
		})
	}
	return items, nil
}

func (r *roleRepository) GetByID(ctx context.Context, id string) (*model.Role, error) {
	var role model.Role
	if err := database.DB.WithContext(ctx).First(&role, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		return nil, err
	}
	return &role, nil
}

func (r *roleRepository) GetByName(ctx context.Context, name string) (*model.Role, error) {
	var role model.Role
	if err := database.DB.WithContext(ctx).Where("name = ?", name).First(&role).Error; err != nil {
		return nil, err
	}
	return &role, nil
}

func (r *roleRepository) Create(ctx context.Context, role *model.Role, permissionIDs []string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(role).Error; err != nil {
			return err
		}
		return createRolePermissions(tx, role.ID, permissionIDs)
	})
}

func (r *roleRepository) Update(ctx context.Context, role *model.Role) error {
	res := database.DB.WithContext(ctx).Model(&model.Role{}).
		Where("id = ?", role.ID).
		Updates(map[string]interface{}{"description": role.Description, "status": role.Status})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *roleRepository) SetPermissions(ctx context.Context, roleID string, permissionIDs []string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&model.RolePermission{}).Error; err != nil {
			return err
		}
		return createRolePermissions(tx, roleID, permissionIDs)
	})
}

func (r *roleRepository) Delete(ctx context.Context, id string) error {
	return database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 有用户绑定的角色禁止删除
		var count int64
		if err := tx.Model(&model.UserRole{}).Where("role_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return apperrors.ErrRoleInUse
		}
		if err := tx.Where("role_id = ?", id).Delete(&model.RolePermission{}).Error; err != nil {
			return err
		}
		res := tx.Where("id = ?", id).Delete(&model.Role{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (r *roleRepository) ListPermissions(ctx context.Context) ([]model.Permission, error) {
	var perms []model.Permission
	if err := database.DB.WithContext(ctx).Order("resource ASC, action ASC").Find(&perms).Error; err != nil {
		return nil, err
	}
	return perms, nil
}

func (r *roleRepository) ListPermissionCodesByRole(ctx context.Context, roleID string) ([]string, error) {
	var codes []string
	err := database.DB.WithContext(ctx).
		Model(&model.RolePermission{}).
		Joins("JOIN permissions ON permissions.id = role_permissions.permission_id").
		Where("role_permissions.role_id = ?", roleID).
		Distinct().
		Pluck("permissions.code", &codes).Error
	return codes, err
}

func createRolePermissions(tx *gorm.DB, roleID string, permissionIDs []string) error {
	if len(permissionIDs) == 0 {
		return nil
	}
	rows := make([]model.RolePermission, 0, len(permissionIDs))
	for _, pid := range permissionIDs {
		rows = append(rows, model.RolePermission{RoleID: roleID, PermissionID: pid})
	}
	return tx.Create(&rows).Error
}

// roleIDsByNames 角色名 -> ID 映射, 存在未知角色名时报错
func roleIDsByNames(db *gorm.DB, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	var roles []model.Role
	if err := db.Where("name IN ?", names).Find(&roles).Error; err != nil {
		return nil, err
	}
	if len(roles) != len(names) {
		found := make(map[string]struct{}, len(roles))
		for _, r := range roles {
			found[r.Name] = struct{}{}
		}
		for _, n := range names {
			if _, ok := found[n]; !ok {
				return nil, errors.New("unknown role: " + n)
			}
		}
	}
	ids := make([]string, 0, len(roles))
	for _, r := range roles {
		ids = append(ids, r.ID)
	}
	return ids, nil
}
