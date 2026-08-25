import apiClient, { type ApiEnvelope } from './client';
import type { PlatformSettings } from '@/types';

// 平台设置: 读取公开 (登录页/侧边导航展示品牌信息), 更新需 platform:manage 权限
export const platformApi = {
  get: () => apiClient.get<ApiEnvelope<PlatformSettings>>('/platform/settings'),

  update: (data: { name: string; icon: string }) =>
    apiClient.put<ApiEnvelope<PlatformSettings>>('/platform/settings', data),
};
