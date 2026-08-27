import { useCallback, useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Divider,
  Form,
  InputNumber,
  Row,
  Space,
  Statistic,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { ArrowLeftOutlined, EditOutlined, MessageOutlined, ReloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { modelApi } from '@/api/model';
import { getErrorMessage } from '@/api/client';
import { ModelStatusTag } from '@/components/common/StatusTag';
import { MODEL_PROVIDER_MAP } from '@/utils/constants';
import { formatDateTime, formatNumber } from '@/utils/format';
import type { ModelHealthData, ModelQuota, ModelTemplate, ModelUsageLog, RouteResult } from '@/types';

const REFRESH_INTERVAL = 5000;

interface QuotaFormValues {
  daily_limit?: number;
  monthly_limit?: number;
  daily_token_limit?: number;
  monthly_token_limit?: number;
}

export function ModelDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { message, modal } = App.useApp();
  const [template, setTemplate] = useState<ModelTemplate | null>(null);
  const [credentials, setCredentials] = useState<{ api_key_set: boolean; api_key_mask?: string } | null>(null);
  const [health, setHealth] = useState<ModelHealthData | null>(null);
  const [usage, setUsage] = useState<{ quota: ModelQuota | null; logs: ModelUsageLog[] } | null>(null);
  const [loading, setLoading] = useState(true);
  const [testing, setTesting] = useState(false);
  const [sayingHi, setSayingHi] = useState(false);
  const [routeResult, setRouteResult] = useState<RouteResult | null>(null);
  const [routing, setRouting] = useState(false);
  const [quotaForm] = Form.useForm<QuotaFormValues>();

  const load = useCallback(async () => {
    if (!id) return;
    try {
      const [templateRes, healthRes, usageRes] = await Promise.all([
        modelApi.getById(id),
        modelApi.getHealth(id, 100),
        modelApi.getUsage(id, 50),
      ]);
      setTemplate(templateRes.data?.template ?? null);
      setCredentials(templateRes.data?.credentials ?? null);
      setHealth(healthRes.data ?? null);
      setUsage(usageRes.data ?? null);
      if (usageRes.data?.quota && !quotaForm.isFieldsTouched()) {
        quotaForm.setFieldsValue({
          daily_limit: usageRes.data.quota.daily_limit,
          monthly_limit: usageRes.data.quota.monthly_limit,
          daily_token_limit: usageRes.data.quota.daily_token_limit,
          monthly_token_limit: usageRes.data.quota.monthly_token_limit,
        });
      }
    } catch (err) {
      if (template === null) message.error(getErrorMessage(err, '加载模型失败'));
    } finally {
      setLoading(false);
    }
  }, [id, message, quotaForm, template]);

  const runRoute = useCallback(async () => {
    setRouting(true);
    try {
      const res = await modelApi.route();
      setRouteResult(res.data ?? null);
    } catch (err) {
      message.error(getErrorMessage(err, '路由选择失败'));
    } finally {
      setRouting(false);
    }
  }, [message]);

  useEffect(() => {
    load();
    const timer = setInterval(load, REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [load]);

  useEffect(() => {
    runRoute();
  }, [runRoute]);

  const onTest = async () => {
    if (!id) return;
    setTesting(true);
    try {
      const res = await modelApi.test(id);
      const result = res.data;
      if (result?.ok) {
        message.success(`连接正常 (${result.latency_ms}ms, 可用模型 ${result.models_count} 个)`);
      } else {
        message.warning(`连接失败: ${result?.error || '未知错误'}`);
      }
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '连通性测试失败'));
    } finally {
      setTesting(false);
    }
  };

  const onSayHi = async () => {
    if (!id) return;
    setSayingHi(true);
    try {
      const res = await modelApi.sayHi(id);
      const result = res.data;
      if (result?.ok) {
        modal.success({
          title: '模型回复正常',
          width: 560,
          content: (
            <div>
              <pre
                style={{
                  whiteSpace: 'pre-wrap',
                  margin: 0,
                  padding: 12,
                  borderRadius: 6,
                  background: 'var(--color-fill-quaternary, #fafafa)',
                  maxHeight: 320,
                  overflow: 'auto',
                }}
              >
                {result.content || '(空回复)'}
              </pre>
              <div style={{ color: 'var(--color-text-secondary)', fontSize: 12, marginTop: 8 }}>
                延迟 {result.latency_ms}ms · tokens {result.total_tokens ?? '-'} · finish_reason{' '}
                {result.finish_reason || '-'}
                {result.model ? ` · 实际模型 ${result.model}` : ''}
              </div>
            </div>
          ),
        });
      } else {
        modal.error({
          title: '模型回复异常',
          width: 560,
          content: <div>{result?.error || '未知错误'}</div>,
        });
      }
    } catch (err) {
      message.error(getErrorMessage(err, '发送Hi消息失败'));
    } finally {
      setSayingHi(false);
    }
  };

  const onQuotaSave = async () => {
    if (!id) return;
    try {
      const values = await quotaForm.validateFields();
      await modelApi.updateQuota(id, {
        daily_limit: values.daily_limit ?? 0,
        monthly_limit: values.monthly_limit ?? 0,
        daily_token_limit: values.daily_token_limit ?? 0,
        monthly_token_limit: values.monthly_token_limit ?? 0,
      });
      message.success('配额已更新');
      load();
    } catch (err) {
      if (err && typeof err === 'object' && 'errorFields' in err) return; // 表单校验错误
      message.error(getErrorMessage(err, '配额更新失败'));
    }
  };

  const historyColumns: ColumnsType<ModelHealthData['history'][number]> = [
    { title: '时间', dataIndex: 'created_at', width: 180, render: (v: string) => formatDateTime(v) },
    {
      title: '结果',
      dataIndex: 'ok',
      width: 90,
      render: (ok: boolean) => <Tag color={ok ? 'green' : 'red'}>{ok ? '成功' : '失败'}</Tag>,
    },
    {
      title: '延迟',
      dataIndex: 'latency_ms',
      width: 100,
      render: (v: number) => `${v}ms`,
    },
    { title: '错误', dataIndex: 'error', ellipsis: true, render: (v: string) => v || '-' },
  ];

  const usageColumns: ColumnsType<ModelUsageLog> = [
    { title: '时间', dataIndex: 'created_at', width: 180, render: (v: string) => formatDateTime(v) },
    {
      title: '结果',
      dataIndex: 'ok',
      width: 90,
      render: (ok: boolean) => <Tag color={ok ? 'green' : 'red'}>{ok ? '成功' : '失败'}</Tag>,
    },
    {
      title: 'Tokens',
      dataIndex: 'tokens',
      width: 100,
      render: (v: number) => formatNumber(v),
    },
    {
      title: '延迟',
      dataIndex: 'latency_ms',
      width: 100,
      render: (v: number) => `${v}ms`,
    },
    { title: '来源 Agent', dataIndex: 'agent_id', ellipsis: true, render: (v: string | null) => v || '-' },
  ];

  const quota = usage?.quota ?? null;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>{template?.name ?? '模型详情'}</h2>
        <Space>
          <Link to="/models">
            <Button icon={<ArrowLeftOutlined />}>返回</Button>
          </Link>
          <Button
            icon={<ReloadOutlined spin={testing} />}
            loading={testing}
            onClick={onTest}
          >
            连通性测试
          </Button>
          <Button
            icon={<MessageOutlined spin={sayingHi} />}
            loading={sayingHi}
            onClick={onSayHi}
          >
            发送Hi消息
          </Button>
          {id && (
            <Link to={`/models/${id}/edit`}>
              <Button icon={<EditOutlined />} type="primary">
                编辑
              </Button>
            </Link>
          )}
        </Space>
      </div>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Card>
            <Statistic title="状态" valueRender={() => (template ? <ModelStatusTag status={template.status} /> : '-')} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="最近检测"
              value={health?.latency_ms !== null && health?.latency_ms !== undefined ? `${health.latency_ms}ms` : '-'}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="今日配额 (次数 / tokens)"
              valueRender={() =>
                quota ? (
                  <span>
                    <div>{formatNumber(quota.daily_used)} / {quota.daily_limit > 0 ? formatNumber(quota.daily_limit) : '不限'}</div>
                    <div style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>
                      tokens {formatNumber(quota.daily_token_used)} / {quota.daily_token_limit > 0 ? formatNumber(quota.daily_token_limit) : '不限'}
                    </div>
                  </span>
                ) : (
                  '未设置'
                )
              }
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="月度配额 (次数 / tokens)"
              valueRender={() =>
                quota ? (
                  <span>
                    <div>{formatNumber(quota.monthly_used)} / {quota.monthly_limit > 0 ? formatNumber(quota.monthly_limit) : '不限'}</div>
                    <div style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>
                      tokens {formatNumber(quota.monthly_token_used)} / {quota.monthly_token_limit > 0 ? formatNumber(quota.monthly_token_limit) : '不限'}
                    </div>
                  </span>
                ) : (
                  '未设置'
                )
              }
            />
          </Card>
        </Col>
      </Row>

      <Card loading={loading}>
        <Descriptions
          column={2}
          size="small"
          items={[
            { key: 'provider', label: '提供商', children: template ? (MODEL_PROVIDER_MAP[template.provider]?.label ?? template.provider) : '-' },
            { key: 'model', label: '模型名称', children: template ? <Typography.Text code>{template.model}</Typography.Text> : '-' },
            { key: 'endpoint', label: '端点', children: template?.endpoint || '官方默认端点' },
            { key: 'priority', label: '路由优先级', children: template ? formatNumber(template.priority) : '-' },
            {
              key: 'api_key',
              label: 'API Key',
              children: credentials?.api_key_set ? <Typography.Text code>{credentials.api_key_mask}</Typography.Text> : '未设置',
            },
            {
              key: 'tags',
              label: '标签',
              children: template && template.tags.length > 0 ? (
                template.tags.map((tag) => <Tag key={tag}>{tag}</Tag>)
              ) : (
                '-'
              ),
            },
            { key: 'created_at', label: '创建时间', children: template ? formatDateTime(template.created_at) : '-' },
            {
              key: 'config',
              label: '生成参数',
              children: template && template.config && Object.keys(template.config).length > 0 ? (
                <Typography.Text code style={{ fontSize: 12 }}>
                  {JSON.stringify(template.config)}
                </Typography.Text>
              ) : (
                '-'
              ),
            },
          ]}
        />

        <Tabs
          style={{ marginTop: 16 }}
          items={[
            {
              key: 'health',
              label: '健康历史',
              children: (
                <>
                  {template?.status === 'error' && template.last_error && (
                    <Tag color="red" style={{ marginBottom: 12 }}>
                      当前异常: {template.last_error}
                    </Tag>
                  )}
                  <Table
                    rowKey="id"
                    size="middle"
                    columns={historyColumns}
                    dataSource={health?.history ?? []}
                    pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 条` }}
                    locale={{ emptyText: '暂无检查记录 (每分钟自动检测一次)' }}
                  />
                </>
              ),
            },
            {
              key: 'quota',
              label: '配额与用量',
              forceRender: true,
              children: (
                <div>
                  <Form form={quotaForm} layout="inline" style={{ marginBottom: 12 }}>
                    <Form.Item name="daily_limit" label="每日调用限额" tooltip="0 = 不限">
                      <InputNumber min={0} style={{ width: 160 }} placeholder="0 不限" />
                    </Form.Item>
                    <Form.Item name="monthly_limit" label="每月调用限额" tooltip="0 = 不限">
                      <InputNumber min={0} style={{ width: 160 }} placeholder="0 不限" />
                    </Form.Item>
                    <Form.Item name="daily_token_limit" label="每日 Token 限额" tooltip="0 = 不限">
                      <InputNumber min={0} style={{ width: 160 }} placeholder="0 不限" />
                    </Form.Item>
                    <Form.Item name="monthly_token_limit" label="每月 Token 限额" tooltip="0 = 不限">
                      <InputNumber min={0} style={{ width: 160 }} placeholder="0 不限" />
                    </Form.Item>
                    <Form.Item>
                      <Button type="primary" onClick={onQuotaSave}>
                        保存配额
                      </Button>
                    </Form.Item>
                  </Form>
                  <div style={{ color: 'var(--color-text-secondary)', fontSize: 12, marginBottom: 16 }}>
                    配额耗尽后, 路由自动切换到低优先级模型 (故障转移)
                  </div>
                  <Divider style={{ margin: '8px 0 16px' }}>最近调用</Divider>
                  <Table
                    rowKey="id"
                    size="middle"
                    columns={usageColumns}
                    dataSource={usage?.logs ?? []}
                    pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 条` }}
                    locale={{ emptyText: '暂无调用记录 (Agent 运行时模拟流量会产生调用)' }}
                  />
                </div>
              ),
            },
            {
              key: 'route',
              label: '路由选择',
              children: (
                <div>
                  <div style={{ marginBottom: 12 }}>
                    <Button loading={routing} onClick={runRoute}>
                      试运行路由选择
                    </Button>
                    <span style={{ color: 'var(--color-text-secondary)', fontSize: 12, marginLeft: 12 }}>
                      按优先级选择首个可用模型 (跳过异常状态与配额耗尽者), 不消耗配额
                    </span>
                  </div>
                  {routeResult ? (
                    routeResult.selected ? (
                      <>
                        <Tag color="green" style={{ marginBottom: 12 }}>
                          选中: {routeResult.selected.name} ({routeResult.selected.model}, priority={routeResult.selected.priority})
                        </Tag>
                        <div style={{ color: 'var(--color-text-secondary)', fontSize: 12, marginBottom: 8 }}>
                          {routeResult.reason}
                        </div>
                      </>
                    ) : (
                      <Tag color="red" style={{ marginBottom: 12 }}>
                        未选中: {routeResult.reason}
                      </Tag>
                    )
                  ) : (
                    <div style={{ color: 'var(--color-text-secondary)', marginBottom: 12 }}>加载中...</div>
                  )}
                  {routeResult && routeResult.skipped && routeResult.skipped.length > 0 && (
                    <Table
                      rowKey="name"
                      size="small"
                      pagination={false}
                      columns={[
                        { title: '模板', dataIndex: 'name' },
                        { title: '模型', dataIndex: 'model' },
                        { title: '跳过原因', dataIndex: 'reason' },
                      ]}
                      dataSource={routeResult.skipped}
                    />
                  )}
                </div>
              ),
            },
          ]}
        />
      </Card>
    </div>
  );
}
