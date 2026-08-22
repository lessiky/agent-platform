# M2 Agent 管理 - 实现总结

> **版本：** v1.1
> **日期：** 2026-08-18
> **范围：** 开发计划 Week 3 + Week 5 后端 + Week 4 前端

---

## 1. 已完成内容

### 1.1 数据模型（6 张表，GORM 自动迁移）

| 表 | 说明 |
|----|------|
| `agents` | Agent 定义。name 唯一、status (idle/running/stopped/error)、version 当前配置版本、config JSONB (model/system_prompt/temperature/max_tokens/tools)、model_id/team_id 预留 (M4/团队) |
| `agent_versions` | 配置版本快照 (agent_id, version 联合唯一)，支持回滚 |
| `agent_instances` | 运行实例 (MVP 一个 Agent 一个实例，unique agent_id)。status/started_at/stopped_at/last_heartbeat |
| `agent_logs` | 运行日志。level (debug/info/warn/error) + message，agent_id+created_at 复合索引，运行时自动裁剪保留最近 5000 条 |
| `agent_api_keys` | API Key。仅存 SHA-256 摘要 + 展示前缀，明文只在创建时返回一次 |
| `agent_call_stats` | 按天聚合的调用统计 (calls/errors/total_tokens/total_latency_ms)，upsert 累加 |

### 1.2 代码结构

`
backend/internal/
├── model/agent.go              # 6 个 GORM 模型
├── repository/agent_repository.go  # 6 个仓储接口+实现
├── runtime/runtime.go          # 进程内运行时管理器 (启停/心跳/模拟流量)
├── service/agent_service.go    # 业务服务层
└── api/agent/handler.go        # HTTP Handler + 路由
`

分层遵循 coding-standards.md：handler 只解析请求，service 管业务，repository 管数据。
路由全部挂在 `middleware.Auth()` + `middleware.AuthCheck("agent:read"/"agent:write")` 上（任务 3.6）。

### 1.3 运行时设计（Phase 1 模拟运行时）

Phase 1 尚无真实 Agent 框架（MCP/模型分别在 M3/M4 接入），`runtime.Runtime` 以进程内 goroutine 模拟实例生命周期：

- **启动**：`Start()` 派生后台 goroutine，写入 `instance started` 日志
- **心跳**：每 5s 刷新 `last_heartbeat`（状态看板据此判断存活，满足 < 5s 状态更新延迟要求）
- **日志**：心跳/请求处理日志写入 `agent_logs`，支持搜索
- **模拟流量**：每 8~15s 模拟一次调用（8% 失败率，200~2000ms 延迟，50~500 tokens），产生真实可查的调用统计
- **对账**：服务重启时把遗留的活动实例标记为 error 并写日志（`ReconcileInstances`）
- **优雅退出**：收到 SIGINT/SIGTERM 先 `Runtime.Shutdown()` 再关 HTTP 服务

> 该包对外接口（Start/Stop/IsRunning/Shutdown）保持稳定，Phase 2 可替换为真实调度器。
> 模拟流量可用 `runtime.SetSimulateTraffic(false)` 关闭。

### 1.4 API 全集（PRD 6.1 + 管理扩展）

`
POST   /api/v1/agents                  # 创建
GET    /api/v1/agents                  # 列表 (q/status/page/size)
GET    /api/v1/agents/dashboard        # 状态看板 (状态计数+运行中实例)
GET    /api/v1/agents/:id              # 详情 (agent + instance)
PUT    /api/v1/agents/:id              # 更新 (仅停止态, 版本+1)
DELETE /api/v1/agents/:id              # 删除 (仅停止态)
POST   /api/v1/agents/:id/start        # 启动实例
POST   /api/v1/agents/:id/stop         # 停止实例
GET    /api/v1/agents/:id/metrics      # 调用统计 (from/to, 默认近 7 天)
GET    /api/v1/agents/:id/logs         # 运行日志 (level/keyword/since)
GET    /api/v1/agents/:id/versions     # 版本历史
POST   /api/v1/agents/:id/rollback     # 回滚 {"version": N}
POST   /api/v1/agents/:id/invoke       # 外部调用 (API Key 认证, 不走 JWT, 2026-08-19; 返回模型应答 + 可选 session_id, 2026-08-21)
POST   /api/v1/agents/:id/keys         # 创建 API Key (可选 expires_at, RFC3339)
GET    /api/v1/agents/:id/keys         # API Key 列表
DELETE /api/v1/agents/:id/keys/:keyId  # 吊销 API Key
POST   /api/v1/agents/:id/keys/:keyId/delete # 删除已吊销的 API Key (2026-08-21)
`

### 1.5 关键业务规则

- 名称全局唯一（创建/更新时校验，含数据库 unique 约束兜底）
- 运行中禁止更新/删除/回滚（返回 400），防止配置与运行时不一致
- 更新/回滚均产生新版本快照，回滚是"恢复到历史配置"而非删除当前版本
- API Key 明文 `akp_` + 64 位 hex，创建后仅返回一次；列表只暴露前缀
- 日志表按 Agent 裁剪（保留 5000 条），防止无限膨胀

### 1.6 前端（Week 4，2026-08-18 完成）

技术栈：Vite 5 + React 18 + TypeScript + AntD 5 + Zustand + React Router 6 + axios，结构遵循 `docs/frontend/component-guide.md`（`@` 别名 → `src/`）。

| 模块 | 说明 |
|----|------|
| `api/client.ts` | axios 实例：token 注入、ApiEnvelope 统一解包（拦截器返回 `response.data`）、统一错误消息 |
| `api/{auth,agent}.ts` | 认证 / Agent API 封装 |
| `store/auth-store.ts` | token/username 持久化 localStorage（`access_token`/`username`） |
| `router/{index,guards}.tsx` | 路由 + 登录守卫（无 token 跳 /login） |
| `layouts/MainLayout` | 侧边栏菜单 + 顶栏（M3/M4/M5 占位禁用） |
| `pages/dashboard` | 状态看板：4 统计卡片 + 运行中 Agent 表，5s 轮询 |
| `pages/agent/*` | 列表（搜索防抖/状态过滤/启停/删除，10s 轮询）、创建/编辑（运行中只读）、详情（配置/版本历史/API Key/调用统计/日志 5 页签）、日志面板（3s 自动刷新 + 关键词/级别过滤）、版本回滚、API Key（明文仅一次） |
| `pages/auth/LoginPage` | 登录 + 注册 tabs（注册成功后自动登录取 token） |

验证：`npm run build`（tsc + vite）通过；Playwright 全链路冒烟通过（登录 → 新建 Agent → 启动实例 → 日志/指标 → 列表/看板），截图见 `output/playwright/`。

前端已知坑：
- 本机防火墙拦截 5173（EACCES），vite 固定 `127.0.0.1:8090`（`vite.config.ts` 有注释），proxy `/api` → `http://localhost:8080`
- 后端 register 不返回 token，注册成功后前端自动调 login 获取
- antd 静态 `message` 无法消费 context，全部页面用 `App.useApp()`
---

## 2. 端到端验证（2026-08-18 全部通过）

`
认证      注册/登录/无 token 401                                PASS
CRUD      创建(v1,idle)/重名400/列表/搜索/过滤/分页/详情/删除     PASS
启停      start->running / 重复 start 400 / stop->stopped        PASS
          重复 stop 400 / 运行中删除 400 / 删除后 404             PASS
心跳      dashboard last_heartbeat 5s 内刷新                      PASS
日志      生成/keyword 搜索/level 过滤/不存在 agent 404           PASS
指标      calls=11 errors=1 tokens=2996 avgLatency=1252ms        PASS
          按天序列/自定义时间范围                                 PASS
看板      状态计数 + 运行中 Agent 列表                            PASS
API Key   创建(明文一次)/列表(无摘要)/吊销/重复吊销 400           PASS
版本      更新->v2 / 版本历史 / 回滚 v1->v3 配置还原 / 缺失版本400 PASS
对账      进程重启后 running 实例自动标记 error + 日志            PASS
`

---

## 3. 依赖变更（M1 基线之上）

`
gorm.io/gorm          v1.25.5 -> v1.25.9   (datatypes v1.2.1 要求)
gorm.io/datatypes     v1.2.1 (新增, JSONB)
github.com/jackc/pgx  v5.4.3 -> v5.5.5     (datatypes v1.2.1 要求)
`

### 3.1 关键坑：AutoMigrate 对已存在表报 "insufficient arguments"

**现象**：`db.AutoMigrate` 扫描已存在的表时，驱动内部
`Session().Table(name).Limit(1).Rows()` 查询报 pgx `insufficient arguments`，
全新空库不触发（建表路径不走该检查），因此空库首次启动可掩盖此问题。

**根因**：gorm postgres driver 的 `GetRows` 会把 `pgx.QueryExecModeSimpleProtocol`
预置到 `Statement.Vars` 头部；GORM 生成 LIMIT 占位符时用 `len(Vars)` 编号，
占位符变成 `$2` 而参数只剩 1 个（pgx 消费模式参数后），sanitize 报参数不足。

**修复**：`database.Init` 开启 `PrepareStmt: true`，驱动不再注入模式参数，走预编译路径。
已在代码中注释说明。**后续若升级 gorm/driver 版本，请回归验证此行为。**

### 3.2 其他修复

- PostgreSQL 16 的 `ON CONFLICT DO UPDATE SET` 中裸列名歧义（`column "calls" is ambiguous`），
  upsert 表达式改为表名限定：`"agent_call_stats".calls + EXCLUDED.calls`
- 统计查询 `total_latency_ms` 列与 Go 字段名不一致，Scan 结构体加 `gorm:"column:"` tag

---

## 4. 与 PRD 的偏差说明

| 项 | PRD | 当前实现 | 说明 |
|----|-----|----------|------|
| 多实例部署 | P0 | 单实例 (1 Agent : 1 实例) | 实例表结构支持扩展，API 未带实例参数，MVP 先保单实例 |
| Agent 分组/团队 | P1 | team_id 字段预留 | 团队模块未排期 |
| Agent 配额 | P2 | 未实现 | 留待 Phase 2 |
| 告警规则/通知 | P1 (Week 5.3/5.4) | 未实现 | 需要通知渠道选型，建议与 M5 告警统一设计 |
| 性能画像 P50/P95/P99 | P2 | 未实现 (现有按天聚合) | 需要更细粒度存储 |
| 审计日志 | P0 | 未独立实现 | 依赖 middleware.getUserPermissions 真实化 + 审计表 |
| API Key 调用校验 | - | 已完成 (2026-08-19) | /invoke 入口 + status/过期校验 + last_used_at 更新, 见第 7 节 |

---

## 5. 运行方式

`
make docker-up     # 依赖 (已运行)
cd backend
go run ./cmd/server

# 前端 (需先启动后端)
cd frontend
npm install    # 首次
npm run dev    # http://127.0.0.1:8090 (本机 5173 被防火墙拦截, 固定 8090)
npm run build  # 生产构建 -> dist/
`

示例：

`
# 登录
curl -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' -d '{"username":"...","password":"..."}'

# 创建 Agent
curl -X POST localhost:8080/api/v1/agents -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "name": "my-agent", "model": "gpt-4o",
  "system_prompt": "You are helpful.", "temperature": 0.7,
  "tools": ["kb.search"]
}'

# 启动 / 看日志 / 看指标
curl -X POST  localhost:8080/api/v1/agents/$ID/start -H "Authorization: Bearer $TOKEN"
curl localhost:8080/api/v1/agents/$ID/logs?keyword=request -H "Authorization: Bearer $TOKEN"
curl localhost:8080/api/v1/agents/$ID/metrics -H "Authorization: Bearer $TOKEN"
curl localhost:8080/api/v1/agents/dashboard -H "Authorization: Bearer $TOKEN"
`

---

## 6. 待办（M2 剩余 + 衔接项）

1. ~~**Week 4 前端**~~ 已完成（见 1.6）：框架 + Agent 全部页面 + Playwright 冒烟通过
2. **Week 5.3/5.4 告警**：错误率/延迟阈值规则 + 邮件/webhook 通知
3. RBAC 落地：`middleware.getUserPermissions` 目前是硬编码桩，需接 user_roles/role_permissions 真实查询，并给 seed 用户分配权限
4. ~~`agent_api_keys` 的 `expires_at` 校验、`last_used_at` 更新~~ 已完成 (2026-08-19, 见第 7 节): /invoke 调用入口 + 校验 + 创建时可选过期
5. 审计日志表 + 记录 Agent 增删改操作

---

## 7. API Key 调用链接入 (2026-08-19)

M3/M4 全部完成后, 实现 M2 延后的 "API Key 调用校验", 完成待办 #4。

### 7.1 外部调用入口 (新增)

- `POST /api/v1/agents/:id/invoke` (不走用户 JWT, 使用 Agent 自身 API Key 认证): `Authorization: Bearer akp_...`, 请求体 `{"message": "..."}`。
- 调用链复用: `runtime.InvokeOnce` 与模拟流量共用 `executeCall` —— 模型路由消费配额 (M4, 优先 Agent config.model, 日志 `reason=agent_preferred`) + 绑定 MCP 工具调用 (M3, 按 config.tools 过滤), 写调用统计 (agent_call_stats) 与运行日志 (agent_logs, `api invoke ok trace_id=...`)。
- 响应: `model_ok / model_detail / mcp_details / tokens / latency_ms / key_prefix`。

### 7.2 Key 校验与使用记录 (待办 #4)

校验顺序: SHA-256 摘要查库 (`GetByHash`) -> 归属校验 (key.AgentID == 路径参数) -> 状态校验 (已吊销 -> 401) -> **expires_at 校验 (已过期 -> 401)** -> Agent 存在性 -> **更新 last_used_at** (失败仅告警不阻断) -> 执行调用链。

- 摘要不匹配返回 401 `无效的 API Key`; key 不属于该 Agent 返回 401 (key 泄露不能跨 Agent 调用)。
- 创建支持可选 `expires_at` (RFC3339 未来时间, 过去/格式错均 400); 前端创建弹窗加可选过期时间选择 (DatePicker showTime)。
- 前端 key 列表: 新增 "过期时间" 列 (null 显示 "永久"), 状态列增加 "已过期" 红 tag (active 但已过 expires_at)。

### 7.3 端到端验证 (2026-08-19 全部通过)

```
调用    无过期 key -> 200, last_used_at 更新, 统计 +1, model/MCP 日志齐全       PASS
过期    +5s 过期 key: 过期前调用 200 / 过期后调用 401 (API Key 已过期)          PASS
吊销    已吊销 key -> 401 (API Key 已吊销)                                     PASS
负例    随机 key 401 (无效) / 无 header 401 (缺少) / 他人 Agent key 401 (不属于) PASS
创建    过去 expires_at 400 / 格式错 400 / 缺 message 400                       PASS
副作用  agent_call_stats 2 calls / 122 tokens; mock-gpt-2 用量日志 2 条
        (reason=agent_preferred); 删除 agent 后 mock-kb 绑定无孤儿              PASS
前端    Playwright 冒烟: "过期时间" 列 (永久) / "已过期" 状态 / 创建弹窗
        DatePicker / 0 控制台错误                                              PASS
```

### 7.4 /invoke 升级: 返回模型应答 (2026-08-21)

定位升级: 从 "验证/触发端点" 升级为真正的对外服务入口 (OpenAI chat completions 风格)。

- **执行链复用**: `agentService.InvokeAgent` 不再走 `runtime.InvokeOnce`, 改走对话执行链 (`chatService.Invoke`, 与网页对话同构): 模型路由/故障转移/配额 (M4) + 工具调用多轮 + M4.5 审核门禁 + 调用统计 (agent_call_stats) + 执行日志 (前缀 `api invoke`)。
- **响应**: 新增 `reply` (模型应答) / `model` / `model_name` / `session_id` / `message_id`; 保留 `model_ok / model_detail / mcp_details / tokens / latency_ms / key_prefix / pending_approvals`。有待审核工具时仍返回 202。
- **多轮**: 请求体可选 `session_id` (须归属该 Agent: 不存在 404 / 不归属 400); 指定时加载会话历史且本轮落库 (网页对话历史可见); 未指定时 stateless 执行 (不创建会话、不落库)。
- **降级兼容**: 无可用模型模板时回退旧链路 (MCP 调用 + token 估算, `model_ok=false`), 保持 2026-08-19 行为; 模型全部调用失败返回 500。
- **代码结构**: `chatService.Chat` 核心抽为 `runTurn` (Chat/Invoke 共用, session=nil 即 stateless); `RouteAndChat` "无可用模型模板" 返回哨兵错误 `ErrNoModelAvailable` (code `no_model_available`)。
