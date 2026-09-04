// 后端统一响应格式
export interface ApiResponse<T = unknown> {
  code: string;
  message: string;
  data?: T;
}

// 分页响应
export interface Paginated<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

// ---------- 认证 ----------
export interface User {
  id: string;
  username: string;
  email: string;
  status: number;
}

export interface LoginResult {
  user: User;
  token: string;
  roles: string[];
}

// ---------- RBAC 管理 ----------
export interface UserAdmin {
  id: string;
  username: string;
  email: string | null;
  status: number; // 1:active 0:disabled
  roles: string[];
  created_at: string;
  updated_at: string;
}

export interface RoleItem {
  id: string;
  name: string;
  description: string;
  status: number;
  permissions: string[];
  user_count: number;
  created_at: string;
}

export interface PermissionItem {
  id: string;
  code: string;
  name: string;
  resource: string;
  action: string;
}

export interface MeResult {
  user: User;
  roles: string[];
  permissions: string[];
}

// ---------- Agent ----------
export type AgentStatus = 'idle' | 'running' | 'stopped' | 'error';
export type InstanceStatus = 'pending' | 'running' | 'stopping' | 'stopped' | 'error';
export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

export interface AgentConfig {
  model?: string;
  system_prompt?: string;
  temperature?: number;
  max_tokens?: number;
  max_tool_rounds?: number;
  tools?: string[];
  skills_usage_mode?: string; // 技能注入模式 (M9)
}

export interface Agent {
  id: string;
  name: string;
  description: string;
  model_id: string | null;
  status: AgentStatus;
  version: number;
  config: AgentConfig;
  team_id: string | null;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface AgentInstance {
  id: string;
  agent_id: string;
  status: InstanceStatus;
  endpoint: string;
  started_at: string | null;
  stopped_at: string | null;
  last_heartbeat: string | null;
}

export interface AgentVersion {
  id: string;
  agent_id: string;
  version: number;
  name: string;
  description: string;
  config: AgentConfig;
  created_by: string | null;
  created_at: string;
}

export interface AgentLog {
  id: string;
  agent_id: string;
  instance_id: string | null;
  level: LogLevel;
  message: string;
  created_at: string;
}

export interface AgentAPIKey {
  id: string;
  agent_id: string;
  name: string;
  key_prefix: string;
  status: 'active' | 'revoked';
  last_used_at: string | null;
  expires_at: string | null;
  created_by: string | null;
  created_at: string;
  revoked_at: string | null;
}

export interface AgentMetrics {
  from: string;
  to: string;
  total_calls: number;
  total_errors: number;
  error_rate: number;
  total_tokens: number;
  avg_latency_ms: number;
  daily: {
    stat_date: string;
    calls: number;
    errors: number;
    total_tokens: number;
    total_latency_ms: number;
  }[];
}

export interface DashboardData {
  status_counts: Record<AgentStatus, number>;
  total_agents: number;
  running_agents: {
    id: string;
    name: string;
    status: AgentStatus;
    version: number;
    instance_id?: string;
    last_heartbeat?: string | null;
    started_at?: string | null;
  }[];
}

// ---------- 请求参数 ----------
export interface AgentQuery {
  q?: string;
  status?: AgentStatus;
  page?: number;
  size?: number;
}

export interface CreateAgentRequest {
  name: string;
  description?: string;
  model: string;
  system_prompt?: string;
  temperature?: number;
  max_tokens?: number;
  tools?: string[];
  mcp_ids?: string[];
  skills?: string[]; // 绑定的技能包 (M9)
  skills_usage_mode?: string; // 技能注入模式 (M9)
  model_id?: string;
  team_id?: string;
}

// Agent 绑定的 MCP 服务器 (含已发现工具)
export interface AgentBoundMCP {
  id: string;
  name: string;
  status: MCPStatus;
  tools: MCPTool[];
  last_error?: string;
}

export interface LogQuery {
  level?: LogLevel;
  keyword?: string;
  since?: string;
  page?: number;
  size?: number;
}
// ---------- 概览 (基本情况统计) ----------
export interface OverviewSummary {
  agents: {
    total: number;
    running: number;
    stopped: number;
    idle: number;
    error: number;
  };
  mcps: {
    total: number;
    normal: number;
    abnormal: number;
    tools_total: number;
  };
  models: {
    total: number;
    available: number;
    abnormal: number;
  };
  workflows: {
    active: number;
    draft: number;
    archived: number;
  };
  approvals: {
    total: number;
    pending: number;
    reviewed: number;
  };
  skills: {
    total: number;
    active: number;
    disabled: number;
  };
}
// ---------- MCP (M3) ----------
export type MCPTransport = 'stdio' | 'sse' | 'http';
export type MCPStatus = 'pending' | 'connected' | 'disconnected' | 'error';

export interface MCPTool {
  name: string;
  description: string;
  inputSchema?: Record<string, unknown>;
  /** 调用需人工审核 (M4.5) */
  requires_approval?: boolean;
}

export interface MCPServer {
  id: string;
  name: string;
  endpoint: string;
  transport: MCPTransport;
  description: string;
  status: MCPStatus;
  tools: MCPTool[];
  tags: string[];
  health_last_check: string | null;
  health_latency_ms: number | null;
  last_error: string;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface MCPCredentialsView {
  api_key_set: boolean;
  api_key_mask?: string;
  header_keys: string[];
}

export interface MCPHealthLog {
  id: string;
  mcp_id: string;
  ok: boolean;
  latency_ms: number;
  error: string;
  created_at: string;
}

export interface MCPHealthData {
  status: MCPStatus;
  last_check: string | null;
  latency_ms: number | null;
  last_error: string;
  history: MCPHealthLog[];
}

export interface MCPTestResult {
  ok: boolean;
  status: MCPStatus;
  latency_ms: number;
  server?: { name: string; version: string };
  tools_count: number;
  error?: string;
}

export interface MCPAgentBinding {
  id: string;
  mcp_id: string;
  agent_id: string;
  created_at: string;
}

// MCP 凭证 (创建/编辑请求)
export interface MCPCredentials {
  api_key?: string;
  headers?: Record<string, string>;
}

export interface MCPQuery {
  q?: string;
  status?: MCPStatus;
  tag?: string;
  page?: number;
  size?: number;
}

// ---------- M4.5 MCP 工具调用人工审核 ----------
export type ApprovalStatus = 'pending' | 'approved' | 'rejected' | 'expired';
export type ApprovalSource = 'manual' | 'runtime' | 'api_invoke' | 'workflow';
export type ApprovalOnTimeout = 'reject' | 'approve';

export interface ToolApproval {
  id: string;
  mcp_server_id: string;
  tool_name: string;
  agent_id: string | null;
  source: ApprovalSource;
  workflow_execution_id?: string | null;
  arguments: Record<string, unknown>;
  status: ApprovalStatus;
  requested_at: string;
  expires_at: string;
  decided_by: string | null;
  decided_at: string | null;
  comment: string | null;
  result?: Record<string, unknown> | null;
  executed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface ApprovalView extends ToolApproval {
  mcp_name: string;
  agent_name?: string;
}

export interface ApprovalSettings {
  id: string;
  default_timeout_minutes: number;
  on_timeout: ApprovalOnTimeout;
  updated_by?: string | null;
  updated_at: string;
}

export interface ApprovalQuery {
  status?: ApprovalStatus;
  mcp_server_id?: string;
  tool?: string;
  agent_id?: string;
  source?: ApprovalSource;
  from?: string;
  to?: string;
  page?: number;
  size?: number;
}

export interface ToolApprovalConfigItem {
  name: string;
  requires_approval: boolean;
}

export interface CreateMCPRequest {
  name: string;
  endpoint: string;
  transport: MCPTransport;
  description?: string;
  tags?: string[];
  credentials?: MCPCredentials;
}

export interface UpdateMCPRequest extends CreateMCPRequest {
  // credentials 为 undefined 表示保持不变, {} 表示清空
}
// ===== M4 模型管理 =====

export type ModelStatus = 'active' | 'inactive' | 'error';
export type ModelProvider = 'openai' | 'anthropic' | 'google' | 'azure' | 'custom';

export interface ModelGenConfig {
  temperature?: number;
  max_tokens?: number;
  top_p?: number;
}

export interface ModelTemplate {
  id: string;
  name: string;
  provider: ModelProvider;
  model: string;
  endpoint: string;
  config: ModelGenConfig;
  status: ModelStatus;
  priority: number;
  team_id: string | null;
  tags: string[];
  health_last_check: string | null;
  health_latency_ms: number | null;
  last_error: string;
  // 向量专用模板 (平台设置-记忆语义检索模型生效值, 不参与对话路由)
  is_embed_model?: boolean;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface ModelCredentialsView {
  api_key_set: boolean;
  api_key_mask?: string;
}

export interface ModelTestResult {
  ok: boolean;
  status: ModelStatus;
  latency_ms: number;
  models?: string[];
  models_count: number;
  error?: string;
}

export interface ModelHiResult {
  ok: boolean;
  latency_ms: number;
  content?: string;
  model?: string;
  finish_reason?: string;
  total_tokens?: number;
  error?: string;
}

export interface ModelHealthLog {
  id: string;
  model_id: string;
  ok: boolean;
  latency_ms: number;
  error: string;
  created_at: string;
}

export interface ModelHealthData {
  status: ModelStatus;
  last_check: string | null;
  latency_ms: number | null;
  last_error: string;
  history: ModelHealthLog[];
}

export interface ModelQuota {
  id: string;
  model_id: string;
  team_id: string | null;
  daily_limit: number;
  monthly_limit: number;
  daily_token_limit: number;
  monthly_token_limit: number;
  daily_used: number;
  monthly_used: number;
  daily_token_used: number;
  monthly_token_used: number;
  reset_daily_at: string;
  reset_monthly_at: string;
  updated_at: string;
  template_name?: string;
  model?: string;
  provider?: string;
}

export interface ModelUsageLog {
  id: string;
  model_id: string;
  agent_id: string | null;
  ok: boolean;
  tokens: number;
  latency_ms: number;
  error: string;
  created_at: string;
}

export interface ModelUsageData {
  quota: ModelQuota | null;
  logs: ModelUsageLog[];
}

export interface ModelUsageSummary {
  model_id: string;
  template_name: string;
  model: string;
  provider: string;
  status: ModelStatus;
  daily_limit: number;
  daily_used: number;
  daily_token_limit: number;
  daily_token_used: number;
  monthly_limit: number;
  monthly_used: number;
  monthly_token_limit: number;
  monthly_token_used: number;
  recent_calls: number;
  recent_tokens: number;
  recent_errors: number;
}

export interface RouteSkip {
  name: string;
  model: string;
  reason: string;
}

export interface RouteResult {
  selected?: ModelTemplate;
  reason: string;
  skipped?: RouteSkip[];
}

export interface CreateModelRequest {
  name: string;
  provider: ModelProvider;
  model: string;
  endpoint?: string;
  api_key?: string;
  priority?: number;
  status?: ModelStatus;
  config?: ModelGenConfig;
  tags?: string[];
}

export interface UpdateModelRequest extends CreateModelRequest {
  // api_key 为空表示保持不变; clear_api_key 清空
  clear_api_key?: boolean;
}

export interface ModelQuery {
  q?: string;
  provider?: ModelProvider;
  status?: ModelStatus;
  tag?: string;
  page?: number;
  size?: number;
}
// ===== M2.5 Agent chat =====
export type ChatRole = 'user' | 'assistant' | 'tool';

export interface ChatSession {
  id: string;
  agent_id: string;
  title: string;
  user_id: string | null;
  status: 'active' | 'archived';
  summary?: string; // 滚动摘要 (M10.2), 空 = 未触发
  last_message_at: string;
  created_at: string;
  updated_at: string;
}

export interface ChatMCPCall {
  mcp_name?: string;
  tool_name: string;
  status: 'ok' | 'error' | 'pending' | 'skipped';
  detail?: string;
  latency_ms?: number;
}

export interface ChatPendingApproval {
  approval_id: string;
  mcp_name: string;
  tool_name: string;
}

export interface ChatMessage {
  id: string;
  session_id: string;
  role: ChatRole;
  content: string;
  execution_id: string | null;
  execution_meta?: Record<string, unknown> | null;
  created_at: string;
}

export interface ChatSkillCall {
  skill_name: string;
  version?: number;
  mode?: string; // metadata (load_skill 按需加载)
  chars?: number;
  latency_ms?: number;
  status: 'ok' | 'partial' | 'duplicate' | 'error';
  detail?: string;
}

export interface ChatResult {
  session_id: string;
  message_id: string;
  reply: string;
  execution_id: string;
  model?: string;
  model_name?: string;
  total_tokens: number;
  latency_ms: number;
  mcp_calls?: ChatMCPCall[];
  skill_calls?: ChatSkillCall[]; // 本轮加载的技能 (M9, 与 execution_meta.skill_calls 同构)
  pending_approvals?: ChatPendingApproval[];
}

// ---------- 记忆 (M10) ----------
export type MemoryKind = 'preference' | 'fact' | 'decision' | 'event';
export type MemorySource = 'user_explicit' | 'llm_extracted';
export type MemoryStatus = 'active' | 'archived';

export interface Memory {
  id: string;
  agent_id: string;
  user_id: string | null; // null = Agent 级全局记忆
  kind: MemoryKind;
  content: string;
  source: MemorySource;
  status: MemoryStatus;
  access_count: number;
  last_accessed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface MemoryListResult {
  items: Memory[];
  total: number;
  page: number;
  page_size: number;
}

// ---------- 技能 (M9) ----------
export type SkillStatus = 'active' | 'disabled';
export type SkillsUsageMode = 'metadata_injection' | 'full_injection';

export interface Skill {
  id: string;
  name: string;
  version: number;
  version_spec: string;
  description: string;
  author: string;
  tags: string[];
  required_tools: string[];
  entry_content: string;
  size_bytes: number;
  file_count: number;
  status: SkillStatus;
  created_by: string | null;
  created_at: string;
  updated_at: string;
  // 列表项附加字段
  agent_count?: number;
  in_use?: boolean;
}

export interface SkillFileMeta {
  path: string;
  size: number;
  sha256: string;
}

export interface SkillDetail {
  skill: Skill;
  files: SkillFileMeta[];
}

export interface SkillAgentBinding {
  agent_id: string;
  agent_name: string;
  bound_at: string;
}

export interface SkillUsage {
  agent_count: number;
  load_count_30d: number;
  last_used_at: string | null;
}

// Agent 绑定的技能视图 (含 required_tools 覆盖状态)
export interface BoundSkillView {
  id: string;
  name: string;
  version: number;
  description: string;
  status: SkillStatus;
  required_tools: string[];
  missing_tools: string[];
}

// ---------- 平台设置 ----------
// 平台名 + 平台图标 (icon 为 base64 data URL, 空串 = 使用内置默认图标)
// + 记忆语义检索向量模型 (memory_embed_model 空串 = 跟随 MEMORY_EMBED_MODEL 环境变量)
// + 记忆抽取/摘要模型 (memory_extract_model 空串 = 跟随 MEMORY_EXTRACT_MODEL 环境变量, 再空 = Agent 当前模型)
export interface PlatformSettings {
  name: string;
  icon: string;
  memory_embed_model?: string;
  memory_embed_model_effective?: string;
  memory_extract_model?: string;
  memory_extract_model_effective?: string;
  updated_at?: string;
}
