import type { ReactNode } from 'react';
import { Layout } from 'antd';

// 登录页布局: 居中卡片
export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <Layout style={{ minHeight: '100vh', background: 'var(--color-bg)' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          minHeight: '100vh',
        }}
      >
        {children}
      </div>
    </Layout>
  );
}