import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, Form, Input, Modal, Space, Typography } from 'antd';
import { RobotOutlined } from '@ant-design/icons';
import { workflowApi, type AIGenerateResult } from '@/api/workflow';
import { getErrorMessage } from '@/api/client';

const NODE_TYPE_LABELS: Record<string, string> = {
  agent: 'Agent',
  mcp_tool: 'MCP 工具',
  http: 'HTTP',
  delay: '延迟',
  condition: '条件',
};

interface AIGenerateWorkflowModalProps {
  open: boolean;
  onClose: () => void;
  /** 用户在预览中确认后回调 (调用方决定后续动作: 新建工作流 / 填充编辑器画布) */
  onGenerated: (result: AIGenerateResult, nameOverride?: string) => void;
  /** 允许用户指定工作流名称 (列表页新建场景); 编辑器场景留空, 采用 AI 建议名称 */
  showName?: boolean;
  /** 生成前的附加提示 (如编辑器: 将替换当前画布) */
  notice?: string;
  /** 确认按钮 loading (父组件正在执行创建等后续操作) */
  confirmLoading?: boolean;
}

// AI 生成工作流弹窗 (M5 Phase 2)
// 流程: 自然语言描述 -> 调用后端生成 -> 预览 (名称/描述/节点列表) -> 确认使用 / 重新生成
export function AIGenerateWorkflowModal({
  open,
  onClose,
  onGenerated,
  showName,
  notice,
  confirmLoading,
}: AIGenerateWorkflowModalProps) {
  const [form] = Form.useForm();
  const [phase, setPhase] = useState<'form' | 'loading' | 'result'>('form');
  const [result, setResult] = useState<AIGenerateResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setPhase('form');
      setResult(null);
      setError(null);
      form.resetFields();
    }
  }, [open, form]);

  const onGenerate = async () => {
    let values: { description: string };
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setPhase('loading');
    setError(null);
    try {
      const res = await workflowApi.aiGenerate({ description: values.description.trim() });
      const data = res.data;
      if (!data?.definition?.nodes?.length) {
        throw new Error('AI 未返回有效的工作流定义');
      }
      setResult(data);
      setPhase('result');
    } catch (err) {
      setError(getErrorMessage(err, 'AI 生成失败'));
      setPhase('form');
    }
  };

  const confirmedName = useMemo(() => {
    const override = (form.getFieldValue('name') as string | undefined)?.trim();
    return override || result?.name || '';
  }, [form, result]);

  const nodeSummary = useMemo(() => {
    if (!result) return [];
    return result.definition.nodes.map((n) => ({
      key: n.id,
      label: `${n.name || n.id} (${NODE_TYPE_LABELS[n.type] ?? n.type})`,
    }));
  }, [result]);

  return (
    <Modal
      title={
        <span>
          <RobotOutlined /> AI 生成工作流
        </span>
      }
      open={open}
      onCancel={onClose}
      width={640}
      forceRender
      footer={
        phase === 'result' && result ? (
          <Space>
            <Button onClick={() => setPhase('form')}>重新生成</Button>
            <Button
              type="primary"
              loading={confirmLoading}
              onClick={() => onGenerated(result, showName ? (form.getFieldValue('name') as string | undefined) : undefined)}
            >
              确认使用
            </Button>
          </Space>
        ) : (
          <Space>
            <Button onClick={onClose}>取消</Button>
            <Button type="primary" icon={<RobotOutlined />} loading={phase === 'loading'} onClick={onGenerate}>
              {phase === 'loading' ? 'AI 生成中…' : '开始生成'}
            </Button>
          </Space>
        )
      }
    >
      <div style={phase === 'result' ? { display: 'none' } : undefined}>
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="用自然语言描述业务流程, AI 将自动编排为工作流 (DAG), 生成结果先经过平台校验。"
          description={notice || undefined}
        />
        {error && (
          <Alert type="error" showIcon style={{ marginBottom: 12 }} message={error} />
        )}
        <Form form={form} layout="vertical">
          <Form.Item
            name="description"
            label="流程描述"
            rules={[{ required: true, message: '请输入流程描述' }, { max: 2000, message: '最多 2000 字' }]}
          >
            <Input.TextArea
              rows={5}
              placeholder="例如: 收到客户工单后, 先用客服 Agent 分析工单内容, 如果严重级别为高则创建运维工单, 否则直接回复客户"
            />
          </Form.Item>
          {showName && (
            <Form.Item name="name" label="工作流名称 (可选)">
              <Input placeholder="留空则由 AI 建议名称" maxLength={64} />
            </Form.Item>
          )}
        </Form>
        {phase === 'loading' && (
          <Typography.Text type="secondary">模型调用通常需要 10-60 秒, 请耐心等待…</Typography.Text>
        )}
      </div>
      {phase === 'result' &&
        result && (
          <>
            <Alert
              type="success"
              showIcon
              style={{ marginBottom: 12 }}
              message={`已生成工作流「${confirmedName}」(共 ${result.definition.nodes.length} 个节点)`}
              description={
                <div>
                  <div>{result.description}</div>
                  {result.attempts === 2 && (
                    <div style={{ marginTop: 4 }}>首次生成未通过校验, 已自动修正后通过。</div>
                  )}
                  {result.model && (
                    <div style={{ marginTop: 4, fontSize: 12 }}>
                      模型: {result.model}
                      {result.model_name ? ` (${result.model_name})` : ''}
                      {typeof result.total_tokens === 'number' ? ` · 消耗 ${result.total_tokens} tokens` : ''}
                    </div>
                  )}
                </div>
              }
            />
            <div style={{ maxHeight: 200, overflow: 'auto' }}>
              {nodeSummary.map((item, index) => (
                <div key={item.key} style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 6 }}>
                  <span style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>{index + 1}.</span>
                  <span>{item.label}</span>
                </div>
              ))}
            </div>
          </>
        )}
    </Modal>
  );
}
