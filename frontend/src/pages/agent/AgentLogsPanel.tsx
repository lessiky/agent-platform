import { useCallback, useEffect, useRef, useState } from 'react';
import { App, Button, Card, Input, Select, Space, Switch, Table } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import { LogLevelTag } from '@/components/common/StatusTag';
import type { AgentLog, LogLevel } from '@/types';
import { LOG_LEVEL_MAP } from '@/utils/constants';
import { formatDateTime } from '@/utils/format';

const AUTO_REFRESH_INTERVAL = 3000;

interface AgentLogsPanelProps {
  agentId: string;
}

// Agent 运行日志: 实时滚动 + 关键词/级别过滤
export function AgentLogsPanel({ agentId }: AgentLogsPanelProps) {
  const { message } = App.useApp();
  const [logs, setLogs] = useState<AgentLog[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(100);
  const [keyword, setKeyword] = useState('');
  const [level, setLevel] = useState<LogLevel | undefined>();
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [loading, setLoading] = useState(false);
  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await agentApi.getLogs(agentId, { keyword: keyword || undefined, level, page, size });
      setLogs(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载日志失败'));
    } finally {
      setLoading(false);
    }
  }, [agentId, keyword, level, page, size, message]);

  useEffect(() => {
    load();
  }, [load]);

  // 实时滚动: 自动刷新时始终看最新页
  useEffect(() => {
    if (!autoRefresh) return;
    const timer = setInterval(() => {
      setPage(1);
      load();
    }, AUTO_REFRESH_INTERVAL);
    return () => clearInterval(timer);
  }, [autoRefresh, load]);

  const onKeywordChange = (value: string) => {
    setKeyword(value);
    setPage(1);
    if (searchTimer.current) clearTimeout(searchTimer.current);
    searchTimer.current = setTimeout(load, 300);
  };

  return (
    <Card
      size="small"
      title="运行日志"
      extra={
        <Space>
          <Input.Search
            placeholder="关键词搜索"
            allowClear
            style={{ width: 220 }}
            onSearch={onKeywordChange}
            onChange={(e) => onKeywordChange(e.target.value)}
          />
          <Select
            placeholder="级别"
            allowClear
            style={{ width: 110 }}
            value={level}
            onChange={(v) => {
              setLevel(v);
              setPage(1);
            }}
            options={Object.entries(LOG_LEVEL_MAP).map(([value, item]) => ({ value, label: item.label }))}
          />
          <Switch checkedChildren="自动" unCheckedChildren="暂停" checked={autoRefresh} onChange={setAutoRefresh} />
          <Button icon={<ReloadOutlined />} onClick={load} />
        </Space>
      }
    >
      <Table<AgentLog>
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={logs}
        onChange={(pagination) => {
          setPage(pagination.current ?? 1);
          setSize(pagination.pageSize ?? 100);
        }}
        pagination={{
          current: page,
          pageSize: size,
          total,
          showSizeChanger: true,
          showTotal: (t) => `共 ${t} 条`,
        }}
        columns={[
          {
            title: '时间',
            dataIndex: 'created_at',
            width: 170,
            render: (v: string) => formatDateTime(v),
          },
          {
            title: '级别',
            dataIndex: 'level',
            width: 90,
            render: (v: LogLevel) => <LogLevelTag level={v} />,
          },
          {
            title: '内容',
            dataIndex: 'message',
            render: (v: string) => <span className="mono-text">{v}</span>,
          },
        ]}
      />
    </Card>
  );
}