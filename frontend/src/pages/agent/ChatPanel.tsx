import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { App, Button, Card, Empty, Input, List, Popconfirm, Space, Spin, Tag, Tooltip } from 'antd';
import { AuditOutlined, DeleteOutlined, PlusOutlined, SendOutlined, ThunderboltOutlined, ToolOutlined } from '@ant-design/icons';
import { agentApi } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import type { ChatMCPCall, ChatMessage, ChatPendingApproval, ChatSession, ChatSkillCall } from '@/types';
import { timeAgo } from '@/utils/format';
import { MathText } from '@/components/common/MathText';

// M2.5.7 Agent 对话面板: 会话列表 + 消息气泡 + 输入框 (执行元数据内嵌, 待审批入口)
// 技能加载状态 (M9) -> 徽标颜色 / 文案
const SKILL_CALL_STATUS_COLOR: Record<string, string> = { ok: 'blue', partial: 'orange', duplicate: 'default', error: 'red' };
const SKILL_CALL_STATUS_TEXT: Record<string, string> = { ok: '已加载', partial: '已加载(缺依赖)', duplicate: '已加载过', error: '加载失败' };

export function ChatPanel({ agentId }: { agentId: string }) {
  const { message } = App.useApp();
  const navigate = useNavigate();
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [messagesLoading, setMessagesLoading] = useState(false);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);

  const loadSessions = useCallback(async (keepActive: boolean) => {
    try {
      const res = await agentApi.listSessions(agentId, { page: 1, size: 50 });
      const items = res.data?.items ?? [];
      setSessions(items);
      if (!keepActive) {
        setActiveId(items.length > 0 ? items[0].id : null);
      }
    } catch {
      // 会话列表加载失败静默
    } finally {
      setSessionsLoading(false);
    }
  }, [agentId]);

  const loadMessages = useCallback(async (sid: string) => {
    setMessagesLoading(true);
    try {
      const res = await agentApi.getSession(agentId, sid);
      setMessages(res.data?.messages ?? []);
    } catch (err) {
      message.error(getErrorMessage(err, '加载对话消息失败'));
      setMessages([]);
    } finally {
      setMessagesLoading(false);
    }
  }, [agentId, message]);

  useEffect(() => {
    loadSessions(false);
  }, [loadSessions]);

  useEffect(() => {
    if (activeId) {
      loadMessages(activeId);
    } else {
      setMessages([]);
    }
  }, [activeId, loadMessages]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }, [messages, sending]);

  const onNewSession = () => {
    setActiveId(null);
    setMessages([]);
    setInput('');
  };

  const onDeleteSession = async (sid: string) => {
    try {
      await agentApi.deleteSession(agentId, sid);
      const remaining = sessions.filter((s) => s.id !== sid);
      setSessions(remaining);
      if (activeId === sid) {
        setActiveId(remaining.length > 0 ? remaining[0].id : null);
      }
      message.success('对话已删除');
    } catch (err) {
      message.error(getErrorMessage(err, '删除会话失败'));
    }
  };


  const onSend = async () => {
    const text = input.trim();
    if (!text || sending) {
      return;
    }
    setSending(true);
    setInput('');
    try {
      const res = await agentApi.chat(agentId, activeId ? { session_id: activeId, message: text } : { message: text });
      const result = res.data;
      if (!result) {
        message.error('响应异常');
        return;
      }
      if (result.session_id === activeId) {
        await loadMessages(result.session_id);
      } else {
        // 新建会话: 刷新列表并切换
        await loadSessions(true);
        setActiveId(result.session_id);
      }
    } catch (err) {
      message.error(getErrorMessage(err, '对话失败'));
    } finally {
      setSending(false);
    }
  };


  const activeSession = sessions.find((s) => s.id === activeId) ?? null;

  return (
    <div style={{ display: 'flex', gap: 12, height: 620, alignItems: 'stretch' }}>
      <Card
        size='small'
        style={{ width: 250 }}
        title='会话'
        extra={
          <Button size='small' type='text' icon={<PlusOutlined />} onClick={onNewSession}>
            新建
          </Button>
        }
      >
        <List
          size='small'
          loading={sessionsLoading}
          dataSource={sessions}
          locale={{ emptyText: '还没有对话, 发送消息开始' }}
          renderItem={(s) => (
            <List.Item
              onClick={() => setActiveId(s.id)}
              style={{
                position: 'relative',
                cursor: 'pointer',
                background: s.id === activeId ? 'rgba(22,119,255,0.08)' : undefined,
                borderRadius: 6,
                padding: '6px 8px',
              }}
            >
              <div style={{ width: '100%', overflow: 'hidden', paddingRight: 20 }}>
                <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {s.title || '未命名会话'}
                </div>
                <div style={{ fontSize: 12, color: '#8c8c8c' }}>{timeAgo(s.last_message_at)}</div>
              </div>
              <Popconfirm
                title="删除对话"
                description="将删除该会话及其中全部消息, 确定吗?"
                okText="删除"
                cancelText="取消"
                okButtonProps={{ danger: true }}
                onConfirm={async (e) => {
                  e?.stopPropagation();
                  await onDeleteSession(s.id);
                }}
              >
                <DeleteOutlined className="session-delete-btn" onClick={(e) => e.stopPropagation()} />
              </Popconfirm>
            </List.Item>
          )}
        />
      </Card>

      <Card
        size='small'
        style={{ flex: 1 }}
        title={activeSession ? activeSession.title || '对话' : '新对话'}
        styles={{ body: { display: 'flex', flexDirection: 'column', height: 'calc(100% - 39px)' } }}
      >
        <div style={{ flex: 1, overflowY: 'auto', padding: '4px 8px' }}>
          {messagesLoading ? (
            <div style={{ textAlign: 'center', padding: 40 }}>
              <Spin />
            </div>
          ) : messages.length === 0 ? (
            <Empty style={{ marginTop: 80 }} description='输入提示词开始对话' />
          ) : (
            messages.map((m) => (
              <MessageBubble key={m.id} msg={m} onOpenApprovals={() => navigate('/approvals')} />
            ))
          )}
          {sending && (
            <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: 12 }}>
              <Spin size='small' />
              <span style={{ marginLeft: 8, color: '#8c8c8c' }}>Agent 应答中…</span>
            </div>
          )}
          <div ref={bottomRef} />
        </div>
        <div style={{ borderTop: '1px solid #f0f0f0', paddingTop: 12, marginTop: 8 }}>
          <Space.Compact style={{ width: '100%' }}>
            <Input.TextArea
              autoSize={{ minRows: 1, maxRows: 4 }}
              placeholder='输入用户提示词, Enter 发送, Shift+Enter 换行'
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onPressEnter={(e) => {
                if (!e.shiftKey) {
                  e.preventDefault();
                  onSend();
                }
              }}
              disabled={sending}
            />
            <Button type='primary' icon={<SendOutlined />} loading={sending} onClick={onSend}>
              发送
            </Button>
          </Space.Compact>
        </div>
      </Card>
    </div>
  );
}


// 单条消息气泡: user(右) / assistant(左, 含执行元数据) / tool(居中摘要)
function MessageBubble({
  msg,
  onOpenApprovals,
}: {
  msg: ChatMessage;
  onOpenApprovals: () => void;
}) {
  if (msg.role === 'tool') {
    const lines = msg.content
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l.length > 0);
    return (
      <div
        style={{
          textAlign: 'center',
          margin: '8px 0',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: 2,
        }}
      >
        {lines.map((line, i) => (
          <Tag
            key={i}
            icon={i === 0 ? <ToolOutlined /> : undefined}
            color='orange'
            style={{ maxWidth: '92%', whiteSpace: 'pre-line', wordBreak: 'break-word', margin: 0 }}
          >
            {i === 0 ? `tool: ${line}` : line}
          </Tag>
        ))}
      </div>
    );
  }

  const isUser = msg.role === 'user';
  const meta = msg.execution_meta;
  const mcpCalls = (meta?.mcp_calls as ChatMCPCall[] | undefined) ?? undefined;
  const pending = (meta?.pending_approvals as ChatPendingApproval[] | undefined) ?? undefined;
  const skillCalls = (meta?.skill_calls as ChatSkillCall[] | undefined) ?? undefined;

  return (
    <div style={{ display: 'flex', justifyContent: isUser ? 'flex-end' : 'flex-start', marginBottom: 12 }}>
      <div
        style={{
          maxWidth: '72%',
          padding: '8px 12px',
          borderRadius: 8,
          background: isUser ? '#1677ff' : '#f5f5f5',
          color: isUser ? '#fff' : 'inherit',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
        }}
      >
        {msg.content ? <MathText text={msg.content} /> : <span style={{ opacity: 0.65 }}>(无应答内容)</span>}
        {!isUser && (
          <div style={{ marginTop: 6, fontSize: 12, opacity: 0.75 }}>
            {meta && (
              <span>
                <Tag style={{ marginRight: 6 }}>execution_id {String(meta.execution_id ?? '')}</Tag>
                {meta.model_name ? <span>模型 {String(meta.model_name)} · </span> : null}
                {meta.total_tokens ? <span>{String(meta.total_tokens)} tokens · </span> : null}
                {meta.latency_ms !== undefined && <span>耗时 {String(meta.latency_ms)}ms</span>}
                {meta.error ? <Tag color='red' style={{ marginLeft: 6 }}>调用失败</Tag> : null}
              </span>
            )}
            {meta?.error ? (
              <div
                style={{
                  marginTop: 4,
                  padding: '4px 8px',
                  borderRadius: 6,
                  background: 'rgba(255,77,79,0.08)',
                  color: '#ff4d4f',
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-all',
                }}
              >
                {String(meta.error)}
              </div>
            ) : null}
            {mcpCalls && mcpCalls.length > 0 ? (
              <div style={{ marginTop: 4 }}>
                {mcpCalls.map((c, i) => (
                  <Tag key={i} color={c.status === 'ok' ? 'green' : c.status === 'pending' ? 'orange' : 'red'}>
                    {c.mcp_name ? c.mcp_name + '/' : ''}
                    {c.tool_name} - {c.status}
                  </Tag>
                ))}
              </div>
            ) : null}
            {skillCalls && skillCalls.length > 0 ? (
              <div style={{ marginTop: 4 }}>
                {skillCalls.map((c, i) => (
                  <Tooltip
                    key={i}
                    title={
                      <span>
                        <div>技能 {c.skill_name}{c.version ? ` (v${c.version})` : ''} · {SKILL_CALL_STATUS_TEXT[c.status] ?? c.status}</div>
                        {c.chars ? <div>正文 {c.chars} 字符</div> : null}
                        {c.latency_ms !== undefined ? <div>耗时 {c.latency_ms}ms</div> : null}
                        {c.detail ? <div>{c.detail}</div> : null}
                      </span>
                    }
                  >
                    <Tag color={SKILL_CALL_STATUS_COLOR[c.status] ?? 'default'} icon={<ThunderboltOutlined />}>
                      {c.skill_name} · {SKILL_CALL_STATUS_TEXT[c.status] ?? c.status}
                    </Tag>
                  </Tooltip>
                ))}
              </div>
            ) : null}
            {pending && pending.length > 0 ? (
              <div style={{ marginTop: 6 }}>
                <Button size='small' type='link' icon={<AuditOutlined />} onClick={onOpenApprovals}>
                  {pending.length} 个工具调用待人工审核, 前往审核中心
                </Button>
              </div>
            ) : null}
          </div>
        )}
      </div>
    </div>
  );
}
