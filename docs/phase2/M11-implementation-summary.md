# M11 知识库 - 实现总结

> **版本：** v1.0
> **日期：** 2026-09-14
> **范围：** 开发计划 W1–W3（D1–D15）全量交付；验收标准 A1–A11 + 权限矩阵（admin/operator/user × kb:read/kb:write）

---

## 1. 已完成内容

### 1.1 数据模型（3 张新表 + Vector 类型 + HNSW 索引）

| 表/字段 | 说明 |
|----|------|
| `kb_categories` | 分类 (PRD 3.1)。name (2-32, 唯一) / description (≤200) / parent_id (多级分类预留, 本期恒 NULL) / created_by |
| `kb_documents` | 知识条目 (PRD 3.2)。category_id / title (2-100) / content (≤200KB Markdown) / source (`manual`/`chat_summary`) / source_session_id + source_agent_id (总结来源, 服务端校验) / status (`active`/`archived`) / access_count + last_accessed_at (命中异步回写) / `embedding vector(1024) NULL=未向量化` |
| `agent_kb_bindings` | Agent 分类绑定 (读 + 写)。agent_id + category_id 唯一; read_only 预留 (P1, 恒 false) |
| `model.Vector` | pgvector GORM 数据类型 (零新依赖: 自实现 `driver.Valuer`/`sql.Scanner`, 文本格式 `[0.1,0.2,...]`) |
| 索引 | `idx_kb_doc_scope (category_id, status)` 复合索引; `idx_kb_doc_embedding` **HNSW 部分索引** (`hnsw(embedding vector_cosine_ops) WHERE embedding IS NOT NULL`) |
| 基建 | `infra/docker-compose.yml` 镜像 `postgres:16-alpine` → `pgvector/postgres:pg16`; `database.Init` 幂等 `CREATE EXTENSION IF NOT EXISTS vector` + HNSW 索引 (存量表零 DDL, 同大版本数据目录兼容) |

### 1.2 代码结构

```
backend/internal/
├── model/kb.go                    # KBCategory / KBDocument / AgentKBBinding + 状态/来源/检索模式常量
├── model/vector.go                # Vector 类型 (pgvector 文本格式, 可空)
├── repository/kb_repository.go    # 3 个仓储; SearchByVector (raw SQL + HNSW 余弦召回, 分类过滤内嵌模板)
├── service/kb_service.go          # 分类/条目 CRUD + 归档 + 试算 + 回填门面 + 审计
├── service/kb_retriever.go        # 两阶段检索器: 召回 (向量/关键词) + rerank 排序 + 四级降级 + 候选集 TTL 缓存
├── service/kb_embedder.go         # KBEmbedder / KBVectorWriter: 异步向量化 + 手动 Backfill
├── service/kb_summarize.go        # KBSummarizer: 会话总结草稿 (严格 JSON 校验 + 建议分类)
├── service/agent_kb.go            # 绑定同步 (Agent 创建/更新) + AgentKBView + 检索模式归一
├── service/chat_kb.go             # 对话注入组装 (数据声明包裹 + 字符预算) + execution_meta
├── service/chat_service.go        # runTurn / ContinueAfterApproval 双组装点 + search_knowledge 工具
├── modelclient/                   # Rerank 客户端 (OpenAI/vLLM 兼容 POST /rerank)
├── service/model_service.go       # RerankForKB + 模板探测 (embed 维度校验 / rerank 连通性)
├── service/platform_service.go    # 平台设置 KB 字段 (运行时 Sink 即时生效) + 保存探测门禁
├── api/kb/handler.go              # 12 个 /kb/* 端点 (RBAC 逐端点权限点)
├── api/agent/handler.go           # GET /:id/kb、GET /:id/kb/search、POST /:id/sessions/:sid/kb-summary
├── config/config.go               # KB_* 环境变量 (11 项, 见 §5)
└── database/seed.go               # RBAC 种子: kb:read / kb:write (admin+operator 双点, user 只读)

backend/tools/mock-model-server/   # E2E mock 扩展: /v1/rerank (bigram 重叠打分)、MOCK_EMBED_DIM、
                                   # "知识库总结助手" 固定 JSON 应答、CALL_TOOL 参数透传、故障注入
frontend/src/                      # api/kb.ts、api/agent.ts (kbSummary)、api/platform.ts + store、
                                   # pages/kb/KbListPage、AgentForm 区块、Agent 详情页签、
                                   # ChatPanel 总结弹框、PlatformSettingsPage KB 区块、菜单路由

tests/
├── kb-w1-api.ps1                  # W1 管理端接口自测
└── kb-e2e.ps1                     # E2E: A1–A11 + 权限矩阵 (自管 mock, 幂等, 87 项断言)
```

### 1.3 两阶段检索流水线（四级降级，永不阻断对话）

```
阶段一 召回 (KB_RECALL_SIZE=20)                     阶段二 排序 (top-K, KB_TOP_K=3, 上限 20)
┌ embed 已配置: 查询向量化 -> HNSW 余弦召回 ────────────┐
│   向量召回为空 -> 返回空 (不回退关键词)                │
│   向量化失败 / 召回失败 -> 降级 ▼                      ├─► rerank 已配置: POST /rerank (KB_RERANK_TIMEOUT=300ms)
└ embed 未配置 / 运行时故障: 关键词预筛                  │     超时/失败 -> 按召回序
   (M10 打分, 候选集 TTL 缓存 60s + 写失效,              │
    上限 KB_MAX_CANDIDATES=500) -> top 20 ─────────────┘
```

- **四级降级阶梯**（按模型配置组合决定主路径，运行时故障继续向下降级）：`embed+rerank` → `仅 embed`（向量召回 + 召回序）→ `仅 rerank`（关键词 + rerank）→ `纯关键词`（W1 行为）。
- **硬约束**：注入路径整体受 `KB_RETRIEVAL_TIMEOUT` (500ms) 预算约束，任何一步失败/超时 → 返回空/降级结果，对话不阻断（A8 验证）。
- **服务端重新鉴权**：注入 / `search_knowledge` 工具 / Agent 试算三条路径均按 **Agent 当前绑定分类**重新鉴权（不信任调用方传参）；越权目标分类在入库环节 403（A5 固化）。

### 1.4 对话注入与可观测

- **检索模式**（Agent 配置 `kb_search_mode`）：`auto` = 每轮自动注入 top-K「知识库参考」段 + 注册 `search_knowledge` 内置工具；`tool` = 仅注册工具，模型按需检索；`off` = 不启用。
- **注入安全**：知识段以数据声明 + 分隔符包裹（视为参考资料而非指令，沿用 M9/M10 提示词注入边界），总字符预算 `KB_CHAR_BUDGET` (4000) 截断，超长条目摘录截断；工具返回同样携带声明。
- **可观测**（assistant 消息 `execution_meta`）：
  - `kb_injected = { count, ids }` — 本轮实际注入条目
  - `kb_searches = [{ round, query, top_k, hit_ids, status, latency_ms }]` — 每轮检索记录，`status` ∈ `ok` / `empty` / `duplicate` / `degraded` / `error`
- **访问统计**：Agent 真实检索命中后异步回写 `access_count` / `last_accessed_at`（fire-and-forget，同 M10 模式）。

### 1.5 一键总结入库

1. `POST /agents/:id/sessions/:sid/kb-summary`（`agent:read` 即可生成草稿）：校验总开关 / 会话归属 (400) / 绑定非空 (400) / 存在用户消息 (400)。
2. 取最近 `KB_SUMMARY_MAX_TURNS` (20) 条 user/assistant 消息 + 固定结构化提示词（可选 `focus` 指定重点）→ LLM 调用（模型 = 平台 `kb_summary_model` → `KB_SUMMARY_MODEL` → Agent 当前模型；30s 超时；用量计入 ModelUsageLog）。
3. **严格解析校验**（失败不产出半成品）：去代码块包裹 → JSON 解析 → title 1-60 字 / content 300-1500 字（过短 400 提示"未提炼出有效知识"，超出截断）。
4. **建议分类**：首条用户消息 + 标题对绑定分类名/描述做关键词打分，取最高分；无区分度（0 分或并列）不返回。
5. 前端确认（标题/正文可编辑，分类下拉**仅含绑定分类**并高亮建议项）→ `POST /kb/documents`（`source=chat_summary`，需 `kb:write`；服务端再校验会话归属 400 + 目标分类 ∈ Agent 绑定 403）。

### 1.6 向量化与回填

- 条目创建 / 正文更新后**异步向量化**（fire-and-forget；embedding 模型未配置时跳过，条目仍可落库并经关键词路径检索）。
- `POST /kb/documents/backfill-embeddings`（`kb:write`）：为未向量化的 active 条目分批补算向量，返回 `{ total, embedded, failed, leftover }`；单条失败仅计数继续，**维度不匹配等系统性错误提前终止并明确报错**。
- **维度门禁**：平台保存 embedding 模型时真实探测（`/embeddings` 输出维度 vs `KB_VECTOR_DIM` 列维度），不匹配 400 明确报错且**保留原值**（A11 验证）。

### 1.7 权限与审计

- 权限点：`kb:read`（列表/详情/试算/总结草稿）/ `kb:write`（CRUD/归档/回填/入库）；种子：admin + operator 双点，user 仅 `kb:read`（权限矩阵 E2E 固化）。
- 审计：分类/条目的创建、更新、删除（含硬删除前留痕）全部落 `audit_logs`（`resource=kb_category` / `kb_document`，detail 含关键上下文）。
- 删除保护：分类存在条目（含归档）时 409 阻断。

### 1.8 前端

- **知识库管理页**（`pages/kb/KbListPage`，菜单「知识库」）：分类 CRUD + 条目 CRUD / 归档 / 恢复 / 多条件搜索（分类/关键词/来源/状态）。
- **Agent 表单区块**：多选绑定分类 + 检索模式（auto/tool/off）；**详情页「知识库」页签**：绑定视图（分类 + 条目数 + 生效模式）+ 试算卡片（实时验证召回效果）。
- **ChatPanel「总结入库」**：按钮门控（`kb:write` + Agent 有绑定 + 存在会话 + ≥1 条用户消息）；弹框标题/正文可编辑、分类下拉仅绑定分类、默认选中建议项。
- **平台设置「知识库 (M11)」区块**：总开关 + 向量化/rerank/总结模型（AutoComplete，模型模板名）+「向量回填」按钮；单 Form 内 Divider 分区（不新建独立 Form 实例）。
- `tsc --noEmit` + `npm run build` 全绿。

### 1.9 联调修缺陷（W3 D14）

| 缺陷 | 根因 | 修复 |
|----|------|------|
| 向量召回静默降级关键词，未向量化条目误命中 | `SearchByVector` 用 GORM `Raw().Where()` 拼接：条件被追加到 `LIMIT` 之后 → SQL 语法错误 → 走关键词降级 | 分类过滤移入 raw SQL 模板（`category_id IN ?` 切片占位符，两变体），`backend/internal/repository/kb_repository.go` |
| A11「召回走 HNSW 索引」断言在小表上不稳定 | 小表规划器合法选择 Seq Scan / btree+Sort（比 HNSW 代价低） | EXPLAIN 前置 `SET enable_seqscan = off; SET enable_sort = off;`（单条 psql 多语句），强制暴露索引可用性 |
| kb-summary 500（30s 超时）间歇出现 | 开发库模型健康检查器 (60s) 在 mock 停止期间把 e2e 模板标记 `error` → 路由跳过 mock 命中库内外部真实端点（慢） | E2E 模板**删旧重建**（重建触发探测恢复 active，并断言 `status=active`）+ 固定 `kb_summary_model` 直指 mock 模板（消除全库路由抖动）；另加全局 KB 数据清理防手动测试残留污染 A11 |

---

## 2. 端到端验证（tests/kb-e2e.ps1, 2026-09-13, **87 PASS / 0 FAIL**）

```
A1   auto 模式对话自动注入: kb_injected 含 doc1 / 不含未绑定分类条目 / count>=1      PASS
A2   未绑定 Agent 对话: 200 且无 kb_injected / 无 kb_searches                        PASS
A3   search_knowledge 工具: status=ok, hit_ids 含 doc1; 非绑定分类查询 empty          PASS
A4   一键总结: 草稿 200 (建议分类=A, 下拉仅含绑定分类) -> 入库 201 (source=chat_summary
     且关联会话) -> 下一轮对话可检索                                                  PASS
A5   越权入库: 非绑定分类 403; 会话不属于该 Agent 400                                  PASS
A6   删除保护: 非空分类 409, 空分类 200; 审计留痕                                      PASS
A7   权限矩阵: admin/operator (kb:read+kb:write) vs user (仅 kb:read) ×
     categories/documents/search CRUD 全组合 + kb-summary (agent:read 即可)           PASS
A8   向量模型故障注入 (mock 500): 对话不阻断 200, 关键词降级仍命中, 试算正常           PASS
A9   归档: 归档后不被检索, 列表可见, 恢复后重新可检索                                   PASS
A10  总开关关闭: 对话 200 无注入 / 工具不注册; 管理端 CRUD 正常; 恢复后复通             PASS
A11  pgvector 路径: 回填前未向量化条目不命中 (向量路径直达, 零降级) /
     回填 {embedded>=3, failed=0, leftover=0} / EXPLAIN 走 idx_kb_doc_embedding /
     rerank 排序 top1=doc1 / 保存维度不匹配模型 400 且原模型继续生效                   PASS
```

脚本特性：自管 mock（:9101 dim=1024 / :9102 dim=512，始终重建二进制）、开头 PUT 重置 KB 平台设置基线（幂等）、全局 KB 数据清理（防残留污染）、finally 全量清理（`-Keep` 跳过）、PS 5.1 UTF-8 BOM 兼容。

---

## 3. 依赖变更

- **Go**：零新第三方依赖（`Vector` 自实现 GORM 数据类型；rerank 复用现有 modelclient HTTP 通道）。
- **前端**：零新依赖（复用 antd / 现有 store 模式）。
- **基建**：postgres 镜像 `postgres:16-alpine` → `pgvector/postgres:pg16`（同 Postgres 16 大版本，数据目录格式兼容；切换前建议 `SELECT version()` 核对小版本并备份 volume；非 Docker 开发环境需本地 PG 安装 pgvector 扩展）。

---

## 4. 与 PRD 的偏差说明

| 项 | PRD | 当前实现 | 说明 |
|----|-----|----------|------|
| 多级分类 | 支持 | `parent_id` 字段预留, 恒 NULL | P1 再做 (计划 §6) |
| 只读绑定 | 读/写区分 | 绑定 = 读 + 写; `read_only` 字段预留恒 false | P1 再做 |
| 条目分块检索 | - | 整条向量 + 摘录截断 | P2 评估 (万级条目内 HNSW 亚 50ms) |
| 向量维度 | 随模型 | 列级固定 1024 (`KB_VECTOR_DIM`)，保存模型时探测校验 | 换维度模型需重建列 (P0 规模下换模型成本低) |
| 回填 | - | 同步分批执行 | P0 规模可接受; 规模增长后转异步任务 |
| 平台试算作用域 | - | `GET /kb/search` 全局（可传 category_ids）; Agent 作用域试算走 `GET /agents/:id/kb/search`（强制绑定鉴权） | 平台视角预览 vs Agent 视角验证 |

---

## 5. 运行方式

### 5.1 环境变量（backend/.env，均有默认值，可全缺省）

| 变量 | 默认 | 说明 |
|----|----|------|
| `KB_ENABLED` | `true` | 总开关（平台设置优先） |
| `KB_TOP_K` | `3` | 注入 top-K |
| `KB_RECALL_SIZE` | `20` | 召回候选数（亦为 rerank 候选上限） |
| `KB_MAX_CANDIDATES` | `500` | 关键词路径候选集上限 |
| `KB_CHAR_BUDGET` | `4000` | 注入段总字符预算 |
| `KB_RETRIEVAL_TIMEOUT` | `500ms` | 注入路径检索超时（超时跳过注入） |
| `KB_RERANK_TIMEOUT` | `300ms` | rerank 调用超时（超时按召回序） |
| `KB_VECTOR_DIM` | `1024` | 向量列维度（保存 embedding 模型时探测校验） |
| `KB_EMBED_MODEL` | 空 | 向量化模型模板名（空 = 不启用向量召回；平台设置优先） |
| `KB_RERANK_MODEL` | 空 | rerank 模型模板名（空 = 不重排；平台设置优先） |
| `KB_SUMMARY_MODEL` | 空 | 总结模型模板名（空 = Agent 当前模型；平台设置优先） |
| `KB_SUMMARY_MAX_TURNS` | `20` | 总结输入消息上限 |
| `KB_MAX_DOC_BYTES` | `204800` | 单条目正文上限 (200KB) |

Docker 部署时以上变量已在 infra/docker-compose.yml 的 backend 服务中注入（三个模型名变量经宿主机 .env 以 ${KB_EMBED_MODEL:-} 等形式覆盖，默认空）。

### 5.2 E2E

```
# 前置: backend 已运行 (:8080), postgres (docker infra-postgres-1) 已运行
powershell -NoProfile -ExecutionPolicy Bypass -File tests\kb-e2e.ps1
# 自管 mock :9101 (dim 1024) / :9102 (dim 512), 结束时自动清理 (测试数据/Agent/模板)
# 预期: 87 PASS / 0 FAIL
```

### 5.3 常用 curl

```
# 分类 / 条目
curl -X POST localhost:8080/api/v1/kb/categories -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"name":"运维","description":"PostgreSQL 等"}'
curl -X POST localhost:8080/api/v1/kb/documents -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"category_id":"$CAT_ID","title":"慢查询排查","content":"..."}'

# 平台试算 / Agent 试算 (绑定作用域)
curl "localhost:8080/api/v1/kb/search?query=慢查询排查&top_k=3" -H "Authorization: Bearer $TOKEN"
curl "localhost:8080/api/v1/agents/$AGENT_ID/kb/search?query=慢查询排查" -H "Authorization: Bearer $TOKEN"

# 向量回填 / 一键总结草稿
curl -X POST localhost:8080/api/v1/kb/documents/backfill-embeddings -H "Authorization: Bearer $TOKEN"
curl -X POST localhost:8080/api/v1/agents/$AGENT_ID/sessions/$SID/kb-summary -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"focus":"慢查询"}'
```

---

## 6. 待办（M11 剩余 + 衔接项）

1. **多级分类**（`parent_id` 已预留）：树形分类 UI + 绑定到父级自动含子级语义（P1）
2. **只读绑定**（`read_only` 已预留）：跨团队共享分类的只读消费（P1）
3. **条目分块 + 块级向量**（P2）：长条目按段落分块召回，提高长文命中精度
4. **回填异步任务化**：条目规模增长后转后台任务 + 进度查询（当前同步分批，P0 规模可接受）
5. **README 模块状态补 M10**：记忆模块交付后模块状态表未补行（历史遗留，与 M11 无关，建议补记）