import { create } from 'zustand';
import type { User, Group, GroupMessage, Member, RecallEvent } from '../types';

type State = {
  user?: User;
  groups: Group[];
  currentGroup?: Group;
  members: Member[];
  messagesByGroup: Record<number, GroupMessage[]>;
  connectionStatus: 'offline' | 'connecting' | 'online';
  setUser: (u?: User) => void;
  setGroups: (groups: Group[]) => void;
  upsertGroup: (g: Group) => void;
  setCurrentGroup: (g?: Group) => void;
  setMembers: (m: Member[]) => void;
  setConnectionStatus: (s: State['connectionStatus']) => void;
  appendLocalMessage: (m: GroupMessage) => void;
  ackMessage: (groupId:number, clientMessageId:string, patch: Partial<GroupMessage>) => void;
  receiveMessage: (m: GroupMessage) => void;
  recallMessage: (evt: RecallEvent) => void;
  setMessages: (groupId:number, messages: GroupMessage[]) => void;
  mergeMessages: (groupId:number, messages: GroupMessage[]) => void;
};

function sortAndDedup(items: GroupMessage[]) {
  const byKey = new Map<string, GroupMessage>();
  for (const m of items) {
    const key = m.messageId || m.clientMessageId;
    byKey.set(key, { ...(byKey.get(key) || {}), ...m });
  }
  return [...byKey.values()].sort((a,b) => (a.sequence || Number.MAX_SAFE_INTEGER) - (b.sequence || Number.MAX_SAFE_INTEGER));
}

export const useStore = create<State>((set, get) => ({
  groups: [], members: [], messagesByGroup: {}, connectionStatus:'offline',
  setUser: user => set({ user }),
  setGroups: groups => set({ groups }),
  upsertGroup: g => set({ groups: get().groups.some(x=>x.groupId===g.groupId) ? get().groups.map(x=>x.groupId===g.groupId ? { ...x, ...g } : x) : [g, ...get().groups] }),
  setCurrentGroup: currentGroup => set({ currentGroup }),
  setMembers: members => set({ members }),
  setConnectionStatus: connectionStatus => set({ connectionStatus }),
  appendLocalMessage: m => set({ messagesByGroup: { ...get().messagesByGroup, [m.groupId]: sortAndDedup([...(get().messagesByGroup[m.groupId] || []), m]) } }),
  ackMessage: (groupId, clientMessageId, patch) => set({ messagesByGroup: { ...get().messagesByGroup, [groupId]: sortAndDedup((get().messagesByGroup[groupId] || []).map(m => m.clientMessageId === clientMessageId ? { ...m, ...patch, status: patch.status || 'success' } : m)) } }),
  receiveMessage: m => set({ messagesByGroup: { ...get().messagesByGroup, [m.groupId]: sortAndDedup([...(get().messagesByGroup[m.groupId] || []), { ...m, status:m.status === 'recalled' ? 'recalled' : 'success' }]) } }),
  recallMessage: evt => set({ messagesByGroup: { ...get().messagesByGroup, [evt.groupId]: (get().messagesByGroup[evt.groupId] || []).map(m => m.messageId === evt.messageId ? { ...m, status:'recalled' } : m) } }),
  setMessages: (groupId, messages) => set({ messagesByGroup: { ...get().messagesByGroup, [groupId]: sortAndDedup(messages) } }),
  mergeMessages: (groupId, messages) => set({ messagesByGroup: { ...get().messagesByGroup, [groupId]: sortAndDedup([...(get().messagesByGroup[groupId] || []), ...messages]) } })
}));
