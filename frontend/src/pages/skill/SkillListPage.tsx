import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  App,
  Button,
  Card,
  Checkbox,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Upload,
} from 'antd';
import { InboxOutlined, PlusOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { UploadFile } from 'antd';
import { skillApi } from '@/api/skill';
import { getErrorMessage } from '@/api/client';
import { SKILL_STATUS_MAP } from '@/utils/constants';
import { formatDateTime, formatNumber } from '@/utils/format';
import type { Skill, SkillStatus } from '@/types';

const STATUS_OPTIONS = (Object.keys(SKILL_STATUS_MAP) as SkillStatus[]).map((key) => ({
  value: key,
  label: SKILL_STATUS_MAP[key].label,
}));

// 字节数展示
function formatBytes(v?: number): string {
  if (!v && v !== 0) return '-';
  if (v < 1024) return `${v} B`;
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`;
  return `${(v / 1024 / 1024).toFixed(1)} MB`;
}

export function SkillListPage() {
  const navigate = useNavigate();
  const { message } = App.useApp();
  const [skills, setSkills] = useState<Skill[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [loading, setLoading] = useState(true);
  const [keyword, setKeyword] = useState('');
  const [debouncedKeyword, setDebouncedKeyword] = useState('');
  const [status, setStatus] = useState<SkillStatus | undefined>(undefined);
  const [importOpen, setImportOpen] = useState(false);
  const [importFile, setImportFile] = useState<UploadFile | null>(null);
  const [force, setForce] = useState(false);
  const [importing, setImporting] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => {
    timerRef.current = setTimeout(() => setDebouncedKeyword(keyword.trim()), 400);
    return () => clearTimeout(timerRef.current);
  }, [keyword]);

  const load = useCallback(async () => {
    try {
      const res = await skillApi.list({ q: debouncedKeyword || undefined, status, page, size });
      setSkills(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载技能列表失败'));
    } finally {
      setLoading(false);
    }
  }, [debouncedKeyword, status, page, size, message]);

  useEffect(() => {
    setLoading(true);
    load();
  }, [load]);

  const doImport = async () => {
    if (!importFile) {
      message.warning('请先选择技能包 zip 文件');
      return;
    }
    setImporting(true);
    try {
      const res = await skillApi.import(importFile.originFileObj as File, force);
      const s = res.data;
      message.success(`导入成功: ${s?.name} v${s?.version}`);
      setImportOpen(false);
      setImportFile(null);
      setForce(false);
      load();
    } catch (err) {
      const msg = getErrorMessage(err);
      if (/已存在/.test(msg) && !force) {
        message.warning(`${msg}; 勾选 "同名覆盖升级" 后重试`);
      } else {
        message.error(msg);
      }
    } finally {
      setImporting(false);
    }
  };

  const onToggleStatus = async (record: Skill) => {
    const next: SkillStatus = record.status === 'active' ? 'disabled' : 'active';
    try {
      await skillApi.updateStatus(record.id, next);
      message.success(next === 'active' ? '已启用' : '已禁用 (关联保留, 运行时不再注入)');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '操作失败'));
    }
  };

  const onDelete = async (record: Skill, forceDelete: boolean) => {
    try {
      await skillApi.remove(record.id, forceDelete);
      message.success('已删除');
      load();
    } catch (err) {
      const data = (err as { response?: { data?: { message?: string; data?: { agents?: { agent_name: string }[] } } } })
        .response?.data;
      if (data?.data?.agents?.length) {
        const names = data.data.agents.map((a) => a.agent_name).join('、');
        Modal.confirm({
          title: '技能已被 Agent 关联',
          content: `以下 Agent 正在使用该技能: ${names}。确定强制删除并解除全部关联?`,
          okText: '强制删除',
          okButtonProps: { danger: true },
          cancelText: '取消',
          onOk: () => onDelete(record, true),
        });
        return;
      }
      message.error(getErrorMessage(err, '删除失败'));
    }
  };

  const columns: ColumnsType<Skill> = [
    {
      title: '名称',
      dataIndex: 'name',
      width: 200,
      render: (name: string, record) => (
        <div>
          <Link to={`/skills/${record.id}`}>{name}</Link>
          <div style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>
            v{record.version}
            {record.version_spec ? ` (${record.version_spec})` : ''}
            {record.author ? ` · ${record.author}` : ''}
          </div>
        </div>
      ),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (s: SkillStatus) => (
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
      width: 170,
      render: (tools: string[]) =>
        tools?.length ? (
          tools.map((t) => (
            <Tag key={t} style={{ fontFamily: 'monospace', fontSize: 11 }}>
              {t}
            </Tag>
          ))
        ) : (
          <span style={{ color: 'var(--color-text-secondary)' }}>-</span>
        ),
    },
    {
      title: '关联 Agent',
      dataIndex: 'agent_count',
      width: 100,
      render: (v: number, record) => (
        <Space size={4}>
          <span>{formatNumber(v ?? 0)}</span>
          {record.in_use && <Tag color="green">使用中</Tag>}
        </Space>
      ),
    },
    {
      title: '大小 / 文件',
      width: 120,
      render: (_, record) => `${formatBytes(record.size_bytes)} / ${record.file_count} 个`,
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
      width: 210,
      render: (_, record) => (
        <Space size="small">
          <Button size="small" onClick={() => navigate(`/skills/${record.id}`)}>
            详情
          </Button>
          <Button size="small" onClick={() => onToggleStatus(record)}>
            {record.status === 'active' ? '禁用' : '启用'}
          </Button>
          <Popconfirm
            title={`确定删除技能 ${record.name}?`}
            description="有关联 Agent 时会先拦截, 需二次确认强制解绑"
            onConfirm={() => onDelete(record, false)}
            okText="删除"
            cancelText="取消"
          >
            <Button size="small" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>技能管理</h2>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setImportOpen(true)}>
            导入技能包
          </Button>
        </Space>
      </div>
      <Card>
        <div style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder="搜索名称 / 描述"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            style={{ width: 280 }}
          />
          <Select
            allowClear
            placeholder="状态过滤"
            options={STATUS_OPTIONS}
            value={status}
            onChange={setStatus}
            style={{ width: 140 }}
          />
        </div>
        <Table
          rowKey="id"
          size="middle"
          loading={loading}
          columns={columns}
          dataSource={skills}
          scroll={{ x: 1150 }}
          pagination={{
            current: page,
            pageSize: size,
            total,
            showSizeChanger: true,
            showTotal: (t) => `共 ${t} 条`,
            onChange: (p, s) => {
              setPage(p);
              setSize(s);
            },
          }}
        />
      </Card>

      <Modal
        title="导入技能包"
        open={importOpen}
        onOk={doImport}
        onCancel={() => {
          setImportOpen(false);
          setImportFile(null);
          setForce(false);
        }}
        okText="导入"
        confirmLoading={importing}
        destroyOnClose
      >
        <p style={{ marginTop: 0 }}>
          上传 zip 格式技能包 (须含 SKILL.md, 支持包根目录或单一顶层目录结构; 限制: 单包 ≤10MB / ≤500 文件 / 单文件 ≤2MB)。
          平台仅存储并只读注入, 不执行包内代码。
        </p>
        <Upload.Dragger
          accept=".zip"
          maxCount={1}
          beforeUpload={() => false}
          fileList={importFile ? [importFile] : []}
          onChange={({ fileList }) => setImportFile(fileList[0] ?? null)}
        >
          <p className="ant-upload-drag-icon">
            <InboxOutlined />
          </p>
          <p className="ant-upload-text">点击或拖拽 zip 文件到此区域</p>
          <p className="ant-upload-hint">示例包: backend/testdata/skills/weekly-report.zip</p>
        </Upload.Dragger>
        <div style={{ marginTop: 12 }}>
          <Checkbox checked={force} onChange={(e) => setForce(e.target.checked)}>
            同名覆盖升级 (版本号 +1, 已有关联保留)
          </Checkbox>
        </div>
      </Modal>
    </div>
  );
}