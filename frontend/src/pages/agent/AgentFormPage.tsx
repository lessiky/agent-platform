import { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  Alert,
  App,
  Button,
  Card,
  Divider,
  Form,
  Input,
  InputNumber,
  Result,
  Select,
  Slider,
  Space,
  Spin,
} from 'antd';
import { ArrowLeftOutlined, SaveOutlined } from '@ant-design/icons';
import { agentApi } from '@/api/agent';
import { mcpApi } from '@/api/mcp';
import { modelApi } from '@/api/model';
import { skillApi } from '@/api/skill';
import { getErrorMessage } from '@/api/client';
import { MCP_STATUS_MAP, SKILL_USAGE_MODE_MAP } from '@/utils/constants';
import type { Agent, AgentBoundMCP, MCPServer, ModelTemplate, Skill, SkillsUsageMode } from '@/types';

interface AgentFormValues {
  name: string;
  description?: string;
  model: string;
  system_prompt?: string;
  temperature?: number;
  max_tokens?: number;
  max_tool_rounds?: number;
  tools?: string[];
  mcp_ids?: string[];
  skills?: string[];
  skills_usage_mode?: string;
}

export function AgentFormPage() {
  const { id } = useParams<{ id: string }>();
  const isEdit = Boolean(id);
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [form] = Form.useForm<AgentFormValues>();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [loading, setLoading] = useState(isEdit);
  const [submitting, setSubmitting] = useState(false);

  const [models, setModels] = useState<ModelTemplate[]>([]);
  const [mcps, setMcps] = useState<MCPServer[]>([]);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [toolOptions, setToolOptions] = useState<{ value: string; label: string }[]>([]);
  const [loadingTools, setLoadingTools] = useState(false);

  // 模型下拉选项 (M4 模板; 编辑时保留当前值即使模板已不存在)
  const modelOptions = useMemo(() => {
    const options = models.map((m) => ({
      value: m.name,
      label: `${m.name} (${m.model})`,
    }));
    if (isEdit && agent?.config?.model && !options.some((o) => o.value === agent.config.model)) {
      options.unshift({ value: agent.config.model, label: `${agent.config.model} (自定义)` });
    }
    return options;
  }, [models, isEdit, agent]);

  // 绑定 MCP 变化时, 重新拉取可用工具 (取所有绑定 MCP 已发现工具的并集)
  const watchedMcpIds = Form.useWatch('mcp_ids', form);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      // 模型模板列表
      try {
        const res = await modelApi.list({ size: 100 });
        if (!cancelled) setModels(res.data?.items ?? []);
      } catch {
        // 模型加载失败不阻塞表单
      }
      // MCP 服务器列表
      try {
        const res = await mcpApi.list({ size: 100 });
        if (!cancelled) setMcps(res.data?.items ?? []);
      } catch {
        // MCP 加载失败不阻塞表单
      }
      // 技能包列表 (M9)
      try {
        const res = await skillApi.list({ size: 100 });
        if (!cancelled) setSkills(res.data?.items ?? []);
      } catch {
        // 技能加载失败不阻塞表单
      }
      // 编辑模式: 加载 Agent + 绑定 MCP
      if (isEdit && id) {
        try {
          const [agentRes, boundRes, skillRes] = await Promise.all([
            agentApi.getById(id),
            agentApi.listBoundMCPS(id),
            agentApi.listBoundSkills(id),
          ]);
          if (cancelled) return;
          const a = agentRes.data?.agent;
          setAgent(a ?? null);
          if (a) {
            const boundMCPS: AgentBoundMCP[] = boundRes.data?.items ?? [];
            form.setFieldsValue({
              name: a.name,
              description: a.description,
              model: a.config.model,
              system_prompt: a.config.system_prompt,
              temperature: a.config.temperature,
              max_tokens: a.config.max_tokens,
              max_tool_rounds: a.config.max_tool_rounds,
              tools: a.config.tools,
              mcp_ids: boundMCPS.map((m) => m.id),
              skills: (skillRes.data?.skills ?? []).map((s) => s.id),
              skills_usage_mode: a.config.skills_usage_mode ?? 'metadata_injection',
            });
            if (boundMCPS.length > 0) {
              applyToolOptions(boundMCPS);
            }
          }
        } catch (err) {
          if (!cancelled) {
            message.error(getErrorMessage(err, '加载 Agent 失败'));
            navigate('/agents');
          }
        } finally {
          if (!cancelled) setLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isEdit, id]);

  // 用户切换绑定 MCP 时联动刷新工具选项
  const prevMcpIdsRef = useRef<string>('');
  useEffect(() => {
    const key = JSON.stringify(watchedMcpIds ?? []);
    if (key === prevMcpIdsRef.current) return;
    prevMcpIdsRef.current = key;
    if (loading) return; // 初始加载由上面的逻辑处理
    const selected = (watchedMcpIds ?? []).map((mid) => mcps.find((m) => m.id === mid)).filter(Boolean) as MCPServer[];
    if (selected.length === 0) {
      setToolOptions([]);
      return;
    }
    (async () => {
      setLoadingTools(true);
      try {
        const views: AgentBoundMCP[] = [];
        for (const server of selected) {
          try {
            const res = await mcpApi.listTools(server.id);
            views.push({
              id: server.id,
              name: server.name,
              status: server.status,
              tools: res.data?.tools ?? [],
            });
          } catch {
            views.push({ id: server.id, name: server.name, status: server.status, tools: [] });
          }
        }
        applyToolOptions(views);
      } finally {
        setLoadingTools(false);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [watchedMcpIds, mcps, loading]);

  // 用各 MCP 已发现工具的并集重建选项, 并剪掉已不可选的已选工具
  const applyToolOptions = (views: AgentBoundMCP[]) => {
    const options: { value: string; label: string }[] = [];
    const seen = new Set<string>();
    for (const view of views) {
      for (const tool of view.tools ?? []) {
        if (seen.has(tool.name)) continue;
        seen.add(tool.name);
        options.push({ value: tool.name, label: `${tool.name} · 来自 ${view.name}` });
      }
    }
    setToolOptions(options);
    const current = form.getFieldValue('tools') as string[] | undefined;
    if (current && current.length > 0) {
      const kept = current.filter((tool) => seen.has(tool));
      const removed = current.filter((tool) => !seen.has(tool));
      if (removed.length > 0) {
        form.setFieldValue('tools', kept);
        message.warning(`已移除不再可用的工具: ${removed.join(', ')}`);
      }
    }
  };

  const onSubmit = async (values: AgentFormValues) => {
    setSubmitting(true);
    try {
      const payload = {
        name: values.name,
        description: values.description,
        model: values.model,
        system_prompt: values.system_prompt,
        temperature: values.temperature,
        max_tokens: values.max_tokens,
        max_tool_rounds: values.max_tool_rounds,
        tools: values.tools ?? [],
        mcp_ids: values.mcp_ids ?? [],
        skills: values.skills ?? [],
        skills_usage_mode: values.skills_usage_mode || 'metadata_injection',
      };
      if (isEdit && id) {
        await agentApi.update(id, payload);
        message.success('更新成功，已产生新版本');
        navigate(`/agents/${id}`);
      } else {
        const res = await agentApi.create(payload);
        message.success('创建成功');
        navigate(`/agents/${res.data?.id ?? ''}`, { replace: true });
      }
    } catch (err) {
      message.error(getErrorMessage(err));
    } finally {
      setSubmitting(false);
    }
  };

  if (isEdit && loading) {
    return (
      <div style={{ textAlign: 'center', padding: 80 }}>
        <Spin size="large" />
      </div>
    );
  }

  if (isEdit && !agent) {
    return <Result status="404" title="Agent 不存在" extra={<Button onClick={() => navigate('/agents')}>返回列表</Button>} />;
  }

  const hasMCP = (watchedMcpIds ?? []).length > 0;

  return (
    <Card
      title={isEdit ? `编辑 Agent: ${agent?.name}` : '新建 Agent'}
      extra={
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate(isEdit && id ? `/agents/${id}` : '/agents')}>
          返回
        </Button>
      }
      style={{ maxWidth: 760, margin: '0 auto' }}
    >
      {isEdit && agent?.status === 'running' && (
        <Alert
          type="warning"
          showIcon
          message="Agent 正在运行中"
          description="运行中禁止修改配置。请先停止实例，再保存更改。"
          style={{ marginBottom: 16 }}
        />
      )}
      <Form
        form={form}
        layout="vertical"
        onFinish={onSubmit}
        initialValues={{ temperature: 0.7, mcp_ids: [], tools: [], skills: [], skills_usage_mode: 'metadata_injection' }}
      >
        <Form.Item
          name="name"
          label="名称"
          rules={[
            { required: true, message: '请输入名称' },
            { min: 2, max: 64, message: '长度 2-64 个字符' },
          ]}
        >
          <Input placeholder="全局唯一，如 demo-support-agent" />
        </Form.Item>
        <Form.Item name="description" label="描述" rules={[{ max: 512, message: '最多 512 字' }]}>
          <Input.TextArea rows={2} placeholder="Agent 的用途说明" />
        </Form.Item>
        <Form.Item
          name="model"
          label="模型"
          tooltip="从模型模板选择 (M4); 运行时优先使用该模板, 不可用时按优先级故障转移"
          rules={[{ required: true, message: '请选择模型' }]}
        >
          <Select
            showSearch
            placeholder="选择模型模板"
            optionFilterProp="label"
            options={modelOptions}
            notFoundContent={models.length === 0 ? '暂无模型模板, 请到模型管理注册' : '无匹配项'}
          />
        </Form.Item>
        <Form.Item
          name="mcp_ids"
          label="绑定 MCP 服务器"
          tooltip="Agent 运行时可调用这些 MCP 的工具; 可用工具由绑定 MCP 的已发现工具决定"
        >
          <Select
            mode="multiple"
            allowClear
            showSearch
            placeholder="选择要绑定的 MCP 服务器"
            optionFilterProp="label"
            options={mcps.map((m) => ({
              value: m.id,
              label: `${m.name} [${MCP_STATUS_MAP[m.status as keyof typeof MCP_STATUS_MAP]?.label ?? m.status}]`,
            }))}
            notFoundContent={mcps.length === 0 ? '暂无 MCP 服务器, 请到 MCP 管理注册' : '无匹配项'}
          />
        </Form.Item>
        <Form.Item
          name="skills"
          label="关联技能"
          tooltip="对话运行时按注入模式注入技能上下文; 技能的 required_tools 须被可用工具覆盖"
        >
          <Select
            mode="multiple"
            allowClear
            showSearch
            placeholder="选择要关联的技能包"
            optionFilterProp="label"
            options={skills.map((s) => ({
              value: s.id,
              label: `${s.name} (v${s.version})${s.status === 'disabled' ? ', 已禁用' : ''}`,
            }))}
            notFoundContent={skills.length === 0 ? '暂无技能, 请到技能管理导入' : '无匹配项'}
          />
        </Form.Item>
        <Form.Item
          name="skills_usage_mode"
          label="技能注入模式"
          tooltip="渐进式披露: 注入技能目录, 通过 load_skill 工具按需加载正文 (默认); 全量注入: 所有技能正文直接注入系统提示词 (总长 ≤128KB)"
        >
          <Select
            options={(Object.keys(SKILL_USAGE_MODE_MAP) as SkillsUsageMode[]).map((key) => ({
              value: key,
              label: `${SKILL_USAGE_MODE_MAP[key].label} — ${SKILL_USAGE_MODE_MAP[key].hint}`,
            }))}
          />
        </Form.Item>
        <Form.Item
          name="tools"
          label="可用工具"
          tooltip="自动校验: 所选工具必须来自绑定 MCP 的已发现工具列表; 留空表示可使用全部绑定工具"
          extra={
            !hasMCP
              ? '先绑定 MCP 服务器, 此处将列出其已发现的工具'
              : toolOptions.length === 0
                ? '绑定 MCP 暂无已发现工具 (连通后自动发现)'
                : undefined
          }
        >
          <Select
            mode="multiple"
            allowClear
            showSearch
            loading={loadingTools}
            placeholder={hasMCP ? '选择允许调用的工具 (留空 = 全部)' : '请先绑定 MCP 服务器'}
            optionFilterProp="label"
            options={toolOptions}
            disabled={!hasMCP}
          />
        </Form.Item>
        <Form.Item name="system_prompt" label="系统提示词">
          <Input.TextArea rows={4} placeholder="You are a helpful assistant." />
        </Form.Item>
        <Form.Item name="temperature" label="Temperature" tooltip="0-2，越高越随机">
          <Slider min={0} max={2} step={0.1} marks={{ 0: '0', 1: '1', 2: '2' }} />
        </Form.Item>
        <Form.Item name="max_tokens" label="最大 Token 数">
          <InputNumber min={1} max={128000} style={{ width: 200 }} placeholder="可选" />
        </Form.Item>
        <Form.Item name="max_tool_rounds" label="工具调用轮数上限" tooltip="单次对话中模型可连续调用工具的最大轮数，留空 = 系统默认 5">
          <InputNumber min={1} max={50} style={{ width: 200 }} placeholder="默认 5" />
        </Form.Item>
        <Divider />
        <Space>
          <Button type="primary" htmlType="submit" icon={<SaveOutlined />} loading={submitting} disabled={isEdit && agent?.status === 'running'}>
            {isEdit ? '保存（产生新版本）' : '创建'}
          </Button>
          <Button onClick={() => navigate(isEdit && id ? `/agents/${id}` : '/agents')}>取消</Button>
        </Space>
      </Form>
    </Card>
  );
}