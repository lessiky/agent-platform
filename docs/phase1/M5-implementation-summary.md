# M5 工作流引擎 - 实现总结

> **版本：** v1.0
> **日期：** 2026-08-20
> **范围：** 开发计划 Week 13-16 (后端引擎 + 调度 + 前端编辑器 + 端到端验证)

---

## 1. 已完成内容

### 1.1 数据模型（4 张新表，GORM AutoMigrate）

| 表 | 说明 |
|----|------|
| `workflows` | 工作流定义 (PRD 5.5)。name (部分唯一索引, 软删除可重建) / definition JSONB (`{version, nodes, edges}`) / status (draft/active/archived) / input_schema / output_schema / version (保存递增) / schedule JSONB (`{cron, input, timezone}`) / schedule_enabled / webhook_token (创建时自动生成 32 位 hex) |
| `workflow_versions` | 版本快照 (每次保存生成, 支持回溯)。workflow_id + version 唯一, 完整 definition/input_schema/output_schema |
| `workflow_executions` | 执行记录 (PRD 5.6)。workflow_id / workflow_name / workflow_version (执行时点版本) / trigger_type (manual/cron/webhook) / status (running/waiting_approval/success/failed/cancelled) / input / output (按节点 id 聚合) / trace_id / error / started_at / finished_at |
| `workflow_node_executions` | 节点执行记录 (PRD 5.7, 执行追踪最小粒度)。execution_id + node_id / node_type / status (pending/running/success/failed/skipped/waiting_approval/cancelled) / attempt (含首次) / input (解析后配置) / output / error / approval_id (审核挂起关联) / duration_ms / started_at / finished_at |

### 1.2 代码结构

```
backend/internal/
├── model/workflow.go                  # Workflow / WorkflowVersion / WorkflowExecution / WorkflowNodeExecution + 状态常量
├── repository/workflow_repository.go  # 工作流/版本/执行/节点执行仓储 (ListScheduled / ListWaitingByExecution / CountsByStatus 等)
├── service/workflow_dag.go            # DAG 定义解析/校验 (Kahn 拓扑环检测, 节点/边合法性, 上限 100 节点/500 边) + 变量解析 (JSONPath 风格)
├── service/workflow_engine.go         # DAG 执行引擎 (依赖调度 + 节点执行 + 重试/超时/取消 + 审核挂起/恢复 + 启动对账)
├── service/workflow_scheduler.go      # Cron 定时调度 (robfig/cron/v3, 进程内, 5 分钟自愈对账)
├── service/workflow_service.go        # CRUD / 版本 / 触发 / 调度 / Webhook / 看板 (执行器抽象接口)
├── service/approval_service.go        # 决策钩子 SetDecisionHook (M4.5 扩展, 决策后重读最新状态再回调)
├── database/database.go               # AutoMigrate 注册 4 张新表 + workflows 部分唯一索引
└── api/workflow/handler.go            # 18 个端点 (含公开 Webhook)

frontend/src/
├── api/workflow.ts                    # 类型 + workflowApi (14 个方法)
└── pages/workflow/
    ├── WorkflowListPage.tsx           # 列表 (搜索/状态过滤/创建/快捷操作)
    ├── WorkflowEditorPage.tsx         # DAG 可视化编辑器 (@xyflow/react: 拖拽节点/连线/条件出口/节点配置表单/重试策略)
    ├── WorkflowDetailPage.tsx         # 详情 (DAG 只读 + 版本 + 调度配置 + 手动触发)
    ├── WorkflowDashboardPage.tsx      # 执行看板 (状态计数 + 最近执行)
    └── ExecutionDetailPage.tsx        # 执行追踪 (节点级日志/耗时/输入输出/审核等待状态)
```

### 1.3 DAG 执行引擎

- **节点类型 (5 类)**：
  - `agent`：调用 Agent 单轮对话 (走 M2.5 对话运行时, 输出 reply/session_id/model_name/total_tokens/latency_ms/mcp_calls)
  - `mcp_tool`：调用 MCP 工具 (输出 content 原始块 + text 展平文本 + is_error; 工具开启审核时挂起)
  - `http`：外部 HTTP 调用 (GET/POST/PUT/PATCH/DELETE, 自定义 header/body, 输出 status_code/body)
  - `delay`：延迟等待 (0, 3600] 秒
  - `condition`：条件分支 (left/operator/right, 支持 == != > < >= <= contains exists, true/false 双出口)
- **依赖调度**：Kahn 拓扑; 节点就绪条件 = 所有前驱成功; condition 仅选中分支的前驱生效; 上游失败/未选中分支的下游级联 `skipped`。
- **变量传递**：`$inputs.<path>` / `$nodes.<node_id>.<path>` / `$execution.id`，路径支持 `a.b[0].c`; 整串引用保留类型, 嵌入引用做文本格式化 (如 `"hello $inputs.name"`)。
- **节点级容错**：
  - 重试：`retry {max_attempts [1,10], interval_seconds [0,600], backoff: fixed|exponential}` (指数退避 interval * 2^(attempt-1))
  - 超时：`timeout_seconds [0,3600]`，默认 300s, context 超时后计入重试
  - 取消：执行级取消信号, 运行中节点转 `cancelled`, 未开始节点级联 `cancelled`
- **执行生命周期**：

```
                          ┌─ 全部节点 success/skipped ─────────────► success (output 按节点 id 聚合)
running ──────────────────┤
   │                      ├─ 任一节点失败 (重试耗尽) ─────────────► failed (下游 skipped)
   │                      ├─ 节点等待人工审核 ──► waiting_approval ─┬─ approve: 回填结果并恢复 running
   │                      │                                        └─ reject/expire: 节点 failed, 执行 failed
   └─ 用户取消 ───────────┴─► cancelled
```

- **启动对账** (ReconcileOnStartup)：进程重启后遗留的 `running` 执行置为 failed; `waiting_approval` 保留 (审核决策仍可恢复)。
- **并发安全**：单进程内 per-execution 互斥锁 (触发/取消/审核恢复共用); 单节点串行执行, 就绪节点并行。

### 1.4 触发方式（3 种）

| 触发 | 入口 | 说明 |
|------|------|------|
| manual | `POST /workflows/:id/trigger` (workflow:execute) | 控制台手动触发, input 作为执行输入 |
| cron | `PUT /workflows/:id/schedule` | 进程内 robfig/cron (Asia/Shanghai); 调度配置持久化于 workflows.schedule, 重启重建; 变更即时刷新 + 5 分钟自愈对账; 触发输入 = schedule.input |
| webhook | `POST /api/v1/webhooks/workflows/:token` (公开端点) | token 创建时自动生成; payload 必须为 JSON 对象, 直接作为执行输入; 非法 token 404 |

> 说明：开发计划中 "Cron 调度使用 RabbitMQ" 调整为进程内 cron 触发 (Phase 1 单进程架构, 无消息队列依赖); 调度配置持久化 + 重启重建保证不丢任务。

### 1.5 MCP 节点人工审核 (联动 M4.5)

- `mcp_tool` 节点执行前检查工具 `requires_approval`; 需审核时经 `ApprovalService.CreateRequest` 创建审核请求 (source=`workflow`, 关联 workflow_execution_id, 参数为解析后快照), 节点转 `waiting_approval` 并挂起执行。
- 审核决策钩子 (M4.5 的 `SetDecisionHook`) 在 approve/reject/超时后重读决策后的最新审核记录并调用 `ResumeAfterApproval`：
  - **approve**：工具执行结果回填审核 `result`, 节点输出归一化为 `{content, text, is_error}` (与常规 MCP 节点一致), 无其他挂起节点则执行恢复 running
  - **reject/expire**：节点 failed ("审核被驳回/审核超时" + 意见), 下游级联 skipped, 执行 failed
- 多节点同时挂起时, 全部决策完成才恢复执行。
- 审核超时走 M4.5 全局策略 (默认 30 分钟 reject), 工作流侧无需额外配置。

### 1.6 API（18 个端点）

见 README "Workflow API" 章节。要点：

- 创建/更新时 definition 自动走 DAG 校验 (失败 400); `POST /workflows/validate` 支持编辑器预检
- 执行详情走独立前缀 `/workflow-executions/:id` (避免与 `/workflows/:id` 路由冲突), 含节点级执行记录
- `GET /workflows/dashboard` 返回状态计数 + 最近 10 条执行
- Webhook 为唯一公开端点 (无 JWT), 其余均需 `workflow:read` / `workflow:write` / `workflow:execute` 权限 (RBAC 种子见 M1, M5 新增 3 个权限点)

### 1.7 前端（Week 14-15）

- **列表页**：搜索/状态过滤/分页, 创建 (DAG 校验), 激活/归档/删除, 快捷跳转编辑与执行历史
- **可视化编辑器**：@xyflow/react (React Flow 12) 画布; 5 类节点拖拽 + 端口连线; condition 节点 true/false 双出口; 节点配置表单 (含重试策略/超时); 保存前本地校验; 保存并激活; 画布内手动触发
- **详情页**：DAG 只读视图 + 版本历史 + 调度配置 (cron/输入 JSON) + 手动触发 + 执行历史表 (状态/触发方式过滤)
- **执行追踪页**：节点级执行时间线 (状态/耗时/尝试次数/输入输出 JSON 展示), 审核等待状态高亮 + 关联审核请求, 执行级 input/output/error
- **看板页**：5 种执行状态计数 + 最近执行列表, 点击跳转执行追踪

---

## 2. 端到端验证（2026-08-20 全部通过, 48/48）

E2E 脚本: `.m5e2e/e2e.mjs` (Node, 需后端 + Postgres + mock-mcp/mock-model 服务器)。

| 组 | 覆盖 | 结果 |
|----|------|------|
| T1 | DAG 校验: 环检测 / 重复节点 id / 非法类型 / 合法 DAG | 4/4 |
| T2 | CRUD + 版本: 创建 (draft v1 + webhook_token) / 更新 v2 / 版本历史 / 搜索 / 归档 | 5/5 |
| T3 | 执行: mcp_tool 变量传递 ($inputs) / condition 分支 (true 执行 false skipped) / http 200 / 节点记录完整 + 耗时 / 输出按节点聚合 / 执行历史过滤分页 | 11/11 |
| T4 | 失败重试: 不存在工具 max_attempts=3 → 执行 failed, 节点 attempt=3, error 记录 | 4/4 |
| T5 | 审核挂起/恢复: waiting_approval + approval_id → 审核列表 (source=workflow) → 批准恢复执行成功 + 节点输出归一化 + 结果回填; 驳回路径: 执行 failed + 节点 failed + 下游 skipped | 8/8 |
| T6 | 取消: 运行中执行取消, 节点级联 cancelled | 2/2 |
| T7 | Webhook: token 生成 / 202 受理 / trigger_type=webhook / payload 作为 input / 非法 token 404 | 6/6 |
| T8 | Agent 节点集成 (Agent + Model + MCP): agent 节点 reply + tokens / reply 变量传递到 condition / 分支正确 | 3/3 |
| T9 | Cron 调度: 每分钟 cron → 定时执行触发成功 + schedule.input 注入 | 3/3 |
| T10 | 看板: 状态计数 + 最近执行 / 列表状态过滤 | 2/2 |

单元测试: `go test ./...` 通过 (DAG 校验/变量解析 + M5 新增 `flattenMCPText` / `normalizeApprovalResult`)。前端 `tsc --noEmit` + `vite build` 通过。

---

## 3. 依赖变更（M4.5 基线之上）

- 后端：`github.com/robfig/cron/v3` (进程内 Cron 调度)
- 前端：`@xyflow/react` ^12 (DAG 可视化编辑器)
- 数据库：4 张新表 (workflows / workflow_versions / workflow_executions / workflow_node_executions) + workflows 部分唯一索引
- 权限：新增 workflow:read / workflow:write / workflow:execute 三个权限点 (admin 全量)
- M4.5 扩展：`ApprovalService.SetDecisionHook` (决策后钩子, 用于工作流恢复); `tool_approvals.source` 启用 `workflow` 值; `tool_approvals.workflow_execution_id` 关联工作流执行

---

## 4. 与 PRD / 计划的偏差说明

| 项 | 计划 | 实际 | 原因 |
|----|------|------|------|
| Cron 调度 | RabbitMQ 分布式调度 | 进程内 robfig/cron + 持久化重建 | Phase 1 单进程架构; 配置持久化 + 重启重建 + 5 分钟自愈对账已覆盖可用性 |
| 变量传递 | JSONPath | `$inputs.*` / `$nodes.<id>.*` / `$execution.id` 点路径 + `[n]` 数组下标 | 覆盖工作流场景 (跨节点输出/执行输入), 避免引入 JSONPath 库 |
| 循环节点 | Phase 1 | 延后 Phase 2 | 按计划风险项 "先实现线性 DAG, 分支/循环延后"; condition 分支已支持 |
| MCP 调度多副本 | - | 单进程内调度 | 同上; 多副本部署时需改为外部触发器或分布式锁 (Phase 2) |

---

## 5. 运行方式

前置: Postgres + mock 服务器 (可选):

```powershell
docker-compose -f infra/docker-compose.yml up -d
# mock (MCP 节点 / agent 节点测试用)
MOCK_MCP_PORT=9100 MOCK_MCP_API_KEY=mock-mcp-key-123 go run ./tools/mock-mcp-server
MOCK_MODEL_PORT=9101 MOCK_MODEL_API_KEY=mock-model-key-123 go run ./tools/mock-model-server
```

```bash
# 创建并校验 DAG
curl -X POST localhost:8080/api/v1/workflows/validate -H "Authorization: Bearer $TOKEN" \
  -d '{"definition":{"version":1,"nodes":[{"id":"n1","type":"delay","name":"wait","config":{"seconds":1}}],"edges":[]}}'

# 创建 -> 激活 -> 手动触发
curl -X POST localhost:8080/api/v1/workflows ...            # 返回 id + webhook_token
curl -X POST localhost:8080/api/v1/workflows/$ID/activate ...
curl -X POST localhost:8080/api/v1/workflows/$ID/trigger -d '{"input":{"name":"e2e"}}'

# 执行详情 (节点级追踪) / 取消
curl localhost:8080/api/v1/workflow-executions/$EXEC_ID ...
curl -X POST localhost:8080/api/v1/workflow-executions/$EXEC_ID/cancel ...

# 定时调度 (每分钟)
curl -X PUT localhost:8080/api/v1/workflows/$ID/schedule -d '{"enabled":true,"cron":"* * * * *","input":{"source":"cron"}}'

# Webhook 触发 (公开端点)
curl -X POST localhost:8080/api/v1/webhooks/workflows/$WEBHOOK_TOKEN -d '{"note":"hi"}'

# E2E 全量验证
node .m5e2e/e2e.mjs
```

---

## 6. 待办（Phase 2 衔接项）

- [ ] 循环节点 (for-each / while) 与子工作流
- [ ] 多副本部署时的调度外部化 (消息队列 / 分布式锁)
- [ ] 执行并发限流 (当前单进程 per-execution 锁, 无全局并发上限)
- [ ] 大 DAG 执行的分片/断点续跑 (当前执行状态在内存, 重启后 running 置失败)
- [ ] 工作流执行通知 (钉钉/邮件) 与执行级重试 (当前仅节点级)
- [ ] 前端编辑器增强: 撤销重做、自动布局 (dagre)、节点拖拽配置面板抽离

---

## 7. Phase 2 更新 (2026-08-22): AI 自动生成工作流

> 在工作流编排入口支持用自然语言描述业务流程, LLM 自动编排为平台可执行的 DAG 草稿。

- **后端**：`service/workflow_ai_generate.go` — `WorkflowAIGenerator`。流程: 收集平台上下文 (可用 Agent + MCP 服务器及其已发现工具) → 组装系统提示词 (节点/边/变量规范 + 资源目录) → 复用模型路由 `RouteAndChat` (故障转移 + 配额) → 解析应答 JSON (容忍代码块围栏) → DAG 结构校验 (9.4 同款) + 资源存在性校验 (agent_id/mcp_server_id/tool 防幻觉) → 失败携带错误反馈重试 1 次 → 返回校验通过的草稿。
- **API**：`POST /api/v1/workflows/ai-generate` (`workflow:write`)，入参 `{description}`，**不落库**；返回 `{name, description, definition, input_schema, model, model_id, model_name, attempts, total_tokens}`。详见 `docs/api/api.md` 9.16.1。
- **前端**：共享弹窗 `pages/workflow/AIGenerateWorkflowModal.tsx` (描述 → 生成 → 预览节点列表 → 确认/重新生成)；列表页「AI 生成」确认后自动创建草稿并进入编辑器，编辑器工具栏「AI 生成」确认后替换画布 (保存前可继续修改)。
- **测试**：`workflow_ai_generate_test.go` 覆盖 JSON 提取 (代码块/赘述)、校验失败重试、幻觉 ID 重试、无可用模型不重试、名称截断/兜底；mock-model-server 增加 `GEN_WORKFLOW` 触发器 (返回带围栏的固定工作流 JSON) 供 E2E。
