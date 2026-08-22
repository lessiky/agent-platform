import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ReactFlow, ReactFlowProvider, addEdge, Background, Controls, MiniMap, useEdgesState, useNodesState, Handle, Position, type Node, type Edge, type Connection, type NodeProps } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { App, Button, Card, Dropdown, Form, Input, InputNumber, Modal, Select, Space, Switch, Tag, Tooltip } from 'antd';
import { ArrowLeftOutlined, CloudDownloadOutlined, PlusOutlined, RobotOutlined, RocketOutlined, SaveOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { workflowApi, type AIGenerateResult, type Workflow, type WorkflowDefinition, type WorkflowNodeDef } from '@/api/workflow';
import { AIGenerateWorkflowModal } from './AIGenerateWorkflowModal';
import { agentApi } from '@/api/agent';
import { mcpApi } from '@/api/mcp';
import { getErrorMessage } from '@/api/client';
import type { Agent, MCPServer, MCPTool } from '@/types';

// ---------- 节点类型元信息 ----------

const NODE_TYPES_META: Record<string, { label: string; color: string; desc: string }> = {
  agent: { label: 'Agent', color: '#1677ff', desc: '调用 Agent 单轮对话' },
  mcp_tool: { label: 'MCP 工具', color: '#722ed1', desc: '调用 MCP 工具 (可触发审核)' },
  http: { label: 'HTTP', color: '#13c2c2', desc: '外部 HTTP 调用' },
  delay: { label: '延迟', color: '#faad14', desc: '等待指定秒数' },
  condition: { label: '条件', color: '#eb2f96', desc: '按表达式选择分支' },
};

// 节点出参字段 (与后端 workflow_engine 各节点 runOnce 的输出结构一致, 下游通过 $nodes.<id>.<field> 引用)
const NODE_OUTPUT_FIELDS: Record<string, { field: string; desc: string }[]> = {
  agent: [
    { field: 'reply', desc: 'Agent 应答文本' },
    { field: 'session_id', desc: '会话 ID' },
    { field: 'model_name', desc: '调用的模型' },
    { field: 'total_tokens', desc: '消耗 token 数' },
    { field: 'latency_ms', desc: '端到端耗时 (ms)' },
    { field: 'mcp_calls', desc: '本轮 MCP 工具调用' },
    { field: 'pending_approvals', desc: '待审核工具调用数' },
  ],
  mcp_tool: [
    { field: 'content', desc: '工具返回的内容块数组' },
    { field: 'text', desc: '展平后的工具文本 (下游引用推荐)' },
    { field: 'is_error', desc: '工具是否返回错误' },
  ],
  http: [
    { field: 'status_code', desc: 'HTTP 状态码' },
    { field: 'body', desc: '响应体 (JSON 时保留对象)' },
    { field: 'latency_ms', desc: '请求耗时 (ms)' },
  ],
  delay: [
    { field: 'waited_seconds', desc: '实际等待秒数' },
  ],
  condition: [
    { field: 'result', desc: '条件求值布尔结果' },
    { field: 'chosen', desc: '选中分支 ("true"/"false"), 决定走出哪条出边' },
  ],
};

// ---------- 自定义节点 ----------

function WorkflowNode({ data }: NodeProps) {
  const meta = NODE_TYPES_META[data.type as string] ?? { label: data.type, color: '#999' };
  return (
    <div
      style={{
        padding: '8px 14px',
        borderRadius: 8,
        border: `2px solid ${meta.color}`,
        background: '#fff',
        minWidth: 140,
        boxShadow: '0 1px 4px rgba(0,0,0,0.12)',
      }}
    >
      <Handle type="target" position={Position.Left} style={{ background: meta.color }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <Tag color={meta.color} style={{ marginInlineEnd: 0 }}>{meta.label}</Tag>
      </div>
      <div style={{ marginTop: 4, fontSize: 12, color: '#333' }}>{String(data.name)}</div>
      <Handle type="source" position={Position.Right} style={{ background: meta.color }} />
    </div>
  );
}

const flowNodeTypes = { workflow: WorkflowNode };

// ---------- DAG <-> ReactFlow 转换 ----------

function layoutPositions(nodeDefs: WorkflowNodeDef[], edgeDefs: WorkflowDefinition['edges']): Record<string, { x: number; y: number }> {
  // 简单分层布局: 入度 0 为第 0 层, BFS 递推
  const levels: Record<string, number> = {};
  const incoming: Record<string, string[]> = {};
  nodeDefs.forEach((n) => (incoming[n.id] = []));
  edgeDefs.forEach((e) => incoming[e.target]?.push(e.source));
  let queue = nodeDefs.filter((n) => incoming[n.id].length === 0).map((n) => n.id);
  let level = 0;
  const seen = new Set<string>();
  while (queue.length > 0) {
    const next: string[] = [];
    for (const id of queue) {
      if (seen.has(id)) continue;
      seen.add(id);
      levels[id] = level;
      edgeDefs.forEach((e) => {
        if (e.source === id && !seen.has(e.target)) next.push(e.target);
      });
    }
    queue = next;
    level++;
  }
  nodeDefs.forEach((n) => {
    if (!seen.has(n.id)) levels[n.id] = 0; // 环/孤立兜底
  });
  const byLevel: Record<number, number> = {};
  const positions: Record<string, { x: number; y: number }> = {};
  for (const n of nodeDefs) {
    const lv = levels[n.id];
    byLevel[lv] = (byLevel[lv] ?? 0) + 1;
    positions[n.id] = { x: lv * 260 + 40, y: ((byLevel[lv] - 1) % 5) * 130 + 40 };
  }
  return positions;
}

function toFlowNodes(def: WorkflowDefinition): Node[] {
  const positions = layoutPositions(def.nodes, def.edges);
  return def.nodes.map((n) => ({
    id: n.id,
    type: 'workflow',
    position: positions[n.id] ?? { x: 40, y: 40 },
    data: { type: n.type, name: n.name || n.id, nodeDef: n },
  }));
}

function toFlowEdges(def: WorkflowDefinition): Edge[] {
  return def.edges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    label: e.condition === 'true' ? '是' : e.condition === 'false' ? '否' : undefined,
    data: { condition: e.condition },
    animated: true,
  }));
}

function parseJsonField(text: string, label: string): Record<string, unknown> | null {
  const trimmed = (text ?? '').trim();
  if (!trimmed) return {};
  try {
    const value = JSON.parse(trimmed);
    if (value === null || typeof value !== 'object' || Array.isArray(value)) {
      throw new Error('必须是 JSON 对象');
    }
    return value;
  } catch (err) {
    throw new Error(`${label} 不是合法 JSON 对象: ${(err as Error).message}`);
  }
}

// ---------- 主组件 ----------

function EditorInner() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [form] = Form.useForm();
  const [saving, setSaving] = useState(false);
  const [triggerOpen, setTriggerOpen] = useState(false);
  const [triggerInput, setTriggerInput] = useState('{}');
  const [aiGenOpen, setAiGenOpen] = useState(false);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [schedForm] = Form.useForm();
  const [condModal, setCondModal] = useState<{ connection: { source: string; target: string }; edgeId?: string } | null>(null);
  const [condValue, setCondValue] = useState<'true' | 'false'>('true');
  const [agents, setAgents] = useState<Agent[]>([]);
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [tools, setTools] = useState<MCPTool[]>([]);
  const seqRef = useRef(1);

  const selectedNode = useMemo(
    () => nodes.find((n) => n.id === selectedNodeId) ?? null,
    [nodes, selectedNodeId]
  );
  const selectedDef = (selectedNode?.data?.nodeDef ?? null) as WorkflowNodeDef | null;
  const selectedMcpServerId = Form.useWatch('mcp_server_id', form);

  const load = useCallback(async () => {
    try {
      const res = await workflowApi.get(id!);
      if (!res.data) { message.error('加载工作流失败'); return; }
      setWorkflow(res.data);
      setNodes(toFlowNodes(res.data.definition));
      setEdges(toFlowEdges(res.data.definition));
      seqRef.current = Math.max(0, ...res.data.definition.nodes.map((n) => {
        const m = n.id.match(/(\d+)$/);
        return m ? parseInt(m[1], 10) : 0;
      })) + 1;
    } catch (err) {
      message.error(getErrorMessage(err, '加载工作流失败'));
    }
  }, [id, message, setNodes, setEdges]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    agentApi.list({ page: 1, size: 100 }).then((res) => setAgents(res.data?.items ?? [])).catch(() => undefined);
    mcpApi.list({ page: 1, size: 100 }).then((res) => setServers(res.data?.items ?? [])).catch(() => undefined);
  }, []);

  useEffect(() => {
    setTools([]);
    if (selectedMcpServerId) {
      mcpApi.listTools(selectedMcpServerId)
        .then((res) => setTools(res.data?.tools ?? []))
        .catch(() => setTools([]));
    }
  }, [selectedMcpServerId]);

  // 选中节点 -> 表单同步
  useEffect(() => {
    if (selectedDef) {
      const cfg = selectedDef.config ?? {};
      form.setFieldsValue({
        name: selectedDef.name,
        timeout_seconds: selectedDef.timeout_seconds || undefined,
        retry_enabled: !!selectedDef.retry,
        max_attempts: selectedDef.retry?.max_attempts || 1,
        interval_seconds: selectedDef.retry?.interval_seconds || 0,
        backoff: selectedDef.retry?.backoff || 'fixed',
        agent_id: (cfg.agent_id as string) || undefined,
        message: (cfg.message as string) || '',
        mcp_server_id: (cfg.mcp_server_id as string) || undefined,
        tool: (cfg.tool as string) || undefined,
        arguments: cfg.arguments ? JSON.stringify(cfg.arguments, null, 2) : '',
        method: (cfg.method as string) || 'GET',
        url: (cfg.url as string) || '',
        headers: cfg.headers ? JSON.stringify(cfg.headers, null, 2) : '',
        body: cfg.body ? JSON.stringify(cfg.body, null, 2) : '',
        seconds: (cfg.seconds as number) || 1,
        left: (cfg.left as string) || '',
        operator: (cfg.operator as string) || '==',
        right: (cfg.right as string) ?? '',
      });
    }
  }, [selectedNodeId, form]);

  const nextId = (prefix: string) => {
    const n = seqRef.current++;
    return `${prefix}${n}`;
  };

  const addNode = (type: string) => {
    const meta = NODE_TYPES_META[type];
    const nid = nextId('n');
    const defaults: Record<string, WorkflowNodeDef['config']> = {
      agent: { agent_id: '', message: '$inputs.message' },
      mcp_tool: { mcp_server_id: '', tool: '', arguments: {} },
      http: { method: 'GET', url: '' },
      delay: { seconds: 1 },
      condition: { left: '$inputs.value', operator: '==', right: '' },
    };
    const nodeDef: WorkflowNodeDef = { id: nid, type: type as WorkflowNodeDef['type'], name: meta.label, config: defaults[type] ?? {} };
    setNodes((nds) => [
      ...nds,
      {
        id: nid,
        type: 'workflow',
        position: { x: 80 + (nds.length % 5) * 200, y: 80 + (nds.length % 5) * 60 },
        data: { type, name: meta.label, nodeDef },
      },
    ]);
    setSelectedNodeId(nid);
  };

  const onConnect = useCallback(
    (conn: Connection) => {
      const source = nodes.find((n) => n.id === conn.source);
      if (source?.data?.type === 'condition') {
        setCondModal({ connection: conn });
        return;
      }
      setEdges((eds) => addEdge({ ...conn, id: nextId('e'), animated: true }, eds));
    },
    [nodes, setEdges]
  );

  const confirmCondEdge = () => {
    if (!condModal) return;
    if (condModal.edgeId) {
      setEdges((eds) =>
        eds.map((e) => (e.id === condModal.edgeId ? { ...e, label: condValue === 'true' ? '是' : '否', data: { condition: condValue } } : e))
      );
    } else {
      setEdges((eds) => addEdge({ ...condModal.connection, id: nextId('e'), animated: true, label: condValue === 'true' ? '是' : '否', data: { condition: condValue } }, eds));
    }
    setCondModal(null);
  };

  const onEdgeDoubleClick = useCallback((_: React.MouseEvent, edge: Edge) => {
    const source = nodes.find((n) => n.id === edge.source);
    if (source?.data?.type === 'condition') {
      setCondValue((edge.data?.condition as 'true' | 'false') ?? 'true');
      setCondModal({ connection: { source: edge.source, target: edge.target }, edgeId: edge.id });
    }
  }, [nodes]);

  const onNodeClick = useCallback((_: React.MouseEvent, node: Node) => {
    setSelectedNodeId(node.id);
  }, []);

  const onPaneClick = useCallback(() => setSelectedNodeId(null), []);

  const buildDefinition = useCallback((flowNodes: Node[]): WorkflowDefinition => {
    const nodeDefs: WorkflowNodeDef[] = flowNodes.map((n) => n.data.nodeDef as WorkflowNodeDef);
    const edgeDefs = edges
      .filter((e) => e.source && e.target)
      .map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        ...(e.data?.condition ? { condition: e.data.condition as 'true' | 'false' } : {}),
      }));
    return { version: 1, nodes: nodeDefs, edges: edgeDefs };
  }, [edges]);

  const collectSelectedConfig = useCallback((): { nodes: Node[]; updated: WorkflowNodeDef | null } => {
    if (!selectedDef) return { nodes, updated: null };
    const values = form.getFieldsValue();
    const config: Record<string, unknown> = {};
    switch (selectedDef.type) {
      case 'agent':
        config.agent_id = values.agent_id || '';
        config.message = values.message ?? '';
        break;
      case 'mcp_tool':
        config.mcp_server_id = values.mcp_server_id || '';
        config.tool = values.tool || '';
        config.arguments = parseJsonField(values.arguments ?? '', 'arguments');
        break;
      case 'http':
        config.method = (values.method || 'GET').toUpperCase();
        config.url = values.url || '';
        if (values.headers) config.headers = parseJsonField(values.headers, 'headers');
        if (values.body && config.method !== 'GET' && config.method !== 'DELETE') {
          config.body = parseJsonField(values.body, 'body');
        }
        break;
      case 'delay':
        config.seconds = values.seconds ?? 1;
        break;
      case 'condition':
        config.left = values.left ?? '';
        config.operator = values.operator || '==';
        config.right = values.right;
        break;
    }
    const updated: WorkflowNodeDef = {
      ...selectedDef,
      name: values.name || selectedDef.id,
      config,
      ...(values.timeout_seconds ? { timeout_seconds: values.timeout_seconds } : {}),
      ...(values.retry_enabled
        ? { retry: { max_attempts: values.max_attempts || 1, interval_seconds: values.interval_seconds || 0, backoff: values.backoff || 'fixed' } }
        : {}),
    };
    const nextNodes = nodes.map((n) => (n.id === selectedDef.id ? { ...n, data: { ...n.data, name: updated.name, nodeDef: updated } } : n));
    return { nodes: nextNodes, updated };
  }, [selectedDef, form, nodes]);

  // AI 生成确认 -> 用生成草稿替换画布 (名称/描述同步更新, 由用户保存后生效)
  const onAIGenerated = (result: AIGenerateResult) => {
    const def = result.definition;
    setWorkflow((prev) => (prev ? { ...prev, name: result.name, description: result.description } : prev));
    setNodes(toFlowNodes(def));
    setEdges(toFlowEdges(def));
    seqRef.current = Math.max(0, ...def.nodes.map((n) => {
      const m = n.id.match(/(\d+)$/);
      return m ? parseInt(m[1], 10) : 0;
    })) + 1;
    setSelectedNodeId(null);
    setAiGenOpen(false);
    message.success('AI 已生成工作流草稿, 请检查节点配置后保存');
  };

  const onSave = async (activateAfter = false) => {
    if (!workflow) return;
    let currentNodes = nodes;
    if (selectedNodeId) {
      try {
        const collected = collectSelectedConfig();
        currentNodes = collected.nodes;
      } catch (err) {
        message.error((err as Error).message);
        return;
      }
    }
    const def = buildDefinition(currentNodes);
    try {
      setSaving(true);
      const vres = await workflowApi.validate(def);
      if (!vres.data?.valid) {
        message.error('DAG 校验未通过');
        return;
      }
      const name = workflow.name;
      const res = await workflowApi.update(workflow.id, { name, definition: def });
      const saved = res.data;
      if (!saved) { message.error('保存失败'); return; }
      setWorkflow(saved);
      setNodes(currentNodes);
      message.success(activateAfter ? '已保存并激活' : `已保存 (v${saved.version})`);
      if (activateAfter && saved.status !== 'active') {
        const act = await workflowApi.activate(workflow.id);
        setWorkflow(act.data ?? null);
      }
    } catch (err) {
      message.error(getErrorMessage(err, '保存失败'));
    } finally {
      setSaving(false);
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
      const res = await workflowApi.trigger(workflow!.id, input);
      const execId = res.data?.id;
      if (!execId) { message.error('触发失败'); return; }
      message.success(`已触发执行 ${execId}`);
      setTriggerOpen(false);
      navigate(`/workflows/executions/${execId}`);
    } catch (err) {
      message.error(getErrorMessage(err, '触发失败'));
    }
  };

  const onSaveSchedule = async () => {
    const values = await schedForm.validateFields();
    try {
      let scheduleInput: Record<string, unknown> | undefined;
      if (values.schedule_input) {
        const trimmed = values.schedule_input.trim();
        if (trimmed) scheduleInput = JSON.parse(trimmed);
      }
      const res = await workflowApi.updateSchedule(workflow!.id, {
        enabled: values.enabled,
        cron: values.cron,
        input: scheduleInput,
      });
      setWorkflow(res.data ?? null);
      message.success('调度配置已保存');
      setScheduleOpen(false);
    } catch (err) {
      if (err && typeof err === 'object' && 'errorFields' in err) return;
      message.error(getErrorMessage(err, '保存调度失败'));
    }
  };

  const addNodeMenu = {
    items: Object.entries(NODE_TYPES_META).map(([key, meta]) => ({
      key,
      label: (
        <span>
          {meta.label} <span style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>{meta.desc}</span>
        </span>
      ),
    })),
    onClick: ({ key }: { key: string }) => addNode(key),
  };

  if (!workflow) return null;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 120px)' }}>
      <Card
        size="small"
        style={{ marginBottom: 8 }}
        title={
          <Space>
            <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/workflows')} />
            <span>{workflow.name}</span>
            <Tag color={workflow.status === 'active' ? 'green' : 'default'}>
              {workflow.status === 'active' ? '已激活' : workflow.status}
            </Tag>
            <span style={{ color: 'var(--color-text-secondary)', fontWeight: 400 }}>v{workflow.version}</span>
          </Space>
        }
        extra={
          <Space>
            <Dropdown menu={addNodeMenu}>
              <Button icon={<PlusOutlined />}>添加节点</Button>
            </Dropdown>
            <Button icon={<RobotOutlined />} onClick={() => setAiGenOpen(true)}>
              AI 生成
            </Button>
            <Button icon={<ThunderboltOutlined />} onClick={() => setScheduleOpen(true)}>
              定时调度
            </Button>
            <Tooltip title="Webhook: POST /api/v1/webhooks/workflows/{token}">
              <Tag color="blue">webhook</Tag>
            </Tooltip>
            <Button icon={<CloudDownloadOutlined />} onClick={() => setTriggerOpen(true)} disabled={workflow.status !== 'active'}>
              触发
            </Button>
            <Button icon={<SaveOutlined />} loading={saving} onClick={() => onSave(false)}>
              保存
            </Button>
            <Button type="primary" icon={<RocketOutlined />} loading={saving} onClick={() => onSave(true)}>
              保存并激活
            </Button>
          </Space>
        }
      >
        <div style={{ display: 'flex', gap: 8 }}>
          <div style={{ flex: 1, height: 560 }}>
            <ReactFlow
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={onConnect}
              onNodeClick={onNodeClick}
              onPaneClick={onPaneClick}
              onEdgeDoubleClick={onEdgeDoubleClick}
              nodeTypes={flowNodeTypes}
              deleteKeyCode={['Delete', 'Backspace']}
              fitView
            >
              <Background gap={20} size={1} />
              <Controls />
              <MiniMap />
            </ReactFlow>
          </div>
          <Card size="small" title="节点配置" style={{ width: 340, overflow: 'auto' }}>
            {!selectedDef ? (
              <div style={{ color: 'var(--color-text-secondary)' }}>
                点击画布中的节点进行配置。
                <div style={{ marginTop: 8, fontSize: 12 }}>
                  变量引用: <code>$inputs.x</code> · <code>$nodes.&lt;id&gt;.field</code> · <code>$execution.id</code>
                </div>
                <div style={{ marginTop: 4, fontSize: 12 }}>连线条件节点出口时选择 是/否 分支。</div>
                <div style={{ marginTop: 4, fontSize: 12 }}>选中节点后, 配置面板底部展示其支持的出参字段 (供下游节点引用)。</div>
              </div>
            ) : (
              <Form form={form} layout="vertical" size="small">
                <Form.Item name="name" label="节点名称">
                  <Input />
                </Form.Item>
                {selectedDef.type === 'agent' && (
                  <>
                    <Form.Item name="agent_id" label="Agent" rules={[{ required: true }]}>
                      <Select
                        showSearch
                        optionFilterProp="label"
                        options={agents.map((a) => ({ value: a.id, label: a.name }))}
                        placeholder="选择 Agent"
                      />
                    </Form.Item>
                    <Form.Item name="message" label="消息 (支持变量)" rules={[{ required: true }]}>
                      <Input.TextArea rows={3} placeholder="例如: 处理 $inputs.topic" />
                    </Form.Item>
                  </>
                )}
                {selectedDef.type === 'mcp_tool' && (
                  <>
                    <Form.Item name="mcp_server_id" label="MCP 服务器" rules={[{ required: true }]}>
                      <Select
                        showSearch
                        optionFilterProp="label"
                        options={servers.map((s) => ({ value: s.id, label: `${s.name} (${s.status})` }))}
                        placeholder="选择 MCP 服务器"
                      />
                    </Form.Item>
                    <Form.Item name="tool" label="工具" rules={[{ required: true }]}>
                      <Select
                        options={tools.map((t) => ({ value: t.name, label: t.name + (t.requires_approval ? ' [需审核]' : '') }))}
                        placeholder={selectedMcpServerId ? '选择工具' : '先选择 MCP 服务器'}
                        disabled={!selectedMcpServerId}
                      />
                    </Form.Item>
                    <Form.Item name="arguments" label="参数 (JSON, 支持变量)">
                      <Input.TextArea rows={4} placeholder='{"key": "$inputs.value"}' />
                    </Form.Item>
                  </>
                )}
                {selectedDef.type === 'http' && (
                  <>
                    <Space.Compact style={{ width: '100%' }}>
                      <Form.Item name="method" style={{ width: 110 }}>
                        <Select options={['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((m) => ({ value: m, label: m }))} />
                      </Form.Item>
                      <Form.Item name="url" style={{ flex: 1 }} rules={[{ required: true }]}>
                        <Input placeholder="https://example.com/api" />
                      </Form.Item>
                    </Space.Compact>
                    <Form.Item name="headers" label="Headers (JSON, 可选)">
                      <Input.TextArea rows={2} placeholder='{"X-Token": "***"}' />
                    </Form.Item>
                    <Form.Item name="body" label="Body (JSON, 可选)">
                      <Input.TextArea rows={3} placeholder='{"a": "$inputs.a"}' />
                    </Form.Item>
                  </>
                )}
                {selectedDef.type === 'delay' && (
                  <Form.Item name="seconds" label="等待秒数 (1-3600)">
                    <InputNumber min={1} max={3600} style={{ width: '100%' }} />
                  </Form.Item>
                )}
                {selectedDef.type === 'condition' && (
                  <>
                    <Form.Item name="left" label="左值 (变量/字面量)" rules={[{ required: true }]}>
                      <Input placeholder="$inputs.score" />
                    </Form.Item>
                    <Form.Item name="operator" label="操作符">
                      <Select options={['==', '!=', '>', '<', '>=', '<=', 'contains', 'exists'].map((o) => ({ value: o, label: o }))} />
                    </Form.Item>
                    <Form.Item name="right" label="右值 (exists 时留空)">
                      <Input placeholder="80" />
                    </Form.Item>
                  </>
                )}
                <div style={{ borderTop: '1px solid #f0f0f0', marginTop: 8, paddingTop: 8 }}>
                  <Form.Item name="retry_enabled" label="失败重试" valuePropName="checked" style={{ marginBottom: 4 }}>
                    <Switch size="small" />
                  </Form.Item>
                  <Form.Item noStyle shouldUpdate={(a, b) => a.retry_enabled !== b.retry_enabled}>
                    {({ getFieldValue }) =>
                      getFieldValue('retry_enabled') ? (
                        <>
                          <Form.Item name="max_attempts" label="最大尝试次数 (含首次)">
                            <InputNumber min={1} max={10} style={{ width: '100%' }} />
                          </Form.Item>
                          <Form.Item name="interval_seconds" label="重试间隔 (秒)">
                            <InputNumber min={0} max={600} style={{ width: '100%' }} />
                          </Form.Item>
                          <Form.Item name="backoff" label="退避策略">
                            <Select options={[{ value: 'fixed', label: '固定' }, { value: 'exponential', label: '指数' }]} />
                          </Form.Item>
                        </>
                      ) : null
                    }
                  </Form.Item>
                  <Form.Item name="timeout_seconds" label="节点超时 (秒, 默认 300)">
                    <InputNumber min={0} max={3600} style={{ width: '100%' }} />
                  </Form.Item>
                </div>
                <div style={{ borderTop: '1px solid #f0f0f0', marginTop: 8, paddingTop: 8 }}>
                  <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 6 }}>
                    节点出参 (下游可用 <code>{'$nodes.' + selectedDef.id + '.' + '<field>'}</code> 引用)
                  </div>
                  {(NODE_OUTPUT_FIELDS[selectedDef.type] ?? []).map((f) => (
                    <div key={f.field} style={{ display: 'flex', alignItems: 'baseline', gap: 6, fontSize: 12, marginBottom: 4 }}>
                      <code style={{ flexShrink: 0, color: '#1677ff' }}>{'$nodes.' + selectedDef.id + '.' + f.field}</code>
                      <span style={{ color: 'var(--color-text-secondary)' }}>{f.desc}</span>
                    </div>
                  ))}
                </div>
              </Form>
            )}
          </Card>
        </div>
      </Card>

      <AIGenerateWorkflowModal
        open={aiGenOpen}
        onClose={() => setAiGenOpen(false)}
        onGenerated={onAIGenerated}
        notice="确认后生成的工作流将替换当前画布 (未保存的修改会丢失)。"
      />

      <Modal title="触发执行" open={triggerOpen} onOk={onTrigger} onCancel={() => setTriggerOpen(false)} okText="触发">
        <p style={{ color: 'var(--color-text-secondary)' }}>
          输入工作流参数 (JSON 对象)。节点中可用 <code>$inputs.&lt;key&gt;</code> 引用。
        </p>
        <Input.TextArea rows={4} value={triggerInput} onChange={(e) => setTriggerInput(e.target.value)} />
      </Modal>

      <Modal
        title="定时调度 (Cron)"
        open={scheduleOpen}
        onOk={onSaveSchedule}
        onCancel={() => setScheduleOpen(false)}
        okText="保存"
      >
        <Form
          form={schedForm}
          layout="vertical"
          initialValues={{
            enabled: workflow.schedule_enabled,
            cron: workflow.schedule?.cron || '',
            schedule_input: workflow.schedule?.input ? JSON.stringify(workflow.schedule.input, null, 2) : '',
          }}
        >
          <Form.Item name="enabled" label="启用定时调度" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="cron" label="Cron 表达式 (5 段: 分 时 日 月 周, 时区 Asia/Shanghai)" rules={[{ required: true }]}>
            <Input placeholder="*/5 * * * *" />
          </Form.Item>
          <Form.Item name="schedule_input" label="触发输入 (JSON, 可选)">
            <Input.TextArea rows={3} placeholder='{"topic": "daily"}' />
          </Form.Item>
        </Form>
      </Modal>

      <Modal title="条件分支" open={!!condModal} onOk={confirmCondEdge} onCancel={() => setCondModal(null)} okText="确定">
        <p>该边从条件节点引出, 请选择条件命中值:</p>
        <Select
          style={{ width: '100%' }}
          value={condValue}
          onChange={setCondValue}
          options={[
            { value: 'true', label: '是 (条件为真)' },
            { value: 'false', label: '否 (条件为假)' },
          ]}
        />
      </Modal>
    </div>
  );
}

export function WorkflowEditorPage() {
  return (
    <ReactFlowProvider>
      <EditorInner />
    </ReactFlowProvider>
  );
}