import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ReactFlow, ReactFlowProvider, Background, Controls, Handle, Position, type Node, type NodeProps } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { App, Button, Card, Descriptions, Drawer, Space, Spin, Tag, Typography } from 'antd';
import { ArrowLeftOutlined, StopOutlined } from '@ant-design/icons';
import { workflowApi, type ExecutionDetail, type WorkflowDefinition, type WorkflowNodeDef } from '@/api/workflow';
import { getErrorMessage } from '@/api/client';
import { formatDateTime } from '@/utils/format';

const NODE_TYPE_LABEL: Record<string, string> = {
  agent: 'Agent',
  mcp_tool: 'MCP 工具',
  http: 'HTTP',
  delay: '延迟',
  condition: '条件',
};

const STATUS_COLOR: Record<string, string> = {
  pending: '#bfbfbf',
  running: '#1677ff',
  success: '#52c41a',
  failed: '#ff4d4f',
  skipped: '#d9d9d9',
  waiting_approval: '#faad14',
  cancelled: '#8c8c8c',
};

const STATUS_LABEL: Record<string, string> = {
  pending: '等待依赖',
  running: '执行中',
  success: '成功',
  failed: '失败',
  skipped: '已跳过',
  waiting_approval: '等待审核',
  cancelled: '已取消',
};

const EXEC_STATUS: Record<string, { label: string; color: string }> = {
  running: { label: '执行中', color: 'processing' },
  waiting_approval: { label: '等待审核', color: 'warning' },
  success: { label: '成功', color: 'success' },
  failed: { label: '失败', color: 'error' },
  cancelled: { label: '已取消', color: 'default' },
};

function TraceNode({ data, selected }: NodeProps) {
  const status: string = (data.status as string) ?? 'pending';
  const color = STATUS_COLOR[status] ?? '#bfbfbf';
  const typeLabel: string = (data.typeLabel as string) ?? (data.type as string);
  return (
    <div
      style={{
        padding: '8px 14px',
        borderRadius: 8,
        border: `2px ${status === 'skipped' ? 'dashed' : 'solid'} ${color}`,
        background: status === 'success' ? '#f6ffed' : status === 'failed' ? '#fff2f0' : status === 'waiting_approval' ? '#fffbe6' : '#fff',
        minWidth: 150,
        outline: selected ? `2px solid ${color}` : undefined,
        boxShadow: status === 'running' ? `0 0 10px ${color}` : undefined,
      }}
    >
      <Handle type="target" position={Position.Left} style={{ background: color }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <Tag color={color} style={{ marginInlineEnd: 0 }}>{typeLabel}</Tag>
      </div>
      <div style={{ marginTop: 4, fontSize: 12, color: '#333' }}>{String(data.name)}</div>
      <div style={{ marginTop: 2, fontSize: 11, color }}>
        {STATUS_LABEL[status] ?? status}
        {(data.attempt as number) > 1 ? ` (第 ${data.attempt} 次)` : ''}
      </div>
      {(data.durationMs as number) != null && (
        <div style={{ fontSize: 10, color: '#999' }}>{String(data.durationMs)}ms</div>
      )}
      <Handle type="source" position={Position.Right} style={{ background: color }} />
    </div>
  );
}

const flowNodeTypes = { trace: TraceNode };

function layoutPositions(nodeDefs: WorkflowNodeDef[], edgeDefs: WorkflowDefinition['edges']): Record<string, { x: number; y: number }> {
  const incoming: Record<string, string[]> = {};
  nodeDefs.forEach((n) => (incoming[n.id] = []));
  edgeDefs.forEach((e) => incoming[e.target]?.push(e.source));
  const levels: Record<string, number> = {};
  let queue = nodeDefs.filter((n) => incoming[n.id].length === 0).map((n) => n.id);
  let level = 0;
  const seen = new Set<string>();
  while (queue.length > 0) {
    const next: string[] = [];
    for (const nid of queue) {
      if (seen.has(nid)) continue;
      seen.add(nid);
      levels[nid] = level;
      edgeDefs.forEach((e) => {
        if (e.source === nid && !seen.has(e.target)) next.push(e.target);
      });
    }
    queue = next;
    level++;
  }
  nodeDefs.forEach((n) => {
    if (!seen.has(n.id)) levels[n.id] = 0;
  });
  const byLevel: Record<number, number> = {};
  const positions: Record<string, { x: number; y: number }> = {};
  for (const n of nodeDefs) {
    const lv = levels[n.id];
    byLevel[lv] = (byLevel[lv] ?? 0) + 1;
    positions[n.id] = { x: lv * 280 + 40, y: ((byLevel[lv] - 1) % 5) * 140 + 40 };
  }
  return positions;
}

function prettyJson(value: unknown): string {
  if (value === undefined || value === null) return '—';
  try {
    return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function TraceInner() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [detail, setDetail] = useState<ExecutionDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await workflowApi.getExecution(id!);
      setDetail(res.data ?? null);
      setSelectedNodeId((cur) => cur ?? null);
    } catch (err) {
      message.error(getErrorMessage(err, '加载执行详情失败'));
    } finally {
      setLoading(false);
    }
  }, [id, message]);

  const isActive = detail?.status === 'running' || detail?.status === 'waiting_approval';

  useEffect(() => {
    load();
    const timer = setInterval(() => {
      if (isActive) load();
    }, 3000);
    return () => clearInterval(timer);
  }, [load, isActive]);

  const [workflowDef, setWorkflowDef] = useState<WorkflowDefinition | null>(null);
  useEffect(() => {
    if (detail && !workflowDef) {
      workflowApi.get(detail.workflow_id).then((res) => setWorkflowDef(res.data?.definition ?? null)).catch(() => undefined);
    }
  }, [detail, workflowDef]);

  const flowNodes: Node[] = useMemo(() => {
    if (!detail || !workflowDef) return [];
    const positions = layoutPositions(workflowDef.nodes, workflowDef.edges);
    return workflowDef.nodes.map((n) => {
      const rec = detail.nodes.find((r) => r.node_id === n.id);
      return {
        id: n.id,
        type: 'trace',
        position: positions[n.id] ?? { x: 40, y: 40 },
        data: {
          type: n.type,
          typeLabel: NODE_TYPE_LABEL[n.type] ?? n.type,
          name: rec?.node_name || n.name || n.id,
          status: rec?.status ?? 'pending',
          attempt: rec?.attempt ?? 0,
          durationMs: rec?.duration_ms,
        },
      };
    });
  }, [detail, workflowDef]);

  const flowEdges = useMemo(() => {
    if (!workflowDef) return [];
    return workflowDef.edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      label: e.condition === 'true' ? '是' : e.condition === 'false' ? '否' : undefined,
      animated: true,
      style: { stroke: '#b1b1b1' },
    }));
  }, [workflowDef]);

  const selectedRec = useMemo(
    () => detail?.nodes.find((n) => n.node_id === selectedNodeId) ?? null,
    [detail, selectedNodeId]
  );

  const onCancel = async () => {
    try {
      await workflowApi.cancelExecution(id!);
      message.success('已请求取消');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '取消失败'));
    }
  };

  if (loading && !detail) {
    return (
      <div style={{ textAlign: 'center', padding: 80 }}>
        <Spin size="large" />
      </div>
    );
  }
  if (!detail) return null;

  const meta = EXEC_STATUS[detail.status];
  const duration = detail.finished_at
    ? new Date(detail.finished_at).getTime() - new Date(detail.started_at).getTime()
    : null;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 120px)' }}>
      <Card
        size="small"
        style={{ marginBottom: 8 }}
        title={
          <Space>
            <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate(`/workflows/${detail.workflow_id}`)}>
              {detail.workflow_name}
            </Button>
            <Tag color={meta?.color}>{meta?.label ?? detail.status}</Tag>
            <span style={{ color: 'var(--color-text-secondary)', fontWeight: 400 }}>
              trace {detail.trace_id} · v{detail.workflow_version}
            </span>
          </Space>
        }
        extra={
          detail.status === 'running' || detail.status === 'waiting_approval' ? (
            <Button danger icon={<StopOutlined />} onClick={onCancel}>
              取消执行
            </Button>
          ) : null
        }
      >
        <Descriptions size="small" column={4}>
          <Descriptions.Item label="触发方式">{detail.trigger_type}</Descriptions.Item>
          <Descriptions.Item label="开始时间">{formatDateTime(detail.started_at)}</Descriptions.Item>
          <Descriptions.Item label="结束时间">{detail.finished_at ? formatDateTime(detail.finished_at) : '—'}</Descriptions.Item>
          <Descriptions.Item label="总耗时">{duration == null ? '—' : duration < 1000 ? `${duration}ms` : `${(duration / 1000).toFixed(1)}s`}</Descriptions.Item>
          {detail.error && (
            <Descriptions.Item label="错误" span={4}>
              <Typography.Text type="danger">{detail.error}</Typography.Text>
            </Descriptions.Item>
          )}
        </Descriptions>
      </Card>

      <Card size="small" title="执行追踪 (节点状态)" style={{ flex: 1 }}>
        <div style={{ height: 'calc(100vh - 360px)', minHeight: 380 }}>
          <ReactFlow
            nodes={flowNodes}
            edges={flowEdges}
            onNodeClick={(_: React.MouseEvent, node: Node) => setSelectedNodeId(node.id)}
            nodeTypes={flowNodeTypes}
            fitView
            nodesDraggable={false}
            elementsSelectable={true}
          >
            <Background gap={20} size={1} />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
      </Card>

      <Drawer
        title={selectedRec ? `${selectedRec.node_name} (${NODE_TYPE_LABEL[selectedRec.node_type] ?? selectedRec.node_type})` : '节点详情'}
        open={!!selectedRec}
        onClose={() => setSelectedNodeId(null)}
        width={560}
      >
        {selectedRec && (
          <div>
            <Space style={{ marginBottom: 12 }} wrap>
              <Tag color={STATUS_COLOR[selectedRec.status]}>{STATUS_LABEL[selectedRec.status] ?? selectedRec.status}</Tag>
              <span>尝试 {selectedRec.attempt} 次</span>
              <span>· {selectedRec.duration_ms}ms</span>
              {selectedRec.started_at && <span>· 开始 {formatDateTime(selectedRec.started_at)}</span>}
            </Space>
            {selectedRec.error && (
              <div style={{ marginBottom: 12 }}>
                <strong>错误:</strong>
                <div style={{ color: '#ff4d4f', whiteSpace: 'pre-wrap' }}>{selectedRec.error}</div>
              </div>
            )}
            {selectedRec.status === 'waiting_approval' && selectedRec.approval_id && (
              <div style={{ marginBottom: 12 }}>
                <Tag color="warning">
                  审核请求 {selectedRec.approval_id.slice(0, 8)}… (在审核中心处理)
                </Tag>
              </div>
            )}
            <Descriptions column={1} size="small" style={{ marginBottom: 8 }}>
              <Descriptions.Item label="输入 (已解析变量)">
                <pre style={{ margin: 0, fontSize: 12, whiteSpace: 'pre-wrap' }}>{prettyJson(selectedRec.input)}</pre>
              </Descriptions.Item>
              <Descriptions.Item label="输出">
                <pre style={{ margin: 0, fontSize: 12, whiteSpace: 'pre-wrap' }}>{prettyJson(selectedRec.output)}</pre>
              </Descriptions.Item>
            </Descriptions>
          </div>
        )}
      </Drawer>
    </div>
  );
}

export function ExecutionDetailPage() {
  return (
    <ReactFlowProvider>
      <TraceInner />
    </ReactFlowProvider>
  );
}