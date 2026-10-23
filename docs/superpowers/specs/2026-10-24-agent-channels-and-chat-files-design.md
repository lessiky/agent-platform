# Agent 渠道与对话文件设计

> 日期：2026-10-24
> 状态：已定稿，待实现
> 范围：飞书双向渠道、对话文件、Agent 文件工具、飞书文件双向传输、文件清理

## 1. 背景与目标

平台当前支持 Agent 网页对话、MCP 工具、技能、知识库、记忆和外部 API 调用，但缺少以下能力：

- Agent 无法配置外部对话渠道。
- 网页对话无法上传文件。
- Agent 无法读取、创建或发送文件。
- 飞书用户无法与 Agent 双向对话或交换文件。
- 文件没有生命周期和清理机制，长期运行会占满服务器磁盘。

本次实现目标：

1. 提供平台级飞书渠道配置和 Agent 级渠道绑定。
2. 通过飞书长连接接收群聊和私聊消息，并把回复发回飞书。
3. 网页和飞书共用同一套 Agent 对话、文件和权限链路。
4. 支持用户上传、飞书入站、模型创建、MCP 产物和外部 API 上传文件。
5. Agent 可通过内置工具读取、创建和发送文件。
6. 文件支持配额、过期清理、手动清理和存储用量展示。

不在本期范围内：

- 多飞书应用同时在线。
- 飞书以外的渠道实现。数据模型和接口保留扩展空间。
- 图片和扫描 PDF 的视觉识别或 OCR。
- 文件内容病毒扫描、DLP 和加密存储。
- 多后端实例之间共享长连接。单实例部署先通过租约防止重复消费，后续可扩展为多实例分片。

## 2. 总体架构

### 2.1 飞书渠道

采用平台级飞书自建应用：

- 管理员在“平台设置 -> 渠道管理”配置 App ID 和 App Secret。
- App Secret 使用现有 AES-256-GCM 凭证加密方案存储。
- 后端使用飞书官方 Go SDK 建立 WebSocket 长连接。
- 应用启动、配置更新或连接异常时由渠道运行管理器建立或重建连接。
- 同一连接通过 PostgreSQL 租约保证只有一个后端实例消费事件。

消息处理分为四层：

1. **接入层**：接收并持久化原始事件，按飞书 `event_id` 和 `message_id` 去重。
2. **路由层**：解析身份、群绑定、私聊偏好和 Agent 授权，确定目标 Agent。
3. **执行层**：复用现有 `ChatService`，保持模型、MCP、技能、知识库、记忆、审核和统计行为一致。
4. **投递层**：把文本、卡片和文件发送回飞书，记录发送状态并支持有限重试。

群聊中同一飞书群固定映射一个 Agent。私聊用户使用 `/agent <名称>` 选择 Agent，并保存用户偏好。

### 2.2 文件

文件采用“元数据 + 消息引用 + 本地对象存储”结构：

- PostgreSQL 保存文件元数据、引用、投递和清理状态。
- 文件正文保存在 `CHAT_FILE_DIR` 指定的持久卷中。
- 存储路径只使用 UUID，不使用用户提供的文件名。
- `FileStorage` 接口隔离物理存储，后续可替换为 MinIO 或 S3。
- 网页、飞书和外部 API 均进入同一文件服务。

### 2.3 Agent 工具

新增以下内置工具：

- `list_session_files`：列出当前会话内可访问文件。
- `read_file`：按文件 ID 和字节范围读取文件。
- `create_file`：创建文本文件并加入当前会话。
- `send_file`：把文件投递到当前网页会话，来源为飞书时同步发送回飞书。

这些工具与 MCP 工具共存。文件工具不走 MCP 人工审核；`create_file` 和 `send_file` 受 Agent 配置和大小限制约束。

## 3. 飞书渠道设计

### 3.1 连接生命周期

渠道状态：

- `disabled`：管理员关闭渠道。
- `connecting`：正在建立长连接。
- `connected`：已连接并接收事件。
- `reconnecting`：连接中断后重连。
- `error`：凭证错误、权限错误或连续重连失败。

运行时行为：

- 后端启动时读取已启用连接并尝试启动。
- 配置保存后先停止旧连接，再使用新凭证启动。
- 长连接断开后指数退避重连，上限 1 分钟。
- 服务正常退出时停止消费并释放租约。
- 租约每 15 秒续期，过期时间 45 秒。
- 无法获得租约的实例保持待机，不消费飞书事件。

### 3.2 身份映射

首次收到某飞书用户消息时自动创建 `channel_identities`：

- 唯一标识优先使用 `union_id`，缺失时使用 `open_id`。
- 保存租户、昵称、头像和最近活跃时间。
- `platform_user_id` 初始为空，管理员可在渠道身份列表中绑定平台账号。
- 未绑定平台账号时仍按飞书身份隔离会话和记忆。
- 绑定平台账号后保留原会话；新记忆可用平台账号作为用户维度。

### 3.3 私聊路由

私聊命令：

- `/agent`：列出当前用户可使用的 Agent。
- `/agent <名称>`：切换默认 Agent。
- `/new`：为当前“用户 + Agent”创建新会话。
- `/help`：显示命令帮助。

路由规则：

1. 读取 `channel_user_preferences` 中的当前 Agent。
2. 未选择时提示用户先执行 `/agent <名称>`。
3. 校验 Agent 渠道启用、审核状态、实例状态和私聊授权范围。
4. 查找 `channel_conversations` 中 `p2p:<identity_id>:<agent_id>` 对应会话。
5. 不存在时创建 `ChatSession` 和渠道会话映射。

### 3.4 群聊路由

群聊只处理 `@机器人` 的消息。非 `@机器人` 消息不进入 Agent。

群绑定支持两种方式：

- 管理员在 Agent 渠道页填写飞书 `chat_id`。
- 管理员生成 10 分钟有效、单次使用的绑定码，在目标群发送 `/bind <绑定码>`。

约束：

- 一个飞书群同时只能绑定一个 Agent。
- 重复绑定必须由管理员先解绑。
- 群绑定成功后，该群消息固定路由到绑定 Agent。
- 群会话使用 `group:<chat_id>` 作为唯一键，所有群成员共享上下文。
- 回复默认引用原消息；可在群内 `@` 发起人，避免多人同时提问时难以对应。

### 3.5 授权

平台侧权限：

- 飞书凭证、租约、全局身份列表：`platform:manage`。
- Agent 渠道绑定、群绑定、私聊授权：`agent:write`。
- 查看渠道状态：具备对应资源的读权限即可。

飞书侧授权：

- Agent 的私聊范围支持 `all`、`users`、`departments`。
- `users` 使用飞书 `open_id` 或 `union_id` 白名单。
- `departments` 使用飞书部门 ID 白名单，需要通讯录读取权限。
- 无法读取部门信息时按失败关闭处理，只允许显式用户白名单。
- 群聊必须存在启用中的群绑定。

Agent 可用性：

- `review_status` 必须为 `approved`。
- Agent 实例必须为 `running`。
- Agent 渠道绑定必须启用。

不满足条件时向飞书返回明确提示，不执行模型调用。

### 3.6 事件处理

支持的消息类型：

- 文本 `text`。
- 富文本 `post`，转换为 Markdown 或纯文本。
- 文件 `file`。
- 图片 `image`，作为普通文件保存，首期不做视觉理解。

处理流程：

1. 长连接收到事件后写 `channel_events`，状态为 `received`。
2. 唯一键冲突时认定重复事件，记录为 `ignored`。
3. 按 `conversation_key` 串行执行，避免同一会话消息并发导致上下文错乱。
4. 执行身份、权限、Agent 和文件校验。
5. 下载飞书附件并登记 `chat_files`。
6. 组装 `file_ids` 并调用现有对话执行链。
7. 把最终结果写入投递队列。
8. 更新事件状态为 `processed` 或 `failed`。

失败事件保留错误和尝试次数。仅对可重试错误重新执行，例如飞书限流、网络超时和临时服务错误。

### 3.7 回复投递

- 短文本使用飞书消息回复。
- Markdown 优先使用飞书交互卡片渲染。
- 超过飞书单消息限制时按自然段拆分，并保持顺序。
- 工具审核、模型错误和权限错误使用文字卡片提示。
- 文本和文件均由 `channel_deliveries` 持久化。
- 投递状态为 `pending`、`sending`、`sent`、`failed`。
- 可重试错误按 1 秒、5 秒、30 秒、2 分钟指数退避，最多 5 次。
- 投递成功后保存飞书消息 ID；失败原因对管理员可见。

## 4. 文件设计

### 4.1 默认限制

| 限制 | 默认值 |
| --- | --- |
| 单文件大小 | 200MB |
| 单消息文件数 | 5 个 |
| 单消息总大小 | 1GB |
| 单会话容量 | 10GB |
| 全局容量 | 200GB |
| 普通文件保留期 | 30 天 |
| 未发送临时文件保留期 | 24 小时 |
| 模型创建文本文件大小 | 2MB |

所有限制通过 `CHAT_FILE_*` 环境变量覆盖。

上传使用流式写入，不允许把 200MB 文件整体读入内存。应用层单消息限制为 1GB，Nginx `client_max_body_size` 调整为 1100MB，为 multipart 开销和并发请求留余量。

### 4.2 文件状态

- `staging`：已上传但尚未绑定到消息，可被发送引用或过期清理。
- `ready`：已绑定消息或完成任务校验，可供读取和投递。
- `deleting`：已被清理任务选中，禁止读取和投递。
- `deleted`：数据库保留墓碑记录，物理文件已删除。
- `failed`：上传、校验或物理删除失败，需要重试或人工处理。

### 4.3 文件来源

- `web_upload`：网页对话上传。
- `channel_inbound`：飞书消息附件。
- `tool_created`：`create_file` 创建。
- `mcp_artifact`：MCP 或技能产物转存。
- `api_upload`：外部 API 上传。

### 4.4 网页上传流程

1. 用户在 ChatPanel 选择或拖拽文件。
2. 前端调用上传接口；新对话允许不传 `session_id`，文件进入 `staging`。
3. 上传成功后展示文件名、类型、大小和删除按钮。
4. 发送消息时在 Chat 或 Chat Stream 请求中携带 `file_ids`。
5. 服务端校验文件归属、状态、会话和 Agent。
6. 持久化用户消息后写 `chat_message_files`，文件状态切换为 `ready`。
7. 消息历史返回 `files`，前端在用户气泡中显示附件卡片。
8. 文件未发送时按临时文件 TTL 清理。

### 4.5 飞书入站文件

1. 从消息 `file_key` 或 `image_key` 调用飞书资源下载接口。
2. 使用流式下载并将实际字节数限制为 200MB。
3. 计算 SHA-256、探测 MIME，并清洗原始文件名。
4. 写入 `chat_files`，来源为 `channel_inbound`。
5. 与对应飞书用户消息建立引用。
6. 在模型上下文中仅提供文件 ID、名称、类型、大小和工具提示。
7. 模型需要内容时调用 `read_file`。

飞书接口本身对文件类型和大小有额外限制。平台先接受 200MB，实际发送失败时记录飞书原始错误并提示用户。

### 4.6 文件读取

`read_file` 参数：

- `file_id`：必填。
- `offset`：可选，从 0 开始。
- `limit`：可选，默认 64KB，上限 1MB。

行为：

- 只允许读取当前会话有引用的文件。
- 文本、Markdown、JSON、CSV 和常见代码文件按 UTF-8 返回。
- UTF-8 无效或二进制文件返回 MIME、大小和安全提示，不返回大段二进制。
- 大文件必须分段读取。
- 每次读取更新 `last_accessed_at` 和访问次数，避免活跃文件过早过期。
- 文件进入 `deleting`、`deleted` 或 `failed` 时拒绝读取。

### 4.7 文件创建

`create_file` 参数：

- `name`：文件名。
- `content`：文本内容，最大 2MB。
- `media_type`：可选，默认按扩展名推断。

首期支持常用文本类型，包括 TXT、Markdown、JSON、CSV、YAML、XML 和代码文件。创建后文件立即绑定当前 assistant 消息，并可由 `send_file` 投递。

### 4.8 文件发送

`send_file` 参数：

- `file_id`：要发送的文件。
- `text`：可选说明文本。

网页渠道：

- 在 assistant 消息中增加文件卡片。
- 提供预览或下载入口。

飞书渠道：

- 调用飞书文件上传接口获得 file key。
- 调用消息发送接口发送文件，并附带说明文本。
- 记录 `channel_deliveries`，与普通回复使用同一重试机制。

### 4.9 外部 API 文件

新增 API Key 鉴权接口：

- `POST /api/v1/agents/:id/invoke/files`：流式上传文件。
- `POST /api/v1/agents/:id/invoke/channel-deliveries`：把已有文件投递到指定飞书私聊或群。

投递目标仅限该 Agent 已授权的群或私聊身份。外部调用方不能借由 API 绕过 Agent 渠道授权。

`InvokeAgentRequest` 增加可选 `file_ids`，允许外部调用把文件作为当前轮附件。

## 5. 数据模型

所有新增表使用 UUID 主键和 `created_at`、`updated_at` 审计字段。

### 5.1 `channel_connections`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `provider` | varchar(32) | 固定为 `feishu` |
| `name` | varchar(64) | 配置名称 |
| `mode` | varchar(32) | `long_connection` |
| `app_id` | varchar(128) | 飞书 App ID |
| `app_secret_encrypted` | text | 加密后的 App Secret |
| `config` | jsonb | 非敏感扩展配置 |
| `enabled` | bool | 是否启用 |
| `status` | varchar(24) | 连接状态 |
| `last_connected_at` | timestamptz | 最近连接成功时间 |
| `last_event_at` | timestamptz | 最近收到事件时间 |
| `last_error` | text | 最近错误 |
| `created_by` | uuid | 创建人 |

首期只允许一个启用的飞书连接。

### 5.2 `channel_runtime_leases`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `connection_id` | uuid | 唯一 |
| `owner_id` | varchar(128) | 实例标识 |
| `lease_expires_at` | timestamptz | 租约过期时间 |
| `updated_at` | timestamptz | 最近续期时间 |

### 5.3 `channel_identities`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `connection_id` | uuid | 渠道连接 |
| `tenant_key` | varchar(128) | 飞书租户 |
| `open_id` | varchar(128) | 应用内用户 ID |
| `union_id` | varchar(128) | 应用间用户 ID，可空 |
| `platform_user_id` | uuid | 平台账号，可空 |
| `display_name` | varchar(128) | 昵称 |
| `avatar_url` | text | 头像 |
| `status` | varchar(16) | `active` / `disabled` |
| `last_seen_at` | timestamptz | 最近活跃时间 |

唯一索引：`connection_id + union_id`；空 union_id 时使用 `connection_id + open_id`。

### 5.4 `agent_channel_bindings`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `agent_id` | uuid | Agent |
| `connection_id` | uuid | 渠道连接 |
| `enabled` | bool | 是否开放 |
| `private_access_mode` | varchar(16) | `all` / `users` / `departments` |
| `allowed_user_ids` | jsonb | 飞书用户 ID 列表 |
| `allowed_department_ids` | jsonb | 飞书部门 ID 列表 |
| `created_by` | uuid | 创建人 |

唯一索引：`agent_id + connection_id`。

### 5.5 `channel_group_bindings`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `connection_id` | uuid | 渠道连接 |
| `chat_id` | varchar(128) | 飞书群 ID |
| `chat_name` | varchar(256) | 群名称 |
| `agent_id` | uuid | 绑定 Agent |
| `enabled` | bool | 是否启用 |
| `bound_via` | varchar(16) | `manual` / `code` |
| `created_by` | uuid | 创建人 |

唯一索引：`connection_id + chat_id`。

### 5.6 `channel_bind_codes`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `connection_id` | uuid | 渠道连接 |
| `agent_id` | uuid | 待绑定 Agent |
| `code_hash` | varchar(64) | 绑定码摘要 |
| `expires_at` | timestamptz | 过期时间 |
| `used_at` | timestamptz | 使用时间，可空 |
| `created_by` | uuid | 创建人 |

### 5.7 `channel_user_preferences`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `connection_id` | uuid | 渠道连接 |
| `channel_identity_id` | uuid | 飞书身份 |
| `agent_id` | uuid | 当前 Agent |
| `updated_at` | timestamptz | 更新时间 |

唯一索引：`connection_id + channel_identity_id`。

### 5.8 `channel_conversations`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `connection_id` | uuid | 渠道连接 |
| `conversation_key` | varchar(256) | 渠道唯一键 |
| `chat_type` | varchar(16) | `p2p` / `group` |
| `chat_id` | varchar(128) | 群 ID，可空 |
| `channel_identity_id` | uuid | 私聊身份，可空 |
| `agent_id` | uuid | Agent |
| `chat_session_id` | uuid | 平台会话 |
| `active` | bool | 是否启用 |
| `last_inbound_at` | timestamptz | 最近入站时间 |

唯一索引：`connection_id + conversation_key`。

`conversation_key` 格式：

- 群聊：`group:<chat_id>`。
- 私聊：`p2p:<channel_identity_id>:<agent_id>`。

### 5.9 `channel_events`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `connection_id` | uuid | 渠道连接 |
| `event_id` | varchar(128) | 飞书事件 ID |
| `message_id` | varchar(128) | 飞书消息 ID |
| `event_type` | varchar(64) | 事件类型 |
| `status` | varchar(16) | `received` / `processing` / `processed` / `failed` / `ignored` |
| `payload` | jsonb | 最小必要事件数据 |
| `attempts` | int | 尝试次数 |
| `last_error` | text | 最近错误 |
| `received_at` | timestamptz | 接收时间 |
| `processed_at` | timestamptz | 完成时间 |
| `expires_at` | timestamptz | 事件数据保留时间，默认 7 天 |

唯一索引：`connection_id + event_id` 和 `connection_id + message_id`。

### 5.10 `channel_deliveries`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `connection_id` | uuid | 渠道连接 |
| `agent_id` | uuid | Agent |
| `chat_session_id` | uuid | 会话，可空 |
| `target_type` | varchar(16) | `chat` / `user` |
| `target_id` | varchar(128) | 飞书 chat_id 或 open_id |
| `kind` | varchar(16) | `text` / `card` / `file` |
| `file_id` | uuid | 文件，可空 |
| `reply_to_message_id` | varchar(128) | 原消息 ID |
| `payload` | jsonb | 发送内容 |
| `status` | varchar(16) | `pending` / `sending` / `sent` / `failed` |
| `attempts` | int | 尝试次数 |
| `next_retry_at` | timestamptz | 下次重试 |
| `last_error` | text | 最近错误 |
| `platform_message_id` | varchar(128) | 飞书消息 ID |
| `sent_at` | timestamptz | 发送时间 |

### 5.11 `chat_files`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `agent_id` | uuid | Agent |
| `session_id` | uuid | 会话，staging 可空 |
| `owner_user_id` | uuid | 平台用户，可空 |
| `owner_identity_id` | uuid | 渠道身份，可空 |
| `source` | varchar(32) | 文件来源 |
| `original_name` | varchar(512) | 原始文件名 |
| `storage_key` | varchar(512) | 存储相对路径 |
| `mime_type` | varchar(128) | MIME |
| `size_bytes` | bigint | 大小 |
| `sha256` | varchar(64) | 内容摘要 |
| `status` | varchar(16) | 文件状态 |
| `retention_expires_at` | timestamptz | 过期时间 |
| `last_accessed_at` | timestamptz | 最近读取时间 |
| `access_count` | int | 读取次数 |
| `last_error` | text | 处理错误 |
| `deleted_at` | timestamptz | 物理删除时间 |

索引：

- `agent_id + session_id + status`
- `status + retention_expires_at`
- `sha256`

### 5.12 `chat_message_files`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `message_id` | uuid | 消息 |
| `file_id` | uuid | 文件 |
| `relation` | varchar(24) | `user_upload` / `assistant_generated` |
| `created_at` | timestamptz | 创建时间 |

唯一索引：`message_id + file_id`。

### 5.13 现有表变更

`agent_chat_sessions` 增加：

- `source`：`web` / `api` / `feishu`。
- `channel_conversation_id`：可空，渠道会话映射。

`agent_chat_messages` 不直接保存文件 ID。接口响应通过 `chat_message_files` 聚合 `files`。

## 6. API 设计

### 6.1 渠道管理

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/api/v1/channels` | `platform:manage` | 渠道列表和运行状态 |
| `GET` | `/api/v1/channels/:id` | `platform:manage` | 渠道详情，凭证脱敏 |
| `PUT` | `/api/v1/channels/:id` | `platform:manage` | 更新凭证和配置 |
| `POST` | `/api/v1/channels/:id/test` | `platform:manage` | 测试凭证和连接 |
| `GET` | `/api/v1/channel-identities` | `platform:manage` | 飞书身份列表 |
| `PUT` | `/api/v1/channel-identities/:id` | `platform:manage` | 绑定或解绑平台账号 |

### 6.2 Agent 渠道绑定

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/api/v1/agents/:id/channels` | `agent:read` | 渠道绑定视图 |
| `PUT` | `/api/v1/agents/:id/channels/:channelId` | `agent:write` | 更新启用状态和私聊授权 |
| `GET` | `/api/v1/agents/:id/channels/:channelId/groups` | `agent:read` | 群绑定列表 |
| `POST` | `/api/v1/agents/:id/channels/:channelId/groups` | `agent:write` | 手动绑定群 |
| `DELETE` | `/api/v1/agents/:id/channels/:channelId/groups/:groupId` | `agent:write` | 解绑群 |
| `POST` | `/api/v1/agents/:id/channels/:channelId/bind-code` | `agent:write` | 生成群绑定码 |

### 6.3 文件

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/api/v1/agents/:id/files` | `agent:write` | 上传文件 |
| `GET` | `/api/v1/agents/:id/files` | `agent:read` | 文件列表 |
| `GET` | `/api/v1/agents/:id/files/:fileId` | `agent:read` | 文件元数据 |
| `GET` | `/api/v1/agents/:id/files/:fileId/content` | `agent:read` | 预览或下载 |
| `DELETE` | `/api/v1/agents/:id/files/:fileId` | `agent:write` | 手动删除 |
| `GET` | `/api/v1/agents/:id/files/storage` | `agent:read` | 会话和 Agent 用量 |

上传请求为 `multipart/form-data`：

- `file`：文件。
- `session_id`：可选。新会话上传时为空。

文件列表支持 `session_id`、`source`、`status`、`page` 和 `size`。

文件响应字段：

- `id`
- `agent_id`
- `session_id`
- `source`
- `original_name`
- `mime_type`
- `size_bytes`
- `status`
- `retention_expires_at`
- `created_at`

不向普通用户返回实际存储路径。

### 6.4 对话请求

`POST /api/v1/agents/:id/chat` 和 `/chat/stream` 请求增加：

```json
{
  "session_id": "uuid",
  "message": "请读取附件并总结",
  "file_ids": ["uuid"],
  "show_thinking": false
}
```

校验规则：

- 最多 5 个文件。
- 文件属于该 Agent。
- 文件已绑定同一会话，或会话为空时可在本轮绑定。
- 文件状态为 `staging` 或 `ready`。
- 文件必须属于当前平台用户或当前渠道身份。

会话消息响应增加：

```json
{
  "files": [
    {
      "id": "uuid",
      "name": "report.pdf",
      "mime_type": "application/pdf",
      "size_bytes": 1048576,
      "url": "/api/v1/agents/agent-id/files/file-id/content"
    }
  ]
}
```

### 6.5 外部 API

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/api/v1/agents/:id/invoke/files` | Agent API Key | 流式上传文件 |
| `POST` | `/api/v1/agents/:id/invoke/channel-deliveries` | Agent API Key | 投递文件到授权会话 |

`/invoke` 和异步执行请求增加 `file_ids`，执行结果增加 `files` 和 `generated_files`。

## 7. 清理与配额

### 7.1 过期时间

- 上传时计算 `retention_expires_at = now + 30 天`。
- 文件每次成功读取时，把过期时间延长到 `now + 30 天`。
- 所属会话发送新消息时，可批量刷新该会话文件的过期时间。
- `staging` 文件未被消息引用时，过期时间为 `now + 24 小时`。
- 会话删除后文件立即失去引用，进入待清理状态。

### 7.2 清理任务

每小时运行一次：

1. 分批查询 `retention_expires_at <= now` 且没有保护引用的文件。
2. 使用数据库锁标记为 `deleting`。
3. 删除物理文件。
4. 更新状态为 `deleted` 并写审计日志。
5. 写入失败时恢复为 `failed`，保留错误并在下一周期重试。

同时清理：

- 超过 7 天的渠道事件 payload。
- 超过 30 天且已完成的渠道投递。
- 已使用的群绑定码。
- 磁盘存在但数据库没有记录的孤儿文件。孤儿文件先移入隔离目录，等待一个周期后删除。

保护引用仅包括 `pending` / `sending` 的渠道投递和执行中的文件读取或发送任务。`staging` 文件按 24 小时 TTL 清理，`ready` 文件按普通保留期清理，`deleting` 文件只能由清理任务推进到终态。历史消息引用不阻止 TTL 清理；文件删除后，历史消息继续保留文件名和大小摘要，但下载入口显示“已过期清理”。

### 7.3 配额检查

上传前按以下顺序检查：

1. 单文件不超过 200MB。
2. 当前消息文件数不超过 5 个。
3. 当前消息文件总大小不超过 1GB。
4. 会话文件总量不超过 10GB。
5. 全局文件总量不超过 200GB。

达到配额时返回 `file_quota_exceeded`，同时写入告警日志。平台设置页展示当前用量、占用最高的会话、待清理数量和最近一次清理结果。

### 7.4 手动清理

管理员可在平台设置页触发“立即清理”。手动清理与定时任务使用同一服务，不允许跳过物理删除和审计步骤。

## 8. 权限与安全

- App Secret、飞书 file key 和原始事件敏感字段不返回给普通用户。
- 文件下载必须校验 Agent、会话和文件归属。
- 文件名经过长度、控制字符和路径分隔符清洗。
- 存储路径由 UUID 生成，拒绝 `..`、绝对路径和软链接。
- 文件不会被执行，不允许通过符号链接逃逸存储根目录。
- 上传和下载均支持 Context 取消，客户端断开时停止流式处理。
- API Key 渠道投递只允许该 Agent 已授权的群和用户。
- 飞书事件、删除和渠道配置变更写现有审计日志。
- 日志中不记录 App Secret、完整事件 payload 和文件正文。
- 删除操作使用状态机，不允许业务请求直接删除物理路径。

## 9. 错误处理

新增稳定错误码：

| 错误码 | 场景 |
| --- | --- |
| `channel_not_configured` | 飞书渠道未配置 |
| `channel_disabled` | 渠道已停用 |
| `channel_disconnected` | 长连接不可用 |
| `channel_not_bound` | 群未绑定 Agent |
| `channel_identity_disabled` | 飞书身份被停用 |
| `channel_access_denied` | 不在私聊授权范围 |
| `agent_not_available` | Agent 未审核或实例未运行 |
| `file_too_large` | 超过单文件 200MB |
| `file_count_exceeded` | 超过单消息 5 个 |
| `file_quota_exceeded` | 超过消息、会话或全局容量 |
| `file_not_ready` | 文件仍在处理中 |
| `file_not_found` | 文件不存在或无权访问 |
| `file_binary_unsupported` | 二进制文件不能按文本读取 |
| `channel_delivery_failed` | 飞书投递最终失败 |

网页和平台 API 返回标准错误响应；飞书渠道把错误转换为用户可读提示，同时记录技术错误。

## 10. 可观测性

日志字段：

- `connection_id`
- `event_id`
- `message_id`
- `conversation_key`
- `agent_id`
- `session_id`
- `file_id`
- `delivery_id`
- `trace_id`

需要观测：

- 长连接状态、重连次数、最近事件时间和事件处理延迟。
- 入站事件成功率、重复率、失败率和重试次数。
- 投递成功率、重试次数和飞书限流。
- 上传大小、失败原因、配额拒绝次数。
- 文件总量、会话占用、孤儿文件和清理释放空间。
- 清理任务耗时、删除成功数和失败数。

平台详情页展示渠道状态、最近错误和文件存储用量。

## 11. 前端设计

### 11.1 平台设置

新增“渠道管理”：

- 飞书应用配置。
- App Secret 脱敏和更新。
- 连接状态、测试按钮、最近事件和错误。
- 飞书身份列表和平台账号绑定。
- 存储用量、配额和“立即清理”。

### 11.2 Agent 详情

新增“渠道”页签：

- 启用或停用飞书渠道。
- 私聊授权模式、用户白名单和部门白名单。
- 群绑定列表、手动绑定和解绑。
- 绑定码生成和过期倒计时。
- 最近渠道错误和投递失败。

### 11.3 ChatPanel

- 输入区增加上传按钮。
- 支持选择和拖拽文件。
- 显示上传进度、文件大小和删除按钮。
- 发送前允许移除附件。
- 用户消息显示附件卡片。
- Assistant 消息显示生成文件和发送文件卡片。
- 文件卡片支持预览或下载。

## 12. 部署与配置

新增环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CHAT_FILE_DIR` | `/app/data/chat-files` | 文件根目录 |
| `CHAT_FILE_MAX_SIZE` | `200MB` | 单文件大小 |
| `CHAT_FILE_MAX_PER_MESSAGE` | `5` | 单消息文件数 |
| `CHAT_FILE_MAX_MESSAGE_BYTES` | `1GB` | 单消息总大小 |
| `CHAT_FILE_MAX_SESSION_BYTES` | `10GB` | 单会话容量 |
| `CHAT_FILE_MAX_TOTAL_BYTES` | `200GB` | 全局容量 |
| `CHAT_FILE_RETENTION` | `720h` | 普通文件保留期 |
| `CHAT_FILE_STAGING_RETENTION` | `24h` | 临时文件保留期 |
| `CHAT_FILE_CLEANUP_INTERVAL` | `1h` | 清理周期 |
| `CHANNEL_EVENT_RETENTION` | `168h` | 事件数据保留期 |
| `CHANNEL_LEASE_TTL` | `45s` | 渠道运行租约 |
| `CHANNEL_RECONNECT_MAX` | `1m` | 最大重连退避 |

Docker Compose 增加 `chat_files` 持久卷并挂载到 `/app/data/chat-files`。Nginx `/api/` 的请求体上限调整为 1100MB，请求与响应读写超时提高到 1800 秒，并保持流式代理。后端继续按单文件 200MB、单消息 1GB 做最终校验。

部署约束：

- 当前版本仅支持一个启用中的飞书自建应用。
- 长连接通过数据库租约防止多实例重复消费。
- 不使用共享文件目录的多实例部署不受支持，后续切换对象存储后再放开。

## 13. 实施阶段

### 阶段 1：文件基础设施

1. 新增文件、消息引用和投递数据表。
2. 实现 `FileStorage` 和本地存储实现。
3. 实现流式上传、元数据、下载、删除和配额检查。
4. 增加网页上传 UI 和消息附件展示。
5. 增加文件单元测试和 Docker 持久卷验证。

验收：

- 200MB 文件可流式上传，不使用与文件大小同等的内存。
- 上传文件可绑定消息、重新加载和下载。
- 配额和文件类型错误准确返回。

### 阶段 2：文件工具与清理

1. 实现 `list_session_files`、`read_file`、`create_file`、`send_file`。
2. 把文件元数据加入模型工具上下文。
3. 实现会话、网页和飞书统一的文件投递抽象。
4. 实现每小时清理任务、手动清理和用量统计。
5. 增加工具、清理、并发释放和失败重试测试。

验收：

- Agent 可列出、读取、创建和发送会话文件。
- 过期文件只在无活动引用时删除。
- 删除失败可见且可重试。
- 清理后磁盘和数据库状态一致。

### 阶段 3：飞书连接、事件与文本对话

1. 新增渠道配置、运行租约、身份、事件和投递数据表。
2. 实现飞书连接管理器和官方 SDK 长连接。
3. 实现事件持久化、幂等和按会话串行处理。
4. 实现身份自动创建、私聊 Agent 选择和群绑定。
5. 实现文本入站、Agent 执行和卡片回复。
6. 增加 Mock 飞书事件测试和租约竞争测试。

验收：

- 配置有效凭证后可建立长连接。
- 私聊和已绑定群可完成 Agent 对话。
- 重复事件不会重复调用模型。
- 断线后自动重连，租约释放后其他实例可接管。

### 阶段 4：飞书文件双向与外部 API

1. 下载飞书文件和图片并登记会话附件。
2. 把飞书文件加入 `file_ids` 和文件工具作用域。
3. 实现飞书文件上传和发送。
4. 实现 API Key 文件上传及渠道投递接口。
5. 实现投递重试、限额和错误展示。

验收：

- 飞书用户发送文件后，Agent 可读取。
- Agent 创建或引用的文件可发回飞书。
- 外部 API 只能向已授权目标投递。
- 超过飞书自身限制时返回明确错误。

### 阶段 5：权限、清理和发布

1. 增加渠道身份、用量和清理管理 UI。
2. 完成平台权限、Agent 授权和身份隔离审计。
3. 完成结构化日志和运行状态展示。
4. 更新 README、API 文档和部署配置。
5. 执行全量单元测试、集成测试、前端构建和 Docker 验证。

验收：

- 所有新增接口按 RBAC 正确拦截。
- 未授权身份、群和外部 API 目标不能访问 Agent。
- 清理任务持续运行，容量指标可观测。
- 文档与实现一致。

## 14. 测试计划

### 14.1 单元测试

- 飞书消息事件解析和消息 ID 去重。
- 群绑定、私聊偏好和私聊授权路由。
- 用户、部门、全员三种授权模式。
- Agent 审核和实例状态门禁。
- 绑定码过期、重复使用和跨 Agent 使用。
- 渠道会话键计算和群会话复用。
- 文件配额、MIME 探测和文件名清洗。
- `read_file` 范围、UTF-8 和二进制处理。
- 文件过期时间刷新和无引用判定。
- 清理任务并发标记和重试。
- 投递退避和最大重试次数。

### 14.2 集成测试

- 文件上传到消息绑定的完整流。
- 消息删除、会话删除和 Agent 删除后的文件清理。
- Mock 飞书事件触发文本对话并发送卡片。
- Mock 飞书文件事件下载后调用 `read_file`。
- Agent 调用 `create_file` 和 `send_file`。
- API Key 上传和授权渠道投递。
- 两个后端实例竞争租约，仅一个消费事件。

### 14.3 前端测试

- 上传进度、失败重试和发送前删除。
- 用户附件、assistant 文件和历史消息恢复展示。
- 渠道配置、群绑定、身份绑定和清理页面。
- TypeScript 编译和 Vite 生产构建。

### 14.4 Docker 与真实飞书验证

- 容器重启后 `chat_files` 持久卷内容仍可访问。
- Nginx 能代理 5 个 200MB 文件的流式上传，且不发生代理超时。
- 真实飞书长连接、私聊、群 `@`、文件和重连。
- 飞书限流、权限不足和文件超限错误可见。

## 15. 验收标准

功能完成后必须满足：

1. 管理员可配置一个飞书自建应用并看到连接状态。
2. 私聊用户可选择已授权 Agent，群聊按绑定路由到 Agent。
3. 同一飞书群共享会话，不同私聊用户和 Agent 的上下文隔离。
4. 重复飞书事件不会重复执行或重复发送。
5. 网页和飞书均可上传文件，并可由 Agent 通过工具读取。
6. Agent 可创建文本文件并发送到网页或飞书。
7. 外部 API 可上传并投递文件，但不能越权选择目标。
8. 单文件 200MB、单消息 1GB、单会话 10GB、全局 200GB 限制生效。
9. 过期、无引用和已删除会话的文件可被自动清理。
10. 平台可查看磁盘用量、配额、待清理数量和清理结果。
11. 凭证、文件路径和正文不会泄露到普通接口或日志。
12. 所有新增接口具备权限、单测、集成测试和错误场景覆盖。

## 16. 风险与后续扩展

风险：

- 飞书自身附件上限可能低于 200MB，平台需要保留原始错误。
- 长连接依赖单实例租约，故障切换和事件恢复需要真实故障测试。
- 本地磁盘容量是硬限制，必须配置持久卷、告警和外部备份。
- 文本文件解析不等于文档内容理解，PDF、DOCX、XLSX 和 OCR 需要后续解析器。
- 文件清理涉及数据库与磁盘非事务操作，必须依靠状态机和重试保证最终一致。

后续扩展：

- 对象存储和分片上传。
- PDF、DOCX、XLSX 解析。
- 图片和扫描件 OCR/视觉模型。
- 飞书及其他渠道的多应用、多租户。
- 文件病毒扫描、内容审查和客户端加密。
- 多实例长连接分片和跨实例会话调度。
