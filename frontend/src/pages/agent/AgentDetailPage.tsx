import { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  Alert,
  App,
  Button,
  Card,
  Descriptions,
  Result,
  Space,
  Spin,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import {
  ArrowLeftOutlined,
  CaretRightOutlined,
  EditOutlined,
  FileTextOutlined,
  StopOutlined,
} from '@ant-design/icons';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import { AgentStatusTag, InstanceStatusTag, MCPStatusTag } from '@/components/common/StatusTag';
import type { Agent, AgentBoundMCP, AgentInstance } from '@/types';
import { formatDateTime, timeAgo } from '@/utils/format';
import { AgentLogsPanel } from './AgentLogsPanel';
import { ChatPanel } from './ChatPanel';
import { VersionsPanel } from './VersionsPanel';
import { KeysPanel } from './KeysPanel';
import { SkillsPanel } from './SkillsPanel';
import { MetricsPanel } from './MetricsPanel';
import type { ColumnsType } from 'antd/es/table';
import { Table } from 'antd';

const REFRESH_INTERVAL = 5000; // 详情页 5s 轮询, 状态实时展示

export function AgentDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [instance, setInstance] = useState<AgentInstance | null>(null);
  const [loading, setLoading] = useState(true);
  const [notFound, setNotFound] = useState(false);
  const [acting, setActing] = useState(false);

  const load = useCallback(async () => {
    if (!id) return;
    try {
      const res = await agentApi.getById(id);
      setAgent(res.data?.agent ?? null);
      setInstance(res.data?.instance ?? null);
    } catch (err) {
      if (err instanceof Error && /404|not_found/.test(String(err))) {
        setNotFound(true);
      } else {
        message.error(getErrorMessage(err, '加载 Agent 失败'));
      }
    } finally {
      setLoading(false);
    }
  }, [id, message]);

  useEffect(() => {
    load();
    const timer = setInterval(load, REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [load]);

  const onToggleLifecycle = async () => {
    if (!agent) return;
    setActing(true);
    try {
      if (agent.status === 'running') {
        await agentApi.stop(agent.id);
        message.success('已停止实例');
      } else {
        await agentApi.start(agent.id);
        message.success('已启动实例');
      }
      await load();
    } catch (err) {
      message.error(getErrorMessage(err));
    } finally {
      setActing(false);
    }
  };

  if (notFound) {
    return (
      <Result
        status="404"
        title="Agent 不存在或已删除"
        extra={<Button onClick={() => navigate('/agents')}>返回列表</Button>}
      />
    );
  }

  if (loading || !agent) {
    return (
      <div style={{ textAlign: 'center', padding: 80 }}>
        <Spin size="large" />
      </div>
    );
  }

  const config = agent.config ?? {};
  const apiBase = import.meta.env.VITE_API_BASE_URL || '/api/v1';
  const invokeUrl = /^https?:\/\//.test(apiBase)
    ? `${apiBase}/agents/${agent.id}/invoke`
    : `${window.location.origin}${apiBase}/agents/${agent.id}/invoke`;

  return (
    <div>
      <Space style={{ marginBottom: 16, width: '100%', justifyContent: 'space-between' }} wrap>
        <Space>
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/agents')}>
            返回
          </Button>
          <span style={{ fontSize: 18, fontWeight: 600 }}>{agent.name}</span>
          <AgentStatusTag status={agent.status} />
          <Tag>v{agent.version}</Tag>
        </Space>
        <Space>
          <Button icon={<FileTextOutlined />} onClick={() => navigate(`/agents/${agent.id}/logs`)}>
            日志
          </Button>
          <Button icon={<EditOutlined />} disabled={agent.status === 'running'} onClick={() => navigate(`/agents/${agent.id}/edit`)}>
            编辑
          </Button>
          {agent.status === 'running' ? (
            <Button danger icon={<StopOutlined />} loading={acting} onClick={onToggleLifecycle}>
              停止实例
            </Button>
          ) : (
            <Button type="primary" icon={<CaretRightOutlined />} loading={acting} onClick={onToggleLifecycle}>
              启动实例
            </Button>
          )}
        </Space>
      </Space>

      {agent.description && (
        <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
          {agent.description}
        </Typography.Paragraph>
      )}

      {agent.status === 'error' && (
        <Alert
          type="error"
          showIcon
          message="实例处于异常状态"
          description="通常为服务重启导致实例状态丢失，可重新启动实例。"
          style={{ marginBottom: 16 }}
        />
      )}

      <Card size="small" style={{ marginBottom: 16 }}>
        <Descriptions column={3} size="small" title="实例信息">
          <Descriptions.Item label="实例状态">
            {instance ? <InstanceStatusTag status={instance.status} /> : <Tag>无实例</Tag>}
          </Descriptions.Item>
          <Descriptions.Item label="启动时间">{formatDateTime(instance?.started_at)}</Descriptions.Item>
          <Descriptions.Item label="最后心跳">
            <span style={{ color: 'var(--color-text-secondary)' }}>{timeAgo(instance?.last_heartbeat)}</span>
          </Descriptions.Item>
          <Descriptions.Item label="端点" span={3}>
            <Tooltip title="外部调用需携带 API Key (Authorization: Bearer akp_...); 实例未运行时调用会被拒绝 (409)">
              <span className="mono-text">{invokeUrl}</span>
            </Tooltip>
          </Descriptions.Item>
        </Descriptions>
      </Card>

      <Tabs
        items={[
          {
            key: 'config',
            label: '配置',
            children: (
              <Card size="small">
                <Descriptions column={1} bordered size="small">
                  <Descriptions.Item label="模型">{config.model || '-'}</Descriptions.Item>
                  <Descriptions.Item label="Temperature">{config.temperature ?? '-'}</Descriptions.Item>
                  <Descriptions.Item label="最大 Token">{config.max_tokens ?? '-'}</Descriptions.Item>
                  <Descriptions.Item label="工具轮数上限">{config.max_tool_rounds ?? '默认 5'}</Descriptions.Item>
                  <Descriptions.Item label="创建时间">{formatDateTime(agent.created_at)}</Descriptions.Item>
                  <Descriptions.Item label="系统提示词">
                    {config.system_prompt || <Typography.Text type="secondary">未设置</Typography.Text>}
                  </Descriptions.Item>
                  <Descriptions.Item label="可用工具">
                    {config.tools?.length ? (
                      config.tools.map((t) => (
                        <Tag key={t}>{t}</Tag>
                      ))
                    ) : (
                      <Typography.Text type="secondary">未配置</Typography.Text>
                    )}
                  </Descriptions.Item>
                </Descriptions>
              </Card>
            ),
          },
          {
            key: 'mcps',
            label: '绑定 MCP',
            children: <BoundMCPsPanel agentId={agent.id} />,
          },
          {
            key: 'skills',
            label: '关联技能',
            children: <SkillsPanel agentId={agent.id} usageMode={agent.config.skills_usage_mode} />,
          },
          {
            key: 'chat',
            label: '对话',
            children: <ChatPanel agentId={agent.id} />,
          },
          {
            key: 'versions',
            label: '版本历史',
            children: (
              <VersionsPanel
                agentId={agent.id}
                currentVersion={agent.version}
                running={agent.status === 'running'}
                onRolledBack={load}
              />
            ),
          },
          {
            key: 'keys',
            label: 'API Key',
            children: <KeysPanel agentId={agent.id} />,
          },
          {
            key: 'metrics',
            label: '调用统计',
            children: <MetricsPanel agentId={agent.id} />,
          },
          {
            key: 'logs',
            label: '日志',
            children: <AgentLogsPanel agentId={agent.id} />,
          },
        ]}
      />
    </div>
  );
}

// 绑定 MCP 页签 (独立组件, 自行加载)
function BoundMCPsPanel({ agentId }: { agentId: string }) {
  const [items, setItems] = useState<AgentBoundMCP[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await agentApi.listBoundMCPS(agentId);
      setItems(res.data?.items ?? []);
    } catch {
      // 静默
    } finally {
      setLoading(false);
    }
  }, [agentId]);

  useEffect(() => {
    load();
    const timer = setInterval(load, 15000);
    return () => clearInterval(timer);
  }, [load]);

  const columns: ColumnsType<AgentBoundMCP> = [
    { title: 'MCP', dataIndex: 'name', width: 200 },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (s: AgentBoundMCP['status']) => <MCPStatusTag status={s} />,
    },
    {
      title: '已发现工具',
      dataIndex: 'tools',
      render: (tools: AgentBoundMCP['tools']) =>
        tools && tools.length > 0
          ? tools.map((tool) => <Tag key={tool.name}>{tool.name}</Tag>)
          : '无 (连通后自动发现)',
    },
    {
      title: '最后错误',
      dataIndex: 'last_error',
      ellipsis: true,
      render: (v: string) =>
        v ? <span style={{ color: '#ff4d4f', fontSize: 12 }}>{v}</span> : '-',
    },
  ];

  return (
    <Table
      rowKey="id"
      size="middle"
      loading={loading}
      columns={columns}
      dataSource={items}
      pagination={false}
      locale={{ emptyText: '尚未绑定 MCP 服务器 (在编辑页配置)' }}
    />
  );
}