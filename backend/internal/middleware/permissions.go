package middleware

import (
	"context"
	"log"
	"sync"
	"time"

	"agent-platform/internal/database"
	"agent-platform/internal/model"
)

// permCacheTTL 权限缓存有效期: 避免每次请求查库; RBAC 变更由服务层显式失效
// (InvalidatePermissionCache), 缓存仅作 DB 抖动时的兜底窗口。
const permCacheTTL = 30 * time.Second

type permCacheEntry struct {
	perms     []string
	fetchedAt time.Time
}

var (
	permCacheMu sync.RWMutex
	permCache   = make(map[string]permCacheEntry)
)

// InvalidatePermissionCache 失效指定用户的权限缓存; userID 传空串时清空全部
// (角色权限变更会影响该角色下所有用户, 无法精确定位, 直接全清最稳妥)
func InvalidatePermissionCache(userID string) {
	permCacheMu.Lock()
	defer permCacheMu.Unlock()
	if userID == "" {
		permCache = make(map[string]permCacheEntry)
		return
	}
	delete(permCache, userID)
}

// GetUserPermissions 从 RBAC 表 (user_roles + role_permissions + permissions)
// 解析用户权限码列表 (M1 遗留 stub 的真实实现)。
// 用户已被删除或禁用时返回空 (等效拒绝)。
// 查询失败时 fail closed: 拒绝访问并记录日志, 避免故障期间越权放行。
func GetUserPermissions(ctx context.Context, userID string) []string {
	permCacheMu.RLock()
	entry, ok := permCache[userID]
	permCacheMu.RUnlock()
	if ok && time.Since(entry.fetchedAt) < permCacheTTL {
		return entry.perms
	}

	var user model.User
	if err := database.DB.WithContext(ctx).First(&user, "id = ?", userID).Error; err != nil {
		log.Printf("RBAC: failed to load user %s: %v (denying)", userID, err)
		return nil
	}
	if user.Status != 1 {
		return []string{}
	}

	var perms []string
	err := database.DB.WithContext(ctx).
		Model(&model.UserRole{}).
		Joins("JOIN role_permissions ON role_permissions.role_id = user_roles.role_id").
		Joins("JOIN permissions ON permissions.id = role_permissions.permission_id").
		Where("user_roles.user_id = ?", userID).
		Distinct().
		Pluck("permissions.code", &perms).Error
	if err != nil {
		log.Printf("RBAC: failed to load permissions for user %s: %v (denying)", userID, err)
		return nil
	}

	if perms == nil {
		perms = []string{}
	}
	permCacheMu.Lock()
	permCache[userID] = permCacheEntry{perms: perms, fetchedAt: time.Now()}
	// 顺手清理过期条目, 防止 map 无限增长
	if len(permCache) > 256 {
		for id, e := range permCache {
			if time.Since(e.fetchedAt) >= permCacheTTL {
				delete(permCache, id)
			}
		}
	}
	permCacheMu.Unlock()
	return perms
}
