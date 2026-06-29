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
)

var (
	ErrForbidden      = errors.New("forbidden")
	ErrNotFound       = errors.New("not found")
	ErrMuted          = errors.New("muted")
	ErrRateLimited    = errors.New("rate limited")
	ErrGroupDismissed = errors.New("group dismissed")
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

func (s *Service) Login(ctx context.Context, username string) (*domain.User, string, error) {
	if username == "" {
		return nil, "", errors.New("username required")
	}
	u, err := s.Repo.LoginOrCreateUser(ctx, username)
	if err != nil {
		return nil, "", err
	}
	token, err := auth.Sign(s.Cfg.AuthSecret, u.ID, u.Username, s.Cfg.TokenTTL)
	return u, token, err
}

func (s *Service) CreateGroup(ctx context.Context, ownerID int64, name, desc, avatar, joinMode, groupType string, maxMemberCount, slowMode int) (*domain.Group, *domain.Message, error) {
	if name == "" {
		return nil, nil, errors.New("group name required")
	}
	owner, err := s.Repo.GetUser(ctx, ownerID)
	if err != nil {
		return nil, nil, err
	}
	g, err := s.Repo.CreateGroup(ctx, owner, name, desc, avatar, joinMode, groupType, maxMemberCount, slowMode)
	if err != nil {
		return nil, nil, err
	}
	msg, _ := s.createSystemMessage(ctx, g.ID, ownerID, fmt.Sprintf("%s 创建了群聊", owner.Nickname), map[string]any{"event": "group_created"})
	return g, msg, nil
}

func (s *Service) JoinGroup(ctx context.Context, groupID, userID int64) (*domain.Message, error) {
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
	if err := s.Repo.AddMember(ctx, groupID, userID, domain.RoleMember); err != nil {
		return nil, err
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
		return nil, err
	}
	return s.createSystemMessage(ctx, groupID, userID, fmt.Sprintf("%s 退出群聊", m.Nickname), map[string]any{"event": "member_left", "userId": userID})
}

func (s *Service) DismissGroup(ctx context.Context, groupID, operatorID int64) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "dismiss_group"); err != nil {
		return nil, err
	}
	if err := s.Repo.DismissGroup(ctx, groupID); err != nil {
		return nil, err
	}
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
		return nil, err
	}
	return s.createSystemMessage(ctx, groupID, operatorID, "群设置已更新", map[string]any{"event": "group_settings_updated", "fields": fields})
}

// SetMemberRole 设置成员角色
func (s *Service) SetMemberRole(ctx context.Context, groupID, operatorID, targetUserID int64, role string) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "set_role"); err != nil {
		return nil, err
	}
	if role != domain.RoleAdmin && role != domain.RoleMember {
		return nil, errors.New("role must be admin or member")
	}
	if err := s.Repo.UpdateMemberRole(ctx, groupID, targetUserID, role); err != nil {
		return nil, err
	}
	return s.createSystemMessage(ctx, groupID, operatorID, fmt.Sprintf("成员 %d 角色已变更为 %s", targetUserID, role), map[string]any{"event": "member_role_changed", "userId": targetUserID, "role": role})
}

// KickMember 移除成员
func (s *Service) KickMember(ctx context.Context, groupID, operatorID, targetUserID int64) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "kick_member"); err != nil {
		return nil, err
	}
	target, err := s.Repo.GetMember(ctx, groupID, targetUserID)
	if err != nil {
		return nil, err
	}
	if target.Role == domain.RoleOwner {
		return nil, ErrForbidden
	}
	if err := s.Repo.MarkMemberStatus(ctx, groupID, targetUserID, domain.MemberKicked); err != nil {
		return nil, err
	}
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
		return nil, err
	}
	return s.createSystemMessage(ctx, groupID, operatorID, fmt.Sprintf("成员 %d 已被禁言", targetUserID), map[string]any{"event": "member_muted", "userId": targetUserID, "seconds": seconds})
}

func (s *Service) UnmuteMember(ctx context.Context, groupID, operatorID, targetUserID int64) (*domain.Message, error) {
	if err := s.requireRole(ctx, groupID, operatorID, "mute_member"); err != nil {
		return nil, err
	}
	if err := s.Repo.CancelMute(ctx, groupID, targetUserID); err != nil {
		return nil, err
	}
	return s.createSystemMessage(ctx, groupID, operatorID, fmt.Sprintf("成员 %d 已解除禁言", targetUserID), map[string]any{"event": "member_unmuted", "userId": targetUserID})
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
	if existing, err := s.Repo.FindMessageByClientID(ctx, in.SenderID, in.ClientMessageID); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	g, err := s.Repo.GetGroup(ctx, in.GroupID)
	if err != nil {
		return nil, false, err
	}
	if g.Status != domain.StatusNormal {
		return nil, false, ErrGroupDismissed
	}
	member, err := s.Repo.GetMember(ctx, in.GroupID, in.SenderID)
	if err != nil {
		return nil, false, ErrForbidden
	}
	if member.Status != "normal" {
		return nil, false, ErrForbidden
	}
	if g.MuteAll && member.Role == domain.RoleMember {
		return nil, false, ErrMuted
	}
	muted, err := s.Repo.IsMuted(ctx, in.GroupID, in.SenderID)
	if err != nil {
		return nil, false, err
	}
	if muted {
		return nil, false, ErrMuted
	}
	if err := s.checkSlowMode(ctx, g, member); err != nil {
		return nil, false, err
	}
	seq, err := s.nextSequence(ctx, in.GroupID)
	if err != nil {
		return nil, false, err
	}
	msg := &domain.Message{MessageID: id.New("msg"), GroupID: in.GroupID, Sequence: seq, SenderID: in.SenderID, SenderName: member.Nickname, ClientMessageID: in.ClientMessageID, MessageType: in.MessageType, Content: in.Content, MentionAll: in.MentionAll, MentionUserIDs: in.MentionUserIDs, Extra: in.Extra, Status: "normal"}
	if err := s.Repo.InsertMessage(ctx, msg); err != nil {
		if existing, qerr := s.Repo.FindMessageByClientID(ctx, in.SenderID, in.ClientMessageID); qerr == nil {
			return existing, true, nil
		}
		return nil, false, err
	}
	s.setMaxSequence(ctx, in.GroupID, seq)
	if err := s.publishCreated(ctx, msg, g.GroupType); err != nil {
		s.Log.Warn("kafka publish failed", zap.Error(err), zap.Int64("groupId", in.GroupID), zap.String("messageId", msg.MessageID))
	}
	return msg, false, nil
}

// createSystemMessage creates a system message in the group and returns it.
func (s *Service) createSystemMessage(ctx context.Context, groupID, operatorID int64, content string, extra map[string]any) (*domain.Message, error) {
	seq, err := s.nextSequence(ctx, groupID)
	if err != nil {
		return nil, err
	}
	msg := &domain.Message{MessageID: id.New("msg"), GroupID: groupID, Sequence: seq, SenderID: 0, SenderName: "系统", ClientMessageID: id.New("system"), MessageType: domain.MessageSystem, Content: content, Extra: extra, Status: "normal"}
	if err := s.Repo.InsertMessage(ctx, msg); err != nil {
		return nil, err
	}
	s.setMaxSequence(ctx, groupID, seq)
	g, _ := s.Repo.GetGroup(ctx, groupID)
	gt := domain.GroupNormal
	if g != nil {
		gt = g.GroupType
	}
	_ = s.publishCreated(ctx, msg, gt)
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
		seq, err := s.Redis.Incr(ctx, key).Result()
		if err == nil {
			return seq, nil
		}
		s.Log.Warn("redis sequence failed, fallback to mysql", zap.Error(err))
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
	if g.SlowModeSeconds <= 0 || m.Role != domain.RoleMember {
		return nil
	}
	if s.Redis == nil {
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

// publishCreated publishes a message creation event.
func (s *Service) publishCreated(ctx context.Context, msg *domain.Message, groupType string) error {
	if s.Producer == nil {
		return nil
	}
	event := map[string]any{"eventId": id.New("evt"), "eventType": "group_message_created", "traceId": "", "groupId": msg.GroupID, "groupType": groupType, "messageId": msg.MessageID, "sequence": msg.Sequence, "senderId": msg.SenderID, "occurredAt": time.Now().Format(time.RFC3339Nano), "payload": msg}
	return s.Producer.Produce(ctx, strconv.FormatInt(msg.GroupID, 10), event)
}

func (s *Service) requireRole(ctx context.Context, groupID, userID int64, action string) error {
	m, err := s.Repo.GetMember(ctx, groupID, userID)
	if err != nil {
		return ErrForbidden
	}
	if m.Status != "normal" {
		return ErrForbidden
	}
	switch action {
	case "dismiss_group", "set_role":
		if m.Role != domain.RoleOwner {
			return ErrForbidden
		}
	case "update_settings", "kick_member", "mute_member":
		if m.Role != domain.RoleOwner && m.Role != domain.RoleAdmin {
			return ErrForbidden
		}
	default:
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
