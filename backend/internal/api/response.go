package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type Response struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"requestId"`
	Timestamp int64  `json:"timestamp"`
}

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Code: "OK", Message: "success", Data: data, RequestID: requestID(c), Timestamp: time.Now().UnixMilli()})
}
func Fail(c *gin.Context, httpCode int, code, msg string) {
	c.JSON(httpCode, Response{Code: code, Message: msg, Data: nil, RequestID: requestID(c), Timestamp: time.Now().UnixMilli()})
}
func requestID(c *gin.Context) string {
	if v := c.GetHeader("X-Request-Id"); v != "" {
		return v
	}
	return ""
}
