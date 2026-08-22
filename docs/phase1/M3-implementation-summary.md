# M3 MCP 管理 - 实现总结

> **版本：** v1.0
> **日期：** 2026-08-18
> **范围：** 开发计划 Week 6 (后端核心) + Week 7 (前端) + Week 8 (Agent-MCP 集成 + E2E)

---

## 1. 已完成内容

### 1.1 数据模型（3 张表，GORM 自动迁移）

| 表 | 说明 |
|----|------|
| `mcp_servers` | MCP 服务器定义。name 唯一、endpoint、transport (stdio/sse/http)、status (pending/connected/disconnected/error)、tools JSONB (已发现工具)、credentials bytea (AES-256-GCM 密文)、tags JSONB、last_error、健康字段 (health_last_check / health_latency_ms) |
| `mcp_health_logs` | 连通性检查历史。ok / latency_ms / error，mcp_id+created_at 复合索引，按服务器自动裁剪保留最近 N 条 |
| `mcp_agent_bindings` | MCP <-> Agent 绑定 (访问控制)。mcp_id+agent_id 联合唯一 |

### 1.2 代码结构

```
backend/internal/
├── model/mcp.go                 # 3 个 GORM 模型 + transport/status 常量
├── crypto/aesgcm.go             # AES-256-GCM (nonce‖密文, 64 hex 密钥)
├── mcpclient/client.go          # JSON-RPC 2.0 MCP 客户端
├── repository/mcp_repository.go # 3 个仓储 (List 支持 q/status/tag 过滤)
├── service/mcp_service.go       # 业务服务层
├── service/mcp_health_checker.go# 定时健康检查器
└── api/mcp/handler.go           # HTTP Handler + 路由
```

分层遵循 coding-standards.md：handler 只解析请求，service 管业务，repository 管数据。
路由全部挂在 `middleware.Auth()` + `middleware.AuthCheck("mcp:read"/"mcp:write")` 上。

### 1.3 MCP 客户端（internal/mcpclient，自实现，无第三方 SDK）

- **initialize**：协议握手，返回 serverInfo
- **tools/list**：工具发现 (name/description/inputSchema)
- **tools/call**：工具调用，返回 content 块
- **http transport**：POST JSON-RPC，兼容两种响应：纯 JSON、SSE 流 (按 id 匹配目标响应)
- **sse transport (legacy)**：GET 消息流端点获取消息地址 (data 为路径或 {"endpoint": ...})，握手不可用时回退直连 POST
- JSON-RPC 字段用 camelCase (`protocolVersion` / `serverInfo` / `inputSchema`)——早期开发中 snake_case 映射不上，已踩坑修正

### 1.4 凭证加密（PRD 2.2.3）

- AES-256-GCM，密文 = nonce(12B)‖ciphertext，密钥 `MCP_CREDENTIALS_KEY` (64 hex，启动时必填校验，缺失 fatal)
- 明文仅在创建请求中出现；API 一律返回脱敏视图 (api_key 前 4 + **** + 后 4，headers 仅回显 key 名)
- 编辑时 api_key 留空 = 不变更；支持整体清空凭证
- 密钥轮换后解密失败不会崩溃，健康检查返回明确错误 `failed to decrypt credentials (key changed?)`

### 1.5 健康检查与工具发现

- **即时检测**：非 stdio 的注册/更新立即 best-effort 检测，失败不阻塞落库，状态记 disconnected + last_error
- **定时检测**：`MCPHealthChecker` 每 `MCP_HEALTH_INTERVAL` (默认 1m) 对全部服务器执行 initialize 刷新延迟；握手成功后顺带刷新 tools 列表并落库 (tools/list 失败不影响连通状态，错误前缀 `tools/list: ` 记入 last_error)
- **历史**：每次检测写入 `mcp_health_logs`，按服务器自动裁剪
- **stdio**：Phase 1 平台不托管本地子进程，仅允许注册 (status 保持 pending)，test 端点返回明确错误

### 1.6 Agent -> MCP 调用代理（PRD 6.6）

- 通过 `mcp_agent_bindings` 绑定：仅绑定的 Agent 可调用该 MCP
- `runtime.Runtime.SetMCPInvoker(mcpService)`：Agent 模拟流量运行时真实调用其绑定的 MCP (每次最多 2 个，随机选工具)，结果写入 agent_logs：`mcp call ok name=... tool=... latency=...ms` / `mcp call failed ...` / `mcp skip ...`
- 未绑定、pending、disconnected 的 MCP 被跳过并记录原因，不影响 Agent 其他指标

### 1.7 API 全集（PRD 6.2 + 绑定扩展）

```
POST   /api/v1/mcp-servers                 # 创建 (即时检测)
GET    /api/v1/mcp-servers                 # 列表 (q/status/tag, page/size)
GET    /api/v1/mcp-servers/:id             # 详情 (含脱敏凭证)
PUT    /api/v1/mcp-servers/:id             # 更新 (endpoint/凭证变更触发重检)
DELETE /api/v1/mcp-servers/:id             # 删除 (级联绑定+健康历史)
POST   /api/v1/mcp-servers/:id/test        # 手动连通性测试
GET    /api/v1/mcp-servers/:id/health      # 健康状态 + 最近检查历史 (limit)
GET    /api/v1/mcp-servers/:id/tools       # 已发现工具列表
POST   /api/v1/mcp-servers/:id/tools/call  # 工具调用代理 {"name","arguments"}
GET    /api/v1/mcp-servers/:id/agents      # 已绑定 Agent 列表
POST   /api/v1/mcp-servers/:id/agents      # 绑定 {"agent_id"}
DELETE /api/v1/mcp-servers/:id/agents/:agentId  # 解绑
```

### 1.8 关键业务规则

- 名称全局唯一（服务层校验 + DB unique 约束兜底）
- 凭证永不明文回显；删除级联清理绑定与健康历史，无孤儿数据
- 列表过滤：q (name/endpoint/description ILIKE)、status、tag (`tags @> ?::jsonb`)
- **Mock MCP 服务器**：`backend/tools/mock-mcp-server`，端口 `MOCK_MCP_PORT` (默认 9100)，可选 `MOCK_MCP_API_KEY` (要求 Bearer)。3 个工具：`kb.search` / `ticket.create` / `echo`；`POST /mcp?sse=1` 返回 SSE 流，`GET /sse` 提供 legacy 握手

### 1.9 前端（Week 7）

| 模块 | 说明 |
|----|------|
| `src/types/index.ts` | MCP 类型定义 |
| `src/utils/constants.ts` | `MCP_STATUS_MAP` / `MCP_TRANSPORT_MAP` |
| `src/components/common/StatusTag.tsx` | `MCPStatusTag` |
| `src/api/mcp.ts` | mcpApi 全套封装 |
| `src/pages/mcp/MCPListPage.tsx` | 列表：搜索防抖 / 状态过滤 / 标签 / 延迟 / 手动测试按钮，10s 轮询 |
| `src/pages/mcp/MCPFormPage.tsx` | 创建/编辑：transport 切换提示、stdio 警告、凭证区 (api_key 留空=不变，headers KV 动态行，编辑可清空) |
| `src/pages/mcp/MCPDetailPage.tsx` | 详情：4 统计卡 + Descriptions + tabs (工具/健康历史/凭证/Agent 绑定)，5s 轮询 |
| `router/index.tsx` | `/mcp`、`/mcp/new`、`/mcp/:id`、`/mcp/:id/edit` |
| `layouts/MainLayout.tsx` | 侧边栏 MCP 菜单启用 |

---

## 2. 端到端验证（2026-08-18 全部通过）

```
注册      http / sse(legacy 握手) / sse(直连) 三种路径注册即检测即连通   PASS
          工具自动发现 (3 个工具落库)                                  PASS
          重名 400                                                     PASS
过滤      q 搜索 / status 过滤 / tag 过滤                               PASS
凭证      加密存储 / 脱敏回显 / 留空=不变 / 可清空                       PASS
故障      broken 端点 -> disconnected + last_error                     PASS
          错误 API key -> 401 捕获进 last_error                        PASS
stdio     允许注册, test 返回明确错误 (不托管子进程)                     PASS
调用      tools/call 代理 (含参数) / 未知工具返回 JSON-RPC 错误          PASS
绑定      绑定 / 重复绑定 400 / 解绑 / 列表                              PASS
删除      级联清理绑定+健康历史 (无孤儿)                                  PASS
更新      endpoint 变更后自动重检                                        PASS
定时      30s 周期检测历史持续增长 (41+ 条)                               PASS
运行时    agent 日志出现 mcp call ok name=mock-kb / mock-sse 真实调用     PASS
前端      Playwright 冒烟: 列表/详情(工具/历史/凭证/绑定)/表单页 0 错误    PASS
```

---

## 3. 依赖变更（M2 基线之上）

无新增第三方依赖：AES-256-GCM 用标准库 (crypto/aes + crypto/cipher)，JSON-RPC / SSE 解析自实现。

---

## 4. 与 PRD 的偏差说明

| 项 | PRD | 当前实现 | 说明 |
|----|-----|----------|------|
| stdio 传输 | P0 | 仅允许注册 | Phase 1 不托管本地子进程，无检测/调用，留 Phase 2 |
| legacy SSE | P1 | GET 握手 + 直连 POST 回退 | 兼容两类服务端实现 |
| 202 session-only SSE | - | 返回明确错误 | Streamable HTTP 202 会话模式不支持 |
| 故障告警 | P1 (6.2.3) | 未实现 | UI 可看 last_error + 历史，通知渠道与 M5 告警统一设计 |
| 连接池/长会话复用 | P2 | 每请求新建连接 | 单次调用足够，MVP 不做 |
| 凭证轮换 | P2 | 未实现 | 更换 MCP_CREDENTIALS_KEY 需重新录入凭证 |

---

## 5. 运行方式

```
# 1. (可选) 启动 mock MCP 服务器, 用于本地验证
cd backend
MOCK_MCP_PORT=9100 MOCK_MCP_API_KEY=mock-mcp-key-123 go run ./tools/mock-mcp-server

# 2. 启动后端 (MCP_CREDENTIALS_KEY 必填, 缺失启动 fatal)
cd backend
go run ./cmd/server

# 3. 启动前端 (需后端先运行)
cd frontend
npm run dev    # http://127.0.0.1:8090
```

`.env` 新增变量：

```
MCP_CREDENTIALS_KEY=<64 hex>   # 生成: openssl rand -hex 32
MCP_HEALTH_INTERVAL=1m        # 定时检测间隔
MCP_CHECK_TIMEOUT=5s           # 单次检测超时
```

示例：

```
# 注册 MCP (注册后立即检测 + 工具发现)
curl -X POST localhost:8080/api/v1/mcp-servers -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "name": "mock-kb", "endpoint": "http://127.0.0.1:9100/mcp", "transport": "http",
  "credentials": {"api_key": "mock-mcp-key-123"},
  "tags": ["demo"]
}'

# 健康 / 工具 / 调用
curl localhost:8080/api/v1/mcp-servers/$ID/health -H "Authorization: Bearer $TOKEN"
curl localhost:8080/api/v1/mcp-servers/$ID/tools -H "Authorization: Bearer $TOKEN"
curl -X POST localhost:8080/api/v1/mcp-servers/$ID/tools/call -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"kb.search","arguments":{"query":"deploy"}}'

# 绑定 Agent (运行时模拟流量将真实调用该 MCP)
curl -X POST localhost:8080/api/v1/mcp-servers/$ID/agents -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"agent_id":"..."}'
```

---

## 6. 待办（M3 剩余 + 衔接项）

1. ~~Week 8 Agent-MCP 集成~~ 已完成：绑定 + 运行时调用代理 (见 1.6)
2. ~~Week 7 前端~~ 已完成 (见 1.9)
3. stdio 本地子进程托管 (本地命令型 MCP) —— Phase 2
4. 故障告警 (连续失败 N 次通知) —— 与 M5 告警统一设计
5. RBAC：`mcp:read` / `mcp:write` 目前为硬编码桩 (与 M2 相同，待 user_roles 落地)
6. 性能压测未单独执行 (PRD P2)