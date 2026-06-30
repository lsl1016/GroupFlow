package api

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
	"groupflow/backend/internal/ws"
)

func TestPushRealtimeEventDirectModeDeliversToTargetConnections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := ws.NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	r := &Router{cfg: config.Config{ServerID: "ws-server-01", KafkaEnabled: false}, hub: hub, log: zap.NewNop()}
	server := newReadWSTestServer(t, hub, r)
	defer server.Close()

	conn, _ := mustDialWS(t, server, 1001)
	defer conn.Close()

	evt := &domain.RealtimeEvent{EventType: "group_member_kicked", GroupID: 10001, TargetUserIDs: []int64{1001}, Body: map[string]any{"userId": 1001}}
	r.pushRealtimeEventIfDirect(context.TODO(), evt)

	assertNextWSType(t, conn, "group_member_kicked")
}

func TestPushRealtimeEventKafkaModeSkipsLocalPush(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := ws.NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	r := &Router{cfg: config.Config{ServerID: "ws-server-01", KafkaEnabled: true}, hub: hub, log: zap.NewNop()}
	server := newReadWSTestServer(t, hub, r)
	defer server.Close()

	conn, _ := mustDialWS(t, server, 1001)
	defer conn.Close()

	evt := &domain.RealtimeEvent{EventType: "group_member_kicked", GroupID: 10001, TargetUserIDs: []int64{1001}, Body: map[string]any{"userId": 1001}}
	r.pushRealtimeEventIfDirect(context.TODO(), evt)

	assertNoWSMessage(t, conn)
}

func TestPushEventKafkaModeSkipsLocalPush(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := ws.NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	r := &Router{cfg: config.Config{ServerID: "ws-server-01", KafkaEnabled: true}, hub: hub, log: zap.NewNop()}
	server := newReadWSTestServer(t, hub, r)
	defer server.Close()

	conn, _ := mustDialWS(t, server, 1001)
	defer conn.Close()

	r.pushEventIfDirect(context.TODO(), 10001, "group_message_recalled", map[string]any{"messageId": "msg-1"})

	assertNoWSMessage(t, conn)
}
