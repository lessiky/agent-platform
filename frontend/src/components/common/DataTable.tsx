import { useCallback, useEffect, useRef, useState } from 'react';
import { Table } from 'antd';
import type { TablePaginationConfig, ColumnsType } from 'antd/es/table';

interface DataTableProps<T extends { id: string }> {
  columns: ColumnsType<T>;
  // 返回当前页数据与总数
  fetchData: (params: { page: number; size: number }) => Promise<{ items: T[]; total: number }>;
  // 变化时触发重新加载 (如搜索关键字)
  reloadKey?: unknown;
  defaultPageSize?: number;
  rowKey?: string | ((record: T) => string);
  loading?: boolean;
  children?: React.ReactNode; // 表格下方附加内容
}

// 通用分页表格: 内部管理 page/size/loading/total
export function DataTable<T extends { id: string }>({
  columns,
  fetchData,
  reloadKey,
  defaultPageSize = 20,
  rowKey = 'id',
  loading,
  children,
}: DataTableProps<T>) {
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(defaultPageSize);
  const [data, setData] = useState<T[]>([]);
  const [total, setTotal] = useState(0);
  const [internalLoading, setInternalLoading] = useState(false);
  const reqIdRef = useRef(0);

  const load = useCallback(async () => {
    const reqId = ++reqIdRef.current;
    setInternalLoading(true);
    try {
      const res = await fetchData({ page, size });
      if (reqId !== reqIdRef.current) return; // 丢弃过期请求
      setData(res.items);
      setTotal(res.total);
    } catch {
      if (reqId !== reqIdRef.current) return;
      setData([]);
      setTotal(0);
    } finally {
      if (reqId === reqIdRef.current) setInternalLoading(false);
    }
  }, [fetchData, page, size]);

  useEffect(() => {
    load();
  }, [load, reloadKey]);

  const onTableChange = (pagination: TablePaginationConfig) => {
    setPage(pagination.current ?? 1);
    setSize(pagination.pageSize ?? defaultPageSize);
  };

  return (
    <>
      <Table<T>
        rowKey={rowKey}
        columns={columns}
        dataSource={data}
        loading={loading ?? internalLoading}
        onChange={onTableChange}
        pagination={{
          current: page,
          pageSize: size,
          total,
          showSizeChanger: true,
          showTotal: (t) => `共 ${t} 条`,
        }}
      />
      {children}
    </>
  );
}