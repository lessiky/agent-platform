import { useEffect, type ReactNode } from 'react';
import { Navigate } from 'react-router-dom';
import { Result, Spin } from 'antd';
import { useAuthStore } from '@/store/auth-store';

// 登录守卫: 未登录跳转登录页
export function RequireAuth({ children }: { children: ReactNode }) {
  const token = useAuthStore((s) => s.token);
  if (!token) {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
}

// 权限守卫: 无对应权限码展示 403 (拉取 /auth/me 前显示加载)
export function RequirePermission({ code, children }: { code: string; children: ReactNode }) {
  const { meLoaded, permissions, fetchMe } = useAuthStore();

  useEffect(() => {
    fetchMe();
  }, [fetchMe]);

  if (meLoaded && !permissions.includes(code)) {
    return (
      <Result
        status="403"
        title="403"
        subTitle="权限不足, 请联系管理员分配相应角色"
        extra={
          <a onClick={() => window.history.back()}>
            返回
          </a>
        }
      />
    );
  }

  if (!meLoaded) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '60vh' }}>
        <Spin size="large" />
      </div>
    );
  }
  return <>{children}</>;
}
