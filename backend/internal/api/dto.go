package api

import "encoding/json"

// 统一请求入参定义。所有写接口的请求体均使用具名结构体，
// 便于 Swagger 生成入参 schema，并集中维护 binding 校验规则。

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
}

type CreateGroupRequest struct {
	Name            string `json:"name" binding:"required"`
	Description     string `json:"description"`
	Avatar          string `json:"avatar"`
	JoinMode        string `json:"joinMode" binding:"omitempty,oneof=direct approval invite"`
	GroupType       string `json:"groupType" binding:"omitempty,oneof=normal large"`
	MaxMemberCount  int    `json:"maxMemberCount" binding:"omitempty,min=1"`
	SlowModeSeconds int    `json:"slowModeSeconds" binding:"omitempty,min=0"`
}

type JoinGroupRequest struct {
	Reason string `json:"reason"`
}

type UpdateSettingsRequest struct {
	MuteAll         *bool   `json:"muteAll"`
	SlowModeSeconds *int    `json:"slowModeSeconds" binding:"omitempty,min=0"`
	GroupType       *string `json:"groupType" binding:"omitempty,oneof=normal large"`
	MaxMemberCount  *int    `json:"maxMemberCount" binding:"omitempty,min=1"`
}

type SetRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=admin member"`
}

type MuteMemberRequest struct {
	Seconds int    `json:"seconds" binding:"min=0"`
	Reason  string `json:"reason"`
}

type RecallMessageRequest struct {
	Reason string `json:"reason"`
}

type ReadRequest struct {
	LastReadSequence int64 `json:"lastReadSequence" binding:"min=0"`
}

type ReadMentionsRequest struct {
	Sequence int64 `json:"sequence" binding:"min=0"`
}

type CreateAnnouncementRequest struct {
	Title   string `json:"title"`
	Content string `json:"content" binding:"required"`
	Pinned  bool   `json:"pinned"`
}

type UpdateAnnouncementRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Pinned  *bool  `json:"pinned"`
}

type InternalPushRequest struct {
	UserIDs []int64         `json:"userIds"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
}
