import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  App,
  Button,
  Card,
  Checkbox,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { permissionApi, roleApi } from '@/api/rbac';
import { getErrorMessage } from '@/api/client';
import { formatDateTime } from '@/utils/format';
import type { PermissionItem, RoleItem } from '@/types';

const BUILTIN_ROLES = new Set(['admin', 'operator', 'user']);

type FormValues = {
  name?: string;
  description?: string;
  permissions?: string[];
};

// 权限按资源分组
function groupByResource(permissions: PermissionItem[]): [string, PermissionItem[]][] {
  const groups = new Map<string, PermissionItem[]>();
  for (const p of permissions) {
    const list = groups.get(p.resource) ?? [];
    list.push(p);
    groups.set(p.resource, list);
  }
  return [...groups.entries()];
}

// 权限选择器: 单一 Checkbox.Group 按资源分组 (受控, 供 Form.Item 注入 value/onChange)
function PermissionPicker({
  value,
  onChange,
  permissions,
}: {
  value?: string[];
  onChange?: (value: string[]) => void;
  permissions: PermissionItem[];
}) {
  const groups = useMemo(() => groupByResource(permissions), [permissions]);
  return (
    <div style={{ maxHeight: 280, overflow: 'auto', paddingRight: 8 }}>
      <Checkbox.Group value={value} onChange={(vals) => onChange?.(vals as string[])}>
        {groups.map(([resource, items]) => (
          <div key={resource} style={{ marginBottom: 12 }}>
            <div style={{ fontWeight: 600, marginBottom: 4, color: 'var(--color-text-secondary)' }}>
              {resource}
            </div>
            <Space wrap>
              {items.map((p) => (
                <Checkbox key={p.code} value={p.code}>
                  {p.name} ({p.code})
                </Checkbox>
              ))}
            </Space>
          </div>
        ))}
      </Checkbox.Group>
    </div>
  );
}

export function RoleListPage() {
  const { message, modal } = App.useApp();
  const [roles, setRoles] = useState<RoleItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [permissions, setPermissions] = useState<PermissionItem[]>([]);

  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<RoleItem | null>(null);
  const [saving, setSaving] = useState(false);
  const [createForm] = Form.useForm<FormValues>();
  const [editForm] = Form.useForm<FormValues>();

  const load = useCallback(async () => {
    try {
      const res = await roleApi.list();
      setRoles(res.data?.items ?? []);
    } catch (err) {
      message.error(getErrorMessage(err, '加载角色列表失败'));
    } finally {
      setLoading(false);
    }
  }, [message]);

  const loadPermissions = useCallback(async () => {
    try {
      const res = await permissionApi.list();
      setPermissions(res.data?.items ?? []);
    } catch {
      // 权限加载失败不阻塞页面
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    load();
    loadPermissions();
  }, [load, loadPermissions]);

  // 权限码 -> 中文名 (展示用)
  const nameByCode = useMemo(() => {
    const map = new Map<string, string>();
    for (const p of permissions) map.set(p.code, p.name);
    return map;
  }, [permissions]);

  const openCreate = () => {
    createForm.resetFields();
    setCreateOpen(true);
  };

  const openEdit = (role: RoleItem) => {
    setEditing(role);
    editForm.setFieldsValue({
      name: role.name,
      description: role.description,
      permissions: role.permissions ?? [],
    });
  };

  const onSubmitCreate = async () => {
    const values = await createForm.validateFields();
    setSaving(true);
    try {
      await roleApi.create({
        name: values.name!,
        description: values.description || undefined,
        permissions: values.permissions ?? [],
      });
      message.success('角色已创建');
      setCreateOpen(false);
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '创建失败'));
    } finally {
      setSaving(false);
    }
  };

  const onSubmitEdit = async () => {
    if (!editing) return;
    const values = await editForm.validateFields();
    setSaving(true);
    try {
      await roleApi.update(editing.id, {
        description: values.description || '',
        permissions: values.permissions ?? [],
      });
      message.success('已保存');
      setEditing(null);
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '保存失败'));
    } finally {
      setSaving(false);
    }
  };

  const onDelete = (role: RoleItem) => {
    modal.confirm({
      title: `删除角色 ${role.name}?`,
      content: `该角色当前分配给 ${role.user_count} 个用户, 有用户绑定时不可删除。`,
      okText: '删除',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await roleApi.remove(role.id);
          message.success('已删除');
          load();
        } catch (err) {
          message.error(getErrorMessage(err, '删除失败'));
          throw err;
        }
      },
    });
  };

  const columns: ColumnsType<RoleItem> = [
    {
      title: '角色',
      dataIndex: 'name',
      width: 160,
      render: (v: string) => (
        <Space size={4}>
          <b>{v}</b>
          {BUILTIN_ROLES.has(v) && <Tag>内置</Tag>}
        </Space>
      ),
    },
    {
      title: '描述',
      dataIndex: 'description',
      ellipsis: true,
      render: (v: string) => v || <span style={{ color: 'var(--color-text-secondary)' }}>—</span>,
    },
    {
      title: '权限',
      dataIndex: 'permissions',
      width: 380,
      render: (codes: string[]) => (
        <Space size={4} wrap>
          {codes && codes.length > 0
            ? codes.map((code) => (
                <Tooltip key={code} title={code}>
                  <Tag style={{ marginRight: 0 }}>{nameByCode.get(code) ?? code}</Tag>
                </Tooltip>
              ))
            : <Tag color="warning">无权限</Tag>}
        </Space>
      ),
    },
    {
      title: '用户数',
      dataIndex: 'user_count',
      width: 80,
      align: 'center',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 170,
      render: (v: string) => formatDateTime(v),
    },
    {
      title: '操作',
      key: 'actions',
      width: 130,
      render: (_: unknown, record) => (
        <Space size="small">
          <Button size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Popconfirm
            title={`确认删除角色 ${record.name}?`}
            onConfirm={() => onDelete(record)}
            disabled={BUILTIN_ROLES.has(record.name)}
          >
            <Button size="small" danger disabled={BUILTIN_ROLES.has(record.name)}>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>角色管理</h2>
        <Space>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建角色
          </Button>
        </Space>
      </div>
      <Card>
        <Table<RoleItem>
          rowKey="id"
          columns={columns}
          dataSource={roles}
          loading={loading}
          pagination={false}
        />

        {/* 新建角色 */}
        <Modal
          title="新建角色"
          open={createOpen}
          onOk={onSubmitCreate}
          confirmLoading={saving}
          onCancel={() => setCreateOpen(false)}
          width={560}
          destroyOnHidden
        >
          <Form form={createForm} layout="vertical" requiredMark={false}>
            <Form.Item
              name="name"
              label="角色名"
              rules={[
                { required: true, message: '请输入角色名' },
                { pattern: /^[a-zA-Z][a-zA-Z0-9_]{1,63}$/, message: '字母开头, 仅含字母/数字/下划线, 2-64 位' },
              ]}
            >
              <Input placeholder="如 support" />
            </Form.Item>
            <Form.Item name="description" label="描述">
              <Input.TextArea rows={2} placeholder="选填" />
            </Form.Item>
            <Form.Item name="permissions" label="权限 (按资源分组)">
              <PermissionPicker permissions={permissions} />
            </Form.Item>
          </Form>
        </Modal>

        {/* 编辑角色 */}
        <Modal
          title={`编辑角色: ${editing?.name ?? ''}`}
          open={!!editing}
          onOk={onSubmitEdit}
          confirmLoading={saving}
          onCancel={() => setEditing(null)}
          width={560}
          destroyOnHidden
        >
          <Form form={editForm} layout="vertical" requiredMark={false}>
            <Form.Item name="name" label="角色名">
              <Input disabled />
            </Form.Item>
            <Form.Item name="description" label="描述">
              <Input.TextArea rows={2} />
            </Form.Item>
            <Form.Item
              name="permissions"
              label="权限 (保存后全量替换)"
              extra={editing?.name === 'admin' ? 'admin 角色将强制保留 用户管理 / 角色管理 / MCP 审批 权限' : undefined}
            >
              <PermissionPicker permissions={permissions} />
            </Form.Item>
          </Form>
        </Modal>
      </Card>
    </div>
  );
}

