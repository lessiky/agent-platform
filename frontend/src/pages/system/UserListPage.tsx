import { useCallback, useEffect, useState } from 'react';
import {
  App,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd';
import { PlusOutlined, SearchOutlined, UserSwitchOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { roleApi, userApi } from '@/api/rbac';
import { getErrorMessage } from '@/api/client';
import { formatDateTime } from '@/utils/format';
import type { RoleItem, UserAdmin } from '@/types';

type FormValues = {
  username?: string;
  email?: string;
  password?: string;
  roles?: string[];
  status?: boolean;
};

export function UserListPage() {
  const { message, modal } = App.useApp();
  const [users, setUsers] = useState<UserAdmin[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [keyword, setKeyword] = useState('');
  const [debouncedKeyword, setDebouncedKeyword] = useState('');
  const [statusFilter, setStatusFilter] = useState<number | undefined>(undefined);

  // 角色下拉选项
  const [roleOptions, setRoleOptions] = useState<RoleItem[]>([]);

  // 新建/编辑弹窗
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<UserAdmin | null>(null);
  const [saving, setSaving] = useState(false);
  const [createForm] = Form.useForm<FormValues>();
  const [editForm] = Form.useForm<FormValues>();

  // 分配角色弹窗
  const [roleTarget, setRoleTarget] = useState<UserAdmin | null>(null);
  const [roleForm] = Form.useForm<{ roles: string[] }>();

  const load = useCallback(async () => {
    try {
      const res = await userApi.list({
        q: debouncedKeyword || undefined,
        status: statusFilter,
        page,
        size,
      });
      setUsers(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载用户列表失败'));
    } finally {
      setLoading(false);
    }
  }, [debouncedKeyword, statusFilter, page, size, message]);

  const loadRoles = useCallback(async () => {
    try {
      const res = await roleApi.list();
      setRoleOptions(res.data?.items ?? []);
    } catch {
      // 角色加载失败不阻塞页面 (下拉为空即可)
    }
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedKeyword(keyword.trim()), 400);
    return () => clearTimeout(timer);
  }, [keyword]);

  useEffect(() => {
    setLoading(true);
    load();
  }, [load]);

  useEffect(() => {
    loadRoles();
  }, [loadRoles]);

  const openCreate = () => {
    createForm.resetFields();
    setCreateOpen(true);
  };

  const openEdit = (user: UserAdmin) => {
    setEditing(user);
    editForm.setFieldsValue({
      email: user.email ?? '',
      status: user.status === 1,
      password: '',
    });
  };

  const onSubmitCreate = async () => {
    const values = await createForm.validateFields();
    setSaving(true);
    try {
      await userApi.create({
        username: values.username!,
        email: values.email || undefined,
        password: values.password!,
        roles: values.roles && values.roles.length > 0 ? values.roles : undefined,
      });
      message.success('用户已创建');
      setCreateOpen(false);
      setPage(1);
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
      await userApi.update(editing.id, {
        email: values.email ?? '',
        status: values.status ? 1 : 0,
        password: values.password || undefined,
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

  const openAssignRoles = (user: UserAdmin) => {
    setRoleTarget(user);
    roleForm.setFieldsValue({ roles: user.roles ?? [] });
  };

  const onSubmitRoles = async () => {
    if (!roleTarget) return;
    const values = await roleForm.validateFields();
    setSaving(true);
    try {
      await userApi.assignRoles(roleTarget.id, values.roles ?? []);
      message.success('角色已更新');
      setRoleTarget(null);
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '分配角色失败'));
    } finally {
      setSaving(false);
    }
  };

  const onToggleStatus = async (user: UserAdmin, active: boolean) => {
    try {
      await userApi.update(user.id, { status: active ? 1 : 0 });
      message.success(active ? '已启用' : '已停用');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '操作失败'));
    }
  };

  const onDelete = (user: UserAdmin) => {
    modal.confirm({
      title: `删除用户 ${user.username}?`,
      content: '删除后该用户将无法登录, 且角色分配将被移除。',
      okText: '删除',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await userApi.remove(user.id);
          message.success('已删除');
          load();
        } catch (err) {
          message.error(getErrorMessage(err, '删除失败'));
          throw err;
        }
      },
    });
  };

  const roleSelect = (
    <Select
      mode="multiple"
      allowClear
      placeholder="不选则分配默认 user 角色"
      options={roleOptions.map((r) => ({ value: r.name, label: `${r.name} (${r.description || '无描述'})` }))}
    />
  );

  const columns: ColumnsType<UserAdmin> = [
    {
      title: '用户名',
      dataIndex: 'username',
      width: 160,
      render: (v: string) => <b>{v}</b>,
    },
    {
      title: '邮箱',
      dataIndex: 'email',
      width: 200,
      render: (v: string | null) => v || <span style={{ color: 'var(--color-text-secondary)' }}>—</span>,
    },
    {
      title: '角色',
      dataIndex: 'roles',
      width: 220,
      render: (roles: string[]) =>
        roles && roles.length > 0 ? (
          <Space size={4} wrap>
            {roles.map((r) => (
              <Tag key={r} color={r === 'admin' ? 'red' : r === 'operator' ? 'blue' : 'default'}>
                {r}
              </Tag>
            ))}
          </Space>
        ) : (
          <Tag color="warning">无角色</Tag>
        ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: number) => (
        <Tag color={v === 1 ? 'green' : 'default'}>{v === 1 ? '启用' : '停用'}</Tag>
      ),
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
      width: 210,
      render: (_: unknown, record) => (
        <Space size="small">
          <Button size="small" icon={<UserSwitchOutlined />} onClick={() => openAssignRoles(record)}>
            角色
          </Button>
          <Button size="small" onClick={() => openEdit(record)}>
            编辑
          </Button>
          <Tooltip title={record.status === 1 ? '停用后该用户将无法通过 API 访问' : undefined}>
            <Popconfirm
              title={record.status === 1 ? '确认停用该用户?' : '确认启用该用户?'}
              onConfirm={() => onToggleStatus(record, record.status !== 1)}
            >
              <Button size="small" danger={record.status === 1}>
                {record.status === 1 ? '停用' : '启用'}
              </Button>
            </Popconfirm>
          </Tooltip>
          <Popconfirm title="确认删除该用户?" onConfirm={() => onDelete(record)}>
            <Button size="small" danger>
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
        <h2 style={{ margin: 0 }}>用户管理</h2>
        <Space>
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder="搜索用户名 / 邮箱"
            style={{ width: 220 }}
            value={keyword}
            onChange={(e) => {
              setKeyword(e.target.value);
              setPage(1);
            }}
          />
          <Select
            allowClear
            placeholder="状态"
            style={{ width: 110 }}
            value={statusFilter}
            onChange={(v) => {
              setStatusFilter(v);
              setPage(1);
            }}
            options={[
              { value: 1, label: '启用' },
              { value: 0, label: '停用' },
            ]}
          />
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建用户
          </Button>
        </Space>
      </div>
      <Card>
        <Table<UserAdmin>
          rowKey="id"
          columns={columns}
          dataSource={users}
          loading={loading}
          pagination={{
            current: page,
            pageSize: size,
            total,
            showSizeChanger: true,
            showTotal: (t) => `共 ${t} 个用户`,
            onChange: (p, s) => {
              setPage(p);
              setSize(s);
            },
          }}
        />

        {/* 新建用户 */}
        <Modal
          title="新建用户"
          open={createOpen}
          onOk={onSubmitCreate}
          confirmLoading={saving}
          onCancel={() => setCreateOpen(false)}
          destroyOnHidden
        >
          <Form form={createForm} layout="vertical" requiredMark={false}>
            <Form.Item
              name="username"
              label="用户名"
              rules={[
                { required: true, message: '请输入用户名' },
                { min: 2, max: 64, message: '用户名长度 2-64' },
              ]}
            >
              <Input placeholder="如 zhangsan" />
            </Form.Item>
            <Form.Item
              name="email"
              label="邮箱"
              rules={[{ type: 'email', message: '邮箱格式不正确' }]}
            >
              <Input placeholder="选填" />
            </Form.Item>
            <Form.Item
              name="password"
              label="密码"
              rules={[{ required: true, min: 6, message: '密码至少 6 位' }]}
            >
              <Input.Password placeholder="至少 6 位" />
            </Form.Item>
            <Form.Item name="roles" label="角色">
              {roleSelect}
            </Form.Item>
          </Form>
        </Modal>

        {/* 编辑用户 */}
        <Modal
          title={`编辑用户: ${editing?.username ?? ''}`}
          open={!!editing}
          onOk={onSubmitEdit}
          confirmLoading={saving}
          onCancel={() => setEditing(null)}
          destroyOnHidden
        >
          <Form form={editForm} layout="vertical" requiredMark={false}>
            <Form.Item
              name="email"
              label="邮箱"
              rules={[{ type: 'email', message: '邮箱格式不正确' }]}
            >
              <Input placeholder="留空表示不修改" />
            </Form.Item>
            <Form.Item name="password" label="重置密码">
              <Input.Password placeholder="留空表示不修改, 至少 6 位" />
            </Form.Item>
            <Form.Item name="status" label="启用状态" valuePropName="checked">
              <Switch checkedChildren="启用" unCheckedChildren="停用" />
            </Form.Item>
          </Form>
        </Modal>

        {/* 分配角色 */}
        <Modal
          title={`分配角色: ${roleTarget?.username ?? ''}`}
          open={!!roleTarget}
          onOk={onSubmitRoles}
          confirmLoading={saving}
          onCancel={() => setRoleTarget(null)}
          destroyOnHidden
        >
          <Form form={roleForm} layout="vertical" requiredMark={false}>
            <Form.Item name="roles" label="角色 (多选, 保存后全量替换)">
              <Select
                mode="multiple"
                allowClear
                options={roleOptions.map((r) => ({ value: r.name, label: `${r.name} (${r.description || '无描述'})` }))}
              />
            </Form.Item>
          </Form>
        </Modal>
      </Card>
    </div>
  );
}

