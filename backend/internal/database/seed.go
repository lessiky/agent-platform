package database

import (
	"log"

	"agent-platform/internal/model"

	"gorm.io/gorm"
)

// SeedPermissionsAndRoles 权限与角色种子数据 (幂等)
//
// RBAC 真实落地 (M1/M2/M3/M4/M4.5 遗留): middleware 按 user_roles/role_permissions
// 查询权限, 种子维护权限点与内置角色:
//   - admin 角色: 全部权限 (含 mcp:approve / user:manage / role:manage)
//   - operator 角色: 业务读写权限 (不含 mcp:approve / user:manage / role:manage)
//   - user 角色: 默认只读角色 (注册新用户自动分配)
//   - demo_user 自动分配 admin 角色; 存量无角色用户自动补 user 角色
func SeedPermissionsAndRoles(db *gorm.DB) error {
	permissions := []model.Permission{
		{Code: "agent:read", Name: "Agent 读", Resource: "agent", Action: "read"},
		{Code: "agent:write", Name: "Agent 写", Resource: "agent", Action: "write"},
		{Code: "mcp:read", Name: "MCP 读", Resource: "mcp", Action: "read"},
		{Code: "mcp:write", Name: "MCP 写", Resource: "mcp", Action: "write"},
		{Code: "mcp:approve", Name: "MCP 工具审批", Resource: "mcp", Action: "approve"},
		{Code: "model:read", Name: "模型读", Resource: "model", Action: "read"},
		{Code: "model:write", Name: "模型写", Resource: "model", Action: "write"},
		{Code: "workflow:read", Name: "工作流读", Resource: "workflow", Action: "read"},
		{Code: "workflow:write", Name: "工作流写", Resource: "workflow", Action: "write"},
		{Code: "workflow:execute", Name: "工作流执行", Resource: "workflow", Action: "execute"},
		{Code: "skill:read", Name: "技能读", Resource: "skill", Action: "read"},
		{Code: "skill:write", Name: "技能写", Resource: "skill", Action: "write"},
		{Code: "user:manage", Name: "用户管理", Resource: "user", Action: "manage"},
		{Code: "role:manage", Name: "角色管理", Resource: "role", Action: "manage"},
	}
	for i := range permissions {
		if err := db.Where(&model.Permission{Code: permissions[i].Code}).
			Assign(model.Permission{Name: permissions[i].Name, Resource: permissions[i].Resource, Action: permissions[i].Action}).
			FirstOrCreate(&permissions[i]).Error; err != nil {
			return err
		}
	}

	permByID := make(map[string]model.Permission, len(permissions))
	for _, p := range permissions {
		permByID[p.Code] = p
	}

	roles := []model.Role{
		{Name: "admin", Description: "管理员 (含 MCP 工具审批权限)", Status: 1},
		{Name: "operator", Description: "运营 (业务读写, 不含审批与用户/角色管理)", Status: 1},
		{Name: "user", Description: "默认角色 (只读)", Status: 1},
	}
	for i := range roles {
		if err := db.Where(&model.Role{Name: roles[i].Name}).
			Assign(model.Role{Description: roles[i].Description, Status: 1}).
			FirstOrCreate(&roles[i]).Error; err != nil {
			return err
		}
	}
	roleByName := make(map[string]model.Role, len(roles))
	for _, r := range roles {
		roleByName[r.Name] = r
	}

	// 角色-权限映射 (只增不删, 避免启动时移除管理员刚在 UI 取消的权限)
	rolePermCodes := map[string][]string{}
	for _, perm := range permissions {
		rolePermCodes["admin"] = append(rolePermCodes["admin"], perm.Code)
		if perm.Code != "mcp:approve" && perm.Code != "user:manage" && perm.Code != "role:manage" {
			rolePermCodes["operator"] = append(rolePermCodes["operator"], perm.Code)
		}
		switch perm.Code {
		case "agent:read", "mcp:read", "model:read", "workflow:read", "skill:read":
			rolePermCodes["user"] = append(rolePermCodes["user"], perm.Code)
		}
	}
	for roleName, codes := range rolePermCodes {
		role := roleByName[roleName]
		for _, code := range codes {
			if err := ensureRolePermission(db, role.ID, permByID[code].ID); err != nil {
				return err
			}
		}
	}

	// demo_user 分配 admin 角色
	var demo model.User
	if err := db.Where("username = ?", "demo_user").First(&demo).Error; err == nil {
		var count int64
		if err := db.Model(&model.UserRole{}).
			Where("user_id = ? AND role_id = ?", demo.ID, roles[0].ID).Count(&count).Error; err == nil && count == 0 {
			if err := db.Create(&model.UserRole{UserID: demo.ID, RoleID: roles[0].ID}).Error; err != nil {
				return err
			}
		}
	}

	// 存量迁移: 无角色的活动用户自动补默认 user 角色 (RBAC 真实查询前用户无角色也拥有基础权限,
	// 避免切换到真实查询后被全部锁定)
	var orphanUsers []model.User
	if err := db.Where("id NOT IN (SELECT user_id FROM user_roles)").
		Find(&orphanUsers).Error; err != nil {
		return err
	}
	for _, u := range orphanUsers {
		if u.Status != 1 {
			continue
		}
		if err := db.Create(&model.UserRole{UserID: u.ID, RoleID: roleByName["user"].ID}).Error; err != nil {
			return err
		}
		log.Printf("RBAC seed: assigned default role 'user' to existing user %s", u.Username)
	}

	log.Println("RBAC seed completed (14 permissions, admin/operator/user roles, demo_user=admin)")
	return nil
}

func ensureRolePermission(db *gorm.DB, roleID, permissionID string) error {
	var count int64
	if err := db.Model(&model.RolePermission{}).
		Where("role_id = ? AND permission_id = ?", roleID, permissionID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return db.Create(&model.RolePermission{RoleID: roleID, PermissionID: permissionID}).Error
}
