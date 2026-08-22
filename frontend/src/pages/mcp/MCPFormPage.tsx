import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { App, Button, Card, Checkbox, Divider, Form, Input, Select, Space, Tag } from 'antd';
import { ArrowLeftOutlined, MinusCircleOutlined, PlusOutlined, SaveOutlined } from '@ant-design/icons';
import { mcpApi } from '@/api/mcp';
import { getErrorMessage } from '@/api/client';
import { MCP_TRANSPORT_MAP } from '@/utils/constants';
import type { MCPCredentials, MCPServer, MCPTransport } from '@/types';

interface HeaderRow {
  key: string;
  value: string;
}

interface MCPFormValues {
  name: string;
  endpoint: string;
  transport: MCPTransport;
  description?: string;
  tags: string[];
  api_key?: string;
  headers: HeaderRow[];
  clear_credentials?: boolean;
}

export function MCPFormPage() {
  const { id } = useParams<{ id: string }>();
  const isEdit = Boolean(id);
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [form] = Form.useForm<MCPFormValues>();
  const [submitting, setSubmitting] = useState(false);
  const [loading, setLoading] = useState(isEdit);
  const [existing, setExisting] = useState<MCPServer | null>(null);
  const [credView, setCredView] = useState<{ api_key_set: boolean; api_key_mask?: string; header_keys: string[] } | null>(null);

  useEffect(() => {
    if (!isEdit || !id) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await mcpApi.getById(id);
        if (cancelled) return;
        const server = res.data?.server;
        setExisting(server ?? null);
        setCredView(res.data?.credentials ?? null);
        if (server) {
          form.setFieldsValue({
            name: server.name,
            endpoint: server.endpoint,
            transport: server.transport,
            description: server.description,
            tags: server.tags ?? [],
            headers: [],
          });
        }
      } catch (err) {
        if (!cancelled) message.error(getErrorMessage(err, '加载 MCP 失败'));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id, isEdit, form, message]);

  const onSubmit = async (values: MCPFormValues) => {
    setSubmitting(true);
    try {
      const headers: Record<string, string> = {};
      for (const row of values.headers ?? []) {
        if (row.key?.trim()) headers[row.key.trim()] = row.value ?? '';
      }
      const credentials: MCPCredentials | undefined =
        values.clear_credentials || values.api_key || Object.keys(headers).length > 0
          ? { api_key: values.api_key, headers: Object.keys(headers).length > 0 ? headers : undefined }
          : undefined;

      const payload = {
        name: values.name,
        endpoint: values.endpoint,
        transport: values.transport,
        description: values.description,
        tags: values.tags,
        credentials,
      };

      if (isEdit && id) {
        await mcpApi.update(id, payload);
        message.success('已保存，连接参数变化将自动重新检测');
      } else {
        await mcpApi.create(payload);
        message.success('已注册，连通性检测与工具发现已完成');
      }
      navigate('/mcp');
    } catch (err) {
      message.error(getErrorMessage(err, isEdit ? '保存失败' : '注册失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const isStdio = Form.useWatch('transport', form);

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>{isEdit ? '编辑 MCP' : '注册 MCP'}</h2>
        <Link to="/mcp">
          <Button icon={<ArrowLeftOutlined />}>返回</Button>
        </Link>
      </div>

      <Card loading={loading} style={{ maxWidth: 760 }}>
        <Form form={form} layout="vertical" onFinish={onSubmit} requiredMark={false} initialValues={{ transport: 'http', headers: [] }}>
          <Form.Item name="name" label="名称" rules={[{ required: true, min: 2, max: 64, message: '2-64 个字符' }]}>
            <Input placeholder="全局唯一, 如 kb-mcp" />
          </Form.Item>
          <Form.Item
            name="transport"
            label="传输类型"
            rules={[{ required: true }]}
            tooltip={MCP_TRANSPORT_MAP[isStdio ?? 'http']?.hint}
          >
            <Select
              options={(Object.keys(MCP_TRANSPORT_MAP) as MCPTransport[]).map((key) => ({
                value: key,
                label: MCP_TRANSPORT_MAP[key].label,
              }))}
            />
          </Form.Item>
          <Form.Item
            name="endpoint"
            label="端点"
            rules={[
              { required: true, message: '请输入端点' },
              isStdio === 'stdio'
                ? {}
                : {
                    pattern: /^https?:\/\//,
                    message: 'http/sse 传输需要 http(s):// 端点',
                  },
            ]}
          >
            <Input
              placeholder={
                isStdio === 'stdio' ? '本地命令, 如 npx @modelcontextprotocol/server-xxx' : '如 http://localhost:3000/mcp'
              }
            />
          </Form.Item>
          <Form.Item name="description" label="描述" rules={[{ max: 512 }]}>
            <Input.TextArea rows={2} placeholder="MCP 服务器的用途说明" />
          </Form.Item>
          <Form.Item name="tags" label="标签" tooltip="用于分组过滤">
            <Select mode="tags" placeholder="输入后回车添加" />
          </Form.Item>

          <Divider style={{ margin: '8px 0 16px' }}>认证凭证 (AES-256 加密存储, API 永不回显明文)</Divider>

          {isEdit && credView && (
            <Form.Item>
              <Space size={4} wrap>
                <span style={{ color: 'var(--color-text-secondary)' }}>当前凭证:</span>
                {credView.api_key_set ? (
                  <Tag color="blue">api_key: {credView.api_key_mask}</Tag>
                ) : (
                  <Tag>无 api_key</Tag>
                )}
                {credView.header_keys.map((key) => (
                  <Tag key={key}>header: {key}</Tag>
                ))}
                {!credView.api_key_set && credView.header_keys.length === 0 && <Tag>未配置凭证</Tag>}
              </Space>
            </Form.Item>
          )}

          <Form.Item
            name="api_key"
            label="API Key"
            tooltip={isEdit ? '留空表示保持不变' : '作为 Authorization: Bearer <key> 发送'}
          >
            <Input.Password placeholder={isEdit ? '留空保持不变, 填写则轮换' : '如 sk-xxxx'} />
          </Form.Item>
          <Form.Item label="自定义请求头">
            <Form.List name="headers">
              {(fields, { add, remove }) => (
                <>
                  {fields.map((field) => (
                    <Space key={field.key} style={{ display: 'flex', marginBottom: 8 }} align="baseline">
                      <Form.Item name={[field.name, 'key']} rules={[{ required: fields.length > 0, message: '必填' }]} noStyle>
                        <Input placeholder="Header 名称, 如 X-Api-Token" style={{ width: 220 }} />
                      </Form.Item>
                      <Form.Item name={[field.name, 'value']} noStyle>
                        <Input placeholder="值" style={{ width: 260 }} />
                      </Form.Item>
                      <MinusCircleOutlined onClick={() => remove(field.name)} />
                    </Space>
                  ))}
                  <Button type="dashed" onClick={() => add({ key: '', value: '' })} icon={<PlusOutlined />} block>
                    添加请求头
                  </Button>
                </>
              )}
            </Form.List>
          </Form.Item>
          {isEdit && (
            <Form.Item name="clear_credentials" valuePropName="checked">
              <Checkbox>清空已有凭证 (不填新值)</Checkbox>
            </Form.Item>
          )}
          {isStdio === 'stdio' && (
            <div style={{ color: '#faad14', marginBottom: 16 }}>
              stdio 传输在 Phase 1 平台仅允许注册, 不支持连通性检测与工具调用。
            </div>
          )}
          <Space>
            <Button type="primary" htmlType="submit" icon={<SaveOutlined />} loading={submitting}>
              {isEdit ? '保存' : '注册'}
            </Button>
            <Link to={isEdit && existing ? `/mcp/${existing.id}` : '/mcp'}>
              <Button>取消</Button>
            </Link>
          </Space>
        </Form>
      </Card>
    </div>
  );
}