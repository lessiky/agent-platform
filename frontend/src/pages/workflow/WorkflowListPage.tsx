import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { App, Button, Card, Modal, Form, Input, Space, Table, Tag } from 'antd';
import { PlusOutlined, ReloadOutlined, RobotOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { workflowApi, type AIGenerateResult, type Workflow } from '@/api/workflow';
import { AIGenerateWorkflowModal } from './AIGenerateWorkflowModal';
import { getErrorMessage } from '@/api/client';
import { formatDateTime, timeAgo } from '@/utils/format';

const STATUS_MAP: Record<string, { label: string; color: string }> = {
  draft: { label: '草稿', color: 'default' },
  active: { label: '已激活', color: 'green' },
  archived: { label: '已归档', color: 'orange' },
};

export function WorkflowListPage() {
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [items, setItems] = useState<Workflow[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [aiGenOpen, setAiGenOpen] = useState(false);
  const [aiCreating, setAiCreating] = useState(false);
  const [form] = Form.useForm();

  const load = useCallback(async () => {
    try {
      const res = await workflowApi.list({ page, size });
      setItems(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载工作流列表失败'));
    } finally {
      setLoading(false);
    }
  }, [page, size, message]);

  useEffect(() => {
    load();
  }, [load]);

  // AI 生成确认 -> 以草稿创建工作流并进入编辑器
  const onAIGenerated = async (result: AIGenerateResult, nameOverride?: string) => {
    const name = (nameOverride ?? result.name).trim();
    if (!name) {
      message.error('工作流名称不能为空');
      return;
    }
    setAiCreating(true);
    try {
      const res = await workflowApi.create({
        name,
        description: result.description,
        definition: result.definition,
        input_schema: result.input_schema,
      });
      const wfId = res.data?.id;
      if (!wfId) {
        message.error('工作流创建失败');
        return;
      }
      setAiGenOpen(false);
      message.success(`AI 已创建工作流「${name}」`);
      navigate(`/workflows/${wfId}/edit`);
    } catch (err) {
      // 保留弹窗打开, 便于修改名称后再次确认 (如名称冲突)
      message.error(getErrorMessage(err, 'AI 工作流创建失败'));
    } finally {
      setAiCreating(false);
    }
  };

  const onCreate = async () => {
    try {
      const values = await form.validateFields();
      setCreating(true);
      const res = await workflowApi.create({
        name: values.name,
        description: values.description || '',
        definition: {
          version: 1,
          nodes: [{ id: 'n1', type: 'delay', name: '等待', config: { seconds: 1 } }],
          edges: [],
        },
      });
      setCreateOpen(false);
      form.resetFields();
      const wfId = res.data?.id;
      if (wfId) navigate(`/workflows/${wfId}/edit`);
    } catch (err) {
      if (err && typeof err === 'object' && 'errorFields' in err) return;
      message.error(getErrorMessage(err, '创建工作流失败'));
    } finally {
      setCreating(false);
    }
  };

  const columns: ColumnsType<Workflow> = [
    {
      title: '名称',
      dataIndex: 'name',
      width: 220,
      render: (name: string, record) => (
        <div>
          <Link to={`/workflows/${record.id}`}>{name}</Link>
          <div style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>
            v{record.version} · {record.definition?.nodes?.length ?? 0} 节点
          </div>
        </div>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (s: string) => <Tag color={STATUS_MAP[s]?.color}>{STATUS_MAP[s]?.label ?? s}</Tag>,
    },
    {
      title: '定时调度',
      width: 160,
      render: (_, record) =>
        record.schedule_enabled && record.schedule ? (
          <Tag color="blue">{record.schedule.cron}</Tag>
        ) : (
          <span style={{ color: 'var(--color-text-secondary)' }}>—</span>
        ),
    },
    { title: '描述', dataIndex: 'description', ellipsis: true },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 170,
      render: (v: string) => <span title={formatDateTime(v)}>{timeAgo(v)}</span>,
    },
    {
      title: '操作',
      width: 160,
      render: (_, record) => (
        <Space size="small">
          <Button type="link" size="small" onClick={() => navigate(`/workflows/${record.id}/edit`)}>
            编排
          </Button>
          <Button type="link" size="small" onClick={() => navigate(`/workflows/${record.id}`)}>
            详情
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>工作流管理</h2>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load}>
            刷新
          </Button>
          <Button icon={<RobotOutlined />} onClick={() => setAiGenOpen(true)}>
            AI 生成
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
            新建工作流
          </Button>
        </Space>
      </div>
      <Card>
        <Table
          rowKey="id"
          loading={loading}
          columns={columns}
          dataSource={items}
          pagination={{
            current: page,
            pageSize: size,
            total,
            showSizeChanger: true,
            onChange: (p, s) => {
              setPage(p);
              setSize(s);
            },
          }}
        />
        <Modal
          title="新建工作流"
          open={createOpen}
          onOk={onCreate}
          confirmLoading={creating}
          onCancel={() => setCreateOpen(false)}
        >
          <Form form={form} layout="vertical">
            <Form.Item name="name" label="名称" rules={[{ required: true, max: 64 }]}>
              <Input placeholder="例如: 客服工单处理" />
            </Form.Item>
            <Form.Item name="description" label="描述">
              <Input.TextArea rows={2} placeholder="可选" />
            </Form.Item>
          </Form>
        </Modal>
        <AIGenerateWorkflowModal
          open={aiGenOpen}
          showName
          confirmLoading={aiCreating}
          onClose={() => setAiGenOpen(false)}
          onGenerated={onAIGenerated}
        />
      </Card>
    </div>
  );
}