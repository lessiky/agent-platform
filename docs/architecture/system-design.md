# 系统架构设计

> **版本：** v1.0
> **日期：** 2026-08-17
> **维护者：** 架构组

---

## 1. 架构概述

Agent 管理平台采用前后端分离加微服务架构。前端通过 API Gateway 与后端服务通信，后端按业务域拆分为独立服务，共享底层基础设施。

### 1.1 架构总览

`
客户端 (Web UI / CLI / Webhook)
         |
         v
+---------------------------------------------+
|               API Gateway                   |
|              (Nginx / Kong)                 |
|   - 路由分发  - 认证转发  - 限流            |
+---------------------+-----------------------+
                      |
        +-------------+-------------+
        |             |             |
        v             v             v
  +----------+ +----------+ +----------+
  |  Auth Svc| | Agent Svc| |  MCP Svc |
  |          | |          | |          |
  +-----+----+ +----+-----+ +----+-----+
        |            |            |
        +-------------+------------+
                      |
              +-------v--------+
              | Workflow Svc   |
              +-------+--------+
                      |
      +---------------+
      |               |
      v               v
+-----------+   +----------+
| PostgreSQL|   |RabbitMQ  |
|           |   |          |
+-----------+   +----------+
`

### 1.2 服务清单

| 服务 | 语言 | 职责 | 端口 |
|------|------|------|------|
| api-gateway | Nginx | 路由、限流、CORS | 80/443 |
| auth-service | Go (Gin) | 用户认证、RBAC、Token | 8081 |
| agent-service | Go (Gin) | Agent 生命周期、状态监控 | 8082 |
| mcp-service | Go (Gin) | MCP 服务器管理、工具发现 | 8083 |
| workflow-service | Go (Gin) | 工作流编排、DAG 执行、调度 | 8084 |
| model-service | Go (Gin) | 模型管理、配额控制、路由选择 | 8086 |
| audit-service | Go (Gin) | 审计日志记录、查询 | 8085 |

---

## 2. 数据架构

### 2.1 数据库选型

| 数据类型 | 数据库 | 理由 |
|----------|--------|------|
| 关系型数据 (用户、Agent、MCP、工作流、模型) | PostgreSQL 16 | 支持 JSONB、事务完整 |
| 事件总线与工作流队列 | RabbitMQ | 成熟稳定、支持消息持久化 |

### 2.2 数据库分库策略

Phase 1 采用单库多 Schema 策略：

`
PostgreSQL
|-- agent_platform
    |-- public              # 公共数据 (用户、角色、权限)
    |-- agent               # Agent 相关数据
    |-- mcp                 # MCP 相关数据
    |-- workflow            # 工作流相关数据
    |-- model               # 模型相关数据
`

### 2.3 数据一致性策略

| 场景 | 策略 | 说明 |
|------|------|------|
| 用户/权限数据 | 强一致性 (本地事务) | 同一服务内，数据量小 |
| Agent/MCP 状态 | 最终一致性 | 缓存加定时同步 |
| 工作流状态 | 事件溯源 | 所有状态变更记录为事件，可回溯 |
| 模型配置 | 强一致性 (本地事务) | 模型选择需要实时一致性 |

---

## 3. 认证与授权

### 3.1 认证流程

`
浏览器 --> API Gateway --> Auth 服务
   |            |             |
   | POST       |             |
   | /login     |             |
   |            |             |
   | <- JWT     |             |
   | Bearer     |             |
   | Token      |             |
   |------------|             |
                   | 验证
                   v
              业务服务
`

### 3.2 RBAC 权限模型

`
用户 -- N:M --> 角色 -- N:M --> 权限

全局权限:
  - system:users:read/write
  - system:audit:read
  - system:config:write
  - model:templates:read/write/delete

资源级权限 (按团队隔离):
  - agent:{team_id}:read/write/delete
  - mcp:{team_id}:read/write
  - workflow:{team_id}:read/write/execute
  - model:{team_id}:read/write/configure
`

---

## 4. 工作流引擎架构

### 4.1 执行模型

`
DAG 定义
      |
      v
+-----------+
| DAG 解析器| -- 校验拓扑、检测循环
+-----+-----+
      |
      v
+--------------+
| 拓扑排序     | -- 生成执行顺序
| Topological  |
| Sort         |
+------+-------+
       |
       v
+------------------+
|  执行引擎        | -- 按序执行节点
|                  | -- 传递变量
|  - Agent 节点    |
|  - MCP 工具节点  |
|  - HTTP 节点     |
|  - 条件分支      |
|  - 并行分支      |
+------------------+
`

### 4.2 节点执行流

| 节点类型 | 执行流程 |
|----------|----------|
| Agent 节点 | 接收输入 -> 调用 Agent Runtime -> 返回响应 |
| MCP 工具节点 | 接收输入 -> 参数映射 (JSONPath) -> 调用 MCP 服务 -> 返回结果 |
| HTTP 节点 | 接收输入 -> 构造 HTTP 请求 -> 调用外部 API -> 返回响应 |
| 条件分支 | 评估变量条件 -> 选择 Yes/No 分支继续执行 |
| 并行分支 | 同时执行多个子节点 -> 等待全部完成 -> 合并结果 |

### 4.3 状态转换

`
Draft --[激活]--> Active --[触发]--> Running
                                          |
                   +----------------------+-------------------+
                   v                       v                     v
              Success               Failed                 Cancelled
                                         |
                                   [重试] -+
`

---

## 5. 安全架构

### 5.1 纵深防御策略

| 层级 | 措施 |
|------|------|
| 传输安全 | 全链路 HTTPS/TLS 1.3、HSTS、证书自动轮换 |
| 认证授权 | JWT + Refresh Token、RBAC 细粒度控制、API Key 隔离 |
| 数据安全 | 敏感凭证 AES-256-GCM 加密、数据库连接 TLS、审计日志 WORM |
| 运行安全 | 输入验证、请求限流、容器非 root 运行、依赖漏洞扫描 |

### 5.2 敏感数据保护

| 数据类型 | 保护方式 |
|----------|----------|
| 用户密码 | bcrypt (cost=12) |
| MCP 凭证 | AES-256-GCM (密钥由 KMS 管理) |
| Agent API Key | HMAC-SHA256 摘要存储 |
| 模型 API Key | AES-256-GCM 加密存储 |
| 审计日志 | WORM + SHA-256 完整性链 |

---

## 6. 部署架构

### 6.1 Phase 1 部署 (Docker Compose)

`
Docker Host
  +-- Frontend (Nginx:80)
  +-- API Gateway (Nginx:8080)
  +-- Backend Services (Go:8081-8086)
  +-- PostgreSQL (5432)
  +-- RabbitMQ (5672, 15672)
`

### 6.2 Phase 2+ 部署 (Kubernetes)

`
K8s 集群
  +-- Ingress (NGINX)
  +-- HPA (自动扩缩容)
      +-- Frontend (3 replicas)
      +-- Auth Service (2 replicas)
      +-- Agent Service (2-5 replicas)
      +-- Workflow Service (3-10 replicas, 按队列扩缩)
      +-- Model Service (2 replicas)
  +-- StatefulSets
      +-- PostgreSQL (HA)
      +-- RabbitMQ (Cluster)
`

### 6.3 CI/CD 流水线

`
代码提交
    |
    v
[代码检查] --> [单元测试] --> [集成测试] --> [部署预发环境]
                                                               |
                                                          [人工审批]
                                                               |
                                                               v
                                                          [部署生产]
`

---

## 7. 可观测性架构

### 7.1 监控指标

| 层级 | 指标 | 采集方式 |
|------|------|----------|
| 服务 | QPS、P50/P95/P99 延迟、错误率 | Prometheus |
| 数据库 | 连接池、慢查询、锁等待 | pg_stat_statements |
| 缓存 | 命中率、内存使用 | - |
| MQ | 队列长度、消费延迟、死信数 | RabbitMQ Management |
| 模型服务 | Token 消耗、API 调用次数、延迟 | Model Service Metrics |

### 7.2 日志规范 (JSON 格式)

`json
{
  "timestamp": "2026-08-17T10:30:00Z",
  "level": "info",
  "service": "agent-service",
  "trace_id": "abc123",
  "span_id": "def456",
  "method": "POST",
  "path": "/api/v1/agents",
  "status": 201,
  "duration_ms": 45,
  "user_id": "user-001",
  "msg": "Agent created successfully"
}
`

### 7.3 分布式追踪

`
浏览器 --> API Gateway --> Agent 服务 --> MCP 服务
   |              |               |              |
   |          trace_id         trace_id      trace_id
   |          span_id          parent_id     child_id
   |
OpenTelemetry Collector
   |
   v
Jaeger / Tempo
   |
   v
Grafana 仪表板
`

---

## 8. 扩展性与性能策略

### 8.1 水平扩展

| 组件 | 扩缩容策略 |
|------|------------|
| 后端服务 | 无状态设计，按 CPU/内存 HPA |
| 工作流执行器 | 按 RabbitMQ 队列长度动态扩缩 |
| Agent 运行时 | 按需创建，空闲自动回收 |
| 模型服务 | 按 API 调用量动态扩缩 |

### 8.2 缓存策略 (Cache-Aside)

`
读:
  1. 查缓存
  2. 未命中 -> 查 PostgreSQL
  3. 写入缓存 (TTL 5 分钟)

写:
  1. 写 PostgreSQL
  2. 删除缓存
  3. 下次读时重建
`

---

## 9. 模型管理服务架构

### 9.1 服务职责

模型管理服务负责 LLM 模型的集中管理，包括模型模板配置、参数管理、配额控制、路由选择等核心能力。

`
                    +-----------------+
                    |  Model Service |
                    +-----------------+
                           |
      +--------------------+--------------------+
      |                    |                    |
      v                    v                    v
+-------------+     +-------------+     +-------------+
| 模板管理    |     | 配额管理    |     | 路由选择    |
|             |     |             |     |             |
| 模型列表    |     | 调用限额    |     | 优先级路由  |
| 参数配置    |     | Token 统计  |     | 故障转移    |
| 版本控制    |     | 配额告警    |     | 负载均衡    |
+-------------+     +-------------+     +-------------+
`

### 9.2 模型路由机制

`
Agent 请求
    |
    v
+------------------+
| 路由选择器       |
|                  |
| 1. 按模型名称    |
| 2. 按优先级      |
| 3. 按负载状态    |
+--------+---------+
         |
         v
+------------------+
| 配额检查         |
|                  |
| - 团队配额       |
| - 模型配额       |
| - 速率限制       |
+--------+---------+
         |
         v
+------------------+
| 模型调用         |
|                  |
| - OpenAI API     |
| - Anthropic API  |
| - 自定义后端     |
+------------------+
`

---

*文档维护：架构变更需经架构评审后更新本文档。*