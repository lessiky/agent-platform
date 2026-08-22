import apiClient from './client';
import type { ApiEnvelope } from './client';
import type { OverviewSummary } from '@/types';

// 概览页 (基本情况) 统计
export const overviewApi = {
  summary: () => apiClient.get<ApiEnvelope<OverviewSummary>>('/overview/summary'),
};
