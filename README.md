# Agent 管理平台

AI Agent 统一管理平台：支持 Agent 创建与运行管理、模型路由与配额、MCP 工具注册与人工审核、技能包能力扩展、DAG 工作流编排。

## 模块总览

| 模块       | 说明                                  | 界面入口          | 所需权限点                                                   |
| -------- | ----------------------------------- | ------------- | ------------------------------------------------------- |
| 概览       | 全平台资源与执行状态                          | 概览            | 任意已登录用户                                                 |
| Agent 管理 | Agent CRUD、实例启停、版本回滚、API Key、统计与日志  | Agent 管理      | `agent:read` / `agent:write`                            |
| Agent 对话 | 多轮对话、对话内工具调用（联动人工审核）、执行元数据          | Agent 详情 → 对话 | `agent:write`                                           |
| 技能管理     | 技能包导入 / 预览 / 删除、Agent 关联、运行时注入      | 技能管理          | `skill:read` / `skill:write`                            |
| MCP 管理   | MCP 注册、工具发现、健康监控、工具审核开关、Agent 绑定    | MCP 管理        | `mcp:read` / `mcp:write`                                |
| 模型管理     | 模型模板 CRUD、连通性检测、配额、优先级路由            | 模型管理          | `model:read` / `model:write`                            |
| 审核中心     | MCP 工具调用人工审核（通过 / 驳回 / 超时策略）        | 审核中心          | 查看 `mcp:read`，通过 / 驳回 `mcp:approve`                     |
| 工作流      | DAG 编排、执行引擎、Cron 定时、Webhook 触发、执行追踪 | 工作流管理         | `workflow:read` / `workflow:write` / `workflow:execute` |
| 系统管理     | 用户 / 角色 / 权限管理, 平台设置 (平台名 / 图标)     | 系统管理          | `user:manage` / `role:manage` / `platform:manage`       |

## 模块状态

- M1 基础框架: 认证 (JWT)、RBAC 权限 (真实查询 user_roles/role_permissions, 15 权限点 / admin·operator·user 角色 + 用户/角色管理 API 与 UI, 详见 docs/phase1/RBAC-implementation-summary.md)、统一响应/错误
- M2 Agent 管理: Agent CRUD (模型下拉选择, MCP 绑定 + 可用工具自动校验)、实例启停、版本回滚、API Key (外部调用入口 /invoke)、运行日志、调用统计、状态看板 + 前端页面 (详见 docs/phase1/M2-implementation-summary.md)
- M2.5 Agent 对话与系统提示词: 多轮对话 (会话持久化, 最近 10 条上下文)、模型调用 (OpenAI 兼容, M4 路由故障转移 + 配额)、对话内工具调用 (白名单 + M4.5 审核门禁, 轮数可配)、执行元数据 (execution_id / tokens / 耗时) + 前端对话面板 (详见 docs/phase1/M2.5-implementation-summary.md)
- M3 MCP 管理: MCP 注册、工具发现、凭证加密 (AES-256-GCM)、健康监控、Agent 绑定调用 + 前端页面 (详见 docs/phase1/M3-implementation-summary.md)
- M4 模型管理: 模型模板 CRUD、连通性测试、配额管理 (调用次数 + token 双维度)、优先级路由 (超配额自动切换) + 前端页面 (详见 docs/phase1/M4-implementation-summary.md)
- M4.5 MCP 工具人工审核: 工具级审核开关、审核请求生命周期 (通过/驳回/超时)、审核中心 + 审计日志 + 前端页面 (详见 docs/phase1/M4.5-implementation-summary.md)
- M5 工作流: DAG 编排 (agent/mcp_tool/http/delay/condition 五类节点 + 条件分支)、执行引擎 (变量传递/节点级重试/超时/取消)、Cron 定时调度、Webhook 触发、MCP 节点人工审核挂起/恢复 (联动 M4.5)、执行追踪看板 + 前端可视化编辑器 (详见 docs/phase1/M5-implementation-summary.md)
- M9 技能管理: 技能包导入 (zip 校验 / 防 zip-slip / 同名升级)、文件预览下载、删除拦截、Agent 关联 (required_tools 依赖校验) + 运行时注入 (渐进式披露 / 全量注入双模式, load_skill 内置工具)、使用追溯 + 前端页面 (详见 docs/phase1/M9-development-plan.md)

## 前提条件

- Docker + Docker Compose v2 (Docker 部署必需; 本地开发启动依赖服务也使用它)
- Go 1.21+ (仅本地开发需要, 如未安装: `winget install GoLang.Go`)
- Node.js 18+ (仅本地开发需要, 前端)
- PostgreSQL 16+ (仅本地开发需要, Docker 部署使用内置容器)

## 快速开始（非Docker部署）

### 1. 启动依赖服务（或单独部署postgres数据库）

```powershell
docker compose -f infra/docker-compose.yml up -d postgres
```

- 本地端口映射: PostgreSQL `15432` (见 `infra/docker-compose.yml`, `backend/.env` 已按此配置)
- 注意: 裸 `up -d` 会连同 backend/frontend 一起启动 (全容器化), 见下方「Docker 部署」

### 2. 启动后端

```powershell
cd backend
mv .env.example .env   # 首次
go mod tidy            # 首次
go run ./cmd/server
```

API 将在 http://localhost:8080 启动。

- 运行日志同时输出到控制台与项目根 logs/backend-YYYY-MM-DD.log (按日自动切割, 可用 LOG_DIR 环境变量自定义目录)
- `backend/.env` 需配置 `MCP_CREDENTIALS_KEY` 与 `MODEL_CREDENTIALS_KEY` (均为 64 位 hex, 生成: `openssl rand -hex 32`)，缺失会启动失败

### 3. 启动前端

```powershell
cd frontend
npm install            # 首次
npm run dev            # http://127.0.0.1:8090
```

- dev 模式通过 vite proxy 将 `/api` 转发到 `http://localhost:8080`
- dev 端口固定在 8090 (见 `vite.config.ts`)
- 生产构建: `npm run build` → `dist/`
- dev 服务输出同时写入项目根 logs/frontend-YYYY-MM-DD.log (按日自动切割, 可用 LOG_DIR 环境变量自定义目录)

### 4. (可选) 启动 mock 服务器

用于本地验证 M3/M4/M2.5（对话与工具调用链路）：

```powershell
cd backend
MOCK_MCP_PORT=9100 MOCK_MCP_API_KEY=mock-mcp-key-123 go run ./tools/mock-mcp-server
MOCK_MODEL_PORT=9101 MOCK_MODEL_API_KEY=mock-model-key-123 go run ./tools/mock-model-server
```

## 快速开始（Docker 部署）

全部服务 (PostgreSQL / 后端 / 前端) 容器化运行。前端由 nginx 托管: 静态资源直接下发, `/api` 反向代理到后端容器 (前端无需跨域配置, 与 dev 的 vite proxy 行为一致)。

### 一键启动

```powershell
make up
# 或
docker compose -f infra/docker-compose.yml up -d --build
```

- 访问入口: 前端 http://localhost:8081 (后端 API 也可直连 http://localhost:8080)
- 启动顺序由健康检查保证: postgres 就绪 → 后端 `/healthz` 通过 → 前端
- 首次启动自动迁移表结构并初始化 RBAC 角色权限; 打开前端页面注册账号即可 (首个注册用户自动成为 admin, 见「模块操作说明 - 登录与注册」)

### 相关文件

| 文件                         | 说明                                                                              |
| -------------------------- | ------------------------------------------------------------------------------- |
| `backend/Dockerfile`       | Go 多阶段构建 (golang:1.25-alpine → alpine:3.21), 容器内 Go 依赖走 goproxy.cn              |
| `frontend/Dockerfile`      | node:20-alpine 构建 (tsc + vite) → nginx:1.27-alpine 托管                           |
| `frontend/nginx.conf`      | SPA history 回退、`/api` 反代、gzip、静态资源长缓存、上传大小上限 20MB (覆盖技能包 10MB 限制)、LLM 长读超时 300s |
| `infra/docker-compose.yml` | 三个服务 + 健康检查 + 数据卷 (postgres_data / backend_logs)                                |

### 端口与参数

均可通过环境变量覆盖, 无需改 compose (示例: `BACKEND_PORT=9000 FRONTEND_PORT=9001 make up`):

| 变量                      | 默认值        | 说明                         |
| ----------------------- | ---------- | -------------------------- |
| `FRONTEND_PORT`         | `8081`     | 前端 (nginx) 宿主机端口           |
| `BACKEND_PORT`          | `8080`     | 后端宿主机端口                    |
| `POSTGRES_PASSWORD`     | `postgres` | PostgreSQL 密码              |
| `JWT_SECRET`            | 内置默认值      | JWT 签名密钥, **生产部署务必更换**     |
| `MCP_CREDENTIALS_KEY`   | 内置默认值      | MCP 凭证加密密钥 (64 位 hex)      |
| `MODEL_CREDENTIALS_KEY` | 内置默认值      | 模型 API Key 加密密钥 (64 位 hex) |

- PostgreSQL `15432` 的宿主机端口映射保留给本地开发用, 纯容器部署时可从 compose 中移除
- 数据持久化: `postgres_data` 卷 (数据库), `backend_logs` 卷 (后端日志)

### 常用命令

```powershell
make up                # 构建并启动全部容器
make down              # 停止全部容器
docker compose -f infra/docker-compose.yml logs -f backend    # 查看后端日志
docker compose -f infra/docker-compose.yml up -d --build backend  # 单独重建某服务
```

## 模块操作说明

### 1. 登录与注册

- 打开 http://127.0.0.1:8090，登录页含「登录」与「注册」两个页签；注册需填写用户名、邮箱、密码。
- **平台首个注册用户自动获得 admin 角色**；之后注册的用户默认分配 `user` 角色（只读），需管理员在「用户管理」中分配角色。日后若管理员被全部禁用，下一个注册用户会自动接任 admin。
- 内置角色与权限：

| 角色         | 说明   | 权限                                                                                |
| ---------- | ---- | --------------------------------------------------------------------------------- |
| `admin`    | 管理员  | 全部 15 个权限点（含 `mcp:approve` / `user:manage` / `role:manage` / `platform:manage`）   |
| `operator` | 运营   | 业务读写（除 `mcp:approve` / `user:manage` / `role:manage` / `platform:manage` 外的 11 个） |
| `user`     | 默认角色 | 只读（`agent:read` / `mcp:read` / `model:read` / `workflow:read` / `skill:read`）     |

- 用户被停用或删除后，其存量 JWT 立即失效。
- 菜单与按钮按当前用户权限动态显示；无权限的页面直接访问返回 403。

### 2. 概览看板

- 「概览」页两个页签：

    - **基本情况**：Agent / MCP / 模型 / 技能 / 工作流 / 审批的总数与健康计数；「运行中的 Agent」列表每 5 秒自动刷新。
    - **工作流看板**：工作流执行状态计数 + 最近 10 条执行记录，点击可跳转执行追踪。

### 3. Agent 管理

入口：Agent 管理（列表页支持名称搜索、状态过滤）。

**创建 / 编辑 Agent**（列表页「新建 Agent」或操作列编辑按钮）

- 名称（全局唯一）、描述
- 模型：从「模型管理」的模型模板下拉选择（Agent 运行时首选模型，故障/超配额时按 M4 路由转移）
- 绑定 MCP 服务器：多选；「可用工具」白名单自动列出所绑定 MCP 的已发现工具，可勾选过滤（留空 = 全部绑定工具可用；未勾选的工具运行时会被跳过）
- 关联技能 + 技能注入模式：见「5. 技能管理」
- 系统提示词：随每次对话发送给模型（纳入版本快照，回滚时一并恢复）
- 生成参数：Temperature (0-2) / 最大 Token 数 / 工具调用轮数上限（留空 = 默认 5）

**实例生命周期**

- 列表操作列或详情页「启动 / 停止」按钮：启动 = 上线（可接受/invoke 调用）；默认不产生常驻背景流量（`simulate_traffic` 默认关闭，开启后日志标注 `simulated`）
- **运行中禁止更新 / 删除 / 回滚**（返回 400，需先停止实例）

**详情页页签**（Agent 详情）

| 页签      | 说明                                                          |
| ------- | ----------------------------------------------------------- |
| 配置      | 基本信息、模型、系统提示词、实例状态                                          |
| 绑定 MCP  | 已绑定 MCP 服务器、连接状态、已发现工具                                      |
| 关联技能    | 已关联技能包、版本、注入模式                                              |
| 对话      | 见下方「4. Agent 对话」                                            |
| 版本历史    | 每次更新 / 回滚生成版本快照；「回滚」到任意历史版本（配置与系统提示词一并恢复，回滚本身也产生新版本）        |
| API Key | 创建（明文 `akp_...` 仅返回一次，可选过期时间）/ 列表（仅前缀）/ 吊销 / 删除             |
| 调用统计    | 选定时间范围内的调用量与成功情况                                            |
| 日志      | 运行日志；「日志」独立页支持 level / 关键字 / 起始时间过滤（日志表按 Agent 保留最近 5000 条） |

### 4. Agent 对话

入口：Agent 详情 →「对话」页签。

- 左侧会话列表：「新建会话」/ 切换 / 删除会话；会话标题取首条消息。
- 发送消息：系统提示词 + 最近 10 条历史消息 + 当前消息送入首选模型；应答气泡内展示执行元数据（execution_id、模型名、tokens、耗时）。
- 模型发起工具调用时：工具结果回传模型继续生成；消息中展示工具状态 Tag（ok=绿 / pending=橙 / error=红）；白名单外工具不执行（记 skipped）。
- 调用「需审核」工具时：不立即执行，生成审核请求（来源=chat），对话照常返回应答并显示橙色横幅「N 个工具调用待人工审核」→ 点击前往审核中心；审核通过后工具结果自动回填。
- 会话按用户隔离，访问他人会话返回 404。

### 5. 技能管理

入口：技能管理。

**导入技能包**（列表页「导入技能包」）

- 上传 zip（支持包根目录或单一顶层目录结构），必须包含 `SKILL.md`（frontmatter 需 name/description，正文非空）。
- 限制：单包 ≤10MB、≤500 个文件、单文件 ≤2MB；文件类型白名单；路径安全检查（防 zip-slip）。
- 同名冲突：默认报「已存在」；勾选「强制覆盖」后升级为 version+1（已关联的 Agent 保留）。
- 示例包：`backend/testdata/skills/weekly-report.zip`。

**技能列表 / 详情**

- 列表：名称 / 版本 / 状态（启用 / 停用）/ 关联 Agent 数；「停用」的技能运行时不注入。
- 详情：指令正文（SKILL.md 内容）、资源文件（查看与下载）、关联 Agent（跳转 Agent 详情）、使用统计（近 30 天加载次数、最近使用时间）。
- 删除：有关联 Agent 时拦截并列出关联清单；「强制删除」级联解绑。

**Agent 关联与运行时注入**

- 在 Agent 创建 / 编辑表单勾选「关联技能」；保存时校验技能 `required_tools` ⊆ Agent 可用工具集，不满足会返回缺失工具列表并拒绝关联。

- 两种注入模式（Agent 表单「技能注入模式」）：

    - `metadata_injection`（默认，渐进式披露）：系统提示词追加技能目录（名称 + 描述），模型按需调用内置工具 `load_skill(技能名)` 加载正文；
    - `full_injection`：全部技能正文直接注入系统提示词。

- 安全边界：平台对技能包只做存储与只读注入，不执行包内任何代码；正文按参考数据注入（带分隔符，防提示词注入）。

- 使用追溯：对话消息的执行元数据含 `skill_calls`（技能名 / 版本 / 模式 / 状态）。

### 6. MCP 管理

入口：MCP 管理。

**注册 MCP 服务器**（列表页「注册 MCP」）

- 字段：名称、传输类型、端点 (endpoint)、描述、标签、凭证（API Key 或自定义请求头）。
- 凭证以 AES-256-GCM 加密存储（密钥 `MCP_CREDENTIALS_KEY`），详情页仅展示脱敏值。
- 注册后立即执行一次连通性检测；之后按 `MCP_HEALTH_INTERVAL`（默认 1 分钟）周期检测，状态显示在列表与详情页。

**列表操作**

- 连通性测试（手动重检）/ 详情 / 编辑（端点或凭证变更自动触发重检）/ 删除（级联清理绑定与健康历史）。

**详情页**

- 健康概览：当前状态、最近检测、检测延迟、已发现工具数、健康历史。
- 已发现工具：工具清单；每个工具带「需审核」开关（增量更新，详见「8. 审核中心」）。
- Agent 绑定：绑定 / 解绑 Agent——访问控制，仅绑定的 Agent 运行时可调用该 MCP 的工具。
- 工具调用：也可通过 API `POST /api/v1/mcp-servers/:id/tools/call` 直接代理调用（需审核工具返回 202 + approval_id，见 API 端点）。

### 7. 模型管理

入口：模型管理。

**创建模型模板**（列表页「注册模型」）

- 字段：名称、提供商、模型名称、API 端点（留空 = 官方默认端点）、**路由优先级（数值越小优先级越高）**、标签、生成参数（temperature / max_tokens / top_p）、API Key（加密存储，详情脱敏）。
- 注册后立即执行连通性探测；之后按 `MODEL_HEALTH_INTERVAL`（默认 1 分钟）周期探测。

**配额管理**（详情页「配额与用量」页签）

- 日 / 月双维度：调用次数 + token 额度，0 = 不限；「保存配额」生效。
- 高优先级模型异常或配额耗尽时，路由自动切换到低优先级模型（故障转移），并记录切换原因。

**路由选择**（详情页「路由选择」页签）

- 「试运行路由选择」：按优先级选择首个可用模型（跳过异常状态与配额耗尽者），**不消耗配额**，用于验证路由结果。

**列表 / 详情**

- 按名称 / 提供商 / 状态 / 标签过滤；手动连通性测试；健康历史；最近调用日志（token 用量累计）。

### 8. 审核中心

入口：审核中心（查看需 `mcp:read`；通过 / 驳回需 `mcp:approve`，内置角色中仅 admin 拥有）。

- 页签：「待审核 (数量)」/「审核历史」；支持按状态 / MCP 服务器 / 工具 / Agent / 来源（直接调用 / 对话 / 工作流）/ 时间过滤。
- 点击请求查看详情：MCP 服务器、工具、参数快照、执行结果、超时时间、审核人与意见。
- **通过**：执行该工具调用并回填结果（来源为工作流时，恢复挂起的执行）；**驳回**：终止请求（工作流来源 → 该节点失败、下游级联跳过、执行失败）。
- 临近超时的请求标记「即将超时」。
- 「审核配置」：默认审核超时（分钟）与超时策略（超时自动驳回 / 超时自动通过）。
- 所有审核动作与 RBAC 变更统一写入审计日志。

### 9. 工作流管理

入口：工作流管理。

**创建工作流**

- 列表页「新建」在「新建工作流」对话框填写名称（描述可选）→ 进入可视化编辑器。

**DAG 可视化编辑器**

- 「添加节点」支持 5 类：`agent`（调用 Agent 对话）/ `mcp_tool`（调用 MCP 工具）/ `http`（外部 HTTP 请求）/ `delay`（延迟秒数）/ `condition`（条件分支）。
- 拖拽布局、连线组网；连线到 condition 节点时选择「是 / 否」出口。
- 选中节点 → 右侧「节点配置」面板填写该节点参数；每个节点支持重试（最大尝试次数 / 间隔 / 固定或指数退避）与超时（默认 300s）。
- 变量引用：`$inputs.<path>`（执行输入）、`$nodes.<节点id>.<字段>`（上游输出）、`$execution.id`；路径支持 `a.b[0].c`；JSON 串可用 `json(<引用>).<path>` 解析取 key（如 `json($nodes.n1.text).data.id`）。
- 「保存」执行 DAG 校验（环检测 / 节点 / 边合法性，上限 100 节点 / 500 边）并生成版本快照；「保存并激活」保存后直接进入可触发状态。
- 工具栏「定时调度」配置 cron 定时任务（详见下方「运行工作流」）；「触发」需工作流已激活。

**运行工作流**

- 详情页：「编排」（进入编辑器）/「触发」（可填写 JSON 输入）/「激活」（草稿 → 已激活）/「归档」（停止调度且不可触发）/ 删除（执行历史保留）。
- **定时任务（Cron 调度）**：在编辑器点「定时调度」按钮配置——启用开关 + Cron 表达式（5 段：分 时 日 月 周，时区 Asia/Shanghai，如 `*/5 * * * *`）+ 可选的 JSON 触发输入；保存后按 cron 自动触发，触发方式记为 `cron`，每次执行注入配置的固定输入。详情页「定时调度」项展示当前生效的 cron。
- 定时调度仅对**已激活**的工作流生效（草稿 / 已归档不调度）；后端启动时全量重建调度，之后每 5 分钟对账自愈（DB 中启用状态变更会被自动感知）。
- Webhook：创建时自动生成 32 位 hex token，`POST /api/v1/webhooks/workflows/<token>`（公开端点，token 鉴权）直接触发，请求 payload 作为执行输入；外部系统可仅凭 token 经 `GET .../webhooks/workflows/<token>/executions/<执行ID>` 轮询执行状态。
- 执行历史：按状态 / 触发方式（manual / cron / webhook）过滤，点击进入执行追踪。

**执行追踪**

- 节点级追踪：每个节点的状态 / 尝试次数 / 耗时 / 输入 / 输出 / 错误，点击节点查看详情。
- 运行中的执行可「取消执行」（运行中节点转 cancelled，未开始节点级联取消）。
- 节点调用「需审核」工具时：执行挂起为「等待审核」，在审核中心批准后自动恢复执行。

### 10. 系统管理

入口：系统管理 - 平台设置 / 用户管理 / 角色管理（分别需 `platform:manage` / `user:manage` / `role:manage`，菜单对无权限用户隐藏）。

**平台设置**

- 平台名称：1-64 个字符，默认「Agent 管理平台」；保存后登录页、侧边导航与浏览器标签页标题即时更新。
- 平台图标：PNG / JPG / SVG / WebP / GIF，大小 ≤ 1MB（以 base64 data URL 落库）；不设置时展示内置默认图标。
- 变更写入审计日志（`action=platform.update`，含名称前后值与图标是否变更）。

**用户管理**

- 新建用户：用户名 / 邮箱 / 密码 / 角色（角色留空 = 默认 `user` 角色）。
- 操作：编辑资料 / 重置密码、「分配角色」（全量替换）、启用 / 停用（停用后该用户无法通过 API 访问，存量 token 立即失效）、删除。
- 保护规则：不可删除自己；不可删除最后一个 admin。

**角色管理**

- 新建角色：名称 / 描述 / 按资源分组勾选权限点（15 个）。
- 编辑权限：全量替换；`admin` 角色强制保留 `user:manage` / `role:manage` / `mcp:approve`（防止把自己锁在管理界面外）。
- 删除：内置角色（admin / operator / user）不可删除；有用户绑定的角色不可删除。
- 权限变更即时生效（30s 权限缓存由变更事件主动失效）；全部用户 / 角色操作写入审计日志。

### 11. 外部集成调用

**Agent API Key 调用**（对外提供 Agent 能力的统一入口）

1. Agent 详情 →「API Key」页签创建 Key（明文 `akp_...` 仅展示一次，可设过期时间）；
2. 使用 Key 调用（不走用户 JWT）：

```bash
curl -X POST http://localhost:8080/api/v1/agents/<agent_id>/invoke \
  -H "Authorization: Bearer akp_xxx" \
  -H "Content-Type: application/json" \
  -d '{"message": "hello"}'
```

- 返回模型应答、tokens、latency 等执行信息（含 `session_id`：不传时自动新建外部会话，可用于多轮续聊）；存在待人工审核的工具调用时返回 `202`，`data.pending_approvals[].approval_id` 为审核请求 ID（对应工具**未执行**）。
- 审核结果查询（轮询闭环）：`GET /api/v1/agents/<agent_id>/invoke/approvals/<approval_id>`（使用同一 API Key，无需登录态）。建议间隔 2~5s 轮询，`status ∈ {approved, rejected, expired}` 为终态：`approved` 且 `result` 非空 = 工具已执行，`result` 即最终工具输出；`rejected` / `expired` = 工具未执行。响应中的 `continuation` 为决策后的模型续答（通过 = 基于工具结果的后续答复，拒绝 = 未执行说明），为空时续答轮未完成，继续轮询。
- Key 过期或被吊销后立即不可用；每次调用更新 `last_used_at`。

**工作流 Webhook 触发**

```bash
curl -X POST http://localhost:8080/api/v1/webhooks/workflows/<webhook_token> \
  -H "Content-Type: application/json" -d '{"note": "hi"}'
```

- 响应 `data` 为完整执行对象，`data.id` 即本次执行 ID；执行异步运行，返回时 `status` 通常为 `running`
- 状态跟踪（外部系统）：`GET /api/v1/webhooks/workflows/<webhook_token>/executions/<data.id>`——仅凭 webhook token 即可轮询（仅本工作流、返回状态视图，不含输入/输出 payload）
- 状态跟踪（平台内部）：`GET /api/v1/workflow-executions/<data.id>`（需用户 JWT，含节点级完整详情）

## 典型端到端流程

1. 按「快速开始」启动依赖、后端、前端（可选启动 mock MCP / mock Model 服务器）。
2. 注册第一个用户（自动 admin）并登录。
3. 模型管理 → 注册模型：创建模型模板（指向 mock 服务器或真实 LLM），连通性测试通过后设置配额。
4. MCP 管理 → 注册 MCP：注册 MCP 服务器，确认已发现工具；（可选）对敏感工具开启「需审核」。
5. Agent 管理 → 新建 Agent：选择模型 + 绑定 MCP + 配置工具白名单与系统提示词 → 启动实例。
6. Agent 详情 → 对话页签：发送消息验证应答与执行元数据；触发需审核工具后前往审核中心处理。
7. （可选）技能管理：导入技能包并关联 Agent，在对话中验证 `load_skill` 加载与 `skill_calls` 追溯。
8. （可选）工作流管理：创建 DAG（agent / mcp_tool / http / condition 节点）→ 保存并激活 → 手动 / Cron / Webhook 触发，在概览与执行追踪中查看结果。
9. 外部集成：创建 API Key，通过 `/invoke` 对外提供服务。

## API 端点

> 各接口的用途、用法、入参与出参详见 [docs/api/api.md](docs/api/api.md)（含统一响应格式、错误码、DAG 定义结构与枚举速查）；对外提供的接口见 [docs/api/external-api.md](docs/api/api.md)。

- POST /api/v1/auth/register - 用户注册 (首个注册用户自动成为 admin)
- POST /api/v1/auth/login - 用户登录
- POST /api/v1/auth/logout - 用户登出
- GET  /api/v1/auth/me - 当前用户 (含角色与权限码, 前端菜单/按钮级权限控制)

## 概览 API (需 Bearer Token)

- GET /api/v1/overview/summary - 概览看板 (Agent/MCP/模型/工作流/审批计数 + 运行中 Agent)

## RBAC API (需 Bearer Token)

- GET    /api/v1/users - 用户列表 (q 搜索, status 过滤, page/size 分页) [user:manage]
- POST   /api/v1/users - 创建用户 {"username","email","password","roles"} (roles 为空=默认 user 角色) [user:manage]
- GET    /api/v1/users/:id - 用户详情 (含角色) [user:manage]
- PUT    /api/v1/users/:id - 更新 (email/status/password 均可空=不变) [user:manage]
- PUT    /api/v1/users/:id/roles - 全量替换角色 {"roles"} [user:manage]
- DELETE /api/v1/users/:id - 删除 (保护: 不可删自己/最后一个管理员) [user:manage]
- GET    /api/v1/roles - 角色列表 (含权限码 + 用户数) [role:manage]
- POST   /api/v1/roles - 创建角色 {"name","description","permissions"} [role:manage]
- PUT    /api/v1/roles/:id - 更新 (permissions 非 null 全量替换; admin 角色强制保留关键权限) [role:manage]
- DELETE /api/v1/roles/:id - 删除 (内置角色/有用户绑定的角色不可删) [role:manage]
- GET    /api/v1/permissions - 权限点定义列表 [role:manage]

## Agent API (需 Bearer Token)

- POST   /api/v1/agents - 创建 Agent (mcp_ids 绑定 MCP, tools 自动校验, 可携带 skills/skills_usage_mode)
- GET    /api/v1/agents - 列表 (q 搜索, status 过滤, page/size 分页)
- GET    /api/v1/agents/:id - 详情 (含实例)
- PUT    /api/v1/agents/:id - 更新 (产生新版本; mcp_ids 非 null 全量同步绑定, null 不变)
- GET    /api/v1/agents/:id/mcps - 绑定 MCP 列表 (含已发现工具)
- GET    /api/v1/agents/:id/skills - 关联技能列表
- PUT    /api/v1/agents/:id/skills - 全量更新技能关联 {"skills":["skill-id"]} (校验 required_tools)
- DELETE /api/v1/agents/:id - 删除 (运行中禁止)
- POST   /api/v1/agents/:id/start - 启动实例
- POST   /api/v1/agents/:id/stop - 停止实例
- GET    /api/v1/agents/:id/metrics - 调用统计 (from/to, RFC3339)
- GET    /api/v1/agents/:id/logs - 运行日志 (level/keyword/since 过滤)
- GET    /api/v1/agents/:id/versions - 版本历史
- POST   /api/v1/agents/:id/rollback - 回滚 {"version": N} (运行中禁止)
- POST   /api/v1/agents/:id/invoke - 外部调用 (API Key 认证, 不走 JWT; 有待审核时返回 202)
- GET    /api/v1/agents/:id/invoke/approvals/:approvalId - 外部调用待审核结果查询 (API Key 认证; 202 后轮询终态与工具执行 result)
- POST   /api/v1/agents/:id/chat - 对话 (M2.5; 不传 session_id 新建会话, 传则续聊; 应答含 execution_id/tokens/latency 元数据与 pending_approvals)
- GET    /api/v1/agents/:id/sessions - 会话列表
- GET    /api/v1/agents/:id/sessions/:sid - 会话详情 (含消息历史)
- DELETE /api/v1/agents/:id/sessions/:sid - 删除会话
- POST   /api/v1/agents/:id/keys - 创建 API Key (明文仅返回一次, 可选 expires_at)
- GET    /api/v1/agents/:id/keys - API Key 列表
- DELETE /api/v1/agents/:id/keys/:keyId - 吊销 API Key
- POST   /api/v1/agents/:id/keys/:keyId/delete - 删除已吊销的 API Key
- GET    /api/v1/agents/dashboard - 状态看板

## 技能 API (需 Bearer Token)

- POST   /api/v1/skills/import - 导入技能包 (multipart zip; force=true 同名升级 version+1) [skill:write]
- GET    /api/v1/skills - 列表 (q 搜索, status 过滤, page/size 分页) [skill:read]
- GET    /api/v1/skills/:id - 详情 (含文件列表) [skill:read]
- PUT    /api/v1/skills/:id - 更新状态 {"status":"active"|"disabled"} [skill:write]
- DELETE /api/v1/skills/:id - 删除 (有关联拦截; force=true 级联解绑) [skill:write]
- GET    /api/v1/skills/:id/files - 资源文件列表 [skill:read]
- GET    /api/v1/skills/:id/files/*path - 文件内容 (路径穿越防护) [skill:read]
- GET    /api/v1/skills/:id/agents - 关联 Agent 列表 [skill:read]
- GET    /api/v1/skills/:id/usage - 使用统计 (近 30 天加载次数等) [skill:read]

## MCP API (需 Bearer Token)

- POST   /api/v1/mcp-servers - 创建 MCP 服务器 (注册后即时连通性检测)
- GET    /api/v1/mcp-servers - 列表 (q 搜索, status/tag 过滤, page/size 分页)
- GET    /api/v1/mcp-servers/:id - 详情 (含脱敏凭证)
- PUT    /api/v1/mcp-servers/:id - 更新 (endpoint/凭证变更触发重检)
- DELETE /api/v1/mcp-servers/:id - 删除 (级联绑定+健康历史)
- POST   /api/v1/mcp-servers/:id/test - 手动连通性测试
- GET    /api/v1/mcp-servers/:id/health - 健康状态 + 最近检查历史 (limit)
- GET    /api/v1/mcp-servers/:id/tools - 已发现工具列表
- PUT    /api/v1/mcp-servers/:id/tools - 工具级审核开关 {"tools":[{"name","requires_approval"}]}
- POST   /api/v1/mcp-servers/:id/tools/call - 工具调用代理 {"name","arguments"} (需审核工具返回 202 + approval_id)
- GET    /api/v1/mcp-servers/:id/agents - 已绑定 Agent 列表
- POST   /api/v1/mcp-servers/:id/agents - 绑定 Agent {"agent_id"}
- DELETE /api/v1/mcp-servers/:id/agents/:agentId - 解绑 Agent

## Approval API (需 Bearer Token)

- GET    /api/v1/approvals - 审核请求列表 (过滤: status/mcp_server_id/tool/agent_id/source/from/to)
- GET    /api/v1/approvals/settings - 审核全局配置 (超时/超时策略)
- PUT    /api/v1/approvals/settings - 更新审核配置 {"default_timeout_minutes","on_timeout"} [mcp:write]
- GET    /api/v1/approvals/:id - 审核详情 (参数快照 + 执行结果)
- POST   /api/v1/approvals/:id/approve - 通过并执行 {"comment"} (mcp:approve)
- POST   /api/v1/approvals/:id/reject - 驳回 {"comment"} (mcp:approve)

## Model API (需 Bearer Token)

- POST   /api/v1/model-templates - 创建模型模板 (注册后即时连通性探测)
- GET    /api/v1/model-templates - 列表 (q 搜索, provider/status/tag 过滤, page/size 分页)
- GET    /api/v1/model-templates/:id - 详情 (含脱敏 API Key)
- PUT    /api/v1/model-templates/:id - 更新 (连接参数变更触发重检)
- DELETE /api/v1/model-templates/:id - 删除 (级联配额+用量+健康历史)
- POST   /api/v1/model-templates/:id/test - 手动连通性测试
- GET    /api/v1/model-templates/:id/health - 健康状态 + 最近检查历史 (limit)
- GET    /api/v1/model-templates/:id/usage - 配额 + 最近调用日志 (limit)
- GET    /api/v1/model-quota - 配额列表
- PUT    /api/v1/model-quota/:modelId - 设置/更新配额 {"daily_limit","monthly_limit","daily_token_limit","monthly_token_limit"} (0=不限)
- GET    /api/v1/model-usage - 全部模型用量概览 (配额+近 24h 聚合)
- POST   /api/v1/models/route - 路由选择 (dry-run, 按优先级+配额故障转移, 不消耗配额)

## Workflow API (需 Bearer Token)

- POST   /api/v1/workflows - 创建 (definition 自动校验 DAG, 生成 webhook_token)
- GET    /api/v1/workflows - 列表 (q 搜索, status 过滤, page/size 分页)
- GET    /api/v1/workflows/dashboard - 执行看板 (状态计数 + 最近 10 条)
- POST   /api/v1/workflows/validate - 校验 DAG 定义 (环检测/节点/边)
- GET    /api/v1/workflows/:id - 详情
- PUT    /api/v1/workflows/:id - 更新 (DAG 变更生成新版本快照)
- DELETE /api/v1/workflows/:id - 删除
- POST   /api/v1/workflows/:id/activate - 激活 (可触发)
- POST   /api/v1/workflows/:id/archive - 归档
- PUT    /api/v1/workflows/:id/schedule - 定时调度 {"enabled","cron","input","timezone"}
- POST   /api/v1/workflows/:id/trigger - 手动触发 {"input"} [workflow:execute]
- GET    /api/v1/workflows/:id/versions - 版本历史
- GET    /api/v1/workflows/:id/executions - 执行历史 (status/trigger 过滤, 分页)
- GET    /api/v1/workflow-executions/:id - 执行详情 (含节点级执行记录)
- POST   /api/v1/workflow-executions/:id/cancel - 取消执行 [workflow:execute]
- POST   /api/v1/webhooks/workflows/:token - Webhook 触发 (公开端点, token 鉴权, payload 作为执行输入)
- GET    /api/v1/webhooks/workflows/:token/executions/:id - 执行状态公开查询 (token 鉴权, 仅本工作流, 状态视图不含输入/输出 payload)
