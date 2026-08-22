import apiClient, { type ApiEnvelope } from './client';
import type { MeResult, Paginated, PermissionItem, RoleItem, UserAdmin } from '@/types';

// 用户管理 (user:manage 权限)
export const userApi = {
  list: (params: { q?: string; status?: number; page?: number; size?: number }) =>
    apiClient.get<ApiEnvelope<Paginated<UserAdmin>>>('/users', { params }),

  create: (data: { username: string; email?: string; password: string; roles?: string[] }) =>
    apiClient.post<ApiEnvelope<{ id: string; username: string }>>('/users', data),

  get: (id: string) => apiClient.get<ApiEnvelope<UserAdmin>>(`/users/${id}`),

  update: (id: string, data: { email?: string; status?: number; password?: string }) =>
    apiClient.put<ApiEnvelope<{ id: string; username: string }>>(`/users/${id}`, data),

  assignRoles: (id: string, roles: string[]) =>
    apiClient.put<ApiEnvelope<{ roles: string[] }>>(`/users/${id}/roles`, { roles }),

  remove: (id: string) => apiClient.delete<ApiEnvelope<null>>(`/users/${id}`),
};

// 角色管理 (role:manage 权限)
export const roleApi = {
  list: () => apiClient.get<ApiEnvelope<{ items: RoleItem[] }>>('/roles'),

  create: (data: { name: string; description?: string; permissions?: string[] }) =>
    apiClient.post<ApiEnvelope<RoleItem>>('/roles', data),

  update: (id: string, data: { description?: string; status?: number; permissions?: string[] }) =>
    apiClient.put<ApiEnvelope<RoleItem>>(`/roles/${id}`, data),

  remove: (id: string) => apiClient.delete<ApiEnvelope<null>>(`/roles/${id}`),
};

// 权限点定义
export const permissionApi = {
  list: () => apiClient.get<ApiEnvelope<{ items: PermissionItem[] }>>('/permissions'),
};

// 当前登录用户 (含角色与权限码)
export const meApi = {
  me: () => apiClient.get<ApiEnvelope<MeResult>>('/auth/me'),
};
