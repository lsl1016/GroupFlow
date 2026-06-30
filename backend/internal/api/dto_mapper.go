package api

import (
	"groupflow/backend/internal/domain"
	"groupflow/backend/internal/service"
)

// 本文件提供 领域模型(domain) -> 响应 DTO 的映射，集中维护转换逻辑，
// 使 handler 不直接下发 domain 对象。所有映射保持 JSON 契约不变。

// toPageDTO 将领域分页对象按元素映射函数转换为响应分页 DTO。
// p 为 nil 时返回空列表（items 为非 nil 空切片，避免前端拿到 null）。
func toPageDTO[T any, U any](p *domain.Page[T], f func(T) U) PageDTO[U] {
	if p == nil {
		return PageDTO[U]{Items: []U{}}
	}
	items := make([]U, 0, len(p.Items))
	for i := range p.Items {
		items = append(items, f(p.Items[i]))
	}
	return PageDTO[U]{Items: items, NextCursor: p.NextCursor, HasMore: p.HasMore}
}

func toUserDTO(u *domain.User) UserDTO {
	if u == nil {
		return UserDTO{}
	}
	return UserDTO{
		UserID:    u.ID,
		Username:  u.Username,
		Nickname:  u.Nickname,
		Avatar:    u.Avatar,
		Status:    u.Status,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

func toGroupDTO(g domain.Group) GroupDTO {
	return GroupDTO{
		GroupID:            g.ID,
		Name:               g.Name,
		Avatar:             g.Avatar,
		Description:        g.Description,
		OwnerID:            g.OwnerID,
		GroupType:          g.GroupType,
		JoinMode:           g.JoinMode,
		Status:             g.Status,
		MuteAll:            g.MuteAll,
		SlowModeSeconds:    g.SlowModeSeconds,
		AllowMemberInvite:  g.AllowMemberInvite,
		MentionAllRole:     g.MentionAllRole,
		MemberCount:        g.MemberCount,
		MaxMemberCount:     g.MaxMemberCount,
		MaxSequence:        g.MaxSequence,
		LastMessageID:      g.LastMessageID,
		LastMessageSummary: g.LastMessageSummary,
		LastMessageAt:      g.LastMessageAt,
		CreatedAt:          g.CreatedAt,
		UpdatedAt:          g.UpdatedAt,
	}
}

func toGroupListItemDTO(it domain.GroupListItem) GroupListItemDTO {
	return GroupListItemDTO{
		GroupDTO:           toGroupDTO(it.Group),
		MyRole:             it.MyRole,
		LastReadSequence:   it.LastReadSequence,
		UnreadCount:        it.UnreadCount,
		MentionCount:       it.MentionCount,
		MentionAllUnread:   it.MentionAllUnread,
		MentionSummaryText: it.MentionSummaryText,
	}
}

func toMemberDTO(m domain.Member) MemberDTO {
	return MemberDTO{
		ID:               m.ID,
		GroupID:          m.GroupID,
		UserID:           m.UserID,
		Username:         m.Username,
		Nickname:         m.Nickname,
		Avatar:           m.Avatar,
		Role:             m.Role,
		Status:           m.Status,
		LastReadSequence: m.LastReadSequence,
		JoinedAt:         m.JoinedAt,
		LeftAt:           m.LeftAt,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

// toMemberDTOPtr 将可空的成员指针映射为可空 DTO 指针（用于群详情中的 myMember）。
func toMemberDTOPtr(m *domain.Member) *MemberDTO {
	if m == nil {
		return nil
	}
	dto := toMemberDTO(*m)
	return &dto
}

func toMessageDTO(m domain.Message) MessageDTO {
	return MessageDTO{
		ID:              m.ID,
		MessageID:       m.MessageID,
		GroupID:         m.GroupID,
		Sequence:        m.Sequence,
		SenderID:        m.SenderID,
		SenderName:      m.SenderName,
		ClientMessageID: m.ClientMessageID,
		MessageType:     m.MessageType,
		Content:         m.Content,
		MentionAll:      m.MentionAll,
		MentionUserIDs:  m.MentionUserIDs,
		Extra:           m.Extra,
		Status:          m.Status,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

func toAnnouncementDTO(a domain.Announcement) AnnouncementDTO {
	return AnnouncementDTO{
		AnnouncementID: a.ID,
		GroupID:        a.GroupID,
		OperatorID:     a.OperatorID,
		OperatorName:   a.OperatorName,
		Title:          a.Title,
		Content:        a.Content,
		Pinned:         a.Pinned,
		Status:         a.Status,
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
	}
}

func toJoinRequestDTO(jr domain.JoinRequest) JoinRequestDTO {
	return JoinRequestDTO{
		RequestID:  jr.ID,
		GroupID:    jr.GroupID,
		UserID:     jr.UserID,
		Username:   jr.Username,
		Nickname:   jr.Nickname,
		Avatar:     jr.Avatar,
		Reason:     jr.Reason,
		Status:     jr.Status,
		OperatorID: jr.OperatorID,
		CreatedAt:  jr.CreatedAt,
		UpdatedAt:  jr.UpdatedAt,
	}
}

func toMentionDTO(m domain.Mention) MentionDTO {
	return MentionDTO{
		MentionID:   m.ID,
		GroupID:     m.GroupID,
		MessageID:   m.MessageID,
		Sequence:    m.Sequence,
		UserID:      m.UserID,
		MentionType: m.MentionType,
		ReadStatus:  m.ReadStatus,
		Content:     m.Content,
		SenderName:  m.SenderName,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

func toRecallEventDTO(e *domain.RecallEvent) RecallEventDTO {
	if e == nil {
		return RecallEventDTO{}
	}
	return RecallEventDTO{
		GroupID:    e.GroupID,
		MessageID:  e.MessageID,
		Sequence:   e.Sequence,
		OperatorID: e.OperatorID,
		SenderID:   e.SenderID,
		Reason:     e.Reason,
		RecalledAt: e.RecalledAt,
	}
}

func toJoinGroupResponse(r *service.JoinGroupResult) JoinGroupResponse {
	if r == nil {
		return JoinGroupResponse{}
	}
	resp := JoinGroupResponse{Joined: r.Joined, Pending: r.Pending}
	if r.Request != nil {
		dto := toJoinRequestDTO(*r.Request)
		resp.Request = &dto
	}
	return resp
}
