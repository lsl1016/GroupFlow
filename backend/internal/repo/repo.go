package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"groupflow/backend/internal/domain"
	"groupflow/backend/pkg/logx"
)

type Repository struct {
	db         *sql.DB
	shardCount int
}

func New(db *sql.DB, shardCount int) *Repository {
	if shardCount <= 0 {
		shardCount = 1
	}
	return &Repository{db: db, shardCount: shardCount}
}
func (r *Repository) DB() *sql.DB { return r.db }

// messageTable 返回 group_message 的分表名（分表预留）。shardCount<=1 时使用单表，
// 否则按 group_id 哈希路由到 group_message_NN。表名为内部常量拼接，不含用户输入，无注入风险。
func (r *Repository) messageTable(groupID int64) string {
	if r.shardCount <= 1 {
		return "group_message"
	}
	idx := ((groupID % int64(r.shardCount)) + int64(r.shardCount)) % int64(r.shardCount)
	return fmt.Sprintf("group_message_%02d", idx)
}

func now() time.Time { return time.Now().Truncate(time.Second) }

// slowSQL 在查询耗时超过阈值时输出 MySQL 慢查询业务日志，traceId 随 context 自动携带。
func slowSQL(ctx context.Context, name string, start time.Time, fields ...zap.Field) {
	ms := time.Since(start).Milliseconds()
	if ms >= logx.MySQLSlowMs() {
		logx.From(ctx).Warn("mysql_slow_query", append([]zap.Field{
			zap.String("event", "mysql_slow_query"), zap.String("queryName", name), zap.Int64("durationMs", ms),
		}, fields...)...)
	}
}

// dbErr 统一记录数据库错误：op 标识具体 SQL 操作，附带业务字段便于排查，最终原样返回 err。
// sql.ErrNoRows 属于“未命中”而非故障（如幂等查询、登录建号探测），跳过不记录以免噪声。
func dbErr(ctx context.Context, op string, err error, fields ...zap.Field) error {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return err
	}
	logx.From(ctx).Error("db_error", append([]zap.Field{
		zap.String("event", "db_error"), zap.String("op", op), zap.Error(err),
	}, fields...)...)
	return err
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanUser(scanner interface{ Scan(dest ...any) error }) (*domain.User, error) {
	u := &domain.User{}
	var avatar sql.NullString
	if err := scanner.Scan(&u.ID, &u.Username, &u.Nickname, &avatar, &u.Status, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	u.Avatar = avatar.String
	return u, nil
}

func (r *Repository) LoginOrCreateUser(ctx context.Context, username string) (*domain.User, error) {
	u, err := r.GetUserByUsername(ctx, username)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	t := now()
	res, err := r.db.ExecContext(ctx, `INSERT INTO user_account(username,nickname,avatar,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, username, username, "", domain.StatusNormal, t, t)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return r.GetUser(ctx, id)
}

func (r *Repository) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	return scanUser(r.db.QueryRowContext(ctx, `SELECT id,username,nickname,avatar,status,created_at,updated_at FROM user_account WHERE username=? LIMIT 1`, username))
}

func (r *Repository) GetUser(ctx context.Context, userID int64) (*domain.User, error) {
	u, err := scanUser(r.db.QueryRowContext(ctx, `SELECT id,username,nickname,avatar,status,created_at,updated_at FROM user_account WHERE id=? LIMIT 1`, userID))
	return u, dbErr(ctx, "get_user", err, zap.Int64("userId", userID))
}

func scanGroup(scanner interface{ Scan(dest ...any) error }) (*domain.Group, error) {
	g := &domain.Group{}
	var avatar, desc, lastID, lastSummary sql.NullString
	var lastAt sql.NullTime
	var muteAll, allowInvite int
	if err := scanner.Scan(&g.ID, &g.Name, &avatar, &desc, &g.OwnerID, &g.GroupType, &g.JoinMode, &g.Status, &muteAll, &g.SlowModeSeconds, &allowInvite, &g.MentionAllRole, &g.MemberCount, &g.MaxMemberCount, &g.MaxSequence, &lastID, &lastSummary, &lastAt, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	g.Avatar = avatar.String
	g.Description = desc.String
	g.LastMessageID = lastID.String
	g.LastMessageSummary = lastSummary.String
	if lastAt.Valid {
		g.LastMessageAt = &lastAt.Time
	}
	g.MuteAll = muteAll == 1
	g.AllowMemberInvite = allowInvite == 1
	return g, nil
}

const groupCols = `id,name,avatar,description,owner_id,group_type,join_mode,status,mute_all,slow_mode_seconds,allow_member_invite,mention_all_role,member_count,max_member_count,max_sequence,last_message_id,last_message_summary,last_message_at,created_at,updated_at`

func (r *Repository) GetGroup(ctx context.Context, groupID int64) (*domain.Group, error) {
	g, err := scanGroup(r.db.QueryRowContext(ctx, `SELECT `+groupCols+` FROM chat_group WHERE id=? LIMIT 1`, groupID))
	return g, dbErr(ctx, "get_group", err, zap.Int64("groupId", groupID))
}

func (r *Repository) CreateGroup(ctx context.Context, owner *domain.User, name, desc, avatar, joinMode, groupType string, maxMemberCount int, slowMode int) (*domain.Group, error) {
	if joinMode == "" {
		joinMode = domain.JoinModeDirect
	}
	if groupType == "" {
		if maxMemberCount >= 500 {
			groupType = domain.GroupLarge
		} else {
			groupType = domain.GroupNormal
		}
	}
	if maxMemberCount <= 0 {
		maxMemberCount = 500
	}
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbErr(ctx, "create_group_begin_tx", err, zap.Int64("ownerId", owner.ID))
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO chat_group(name,avatar,description,owner_id,group_type,join_mode,status,mute_all,slow_mode_seconds,allow_member_invite,mention_all_role,member_count,max_member_count,max_sequence,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, name, avatar, desc, owner.ID, groupType, joinMode, domain.StatusNormal, 0, slowMode, 1, "admin", 1, maxMemberCount, 0, t, t)
	if err != nil {
		return nil, dbErr(ctx, "create_group_insert_group", err, zap.Int64("ownerId", owner.ID), zap.String("name", name))
	}
	gid, _ := res.LastInsertId()
	_, err = tx.ExecContext(ctx, `INSERT INTO group_member(group_id,user_id,role,status,last_read_sequence,joined_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, gid, owner.ID, domain.RoleOwner, domain.StatusNormal, 0, t, t, t)
	if err != nil {
		return nil, dbErr(ctx, "create_group_insert_owner_member", err, zap.Int64("groupId", gid), zap.Int64("ownerId", owner.ID))
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,action,detail,created_at) VALUES(?,?,?,?,?)`, gid, owner.ID, "group_create", jsonRaw(map[string]any{"name": name}), t)
	if err != nil {
		return nil, dbErr(ctx, "create_group_insert_oplog", err, zap.Int64("groupId", gid), zap.Int64("ownerId", owner.ID))
	}
	if err := tx.Commit(); err != nil {
		return nil, dbErr(ctx, "create_group_commit", err, zap.Int64("groupId", gid), zap.Int64("ownerId", owner.ID))
	}
	logx.From(ctx).Info("db_group_created", zap.String("event", "db_group_created"), zap.Int64("groupId", gid), zap.Int64("ownerId", owner.ID), zap.String("groupType", groupType))
	return r.GetGroup(ctx, gid)
}

func prefixGroupCols(p string) string {
	parts := strings.Split(groupCols, ",")
	for i, c := range parts {
		parts[i] = p + "." + c
	}
	return strings.Join(parts, ",")
}

func scanGroupWithPrefix(rows *sql.Rows, pre ...any) (*domain.Group, error) {
	g := &domain.Group{}
	var avatar, desc, lastID, lastSummary sql.NullString
	var lastAt sql.NullTime
	var muteAll, allowInvite int
	dest := append(pre, &g.ID, &g.Name, &avatar, &desc, &g.OwnerID, &g.GroupType, &g.JoinMode, &g.Status, &muteAll, &g.SlowModeSeconds, &allowInvite, &g.MentionAllRole, &g.MemberCount, &g.MaxMemberCount, &g.MaxSequence, &lastID, &lastSummary, &lastAt, &g.CreatedAt, &g.UpdatedAt)
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	g.Avatar = avatar.String
	g.Description = desc.String
	g.LastMessageID = lastID.String
	g.LastMessageSummary = lastSummary.String
	if lastAt.Valid {
		g.LastMessageAt = &lastAt.Time
	}
	g.MuteAll = muteAll == 1
	g.AllowMemberInvite = allowInvite == 1
	return g, nil
}

func (r *Repository) ListGroupsForUser(ctx context.Context, userID int64, cursor int64, limit int) (*domain.Page[domain.GroupListItem], error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.db.QueryContext(ctx, `SELECT gm.id, gm.role, gm.last_read_sequence,
  COALESCE((SELECT COUNT(1) FROM group_mention mt WHERE mt.group_id=gm.group_id AND mt.user_id=? AND mt.read_status=0),0) AS mention_count,
  EXISTS(SELECT 1 FROM group_message mm WHERE mm.group_id=gm.group_id AND mm.sequence>gm.last_read_sequence AND mm.mention_all=1 AND mm.status='normal' LIMIT 1) AS mention_all_unread,
  `+prefixGroupCols("g")+`
FROM group_member gm JOIN chat_group g ON gm.group_id=g.id
WHERE gm.user_id=? AND gm.status='normal' AND gm.id>? AND g.status <> 'dismissed'
ORDER BY gm.id ASC LIMIT ?`, userID, userID, cursor, limit+1)
	if err != nil {
		return nil, dbErr(ctx, "list_groups_for_user", err, zap.Int64("userId", userID))
	}
	defer rows.Close()
	items := make([]domain.GroupListItem, 0, limit)
	var nextCursor string
	for rows.Next() {
		var memberID, mentionCount int64
		var role string
		var lastRead int64
		var mentionAllUnreadInt int
		g, err := scanGroupWithPrefix(rows, &memberID, &role, &lastRead, &mentionCount, &mentionAllUnreadInt)
		if err != nil {
			return nil, err
		}
		if len(items) >= limit {
			nextCursor = strconv.FormatInt(memberID, 10)
			break
		}
		text := ""
		if mentionCount > 0 {
			text = "有人@我"
		} else if mentionAllUnreadInt == 1 {
			text = "@所有人"
		}
		items = append(items, domain.GroupListItem{Group: *g, MyRole: role, LastReadSequence: lastRead, UnreadCount: max64(0, g.MaxSequence-lastRead), MentionCount: mentionCount, MentionAllUnread: mentionAllUnreadInt == 1, MentionSummaryText: text})
	}
	return &domain.Page[domain.GroupListItem]{Items: items, NextCursor: nextCursor, HasMore: nextCursor != ""}, rows.Err()
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func scanMember(scanner interface{ Scan(dest ...any) error }) (*domain.Member, error) {
	m := &domain.Member{}
	var avatar sql.NullString
	var left sql.NullTime
	if err := scanner.Scan(&m.ID, &m.GroupID, &m.UserID, &m.Username, &m.Nickname, &avatar, &m.Role, &m.Status, &m.LastReadSequence, &m.JoinedAt, &left, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.Avatar = avatar.String
	if left.Valid {
		m.LeftAt = &left.Time
	}
	return m, nil
}

func (r *Repository) GetMember(ctx context.Context, groupID, userID int64) (*domain.Member, error) {
	q := `SELECT gm.id,gm.group_id,gm.user_id,u.username,u.nickname,u.avatar,gm.role,gm.status,gm.last_read_sequence,gm.joined_at,gm.left_at,gm.created_at,gm.updated_at FROM group_member gm JOIN user_account u ON gm.user_id=u.id WHERE gm.group_id=? AND gm.user_id=? LIMIT 1`
	m, err := scanMember(r.db.QueryRowContext(ctx, q, groupID, userID))
	return m, dbErr(ctx, "get_member", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
}

func (r *Repository) IsActiveMember(ctx context.Context, groupID, userID int64) bool {
	var n int
	_ = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM group_member WHERE group_id=? AND user_id=? AND status='normal'`, groupID, userID).Scan(&n)
	return n > 0
}

func (r *Repository) ListMembers(ctx context.Context, groupID int64, cursor int64, limit int, role string) (*domain.Page[domain.Member], error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{groupID, cursor}
	whereRole := ""
	if role != "" {
		whereRole = " AND gm.role=?"
		args = append(args, role)
	}
	args = append(args, limit+1)
	q := `SELECT gm.id,gm.group_id,gm.user_id,u.username,u.nickname,u.avatar,gm.role,gm.status,gm.last_read_sequence,gm.joined_at,gm.left_at,gm.created_at,gm.updated_at FROM group_member gm JOIN user_account u ON gm.user_id=u.id WHERE gm.group_id=? AND gm.status='normal' AND gm.id>?` + whereRole + ` ORDER BY gm.id ASC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, dbErr(ctx, "list_members", err, zap.Int64("groupId", groupID))
	}
	defer rows.Close()
	items := make([]domain.Member, 0, limit)
	var next string
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		if len(items) >= limit {
			next = strconv.FormatInt(m.ID, 10)
			break
		}
		items = append(items, *m)
	}
	return &domain.Page[domain.Member]{Items: items, NextCursor: next, HasMore: next != ""}, rows.Err()
}

func (r *Repository) AddMember(ctx context.Context, groupID int64, userID int64, role string) error {
	if role == "" {
		role = domain.RoleMember
	}
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr(ctx, "add_member_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO group_member(group_id,user_id,role,status,last_read_sequence,joined_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status='normal', role=IF(role='owner',role,VALUES(role)), left_at=NULL, updated_at=VALUES(updated_at)`, groupID, userID, role, domain.StatusNormal, 0, t, t, t)
	if err != nil {
		return dbErr(ctx, "add_member_upsert", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.String("role", role))
	}
	_, _ = tx.ExecContext(ctx, `UPDATE chat_group SET member_count=(SELECT COUNT(*) FROM group_member WHERE group_id=? AND status='normal'), updated_at=? WHERE id=?`, groupID, t, groupID)
	if err := tx.Commit(); err != nil {
		return dbErr(ctx, "add_member_commit", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	logx.From(ctx).Info("db_member_added", zap.String("event", "db_member_added"), zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.String("role", role))
	return nil
}

func (r *Repository) UpdateGroupSettings(ctx context.Context, groupID int64, fields map[string]any) error {
	sets := []string{"updated_at=?"}
	args := []any{now()}
	for k, v := range fields {
		sets = append(sets, k+"=?")
		args = append(args, v)
	}
	args = append(args, groupID)
	_, err := r.db.ExecContext(ctx, `UPDATE chat_group SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	return dbErr(ctx, "update_group_settings", err, zap.Int64("groupId", groupID))
}

func (r *Repository) UpdateMemberRole(ctx context.Context, groupID, userID int64, role string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE group_member SET role=?, updated_at=? WHERE group_id=? AND user_id=? AND status='normal' AND role <> 'owner'`, role, now(), groupID, userID)
	return dbErr(ctx, "update_member_role", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.String("role", role))
}

func (r *Repository) MarkMemberStatus(ctx context.Context, groupID, userID int64, status string, ob *domain.OutboxEvent) error {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr(ctx, "mark_member_status_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE group_member SET status=?, left_at=?, updated_at=? WHERE group_id=? AND user_id=? AND role <> 'owner'`, status, t, t, groupID, userID)
	if err != nil {
		return dbErr(ctx, "mark_member_status_update", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.String("status", status))
	}
	_, _ = tx.ExecContext(ctx, `UPDATE chat_group SET member_count=(SELECT COUNT(*) FROM group_member WHERE group_id=? AND status='normal'), updated_at=? WHERE id=?`, groupID, t, groupID)
	if err := insertOutboxTx(ctx, tx, ob, t); err != nil {
		return dbErr(ctx, "mark_member_status_outbox", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.String("status", status))
	}
	if err := tx.Commit(); err != nil {
		return dbErr(ctx, "mark_member_status_commit", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	logx.From(ctx).Info("db_member_status_changed", zap.String("event", "db_member_status_changed"), zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.String("status", status))
	return nil
}

func (r *Repository) DismissGroup(ctx context.Context, groupID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE chat_group SET status='dismissed', updated_at=? WHERE id=?`, now(), groupID)
	if err != nil {
		return dbErr(ctx, "dismiss_group", err, zap.Int64("groupId", groupID))
	}
	logx.From(ctx).Info("db_group_dismissed", zap.String("event", "db_group_dismissed"), zap.Int64("groupId", groupID))
	return nil
}

func (r *Repository) UpsertMute(ctx context.Context, groupID, userID, operatorID int64, reason string, expireAt *time.Time) error {
	t := now()
	_, _ = r.db.ExecContext(ctx, `UPDATE group_mute_record SET status='canceled', updated_at=? WHERE group_id=? AND user_id=? AND status='active'`, t, groupID, userID)
	_, err := r.db.ExecContext(ctx, `INSERT INTO group_mute_record(group_id,user_id,operator_id,reason,expire_at,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, groupID, userID, operatorID, reason, expireAt, "active", t, t)
	if err != nil {
		return dbErr(ctx, "upsert_mute", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.Int64("operatorId", operatorID))
	}
	logx.From(ctx).Info("db_mute_upserted", zap.String("event", "db_mute_upserted"), zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.Int64("operatorId", operatorID))
	return nil
}

func (r *Repository) CancelMute(ctx context.Context, groupID, userID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE group_mute_record SET status='canceled', updated_at=? WHERE group_id=? AND user_id=? AND status='active'`, now(), groupID, userID)
	return dbErr(ctx, "cancel_mute", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
}

func (r *Repository) IsMuted(ctx context.Context, groupID, userID int64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM group_mute_record WHERE group_id=? AND user_id=? AND status='active' AND (expire_at IS NULL OR expire_at > NOW())`, groupID, userID).Scan(&n)
	return n > 0, dbErr(ctx, "is_muted", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
}

func (r *Repository) FindMessageByClientID(ctx context.Context, groupID, senderID int64, clientID string) (*domain.Message, error) {
	return scanMessage(r.db.QueryRowContext(ctx, `SELECT `+messageCols+` FROM `+r.messageTable(groupID)+` WHERE sender_id=? AND client_message_id=? LIMIT 1`, senderID, clientID))
}

func (r *Repository) FindMessageByID(ctx context.Context, groupID int64, messageID string) (*domain.Message, error) {
	return scanMessage(r.db.QueryRowContext(ctx, `SELECT `+messageCols+` FROM `+r.messageTable(groupID)+` WHERE message_id=? LIMIT 1`, messageID))
}

const messageCols = `id,message_id,group_id,sequence,sender_id,sender_name,client_message_id,message_type,content,mention_all,mention_user_ids,extra,status,created_at,updated_at`

func scanMessage(scanner interface{ Scan(dest ...any) error }) (*domain.Message, error) {
	m := &domain.Message{}
	var mentionRaw, extraRaw sql.NullString
	var mentionAll int
	if err := scanner.Scan(&m.ID, &m.MessageID, &m.GroupID, &m.Sequence, &m.SenderID, &m.SenderName, &m.ClientMessageID, &m.MessageType, &m.Content, &mentionAll, &mentionRaw, &extraRaw, &m.Status, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.MentionAll = mentionAll == 1
	if mentionRaw.Valid && mentionRaw.String != "" {
		_ = json.Unmarshal([]byte(mentionRaw.String), &m.MentionUserIDs)
	}
	if extraRaw.Valid && extraRaw.String != "" {
		_ = json.Unmarshal([]byte(extraRaw.String), &m.Extra)
	}
	if m.MentionUserIDs == nil {
		m.MentionUserIDs = []int64{}
	}
	if m.Extra == nil {
		m.Extra = map[string]any{}
	}
	return m, nil
}

func (r *Repository) InsertMessage(ctx context.Context, m *domain.Message, ob *domain.OutboxEvent) error {
	defer slowSQL(ctx, "insert_group_message", time.Now(), zap.Int64("groupId", m.GroupID), zap.String("messageId", m.MessageID))
	mention, _ := json.Marshal(m.MentionUserIDs)
	extra, _ := json.Marshal(m.Extra)
	t := now()
	m.CreatedAt = t
	m.UpdatedAt = t
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr(ctx, "insert_message_begin_tx", err, zap.Int64("groupId", m.GroupID), zap.String("messageId", m.MessageID))
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO `+r.messageTable(m.GroupID)+`(message_id,group_id,sequence,sender_id,sender_name,client_message_id,message_type,content,mention_all,mention_user_ids,extra,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.MessageID, m.GroupID, m.Sequence, m.SenderID, m.SenderName, m.ClientMessageID, m.MessageType, m.Content, boolInt(m.MentionAll), string(mention), string(extra), domain.StatusNormal, t, t)
	if err != nil {
		return dbErr(ctx, "insert_message_insert", err, zap.Int64("groupId", m.GroupID), zap.String("messageId", m.MessageID), zap.String("clientMessageId", m.ClientMessageID), zap.Int64("sequence", m.Sequence))
	}
	summary := m.Content
	if len([]rune(summary)) > 80 {
		summary = string([]rune(summary)[:80])
	}
	_, err = tx.ExecContext(ctx, `UPDATE chat_group SET max_sequence=GREATEST(max_sequence,?), last_message_id=?, last_message_summary=?, last_message_at=?, updated_at=? WHERE id=?`, m.Sequence, m.MessageID, summary, t, t, m.GroupID)
	if err != nil {
		return dbErr(ctx, "insert_message_update_group", err, zap.Int64("groupId", m.GroupID), zap.String("messageId", m.MessageID))
	}
	// @某人只给被点名的用户写 group_mention；@所有人不展开，避免大群同步写入海量记录。
	for _, uid := range uniqueInt64s(m.MentionUserIDs) {
		if uid <= 0 || uid == m.SenderID {
			continue
		}
		_, _ = tx.ExecContext(ctx, `INSERT IGNORE INTO group_mention(group_id,message_id,sequence,user_id,mention_type,read_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, m.GroupID, m.MessageID, m.Sequence, uid, domain.MentionUser, 0, t, t)
	}
	if err := insertOutboxTx(ctx, tx, ob, t); err != nil {
		return dbErr(ctx, "insert_message_outbox", err, zap.Int64("groupId", m.GroupID), zap.String("messageId", m.MessageID))
	}
	if err := tx.Commit(); err != nil {
		return dbErr(ctx, "insert_message_commit", err, zap.Int64("groupId", m.GroupID), zap.String("messageId", m.MessageID))
	}
	return nil
}

// insertOutboxTx 在给定事务内写入一条待发 Outbox 事件；ob 为 nil（如 Kafka 关闭）时跳过。
// 与消息落库同事务提交，保证“消息已存储 ⇔ 事件已入队待发”的原子性。
func insertOutboxTx(ctx context.Context, tx *sql.Tx, ob *domain.OutboxEvent, t time.Time) error {
	if ob == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO message_outbox(event_id,topic,aggregate_id,payload,status,retry_count,next_retry_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		ob.EventID, ob.Topic, ob.AggregateID, string(ob.Payload), "pending", 0, t, t, t)
	return err
}

func (r *Repository) MaxSequence(ctx context.Context, groupID int64) (int64, error) {
	var max sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT MAX(sequence) FROM `+r.messageTable(groupID)+` WHERE group_id=?`, groupID).Scan(&max)
	if err != nil {
		return 0, dbErr(ctx, "max_sequence", err, zap.Int64("groupId", groupID))
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Int64, nil
}

func (r *Repository) ListMessages(ctx context.Context, groupID int64, beforeSeq, afterSeq int64, limit int) (*domain.Page[domain.Message], error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	defer slowSQL(ctx, "list_group_messages", time.Now(), zap.Int64("groupId", groupID), zap.Int("limit", limit))
	table := r.messageTable(groupID)
	var rows *sql.Rows
	var err error
	if afterSeq > 0 {
		rows, err = r.db.QueryContext(ctx, `SELECT `+messageCols+` FROM `+table+` WHERE group_id=? AND sequence>? ORDER BY sequence ASC LIMIT ?`, groupID, afterSeq, limit+1)
	} else if beforeSeq > 0 {
		rows, err = r.db.QueryContext(ctx, `SELECT `+messageCols+` FROM `+table+` WHERE group_id=? AND sequence<? ORDER BY sequence DESC LIMIT ?`, groupID, beforeSeq, limit+1)
	} else {
		rows, err = r.db.QueryContext(ctx, `SELECT `+messageCols+` FROM `+table+` WHERE group_id=? ORDER BY sequence DESC LIMIT ?`, groupID, limit+1)
	}
	if err != nil {
		return nil, dbErr(ctx, "list_messages", err, zap.Int64("groupId", groupID), zap.Int64("beforeSeq", beforeSeq), zap.Int64("afterSeq", afterSeq))
	}
	defer rows.Close()
	items := make([]domain.Message, 0, limit)
	var next string
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		if len(items) >= limit {
			next = strconv.FormatInt(m.Sequence, 10)
			break
		}
		items = append(items, *m)
	}
	if afterSeq <= 0 {
		reverseMessages(items)
	}
	return &domain.Page[domain.Message]{Items: items, NextCursor: next, HasMore: next != ""}, rows.Err()
}

func reverseMessages(s []domain.Message) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func (r *Repository) UpdateRead(ctx context.Context, groupID, userID, lastRead int64) error {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr(ctx, "update_read_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE group_member SET last_read_sequence=GREATEST(last_read_sequence,?), updated_at=? WHERE group_id=? AND user_id=? AND status='normal'`, lastRead, t, groupID, userID)
	if err != nil {
		return dbErr(ctx, "update_read_member", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.Int64("lastRead", lastRead))
	}
	_, _ = tx.ExecContext(ctx, `UPDATE group_mention SET read_status=1, updated_at=? WHERE group_id=? AND user_id=? AND sequence<=? AND read_status=0`, t, groupID, userID, lastRead)
	if err := tx.Commit(); err != nil {
		return dbErr(ctx, "update_read_commit", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	return nil
}

type memberIDRow struct {
	rowID  int64
	userID int64
}

func paginateActiveMemberIDs(rows []memberIDRow, limit int) ([]int64, int64) {
	ids := make([]int64, 0, limit)
	var next int64
	for i, row := range rows {
		if i >= limit {
			next = rows[limit-1].rowID
			break
		}
		ids = append(ids, row.userID)
	}
	return ids, next
}

func (r *Repository) ListActiveMemberIDs(ctx context.Context, groupID int64, cursor int64, limit int) ([]int64, int64, error) {
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	defer slowSQL(ctx, "list_active_member_ids", time.Now(), zap.Int64("groupId", groupID), zap.Int("limit", limit))
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id FROM group_member WHERE group_id=? AND status='normal' AND id>? ORDER BY id ASC LIMIT ?`, groupID, cursor, limit+1)
	if err != nil {
		return nil, 0, dbErr(ctx, "list_active_member_ids", err, zap.Int64("groupId", groupID), zap.Int64("cursor", cursor))
	}
	defer rows.Close()
	memberRows := make([]memberIDRow, 0, limit+1)
	for rows.Next() {
		var row memberIDRow
		if err := rows.Scan(&row.rowID, &row.userID); err != nil {
			return nil, 0, err
		}
		memberRows = append(memberRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	ids, next := paginateActiveMemberIDs(memberRows, limit)
	return ids, next, nil
}

func scanAnnouncement(scanner interface{ Scan(dest ...any) error }) (*domain.Announcement, error) {
	a := &domain.Announcement{}
	var pinned int
	var operatorName sql.NullString
	if err := scanner.Scan(&a.ID, &a.GroupID, &a.OperatorID, &operatorName, &a.Title, &a.Content, &pinned, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.OperatorName = operatorName.String
	a.Pinned = pinned == 1
	return a, nil
}

func (r *Repository) CreateAnnouncement(ctx context.Context, groupID, operatorID int64, title, content string, pinned bool) (*domain.Announcement, error) {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbErr(ctx, "create_announcement_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID))
	}
	defer tx.Rollback()
	if pinned {
		_, _ = tx.ExecContext(ctx, `UPDATE group_announcement SET pinned=0, updated_at=? WHERE group_id=? AND status='normal'`, t, groupID)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO group_announcement(group_id,operator_id,title,content,pinned,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, groupID, operatorID, title, content, boolInt(pinned), domain.StatusNormal, t, t)
	if err != nil {
		return nil, dbErr(ctx, "create_announcement_insert", err, zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID))
	}
	id, _ := res.LastInsertId()
	_, _ = tx.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,action,detail,created_at) VALUES(?,?,?,?,?)`, groupID, operatorID, "announcement_create", jsonRaw(map[string]any{"announcementId": id, "title": title}), t)
	if err := tx.Commit(); err != nil {
		return nil, dbErr(ctx, "create_announcement_commit", err, zap.Int64("groupId", groupID), zap.Int64("announcementId", id))
	}
	logx.From(ctx).Info("db_announcement_created", zap.String("event", "db_announcement_created"), zap.Int64("groupId", groupID), zap.Int64("announcementId", id), zap.Int64("operatorId", operatorID))
	return r.GetAnnouncement(ctx, groupID, id)
}

func (r *Repository) GetAnnouncement(ctx context.Context, groupID, id int64) (*domain.Announcement, error) {
	return scanAnnouncement(r.db.QueryRowContext(ctx, `SELECT a.id,a.group_id,a.operator_id,u.nickname,a.title,a.content,a.pinned,a.status,a.created_at,a.updated_at FROM group_announcement a LEFT JOIN user_account u ON a.operator_id=u.id WHERE a.group_id=? AND a.id=? LIMIT 1`, groupID, id))
}

func (r *Repository) ListAnnouncements(ctx context.Context, groupID, cursor int64, limit int) (*domain.Page[domain.Announcement], error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	// 公告按 置顶优先 + id 倒序（最新在前）展示，游标语义为“取比 cursor 更旧（id 更小）的记录”，
	// cursor=0 表示从最新开始。每群至多一条置顶（发布/置顶时会取消其它置顶），故置顶项稳定出现在首页。
	args := []any{groupID}
	whereCursor := ""
	if cursor > 0 {
		whereCursor = " AND a.id<?"
		args = append(args, cursor)
	}
	args = append(args, limit+1)
	rows, err := r.db.QueryContext(ctx, `SELECT a.id,a.group_id,a.operator_id,u.nickname,a.title,a.content,a.pinned,a.status,a.created_at,a.updated_at FROM group_announcement a LEFT JOIN user_account u ON a.operator_id=u.id WHERE a.group_id=? AND a.status='normal'`+whereCursor+` ORDER BY a.pinned DESC, a.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, dbErr(ctx, "list_announcements", err, zap.Int64("groupId", groupID))
	}
	defer rows.Close()
	items := make([]domain.Announcement, 0, limit)
	var next string
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		if len(items) >= limit {
			next = strconv.FormatInt(a.ID, 10)
			break
		}
		items = append(items, *a)
	}
	return &domain.Page[domain.Announcement]{Items: items, NextCursor: next, HasMore: next != ""}, rows.Err()
}

func (r *Repository) UpdateAnnouncement(ctx context.Context, groupID, id, operatorID int64, title, content string, pinned *bool) (*domain.Announcement, error) {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbErr(ctx, "update_announcement_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("announcementId", id))
	}
	defer tx.Rollback()
	if pinned != nil && *pinned {
		_, _ = tx.ExecContext(ctx, `UPDATE group_announcement SET pinned=0, updated_at=? WHERE group_id=? AND status='normal'`, t, groupID)
	}
	sets := []string{"updated_at=?"}
	args := []any{t}
	if title != "" {
		sets = append(sets, "title=?")
		args = append(args, title)
	}
	if content != "" {
		sets = append(sets, "content=?")
		args = append(args, content)
	}
	if pinned != nil {
		sets = append(sets, "pinned=?")
		args = append(args, boolInt(*pinned))
	}
	args = append(args, groupID, id)
	_, err = tx.ExecContext(ctx, `UPDATE group_announcement SET `+strings.Join(sets, ",")+` WHERE group_id=? AND id=? AND status='normal'`, args...)
	if err != nil {
		return nil, dbErr(ctx, "update_announcement", err, zap.Int64("groupId", groupID), zap.Int64("announcementId", id))
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,action,detail,created_at) VALUES(?,?,?,?,?)`, groupID, operatorID, "announcement_update", jsonRaw(map[string]any{"announcementId": id}), t)
	if err := tx.Commit(); err != nil {
		return nil, dbErr(ctx, "update_announcement_commit", err, zap.Int64("groupId", groupID), zap.Int64("announcementId", id))
	}
	return r.GetAnnouncement(ctx, groupID, id)
}

func (r *Repository) DeleteAnnouncement(ctx context.Context, groupID, id, operatorID int64) error {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr(ctx, "delete_announcement_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("announcementId", id))
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE group_announcement SET status='deleted', updated_at=? WHERE group_id=? AND id=? AND status='normal'`, t, groupID, id)
	if err != nil {
		return dbErr(ctx, "delete_announcement", err, zap.Int64("groupId", groupID), zap.Int64("announcementId", id))
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,action,detail,created_at) VALUES(?,?,?,?,?)`, groupID, operatorID, "announcement_delete", jsonRaw(map[string]any{"announcementId": id}), t)
	if err := tx.Commit(); err != nil {
		return dbErr(ctx, "delete_announcement_commit", err, zap.Int64("groupId", groupID), zap.Int64("announcementId", id))
	}
	return nil
}

func scanJoinRequest(scanner interface{ Scan(dest ...any) error }) (*domain.JoinRequest, error) {
	jr := &domain.JoinRequest{}
	var avatar, reason sql.NullString
	var op sql.NullInt64
	if err := scanner.Scan(&jr.ID, &jr.GroupID, &jr.UserID, &jr.Username, &jr.Nickname, &avatar, &reason, &jr.Status, &op, &jr.CreatedAt, &jr.UpdatedAt); err != nil {
		return nil, err
	}
	jr.Avatar = avatar.String
	jr.Reason = reason.String
	if op.Valid {
		v := op.Int64
		jr.OperatorID = &v
	}
	return jr, nil
}

const joinReqCols = `jr.id,jr.group_id,jr.user_id,u.username,u.nickname,u.avatar,jr.reason,jr.status,jr.operator_id,jr.created_at,jr.updated_at`

func (r *Repository) CreateJoinRequest(ctx context.Context, groupID, userID int64, reason string, buildOutbox func(*domain.JoinRequest) *domain.OutboxEvent) (*domain.JoinRequest, error) {
	if pending, err := r.GetPendingJoinRequest(ctx, groupID, userID); err == nil {
		return pending, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbErr(ctx, "create_join_request_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO group_join_request(group_id,user_id,reason,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, groupID, userID, reason, domain.JoinPending, t, t)
	if err != nil {
		return nil, dbErr(ctx, "create_join_request", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	id, _ := res.LastInsertId()
	jr, err := scanJoinRequest(tx.QueryRowContext(ctx, `SELECT `+joinReqCols+` FROM group_join_request jr JOIN user_account u ON jr.user_id=u.id WHERE jr.group_id=? AND jr.id=? LIMIT 1`, groupID, id))
	if err != nil {
		return nil, dbErr(ctx, "create_join_request_get_created", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.Int64("requestId", id))
	}
	if buildOutbox != nil {
		if err := insertOutboxTx(ctx, tx, buildOutbox(jr), t); err != nil {
			return nil, dbErr(ctx, "create_join_request_outbox", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, dbErr(ctx, "create_join_request_commit", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	logx.From(ctx).Info("db_join_request_created", zap.String("event", "db_join_request_created"), zap.Int64("groupId", groupID), zap.Int64("userId", userID), zap.Int64("requestId", id))
	return jr, nil
}

func (r *Repository) GetPendingJoinRequest(ctx context.Context, groupID, userID int64) (*domain.JoinRequest, error) {
	return scanJoinRequest(r.db.QueryRowContext(ctx, `SELECT `+joinReqCols+` FROM group_join_request jr JOIN user_account u ON jr.user_id=u.id WHERE jr.group_id=? AND jr.user_id=? AND jr.status='pending' ORDER BY jr.id DESC LIMIT 1`, groupID, userID))
}

func (r *Repository) GetJoinRequest(ctx context.Context, groupID, requestID int64) (*domain.JoinRequest, error) {
	return scanJoinRequest(r.db.QueryRowContext(ctx, `SELECT `+joinReqCols+` FROM group_join_request jr JOIN user_account u ON jr.user_id=u.id WHERE jr.group_id=? AND jr.id=? LIMIT 1`, groupID, requestID))
}

func (r *Repository) ListJoinRequests(ctx context.Context, groupID int64, cursor int64, limit int, status string) (*domain.Page[domain.JoinRequest], error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args := []any{groupID, cursor}
	whereStatus := ""
	if status != "" {
		whereStatus = " AND jr.status=?"
		args = append(args, status)
	}
	args = append(args, limit+1)
	rows, err := r.db.QueryContext(ctx, `SELECT `+joinReqCols+` FROM group_join_request jr JOIN user_account u ON jr.user_id=u.id WHERE jr.group_id=? AND jr.id>?`+whereStatus+` ORDER BY jr.id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, dbErr(ctx, "list_join_requests", err, zap.Int64("groupId", groupID))
	}
	defer rows.Close()
	items := make([]domain.JoinRequest, 0, limit)
	var next string
	for rows.Next() {
		jr, err := scanJoinRequest(rows)
		if err != nil {
			return nil, err
		}
		if len(items) >= limit {
			next = strconv.FormatInt(jr.ID, 10)
			break
		}
		items = append(items, *jr)
	}
	return &domain.Page[domain.JoinRequest]{Items: items, NextCursor: next, HasMore: next != ""}, rows.Err()
}

func (r *Repository) ApproveJoinRequest(ctx context.Context, groupID, requestID, operatorID int64, buildOutbox func(*domain.JoinRequest) *domain.OutboxEvent) (*domain.JoinRequest, error) {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbErr(ctx, "approve_join_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	defer tx.Rollback()
	var userID int64
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM group_join_request WHERE group_id=? AND id=? AND status='pending' FOR UPDATE`, groupID, requestID).Scan(&userID); err != nil {
		return nil, dbErr(ctx, "approve_join_select", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	_, err = tx.ExecContext(ctx, `UPDATE group_join_request SET status='approved', operator_id=?, updated_at=? WHERE group_id=? AND id=?`, operatorID, t, groupID, requestID)
	if err != nil {
		return nil, dbErr(ctx, "approve_join_update_request", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO group_member(group_id,user_id,role,status,last_read_sequence,joined_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status='normal', left_at=NULL, updated_at=VALUES(updated_at)`, groupID, userID, domain.RoleMember, domain.StatusNormal, 0, t, t, t)
	if err != nil {
		return nil, dbErr(ctx, "approve_join_add_member", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	_, _ = tx.ExecContext(ctx, `UPDATE chat_group SET member_count=(SELECT COUNT(*) FROM group_member WHERE group_id=? AND status='normal'), updated_at=? WHERE id=?`, groupID, t, groupID)
	_, _ = tx.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,target_user_id,action,detail,created_at) VALUES(?,?,?,?,?,?)`, groupID, operatorID, userID, "join_request_approve", jsonRaw(map[string]any{"requestId": requestID}), t)
	jr, err := scanJoinRequest(tx.QueryRowContext(ctx, `SELECT `+joinReqCols+` FROM group_join_request jr JOIN user_account u ON jr.user_id=u.id WHERE jr.group_id=? AND jr.id=? LIMIT 1`, groupID, requestID))
	if err != nil {
		return nil, dbErr(ctx, "approve_join_get_request", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	if buildOutbox != nil {
		if err := insertOutboxTx(ctx, tx, buildOutbox(jr), t); err != nil {
			return nil, dbErr(ctx, "approve_join_outbox", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, dbErr(ctx, "approve_join_commit", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	logx.From(ctx).Info("db_join_request_approved", zap.String("event", "db_join_request_approved"), zap.Int64("groupId", groupID), zap.Int64("requestId", requestID), zap.Int64("userId", userID), zap.Int64("operatorId", operatorID))
	return jr, nil
}

func (r *Repository) RejectJoinRequest(ctx context.Context, groupID, requestID, operatorID int64, buildOutbox func(*domain.JoinRequest) *domain.OutboxEvent) (*domain.JoinRequest, error) {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbErr(ctx, "reject_join_begin_tx", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE group_join_request SET status='rejected', operator_id=?, updated_at=? WHERE group_id=? AND id=? AND status='pending'`, operatorID, t, groupID, requestID)
	if err != nil {
		return nil, dbErr(ctx, "reject_join_request", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, sql.ErrNoRows
	}
	var userID int64
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM group_join_request WHERE group_id=? AND id=? LIMIT 1`, groupID, requestID).Scan(&userID); err != nil {
		return nil, dbErr(ctx, "reject_join_select_user", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,target_user_id,action,detail,created_at) VALUES(?,?,?,?,?,?)`, groupID, operatorID, userID, "join_request_reject", jsonRaw(map[string]any{"requestId": requestID, "userId": userID}), t)
	jr, err := scanJoinRequest(tx.QueryRowContext(ctx, `SELECT `+joinReqCols+` FROM group_join_request jr JOIN user_account u ON jr.user_id=u.id WHERE jr.group_id=? AND jr.id=? LIMIT 1`, groupID, requestID))
	if err != nil {
		return nil, dbErr(ctx, "reject_join_get_request", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	if buildOutbox != nil {
		if err := insertOutboxTx(ctx, tx, buildOutbox(jr), t); err != nil {
			return nil, dbErr(ctx, "reject_join_outbox", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, dbErr(ctx, "reject_join_commit", err, zap.Int64("groupId", groupID), zap.Int64("requestId", requestID))
	}
	return jr, nil
}

func (r *Repository) ListOwnerAndAdminIDs(ctx context.Context, groupID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT gm.user_id FROM group_member gm WHERE gm.group_id=? AND gm.status='normal' AND gm.role IN('owner','admin') ORDER BY gm.user_id ASC`, groupID)
	if err != nil {
		return nil, dbErr(ctx, "list_owner_admin_ids", err, zap.Int64("groupId", groupID))
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		ids = append(ids, userID)
	}
	return uniqueInt64s(ids), rows.Err()
}

func scanMention(scanner interface{ Scan(dest ...any) error }) (*domain.Mention, error) {
	m := &domain.Mention{}
	var read int
	if err := scanner.Scan(&m.ID, &m.GroupID, &m.MessageID, &m.Sequence, &m.UserID, &m.MentionType, &read, &m.Content, &m.SenderName, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.ReadStatus = read == 1
	return m, nil
}

func (r *Repository) ListMentions(ctx context.Context, groupID, userID, cursor int64, limit int, unreadOnly bool) (*domain.Page[domain.Mention], error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args := []any{groupID, userID, cursor}
	whereRead := ""
	if unreadOnly {
		whereRead = " AND mt.read_status=0"
	}
	args = append(args, limit+1)
	rows, err := r.db.QueryContext(ctx, `SELECT mt.id,mt.group_id,mt.message_id,mt.sequence,mt.user_id,mt.mention_type,mt.read_status,gm.content,gm.sender_name,mt.created_at,mt.updated_at FROM group_mention mt JOIN `+r.messageTable(groupID)+` gm ON mt.message_id=gm.message_id WHERE mt.group_id=? AND mt.user_id=? AND mt.id>?`+whereRead+` ORDER BY mt.id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, dbErr(ctx, "list_mentions", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
	}
	defer rows.Close()
	items := make([]domain.Mention, 0, limit)
	var next string
	for rows.Next() {
		m, err := scanMention(rows)
		if err != nil {
			return nil, err
		}
		if len(items) >= limit {
			next = strconv.FormatInt(m.ID, 10)
			break
		}
		items = append(items, *m)
	}
	return &domain.Page[domain.Mention]{Items: items, NextCursor: next, HasMore: next != ""}, rows.Err()
}

func (r *Repository) MarkMentionsRead(ctx context.Context, groupID, userID, sequence int64) error {
	q := `UPDATE group_mention SET read_status=1, updated_at=? WHERE group_id=? AND user_id=? AND read_status=0`
	args := []any{now(), groupID, userID}
	if sequence > 0 {
		q += ` AND sequence<=?`
		args = append(args, sequence)
	}
	_, err := r.db.ExecContext(ctx, q, args...)
	return dbErr(ctx, "mark_mentions_read", err, zap.Int64("groupId", groupID), zap.Int64("userId", userID))
}

func (r *Repository) RecallMessage(ctx context.Context, msg *domain.Message, operatorID int64, reason string, ob *domain.OutboxEvent) (*domain.RecallEvent, error) {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbErr(ctx, "recall_message_begin_tx", err, zap.Int64("groupId", msg.GroupID), zap.String("messageId", msg.MessageID))
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE `+r.messageTable(msg.GroupID)+` SET status='recalled', updated_at=? WHERE group_id=? AND message_id=? AND status <> 'recalled'`, t, msg.GroupID, msg.MessageID)
	if err != nil {
		return nil, dbErr(ctx, "recall_message_update", err, zap.Int64("groupId", msg.GroupID), zap.String("messageId", msg.MessageID))
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, sql.ErrNoRows
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO group_message_recall(group_id,message_id,operator_id,sender_id,reason,created_at) VALUES(?,?,?,?,?,?)`, msg.GroupID, msg.MessageID, operatorID, msg.SenderID, reason, t)
	if err != nil {
		return nil, dbErr(ctx, "recall_message_insert_record", err, zap.Int64("groupId", msg.GroupID), zap.String("messageId", msg.MessageID))
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,target_user_id,action,detail,created_at) VALUES(?,?,?,?,?,?)`, msg.GroupID, operatorID, msg.SenderID, "message_recall", jsonRaw(map[string]any{"messageId": msg.MessageID, "sequence": msg.Sequence, "reason": reason}), t)
	if err := insertOutboxTx(ctx, tx, ob, t); err != nil {
		return nil, dbErr(ctx, "recall_message_outbox", err, zap.Int64("groupId", msg.GroupID), zap.String("messageId", msg.MessageID))
	}
	if err := tx.Commit(); err != nil {
		return nil, dbErr(ctx, "recall_message_commit", err, zap.Int64("groupId", msg.GroupID), zap.String("messageId", msg.MessageID))
	}
	logx.From(ctx).Info("db_message_recalled", zap.String("event", "db_message_recalled"), zap.Int64("groupId", msg.GroupID), zap.String("messageId", msg.MessageID), zap.Int64("operatorId", operatorID))
	return &domain.RecallEvent{GroupID: msg.GroupID, MessageID: msg.MessageID, Sequence: msg.Sequence, OperatorID: operatorID, SenderID: msg.SenderID, Reason: reason, RecalledAt: t}, nil
}

func uniqueInt64s(in []int64) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func jsonRaw(v any) string { b, _ := json.Marshal(v); return string(b) }

// ClaimPendingOutbox 原子认领一批待发 Outbox 记录：在事务内 FOR UPDATE SKIP LOCKED 选出到期的
// pending/failed 记录并置为 sending（30s 后可被重新认领），避免多实例 relay 重复投递同一事件。
func (r *Repository) ClaimPendingOutbox(ctx context.Context, limit int) ([]domain.OutboxRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, dbErr(ctx, "claim_outbox_begin_tx", err)
	}
	defer tx.Rollback()
	// 含 'sending'：relay 认领后崩溃的记录会停留在 sending，其 next_retry_at=认领时+30s，
	// 过期后在此被重新认领，避免事件永久卡死。
	rows, err := tx.QueryContext(ctx, `SELECT id,event_id,topic,aggregate_id,payload,retry_count FROM message_outbox WHERE status IN('pending','failed','sending') AND (next_retry_at IS NULL OR next_retry_at<=NOW()) ORDER BY id ASC LIMIT ? FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, dbErr(ctx, "claim_outbox_select", err)
	}
	var out []domain.OutboxRow
	var ids []int64
	for rows.Next() {
		var row domain.OutboxRow
		var payload []byte
		if err := rows.Scan(&row.ID, &row.EventID, &row.Topic, &row.AggregateID, &payload, &row.RetryCount); err != nil {
			rows.Close()
			return nil, err
		}
		row.Payload = append([]byte(nil), payload...)
		out = append(out, row)
		ids = append(ids, row.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	t := now()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE message_outbox SET status='sending', next_retry_at=DATE_ADD(?, INTERVAL 30 SECOND), updated_at=? WHERE id=?`, t, t, id); err != nil {
			return nil, dbErr(ctx, "claim_outbox_mark_sending", err, zap.Int64("outboxId", id))
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, dbErr(ctx, "claim_outbox_commit", err)
	}
	return out, nil
}

func (r *Repository) MarkOutboxSent(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE message_outbox SET status='sent', updated_at=? WHERE id=?`, now(), id)
	return dbErr(ctx, "mark_outbox_sent", err, zap.Int64("outboxId", id))
}

// MarkOutboxRetry 将投递失败的记录退回 failed 并设置下次重试时间（指数退避由调用方计算）。
func (r *Repository) MarkOutboxRetry(ctx context.Context, id int64, retryCount int, nextRetryAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE message_outbox SET status='failed', retry_count=?, next_retry_at=?, updated_at=? WHERE id=?`, retryCount, nextRetryAt, now(), id)
	return dbErr(ctx, "mark_outbox_retry", err, zap.Int64("outboxId", id))
}

func (r *Repository) CreateOperationLog(ctx context.Context, groupID, operatorID int64, action string, detail any) {
	if _, err := r.db.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,action,detail,created_at) VALUES(?,?,?,?,?)`, groupID, operatorID, action, jsonRaw(detail), now()); err != nil {
		_ = dbErr(ctx, "create_operation_log", err, zap.Int64("groupId", groupID), zap.Int64("operatorId", operatorID), zap.String("action", action))
	}
}

func (r *Repository) DebugString() string { return fmt.Sprintf("Repository{%p}", r.db) }
