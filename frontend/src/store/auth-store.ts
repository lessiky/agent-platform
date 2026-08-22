import { create } from 'zustand';
import { meApi } from '@/api/rbac';

// 与后端拦截器约定的 localStorage key 保持一致
const TOKEN_KEY = 'access_token';
const USERNAME_KEY = 'username';

interface AuthState {
  token: string | null;
  username: string | null;
  roles: string[];
  permissions: string[];
  // 是否已拉取 /auth/me (角色与权限)
  meLoaded: boolean;
  setAuth: (token: string, username: string) => void;
  fetchMe: () => Promise<void>;
  logout: () => void;
}

export const useAuthStore = create<AuthState>((set, get) => ({
  token: localStorage.getItem(TOKEN_KEY),
  username: localStorage.getItem(USERNAME_KEY),
  roles: [],
  permissions: [],
  meLoaded: false,
  setAuth: (token, username) => {
    localStorage.setItem(TOKEN_KEY, token);
    localStorage.setItem(USERNAME_KEY, username);
    // 重新登录后角色/权限需重新拉取
    set({ token, username, roles: [], permissions: [], meLoaded: false });
  },
  fetchMe: async () => {
    const { token, meLoaded } = get();
    if (!token || meLoaded) return;
    try {
      const res = await meApi.me();
      if (res.data) {
        set({
          roles: res.data.roles || [],
          permissions: res.data.permissions || [],
          meLoaded: true,
        });
      }
    } catch (error) {
      // 401 由 axios 拦截器统一处理跳转; 其他错误保持空权限 (fail closed)
      console.warn('fetchMe failed:', error);
    }
  },
  logout: () => {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(USERNAME_KEY);
    set({ token: null, username: null, roles: [], permissions: [], meLoaded: false });
  },
}));

// 是否拥有指定权限 (供组件/菜单做按钮级控制)
export function useHasPermission() {
  const permissions = useAuthStore((s) => s.permissions);
  return (code: string) => permissions.includes(code);
}
