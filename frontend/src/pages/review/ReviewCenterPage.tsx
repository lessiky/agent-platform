import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { App, Button, Card, Descriptions, Input, Modal, Space, Table, Tabs, Tag, Tooltip } from 'antd';
import { CheckOutlined, EyeOutlined, ReloadOutlined, StopOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { reviewApi } from '@/api/review';
import { getErrorMessage } from '@/api/client';
import { useAuthStore } from '@/store/auth-store';
import type { Agent } from '@/types';
import type { Workflow } from '@/api/workflow';
import { formatDateTime } from '@/utils/format';

// 审核中心: Agent/工作流 发布审核 (新建/修改 -> 审核中, 管理员通过/驳回)
export function ReviewCenterPage() {
  const { message } = App.useApp();
  const permissions = useAuthStore((s) => s.permissions);
  const canAgent = permissions.includes('agent:review');
  const canWorkflow = permissions.includes('workflow:review');
  const [tab, setTab] = useState(canAgent ? 'agent' : 'workflow');

  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentTotal, setAgentTotal] = useState(0);
  const [agentPage, setAgentPage] = useState(1);
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [wfTotal, setWfTotal] = useState(0);
  const [wfPage, setWfPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [actingId, setActingId] = useState<string | null>(null);

  const [detailOpen, setDetailOpen] = useState(false);
  const [detailTitle, setDetailTitle] = useState('');
  const [detailBody, setDetailBody] = useState<React.ReactNode>(null);
  const [decisionOpen, setDecisionOpen] = useState(false);
  const [decisionTarget, setDecisionTarget] = useState<{ kind: 'agent' | 'workflow'; id: string; name: string } | null>(null);
  const [decision, setDecision] = useState<'approve' | 'reject'>('approve');
  const [comment, setComment] = useState('');

  const load = useCallback(async () => {
    // 两个队列同时拉取 (权限允许时), 保证 Tab 计数实时准确
    setLoading(true);
    try {
      const jobs: Promise<unknown>[] = [];
      if (canAgent) {
        jobs.push(
          reviewApi
            .pendingAgents({ page: agentPage, size: 20 })
            .then((res) => {
              setAgents(res.data?.items ?? []);
              setAgentTotal(res.data?.total ?? 0);
            }),
        );
      }
      if (canWorkflow) {
        jobs.push(
          reviewApi
            .pendingWorkflows({ page: wfPage, size: 20 })
            .then((res) => {
              setWorkflows(res.data?.items ?? []);
              setWfTotal(res.data?.total ?? 0);
            }),
        );
      }
      await Promise.all(jobs);
    } catch (err) {
      message.error(getErrorMessage(err, '加载审核队列失败'));
    } finally {
      setLoading(false);
    }
  }, [agentPage, wfPage, canAgent, canWorkflow, message]);

  useEffect(() => {
    load();
  }, [load]);

  // 无 agent 审核权限时自动切到工作流 Tab
  useEffect(() => {
    if (!canAgent && tab === 'agent') setTab('workflow');
    if (canAgent && tab === 'workflow' && !canWorkflow) setTab('agent');
  }, [canAgent, canWorkflow, tab]);

  const openDetail = (kind: 'agent' | 'workflow', item: Agent | Workflow) => {
    setDetailTitle(kind === 'agent' ? `Agent 详情: ${item.name}` : `工作流详情: ${item.name}`);
    if (kind === 'agent') {
      const cfg = (item as Agent).config ?? {};
      setDetailBody(
        <Descriptions column={1} size="small" bordered>
          <Descriptions.Item label="版本">v{(item as Agent).version}</Descriptions.Item>
          <Descriptions.Item label="模型">{cfg.model || '-'}</Descriptions.Item>
          <Descriptions.Item label="系统提示词">{cfg.system_prompt || <span style={{ color: '#999' }}>未设置</span>}</Descriptions.Item>
          <Descriptions.Item label="可用工具">
            {cfg.tools?.length ? cfg.tools.map((t) => <Tag key={t}>{t}</Tag>) : <span style={{ color: '#999' }}>未配置</span>}
          </Descriptions.Item>
          <Descriptions.Item label="技能注入模式">{cfg.skills_usage_mode || '默认'}</Descriptions.Item>
          <Descriptions.Item label="更新时间">{formatDateTime((item as Agent).updated_at)}</Descriptions.Item>
          <Descriptions.Item label="审核意见">{(item as Agent).review_comment || <span style={{ color: '#999' }}>无</span>}</Descriptions.Item>
        </Descriptions>,
      );
    } else {
      const wf = item as Workflow;
      const nodes = wf.definition?.nodes ?? [];
      setDetailBody(
        <div>
          <div style={{ marginBottom: 12 }}>
            <span style={{ color: 'var(--color-text-secondary)' }}>版本 v{wf.version} · {nodes.length} 节点 · 更新于 {formatDateTime(wf.updated_at)}</span>
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 12 }}>
            {nodes.map((n) => (
              <Tag key={n.id} color={n.type === 'agent' ? 'purple' : 'geekblue'}>
                {n.name || n.id} ({n.type}
                {n.type === 'agent' ? ` -> ${String(n.config?.agent_id ?? '').slice(0, 8)}…` : ''})
              </Tag>
            ))}
          </div>
          <div style={{ color: 'var(--color-text-secondary)' }}>
            提示: 紫色为 Agent 节点; 其引用的 Agent 也需审核通过后工作流才能运行 (运行前服务端会预检)。
          </div>
        </div>,
      );
    }
    setDetailOpen(true);
  };

  const submitDecision = async () => {
    if (!decisionTarget) return;
    if (decision === 'reject' && !comment.trim()) {
      message.warning('驳回时必须填写审核意见');
      return;
    }
    setActingId(decisionTarget.id);
    try {
      const payload = { decision, comment: comment.trim() || undefined };
      if (decisionTarget.kind === 'agent') {
        await reviewApi.reviewAgent(decisionTarget.id, payload);
      } else {
        await reviewApi.reviewWorkflow(decisionTarget.id, payload);
      }
      message.success(decision === 'approve' ? '已通过审核' : '已驳回');
      setDecisionOpen(false);
      setComment('');
      await load();
    } catch (err) {
      message.error(getErrorMessage(err, '审核操作失败'));
    } finally {
      setActingId(null);
    }
  };

  const agentColumns: ColumnsType<Agent> = [
    {
      title: '名称',
      dataIndex: 'name',
      render: (name: string, r: Agent) => (
        <Link to={`/agents/${r.id}`} style={{ fontWeight: 500 }}>{name}</Link>
      ),
    },
    { title: '模型', width: 140, render: (_, r) => r.config?.model || '-' },
    { title: '版本', dataIndex: 'version', width: 70, render: (v: number) => `v${v}` },
    {
      title: '提交时间',
      dataIndex: 'updated_at',
      width: 170,
      render: (v: string) => formatDateTime(v),
    },
    {
      title: '上次审核意见',
      dataIndex: 'review_comment',
      ellipsis: true,
      render: (v?: string | null) => v || '—',
    },
    {
      title: '操作',
      width: 240,
      render: (_, r) => (
        <Space size="small">
          <Button size="small" icon={<EyeOutlined />} onClick={() => openDetail('agent', r)}>查看</Button>
          <Button
            size="small"
            type="primary"
            icon={<CheckOutlined />}
            loading={actingId === r.id}
            onClick={() => {
              setDecisionTarget({ kind: 'agent', id: r.id, name: r.name });
              setDecision('approve');
              setComment('');
              setDecisionOpen(true);
            }}
          >
            通过
          </Button>
          <Button
            size="small"
            danger
            icon={<StopOutlined />}
            loading={actingId === r.id}
            onClick={() => {
              setDecisionTarget({ kind: 'agent', id: r.id, name: r.name });
              setDecision('reject');
              setComment('');
              setDecisionOpen(true);
            }}
          >
            驳回
          </Button>
        </Space>
      ),
    },
  ];

  const wfColumns: ColumnsType<Workflow> = [
    {
      title: '名称',
      dataIndex: 'name',
      render: (name: string, r: Workflow) => (
        <Link to={`/workflows/${r.id}`} style={{ fontWeight: 500 }}>{name}</Link>
      ),
    },
    {
      title: '节点',
      width: 90,
      render: (_, r) => `${r.definition?.nodes?.length ?? 0} 个`,
    },
    { title: '版本', dataIndex: 'version', width: 70, render: (v: number) => `v${v}` },
    {
      title: '定时调度',
      width: 130,
      render: (_, r) =>
        r.schedule_enabled && r.schedule ? <Tag color="blue">{r.schedule.cron}</Tag> : '—',
    },
    {
      title: '提交时间',
      dataIndex: 'updated_at',
      width: 170,
      render: (v: string) => formatDateTime(v),
    },
    {
      title: '上次审核意见',
      dataIndex: 'review_comment',
      ellipsis: true,
      render: (v?: string | null) => v || '—',
    },
    {
      title: '操作',
      width: 240,
      render: (_, r) => (
        <Space size="small">
          <Button size="small" icon={<EyeOutlined />} onClick={() => openDetail('workflow', r)}>查看</Button>
          <Button
            size="small"
            type="primary"
            icon={<CheckOutlined />}
            loading={actingId === r.id}
            onClick={() => {
              setDecisionTarget({ kind: 'workflow', id: r.id, name: r.name });
              setDecision('approve');
              setComment('');
              setDecisionOpen(true);
            }}
          >
            通过
          </Button>
          <Button
            size="small"
            danger
            icon={<StopOutlined />}
            loading={actingId === r.id}
            onClick={() => {
              setDecisionTarget({ kind: 'workflow', id: r.id, name: r.name });
              setDecision('reject');
              setComment('');
              setDecisionOpen(true);
            }}
          >
            驳回
          </Button>
        </Space>
      ),
    },
  ];

  const tabItems = useMemo(
    () => [
      ...(canAgent
        ? [
            {
              key: 'agent',
              label: `Agent 审核 (${agentTotal})`,
              children: (
                <Table<Agent>
                  rowKey="id"
                  columns={agentColumns}
                  dataSource={agents}
                  loading={loading}
                  pagination={{
                    current: agentPage,
                    pageSize: 20,
                    total: agentTotal,
                    onChange: (p) => setAgentPage(p),
                  }}
                />
              ),
            },
          ]
        : []),
      ...(canWorkflow
        ? [
            {
              key: 'workflow',
              label: `工作流审核 (${wfTotal})`,
              children: (
                <Table<Workflow>
                  rowKey="id"
                  columns={wfColumns}
                  dataSource={workflows}
                  loading={loading}
                  pagination={{
                    current: wfPage,
                    pageSize: 20,
                    total: wfTotal,
                    onChange: (p) => setWfPage(p),
                  }}
                />
              ),
            },
          ]
        : []),
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [canAgent, canWorkflow, agents, workflows, agentTotal, wfTotal, agentPage, wfPage, loading],
  );

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>发布审核</h2>
        <Space>
          <Tooltip title="仅展示审核中 (待审核) 的 Agent/工作流">
            <Tag color="orange">审核中</Tag>
          </Tooltip>
          <Button icon={<ReloadOutlined />} onClick={load}>刷新</Button>
        </Space>
      </div>
      <Card>
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={tabItems}
        />
      </Card>

      <Modal title={detailTitle} open={detailOpen} onCancel={() => setDetailOpen(false)} footer={null} width={640}>
        {detailBody}
      </Modal>

      <Modal
        title={decisionTarget ? `${decision === 'approve' ? '通过' : '驳回'} ${decisionTarget.kind === 'agent' ? 'Agent' : '工作流'}「${decisionTarget.name}」` : ''}
        open={decisionOpen}
        onCancel={() => setDecisionOpen(false)}
        onOk={submitDecision}
        okText={decision === 'approve' ? '确认通过' : '确认驳回'}
        okButtonProps={{ danger: decision === 'reject', loading: actingId !== null }}
        cancelText="取消"
      >
        <Space direction="vertical" style={{ width: '100%' }} size="middle">
          <Space>
            <span>审核决策:</span>
            <Button
              type={decision === 'approve' ? 'primary' : 'default'}
              size="small"
              icon={<CheckOutlined />}
              onClick={() => setDecision('approve')}
            >
              通过
            </Button>
            <Button
              type={decision === 'reject' ? 'primary' : 'default'}
              size="small"
              danger={decision === 'reject'}
              icon={<StopOutlined />}
              onClick={() => setDecision('reject')}
            >
              驳回
            </Button>
          </Space>
          <div>
            <div style={{ marginBottom: 6 }}>
              审核意见 {decision === 'reject' && <span style={{ color: '#ff4d4f' }}>(驳回必填)</span>}
            </div>
            <Input.TextArea
              rows={3}
              maxLength={512}
              placeholder="例如: 系统提示词存在越权风险, 请修改后重新提交"
              value={comment}
              onChange={(e) => setComment(e.target.value)}
            />
          </div>
          <div style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>
            通过后 Agent 可被外部调用/工作流调用/发起会话; 工作流可被手工/定时/Webhook 触发。驳回后需修改并重新保存以再次进入审核。
          </div>
        </Space>
      </Modal>
    </div>
  );
}