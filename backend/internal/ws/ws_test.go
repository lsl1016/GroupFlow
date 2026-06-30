package ws

import (
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	"groupflow/backend/internal/config"
)

func TestHubSendToConnectionsTargetsOnlyRequestedConnections(t *testing.T) {
	hub := NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	conn1 := &Client{ID: "conn-1", UserID: 1001, Hub: hub, Send: make(chan []byte, 1)}
	conn2 := &Client{ID: "conn-2", UserID: 1001, Hub: hub, Send: make(chan []byte, 1)}
	conn3 := &Client{ID: "conn-3", UserID: 1002, Hub: hub, Send: make(chan []byte, 1)}

	hub.clients[conn1.ID] = conn1
	hub.clients[conn2.ID] = conn2
	hub.clients[conn3.ID] = conn3

	pushed := hub.SendToConnections([]string{"conn-1", "conn-3", "missing"}, "group_message_receive", map[string]any{"messageId": "msg-1"})
	if pushed != 2 {
		t.Fatalf("expected 2 pushes, got %d", pushed)
	}

	assertMessageType(t, conn1.Send, "group_message_receive")
	assertChannelEmpty(t, conn2.Send)
	assertMessageType(t, conn3.Send, "group_message_receive")
}

func TestHubSendToUsersStillFansOutToAllLocalUserConnections(t *testing.T) {
	hub := NewHub(config.Config{ServerID: "ws-server-01"}, nil, zap.NewNop())
	conn1 := &Client{ID: "conn-1", UserID: 1001, Hub: hub, Send: make(chan []byte, 1)}
	conn2 := &Client{ID: "conn-2", UserID: 1001, Hub: hub, Send: make(chan []byte, 1)}
	conn3 := &Client{ID: "conn-3", UserID: 1002, Hub: hub, Send: make(chan []byte, 1)}

	hub.userClients[conn1.UserID] = map[string]*Client{conn1.ID: conn1, conn2.ID: conn2}
	hub.userClients[conn3.UserID] = map[string]*Client{conn3.ID: conn3}

	pushed := hub.SendToUsers([]int64{1001}, "group_message_receive", map[string]any{"messageId": "msg-2"})
	if pushed != 2 {
		t.Fatalf("expected 2 pushes, got %d", pushed)
	}

	assertMessageType(t, conn1.Send, "group_message_receive")
	assertMessageType(t, conn2.Send, "group_message_receive")
	assertChannelEmpty(t, conn3.Send)
}

func assertMessageType(t *testing.T, ch <-chan []byte, want string) {
	t.Helper()
	select {
	case raw := <-ch:
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if got, _ := payload["type"].(string); got != want {
			t.Fatalf("expected type %q, got %q", want, got)
		}
	default:
		t.Fatalf("expected message in channel")
	}
}

func assertChannelEmpty(t *testing.T, ch <-chan []byte) {
	t.Helper()
	select {
	case raw := <-ch:
		t.Fatalf("expected empty channel, got message %s", string(raw))
	default:
	}
}
