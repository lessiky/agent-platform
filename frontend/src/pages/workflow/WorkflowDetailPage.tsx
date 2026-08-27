import { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { App, Button, Card, Col, Descriptions, Modal, Input, Popconfirm, Row, Space, Table, Tabs, Tag, Timeline, Typography } from 'antd';
import { ArrowLeftOutlined, CloudDownloadOutlined, DeleteOutlined, EditOutlined, PlayCircleOutlined, StopOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { workflowApi, type PrintOutputEntry, type Workflow, type WorkflowExecution, type WorkflowVersion } from '@/api/workflow';
import { getErrorMessage } from '@/api/client';
import { formatDateTime, timeAgo } from '@/utils/format';

const WF_STATUS_MAP: Record<string, { label: string; color: string }> = {
  draft: { label: '草稿', color: 'default' },
  active: { label: '已激活', color: 'green' },
  archived: { label: '已归档', color: 'orange' },
};

const EXEC_STATUS_MAP: Record<string, { label: string; color: string }> = {
  running: { label: '执行中', color: 'processing' },
  waiting_approval: { label: '等待审核', color: 'warning' },
  success: { label: '成功', color: 'success' },
  failed: { label: '失败', color: 'error' },
  cancelled: { label: '已取消', color: 'default' },
};

const TRIGGER_MAP: Record<string, string> = { manual: '手动', cron: '定时', webhook: 'Webhook' };

export function WorkflowDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [executions, setExecutions] = useState<WorkflowExecution[]>([]);
  const [execTotal, setExecTotal] = useState(0);
  const [execPage, setExecPage] = useState(1);
  const [versions, setVersions] = useState<WorkflowVersion[]>([]);
  const [triggerOpen, setTriggerOpen] = useState(false);
  const [triggerInput, setTriggerInput] = useState('{}');
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const [wf, execs, vers] = await Promise.all([
        workflowApi.get(id!),
        workflowApi.executions(id!, { page: execPage, size: 20 }),
        workflowApi.versions(id!),
      ]);
      setWorkflow(wf.data ?? null);
      setExecutions(execs.data?.items ?? []);
      setExecTotal(execs.data?.total ?? 0);
      setVersions(vers.data?.items ?? []);
    } catch (err) {
      message.error(getErrorMessage(err, '加载工作流详情失败'));
    } finally {
      setLoading(false);
    }
  }, [id, execPage, message]);

  useEffect(() => {
    load();
    const timer = setInterval(load, 10000);
    return () => clearInterval(timer);
  }, [load]);

  const onActivate = async () => {
    try {
      const res = await workflowApi.activate(id!);
      setWorkflow(res.data ?? null);
      message.success('已激活');
    } catch (err) {
      message.error(getErrorMessage(err, '激活失败'));
    }
  };

  const onArchive = async () => {
    try {
      const res = await workflowApi.archive(id!);
      setWorkflow(res.data ?? null);
      message.success('已归档');
    } catch (err) {
      message.error(getErrorMessage(err, '归档失败'));
    }
  };

  const onDelete = async () => {
    try {
      await workflowApi.remove(id!);
      message.success('已删除');
      navigate('/workflows');
    } catch (err) {
      message.error(getErrorMessage(err, '删除失败'));
    }
  };

  const onTrigger = async () => {
    let input: Record<string, unknown> = {};
    try {
      const trimmed = triggerInput.trim();
      if (trimmed) input = JSON.parse(trimmed);
    } catch {
      message.error('输入参数必须是合法 JSON');
      return;
    }
    try {
      const res = await workflowApi.trigger(id!, input);
      const execId = res.data?.id;
      if (!execId) { message.error('触发失败'); return; }
      setTriggerOpen(false);
      message.success(`已触发执行 ${execId}`);
      navigate(`/workflows/executions/${execId}`);
    } catch (err) {
      message.error(getErrorMessage(err, '触发失败'));
    }
  };

  const execColumns: ColumnsType<WorkflowExecution> = [
    {
      title: '执行 ID',
      dataIndex: 'id',
      width: 130,
      render: (v: string) => (
        <a onClick={() => navigate(`/workflows/executions/${v}`)}>{v.slice(0, 8)}…</a>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (s: string) => <Tag color={EXEC_STATUS_MAP[s]?.color}>{EXEC_STATUS_MAP[s]?.label ?? s}</Tag>,
    },
    { title: '触发方式', dataIndex: 'trigger_type', width: 100, render: (v: string) => TRIGGER_MAP[v] ?? v },
    { title: '版本', dataIndex: 'workflow_version', width: 70, render: (v: number) => `v${v}` },
    {
      title: '工作流输出',
      dataIndex: 'print_output',
      width: 260,
      render: (v?: PrintOutputEntry[] | null) =>
        v && v.length > 0 ? (
          <div>
            {v.map((entry, i) => (
              <div key={`${entry.node_id}-${i}`} style={{ color: entry.color || undefined, wordBreak: 'break-all' }}>
                {entry.node_name}：{entry.message}
              </div>
            ))}
          </div>
        ) : (
          '—'
        ),
    },
    {
      title: '开始时间',
      dataIndex: 'started_at',
      width: 170,
      render: (v: string) => <span title={formatDateTime(v)}>{timeAgo(v)}</span>,
    },
    {
      title: '耗时',
      width: 100,
      render: (_, r) => {
        if (!r.finished_at) return '—';
        const ms = new Date(r.finished_at).getTime() - new Date(r.started_at).getTime();
        return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
      },
    },
    { title: '错误', dataIndex: 'error', ellipsis: true, render: (v?: string) => v || '—' },
  ];

  if (!workflow) return null;
  const meta = WF_STATUS_MAP[workflow.status];

  return (
    <div>
      <Card
        size="small"
        style={{ marginBottom: 8 }}
        title={
          <Space>
            <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/workflows')} />
            <span>{workflow.name}</span>
            <Tag color={meta?.color}>{meta?.label}</Tag>
            <span style={{ color: 'var(--color-text-secondary)', fontWeight: 400 }}>
              v{workflow.version} · {workflow.definition?.nodes?.length ?? 0} 节点
            </span>
          </Space>
        }
        extra={
          <Space>
            <Button icon={<EditOutlined />} onClick={() => navigate(`/workflows/${id}/edit`)}>
              编排
            </Button>
            <Button
              icon={<PlayCircleOutlined />}
              onClick={() => setTriggerOpen(true)}
              disabled={workflow.status !== 'active'}
            >
              触发
            </Button>
            {workflow.status !== 'active' && (
              <Button type="primary" icon={<CloudDownloadOutlined />} onClick={onActivate}>
                {workflow.status === 'archived' ? '重新激活' : '激活'}
              </Button>
            )}
            {workflow.status === 'active' && (
              <Popconfirm title="归档后停止调度且不可触发, 确定?" onConfirm={onArchive}>
                <Button icon={<StopOutlined />}>归档</Button>
              </Popconfirm>
            )}
            <Popconfirm title="确定删除该工作流? 执行历史保留。" onConfirm={onDelete}>
              <Button danger icon={<DeleteOutlined />}>
                删除
              </Button>
            </Popconfirm>
          </Space>
        }
      >
        <Row gutter={16}>
          <Col span={12}>
            <Descriptions column={1} size="small">
              <Descriptions.Item label="工作流 ID">
                <Typography.Text className="mono-text" copyable>
                  {workflow.id}
                </Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label="描述">{workflow.description || '—'}</Descriptions.Item>
              <Descriptions.Item label="定时调度">
                {workflow.schedule_enabled && workflow.schedule ? (
                  <Tag color="blue">{workflow.schedule.cron}</Tag>
                ) : (
                  '未启用'
                )}
              </Descriptions.Item>
              <Descriptions.Item label="Webhook 端点">
                <code style={{ fontSize: 12 }}>POST /api/v1/webhooks/workflows/{workflow.webhook_token}</code>
              </Descriptions.Item>
              <Descriptions.Item label="创建时间">{formatDateTime(workflow.created_at)}</Descriptions.Item>
              <Descriptions.Item label="更新时间">{formatDateTime(workflow.updated_at)}</Descriptions.Item>
            </Descriptions>
          </Col>
          <Col span={12}>
            <div style={{ fontSize: 13, color: 'var(--color-text-secondary)' }}>节点概览</div>
            <div style={{ marginTop: 8, display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              {(workflow.definition?.nodes ?? []).map((n) => (
                <Tag key={n.id} color="geekblue">
                  {n.name || n.id} ({n.type})
                </Tag>
              ))}
            </div>
          </Col>
        </Row>
      </Card>

      <Tabs
        items={[
          {
            key: 'executions',
            label: '执行历史',
            children: (
              <Table
                rowKey="id"
                loading={loading}
                columns={execColumns}
                dataSource={executions}
                pagination={{
                  current: execPage,
                  pageSize: 20,
                  total: execTotal,
                  onChange: (p) => setExecPage(p),
                }}
              />
            ),
          },
          {
            key: 'versions',
            label: `版本 (${versions.length})`,
            children: (
              <Timeline
                items={versions.map((v) => ({
                  color: v.version === workflow.version ? 'green' : 'gray',
                  children: (
                    <div>
                      <strong>v{v.version}</strong>
                      {v.version === workflow.version && <Tag color="green" style={{ marginLeft: 8 }}>当前</Tag>}
                      <div style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>
                        {formatDateTime(v.created_at)} · {v.definition.nodes.length} 节点 / {v.definition.edges.length} 边
                      </div>
                    </div>
                  ),
                }))}
              />
            ),
          },
        ]}
      />

      <Modal title="触发执行" open={triggerOpen} onOk={onTrigger} onCancel={() => setTriggerOpen(false)} okText="触发">
        <p style={{ color: 'var(--color-text-secondary)' }}>
          输入工作流参数 (JSON 对象)。节点中可用 <code>$inputs.&lt;key&gt;</code> 引用。
        </p>
        <Input.TextArea rows={4} value={triggerInput} onChange={(e) => setTriggerInput(e.target.value)} />
      </Modal>
    </div>
  );
}