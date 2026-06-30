package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
	"groupflow/backend/internal/infra"
	"groupflow/backend/internal/repo"
	"groupflow/backend/pkg/auth"
	"groupflow/backend/pkg/id"
	"groupflow/backend/pkg/logx"
)

var (
	ErrForbidden       = errors.New("forbidden")
	ErrNotFound        = errors.New("not found")
	ErrMuted           = errors.New("muted")
	ErrRateLimited     = errors.New("rate limited")
	ErrGroupDismissed  = errors.New("group dismissed")
	ErrApprovalPending = errors.New("join request pending")
)

type Service struct {
	Cfg      config.Config
	Repo     *repo.Repository
	Redis    *redis.Client
	Producer infra.KafkaProducer
	Log      *zap.Logger
}

func New(cfg config.Config, r *repo.Repository, redis *redis.Client, producer infra.KafkaProducer, log *zap.Logger) *Service {
	return &Service{Cfg: cfg, Repo: r, Redis: redis, Producer: producer, Log: log}
}

// opErr 记录业务操作失败日志：op 标识操作语义，附带业务字段便于排查，最终原样返回 err。
// 业务校验类错误（forbidden/muted/限频等）属预期分支，调用方可按需跳过传入。
func opErr(ctx context.Context, op string, err error, fields ...zap.Field) error {
	logx.From(ctx).Error("service_error", append([]zap.Field{
		zap.String("event", "service_error"), zap.String("op", op), zap.Error(err),
	}, fields...)...)
	return err
}

func (s *Service) Login(ctx context.Context, username string) (*domain.User, string, error) {
	if username == "" {
		return nil, "", errors.New("username required")
	}
	u, err := s.Repo.LoginOrCreateUser(ctx, username)
	if err != nil {
		return nil, "", opErr(ctx, "login_or_create_user", err, zap.String("username", username))
	}
	token, err := auth.Sign(s.Cfg.AuthSecret, u.ID, u.Username, s.Cfg.TokenTTL)
	if err != nil {
		return nil, "", opErr(ctx, "login_sign_token", err, zap.Int64("userId", u.ID))
	}
	logx.From(ctx).Info("user_login", zap.String("event", "user_login"), zap.Int64("userId", u.ID), zap.String("username", u.Username))
	return u, token, nil
}

func (s *Service) CreateGroup(ctx context.Context, ownerID int64, name, desc, avatar, joinMode, groupType string, maxMemberCount, slowMode int) (*domain.Group, *domain.Message, error) {
	if name == "" {
		return nil, nil, errors.New("group name required")
	}
	owner, err := s.Repo.GetUser(ctx, ownerID)
	if err != nil {
		return nil, nil, opErr(ctx, "create_group_get_owner", err, zap.Int64("ownerId", ownerID))
	}
	g, err := s.Repo.CreateGroup(ctx, owner, name, desc, avatar, joinMode, groupType, maxMemberCount, slowMode)
	if err != nil {
		return nil, nil, opErr(ctx, "create_group", err, zap.Int64("ownerId", ownerID), zap.String("name", name))
	}
	logx.From(ctx).Info("group_created", zap.String("event", "group_created"), zap.Int64("groupId", g.ID), zap.Int64("ownerId", ownerID), zap.String("groupType", g.GroupType))
	msg, _ := s.createSystemMessage(ctx, g.ID, ownerID, fmt.Sprintf("%s 创建了群聊", owner.Nickname), map[string]any{"event": "group_created"})
	return g, msg, nil
}

type JoinGroupResult struct {
	Joined  bool                `json:"joined"`
	Pending bool                `json:"pending"`
	Request *domain.JoinRequest `json:"request,omitempty"`
	Message *domain.Message     `json:"-"`
}

func (s *Service) JoinGroup(ctx context.Context, groupID, userID int64, reason string) (*JoinGroupResult, error) {
	g, err := s.Repo.GetGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g.Status != domain.StatusNormal {
		return nil, ErrGroupDismissed
	}
	if g.MemberCount >= g.MaxMemberCount {
		return nil, errors.New("group full")
	}
	if s.Repo.IsActiveMember(ctx, groupID, userID) {
		return &JoinGroupResult{Joined: true}, nil
	}
	if g.JoinMode == domain.JoinModeApproval {
		jr, err := s.Repo.CreateJoinRequest(ctx, groupID, userID, reason)
		if err != nil {
			return nil, opErr(ctx, "join_group_create_request", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
		}
		logx.From(ctx).Info("join_request_submitted", zap.String("event", "join_request_submitted"), zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.Int64("requestId", jr.ID))
		return &JoinGroupResult{Pending: true, Request: jr}, nil
	}
	msg, err := s.addMemberAndSystemMessage(ctx, groupID, userID)
	if err != nil {
		return nil, opErr(ctx, "join_group_add_member", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	logx.From(ctx).Info("group_joined", zap.String("event", "group_joined"), zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	return &JoinGroupResult{Joined: true, Message: msg}, nil
}

func (s *Service) ApproveJoinRequest(ctx context.Context, groupID, operatorID, requestID int64) (*domain.JoinRequest, *domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "approve_join"); err != nil {
		return nil, nil, err
	}
	jr, err := s.Repo.ApproveJoinRequest(ctx, groupID, requestID, operatorID)
	if err != nil {
		return nil, nil, opErr(ctx, "approve_join_request", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID), zap.Int64("operatorId", operatorID))
	}
	logx.From(ctx).Info("join_request_approved", zap.String("event", "join_request_approved"), zap.Int64("groupId", groupID), zap.Int64("requestId", requestID), zap.Int64("userId", jr.UserID), zap.Int64("operatorId", operatorID))
	msg, _ := s.createSystemMessage(ctx, groupID, operatorID, fmt.Sprintf("%s 加入群聊", jr.Nickname), map[string]any{"event": "member_joined", "userId": jr.UserID})
	return jr, msg, nil
}

func (s *Service) RejectJoinRequest(ctx context.Context, groupID, operatorID, requestID int64) (*domain.JoinRequest, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "approve_join"); err != nil {
		return nil, err
	}
	jr, err := s.Repo.RejectJoinRequest(ctx, groupID, requestID, operatorID)
	if err != nil {
		return nil, opErr(ctx, "reject_join_request", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID), zap.Int64("operatorId", operatorID))
	}
	logx.From(ctx).Info("join_request_rejected", zap.String("event", "join_request_rejected"), zap.Int64("groupId", groupID), zap.Int64("requestId", requestID), zap.Int64("operatorId", operatorID))
	return jr, nil
}

func (s *Service) addMemberAndSystemMessage(ctx context.Context, groupID, userID int64) (*domain.Message, error) {
	if err := s.Repo.AddMember(ctx, groupID, userID, domain.RoleMember); err != nil {
		return nil, opErr(ctx, "add_member", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	u, _ := s.Repo.GetUser(ctx, userID)
	name := fmt.Sprintf("用户 %d", userID)
	if u != nil {
		name = u.Nickname
	}
	return s.createSystemMessage(ctx, groupID, userID, fmt.Sprintf("%s 加入群聊", name), map[string]any{"event": "member_joined", "userId": userID})
}

func (s *Service) LeaveGroup(ctx context.Context, groupID, userID int64) (*domain.Message, error) {
	m, err := s.Repo.GetMember(ctx, groupID, userID)
	if err != nil {
		return nil, err
	}
	if m.Role == domain.RoleOwner {
		return nil, errors.New("owner cannot leave, dismiss group instead")
	}
	if err := s.Repo.MarkMemberStatus(ctx, groupID, userID, domain.MemberLeft); err != nil {
		return nil, opErr(ctx, "leave_group", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	logx.From(ctx).Info("group_left", zap.String("event", "group_left"), zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	return s.createSystemMessage(ctx, groupID, userID, fmt.Sprintf("%s 退出群聊", m.Nickname), map[string]any{"event": "member_left", "userId": userID})
}

func (s *Service) DismissGroup(ctx context.Context, groupID, operatorID int64) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "dismiss_group"); err != nil {
		return nil, err
	}
	if err := s.Repo.DismissGroup(ctx, groupID); err != nil {
		return nil, opErr(ctx, "dismiss_group", err, zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID))
	}
	logx.From(ctx).Info("group_dismissed", zap.String("event", "group_dismissed"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID))
	return s.createSystemMessage(ctx, groupID, operatorID, "群聊已解散", map[string]any{"event": "group_dismissed"})
}

func (s *Service) UpdateSettings(ctx context.Context, groupID, operatorID int64, muteAll *bool, slowMode *int, groupType *string, maxMemberCount *int) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "update_settings"); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if muteAll != nil {
		fields["mute_all"] = boolToInt(*muteAll)
	}
	if slowMode != nil {
		if *slowMode < 0 {
			return nil, errors.New("slow mode invalid")
		}
		fields["slow_mode_seconds"] = *slowMode
	}
	if groupType != nil {
		if *groupType != domain.GroupNormal && *groupType != domain.GroupLarge {
			return nil, errors.New("invalid group type")
		}
		fields["group_type"] = *groupType
	}
	if maxMemberCount != nil {
		fields["max_member_count"] = *maxMemberCount
	}
	if len(fields) == 0 {
		return nil, nil
	}
	if err := s.Repo.UpdateGroupSettings(ctx, groupID, fields); err != nil {
		return nil, opErr(ctx, "update_settings", err, zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID))
	}
	logx.From(ctx).Info("group_settings_updated", zap.String("event", "group_settings_updated"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int("fieldCount", len(fields)))
	return s.createSystemMessage(ctx, groupID, operatorID, "群设置已更新", map[string]any{"event": "group_settings_updated", "fields": fields})
}

func (s *Service) SetMemberRole(ctx context.Context, groupID, operatorID, targetUserID int64, role string) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "set_role"); err != nil {
		return nil, err
	}
	if role != domain.RoleAdmin && role != domain.RoleMember {
		return nil, errors.New("role must be admin or member")
	}
	if err := s.Repo.UpdateMemberRole(ctx, groupID, targetUserID, role); err != nil {
		return nil, opErr(ctx, "set_member_role", err, zap.Int64("groupId", groupID), zap.Int64("targetUserId", targetUserID), zap.String("role", role))
	}
	logx.From(ctx).Info("member_role_changed", zap.String("event", "member_role_changed"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("targetUserId", targetUserID), zap.String("role", role))
	return s.createSystemMessage(ctx, groupID, operatorID, fmt.Sprintf("成员 %d 角色已变更为 %s", targetUserID, role), map[string]any{"event": "member_role_changed", "userId": targetUserID, "role": role})
}

func (s *Service) KickMember(ctx context.Context, groupID, operatorID, targetUserID int64) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "kick_member"); err != nil {
		return nil, err
	}
	target, err := s.Repo.GetMember(ctx, groupID, targetUserID)
	if err != nil {
		return nil, opErr(ctx, "kick_member_get_target", err, zap.Int64("groupId", groupID), zap.Int64("targetUserId", targetUserID))
	}
	if target.Role == domain.RoleOwner {
		return nil, ErrForbidden
	}
	if err := s.Repo.MarkMemberStatus(ctx, groupID, targetUserID, domain.MemberKicked); err != nil {
		return nil, opErr(ctx, "kick_member", err, zap.Int64("groupId", groupID), zap.Int64("targetUserId", targetUserID))
	}
	logx.From(ctx).Info("member_kicked", zap.String("event", "member_kicked"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("targetUserId", targetUserID))
	return s.createSystemMessage(ctx, groupID, operatorID, fmt.Sprintf("%s 被移出群聊", target.Nickname), map[string]any{"event": "member_kicked", "userId": targetUserID})
}

func (s *Service) MuteMember(ctx context.Context, groupID, operatorID, targetUserID int64, seconds int, reason string) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "mute_member"); err != nil {
		return nil, err
	}
	var expire *time.Time
	if seconds > 0 {
		t := time.Now().Add(time.Duration(seconds) * time.Second)
		expire = &t
	}
	if err := s.Repo.UpsertMute(ctx, groupID, targetUserID, operatorID, reason, expire); err != nil {
		return nil, opErr(ctx, "mute_member", err, zap.Int64("groupId", groupID), zap.Int64("targetUserId", targetUserID), zap.Int("seconds", seconds))
	}
	logx.From(ctx).Info("member_muted", zap.String("event", "member_muted"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("targetUserId", targetUserID), zap.Int("seconds", seconds))
	return s.createSystemMessage(ctx, groupID, operatorID, fmt.Sprintf("成员 %d 已被禁言", targetUserID), map[string]any{"event": "member_muted", "userId": targetUserID, "seconds": seconds})
}

func (s *Service) UnmuteMember(ctx context.Context, groupID, operatorID, targetUserID int64) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "mute_member"); err != nil {
		return nil, err
	}
	if err := s.Repo.CancelMute(ctx, groupID, targetUserID); err != nil {
		return nil, opErr(ctx, "unmute_member", err, zap.Int64("groupId", groupID), zap.Int64("targetUserId", targetUserID))
	}
	logx.From(ctx).Info("member_unmuted", zap.String("event", "member_unmuted"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("targetUserId", targetUserID))
	return s.createSystemMessage(ctx, groupID, operatorID, fmt.Sprintf("成员 %d 已解除禁言", targetUserID), map[string]any{"event": "member_unmuted", "userId": targetUserID})
}

func (s *Service) CreateAnnouncement(ctx context.Context, groupID, operatorID int64, title, content string, pinned bool) (*domain.Announcement, *domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "announcement_manage"); err != nil {
		return nil, nil, err
	}
	if title == "" || content == "" {
		return nil, nil, errors.New("title and content required")
	}
	a, err := s.Repo.CreateAnnouncement(ctx, groupID, operatorID, title, content, pinned)
	if err != nil {
		return nil, nil, opErr(ctx, "create_announcement", err, zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID))
	}
	logx.From(ctx).Info("announcement_created", zap.String("event", "announcement_created"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("announcementId", a.ID))
	msg, _ := s.createSystemMessage(ctx, groupID, operatorID, "发布了新的群公告："+title, map[string]any{"event": "announcement_created", "announcementId": a.ID})
	return a, msg, nil
}

func (s *Service) UpdateAnnouncement(ctx context.Context, groupID, operatorID, announcementID int64, title, content string, pinned *bool) (*domain.Announcement, *domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "announcement_manage"); err != nil {
		return nil, nil, err
	}
	a, err := s.Repo.UpdateAnnouncement(ctx, groupID, announcementID, operatorID, title, content, pinned)
	if err != nil {
		return nil, nil, opErr(ctx, "update_announcement", err, zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("announcementId", announcementID))
	}
	logx.From(ctx).Info("announcement_updated", zap.String("event", "announcement_updated"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("announcementId", a.ID))
	msg, _ := s.createSystemMessage(ctx, groupID, operatorID, "群公告已更新："+a.Title, map[string]any{"event": "announcement_updated", "announcementId": a.ID})
	return a, msg, nil
}

func (s *Service) DeleteAnnouncement(ctx context.Context, groupID, operatorID, announcementID int64) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "announcement_manage"); err != nil {
		return nil, err
	}
	if err := s.Repo.DeleteAnnouncement(ctx, groupID, announcementID, operatorID); err != nil {
		return nil, opErr(ctx, "delete_announcement", err, zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("announcementId", announcementID))
	}
	logx.From(ctx).Info("announcement_deleted", zap.String("event", "announcement_deleted"), zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.Int64("announcementId", announcementID))
	return s.createSystemMessage(ctx, groupID, operatorID, "群公告已删除", map[string]any{"event": "announcement_deleted", "announcementId": announcementID})
}

type SendMessageInput struct {
	GroupID         int64
	SenderID        int64
	ClientMessageID string
	MessageType     string
	Content         string
	MentionAll      bool
	MentionUserIDs  []int64
	Extra           map[string]any
}

func (s *Service) SendGroupMessage(ctx context.Context, in SendMessageInput) (*domain.Message, bool, error) {
	start := time.Now()
	if in.ClientMessageID == "" {
		return nil, false, errors.New("clientMessageId required")
	}
	if in.MessageType == "" {
		in.MessageType = domain.MessageText
	}
	if in.MessageType != domain.MessageText {
		return nil, false, errors.New("only text message is allowed in MVP")
	}
	if in.Content == "" {
		return nil, false, errors.New("content required")
	}
	// clientMessageId 是发送幂等入口：客户端重试必须复用同一个 ID，服务端查到旧消息就直接返回旧 ACK。
	if existing, err := s.Repo.FindMessageByClientID(ctx, in.SenderID, in.ClientMessageID); err == nil {
		logx.From(ctx).Info("group_message_idempotent_hit",
			zap.String("event", "group_message_idempotent_hit"), zap.Int64("groupId", in.GroupID),
			zap.String("messageId", existing.MessageID), zap.String("clientMessageId", in.ClientMessageID),
			zap.Int64("sequence", existing.Sequence))
		return existing, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	g, err := s.Repo.GetGroup(ctx, in.GroupID)
	if err != nil {
		return nil, false, opErr(ctx, "send_message_get_group", err, zap.Int64("groupId", in.GroupID), zap.Int64("senderId", in.SenderID))
	}
	if g.Status != domain.StatusNormal {
		return nil, false, ErrGroupDismissed
	}
	member, err := s.Repo.GetMember(ctx, in.GroupID, in.SenderID)
	if err != nil {
		return nil, false, opErr(ctx, "send_message_get_member", err, zap.Int64("groupId", in.GroupID), zap.Int64("senderId", in.SenderID))
	}
	if member.Status != domain.StatusNormal {
		return nil, false, ErrForbidden
	}
	if g.MuteAll && member.Role == domain.RoleMember {
		return nil, false, ErrMuted
	}
	muted, err := s.Repo.IsMuted(ctx, in.GroupID, in.SenderID)
	if err != nil {
		return nil, false, opErr(ctx, "send_message_check_muted", err, zap.Int64("groupId", in.GroupID), zap.Int64("senderId", in.SenderID))
	}
	if muted {
		return nil, false, ErrMuted
	}
	if in.MentionAll {
		if err := s.checkMentionAll(ctx, g, member); err != nil {
			return nil, false, err
		}
	}
	if err := s.checkSlowMode(ctx, g, member); err != nil {
		return nil, false, err
	}
	seq, err := s.nextSequence(ctx, in.GroupID)
	if err != nil {
		return nil, false, opErr(ctx, "send_message_next_sequence", err, zap.Int64("groupId", in.GroupID), zap.Int64("senderId", in.SenderID))
	}
	msg := &domain.Message{MessageID: id.New("msg"), GroupID: in.GroupID, Sequence: seq, SenderID: in.SenderID, SenderName: member.Nickname, ClientMessageID: in.ClientMessageID, MessageType: in.MessageType, Content: in.Content, MentionAll: in.MentionAll, MentionUserIDs: in.MentionUserIDs, Extra: in.Extra, Status: domain.StatusNormal}
	if err := s.Repo.InsertMessage(ctx, msg); err != nil {
		if existing, qerr := s.Repo.FindMessageByClientID(ctx, in.SenderID, in.ClientMessageID); qerr == nil {
			return existing, true, nil
		}
		return nil, false, opErr(ctx, "send_message_insert", err, zap.Int64("groupId", in.GroupID), zap.Int64("senderId", in.SenderID), zap.String("clientMessageId", in.ClientMessageID))
	}
	s.setMaxSequence(ctx, in.GroupID, seq)
	if err := s.PublishEvent(ctx, "group_message_created", msg.GroupID, g.GroupType, msg.MessageID, msg.Sequence, msg.SenderID, msg); err != nil {
		logx.From(ctx).Warn("kafka_publish_failed", zap.String("event", "kafka_publish_failed"), zap.Error(err), zap.Int64("groupId", in.GroupID), zap.String("messageId", msg.MessageID))
	}
	preview, contentLen := logx.ContentPreview(msg.Content)
	logx.From(ctx).Info("group_message_send_success",
		zap.String("event", "group_message_send_success"), zap.Int64("groupId", msg.GroupID),
		zap.String("messageId", msg.MessageID), zap.String("clientMessageId", msg.ClientMessageID),
		zap.String("messageType", msg.MessageType), zap.Int64("sequence", msg.Sequence),
		zap.Int("contentLength", contentLen), zap.String("contentPreview", preview),
		zap.Int64("durationMs", time.Since(start).Milliseconds()))
	return msg, false, nil
}

func (s *Service) RecallMessage(ctx context.Context, groupID, operatorID int64, messageID, reason string) (*domain.RecallEvent, error) {
	msg, err := s.Repo.FindMessageByID(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if msg.GroupID != groupID || msg.Status == domain.StatusRecalled {
		return nil, ErrNotFound
	}
	if msg.MessageType == domain.MessageSystem {
		return nil, errors.New("system message cannot be recalled")
	}
	m, err := s.Repo.GetMember(ctx, groupID, operatorID)
	if err != nil || m.Status != domain.StatusNormal {
		return nil, ErrForbidden
	}
	if msg.SenderID != operatorID && m.Role != domain.RoleOwner && m.Role != domain.RoleAdmin {
		return nil, ErrForbidden
	}
	evt, err := s.Repo.RecallMessage(ctx, msg, operatorID, reason)
	if err != nil {
		return nil, opErr(ctx, "recall_message", err, zap.Int64("groupId", groupID), zap.String("messageId", messageID), zap.Int64("operatorId", operatorID))
	}
	logx.From(ctx).Info("message_recalled", zap.String("event", "message_recalled"), zap.Int64("groupId", groupID), zap.String("messageId", msg.MessageID), zap.Int64("sequence", msg.Sequence), zap.Int64("operatorId", operatorID))
	g, _ := s.Repo.GetGroup(ctx, groupID)
	gt := domain.GroupNormal
	if g != nil {
		gt = g.GroupType
	}
	_ = s.PublishEvent(ctx, "group_message_recalled", groupID, gt, msg.MessageID, msg.Sequence, operatorID, evt)
	return evt, nil
}

func (s *Service) createSystemMessage(ctx context.Context, groupID, operatorID int64, content string, extra map[string]any) (*domain.Message, error) {
	seq, err := s.nextSequence(ctx, groupID)
	if err != nil {
		return nil, opErr(ctx, "system_message_next_sequence", err, zap.Int64("groupId", groupID))
	}
	msg := &domain.Message{MessageID: id.New("msg"), GroupID: groupID, Sequence: seq, SenderID: 0, SenderName: "系统", ClientMessageID: id.New("system"), MessageType: domain.MessageSystem, Content: content, Extra: extra, Status: domain.StatusNormal}
	if err := s.Repo.InsertMessage(ctx, msg); err != nil {
		return nil, opErr(ctx, "system_message_insert", err, zap.Int64("groupId", groupID), zap.String("messageId", msg.MessageID))
	}
	s.setMaxSequence(ctx, groupID, seq)
	g, _ := s.Repo.GetGroup(ctx, groupID)
	gt := domain.GroupNormal
	if g != nil {
		gt = g.GroupType
	}
	_ = s.PublishEvent(ctx, "group_message_created", msg.GroupID, gt, msg.MessageID, msg.Sequence, msg.SenderID, msg)
	s.Repo.CreateOperationLog(ctx, groupID, operatorID, "system_message", extra)
	return msg, nil
}

func (s *Service) nextSequence(ctx context.Context, groupID int64) (int64, error) {
	key := fmt.Sprintf("group:%d:sequence", groupID)
	if s.Redis != nil {
		ok, _ := s.Redis.SetNX(ctx, key, 0, 0).Result()
		if ok {
			max, _ := s.Repo.MaxSequence(ctx, groupID)
			_ = s.Redis.Set(ctx, key, max, 0).Err()
		}
		// Redis INCR 是群内 sequence 的高频路径；MySQL MAX(sequence) 只作为 Redis 故障时的降级兜底。
		incrStart := time.Now()
		seq, err := s.Redis.Incr(ctx, key).Result()
		if err == nil {
			if ms := time.Since(incrStart).Milliseconds(); ms >= logx.RedisSlowMs() {
				logx.From(ctx).Warn("redis_sequence_slow",
					zap.String("event", "redis_sequence_slow"), zap.Int64("groupId", groupID),
					zap.String("key", key), zap.String("operation", "INCR"), zap.Int64("durationMs", ms))
			}
			return seq, nil
		}
		logx.From(ctx).Error("redis_operation_failed",
			zap.String("event", "redis_operation_failed"), zap.Int64("groupId", groupID),
			zap.String("key", key), zap.String("operation", "INCR"), zap.Error(err))
	}
	max, err := s.Repo.MaxSequence(ctx, groupID)
	if err != nil {
		return 0, err
	}
	return max + 1, nil
}

func (s *Service) setMaxSequence(ctx context.Context, groupID, seq int64) {
	if s.Redis != nil {
		_ = s.Redis.Set(ctx, fmt.Sprintf("group:%d:max_sequence", groupID), seq, 0).Err()
	}
}

func (s *Service) checkSlowMode(ctx context.Context, g *domain.Group, m *domain.Member) error {
	if g.SlowModeSeconds <= 0 || m.Role != domain.RoleMember || s.Redis == nil {
		return nil
	}
	key := fmt.Sprintf("rate_limit:group:%d:user:%d", g.ID, m.UserID)
	ok, err := s.Redis.SetNX(ctx, key, "1", time.Duration(g.SlowModeSeconds)*time.Second).Result()
	if err != nil {
		return nil
	}
	if !ok {
		return ErrRateLimited
	}
	return nil
}

func (s *Service) checkMentionAll(ctx context.Context, g *domain.Group, m *domain.Member) error {
	if g.MentionAllRole == "disabled" {
		return ErrForbidden
	}
	if g.MentionAllRole == domain.RoleOwner && m.Role != domain.RoleOwner {
		return ErrForbidden
	}
	if g.MentionAllRole == domain.RoleAdmin && m.Role != domain.RoleOwner && m.Role != domain.RoleAdmin {
		return ErrForbidden
	}
	if s.Redis != nil {
		seconds := 60
		if g.GroupType == domain.GroupLarge {
			seconds = 300
		}
		// @所有人不展开提醒记录，只用 Redis 做群维度限频，保护大群。
		key := fmt.Sprintf("rate_limit:mention_all:group:%d", g.ID)
		ok, err := s.Redis.SetNX(ctx, key, "1", time.Duration(seconds)*time.Second).Result()
		if err == nil && !ok {
			return ErrRateLimited
		}
	}
	return nil
}

func (s *Service) PublishEvent(ctx context.Context, eventType string, groupID int64, groupType, messageID string, sequence int64, operatorID int64, payload any) error {
	if s.Producer == nil {
		return nil
	}
	eventID := id.New("evt")
	traceID := logx.TraceIDFrom(ctx)
	event := map[string]any{"eventId": eventID, "eventType": eventType, "traceId": traceID, "groupId": groupID, "groupType": groupType, "messageId": messageID, "sequence": sequence, "senderId": operatorID, "occurredAt": time.Now().Format(time.RFC3339Nano), "payload": payload}
	start := time.Now()
	err := s.Producer.Produce(ctx, strconv.FormatInt(groupID, 10), event)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		logx.From(ctx).Error("kafka_produce_failed",
			zap.String("event", "kafka_produce_failed"), zap.String("topic", s.Cfg.KafkaTopic),
			zap.String("eventId", eventID), zap.String("eventType", eventType), zap.Int64("groupId", groupID),
			zap.String("messageId", messageID), zap.Int64("sequence", sequence), zap.Int64("durationMs", ms), zap.Error(err))
		return err
	}
	if s.Cfg.KafkaEnabled {
		logx.From(ctx).Info("kafka_produce_success",
			zap.String("event", "kafka_produce_success"), zap.String("topic", s.Cfg.KafkaTopic),
			zap.String("eventId", eventID), zap.String("eventType", eventType), zap.Int64("groupId", groupID),
			zap.String("messageId", messageID), zap.Int64("sequence", sequence), zap.Int64("durationMs", ms))
	}
	return nil
}

func (s *Service) requireRole(ctx context.Context, groupID, userID int64, action string) error {
	m, err := s.Repo.GetMember(ctx, groupID, userID)
	if err != nil || m.Status != domain.StatusNormal {
		logx.From(ctx).Warn("permission_denied", zap.String("event", "permission_denied"), zap.String("action", action), zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.String("reason", "not an active member"))
		return ErrForbidden
	}
	denied := false
	switch action {
	case "dismiss_group", "set_role":
		denied = m.Role != domain.RoleOwner
	case "update_settings", "kick_member", "mute_member", "announcement_manage", "approve_join":
		denied = m.Role != domain.RoleOwner && m.Role != domain.RoleAdmin
	default:
		denied = true
	}
	if denied {
		logx.From(ctx).Warn("permission_denied", zap.String("event", "permission_denied"), zap.String("action", action), zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.String("role", m.Role))
		return ErrForbidden
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
