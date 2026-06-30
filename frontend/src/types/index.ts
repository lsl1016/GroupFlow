export type User = { userId:number; username:string; nickname:string; avatar:string; token?:string };
export type Role = 'owner' | 'admin' | 'member';

export type Group = {
  groupId:number; name:string; avatar:string; description:string; ownerId:number;
  groupType:'normal'|'large'; joinMode:'direct'|'approval'|'invite'; status:string;
  muteAll:boolean; slowModeSeconds:number; memberCount:number; maxMemberCount:number; maxSequence:number;
  lastMessageSummary?:string; unreadCount?:number; myRole?:Role; lastReadSequence?:number;
  mentionCount?:number; mentionAllUnread?:boolean; mentionSummaryText?:string;
};

export type Member = { id:number; groupId:number; userId:number; username:string; nickname:string; avatar:string; role:Role; status:string; lastReadSequence:number };
export type GroupMessage = { id?:number; messageId?:string; clientMessageId:string; groupId:number; sequence?:number; senderId:number; senderName:string; messageType:'text'|'system'; content:string; mentionAll?:boolean; mentionUserIds?:number[]; status:'sending'|'success'|'failed'|'normal'|'recalled'; createdAt?:string };
export type Announcement = { announcementId:number; groupId:number; operatorId:number; operatorName:string; title:string; content:string; pinned:boolean; status:string; createdAt:string; updatedAt:string };
export type JoinRequest = { requestId:number; groupId:number; userId:number; username:string; nickname:string; avatar:string; reason:string; status:'pending'|'approved'|'rejected'; operatorId?:number; createdAt:string; updatedAt:string };
export type Mention = { mentionId:number; groupId:number; messageId:string; sequence:number; userId:number; mentionType:'user'|'all'; readStatus:boolean; content:string; senderName:string; createdAt:string };
export type RecallEvent = { groupId:number; messageId:string; sequence:number; operatorId:number; senderId:number; reason?:string; recalledAt:string };
export type Page<T> = { items:T[]; nextCursor:string; hasMore:boolean };
export type ApiResp<T> = { errNo:number; errMsg:string; traceId:string; data:T };
export type WSEnvelope<T=any> = { type:string; version?:string; requestId?:string; timestamp:number; data:T };
