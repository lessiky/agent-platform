# Agent / 工作流审核功能 — 修改方案

> 目标:Agent 与工作流在新建/修改后进入"待审核"状态,必须由管理员审核通过后才可被调用/运行;
> 审核中的 Agent 禁止外部接口调用、禁止被工作流调用、禁止发起会话;
> 工作流运行前若包含审核中的 Agent 则报错;审核中的工作流运行(manual/cron/webhook)一律报错。

## 1. 总体设计

### 1.1 状态模型

为 `agents` 与 `workflows` 表各新增一个**独立审核状态字段** `review_status`,与现有运行时状态
(Agent: idle/running/stopped/error;Workflow: draft/active/archived)正交,互不污染:

| 取值 | 含义 | 可否调用/运行 |
|------|------|--------------|
| `pending` | 审核中(即"待审核",新建/修改后自动进入) | 否 |
| `approved` | 审核通过 | 是 |
| `rejected` | 已驳回 | 否(编辑保存后自动回到 pending) |

状态机:

```
新建            → pending
pending + 通过   → approved
pending + 驳回   → rejected
approved + 内容修改 → pending   (重新审核)
rejected + 内容修改 → pending   (重新审核)
```

**存量数据**:列默认值 `approved`(PostgreSQL `ADD COLUMN ... DEFAULT` 会自动回填历史行),
升级后存量 Agent/工作流不受影响,无需管理员重新审核。

**"内容修改"的范围**(触发重新审核):
- Agent:`CreateAgent`、`UpdateAgent`(表单保存)、`RollbackAgent`(版本回滚)、`UpdateAgentSkills`(技能绑定变更)
- Workflow:`Create`、`Update`(名称/描述/DAG/schema)、`UpdateSchedule`(调度配置影响运行行为,纳入范围)
- 不触发:Agent Start/Stop、API Key 增删、Workflow Activate/Archive(生命周期操作)、删除

> 决策点 A:Activate 不要求先审核通过(审核只卡"运行"),激活后若 review_status=pending,
> 触发时仍会被拦截。如需"激活必须已审核"可在 `Activate` 加一行校验,按业务偏好二选一。
> 决策点 B:UpdateSchedule 纳入重新审核范围(影响 cron/输入);如认为调度调整不需要审核,去掉该处即可。

### 1.2 权限(RBAC)

沿用 `mcp:approve` 的模式,新增两个权限码,种子数据中**仅 admin 角色**默认拥有:

| 权限码 | 含义 | admin | operator | user |
|--------|------|:-----:|:--------:|:----:|
| `agent:review` | 审核 Agent | ✓ | ✗ | ✗ |
| `workflow:review` | 审核工作流 | ✓ | ✗ | ✗ |

满足"必须由管理员权限的用户审核";后续需要可再授权给自定义角色,无需改代码。

### 1.3 调用拦截(后端单点收敛)

**Agent 的三条外部路径全部收敛到 chatService 的 4 个入口**,在入口取到 agent 后统一校验,一处改动全覆盖:

| 需求路径 | 后端入口 |
|----------|----------|
| 外部接口调用(API Key `/invoke` 同步/异步) | `chatService.Invoke` / `InvokeAsync` |
| 被工作流调用(agent 节点) | `workflowEngine.runAgentNode → chatService.Chat` |
| 发起会话(控制台) | `chatService.Chat` / `ChatStream` |

校验规则:`review_status != approved` → 400 错误,文案区分 pending("Agent 审核中, 管理员审核通过前禁止调用")
与 rejected("Agent 审核已驳回, 请修改后重新提交审核")。

**工作流的三种触发路径全部收敛到 `workflowService.Trigger`**(manual → 直接调用;cron → scheduler job 调用;
webhook → `HandleWebhook` 内部调用),在 Trigger 创建执行记录前新增两道校验:

1. 工作流自身:`review_status != approved` → 400 "工作流审核中, 禁止运行 (手工/定时/Webhook 调用均拦截)"
2. **Agent 预检**:解析 definition,收集所有 agent 节点 `config.agent_id`(去重)→ 批量查 agents →
   任一 `review_status != approved` → 400 "工作流包含审核中的 Agent: [名称列表], 禁止运行"

chatService 层的校验作为运行期间状态变化的兜底(例如执行中途 agent 被再次修改)。

## 2. 数据模型改动

### 2.1 `backend/internal/model/agent.go`

```go
// Agent 审核状态
const (
    AgentReviewPending  = "pending"  // 审核中 (待审核)
    AgentReviewApproved = "approved" // 审核通过
    AgentReviewRejected = "rejected" // 已驳回
)

// Agent 结构体新增:
ReviewStatus  string     `gorm:"type:varchar(16);not null;default:'approved';index" json:"review_status"`
ReviewedBy    *string    `gorm:"type:uuid" json:"reviewed_by"`
ReviewedAt    *time.Time `json:"reviewed_at"`
ReviewComment *string    `gorm:"type:text" json:"review_comment"` // 审核意见 / 驳回原因
```

### 2.2 `backend/internal/model/workflow.go`

```go
// 工作流审核状态
const (
    WorkflowReviewPending  = "pending"
    WorkflowReviewApproved = "approved"
    WorkflowReviewRejected = "rejected"
)

// Workflow 结构体新增 (字段定义同 Agent):
ReviewStatus  string     `gorm:"type:varchar(16);not null;default:'approved';index" json:"review_status"`
ReviewedBy    *string    `gorm:"type:uuid" json:"reviewed_by"`
ReviewedAt    *time.Time `json:"reviewed_at"`
ReviewComment *string    `gorm:"type:text" json:"review_comment"`
```

### 2.3 迁移

`database.AutoMigrate` 已注册这两个模型,新增列随 AutoMigrate 自动创建;
DEFAULT 'approved' 保证存量行回填为"审核通过",**无需额外 SQL 迁移**。

## 3. 后端改动清单

### 3.1 新增服务 `backend/internal/service/review_service.go`

```go
type ReviewService interface {
    ListPendingAgents(ctx context.Context, filter repository.AgentListFilter) ([]model.Agent, int64, error)
    ListPendingWorkflows(ctx context.Context, filter repository.WorkflowListFilter) ([]model.Workflow, int64, error)
    ReviewAgent(ctx context.Context, id, decision, comment, operatorID, operatorName, ip string) (*model.Agent, error)
    ReviewWorkflow(ctx context.Context, id, decision, comment, operatorID, operatorName, ip string) (*model.Workflow, error)
}
```

- decision: `approve` / `reject`;reject 建议必填 comment(驳回原因)
- 状态约束:pending 可 approve 可 reject;rejected 只能 approve(便于管理员纠正误驳回),不能 reject
- approved 状态不可再操作(已是终态,内容变更会自动回到 pending)
- 落库:`review_status` + `reviewed_by` + `reviewed_at` + `review_comment`
- 审计:复用现有 `AuditLog`,action 用 `agent.review.approve` / `agent.review.reject` / `workflow.review.approve` / `workflow.review.reject`,detail 记录 id/decision/comment

### 3.2 新增 API `backend/internal/api/review/handler.go`

```
GET  /api/v1/reviews/pending?type=agent|workflow   审核队列 (分页, 管理员审核中心用)
POST /api/v1/agents/:id/review       {"decision":"approve|reject","comment":"..."}
POST /api/v1/workflows/:id/review    {"decision":"approve|reject","comment":"..."}
```

- 中间件:`middleware.AuthCheck("agent:review")` / `middleware.AuthCheck("workflow:review")`(非管理员 → 403)
- 依赖:ReviewService(注入 AgentRepository / WorkflowRepository / AuditLogRepository)

### 3.3 现有列表接口支持审核状态过滤

- `repository.AgentListFilter` 增加 `ReviewStatus string`;`agentRepository.List` 增加 where
- `repository.WorkflowListFilter` 增加 `ReviewStatus string`;同上
- `GET /agents?review_status=pending`、`GET /workflows?review_status=pending` 即可查队列(前端也可直接用)

### 3.4 `agent_service.go` — 状态流转

- `CreateAgent`:构造 Agent 时 `ReviewStatus: model.AgentReviewPending`,清空 ReviewedBy/At/Comment
- `UpdateAgent`:保存成功后 `ReviewStatus = pending`,清空审核信息(现实现每次保存都会 Version++,与此一致)
- `RollbackAgent`:恢复配置后同样重置 pending
- `UpdateAgentSkills`:技能绑定变更,重置 pending
- Start/Stop、API Key 相关、Delete 不动

### 3.5 `chat_service.go` — 调用拦截(需求 1.2)

在 `Chat` / `ChatStream` / `Invoke` / `InvokeAsync` 四个入口取到 agent 后调用统一 helper:

```go
// assertAgentUsable 审核门: 仅 approved 的 Agent 可被调用 (外部调用/工作流节点/会话)
func assertAgentUsable(agent *model.Agent) error {
    switch agent.ReviewStatus {
    case model.AgentReviewPending:
        return errors.NewValidationError("Agent 审核中, 管理员审核通过前禁止调用")
    case model.AgentReviewRejected:
        return errors.NewValidationError("Agent 审核已驳回, 请修改后重新提交审核")
    }
    return nil
}
```

### 3.6 `workflow_service.go` — 触发拦截(需求 1.3 / 2.2)

- `Create`:`ReviewStatus = pending`
- `Update`:`changed == true` 时重置 pending(现有代码已有 changed 判断,零开销)
- `UpdateSchedule`:重置 pending(见决策点 B)
- `Trigger` 开头新增:

```go
// 1) 工作流自身审核门
if workflow.ReviewStatus != model.WorkflowReviewApproved {
    return nil, errors.NewValidationError("工作流审核中, 禁止运行 (手工/定时/Webhook 调用均拦截)")
}
// 2) Agent 预检: 任一 agent 节点引用审核中/被驳回的 Agent 则报错
if err := s.checkAgentsUsable(ctx, def); err != nil {
    return nil, err
}
```

`checkAgentsUsable` 实现:遍历 `def.Nodes` 中 `Type == NodeTypeAgent` 的节点,
从 `node.Config["agent_id"]` 收集 ID(去重)→ `agentRepo.ListByIDs` 批量查询 →
汇总非 approved 的 agent 名称,返回带名称列表的 ValidationError。

- `NewWorkflowService` 增加 `repository.AgentRepository` 参数(main.go 第 305 行装配处传入 `repository.NewAgentRepository()`)

### 3.7 仓库层

- `AgentRepository` 新增 `ListByIDs(ctx, ids []string) ([]model.Agent, error)`(预检用)
- Agent/Workflow 列表 Filter 加 `ReviewStatus`(见 3.3)

### 3.8 `database/seed.go` — 权限种子

- permissions 增加 `agent:review`(Agent 审核)、`workflow:review`(工作流审核)
- admin 全量获得(现有循环自动);operator 排除列表加入这两个 code
- 更新结尾日志文案(权限数量)

### 3.9 `cmd/server/main.go` — 装配

- 创建 `reviewService := service.NewReviewService(...)`
- 创建 `reviewHandler := review.NewHandler(reviewService)`,`reviewHandler.RegisterRoutes(api)`(约 428-438 行处)
- `NewWorkflowService` 传入 AgentRepository

> 调度器 `WorkflowScheduler` 无需改动:审核中的工作流 cron 条目照常注册,
> 到点 Trigger 被审核门拒绝,仅产生一条错误日志;审核通过后下个周期自动恢复。

## 4. 前端改动清单

### 4.1 类型与常量

- `src/types/index.ts`:`Agent`、`Workflow` 增加 `review_status: 'pending'|'approved'|'rejected'`、`reviewed_by?`、`reviewed_at?`、`review_comment?`
- `src/utils/constants.ts` 新增:

```ts
export const REVIEW_STATUS_MAP = {
  pending:  { label: '审核中', color: 'orange' },
  approved: { label: '审核通过', color: 'green' },
  rejected: { label: '已驳回', color: 'red' },
};
```

### 4.2 API 层

- `src/api/review.ts`(新增):`pendingList({type, page, size})`、`reviewAgent(id, decision, comment)`、`reviewWorkflow(id, decision, comment)`
- `src/api/agent.ts` / `src/api/workflow.ts`:list 参数支持 `review_status`

### 4.3 状态展示

- `AgentListPage.tsx`:新增"审核状态"列(Tag)
- `WorkflowListPage.tsx`:新增"审核状态"列(Tag);STATUS_MAP 不变(生命周期状态与审核状态分开展示)
- `AgentDetailPage.tsx` / `WorkflowDetailPage.tsx`:头部展示审核状态;`rejected` 时用 Alert 展示驳回原因(`review_comment`)

### 4.4 交互拦截

- `AgentFormPage.tsx` / `WorkflowEditorPage.tsx`:保存成功提示改为
  "已保存, Agent 已进入审核中状态, 需管理员审核通过后方可使用"(工作流同理:"审核通过前不可运行")
- `ChatPanel.tsx`:`agent.review_status !== 'approved'` 时禁用输入框并提示原因(后端同样拦截,双保险)
- `WorkflowDetailPage.tsx`:触发按钮在审核中时置灰 + Tooltip "审核中, 暂不可运行"

### 4.5 新增页面:审核中心 `src/pages/review/ReviewCenterPage.tsx`

- 路由 `/reviews`(MainLayout 子路由),用现有 `RequirePermission` 守卫(`agent:review` 或 `workflow:review` 任一)
- 侧边菜单"审核中心"项,仅持有审核权限时可见(参考 MainLayout 现有权限过滤写法)
- 两个 Tab:**Agent 审核** / **工作流审核**
  - 列表:名称、版本、提交人(created_by/updated_by)、更新时间、(驳回原因)
  - 操作:查看详情、通过、驳回(Modal 输入意见,驳回必填)
  - Agent 详情:当前配置快照(模型/系统提示词/MCP 绑定/技能/知识库)+ 版本历史
  - 工作流详情:DAG 节点列表(高亮 agent 节点及其审核状态)+ 版本历史
  - 审核人视角:可看到该工作流引用的 Agent 哪些还在审核中,避免"工作流通过但 Agent 没通过"的困惑

## 5. 边界情况与决策汇总

| # | 场景 | 处理 |
|---|------|------|
| 1 | 审核中实体存在运行中执行 | 不强杀;后续调用 agent 的节点被 chatService 兜底拦截,节点失败、执行失败 |
| 2 | 工作流引用审核中的 Agent 后保存工作流 | 允许保存(需求只要求运行前报错);可选:编辑器保存时黄色提示(可后续加) |
| 3 | API Key 外部调用审核中的 Agent | 400 + 明确文案(code=validation_error,与鉴权失败 401/403 可区分) |
| 4 | 模拟流量(runtime) | 只写日志/统计,不走对话链,不拦截;可选:审核中关闭 simulate_traffic |
| 5 | 审核中的定时任务 | cron 照常注册,到点被 Trigger 拒绝(日志可见),审核通过后自动恢复 |
| 6 | Webhook 状态查询端点 | 不受影响 |
| 7 | 管理员审核自己创建的内容 | 允许(需求未限制);如需"禁止自审"在 ReviewService 加一行判断即可 |
| 8 | 存量数据 | 默认 approved,不锁定 |
| 9 | 驳回后处理 | 列表/详情展示驳回原因;编辑保存自动回到 pending |
| 10 | 激活(activate)审核中的工作流 | 允许激活,但触发仍被拦截(见决策点 A) |

## 6. 实施步骤(建议顺序)

1. **模型与基础设施**:model 加字段/常量、seed 权限、repository Filter/ListByIDs(AutoMigrate 自动建列)
2. **状态流转**:AgentService(Create/Update/Rollback/UpdateSkills)、WorkflowService(Create/Update/UpdateSchedule)
3. **审核服务与 API**:ReviewService + review handler + main.go 装配 + AuditLog
4. **运行拦截**:chatService 四入口断言、Trigger 双预检(依赖步骤 1 的 ListByIDs)
5. **前端基础**:types/constants/api + 列表/详情状态列与驳回提示
6. **前端交互**:表单保存提示、ChatPanel/触发按钮禁用
7. **审核中心页面** + 路由 + 菜单
8. **测试**:单测 + e2e

## 7. 测试要点

**后端单测**(service 层,参照现有 `*_test.go` 模式):
- Create/Update Agent/Workflow → review_status=pending;Update 无变化(workflow)不变
- 非 pending 状态审核 → 报错;pending approve/reject → 状态与审核人/时间/意见正确
- chatService:pending/rejected agent → 四入口全部拒绝;approved → 放行
- Trigger:审核中工作流 → 拒绝;含审核中 agent 的工作流 → 报错且带 agent 名称;cron/webhook 路径同样命中
- 权限:operator 角色调用审核端点 → 403

**E2E**(参照 `tests/webhook-e2e.ps1` 模式新增 `tests/review-e2e.ps1`):
- 普通用户登录 → 创建 agent/工作流 → 列表显示"审核中"
- /invoke、/chat、工作流手动触发、webhook 触发、cron 触发 → 全部报错
- 非管理员调审核 API → 403;管理员驳回 → 展示原因;修改后重新提交 → 通过 → 三条调用路径恢复正常

## 8. 改动文件清单(汇总)

**后端**
- `backend/internal/model/agent.go`、`backend/internal/model/workflow.go` — 字段与常量
- `backend/internal/database/seed.go` — 两个新权限
- `backend/internal/repository/agent_repository.go`、`workflow_repository.go` — Filter 过滤 + ListByIDs
- `backend/internal/service/agent_service.go`、`workflow_service.go`、`chat_service.go` — 状态流转与拦截
- `backend/internal/service/review_service.go` — 新增
- `backend/internal/api/review/handler.go` — 新增
- `backend/cmd/server/main.go` — 装配

**前端**
- `frontend/src/types/index.ts`、`utils/constants.ts`、`api/review.ts`(新增)、`api/agent.ts`、`api/workflow.ts`
- `frontend/src/pages/agent/AgentListPage.tsx`、`AgentDetailPage.tsx`、`AgentFormPage.tsx`、`ChatPanel.tsx`
- `frontend/src/pages/workflow/WorkflowListPage.tsx`、`WorkflowDetailPage.tsx`、`WorkflowEditorPage.tsx`
- `frontend/src/pages/review/ReviewCenterPage.tsx`(新增)、`router/index.tsx`、`layouts/MainLayout.tsx`(菜单)

## 9. 实施记录 (2026-09-15, 已实现并部署)

**后端**
- 模型: Agent/Workflow 新增 review_status/reviewed_by/reviewed_at/review_comment (默认 approved, 存量数据不锁定)
- 权限: agent:review / workflow:review (仅 admin 默认持有, 种子 19 个权限)
- 状态流转: Agent 新建/修改/回滚/技能绑定变更 -> pending; Workflow 新建/内容修改/调度变更 -> pending
- 审核服务: service/review_service.go + api/review/handler.go
  - GET /api/v1/reviews/pending?type=agent|workflow (对应审核权限)
  - POST /api/v1/agents/:id/review 与 /api/v1/workflows/:id/review (approve/reject, 驳回必填意见, 审计落 AuditLog)
  - 状态约束: pending 可 approve/reject; rejected 仅可 approve (纠正误驳回); approved 为终态
- 调用拦截: chatService 四入口 (Chat/ChatStream/Invoke/InvokeAsync) 统一 assertAgentUsable
- 触发拦截: workflowService.Trigger 双道审核门 (工作流自身 + agent 节点预检, 报错含 agent 名称); manual/cron/webhook 全收敛
- 仓库: AgentRepository.ListByIDs, 列表 Filter 支持 review_status

**前端**
- 类型/常量/API: ReviewStatus、REVIEW_STATUS_MAP、api/review.ts
- 列表页新增"审核状态"列; 详情页审核标签 + 审核中/驳回 Alert; ChatPanel 审核中禁用输入; 工作流触发按钮审核中禁用; 表单保存提示
- 新增"发布审核"页 /reviews (RequireAnyPermission 守卫, 双 Tab 实时计数, 查看/通过/驳回)
- 侧边菜单"发布审核" (持任一审核权限可见)

**验证**
- 单测: internal/service/review_feature_test.go (状态流转/审核门/预检/驳回流程) + 全量 go test 通过
- E2E: tests/review-e2e.ps1 25/25 通过 (operator 403、驳回流程、webhook 拦截、agent 预检带名称报错)
- UI: playwright 截图确认 (审核中心/Agent 列表/工作流 Tab)
- 部署: docker compose up -d --build backend frontend 完成, 8080/8081 运行新版本
