package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
	"groupflow/backend/internal/service"
	"groupflow/backend/internal/ws"
)

func newReadWSTestServer(t *testing.T, hub *ws.Hub, router *Router) *httptest.Server {
	t.Helper()
	hub.OnMessage = router.onWSMessage
	engine := gin.New()
	engine.GET("/ws/:userId", func(c *gin.Context) {
		var userID int64
		if _, err := fmt.Sscan(c.Param("userId"), &userID); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		if err := hub.Upgrade(c.Writer, c.Request, userID); err != nil {
			c.Status(http.StatusInternalServerError)
		}
	})
	return httptest.NewServer(engine)
}

func sendWSRead(t *testing.T, conn *websocket.Conn, groupID, lastRead int64) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"groupId": groupID, "lastReadSequence": lastRead})
	env := map[string]any{"type": "group_message_read", "requestId": "req-read-1", "data": json.RawMessage(data)}
	raw, _ := json.Marshal(env)
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("write read message: %v", err)
	}
}

func TestWSReadRollbackReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := ws.NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	svc := &service.Service{Cfg: config.Config{}, Log: zap.NewNop()}
	svc.SetReadHooksForTest(
		func(ctx context.Context, groupID, userID int64) (*domain.Member, error) {
			return &domain.Member{GroupID: groupID, UserID: userID, Status: domain.StatusNormal, LastReadSequence: 100}, nil
		},
		func(ctx context.Context, groupID, userID, lastRead int64) error {
			t.Fatalf("rollback must not persist")
			return nil
		},
	)
	r := &Router{cfg: config.Config{ServerID: "ws-server-01"}, svc: svc, hub: hub, log: zap.NewNop()}
	server := newReadWSTestServer(t, hub, r)
	defer server.Close()

	conn, _ := mustDialWS(t, server, 1001)
	defer conn.Close()

	sendWSRead(t, conn, 10001, 50)
	env, err := readEnvelopeType(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if env.Type != "error" {
		t.Fatalf("expected error response, got %q", env.Type)
	}
	var data struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("unmarshal error data: %v", err)
	}
	if data.Code != "READ_SEQUENCE_ROLLBACK" {
		t.Fatalf("expected READ_SEQUENCE_ROLLBACK, got %q", data.Code)
	}
}

func TestWSReadForwardReturnsAck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := ws.NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	svc := &service.Service{Cfg: config.Config{}, Log: zap.NewNop()}
	var persistedTo int64
	svc.SetReadHooksForTest(
		func(ctx context.Context, groupID, userID int64) (*domain.Member, error) {
			return &domain.Member{GroupID: groupID, UserID: userID, Status: domain.StatusNormal, LastReadSequence: 100}, nil
		},
		func(ctx context.Context, groupID, userID, lastRead int64) error {
			persistedTo = lastRead
			return nil
		},
	)
	r := &Router{cfg: config.Config{ServerID: "ws-server-01"}, svc: svc, hub: hub, log: zap.NewNop()}
	server := newReadWSTestServer(t, hub, r)
	defer server.Close()

	conn, _ := mustDialWS(t, server, 1001)
	defer conn.Close()

	sendWSRead(t, conn, 10001, 130)
	env, err := readEnvelopeType(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if env.Type != "group_message_read_ack" {
		t.Fatalf("expected group_message_read_ack, got %q", env.Type)
	}
	if persistedTo != 130 {
		t.Fatalf("expected persist to 130, got %d", persistedTo)
	}
}
