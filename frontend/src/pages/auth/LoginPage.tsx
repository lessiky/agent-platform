import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Alert, App, Button, Card, Form, Input, Tabs } from 'antd';
import { LockOutlined, MailOutlined, UserOutlined } from '@ant-design/icons';
import { authApi } from '@/api/auth';
import { getErrorMessage } from '@/api/client';
import { useAuthStore } from '@/store/auth-store';
import { usePlatformStore } from '@/store/platform-store';
import { PlatformLogo } from '@/components/common/PlatformLogo';

interface LoginForm {
  username: string;
  password: string;
}

interface RegisterForm {
  username: string;
  email: string;
  password: string;
}

export function LoginPage() {
  const navigate = useNavigate();
  const { message } = App.useApp();
  const setAuth = useAuthStore((s) => s.setAuth);
  const { name, fetchPlatform } = usePlatformStore();
  const [submitting, setSubmitting] = useState(false);
  const [loginForm] = Form.useForm<LoginForm>();
  const [registerForm] = Form.useForm<RegisterForm>();
  const [error, setError] = useState<string | null>(null);

  // 拉取平台名/图标 (登录页品牌展示)
  useEffect(() => {
    fetchPlatform();
  }, [fetchPlatform]);

  const onLogin = async (values: LoginForm) => {
    setSubmitting(true);
    setError(null);
    try {
      const res = await authApi.login(values.username, values.password);
      if (!res.data?.token) throw new Error('登录响应异常');
      setAuth(res.data.token, values.username);
      message.success('登录成功');
      navigate('/dashboard');
    } catch (err) {
      setError(getErrorMessage(err, '登录失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const onRegister = async (values: RegisterForm) => {
    setSubmitting(true);
    setError(null);
    try {
      // 后端注册接口不返回 token, 注册成功后自动登录获取
      await authApi.register(values.username, values.email, values.password);
      const res = await authApi.login(values.username, values.password);
      if (!res.data?.token) throw new Error('登录响应异常');
      setAuth(res.data.token, values.username);
      message.success('注册成功，已自动登录');
      navigate('/dashboard');
    } catch (err) {
      setError(getErrorMessage(err, '注册失败'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Card style={{ width: 400, boxShadow: 'var(--shadow-md)' }}>
      <div style={{ textAlign: 'center', marginBottom: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 12 }}>
          <PlatformLogo size={52} />
        </div>
        <h2 style={{ margin: 0, fontSize: 20 }}>{name}</h2>
        <p style={{ color: 'var(--color-text-secondary)' }}>登录后管理你的 Agent</p>
      </div>
      {error && (
        <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} closable onClose={() => setError(null)} />
      )}
      <Tabs
        centered
        items={[
          {
            key: 'login',
            label: '登录',
            children: (
              <Form form={loginForm} layout="vertical" onFinish={onLogin} requiredMark={false}>
                <Form.Item
                  name="username"
                  label="用户名"
                  rules={[{ required: true, message: '请输入用户名' }]}
                >
                  <Input prefix={<UserOutlined />} placeholder="用户名" autoFocus />
                </Form.Item>
                <Form.Item
                  name="password"
                  label="密码"
                  rules={[{ required: true, message: '请输入密码' }]}
                >
                  <Input.Password prefix={<LockOutlined />} placeholder="密码" />
                </Form.Item>
                <Button type="primary" htmlType="submit" block loading={submitting}>
                  登录
                </Button>
              </Form>
            ),
          },
          {
            key: 'register',
            label: '注册',
            children: (
              <Form form={registerForm} layout="vertical" onFinish={onRegister} requiredMark={false}>
                <Form.Item
                  name="username"
                  label="用户名"
                  rules={[
                    { required: true, message: '请输入用户名' },
                    { min: 2, max: 64, message: '长度 2-64 个字符' },
                  ]}
                >
                  <Input prefix={<UserOutlined />} placeholder="用户名" />
                </Form.Item>
                <Form.Item
                  name="email"
                  label="邮箱"
                  rules={[
                    { required: true, message: '请输入邮箱' },
                    { type: 'email', message: '邮箱格式不正确' },
                  ]}
                >
                  <Input prefix={<MailOutlined />} placeholder="邮箱" />
                </Form.Item>
                <Form.Item
                  name="password"
                  label="密码"
                  rules={[
                    { required: true, message: '请输入密码' },
                    { min: 6, message: '密码至少 6 位' },
                  ]}
                >
                  <Input.Password prefix={<LockOutlined />} placeholder="密码" />
                </Form.Item>
                <Button type="primary" htmlType="submit" block loading={submitting}>
                  注册并登录
                </Button>
              </Form>
            ),
          },
        ]}
      />
    </Card>
  );
}
