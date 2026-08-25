# 外部调用 API 参考（External Integration）

> 本文件描述平台后端面向外部系统提供的 4 个接口（**无需平台登录态**）：**外部调用 Agent**、**获取 Agent 续答结果**、**Webhook 触发工作流**、**查询工作流执行状态**。
> 其余平台接口（用户 JWT 认证）见 `api.md`；代码实现见 `backend/internal/api/*`。
> Python 对接示例（覆盖需审核 / 无需审核两种场景）见 `examples/external_invoke_agent.py` 与 `examples/webhook_trigger_workflow.py`。

## 通用约定

### Base URL 与协议

- 后端 API 根路径：`http://localhost:8080/api/v1`
- 除特别说明外，请求体与响应体均为 `application/json; charset=utf-8`
- 时间字段统一为 RFC3339（如 `2026-08-22T10:00:00+08:00`）

### 认证方式

本文件所有接口均不走用户 JWT，分为 2 类鉴权：

| 方式 | 适用接口 | 说明 |
| ---- | -------- | ---- |
| Agent API Key | `POST /agents/:id/invoke`、`GET /agents/:id/invoke/executions/:executionId`、`POST /agents/:id/invoke/executions/:executionId/cancel`、`GET /agents/:id/invoke/approvals/:approvalId` | `Authorization: Bearer akp_<64位hex>`；Key 在平台内为指定 Agent 创建（见 api.md「4.15 创建 API Key」） |
| Webhook Token | `POST /webhooks/workflows/:token`、`GET /webhooks/workflows/:token/executions/:id` | 无请求头；`token` 为创建工作流时生成的 32 位 hex `webhook_token`，在 URL 路径中 |

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
| 202 | `pending_approval`（message=`已受理, 等待人工审核`） | 已受理，等待人工审核（涉及需审核的 MCP 工具调用） |
| 400 | `validation_error` | 参数校验失败 |
| 401 | `unauthorized` | 未授权（Key 缺失/无效/过期） |
| 404 | `not_found` | 资源不存在 |
| 500 | `internal_error` / `wrapped_error` | 服务器内部错误 |

> 错误响应不含 `data` 字段，`message` 为可读错误描述。

---

## 1. 外部调用 Agent（API Key 认证）

- **用途**：外部系统以 Agent API Key 调用 Agent（与用户对话同一执行链路：模型路由 + 工具调用 + 审核门禁）。**不走用户 JWT**。
- **接口**：`POST /api/v1/agents/:id/invoke`
- **认证**：`Authorization: Bearer akp_<key>`
- **入参**（JSON body，`InvokeAgentRequest`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| ---- | ---- | ---- | ---- | ---- |
| `message` | string | 是 | ≤8192 | 用户提示词 |
| `session_id` | string | 否 | 该 Agent 下已存在的会话 | 指定则复用会话（多轮上下文）；不传则自动新建（外部会话，响应中返回，可续用） |

- **出参**：`202 accepted`，`data` 为 `InvokeAgentResult`（异步路径）：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `agent_id` | string | Agent ID |
| `key_prefix` | string | 使用的 Key 前缀 |
| `status` | string | `running`（任务已受理，执行中） |
| `execution_id` | string | **执行任务 ID**；凭它轮询下文「2. 获取执行任务状态」获取阶段/结果 |

- **降级同步路径**（无可用模型时）：返回 `200`，`data` 额外含 `reply` / `model_ok`（`false`）/ `mcp_details` / `tokens` / `latency_ms`（旧结构，行为不变）。
- **特殊状态**：执行中出现需人工审核的工具调用时，执行任务状态转为 `waiting_approval`，`result.pending_approvals` 携带待审核请求（`{approval_id, mcp_name, tool_name}`，对应工具**未执行**）；外部系统凭同一 API Key 轮询下文「4. 获取 Agent 续答结果」获取终态、工具执行结果与模型续答（审核决策后平台自动回填执行任务终态，也可回到「2. 获取执行任务状态」拿最终状态）。
- **错误**：`401` Key 无效/过期/已吊销；`404` Agent 不存在；`409` 实例未运行。

## 2. 获取执行任务状态（API Key 认证）

- **用途**：`/invoke` 返回 `202` 后，外部系统凭同一 API Key 轮询异步执行任务的**状态、进度阶段与结果**。`status` + `stage` + `last_activity_at` 三字段可明确区分「执行中」与「卡死」，无需调整调用方超时硬扛。
- **接口**：`GET /api/v1/agents/:id/invoke/executions/:executionId`
- **认证**：`Authorization: Bearer akp_<key>`
- **路径参数**：

| 参数 | 说明 |
| ---- | ---- |
| `id` | Agent ID（须为 Key 归属的 Agent） |
| `executionId` | `/invoke` `202` 响应的 `execution_id` |

- **权限边界**：Key 只能查询**本 Agent** 的执行任务；任务不存在、ID 非 UUID 或属于其他 Agent 时一律 `404`（不泄露存在性）。
- **出参**：`200`，`data` 为 `AgentExecution`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 执行任务 ID |
| `agent_id` / `source` / `session_id` | string | 归属 Agent / 来源（`api_invoke`）/ 会话 ID |
| `status` | string | `running` 执行中 / `waiting_approval` 等待人工审核 / `success` 成功 / `failed` 失败 / `stalled` 卡死 / `cancelled` 已取消（外部方主动放弃） |
| `stage` | string | 当前阶段：`queued` 已受理；`model:round=N` 第 N 次模型调用；`tool:<mcp名>/<工具名>` 正在执行该工具；`model:final (...)` 强制终答轮；`等待审核: ...` 等待人工审核 |
| `pending_approvals` | string[] \| null | 本次执行产生的审核请求 ID 数组（进入 `waiting_approval` 时回填；审核续答轮再次命中审核门禁时**累积追加**新审核单，终态保留全部） |
| `result` | object \| null | 执行结果（`success` 时回填 `ChatResult`：`reply` / `session_id` / `mcp_calls` / `total_tokens` / `latency_ms` 等）；进入 `waiting_approval` 时先存中间应答；审核决策完成后回填续答轮 `ChatResult`（含 `session_id` / `total_tokens` / `latency_ms`，为续答轮指标）并附加 `approval_id` / `approval_status` / `pre_review_mcp_calls`（命中审核门禁轮即**审核前**的工具调用明细，含对应 `pending` 项；多轮审核时累积各轮审核前明细） |
| `error` | string \| null | 失败/卡死原因 |
| `deadline` | string | 整体 deadline（平台预算：按工具轮数与单次模型/工具超时推导，调大工具超时会随之放大） |
| `last_activity_at` | string | 最近一次**进度心跳**（每次阶段推进刷新）；与当前时间的间隔即「已多久无进展」 |
| `started_at` / `finished_at` | string | 开始 / 结束时间 |

- **终态判定**：`status ∈ {success, failed, stalled, cancelled}` 即终态。
  - `success`：`result` 即模型应答与调用明细（含 `session_id`，可下一轮复用）。
  - `failed`：`error` 给出原因（执行错误 / 整体 deadline 耗尽 / 审核驳回或超时未执行 / 服务重启中断）。
  - `stalled`：平台 watchdog 判定**卡死**——running 任务超过「max(单次模型调用超时, 单次工具调用超时) + 60s」无进度心跳，任务已被主动取消；`error` 中 `stage=` 为卡死时所处阶段。
  - `cancelled`：外部方经「3. 取消执行任务」主动放弃任务（执行上下文已取消，进行中的模型/工具调用被中断）；`error` 为取消说明。
  - `running` 且 `stage=tool:<mcp>/<工具>` = 正在调用该工具；结合 `last_activity_at` 新鲜度即可判断存活，无需盲等。
- **轮询建议**：间隔 2~5s。进入 `waiting_approval` 后可转轮询下文「4. 获取 Agent 续答结果」；审核决策后平台自动回填本任务终态与模型续答。
- **错误**：`401` Key 无效/不属于该 Agent/已吊销/已过期；`404` 执行任务不存在或不属于该 Agent。
- **出参示例**（执行中，正在调用工具）：

```json
{
  "code": "success",
  "message": "ok",
  "data": {
    "id": "44012b19-9eb2-4621-869e-c9669a6c8b2d",
    "agent_id": "76ac5fc2-cd9a-41d1-85bb-8b8714175e2e",
    "source": "api_invoke",
    "session_id": "eaf56ae2-a9ed-4853-b3b4-e37921d697d3",
    "status": "running",
    "stage": "tool:ops-mcp/kb.search",
    "deadline": "2026-08-24T10:13:25+08:00",
    "last_activity_at": "2026-08-24T09:50:38+08:00",
    "started_at": "2026-08-24T09:50:33+08:00"
  }
}
```
## 3. 取消执行任务（API Key 认证）

- **用途**：外部方决定**放弃**一次未达终态的 `/invoke` 执行任务（业务侧超时、用户改变主意等）时，主动取消任务；平台取消该任务的执行上下文（透传至进行中的模型/MCP 调用），任务写入终态 `cancelled`，外部方无需继续轮询。
- **接口**：`POST /api/v1/agents/:id/invoke/executions/:executionId/cancel`
- **认证**：`Authorization: Bearer akp_<key>`
- **路径参数**：

| 参数 | 说明 |
| ---- | ---- |
| `id` | Agent ID（须为 Key 归属的 Agent） |
| `executionId` | `/invoke` `202` 响应的 `execution_id` |

- **请求体**：无。
- **权限边界**：同「2. 获取执行任务状态」；任务不存在或属于其他 Agent 时 `404`，`execution_id` 非 UUID 时 `400`。
- **取消语义**：
  - `running` 且任务在当前进程执行：取消立即生效，`cancelled=true`、`status=cancelled`；进行中的模型调用/MCP 工具调用随上下文被中断，任务 `error` 记录取消说明。
  - 已是终态（`success` / `failed` / `stalled` / `cancelled`）：**幂等**，`cancelled=false`、`status` 为当前终态——无需先判断终态再取消。
  - `waiting_approval`：`409`——任务在等待人工审核（无进行中的上下文可中断），请经「4. 获取 Agent 续答结果」端点或平台内审核决策。
  - `running` 但任务不在当前进程（如服务已重启）：`409`；此类任务在进程启动对账时已置为 `failed`，直接轮询「2. 获取执行任务状态」拿终态即可。
- **出参**：`200`，`data`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `execution_id` | string | 执行任务 ID |
| `cancelled` | bool | 本次调用是否触发了取消 |
| `status` | string | 操作后的任务状态（`cancelled` 或自然终态） |

- **与 watchdog 的关系**：本端点与平台 watchdog（卡死/超时判定）复用同一套进程内取消机制（逐任务执行上下文）；`stalled` / `failed`（deadline 耗尽）仍由平台判定标记，`cancelled` 专指外部方主动取消。
- **出参示例**（成功取消）：

```json
{
  "code": "success",
  "message": "ok",
  "data": {
    "execution_id": "44012b19-9eb2-4621-869e-c9669a6c8b2d",
    "cancelled": true,
    "status": "cancelled"
  }
}
```

## 4. 获取 Agent 续答结果（API Key 认证）

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

## 5. Webhook 触发工作流（公开端点）

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
  - 外部系统：`GET /api/v1/webhooks/workflows/:token/executions/<data.id>`（见下文「6. 查询工作流执行状态」，**仅需 webhook token**，返回状态视图，不含输入/输出 payload）
  - 平台内部：`GET /api/v1/workflow-executions/:id`（见 api.md「9.13 执行详情（含节点级记录）」，需 JWT，含节点级输入输出完整详情）
- **约束**：工作流须为 `active`。

## 6. 查询工作流执行状态（Webhook Token）

- **用途**：外部系统仅凭 webhook token 轮询**本工作流**的执行状态（无需用户 JWT）。
- **接口**：`GET /api/v1/webhooks/workflows/:token/executions/:id`
- **认证**：无 JWT；`token` 为工作流的 `webhook_token`（与「5. Webhook 触发工作流」相同）
- **入参**（path）：

| 参数 | 类型 | 说明 |
| ---- | ---- | ---- |
| `token` | string | 工作流 webhook_token（32 位 hex） |
| `id` | string | 执行 ID（「5. Webhook 触发工作流」触发响应的 `data.id`） |

- **权限边界**：
  - `token` 只能查询**其所属工作流**的执行；跨工作流查询、token 不存在均返回 `404`（不泄露其他工作流执行的存在性）
  - 仅返回**状态视图**，不含执行输入/输出与节点输入/输出 payload（完整详情请用 api.md「9.13 执行详情（含节点级记录）」的 JWT 端点）
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
