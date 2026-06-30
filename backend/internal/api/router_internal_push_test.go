package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/ws"
)

func TestInternalPushTargetsExplicitConnections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := ws.NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	r := &Router{cfg: config.Config{ServerID: "ws-server-01"}, hub: hub, log: zap.NewNop()}
	server := newInternalPushTestServer(t, hub, r)
	defer server.Close()

	conn1, connID1 := mustDialWS(t, server, 1001)
	defer conn1.Close()
	conn2, _ := mustDialWS(t, server, 1001)
	defer conn2.Close()

	payload := []byte(`{"messageId":"msg-1"}`)
	body, err := json.Marshal(InternalPushRequest{ConnectionIDs: []string{connID1}, Type: "group_message_receive", Data: payload})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp := doInternalPush(t, server, body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	assertNextWSType(t, conn1, "group_message_receive")
	assertNoWSMessage(t, conn2)
}

func TestInternalPushFallsBackToUserIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := ws.NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	r := &Router{cfg: config.Config{ServerID: "ws-server-01"}, hub: hub, log: zap.NewNop()}
	server := newInternalPushTestServer(t, hub, r)
	defer server.Close()

	conn1, _ := mustDialWS(t, server, 1001)
	defer conn1.Close()
	conn2, _ := mustDialWS(t, server, 1001)
	defer conn2.Close()
	conn3, _ := mustDialWS(t, server, 1002)
	defer conn3.Close()

	payload := []byte(`{"messageId":"msg-2"}`)
	body, err := json.Marshal(InternalPushRequest{UserIDs: []int64{1001}, Type: "group_message_receive", Data: payload})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp := doInternalPush(t, server, body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	assertNextWSType(t, conn1, "group_message_receive")
	assertNextWSType(t, conn2, "group_message_receive")
	assertNoWSMessage(t, conn3)
}

func TestInternalPushPrefersConnectionIDsWhenBothTargetsProvided(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := ws.NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	r := &Router{cfg: config.Config{ServerID: "ws-server-01"}, hub: hub, log: zap.NewNop()}
	server := newInternalPushTestServer(t, hub, r)
	defer server.Close()

	conn1, connID1 := mustDialWS(t, server, 1001)
	defer conn1.Close()
	conn2, _ := mustDialWS(t, server, 1001)
	defer conn2.Close()
	conn3, _ := mustDialWS(t, server, 1002)
	defer conn3.Close()

	payload := []byte(`{"messageId":"msg-3"}`)
	body, err := json.Marshal(InternalPushRequest{UserIDs: []int64{1002}, ConnectionIDs: []string{connID1}, Type: "group_message_receive", Data: payload})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp := doInternalPush(t, server, body)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	assertNextWSType(t, conn1, "group_message_receive")
	assertNoWSMessage(t, conn2)
	assertNoWSMessage(t, conn3)
}

func newInternalPushTestServer(t *testing.T, hub *ws.Hub, router *Router) *httptest.Server {
	t.Helper()
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
	engine.POST("/internal/push", router.internalPush)
	return httptest.NewServer(engine)
}

func mustDialWS(t *testing.T, server *httptest.Server, userID int64) (*websocket.Conn, string) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/ws/%d", userID)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	msgType, err := readEnvelopeType(conn)
	if err != nil {
		conn.Close()
		t.Fatalf("read connected event: %v", err)
	}
	if msgType.Type != "connection_connected" {
		conn.Close()
		t.Fatalf("expected connection_connected, got %s", msgType.Type)
	}
	var data struct {
		ConnectionID string `json:"connectionId"`
	}
	if err := json.Unmarshal(msgType.Data, &data); err != nil {
		conn.Close()
		t.Fatalf("unmarshal connection data: %v", err)
	}
	return conn, data.ConnectionID
}

func doInternalPush(t *testing.T, server *httptest.Server, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/internal/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	http.DefaultServeMux = http.NewServeMux()
	engine := gin.New()
	_ = engine
	resp, err := http.Post(server.URL+"/internal/push", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post internal push: %v", err)
	}
	defer resp.Body.Close()
	rec.Code = resp.StatusCode
	_, _ = rec.Body.ReadFrom(resp.Body)
	return rec
}

func readEnvelopeType(conn *websocket.Conn) (ws.Envelope, error) {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return ws.Envelope{}, err
	}
	var env ws.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ws.Envelope{}, err
	}
	return env, nil
}

func assertNextWSType(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	env, err := readEnvelopeType(conn)
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	if env.Type != want {
		t.Fatalf("expected websocket type %q, got %q", want, env.Type)
	}
}

func assertNoWSMessage(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("expected no websocket message")
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return
	}
	if websocket.IsUnexpectedCloseError(err) {
		t.Fatalf("unexpected websocket close: %v", err)
	}
}
