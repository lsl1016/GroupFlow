package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"

	_ "groupflow/backend/docs"
	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
	"groupflow/backend/internal/repo"
	"groupflow/backend/internal/service"
	"groupflow/backend/internal/ws"
	"groupflow/backend/pkg/auth"
	"groupflow/backend/pkg/id"
	"groupflow/backend/pkg/logx"
)

type Router struct {
	cfg  config.Config
	svc  *service.Service
	repo *repo.Repository
	hub  *ws.Hub
	log  *zap.Logger
}

func NewRouter(cfg config.Config, svc *service.Service, repo *repo.Repository, hub *ws.Hub, log *zap.Logger) *gin.Engine {
	r := &Router{cfg: cfg, svc: svc, repo: repo, hub: hub, log: log}
	gin.SetMode(gin.ReleaseMode)
	g := gin.New()
	g.Use(gin.Recovery(), r.traceMiddleware())
	g.GET("/metrics", gin.WrapH(promhttp.Handler()))
	g.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	g.GET("/ws", r.wsUpgrade)
	g.GET("/api/ws", r.wsUpgrade)

	v1 := g.Group("/api/v1")
	v1.GET("/health", r.health)
	v1.POST("/auth/login", r.login)
	v1.GET("/auth/me", r.authRequired(), r.me)

	groups := v1.Group("/groups", r.authRequired())
	groups.GET("", r.listGroups)
	groups.POST("", r.createGroup)
	groups.GET("/:groupId", r.groupDetail)
	groups.POST("/:groupId/join", r.joinGroup)
	groups.GET("/:groupId/members", r.members)
	groups.POST("/:groupId/leave", r.leaveGroup)
	groups.DELETE("/:groupId", r.dismissGroup)
	groups.PATCH("/:groupId/settings", r.updateSettings)
	groups.POST("/:groupId/members/:userId/role", r.setRole)
	groups.DELETE("/:groupId/members/:userId", r.kickMember)
	groups.POST("/:groupId/members/:userId/mute", r.muteMember)
	groups.DELETE("/:groupId/members/:userId/mute", r.unmuteMember)
	groups.GET("/:groupId/messages", r.messages)
	groups.POST("/:groupId/messages/:messageId/recall", r.recallMessage)
	groups.POST("/:groupId/read", r.read)
	groups.GET("/:groupId/mentions", r.mentions)
	groups.POST("/:groupId/mentions/read", r.readMentions)
	groups.GET("/:groupId/announcements", r.announcements)
	groups.POST("/:groupId/announcements", r.createAnnouncement)
	groups.PUT("/:groupId/announcements/:announcementId", r.updateAnnouncement)
	groups.DELETE("/:groupId/announcements/:announcementId", r.deleteAnnouncement)
	groups.GET("/:groupId/join-requests", r.joinRequests)
	groups.POST("/:groupId/join-requests/:requestId/approve", r.approveJoinRequest)
	groups.POST("/:groupId/join-requests/:requestId/reject", r.rejectJoinRequest)

	g.POST("/internal/push", r.internalPush)

	hub.OnMessage = r.onWSMessage
	return g
}

// traceMiddleware 注入 traceId 并输出 HTTP 访问日志：5xx 记 ERROR，超阈值记慢请求 WARN，其余 INFO。
func (r *Router) traceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		ensureTraceID(c)
		c.Next()
		ms := time.Since(start).Milliseconds()
		status := c.Writer.Status()
		lg := logx.From(c.Request.Context())
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Int64("durationMs", ms),
			zap.String("clientIp", c.ClientIP()),
		}
		switch {
		case status >= 500:
			lg.Error("http_request_failed", append(fields, zap.String("event", "http_request_failed"))...)
		case ms >= logx.HTTPSlowMs():
			lg.Warn("http_slow_request", append(fields, zap.String("event", "http_slow_request"))...)
		default:
			lg.Info("http_request", append(fields, zap.String("event", "http_request"))...)
		}
	}
}

func (r *Router) authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := r.parseBearer(c.GetHeader("Authorization"))
		if err != nil {
			Fail(c, http.StatusUnauthorized, "AUTH_FAILED", err.Error())
			c.Abort()
			return
		}
		c.Set("userId", claims.UserID)
		c.Set("username", claims.Username)
		setUserContext(c, claims.UserID)
		c.Next()
	}
}

func (r *Router) parseBearer(h string) (*auth.Claims, error) {
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, errors.New("missing bearer token")
	}
	return auth.Parse(r.cfg.AuthSecret, strings.TrimPrefix(h, "Bearer "))
}

// bindJSON 统一请求体解析与校验，失败时返回 400 BAD_REQUEST 并终止后续处理。
func bindJSON(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		Fail(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return false
	}
	return true
}

// uid 从请求上下文中获取当前用户 ID。
func uid(c *gin.Context) int64 { v, _ := c.Get("userId"); n, _ := v.(int64); return n }
func paramInt(c *gin.Context, name string) (int64, error) {
	return strconv.ParseInt(c.Param(name), 10, 64)
}
func queryInt(c *gin.Context, name string) int64 {
	n, _ := strconv.ParseInt(c.Query(name), 10, 64)
	return n
}
func limit(c *gin.Context, def int) int {
	n, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(def)))
	return n
}

// health godoc
// @Summary 健康检查
// @Tags system
// @Produce json
// @Success 200 {object} Response
// @Router /health [get]
func (r *Router) health(c *gin.Context) {
	OK(c, gin.H{"status": "ok", "time": time.Now().Format(time.RFC3339)})
}

// login godoc
// @Summary 用户登录
// @Tags auth
// @Accept json
// @Produce json
// @Param body body LoginRequest true "登录信息"
// @Success 200 {object} Response
// @Router /auth/login [post]
func (r *Router) login(c *gin.Context) {
	var req LoginRequest
	if !bindJSON(c, &req) {
		return
	}
	u, t, err := r.svc.Login(c.Request.Context(), req.Username)
	if err != nil {
		Fail(c, 400, "AUTH_FAILED", err.Error())
		return
	}
	OK(c, LoginResponse{UserID: u.ID, Username: u.Username, Nickname: u.Nickname, Avatar: u.Avatar, Token: t})
}

// me godoc
// @Summary 当前用户信息
// @Tags auth
// @Security BearerAuth
// @Success 200 {object} Response
// @Router /auth/me [get]
func (r *Router) me(c *gin.Context) {
	u, err := r.repo.GetUser(c.Request.Context(), uid(c))
	if err != nil {
		Fail(c, 404, "USER_NOT_FOUND", err.Error())
		return
	}
	OK(c, toUserDTO(u))
}

// listGroups godoc
// @Summary 群列表，包含未读数和 @ 提醒
// @Tags group
// @Security BearerAuth
// @Param cursor query string false "游标"
// @Param limit query int false "数量"
// @Success 200 {object} Response
// @Router /groups [get]
func (r *Router) listGroups(c *gin.Context) {
	page, err := r.repo.ListGroupsForUser(c.Request.Context(), uid(c), queryInt(c, "cursor"), limit(c, 30))
	if err != nil {
		Fail(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	OK(c, toPageDTO(page, toGroupListItemDTO))
}

// createGroup godoc
// @Summary 创建群
// @Tags group
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body CreateGroupRequest true "群信息"
// @Success 200 {object} Response
// @Router /groups [post]
func (r *Router) createGroup(c *gin.Context) {
	var req CreateGroupRequest
	if !bindJSON(c, &req) {
		return
	}
	g, msg, err := r.svc.CreateGroup(c.Request.Context(), uid(c), req.Name, req.Description, req.Avatar, req.JoinMode, req.GroupType, req.MaxMemberCount, req.SlowModeSeconds)
	if err != nil {
		Fail(c, 400, "CREATE_GROUP_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, toGroupDTO(*g))
}

// groupDetail godoc
// @Summary 群详情
// @Tags group
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Success 200 {object} Response
// @Router /groups/{groupId} [get]
func (r *Router) groupDetail(c *gin.Context) {
	gid, err := paramInt(c, "groupId")
	if err != nil {
		Fail(c, 400, "BAD_REQUEST", "invalid groupId")
		return
	}
	if !r.repo.IsActiveMember(c.Request.Context(), gid, uid(c)) {
		Fail(c, 403, "FORBIDDEN", "not group member")
		return
	}
	g, err := r.repo.GetGroup(c.Request.Context(), gid)
	if err != nil {
		Fail(c, 404, "GROUP_NOT_FOUND", err.Error())
		return
	}
	m, _ := r.repo.GetMember(c.Request.Context(), gid, uid(c))
	OK(c, GroupDetailResponse{Group: toGroupDTO(*g), MyMember: toMemberDTOPtr(m), OnlineUserIDs: r.hub.OnlineUserIDs()})
}

// joinGroup godoc
// @Summary 加入群或提交加群审批
// @Tags group
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param groupId path int true "群ID"
// @Param body body JoinGroupRequest false "加群申请理由"
// @Success 200 {object} Response
// @Router /groups/{groupId}/join [post]
func (r *Router) joinGroup(c *gin.Context) {
	gid, err := paramInt(c, "groupId")
	if err != nil {
		Fail(c, 400, "BAD_REQUEST", "invalid groupId")
		return
	}
	var req JoinGroupRequest
	if !bindJSON(c, &req) {
		return
	}
	result, err := r.svc.JoinGroup(c.Request.Context(), gid, uid(c), req.Reason)
	if err != nil {
		Fail(c, 400, "JOIN_GROUP_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), result.Message)
	if result.RealtimeEvent != nil {
		r.pushRealtimeEventIfDirect(c.Request.Context(), result.RealtimeEvent)
	}
	OK(c, toJoinGroupResponse(result))
}

// members godoc
// @Summary 群成员列表
// @Tags member
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Param cursor query string false "游标"
// @Param limit query int false "数量"
// @Param role query string false "按角色过滤"
// @Success 200 {object} Response
// @Router /groups/{groupId}/members [get]
func (r *Router) members(c *gin.Context) {
	gid, err := paramInt(c, "groupId")
	if err != nil {
		Fail(c, 400, "BAD_REQUEST", "invalid groupId")
		return
	}
	if !r.repo.IsActiveMember(c.Request.Context(), gid, uid(c)) {
		Fail(c, 403, "FORBIDDEN", "not group member")
		return
	}
	page, err := r.repo.ListMembers(c.Request.Context(), gid, queryInt(c, "cursor"), limit(c, 50), c.Query("role"))
	if err != nil {
		Fail(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	OK(c, toPageDTO(page, toMemberDTO))
}

// leaveGroup godoc
// @Summary 退出群
// @Tags group
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Success 200 {object} Response
// @Router /groups/{groupId}/leave [post]
func (r *Router) leaveGroup(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	msg, err := r.svc.LeaveGroup(c.Request.Context(), gid, uid(c))
	if err != nil {
		Fail(c, 400, "LEAVE_GROUP_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"left": true})
}

// dismissGroup godoc
// @Summary 解散群
// @Tags group
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Success 200 {object} Response
// @Router /groups/{groupId} [delete]
func (r *Router) dismissGroup(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	msg, err := r.svc.DismissGroup(c.Request.Context(), gid, uid(c))
	if err != nil {
		Fail(c, 403, "DISMISS_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"dismissed": true})
}

// updateSettings godoc
// @Summary 修改群设置（全员禁言/慢速模式/群类型/人数上限）
// @Tags group
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param groupId path int true "群ID"
// @Param body body UpdateSettingsRequest true "群设置"
// @Success 200 {object} Response
// @Router /groups/{groupId}/settings [patch]
func (r *Router) updateSettings(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	var req UpdateSettingsRequest
	if !bindJSON(c, &req) {
		return
	}
	msg, err := r.svc.UpdateSettings(c.Request.Context(), gid, uid(c), req.MuteAll, req.SlowModeSeconds, req.GroupType, req.MaxMemberCount)
	if err != nil {
		Fail(c, 403, "UPDATE_SETTINGS_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"updated": true})
}

// setRole godoc
// @Summary 设置成员角色
// @Tags member
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param groupId path int true "群ID"
// @Param userId path int true "成员用户ID"
// @Param body body SetRoleRequest true "目标角色"
// @Success 200 {object} Response
// @Router /groups/{groupId}/members/{userId}/role [post]
func (r *Router) setRole(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	tid, _ := paramInt(c, "userId")
	var req SetRoleRequest
	if !bindJSON(c, &req) {
		return
	}
	msg, err := r.svc.SetMemberRole(c.Request.Context(), gid, uid(c), tid, req.Role)
	if err != nil {
		Fail(c, 403, "SET_ROLE_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"updated": true})
}

// kickMember godoc
// @Summary 踢出群成员
// @Tags member
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Param userId path int true "成员用户ID"
// @Success 200 {object} Response
// @Router /groups/{groupId}/members/{userId} [delete]
func (r *Router) kickMember(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	tid, _ := paramInt(c, "userId")
	msg, evt, err := r.svc.KickMember(c.Request.Context(), gid, uid(c), tid)
	if err != nil {
		Fail(c, 403, "KICK_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	r.pushRealtimeEventIfDirect(c.Request.Context(), evt)
	OK(c, gin.H{"kicked": true})
}

// muteMember godoc
// @Summary 禁言成员
// @Tags member
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param groupId path int true "群ID"
// @Param userId path int true "成员用户ID"
// @Param body body MuteMemberRequest true "禁言时长与原因"
// @Success 200 {object} Response
// @Router /groups/{groupId}/members/{userId}/mute [post]
func (r *Router) muteMember(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	tid, _ := paramInt(c, "userId")
	var req MuteMemberRequest
	if !bindJSON(c, &req) {
		return
	}
	msg, err := r.svc.MuteMember(c.Request.Context(), gid, uid(c), tid, req.Seconds, req.Reason)
	if err != nil {
		Fail(c, 403, "MUTE_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"muted": true})
}

// unmuteMember godoc
// @Summary 解除成员禁言
// @Tags member
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Param userId path int true "成员用户ID"
// @Success 200 {object} Response
// @Router /groups/{groupId}/members/{userId}/mute [delete]
func (r *Router) unmuteMember(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	tid, _ := paramInt(c, "userId")
	msg, err := r.svc.UnmuteMember(c.Request.Context(), gid, uid(c), tid)
	if err != nil {
		Fail(c, 403, "UNMUTE_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"unmuted": true})
}

// messages godoc
// @Summary 历史消息游标分页 / 断线补拉
// @Tags message
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Param beforeSequence query int false "向上翻页"
// @Param afterSequence query int false "断线补拉"
// @Success 200 {object} Response
// @Router /groups/{groupId}/messages [get]
func (r *Router) messages(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	if !r.repo.IsActiveMember(c.Request.Context(), gid, uid(c)) {
		Fail(c, 403, "FORBIDDEN", "not group member")
		return
	}
	before := queryInt(c, "beforeSequence")
	after := queryInt(c, "afterSequence")
	page, err := r.repo.ListMessages(c.Request.Context(), gid, before, after, limit(c, 50))
	if err != nil {
		Fail(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	OK(c, toPageDTO(page, toMessageDTO))
}

// recallMessage godoc
// @Summary 撤回消息
// @Tags message
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Param messageId path string true "消息ID"
// @Param body body RecallMessageRequest false "撤回原因"
// @Success 200 {object} Response
// @Router /groups/{groupId}/messages/{messageId}/recall [post]
func (r *Router) recallMessage(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	messageID := c.Param("messageId")
	var req RecallMessageRequest
	if !bindJSON(c, &req) {
		return
	}
	evt, err := r.svc.RecallMessage(c.Request.Context(), gid, uid(c), messageID, req.Reason)
	if err != nil {
		Fail(c, 403, "RECALL_FAILED", err.Error())
		return
	}
	// 撤回不是新消息，不占用 sequence；通过 WS 事件更新客户端本地消息状态。
	r.pushEventIfDirect(c.Request.Context(), gid, "group_message_recalled", evt)
	OK(c, toRecallEventDTO(evt))
}

// read godoc
// @Summary 更新已读位置
// @Tags message
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param groupId path int true "群ID"
// @Param body body ReadRequest true "已读位置"
// @Success 200 {object} Response
// @Router /groups/{groupId}/read [post]
func (r *Router) read(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	var req ReadRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := r.svc.UpdateReadPosition(c.Request.Context(), gid, uid(c), req.LastReadSequence); err != nil {
		switch {
		case errors.Is(err, service.ErrReadSequenceRollback):
			Fail(c, 400, "READ_SEQUENCE_ROLLBACK", "last read sequence cannot roll back")
		case errors.Is(err, service.ErrForbidden):
			Fail(c, 403, "FORBIDDEN", "not group member")
		default:
			Fail(c, 500, "READ_FAILED", err.Error())
		}
		return
	}
	OK(c, gin.H{"lastReadSequence": req.LastReadSequence})
}

// mentions godoc
// @Summary @ 提醒列表
// @Tags message
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Param cursor query string false "游标"
// @Param limit query int false "数量"
// @Param unreadOnly query bool false "仅未读，默认 true"
// @Success 200 {object} Response
// @Router /groups/{groupId}/mentions [get]
func (r *Router) mentions(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	if !r.repo.IsActiveMember(c.Request.Context(), gid, uid(c)) {
		Fail(c, 403, "FORBIDDEN", "not group member")
		return
	}
	page, err := r.repo.ListMentions(c.Request.Context(), gid, uid(c), queryInt(c, "cursor"), limit(c, 30), c.DefaultQuery("unreadOnly", "true") != "false")
	if err != nil {
		Fail(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	OK(c, toPageDTO(page, toMentionDTO))
}

// readMentions godoc
// @Summary 标记 @ 提醒已读
// @Tags message
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param groupId path int true "群ID"
// @Param body body ReadMentionsRequest true "已读到的 @ 序号"
// @Success 200 {object} Response
// @Router /groups/{groupId}/mentions/read [post]
func (r *Router) readMentions(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	var req ReadMentionsRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := r.repo.MarkMentionsRead(c.Request.Context(), gid, uid(c), req.Sequence); err != nil {
		Fail(c, 500, "MENTION_READ_FAILED", err.Error())
		return
	}
	OK(c, gin.H{"read": true})
}

// announcements godoc
// @Summary 群公告列表
// @Tags announcement
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Success 200 {object} Response
// @Router /groups/{groupId}/announcements [get]
func (r *Router) announcements(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	if !r.repo.IsActiveMember(c.Request.Context(), gid, uid(c)) {
		Fail(c, 403, "FORBIDDEN", "not group member")
		return
	}
	page, err := r.repo.ListAnnouncements(c.Request.Context(), gid, queryInt(c, "cursor"), limit(c, 20))
	if err != nil {
		Fail(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	OK(c, toPageDTO(page, toAnnouncementDTO))
}

// createAnnouncement godoc
// @Summary 发布群公告
// @Tags announcement
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param groupId path int true "群ID"
// @Param body body CreateAnnouncementRequest true "公告内容"
// @Success 200 {object} Response
// @Router /groups/{groupId}/announcements [post]
func (r *Router) createAnnouncement(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	var req CreateAnnouncementRequest
	if !bindJSON(c, &req) {
		return
	}
	a, msg, err := r.svc.CreateAnnouncement(c.Request.Context(), gid, uid(c), req.Title, req.Content, req.Pinned)
	if err != nil {
		Fail(c, 403, "ANNOUNCEMENT_CREATE_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, toAnnouncementDTO(*a))
}

// updateAnnouncement godoc
// @Summary 编辑群公告
// @Tags announcement
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param groupId path int true "群ID"
// @Param announcementId path int true "公告ID"
// @Param body body UpdateAnnouncementRequest true "公告内容"
// @Success 200 {object} Response
// @Router /groups/{groupId}/announcements/{announcementId} [put]
func (r *Router) updateAnnouncement(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	aid, _ := paramInt(c, "announcementId")
	var req UpdateAnnouncementRequest
	if !bindJSON(c, &req) {
		return
	}
	a, msg, err := r.svc.UpdateAnnouncement(c.Request.Context(), gid, uid(c), aid, req.Title, req.Content, req.Pinned)
	if err != nil {
		Fail(c, 403, "ANNOUNCEMENT_UPDATE_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, toAnnouncementDTO(*a))
}

// deleteAnnouncement godoc
// @Summary 删除群公告
// @Tags announcement
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Param announcementId path int true "公告ID"
// @Success 200 {object} Response
// @Router /groups/{groupId}/announcements/{announcementId} [delete]
func (r *Router) deleteAnnouncement(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	aid, _ := paramInt(c, "announcementId")
	msg, err := r.svc.DeleteAnnouncement(c.Request.Context(), gid, uid(c), aid)
	if err != nil {
		Fail(c, 403, "ANNOUNCEMENT_DELETE_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"deleted": true})
}

// joinRequests godoc
// @Summary 加群审批列表（仅群主/管理员）
// @Tags join-request
// @Security BearerAuth
// @Param groupId path int true "群ID"
// @Param cursor query string false "游标"
// @Param limit query int false "数量"
// @Param status query string false "审批状态，默认 pending"
// @Success 200 {object} Response
// @Router /groups/{groupId}/join-requests [get]
func (r *Router) joinRequests(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	m, err := r.repo.GetMember(c.Request.Context(), gid, uid(c))
	if err != nil || (m.Role != domain.RoleOwner && m.Role != domain.RoleAdmin) {
		Fail(c, 403, "FORBIDDEN", "only owner/admin can view join requests")
		return
	}
	page, err := r.repo.ListJoinRequests(c.Request.Context(), gid, queryInt(c, "cursor"), limit(c, 30), c.DefaultQuery("status", domain.JoinPending))
	if err != nil {
		Fail(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	OK(c, toPageDTO(page, toJoinRequestDTO))
}

// approveJoinRequest godoc
// @Summary 通过加群审批
// @Tags join-request
// @Security BearerAuth
// @Success 200 {object} Response
// @Router /groups/{groupId}/join-requests/{requestId}/approve [post]
func (r *Router) approveJoinRequest(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	rid, _ := paramInt(c, "requestId")
	jr, msg, evt, err := r.svc.ApproveJoinRequest(c.Request.Context(), gid, uid(c), rid)
	if err != nil {
		Fail(c, 403, "APPROVE_JOIN_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	r.pushRealtimeEventIfDirect(c.Request.Context(), evt)
	OK(c, toJoinRequestDTO(*jr))
}

// rejectJoinRequest godoc
// @Summary 拒绝加群审批
// @Tags join-request
// @Security BearerAuth
// @Success 200 {object} Response
// @Router /groups/{groupId}/join-requests/{requestId}/reject [post]
func (r *Router) rejectJoinRequest(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	rid, _ := paramInt(c, "requestId")
	jr, evt, err := r.svc.RejectJoinRequest(c.Request.Context(), gid, uid(c), rid)
	if err != nil {
		Fail(c, 403, "REJECT_JOIN_FAILED", err.Error())
		return
	}
	r.pushRealtimeEventIfDirect(c.Request.Context(), evt)
	OK(c, toJoinRequestDTO(*jr))
}

func (r *Router) wsUpgrade(c *gin.Context) {
	token := c.Query("token")
	claims, err := auth.Parse(r.cfg.AuthSecret, token)
	if err != nil {
		c.AbortWithStatusJSON(401, gin.H{"type": "error", "data": gin.H{"code": "AUTH_TOKEN_INVALID", "message": err.Error(), "retryable": false}})
		return
	}
	if err := r.hub.Upgrade(c.Writer, c.Request, claims.UserID); err != nil {
		r.log.Warn("ws upgrade failed", zap.Error(err))
	}
}

func (r *Router) onWSMessage(client *ws.Client, env ws.Envelope) {
	// WS 入站消息没有 HTTP 中间件，这里为每条消息生成/透传 traceId，写入 context 贯穿后续链路。
	tid := env.TraceID
	if tid == "" {
		tid = id.New("trace")
	}
	ctx := logx.WithTrace(context.Background(), logx.Trace{TraceID: tid, RequestID: env.RequestID, UserID: client.UserID})
	switch env.Type {
	case "group_message_send":
		var req struct {
			GroupID         int64          `json:"groupId"`
			ClientMessageID string         `json:"clientMessageId"`
			MessageType     string         `json:"messageType"`
			Content         string         `json:"content"`
			MentionAll      bool           `json:"mentionAll"`
			MentionUserIDs  []int64        `json:"mentionUserIds"`
			Extra           map[string]any `json:"extra"`
		}
		if err := json.Unmarshal(env.Data, &req); err != nil {
			client.SendJSON("group_message_failed", env.RequestID, gin.H{"code": "BAD_REQUEST", "message": err.Error(), "retryable": false})
			return
		}
		msg, dup, err := r.svc.SendGroupMessage(ctx, service.SendMessageInput{GroupID: req.GroupID, SenderID: client.UserID, ClientMessageID: req.ClientMessageID, MessageType: req.MessageType, Content: req.Content, MentionAll: req.MentionAll, MentionUserIDs: req.MentionUserIDs, Extra: req.Extra})
		if err != nil {
			logx.From(ctx).Warn("group_message_send_failed",
				zap.String("event", "group_message_send_failed"), zap.Int64("groupId", req.GroupID),
				zap.String("clientMessageId", req.ClientMessageID), zap.String("code", codeFromErr(err)),
				zap.String("reason", err.Error()), zap.Bool("retryable", isRetryable(err)))
			client.SendJSON("group_message_failed", env.RequestID, gin.H{"clientMessageId": req.ClientMessageID, "code": codeFromErr(err), "message": err.Error(), "retryable": isRetryable(err)})
			return
		}
		logx.From(ctx).Info("group_message_ack_sent",
			zap.String("event", "group_message_ack_sent"), zap.Int64("groupId", msg.GroupID),
			zap.String("messageId", msg.MessageID), zap.String("clientMessageId", msg.ClientMessageID),
			zap.Int64("sequence", msg.Sequence), zap.Bool("duplicate", dup))
		client.SendJSON("group_message_ack", env.RequestID, gin.H{"messageId": msg.MessageID, "clientMessageId": msg.ClientMessageID, "groupId": msg.GroupID, "sequence": msg.Sequence, "status": "success", "duplicate": dup, "createdAt": msg.CreatedAt})
		r.pushIfDirect(ctx, msg)
	case "group_message_read":
		var req struct {
			GroupID          int64 `json:"groupId"`
			LastReadSequence int64 `json:"lastReadSequence"`
		}
		if err := json.Unmarshal(env.Data, &req); err != nil {
			client.SendJSON("error", env.RequestID, gin.H{"code": "BAD_REQUEST", "message": err.Error(), "retryable": false})
			return
		}
		if err := r.svc.UpdateReadPosition(ctx, req.GroupID, client.UserID, req.LastReadSequence); err != nil {
			code := "READ_FAILED"
			retryable := true
			if errors.Is(err, service.ErrReadSequenceRollback) {
				code = "READ_SEQUENCE_ROLLBACK"
				retryable = false
			} else if errors.Is(err, service.ErrForbidden) {
				code = "FORBIDDEN"
				retryable = false
			}
			client.SendJSON("error", env.RequestID, gin.H{"code": code, "message": err.Error(), "groupId": req.GroupID, "retryable": retryable})
			return
		}
		client.SendJSON("group_message_read_ack", env.RequestID, gin.H{"groupId": req.GroupID, "lastReadSequence": req.LastReadSequence})
	default:
		client.SendJSON("error", env.RequestID, gin.H{"code": "UNKNOWN_TYPE", "message": "unknown message type", "retryable": false})
	}
}

func codeFromErr(err error) string {
	if errors.Is(err, service.ErrMuted) {
		return "GROUP_MUTED"
	}
	if errors.Is(err, service.ErrRateLimited) {
		return "SLOW_MODE_LIMITED"
	}
	if errors.Is(err, service.ErrForbidden) {
		return "FORBIDDEN"
	}
	return "MESSAGE_SEND_FAILED"
}
func isRetryable(err error) bool {
	return !errors.Is(err, service.ErrMuted) && !errors.Is(err, service.ErrForbidden) && !errors.Is(err, service.ErrRateLimited)
}

func (r *Router) pushIfDirect(ctx context.Context, msg *domain.Message) {
	if msg == nil || r.cfg.KafkaEnabled {
		return
	}
	ids := r.memberIDs(ctx, msg.GroupID)
	r.hub.SendToUsers(ids, "group_message_receive", msg)
}

func (r *Router) pushEventIfDirect(ctx context.Context, groupID int64, eventType string, payload any) {
	if payload == nil || r.cfg.KafkaEnabled {
		return
	}
	ids := r.memberIDs(ctx, groupID)
	r.hub.SendToUsers(ids, eventType, payload)
}

func (r *Router) pushRealtimeEventIfDirect(ctx context.Context, evt *domain.RealtimeEvent) {
	if evt == nil || r.cfg.KafkaEnabled {
		return
	}
	r.hub.SendToUsers(evt.TargetUserIDs, evt.EventType, evt.Body)
}

func (r *Router) memberIDs(ctx context.Context, gid int64) []int64 {
	out := []int64{}
	var cursor int64
	for {
		ids, next, err := r.repo.ListActiveMemberIDs(ctx, gid, cursor, 1000)
		if err != nil {
			return out
		}
		out = append(out, ids...)
		if next == 0 {
			return out
		}
		cursor = next
	}
}

func (r *Router) internalPush(c *gin.Context) {
	// Delivery 通过 X-Trace-Id 头透传 traceId，这里还原到 context 以便推送日志归并到同一链路。
	if tid := c.GetHeader("X-Trace-Id"); tid != "" {
		c.Request = c.Request.WithContext(logx.WithTrace(c.Request.Context(), logx.Trace{TraceID: tid, RequestID: c.GetHeader("X-Request-Id")}))
	}
	var req InternalPushRequest
	if !bindJSON(c, &req) {
		return
	}
	var data any
	_ = json.Unmarshal(req.Data, &data)
	if req.Type == "" {
		req.Type = "group_message_receive"
	}
	targetUserCount := len(req.UserIDs)
	targetConnectionCount := len(req.ConnectionIDs)
	n := 0
	failedCount := 0
	if len(req.ConnectionIDs) > 0 {
		n = r.hub.SendToConnections(req.ConnectionIDs, req.Type, data)
		failedCount = len(req.ConnectionIDs) - n
	} else {
		n = r.hub.SendToUsers(req.UserIDs, req.Type, data)
	}
	logx.From(c.Request.Context()).Info("ws_push",
		zap.String("event", "ws_push"), zap.String("type", req.Type),
		zap.String("serverId", r.cfg.ServerID), zap.Int("targetUserCount", targetUserCount),
		zap.Int("targetConnectionCount", targetConnectionCount),
		zap.Int("successCount", n), zap.Int("failedCount", failedCount))
	OK(c, gin.H{"pushed": n})
}
