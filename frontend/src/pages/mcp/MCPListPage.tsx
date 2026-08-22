import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { App, Button, Card, Input, Popconfirm, Select, Space, Table, Tag, Tooltip } from 'antd';
import { PlusOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { mcpApi } from '@/api/mcp';
import { getErrorMessage } from '@/api/client';
import { MCPStatusTag } from '@/components/common/StatusTag';
import { MCP_STATUS_MAP, MCP_TRANSPORT_MAP } from '@/utils/constants';
import { formatDateTime, formatNumber, timeAgo } from '@/utils/format';
import type { MCPServer, MCPStatus } from '@/types';

const REFRESH_INTERVAL = 10000; // 列表 10s 轮询
const STATUS_OPTIONS = (Object.keys(MCP_STATUS_MAP) as MCPStatus[]).map((key) => ({
  value: key,
  label: MCP_STATUS_MAP[key].label,
}));

export function MCPListPage() {
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [keyword, setKeyword] = useState('');
  const [debouncedKeyword, setDebouncedKeyword] = useState('');
  const [status, setStatus] = useState<MCPStatus | undefined>(undefined);
  const [testingId, setTestingId] = useState<string | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout>>();

  // 搜索防抖
  useEffect(() => {
    timerRef.current = setTimeout(() => setDebouncedKeyword(keyword.trim()), 400);
    return () => clearTimeout(timerRef.current);
  }, [keyword]);

  const load = useCallback(async () => {
    try {
      const res = await mcpApi.list({ q: debouncedKeyword || undefined, status, page, size });
      setServers(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载 MCP 列表失败'));
    } finally {
      setLoading(false);
    }
  }, [debouncedKeyword, status, page, size, message]);

  useEffect(() => {
    setLoading(true);
    load();
    const timer = setInterval(load, REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [load]);

  const onTest = async (server: MCPServer) => {
    setTestingId(server.id);
    try {
      const res = await mcpApi.test(server.id);
      const result = res.data;
      if (result?.ok) {
        message.success(`连接正常 (${result.latency_ms}ms, 工具 ${result.tools_count} 个)`);
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

  const onDelete = async (server: MCPServer) => {
    try {
      await mcpApi.remove(server.id);
      message.success('已删除');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '删除失败'));
    }
  };

  const columns: ColumnsType<MCPServer> = [
    {
      title: '名称',
      dataIndex: 'name',
      width: 180,
      render: (name: string, record) => (
        <div>
          <Link to={`/mcp/${record.id}`}>{name}</Link>
          <div style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>
            {MCP_TRANSPORT_MAP[record.transport]?.label ?? record.transport}
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
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (s: MCPStatus) => <MCPStatusTag status={s} />,
    },
    {
      title: '端点',
      dataIndex: 'endpoint',
      ellipsis: true,
      render: (v: string) => (
        <Tooltip title={v}>
          <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</span>
        </Tooltip>
      ),
    },
    {
      title: '工具',
      dataIndex: 'tools',
      width: 70,
      render: (tools: MCPServer['tools']) => formatNumber(tools?.length ?? 0),
    },
    {
      title: '最近检测',
      dataIndex: 'health_last_check',
      width: 190,
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
      width: 180,
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
          <Button size="small" onClick={() => navigate(`/mcp/${record.id}`)}>
            详情
          </Button>
          <Button size="small" onClick={() => navigate(`/mcp/${record.id}/edit`)}>
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
        <h2 style={{ margin: 0 }}>MCP 服务器</h2>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/mcp/new')}>
            注册 MCP
          </Button>
        </Space>
      </div>
      <Card>
        <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder="搜索名称 / 描述 / 端点"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            style={{ width: 280 }}
          />
          <Select
            allowClear
            placeholder="状态过滤"
            options={STATUS_OPTIONS}
            value={status}
            onChange={setStatus}
            style={{ width: 140 }}
          />
        </div>
        <Table
          rowKey="id"
          size="middle"
          loading={loading}
          columns={columns}
          dataSource={servers}
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