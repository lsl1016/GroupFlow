import type { GroupMessage, Role } from '../types';

export function VirtualMessageList({ messages, myUserId, myRole, onRecall, onRetry }: { messages: GroupMessage[]; myUserId?:number; myRole?:Role; onRecall?: (m:GroupMessage)=>void; onRetry?: (m:GroupMessage)=>void }) {
  const visible = messages.slice(Math.max(0, messages.length - 200));
  const canManageRecall = myRole === 'owner' || myRole === 'admin';
  return <div className="message-list">
    {messages.length > 200 && <div className="sys-tip">大群优化：仅渲染最近 200 条消息，历史消息通过游标分页加载</div>}
    {visible.map(m => {
      const canRecall = !!m.messageId && m.status !== 'recalled' && m.messageType !== 'system' && (m.senderId === myUserId || canManageRecall);
      return <div key={m.messageId || m.clientMessageId} className={`msg ${m.messageType === 'system' ? 'system' : ''} ${m.status === 'recalled' ? 'recalled' : ''}`}>
        {m.messageType === 'system' ? <span>{m.content}</span> : <>
          <b>{m.senderName}</b>
          {m.status === 'recalled' ? <span className="recall-text">这条消息已被撤回</span> : <span>{m.mentionAll && <em className="mention">@所有人</em>}{m.mentionUserIds?.length ? <em className="mention">@{m.mentionUserIds.join(',')}</em> : null}{m.content}</span>}
          <small>{m.status === 'sending' ? '发送中' : m.status === 'failed' ? <span className="retry" onClick={() => onRetry?.(m)}>发送失败，点击重试</span> : m.sequence ? `#${m.sequence}` : ''}</small>
          {canRecall && <button className="mini-btn" onClick={() => onRecall?.(m)}>撤回</button>}
        </>}
      </div>;
    })}
  </div>;
}
