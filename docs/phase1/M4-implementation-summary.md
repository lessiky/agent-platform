# M4 模型管理 - 实现总结

> **版本：** v1.0
> **日期：** 2026-08-19
> **范围：** 开发计划 Week 9 (后端核心) + Week 10 (前端)

---

## 1. 已完成内容

### 1.1 数据模型（4 张表，GORM 自动迁移）

| 表 | 说明 |
|----|------|
| `model_templates` | 模型模板 (PRD 5.3)。name 唯一、provider (openai/anthropic/google/azure/custom)、model 模型名、api_key bytea (AES-256-GCM 密文)、endpoint、config JSONB (temperature/max_tokens/top_p)、status (active/inactive/error)、priority (越小越高)、tags JSONB、team_id 预留、健康字段 (health_last_check / health_latency_ms)、last_error |
| `model_quotas` | 模型配额 (PRD 5.4)。model_id 唯一、daily_limit / monthly_limit (0=不限)、daily_used / monthly_used、daily_token_limit / monthly_token_limit (0=不限) + 对应 token 用量、reset_daily_at / reset_monthly_at 重置锚点 |
| `model_usage_logs` | 模型调用用量日志。ok / tokens / latency_ms / agent_id，每模型保留最近 2000 条 |
| `model_health_logs` | 连通性检查历史。每模型保留最近 500 条 |

### 1.2 代码结构

```
backend/internal/
├── model/model.go                 # 4 个 GORM 模型 + provider/status 常量
├── modelclient/client.go          # 模型提供商连通性探测客户端
├── repository/model_repository.go # 4 个仓储 (List 支持 q/provider/status/tag 过滤)
├── service/model_service.go       # 业务服务层 (CRUD/测试/配额/路由)
├── service/model_health_checker.go# 定时健康检查器
└── api/model/handler.go           # HTTP Handler + 路由
```

复用 M3 的 `crypto.AesGCM` 加密 API Key（独立密钥 `MODEL_CREDENTIALS_KEY`）。

### 1.3 连通性探测（internal/modelclient）

采用各提供商"模型列表"接口做轻量探测（不产生 token 消耗，同时验证端点可达 + Key 有效）：

- **openai / custom**：GET `{endpoint}/models`，`Authorization: Bearer <key>`
- **anthropic**：GET `{endpoint}/v1/models`，`x-api-key`
- **google**：GET `{endpoint}/v1beta/models?key=<key>`
- **azure**：GET `{endpoint}/openai/models?api-version=2024-06-01`，`api-key`

openai/anthropic/google 未填端点时用官方默认端点；401/403 明确报 "API Key 无效?"；响应体尽力解析模型列表 (data/models/value 三种结构)。

### 1.4 配额管理（PRD 2.3.3）

- 每模板独立配额：日/月调用限额 (0=不限)，超限后路由自动跳过该模板
- 重置逻辑：按锚点滚动清零（日=24h、月=1 个月），读取时即时生效
- 消费入口：运行时模拟流量每次调用 `RouteAndConsume` 计 1 次（失败调用同样消耗）
- 用量可查：单模型调用日志 + 全部模型近 24h 聚合 (calls/tokens/errors)

### 1.5 路由选择（PRD 2.3.4 P0 + 2.3.3 P2）

- **优先级路由**：按 priority 升序遍历 active 模板，选中首个可用者
- **故障转移**：跳过 status != active（含 last_error 提示）与配额耗尽的模板
- `POST /api/v1/models/route`：dry-run 选择（不消耗配额），返回 selected + skipped 原因列表，供 UI 展示与调试
- **运行时集成**：`runtime.Runtime.SetModelRouter` —— Agent 模拟流量每次调用都走真实路由 + 配额消费 + 用量落库，结果写入 agent_logs（`model route ok name=... quota=daily 2/3`）

### 1.6 健康检查

- 注册/更新立即探测（best effort，失败不阻塞落库，status=error + last_error）
- 定时探测：`ModelHealthChecker` 每 `MODEL_HEALTH_INTERVAL` (默认 1m) 检测全部非停用模板
- 手动停用 (inactive) 的模板不自动探测、不参与路由
- 更新时连接参数 (provider/endpoint/api_key) 变化或从停用恢复时自动重检

### 1.7 API 全集（PRD 6.3 + 扩展）

```
POST   /api/v1/model-templates                 # 创建 (即时探测)
GET    /api/v1/model-templates                 # 列表 (q/provider/status/tag, page/size)
GET    /api/v1/model-templates/:id             # 详情 (含脱敏 API Key)
PUT    /api/v1/model-templates/:id             # 更新 (连接参数变更触发重检)
DELETE /api/v1/model-templates/:id             # 删除 (级联配额+用量+健康历史)
POST   /api/v1/model-templates/:id/test        # 手动连通性测试
GET    /api/v1/model-templates/:id/health      # 健康状态 + 最近检查历史 (limit)
GET    /api/v1/model-templates/:id/usage       # 配额 + 最近调用日志 (limit)
GET    /api/v1/model-quota                     # 配额列表
PUT    /api/v1/model-quota/:modelId            # 设置/更新配额 {"daily_limit","monthly_limit","daily_token_limit","monthly_token_limit"}
GET    /api/v1/model-usage                     # 全部模型用量概览 (配额+近 24h 聚合)
POST   /api/v1/models/route                    # 路由选择 (dry-run)
```

### 1.8 前端（Week 10）

| 模块 | 说明 |
|----|------|
| `src/types/index.ts` | 模型类型定义 |
| `src/utils/constants.ts` | `MODEL_STATUS_MAP` / `MODEL_PROVIDER_MAP` (含默认端点) |
| `src/components/common/StatusTag.tsx` | `ModelStatusTag` |
| `src/api/model.ts` | modelApi 全套封装 |
| `src/pages/model/ModelListPage.tsx` | 列表：搜索防抖 / 提供商+状态过滤 / 测试按钮，10s 轮询 |
| `src/pages/model/ModelFormPage.tsx` | 创建/编辑：provider 切换自动填充官方端点 (openai 等端点锁定)、生成参数表单、API Key (留空=不变 / 可清空) |
| `src/pages/model/ModelDetailPage.tsx` | 详情：4 统计卡 + Descriptions + tabs (健康历史 / 配额与用量[限额编辑+调用日志] / 路由选择[试运行+跳过原因])，5s 轮询 |
| `router/index.tsx` | `/models`、`/models/new`、`/models/:id`、`/models/:id/edit` |
| `layouts/MainLayout.tsx` | 侧边栏模型管理菜单启用 |

---

## 2. 端到端验证（2026-08-19 全部通过）

```
注册      custom 提供商注册即探测连通 (status=active, 延迟 2ms)          PASS
          重名 400                                                       PASS
          错误 API key -> status=error + "unauthorized: HTTP 401"        PASS
          broken 端点 -> status=error + 连接拒绝捕获                      PASS
过滤      q 搜索 / provider 过滤 / status 过滤 / tag 过滤                 PASS
凭证      加密存储 / 脱敏回显 (mock****-123) / 编辑留空=不变 / 可清空      PASS
测试      手动测试返回模型列表 (mock-gpt-mini 等 2 个)                    PASS
配额      设置 daily=3 / monthly=100, 重置锚点正确                        PASS
路由      dry-run 按优先级选中 (priority=10)                              PASS
运行时    模拟流量消费配额 1/3 -> 2/3 -> 3/3 (agent 日志可见)              PASS
故障转移  配额耗尽后自动切换备用模型 (priority=50), 运行时+ dry-run 均生效  PASS
          停用 (inactive) 模板被路由跳过 + 恢复后重检                      PASS
更新      更换 api_key 后自动重检, error -> active                        PASS
用量      单模型调用日志 (含 agent_id) + 近 24h 聚合 (calls/tokens/errs)   PASS
定时      10s 间隔检测历史持续累积 (21+ 条, 生产默认 1m)                   PASS
删除      级联清理配额+用量+健康历史 / 删除后 404                          PASS
前端      Playwright 冒烟: 列表/详情(3 tabs)/表单页 0 新控制台错误          PASS
```

---

## 3. 依赖变更（M3 基线之上）

无新增第三方依赖：探测客户端基于标准库 net/http，加密复用 `crypto/aesgcm.go`。

---

## 4. 与 PRD 的偏差说明

| 项 | PRD | 当前实现 | 说明 |
|----|-----|----------|------|
| 配额维度 | 团队/用户 (2.3.3) | 按模型模板 (team_id 预留) | Phase 1 无团队模块，团队配额待团队落地后扩展 |
| 配额限额 | 调用次数 | 调用次数 (token 限额未实现) | usage 表已记录 tokens，扩展限额类型即可 |
| 路由端点 | `POST /models/:id/route` | `POST /models/route` | 路由是全局选择行为, :id 语义不明, 已按全局实现 |
| 版本管理 | P1 (2.3.1) | 未实现 | 任务清单 (9.1-9.6) 未包含, 留 Phase 2 |
| 负载均衡/条件路由 | P2 | 未实现 | 留 Phase 2 |
| 故障告警 | P1 (2.3.2) | 未实现 | 与 M5 告警统一设计 |
| 探测方式 | 连通性测试 | 模型列表接口探测 | 不消耗 token; 如需 chat 级验证可后续扩展 |

---

## 5. 运行方式

```
# 1. (可选) 启动 mock 模型服务器, 用于本地验证
cd backend
MOCK_MODEL_PORT=9101 MOCK_MODEL_API_KEY=mock-model-key-123 go run ./tools/mock-model-server

# 2. 启动后端 (MODEL_CREDENTIALS_KEY 必填, 缺失启动 fatal)
cd backend
go run ./cmd/server

# 3. 启动前端 (需后端先运行)
cd frontend
npm run dev    # http://127.0.0.1:8090
```

`.env` 新增变量：

```
MODEL_CREDENTIALS_KEY=<64 hex>   # 生成: openssl rand -hex 32
MODEL_HEALTH_INTERVAL=1m         # 定时检测间隔
MODEL_CHECK_TIMEOUT=5s           # 单次探测超时
MODEL_CHAT_TIMEOUT=120s          # 单次对话调用超时
```

示例：

```
# 注册模型 (注册后立即探测)
curl -X POST localhost:8080/api/v1/model-templates -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "name": "mock-gpt", "provider": "custom", "model": "mock-gpt-mini",
  "endpoint": "http://127.0.0.1:9101/v1",
  "api_key": "mock-model-key-123", "priority": 10,
  "config": {"temperature": 0.7, "max_tokens": 1024}
}'

# 设置配额 / 查配额 / 看用量
curl -X PUT localhost:8080/api/v1/model-quota/$ID -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"daily_limit": 1000}'
curl localhost:8080/api/v1/model-quota -H "Authorization: Bearer $TOKEN"
curl localhost:8080/api/v1/model-templates/$ID/usage -H "Authorization: Bearer $TOKEN"
curl localhost:8080/api/v1/model-usage -H "Authorization: Bearer $TOKEN"

# 路由选择 (dry-run: 显示选中与跳过原因)
curl -X POST localhost:8080/api/v1/models/route -H "Authorization: Bearer $TOKEN"
```

---

## 6. 待办（M4 剩余 + 衔接项）

1. ~~Week 10 前端~~ 已完成 (见 1.8)
2. ~~Agent -> 模型运行时集成~~ 已完成 (见 1.5)
3. ~~token 维度配额 (按 tokens 而非次数限额)~~ 已完成 (2026-08-19, 见第 8 节): model_quotas 扩展 token 4 字段, 消费/跳过/重置/UI 全部接入
4. 故障告警 (连续失败 N 次通知) —— 与 M5 告警统一设计
5. 模型模板版本管理 + 回滚 (PRD P1) —— 可复用 M2 agent_versions 模式
6. RBAC：`model:read` / `model:write` 目前为硬编码桩 (与 M2/M3 相同)
---

## 7. Agent 管理增强（模型下拉 + 可用工具自动校验，2026-08-19）

M5 启动前基于 M3/M4 数据对 M2 Agent 管理做的增强，端到端验证全部通过。

### 7.1 功能

- **模型选择下拉**：Agent 创建/编辑表单 `model` 由自由文本改为下拉，选项来自模型模板列表（展示 `${name} (${model})`）；已有值不在模板列表时以 "xxx (自定义)" 兼容展示。
- **MCP 绑定 + 可用工具自动校验**：表单支持多选绑定 MCP 服务器（`mcp_ids`）；可用工具下拉仅列出绑定 MCP 已发现工具的并集（展示 `tool · 来自 mcpName`），MCP 变化时自动重拉并剪掉失效项。后端保存时二次校验：`tools` 必须全部在绑定 MCP 已发现工具的并集内，否则 400 并返回可用工具列表。
- **绑定全量同步**：Update 时 `mcp_ids` 非 null 全量同步绑定（补缺失、删多余），null 表示不变；Delete 级联清理绑定（修复 M3 遗留的孤儿绑定问题）。
- **运行时联动**：
  - 模型路由 `RouteAndConsume` 优先使用 Agent config 的 `model`（按模板名或模型名不区分大小写匹配），不可用时回退按优先级故障转移，日志带 `reason=agent_preferred`。
  - MCP 调用 `InvokeMCPTools` 在 Agent config `tools` 非空时按其过滤候选工具（无匹配则 `no_allowed_tools`）。
- **绑定 MCP tab**：Agent 详情页新增 "绑定 MCP" tab，15s 轮询展示各绑定 MCP 的状态 / 已发现工具 / 最后错误。

### 7.2 后端变更（M4 基线之上）

- `internal/repository/mcp_repository.go`：`MCPAgentBindingRepository` 增加 `DeleteByAgent`。
- `internal/service/agent_service.go`：
  - 请求结构加 `mcp_ids`（Create/Update）；`syncMCPBindings`（幂等 bind + 移除多余）、`validateTools`（tools 必须属于绑定 MCP 已发现工具并集）；
  - 新增 `ListBoundMCPS` → `BoundMCPView {ID,Name,Status,Tools,LastError}`；
  - Delete 级联 `DeleteByAgent`。
- `internal/service/mcp_service.go`：`InvokeMCPTools` 读取 Agent config 的 tools 过滤候选工具。
- `internal/service/model_service.go`：`RouteAndConsume` 优先 Agent config 的模型（不可用再按优先级故障转移）。
- `internal/api/agent/handler.go`：新增 `GET /api/v1/agents/:id/mcps`（权限 `agent:read`）。
- `cmd/server/main.go`：构造函数接线更新（AgentService +2 仓储；MCPServerService / ModelTemplateService 各 +AgentRepository）。

### 7.3 前端变更

- `types/index.ts`：`CreateAgentRequest` 加 `mcp_ids`，新增 `AgentBoundMCP`。
- `api/agent.ts`：新增 `listBoundMCPS(id)`。
- `pages/agent/AgentFormPage.tsx`：model 改 Select；MCP 多选（展示 `${name} [状态]`）；tools 多选（仅选项、未选 MCP 时 disabled）；payload 带 `mcp_ids`。
- `pages/agent/AgentDetailPage.tsx`："绑定 MCP" tab（BoundMCPsPanel，15s 轮询）。

### 7.4 端到端验证（2026-08-19 全部通过）

```
创建    model=mock-gpt-2 + mcp_ids=[mock-kb] + tools=[kb.search,echo] -> 201   PASS
负例    tools 含不存在工具 -> 400 (响应含可用工具列表)                        PASS
负例    配置 tools 但未绑定任何 MCP -> 400                                   PASS
查询    GET /agents/:id/mcps 返回绑定 MCP + 已发现工具                        PASS
运行时  model route reason=agent_preferred; mcp call ok (仅 kb.search/echo,
        未调用 ticket.create —— 工具过滤生效)                                PASS
更新    mcp_ids=[] 全部解绑; DELETE agent 后无孤儿绑定                        PASS
前端    Playwright 冒烟: 模型下拉 (4 项含 "gpt-4o (自定义)") / MCP 预填 /
        工具下拉 3 项 / 详情 "绑定 MCP" tab / 0 控制台错误                    PASS
```

### 7.5 API 补充

- 前端    Playwright 冒烟: 模型下拉 (4 项含 "gpt-4o (自定义)") / MCP 预填 /
        工具下拉 3 项 / 详情 "绑定 MCP" tab / 0 控制台错误                    PASS
```

### 7.5 API 补充

- `GET /api/v1/agents/:id/mcps` - 绑定 MCP 列表（含已发现工具）。
- `POST /api/v1/agents` / `PUT /api/v1/agents/:id` 支持 `mcp_ids`（非 null = 全量同步绑定）。

---

## 8. token 维度配额 (2026-08-19)

完成待办 #3: 配额不再仅限次数, 支持 token 维度限额 (次数 / token 两类限额独立设置, 0 = 不限)。

### 8.1 数据与消费

- `model_quotas` 新增 4 字段: `daily_token_limit` / `monthly_token_limit` (0 = 不限) + `daily_token_used` / `monthly_token_used` (GORM AutoMigrate 自动加列, 默认 0)。
- `RouteAndConsume` 每次调用后把本次估算 token 累入 token 计数 (失败调用同样消耗); 限额生效时日志含 `token_quota=daily 52/100`。
- `quotaExceeded` 新增 token 限额检查: 跳过原因 `日 token 配额耗尽 (104/100)` / `月 token 配额耗尽 (x/y)`; 日/月滚动时 `ensureQuotaFresh` 同步重置 token 计数。

### 8.2 API 与前端

- `PUT /api/v1/model-quota/:modelId` 新增 `daily_token_limit` / `monthly_token_limit`; 配额列表 / 用量 / 概览 (`/model-usage`) 均返回 token 字段。
- 模型详情页 "配额与用量" tab 新增 "每日 Token 限额" / "每月 Token 限额"; "今日配额" / "月度配额" 统计卡改为 (次数 / tokens) 双行展示。
- 修复既有问题: 详情页 5s 轮询 `load()` 会反复 `setFieldsValue` 覆盖用户正在输入的限额, 现仅在表单未编辑时预填 (`isFieldsTouched`)。

### 8.3 端到端验证 (2026-08-19 全部通过)

```
设置    daily_token_limit=100 -> 用量 0/100                                    PASS
消费    3 次 agent invoke (各 52 tokens): 52/100 -> 104/100 -> 第 3 次自动切换  PASS
转移    第 3 次调用落到下一优先级模型 (mock-gpt 被跳过, 无 agent_preferred)     PASS
跳过    dry-run route skipped 列表: mock-gpt reason=日 token 配额耗尽 (104/100)  PASS
API     usage / model-quota / model-usage 均返回 token 字段                   PASS
前端    Playwright 冒烟: 统计卡 "tokens 104/100" / 表单预填 100 / 改 500 保存
        -> 卡片 "tokens 104/500" / 轮询不再覆盖输入 / 0 控制台错误              PASS
清理    恢复不限 (0), 删除测试 agent, 吊销 key                                 PASS
```
