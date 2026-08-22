import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { App, Button, Card, Checkbox, Divider, Form, Input, InputNumber, Select, Space, Tag } from 'antd';
import { ArrowLeftOutlined, SaveOutlined } from '@ant-design/icons';
import { modelApi } from '@/api/model';
import { getErrorMessage } from '@/api/client';
import { MODEL_PROVIDER_MAP } from '@/utils/constants';
import type { ModelGenConfig, ModelProvider, ModelTemplate } from '@/types';

interface ModelFormValues {
  name: string;
  provider: ModelProvider;
  model: string;
  endpoint?: string;
  api_key?: string;
  priority?: number;
  tags: string[];
  temperature?: number;
  max_tokens?: number;
  top_p?: number;
  clear_api_key?: boolean;
}

// openai/anthropic/google 官方端点固定, 不展示编辑
const FIXED_ENDPOINT_PROVIDERS: ModelProvider[] = ['openai', 'anthropic', 'google'];

export function ModelFormPage() {
  const { id } = useParams<{ id: string }>();
  const isEdit = Boolean(id);
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [form] = Form.useForm<ModelFormValues>();
  const [submitting, setSubmitting] = useState(false);
  const [loading, setLoading] = useState(isEdit);
  const [existing, setExisting] = useState<ModelTemplate | null>(null);
  const [credView, setCredView] = useState<{ api_key_set: boolean; api_key_mask?: string } | null>(null);

  useEffect(() => {
    if (!isEdit || !id) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await modelApi.getById(id);
        if (cancelled) return;
        const template = res.data?.template;
        setExisting(template ?? null);
        setCredView(res.data?.credentials ?? null);
        if (template) {
          form.setFieldsValue({
            name: template.name,
            provider: template.provider,
            model: template.model,
            endpoint: template.endpoint,
            priority: template.priority,
            tags: template.tags ?? [],
            temperature: template.config?.temperature,
            max_tokens: template.config?.max_tokens,
            top_p: template.config?.top_p,
          });
        }
      } catch (err) {
        if (!cancelled) message.error(getErrorMessage(err, '加载模型失败'));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id, isEdit, form, message]);

  // 切换提供商时自动填充默认端点
  const onProviderChange = (provider: ModelProvider) => {
    const fixed = FIXED_ENDPOINT_PROVIDERS.includes(provider);
    if (fixed && !isEdit) {
      form.setFieldValue('endpoint', MODEL_PROVIDER_MAP[provider].defaultEndpoint);
    }
    form.setFieldValue('endpoint', fixed ? MODEL_PROVIDER_MAP[provider].defaultEndpoint : form.getFieldValue('endpoint'));
  };

  const onSubmit = async (values: ModelFormValues) => {
    setSubmitting(true);
    try {
      const config: ModelGenConfig = {};
      if (values.temperature !== undefined && values.temperature !== null) config.temperature = values.temperature;
      if (values.max_tokens !== undefined && values.max_tokens !== null) config.max_tokens = values.max_tokens;
      if (values.top_p !== undefined && values.top_p !== null) config.top_p = values.top_p;

      const payload = {
        name: values.name,
        provider: values.provider,
        model: values.model,
        endpoint: values.endpoint,
        api_key: values.api_key,
        priority: values.priority ?? 100,
        tags: values.tags,
        config: Object.keys(config).length > 0 ? config : undefined,
        clear_api_key: values.clear_api_key,
      };

      if (isEdit && id) {
        await modelApi.update(id, payload);
        message.success('已保存，连接参数变化将自动重新检测');
      } else {
        await modelApi.create(payload);
        message.success('已注册，连通性检测已完成');
      }
      navigate('/models');
    } catch (err) {
      message.error(getErrorMessage(err, isEdit ? '保存失败' : '注册失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const provider = Form.useWatch('provider', form);
  const fixedEndpoint = provider ? FIXED_ENDPOINT_PROVIDERS.includes(provider) : false;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>{isEdit ? '编辑模型' : '注册模型'}</h2>
        <Link to="/models">
          <Button icon={<ArrowLeftOutlined />}>返回</Button>
        </Link>
      </div>

      <Card loading={loading} style={{ maxWidth: 760 }}>
        <Form form={form} layout="vertical" onFinish={onSubmit} requiredMark={false} initialValues={{ provider: 'openai', priority: 100, tags: [] }}>
          <Form.Item name="name" label="名称" rules={[{ required: true, min: 2, max: 64, message: '2-64 个字符' }]}>
            <Input placeholder="全局唯一, 如 gpt-4o-prod" />
          </Form.Item>
          <Form.Item
            name="provider"
            label="提供商"
            rules={[{ required: true }]}
            tooltip={provider ? MODEL_PROVIDER_MAP[provider]?.hint : undefined}
          >
            <Select
              options={(Object.keys(MODEL_PROVIDER_MAP) as ModelProvider[]).map((key) => ({
                value: key,
                label: MODEL_PROVIDER_MAP[key].label,
              }))}
              onChange={onProviderChange}
            />
          </Form.Item>
          <Form.Item name="model" label="模型名称" rules={[{ required: true, message: '请输入模型名称' }]}>
            <Input placeholder="如 gpt-4o / claude-3-5-sonnet" />
          </Form.Item>
          <Form.Item
            name="endpoint"
            label="API 端点"
            tooltip="openai/anthropic/google 使用官方端点; azure/custom 需填写基础端点 (探测 GET {endpoint}/models)"
            rules={[
              {
                required: provider === 'azure' || provider === 'custom',
                message: 'azure/custom 提供商必须填写端点',
              },
            ]}
          >
            <Input
              placeholder={
                provider === 'azure'
                  ? 'https://<resource>.openai.azure.com'
                  : provider === 'custom'
                    ? '如 http://localhost:9101/v1'
                    : MODEL_PROVIDER_MAP[provider ?? 'openai']?.defaultEndpoint
              }
              disabled={fixedEndpoint}
            />
          </Form.Item>
          <Form.Item name="priority" label="路由优先级" tooltip="数值越小优先级越高; 高优先级模型异常或配额耗尽时自动切换到低优先级">
            <InputNumber min={0} max={9999} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="tags" label="标签" tooltip="用于分组过滤">
            <Select mode="tags" placeholder="输入后回车添加" />
          </Form.Item>

          <Divider style={{ margin: '8px 0 16px' }}>生成参数</Divider>
          <Space size={16} wrap>
            <Form.Item name="temperature" label="temperature" tooltip="0-2" style={{ marginBottom: 8 }}>
              <InputNumber min={0} max={2} step={0.1} placeholder="如 0.7" />
            </Form.Item>
            <Form.Item name="max_tokens" label="max_tokens" style={{ marginBottom: 8 }}>
              <InputNumber min={1} max={128000} placeholder="如 2048" />
            </Form.Item>
            <Form.Item name="top_p" label="top_p" tooltip="0-1" style={{ marginBottom: 8 }}>
              <InputNumber min={0} max={1} step={0.05} placeholder="如 1" />
            </Form.Item>
          </Space>

          <Divider style={{ margin: '8px 0 16px' }}>认证凭证 (AES-256 加密存储, API 永不回显明文)</Divider>
          {isEdit && credView && (
            <Form.Item>
              <Space size={4} wrap>
                <span style={{ color: 'var(--color-text-secondary)' }}>当前凭证:</span>
                {credView.api_key_set ? (
                  <Tag color="blue">api_key: {credView.api_key_mask}</Tag>
                ) : (
                  <Tag>未配置</Tag>
                )}
              </Space>
            </Form.Item>
          )}
          <Form.Item
            name="api_key"
            label="API Key"
            tooltip={isEdit ? '留空表示保持不变' : '如 sk-xxxx'}
          >
            <Input.Password placeholder={isEdit ? '留空保持不变, 填写则轮换' : '如 sk-xxxx'} />
          </Form.Item>
          {isEdit && (
            <Form.Item name="clear_api_key" valuePropName="checked">
              <Checkbox>清空已有 API Key</Checkbox>
            </Form.Item>
          )}
          <Space>
            <Button type="primary" htmlType="submit" icon={<SaveOutlined />} loading={submitting}>
              {isEdit ? '保存' : '注册'}
            </Button>
            <Link to={isEdit && existing ? `/models/${existing.id}` : '/models'}>
              <Button>取消</Button>
            </Link>
          </Space>
        </Form>
      </Card>
    </div>
  );
}