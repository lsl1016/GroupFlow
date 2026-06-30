import { getToken } from '../api/request';
import { newId } from '../utils/idgen';
import { useStore } from '../stores/useStore';
import type { GroupMessage, RecallEvent, WSEnvelope } from '../types';
import { getMessages } from '../api/groupApi';

class WSClient {
  private ws?: WebSocket;
  private timer?: number;
  private reconnectTimer?: number;
  private ackTimers = new Map<string, number>();
  private lastReceived: Record<number, number> = {};

  connect() {
    const token = getToken(); if (!token) return;
    // 防止重复连接：登录页与聊天页都会触发 connect，已有连接/正在连接时直接复用，避免产生多个并存 socket 与互相竞争的重连/心跳定时器。
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) return;
    useStore.getState().setConnectionStatus('connecting');
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${proto}://${location.host}/ws?token=${encodeURIComponent(token)}&deviceId=web_${newId('device')}&clientType=web&protocolVersion=v1`;
    this.ws = new WebSocket(url);
    this.ws.onopen = () => { useStore.getState().setConnectionStatus('online'); this.startHeartbeat(); this.reconnectPull(); };
    this.ws.onmessage = ev => this.handle(JSON.parse(ev.data));
    this.ws.onclose = () => this.scheduleReconnect();
    this.ws.onerror = () => this.scheduleReconnect();
  }

  private scheduleReconnect() {
    useStore.getState().setConnectionStatus('offline');
    if (this.timer) window.clearInterval(this.timer);
    if (this.reconnectTimer) return;
    this.reconnectTimer = window.setTimeout(() => { this.reconnectTimer = undefined; this.connect(); }, 1200);
  }
  private startHeartbeat() {
    if (this.timer) window.clearInterval(this.timer);
    this.timer = window.setInterval(() => this.send({ type:'ping', requestId:newId('req'), timestamp:Date.now(), data:{} }), 20000);
  }
  private send(env: WSEnvelope) { if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(env)); }

  sendText(groupId:number, content:string, opts?:{mentionAll?:boolean; mentionUserIds?:number[]}) {
    const clientMessageId = newId('client_msg');
    const user = useStore.getState().user!;
    const local: GroupMessage = { groupId, clientMessageId, senderId:user.userId, senderName:user.nickname, messageType:'text', content, mentionAll:!!opts?.mentionAll, mentionUserIds:opts?.mentionUserIds || [], status:'sending' };
    useStore.getState().appendLocalMessage(local);
    const reqId = newId('req');
    this.send({ type:'group_message_send', version:'v1', requestId:reqId, timestamp:Date.now(), data:{ groupId, clientMessageId, messageType:'text', content, mentionAll:!!opts?.mentionAll, mentionUserIds:opts?.mentionUserIds || [], extra:{} } });
    this.armAckTimeout(groupId, clientMessageId);
  }

  // retry 复用原 clientMessageId 重发失败消息（服务端按 senderId + clientMessageId 幂等），并重置状态与 ACK 超时。
  retry(groupId:number, clientMessageId:string) {
    const target = (useStore.getState().messagesByGroup[groupId] || []).find(m => m.clientMessageId === clientMessageId);
    if (!target || target.status !== 'failed') return;
    useStore.getState().ackMessage(groupId, clientMessageId, { status:'sending' });
    this.send({ type:'group_message_send', version:'v1', requestId:newId('req'), timestamp:Date.now(), data:{ groupId, clientMessageId, messageType:target.messageType, content:target.content, mentionAll:!!target.mentionAll, mentionUserIds:target.mentionUserIds || [], extra:{} } });
    this.armAckTimeout(groupId, clientMessageId);
  }

  // armAckTimeout 启动/重置某条消息的 ACK 超时计时器，超时后标记为发送失败。
  private armAckTimeout(groupId:number, clientMessageId:string) {
    const prev = this.ackTimers.get(clientMessageId); if (prev) window.clearTimeout(prev);
    const t = window.setTimeout(() => useStore.getState().ackMessage(groupId, clientMessageId, { status:'failed' }), 5000);
    this.ackTimers.set(clientMessageId, t);
  }

  // seedLastReceived 在加载历史消息后播种 lastReceived，使首条实时推送能与已加载的最大 sequence 对比、检出边界缺口。
  seedLastReceived(groupId:number, sequence:number) {
    if (sequence > (this.lastReceived[groupId] || 0)) this.lastReceived[groupId] = sequence;
  }

  markRead(groupId:number, sequence:number) {
    this.send({ type:'group_message_read', version:'v1', requestId:newId('req'), timestamp:Date.now(), data:{ groupId, lastReadSequence:sequence } });
  }

  private handle(env: WSEnvelope) {
    if (env.type === 'group_message_ack') {
      const d = env.data as any;
      const t = this.ackTimers.get(d.clientMessageId); if (t) window.clearTimeout(t);
      useStore.getState().ackMessage(d.groupId, d.clientMessageId, { messageId:d.messageId, sequence:d.sequence, createdAt:d.createdAt });
    }
    if (env.type === 'group_message_receive') {
      const m = env.data as GroupMessage;
      const last = this.lastReceived[m.groupId] || 0;
      // 出现 sequence 缺口时立即 afterSequence 补拉，这是大群异步投递失败的兜底路径。
      if (m.sequence && last && m.sequence > last + 1) this.pullAfter(m.groupId, last);
      if (m.sequence) this.lastReceived[m.groupId] = Math.max(last, m.sequence);
      useStore.getState().receiveMessage(m);
    }
    if (env.type === 'group_message_recalled') {
      useStore.getState().recallMessage(env.data as RecallEvent);
    }
    if (env.type === 'group_message_failed') {
      console.warn('message failed', env.data);
    }
    if (env.type === 'group_join_request_approved' || env.type === 'group_join_request_rejected') {
      console.info('join request result', env.data);
    }
  }

  private async pullAfter(groupId:number, afterSequence:number) {
    const page = await getMessages(groupId, { afterSequence, limit:100 });
    useStore.getState().mergeMessages(groupId, page.items);
    const max = Math.max(afterSequence, ...page.items.map(x => x.sequence || 0));
    this.lastReceived[groupId] = max;
  }
  private reconnectPull() {
    const store = useStore.getState();
    for (const group of store.groups) {
      const messages = store.messagesByGroup[group.groupId] || [];
      const last = Math.max(0, ...messages.map(m => m.sequence || 0));
      if (last) this.pullAfter(group.groupId, last);
    }
  }
}
export const wsClient = new WSClient();
