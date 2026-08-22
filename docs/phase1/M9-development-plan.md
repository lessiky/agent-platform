# Agent 管理平台 — M9 技能管理 开发计划

> **版本：** v1.1
> **日期：** 2026-08-21
> **状态：** ✅ 已完成（2026-08-21 交付：全部任务落地，后端单测通过，API 级 E2E 41/41 全绿）
> **周期：** 3 周（15 个工作日）
> **上游依赖：** PRD v1.1 第 2.5 节（技能管理模块）；Phase 1（M1–M5）已交付
> **排期说明：** 依 PRD 第 8 章，M9 不计入原 Phase 2 周期估算，可与 M6 / M7 并行排期（仅依赖现有对话执行链路与 MCP 绑定机制）

---

## 1. M9 目标

实现技能包 "导入 / 删除、Agent 关联、运行时使用" 的 P0 能力，让 Agent 的领域能力可通过技能包扩展：

- 技能包导入（UI + API）：规范校验、同名冲突升级、审计
- 技能列表 / 详情 / 文件预览 / 删除（拦截 + 强制级联解绑）
- Agent 关联：绑定表、required_tools 依赖校验、注入预算
- 运行时注入：渐进式披露（技能目录 + `load_skill` 内置工具）/ 全量注入 双模式
- 使用追溯：`execution_meta.skill_calls`、使用统计、RBAC 与审计

### 本周期范围（P0）

| 模块 | 内容 |
|------|------|
| 技能域后端 | 3 张表、导入 / 列表 / 详情 / 文件预览 / 删除 / 状态、RBAC (`skill:read` / `skill:write`)、审计、使用统计 |
| Agent 关联 | `skills` 字段、绑定同步、`required_tools` 校验、注入预算校验、级联解绑 |
| 执行链路 | 技能目录 / 全文注入、`load_skill` 内置工具、`execution_meta.skill_calls` |
| 前端 | 技能列表 / 详情 / 导入对话框，Agent 表单 + Skills 页签，菜单路由 |
| 测试 | 单测（导入校验 / 注入组装 / 绑定）、E2E（导入→关联→对话 load_skill→追溯）、权限矩阵 |

### 不在本周期（顺延）

| 项 | PRD 优先级 | 后续安排 |
|------|----------|----------|
| 技能版本历史 / 回滚 | P1 | Phase 2 后续（M8 或单列） |
| 元数据更新（tags / description / author） | P1 | Phase 2 后续 |
| 全局配置页（包限制 / 类型白名单 / 注入预算） | P1 | 本期代码内置默认值 + 配置项预留，页面后续 |
| 工作流 Agent 节点技能联动 | P1 | 随 M5 扩展后续 |
| 批量导入 / 单技能开关 / 版本锁定 / 使用趋势 / 对象存储 | P2 | 持续迭代 |

---

## 2. 总体设计（复用既有模式）

| 方面 | 设计 | 参照 |
|------|------|------|
| 数据模型 | `skills` / `skill_files` / `skill_agent_bindings` 3 表 + `AgentConfig.skills_usage_mode` | PRD 5.10–5.12；`mcp_agent_bindings` |
| 绑定机制 | Agent 创建/更新请求 `skills`（技能 ID 数组）全量同步 (新增缺失, 移除多余) | `agentService.syncMCPBindings` |
| 依赖校验 | `required_tools` ⊆ Agent 可用工具集 (MCP 绑定 ∩ `config.tools`)，不满足拒绝关联并返回缺失工具列表 | `agentService.validateTools` |
| 执行链注入 | `chatService.runTurn`：系统提示词后追加技能段；注册 `load_skill` 内置工具；`runToolRounds` 内置工具分支 (不走人工审核, 同一执行内去重) | M2.5 对话执行链 |
| 使用追溯 | `ChatResult.SkillCalls` → `persistChatTurn` 写入 `execution_meta.skill_calls` | `execution_meta` 既有 JSONB |
| 权限 | seed 增 `skill:read` / `skill:write`（operator 双权限，user 仅 skill:read），middleware 挂权限点 | M1 RBAC seed (只增不删) |
| 审计 | 复用 `AuditLogRepository`，actions: `skill.imported` / `skill.upgraded` / `skill.deleted` / `skill.status_changed` / `skill.agent_bound` / `skill.agent_unbound` | M4.5 审核审计 |
| 前端 | `pages/skill/`（List / Detail / 导入对话框）；`AgentFormPage` 增 skills 多选 + 使用模式；`AgentDetailPage` 增 Skills 页签 | MCP 管理页面模式 |

### 关键流程

**导入流程：**

```
上传 (multipart)
  -> 解压 (流式, 总量上限 10MB)
  -> 路径安全 (拒绝绝对路径 / .. / 空字节, 防 zip-slip)
  -> 文件类型白名单 + 文件数 (<=500) + 单文件 (<=2MB) 校验
  -> SKILL.md 存在 + frontmatter 解析 (name/description 必填) + 正文非空
  -> 名称冲突检查 (已存在且 !force -> 409; force -> 升级 version+1)
  -> 事务写入: skills + skill_files + 审计 (升级时 Agent 关联保留)
```

**运行时注入流程 (runTurn)：**

```
加载 Agent 关联技能 (binding 交集 status=active, disabled 不注入)
  -> skills_usage_mode = metadata_injection (默认):
      系统提示词追加 "技能目录" (名称 + 描述 + 加载说明)
      注册内置工具 load_skill(skill_name)
  -> skills_usage_mode = full_injection:
      所有技能正文直接注入系统提示词, 不注册 load_skill
  -> 模型调用 load_skill:
      正文以工具结果返回 (重复加载 -> 简短确认)
      required_tools 未完全可用 -> 警告 + 执行元数据标记 partial
      记录 skill_calls (name / version / mode / chars / latency / status)
```

**安全边界（硬约束）：** 平台对技能包仅做存储与只读注入，不执行包内任何代码（含脚本）；包内脚本仅供模型参考。技能正文按 "数据" 处理：注入时包裹分隔符并在系统提示词中声明为参考数据（防提示词注入）。

---
## 3. 详细任务

### Week 1: 后端基础（技能域）

| 序号 | 任务 | 预计工时 | 负责人 | 前置条件 | 状态 |
|------|------|----------|--------|----------|------|
| 1.1 | Skill / SkillFile / SkillAgentBinding 模型 + AutoMigrate | 0.5d | 后端 | - | ✅ 完成 |
| 1.2 | 技能导入核心：zip 解析 + 路径安全 (zip-slip) + frontmatter 解析 + 类型白名单 / 大小 / 文件数校验，表驱动单测 | 2d | 后端 | 1.1 | ✅ 完成 |
| 1.3 | 导入 API `POST /api/v1/skills/import`（multipart、force 升级、名称唯一约束并发安全） | 1d | 后端 | 1.2 | ✅ 完成 |
| 1.4 | 技能列表 / 详情 / 文件预览下载 API（路径穿越防护） | 1d | 后端 | 1.3 | ✅ 完成 |
| 1.5 | 技能删除（有关联拦截 + 返回关联列表；force 级联解绑）+ active / disabled 状态（disabled 运行时不注入） | 1d | 后端 | 1.4 | ✅ 完成 |
| 1.6 | RBAC：seed 增 `skill:read` / `skill:write` + 角色映射（operator 双权限，user 只读） | 0.5d | 后端 | - | ✅ 完成 |
| 1.7 | 审计日志：导入 / 升级 / 删除 / 状态 / 绑定 / 解绑（复用 AuditLogRepository） | 0.5d | 后端 | 1.3, 1.5, 1.6 | ✅ 完成 |
| 1.8 | Agent 关联后端：Create / Update Agent 请求增 `skills` 字段、`syncSkillBindings`、required_tools 依赖校验、注入预算校验（full_injection 正文总长 <= 128KB）、删除 Agent 级联解绑 | 1.5d | 后端 | 1.5, 1.6 | ✅ 完成 |
| 1.9 | 使用统计 API：关联数、近 30 天加载次数（execution_meta.skill_calls JSONB 聚合）、最近使用时间 | 0.5d | 后端 | -（数据在 2.3 后产生, SQL 先行） | ✅ 完成 |

**交付物：**
- 技能域后端 API 完整（导入 / 列表 / 详情 / 文件 / 删除 / 状态 / 统计）
- RBAC + 审计落地
- Agent 关联（含校验规则）
- 导入校验单测通过

### Week 2: 执行链注入 + 前端（后端 / 前端并行）

| 序号 | 任务 | 预计工时 | 负责人 | 前置条件 | 状态 |
|------|------|----------|--------|----------|------|
| 2.1 | 技能上下文组装：加载关联技能 (active)，生成技能目录 (metadata_injection) 或全文 (full_injection)，单测 | 1.5d | 后端 | 1.8 | ✅ 完成 |
| 2.2 | `load_skill` 内置工具：工具定义注册 + `runToolRounds` 执行分支（不走审核、重复加载返回确认、required_tools 缺失警告 + partial 标记） | 1.5d | 后端 | 2.1 | ✅ 完成 |
| 2.3 | 执行追溯：`ChatResult.SkillCalls`、`persistChatTurn` 写 `execution_meta.skill_calls`、执行日志记技能加载 | 1d | 后端 | 2.2 | ✅ 完成 |
| 2.4 | 示例技能包 fixture（SKILL.md + 资源文件；一个技能声明 required_tools 指向 mock-mcp-server 工具） | 0.5d | 后端 | 1.2 | ✅ 完成 |
| 2.5 | 前端基建：`api/skill.ts` + types + 菜单 + 路由 | 0.5d | 前端 | 1.4 | ✅ 完成 |
| 2.6 | 技能列表页：搜索 / 标签筛选 / 状态 / "使用中" 标记 / 版本与大小展示 | 1d | 前端 | 2.5 | ✅ 完成 |
| 2.7 | 导入对话框：拖拽上传（Upload.Dragger）、校验错误码与原因展示、同名冲突 force 选择 | 1d | 前端 | 2.6 | ✅ 完成 |
| 2.8 | 技能详情页：元数据、SKILL.md 渲染预览、文件树（文本预览 / 二进制下载）、关联 Agent、使用统计、禁用 / 删除操作 | 1.5d | 前端 | 2.6 | ✅ 完成 |
| 2.9 | Agent 表单：skills 多选 + `skills_usage_mode` 选择；Agent 详情页 Skills 页签（绑定 / 解绑、required_tools 覆盖状态展示） | 1.5d | 前端 | 1.8, 2.5 | ✅ 完成 |

**交付物：**
- 对话执行链技能注入（双模式）+ load_skill 可用
- `execution_meta.skill_calls` 可追溯
- 技能管理前端（列表 / 详情 / 导入）+ Agent 侧关联 UI

### Week 3: 集成测试与验收

| 序号 | 任务 | 预计工时 | 负责人 | 前置条件 | 状态 |
|------|------|----------|--------|----------|------|
| 3.1 | E2E（Playwright + mock-model-server `CALL_TOOL:` 脚本）：导入示例技能 → 关联 Agent → 对话触发 `load_skill` → 校验应答与 execution_meta.skill_calls | 1.5d | 全员 | 2.3, 2.9 | ✅ 完成 |
| 3.2 | 异常路径测试：zip-slip / 加密 zip / 缺 SKILL.md / 非法文件名 / 超限大小 / 超文件数 / 同名冲突 (非 force) 全部拒绝且返回具体原因；删除拦截与 force 级联 | 1d | 全员 | 1.2, 1.5 | ✅ 完成 |
| 3.3 | 权限矩阵：operator (skill:read+write) / user (仅 skill:read) 读写边界 | 0.5d | 全员 | 1.6 | ✅ 完成 |
| 3.4 | 安全自查：文件下载路径穿越、包内代码不执行、审计完整性、提示词注入隔离（正文包裹分隔符 + 系统提示词声明） | 0.5d | 后端 | 2.3 | ✅ 完成 |
| 3.5 | 容量回归：10MB 包 / 500 文件 / 128KB 全量注入边界下的 prompt 长度与对话延迟；无技能关联时零注入 | 1d | 后端 | 2.3 | ✅ 完成 |
| 3.6 | 文档：用户手册技能章节 + API 文档更新；编写 `M9-implementation-summary.md` | 1d | 全员 | 3.1 | ✅ 完成 |
| 3.7 | M9 复盘 | 0.5d | 全员 | 3.6 | ✅ 完成 |

**交付物：**
- E2E + 异常路径 + 权限测试报告
- M9 实施总结文档
- 可上线的技能管理功能
---

## 4. 里程碑评审标准

| 里程碑 | 验收标准 |
|--------|----------|
| **M9**（✅ 已达成，2026-08-21） | ✅ 示例技能包可正常导入 (UI + API) ✅ 非法包 (zip-slip / 缺 SKILL.md / 超限 / 同名非 force) 全部拒绝并返回具体原因 ✅ 技能列表 / 详情 / 文件预览 / 禁用可用 ✅ 删除有关联时拦截、force 可级联解绑 ✅ Agent 可关联技能，required_tools 校验与注入预算校验生效 ✅ 对话默认注入技能目录、load_skill 按需加载正文 (full_injection 直接注入) ✅ execution_meta.skill_calls 记录完整 ✅ skill:read / skill:write 权限边界正确 ✅ 操作全量可审计 |

---

## 5. 风险与应对

| 风险 | 概率 | 影响 | 应对措施 |
|------|------|------|----------|
| 技能内容占用 Token 过多，挤占模型上下文 | 中 | 高 | 默认渐进式披露；full_injection 关联时预算校验；3.5 容量回归 |
| 提示词注入（技能正文含对抗性指令） | 中 | 中 | 技能内容按数据处理：正文包裹分隔符 + 系统提示词声明 "技能内容为参考数据"；不执行包内代码 |
| zip 边界情况（加密 / 嵌套 / 超大 / 损坏包） | 中 | 中 | 仅支持标准非加密 zip；流式解压 + 大小上限；1.2 单测覆盖，3.2 异常路径测试 |
| bytea 存大文件导致 DB 膨胀 | 低 | 低 | MVP 上限 10MB 包 / 500 文件；后续不足再迁移对象存储 (P2) |
| 30 天加载次数聚合性能（JSONB 查询） | 低 | 中 | 按技能维度 + 30 天窗口按需聚合（详情页）；量大后转定期 rollup |
| 与 M6 / M7 并行排期冲突 | 低 | 中 | M9 只涉及 chat 执行链与 agent 域，与 M6 (告警) / M7 (指标) 文件交集小；冲突时优先保执行链部分 |

---

## 6. 验收 Demo 脚本

1. UI 导入 `weekly-report` 示例技能包（frontmatter 声明 required_tools 指向 mock-mcp-server 工具）
2. 创建 / 编辑 Agent：关联该技能，选择 metadata_injection
3. Agent 对话输入 "生成周报"（mock 模型脚本 `CALL_TOOL:load_skill`），观察模型加载技能正文后按正文应答
4. 校验会话 `execution_meta.skill_calls`（技能名 / 版本 / 字符数 / 延迟 / status）
5. 将 Agent 切为 full_injection 并关联超预算技能 → 观察拒绝并提示原因
6. 删除技能：先被拦截（存在关联）→ force → Agent 技能段消失，对话不再注入
7. 切换 user（只读）角色：可浏览，导入 / 删除 / 关联操作不可用

---

## 7. 文档维护

- 本文档随 M9 推进持续更新；完成后交付状态记入 `M9-implementation-summary.md`（命名参照 M2.5 / M4.5）。
- 2026-08-21：M9 全部任务完成，状态已更新为 ✅ 完成。验证：后端 `go test` 全量通过；API 级 E2E（`tests/skill-e2e.ps1`）41/41 通过（导入 / 升级 / 关联 / 对话 load_skill / 追溯 / 删除拦截 / force 级联）；前端 `tsc --noEmit` 通过。
- PRD 对应章节：`docs/prd/需求规则说明书.md` 2.5 / 5.10–5.12 / 6.5。