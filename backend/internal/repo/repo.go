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

	"groupflow/backend/internal/domain"
)

type Repository struct{ db *sql.DB }

func New(db *sql.DB) *Repository  { return &Repository{db: db} }
func (r *Repository) DB() *sql.DB { return r.db }

func now() time.Time { return time.Now().Truncate(time.Second) }

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
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
	res, err := r.db.ExecContext(ctx, `INSERT INTO user_account(username,nickname,avatar,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, username, username, "", "normal", t, t)
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
	return scanUser(r.db.QueryRowContext(ctx, `SELECT id,username,nickname,avatar,status,created_at,updated_at FROM user_account WHERE id=? LIMIT 1`, userID))
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
	return scanGroup(r.db.QueryRowContext(ctx, `SELECT `+groupCols+` FROM chat_group WHERE id=? LIMIT 1`, groupID))
}

func (r *Repository) CreateGroup(ctx context.Context, owner *domain.User, name, desc, avatar, joinMode, groupType string, maxMemberCount int, slowMode int) (*domain.Group, error) {
	if joinMode == "" {
		joinMode = "direct"
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
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO chat_group(name,avatar,description,owner_id,group_type,join_mode,status,mute_all,slow_mode_seconds,allow_member_invite,mention_all_role,member_count,max_member_count,max_sequence,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, name, avatar, desc, owner.ID, groupType, joinMode, "normal", 0, slowMode, 1, "admin", 1, maxMemberCount, 0, t, t)
	if err != nil {
		return nil, err
	}
	gid, _ := res.LastInsertId()
	_, err = tx.ExecContext(ctx, `INSERT INTO group_member(group_id,user_id,role,status,last_read_sequence,joined_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, gid, owner.ID, domain.RoleOwner, "normal", 0, t, t, t)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,action,detail,created_at) VALUES(?,?,?,?,?)`, gid, owner.ID, "group_create", jsonRaw(map[string]any{"name": name}), t)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetGroup(ctx, gid)
}

func (r *Repository) ListGroupsForUser(ctx context.Context, userID int64, cursor int64, limit int) (*domain.Page[domain.GroupListItem], error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.db.QueryContext(ctx, `SELECT gm.id, gm.role, gm.last_read_sequence, `+prefixGroupCols("g")+`
FROM group_member gm JOIN chat_group g ON gm.group_id=g.id
WHERE gm.user_id=? AND gm.status='normal' AND gm.id>? AND g.status <> 'dismissed'
ORDER BY gm.id ASC LIMIT ?`, userID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.GroupListItem, 0, limit)
	var nextCursor string
	for rows.Next() {
		var memberID int64
		var role string
		var lastRead int64
		g, err := scanGroupWithPrefix(rows, &memberID, &role, &lastRead)
		if err != nil {
			return nil, err
		}
		if len(items) >= limit {
			nextCursor = strconv.FormatInt(memberID, 10)
			break
		}
		items = append(items, domain.GroupListItem{Group: *g, MyRole: role, LastReadSequence: lastRead, UnreadCount: max64(0, g.MaxSequence-lastRead)})
	}
	return &domain.Page[domain.GroupListItem]{Items: items, NextCursor: nextCursor, HasMore: nextCursor != ""}, rows.Err()
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
	return scanMember(r.db.QueryRowContext(ctx, q, groupID, userID))
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
		return nil, err
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
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO group_member(group_id,user_id,role,status,last_read_sequence,joined_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status='normal', role=IF(role='owner',role,VALUES(role)), left_at=NULL, updated_at=VALUES(updated_at)`, groupID, userID, role, "normal", 0, t, t, t)
	if err != nil {
		return err
	}
	_, _ = tx.ExecContext(ctx, `UPDATE chat_group SET member_count=(SELECT COUNT(*) FROM group_member WHERE group_id=? AND status='normal'), updated_at=? WHERE id=?`, groupID, t, groupID)
	return tx.Commit()
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
	return err
}

func (r *Repository) UpdateMemberRole(ctx context.Context, groupID, userID int64, role string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE group_member SET role=?, updated_at=? WHERE group_id=? AND user_id=? AND status='normal' AND role <> 'owner'`, role, now(), groupID, userID)
	return err
}

func (r *Repository) MarkMemberStatus(ctx context.Context, groupID, userID int64, status string) error {
	t := now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE group_member SET status=?, left_at=?, updated_at=? WHERE group_id=? AND user_id=? AND role <> 'owner'`, status, t, t, groupID, userID)
	if err != nil {
		return err
	}
	_, _ = tx.ExecContext(ctx, `UPDATE chat_group SET member_count=(SELECT COUNT(*) FROM group_member WHERE group_id=? AND status='normal'), updated_at=? WHERE id=?`, groupID, t, groupID)
	return tx.Commit()
}

func (r *Repository) DismissGroup(ctx context.Context, groupID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE chat_group SET status='dismissed', updated_at=? WHERE id=?`, now(), groupID)
	return err
}

func (r *Repository) UpsertMute(ctx context.Context, groupID, userID, operatorID int64, reason string, expireAt *time.Time) error {
	t := now()
	_, _ = r.db.ExecContext(ctx, `UPDATE group_mute_record SET status='canceled', updated_at=? WHERE group_id=? AND user_id=? AND status='active'`, t, groupID, userID)
	_, err := r.db.ExecContext(ctx, `INSERT INTO group_mute_record(group_id,user_id,operator_id,reason,expire_at,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, groupID, userID, operatorID, reason, expireAt, "active", t, t)
	return err
}

func (r *Repository) CancelMute(ctx context.Context, groupID, userID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE group_mute_record SET status='canceled', updated_at=? WHERE group_id=? AND user_id=? AND status='active'`, now(), groupID, userID)
	return err
}

func (r *Repository) IsMuted(ctx context.Context, groupID, userID int64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM group_mute_record WHERE group_id=? AND user_id=? AND status='active' AND (expire_at IS NULL OR expire_at > NOW())`, groupID, userID).Scan(&n)
	return n > 0, err
}

func (r *Repository) FindMessageByClientID(ctx context.Context, senderID int64, clientID string) (*domain.Message, error) {
	return scanMessage(r.db.QueryRowContext(ctx, `SELECT `+messageCols+` FROM group_message WHERE sender_id=? AND client_message_id=? LIMIT 1`, senderID, clientID))
}

func (r *Repository) FindMessageByID(ctx context.Context, messageID string) (*domain.Message, error) {
	return scanMessage(r.db.QueryRowContext(ctx, `SELECT `+messageCols+` FROM group_message WHERE message_id=? LIMIT 1`, messageID))
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

func (r *Repository) InsertMessage(ctx context.Context, m *domain.Message) error {
	mention, _ := json.Marshal(m.MentionUserIDs)
	extra, _ := json.Marshal(m.Extra)
	t := now()
	m.CreatedAt = t
	m.UpdatedAt = t
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO group_message(message_id,group_id,sequence,sender_id,sender_name,client_message_id,message_type,content,mention_all,mention_user_ids,extra,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.MessageID, m.GroupID, m.Sequence, m.SenderID, m.SenderName, m.ClientMessageID, m.MessageType, m.Content, boolInt(m.MentionAll), string(mention), string(extra), "normal", t, t)
	if err != nil {
		return err
	}
	summary := m.Content
	if len([]rune(summary)) > 80 {
		summary = string([]rune(summary)[:80])
	}
	_, err = tx.ExecContext(ctx, `UPDATE chat_group SET max_sequence=GREATEST(max_sequence,?), last_message_id=?, last_message_summary=?, last_message_at=?, updated_at=? WHERE id=?`, m.Sequence, m.MessageID, summary, t, t, m.GroupID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) MaxSequence(ctx context.Context, groupID int64) (int64, error) {
	var max sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT MAX(sequence) FROM group_message WHERE group_id=?`, groupID).Scan(&max)
	if err != nil {
		return 0, err
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
	var rows *sql.Rows
	var err error
	if afterSeq > 0 {
		rows, err = r.db.QueryContext(ctx, `SELECT `+messageCols+` FROM group_message WHERE group_id=? AND sequence>? ORDER BY sequence ASC LIMIT ?`, groupID, afterSeq, limit+1)
	} else if beforeSeq > 0 {
		rows, err = r.db.QueryContext(ctx, `SELECT `+messageCols+` FROM group_message WHERE group_id=? AND sequence<? ORDER BY sequence DESC LIMIT ?`, groupID, beforeSeq, limit+1)
	} else {
		rows, err = r.db.QueryContext(ctx, `SELECT `+messageCols+` FROM group_message WHERE group_id=? ORDER BY sequence DESC LIMIT ?`, groupID, limit+1)
	}
	if err != nil {
		return nil, err
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
	_, err := r.db.ExecContext(ctx, `UPDATE group_member SET last_read_sequence=GREATEST(last_read_sequence,?), updated_at=? WHERE group_id=? AND user_id=? AND status='normal'`, lastRead, now(), groupID, userID)
	return err
}

func (r *Repository) ListActiveMemberIDs(ctx context.Context, groupID int64, cursor int64, limit int) ([]int64, int64, error) {
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id FROM group_member WHERE group_id=? AND status='normal' AND id>? ORDER BY id ASC LIMIT ?`, groupID, cursor, limit+1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	ids := make([]int64, 0, limit)
	var next int64
	for rows.Next() {
		var rowID, userID int64
		if err := rows.Scan(&rowID, &userID); err != nil {
			return nil, 0, err
		}
		if len(ids) >= limit {
			next = rowID
			break
		}
		ids = append(ids, userID)
	}
	return ids, next, rows.Err()
}

func jsonRaw(v any) string { b, _ := json.Marshal(v); return string(b) }

func (r *Repository) CreateOperationLog(ctx context.Context, groupID, operatorID int64, action string, detail any) {
	_, _ = r.db.ExecContext(ctx, `INSERT INTO group_operation_log(group_id,operator_id,action,detail,created_at) VALUES(?,?,?,?,?)`, groupID, operatorID, action, jsonRaw(detail), now())
}

func (r *Repository) DebugString() string { return fmt.Sprintf("Repository{%p}", r.db) }
