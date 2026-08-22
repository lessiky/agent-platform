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
| Agent API Key | `POST /agents/:id/invoke`、`GET /agents/:id/invoke/approvals/:approvalId` | `Authorization: Bearer akp_<64位hex>`；Key 在平台内为指定 Agent 创建（见 api.md「4.15 创建 API Key」） |
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

- **出参**：`200`，`data` 为 `InvokeAgentResult`：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `agent_id` | string | Agent ID |
| `key_prefix` | string | 使用的 Key 前缀 |
| `reply` | string | 模型应答文本 |
| `model` / `model_name` / `model_detail` | string | 实际使用的模型（故障转移后可能非首选） |
| `model_ok` | bool | 模型调用是否成功 |
| `mcp_details` | string[] | 工具调用日志行 |
| `pending_approvals` | array | 待审核请求：`{approval_id, mcp_name, tool_name}`（对应工具**未执行**） |
| `session_id` | string | 会话 ID（未指定时自动创建并返回，可下一轮复用） |
| `message_id` | string | 落库消息 ID |
| `tokens` | int | 该轮累计 token |
| `latency_ms` | int | 耗时 |

- **特殊状态**：存在待审核工具调用时返回 `202 pending_approval`（同结构），对应工具**未执行**；外部系统凭同一 API Key 轮询下文「2. 获取 Agent 续答结果」获取终态、工具执行结果与模型续答。
- **错误**：`401` Key 无效/过期/已吊销；`404` Agent 不存在。

## 2. 获取 Agent 续答结果（API Key 认证）

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

## 3. Webhook 触发工作流（公开端点）

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
  - 外部系统：`GET /api/v1/webhooks/workflows/:token/executions/<data.id>`（见下文「4. 查询工作流执行状态」，**仅需 webhook token**，返回状态视图，不含输入/输出 payload）
  - 平台内部：`GET /api/v1/workflow-executions/:id`（见 api.md「9.13 执行详情（含节点级记录）」，需 JWT，含节点级输入输出完整详情）
- **约束**：工作流须为 `active`。

## 4. 查询工作流执行状态（Webhook Token）

- **用途**：外部系统仅凭 webhook token 轮询**本工作流**的执行状态（无需用户 JWT）。
- **接口**：`GET /api/v1/webhooks/workflows/:token/executions/:id`
- **认证**：无 JWT；`token` 为工作流的 `webhook_token`（与「3. Webhook 触发工作流」相同）
- **入参**（path）：

| 参数 | 类型 | 说明 |
| ---- | ---- | ---- |
| `token` | string | 工作流 webhook_token（32 位 hex） |
| `id` | string | 执行 ID（「3. Webhook 触发工作流」触发响应的 `data.id`） |

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
