# Agent 管理平台 — Phase 2 开发计划

> **版本：** v1.0
> **日期：** 2026-08-20
> **周期：** 8 周（PRD 建议 6-8 周，本计划取上限留缓冲）
> **前置条件：** Phase 1（M1–M5，含 M2.5 Agent 对话、M4.5 工具审核）已交付

---

## 1. Phase 2 目标

将 Phase 1 的 MVP 打造成**可观测、可运维、易用**的生产就绪平台。三条主线：

1. **可观测**：告警与通知中心（M6）、Prometheus 指标 + 性能画像（M7）
2. **易操作**：工作流编辑器打磨、模板市场、批量操作（M8）
3. **能力补齐**：Agent 分组、Agent 配额、MCP 版本管理、观察者角色（M8，PRD P1/P2 遗留项）

| 里程碑 | 交付内容 | 周期 |
|--------|----------|------|
| **M6: 告警与通知中心** | 指标汇聚 (rollup)、告警规则引擎、多通道通知 (webhook/邮件/钉钉/企微)、告警生命周期与静默、告警中心 UI | 2 周 |
| **M7: 监控增强** | Prometheus /metrics、Grafana 面板、P50/P95/P99 性能画像、跨模块执行追踪 | 2 周 |
| **M8: 体验优化** | 编辑器打磨、模板市场、批量操作、Agent 分组/配额、MCP 版本管理、观察者角色、UI 细节 | 3 周 |
| **H1: 加固与验收** | 限流、全局审计日志、E2E/压测、文档、Phase 2 评审 | 1 周 |

### 1.1 范围调整说明（相对 PRD 第 8 章）

| PRD Phase 2 条目 | 本计划处理 | 原因 |
|------------------|-----------|------|
| M6 Cron 调度、Webhook 触发 | 不重复开发 | 已在 M5 交付（见 `docs/phase1/M5-implementation-summary.md`） |
| M6 告警规则 | 扩展为完整告警与通知中心 | PRD 仅列"告警规则"，补全通道/生命周期/静默才可用 |
| M7 Prometheus / Grafana / 性能画像 | 全部纳入；性能画像 (PRD P2) 提前为本阶段页面功能 | 与 M6 的 rollup 数据基建复用，边际成本低 |
| M8 编辑器打磨、模板市场、批量操作 | 全部纳入；模板市场同时覆盖 Agent 与工作流 | 模板化是降低使用门槛的核心体验 |
| PRD 未列入 Phase 2 的 P1/P2 项 | Agent 分组 (P1)、Agent 配额 (P2)、MCP 版本管理 (P1)、观察者角色 (P1) 纳入 M8 | 属于"完善"范畴，且实现量可控 |

### 1.2 周度总览

| 周次 | 里程碑 | 主题 |
|------|--------|------|
| W1 | M6 | rollup 数据基础 + 规则引擎 |
| W2 | M6 | 通知通道 + 告警生命周期 + 告警中心 UI |
| W3 | M7 | Prometheus 指标体系 |
| W4 | M7 | Grafana 面板 + 性能画像 + 执行追踪 |
| W5 | M8 | 编辑器打磨 + 模板后端 |
| W6 | M8 | 模板市场 UI + 批量操作 + Agent 分组 |
| W7 | M8 | Agent 配额 + MCP 版本管理 + 观察者角色 + UI 细节 |
| W8 | H1 | 加固、测试、文档、验收 |

---

## 2. Phase 1 回顾（本阶段输入）

**已交付能力：**
- M1 认证 (JWT) + RBAC（12 权限点，`domain:read|write|execute|approve|manage` 命名）
- M2 Agent 管理（CRUD/实例/版本回滚/API Key/调用统计/日志/看板）
- M2.5 Agent 对话（系统提示词版本化、多轮会话、对话内工具调用走审核、execution_id 可追溯）
- M3 MCP 管理（注册/工具发现/凭证 AES-256-GCM 加密/健康监控）
- M4 模型管理（模板 CRUD/连通性测试/双维度配额/优先级路由）
- M4.5 工具人工审核（工具级开关/生命周期/审核中心/审计日志）
- M5 工作流（DAG 五类节点/重试/超时/变量传递/Cron/Webhook/审核挂起恢复/可视化编辑器）

**技术基础：** Go (Gin) + GORM + PostgreSQL 16 + Redis 7 + robfig/cron 进程内调度（未引入消息队列）；前端 React 18 + TypeScript + Ant Design + Vite；Playwright E2E 已建立。

**遗留待办（本阶段吸收）：**
- 无告警能力（Phase 1 风险表"审核积压 P2 通知"未落地）
- 无外部指标导出（/metrics），性能画像 (P2) 未做
- MCP 配置无版本管理 (P1)
- 无全局审计日志 UI（M4.5 审计仅覆盖审核操作）
- 无观察者（只读）角色（PRD 1.3 角色定义）
- Agent 分组 (P1)、Agent 配额 (P2) 未做

---

## 3. 技术选型（本阶段新增）

| 项 | 技术 | 说明 |
|----|------|------|
| 指标客户端 | prometheus/client_golang | /metrics 暴露 |
| 监控栈 | Prometheus + Grafana（dev 容器化） | 扩展 infra/docker-compose.yml |
| 邮件通知 | net/smtp（Go 标准库） | 不引入新依赖 |
| 指标汇聚 | PostgreSQL rollup 表 + 进程内定时任务 | M6 产出，M7 性能画像复用 |
| 限流 | Redis 令牌桶（自研轻量实现） | API Key / Agent 粒度 |
| 编辑器撤销重做 | JSON 快照栈（限深 50） | 不引入新状态管理库 |

**设计原则：** 延续 Phase 1 的"进程内调度 + 单二进制部署"模式，不引入 Kafka/RabbitMQ；所有新增组件可在单机 docker-compose 内运行。

---

## 4. M6: 告警与通知中心（第 1-2 周）

### 4.1 目标

建立平台级告警能力：指标汇聚 → 规则判定 → 多通道通知 → 生命周期管理（触发/确认/恢复/静默），并配套告警中心 UI。

### 4.2 架构设计

```
现有数据源                          M6 组件
┌─────────────────┐          ┌──────────────────┐
│ Agent 调用记录   │          │                  │
│ 模型用量/配额    │──定时采集──▶  rollup 收集器    │ (cron, 分钟级→小时粒度)
│ MCP 健康历史     │          │  stat_hourly 表   │
│ 工作流执行记录   │          └────────┬─────────┘
│ 审核待办计数     │                   │ 查询
└─────────────────┘          ┌────────▼─────────┐      ┌──────────────────┐
                             │  规则引擎 (cron)   │─────▶│  通知器           │
                             │  阈值/窗口/级别    │      │  webhook/邮件/   │
                             └────────┬─────────┘      │  钉钉/企微        │
                                      │                └──────────────────┘
                             ┌────────▼─────────┐
                             │  事件管理          │  去重聚合 / 确认 / 恢复 / 静默
                             │  alert_events     │
                             └──────────────────┘
```

**rollup 数据源说明：** 从现有表（agent 调用记录、model 用量、mcp_health_history、workflow 执行记录、approval 待办）按小时粒度聚合出：调用次数、错误次数、P50/P95/P99 延迟、token 消耗。该表同时作为 M7 性能画像的数据源（避免二次实现）。

### 4.3 数据模型

| 表 | 主要字段 | 说明 |
|----|----------|------|
| `stat_hourly` | dim_type(agent/model/mcp/workflow/platform), dim_id, hour, calls, errors, p50_ms, p95_ms, p99_ms, tokens, pending_approvals | 小时粒度指标快照，保留 30 天，定时清理 |
| `alert_channels` | name, type(webhook/email/dingtalk/wecom), config(JSONB, 凭证脱敏存储), enabled | 通知通道 |
| `alert_rules` | name, metric, dim_type, dim_id(空=全局), window(分钟), op(>,<,>=,<=), threshold, level(info/warning/critical), channel_ids, enabled, notify_on_recover | 告警规则 |
| `alert_events` | rule_id, dim_type, dim_id, level, status(triggered/acked/resolved), triggered_at, acked_by, resolved_at, detail(JSONB: 当前值/阈值) | 告警事件 |
| `alert_silences` | scope(rule_id/dim), start_at, end_at, reason, created_by | 静默配置 |

### 4.4 内置推荐规则（预置，可编辑/禁用）

| 规则 | 判定 | 默认级别 |
|------|------|----------|
| MCP 健康检查失败 | 最近 1 次健康检查失败 | warning |
| 模型服务不可用 | 模型连通性检查失败 | critical |
| 模型配额使用率 | 日配额消耗 ≥ 80% / 100% | warning / critical |
| Agent 错误率 | 10 分钟窗口错误率 > 30% | warning |
| Agent 调用延迟 | P95 延迟超过配置阈值 | warning |
| 工作流执行失败率 | 1 小时窗口失败率 > 50% 且 ≥3 次 | warning |
| 审核积压 | 待审核请求数 > N（默认 20） | warning |

### 4.5 详细任务

#### Week 1: 数据基础 + 规则引擎（后端）

| 序号 | 任务 | 工时 | 负责人 | 前置条件 |
|------|------|------|--------|----------|
| 1.1 | 设计 `stat_hourly` rollup 表 + 索引 + 保留策略 (30 天清理任务) | 0.5d | 后端 | - |
| 1.2 | 实现 rollup 收集器（cron 小时级聚合，复用现有查询；分钟级实时值缓存于 Redis） | 1.5d | 后端 | 1.1 |
| 1.3 | 告警四表数据模型 + GORM AutoMigrate | 0.5d | 后端 | - |
| 1.4 | 规则引擎核心（指标查询 → 阈值比较 → 级别判定 → 同规则同目标去重聚合） | 2d | 后端 | 1.1, 1.3 |
| 1.5 | webhook 通知通道（JSON payload + 可选 HMAC-SHA256 签名 + 3 次重试） | 1d | 后端 | 1.3 |

**交付物：** rollup 数据管道、规则引擎（可单测驱动）、webhook 通道。

#### Week 2: 通道 + 生命周期 + 前端（后端 + 前端）

| 序号 | 任务 | 工时 | 负责人 | 前置条件 |
|------|------|------|--------|----------|
| 2.1 | 邮件通道（net/smtp，HTML 模板，SMTP 配置管理） | 1d | 后端 | 1.5 |
| 2.2 | 钉钉/企微机器人通道（复用 webhook，Markdown 模板） | 0.5d | 后端 | 1.5 |
| 2.3 | 告警生命周期（triggered → acked → resolved，恢复通知，事件聚合防风暴） | 1.5d | 后端 | 1.4 |
| 2.4 | 静默能力（按规则/目标维度，时间范围，优先级：静默 > 规则禁用） | 0.5d | 后端 | 2.3 |
| 2.5 | 内置推荐规则预置数据（4.4 节 7 条） | 0.5d | 后端 | 1.4 |
| 2.6 | 告警 API（见 §4.6）+ 权限点 `alert:read` / `alert:write` | 1d | 后端 | 2.3 |
| 2.7 | 告警中心前端：规则列表/创建/编辑/启停、事件历史（状态过滤+确认操作）、通道管理、静默管理 | 2.5d | 前端 | 2.6 |
| 2.8 | 全局概览页告警摘要横幅 + 顶栏待处理告警徽标 | 0.5d | 前端 | 2.7 |
| 2.9 | M6 集成测试（触发→通知→确认→恢复全链路）+ 阶段 Demo | 0.5d | 全员 | 2.8 |

**交付物：** 完整告警与通知中心（后端 + 前端）+ 7 条预置规则。

### 4.6 API 设计

```
GET    /api/v1/alerts/rules                  # 规则列表 (q/enabled 过滤, 分页)
POST   /api/v1/alerts/rules                  # 创建规则 (创建时试算当前值)
PUT    /api/v1/alerts/rules/:id              # 更新规则
DELETE /api/v1/alerts/rules/:id              # 删除规则
GET    /api/v1/alerts/events                 # 事件历史 (status/level/dim_type/dim_id/from/to, 分页)
POST   /api/v1/alerts/events/:id/ack         # 确认告警
GET    /api/v1/alerts/channels               # 通道列表 (config 脱敏)
POST   /api/v1/alerts/channels               # 创建通道 (创建时发测试通知)
PUT    /api/v1/alerts/channels/:id
DELETE /api/v1/alerts/channels/:id           # 被规则引用时禁删
GET    /api/v1/alerts/silences
POST   /api/v1/alerts/silences
DELETE /api/v1/alerts/silences/:id
GET    /api/v1/alerts/summary                # 概览: 按级别统计当前 active 事件
```

权限：`alert:read`（viewer/operator/admin）、`alert:write`（operator/admin）。

---

## 5. M7: 监控增强（第 3-4 周）

### 5.1 目标

对外暴露 Prometheus 指标（供生产环境抓取），提供 Grafana 现成面板；平台内提供性能画像页面（P50/P95/P99 + 错误率趋势）与跨模块执行追踪下钻。

### 5.2 指标设计（/metrics）

| 域 | 指标 | 类型 | 标签 |
|----|------|------|------|
| HTTP | `ap_http_requests_total` / `ap_http_request_duration_seconds` | counter / histogram | method, route, status |
| Agent | `ap_agent_invoke_total` / `ap_agent_invoke_duration_seconds` / `ap_agent_tokens_total` | counter / histogram / counter | agent_id |
| 模型 | `ap_model_invoke_total` / `ap_model_invoke_duration_seconds` / `ap_model_tokens_total` | counter / histogram / counter | model_id, provider |
| MCP | `ap_mcp_tool_call_total` / `ap_mcp_tool_call_duration_seconds` | counter / histogram | server_id, tool |
| MCP 健康 | `ap_mcp_server_up` / `ap_mcp_health_check_total` | gauge / counter | server_id, result |
| 工作流 | `ap_workflow_run_total` / `ap_workflow_run_duration_seconds` / `ap_workflow_node_duration_seconds` | counter / histogram / histogram | workflow_id, status / node_type |
| 审核 | `ap_approval_pending` / `ap_approval_timeout_total` | gauge / counter | - |
| 系统 | pg/redis 连接池占用、goroutine、GC | gauge | - |

约定：统一 `ap_` 前缀；histogram bucket 覆盖 10ms–30s；`/metrics` 独立于 `/api` 路由，默认仅监听内网 + 可选 Bearer Token。

### 5.3 详细任务

#### Week 3: Prometheus 指标体系（后端）

| 序号 | 任务 | 工时 | 负责人 | 前置条件 |
|------|------|------|--------|----------|
| 3.1 | 引入 client_golang，/metrics 端点（独立路由 + 可选 token 鉴权） | 0.5d | 后端 | - |
| 3.2 | HTTP 中间件指标（route 模板化，防 label 爆炸） | 0.5d | 后端 | 3.1 |
| 3.3 | 业务指标埋点：agent/model/mcp/workflow/approval 五组（嵌入现有调用链，复用 execution_id 上下文） | 2d | 后端 | 3.1 |
| 3.4 | 系统指标：pg 连接池 / redis 池 / goroutine / GC | 0.5d | 后端 | 3.1 |
| 3.5 | infra/docker-compose 增加 prometheus + grafana 服务 + 抓取配置 | 0.5d | 后端 | - |
| 3.6 | 指标单测（promtest 校验指标名/标签/bucket） | 0.5d | 后端 | 3.3 |
| 3.7 | 缓冲 + 代码评审 | 0.5d | 后端 | - |

**交付物：** 完整 /metrics 端点、dev 监控栈（compose 一键起）。

#### Week 4: Grafana + 性能画像 + 执行追踪（后端 + 前端）

| 序号 | 任务 | 工时 | 负责人 | 前置条件 |
|------|------|------|--------|----------|
| 4.1 | Grafana dashboard JSON（4 行：平台概览 / Agent / 模型 / MCP+工作流），提交至 infra/grafana/ | 1.5d | 后端 | 3.3, 3.5 |
| 4.2 | Agent 性能画像 tab：P50/P95/P99 延迟分布、错误率趋势、token 消耗趋势（数据源 stat_hourly，from/to 区间） | 1.5d | 前端 | M6-1.2 |
| 4.3 | 模型管理使用统计页：按模型的调用量/延迟/错误率/token/配额消耗趋势 | 1d | 前端 | M6-1.2 |
| 4.4 | 跨模块执行追踪：execution 详情页下钻时间线（模型调用 → 工具调用 → 审核 → 配额消费；chat 与 workflow 两种来源统一视图） | 1.5d | 前端 | - |
| 4.5 | 后端补充：执行追踪聚合 API（按 execution_id 汇总各模块日志/事件） | 1d | 后端 | - |
| 4.6 | E2E 验证：触发调用 → /metrics 可见 → Grafana 出数 | 0.5d | 全员 | 4.1 |

**交付物：** Grafana 面板、性能画像与使用统计页面、执行追踪下钻。

### 5.4 备选（不阻塞验收，视进度）

- OpenTelemetry SDK 接入（trace 导出至 OTLP）→ 若本阶段有余力做评估 spike，默认顺延 Phase 3。

---

## 6. M8: 体验优化（第 5-7 周）

### 6.1 目标

围绕"降低使用门槛 + 批量运维效率"打磨体验：编辑器可逆可导、模板一键起步、批量操作、分组与配额治理、MCP 版本化、只读角色。

### 6.2 详细任务

#### Week 5: 编辑器打磨（前端）+ 模板后端（后端）

| 序号 | 任务 | 工时 | 负责人 | 前置条件 |
|------|------|------|--------|----------|
| 5.1 | 撤销/重做（JSON 快照栈，限深 50；Ctrl+Z / Ctrl+Shift+Z） | 1.5d | 前端 | - |
| 5.2 | 自动保存（3s 防抖 + 保存状态指示 + 并发编辑冲突提示） | 1d | 前端 | - |
| 5.3 | JSON 导入/导出 + 校验增强（环检测/孤儿节点/必填字段，错误高亮定位到节点） | 1.5d | 前端 | - |
| 5.4 | 节点配置表单细节（必填校验、MCP 工具参数按 schema 自动生成表单、类型提示） | 1d | 前端 | - |
| 5.5 | 模板后端：`resource_templates` 表 (kind=workflow/agent, builtin 标记, 占位符变量) + CRUD API + 内置预置（工作流 3 个：定时数据汇总、MCP 巡检告警、报告生成；Agent 3 个：FAQ 客服、工具执行器、数据分析） | 1.5d | 后端 | - |

**交付物：** 编辑器 v2（撤销/自动保存/导入导出/校验）、模板 API + 6 个内置模板。

#### Week 6: 模板市场 UI（前端）+ 批量操作 + Agent 分组（前后端）

| 序号 | 任务 | 工时 | 负责人 | 前置条件 |
|------|------|------|--------|----------|
| 6.1 | 模板市场页面：浏览/搜索/预览、一键创建（占位符变量表单填充）、"存为模板"（编辑器内） | 1.5d | 前端 | 5.5 |
| 6.2 | 批量操作：Agent 批量启动/停止（结果汇总：成功/失败/原因） | 1d | 全员 | - |
| 6.3 | 批量操作：MCP 批量连通性检查、工作流批量删除（二次确认 + 有活跃调度的项自动跳过并提示） | 1d | 全员 | - |
| 6.4 | Agent 分组：`group` 字段（项目/团队/环境标签）+ 列表筛选/分组视图 + 看板按组过滤 | 1.5d | 全员 | - |
| 6.5 | E2E 补充：模板一键创建、批量操作主流程 | 0.5d | 前端 | 6.1-6.4 |

**交付物：** 模板市场、三类批量操作、Agent 分组能力。

#### Week 7: Agent 配额 + MCP 版本管理 + 观察者角色 + UI 细节

| 序号 | 任务 | 工时 | 负责人 | 前置条件 |
|------|------|------|--------|----------|
| 7.1 | Agent 配额后端：并发上限（Redis 信号量）+ 每日调用上限（Redis 计数，UTC+8 日切） | 1.5d | 后端 | - |
| 7.2 | 配额命中处理：429 + Retry-After + 运行日志记录 + 接入 M6 告警规则（配额使用率复用 4.4 模型配额规则模式） | 0.5d | 后端 | 7.1, M6 |
| 7.3 | MCP 版本管理：配置变更产生版本快照（endpoint/凭证引用/工具集）+ 版本历史 API + 回滚（回滚后即时重检 + 工具快照 diff 提示） | 1.5d | 后端 | - |
| 7.4 | 观察者角色：内置 viewer（仅 `*:read`）+ 前端隐藏写操作入口 + 后端写接口 403 | 1d | 全员 | - |
| 7.5 | UI 细节：全局搜索（Agent/MCP/模型/工作流按名称）、空态/错误态统一组件、破坏性操作二次确认规范落地 | 1.5d | 前端 | - |
| 7.6 | E2E 补充：配额 429、MCP 回滚、viewer 只读 | 0.5d | 前端 | 7.1-7.5 |

**交付物：** Agent 配额、MCP 版本管理、viewer 角色、UI 一致性改进。

---

## 7. H1: 加固与验收（第 8 周）

### 7.1 详细任务

| 序号 | 任务 | 工时 | 负责人 | 前置条件 |
|------|------|------|--------|----------|
| 8.1 | 限流：API Key / IP 粒度令牌桶（Redis，默认关闭可配置，429 标准响应） | 1d | 后端 | - |
| 8.2 | 全局审计日志：`audit_logs` 表（用户/时间/动作/资源/变更摘要）+ 中间件自动记录写操作 + 系统设置页审计日志 UI（检索/导出） | 1.5d | 全员 | - |
| 8.3 | 安全检查：密码策略（长度/复杂度）、API Key 到期提醒（临期 7 天告警）、敏感字段脱敏审查（凭证/密钥/token） | 1d | 后端 | - |
| 8.4 | E2E 全量回归：M6–M8 新流程 + Phase 1 主链路回归 | 1d | 全员 | - |
| 8.5 | 压测基线：/invoke 与工作流执行并发场景（目标：P95 延迟与错误率记录基线值），瓶颈调优 | 1d | 后端 | - |
| 8.6 | 文档：用户手册更新（告警/监控/模板/批量操作章节）、运维手册（部署/监控接入/告警配置/值班 runbook）、swaggo API 文档刷新 | 1d | 全员 | - |
| 8.7 | Phase 2 评审 + 总结文档（各里程碑 implementation-summary）+ Phase 3 启动规划 | 0.5d | 全员 | 8.1-8.6 |

**交付物：** 生产加固完成、E2E/压测报告、完整文档集、Phase 2 总结。

---

## 8. 新增 API 汇总（相对 Phase 1）

```
# 告警 (M6)
GET/POST/PUT/DELETE  /api/v1/alerts/rules[/:id]
GET    /api/v1/alerts/events          POST /api/v1/alerts/events/:id/ack
GET/POST/PUT/DELETE  /api/v1/alerts/channels[/:id]
GET/POST/DELETE      /api/v1/alerts/silences[/:id]
GET    /api/v1/alerts/summary

# 性能画像与追踪 (M7)
GET    /api/v1/agents/:id/performance          # P50/P95/P99/错误率/token 趋势
GET    /api/v1/models/usage-stats              # 按模型用量统计
GET    /api/v1/executions/:executionId/trace   # 跨模块执行追踪时间线

# 模板 (M8)
GET    /api/v1/templates                       # 模板列表 (kind/q, 分页)
GET    /api/v1/templates/:id
POST   /api/v1/templates                       # 存为模板 (from_resource_id)
POST   /api/v1/templates/:id/instantiate       # 一键创建 (variables 填充)
DELETE /api/v1/templates/:id                   # 内置模板禁删

# 批量操作与分组 (M8)
POST   /api/v1/agents/batch/start              # {"agent_ids"}
POST   /api/v1/agents/batch/stop
POST   /api/v1/mcp-servers/batch/test
POST   /api/v1/workflows/batch/delete

# Agent 配额 (M8)
GET    /api/v1/agents/:id/quota                # 当前并发/今日用量/上限
PUT    /api/v1/agents/:id/quota                # {"max_concurrency","daily_call_limit"}

# MCP 版本 (M8)
GET    /api/v1/mcp-servers/:id/versions
POST   /api/v1/mcp-servers/:id/rollback        # {"version": N}

# 审计 (H1)
GET    /api/v1/audit-logs                      # 检索 + 分页 (可导出 CSV)
```

---

## 9. 里程碑评审标准

| 里程碑 | 验收标准 |
|--------|----------|
| **M6** | ✅ 告警规则可创建/编辑/启停，创建时试算当前值 ✅ 7 条内置规则开箱可用 ✅ 触发→webhook/邮件/钉钉通知可达（含签名与重试）✅ 恢复通知正常 ✅ 静默期间不发送 ✅ 事件历史可按状态/级别/目标检索并确认 ✅ 概览页告警摘要与顶栏徽标 |
| **M7** | ✅ /metrics 全量指标可抓取，label 无爆炸 ✅ Grafana 面板在 dev 环境出数 ✅ Agent P50/P95/P99 与错误率趋势页面 ✅ 模型使用统计页 ✅ execution 时间线下钻（模型/工具/审核/配额） |
| **M8** | ✅ 撤销/重做、自动保存可用 ✅ JSON 导入导出 + 校验错误定位到节点 ✅ 模板一键创建（工作流 + Agent）✅ 批量启停/检查/删除有结果汇总 ✅ Agent 分组筛选与分组视图 ✅ 并发/日调用配额生效并返回 429+Retry-After ✅ MCP 版本历史与回滚（回滚后重检）✅ viewer 只读全链路 403 ✅ 全局搜索 |
| **H1** | ✅ 限流开关可配且 429 标准 ✅ 写操作全部入审计日志并可检索 ✅ E2E 回归通过 ✅ 压测基线报告 ✅ 用户手册/运维手册发布 ✅ Phase 2 总结文档 |

---

## 10. 风险与应对

| 风险 | 概率 | 影响 | 应对措施 |
|------|------|------|----------|
| 告警误报 / 告警风暴 | 中 | 中 | 事件聚合去重（同规则同目标合并）+ 恢复通知 + 静默机制 + 预置阈值经 E2E 校准 |
| rollup 表数据增长 | 低 | 中 | 小时粒度 + 30 天保留 + 定时清理；画像查询走 (dim_type, dim_id, hour) 复合索引 |
| 编辑器撤销/重做状态不一致 | 中 | 中 | JSON 快照栈（限深 50）而非操作栈；恢复前跑 DAG 校验，非法状态拒绝并提示 |
| 模板漂移（源实体后续修改） | 低 | 低 | 模板为创建时只读快照，实例独立演化；模板带版本号 |
| 限流误伤正常高并发调用 | 中 | 中 | 默认关闭、按 API Key/Agent 粒度可配、429 携带 Retry-After 与原因；先灰度单一 Agent 验证 |
| /metrics 性能开销 | 低 | 低 | histogram bucket 收敛（10ms–30s）、route 模板化；压测阶段复核 |
| MCP 回滚后与服务端实际工具不一致 | 中 | 中 | 回滚触发即时连通性重检 + 工具快照 diff 高亮提示 |
| M8 前端工作量集中 | 中 | 中 | W5–W7 前端分工：编辑器 1 人 / 列表与模板市场 1 人；每里程碑末 Demo 对齐 |

---

## 11. Phase 3 展望（本阶段不开发，仅预留接口）

| 方向 | 内容 | Phase 2 预留 |
|------|------|--------------|
| 多租户 | 租户隔离、独立配额与计费 | 数据模型保持 tenant_id 扩展位；审计日志含操作主体字段 |
| 生态集成 | LangChain / LlamaIndex / Dify 对接 | Agent 注册契约（API Key + /invoke）保持标准化 |
| 插件体系 | 自定义工作流节点类型、导出格式 | 节点执行器接口（M5 引擎）保持注册可扩展 |
| AI 辅助 | LLM 自动编排建议、智能故障诊断 | 执行追踪 (M7-4.4) 提供诊断数据基础 |
| 全链路追踪 | OpenTelemetry trace | M7-5.4 视进度做 spike |

---

## 12. 协作机制

- **每日站会（15 分钟）：** 昨日进展 / 今日计划 / 阻塞项
- **周报（每周五）：** 本周完成 / 下周计划 / 风险与依赖 / Demo
- **里程碑评审：** M6/M7/M8/H1 结束各一次，对照 §9 验收标准逐项确认，未达标项明确补救计划
- **文档纪律：** 每个里程碑交付后在 `docs/phase2/` 补 `Mx-implementation-summary.md`（沿用 Phase 1 格式）

---

*文档维护：本文档随 Phase 2 推进持续更新；范围变更需在周会评审后修订本文档并升版本。*