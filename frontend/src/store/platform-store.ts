import { create } from 'zustand';
import { platformApi } from '@/api/platform';
import { DEFAULT_PLATFORM_NAME } from '@/utils/constants';

interface PlatformState {
  name: string;
  icon: string; // base64 data URL, 空串 = 内置默认图标
  memoryEmbedModel: string; // 记忆语义检索向量模型 (平台设置值, 空串 = 跟随 MEMORY_EMBED_MODEL 环境变量)
  memoryEmbedModelEffective: string; // 当前生效向量模型 (平台设置优先, 空时回退环境变量)
  memoryExtractModel: string; // 记忆抽取/摘要模型 (平台设置值, 空串 = 跟随 MEMORY_EXTRACT_MODEL 环境变量, 再空 = Agent 当前模型)
  memoryExtractModelEffective: string; // 当前生效抽取/摘要模型 (平台设置优先, 空时回退环境变量, 再空 = Agent 当前模型)
  updatedAt: string | null; // 最近更新时间 (展示用)
  loaded: boolean;
  // 拉取平台设置 (公开端点, 登录页与主布局均可调用; 失败时保持默认名兜底)
  fetchPlatform: () => Promise<void>;
  // 保存成功后同步本地状态
  setPlatform: (name: string, icon: string, updatedAt?: string) => void;
  // 保存模型设置后同步本地状态 (向量 + 抽取/摘要)
  setModelSettings: (
    memoryEmbedModel: string,
    memoryEmbedModelEffective: string,
    memoryExtractModel: string,
    memoryExtractModelEffective: string,
  ) => void;
}

export const usePlatformStore = create<PlatformState>((set, get) => ({
  name: DEFAULT_PLATFORM_NAME,
  icon: '',
  memoryEmbedModel: '',
  memoryEmbedModelEffective: '',
  memoryExtractModel: '',
  memoryExtractModelEffective: '',
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
          memoryEmbedModel: res.data.memory_embed_model || '',
          memoryEmbedModelEffective: res.data.memory_embed_model_effective || '',
          memoryExtractModel: res.data.memory_extract_model || '',
          memoryExtractModelEffective: res.data.memory_extract_model_effective || '',
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
  setModelSettings: (
    memoryEmbedModel,
    memoryEmbedModelEffective,
    memoryExtractModel,
    memoryExtractModelEffective,
  ) => {
    set({
      memoryEmbedModel,
      memoryEmbedModelEffective,
      memoryExtractModel,
      memoryExtractModelEffective,
    });
  },
}));

// 浏览器标签页标题跟随平台名
function applyTitle(name: string) {
  if (name) document.title = name;
}
