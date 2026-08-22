import { useEffect, useMemo, useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { Avatar, Dropdown, Layout, Menu, Space, Typography, type MenuProps } from 'antd';
import {
  AppstoreOutlined,
  AuditOutlined,
  ClusterOutlined,
  DashboardOutlined,
  LogoutOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  SwapOutlined,
  TeamOutlined,
  ThunderboltOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useAuthStore } from '@/store/auth-store';

const { Sider, Header, Content } = Layout;

// 主布局: 侧边导航 + 顶部用户信息
export function MainLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(false);
  const { username, logout, permissions, fetchMe } = useAuthStore();

  // 拉取当前用户角色/权限 (菜单与路由守卫依赖)
  useEffect(() => {
    fetchMe();
  }, [fetchMe]);

  const menuItems = useMemo<MenuProps['items']>(() => {
    const items: NonNullable<MenuProps['items']> = [
      {
        key: '/dashboard',
        icon: <DashboardOutlined />,
        label: '概览',
      },
      {
        key: '/agents',
        icon: <RobotOutlined />,
        label: 'Agent 管理',
      },
      {
        type: 'divider' as const,
      },
      {
        key: '/mcp',
        icon: <ClusterOutlined />,
        label: 'MCP 管理',
      },
      {
        key: '/approvals',
        icon: <AuditOutlined />,
        label: '审核中心',
      },
      {
        key: '/models',
        icon: <AppstoreOutlined />,
        label: '模型管理',
      },
      {
        key: '/skills',
        icon: <ThunderboltOutlined />,
        label: '技能管理',
      },
      {
        key: '/workflows',
        icon: <SwapOutlined />,
        label: '工作流',
      },
    ];
    // 系统管理: 按权限显示 (user:manage / role:manage)
    const systemItems: NonNullable<MenuProps['items']> = [];
    if (permissions.includes('user:manage')) {
      systemItems.push({ key: '/system/users', icon: <TeamOutlined />, label: '用户管理' });
    }
    if (permissions.includes('role:manage')) {
      systemItems.push({ key: '/system/roles', icon: <SafetyCertificateOutlined />, label: '角色管理' });
    }
    if (systemItems.length > 0) {
      items.push({ type: 'divider' as const });
      items.push(...systemItems);
    }
    return items;
  }, [permissions]);

  const selectedKey = location.pathname.startsWith('/agents')
      ? '/agents'
      : location.pathname.startsWith('/mcp')
        ? '/mcp'
        : location.pathname.startsWith('/approvals')
          ? '/approvals'
          : location.pathname.startsWith('/models')
            ? '/models'
            : location.pathname.startsWith('/skills')
              ? '/skills'
              : location.pathname.startsWith('/workflows')
              ? '/workflows'
              : location.pathname;

  const onLogout = () => {
    logout();
    navigate('/login');
  };

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider collapsible collapsed={collapsed} onCollapse={setCollapsed}>
        <div
          style={{
            height: 48,
            margin: 16,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: '#fff',
            fontWeight: 600,
            fontSize: collapsed ? 14 : 16,
            whiteSpace: 'nowrap',
            overflow: 'hidden',
          }}
        >
          {collapsed ? 'AP' : 'Agent 管理平台'}
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[selectedKey]}
          items={menuItems}
          onClick={({ key }) => navigate(key)}
        />
      </Sider>
      <Layout>
        <Header
          style={{
            background: 'var(--color-bg-white)',
            padding: '0 24px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'flex-end',
            boxShadow: 'var(--shadow-sm)',
          }}
        >
          <Dropdown
            menu={{
              items: [
                {
                  key: 'logout',
                  icon: <LogoutOutlined />,
                  label: '退出登录',
                  onClick: onLogout,
                },
              ],
            }}
          >
            <Space style={{ cursor: 'pointer' }}>
              <Avatar size="small" icon={<UserOutlined />} />
              <Typography.Text>{username || '未登录'}</Typography.Text>
            </Space>
          </Dropdown>
        </Header>
        <Content style={{ margin: 24, padding: 24, background: 'var(--color-bg-white)', borderRadius: 8, overflow: 'auto' }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  );
}
