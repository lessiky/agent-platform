import apiClient from './client';
import type { ApiEnvelope } from './client';
import type {
  CreateModelRequest,
  ModelHealthData,
  ModelQuery,
  ModelTemplate,
  ModelTestResult,
  ModelQuota,
  ModelUsageData,
  ModelUsageSummary,
  Paginated,
  RouteResult,
  UpdateModelRequest,
} from '@/types';

export const modelApi = {
  list: (params: ModelQuery) =>
    apiClient.get<ApiEnvelope<Paginated<ModelTemplate>>>('/model-templates', { params }),

  getById: (id: string) =>
    apiClient.get<ApiEnvelope<{ template: ModelTemplate; credentials: { api_key_set: boolean; api_key_mask?: string } }>>(`/model-templates/${id}`),

  create: (data: CreateModelRequest) =>
    apiClient.post<ApiEnvelope<{ template: ModelTemplate; credentials: { api_key_set: boolean; api_key_mask?: string } }>>('/model-templates', data),

  update: (id: string, data: UpdateModelRequest) =>
    apiClient.put<ApiEnvelope<{ template: ModelTemplate; credentials: { api_key_set: boolean; api_key_mask?: string } }>>(`/model-templates/${id}`, data),

  remove: (id: string) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(`/model-templates/${id}`),

  test: (id: string) =>
    apiClient.post<ApiEnvelope<ModelTestResult>>(`/model-templates/${id}/test`),

  getHealth: (id: string, limit = 100) =>
    apiClient.get<ApiEnvelope<ModelHealthData>>(`/model-templates/${id}/health`, { params: { limit } }),

  getUsage: (id: string, limit = 100) =>
    apiClient.get<ApiEnvelope<ModelUsageData>>(`/model-templates/${id}/usage`, { params: { limit } }),

  listQuota: () =>
    apiClient.get<ApiEnvelope<{ items: ModelQuota[] }>>('/model-quota'),

  updateQuota: (
    modelId: string,
    data: {
      daily_limit?: number;
      monthly_limit?: number;
      daily_token_limit?: number;
      monthly_token_limit?: number;
    },
  ) =>
    apiClient.put<ApiEnvelope<ModelQuota>>(`/model-quota/${modelId}`, data),

  usageSummary: () =>
    apiClient.get<ApiEnvelope<{ items: ModelUsageSummary[] }>>('/model-usage'),

  route: () =>
    apiClient.post<ApiEnvelope<RouteResult>>('/models/route'),
};