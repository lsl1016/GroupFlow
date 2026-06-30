import { useEffect, useState } from 'react';
import { approveJoinRequest, createAnnouncement, createGroup, deleteAnnouncement, dismissGroup, getGroup, getMessages, joinGroup, kickMember, leaveGroup, listAnnouncements, listGroups, listJoinRequests, listMembers, markRead, muteMember, readMentions, recallMessage, rejectJoinRequest, setRole, unmuteMember, updateSettings } from '../api/groupApi';
import { useStore } from '../stores/useStore';
import { wsClient } from '../websocket/wsClient';
import { VirtualMessageList } from '../components/VirtualMessageList';
import type { Announcement, Group, GroupMessage, JoinRequest, Member } from '../types';

export function ChatPage() {
  const { user, groups, currentGroup, messagesByGroup, members, connectionStatus, setGroups, setCurrentGroup, setMembers, mergeMessages, setMessages, recallMessage: markRecalled } = useStore();
  const [text, setText] = useState('');
  const [notice, setNotice] = useState('');
  const [newGroup, setNewGroup] = useState('');
  const [joinId, setJoinId] = useState('10001');
  const [joinReason, setJoinReason] = useState('想加入一起学习高并发');
  const [mentionAll, setMentionAll] = useState(false);
  const [mentionIds, setMentionIds] = useState('');
  const msgs = currentGroup ? (messagesByGroup[currentGroup.groupId] || []) : [];

  useEffect(() => { refreshGroups(); wsClient.connect(); }, []);

  async function refreshGroups() {
    const page = await listGroups();
    setGroups(page.items);
    if (!currentGroup && page.items[0]) openGroup(page.items[0]);
  }
  async function openGroup(g: Group) {
    const detail = await getGroup(g.groupId);
    setCurrentGroup({ ...detail.group, myRole: detail.myMember?.role });
    const page = await getMessages(g.groupId, { limit:50 });
    setMessages(g.groupId, page.items);
    const maxSeq = Math.max(0, ...page.items.map(m => m.sequence || 0));
    if (maxSeq) wsClient.seedLastReceived(g.groupId, maxSeq);
    const mem = await listMembers(g.groupId);
    setMembers(mem.items);
    if (g.mentionCount || g.mentionAllUnread) await readMentions(g.groupId, 0).catch(()=>{});
  }
  async function loadMore() {
    if (!currentGroup || !msgs[0]?.sequence) return;
    const page = await getMessages(currentGroup.groupId, { beforeSequence: msgs[0].sequence, limit:50 });
    mergeMessages(currentGroup.groupId, page.items);
  }
  async function send() {
    if (!currentGroup || !text.trim()) return;
    const ids = mentionIds.split(',').map(x=>Number(x.trim())).filter(Boolean);
    wsClient.sendText(currentGroup.groupId, text.trim(), { mentionAll, mentionUserIds: ids });
    setText(''); setMentionAll(false); setMentionIds('');
  }
  async function recall(m: GroupMessage) {
    if (!currentGroup || !m.messageId) return;
    const evt = await recallMessage(currentGroup.groupId, m.messageId, '前端手动撤回');
    markRecalled(evt);
  }
  async function readAll() {
    if (!currentGroup || !msgs.length) return;
    const max = Math.max(...msgs.map(m=>m.sequence || 0));
    await markRead(currentGroup.groupId, max);
    wsClient.markRead(currentGroup.groupId, max);
    setNotice(`已读位置更新到 ${max}`);
    refreshGroups();
  }
  async function create() {
    if (!newGroup.trim()) return;
    const g = await createGroup({ name:newGroup, joinMode:'direct', maxMemberCount:500, groupType:'normal' });
    setNewGroup('');
    await refreshGroups();
    openGroup(g);
  }
  async function join() {
    const result = await joinGroup(Number(joinId), joinReason);
    setNotice(result.pending ? '已提交加群申请，等待管理员审批' : '已加入群聊');
    await refreshGroups();
  }
  async function toggleLarge() {
    if (!currentGroup) return;
    await updateSettings(currentGroup.groupId, { groupType: currentGroup.groupType === 'large' ? 'normal' : 'large' });
    await openGroup(currentGroup);
  }
  async function toggleMuteAll() {
    if (!currentGroup) return;
    await updateSettings(currentGroup.groupId, { muteAll: !currentGroup.muteAll });
    await openGroup(currentGroup);
  }
  async function slow(seconds:number) {
    if (!currentGroup) return;
    await updateSettings(currentGroup.groupId, { slowModeSeconds: seconds });
    await openGroup(currentGroup);
  }
  async function leave() {
    if (!currentGroup) return;
    await leaveGroup(currentGroup.groupId);
    setCurrentGroup(undefined);
    refreshGroups();
  }
  async function dismiss() {
    if (!currentGroup) return;
    await dismissGroup(currentGroup.groupId);
    setCurrentGroup(undefined);
    refreshGroups();
  }
  async function refreshMembers() {
    if (!currentGroup) return;
    const m = await listMembers(currentGroup.groupId);
    setMembers(m.items);
  }

  return <div className="app-shell">
    <aside className="left-panel">
      <div className="topbar"><b>群流</b><span className={`dot ${connectionStatus}`}>{connectionStatus}</span></div>
      <div className="create-row"><input value={newGroup} onChange={e=>setNewGroup(e.target.value)} placeholder="创建群名称"/><button onClick={create}>建群</button></div>
      <div className="create-row stacked"><input value={joinId} onChange={e=>setJoinId(e.target.value)} placeholder="群ID"/><input value={joinReason} onChange={e=>setJoinReason(e.target.value)} placeholder="加群理由"/><button onClick={join}>加入/申请</button></div>
      <div className="group-list">{groups.map(g => <button className={`group-item ${currentGroup?.groupId === g.groupId ? 'active' : ''}`} key={g.groupId} onClick={()=>openGroup(g)}>
        <b>{g.name}</b><span>{g.lastMessageSummary || '暂无消息'}</span>{g.mentionSummaryText && <strong>{g.mentionSummaryText}</strong>}{!!g.unreadCount && <em>{g.unreadCount > 99 ? '99+' : g.unreadCount}</em>}
      </button>)}</div>
    </aside>
    <main className="chat-panel">
      {!currentGroup ? <div className="empty">请选择或创建一个群</div> : <>
        <header className="chat-header"><div><h2>{currentGroup.name}</h2><p>群ID {currentGroup.groupId} · {currentGroup.memberCount} 人 · {currentGroup.groupType === 'large' ? '大群模式' : '普通群'} · 慢速 {currentGroup.slowModeSeconds}s · {currentGroup.muteAll ? '全员禁言' : '可发言'} · 入群 {currentGroup.joinMode}</p></div><button onClick={readAll}>标记已读</button></header>
        <button className="load-more" onClick={loadMore}>加载更多历史消息</button>
        <VirtualMessageList messages={msgs} myUserId={user?.userId} myRole={currentGroup.myRole} onRecall={recall} onRetry={m=>wsClient.retry(currentGroup.groupId, m.clientMessageId)}/>
        <footer className="input-bar">
          <label className="check"><input type="checkbox" checked={mentionAll} onChange={e=>setMentionAll(e.target.checked)}/> @所有人</label>
          <input className="mention-input" value={mentionIds} onChange={e=>setMentionIds(e.target.value)} placeholder="@用户ID，逗号分隔"/>
          <input disabled={currentGroup.muteAll} value={text} onChange={e=>setText(e.target.value)} onKeyDown={e=>{ if(e.key==='Enter') send(); }} placeholder={currentGroup.muteAll ? '当前全员禁言' : '输入文本消息'}/>
          <button onClick={send}>发送</button>
        </footer>
        {notice && <div className="toast" onClick={()=>setNotice('')}>{notice}</div>}
      </>}
    </main>
    <aside className="right-panel">{currentGroup && <GroupSide group={currentGroup} members={members} reload={async()=>{await openGroup(currentGroup); await refreshMembers();}} actions={{toggleLarge,toggleMuteAll,slow,leave,dismiss}}/>}</aside>
  </div>;
}

function GroupSide({ group, members, reload, actions }: { group:Group; members:Member[]; reload:()=>Promise<void>; actions:any }) {
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [joinRequests, setJoinRequests] = useState<JoinRequest[]>([]);
  const [annTitle, setAnnTitle] = useState('');
  const [annContent, setAnnContent] = useState('');
  const canAdmin = group.myRole === 'owner' || group.myRole === 'admin';

  useEffect(() => { loadSide(); }, [group.groupId, group.myRole]);

  async function loadSide() {
    const anns = await listAnnouncements(group.groupId).catch(()=>({items:[]} as any));
    setAnnouncements(anns.items || []);
    if (canAdmin) {
      const reqs = await listJoinRequests(group.groupId).catch(()=>({items:[]} as any));
      setJoinRequests(reqs.items || []);
    } else {
      setJoinRequests([]);
    }
  }
  async function publishAnn() {
    if (!annTitle.trim() || !annContent.trim()) return;
    await createAnnouncement(group.groupId, { title:annTitle, content:annContent, pinned:true });
    setAnnTitle(''); setAnnContent(''); await loadSide(); await reload();
  }
  async function removeAnn(a:Announcement) { await deleteAnnouncement(group.groupId, a.announcementId); await loadSide(); await reload(); }
  async function approve(r:JoinRequest) { await approveJoinRequest(group.groupId, r.requestId); await loadSide(); await reload(); }
  async function reject(r:JoinRequest) { await rejectJoinRequest(group.groupId, r.requestId); await loadSide(); }
  async function makeAdmin(m: Member) { await setRole(group.groupId, m.userId, m.role === 'admin' ? 'member' : 'admin'); await reload(); }
  async function kick(m: Member) { await kickMember(group.groupId, m.userId); await reload(); }
  async function mute(m: Member) { await muteMember(group.groupId, m.userId, 600); await reload(); }
  async function unmute(m: Member) { await unmuteMember(group.groupId, m.userId); await reload(); }

  return <div className="side-card">
    <h3>群详情</h3><p>{group.description || '暂无群简介'}</p>
    <div className="settings"><button onClick={actions.toggleLarge}>{group.groupType === 'large' ? '关闭大群' : '开启大群'}</button><button onClick={actions.toggleMuteAll}>{group.muteAll ? '解除全员禁言' : '全员禁言'}</button><button onClick={()=>actions.slow(group.slowModeSeconds ? 0 : 10)}>{group.slowModeSeconds ? '关闭慢速' : '10s 慢速'}</button><button onClick={actions.leave}>退群</button>{group.myRole==='owner' && <button className="danger" onClick={actions.dismiss}>解散</button>}</div>

    <h3>群公告</h3>
    <div className="ann-list">{announcements.map(a => <div className="ann" key={a.announcementId}><b>{a.pinned ? '📌 ' : ''}{a.title}</b><p>{a.content}</p>{canAdmin && <button className="mini-btn" onClick={()=>removeAnn(a)}>删除</button>}</div>)}</div>
    {canAdmin && <div className="ann-form"><input value={annTitle} onChange={e=>setAnnTitle(e.target.value)} placeholder="公告标题"/><textarea value={annContent} onChange={e=>setAnnContent(e.target.value)} placeholder="公告内容"/><button onClick={publishAnn}>发布公告</button></div>}

    {canAdmin && <><h3>加群审批</h3><div className="members">{joinRequests.length === 0 && <small className="muted">暂无待审批申请</small>}{joinRequests.map(r => <div className="member" key={r.requestId}><div><b>{r.nickname}</b><span>{r.reason || '无理由'}</span></div><div className="member-actions"><button onClick={()=>approve(r)}>同意</button><button onClick={()=>reject(r)}>拒绝</button></div></div>)}</div></>}

    <h3>群成员</h3><div className="members">{members.map(m => <div className="member" key={m.userId}><div><b>{m.nickname}</b><span>{m.role}</span></div>{canAdmin && m.role !== 'owner' && <div className="member-actions"><button onClick={()=>makeAdmin(m)}>{m.role==='admin'?'取消管理':'设管理'}</button><button onClick={()=>mute(m)}>禁言</button><button onClick={()=>unmute(m)}>解禁</button><button onClick={()=>kick(m)}>踢</button></div>}</div>)}</div>
  </div>;
}
