import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { App, Button, Card, Descriptions, Divider, Input, Modal, Space, Spin, Table, Tabs, Tag, Tooltip } from 'antd';
import { CheckOutlined, EyeOutlined, ReloadOutlined, StopOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { reviewApi } from '@/api/review';
import { agentApi } from '@/api/agent';
import { kbApi } from '@/api/kb';
import { mcpApi } from '@/api/mcp';
import { getErrorMessage } from '@/api/client';
import { useAuthStore } from '@/store/auth-store';
import type { Agent, AgentBoundMCP, BoundSkillView, KBCategory } from '@/types';
import type { Workflow, WorkflowNodeDef } from '@/api/workflow';
import {
  AGENT_STATUS_MAP,
  KB_SEARCH_MODE_MAP,
  MCP_STATUS_MAP,
  REVIEW_STATUS_MAP,
  SKILL_STATUS_MAP,
  SKILL_USAGE_MODE_MAP,
} from '@/utils/constants';
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

  const [detail, setDetail] = useState<DetailTarget | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  // 详情弹窗补充信息: null = 尚未加载或加载失败 (绑定关系/名称解析失败时降级展示, 不阻断)
  const [agentExtra, setAgentExtra] = useState<AgentDetailExtra | null>(null);
  const [nodeRefs, setNodeRefs] = useState<WorkflowNodeRefs | null>(null);
  // 弹窗打开序号: 防止关闭后仍在飞的请求把旧数据回填到下一次打开
  const detailSeq = useRef(0);
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

  // 详情弹窗: 主体信息来自队列数据 (即编辑页已保存的完整配置),
  // 绑定关系/名称解析异步补充; 单个请求失败仅降级展示, 不阻断弹窗
  const openDetail = async (kind: 'agent' | 'workflow', item: Agent | Workflow) => {
    const seq = ++detailSeq.current;
    setDetail(kind === 'agent' ? { kind, item: item as Agent } : { kind, item: item as Workflow });
    setAgentExtra(null);
    setNodeRefs(null);
    setDetailOpen(true);
    setDetailLoading(true);
    try {
      if (kind === 'agent') {
        const [mcpRes, skillRes, catRes] = await Promise.allSettled([
          agentApi.listBoundMCPS(item.id),
          agentApi.listBoundSkills(item.id),
          kbApi.listCategories(),
        ]);
        if (seq !== detailSeq.current) return;
        setAgentExtra({
          mcps: mcpRes.status === 'fulfilled' ? (mcpRes.value.data?.items ?? []) : null,
          skills: skillRes.status === 'fulfilled' ? (skillRes.value.data?.skills ?? []) : null,
          kbCats: catRes.status === 'fulfilled' ? (catRes.value.data?.items ?? []) : null,
        });
      } else {
        const [agentRes, mcpRes] = await Promise.allSettled([
          agentApi.list({ size: 100 }),
          mcpApi.list({ size: 100 }),
        ]);
        if (seq !== detailSeq.current) return;
        const agentNames: Record<string, string> = {};
        if (agentRes.status === 'fulfilled') {
          for (const a of agentRes.value.data?.items ?? []) agentNames[a.id] = a.name;
        }
        const mcpNames: Record<string, string> = {};
        if (mcpRes.status === 'fulfilled') {
          for (const m of mcpRes.value.data?.items ?? []) mcpNames[m.id] = m.name;
        }
        setNodeRefs({ agentNames, mcpNames });
      }
    } finally {
      if (seq === detailSeq.current) setDetailLoading(false);
    }
  };

  const closeDetail = () => {
    detailSeq.current += 1;
    setDetailOpen(false);
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
          <Button size="small" icon={<EyeOutlined />} onClick={() => openDetail('agent', r)}>详情</Button>
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
          <Button size="small" icon={<EyeOutlined />} onClick={() => openDetail('workflow', r)}>详情</Button>
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

      <Modal
        title={detail ? (detail.kind === 'agent' ? `Agent 详情: ${detail.item.name}` : `工作流详情: ${detail.item.name}`) : ''}
        open={detailOpen}
        onCancel={closeDetail}
        footer={<Button onClick={closeDetail}>关闭</Button>}
        width={880}
        destroyOnClose
        styles={{ body: { maxHeight: '68vh', overflowY: 'auto' } }}
      >
        {detail?.kind === 'agent' && (
          <AgentDetailBody agent={detail.item} extra={agentExtra} loading={detailLoading} />
        )}
        {detail?.kind === 'workflow' && (
          <WorkflowDetailBody wf={detail.item} refs={nodeRefs} loading={detailLoading} />
        )}
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

// ---------- 详情弹窗: 展示与编辑页一致的完整信息 (只读) ----------

type DetailTarget = { kind: 'agent'; item: Agent } | { kind: 'workflow'; item: Workflow };

interface AgentDetailExtra {
  mcps: AgentBoundMCP[] | null;
  skills: BoundSkillView[] | null;
  kbCats: KBCategory[] | null;
}

interface WorkflowNodeRefs {
  agentNames: Record<string, string>;
  mcpNames: Record<string, string>;
}

const DASH = <span style={{ color: 'var(--color-text-secondary)' }}>—</span>;

const PRE_STYLE: CSSProperties = {
  margin: 0,
  padding: 12,
  background: 'var(--color-bg)',
  border: '1px solid var(--color-border)',
  borderRadius: 'var(--radius-sm)',
  fontSize: 12,
  lineHeight: 1.6,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
  maxHeight: 240,
  overflow: 'auto',
};

function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <Divider orientation="left" plain style={{ margin: '18px 0 12px', fontSize: 13 }}>
      {children}
    </Divider>
  );
}

function prettyJson(value: unknown): string {
  if (value === undefined || value === null) return '';
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function AgentDetailBody({ agent, extra, loading }: { agent: Agent; extra: AgentDetailExtra | null; loading: boolean }) {
  const cfg = agent.config ?? {};
  const readOnlyIds = new Set(cfg.knowledge_categories_readonly ?? []);
  const kbCategoryIds = [...new Set([...(cfg.knowledge_categories ?? []), ...(cfg.knowledge_categories_readonly ?? [])])];
  const catById = new Map((extra?.kbCats ?? []).map((c) => [c.id, c]));
  const usageMode = cfg.skills_usage_mode ?? 'metadata_injection';
  const usage = (SKILL_USAGE_MODE_MAP as Record<string, { label: string; hint: string }>)[usageMode];
  const kbMode = (KB_SEARCH_MODE_MAP as Record<string, { label: string; hint: string }>)[cfg.kb_search_mode ?? 'auto'];
  const statusMeta = AGENT_STATUS_MAP[agent.status];
  const reviewMeta = REVIEW_STATUS_MAP[agent.review_status];

  const mcpColumns: ColumnsType<AgentBoundMCP> = [
    { title: '名称', dataIndex: 'name', render: (v: string, r) => v || r.id },
    {
      title: '状态',
      width: 100,
      render: (_, r) => {
        const m = MCP_STATUS_MAP[r.status];
        return m ? <Tag color={m.color}>{m.label}</Tag> : r.status;
      },
    },
    { title: '已发现工具', width: 100, render: (_, r) => `${r.tools?.length ?? 0} 个` },
  ];

  const skillColumns: ColumnsType<BoundSkillView> = [
    { title: '名称', dataIndex: 'name', width: 180 },
    { title: '版本', width: 70, render: (v: number) => `v${v}` },
    {
      title: '状态',
      width: 80,
      render: (_, r) => {
        const m = SKILL_STATUS_MAP[r.status];
        return m ? <Tag color={m.color}>{m.label}</Tag> : r.status;
      },
    },
    { title: '描述', dataIndex: 'description', ellipsis: true, render: (v: string) => v || DASH },
  ];

  return (
    <Spin spinning={loading} tip="加载绑定信息...">
      <div>
        <Descriptions column={2} size="small" bordered>
          <Descriptions.Item label="名称">{agent.name}</Descriptions.Item>
          <Descriptions.Item label="版本">v{agent.version}</Descriptions.Item>
          <Descriptions.Item label="描述" span={2}>{agent.description || DASH}</Descriptions.Item>
          <Descriptions.Item label="状态">
            {statusMeta ? <Tag color={statusMeta.color}>{statusMeta.label}</Tag> : agent.status}
          </Descriptions.Item>
          <Descriptions.Item label="模型">{cfg.model || DASH}</Descriptions.Item>
          <Descriptions.Item label="创建时间">{formatDateTime(agent.created_at)}</Descriptions.Item>
          <Descriptions.Item label="更新时间">{formatDateTime(agent.updated_at)}</Descriptions.Item>
        </Descriptions>

        <SectionTitle>模型参数</SectionTitle>
        <Descriptions column={3} size="small" bordered>
          <Descriptions.Item label="Temperature">{cfg.temperature ?? DASH}</Descriptions.Item>
          <Descriptions.Item label="最大 Token 数">{cfg.max_tokens ?? DASH}</Descriptions.Item>
          <Descriptions.Item label="工具调用轮数上限">{cfg.max_tool_rounds ?? '默认 5'}</Descriptions.Item>
        </Descriptions>

        <SectionTitle>系统提示词</SectionTitle>
        {cfg.system_prompt ? <pre style={PRE_STYLE}>{cfg.system_prompt}</pre> : DASH}

        <SectionTitle>MCP 服务器</SectionTitle>
        <Table
          size="small"
          rowKey="id"
          columns={mcpColumns}
          dataSource={extra?.mcps ?? []}
          pagination={false}
          locale={{ emptyText: extra?.mcps === null ? '加载失败 (需要 agent:read 权限)' : '未绑定 MCP 服务器' }}
        />

        <SectionTitle>可用工具</SectionTitle>
        {cfg.tools && cfg.tools.length > 0 ? (
          <Space size={[0, 6]} wrap>
            {cfg.tools.map((t) => (
              <Tag key={t}>{t}</Tag>
            ))}
          </Space>
        ) : (
          <span style={{ color: 'var(--color-text-secondary)' }}>不限制 (绑定 MCP 的全部工具均可用)</span>
        )}

        <SectionTitle>技能包</SectionTitle>
        <Table
          size="small"
          rowKey="id"
          columns={skillColumns}
          dataSource={extra?.skills ?? []}
          pagination={false}
          locale={{ emptyText: extra?.skills === null ? '加载失败 (需要 agent:read 权限)' : '未绑定技能包' }}
        />
        <div style={{ marginTop: 8, fontSize: 13 }}>
          技能注入模式: {usage ? `${usage.label} — ${usage.hint}` : usageMode}
        </div>

        <SectionTitle>知识库</SectionTitle>
        <div style={{ marginBottom: 8 }}>
          {kbCategoryIds.length > 0 ? (
            <Space size={[0, 6]} wrap>
              {kbCategoryIds.map((cid) => (
                <Tag key={cid} color={readOnlyIds.has(cid) ? 'orange' : 'default'}>
                  {catById.get(cid)?.name ?? cid}
                  {readOnlyIds.has(cid) ? ' (只读)' : ''}
                </Tag>
              ))}
            </Space>
          ) : (
            <span style={{ color: 'var(--color-text-secondary)' }}>未绑定知识库分类</span>
          )}
        </div>
        <div style={{ fontSize: 13 }}>
          检索模式: {kbMode ? `${kbMode.label} — ${kbMode.hint}` : cfg.kb_search_mode ?? 'auto'}
        </div>

        <SectionTitle>审核信息</SectionTitle>
        <Descriptions column={2} size="small" bordered>
          <Descriptions.Item label="审核状态">
            {reviewMeta ? <Tag color={reviewMeta.color}>{reviewMeta.label}</Tag> : agent.review_status}
          </Descriptions.Item>
          <Descriptions.Item label="审核人">{agent.reviewed_by ?? DASH}</Descriptions.Item>
          <Descriptions.Item label="审核时间">{agent.reviewed_at ? formatDateTime(agent.reviewed_at) : DASH}</Descriptions.Item>
          <Descriptions.Item label="审核意见">{agent.review_comment || '无'}</Descriptions.Item>
        </Descriptions>
      </div>
    </Spin>
  );
}

const WF_STATUS_MAP: Record<Workflow['status'], { label: string; color: string }> = {
  draft: { label: '草稿', color: 'default' },
  active: { label: '已激活', color: 'green' },
  archived: { label: '已归档', color: 'orange' },
};

const WF_NODE_TYPE_MAP: Record<WorkflowNodeDef['type'], { label: string; color: string }> = {
  agent: { label: 'Agent', color: 'purple' },
  mcp_tool: { label: 'MCP 工具', color: 'geekblue' },
  http: { label: 'HTTP 请求', color: 'blue' },
  delay: { label: '等待', color: 'default' },
  condition: { label: '条件分支', color: 'gold' },
  print: { label: '输出', color: 'cyan' },
};

// 节点引用摘要 (与编辑页节点面板一致: Agent 名称 / MCP+工具 / URL / 参数等)
function nodeHint(n: WorkflowNodeDef, refs: WorkflowNodeRefs | null): string {
  const c = n.config ?? {};
  switch (n.type) {
    case 'agent': {
      const id = String(c.agent_id ?? '');
      if (!id) return 'Agent 未指定';
      return `Agent: ${refs?.agentNames[id] ?? `${id.slice(0, 8)}…`}`;
    }
    case 'mcp_tool': {
      const mcpId = String(c.mcp_server_id ?? '');
      const mcp = mcpId ? (refs?.mcpNames[mcpId] ?? `${mcpId.slice(0, 8)}…`) : '未指定';
      return `MCP: ${mcp} · 工具: ${String(c.tool ?? '未指定')}`;
    }
    case 'http':
      return [String(c.method ?? 'GET').toUpperCase(), String(c.url ?? '')].filter(Boolean).join(' ');
    case 'delay':
      return `等待 ${String(c.seconds ?? '—')} 秒`;
    case 'condition':
      return [String(c.left ?? ''), String(c.operator ?? ''), c.operator === 'exists' ? '' : String(c.right ?? '')]
        .filter(Boolean)
        .join(' ');
    case 'print':
      return String(c.print_message ?? '');
    default:
      return '';
  }
}

function WorkflowDetailBody({ wf, refs, loading }: { wf: Workflow; refs: WorkflowNodeRefs | null; loading: boolean }) {
  const nodes = wf.definition?.nodes ?? [];
  const edges = wf.definition?.edges ?? [];
  const nodeName = (id: string) => nodes.find((n) => n.id === id)?.name || id;
  const statusMeta = WF_STATUS_MAP[wf.status];
  const reviewMeta = REVIEW_STATUS_MAP[wf.review_status];

  const nodeColumns: ColumnsType<WorkflowNodeDef> = [
    {
      title: '节点',
      width: 260,
      render: (_, n) => (
        <div>
          <div style={{ fontWeight: 500 }}>{n.name || n.id}</div>
          <div style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>{nodeHint(n, refs)}</div>
        </div>
      ),
    },
    {
      title: '类型',
      width: 110,
      render: (_, n) => {
        const m = WF_NODE_TYPE_MAP[n.type];
        return m ? <Tag color={m.color}>{m.label}</Tag> : n.type;
      },
    },
    { title: '重试', width: 100, render: (_, n) => (n.retry?.max_attempts ? `最多 ${n.retry.max_attempts} 次` : '—') },
    { title: '超时', width: 80, render: (_, n) => (n.timeout_seconds ? `${n.timeout_seconds}s` : '—') },
  ];

  return (
    <Spin spinning={loading} tip="加载节点引用...">
      <div>
        <Descriptions column={2} size="small" bordered>
          <Descriptions.Item label="名称">{wf.name}</Descriptions.Item>
          <Descriptions.Item label="版本">v{wf.version}</Descriptions.Item>
          <Descriptions.Item label="描述" span={2}>{wf.description || DASH}</Descriptions.Item>
          <Descriptions.Item label="状态">
            {statusMeta ? <Tag color={statusMeta.color}>{statusMeta.label}</Tag> : wf.status}
          </Descriptions.Item>
          <Descriptions.Item label="节点 / 连接">{`${nodes.length} 节点 / ${edges.length} 连接`}</Descriptions.Item>
          <Descriptions.Item label="创建时间">{formatDateTime(wf.created_at)}</Descriptions.Item>
          <Descriptions.Item label="更新时间">{formatDateTime(wf.updated_at)}</Descriptions.Item>
        </Descriptions>

        <SectionTitle>定时调度</SectionTitle>
        {wf.schedule_enabled && wf.schedule ? (
          <Space size="middle" wrap>
            <span>
              Cron: <Tag color="blue">{wf.schedule.cron}</Tag>
            </span>
            {wf.schedule.timezone && <span>时区: {wf.schedule.timezone}</span>}
            {wf.schedule.input && Object.keys(wf.schedule.input).length > 0 && (
              <span>
                调度入参: <code style={{ fontSize: 12 }}>{JSON.stringify(wf.schedule.input)}</code>
              </span>
            )}
          </Space>
        ) : (
          DASH
        )}

        <SectionTitle>Webhook</SectionTitle>
        <code style={{ fontSize: 12 }}>POST /api/v1/webhooks/workflows/{wf.webhook_token}</code>

        <SectionTitle>输入 / 输出参数</SectionTitle>
        <div style={{ display: 'flex', gap: 12 }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ marginBottom: 6, fontSize: 12, color: 'var(--color-text-secondary)' }}>input_schema</div>
            {wf.input_schema ? <pre style={PRE_STYLE}>{prettyJson(wf.input_schema)}</pre> : DASH}
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ marginBottom: 6, fontSize: 12, color: 'var(--color-text-secondary)' }}>output_schema</div>
            {wf.output_schema ? <pre style={PRE_STYLE}>{prettyJson(wf.output_schema)}</pre> : DASH}
          </div>
        </div>

        <SectionTitle>节点详情 ({nodes.length})</SectionTitle>
        <Table
          size="small"
          rowKey="id"
          columns={nodeColumns}
          dataSource={nodes}
          pagination={false}
          expandable={{
            expandedRowRender: (n) =>
              n.config && Object.keys(n.config).length > 0 ? (
                <pre style={PRE_STYLE}>{prettyJson(n.config)}</pre>
              ) : (
                <span style={{ color: 'var(--color-text-secondary)' }}>无额外参数</span>
              ),
          }}
        />
        <div style={{ marginTop: 6, fontSize: 12, color: 'var(--color-text-secondary)' }}>
          点击行首展开查看节点完整参数; 紫色 Agent 节点引用的 Agent 需审核通过后工作流才能运行 (运行前服务端会预检)。
        </div>

        <SectionTitle>连接 ({edges.length})</SectionTitle>
        {edges.length > 0 ? (
          <div>
            {edges.map((e) => (
              <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, fontSize: 13 }}>
                <Tag style={{ marginInlineEnd: 0 }}>{nodeName(e.source)}</Tag>
                <span>→</span>
                <Tag style={{ marginInlineEnd: 0 }}>{nodeName(e.target)}</Tag>
                {e.condition && <Tag color={e.condition === 'true' ? 'green' : 'orange'}>{e.condition} 分支</Tag>}
              </div>
            ))}
          </div>
        ) : (
          DASH
        )}

        <SectionTitle>审核信息</SectionTitle>
        <Descriptions column={2} size="small" bordered>
          <Descriptions.Item label="审核状态">
            {reviewMeta ? <Tag color={reviewMeta.color}>{reviewMeta.label}</Tag> : wf.review_status}
          </Descriptions.Item>
          <Descriptions.Item label="审核人">{wf.reviewed_by ?? DASH}</Descriptions.Item>
          <Descriptions.Item label="审核时间">{wf.reviewed_at ? formatDateTime(wf.reviewed_at) : DASH}</Descriptions.Item>
          <Descriptions.Item label="审核意见">{wf.review_comment || '无'}</Descriptions.Item>
        </Descriptions>
      </div>
    </Spin>
  );
}
