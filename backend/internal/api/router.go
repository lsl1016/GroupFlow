package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
	"groupflow/backend/internal/repo"
	"groupflow/backend/internal/service"
	"groupflow/backend/internal/ws"
	"groupflow/backend/pkg/auth"
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
	g.GET("/ws", r.wsUpgrade)
	g.GET("/api/ws", r.wsUpgrade)
	v1 := g.Group("/api/v1")
	v1.GET("/health", func(c *gin.Context) { OK(c, gin.H{"status": "ok", "time": time.Now().Format(time.RFC3339)}) })
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
	groups.POST("/:groupId/read", r.read)
	g.POST("/internal/push", r.internalPush)

	hub.OnMessage = r.onWSMessage
	return g
}

func (r *Router) traceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		r.log.Info("http", zap.String("method", c.Request.Method), zap.String("path", c.Request.URL.Path), zap.Int("status", c.Writer.Status()), zap.Duration("cost", time.Since(start)))
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
		c.Next()
	}
}

func (r *Router) parseBearer(h string) (*auth.Claims, error) {
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, errors.New("missing bearer token")
	}
	return auth.Parse(r.cfg.AuthSecret, strings.TrimPrefix(h, "Bearer "))
}
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

func (r *Router) login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, 400, "BAD_REQUEST", err.Error())
		return
	}
	u, t, err := r.svc.Login(c.Request.Context(), req.Username)
	if err != nil {
		Fail(c, 400, "AUTH_FAILED", err.Error())
		return
	}
	OK(c, gin.H{"userId": u.ID, "username": u.Username, "nickname": u.Nickname, "avatar": u.Avatar, "token": t})
}
func (r *Router) me(c *gin.Context) {
	u, err := r.repo.GetUser(c.Request.Context(), uid(c))
	if err != nil {
		Fail(c, 404, "USER_NOT_FOUND", err.Error())
		return
	}
	OK(c, u)
}
func (r *Router) listGroups(c *gin.Context) {
	page, err := r.repo.ListGroupsForUser(c.Request.Context(), uid(c), queryInt(c, "cursor"), limit(c, 30))
	if err != nil {
		Fail(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	OK(c, page)
}
func (r *Router) createGroup(c *gin.Context) {
	var req struct {
		Name, Description, Avatar, JoinMode, GroupType string
		MaxMemberCount                                 int
		SlowModeSeconds                                int
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, 400, "BAD_REQUEST", err.Error())
		return
	}
	g, msg, err := r.svc.CreateGroup(c.Request.Context(), uid(c), req.Name, req.Description, req.Avatar, req.JoinMode, req.GroupType, req.MaxMemberCount, req.SlowModeSeconds)
	if err != nil {
		Fail(c, 400, "CREATE_GROUP_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, g)
}
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
	OK(c, gin.H{"group": g, "myMember": m, "onlineUserIds": r.hub.OnlineUserIDs()})
}
func (r *Router) joinGroup(c *gin.Context) {
	gid, err := paramInt(c, "groupId")
	if err != nil {
		Fail(c, 400, "BAD_REQUEST", "invalid groupId")
		return
	}
	msg, err := r.svc.JoinGroup(c.Request.Context(), gid, uid(c))
	if err != nil {
		Fail(c, 400, "JOIN_GROUP_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"joined": true})
}
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
	OK(c, page)
}
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
func (r *Router) updateSettings(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	var req struct {
		MuteAll         *bool   `json:"muteAll"`
		SlowModeSeconds *int    `json:"slowModeSeconds"`
		GroupType       *string `json:"groupType"`
		MaxMemberCount  *int    `json:"maxMemberCount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, 400, "BAD_REQUEST", err.Error())
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
func (r *Router) setRole(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	tid, _ := paramInt(c, "userId")
	var req struct {
		Role string `json:"role"`
	}
	_ = c.ShouldBindJSON(&req)
	msg, err := r.svc.SetMemberRole(c.Request.Context(), gid, uid(c), tid, req.Role)
	if err != nil {
		Fail(c, 403, "SET_ROLE_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"updated": true})
}
func (r *Router) kickMember(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	tid, _ := paramInt(c, "userId")
	msg, err := r.svc.KickMember(c.Request.Context(), gid, uid(c), tid)
	if err != nil {
		Fail(c, 403, "KICK_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	r.hub.SendToUsers([]int64{tid}, "group_member_kicked", gin.H{"groupId": gid, "userId": tid})
	OK(c, gin.H{"kicked": true})
}
func (r *Router) muteMember(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	tid, _ := paramInt(c, "userId")
	var req struct {
		Seconds int    `json:"seconds"`
		Reason  string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	msg, err := r.svc.MuteMember(c.Request.Context(), gid, uid(c), tid, req.Seconds, req.Reason)
	if err != nil {
		Fail(c, 403, "MUTE_FAILED", err.Error())
		return
	}
	r.pushIfDirect(c.Request.Context(), msg)
	OK(c, gin.H{"muted": true})
}
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
	OK(c, page)
}
func (r *Router) read(c *gin.Context) {
	gid, _ := paramInt(c, "groupId")
	var req struct {
		LastReadSequence int64 `json:"lastReadSequence"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := r.repo.UpdateRead(c.Request.Context(), gid, uid(c), req.LastReadSequence); err != nil {
		Fail(c, 500, "READ_FAILED", err.Error())
		return
	}
	OK(c, gin.H{"lastReadSequence": req.LastReadSequence})
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
		msg, dup, err := r.svc.SendGroupMessage(context.Background(), service.SendMessageInput{GroupID: req.GroupID, SenderID: client.UserID, ClientMessageID: req.ClientMessageID, MessageType: req.MessageType, Content: req.Content, MentionAll: req.MentionAll, MentionUserIDs: req.MentionUserIDs, Extra: req.Extra})
		if err != nil {
			client.SendJSON("group_message_failed", env.RequestID, gin.H{"clientMessageId": req.ClientMessageID, "code": codeFromErr(err), "message": err.Error(), "retryable": isRetryable(err)})
			return
		}
		client.SendJSON("group_message_ack", env.RequestID, gin.H{"messageId": msg.MessageID, "clientMessageId": msg.ClientMessageID, "groupId": msg.GroupID, "sequence": msg.Sequence, "status": "success", "duplicate": dup, "createdAt": msg.CreatedAt})
		r.pushIfDirect(context.Background(), msg)
	case "group_message_read":
		var req struct {
			GroupID          int64 `json:"groupId"`
			LastReadSequence int64 `json:"lastReadSequence"`
		}
		_ = json.Unmarshal(env.Data, &req)
		_ = r.repo.UpdateRead(context.Background(), req.GroupID, client.UserID, req.LastReadSequence)
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
	if msg == nil {
		return
	}
	if r.cfg.KafkaEnabled {
		return
	}
	ids := r.memberIDs(ctx, msg.GroupID)
	r.hub.SendToUsers(ids, "group_message_receive", msg)
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
	var req struct {
		UserIDs []int64         `json:"userIds"`
		Type    string          `json:"type"`
		Data    json.RawMessage `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, 400, "BAD_REQUEST", err.Error())
		return
	}
	var data any
	_ = json.Unmarshal(req.Data, &data)
	if req.Type == "" {
		req.Type = "group_message_receive"
	}
	n := r.hub.SendToUsers(req.UserIDs, req.Type, data)
	OK(c, gin.H{"pushed": n})
}

var _ = sql.ErrNoRows
