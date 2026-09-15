import apiClient from './client';
import type { ApiEnvelope } from './client';
import type { Paginated } from '@/types';
import type { Agent } from '@/types';
import type { Workflow } from './workflow';

export interface ReviewRequest {
  decision: 'approve' | 'reject';
  comment?: string;
}

export const reviewApi = {
  // 审核队列 (type=agent 需 agent:review, type=workflow 需 workflow:review)
  pendingAgents: (params?: { page?: number; size?: number }) =>
    apiClient.get<ApiEnvelope<Paginated<Agent>>>('/reviews/pending', { params: { type: 'agent', ...params } }),
  pendingWorkflows: (params?: { page?: number; size?: number }) =>
    apiClient.get<ApiEnvelope<Paginated<Workflow>>>('/reviews/pending', { params: { type: 'workflow', ...params } }),
  reviewAgent: (id: string, data: ReviewRequest) =>
    apiClient.post<ApiEnvelope<Agent>>(`/agents/${id}/review`, data),
  reviewWorkflow: (id: string, data: ReviewRequest) =>
    apiClient.post<ApiEnvelope<Workflow>>(`/workflows/${id}/review`, data),
};