import { createBrowserRouter, Navigate } from 'react-router-dom';
import { MainLayout } from '@/layouts/MainLayout';
import { AuthLayout } from '@/layouts/AuthLayout';
import { RequireAuth, RequirePermission } from './guards';
import { LoginPage } from '@/pages/auth/LoginPage';
import { DashboardPage } from '@/pages/dashboard/DashboardPage';
import { AgentListPage } from '@/pages/agent/AgentListPage';
import { AgentFormPage } from '@/pages/agent/AgentFormPage';
import { AgentDetailPage } from '@/pages/agent/AgentDetailPage';
import { AgentLogPage } from '@/pages/agent/AgentLogPage';
import { MCPListPage } from '@/pages/mcp/MCPListPage';
import { MCPFormPage } from '@/pages/mcp/MCPFormPage';
import { MCPDetailPage } from '@/pages/mcp/MCPDetailPage';
import { ApprovalCenterPage } from '@/pages/mcp/ApprovalCenterPage';
import { WorkflowListPage } from '@/pages/workflow/WorkflowListPage';
import { WorkflowEditorPage } from '@/pages/workflow/WorkflowEditorPage';
import { WorkflowDetailPage } from '@/pages/workflow/WorkflowDetailPage';
import { ExecutionDetailPage } from '@/pages/workflow/ExecutionDetailPage';
import { ModelListPage } from '@/pages/model/ModelListPage';
import { ModelFormPage } from '@/pages/model/ModelFormPage';
import { ModelDetailPage } from '@/pages/model/ModelDetailPage';
import { SkillListPage } from '@/pages/skill/SkillListPage';
import { SkillDetailPage } from '@/pages/skill/SkillDetailPage';
import { UserListPage } from '@/pages/system/UserListPage';
import { RoleListPage } from '@/pages/system/RoleListPage';
import { PlatformSettingsPage } from '@/pages/system/PlatformSettingsPage';

export const router = createBrowserRouter([
  {
    path: '/login',
    element: (
      <AuthLayout>
        <LoginPage />
      </AuthLayout>
    ),
  },
  {
    path: '/',
    element: (
      <RequireAuth>
        <MainLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/dashboard" replace /> },
      { path: 'dashboard', element: <DashboardPage /> },
      { path: 'agents', element: <AgentListPage /> },
      { path: 'agents/new', element: <AgentFormPage /> },
      { path: 'agents/:id', element: <AgentDetailPage /> },
      { path: 'agents/:id/edit', element: <AgentFormPage /> },
      { path: 'agents/:id/logs', element: <AgentLogPage /> },
      { path: 'mcp', element: <MCPListPage /> },
      { path: 'mcp/new', element: <MCPFormPage /> },
      { path: 'mcp/:id', element: <MCPDetailPage /> },
      { path: 'mcp/:id/edit', element: <MCPFormPage /> },
      { path: 'approvals', element: <ApprovalCenterPage /> },
      { path: 'models', element: <ModelListPage /> },
      { path: 'models/new', element: <ModelFormPage /> },
      { path: 'models/:id', element: <ModelDetailPage /> },
      { path: 'models/:id/edit', element: <ModelFormPage /> },
      { path: 'skills', element: <SkillListPage /> },
      { path: 'skills/:id', element: <SkillDetailPage /> },
      { path: 'workflows', element: <WorkflowListPage /> },
      // 工作流看板已并入概览页, 旧地址重定向保持链接可用
      { path: 'workflows/dashboard', element: <Navigate to="/dashboard?tab=workflow" replace /> },
      { path: 'workflows/executions/:id', element: <ExecutionDetailPage /> },
      { path: 'workflows/:id', element: <WorkflowDetailPage /> },
      { path: 'workflows/:id/edit', element: <WorkflowEditorPage /> },
      {
        path: 'system/users',
        element: (
          <RequirePermission code="user:manage">
            <UserListPage />
          </RequirePermission>
        ),
      },
      {
        path: 'system/roles',
        element: (
          <RequirePermission code="role:manage">
            <RoleListPage />
          </RequirePermission>
        ),
      },
      {
        path: 'system/platform',
        element: (
          <RequirePermission code="platform:manage">
            <PlatformSettingsPage />
          </RequirePermission>
        ),
      },
      { path: '*', element: <Navigate to="/dashboard" replace /> },
    ],
  },
]);
