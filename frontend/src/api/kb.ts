import apiClient from './client';
import type { ApiEnvelope } from './client';
import type { KBCategory, KBBackfillTask, KBChunkView, KBDocument, KBSearchHit, Paginated } from '@/types';

// 知识库管理 API (M11, PRD 6.1)
export const kbApi = {
  // ---- 分类 (KB-1) ----
  listCategories: () => apiClient.get<ApiEnvelope<{ items: KBCategory[] }>>('/kb/categories'),

  // M11.5: parent_id 可选 (空 = 顶级; 须指向顶级分类)
  createCategory: (data: { name: string; description?: string; parent_id?: string }) =>
    apiClient.post<ApiEnvelope<KBCategory>>('/kb/categories', data),

  // M11.5: parent_id 缺省/JSON null = 不变; "" = 升顶级 (有子级时降级 400)
  updateCategory: (id: string, data: { name: string; description?: string; parent_id?: string | null }) =>
    apiClient.put<ApiEnvelope<KBCategory>>(`/kb/categories/${id}`, data),

  // 删除; 有子级分类 409 (kb_category_has_children) / 有条目时 409 (kb_category_in_use)
  removeCategory: (id: string) => apiClient.delete<ApiEnvelope<unknown>>(`/kb/categories/${id}`),

  // ---- 条目 (KB-1/KB-3) ----
  listDocuments: (params: {
    category_id?: string; // M11.5: 支持逗号分隔多分类 (顶级点选 = 父 + 子)
    keyword?: string;
    source?: string;
    status?: string;
    page?: number;
    page_size?: number;
  }) => apiClient.get<ApiEnvelope<Paginated<KBDocument>>>('/kb/documents', { params }),

  getDocument: (id: string) => apiClient.get<ApiEnvelope<KBDocument>>(`/kb/documents/${id}`),

  // M11.5 事项 4: 条目分块列表 (管理预览/调试; 未启用分块返回空)
  getDocumentChunks: (id: string) =>
    apiClient.get<ApiEnvelope<{ doc_id: string; chunk_count: number; chunks: KBChunkView[] }>>(
      `/kb/documents/${id}/chunks`,
    ),

  // source=chat_summary 时须携带 source_session_id / source_agent_id (服务端校验会话归属与 Agent 绑定)
  createDocument: (data: {
    category_id: string;
    title: string;
    content: string;
    source?: string;
    source_session_id?: string;
    source_agent_id?: string;
  }) => apiClient.post<ApiEnvelope<KBDocument>>('/kb/documents', data),

  updateDocument: (id: string, data: { title?: string; content?: string; category_id?: string }) =>
    apiClient.put<ApiEnvelope<KBDocument>>(`/kb/documents/${id}`, data),

  removeDocument: (id: string) => apiClient.delete<ApiEnvelope<unknown>>(`/kb/documents/${id}`),

  // 归档 / 恢复
  setDocumentStatus: (id: string, status: 'active' | 'archived') =>
    apiClient.patch<ApiEnvelope<KBDocument>>(`/kb/documents/${id}/status`, { status }),

  // ---- 试算检索 (UI 用) ----
  search: (params: { query: string; category_ids?: string; top_k?: number }) =>
    apiClient.get<ApiEnvelope<{ hits: KBSearchHit[] }>>('/kb/search', { params }),

  // ---- 向量回填 (M11.5: 后台任务; 旧同步端点已 410) ----
  // 启动任务 (单飞: 已有运行中任务 409, 返回运行中任务)
  startBackfillTask: () => apiClient.post<ApiEnvelope<KBBackfillTask>>('/kb/backfill-tasks'),

  // 最近任务列表 (20 条)
  listBackfillTasks: () =>
    apiClient.get<ApiEnvelope<{ items: KBBackfillTask[] }>>('/kb/backfill-tasks'),

  // 任务详情 + 进度
  getBackfillTask: (id: string) => apiClient.get<ApiEnvelope<KBBackfillTask>>(`/kb/backfill-tasks/${id}`),

  // 取消 (批边界协作取消)
  cancelBackfillTask: (id: string) =>
    apiClient.post<ApiEnvelope<KBBackfillTask>>(`/kb/backfill-tasks/${id}/cancel`),

  // M11.5: 未向量化目标统计 (平台设置「存在未向量化块时提示回填」; 块模式 unit=chunk)
  getBackfillStatus: () =>
    apiClient.get<ApiEnvelope<{ unembedded: number; unit: 'chunk' | 'document' }>>('/kb/backfill-status'),
};
