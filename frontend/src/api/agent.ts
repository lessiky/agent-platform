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

  deleteSession: (id: string, sessionId: string) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(`agents/${id}/sessions/${sessionId}`),
};
