import { useEffect, useState } from 'react';
import { App, AutoComplete, Button, Card, Form, Input, Space, Typography, Upload } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { UploadFile } from 'antd';
import { platformApi } from '@/api/platform';
import { modelApi } from '@/api/model';
import { getErrorMessage } from '@/api/client';
import { usePlatformStore } from '@/store/platform-store';

// 与后端校验规则保持一致
const ICON_ACCEPT = 'image/png,image/jpeg,image/svg+xml,image/webp,image/gif';
const ICON_MAX_SIZE = 1024 * 1024; // 1 MB
const NAME_MAX = 64;

// 平台设置: 平台名 + 平台图标 (登录页与侧边导航展示) + 记忆语义检索向量模型 (运行时生效, 免重启), 需 platform:manage 权限
export function PlatformSettingsPage() {
  const { message } = App.useApp();
  const {
    name,
    icon,
    memoryEmbedModel,
    memoryEmbedModelEffective,
    memoryExtractModel,
    memoryExtractModelEffective,
    loaded,
    updatedAt,
    fetchPlatform,
    setPlatform,
    setModelSettings,
  } = usePlatformStore();
  const [form] = Form.useForm<{ name: string; memory_embed_model?: string; memory_extract_model?: string }>();
  const [iconData, setIconData] = useState('');
  const [fileList, setFileList] = useState<UploadFile[]>([]);
  const [modelOptions, setModelOptions] = useState<{ value: string; label: string }[]>([]);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    fetchPlatform();
  }, [fetchPlatform]);

  // 向量模型下拉候选: 模型模板名称 (无 model:read 权限或拉取失败时保持手输可用)
  useEffect(() => {
    modelApi
      .list({ page: 1, size: 100 })
      .then((res) => {
        const items = res.data?.items ?? [];
        setModelOptions(items.map((t) => ({ value: t.name, label: `${t.name} (${t.model})` })));
      })
      .catch(() => {
        // 忽略: 下拉为空仍可手输模板名
      });
  }, []);

  // 拉取成功后回填表单
  useEffect(() => {
    if (!loaded) return;
    form.setFieldsValue({
      name,
      memory_embed_model: memoryEmbedModel || '',
      memory_extract_model: memoryExtractModel || '',
    });
    setIconData(icon);
    setFileList(icon ? [{ uid: '-1', name: 'icon', status: 'done', url: icon }] : []);
  }, [loaded, name, icon, memoryEmbedModel, memoryExtractModel, form]);

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
      const res = await platformApi.update({
        name: values.name.trim(),
        icon: iconData,
        memory_embed_model: (values.memory_embed_model ?? '').trim(),
        memory_extract_model: (values.memory_extract_model ?? '').trim(),
      });
      if (res.data) {
        setPlatform(res.data.name, res.data.icon || '', res.data.updated_at);
        setModelSettings(
          res.data.memory_embed_model || '',
          res.data.memory_embed_model_effective || '',
          res.data.memory_extract_model || '',
          res.data.memory_extract_model_effective || '',
        );
      }
      message.success('平台设置已保存, 模型设置已即时生效');
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

          <Form.Item
            name="memory_embed_model"
            label="记忆语义检索模型 (Embedding)"
            extra={
              <>
                向量专用模型模板名称 (OpenAI 兼容 /embeddings 端点), 保存后即时生效无需重启;
                留空则跟随环境变量 MEMORY_EMBED_MODEL。
                {memoryEmbedModelEffective && (
                  <span> 当前生效: {memoryEmbedModelEffective}</span>
                )}
                {!memoryEmbedModel && !memoryEmbedModelEffective && (
                  <span> 未配置时语义检索不生效 (纯关键词检索)</span>
                )}
              </>
            }
          >
            <AutoComplete
              allowClear
              options={modelOptions}
              placeholder="跟随环境变量 MEMORY_EMBED_MODEL"
            />
          </Form.Item>

          <Form.Item
            name="memory_extract_model"
            label="记忆抽取 / 会话摘要模型 (Extract)"
            extra={
              <>
                记忆自动抽取与会话滚动摘要使用的模型模板名称, 保存后即时生效无需重启;
                留空则跟随环境变量 MEMORY_EXTRACT_MODEL。
                {memoryExtractModelEffective ? (
                  <span> 当前生效: {memoryExtractModelEffective}</span>
                ) : (
                  <span> 当前使用 Agent 各自配置的模型</span>
                )}
              </>
            }
          >
            <AutoComplete
              allowClear
              options={modelOptions}
              placeholder="跟随环境变量 MEMORY_EXTRACT_MODEL (空则用 Agent 当前模型)"
            />
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
