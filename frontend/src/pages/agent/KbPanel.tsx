import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Alert, Button, Card, Empty, Input, InputNumber, Space, Spin, Table, Tag, Typography } from 'antd';
import { SearchOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import { MarkdownExcerpt } from '@/components/common/MarkdownExcerpt';
import { KB_SEARCH_MODE_MAP } from '@/utils/constants';
import type { AgentKBCategoryView, AgentKBView, KBSearchHit, KBSearchMode } from '@/types';

// Agent 详情页 "知识库" 页签 (M11 W2): 绑定分类 + 检索模式 + 检索试算 (服务端按当前绑定重新鉴权)
export function KbPanel({ agentId }: { agentId: string }) {
  const [view, setView] = useState<AgentKBView | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');

  const [query, setQuery] = useState('');
  const [topK, setTopK] = useState(3);
  const [hits, setHits] = useState<KBSearchHit[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState('');

  const load = useCallback(async () => {
    try {
      const res = await agentApi.listKB(agentId);
      setView(res.data ?? null);
      setLoadError('');
    } catch (err) {
      setLoadError(getErrorMessage(err, '加载知识库绑定失败'));
    } finally {
      setLoading(false);
    }
  }, [agentId]);

  useEffect(() => {
    load();
  }, [load]);

  const onSearch = async () => {
    if (!query.trim()) {
      setHits([]);
      setSearchError('');
      return;
    }
    setSearching(true);
    try {
      const res = await agentApi.searchKB(agentId, { query: query.trim(), top_k: topK });
      setHits(res.data?.hits ?? []);
      setSearchError('');
    } catch (err) {
      setHits(null);
      setSearchError(getErrorMessage(err, '检索试算失败'));
    } finally {
      setSearching(false);
    }
  };

  if (loading) {
    return (
      <div style={{ textAlign: 'center', padding: 60 }}>
        <Spin />
      </div>
    );
  }

  const mode = (view?.kb_search_mode ?? 'auto') as KBSearchMode;
  const modeInfo = KB_SEARCH_MODE_MAP[mode] ?? KB_SEARCH_MODE_MAP.auto;
  const categories = view?.categories ?? [];

  const columns: ColumnsType<AgentKBCategoryView> = [
    {
      title: '分类',
      dataIndex: 'name',
      key: 'name',
      // M11.5: 子级显示 父/子 路径
      render: (_: string, row: AgentKBCategoryView) =>
        row.parent_name ? `${row.parent_name}/${row.name}` : row.name,
    },
    {
      title: '权限',
      dataIndex: 'read_only',
      key: 'read_only',
      width: 90,
      render: (ro: boolean) => (ro ? <Tag color="orange">只读</Tag> : <Tag color="green">读写</Tag>),
    },
    { title: '描述', dataIndex: 'description', key: 'description', ellipsis: true },
    { title: '条目数', dataIndex: 'document_count', key: 'document_count', width: 100, align: 'right' },
  ];

  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Card size="small" title="绑定分类">
        {loadError ? (
          <Alert type="error" showIcon message={loadError} action={<Button size="small" onClick={load}>重试</Button>} />
        ) : (
          <Table
            rowKey="id"
            size="small"
            columns={columns}
            dataSource={categories}
            pagination={false}
            locale={{
              emptyText: (
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="未绑定知识库分类">
                  <Link to={`/agents/${agentId}/edit`}>去编辑绑定</Link>
                </Empty>
              ),
            }}
          />
        )}
      </Card>

      <Card size="small" title="检索模式">
        <Space>
          <Tag color="blue">{modeInfo.label}</Tag>
          <Typography.Text type="secondary">{modeInfo.hint}</Typography.Text>
        </Space>
      </Card>

      <Card size="small" title="检索试算" extra={<Typography.Text type="secondary">仅检索绑定分类 (服务端重新鉴权)</Typography.Text>}>
        <Space direction="vertical" size="small" style={{ width: '100%' }}>
          <Space.Compact style={{ width: '100%' }}>
            <Input
              placeholder="输入检索问题, 如: 数据库备份失败怎么处理"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onPressEnter={onSearch}
              allowClear
            />
            <InputNumber
              min={1}
              max={20}
              value={topK}
              onChange={(v) => setTopK(v ?? 3)}
              style={{ width: 96 }}
              addonBefore="Top"
            />
            <Button type="primary" icon={<SearchOutlined />} loading={searching} onClick={onSearch}>
              试算
            </Button>
          </Space.Compact>
          {searchError && <Alert type="error" showIcon message={searchError} />}
          {hits !== null && hits.length === 0 && (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="未检索到匹配的知识条目" />
          )}
          {hits && hits.length > 0 && (
            <Space direction="vertical" size="small" style={{ width: '100%' }}>
              {hits.map((h) => (
                <Card key={h.id} size="small">
                  <Space direction="vertical" size={4} style={{ width: '100%' }}>
                    <Space>
                      <Typography.Text strong>{h.title}</Typography.Text>
                      <Tag>{h.category_name}</Tag>
                      <Typography.Text type="secondary">相关度 {h.score.toFixed(2)}</Typography.Text>
                    </Space>
                    <div style={{ color: 'rgba(0, 0, 0, 0.45)' }}>
                      <MarkdownExcerpt text={h.excerpt} />
                    </div>
                    {h.matched_chunk && (
                      <div>
                        <Tag color="blue" style={{ marginBottom: 4 }}>
                          命中块
                        </Tag>
                        <Typography.Text
                          type="secondary"
                          style={{ fontSize: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}
                        >
                          {h.matched_chunk}
                        </Typography.Text>
                      </div>
                    )}
                  </Space>
                </Card>
              ))}
            </Space>
          )}
        </Space>
      </Card>
    </Space>
  );
}
