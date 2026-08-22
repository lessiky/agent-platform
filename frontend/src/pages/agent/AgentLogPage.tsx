import { useNavigate, useParams } from 'react-router-dom';
import { Button, Space } from 'antd';
import { ArrowLeftOutlined } from '@ant-design/icons';
import { AgentLogsPanel } from './AgentLogsPanel';

// 独立日志页 (M2 4.6 日志查看页)
export function AgentLogPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate(`/agents/${id}`)}>
          返回详情
        </Button>
        <span style={{ fontWeight: 600, fontSize: 16 }}>Agent 日志</span>
      </Space>
      {id && <AgentLogsPanel agentId={id} />}
    </div>
  );
}