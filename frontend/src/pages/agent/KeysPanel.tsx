import { useCallback, useEffect, useState } from 'react';
import {
  App,
  Button,
  Card,
  DatePicker,
  Descriptions,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
} from 'antd';
import { KeyOutlined, PlusOutlined } from '@ant-design/icons';
import dayjs, { type Dayjs } from 'dayjs';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import type { AgentAPIKey } from '@/types';
import { formatDateTime } from '@/utils/format';

interface KeysPanelProps {
  agentId: string;
}

// API Key 管理: 创建(明文仅一次) / 列表 / 吊销 / 删除(仅已吊销)
export function KeysPanel({ agentId }: KeysPanelProps) {
  const { message } = App.useApp();
  const [keys, setKeys] = useState<AgentAPIKey[]>([]);
  const [loading, setLoading] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [keyName, setKeyName] = useState('');
  const [keyExpires, setKeyExpires] = useState<Dayjs | null>(null);
  const [creating, setCreating] = useState(false);
  const [createdKey, setCreatedKey] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await agentApi.listKeys(agentId);
      setKeys(res.data?.items ?? []);
    } catch (err) {
      message.error(getErrorMessage(err, '加载 API Key 失败'));
    } finally {
      setLoading(false);
    }
  }, [agentId, message]);

  useEffect(() => {
    load();
  }, [load]);

  const onCreate = async () => {
    setCreating(true);
    try {
      const res = await agentApi.createKey(agentId, keyName.trim(), keyExpires ? keyExpires.toISOString() : undefined);
      setCreatedKey(res.data?.key ?? null);
      setCreateOpen(false);
      setKeyName('');
      setKeyExpires(null);
      await load();
    } catch (err) {
      message.error(getErrorMessage(err));
    } finally {
      setCreating(false);
    }
  };

  const onRevoke = async (key: AgentAPIKey) => {
    try {
      await agentApi.revokeKey(agentId, key.id);
      message.success(`已吊销 ${key.name}`);
      await load();
    } catch (err) {
      message.error(getErrorMessage(err));
    }
  };

  const onDelete = async (key: AgentAPIKey) => {
    try {
      await agentApi.deleteKey(agentId, key.id);
      message.success(`已删除 ${key.name}`);
      await load();
    } catch (err) {
      message.error(getErrorMessage(err));
    }
  };

  return (
    <Card
      size="small"
      title="API Key"
      extra={
        <Button type="primary" size="small" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          创建 Key
        </Button>
      }
    >
      <Table<AgentAPIKey>
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={keys}
        pagination={false}
        columns={[
          { title: '名称', dataIndex: 'name', width: 160 },
          {
            title: '前缀',
            dataIndex: 'key_prefix',
            width: 160,
            render: (v: string) => <span className="mono-text">{v}...</span>,
          },
          {
            title: '状态',
            dataIndex: 'status',
            width: 90,
            render: (s: string, record: AgentAPIKey) => {
              if (s !== 'active') return <Tag color="default">已吊销</Tag>;
              if (record.expires_at && new Date(record.expires_at).getTime() < Date.now()) {
                return <Tag color="red">已过期</Tag>;
              }
              return <Tag color="green">有效</Tag>;
            },
          },
          {
            title: '创建时间',
            dataIndex: 'created_at',
            width: 170,
            render: (v: string) => formatDateTime(v),
          },
          {
            title: '最近使用',
            dataIndex: 'last_used_at',
            width: 170,
            render: (v?: string | null) => formatDateTime(v),
          },
          {
            title: '过期时间',
            dataIndex: 'expires_at',
            width: 170,
            render: (v?: string | null) => (v ? formatDateTime(v) : <Tag>永久</Tag>),
          },
          {
            title: '操作',
            key: 'actions',
            width: 90,
            render: (_: unknown, record: AgentAPIKey) =>
              record.status === 'active' ? (
                <Popconfirm title={`吊销 ${record.name}？`} onConfirm={() => onRevoke(record)} okText="吊销" okButtonProps={{ danger: true }}>
                  <Button size="small" danger>
                    吊销
                  </Button>
                </Popconfirm>
              ) : (
                <Popconfirm
                  title={`删除 ${record.name}？`}
                  onConfirm={() => onDelete(record)}
                  okText="删除"
                  okButtonProps={{ danger: true }}
                >
                  <Button size="small" danger>
                    删除
                  </Button>
                </Popconfirm>
              ),
          },
        ]}
      />

      <Modal
        title="创建 API Key"
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onOk={onCreate}
        confirmLoading={creating}
        okText="创建"
      >
        <Space direction="vertical" size="small" style={{ width: '100%' }}>
          <Input
            placeholder="Key 名称，如 ci-deploy"
            value={keyName}
            onChange={(e) => setKeyName(e.target.value)}
            maxLength={64}
          />
          <DatePicker
            showTime
            style={{ width: '100%' }}
            placeholder="可选：过期时间（留空永久有效）"
            value={keyExpires}
            onChange={setKeyExpires}
            disabledDate={(d) => d < dayjs().startOf('day')}
          />
        </Space>
      </Modal>

      <Modal
        title={
          <Space>
            <KeyOutlined />
            新 API Key
          </Space>
        }
        open={createdKey !== null}
        onCancel={() => setCreatedKey(null)}
        footer={[
          <Button key="copy" onClick={() => { void navigator.clipboard.writeText(createdKey ?? ''); message.success('已复制到剪贴板'); }}>
            复制
          </Button>,
          <Button key="close" type="primary" onClick={() => setCreatedKey(null)}>
            我已保存
          </Button>,
        ]}
      >
        <Descriptions column={1} size="small">
          <Descriptions.Item label="密钥">
            <Typography.Text className="mono-text" copyable>
              {createdKey}
            </Typography.Text>
          </Descriptions.Item>
        </Descriptions>
        <Typography.Paragraph type="warning" style={{ marginTop: 12, marginBottom: 0 }}>
          该密钥仅显示这一次，请立即保存到安全位置。之后只能查看前缀。
        </Typography.Paragraph>
      </Modal>
    </Card>
  );
}
