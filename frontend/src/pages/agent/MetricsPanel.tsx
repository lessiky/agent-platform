import { useCallback, useEffect, useState } from 'react';
import { App, Card, Col, Row, Statistic, Table } from 'antd';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import type { AgentMetrics } from '@/types';
import { formatNumber, formatPercent } from '@/utils/format';
import dayjs from 'dayjs';

const RANGE_PRESETS = [
  { label: '最近 24 小时', value: 1 },
  { label: '最近 7 天', value: 7 },
  { label: '最近 30 天', value: 30 },
];

interface MetricsPanelProps {
  agentId: string;
}

// 调用统计 (Week 5 图表化前先用统计卡片 + 按天表格)
export function MetricsPanel({ agentId }: MetricsPanelProps) {
  const { message } = App.useApp();
  const [metrics, setMetrics] = useState<AgentMetrics | null>(null);
  const [loading, setLoading] = useState(false);
  const [days, setDays] = useState(7);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const from = dayjs().subtract(days, 'day').startOf('day').toISOString();
      const res = await agentApi.getMetrics(agentId, { from });
      setMetrics(res.data ?? null);
    } catch (err) {
      message.error(getErrorMessage(err, '加载指标失败'));
    } finally {
      setLoading(false);
    }
  }, [agentId, days, message]);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <Card
      size="small"
      title="调用统计"
      extra={<span style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>时间范围切换见下方表格标题</span>}
    >
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Statistic title="调用次数" value={metrics?.total_calls ?? 0} loading={loading} />
        </Col>
        <Col span={6}>
          <Statistic
            title="错误次数"
            value={metrics?.total_errors ?? 0}
            valueStyle={{ color: (metrics?.total_errors ?? 0) > 0 ? '#ff4d4f' : undefined }}
            loading={loading}
          />
        </Col>
        <Col span={6}>
          <Statistic title="错误率" value={formatPercent(metrics?.error_rate)} loading={loading} />
        </Col>
        <Col span={6}>
          <Statistic title="平均延迟" value={metrics ? `${Math.round(metrics.avg_latency_ms)} ms` : '-'} loading={loading} />
        </Col>
      </Row>
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={12}>
          <Statistic title="Token 消耗" value={formatNumber(metrics?.total_tokens)} loading={loading} />
        </Col>
        <Col span={12} />
      </Row>

      <Table
        rowKey="stat_date"
        size="small"
        loading={loading}
        dataSource={metrics?.daily ?? []}
        pagination={false}
        title={() => (
          <span style={{ fontSize: 13, color: 'var(--color-text-secondary)' }}>
            按天明细 ·{' '}
            {RANGE_PRESETS.map((p, i) => (
              <span key={p.value}>
                {i > 0 && ' | '}
                <a
                  style={{ color: days === p.value ? 'var(--color-primary)' : undefined, fontWeight: days === p.value ? 600 : 400 }}
                  onClick={() => setDays(p.value)}
                >
                  {p.label}
                </a>
              </span>
            ))}
          </span>
        )}
        columns={[
          { title: '日期', dataIndex: 'stat_date', width: 130, render: (v: string) => dayjs(v).format('YYYY-MM-DD') },
          { title: '调用次数', dataIndex: 'calls', width: 110, render: (v: number) => formatNumber(v) },
          { title: '错误', dataIndex: 'errors', width: 90, render: (v: number) => formatNumber(v) },
          { title: 'Token', dataIndex: 'total_tokens', width: 110, render: (v: number) => formatNumber(v) },
          {
            title: '平均延迟 (ms)',
            dataIndex: 'total_latency_ms',
            render: (v: number, record) => (record.calls > 0 ? Math.round(v / record.calls) : '-'),
          },
        ]}
      />
    </Card>
  );
}