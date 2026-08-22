import apiClient from './client';
import type { ApiEnvelope } from './client';
import type { Paginated, Skill, SkillAgentBinding, SkillDetail, SkillUsage } from '@/types';

// 技能管理 API (M9, PRD 6.5)
export const skillApi = {
  list: (params: { q?: string; status?: string; tag?: string; page?: number; size?: number }) =>
    apiClient.get<ApiEnvelope<Paginated<Skill>>>('/skills', { params }),

  getById: (id: string) => apiClient.get<ApiEnvelope<SkillDetail>>(`/skills/${id}`),

  // 导入技能包 (multipart: file + force); force=true 同名升级
  import: (file: File, force = false) => {
    const form = new FormData();
    form.append('file', file);
    form.append('force', String(force));
    return apiClient.post<ApiEnvelope<Skill>>('/skills/import', form, {
      headers: { 'Content-Type': 'multipart/form-data' },
    });
  },

  updateStatus: (id: string, status: 'active' | 'disabled') =>
    apiClient.put<ApiEnvelope<Skill>>(`/skills/${id}`, { status }),

  // 删除; force=true 级联解绑 (409 时响应 data.agents 为关联列表)
  remove: (id: string, force = false) =>
    apiClient.delete<ApiEnvelope<{ deleted: boolean }>>(`/skills/${id}`, { params: { force } }),

  // 资源文件内容 (二进制原样, 非信封)
  getFile: (id: string, path: string) =>
    apiClient.get<Blob>(`/skills/${id}/files/${path.split('/').map(encodeURIComponent).join('/')}`, {
      responseType: 'blob',
    }),

  listAgents: (id: string) =>
    apiClient.get<ApiEnvelope<{ agents: SkillAgentBinding[] }>>(`/skills/${id}/agents`),

  usage: (id: string) => apiClient.get<ApiEnvelope<SkillUsage>>(`/skills/${id}/usage`),
};