package domain

import "time"

const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"

	GroupNormal = "normal"
	GroupLarge  = "large"

	StatusNormal    = "normal"
	StatusDismissed = "dismissed"
	StatusRecalled  = "recalled"

	MemberLeft   = "left"
	MemberKicked = "kicked"

	MessageText   = "text"
	MessageSystem = "system"

	JoinModeDirect   = "direct"
	JoinModeApproval = "approval"
	JoinModeInvite   = "invite"

	JoinPending  = "pending"
	JoinApproved = "approved"
	JoinRejected = "rejected"

	MentionUser = "user"
	MentionAll  = "all"
)

type User struct {
	ID        int64     `json:"userId"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	Avatar    string    `json:"avatar"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Group struct {
	ID                 int64      `json:"groupId"`
	Name               string     `json:"name"`
	Avatar             string     `json:"avatar"`
	Description        string     `json:"description"`
	OwnerID            int64      `json:"ownerId"`
	GroupType          string     `json:"groupType"`
	JoinMode           string     `json:"joinMode"`
	Status             string     `json:"status"`
	MuteAll            bool       `json:"muteAll"`
	SlowModeSeconds    int        `json:"slowModeSeconds"`
	AllowMemberInvite  bool       `json:"allowMemberInvite"`
	MentionAllRole     string     `json:"mentionAllRole"`
	MemberCount        int        `json:"memberCount"`
	MaxMemberCount     int        `json:"maxMemberCount"`
	MaxSequence        int64      `json:"maxSequence"`
	LastMessageID      string     `json:"lastMessageId"`
	LastMessageSummary string     `json:"lastMessageSummary"`
	LastMessageAt      *time.Time `json:"lastMessageAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type GroupListItem struct {
	Group
	MyRole             string `json:"myRole"`
	LastReadSequence   int64  `json:"lastReadSequence"`
	UnreadCount        int64  `json:"unreadCount"`
	MentionCount       int64  `json:"mentionCount"`
	MentionAllUnread   bool   `json:"mentionAllUnread"`
	MentionSummaryText string `json:"mentionSummaryText"`
}

type Member struct {
	ID               int64      `json:"id"`
	GroupID          int64      `json:"groupId"`
	UserID           int64      `json:"userId"`
	Username         string     `json:"username"`
	Nickname         string     `json:"nickname"`
	Avatar           string     `json:"avatar"`
	Role             string     `json:"role"`
	Status           string     `json:"status"`
	LastReadSequence int64      `json:"lastReadSequence"`
	JoinedAt         time.Time  `json:"joinedAt"`
	LeftAt           *time.Time `json:"leftAt,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type Message struct {
	ID              int64          `json:"id"`
	MessageID       string         `json:"messageId"`
	GroupID         int64          `json:"groupId"`
	Sequence        int64          `json:"sequence"`
	SenderID        int64          `json:"senderId"`
	SenderName      string         `json:"senderName"`
	ClientMessageID string         `json:"clientMessageId"`
	MessageType     string         `json:"messageType"`
	Content         string         `json:"content"`
	MentionAll      bool           `json:"mentionAll"`
	MentionUserIDs  []int64        `json:"mentionUserIds"`
	Extra           map[string]any `json:"extra"`
	Status          string         `json:"status"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

type Announcement struct {
	ID           int64     `json:"announcementId"`
	GroupID      int64     `json:"groupId"`
	OperatorID   int64     `json:"operatorId"`
	OperatorName string    `json:"operatorName"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	Pinned       bool      `json:"pinned"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type JoinRequest struct {
	ID         int64     `json:"requestId"`
	GroupID    int64     `json:"groupId"`
	UserID     int64     `json:"userId"`
	Username   string    `json:"username"`
	Nickname   string    `json:"nickname"`
	Avatar     string    `json:"avatar"`
	Reason     string    `json:"reason"`
	Status     string    `json:"status"`
	OperatorID *int64    `json:"operatorId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Mention struct {
	ID          int64     `json:"mentionId"`
	GroupID     int64     `json:"groupId"`
	MessageID   string    `json:"messageId"`
	Sequence    int64     `json:"sequence"`
	UserID      int64     `json:"userId"`
	MentionType string    `json:"mentionType"`
	ReadStatus  bool      `json:"readStatus"`
	Content     string    `json:"content"`
	SenderName  string    `json:"senderName"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type RecallEvent struct {
	GroupID    int64     `json:"groupId"`
	MessageID  string    `json:"messageId"`
	Sequence   int64     `json:"sequence"`
	OperatorID int64     `json:"operatorId"`
	SenderID   int64     `json:"senderId"`
	Reason     string    `json:"reason,omitempty"`
	RecalledAt time.Time `json:"recalledAt"`
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor"`
	HasMore    bool   `json:"hasMore"`
}
