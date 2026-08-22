import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { App, Button, Card, Input, Popconfirm, Select, Space, Table } from 'antd';
import {
  CaretRightOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
  StopOutlined,
} from '@ant-design/icons';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import { AgentStatusTag } from '@/components/common/StatusTag';
import type { Agent, AgentStatus } from '@/types';
import { AGENT_STATUS_MAP } from '@/utils/constants';
import { formatDateTime } from '@/utils/format';

const REFRESH_INTERVAL = 10000; // 列表 10s 轮询保持状态新鲜

export function AgentListPage() {
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [data, setData] = useState<Agent[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [loading, setLoading] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [status, setStatus] = useState<AgentStatus | undefined>();
  const [actingId, setActingId] = useState<string | null>(null);
  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await agentApi.list({ q: keyword || undefined, status, page, size });
      setData(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载 Agent 列表失败'));
    } finally {
      setLoading(false);
    }
  }, [keyword, status, page, size, message]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    const timer = setInterval(load, REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [load]);

  // 搜索防抖
  const onKeywordChange = (value: string) => {
    setKeyword(value);
    setPage(1);
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(() => load(), 300);
  };

  const toggleLifecycle = async (agent: Agent) => {
    setActingId(agent.id);
    try {
      if (agent.status === 'running') {
        await agentApi.stop(agent.id);
        message.success(`已停止 ${agent.name}`);
      } else {
        await agentApi.start(agent.id);
        message.success(`已启动 ${agent.name}`);
      }
      await load();
    } catch (err) {
      message.error(getErrorMessage(err));
    } finally {
      setActingId(null);
    }
  };

  const onDelete = async (agent: Agent) => {
    try {
      await agentApi.remove(agent.id);
      message.success(`已删除 ${agent.name}`);
      await load();
    } catch (err) {
      message.error(getErrorMessage(err));
    }
  };

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      render: (name: string, record: Agent) => (
        <Space direction="vertical" size={0}>
          <Link to={`/agents/${record.id}`} style={{ fontWeight: 500 }}>
            {name}
          </Link>
          <span style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>
            {record.description || '-'}
          </span>
        </Space>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (s: AgentStatus) => <AgentStatusTag status={s} />,
    },
    { title: '模型', dataIndex: ['config', 'model'], width: 140, render: (m?: string) => m || '-' },
    { title: '版本', dataIndex: 'version', width: 70, render: (v: number) => `v${v}` },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => formatDateTime(v),
    },
    {
      title: '操作',
      key: 'actions',
      width: 210,
      render: (_: unknown, record: Agent) => (
        <Space size="small">
          {record.status === 'running' ? (
            <Button
              size="small"
              danger
              icon={<StopOutlined />}
              loading={actingId === record.id}
              onClick={() => toggleLifecycle(record)}
            >
              停止
            </Button>
          ) : (
            <Button
              size="small"
              type="primary"
              icon={<CaretRightOutlined />}
              loading={actingId === record.id}
              onClick={() => toggleLifecycle(record)}
            >
              启动
            </Button>
          )}
          <Button size="small" icon={<EditOutlined />} onClick={() => navigate(`/agents/${record.id}/edit`)}>
            编辑
          </Button>
          <Popconfirm
            title="确认删除该 Agent？"
            description="删除后不可恢复（运行中的 Agent 无法删除）"
            onConfirm={() => onDelete(record)}
            okText="删除"
            okButtonProps={{ danger: true }}
          >
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>Agent 列表</h2>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/agents/new')}>
            新建 Agent
          </Button>
        </Space>
      </div>
      <Card>
        <Space style={{ marginBottom: 16 }} wrap>
          <Input.Search
            placeholder="搜索名称 / 描述"
            allowClear
            style={{ width: 260 }}
            onSearch={onKeywordChange}
            onChange={(e) => onKeywordChange(e.target.value)}
          />
          <Select
            placeholder="状态过滤"
            allowClear
            style={{ width: 140 }}
            value={status}
            onChange={(v) => {
              setStatus(v);
              setPage(1);
            }}
            options={Object.entries(AGENT_STATUS_MAP).map(([value, item]) => ({
              value,
              label: item.label,
            }))}
          />
        </Space>
        <Table<Agent>
          rowKey="id"
          columns={columns}
          dataSource={data}
          loading={loading}
          onChange={(pagination) => {
            setPage(pagination.current ?? 1);
            setSize(pagination.pageSize ?? 20);
          }}
          pagination={{
            current: page,
            pageSize: size,
            total,
            showSizeChanger: true,
            showTotal: (t) => `共 ${t} 条`,
          }}
        />
      </Card>
    </div>
  );
}