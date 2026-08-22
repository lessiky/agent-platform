# 前端组件指南

> **版本：** v1.0
> **日期：** 2026-08-17
> **技术栈：** React 18 + TypeScript + Ant Design + Vite

---

## 1. 项目结构

`
frontend/
├── public/
├── src/
│   ├── api/                  # API 请求封装
│   │   ├── client.ts         # Axios 实例 + 拦截器
│   │   ├── agent.ts          # Agent API
│   │   ├── mcp.ts            # MCP API
│   │   ├── workflow.ts       # 工作流 API
│   │   └── model.ts          # 模型管理 API
│   ├── assets/               # 静态资源
│   ├── components/           # 通用组件
│   │   ├── ui/               # 基础 UI 组件
│   │   ├── layout/           # 布局组件
│   │   └── common/           # 通用业务组件
│   ├── layouts/              # 页面布局
│   │   ├── MainLayout.tsx
│   │   └── AuthLayout.tsx
│   ├── pages/                # 页面组件 (按模块分)
│   │   ├── auth/
│   │   ├── dashboard/
│   │   ├── agent/
│   │   ├── mcp/
│   │   ├── workflow/
│   │   └── model/            # 模型管理页面
│   ├── router/               # 路由配置
│   │   ├── index.ts
│   │   └── guards.ts
│   ├── store/                # 状态管理 (Zustand)
│   │   ├── index.ts
│   │   ├── auth-store.ts
│   │   └── settings-store.ts
│   ├── styles/               # 全局样式
│   │   ├── global.css
│   │   └── variables.css
│   ├── types/                # TypeScript 类型
│   │   ├── api.d.ts
│   │   └── index.d.ts
│   ├── utils/                # 工具函数
│   │   ├── format.ts
│   │   └── constants.ts
│   ├── App.tsx
│   └── main.tsx
├── index.html
├── vite.config.ts
├── tsconfig.json
└── package.json
`

---

## 2. API 请求封装

### 2.1 Axios 实例

`	ypescript
// src/api/client.ts
import axios, { type AxiosInstance, type AxiosRequestConfig } from 'axios';

const apiClient: AxiosInstance = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '/api',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
});

// 请求拦截器
apiClient.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('access_token');
    if (token) {
      config.headers.Authorization = 'Bearer ' + token;
    }
    return config;
  },
  (error) => Promise.reject(error)
);

// 响应拦截器
apiClient.interceptors.response.use(
  (response) => response.data,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('access_token');
      window.location.href = '/login';
    }
    return Promise.reject(error);
  }
);

export default apiClient;
`

### 2.2 API 模块

`	ypescript
// src/api/agent.ts
import apiClient from './client';
import type { Agent, PaginatedResponse } from '@/types';

export const agentApi = {
  list: (params: { page?: number; size?: number; keyword?: string }) =>
    apiClient.get<PaginatedResponse<Agent[]>>('/agents', { params }),

  getById: (id: string) =>
    apiClient.get<Agent>('/agents/' + id),

  create: (data: CreateAgentRequest) =>
    apiClient.post<Agent>('/agents', data),

  update: (id: string, data: UpdateAgentRequest) =>
    apiClient.put<Agent>('/agents/' + id, data),

  delete: (id: string) =>
    apiClient.delete('/agents/' + id),

  start: (id: string) =>
    apiClient.post('/agents/' + id + '/start'),

  stop: (id: string) =>
    apiClient.post('/agents/' + id + '/stop'),

  getMetrics: (id: string) =>
    apiClient.get('/agents/' + id + '/metrics'),

  getLogs: (id: string, params: { keyword?: string; limit?: number }) =>
    apiClient.get('/agents/' + id + '/logs', { params }),
};

// src/api/model.ts
import apiClient from './client';
import type { Model, ModelTemplate } from '@/types';

export const modelApi = {
  // 模型模板管理
  listTemplates: (params: { page?: number; size?: number; category?: string }) =>
    apiClient.get<PaginatedResponse<ModelTemplate[]>>('/model-templates', { params }),

  getTemplate: (id: string) =>
    apiClient.get<ModelTemplate>('/model-templates/' + id),

  createTemplate: (data: CreateModelTemplateRequest) =>
    apiClient.post<ModelTemplate>('/model-templates', data),

  updateTemplate: (id: string, data: UpdateModelTemplateRequest) =>
    apiClient.put<ModelTemplate>('/model-templates/' + id, data),

  deleteTemplate: (id: string) =>
    apiClient.delete('/model-templates/' + id),

  // 模型配额管理
  getQuota: (teamId: string) =>
    apiClient.get(/model-quota?team_id=),

  updateQuota: (teamId: string, data: { daily_limit: number; monthly_limit: number }) =>
    apiClient.put(/model-quota/, data),

  // 模型统计
  getUsage: (params: { team_id?: string; model?: string; start_date?: string; end_date?: string }) =>
    apiClient.get('/model-usage', { params }),

  // 模型测试
  testConnection: (modelId: string, config: ModelConfig) =>
    apiClient.post(/models//test, config),
};
`

---

## 3. 通用组件

### 3.1 基础 UI 组件

`	sx
// src/components/ui/StatusBadge.tsx
import { Tag } from 'antd';
import type { StatusType } from '@/types';

const statusConfig: Record<StatusType, { color: string; label: string }> = {
  idle:     { color: 'default',  label: '空闲' },
  running:  { color: 'processing', label: '运行中' },
  error:    { color: 'error',    label: '错误' },
  stopped:  { color: 'default',  label: '已停止' },
};

export const StatusBadge: React.FC<{ status: StatusType }> = ({ status }) => {
  const config = statusConfig[status] || statusConfig.idle;
  return <Tag color={config.color}>{config.label}</Tag>;
};
`

`	sx
// src/components/ui/ModelTag.tsx
import { Tag } from 'antd';

interface ModelTagProps {
  provider: string;
  model: string;
}

const providerColors: Record<string, string> = {
  openai: 'blue',
  anthropic: 'orange',
  google: 'green',
  azure: 'purple',
  custom: 'geekblue',
};

export const ModelTag: React.FC<ModelTagProps> = ({ provider, model }) => {
  const color = providerColors[provider] || 'default';
  return <Tag color={color}>{provider.toUpperCase()} / {model}</Tag>;
};
`

`	sx
// src/components/ui/EmptyState.tsx
import { Result } from 'antd';

export const EmptyState: React.FC<{
  title?: string;
  description?: string;
  action?: React.ReactNode;
}> = ({ title = '暂无数据', description, action }) => (
  <Result
    status="info"
    title={title}
    subTitle={description}
    extra={action}
  />
);
`

### 3.2 布局组件

`	sx
// src/layouts/MainLayout.tsx
import { Layout, Menu, type MenuProps } from 'antd';
import { Outlet, useNavigate, useSearchParams } from 'react-router-dom';
import {
  DashboardOutlined,
  RobotOutlined,
  CloudServerOutlined,
  BranchesOutlined,
  AppstoreOutlined,
  SettingOutlined,
} from '@ant-design/icons';

const { Sider, Content } = Layout;

const menuItems: MenuProps['items'] = [
  { key: '/dashboard', icon: <DashboardOutlined />, label: '概览' },
  { key: '/agents', icon: <RobotOutlined />, label: 'Agent 管理' },
  { key: '/mcp', icon: <CloudServerOutlined />, label: 'MCP 管理' },
  { key: '/workflows', icon: <BranchesOutlined />, label: '工作流' },
  { key: '/models', icon: <AppstoreOutlined />, label: '模型管理' },
  { key: '/settings', icon: <SettingOutlined />, label: '系统设置' },
];

export const MainLayout: React.FC = () => {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const key = searchParams.get('tab') || '/dashboard';

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider theme="light" width={200}>
        <div style={{ padding: '16px', fontSize: 18, fontWeight: 600 }}>
          Agent 管理平台
        </div>
        <Menu
          mode="inline"
          items={menuItems}
          selectedKeys={[key]}
          onClick={({ key: k }) => navigate('?tab=' + k)}
        />
      </Sider>
      <Content style={{ margin: 16 }}>
        <Outlet />
      </Content>
    </Layout>
  );
};
`

### 3.3 通用业务组件

`	sx
// src/components/common/DataTable.tsx
import { Table, type TableProps } from 'antd';
import { useState } from 'react';
import type { PaginationConfig } from 'antd/es/table/interface';

interface DataTableProps<T> extends Omit<TableProps<T>, 'pagination'> {
  api: {
    list: (params: { page: number; size: number; keyword?: string }) => Promise<any>;
  };
  searchPlaceholder?: string;
  defaultPageSize?: number;
}

export const DataTable = <T,>({
  columns,
  api,
  searchPlaceholder = '搜索',
  defaultPageSize = 10,
  ...rest
}: DataTableProps<T>) => {
  const [data, setData] = useState<T[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [search, setSearch] = useState('');

  const fetchData = async (page = 1, size = defaultPageSize) => {
    setLoading(true);
    try {
      const res = await api.list({ page, size, keyword: search });
      setData(res.data.items || []);
      setTotal(res.data.total || 0);
    } finally {
      setLoading(false);
    }
  };

  const handleSearch = (value: string) => {
    setSearch(value);
    fetchData(1);
  };

  const pagination: PaginationConfig = {
    current: 1,
    total,
    pageSize: defaultPageSize,
    onChange: (page) => fetchData(page),
  };

  return (
    <div>
      <Table<T>
        columns={columns}
        dataSource={data}
        loading={loading}
        pagination={pagination}
        {...rest}
      />
    </div>
  );
};
`

`	sx
// src/components/common/ModelConfigForm.tsx
import { Form, Input, InputNumber, Select, Switch, Card } from 'antd';
import type { ModelConfig } from '@/types';

interface ModelConfigFormProps {
  initialValues?: Partial<ModelConfig>;
  onSubmit: (values: ModelConfig) => void;
}

export const ModelConfigForm: React.FC<ModelConfigFormProps> = ({ initialValues, onSubmit }) => {
  const [form] = Form.useForm();

  const modelProviders = [
    { label: 'OpenAI', value: 'openai' },
    { label: 'Anthropic', value: 'anthropic' },
    { label: 'Google', value: 'google' },
    { label: 'Azure', value: 'azure' },
    { label: '自定义', value: 'custom' },
  ];

  const modelsByProvider: Record<string, string[]> = {
    openai: ['gpt-4', 'gpt-3.5-turbo', 'gpt-4o'],
    anthropic: ['claude-3-opus', 'claude-3-sonnet', 'claude-3-haiku'],
    google: ['gemini-pro', 'gemini-ultra'],
    azure: ['gpt-4', 'gpt-35-turbo'],
    custom: [],
  };

  return (
    <Form
      form={form}
      initialValues={initialValues}
      onFinish={onSubmit}
      layout="vertical"
    >
      <Card title="模型配置" style={{ marginBottom: 16 }}>
        <Form.Item label="提供商" name="provider" rules={[{ required: true }]}>
          <Select options={modelProviders} />
        </Form.Item>

        <Form.Item label="模型" name="model" rules={[{ required: true }]}>
          <Select>
            {initialValues?.provider && modelsByProvider[initialValues.provider]?.map(m => (
              <Select.Option key={m} value={m}>{m}</Select.Option>
            ))}
          </Select>
        </Form.Item>

        <Form.Item label="API Key" name="api_key" rules={[{ required: true }]}>
          <Input.Password />
        </Form.Item>

        <Form.Item label="自定义 Endpoint" name="endpoint">
          <Input placeholder="https://api.openai.com/v1" />
        </Form.Item>
      </Card>

      <Card title="生成参数" style={{ marginBottom: 16 }}>
        <Form.Item label="Temperature" name="temperature" rules={[{ required: true }]}>
          <InputNumber min={0} max={2} step={0.1} defaultValue={0.7} />
        </Form.Item>

        <Form.Item label="Max Tokens" name="max_tokens" rules={[{ required: true }]}>
          <InputNumber min={1} max={4096} step={1} defaultValue={1024} />
        </Form.Item>

        <Form.Item label="Top P" name="top_p" rules={[{ required: true }]}>
          <InputNumber min={0} max={1} step={0.1} defaultValue={0.9} />
        </Form.Item>

        <Form.Item label="频率惩罚" name="frequency_penalty">
          <InputNumber min={-2} max={2} step={0.1} defaultValue={0} />
        </Form.Item>

        <Form.Item label="存在惩罚" name="presence_penalty">
          <InputNumber min={-2} max={2} step={0.1} defaultValue={0} />
        </Form.Item>
      </Card>

      <Card title="高级设置">
        <Form.Item label="超时时间 (秒)" name="timeout">
          <InputNumber min={1} max={300} defaultValue={30} />
        </Form.Item>

        <Form.Item label="最大重试次数" name="max_retries">
          <InputNumber min={0} max={10} defaultValue={3} />
        </Form.Item>

        <Form.Item label="启用缓存" name="cache_enabled" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Card>
    </Form>
  );
};
`

---

## 4. 状态管理 (Zustand)

### 4.1 认证 Store

`	ypescript
// src/store/auth-store.ts
import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface AuthState {
  token: string | null;
  user: { id: string; name: string; roles: string[] } | null;
  isAuthenticated: boolean;
  setAuth: (token: string, user: AuthState['user']) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      user: null,
      isAuthenticated: false,

      setAuth: (token, user) =>
        set({ token, user, isAuthenticated: true }),

      logout: () =>
        set({ token: null, user: null, isAuthenticated: false }),
    }),
    { name: 'auth-storage' }
  )
);
`

### 4.2 设置 Store

`	ypescript
// src/store/settings-store.ts
import { create } from 'zustand';

interface SettingsState {
  theme: 'light' | 'dark';
  setTheme: (theme: 'light' | 'dark') => void;
  sidebarCollapsed: boolean;
  toggleSidebar: () => void;
}

export const useSettingsStore = create<SettingsState>()((set) => ({
  theme: 'light',
  setTheme: (theme) => set({ theme }),
  sidebarCollapsed: false,
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
}));
`

### 4.3 模型 Store

`	ypescript
// src/store/model-store.ts
import { create } from 'zustand';
import { modelApi } from '@/api/model';
import type { ModelTemplate, ModelUsage } from '@/types';

interface ModelState {
  templates: ModelTemplate[];
  usage: ModelUsage | null;
  loading: boolean;
  error: string | null;
  setTemplates: (templates: ModelTemplate[]) => void;
  loadTemplates: () => Promise<void>;
  loadUsage: (teamId: string) => Promise<void>;
}

export const useModelStore = create<ModelState>()((set) => ({
  templates: [],
  usage: null,
  loading: false,
  error: null,

  setTemplates: (templates) => set({ templates }),

  loadTemplates: async () => {
    set({ loading: true, error: null });
    try {
      const response = await modelApi.listTemplates({ page: 1, size: 100 });
      set({ templates: response.data.items || [], loading: false });
    } catch (error) {
      set({ error: 'Failed to load templates', loading: false });
    }
  },

  loadUsage: async (teamId) => {
    set({ loading: true, error: null });
    try {
      const response = await modelApi.getUsage({ team_id: teamId });
      set({ usage: response.data, loading: false });
    } catch (error) {
      set({ error: 'Failed to load usage', loading: false });
    }
  },
}));
`

---

## 5. 路由配置

### 5.1 路由结构

`	ypescript
// src/router/index.ts
import { createBrowserRouter, Navigate } from 'react-router-dom';
import { MainLayout } from '@/layouts/MainLayout';
import { AuthLayout } from '@/layouts/AuthLayout';
import { ProtectedRoute } from './guards';

const routes = [
  // 认证路由 (不需要登录)
  {
    path: '/auth',
    element: <AuthLayout />,
    children: [
      { path: 'login', element: <LoginPage /> },
      { path: 'register', element: <RegisterPage /> },
    ],
  },

  // 主应用路由 (需要登录)
  {
    path: '/',
    element: <ProtectedRoute><MainLayout /></ProtectedRoute>,
    children: [
      { index: element: <Navigate to="/dashboard" replace /> },
      { path: 'dashboard', element: <DashboardPage /> },
      { path: 'agents', element: <AgentListPage /> },
      { path: 'agents/:id', element: <AgentDetailPage /> },
      { path: 'mcp', element: <MCPListPage /> },
      { path: 'mcp/:id', element: <MCPDetailPage /> },
      { path: 'workflows', element: <WorkflowListPage /> },
      { path: 'workflows/:id', element: <WorkflowDetailPage /> },
      { path: 'models', element: <ModelListPage /> },
      { path: 'models/:id', element: <ModelDetailPage /> },
      { path: 'settings', element: <SettingsPage /> },
    ],
  },
];

export const router = createBrowserRouter(routes);
`

### 5.2 路由守卫

`	ypescript
// src/router/guards.tsx
import { Navigate, useLocation } from 'react-router-dom';
import { useAuthStore } from '@/store/auth-store';

export const ProtectedRoute: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { isAuthenticated } = useAuthStore();
  const location = useLocation();

  if (!isAuthenticated) {
    return <Navigate to="/auth/login" state={{ from: location }} replace />;
  }

  return <>{children}</>;
};
`

---

## 6. 页面组件模式

### 6.1 列表页标准结构

`	sx
// src/pages/agent/AgentListPage.tsx
import { useState } from 'react';
import { Button, Card, Input, Space } from 'antd';
import { PlusOutlined, SearchOutlined } from '@ant-design/icons';
import { DataTable } from '@/components/common/DataTable';
import { StatusBadge } from '@/components/ui/StatusBadge';
import { agentApi } from '@/api/agent';
import { CreateAgentModal } from './CreateAgentModal';
import type { Agent } from '@/types';

export const AgentListPage: React.FC = () => {
  const [createModalOpen, setCreateModalOpen] = useState(false);

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => <StatusBadge status={status as any} />,
    },
    {
      title: '模型',
      dataIndex: 'model',
      key: 'model',
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: '操作',
      key: 'actions',
      render: (_: any, record: Agent) => (
        <Space>
          <a href={'/agents/' + record.id}>详情</a>
          <a onClick={() => handleStart(record.id)}>启动</a>
          <a onClick={() => handleStop(record.id)}>停止</a>
          <a onClick={() => handleDelete(record.id)}>删除</a>
        </Space>
      ),
    },
  ];

  return (
    <Card>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between' }}>
        <Input.Search
          placeholder="搜索 Agent"
          allowClear
          style={{ width: 300 }}
          onSearch={(v) => console.log(v)}
        />
        <Space>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateModalOpen(true)}>
            新建 Agent
          </Button>
        </Space>
      </div>
      <DataTable
        columns={columns}
        api={agentApi}
        defaultPageSize={10}
      />
      <CreateAgentModal
        open={createModalOpen}
        onCancel={() => setCreateModalOpen(false)}
      />
    </Card>
  );
};
`

### 6.2 模型管理页面

`	sx
// src/pages/model/ModelListPage.tsx
import { useState } from 'react';
import { Button, Card, Input, Space, Tag } from 'antd';
import { PlusOutlined, SearchOutlined, ExperimentOutlined } from '@ant-design/icons';
import { DataTable } from '@/components/common/DataTable';
import { ModelTag } from '@/components/ui/ModelTag';
import { modelApi } from '@/api/model';
import { CreateModelModal } from './CreateModelModal';
import { ModelConfigForm } from '@/components/common/ModelConfigForm';
import type { ModelTemplate } from '@/types';

export const ModelListPage: React.FC = () => {
  const [createModalOpen, setCreateModalOpen] = useState(false);
  const [testModalOpen, setTestModalOpen] = useState(false);
  const [selectedModel, setSelectedModel] = useState<ModelTemplate | null>(null);

  const columns = [
    {
      title: '模板名称',
      dataIndex: 'name',
      key: 'name',
    },
    {
      title: '提供商',
      dataIndex: 'provider',
      key: 'provider',
      render: (provider: string) => <ModelTag provider={provider} model={''} />,
    },
    {
      title: '模型',
      dataIndex: 'model',
      key: 'model',
    },
    {
      title: '温度',
      dataIndex: 'temperature',
      key: 'temperature',
      render: (temp: number) => temp.toFixed(1),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => (
        <Tag color={status === 'active' ? 'green' : 'default'}>
          {status === 'active' ? '可用' : '禁用'}
        </Tag>
      ),
    },
    {
      title: '操作',
      key: 'actions',
      render: (_: any, record: ModelTemplate) => (
        <Space>
          <a onClick={() => handleTest(record)}>测试</a>
          <a onClick={() => handleEdit(record)}>编辑</a>
          <a onClick={() => handleDelete(record.id)}>删除</a>
        </Space>
      ),
    },
  ];

  return (
    <Card>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between' }}>
        <Input.Search
          placeholder="搜索模型模板"
          allowClear
          style={{ width: 300 }}
          onSearch={(v) => console.log(v)}
        />
        <Space>
          <Button onClick={() => setTestModalOpen(true)}>
            <ExperimentOutlined /> 快速测试
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateModalOpen(true)}>
            新建模板
          </Button>
        </Space>
      </div>
      <DataTable
        columns={columns}
        api={modelApi}
        defaultPageSize={10}
      />
      <CreateModelModal
        open={createModalOpen}
        onCancel={() => setCreateModalOpen(false)}
      />
    </Card>
  );
};

// 新建模型模板弹窗
export const CreateModelModal: React.FC<{
  open: boolean;
  onCancel: () => void;
}> = ({ open, onCancel }) => {
  const handleSubmit = async (values: ModelConfig) => {
    // 创建模型模板
    await modelApi.createTemplate(values);
    onCancel();
  };

  return (
    <Modal
      title="新建模型模板"
      open={open}
      onCancel={onCancel}
      footer={null}
      width={800}
    >
      <ModelConfigForm onSubmit={handleSubmit} />
    </Modal>
  );
};
`

---

## 7. 样式规范

### 7.1 全局变量

`css
/* src/styles/variables.css */
:root {
  --color-primary: #1677ff;
  --color-success: #52c41a;
  --color-warning: #faad14;
  --color-error: #ff4d4f;
  --color-text: #333;
  --color-text-secondary: #666;
  --color-border: #e8e8e8;
  --color-bg: #f5f5f5;
  --color-bg-white: #ffffff;
  --radius-sm: 4px;
  --radius-md: 8px;
  --radius-lg: 12px;
  --shadow-sm: 0 1px 2px rgba(0, 0, 0, 0.05);
  --shadow-md: 0 4px 12px rgba(0, 0, 0, 0.1);
}
`

### 7.2 样式约定

| 规则 | 说明 |
|------|------|
| 优先使用 Ant Design 组件 | 减少自定义样式 |
| CSS Module 或 CSS-in-JS | 避免全局样式污染 |
| 响应式断点 | sm: 576px, md: 768px, lg: 992px, xl: 1200px |
| 间距单位 | 4px 倍数 (4/8/12/16/24/32/48) |

---

## 8. 组件审查清单

- [ ] 组件是否有 TypeScript 类型定义
- [ ] 是否处理了空数据状态
- [ ] 是否处理了加载状态
- [ ] 是否处理了错误状态
- [ ] 组件是否有合理的 prop 默认值
- [ ] 事件处理是否有防抖/节流 (高频操作)
- [ ] 列表渲染是否有唯一 key
- [ ] 是否避免了直接操作 DOM
- [ ] 是否符合访问性 (a11y) 规范
- [ ] 组件是否可复用

---

*文档维护：前端组件库更新时同步更新本文档。*