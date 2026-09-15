import { useCallback, useEffect, useState } from 'react';
import { App, AutoComplete, Button, Card, Divider, Form, Input, Progress, Space, Switch, Tag, Typography, Upload } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { UploadFile } from 'antd';
import type { KBBackfillTask, KBBackfillTaskStatus } from '@/types';
import { kbApi } from '@/api/kb';
import { platformApi } from '@/api/platform';
import { modelApi } from '@/api/model';
import { getErrorMessage } from '@/api/client';
import { usePlatformStore } from '@/store/platform-store';

// 与后端校验规则保持一致
const ICON_ACCEPT = 'image/png,image/jpeg,image/svg+xml,image/webp,image/gif';
const ICON_MAX_SIZE = 1024 * 1024; // 1 MB
const NAME_MAX = 64;

// 向量回填任务状态展示 (M11.5)
const BACKFILL_TASK_STATUS: Record<KBBackfillTaskStatus, { label: string; color: string }> = {
  pending: { label: '排队中', color: 'default' },
  running: { label: '运行中', color: 'processing' },
  succeeded: { label: '成功', color: 'success' },
  failed: { label: '失败', color: 'error' },
  cancelled: { label: '已取消', color: 'warning' },
};

// 平台设置: 平台名 + 平台图标 (登录页与侧边导航展示) + 记忆语义检索向量模型 + 知识库 (M11: 总开关 / 向量 / 重排 / 总结模型, 运行时生效, 免重启) + 向量回填任务 (M11.5: 异步任务/状态机/单飞/取消), 需 platform:manage 权限
export function PlatformSettingsPage() {
  const { message } = App.useApp();
  const {
    name,
    icon,
    memoryEmbedModel,
    memoryEmbedModelEffective,
    memoryExtractModel,
    memoryExtractModelEffective,
    kbEnabled,
    kbEnabledEffective,
    kbEmbedModel,
    kbEmbedModelEffective,
    kbRerankModel,
    kbRerankModelEffective,
    kbSummaryModel,
    kbSummaryModelEffective,
    loaded,
    updatedAt,
    fetchPlatform,
    setPlatform,
    setModelSettings,
    setKBSettings,
  } = usePlatformStore();
  const [form] = Form.useForm<{
    name: string;
    memory_embed_model?: string;
    memory_extract_model?: string;
    kb_enabled?: boolean;
    kb_embed_model?: string;
    kb_rerank_model?: string;
    kb_summary_model?: string;
  }>();
  const [iconData, setIconData] = useState('');
  const [fileList, setFileList] = useState<UploadFile[]>([]);
  const [modelOptions, setModelOptions] = useState<{ value: string; label: string }[]>([]);
  const [saving, setSaving] = useState(false);
  const [activeTask, setActiveTask] = useState<KBBackfillTask | null>(null); // 活动任务 (pending/running)
  const [recentTasks, setRecentTasks] = useState<KBBackfillTask[]>([]); // 最近任务 (5 条)
  // M11.5 事项 4: 未向量化目标统计 (存在未向量化块/条目时提示回填)
  const [unembedded, setUnembedded] = useState<{ n: number; unit: 'chunk' | 'document' } | null>(null);
  const [startingTask, setStartingTask] = useState(false);

  useEffect(() => {
    fetchPlatform();
  }, [fetchPlatform]);

  // 向量模型下拉候选: 模型模板名称 (无 model:read 权限或拉取失败时保持手输可用)
  useEffect(() => {
    modelApi
      .list({ page: 1, size: 100 })
      .then((res) => {
        const items = res.data?.items ?? [];
        setModelOptions(items.map((t) => ({ value: t.name, label: `${t.name} (${t.model})` })));
      })
      .catch(() => {
        // 忽略: 下拉为空仍可手输模板名
      });
  }, []);

  // 拉取成功后回填表单
  useEffect(() => {
    if (!loaded) return;
    form.setFieldsValue({
      name,
      memory_embed_model: memoryEmbedModel || '',
      memory_extract_model: memoryExtractModel || '',
      kb_enabled: kbEnabled ?? kbEnabledEffective,
      kb_embed_model: kbEmbedModel || '',
      kb_rerank_model: kbRerankModel || '',
      kb_summary_model: kbSummaryModel || '',
    });
    setIconData(icon);
    setFileList(icon ? [{ uid: '-1', name: 'icon', status: 'done', url: icon }] : []);
  }, [loaded, name, icon, memoryEmbedModel, memoryExtractModel, kbEnabled, kbEnabledEffective, kbEmbedModel, kbRerankModel, kbSummaryModel, form]);

  const onBeforeUpload = (file: File) => {
    if (!ICON_ACCEPT.split(',').includes(file.type)) {
      message.error('仅支持 PNG / JPG / SVG / WebP / GIF 图片');
      return Upload.LIST_IGNORE;
    }
    if (file.size > ICON_MAX_SIZE) {
      message.error('图标大小不能超过 1MB');
      return Upload.LIST_IGNORE;
    }
    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = String(reader.result || '');
      setIconData(dataUrl);
      setFileList([{ uid: '-1', name: file.name, status: 'done', url: dataUrl }]);
    };
    reader.readAsDataURL(file);
    return false; // 不实际上传, 仅转为 data URL 随表单提交
  };

  const onRemoveIcon = () => {
    setIconData('');
    setFileList([]);
    return true;
  };

  const onSubmit = async () => {
    const values = await form.validateFields();
    setSaving(true);
    try {
      const res = await platformApi.update({
        name: values.name.trim(),
        icon: iconData,
        memory_embed_model: (values.memory_embed_model ?? '').trim(),
        memory_extract_model: (values.memory_extract_model ?? '').trim(),
        kb_enabled: values.kb_enabled ?? true,
        kb_embed_model: (values.kb_embed_model ?? '').trim(),
        kb_rerank_model: (values.kb_rerank_model ?? '').trim(),
        kb_summary_model: (values.kb_summary_model ?? '').trim(),
      });
      if (res.data) {
        setPlatform(res.data.name, res.data.icon || '', res.data.updated_at);
        setModelSettings(
          res.data.memory_embed_model || '',
          res.data.memory_embed_model_effective || '',
          res.data.memory_extract_model || '',
          res.data.memory_extract_model_effective || '',
        );
        setKBSettings(
          res.data.kb_enabled ?? null,
          res.data.kb_enabled_effective ?? true,
          res.data.kb_embed_model || '',
          res.data.kb_embed_model_effective || '',
          res.data.kb_rerank_model || '',
          res.data.kb_rerank_model_effective || '',
          res.data.kb_summary_model || '',
          res.data.kb_summary_model_effective || '',
        );
      }
      message.success('平台设置已保存, 模型设置已即时生效');
    } catch (err) {
      message.error(getErrorMessage(err, '保存失败'));
    } finally {
      setSaving(false);
    }
  };

  // 向量回填任务 (M11.5): 状态持久化, 单飞 + 批边界取消 + 重启恢复; 需已配置 embedding 模型
  const refreshTasks = useCallback(async () => {
    try {
      const res = await kbApi.listBackfillTasks();
      const items = res.data?.items ?? [];
      setRecentTasks(items.slice(0, 5));
      setActiveTask(items.find((t) => t.status === 'pending' || t.status === 'running') ?? null);
    } catch {
      // 轮询失败忽略, 不阻断设置页
    }
  }, []);

  useEffect(() => {
    refreshTasks();
  }, [refreshTasks]);

  // M11.5: 未向量化统计 (已配置 embedding 模型时拉取; 任务结束/模型变更时刷新)
  useEffect(() => {
    if (!kbEmbedModelEffective) {
      setUnembedded(null);
      return;
    }
    let cancelled = false;
    kbApi
      .getBackfillStatus()
      .then((res) => {
        if (!cancelled) setUnembedded({ n: res.data?.unembedded ?? 0, unit: res.data?.unit ?? 'document' });
      })
      .catch(() => {
        if (!cancelled) setUnembedded(null);
      });
    return () => {
      cancelled = true;
    };
  }, [kbEmbedModelEffective, activeTask?.id]);

  // 活动任务期间 2s 轮询进庨
  useEffect(() => {
    if (!activeTask) return;
    const timer = setInterval(refreshTasks, 2000);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTask?.id, refreshTasks]);

  const onStartBackfill = async () => {
    setStartingTask(true);
    try {
      await kbApi.startBackfillTask();
      message.success('回填任务已启动');
      await refreshTasks();
    } catch (err) {
      message.error(getErrorMessage(err, '启动回填任务失败'));
    } finally {
      setStartingTask(false);
    }
  };

  const onCancelTask = async (task: KBBackfillTask) => {
    try {
      await kbApi.cancelBackfillTask(task.id);
      message.success('已提交取消 (批边界生效)');
      await refreshTasks();
    } catch (err) {
      message.error(getErrorMessage(err, '取消任务失败'));
    }
  };

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <Space direction="vertical" size={0}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            平台设置
          </Typography.Title>
          <Typography.Text type="secondary">平台名与图标将展示在登录页、侧边导航及浏览器标签页</Typography.Text>
        </Space>
      </div>

      <Card>
        <Form form={form} layout="vertical" style={{ maxWidth: 560 }} requiredMark={false}>
          <Form.Item
            name="name"
            label="平台名称"
            rules={[
              { required: true, message: '请输入平台名称' },
              { max: NAME_MAX, message: `平台名称不能超过 ${NAME_MAX} 个字符` },
            ]}
          >
            <Input placeholder="如 Agent 管理平台" maxLength={NAME_MAX} showCount />
          </Form.Item>

          <Form.Item label="平台图标">
            <Upload
              listType="picture-card"
              accept={ICON_ACCEPT}
              maxCount={1}
              fileList={fileList}
              beforeUpload={onBeforeUpload}
              onRemove={onRemoveIcon}
            >
              {fileList.length === 0 && (
                <div>
                  <PlusOutlined />
                  <div style={{ marginTop: 8 }}>上传图标</div>
                </div>
              )}
            </Upload>
            <Typography.Text type="secondary" style={{ display: 'block', marginTop: 8 }}>
              PNG / JPG / SVG / WebP / GIF, 大小不超过 1MB; 不上传则使用内置默认图标
            </Typography.Text>
          </Form.Item>

          <Form.Item
            name="memory_embed_model"
            label="记忆语义检索模型 (Embedding)"
            extra={
              <>
                向量专用模型模板名称 (OpenAI 兼容 /embeddings 端点), 保存后即时生效无需重启;
                留空则跟随环境变量 MEMORY_EMBED_MODEL。
                {memoryEmbedModelEffective && (
                  <span> 当前生效: {memoryEmbedModelEffective}</span>
                )}
                {!memoryEmbedModel && !memoryEmbedModelEffective && (
                  <span> 未配置时语义检索不生效 (纯关键词检索)</span>
                )}
              </>
            }
          >
            <AutoComplete
              allowClear
              options={modelOptions}
              placeholder="跟随环境变量 MEMORY_EMBED_MODEL"
            />
          </Form.Item>

          <Form.Item
            name="memory_extract_model"
            label="记忆抽取 / 会话摘要模型 (Extract)"
            extra={
              <>
                记忆自动抽取与会话滚动摘要使用的模型模板名称, 保存后即时生效无需重启;
                留空则跟随环境变量 MEMORY_EXTRACT_MODEL。
                {memoryExtractModelEffective ? (
                  <span> 当前生效: {memoryExtractModelEffective}</span>
                ) : (
                  <span> 当前使用 Agent 各自配置的模型</span>
                )}
              </>
            }
          >
            <AutoComplete
              allowClear
              options={modelOptions}
              placeholder="跟随环境变量 MEMORY_EXTRACT_MODEL (空则用 Agent 当前模型)"
            />
          </Form.Item>

          <Divider orientation="left" plain>
            知识库
          </Divider>

          <Form.Item
            name="kb_enabled"
            label="知识库总开关"
            valuePropName="checked"
            extra={
              <>
                关闭后所有 Agent 不注入知识库参考段、不注册 search_knowledge 工具 (管理端 CRUD 不受影响);
                留空默认时跟随环境变量 KB_ENABLED。当前生效: {kbEnabledEffective ? '开' : '关'}。
              </>
            }
          >
            <Switch />
          </Form.Item>

          <Form.Item
            name="kb_embed_model"
            label="知识库向量模型 (Embedding)"
            extra={
              <>
                向量专用模型模板名称 (OpenAI 兼容 /embeddings 端点, 输出维度须等于 KB_VECTOR_DIM),
                保存时探测校验维度, 不匹配明确报错且保留原值; 留空则跟随环境变量 KB_EMBED_MODEL。
                {kbEmbedModelEffective ? (
                  <span> 当前生效: {kbEmbedModelEffective}</span>
                ) : (
                  <span> 未配置时不启用向量召回 (走关键词召回路径)</span>
                )}
              </>
            }
          >
            <AutoComplete
              allowClear
              options={modelOptions}
              placeholder="跟随环境变量 KB_EMBED_MODEL"
            />
          </Form.Item>

          <Form.Item
            name="kb_rerank_model"
            label="知识库重排模型 (Rerank)"
            extra={
              <>
                重排模型模板名称 (vLLM / Xinference 兼容 /rerank 端点), 保存时真实探测连通性,
                不通过明确报错且保留原值; 留空则跟随环境变量 KB_RERANK_MODEL。
                {kbRerankModelEffective ? (
                  <span> 当前生效: {kbRerankModelEffective}</span>
                ) : (
                  <span> 未配置时按召回序排序 (不重排)</span>
                )}
              </>
            }
          >
            <AutoComplete
              allowClear
              options={modelOptions}
              placeholder="跟随环境变量 KB_RERANK_MODEL"
            />
          </Form.Item>

          <Form.Item
            name="kb_summary_model"
            label="知识库总结模型 (Summary)"
            extra={
              <>
                对话一键总结入库使用的模型模板名称, 保存后即时生效无需重启。
                {kbSummaryModelEffective ? (
                  <span> 当前生效: {kbSummaryModelEffective}</span>
                ) : (
                  <span> 留空时使用 Agent 各自配置的模型</span>
                )}
              </>
            }
          >
            <AutoComplete
              allowClear
              options={modelOptions}
              placeholder="留空 = 使用 Agent 当前模型"
            />
          </Form.Item>

          <Form.Item style={{ marginBottom: 0 }}>
            <Space direction="vertical" size="small" style={{ width: '100%' }}>
              <Space wrap>
                <Button
                  loading={startingTask}
                  onClick={onStartBackfill}
                  disabled={!kbEmbedModelEffective || activeTask !== null}
                  style={{ marginRight: 8 }}
                >
                  启动回填任务
                </Button>
                <Button type="primary" loading={saving} onClick={onSubmit}>
                  保存
                </Button>
                {updatedAt && (
                  <Typography.Text type="secondary">最近更新: {updatedAt}</Typography.Text>
                )}
              </Space>
              {!activeTask && unembedded && unembedded.n > 0 && (
                <Typography.Text type="warning" style={{ fontSize: 12 }}>
                  存在 {unembedded.n} 个未向量化{unembedded.unit === 'chunk' ? '块' : '条目'}, 建议启动回填任务
                </Typography.Text>
              )}
              {activeTask && (
                <Space direction="vertical" size={4} style={{ width: '100%' }}>
                  <Progress
                    percent={activeTask.total > 0 ? Math.round((activeTask.done / activeTask.total) * 100) : 0}
                    status="active"
                    format={() =>
                      `${activeTask.done}/${activeTask.total}` +
                      (activeTask.failed > 0 ? `, 失败 ${activeTask.failed}` : '')
                    }
                  />
                  <Space>
                    <Tag color="processing">{BACKFILL_TASK_STATUS[activeTask.status].label}</Tag>
                    {activeTask.last_error && <Typography.Text type="danger">{activeTask.last_error}</Typography.Text>}
                    <Button size="small" danger onClick={() => onCancelTask(activeTask)}>
                      取消任务
                    </Button>
                  </Space>
                </Space>
              )}
              {recentTasks.length > 0 && (
                <div>
                  <Typography.Text type="secondary">最近回填任务</Typography.Text>
                  {recentTasks.map((t) => (
                    <div key={t.id} style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 4 }}>
                      <Tag color={BACKFILL_TASK_STATUS[t.status].color}>{BACKFILL_TASK_STATUS[t.status].label}</Tag>
                      <span style={{ width: 96 }}>{t.done}/{t.total}</span>
                      {t.last_error && (
                        <Typography.Text type="danger" ellipsis style={{ flex: 1 }}>
                          {t.last_error}
                        </Typography.Text>
                      )}
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                        {t.created_at}
                      </Typography.Text>
                      {(t.status === 'running' || t.status === 'pending') && (
                        <Button size="small" danger onClick={() => onCancelTask(t)}>
                          取消
                        </Button>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
}
