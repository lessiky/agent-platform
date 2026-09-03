import { useCallback, useEffect, useState } from 'react';
import {
  App,
  Button,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import { useAuthStore } from '@/store/auth-store';
import type { Memory, MemoryKind } from '@/types';
import { formatDateTime } from '@/utils/format';
import type { ColumnsType } from 'antd/es/table';

// 记忆类型标签 (与后端 memoryKindLabels 对齐)
const KIND_MAP: Record<MemoryKind, { label: string; color: string }> = {
  preference: { label: '偏好', color: 'purple' },
  fact: { label: '事实', color: 'blue' },
  decision: { label: '决定', color: 'gold' },
  event: { label: '事件', color: 'cyan' },
};

const PAGE_SIZE = 20;

interface MemoryFormState {
  content: string;
  kind: MemoryKind;
  scope: 'user' | 'agent';
  status: 'active' | 'archived';
}

const EMPTY_FORM: MemoryFormState = { content: '', kind: 'fact', scope: 'user', status: 'active' };

// Agent 详情页 "记忆" 页签 (M10.2): 列表 / 筛选 / 增删改 / 启用停用 / 来源展示
export function MemoryPanel({ agentId }: { agentId: string }) {
  const { message } = App.useApp();
  const roles = useAuthStore((s) => s.roles);
  const isAdmin = roles.includes('admin');

  const [items, setItems] = useState<Memory[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [kind, setKind] = useState<string | undefined>();
  const [status, setStatus] = useState<string | undefined>();
  const [scope, setScope] = useState<string>('mine');

  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Memory | null>(null);
  const [form, setForm] = useState<MemoryFormState>(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const [actingId, setActingId] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await agentApi.listMemories(agentId, { kind, status, scope, page, size: PAGE_SIZE });
      setItems(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载记忆失败'));
    } finally {
      setLoading(false);
    }
  }, [agentId, kind, status, scope, page, message]);

  useEffect(() => {
    load();
  }, [load]);

  const resetPagedFilters = (next: Partial<{ kind?: string; status?: string; scope?: string }>) => {
    setPage(1);
    if (next.kind !== undefined) setKind(next.kind);
    if (next.status !== undefined) setStatus(next.status);
    if (next.scope !== undefined) setScope(next.scope);
  };

  const openCreate = () => {
    setEditing(null);
    setForm(EMPTY_FORM);
    setModalOpen(true);
  };

  const openEdit = (m: Memory) => {
    setEditing(m);
    setForm({
      content: m.content,
      kind: m.kind,
      scope: m.user_id ? 'user' : 'agent',
      status: m.status,
    });
    setModalOpen(true);
  };

  const save = async () => {
    const contentText = form.content.trim();
    if (!contentText) {
      message.warning('记忆内容不能为空');
      return;
    }
    setSaving(true);
    try {
      if (editing) {
        await agentApi.updateMemory(agentId, editing.id, {
          content: contentText,
          kind: form.kind,
          status: form.status,
        });
        message.success('记忆已更新');
      } else {
        await agentApi.createMemory(agentId, {
          content: contentText,
          kind: form.kind,
          scope: form.scope,
        });
        message.success('记忆已添加');
      }
      setModalOpen(false);
      await load();
    } catch (err) {
      message.error(getErrorMessage(err, '保存失败'));
    } finally {
      setSaving(false);
    }
  };

  const toggleStatus = async (m: Memory) => {
    setActingId(m.id);
    try {
      const next = m.status === 'active' ? 'archived' : 'active';
      await agentApi.updateMemory(agentId, m.id, { status: next });
      message.success(next === 'active' ? '已启用' : '已停用');
      await load();
    } catch (err) {
      message.error(getErrorMessage(err));
    } finally {
      setActingId(null);
    }
  };

  const remove = async (m: Memory) => {
    setActingId(m.id);
    try {
      await agentApi.deleteMemory(agentId, m.id);
      message.success('记忆已删除');
      await load();
    } catch (err) {
      message.error(getErrorMessage(err));
    } finally {
      setActingId(null);
    }
  };

  const columns: ColumnsType<Memory> = [
    {
      title: '内容',
      dataIndex: 'content',
      render: (v: string) => (
        <Tooltip title={v}>
          <span>{v}</span>
        </Tooltip>
      ),
    },
    {
      title: '类型',
      dataIndex: 'kind',
      width: 80,
      render: (k: MemoryKind) => (
        <Tag color={KIND_MAP[k]?.color}>{KIND_MAP[k]?.label ?? k}</Tag>
      ),
    },
    {
      title: '来源',
      dataIndex: 'source',
      width: 80,
      render: (s: Memory['source']) =>
        s === 'user_explicit' ? (
          <Tag color="green">手动</Tag>
        ) : (
          <Tag color="blue">自动</Tag>
        ),
    },
    {
      title: '级别',
      dataIndex: 'user_id',
      width: 90,
      render: (uid: string | null) =>
        uid ? (
          <Tooltip title={uid}>
            <Tag>用户级</Tag>
          </Tooltip>
        ) : (
          <Tag>Agent 级</Tag>
        ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 80,
      render: (s: Memory['status']) =>
        s === 'active' ? (
          <Tag color="green">启用</Tag>
        ) : (
          <Tag color="default">停用</Tag>
        ),
    },
    {
      title: '引用',
      dataIndex: 'access_count',
      width: 70,
      align: 'center',
      render: (v: number) => v ?? 0,
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 160,
      render: (v: string) => (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {formatDateTime(v)}
        </Typography.Text>
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 190,
      render: (_, m) => (
        <Space size={4}>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(m)}>
            编辑
          </Button>
          <Button
            size="small"
            loading={actingId === m.id}
            onClick={() => toggleStatus(m)}
          >
            {m.status === 'active' ? '停用' : '启用'}
          </Button>
          <Popconfirm title="删除该记忆?" description="删除后新会话不再引用" onConfirm={() => remove(m)} okText="删除" cancelText="取消">
            <Button size="small" danger icon={<DeleteOutlined />} loading={actingId === m.id} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 12,
          gap: 12,
          flexWrap: 'wrap',
        }}
      >
        <Space wrap>
          <Select
            allowClear
            placeholder="类型"
            style={{ width: 110 }}
            value={kind}
            onChange={(v) => resetPagedFilters({ kind: v })}
            options={Object.entries(KIND_MAP).map(([value, o]) => ({ value, label: o.label }))}
          />
          <Select
            allowClear
            placeholder="状态"
            style={{ width: 110 }}
            value={status}
            onChange={(v) => resetPagedFilters({ status: v })}
            options={[
              { value: 'active', label: '启用' },
              { value: 'archived', label: '停用' },
            ]}
          />
          <Select
            value={scope}
            style={{ width: 150 }}
            onChange={(v) => resetPagedFilters({ scope: v })}
            options={[
              { value: 'mine', label: '我的 + Agent 级' },
              { value: 'agent', label: '仅 Agent 级' },
              ...(isAdmin ? [{ value: 'all', label: '全部 (含其他用户) ' }] : []),
            ]}
          />
        </Space>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load} />
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            添加记忆
          </Button>
        </Space>
      </div>

      <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 0 }}>
        记忆会在对话检索命中时注入模型上下文: 用户级记忆仅属主可见, Agent 级记忆对本 Agent
        所有用户生效。自动抽取条目由对话结束后异步生成 (来源 = 自动)。
      </Typography.Paragraph>

      <Table
        rowKey="id"
        size="middle"
        loading={loading}
        columns={columns}
        dataSource={items}
        pagination={{
          current: page,
          pageSize: PAGE_SIZE,
          total,
          showSizeChanger: false,
          showTotal: (t) => `共 ${t} 条`,
          onChange: (p) => setPage(p),
        }}
        locale={{ emptyText: '暂无记忆 (可在对话中积累, 或点击 "添加记忆" 显式添加)' }}
      />

      <Modal
        title={editing ? '编辑记忆' : '添加记忆'}
        open={modalOpen}
        onOk={save}
        onCancel={() => setModalOpen(false)}
        confirmLoading={saving}
        okText="保存"
        cancelText="取消"
        destroyOnClose
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, paddingTop: 8 }}>
          <div>
            <div style={{ marginBottom: 4 }}>
              <Typography.Text>
                内容 <Typography.Text type="danger">*</Typography.Text>
              </Typography.Text>
            </div>
            <Input.TextArea
              rows={3}
              maxLength={500}
              showCount
              placeholder="一句话记忆, 例如: 用户偏好简洁直接的回答"
              value={form.content}
              onChange={(e) => setForm({ ...form, content: e.target.value })}
            />
          </div>
          <Space size={24} wrap>
            <div>
              <div style={{ marginBottom: 4 }}>
                <Typography.Text>类型</Typography.Text>
              </div>
              <Select
                value={form.kind}
                style={{ width: 140 }}
                onChange={(v) => setForm({ ...form, kind: v })}
                options={Object.entries(KIND_MAP).map(([value, o]) => ({ value, label: o.label }))}
              />
            </div>
            {editing ? (
              <div>
                <div style={{ marginBottom: 4 }}>
                  <Typography.Text>状态</Typography.Text>
                </div>
                <Select
                  value={form.status}
                  style={{ width: 140 }}
                  onChange={(v) => setForm({ ...form, status: v })}
                  options={[
                    { value: 'active', label: '启用' },
                    { value: 'archived', label: '停用' },
                  ]}
                />
              </div>
            ) : (
              <div>
                <div style={{ marginBottom: 4 }}>
                  <Typography.Text>级别</Typography.Text>
                </div>
                <Select
                  value={form.scope}
                  style={{ width: 220 }}
                  onChange={(v) => setForm({ ...form, scope: v })}
                  options={[
                    { value: 'user', label: '用户级 (仅当前账号)' },
                    { value: 'agent', label: 'Agent 级 (所有用户)' },
                  ]}
                />
              </div>
            )}
          </Space>
        </div>
      </Modal>
    </div>
  );
}
