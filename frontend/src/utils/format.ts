import dayjs from 'dayjs';

// 格式化日期时间
export function formatDateTime(value?: string | null): string {
  if (!value) return '-';
  return dayjs(value).format('YYYY-MM-DD HH:mm:ss');
}

// 相对时间 (用于心跳等实时展示)
export function timeAgo(value?: string | null): string {
  if (!value) return '-';
  const diff = Date.now() - dayjs(value).valueOf();
  if (diff < 0) return '刚刚';
  const seconds = Math.floor(diff / 1000);
  if (seconds < 5) return '刚刚';
  if (seconds < 60) return `${seconds} 秒前`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return dayjs(value).format('MM-DD HH:mm');
}

// 千分位数字
export function formatNumber(value?: number | null): string {
  if (value === null || value === undefined) return '-';
  return value.toLocaleString('zh-CN');
}

// 百分比
export function formatPercent(value?: number | null): string {
  if (value === null || value === undefined) return '-';
  return `${(value * 100).toFixed(1)}%`;
}