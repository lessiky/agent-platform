import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { Alert, Card, Col, Row, Statistic, Table, Tabs, Tag } from 'antd';
import {
  AppstoreOutlined,
  AuditOutlined,
  ClusterOutlined,
  RobotOutlined,
  SwapOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { agentApi } from '@/api/agent';
import { overviewApi } from '@/api/overview';
import type { AgentStatus, DashboardData, OverviewSummary } from '@/types';
import { formatDateTime, timeAgo } from '@/utils/format';
import { AgentStatusTag } from '@/components/common/StatusTag';
import { WorkflowDashboardPage } from '@/pages/workflow/WorkflowDashboardPage';

const REFRESH_INTERVAL = 5000; // 状态看板 5s 轮询

// 基本情况统计块
function StatBlock({
  icon,
  title,
  children,
}: {
  icon: ReactNode;
  title: string;
  children: ReactNode;
}) {
  return (
    <Card
      size="small"
      style={{ height: '100%' }}
      title={
        <span style={{ fontSize: 14 }}>
          {icon} {title}
        </span>
      }
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {children}
      </div>
    </Card>
  );
}

export function DashboardPage() {
  const location = useLocation();
  const [data, setData] = useState<DashboardData | null>(null);
  const [summary, setSummary] = useState<OverviewSummary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const [dashRes, overviewRes] = await Promise.all([agentApi.getDashboard(), overviewApi.summary()]);
      setData(dashRes.data ?? null);
      setSummary(overviewRes.data ?? null);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const timer = setInterval(load, REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [load]);

  const s = summary;

  const overviewTab = (
    <div>
      <Row gutter={[16, 16]}>
        <Col flex="1 1 0" style={{ minWidth: 150 }}>
          <StatBlock icon={<RobotOutlined />} title="Agent">
            <Statistic title="总数" value={s?.agents.total ?? 0} loading={loading} />
            <Statistic
              title="运行中"
              value={s?.agents.running ?? 0}
              valueStyle={{ color: '#52c41a' }}
              loading={loading}
            />
            <Statistic
              title="已停止 / 空闲"
              value={`${s?.agents.stopped ?? 0} / ${s?.agents.idle ?? 0}`}
              loading={loading}
            />
            <Statistic
              title="异常"
              value={s?.agents.error ?? 0}
              valueStyle={{ color: (s?.agents.error ?? 0) > 0 ? '#ff4d4f' : undefined }}
              loading={loading}
            />
          </StatBlock>
        </Col>
        <Col flex="1 1 0" style={{ minWidth: 150 }}>
          <StatBlock icon={<ClusterOutlined />} title="MCP">
            <Statistic title="总数" value={s?.mcps.total ?? 0} loading={loading} />
            <Statistic
              title="正常"
              value={s?.mcps.normal ?? 0}
              valueStyle={{ color: '#52c41a' }}
              loading={loading}
            />
            <Statistic
              title="异常"
              value={s?.mcps.abnormal ?? 0}
              valueStyle={{ color: (s?.mcps.abnormal ?? 0) > 0 ? '#ff4d4f' : undefined }}
              loading={loading}
            />
            <Statistic title="工具总数" value={s?.mcps.tools_total ?? 0} loading={loading} />
          </StatBlock>
        </Col>
        <Col flex="1 1 0" style={{ minWidth: 150 }}>
          <StatBlock icon={<AppstoreOutlined />} title="模型">
            <Statistic title="总数" value={s?.models.total ?? 0} loading={loading} />
            <Statistic
              title="可用"
              value={s?.models.available ?? 0}
              valueStyle={{ color: '#52c41a' }}
              loading={loading}
            />
            <Statistic
              title="异常"
              value={s?.models.abnormal ?? 0}
              valueStyle={{ color: (s?.models.abnormal ?? 0) > 0 ? '#ff4d4f' : undefined }}
              loading={loading}
            />
          </StatBlock>
        </Col>
        <Col flex="1 1 0" style={{ minWidth: 150 }}>
          <StatBlock icon={<ThunderboltOutlined />} title="技能">
            <Statistic title="总数" value={s?.skills.total ?? 0} loading={loading} />
            <Statistic
              title="启用"
              value={s?.skills.active ?? 0}
              valueStyle={{ color: '#52c41a' }}
              loading={loading}
            />
            <Statistic
              title="禁用"
              value={s?.skills.disabled ?? 0}
              valueStyle={{ color: (s?.skills.disabled ?? 0) > 0 ? '#faad14' : undefined }}
              loading={loading}
            />
          </StatBlock>
        </Col>
        <Col flex="1 1 0" style={{ minWidth: 150 }}>
          <StatBlock icon={<SwapOutlined />} title="工作流">
            <Statistic
              title="已激活"
              value={s?.workflows.active ?? 0}
              valueStyle={{ color: '#1677ff' }}
              loading={loading}
            />
            <Statistic title="草稿" value={s?.workflows.draft ?? 0} loading={loading} />
            <Statistic
              title="已归档"
              value={s?.workflows.archived ?? 0}
              valueStyle={{ color: (s?.workflows.archived ?? 0) > 0 ? '#faad14' : undefined }}
              loading={loading}
            />
          </StatBlock>
        </Col>
        <Col flex="1 1 0" style={{ minWidth: 150 }}>
          <StatBlock icon={<AuditOutlined />} title="审批">
            <Statistic title="总数" value={s?.approvals.total ?? 0} loading={loading} />
            <Statistic
              title="待审核"
              value={s?.approvals.pending ?? 0}
              valueStyle={{ color: (s?.approvals.pending ?? 0) > 0 ? '#faad14' : undefined }}
              loading={loading}
            />
            <Statistic title="已审核" value={s?.approvals.reviewed ?? 0} loading={loading} />
          </StatBlock>
        </Col>
      </Row>

      {error && (
        <Alert type="error" showIcon message={`加载失败: ${error}`} style={{ margin: '16px 0' }} />
      )}

      <Card title="运行中的 Agent" extra={<Tag color="blue">每 5 秒自动刷新</Tag>} style={{ marginTop: 16 }}>
        <Table
          rowKey="id"
          size="middle"
          loading={loading}
          dataSource={data?.running_agents ?? []}
          pagination={false}
          columns={[
            {
              title: '名称',
              dataIndex: 'name',
              render: (name: string, record) => <Link to={`/agents/${record.id}`}>{name}</Link>,
            },
            {
              title: '状态',
              dataIndex: 'status',
              width: 100,
              render: (status: AgentStatus) => <AgentStatusTag status={status} />,
            },
            { title: '版本', dataIndex: 'version', width: 80, render: (v: number) => `v${v}` },
            {
              title: '启动时间',
              dataIndex: 'started_at',
              width: 180,
              render: (v?: string | null) => formatDateTime(v),
            },
            {
              title: '最后心跳',
              dataIndex: 'last_heartbeat',
              width: 140,
              render: (v?: string | null) => <span style={{ color: 'var(--color-text-secondary)' }}>{timeAgo(v)}</span>,
            },
          ]}
        />
      </Card>

      <div style={{ marginTop: 16, color: 'var(--color-text-secondary)' }}>
        调用统计: 今日累计 - (详见各 Agent 详情页指标页签) · 快捷入口: <Link to="/agents">Agent 管理</Link>
      </div>
    </div>
  );

  return (
    <Tabs
      defaultActiveKey={
        new URLSearchParams(location.search).get('tab') === 'workflow' ? 'workflow' : 'overview'
      }
      items={[
        { key: 'overview', label: '基本情况', children: overviewTab },
        { key: 'workflow', label: '工作流看板', children: <WorkflowDashboardPage /> },
      ]}
    />
  );
}
