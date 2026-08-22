import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  App,
  Badge,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  Input,
  Popconfirm,
  Row,
  Select,
  Space,
  Statistic,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { ArrowLeftOutlined, EditOutlined, ReloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { mcpApi } from '@/api/mcp';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import { MCPStatusTag } from '@/components/common/StatusTag';
import { MCP_STATUS_MAP, MCP_TRANSPORT_MAP } from '@/utils/constants';
import { formatDateTime, timeAgo } from '@/utils/format';
import type { Agent, MCPServer, MCPTool } from '@/types';

const REFRESH_INTERVAL = 5000;

export function MCPDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { message } = App.useApp();

  const [server, setServer] = useState<MCPServer | null>(null);
  const [pendingCount, setPendingCount] = useState(0);
  const [togglingTool, setTogglingTool] = useState<string | null>(null);
  const [credentials, setCredentials] = useState<{ api_key_set: boolean; api_key_mask?: string; header_keys: string[] } | null>(null);
  const [health, setHealth] = useState<{ history: { id: string; ok: boolean; latency_ms: number; error: string; created_at: string }[] } | null>(null);
  const [loading, setLoading] = useState(true);
  const [testing, setTesting] = useState(false);
  const [bindAgentId, setBindAgentId] = useState<string | undefined>(undefined);
  const [agents, setAgents] = useState<Agent[]>([]);

  const load = useCallback(async () => {
    if (!id) return;
    try {
      const [serverRes, healthRes, pendingRes] = await Promise.all([
        mcpApi.getById(id),
        mcpApi.getHealth(id, 100),
        mcpApi.pendingCount(id),
      ]);
      setServer(serverRes.data?.server ?? null);
      setCredentials(serverRes.data?.credentials ?? null);
      setHealth(healthRes.data ?? null);
      setPendingCount(pendingRes.data?.total ?? 0);
    } catch (err) {
      if (server === null) message.error(getErrorMessage(err, '加载 MCP 失败'));
    } finally {
      setLoading(false);
    }
  }, [id, message, server]);

  useEffect(() => {
    load();
    const timer = setInterval(load, REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [load]);

  // Agent 列表 (绑定向用)
  useEffect(() => {
    (async () => {
      try {
        const res = await agentApi.list({ size: 100 });
        setAgents(res.data?.items ?? []);
      } catch {
        // 绑定非核心功能, 失败静默
      }
    })();
  }, []);

  const onTest = async () => {
    if (!id) return;
    setTesting(true);
    try {
      const res = await mcpApi.test(id);
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
      setTesting(false);
    }
  };

  const onBind = async () => {
    if (!id || !bindAgentId) return;
    try {
      await mcpApi.bindAgent(id, bindAgentId);
      message.success('已绑定');
      setBindAgentId(undefined);
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '绑定失败'));
    }
  };

  const onUnbind = async (agentId: string) => {
    if (!id) return;
    try {
      await mcpApi.unbindAgent(id, agentId);
      message.success('已解绑');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '解绑失败'));
    }
  };

  const agentName = (agentId: string) => agents.find((a) => a.id === agentId)?.name ?? agentId;

  // M4.5: 工具级"需审核"开关 (增量更新, 其余工具标记保持不变)
  const onToggleApproval = async (tool: MCPTool, checked: boolean) => {
    if (!id) return;
    setTogglingTool(tool.name);
    try {
      await mcpApi.updateToolApprovals(id, [{ name: tool.name, requires_approval: checked }]);
      message.success(checked ? `工具 ${tool.name} 已设置需人工审核` : `工具 ${tool.name} 已关闭需审核`);
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '更新审核配置失败'));
    } finally {
      setTogglingTool(null);
    }
  };

  const toolColumns: ColumnsType<MCPTool> = [
    { title: '名称', dataIndex: 'name', width: 200, render: (v: string) => <Typography.Text code>{v}</Typography.Text> },
    { title: '描述', dataIndex: 'description' },
    {
      title: '需审核',
      dataIndex: 'requires_approval',
      width: 90,
      render: (v: boolean | undefined, tool: MCPTool) => (
        <Switch
          size="small"
          checked={!!v}
          loading={togglingTool === tool.name}
          onChange={(checked) => onToggleApproval(tool, checked)}
        />
      ),
    },
    {
      title: '参数 Schema',
      dataIndex: 'inputSchema',
      width: 360,
      render: (schema?: Record<string, unknown>) =>
        schema ? (
          <Input.TextArea
            readOnly
            autoSize={{ minRows: 1, maxRows: 6 }}
            style={{ fontFamily: 'monospace', fontSize: 12 }}
            value={JSON.stringify(schema, null, 2)}
          />
        ) : (
          '-'
        ),
    },
  ];

  const historyColumns: ColumnsType<{ id: string; ok: boolean; latency_ms: number; error: string; created_at: string }> = [
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
      render: (v: number) => (v > 0 ? `${v} ms` : '-'),
    },
    { title: '错误信息', dataIndex: 'error', render: (v: string) => (v ? <span style={{ color: '#ff4d4f' }}>{v}</span> : '-') },
  ];

  if (!server) {
    return (
      <Card loading={loading}>
        {!loading && (
          <Empty>
            <Link to="/mcp">
              <Button>返回列表</Button>
            </Link>
          </Empty>
        )}
      </Card>
    );
  }


  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Space size={12} align="center">
          <Link to="/mcp">
            <Button icon={<ArrowLeftOutlined />} />
          </Link>
          <h2 style={{ margin: 0 }}>{server.name}</h2>
          <MCPStatusTag status={server.status} />
          <Tag>{MCP_TRANSPORT_MAP[server.transport]?.label ?? server.transport}</Tag>
          {server.tags?.map((tag) => (
            <Tag key={tag} color="blue">
              {tag}
            </Tag>
          ))}
        </Space>
        <Space>
          <Button icon={<ReloadOutlined spin={testing} />} loading={testing} onClick={onTest}>
            连通性测试
          </Button>
          <Button icon={<EditOutlined />} onClick={() => navigate(`/mcp/${server.id}/edit`)}>
            编辑
          </Button>
        </Space>
      </div>

      {server.description && <p style={{ color: 'var(--color-text-secondary)', marginTop: 0 }}>{server.description}</p>}

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Card>
            <Statistic title="当前状态" value={MCP_STATUS_MAP[server.status]?.label ?? server.status} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="最近检测" value={server.health_last_check ? timeAgo(server.health_last_check) : '未检测'} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="检测延迟"
              value={server.health_latency_ms ?? '-'}
              suffix={server.health_latency_ms !== null ? 'ms' : ''}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="已发现工具" value={server.tools?.length ?? 0} suffix="个" />
          </Card>
        </Col>
      </Row>

      <Card>
        <Descriptions
          column={2}
          size="small"
          items={[
            { key: 'endpoint', label: '端点', children: <Typography.Text code>{server.endpoint}</Typography.Text> },
            {
              key: 'created',
              label: '创建时间',
              children: formatDateTime(server.created_at),
            },
            {
              key: 'error',
              label: '最后错误',
              span: 2,
              children: server.last_error ? <span style={{ color: '#ff4d4f' }}>{server.last_error}</span> : '-',
            },
          ]}
        />
      </Card>

      <Card style={{ marginTop: 16 }}>
        <Tabs
          items={[
            {
              key: 'tools',
              label: (
                <span>
                  工具 ({server.tools?.length ?? 0})
                  {pendingCount > 0 && <Badge count={pendingCount} size="small" style={{ marginLeft: 8 }} />}
                </span>
              ),
              children: (
                <Table
                  rowKey="name"
                  size="middle"
                  columns={toolColumns}
                  dataSource={server.tools ?? []}
                  pagination={false}
                  locale={{ emptyText: '尚未发现工具, 点击"连通性测试"刷新' }}
                />
              ),
            },
            {
              key: 'health',
              label: '健康历史',
              children: (
                <>
                  {server.last_error && (
                    <Tag color="red" style={{ marginBottom: 12 }}>
                      当前异常: {server.last_error}
                    </Tag>
                  )}
                  <Table
                    rowKey="id"
                    size="middle"
                    columns={historyColumns}
                    dataSource={health?.history ?? []}
                    pagination={{ pageSize: 20, showTotal: (t) => `共 ${t} 条` }}
                    locale={{ emptyText: '暂无检查记录 (每 30 秒自动检测一次)' }}
                  />
                </>
              ),
            },
            {
              key: 'credentials',
              label: '凭证',
              children: (
                <Descriptions column={1} size="small" items={[
                  {
                    key: 'api_key',
                    label: 'API Key',
                    children: credentials?.api_key_set ? (
                      <Space>
                        <Typography.Text code>{credentials.api_key_mask}</Typography.Text>
                        <Tag color="blue">已设置</Tag>
                      </Space>
                    ) : (
                      '未设置'
                    ),
                  },
                  {
                    key: 'headers',
                    label: '自定义请求头',
                    children: credentials && credentials.header_keys.length > 0 ? (
                      credentials.header_keys.map((key) => (
                        <Tag key={key}>{key}</Tag>
                      ))
                    ) : (
                      '未设置'
                    ),
                  },
                ]} />
              ),
            },
            {
              key: 'bindings',
              label: 'Agent 绑定',
              children: (
                <BindingsTab
                  mcpId={server.id}
                  agents={agents}
                  bindAgentId={bindAgentId}
                  onAgentChange={setBindAgentId}
                  onBind={onBind}
                  onUnbind={onUnbind}
                  agentName={agentName}
                />
              ),
            },
          ]}
        />
      </Card>
    </div>
  );
}

// Agent 绑定页签 (独立组件, 自行加载绑定关系)
function BindingsTab({
  mcpId,
  agents,
  bindAgentId,
  onAgentChange,
  onBind,
  onUnbind,
  agentName,
}: {
  mcpId: string;
  agents: Agent[];
  bindAgentId?: string;
  onAgentChange: (id?: string) => void;
  onBind: () => void;
  onUnbind: (agentId: string) => void;
  agentName: (id: string) => string;
}) {
  const [bindings, setBindings] = useState<{ id: string; mcp_id: string; agent_id: string }[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await mcpApi.listAgents(mcpId);
      setBindings(res.data?.agents ?? []);
    } catch {
      // 静默
    } finally {
      setLoading(false);
    }
  }, [mcpId]);

  useEffect(() => {
    load();
    const timer = setInterval(load, 15000);
    return () => clearInterval(timer);
  }, [load]);

  const available = agents.filter((agent) => !bindings.some((b) => b.agent_id === agent.id));

  return (
    <div>
      <div style={{ marginBottom: 12 }}>
        <Space>
          <Select
            placeholder="选择 Agent"
            style={{ width: 280 }}
            value={bindAgentId}
            onChange={onAgentChange}
            options={available.map((agent) => ({ value: agent.id, label: agent.name }))}
          />
          <Button type="primary" disabled={!bindAgentId} onClick={onBind}>
            绑定
          </Button>
        </Space>
        <div style={{ color: 'var(--color-text-secondary)', fontSize: 12, marginTop: 8 }}>
          绑定的 Agent 在运行时可调用该 MCP 的工具 (访问控制, PRD 2.2.3 P0)
        </div>
      </div>
      <Table
        rowKey="id"
        size="middle"
        loading={loading}
        columns={[
          { title: 'Agent', dataIndex: 'agent_id', render: (v: string) => <Link to={`/agents/${v}`}>{agentName(v)}</Link> },
          { title: '绑定时间', dataIndex: 'created_at', width: 180, render: (v: string) => formatDateTime(v) },
          {
            title: '操作',
            key: 'actions',
            width: 100,
            render: (_: unknown, record: { agent_id: string }) => (
              <Popconfirm title="确定解绑?" onConfirm={() => onUnbind(record.agent_id)} okText="解绑" cancelText="取消">
                <Button size="small" danger>
                  解绑
                </Button>
              </Popconfirm>
            ),
          },
        ]}
        dataSource={bindings}
        pagination={false}
        locale={{ emptyText: '尚未绑定 Agent' }}
      />
    </div>
  );
}