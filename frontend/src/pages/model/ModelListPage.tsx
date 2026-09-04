import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { App, Button, Card, Input, Popconfirm, Select, Space, Table, Tag, Tooltip } from 'antd';
import { PlusOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { modelApi } from '@/api/model';
import { getErrorMessage } from '@/api/client';
import { ModelStatusTag } from '@/components/common/StatusTag';
import { MODEL_PROVIDER_MAP, MODEL_STATUS_MAP } from '@/utils/constants';
import { formatDateTime, formatNumber, timeAgo } from '@/utils/format';
import type { ModelProvider, ModelStatus, ModelTemplate } from '@/types';

const REFRESH_INTERVAL = 10000; // 列表 10s 轮询
const STATUS_OPTIONS = (Object.keys(MODEL_STATUS_MAP) as ModelStatus[]).map((key) => ({
  value: key,
  label: MODEL_STATUS_MAP[key].label,
}));
const PROVIDER_OPTIONS = (Object.keys(MODEL_PROVIDER_MAP) as ModelProvider[]).map((key) => ({
  value: key,
  label: MODEL_PROVIDER_MAP[key].label,
}));

export function ModelListPage() {
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [templates, setTemplates] = useState<ModelTemplate[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [keyword, setKeyword] = useState('');
  const [debouncedKeyword, setDebouncedKeyword] = useState('');
  const [status, setStatus] = useState<ModelStatus | undefined>(undefined);
  const [provider, setProvider] = useState<ModelProvider | undefined>(undefined);
  const [testingId, setTestingId] = useState<string | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout>>();

  // 搜索防抖
  useEffect(() => {
    timerRef.current = setTimeout(() => setDebouncedKeyword(keyword.trim()), 400);
    return () => clearTimeout(timerRef.current);
  }, [keyword]);

  const load = useCallback(async () => {
    try {
      const res = await modelApi.list({ q: debouncedKeyword || undefined, status, provider, page, size });
      setTemplates(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载模型列表失败'));
    } finally {
      setLoading(false);
    }
  }, [debouncedKeyword, status, provider, page, size, message]);

  useEffect(() => {
    setLoading(true);
    load();
    const timer = setInterval(load, REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [load]);

  const onTest = async (template: ModelTemplate) => {
    setTestingId(template.id);
    try {
      const res = await modelApi.test(template.id);
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
      setTestingId(null);
    }
  };

  const onDelete = async (template: ModelTemplate) => {
    try {
      await modelApi.remove(template.id);
      message.success('已删除');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '删除失败'));
    }
  };

  const columns: ColumnsType<ModelTemplate> = [
    {
      title: '名称',
      dataIndex: 'name',
      width: 180,
      render: (name: string, record) => (
        <div>
          <Space size={4}>
            <Link to={`/models/${record.id}`}>{name}</Link>
            {record.is_embed_model && (
              <Tooltip title="向量专用模型 (平台设置-记忆语义检索), 不参与对话路由">
                <Tag color="purple" style={{ marginRight: 0 }}>向量</Tag>
              </Tooltip>
            )}
          </Space>
          <div style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>
            {MODEL_PROVIDER_MAP[record.provider]?.label ?? record.provider}
            {record.tags?.length > 0 && (
              <span>
                {' · '}
                {record.tags.map((tag) => (
                  <Tag key={tag} style={{ marginRight: 4 }}>
                    {tag}
                  </Tag>
                ))}
              </span>
            )}
          </div>
        </div>
      ),
    },
    {
      title: '模型',
      dataIndex: 'model',
      width: 180,
      render: (v: string) => (
        <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</span>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (s: ModelStatus) => <ModelStatusTag status={s} />,
    },
    {
      title: '优先级',
      dataIndex: 'priority',
      width: 80,
      render: (v: number) => formatNumber(v),
    },
    {
      title: '最近检测',
      dataIndex: 'health_last_check',
      width: 180,
      render: (v: string | null, record) =>
        v ? (
          <span>
            {timeAgo(v)}
            {record.health_latency_ms !== null && (
              <span style={{ color: 'var(--color-text-secondary)' }}> · {record.health_latency_ms}ms</span>
            )}
          </span>
        ) : (
          '-'
        ),
    },
    {
      title: '最后错误',
      dataIndex: 'last_error',
      width: 200,
      ellipsis: true,
      render: (v: string) =>
        v ? (
          <Tooltip title={v}>
            <span style={{ color: '#ff4d4f', fontSize: 12 }}>{v}</span>
          </Tooltip>
        ) : (
          '-'
        ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => formatDateTime(v),
    },
    {
      title: '操作',
      key: 'actions',
      width: 240,
      render: (_, record) => (
        <Space size="small">
          <Button
            size="small"
            icon={<ReloadOutlined spin={testingId === record.id} />}
            loading={testingId === record.id}
            onClick={() => onTest(record)}
          >
            测试
          </Button>
          <Button size="small" onClick={() => navigate(`/models/${record.id}`)}>
            详情
          </Button>
          <Button size="small" onClick={() => navigate(`/models/${record.id}/edit`)}>
            编辑
          </Button>
          <Popconfirm title={`确定删除 ${record.name}?`} onConfirm={() => onDelete(record)} okText="删除" cancelText="取消">
            <Button size="small" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>模型管理</h2>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/models/new')}>
            注册模型
          </Button>
        </Space>
      </div>
      <Card>
        <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder="搜索名称 / 模型 / 端点"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            style={{ width: 260 }}
          />
          <Select
            allowClear
            placeholder="提供商过滤"
            options={PROVIDER_OPTIONS}
            value={provider}
            onChange={setProvider}
            style={{ width: 150 }}
          />
          <Select
            allowClear
            placeholder="状态过滤"
            options={STATUS_OPTIONS}
            value={status}
            onChange={setStatus}
            style={{ width: 130 }}
          />
        </div>
        <Table
          rowKey="id"
          size="middle"
          loading={loading}
          columns={columns}
          dataSource={templates}
          scroll={{ x: 1310 }}
          pagination={{
            current: page,
            pageSize: size,
            total,
            showSizeChanger: true,
            showTotal: (t) => `共 ${t} 条`,
            onChange: (p, s) => {
              setPage(p);
              setSize(s);
            },
          }}
        />
      </Card>
    </div>
  );
}
