import type { AgentStatus, ApprovalSource, ApprovalStatus, InstanceStatus, LogLevel, MCPStatus, MCPTransport, ModelProvider, ModelStatus, SkillStatus, SkillsUsageMode } from '@/types';

// Agent 状态展示
export const AGENT_STATUS_MAP: Record<AgentStatus, { label: string; color: string }> = {
  idle: { label: '空闲', color: 'blue' },
  running: { label: '运行中', color: 'green' },
  stopped: { label: '已停止', color: 'orange' },
  error: { label: '异常', color: 'red' },
};

// 实例状态展示
export const INSTANCE_STATUS_MAP: Record<InstanceStatus, { label: string; color: string }> = {
  pending: { label: '启动中', color: 'blue' },
  running: { label: '运行中', color: 'green' },
  stopping: { label: '停止中', color: 'cyan' },
  stopped: { label: '已停止', color: 'orange' },
  error: { label: '异常', color: 'red' },
};

// 日志级别展示
export const LOG_LEVEL_MAP: Record<LogLevel, { label: string; color: string }> = {
  debug: { label: 'DEBUG', color: 'default' },
  info: { label: 'INFO', color: 'blue' },
  warn: { label: 'WARN', color: 'gold' },
  error: { label: 'ERROR', color: 'red' },
};
// 审核请求状态展示 (M4.5)
export const APPROVAL_STATUS_MAP: Record<ApprovalStatus, { label: string; color: string }> = {
  pending: { label: '待审核', color: 'orange' },
  approved: { label: '已通过', color: 'green' },
  rejected: { label: '已驳回', color: 'red' },
  expired: { label: '已超时', color: 'default' },
};

// 审核请求调用来源展示 (M4.5)
export const APPROVAL_SOURCE_MAP: Record<ApprovalSource, { label: string; color: string }> = {
  manual: { label: '手动调用', color: 'blue' },
  runtime: { label: '模拟流量', color: 'cyan' },
  api_invoke: { label: 'API 调用', color: 'purple' },
  workflow: { label: '工作流', color: 'geekblue' },
};

// MCP 服务器状态展示
export const MCP_STATUS_MAP: Record<MCPStatus, { label: string; color: string }> = {
  pending: { label: '未检测', color: 'default' },
  connected: { label: '已连接', color: 'green' },
  disconnected: { label: '断开', color: 'orange' },
  error: { label: '异常', color: 'red' },
};

// MCP 传输类型展示
export const MCP_TRANSPORT_MAP: Record<MCPTransport, { label: string; hint: string }> = {
  http: { label: 'HTTP', hint: 'Streamable HTTP (JSON-RPC over POST)' },
  sse: { label: 'SSE', hint: 'SSE 传输 (legacy 握手, 不可用时回退直连)' },
  stdio: { label: 'stdio', hint: '本地子进程 (Phase 1 平台仅允许注册, 不支持检测/调用)' },
};

// 模型模板状态展示
export const MODEL_STATUS_MAP: Record<ModelStatus, { label: string; color: string }> = {
  active: { label: '可用', color: 'green' },
  inactive: { label: '已停用', color: 'orange' },
  error: { label: '异常', color: 'red' },
};

// 模型提供商展示 (endpoint 默认值 + 提示)
export const MODEL_PROVIDER_MAP: Record<ModelProvider, { label: string; defaultEndpoint: string; hint: string }> = {
  openai: { label: 'OpenAI', defaultEndpoint: 'https://api.openai.com/v1', hint: '官方端点固定, API Key 必填' },
  anthropic: { label: 'Anthropic', defaultEndpoint: 'https://api.anthropic.com', hint: '官方端点固定, API Key 必填' },
  google: { label: 'Google', defaultEndpoint: 'https://generativelanguage.googleapis.com', hint: '官方端点固定, API Key 必填' },
  azure: { label: 'Azure OpenAI', defaultEndpoint: '', hint: '需填写资源端点 https://<resource>.openai.azure.com' },
  custom: { label: '自定义', defaultEndpoint: '', hint: 'OpenAI 兼容端点 (需实现 GET /models)' },
};

// 技能状态展示 (M9)
export const SKILL_STATUS_MAP: Record<SkillStatus, { label: string; color: string }> = {
  active: { label: '启用', color: 'green' },
  disabled: { label: '禁用', color: 'default' },
};

// 技能注入模式展示 (M9)
export const SKILL_USAGE_MODE_MAP: Record<SkillsUsageMode, { label: string; hint: string }> = {
  metadata_injection: { label: '渐进式披露', hint: '注入技能目录, 通过 load_skill 工具按需加载正文 (默认)' },
  full_injection: { label: '全量注入', hint: '所有技能正文直接注入系统提示词 (总长上限 128KB)' },
};

// 平台设置默认值 (与后端 DefaultPlatformName 保持一致, 拉取失败时兜底展示)
export const DEFAULT_PLATFORM_NAME = 'Agent 管理平台';
