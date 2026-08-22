import apiClient from './client';
import type { ApiEnvelope } from './client';
import type {
  ApprovalView,
  CreateMCPRequest,
  MCPAgentBinding,
  MCPHealthData,
  MCPQuery,
  MCPServer,
  MCPTestResult,
  MCPTool,
  Paginated,
  ToolApprovalConfigItem,
  UpdateMCPRequest,
} from '@/types';

export const mcpApi = {
  list: (params: MCPQuery) =>
    apiClient.get<ApiEnvelope<Paginated<MCPServer>>>('/mcp-servers', { params }),

  getById: (id: string) =>
    apiClient.get<ApiEnvelope<{ server: MCPServer; credentials: { api_key_set: boolean; api_key_mask?: string; header_keys: string[] } }>>(`/mcp-servers/${id}`),

  create: (data: CreateMCPRequest) =>
    apiClient.post<ApiEnvelope<MCPServer>>('/mcp-servers', data),

  update: (id: string, data: UpdateMCPRequest) =>
    apiClient.put<ApiEnvelope<MCPServer>>(`/mcp-servers/${id}`, data),

  remove: (id: string) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(`/mcp-servers/${id}`),

  test: (id: string) =>
    apiClient.post<ApiEnvelope<MCPTestResult>>(`/mcp-servers/${id}/test`),

  getHealth: (id: string, limit = 100) =>
    apiClient.get<ApiEnvelope<MCPHealthData>>(`/mcp-servers/${id}/health`, { params: { limit } }),

  listTools: (id: string) =>
    apiClient.get<ApiEnvelope<{ tools: MCPTool[] }>>(`/mcp-servers/${id}/tools`),

  updateToolApprovals: (id: string, tools: ToolApprovalConfigItem[]) =>
    apiClient.put<ApiEnvelope<{ tools: MCPTool[] }>>(`/mcp-servers/${id}/tools`, { tools }),

  pendingCount: (id: string) =>
    apiClient.get<ApiEnvelope<Paginated<ApprovalView>>>('/approvals', {
      params: { mcp_server_id: id, status: 'pending', size: 1 },
    }),

  callTool: (id: string, name: string, arguments_: Record<string, unknown>) =>
    apiClient.post<ApiEnvelope<{ content: { type: string; text?: string }[]; is_error: boolean }>>(
      `/mcp-servers/${id}/tools/call`,
      { name, arguments: arguments_ }
    ),

  listAgents: (id: string) =>
    apiClient.get<ApiEnvelope<{ agents: MCPAgentBinding[] }>>(`/mcp-servers/${id}/agents`),

  bindAgent: (id: string, agentId: string) =>
    apiClient.post<ApiEnvelope<{ bound: boolean }>>(`/mcp-servers/${id}/agents`, { agent_id: agentId }),

  unbindAgent: (id: string, agentId: string) =>
    apiClient.delete<ApiEnvelope<{ unbound: boolean }>>(`/mcp-servers/${id}/agents/${agentId}`),
};