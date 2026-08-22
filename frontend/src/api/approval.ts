import apiClient from './client';
import type { ApiEnvelope } from './client';
import type { ApprovalQuery, ApprovalSettings, ApprovalView, Paginated } from '@/types';

export const approvalApi = {
  list: (params: ApprovalQuery) =>
    apiClient.get<ApiEnvelope<Paginated<ApprovalView>>>('/approvals', { params }),

  getById: (id: string) =>
    apiClient.get<ApiEnvelope<ApprovalView>>(`/approvals/${id}`),

  approve: (id: string, comment?: string) =>
    apiClient.post<ApiEnvelope<ApprovalView>>(`/approvals/${id}/approve`, { comment }),

  reject: (id: string, comment?: string) =>
    apiClient.post<ApiEnvelope<ApprovalView>>(`/approvals/${id}/reject`, { comment }),

  getSettings: () => apiClient.get<ApiEnvelope<ApprovalSettings>>('/approvals/settings'),

  updateSettings: (data: { default_timeout_minutes?: number; on_timeout?: 'reject' | 'approve' }) =>
    apiClient.put<ApiEnvelope<ApprovalSettings>>('/approvals/settings', data),
};