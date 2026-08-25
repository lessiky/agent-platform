import { create } from 'zustand';
import { platformApi } from '@/api/platform';
import { DEFAULT_PLATFORM_NAME } from '@/utils/constants';

interface PlatformState {
  name: string;
  icon: string; // base64 data URL, 空串 = 内置默认图标
  updatedAt: string | null; // 最近更新时间 (展示用)
  loaded: boolean;
  // 拉取平台设置 (公开端点, 登录页与主布局均可调用; 失败时保持默认名兜底)
  fetchPlatform: () => Promise<void>;
  // 保存成功后同步本地状态
  setPlatform: (name: string, icon: string, updatedAt?: string) => void;
}

export const usePlatformStore = create<PlatformState>((set, get) => ({
  name: DEFAULT_PLATFORM_NAME,
  icon: '',
  updatedAt: null,
  loaded: false,
  fetchPlatform: async () => {
    if (get().loaded) return;
    try {
      const res = await platformApi.get();
      if (res.data?.name) {
        set({
          name: res.data.name,
          icon: res.data.icon || '',
          updatedAt: res.data.updated_at || null,
          loaded: true,
        });
        applyTitle(res.data.name);
      }
    } catch {
      // 拉取失败不影响页面渲染, 使用默认平台名
    }
  },
  setPlatform: (name, icon, updatedAt) => {
    set({ name, icon, updatedAt: updatedAt ?? null, loaded: true });
    applyTitle(name);
  },
}));

// 浏览器标签页标题跟随平台名
function applyTitle(name: string) {
  if (name) document.title = name;
}
