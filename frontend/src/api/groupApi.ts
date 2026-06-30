import { request } from './request';
import type { Announcement, Group, GroupMessage, JoinRequest, Member, Mention, Page, RecallEvent } from '../types';

export const listGroups = (cursor = '', limit = 30) => request<Page<Group>>(`/groups?cursor=${cursor}&limit=${limit}`);
export const createGroup = (body: any) => request<Group>('/groups', { method:'POST', body: JSON.stringify(body) });
export const getGroup = (groupId: number) => request<{group:Group; myMember:Member; onlineUserIds:number[]}>(`/groups/${groupId}`);
export const joinGroup = (groupId: number, reason = '') => request<{joined:boolean; pending:boolean; request?:JoinRequest}>(`/groups/${groupId}/join`, { method:'POST', body: JSON.stringify({ reason }) });
export const listMembers = (groupId:number, cursor='', limit=50, role='') => request<Page<Member>>(`/groups/${groupId}/members?cursor=${cursor}&limit=${limit}${role ? `&role=${role}` : ''}`);

export const getMessages = (groupId:number, params:{beforeSequence?:number; afterSequence?:number; limit?:number}) => {
  const qs = new URLSearchParams();
  if (params.beforeSequence) qs.set('beforeSequence', String(params.beforeSequence));
  if (params.afterSequence) qs.set('afterSequence', String(params.afterSequence));
  qs.set('limit', String(params.limit || 50));
  return request<Page<GroupMessage>>(`/groups/${groupId}/messages?${qs}`);
};
export const recallMessage = (groupId:number, messageId:string, reason='') => request<RecallEvent>(`/groups/${groupId}/messages/${messageId}/recall`, { method:'POST', body: JSON.stringify({ reason }) });
export const markRead = (groupId:number, lastReadSequence:number) => request(`/groups/${groupId}/read`, { method:'POST', body: JSON.stringify({ lastReadSequence }) });
export const updateSettings = (groupId:number, body:any) => request(`/groups/${groupId}/settings`, { method:'PATCH', body: JSON.stringify(body) });
export const setRole = (groupId:number, userId:number, role:string) => request(`/groups/${groupId}/members/${userId}/role`, { method:'POST', body: JSON.stringify({ role }) });
export const kickMember = (groupId:number, userId:number) => request(`/groups/${groupId}/members/${userId}`, { method:'DELETE' });
export const muteMember = (groupId:number, userId:number, seconds:number) => request(`/groups/${groupId}/members/${userId}/mute`, { method:'POST', body: JSON.stringify({ seconds }) });
export const unmuteMember = (groupId:number, userId:number) => request(`/groups/${groupId}/members/${userId}/mute`, { method:'DELETE' });
export const leaveGroup = (groupId:number) => request(`/groups/${groupId}/leave`, { method:'POST' });
export const dismissGroup = (groupId:number) => request(`/groups/${groupId}`, { method:'DELETE' });

export const listMentions = (groupId:number, cursor='', unreadOnly=true) => request<Page<Mention>>(`/groups/${groupId}/mentions?cursor=${cursor}&unreadOnly=${unreadOnly}`);
export const readMentions = (groupId:number, sequence=0) => request(`/groups/${groupId}/mentions/read`, { method:'POST', body: JSON.stringify({ sequence }) });

export const listAnnouncements = (groupId:number, cursor='', limit=20) => request<Page<Announcement>>(`/groups/${groupId}/announcements?cursor=${cursor}&limit=${limit}`);
export const createAnnouncement = (groupId:number, body:{title:string; content:string; pinned:boolean}) => request<Announcement>(`/groups/${groupId}/announcements`, { method:'POST', body: JSON.stringify(body) });
export const updateAnnouncement = (groupId:number, announcementId:number, body:Partial<Announcement>) => request<Announcement>(`/groups/${groupId}/announcements/${announcementId}`, { method:'PUT', body: JSON.stringify(body) });
export const deleteAnnouncement = (groupId:number, announcementId:number) => request(`/groups/${groupId}/announcements/${announcementId}`, { method:'DELETE' });

export const listJoinRequests = (groupId:number, cursor='', status='pending') => request<Page<JoinRequest>>(`/groups/${groupId}/join-requests?cursor=${cursor}&status=${status}`);
export const approveJoinRequest = (groupId:number, requestId:number) => request<JoinRequest>(`/groups/${groupId}/join-requests/${requestId}/approve`, { method:'POST' });
export const rejectJoinRequest = (groupId:number, requestId:number) => request<JoinRequest>(`/groups/${groupId}/join-requests/${requestId}/reject`, { method:'POST' });
