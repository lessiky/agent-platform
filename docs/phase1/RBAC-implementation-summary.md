# RBAC 权限体系落地 - 实现总结 (M1/M2/M3/M4/M4.5 遗留项)

> **版本：** v1.0
> **日期：** 2026-08-20
> **范围：** 权限中间件真实查询 (M2 待办 #3 / M3 待办 #5 / M4 待办 #6) + 用户/角色管理 API 与 UI (M1 计划项 + M4.5 待办 #3)

---

## 1. 已完成内容

### 1.1 权限模型与种子数据 (`database/seed.go`, 幂等)

12 个权限点 / 3 个内置角色:

| 角色 | 权限 |
|------|------|
| `admin` | 全部 12 个 (含 `mcp:approve` / `user:manage` / `role:manage`) |
| `operator` | 9 个业务读写 (不含 `mcp:approve` / `user:manage` / `role:manage`) |
| `user` | 4 个只读 (`agent:read` / `mcp:read` / `model:read` / `workflow:read`), 默认角色 |

- 新增权限点: `workflow:read/write/execute` (M5 路由已在用, 种子此前遗漏)、`user:manage`、`role:manage`。
- `demo_user` 固定 `admin`; **存量无角色活动用户启动时自动补 `user` 角色** (切换真实权限查询前无角色用户也拥有基础权限, 避免被全部锁定)。
- 角色-权限种子只增不删: 启动不会移除管理员刚在 UI 取消的权限。

### 1.2 代码结构

```
backend/internal/
├── middleware/permissions.go         # 新增: GetUserPermissions (user_roles+role_permissions+permissions 联表)
│                                      #   + 30s TTL 缓存 + InvalidatePermissionCache + 禁用/删除用户拒绝
├── middleware/auth.go                # AuthCheck 改用真实权限查询; 删除硬编码 getUserPermissions stub
├── database/seed.go                  # 12 权限 / 3 内置角色 / 存量用户补默认角色 (幂等)
├── repository/rbac_repository.go     # 新增: 用户列表(含角色名)/分配角色/删除保护查询;
│                                      #   角色 CRUD + 权限全量替换 + 用户数统计
├── repository/user_repository.go     # 接口扩展: CreateWithRoles / List / UpdateProfile / UpdatePassword /
│                                      #   Delete / AssignRoles / CountActiveUsersWithRole / ListRoleNames
├── service/rbac_service.go           # 新增: RBACService (用户/角色 CRUD + 保护规则 + 审计 + 缓存失效)
├── service/auth_service.go           # Register 分配默认 user 角色 (原 TODO)
├── model/user.go                     # Email 改为 *string (可空, 避免空邮箱撞部分唯一索引)
└── api/rbac/handler.go               # 新增: /users /roles /permissions /auth/me 路由

frontend/src/
├── api/rbac.ts                       # 新增: userApi / roleApi / permissionApi / meApi
├── store/auth-store.ts               # roles/permissions + fetchMe (/auth/me) + useHasPermission
├── router/guards.tsx                 # 新增 RequirePermission (无权限 403 页)
├── layouts/MainLayout.tsx            # 菜单按 user:manage/role:manage 显示 "用户管理/角色管理"
├── pages/system/UserListPage.tsx     # 新增: 用户 CRUD + 分配角色 + 启停
└── pages/system/RoleListPage.tsx     # 新增: 角色 CRUD + 按资源分组勾选权限
```

### 1.3 权限解析与缓存 (`middleware/permissions.go`)

- `AuthCheck` 按 JWT `user_id` 联表查询有效权限码; **查询失败 fail closed** (拒绝并记日志), 用户被删除/停用时返回空权限 (存量 token 立即失效, 无需等过期)。
- 30s TTL 缓存降低每请求查库开销; RBAC 变更 (用户角色/角色权限/启停) 由服务层显式失效缓存, 变更即时生效。

### 1.4 API (11 个新端点)

```
GET    /api/v1/users                  # user:manage  (q 搜索 / status 过滤 / page/size)
POST   /api/v1/users                  # user:manage  (roles 为空时分配默认 user 角色)
GET    /api/v1/users/:id              # user:manage  (含角色名)
PUT    /api/v1/users/:id              # user:manage  (email / status / password, 均可空=不变)
PUT    /api/v1/users/:id/roles        # user:manage  (全量替换角色)
DELETE /api/v1/users/:id              # user:manage
GET    /api/v1/roles                  # role:manage  (含权限码 + 用户数)
POST   /api/v1/roles                  # role:manage  (permissions 为权限码列表)
PUT    /api/v1/roles/:id              # role:manage  (permissions 非 null 时全量替换)
DELETE /api/v1/roles/:id              # role:manage
GET    /api/v1/permissions            # role:manage  (权限点定义)
GET    /api/v1/auth/me                # 任意已登录用户 (user + roles + permissions, 前端权限控制数据源)
```

### 1.5 保护规则与审计

- 用户: 不可删除自己; 不可删除最后一个管理员 (admin 角色); 删除级联清理 user_roles。
- 角色: 内置角色 (admin/operator/user) 不可删除; 有用户绑定的角色不可删除; `admin` 角色权限更新时强制保留 `user:manage` / `role:manage` / `mcp:approve` (防把自己锁在管理界面外)。
- 校验: 未知角色名/权限码 → 400; 名称冲突 (users.username/roles.name 部分唯一索引) → 400 "名称已存在"。
- 审计: 全部用户/角色变更落 `audit_logs` (8 类 action: user.create/update/delete/assign_roles, role.create/update/delete, detail 含变更上下文), 与 M4.5 审核审计同表。

### 1.6 前端 (RBAC 管理 UI)

- 侧边栏 "用户管理 / 角色管理" 按当前用户权限显示; 路由 `RequirePermission` 守卫 (无权限渲染 403 页, 拉取 /auth/me 前显示加载态)。
- 用户管理: 搜索/状态过滤/分页, 新建 (角色多选, 不选=默认 user), 编辑 (邮箱/重置密码/启停), 分配角色 (全量替换), 删除确认。
- 角色管理: 权限按资源分组勾选 (单一受控 Checkbox.Group), 内置角色标记且禁止删除, admin 编辑提示强制保留项。

---

## 2. 端到端验证 (2026-08-20 全部通过)

```
me        demo_user 登录 -> /auth/me 返回 admin + 12 权限                                PASS
存量      xuyao/uitest01 (无角色) 启动后自动补 user 角色, 用户列表展示正确                 PASS
operator  创建+登录 -> 9 权限, 无 mcp:approve/user:manage/role:manage                    PASS
越权      operator GET /users /roles -> 403; GET /agents -> 200                          PASS
只读      默认 user 角色 4 权限; POST /agents -> 403, GET -> 200                         PASS
即时生效  清空用户角色后旧 token 立即 403 (缓存主动失效)                                  PASS
角色 CRUD 创建/更新(权限全量替换)/删除 support 角色; 未知权限码 -> 400                   PASS
内置保护  删除内置 operator -> 400; admin 权限收缩后仍保留 3 个关键权限                   PASS
用户保护  删除自己 -> 400; 重名 -> 400; 未知用户 -> 404                                  PASS
停用      停用后存量 token 立即 403, 登录 -> 401                                         PASS
审计      audit_logs 8 类 action 完整 (user.*/role.*)                                    PASS
API 合计  43 项断言 43 PASS (output/rbac-e2e.ps1)                                        PASS
前端      Playwright 冒烟: 登录 -> 系统管理菜单 (admin 可见) -> 用户页 4 用户/角色页 3 角色
          -> UI 新建用户成功; 只读账号无系统管理菜单, 直访 /system/users 渲染 403 页     PASS
构建      go build / go vet 通过; tsc --noEmit + vite build 通过                        PASS
```

---

## 3. 依赖变更

无新增第三方依赖 (Go/前端均复用现有 GORM / antd / axios 能力)。

---

## 4. 行为变更与取舍

| 项 | 变更前 | 变更后 | 说明 |
|----|--------|--------|------|
| 权限来源 | middleware 硬编码 (所有登录用户拥有全部业务权限, admin 追加 mcp:approve) | user_roles/role_permissions 真实查询 | 无角色用户现在没有任何权限 (种子已为存量用户补 user 角色) |
| DB 故障时鉴权 | 硬编码放行 | fail closed (403 + 日志) | 更安全的取舍; DB 故障期间所有数据接口本就不可用 |
| 用户停用 | 存量 token 有效期 (24h) 内仍可访问 | 立即拒绝 | 权限缓存 30s TTL + 变更时主动失效 |
| 注册新用户 | 无角色 (TODO) | 自动分配 user 角色 (只读) | 可在用户管理页改角色 |
| 登录响应 | user + token | 不变 (角色/权限经 /auth/me 拉取, 登录响应保持轻量) | 前端 MainLayout 挂载时 fetchMe |
| User.email | string | *string (可空) | 管理端建用户邮箱选填; 空值存 NULL 避免空串撞部分唯一索引 |

- **权限变更生效时延**: 角色权限变更清空全部用户缓存 (影响面无法精确定位, 选择全清); 用户级角色变更只失效该用户。
- **软删语义**: users/roles 软删, user_roles/role_permissions join 行在删除用户/角色时硬删, 不产生孤儿关联。

---

## 5. 运行方式

无新增 `.env` 配置; 权限与角色由启动时幂等种子初始化 (重复启动安全, 种子只增不删)。

```
# 登录 (demo_user 固定 admin)
curl -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' -d '{"username":"demo_user","password":"pass1234"}'

# 当前用户角色/权限
curl localhost:8080/api/v1/auth/me -H "Authorization: Bearer $TOKEN"

# 创建用户并分配 operator 角色
curl -X POST localhost:8080/api/v1/users -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"username":"ops1","password":"secret123","roles":["operator"]}'

# 全量替换用户角色
curl -X PUT localhost:8080/api/v1/users/$USER_ID/roles -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"roles":["operator"]}'

# 创建角色 (权限码列表)
curl -X POST localhost:8080/api/v1/roles -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"support","permissions":["agent:read","mcp:read"]}'
```

---

## 6. 待办 (后续衔接项)

1. **资源级权限 (PRD 2.1.3)**: 当前为功能级 RBAC (权限点 = 资源:动作), 尚未做到 Agent 实例级操作权限; PRD 中 "精细到 Agent 实例级别" 留待 Phase 2 (团队/属主维度)
2. **权限点扩展**: 对话 (chat) / API Key 管理等 M2.5 后新增功能目前复用 agent:read/write, 可按需拆分独立权限点 (种子与种子映射机制已支持)
3. **管理员操作通知**: 用户/角色变更目前仅落审计日志, 无站内/IM 通知 (与 M4.5 审核通知统一设计)
4. **登录态刷新**: JWT 24h 过期后权限变更自然生效; 如需即时踢出可引入 token 版本号 (当前停用即拒绝已覆盖主要场景)