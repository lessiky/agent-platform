import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  App,
  Button,
  Card,
  Descriptions,
  Modal,
  Popconfirm,
  Result,
  Space,
  Spin,
  Statistic,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { ArrowLeftOutlined, DeleteOutlined, EyeOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { skillApi } from '@/api/skill';
import { getErrorMessage } from '@/api/client';
import { SKILL_STATUS_MAP } from '@/utils/constants';
import { formatDateTime, timeAgo } from '@/utils/format';
import type { Skill, SkillAgentBinding, SkillFileMeta, SkillUsage } from '@/types';

const TEXT_EXT = new Set(['md', 'markdown', 'txt', 'rst', 'json', 'yaml', 'yml', 'toml', 'csv', 'tsv', 'html', 'css', 'py', 'js', 'ts', 'sh', 'bat', 'ps1', 'sql', 'xml']);

function formatBytes(v?: number): string {
  if (v === undefined || v === null) return '-';
  if (v < 1024) return `${v} B`;
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`;
  return `${(v / 1024 / 1024).toFixed(1)} MB`;
}

function fileExt(path: string): string {
  const i = path.lastIndexOf('.');
  return i >= 0 ? path.slice(i + 1).toLowerCase() : '';
}

export function SkillDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [loading, setLoading] = useState(true);
  const [notFound, setNotFound] = useState(false);
  const [skill, setSkill] = useState<Skill | null>(null);
  const [files, setFiles] = useState<SkillFileMeta[]>([]);
  const [agents, setAgents] = useState<SkillAgentBinding[]>([]);
  const [usage, setUsage] = useState<SkillUsage | null>(null);
  const [fileModal, setFileModal] = useState<{ path: string; content: string } | null>(null);

  const load = useCallback(async () => {
    if (!id) return;
    try {
      const [detailRes, agentsRes, usageRes] = await Promise.all([
        skillApi.getById(id),
        skillApi.listAgents(id),
        skillApi.usage(id),
      ]);
      setSkill(detailRes.data?.skill ?? null);
      setFiles(detailRes.data?.files ?? []);
      setAgents(agentsRes.data?.agents ?? []);
      setUsage(usageRes.data ?? null);
    } catch (err) {
      if (getErrorMessage(err).includes('not found') || (err as { response?: { status?: number } }).response?.status === 404) {
        setNotFound(true);
      } else {
        message.error(getErrorMessage(err, '加载技能详情失败'));
      }
    } finally {
      setLoading(false);
    }
  }, [id, message]);

  useEffect(() => {
    load();
  }, [load]);

  const onToggleStatus = async () => {
    if (!skill || !id) return;
    const next = skill.status === 'active' ? 'disabled' : 'active';
    try {
      await skillApi.updateStatus(id, next);
      message.success(next === 'active' ? '已启用' : '已禁用 (关联保留, 运行时不再注入)');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '操作失败'));
    }
  };

  const onDelete = async (forceDelete: boolean) => {
    if (!id) return;
    try {
      await skillApi.remove(id, forceDelete);
      message.success('已删除');
      navigate('/skills');
    } catch (err) {
      const data = (err as { response?: { data?: { data?: { agents?: { agent_name: string }[] } } } }).response?.data;
      if (data?.data?.agents?.length) {
        const names = data.data.agents.map((a) => a.agent_name).join('、');
        Modal.confirm({
          title: '技能已被 Agent 关联',
          content: `以下 Agent 正在使用该技能: ${names}。确定强制删除并解除全部关联?`,
          okText: '强制删除',
          okButtonProps: { danger: true },
          cancelText: '取消',
          onOk: () => onDelete(true),
        });
        return;
      }
      message.error(getErrorMessage(err, '删除失败'));
    }
  };

  const onPreview = async (path: string) => {
    if (!id) return;
    if (!TEXT_EXT.has(fileExt(path))) {
      message.info('二进制文件不支持在线预览');
      return;
    }
    try {
      const blob = await skillApi.getFile(id, path);
      const text = await blob.text();
      setFileModal({ path, content: text });
    } catch (err) {
      message.error(getErrorMessage(err, '读取文件失败'));
    }
  };

  if (loading) {
    return (
      <div style={{ textAlign: 'center', padding: 80 }}>
        <Spin size="large" />
      </div>
    );
  }
  if (notFound || !skill) {
    return <Result status="404" title="技能不存在" extra={<Button onClick={() => navigate('/skills')}>返回列表</Button>} />;
  }

  const fileColumns: ColumnsType<SkillFileMeta> = [
    {
      title: '路径',
      dataIndex: 'path',
      render: (v: string) => <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</span>,
    },
    { title: '大小', dataIndex: 'size', width: 110, render: (v: number) => formatBytes(v) },
    {
      title: 'SHA256',
      dataIndex: 'sha256',
      width: 180,
      render: (v: string) => (
        <Tooltip title={v}>
          <span style={{ fontFamily: 'monospace', fontSize: 11 }}>{v.slice(0, 12)}…</span>
        </Tooltip>
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 90,
      render: (_, record) => (
        <Button size="small" icon={<EyeOutlined />} onClick={() => onPreview(record.path)}>
          预览
        </Button>
      ),
    },
  ];

  const agentColumns: ColumnsType<SkillAgentBinding> = [
    {
      title: 'Agent',
      dataIndex: 'agent_name',
      render: (v: string, record) => <Link to={`/agents/${record.agent_id}`}>{v}</Link>,
    },
    { title: '关联时间', dataIndex: 'bound_at', render: (v: string) => formatDateTime(v) },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <Space size={12}>
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/skills')}>
            返回
          </Button>
          <h2 style={{ margin: 0 }}>{skill.name}</h2>
          <Tag color={SKILL_STATUS_MAP[skill.status]?.color}>{SKILL_STATUS_MAP[skill.status]?.label ?? skill.status}</Tag>
        </Space>
        <Space>
          <Button onClick={onToggleStatus}>{skill.status === 'active' ? '禁用' : '启用'}</Button>
          <Popconfirm title={`确定删除技能 ${skill.name}?`} onConfirm={() => onDelete(false)} okText="删除" cancelText="取消">
            <Button danger icon={<DeleteOutlined />}>
              删除
            </Button>
          </Popconfirm>
        </Space>
      </div>

      <Card size="small" style={{ marginBottom: 16 }}>
        <Space size={48}>
          <Statistic title="关联 Agent" value={usage?.agent_count ?? skill.agent_count ?? 0} />
          <Statistic title="近 30 天加载次数" value={usage?.load_count_30d ?? 0} />
          <Statistic
            title="最近使用"
            value={usage?.last_used_at ? timeAgo(usage.last_used_at) : '暂无'}
          />
        </Space>
      </Card>

      <Card size="small" style={{ marginBottom: 16 }}>
        <Descriptions column={2} size="small">
          <Descriptions.Item label="版本号">
            v{skill.version}
            {skill.version_spec ? ` (${skill.version_spec})` : ''}
          </Descriptions.Item>
          <Descriptions.Item label="作者">{skill.author || '-'}</Descriptions.Item>
          <Descriptions.Item label="标签">
            {skill.tags?.length ? skill.tags.map((t) => <Tag key={t}>{t}</Tag>) : '-'}
          </Descriptions.Item>
          <Descriptions.Item label="依赖工具">
            {skill.required_tools?.length ? (
              skill.required_tools.map((t) => (
                <Tag key={t} style={{ fontFamily: 'monospace', fontSize: 11 }}>
                  {t}
                </Tag>
              ))
            ) : (
              '-'
            )}
          </Descriptions.Item>
          <Descriptions.Item label="包大小 / 文件数">
            {formatBytes(skill.size_bytes)} / {skill.file_count} 个
          </Descriptions.Item>
          <Descriptions.Item label="创建时间">{formatDateTime(skill.created_at)}</Descriptions.Item>
          <Descriptions.Item label="描述" span={2}>
            {skill.description || '-'}
          </Descriptions.Item>
        </Descriptions>
      </Card>

      <Tabs
        items={[
          {
            key: 'content',
            label: '指令正文',
            children: (
              <Card size="small">
                <pre
                  style={{
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word',
                    margin: 0,
                    fontFamily: 'inherit',
                    maxHeight: 600,
                    overflow: 'auto',
                  }}
                >
                  {skill.entry_content}
                </pre>
              </Card>
            ),
          },
          {
            key: 'files',
            label: `资源文件 (${files.length})`,
            children: (
              <Card size="small">
                <Table rowKey="path" size="small" columns={fileColumns} dataSource={files} pagination={false} />
              </Card>
            ),
          },
          {
            key: 'agents',
            label: `关联 Agent (${agents.length})`,
            children: (
              <Card size="small">
                {agents.length === 0 ? (
                  <Typography.Text type="secondary">暂无 Agent 关联</Typography.Text>
                ) : (
                  <Table rowKey="agent_id" size="small" columns={agentColumns} dataSource={agents} pagination={false} />
                )}
              </Card>
            ),
          },
        ]}
      />

      <Modal
        title={`文件预览: ${fileModal?.path ?? ''}`}
        open={fileModal !== null}
        onCancel={() => setFileModal(null)}
        footer={null}
        width={720}
      >
        <pre
          style={{
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            margin: 0,
            fontFamily: 'monospace',
            fontSize: 12,
            maxHeight: 560,
            overflow: 'auto',
          }}
        >
          {fileModal?.content}
        </pre>
      </Modal>
    </div>
  );
}

