import { Tag } from 'antd';
import { AGENT_STATUS_MAP, INSTANCE_STATUS_MAP, LOG_LEVEL_MAP, MCP_STATUS_MAP, MODEL_STATUS_MAP } from '@/utils/constants';
import type { AgentStatus, InstanceStatus, LogLevel, MCPStatus, ModelStatus } from '@/types';

export function AgentStatusTag({ status }: { status: AgentStatus }) {
  const item = AGENT_STATUS_MAP[status] ?? { label: status, color: 'default' };
  return <Tag color={item.color}>{item.label}</Tag>;
}

export function InstanceStatusTag({ status }: { status: InstanceStatus }) {
  const item = INSTANCE_STATUS_MAP[status] ?? { label: status, color: 'default' };
  return <Tag color={item.color}>{item.label}</Tag>;
}

export function LogLevelTag({ level }: { level: LogLevel }) {
  const item = LOG_LEVEL_MAP[level] ?? { label: level, color: 'default' };
  return <Tag color={item.color}>{item.label}</Tag>;
}

export function MCPStatusTag({ status }: { status: MCPStatus }) {
  const item = MCP_STATUS_MAP[status] ?? { label: status, color: 'default' };
  return <Tag color={item.color}>{item.label}</Tag>;
}

export function ModelStatusTag({ status }: { status: ModelStatus }) {
  const item = MODEL_STATUS_MAP[status] ?? { label: status, color: 'default' };
  return <Tag color={item.color}>{item.label}</Tag>;
}