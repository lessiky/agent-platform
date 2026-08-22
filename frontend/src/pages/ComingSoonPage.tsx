import { Result } from 'antd';
import { ClockCircleOutlined } from '@ant-design/icons';

export function ComingSoonPage({ milestone, title }: { milestone: string; title: string }) {
  return (
    <Result
      icon={<ClockCircleOutlined style={{ fontSize: 48, color: '#bfbfbf' }} />}
      title={`${title} (${milestone})`}
      subTitle="该模块尚未开发，敬请期待。"
      style={{ marginTop: 120 }}
    />
  );
}