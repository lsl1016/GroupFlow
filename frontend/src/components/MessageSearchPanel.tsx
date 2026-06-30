import { useEffect, useRef, useState } from 'react';
import { searchMessages } from '../api/groupApi';
import type { Group, Member, SearchHit } from '../types';

type Props = {
  // 单群搜索时传入 group（收窄范围）；全局搜索时传 undefined。
  group?: Group;
  groups: Group[];
  members: Member[];
  onClose: () => void;
  onJump: (groupId: number, sequence: number) => void;
};

// 客户端高亮关键词，避免直接渲染服务端 HTML 带来的 XSS 风险（React 会转义文本节点）。
function highlight(text: string, keyword: string) {
  if (!keyword) return [text];
  const parts: (string | JSX.Element)[] = [];
  const lower = text.toLowerCase();
  const kw = keyword.toLowerCase();
  let i = 0;
  let n = 0;
  while (i < text.length) {
    const idx = lower.indexOf(kw, i);
    if (idx < 0) { parts.push(text.slice(i)); break; }
    if (idx > i) parts.push(text.slice(i, idx));
    parts.push(<mark key={n++}>{text.slice(idx, idx + keyword.length)}</mark>);
    i = idx + keyword.length;
  }
  return parts;
}

function groupName(groups: Group[], groupId: number) {
  return groups.find(g => g.groupId === groupId)?.name || `群 ${groupId}`;
}

export function MessageSearchPanel({ group, groups, members, onClose, onJump }: Props) {
  const [keyword, setKeyword] = useState('');
  const [senderId, setSenderId] = useState(0);
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');
  const [items, setItems] = useState<SearchHit[]>([]);
  const [cursor, setCursor] = useState('');
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const reqSeq = useRef(0);

  function toUnix(v: string) { return v ? Math.floor(new Date(v).getTime() / 1000) : 0; }

  async function run(reset: boolean) {
    const my = ++reqSeq.current;
    setLoading(true); setError('');
    try {
      const res = await searchMessages({
        keyword: keyword.trim(),
        groupId: group?.groupId,
        senderId: senderId || undefined,
        startTime: toUnix(start) || undefined,
        endTime: toUnix(end) || undefined,
        cursor: reset ? '' : cursor,
        limit: 20,
      });
      if (my !== reqSeq.current) return; // 丢弃过期请求结果
      setItems(prev => reset ? res.items : [...prev, ...res.items]);
      setCursor(res.nextCursor);
      setHasMore(res.hasMore);
    } catch (e: any) {
      if (my === reqSeq.current) setError(e?.message || '搜索失败');
    } finally {
      if (my === reqSeq.current) setLoading(false);
    }
  }

  // 关键词/筛选变化时 debounce 触发首页搜索。
  useEffect(() => {
    const t = setTimeout(() => { run(true); }, 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keyword, senderId, start, end, group?.groupId]);

  return (
    <div className="search-overlay" onClick={onClose}>
      <div className="search-panel" onClick={e => e.stopPropagation()}>
        <div className="search-head">
          <b>{group ? `在「${group.name}」中搜索` : '全局搜索聊天记录'}</b>
          <button className="mini-btn" onClick={onClose}>关闭</button>
        </div>
        <div className="search-filters">
          <input autoFocus value={keyword} onChange={e => setKeyword(e.target.value)} placeholder="输入关键词搜索消息" />
          {group && (
            <select value={senderId} onChange={e => setSenderId(Number(e.target.value))}>
              <option value={0}>全部成员</option>
              {members.map(m => <option key={m.userId} value={m.userId}>{m.nickname}</option>)}
            </select>
          )}
          <label className="search-time">从 <input type="datetime-local" value={start} onChange={e => setStart(e.target.value)} /></label>
          <label className="search-time">到 <input type="datetime-local" value={end} onChange={e => setEnd(e.target.value)} /></label>
        </div>
        <div className="search-results">
          {error && <div className="search-error">{error}</div>}
          {!error && !loading && items.length === 0 && <div className="search-empty">没有匹配的消息</div>}
          {items.map(hit => (
            <button className="search-hit" key={hit.messageId} onClick={() => onJump(hit.groupId, hit.sequence)}>
              <div className="search-hit-head">
                {!group && <span className="search-hit-group">{groupName(groups, hit.groupId)}</span>}
                <span className="search-hit-sender">{hit.senderName}</span>
                <span className="search-hit-time">{new Date(hit.createdAt).toLocaleString()}</span>
              </div>
              <div className="search-hit-content">{highlight(hit.content, keyword.trim())}</div>
            </button>
          ))}
          {loading && <div className="search-loading">搜索中…</div>}
          {hasMore && !loading && <button className="load-more" onClick={() => run(false)}>加载更多结果</button>}
        </div>
      </div>
    </div>
  );
}
