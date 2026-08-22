import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, Card, Col, Row, Statistic, Table, Tag } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { workflowApi, type DashboardData, type WorkflowExecution } from '@/api/workflow';
import { getErrorMessage } from '@/api/client';
import { App } from 'antd';
import { timeAgo } from '@/utils/format';

const EXEC_STATUS: Record<string, { label: string; color: string }> = {
  running: { label: '执行中', color: 'processing' },
  waiting_approval: { label: '等待审核', color: 'warning' },
  success: { label: '成功', color: 'success' },
  failed: { label: '失败', color: 'error' },
  cancelled: { label: '已取消', color: 'default' },
};

const REFRESH_INTERVAL = 5000;

export function WorkflowDashboardPage() {
  const { message } = App.useApp();
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await workflowApi.dashboard();
      setData(res.data ?? null);
    } catch (err) {
      message.error(getErrorMessage(err, '加载看板失败'));
    } finally {
      setLoading(false);
    }
  }, [message]);

  useEffect(() => {
    load();
    const timer = setInterval(load, REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [load]);

  const columns: ColumnsType<WorkflowExecution> = [
    {
      title: '工作流',
      dataIndex: 'workflow_name',
      width: 200,
      render: (name: string, r) => <Link to={`/workflows/${r.workflow_id}`}>{name}</Link>,
    },
    {
      title: '执行 ID',
      dataIndex: 'id',
      width: 130,
      render: (v: string) => <Link to={`/workflows/executions/${v}`}>{v.slice(0, 8)}…</Link>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (s: string) => <Tag color={EXEC_STATUS[s]?.color}>{EXEC_STATUS[s]?.label ?? s}</Tag>,
    },
    { title: '触发', dataIndex: 'trigger_type', width: 90 },
    { title: '开始', dataIndex: 'started_at', width: 150, render: (v: string) => timeAgo(v) },
    {
      title: '耗时',
      width: 100,
      render: (_, r) => {
        if (!r.finished_at) return '—';
        const ms = new Date(r.finished_at).getTime() - new Date(r.started_at).getTime();
        return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
      },
    },
  ];

  const counts = data?.counts_by_status ?? {};

  return (
    <div>
      <Row gutter={16} style={{ marginBottom: 8 }}>
        <Col span={4}>
          <Card size="small">
            <Statistic title="执行中" value={data?.running ?? 0} valueStyle={{ color: '#1677ff' }} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="等待审核" value={data?.waiting_approval ?? 0} valueStyle={{ color: '#faad14' }} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="成功" value={data?.success ?? 0} valueStyle={{ color: '#52c41a' }} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="失败" value={data?.failed ?? 0} valueStyle={{ color: '#ff4d4f' }} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="已取消" value={data?.cancelled ?? 0} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>
              刷新
            </Button>
          </Card>
        </Col>
      </Row>
      <Card size="small" title="最近执行 (自动刷新 5s)">
        <Table
          rowKey="id"
          loading={loading}
          columns={columns}
          dataSource={data?.recent ?? []}
          pagination={false}
          size="small"
        />
      </Card>
      <div style={{ marginTop: 8, color: 'var(--color-text-secondary)', fontSize: 12 }}>
        全量分布: {Object.entries(counts).map(([k, v]) => `${EXEC_STATUS[k]?.label ?? k} ${v}`).join(' · ') || '—'}
      </div>
    </div>
  );
}