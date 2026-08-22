import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { App, Button, Popconfirm, Select, Space, Table, Tag, Tooltip, Typography } from 'antd';
import { EditOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { agentApi } from '@/api/agent';
import { skillApi } from '@/api/skill';
import { getErrorMessage } from '@/api/client';
import { SKILL_STATUS_MAP, SKILL_USAGE_MODE_MAP } from '@/utils/constants';
import type { BoundSkillView, Skill, SkillsUsageMode } from '@/types';

// Agent 详情页 "关联技能" 页签 (M9): 展示绑定 + required_tools 覆盖状态, 支持增删关联
export function SkillsPanel({ agentId, usageMode }: { agentId: string; usageMode?: string }) {
  const { message } = App.useApp();
  const [items, setItems] = useState<BoundSkillView[]>([]);
  const [allSkills, setAllSkills] = useState<Skill[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      const [boundRes, listRes] = await Promise.all([
        agentApi.listBoundSkills(agentId),
        skillApi.list({ size: 100 }),
      ]);
      setItems(boundRes.data?.skills ?? []);
      setAllSkills(listRes.data?.items ?? []);
    } catch (err) {
      message.error(getErrorMessage(err, '加载技能关联失败'));
    } finally {
      setLoading(false);
    }
  }, [agentId, message]);

  useEffect(() => {
    load();
  }, [load]);

  const skillOptions = allSkills.map((s) => ({
    value: s.id,
    label:
      s.status === 'disabled' ? `${s.name} (v${s.version}, 已禁用)` : `${s.name} (v${s.version})`,
  }));

  const startEdit = () => {
    setSelected(items.map((s) => s.id));
    setEditing(true);
  };

  const save = async () => {
    setSaving(true);
    try {
      await agentApi.updateSkills(agentId, selected);
      message.success('技能关联已更新');
      setEditing(false);
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '更新失败'));
    } finally {
      setSaving(false);
    }
  };

  const unbind = async (skillId: string) => {
    const rest = items.filter((s) => s.id !== skillId).map((s) => s.id);
    try {
      await agentApi.updateSkills(agentId, rest);
      message.success('已解绑');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '解绑失败'));
    }
  };

  const columns: ColumnsType<BoundSkillView> = [
    {
      title: '名称',
      dataIndex: 'name',
      width: 200,
      render: (v: string, record) => (
        <Link to={`/skills/${record.id}`}>{v}</Link>
      ),
    },
    {
      title: '版本',
      dataIndex: 'version',
      width: 80,
      render: (v: number) => `v${v}`,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (s: BoundSkillView['status']) => (
        <Tag color={SKILL_STATUS_MAP[s]?.color}>{SKILL_STATUS_MAP[s]?.label ?? s}</Tag>
      ),
    },
    {
      title: '描述',
      dataIndex: 'description',
      ellipsis: true,
      render: (v: string) => (
        <Tooltip title={v}>
          <span>{v || '-'}</span>
        </Tooltip>
      ),
    },
    {
      title: '依赖工具',
      dataIndex: 'required_tools',
      width: 240,
      render: (tools: string[], record) => {
        if (!tools?.length) return <span style={{ color: 'var(--color-text-secondary)' }}>-</span>;
        return (
          <Space size={4} wrap>
            {tools.map((t) => {
              const missing = record.missing_tools?.includes(t);
              return (
                <Tooltip
                  key={t}
                  title={missing ? '未被当前可用工具集 (MCP 绑定 ∩ 工具白名单) 覆盖' : '已覆盖'}
                >
                  <Tag color={missing ? 'red' : 'default'} style={{ fontFamily: 'monospace', fontSize: 11 }}>
                    {t}
                  </Tag>
                </Tooltip>
              );
            })}
          </Space>
        );
      },
    },
    {
      title: '操作',
      key: 'actions',
      width: 90,
      render: (_, record) => (
        <Popconfirm title="解除该技能关联?" onConfirm={() => unbind(record.id)} okText="解绑" cancelText="取消">
          <Button size="small" danger>
            解绑
          </Button>
        </Popconfirm>
      ),
    },
  ];

  const modeLabel = SKILL_USAGE_MODE_MAP[(usageMode || 'metadata_injection') as SkillsUsageMode];

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 12,
          gap: 12,
          flexWrap: 'wrap',
        }}
      >
        <Typography.Text type="secondary">
          注入模式: <Tag>{modeLabel?.label ?? usageMode}</Tag>
          <span style={{ fontSize: 12 }}>{modeLabel?.hint}</span>
        </Typography.Text>
        {!editing ? (
          <Button icon={<EditOutlined />} onClick={startEdit} disabled={allSkills.length === 0 && items.length === 0}>
            编辑关联
          </Button>
        ) : (
          <Space>
            <Select
              mode="multiple"
              allowClear
              placeholder="选择要关联的技能"
              style={{ minWidth: 320 }}
              options={skillOptions}
              value={selected}
              onChange={setSelected}
            />
            <Button type="primary" loading={saving} onClick={save}>
              保存
            </Button>
            <Button onClick={() => setEditing(false)}>取消</Button>
          </Space>
        )}
      </div>
      <Table
        rowKey="id"
        size="middle"
        loading={loading}
        columns={columns}
        dataSource={items}
        pagination={false}
        locale={{ emptyText: '尚未关联技能 (点击 "编辑关联" 添加)' }}
      />
    </div>
  );
}