package service

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"agent-platform/internal/database"
	"agent-platform/internal/middleware"
	"agent-platform/internal/model"
	"agent-platform/internal/repository"
	"agent-platform/pkg/errors"

	"golang.org/x/crypto/bcrypt"
)

// 内置角色: 不可删除, admin 权限更新时强制保留关键权限 (防锁死)
var builtinRoleNames = map[string]struct{}{
	"admin":    {},
	"operator": {},
	"user":     {},
}

// adminUpdateProtectedPerms admin 角色权限更新时强制保留, 防止管理员把自己锁在管理界面外
var adminUpdateProtectedPerms = []string{"user:manage", "role:manage", "mcp:approve"}

// ---------- 请求类型 ----------

type CreateUserRequest struct {
	Username string   `json:"username" binding:"required,min=2,max=64"`
	Email    string   `json:"email" binding:"omitempty,email"`
	Password string   `json:"password" binding:"required,min=6,max=128"`
	Roles    []string `json:"roles"`
}

type UpdateUserRequest struct {
	Email    *string `json:"email"`
	Status   *int8   `json:"status" binding:"omitempty,oneof=0 1"`
	Password *string `json:"password" binding:"omitempty,min=6,max=128"`
}

type AssignUserRolesRequest struct {
	Roles []string `json:"roles"`
}

type CreateRoleRequest struct {
	Name        string   `json:"name" binding:"required,min=2,max=64"`
	Description string   `json:"description" binding:"max=512"`
	Permissions []string `json:"permissions"`
}

type UpdateRoleRequest struct {
	Description *string  `json:"description" binding:"omitempty,max=512"`
	Status      *int8    `json:"status" binding:"omitempty,oneof=0 1"`
	Permissions *[]string `json:"permissions"`
}

// MeResult 当前登录用户信息 (含角色与权限)
type MeResult struct {
	User        model.User `json:"user"`
	Roles       []string   `json:"roles"`
	Permissions []string   `json:"permissions"`
}

// RBACService 用户/角色/权限管理 (M1 遗留: RBAC 落地 + 管理 API)
type RBACService struct {
	users  repository.UserRepository
	roles  repository.RoleRepository
	audits repository.AuditLogRepository
}

func NewRBACService(users repository.UserRepository, roles repository.RoleRepository, audits repository.AuditLogRepository) *RBACService {
	return &RBACService{users: users, roles: roles, audits: audits}
}

// ---------- 用户 ----------

func (s *RBACService) ListUsers(ctx context.Context, filter repository.UserListFilter) ([]repository.UserWithRoles, int64, error) {
	return s.users.List(ctx, filter)
}

// GetUser 用户详情 (含角色名)
func (s *RBACService) GetUser(ctx context.Context, id string) (*repository.UserWithRoles, error) {
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, mapRBACError(err)
	}
	roles, err := s.users.ListRoleNames(ctx, id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user roles")
	}
	return &repository.UserWithRoles{User: *user, Roles: roles}, nil
}

func (s *RBACService) CreateUser(ctx context.Context, req CreateUserRequest, actorID, actorName, ip string) (*model.User, error) {
	roleNames := req.Roles
	if len(roleNames) == 0 {
		roleNames = []string{"user"}
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, errors.Wrap(err, "failed to hash password")
	}
	email := req.Email
	user := &model.User{
		Username: req.Username,
		Password: string(hashed),
		Status:   1,
	}
	if email != "" {
		user.Email = &email
	}

	if err := s.users.CreateWithRoles(ctx, user, roleNames); err != nil {
		return nil, mapRBACError(err)
	}
	middleware.InvalidatePermissionCache(user.ID)
	s.audit(ctx, &actorID, actorName, "user.create", "user", &user.ID, ip, map[string]interface{}{
		"username": user.Username,
		"roles":    roleNames,
	})
	return user, nil
}

func (s *RBACService) UpdateUser(ctx context.Context, id string, req UpdateUserRequest, actorID, actorName, ip string) (*model.User, error) {
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, mapRBACError(err)
	}

	if err := s.users.UpdateProfile(ctx, id, req.Email, req.Status); err != nil {
		return nil, mapRBACError(err)
	}
	if req.Password != nil && *req.Password != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, errors.Wrap(err, "failed to hash password")
		}
		if err := s.users.UpdatePassword(ctx, id, string(hashed)); err != nil {
			return nil, mapRBACError(err)
		}
	}

	middleware.InvalidatePermissionCache(id)
	s.audit(ctx, &actorID, actorName, "user.update", "user", &id, ip, map[string]interface{}{
		"username": user.Username,
		"changed":  changedFields(req),
	})
	return s.users.GetByID(ctx, id)
}

func (s *RBACService) AssignUserRoles(ctx context.Context, id string, roleNames []string, actorID, actorName, ip string) ([]string, error) {
	if _, err := s.users.GetByID(ctx, id); err != nil {
		return nil, mapRBACError(err)
	}
	if len(roleNames) == 0 {
		roleNames = []string{}
	}
	roleIDs, err := s.roleIDsByNames(ctx, roleNames)
	if err != nil {
		return nil, errors.NewValidationError(err.Error())
	}
	if err := s.users.AssignRoles(ctx, id, roleIDs); err != nil {
		return nil, errors.Wrap(err, "failed to assign roles")
	}
	middleware.InvalidatePermissionCache(id)
	s.audit(ctx, &actorID, actorName, "user.assign_roles", "user", &id, ip, map[string]interface{}{
		"roles": roleNames,
	})
	return roleNames, nil
}

func (s *RBACService) DeleteUser(ctx context.Context, id, actorID, actorName, ip string) error {
	if id == actorID {
		return errors.NewValidationError("不能删除当前登录用户")
	}
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		return mapRBACError(err)
	}

	// 最后一个管理员保护: 目标是 admin 且活跃 admin 用户只有 ta 自己时禁止删除
	if s.hasRoleName(ctx, user, "admin") {
		adminCount, err := s.users.CountActiveUsersWithRole(ctx, "admin")
		if err != nil {
			return errors.Wrap(err, "failed to count admin users")
		}
		if adminCount <= 1 {
			return errors.NewValidationError("不能删除最后一个管理员")
		}
	}

	if err := s.users.Delete(ctx, id); err != nil {
		return mapRBACError(err)
	}
	middleware.InvalidatePermissionCache(id)
	s.audit(ctx, &actorID, actorName, "user.delete", "user", &id, ip, map[string]interface{}{
		"username": user.Username,
	})
	return nil
}

// hasRoleName 用户是否拥有指定角色 (通过 user_roles 关联判断)
func (s *RBACService) hasRoleName(ctx context.Context, user *model.User, roleName string) bool {
	names, err := s.users.ListRoleNames(ctx, user.ID)
	if err != nil {
		return false
	}
	for _, n := range names {
		if n == roleName {
			return true
		}
	}
	return false
}

// ---------- 角色 ----------

func (s *RBACService) ListRoles(ctx context.Context) ([]repository.RoleWithPermissions, error) {
	return s.roles.List(ctx)
}

func (s *RBACService) CreateRole(ctx context.Context, req CreateRoleRequest, actorID, actorName, ip string) (*model.Role, error) {
	permIDs, err := s.permissionIDsByCodes(ctx, req.Permissions)
	if err != nil {
		return nil, errors.NewValidationError(err.Error())
	}
	role := &model.Role{Name: req.Name, Description: req.Description, Status: 1}
	if err := s.roles.Create(ctx, role, permIDs); err != nil {
		return nil, mapRBACError(err)
	}
	middleware.InvalidatePermissionCache("")
	s.audit(ctx, &actorID, actorName, "role.create", "role", &role.ID, ip, map[string]interface{}{
		"name":        role.Name,
		"permissions": req.Permissions,
	})
	return role, nil
}

func (s *RBACService) UpdateRole(ctx context.Context, id string, req UpdateRoleRequest, actorID, actorName, ip string) (*model.Role, error) {
	role, err := s.roles.GetByID(ctx, id)
	if err != nil {
		return nil, mapRBACError(err)
	}

	if req.Description != nil {
		role.Description = *req.Description
	}
	if req.Status != nil {
		role.Status = *req.Status
	}
	if err := s.roles.Update(ctx, role); err != nil {
		return nil, mapRBACError(err)
	}

	changedPerm := false
	if req.Permissions != nil {
		codes := *req.Permissions
		// admin 角色强制保留关键权限, 防止锁死
		if role.Name == "admin" {
			codes = mergeProtectedPerms(codes, adminUpdateProtectedPerms)
		}
		permIDs, err := s.permissionIDsByCodes(ctx, codes)
		if err != nil {
			return nil, errors.NewValidationError(err.Error())
		}
		if err := s.roles.SetPermissions(ctx, id, permIDs); err != nil {
			return nil, errors.Wrap(err, "failed to update role permissions")
		}
		changedPerm = true
	}

	middleware.InvalidatePermissionCache("")
	s.audit(ctx, &actorID, actorName, "role.update", "role", &id, ip, map[string]interface{}{
		"name":            role.Name,
		"permissions":     req.Permissions,
		"permissions_set": changedPerm,
	})
	return s.roles.GetByID(ctx, id)
}

func (s *RBACService) DeleteRole(ctx context.Context, id string, actorID, actorName, ip string) error {
	role, err := s.roles.GetByID(ctx, id)
	if err != nil {
		return mapRBACError(err)
	}
	if _, ok := builtinRoleNames[role.Name]; ok {
		return errors.ErrBuiltinRole
	}
	if err := s.roles.Delete(ctx, id); err != nil {
		return mapRBACError(err)
	}
	middleware.InvalidatePermissionCache("")
	s.audit(ctx, &actorID, actorName, "role.delete", "role", &id, ip, map[string]interface{}{
		"name": role.Name,
	})
	return nil
}

// ---------- 权限 ----------

func (s *RBACService) ListPermissions(ctx context.Context) ([]model.Permission, error) {
	return s.roles.ListPermissions(ctx)
}

// Me 当前登录用户的角色与权限 (供前端做菜单/按钮级控制)
func (s *RBACService) Me(ctx context.Context, userID string) (*MeResult, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, mapRBACError(err)
	}
	roles, err := repository.GetUserRoles(database.DB, userID)
	if err != nil {
		roles = []string{}
	}
	perms, err := repository.GetUserPermissionCodes(ctx, userID)
	if err != nil {
		perms = []string{}
	}
	return &MeResult{User: *user, Roles: roles, Permissions: perms}, nil
}

// ---------- 内部工具 ----------

func (s *RBACService) roleIDsByNames(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	roleIDs := make([]string, 0, len(names))
	for _, name := range names {
		role, err := s.roles.GetByName(ctx, name)
		if err != nil {
			return nil, errUnknownRole(name)
		}
		roleIDs = append(roleIDs, role.ID)
	}
	return roleIDs, nil
}

func (s *RBACService) permissionIDsByCodes(ctx context.Context, codes []string) ([]string, error) {
	if len(codes) == 0 {
		return nil, nil
	}
	all, err := s.roles.ListPermissions(ctx)
	if err != nil {
		return nil, err
	}
	idByCode := make(map[string]string, len(all))
	for _, p := range all {
		idByCode[p.Code] = p.ID
	}
	ids := make([]string, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		id, ok := idByCode[code]
		if !ok {
			return nil, errUnknownPermission(code)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func mergeProtectedPerms(codes, protected []string) []string {
	set := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		set[c] = struct{}{}
	}
	merged := make([]string, 0, len(codes)+len(protected))
	for _, c := range codes {
		merged = append(merged, c)
	}
	for _, p := range protected {
		if _, ok := set[p]; !ok {
			merged = append(merged, p)
		}
	}
	return merged
}

// mapRBACError 仓储层错误 -> 应用层错误 (唯一约束冲突/记录不存在等)
func mapRBACError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "23505"), strings.Contains(msg, "duplicate key"):
		return errors.NewValidationError("名称已存在")
	case strings.Contains(msg, "record not found"):
		return errors.ErrNotFound
	default:
		return err
	}
}

func errUnknownRole(name string) error {
	return &errors.AppError{Code: "validation_error", Message: "角色不存在: " + name, HTTPCode: 400}
}

func errUnknownPermission(code string) error {
	return &errors.AppError{Code: "validation_error", Message: "权限不存在: " + code, HTTPCode: 400}
}

func changedFields(req UpdateUserRequest) []string {
	var fields []string
	if req.Email != nil {
		fields = append(fields, "email")
	}
	if req.Status != nil {
		fields = append(fields, "status")
	}
	if req.Password != nil && *req.Password != "" {
		fields = append(fields, "password")
	}
	return fields
}

// audit 写审计日志 (失败仅告警, 不阻塞主流程)
func (s *RBACService) audit(ctx context.Context, userID *string, username, action, resource string, resourceID *string, ip string, detail map[string]interface{}) {
	if s.audits == nil {
		return
	}
	payload, _ := json.Marshal(detail)
	entry := &model.AuditLog{
		UserID:     userID,
		Username:   username,
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		Detail:     payload,
		IP:         ip,
	}
	if err := s.audits.Append(ctx, entry); err != nil {
		log.Printf("rbac: audit append failed action=%s: %v", action, err)
	}
}
