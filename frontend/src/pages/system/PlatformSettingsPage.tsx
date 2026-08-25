import { useEffect, useState } from 'react';
import { App, Button, Card, Form, Input, Space, Typography, Upload } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { UploadFile } from 'antd';
import { platformApi } from '@/api/platform';
import { getErrorMessage } from '@/api/client';
import { usePlatformStore } from '@/store/platform-store';

// 与后端校验规则保持一致
const ICON_ACCEPT = 'image/png,image/jpeg,image/svg+xml,image/webp,image/gif';
const ICON_MAX_SIZE = 1024 * 1024; // 1 MB
const NAME_MAX = 64;

// 平台设置: 平台名 + 平台图标 (登录页与侧边导航展示), 需 platform:manage 权限
export function PlatformSettingsPage() {
  const { message } = App.useApp();
  const { name, icon, loaded, updatedAt, fetchPlatform, setPlatform } = usePlatformStore();
  const [form] = Form.useForm<{ name: string }>();
  const [iconData, setIconData] = useState('');
  const [fileList, setFileList] = useState<UploadFile[]>([]);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    fetchPlatform();
  }, [fetchPlatform]);

  // 拉取成功后回填表单
  useEffect(() => {
    if (!loaded) return;
    form.setFieldsValue({ name });
    setIconData(icon);
    setFileList(icon ? [{ uid: '-1', name: 'icon', status: 'done', url: icon }] : []);
  }, [loaded, name, icon, form]);

  const onBeforeUpload = (file: File) => {
    if (!ICON_ACCEPT.split(',').includes(file.type)) {
      message.error('仅支持 PNG / JPG / SVG / WebP / GIF 图片');
      return Upload.LIST_IGNORE;
    }
    if (file.size > ICON_MAX_SIZE) {
      message.error('图标大小不能超过 1MB');
      return Upload.LIST_IGNORE;
    }
    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = String(reader.result || '');
      setIconData(dataUrl);
      setFileList([{ uid: '-1', name: file.name, status: 'done', url: dataUrl }]);
    };
    reader.readAsDataURL(file);
    return false; // 不实际上传, 仅转为 data URL 随表单提交
  };

  const onRemoveIcon = () => {
    setIconData('');
    setFileList([]);
    return true;
  };

  const onSubmit = async () => {
    const values = await form.validateFields();
    setSaving(true);
    try {
      const res = await platformApi.update({ name: values.name.trim(), icon: iconData });
      if (res.data) {
        setPlatform(res.data.name, res.data.icon || '', res.data.updated_at);
      }
      message.success('平台设置已保存, 登录页与侧边导航已更新');
    } catch (err) {
      message.error(getErrorMessage(err, '保存失败'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <Space direction="vertical" size={0}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            平台设置
          </Typography.Title>
          <Typography.Text type="secondary">平台名与图标将展示在登录页、侧边导航及浏览器标签页</Typography.Text>
        </Space>
      </div>

      <Card>
        <Form form={form} layout="vertical" style={{ maxWidth: 560 }} requiredMark={false}>
          <Form.Item
            name="name"
            label="平台名称"
            rules={[
              { required: true, message: '请输入平台名称' },
              { max: NAME_MAX, message: `平台名称不能超过 ${NAME_MAX} 个字符` },
            ]}
          >
            <Input placeholder="如 Agent 管理平台" maxLength={NAME_MAX} showCount />
          </Form.Item>

          <Form.Item label="平台图标">
            <Upload
              listType="picture-card"
              accept={ICON_ACCEPT}
              maxCount={1}
              fileList={fileList}
              beforeUpload={onBeforeUpload}
              onRemove={onRemoveIcon}
            >
              {fileList.length === 0 && (
                <div>
                  <PlusOutlined />
                  <div style={{ marginTop: 8 }}>上传图标</div>
                </div>
              )}
            </Upload>
            <Typography.Text type="secondary" style={{ display: 'block', marginTop: 8 }}>
              PNG / JPG / SVG / WebP / GIF, 大小不超过 1MB; 不上传则使用内置默认图标
            </Typography.Text>
          </Form.Item>

          <Form.Item style={{ marginBottom: 0 }}>
            <Space>
              <Button type="primary" loading={saving} onClick={onSubmit}>
                保存
              </Button>
              {updatedAt && (
                <Typography.Text type="secondary">最近更新: {updatedAt}</Typography.Text>
              )}
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
}
