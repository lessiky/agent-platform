import apiClient from './client';
import type { ApiEnvelope } from './client';
import type {
  Agent,
  AgentAPIKey,
  AgentBoundMCP,
  AgentInstance,
  AgentLog,
  AgentMetrics,
  AgentQuery,
  AgentVersion,
  BoundSkillView,
  ChatMessage,
  ChatResult,
  ChatSession,
  CreateAgentRequest,
  DashboardData,
  LogQuery,
  Memory,
  MemoryListResult,
  Paginated,
} from '@/types';

export const agentApi = {
  list: (params: AgentQuery) =>
    apiClient.get<ApiEnvelope<Paginated<Agent>>>('/agents', { params }),

  getById: (id: string) =>
    apiClient.get<ApiEnvelope<{ agent: Agent; instance: AgentInstance | null }>>(`/agents/${id}`),

  create: (data: CreateAgentRequest) =>
    apiClient.post<ApiEnvelope<Agent>>('/agents', data),

  update: (id: string, data: CreateAgentRequest) =>
    apiClient.put<ApiEnvelope<Agent>>(`/agents/${id}`, data),

  remove: (id: string) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(`/agents/${id}`),

  start: (id: string) =>
    apiClient.post<ApiEnvelope<AgentInstance>>(`/agents/${id}/start`),

  stop: (id: string) =>
    apiClient.post<ApiEnvelope<AgentInstance>>(`/agents/${id}/stop`),

  getMetrics: (id: string, params?: { from?: string; to?: string }) =>
    apiClient.get<ApiEnvelope<AgentMetrics>>(`/agents/${id}/metrics`, { params }),

  getLogs: (id: string, params: LogQuery) =>
    apiClient.get<ApiEnvelope<Paginated<AgentLog>>>(`/agents/${id}/logs`, { params }),

  listBoundMCPS: (id: string) =>
    apiClient.get<ApiEnvelope<{ items: AgentBoundMCP[] }>>(`/agents/${id}/mcps`),

  // 技能关联 (M9)
  listBoundSkills: (id: string) =>
    apiClient.get<ApiEnvelope<{ skills: BoundSkillView[] }>>(`/agents/${id}/skills`),

  updateSkills: (id: string, skills: string[]) =>
    apiClient.put<ApiEnvelope<{ skills: BoundSkillView[] }>>(`/agents/${id}/skills`, { skills }),

  listVersions: (id: string) =>
    apiClient.get<ApiEnvelope<{ items: AgentVersion[] }>>(`/agents/${id}/versions`),

  rollback: (id: string, version: number) =>
    apiClient.post<ApiEnvelope<Agent>>(`/agents/${id}/rollback`, { version }),

  createKey: (id: string, name: string, expiresAt?: string) =>
    apiClient.post<ApiEnvelope<{ key: string; api_key: AgentAPIKey }>>(`/agents/${id}/keys`, {
      name,
      ...(expiresAt ? { expires_at: expiresAt } : {}),
    }),

  listKeys: (id: string) =>
    apiClient.get<ApiEnvelope<{ items: AgentAPIKey[] }>>(`/agents/${id}/keys`),

  revokeKey: (id: string, keyId: string) =>
    apiClient.delete<ApiEnvelope<{ revoked: boolean }>>(`/agents/${id}/keys/${keyId}`),

  deleteKey: (id: string, keyId: string) =>
    apiClient.post<ApiEnvelope<{ deleted: boolean }>>(`/agents/${id}/keys/${keyId}/delete`),

  getDashboard: () =>
    apiClient.get<ApiEnvelope<DashboardData>>('/agents/dashboard'),

  // M2.5 chat (模型生成 + 工具轮次耗时较长, 单独放宽, 避免全局 30s 提前掐断)
  chat: (id: string, data: { session_id?: string; message: string }) =>
    apiClient.post<ApiEnvelope<ChatResult>>(`agents/${id}/chat`, data, { timeout: 300000 }),

  listSessions: (id: string, params?: { page?: number; size?: number }) =>
    apiClient.get<ApiEnvelope<{ items: ChatSession[]; total: number }>>(`agents/${id}/sessions`, { params }),

  getSession: (id: string, sessionId: string) =>
    apiClient.get<ApiEnvelope<{ session: ChatSession; messages: ChatMessage[] }>>(`agents/${id}/sessions/${sessionId}`),

  renameSession: (id: string, sessionId: string, title: string) =>
    apiClient.put<ApiEnvelope<ChatSession>>(`agents/${id}/sessions/${sessionId}`, { title }),

  deleteSession: (id: string, sessionId: string) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(`agents/${id}/sessions/${sessionId}`),

  // 记忆 (M10): 列表/详情/显式添加/更新/删除 (scope=mine 默认, agent 仅 Agent 级, all 仅 admin)
  listMemories: (id: string, params?: { kind?: string; status?: string; scope?: string; page?: number; size?: number }) =>
    apiClient.get<ApiEnvelope<MemoryListResult>>(`agents/${id}/memories`, { params }),

  getMemory: (id: string, memoryId: string) =>
    apiClient.get<ApiEnvelope<Memory>>(`agents/${id}/memories/${memoryId}`),

  createMemory: (id: string, data: { content: string; kind?: string; scope?: string }) =>
    apiClient.post<ApiEnvelope<Memory>>(`agents/${id}/memories`, data),

  updateMemory: (id: string, memoryId: string, data: { content?: string; kind?: string; status?: string }) =>
    apiClient.patch<ApiEnvelope<Memory>>(`agents/${id}/memories/${memoryId}`, data),

  deleteMemory: (id: string, memoryId: string) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(`agents/${id}/memories/${memoryId}`),
};

// ---------------------------------------------------------------------------
// M2.5 chat 流式 (SSE, 2026-08-24): POST body 与 chat 相同, 响应为 text/event-stream
// EventSource 不支持 POST, 故用 fetch + ReadableStream 解析
// ---------------------------------------------------------------------------

export type ChatStreamEventType =
  | 'turn_start'
  | 'model_round'
  | 'thinking_delta'
  | 'tool_start'
  | 'tool_end'
  | 'final'
  | 'error';

export interface ChatStreamEventPayload {
  type: ChatStreamEventType;
  data: Record<string, unknown>;
}

export interface ChatStreamHandlers {
  onEvent?: (evt: ChatStreamEventPayload) => void;
  signal?: AbortSignal;
}

// SSE 流式对话: 执行过程实时回调阶段事件, 完成后 resolve 最终 ChatResult;
// error 事件 / 非 2xx / 中断时 reject (中断为 DOMException AbortError)
export async function chatStream(
  id: string,
  body: { session_id?: string; message: string; show_thinking?: boolean },
  handlers: ChatStreamHandlers = {},
): Promise<ChatResult> {
  const base = import.meta.env.VITE_API_BASE_URL || '/api/v1';
  const token = localStorage.getItem('access_token');
  const resp = await fetch(`${base}/agents/${id}/chat/stream`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: 'Bearer ' + token } : {}),
    },
    body: JSON.stringify(body),
    signal: handlers.signal,
  });

  if (!resp.ok) {
    let msg = `HTTP ${resp.status}`;
    try {
      const envelope = (await resp.json()) as ApiEnvelope;
      if (envelope?.message) msg = envelope.message;
    } catch {
      // 忽略非 JSON 错误体
    }
    if (resp.status === 401) {
      localStorage.removeItem('access_token');
      localStorage.removeItem('username');
      if (window.location.pathname !== '/login') window.location.href = '/login';
      throw new Error('登录已过期');
    }
    throw new Error(msg);
  }
  if (!resp.body) {
    throw new Error('当前浏览器不支持流式响应');
  }

  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  let result: ChatResult | null = null;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let sep: number;
    while ((sep = buf.indexOf('\n\n')) !== -1) {
      const frame = buf.slice(0, sep);
      buf = buf.slice(sep + 2);
      let eventType = 'message';
      const dataLines: string[] = [];
      for (const line of frame.split('\n')) {
        if (line.startsWith('event:')) eventType = line.slice(6).trim();
        else if (line.startsWith('data:')) dataLines.push(line.slice(5).trim());
      }
      if (dataLines.length === 0) continue; // keepalive 注释帧
      let data: Record<string, unknown>;
      try {
        data = JSON.parse(dataLines.join('\n'));
      } catch {
        continue;
      }
      const evt: ChatStreamEventPayload = {
        type: eventType as ChatStreamEventType,
        data,
      };
      handlers.onEvent?.(evt);
      if (eventType === 'final') result = data as unknown as ChatResult;
      if (eventType === 'error') throw new Error(String(data?.message ?? '执行失败'));
    }
  }
  if (!result) throw new Error('连接中断, 未收到最终结果');
  return result;
}
