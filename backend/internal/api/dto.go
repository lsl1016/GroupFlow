package api

import (
	"encoding/json"
	"time"
)

// 本文件集中定义 API 层的请求入参与响应出参结构体。
//
// 约定：
//   - 请求 DTO 统一使用具名结构体，便于 Swagger 生成入参 schema 并集中维护 binding 校验。
//   - 响应 DTO 与领域模型（internal/domain）解耦：handler 不直接返回 service/repo 的领域对象，
//     而是先映射为本文件的 *DTO 再下发，避免领域模型字段变动直接泄漏到 API 契约。
//   - 响应 DTO 的 JSON 字段与现有契约保持一致，前端无需改动。映射逻辑见 dto_mapper.go。

// ============================ 请求 DTO ============================

// LoginRequest 登录请求体。
type LoginRequest struct {
	Username string `json:"username" binding:"required"` // 登录用户名，必填
}

// CreateGroupRequest 创建群请求体。
type CreateGroupRequest struct {
	Name            string `json:"name" binding:"required"`                                   // 群名称，必填
	Description     string `json:"description"`                                               // 群简介，可选
	Avatar          string `json:"avatar"`                                                    // 群头像 URL，可选
	JoinMode        string `json:"joinMode" binding:"omitempty,oneof=direct approval invite"` // 加群方式：direct 直接加入 / approval 需审批 / invite 仅邀请
	GroupType       string `json:"groupType" binding:"omitempty,oneof=normal large"`          // 群类型：normal 普通群 / large 大群
	MaxMemberCount  int    `json:"maxMemberCount" binding:"omitempty,min=1"`                  // 群人数上限，最小 1
	SlowModeSeconds int    `json:"slowModeSeconds" binding:"omitempty,min=0"`                 // 慢速模式发言间隔（秒），0 表示关闭
}

// JoinGroupRequest 加入群 / 提交加群审批请求体。
type JoinGroupRequest struct {
	Reason string `json:"reason"` // 加群申请理由，审批模式下展示给管理员，可选
}

// UpdateSettingsRequest 修改群设置请求体，指针字段为 nil 表示本次不修改该项。
type UpdateSettingsRequest struct {
	MuteAll         *bool   `json:"muteAll"`                                          // 是否全员禁言，nil 表示不变更
	SlowModeSeconds *int    `json:"slowModeSeconds" binding:"omitempty,min=0"`        // 慢速模式间隔（秒），nil 表示不变更
	GroupType       *string `json:"groupType" binding:"omitempty,oneof=normal large"` // 群类型，nil 表示不变更
	MaxMemberCount  *int    `json:"maxMemberCount" binding:"omitempty,min=1"`         // 群人数上限，nil 表示不变更
}

// SetRoleRequest 设置成员角色请求体。
type SetRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=admin member"` // 目标角色：admin 管理员 / member 普通成员，必填
}

// MuteMemberRequest 禁言成员请求体。
type MuteMemberRequest struct {
	Seconds int    `json:"seconds" binding:"min=0"` // 禁言时长（秒），0 表示立即解除
	Reason  string `json:"reason"`                  // 禁言原因，可选
}

// RecallMessageRequest 撤回消息请求体。
type RecallMessageRequest struct {
	Reason string `json:"reason"` // 撤回原因，可选
}

// ReadRequest 更新已读位置请求体。
type ReadRequest struct {
	LastReadSequence int64 `json:"lastReadSequence" binding:"min=0"` // 已读到的最大消息序号
}

// ReadMentionsRequest 标记 @ 提醒已读请求体。
type ReadMentionsRequest struct {
	Sequence int64 `json:"sequence" binding:"min=0"` // 已读到的 @ 提醒消息序号
}

// CreateAnnouncementRequest 发布群公告请求体。
type CreateAnnouncementRequest struct {
	Title   string `json:"title"`                      // 公告标题，可选
	Content string `json:"content" binding:"required"` // 公告正文，必填
	Pinned  bool   `json:"pinned"`                     // 是否置顶
}

// UpdateAnnouncementRequest 编辑群公告请求体，Pinned 为 nil 表示不修改置顶状态。
type UpdateAnnouncementRequest struct {
	Title   string `json:"title"`   // 公告标题
	Content string `json:"content"` // 公告正文
	Pinned  *bool  `json:"pinned"`  // 是否置顶，nil 表示不变更
}

// InternalPushRequest 内部服务（Delivery）回推 WS 消息的请求体。
type InternalPushRequest struct {
	UserIDs       []int64         `json:"userIds,omitempty"`       // 兼容旧路径：目标用户 ID 列表
	ConnectionIDs []string        `json:"connectionIds,omitempty"` // 优先路径：目标连接 ID 列表
	Type          string          `json:"type"`                    // WS 事件类型，缺省为 group_message_receive
	Data          json.RawMessage `json:"data"`                    // 透传的业务负载，原样下发
}

// ============================ 响应 DTO ============================

// PageDTO 通用游标分页响应。T 为具体的响应 DTO 元素类型。
type PageDTO[T any] struct {
	Items      []T    `json:"items"`      // 当前页数据
	NextCursor string `json:"nextCursor"` // 下一页游标，空串表示无更多
	HasMore    bool   `json:"hasMore"`    // 是否还有更多数据
}

// UserDTO 用户信息响应。
type UserDTO struct {
	UserID    int64     `json:"userId"`    // 用户 ID
	Username  string    `json:"username"`  // 用户名
	Nickname  string    `json:"nickname"`  // 昵称
	Avatar    string    `json:"avatar"`    // 头像 URL
	Status    string    `json:"status"`    // 账号状态
	CreatedAt time.Time `json:"createdAt"` // 创建时间
	UpdatedAt time.Time `json:"updatedAt"` // 更新时间
}

// LoginResponse 登录成功响应，携带用户基础信息与访问令牌。
type LoginResponse struct {
	UserID   int64  `json:"userId"`   // 用户 ID
	Username string `json:"username"` // 用户名
	Nickname string `json:"nickname"` // 昵称
	Avatar   string `json:"avatar"`   // 头像 URL
	Token    string `json:"token"`    // 访问令牌（JWT）
}

// GroupDTO 群基础信息响应。
type GroupDTO struct {
	GroupID            int64      `json:"groupId"`                 // 群 ID
	Name               string     `json:"name"`                    // 群名称
	Avatar             string     `json:"avatar"`                  // 群头像 URL
	Description        string     `json:"description"`             // 群简介
	OwnerID            int64      `json:"ownerId"`                 // 群主用户 ID
	GroupType          string     `json:"groupType"`               // 群类型：normal / large
	JoinMode           string     `json:"joinMode"`                // 加群方式：direct / approval / invite
	Status             string     `json:"status"`                  // 群状态：normal / dismissed
	MuteAll            bool       `json:"muteAll"`                 // 是否全员禁言
	SlowModeSeconds    int        `json:"slowModeSeconds"`         // 慢速模式发言间隔（秒）
	AllowMemberInvite  bool       `json:"allowMemberInvite"`       // 是否允许普通成员邀请
	MentionAllRole     string     `json:"mentionAllRole"`          // 允许 @全体 的最低角色
	MemberCount        int        `json:"memberCount"`             // 当前成员数
	MaxMemberCount     int        `json:"maxMemberCount"`          // 成员数上限
	MaxSequence        int64      `json:"maxSequence"`             // 群内当前最大消息序号
	LastMessageID      string     `json:"lastMessageId"`           // 最新一条消息 ID
	LastMessageSummary string     `json:"lastMessageSummary"`      // 最新消息摘要（列表展示用）
	LastMessageAt      *time.Time `json:"lastMessageAt,omitempty"` // 最新消息时间，无消息时为空
	CreatedAt          time.Time  `json:"createdAt"`               // 建群时间
	UpdatedAt          time.Time  `json:"updatedAt"`               // 更新时间
}

// GroupListItemDTO 群列表项，在群信息基础上附加当前用户维度的未读 / @ 状态。
type GroupListItemDTO struct {
	GroupDTO
	MyRole             string `json:"myRole"`             // 当前用户在该群的角色
	LastReadSequence   int64  `json:"lastReadSequence"`   // 当前用户已读到的序号
	UnreadCount        int64  `json:"unreadCount"`        // 未读消息数
	MentionCount       int64  `json:"mentionCount"`       // 未读 @ 我的条数
	MentionAllUnread   bool   `json:"mentionAllUnread"`   // 是否存在未读的 @全体
	MentionSummaryText string `json:"mentionSummaryText"` // @ 摘要文案（如“[有人@我]”）
}

// MemberDTO 群成员响应。
type MemberDTO struct {
	ID               int64      `json:"id"`               // 成员记录 ID
	GroupID          int64      `json:"groupId"`          // 群 ID
	UserID           int64      `json:"userId"`           // 用户 ID
	Username         string     `json:"username"`         // 用户名
	Nickname         string     `json:"nickname"`         // 群内昵称
	Avatar           string     `json:"avatar"`           // 头像 URL
	Role             string     `json:"role"`             // 群内角色：owner / admin / member
	Status           string     `json:"status"`           // 成员状态：normal / left / kicked
	LastReadSequence int64      `json:"lastReadSequence"` // 已读到的消息序号
	JoinedAt         time.Time  `json:"joinedAt"`         // 入群时间
	LeftAt           *time.Time `json:"leftAt,omitempty"` // 离群时间，在群时为空
	CreatedAt        time.Time  `json:"createdAt"`        // 记录创建时间
	UpdatedAt        time.Time  `json:"updatedAt"`        // 记录更新时间
}

// MessageDTO 群消息响应。
type MessageDTO struct {
	ID              int64          `json:"id"`              // 消息记录 ID
	MessageID       string         `json:"messageId"`       // 业务消息 ID（全局唯一）
	GroupID         int64          `json:"groupId"`         // 群 ID
	Sequence        int64          `json:"sequence"`        // 群内消息序号
	SenderID        int64          `json:"senderId"`        // 发送者用户 ID
	SenderName      string         `json:"senderName"`      // 发送者昵称
	ClientMessageID string         `json:"clientMessageId"` // 客户端消息 ID（用于去重 / 回执匹配）
	MessageType     string         `json:"messageType"`     // 消息类型：text / system
	Content         string         `json:"content"`         // 消息内容
	MentionAll      bool           `json:"mentionAll"`      // 是否 @全体
	MentionUserIDs  []int64        `json:"mentionUserIds"`  // 被 @ 的用户 ID 列表
	Extra           map[string]any `json:"extra"`           // 扩展字段（系统消息事件等）
	Status          string         `json:"status"`          // 消息状态：normal / recalled
	CreatedAt       time.Time      `json:"createdAt"`       // 发送时间
	UpdatedAt       time.Time      `json:"updatedAt"`       // 更新时间
}

// AnnouncementDTO 群公告响应。
type AnnouncementDTO struct {
	AnnouncementID int64     `json:"announcementId"` // 公告 ID
	GroupID        int64     `json:"groupId"`        // 群 ID
	OperatorID     int64     `json:"operatorId"`     // 发布 / 修改者用户 ID
	OperatorName   string    `json:"operatorName"`   // 操作者昵称
	Title          string    `json:"title"`          // 公告标题
	Content        string    `json:"content"`        // 公告正文
	Pinned         bool      `json:"pinned"`         // 是否置顶
	Status         string    `json:"status"`         // 公告状态
	CreatedAt      time.Time `json:"createdAt"`      // 创建时间
	UpdatedAt      time.Time `json:"updatedAt"`      // 更新时间
}

// JoinRequestDTO 加群审批记录响应。
type JoinRequestDTO struct {
	RequestID  int64     `json:"requestId"`            // 审批记录 ID
	GroupID    int64     `json:"groupId"`              // 群 ID
	UserID     int64     `json:"userId"`               // 申请人用户 ID
	Username   string    `json:"username"`             // 申请人用户名
	Nickname   string    `json:"nickname"`             // 申请人昵称
	Avatar     string    `json:"avatar"`               // 申请人头像 URL
	Reason     string    `json:"reason"`               // 申请理由
	Status     string    `json:"status"`               // 审批状态：pending / approved / rejected
	OperatorID *int64    `json:"operatorId,omitempty"` // 审批人用户 ID，未处理时为空
	CreatedAt  time.Time `json:"createdAt"`            // 申请时间
	UpdatedAt  time.Time `json:"updatedAt"`            // 更新时间
}

// MentionDTO @ 提醒响应。
type MentionDTO struct {
	MentionID   int64     `json:"mentionId"`   // 提醒记录 ID
	GroupID     int64     `json:"groupId"`     // 群 ID
	MessageID   string    `json:"messageId"`   // 关联消息 ID
	Sequence    int64     `json:"sequence"`    // 关联消息序号
	UserID      int64     `json:"userId"`      // 被提醒的用户 ID
	MentionType string    `json:"mentionType"` // 提醒类型：user 指定 / all 全体
	ReadStatus  bool      `json:"readStatus"`  // 是否已读
	Content     string    `json:"content"`     // 消息内容摘要
	SenderName  string    `json:"senderName"`  // 发送者昵称
	CreatedAt   time.Time `json:"createdAt"`   // 创建时间
	UpdatedAt   time.Time `json:"updatedAt"`   // 更新时间
}

// RecallEventDTO 消息撤回事件响应。
type RecallEventDTO struct {
	GroupID    int64     `json:"groupId"`          // 群 ID
	MessageID  string    `json:"messageId"`        // 被撤回的消息 ID
	Sequence   int64     `json:"sequence"`         // 被撤回消息的序号
	OperatorID int64     `json:"operatorId"`       // 执行撤回的用户 ID
	SenderID   int64     `json:"senderId"`         // 原消息发送者用户 ID
	Reason     string    `json:"reason,omitempty"` // 撤回原因，可选
	RecalledAt time.Time `json:"recalledAt"`       // 撤回时间
}

// JoinGroupResponse 加群 / 提交审批结果响应。
type JoinGroupResponse struct {
	Joined  bool            `json:"joined"`            // 是否已直接加入
	Pending bool            `json:"pending"`           // 是否进入待审批状态
	Request *JoinRequestDTO `json:"request,omitempty"` // 审批模式下生成的审批记录，直接加入时为空
}

// GroupDetailResponse 群详情响应，聚合群信息、当前用户成员信息与在线用户。
type GroupDetailResponse struct {
	Group         GroupDTO   `json:"group"`         // 群基础信息
	MyMember      *MemberDTO `json:"myMember"`      // 当前用户的成员信息，非成员时为空
	OnlineUserIDs []int64    `json:"onlineUserIds"` // 当前在线用户 ID 列表
}
