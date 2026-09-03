import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { App, Button, Card, Checkbox, Empty, Input, List, Popconfirm, Space, Spin, Tag, Tooltip } from 'antd';
import { AuditOutlined, BulbOutlined, DeleteOutlined, EditOutlined, PlusOutlined, SendOutlined, StopOutlined, ThunderboltOutlined, ToolOutlined } from '@ant-design/icons';
import { agentApi, chatStream, type ChatStreamEventPayload } from '@/api/agent';
import { getErrorMessage } from '@/api/client';
import type { ChatMCPCall, ChatMessage, ChatPendingApproval, ChatSession, ChatSkillCall } from '@/types';
import { timeAgo } from '@/utils/format';
import { MathText } from '@/components/common/MathText';

// M2.5.7 Agent 对话面板: 会话列表 + 消息气泡 + 输入框 (执行元数据内嵌, 待审批入口)
// 技能加载状态 (M9) -> 徽标颜色 / 文案
const SKILL_CALL_STATUS_COLOR: Record<string, string> = { ok: 'blue', partial: 'orange', duplicate: 'default', error: 'red' };
const SKILL_CALL_STATUS_TEXT: Record<string, string> = { ok: '已加载', partial: '已加载(缺依赖)', duplicate: '已加载过', error: '加载失败' };

// SSE 流式执行进度 (进度卡): 阶段文案 + 工具调用明细
interface StreamToolItem {
  key: string;
  label: string;
  status: string;
  latencyMs?: number;
}

interface StreamThinkingSeg {
  round: number;
  text: string;
}

interface StreamProgress {
  stage: string;
  since: number;
  tools: StreamToolItem[];
  thinking: StreamThinkingSeg[]; // 思考过程 (显示思考过程开启时, 按轮次分段累积)
}

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
  const thinkingRef = useRef<HTMLDivElement>(null);
  // SSE 进度卡状态: progress 当前阶段/工具明细, now 秒级 tick, abortRef 停止控制, lastInputRef 停止后还原输入
  const [progress, setProgress] = useState<StreamProgress | null>(null);
  const [now, setNow] = useState(0);
  const abortRef = useRef<AbortController | null>(null);
  const lastInputRef = useRef('');
  // 显示思考过程 (用户偏好, localStorage 持久化): 勾选后实时展示模型思考增量, 历史消息展示思考块
  const [showThinking, setShowThinking] = useState(() => localStorage.getItem('chat_show_thinking') === '1');
  const onToggleShowThinking = (checked: boolean) => {
    setShowThinking(checked);
    localStorage.setItem('chat_show_thinking', checked ? '1' : '0');
  };
  // 会话重命名 (行内编辑): 正在重命名的会话 / 输入值 / 保存中
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renameSaving, setRenameSaving] = useState(false);

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
  }, [messages, sending, progress]);
  // 思考过程块内部自动滚动到底部
  useEffect(() => {
    if (thinkingRef.current) {
      thinkingRef.current.scrollTop = thinkingRef.current.scrollHeight;
    }
  }, [progress]);

  // 发送中每秒刷新, 驱动进度卡"已耗时"计数
  useEffect(() => {
    if (!sending) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [sending]);

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

  // 会话重命名 (PUT /agents/:id/sessions/:sid)
  const onStartRename = (s: ChatSession, e: { stopPropagation: () => void }) => {
    e.stopPropagation();
    setRenamingId(s.id);
    setRenameValue(s.title || '');
  };

  const onCancelRename = () => {
    setRenamingId(null);
    setRenameValue('');
  };

  const onConfirmRename = async () => {
    const title = renameValue.trim();
    if (!renamingId || renameSaving) {
      return;
    }
    if (!title) {
      message.warning('会话名不能为空');
      return;
    }
    setRenameSaving(true);
    try {
      const res = await agentApi.renameSession(agentId, renamingId, title);
      setSessions((prev) => prev.map((s) => (s.id === renamingId ? { ...s, title: res.data?.title ?? title } : s)));
      setRenamingId(null);
      setRenameValue('');
      message.success('会话名已更新');
    } catch (err) {
      message.error(getErrorMessage(err, '修改会话名失败'));
    } finally {
      setRenameSaving(false);
    }
  };


  // SSE 阶段事件 -> 进度卡状态 (final/error 不处理, 由 onSend 收尾清理)
  const handleStreamEvent = useCallback((evt: ChatStreamEventPayload) => {
    const data = evt.data;
    const toolLabel = (d: Record<string, unknown>) =>
      (d.mcp_name ? String(d.mcp_name) + '/' : '') + String(d.tool_name ?? '');
    switch (evt.type) {
      case 'turn_start':
        setProgress({ stage: '已开始执行…', since: Date.now(), tools: [], thinking: [] });
        break;
      case 'model_round':
        setProgress((p) => {
          if (!p) return p;
          const stage = data.forced ? '工具轮次用尽, 生成最终答复…' : `模型思考中 (第 ${String(data.round ?? 1)} 轮)…`;
          return { ...p, stage };
        });
        break;
      case 'tool_start':
        setProgress((p) => {
          const base = p ?? { stage: '', since: Date.now(), tools: [], thinking: [] };
          const label = toolLabel(data);
          return {
            ...base,
            stage: `调用工具 ${label}…`,
            tools: [...base.tools, { key: `${base.tools.length}-${label}`, label, status: 'running' }],
          };
        });
        break;
      case 'tool_end':
        setProgress((p) => {
          if (!p) return p;
          const label = toolLabel(data);
          let idx = -1;
          for (let i = p.tools.length - 1; i >= 0; i--) {
            if (p.tools[i].status === 'running' && p.tools[i].label === label) {
              idx = i;
              break;
            }
          }
          const tools = p.tools.map((t, i) =>
            i === idx
              ? { ...t, status: String(data.status ?? 'error'), latencyMs: typeof data.latency_ms === 'number' ? data.latency_ms : undefined }
              : t,
          );
          return { ...p, tools, stage: '模型思考中…' };
        });
        break;
      case 'thinking_delta': {
        const round = typeof data.round === 'number' ? data.round : 1;
        const delta = String(data.delta ?? '');
        if (!delta) return;
        setProgress((p) => {
          const base = p ?? { stage: '', since: Date.now(), tools: [], thinking: [] };
          const segs = [...base.thinking];
          const last = segs[segs.length - 1];
          if (last && last.round === round) {
            segs[segs.length - 1] = { round, text: last.text + delta };
          } else {
            segs.push({ round, text: delta });
          }
          return { ...base, thinking: segs };
        });
        break;
      }
      case 'final':
      case 'error':
        break;
    }
  }, []);

  const onSend = async () => {
    const text = input.trim();
    if (!text || sending) {
      return;
    }
    const controller = new AbortController();
    abortRef.current = controller;
    lastInputRef.current = text;
    setNow(Date.now());
    setProgress({ stage: '请求中…', since: Date.now(), tools: [], thinking: [] });
    setSending(true);
    setInput('');
    // 乐观插入用户消息: 不等模型应答立即上屏; 成功后由服务端数据整体替换, 失败/停止时回滚移除
    const tempId = `tmp-user-${Date.now()}`;
    setMessages((prev) => [
      ...prev,
      { id: tempId, session_id: activeId ?? '', role: 'user', content: text, execution_id: null, created_at: new Date().toISOString() },
    ]);
    try {
      const streamBody = activeId ? { session_id: activeId, message: text } : { message: text };
      const result = await chatStream(agentId, { ...streamBody, show_thinking: showThinking }, {
        signal: controller.signal,
        onEvent: handleStreamEvent,
      });
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
      if (err instanceof DOMException && err.name === 'AbortError') {
        // 用户主动停止: 还原输入文案, 不弹错误
        setInput(lastInputRef.current);
      } else {
        message.error(getErrorMessage(err, '对话失败'));
      }
    } finally {
      // 回滚乐观消息: 成功时列表已被服务端数据替换 (此处为 no-op), 失败/停止时移除临时气泡
      setMessages((prev) => prev.filter((m) => m.id !== tempId));
      setSending(false);
      setProgress(null);
      abortRef.current = null;
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
              onClick={() => {
                if (renamingId !== s.id) {
                  setActiveId(s.id);
                }
              }}
              style={{
                position: 'relative',
                cursor: 'pointer',
                background: s.id === activeId ? 'rgba(22,119,255,0.08)' : undefined,
                borderRadius: 6,
                padding: '6px 8px',
              }}
            >
              {renamingId === s.id ? (
                <div style={{ width: '100%', paddingRight: 8 }} onClick={(e) => e.stopPropagation()}>
                  <Input
                    size='small'
                    autoFocus
                    maxLength={128}
                    value={renameValue}
                    onChange={(e) => setRenameValue(e.target.value)}
                    onPressEnter={() => onConfirmRename()}
                    onKeyDown={(e) => {
                      if (e.key === 'Escape') {
                        onCancelRename();
                      }
                    }}
                  />
                  <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 4, marginTop: 2 }}>
                    <Button size='small' type='text' disabled={renameSaving} onClick={onCancelRename}>
                      取消
                    </Button>
                    <Button size='small' type='link' loading={renameSaving} onClick={onConfirmRename}>
                      保存
                    </Button>
                  </div>
                </div>
              ) : (
                <>
                  <div style={{ width: '100%', overflow: 'hidden', paddingRight: 44 }}>
                    <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {s.title || '未命名会话'}
                    </div>
                    <div style={{ fontSize: 12, color: '#8c8c8c' }}>{timeAgo(s.last_message_at)}</div>
                  </div>
                  <EditOutlined className="session-rename-btn" onClick={(e) => onStartRename(s, e)} />
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
                </>
              )}
            </List.Item>
          )}
        />
      </Card>

      <Card
        size='small'
        style={{ flex: 1 }}
        title={activeSession ? activeSession.title || '对话' : '新对话'}
        extra={
          <Checkbox checked={showThinking} onChange={(e) => onToggleShowThinking(e.target.checked)}>
            显示思考过程
          </Checkbox>
        }
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
              <MessageBubble key={m.id} msg={m} onOpenApprovals={() => navigate('/approvals')} showThinking={showThinking} />
            ))
          )}
          {sending && (
            <div style={{ display: 'flex', justifyContent: 'flex-start', marginBottom: 12 }}>
              <div
                style={{
                  padding: '8px 12px',
                  borderRadius: 8,
                  background: '#f5f5f5',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 6,
                  minWidth: 260,
                  maxWidth: '72%',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <Spin size='small' />
                  <span style={{ fontSize: 13 }}>{progress ? progress.stage : 'Agent 应答中…'}</span>
                  {progress && (
                    <span style={{ fontSize: 12, color: '#8c8c8c' }}>已耗时 {Math.max(0, Math.floor((now - progress.since) / 1000))}s</span>
                  )}
                </div>
                {progress && progress.tools.length > 0 && (
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                    {progress.tools.map((t) => (
                      <Tag
                        key={t.key}
                        color={t.status === 'running' ? 'blue' : t.status === 'ok' ? 'green' : t.status === 'pending' ? 'orange' : 'red'}
                      >
                        {t.label}
                        {t.status === 'running' ? ' 执行中…' : ` · ${t.status}`}
                        {t.latencyMs !== undefined ? ` ${t.latencyMs}ms` : ''}
                      </Tag>
                    ))}
                  </div>
                )}
                {showThinking && progress && progress.thinking.length > 0 && (
                  <div style={{ marginTop: 2 }}>
                    <div style={{ fontSize: 12, color: '#8c8c8c', marginBottom: 4 }}>
                      <BulbOutlined style={{ marginRight: 4 }} />思考过程
                    </div>
                    <div
                      ref={thinkingRef}
                      style={{
                        maxHeight: 180,
                        overflowY: 'auto',
                        background: '#fafafa',
                        border: '1px dashed #d9d9d9',
                        borderRadius: 6,
                        padding: '6px 8px',
                        fontSize: 12,
                        color: '#666',
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-word',
                      }}
                    >
                      {progress.thinking.map((seg) => (
                        <div key={seg.round} style={{ marginBottom: seg.round === progress.thinking.length - 1 ? 0 : 8 }}>
                          {progress.thinking.length > 1 && <div style={{ color: '#bfbfbf', marginBottom: 2 }}>第 {seg.round} 轮</div>}
                          {seg.text}
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
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
            {sending && (
              <Button danger icon={<StopOutlined />} onClick={() => abortRef.current?.abort()}>
                停止
              </Button>
            )}
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
  showThinking,
}: {
  msg: ChatMessage;
  onOpenApprovals: () => void;
  showThinking: boolean;
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
  const thinking = typeof meta?.thinking === 'string' && meta.thinking ? meta.thinking : '';

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
        {showThinking && thinking && (
          <details style={{ marginTop: 6 }}>
            <summary style={{ cursor: 'pointer', fontSize: 12, color: '#8c8c8c', userSelect: 'none', opacity: 0.9 }}>
              思考过程
            </summary>
            <div
              style={{
                marginTop: 6,
                maxHeight: 240,
                overflowY: 'auto',
                background: '#fafafa',
                border: '1px dashed #d9d9d9',
                borderRadius: 6,
                padding: '6px 8px',
                fontSize: 12,
                color: '#666',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
              }}
            >
              {thinking}
            </div>
          </details>
        )}
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
