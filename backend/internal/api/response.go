package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"groupflow/backend/pkg/id"
	"groupflow/backend/pkg/logx"
)

const traceIDKey = "traceId"

// Response 统一响应体。errNo=0 表示成功，非 0 为业务错误码；errMsg 为可读信息；
// traceId 用于链路追踪；data 为业务数据。
type Response struct {
	ErrNo   int    `json:"errNo"`
	ErrMsg  string `json:"errMsg"`
	TraceId string `json:"traceId"`
	Data    any    `json:"data"`
}

// errNoMap 将既有字符串错误码映射为整数业务错误码，集中维护。
// 0 保留给成功；未登记的字符串码统一映射为 -1。
var errNoMap = map[string]int{
	"OK": 0,

	// 通用 / 鉴权 (100xx)
	"BAD_REQUEST":        10001,
	"AUTH_FAILED":        10002,
	"AUTH_TOKEN_INVALID": 10003,
	"FORBIDDEN":          10004,
	"USER_NOT_FOUND":     10005,
	"INTERNAL_ERROR":     10006,

	// 群 (200xx)
	"CREATE_GROUP_FAILED":    20001,
	"GROUP_NOT_FOUND":        20002,
	"JOIN_GROUP_FAILED":      20003,
	"LEAVE_GROUP_FAILED":     20004,
	"DISMISS_FAILED":         20005,
	"UPDATE_SETTINGS_FAILED": 20006,

	// 成员 (210xx)
	"SET_ROLE_FAILED": 21001,
	"KICK_FAILED":     21002,
	"MUTE_FAILED":     21003,
	"UNMUTE_FAILED":   21004,

	// 消息 (220xx)
	"RECALL_FAILED":          22001,
	"READ_FAILED":            22002,
	"MENTION_READ_FAILED":    22003,
	"READ_SEQUENCE_ROLLBACK": 22004,

	// 公告 (230xx)
	"ANNOUNCEMENT_CREATE_FAILED": 23001,
	"ANNOUNCEMENT_UPDATE_FAILED": 23002,
	"ANNOUNCEMENT_DELETE_FAILED": 23003,

	// 加群审批 (240xx)
	"APPROVE_JOIN_FAILED": 24001,
	"REJECT_JOIN_FAILED":  24002,

	// 搜索 (250xx)
	"SEARCH_FAILED":   25001,
	"SEARCH_DISABLED": 25002,
}

func errNoOf(code string) int {
	if n, ok := errNoMap[code]; ok {
		return n
	}
	return -1
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{ErrNo: 0, ErrMsg: "succ", TraceId: getTraceId(c), Data: data})
}

// Fail 渲染失败响应：保留语义化 HTTP 状态码，body 携带映射后的整数 errNo 与 errMsg。
func Fail(c *gin.Context, httpCode int, code, msg string) {
	c.JSON(httpCode, Response{ErrNo: errNoOf(code), ErrMsg: msg, TraceId: getTraceId(c), Data: nil})
}

func getTraceId(c *gin.Context) string {
	if v, ok := c.Get(traceIDKey); ok {
		if s, _ := v.(string); s != "" {
			return s
		}
	}
	return c.GetHeader("X-Request-Id")
}

// ensureTraceID 解析/生成 traceId（优先 X-Trace-Id，其次 X-Request-Id），写入 gin 上下文、
// 响应头与 request.Context，使 traceId 随 context 贯穿到 service/repo/redis/kafka 各层。
func ensureTraceID(c *gin.Context) string {
	tid := c.GetHeader("X-Trace-Id")
	if tid == "" {
		tid = c.GetHeader("X-Request-Id")
	}
	if tid == "" {
		tid = id.New("trace")
	}
	c.Set(traceIDKey, tid)
	c.Header("X-Trace-Id", tid)
	ctx := logx.WithTrace(c.Request.Context(), logx.Trace{TraceID: tid, RequestID: c.GetHeader("X-Request-Id")})
	c.Request = c.Request.WithContext(ctx)
	return tid
}

// setUserContext 在鉴权通过后补充 userId，使后续日志携带用户维度。
func setUserContext(c *gin.Context, userID int64) {
	t := logx.TraceOf(c.Request.Context())
	t.UserID = userID
	c.Request = c.Request.WithContext(logx.WithTrace(c.Request.Context(), t))
}
