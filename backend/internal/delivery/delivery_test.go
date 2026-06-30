package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"groupflow/backend/internal/config"
)

func TestFlattenUniqueConnectionIDsDeduplicatesAndSkipsEmpty(t *testing.T) {
	got := flattenUniqueConnectionIDs([][]string{{"conn-a1", "", "conn-a2"}, {"conn-a2", "conn-b1"}})
	assertStringSet(t, got, []string{"conn-a1", "conn-a2", "conn-b1"})
}

func TestGroupConnectionsByServerSkipsUnresolvedConnections(t *testing.T) {
	routes := groupConnectionsByServer([]string{"conn-a1", "conn-stale", "conn-b1", "conn-a2"}, map[string]string{
		"conn-a1": "ws-a",
		"conn-a2": "ws-a",
		"conn-b1": "ws-b",
	})
	if len(routes) != 2 {
		t.Fatalf("expected 2 server groups, got %d", len(routes))
	}
	assertStringSet(t, routes["ws-a"], []string{"conn-a1", "conn-a2"})
	assertStringSet(t, routes["ws-b"], []string{"conn-b1"})
}

func TestResolveOnlineRoutesReturnsErrorWhenRedisUnavailable(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0", DialTimeout: 20 * time.Millisecond, ReadTimeout: 20 * time.Millisecond, WriteTimeout: 20 * time.Millisecond})
	defer redisClient.Close()

	c := &Consumer{cfg: config.Config{}, redis: redisClient, log: zap.NewNop(), http: &http.Client{Timeout: time.Second}}
	if _, _, err := c.resolveOnlineRoutes(context.Background(), []int64{1001}); err == nil {
		t.Fatalf("expected redis routing error")
	}
}

func TestPushURLRejectsRemoteServerFallbackWithoutRegistry(t *testing.T) {
	c := &Consumer{cfg: config.Config{InternalPushURL: "http://localhost:8080/internal/push"}, log: zap.NewNop(), http: &http.Client{Timeout: time.Second}}
	if _, err := c.pushURL(context.Background(), "ws-remote"); err == nil {
		t.Fatalf("expected missing push url error for remote server")
	}

	url, err := c.pushURL(context.Background(), "")
	if err != nil {
		t.Fatalf("expected local fallback to succeed, got %v", err)
	}
	if url != "http://localhost:8080/internal/push" {
		t.Fatalf("expected fallback url, got %q", url)
	}
}

func TestPushUsesConnectionIDsPayload(t *testing.T) {
	var (
		mu       sync.Mutex
		captured struct {
			ConnectionIDs []string `json:"connectionIds"`
			UserIDs       []int64  `json:"userIds"`
			Type          string   `json:"type"`
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
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"pushed":2}}`))
	}))
	defer server.Close()

	c := &Consumer{cfg: config.Config{}, log: zap.NewNop(), http: server.Client()}
	pushed, err := c.push(context.Background(), server.URL, []string{"conn-a1", "conn-b1"}, "group_message_receive", map[string]any{"messageId": "msg-1"})
	if err != nil {
		t.Fatalf("push returned error: %v", err)
	}
	if pushed != 2 {
		t.Fatalf("expected pushed=2, got %d", pushed)
	}

	mu.Lock()
	defer mu.Unlock()
	assertStringSet(t, captured.ConnectionIDs, []string{"conn-a1", "conn-b1"})
	if len(captured.UserIDs) != 0 {
		t.Fatalf("expected empty userIds payload, got %#v", captured.UserIDs)
	}
	if captured.Type != "group_message_receive" {
		t.Fatalf("expected message type group_message_receive, got %q", captured.Type)
	}
}

func TestPushReturnsErrorOnMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":`))
	}))
	defer server.Close()

	c := &Consumer{cfg: config.Config{}, log: zap.NewNop(), http: server.Client()}
	if _, err := c.push(context.Background(), server.URL, []string{"conn-a1"}, "group_message_receive", map[string]any{"messageId": "msg-1"}); err == nil {
		t.Fatalf("expected decode error from malformed response")
	}
}

func assertStringSet(t *testing.T, got []string, want []string) {
	t.Helper()
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	if len(gotCopy) != len(wantCopy) {
		t.Fatalf("expected %v, got %v", wantCopy, gotCopy)
	}
	for i := range gotCopy {
		if gotCopy[i] != wantCopy[i] {
			t.Fatalf("expected %v, got %v", wantCopy, gotCopy)
		}
	}
}
