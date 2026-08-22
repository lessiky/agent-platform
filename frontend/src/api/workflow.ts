import apiClient from './client';
import type { ApiEnvelope } from './client';

// ---------- 类型 ----------

export interface WorkflowNodeDef {
  id: string;
  type: 'agent' | 'mcp_tool' | 'http' | 'delay' | 'condition';
  name?: string;
  config?: Record<string, unknown>;
  retry?: { max_attempts?: number; interval_seconds?: number; backoff?: 'fixed' | 'exponential' };
  timeout_seconds?: number;
}

export interface WorkflowEdgeDef {
  id: string;
  source: string;
  target: string;
  condition?: 'true' | 'false';
}

export interface WorkflowDefinition {
  version: number;
  nodes: WorkflowNodeDef[];
  edges: WorkflowEdgeDef[];
}

export interface WorkflowSchedule {
  cron: string;
  input?: Record<string, unknown>;
  timezone?: string;
}

export interface Workflow {
  id: string;
  name: string;
  description: string;
  definition: WorkflowDefinition;
  status: 'draft' | 'active' | 'archived';
  input_schema?: unknown;
  output_schema?: unknown;
  version: number;
  schedule?: WorkflowSchedule | null;
  schedule_enabled: boolean;
  webhook_token: string;
  created_at: string;
  updated_at: string;
}

// AI 生成工作流结果 (M5 Phase 2, POST /workflows/ai-generate)
export interface AIGenerateResult {
  name: string;
  description: string;
  definition: WorkflowDefinition;
  input_schema?: Record<string, unknown>;
  model?: string;
  model_id?: string;
  model_name?: string;
  attempts?: number;
  total_tokens?: number;
}

export type ExecutionStatus = 'running' | 'waiting_approval' | 'success' | 'failed' | 'cancelled';

export interface WorkflowExecution {
  id: string;
  workflow_id: string;
  workflow_name: string;
  workflow_version: number;
  trigger_type: 'manual' | 'cron' | 'webhook';
  status: ExecutionStatus;
  input?: unknown;
  output?: unknown;
  trace_id: string;
  error?: string;
  started_at: string;
  finished_at?: string;
}

export interface WorkflowNodeExecution {
  id: string;
  execution_id: string;
  node_id: string;
  node_type: string;
  node_name: string;
  status: 'pending' | 'running' | 'success' | 'failed' | 'skipped' | 'waiting_approval' | 'cancelled';
  attempt: number;
  input?: unknown;
  output?: unknown;
  error?: string;
  approval_id?: string;
  duration_ms: number;
  started_at?: string;
  finished_at?: string;
}

export interface ExecutionDetail extends WorkflowExecution {
  nodes: WorkflowNodeExecution[];
}

export interface WorkflowVersion {
  id: string;
  workflow_id: string;
  version: number;
  definition: WorkflowDefinition;
  created_at: string;
}

export interface DashboardData {
  counts_by_status: Record<string, number>;
  running: number;
  waiting_approval: number;
  success: number;
  failed: number;
  cancelled: number;
  recent: WorkflowExecution[];
}

// ---------- API ----------

export const workflowApi = {
  list: (params?: { page?: number; size?: number; status?: string }) =>
    apiClient.get<ApiEnvelope<{ items: Workflow[]; total: number }>>('/workflows', { params }),
  get: (id: string) => apiClient.get<ApiEnvelope<Workflow>>(`/workflows/${id}`),
  create: (data: {
    name: string;
    description?: string;
    definition: WorkflowDefinition;
    input_schema?: unknown;
  }) => apiClient.post<ApiEnvelope<Workflow>>('/workflows', data),
  update: (id: string, data: {
    name?: string;
    description?: string;
    definition?: WorkflowDefinition;
  }) => apiClient.put<ApiEnvelope<Workflow>>(`/workflows/${id}`, data),
  remove: (id: string) => apiClient.delete<ApiEnvelope<{ deleted: string }>>(`/workflows/${id}`),
  validate: (definition: WorkflowDefinition) =>
    apiClient.post<ApiEnvelope<{ valid: boolean }>>('/workflows/validate', { definition }),
  // AI 自动生成: 自然语言描述 -> 校验通过的 DAG 草稿 (不落库); LLM 生成耗时较长, 单独放宽超时
  aiGenerate: (data: { description: string }) =>
    apiClient.post<ApiEnvelope<AIGenerateResult>>('/workflows/ai-generate', data, { timeout: 180000 }),
  activate: (id: string) => apiClient.post<ApiEnvelope<Workflow>>(`/workflows/${id}/activate`),
  archive: (id: string) => apiClient.post<ApiEnvelope<Workflow>>(`/workflows/${id}/archive`),
  updateSchedule: (id: string, data: { enabled: boolean; cron?: string; input?: Record<string, unknown> }) =>
    apiClient.put<ApiEnvelope<Workflow>>(`/workflows/${id}/schedule`, data),
  trigger: (id: string, input?: Record<string, unknown>) =>
    apiClient.post<ApiEnvelope<WorkflowExecution>>(`/workflows/${id}/trigger`, { input: input ?? {} }),
  versions: (id: string) => apiClient.get<ApiEnvelope<{ items: WorkflowVersion[] }>>(`/workflows/${id}/versions`),
  executions: (workflowId: string, params?: { page?: number; size?: number; status?: string }) =>
    apiClient.get<ApiEnvelope<{ items: WorkflowExecution[]; total: number }>>(`/workflows/${workflowId}/executions`, { params }),
  getExecution: (id: string) => apiClient.get<ApiEnvelope<ExecutionDetail>>(`/workflow-executions/${id}`),
  cancelExecution: (id: string) => apiClient.post<ApiEnvelope<{ cancelled: string }>>(`/workflow-executions/${id}/cancel`),
  dashboard: () => apiClient.get<ApiEnvelope<DashboardData>>('/workflows/dashboard'),
};