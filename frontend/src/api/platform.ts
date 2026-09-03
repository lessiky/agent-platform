import apiClient, { type ApiEnvelope } from './client';
import type { PlatformSettings } from '@/types';

// 平台设置: 读取公开 (登录页/侧边导航展示品牌信息), 更新需 platform:manage 权限
export const platformApi = {
  get: () => apiClient.get<ApiEnvelope<PlatformSettings>>('/platform/settings'),

  // memory_embed_model / memory_extract_model: 空串 = 跟随对应环境变量
  update: (data: {
    name: string;
    icon: string;
    memory_embed_model?: string;
    memory_extract_model?: string;
  }) =>
    apiClient.put<ApiEnvelope<PlatformSettings>>('/platform/settings', data),
};
