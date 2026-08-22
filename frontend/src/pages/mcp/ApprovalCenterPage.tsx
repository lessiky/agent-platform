import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  App,
  Button,
  Card,
  Descriptions,
  Drawer,
  Input,
  Modal,
  Radio,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { CheckOutlined, CloseOutlined, ReloadOutlined, SettingOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { approvalApi } from '@/api/approval';
import { getErrorMessage } from '@/api/client';
import { APPROVAL_SOURCE_MAP, APPROVAL_STATUS_MAP } from '@/utils/constants';
import { formatDateTime, timeAgo } from '@/utils/format';
import type { ApprovalView } from '@/types';

const PENDING_REFRESH = 5000; // 待审核 5s 轮询 (PRD 2.2.4)
const HISTORY_REFRESH = 15000; // 历史 15s 轮询

function StatusTag({ status }: { status: ApprovalView['status'] }) {
  const meta = APPROVAL_STATUS_MAP[status] ?? { label: status, color: 'default' };
  return <Tag color={meta.color}>{meta.label}</Tag>;
}

function SourceTag({ source }: { source: ApprovalView['source'] }) {
  const meta = APPROVAL_SOURCE_MAP[source] ?? { label: source, color: 'default' };
  return <Tag color={meta.color}>{meta.label}</Tag>;
}

export function ApprovalCenterPage() {
  const { message } = App.useApp();

  const [activeTab, setActiveTab] = useState<'pending' | 'history'>('pending');
  const [items, setItems] = useState<ApprovalView[]>([]);
  const [pendingTotal, setPendingTotal] = useState(0);
  const [historyTotal, setHistoryTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [loading, setLoading] = useState(true);

  // 详情抽屉
  const [detail, setDetail] = useState<ApprovalView | null>(null);
  // 通过/驳回弹窗
  const [decision, setDecision] = useState<{ mode: 'approve' | 'reject'; item: ApprovalView } | null>(null);
  const [decisionComment, setDecisionComment] = useState('');
  const [deciding, setDeciding] = useState(false);
  // 全局配置弹窗
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settings, setSettings] = useState<{ default_timeout_minutes: number; on_timeout: 'reject' | 'approve' } | null>(null);

  const loadPendingTotal = useCallback(async () => {
    try {
      const res = await approvalApi.list({ status: 'pending', page: 1, size: 1 });
      setPendingTotal(res.data?.total ?? 0);
    } catch {
      // 角标刷新失败时保留上次数值
    }
  }, []);

  const load = useCallback(async () => {
    try {
      const res = await approvalApi.list({
        status: activeTab === 'pending' ? 'pending' : undefined,
        page,
        size,
      });
      setItems(res.data?.items ?? []);
      if (activeTab === 'pending') {
        setPendingTotal(res.data?.total ?? 0);
      } else {
        setHistoryTotal(res.data?.total ?? 0);
      }
    } catch (err) {
      message.error(getErrorMessage(err, '加载审核请求失败'));
    } finally {
      setLoading(false);
    }
  }, [activeTab, page, size, message]);

  useEffect(() => {
    setLoading(true);
    load();
    if (activeTab === 'history') loadPendingTotal();
    const interval = activeTab === 'pending' ? PENDING_REFRESH : HISTORY_REFRESH;
    const timer = setInterval(() => {
      load();
      if (activeTab === 'history') loadPendingTotal();
    }, interval);
    return () => clearInterval(timer);
  }, [load, loadPendingTotal]);

  const openSettings = async () => {
    try {
      const res = await approvalApi.getSettings();
      setSettings({
        default_timeout_minutes: res.data?.default_timeout_minutes ?? 30,
        on_timeout: res.data?.on_timeout ?? 'reject',
      });
    } catch (err) {
      message.error(getErrorMessage(err, '加载审核配置失败'));
      return;
    }
    setSettingsOpen(true);
  };

  const saveSettings = async () => {
    if (!settings) return;
    try {
      await approvalApi.updateSettings(settings);
      message.success('审核配置已更新');
      setSettingsOpen(false);
    } catch (err) {
      message.error(getErrorMessage(err, '更新审核配置失败'));
    }
  };

  const submitDecision = async () => {
    if (!decision) return;
    setDeciding(true);
    try {
      if (decision.mode === 'approve') {
        await approvalApi.approve(decision.item.id, decisionComment.trim() || undefined);
        message.success('已通过, 工具执行中');
      } else {
        await approvalApi.reject(decision.item.id, decisionComment.trim() || undefined);
        message.success('已驳回');
      }
      setDecision(null);
      setDecisionComment('');
      load();
    } catch (err) {
      message.error(getErrorMessage(err, '操作失败'));
    } finally {
      setDeciding(false);
    }
  };

  const columns: ColumnsType<ApprovalView> = [
    {
      title: '工具',
      key: 'tool',
      width: 280,
      render: (_, item) => (
        <Space direction="vertical" size={0}>
          <Typography.Text code>{item.tool_name}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {item.mcp_name || item.mcp_server_id}
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: 'Agent',
      dataIndex: 'agent_name',
      width: 140,
      render: (v: string, item) =>
        v ? <Link to={`/agents/${item.agent_id}`}>{v}</Link> : <Typography.Text type="secondary">-</Typography.Text>,
    },
    {
      title: '来源',
      dataIndex: 'source',
      width: 100,
      render: (v: ApprovalView['source']) => <SourceTag source={v} />,
    },
    {
      title: '请求时间',
      dataIndex: 'requested_at',
      width: 170,
      render: (v: string) => formatDateTime(v),
    },
    {
      title: '超时时间',
      dataIndex: 'expires_at',
      width: 190,
      render: (v: string, item) => {
        if (item.status !== 'pending') return formatDateTime(v);
        const remainMs = new Date(v).getTime() - Date.now();
        const urgent = remainMs < 5 * 60 * 1000;
        return (
          <Tooltip title={urgent ? '即将超时' : '等待审核中'}>
            <span style={{ color: urgent ? '#ff4d4f' : undefined }}>
              {formatDateTime(v)}
            </span>
          </Tooltip>
        );
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: ApprovalView['status']) => <StatusTag status={v} />,
    },
    {
      title: '操作',
      key: 'actions',
      width: 190,
      render: (_, item) =>
        item.status === 'pending' ? (
          <Space size={4}>
            <Button size="small" type="primary" icon={<CheckOutlined />} onClick={() => setDecision({ mode: 'approve', item })}>
              通过
            </Button>
            <Button size="small" danger icon={<CloseOutlined />} onClick={() => setDecision({ mode: 'reject', item })}>
              驳回
            </Button>
          </Space>
        ) : (
          <Button size="small" onClick={() => setDetail(item)}>
            详情
          </Button>
        ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Space size={12} align="center">
          <h2 style={{ margin: 0 }}>审核中心</h2>
          <Typography.Text type="secondary">MCP 工具调用需人工审核通过后才会执行</Typography.Text>
        </Space>
        <Space>
          <Button icon={<SettingOutlined />} onClick={openSettings}>
            审核配置
          </Button>
          <Button icon={<ReloadOutlined />} onClick={load}>
            刷新
          </Button>
        </Space>
      </div>

      <Card>
        <Tabs
          activeKey={activeTab}
          onChange={(key) => {
            setActiveTab(key as 'pending' | 'history');
            setPage(1);
          }}
          items={[
            {
              key: 'pending',
              label: `待审核 (${pendingTotal})`,
              children: (
                <Table
                  rowKey="id"
                  size="middle"
                  loading={loading}
                  columns={columns}
                  dataSource={items}
                  pagination={{
                    current: page,
                    pageSize: size,
                    total: pendingTotal,
                    showSizeChanger: true,
                    showTotal: (t) => `共 ${t} 条`,
                    onChange: (p, s) => {
                      setPage(p);
                      setSize(s);
                    },
                  }}
                  locale={{ emptyText: '暂无待审核请求' }}
                  onRow={(item) => ({
                    onDoubleClick: () => item.status === 'pending' && setDecision({ mode: 'approve', item }),
                  })}
                />
              ),
            },
            {
              key: 'history',
              label: '审核历史',
              children: (
                <Table
                  rowKey="id"
                  size="middle"
                  loading={loading}
                  columns={columns}
                  dataSource={items}
                  pagination={{
                    current: page,
                    pageSize: size,
                    total: historyTotal,
                    showSizeChanger: true,
                    showTotal: (t) => `共 ${t} 条`,
                    onChange: (p, s) => {
                      setPage(p);
                      setSize(s);
                    },
                  }}
                  locale={{ emptyText: '暂无审核记录' }}
                />
              ),
            },
          ]}
        />
      </Card>

      {/* 详情抽屉 */}
      <Drawer
        title={detail ? `审核详情 - ${detail.tool_name}` : '审核详情'}
        width={640}
        open={detail !== null}
        onClose={() => setDetail(null)}
      >
        {detail && (
          <Space direction="vertical" style={{ width: '100%' }} size={16}>
            <Descriptions
              column={2}
              size="small"
              items={[
                { key: 'mcp', label: 'MCP 服务器', children: detail.mcp_name || detail.mcp_server_id },
                { key: 'tool', label: '工具', children: <Typography.Text code>{detail.tool_name}</Typography.Text> },
                {
                  key: 'agent',
                  label: 'Agent',
                  children: detail.agent_name ? <Link to={`/agents/${detail.agent_id}`}>{detail.agent_name}</Link> : '-',
                },
                { key: 'source', label: '来源', children: <SourceTag source={detail.source} /> },
                { key: 'status', label: '状态', children: <StatusTag status={detail.status} /> },
                {
                  key: 'executed',
                  label: '执行状态',
                  children: detail.executed_at ? `已于 ${formatDateTime(detail.executed_at)} 执行` : '未执行',
                },
                { key: 'requested', label: '请求时间', children: formatDateTime(detail.requested_at) },
                { key: 'expires', label: '超时时间', children: formatDateTime(detail.expires_at) },
                { key: 'decided_by', label: '审核人', children: detail.decided_by || '系统' },
                { key: 'decided_at', label: '审核时间', children: detail.decided_at ? formatDateTime(detail.decided_at) : '-' },
                {
                  key: 'comment',
                  label: '审核意见',
                  span: 2,
                  children: detail.comment || '-',
                },
              ]}
            />
            <div>
              <Typography.Title level={5} style={{ marginBottom: 8 }}>
                调用参数 (审核时点快照)
              </Typography.Title>
              <Input.TextArea
                readOnly
                autoSize={{ minRows: 2, maxRows: 10 }}
                style={{ fontFamily: 'monospace', fontSize: 12 }}
                value={JSON.stringify(detail.arguments, null, 2)}
              />
            </div>
            <div>
              <Typography.Title level={5} style={{ marginBottom: 8 }}>
                执行结果
              </Typography.Title>
              {detail.result ? (
                <Input.TextArea
                  readOnly
                  autoSize={{ minRows: 2, maxRows: 10 }}
                  style={{ fontFamily: 'monospace', fontSize: 12 }}
                  value={JSON.stringify(detail.result, null, 2)}
                />
              ) : (
                <Typography.Text type="secondary">暂无 (未执行或执行中)</Typography.Text>
              )}
            </div>
          </Space>
        )}
      </Drawer>

      {/* 通过/驳回弹窗 */}
      <Modal
        title={decision?.mode === 'approve' ? '通过审核' : '驳回审核'}
        open={decision !== null}
        onOk={submitDecision}
        okText={decision?.mode === 'approve' ? '通过并执行' : '确认驳回'}
        okButtonProps={{ danger: decision?.mode === 'reject' }}
        cancelText="取消"
        confirmLoading={deciding}
        onCancel={() => {
          setDecision(null);
          setDecisionComment('');
        }}
      >
        {decision && (
          <Space direction="vertical" style={{ width: '100%' }} size={12}>
            <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
              工具 <Typography.Text code>{decision.item.tool_name}</Typography.Text> (
              {decision.item.mcp_name})
              {decision.item.agent_name ? `, Agent: ${decision.item.agent_name}` : ''}
              , 请求于 {timeAgo(decision.item.requested_at)}
            </Typography.Paragraph>
            {decision.item.arguments && Object.keys(decision.item.arguments).length > 0 && (
              <Input.TextArea
                readOnly
                autoSize={{ minRows: 1, maxRows: 6 }}
                style={{ fontFamily: 'monospace', fontSize: 12, background: '#fafafa' }}
                value={JSON.stringify(decision.item.arguments, null, 2)}
              />
            )}
            <Input.TextArea
              placeholder={decision.mode === 'approve' ? '审核意见 (可选)' : '驳回原因 (建议填写)'}
              autoSize={{ minRows: 2, maxRows: 5 }}
              value={decisionComment}
              onChange={(e) => setDecisionComment(e.target.value)}
              maxLength={512}
            />
          </Space>
        )}
      </Modal>

      {/* 审核全局配置弹窗 */}
      <Modal
        title="审核全局配置"
        open={settingsOpen}
        onOk={saveSettings}
        okText="保存"
        cancelText="取消"
        onCancel={() => setSettingsOpen(false)}
      >
        {settings && (
          <Space direction="vertical" style={{ width: '100%' }} size={16}>
            <div>
              <Typography.Text>超时时间 (分钟)</Typography.Text>
              <Input
                type="number"
                min={1}
                max={1440}
                value={settings.default_timeout_minutes}
                onChange={(e) => setSettings({ ...settings, default_timeout_minutes: Number(e.target.value) || 30 })}
                style={{ marginTop: 8 }}
              />
              <Typography.Text type="secondary">待审核请求超过该时间未处理将自动按超时策略执行</Typography.Text>
            </div>
            <div>
              <Typography.Text>超时策略</Typography.Text>
              <Radio.Group
                style={{ marginTop: 8, display: 'block' }}
                value={settings.on_timeout}
                onChange={(e) => setSettings({ ...settings, on_timeout: e.target.value })}
              >
                <Radio value="reject">超时拒绝 (默认, 终止本次调用)</Radio>
                <br />
                <Radio value="approve">超时自动通过 (自动执行工具)</Radio>
              </Radio.Group>
            </div>
          </Space>
        )}
      </Modal>
    </div>
  );
}