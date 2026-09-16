import { useCallback, useEffect, useRef, useState } from 'react';
import {
  App,
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Input,
  Modal,
  Popconfirm,
  Select,
  Segmented,
  Space,
  Spin,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Tree,
  Typography,
} from 'antd';
import {
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  FolderAddOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  StopOutlined,
  UndoOutlined,
} from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { kbApi } from '@/api/kb';
import { getErrorMessage } from '@/api/client';
import { Markdown } from '@/components/common/Markdown';
import { useAuthStore } from '@/store/auth-store';
import { KB_SOURCE_MAP, KB_STATUS_MAP } from '@/utils/constants';
import { formatDateTime } from '@/utils/format';
import type { KBCategory, KBChunkView, KBDocument, KBSource, KBStatus } from '@/types';

const SOURCE_OPTIONS = (Object.keys(KB_SOURCE_MAP) as KBSource[]).map((key) => ({
  value: key,
  label: KB_SOURCE_MAP[key].label,
}));

const STATUS_OPTIONS = (Object.keys(KB_STATUS_MAP) as KBStatus[]).map((key) => ({
  value: key,
  label: KB_STATUS_MAP[key].label,
}));

export function KbListPage() {
  const { message } = App.useApp();
  const permissions = useAuthStore((s) => s.permissions);
  const canWrite = permissions.includes('kb:write');

  // ---- 分类 ----
  const [categories, setCategories] = useState<KBCategory[]>([]);
  const [catLoading, setCatLoading] = useState(true);
  const [selectedCat, setSelectedCat] = useState<string>('');
  const [catModalOpen, setCatModalOpen] = useState(false);
  const [catEditing, setCatEditing] = useState<KBCategory | null>(null);
  const [catName, setCatName] = useState('');
  const [catDesc, setCatDesc] = useState('');
  const [catParent, setCatParent] = useState<string | undefined>(undefined); // M11.5: 父级 (顶级分类 id; undefined = 顶级)
  const [catSaving, setCatSaving] = useState(false);
  const [treeExpanded, setTreeExpanded] = useState<string[]>([]);

  // ---- 条目 ----
  const [docs, setDocs] = useState<KBDocument[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [docLoading, setDocLoading] = useState(true);
  const [keyword, setKeyword] = useState('');
  const [debouncedKeyword, setDebouncedKeyword] = useState('');
  const [source, setSource] = useState<KBSource | undefined>(undefined);
  const [status, setStatus] = useState<KBStatus | undefined>(undefined);
  const timerRef = useRef<ReturnType<typeof setTimeout>>();

  // 条目编辑 / 查看
  const [docModalOpen, setDocModalOpen] = useState(false);
  const [docEditing, setDocEditing] = useState<KBDocument | null>(null);
  const [docTitle, setDocTitle] = useState('');
  const [docContent, setDocContent] = useState('');
  const [docCategory, setDocCategory] = useState<string | undefined>(undefined);
  const [docSaving, setDocSaving] = useState(false);
  const [viewDoc, setViewDoc] = useState<KBDocument | null>(null);
  // M11.5 事项 4: 条目分块预览 (详情抽屉「分块」页签; null = 未加载)
  const [docChunks, setDocChunks] = useState<KBChunkView[] | null>(null);
  // 正文查看模式: rendered = Markdown 渲染; raw = 原文
  const [viewMode, setViewMode] = useState<'rendered' | 'raw'>('rendered');

  useEffect(() => {
    timerRef.current = setTimeout(() => setDebouncedKeyword(keyword.trim()), 400);
    return () => clearTimeout(timerRef.current);
  }, [keyword]);

  const loadCategories = useCallback(async () => {
    try {
		const res = await kbApi.listCategories();
		const items = res.data?.items ?? [];
		setCategories(items);
		// M11.5: 有子级的顶级分类默认展开
		setTreeExpanded(items.filter((c) => !c.parent_id && items.some((k) => k.parent_id === c.id)).map((c) => c.id));
    } catch (err) {
      message.error(getErrorMessage(err, '加载分类失败'));
    } finally {
      setCatLoading(false);
    }
  }, [message]);

  // M11.5: 点选顶级分类时按 父 + 子 过滤条目; 子级仅自身
  const filterCategoryIds = (() => {
    if (!selectedCat) return '';
    const cat = categories.find((c) => c.id === selectedCat);
    if (!cat) return selectedCat;
    if (!cat.parent_id) {
      const kids = categories.filter((c) => c.parent_id === cat.id);
      return [cat.id, ...kids.map((c) => c.id)].join(',');
    }
    return selectedCat;
  })();

  const loadDocs = useCallback(async () => {
    try {
      const res = await kbApi.listDocuments({
        category_id: filterCategoryIds || undefined,
        keyword: debouncedKeyword || undefined,
        source,
        status,
        page,
        page_size: size,
      });
      setDocs(res.data?.items ?? []);
      setTotal(res.data?.total ?? 0);
    } catch (err) {
      message.error(getErrorMessage(err, '加载条目失败'));
    } finally {
      setDocLoading(false);
    }
  }, [filterCategoryIds, debouncedKeyword, source, status, page, size, message]);

  useEffect(() => {
    loadCategories();
  }, [loadCategories]);

  useEffect(() => {
    setDocLoading(true);
    loadDocs();
  }, [loadDocs]);

  // 切换分类 / 筛选时回到第一页
  useEffect(() => {
    setPage(1);
  }, [selectedCat, debouncedKeyword, source, status]);

  // M11.5: 打开详情且有分块时加载块列表 (无分块/未启用分块 = 空)
  useEffect(() => {
    setDocChunks(null);
    if (!viewDoc || !viewDoc.chunk_count) return;
    let cancelled = false;
    kbApi
      .getDocumentChunks(viewDoc.id)
      .then((res) => {
        if (!cancelled) setDocChunks(res.data?.chunks ?? []);
      })
      .catch(() => {
        if (!cancelled) setDocChunks([]);
      });
    return () => {
      cancelled = true;
    };
  }, [viewDoc]);

  // ---- 分类操作 ----
  const openCatCreate = () => {
    setCatEditing(null);
    setCatName('');
    setCatDesc('');
    setCatParent(undefined);
    setCatModalOpen(true);
  };

  const openCatEdit = (record: KBCategory) => {
    setCatEditing(record);
    setCatName(record.name);
    setCatDesc(record.description);
    setCatParent(record.parent_id || undefined);
    setCatModalOpen(true);
  };

  const saveCategory = async () => {
    if (!canWrite) return;
    const name = catName.trim();
    if (name.length < 2 || name.length > 32) {
      message.warning('分类名称须为 2-32 个字符');
      return;
    }
    setCatSaving(true);
    try {
      if (catEditing) {
        await kbApi.updateCategory(catEditing.id, {
          name,
          description: catDesc,
          parent_id: catParent || '',
        });
        message.success('分类已更新');
      } else {
        await kbApi.createCategory({ name, description: catDesc, parent_id: catParent });
        message.success('分类已创建');
      }
      setCatModalOpen(false);
      await Promise.all([loadCategories(), loadDocs()]);
    } catch (err) {
      message.error(getErrorMessage(err, '保存分类失败'));
    } finally {
      setCatSaving(false);
    }
  };

  const onDeleteCategory = async (record: KBCategory) => {
    try {
      await kbApi.removeCategory(record.id);
      message.success('已删除');
      if (selectedCat === record.id) setSelectedCat('');
      await Promise.all([loadCategories(), loadDocs()]);
    } catch (err) {
      message.error(getErrorMessage(err, '删除失败'));
    }
  };

  // ---- 条目操作 ----
  const openDocCreate = () => {
    if (!canWrite) return;
    if (categories.length === 0) {
      message.warning('请先创建知识库分类');
      return;
    }
    setDocEditing(null);
    setDocTitle('');
    setDocContent('');
    setDocCategory(selectedCat || categories[0].id);
    setDocModalOpen(true);
  };

  const openDocEdit = (record: KBDocument) => {
    setDocEditing(record);
    setDocTitle(record.title);
    setDocContent(record.content);
    setDocCategory(record.category_id);
    setDocModalOpen(true);
  };

  const saveDocument = async () => {
    if (!canWrite) return;
    const title = docTitle.trim();
    if (title.length < 2 || title.length > 100) {
      message.warning('标题须为 2-100 个字符');
      return;
    }
    if (!docContent.trim()) {
      message.warning('正文不能为空');
      return;
    }
    setDocSaving(true);
    try {
      if (docEditing) {
        await kbApi.updateDocument(docEditing.id, {
          title,
          content: docContent,
          category_id: docCategory,
        });
        message.success('条目已更新');
      } else {
        if (!docCategory) {
          message.warning('请选择分类');
          return;
        }
        await kbApi.createDocument({ category_id: docCategory, title, content: docContent });
        message.success('条目已创建');
      }
      setDocModalOpen(false);
      await Promise.all([loadCategories(), loadDocs()]);
    } catch (err) {
      message.error(getErrorMessage(err, '保存条目失败'));
    } finally {
      setDocSaving(false);
    }
  };

  const onToggleStatus = async (record: KBDocument) => {
    const next: KBStatus = record.status === 'active' ? 'archived' : 'active';
    try {
      await kbApi.setDocumentStatus(record.id, next);
      message.success(next === 'archived' ? '已归档 (不参与检索, 可恢复)' : '已恢复');
      loadDocs();
    } catch (err) {
      message.error(getErrorMessage(err, '操作失败'));
    }
  };

  const onDeleteDoc = async (record: KBDocument) => {
    try {
      await kbApi.removeDocument(record.id);
      message.success('已删除');
      await Promise.all([loadCategories(), loadDocs()]);
    } catch (err) {
      message.error(getErrorMessage(err, '删除失败'));
    }
  };

  const columns: ColumnsType<KBDocument> = [
    {
      title: '标题',
      dataIndex: 'title',
      width: 260,
      render: (title: string, record) => (
        <a onClick={() => setViewDoc(record)}>{title}</a>
      ),
    },
    {
      title: '分类',
      dataIndex: 'category_name',
      width: 140,
      render: (v: string) => v || <span style={{ color: 'var(--color-text-secondary)' }}>-</span>,
    },
    {
      title: '来源',
      dataIndex: 'source',
      width: 110,
      render: (v: KBSource) => {
        const meta = KB_SOURCE_MAP[v];
        return meta ? <Tag color={meta.color}>{meta.label}</Tag> : v;
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 90,
      render: (v: KBStatus) => {
        const meta = KB_STATUS_MAP[v];
        return meta ? <Tag color={meta.color}>{meta.label}</Tag> : v;
      },
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 170,
      render: (v: string) => formatDateTime(v),
    },
    {
      title: '操作',
      key: 'actions',
      width: 250,
      render: (_, record) => (
        <Space size="small">
          <Button size="small" icon={<EyeOutlined />} onClick={() => setViewDoc(record)}>
            查看
          </Button>
          {canWrite && (
            <>
              <Button size="small" icon={<EditOutlined />} onClick={() => openDocEdit(record)}>
                编辑
              </Button>
              <Button
                size="small"
                icon={record.status === 'active' ? <StopOutlined /> : <UndoOutlined />}
                onClick={() => onToggleStatus(record)}
              >
                {record.status === 'active' ? '归档' : '恢复'}
              </Button>
              <Popconfirm
                title={`确定删除条目 ${record.title}?`}
                description="删除后不可恢复"
                onConfirm={() => onDeleteDoc(record)}
                okText="删除"
                okButtonProps={{ danger: true }}
                cancelText="取消"
              >
                <Button size="small" danger icon={<DeleteOutlined />}>
                  删除
                </Button>
              </Popconfirm>
            </>
          )}
        </Space>
      ),
    },
  ];

  // M11.5: 树数据辅助 (顶级/子级分组; 路径 「父/子」)
  const topCats = categories.filter((c) => !c.parent_id);
  const childrenByParent = (id: string) => categories.filter((c) => c.parent_id === id);
  const catPath = (cat: KBCategory) => {
    if (!cat.parent_id) return cat.name;
    const parent = categories.find((c) => c.id === cat.parent_id);
    return parent ? `${parent.name}/${cat.name}` : cat.name;
  };
  const selectedCategory = categories.find((c) => c.id === selectedCat);

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>知识库</h2>
        <Button icon={<ReloadOutlined />} onClick={() => Promise.all([loadCategories(), loadDocs()])}>
          刷新
        </Button>
      </div>
      <div style={{ display: 'flex', gap: 16, alignItems: 'flex-start' }}>
        {/* 分类侧栏 */}
        <Card
          size="small"
          style={{ width: 280, flexShrink: 0 }}
          title="分类"
          extra={
            canWrite && (
              <Button type="link" size="small" icon={<FolderAddOutlined />} onClick={openCatCreate}>
                新建
              </Button>
            )
          }
        >
          <Spin spinning={catLoading}>
            {categories.length === 0 && !catLoading ? (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无分类" />
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                <div
                  onClick={() => setSelectedCat('')}
                  style={{
                    padding: '6px 10px',
                    borderRadius: 6,
                    cursor: 'pointer',
                    background: selectedCat === '' ? 'var(--color-bg-hover, rgba(0,0,0,0.06))' : undefined,
                  }}
                >
                  <span>全部分类</span>
                </div>
                {/* M11.5: 两级分类树 (带条目数; 点顶级分类筛选父 + 子条目) */}
                <Tree
                  blockNode
                  showLine
                  selectable
                  selectedKeys={selectedCat ? [selectedCat] : []}
                  expandedKeys={treeExpanded}
                  onExpand={(keys) => setTreeExpanded(keys as string[])}
                  onSelect={(keys) => setSelectedCat(String(keys[0] ?? ''))}
                  treeData={topCats.map((cat) => ({
                    key: cat.id,
                    title: (
                      <Space style={{ width: '100%', justifyContent: 'space-between' }}>
                        <Tooltip title={cat.description || cat.name} placement="topLeft">
                          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 140, display: 'inline-block' }}>
                            {cat.name}
                          </span>
                        </Tooltip>
                        <Space size={4} onClick={(e) => e.stopPropagation()}>
                          <Tag style={{ marginInlineEnd: 0 }}>{cat.document_count ?? 0}</Tag>
                          {canWrite && (
                            <>
                              <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openCatEdit(cat)} />
                              <Popconfirm
                                title={`确定删除分类 ${cat.name}?`}
                                description="分类下存在条目或子级分类时将被拦截"
                                onConfirm={() => onDeleteCategory(cat)}
                                okText="删除"
                                okButtonProps={{ danger: true }}
                                cancelText="取消"
                              >
                                <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                              </Popconfirm>
                            </>
                          )}
                        </Space>
                      </Space>
                    ),
                    children: childrenByParent(cat.id).map((sub) => ({
                      key: sub.id,
                      title: (
                        <Space style={{ width: '100%', justifyContent: 'space-between' }}>
                          <Tooltip title={sub.description || `${cat.name}/${sub.name}`} placement="topLeft">
                            <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 120, display: 'inline-block' }}>
                              {sub.name}
                            </span>
                          </Tooltip>
                          <Space size={4} onClick={(e) => e.stopPropagation()}>
                            <Tag style={{ marginInlineEnd: 0 }}>{sub.document_count ?? 0}</Tag>
                            {canWrite && (
                              <>
                                <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openCatEdit(sub)} />
                                <Popconfirm
                                  title={`确定删除分类 ${sub.name}?`}
                                  description="分类下存在条目时将被拦截"
                                  onConfirm={() => onDeleteCategory(sub)}
                                  okText="删除"
                                  okButtonProps={{ danger: true }}
                                  cancelText="取消"
                                >
                                  <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                                </Popconfirm>
                              </>
                            )}
                          </Space>
                        </Space>
                      ),
                    })),
                  }))}
                />
              </div>
            )}
          </Spin>
        </Card>

        {/* 条目表格 */}
        <Card style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', gap: 12, marginBottom: 16, flexWrap: 'wrap' }}>
            <Input
              allowClear
              prefix={<SearchOutlined />}
              placeholder="搜索标题 / 正文"
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              style={{ width: 260 }}
            />
            <Select
              allowClear
              placeholder="来源"
              options={SOURCE_OPTIONS}
              value={source}
              onChange={setSource}
              style={{ width: 130 }}
            />
            <Select
              allowClear
              placeholder="状态"
              options={STATUS_OPTIONS}
              value={status}
              onChange={setStatus}
              style={{ width: 130 }}
            />
            <div style={{ flex: 1 }} />
            <span style={{ color: 'var(--color-text-secondary)', lineHeight: '32px' }}>
              {selectedCategory ? `当前分类: ${selectedCategory.name}` : '全部条目'}
            </span>
            {canWrite && (
              <Button type="primary" icon={<PlusOutlined />} onClick={openDocCreate}>
                新建条目
              </Button>
            )}
          </div>
          <Table
            rowKey="id"
            size="middle"
            loading={docLoading}
            columns={columns}
            dataSource={docs}
            scroll={{ x: 950 }}
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
      </div>

      {/* 分类新建/编辑 */}
      <Modal
        title={
          catEditing ? `编辑分类 — ${catPath(catEditing)}` : catParent ? `新建子级分类` : '新建分类'
        }
        open={catModalOpen}
        onOk={saveCategory}
        onCancel={() => setCatModalOpen(false)}
        okText="保存"
        confirmLoading={catSaving}
        destroyOnClose
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 8 }}>
          <div>
            <div style={{ marginBottom: 4 }}>名称 (2-32 字符, 同一父级下同级唯一)</div>
            <Input value={catName} onChange={(e) => setCatName(e.target.value)} maxLength={32} placeholder="如: 运维手册" />
          </div>
          <div>
            <div style={{ marginBottom: 4 }}>所属 (仅支持两级: 子级须挂在顶级分类下)</div>
            <Select
              value={catParent ?? ''}
              onChange={(v) => setCatParent(v || undefined)}
              style={{ width: '100%' }}
              options={[
                { value: '', label: '顶级分类' },
                ...topCats
                  .filter((c) => (catEditing ? c.id !== catEditing.id : true))
                  .map((c) => ({ value: c.id, label: c.name })),
              ]}
            />
          </div>
          <div>
            <div style={{ marginBottom: 4 }}>描述 (可选, ≤200 字符)</div>
            <Input.TextArea
              value={catDesc}
              onChange={(e) => setCatDesc(e.target.value)}
              maxLength={200}
              rows={3}
              placeholder="分类用途说明, 会展示在 Agent 绑定选择中"
            />
          </div>
        </div>
      </Modal>

      {/* 条目新建/编辑 */}
      <Modal
        title={docEditing ? '编辑条目' : '新建条目'}
        open={docModalOpen}
        onOk={saveDocument}
        onCancel={() => setDocModalOpen(false)}
        okText="保存"
        confirmLoading={docSaving}
        width={720}
        destroyOnClose
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 8 }}>
          <div>
            <div style={{ marginBottom: 4 }}>所属分类</div>
            <Select
              value={docCategory}
              onChange={setDocCategory}
              options={categories.map((c) => ({ value: c.id, label: catPath(c) }))}
              placeholder="选择分类"
            />
          </div>
          <div>
            <div style={{ marginBottom: 4 }}>标题 (2-100 字符)</div>
            <Input value={docTitle} onChange={(e) => setDocTitle(e.target.value)} maxLength={100} placeholder="条目标题" />
          </div>
          <div>
            <div style={{ marginBottom: 4 }}>
              正文 (Markdown, ≤200KB)
              {docEditing?.source === 'chat_summary' && (
                <Tag color="purple" style={{ marginLeft: 8 }}>
                  对话总结入库 (来源标记不可改)
                </Tag>
              )}
            </div>
            <Input.TextArea
              value={docContent}
              onChange={(e) => setDocContent(e.target.value)}
              rows={12}
              placeholder="支持 Markdown 语法"
              style={{ fontFamily: 'monospace' }}
            />
          </div>
        </div>
      </Modal>

      {/* 条目详情抽屉 */}
      <Drawer
        title={viewDoc?.title ?? ''}
        open={!!viewDoc}
        onClose={() => setViewDoc(null)}
        width={640}
        destroyOnClose
      >
        {viewDoc && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div>
              <Space size={8} wrap>
                <Tag color={KB_SOURCE_MAP[viewDoc.source]?.color}>{KB_SOURCE_MAP[viewDoc.source]?.label}</Tag>
                <Tag color={KB_STATUS_MAP[viewDoc.status]?.color}>{KB_STATUS_MAP[viewDoc.status]?.label}</Tag>
                <Tag>{viewDoc.category_name || viewDoc.category_id}</Tag>
              </Space>
            </div>
            <Descriptions column={2} size="small" bordered>
              <Descriptions.Item label="创建时间" span={2}>
                {formatDateTime(viewDoc.created_at)}
              </Descriptions.Item>
              <Descriptions.Item label="更新时间" span={2}>
                {formatDateTime(viewDoc.updated_at)}
              </Descriptions.Item>
              <Descriptions.Item label="访问次数">{viewDoc.access_count}</Descriptions.Item>
              <Descriptions.Item label="最近访问">
                {viewDoc.last_accessed_at ? formatDateTime(viewDoc.last_accessed_at) : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="分块数">{viewDoc.chunk_count ?? 0}</Descriptions.Item>
              {viewDoc.source_session_id && (
                <Descriptions.Item label="来源会话" span={2}>
                  <Typography.Text copyable code>
                    {viewDoc.source_session_id}
                  </Typography.Text>
                </Descriptions.Item>
              )}
            </Descriptions>
            <Tabs
              defaultActiveKey="content"
              items={[
                {
                  key: 'content',
                  label: '正文',
                  children: (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                      <Segmented
                        size="small"
                        value={viewMode}
                        onChange={(value) => setViewMode(value as 'rendered' | 'raw')}
                        options={[
                          { label: '渲染', value: 'rendered' },
                          { label: '原文', value: 'raw' },
                        ]}
                      />
                      {viewMode === 'rendered' ? (
                        viewDoc.content.trim() ? (
                          <div
                            style={{
                              background: 'var(--color-bg-light, rgba(0,0,0,0.03))',
                              padding: 12,
                              borderRadius: 8,
                              maxHeight: '60vh',
                              overflow: 'auto',
                            }}
                          >
                            <Markdown content={viewDoc.content} />
                          </div>
                        ) : (
                          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无正文" />
                        )
                      ) : (
                        <pre
                          style={{
                            margin: 0,
                            whiteSpace: 'pre-wrap',
                            wordBreak: 'break-word',
                            background: 'var(--color-bg-light, rgba(0,0,0,0.03))',
                            padding: 12,
                            borderRadius: 8,
                            fontSize: 13,
                            lineHeight: 1.7,
                            maxHeight: '60vh',
                            overflow: 'auto',
                          }}
                        >
                          {viewDoc.content}
                        </pre>
                      )}
                    </div>
                  ),
                },
                {
                  key: 'chunks',
                  label: `分块${viewDoc.chunk_count ? ` (${viewDoc.chunk_count})` : ''}`,
                  children: (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                      {docChunks === null ? (
                        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无分块 (短条目 1 块 = 整条; 未启用分块时无数据)" />
                      ) : docChunks.length === 0 ? (
                        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无分块" />
                      ) : (
                        docChunks.map((c) => (
                          <div key={c.chunk_index}>
                            <Space size={8} style={{ marginBottom: 4 }}>
                              <Tag>#{c.chunk_index + 1}</Tag>
                              {c.vectorized ? (
                                <Tag color="green">已向量化</Tag>
                              ) : (
                                <Tag color="orange">待回填</Tag>
                              )}
                            </Space>
                            <pre
                              style={{
                                whiteSpace: 'pre-wrap',
                                wordBreak: 'break-word',
                                background: 'var(--color-bg-light, rgba(0,0,0,0.03))',
                                padding: 10,
                                borderRadius: 8,
                                fontSize: 12,
                                lineHeight: 1.6,
                              }}
                            >
                              {c.content}
                            </pre>
                          </div>
                        ))
                      )}
                    </div>
                  ),
                },
              ]}
            />
          </div>
        )}
      </Drawer>
    </div>
  );
}
