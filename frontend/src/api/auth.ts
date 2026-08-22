import apiClient from './client';
import type { ApiEnvelope } from './client';
import type { LoginResult } from '@/types';

export const authApi = {
  login: (username: string, password: string) =>
    apiClient.post<ApiEnvelope<LoginResult>>('/auth/login', { username, password }),

  register: (username: string, email: string, password: string) =>
    apiClient.post<ApiEnvelope<{ id: string; username: string; email: string }>>('/auth/register', {
      username,
      email,
      password,
    }),

  logout: () => apiClient.post<ApiEnvelope<null>>('/auth/logout'),
};