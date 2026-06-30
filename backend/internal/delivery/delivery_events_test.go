package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.uber.org/zap"

	"groupflow/backend/internal/config"
)

func TestHandleStructuredRealtimeEventPushesTargetUserConnections(t *testing.T) {
	var (
		mu       sync.Mutex
		captured []struct {
			ConnectionIDs []string       `json:"connectionIds"`
			Type          string         `json:"type"`
			Data          map[string]any `json:"data"`
		}
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		var req struct {
			ConnectionIDs []string       `json:"connectionIds"`
			Type          string         `json:"type"`
			Data          map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}
		captured = append(captured, req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"pushed":2}}`))
	}))
	defer server.Close()

	consumer := &Consumer{cfg: config.Config{}, log: zap.NewNop(), http: server.Client()}
	consumer.routeResolver = func(ctx context.Context, userIDs []int64) (map[string][]string, int, error) {
		return map[string][]string{"ws-a": {"conn-a1"}, "ws-b": {"conn-b1"}}, 2, nil
	}
	consumer.pushURLResolver = func(ctx context.Context, serverID string) (string, error) {
		return server.URL, nil
	}
	evt := groupEvent{EventID: "evt-1", EventType: "group_join_request_approved", GroupID: 10001, GroupType: "large"}
	payload, err := json.Marshal(map[string]any{"targetUserIds": []int64{30001, 30002}, "body": map[string]any{"requestId": 9001}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	evt.Payload = payload

	if err := consumer.handleStructuredEvent(context.Background(), evt); err != nil {
		t.Fatalf("handleStructuredEvent returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("expected 2 push requests, got %d", len(captured))
	}
	allConnections := make([]string, 0, 2)
	for _, req := range captured {
		if req.Type != "group_join_request_approved" {
			t.Fatalf("expected event type group_join_request_approved, got %q", req.Type)
		}
		if got, _ := req.Data["requestId"].(float64); got != 9001 {
			t.Fatalf("expected requestId 9001, got %#v", req.Data)
		}
		allConnections = append(allConnections, req.ConnectionIDs...)
	}
	assertStringSet(t, allConnections, []string{"conn-a1", "conn-b1"})
}

func TestHandleStructuredRealtimeEventRejectsMalformedPayload(t *testing.T) {
	consumer := &Consumer{cfg: config.Config{}, log: zap.NewNop(), http: &http.Client{}}
	consumer.routeResolver = func(ctx context.Context, userIDs []int64) (map[string][]string, int, error) {
		return nil, 0, nil
	}
	evt := groupEvent{EventID: "evt-2", EventType: "group_member_kicked", GroupID: 10001, GroupType: "large", Payload: []byte(`{"targetUserIds":`)}
	if err := consumer.handleStructuredEvent(context.Background(), evt); err == nil {
		t.Fatalf("expected malformed payload error")
	}
}
