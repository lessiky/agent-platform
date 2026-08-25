# Agent 管理平台 API 参考

> 本文件描述平台后端对外提供的所有 HTTP 接口的用途、用法、入参与出参。
> 代码实现见 `backend/internal/api/*`，业务规则见 `docs/phase1/*-implementation-summary.md`。
> 面向外部系统的 4 个接口（Agent API Key / Webhook Token 认证，无需平台登录态）单独收录于 `docs/external-api.md`。

## 通用约定

### Base URL 与协议

- 后端 API 根路径：`http://localhost:8080/api/v1`
- 前端 dev 环境经 vite 代理访问：`http://127.0.0.1:8090/api` → 转发到 `http://localhost:8080/api`
- 除特别说明外，请求体与响应体均为 `application/json; charset=utf-8`
- 时间字段统一为 RFC3339（如 `2026-08-22T10:00:00+08:00`）

### 认证方式

平台有 3 类鉴权：

| 方式 | 适用接口 | 请求头 |
| ---- | -------- | ------ |
| JWT（用户登录态） | 绝大多数 `/api/v1/**` 接口 | `Authorization: Bearer <token>`，token 来自 `POST /auth/login` |
| Agent API Key | `POST /agents/:id/invoke`、`GET /agents/:id/invoke/executions/:executionId`、`POST /agents/:id/invoke/executions/:executionId/cancel`、`GET /agents/:id/invoke/approvals/:approvalId`（外部系统调用 Agent 及其执行任务查询/取消） | `Authorization: Bearer akp_<64位hex>` |
| Webhook Token | `POST /webhooks/workflows/:token`（公开端点） | 无请求头，token 在 URL 路径中 |

- JWT 过期默认 24h（`JWT_EXPIRE_HOUR`）。用户被停用/删除后，其存量 JWT 立即失效。
- 除登录、注册、外部 invoke、webhook 外，其余接口未带/带错 token 返回 `401 unauthorized`；带有效 token 但权限点不足返回 `403 forbidden`。

### 统一响应封装

所有接口返回统一 JSON 结构（`pkg/response.Response`）：

```json
{
  "code": "success",
  "message": "ok",
  "data": { }
}
```

| HTTP 状态 | `code` | 含义 |
| --------- | ------ | ---- |
| 200 | `success` | 成功 |
| 201 | `success`（message=`created`） | 创建成功 |
| 202 | `pending_approval`（message=`已受理, 等待人工审核`） | 已受理，等待人工审核（涉及需审核的 MCP 工具调用） |
| 400 | `validation_error` | 参数校验失败 |
| 401 | `unauthorized` | 未授权（token 缺失/无效/过期） |
| 403 | `forbidden` | 权限不足 |
| 404 | `not_found` | 资源不存在 |
| 409 | `skill_conflict` / `skill_in_use` | 技能名冲突 / 技能被 Agent 关联 |
| 500 | `internal_error` / `wrapped_error` | 服务器内部错误 |

> 错误响应不含 `data` 字段，`message` 为可读错误描述。

### 分页约定

列表类接口统一使用查询参数分页：

| 参数 | 类型 | 默认 | 说明 |
| ---- | ---- | ---- | ---- |
| `page` | int | 1 | 页码，从 1 开始 |
| `size` | int | 20 | 每页条数 |

分页响应 `data` 统一为：

```json
{ "items": [ ], "total": 100, "page": 1, "page_size": 20 }
```

### 权限点

接口所需权限点（RBAC）如下，角色与权限对应关系见「模块操作说明」：

`agent:read` `agent:write` `mcp:read` `mcp:write` `mcp:approve` `model:read` `model:write` `workflow:read` `workflow:write` `workflow:execute` `skill:read` `skill:write` `user:manage` `role:manage` `platform:manage`

---

## 1. 认证 Auth

### 1.1 注册用户

- **用途**：注册新用户。平台首个注册用户自动获得 admin 角色，之后注册默认 `user` 角色。
- **接口**：`POST /api/v1/auth/register`
- **认证**：无（公开）
- **入参**（JSON body）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `username` | string | 是 | 2-64 | 用户名，全局唯一 |
| `email` | string | 是 | email 格式 | 邮箱 |
| `password` | string | 是 | ≥6 | 密码（bcrypt 加密存储） |

- **出参**：`201 created`

```json
{ "code": "success", "message": "created", "data": { "id": "uuid", "username": "alice", "email": "a@x.com" } }
```

- **错误**：`400` 用户名已存在 / 参数非法。

### 1.2 登录

- **用途**：用户名密码登录，返回 JWT。
- **接口**：`POST /api/v1/auth/login`
- **认证**：无（公开）
- **入参**（JSON body）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `username` | string | 是 | 用户名 |
| `password` | string | 是 | 密码 |

- **出参**：`200`

```json
{
  "code": "success", "message": "ok",
  "data": {
    "token": "eyJhbGciOi...",
    "user": { "id": "uuid", "username": "alice", "email": "a@x.com" }
  }
}
```

- **错误**：`401` 用户名或密码错误。

### 1.3 登出

- **用途**：登出（前端清除本地 token；后端无状态）。
- **接口**：`POST /api/v1/auth/logout`
- **认证**：无（公开）
- **入参**：无
- **出参**：`200`

```json
{ "code": "success", "message": "ok", "data": { "message": "logout successfully" } }
```

### 1.4 当前登录用户

- **用途**：获取当前用户信息及其角色、权限码，前端据此做菜单/按钮级权限控制。
- **接口**：`GET /api/v1/auth/me`
- **认证**：JWT
- **入参**：无
- **出参**：`200`，`data` 为 `MeResult`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `user` | object | 用户对象（`id`/`username`/`email`/`status`/`created_at`/`updated_at`） |
| `roles` | string[] | 角色名列表，如 `["admin"]` |
| `permissions` | string[] | 权限码列表，如 `["agent:read","agent:write",...]` |

---

## 2. 概览 Overview

### 2.1 概览统计

- **用途**：概览页「基本情况」统计（Agent / MCP / 模型 / 工作流 / 审核 / 技能 的数量与健康状态）。
- **接口**：`GET /api/v1/overview/summary`
- **认证**：JWT（任意已登录用户）
- **入参**：无
- **出参**：`200`，`data` 为 `OverviewSummary`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `agents` | object | `{total, running, stopped, idle, error}` |
| `mcps` | object | `{total, normal, abnormal, tools_total}`，normal=connected，abnormal=其余 |
| `models` | object | `{total, available, abnormal}`，available=active，abnormal=error |
| `workflows` | object | `{active, draft}` |
| `approvals` | object | `{total, pending, reviewed}`，reviewed=已处置(approved/rejected/expired) |
| `skills` | object | `{total, active, disabled}` |

```json
{
  "code": "success", "message": "ok",
  "data": {
    "agents": { "total": 5, "running": 2, "stopped": 1, "idle": 2, "error": 0 },
    "mcps": { "total": 3, "normal": 2, "abnormal": 1, "tools_total": 8 },
    "models": { "total": 4, "available": 3, "abnormal": 1 },
    "workflows": { "active": 2, "draft": 1 },
    "approvals": { "total": 10, "pending": 2, "reviewed": 8 },
    "skills": { "total": 3, "active": 3, "disabled": 0 }
  }
}
```
---

## 3. 用户与角色管理 RBAC

> 本组接口供系统管理页面使用。用户/角色变更全部写入审计日志；权限变更即时生效（30s 权限缓存被主动失效）。

### 3.1 用户列表

- **用途**：分页查询用户（含角色名）。
- **接口**：`GET /api/v1/users`
- **权限**：`user:manage`
- **入参**（query）：

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `q` | string | 否 | 用户名/邮箱模糊搜索 |
| `status` | int | 否 | 1=启用 0=停用 |
| `page` / `size` | int | 否 | 分页（默认 1 / 20） |

- **出参**：`200`，`items` 为 `UserWithRoles[]`（用户字段 + `roles: string[]`）。

### 3.2 创建用户

- **用途**：创建用户并分配角色；`roles` 为空时分配默认 `user` 角色。
- **接口**：`POST /api/v1/users`
- **权限**：`user:manage`
- **入参**（JSON body，`CreateUserRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `username` | string | 是 | 2-64 | 用户名，唯一 |
| `email` | string | 否 | email 格式 | 邮箱 |
| `password` | string | 是 | 6-128 | 密码 |
| `roles` | string[] | 否 | 已存在的角色名 | 角色名列表，空=默认 `user` |

- **出参**：`201 created`，`data`：`{ "id": "uuid", "username": "bob" }`
- **错误**：`400` 名称已存在 / 未知角色名。

### 3.3 用户详情

- **接口**：`GET /api/v1/users/:id`
- **权限**：`user:manage`
- **出参**：`200`，`data` 为 `UserWithRoles`（含 `roles`）。

### 3.4 更新用户

- **用途**：更新邮箱 / 状态 / 密码；字段可空（空 = 不变）。
- **接口**：`PUT /api/v1/users/:id`
- **权限**：`user:manage`
- **入参**（JSON body，`UpdateUserRequest`）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `email` | string \| null | 否 | 新邮箱 |
| `status` | int \| null | 否 | 1=启用 0=停用（停用后该用户存量 token 立即失效） |
| `password` | string \| null | 否 | 新密码（6-128） |

- **出参**：`200`，`data`：`{ "id": "uuid", "username": "bob" }`

### 3.5 全量替换用户角色

- **接口**：`PUT /api/v1/users/:id/roles`
- **权限**：`user:manage`
- **入参**（JSON body）：`{ "roles": ["operator"] }`（数组为**全量**替换；未知角色名返回 400）
- **出参**：`200`，`data`：`{ "roles": ["operator"] }`

### 3.6 删除用户

- **接口**：`DELETE /api/v1/users/:id`
- **权限**：`user:manage`
- **保护规则**：不可删除自己；不可删除最后一个 admin。
- **出参**：`200`，`data`：`{ "message": "deleted" }`

### 3.7 角色列表

- **用途**：角色列表（含权限码与用户数）。
- **接口**：`GET /api/v1/roles`
- **权限**：`role:manage`
- **入参**：无
- **出参**：`200`，`data.items` 为 `RoleWithPermissions[]`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` / `name` / `description` / `status` | - | 角色基本字段 |
| `permissions` | string[] | 权限码列表 |
| `user_count` | int | 绑定该角色的用户数 |

### 3.8 创建角色

- **接口**：`POST /api/v1/roles`
- **权限**：`role:manage`
- **入参**（JSON body，`CreateRoleRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `name` | string | 是 | 2-64 | 角色名，唯一 |
| `description` | string | 否 | ≤512 | 描述 |
| `permissions` | string[] | 否 | 已存在的权限码 | 权限码列表 |

- **出参**：`201 created`，`data` 为 `Role` 对象。

### 3.9 更新角色

- **接口**：`PUT /api/v1/roles/:id`
- **权限**：`role:manage`
- **入参**（JSON body，`UpdateRoleRequest`）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `description` | string \| null | 否 | 新描述 |
| `status` | int \| null | 否 | 1/0 |
| `permissions` | string[] \| null | 否 | **非 null 时全量替换**权限 |

- **保护规则**：`admin` 角色强制保留 `user:manage` / `role:manage` / `mcp:approve`（防止锁死管理入口）。
- **出参**：`200`，`data` 为更新后的 `Role` 对象。

### 3.10 删除角色

- **接口**：`DELETE /api/v1/roles/:id`
- **权限**：`role:manage`
- **保护规则**：内置角色（admin / operator / user）不可删；有用户绑定的角色不可删（`400 role_in_use`）。
- **出参**：`200`，`data`：`{ "message": "deleted" }`

### 3.11 权限点定义列表

- **用途**：获取平台全部权限点定义（14 个），供角色管理页分组勾选。
- **接口**：`GET /api/v1/permissions`
- **权限**：`role:manage`
- **出参**：`200`，`data.items` 为 `Permission[]`：`{id, code, name, resource, action, created_at}`

---

## 4. Agent 管理

> Agent = 模型模板 + 绑定 MCP 工具 + 技能 + 系统提示词 的运行时实体。一个 Agent 对应一个实例；实例「启动」= 上线（可接受对话与外部调用）。**运行中禁止更新 / 删除 / 回滚**（400）。

### 4.1 创建 Agent

- **接口**：`POST /api/v1/agents`
- **权限**：`agent:write`
- **入参**（JSON body，`CreateAgentRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `name` | string | 是 | 2-64 | 名称，全局唯一 |
| `description` | string | 否 | ≤512 | 描述 |
| `model_id` | string | 否 | 已存在的模型模板 ID | 关联模型模板（M4 路由首选） |
| `model` | string | 是 | - | 模型名（如 `gpt-4o`） |
| `system_prompt` | string | 否 | - | 系统提示词（随每次对话发送，纳入版本快照） |
| `temperature` | number | 否 | 0-2 | 随机度 |
| `max_tokens` | int | 否 | >0 | 最大生成 token |
| `tools` | string[] | 否 | 须来自绑定 MCP 的已发现工具 | 可用工具白名单；空 = 全部绑定工具可用 |
| `max_tool_rounds` | int | 否 | >0 | 单次对话工具调用轮数上限（0=默认 5） |
| `mcp_ids` | string[] | 否 | 已存在的 MCP 服务器 ID | 绑定的 MCP（可用工具来源） |
| `skills` | string[] | 否 | 已存在的技能 ID | 绑定的技能包（校验 `required_tools` ⊆ 可用工具集） |
| `skills_usage_mode` | string | 否 | `metadata_injection` \| `full_injection` | 技能注入模式，默认 `metadata_injection` |
| `team_id` | string | 否 | - | 团队（预留） |
| `simulate_traffic` | bool | 否 | 默认 false | 实例常驻时是否生成模拟流量 |

- **出参**：`201 created`，`data` 为 `Agent` 对象（`version=1`，`status=idle`）。
- **错误**：`400` 名称已存在 / 工具不在已发现工具列表 / 技能依赖工具缺失。

### 4.2 Agent 列表

- **接口**：`GET /api/v1/agents`
- **权限**：`agent:read`
- **入参**（query）：`q`（名称搜索）、`status`（`idle`/`running`/`stopped`/`error`）、`page`/`size`
- **出参**：`200`，分页 `items` 为 `Agent[]`。

`Agent` 对象字段：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` / `name` / `description` | - | 基本字段 |
| `model_id` | string \| null | 关联模型模板 ID |
| `status` | string | `idle` / `running` / `stopped` / `error` |
| `version` | int | 当前版本号 |
| `config` | object | 配置快照（`model`/`system_prompt`/`temperature`/`max_tokens`/`tools`/`max_tool_rounds`/`skills_usage_mode`/`simulate_traffic`） |
| `created_at` / `updated_at` | string | 时间 |

### 4.3 Agent 详情（含实例）

- **接口**：`GET /api/v1/agents/:id`
- **权限**：`agent:read`
- **出参**：`200`，`data`：`{ "agent": Agent, "instance": AgentInstance | null }`

`AgentInstance` 字段：`id`、`agent_id`、`status`（`pending`/`running`/`stopped`/`error`）、`endpoint`、`started_at`、`stopped_at`、`last_heartbeat`、`created_at`、`updated_at`

### 4.4 更新 Agent

- **用途**：全量更新配置并产生**新版本快照**（`version+1`）。
- **接口**：`PUT /api/v1/agents/:id`
- **权限**：`agent:write`
- **入参**（JSON body，`UpdateAgentRequest`）：字段同「4.1 创建」，差异：
  - `mcp_ids` 为 `null` 表示绑定不变（空数组 `[]` = 清空绑定）
  - `skills` 为 `null` 表示关联不变（空数组 `[]` = 清空关联）
- **出参**：`200`，`data` 为更新后的 `Agent`（`version` 递增）。
- **错误**：`400` 运行中禁止更新 / 工具或技能校验失败。

### 4.5 删除 Agent

- **接口**：`DELETE /api/v1/agents/:id`
- **权限**：`agent:write`
- **出参**：`200`，`data`：`{ "deleted": true }`

### 4.6 启动实例

- **用途**：实例上线（可接受对话 / `/invoke` 调用）；默认不产生背景流量。
- **接口**：`POST /api/v1/agents/:id/start`
- **权限**：`agent:write`
- **入参**：无
- **出参**：`200`，`data` 为 `AgentInstance`（`status=running`，`started_at` 已设置）。

### 4.7 停止实例

- **接口**：`POST /api/v1/agents/:id/stop`
- **权限**：`agent:write`
- **出参**：`200`，`data` 为 `AgentInstance`（`status=stopped`）。

### 4.8 调用统计

- **用途**：区间调用量 / 错误率 / token / 平均耗时 + 按日序列。
- **接口**：`GET /api/v1/agents/:id/metrics`
- **权限**：`agent:read`
- **入参**（query）：

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `from` | string | 否 | RFC3339，默认 `to` 前 7 天（按日对齐） |
| `to` | string | 否 | RFC3339，默认当前时间 |

- **出参**：`200`，`data`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `from` / `to` | string | 实际统计区间（按日对齐，`to` 上界不含） |
| `total_calls` / `total_errors` | int | 调用总数 / 失败数 |
| `error_rate` | number | 失败率（0-1） |
| `total_tokens` | int | token 总量 |
| `avg_latency_ms` | number | 平均耗时 |
| `daily` | array | 按日序列（`stat_date`/`calls`/`errors`/`tokens`/`latency_ms`） |

### 4.9 运行日志

- **接口**：`GET /api/v1/agents/:id/logs`
- **权限**：`agent:read`
- **入参**（query）：

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `level` | string | 否 | `info` / `warn` / `error` |
| `keyword` | string | 否 | 消息关键字 |
| `since` | string | 否 | RFC3339 或 unix 秒时间戳 |
| `page` / `size` | int | 否 | 分页（size 默认 100） |

- **出参**：`200`，分页 `items` 为 `AgentLog[]`：`{id, agent_id, instance_id, level, message, created_at}`
- **说明**：日志表按 Agent 保留最近 5000 条。

### 4.10 版本历史

- **接口**：`GET /api/v1/agents/:id/versions`
- **权限**：`agent:read`
- **出参**：`200`，`data.items` 为 `AgentVersion[]`：`{id, agent_id, version, name, description, config, created_by, created_at}`（`config` 为该版本完整配置快照，含 `system_prompt`）。

### 4.11 版本回滚

- **用途**：将 Agent 配置恢复到历史版本；回滚本身也产生新版本。
- **接口**：`POST /api/v1/agents/:id/rollback`
- **权限**：`agent:write`
- **入参**（JSON body）：`{ "version": 2 }`（必填，目标版本号）
- **出参**：`200`，`data` 为回滚后的 `Agent`。
- **错误**：`400` 运行中禁止回滚 / 版本不存在。

### 4.12 绑定 MCP 列表

- **接口**：`GET /api/v1/agents/:id/mcps`
- **权限**：`agent:read`
- **出参**：`200`，`data.items` 为 `BoundMCPView[]`：`{id, name, status, tools: MCPTool[], last_error}`

### 4.13 关联技能列表

- **接口**：`GET /api/v1/agents/:id/skills`
- **权限**：`agent:read`
- **出参**：`200`，`data.skills` 为 `BoundSkillView[]`：`{id, name, version, description, status, required_tools, missing_tools}`（`missing_tools` = 未被当前可用工具集覆盖的依赖工具）。

### 4.14 全量更新技能关联

- **用途**：全量替换 Agent 的技能关联；校验每个技能 `required_tools` ⊆ Agent 可用工具集。
- **接口**：`PUT /api/v1/agents/:id/skills`
- **权限**：`agent:write`
- **入参**（JSON body）：`{ "skills": ["<skill-id>", "..."] }`（空数组 = 清空）
- **出参**：`200`，`data.skills` 为更新后的 `BoundSkillView[]`。
- **错误**：`400` 缺失依赖工具（返回缺失工具列表）。

### 4.15 创建 API Key

- **用途**：创建外部调用 Key。**明文 `akp_...` 仅本次响应返回一次**，之后只存 SHA-256 摘要。
- **接口**：`POST /api/v1/agents/:id/keys`
- **权限**：`agent:write`
- **入参**（JSON body，`CreateKeyRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `name` | string | 否 | ≤64 | Key 备注名 |
| `expires_at` | string | 否 | RFC3339 未来时间 | 过期时间，空 = 永不过期 |

- **出参**：`201 created`，`data`：

```json
{ "key": "akp_<64位hex 明文, 仅返回一次>", "api_key": { "id": "uuid", "key_prefix": "akp_ab12", "status": "active", "expires_at": null, "created_at": "..." } }
```

### 4.16 API Key 列表

- **接口**：`GET /api/v1/agents/:id/keys`
- **权限**：`agent:read`
- **出参**：`200`，`data.items` 为 `AgentAPIKey[]`：`{id, agent_id, name, key_prefix, status(active/revoked), last_used_at, expires_at, created_at, revoked_at}`

### 4.17 吊销 API Key

- **接口**：`DELETE /api/v1/agents/:id/keys/:keyId`
- **权限**：`agent:write`
- **出参**：`200`，`data`：`{ "revoked": true }`（吊销后立即不可用）。

### 4.18 删除已吊销的 API Key

- **接口**：`POST /api/v1/agents/:id/keys/:keyId/delete`
- **权限**：`agent:write`
- **约束**：仅允许删除**已吊销**的 Key。
- **出参**：`200`，`data`：`{ "deleted": true }`

### 4.19 状态看板

- **用途**：Agent 状态计数 + 运行中 Agent 列表（概览页数据源）。
- **接口**：`GET /api/v1/agents/dashboard`
- **权限**：`agent:read`
- **出参**：`200`，`data`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `status_counts` | object | `{idle, running, stopped, error}` |
| `total_agents` | int | Agent 总数 |
| `running_agents` | array | 运行中 Agent：`{id, name, status, version, instance_id, last_heartbeat, started_at}` |

### 4.20 外部调用（API Key 认证）

- **用途**：外部系统以 Agent API Key 调用 Agent（与用户对话同一执行链路：模型路由 + 工具调用 + 审核门禁）。**不走用户 JWT**。
- **接口**：`POST /api/v1/agents/:id/invoke`
- **认证**：`Authorization: Bearer akp_<key>`
- **入参**（JSON body，`InvokeAgentRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `message` | string | 是 | ≤8192 | 用户提示词 |
| `session_id` | string | 否 | 该 Agent 下已存在的会话 | 指定则复用会话（多轮上下文）；不传则自动新建（外部会话，响应中返回，可续用） |

- **出参**：`202 accepted`，`data` 为 `InvokeAgentResult`（异步执行任务）：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `agent_id` | string | Agent ID |
| `key_prefix` | string | 使用的 Key 前缀 |
| `status` | string | `running`（任务已受理，执行中） |
| `execution_id` | string | 执行任务 ID；状态/阶段/结果经 [4.21 外部调用执行任务状态查询](#421) 轮询 |

- **异步语义（2026-08-24）**：执行链（模型 + 工具轮 + 审核门禁）在平台后台运行，接口立即返回 202 + `execution_id`；状态 `running`/`waiting_approval`/`success`/`failed`/`stalled`/`cancelled`、当前阶段 `stage`、进度心跳 `last_activity_at` 均可查询（外部方无需长挂连接即可区分「执行中」与「卡死」）；外部方亦可经 [4.23 取消外部调用执行任务](#423) 主动放弃任务。
- **降级同步路径**（无可用模型时）：返回 `200`，`data` 额外含 `reply` / `model_ok`（`false`）/ `mcp_details` / `tokens` / `latency_ms`（旧结构，行为不变）。
- **特殊状态**：执行中出现需人工审核的工具调用时，执行任务状态转为 `waiting_approval`（`result.pending_approvals` 携带待审核请求，对应工具**未执行**）；外部系统凭同一 API Key 轮询 [4.22 外部调用待审核结果查询](#422) 获取终态、工具执行结果与模型续答（审核决策后平台自动回填执行任务终态）。
- **错误**：`401` Key 无效/过期/已吊销；`404` Agent 不存在；`409` 实例未运行。

### 4.21 外部调用执行任务状态查询（API Key 认证）

- **用途**：`/invoke` 返回 `202` 后，外部系统凭同一 API Key 轮询异步执行任务的状态/进度/结果，无需平台登录态。
- **接口**：`GET /api/v1/agents/:id/invoke/executions/:executionId`
- **认证**：`Authorization: Bearer akp_<key>`
- **路径参数**：`id` = Agent ID（须为 Key 归属的 Agent）；`executionId` = `/invoke` `202` 响应的 `execution_id`
- **权限边界**：Key 只能查询**本 Agent** 的执行任务；任务不存在、ID 非 UUID 或属于其他 Agent 时一律 `404`（不泄露存在性）。
- **出参**：`200`，`data` 为 `AgentExecution`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` / `agent_id` / `source` / `session_id` | string | 执行任务 ID / 归属 / 来源（`api_invoke`）/ 会话 ID |
| `status` | string | `running` 执行中 / `waiting_approval` 等待人工审核 / `success` 成功 / `failed` 失败 / `stalled` 卡死 / `cancelled` 已取消（外部方主动放弃） |
| `stage` | string | 当前阶段：`queued` / `model:round=N` / `tool:<mcp名>/<工具名>` / `model:final (...)` / `等待审核: ...` |
| `pending_approvals` | string[] \| null | 本次执行产生的审核请求 ID 数组（进入 `waiting_approval` 时回填） |
| `result` | object \| null | 执行结果（`success` 时为 `ChatResult`：`reply`/`session_id`/`mcp_calls` 等；进入 `waiting_approval` 时先存中间应答；审核决策后回填续答轮 `ChatResult` + `approval_id`/`approval_status`） |
| `error` | string \| null | 失败/卡死原因 |
| `deadline` | string | 整体 deadline（按工具轮数与单次模型/工具超时推导的预算） |
| `last_activity_at` | string | 最近一次进度心跳（每次阶段推进刷新） |
| `started_at` / `finished_at` | string | 开始 / 结束时间 |

- **终态判定**：`status ∈ {success, failed, stalled, cancelled}` 即终态。`stalled` = 平台 watchdog 判定卡死（running 任务超过「max(单次模型调用超时, 单次工具调用超时) + 60s」无进度心跳），任务已被主动取消，`error` 中 `stage=` 为卡死阶段。`cancelled` = 外部方经取消端点主动放弃任务（与 watchdog 复用同一套进程内取消机制）。
- **错误**：`401` Key 无效/不属于该 Agent/已吊销/已过期；`404` 执行任务不存在或不属于该 Agent。
- **对接文档**：字段与轮询建议详见 `docs/api/external-api.md`「2. 获取执行任务状态」。
### 4.22 外部调用待审核结果查询（API Key 认证）

- **用途**：`/invoke` 返回 `202`（存在待审核工具调用）后，外部系统凭同一 API Key 轮询审核请求的终态与工具执行结果，无需平台登录态。
- **接口**：`GET /api/v1/agents/:id/invoke/approvals/:approvalId`
- **认证**：`Authorization: Bearer akp_<key>`
- **路径参数**：

| 参数 | 说明 |
| ---- | ---- |
| `id` | Agent ID（须为 Key 归属的 Agent） |
| `approvalId` | `202` 响应 `pending_approvals[].approval_id` |

- **权限边界**：Key 只能查询**本 Agent** 的审核请求；审批单不存在、ID 非 UUID、或属于其他 Agent 时一律 `404`（不泄露存在性）。
- **出参**：`200`，`data` 为 `InvokeApprovalView`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `approval_id` | string | 审核请求 ID |
| `tool_name` | string | 待执行工具名 |
| `status` | string | `pending` 待审核 / `approved` 已通过 / `rejected` 已拒绝 / `expired` 已超时 |
| `requested_at` / `expires_at` | string | 请求 / 过期时间（RFC3339） |
| `decided_at` | string \| null | 审核决定时间（通过/拒绝时回填） |
| `executed_at` | string \| null | 工具执行完成时间 |
| `comment` | string \| null | 审核意见（拒绝/超时自动拒绝时） |
| `result` | object \| null | 工具执行结果，仅执行后回填：成功为 MCP 结果 `{"content":[...],"is_error":bool}`；执行失败为 `{"error":"..."}` |
| `continuation` | object \| null | 决策后的模型续答（续答轮落库后回填）：`{reply, message_id, created_at}`。`approved` 时为模型基于工具执行结果的后续答复；`rejected` / `expired` 时为未执行说明。空 = 续答轮尚未完成，继续轮询 |

- **终态判定**：`status ∈ {approved, rejected, expired}` 即终态。`approved` 且 `result` 非空 = 工具已执行完成，`result` 即最终工具输出；`rejected` / `expired` 时工具未执行，`result` 为空。需要工具执行后的模型续答时，继续轮询至 `continuation` 非空（续答由决策钩子异步驱动，通常数秒~数十秒；若长时间为空说明续答轮失败，以 `result` 为最终输出）。
- **轮询建议**：间隔 2~5s；超过 `expires_at` 后由平台超时任务（30s 间隔）标记为 `expired`，注意全局审核配置中的超时策略（拒绝或自动通过）。
- **错误**：`401` Key 无效/不属于该 Agent/已吊销/已过期；`404` 审批单不存在或不属于该 Agent。
- **出参示例**（审核通过且工具已执行）：

```json
{
  "code": "success",
  "message": "ok",
  "data": {
    "approval_id": "b3e8f1c2-7a4d-4e6a-9c1b-2d5f8a9c0e11",
    "tool_name": "ticket.create",
    "status": "approved",
    "requested_at": "2026-08-22T10:00:00+08:00",
    "expires_at": "2026-08-22T10:30:00+08:00",
    "decided_at": "2026-08-22T10:01:02+08:00",
    "executed_at": "2026-08-22T10:01:03+08:00",
    "result": { "content": [ { "type": "text", "text": "ticket #123 created" } ], "is_error": false },
    "continuation": { "reply": "工单已创建成功（编号 TK-123），如需补充描述请告诉我。", "message_id": "7c1d…", "created_at": "2026-08-22T10:01:40+08:00" }
  }
}
```

### 4.23 取消外部调用执行任务（API Key 认证）

- **用途**：外部调用方主动放弃进行中的 `/invoke` 执行任务（业务侧超时、用户改变主意等）；平台取消该任务的执行上下文（透传至进行中的模型/MCP 调用），任务写入终态 `cancelled`，外部方无需继续轮询。
- **接口**：`POST /api/v1/agents/:id/invoke/executions/:executionId/cancel`
- **认证**：`Authorization: Bearer akp_<key>`
- **路径参数**：`id` = Agent ID（须为 Key 归属的 Agent）；`executionId` = `/invoke` `202` 响应的 `execution_id`；无请求体。
- **取消语义**：
  - `running` 且任务在当前进程执行：取消立即生效，`cancelled=true`、`status=cancelled`，进行中的模型/MCP 调用随上下文中断；
  - 已是终态（`success`/`failed`/`stalled`/`cancelled`）：幂等，`cancelled=false` + 当前终态；
  - `waiting_approval`（等人工审核，无可中断的执行上下文）或任务不在当前进程（服务重启后）：`409`。
- **出参**：`200`，`data`：`{execution_id, cancelled, status}`。
- **错误**：`401` Key 无效/不属于该 Agent/已吊销/已过期；`400` `execution_id` 非 UUID；`404` 执行任务不存在或不属于该 Agent；`409` 任务 `waiting_approval` 或不在当前进程。
- **对接文档**：取消语义与 watchdog 关系详见 `docs/api/external-api.md`「3. 取消执行任务」。

### 4.24 对话

- **用途**：用户在平台内与 Agent 对话。执行链路：系统提示词 + 最近 10 条历史 + 当前消息 → 模型（M4 路由故障转移 + 配额）→ 可选工具调用轮（至多 `max_tool_rounds` 轮）→ 应答。
- **接口**：`POST /api/v1/agents/:id/chat`
- **权限**：`agent:write`
- **入参**（JSON body，`ChatRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `message` | string | 是 | ≤8192 | 用户消息 |
| `session_id` | string | 否 | 该用户在该 Agent 下的会话 | 不传 = 新建会话；传 = 续聊 |

- **出参**：`200`，`data` 为 `ChatResult`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `session_id` | string | 会话 ID（新建时即返回） |
| `message_id` | string | 应答消息 ID |
| `reply` | string | 模型应答文本 |
| `execution_id` | string | 8 位 hex 执行 ID（与运行日志配对） |
| `model` / `model_name` | string | 实际使用的模型 |
| `total_tokens` | int | 该轮全部模型调用（含工具轮）token 累加 |
| `latency_ms` | int | 耗时 |
| `mcp_calls` | array | 工具调用明细：`{mcp_name, tool_name, status(ok/error/pending/skipped), detail, latency_ms}` |
| `skill_calls` | array | 技能加载明细（M9）：`{name, version, mode, chars, latency_ms, status}` |
| `pending_approvals` | array | 待审核工具：`{approval_id, mcp_name, tool_name}` |

- **流式接口（SSE）**：`POST /api/v1/agents/:id/chat/stream`，权限 `agent:write`，请求体同上（`ChatRequest`）。响应为 `text/event-stream`，连接保持至执行结束，期间实时推送执行阶段事件（长耗时工具调用也可区分"执行中"）；每 15 秒发送一次 keepalive 注释帧（`: keepalive`）防止代理断连；客户端可主动断开（断开不影响服务端执行与结果落库）。

  | event | data | 说明 |
  | ---- | ---- | ---- |
  | `turn_start` | `{execution_id, session_id}` | 执行开始 |
  | `model_round` | `{round, forced?}` | 第 N 轮模型调用开始；`forced=true` 表示工具轮次用尽, 生成最终答复 |
  | `tool_start` | `{round, mcp_name, tool_name}` | 开始调用工具 |
  | `tool_end` | `{round, mcp_name, tool_name, status, latency_ms, detail?}` | 工具调用结束, `status` 为 ok/error/pending/skipped |
  | `final` | `ChatResult`（与同步接口 `data` 同构） | 执行完成, 推送后服务端关闭连接 |
  | `error` | `{message}` | 执行失败, 推送后服务端关闭连接 |

- **说明**：调用需审核工具时不立即执行，生成审核请求（`source=chat`），`reply` 照常返回；审批通过后工具结果自动回填（不重跑模型轮）。

### 4.25 会话列表

- **接口**：`GET /api/v1/agents/:id/sessions`
- **权限**：`agent:read`
- **入参**（query）：`page` / `size`（默认 1 / 20）
- **出参**：`200`，分页 `items` 为 `ChatSession[]`：`{id, agent_id, title, user_id, status, last_message_at, created_at, updated_at}`（`title` 取首条消息截断）。
- **说明**：仅返回当前登录用户自己的会话（会话按用户隔离，访问他人会话返回 404）。

### 4.26 会话详情（含消息历史）

- **接口**：`GET /api/v1/agents/:id/sessions/:sid`
- **权限**：`agent:read`
- **出参**：`200`，`data`：`{ "session": ChatSession, "messages": ChatMessage[] }`

`ChatMessage` 字段：`{id, session_id, role(user/assistant/tool), content, execution_id, execution_meta(JSON), created_at}`；`execution_meta` 含 `execution_id`/`model_name`/`total_tokens`/`latency_ms`/`mcp_calls`/`skill_calls`/`pending_approvals`。

### 4.27 删除会话

- **接口**：`DELETE /api/v1/agents/:id/sessions/:sid`
- **权限**：`agent:write`
- **出参**：`200`，`data`：`{ "deleted": true }`

### 4.28 修改会话名

- **接口**：`PUT /api/v1/agents/:id/sessions/:sid`
- **权限**：`agent:write`
- **入参**（body）：`{ "title": "新会话名" }`（非空，≤ 128 字符，首尾空白去除）
- **出参**：`200`，`data` 为更新后的 `ChatSession`；会话不属于该 Agent 时 `404`
---

## 5. 技能管理

> 技能包 = zip 压缩包（须含 `SKILL.md`），为 Agent 提供领域知识/指令。平台对技能包**只做存储与只读注入，不执行包内任何代码**。

### 5.1 导入技能包

- **用途**：上传 zip 技能包并入库；同名已存在时默认拒绝，`force=true` 升级为 `version+1`（已关联 Agent 保留）。
- **接口**：`POST /api/v1/skills/import`
- **权限**：`skill:write`
- **入参**（`multipart/form-data`）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `file` | file | 是 | zip 技能包（上传上限 20MB） |
| `force` | string | 否 | `true` = 同名强制升级 |

- **导入校验**（任一项失败返回 400）：
  - 支持包根目录或单一顶层目录结构；必须含 `SKILL.md`，frontmatter 需 `name`/`description`，正文非空（正文 ≤200KB）
  - 解压后总量 ≤10MB、文件数 ≤500、单文件 ≤2MB
  - 路径安全（拒绝绝对路径 / `..` / 空字节，防 zip-slip）；文件类型白名单（md/txt/json/yaml/py/js/ts/sql/png/jpg/pdf 等）
- **出参**：`201 created`，`data` 为 `Skill` 对象：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` / `name` | - | 技能名全局唯一 |
| `version` / `version_spec` | - | 平台版本号（每次升级 +1）/ 包内声明版本号 |
| `description` / `author` / `tags` | - | 元数据（来自 frontmatter） |
| `required_tools` | string[] | 依赖的 MCP 工具名 |
| `entry_content` | string | SKILL.md 指令正文（不含 frontmatter） |
| `size_bytes` / `file_count` | int | 包体积 / 文件数 |
| `status` | string | `active` / `disabled`（`disabled` 运行时不注入） |

- **错误**：`409 skill_conflict` 技能名已存在（提示可用强制升级）；`400` 包校验失败。

### 5.2 技能列表

- **接口**：`GET /api/v1/skills`
- **权限**：`skill:read`
- **入参**（query）：`q`（名称搜索）、`tag`、`status`（`active`/`disabled`）、`page`/`size`
- **出参**：`200`，分页 `items` 为 `SkillListItem`（`Skill` 字段 + `agent_count: int` + `in_use: bool`）。

### 5.3 技能详情

- **用途**：元数据 + 指令正文 + 文件清单。
- **接口**：`GET /api/v1/skills/:id`
- **权限**：`skill:read`
- **出参**：`200`，`data`：`{ "skill": Skill, "files": SkillFileMeta[] }`

`SkillFileMeta`：`{id, skill_id, path, size, sha256, created_at}`

### 5.4 启用 / 禁用

- **接口**：`PUT /api/v1/skills/:id`
- **权限**：`skill:write`
- **入参**（JSON body）：`{ "status": "active" | "disabled" }`
- **出参**：`200`，`data` 为更新后的 `Skill`。
- **说明**：`disabled` 技能运行时不注入（已关联的 Agent 不受影响，仅停止生效）。

### 5.5 删除技能

- **接口**：`DELETE /api/v1/skills/:id?force=true`
- **权限**：`skill:write`
- **行为**：
  - 有关联 Agent 且未 `force`：`409 skill_in_use`，`data.agents` 返回关联 Agent 列表
  - `force=true`：级联解绑后删除
- **出参**：`200`，`data`：`{ "message": "deleted" }`

### 5.6 资源文件清单

- **接口**：`GET /api/v1/skills/:id/files`
- **权限**：`skill:read`
- **出参**：`200`，`data.files` 为 `SkillFileMeta[]`（同 5.3，不含内容）。

### 5.7 资源文件内容

- **用途**：下载/预览单个资源文件（二进制原样返回，文本类型由前端自行渲染）。
- **接口**：`GET /api/v1/skills/:id/files/<path>`（`path` 为包内相对路径，含路径穿越防护）
- **权限**：`skill:read`
- **出参**：`200`，`Content-Type: application/octet-stream`，响应体为文件原始字节（非统一 JSON 封装）。

### 5.8 关联 Agent 列表

- **接口**：`GET /api/v1/skills/:id/agents`
- **权限**：`skill:read`
- **出参**：`200`，`data.agents`：`{agent_id, agent_name, bound_at}[]`

### 5.9 使用统计

- **接口**：`GET /api/v1/skills/:id/usage`
- **权限**：`skill:read`
- **出参**：`200`，`data`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `agent_count` | int | 关联 Agent 数 |
| `load_count_30d` | int | 近 30 天技能加载次数（`execution_meta.skill_calls` 聚合） |
| `last_used_at` | string \| null | 最近使用时间 |

---

## 6. MCP 管理

> MCP 服务器注册后：即时连通性检测 + 周期检测（`MCP_HEALTH_INTERVAL` 默认 1 分钟）+ 工具发现。凭证 AES-256-GCM 加密存储，API 永不回显明文。

### 6.1 注册 MCP 服务器

- **接口**：`POST /api/v1/mcp-servers`
- **权限**：`mcp:write`
- **入参**（JSON body，`CreateMCPRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `name` | string | 是 | 2-64 | 名称 |
| `endpoint` | string | 是 | ≤512 | 端点地址 |
| `transport` | string | 是 | `stdio` \| `sse` \| `http` | 传输类型 |
| `description` | string | 否 | ≤512 | 描述 |
| `tags` | string[] | 否 | - | 标签（用于分组过滤） |
| `credentials` | object | 否 | - | `{ "api_key": string, "headers": { "k": "v" } }`，加密存储 |

- **行为**：注册后立即执行一次连通性检测与工具发现；`status` 更新为 `connected` / `error`。
- **出参**：`201 created`，`data` 为 `MCPServer` 对象：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` / `name` / `endpoint` / `transport` / `description` / `tags` | - | 基本字段 |
| `status` | string | `pending` / `connected` / `disconnected` / `error` |
| `tools` | array | 已发现工具：`MCPTool[]` |
| `health_last_check` / `health_latency_ms` / `last_error` | - | 健康检测信息 |

`MCPTool`：`{name, description, inputSchema, requires_approval}`（`requires_approval=true` 时调用需先过人工审核）。

### 6.2 MCP 列表

- **接口**：`GET /api/v1/mcp-servers`
- **权限**：`mcp:read`
- **入参**（query）：`q`、`status`、`tag`、`page`/`size`
- **出参**：`200`，分页 `items` 为 `MCPServer[]`。

### 6.3 MCP 详情（含脱敏凭证）

- **接口**：`GET /api/v1/mcp-servers/:id`
- **权限**：`mcp:read`
- **出参**：`200`，`data`：`{ "server": MCPServer, "credentials": CredentialsView }`

`CredentialsView`：`{ api_key_set: bool, api_key_mask: string, header_keys: string[] }`（仅脱敏视图）。

### 6.4 更新 MCP 服务器

- **接口**：`PUT /api/v1/mcp-servers/:id`
- **权限**：`mcp:write`
- **入参**（JSON body，`UpdateMCPRequest`）：字段同「6.1」；`credentials` 为 `null` = 凭证保持不变，非 null = 全量替换。
- **行为**：endpoint 或凭证变更自动触发重检。
- **出参**：`200`，`data` 为 `MCPServer`。

### 6.5 删除 MCP 服务器

- **接口**：`DELETE /api/v1/mcp-servers/:id`
- **权限**：`mcp:write`
- **行为**：级联清理 Agent 绑定与健康历史。
- **出参**：`200`，`data`：`{ "message": "deleted" }`

### 6.6 手动连通性测试

- **接口**：`POST /api/v1/mcp-servers/:id/test`
- **权限**：`mcp:write`
- **出参**：`200`，`data` 为 `HealthResult`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `ok` | bool | 是否连通 |
| `status` | string | 更新后的服务器状态 |
| `latency_ms` | int | 检测耗时 |
| `server` | object | 服务器信息（name/version 等，若协议返回） |
| `tools_count` | int | 发现的工具数 |
| `error` | string | 失败原因（成功时省略） |

### 6.7 健康状态 + 检查历史

- **接口**：`GET /api/v1/mcp-servers/:id/health`
- **权限**：`mcp:read`
- **入参**（query）：`limit`（历史记录条数，默认 100）
- **出参**：`200`，`data`：

```json
{
  "status": "connected",
  "last_check": "2026-08-22T10:00:00+08:00",
  "latency_ms": 42,
  "last_error": "",
  "history": [ { "id": "uuid", "mcp_id": "uuid", "ok": true, "latency_ms": 42, "error": "", "created_at": "..." } ]
}
```

### 6.8 已发现工具列表

- **接口**：`GET /api/v1/mcp-servers/:id/tools`
- **权限**：`mcp:read`
- **出参**：`200`，`data`：`{ "tools": MCPTool[] }`（含各工具 `requires_approval` 开关状态）。

### 6.9 更新工具级审核开关

- **用途**：批量设置工具「调用需人工审核」（增量更新：仅更新所列工具，其余保持不变）。
- **接口**：`PUT /api/v1/mcp-servers/:id/tools`
- **权限**：`mcp:write`
- **入参**（JSON body，`UpdateToolApprovalsRequest`）：

```json
{ "tools": [ { "name": "ticket.create", "requires_approval": true }, { "name": "echo", "requires_approval": false } ] }
```

- 约束：`tools` 数组 1-100 项；`name` 必填 ≤128。
- **出参**：`200`，`data`：`{ "tools": MCPTool[] }`（更新后的工具列表）。
- **错误**：`400` 工具名不存在于已发现工具。

### 6.10 工具调用代理

- **用途**：平台代调用 MCP 工具（凭证由平台注入）；标记需审核的工具不直接执行，生成审核请求。
- **接口**：`POST /api/v1/mcp-servers/:id/tools/call`
- **权限**：`mcp:write`
- **入参**（JSON body）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `name` | string | 是 | ≤128，须为已发现工具 | 工具名 |
| `arguments` | object | 否 | - | 工具参数（按 `inputSchema`） |

- **出参**（两种）：
  - `200`：`data` 为 `CallResult`：`{ "content": [ { "type": "text", "text": "..." } ], "is_error": false }`
  - `202 pending_approval`：`data`：`{ "approval_id": "uuid", "approval": ToolApproval }`（工具未执行，等待审核）

### 6.11 已绑定 Agent 列表

- **接口**：`GET /api/v1/mcp-servers/:id/agents`
- **权限**：`mcp:read`
- **出参**：`200`，`data.agents` 为 `MCPAgentBinding[]`：`{id, mcp_id, agent_id, created_at}`。

### 6.12 绑定 Agent

- **用途**：建立 MCP ↔ Agent 绑定（访问控制：仅绑定的 Agent 运行时可调用该 MCP 的工具）。
- **接口**：`POST /api/v1/mcp-servers/:id/agents`
- **权限**：`mcp:write`
- **入参**（JSON body）：`{ "agent_id": "uuid" }`
- **出参**：`200`，`data`：`{ "message": "bound" }`

### 6.13 解绑 Agent

- **接口**：`DELETE /api/v1/mcp-servers/:id/agents/:agentId`
- **权限**：`mcp:write`
- **出参**：`200`，`data`：`{ "message": "unbound" }`

---

## 7. 模型管理

> 模型模板注册后：即时连通性探测 + 周期探测（`MODEL_HEALTH_INTERVAL` 默认 1 分钟）。运行时按 `priority`（越小越高）路由，异常或配额耗尽自动故障转移到低优先级模型。

### 7.1 创建模型模板

- **接口**：`POST /api/v1/model-templates`
- **权限**：`model:write`
- **入参**（JSON body，`CreateModelRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `name` | string | 是 | 2-64 | 模板名称 |
| `provider` | string | 是 | `openai` \| `anthropic` \| `google` \| `azure` \| `custom` | 提供商 |
| `model` | string | 是 | ≤128 | 模型名（如 `gpt-4o`） |
| `endpoint` | string | 否 | ≤512 | API 端点（自定义端点必填；留空用官方默认） |
| `api_key` | string | 否 | ≤512 | API Key（AES-256-GCM 加密存储） |
| `priority` | int | 否 | 默认 100 | 路由优先级，越小越高 |
| `status` | string | 否 | `active` \| `inactive`，默认 `active` | 状态（`inactive` 为手动停用，不参与路由） |
| `config` | object | 否 | - | 生成参数：`{temperature?, max_tokens?, top_p?}` |
| `tags` | string[] | 否 | - | 标签 |

- **行为**：注册后立即连通性探测。
- **出参**：`201 created`，`data`：`{ "template": ModelTemplate, "credentials": { "api_key_set": true, "api_key_mask": "sk-...****" } }`

`ModelTemplate` 字段：`id`、`name`、`provider`、`model`、`endpoint`、`config`、`status`（`active`/`inactive`/`error`）、`priority`、`tags`、`health_last_check`、`health_latency_ms`、`last_error`、`created_at`、`updated_at`。

### 7.2 模型列表

- **接口**：`GET /api/v1/model-templates`
- **权限**：`model:read`
- **入参**（query）：`q`、`provider`、`status`、`tag`、`page`/`size`
- **出参**：`200`，分页 `items` 为 `ModelTemplate[]`。

### 7.3 模型详情（含脱敏 API Key）

- **接口**：`GET /api/v1/model-templates/:id`
- **权限**：`model:read`
- **出参**：`200`，`data`：`{ "template": ModelTemplate, "credentials": { "api_key_set": bool, "api_key_mask": string } }`

### 7.4 更新模型模板

- **接口**：`PUT /api/v1/model-templates/:id`
- **权限**：`model:write`
- **入参**（JSON body，`UpdateModelRequest` = `CreateModelRequest` 字段 + 扩展）：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| （同 7.1 各字段） | - | - |
| `clear_api_key` | bool | `true` = 清空已存 API Key；`api_key` 留空 = 保持不变 |

- **行为**：连接参数变更触发重检。
- **出参**：`200`，`data`：`{ "template": ModelTemplate, "credentials": APIKeyView }`

### 7.5 删除模型模板

- **接口**：`DELETE /api/v1/model-templates/:id`
- **权限**：`model:write`
- **行为**：级联清理配额 + 用量 + 健康历史。
- **出参**：`200`，`data`：`{ "message": "deleted" }`

### 7.6 手动连通性测试

- **接口**：`POST /api/v1/model-templates/:id/test`
- **权限**：`model:write`
- **出参**：`200`，`data` 为 `ProbeView`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `ok` | bool | 是否连通 |
| `status` | string | 更新后的模板状态 |
| `latency_ms` | int | 探测耗时 |
| `models` / `models_count` | array / int | 对端返回的可用模型列表（部分提供商支持） |
| `error` | string | 失败原因 |

### 7.7 健康状态 + 检查历史

- **接口**：`GET /api/v1/model-templates/:id/health`
- **权限**：`model:read`
- **入参**（query）：`limit`（默认 100）
- **出参**：`200`，`data` 结构同 MCP 健康接口（`status`/`last_check`/`latency_ms`/`last_error`/`history[]`）。

### 7.8 单模型用量（配额 + 调用日志）

- **接口**：`GET /api/v1/model-templates/:id/usage`
- **权限**：`model:read`
- **入参**（query）：`limit`（调用日志条数，默认 100）
- **出参**：`200`，`data`：`{ "quota": QuotaView | null, "logs": ModelUsageLog[] }`

`QuotaView` 字段（= `ModelQuota` + 模板信息）：

| 字段 | 说明 |
| ---- | ---- |
| `daily_limit` / `monthly_limit` | 日/月调用次数上限（0=不限） |
| `daily_token_limit` / `monthly_token_limit` | 日/月 token 上限（0=不限） |
| `daily_used` / `monthly_used` / `daily_token_used` / `monthly_token_used` | 已用额度（跨日/跨月自动重置） |
| `template_name` / `model` / `provider` | 模板信息 |

`ModelUsageLog`：`{id, model_id, agent_id, ok, tokens, latency_ms, error, created_at}`

### 7.9 配额列表

- **接口**：`GET /api/v1/model-quota`
- **权限**：`model:read`
- **出参**：`200`，`data.items` 为 `QuotaView[]`（展示值已应用日/月重置逻辑）。

### 7.10 设置 / 更新配额

- **接口**：`PUT /api/v1/model-quota/:modelId`
- **权限**：`model:write`
- **入参**（JSON body，`QuotaRequest`；字段为 `null` = 保持不变）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `daily_limit` | int \| null | 否 | 日调用次数上限（0=不限） |
| `monthly_limit` | int \| null | 否 | 月调用次数上限（0=不限） |
| `daily_token_limit` | int \| null | 否 | 日 token 上限（0=不限） |
| `monthly_token_limit` | int \| null | 否 | 月 token 上限（0=不限） |

- **出参**：`200`，`data` 为更新后的 `QuotaView`。

### 7.11 全部模型用量概览

- **用途**：所有模型的配额 + 近 24h 用量聚合。
- **接口**：`GET /api/v1/model-usage`
- **权限**：`model:read`
- **出参**：`200`，`data.items` 为 `UsageSummary[]`：

| 字段 | 说明 |
| ---- | ---- |
| `model_id` / `template_name` / `model` / `provider` / `status` | 模板信息 |
| `daily_limit` / `daily_used` / `monthly_limit` / `monthly_used` | 次数配额与已用 |
| `daily_token_limit` / `daily_token_used` / `monthly_token_limit` / `monthly_token_used` | token 配额与已用 |
| `recent_calls` / `recent_tokens` / `recent_errors` | 近 24h 调用量 / token / 错误数 |

### 7.12 路由选择（dry-run）

- **用途**：按优先级 + 配额模拟一次路由选择，**不消耗配额**，用于验证路由结果。
- **接口**：`POST /api/v1/models/route`
- **权限**：`model:read`
- **入参**：无
- **出参**：`200`，`data` 为 `RouteResult`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `selected` | ModelTemplate \| null | 选中的模型（无可用模型时为 null） |
| `reason` | string | 选择/跳过原因 |
| `skipped` | array | 被跳过的模型：`{name, model, reason}[]`（异常状态 / 配额耗尽 / inactive） |
---

## 8. 审核中心 Approval

> MCP 工具人工审核（M4.5）。标记 `requires_approval=true` 的工具，任何来源（手动 / 对话 / 外部调用 / 工作流）的调用都会先挂起生成审核请求；通过 = 执行并回填结果，驳回 = 终止。

### 8.1 审核请求列表

- **接口**：`GET /api/v1/approvals`
- **权限**：`mcp:read`
- **入参**（query）：

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `status` | string | 否 | `pending` / `approved` / `rejected` / `expired` |
| `mcp_server_id` | string | 否 | 按 MCP 服务器过滤 |
| `tool` | string | 否 | 按工具名过滤 |
| `agent_id` | string | 否 | 按 Agent 过滤 |
| `source` | string | 否 | `manual` / `chat` / `api_invoke` / `workflow` / `runtime` |
| `from` / `to` | string | 否 | RFC3339 请求时间区间 |
| `page` / `size` | int | 否 | 分页 |

- **出参**：`200`，分页 `items` 为 `ApprovalView[]`：

`ToolApproval` 字段：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 审核请求 ID |
| `mcp_server_id` / `tool_name` | string | MCP 服务器 / 工具 |
| `agent_id` | string \| null | 发起调用的 Agent |
| `source` | string | 来源（见上） |
| `workflow_execution_id` | string \| null | 工作流执行 ID（`source=workflow` 时） |
| `chat_session_id` | string \| null | 对话会话 ID（`source=chat` 时） |
| `arguments` | object | 调用参数快照 |
| `status` | string | `pending` / `approved` / `rejected` / `expired` |
| `requested_at` / `expires_at` | string | 请求时间 / 超时时间 |
| `decided_by` / `decided_at` / `comment` | - | 审核人 / 审核时间 / 审核意见 |
| `result` | object | 执行结果（通过后回填） |
| `executed_at` | string \| null | 执行时间 |

`ApprovalView` = `ToolApproval` + `{mcp_name: string, agent_name: string}`。

### 8.2 审核全局配置

- **接口**：`GET /api/v1/approvals/settings`
- **权限**：`mcp:read`
- **出参**：`200`，`data` 为 `ApprovalSettings`：`{id, default_timeout_minutes (默认 30), on_timeout ("reject"|"approve"), updated_by, updated_at}`

### 8.3 更新审核全局配置

- **接口**：`PUT /api/v1/approvals/settings`
- **权限**：`mcp:write`
- **入参**（JSON body，`UpdateApprovalSettingsRequest`；字段可空 = 保持不变）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `default_timeout_minutes` | int | 否 | 1-1440 | 默认审核超时（分钟） |
| `on_timeout` | string | 否 | `reject` \| `approve` | 超时策略：自动驳回 / 自动通过 |

- **出参**：`200`，`data` 为更新后的 `ApprovalSettings`。

### 8.4 审核详情

- **接口**：`GET /api/v1/approvals/:id`
- **权限**：`mcp:read`
- **出参**：`200`，`data` 为 `ApprovalView`（含参数快照与执行结果）。

### 8.5 通过并执行

- **用途**：审核通过 → 执行该工具调用并回填结果（`source=workflow` 时恢复挂起的工作流执行；`source=chat`/`api_invoke` 时回填工具结果）。
- **接口**：`POST /api/v1/approvals/:id/approve`
- **权限**：`mcp:approve`
- **入参**（JSON body，可空）：`{ "comment": "审核意见 (≤512)" }`
- **出参**：`200`，`data` 为处置后的 `ApprovalView`（`status=approved`，`result` 已回填）。
- **错误**：`400` 请求已处置 / 已超时；`404` 不存在。

### 8.6 驳回

- **用途**：驳回该工具调用（`source=workflow` 时：节点失败、下游级联跳过、执行失败）。
- **接口**：`POST /api/v1/approvals/:id/reject`
- **权限**：`mcp:approve`
- **入参**（JSON body，可空）：`{ "comment": "驳回原因 (≤512)" }`
- **出参**：`200`，`data` 为处置后的 `ApprovalView`（`status=rejected`）。

---

## 9. 工作流 Workflow

> DAG 编排 + 执行引擎。工作流状态机：`draft` →（激活）→ `active`（可触发/可调度）→（归档）→ `archived`。每次保存生成版本快照；触发方式：手动 / cron / webhook。

### 9.1 创建工作流

- **接口**：`POST /api/v1/workflows`
- **权限**：`workflow:write`
- **入参**（JSON body，`CreateWorkflowRequest`）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `name` | string | 是 | 名称 ≤64，唯一（软删除后可重建） |
| `description` | string | 否 | 描述 |
| `definition` | object | 是 | DAG 定义（结构见「9.14 DAG 定义结构」），创建时自动校验 |
| `input_schema` | object | 否 | 执行输入 JSON Schema（展示用） |
| `output_schema` | object | 否 | 输出 JSON Schema（展示用） |
| `schedule` | object | 否 | 初始调度：`{cron, input, timezone}`（传入仅保存配置，`schedule_enabled` 仍为 `false`；需用 9.9 启用） |

- **行为**：自动生成 32 位 hex `webhook_token`；初始 `status=draft`、`version=1`。
- **出参**：`201 created`，`data` 为 `Workflow` 对象：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` / `name` / `description` | - | 基本字段 |
| `definition` | object | DAG 定义 |
| `status` | string | `draft` / `active` / `archived` |
| `input_schema` / `output_schema` | object | 输入/输出 Schema |
| `version` | int | 当前版本（保存递增） |
| `schedule` | object \| null | `{cron, input, timezone}` |
| `schedule_enabled` | bool | 调度是否启用 |
| `webhook_token` | string | Webhook 触发 token（32 位 hex） |

### 9.2 工作流列表

- **接口**：`GET /api/v1/workflows`
- **权限**：`workflow:read`
- **入参**（query）：`status`（`draft`/`active`/`archived`）、`page`/`size`
- **出参**：`200`，分页 `items` 为 `Workflow[]`。

### 9.3 执行看板

- **用途**：全部工作流执行的实时状态计数 + 最近 10 条执行（概览页「工作流看板」数据源）。
- **接口**：`GET /api/v1/workflows/dashboard`
- **权限**：`workflow:read`
- **出参**：`200`，`data`：

| 字段 | 说明 |
| ---- | ---- |
| `counts_by_status` | `{running, waiting_approval, success, failed, cancelled}` |
| `running` / `waiting_approval` / `success` / `failed` / `cancelled` | 各状态计数（同 `counts_by_status` 的扁平展开） |
| `recent` | 最近 10 条 `WorkflowExecution[]` |

### 9.4 校验 DAG 定义（不落库）

- **用途**：前端编辑器保存前校验；校验环检测 / 节点 / 边合法性。
- **接口**：`POST /api/v1/workflows/validate`
- **权限**：`workflow:write`
- **入参**（JSON body）：`{ "definition": <DAG 定义对象> }`
- **出参**：`200`，`data`：`{ "valid": true }`；校验失败 `400`（`message` 为具体原因）。

### 9.5 工作流详情

- **接口**：`GET /api/v1/workflows/:id`
- **权限**：`workflow:read`
- **出参**：`200`，`data` 为 `Workflow`。

### 9.6 更新工作流

- **用途**：更新名称/描述/DAG；`definition` 变更生成**新版本快照**（`version+1`）。
- **接口**：`PUT /api/v1/workflows/:id`
- **权限**：`workflow:write`
- **入参**（JSON body，`UpdateWorkflowRequest`；字段为 `null` = 保持不变）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `name` | string | 否 | ≤64 |
| `description` | string | 否 | - |
| `definition` | object | 否 | DAG 定义（变更则版本 +1 并重新校验） |
| `input_schema` / `output_schema` | object | 否 | - |

- **出参**：`200`，`data` 为更新后的 `Workflow`。

### 9.7 删除工作流

- **接口**：`DELETE /api/v1/workflows/:id`
- **权限**：`workflow:write`
- **约束**：存在活动执行（running / waiting_approval）时拒绝删除。
- **出参**：`200`，`data`：`{ "deleted": "<workflow-id>" }`（执行历史保留）。

### 9.8 激活 / 归档

- **激活**：`POST /api/v1/workflows/:id/activate`（`draft`/`archived` → `active`，之后可触发/可调度）
- **归档**：`POST /api/v1/workflows/:id/archive`（`active` → `archived`，停止调度且不可触发）
- **权限**：`workflow:write`
- **入参**：无
- **出参**：`200`，`data` 为状态更新后的 `Workflow`。

### 9.9 更新定时调度

- **接口**：`PUT /api/v1/workflows/:id/schedule`
- **权限**：`workflow:write`
- **入参**（JSON body，`UpdateScheduleRequest`）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `enabled` | bool | 是 | 是否启用定时调度（`true` 时必须存在有效 cron：本次传入或已有配置） |
| `cron` | string | 否 | 5 段 cron（分 时 日 月 周），如 `*/5 * * * *`；空 = 沿用已有配置 |
| `input` | object | 否 | 每次定时执行注入的固定输入；`null` = 沿用已有配置 |
| `timezone` | string | 否 | 时区（如 `Asia/Shanghai`）；空 = 已有配置，否则默认 `Asia/Shanghai` |

- **行为**：调度器启动时全量重建，运行中每 5 分钟对账自愈（DB 变更自动感知）；仅对 `active` 工作流生效。
- **出参**：`200`，`data` 为更新后的 `Workflow`（`schedule` / `schedule_enabled` 已更新）。

### 9.10 手动触发

- **接口**：`POST /api/v1/workflows/:id/trigger`
- **权限**：`workflow:execute`
- **约束**：工作流须为 `active`。
- **入参**（JSON body，可空）：`{ "input": { ... } }`（作为执行输入，节点内以 `$inputs.*` 引用）
- **出参**：`200`，`data` 为创建的 `WorkflowExecution`（`trigger_type=manual`）。执行**先落库再异步运行**，响应中的 `data.id` 即本次执行 ID，`status` 返回时通常为 `running`。

`WorkflowExecution` 字段：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 执行 ID |
| `workflow_id` / `workflow_name` / `workflow_version` | - | 工作流信息（执行时点版本） |
| `trigger_type` | string | `manual` / `cron` / `webhook` |
| `triggered_by` | string \| null | 触发人（webhook 为 null） |
| `status` | string | `running` / `waiting_approval` / `success` / `failed` / `cancelled` |
| `input` / `output` | object | 执行输入 / 输出（按节点 id 聚合各节点输出） |
| `trace_id` | string | 追踪 ID |
| `error` | string | 失败原因 |
| `started_at` / `finished_at` | string | 起止时间 |

### 9.11 版本历史

- **接口**：`GET /api/v1/workflows/:id/versions`
- **权限**：`workflow:read`
- **出参**：`200`，`data.items` 为 `WorkflowVersion[]`：`{id, workflow_id, version, definition, input_schema, output_schema, created_by, created_at}`

### 9.12 执行历史

- **接口**：`GET /api/v1/workflows/:id/executions`
- **权限**：`workflow:read`
- **入参**（query）：`status`、`trigger`（`manual`/`cron`/`webhook`）、`page`/`size`
- **出参**：`200`，分页 `items` 为 `WorkflowExecution[]`。

### 9.13 执行详情（含节点级记录）

- **用途**：执行追踪——节点级状态 / 尝试次数 / 耗时 / 输入输出 / 错误。
- **接口**：`GET /api/v1/workflow-executions/:id`
- **权限**：`workflow:read`
- **出参**：`200`，`data` = `WorkflowExecution` 字段 + `nodes: WorkflowNodeExecution[]`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` / `execution_id` / `node_id` | - | 记录 ID / 执行 ID / DAG 节点 ID |
| `node_type` / `node_name` | string | 节点类型 / 名称 |
| `status` | string | `pending` / `running` / `success` / `failed` / `skipped` / `waiting_approval` / `cancelled` |
| `attempt` | int | 尝试次数（含首次） |
| `input` / `output` | object | 解析后的节点配置 / 节点输出 |
| `error` | string | 错误信息 |
| `approval_id` | string \| null | 审核挂起时关联的审核请求 ID |
| `duration_ms` | int | 耗时 |
| `started_at` / `finished_at` | string | 起止时间 |

### 9.14 取消执行

- **用途**：取消运行中的执行（运行中节点转 `cancelled`，未开始节点级联 `cancelled`）。
- **接口**：`POST /api/v1/workflow-executions/:id/cancel`
- **权限**：`workflow:execute`
- **入参**：无
- **出参**：`200`，`data`：`{ "cancelled": "<execution-id>" }`

### 9.15 Webhook 触发（公开端点）

- **用途**：外部系统经 Webhook 直接触发工作流。
- **接口**：`POST /api/v1/webhooks/workflows/:token`
- **认证**：无 JWT；`token` 为创建工作流时生成的 32 位 hex `webhook_token`（非法 token 返回 404）
- **入参**（JSON body，可为空对象）：请求体即执行输入（节点内以 `$inputs.*` 引用）；须为 JSON 对象
- **出参**：`200`，`data` 为创建的 `WorkflowExecution`（`trigger_type=webhook`），示例：

```json
{
  "code": "success", "message": "ok",
  "data": {
    "id": "f8a3c2e1-....",
    "workflow_id": "b7d1...",
    "workflow_name": "客服工单处理",
    "workflow_version": 3,
    "trigger_type": "webhook",
    "triggered_by": null,
    "status": "running",
    "input": { "order_id": "SO-1001" },
    "output": null,
    "trace_id": "wf-9f3a2b",
    "error": "",
    "started_at": "2026-08-22T10:00:00+08:00",
    "finished_at": null
  }
}
```

- **跟踪执行状态**：取 `data.id` 轮询（状态流转 `running` / `waiting_approval` → `success` / `failed` / `cancelled`）：
  - 外部系统：`GET /api/v1/webhooks/workflows/:token/executions/<data.id>`（见 9.16，**仅需 webhook token**，返回状态视图，不含输入/输出 payload）
  - 平台内部：`GET /api/v1/workflow-executions/:id`（见 9.13，需 JWT，含节点级输入输出完整详情）
- **约束**：工作流须为 `active`。

### 9.16 执行状态公开查询（Webhook Token）

- **用途**：外部系统仅凭 webhook token 轮询**本工作流**的执行状态（无需用户 JWT）。
- **接口**：`GET /api/v1/webhooks/workflows/:token/executions/:id`
- **认证**：无 JWT；`token` 为工作流的 `webhook_token`（与 9.15 相同）
- **入参**（path）：

| 参数 | 类型 | 说明 |
| ---- | ---- | ---- |
| `token` | string | 工作流 webhook_token（32 位 hex） |
| `id` | string | 执行 ID（9.15 触发响应的 `data.id`） |

- **权限边界**：
  - `token` 只能查询**其所属工作流**的执行；跨工作流查询、token 不存在均返回 `404`（不泄露其他工作流执行的存在性）
  - 仅返回**状态视图**，不含执行输入/输出与节点输入/输出 payload（完整详情请用 9.13 的 JWT 端点）
- **出参**：`200`，`data` 为 `WebhookExecutionView`，示例：

```json
{
  "code": "success", "message": "ok",
  "data": {
    "id": "9ff56a0e-....",
    "workflow_id": "b7d1...",
    "workflow_name": "客服工单处理",
    "workflow_version": 3,
    "trigger_type": "webhook",
    "status": "success",
    "trace_id": "wf-9f3a2b",
    "started_at": "2026-08-22T10:00:00+08:00",
    "finished_at": "2026-08-22T10:00:02+08:00",
    "nodes": [
      {
        "node_id": "n1",
        "node_type": "mcp_tool",
        "node_name": "创建工单",
        "status": "success",
        "attempt": 1,
        "duration_ms": 120
      }
    ]
  }
}
```

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 执行 ID |
| `workflow_id` / `workflow_name` / `workflow_version` | - | 工作流信息（执行时点版本） |
| `trigger_type` | string | `manual` / `cron` / `webhook` |
| `status` | string | `running` / `waiting_approval` / `success` / `failed` / `cancelled` |
| `error` | string | 失败原因（成功时省略） |
| `trace_id` | string | 追踪 ID |
| `started_at` / `finished_at` | string | 起止时间（未结束时 `finished_at` 为 null） |
| `nodes[]` | array | 节点级状态：`{node_id, node_type, node_name, status, attempt, error, approval_id, duration_ms}`（不含节点输入/输出） |

- **典型轮询**：触发后每 1-5s 轮询一次，`status` 进入终态（`success` / `failed` / `cancelled`）即停止；`waiting_approval` 表示有节点等待人工审核（`approval_id` 可关联审核详情，处置需平台内操作）。
- **错误**：`404` token 无效 / 执行不存在 / 执行不属于该工作流 / 执行 ID 非 UUID 格式。

### 9.16.1 AI 自动生成工作流

- **用途**：用自然语言描述业务流程，LLM 自动编排为平台可执行的工作流 DAG 草稿（M5 Phase 2）。**不落库**：前端在编辑器/列表页预览确认后，再经 9.1 创建或 9.6 更新保存。
- **接口**：`POST /api/v1/workflows/ai-generate`
- **权限**：`workflow:write`
- **入参**（JSON body）：

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `description` | string | 是 | 自然语言流程描述（≤2000 字），如「收到工单后用客服 Agent 分析，严重级别高则创建运维工单」 |

- **行为**：
  1. 收集平台上下文（可用 Agent 目录 + MCP 服务器及其已发现工具，各 ≤20 条）组装提示词；
  2. 经模型路由（与对话同一故障转移/配额链路）调用 LLM，要求输出严格 JSON；
  3. 解析应答（容忍 Markdown 代码块包裹），执行 9.4 同款的 DAG 结构校验 + 资源存在性校验（`agent_id` / `mcp_server_id` / `tool` 必须真实存在，防 LLM 幻觉）；
  4. 校验失败携带错误信息自动重试 1 次（最多 2 次尝试）；全部失败返回 `400`。
- **出参**：`200`，`data`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `name` | string | 工作流名称（AI 建议，≤64；前端可覆盖） |
| `description` | string | 工作流描述（AI 生成，缺省时回退用户描述） |
| `definition` | object | 校验通过的 DAG 定义（结构见「9.17 DAG 定义结构」，`version` 恒为 1） |
| `input_schema` | object | 工作流输入参数 JSON Schema（无输入参数时省略） |
| `model` / `model_id` / `model_name` | string | 实际使用的模型模板 / 模板 ID / 模型名 |
| `attempts` | int | 实际尝试次数（1 或 2；`2` 表示首次未通过校验后自动修正） |
| `total_tokens` | int | 模型消耗 token |

- **错误**：`400` `no_model_available`（无可用模型模板）/ 描述为空或过长 / 两次尝试后定义仍未通过校验；`503` `ai_generate_unavailable`（AI 生成未接线）。
- **前端入口**：工作流列表页「AI 生成」（确认后自动创建草稿并进入编辑器）、工作流编辑器工具栏「AI 生成」（确认后替换画布，需手动保存）。

### 9.17 DAG 定义结构

`definition` 对象（`WorkflowDefinition`）：

```json
{
  "version": 1,
  "nodes": [
    {
      "id": "n1",
      "type": "agent",
      "name": "工单处理",
      "config": { "agent_id": "uuid", "message": "处理工单 $inputs.title" },
      "retry": { "max_attempts": 3, "interval_seconds": 5, "backoff": "fixed" },
      "timeout_seconds": 300
    }
  ],
  "edges": [
    { "id": "e1", "source": "n1", "target": "n2" },
    { "id": "e2", "source": "c1", "target": "n3", "condition": "true" },
    { "id": "e3", "source": "c1", "target": "n4", "condition": "false" }
  ]
}
```

- `nodes[]`（`WorkflowNodeDef`）：`id`（DAG 内唯一）、`type`、`name`、`config`（按类型，见下）、`retry`（可选）、`timeout_seconds`（可选，[0,3600]，默认 300）。
- `edges[]`（`WorkflowEdgeDef`）：`id`、`source`、`target`、`condition`（仅 condition 节点出口使用，`true`|`false`）。
- `retry`（`NodeRetryPolicy`）：`max_attempts`（总尝试次数含首次，[1,10]，默认 1）、`interval_seconds`（[0,600]）、`backoff`（`fixed` 默认 | `exponential`，间隔 × 2^(attempt-1)）。
- 规模上限：≤100 节点 / ≤500 边；环检测（Kahn 拓扑）；节点依赖所有前驱成功才就绪，上游失败/未选中分支的下游级联 `skipped`。

**节点 `config` 与输出**（按类型）：

| 类型 | config 字段 | 节点输出（下游经 `$nodes.<id>.<字段>` 引用） |
| ---- | ---------- | -------------------------------------------- |
| `agent` | `agent_id`（Agent ID）、`message`（提示词，支持变量） | `reply`、`session_id`、`model_name`、`total_tokens`、`latency_ms`、`mcp_calls`、`pending_approvals` |
| `mcp_tool` | `mcp_server_id`、`tool`（工具名）、`arguments`（参数对象，支持变量） | `content`（原始块）、`text`（展平文本）、`is_error`；工具需审核时执行挂起 `waiting_approval`，审批通过后恢复 |
| `http` | `url`、`method`（默认 GET）、`headers`（对象）、`body`（对象，非 GET/DELETE 时发送） | `status_code`、`body`（JSON 或文本）、`latency_ms` |
| `delay` | `seconds`（(0,3600]） | `waited_seconds` |
| `condition` | `left`、`operator`（`==` `!=` `>` `<` `>=` `<=` `contains` `exists`）、`right`（`exists` 时可省略） | `result`（bool）、`chosen`（`true`/`false`，决定走哪条出口边） |

**变量引用**（config 字符串/对象中）：

| 变量 | 说明 |
| ---- | ---- |
| `$inputs.<path>` | 执行输入（手动触发的 `input` / cron 的 `input` / webhook payload） |
| `$nodes.<节点id>.<path>` | 上游节点输出字段 |
| `$execution.id` | 当前执行 ID |

路径支持点号与数组下标（如 `$nodes.n1.reply`、`$inputs.items[0].name`）；整串引用保留原始类型，嵌入引用做文本格式化（如 `"hello $inputs.name"`）。

---

## 10. 平台设置 Platform

### 10.1 获取平台设置

- **用途**：获取平台名与平台图标（登录页 / 侧边导航 / 浏览器标签页展示）。
- **接口**：`GET /api/v1/platform/settings`
- **认证**：公开端点（无需 JWT, 品牌信息非敏感）
- **入参**：无
- **出参**：`200`，`data` 为 `PlatformInfo`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `name` | string | 平台名（默认 `Agent 管理平台`） |
| `icon` | string | 平台图标（base64 data URL: `data:image/png|jpeg|svg+xml|webp|gif;base64,...`）；空串 = 使用内置默认图标 |
| `updated_at` | string | 最近更新时间 `YYYY-MM-DD HH:mm:ss`（从未更新时省略） |

```json
{
  "code": "success", "message": "ok",
  "data": { "name": "Agent 管理平台", "icon": "data:image/png;base64,iVBORw0KGgo=", "updated_at": "2026-08-24 10:00:00" }
}
```

### 10.2 更新平台设置

- **接口**：`PUT /api/v1/platform/settings`
- **权限**：`platform:manage`（内置 admin 角色）
- **入参**（JSON body，`UpdatePlatformRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `name` | string | 是 | 1-64 个字符 | 平台名（首尾空白自动去除） |
| `icon` | string \| null | 否 | base64 data URL, 原图 ≤ 1MB | `null` = 不修改；空串 = 清除自定义图标；其余须为 PNG / JPG / SVG / WebP / GIF 的 data URL |

- **出参**：`200`，`data` 为更新后的 `PlatformInfo`。
- 变更写入审计日志（`action=platform.update`, `resource=platform`, detail 含名称前后值与图标是否变更）。

---

## 附录：枚举值速查

| 枚举 | 取值 |
| ---- | ---- |
| Agent 状态 | `idle` / `running` / `stopped` / `error` |
| 用户 / 角色状态 | `1`=启用 / `0`=停用 |
| MCP 传输类型 | `stdio` / `sse` / `http` |
| MCP 状态 | `pending` / `connected` / `disconnected` / `error` |
| 模型提供商 | `openai` / `anthropic` / `google` / `azure` / `custom` |
| 模型状态 | `active` / `inactive`（手动停用）/ `error`（探测异常） |
| 技能状态 | `active` / `disabled` |
| 技能注入模式 | `metadata_injection`（默认，渐进式披露 + `load_skill`）/ `full_injection`（全文注入） |
| 审核状态 | `pending` / `approved` / `rejected` / `expired` |
| 审核来源 | `manual` / `chat` / `api_invoke` / `workflow` / `runtime` |
| 审核超时策略 | `reject`（自动驳回，默认）/ `approve`（自动通过） |
| 工作流状态 | `draft` / `active` / `archived` |
| 执行状态 | `running` / `waiting_approval` / `success` / `failed` / `cancelled` |
| 节点状态 | `pending` / `running` / `success` / `failed` / `skipped` / `waiting_approval` / `cancelled` |
| 触发方式 | `manual` / `cron` / `webhook` |
| 节点类型 | `agent` / `mcp_tool` / `http` / `delay` / `condition` |
| 条件操作符 | `==` / `!=` / `>` / `<` / `>=` / `<=` / `contains` / `exists` |
| 日志级别 | `info` / `warn` / `error` |
