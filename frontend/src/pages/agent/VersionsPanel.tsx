import { useCallback, useEffect, useState } from 'react';
import { App, Button, Card, Popconfirm, Space, Table, Tag, Typography } from 'antd';
import { RollbackOutlined } from '@ant-design/icons';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import type { AgentVersion } from '@/types';
import { formatDateTime } from '@/utils/format';

interface VersionsPanelProps {
  agentId: string;
  currentVersion: number;
  running: boolean;
  onRolledBack: () => void;
}

// 版本历史 + 回滚
export function VersionsPanel({ agentId, currentVersion, running, onRolledBack }: VersionsPanelProps) {
  const { message } = App.useApp();
  const [versions, setVersions] = useState<AgentVersion[]>([]);
  const [loading, setLoading] = useState(false);
  const [rollingBack, setRollingBack] = useState<number | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await agentApi.listVersions(agentId);
      setVersions(res.data?.items ?? []);
    } catch (err) {
      message.error(getErrorMessage(err, '加载版本历史失败'));
    } finally {
      setLoading(false);
    }
  }, [agentId, message]);

  useEffect(() => {
    load();
  }, [load]);

  const onRollback = async (version: number) => {
    setRollingBack(version);
    try {
      await agentApi.rollback(agentId, version);
      message.success(`已回滚到 v${version}（当前为 v${currentVersion + 1}）`);
      await load();
      onRolledBack();
    } catch (err) {
      message.error(getErrorMessage(err));
    } finally {
      setRollingBack(null);
    }
  };

  return (
    <Card size="small" title="版本历史" extra={running && <Tag color="orange">运行中禁止回滚</Tag>}>
      <Table<AgentVersion>
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={versions}
        pagination={false}
        columns={[
          {
            title: '版本',
            dataIndex: 'version',
            width: 80,
            render: (v: number) => (
              <Space>
                <span>v{v}</span>
                {v === currentVersion && <Tag color="blue">当前</Tag>}
              </Space>
            ),
          },
          {
            title: '模型',
            dataIndex: ['config', 'model'],
            width: 140,
            render: (m?: string) => m || '-',
          },
          {
            title: '描述',
            dataIndex: 'description',
            ellipsis: true,
            render: (d: string) => d || <Typography.Text type="secondary">-</Typography.Text>,
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
            width: 100,
            render: (_: unknown, record: AgentVersion) =>
              record.version === currentVersion ? null : (
                <Popconfirm
                  title={`回滚到 v${record.version}？`}
                  description="将恢复该版本的配置，并产生新版本号"
                  onConfirm={() => onRollback(record.version)}
                  okText="回滚"
                >
                  <Button size="small" icon={<RollbackOutlined />} loading={rollingBack === record.version} disabled={running}>
                    回滚
                  </Button>
                </Popconfirm>
              ),
          },
        ]}
      />
    </Card>
  );
}